package domain

// VectorRecord is the unit stored in and retrieved from a VectorStore.
type VectorRecord struct {
	ID     string // NoteID (ULID for MCP, UUIDv5 for editor)
	Ref    NoteRef
	Chunk  int // chunk index; 0 = whole-note
	Vector []float32
	Body   string // chunk text, used for re-ranking
}

// VectorHit is a single result from a vector similarity search.
type VectorHit struct {
	Ref   NoteRef
	ID    string
	Chunk int
	Score float64
	Body  string
}

// BodyMode controls whether semantic search results include note bodies.
type BodyMode string

const (
	// BodyNever omits note bodies from semantic search results.
	BodyNever BodyMode = "never"
	// BodyOnDemand loads full note bodies for the top results. When no
	// threshold is set, only the top BodyOnDemandTopK results carry bodies.
	BodyOnDemand BodyMode = "on_demand"
)

// BodyOnDemandTopK is the default cap on body-loaded results when BodyOnDemand
// is requested without a threshold.
const BodyOnDemandTopK = 3

// SemanticSearchOptions configures a NoteService.SemanticSearch call.
type SemanticSearchOptions struct {
	// Limit caps the number of returned results. Zero means service default.
	Limit int
	// Threshold is the cosine-similarity floor. Zero disables the floor.
	Threshold float64
	// BodyMode selects whether result bodies are loaded.
	BodyMode BodyMode
}

// RRF (Reciprocal Rank Fusion) constants. Hybrid search merges BM25 and
// vector ranklists using score(d) = sum(1 / (RRFK + rank_i)) per list,
// then weights the lists by RRFAlpha (lexical) and 1-RRFAlpha (semantic).
const (
	// RRFK dampens the contribution of low-ranked items.
	RRFK = 60
	// RRFAlpha is the lexical/semantic weight. 0.5 = equal weighting.
	// Held fixed until Phase 7 auto-tuning.
	RRFAlpha = 0.5
)

// HybridSearchOptions configures a NoteService.HybridSearch call.
type HybridSearchOptions struct {
	// Limit caps the number of returned results. Zero means service default.
	Limit int
}
