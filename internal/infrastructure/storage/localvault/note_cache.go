package localvault

import (
	"sync"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// NoteCache is a thread-safe in-memory cache of note summaries keyed by index key.
type NoteCache struct {
	mu    sync.RWMutex
	notes map[string]domain.NoteSummary
}

func newNoteCache() *NoteCache {
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

// Iterate calls fn for every entry under a read lock.
func (c *NoteCache) Iterate(fn func(key string, s domain.NoteSummary)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, s := range c.notes {
		fn(k, s)
	}
}
