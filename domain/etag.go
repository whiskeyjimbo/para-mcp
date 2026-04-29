package domain

import (
	"encoding/hex"
	"sort"
	"strings"

	"github.com/zeebo/blake3"
	"gopkg.in/yaml.v3"
)

// ComputeETag returns the strong ETag for a note per the spec:
//
//	ETag = '"' + hex(blake3(canonical_user_yaml + "\n---\n" + body))[:16] + '"'
//
// canonical_user_yaml is the frontmatter with the 'derived' namespace removed
// and keys sorted. mtime is not part of the input so a touch does not rotate
// the ETag. The 'derived' block is excluded so async pipeline writes don't
// invalidate in-flight ETags from user edits.
func ComputeETag(fm FrontMatter, body string) string {
	canonical := canonicalFrontMatterYAML(fm)
	input := canonical + "\n---\n" + body
	sum := blake3.Sum256([]byte(input))
	return `"` + hex.EncodeToString(sum[:8]) + `"` // 8 bytes = 16 hex chars
}

// canonicalFrontMatterYAML serializes fm to YAML with the 'derived' key removed
// and all top-level keys sorted alphabetically.
func canonicalFrontMatterYAML(fm FrontMatter) string {
	var doc yaml.Node
	if err := doc.Encode(fm); err != nil {
		// FrontMatter is a known type; encoding failure is a programmer error.
		panic("canonicalFrontMatterYAML: " + err.Error())
	}
	// doc is a Document node; Content[0] is the Mapping node.
	if len(doc.Content) == 0 {
		return ""
	}
	mapping := doc.Content[0]
	removeMappingKey(mapping, "derived")
	sortMappingKeys(mapping)

	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(mapping)
	_ = enc.Close()
	return b.String()
}

// removeMappingKey removes a key (and its value) from a yaml Mapping node.
func removeMappingKey(mapping *yaml.Node, key string) {
	out := mapping.Content[:0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			continue
		}
		out = append(out, mapping.Content[i], mapping.Content[i+1])
	}
	mapping.Content = out
}

// sortMappingKeys sorts the key-value pairs of a yaml Mapping node by key name.
func sortMappingKeys(mapping *yaml.Node) {
	type pair struct{ k, v *yaml.Node }
	pairs := make([]pair, 0, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		pairs = append(pairs, pair{mapping.Content[i], mapping.Content[i+1]})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].k.Value < pairs[j].k.Value
	})
	for i, p := range pairs {
		mapping.Content[2*i] = p.k
		mapping.Content[2*i+1] = p.v
	}
}
