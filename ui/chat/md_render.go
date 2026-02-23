package chat

import (
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// blockContext is threaded through all block renderers, accumulating
// output lines and code regions.
type blockContext struct {
	source    []byte        // Original markdown source bytes.
	width     int           // Current available width (shrinks with nesting).
	fullWidth int           // Original full width (for code blocks).
	styles    *chatMdStyles // Rendering styles.
	theme     *theme.Theme  // Theme for code block highlighting.
	lines     []string      // Accumulated output lines.
	regions   []CodeRegion  // Accumulated code regions.
	listDepth int           // Current list nesting depth.
}

// renderMarkdownContent parses raw markdown and renders it to styled terminal
// lines with code regions. This is the primary entry point for the chat panel's
// markdown rendering pipeline.
func renderMarkdownContent(raw string, width int, style lipgloss.Style, th *theme.Theme) ([]string, []CodeRegion) {
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
	}

	renderBlock(tree, ctx)

	// Trim trailing empty lines (blocks append blank lines as separators,
	// but the last one is unnecessary).
	for len(ctx.lines) > 0 && ctx.lines[len(ctx.lines)-1] == "" {
		ctx.lines = ctx.lines[:len(ctx.lines)-1]
	}

	return ctx.lines, ctx.regions
}
