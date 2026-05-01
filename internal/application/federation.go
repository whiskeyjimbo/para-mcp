package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/server/cursorstore"
)

const maxCursorOffset = 500

// TombstoneStore records deleted notes so stale summary caches cannot resurface them.
// Keys are (NoteRef, NoteID); Contains checks by NoteRef only.
type TombstoneStore interface {
	Add(ref domain.NoteRef, noteID string)
	Contains(ref domain.NoteRef) bool
}

// idempotencyEntry caches a completed promote result for a given key.
type idempotencyEntry struct {
	result domain.MutationResult
	expiry time.Time
}

const idempotencyTTL = 24 * time.Hour

// FederationService implements ports.NoteService by fan-out across all
// registered vaults. Single-vault operations (Get, Create, etc.) route
// to the entry matching the request's scope; the primary (local) vault
// is used when no scope is specified.
type FederationService struct {
	reg              *VaultRegistry
	cursorKey        []byte
	cursorStore      cursorStore
	tombstones       TombstoneStore
	idMinter         func() string
	idempotencyMu    sync.Mutex
	idempotencyCache map[string]idempotencyEntry
}

// NewFederationService creates a FederationService backed by reg.
// A 32-byte HMAC key is generated automatically; use
// NewFederationServiceWithKey when you need a deterministic key (tests).
func NewFederationService(reg *VaultRegistry, opts ...FederationOption) (*FederationService, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("federation: generate cursor key: %w", err)
	}
	return NewFederationServiceWithKey(reg, key, opts...), nil
}

// FederationOption configures a FederationService.
type FederationOption func(*FederationService)

// WithTombstoneStore sets a custom tombstone store (default: in-memory).
func WithTombstoneStore(ts TombstoneStore) FederationOption {
	return func(f *FederationService) { f.tombstones = ts }
}

// WithFederationIDMinter overrides the note ID generator for promoted notes.
func WithFederationIDMinter(fn func() string) FederationOption {
	return func(f *FederationService) { f.idMinter = fn }
}

// WithCursorStore replaces the default in-memory cursor store with a
// pluggable implementation (Postgres or Redis for multi-replica deploys).
func WithCursorStore(s cursorstore.CursorStore) FederationOption {
	return func(f *FederationService) { f.cursorStore = newPublicCursorStoreAdapter(s) }
}

// NewFederationServiceWithKey creates a FederationService with an explicit HMAC key.
func NewFederationServiceWithKey(reg *VaultRegistry, key []byte, opts ...FederationOption) *FederationService {
	f := &FederationService{
		reg:              reg,
		cursorKey:        key,
		cursorStore:      newDefaultCursorStore(),
		tombstones:       newInMemoryTombstoneStore(),
		idMinter:         defaultFederationIDMinter,
		idempotencyCache: make(map[string]idempotencyEntry),
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

// localEntry returns the first registered vault entry (the local vault).
func (f *FederationService) localEntry() (*VaultEntry, error) {
	entries := f.reg.Entries()
	if len(entries) == 0 {
		return nil, domain.ErrUnavailable
	}
	return &entries[0], nil
}

// entryForRef returns the vault entry matching ref.Scope, or the local
// vault when ref.Scope is empty.
func (f *FederationService) entryForRef(ref domain.NoteRef) (*VaultEntry, error) {
	if ref.Scope == "" {
		return f.localEntry()
	}
	e, ok := f.reg.EntryFor(ref.Scope)
	if !ok {
		return nil, fmt.Errorf("%w: scope %q", domain.ErrScopeForbidden, ref.Scope)
	}
	return e, nil
}

// --- Read operations ---

func (f *FederationService) Get(ctx context.Context, ref domain.NoteRef) (domain.Note, error) {
	e, err := f.entryForRef(ref)
	if err != nil {
		return domain.Note{}, err
	}
	return e.svc.Get(ctx, ref)
}

func (f *FederationService) Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	if q.AllowedScopes == nil {
		return domain.QueryResult{}, errAllowedScopesNil
	}
	effectiveScopes, offsets, err := f.resolveScopesAndOffsets(ctx, q)
	if err != nil {
		return domain.QueryResult{}, err
	}

	type scopeResult struct {
		scope domain.ScopeID
		res   domain.QueryResult
		err   error
	}

	ch := make(chan scopeResult, len(effectiveScopes))
	var wg sync.WaitGroup
	for _, scope := range effectiveScopes {
		e, ok := f.reg.EntryFor(scope)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(entry *VaultEntry, sc domain.ScopeID, off int) {
			defer wg.Done()
			res, err := entry.svc.Query(ctx, domain.NewQueryRequest(
				domain.WithQueryFilter(q.Filter),
				domain.WithQueryAllowedScopes([]domain.ScopeID{sc}),
				domain.WithQuerySort(q.Sort, q.Desc),
				domain.WithQueryPagination(q.Limit+1, off),
			))
			ch <- scopeResult{scope: sc, res: res, err: err}
		}(e, scope, offsets[scope])
	}
	wg.Wait()
	close(ch)

	var (
		all       []domain.NoteSummary
		perScope  = make(map[domain.ScopeID]int)
		attempted []domain.ScopeID
		succeeded []domain.ScopeID
		partial   *domain.PartialFailure
	)
	for r := range ch {
		attempted = append(attempted, r.scope)
		if r.err != nil {
			if partial == nil {
				partial = &domain.PartialFailure{Reason: make(map[domain.ScopeID]string)}
			}
			partial.FailedScopes = append(partial.FailedScopes, r.scope)
			partial.Reason[r.scope] = r.err.Error()
			continue
		}
		succeeded = append(succeeded, r.scope)
		for _, n := range r.res.Notes {
			if !f.tombstones.Contains(n.Ref) {
				all = append(all, n)
			}
		}
	}

	if len(succeeded) == 0 {
		return domain.QueryResult{}, domain.ErrUnavailable
	}

	domain.SortSummaries(all, q.Sort, q.Desc)

	hasMore := false
	if len(all) > q.Limit {
		all = all[:q.Limit]
		hasMore = true
	}

	// Count per-scope contributions in the result page.
	for _, n := range all {
		perScope[n.Ref.Scope]++
	}

	// Build new offsets: advance each scope by how many of its notes we took.
	newOffsets := make(map[domain.ScopeID]int, len(effectiveScopes))
	for _, sc := range effectiveScopes {
		newOffsets[sc] = offsets[sc] + perScope[sc]
	}

	var nextCursor string
	if hasMore {
		nextCursor, err = encodeCursor(ctx, f.cursorKey, f.cursorStore, cursorPayload{
			Sort:    q.Sort,
			Desc:    q.Desc,
			Scopes:  effectiveScopes,
			Offsets: newOffsets,
		})
		if err != nil {
			return domain.QueryResult{}, fmt.Errorf("federation: encode cursor: %w", err)
		}
	}

	if partial != nil {
		partial.WarningText = fmt.Sprintf("%d scope(s) failed: %v", len(partial.FailedScopes), partial.FailedScopes)
	}

	total := 0
	for _, c := range perScope {
		total += c
	}

	return domain.QueryResult{
		Notes:           all,
		Total:           total,
		HasMore:         hasMore,
		PerScope:        perScope,
		ScopesAttempted: attempted,
		ScopesSucceeded: succeeded,
		PartialFailure:  partial,
		NextCursor:      nextCursor,
	}, nil
}

func (f *FederationService) Search(ctx context.Context, text string, filter domain.AuthFilter, limit int) ([]domain.RankedNote, error) {
	if filter.AllowedScopes == nil {
		return nil, errAllowedScopesNil
	}
	scopes := f.effectiveScopes(filter.AllowedScopes, filter.Scopes)

	type result struct {
		notes []domain.RankedNote
		err   error
	}
	ch := make(chan result, len(scopes))
	var wg sync.WaitGroup
	for _, scope := range scopes {
		e, ok := f.reg.EntryFor(scope)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(entry *VaultEntry, sc domain.ScopeID) {
			defer wg.Done()
			ns, err := entry.svc.Search(ctx, text, domain.AuthFilter{
				Filter:        filter.Filter,
				AllowedScopes: []domain.ScopeID{sc},
			}, limit)
			ch <- result{notes: ns, err: err}
		}(e, scope)
	}
	wg.Wait()
	close(ch)

	var all []domain.RankedNote
	for r := range ch {
		if r.err != nil {
			continue
		}
		all = append(all, r.notes...)
	}
	slices.SortFunc(all, func(a, b domain.RankedNote) int {
		if b.Score > a.Score {
			return 1
		}
		if b.Score < a.Score {
			return -1
		}
		return 0
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (f *FederationService) Stale(ctx context.Context, days int, categories []domain.Category, status string, limit int, filter domain.AuthFilter) (domain.QueryResult, error) {
	local, err := f.localEntry()
	if err != nil {
		return domain.QueryResult{}, err
	}
	return local.svc.Stale(ctx, days, categories, status, limit, filter)
}

func (f *FederationService) Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, filter domain.AuthFilter) ([]domain.BacklinkEntry, error) {
	if filter.AllowedScopes == nil {
		return nil, errAllowedScopesNil
	}
	scopes := f.effectiveScopes(filter.AllowedScopes, filter.Scopes)

	type result struct {
		entries []domain.BacklinkEntry
		err     error
	}
	ch := make(chan result, len(scopes))
	var wg sync.WaitGroup
	for _, scope := range scopes {
		e, ok := f.reg.EntryFor(scope)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(entry *VaultEntry, sc domain.ScopeID) {
			defer wg.Done()
			bl, err := entry.svc.Backlinks(ctx, ref, includeAssets, domain.AuthFilter{
				Filter:        filter.Filter,
				AllowedScopes: []domain.ScopeID{sc},
			})
			ch <- result{entries: bl, err: err}
		}(e, scope)
	}
	wg.Wait()
	close(ch)

	var all []domain.BacklinkEntry
	for r := range ch {
		if r.err == nil {
			all = append(all, r.entries...)
		}
	}
	return all, nil
}

func (f *FederationService) Related(ctx context.Context, ref domain.NoteRef, limit int, filter domain.AuthFilter) ([]domain.RankedNote, error) {
	e, err := f.entryForRef(ref)
	if err != nil {
		return nil, err
	}
	return e.svc.Related(ctx, ref, limit, filter)
}

func (f *FederationService) Stats(ctx context.Context) (domain.VaultStats, error) {
	local, err := f.localEntry()
	if err != nil {
		return domain.VaultStats{}, err
	}
	return local.svc.Stats(ctx)
}

func (f *FederationService) Health(ctx context.Context) (domain.VaultHealth, error) {
	local, err := f.localEntry()
	if err != nil {
		return domain.VaultHealth{}, err
	}
	return local.svc.Health(ctx)
}

func (f *FederationService) Rescan(ctx context.Context) error {
	local, err := f.localEntry()
	if err != nil {
		return err
	}
	return local.svc.Rescan(ctx)
}

func (f *FederationService) ListScopes(_ context.Context) []domain.ScopeInfo {
	entries := f.reg.Entries()
	infos := make([]domain.ScopeInfo, 0, len(entries))
	for _, e := range entries {
		infos = append(infos, domain.ScopeInfo{
			Scope:        e.ScopeID,
			Capabilities: e.vault.Capabilities(),
		})
	}
	return infos
}

// --- Write operations (local-only in Phase 3) ---

func (f *FederationService) Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error) {
	local, err := f.localEntry()
	if err != nil {
		return domain.MutationResult{}, err
	}
	return local.svc.Create(ctx, in)
}

func (f *FederationService) UpdateBody(ctx context.Context, ref domain.NoteRef, body, ifMatch string) (domain.MutationResult, error) {
	e, err := f.entryForRef(ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	return e.svc.UpdateBody(ctx, ref, body, ifMatch)
}

func (f *FederationService) PatchFrontMatter(ctx context.Context, ref domain.NoteRef, fields map[string]any, ifMatch string) (domain.MutationResult, error) {
	e, err := f.entryForRef(ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	return e.svc.PatchFrontMatter(ctx, ref, fields, ifMatch)
}

func (f *FederationService) Replace(ctx context.Context, ref domain.NoteRef, fields map[string]any, body, ifMatch string) (domain.MutationResult, error) {
	e, err := f.entryForRef(ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	return e.svc.Replace(ctx, ref, fields, body, ifMatch)
}

func (f *FederationService) Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.MutationResult, error) {
	e, err := f.entryForRef(ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	return e.svc.Move(ctx, ref, newPath, ifMatch)
}

func (f *FederationService) Delete(ctx context.Context, ref domain.NoteRef, soft bool, ifMatch string) error {
	e, err := f.entryForRef(ref)
	if err != nil {
		return err
	}
	// Fetch NoteID before deletion for tombstone keying.
	var noteID string
	if note, gerr := e.svc.Get(ctx, ref); gerr == nil {
		noteID = domain.GetNoteID(note.FrontMatter)
	}
	if err := e.svc.Delete(ctx, ref, soft, ifMatch); err != nil {
		return err
	}
	f.tombstones.Add(ref, noteID)
	return nil
}

func (f *FederationService) Promote(ctx context.Context, in domain.PromoteInput) (domain.MutationResult, error) {
	if in.IdempotencyKey != "" {
		if cached, ok := f.lookupIdempotency(in.IdempotencyKey); ok {
			return cached, nil
		}
	}

	srcEntry, err := f.entryForRef(in.Ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	destRef := domain.NoteRef{Scope: in.ToScope, Path: in.Ref.Path}
	destEntry, err := f.entryForRef(destRef)
	if err != nil {
		return domain.MutationResult{}, err
	}

	src, err := srcEntry.svc.Get(ctx, in.Ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if in.IfMatch != "" && src.ETag != in.IfMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	ci := domain.NewCreateInput(in.Ref.Path, src.FrontMatter, src.Body)
	domain.SetNoteID(&ci.FrontMatter, f.idMinter())

	res, cerr := destEntry.svc.Create(ctx, ci)
	if cerr != nil {
		if !isConflict(cerr) || in.OnConflict != domain.ConflictOverwrite {
			return domain.MutationResult{}, cerr
		}
		res, err = destEntry.svc.Replace(ctx, destRef, frontMatterFields(src.FrontMatter), src.Body, "")
		if err != nil {
			return domain.MutationResult{}, err
		}
	}

	if !in.KeepSource {
		archPath, aerr := domain.ArchivePath(in.Ref.Path)
		if aerr == nil {
			_, _ = srcEntry.svc.Move(ctx, in.Ref, archPath, src.ETag)
		}
	}

	if in.IdempotencyKey != "" {
		f.storeIdempotency(in.IdempotencyKey, res)
	}
	return res, nil
}

func (f *FederationService) CreateBatch(ctx context.Context, inputs []domain.CreateInput, filter domain.AuthFilter) (domain.BatchResult, error) {
	local, err := f.localEntry()
	if err != nil {
		return domain.BatchResult{}, err
	}
	return local.svc.CreateBatch(ctx, inputs, filter)
}

func (f *FederationService) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput, filter domain.AuthFilter) (domain.BatchResult, error) {
	local, err := f.localEntry()
	if err != nil {
		return domain.BatchResult{}, err
	}
	return local.svc.UpdateBodyBatch(ctx, items, filter)
}

func (f *FederationService) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput, filter domain.AuthFilter) (domain.BatchResult, error) {
	local, err := f.localEntry()
	if err != nil {
		return domain.BatchResult{}, err
	}
	return local.svc.PatchFrontMatterBatch(ctx, items, filter)
}

// --- Helpers ---

// effectiveScopes intersects allowed with registered scopes, then further
// restricts to requested when non-empty (Filter.Scopes is the client-side
// scope selector; AllowedScopes is the server-side authorization ceiling).
func (f *FederationService) effectiveScopes(allowed, requested []domain.ScopeID) []domain.ScopeID {
	out := make([]domain.ScopeID, 0, len(allowed))
	for _, sc := range allowed {
		if _, ok := f.reg.EntryFor(sc); ok {
			out = append(out, sc)
		}
	}
	if len(requested) == 0 {
		return out
	}
	req := make(map[domain.ScopeID]bool, len(requested))
	for _, sc := range requested {
		req[sc] = true
	}
	filtered := out[:0]
	for _, sc := range out {
		if req[sc] {
			filtered = append(filtered, sc)
		}
	}
	return filtered
}

// resolveScopesAndOffsets decodes the cursor (if present) to get the sticky
// scope-set and per-scope offsets, or builds fresh values from the request.
func (f *FederationService) resolveScopesAndOffsets(ctx context.Context, q domain.QueryRequest) ([]domain.ScopeID, map[domain.ScopeID]int, error) {
	if q.Cursor != "" {
		p, err := decodeCursor(ctx, f.cursorKey, f.cursorStore, q.Cursor)
		if err != nil {
			return nil, nil, err
		}
		// Re-intersect sticky scopes with currently allowed scopes.
		allowed := make(map[domain.ScopeID]bool, len(q.AllowedScopes))
		for _, sc := range q.AllowedScopes {
			allowed[sc] = true
		}
		var scopes []domain.ScopeID
		for _, sc := range p.Scopes {
			if allowed[sc] {
				scopes = append(scopes, sc)
			}
		}
		return scopes, p.Offsets, nil
	}

	// Fresh request: build uniform zero offsets, but honour q.Offset with the cap.
	if q.Offset > maxCursorOffset {
		return nil, nil, fmt.Errorf("%w: offset %d exceeds maximum %d", domain.ErrInvalidCursor, q.Offset, maxCursorOffset)
	}
	scopes := f.effectiveScopes(q.AllowedScopes, q.Filter.Scopes)
	offsets := make(map[domain.ScopeID]int, len(scopes))
	for _, sc := range scopes {
		offsets[sc] = q.Offset
	}
	return scopes, offsets, nil
}

// Ensure FederationService satisfies the full NoteService port.
var _ ports.NoteService = (*FederationService)(nil)

// Ensure NoteService still satisfies the port (regression guard).
var _ ports.NoteService = (*NoteService)(nil)

// --- Tombstone store ---

type inMemoryTombstoneStore struct {
	mu      sync.RWMutex
	entries map[string]string // key: scope:path → noteID
}

func newInMemoryTombstoneStore() *inMemoryTombstoneStore {
	return &inMemoryTombstoneStore{entries: make(map[string]string)}
}

func (s *inMemoryTombstoneStore) Add(ref domain.NoteRef, noteID string) {
	s.mu.Lock()
	s.entries[ref.String()] = noteID
	s.mu.Unlock()
}

func (s *inMemoryTombstoneStore) Contains(ref domain.NoteRef) bool {
	s.mu.RLock()
	_, ok := s.entries[ref.String()]
	s.mu.RUnlock()
	return ok
}

// --- Helpers ---

func (f *FederationService) lookupIdempotency(key string) (domain.MutationResult, bool) {
	f.idempotencyMu.Lock()
	defer f.idempotencyMu.Unlock()
	e, ok := f.idempotencyCache[key]
	if !ok {
		return domain.MutationResult{}, false
	}
	if time.Now().After(e.expiry) {
		delete(f.idempotencyCache, key)
		return domain.MutationResult{}, false
	}
	return e.result, true
}

func (f *FederationService) storeIdempotency(key string, result domain.MutationResult) {
	f.idempotencyMu.Lock()
	f.idempotencyCache[key] = idempotencyEntry{result: result, expiry: time.Now().Add(idempotencyTTL)}
	f.idempotencyMu.Unlock()
}

func defaultFederationIDMinter() string {
	return defaultIDMinter()
}

func isConflict(err error) bool {
	return errors.Is(err, domain.ErrConflict)
}

// frontMatterFields converts a FrontMatter into the patch fields map used by PatchFrontMatter.
func frontMatterFields(fm domain.FrontMatter) map[string]any {
	m := map[string]any{
		"title":   fm.Title,
		"status":  fm.Status,
		"area":    fm.Area,
		"project": fm.Project,
		"tags":    fm.Tags,
	}
	maps.Copy(m, fm.Extra)
	return m
}
