package domain

import (
	"crypto/rand"
	"time"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

var paraNamespace = uuid.MustParse("01960000-0000-7000-8000-000000000000")

func MintNoteID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// DeriveNoteID produces a stable ID for editor-created notes.
func DeriveNoteID(path, contentHash string) string {
	return uuid.NewSHA1(paraNamespace, []byte(path+"\x00"+contentHash)).String()
}

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
