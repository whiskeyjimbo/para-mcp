package semantic_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infra/semantic"
	"github.com/whiskeyjimbo/paras/internal/infra/semantic/tombstone"
)

// --- stubs ---

type countingEmbedder struct {
	mu    sync.Mutex
	calls int
	dims  int
	// peak tracks the maximum concurrent embed calls in flight.
	inflight int32
	peak     int32
}

func newEmbedder(dims int) *countingEmbedder { return &countingEmbedder{dims: dims} }

func (e *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	cur := atomic.AddInt32(&e.inflight, 1)
	for {
		old := atomic.LoadInt32(&e.peak)
		if cur <= old || atomic.CompareAndSwapInt32(&e.peak, old, cur) {
			break
		}
	}
	defer atomic.AddInt32(&e.inflight, -1)

	e.mu.Lock()
	e.calls++
	e.mu.Unlock()

	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, e.dims)
	}
	return out, nil
}

func (e *countingEmbedder) Dims() int         { return e.dims }
func (e *countingEmbedder) ModelName() string { return "stub" }

func (e *countingEmbedder) embedCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type countingSummarizer struct {
	mu    sync.Mutex
	calls int
}

func (s *countingSummarizer) Summarize(_ context.Context, _ domain.NoteRef, _ string) (*domain.DerivedMetadata, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return &domain.DerivedMetadata{Summary: "stub", Purpose: "stub"}, nil
}

func (s *countingSummarizer) summarizeCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type stubVS struct {
	mu         sync.Mutex
	tombstoned []string
	records    map[string]bool
}

func newStubVS() *stubVS {
	return &stubVS{records: map[string]bool{}}
}

func (s *stubVS) Upsert(_ context.Context, recs []domain.VectorRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range recs {
		s.records[r.ID] = true
	}
	return nil
}
func (s *stubVS) Search(_ context.Context, _ []float32, _ domain.AuthFilter, _ int) ([]domain.VectorHit, error) {
	return nil, nil
}
func (s *stubVS) Delete(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.records, id)
	}
	return nil
}
func (s *stubVS) Tombstone(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tombstoned = append(s.tombstoned, ids...)
	return nil
}
func (s *stubVS) ListTombstoned(_ context.Context, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tombstoned) < limit {
		return s.tombstoned, nil
	}
	return s.tombstoned[:limit], nil
}
func (s *stubVS) Close() error { return nil }

type stubDS struct {
	mu      sync.Mutex
	records map[string]*domain.DerivedMetadata
}

func newStubDS() *stubDS {
	return &stubDS{records: map[string]*domain.DerivedMetadata{}}
}

func (s *stubDS) Get(_ context.Context, id string) (*domain.DerivedMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.records[id]; ok {
		return m, nil
	}
	return nil, domain.ErrNotFound
}
func (s *stubDS) Set(_ context.Context, id string, _ domain.NoteRef, m *domain.DerivedMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m == nil {
		delete(s.records, id)
		return nil
	}
	s.records[id] = m
	return nil
}
func (s *stubDS) IsEditedByUser(_ context.Context, _ string) (bool, error) { return false, nil }

// ---

func makePipeline(t *testing.T, emb *countingEmbedder, sum *countingSummarizer, vs *stubVS, ds *stubDS, cfg semantic.Config) *semantic.Pipeline {
	t.Helper()
	purger := tombstone.New(vs, ds, tombstone.Config{StartupSweepMax: 100})
	return semantic.NewPipeline(emb, vs, sum, ds, purger, cfg)
}

func TestPipelineBodyEventDebounce(t *testing.T) {
	emb := newEmbedder(4)
	sum := &countingSummarizer{}
	vs := newStubVS()
	ds := newStubDS()

	p := makePipeline(t, emb, sum, vs, ds, semantic.Config{
		BodyDebounce: 20 * time.Millisecond,
	})

	ref := domain.NoteRef{Scope: "s", Path: "a.md"}
	// Submit three events in quick succession — only one embed should fire.
	for i := range 3 {
		p.Submit(context.Background(), semantic.NoteEvent{
			NoteID: "note-1",
			Ref:    ref,
			Body:   "body v" + string(rune('1'+i)),
			Kind:   semantic.ChangeBody,
		})
	}
	time.Sleep(80 * time.Millisecond) // wait for debounce + processing

	if got := emb.embedCalls(); got != 1 {
		t.Errorf("embed called %d times, want 1 (debounce should coalesce)", got)
	}
}

func TestPipelineBodyEventIdempotent(t *testing.T) {
	emb := newEmbedder(4)
	sum := &countingSummarizer{}
	vs := newStubVS()
	ds := newStubDS()

	p := makePipeline(t, emb, sum, vs, ds, semantic.Config{
		BodyDebounce: 5 * time.Millisecond,
	})

	ref := domain.NoteRef{Scope: "s", Path: "a.md"}
	event := semantic.NoteEvent{NoteID: "note-2", Ref: ref, Body: "stable body", Kind: semantic.ChangeBody}

	// First submission — should embed + summarize.
	p.Submit(context.Background(), event)
	time.Sleep(50 * time.Millisecond)
	if got := emb.embedCalls(); got != 1 {
		t.Fatalf("first submit: embed called %d times, want 1", got)
	}

	// Second submission with same body — idempotency key matches, no re-embed.
	p.Submit(context.Background(), event)
	time.Sleep(50 * time.Millisecond)
	if got := emb.embedCalls(); got != 1 {
		t.Errorf("second submit (same body): embed called %d times, want still 1", got)
	}
}

func TestPipelineFrontmatterOnlyNoEmbed(t *testing.T) {
	emb := newEmbedder(4)
	sum := &countingSummarizer{}
	vs := newStubVS()
	ds := newStubDS()

	// Pre-populate with a fresh schema version so ShouldRederive returns false.
	ds.Set(context.Background(), "note-3", domain.NoteRef{}, &domain.DerivedMetadata{
		Summary:       "existing",
		Purpose:       "existing",
		BodyHash:      "abc",
		SchemaVersion: 1,
	})

	p := makePipeline(t, emb, sum, vs, ds, semantic.Config{
		BodyDebounce:  5 * time.Millisecond,
		CurrentSchema: 1,
	})

	p.Submit(context.Background(), semantic.NoteEvent{
		NoteID: "note-3",
		Ref:    domain.NoteRef{Scope: "s", Path: "a.md"},
		Body:   "body",
		Kind:   semantic.ChangeFrontmatter,
	})
	time.Sleep(40 * time.Millisecond)

	if got := emb.embedCalls(); got != 0 {
		t.Errorf("frontmatter event: embed called %d times, want 0", got)
	}
	if got := sum.summarizeCalls(); got != 0 {
		t.Errorf("frontmatter event: summarize called %d times, want 0", got)
	}
}

func TestPipelineDeleteTombstones(t *testing.T) {
	emb := newEmbedder(4)
	sum := &countingSummarizer{}
	vs := newStubVS()
	ds := newStubDS()

	p := makePipeline(t, emb, sum, vs, ds, semantic.Config{})

	p.Submit(context.Background(), semantic.NoteEvent{
		NoteID: "note-4",
		Ref:    domain.NoteRef{Scope: "s", Path: "a.md"},
		Kind:   semantic.ChangeDelete,
	})
	time.Sleep(30 * time.Millisecond)

	vs.mu.Lock()
	tombstoned := vs.tombstoned
	vs.mu.Unlock()
	found := false
	for _, id := range tombstoned {
		if id == "note-4" {
			found = true
			break
		}
	}
	if !found {
		t.Error("note-4 not tombstoned after delete event")
	}
}

func TestPipelineWorkerOutlivesInitiatorContext(t *testing.T) {
	emb := newEmbedder(4)
	sum := &countingSummarizer{}
	vs := newStubVS()
	ds := newStubDS()

	p := makePipeline(t, emb, sum, vs, ds, semantic.Config{
		BodyDebounce: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	p.Submit(ctx, semantic.NoteEvent{
		NoteID: "note-5",
		Ref:    domain.NoteRef{Scope: "s", Path: "a.md"},
		Body:   "body",
		Kind:   semantic.ChangeBody,
	})
	// Cancel the initiator context before the debounce fires.
	cancel()

	time.Sleep(100 * time.Millisecond) // debounce + processing

	if got := emb.embedCalls(); got != 1 {
		t.Errorf("embed called %d times after context cancel, want 1 (detached context)", got)
	}
}

func TestPipelineEmbedConcurrencyLimited(t *testing.T) {
	const maxEmbed = 3
	emb := newEmbedder(4)
	sum := &countingSummarizer{}
	vs := newStubVS()
	ds := newStubDS()

	p := makePipeline(t, emb, sum, vs, ds, semantic.Config{
		BodyDebounce:            1 * time.Millisecond,
		MaxConcurrentEmbeddings: maxEmbed,
	})

	// Submit 10 distinct notes simultaneously.
	for i := range 10 {
		p.Submit(context.Background(), semantic.NoteEvent{
			NoteID: domain.DeriveNoteID("p", string(rune('a'+i))),
			Ref:    domain.NoteRef{Scope: "s", Path: "a.md"},
			Body:   "body " + string(rune('a'+i)),
			Kind:   semantic.ChangeBody,
		})
	}
	time.Sleep(200 * time.Millisecond)

	if got := int(atomic.LoadInt32(&emb.peak)); got > maxEmbed {
		t.Errorf("peak concurrent embeds = %d, want <= %d", got, maxEmbed)
	}
}
