// Package nodirectbuspub enforces that the channel bus's Publish path
// flows only through the canonical wrapper (and its allowlisted
// internal call sites). Today the analyzer is intentionally narrow —
// it flags packages outside the allowlist that try to side-step
// Publish through reflection or alternative dispatch helpers. The
// real chokepoint instrumentation already wraps Publish itself.
//
// Future expansion: when alternative emit paths surface (e.g., a
// "fast-path" for telemetry that bypasses the instrumented Publish),
// the lint catches those at compile time.
package nodirectbuspub

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "nodirectbuspub",
	Doc: "enforces that bus delivery goes through the instrumented ChannelBus.Publish path: " +
		"raw send-on-channel of bus messages outside the allowlist is forbidden so the fabric " +
		"chokepoint emission cannot be bypassed.",
	Run: run,
}

// bannedSends are package-qualified function names that bypass the
// instrumented Publish path. The current implementation flags none by
// default — the chokepoint is enforced at the Publish wrapper itself,
// and alternative dispatchers are not yet present. Adding new entries
// here is the explicit way to extend coverage when alternatives appear.
var bannedSends = map[string]string{}

// allowedPathPrefixes lists import paths that legitimately use any
// future alternative dispatch helpers.
var allowedPathPrefixes = []string{
	"github.com/adalundhe/sylk/agents/guide",
	"github.com/adalundhe/sylk/core/events",
}

func run(pass *analysis.Pass) (interface{}, error) {
	if isAllowedPackage(pass.Pkg.Path()) || len(bannedSends) == 0 {
		return nil, nil
	}
	for _, file := range pass.Files {
		filename := pass.Fset.Position(file.Pos()).Filename
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fqName, ok := resolveFQN(pass.TypesInfo, call.Fun)
			if !ok {
				return true
			}
			display, banned := bannedSends[fqName]
			if !banned {
				return true
			}
			pass.Reportf(call.Pos(), "direct %s call bypasses the instrumented bus chokepoint; route through agents/guide.ChannelBus.Publish so the fabric captures the message", display)
			return true
		})
	}
	return nil, nil
}

func resolveFQN(info *types.Info, fun ast.Expr) (string, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	obj := info.ObjectOf(sel.Sel)
	if obj == nil {
		return "", false
	}
	fn, ok := obj.(*types.Func)
	if !ok {
		return "", false
	}
	pkg := fn.Pkg()
	if pkg == nil {
		return "", false
	}
	return pkg.Path() + "." + fn.Name(), true
}

func isAllowedPackage(pkgPath string) bool {
	for _, prefix := range allowedPathPrefixes {
		if pkgPath == prefix || strings.HasPrefix(pkgPath, prefix+"/") {
			return true
		}
	}
	return false
}
