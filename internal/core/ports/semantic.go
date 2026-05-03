package ports

import (
	"context"
	"io"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

// Embedder converts text into dense vector representations.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dims() int
	ModelName() string
}

// VectorStore persists and queries dense vector records.
// AllowedScopes pre-filtering is enforced inside Search; callers must supply
// an AuthFilter with a non-nil AllowedScopes slice.
type VectorStore interface {
	io.Closer
	Upsert(ctx context.Context, records []domain.VectorRecord) error
	Search(ctx context.Context, query []float32, filter domain.AuthFilter, k int) ([]domain.VectorHit, error)
	Delete(ctx context.Context, ids []string) error
	Tombstone(ctx context.Context, ids []string) error
	// ListTombstoned returns up to limit IDs of soft-deleted records, used by the sweeper.
	ListTombstoned(ctx context.Context, limit int) ([]string, error)
}

// VectorTombstoner is the narrow interface required by the tombstone sweeper.
// Implementations of VectorStore satisfy this automatically.
type VectorTombstoner interface {
	Delete(ctx context.Context, ids []string) error
	Tombstone(ctx context.Context, ids []string) error
	ListTombstoned(ctx context.Context, limit int) ([]string, error)
}

// Summarizer generates DerivedMetadata for a note body.
type Summarizer interface {
	Summarize(ctx context.Context, ref domain.NoteRef, body string) (*domain.DerivedMetadata, error)
}

// Reranker re-scores vector hits given the original query string.
type Reranker interface {
	Rerank(ctx context.Context, query string, hits []domain.VectorHit) ([]domain.VectorHit, error)
}

// SemanticEnricher populates Derived and IndexState on a NoteSummary after a mutation.
// Pass nil to skip enrichment entirely.
type SemanticEnricher interface {
	Enrich(ctx context.Context, ref domain.NoteRef, sum *domain.NoteSummary)
}

// SemanticSearcher runs a pure vector query and returns hits already deduplicated
// to one-per-NoteRef with chunk-aggregated scores. Implementations are responsible
// for embedding the query, calling VectorStore.Search, applying the threshold
// floor, and trimming to opts.Limit. AllowedScopes pre-filtering is enforced
// inside VectorStore.Search; callers must supply a non-nil AllowedScopes slice
// (the NoteService boundary checks this).
type SemanticSearcher interface {
	SemanticSearch(ctx context.Context, query string, filter domain.AuthFilter, opts domain.SemanticSearchOptions) ([]domain.VectorHit, error)
}
