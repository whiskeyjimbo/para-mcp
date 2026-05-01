// Package noteutil provides shared utilities for storage backends:
// note parsing/formatting, ETag computation, link parsing, NoteCache, and BacklinkGraph.
package noteutil

import (
	"bytes"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"gopkg.in/yaml.v3"
)

const fmDelimiter = "---"

// ParseNote parses YAML front matter and body from raw note content.
func ParseNote(content []byte) (domain.FrontMatter, string, error) {
	s := string(content)
	rest, found := strings.CutPrefix(s, fmDelimiter+"\n")
	if !found {
		rest, found = strings.CutPrefix(s, fmDelimiter+"\r\n")
	}
	if !found {
		return domain.FrontMatter{}, s, nil
	}
	idx := strings.Index(rest, "\n"+fmDelimiter)
	if idx < 0 {
		return domain.FrontMatter{}, s, nil
	}
	yamlPart := rest[:idx]
	body := rest[idx+1+len(fmDelimiter):]
	body = strings.TrimLeft(body, "\r\n")
	var fm domain.FrontMatter
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		return domain.FrontMatter{}, s, err
	}
	return fm, body, nil
}

// FormatNote serializes front matter and body into raw note content.
func FormatNote(fm domain.FrontMatter, body string) ([]byte, error) {
	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(fmDelimiter + "\n")
	buf.Write(yamlBytes)
	buf.WriteString(fmDelimiter + "\n")
	if body != "" {
		buf.WriteString("\n")
		buf.WriteString(body)
	}
	return buf.Bytes(), nil
}

// CanonicalFrontMatterYAML serializes fm to YAML with keys sorted and the
// "derived" key excluded, giving a stable input for ETag hashing.
func CanonicalFrontMatterYAML(fm domain.FrontMatter) string {
	var doc yaml.Node
	if err := doc.Encode(fm); err != nil {
		panic("CanonicalFrontMatterYAML: " + err.Error())
	}
	mapping := &doc
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return ""
		}
		mapping = doc.Content[0]
	}
	removeMappingKey(mapping, "derived")
	sortMappingKeys(mapping)
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(mapping)
	_ = enc.Close()
	return b.String()
}

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

func sortMappingKeys(mapping *yaml.Node) {
	type pair struct{ k, v *yaml.Node }
	pairs := make([]pair, 0, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		pairs = append(pairs, pair{mapping.Content[i], mapping.Content[i+1]})
	}
	slices.SortFunc(pairs, func(a, b pair) int {
		return strings.Compare(a.k.Value, b.k.Value)
	})
	for i, p := range pairs {
		mapping.Content[2*i] = p.k
		mapping.Content[2*i+1] = p.v
	}
}

// SummaryToDoc converts a NoteSummary + body into a ports.Doc for FTS indexing.
func SummaryToDoc(s domain.NoteSummary, body string) ports.Doc {
	return ports.Doc{
		Ref:       s.Ref,
		Title:     s.Title,
		Body:      body,
		UpdatedAt: s.UpdatedAt,
	}
}

// IndexKey returns the cache key for a path. When !caseSensitive it is lowercased.
func IndexKey(path string, caseSensitive bool) string {
	if caseSensitive {
		return path
	}
	return strings.ToLower(path)
}

// OutLink is an outbound wikilink from a note.
type OutLink struct {
	TargetKey string
	IsAsset   bool
}

// BacklinkSrc is the source note of a backlink.
type BacklinkSrc struct {
	Path    string
	IsAsset bool
}

var wikilinkRE = regexp.MustCompile(`(!?)\[\[([^\]|]+)`)

// ParseLinks extracts all wikilinks from body.
func ParseLinks(body string) []OutLink {
	matches := wikilinkRE.FindAllStringSubmatch(body, -1)
	out := make([]OutLink, 0, len(matches))
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
		out = append(out, OutLink{TargetKey: key, IsAsset: isAsset})
	}
	return out
}

// LinkMatchKeys returns the normalized keys under which a vault-relative
// path may appear in a wikilink.
func LinkMatchKeys(path string) []string {
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

// NoteCache is a thread-safe in-memory cache of note summaries keyed by index key.
type NoteCache struct {
	mu    sync.RWMutex
	notes map[string]domain.NoteSummary
}

// NewNoteCache returns an empty NoteCache.
func NewNoteCache() *NoteCache {
	return &NoteCache{notes: make(map[string]domain.NoteSummary)}
}

func (c *NoteCache) Get(key string) (domain.NoteSummary, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.notes[key]
	return s, ok
}

func (c *NoteCache) Set(key string, s domain.NoteSummary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notes[key] = s
}

func (c *NoteCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.notes, key)
}

func (c *NoteCache) All() []domain.NoteSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]domain.NoteSummary, 0, len(c.notes))
	for _, s := range c.notes {
		out = append(out, s)
	}
	return out
}

func (c *NoteCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.notes)
}

// Move atomically renames oldKey→newKey and updates the stored summary.
func (c *NoteCache) Move(oldKey, newKey string, s domain.NoteSummary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.notes, oldKey)
	c.notes[newKey] = s
}

// Iterate calls fn for every entry while holding the read lock.
func (c *NoteCache) Iterate(fn func(key string, s domain.NoteSummary)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, s := range c.notes {
		fn(k, s)
	}
}

// BacklinkGraph tracks outbound links and the inverse backlink index.
type BacklinkGraph struct {
	mu        sync.RWMutex
	outLinks  map[string][]OutLink
	backlinks map[string][]BacklinkSrc
}

// NewBacklinkGraph returns an empty BacklinkGraph.
func NewBacklinkGraph() *BacklinkGraph {
	return &BacklinkGraph{
		outLinks:  make(map[string][]OutLink),
		backlinks: make(map[string][]BacklinkSrc),
	}
}

// Upsert replaces outbound links for storagePath and updates the inverse index.
func (g *BacklinkGraph) Upsert(storagePath string, links []OutLink) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removeLocked(storagePath)
	if len(links) == 0 {
		return
	}
	g.outLinks[storagePath] = links
	for _, link := range links {
		g.backlinks[link.TargetKey] = append(g.backlinks[link.TargetKey], BacklinkSrc{
			Path:    storagePath,
			IsAsset: link.IsAsset,
		})
	}
}

// Remove removes all outbound links for storagePath from the graph.
func (g *BacklinkGraph) Remove(storagePath string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removeLocked(storagePath)
}

// Links returns the outbound links for storagePath.
func (g *BacklinkGraph) Links(storagePath string) []OutLink {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.outLinks[storagePath]
}

// Backlinks returns the backlink sources for targetKey.
func (g *BacklinkGraph) Backlinks(targetKey string) []BacklinkSrc {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.backlinks[targetKey]
}

func (g *BacklinkGraph) removeLocked(storagePath string) {
	old, ok := g.outLinks[storagePath]
	if !ok {
		return
	}
	for _, link := range old {
		srcs := g.backlinks[link.TargetKey]
		out := srcs[:0]
		for _, s := range srcs {
			if s.Path != storagePath {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			delete(g.backlinks, link.TargetKey)
		} else {
			g.backlinks[link.TargetKey] = out
		}
	}
	delete(g.outLinks, storagePath)
}

// RunBatch is a generic helper that runs fn on each item and accumulates a BatchResult.
func RunBatch[I any](items []I, fn func(I) (path string, res domain.MutationResult, err error)) domain.BatchResult {
	result := domain.BatchResult{Results: make([]domain.BatchItemResult, len(items))}
	for i, item := range items {
		path, res, err := fn(item)
		r := domain.BatchItemResult{Index: i, Path: path}
		if err != nil {
			r.Error = err.Error()
			result.FailureCount++
		} else {
			r.OK = true
			r.Summary = &res.Summary
			r.ETag = res.ETag
			result.SuccessCount++
		}
		result.Results[i] = r
	}
	return result
}

// IsMDFile reports whether the file has a .md extension.
func IsMDFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".md")
}

// Clock is a function that returns the current time. Useful as a replaceable time source.
type Clock func() time.Time

// DefaultClock returns the current UTC time.
func DefaultClock() time.Time { return time.Now().UTC() }
