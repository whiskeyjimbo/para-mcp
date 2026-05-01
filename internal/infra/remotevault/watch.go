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

// sseEvent is a parsed Server-Sent Event.
type sseEvent struct {
	Data string
}

// WatchEvents connects to the remote server's /events SSE endpoint and calls
// onEvent for each received event data payload. It reconnects automatically on
// transient errors until ctx is cancelled. This method is intended to be run
// as a goroutine; callers should start it only when Capabilities().Watch is true.
func (v *RemoteVault) WatchEvents(ctx context.Context, onEvent func(eventType, path string)) error {
	eventsURL := strings.TrimRight(v.baseURL, "/") + "/events?scope=" + string(v.canonicalRemote)
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := v.streamSSE(ctx, eventsURL, onEvent); err != nil && ctx.Err() == nil {
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

func (v *RemoteVault) streamSSE(ctx context.Context, url string, onEvent func(eventType, path string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE endpoint returned %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var dataLine string
	for scanner.Scan() {
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
	return scanner.Err()
}

// StartWatch starts a background SSE subscriber that invalidates summary and body caches
// on remote note changes. Returns a stop function. Should only be called when
// Capabilities().Watch is true.
func (v *RemoteVault) StartWatch(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		_ = v.WatchEvents(ctx, func(eventType, path string) {
			if path != "" {
				v.bodies.invalidate(path)
			}
			v.summaries.invalidate()
		})
	}()
	return cancel
}
