package domain

import "time"

// FilterOption configures a Filter.
type FilterOption func(*Filter)

// NewFilter builds a Filter from zero or more options.
func NewFilter(opts ...FilterOption) Filter {
	var f Filter
	for _, o := range opts {
		o(&f)
	}
	return f
}

// WithStatus sets the status constraint.
func WithStatus(s string) FilterOption { return func(f *Filter) { f.Status = s } }

// WithArea sets the area constraint.
func WithArea(s string) FilterOption { return func(f *Filter) { f.Area = s } }

// WithProject sets the project constraint.
func WithProject(s string) FilterOption { return func(f *Filter) { f.Project = s } }

// WithText sets the full-text search constraint.
func WithText(s string) FilterOption { return func(f *Filter) { f.Text = s } }

// WithTags requires all given tags to be present.
func WithTags(tags ...string) FilterOption { return func(f *Filter) { f.Tags = tags } }

// WithAnyTags requires at least one of the given tags to be present.
func WithAnyTags(tags ...string) FilterOption { return func(f *Filter) { f.AnyTags = tags } }

// WithCategories restricts results to the given categories.
func WithCategories(cats ...Category) FilterOption {
	return func(f *Filter) { f.Categories = cats }
}

// WithIncludeArchives includes archived notes in results.
func WithIncludeArchives() FilterOption { return func(f *Filter) { f.IncludeArchives = true } }

// WithUpdatedAfter restricts to notes updated after t.
func WithUpdatedAfter(t time.Time) FilterOption { return func(f *Filter) { f.UpdatedAfter = &t } }

// WithUpdatedBefore restricts to notes updated before t.
func WithUpdatedBefore(t time.Time) FilterOption { return func(f *Filter) { f.UpdatedBefore = &t } }

// WithScopes restricts the query to the given scope IDs (client-side selector;
// AllowedScopes remains the server-side authorization ceiling).
func WithScopes(scopes ...ScopeID) FilterOption { return func(f *Filter) { f.Scopes = scopes } }

// WithPurpose sets the semantic purpose hint used by semantic and hybrid search.
func WithPurpose(p string) FilterOption { return func(f *Filter) { f.Purpose = p } }

// WithEntities sets the entity-name hints used by semantic and hybrid search.
func WithEntities(entities ...string) FilterOption {
	return func(f *Filter) { f.Entities = entities }
}
