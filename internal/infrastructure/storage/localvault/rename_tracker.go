package localvault

import (
	"sync"
	"time"
)

// renamePairTracker correlates Remove/Rename + Create event pairs within a
// short window so that file renames are handled atomically rather than as a
// delete followed by a create.
type renamePairTracker struct {
	mu      sync.Mutex
	pending map[string]time.Time
	window  time.Duration
}

func newRenamePairTracker(window time.Duration) *renamePairTracker {
	return &renamePairTracker{
		pending: make(map[string]time.Time),
		window:  window,
	}
}

// MarkRemoved records path as removed and returns whether it was already pending.
func (t *renamePairTracker) MarkRemoved(path string) (alreadyPending bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, alreadyPending = t.pending[path]
	t.pending[path] = time.Now().Add(t.window)
	return alreadyPending
}

// ClaimIfPending removes path from pending and reports whether it was still there.
func (t *renamePairTracker) ClaimIfPending(path string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.pending[path]; !ok {
		return false
	}
	delete(t.pending, path)
	return true
}

// FindPairedRemoval returns the pending path whose base name matches newPath
// (case-insensitive) and whose deadline is still in the future, then removes
// it from pending. Returns "" if none found.
func (t *renamePairTracker) FindPairedRemoval(newPath string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	for old, deadline := range t.pending {
		if time.Now().Before(deadline) && isSameBase(old, newPath) {
			delete(t.pending, old)
			return old
		}
	}
	return ""
}
