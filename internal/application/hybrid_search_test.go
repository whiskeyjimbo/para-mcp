package application

import (
	"context"
	"testing"
	"time"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

// flushIndex gives the localvault BM25 index time to ingest async writes.
func flushIndex() { time.Sleep(50 * time.Millisecond) }

func TestHybridSearch_LexicalOnlyWhenNoSemantic(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	for _, n := range []string{"alpha", "beta", "gamma"} {
		_, err := svc.Create(ctx, domain.CreateInput{
			Path:        "projects/" + n + ".md",
			FrontMatter: domain.FrontMatter{Title: n},
			Body:        n + " body content",
		})
		if err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}
	flushIndex()
	out, err := svc.HybridSearch(ctx, "alpha",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.HybridSearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected lexical-only results when semantic infra is absent")
	}
	if out[0].Summary.Title != "alpha" {
		t.Errorf("expected alpha first, got %q", out[0].Summary.Title)
	}
}

func TestHybridSearch_FusesLexicalAndSemantic(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	mrLex, err := svc.Create(ctx, domain.CreateInput{
		Path: "projects/lexical.md", FrontMatter: domain.FrontMatter{Title: "Lexical hit"}, Body: "auth refactor specifics",
	})
	if err != nil {
		t.Fatalf("create lex: %v", err)
	}
	mrSem, err := svc.Create(ctx, domain.CreateInput{
		Path: "projects/semantic.md", FrontMatter: domain.FrontMatter{Title: "OIDC plan"}, Body: "single sign-on rollout",
	})
	if err != nil {
		t.Fatalf("create sem: %v", err)
	}
	ss := &stubSearcher{hits: []domain.VectorHit{{Ref: mrSem.Summary.Ref, Score: 0.95}}}
	WithSemanticSearcher(ss)(svc)

	flushIndex()
	out, err := svc.HybridSearch(ctx, "auth",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.HybridSearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var sawLex, sawSem bool
	for _, r := range out {
		if r.Summary.Ref == mrLex.Summary.Ref {
			sawLex = true
		}
		if r.Summary.Ref == mrSem.Summary.Ref {
			sawSem = true
		}
	}
	if !sawLex {
		t.Error("lexical hit missing from fused output")
	}
	if !sawSem {
		t.Error("semantic-only hit missing from fused output")
	}
	if ss.calledCount != 1 {
		t.Errorf("semantic searcher should be called once, got %d", ss.calledCount)
	}
}

func TestHybridSearch_ScoreUsesRRFConstants(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	mr, err := svc.Create(ctx, domain.CreateInput{
		Path:        "projects/widgets.md",
		FrontMatter: domain.FrontMatter{Title: "Widgets"},
		Body:        "widgets are durable artifacts in the catalogue",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ss := &stubSearcher{hits: []domain.VectorHit{{Ref: mr.Summary.Ref, Score: 0.9}}}
	WithSemanticSearcher(ss)(svc)
	flushIndex()
	out, err := svc.HybridSearch(ctx, "widgets",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.HybridSearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d", len(out))
	}
	// One lexical rank-1 + one semantic rank-1, both at alpha=0.5
	want := domain.RRFAlpha*(1.0/float64(domain.RRFK+1)) + (1.0-domain.RRFAlpha)*(1.0/float64(domain.RRFK+1))
	if out[0].Score < want-1e-9 || out[0].Score > want+1e-9 {
		t.Errorf("RRF score: got %v, want ~%v", out[0].Score, want)
	}
}

func TestHybridSearch_DeniedScopeReturnsEmpty(t *testing.T) {
	svc := newTestService(t)
	out, err := svc.HybridSearch(context.Background(), "q",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"team-eng"}},
		domain.HybridSearchOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty when scope denied, got %d", len(out))
	}
}

func TestHybridSearch_NilAllowedScopesIsProgrammerError(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.HybridSearch(context.Background(), "q",
		domain.AuthFilter{AllowedScopes: nil},
		domain.HybridSearchOptions{})
	if err == nil {
		t.Fatal("expected error for nil AllowedScopes")
	}
}

func TestHybridSearch_SemanticErrorDoesNotFailLexical(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	_, err := svc.Create(ctx, domain.CreateInput{
		Path: "projects/lexhit.md", FrontMatter: domain.FrontMatter{Title: "lexhit"}, Body: "auth refactor",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ss := &stubSearcher{err: context.DeadlineExceeded}
	WithSemanticSearcher(ss)(svc)
	flushIndex()
	out, err := svc.HybridSearch(ctx, "auth",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.HybridSearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("lexical results should survive semantic failure")
	}
}

func TestHybridSearch_DedupesBetweenLexicalAndSemantic(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	mr, err := svc.Create(ctx, domain.CreateInput{
		Path: "projects/dup.md", FrontMatter: domain.FrontMatter{Title: "dup"}, Body: "auth refactor",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ss := &stubSearcher{hits: []domain.VectorHit{{Ref: mr.Summary.Ref, Score: 0.9}}}
	WithSemanticSearcher(ss)(svc)
	flushIndex()
	out, err := svc.HybridSearch(ctx, "auth",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.HybridSearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	count := 0
	for _, r := range out {
		if r.Summary.Ref == mr.Summary.Ref {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ref appeared %d times in fused output, expected 1", count)
	}
}
