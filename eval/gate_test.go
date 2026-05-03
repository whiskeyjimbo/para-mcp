package eval_test

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/whiskeyjimbo/para-mcp/eval"
	"github.com/whiskeyjimbo/para-mcp/internal/application"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/localvault"
)

const (
	fixturePath  = "fixtures.json"
	baselinePath = "baseline.json"
	// Tolerance is a per-metric absolute floor below baseline that a run is
	// allowed to drop without failing. Tighten after measurement stabilises.
	regressionTolerance = 0.05
)

// updateBaseline rewrites baseline.json with the current run's metrics. To use:
//
//	go test ./eval -update-baseline
//
// Commit the resulting baseline.json change in a dedicated PR.
var updateBaseline = flag.Bool("update-baseline", false, "rewrite eval/baseline.json from the current run")

// loadCorpusIntoVault populates a fresh localvault with the fixture corpus.
// Each doc is created at the path "<id>.md" (eval IDs are url-safe by design).
func loadCorpusIntoVault(t *testing.T, fx eval.Fixture) (*application.NoteService, map[string]string) {
	t.Helper()
	v, err := localvault.New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	svc := application.NewService(v)
	pathByID := make(map[string]string, len(fx.Corpus))
	for _, d := range fx.Corpus {
		path := "projects/" + d.ID + ".md"
		_, err := svc.Create(context.Background(), domain.CreateInput{
			Path:        path,
			FrontMatter: domain.FrontMatter{Title: d.Title},
			Body:        d.Body,
		})
		if err != nil {
			t.Fatalf("seed %q: %v", d.ID, err)
		}
		pathByID[d.ID] = path
	}
	// Localvault BM25 ingest is async; give it a moment to flush.
	time.Sleep(100 * time.Millisecond)
	return svc, pathByID
}

// lexicalSearcher routes eval queries through NoteService.Search.
// IDs returned correspond to fixture corpus IDs (the part of the path before .md).
func lexicalSearcher(svc *application.NoteService, pathByID map[string]string) eval.Searcher {
	idByPath := make(map[string]string, len(pathByID))
	for id, p := range pathByID {
		idByPath[p] = id
	}
	return eval.SearcherFunc(func(ctx context.Context, q string, limit int) ([]string, error) {
		results, err := svc.Search(ctx, q, domain.AuthFilter{
			AllowedScopes: []domain.ScopeID{"personal"},
		}, limit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(results))
		for _, r := range results {
			if id, ok := idByPath[r.Summary.Ref.Path]; ok {
				out = append(out, id)
			}
		}
		return out, nil
	})
}

// TestEvalGate enforces that the lexical retrieval path does not regress
// per-class against the baseline. When the underlying retrieval improves and
// a new baseline is desired, run with -update-baseline and commit the diff.
func TestEvalGate(t *testing.T) {
	fx, err := eval.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	svc, pathByID := loadCorpusIntoVault(t, fx)
	current, err := eval.Run(context.Background(), fx, lexicalSearcher(svc, pathByID))
	if err != nil {
		t.Fatalf("eval run: %v", err)
	}

	if *updateBaseline {
		writeBaseline(t, current)
		t.Logf("baseline updated with %d classes", len(current))
		return
	}

	baseline, err := eval.LoadBaseline(baselinePath)
	if err != nil {
		t.Fatalf("load baseline: %v (rerun with -update-baseline to seed it)", err)
	}
	regressions := eval.CompareToBaseline(current, baseline, regressionTolerance)
	for class, m := range current {
		t.Logf("%-20s ndcg@10=%.4f recall@10=%.4f mrr=%.4f n=%d",
			class, m.NDCG10, m.Recall10, m.MRR, m.Queries)
	}
	if len(regressions) > 0 {
		for _, r := range regressions {
			t.Errorf("regression: %s", r)
		}
	}
}

func writeBaseline(t *testing.T, m map[eval.QueryClass]eval.ClassMetrics) {
	t.Helper()
	classes := make([]string, 0, len(m))
	for c := range m {
		classes = append(classes, string(c))
	}
	sort.Strings(classes)
	ordered := make(map[string]eval.ClassMetrics, len(m))
	for _, c := range classes {
		ordered[c] = m[eval.QueryClass(c)]
	}
	b, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b = append(b, '\n')
	abs, _ := filepath.Abs(baselinePath)
	if err := os.WriteFile(baselinePath, b, 0o644); err != nil {
		t.Fatalf("write baseline %s: %v", abs, err)
	}
}
