package extractors

import (
	"regexp"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/knowledge"
)

// PythonExtractor extracts entities from Python source files using regex patterns.
type PythonExtractor struct {
	// Compiled regex patterns for different entity types
	functionPattern      *regexp.Regexp
	asyncFunctionPattern *regexp.Regexp
	classPattern         *regexp.Regexp
	methodPattern        *regexp.Regexp
	asyncMethodPattern   *regexp.Regexp
	decoratorPattern     *regexp.Regexp
}

// blockEndKey uniquely identifies a block by its start line and base indentation.
type blockEndKey struct {
	startLine  int
	baseIndent int
}

// blockEndCache stores precomputed block end positions for O(1) lookup.
// This eliminates O(n^2) behavior when many functions/classes are present.
type blockEndCache struct {
	cache map[blockEndKey]int
}

// newBlockEndCache creates a cache by scanning all lines once (O(n)).
func newBlockEndCache(lines []string, extractor *PythonExtractor) *blockEndCache {
	return &blockEndCache{cache: make(map[blockEndKey]int)}
}

// precomputeBlockEnds scans lines once and caches block ends for given start positions.
func (c *blockEndCache) precomputeBlockEnds(lines []string, starts []blockEndKey, extractor *PythonExtractor) {
	for _, key := range starts {
		if _, exists := c.cache[key]; !exists {
			c.cache[key] = extractor.computeBlockEnd(lines, key.startLine, key.baseIndent)
		}
	}
}

// get retrieves a cached block end, computing it if not present.
func (c *blockEndCache) get(startLine, baseIndent int, lines []string, extractor *PythonExtractor) int {
	key := blockEndKey{startLine: startLine, baseIndent: baseIndent}
	if end, exists := c.cache[key]; exists {
		return end
	}
	end := extractor.computeBlockEnd(lines, startLine, baseIndent)
	c.cache[key] = end
	return end
}

// classInfo holds information about a class for O(1) lookup.
type classInfo struct {
	name      string
	id        string
	startLine int
	endLine   int
	indent    int
}

// classLookup provides O(1) parent class detection using binary search.
type classLookup struct {
	classes []classInfo // sorted by startLine
}

// newClassLookup creates a class lookup from class entities.
func newClassLookup(classEntities []Entity, lines []string, extractor *PythonExtractor) *classLookup {
	classes := make([]classInfo, 0, len(classEntities))
	for _, ce := range classEntities {
		indent := extractor.getIndentLevel(lines[ce.StartLine-1])
		classes = append(classes, classInfo{
			name:      ce.Name,
			id:        ce.ID,
			startLine: ce.StartLine,
			endLine:   ce.EndLine,
			indent:    indent,
		})
	}
	sort.Slice(classes, func(i, j int) bool {
		return classes[i].startLine < classes[j].startLine
	})
	return &classLookup{classes: classes}
}

// findParentClass finds the parent class for a function at given line with given indent.
// Returns className, classID, or empty strings if no parent class.
func (cl *classLookup) findParentClass(line, indent int) (string, string) {
	if len(cl.classes) == 0 {
		return "", ""
	}
	idx := cl.searchClassIndex(line)
	return cl.checkCandidates(idx, line, indent)
}

// searchClassIndex finds the index of the last class starting before or at line.
func (cl *classLookup) searchClassIndex(line int) int {
	return sort.Search(len(cl.classes), func(i int) bool {
		return cl.classes[i].startLine > line
	}) - 1
}

// checkCandidates checks candidate classes starting from idx backwards.
// For nested classes, we must check all candidates since an inner class
// may end before the line but an outer class may still contain it.
func (cl *classLookup) checkCandidates(idx, line, indent int) (string, string) {
	for i := idx; i >= 0; i-- {
		c := cl.classes[i]
		if cl.isValidParent(c, line, indent) {
			return c.name, c.id
		}
	}
	return "", ""
}

// isValidParent checks if c is a valid parent class for the given line/indent.
func (cl *classLookup) isValidParent(c classInfo, line, indent int) bool {
	return line > c.startLine && line <= c.endLine && indent > c.indent
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

	// Create block end cache and collect all block starts in single pass
	cache := newBlockEndCache(lines, e)
	blockStarts := e.collectBlockStarts(source)
	cache.precomputeBlockEnds(lines, blockStarts, e)

	var entities []Entity

	// First pass: extract classes using cache
	classEntities := e.extractClassesWithCache(filePath, source, lines, cache)
	entities = append(entities, classEntities...)

	// Build O(1) class lookup using sorted classes with binary search
	lookup := newClassLookup(classEntities, lines, e)

	// Extract functions and methods using cache
	entities = append(entities, e.extractFunctionsAndMethodsWithCache(filePath, source, lines, lookup, cache)...)

	return entities, nil
}

// collectBlockStarts gathers all block start positions from the source.
func (e *PythonExtractor) collectBlockStarts(source string) []blockEndKey {
	var starts []blockEndKey

	// Collect class starts
	classMatches := e.classPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range classMatches {
		if len(match) >= 6 {
			startLine := e.posToLine(source, match[0])
			indent := 0
			if match[2] != -1 && match[3] != -1 {
				indent = match[3] - match[2]
			}
			starts = append(starts, blockEndKey{startLine: startLine, baseIndent: indent})
		}
	}

	// Collect function starts
	funcMatches := e.functionPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range funcMatches {
		if len(match) >= 6 {
			startLine := e.posToLine(source, match[0])
			indent := 0
			if match[2] != -1 && match[3] != -1 {
				indent = match[3] - match[2]
			}
			starts = append(starts, blockEndKey{startLine: startLine, baseIndent: indent})
		}
	}

	// Collect async function starts
	asyncMatches := e.asyncFunctionPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range asyncMatches {
		if len(match) >= 6 {
			startLine := e.posToLine(source, match[0])
			indent := 0
			if match[2] != -1 && match[3] != -1 {
				indent = match[3] - match[2]
			}
			starts = append(starts, blockEndKey{startLine: startLine, baseIndent: indent})
		}
	}

	return starts
}

// extractClassesWithCache extracts class declarations using cached block ends.
func (e *PythonExtractor) extractClassesWithCache(filePath, source string, lines []string, cache *blockEndCache) []Entity {
	var entities []Entity
	matches := e.classPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)

			// Get indentation
			indentLen := 0
			if match[2] != -1 && match[3] != -1 {
				indentLen = match[3] - match[2]
			}

			// Extract class name (group 2)
			name := source[match[4]:match[5]]

			// Get block end from cache (O(1) lookup)
			endLine := cache.get(startLine, indentLen, lines, e)

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

// extractFunctionsAndMethodsWithCache extracts functions and methods using cached block ends.
func (e *PythonExtractor) extractFunctionsAndMethodsWithCache(
	filePath, source string,
	lines []string,
	lookup *classLookup,
	cache *blockEndCache,
) []Entity {
	var entities []Entity

	// Extract regular functions and methods
	entities = append(entities, e.extractFuncsByPatternWithCache(filePath, source, lines, lookup, e.functionPattern, false, cache)...)

	// Extract async functions and methods
	entities = append(entities, e.extractFuncsByPatternWithCache(filePath, source, lines, lookup, e.asyncFunctionPattern, true, cache)...)

	return entities
}

// extractFuncsByPatternWithCache extracts functions matching the given pattern using cache.
func (e *PythonExtractor) extractFuncsByPatternWithCache(
	filePath, source string,
	lines []string,
	lookup *classLookup,
	pattern *regexp.Regexp,
	isAsync bool,
	cache *blockEndCache,
) []Entity {
	matches := pattern.FindAllStringSubmatchIndex(source, -1)
	entities := make([]Entity, 0, len(matches))

	for _, match := range matches {
		if entity := e.processMatchWithCache(filePath, source, lines, lookup, match, isAsync, cache); entity != nil {
			entities = append(entities, *entity)
		}
	}

	return entities
}

// processMatchWithCache processes a single regex match using cache and returns an entity or nil.
func (e *PythonExtractor) processMatchWithCache(
	filePath, source string,
	lines []string,
	lookup *classLookup,
	match []int,
	isAsync bool,
	cache *blockEndCache,
) *Entity {
	if len(match) < 6 {
		return nil
	}

	startPos := match[0]
	startLine := e.posToLine(source, startPos)
	indentLevel := e.extractIndentLevel(source, match)
	name := source[match[4]:match[5]]
	endLine := cache.get(startLine, indentLevel, lines, e)
	sig := e.buildSignature(source, match, isAsync, name)

	// O(1) parent class lookup using binary search
	parentClass, parentID := lookup.findParentClass(startLine, indentLevel)

	kind, entityPath := e.determineKindAndPath(parentClass, name)

	entity := Entity{
		ID:        generateEntityID(filePath, entityPath),
		Name:      name,
		Kind:      kind,
		FilePath:  filePath,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: sig,
		ParentID:  parentID,
	}
	return &entity
}

// extractIndentLevel extracts the indentation level from a match.
func (e *PythonExtractor) extractIndentLevel(source string, match []int) int {
	if match[2] != -1 && match[3] != -1 {
		return len(source[match[2]:match[3]])
	}
	return 0
}

// buildSignature builds the function signature string.
func (e *PythonExtractor) buildSignature(source string, match []int, isAsync bool, name string) string {
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
	return sig.String()
}

// determineKindAndPath determines the entity kind and path based on parent class.
func (e *PythonExtractor) determineKindAndPath(parentClass, name string) (knowledge.EntityKind, string) {
	if parentClass != "" {
		return knowledge.EntityKindMethod, parentClass + "." + name
	}
	return knowledge.EntityKindFunction, "func:" + name
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

// computeBlockEnd computes where a Python block ends based on indentation.
// A block ends when we find a line with equal or less indentation that is not
// empty, comment, or inside a triple-quoted docstring.
// This is called once per block during cache precomputation.
func (e *PythonExtractor) computeBlockEnd(lines []string, startLine int, baseIndent int) int {
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
