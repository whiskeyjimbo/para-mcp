package domain

import (
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
