package vault

import (
	"context"

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
	np, err := s.normalizePath(ref.Path)
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
	np, err := s.normalizePath(ref.Path)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	return s.vault.UpdateBody(ctx, np.Storage, body, ifMatch)
}

func (s *NoteService) PatchFrontMatter(ctx context.Context, ref domain.NoteRef, fields map[string]any, ifMatch string) (domain.NoteSummary, error) {
	np, err := s.normalizePath(ref.Path)
	if err != nil {
		return domain.NoteSummary{}, err
	}
	return s.vault.PatchFrontMatter(ctx, np.Storage, fields, ifMatch)
}

func (s *NoteService) Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.NoteSummary, error) {
	np, err := s.normalizePath(ref.Path)
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
	np, err := s.normalizePath(ref.Path)
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

// normalizePath validates a vault-relative path without a symlink check (handler layer concern).
// vault root is embedded in LocalVault; NoteService omits it so the check is skipped here.
func (s *NoteService) normalizePath(path string) (domain.NormalizedPath, error) {
	return domain.Normalize("", path, s.vault.Capabilities().CaseSensitive)
}
