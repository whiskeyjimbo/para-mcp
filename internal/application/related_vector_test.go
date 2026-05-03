package application

import (
	"context"
	"testing"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

func TestRelated_FallbackHeuristicWhenNoSemantic(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	src, err := svc.Create(ctx, domain.CreateInput{
		Path:        "projects/src.md",
		FrontMatter: domain.FrontMatter{Title: "src", Area: "infra", Tags: []string{"aws", "go"}},
		Body:        "src body",
	})
	if err != nil {
		t.Fatalf("create src: %v", err)
	}
	_, err = svc.Create(ctx, domain.CreateInput{
		Path:        "projects/peer.md",
		FrontMatter: domain.FrontMatter{Title: "peer", Area: "infra", Tags: []string{"aws"}},
		Body:        "peer body",
	})
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	out, err := svc.Related(ctx, src.Summary.Ref, 10, domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 related (peer), got %d", len(out))
	}
	if out[0].Summary.Title != "peer" {
		t.Errorf("expected peer, got %q", out[0].Summary.Title)
	}
}

func TestRelated_VectorRankingWhenSemanticCapable(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	src, err := svc.Create(ctx, domain.CreateInput{
		Path:        "projects/src.md",
		FrontMatter: domain.FrontMatter{Title: "src", Area: "infra", Tags: []string{"aws"}},
		Body:        "long body for embedding",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	farTagOverlap, err := svc.Create(ctx, domain.CreateInput{
		Path:        "projects/heuristic-winner.md",
		FrontMatter: domain.FrontMatter{Title: "heuristic-winner", Area: "infra", Tags: []string{"aws"}},
		Body:        "irrelevant body",
	})
	if err != nil {
		t.Fatalf("create heuristic-winner: %v", err)
	}
	semWinner, err := svc.Create(ctx, domain.CreateInput{
		Path:        "areas/sem-winner.md",
		FrontMatter: domain.FrontMatter{Title: "sem-winner"},
		Body:        "vector-similar body",
	})
	if err != nil {
		t.Fatalf("create sem-winner: %v", err)
	}

	// Stub returns sem-winner with high score; heuristic-winner not in vector hits.
	ss := &stubSearcher{hits: []domain.VectorHit{
		{Ref: semWinner.Summary.Ref, Score: 0.95},
		// Include source -- service must filter it out.
		{Ref: src.Summary.Ref, Score: 0.99},
	}}
	WithSemanticSearcher(ss)(svc)

	out, err := svc.Related(ctx, src.Summary.Ref, 10, domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 result (sem-winner; source excluded), got %d", len(out))
	}
	if out[0].Summary.Ref != semWinner.Summary.Ref {
		t.Errorf("expected sem-winner, got %v", out[0].Summary.Ref)
	}
	// Heuristic-winner should not appear because semantic path took over.
	for _, r := range out {
		if r.Summary.Ref == farTagOverlap.Summary.Ref {
			t.Error("heuristic-winner should not appear when semantic ranking is in use")
		}
	}
}

func TestRelated_FallsBackOnSemanticError(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	src, err := svc.Create(ctx, domain.CreateInput{
		Path:        "projects/src.md",
		FrontMatter: domain.FrontMatter{Title: "src", Tags: []string{"aws"}},
		Body:        "body",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svc.Create(ctx, domain.CreateInput{
		Path:        "projects/peer.md",
		FrontMatter: domain.FrontMatter{Title: "peer", Tags: []string{"aws"}},
		Body:        "body",
	})
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	ss := &stubSearcher{err: context.DeadlineExceeded}
	WithSemanticSearcher(ss)(svc)

	out, err := svc.Related(ctx, src.Summary.Ref, 10, domain.AuthFilter{AllowedScopes: []domain.ScopeID{"personal"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 fallback related, got %d", len(out))
	}
}
