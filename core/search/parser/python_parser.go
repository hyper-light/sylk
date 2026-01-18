package parser

import (
	"bytes"
	"regexp"
	"strings"
)

// PythonParser extracts symbols, comments, and imports from Python files.
type PythonParser struct {
	funcPattern      *regexp.Regexp
	classPattern     *regexp.Regexp
	asyncFuncPattern *regexp.Regexp
	importPattern    *regexp.Regexp
	fromImportPat    *regexp.Regexp
	decoratorPattern *regexp.Regexp
	singleComment    *regexp.Regexp
	docstringStart   *regexp.Regexp
}

// NewPythonParser creates a new Python parser instance.
func NewPythonParser() *PythonParser {
	return &PythonParser{
		funcPattern:      regexp.MustCompile(`^[\t ]*def\s+(\w+)\s*\(`),
		classPattern:     regexp.MustCompile(`^[\t ]*class\s+(\w+)`),
		asyncFuncPattern: regexp.MustCompile(`^[\t ]*async\s+def\s+(\w+)\s*\(`),
		importPattern:    regexp.MustCompile(`^[\t ]*import\s+(.+)`),
		fromImportPat:    regexp.MustCompile(`^[\t ]*from\s+(\S+)\s+import`),
		decoratorPattern: regexp.MustCompile(`^[\t ]*@(\w+)`),
		singleComment:    regexp.MustCompile(`#(.*)$`),
		docstringStart:   regexp.MustCompile(`^[\t ]*(?:'''|""")`),
	}
}

// Extensions returns the file extensions handled by this parser.
func (p *PythonParser) Extensions() []string {
	return []string{".py", ".pyi", ".pyw"}
}

// Parse extracts symbols, comments, imports, and metadata from Python source.
func (p *PythonParser) Parse(content []byte, path string) (*ParseResult, error) {
	if len(content) == 0 {
		return NewParseResult(), nil
	}

	result := NewParseResult()
	text := string(content)
	lines := strings.Split(text, "\n")

	p.extractImports(lines, result)
	p.extractComments(text, lines, result)
	p.extractSymbols(lines, result)
	p.setMetadata(path, result)

	return result, nil
}

// extractImports extracts import statements from the source.
func (p *PythonParser) extractImports(lines []string, result *ParseResult) {
	for _, line := range lines {
		p.extractImportLine(line, result)
		p.extractFromImportLine(line, result)
	}
}

// extractImportLine handles "import x" statements.
func (p *PythonParser) extractImportLine(line string, result *ParseResult) {
	match := p.importPattern.FindStringSubmatch(line)
	if len(match) <= 1 {
		return
	}

	imports := parseImportList(match[1])
	result.Imports = append(result.Imports, imports...)
}

// extractFromImportLine handles "from x import y" statements.
func (p *PythonParser) extractFromImportLine(line string, result *ParseResult) {
	match := p.fromImportPat.FindStringSubmatch(line)
	if len(match) > 1 {
		result.Imports = append(result.Imports, match[1])
	}
}

// extractComments extracts all comments and docstrings from the source.
func (p *PythonParser) extractComments(text string, lines []string, result *ParseResult) {
	var buf bytes.Buffer

	p.extractLineComments(lines, &buf)
	p.extractDocstrings(text, &buf)

	result.Comments = strings.TrimSpace(buf.String())
}

// extractLineComments extracts # style comments.
func (p *PythonParser) extractLineComments(lines []string, buf *bytes.Buffer) {
	for _, line := range lines {
		match := p.singleComment.FindStringSubmatch(line)
		if len(match) > 1 {
			comment := strings.TrimSpace(match[1])
			if comment != "" {
				buf.WriteString(comment)
				buf.WriteString("\n")
			}
		}
	}
}

// extractDocstrings extracts triple-quoted docstrings.
func (p *PythonParser) extractDocstrings(text string, buf *bytes.Buffer) {
	// Extract """ docstrings
	extractQuotedStrings(text, `"""`, buf)
	// Extract ''' docstrings
	extractQuotedStrings(text, `'''`, buf)
}

// extractSymbols extracts function and class definitions.
func (p *PythonParser) extractSymbols(lines []string, result *ParseResult) {
	var pendingDecorators []string

	for i, line := range lines {
		lineNum := i + 1

		// Check for decorators
		if decorators := p.extractDecorators(line); len(decorators) > 0 {
			pendingDecorators = append(pendingDecorators, decorators...)
			continue
		}

		// Extract functions and classes
		if p.tryExtractFunction(line, lineNum, pendingDecorators, result) {
			pendingDecorators = nil
			continue
		}

		if p.tryExtractClass(line, lineNum, pendingDecorators, result) {
			pendingDecorators = nil
			continue
		}

		// Reset decorators if we hit a non-decorator, non-definition line
		if !isEmptyOrWhitespace(line) {
			pendingDecorators = nil
		}
	}
}

// extractDecorators extracts decorator names from a line.
func (p *PythonParser) extractDecorators(line string) []string {
	match := p.decoratorPattern.FindStringSubmatch(line)
	if len(match) > 1 {
		return []string{match[1]}
	}
	return nil
}

// tryExtractFunction attempts to extract a function definition.
func (p *PythonParser) tryExtractFunction(line string, lineNum int, decorators []string, result *ParseResult) bool {
	// Try async function first
	if match := p.asyncFuncPattern.FindStringSubmatch(line); len(match) > 1 {
		p.addFunctionSymbol(match[1], line, lineNum, true, decorators, result)
		return true
	}

	// Try regular function
	if match := p.funcPattern.FindStringSubmatch(line); len(match) > 1 {
		p.addFunctionSymbol(match[1], line, lineNum, false, decorators, result)
		return true
	}

	return false
}

// addFunctionSymbol adds a function symbol to the result.
func (p *PythonParser) addFunctionSymbol(name, line string, lineNum int, isAsync bool, decorators []string, result *ParseResult) {
	sig := buildPythonFuncSignature(line, isAsync)
	exported := !strings.HasPrefix(name, "_")

	// Add decorator info to metadata-style signature
	if len(decorators) > 0 {
		sig = "@" + strings.Join(decorators, " @") + " " + sig
	}

	result.Symbols = append(result.Symbols, Symbol{
		Name:      name,
		Kind:      KindFunction,
		Signature: sig,
		Line:      lineNum,
		Exported:  exported,
	})
}

// tryExtractClass attempts to extract a class definition.
func (p *PythonParser) tryExtractClass(line string, lineNum int, decorators []string, result *ParseResult) bool {
	match := p.classPattern.FindStringSubmatch(line)
	if len(match) <= 1 {
		return false
	}

	name := match[1]
	exported := !strings.HasPrefix(name, "_")

	sig := strings.TrimSpace(line)
	if len(decorators) > 0 {
		sig = "@" + strings.Join(decorators, " @") + " " + sig
	}

	result.Symbols = append(result.Symbols, Symbol{
		Name:      name,
		Kind:      KindClass,
		Signature: sig,
		Line:      lineNum,
		Exported:  exported,
	})

	return true
}

// setMetadata sets file metadata.
func (p *PythonParser) setMetadata(path string, result *ParseResult) {
	result.Metadata["language"] = "python"

	if strings.HasSuffix(path, ".pyi") {
		result.Metadata["stub_file"] = "true"
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// parseImportList parses a comma-separated import list.
func parseImportList(importStr string) []string {
	var result []string

	parts := strings.Split(importStr, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)

		// Handle "import x as y" by extracting just x
		if asIdx := strings.Index(part, " as "); asIdx > 0 {
			part = part[:asIdx]
		}

		// Stop at comments
		if hashIdx := strings.Index(part, "#"); hashIdx >= 0 {
			part = part[:hashIdx]
		}

		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}

	return result
}

// buildPythonFuncSignature builds a function signature from the def line.
func buildPythonFuncSignature(line string, isAsync bool) string {
	line = strings.TrimSpace(line)

	// Remove trailing comments first
	if hashIdx := strings.Index(line, "#"); hashIdx > 0 {
		line = strings.TrimSpace(line[:hashIdx])
	}

	// Find the last colon which ends the function definition
	// This handles type annotations which also contain colons
	if colonIdx := strings.LastIndex(line, ":"); colonIdx > 0 {
		line = strings.TrimSpace(line[:colonIdx])
	}

	return line
}

// extractQuotedStrings extracts content between triple quotes.
func extractQuotedStrings(text, quote string, buf *bytes.Buffer) {
	remaining := text

	for {
		startIdx := strings.Index(remaining, quote)
		if startIdx == -1 {
			break
		}

		afterStart := remaining[startIdx+len(quote):]
		endIdx := strings.Index(afterStart, quote)
		if endIdx == -1 {
			break
		}

		docstring := strings.TrimSpace(afterStart[:endIdx])
		if docstring != "" {
			buf.WriteString(docstring)
			buf.WriteString("\n")
		}

		remaining = afterStart[endIdx+len(quote):]
	}
}

// isEmptyOrWhitespace returns true if line is empty or only whitespace.
func isEmptyOrWhitespace(line string) bool {
	return strings.TrimSpace(line) == ""
}

// init registers the Python parser in the default registry.
func init() {
	RegisterParser(NewPythonParser())
}
