package a

import (
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"paras/domain"
)

// bad: AllowedScopes sourced from wire input via composite literal
func badCompositeLit(req mcplib.CallToolRequest) domain.AuthFilter {
	return domain.AuthFilter{AllowedScopes: scopeSlice(req.GetString("scopes", ""))} // want `AllowedScopes must not be sourced from wire input`
}

// bad: AllowedScopes sourced from wire input via assignment
func badAssign(req mcplib.CallToolRequest) domain.AuthFilter {
	var f domain.AuthFilter
	f.AllowedScopes = scopeSlice(req.GetString("scopes", "")) // want `AllowedScopes must not be sourced from wire input`
	return f
}

// good: AllowedScopes comes from an RBAC resolver, not from req
func good(rbacScopes []domain.ScopeID) domain.AuthFilter {
	return domain.AuthFilter{AllowedScopes: rbacScopes}
}

func scopeSlice(s string) []domain.ScopeID { return []domain.ScopeID{domain.ScopeID(s)} }
