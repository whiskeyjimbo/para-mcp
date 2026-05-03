package semantic

import (
	"context"
	"fmt"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic/scoring"
)

// Searcher adapts an Embedder + VectorStore pair to ports.SemanticSearcher.
// It embeds the query, calls VectorStore.Search with an over-fetch factor,
// aggregates chunk hits per-Ref, applies the threshold floor, and trims to
// the requested limit.
type Searcher struct {
	embedder  ports.Embedder
	vs        ports.VectorStore
	overFetch int
}

// NewSearcher constructs a Searcher. overFetch defaults to 4 when <= 0;
// VectorStore.Search is called with k = limit * overFetch to give the
// chunk-aggregation step a meaningful candidate pool.
func NewSearcher(embedder ports.Embedder, vs ports.VectorStore, overFetch int) *Searcher {
	if overFetch <= 0 {
		overFetch = 4
	}
	return &Searcher{embedder: embedder, vs: vs, overFetch: overFetch}
}

// SemanticSearch implements ports.SemanticSearcher.
func (s *Searcher) SemanticSearch(ctx context.Context, query string, filter domain.AuthFilter, opts domain.SemanticSearchOptions) ([]domain.VectorHit, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("semantic search: embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("semantic search: embedder returned no vectors")
	}
	k := limit * s.overFetch
	hits, err := s.vs.Search(ctx, vecs[0], filter, k)
	if err != nil {
		return nil, fmt.Errorf("semantic search: vector store: %w", err)
	}
	aggregated := scoring.AggregateChunkHits(hits)
	if opts.Threshold > 0 {
		filtered := aggregated[:0]
		for _, h := range aggregated {
			if h.Score >= opts.Threshold {
				filtered = append(filtered, h)
			}
		}
		aggregated = filtered
	}
	if len(aggregated) > limit {
		aggregated = aggregated[:limit]
	}
	return aggregated, nil
}
