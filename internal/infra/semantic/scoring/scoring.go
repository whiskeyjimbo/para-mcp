// Package scoring implements chunk-to-document score aggregation for semantic search.
package scoring

import (
	"slices"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

const (
	// maxChunkTokens is the maximum chunk size in tokens before overlap splitting.
	maxChunkTokens = 8000
	// chunkOverlap is the overlap in tokens between adjacent chunks.
	ChunkOverlap = 200
	// bonusThreshold is the minimum score for a secondary chunk to contribute to the bonus.
	bonusThreshold = 0.6
	// bonusWeight is the multiplier applied to qualifying secondary chunk scores.
	bonusWeight = 0.1
)

// ChunkSize returns the chunk size in tokens: min(embedderMax, 8000).
func ChunkSize(embedderMax int) int {
	if embedderMax < maxChunkTokens {
		return embedderMax
	}
	return maxChunkTokens
}

// AggregateChunkHits collapses chunk-level VectorHits to one result per NoteRef.
// Formula: raw = max(chunk_score) + 0.1 * Σ(other_chunks >= 0.6)
//
//	doc_score = min(raw, 1.0)
//
// Results are sorted by doc_score descending.
func AggregateChunkHits(hits []domain.VectorHit) []domain.VectorHit {
	// Group hits by ref.
	type group struct {
		ref   domain.NoteRef
		chunk domain.VectorHit
		sum   float64
	}
	byRef := map[domain.NoteRef]*group{}
	for _, h := range hits {
		g, ok := byRef[h.Ref]
		if !ok {
			g = &group{ref: h.Ref, chunk: h}
			byRef[h.Ref] = g
		}
		// Track max chunk separately.
		if h.Score > g.chunk.Score {
			g.chunk = h
		}
	}
	// Compute bonus from non-max chunks >= threshold.
	for _, h := range hits {
		g := byRef[h.Ref]
		if h.Chunk == g.chunk.Chunk {
			continue // skip the max chunk itself
		}
		if h.Score >= bonusThreshold {
			g.sum += h.Score
		}
	}

	// Build result slice.
	out := make([]domain.VectorHit, 0, len(byRef))
	for _, g := range byRef {
		raw := g.chunk.Score + bonusWeight*g.sum
		score := min(raw, 1.0)
		r := g.chunk
		r.Score = score
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b domain.VectorHit) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		return 0
	})
	return out
}
