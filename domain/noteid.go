package domain

import (
	"crypto/rand"
	"time"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

// paraNamespace is the fixed UUIDv5 namespace for deriving stable note IDs.
var paraNamespace = uuid.MustParse("01960000-0000-7000-8000-000000000000")

// MintNoteID mints a fresh ULID for MCP-created notes.
func MintNoteID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// DeriveNoteID derives a stable UUIDv5 ID for editor-created (pre-existing) notes.
// Input is canonical vault-relative path + null byte + initial content hash.
// The result is stable for the same path and initial hash.
func DeriveNoteID(path, contentHash string) string {
	name := path + "\x00" + contentHash
	return uuid.NewSHA1(paraNamespace, []byte(name)).String()
}

// GetNoteID returns the note_id stored in FrontMatter.Extra["derived"], or "".
func GetNoteID(fm FrontMatter) string {
	if fm.Extra == nil {
		return ""
	}
	derived, ok := fm.Extra["derived"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := derived["note_id"].(string)
	return id
}

// SetNoteID writes id into FrontMatter.Extra["derived"]["note_id"].
func SetNoteID(fm *FrontMatter, id string) {
	if fm.Extra == nil {
		fm.Extra = make(map[string]any)
	}
	derived, ok := fm.Extra["derived"].(map[string]any)
	if !ok {
		derived = make(map[string]any)
		fm.Extra["derived"] = derived
	}
	derived["note_id"] = id
}
