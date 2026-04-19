// Package nodirectdbwrite implements a go/analysis pass that enforces the
// Activity Fabric's "all writes go through the chokepoint" invariant.
//
// Specifically, it forbids calling write-shaped Bun builder methods
// (NewInsert / NewUpdate / NewDelete) on a *bun.DB receiver outside of
// the core/database package itself. Production code must instead route
// writes through database.RunInWriteTx, which serializes mutations on
// the BunSQLite write mutex AND emits a store_write_committed activity
// for fabric capture.
//
// Calling these builders on a *bun.Tx receiver is allowed — that's the
// transactional path inside a RunInWriteTx callback. The analyzer's
// rule is scoped to *bun.DB so the inside-callback path is unaffected.
//
// Without this lint, a future contributor could add a direct
// db.NewInsert(...) call that:
//   - bypasses the writeMu serialization (re-introducing the
//     "database is locked" sub-millisecond races we recently fixed)
//   - bypasses the fabric chokepoint (silently disabling activity
//     capture for that subsystem)
//
// The lint catches both regressions at compile time.
package nodirectdbwrite

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "nodirectdbwrite",
	Doc: "enforces the Activity Fabric chokepoint invariant: write-shaped Bun " +
		"builders (NewInsert/NewUpdate/NewDelete) on a *bun.DB receiver are " +
		"forbidden outside the core/database package. Route writes through " +
		"database.RunInWriteTx so they pick up writeMu serialization and " +
		"fabric capture.",
	Run: run,
}

// bannedDBMethods are method names that perform a write when invoked on a
// *bun.DB receiver. Invoking them on a *bun.Tx receiver is allowed —
// that's the transactional code path the lint is steering callers toward.
var bannedDBMethods = map[string]struct{}{
	"NewInsert": {},
	"NewUpdate": {},
	"NewDelete": {},
	"NewMerge":  {},
	"NewRaw":    {}, // raw SQL execution can mutate; force into transaction
	"NewTruncate": {},
}

// allowedPathPrefixes lists import paths that legitimately call write
// builders on a *bun.DB receiver.
var allowedPathPrefixes = []string{
	// The database package owns the wrapper and the RunInWriteTx
	// implementation; it must access *bun.DB directly.
	"github.com/adalundhe/sylk/core/database",

	// The activity store bypasses RunInWriteTx for its own writes to
	// avoid recursive fabric emission. It uses raw SQLDB().ExecContext
	// rather than NewInsert, but the package is allowlisted as a
	// belt-and-suspenders so a future refactor that switches to
	// builders doesn't break the lint.
	"github.com/adalundhe/sylk/core/activity/activitystore",
}

func run(pass *analysis.Pass) (interface{}, error) {
	if isAllowedPackage(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		filename := pass.Fset.Position(file.Pos()).Filename
		// Tests are exempt — they often instantiate a bun.DB and
		// exercise builders directly to set up fixtures.
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, recvType, ok := resolveMethodCall(pass.TypesInfo, call.Fun)
			if !ok {
				return true
			}
			if _, banned := bannedDBMethods[method]; !banned {
				return true
			}
			if !isBunDBReceiver(recvType) {
				return true
			}
			pass.Reportf(call.Pos(),
				"direct *bun.DB.%s call is forbidden outside core/database; route writes through database.RunInWriteTx so they serialize on the write mutex and emit a store_write_committed fabric activity",
				method,
			)
			return true
		})
	}
	return nil, nil
}

// resolveMethodCall returns (methodName, receiverType, true) when the
// call expression is a method invocation on an addressable receiver.
// Returns ok=false for top-level functions, function values, or
// expressions whose type information is missing.
func resolveMethodCall(info *types.Info, fun ast.Expr) (string, types.Type, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil, false
	}
	obj := info.ObjectOf(sel.Sel)
	if obj == nil {
		return "", nil, false
	}
	fn, ok := obj.(*types.Func)
	if !ok {
		return "", nil, false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return "", nil, false
	}
	return fn.Name(), sig.Recv().Type(), true
}

// isBunDBReceiver reports whether t is *bun.DB (or bun.DB).
func isBunDBReceiver(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	pkg := named.Obj().Pkg()
	if pkg == nil {
		return false
	}
	return pkg.Path() == "github.com/uptrace/bun" && named.Obj().Name() == "DB"
}

func isAllowedPackage(pkgPath string) bool {
	for _, prefix := range allowedPathPrefixes {
		if pkgPath == prefix || strings.HasPrefix(pkgPath, prefix+"/") {
			return true
		}
	}
	return false
}
