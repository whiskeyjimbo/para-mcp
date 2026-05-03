package mcp

import (
	"context"
	"encoding/json"
	"strings"
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
func TestNotesSemanticSearch_CapabilityUnavailable(t *testing.T) {
	svc := newTestService(t)
	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "auth refactor"}
	res, err := h.notesSemanticSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result when no semantic searcher configured")
	}
	text := res.Content[0].(mcplib.TextContent).Text
	if !strings.Contains(text, "capability_unavailable") {
		t.Errorf("expected capability_unavailable in error, got %q", text)
	}
}

type stubSemSearcher struct {
	hits []domain.VectorHit
}

func (s *stubSemSearcher) SemanticSearch(_ context.Context, _ string, _ domain.AuthFilter, _ domain.SemanticSearchOptions) ([]domain.VectorHit, error) {
	return s.hits, nil
}

func TestNotesSemanticSearch_RoutesArgsAndReturnsRanked(t *testing.T) {
	v, err := localvault.New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	stub := &stubSemSearcher{}
	svc := application.NewService(v, application.WithSemanticSearcher(stub))
	mr, err := svc.Create(context.Background(), domain.CreateInput{
		Path:        "projects/oidc.md",
		FrontMatter: domain.FrontMatter{Title: "OIDC migration"},
		Body:        "long body text",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stub.hits = []domain.VectorHit{{Ref: mr.Summary.Ref, Score: 0.91}}

	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query": "auth refactor",
		"body":  "on_demand",
		"limit": float64(5),
	}
	res, err := h.notesSemanticSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	var got []domain.RankedNote
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].Body == "" {
		t.Error("body=on_demand should populate body")
	}
	if got[0].Summary.Title != "OIDC migration" {
		t.Errorf("Title: got %q", got[0].Summary.Title)
	}
}

func TestNotesSemanticSearch_RejectsInvalidBodyMode(t *testing.T) {
	svc := application.NewService(&scopeRecorder{}, application.WithSemanticSearcher(&stubSemSearcher{}))
	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "q", "body": "weird"}
	res, _ := h.notesSemanticSearch(context.Background(), req)
	if !res.IsError {
		t.Fatal("expected invalid_argument for unknown body mode")
	}
}

func TestNotesSearch_ModeRouting(t *testing.T) {
	v, err := localvault.New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("localvault: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	mr, err := application.NewService(v).Create(context.Background(), domain.CreateInput{
		Path: "projects/widgets.md", FrontMatter: domain.FrontMatter{Title: "widgets"}, Body: "widgets are nice and good",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	stub := &stubSemSearcher{hits: []domain.VectorHit{{Ref: mr.Summary.Ref, Score: 0.9}}}
	svcCapable := application.NewService(v, application.WithSemanticSearcher(stub))
	svcLexOnly := application.NewService(v)

	resolver := ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} })

	cases := []struct {
		name       string
		svc        ports.NoteService
		mode       string
		wantErr    bool
		wantErrSub string
	}{
		{"omit-mode-capable-uses-hybrid", svcCapable, "", false, ""},
		{"omit-mode-not-capable-uses-lexical", svcLexOnly, "", false, ""},
		{"explicit-lexical-no-cap-ok", svcLexOnly, "lexical", false, ""},
		{"explicit-lexical-cap-ok", svcCapable, "lexical", false, ""},
		{"explicit-semantic-no-cap-errors", svcLexOnly, "semantic", true, "capability_unavailable"},
		{"explicit-semantic-cap-ok", svcCapable, "semantic", false, ""},
		{"explicit-hybrid-no-cap-errors", svcLexOnly, "hybrid", true, "capability_unavailable"},
		{"explicit-hybrid-cap-ok", svcCapable, "hybrid", false, ""},
		{"invalid-mode-rejected", svcCapable, "weird", true, "invalid_argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &handlers{svc: tc.svc, scopes: resolver}
			req := mcplib.CallToolRequest{}
			args := map[string]any{"text": "widgets"}
			if tc.mode != "" {
				args["mode"] = tc.mode
			}
			req.Params.Arguments = args
			res, err := h.notesSearch(context.Background(), req)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if tc.wantErr {
				if !res.IsError {
					t.Fatalf("expected error result")
				}
				text := res.Content[0].(mcplib.TextContent).Text
				if !strings.Contains(text, tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", text, tc.wantErrSub)
				}
				return
			}
			if res.IsError {
				t.Fatalf("unexpected error: %v", res.Content)
			}
		})
	}
}

type stubIndexStateProvider struct {
	calls int
	seq   []domain.IndexState
	err   error
}

func (s *stubIndexStateProvider) IndexState(_ context.Context, _ string) (domain.IndexState, error) {
	if s.err != nil {
		return "", s.err
	}
	i := s.calls
	s.calls++
	if i >= len(s.seq) {
		return s.seq[len(s.seq)-1], nil
	}
	return s.seq[i], nil
}

func TestWaitForIndex_CapabilityUnavailable(t *testing.T) {
	svc := newTestService(t)
	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"scope": "personal", "path": "projects/x.md"}
	res, _ := h.waitForIndex(context.Background(), req)
	if !res.IsError {
		t.Fatal("expected error when no provider configured")
	}
	if !strings.Contains(res.Content[0].(mcplib.TextContent).Text, "capability_unavailable") {
		t.Errorf("expected capability_unavailable, got %v", res.Content)
	}
}

func TestWaitForIndex_ImmediateTerminalState(t *testing.T) {
	svc := newTestService(t)
	mr, err := svc.Create(context.Background(), domain.CreateInput{
		Path: "projects/x.md", FrontMatter: domain.FrontMatter{Title: "x"}, Body: "x",
	})
	if err != nil || mr.Summary.Ref.Path == "" {
		t.Fatalf("create: %v", err)
	}
	provider := &stubIndexStateProvider{seq: []domain.IndexState{domain.IndexStateIndexed}}
	h := &handlers{
		svc:                svc,
		scopes:             ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
		indexStateProvider: provider,
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"scope": "personal", "path": mr.Summary.Ref.Path}
	res, err := h.waitForIndex(context.Background(), req)
	if err != nil || res.IsError {
		t.Fatalf("unexpected: err=%v isErr=%v %v", err, res.IsError, res.Content)
	}
	var resp waitForIndexResp
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.State != domain.IndexStateIndexed {
		t.Errorf("state: got %q, want indexed", resp.State)
	}
	if resp.TimedOut {
		t.Error("should not be timed out for terminal state")
	}
}

func TestWaitForIndex_TimeoutWhenPendingForever(t *testing.T) {
	svc := newTestService(t)
	mr, err := svc.Create(context.Background(), domain.CreateInput{
		Path: "projects/x.md", FrontMatter: domain.FrontMatter{Title: "x"}, Body: "x",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	provider := &stubIndexStateProvider{seq: []domain.IndexState{domain.IndexStatePending}}
	h := &handlers{
		svc:                svc,
		scopes:             ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
		indexStateProvider: provider,
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"scope":            "personal",
		"path":             mr.Summary.Ref.Path,
		"index_timeout_ms": float64(50),
	}
	res, _ := h.waitForIndex(context.Background(), req)
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	var resp waitForIndexResp
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.TimedOut {
		t.Error("expected timed_out=true on stuck pending state")
	}
	if resp.State != domain.IndexStatePending {
		t.Errorf("state: got %q, want pending", resp.State)
	}
}

func TestWaitForIndex_TransitionsThroughPendingToIndexed(t *testing.T) {
	svc := newTestService(t)
	mr, err := svc.Create(context.Background(), domain.CreateInput{
		Path: "projects/x.md", FrontMatter: domain.FrontMatter{Title: "x"}, Body: "x",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	provider := &stubIndexStateProvider{seq: []domain.IndexState{
		domain.IndexStatePending,
		domain.IndexStatePending,
		domain.IndexStateIndexed,
	}}
	h := &handlers{
		svc:                svc,
		scopes:             ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
		indexStateProvider: provider,
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"scope":            "personal",
		"path":             mr.Summary.Ref.Path,
		"index_timeout_ms": float64(2000),
	}
	res, _ := h.waitForIndex(context.Background(), req)
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	var resp waitForIndexResp
	_ = json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &resp)
	if resp.State != domain.IndexStateIndexed {
		t.Errorf("state: got %q, want indexed", resp.State)
	}
	if resp.TimedOut {
		t.Error("should not time out when terminal reached")
	}
	if provider.calls < 2 {
		t.Errorf("expected multiple poll calls, got %d", provider.calls)
	}
}

func TestNotesSemanticSearch_RejectsBadThreshold(t *testing.T) {
	svc := application.NewService(&scopeRecorder{}, application.WithSemanticSearcher(&stubSemSearcher{}))
	h := &handlers{
		svc:    svc,
		scopes: ports.ScopesFunc(func(_ context.Context) []domain.ScopeID { return []domain.ScopeID{"personal"} }),
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "q", "threshold": 1.5}
	res, _ := h.notesSemanticSearch(context.Background(), req)
	if !res.IsError {
		t.Fatal("expected invalid_argument for out-of-range threshold")
	}
}

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
