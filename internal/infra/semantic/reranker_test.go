package semantic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infra/semantic"
)

func TestStubRerankerReturnsErrNotImplemented(t *testing.T) {
	r := semantic.StubReranker{}
	hits, err := r.Rerank(context.Background(), "query", []domain.VectorHit{})
	if !errors.Is(err, semantic.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
	if hits != nil {
		t.Fatalf("expected nil hits, got %v", hits)
	}
}
