package ports

import (
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// Doc is the document representation fed into an FTSIndex.
type Doc struct {
	Ref       domain.NoteRef
	Title     string
	Body      string
	UpdatedAt time.Time
}

// SearchResult is a single full-text search hit with its score.
type SearchResult struct {
	Ref       domain.NoteRef
	Score     float64
	UpdatedAt time.Time
}

// FTSIndex is the port that the storage layer uses to interact with a
// full-text search index. Concrete implementations live in infrastructure.
type FTSIndex interface {
	Add(doc Doc)
	Remove(ref domain.NoteRef)
	Search(query string, limit int) []SearchResult
	Close()
}
