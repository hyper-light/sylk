// Package claimsops enforces claims infrastructure invariants that are cheap
// enough to check statically: no untracked goroutines in claims runtime code,
// no literal unbounded work queues, no broad claims subscriptions, no
// untyped binary artifacts, no participant registrations without declared
// determinism, and no new direct VFS mutation bypasses outside explicit
// migration boundaries.
package claimsops

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "claimsops",
	Doc:  "enforces claims infrastructure operational invariants and directs callers to participant-aware claims APIs",
	Run:  run,
}

var directVFSBypassMethods = map[string]struct{}{
	"BeginPipeline":             {},
	"CommitPipeline":            {},
	"CreatePipelineVFS":         {},
	"MergePipelineIntoGreen":    {},
	"RollbackPipelineIfTracked": {},
}

var allowedDirectVFSPackages = []string{
	"github.com/adalundhe/sylk/core/versioning",
	"github.com/adalundhe/sylk/core/container",
	"github.com/adalundhe/sylk/core/claims",
}

var allowedDirectVFSFiles = []string{
	"agents/shared/pipeline_committer.go",
}

var allowedRawGoFiles = []string{
	"core/claims/validator_registry.go",
}

func run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		filename := filepathSlash(pass.Fset.Position(file.Pos()).Filename)
		if skipFile(filename, file) {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			checkNode(pass, filename, n)
			return true
		})
	}
	return nil, nil
}

func checkNode(pass *analysis.Pass, filename string, n ast.Node) {
	switch node := n.(type) {
	case *ast.GoStmt:
		checkGoStmt(pass, filename, node)
	case *ast.CallExpr:
		checkCall(pass, filename, node)
	case *ast.CompositeLit:
		checkComposite(pass, node)
	}
}

func checkGoStmt(pass *analysis.Pass, filename string, node *ast.GoStmt) {
	if !checksClaimsConcurrency(pass.Pkg.Path()) || rawGoAllowed(filename) {
		return
	}
	pass.Reportf(node.Pos(), "raw go statement is forbidden in claims infrastructure; launch work through core/concurrency.GoroutineScope.Go or claims.ScopeProvider")
}

func checkCall(pass *analysis.Pass, filename string, call *ast.CallExpr) {
	if checksClaimsConcurrency(pass.Pkg.Path()) && literalUnboundedChannel(call) {
		pass.Reportf(call.Pos(), "literal unbounded work channel is forbidden; derive a bounded capacity from participant registration or runtime config")
	}
	if broadClaimsSubscription(call) {
		pass.Reportf(call.Pos(), "broad claims subscription is forbidden; subscribe with CanonicalAgentRefTopic, InboxPatternsFor, or another bounded participant topic")
	}
	if missingRegistrationDeterminism(pass.TypesInfo, call) {
		pass.Reportf(call.Pos(), "participant registration must declare HandlerDeterminism; use HandlerDeterminismPure, Content, SideEffect, or Nondeterministic")
	}
	if directVFSBypass(pass, call) && !directVFSBypassAllowedAt(pass.Pkg.Path(), filename) {
		pass.Reportf(call.Pos(), "direct VFS mutation bypasses claims evidence; route through the infrastructure service dispatcher or a documented claim-synthesizing wrapper")
	}
}

func checkComposite(pass *analysis.Pass, lit *ast.CompositeLit) {
	if !artifactLiteralMissingDataType(lit) {
		return
	}
	pass.Reportf(lit.Pos(), "claims Artifact with Data must set DataType; use SetArtifactData or a typed artifact constructor")
}

func literalUnboundedChannel(call *ast.CallExpr) bool {
	if len(call.Args) != 1 || !isBuiltinMake(call.Fun) {
		return false
	}
	chanType, ok := call.Args[0].(*ast.ChanType)
	if !ok {
		return false
	}
	return !isSignalChannel(chanType)
}

func isBuiltinMake(fun ast.Expr) bool {
	ident, ok := fun.(*ast.Ident)
	return ok && ident.Name == "make"
}

func isSignalChannel(chanType *ast.ChanType) bool {
	st, ok := chanType.Value.(*ast.StructType)
	return ok && st.Fields != nil && len(st.Fields.List) == 0
}

func broadClaimsSubscription(call *ast.CallExpr) bool {
	if len(call.Args) == 0 || selectorName(call.Fun) != "SubscribeDelta" {
		return false
	}
	pattern, ok := stringLiteral(call.Args[0])
	if !ok {
		return false
	}
	return claimsPatternBroad(pattern)
}

func claimsPatternBroad(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	return pattern == "" || pattern == "*" || pattern == "claims.*" || strings.HasSuffix(pattern, ".>")
}

func missingRegistrationDeterminism(info *types.Info, call *ast.CallExpr) bool {
	if len(call.Args) < 7 || calleeName(info, call.Fun) != "NewParticipantRegistration" {
		return false
	}
	return emptyDeterminismArg(call.Args[6])
}

func emptyDeterminismArg(arg ast.Expr) bool {
	if value, ok := stringLiteral(arg); ok {
		return strings.TrimSpace(value) == ""
	}
	call, ok := arg.(*ast.CallExpr)
	return ok && len(call.Args) == 1 && emptyDeterminismArg(call.Args[0])
}

func artifactLiteralMissingDataType(lit *ast.CompositeLit) bool {
	if !artifactLiteralType(lit.Type) {
		return false
	}
	fields := keyedFields(lit.Elts)
	_, hasData := fields["Data"]
	_, hasType := fields["DataType"]
	return hasData && !hasType
}

func artifactLiteralType(expr ast.Expr) bool {
	switch typ := expr.(type) {
	case *ast.Ident:
		return typ.Name == "Artifact"
	case *ast.SelectorExpr:
		return typ.Sel.Name == "Artifact"
	default:
		return false
	}
}

func keyedFields(elts []ast.Expr) map[string]struct{} {
	out := make(map[string]struct{}, len(elts))
	for _, elt := range elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok {
			out[key.Name] = struct{}{}
		}
	}
	return out
}

func directVFSBypass(pass *analysis.Pass, call *ast.CallExpr) bool {
	name := selectorName(call.Fun)
	if _, ok := directVFSBypassMethods[name]; !ok {
		return false
	}
	return methodFromVersioning(pass.TypesInfo, call.Fun)
}

func methodFromVersioning(info *types.Info, fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	selection := info.Selections[sel]
	if selection == nil {
		return false
	}
	obj := selection.Obj()
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == "github.com/adalundhe/sylk/core/versioning"
}

func directVFSBypassAllowed(pkgPath string) bool {
	for _, prefix := range allowedDirectVFSPackages {
		if pkgPath == prefix || strings.HasPrefix(pkgPath, prefix+"/") {
			return true
		}
	}
	return false
}

func directVFSBypassAllowedAt(pkgPath, filename string) bool {
	if directVFSBypassAllowed(pkgPath) {
		return true
	}
	for _, allowed := range allowedDirectVFSFiles {
		if strings.HasSuffix(filename, allowed) {
			return true
		}
	}
	return false
}

func checksClaimsConcurrency(pkgPath string) bool {
	if !strings.HasPrefix(pkgPath, "github.com/adalundhe/sylk/") {
		return true
	}
	return pkgPath == "github.com/adalundhe/sylk/core/claims"
}

func rawGoAllowed(filename string) bool {
	for _, allowed := range allowedRawGoFiles {
		if strings.HasSuffix(filename, allowed) {
			return true
		}
	}
	return false
}

func selectorName(expr ast.Expr) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

func calleeName(info *types.Info, expr ast.Expr) string {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if obj := info.ObjectOf(sel.Sel); obj != nil {
			return obj.Name()
		}
		return sel.Sel.Name
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

func skipFile(filename string, file *ast.File) bool {
	return strings.HasSuffix(filename, "_test.go") || inGeneratedOrMocks(filename, file)
}

func inGeneratedOrMocks(filename string, file *ast.File) bool {
	if strings.Contains(filename, "/mocks/") {
		return true
	}
	for _, group := range file.Comments {
		if strings.Contains(group.Text(), "Code generated") {
			return true
		}
	}
	return false
}

func filepathSlash(filename string) string {
	return strings.ReplaceAll(filename, "\\", "/")
}
