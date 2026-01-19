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

		// Track if we're inside a conditional
		var inConditional bool

		// Walk the function body
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				inConditional = true
			case *ast.CallExpr:
				calleeName, callType := e.extractGoCallee(node, inConditional)
				if calleeName == "" {
					return true
				}

				// Find matching callee entities
				callees, found := entityByName[calleeName]
				if !found {
					return true
				}

				for _, callee := range callees {
					if callee.Kind != knowledge.EntityKindFunction && callee.Kind != knowledge.EntityKindMethod {
						continue
					}

					line := fset.Position(node.Pos()).Line
					rel := e.createCallRelation(caller, callee, callType, filePath, line, content)
					relations = append(relations, rel)
				}
			}
			return true
		})
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

	// Keywords to exclude from call detection
	keywords := map[string]bool{
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

	// For each function in the file, find calls within its body
	for _, caller := range funcsInFile {
		// Get the lines within this function
		startIdx := caller.StartLine - 1
		endIdx := caller.EndLine
		if startIdx < 0 {
			startIdx = 0
		}
		if endIdx > len(lines) {
			endIdx = len(lines)
		}

		// Track if we're in a conditional
		inConditional := false
		conditionalKeywords := []string{"if ", "if(", "else ", "else{", "elif ", "switch ", "switch("}

		for lineIdx := startIdx; lineIdx < endIdx; lineIdx++ {
			line := lines[lineIdx]

			// Check if line contains conditional
			for _, kw := range conditionalKeywords {
				if strings.Contains(line, kw) {
					inConditional = true
					break
				}
			}

			// Find all function calls in this line
			matches := pattern.FindAllStringSubmatchIndex(line, -1)
			for _, match := range matches {
				if len(match) >= 4 {
					calleeName := line[match[2]:match[3]]

					// Skip keywords
					if keywords[calleeName] {
						continue
					}

					// Find matching callee entities
					callees, found := entityByName[calleeName]
					if !found {
						continue
					}

					for _, callee := range callees {
						if callee.Kind != knowledge.EntityKindFunction && callee.Kind != knowledge.EntityKindMethod {
							continue
						}

						// Skip self-reference in same file and same entity
						if callee.ID == caller.ID {
							continue
						}

						callType := CallTypeDirect
						if inConditional {
							callType = CallTypeConditional
						}

						rel := e.createCallRelation(caller, callee, callType, filePath, lineIdx+1, content)
						relations = append(relations, rel)
					}
				}
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
