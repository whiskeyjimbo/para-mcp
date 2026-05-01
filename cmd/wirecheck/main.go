// Command wirecheck runs the AllowedScopes wire-source linter.
// Usage: wirecheck [packages]
// Example: wirecheck github.com/whiskeyjimbo/paras/internal/infrastructure/mcp
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/whiskeyjimbo/paras/internal/analysis/wirecheck"
)

func main() {
	singlechecker.Main(wirecheck.Analyzer)
}
