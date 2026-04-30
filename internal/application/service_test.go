package application

import (
	"context"
	"testing"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/index"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
)

func newTestService(t *testing.T) *NoteService {
	t.Helper()
	v, err := localvault.New("personal", t.TempDir(), index.Config{})
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(v.Close)
	return NewService(v)
}

func TestNoteService_Query_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Query(context.Background(), domain.QueryRequest{Filter: domain.Filter{AllowedScopes: nil}})
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestNoteService_Query_AllowedScopesEmpty(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	svc.Create(ctx, domain.CreateInput{Path: "projects/x.md", Body: "x"})
	result, err := svc.Query(ctx, domain.QueryRequest{Filter: domain.Filter{AllowedScopes: []domain.ScopeID{}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("empty AllowedScopes should deny all results, got %d", len(result.Notes))
	}
}

func TestNoteService_Search_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Search(context.Background(), "foo", domain.Filter{AllowedScopes: nil}, 10)
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}

func TestNoteService_Backlinks_AllowedScopesNil(t *testing.T) {
	svc := newTestService(t)
	ref := domain.NoteRef{Scope: "personal", Path: "projects/foo.md"}
	_, err := svc.Backlinks(context.Background(), ref, false, domain.Filter{AllowedScopes: nil})
	if err == nil {
		t.Fatal("nil AllowedScopes should return internal error")
	}
}
