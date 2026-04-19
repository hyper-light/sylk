// Package nodirectexec implements a go/analysis pass that enforces the
// strict-disk invariant at the type system: no package outside the authorized
// allowlist may call os/exec.Command, os/exec.CommandContext, or the
// syscall-level process-spawn primitives directly. All agent-dispatched
// commands must route through the purevfs execution broker so that side
// effects (package-manager caches, lockfile changes, build artifacts) land in
// the FUSE-projected workspace overlay rather than on host disk.
//
// The analyzer does not attempt runtime detection — it catches violations at
// compile time / CI time. Any leak that would reach production must first
// pass this check, which fails the build when an unauthorized caller tries
// to spawn a process outside the broker.
package nodirectexec

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "nodirectexec",
	Doc: "enforces the strict-disk broker mandate: direct exec.Command/CommandContext/" +
		"syscall.StartProcess/Exec/ForkExec calls are forbidden outside an explicit " +
		"allowlist of packages that need them for system-service boundaries or " +
		"sylk-internal bootstrap. Agent command execution must route through the " +
		"purevfs broker so no writes leak to host disk.",
	Run: run,
}

// bannedFuncs are package-qualified function names that spawn processes.
// Reaching any of these outside the allowlist indicates a potential leak of
// side effects to the host filesystem.
var bannedFuncs = map[string]string{
	"os/exec.Command":        "exec.Command",
	"os/exec.CommandContext": "exec.CommandContext",
	"syscall.StartProcess":   "syscall.StartProcess",
	"syscall.Exec":           "syscall.Exec",
	"syscall.ForkExec":       "syscall.ForkExec",
}

// allowedPathPrefixes lists import paths that legitimately spawn processes
// without the strict-disk broker. Each entry has a one-line rationale; the
// full policy is documented in the audit output the analyzer was built from.
var allowedPathPrefixes = []string{
	// The broker itself — it *is* the strict-disk sandbox implementation.
	"github.com/adalundhe/sylk/core/purevfs",

	// OS credential stores: keychain operations write to the system-provided
	// credential service, not to project disk. Out of scope per invariant.
	"github.com/adalundhe/sylk/core/oauth",
	"github.com/adalundhe/sylk/core/credentials",

	// Sylk bootstrap, explicitly out of scope: tree-sitter grammar compile,
	// LSP server install/launch. Artifacts land in sylk's own cache dir, not
	// the agent workspace; not treated as agent-command execution.
	"github.com/adalundhe/sylk/core/treesitter",
	"github.com/adalundhe/sylk/core/lsp",

	// User-invoked UI actions: open browser, clipboard helpers, font probes.
	// Triggered by explicit user input; not agent-dispatched.
	"github.com/adalundhe/sylk/ui/browser",
	"github.com/adalundhe/sylk/ui/fonts",
	"github.com/adalundhe/sylk/ui/editor",

	// Read-only system probes and git queries used for indexing / boot /
	// hardware detection. No writes to the project tree.
	"github.com/adalundhe/sylk/core/boot",
	"github.com/adalundhe/sylk/core/search/watcher",
	"github.com/adalundhe/sylk/core/search/cmt",
	"github.com/adalundhe/sylk/core/search/git",
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder",
	"github.com/adalundhe/sylk/core/storage/sylkdir",
}

func run(pass *analysis.Pass) (interface{}, error) {
	if isAllowedPackage(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		filename := pass.Fset.Position(file.Pos()).Filename
		// Tests use tempdirs and spawn helper processes (git init, etc.);
		// they are not part of the production strict-disk surface.
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fqName, ok := resolveCalleeFQN(pass.TypesInfo, call.Fun)
			if !ok {
				return true
			}
			display, banned := bannedFuncs[fqName]
			if !banned {
				return true
			}
			pass.Reportf(call.Pos(),
				"direct %s call is forbidden outside the strict-disk broker; route through the execution broker (see agents/shared/strict_disk_exec.go) or add this package to allowedPathPrefixes in core/ci/analyzers/nodirectexec with justification",
				display,
			)
			return true
		})
	}
	return nil, nil
}

// resolveCalleeFQN returns the fully-qualified name ("<pkg>.<func>") of the
// callee of a call expression, if the expression references a top-level
// package function. Method calls and function values return ok=false.
func resolveCalleeFQN(info *types.Info, fun ast.Expr) (string, bool) {
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
