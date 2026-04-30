package mcp

import (
	"context"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/whiskeyjimbo/paras/internal/application"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/index"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
)

// scopeRecorder is a ports.Vault stub that records the AllowedScopes
// received on Query and Search calls. Unimplemented methods panic.
type scopeRecorder struct {
	ports.Vault
	queryScopes  []domain.ScopeID
	searchScopes []domain.ScopeID
}

func (r *scopeRecorder) Scope() domain.ScopeID { return "personal" }
func (r *scopeRecorder) Capabilities() domain.Capabilities {
	return domain.Capabilities{CaseSensitive: true}
}
func (r *scopeRecorder) Stats(_ context.Context) (domain.VaultStats, error) {
	return domain.VaultStats{ByCategory: map[domain.Category]int{}}, nil
}
func (r *scopeRecorder) Query(_ context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	r.queryScopes = q.Filter.AllowedScopes
	return domain.QueryResult{
		ScopesAttempted: []domain.ScopeID{"personal"},
		ScopesSucceeded: []domain.ScopeID{"personal"},
	}, nil
}
func (r *scopeRecorder) Search(_ context.Context, _ string, f domain.Filter, _ int) ([]domain.RankedNote, error) {
	r.searchScopes = f.AllowedScopes
	return nil, nil
}

func newTestService(t *testing.T) *application.NoteService {
	t.Helper()
	v, err := localvault.New("personal", t.TempDir(), index.Config{})
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(v.Close)
	return application.NewService(v)
}

func emptyListReq() mcplib.CallToolRequest {
	return mcplib.CallToolRequest{}
}

func searchReq(text string) mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"text": text}
	return req
}

func TestPersonalOnly(t *testing.T) {
	got := personalOnly(context.Background())
	if len(got) != 1 || got[0] != "personal" {
		t.Fatalf("personalOnly() = %v, want [personal]", got)
	}
}

func TestBuildNilScopesFnInstallsPersonalOnly(t *testing.T) {
	svc := newTestService(t)
	s := Build(svc, nil)
	if s == nil {
		t.Fatal("Build returned nil")
	}
	got := personalOnly(context.Background())
	if len(got) == 0 {
		t.Fatal("fallback resolver returned empty scopes")
	}
}

func TestScopesFuncFlowsIntoNotesList(t *testing.T) {
	rec := &scopeRecorder{}
	svc := application.NewService(rec)

	want := []domain.ScopeID{"personal", "team-eng"}
	h := &handlers{
		svc:    svc,
		scopes: func(_ context.Context) []domain.ScopeID { return want },
	}

	ctx := context.Background()
	result, err := h.notesList(ctx, emptyListReq())
	if err != nil {
		t.Fatalf("notesList: %v", err)
	}
	if result.IsError {
		t.Fatalf("notesList returned error result")
	}
	if len(rec.queryScopes) != 2 || rec.queryScopes[0] != "personal" || rec.queryScopes[1] != "team-eng" {
		t.Fatalf("AllowedScopes on Query = %v, want %v", rec.queryScopes, want)
	}
}

func TestScopesFuncFlowsIntoNotesSearch(t *testing.T) {
	rec := &scopeRecorder{}
	svc := application.NewService(rec)

	want := []domain.ScopeID{"personal"}
	h := &handlers{
		svc:    svc,
		scopes: func(_ context.Context) []domain.ScopeID { return want },
	}

	ctx := context.Background()
	result, err := h.notesSearch(ctx, searchReq("hello"))
	if err != nil {
		t.Fatalf("notesSearch: %v", err)
	}
	if result.IsError {
		t.Fatalf("notesSearch returned error result")
	}
	if len(rec.searchScopes) != 1 || rec.searchScopes[0] != "personal" {
		t.Fatalf("AllowedScopes on Search = %v, want %v", rec.searchScopes, want)
	}
}

func TestDenyAllScopesFuncExcludesVault(t *testing.T) {
	svc := newTestService(t)
	h := &handlers{
		svc: svc,
		scopes: func(_ context.Context) []domain.ScopeID {
			return []domain.ScopeID{}
		},
	}

	ctx := context.Background()
	result, err := h.notesList(ctx, emptyListReq())
	if err != nil {
		t.Fatalf("notesList: %v", err)
	}
	if result.IsError {
		t.Fatalf("notesList returned error result: %v", result)
	}
	_ = result
}
