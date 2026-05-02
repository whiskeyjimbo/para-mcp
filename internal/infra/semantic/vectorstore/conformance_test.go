// Package vectorstore_test contains shared conformance tests for VectorStore backends.
// Each backend must pass the same isolation and correctness guarantees.
package vectorstore_test

import (
	"context"
	"testing"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

// runConformanceTests exercises a VectorStore against the AllowedScopes ACL contract.
// Call this from each backend's own _test.go after setting up the store.
func runConformanceTests(t *testing.T, store ports.VectorStore, dims int) {
	t.Helper()

	scopeA := domain.ScopeID("scope-a")
	scopeB := domain.ScopeID("scope-b")

	refA := domain.NoteRef{Scope: scopeA, Path: "projects/a.md"}
	refB := domain.NoteRef{Scope: scopeB, Path: "projects/b.md"}

	vecA := makeVec(dims, 1.0)
	vecB := makeVec(dims, 0.5)

	// Upsert two records in different scopes.
	if err := store.Upsert(context.Background(), []domain.VectorRecord{
		{ID: "note-a", Ref: refA, Chunk: 0, Vector: vecA, Body: "note a body"},
		{ID: "note-b", Ref: refB, Chunk: 0, Vector: vecB, Body: "note b body"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// User A can only see scope-a.
	t.Run("scope_isolation_user_a_cannot_see_scope_b", func(t *testing.T) {
		hits, err := store.Search(context.Background(), vecA, domain.AuthFilter{
			Filter:        domain.Filter{},
			AllowedScopes: []domain.ScopeID{scopeA},
		}, 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		for _, h := range hits {
			if h.Ref.Scope == scopeB {
				t.Errorf("user A received note from scope-b: %+v", h)
			}
		}
	})

	// nil AllowedScopes is a programmer error → internal error, not silent bypass.
	t.Run("nil_allowed_scopes_returns_error", func(t *testing.T) {
		_, err := store.Search(context.Background(), vecA, domain.AuthFilter{
			Filter:        domain.Filter{},
			AllowedScopes: nil,
		}, 10)
		if err == nil {
			t.Fatal("expected error for nil AllowedScopes, got nil")
		}
	})

	// Empty AllowedScopes → empty result (deny everything).
	t.Run("empty_allowed_scopes_returns_empty", func(t *testing.T) {
		hits, err := store.Search(context.Background(), vecA, domain.AuthFilter{
			Filter:        domain.Filter{},
			AllowedScopes: []domain.ScopeID{},
		}, 10)
		if err != nil {
			t.Fatalf("Search with empty scopes: %v", err)
		}
		if len(hits) != 0 {
			t.Errorf("expected empty result for empty scopes, got %d hits", len(hits))
		}
	})

	// Upsert idempotency.
	t.Run("upsert_idempotent", func(t *testing.T) {
		err := store.Upsert(context.Background(), []domain.VectorRecord{
			{ID: "note-a", Ref: refA, Chunk: 0, Vector: vecA, Body: "updated"},
		})
		if err != nil {
			t.Fatalf("second Upsert: %v", err)
		}
	})

	// Delete idempotency.
	t.Run("delete_idempotent", func(t *testing.T) {
		if err := store.Delete(context.Background(), []string{"note-a"}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		// Second delete must not error.
		if err := store.Delete(context.Background(), []string{"note-a"}); err != nil {
			t.Fatalf("second Delete: %v", err)
		}
	})

	// After delete, note-a no longer appears in searches.
	t.Run("deleted_note_not_returned", func(t *testing.T) {
		hits, err := store.Search(context.Background(), vecA, domain.AuthFilter{
			Filter:        domain.Filter{},
			AllowedScopes: []domain.ScopeID{scopeA},
		}, 10)
		if err != nil {
			t.Fatalf("Search after delete: %v", err)
		}
		for _, h := range hits {
			if h.ID == "note-a" {
				t.Errorf("deleted note-a still appears in results")
			}
		}
	})
}

// makeVec produces a unit-normalised vector of given dims.
func makeVec(dims int, fill float32) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = fill
	}
	return v
}
