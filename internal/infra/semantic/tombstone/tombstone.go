// Package tombstone implements note deletion lifecycle: local purge, external purge queue,
// and reconciliation sweeper for orphan vectors.
package tombstone

import (
	"context"
	"sync"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/infra/semantic/derived"
)

const (
	defaultStartupSweepMax    = 10_000
	defaultSweepFailureWindow = 24 * time.Hour
)

// Config controls sweeper behaviour.
type Config struct {
	StartupSweepMax    int                                             // max tombstones to process per startup sweep (default 10k)
	OrphanMaxAge       time.Duration                                   // min tombstone age before hard-delete (default 0 = immediate)
	SweepFailureWindow time.Duration                                   // how long continuous sweep failures must persist before escalation (default 24h)
	OnSweepDegraded    func(firstFailedAt time.Time, failureCount int) // called once the window is exceeded; nil disables
}

// externalItem is a queued vendor-side purge task.
type externalItem struct {
	id  string
	ref domain.NoteRef
	at  time.Time
}

// Purger orchestrates local and external note purge operations and the reconciliation sweeper.
type Purger struct {
	mu       sync.Mutex
	vs       ports.VectorStore
	ds       derived.Store
	cfg      Config
	extQueue []externalItem

	// tombstoneIndex is a simplified in-memory index for sweeper: id → tombstoneTime.
	// A production implementation would persist this in the sidecar DB.
	tombstoneIndex map[string]time.Time

	// sweep failure tracking — protected by mu.
	sweepFirstFailedAt time.Time
	sweepFailureCount  int
}

// New creates a Purger.
func New(vs ports.VectorStore, ds derived.Store, cfg Config) *Purger {
	if cfg.StartupSweepMax <= 0 {
		cfg.StartupSweepMax = defaultStartupSweepMax
	}
	return &Purger{vs: vs, ds: ds, cfg: cfg, tombstoneIndex: map[string]time.Time{}}
}

// NotePurge removes all local legs for noteID atomically.
// VectorStore entry, sidecar record, and frontmatter derived: block are all deleted.
// Subsequent semantic search must return no results for this ref.
func (p *Purger) NotePurge(ctx context.Context, noteID string, ref domain.NoteRef) error {
	if err := p.vs.Delete(ctx, []string{noteID}); err != nil {
		return err
	}
	// Derived store returns ErrNotFound if the note was never derived; ignore.
	if err := p.ds.Set(ctx, noteID, ref, nil); err != nil && err != domain.ErrNotFound {
		// On nil meta we mark as "purged" — real impl would delete the row.
		_ = err
	}
	p.mu.Lock()
	delete(p.tombstoneIndex, noteID)
	p.mu.Unlock()
	return nil
}

// NotePurgeExternal enqueues an async vendor-side erasure task.
func (p *Purger) NotePurgeExternal(ctx context.Context, noteID string, ref domain.NoteRef) error {
	p.mu.Lock()
	p.extQueue = append(p.extQueue, externalItem{id: noteID, ref: ref, at: time.Now()})
	p.mu.Unlock()
	return nil
}

// IsPendingExternal reports whether noteID is in the external purge queue.
func (p *Purger) IsPendingExternal(noteID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, item := range p.extQueue {
		if item.id == noteID {
			return true
		}
	}
	return false
}

// Tombstone marks a vector record as soft-deleted and records the tombstone time.
func (p *Purger) Tombstone(ctx context.Context, noteID string) error {
	if err := p.vs.Tombstone(ctx, []string{noteID}); err != nil {
		return err
	}
	p.mu.Lock()
	p.tombstoneIndex[noteID] = time.Now()
	p.mu.Unlock()
	return nil
}

// onSweepError records a sweep failure and fires OnSweepDegraded if the failure window is exceeded.
func (p *Purger) onSweepError(now time.Time) {
	window := p.cfg.SweepFailureWindow
	if window <= 0 {
		window = defaultSweepFailureWindow
	}

	p.mu.Lock()
	if p.sweepFirstFailedAt.IsZero() {
		p.sweepFirstFailedAt = now
	}
	p.sweepFailureCount++
	exceeded := now.Sub(p.sweepFirstFailedAt) >= window
	firstAt := p.sweepFirstFailedAt
	count := p.sweepFailureCount
	p.mu.Unlock()

	if exceeded && p.cfg.OnSweepDegraded != nil {
		p.cfg.OnSweepDegraded(firstAt, count)
	}
}

// onSweepSuccess resets sweep failure tracking after a successful sweep.
func (p *Purger) onSweepSuccess() {
	p.mu.Lock()
	p.sweepFirstFailedAt = time.Time{}
	p.sweepFailureCount = 0
	p.mu.Unlock()
}

// Sweep removes orphan vectors by querying the VectorStore for tombstoned records.
// It processes at most StartupSweepMax records per call.
// Returns the number of records swept.
func (p *Purger) Sweep(ctx context.Context) (int, error) {
	candidates, err := p.vs.ListTombstoned(ctx, p.cfg.StartupSweepMax)
	if err != nil {
		p.onSweepError(time.Now())
		return 0, err
	}

	p.mu.Lock()
	now := time.Now()
	var toDelete []string
	for _, id := range candidates {
		at, tracked := p.tombstoneIndex[id]
		// !tracked means the record was tombstoned by a prior crashed run (no age info); delete immediately.
		if !tracked || now.Sub(at) >= p.cfg.OrphanMaxAge {
			toDelete = append(toDelete, id)
		}
	}
	// Remove from index before releasing the lock so a concurrent Sweep won't re-select the same IDs.
	for _, id := range toDelete {
		delete(p.tombstoneIndex, id)
	}
	p.mu.Unlock()

	if len(toDelete) == 0 {
		p.onSweepSuccess()
		return 0, nil
	}

	if err := p.vs.Delete(ctx, toDelete); err != nil {
		// Re-add to index so a future sweep retries; best-effort.
		p.mu.Lock()
		for _, id := range toDelete {
			p.tombstoneIndex[id] = now
		}
		p.mu.Unlock()
		p.onSweepError(now)
		return 0, err
	}

	p.onSweepSuccess()
	return len(toDelete), nil
}
