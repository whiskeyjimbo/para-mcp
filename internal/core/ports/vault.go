package ports

import (
	"context"
	"io"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// VaultReader is the read-only slice of the Vault port.
type VaultReader interface {
	Scope() domain.ScopeID
	Capabilities() domain.Capabilities

	Get(ctx context.Context, path string) (domain.Note, error)
	Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error)
	Search(ctx context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error)
	Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, filter domain.Filter) ([]domain.BacklinkEntry, error)

	Stats(ctx context.Context) (domain.VaultStats, error)
	Health(ctx context.Context) (domain.VaultHealth, error)
	Rescan(ctx context.Context) error
}

// VaultWriter is the mutation slice of the Vault port.
type VaultWriter interface {
	Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error)
	UpdateBody(ctx context.Context, path, body string, ifMatch string) (domain.MutationResult, error)
	PatchFrontMatter(ctx context.Context, path string, fields map[string]any, ifMatch string) (domain.MutationResult, error)
	Move(ctx context.Context, path, newPath string, ifMatch string) (domain.MutationResult, error)
	Delete(ctx context.Context, path string, soft bool, ifMatch string) error
}

// VaultBatcher is the batch-mutation slice of the Vault port.
type VaultBatcher interface {
	CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error)
	UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error)
	PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error)
}

// Vault is the full storage port. All storage adapters implement this interface.
// AllowedScopes enforcement happens at the NoteService boundary; vault methods
// receive plain Filter values with no authorization fields.
type Vault interface {
	io.Closer
	VaultReader
	VaultWriter
	VaultBatcher
}
