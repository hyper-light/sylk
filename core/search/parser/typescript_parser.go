package parser

import (
	"bytes"
	"regexp"
	"strings"
)

// TypeScriptParser extracts symbols, comments, and imports from TypeScript/JavaScript files.
type TypeScriptParser struct {
	funcPattern      *regexp.Regexp
	classPattern     *regexp.Regexp
	interfacePattern *regexp.Regexp
	typePattern      *regexp.Regexp
	constPattern     *regexp.Regexp
	letVarPattern    *regexp.Regexp
	importPattern    *regexp.Regexp
	exportPattern    *regexp.Regexp
	singleComment    *regexp.Regexp
	multiComment     *regexp.Regexp
}

// NewTypeScriptParser creates a new TypeScript parser instance.
func NewTypeScriptParser() *TypeScriptParser {
	return &TypeScriptParser{
		funcPattern:      regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?(?:async\s+)?function\s+(\w+)`),
		classPattern:     regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?(?:abstract\s+)?class\s+(\w+)`),
		interfacePattern: regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?interface\s+(\w+)`),
		typePattern:      regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?type\s+(\w+)`),
		constPattern:     regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?const\s+(\w+)`),
		letVarPattern:    regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?(?:let|var)\s+(\w+)`),
		importPattern:    regexp.MustCompile(`(?m)^[\t ]*import\s+(?:.*\s+from\s+)?['"]([^'"]+)['"]`),
		exportPattern:    regexp.MustCompile(`(?m)^[\t ]*export\s+`),
		singleComment:    regexp.MustCompile(`(?m)//(.*)$`),
		multiComment:     regexp.MustCompile(`(?s)/\*(.+?)\*/`),
	}
}

// Extensions returns the file extensions handled by this parser.
func (p *TypeScriptParser) Extensions() []string {
	return []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}
}

// Parse extracts symbols, comments, imports, and metadata from TypeScript/JavaScript.
func (p *TypeScriptParser) Parse(content []byte, path string) (*ParseResult, error) {
	if len(content) == 0 {
		return NewParseResult(), nil
	}

	result := NewParseResult()
	text := string(content)
	lines := strings.Split(text, "\n")

	p.extractImports(text, result)
	p.extractComments(text, result)
	p.extractSymbols(lines, result)
	p.setMetadata(path, result)

	return result, nil
}

// extractImports extracts import paths from the source.
func (p *TypeScriptParser) extractImports(text string, result *ParseResult) {
	matches := p.importPattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) > 1 {
			result.Imports = append(result.Imports, match[1])
		}
	}
}

// extractComments extracts all comments from the source.
func (p *TypeScriptParser) extractComments(text string, result *ParseResult) {
	var buf bytes.Buffer

	p.extractSingleLineComments(text, &buf)
	p.extractMultiLineComments(text, &buf)

	result.Comments = strings.TrimSpace(buf.String())
}

// extractSingleLineComments extracts // style comments.
func (p *TypeScriptParser) extractSingleLineComments(text string, buf *bytes.Buffer) {
	matches := p.singleComment.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) > 1 {
			comment := strings.TrimSpace(match[1])
			if comment != "" {
				buf.WriteString(comment)
				buf.WriteString("\n")
			}
		}
	}
}

// extractMultiLineComments extracts /* */ style comments.
func (p *TypeScriptParser) extractMultiLineComments(text string, buf *bytes.Buffer) {
	matches := p.multiComment.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) > 1 {
			comment := cleanMultiLineComment(match[1])
			if comment != "" {
				buf.WriteString(comment)
				buf.WriteString("\n")
			}
		}
	}
}

// extractSymbols extracts all symbol declarations from the source.
func (p *TypeScriptParser) extractSymbols(lines []string, result *ParseResult) {
	for i, line := range lines {
		lineNum := i + 1
		exported := p.exportPattern.MatchString(line)

		p.extractFunctions(line, lineNum, exported, result)
		p.extractClasses(line, lineNum, exported, result)
		p.extractInterfaces(line, lineNum, exported, result)
		p.extractTypes(line, lineNum, exported, result)
		p.extractConstants(line, lineNum, exported, result)
		p.extractVariables(line, lineNum, exported, result)
	}
}

// extractFunctions extracts function declarations.
func (p *TypeScriptParser) extractFunctions(line string, lineNum int, exported bool, result *ParseResult) {
	match := p.funcPattern.FindStringSubmatch(line)
	if len(match) > 1 {
		result.Symbols = append(result.Symbols, Symbol{
			Name:      match[1],
			Kind:      KindFunction,
			Signature: buildTSFuncSignature(line),
			Line:      lineNum,
			Exported:  exported,
		})
	}
}

// extractClasses extracts class declarations.
func (p *TypeScriptParser) extractClasses(line string, lineNum int, exported bool, result *ParseResult) {
	match := p.classPattern.FindStringSubmatch(line)
	if len(match) > 1 {
		result.Symbols = append(result.Symbols, Symbol{
			Name:     match[1],
			Kind:     KindClass,
			Line:     lineNum,
			Exported: exported,
		})
	}
}

// extractInterfaces extracts interface declarations.
func (p *TypeScriptParser) extractInterfaces(line string, lineNum int, exported bool, result *ParseResult) {
	match := p.interfacePattern.FindStringSubmatch(line)
	if len(match) > 1 {
		result.Symbols = append(result.Symbols, Symbol{
			Name:     match[1],
			Kind:     KindInterface,
			Line:     lineNum,
			Exported: exported,
		})
	}
}

// extractTypes extracts type alias declarations.
func (p *TypeScriptParser) extractTypes(line string, lineNum int, exported bool, result *ParseResult) {
	match := p.typePattern.FindStringSubmatch(line)
	if len(match) > 1 {
		result.Symbols = append(result.Symbols, Symbol{
			Name:     match[1],
			Kind:     KindType,
			Line:     lineNum,
			Exported: exported,
		})
	}
}

// extractConstants extracts const declarations.
func (p *TypeScriptParser) extractConstants(line string, lineNum int, exported bool, result *ParseResult) {
	match := p.constPattern.FindStringSubmatch(line)
	if len(match) > 1 {
		result.Symbols = append(result.Symbols, Symbol{
			Name:     match[1],
			Kind:     KindConst,
			Line:     lineNum,
			Exported: exported,
		})
	}
}

// extractVariables extracts let/var declarations.
func (p *TypeScriptParser) extractVariables(line string, lineNum int, exported bool, result *ParseResult) {
	match := p.letVarPattern.FindStringSubmatch(line)
	if len(match) > 1 {
		result.Symbols = append(result.Symbols, Symbol{
			Name:     match[1],
			Kind:     KindVariable,
			Line:     lineNum,
			Exported: exported,
		})
	}
}

// setMetadata sets file metadata.
func (p *TypeScriptParser) setMetadata(path string, result *ParseResult) {
	result.Metadata["language"] = detectTSLanguage(path)
}

// =============================================================================
// Helper Functions
// =============================================================================

// buildTSFuncSignature extracts a simplified function signature from a line.
func buildTSFuncSignature(line string) string {
	line = strings.TrimSpace(line)

	// Find the opening brace or end of signature
	braceIdx := strings.Index(line, "{")
	if braceIdx > 0 {
		line = strings.TrimSpace(line[:braceIdx])
	}

	// Remove trailing comments
	commentIdx := strings.Index(line, "//")
	if commentIdx > 0 {
		line = strings.TrimSpace(line[:commentIdx])
	}

	return line
}

// cleanMultiLineComment cleans up multi-line comment content.
func cleanMultiLineComment(text string) string {
	lines := strings.Split(text, "\n")
	var cleaned []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}

	return strings.Join(cleaned, " ")
}

// detectTSLanguage determines if the file is TypeScript or JavaScript.
func detectTSLanguage(path string) string {
	if strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx") {
		return "typescript"
	}
	return "javascript"
}

// init registers the TypeScript parser in the default registry.
func init() {
	RegisterParser(NewTypeScriptParser())
}
