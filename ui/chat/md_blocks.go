package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// Block-level bullet glyphs cycle with nesting depth.
var bulletGlyphs = [...]string{"•", "◦", "▪"}

// renderBlock dispatches a block-level AST node to the appropriate renderer.
// Recursive descent — explicit control rather than ast.Walk for lower
// per-function cyclomatic complexity.
func renderBlock(node ast.Node, ctx *blockContext) {
	switch n := node.(type) {
	case *ast.Document:
		renderChildren(n, ctx)
	case *ast.Heading:
		renderHeading(n, ctx)
	case *ast.Paragraph:
		renderParagraph(n, ctx)
	case *ast.TextBlock:
		// TextBlock is goldmark's representation of tight list item content.
		// Rendered identically to Paragraph: collect inline runs and wrap.
		renderTextBlock(n, ctx)
	case *ast.FencedCodeBlock:
		renderFencedCode(n, ctx)
	case *ast.CodeBlock:
		renderIndentedCode(n, ctx)
	case *ast.List:
		renderList(n, ctx)
	case *ast.ListItem:
		renderListItem(n, ctx, 0, false, 0)
	case *ast.Blockquote:
		renderBlockquote(n, ctx)
	case *ast.ThematicBreak:
		renderThematicBreak(ctx)
	case *ast.HTMLBlock:
		renderHTMLBlock(n, ctx)
	case *east.Table:
		renderTable(n, ctx)
	default:
		// Unknown block — render children for graceful degradation.
		renderChildren(node, ctx)
	}
}

// renderChildren iterates a node's children and renders each block.
func renderChildren(node ast.Node, ctx *blockContext) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		renderBlock(child, ctx)
	}
}

// renderHeading collects inline runs, wraps them with heading style,
// and appends an underline rule for H1/H2.
func renderHeading(n *ast.Heading, ctx *blockContext) {
	level := max(n.Level, 1)
	idx := min(level, 6) - 1
	style := ctx.styles.heading[idx]

	runs := collectInlineRuns(n, ctx.source, style, ctx.styles)
	lines := wrapRuns(runs, ctx.width)
	ctx.lines = append(ctx.lines, lines...)

	switch level {
	case 1:
		ruleWidth := maxLineWidth(lines, ctx.width)
		ctx.lines = append(ctx.lines, ctx.styles.h1Rule.Render(strings.Repeat("═", ruleWidth)))
	case 2:
		ruleWidth := maxLineWidth(lines, ctx.width)
		ctx.lines = append(ctx.lines, ctx.styles.h2Rule.Render(strings.Repeat("─", ruleWidth)))
	}
	ctx.lines = append(ctx.lines, "")
}

// renderParagraph collects inline runs, wraps, and appends a blank line.
func renderParagraph(n *ast.Paragraph, ctx *blockContext) {
	runs := collectInlineRuns(n, ctx.source, ctx.styles.text, ctx.styles)
	lines := wrapRuns(runs, ctx.width)
	ctx.lines = append(ctx.lines, lines...)
	ctx.lines = append(ctx.lines, "")
}

// renderTextBlock handles ast.TextBlock — goldmark's tight list item content.
// Unlike Paragraph, TextBlock omits the trailing blank line since tight lists
// have no inter-item spacing.
func renderTextBlock(n *ast.TextBlock, ctx *blockContext) {
	runs := collectInlineRuns(n, ctx.source, ctx.styles.text, ctx.styles)
	lines := wrapRuns(runs, ctx.width)
	ctx.lines = append(ctx.lines, lines...)
}

// renderFencedCode extracts code lines from AST segments, calls the existing
// renderCodeBlock, and records a CodeRegion.
func renderFencedCode(n *ast.FencedCodeBlock, ctx *blockContext) {
	lang := ""
	if n.Info != nil {
		info := string(n.Info.Segment.Value(ctx.source))
		// Language tag is the first word of the info string.
		if idx := strings.IndexByte(info, ' '); idx >= 0 {
			lang = info[:idx]
		} else {
			lang = info
		}
		lang = normalizeLang(lang)
	}

	var codeLines []string
	for i := range n.Lines().Len() {
		seg := n.Lines().At(i)
		line := string(ctx.source[seg.Start:seg.Stop])
		// Strip trailing newline from each line segment.
		line = strings.TrimRight(line, "\n\r")
		codeLines = append(codeLines, line)
	}

	codeStart := len(ctx.lines)
	ctx.lines = append(ctx.lines, renderCodeBlock(codeLines, lang, ctx.fullWidth, ctx.theme, ctx.codeCache)...)
	ctx.regions = append(ctx.regions, CodeRegion{
		Start:   codeStart,
		End:     len(ctx.lines),
		Content: strings.Join(codeLines, "\n"),
	})
	ctx.lines = append(ctx.lines, "")
}

// renderIndentedCode handles indented code blocks (4-space or tab-indented).
func renderIndentedCode(n *ast.CodeBlock, ctx *blockContext) {
	var codeLines []string
	for i := range n.Lines().Len() {
		seg := n.Lines().At(i)
		line := string(ctx.source[seg.Start:seg.Stop])
		line = strings.TrimRight(line, "\n\r")
		codeLines = append(codeLines, line)
	}

	codeStart := len(ctx.lines)
	ctx.lines = append(ctx.lines, renderCodeBlock(codeLines, "", ctx.fullWidth, ctx.theme, ctx.codeCache)...)
	ctx.regions = append(ctx.regions, CodeRegion{
		Start:   codeStart,
		End:     len(ctx.lines),
		Content: strings.Join(codeLines, "\n"),
	})
	ctx.lines = append(ctx.lines, "")
}

// renderList handles ordered and unordered lists.
func renderList(n *ast.List, ctx *blockContext) {
	ordered := n.IsOrdered()
	start := n.Start
	idx := 0
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if li, ok := child.(*ast.ListItem); ok {
			renderListItem(li, ctx, ctx.listDepth, ordered, start+idx)
			idx++
		}
	}
	// Blank line after top-level lists only.
	if ctx.listDepth == 0 {
		ctx.lines = append(ctx.lines, "")
	}
}

// listIndent is the number of columns per nesting level.
// Derived from: bullet(1) + space(1) = 2 columns.
const listIndent = 2

// renderListItem renders a single list item with bullet/number prefix.
func renderListItem(n *ast.ListItem, ctx *blockContext, depth int, ordered bool, number int) {
	// Build bullet/number prefix.
	var prefix string
	if ordered {
		prefix = ctx.styles.bullet.Render(strings.Repeat(" ", depth*listIndent) + intToStr(number) + ".")
	} else {
		glyph := bulletGlyphs[depth%len(bulletGlyphs)]
		prefix = ctx.styles.bullet.Render(strings.Repeat(" ", depth*listIndent) + glyph)
	}
	prefixWidth := lipgloss.Width(prefix)

	// Render children into a sub-context with reduced width.
	contentWidth := max(ctx.width-prefixWidth-1, 1)
	sub := &blockContext{
		source:    ctx.source,
		width:     contentWidth,
		fullWidth: ctx.fullWidth,
		styles:    ctx.styles,
		theme:     ctx.theme,
		listDepth: depth + 1,
		codeCache: ctx.codeCache,
	}

	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		renderBlock(child, sub)
	}

	// Remove trailing blank line from sub (paragraph adds one, but we
	// don't want double spacing within list items).
	if len(sub.lines) > 0 && sub.lines[len(sub.lines)-1] == "" {
		sub.lines = sub.lines[:len(sub.lines)-1]
	}

	// Prepend prefix to first line, indent continuation lines.
	indent := strings.Repeat(" ", prefixWidth+1)
	for i, line := range sub.lines {
		if i == 0 {
			ctx.lines = append(ctx.lines, prefix+" "+line)
		} else {
			ctx.lines = append(ctx.lines, indent+line)
		}
	}

	// Carry code regions forward with adjusted offsets.
	baseOffset := len(ctx.lines) - len(sub.lines)
	for _, r := range sub.regions {
		ctx.regions = append(ctx.regions, CodeRegion{
			Start:   r.Start + baseOffset,
			End:     r.End + baseOffset,
			Content: r.Content,
		})
	}
}

// quoteBarWidth is the visible column width of the quote bar prefix.
// Derived from: bar(1) + space(1) = 2.
const quoteBarWidth = 2

// renderBlockquote renders children with reduced width and prepends a styled bar.
func renderBlockquote(n *ast.Blockquote, ctx *blockContext) {
	contentWidth := max(ctx.width-quoteBarWidth, 1)
	sub := &blockContext{
		source:    ctx.source,
		width:     contentWidth,
		fullWidth: ctx.fullWidth,
		styles:    ctx.styles,
		theme:     ctx.theme,
		listDepth: ctx.listDepth,
		codeCache: ctx.codeCache,
	}
	// Override text style for blockquote content.
	savedText := ctx.styles.text
	ctx.styles.text = ctx.styles.quoteTx
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		renderBlock(child, sub)
	}
	ctx.styles.text = savedText

	bar := ctx.styles.quoteBar.Render("│") + " "
	for _, line := range sub.lines {
		ctx.lines = append(ctx.lines, bar+line)
	}

	// Carry code regions with adjusted offsets.
	baseOffset := len(ctx.lines) - len(sub.lines)
	for _, r := range sub.regions {
		ctx.regions = append(ctx.regions, CodeRegion{
			Start:   r.Start + baseOffset,
			End:     r.End + baseOffset,
			Content: r.Content,
		})
	}
}

// renderThematicBreak renders a full-width horizontal rule.
func renderThematicBreak(ctx *blockContext) {
	ctx.lines = append(ctx.lines, ctx.styles.sep.Render(strings.Repeat("─", ctx.width)))
	ctx.lines = append(ctx.lines, "")
}

// renderHTMLBlock renders HTML as plain text.
func renderHTMLBlock(n *ast.HTMLBlock, ctx *blockContext) {
	for i := range n.Lines().Len() {
		seg := n.Lines().At(i)
		line := strings.TrimRight(string(ctx.source[seg.Start:seg.Stop]), "\n\r")
		ctx.lines = append(ctx.lines, wrapRuns(splitTextIntoRuns(line, ctx.styles.text, ctx.styles), ctx.width)...)
	}
	ctx.lines = append(ctx.lines, "")
}

// maxLineWidth returns the maximum visible width across lines, capped at maxW.
func maxLineWidth(lines []string, maxW int) int {
	best := 0
	for _, l := range lines {
		w := lipgloss.Width(l)
		if w > best {
			best = w
		}
	}
	return min(best, maxW)
}

// intToStr converts an int to its string representation without fmt.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
