package semantic

import (
	"context"
	"errors"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infra/semantic/derived"
)

// DerivedEnricher populates Derived and IndexState on a NoteSummary by looking
// up the derived metadata store by NoteRef after a mutation. It satisfies the
// mcp.SemanticEnricher interface via Go structural typing.
type DerivedEnricher struct {
	store derived.Store
}

// NewDerivedEnricher returns a DerivedEnricher backed by store.
func NewDerivedEnricher(store derived.Store) *DerivedEnricher {
	return &DerivedEnricher{store: store}
}

// Enrich sets sum.Derived and sum.IndexState from the derived metadata store.
// A missing record sets IndexStatePending; a found record sets IndexStateIndexed.
// On unexpected errors the summary is left unchanged.
func (e *DerivedEnricher) Enrich(ctx context.Context, ref domain.NoteRef, sum *domain.NoteSummary) {
	meta, err := e.store.GetByRef(ctx, ref)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			sum.IndexState = domain.IndexStatePending
		}
		return
	}
	sum.Derived = meta
	sum.IndexState = domain.IndexStateIndexed
}
