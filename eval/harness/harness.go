// Package harness provides the eval harness for semantic search quality measurement.
// It defines the file format for queries and judgments, computes recall@k, and
// runs end-to-end scoring against a live or stub pipeline.
//
// File formats (JSON):
//
//	queries.json   — array of Query objects
//	judgments.json — array of QueryJudgment objects
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

// Query is a single search input with expected high-relevance targets.
type Query struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// JudgedResult is a human-labeled relevance score for one note against one query.
// Relevance: 0 = not relevant, 1 = somewhat relevant, 2 = highly relevant.
type JudgedResult struct {
	Ref       domain.NoteRef `json:"ref"`
	Relevance int            `json:"relevance"`
}

// QueryJudgment holds all judgments for one query.
type QueryJudgment struct {
	QueryID string         `json:"query_id"`
	Results []JudgedResult `json:"results"`
}

// LoadFixtures reads queries.json and judgments.json from dir.
func LoadFixtures(dir string) ([]Query, []QueryJudgment, error) {
	queries, err := loadJSON[[]Query](filepath.Join(dir, "queries.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("load queries: %w", err)
	}
	judgments, err := loadJSON[[]QueryJudgment](filepath.Join(dir, "judgments.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("load judgments: %w", err)
	}
	return queries, judgments, nil
}

// RecallAtK computes recall@k: the fraction of highly-relevant notes (relevance >= 2)
// that appear in the top-k results.
func RecallAtK(judgments []JudgedResult, results []domain.VectorHit, k int) float64 {
	relevant := make(map[domain.NoteRef]bool)
	for _, j := range judgments {
		if j.Relevance >= 2 {
			relevant[j.Ref] = true
		}
	}
	if len(relevant) == 0 {
		return 0.0
	}

	limit := min(k, len(results))

	found := 0
	for _, h := range results[:limit] {
		if relevant[h.Ref] {
			found++
		}
	}
	return float64(found) / float64(len(relevant))
}

// SearchFunc runs a semantic search for a query and returns the top hits.
type SearchFunc func(q Query) []domain.VectorHit

// EvalScore runs all queries through searchFn and returns the mean recall@k across queries.
// Queries with no highly-relevant judgments are excluded from the mean.
func EvalScore(queries []Query, judgments []QueryJudgment, searchFn SearchFunc, k int) float64 {
	jMap := make(map[string][]JudgedResult, len(judgments))
	for _, j := range judgments {
		jMap[j.QueryID] = j.Results
	}

	var total float64
	count := 0
	for _, q := range queries {
		jds := jMap[q.ID]
		hasRelevant := false
		for _, j := range jds {
			if j.Relevance >= 2 {
				hasRelevant = true
				break
			}
		}
		if !hasRelevant {
			continue
		}
		hits := searchFn(q)
		total += RecallAtK(jds, hits, k)
		count++
	}

	if count == 0 {
		return 0.0
	}
	return total / float64(count)
}

func loadJSON[T any](path string) (T, error) {
	var zero T
	f, err := os.Open(path)
	if err != nil {
		return zero, err
	}
	defer f.Close()
	var v T
	if err := json.NewDecoder(f).Decode(&v); err != nil {
		return zero, err
	}
	return v, nil
}
