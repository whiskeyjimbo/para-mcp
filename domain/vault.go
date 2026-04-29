package domain

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by Vault implementations.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict: note has been modified")
)

// SearchMode selects the retrieval strategy.
type SearchMode string

const (
	SearchModeLexical SearchMode = "lexical" // BM25 only (Phase 1)
)

// SortField names the field used to order query results.
type SortField string

const (
	SortByUpdated   SortField = "updated_at"
	SortByCreated   SortField = "created_at"
	SortByTitle     SortField = "title"
	SortByRelevance SortField = "relevance" // valid only when Filter.Text is set
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

// Filter narrows query and search results.
//
// AllowedScopes is server-resolved and must never come from wire input.
// nil AllowedScopes is a programmer error and triggers an internal error.
// Empty []ScopeID{} is the legitimate "deny everything" value.
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

	// Populated server-side from RBAC; never accepted from wire input.
	AllowedScopes []ScopeID
}

// QueryRequest specifies a paginated query over a vault.
type QueryRequest struct {
	Filter Filter
	Sort   SortField
	Desc   bool
	Limit  int
	Offset int
	Cursor string
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

// RankedNote pairs a summary with a relevance score from search.
type RankedNote struct {
	Summary NoteSummary
	Score   float64
}

// VaultStats holds aggregate counts for a vault.
type VaultStats struct {
	TotalNotes int
	ByCategory map[Category]int
}

// CaseCollision is a pair of paths that differ only in case on a
// case-sensitive vault, representing a federation portability risk.
type CaseCollision struct {
	PathA string
	PathB string
}

// VaultHealth holds diagnostic information about a vault.
type VaultHealth struct {
	CaseCollisions    []CaseCollision
	UnrecognizedFiles int
	SyncConflicts     int
	WatcherStatus     string // "ok" | "limit_exceeded"
}

// Vault is the storage port. LocalVault and RemoteVault both implement it.
//
// AllowedScopes contract: every method that accepts a Filter must apply
// Filter.AllowedScopes as an index-side pre-filter. nil AllowedScopes
// triggers an internal error; empty []ScopeID{} returns an empty result.
type Vault interface {
	Scope() ScopeID
	Capabilities() Capabilities

	Get(ctx context.Context, path string) (Note, error)
	Create(ctx context.Context, in CreateInput) (NoteSummary, error)
	UpdateBody(ctx context.Context, path, body string, ifMatch string) (NoteSummary, error)
	PatchFrontMatter(ctx context.Context, path string, fields map[string]any, ifMatch string) (NoteSummary, error)
	Move(ctx context.Context, path, newPath string, ifMatch string) (NoteSummary, error)
	Delete(ctx context.Context, path string, soft bool) error

	Query(ctx context.Context, q QueryRequest) (QueryResult, error)
	Search(ctx context.Context, text string, filter Filter, limit int) ([]RankedNote, error)
	Backlinks(ctx context.Context, ref NoteRef, filter Filter) ([]NoteSummary, error)

	Stats(ctx context.Context) (VaultStats, error)
	Health(ctx context.Context) (VaultHealth, error)
}
