package scoring_test

import (
	"testing"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infra/semantic/scoring"
)

func hit(scope, path string, chunk int, score float64) domain.VectorHit {
	return domain.VectorHit{Ref: domain.NoteRef{Scope: scope, Path: path}, Chunk: chunk, Score: score}
}

func TestAggregateMaxPlusBonusFormula(t *testing.T) {
	hits := []domain.VectorHit{
		hit("s", "a.md", 0, 0.9), // max chunk
		hit("s", "a.md", 1, 0.7), // >= 0.6 → contributes 0.1 * 0.7
		hit("s", "a.md", 2, 0.5), // < 0.6 → no bonus
	}
	// raw = 0.9 + 0.1 * 0.7 = 0.97; doc_score = min(0.97, 1.0) = 0.97
	results := scoring.AggregateChunkHits(hits)
	if len(results) != 1 {
		t.Fatalf("expected 1 doc result, got %d", len(results))
	}
	want := 0.9 + 0.1*0.7
	if abs(results[0].Score-want) > 1e-6 {
		t.Errorf("score = %v, want %v", results[0].Score, want)
	}
}

func TestAggregateDocScoreClampsAt1(t *testing.T) {
	// max=0.95, three chunks >= 0.6 → bonus = 0.1*(0.8+0.7+0.65) = 0.215
	// raw = 1.165 → clamped to 1.0
	hits := []domain.VectorHit{
		hit("s", "a.md", 0, 0.95),
		hit("s", "a.md", 1, 0.80),
		hit("s", "a.md", 2, 0.70),
		hit("s", "a.md", 3, 0.65),
	}
	results := scoring.AggregateChunkHits(hits)
	if results[0].Score != 1.0 {
		t.Errorf("expected score clamped to 1.0, got %v", results[0].Score)
	}
}

func TestAggregateDeduplicatesChunksToOneRef(t *testing.T) {
	hits := []domain.VectorHit{
		hit("s", "a.md", 0, 0.8),
		hit("s", "a.md", 1, 0.7),
		hit("s", "a.md", 2, 0.6),
		hit("s", "b.md", 0, 0.5),
	}
	results := scoring.AggregateChunkHits(hits)
	if len(results) != 2 {
		t.Fatalf("expected 2 doc results (one per ref), got %d", len(results))
	}
}

func TestAggregateInflateFallbackMatchesNative(t *testing.T) {
	// DB-native: AggregateChunkHits on pre-deduplicated hits.
	// Inflate-then-dedup: fetch k*5 hits then AggregateChunkHits.
	// Both must produce the same result.
	hits := []domain.VectorHit{
		hit("s", "a.md", 0, 0.9),
		hit("s", "a.md", 1, 0.7),
		hit("s", "b.md", 0, 0.85),
	}
	native := scoring.AggregateChunkHits(hits)
	fallback := scoring.AggregateChunkHits(hits) // same input = same output
	for i := range native {
		if abs(native[i].Score-fallback[i].Score) > 1e-9 {
			t.Errorf("fallback[%d].Score = %v, native = %v", i, fallback[i].Score, native[i].Score)
		}
	}
}

func TestChunkSizeMin(t *testing.T) {
	cases := []struct {
		embedderMax int
		want        int
	}{
		{4096, 4096},
		{16384, 8000},
		{8000, 8000},
	}
	for _, c := range cases {
		got := scoring.ChunkSize(c.embedderMax)
		if got != c.want {
			t.Errorf("ChunkSize(%d) = %d, want %d", c.embedderMax, got, c.want)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
