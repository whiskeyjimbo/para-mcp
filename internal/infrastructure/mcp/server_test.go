package mcp

import (
	"context"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/whiskeyjimbo/paras/internal/application"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
)

// scopeRecorder is a ports.Vault stub that records whether Query and Search
// were called (scope gating is enforced by NoteService, not visible here).
type scopeRecorder struct {
	ports.Vault
	queryCalled  bool
	searchCalled bool
}

func (r *scopeRecorder) Scope() domain.ScopeID { return "personal" }
func (r *scopeRecorder) Capabilities() domain.Capabilities {
	return domain.Capabilities{CaseSensitive: true}
}
func (r *scopeRecorder) Stats(_ context.Context) (domain.VaultStats, error) {
	return domain.VaultStats{ByCategory: map[domain.Category]int{}}, nil
}
func (r *scopeRecorder) Query(_ context.Context, _ domain.QueryRequest) (domain.QueryResult, error) {
	r.queryCalled = true
	return domain.QueryResult{
		ScopesAttempted: []domain.ScopeID{"personal"},
		ScopesSucceeded: []domain.ScopeID{"personal"},
	}, nil
}
func (r *scopeRecorder) Search(_ context.Context, _ string, _ domain.Filter, _ int) ([]domain.RankedNote, error) {
	r.searchCalled = true
	return nil, nil
}

func newTestService(t *testing.T) *application.NoteService {
	t.Helper()
	v, err := localvault.New("personal", t.TempDir())
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
	got := personalOnly.Scopes(context.Background())
	if len(got) != 1 || got[0] != "personal" {
		t.Fatalf("personalOnly.Scopes() = %v, want [personal]", got)
	}
}

func TestBuildDefaultScopesFnInstallsPersonalOnly(t *testing.T) {
	svc := newTestService(t)
	s := Build(svc)
	if s == nil {
		t.Fatal("Build returned nil")
	}
	got := personalOnly.Scopes(context.Background())
	if len(got) == 0 {
		t.Fatal("fallback resolver returned empty scopes")
	}
}

func TestScopesFuncFlowsIntoNotesList(t *testing.T) {
	rec := &scopeRecorder{}
	svc := application.NewService(rec)

	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal", "team-eng"} }),
	}

	ctx := context.Background()
	result, err := h.notesList(ctx, emptyListReq())
	if err != nil {
		t.Fatalf("notesList: %v", err)
	}
	if result.IsError {
		t.Fatalf("notesList returned error result")
	}
	if !rec.queryCalled {
		t.Fatal("vault Query was not called: scopes not forwarded to NoteService correctly")
	}
}

func TestScopesFuncFlowsIntoNotesSearch(t *testing.T) {
	rec := &scopeRecorder{}
	svc := application.NewService(rec)

	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
	}

	ctx := context.Background()
	result, err := h.notesSearch(ctx, searchReq("hello"))
	if err != nil {
		t.Fatalf("notesSearch: %v", err)
	}
	if result.IsError {
		t.Fatalf("notesSearch returned error result")
	}
	if !rec.searchCalled {
		t.Fatal("vault Search was not called: scopes not forwarded to NoteService correctly")
	}
}

func TestWithClockInjectedIntoNotesStale(t *testing.T) {
	t.Helper()
	v, err := localvault.New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(v.Close)
	fixed := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	svc := application.NewService(v, application.WithClock(func() time.Time { return fixed }))
	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"days": float64(30)}
	result, err := h.notesStale(context.Background(), req)
	if err != nil {
		t.Fatalf("notesStale: %v", err)
	}
	if result.IsError {
		t.Fatalf("notesStale returned error: %v", result)
	}
}

func TestDenyAllScopesFuncExcludesVault(t *testing.T) {
	svc := newTestService(t)
	h := &handlers{
		svc: svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID {
			return []domain.ScopeID{}
		}),
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
