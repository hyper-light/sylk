// cmd/nogo/main.go
package main

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

var Analyzer = &analysis.Analyzer{
	Name: "nogo",
	Doc:  "forbids raw go statements outside of approved packages",
	Run:  run,
}

func main() {
	singlechecker.Main(Analyzer)
}

func run(pass *analysis.Pass) (interface{}, error) {
	// Whitelist: only core/concurrency can use raw go
	if strings.Contains(pass.Pkg.Path(), "core/concurrency") {
		return nil, nil
	}

	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			if goStmt, ok := n.(*ast.GoStmt); ok {
				pass.Reportf(goStmt.Pos(),
					"raw 'go' statement forbidden - use scope.Go() from core/concurrency")
			}
			return true
		})
	}
	return nil, nil
}
