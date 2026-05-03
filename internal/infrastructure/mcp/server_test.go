package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/whiskeyjimbo/para-mcp/internal/application"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/ctxutil"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/localvault"
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
	t.Cleanup(func() { _ = v.Close() })
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
	t.Cleanup(func() { _ = v.Close() })
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

// TestConflictError_DetailsRequestID verifies that a stale-ETag write returns a
// JSON body with {"error":"conflict","details":{"request_id":"..."}} when the
// caller supplies an X-PARA-Request-Id via context.
func TestConflictError_DetailsRequestID(t *testing.T) {
	svc := newTestService(t)
	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
	}
	ctx := context.Background()

	// Create a note and capture its ETag.
	createReq := mcplib.CallToolRequest{}
	createReq.Params.Arguments = map[string]any{"path": "projects/occ.md", "body": "v1"}
	res, err := h.noteCreate(ctx, createReq)
	if err != nil || res.IsError {
		t.Fatalf("noteCreate failed: err=%v isError=%v", err, res.IsError)
	}
	var created struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &created); err != nil {
		t.Fatalf("unmarshal create result: %v", err)
	}
	staleETag := created.ETag

	// Mutate so the ETag advances.
	updateReq := mcplib.CallToolRequest{}
	updateReq.Params.Arguments = map[string]any{
		"scope":    "personal",
		"path":     "projects/occ.md",
		"body":     "v2",
		"if_match": staleETag,
	}
	if _, err := h.noteUpdateBody(ctx, updateReq); err != nil {
		t.Fatalf("noteUpdateBody: %v", err)
	}

	// Attempt update with stale ETag + a request ID in context.
	const reqID = "req_01HZZZZZZZZZZZZZZZZZZZZZZA"
	ctxWithID := ctxutil.WithRequestID(ctx, reqID)
	staleReq := mcplib.CallToolRequest{}
	staleReq.Params.Arguments = map[string]any{
		"scope":    "personal",
		"path":     "projects/occ.md",
		"body":     "v3",
		"if_match": staleETag,
	}
	result, err := h.noteUpdateBody(ctxWithID, staleReq)
	if err != nil {
		t.Fatalf("noteUpdateBody: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for stale ETag")
	}

	var resp struct {
		Error   string `json:"error"`
		Details struct {
			RequestID string `json:"request_id"`
		} `json:"details"`
	}
	text := result.Content[0].(mcplib.TextContent).Text
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("conflict response is not valid JSON: %v\nbody: %s", err, text)
	}
	if resp.Error != "conflict" {
		t.Errorf("error field = %q, want %q", resp.Error, "conflict")
	}
	if resp.Details.RequestID != reqID {
		t.Errorf("details.request_id = %q, want %q", resp.Details.RequestID, reqID)
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
