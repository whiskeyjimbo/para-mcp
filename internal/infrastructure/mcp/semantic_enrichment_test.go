package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

// stubEnricher implements SemanticEnricher for tests.
type stubEnricher struct {
	derived    *domain.DerivedMetadata
	indexState domain.IndexState
}

func (s *stubEnricher) Enrich(_ context.Context, _ domain.NoteRef, sum *domain.NoteSummary) {
	sum.Derived = s.derived
	sum.IndexState = s.indexState
}

func TestFlatMutationNoEnricher(t *testing.T) {
	res := domain.MutationResult{
		Summary: domain.NoteSummary{Ref: domain.NoteRef{Scope: "s", Path: "a.md"}},
		ETag:    "etag1",
	}
	mr := flatMutation(context.Background(), res, nil)
	if mr.ETag != "etag1" {
		t.Errorf("ETag = %q, want etag1", mr.ETag)
	}
	if mr.Generated {
		t.Error("Generated should be false when no enricher")
	}
	if mr.Warning != "" {
		t.Error("Warning should be empty when no enricher")
	}
}

func TestFlatMutationWithIndexedEnricher(t *testing.T) {
	res := domain.MutationResult{
		Summary: domain.NoteSummary{Ref: domain.NoteRef{Scope: "s", Path: "a.md"}},
		ETag:    "etag2",
	}
	enr := &stubEnricher{
		derived:    &domain.DerivedMetadata{Summary: "sum", Purpose: "p"},
		indexState: domain.IndexStateIndexed,
	}
	mr := flatMutation(context.Background(), res, enr)
	if !mr.Generated {
		t.Error("Generated should be true when derived content present")
	}
	if mr.Warning == "" {
		t.Error("Warning should be non-empty when derived content present")
	}
	if mr.IndexStateExplainer != "" {
		t.Error("IndexStateExplainer should be empty for indexed state")
	}
}

func TestFlatMutationWithPendingEnricher(t *testing.T) {
	res := domain.MutationResult{
		Summary: domain.NoteSummary{Ref: domain.NoteRef{Scope: "s", Path: "a.md"}},
		ETag:    "etag3",
	}
	enr := &stubEnricher{
		derived:    nil,
		indexState: domain.IndexStatePending,
	}
	mr := flatMutation(context.Background(), res, enr)
	if mr.Generated {
		t.Error("Generated should be false when no derived content")
	}
	if mr.IndexStateExplainer == "" {
		t.Error("IndexStateExplainer should be non-empty for pending state")
	}
}

func TestFlatMutationNonIndexedStatesHaveExplainer(t *testing.T) {
	nonIndexed := []domain.IndexState{
		domain.IndexStatePending,
		domain.IndexStateFailed,
		domain.IndexStateSkippedShort,
		domain.IndexStateSkippedUserEdited,
		domain.IndexStateTombstoned,
	}
	for _, s := range nonIndexed {
		res := domain.MutationResult{Summary: domain.NoteSummary{}}
		enr := &stubEnricher{indexState: s}
		mr := flatMutation(context.Background(), res, enr)
		if mr.IndexStateExplainer == "" {
			t.Errorf("IndexStateExplainer empty for state %q", s)
		}
	}
}

func TestFlatMutationJSONShape(t *testing.T) {
	res := domain.MutationResult{
		Summary: domain.NoteSummary{
			Ref:        domain.NoteRef{Scope: "s", Path: "a.md"},
			IndexState: domain.IndexStateIndexed,
		},
		ETag: "etag4",
	}
	enr := &stubEnricher{
		derived:    &domain.DerivedMetadata{Summary: "s", Purpose: "p"},
		indexState: domain.IndexStateIndexed,
	}
	mr := flatMutation(context.Background(), res, enr)
	b, err := json.Marshal(mr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["ETag"]; !ok {
		t.Error("ETag missing from JSON")
	}
	if _, ok := m["generated"]; !ok {
		t.Error("generated missing from JSON")
	}
	if _, ok := m["_warning"]; !ok {
		t.Error("_warning missing from JSON")
	}
}
