package domain

import (
	"errors"
	"time"
)

// Sentinel errors returned by domain validation and Vault implementations.
var (
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict: note has been modified")
	ErrInvalidPath        = errors.New("invalid path")
	ErrInvalidFrontMatter = errors.New("invalid frontmatter")
	ErrScopeForbidden     = errors.New("scope not permitted")
	ErrUnavailable        = errors.New("all scopes unavailable")
	ErrInvalidCursor      = errors.New("cursor invalid or expired")
)

// SearchMode selects the retrieval strategy.
type SearchMode string

const (
	SearchModeLexical SearchMode = "lexical"
)

// SortField names the field used to order query results.
type SortField string

const (
	SortByUpdated   SortField = "updated_at"
	SortByCreated   SortField = "created_at"
	SortByTitle     SortField = "title"
	SortByRelevance SortField = "relevance"
)

// Capabilities reports what a vault supports.
type Capabilities struct {
	Writable      bool
	SoftDelete    bool
	Watch         bool
	MaxBodyBytes  int
	CaseSensitive bool
	Semantic      bool
	Derived       bool
	Clustering    bool
}

// CreateInput holds the inputs for creating a new note.
type CreateInput struct {
	Path        string
	FrontMatter FrontMatter
	Body        string
}

// NewFrontMatter constructs a FrontMatter with normalized tags.
func NewFrontMatter(title, status, area, project string, tags []string) FrontMatter {
	return FrontMatter{
		Title:   title,
		Status:  status,
		Area:    area,
		Project: project,
		Tags:    NormalizeTags(tags),
	}
}

// NewCreateInput constructs a CreateInput with normalized frontmatter.
func NewCreateInput(path string, fm FrontMatter, body string) CreateInput {
	return CreateInput{Path: path, FrontMatter: fm, Body: body}
}

// Filter narrows query and search results by note content fields.
// It carries no authorization state — use AuthFilter at the service boundary.
type Filter struct {
	Scopes          []ScopeID
	Categories      []Category
	IncludeArchives bool
	Tags            []string
	AnyTags         []string
	Status          string
	Area            string
	Project         string
	Text            string
	UpdatedAfter    *time.Time
	UpdatedBefore   *time.Time
	Purpose         string
	Entities        []string
}

// AuthFilter pairs a content Filter with the authorization scope list.
// AllowedScopes is server-resolved and must never be accepted from wire input.
// nil AllowedScopes is a programmer error caught at the NoteService boundary.
// Empty []ScopeID{} is the legitimate "deny everything" value.
type AuthFilter struct {
	Filter
	AllowedScopes []ScopeID
}

// QueryRequest specifies a paginated query over a vault.
// AllowedScopes is the authorization constraint enforced by NoteService;
// vault implementations receive a QueryRequest without AllowedScopes.
type QueryRequest struct {
	Filter        Filter
	AllowedScopes []ScopeID
	Sort          SortField
	Desc          bool
	Limit         int
	Offset        int
	Cursor        string
}

// PartialFailure is non-nil when some but not all scopes responded.
type PartialFailure struct {
	FailedScopes []ScopeID
	Reason       map[ScopeID]string
	WarningText  string
}

// QueryResult holds a page of notes plus pagination metadata.
type QueryResult struct {
	Notes           []NoteSummary
	Total           int
	HasMore         bool
	PerScope        map[ScopeID]int
	ScopesAttempted []ScopeID
	ScopesSucceeded []ScopeID
	PartialFailure  *PartialFailure
	NextCursor      string
}

// ScopeInfo describes a single vault scope visible to the caller.
type ScopeInfo struct {
	Scope        ScopeID      `json:"scope"`
	Capabilities Capabilities `json:"capabilities"`
}

// VaultStats holds aggregate counts for a vault.
type VaultStats struct {
	TotalNotes int
	ByCategory map[Category]int
}

// CaseCollision is a pair of paths that differ only in case on a case-sensitive vault.
type CaseCollision struct {
	PathA string
	PathB string
}

// VaultHealth holds diagnostic information about a vault.
type VaultHealth struct {
	CaseCollisions    []CaseCollision
	UnrecognizedFiles int
	SyncConflicts     int
	WatcherStatus     string
}
