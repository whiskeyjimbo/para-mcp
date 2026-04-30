// Package vault implements the LocalVault filesystem adapter and NoteService.
package vault

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/whiskeyjimbo/paras/internal/actor"
	"github.com/whiskeyjimbo/paras/internal/domain"
	"github.com/whiskeyjimbo/paras/internal/index"
)

// errInternal is returned when AllowedScopes is nil (programmer error).
var errInternal = errors.New("internal: AllowedScopes must not be nil")

type LocalVault struct {
	scope string
	root  string
	caps  domain.Capabilities

	actors *actor.Pool
	idx    *index.Index
	w      *watcher

	mu    sync.RWMutex
	notes map[string]domain.NoteSummary // indexKey -> summary
}

// New creates a LocalVault rooted at root with the given scope.
func New(scope, root string, idxCfg index.Config) (*LocalVault, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create vault root: %w", err)
	}
	caseSensitive := probeCaseSensitivity(root)
	v := &LocalVault{
		scope:  scope,
		root:   root,
		actors: actor.New(),
		idx:    index.New(idxCfg),
		notes:  make(map[string]domain.NoteSummary),
		caps: domain.Capabilities{
			Writable:      true,
			SoftDelete:    true,
			CaseSensitive: caseSensitive,
		},
	}
	if err := v.scanVault(); err != nil {
		return nil, fmt.Errorf("scan vault: %w", err)
	}
	v.w = newWatcher(v)
	v.w.start()
	return v, nil
}

// Close shuts down background goroutines.
func (v *LocalVault) Close() {
	v.w.close()
	v.actors.Close()
	v.idx.Close()
}

func (v *LocalVault) Scope() domain.ScopeID             { return v.scope }
func (v *LocalVault) Capabilities() domain.Capabilities { return v.caps }

func (v *LocalVault) Get(_ context.Context, path string) (domain.Note, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.Note{}, err
	}
	return v.readNote(np.Storage)
}

// Create returns ErrConflict if the path already exists (enforced atomically via O_EXCL).
func (v *LocalVault) Create(ctx context.Context, in domain.CreateInput) (domain.NoteSummary, error) {
	np, err := v.normalizePath(in.Path)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	var summary domain.NoteSummary
	err = v.actors.Do(ctx, v.scope, np.Storage, func() error {
		absPath := filepath.Join(v.root, np.Storage)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return err
		}
		in.FrontMatter.CreatedAt = time.Now().UTC()
		in.FrontMatter.UpdatedAt = in.FrontMatter.CreatedAt
		in.FrontMatter.Tags = normalizeTags(in.FrontMatter.Tags)
		in.FrontMatter.Status = normalizeStatus(in.FrontMatter.Status)
		if domain.GetNoteID(in.FrontMatter) == "" {
			domain.SetNoteID(&in.FrontMatter, domain.MintNoteID())
		}
		data, err := formatNote(in.FrontMatter, in.Body)
		if err != nil {
			return err
		}
		// O_EXCL makes the existence check atomic, eliminating the TOCTOU race.
		f, err := os.OpenFile(absPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if os.IsExist(err) {
				return domain.ErrConflict
			}
			return err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			return werr
		}
		if cerr != nil {
			return cerr
		}
		note, err := v.readNote(np.Storage)
		if err != nil {
			return err
		}
		summary = v.noteToSummary(note)
		v.upsertNoteLocked(np.IndexKey, summary)
		v.idx.Add(summaryToDoc(summary, in.Body))
		return nil
	})
	return summary, err
}

// UpdateBody replaces the body of an existing note, checking the ETag.
func (v *LocalVault) UpdateBody(ctx context.Context, path, body, ifMatch string) (domain.NoteSummary, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	var summary domain.NoteSummary
	err = v.actors.Do(ctx, v.scope, np.Storage, func() error {
		note, err := v.readNote(np.Storage)
		if err != nil {
			return err
		}
		if ifMatch != "" && note.ETag != ifMatch {
			return domain.ErrConflict
		}
		note.FrontMatter.UpdatedAt = time.Now().UTC()
		note.Body = body
		note.ETag = domain.ComputeETag(note.FrontMatter, body)
		data, err := formatNote(note.FrontMatter, body)
		if err != nil {
			return err
		}
		absPath := filepath.Join(v.root, np.Storage)
		if err := os.WriteFile(absPath, data, 0o644); err != nil {
			return err
		}
		summary = v.noteToSummary(note)
		v.upsertNoteLocked(np.IndexKey, summary)
		v.idx.Add(summaryToDoc(summary, body))
		return nil
	})
	return summary, err
}

// PatchFrontMatter merges fields into the note's frontmatter, checking the ETag.
func (v *LocalVault) PatchFrontMatter(ctx context.Context, path string, fields map[string]any, ifMatch string) (domain.NoteSummary, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	var summary domain.NoteSummary
	err = v.actors.Do(ctx, v.scope, np.Storage, func() error {
		note, err := v.readNote(np.Storage)
		if err != nil {
			return err
		}
		if ifMatch != "" && note.ETag != ifMatch {
			return domain.ErrConflict
		}
		applyFrontMatterPatch(&note.FrontMatter, fields)
		note.FrontMatter.UpdatedAt = time.Now().UTC()
		note.ETag = domain.ComputeETag(note.FrontMatter, note.Body)
		data, err := formatNote(note.FrontMatter, note.Body)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(v.root, np.Storage), data, 0o644); err != nil {
			return err
		}
		summary = v.noteToSummary(note)
		v.upsertNoteLocked(np.IndexKey, summary)
		return nil
	})
	return summary, err
}

// Move renames a note to a new vault-relative path.
func (v *LocalVault) Move(ctx context.Context, path, newPath string, ifMatch string) (domain.NoteSummary, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	nnp, err := v.normalizePath(newPath)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	var summary domain.NoteSummary
	err = v.actors.Do(ctx, v.scope, np.Storage, func() error {
		note, err := v.readNote(np.Storage)
		if err != nil {
			return err
		}
		if ifMatch != "" && note.ETag != ifMatch {
			return domain.ErrConflict
		}
		newAbs := filepath.Join(v.root, nnp.Storage)
		if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(v.root, np.Storage), newAbs); err != nil {
			return err
		}
		note.Ref.Path = nnp.Storage
		summary = v.noteToSummary(note)
		v.mu.Lock()
		delete(v.notes, np.IndexKey)
		v.notes[nnp.IndexKey] = summary
		v.mu.Unlock()
		v.idx.Remove(domain.NoteRef{Scope: v.scope, Path: np.Storage})
		v.idx.Add(summaryToDoc(summary, note.Body))
		return nil
	})
	return summary, err
}

// Delete removes or soft-deletes a note.
func (v *LocalVault) Delete(ctx context.Context, path string, soft bool) error {
	np, err := v.normalizePath(path)
	if err != nil {
		return err
	}
	return v.actors.Do(ctx, v.scope, np.Storage, func() error {
		absPath := filepath.Join(v.root, np.Storage)
		if soft {
			trashPath := filepath.Join(v.root, ".trash", filepath.Base(np.Storage))
			if err := os.MkdirAll(filepath.Dir(trashPath), 0o755); err != nil {
				return err
			}
			if err := os.Rename(absPath, trashPath); err != nil {
				return err
			}
		} else {
			if err := os.Remove(absPath); err != nil {
				return err
			}
		}
		ref := domain.NoteRef{Scope: v.scope, Path: np.Storage}
		v.mu.Lock()
		delete(v.notes, np.IndexKey)
		v.mu.Unlock()
		v.idx.Remove(ref)
		return nil
	})
}

// Query returns notes matching the filter, sorted and paginated.
func (v *LocalVault) Query(_ context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	if err := checkAllowedScopes(q.Filter.AllowedScopes, v.scope); err != nil {
		if errors.Is(err, errDenied) {
			return domain.QueryResult{ScopesAttempted: []domain.ScopeID{v.scope}, ScopesSucceeded: []domain.ScopeID{v.scope}}, nil
		}
		return domain.QueryResult{}, err
	}

	v.mu.RLock()
	all := make([]domain.NoteSummary, 0, len(v.notes))
	for _, s := range v.notes {
		all = append(all, s)
	}
	v.mu.RUnlock()

	filtered := applyFilter(all, q.Filter)
	sortSummaries(filtered, q.Sort, q.Desc)

	total := len(filtered)
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := max(q.Offset, 0)
	if offset >= total {
		return domain.QueryResult{
			Total:           total,
			ScopesAttempted: []domain.ScopeID{v.scope},
			ScopesSucceeded: []domain.ScopeID{v.scope},
			PerScope:        map[domain.ScopeID]int{v.scope: 0},
		}, nil
	}
	end := min(offset+limit, total)
	page := filtered[offset:end]

	return domain.QueryResult{
		Notes:           page,
		Total:           total,
		HasMore:         end < total,
		PerScope:        map[domain.ScopeID]int{v.scope: len(page)},
		ScopesAttempted: []domain.ScopeID{v.scope},
		ScopesSucceeded: []domain.ScopeID{v.scope},
	}, nil
}

// Search returns notes ranked by BM25 relevance.
func (v *LocalVault) Search(_ context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
	if err := checkAllowedScopes(filter.AllowedScopes, v.scope); err != nil {
		if errors.Is(err, errDenied) {
			return nil, nil
		}
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	hits := v.idx.Search(text, limit*3) // over-fetch to allow post-filter
	var results []domain.RankedNote
	v.mu.RLock()
	defer v.mu.RUnlock()
	for _, h := range hits {
		key := domain.IndexKey(h.Ref.Path, v.caps.CaseSensitive)
		s, ok := v.notes[key]
		if !ok {
			continue
		}
		if !matchesFilter(s, filter) {
			continue
		}
		results = append(results, domain.RankedNote{Summary: s, Score: h.Score})
		if len(results) == limit {
			break
		}
	}
	return results, nil
}

// Backlinks returns notes that link to ref. Stub for Phase 1 (outgoing-links
// index is built in Phase 2).
func (v *LocalVault) Backlinks(_ context.Context, _ domain.NoteRef, filter domain.Filter) ([]domain.NoteSummary, error) {
	if filter.AllowedScopes == nil {
		return nil, errInternal
	}
	return nil, nil
}

// Stats returns aggregate note counts.
func (v *LocalVault) Stats(_ context.Context) (domain.VaultStats, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	stats := domain.VaultStats{ByCategory: make(map[domain.Category]int)}
	for _, s := range v.notes {
		stats.TotalNotes++
		stats.ByCategory[s.Category]++
	}
	return stats, nil
}

// Health returns vault diagnostic information including case collisions.
func (v *LocalVault) Health(_ context.Context) (domain.VaultHealth, error) {
	h := domain.VaultHealth{
		WatcherStatus: v.w.watcherStatus.Load().(string),
		SyncConflicts: int(v.w.syncConflicts.Load()),
	}
	if v.caps.CaseSensitive {
		h.CaseCollisions = v.detectCaseCollisions()
	}
	return h, nil
}

func (v *LocalVault) normalizePath(path string) (domain.NormalizedPath, error) {
	return domain.Normalize(v.root, path, v.caps.CaseSensitive)
}

func (v *LocalVault) readNote(storagePath string) (domain.Note, error) {
	absPath := filepath.Join(v.root, storagePath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.Note{}, domain.ErrNotFound
		}
		return domain.Note{}, err
	}
	fm, body, err := parseNote(data)
	if err != nil {
		return domain.Note{}, err
	}
	note := domain.Note{
		Ref:         domain.NoteRef{Scope: v.scope, Path: storagePath},
		FrontMatter: fm,
		Body:        body,
	}
	note.ETag = domain.ComputeETag(fm, body)
	return note, nil
}

func (v *LocalVault) noteToSummary(note domain.Note) domain.NoteSummary {
	cat, _ := domain.CategoryFromPath(note.Ref.Path)
	return domain.NoteSummary{
		Ref:       note.Ref,
		Title:     note.FrontMatter.Title,
		Tags:      note.FrontMatter.Tags,
		Status:    note.FrontMatter.Status,
		Area:      note.FrontMatter.Area,
		Project:   note.FrontMatter.Project,
		Category:  cat,
		UpdatedAt: note.FrontMatter.UpdatedAt,
		ETag:      note.ETag,
	}
}

func (v *LocalVault) upsertNoteLocked(indexKey string, s domain.NoteSummary) {
	v.mu.Lock()
	v.notes[indexKey] = s
	v.mu.Unlock()
}

func (v *LocalVault) scanVault() error {
	return filepath.WalkDir(v.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isMDFile(path) {
			return nil
		}
		rel, err := filepath.Rel(v.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		np, err := domain.Normalize(v.root, rel, v.caps.CaseSensitive)
		if err != nil {
			return nil // skip unrecognized paths
		}
		v.indexNote(path, np)
		return nil
	})
}

// indexNote reads absPath, ensures it has a NoteID, persists if needed, then
// upserts into the in-memory index. Safe to call from multiple goroutines.
func (v *LocalVault) indexNote(absPath string, np domain.NormalizedPath) {
	note, err := v.readNote(np.Storage)
	if err != nil {
		return
	}
	if domain.GetNoteID(note.FrontMatter) == "" {
		domain.SetNoteID(&note.FrontMatter, domain.DeriveNoteID(np.Storage, note.ETag))
		if data, err := formatNote(note.FrontMatter, note.Body); err == nil {
			_ = os.WriteFile(absPath, data, 0o644)
		}
	}
	s := v.noteToSummary(note)
	v.upsertNoteLocked(np.IndexKey, s)
	v.idx.Add(summaryToDoc(s, note.Body))
}

func (v *LocalVault) detectCaseCollisions() []domain.CaseCollision {
	v.mu.RLock()
	defer v.mu.RUnlock()
	lower := make(map[string]string, len(v.notes))
	var collisions []domain.CaseCollision
	for key, s := range v.notes {
		lk := domain.IndexKey(key, false)
		if prev, exists := lower[lk]; exists && prev != key {
			collisions = append(collisions, domain.CaseCollision{PathA: prev, PathB: s.Ref.Path})
		} else {
			lower[lk] = key
		}
	}
	return collisions
}

// errDenied signals an empty AllowedScopes (deny-all, not an error).
var errDenied = errors.New("denied")

// checkAllowedScopes enforces the AllowedScopes pre-filter contract.
func checkAllowedScopes(allowed []domain.ScopeID, scope domain.ScopeID) error {
	if allowed == nil {
		return errInternal
	}
	if slices.Contains(allowed, scope) {
		return nil
	}
	return errDenied
}

func applyFilter(notes []domain.NoteSummary, f domain.Filter) []domain.NoteSummary {
	out := notes[:0:0]
	for _, n := range notes {
		if matchesFilter(n, f) {
			out = append(out, n)
		}
	}
	return out
}

func matchesFilter(n domain.NoteSummary, f domain.Filter) bool {
	isArchive := n.Category == domain.Archives
	inRequestedCategories := len(f.Categories) > 0 && slices.Contains(f.Categories, n.Category)
	if isArchive && !f.IncludeArchives && !inRequestedCategories {
		return false
	}
	if len(f.Categories) > 0 && !inRequestedCategories && !(isArchive && f.IncludeArchives) {
		return false
	}
	if f.Status != "" && !strings.EqualFold(n.Status, f.Status) {
		return false
	}
	if f.Area != "" && !strings.EqualFold(n.Area, f.Area) {
		return false
	}
	if f.Project != "" && !strings.EqualFold(n.Project, f.Project) {
		return false
	}
	for _, tag := range f.Tags {
		if !hasTag(n.Tags, tag) {
			return false
		}
	}
	if len(f.AnyTags) > 0 && !slices.ContainsFunc(f.AnyTags, func(tag string) bool { return hasTag(n.Tags, tag) }) {
		return false
	}
	if f.UpdatedAfter != nil && !n.UpdatedAt.After(*f.UpdatedAfter) {
		return false
	}
	if f.UpdatedBefore != nil && !n.UpdatedAt.Before(*f.UpdatedBefore) {
		return false
	}
	return true
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

func sortSummaries(notes []domain.NoteSummary, field domain.SortField, desc bool) {
	slices.SortStableFunc(notes, func(a, b domain.NoteSummary) int {
		var cmp int
		switch field {
		case domain.SortByTitle:
			cmp = strings.Compare(a.Title, b.Title)
		default: // SortByUpdated, SortByCreated (CreatedAt not yet in summary)
			cmp = a.UpdatedAt.Compare(b.UpdatedAt)
		}
		if desc {
			return -cmp
		}
		return cmp
	})
}

func summaryToDoc(s domain.NoteSummary, body string) index.Doc {
	return index.Doc{
		Ref:       s.Ref,
		Title:     s.Title,
		Body:      body,
		UpdatedAt: s.UpdatedAt,
	}
}

func applyFrontMatterPatch(fm *domain.FrontMatter, fields map[string]any) {
	for k, v := range fields {
		switch k {
		case "title":
			if s, ok := v.(string); ok {
				fm.Title = s
			}
		case "status":
			if s, ok := v.(string); ok {
				fm.Status = normalizeStatus(s)
			}
		case "area":
			if s, ok := v.(string); ok {
				fm.Area = s
			}
		case "project":
			if s, ok := v.(string); ok {
				fm.Project = s
			}
		case "tags":
			// Handled via Extra or direct slice assertion.
			switch tv := v.(type) {
			case []string:
				fm.Tags = normalizeTags(tv)
			case []any:
				tags := make([]string, 0, len(tv))
				for _, t := range tv {
					if s, ok := t.(string); ok {
						tags = append(tags, s)
					}
				}
				fm.Tags = normalizeTags(tags)
			}
		default:
			if fm.Extra == nil {
				fm.Extra = make(map[string]any)
			}
			fm.Extra[k] = v
		}
	}
}

func normalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if n, err := domain.NormalizeTag(t); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func normalizeStatus(s string) string {
	n, err := domain.NormalizeTag(s)
	if err != nil {
		return s
	}
	return n
}

func isMDFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

// probeCaseSensitivity writes a probe file and reads it back with opposite
// case to determine if the filesystem is case-sensitive.
func probeCaseSensitivity(root string) bool {
	probe := filepath.Join(root, ".para_case_probe")
	_ = os.WriteFile(probe, []byte{}, 0o600)
	defer os.Remove(probe)
	_, err := os.Stat(strings.ToUpper(probe))
	// On case-insensitive FS the upper-cased probe resolves to the same file.
	return os.IsNotExist(err)
}
