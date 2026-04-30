package vault

import (
	"context"
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/domain"
	"github.com/whiskeyjimbo/paras/internal/index"
)

func newTestVault(t *testing.T) *LocalVault {
	t.Helper()
	v, err := New("personal", t.TempDir(), index.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(v.Close)
	return v
}

var allowedPersonal = []domain.ScopeID{"personal"}

func TestLocalVault_CreateAndGet(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	summary, err := v.Create(ctx, domain.CreateInput{
		Path:        "projects/hello.md",
		FrontMatter: domain.FrontMatter{Title: "Hello"},
		Body:        "body content",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if summary.Title != "Hello" {
		t.Errorf("Title = %q, want %q", summary.Title, "Hello")
	}
	if summary.ETag == "" {
		t.Error("ETag should be set on Create summary")
	}

	note, err := v.Get(ctx, "projects/hello.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if note.Body != "body content" {
		t.Errorf("Body = %q", note.Body)
	}
	if note.ETag != summary.ETag {
		t.Errorf("ETag mismatch: %q vs %q", note.ETag, summary.ETag)
	}
}

func TestLocalVault_UpdateBody_ETag(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	sum, _ := v.Create(ctx, domain.CreateInput{Path: "projects/x.md", Body: "v1"})

	// Correct ETag — should succeed.
	sum2, err := v.UpdateBody(ctx, "projects/x.md", "v2", sum.ETag)
	if err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	if sum2.ETag == sum.ETag {
		t.Error("ETag should change after update")
	}

	// Stale ETag — should conflict.
	_, err = v.UpdateBody(ctx, "projects/x.md", "v3", sum.ETag)
	if err == nil {
		t.Fatal("expected conflict error on stale ETag")
	}
}

func TestLocalVault_PatchFrontMatter(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	sum, _ := v.Create(ctx, domain.CreateInput{
		Path:        "areas/work.md",
		FrontMatter: domain.FrontMatter{Title: "Work", Status: "active"},
	})

	sum2, err := v.PatchFrontMatter(ctx, "areas/work.md", map[string]any{
		"status": "done",
		"title":  "Work Done",
	}, sum.ETag)
	if err != nil {
		t.Fatalf("PatchFrontMatter: %v", err)
	}
	if sum2.Status != "done" {
		t.Errorf("Status = %q, want %q", sum2.Status, "done")
	}
	if sum2.Title != "Work Done" {
		t.Errorf("Title = %q", sum2.Title)
	}
}

func TestLocalVault_Move(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	sum, _ := v.Create(ctx, domain.CreateInput{Path: "projects/old.md", Body: "content"})
	_, err := v.Move(ctx, "projects/old.md", "archives/old.md", sum.ETag)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}

	// Old path should not exist.
	if _, err := v.Get(ctx, "projects/old.md"); err == nil {
		t.Error("old path should not exist after move")
	}
	// New path should exist.
	if _, err := v.Get(ctx, "archives/old.md"); err != nil {
		t.Errorf("new path should exist: %v", err)
	}
}

func TestLocalVault_Delete_Soft(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	v.Create(ctx, domain.CreateInput{Path: "projects/doomed.md", Body: "bye"})
	if err := v.Delete(ctx, "projects/doomed.md", true); err != nil {
		t.Fatalf("Delete(soft): %v", err)
	}
	if _, err := v.Get(ctx, "projects/doomed.md"); err == nil {
		t.Error("soft-deleted note should not be accessible via Get")
	}
}

func TestLocalVault_Delete_Hard(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	v.Create(ctx, domain.CreateInput{Path: "projects/gone.md", Body: "bye"})
	if err := v.Delete(ctx, "projects/gone.md", false); err != nil {
		t.Fatalf("Delete(hard): %v", err)
	}
	if _, err := v.Get(ctx, "projects/gone.md"); err == nil {
		t.Error("hard-deleted note should not be accessible via Get")
	}
}

func TestLocalVault_Query_AllowedScopesNil(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()
	_, err := v.Query(ctx, domain.QueryRequest{Filter: domain.Filter{AllowedScopes: nil}})
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestLocalVault_Query_AllowedScopesEmpty(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()
	v.Create(ctx, domain.CreateInput{Path: "projects/x.md", Body: "x"})
	result, err := v.Query(ctx, domain.QueryRequest{Filter: domain.Filter{AllowedScopes: []domain.ScopeID{}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Notes) != 0 {
		t.Fatal("empty AllowedScopes should deny all results")
	}
}

func TestLocalVault_Query_Filter(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	v.Create(ctx, domain.CreateInput{
		Path:        "projects/a.md",
		FrontMatter: domain.FrontMatter{Status: "active", Tags: []string{"aws"}},
	})
	v.Create(ctx, domain.CreateInput{
		Path:        "areas/b.md",
		FrontMatter: domain.FrontMatter{Status: "inactive"},
	})

	result, err := v.Query(ctx, domain.QueryRequest{
		Filter: domain.Filter{
			AllowedScopes: allowedPersonal,
			Status:        "active",
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Notes) != 1 || result.Notes[0].Ref.Path != "projects/a.md" {
		t.Errorf("expected 1 active note, got %d: %v", len(result.Notes), result.Notes)
	}
}

func TestLocalVault_Search_AllowedScopesNil(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()
	_, err := v.Search(ctx, "foo", domain.Filter{AllowedScopes: nil}, 10)
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestLocalVault_Search(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	v.Create(ctx, domain.CreateInput{
		Path:        "resources/aws.md",
		FrontMatter: domain.FrontMatter{Title: "AWS Guide"},
		Body:        "This note covers vpc configuration",
	})

	// Give index writer time to process.
	time.Sleep(50 * time.Millisecond)

	results, err := v.Search(ctx, "vpc", domain.Filter{AllowedScopes: allowedPersonal}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}
}

func TestLocalVault_Stats(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	v.Create(ctx, domain.CreateInput{Path: "projects/a.md"})
	v.Create(ctx, domain.CreateInput{Path: "projects/b.md"})
	v.Create(ctx, domain.CreateInput{Path: "areas/c.md"})

	stats, err := v.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalNotes != 3 {
		t.Errorf("TotalNotes = %d, want 3", stats.TotalNotes)
	}
	if stats.ByCategory[domain.Projects] != 2 {
		t.Errorf("Projects = %d, want 2", stats.ByCategory[domain.Projects])
	}
}

func TestLocalVault_TagNormalization(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	sum, err := v.Create(ctx, domain.CreateInput{
		Path:        "projects/tagged.md",
		FrontMatter: domain.FrontMatter{Tags: []string{"AWS", "#Cloud", " infra "}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, tag := range sum.Tags {
		if tag != "aws" && tag != "cloud" && tag != "infra" {
			t.Errorf("tag not normalized: %q", tag)
		}
	}
}

func TestNoteService_RejectsInvalidPath(t *testing.T) {
	v := newTestVault(t)
	svc := NewService(v)
	ctx := context.Background()

	_, err := svc.Get(ctx, domain.NoteRef{Scope: "personal", Path: "../etc/passwd"})
	if err == nil {
		t.Fatal("expected error for path traversal via NoteService")
	}
}

func TestNoteService_RejectsNonPARARoot(t *testing.T) {
	v := newTestVault(t)
	svc := NewService(v)
	ctx := context.Background()

	_, err := svc.Get(ctx, domain.NoteRef{Scope: "personal", Path: "notes/foo.md"})
	if err == nil {
		t.Fatal("expected error for non-PARA root via NoteService")
	}
}
