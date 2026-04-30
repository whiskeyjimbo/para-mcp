package ports

import (
	"context"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// ScopeResolver resolves the permitted scopes for a request.
// Implementations may derive scopes from context (middleware-injected JWT,
// session token), from static config, or from any other source.
type ScopeResolver interface {
	Scopes(ctx context.Context) []domain.ScopeID
}

// ScopesFunc is a function that implements ScopeResolver, allowing
// an inline func to be used wherever a ScopeResolver is required.
type ScopesFunc func(ctx context.Context) []domain.ScopeID

func (f ScopesFunc) Scopes(ctx context.Context) []domain.ScopeID { return f(ctx) }

// NoteReader is the read-only slice of the NoteService port.
type NoteReader interface {
	Get(ctx context.Context, ref domain.NoteRef) (domain.Note, error)
	Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error)
	Search(ctx context.Context, text string, filter domain.AuthFilter, limit int) ([]domain.RankedNote, error)
	Stale(ctx context.Context, days int, categories []domain.Category, status string, limit int, allowedScopes []domain.ScopeID) (domain.QueryResult, error)
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
// allowedScopes enforces the same vault-scope gate as Query and Search.
// nil allowedScopes is a programmer error; use []ScopeID{} to deny all.
type NoteBatcher interface {
	CreateBatch(ctx context.Context, inputs []domain.CreateInput, allowedScopes []domain.ScopeID) (domain.BatchResult, error)
	UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput, allowedScopes []domain.ScopeID) (domain.BatchResult, error)
	PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput, allowedScopes []domain.ScopeID) (domain.BatchResult, error)
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
