package application

import (
	"context"
	"errors"
	"testing"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
)

type stubSearcher struct {
	hits        []domain.VectorHit
	err         error
	gotQuery    string
	gotOpts     domain.SemanticSearchOptions
	gotAllowed  []domain.ScopeID
	calledCount int
}

func (s *stubSearcher) SemanticSearch(_ context.Context, query string, filter domain.AuthFilter, opts domain.SemanticSearchOptions) ([]domain.VectorHit, error) {
	s.calledCount++
	s.gotQuery = query
	s.gotOpts = opts
	s.gotAllowed = filter.AllowedScopes
	return s.hits, s.err
}

var _ ports.SemanticSearcher = (*stubSearcher)(nil)

func newSemSvcWithSearcher(t *testing.T, ss ports.SemanticSearcher) (*NoteService, domain.NoteRef) {
	t.Helper()
	svc := newTestService(t)
	if ss != nil {
		WithSemanticSearcher(ss)(svc)
	}
	ref := domain.NoteRef{Scope: "personal", Path: "projects/auth.md"}
	mr, err := svc.Create(context.Background(), domain.CreateInput{
		Path:        ref.Path,
		FrontMatter: domain.FrontMatter{Title: "Auth refactor"},
		Body:        "OIDC migration plan",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return svc, mr.Summary.Ref
}

func TestSemanticSearch_NilSearcherReturnsCapabilityUnavailable(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.SemanticSearch(context.Background(), "auth", domain.AuthFilter{
		AllowedScopes: []domain.ScopeID{"personal"},
	}, domain.SemanticSearchOptions{})
	if !errors.Is(err, domain.ErrCapabilityUnavailable) {
		t.Fatalf("expected ErrCapabilityUnavailable, got %v", err)
	}
}

func TestSemanticSearch_NilAllowedScopesIsProgrammerError(t *testing.T) {
	svc, _ := newSemSvcWithSearcher(t, &stubSearcher{})
	_, err := svc.SemanticSearch(context.Background(), "auth", domain.AuthFilter{AllowedScopes: nil}, domain.SemanticSearchOptions{})
	if err == nil {
		t.Fatal("nil AllowedScopes should error")
	}
}

func TestSemanticSearch_ScopeNotPermittedReturnsEmpty(t *testing.T) {
	ss := &stubSearcher{}
	svc, _ := newSemSvcWithSearcher(t, ss)
	results, err := svc.SemanticSearch(context.Background(), "auth", domain.AuthFilter{
		AllowedScopes: []domain.ScopeID{"team-eng"}, // not "personal"
	}, domain.SemanticSearchOptions{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty when scope not permitted, got %d", len(results))
	}
	if ss.calledCount != 0 {
		t.Fatal("searcher should not be called when scope is denied")
	}
}

func TestSemanticSearch_BodyNeverOmitsBody(t *testing.T) {
	ss := &stubSearcher{}
	svc, ref := newSemSvcWithSearcher(t, ss)
	ss.hits = []domain.VectorHit{{Ref: ref, Score: 0.9}}
	results, err := svc.SemanticSearch(context.Background(), "auth",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{BodyMode: domain.BodyNever})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Body != "" {
		t.Errorf("body=never should omit body, got %q", results[0].Body)
	}
	if results[0].Summary.Title != "Auth refactor" {
		t.Errorf("expected summary populated, got %q", results[0].Summary.Title)
	}
}

func TestSemanticSearch_BodyOnDemandLoadsBody(t *testing.T) {
	ss := &stubSearcher{}
	svc, ref := newSemSvcWithSearcher(t, ss)
	ss.hits = []domain.VectorHit{{Ref: ref, Score: 0.9}}
	results, err := svc.SemanticSearch(context.Background(), "auth",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{BodyMode: domain.BodyOnDemand})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Body == "" {
		t.Error("body=on_demand should load body")
	}
}

func TestSemanticSearch_BodyOnDemandWithoutThresholdCapsAtTop3(t *testing.T) {
	svc := newTestService(t)
	ss := &stubSearcher{}
	WithSemanticSearcher(ss)(svc)
	ctx := context.Background()
	var refs []domain.NoteRef
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		mr, err := svc.Create(ctx, domain.CreateInput{
			Path:        "projects/" + name + ".md",
			FrontMatter: domain.FrontMatter{Title: name},
			Body:        "body of " + name,
		})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		refs = append(refs, mr.Summary.Ref)
	}
	for _, r := range refs {
		ss.hits = append(ss.hits, domain.VectorHit{Ref: r, Score: 0.9})
	}
	results, err := svc.SemanticSearch(ctx, "q",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{BodyMode: domain.BodyOnDemand, Limit: 5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
	for i := 0; i < domain.BodyOnDemandTopK; i++ {
		if results[i].Body == "" {
			t.Errorf("result %d should have body when in top-%d", i, domain.BodyOnDemandTopK)
		}
	}
	for i := domain.BodyOnDemandTopK; i < len(results); i++ {
		if results[i].Body != "" {
			t.Errorf("result %d should NOT have body (beyond top-%d), got %q", i, domain.BodyOnDemandTopK, results[i].Body)
		}
	}
}

func TestSemanticSearch_BodyOnDemandWithThresholdLoadsAll(t *testing.T) {
	svc := newTestService(t)
	ss := &stubSearcher{}
	WithSemanticSearcher(ss)(svc)
	ctx := context.Background()
	for i, name := range []string{"a", "b", "c", "d"} {
		mr, err := svc.Create(ctx, domain.CreateInput{
			Path:        "projects/" + name + ".md",
			FrontMatter: domain.FrontMatter{Title: name},
			Body:        "body",
		})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ss.hits = append(ss.hits, domain.VectorHit{Ref: mr.Summary.Ref, Score: 0.9})
	}
	results, err := svc.SemanticSearch(ctx, "q",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{BodyMode: domain.BodyOnDemand, Threshold: 0.5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Body == "" {
			t.Errorf("result %d should have body when threshold is set", i)
		}
	}
}

func TestSemanticSearch_PassesOptionsThrough(t *testing.T) {
	ss := &stubSearcher{}
	svc, ref := newSemSvcWithSearcher(t, ss)
	ss.hits = []domain.VectorHit{{Ref: ref, Score: 0.9}}
	_, err := svc.SemanticSearch(context.Background(), "auth refactor",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{Limit: 25, Threshold: 0.7, BodyMode: domain.BodyNever})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ss.gotQuery != "auth refactor" {
		t.Errorf("query: got %q", ss.gotQuery)
	}
	if ss.gotOpts.Limit != 25 {
		t.Errorf("Limit: got %d, want 25", ss.gotOpts.Limit)
	}
	if ss.gotOpts.Threshold != 0.7 {
		t.Errorf("Threshold: got %v, want 0.7", ss.gotOpts.Threshold)
	}
	if ss.gotOpts.BodyMode != domain.BodyNever {
		t.Errorf("BodyMode: got %q", ss.gotOpts.BodyMode)
	}
}

func TestSemanticSearch_DefaultLimitWhenZero(t *testing.T) {
	ss := &stubSearcher{}
	svc, _ := newSemSvcWithSearcher(t, ss)
	_, err := svc.SemanticSearch(context.Background(), "q",
		domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}},
		domain.SemanticSearchOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ss.gotOpts.Limit != 10 {
		t.Errorf("default limit: got %d, want 10", ss.gotOpts.Limit)
	}
}
