package localvault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/application"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

func newTestVault(t *testing.T) *LocalVault {
	t.Helper()
	v, err := New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func TestLocalVault_CreateAndGet(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	res, err := v.Create(ctx, domain.CreateInput{
		Path:        "projects/hello.md",
		FrontMatter: domain.FrontMatter{Title: "Hello"},
		Body:        "body content",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Summary.Title != "Hello" {
		t.Errorf("Title = %q, want %q", res.Summary.Title, "Hello")
	}
	if res.ETag == "" {
		t.Error("ETag should be set on Create result")
	}

	note, err := v.Get(ctx, "projects/hello.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if note.Body != "body content" {
		t.Errorf("Body = %q", note.Body)
	}
	if note.ETag != res.ETag {
		t.Errorf("ETag mismatch: %q vs %q", note.ETag, res.ETag)
	}
}

func TestLocalVault_UpdateBody_ETag(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	res1, _ := v.Create(ctx, domain.CreateInput{Path: "projects/x.md", Body: "v1"})

	res2, err := v.UpdateBody(ctx, "projects/x.md", "v2", res1.ETag)
	if err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	if res2.ETag == res1.ETag {
		t.Error("ETag should change after update")
	}

	_, err = v.UpdateBody(ctx, "projects/x.md", "v3", res1.ETag)
	if err == nil {
		t.Fatal("expected conflict error on stale ETag")
	}
}

func TestLocalVault_PatchFrontMatter(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	res1, _ := v.Create(ctx, domain.CreateInput{
		Path:        "areas/work.md",
		FrontMatter: domain.FrontMatter{Title: "Work", Status: "active"},
	})

	res2, err := v.PatchFrontMatter(ctx, "areas/work.md", map[string]any{
		"status": "done",
		"title":  "Work Done",
	}, res1.ETag)
	if err != nil {
		t.Fatalf("PatchFrontMatter: %v", err)
	}
	if res2.Summary.Status != "done" {
		t.Errorf("Status = %q, want %q", res2.Summary.Status, "done")
	}
	if res2.Summary.Title != "Work Done" {
		t.Errorf("Title = %q", res2.Summary.Title)
	}
}

func TestLocalVault_Move(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	res, _ := v.Create(ctx, domain.CreateInput{Path: "projects/old.md", Body: "content"})
	_, err := v.Move(ctx, "projects/old.md", "archives/old.md", res.ETag)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}

	if _, err := v.Get(ctx, "projects/old.md"); err == nil {
		t.Error("old path should not exist after move")
	}
	if _, err := v.Get(ctx, "archives/old.md"); err != nil {
		t.Errorf("new path should exist: %v", err)
	}
}

func TestLocalVault_Delete_Soft(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	v.Create(ctx, domain.CreateInput{Path: "projects/doomed.md", Body: "bye"})
	if err := v.Delete(ctx, "projects/doomed.md", true, ""); err != nil {
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
	if err := v.Delete(ctx, "projects/gone.md", false, ""); err != nil {
		t.Fatalf("Delete(hard): %v", err)
	}
	if _, err := v.Get(ctx, "projects/gone.md"); err == nil {
		t.Error("hard-deleted note should not be accessible via Get")
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

	result, err := v.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryFilter(domain.NewFilter(domain.WithStatus("active"))),
	))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Notes) != 1 || result.Notes[0].Ref.Path != "projects/a.md" {
		t.Errorf("expected 1 active note, got %d: %v", len(result.Notes), result.Notes)
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

	time.Sleep(50 * time.Millisecond)

	results, err := v.Search(ctx, "vpc", domain.Filter{}, 10)
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

	res, err := v.Create(ctx, domain.NewCreateInput(
		"projects/tagged.md",
		domain.NewFrontMatter("", "", "", "", []string{"AWS", "#Cloud", " infra "}),
		"",
	))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, tag := range res.Summary.Tags {
		if tag != "aws" && tag != "cloud" && tag != "infra" {
			t.Errorf("tag not normalized: %q", tag)
		}
	}
}

func TestNoteService_RejectsInvalidPath(t *testing.T) {
	v := newTestVault(t)
	svc := application.NewService(v)
	ctx := context.Background()

	_, err := svc.Get(ctx, domain.NoteRef{Scope: "personal", Path: "../etc/passwd"})
	if err == nil {
		t.Fatal("expected error for path traversal via NoteService")
	}
}

func TestNoteService_RejectsNonPARARoot(t *testing.T) {
	v := newTestVault(t)
	svc := application.NewService(v)
	ctx := context.Background()

	_, err := svc.Get(ctx, domain.NoteRef{Scope: "personal", Path: "notes/foo.md"})
	if err == nil {
		t.Fatal("expected error for non-PARA root via NoteService")
	}
}

func TestCheckSymlinks_OutsideVault(t *testing.T) {
	vault := t.TempDir()
	outsideTarget := t.TempDir()

	projDir := filepath.Join(vault, "projects")
	if err := os.Mkdir(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evilFile := filepath.Join(outsideTarget, "evil.md")
	if err := os.WriteFile(evilFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(projDir, "evil.md")
	if err := os.Symlink(evilFile, linkPath); err != nil {
		t.Fatal(err)
	}

	if err := checkSymlinks(vault, "projects/evil.md"); err == nil {
		t.Fatal("expected error for symlink pointing outside vault")
	}
}

func TestCheckSymlinks_InsideVault(t *testing.T) {
	vault := t.TempDir()
	projDir := filepath.Join(vault, "projects")
	if err := os.Mkdir(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(projDir, "real.md")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(projDir, "alias.md")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}

	if err := checkSymlinks(vault, "projects/alias.md"); err != nil {
		t.Fatalf("expected no error for in-vault symlink: %v", err)
	}
}

func TestCheckSymlinks_NonExistentPath(t *testing.T) {
	vault := t.TempDir()
	if err := checkSymlinks(vault, "projects/new-note.md"); err != nil {
		t.Fatalf("non-existent path should not error: %v", err)
	}
}
