// Package application contains the NoteService use-case layer.
// It depends only on core/domain and core/ports — never on infrastructure.
package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
)

var errAllowedScopesNil = errors.New("internal: AllowedScopes must not be nil (programmer error)")

// checkScopes validates that allowedScopes is non-nil and returns whether
// the vault's scope is permitted. Returns (false, err) on nil, (false, nil)
// when the vault scope is not in the allowed set, (true, nil) when it is.
func (s *NoteService) checkScopes(allowedScopes []domain.ScopeID) (allowed bool, err error) {
	if allowedScopes == nil {
		return false, errAllowedScopesNil
	}
	return slices.Contains(allowedScopes, s.vault.Scope()), nil
}

// NoteService validates all NoteRef inputs via domain.Normalize before
// delegating to the underlying Vault port. It is the single entry point for
// MCP tool handlers — handlers must never call a Vault directly.
type NoteService struct {
	vault            ports.Vault
	templates        map[domain.Category]domain.CategoryTemplate
	idMinter         func() string
	clock            func() time.Time
	relatedScanLimit int
	semanticSearcher ports.SemanticSearcher
}

// Option configures a NoteService.
type Option func(*NoteService)

// WithTemplates overrides per-category creation templates (default: domain.DefaultTemplates).
func WithTemplates(t map[domain.Category]domain.CategoryTemplate) Option {
	return func(s *NoteService) { s.templates = t }
}

// WithIDMinter overrides the note ID generator (default: ULID).
func WithIDMinter(fn func() string) Option {
	return func(s *NoteService) { s.idMinter = fn }
}

// WithRelatedScanLimit sets the maximum number of notes fetched from the vault
// when computing related notes (default: 1000).
func WithRelatedScanLimit(n int) Option {
	return func(s *NoteService) { s.relatedScanLimit = n }
}

// WithSemanticSearcher attaches a SemanticSearcher used by SemanticSearch.
// When nil (default) SemanticSearch returns ErrCapabilityUnavailable.
func WithSemanticSearcher(ss ports.SemanticSearcher) Option {
	return func(s *NoteService) { s.semanticSearcher = ss }
}

// WithClock overrides the time source used by Stale (default: time.Now).
func WithClock(fn func() time.Time) Option {
	return func(s *NoteService) { s.clock = fn }
}

func defaultIDMinter() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// NewService wraps a Vault with NoteRef validation.
func NewService(v ports.Vault, opts ...Option) *NoteService {
	s := &NoteService{vault: v, templates: domain.DefaultTemplates, idMinter: defaultIDMinter, clock: time.Now, relatedScanLimit: 1000}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *NoteService) Get(ctx context.Context, ref domain.NoteRef) (domain.Note, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.Note{}, err
	}
	return s.vault.Get(ctx, np.Storage)
}

func (s *NoteService) Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error) {
	np, err := s.normalizePath(in.Path)
	if err != nil {
		return domain.MutationResult{}, err
	}
	in.Path = np.Storage
	if cat, ok := domain.CategoryFromPath(np.Storage); ok {
		if tmpl, ok := s.templates[cat]; ok {
			if in.FrontMatter.Status == "" && tmpl.Status != "" {
				in.FrontMatter.Status = tmpl.Status
			}
			if len(in.FrontMatter.Tags) == 0 && len(tmpl.Tags) > 0 {
				in.FrontMatter.Tags = append(in.FrontMatter.Tags, tmpl.Tags...)
			}
		}
	}
	if domain.GetNoteID(in.FrontMatter) == "" {
		domain.SetNoteID(&in.FrontMatter, s.idMinter())
	}
	return s.vault.Create(ctx, in)
}

func (s *NoteService) UpdateBody(ctx context.Context, ref domain.NoteRef, body, ifMatch string) (domain.MutationResult, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	return s.vault.UpdateBody(ctx, np.Storage, body, ifMatch)
}

func (s *NoteService) PatchFrontMatter(ctx context.Context, ref domain.NoteRef, fields map[string]any, ifMatch string) (domain.MutationResult, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	return s.vault.PatchFrontMatter(ctx, np.Storage, fields, ifMatch)
}

func (s *NoteService) Replace(ctx context.Context, ref domain.NoteRef, fields map[string]any, body, ifMatch string) (domain.MutationResult, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	return s.vault.Replace(ctx, np.Storage, fields, body, ifMatch)
}

func (s *NoteService) Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.MutationResult, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.MutationResult{}, err
	}
	npNew, err := s.normalizePath(newPath)
	if err != nil {
		return domain.MutationResult{}, err
	}
	return s.vault.Move(ctx, np.Storage, npNew.Storage, ifMatch)
}

func (s *NoteService) Delete(ctx context.Context, ref domain.NoteRef, soft bool, ifMatch string) error {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return err
	}
	return s.vault.Delete(ctx, np.Storage, soft, ifMatch)
}

func (s *NoteService) Promote(_ context.Context, in domain.PromoteInput) (domain.MutationResult, error) {
	return domain.MutationResult{}, fmt.Errorf("%w: note_promote from scope %q to %q requires FederationService", domain.ErrScopeForbidden, in.Ref.Scope, in.ToScope)
}

func (s *NoteService) Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	ok, err := s.checkScopes(q.AllowedScopes)
	if err != nil {
		return domain.QueryResult{}, err
	}
	if !ok {
		return domain.QueryResult{
			Notes:           []domain.NoteSummary{},
			ScopesAttempted: []domain.ScopeID{s.vault.Scope()},
			ScopesSucceeded: []domain.ScopeID{s.vault.Scope()},
		}, nil
	}
	return s.vault.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryFilter(q.Filter),
		domain.WithQuerySort(q.Sort, q.Desc),
		domain.WithQueryPagination(q.Limit, q.Offset),
		domain.WithQueryCursor(q.Cursor),
	))
}

func (s *NoteService) Search(ctx context.Context, text string, filter domain.AuthFilter, limit int) ([]domain.RankedNote, error) {
	ok, err := s.checkScopes(filter.AllowedScopes)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return s.vault.Search(ctx, text, filter.Filter, limit)
}

// SemanticCapable reports whether a SemanticSearcher is attached.
func (s *NoteService) SemanticCapable() bool { return s.semanticSearcher != nil }

// HybridSearch fuses BM25 (lexical) and vector results via Reciprocal Rank
// Fusion. When no SemanticSearcher is configured, falls back to lexical-only
// results — never errors with capability_unavailable.
func (s *NoteService) HybridSearch(ctx context.Context, query string, filter domain.AuthFilter, opts domain.HybridSearchOptions) ([]domain.RankedNote, error) {
	ok, err := s.checkScopes(filter.AllowedScopes)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []domain.RankedNote{}, nil
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	overFetch := limit * 4

	lex, lerr := s.vault.Search(ctx, query, filter.Filter, overFetch)
	if lerr != nil {
		lex = nil
	}
	var sem []domain.VectorHit
	if s.semanticSearcher != nil {
		semHits, serr := s.semanticSearcher.SemanticSearch(ctx, query, filter, domain.SemanticSearchOptions{
			Limit: overFetch,
		})
		if serr == nil {
			sem = semHits
		}
	}

	type fused struct {
		ref     domain.NoteRef
		summary domain.NoteSummary
		score   float64
	}
	byRef := map[domain.NoteRef]*fused{}
	for i, ln := range lex {
		f, ok := byRef[ln.Summary.Ref]
		if !ok {
			f = &fused{ref: ln.Summary.Ref, summary: ln.Summary}
			byRef[ln.Summary.Ref] = f
		}
		f.score += domain.RRFAlpha * (1.0 / float64(domain.RRFK+i+1))
	}
	for i, vh := range sem {
		f, ok := byRef[vh.Ref]
		if !ok {
			note, gerr := s.vault.Get(ctx, vh.Ref.Path)
			if gerr != nil {
				continue
			}
			f = &fused{ref: vh.Ref, summary: note.Summary()}
			byRef[vh.Ref] = f
		}
		f.score += (1.0 - domain.RRFAlpha) * (1.0 / float64(domain.RRFK+i+1))
	}
	out := make([]domain.RankedNote, 0, len(byRef))
	for _, f := range byRef {
		out = append(out, domain.RankedNote{Summary: f.summary, Score: f.score})
	}
	slices.SortFunc(out, func(a, b domain.RankedNote) int {
		if b.Score > a.Score {
			return 1
		}
		if b.Score < a.Score {
			return -1
		}
		return 0
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SemanticSearch runs a pure vector search and projects hits to RankedNote.
// When BodyMode == BodyOnDemand and no threshold is set, only the top
// BodyOnDemandTopK results carry note bodies. Returns ErrCapabilityUnavailable
// when no SemanticSearcher is configured.
func (s *NoteService) SemanticSearch(ctx context.Context, query string, filter domain.AuthFilter, opts domain.SemanticSearchOptions) ([]domain.RankedNote, error) {
	if s.semanticSearcher == nil {
		return nil, domain.ErrCapabilityUnavailable
	}
	ok, err := s.checkScopes(filter.AllowedScopes)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []domain.RankedNote{}, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	hits, err := s.semanticSearcher.SemanticSearch(ctx, query, filter, opts)
	if err != nil {
		return nil, err
	}
	bodyCap := len(hits)
	if opts.BodyMode == domain.BodyOnDemand && opts.Threshold == 0 && bodyCap > domain.BodyOnDemandTopK {
		bodyCap = domain.BodyOnDemandTopK
	}
	out := make([]domain.RankedNote, 0, len(hits))
	for i, h := range hits {
		note, gerr := s.vault.Get(ctx, h.Ref.Path)
		if gerr != nil {
			continue
		}
		rn := domain.RankedNote{Summary: note.Summary(), Score: h.Score}
		if opts.BodyMode == domain.BodyOnDemand && i < bodyCap {
			rn.Body = note.Body
		}
		out = append(out, rn)
	}
	return out, nil
}

func (s *NoteService) Stats(ctx context.Context) (domain.VaultStats, error) {
	return s.vault.Stats(ctx)
}

func (s *NoteService) Stale(ctx context.Context, days int, categories []domain.Category, status string, limit int, filter domain.AuthFilter) (domain.QueryResult, error) {
	ok, err := s.checkScopes(filter.AllowedScopes)
	if err != nil {
		return domain.QueryResult{}, err
	}
	if !ok {
		return domain.QueryResult{}, nil
	}
	cutoff := s.clock().AddDate(0, 0, -days)
	return s.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryFilter(domain.NewFilter(
			domain.WithStatus(status),
			domain.WithUpdatedBefore(cutoff),
			domain.WithCategories(categories...),
		)),
		domain.WithQueryAllowedScopes(filter.AllowedScopes),
		domain.WithQuerySort(domain.SortByUpdated, false),
		domain.WithQueryPagination(limit, 0),
	))
}

func (s *NoteService) ListScopes(_ context.Context) []domain.ScopeInfo {
	return []domain.ScopeInfo{{Scope: s.vault.Scope(), Capabilities: s.vault.Capabilities()}}
}

func (s *NoteService) Health(ctx context.Context) (domain.VaultHealth, error) {
	return s.vault.Health(ctx)
}

func (s *NoteService) Rescan(ctx context.Context) error {
	return s.vault.Rescan(ctx)
}

func (s *NoteService) Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, filter domain.AuthFilter) ([]domain.BacklinkEntry, error) {
	ok, err := s.checkScopes(filter.AllowedScopes)
	if err != nil {
		return nil, err
	}
	np, err := s.normalizeRef(ref)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	ref.Path = np.Storage
	return s.vault.Backlinks(ctx, ref, includeAssets, filter.Filter)
}

// Related returns notes related to ref. When semantic capability is available
// it ranks by vector cosine similarity using the source note body as the query;
// otherwise it falls back to the tag/area/project overlap heuristic
// (1 per shared tag + 2 same area + 2 same project).
func (s *NoteService) Related(ctx context.Context, ref domain.NoteRef, limit int, filter domain.AuthFilter) ([]domain.RankedNote, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return nil, err
	}
	note, err := s.vault.Get(ctx, np.Storage)
	if err != nil {
		return nil, err
	}
	if s.semanticSearcher != nil && note.Body != "" {
		// Over-fetch by 1 to allow excluding the source note from its own results.
		hits, serr := s.semanticSearcher.SemanticSearch(ctx, note.Body, filter, domain.SemanticSearchOptions{
			Limit: limit + 1,
		})
		if serr == nil {
			out := make([]domain.RankedNote, 0, limit)
			for _, h := range hits {
				if h.Ref.Path == np.Storage && h.Ref.Scope == ref.Scope {
					continue
				}
				other, gerr := s.vault.Get(ctx, h.Ref.Path)
				if gerr != nil {
					continue
				}
				out = append(out, domain.RankedNote{Summary: other.Summary(), Score: h.Score})
				if len(out) == limit {
					break
				}
			}
			return out, nil
		}
		// Fall through to heuristic on semantic error.
	}
	result, err := s.vault.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryFilter(filter.Filter),
		domain.WithQueryPagination(s.relatedScanLimit, 0),
	))
	if err != nil {
		return nil, err
	}
	return domain.RankRelated(note, result.Notes, np.Storage, limit), nil
}

func (s *NoteService) CreateBatch(ctx context.Context, inputs []domain.CreateInput, filter domain.AuthFilter) (domain.BatchResult, error) {
	ok, err := s.checkScopes(filter.AllowedScopes)
	if err != nil {
		return domain.BatchResult{}, err
	}
	if !ok {
		return domain.BatchResult{}, nil
	}
	return s.vault.CreateBatch(ctx, inputs)
}

func (s *NoteService) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput, filter domain.AuthFilter) (domain.BatchResult, error) {
	ok, err := s.checkScopes(filter.AllowedScopes)
	if err != nil {
		return domain.BatchResult{}, err
	}
	if !ok {
		return domain.BatchResult{}, nil
	}
	return s.vault.UpdateBodyBatch(ctx, items)
}

func (s *NoteService) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput, filter domain.AuthFilter) (domain.BatchResult, error) {
	ok, err := s.checkScopes(filter.AllowedScopes)
	if err != nil {
		return domain.BatchResult{}, err
	}
	if !ok {
		return domain.BatchResult{}, nil
	}
	return s.vault.PatchFrontMatterBatch(ctx, items)
}

// normalizeRef validates the scope against the vault and normalizes the path.
// In Phase 3, scope routing moves to VaultRegistry; NoteService becomes single-vault.
func (s *NoteService) normalizeRef(ref domain.NoteRef) (domain.NormalizedPath, error) {
	if ref.Scope != "" && ref.Scope != string(s.vault.Scope()) {
		return domain.NormalizedPath{}, fmt.Errorf("%w: scope %q", domain.ErrScopeForbidden, ref.Scope)
	}
	return s.normalizePath(ref.Path)
}

func (s *NoteService) normalizePath(path string) (domain.NormalizedPath, error) {
	return domain.Normalize(path, s.vault.Capabilities().CaseSensitive)
}
