package mcp

// Auth acceptance tests for FEAT-006.
// Scenario 5 (concurrent Postgres writes) requires a live Postgres instance;
// set POSTGRES_TEST_DSN to enable it. All other scenarios use localvault.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/para-mcp/internal/application"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/localvault"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/postgresv"
	"github.com/whiskeyjimbo/para-mcp/internal/server/audit"
	"github.com/whiskeyjimbo/para-mcp/internal/server/auth"
	"github.com/whiskeyjimbo/para-mcp/internal/server/rbac"
)

// --- Shared test harness ---

type testHarness struct {
	handlers *handlers
	reg      *rbac.Registry
}

func newHarness(t *testing.T, scope string, grants []rbac.ScopeGrant) *testHarness {
	t.Helper()
	v, err := localvault.New(scope, t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })

	svc := application.NewService(v)
	reg := rbac.New(rbac.WithRoleLoader(grants))

	return &testHarness{
		handlers: &handlers{
			svc:              svc,
			scopes:           personalOnly,
			rbacRegistry:     reg,
			exposeAdminTools: true,
		},
		reg: reg,
	}
}

func callerCtx(identity string) context.Context {
	return auth.WithCaller(context.Background(), auth.CallerIdentity(identity))
}

func createReq(path string) mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": path, "title": "test"}
	return req
}

func updateBodyReq(scope, path, body, ifMatch string) mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	args := map[string]any{"scope": scope, "path": path, "body": body}
	if ifMatch != "" {
		args["if_match"] = ifMatch
	}
	req.Params.Arguments = args
	return req
}

func promoteReq(scope, path, toScope string) mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"scope": scope, "path": path, "to_scope": toScope}
	return req
}

// --- Scenario 1: Unauthenticated request → 401 ---

func TestAuthAccept_Unauthenticated_401(t *testing.T) {
	// Wire a real MCP HTTP server with bearer middleware in front.
	v, err := localvault.New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })

	svc := application.NewService(v)
	tokenStore := auth.MapTokenStore{"valid-token": "alice"}
	bearerMW := auth.BearerMiddleware(auth.WithTokenStore(tokenStore))

	mcpSrv := Build(svc)
	streamable := mcpserver.NewStreamableHTTPServer(mcpSrv)
	wrapped := bearerMW(streamable)

	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)

	// No Authorization header.
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("http.Post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestAuthAccept_Unauthenticated_HandlerNotInvoked(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	tokenStore := auth.MapTokenStore{}
	h := auth.BearerMiddleware(auth.WithTokenStore(tokenStore))(inner)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("inner handler must not be invoked when token is missing")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

// --- Scenario 2: viewer cannot call note_create → permission_denied ---

func TestAuthAccept_ViewerCannotCreate(t *testing.T) {
	h := newHarness(t, "team-platform", []rbac.ScopeGrant{
		{Identity: "alice", Scope: "team-platform", Role: rbac.Viewer},
	})

	ctx := callerCtx("alice")
	res, err := h.handlers.noteCreate(ctx, createReq("projects/foo.md"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("want error result for viewer calling note_create")
	}
	if !strings.Contains(resultText(res), "permission_denied") {
		t.Errorf("want permission_denied in result, got: %s", resultText(res))
	}
}

func TestAuthAccept_ContributorCanCreate(t *testing.T) {
	h := newHarness(t, "team-platform", []rbac.ScopeGrant{
		{Identity: "bob", Scope: "team-platform", Role: rbac.Contributor},
	})

	ctx := callerCtx("bob")
	res, err := h.handlers.noteCreate(ctx, createReq("projects/bar.md"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("contributor should be allowed to create; got error: %s", resultText(res))
	}
}

// --- Scenario 3: note_promote requires Lead on destination scope ---

func TestAuthAccept_ContributorCannotPromote(t *testing.T) {
	// contributor on team-platform cannot promote to that scope
	reg := rbac.New(rbac.WithRoleLoader([]rbac.ScopeGrant{
		{Identity: "bob", Scope: "team-platform", Role: rbac.Contributor},
	}))
	v, err := localvault.New("team-platform", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	svc := application.NewService(v)
	h := &handlers{svc: svc, scopes: personalOnly, rbacRegistry: reg, exposeAdminTools: true}

	ctx := callerCtx("bob")
	res, err := h.notePromote(ctx, promoteReq("team-platform", "projects/foo.md", "team-platform"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("want error result for contributor calling note_promote")
	}
	if !strings.Contains(resultText(res), "permission_denied") {
		t.Errorf("want permission_denied in result, got: %s", resultText(res))
	}
}

func TestAuthAccept_LeadCanPromote(t *testing.T) {
	// lead on team-platform: promote check passes (the actual promote may fail on
	// note-not-found, but RBAC must not block it)
	reg := rbac.New(rbac.WithRoleLoader([]rbac.ScopeGrant{
		{Identity: "carol", Scope: "team-platform", Role: rbac.Lead},
	}))
	v, err := localvault.New("team-platform", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	svc := application.NewService(v)
	h := &handlers{svc: svc, scopes: personalOnly, rbacRegistry: reg, exposeAdminTools: true}

	ctx := callerCtx("carol")
	res, err := h.notePromote(ctx, promoteReq("team-platform", "projects/nonexistent.md", "team-platform"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// RBAC passes; error (if any) must be a domain error, not permission_denied.
	if res.IsError && strings.Contains(resultText(res), "permission_denied") {
		t.Errorf("lead should pass RBAC gate; got permission_denied: %s", resultText(res))
	}
}

// --- Scenario 3b: require_promotion_approval flag ---

func TestAuthAccept_RequirePromotionApproval_ReturnssPendingWhenEnabled(t *testing.T) {
	reg := rbac.New(rbac.WithRoleLoader([]rbac.ScopeGrant{
		{Identity: "carol", Scope: "team-platform", Role: rbac.Lead},
	}))
	v, err := localvault.New("team-platform", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	svc := application.NewService(v)
	h := &handlers{
		svc:                      svc,
		scopes:                   personalOnly,
		rbacRegistry:             reg,
		exposeAdminTools:         true,
		requirePromotionApproval: true,
	}

	ctx := callerCtx("carol")
	res, err := h.notePromote(ctx, promoteReq("team-platform", "projects/foo.md", "team-platform"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("pending_approval should be a non-error result; got: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), "pending_approval") {
		t.Errorf("want pending_approval in result, got: %s", resultText(res))
	}
}

func TestAuthAccept_RequirePromotionApproval_ExecutesWhenDisabled(t *testing.T) {
	reg := rbac.New(rbac.WithRoleLoader([]rbac.ScopeGrant{
		{Identity: "carol", Scope: "team-platform", Role: rbac.Lead},
	}))
	v, err := localvault.New("team-platform", t.TempDir())
	if err != nil {
		t.Fatalf("localvault.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	svc := application.NewService(v)
	h := &handlers{
		svc:                      svc,
		scopes:                   personalOnly,
		rbacRegistry:             reg,
		exposeAdminTools:         true,
		requirePromotionApproval: false,
	}

	ctx := callerCtx("carol")
	res, err := h.notePromote(ctx, promoteReq("team-platform", "projects/nonexistent.md", "team-platform"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Flag is off: execution proceeds; result is a domain error (not-found), not pending_approval.
	if strings.Contains(resultText(res), "pending_approval") {
		t.Errorf("flag is off; should not return pending_approval, got: %s", resultText(res))
	}
}

// --- Scenario 4: audit_search gating ---

func TestAuthAccept_AuditSearch_AdminSeesRows(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "audit*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()

	fb, err := audit.NewFileBackend(tmpFile.Name())
	if err != nil {
		t.Fatalf("NewFileBackend: %v", err)
	}
	logger := audit.New(audit.WithBackend(fb))
	logger.Log(audit.Row{
		RequestID: "req_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Actor:     "alice",
		Action:    "note_create",
		Outcome:   "ok",
		Side:      "gateway",
	})
	if err := logger.Close(); err != nil {
		t.Fatalf("logger.Close: %v", err)
	}

	reg := rbac.New(rbac.WithRoleLoader([]rbac.ScopeGrant{
		{Identity: "alice", Scope: "team", Role: rbac.Admin},
	}))
	h := &handlers{
		auditSearcher:    fb,
		rbacRegistry:     reg,
		exposeAdminTools: true,
	}

	ctx := callerCtx("alice")
	res, err := h.auditSearch(ctx, auditSearchReq("team", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("admin should see audit rows; got error: %s", resultText(res))
	}
}

func TestAuthAccept_AuditSearch_NonAdminDenied(t *testing.T) {
	reg := rbac.New(rbac.WithRoleLoader([]rbac.ScopeGrant{
		{Identity: "bob", Scope: "team", Role: rbac.Contributor},
	}))
	h := &handlers{
		auditSearcher:    &stubSearcher{},
		rbacRegistry:     reg,
		exposeAdminTools: true,
	}

	ctx := callerCtx("bob")
	res, err := h.auditSearch(ctx, auditSearchReq("team", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("non-admin should get permission_denied from audit_search")
	}
	if !strings.Contains(resultText(res), "permission_denied") {
		t.Errorf("want permission_denied in result, got: %s", resultText(res))
	}
}

// --- Scenario 5: concurrent Postgres writes — one succeeds, one conflicts ---

func TestAuthAccept_PostgresConcurrentWrite_OneConflicts(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set; skipping Postgres concurrency test")
	}

	ctx := t.Context()
	v, err := postgresv.New(ctx, "test-concurrent",
		postgresv.WithDSN(dsn),
		postgresv.WithMaxConns(4),
	)
	if err != nil {
		t.Fatalf("postgresv.New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })

	// Seed one note.
	created, err := v.Create(ctx, domain.CreateInput{
		Path:        "projects/concurrent.md",
		FrontMatter: domain.FrontMatter{Title: "concurrent"},
		Body:        "initial",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	etag := created.ETag

	// Two goroutines both try to update using the same ETag.
	type result struct {
		err error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			_, err := v.UpdateBody(ctx, "projects/concurrent.md", "update "+string(rune('A'+i)), etag)
			results[i] = result{err: err}
		}(i)
	}
	wg.Wait()

	errs := make([]error, 0, 2)
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}

	if len(errs) != 1 {
		t.Fatalf("want exactly 1 conflict error, got %d errors: %v", len(errs), errs)
	}
	// Clean up the test note.
	_ = v.Delete(ctx, "projects/concurrent.md", false, "")
}

// resultText extracts the text content from a tool result for assertions.
func resultText(res *mcplib.CallToolResult) string {
	if res == nil {
		return ""
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, " ")
}
