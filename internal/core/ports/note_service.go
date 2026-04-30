package ports

import (
	"context"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// NoteService is the application service interface consumed by transport layers
// (MCP, HTTP, gRPC). All transports depend on this port; the concrete
// application.NoteService implements it.
type NoteService interface {
	Get(ctx context.Context, ref domain.NoteRef) (domain.Note, error)
	Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error)
	UpdateBody(ctx context.Context, ref domain.NoteRef, body, ifMatch string) (domain.MutationResult, error)
	PatchFrontMatter(ctx context.Context, ref domain.NoteRef, fields map[string]any, ifMatch string) (domain.MutationResult, error)
	Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.MutationResult, error)
	Delete(ctx context.Context, ref domain.NoteRef, soft bool) error
	Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error)
	Search(ctx context.Context, text string, filter domain.AuthFilter, limit int) ([]domain.RankedNote, error)
	Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, filter domain.AuthFilter) ([]domain.BacklinkEntry, error)
	Related(ctx context.Context, ref domain.NoteRef, limit int, filter domain.AuthFilter) ([]domain.RankedNote, error)
	Stats(ctx context.Context) (domain.VaultStats, error)
	Health(ctx context.Context) (domain.VaultHealth, error)
	Rescan(ctx context.Context) error
	ListScopes(ctx context.Context) []domain.ScopeInfo
	CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error)
	UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error)
	PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error)
}
