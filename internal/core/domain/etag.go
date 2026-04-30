package domain

import (
	"encoding/hex"

	"github.com/zeebo/blake3"
)

// ComputeETag returns the strong ETag for a note per the spec:
//
//	ETag = '"' + hex(blake3(canonical_user_yaml + "\n---\n" + body))[:16] + '"'
//
// canonical is the serialized front-matter with derived fields excluded and
// keys in deterministic order — callers in the infrastructure layer produce it.
func ComputeETag(canonical, body string) string {
	input := canonical + "\n---\n" + body
	sum := blake3.Sum256([]byte(input))
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}
