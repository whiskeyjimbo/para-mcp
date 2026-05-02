package domain

import (
	"strings"
	"time"
)

// ScopeID is the canonical type for scope identifiers across the API.
type ScopeID = string

// NoteRef addresses a note across the federation: scope + vault-relative path.
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

// Summary projects a Note into a NoteSummary for list and query responses.
func (n Note) Summary() NoteSummary {
	cat, _ := CategoryFromPath(n.Ref.Path)
	return NoteSummary{
		Ref:       n.Ref,
		Title:     n.FrontMatter.Title,
		Tags:      n.FrontMatter.Tags,
		Status:    n.FrontMatter.Status,
		Area:      n.FrontMatter.Area,
		Project:   n.FrontMatter.Project,
		Category:  cat,
		UpdatedAt: n.FrontMatter.UpdatedAt,
	}
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
	// BodyHash is the truncated SHA-256 of the note body at derivation time, used for idempotency.
	BodyHash string `json:"body_hash,omitempty"`
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

	// Present only when the vault has Derived/Semantic capability.
	Derived    *DerivedMetadata
	IndexState IndexState
}

// MutationResult is returned by single-note write operations (Create, UpdateBody,
// PatchFrontMatter, Move). The ETag is the concurrency token for the next write.
type MutationResult struct {
	Summary NoteSummary
	ETag    string
}

// ConflictStrategy controls behaviour when a promote target already exists.
type ConflictStrategy string

const (
	ConflictError     ConflictStrategy = "error"
	ConflictOverwrite ConflictStrategy = "overwrite"
)

// PromoteInput is the input for note_promote.
type PromoteInput struct {
	Ref            NoteRef
	ToScope        ScopeID
	IfMatch        string
	KeepSource     bool
	OnConflict     ConflictStrategy
	IdempotencyKey string
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
	IsAsset bool
}

// BatchItemResult reports per-note outcome for a batch operation.
type BatchItemResult struct {
	Index   int
	Path    string
	OK      bool
	Summary *NoteSummary
	ETag    string
	Error   string
}

// BatchResult is the aggregate response for a batch operation.
type BatchResult struct {
	Results      []BatchItemResult
	SuccessCount int
	FailureCount int
}
