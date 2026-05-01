// Package wirecheck provides a go/analysis pass that enforces the invariant:
// AllowedScopes must never be sourced from wire input (CallToolRequest methods).
// This is a security gate — AllowedScopes must always come from the RBAC resolver.
package wirecheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const callToolRequestType = "github.com/mark3labs/mcp-go/mcp.CallToolRequest"

// Analyzer is the go/analysis entry point for the wirecheck pass.
var Analyzer = &analysis.Analyzer{
	Name:     "wirecheck",
	Doc:      "reports assignments of AllowedScopes from wire input (CallToolRequest methods)",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.CompositeLit)(nil),
		(*ast.AssignStmt)(nil),
	}

	insp.Preorder(nodeFilter, func(n ast.Node) {
		switch node := n.(type) {
		case *ast.CompositeLit:
			checkCompositeLit(pass, node)
		case *ast.AssignStmt:
			checkAssignStmt(pass, node)
		}
	})
	return nil, nil
}

// checkCompositeLit reports AllowedScopes: <wireExpr> in struct literals.
func checkCompositeLit(pass *analysis.Pass, lit *ast.CompositeLit) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := kv.Key.(*ast.Ident)
		if !ok || ident.Name != "AllowedScopes" {
			continue
		}
		if isWireExpr(pass, kv.Value) {
			pass.Reportf(kv.Pos(), "AllowedScopes must not be sourced from wire input; use rbac.AllowedScopes() instead")
		}
	}
}

// checkAssignStmt reports x.AllowedScopes = <wireExpr>.
func checkAssignStmt(pass *analysis.Pass, stmt *ast.AssignStmt) {
	for i, lhs := range stmt.Lhs {
		sel, ok := lhs.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "AllowedScopes" {
			continue
		}
		if i < len(stmt.Rhs) && isWireExpr(pass, stmt.Rhs[i]) {
			pass.Reportf(stmt.Pos(), "AllowedScopes must not be sourced from wire input; use rbac.AllowedScopes() instead")
		}
	}
}

// isWireExpr reports whether expr contains, anywhere in its subtree, a method
// call on a CallToolRequest value. This covers direct calls as well as calls
// nested inside type conversions, composite literals, and other wrappers.
func isWireExpr(pass *analysis.Pass, expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		t := pass.TypesInfo.TypeOf(sel.X)
		if t == nil {
			return true
		}
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		}
		named, ok := t.(*types.Named)
		if !ok {
			return true
		}
		if named.Obj().Pkg().Path()+"."+named.Obj().Name() == callToolRequestType {
			found = true
			return false
		}
		return true
	})
	return found
}
