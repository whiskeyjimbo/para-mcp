package derived_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infra/semantic/derived"
)

func makeMeta(schemaVer int, editedByUser bool) *domain.DerivedMetadata {
	return &domain.DerivedMetadata{
		Summary:       "test summary",
		Purpose:       "reference",
		SummaryRatio:  0.5,
		SchemaVersion: schemaVer,
		EditedByUser:  editedByUser,
		GeneratedAt:   time.Now().UTC(),
		SummaryModel:  "test-model",
	}
}

// --- Frontmatter store tests (no DB required) ---

func TestFrontmatterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	notePath := filepath.Join(dir, "projects", "foo.md")
	os.MkdirAll(filepath.Dir(notePath), 0o755)
	os.WriteFile(notePath, []byte("---\ntitle: Foo\n---\n\nBody text.\n"), 0o644)

	store := derived.NewFrontmatterStore(dir)
	ref := domain.NoteRef{Scope: "local", Path: "projects/foo.md"}
	meta := makeMeta(1, false)

	if err := store.Set(context.Background(), "note-id-1", ref, meta); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, err := store.Get(context.Background(), "note-id-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Summary != meta.Summary {
		t.Errorf("summary mismatch: got %q, want %q", got.Summary, meta.Summary)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("schema version mismatch: %d", got.SchemaVersion)
	}

	// Verify markdown file still contains original body (sidecar did not overwrite).
	content, _ := os.ReadFile(notePath)
	if !containsStr(string(content), "Body text.") {
		t.Errorf("frontmatter store overwrote note body: %s", content)
	}
}

func TestFrontmatterEditedByUserBlocks(t *testing.T) {
	dir := t.TempDir()
	notePath := filepath.Join(dir, "projects", "foo.md")
	os.MkdirAll(filepath.Dir(notePath), 0o755)
	os.WriteFile(notePath, []byte("---\ntitle: Foo\n---\n\nBody.\n"), 0o644)

	store := derived.NewFrontmatterStore(dir)
	ref := domain.NoteRef{Scope: "local", Path: "projects/foo.md"}

	// Write with EditedByUser=true
	if err := store.Set(context.Background(), "id1", ref, makeMeta(1, true)); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	edited, err := store.IsEditedByUser(context.Background(), "id1")
	if err != nil {
		t.Fatalf("IsEditedByUser failed: %v", err)
	}
	if !edited {
		t.Error("expected EditedByUser=true")
	}
}

func TestSchemaVersionOlderQueuesRederive(t *testing.T) {
	const currentSchema = 2
	old := makeMeta(1, false)
	// SchemaVersion < current → ShouldRederive returns true
	if !derived.ShouldRederive(old, currentSchema) {
		t.Error("expected ShouldRederive=true for older schema version")
	}
}

func TestSchemaVersionNewerPreserved(t *testing.T) {
	const currentSchema = 1
	newer := makeMeta(99, false) // future schema
	// SchemaVersion > current → forward-compat, do not re-derive
	if derived.ShouldRederive(newer, currentSchema) {
		t.Error("expected ShouldRederive=false for newer schema version")
	}
}

func TestConditionalWriteProbeFailureFallsBackToSidecar(t *testing.T) {
	// A failing probe must not return an error — caller falls back to sidecar.
	probe := derived.StaticProber(false) // always fails
	mode := derived.SelectMode(probe)
	if mode != derived.ModeSidecar {
		t.Errorf("expected sidecar fallback when probe fails, got %v", mode)
	}
}

func TestConditionalWriteProbeSuccessUsesFrontmatter(t *testing.T) {
	probe := derived.StaticProber(true)
	mode := derived.SelectMode(probe)
	if mode != derived.ModeFrontmatter {
		t.Errorf("expected frontmatter when probe succeeds, got %v", mode)
	}
}

// --- Sidecar (postgres) tests ---

func TestSidecarRoundTrip(t *testing.T) {
	dsn := os.Getenv("PARAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PARAS_TEST_POSTGRES_DSN not set; skipping sidecar postgres test")
	}

	store, err := derived.NewSidecarStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("NewSidecarStore: %v", err)
	}
	defer store.Close()

	ref := domain.NoteRef{Scope: "test-scope", Path: "projects/foo.md"}
	meta := makeMeta(1, false)

	if err := store.Set(context.Background(), "sidecar-note-1", ref, meta); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(context.Background(), "sidecar-note-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Summary != meta.Summary {
		t.Errorf("summary mismatch: got %q", got.Summary)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
