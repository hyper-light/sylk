// Package hover provides a floating tooltip for LSP hover information
// with markdown rendering support and syntax-highlighted code blocks.
package hover

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/theme"
)

// Derived from: readable text caps at half the editor width; at minimum 30
// for single-column type signatures.
const (
	popupWidthFraction = 3   // numerator: use 3/4 of editor width
	popupWidthDivisor  = 4   // denominator
	popupMaxCap        = 100 // absolute upper bound
	popupMinWidth      = 30  // hard floor
)

// minVisibleLines is the hard floor for the hover viewport height.
// Derived from: minimum for a type signature + one doc line.
const minVisibleLines = 5

// Hover manages the state and rendering of a floating hover tooltip.
type Hover struct {
	content string // raw content (may be markdown or plaintext)
	line    int    // anchor line in the editor (0-indexed)
	col     int    // anchor column in the editor (0-indexed)
	active  bool

	// Scroll state.
	scrollOffset  int      // first visible line index in renderedLines
	renderedLines []string // cached rendered output (invalidated on change)
	renderWidth   int      // innerWidth used for last render

	// Definition footer (displayed as: symbol ··· pkgPath).
	defSymbol  string // qualified symbol name (e.g. "context.WithCancel")
	defPkgPath string // clean package/import path (e.g. "context")
}

// New creates an inactive Hover instance.
func New() *Hover {
	return &Hover{}
}

// Show activates the hover tooltip with the given content at the anchor position.
func (h *Hover) Show(content string, line, col int) {
	h.content = content
	h.line = line
	h.col = col
	h.active = true
	h.scrollOffset = 0
	h.renderedLines = nil
	h.defSymbol = ""
	h.defPkgPath = ""
}

// Dismiss clears the hover tooltip.
func (h *Hover) Dismiss() {
	h.content = ""
	h.active = false
	h.scrollOffset = 0
	h.renderedLines = nil
	h.defSymbol = ""
	h.defPkgPath = ""
}

// SetDefinition stores the qualified symbol name and package path for
// display in the tooltip footer. Invalidates the render cache.
func (h *Hover) SetDefinition(symbol, pkgPath string) {
	h.defSymbol = symbol
	h.defPkgPath = pkgPath
	h.renderedLines = nil
}

// Active reports whether the hover tooltip is currently displayed.
func (h *Hover) Active() bool {
	return h.active
}

// AnchorLine returns the editor line this hover is anchored to.
func (h *Hover) AnchorLine() int {
	return h.line
}

// AnchorCol returns the editor column this hover is anchored to.
func (h *Hover) AnchorCol() int {
	return h.col
}

// ScrollDown advances the scroll offset by one line.
// Bounds are clamped in View when the max height is known.
func (h *Hover) ScrollDown() { h.scrollOffset++ }

// ScrollUp moves the scroll offset back by one line, clamped to zero.
func (h *Hover) ScrollUp() { h.scrollOffset = max(h.scrollOffset-1, 0) }

// responsivePopupWidth derives the popup width from the available editor width.
func responsivePopupWidth(editorWidth int) int {
	derived := editorWidth * popupWidthFraction / popupWidthDivisor
	return max(min(derived, popupMaxCap), popupMinWidth)
}

// View renders the hover popup as a styled string with markdown formatting,
// syntax-highlighted code blocks, optional definition footer, and scroll
// indicators. maxLines is the maximum number of content rows the popup may
// occupy (caller derives this from available viewport space).
func (h *Hover) View(width, maxLines int, th *theme.Theme) string {
	if !h.active || h.content == "" {
		return ""
	}

	popupWidth := responsivePopupWidth(width)
	innerWidth := popupWidth - 4 // 2 border + 2 padding

	// Render content lazily; invalidated by Show/Dismiss/SetDefinition.
	if h.renderedLines == nil || h.renderWidth != innerWidth {
		styles := newHoverStyles(th)
		h.renderedLines = renderMarkdown(h.content, innerWidth, styles, th)
		h.appendDefFooter(innerWidth, styles, th)
		h.renderWidth = innerWidth
	}

	// Subtract 2 for top/bottom border rows that are part of the popup.
	maxVisible := max(maxLines-2, minVisibleLines)
	totalLines := len(h.renderedLines)

	bgColor := th.Palette.PopupBg
	borderStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Background(bgColor)
	bgStyle := lipgloss.NewStyle().Background(bgColor)
	indicatorStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Muted).
		Background(bgColor)

	var displayLines []string
	if totalLines <= maxVisible {
		// Everything fits — no scrolling needed.
		h.scrollOffset = 0
		displayLines = h.renderedLines
	} else {
		// Clamp scroll offset.
		maxScroll := totalLines - maxVisible
		h.scrollOffset = max(0, min(h.scrollOffset, maxScroll))

		showUp := h.scrollOffset > 0
		showDown := h.scrollOffset+maxVisible < totalLines

		// Indicator rows consume content slots to keep total height constant.
		contentSlots := maxVisible
		if showUp {
			contentSlots--
		}
		if showDown {
			contentSlots--
		}
		contentLines := h.renderedLines[h.scrollOffset : h.scrollOffset+contentSlots]

		if showUp {
			ind := fmt.Sprintf("▲ %d more above", h.scrollOffset)
			displayLines = append(displayLines, centerText(ind, innerWidth, indicatorStyle, bgStyle))
		}
		displayLines = append(displayLines, contentLines...)
		if showDown {
			below := totalLines - (h.scrollOffset + contentSlots)
			ind := fmt.Sprintf("▼ %d more below", below)
			displayLines = append(displayLines, centerText(ind, innerWidth, indicatorStyle, bgStyle))
		}
	}

	var b strings.Builder
	topFill := strings.Repeat("─", innerWidth+2)
	b.WriteString(borderStyle.Render("┌" + topFill + "┐"))
	b.WriteRune('\n')

	pad := bgStyle.Render(" ")
	for _, line := range displayLines {
		padded := padToVisualWidth(line, innerWidth, bgStyle)
		b.WriteString(borderStyle.Render("│") + pad + padded + pad + borderStyle.Render("│"))
		b.WriteRune('\n')
	}

	botFill := strings.Repeat("─", innerWidth+2)
	b.WriteString(borderStyle.Render("└" + botFill + "┘"))

	return b.String()
}

// centerText renders text centered within width, padded with bgStyle.
func centerText(text string, width int, textStyle, bgStyle lipgloss.Style) string {
	styled := textStyle.Render(text)
	textW := lipgloss.Width(styled)
	if textW >= width {
		return styled
	}
	leftPad := (width - textW) / 2
	rightPad := width - textW - leftPad
	return bgStyle.Render(strings.Repeat(" ", leftPad)) + styled + bgStyle.Render(strings.Repeat(" ", rightPad))
}

// appendDefFooter appends a separator and a footer line showing the
// definition symbol (left) and package path as a link (right).
func (h *Hover) appendDefFooter(innerW int, styles hoverStyles, th *theme.Theme) {
	if h.defSymbol == "" && h.defPkgPath == "" {
		return
	}
	bgStyle := lipgloss.NewStyle().Background(th.Palette.PopupBg)
	h.renderedLines = append(h.renderedLines,
		styles.sep.Render(strings.Repeat("─", innerW)))

	defStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Foreground).
		Background(th.Palette.PopupBg).
		Bold(true)
	pkgStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Info).
		Background(th.Palette.PopupBg).
		Underline(true)

	left := defStyle.Render(h.defSymbol)
	right := pkgStyle.Render(h.defPkgPath)
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := max(innerW-leftW-rightW, 1)
	line := left + bgStyle.Render(strings.Repeat(" ", gap)) + right
	h.renderedLines = append(h.renderedLines, padToVisualWidth(line, innerW, bgStyle))
}

// ---------------------------------------------------------------------------
// Markdown rendering
// ---------------------------------------------------------------------------

// hoverStyles holds pre-built lipgloss styles for hover content rendering.
type hoverStyles struct {
	text   lipgloss.Style // normal prose
	code   lipgloss.Style // inline code spans
	codeBg lipgloss.Style // code block line background (padding fill)
	bold   lipgloss.Style // **bold** text
	link   lipgloss.Style // [link text](url)
	sep    lipgloss.Style // --- separators
}

// newHoverStyles creates the style set from a theme.
func newHoverStyles(th *theme.Theme) hoverStyles {
	bg := th.Palette.PopupBg // mantle — uniform popup background
	return hoverStyles{
		text:   lipgloss.NewStyle().Foreground(th.Palette.Foreground).Background(bg),
		code:   lipgloss.NewStyle().Foreground(th.Palette.Primary).Background(bg),
		codeBg: lipgloss.NewStyle().Background(bg),
		bold:   lipgloss.NewStyle().Foreground(th.Palette.Foreground).Background(bg).Bold(true),
		link:   lipgloss.NewStyle().Foreground(th.Palette.Info).Background(bg).Underline(true),
		sep:    lipgloss.NewStyle().Foreground(th.Palette.Border).Background(bg),
	}
}

// isThematicBreak reports whether a line is a markdown thematic break (---, ***).
func isThematicBreak(line string) bool {
	trimmed := strings.TrimSpace(line)
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

// renderMarkdown converts raw markdown (or plaintext) content into pre-styled
// lines ready for boxing. Handles fenced code blocks (with syntax highlighting),
// inline code, bold, links, and thematic breaks.
func renderMarkdown(content string, innerW int, styles hoverStyles, th *theme.Theme) []string {
	if innerW <= 0 {
		innerW = 40
	}

	// Normalize line endings.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	raw := strings.Split(content, "\n")
	var result []string
	inCode := false
	var codeLang string
	var codeBlockLines []string

	for _, line := range raw {
		if strings.HasPrefix(line, "```") {
			if !inCode {
				codeLang = strings.TrimSpace(line[3:])
				codeBlockLines = codeBlockLines[:0]
			} else {
				result = append(result, highlightCodeBlock(codeBlockLines, codeLang, innerW, styles, th)...)
				codeLang = ""
			}
			inCode = !inCode
			continue
		}

		if inCode {
			codeBlockLines = append(codeBlockLines, line)
			continue
		}

		result = append(result, renderProseLine(line, innerW, styles)...)
	}

	// Handle unclosed code block.
	if inCode && len(codeBlockLines) > 0 {
		result = append(result, highlightCodeBlock(codeBlockLines, codeLang, innerW, styles, th)...)
	}

	return result
}

// renderProseLine renders a single non-code line (thematic break, empty, or
// inline-formatted prose with word wrapping).
func renderProseLine(line string, innerW int, styles hoverStyles) []string {
	if isThematicBreak(line) {
		return []string{styles.sep.Render(strings.Repeat("─", innerW))}
	}
	if strings.TrimSpace(line) == "" {
		return []string{""}
	}
	if visibleLength(line) <= innerW {
		return []string{renderInline(line, styles)}
	}
	wrapped := wrapMarkdownLine(line, innerW)
	result := make([]string, len(wrapped))
	for i, seg := range wrapped {
		result[i] = renderInline(seg, styles)
	}
	return result
}

// highlightCodeBlock applies tree-sitter (or regex fallback) syntax
// highlighting to an accumulated code block. Lines are truncated with
// an ellipsis when they exceed innerW, then padded to full width.
func highlightCodeBlock(lines []string, language string, innerW int, styles hoverStyles, th *theme.Theme) []string {
	content := strings.Join(lines, "\n")
	hl := code.NewHighlighterWithBg(th, th.Palette.PopupBg)
	defer hl.Close()

	regions := hl.HighlightContent(content, language)
	result := make([]string, len(lines))
	for i, line := range lines {
		var lineRegions []code.HighlightRegion
		if i < len(regions) {
			lineRegions = regions[i]
		}
		styled := hl.HighlightLine(line, i, lineRegions)
		if lipgloss.Width(styled) > innerW {
			styled = truncateStyled(styled, innerW-1) + styles.codeBg.Render("\u2026")
		}
		result[i] = padToVisualWidth(styled, innerW, styles.codeBg)
	}
	return result
}

// visibleLength returns the number of visible characters in a line after
// removing markdown syntax markers (backticks, **, [text](url) -> text).
func visibleLength(line string) int {
	runes := []rune(line)
	n := 0
	i := 0
	for i < len(runes) {
		if runes[i] == '`' {
			end := indexRune(runes, '`', i+1)
			if end > i {
				n += end - i - 1
				i = end + 1
				continue
			}
		}
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			end := indexDoubleRune(runes, '*', i+2)
			if end > i {
				n += end - i - 2
				i = end + 2
				continue
			}
		}
		if runes[i] == '[' {
			textEnd := indexRune(runes, ']', i+1)
			if textEnd > i && textEnd+1 < len(runes) && runes[textEnd+1] == '(' {
				urlEnd := indexRune(runes, ')', textEnd+2)
				if urlEnd > textEnd {
					for j := i + 1; j < textEnd; j++ {
						if runes[j] != '`' {
							n++
						}
					}
					i = urlEnd + 1
					continue
				}
			}
		}
		n++
		i++
	}
	return n
}

// wrapMarkdownLine splits a markdown line at word boundaries, keeping markdown
// syntax intact within words. Uses visibleLength for width measurement.
func wrapMarkdownLine(line string, maxW int) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		combined := current + " " + word
		if visibleLength(combined) <= maxW {
			current = combined
			continue
		}
		lines = append(lines, current)
		current = word
	}
	lines = append(lines, current)
	return lines
}

// renderInline processes inline markdown formatting in a single line.
// Batches contiguous plain text into a single styled segment.
// Handles: `code`, **bold**, [text](url).
func renderInline(line string, styles hoverStyles) string {
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
		// Inline code: `...`
		if runes[i] == '`' {
			end := indexRune(runes, '`', i+1)
			if end > i {
				flushPlain()
				b.WriteString(styles.code.Render(string(runes[i+1 : end])))
				i = end + 1
				plainStart = i
				continue
			}
		}

		// Bold: **...**
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			end := indexDoubleRune(runes, '*', i+2)
			if end > i {
				flushPlain()
				b.WriteString(styles.bold.Render(string(runes[i+2 : end])))
				i = end + 2
				plainStart = i
				continue
			}
		}

		// Link: [text](url) -- strip decorative backticks from display text.
		if runes[i] == '[' {
			textEnd := indexRune(runes, ']', i+1)
			if textEnd > i && textEnd+1 < len(runes) && runes[textEnd+1] == '(' {
				urlEnd := indexRune(runes, ')', textEnd+2)
				if urlEnd > textEnd {
					flushPlain()
					linkText := strings.ReplaceAll(string(runes[i+1:textEnd]), "`", "")
					b.WriteString(styles.link.Render(linkText))
					i = urlEnd + 1
					plainStart = i
					continue
				}
			}
		}

		i++
	}
	flushPlain()
	return b.String()
}

// indexRune finds the first occurrence of ch in runes starting from pos.
// Returns the index, or -1 if not found.
func indexRune(runes []rune, ch rune, pos int) int {
	for j := pos; j < len(runes); j++ {
		if runes[j] == ch {
			return j
		}
	}
	return -1
}

// indexDoubleRune finds the first occurrence of two consecutive ch runes
// starting from pos. Returns the index of the first ch, or -1 if not found.
func indexDoubleRune(runes []rune, ch rune, pos int) int {
	for j := pos; j+1 < len(runes); j++ {
		if runes[j] == ch && runes[j+1] == ch {
			return j
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Width-aware wrapping and padding
// ---------------------------------------------------------------------------

// truncateStyled truncates a potentially ANSI-styled string to maxW visible
// columns. Walks through runes, tracking visible width and preserving escape
// sequences.
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
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
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

// padToVisualWidth right-pads a (possibly styled) string with spaces to
// reach the desired visible width. Uses lipgloss.Width for ANSI awareness.
func padToVisualWidth(s string, w int, bgStyle lipgloss.Style) string {
	vis := lipgloss.Width(s)
	if vis >= w {
		return s
	}
	return s + bgStyle.Render(strings.Repeat(" ", w-vis))
}
