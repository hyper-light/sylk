// Package relations provides relation extraction from source code entities.
package relations

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/extractors"
)

// =============================================================================
// Call Type Enum
// =============================================================================

// CallType represents the type of function call relationship.
type CallType int

const (
	CallTypeDirect      CallType = 0 // Direct function/method call
	CallTypeIndirect    CallType = 1 // Indirect call (via variable/parameter)
	CallTypeConditional CallType = 2 // Call within conditional branch
)

// String returns the string representation of the call type.
func (ct CallType) String() string {
	switch ct {
	case CallTypeDirect:
		return "direct"
	case CallTypeIndirect:
		return "indirect"
	case CallTypeConditional:
		return "conditional"
	default:
		return "unknown"
	}
}

// ParseCallType parses a string into a CallType.
func ParseCallType(s string) (CallType, bool) {
	switch s {
	case "direct":
		return CallTypeDirect, true
	case "indirect":
		return CallTypeIndirect, true
	case "conditional":
		return CallTypeConditional, true
	default:
		return CallTypeDirect, false
	}
}

// =============================================================================
// Call Relation
// =============================================================================

// CallRelation represents a function call relationship between two entities.
type CallRelation struct {
	ID       string   `json:"id"`
	CallerID string   `json:"caller_id"`
	CalleeID string   `json:"callee_id"`
	CallType CallType `json:"call_type"`
	FilePath string   `json:"file_path"`
	Line     int      `json:"line"`
}

// =============================================================================
// Call Graph Extractor
// =============================================================================

// scopeTracker tracks the depth of conditional scopes during AST traversal.
type scopeTracker struct {
	conditionalDepth int
}

// inConditional returns true if currently inside a conditional scope.
func (s *scopeTracker) inConditional() bool {
	return s.conditionalDepth > 0
}

// enterConditional increments the conditional depth.
func (s *scopeTracker) enterConditional() {
	s.conditionalDepth++
}

// exitConditional decrements the conditional depth.
func (s *scopeTracker) exitConditional() {
	if s.conditionalDepth > 0 {
		s.conditionalDepth--
	}
}

// CallGraphExtractor extracts function call relationships from source code.
type CallGraphExtractor struct {
	// Regex patterns for non-Go languages
	goCallPattern *regexp.Regexp
	tsCallPattern *regexp.Regexp
	pyCallPattern *regexp.Regexp
}

// NewCallGraphExtractor creates a new CallGraphExtractor.
func NewCallGraphExtractor() *CallGraphExtractor {
	return &CallGraphExtractor{
		// Go function calls: identifier followed by (
		goCallPattern: regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`),

		// TypeScript/JavaScript function calls: identifier followed by optional generics and (
		tsCallPattern: regexp.MustCompile(`\b([a-zA-Z_$][a-zA-Z0-9_$]*)\s*(?:<[^>]*>)?\s*\(`),

		// Python function calls: identifier followed by (
		pyCallPattern: regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`),
	}
}

// ExtractRelations extracts call relationships from the provided entities and content.
func (e *CallGraphExtractor) ExtractRelations(entities []extractors.Entity, content map[string][]byte) ([]knowledge.ExtractedRelation, error) {
	var relations []knowledge.ExtractedRelation

	// Build entity lookup maps
	entityByID := make(map[string]extractors.Entity)
	entityByName := make(map[string][]extractors.Entity)
	funcEntities := make(map[string][]extractors.Entity) // file -> functions in file

	for _, entity := range entities {
		entityByID[entity.ID] = entity
		entityByName[entity.Name] = append(entityByName[entity.Name], entity)

		if entity.Kind == knowledge.EntityKindFunction || entity.Kind == knowledge.EntityKindMethod {
			funcEntities[entity.FilePath] = append(funcEntities[entity.FilePath], entity)
		}
	}

	// Process each file
	for filePath, fileContent := range content {
		fileRelations := e.extractCallsFromFile(filePath, fileContent, funcEntities[filePath], entityByName)
		relations = append(relations, fileRelations...)
	}

	return relations, nil
}

// extractCallsFromFile extracts function calls from a single file.
func (e *CallGraphExtractor) extractCallsFromFile(
	filePath string,
	content []byte,
	funcsInFile []extractors.Entity,
	entityByName map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	// Determine language by extension
	if strings.HasSuffix(filePath, ".go") {
		relations = e.extractGoCallGraph(filePath, content, funcsInFile, entityByName)
	} else if strings.HasSuffix(filePath, ".ts") || strings.HasSuffix(filePath, ".tsx") ||
		strings.HasSuffix(filePath, ".js") || strings.HasSuffix(filePath, ".jsx") {
		relations = e.extractTSCallGraph(filePath, content, funcsInFile, entityByName)
	} else if strings.HasSuffix(filePath, ".py") {
		relations = e.extractPyCallGraph(filePath, content, funcsInFile, entityByName)
	}

	return relations
}

// extractGoCallGraph extracts call relationships from Go source code using AST.
func (e *CallGraphExtractor) extractGoCallGraph(
	filePath string,
	content []byte,
	funcsInFile []extractors.Entity,
	entityByName map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	if len(content) == 0 {
		return relations
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, 0)
	if err != nil {
		// Fall back to regex-based extraction on parse error
		return e.extractCallsWithRegex(filePath, content, funcsInFile, entityByName, e.goCallPattern)
	}

	// Build a map of function ranges to caller entities
	callerMap := make(map[*ast.FuncDecl]extractors.Entity)
	for _, entity := range funcsInFile {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				fnStart := fset.Position(fn.Pos()).Line
				if fnStart == entity.StartLine {
					callerMap[fn] = entity
					break
				}
			}
		}
	}

	// Visit each function declaration
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		caller, hasCaller := callerMap[fn]
		if !hasCaller {
			continue
		}

		// Extract calls with proper scope tracking
		scope := &scopeTracker{}
		fnRelations := e.extractCallsFromFuncBody(fn.Body, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, fnRelations...)
	}

	return relations
}

// extractCallsFromFuncBody extracts call relations from a function body with scope tracking.
func (e *CallGraphExtractor) extractCallsFromFuncBody(
	body *ast.BlockStmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	for _, stmt := range body.List {
		stmtRels := e.extractCallsFromStmt(stmt, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, stmtRels...)
	}

	return relations
}

// stmtContext holds common parameters for statement extraction to reduce parameter passing.
type stmtContext struct {
	caller       extractors.Entity
	fset         *token.FileSet
	entityByName map[string][]extractors.Entity
	filePath     string
	content      []byte
	scope        *scopeTracker
}

// extractCallsFromStmt extracts call relations from a single statement.
func (e *CallGraphExtractor) extractCallsFromStmt(
	stmt ast.Stmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	ctx := &stmtContext{caller, fset, entityByName, filePath, content, scope}
	return e.dispatchStmt(stmt, ctx)
}

// dispatchStmt dispatches statement to appropriate handler.
func (e *CallGraphExtractor) dispatchStmt(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		return e.extractCallsFromIfStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.SwitchStmt:
		return e.extractCallsFromSwitchStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.TypeSwitchStmt:
		return e.extractCallsFromTypeSwitchStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.SelectStmt:
		return e.extractCallsFromSelectStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.ForStmt:
		return e.extractCallsFromForStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.RangeStmt:
		return e.extractCallsFromRangeStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.BlockStmt:
		return e.extractCallsFromFuncBody(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	default:
		return e.dispatchSimpleStmt(stmt, ctx)
	}
}

// dispatchSimpleStmt handles simpler statement types.
func (e *CallGraphExtractor) dispatchSimpleStmt(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return e.extractCallsFromExpr(s.X, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.AssignStmt:
		return e.extractCallsFromAssignStmt(s, ctx)
	case *ast.ReturnStmt:
		return e.extractCallsFromReturnStmt(s, ctx)
	case *ast.DeclStmt:
		return e.extractCallsFromDeclStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.DeferStmt:
		return e.extractCallsFromExpr(s.Call, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.GoStmt:
		return e.extractCallsFromExpr(s.Call, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.CaseClause:
		return e.extractCallsFromCaseClause(s, ctx)
	case *ast.CommClause:
		return e.extractCallsFromCommClause(s, ctx)
	default:
		return nil
	}
}

// extractCallsFromAssignStmt extracts calls from assignment statements.
func (e *CallGraphExtractor) extractCallsFromAssignStmt(s *ast.AssignStmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	for _, expr := range s.Rhs {
		exprRels := e.extractCallsFromExpr(expr, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
		relations = append(relations, exprRels...)
	}
	return relations
}

// extractCallsFromReturnStmt extracts calls from return statements.
func (e *CallGraphExtractor) extractCallsFromReturnStmt(s *ast.ReturnStmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	for _, expr := range s.Results {
		exprRels := e.extractCallsFromExpr(expr, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
		relations = append(relations, exprRels...)
	}
	return relations
}

// extractCallsFromCaseClause extracts calls from switch case clauses.
func (e *CallGraphExtractor) extractCallsFromCaseClause(s *ast.CaseClause, ctx *stmtContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	for _, expr := range s.List {
		exprRels := e.extractCallsFromExpr(expr, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
		relations = append(relations, exprRels...)
	}
	for _, bodyStmt := range s.Body {
		bodyRels := e.dispatchStmt(bodyStmt, ctx)
		relations = append(relations, bodyRels...)
	}
	return relations
}

// extractCallsFromCommClause extracts calls from select comm clauses.
func (e *CallGraphExtractor) extractCallsFromCommClause(s *ast.CommClause, ctx *stmtContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	if s.Comm != nil {
		commRels := e.dispatchStmt(s.Comm, ctx)
		relations = append(relations, commRels...)
	}
	for _, bodyStmt := range s.Body {
		bodyRels := e.dispatchStmt(bodyStmt, ctx)
		relations = append(relations, bodyRels...)
	}
	return relations
}

// extractCallsFromIfStmt extracts calls from an if statement (conditional scope).
func (e *CallGraphExtractor) extractCallsFromIfStmt(
	s *ast.IfStmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	// Condition is evaluated before entering conditional scope
	condRels := e.extractCallsFromExpr(s.Cond, caller, fset, entityByName, filePath, content, scope)
	relations = append(relations, condRels...)

	// Init statement is also outside conditional scope
	if s.Init != nil {
		initRels := e.extractCallsFromStmt(s.Init, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, initRels...)
	}

	// Body is inside conditional scope
	scope.enterConditional()
	bodyRels := e.extractCallsFromFuncBody(s.Body, caller, fset, entityByName, filePath, content, scope)
	relations = append(relations, bodyRels...)
	scope.exitConditional()

	// Else branch is also conditional
	if s.Else != nil {
		scope.enterConditional()
		elseRels := e.extractCallsFromStmt(s.Else, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, elseRels...)
		scope.exitConditional()
	}

	return relations
}

// extractCallsFromSwitchStmt extracts calls from a switch statement.
func (e *CallGraphExtractor) extractCallsFromSwitchStmt(
	s *ast.SwitchStmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	// Init and tag are outside conditional scope
	if s.Init != nil {
		initRels := e.extractCallsFromStmt(s.Init, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, initRels...)
	}
	if s.Tag != nil {
		tagRels := e.extractCallsFromExpr(s.Tag, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, tagRels...)
	}

	// Case bodies are conditional
	scope.enterConditional()
	bodyRels := e.extractCallsFromFuncBody(s.Body, caller, fset, entityByName, filePath, content, scope)
	relations = append(relations, bodyRels...)
	scope.exitConditional()

	return relations
}

// extractCallsFromTypeSwitchStmt extracts calls from a type switch statement.
func (e *CallGraphExtractor) extractCallsFromTypeSwitchStmt(
	s *ast.TypeSwitchStmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	if s.Init != nil {
		initRels := e.extractCallsFromStmt(s.Init, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, initRels...)
	}
	if s.Assign != nil {
		assignRels := e.extractCallsFromStmt(s.Assign, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, assignRels...)
	}

	scope.enterConditional()
	bodyRels := e.extractCallsFromFuncBody(s.Body, caller, fset, entityByName, filePath, content, scope)
	relations = append(relations, bodyRels...)
	scope.exitConditional()

	return relations
}

// extractCallsFromSelectStmt extracts calls from a select statement.
func (e *CallGraphExtractor) extractCallsFromSelectStmt(
	s *ast.SelectStmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	scope.enterConditional()
	relations := e.extractCallsFromFuncBody(s.Body, caller, fset, entityByName, filePath, content, scope)
	scope.exitConditional()
	return relations
}

// extractCallsFromForStmt extracts calls from a for statement.
func (e *CallGraphExtractor) extractCallsFromForStmt(
	s *ast.ForStmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	// Init, condition, and post are outside the conditional loop body
	if s.Init != nil {
		initRels := e.extractCallsFromStmt(s.Init, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, initRels...)
	}
	if s.Cond != nil {
		condRels := e.extractCallsFromExpr(s.Cond, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, condRels...)
	}
	if s.Post != nil {
		postRels := e.extractCallsFromStmt(s.Post, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, postRels...)
	}

	// Loop body is conditional
	scope.enterConditional()
	bodyRels := e.extractCallsFromFuncBody(s.Body, caller, fset, entityByName, filePath, content, scope)
	relations = append(relations, bodyRels...)
	scope.exitConditional()

	return relations
}

// extractCallsFromRangeStmt extracts calls from a range statement.
func (e *CallGraphExtractor) extractCallsFromRangeStmt(
	s *ast.RangeStmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	// Range expression is outside conditional scope
	if s.X != nil {
		rangeRels := e.extractCallsFromExpr(s.X, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, rangeRels...)
	}

	// Loop body is conditional
	scope.enterConditional()
	bodyRels := e.extractCallsFromFuncBody(s.Body, caller, fset, entityByName, filePath, content, scope)
	relations = append(relations, bodyRels...)
	scope.exitConditional()

	return relations
}

// extractCallsFromDeclStmt extracts calls from declaration statements.
func (e *CallGraphExtractor) extractCallsFromDeclStmt(
	s *ast.DeclStmt,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	genDecl, ok := s.Decl.(*ast.GenDecl)
	if !ok {
		return relations
	}

	for _, spec := range genDecl.Specs {
		if valueSpec, ok := spec.(*ast.ValueSpec); ok {
			for _, value := range valueSpec.Values {
				exprRels := e.extractCallsFromExpr(value, caller, fset, entityByName, filePath, content, scope)
				relations = append(relations, exprRels...)
			}
		}
	}

	return relations
}

// extractCallsFromExpr extracts call relations from an expression.
func (e *CallGraphExtractor) extractCallsFromExpr(
	expr ast.Expr,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	switch x := expr.(type) {
	case *ast.CallExpr:
		// Extract the call itself
		callRels := e.extractSingleCall(x, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, callRels...)
		// Also extract calls in arguments
		for _, arg := range x.Args {
			argRels := e.extractCallsFromExpr(arg, caller, fset, entityByName, filePath, content, scope)
			relations = append(relations, argRels...)
		}
		// Extract calls in the function expression itself (e.g., for method chains)
		funRels := e.extractCallsFromExpr(x.Fun, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, funRels...)
	case *ast.BinaryExpr:
		leftRels := e.extractCallsFromExpr(x.X, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, leftRels...)
		rightRels := e.extractCallsFromExpr(x.Y, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, rightRels...)
	case *ast.UnaryExpr:
		relations = e.extractCallsFromExpr(x.X, caller, fset, entityByName, filePath, content, scope)
	case *ast.ParenExpr:
		relations = e.extractCallsFromExpr(x.X, caller, fset, entityByName, filePath, content, scope)
	case *ast.SelectorExpr:
		relations = e.extractCallsFromExpr(x.X, caller, fset, entityByName, filePath, content, scope)
	case *ast.IndexExpr:
		xRels := e.extractCallsFromExpr(x.X, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, xRels...)
		idxRels := e.extractCallsFromExpr(x.Index, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, idxRels...)
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			eltRels := e.extractCallsFromExpr(elt, caller, fset, entityByName, filePath, content, scope)
			relations = append(relations, eltRels...)
		}
	case *ast.KeyValueExpr:
		valRels := e.extractCallsFromExpr(x.Value, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, valRels...)
	}

	return relations
}

// extractSingleCall extracts a single call relation.
func (e *CallGraphExtractor) extractSingleCall(
	call *ast.CallExpr,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	calleeName, callType := e.extractGoCallee(call, scope.inConditional())
	if calleeName == "" {
		return relations
	}

	callees, found := entityByName[calleeName]
	if !found {
		return relations
	}

	for _, callee := range callees {
		if callee.Kind != knowledge.EntityKindFunction && callee.Kind != knowledge.EntityKindMethod {
			continue
		}

		line := fset.Position(call.Pos()).Line
		rel := e.createCallRelation(caller, callee, callType, filePath, line, content)
		relations = append(relations, rel)
	}

	return relations
}

// extractGoCallee extracts the callee name from a Go call expression.
func (e *CallGraphExtractor) extractGoCallee(call *ast.CallExpr, inConditional bool) (string, CallType) {
	callType := CallTypeDirect
	if inConditional {
		callType = CallTypeConditional
	}

	switch fun := call.Fun.(type) {
	case *ast.Ident:
		// Direct function call: functionName()
		return fun.Name, callType
	case *ast.SelectorExpr:
		// Method call or qualified call: obj.Method() or pkg.Function()
		return fun.Sel.Name, callType
	case *ast.CallExpr:
		// Indirect call: someFunc()()
		return "", CallTypeIndirect
	case *ast.FuncLit:
		// Anonymous function call
		return "", CallTypeIndirect
	default:
		return "", CallTypeIndirect
	}
}

// extractTSCallGraph extracts call relationships from TypeScript/JavaScript using regex.
func (e *CallGraphExtractor) extractTSCallGraph(
	filePath string,
	content []byte,
	funcsInFile []extractors.Entity,
	entityByName map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	return e.extractCallsWithRegex(filePath, content, funcsInFile, entityByName, e.tsCallPattern)
}

// extractPyCallGraph extracts call relationships from Python using regex.
func (e *CallGraphExtractor) extractPyCallGraph(
	filePath string,
	content []byte,
	funcsInFile []extractors.Entity,
	entityByName map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	return e.extractCallsWithRegex(filePath, content, funcsInFile, entityByName, e.pyCallPattern)
}

// extractCallsWithRegex extracts call relationships using regex patterns.
func (e *CallGraphExtractor) extractCallsWithRegex(
	filePath string,
	content []byte,
	funcsInFile []extractors.Entity,
	entityByName map[string][]extractors.Entity,
	pattern *regexp.Regexp,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	source := string(content)
	lines := strings.Split(source, "\n")
	keywords := e.getKeywords()

	for _, caller := range funcsInFile {
		callerRels := e.extractCallsForCaller(caller, lines, entityByName, pattern, keywords, filePath, content)
		relations = append(relations, callerRels...)
	}

	return relations
}

// getKeywords returns keywords to exclude from call detection.
func (e *CallGraphExtractor) getKeywords() map[string]bool {
	return map[string]bool{
		"if": true, "else": true, "for": true, "while": true, "switch": true,
		"case": true, "return": true, "class": true, "function": true, "def": true,
		"import": true, "from": true, "export": true, "const": true, "let": true,
		"var": true, "type": true, "interface": true, "struct": true, "package": true,
		"async": true, "await": true, "try": true, "catch": true, "finally": true,
		"throw": true, "new": true, "typeof": true, "instanceof": true,
		"in": true, "of": true, "range": true, "select": true, "go": true, "defer": true,
		"with": true, "as": true, "pass": true, "break": true, "continue": true,
		"yield": true, "lambda": true, "elif": true, "except": true, "raise": true,
	}
}

// extractCallsForCaller extracts calls for a single caller function.
func (e *CallGraphExtractor) extractCallsForCaller(
	caller extractors.Entity,
	lines []string,
	entityByName map[string][]extractors.Entity,
	pattern *regexp.Regexp,
	keywords map[string]bool,
	filePath string,
	content []byte,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	startIdx := caller.StartLine - 1
	endIdx := caller.EndLine
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > len(lines) {
		endIdx = len(lines)
	}

	// Track conditional depth using brace counting
	scope := &scopeTracker{}
	conditionalBraceStack := []int{} // Stack of brace depths where conditionals started

	for lineIdx := startIdx; lineIdx < endIdx; lineIdx++ {
		line := lines[lineIdx]
		e.updateRegexScope(line, scope, &conditionalBraceStack)
		lineRels := e.extractCallsFromLine(line, lineIdx+1, caller, entityByName, pattern, keywords, filePath, content, scope)
		relations = append(relations, lineRels...)
	}

	return relations
}

// updateRegexScope updates scope tracking based on line content.
func (e *CallGraphExtractor) updateRegexScope(line string, scope *scopeTracker, conditionalBraceStack *[]int) {
	trimmed := strings.TrimSpace(line)
	conditionalKeywords := []string{"if ", "if(", "else ", "else{", "elif ", "switch ", "switch(", "for ", "for(", "while ", "while("}

	// Check for closing braces first (before checking for new conditionals)
	// This handles cases like "} else {" properly
	openBraces := strings.Count(line, "{")
	closeBraces := strings.Count(line, "}")

	// Process closing braces - exit conditional scopes
	for i := 0; i < closeBraces; i++ {
		if len(*conditionalBraceStack) > 0 {
			// Pop from stack and exit conditional
			*conditionalBraceStack = (*conditionalBraceStack)[:len(*conditionalBraceStack)-1]
			scope.exitConditional()
		}
	}

	// Check if line starts a new conditional
	startsConditional := false
	for _, kw := range conditionalKeywords {
		if strings.Contains(trimmed, kw) {
			startsConditional = true
			break
		}
	}

	// Process opening braces - enter conditional scopes if this is a conditional line
	if startsConditional {
		for i := 0; i < openBraces; i++ {
			scope.enterConditional()
			*conditionalBraceStack = append(*conditionalBraceStack, 1)
		}
	}
}

// extractCallsFromLine extracts function calls from a single line.
func (e *CallGraphExtractor) extractCallsFromLine(
	line string,
	lineNum int,
	caller extractors.Entity,
	entityByName map[string][]extractors.Entity,
	pattern *regexp.Regexp,
	keywords map[string]bool,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	matches := pattern.FindAllStringSubmatchIndex(line, -1)
	for _, match := range matches {
		if len(match) >= 4 {
			calleeName := line[match[2]:match[3]]

			if keywords[calleeName] {
				continue
			}

			callees, found := entityByName[calleeName]
			if !found {
				continue
			}

			for _, callee := range callees {
				if callee.Kind != knowledge.EntityKindFunction && callee.Kind != knowledge.EntityKindMethod {
					continue
				}

				if callee.ID == caller.ID {
					continue
				}

				callType := CallTypeDirect
				if scope.inConditional() {
					callType = CallTypeConditional
				}

				rel := e.createCallRelation(caller, callee, callType, filePath, lineNum, content)
				relations = append(relations, rel)
			}
		}
	}

	return relations
}

// createCallRelation creates an ExtractedRelation for a function call.
func (e *CallGraphExtractor) createCallRelation(
	caller, callee extractors.Entity,
	callType CallType,
	filePath string,
	line int,
	content []byte,
) knowledge.ExtractedRelation {
	// Generate stable relation ID (used for deduplication in callers)
	_ = generateRelationID(caller.ID, callee.ID, "calls")

	// Create source entity
	sourceEntity := &knowledge.ExtractedEntity{
		Name:      caller.Name,
		Kind:      caller.Kind,
		FilePath:  caller.FilePath,
		StartLine: caller.StartLine,
		EndLine:   caller.EndLine,
		Signature: caller.Signature,
	}

	// Create target entity
	targetEntity := &knowledge.ExtractedEntity{
		Name:      callee.Name,
		Kind:      callee.Kind,
		FilePath:  callee.FilePath,
		StartLine: callee.StartLine,
		EndLine:   callee.EndLine,
		Signature: callee.Signature,
	}

	// Extract evidence snippet
	lines := strings.Split(string(content), "\n")
	snippet := ""
	if line > 0 && line <= len(lines) {
		snippet = strings.TrimSpace(lines[line-1])
	}

	evidence := []knowledge.EvidenceSpan{
		{
			FilePath:  filePath,
			StartLine: line,
			EndLine:   line,
			Snippet:   snippet,
		},
	}

	// Calculate confidence based on call type
	confidence := 1.0
	if callType == CallTypeConditional {
		confidence = 0.8
	} else if callType == CallTypeIndirect {
		confidence = 0.6
	}

	return knowledge.ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: targetEntity,
		RelationType: knowledge.RelCalls,
		Evidence:     evidence,
		Confidence:   confidence,
		ExtractedAt:  time.Now(),
	}
}

// generateRelationID creates a stable ID for a relation.
func generateRelationID(sourceID, targetID, relationType string) string {
	hash := sha256.Sum256([]byte(sourceID + ":" + targetID + ":" + relationType))
	return hex.EncodeToString(hash[:16])
}
