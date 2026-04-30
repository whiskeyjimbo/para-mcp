package localvault

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// mintNoteID generates a new ULID-based note ID.
func mintNoteID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
