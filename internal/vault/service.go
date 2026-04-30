package vault

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/whiskeyjimbo/paras/internal/domain"
)

// NoteService validates all NoteRef inputs via domain.Normalize before
// delegating to the underlying Vault. It is the single entry point for
// MCP tool handlers — handlers must never call Vault directly.
type NoteService struct {
	vault domain.Vault
}

// NewService wraps a Vault with NoteRef validation.
func NewService(v domain.Vault) *NoteService {
	return &NoteService{vault: v}
}

func (s *NoteService) Get(ctx context.Context, ref domain.NoteRef) (domain.Note, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.Note{}, err
	}
	return s.vault.Get(ctx, np.Storage)
}

func (s *NoteService) Create(ctx context.Context, in domain.CreateInput) (domain.NoteSummary, error) {
	np, err := s.normalizePath(in.Path)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	in.Path = np.Storage
	return s.vault.Create(ctx, in)
}

func (s *NoteService) UpdateBody(ctx context.Context, ref domain.NoteRef, body, ifMatch string) (domain.NoteSummary, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	return s.vault.UpdateBody(ctx, np.Storage, body, ifMatch)
}

func (s *NoteService) PatchFrontMatter(ctx context.Context, ref domain.NoteRef, fields map[string]any, ifMatch string) (domain.NoteSummary, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	return s.vault.PatchFrontMatter(ctx, np.Storage, fields, ifMatch)
}

func (s *NoteService) Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.NoteSummary, error) {
	np, err := s.normalizeRef(ref)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	npNew, err := s.normalizePath(newPath)
	if err != nil {
		return domain.NoteSummary{}, err
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
	return s.vault.Query(ctx, q)
}

func (s *NoteService) Search(ctx context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
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
	np, err := s.normalizeRef(ref)
	if err != nil {
		return nil, err
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
	result, err := s.vault.Query(ctx, domain.QueryRequest{Filter: filter, Limit: 1000})
	if err != nil {
		return nil, err
	}
	var ranked []domain.RankedNote
	for _, n := range result.Notes {
		if n.Ref.Path == ref.Path {
			continue
		}
		score := relatedScore(note, n)
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

func relatedScore(target domain.Note, candidate domain.NoteSummary) float64 {
	var score float64
	for _, t := range target.FrontMatter.Tags {
		for _, ct := range candidate.Tags {
			if strings.EqualFold(t, ct) {
				score++
			}
		}
	}
	if target.FrontMatter.Area != "" && strings.EqualFold(target.FrontMatter.Area, candidate.Area) {
		score += 2
	}
	if target.FrontMatter.Project != "" && strings.EqualFold(target.FrontMatter.Project, candidate.Project) {
		score += 2
	}
	return score
}

func (s *NoteService) CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error) {
	lv, ok := s.vault.(*LocalVault)
	if !ok {
		return domain.BatchResult{}, fmt.Errorf("batch ops require LocalVault")
	}
	return lv.CreateBatch(ctx, inputs)
}

func (s *NoteService) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	lv, ok := s.vault.(*LocalVault)
	if !ok {
		return domain.BatchResult{}, fmt.Errorf("batch ops require LocalVault")
	}
	return lv.UpdateBodyBatch(ctx, items)
}

func (s *NoteService) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	lv, ok := s.vault.(*LocalVault)
	if !ok {
		return domain.BatchResult{}, fmt.Errorf("batch ops require LocalVault")
	}
	return lv.PatchFrontMatterBatch(ctx, items)
}

// normalizeRef validates the scope against the vault and normalizes the path.
// In Phase 3, scope routing moves to VaultRegistry; NoteService becomes single-vault.
func (s *NoteService) normalizeRef(ref domain.NoteRef) (domain.NormalizedPath, error) {
	if ref.Scope != "" && ref.Scope != string(s.vault.Scope()) {
		return domain.NormalizedPath{}, fmt.Errorf("%w: scope %q", domain.ErrNotFound, ref.Scope)
	}
	return s.normalizePath(ref.Path)
}

// normalizePath validates a vault-relative path without a symlink check (handler layer concern).
// vault root is embedded in LocalVault; NoteService omits it so the check is skipped here.
func (s *NoteService) normalizePath(path string) (domain.NormalizedPath, error) {
	return domain.Normalize("", path, s.vault.Capabilities().CaseSensitive)
}
