package parser

import (
	"bytes"
	"regexp"
	"strings"
)

// MarkdownParser extracts headings, code blocks, and links from Markdown files.
type MarkdownParser struct {
	headingPattern    *regexp.Regexp
	linkPattern       *regexp.Regexp
	codeBlockStart    *regexp.Regexp
	codeBlockEnd      *regexp.Regexp
	inlineCodePattern *regexp.Regexp
}

// NewMarkdownParser creates a new Markdown parser instance.
func NewMarkdownParser() *MarkdownParser {
	return &MarkdownParser{
		headingPattern:    regexp.MustCompile(`^(#{1,6})\s+(.+)$`),
		linkPattern:       regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`),
		codeBlockStart:    regexp.MustCompile("^```(\\w*)"),
		codeBlockEnd:      regexp.MustCompile("^```\\s*$"),
		inlineCodePattern: regexp.MustCompile("`([^`]+)`"),
	}
}

// Extensions returns the file extensions handled by this parser.
func (p *MarkdownParser) Extensions() []string {
	return []string{".md", ".markdown", ".mdx"}
}

// Parse extracts headings, code blocks, links, and text from Markdown.
func (p *MarkdownParser) Parse(content []byte, path string) (*ParseResult, error) {
	if len(content) == 0 {
		return NewParseResult(), nil
	}

	result := NewParseResult()
	lines := strings.Split(string(content), "\n")

	p.extractSymbols(lines, result)
	p.extractLinks(string(content), result)
	p.extractTextContent(lines, result)
	p.setMetadata(path, result)

	return result, nil
}

// extractSymbols extracts headings and code blocks as symbols.
func (p *MarkdownParser) extractSymbols(lines []string, result *ParseResult) {
	inCodeBlock := false
	codeBlockStart := 0
	codeBlockLang := ""

	for i, line := range lines {
		lineNum := i + 1

		// Handle code block toggling
		if p.handleCodeBlock(line, lineNum, &inCodeBlock, &codeBlockStart, &codeBlockLang, result) {
			continue
		}

		// Extract headings when not in code block
		if !inCodeBlock {
			p.extractHeading(line, lineNum, result)
		}
	}
}

// handleCodeBlock processes code block start/end markers.
func (p *MarkdownParser) handleCodeBlock(line string, lineNum int, inCodeBlock *bool, startLine *int, lang *string, result *ParseResult) bool {
	if *inCodeBlock {
		return p.handleCodeBlockEnd(line, lineNum, inCodeBlock, startLine, lang, result)
	}
	return p.handleCodeBlockStart(line, lineNum, inCodeBlock, startLine, lang)
}

// handleCodeBlockStart processes code block start markers.
func (p *MarkdownParser) handleCodeBlockStart(line string, lineNum int, inCodeBlock *bool, startLine *int, lang *string) bool {
	match := p.codeBlockStart.FindStringSubmatch(line)
	if len(match) > 0 {
		*inCodeBlock = true
		*startLine = lineNum
		*lang = match[1]
		return true
	}
	return false
}

// handleCodeBlockEnd processes code block end markers.
func (p *MarkdownParser) handleCodeBlockEnd(line string, lineNum int, inCodeBlock *bool, startLine *int, lang *string, result *ParseResult) bool {
	if !p.codeBlockEnd.MatchString(line) {
		return false
	}

	// Add code block as symbol
	name := "code_block"
	if *lang != "" {
		name = *lang + "_code_block"
	}

	result.Symbols = append(result.Symbols, Symbol{
		Name:      name,
		Kind:      KindCodeBlock,
		Signature: *lang,
		Line:      *startLine,
		Exported:  true,
	})

	*inCodeBlock = false
	*lang = ""
	return true
}

// extractHeading extracts a heading symbol from a line.
func (p *MarkdownParser) extractHeading(line string, lineNum int, result *ParseResult) {
	match := p.headingPattern.FindStringSubmatch(line)
	if len(match) <= 2 {
		return
	}

	level := len(match[1])
	text := strings.TrimSpace(match[2])

	result.Symbols = append(result.Symbols, Symbol{
		Name:      text,
		Kind:      KindHeading,
		Signature: headingLevelName(level),
		Line:      lineNum,
		Exported:  true,
	})
}

// extractLinks extracts all links from the content.
func (p *MarkdownParser) extractLinks(text string, result *ParseResult) {
	matches := p.linkPattern.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		linkText := match[1]
		linkURL := match[2]

		// Store links as imports (URL references)
		result.Imports = append(result.Imports, linkURL)

		// Also store as symbol for searchability
		result.Symbols = append(result.Symbols, Symbol{
			Name:      linkText,
			Kind:      KindLink,
			Signature: linkURL,
			Line:      0, // Links can span text, line not meaningful
			Exported:  true,
		})
	}
}

// extractTextContent extracts plain text content (excluding code blocks).
func (p *MarkdownParser) extractTextContent(lines []string, result *ParseResult) {
	var buf bytes.Buffer
	inCodeBlock := false

	for _, line := range lines {
		// Toggle code block state
		if p.codeBlockStart.MatchString(line) && !inCodeBlock {
			inCodeBlock = true
			continue
		}
		if p.codeBlockEnd.MatchString(line) && inCodeBlock {
			inCodeBlock = false
			continue
		}

		// Skip code block content and headings
		if inCodeBlock || p.headingPattern.MatchString(line) {
			continue
		}

		// Add non-empty lines to content
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			buf.WriteString(trimmed)
			buf.WriteString(" ")
		}
	}

	result.Comments = strings.TrimSpace(buf.String())
}

// setMetadata sets file metadata.
func (p *MarkdownParser) setMetadata(path string, result *ParseResult) {
	result.Metadata["language"] = "markdown"
	result.Metadata["type"] = "documentation"

	// Extract title from first heading if available
	for _, sym := range result.Symbols {
		if sym.Kind == KindHeading && sym.Signature == "h1" {
			result.Metadata["title"] = sym.Name
			break
		}
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// headingLevelName returns the heading level name (h1-h6).
func headingLevelName(level int) string {
	switch level {
	case 1:
		return "h1"
	case 2:
		return "h2"
	case 3:
		return "h3"
	case 4:
		return "h4"
	case 5:
		return "h5"
	case 6:
		return "h6"
	default:
		return "heading"
	}
}

// init registers the Markdown parser in the default registry.
func init() {
	RegisterParser(NewMarkdownParser())
}
