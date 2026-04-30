package domain

import (
	"slices"
	"strings"
)

// ApplyFilter returns the subset of notes that satisfy f.
func ApplyFilter(notes []NoteSummary, f Filter) []NoteSummary {
	out := notes[:0:0]
	for _, n := range notes {
		if MatchesFilter(n, f) {
			out = append(out, n)
		}
	}
	return out
}

// MatchesFilter reports whether n satisfies all constraints in f.
func MatchesFilter(n NoteSummary, f Filter) bool {
	isArchive := n.Category == Archives
	inRequestedCategories := len(f.Categories) > 0 && slices.Contains(f.Categories, n.Category)
	if isArchive && !f.IncludeArchives && !inRequestedCategories {
		return false
	}
	if len(f.Categories) > 0 && !inRequestedCategories && !(isArchive && f.IncludeArchives) {
		return false
	}
	if f.Status != "" && !strings.EqualFold(n.Status, f.Status) {
		return false
	}
	if f.Area != "" && !strings.EqualFold(n.Area, f.Area) {
		return false
	}
	if f.Project != "" && !strings.EqualFold(n.Project, f.Project) {
		return false
	}
	for _, tag := range f.Tags {
		if !HasTag(n.Tags, tag) {
			return false
		}
	}
	if len(f.AnyTags) > 0 && !slices.ContainsFunc(f.AnyTags, func(tag string) bool { return HasTag(n.Tags, tag) }) {
		return false
	}
	if f.UpdatedAfter != nil && !n.UpdatedAt.After(*f.UpdatedAfter) {
		return false
	}
	if f.UpdatedBefore != nil && !n.UpdatedAt.Before(*f.UpdatedBefore) {
		return false
	}
	return true
}

// HasTag reports whether tags contains want (case-insensitive).
func HasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// summaryComparators maps SortField values to comparator functions.
// Adding a new sort field is an additive change: register it here.
var summaryComparators = map[SortField]func(a, b NoteSummary) int{
	SortByTitle:   func(a, b NoteSummary) int { return strings.Compare(a.Title, b.Title) },
	SortByUpdated: func(a, b NoteSummary) int { return a.UpdatedAt.Compare(b.UpdatedAt) },
}

// SortSummaries sorts notes in-place by field; desc reverses the order.
// Unknown fields fall back to SortByUpdated.
func SortSummaries(notes []NoteSummary, field SortField, desc bool) {
	cmp, ok := summaryComparators[field]
	if !ok {
		cmp = summaryComparators[SortByUpdated]
	}
	slices.SortStableFunc(notes, func(a, b NoteSummary) int {
		if desc {
			return -cmp(a, b)
		}
		return cmp(a, b)
	})
}
