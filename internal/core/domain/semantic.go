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
