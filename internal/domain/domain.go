package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ScopeID is the canonical type for scope identifiers across the API.
// Alias rather than a new type so existing string-keyed maps and JSON
// serialization stay unchanged.
type ScopeID = string

// NoteRef addresses a note across the federation: scope + vault-relative path.
// String form: "personal:projects/infra-refactor.md"
type NoteRef struct {
	Scope ScopeID
	Path  string
}

func (r NoteRef) String() string {
	return r.Scope + ":" + r.Path
}

// Category is derived from the first path segment. Never stored.
type Category string

const (
	Projects  Category = "projects"
	Areas     Category = "areas"
	Resources Category = "resources"
	Archives  Category = "archives"
)

var knownCategories = map[Category]bool{
	Projects:  true,
	Areas:     true,
	Resources: true,
	Archives:  true,
}

// CategoryFromPath returns the implied category from a vault-relative path.
// Returns ("", false) if the path doesn't begin with a known PARA folder.
func CategoryFromPath(p string) (Category, bool) {
	seg, _, _ := strings.Cut(p, "/")
	c := Category(strings.ToLower(seg))
	if knownCategories[c] {
		return c, true
	}
	return "", false
}

// FrontMatter holds the parsed YAML front matter of a note.
type FrontMatter struct {
	Title     string         `yaml:"title"`
	Tags      []string       `yaml:"tags,omitempty"`
	Status    string         `yaml:"status,omitempty"`
	Area      string         `yaml:"area,omitempty"`
	Project   string         `yaml:"project,omitempty"`
	CreatedAt time.Time      `yaml:"created_at,omitempty"`
	UpdatedAt time.Time      `yaml:"updated_at,omitempty"`
	Extra     map[string]any `yaml:",inline"`
}

// Note is the full content of a note including body and concurrency token.
type Note struct {
	Ref         NoteRef
	FrontMatter FrontMatter
	Body        string
	ETag        string
}

// Category derives from Ref.Path; not a stored field.
func (n Note) Category() Category {
	c, _ := CategoryFromPath(n.Ref.Path)
	return c
}

// IndexState reports where a note is in the semantic pipeline.
type IndexState string

const (
	IndexStatePending           IndexState = "pending"
	IndexStateIndexed           IndexState = "indexed"
	IndexStateFailed            IndexState = "failed"
	IndexStateSkippedShort      IndexState = "skipped_short"
	IndexStateSkippedUserEdited IndexState = "skipped_user_edited"
	IndexStateTombstoned        IndexState = "tombstoned"
)

// DerivedMetadata holds model-generated fields for a note.
// Only populated when the vault has Derived capability.
type DerivedMetadata struct {
	Summary       string    `json:"summary"`
	Entities      []Entity  `json:"entities,omitempty"`
	SuggestedTags []string  `json:"suggested_tags,omitempty"`
	Purpose       string    `json:"purpose,omitempty"`
	SummaryRatio  float32   `json:"summary_ratio"`
	EmbedModel    string    `json:"embed_model,omitempty"`
	SummaryModel  string    `json:"summary_model,omitempty"`
	GeneratedAt   time.Time `json:"generated_at"`
	SchemaVersion int       `json:"schema_version"`
	EditedByUser  bool      `json:"edited_by_user,omitempty"`
}

// EntityKind classifies an extracted entity.
type EntityKind string

const (
	EntityPerson  EntityKind = "person"
	EntityProject EntityKind = "project"
	EntityTool    EntityKind = "tool"
	EntityConcept EntityKind = "concept"
	EntityOther   EntityKind = "other"
)

// Entity is a named entity extracted from a note body.
type Entity struct {
	Text string     `json:"text"`
	Kind EntityKind `json:"kind"`
}

// NoteSummary is what list and query tools return. Full body only via note_get.
type NoteSummary struct {
	Ref       NoteRef
	Title     string
	Tags      []string
	Status    string
	Area      string
	Project   string
	Category  Category
	UpdatedAt time.Time
	// ETag populated on summaries from mutation tools; empty on list/query results.
	ETag string

	// Present only when the vault has Derived/Semantic capability.
	Derived    *DerivedMetadata
	IndexState IndexState
}

// NormalizeTag applies the canonical tag normalization at every write boundary.
//
//  1. Trim leading/trailing whitespace
//  2. Strip a leading '#' if present
//  3. Lowercase (Unicode-aware)
//  4. Collapse internal whitespace runs to single '-'
//  5. Reject if result is empty or longer than 64 runes
func NormalizeTag(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	s = strings.ToLower(s)
	result := strings.Join(strings.FieldsFunc(s, unicode.IsSpace), "-")
	if result == "" {
		return "", fmt.Errorf("tag normalizes to empty string")
	}
	if utf8.RuneCountInString(result) > 64 {
		return "", fmt.Errorf("tag exceeds 64 runes after normalization")
	}
	return result, nil
}

// BatchUpdateBodyInput is one item in a notes_update_batch request.
type BatchUpdateBodyInput struct {
	Path    string
	Body    string
	IfMatch string
}

// BatchPatchFrontMatterInput is one item in a notes_patch_frontmatter_batch request.
type BatchPatchFrontMatterInput struct {
	Path    string
	Fields  map[string]any
	IfMatch string
}

// BacklinkEntry is a note that contains a wikilink pointing at a given target.
type BacklinkEntry struct {
	Summary NoteSummary
	IsAsset bool // true when referenced via ![[...]] asset-embed syntax
}

// BatchItemResult reports per-note outcome for a batch operation.
// One note's failure never rolls back siblings.
type BatchItemResult struct {
	Index   int          // position in the original request slice
	Path    string       // vault-relative path
	OK      bool         // true on success
	Summary *NoteSummary // non-nil on success
	Error   string       // non-empty on failure
}

// BatchResult is the aggregate response for a batch operation.
type BatchResult struct {
	Results      []BatchItemResult
	SuccessCount int
	FailureCount int
}

// CategoryTemplate defines default frontmatter applied on note creation.
type CategoryTemplate struct {
	Status string
	Tags   []string
}

// DefaultTemplates are the built-in per-category creation defaults.
var DefaultTemplates = map[Category]CategoryTemplate{
	Projects:  {Status: "active"},
	Areas:     {},
	Resources: {},
	Archives:  {Status: "archived"},
}

// NormalizeScopeID applies canonical scope ID normalization.
// Rules: lowercase, trim whitespace, [a-z0-9_-] only, 1-64 runes.
func NormalizeScopeID(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	if s == "" {
		return "", fmt.Errorf("scope ID is empty")
	}
	if utf8.RuneCountInString(s) > 64 {
		return "", fmt.Errorf("scope ID exceeds 64 runes")
	}
	for _, r := range s {
		if !isSlugRune(r) {
			return "", fmt.Errorf("scope ID contains invalid character %q (only [a-z0-9_-] allowed)", r)
		}
	}
	return s, nil
}

func isSlugRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}
