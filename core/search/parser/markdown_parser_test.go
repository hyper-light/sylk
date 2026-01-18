package parser

import (
	"strings"
	"testing"
)

// =============================================================================
// MarkdownParser Tests
// =============================================================================

func TestMarkdownParser_Extensions(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	extensions := parser.Extensions()

	expected := []string{".md", ".markdown", ".mdx"}
	if len(extensions) != len(expected) {
		t.Errorf("expected %d extensions, got %d", len(expected), len(extensions))
	}

	for _, ext := range expected {
		found := false
		for _, got := range extensions {
			if got == ext {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected extension %s not found", ext)
		}
	}
}

func TestMarkdownParser_EmptyContent(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	result, err := parser.Parse([]byte{}, "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if len(result.Symbols) != 0 {
		t.Error("empty content should have no symbols")
	}
}

func TestMarkdownParser_ParseHeadings(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := `# Main Title

Some intro text.

## Section One

Content here.

### Subsection

More content.

#### Level 4

##### Level 5

###### Level 6
`
	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	headingSymbols := filterSymbolsByKind(result.Symbols, KindHeading)
	if len(headingSymbols) != 6 {
		t.Errorf("expected 6 headings, got %d", len(headingSymbols))
	}

	// Check heading levels
	mainTitle := findSymbolByName(result.Symbols, "Main Title")
	if mainTitle == nil {
		t.Fatal("Main Title heading not found")
	}
	if mainTitle.Signature != "h1" {
		t.Errorf("expected h1, got %s", mainTitle.Signature)
	}

	section := findSymbolByName(result.Symbols, "Section One")
	if section == nil {
		t.Fatal("Section One heading not found")
	}
	if section.Signature != "h2" {
		t.Errorf("expected h2, got %s", section.Signature)
	}
}

func TestMarkdownParser_ParseCodeBlocks(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := "# Example\n\n```go\nfunc main() {}\n```\n\n```python\ndef hello():\n    pass\n```\n\n```\nplain code\n```\n"

	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	codeBlockSymbols := filterSymbolsByKind(result.Symbols, KindCodeBlock)
	if len(codeBlockSymbols) != 3 {
		t.Errorf("expected 3 code blocks, got %d", len(codeBlockSymbols))
	}

	// Check language detection
	var foundGo, foundPython bool
	for _, sym := range codeBlockSymbols {
		if sym.Name == "go_code_block" {
			foundGo = true
			if sym.Signature != "go" {
				t.Errorf("expected 'go' signature, got %s", sym.Signature)
			}
		}
		if sym.Name == "python_code_block" {
			foundPython = true
		}
	}

	if !foundGo {
		t.Error("go code block not found")
	}
	if !foundPython {
		t.Error("python code block not found")
	}
}

func TestMarkdownParser_ParseLinks(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := `# Links

Check out [Google](https://google.com) and [GitHub](https://github.com).

Also see [local link](./docs/readme.md) and [relative](../other.md).
`
	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	linkSymbols := filterSymbolsByKind(result.Symbols, KindLink)
	if len(linkSymbols) != 4 {
		t.Errorf("expected 4 links, got %d", len(linkSymbols))
	}

	// Links should also be in imports
	if len(result.Imports) != 4 {
		t.Errorf("expected 4 imports (URLs), got %d", len(result.Imports))
	}

	// Check specific link
	google := findSymbolByName(result.Symbols, "Google")
	if google == nil {
		t.Fatal("Google link not found")
	}
	if google.Signature != "https://google.com" {
		t.Errorf("expected google.com URL, got %s", google.Signature)
	}
}

func TestMarkdownParser_ExtractTextContent(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := `# Title

This is paragraph text.

More text here.

` + "```\ncode should be excluded\n```" + `

Final paragraph.
`
	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Comments field contains text content
	if !strings.Contains(result.Comments, "paragraph text") {
		t.Error("text content should include 'paragraph text'")
	}
	if strings.Contains(result.Comments, "code should be excluded") {
		t.Error("text content should not include code block content")
	}
}

func TestMarkdownParser_ParseMetadata(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := `# Document Title

Some content here.
`
	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.Metadata["language"] != "markdown" {
		t.Errorf("expected language 'markdown', got %q", result.Metadata["language"])
	}
	if result.Metadata["type"] != "documentation" {
		t.Errorf("expected type 'documentation', got %q", result.Metadata["type"])
	}
	if result.Metadata["title"] != "Document Title" {
		t.Errorf("expected title 'Document Title', got %q", result.Metadata["title"])
	}
}

func TestMarkdownParser_LineNumbers(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := `# First

## Second

### Third
`
	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	first := findSymbolByName(result.Symbols, "First")
	if first == nil || first.Line != 1 {
		line := 0
		if first != nil {
			line = first.Line
		}
		t.Errorf("First should be at line 1, got %d", line)
	}

	second := findSymbolByName(result.Symbols, "Second")
	if second == nil || second.Line != 3 {
		line := 0
		if second != nil {
			line = second.Line
		}
		t.Errorf("Second should be at line 3, got %d", line)
	}
}

func TestMarkdownParser_HeadingsInCodeBlocks(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := "# Real Heading\n\n```markdown\n# This is not a heading\n```\n"

	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should only find one heading (the real one)
	headingSymbols := filterSymbolsByKind(result.Symbols, KindHeading)
	if len(headingSymbols) != 1 {
		t.Errorf("expected 1 heading (not counting code block), got %d", len(headingSymbols))
	}

	if headingSymbols[0].Name != "Real Heading" {
		t.Errorf("expected 'Real Heading', got %q", headingSymbols[0].Name)
	}
}

func TestMarkdownParser_NestedCodeBlocks(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	// This tests the parser doesn't get confused by backticks inside code
	content := "```bash\necho \"```\"\n```\n"

	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	codeBlockSymbols := filterSymbolsByKind(result.Symbols, KindCodeBlock)
	if len(codeBlockSymbols) != 1 {
		t.Errorf("expected 1 code block, got %d", len(codeBlockSymbols))
	}
}

func TestMarkdownParser_AllSymbolsExported(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := `# Heading

[Link](https://example.com)

` + "```go\ncode\n```"

	result, err := parser.Parse([]byte(content), "test.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	for _, sym := range result.Symbols {
		if !sym.Exported {
			t.Errorf("all markdown symbols should be exported, %q is not", sym.Name)
		}
	}
}

func TestMarkdownParser_ComplexDocument(t *testing.T) {
	t.Parallel()

	parser := NewMarkdownParser()
	content := `# API Documentation

## Overview

This is the API documentation for our service.

## Endpoints

### GET /users

Returns a list of users.

` + "```json\n{\"users\": []}\n```" + `

### POST /users

Creates a new user. See [User Model](#user-model) for details.

## User Model

| Field | Type |
|-------|------|
| name  | string |

For more info, visit [our website](https://example.com).
`
	result, err := parser.Parse([]byte(content), "api.md")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have multiple headings
	headings := filterSymbolsByKind(result.Symbols, KindHeading)
	if len(headings) < 5 {
		t.Errorf("expected at least 5 headings, got %d", len(headings))
	}

	// Should have code block
	codeBlocks := filterSymbolsByKind(result.Symbols, KindCodeBlock)
	if len(codeBlocks) != 1 {
		t.Errorf("expected 1 code block, got %d", len(codeBlocks))
	}

	// Should have links
	links := filterSymbolsByKind(result.Symbols, KindLink)
	if len(links) != 2 {
		t.Errorf("expected 2 links, got %d", len(links))
	}

	// Title should be set
	if result.Metadata["title"] != "API Documentation" {
		t.Errorf("expected title 'API Documentation', got %q", result.Metadata["title"])
	}
}
