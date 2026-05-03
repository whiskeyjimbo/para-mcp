package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// QueryClass groups queries by retrieval challenge type. New classes can be
// added freely; the gate runs per-class so a new class never regresses the
// existing ones.
type QueryClass string

const (
	ClassLexicalOverlap QueryClass = "lexical-overlap"
	ClassParaphrase     QueryClass = "paraphrase"
	ClassMultiHop       QueryClass = "multi-hop"
)

// Doc is a corpus document available to the harness's retrieval target.
type Doc struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Query is one evaluation case.
type Query struct {
	ID        string             `json:"id"`
	Class     QueryClass         `json:"class"`
	Text      string             `json:"text"`
	Relevance map[string]float64 `json:"relevance"` // doc ID → graded relevance
}

// Fixture pairs a corpus with the queries that exercise it.
type Fixture struct {
	Corpus  []Doc   `json:"corpus"`
	Queries []Query `json:"queries"`
}

// Searcher abstracts the retrieval target under evaluation. Implementations
// return a ranked list of doc IDs (highest relevance first).
type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]string, error)
}

// SearcherFunc adapts a function to Searcher.
type SearcherFunc func(ctx context.Context, query string, limit int) ([]string, error)

// Search implements Searcher.
func (f SearcherFunc) Search(ctx context.Context, query string, limit int) ([]string, error) {
	return f(ctx, query, limit)
}

// ClassMetrics holds the per-class averaged retrieval metrics.
type ClassMetrics struct {
	NDCG10   float64 `json:"ndcg_10"`
	Recall10 float64 `json:"recall_10"`
	MRR      float64 `json:"mrr"`
	Queries  int     `json:"queries"`
}

// Run evaluates s against fx and returns metrics keyed by query class.
func Run(ctx context.Context, fx Fixture, s Searcher) (map[QueryClass]ClassMetrics, error) {
	type accum struct {
		ndcg, recall, mrr float64
		n                 int
	}
	totals := map[QueryClass]*accum{}
	for _, q := range fx.Queries {
		ranked, err := s.Search(ctx, q.Text, 10)
		if err != nil {
			return nil, fmt.Errorf("search %q: %w", q.ID, err)
		}
		a, ok := totals[q.Class]
		if !ok {
			a = &accum{}
			totals[q.Class] = a
		}
		a.ndcg += NDCGAt(10, ranked, q.Relevance)
		a.recall += RecallAt(10, ranked, q.Relevance)
		a.mrr += MRR(ranked, q.Relevance)
		a.n++
	}
	out := make(map[QueryClass]ClassMetrics, len(totals))
	for class, a := range totals {
		if a.n == 0 {
			continue
		}
		out[class] = ClassMetrics{
			NDCG10:   a.ndcg / float64(a.n),
			Recall10: a.recall / float64(a.n),
			MRR:      a.mrr / float64(a.n),
			Queries:  a.n,
		}
	}
	return out, nil
}

// LoadFixture reads a JSON fixture file from disk.
func LoadFixture(path string) (Fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("read fixture %q: %w", path, err)
	}
	var fx Fixture
	if err := json.Unmarshal(b, &fx); err != nil {
		return Fixture{}, fmt.Errorf("parse fixture %q: %w", path, err)
	}
	return fx, nil
}

// LoadBaseline reads a JSON baseline file.
func LoadBaseline(path string) (map[QueryClass]ClassMetrics, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %q: %w", path, err)
	}
	var out map[QueryClass]ClassMetrics
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse baseline %q: %w", path, err)
	}
	return out, nil
}

// CompareToBaseline returns a regression message for each (class, metric) pair
// where current is below baseline by more than `tolerance` (absolute drop).
// An empty slice means no regressions.
func CompareToBaseline(current, baseline map[QueryClass]ClassMetrics, tolerance float64) []string {
	var regressions []string
	for class, base := range baseline {
		cur, ok := current[class]
		if !ok {
			regressions = append(regressions, fmt.Sprintf("class %q missing from current run (baseline expected)", class))
			continue
		}
		check := func(name string, b, c float64) {
			if c < b-tolerance {
				regressions = append(regressions,
					fmt.Sprintf("%s %s: %.4f < baseline %.4f - tol %.4f", class, name, c, b, tolerance))
			}
		}
		check("ndcg@10", base.NDCG10, cur.NDCG10)
		check("recall@10", base.Recall10, cur.Recall10)
		check("mrr", base.MRR, cur.MRR)
	}
	return regressions
}
