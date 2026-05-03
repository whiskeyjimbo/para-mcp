package mcp

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSSEHandler_EmitsHeartbeats: a connected client with no real events must
// still receive periodic SSE comment frames on the heartbeat cadence so a
// stalled connection can be detected client-side.
func TestSSEHandler_EmitsHeartbeats(t *testing.T) {
	const interval = 30 * time.Millisecond
	bus := NewEventBus(WithHeartbeatInterval(interval))
	ts := httptest.NewServer(SSEHandler(bus))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	// Read frames for ~5 intervals; expect ≥3 heartbeat comment lines.
	deadline := time.Now().Add(5 * interval)
	scanner := bufio.NewScanner(resp.Body)
	heartbeats := 0
	done := make(chan struct{})
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			// SSE comments begin with ':' (the initial ': connected' frame and heartbeat pings).
			if strings.HasPrefix(line, ":") {
				heartbeats++
			}
			if time.Now().After(deadline) {
				break
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5*interval + 200*time.Millisecond):
	}

	if heartbeats < 3 {
		t.Errorf("expected ≥3 heartbeat/comment frames within %v, got %d", 5*interval, heartbeats)
	}
}
