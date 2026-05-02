package tombstone_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infra/semantic/tombstone"
)

// --- stub VectorStore + DerivedStore for tests ---

type stubVectorStore struct {
	mu         sync.Mutex
	records    map[string]bool // id → exists
	tombstoned map[string]bool
}

func newStubVS() *stubVectorStore {
	return &stubVectorStore{records: map[string]bool{}, tombstoned: map[string]bool{}}
}
func (s *stubVectorStore) Upsert(_ context.Context, recs []domain.VectorRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range recs {
		s.records[r.ID] = true
	}
	return nil
}
func (s *stubVectorStore) Search(_ context.Context, _ []float32, _ domain.AuthFilter, _ int) ([]domain.VectorHit, error) {
	return nil, nil
}
func (s *stubVectorStore) Delete(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.records, id)
		delete(s.tombstoned, id)
	}
	return nil
}
func (s *stubVectorStore) Tombstone(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.tombstoned[id] = true
	}
	return nil
}
func (s *stubVectorStore) ListTombstoned(_ context.Context, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, v := range s.tombstoned {
		if v {
			ids = append(ids, id)
		}
		if len(ids) >= limit {
			break
		}
	}
	return ids, nil
}
func (s *stubVectorStore) Close() error { return nil }
func (s *stubVectorStore) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[id]
}
func (s *stubVectorStore) isTombstoned(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tombstoned[id]
}

type stubDerivedStore struct {
	mu      sync.Mutex
	records map[string]*domain.DerivedMetadata
}

func newStubDS() *stubDerivedStore {
	return &stubDerivedStore{records: map[string]*domain.DerivedMetadata{}}
}
func (s *stubDerivedStore) Get(_ context.Context, id string) (*domain.DerivedMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.records[id]; ok {
		return m, nil
	}
	return nil, domain.ErrNotFound
}
func (s *stubDerivedStore) Set(_ context.Context, id string, _ domain.NoteRef, m *domain.DerivedMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m == nil {
		delete(s.records, id)
		return nil
	}
	s.records[id] = m
	return nil
}
func (s *stubDerivedStore) IsEditedByUser(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *stubDerivedStore) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.records[id]
	return ok
}

// ---

func TestNotePurgeRemovesAllLocalLegs(t *testing.T) {
	vs := newStubVS()
	ds := newStubDS()

	ref := domain.NoteRef{Scope: "s", Path: "projects/a.md"}
	// Pre-populate
	vs.Upsert(context.Background(), []domain.VectorRecord{{ID: "note-1", Ref: ref}})
	ds.Set(context.Background(), "note-1", ref, &domain.DerivedMetadata{Summary: "x"})

	purger := tombstone.New(vs, ds, tombstone.Config{StartupSweepMax: 10})
	if err := purger.NotePurge(context.Background(), "note-1", ref); err != nil {
		t.Fatalf("NotePurge: %v", err)
	}

	if vs.has("note-1") {
		t.Error("vector record still present after NotePurge")
	}
	if ds.has("note-1") {
		t.Error("derived record still present after NotePurge")
	}
}

func TestNotePurgeExternalEnqueues(t *testing.T) {
	vs := newStubVS()
	ds := newStubDS()
	purger := tombstone.New(vs, ds, tombstone.Config{StartupSweepMax: 10})

	ref := domain.NoteRef{Scope: "s", Path: "projects/a.md"}
	if err := purger.NotePurgeExternal(context.Background(), "note-1", ref); err != nil {
		t.Fatalf("NotePurgeExternal: %v", err)
	}
	// Verify the item is in the purge queue.
	if !purger.IsPendingExternal("note-1") {
		t.Error("note-1 not in external purge queue")
	}
}

func TestSweeperRemovesOrphanVectors(t *testing.T) {
	vs := newStubVS()
	ds := newStubDS()

	// Simulate crash mid-delete: tombstone written, VectorStore.Delete not called.
	ref := domain.NoteRef{Scope: "s", Path: "projects/a.md"}
	vs.Upsert(context.Background(), []domain.VectorRecord{{ID: "orphan-1", Ref: ref}})
	vs.Tombstone(context.Background(), []string{"orphan-1"})

	purger := tombstone.New(vs, ds, tombstone.Config{
		StartupSweepMax: 10,
		OrphanMaxAge:    1 * time.Millisecond, // short for test
	})

	time.Sleep(5 * time.Millisecond) // ensure orphan is "old enough"
	swept, err := purger.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if swept == 0 {
		t.Error("sweeper did not remove any orphan vectors")
	}
}

func TestSweeperStartupBoundedByMax(t *testing.T) {
	vs := newStubVS()
	ds := newStubDS()

	// Tombstone 20 vectors.
	var recs []domain.VectorRecord
	for i := range 20 {
		id := domain.DeriveNoteID("path", string(rune('a'+i)))
		recs = append(recs, domain.VectorRecord{
			ID:  id,
			Ref: domain.NoteRef{Scope: "s", Path: "p"},
		})
	}
	vs.Upsert(context.Background(), recs)
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	vs.Tombstone(context.Background(), ids)

	purger := tombstone.New(vs, ds, tombstone.Config{
		StartupSweepMax: 5,
		OrphanMaxAge:    0, // no age requirement for test
	})

	swept, err := purger.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if swept > 5 {
		t.Errorf("sweep processed %d items, expected <= 5", swept)
	}
}
