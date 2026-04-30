package application

import (
	"context"
	"testing"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
)

func newTestService(t *testing.T) *NoteService {
	t.Helper()
	v, err := localvault.New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return NewService(v)
}

func TestNoteService_Query_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Query(context.Background(), domain.NewQueryRequest(domain.WithQueryAllowedScopes(nil)))
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestNoteService_Query_AllowedScopesEmpty(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	svc.Create(ctx, domain.CreateInput{Path: "projects/x.md", Body: "x"})
	result, err := svc.Query(ctx, domain.NewQueryRequest(domain.WithQueryAllowedScopes([]domain.ScopeID{})))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("empty AllowedScopes should deny all results, got %d", len(result.Notes))
	}
}

func TestNoteService_Search_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Search(context.Background(), "foo", domain.AuthFilter{AllowedScopes: nil}, 10)
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestNoteService_Backlinks_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	ref := domain.NoteRef{Scope: "personal", Path: "projects/foo.md"}
	_, err := svc.Backlinks(context.Background(), ref, false, domain.AuthFilter{AllowedScopes: nil})
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestNoteService_CreateBatch_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.CreateBatch(context.Background(), []domain.CreateInput{{Path: "projects/x.md"}}, domain.AuthFilter{AllowedScopes: nil})
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestNoteService_CreateBatch_DeniedScope(t *testing.T) {
	svc := newTestService(t)
	result, err := svc.CreateBatch(context.Background(), []domain.CreateInput{{Path: "projects/x.md"}}, domain.AuthFilter{AllowedScopes: []domain.ScopeID{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 0 {
		t.Fatalf("empty AllowedScopes should deny batch, got %d results", len(result.Results))
	}
}

func TestNoteService_UpdateBodyBatch_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.UpdateBodyBatch(context.Background(), []domain.BatchUpdateBodyInput{{Path: "projects/x.md"}}, domain.AuthFilter{AllowedScopes: nil})
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestNoteService_PatchFrontMatterBatch_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.PatchFrontMatterBatch(context.Background(), []domain.BatchPatchFrontMatterInput{{Path: "projects/x.md", Fields: map[string]any{"status": "done"}}}, domain.AuthFilter{AllowedScopes: nil})
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}
