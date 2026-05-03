package semantic

import (
	"context"
	"errors"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
)

// ErrNotImplemented is returned by stub implementations pending a real backend.
var ErrNotImplemented = errors.New("not implemented")

var _ ports.Reranker = (*StubReranker)(nil)

// StubReranker satisfies the Reranker port but always returns ErrNotImplemented.
// No production call site wires this in; it exists only to satisfy the interface
// in compositions that declare a Reranker field before a real backend is available.
type StubReranker struct{}

func (StubReranker) Rerank(_ context.Context, _ string, _ []domain.VectorHit) ([]domain.VectorHit, error) {
	return nil, ErrNotImplemented
}
