package remotevault

import (
	"sync"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

const (
	summaryCacheTTL = 30 * time.Second
	bodyCacheTTL    = 5 * time.Minute
	bodyCacheMaxB   = 64 * 1024 * 1024 // 64 MiB
)

// summaryCache holds the last Query result per (filter hash) key.
// Entries expire after summaryCacheTTL.
type summaryCache struct {
	mu      sync.Mutex
	entries map[string]summaryCacheEntry
}

type summaryCacheEntry struct {
	result domain.QueryResult
	expiry time.Time
}

func newSummaryCache() *summaryCache {
	return &summaryCache{entries: make(map[string]summaryCacheEntry)}
}

func (c *summaryCache) get(key string) (domain.QueryResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiry) {
		delete(c.entries, key)
		return domain.QueryResult{}, false
	}
	return e.result, true
}

func (c *summaryCache) set(key string, r domain.QueryResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = summaryCacheEntry{result: r, expiry: time.Now().Add(summaryCacheTTL)}
}

func (c *summaryCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]summaryCacheEntry)
}

// bodyCache holds full Note objects keyed by path, with LRU-style eviction
// when total estimated size exceeds bodyCacheMaxB.
type bodyCache struct {
	mu      sync.Mutex
	entries map[string]bodyCacheEntry
	sizeB   int
}

type bodyCacheEntry struct {
	note   domain.Note
	expiry time.Time
	sizeB  int
}

func newBodyCache() *bodyCache {
	return &bodyCache{entries: make(map[string]bodyCacheEntry)}
}

func (c *bodyCache) get(path string) (domain.Note, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[path]
	if !ok || time.Now().After(e.expiry) {
		if ok {
			c.sizeB -= e.sizeB
			delete(c.entries, path)
		}
		return domain.Note{}, false
	}
	return e.note, true
}

func (c *bodyCache) set(path string, n domain.Note) {
	size := len(n.Body) + len(n.ETag) + len(n.Ref.Path) + 64
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[path]; ok {
		c.sizeB -= old.sizeB
	}
	// Evict expired entries if over budget.
	if c.sizeB+size > bodyCacheMaxB {
		now := time.Now()
		for k, e := range c.entries {
			if now.After(e.expiry) {
				c.sizeB -= e.sizeB
				delete(c.entries, k)
			}
		}
	}
	// If still over budget after expiry sweep, skip caching.
	if c.sizeB+size > bodyCacheMaxB {
		return
	}
	c.entries[path] = bodyCacheEntry{note: n, expiry: time.Now().Add(bodyCacheTTL), sizeB: size}
	c.sizeB += size
}

func (c *bodyCache) invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[path]; ok {
		c.sizeB -= e.sizeB
		delete(c.entries, path)
	}
}
