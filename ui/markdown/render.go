// Package markdown provides a terminal-based markdown renderer that converts
// raw markdown content into styled terminal lines for display in a TUI panel.
package markdown

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/theme"
)

// lineKind classifies a markdown line for dispatch to per-element renderers.
type lineKind int

const (
	lineText lineKind = iota
	lineHeading
	lineCodeFence
	lineUnorderedList
	lineOrderedList
	lineBlockquote
	lineThematicBreak
	lineBlank
)

// headingLevels is the number of distinct heading levels supported.
const headingLevels = 6

// mdStyles holds pre-built lipgloss styles for markdown content rendering.
type mdStyles struct {
	text    lipgloss.Style
	code    lipgloss.Style
	codeBg  lipgloss.Style
	bold    lipgloss.Style
	link    lipgloss.Style
	heading [headingLevels]lipgloss.Style
	h1Rule  lipgloss.Style
	h2Rule  lipgloss.Style
	bullet  lipgloss.Style
	quote   lipgloss.Style
	quoteTx lipgloss.Style
	sep     lipgloss.Style
}

// newMdStyles creates the style set from a theme.
func newMdStyles(th *theme.Theme) mdStyles {
	return mdStyles{
		text:   lipgloss.NewStyle().Foreground(th.Palette.Foreground),
		code:   lipgloss.NewStyle().Foreground(th.Palette.Primary).Background(th.Palette.Subtle),
		codeBg: lipgloss.NewStyle().Background(th.Palette.Subtle),
		bold:   lipgloss.NewStyle().Foreground(th.Palette.Foreground).Bold(true),
		link:   lipgloss.NewStyle().Foreground(th.Palette.Info).Underline(true),
		heading: [headingLevels]lipgloss.Style{
			lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true),
			lipgloss.NewStyle().Foreground(th.Palette.Secondary).Bold(true),
			lipgloss.NewStyle().Foreground(th.Palette.Accent).Bold(true),
			lipgloss.NewStyle().Foreground(th.Palette.Foreground).Bold(true),
			lipgloss.NewStyle().Foreground(th.Palette.Foreground).Bold(true),
			lipgloss.NewStyle().Foreground(th.Palette.Foreground).Bold(true),
		},
		h1Rule:  lipgloss.NewStyle().Foreground(th.Palette.Primary),
		h2Rule:  lipgloss.NewStyle().Foreground(th.Palette.Secondary),
		bullet:  lipgloss.NewStyle().Foreground(th.Palette.Accent),
		quote:   lipgloss.NewStyle().Foreground(th.Palette.Border),
		quoteTx: lipgloss.NewStyle().Foreground(th.Palette.Subtext).Italic(true),
		sep:     lipgloss.NewStyle().Foreground(th.Palette.Border),
	}
}

// RenderMarkdown converts raw markdown content into pre-styled terminal lines.
func RenderMarkdown(content string, width int, th *theme.Theme) []string {
	if width <= 0 {
		width = 40
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	styles := newMdStyles(th)
	raw := strings.Split(content, "\n")
	return renderLines(raw, width, styles, th)
}

// renderLines processes all lines, dispatching to per-element renderers.
func renderLines(raw []string, width int, styles mdStyles, th *theme.Theme) []string {
	var result []string
	var codeLines []string
	var codeLang string
	inCode := false

	for _, line := range raw {
		if classifyLine(line) == lineCodeFence {
			if !inCode {
				codeLang = strings.TrimSpace(line[3:])
				codeLines = codeLines[:0]
			} else {
				result = append(result, renderCodeBlock(codeLines, codeLang, width, styles, th)...)
				codeLang = ""
			}
			inCode = !inCode
			continue
		}
		if inCode {
			codeLines = append(codeLines, line)
			continue
		}
		result = append(result, renderLine(line, width, styles)...)
	}

	// Handle unclosed code block.
	if inCode && len(codeLines) > 0 {
		result = append(result, renderCodeBlock(codeLines, codeLang, width, styles, th)...)
	}
	return result
}

// renderLine dispatches a single non-code line to the appropriate renderer.
func renderLine(line string, width int, styles mdStyles) []string {
	kind := classifyLine(line)
	switch kind {
	case lineBlank:
		return []string{""}
	case lineThematicBreak:
		return []string{styles.sep.Render(strings.Repeat("─", width))}
	case lineHeading:
		return renderHeading(line, width, styles)
	case lineBlockquote:
		return renderBlockquote(line, width, styles)
	case lineUnorderedList:
		return renderUnorderedList(line, width, styles)
	case lineOrderedList:
		return renderOrderedList(line, width, styles)
	default:
		return renderProse(line, width, styles)
	}
}

// classifyLine returns the kind of a markdown line.
func classifyLine(line string) lineKind {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return lineBlank
	}
	if strings.HasPrefix(line, "```") {
		return lineCodeFence
	}
	if isThematicBreak(trimmed) {
		return lineThematicBreak
	}
	if headingLevel(line) > 0 {
		return lineHeading
	}
	if strings.HasPrefix(trimmed, "> ") || trimmed == ">" {
		return lineBlockquote
	}
	if isUnorderedListItem(trimmed) {
		return lineUnorderedList
	}
	if isOrderedListItem(trimmed) {
		return lineOrderedList
	}
	return lineText
}

// ---------------------------------------------------------------------------
// Headings
// ---------------------------------------------------------------------------

// headingLevel returns 1-6 for valid ATX headings, 0 otherwise.
func headingLevel(line string) int {
	level := 0
	for _, r := range line {
		if r == '#' {
			level++
			continue
		}
		break
	}
	if level < 1 || level > headingLevels {
		return 0
	}
	// Must be followed by space or end of line.
	if len(line) > level && line[level] != ' ' {
		return 0
	}
	return level
}

// renderHeading renders an ATX heading as clean styled text (no # prefix).
// H1 gets a full-width underline with ═, H2 gets ─.
func renderHeading(line string, width int, styles mdStyles) []string {
	level := headingLevel(line)
	text := strings.TrimSpace(line[level:])
	style := styles.heading[level-1]

	wrapped := wrapText(text, width)
	out := make([]string, 0, len(wrapped)+2)
	out = append(out, "") // blank line before heading
	for _, seg := range wrapped {
		out = append(out, style.Render(seg))
	}
	switch level {
	case 1:
		ruleW := min(lipgloss.Width(style.Render(wrapped[0])), width)
		out = append(out, styles.h1Rule.Render(strings.Repeat("═", ruleW)))
	case 2:
		ruleW := min(lipgloss.Width(style.Render(wrapped[0])), width)
		out = append(out, styles.h2Rule.Render(strings.Repeat("─", ruleW)))
	}
	return out
}

// ---------------------------------------------------------------------------
// Lists
// ---------------------------------------------------------------------------

// isUnorderedListItem reports whether trimmed starts with -, *, or + followed by space.
func isUnorderedListItem(trimmed string) bool {
	if len(trimmed) < 2 {
		return false
	}
	return (trimmed[0] == '-' || trimmed[0] == '*' || trimmed[0] == '+') && trimmed[1] == ' '
}

// isOrderedListItem reports whether trimmed starts with digits followed by ". ".
func isOrderedListItem(trimmed string) bool {
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(trimmed) {
		return false
	}
	return trimmed[i] == '.' && trimmed[i+1] == ' '
}

// renderUnorderedList renders a bullet list item with hanging indent.
func renderUnorderedList(line string, width int, styles mdStyles) []string {
	trimmed := strings.TrimSpace(line)
	text := trimmed[2:] // skip "- " or "* " or "+ "
	bullet := styles.bullet.Render("  • ")
	bulletW := lipgloss.Width(bullet)
	contentW := max(width-bulletW, 1)
	wrapped := wrapText(text, contentW)
	out := make([]string, len(wrapped))
	indent := strings.Repeat(" ", bulletW)
	for i, seg := range wrapped {
		if i == 0 {
			out[i] = bullet + renderInline(seg, styles)
		} else {
			out[i] = indent + renderInline(seg, styles)
		}
	}
	return out
}

// renderOrderedList renders a numbered list item with hanging indent.
func renderOrderedList(line string, width int, styles mdStyles) []string {
	trimmed := strings.TrimSpace(line)
	dotIdx := strings.Index(trimmed, ". ")
	num := trimmed[:dotIdx]
	text := trimmed[dotIdx+2:]
	prefix := styles.bullet.Render("  " + num + ". ")
	prefixW := lipgloss.Width(prefix)
	contentW := max(width-prefixW, 1)
	wrapped := wrapText(text, contentW)
	out := make([]string, len(wrapped))
	indent := strings.Repeat(" ", prefixW)
	for i, seg := range wrapped {
		if i == 0 {
			out[i] = prefix + renderInline(seg, styles)
		} else {
			out[i] = indent + renderInline(seg, styles)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Blockquotes
// ---------------------------------------------------------------------------

// renderBlockquote renders a "> " prefixed line with a styled left bar.
func renderBlockquote(line string, width int, styles mdStyles) []string {
	trimmed := strings.TrimSpace(line)
	text := ""
	if len(trimmed) > 2 {
		text = trimmed[2:]
	}
	bar := styles.quote.Render("  │ ")
	barW := lipgloss.Width(bar)
	contentW := max(width-barW, 1)
	wrapped := wrapText(text, contentW)
	out := make([]string, len(wrapped))
	for i, seg := range wrapped {
		out[i] = bar + styles.quoteTx.Render(seg)
	}
	return out
}

// ---------------------------------------------------------------------------
// Prose (plain text with inline formatting)
// ---------------------------------------------------------------------------

// renderProse renders a line of text with inline formatting and word wrapping.
func renderProse(line string, width int, styles mdStyles) []string {
	if visibleLength(line) <= width {
		return []string{renderInline(line, styles)}
	}
	wrapped := wrapText(line, width)
	out := make([]string, len(wrapped))
	for i, seg := range wrapped {
		out[i] = renderInline(seg, styles)
	}
	return out
}

// ---------------------------------------------------------------------------
// Code blocks
// ---------------------------------------------------------------------------

// codeBlockPad is the left/right padding inside fenced code blocks.
const codeBlockPad = 2

// renderCodeBlock applies syntax highlighting to a fenced code block.
// Each line gets a Subtle background spanning the full width for visual contrast.
func renderCodeBlock(lines []string, language string, width int, styles mdStyles, th *theme.Theme) []string {
	content := strings.Join(lines, "\n")
	hl := code.NewHighlighterWithBg(th, th.Palette.Subtle)
	defer hl.Close()

	pad := strings.Repeat(" ", codeBlockPad)
	innerW := max(width-codeBlockPad*2, 1)
	regions := hl.HighlightContent(content, language)

	// Blank top line with background.
	blankLine := styles.codeBg.Render(strings.Repeat(" ", width))

	result := make([]string, 0, len(lines)+2)
	result = append(result, blankLine)
	for i, line := range lines {
		var lineRegions []code.HighlightRegion
		if i < len(regions) {
			lineRegions = regions[i]
		}
		styled := hl.HighlightLine(line, i, lineRegions)
		vis := lipgloss.Width(styled)
		if vis > innerW {
			styled = truncateStyled(styled, innerW-1) + styles.codeBg.Render("\u2026")
			vis = innerW
		}
		rightPad := max(width-codeBlockPad*2-vis, 0)
		result = append(result, styles.codeBg.Render(pad)+styled+styles.codeBg.Render(strings.Repeat(" ", rightPad)+pad))
	}
	result = append(result, blankLine)
	return result
}

// ---------------------------------------------------------------------------
// Inline formatting
// ---------------------------------------------------------------------------

// renderInline processes inline markdown formatting in a single line.
// Handles: `code`, **bold**, [text](url).
func renderInline(line string, styles mdStyles) string {
	var b strings.Builder
	runes := []rune(line)
	i := 0
	plainStart := 0

	flushPlain := func() {
		if plainStart < i {
			b.WriteString(styles.text.Render(string(runes[plainStart:i])))
		}
	}

	for i < len(runes) {
		if runes[i] == '`' {
			if end := indexRune(runes, '`', i+1); end > i {
				flushPlain()
				b.WriteString(styles.code.Render(" " + string(runes[i+1:end]) + " "))
				i = end + 1
				plainStart = i
				continue
			}
		}
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			if end := indexDoubleRune(runes, '*', i+2); end > i {
				flushPlain()
				b.WriteString(styles.bold.Render(string(runes[i+2 : end])))
				i = end + 2
				plainStart = i
				continue
			}
		}
		if runes[i] == '[' {
			if textEnd, urlEnd := linkBounds(runes, i); urlEnd > 0 {
				flushPlain()
				linkText := strings.ReplaceAll(string(runes[i+1:textEnd]), "`", "")
				b.WriteString(styles.link.Render(linkText))
				i = urlEnd + 1
				plainStart = i
				continue
			}
		}
		i++
	}
	flushPlain()
	return b.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isThematicBreak reports whether a trimmed line is a markdown thematic break.
func isThematicBreak(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	ch := trimmed[0]
	if ch != '-' && ch != '*' && ch != '_' {
		return false
	}
	for _, r := range trimmed {
		if r != rune(ch) && r != ' ' {
			return false
		}
	}
	return true
}

// wrapText splits text at word boundaries to fit within maxW visible columns.
func wrapText(text string, maxW int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) <= maxW {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	return append(lines, current)
}

// visibleLength returns the visible character count ignoring markdown syntax.
func visibleLength(line string) int {
	runes := []rune(line)
	n := 0
	i := 0
	for i < len(runes) {
		if runes[i] == '`' {
			if end := indexRune(runes, '`', i+1); end > i {
				n += end - i - 1
				i = end + 1
				continue
			}
		}
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			if end := indexDoubleRune(runes, '*', i+2); end > i {
				n += end - i - 2
				i = end + 2
				continue
			}
		}
		if runes[i] == '[' {
			if textEnd, urlEnd := linkBounds(runes, i); urlEnd > 0 {
				for j := i + 1; j < textEnd; j++ {
					if runes[j] != '`' {
						n++
					}
				}
				i = urlEnd + 1
				continue
			}
		}
		n++
		i++
	}
	return n
}

// linkBounds returns the text end and url end positions for a [text](url) link
// starting at runes[i]. Returns (0, 0) if no valid link.
func linkBounds(runes []rune, i int) (textEnd, urlEnd int) {
	textEnd = indexRune(runes, ']', i+1)
	if textEnd <= i || textEnd+1 >= len(runes) || runes[textEnd+1] != '(' {
		return 0, 0
	}
	urlEnd = indexRune(runes, ')', textEnd+2)
	if urlEnd <= textEnd {
		return 0, 0
	}
	return textEnd, urlEnd
}

// indexRune finds the first occurrence of ch in runes starting from pos.
func indexRune(runes []rune, ch rune, pos int) int {
	for j := pos; j < len(runes); j++ {
		if runes[j] == ch {
			return j
		}
	}
	return -1
}

// indexDoubleRune finds the first occurrence of two consecutive ch runes from pos.
func indexDoubleRune(runes []rune, ch rune, pos int) int {
	for j := pos; j+1 < len(runes); j++ {
		if runes[j] == ch && runes[j+1] == ch {
			return j
		}
	}
	return -1
}

// truncateStyled truncates a styled string to maxW visible columns.
func truncateStyled(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	vis := lipgloss.Width(s)
	if vis <= maxW {
		return s
	}
	var b strings.Builder
	col := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			b.WriteRune(r)
			continue
		}
		if inEsc {
			b.WriteRune(r)
			if isCSITerminator(r) {
				inEsc = false
			}
			continue
		}
		if col >= maxW {
			break
		}
		b.WriteRune(r)
		col++
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// isCSITerminator reports whether r terminates a CSI escape sequence.
func isCSITerminator(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~'
}

