package ports

import (
	"context"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// Vault is the storage port. All storage adapters implement this interface.
//
// AllowedScopes contract: every method that accepts a Filter must apply
// Filter.AllowedScopes as an index-side pre-filter. nil AllowedScopes
// triggers an internal error; empty []ScopeID{} returns an empty result.
type Vault interface {
	Scope() domain.ScopeID
	Capabilities() domain.Capabilities

	Get(ctx context.Context, path string) (domain.Note, error)
	Create(ctx context.Context, in domain.CreateInput) (domain.NoteSummary, error)
	UpdateBody(ctx context.Context, path, body string, ifMatch string) (domain.NoteSummary, error)
	PatchFrontMatter(ctx context.Context, path string, fields map[string]any, ifMatch string) (domain.NoteSummary, error)
	Move(ctx context.Context, path, newPath string, ifMatch string) (domain.NoteSummary, error)
	Delete(ctx context.Context, path string, soft bool) error

	Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error)
	Search(ctx context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error)
	Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, filter domain.Filter) ([]domain.BacklinkEntry, error)

	Stats(ctx context.Context) (domain.VaultStats, error)
	Health(ctx context.Context) (domain.VaultHealth, error)
	Rescan(ctx context.Context) error

	CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error)
	UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error)
	PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error)
}
