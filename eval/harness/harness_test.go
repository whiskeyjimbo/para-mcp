package harness_test

import (
	"testing"

	"github.com/whiskeyjimbo/para-mcp/eval/harness"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

func ref(scope, path string) domain.NoteRef {
	return domain.NoteRef{Scope: scope, Path: path}
}

func hit(scope, path string, score float64) domain.VectorHit {
	return domain.VectorHit{Ref: domain.NoteRef{Scope: scope, Path: path}, Score: score}
}

func TestScorePerfectRecall(t *testing.T) {
	judgments := []harness.JudgedResult{
		{Ref: ref("s", "a.md"), Relevance: 2},
		{Ref: ref("s", "b.md"), Relevance: 2},
	}
	results := []domain.VectorHit{
		hit("s", "a.md", 0.9),
		hit("s", "b.md", 0.8),
	}
	got := harness.RecallAtK(judgments, results, 5)
	if got != 1.0 {
		t.Errorf("RecallAtK = %v, want 1.0", got)
	}
}

func TestScoreZeroRecall(t *testing.T) {
	judgments := []harness.JudgedResult{
		{Ref: ref("s", "a.md"), Relevance: 2},
	}
	results := []domain.VectorHit{
		hit("s", "c.md", 0.9), // different note
	}
	got := harness.RecallAtK(judgments, results, 5)
	if got != 0.0 {
		t.Errorf("RecallAtK = %v, want 0.0", got)
	}
}

func TestScorePartialRecall(t *testing.T) {
	judgments := []harness.JudgedResult{
		{Ref: ref("s", "a.md"), Relevance: 2},
		{Ref: ref("s", "b.md"), Relevance: 2},
		{Ref: ref("s", "c.md"), Relevance: 2},
	}
	results := []domain.VectorHit{
		hit("s", "a.md", 0.9),
		// b.md and c.md missing
	}
	got := harness.RecallAtK(judgments, results, 5)
	const want = 1.0 / 3.0
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("RecallAtK = %v, want %v", got, want)
	}
}

func TestScoreIgnoresLowRelevance(t *testing.T) {
	judgments := []harness.JudgedResult{
		{Ref: ref("s", "a.md"), Relevance: 2}, // relevant
		{Ref: ref("s", "b.md"), Relevance: 0}, // not relevant — should not count
	}
	results := []domain.VectorHit{
		hit("s", "b.md", 0.95), // returned but not relevant
	}
	// a.md not returned → recall 0/1 = 0
	got := harness.RecallAtK(judgments, results, 5)
	if got != 0.0 {
		t.Errorf("RecallAtK = %v, want 0.0 (low-relevance items should not count)", got)
	}
}

func TestScoreKCutsOffResults(t *testing.T) {
	judgments := []harness.JudgedResult{
		{Ref: ref("s", "a.md"), Relevance: 2},
	}
	results := []domain.VectorHit{
		hit("s", "z.md", 0.9),
		hit("s", "a.md", 0.5), // at position 2, beyond k=1
	}
	got := harness.RecallAtK(judgments, results, 1)
	if got != 0.0 {
		t.Errorf("RecallAtK = %v, want 0.0 (a.md is beyond k=1)", got)
	}
}

func TestLoadFixtures(t *testing.T) {
	queries, judgments, err := harness.LoadFixtures("../../eval/fixtures")
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(queries) < 10 {
		t.Errorf("want ≥10 fixture queries, got %d", len(queries))
	}
	if len(judgments) == 0 {
		t.Error("want non-empty judgments")
	}
	// Every query must have a corresponding judgment entry.
	jMap := make(map[string]bool, len(judgments))
	for _, j := range judgments {
		jMap[j.QueryID] = true
	}
	for _, q := range queries {
		if !jMap[q.ID] {
			t.Errorf("query %q has no corresponding judgment", q.ID)
		}
	}
}

func TestEvalScoreReturnsBetweenZeroAndOne(t *testing.T) {
	queries, judgments, err := harness.LoadFixtures("../../eval/fixtures")
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	// With no search results (empty hits), recall should be 0.0.
	score := harness.EvalScore(queries, judgments, func(q harness.Query) []domain.VectorHit {
		return nil
	}, 10)
	if score < 0.0 || score > 1.0 {
		t.Errorf("EvalScore = %v, want value in [0, 1]", score)
	}
}
