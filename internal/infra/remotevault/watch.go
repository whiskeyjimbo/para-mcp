package remotevault

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// streamReadTimeout bounds how long the client will wait for any frame on an
// SSE stream before tearing the connection down. Sized to ~2× the server's
// default heartbeat interval (mcp.DefaultHeartbeatInterval = 15s) so a normally
// idle stream punctuated by heartbeats stays connected indefinitely. Defined
// as a package var to allow tests to inject a short timeout.
var streamReadTimeout = 30 * time.Second

// sseEvent is a parsed Server-Sent Event.
type sseEvent struct {
	Data string
}

// WatchEvents connects to the remote server's /events SSE endpoint and calls
// onEvent for each received event data payload. It reconnects automatically on
// transient errors until ctx is cancelled. onDisconnect, if non-nil, is called
// after a successful connection drops — failed initial dials before any
// successful connection do not trigger it. This method is intended to be run
// as a goroutine; callers should start it only when Capabilities().Watch is true.
func (v *RemoteVault) WatchEvents(ctx context.Context, onEvent func(eventType, path string), onDisconnect func()) error {
	eventsURL := strings.TrimRight(v.baseURL, "/") + "/events?scope=" + string(v.canonicalRemote)
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		connected, err := v.streamSSE(ctx, eventsURL, onEvent)
		if connected && onDisconnect != nil {
			onDisconnect()
		}
		if err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				if backoff < 30*time.Second {
					backoff *= 2
				}
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else {
			backoff = time.Second
		}
	}
}

// streamSSE opens the SSE stream and reads events until the connection ends or
// the stream stalls (no frame received within streamReadTimeout). Returns
// connected=true once HTTP 200 is received, regardless of how the stream
// subsequently terminates.
func (v *RemoteVault) streamSSE(ctx context.Context, url string, onEvent func(eventType, path string)) (connected bool, err error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("SSE endpoint returned %d", resp.StatusCode)
	}
	connected = true

	// Stall watchdog: any activity (real event or heartbeat comment) resets a
	// timer; if the timer fires before the next frame arrives, cancel the
	// stream context to tear the connection down.
	activity := make(chan struct{}, 1)
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		timer := time.NewTimer(streamReadTimeout)
		defer timer.Stop()
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(streamReadTimeout)
			case <-timer.C:
				cancel()
				return
			}
		}
	}()

	scanner := bufio.NewScanner(resp.Body)
	var dataLine string
	for scanner.Scan() {
		// Any line (data, comment heartbeat, blank separator) signals liveness.
		select {
		case activity <- struct{}{}:
		default:
		}
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			dataLine = after
		} else if line == "" && dataLine != "" {
			var e struct {
				Type string `json:"type"`
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(dataLine), &e); err == nil {
				onEvent(e.Type, e.Path)
			}
			dataLine = ""
		}
	}
	scanErr := scanner.Err()
	cancel()
	<-watchdogDone
	return connected, scanErr
}

// StartWatch starts a background SSE subscriber that invalidates summary and body caches
// on remote note changes, and clears both caches whenever a previously-successful SSE
// connection drops (so missed events during the disconnect window cannot serve stale
// data beyond TTL). Returns a stop function that cancels the watcher and blocks until
// the background goroutine exits. Should only be called when Capabilities().Watch is true.
func (v *RemoteVault) StartWatch(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = v.WatchEvents(ctx,
			func(eventType, path string) {
				if path != "" {
					v.bodies.invalidate(path)
				}
				v.summaries.invalidate()
			},
			func() {
				v.summaries.invalidate()
				v.bodies.invalidateAll()
			},
		)
	}()
	return func() {
		cancel()
		<-done
	}
}
