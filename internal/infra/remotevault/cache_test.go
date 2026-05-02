package remotevault

import (
	"fmt"
	"testing"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

func makeNote(path, body string) domain.Note {
	return domain.Note{Ref: domain.NoteRef{Path: path}, Body: body}
}

func noteSize(n domain.Note) int {
	return len(n.Body) + len(n.ETag) + len(n.Ref.Path) + 64
}

func TestBodyCache_BasicSetGet(t *testing.T) {
	c := newBodyCache()
	n := makeNote("a.md", "hello")
	c.set("a.md", n)
	got, ok := c.get("a.md")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Body != "hello" {
		t.Errorf("body = %q, want %q", got.Body, "hello")
	}
}

func TestBodyCache_EvictsOldestOnBudgetExceeded(t *testing.T) {
	c := newBodyCache()

	// Insert 1000 entries each using 1 KiB of body.
	body := string(make([]byte, 1024))
	for i := range 1000 {
		path := string(rune('a'+i%26)) + ".md"
		c.set(path, makeNote(path, body))
	}

	// Size must not exceed budget.
	c.mu.Lock()
	size := c.sizeB
	c.mu.Unlock()
	if size > bodyCacheMaxB {
		t.Errorf("cache size %d exceeds budget %d", size, bodyCacheMaxB)
	}
}

func TestBodyCache_FIFOEviction(t *testing.T) {
	// Verify that insertions beyond the budget evict the oldest entry (FIFO).
	c := newBodyCache()

	// Use entries large enough that a small number fills the budget.
	// 4MB each → ~16 entries fill 64MB budget.
	body := string(make([]byte, 4*1024*1024))
	probe := makeNote("probe.md", body)
	perEntry := noteSize(probe)
	capacity := bodyCacheMaxB / perEntry

	for i := range capacity {
		path := fmt.Sprintf("n%05d.md", i)
		c.set(path, makeNote(path, body))
	}

	// Record the oldest entry (first inserted — at the front of the list).
	c.mu.Lock()
	oldestPath := c.order.Front().Value.(*bodyCacheEntry).path
	c.mu.Unlock()

	// Insert one more — should evict oldest to stay within budget.
	c.set("trigger.md", makeNote("trigger.md", body))

	// The oldest entry should now be gone.
	if _, ok := c.get(oldestPath); ok {
		t.Errorf("oldest entry %q still present after evicting insert", oldestPath)
	}
	// The new entry should be present.
	if _, ok := c.get("trigger.md"); !ok {
		t.Error("new entry not in cache after evicting insert")
	}
	// Budget must still be respected.
	c.mu.Lock()
	size := c.sizeB
	c.mu.Unlock()
	if size > bodyCacheMaxB {
		t.Errorf("cache size %d exceeds budget %d after eviction", size, bodyCacheMaxB)
	}
}

func TestBodyCache_InvalidateRemovesEntry(t *testing.T) {
	c := newBodyCache()
	c.set("x.md", makeNote("x.md", "data"))
	c.invalidate("x.md")
	if _, ok := c.get("x.md"); ok {
		t.Error("expected cache miss after invalidate")
	}
	if c.sizeB != 0 {
		t.Errorf("sizeB = %d, want 0 after invalidate", c.sizeB)
	}
}
