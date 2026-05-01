package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// NoteEvent describes a change to a note published after successful mutations.
type NoteEvent struct {
	Type  string         `json:"type"` // "note_changed" | "note_deleted"
	Scope domain.ScopeID `json:"scope"`
	Path  string         `json:"path"`
}

// EventBus is a fan-out pub/sub bus for note change events.
type EventBus struct {
	mu          sync.Mutex
	subscribers map[int]chan NoteEvent
	next        int
}

// NewEventBus creates a new EventBus.
func NewEventBus() *EventBus {
	return &EventBus{subscribers: make(map[int]chan NoteEvent)}
}

// Publish broadcasts an event to all current subscribers (non-blocking; slow subscribers drop events).
func (b *EventBus) Publish(e NoteEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}

// SubscriberCount returns the current number of active subscribers.
func (b *EventBus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers)
}

// Subscribe returns a channel that receives events and an unsubscribe function.
func (b *EventBus) Subscribe() (<-chan NoteEvent, func()) {
	b.mu.Lock()
	id := b.next
	b.next++
	ch := make(chan NoteEvent, 64)
	b.subscribers[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subscribers, id)
		b.mu.Unlock()
	}
}

// SSEHandler returns an HTTP handler that streams NoteEvents to clients as Server-Sent Events.
// Clients may optionally filter by ?scope= query parameter.
func SSEHandler(bus *EventBus) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scopeFilter := r.URL.Query().Get("scope")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		ch, unsub := bus.Subscribe()
		defer unsub()

		_, _ = fmt.Fprintf(w, ": connected\n\n")
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case e := <-ch:
				if scopeFilter != "" && string(e.Scope) != scopeFilter {
					continue
				}
				data, err := json.Marshal(e)
				if err != nil {
					continue
				}
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	})
}
