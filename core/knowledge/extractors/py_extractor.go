package extractors

import (
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/knowledge"
)

// PythonExtractor extracts entities from Python source files using regex patterns.
type PythonExtractor struct {
	// Compiled regex patterns for different entity types
	functionPattern     *regexp.Regexp
	asyncFunctionPattern *regexp.Regexp
	classPattern        *regexp.Regexp
	methodPattern       *regexp.Regexp
	asyncMethodPattern  *regexp.Regexp
	decoratorPattern    *regexp.Regexp
}

// NewPythonExtractor creates a new Python entity extractor.
func NewPythonExtractor() *PythonExtractor {
	return &PythonExtractor{
		// Function declarations: def name(...):
		functionPattern: regexp.MustCompile(`(?m)^([\t ]*)def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),

		// Async function declarations: async def name(...):
		asyncFunctionPattern: regexp.MustCompile(`(?m)^([\t ]*)async\s+def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),

		// Class declarations: class Name or class Name(Base):
		classPattern: regexp.MustCompile(`(?m)^([\t ]*)class\s+(\w+)(\s*\([^)]*\))?\s*:`),

		// Method declarations (indented def inside class)
		methodPattern: regexp.MustCompile(`(?m)^([\t ]+)def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),

		// Async method declarations
		asyncMethodPattern: regexp.MustCompile(`(?m)^([\t ]+)async\s+def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),

		// Decorator pattern to detect decorated functions
		decoratorPattern: regexp.MustCompile(`(?m)^([\t ]*)@[\w.]+(\([^)]*\))?\s*$`),
	}
}

// Extract parses Python source code and extracts functions, classes, and methods.
func (e *PythonExtractor) Extract(filePath string, content []byte) ([]Entity, error) {
	if len(content) == 0 {
		return []Entity{}, nil
	}

	source := string(content)
	lines := strings.Split(source, "\n")
	var entities []Entity

	// First pass: extract classes
	classEntities := e.extractClasses(filePath, source, lines)
	entities = append(entities, classEntities...)

	// Build a map of class ranges for method association
	classRanges := make(map[string]struct {
		id        string
		startLine int
		endLine   int
		indent    int
	})

	for _, ce := range classEntities {
		indent := e.getIndentLevel(lines[ce.StartLine-1])
		classRanges[ce.Name] = struct {
			id        string
			startLine int
			endLine   int
			indent    int
		}{
			id:        ce.ID,
			startLine: ce.StartLine,
			endLine:   ce.EndLine,
			indent:    indent,
		}
	}

	// Extract functions (top-level only) and methods (within classes)
	entities = append(entities, e.extractFunctionsAndMethods(filePath, source, lines, classRanges)...)

	return entities, nil
}

// extractClasses extracts class declarations.
func (e *PythonExtractor) extractClasses(filePath, source string, lines []string) []Entity {
	var entities []Entity
	matches := e.classPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)

			// Get indentation
			indent := ""
			if match[2] != -1 && match[3] != -1 {
				indent = source[match[2]:match[3]]
			}

			// Extract class name (group 2)
			name := source[match[4]:match[5]]

			// Find the end of the class (based on indentation)
			endLine := e.findPythonBlockEnd(lines, startLine, len(indent))

			// Build signature
			var sig strings.Builder
			sig.WriteString("class ")
			sig.WriteString(name)
			if match[6] != -1 && match[7] != -1 {
				sig.WriteString(source[match[6]:match[7]])
			}

			entity := Entity{
				ID:        generateEntityID(filePath, "class:"+name),
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

// extractFunctionsAndMethods extracts function and method declarations.
func (e *PythonExtractor) extractFunctionsAndMethods(filePath, source string, lines []string, classRanges map[string]struct {
	id        string
	startLine int
	endLine   int
	indent    int
}) []Entity {
	var entities []Entity

	// Extract regular functions and methods
	entities = append(entities, e.extractFuncsByPattern(filePath, source, lines, classRanges, e.functionPattern, false)...)

	// Extract async functions and methods
	entities = append(entities, e.extractFuncsByPattern(filePath, source, lines, classRanges, e.asyncFunctionPattern, true)...)

	return entities
}

// extractFuncsByPattern extracts functions matching the given pattern.
func (e *PythonExtractor) extractFuncsByPattern(filePath, source string, lines []string, classRanges map[string]struct {
	id        string
	startLine int
	endLine   int
	indent    int
}, pattern *regexp.Regexp, isAsync bool) []Entity {
	var entities []Entity
	matches := pattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)

			// Get indentation
			indent := ""
			if match[2] != -1 && match[3] != -1 {
				indent = source[match[2]:match[3]]
			}
			indentLevel := len(indent)

			// Extract function name (group 2)
			name := source[match[4]:match[5]]

			// Find the end of the function (based on indentation)
			endLine := e.findPythonBlockEnd(lines, startLine, indentLevel)

			// Build signature
			var sig strings.Builder
			if isAsync {
				sig.WriteString("async ")
			}
			sig.WriteString("def ")
			sig.WriteString(name)
			sig.WriteString("(")
			if match[6] != -1 && match[7] != -1 {
				params := strings.TrimSpace(source[match[6]:match[7]])
				sig.WriteString(params)
			}
			sig.WriteString(")")
			if match[8] != -1 && match[9] != -1 {
				sig.WriteString(source[match[8]:match[9]])
			}

			// Determine if this is a method (inside a class) or a top-level function
			var parentID string
			var kind knowledge.EntityKind
			var entityPath string

			// Check if this function is inside any class
			parentClass := ""
			for className, classInfo := range classRanges {
				// Method must be:
				// 1. Within the class line range
				// 2. Have greater indentation than the class definition
				if startLine > classInfo.startLine && startLine <= classInfo.endLine && indentLevel > classInfo.indent {
					parentClass = className
					parentID = classInfo.id
					break
				}
			}

			if parentClass != "" {
				kind = knowledge.EntityKindMethod
				entityPath = parentClass + "." + name
			} else {
				kind = knowledge.EntityKindFunction
				entityPath = "func:" + name
			}

			entity := Entity{
				ID:        generateEntityID(filePath, entityPath),
				Name:      name,
				Kind:      kind,
				FilePath:  filePath,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig.String(),
				ParentID:  parentID,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

// posToLine converts a byte position to a line number (1-indexed).
func (e *PythonExtractor) posToLine(source string, pos int) int {
	return strings.Count(source[:pos], "\n") + 1
}

// getIndentLevel returns the number of leading whitespace characters.
func (e *PythonExtractor) getIndentLevel(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
		} else if ch == '\t' {
			count += 4 // Treat tab as 4 spaces
		} else {
			break
		}
	}
	return count
}

// tripleQuoteState tracks the state of triple-quoted string parsing.
type tripleQuoteState struct {
	inTripleQuote bool
	quoteChar     byte // '"' or '\''
}

// checkTripleQuote checks for triple quote markers and updates state.
// Returns whether the line ends inside a triple-quoted string.
func (s *tripleQuoteState) checkTripleQuote(line string) bool {
	i := 0
	for i < len(line) {
		i = s.processChar(line, i)
	}
	return s.inTripleQuote
}

// processChar processes a single character position and returns the next index.
func (s *tripleQuoteState) processChar(line string, i int) int {
	if s.inTripleQuote {
		return s.processInsideTripleQuote(line, i)
	}
	return s.processOutsideTripleQuote(line, i)
}

// processInsideTripleQuote handles characters when inside a triple-quoted string.
func (s *tripleQuoteState) processInsideTripleQuote(line string, i int) int {
	if s.matchTripleQuote(line, i) {
		s.inTripleQuote = false
		return i + 3
	}
	return i + 1
}

// processOutsideTripleQuote handles characters when outside a triple-quoted string.
func (s *tripleQuoteState) processOutsideTripleQuote(line string, i int) int {
	if line[i] == '#' {
		return len(line) // Skip rest of line (comment)
	}
	return s.handleQuoteOrAdvance(line, i)
}

// handleQuoteOrAdvance handles potential quote start or advances by one.
func (s *tripleQuoteState) handleQuoteOrAdvance(line string, i int) int {
	if s.matchTripleQuote(line, i) {
		s.inTripleQuote = true
		s.quoteChar = line[i]
		return i + 3
	}
	if s.isQuoteChar(line[i]) {
		return s.skipSingleString(line, i)
	}
	return i + 1
}

// matchTripleQuote checks if there's a triple quote at position i.
func (s *tripleQuoteState) matchTripleQuote(line string, i int) bool {
	if !s.canMatchTripleQuote(line, i) {
		return false
	}
	ch := line[i]
	return s.isValidTripleQuote(line, i, ch)
}

// canMatchTripleQuote checks if there's enough room for a triple quote.
func (s *tripleQuoteState) canMatchTripleQuote(line string, i int) bool {
	return i+2 < len(line) && s.isQuoteChar(line[i])
}

// isValidTripleQuote checks if the characters form a valid triple quote.
func (s *tripleQuoteState) isValidTripleQuote(line string, i int, ch byte) bool {
	if s.inTripleQuote && ch != s.quoteChar {
		return false
	}
	return line[i+1] == ch && line[i+2] == ch
}

// isQuoteChar returns true if the character is a quote character.
func (s *tripleQuoteState) isQuoteChar(ch byte) bool {
	return ch == '"' || ch == '\''
}

// skipSingleString skips over a single-quoted string starting at position i.
func (s *tripleQuoteState) skipSingleString(line string, i int) int {
	if i >= len(line) {
		return i
	}
	quote := line[i]
	return s.findStringEnd(line, i+1, quote)
}

// findStringEnd finds the end of a string starting at position i.
func (s *tripleQuoteState) findStringEnd(line string, i int, quote byte) int {
	for i < len(line) {
		if s.isEscapedChar(line, i) {
			i += 2
			continue
		}
		if line[i] == quote {
			return i + 1
		}
		i++
	}
	return i
}

// isEscapedChar checks if position i is an escape sequence.
func (s *tripleQuoteState) isEscapedChar(line string, i int) bool {
	return line[i] == '\\' && i+1 < len(line)
}

// blockEndState holds state for block end detection.
type blockEndState struct {
	lastNonEmptyLine int
	inBlock          bool
	quoteState       *tripleQuoteState
	baseIndent       int
}

// findPythonBlockEnd finds where a Python block ends based on indentation.
// A block ends when we find a line with equal or less indentation that is not
// empty, comment, or inside a triple-quoted docstring.
func (e *PythonExtractor) findPythonBlockEnd(lines []string, startLine int, baseIndent int) int {
	if !e.isValidStartLine(startLine, len(lines)) {
		return startLine
	}
	state := e.newBlockEndState(startLine, baseIndent)
	e.scanBlockLines(lines, startLine, state)
	return e.resolveBlockEnd(lines, startLine, state)
}

// isValidStartLine checks if startLine is within valid bounds.
func (e *PythonExtractor) isValidStartLine(startLine, lineCount int) bool {
	return startLine >= 1 && startLine <= lineCount
}

// newBlockEndState creates a new block end state.
func (e *PythonExtractor) newBlockEndState(startLine int, baseIndent int) *blockEndState {
	return &blockEndState{
		lastNonEmptyLine: startLine,
		inBlock:          false,
		quoteState:       &tripleQuoteState{},
		baseIndent:       baseIndent,
	}
}

// scanBlockLines scans through lines to find block end.
func (e *PythonExtractor) scanBlockLines(lines []string, startLine int, state *blockEndState) {
	for i := startLine; i < len(lines); i++ {
		if e.processBlockLine(lines[i], i, state) == blockEndBreak {
			return
		}
	}
}

// resolveBlockEnd returns the final block end line.
func (e *PythonExtractor) resolveBlockEnd(lines []string, startLine int, state *blockEndState) int {
	if !state.inBlock {
		return e.checkSingleLineBody(lines, startLine)
	}
	return state.lastNonEmptyLine
}

// blockAction represents the action to take after processing a line.
type blockAction int

const (
	blockEndContinue blockAction = iota
	blockEndBreak
)

// processBlockLine processes a single line for block end detection.
func (e *PythonExtractor) processBlockLine(line string, i int, state *blockEndState) blockAction {
	if e.isInDocstring(line, i, state) {
		return blockEndContinue
	}
	if e.isSkippableLine(line) {
		return blockEndContinue
	}
	return e.updateBlockState(line, i, state)
}

// isInDocstring checks if the line is inside a docstring and updates state.
func (e *PythonExtractor) isInDocstring(line string, lineIdx int, state *blockEndState) bool {
	wasInTripleQuote := state.quoteState.inTripleQuote
	stillInTripleQuote := state.quoteState.checkTripleQuote(line)
	if wasInTripleQuote || stillInTripleQuote {
		if state.inBlock {
			state.lastNonEmptyLine = lineIdx + 1
		}
		return true
	}
	return false
}

// isSkippableLine returns true if the line should be skipped (empty or comment).
func (e *PythonExtractor) isSkippableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// updateBlockState updates state based on indentation and returns action.
func (e *PythonExtractor) updateBlockState(line string, i int, state *blockEndState) blockAction {
	currentIndent := e.getIndentLevel(line)
	if !state.inBlock {
		return e.handleBlockStart(currentIndent, i, state)
	}
	return e.handleBlockContent(currentIndent, i, state)
}

// handleBlockStart handles the transition into a block.
func (e *PythonExtractor) handleBlockStart(indent int, i int, state *blockEndState) blockAction {
	if indent > state.baseIndent {
		state.inBlock = true
		state.lastNonEmptyLine = i + 1
	}
	return blockEndContinue
}

// handleBlockContent handles content inside a block.
func (e *PythonExtractor) handleBlockContent(indent int, i int, state *blockEndState) blockAction {
	if indent > state.baseIndent {
		state.lastNonEmptyLine = i + 1
		return blockEndContinue
	}
	return blockEndBreak
}

// checkSingleLineBody checks if a function has its body on the same line.
func (e *PythonExtractor) checkSingleLineBody(lines []string, startLine int) int {
	if startLine > len(lines) {
		return startLine
	}
	return e.analyzeSingleLine(lines[startLine-1], startLine)
}

// analyzeSingleLine analyzes a line to determine if it has inline body.
func (e *PythonExtractor) analyzeSingleLine(defLine string, startLine int) int {
	colonIdx := strings.Index(defLine, ":")
	if colonIdx == -1 {
		return startLine
	}
	afterColon := strings.TrimSpace(defLine[colonIdx+1:])
	if e.hasInlineBody(afterColon) {
		return startLine
	}
	return startLine
}

// hasInlineBody checks if text after colon is an inline body.
func (e *PythonExtractor) hasInlineBody(afterColon string) bool {
	return afterColon != "" && !strings.HasPrefix(afterColon, "#")
}
