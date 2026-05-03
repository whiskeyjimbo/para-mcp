package domain

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestHasTag(t *testing.T) {
	tags := []string{"aws", "go", "infra"}
	if !HasTag(tags, "AWS") {
		t.Error("HasTag should be case-insensitive")
	}
	if !HasTag(tags, "go") {
		t.Error("HasTag should find exact match")
	}
	if HasTag(tags, "python") {
		t.Error("HasTag should return false for missing tag")
	}
	if HasTag(nil, "aws") {
		t.Error("HasTag on nil should return false")
	}
}

func TestMatchesFilter_ArchiveExclusion(t *testing.T) {
	n := NoteSummary{Category: Archives, Status: "archived"}
	if MatchesFilter(n, Filter{}) {
		t.Error("archives should be excluded without IncludeArchives")
	}
	if !MatchesFilter(n, Filter{IncludeArchives: true}) {
		t.Error("archives should be included with IncludeArchives")
	}
	if !MatchesFilter(n, Filter{Categories: []Category{Archives}}) {
		t.Error("archives should be included when explicitly requested")
	}
}

func TestMatchesFilter_Status(t *testing.T) {
	n := NoteSummary{Category: Projects, Status: "active"}
	if !MatchesFilter(n, Filter{Status: "ACTIVE"}) {
		t.Error("status match should be case-insensitive")
	}
	if MatchesFilter(n, Filter{Status: "inactive"}) {
		t.Error("wrong status should not match")
	}
}

func TestMatchesFilter_Tags(t *testing.T) {
	n := NoteSummary{Category: Projects, Tags: []string{"aws", "go"}}
	if !MatchesFilter(n, Filter{Tags: []string{"AWS", "go"}}) {
		t.Error("all-of tags should match case-insensitively")
	}
	if MatchesFilter(n, Filter{Tags: []string{"aws", "python"}}) {
		t.Error("all-of tags: missing tag should exclude note")
	}
	if !MatchesFilter(n, Filter{AnyTags: []string{"python", "go"}}) {
		t.Error("any-of tags should match on partial overlap")
	}
	if MatchesFilter(n, Filter{AnyTags: []string{"python", "ruby"}}) {
		t.Error("any-of tags: no overlap should exclude note")
	}
}

func TestMatchesFilter_UpdatedAfterBefore(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	n := NoteSummary{Category: Projects, UpdatedAt: now}

	if !MatchesFilter(n, Filter{UpdatedAfter: &past}) {
		t.Error("note updated after past should match")
	}
	if MatchesFilter(n, Filter{UpdatedAfter: &future}) {
		t.Error("note updated before future threshold should not match")
	}
	if !MatchesFilter(n, Filter{UpdatedBefore: &future}) {
		t.Error("note updated before future should match")
	}
	if MatchesFilter(n, Filter{UpdatedBefore: &past}) {
		t.Error("note updated after past threshold should not match")
	}
}

func TestApplyFilter(t *testing.T) {
	notes := []NoteSummary{
		{Category: Projects, Status: "active"},
		{Category: Areas, Status: "inactive"},
		{Category: Archives, Status: "archived"},
	}
	result := ApplyFilter(notes, Filter{Status: "active"})
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
}

func TestSortSummaries_ByUpdated(t *testing.T) {
	now := time.Now()
	notes := []NoteSummary{
		{Title: "B", UpdatedAt: now.Add(-time.Hour)},
		{Title: "A", UpdatedAt: now},
	}
	SortSummaries(notes, SortByUpdated, false)
	if notes[0].Title != "B" {
		t.Errorf("expected B first (older), got %s", notes[0].Title)
	}
	SortSummaries(notes, SortByUpdated, true)
	if notes[0].Title != "A" {
		t.Errorf("expected A first (newer) in desc, got %s", notes[0].Title)
	}
}

func TestFilter_PurposeAndEntities_ZeroValues(t *testing.T) {
	var f Filter
	if f.Purpose != "" {
		t.Errorf("zero-value Purpose should be empty, got %q", f.Purpose)
	}
	if f.Entities != nil {
		t.Errorf("zero-value Entities should be nil, got %v", f.Entities)
	}
}

func TestFilter_PurposeAndEntities_Set(t *testing.T) {
	f := Filter{Purpose: "research", Entities: []string{"acme", "bigco"}}
	if f.Purpose != "research" {
		t.Errorf("Purpose = %q, want research", f.Purpose)
	}
	if !reflect.DeepEqual(f.Entities, []string{"acme", "bigco"}) {
		t.Errorf("Entities = %v, want [acme bigco]", f.Entities)
	}
}

func TestFilter_JSONRoundTrip_PreservesPurposeAndEntities(t *testing.T) {
	in := Filter{
		Status:   "active",
		Purpose:  "discovery",
		Entities: []string{"openai", "anthropic"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Filter
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Purpose != in.Purpose {
		t.Errorf("Purpose round-trip: got %q, want %q", out.Purpose, in.Purpose)
	}
	if !reflect.DeepEqual(out.Entities, in.Entities) {
		t.Errorf("Entities round-trip: got %v, want %v", out.Entities, in.Entities)
	}
	if out.Status != in.Status {
		t.Errorf("Status (regression): got %q, want %q", out.Status, in.Status)
	}
}

func TestFilter_UnmarshalOmittedFieldsAreZeroValued(t *testing.T) {
	var f Filter
	if err := json.Unmarshal([]byte(`{"Status":"active"}`), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.Purpose != "" {
		t.Errorf("Purpose should be zero when omitted, got %q", f.Purpose)
	}
	if f.Entities != nil {
		t.Errorf("Entities should be nil when omitted, got %v", f.Entities)
	}
	if f.Status != "active" {
		t.Errorf("Status (regression): got %q, want active", f.Status)
	}
}

func TestMatchesFilter_PurposeAndEntitiesAreContractOnly(t *testing.T) {
	n := NoteSummary{Category: Projects, Status: "active"}
	withPurpose := Filter{Purpose: "anything"}
	if !MatchesFilter(n, withPurpose) {
		t.Error("Purpose is contract-only at this stage; should not affect matching")
	}
	withEntities := Filter{Entities: []string{"anything"}}
	if !MatchesFilter(n, withEntities) {
		t.Error("Entities is contract-only at this stage; should not affect matching")
	}
}

func TestNewFilter_WithPurposeAndEntities(t *testing.T) {
	f := NewFilter(WithPurpose("research"), WithEntities("acme", "bigco"))
	if f.Purpose != "research" {
		t.Errorf("Purpose = %q, want research", f.Purpose)
	}
	if !reflect.DeepEqual(f.Entities, []string{"acme", "bigco"}) {
		t.Errorf("Entities = %v, want [acme bigco]", f.Entities)
	}
}

func TestSortSummaries_ByTitle(t *testing.T) {
	notes := []NoteSummary{
		{Title: "Zebra"},
		{Title: "Apple"},
	}
	SortSummaries(notes, SortByTitle, false)
	if notes[0].Title != "Apple" {
		t.Errorf("expected Apple first, got %s", notes[0].Title)
	}
}
