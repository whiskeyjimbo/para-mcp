package ports

import (
	"context"
	"sync"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

type scopeMemoKey struct{}

type scopeMemo struct {
	once   sync.Once
	result []domain.ScopeID
}

// WithScopeMemo returns a context carrying a memoization slot for scope resolution.
// Install this once per request (e.g. in HTTP middleware) so MemoScopeResolver can
// short-circuit redundant Scopes calls within the same request lifecycle.
func WithScopeMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, scopeMemoKey{}, &scopeMemo{})
}

// MemoScopeResolver wraps a ScopeResolver, returning a cached result within a
// single request context when the context carries a memo slot from WithScopeMemo.
// Without the slot (e.g. in tests that bypass middleware) it falls through to the
// inner resolver unchanged.
type MemoScopeResolver struct {
	inner ScopeResolver
}

// NewMemoScopeResolver wraps inner so that repeated Scopes calls within the same
// request context resolve at most once when the context carries a WithScopeMemo slot.
func NewMemoScopeResolver(inner ScopeResolver) *MemoScopeResolver {
	return &MemoScopeResolver{inner: inner}
}

func (m *MemoScopeResolver) Scopes(ctx context.Context) []domain.ScopeID {
	if memo, ok := ctx.Value(scopeMemoKey{}).(*scopeMemo); ok {
		memo.once.Do(func() { memo.result = m.inner.Scopes(ctx) })
		return memo.result
	}
	return m.inner.Scopes(ctx)
}

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
	SemanticSearch(ctx context.Context, query string, filter domain.AuthFilter, opts domain.SemanticSearchOptions) ([]domain.RankedNote, error)
	Stale(ctx context.Context, days int, categories []domain.Category, status string, limit int, filter domain.AuthFilter) (domain.QueryResult, error)
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
	// Replace atomically replaces a note's body and patches its frontmatter in a single
	// write. The note must already exist. NoteID and CreatedAt are preserved. ifMatch="" is unconditional.
	Replace(ctx context.Context, ref domain.NoteRef, fields map[string]any, body, ifMatch string) (domain.MutationResult, error)
	Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.MutationResult, error)
	Delete(ctx context.Context, ref domain.NoteRef, soft bool, ifMatch string) error
	Promote(ctx context.Context, in domain.PromoteInput) (domain.MutationResult, error)
}

// NoteBatcher is the batch-mutation slice of the NoteService port.
// filter.AllowedScopes enforces the same vault-scope gate as Query and Search.
// nil AllowedScopes is a programmer error; use []ScopeID{} to deny all.
type NoteBatcher interface {
	CreateBatch(ctx context.Context, inputs []domain.CreateInput, filter domain.AuthFilter) (domain.BatchResult, error)
	UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput, filter domain.AuthFilter) (domain.BatchResult, error)
	PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput, filter domain.AuthFilter) (domain.BatchResult, error)
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
