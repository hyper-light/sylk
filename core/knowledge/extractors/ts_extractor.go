package extractors

import (
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/knowledge"
)

// TypeScriptExtractor extracts entities from TypeScript/JavaScript source files using regex patterns.
type TypeScriptExtractor struct {
	// Compiled regex patterns for different entity types
	functionPattern   *regexp.Regexp
	arrowFuncPattern  *regexp.Regexp
	classPattern      *regexp.Regexp
	methodPattern     *regexp.Regexp
	interfacePattern  *regexp.Regexp
	typeAliasPattern  *regexp.Regexp
	exportFuncPattern *regexp.Regexp
	asyncFuncPattern  *regexp.Regexp
}

// NewTypeScriptExtractor creates a new TypeScript entity extractor.
func NewTypeScriptExtractor() *TypeScriptExtractor {
	return &TypeScriptExtractor{
		// Regular function declarations: function name(...) or export function name(...)
		functionPattern: regexp.MustCompile(`(?m)^[\t ]*(export\s+)?(async\s+)?function\s+(\w+)\s*(<[^>]*>)?\s*\(([^)]*)\)(\s*:\s*[^{;]+)?`),

		// Arrow functions assigned to const/let/var: const name = (...) => or const name = async (...) =>
		arrowFuncPattern: regexp.MustCompile(`(?m)^[\t ]*(export\s+)?(const|let|var)\s+(\w+)\s*(<[^>]*>)?\s*=\s*(async\s+)?\(?([^)]*)\)?\s*:\s*[^=]*=>`),

		// Class declarations: class Name or export class Name
		classPattern: regexp.MustCompile(`(?m)^[\t ]*(export\s+)?(abstract\s+)?class\s+(\w+)(\s+extends\s+\w+)?(\s+implements\s+[\w,\s]+)?\s*\{`),

		// Method declarations inside classes (including async, static, private, etc.)
		methodPattern: regexp.MustCompile(`(?m)^[\t ]*(public|private|protected)?\s*(static\s+)?(async\s+)?(\w+)\s*(<[^>]*>)?\s*\(([^)]*)\)(\s*:\s*[^{;]+)?\s*\{`),

		// Interface declarations: interface Name or export interface Name
		interfacePattern: regexp.MustCompile(`(?m)^[\t ]*(export\s+)?interface\s+(\w+)(<[^>]*>)?(\s+extends\s+[\w,\s<>]+)?\s*\{`),

		// Type alias declarations: type Name = or export type Name =
		typeAliasPattern: regexp.MustCompile(`(?m)^[\t ]*(export\s+)?type\s+(\w+)(<[^>]*>)?\s*=`),

		// Export function pattern for default exports
		exportFuncPattern: regexp.MustCompile(`(?m)^[\t ]*export\s+default\s+(async\s+)?function\s+(\w+)?\s*\(([^)]*)\)`),

		// Async function pattern
		asyncFuncPattern: regexp.MustCompile(`(?m)^[\t ]*(export\s+)?async\s+function\s+(\w+)\s*(<[^>]*>)?\s*\(([^)]*)\)(\s*:\s*[^{;]+)?`),
	}
}

// Extract parses TypeScript source code and extracts functions, classes, interfaces, and types.
func (e *TypeScriptExtractor) Extract(filePath string, content []byte) ([]Entity, error) {
	if len(content) == 0 {
		return []Entity{}, nil
	}

	source := string(content)
	lines := strings.Split(source, "\n")
	var entities []Entity

	// Extract functions
	entities = append(entities, e.extractFunctions(filePath, source, lines)...)

	// Extract arrow functions
	entities = append(entities, e.extractArrowFunctions(filePath, source, lines)...)

	// Extract classes and their methods
	entities = append(entities, e.extractClasses(filePath, source, lines)...)

	// Extract interfaces
	entities = append(entities, e.extractInterfaces(filePath, source, lines)...)

	// Extract type aliases
	entities = append(entities, e.extractTypeAliases(filePath, source, lines)...)

	return entities, nil
}

// extractFunctions extracts regular function declarations.
func (e *TypeScriptExtractor) extractFunctions(filePath, source string, lines []string) []Entity {
	var entities []Entity
	matches := e.functionPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 8 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)
			endLine := e.findBlockEnd(lines, startLine)

			// Extract function name (group 3)
			name := source[match[6]:match[7]]

			// Build signature
			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			if match[4] != -1 && match[5] != -1 {
				sig.WriteString("async ")
			}
			sig.WriteString("function ")
			sig.WriteString(name)

			// Add generics if present
			if match[8] != -1 && match[9] != -1 {
				sig.WriteString(source[match[8]:match[9]])
			}

			// Add parameters
			sig.WriteString("(")
			if match[10] != -1 && match[11] != -1 {
				sig.WriteString(strings.TrimSpace(source[match[10]:match[11]]))
			}
			sig.WriteString(")")

			// Add return type if present
			if match[12] != -1 && match[13] != -1 {
				sig.WriteString(strings.TrimSpace(source[match[12]:match[13]]))
			}

			entity := Entity{
				ID:        generateEntityID(filePath, "func:"+name),
				Name:      name,
				Kind:      knowledge.EntityKindFunction,
				FilePath:  filePath,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig.String(),
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

// extractArrowFunctions extracts arrow function declarations.
func (e *TypeScriptExtractor) extractArrowFunctions(filePath, source string, lines []string) []Entity {
	var entities []Entity
	matches := e.arrowFuncPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 14 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)
			endLine := e.findBlockEnd(lines, startLine)

			// Extract function name (group 3)
			name := source[match[6]:match[7]]

			// Build signature
			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			sig.WriteString("const ")
			sig.WriteString(name)
			sig.WriteString(" = ")
			if match[10] != -1 && match[11] != -1 {
				sig.WriteString("async ")
			}
			sig.WriteString("(")
			if match[12] != -1 && match[13] != -1 {
				sig.WriteString(strings.TrimSpace(source[match[12]:match[13]]))
			}
			sig.WriteString(") => ...")

			entity := Entity{
				ID:        generateEntityID(filePath, "func:"+name),
				Name:      name,
				Kind:      knowledge.EntityKindFunction,
				FilePath:  filePath,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig.String(),
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

// extractClasses extracts class declarations and their methods.
func (e *TypeScriptExtractor) extractClasses(filePath, source string, lines []string) []Entity {
	var entities []Entity
	matches := e.classPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 8 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)
			endLine := e.findBlockEnd(lines, startLine)

			// Extract class name (group 3)
			name := source[match[6]:match[7]]

			// Build signature
			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			if match[4] != -1 && match[5] != -1 {
				sig.WriteString("abstract ")
			}
			sig.WriteString("class ")
			sig.WriteString(name)
			if match[8] != -1 && match[9] != -1 {
				sig.WriteString(strings.TrimSpace(source[match[8]:match[9]]))
			}
			if match[10] != -1 && match[11] != -1 {
				sig.WriteString(strings.TrimSpace(source[match[10]:match[11]]))
			}

			classEntity := Entity{
				ID:        generateEntityID(filePath, "class:"+name),
				Name:      name,
				Kind:      knowledge.EntityKindType,
				FilePath:  filePath,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig.String(),
			}
			entities = append(entities, classEntity)

			// Extract methods within the class
			classBody := e.extractClassBody(lines, startLine, endLine)
			methods := e.extractMethods(filePath, classBody, name, classEntity.ID, startLine)
			entities = append(entities, methods...)
		}
	}

	return entities
}

// extractMethods extracts method declarations from a class body.
func (e *TypeScriptExtractor) extractMethods(filePath, classBody, className, parentID string, classStartLine int) []Entity {
	var entities []Entity
	bodyLines := strings.Split(classBody, "\n")
	matches := e.methodPattern.FindAllStringSubmatchIndex(classBody, -1)

	for _, match := range matches {
		if len(match) >= 16 {
			startPos := match[0]
			methodStartLine := e.posToLine(classBody, startPos) + classStartLine

			// Extract method name (group 4)
			name := classBody[match[8]:match[9]]

			// Skip constructor - it's a special method
			if name == "constructor" {
				continue
			}

			// Find method end
			methodEndLine := e.findBlockEnd(bodyLines, e.posToLine(classBody, startPos))
			if methodEndLine > 0 {
				methodEndLine += classStartLine
			} else {
				methodEndLine = methodStartLine
			}

			// Build signature
			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString(classBody[match[2]:match[3]])
				sig.WriteString(" ")
			}
			if match[4] != -1 && match[5] != -1 {
				sig.WriteString("static ")
			}
			if match[6] != -1 && match[7] != -1 {
				sig.WriteString("async ")
			}
			sig.WriteString(name)

			// Add generics if present
			if match[10] != -1 && match[11] != -1 {
				sig.WriteString(classBody[match[10]:match[11]])
			}

			sig.WriteString("(")
			if match[12] != -1 && match[13] != -1 {
				sig.WriteString(strings.TrimSpace(classBody[match[12]:match[13]]))
			}
			sig.WriteString(")")

			// Add return type if present
			if match[14] != -1 && match[15] != -1 {
				sig.WriteString(strings.TrimSpace(classBody[match[14]:match[15]]))
			}

			entity := Entity{
				ID:        generateEntityID(filePath, className+"."+name),
				Name:      name,
				Kind:      knowledge.EntityKindMethod,
				FilePath:  filePath,
				StartLine: methodStartLine,
				EndLine:   methodEndLine,
				Signature: sig.String(),
				ParentID:  parentID,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

// extractInterfaces extracts interface declarations.
func (e *TypeScriptExtractor) extractInterfaces(filePath, source string, lines []string) []Entity {
	var entities []Entity
	matches := e.interfacePattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)
			endLine := e.findBlockEnd(lines, startLine)

			// Extract interface name (group 2)
			name := source[match[4]:match[5]]

			// Build signature
			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			sig.WriteString("interface ")
			sig.WriteString(name)

			// Add generics if present
			if match[6] != -1 && match[7] != -1 {
				sig.WriteString(source[match[6]:match[7]])
			}

			// Add extends if present
			if match[8] != -1 && match[9] != -1 {
				sig.WriteString(strings.TrimSpace(source[match[8]:match[9]]))
			}

			entity := Entity{
				ID:        generateEntityID(filePath, "interface:"+name),
				Name:      name,
				Kind:      knowledge.EntityKindInterface,
				FilePath:  filePath,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig.String(),
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

// extractTypeAliases extracts type alias declarations.
func (e *TypeScriptExtractor) extractTypeAliases(filePath, source string, lines []string) []Entity {
	var entities []Entity
	matches := e.typeAliasPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)
			// Type aliases typically end at the same line or the next semicolon
			endLine := e.findStatementEnd(lines, startLine)

			// Extract type name (group 2)
			name := source[match[4]:match[5]]

			// Build signature
			var sig strings.Builder
			if match[2] != -1 && match[3] != -1 {
				sig.WriteString("export ")
			}
			sig.WriteString("type ")
			sig.WriteString(name)

			// Add generics if present
			if match[6] != -1 && match[7] != -1 {
				sig.WriteString(source[match[6]:match[7]])
			}
			sig.WriteString(" = ...")

			entity := Entity{
				ID:        generateEntityID(filePath, "type:"+name),
				Name:      name,
				Kind:      knowledge.EntityKindType,
				FilePath:  filePath,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig.String(),
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

// posToLine converts a byte position to a line number (1-indexed).
func (e *TypeScriptExtractor) posToLine(source string, pos int) int {
	return strings.Count(source[:pos], "\n") + 1
}

// findBlockEnd finds the line where a block (started with {) ends.
// It properly handles strings, comments, and template literals.
func (e *TypeScriptExtractor) findBlockEnd(lines []string, startLine int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}

	lexer := newTSLexer()
	braceCount := 0
	started := false

	for i := startLine - 1; i < len(lines); i++ {
		line := lines[i]
		endLine, done := e.processLineForBraces(line, i, lexer, &braceCount, &started)
		if done {
			return endLine
		}
		lexer.resetForNewLine()
	}

	return startLine
}

// processLineForBraces processes a single line for brace counting.
// Returns (line number, true) if block end is found, (0, false) otherwise.
func (e *TypeScriptExtractor) processLineForBraces(line string, lineIdx int, lexer *tsLexer, braceCount *int, started *bool) (int, bool) {
	runes := []rune(line)
	for j := 0; j < len(runes); j++ {
		ch := runes[j]
		next := e.peekNextRune(runes, j)

		lexer.processChar(ch, next)

		if !lexer.isInCodeContext() {
			continue
		}

		if ch == '{' {
			*braceCount++
			*started = true
		} else if ch == '}' {
			*braceCount--
			if *started && *braceCount == 0 {
				return lineIdx + 1, true
			}
		}
	}
	return 0, false
}

// peekNextRune returns the next rune in the slice, or 0 if at end.
func (e *TypeScriptExtractor) peekNextRune(runes []rune, idx int) rune {
	if idx+1 < len(runes) {
		return runes[idx+1]
	}
	return 0
}

// findStatementEnd finds the line where a statement ends (typically with ; or end of object literal).
// It properly handles strings, comments, and template literals.
func (e *TypeScriptExtractor) findStatementEnd(lines []string, startLine int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}

	lexer := newTSLexer()
	braceCount := 0
	parenCount := 0

	for i := startLine - 1; i < len(lines); i++ {
		line := lines[i]
		endLine, done := e.processLineForStatement(line, i, startLine, lexer, &braceCount, &parenCount)
		if done {
			return endLine
		}
		lexer.resetForNewLine()
	}

	return startLine
}

// processLineForStatement processes a single line for statement end detection.
// Returns (line number, true) if statement end is found, (0, false) otherwise.
func (e *TypeScriptExtractor) processLineForStatement(line string, lineIdx, startLine int, lexer *tsLexer, braceCount, parenCount *int) (int, bool) {
	runes := []rune(line)
	for j := 0; j < len(runes); j++ {
		ch := runes[j]
		next := e.peekNextRune(runes, j)

		lexer.processChar(ch, next)

		if !lexer.isInCodeContext() {
			continue
		}

		switch ch {
		case '{':
			*braceCount++
		case '}':
			*braceCount--
		case '(':
			*parenCount++
		case ')':
			*parenCount--
		case ';':
			if *braceCount == 0 && *parenCount == 0 {
				return lineIdx + 1, true
			}
		}
	}

	return e.checkStatementEndByLine(line, lineIdx, startLine, *braceCount, *parenCount)
}

// checkStatementEndByLine checks if the current line ends a statement.
func (e *TypeScriptExtractor) checkStatementEndByLine(line string, lineIdx, startLine, braceCount, parenCount int) (int, bool) {
	if braceCount == 0 && parenCount == 0 && lineIdx > startLine-1 {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, ";") || strings.HasSuffix(trimmed, "}") {
			return lineIdx + 1, true
		}
	}
	return 0, false
}

// extractClassBody extracts the body of a class between startLine and endLine.
func (e *TypeScriptExtractor) extractClassBody(lines []string, startLine, endLine int) string {
	if startLine < 1 || endLine > len(lines) || startLine > endLine {
		return ""
	}

	var sb strings.Builder
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}
	return sb.String()
}

// tsLexerState tracks the current lexical context during parsing.
type tsLexerState int

const (
	tsStateNormal tsLexerState = iota
	tsStateSingleQuoteString
	tsStateDoubleQuoteString
	tsStateTemplateLiteral
	tsStateSingleLineComment
	tsStateMultiLineComment
	tsStateRegex
)

// tsLexer provides character-by-character lexical analysis for TypeScript.
type tsLexer struct {
	state   tsLexerState
	escaped bool
}

// newTSLexer creates a new TypeScript lexer in normal state.
func newTSLexer() *tsLexer {
	return &tsLexer{state: tsStateNormal}
}

// isInCodeContext returns true if we're in normal code (not string/comment/regex).
func (l *tsLexer) isInCodeContext() bool {
	return l.state == tsStateNormal
}

// processChar advances the lexer state based on the current and next character.
// Returns whether the character was consumed by a state transition.
func (l *tsLexer) processChar(ch, next rune) {
	if l.escaped {
		l.escaped = false
		return
	}

	switch l.state {
	case tsStateNormal:
		l.processNormalState(ch, next)
	case tsStateSingleQuoteString:
		l.processSingleQuoteState(ch)
	case tsStateDoubleQuoteString:
		l.processDoubleQuoteState(ch)
	case tsStateTemplateLiteral:
		l.processTemplateLiteralState(ch)
	case tsStateMultiLineComment:
		l.processMultiLineCommentState(ch, next)
	case tsStateRegex:
		l.processRegexState(ch)
	}
}

// processNormalState handles state transitions from normal code context.
func (l *tsLexer) processNormalState(ch, next rune) {
	switch ch {
	case '\'':
		l.state = tsStateSingleQuoteString
	case '"':
		l.state = tsStateDoubleQuoteString
	case '`':
		l.state = tsStateTemplateLiteral
	case '/':
		if next == '/' {
			l.state = tsStateSingleLineComment
		} else if next == '*' {
			l.state = tsStateMultiLineComment
		}
	}
}

// processSingleQuoteState handles characters inside single-quoted strings.
func (l *tsLexer) processSingleQuoteState(ch rune) {
	switch ch {
	case '\\':
		l.escaped = true
	case '\'':
		l.state = tsStateNormal
	}
}

// processDoubleQuoteState handles characters inside double-quoted strings.
func (l *tsLexer) processDoubleQuoteState(ch rune) {
	switch ch {
	case '\\':
		l.escaped = true
	case '"':
		l.state = tsStateNormal
	}
}

// processTemplateLiteralState handles characters inside template literals.
func (l *tsLexer) processTemplateLiteralState(ch rune) {
	switch ch {
	case '\\':
		l.escaped = true
	case '`':
		l.state = tsStateNormal
	}
}

// processMultiLineCommentState handles characters inside multi-line comments.
func (l *tsLexer) processMultiLineCommentState(ch, next rune) {
	if ch == '*' && next == '/' {
		l.state = tsStateNormal
	}
}

// processRegexState handles characters inside regular expressions.
func (l *tsLexer) processRegexState(ch rune) {
	switch ch {
	case '\\':
		l.escaped = true
	case '/':
		l.state = tsStateNormal
	}
}

// resetForNewLine resets state that doesn't carry across lines.
func (l *tsLexer) resetForNewLine() {
	if l.state == tsStateSingleLineComment {
		l.state = tsStateNormal
	}
	l.escaped = false
}
