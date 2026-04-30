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

// SortSummaries sorts notes in-place by field; desc reverses the order.
func SortSummaries(notes []NoteSummary, field SortField, desc bool) {
	slices.SortStableFunc(notes, func(a, b NoteSummary) int {
		var cmp int
		switch field {
		case SortByTitle:
			cmp = strings.Compare(a.Title, b.Title)
		default:
			cmp = a.UpdatedAt.Compare(b.UpdatedAt)
		}
		if desc {
			return -cmp
		}
		return cmp
	})
}
