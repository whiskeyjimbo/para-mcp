package eval

import (
	"math"
	"testing"
)

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestNDCGAt_PerfectRanking(t *testing.T) {
	rel := map[string]float64{"a": 3, "b": 2, "c": 1}
	ranked := []string{"a", "b", "c", "d", "e"}
	got := NDCGAt(10, ranked, rel)
	if !approxEq(got, 1.0) {
		t.Errorf("perfect ranking should be 1.0, got %v", got)
	}
}

func TestNDCGAt_NoRelevantDocs(t *testing.T) {
	got := NDCGAt(10, []string{"a", "b"}, map[string]float64{})
	if got != 0 {
		t.Errorf("no qrels → 0, got %v", got)
	}
}

func TestNDCGAt_KnownValue(t *testing.T) {
	// Two relevant docs with grade 1; system ranks one at position 2 and one at position 4.
	// DCG  = (2^1-1)/log2(3) + (2^1-1)/log2(5) = 1/log2(3) + 1/log2(5)
	// IDCG = (2^1-1)/log2(2) + (2^1-1)/log2(3) = 1 + 1/log2(3)
	rel := map[string]float64{"x": 1, "y": 1}
	ranked := []string{"junk", "x", "junk2", "y"}
	dcg := 1.0/math.Log2(3) + 1.0/math.Log2(5)
	idcg := 1.0 + 1.0/math.Log2(3)
	want := dcg / idcg
	got := NDCGAt(10, ranked, rel)
	if !approxEq(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestNDCGAt_KCutsOff(t *testing.T) {
	rel := map[string]float64{"y": 1}
	ranked := []string{"junk", "junk", "junk", "y"}
	if got := NDCGAt(3, ranked, rel); got != 0 {
		t.Errorf("k=3 should not see position-4 result, got %v", got)
	}
	if got := NDCGAt(4, ranked, rel); got == 0 {
		t.Error("k=4 should see position-4 result")
	}
}

func TestRecallAt(t *testing.T) {
	rel := map[string]float64{"a": 1, "b": 1, "c": 1, "d": 1}
	ranked := []string{"a", "b", "x", "y"}
	if got := RecallAt(2, ranked, rel); !approxEq(got, 0.5) {
		t.Errorf("R@2 got %v, want 0.5", got)
	}
	if got := RecallAt(10, ranked, rel); !approxEq(got, 0.5) {
		t.Errorf("R@10 got %v, want 0.5", got)
	}
}

func TestRecallAt_EmptyQrels(t *testing.T) {
	if got := RecallAt(10, []string{"a"}, map[string]float64{}); got != 0 {
		t.Errorf("empty qrels → 0, got %v", got)
	}
}

func TestCompareToBaseline_NoRegression(t *testing.T) {
	base := map[QueryClass]ClassMetrics{
		ClassLexicalOverlap: {NDCG10: 0.9, Recall10: 0.85, MRR: 0.92},
	}
	cur := map[QueryClass]ClassMetrics{
		ClassLexicalOverlap: {NDCG10: 0.91, Recall10: 0.86, MRR: 0.92},
	}
	if reg := CompareToBaseline(cur, base, 0.05); len(reg) != 0 {
		t.Errorf("expected no regressions, got %v", reg)
	}
}

func TestCompareToBaseline_DetectsDrop(t *testing.T) {
	base := map[QueryClass]ClassMetrics{
		ClassLexicalOverlap: {NDCG10: 0.9, Recall10: 0.85, MRR: 0.92},
	}
	cur := map[QueryClass]ClassMetrics{
		ClassLexicalOverlap: {NDCG10: 0.7, Recall10: 0.85, MRR: 0.92},
	}
	reg := CompareToBaseline(cur, base, 0.05)
	if len(reg) == 0 {
		t.Fatal("expected regression on ndcg@10")
	}
}

func TestCompareToBaseline_ToleranceHonored(t *testing.T) {
	base := map[QueryClass]ClassMetrics{ClassLexicalOverlap: {NDCG10: 0.9, Recall10: 0.9, MRR: 0.9}}
	cur := map[QueryClass]ClassMetrics{ClassLexicalOverlap: {NDCG10: 0.86, Recall10: 0.9, MRR: 0.9}}
	if reg := CompareToBaseline(cur, base, 0.05); len(reg) != 0 {
		t.Errorf("0.04 drop should be inside 0.05 tolerance, got %v", reg)
	}
}

func TestCompareToBaseline_MissingClassFlags(t *testing.T) {
	base := map[QueryClass]ClassMetrics{ClassLexicalOverlap: {NDCG10: 0.9}}
	cur := map[QueryClass]ClassMetrics{}
	if reg := CompareToBaseline(cur, base, 0.05); len(reg) == 0 {
		t.Error("missing class should be flagged as regression")
	}
}

func TestMRR(t *testing.T) {
	rel := map[string]float64{"target": 1}
	if got := MRR([]string{"junk", "target"}, rel); !approxEq(got, 0.5) {
		t.Errorf("MRR got %v, want 0.5", got)
	}
	if got := MRR([]string{"target"}, rel); !approxEq(got, 1.0) {
		t.Errorf("MRR got %v, want 1.0", got)
	}
	if got := MRR([]string{"junk"}, rel); got != 0 {
		t.Errorf("MRR got %v, want 0", got)
	}
}
