// Package localvault implements the filesystem-backed Vault adapter.
package localvault

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/actor"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/index"
)

// Option configures a LocalVault.
type Option func(*vaultConfig)

type vaultConfig struct {
	indexOpts []index.Option
	ftsIndex  index.FTSIndex
	templates map[domain.Category]domain.CategoryTemplate
	clock     func() time.Time
}

// WithIndexOptions passes options through to the default BM25 index.
// Ignored if WithFTSIndex is also provided.
func WithIndexOptions(opts ...index.Option) Option {
	return func(c *vaultConfig) { c.indexOpts = append(c.indexOpts, opts...) }
}

// WithFTSIndex replaces the default BM25 index with a custom implementation.
func WithFTSIndex(i index.FTSIndex) Option {
	return func(c *vaultConfig) { c.ftsIndex = i }
}

// WithTemplates overrides per-category creation templates (default: domain.DefaultTemplates).
func WithTemplates(t map[domain.Category]domain.CategoryTemplate) Option {
	return func(c *vaultConfig) { c.templates = t }
}

// WithClock overrides the time source for note timestamps (default: time.Now).
func WithClock(fn func() time.Time) Option {
	return func(c *vaultConfig) { c.clock = fn }
}

// LocalVault is a filesystem-backed implementation of ports.Vault.
type LocalVault struct {
	scope     string
	root      string
	caps      domain.Capabilities
	templates map[domain.Category]domain.CategoryTemplate
	clock     func() time.Time

	actors *actor.Pool
	idx    index.FTSIndex
	w      *watcher

	mu    sync.RWMutex
	notes map[string]domain.NoteSummary
	graph *BacklinkGraph
}

// New creates a LocalVault rooted at root with the given scope.
func New(scope, root string, opts ...Option) (*LocalVault, error) {
	cfg := vaultConfig{
		templates: domain.DefaultTemplates,
		clock:     time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create vault root: %w", err)
	}
	caseSensitive := probeCaseSensitivity(root)
	fts := cfg.ftsIndex
	if fts == nil {
		fts = index.New(cfg.indexOpts...)
	}
	v := &LocalVault{
		scope:     scope,
		root:      root,
		clock:     cfg.clock,
		actors:    actor.New(),
		idx:       fts,
		notes:     make(map[string]domain.NoteSummary),
		graph:     newBacklinkGraph(),
		templates: cfg.templates,
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
func (v *LocalVault) Root() string                      { return v.root }
func (v *LocalVault) CaseSensitive() bool               { return v.caps.CaseSensitive }

// IndexFile parses and indexes the note at absPath. No-op for non-markdown files.
func (v *LocalVault) IndexFile(absPath string) {
	if !isMDFile(absPath) {
		return
	}
	rel, err := filepath.Rel(v.root, absPath)
	if err != nil {
		return
	}
	np, err := v.normalizePath(filepath.ToSlash(rel))
	if err != nil {
		return
	}
	v.indexNote(absPath, np)
}

// RescanVault re-walks the vault root and rebuilds all indexes.
func (v *LocalVault) RescanVault() error { return v.scanVault() }

// RemoveFile removes the note at absPath from all indexes. No-op for non-markdown files.
func (v *LocalVault) RemoveFile(absPath string) {
	if !isMDFile(absPath) {
		return
	}
	rel, err := filepath.Rel(v.root, absPath)
	if err != nil {
		return
	}
	np, err := v.normalizePath(filepath.ToSlash(rel))
	if err != nil {
		return
	}
	v.removeNoteFromAllIndexes(np.IndexKey, np.Storage)
}

func (v *LocalVault) Get(_ context.Context, path string) (domain.Note, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.Note{}, err
	}
	return v.readNote(np.Storage)
}

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
		cat, hasCat := domain.CategoryFromPath(np.Storage)
		if hasCat {
			if tmpl, ok := v.templates[cat]; ok {
				if in.FrontMatter.Status == "" && tmpl.Status != "" {
					in.FrontMatter.Status = tmpl.Status
				}
				if len(in.FrontMatter.Tags) == 0 && len(tmpl.Tags) > 0 {
					in.FrontMatter.Tags = append(in.FrontMatter.Tags, tmpl.Tags...)
				}
			}
		}
		in.FrontMatter.CreatedAt = v.clock().UTC()
		in.FrontMatter.UpdatedAt = in.FrontMatter.CreatedAt
		in.FrontMatter.Tags = domain.NormalizeTags(in.FrontMatter.Tags)
		in.FrontMatter.Status = domain.NormalizeStatus(in.FrontMatter.Status)
		if domain.GetNoteID(in.FrontMatter) == "" {
			domain.SetNoteID(&in.FrontMatter, domain.MintNoteID())
		}
		data, err := formatNote(in.FrontMatter, in.Body)
		if err != nil {
			return err
		}
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
		links := parseLinks(in.Body)
		v.upsertWithLinks(np.IndexKey, np.Storage, summary, links)
		v.idx.Add(summaryToDoc(summary, in.Body))
		return nil
	})
	return summary, err
}

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
		note.FrontMatter.UpdatedAt = v.clock().UTC()
		note.Body = body
		note.ETag = domain.ComputeETag(canonicalFrontMatterYAML(note.FrontMatter), body)
		data, err := formatNote(note.FrontMatter, body)
		if err != nil {
			return err
		}
		absPath := filepath.Join(v.root, np.Storage)
		if err := os.WriteFile(absPath, data, 0o644); err != nil {
			return err
		}
		summary = v.noteToSummary(note)
		links := parseLinks(body)
		v.upsertWithLinks(np.IndexKey, np.Storage, summary, links)
		v.idx.Add(summaryToDoc(summary, body))
		return nil
	})
	return summary, err
}

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
		domain.ApplyFrontMatterPatch(&note.FrontMatter, fields)
		note.FrontMatter.UpdatedAt = v.clock().UTC()
		note.ETag = domain.ComputeETag(canonicalFrontMatterYAML(note.FrontMatter), note.Body)
		data, err := formatNote(note.FrontMatter, note.Body)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(v.root, np.Storage), data, 0o644); err != nil {
			return err
		}
		summary = v.noteToSummary(note)
		existingLinks := v.graph.Links(np.Storage)
		v.upsertWithLinks(np.IndexKey, np.Storage, summary, existingLinks)
		return nil
	})
	return summary, err
}

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
		links := parseLinks(note.Body)
		v.mu.Lock()
		delete(v.notes, np.IndexKey)
		v.notes[nnp.IndexKey] = summary
		v.mu.Unlock()
		v.graph.Remove(np.Storage)
		v.graph.Upsert(nnp.Storage, links)
		v.idx.Remove(domain.NoteRef{Scope: v.scope, Path: np.Storage})
		v.idx.Add(summaryToDoc(summary, note.Body))
		return nil
	})
	return summary, err
}

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
				if os.IsNotExist(err) {
					return domain.ErrNotFound
				}
				return err
			}
		} else {
			if err := os.Remove(absPath); err != nil {
				if os.IsNotExist(err) {
					return domain.ErrNotFound
				}
				return err
			}
		}
		v.removeNoteFromAllIndexes(np.IndexKey, np.Storage)
		return nil
	})
}

func (v *LocalVault) Query(_ context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	v.mu.RLock()
	all := make([]domain.NoteSummary, 0, len(v.notes))
	for _, s := range v.notes {
		all = append(all, s)
	}
	v.mu.RUnlock()

	filtered := domain.ApplyFilter(all, q.Filter)
	domain.SortSummaries(filtered, q.Sort, q.Desc)

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
			Notes:           []domain.NoteSummary{},
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

func (v *LocalVault) Search(_ context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
	if limit <= 0 {
		limit = 20
	}
	hits := v.idx.Search(text, limit*3)
	var results []domain.RankedNote
	v.mu.RLock()
	defer v.mu.RUnlock()
	for _, h := range hits {
		key := domain.IndexKey(h.Ref.Path, v.caps.CaseSensitive)
		s, ok := v.notes[key]
		if !ok {
			continue
		}
		if !domain.MatchesFilter(s, filter) {
			continue
		}
		results = append(results, domain.RankedNote{Summary: s, Score: h.Score})
		if len(results) == limit {
			break
		}
	}
	return results, nil
}

func (v *LocalVault) Backlinks(_ context.Context, ref domain.NoteRef, includeAssets bool, filter domain.Filter) ([]domain.BacklinkEntry, error) {
	keys := linkMatchKeys(ref.Path)
	seen := make(map[string]bool)
	var entries []domain.BacklinkEntry
	v.mu.RLock()
	defer v.mu.RUnlock()
	for _, key := range keys {
		for _, src := range v.graph.Backlinks(key) {
			if !includeAssets && src.isAsset {
				continue
			}
			if seen[src.path] {
				continue
			}
			seen[src.path] = true
			srcKey := domain.IndexKey(src.path, v.caps.CaseSensitive)
			s, ok := v.notes[srcKey]
			if !ok {
				continue
			}
			if !domain.MatchesFilter(s, filter) {
				continue
			}
			entries = append(entries, domain.BacklinkEntry{Summary: s, IsAsset: src.isAsset})
		}
	}
	return entries, nil
}

func (v *LocalVault) Rescan(_ context.Context) error {
	return v.scanVault()
}

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

func (v *LocalVault) Health(_ context.Context) (domain.VaultHealth, error) {
	h := domain.VaultHealth{
		WatcherStatus:     v.w.watcherStatus.Load().(string),
		SyncConflicts:     int(v.w.syncConflicts.Load()),
		UnrecognizedFiles: v.countUnrecognized(),
	}
	if v.caps.CaseSensitive {
		h.CaseCollisions = v.detectCaseCollisions()
	}
	return h, nil
}

func (v *LocalVault) countUnrecognized() int {
	var count int
	_ = filepath.WalkDir(v.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(v.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		first, _, _ := strings.Cut(rel, "/")
		if strings.HasPrefix(first, ".") {
			return filepath.SkipDir
		}
		if isMDFile(path) {
			if _, ok := domain.CategoryFromPath(rel); ok {
				return nil
			}
		}
		count++
		return nil
	})
	return count
}

func (v *LocalVault) CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error) {
	return runBatch(inputs, func(in domain.CreateInput) (string, domain.NoteSummary, error) {
		np, err := v.normalizePath(in.Path)
		if err != nil {
			return in.Path, domain.NoteSummary{}, err
		}
		sum, err := v.Create(ctx, domain.CreateInput{Path: np.Storage, FrontMatter: in.FrontMatter, Body: in.Body})
		return in.Path, sum, err
	}), nil
}

func (v *LocalVault) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	return runBatch(items, func(it domain.BatchUpdateBodyInput) (string, domain.NoteSummary, error) {
		sum, err := v.UpdateBody(ctx, it.Path, it.Body, it.IfMatch)
		return it.Path, sum, err
	}), nil
}

func (v *LocalVault) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	return runBatch(items, func(it domain.BatchPatchFrontMatterInput) (string, domain.NoteSummary, error) {
		sum, err := v.PatchFrontMatter(ctx, it.Path, it.Fields, it.IfMatch)
		return it.Path, sum, err
	}), nil
}

func runBatch[I any](items []I, fn func(I) (path string, sum domain.NoteSummary, err error)) domain.BatchResult {
	res := domain.BatchResult{Results: make([]domain.BatchItemResult, len(items))}
	for i, item := range items {
		path, sum, err := fn(item)
		r := domain.BatchItemResult{Index: i, Path: path}
		if err != nil {
			r.Error = err.Error()
			res.FailureCount++
		} else {
			r.OK = true
			r.Summary = &sum
			res.SuccessCount++
		}
		res.Results[i] = r
	}
	return res
}

func (v *LocalVault) normalizePath(path string) (domain.NormalizedPath, error) {
	np, err := domain.Normalize(path, v.caps.CaseSensitive)
	if err != nil {
		return domain.NormalizedPath{}, err
	}
	if err := checkSymlinks(v.root, np.Storage); err != nil {
		return domain.NormalizedPath{}, err
	}
	return np, nil
}

func checkSymlinks(vaultRoot, path string) error {
	abs := filepath.Join(vaultRoot, path)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real, err = evalSymlinksPartial(abs)
		if err != nil {
			return nil
		}
	}
	vaultReal, err := filepath.EvalSymlinks(vaultRoot)
	if err != nil {
		return nil
	}
	prefix := vaultReal + string(filepath.Separator)
	if real != vaultReal && !strings.HasPrefix(real, prefix) {
		return fmt.Errorf("path resolves outside vault root")
	}
	return nil
}

func evalSymlinksPartial(abs string) (string, error) {
	p := abs
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return "", errors.New("no existing ancestor found")
		}
		p = parent
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			return real, nil
		}
	}
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
	note.ETag = domain.ComputeETag(canonicalFrontMatterYAML(fm), body)
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

func (v *LocalVault) upsertWithLinks(indexKey, storagePath string, s domain.NoteSummary, links []outLink) {
	v.mu.Lock()
	v.notes[indexKey] = s
	v.mu.Unlock()
	v.graph.Upsert(storagePath, links)
}

func (v *LocalVault) removeNoteFromAllIndexes(indexKey, storagePath string) {
	ref := domain.NoteRef{Scope: v.scope, Path: storagePath}
	v.mu.Lock()
	delete(v.notes, indexKey)
	v.mu.Unlock()
	v.graph.Remove(storagePath)
	v.idx.Remove(ref)
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
		np, err := v.normalizePath(rel)
		if err != nil {
			return nil
		}
		v.indexNote(path, np)
		return nil
	})
}

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
	links := parseLinks(note.Body)
	v.upsertWithLinks(np.IndexKey, np.Storage, s, links)
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

func summaryToDoc(s domain.NoteSummary, body string) index.Doc {
	return index.Doc{
		Ref:       s.Ref,
		Title:     s.Title,
		Body:      body,
		UpdatedAt: s.UpdatedAt,
	}
}

func isMDFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

func probeCaseSensitivity(root string) bool {
	probe := filepath.Join(root, ".para_case_probe")
	_ = os.WriteFile(probe, []byte{}, 0o600)
	defer os.Remove(probe)
	_, err := os.Stat(strings.ToUpper(probe))
	return os.IsNotExist(err)
}
