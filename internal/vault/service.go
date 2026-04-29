package vault

import (
	"context"

	"github.com/whiskeyjimbo/paras/domain"
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

// Get fetches a note, normalizing the path first.
func (s *NoteService) Get(ctx context.Context, ref domain.NoteRef) (domain.Note, error) {
	if _, err := s.normalizeRef(ref); err != nil {
		return domain.Note{}, err
	}
	return s.vault.Get(ctx, ref.Path)
}

// Create creates a note, normalizing the path first.
func (s *NoteService) Create(ctx context.Context, in domain.CreateInput) (domain.NoteSummary, error) {
	np, err := s.normalizeForVault(in.Path)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	in.Path = np.Storage
	return s.vault.Create(ctx, in)
}

// UpdateBody updates a note's body, normalizing the path first.
func (s *NoteService) UpdateBody(ctx context.Context, ref domain.NoteRef, body, ifMatch string) (domain.NoteSummary, error) {
	if _, err := s.normalizeRef(ref); err != nil {
		return domain.NoteSummary{}, err
	}
	return s.vault.UpdateBody(ctx, ref.Path, body, ifMatch)
}

// PatchFrontMatter patches frontmatter, normalizing the path first.
func (s *NoteService) PatchFrontMatter(ctx context.Context, ref domain.NoteRef, fields map[string]any, ifMatch string) (domain.NoteSummary, error) {
	if _, err := s.normalizeRef(ref); err != nil {
		return domain.NoteSummary{}, err
	}
	return s.vault.PatchFrontMatter(ctx, ref.Path, fields, ifMatch)
}

// Move moves a note, normalizing both paths.
func (s *NoteService) Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.NoteSummary, error) {
	if _, err := s.normalizeRef(ref); err != nil {
		return domain.NoteSummary{}, err
	}
	npNew, err := s.normalizeForVault(newPath)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	return s.vault.Move(ctx, ref.Path, npNew.Storage, ifMatch)
}

// Delete deletes a note, normalizing the path first.
func (s *NoteService) Delete(ctx context.Context, ref domain.NoteRef, soft bool) error {
	if _, err := s.normalizeRef(ref); err != nil {
		return err
	}
	return s.vault.Delete(ctx, ref.Path, soft)
}

// Query delegates to the vault after validating AllowedScopes is set.
func (s *NoteService) Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	return s.vault.Query(ctx, q)
}

// Search delegates to the vault.
func (s *NoteService) Search(ctx context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
	return s.vault.Search(ctx, text, filter, limit)
}

func (s *NoteService) normalizeRef(ref domain.NoteRef) (domain.NormalizedPath, error) {
	caps := s.vault.Capabilities()
	// vault root is embedded in LocalVault; NoteService uses empty root so
	// path validation runs without FS symlink check (handler layer concern).
	return domain.Normalize("", ref.Path, caps.CaseSensitive)
}

func (s *NoteService) normalizeForVault(path string) (domain.NormalizedPath, error) {
	caps := s.vault.Capabilities()
	return domain.Normalize("", path, caps.CaseSensitive)
}
