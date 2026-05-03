// Package eval contains the regression-eval harness for paras retrieval tools.
// Metrics here are intentionally generic — a ranked list of doc IDs vs a set of
// known-relevant doc IDs — so the same harness can grade lexical, semantic, or
// hybrid output.
package eval

import "math"

// NDCGAt computes normalized discounted cumulative gain at cut-off k.
// `relevance` maps a doc ID to its graded relevance (>=0); missing IDs are 0.
// `ranked` is the ordered list returned by a retrieval system.
// Returns 0 when the ideal DCG is 0 (no relevant docs in the qrel set).
func NDCGAt(k int, ranked []string, relevance map[string]float64) float64 {
	if k <= 0 {
		return 0
	}
	dcg := 0.0
	for i, id := range ranked {
		if i >= k {
			break
		}
		rel := relevance[id]
		if rel == 0 {
			continue
		}
		dcg += (math.Pow(2, rel) - 1) / math.Log2(float64(i+2))
	}
	idealRels := topKRelevances(k, relevance)
	idcg := 0.0
	for i, rel := range idealRels {
		if rel == 0 {
			continue
		}
		idcg += (math.Pow(2, rel) - 1) / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// RecallAt computes recall at cut-off k against the binary qrel set
// (any doc with relevance > 0 counts as relevant).
func RecallAt(k int, ranked []string, relevance map[string]float64) float64 {
	if k <= 0 {
		return 0
	}
	totalRelevant := 0
	for _, r := range relevance {
		if r > 0 {
			totalRelevant++
		}
	}
	if totalRelevant == 0 {
		return 0
	}
	hit := 0
	for i, id := range ranked {
		if i >= k {
			break
		}
		if relevance[id] > 0 {
			hit++
		}
	}
	return float64(hit) / float64(totalRelevant)
}

// MRR is the reciprocal rank of the first relevant doc, or 0 if none appears.
func MRR(ranked []string, relevance map[string]float64) float64 {
	for i, id := range ranked {
		if relevance[id] > 0 {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// topKRelevances returns the top-k relevance grades from the qrel set, sorted
// descending. Used to construct the ideal-DCG denominator.
func topKRelevances(k int, relevance map[string]float64) []float64 {
	rels := make([]float64, 0, len(relevance))
	for _, r := range relevance {
		rels = append(rels, r)
	}
	// Insertion sort descending — eval qrel sets are tiny.
	for i := 1; i < len(rels); i++ {
		for j := i; j > 0 && rels[j] > rels[j-1]; j-- {
			rels[j], rels[j-1] = rels[j-1], rels[j]
		}
	}
	if len(rels) > k {
		rels = rels[:k]
	}
	return rels
}
