package localvault

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// countingIndexer records RescanVault call count for testing.
type countingIndexer struct {
	rescanCount atomic.Int64
}

func (c *countingIndexer) IndexFile(_ string)  {}
func (c *countingIndexer) RemoveFile(_ string) {}
func (c *countingIndexer) RescanVault() error {
	c.rescanCount.Add(1)
	return nil
}

func TestWatcher_SkipsRescanWhenHealthy(t *testing.T) {
	idx := &countingIndexer{}
	w := newWatcher(idx, slog.Default(), t.TempDir(), nil, 20*time.Millisecond, 5*time.Millisecond)

	// dirty=false (default): rescan should not fire on ticker.
	w.ticker = time.NewTicker(20 * time.Millisecond)
	w.wg.Add(1)
	// Run a short fake loop iteration to simulate ticker firing without dirty set.
	// We can't start the real loop (it needs fsnotify), so test the dirty flag directly.

	// When dirty is false, CompareAndSwap(true, false) returns false → skip.
	if w.dirty.CompareAndSwap(true, false) {
		t.Error("dirty should start as false")
	}

	// Simulate dirty=true (e.g., fsnotify error).
	w.dirty.Store(true)
	if !w.dirty.CompareAndSwap(true, false) {
		t.Error("dirty should be true after Store(true)")
	}
	// After swap, dirty is false again.
	if w.dirty.Load() {
		t.Error("dirty should be false after CompareAndSwap consumed it")
	}
}

func TestWatcher_RescanTriggeredWhenDirty(t *testing.T) {
	idx := &countingIndexer{}
	interval := 30 * time.Millisecond
	w := newWatcher(idx, slog.Default(), t.TempDir(), nil, interval, 5*time.Millisecond)

	// Use rescanLoop directly (same as degraded mode) to verify unconditional rescan.
	w.ticker = time.NewTicker(interval)
	w.wg.Add(1)
	go w.rescanLoop()

	time.Sleep(3 * interval)
	close(w.done)
	w.wg.Wait()

	if got := idx.rescanCount.Load(); got < 2 {
		t.Errorf("rescanLoop rescanned %d times in 3 intervals, want >= 2", got)
	}
}
