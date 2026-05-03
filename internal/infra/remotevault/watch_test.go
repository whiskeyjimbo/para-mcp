package remotevault

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

// sseTestServer streams a single event then closes the connection. Each
// connection (including reconnects) increments connects.
type sseTestServer struct {
	mu       sync.Mutex
	connects int
}

func (s *sseTestServer) handler(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.connects++
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()
	// Close immediately to simulate a dropped stream.
}

func (s *sseTestServer) connectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connects
}

func newRemoteVaultForWatch(baseURL string) *RemoteVault {
	return &RemoteVault{
		localScope:      domain.ScopeID("test"),
		canonicalRemote: "test",
		baseURL:         baseURL,
		summaries:       newSummaryCache(),
		bodies:          newBodyCache(),
	}
}

// TestStartWatch_InvalidatesCachesOnDisconnect: when SSE drops, both summary
// and body caches must be cleared so the next read repopulates from source.
func TestStartWatch_InvalidatesCachesOnDisconnect(t *testing.T) {
	srv := &sseTestServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	v := newRemoteVaultForWatch(ts.URL)
	v.summaries.set("k", domain.QueryResult{Notes: []domain.NoteSummary{{Ref: domain.NoteRef{Path: "a.md"}}}})
	v.bodies.set("a.md", domain.Note{Ref: domain.NoteRef{Path: "a.md"}, Body: "hello"})
	v.bodies.set("b.md", domain.Note{Ref: domain.NoteRef{Path: "b.md"}, Body: "world"})

	stop := v.StartWatch(t.Context())
	defer stop()

	// Wait for at least one reconnect (i.e. one full disconnect) to occur,
	// signalling that the post-disconnect invalidation hook had a chance to fire.
	deadline := time.Now().Add(3 * time.Second)
	for srv.connectionCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if srv.connectionCount() < 2 {
		t.Fatalf("expected ≥2 connection attempts (initial + reconnect), got %d", srv.connectionCount())
	}

	if _, ok := v.summaries.get("k"); ok {
		t.Error("summary cache still populated after SSE disconnect")
	}
	if _, ok := v.bodies.get("a.md"); ok {
		t.Error("body cache entry a.md still populated after SSE disconnect")
	}
	if _, ok := v.bodies.get("b.md"); ok {
		t.Error("body cache entry b.md still populated after SSE disconnect")
	}
}

// TestStartWatch_DoesNotInvalidateOnInitialDialFailure: if WatchEvents never
// successfully connects (remote down at boot), caches must NOT be wiped — that
// would permanently disable caching during a brief outage.
func TestStartWatch_DoesNotInvalidateOnInitialDialFailure(t *testing.T) {
	// Server returns 500 on every request: connection established but never
	// successful (StatusCode != 200), so the "connected" flag is never set.
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer ts.Close()

	v := newRemoteVaultForWatch(ts.URL)
	v.summaries.set("k", domain.QueryResult{})
	v.bodies.set("a.md", domain.Note{Ref: domain.NoteRef{Path: "a.md"}, Body: "x"})

	stop := v.StartWatch(t.Context())
	defer stop()

	// Wait for the watcher to make at least one failed connection attempt
	// (signalling the failure path was exercised) without sleeping a fixed budget.
	deadline := time.Now().Add(3 * time.Second)
	for attempts.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if attempts.Load() < 1 {
		t.Fatal("server received no connection attempts — watcher did not start")
	}

	if _, ok := v.summaries.get("k"); !ok {
		t.Error("summary cache wiped on failed initial connect — should only invalidate after a successful connection")
	}
	if _, ok := v.bodies.get("a.md"); !ok {
		t.Error("body cache wiped on failed initial connect — should only invalidate after a successful connection")
	}
}
