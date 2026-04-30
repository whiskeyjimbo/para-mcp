package localvault

import "sync"

// BacklinkGraph tracks outbound links and the inverse backlink index.
// All methods are safe for concurrent use.
type BacklinkGraph struct {
	mu        sync.RWMutex
	outLinks  map[string][]outLink
	backlinks map[string][]backlinkSrc
}

func newBacklinkGraph() *BacklinkGraph {
	return &BacklinkGraph{
		outLinks:  make(map[string][]outLink),
		backlinks: make(map[string][]backlinkSrc),
	}
}

// Upsert replaces the outbound links for storagePath and updates the inverse index.
func (g *BacklinkGraph) Upsert(storagePath string, links []outLink) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removeLocked(storagePath)
	if len(links) == 0 {
		return
	}
	g.outLinks[storagePath] = links
	for _, link := range links {
		g.backlinks[link.targetKey] = append(g.backlinks[link.targetKey], backlinkSrc{
			path:    storagePath,
			isAsset: link.isAsset,
		})
	}
}

// Remove removes all outbound links for storagePath and updates the inverse index.
func (g *BacklinkGraph) Remove(storagePath string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removeLocked(storagePath)
}

// Links returns the outbound links for storagePath (read-only snapshot).
func (g *BacklinkGraph) Links(storagePath string) []outLink {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.outLinks[storagePath]
}

// Backlinks returns the backlink sources for targetKey (read-only snapshot).
func (g *BacklinkGraph) Backlinks(targetKey string) []backlinkSrc {
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
		srcs := g.backlinks[link.targetKey]
		out := srcs[:0]
		for _, s := range srcs {
			if s.path != storagePath {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			delete(g.backlinks, link.targetKey)
		} else {
			g.backlinks[link.targetKey] = out
		}
	}
	delete(g.outLinks, storagePath)
}
