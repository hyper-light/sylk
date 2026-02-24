package chat

import (
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// blockContext is threaded through all block renderers, accumulating
// output lines and code regions.
type blockContext struct {
	source    []byte          // Original markdown source bytes.
	width     int             // Current available width (shrinks with nesting).
	fullWidth int             // Original full width (for code blocks).
	styles    *chatMdStyles   // Rendering styles.
	theme     *theme.Theme    // Theme for code block highlighting.
	lines     []string        // Accumulated output lines.
	regions   []CodeRegion    // Accumulated code regions.
	listDepth int             // Current list nesting depth.
	codeCache *codeBlockCache // Optional cache for rendered code blocks.
}

// renderMarkdownContent parses raw markdown and renders it to styled terminal
// lines with code regions. This is the primary entry point for the chat panel's
// markdown rendering pipeline. The optional cache stores pre-rendered code blocks.
// Trailing blank separator lines are trimmed from the output.
func renderMarkdownContent(raw string, width int, style lipgloss.Style, th *theme.Theme, cache *codeBlockCache) ([]string, []CodeRegion) {
	lines, regions := renderMarkdownContentRaw(raw, width, style, th, cache)
	lines = trimTrailingBlankLines(lines)
	return lines, regions
}

// renderMarkdownContentRaw is the untrimmed variant of renderMarkdownContent.
// It preserves trailing blank lines emitted by block renderers as inter-block
// separators. Used by the streaming renderer which concatenates multiple
// fragments before applying a single final trim.
func renderMarkdownContentRaw(raw string, width int, style lipgloss.Style, th *theme.Theme, cache *codeBlockCache) ([]string, []CodeRegion) {
	if width <= 0 {
		return nil, nil
	}

	// Normalize line endings.
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	source := []byte(raw)
	tree := parseMarkdown(source)

	styles := newChatMdStyles(th, style)
	ctx := &blockContext{
		source:    source,
		width:     width,
		fullWidth: width,
		styles:    styles,
		theme:     th,
		codeCache: cache,
	}

	renderBlock(tree, ctx)
	return ctx.lines, ctx.regions
}

// trimTrailingBlankLines removes trailing empty strings from the end of
// a rendered line slice. Block renderers append blank lines as inter-block
// separators; the final trailing ones are cosmetic and should be stripped.
func trimTrailingBlankLines(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
