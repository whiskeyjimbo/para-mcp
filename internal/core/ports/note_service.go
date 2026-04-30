package ports

import (
	"context"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// NoteReader is the read-only slice of the NoteService port.
type NoteReader interface {
	Get(ctx context.Context, ref domain.NoteRef) (domain.Note, error)
	Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error)
	Search(ctx context.Context, text string, filter domain.AuthFilter, limit int) ([]domain.RankedNote, error)
	Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, filter domain.AuthFilter) ([]domain.BacklinkEntry, error)
	Related(ctx context.Context, ref domain.NoteRef, limit int, filter domain.AuthFilter) ([]domain.RankedNote, error)
	Stats(ctx context.Context) (domain.VaultStats, error)
	Health(ctx context.Context) (domain.VaultHealth, error)
	Rescan(ctx context.Context) error
	ListScopes(ctx context.Context) []domain.ScopeInfo
}

// NoteWriter is the single-note mutation slice of the NoteService port.
type NoteWriter interface {
	Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error)
	UpdateBody(ctx context.Context, ref domain.NoteRef, body, ifMatch string) (domain.MutationResult, error)
	PatchFrontMatter(ctx context.Context, ref domain.NoteRef, fields map[string]any, ifMatch string) (domain.MutationResult, error)
	Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.MutationResult, error)
	Delete(ctx context.Context, ref domain.NoteRef, soft bool) error
}

// NoteBatcher is the batch-mutation slice of the NoteService port.
type NoteBatcher interface {
	CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error)
	UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error)
	PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error)
}

// NoteService is the full application service interface consumed by transport
// layers (MCP, HTTP, gRPC). The concrete application.NoteService implements it.
// Transports that only need a subset should depend on NoteReader, NoteWriter,
// or NoteBatcher directly.
type NoteService interface {
	NoteReader
	NoteWriter
	NoteBatcher
}
