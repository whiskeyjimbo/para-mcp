package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic/derived"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic/scoring"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic/tombstone"
)

// Embedder is an alias for the ports interface, exposed so tests can satisfy it without importing ports.
type Embedder = ports.Embedder

// Summarizer is an alias for the ports interface, exposed so tests can satisfy it without importing ports.
type Summarizer = ports.Summarizer

// ChangeKind describes what changed in a note event.
type ChangeKind int

const (
	ChangeBody        ChangeKind = iota // body/title changed → re-embed + re-summarize (debounced)
	ChangeFrontmatter                   // frontmatter-only → metadata-only update, no embed
	ChangeDelete                        // deleted → tombstone immediately
)

// NoteEvent is submitted to the Pipeline for processing.
type NoteEvent struct {
	NoteID string
	Ref    domain.NoteRef
	Body   string
	Kind   ChangeKind
}

// Config controls pipeline concurrency and timing.
type Config struct {
	MaxConcurrentEmbeddings int
	MaxConcurrentSummaries  int
	BodyDebounce            time.Duration
	CurrentSchema           int
	MaxRetryAttempts        int           // max embed/summarize attempts per event (default 3)
	RetryBaseDelay          time.Duration // initial backoff before first retry (default 500ms)
}

func (c *Config) applyDefaults() {
	if c.MaxConcurrentEmbeddings <= 0 {
		c.MaxConcurrentEmbeddings = 10
	}
	if c.MaxConcurrentSummaries <= 0 {
		c.MaxConcurrentSummaries = 5
	}
	if c.BodyDebounce <= 0 {
		c.BodyDebounce = 5 * time.Minute
	}
	if c.MaxRetryAttempts <= 0 {
		c.MaxRetryAttempts = 3
	}
	if c.RetryBaseDelay <= 0 {
		c.RetryBaseDelay = 500 * time.Millisecond
	}
}

// debounceEntry tracks the active timer and a sequence number for each debounced NoteID.
type debounceEntry struct {
	timer *time.Timer
	seq   int
}

// Pipeline coordinates embedding, vector storage, and summarisation for a vault.
type Pipeline struct {
	embedder   ports.Embedder
	vs         ports.VectorStore
	summarizer ports.Summarizer
	ds         derived.Store
	purger     *tombstone.Purger

	embedSem   *semaphore.Weighted
	summarySem *semaphore.Weighted
	sf         singleflight.Group

	// done is cancelled by Close() to unblock goroutines waiting on semaphore slots.
	done     context.Context
	shutdown context.CancelFunc

	mu       sync.Mutex
	debounce map[string]*debounceEntry

	cfg Config
}

// NewPipeline constructs a Pipeline with the given dependencies and config.
func NewPipeline(
	embedder ports.Embedder,
	vs ports.VectorStore,
	summarizer ports.Summarizer,
	ds derived.Store,
	purger *tombstone.Purger,
	cfg Config,
) *Pipeline {
	cfg.applyDefaults()
	done, shutdown := context.WithCancel(context.Background())
	return &Pipeline{
		embedder:   embedder,
		vs:         vs,
		summarizer: summarizer,
		ds:         ds,
		purger:     purger,
		embedSem:   semaphore.NewWeighted(int64(cfg.MaxConcurrentEmbeddings)),
		summarySem: semaphore.NewWeighted(int64(cfg.MaxConcurrentSummaries)),
		done:       done,
		shutdown:   shutdown,
		debounce:   make(map[string]*debounceEntry),
		cfg:        cfg,
	}
}

// Close cancels the shutdown context, unblocking any goroutines waiting on semaphore slots.
func (p *Pipeline) Close() { p.shutdown() }

// Submit enqueues a NoteEvent for processing.
//   - ChangeDelete: tombstones immediately in a detached goroutine.
//   - ChangeFrontmatter: dispatched asynchronously without debouncing.
//   - ChangeBody: debounced for cfg.BodyDebounce then processed asynchronously.
func (p *Pipeline) Submit(ctx context.Context, event NoteEvent) {
	detached := context.WithoutCancel(ctx)
	switch event.Kind {
	case ChangeDelete:
		go func() { _ = p.purger.Tombstone(detached, event.NoteID) }()
	case ChangeFrontmatter:
		go func() { _ = p.processFrontmatter(detached, event) }()
	case ChangeBody:
		p.mu.Lock()
		entry, ok := p.debounce[event.NoteID]
		seq := 0
		if ok {
			entry.timer.Stop()
			seq = entry.seq + 1
		}
		newSeq := seq
		newEntry := &debounceEntry{seq: seq}
		p.debounce[event.NoteID] = newEntry
		newEntry.timer = time.AfterFunc(p.cfg.BodyDebounce, func() {
			// Generation check: if this timer was superseded, skip.
			p.mu.Lock()
			if cur := p.debounce[event.NoteID]; cur == nil || cur.seq != newSeq {
				p.mu.Unlock()
				return
			}
			delete(p.debounce, event.NoteID)
			p.mu.Unlock()
			_ = p.processBody(detached, event)
		})
		p.mu.Unlock()
	}
}

// processBody runs embed → upsert → summarize → store for a body/title change.
func (p *Pipeline) processBody(ctx context.Context, event NoteEvent) error {
	bodyHash := hashBody(event.Body)

	// Skip if this (NoteID, bodyHash) was already processed.
	if meta, err := p.ds.Get(ctx, event.NoteID); err == nil && meta != nil && meta.BodyHash == bodyHash {
		return nil
	}

	// Include bodyHash in the key so concurrent calls for different bodies don't coalesce.
	sfKey := event.NoteID + ":" + bodyHash
	_, err, _ := p.sf.Do(sfKey, func() (any, error) {
		return nil, p.runBodyPipeline(ctx, event, bodyHash)
	})
	return err
}

// runBodyPipeline is the embed+upsert+summarize+store sequence.
func (p *Pipeline) runBodyPipeline(ctx context.Context, event NoteEvent, bodyHash string) error {
	chunkSize := scoring.ChunkSize(p.embedder.Dims())
	chunks := splitChunks(event.Body, chunkSize, scoring.ChunkOverlap)

	if err := p.embedSem.Acquire(p.done, 1); err != nil {
		return fmt.Errorf("pipeline shutting down before embed %q: %w", event.NoteID, err)
	}
	var vecs [][]float32
	if err := retryWithBackoff(p.done, p.cfg.MaxRetryAttempts, p.cfg.RetryBaseDelay, func() error {
		var e error
		vecs, e = p.embedder.Embed(ctx, chunks)
		return e
	}); err != nil {
		p.embedSem.Release(1)
		return fmt.Errorf("pipeline embed %q: %w", event.NoteID, err)
	}
	p.embedSem.Release(1)

	records := make([]domain.VectorRecord, len(chunks))
	for i, chunk := range chunks {
		records[i] = domain.VectorRecord{
			ID:     event.NoteID,
			Ref:    event.Ref,
			Chunk:  i,
			Vector: vecs[i],
			Body:   chunk,
		}
	}
	if err := p.vs.Upsert(ctx, records); err != nil {
		return fmt.Errorf("pipeline upsert %q: %w", event.NoteID, err)
	}

	if err := p.summarySem.Acquire(p.done, 1); err != nil {
		return fmt.Errorf("pipeline shutting down before summarize %q: %w", event.NoteID, err)
	}
	var meta *domain.DerivedMetadata
	if err := retryWithBackoff(p.done, p.cfg.MaxRetryAttempts, p.cfg.RetryBaseDelay, func() error {
		var e error
		meta, e = p.summarizer.Summarize(ctx, event.Ref, event.Body)
		return e
	}); err != nil {
		p.summarySem.Release(1)
		return fmt.Errorf("pipeline summarize %q: %w", event.NoteID, err)
	}
	p.summarySem.Release(1)

	meta.EmbedModel = p.embedder.ModelName()
	meta.BodyHash = bodyHash
	meta.SchemaVersion = p.cfg.CurrentSchema

	if err := p.ds.Set(ctx, event.NoteID, event.Ref, meta); err != nil {
		return fmt.Errorf("pipeline store derived %q: %w", event.NoteID, err)
	}
	return nil
}

// processFrontmatter handles frontmatter-only changes.
// No re-embed or re-summarize unless the schema version is stale.
func (p *Pipeline) processFrontmatter(ctx context.Context, event NoteEvent) error {
	meta, err := p.ds.Get(ctx, event.NoteID)
	if err != nil || meta == nil {
		return nil
	}
	if derived.ShouldRederive(meta, p.cfg.CurrentSchema) {
		return p.processBody(ctx, event)
	}
	return nil
}

const maxBackoffDelay = 30 * time.Second

// retryWithBackoff calls fn up to maxAttempts times with exponential backoff.
// It stops early if ctx is cancelled between attempts.
func retryWithBackoff(ctx context.Context, maxAttempts int, base time.Duration, fn func() error) error {
	var err error
	for attempt := range maxAttempts {
		if attempt > 0 {
			delay := min(base<<(attempt-1), maxBackoffDelay)
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
		err = fn()
		if err == nil {
			return nil
		}
	}
	return err
}

// hashBody returns a truncated hex SHA-256 of the body for idempotency keying.
func hashBody(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:8])
}

// splitChunks splits text into overlapping chunks of at most size runes.
func splitChunks(text string, size, overlap int) []string {
	runes := []rune(text)
	if size <= 0 || len(runes) <= size {
		return []string{text}
	}
	step := size - overlap
	if step <= 0 {
		step = size
	}
	var chunks []string
	for start := 0; start < len(runes); start += step {
		end := min(start+size, len(runes))
		chunks = append(chunks, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return chunks
}
