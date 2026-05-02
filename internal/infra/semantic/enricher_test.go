package semantic

import (
	"context"
	"errors"
	"testing"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

type stubEnricherStore struct {
	meta *domain.DerivedMetadata
	err  error
}

func (s *stubEnricherStore) GetByRef(_ context.Context, _ domain.NoteRef) (*domain.DerivedMetadata, error) {
	return s.meta, s.err
}
func (s *stubEnricherStore) Get(_ context.Context, _ string) (*domain.DerivedMetadata, error) {
	return nil, domain.ErrNotFound
}
func (s *stubEnricherStore) Set(_ context.Context, _ string, _ domain.NoteRef, _ *domain.DerivedMetadata) error {
	return nil
}
func (s *stubEnricherStore) IsEditedByUser(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestDerivedEnricher_Indexed(t *testing.T) {
	meta := &domain.DerivedMetadata{Summary: "test summary"}
	enr := NewDerivedEnricher(&stubEnricherStore{meta: meta})
	var sum domain.NoteSummary
	enr.Enrich(context.Background(), domain.NoteRef{Scope: "personal", Path: "note.md"}, &sum)
	if sum.IndexState != domain.IndexStateIndexed {
		t.Fatalf("want IndexStateIndexed, got %q", sum.IndexState)
	}
	if sum.Derived == nil || sum.Derived.Summary != "test summary" {
		t.Fatalf("Derived not populated: %+v", sum.Derived)
	}
}

func TestDerivedEnricher_NotFound_SetsPending(t *testing.T) {
	enr := NewDerivedEnricher(&stubEnricherStore{err: domain.ErrNotFound})
	var sum domain.NoteSummary
	enr.Enrich(context.Background(), domain.NoteRef{Scope: "personal", Path: "note.md"}, &sum)
	if sum.IndexState != domain.IndexStatePending {
		t.Fatalf("want IndexStatePending, got %q", sum.IndexState)
	}
	if sum.Derived != nil {
		t.Fatalf("Derived should be nil")
	}
}

func TestDerivedEnricher_UnexpectedError_LeavesUnchanged(t *testing.T) {
	enr := NewDerivedEnricher(&stubEnricherStore{err: errors.New("db down")})
	sum := domain.NoteSummary{IndexState: domain.IndexStateIndexed}
	enr.Enrich(context.Background(), domain.NoteRef{}, &sum)
	if sum.IndexState != domain.IndexStateIndexed {
		t.Fatalf("unexpected error should leave IndexState unchanged, got %q", sum.IndexState)
	}
}
