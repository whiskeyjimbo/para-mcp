package vault

import (
	"path/filepath"
	"regexp"
	"strings"
)

// wikilinkRE matches [[target]], [[target|alias]], ![[asset]], ![[asset|alias]].
// Group 1: "!" prefix (asset embed). Group 2: target text (before | or ]]).
var wikilinkRE = regexp.MustCompile(`(!?)\[\[([^\]|]+)`)

// outLink is one edge in the outgoing wikilink graph.
type outLink struct {
	targetKey string // normalized key used for reverse-index lookup
	isAsset   bool   // true if ![[...]] embed syntax
}

// backlinkSrc is the reverse edge: a note that links to a target.
type backlinkSrc struct {
	path    string // source note vault-relative storage path
	isAsset bool
}

// parseLinks extracts all wikilinks from body, deduplicating by target key.
func parseLinks(body string) []outLink {
	matches := wikilinkRE.FindAllStringSubmatch(body, -1)
	out := make([]outLink, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		isAsset := m[1] == "!"
		raw := strings.TrimSpace(m[2])
		// strip #section fragment
		if i := strings.IndexByte(raw, '#'); i >= 0 {
			raw = raw[:i]
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		key := strings.ToLower(raw)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, outLink{targetKey: key, isAsset: isAsset})
	}
	return out
}

// linkMatchKeys returns all normalized keys under which a vault-relative path
// may appear in a wikilink. E.g. "projects/hello.md" →
// ["hello", "hello.md", "projects/hello", "projects/hello.md"]
func linkMatchKeys(path string) []string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	noExt := strings.TrimSuffix(base, ext)
	noExtPath := strings.TrimSuffix(path, ext)

	candidates := []string{
		strings.ToLower(noExt),
		strings.ToLower(base),
		strings.ToLower(noExtPath),
		strings.ToLower(path),
	}
	seen := make(map[string]bool, len(candidates))
	keys := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c != "" && !seen[c] {
			seen[c] = true
			keys = append(keys, c)
		}
	}
	return keys
}
