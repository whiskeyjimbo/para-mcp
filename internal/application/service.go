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
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
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

// Related returns notes scored by tag/area/project overlap with ref.
// Score = 1 per shared tag + 2 if same area + 2 if same project.
func (s *NoteService) Related(ctx context.Context, ref domain.NoteRef, limit int, filter domain.AuthFilter) ([]domain.RankedNote, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return nil, err
	}
	note, err := s.vault.Get(ctx, np.Storage)
	if err != nil {
		return nil, err
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
