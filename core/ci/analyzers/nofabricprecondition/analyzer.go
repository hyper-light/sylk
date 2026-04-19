// Package nofabricprecondition enforces the constitutional rule of
// the Activity Fabric: no skill in the codebase may inspect fabric
// state as a precondition for executing its primary work.
//
// Concretely: skill handler functions (registered via
// skills.NewSkill(...).Handler(...)) must not call lens-style query
// functions before the handler's primary mutating work. The fabric is
// an invitation surface, not a gate.
//
// The current analyzer is intentionally permissive — it catches
// imports of forbidden gate-style packages from skill files. The
// detection is heuristic; the rule itself is documented and enforced
// primarily by code review for now. The analyzer's strength comes
// from making future violations easy to spot in CI.
package nofabricprecondition

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "nofabricprecondition",
	Doc: "enforces the Activity Fabric constitutional rule: skill handlers may not " +
		"call lens-style fabric queries as preconditions for executing primary work. " +
		"The fabric is informative, never prescriptive.",
	Run: run,
}

// gateFunctionPatterns are name fragments associated with gate-style
// preconditions. Functions whose names match these patterns are
// flagged when they appear inside packages that register skills.
var gateFunctionPatterns = []string{
	"requireDecision",
	"requireFabric",
	"requireManifest",
	"mustHavePeer",
}

// allowedPathPrefixes are the documentation/test packages where these
// names may legitimately appear (as references, not real gates).
var allowedPathPrefixes = []string{
	"github.com/adalundhe/sylk/core/ci/analyzers/nofabricprecondition",
	"github.com/adalundhe/sylk/docs",
}

func run(pass *analysis.Pass) (interface{}, error) {
	if isAllowedPackage(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		filename := pass.Fset.Position(file.Pos()).Filename
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			name := fn.Name.Name
			for _, pattern := range gateFunctionPatterns {
				if strings.Contains(name, pattern) {
					pass.Reportf(fn.Pos(),
						"function %q matches a gate-style precondition pattern (%q); the Activity Fabric is informative, never prescriptive — skill handlers must not block primary work on fabric state. See docs/FABRIC.md \"Gates that disappear\"",
						name, pattern,
					)
				}
			}
			return true
		})
	}
	return nil, nil
}

func isAllowedPackage(pkgPath string) bool {
	for _, prefix := range allowedPathPrefixes {
		if pkgPath == prefix || strings.HasPrefix(pkgPath, prefix+"/") {
			return true
		}
	}
	return false
}
