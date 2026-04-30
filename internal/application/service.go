// Package application contains the NoteService use-case layer.
// It depends only on core/domain and core/ports — never on infrastructure.
package application

import (
	"cmp"
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

var errAllowedScopesNil = errors.New("internal: AllowedScopes must not be nil")

// NoteService validates all NoteRef inputs via domain.Normalize before
// delegating to the underlying Vault port. It is the single entry point for
// MCP tool handlers — handlers must never call a Vault directly.
type NoteService struct {
	vault            ports.Vault
	templates        map[domain.Category]domain.CategoryTemplate
	idMinter         func() string
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

func defaultIDMinter() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// NewService wraps a Vault with NoteRef validation.
func NewService(v ports.Vault, opts ...Option) *NoteService {
	s := &NoteService{vault: v, templates: domain.DefaultTemplates, idMinter: defaultIDMinter, relatedScanLimit: 1000}
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

func (s *NoteService) Delete(ctx context.Context, ref domain.NoteRef, soft bool) error {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return err
	}
	return s.vault.Delete(ctx, np.Storage, soft)
}

func (s *NoteService) Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	if q.Filter.AllowedScopes == nil {
		return domain.QueryResult{}, errAllowedScopesNil
	}
	if !slices.Contains(q.Filter.AllowedScopes, s.vault.Scope()) {
		return domain.QueryResult{
			Notes:           []domain.NoteSummary{},
			ScopesAttempted: []domain.ScopeID{s.vault.Scope()},
			ScopesSucceeded: []domain.ScopeID{s.vault.Scope()},
		}, nil
	}
	return s.vault.Query(ctx, q)
}

func (s *NoteService) Search(ctx context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
	if filter.AllowedScopes == nil {
		return nil, errAllowedScopesNil
	}
	if !slices.Contains(filter.AllowedScopes, s.vault.Scope()) {
		return nil, nil
	}
	return s.vault.Search(ctx, text, filter, limit)
}

func (s *NoteService) Stats(ctx context.Context) (domain.VaultStats, error) {
	return s.vault.Stats(ctx)
}

func (s *NoteService) Health(ctx context.Context) (domain.VaultHealth, error) {
	return s.vault.Health(ctx)
}

func (s *NoteService) Rescan(ctx context.Context) error {
	return s.vault.Rescan(ctx)
}

func (s *NoteService) Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, filter domain.Filter) ([]domain.BacklinkEntry, error) {
	if filter.AllowedScopes == nil {
		return nil, errAllowedScopesNil
	}
	np, err := s.normalizeRef(ref)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(filter.AllowedScopes, s.vault.Scope()) {
		return nil, nil
	}
	ref.Path = np.Storage
	return s.vault.Backlinks(ctx, ref, includeAssets, filter)
}

// Related returns notes scored by tag/area/project overlap with ref.
// Score = 1 per shared tag + 2 if same area + 2 if same project.
func (s *NoteService) Related(ctx context.Context, ref domain.NoteRef, limit int, filter domain.Filter) ([]domain.RankedNote, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return nil, err
	}
	ref.Path = np.Storage
	note, err := s.vault.Get(ctx, np.Storage)
	if err != nil {
		return nil, err
	}
	result, err := s.vault.Query(ctx, domain.QueryRequest{Filter: filter, Limit: s.relatedScanLimit})
	if err != nil {
		return nil, err
	}
	var ranked []domain.RankedNote
	for _, n := range result.Notes {
		if n.Ref.Path == ref.Path {
			continue
		}
		score := domain.ScoreRelatedness(note, n)
		if score > 0 {
			ranked = append(ranked, domain.RankedNote{Summary: n, Score: score})
		}
	}
	slices.SortFunc(ranked, func(a, b domain.RankedNote) int {
		return cmp.Compare(b.Score, a.Score)
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

func (s *NoteService) CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error) {
	return s.vault.CreateBatch(ctx, inputs)
}

func (s *NoteService) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	return s.vault.UpdateBodyBatch(ctx, items)
}

func (s *NoteService) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	return s.vault.PatchFrontMatterBatch(ctx, items)
}

// normalizeRef validates the scope against the vault and normalizes the path.
// In Phase 3, scope routing moves to VaultRegistry; NoteService becomes single-vault.
func (s *NoteService) normalizeRef(ref domain.NoteRef) (domain.NormalizedPath, error) {
	if ref.Scope != "" && ref.Scope != string(s.vault.Scope()) {
		return domain.NormalizedPath{}, fmt.Errorf("%w: scope %q", domain.ErrNotFound, ref.Scope)
	}
	return s.normalizePath(ref.Path)
}

func (s *NoteService) normalizePath(path string) (domain.NormalizedPath, error) {
	return domain.Normalize(path, s.vault.Capabilities().CaseSensitive)
}
