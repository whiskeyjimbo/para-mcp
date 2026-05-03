package rbac_test

import (
	"testing"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/server/auth"
	"github.com/whiskeyjimbo/para-mcp/internal/server/rbac"
)

var (
	jrose  auth.CallerIdentity = "jrose"
	cibot  auth.CallerIdentity = "ci-bot"
	scopes                     = []domain.ScopeID{"personal", "team", "company"}
)

func grants() []rbac.ScopeGrant {
	return []rbac.ScopeGrant{
		{Identity: jrose, Scope: "personal", Role: rbac.Admin},
		{Identity: jrose, Scope: "team", Role: rbac.Lead},
		{Identity: cibot, Scope: "team", Role: rbac.Viewer},
	}
}

func TestAllowedScopes_EmptyRequested(t *testing.T) {
	r := rbac.New(rbac.WithRoleLoader(grants()))
	got, err := r.AllowedScopes(jrose, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 scopes, got %v", got)
	}
}

func TestAllowedScopes_FilteredByRole(t *testing.T) {
	r := rbac.New(rbac.WithRoleLoader(grants()))
	got, err := r.AllowedScopes(jrose, scopes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 (personal+team), got %v", got)
	}
}

func TestAllowedScopes_DenyAll(t *testing.T) {
	r := rbac.New(rbac.WithRoleLoader(grants()))
	got, err := r.AllowedScopes(jrose, []domain.ScopeID{"company"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty deny-all, got %v", got)
	}
}

func TestAllowedScopes_UnknownCaller(t *testing.T) {
	r := rbac.New(rbac.WithRoleLoader(grants()))
	got, err := r.AllowedScopes("stranger", scopes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty for unknown caller, got %v", got)
	}
}

func TestHasRole(t *testing.T) {
	r := rbac.New(rbac.WithRoleLoader(grants()))
	if !r.HasRole(jrose, "team", rbac.Lead) {
		t.Fatal("jrose should be lead on team")
	}
	if r.HasRole(jrose, "team", rbac.Admin) {
		t.Fatal("jrose should not be admin on team")
	}
	if !r.HasRole(cibot, "team", rbac.Viewer) {
		t.Fatal("ci-bot should be viewer on team")
	}
	if r.HasRole(cibot, "team", rbac.Contributor) {
		t.Fatal("ci-bot should not be contributor on team")
	}
}

func TestReload(t *testing.T) {
	r := rbac.New(rbac.WithRoleLoader(grants()))
	got, _ := r.AllowedScopes(cibot, nil)
	if len(got) != 1 {
		t.Fatalf("before reload: want 1, got %v", got)
	}
	r.Reload([]rbac.ScopeGrant{})
	got, _ = r.AllowedScopes(cibot, nil)
	if len(got) != 0 {
		t.Fatalf("after reload: want 0, got %v", got)
	}
}
