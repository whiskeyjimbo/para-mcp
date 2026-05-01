package audit_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/server/audit"
)

func writeRows(t *testing.T, path string, rows []audit.Row) *audit.FileBackend {
	t.Helper()
	be, err := audit.NewFileBackend(path)
	if err != nil {
		t.Fatalf("NewFileBackend: %v", err)
	}
	for _, r := range rows {
		if err := be.Append(context.Background(), r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return be
}

func tmpPath(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "audit*.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

var (
	t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Minute)
	t2 = t0.Add(2 * time.Minute)
)

func seedRows() []audit.Row {
	return []audit.Row{
		{Timestamp: t0, Actor: "alice", Action: "note_get", ScopeLocal: "team", Outcome: "ok", RequestID: "r1", Side: "gateway"},
		{Timestamp: t1, Actor: "bob", Action: "note_create", ScopeLocal: "personal", Outcome: "error", RequestID: "r2", Side: "gateway"},
		{Timestamp: t2, Actor: "alice", Action: "note_delete", ScopeLocal: "team", Outcome: "ok", RequestID: "r3", Side: "gateway"},
	}
}

func TestFileBackend_Search_NoFilter(t *testing.T) {
	be := writeRows(t, tmpPath(t), seedRows())
	defer be.Close()

	rows, err := be.Search(context.Background(), audit.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	// descending order: newest first
	if rows[0].RequestID != "r3" {
		t.Errorf("want r3 first, got %s", rows[0].RequestID)
	}
}

func TestFileBackend_Search_FilterActor(t *testing.T) {
	be := writeRows(t, tmpPath(t), seedRows())
	defer be.Close()

	rows, err := be.Search(context.Background(), audit.SearchFilter{Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows for alice, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Actor != "alice" {
			t.Errorf("unexpected actor %s", r.Actor)
		}
	}
}

func TestFileBackend_Search_FilterOutcome(t *testing.T) {
	be := writeRows(t, tmpPath(t), seedRows())
	defer be.Close()

	rows, err := be.Search(context.Background(), audit.SearchFilter{Outcome: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RequestID != "r2" {
		t.Fatalf("want r2 only, got %v", rows)
	}
}

func TestFileBackend_Search_FilterScope(t *testing.T) {
	be := writeRows(t, tmpPath(t), seedRows())
	defer be.Close()

	rows, err := be.Search(context.Background(), audit.SearchFilter{Scope: "team"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 team rows, got %d", len(rows))
	}
}

func TestFileBackend_Search_TimeRange(t *testing.T) {
	be := writeRows(t, tmpPath(t), seedRows())
	defer be.Close()

	rows, err := be.Search(context.Background(), audit.SearchFilter{Since: t1, Until: t1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RequestID != "r2" {
		t.Fatalf("want r2 only for t1 range, got %v", rows)
	}
}

func TestFileBackend_Search_Pagination(t *testing.T) {
	be := writeRows(t, tmpPath(t), seedRows())
	defer be.Close()

	first, err := be.Search(context.Background(), audit.SearchFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("want 2 rows (page 1), got %d", len(first))
	}
	second, err := be.Search(context.Background(), audit.SearchFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("want 1 row (page 2), got %d", len(second))
	}
	// no overlap
	if first[0].RequestID == second[0].RequestID {
		t.Error("pagination overlap")
	}
}

func TestFileBackend_Search_OffsetBeyondEnd(t *testing.T) {
	be := writeRows(t, tmpPath(t), seedRows())
	defer be.Close()

	rows, err := be.Search(context.Background(), audit.SearchFilter{Offset: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("want empty result, got %d rows", len(rows))
	}
}
