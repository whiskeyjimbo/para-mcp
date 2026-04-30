package localvault

import (
	"path/filepath"
	"regexp"
	"strings"
)

// wikilinkRE matches [[target]], [[target|alias]], ![[asset]], ![[asset|alias]].
var wikilinkRE = regexp.MustCompile(`(!?)\[\[([^\]|]+)`)

type outLink struct {
	targetKey string
	isAsset   bool
}

type backlinkSrc struct {
	path    string
	isAsset bool
}

func parseLinks(body string) []outLink {
	matches := wikilinkRE.FindAllStringSubmatch(body, -1)
	out := make([]outLink, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		isAsset := m[1] == "!"
		raw := strings.TrimSpace(m[2])
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
// may appear in a wikilink.
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
