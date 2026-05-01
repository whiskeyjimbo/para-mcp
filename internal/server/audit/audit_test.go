package audit_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/server/audit"
)

type memBackend struct {
	mu   sync.Mutex
	rows []audit.Row
}

func (m *memBackend) Append(_ context.Context, row audit.Row) error {
	m.mu.Lock()
	m.rows = append(m.rows, row)
	m.mu.Unlock()
	return nil
}

func (m *memBackend) Close() error { return nil }

func TestLogger_AsyncWrite(t *testing.T) {
	be := &memBackend{}
	l := audit.New(audit.WithBackend(be), audit.WithBufferSize(16))

	l.Log(audit.Row{
		RequestID: "req_01JVXYZ1234567890ABCDEFGH",
		Actor:     "jrose",
		ActorKind: "user",
		Action:    "note_get",
		Outcome:   "ok",
		Side:      "gateway",
	})

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(be.rows))
	}
	if be.rows[0].Actor != "jrose" {
		t.Errorf("want actor jrose, got %s", be.rows[0].Actor)
	}
}

func TestLogger_TimestampAutoset(t *testing.T) {
	be := &memBackend{}
	l := audit.New(audit.WithBackend(be))
	before := time.Now().UTC().Truncate(time.Second)
	l.Log(audit.Row{RequestID: "req_TEST00000000000000000001", Outcome: "ok", Side: "gateway"})
	_ = l.Close()
	be.mu.Lock()
	ts := be.rows[0].Timestamp
	be.mu.Unlock()
	if ts.Before(before) {
		t.Errorf("timestamp not set: got %v", ts)
	}
}

func TestRow_JSONSchema(t *testing.T) {
	row := audit.Row{
		Timestamp:       time.Date(2026, 4, 28, 14, 32, 11, 123000000, time.UTC),
		RequestID:       "req_01JVXYZ1234567890ABCDEFGH",
		Actor:           "user@example.com",
		ActorKind:       "user",
		Action:          "note_update_body",
		ScopeLocal:      "team-platform",
		ScopeCanonical:  "team-infra",
		RequestedScopes: []string{"team-platform"},
		EffectiveScopes: []string{"team-platform"},
		Path:            "projects/x.md",
		Outcome:         "ok",
		DurationMS:      42,
		Side:            "gateway",
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ts", "request_id", "actor", "action", "outcome", "side"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in JSON", key)
		}
	}
	// empty fields should be omitted
	if _, ok := m["error_code"]; ok {
		t.Error("error_code should be omitted when empty")
	}
}
