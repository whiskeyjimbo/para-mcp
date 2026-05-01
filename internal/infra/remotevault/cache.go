package remotevault

import (
	"container/list"
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

// bodyCache holds full Note objects keyed by path, with FIFO eviction when
// total estimated size exceeds bodyCacheMaxB. Eviction is O(1) per insert via
// a doubly-linked list tracking insertion order.
type bodyCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element // path → list element
	order   *list.List               // front = oldest; back = newest
	sizeB   int
}

type bodyCacheEntry struct {
	path   string
	note   domain.Note
	expiry time.Time
	sizeB  int
}

func newBodyCache() *bodyCache {
	return &bodyCache{
		entries: make(map[string]*list.Element),
		order:   list.New(),
	}
}

func (c *bodyCache) get(path string) (domain.Note, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[path]
	if !ok {
		return domain.Note{}, false
	}
	e := elem.Value.(*bodyCacheEntry)
	if time.Now().After(e.expiry) {
		c.removeLocked(path, elem, e.sizeB)
		return domain.Note{}, false
	}
	return e.note, true
}

func (c *bodyCache) set(path string, n domain.Note) {
	size := len(n.Body) + len(n.ETag) + len(n.Ref.Path) + 64
	c.mu.Lock()
	defer c.mu.Unlock()
	// Remove existing entry for this path if present.
	if elem, ok := c.entries[path]; ok {
		c.removeLocked(path, elem, elem.Value.(*bodyCacheEntry).sizeB)
	}
	// Evict oldest entries until within budget (O(1) per eviction).
	for c.sizeB+size > bodyCacheMaxB && c.order.Len() > 0 {
		front := c.order.Front()
		e := front.Value.(*bodyCacheEntry)
		c.removeLocked(e.path, front, e.sizeB)
	}
	// If a single entry exceeds the budget, skip caching.
	if size > bodyCacheMaxB {
		return
	}
	entry := &bodyCacheEntry{path: path, note: n, expiry: time.Now().Add(bodyCacheTTL), sizeB: size}
	elem := c.order.PushBack(entry)
	c.entries[path] = elem
	c.sizeB += size
}

func (c *bodyCache) invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[path]; ok {
		c.removeLocked(path, elem, elem.Value.(*bodyCacheEntry).sizeB)
	}
}

func (c *bodyCache) removeLocked(path string, elem *list.Element, sizeB int) {
	c.order.Remove(elem)
	delete(c.entries, path)
	c.sizeB -= sizeB
}
