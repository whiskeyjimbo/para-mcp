package semantic

import (
	"context"
	"errors"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

// ErrNotImplemented is returned by stub implementations pending a real backend.
var ErrNotImplemented = errors.New("not implemented")

var _ ports.Reranker = (*StubReranker)(nil)

// StubReranker satisfies the Reranker port but always returns ErrNotImplemented.
type StubReranker struct{}

func (StubReranker) Rerank(_ context.Context, _ string, _ []domain.VectorHit) ([]domain.VectorHit, error) {
	return nil, ErrNotImplemented
}
