package mcp

// SSE push acceptance test for FEAT-005 DoD.
// Verifies: when the remote server emits an SSE note_changed event, the
// RemoteVault summary cache is invalidated and the next Query fetches from
// the network rather than serving the stale cached result.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/paras/internal/application"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/infra/remotevault"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
)

// queryCounter wraps LocalVault and records how many times Query is called
// so the test can distinguish cache hits (no server call) from cache misses.
type queryCounter struct {
	ports.Vault
	mu    sync.Mutex
	calls int
}

func (c *queryCounter) Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.Vault.Query(ctx, q)
}

func (c *queryCounter) load() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestSSEPushInvalidatesSummaryCache(t *testing.T) {
	const scope = "team-x"

	// Backing storage with a query counter so we can observe cache misses.
	lv, err := localvault.New(scope, t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = lv.Close() })
	counter := &queryCounter{Vault: lv}

	// MCP server with an EventBus so mutation handlers can publish SSE events.
	bus := NewEventBus()
	mcpSrv := Build(
		application.NewService(counter),
		WithEventBus(bus),
		WithScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{scope} }),
	)
	streamable := mcpserver.NewStreamableHTTPServer(mcpSrv)

	// Single HTTP mux: MCP streamable at "/" and SSE events at "/events".
	mux := http.NewServeMux()
	mux.Handle("/events", SSEHandler(bus))
	mux.Handle("/", streamable)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Connect a RemoteVault and start watching for SSE events.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	rv, err := remotevault.New(ctx, remotevault.Config{
		LocalScope:      scope,
		CanonicalRemote: scope,
		BaseURL:         ts.URL,
	})
	if err != nil {
		t.Fatalf("remotevault.New: %v", err)
	}
	stopWatch := rv.StartWatch(ctx)
	t.Cleanup(stopWatch)

	q := domain.QueryRequest{Limit: 10}

	// First Query: cache miss — should reach the server.
	if _, err := rv.Query(ctx, q); err != nil {
		t.Fatalf("Query #1: %v", err)
	}
	if got := counter.load(); got != 1 {
		t.Fatalf("after first Query: server calls = %d, want 1", got)
	}

	// Second Query: cache hit — server must NOT be called again.
	if _, err := rv.Query(ctx, q); err != nil {
		t.Fatalf("Query #2: %v", err)
	}
	if got := counter.load(); got != 1 {
		t.Fatalf("after second Query (cache hit expected): server calls = %d, want 1", got)
	}

	// Wait for the watch goroutine to establish its SSE connection before publishing.
	// Without this, the event may be broadcast with no subscribers and be dropped.
	connectDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(connectDeadline) && bus.SubscriberCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if bus.SubscriberCount() == 0 {
		t.Fatal("SSE watch goroutine did not connect within 2 s")
	}

	// Push an SSE event from the server side.
	bus.Publish(NoteEvent{Type: "note_changed", Scope: scope, Path: "docs/foo.md"})

	// Poll until the RemoteVault's cache is invalidated and a network call goes
	// through, or fail after a generous push-latency budget.
	deadline := time.Now().Add(2 * time.Second)
	invalidated := false
	for time.Now().Before(deadline) {
		if _, err := rv.Query(ctx, q); err != nil {
			t.Fatalf("Query during poll: %v", err)
		}
		if counter.load() >= 2 {
			invalidated = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !invalidated {
		t.Fatal("summary cache was not invalidated within 2 s of SSE event push")
	}
}
