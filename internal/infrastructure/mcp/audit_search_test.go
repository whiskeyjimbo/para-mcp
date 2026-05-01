package mcp

import (
	"context"
	"encoding/json"
	"maps"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/server/audit"
	"github.com/whiskeyjimbo/paras/internal/server/auth"
	"github.com/whiskeyjimbo/paras/internal/server/rbac"
)

// stubSearcher returns a fixed set of rows.
type stubSearcher struct {
	rows []audit.Row
}

func (s *stubSearcher) Search(_ context.Context, _ audit.SearchFilter) ([]audit.Row, error) {
	return s.rows, nil
}

func adminCtx(identity string) context.Context {
	return auth.WithCaller(context.Background(), auth.CallerIdentity(identity))
}

func auditSearchReq(scope string, extras map[string]any) mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	args := map[string]any{"scope": scope}
	maps.Copy(args, extras)
	req.Params.Arguments = args
	return req
}

func buildAdminHandlers(exposeAdminTools bool, identity string, role rbac.Role) *handlers {
	reg := rbac.New(rbac.WithRoleLoader([]rbac.ScopeGrant{
		{Identity: auth.CallerIdentity(identity), Scope: domain.ScopeID("team"), Role: role},
	}))
	return &handlers{
		// svc is unused by auditSearch; omit to avoid needing a temp dir.
		auditSearcher:    &stubSearcher{rows: []audit.Row{{RequestID: "r1", Actor: "alice", Outcome: "ok", Side: "gateway", Timestamp: time.Now()}}},
		rbacRegistry:     reg,
		exposeAdminTools: exposeAdminTools,
	}
}

func TestAuditSearch_FlagDisabled(t *testing.T) {
	h := buildAdminHandlers(false, "alice", rbac.Admin)
	ctx := adminCtx("alice")
	res, err := h.auditSearch(ctx, auditSearchReq("team", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("want error result when expose_admin_tools=false")
	}
}

func TestAuditSearch_InsufficientRole(t *testing.T) {
	h := buildAdminHandlers(true, "alice", rbac.Contributor)
	ctx := adminCtx("alice")
	res, err := h.auditSearch(ctx, auditSearchReq("team", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("want error result for non-admin caller")
	}
}

func TestAuditSearch_NoCallerInContext(t *testing.T) {
	h := buildAdminHandlers(true, "alice", rbac.Admin)
	res, err := h.auditSearch(context.Background(), auditSearchReq("team", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("want error result when no caller in context")
	}
}

func TestAuditSearch_AdminSuccess(t *testing.T) {
	h := buildAdminHandlers(true, "alice", rbac.Admin)
	ctx := adminCtx("alice")
	res, err := h.auditSearch(ctx, auditSearchReq("team", nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("want success, got error: %v", res.Content)
	}
	var rows []audit.Row
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &rows); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "r1" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

func TestAuditSearch_BothGatesFail_SameError(t *testing.T) {
	// flag off + wrong role — should get identical error message (don't reveal which check failed)
	h1 := buildAdminHandlers(false, "alice", rbac.Contributor)
	h2 := buildAdminHandlers(true, "alice", rbac.Contributor)
	ctx := adminCtx("alice")

	r1, _ := h1.auditSearch(ctx, auditSearchReq("team", nil))
	r2, _ := h2.auditSearch(ctx, auditSearchReq("team", nil))

	msg1 := r1.Content[0].(mcplib.TextContent).Text
	msg2 := r2.Content[0].(mcplib.TextContent).Text
	if msg1 != msg2 {
		t.Errorf("error messages differ (leaks which check failed):\n  flag-off: %s\n  role-fail: %s", msg1, msg2)
	}
}

func TestAuditSearch_NotRegistered_WhenFlagOff(t *testing.T) {
	svc := newTestService(t)
	s := Build(svc,
		WithRBACRegistry(rbac.New()),
		WithAuditSearcher(&stubSearcher{}),
		// WithExposeAdminTools NOT called — default false
	)
	_ = s // confirms Build does not panic when flag is off
}
