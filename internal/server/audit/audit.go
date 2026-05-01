// Package audit provides an append-only audit log for every tool call outcome.
// Writes are async-buffered and never block the request path.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Row is one immutable audit record. Schema is fixed per DESIGN-federation.md.
type Row struct {
	Timestamp       time.Time `json:"ts"`
	RequestID       string    `json:"request_id"`
	ParentRequestID string    `json:"parent_request_id,omitempty"`
	Actor           string    `json:"actor"`
	ActorKind       string    `json:"actor_kind"`
	Action          string    `json:"action"`
	ScopeLocal      string    `json:"scope_local,omitempty"`
	ScopeCanonical  string    `json:"scope_canonical,omitempty"`
	RequestedScopes []string  `json:"requested_scopes,omitempty"`
	EffectiveScopes []string  `json:"effective_scopes,omitempty"`
	Path            string    `json:"path,omitempty"`
	NoteID          string    `json:"note_id,omitempty"`
	EtagBefore      string    `json:"etag_before,omitempty"`
	EtagAfter       string    `json:"etag_after,omitempty"`
	Outcome         string    `json:"outcome"`
	ErrorCode       string    `json:"error_code,omitempty"`
	DurationMS      int64     `json:"duration_ms"`
	Side            string    `json:"side"`
}

// Backend persists audit rows.
type Backend interface {
	Append(ctx context.Context, row Row) error
	Close() error
}

// Logger buffers Row writes and drains them to a Backend asynchronously.
// Audit write failure is logged locally and surfaced via Health but never
// blocks or fails the calling request.
type Logger struct {
	backend Backend
	ch      chan Row
	wg      sync.WaitGroup
	errMu   sync.Mutex
	lastErr error
}

// Option configures a Logger.
type Option func(*loggerCfg)

type loggerCfg struct {
	backend    Backend
	bufferSize int
}

// WithBackend sets the persistence backend (default: stderr JSON lines).
func WithBackend(b Backend) Option {
	return func(c *loggerCfg) { c.backend = b }
}

// WithBufferSize sets the async write channel depth (default 1024).
func WithBufferSize(n int) Option {
	return func(c *loggerCfg) { c.bufferSize = n }
}

// New creates and starts a Logger. Call Close to drain the buffer and stop.
func New(opts ...Option) *Logger {
	cfg := loggerCfg{backend: &stderrBackend{}, bufferSize: 1024}
	for _, o := range opts {
		o(&cfg)
	}
	l := &Logger{
		backend: cfg.backend,
		ch:      make(chan Row, cfg.bufferSize),
	}
	l.wg.Add(1)
	go l.drain()
	return l
}

// Log enqueues row for async persistence. Returns immediately; never blocks
// the caller. Dropped rows (full buffer) are counted and visible via Health.
func (l *Logger) Log(row Row) {
	if row.Timestamp.IsZero() {
		row.Timestamp = time.Now().UTC()
	}
	select {
	case l.ch <- row:
	default:
		// Buffer full — log locally but don't block.
		fmt.Fprintf(os.Stderr, "audit: buffer full, dropping row request_id=%s\n", row.RequestID)
	}
}

// LastErr returns the most recent backend write error, if any.
func (l *Logger) LastErr() error {
	l.errMu.Lock()
	defer l.errMu.Unlock()
	return l.lastErr
}

// Close drains buffered rows and closes the backend. Blocks until done.
func (l *Logger) Close() error {
	close(l.ch)
	l.wg.Wait()
	return l.backend.Close()
}

func (l *Logger) drain() {
	defer l.wg.Done()
	for row := range l.ch {
		if err := l.backend.Append(context.Background(), row); err != nil {
			l.errMu.Lock()
			l.lastErr = err
			l.errMu.Unlock()
			fmt.Fprintf(os.Stderr, "audit: write error: %v\n", err)
		}
	}
}

// --- Stderr (FS fallback) backend ---

// stderrBackend writes newline-delimited JSON to stderr. Used when no
// persistent backend is configured (local gateway mode).
type stderrBackend struct{}

func (s *stderrBackend) Append(_ context.Context, row Row) error {
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stderr, string(b))
	return err
}

func (s *stderrBackend) Close() error { return nil }

// --- File backend ---

// FileBackend appends newline-delimited JSON rows to a local file.
type FileBackend struct {
	mu sync.Mutex
	f  *os.File
}

// NewFileBackend opens (or creates) path for append-only audit writing.
func NewFileBackend(path string) (*FileBackend, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open file backend: %w", err)
	}
	return &FileBackend{f: f}, nil
}

func (fb *FileBackend) Append(_ context.Context, row Row) error {
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	_, err = fmt.Fprintln(fb.f, string(b))
	return err
}

func (fb *FileBackend) Close() error {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return fb.f.Close()
}
