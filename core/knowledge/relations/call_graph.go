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
	entityByName, funcEntities := e.buildEntityMaps(entities)
	return e.processAllFiles(content, funcEntities, entityByName), nil
}

// buildEntityMaps builds lookup maps from entities.
func (e *CallGraphExtractor) buildEntityMaps(entities []extractors.Entity) (map[string][]extractors.Entity, map[string][]extractors.Entity) {
	entityByName := make(map[string][]extractors.Entity)
	funcEntities := make(map[string][]extractors.Entity)

	for _, entity := range entities {
		entityByName[entity.Name] = append(entityByName[entity.Name], entity)
		if e.isCallableEntity(entity) {
			funcEntities[entity.FilePath] = append(funcEntities[entity.FilePath], entity)
		}
	}

	return entityByName, funcEntities
}

// processAllFiles processes all files and extracts relations.
func (e *CallGraphExtractor) processAllFiles(
	content map[string][]byte,
	funcEntities map[string][]extractors.Entity,
	entityByName map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	for filePath, fileContent := range content {
		fileRelations := e.extractCallsFromFile(filePath, fileContent, funcEntities[filePath], entityByName)
		relations = append(relations, fileRelations...)
	}
	return relations
}

// extractCallsFromFile extracts function calls from a single file.
func (e *CallGraphExtractor) extractCallsFromFile(
	filePath string,
	content []byte,
	funcsInFile []extractors.Entity,
	entityByName map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	if e.isGoFile(filePath) {
		return e.extractGoCallGraph(filePath, content, funcsInFile, entityByName)
	}
	if e.isTSFile(filePath) {
		return e.extractTSCallGraph(filePath, content, funcsInFile, entityByName)
	}
	if e.isPyFile(filePath) {
		return e.extractPyCallGraph(filePath, content, funcsInFile, entityByName)
	}
	return nil
}

// isGoFile checks if file is a Go source file.
func (e *CallGraphExtractor) isGoFile(filePath string) bool {
	return strings.HasSuffix(filePath, ".go")
}

// isTSFile checks if file is a TypeScript/JavaScript source file.
func (e *CallGraphExtractor) isTSFile(filePath string) bool {
	return strings.HasSuffix(filePath, ".ts") || strings.HasSuffix(filePath, ".tsx") ||
		strings.HasSuffix(filePath, ".js") || strings.HasSuffix(filePath, ".jsx")
}

// isPyFile checks if file is a Python source file.
func (e *CallGraphExtractor) isPyFile(filePath string) bool {
	return strings.HasSuffix(filePath, ".py")
}

// extractGoCallGraph extracts call relationships from Go source code using AST.
func (e *CallGraphExtractor) extractGoCallGraph(
	filePath string,
	content []byte,
	funcsInFile []extractors.Entity,
	entityByName map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	if len(content) == 0 {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, 0)
	if err != nil {
		return e.extractCallsWithRegex(filePath, content, funcsInFile, entityByName, e.goCallPattern)
	}

	callerMap := e.buildCallerMap(file, funcsInFile, fset)
	return e.extractCallsFromFuncs(file.Decls, callerMap, fset, entityByName, filePath, content)
}

// buildCallerMap builds a map of function declarations to their entities.
func (e *CallGraphExtractor) buildCallerMap(
	file *ast.File,
	funcsInFile []extractors.Entity,
	fset *token.FileSet,
) map[*ast.FuncDecl]extractors.Entity {
	callerMap := make(map[*ast.FuncDecl]extractors.Entity)
	for _, entity := range funcsInFile {
		e.matchEntityToFunc(file.Decls, entity, fset, callerMap)
	}
	return callerMap
}

// matchEntityToFunc matches an entity to a function declaration.
func (e *CallGraphExtractor) matchEntityToFunc(
	decls []ast.Decl,
	entity extractors.Entity,
	fset *token.FileSet,
	callerMap map[*ast.FuncDecl]extractors.Entity,
) {
	for _, decl := range decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fset.Position(fn.Pos()).Line == entity.StartLine {
			callerMap[fn] = entity
			return
		}
	}
}

// extractCallsFromFuncs extracts calls from all function declarations.
func (e *CallGraphExtractor) extractCallsFromFuncs(
	decls []ast.Decl,
	callerMap map[*ast.FuncDecl]extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	for _, decl := range decls {
		fnRels := e.extractCallsFromDecl(decl, callerMap, fset, entityByName, filePath, content)
		relations = append(relations, fnRels...)
	}
	return relations
}

// extractCallsFromDecl extracts calls from a single declaration.
func (e *CallGraphExtractor) extractCallsFromDecl(
	decl ast.Decl,
	callerMap map[*ast.FuncDecl]extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
) []knowledge.ExtractedRelation {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || fn.Body == nil {
		return nil
	}

	caller, hasCaller := callerMap[fn]
	if !hasCaller {
		return nil
	}

	scope := &scopeTracker{}
	return e.extractCallsFromFuncBody(fn.Body, caller, fset, entityByName, filePath, content, scope)
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
	if rels := e.tryConditionalStmt(stmt, ctx); rels != nil {
		return rels
	}
	if rels := e.tryLoopStmt(stmt, ctx); rels != nil {
		return rels
	}
	return e.dispatchSimpleStmt(stmt, ctx)
}

// tryConditionalStmt handles if/switch/select statements.
func (e *CallGraphExtractor) tryConditionalStmt(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	if rels := e.tryIfSwitch(stmt, ctx); rels != nil {
		return rels
	}
	return e.tryTypeSwitchSelect(stmt, ctx)
}

// tryIfSwitch handles if and switch statements.
func (e *CallGraphExtractor) tryIfSwitch(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		return e.extractCallsFromIfStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.SwitchStmt:
		return e.extractCallsFromSwitchStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	}
	return nil
}

// tryTypeSwitchSelect handles type switch and select statements.
func (e *CallGraphExtractor) tryTypeSwitchSelect(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.TypeSwitchStmt:
		return e.extractCallsFromTypeSwitchStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.SelectStmt:
		return e.extractCallsFromSelectStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	}
	return nil
}

// tryLoopStmt handles for/range/block statements.
func (e *CallGraphExtractor) tryLoopStmt(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.ForStmt:
		return e.extractCallsFromForStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.RangeStmt:
		return e.extractCallsFromRangeStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.BlockStmt:
		return e.extractCallsFromFuncBody(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	}
	return nil
}

// dispatchSimpleStmt handles simpler statement types.
func (e *CallGraphExtractor) dispatchSimpleStmt(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	if rels := e.tryExprStmts(stmt, ctx); rels != nil {
		return rels
	}
	return e.tryClauseStmts(stmt, ctx)
}

// tryExprStmts handles expression-based statements.
func (e *CallGraphExtractor) tryExprStmts(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	if rels := e.tryExprAssign(stmt, ctx); rels != nil {
		return rels
	}
	return e.tryReturnDecl(stmt, ctx)
}

// tryExprAssign handles expression and assignment statements.
func (e *CallGraphExtractor) tryExprAssign(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return e.extractCallsFromExpr(s.X, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.AssignStmt:
		return e.extractCallsFromAssignStmt(s, ctx)
	}
	return nil
}

// tryReturnDecl handles return and declaration statements.
func (e *CallGraphExtractor) tryReturnDecl(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return e.extractCallsFromReturnStmt(s, ctx)
	case *ast.DeclStmt:
		return e.extractCallsFromDeclStmt(s, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	}
	return nil
}

// tryClauseStmts handles defer, go, and clause statements.
func (e *CallGraphExtractor) tryClauseStmts(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	if rels := e.tryDeferGo(stmt, ctx); rels != nil {
		return rels
	}
	return e.tryCaseClauses(stmt, ctx)
}

// tryDeferGo handles defer and go statements.
func (e *CallGraphExtractor) tryDeferGo(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.DeferStmt:
		return e.extractCallsFromExpr(s.Call, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	case *ast.GoStmt:
		return e.extractCallsFromExpr(s.Call, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	}
	return nil
}

// tryCaseClauses handles case and comm clauses.
func (e *CallGraphExtractor) tryCaseClauses(stmt ast.Stmt, ctx *stmtContext) []knowledge.ExtractedRelation {
	switch s := stmt.(type) {
	case *ast.CaseClause:
		return e.extractCallsFromCaseClause(s, ctx)
	case *ast.CommClause:
		return e.extractCallsFromCommClause(s, ctx)
	}
	return nil
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
	genDecl, ok := s.Decl.(*ast.GenDecl)
	if !ok {
		return nil
	}
	return e.extractCallsFromSpecs(genDecl.Specs, caller, fset, entityByName, filePath, content, scope)
}

// extractCallsFromSpecs extracts calls from declaration specs.
func (e *CallGraphExtractor) extractCallsFromSpecs(
	specs []ast.Spec,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	for _, spec := range specs {
		specRels := e.extractCallsFromValueSpec(spec, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, specRels...)
	}
	return relations
}

// extractCallsFromValueSpec extracts calls from a value spec.
func (e *CallGraphExtractor) extractCallsFromValueSpec(
	spec ast.Spec,
	caller extractors.Entity,
	fset *token.FileSet,
	entityByName map[string][]extractors.Entity,
	filePath string,
	content []byte,
	scope *scopeTracker,
) []knowledge.ExtractedRelation {
	valueSpec, ok := spec.(*ast.ValueSpec)
	if !ok {
		return nil
	}

	var relations []knowledge.ExtractedRelation
	for _, value := range valueSpec.Values {
		exprRels := e.extractCallsFromExpr(value, caller, fset, entityByName, filePath, content, scope)
		relations = append(relations, exprRels...)
	}
	return relations
}

// exprContext holds common parameters for expression extraction.
type exprContext struct {
	caller       extractors.Entity
	fset         *token.FileSet
	entityByName map[string][]extractors.Entity
	filePath     string
	content      []byte
	scope        *scopeTracker
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
	ctx := &exprContext{caller, fset, entityByName, filePath, content, scope}
	return e.dispatchExpr(expr, ctx)
}

// dispatchExpr dispatches expression to appropriate handler.
func (e *CallGraphExtractor) dispatchExpr(expr ast.Expr, ctx *exprContext) []knowledge.ExtractedRelation {
	if rels := e.tryCallExpr(expr, ctx); rels != nil {
		return rels
	}
	if rels := e.tryCompoundExpr(expr, ctx); rels != nil {
		return rels
	}
	return e.trySimpleExpr(expr, ctx)
}

// tryCallExpr handles call and binary expressions.
func (e *CallGraphExtractor) tryCallExpr(expr ast.Expr, ctx *exprContext) []knowledge.ExtractedRelation {
	switch x := expr.(type) {
	case *ast.CallExpr:
		return e.extractCallsFromCallExpr(x, ctx)
	case *ast.BinaryExpr:
		return e.extractCallsFromBinaryExpr(x, ctx)
	}
	return nil
}

// tryCompoundExpr handles compound expressions.
func (e *CallGraphExtractor) tryCompoundExpr(expr ast.Expr, ctx *exprContext) []knowledge.ExtractedRelation {
	switch x := expr.(type) {
	case *ast.IndexExpr:
		return e.extractCallsFromIndexExpr(x, ctx)
	case *ast.CompositeLit:
		return e.extractCallsFromCompositeLit(x, ctx)
	}
	return nil
}

// trySimpleExpr handles simple wrapper expressions.
func (e *CallGraphExtractor) trySimpleExpr(expr ast.Expr, ctx *exprContext) []knowledge.ExtractedRelation {
	if rels := e.tryUnaryParen(expr, ctx); rels != nil {
		return rels
	}
	return e.trySelectorKeyValue(expr, ctx)
}

// tryUnaryParen handles unary and parenthesized expressions.
func (e *CallGraphExtractor) tryUnaryParen(expr ast.Expr, ctx *exprContext) []knowledge.ExtractedRelation {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		return e.dispatchExpr(x.X, ctx)
	case *ast.ParenExpr:
		return e.dispatchExpr(x.X, ctx)
	}
	return nil
}

// trySelectorKeyValue handles selector and key-value expressions.
func (e *CallGraphExtractor) trySelectorKeyValue(expr ast.Expr, ctx *exprContext) []knowledge.ExtractedRelation {
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		return e.dispatchExpr(x.X, ctx)
	case *ast.KeyValueExpr:
		return e.dispatchExpr(x.Value, ctx)
	}
	return nil
}

// extractCallsFromCallExpr extracts calls from a call expression.
func (e *CallGraphExtractor) extractCallsFromCallExpr(x *ast.CallExpr, ctx *exprContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	callRels := e.extractSingleCall(x, ctx.caller, ctx.fset, ctx.entityByName, ctx.filePath, ctx.content, ctx.scope)
	relations = append(relations, callRels...)
	for _, arg := range x.Args {
		argRels := e.dispatchExpr(arg, ctx)
		relations = append(relations, argRels...)
	}
	funRels := e.dispatchExpr(x.Fun, ctx)
	relations = append(relations, funRels...)
	return relations
}

// extractCallsFromBinaryExpr extracts calls from a binary expression.
func (e *CallGraphExtractor) extractCallsFromBinaryExpr(x *ast.BinaryExpr, ctx *exprContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	leftRels := e.dispatchExpr(x.X, ctx)
	relations = append(relations, leftRels...)
	rightRels := e.dispatchExpr(x.Y, ctx)
	relations = append(relations, rightRels...)
	return relations
}

// extractCallsFromIndexExpr extracts calls from an index expression.
func (e *CallGraphExtractor) extractCallsFromIndexExpr(x *ast.IndexExpr, ctx *exprContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	xRels := e.dispatchExpr(x.X, ctx)
	relations = append(relations, xRels...)
	idxRels := e.dispatchExpr(x.Index, ctx)
	relations = append(relations, idxRels...)
	return relations
}

// extractCallsFromCompositeLit extracts calls from a composite literal.
func (e *CallGraphExtractor) extractCallsFromCompositeLit(x *ast.CompositeLit, ctx *exprContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	for _, elt := range x.Elts {
		eltRels := e.dispatchExpr(elt, ctx)
		relations = append(relations, eltRels...)
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
	calleeName, callType := e.extractGoCallee(call, scope.inConditional())
	if calleeName == "" {
		return nil
	}

	callees, found := entityByName[calleeName]
	if !found {
		return nil
	}

	line := fset.Position(call.Pos()).Line
	return e.createCallRelationsForCallees(callees, caller, callType, filePath, line, content)
}

// createCallRelationsForCallees creates call relations for all matching callees.
func (e *CallGraphExtractor) createCallRelationsForCallees(
	callees []extractors.Entity,
	caller extractors.Entity,
	callType CallType,
	filePath string,
	line int,
	content []byte,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	for _, callee := range callees {
		if !e.isCallableEntity(callee) {
			continue
		}
		rel := e.createCallRelation(caller, callee, callType, filePath, line, content)
		relations = append(relations, rel)
	}
	return relations
}

// isCallableEntity checks if an entity is a function or method.
func (e *CallGraphExtractor) isCallableEntity(entity extractors.Entity) bool {
	return entity.Kind == knowledge.EntityKindFunction || entity.Kind == knowledge.EntityKindMethod
}

// extractGoCallee extracts the callee name from a Go call expression.
func (e *CallGraphExtractor) extractGoCallee(call *ast.CallExpr, inConditional bool) (string, CallType) {
	callType := e.getCallType(inConditional)
	return e.getCalleeName(call.Fun, callType)
}

// getCallType returns the call type based on conditional context.
func (e *CallGraphExtractor) getCallType(inConditional bool) CallType {
	if inConditional {
		return CallTypeConditional
	}
	return CallTypeDirect
}

// getCalleeName extracts the callee name from a function expression.
func (e *CallGraphExtractor) getCalleeName(fun ast.Expr, callType CallType) (string, CallType) {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name, callType
	case *ast.SelectorExpr:
		return f.Sel.Name, callType
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
	openBraces := strings.Count(line, "{")
	closeBraces := strings.Count(line, "}")

	e.processClosingBraces(closeBraces, scope, conditionalBraceStack)
	e.processOpeningBraces(line, openBraces, scope, conditionalBraceStack)
}

// processClosingBraces exits conditional scopes for closing braces.
func (e *CallGraphExtractor) processClosingBraces(closeBraces int, scope *scopeTracker, conditionalBraceStack *[]int) {
	for i := 0; i < closeBraces; i++ {
		if len(*conditionalBraceStack) > 0 {
			*conditionalBraceStack = (*conditionalBraceStack)[:len(*conditionalBraceStack)-1]
			scope.exitConditional()
		}
	}
}

// processOpeningBraces enters conditional scopes for opening braces on conditional lines.
func (e *CallGraphExtractor) processOpeningBraces(line string, openBraces int, scope *scopeTracker, conditionalBraceStack *[]int) {
	if !e.isConditionalLine(line) {
		return
	}
	for i := 0; i < openBraces; i++ {
		scope.enterConditional()
		*conditionalBraceStack = append(*conditionalBraceStack, 1)
	}
}

// isConditionalLine checks if a line starts a conditional block.
func (e *CallGraphExtractor) isConditionalLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	conditionalKeywords := []string{"if ", "if(", "else ", "else{", "elif ", "switch ", "switch(", "for ", "for(", "while ", "while("}
	for _, kw := range conditionalKeywords {
		if strings.Contains(trimmed, kw) {
			return true
		}
	}
	return false
}

// regexLineContext holds context for regex line extraction.
type regexLineContext struct {
	line         string
	lineNum      int
	caller       extractors.Entity
	entityByName map[string][]extractors.Entity
	keywords     map[string]bool
	filePath     string
	content      []byte
	scope        *scopeTracker
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
	ctx := &regexLineContext{line, lineNum, caller, entityByName, keywords, filePath, content, scope}
	return e.extractMatchesFromLine(pattern, ctx)
}

// extractMatchesFromLine extracts calls from pattern matches.
func (e *CallGraphExtractor) extractMatchesFromLine(pattern *regexp.Regexp, ctx *regexLineContext) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	matches := pattern.FindAllStringSubmatchIndex(ctx.line, -1)
	for _, match := range matches {
		matchRels := e.processMatch(match, ctx)
		relations = append(relations, matchRels...)
	}
	return relations
}

// processMatch processes a single regex match.
func (e *CallGraphExtractor) processMatch(match []int, ctx *regexLineContext) []knowledge.ExtractedRelation {
	if len(match) < 4 {
		return nil
	}
	calleeName := ctx.line[match[2]:match[3]]
	if ctx.keywords[calleeName] {
		return nil
	}
	return e.findMatchingCallees(calleeName, ctx)
}

// findMatchingCallees finds and creates relations for matching callees.
func (e *CallGraphExtractor) findMatchingCallees(calleeName string, ctx *regexLineContext) []knowledge.ExtractedRelation {
	callees, found := ctx.entityByName[calleeName]
	if !found {
		return nil
	}

	var relations []knowledge.ExtractedRelation
	for _, callee := range callees {
		rel := e.tryCreateCallRelation(callee, ctx)
		if rel != nil {
			relations = append(relations, *rel)
		}
	}
	return relations
}

// tryCreateCallRelation creates a call relation if the callee is valid.
func (e *CallGraphExtractor) tryCreateCallRelation(callee extractors.Entity, ctx *regexLineContext) *knowledge.ExtractedRelation {
	if !e.isValidCallee(callee, ctx.caller) {
		return nil
	}

	callType := e.getCallType(ctx.scope.inConditional())
	rel := e.createCallRelation(ctx.caller, callee, callType, ctx.filePath, ctx.lineNum, ctx.content)
	return &rel
}

// isValidCallee checks if a callee is valid for creating a call relation.
func (e *CallGraphExtractor) isValidCallee(callee, caller extractors.Entity) bool {
	if !e.isCallableEntity(callee) {
		return false
	}
	return callee.ID != caller.ID
}

// createCallRelation creates an ExtractedRelation for a function call.
func (e *CallGraphExtractor) createCallRelation(
	caller, callee extractors.Entity,
	callType CallType,
	filePath string,
	line int,
	content []byte,
) knowledge.ExtractedRelation {
	_ = generateRelationID(caller.ID, callee.ID, "calls")

	return knowledge.ExtractedRelation{
		SourceEntity: e.entityToExtracted(caller),
		TargetEntity: e.entityToExtracted(callee),
		RelationType: knowledge.RelCalls,
		Evidence:     e.createEvidence(filePath, line, content),
		Confidence:   e.getConfidence(callType),
		ExtractedAt:  time.Now(),
	}
}

// entityToExtracted converts an Entity to an ExtractedEntity.
func (e *CallGraphExtractor) entityToExtracted(entity extractors.Entity) *knowledge.ExtractedEntity {
	return &knowledge.ExtractedEntity{
		Name:      entity.Name,
		Kind:      entity.Kind,
		FilePath:  entity.FilePath,
		StartLine: entity.StartLine,
		EndLine:   entity.EndLine,
		Signature: entity.Signature,
	}
}

// createEvidence creates evidence span for a call relation.
func (e *CallGraphExtractor) createEvidence(filePath string, line int, content []byte) []knowledge.EvidenceSpan {
	snippet := e.extractSnippet(content, line)
	return []knowledge.EvidenceSpan{{
		FilePath:  filePath,
		StartLine: line,
		EndLine:   line,
		Snippet:   snippet,
	}}
}

// extractSnippet extracts the code snippet at the given line.
func (e *CallGraphExtractor) extractSnippet(content []byte, line int) string {
	lines := strings.Split(string(content), "\n")
	if line > 0 && line <= len(lines) {
		return strings.TrimSpace(lines[line-1])
	}
	return ""
}

// getConfidence returns confidence based on call type.
func (e *CallGraphExtractor) getConfidence(callType CallType) float64 {
	switch callType {
	case CallTypeConditional:
		return 0.8
	case CallTypeIndirect:
		return 0.6
	default:
		return 1.0
	}
}

// generateRelationID creates a stable ID for a relation.
func generateRelationID(sourceID, targetID, relationType string) string {
	hash := sha256.Sum256([]byte(sourceID + ":" + targetID + ":" + relationType))
	return hex.EncodeToString(hash[:16])
}
