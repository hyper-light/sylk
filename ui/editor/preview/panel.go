// Package preview provides a lightweight read-only file viewer used as a
// sub-panel within the editor split. It shows syntax-highlighted content with
// line numbers and a block cursor but no editing, diagnostics, or selection.
package preview

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/editor/highlight"
	"github.com/adalundhe/sylk/ui/theme"
)

const tabWidth = 4

// blinkHalfPeriod is the duration between cursor visibility toggles.
const blinkHalfPeriod = 530 * time.Millisecond

// Panel is a read-only file viewer for the preview sub-panel.
type Panel struct {
	content     string
	lines       []string
	filePath    string
	language    string
	regions     [][]highlight.HighlightRegion
	highlighter *highlight.Highlighter
	scrollOff   int
	cursorLine  int
	cursorCol   int
	scrollX     int
	width       int
	height      int
	focused     bool
	cursorBlink bool
	lastBlinkAt time.Time
	theme       *theme.Theme
	bounceOff   int
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Panel)(nil)
	_ component.Resizable = (*Panel)(nil)
	_ component.Component = (*Panel)(nil)
)

// New creates a preview Panel with the given theme.
func New(th *theme.Theme) *Panel {
	return &Panel{
		theme:       th,
		highlighter: highlight.NewHighlighter(th),
		cursorBlink: true,
	}
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (p *Panel) ID() component.FocusID  { return component.FocusPreview }
func (p *Panel) Focused() bool           { return p.focused }
func (p *Panel) SetFocused(focused bool) { p.focused = focused }

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

func (p *Panel) SetSize(w, h int) { p.width = w; p.height = h }

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

func (p *Panel) Init() tea.Cmd { return nil }

// HandleTick toggles the cursor blink state on each half-period.
func (p *Panel) HandleTick(t time.Time) {
	if !p.focused {
		p.cursorBlink = true
		p.lastBlinkAt = t
		return
	}
	if t.Sub(p.lastBlinkAt) >= blinkHalfPeriod {
		p.cursorBlink = !p.cursorBlink
		p.lastBlinkAt = t
	}
}

func (p *Panel) Update(msg tea.Msg) (component.Component, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	// Any keystroke resets cursor to visible and restarts blink cycle.
	p.cursorBlink = true
	p.lastBlinkAt = time.Now()

	switch km.String() {
	// Vertical movement.
	case "j", "down":
		p.scrollDown(1)
		p.cursorLine = min(p.cursorLine+1, max(len(p.lines)-1, 0))
		p.clampCursor()
		p.clampCursorCol()
	case "k", "up":
		p.scrollUp(1)
		p.cursorLine = max(p.cursorLine-1, 0)
		p.clampCursor()
		p.clampCursorCol()
	case "g":
		p.scrollOff = 0
		p.cursorLine = 0
		p.cursorCol = 0
		p.scrollX = 0
	case "G":
		p.scrollToEnd()
		p.cursorLine = max(len(p.lines)-1, 0)
		p.clampCursorCol()
	case "ctrl+d":
		half := max(p.height/2, 1)
		p.scrollDown(half)
		p.cursorLine = min(p.cursorLine+half, max(len(p.lines)-1, 0))
		p.clampCursor()
		p.clampCursorCol()
	case "ctrl+u":
		half := max(p.height/2, 1)
		p.scrollUp(half)
		p.cursorLine = max(p.cursorLine-half, 0)
		p.clampCursor()
		p.clampCursorCol()
	case "pgdown":
		p.scrollDown(p.height)
		p.cursorLine = min(p.cursorLine+p.height, max(len(p.lines)-1, 0))
		p.clampCursor()
		p.clampCursorCol()
	case "pgup":
		p.scrollUp(p.height)
		p.cursorLine = max(p.cursorLine-p.height, 0)
		p.clampCursor()
		p.clampCursorCol()

	// Horizontal movement.
	case "h", "left":
		p.cursorCol = max(p.cursorCol-1, 0)
	case "l", "right":
		p.cursorCol = min(p.cursorCol+1, max(p.displayLineLen(p.cursorLine)-1, 0))
	case "0":
		p.cursorCol = 0
		p.scrollX = 0
	case "$":
		p.cursorCol = max(p.displayLineLen(p.cursorLine)-1, 0)
	case "w":
		p.cursorCol = p.nextWordBoundary(p.cursorLine, p.cursorCol)
	case "b":
		p.cursorCol = p.prevWordBoundary(p.cursorLine, p.cursorCol)
	}
	p.ensureCursorVisible()
	return p, nil
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// SetContent sets the file content, path, and language, triggering highlighting.
func (p *Panel) SetContent(content, filePath, language string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	p.content = content
	p.filePath = filePath
	p.language = language
	p.lines = strings.Split(content, "\n")
	p.regions = p.highlighter.Highlight(content, language)
	p.scrollOff = 0
	p.cursorLine = 0
	p.cursorCol = 0
	p.scrollX = 0
}

// ClearFile resets the panel to an empty state.
func (p *Panel) ClearFile() {
	p.content = ""
	p.filePath = ""
	p.language = ""
	p.lines = nil
	p.regions = nil
	p.scrollOff = 0
	p.cursorLine = 0
	p.cursorCol = 0
	p.scrollX = 0
	p.bounceOff = 0
}

// FilePath returns the currently previewed file path.
func (p *Panel) FilePath() string { return p.filePath }

// Language returns the language identifier.
func (p *Panel) Language() string { return p.language }

// ScrollUp scrolls up by n lines. Returns true if the offset changed.
func (p *Panel) ScrollUp(n int) bool {
	prev := p.scrollOff
	p.scrollUp(n)
	return p.scrollOff != prev
}

// ScrollDown scrolls down by n lines. Returns true if the offset changed.
func (p *Panel) ScrollDown(n int) bool {
	prev := p.scrollOff
	p.scrollDown(n)
	return p.scrollOff != prev
}

// ScrollToLine scrolls so that line is visible, placing it near the top.
func (p *Panel) ScrollToLine(line int) {
	p.scrollOff = max(line, 0)
	p.cursorLine = max(line, 0)
	p.clampScroll()
	p.clampCursor()
}

// SetBounceOffset sets the visual bounce displacement for overscroll feedback.
func (p *Panel) SetBounceOffset(offset int) { p.bounceOff = offset }

// Close releases resources held by the highlighter.
func (p *Panel) Close() { p.highlighter.Close() }

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View renders the visible portion of the file with line numbers, syntax
// highlighting, and a block cursor.
func (p *Panel) View() string {
	if len(p.lines) == 0 {
		return p.renderPlaceholder()
	}

	totalLines := len(p.lines)
	gutterW := gutterWidth(totalLines)
	defaultStyle := lipgloss.NewStyle().Foreground(p.theme.Palette.Foreground)
	gutterStyle := lipgloss.NewStyle().Foreground(p.theme.Palette.Muted)
	cursorBg := lipgloss.NewStyle().Reverse(true)

	visEnd := min(p.scrollOff+p.height, totalLines)
	result := make([]string, 0, p.height)

	for i := p.scrollOff; i < visEnd; i++ {
		var lb strings.Builder

		// Gutter (line number).
		numStr := fmt.Sprintf("%*d", gutterW, i+1)
		lb.WriteString(gutterStyle.Render(numStr))
		lb.WriteByte(' ')

		// Expand tabs and remap regions.
		raw := p.lines[i]
		display, colMap := expandTabs(raw, tabWidth)
		var regions []highlight.HighlightRegion
		if i < len(p.regions) {
			regions = remapRegions(p.regions[i], colMap)
		}

		// Apply horizontal scroll: slice display and shift regions.
		display, regions = applyHorizontalScroll(display, regions, p.scrollX)

		// Syntax-highlighted content.
		styled := highlight.RenderLine(display, regions, p.theme.Syntax, defaultStyle)

		// Overlay block cursor when focused and blink phase is visible.
		if p.focused && p.cursorBlink && i == p.cursorLine {
			col := p.cursorCol - p.scrollX
			styled = overlayCursorAt(display, regions, col, p.theme.Syntax, defaultStyle, cursorBg)
		}

		lb.WriteString(styled)

		line := lb.String()
		visWidth := lipgloss.Width(line)
		switch {
		case visWidth > p.width && p.width > 0:
			line = truncateStyled(line, p.width)
		case visWidth < p.width:
			line += strings.Repeat(" ", p.width-visWidth)
		}
		result = append(result, line)
	}

	// Pad remaining viewport with tilde lines.
	for len(result) < p.height {
		tilde := gutterStyle.Render(fmt.Sprintf("%*s", gutterW, "~"))
		pad := strings.Repeat(" ", max(p.width-gutterW, 0))
		result = append(result, tilde+pad)
	}

	// Apply bounce shift.
	result = applyBounceShift(result, p.bounceOff, p.height)

	return strings.Join(result, "\n")
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (p *Panel) scrollUp(n int) {
	p.scrollOff = max(p.scrollOff-n, 0)
}

func (p *Panel) scrollDown(n int) {
	maxOff := max(len(p.lines)-p.height, 0)
	p.scrollOff = min(p.scrollOff+n, maxOff)
}

func (p *Panel) scrollToEnd() {
	p.scrollOff = max(len(p.lines)-p.height, 0)
}

func (p *Panel) clampScroll() {
	maxOff := max(len(p.lines)-p.height, 0)
	p.scrollOff = min(p.scrollOff, maxOff)
	p.scrollOff = max(p.scrollOff, 0)
}

func (p *Panel) clampCursor() {
	p.cursorLine = max(p.cursorLine, p.scrollOff)
	p.cursorLine = min(p.cursorLine, p.scrollOff+p.height-1)
	p.cursorLine = min(p.cursorLine, max(len(p.lines)-1, 0))
}

// clampCursorCol ensures cursorCol is within the current line's display bounds.
func (p *Panel) clampCursorCol() {
	maxCol := max(p.displayLineLen(p.cursorLine)-1, 0)
	p.cursorCol = min(p.cursorCol, maxCol)
}

// displayLineLen returns the display width (after tab expansion) of the line.
func (p *Panel) displayLineLen(lineIdx int) int {
	if lineIdx < 0 || lineIdx >= len(p.lines) {
		return 0
	}
	display, _ := expandTabs(p.lines[lineIdx], tabWidth)
	return len([]rune(display))
}

// ensureCursorVisible adjusts scrollX so the cursor column is within the
// visible content area (width minus gutter).
func (p *Panel) ensureCursorVisible() {
	contentW := p.contentWidth()
	if contentW <= 0 {
		return
	}
	if p.cursorCol < p.scrollX {
		p.scrollX = p.cursorCol
	}
	if p.cursorCol >= p.scrollX+contentW {
		p.scrollX = p.cursorCol - contentW + 1
	}
}

// contentWidth returns the number of display columns available for line
// content (total width minus gutter and separator).
func (p *Panel) contentWidth() int {
	gw := gutterWidth(len(p.lines))
	return max(p.width-gw-1, 1) // gutter + 1 space separator
}

// nextWordBoundary returns the display column of the next word start after col.
func (p *Panel) nextWordBoundary(lineIdx, col int) int {
	if lineIdx < 0 || lineIdx >= len(p.lines) {
		return col
	}
	display, _ := expandTabs(p.lines[lineIdx], tabWidth)
	runes := []rune(display)
	n := len(runes)
	i := min(col, n-1)
	if i < 0 {
		return 0
	}
	// Skip current word characters.
	for i < n && isWordRune(runes[i]) {
		i++
	}
	// Skip whitespace/punctuation.
	for i < n && !isWordRune(runes[i]) {
		i++
	}
	return min(i, max(n-1, 0))
}

// prevWordBoundary returns the display column of the previous word start before col.
func (p *Panel) prevWordBoundary(lineIdx, col int) int {
	if lineIdx < 0 || lineIdx >= len(p.lines) {
		return 0
	}
	display, _ := expandTabs(p.lines[lineIdx], tabWidth)
	runes := []rune(display)
	i := min(col-1, len(runes)-1)
	if i <= 0 {
		return 0
	}
	// Skip whitespace/punctuation backwards.
	for i > 0 && !isWordRune(runes[i]) {
		i--
	}
	// Skip word characters backwards.
	for i > 0 && isWordRune(runes[i-1]) {
		i--
	}
	return max(i, 0)
}

// isWordRune returns true for alphanumeric and underscore characters.
func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_'
}

func (p *Panel) renderPlaceholder() string {
	lines := make([]string, p.height)
	return strings.Join(lines, "\n")
}

// gutterWidth returns the character width needed for line numbers.
func gutterWidth(totalLines int) int {
	w := 1
	n := totalLines
	for n >= 10 {
		w++
		n /= 10
	}
	return w
}

// expandTabs replaces tabs with spaces, returning the display string and a
// column map from source rune index to display column.
func expandTabs(line string, tw int) (string, []int) {
	runes := []rune(line)
	colMap := make([]int, len(runes)+1)
	displayCol := 0
	var buf strings.Builder
	buf.Grow(len(line) + len(line)/4)
	for i, r := range runes {
		colMap[i] = displayCol
		if r == '\t' {
			spaces := tw - (displayCol % tw)
			buf.WriteString(strings.Repeat(" ", spaces))
			displayCol += spaces
		} else {
			buf.WriteRune(r)
			displayCol++
		}
	}
	colMap[len(runes)] = displayCol
	return buf.String(), colMap
}

// remapRegions adjusts highlight regions from source rune offsets to display
// column offsets using the column map from expandTabs.
func remapRegions(regions []highlight.HighlightRegion, colMap []int) []highlight.HighlightRegion {
	if len(regions) == 0 {
		return regions
	}
	out := make([]highlight.HighlightRegion, len(regions))
	for i, r := range regions {
		out[i] = highlight.HighlightRegion{
			StartCol: safeMapCol(colMap, r.StartCol),
			EndCol:   safeMapCol(colMap, r.EndCol),
			Category: r.Category,
		}
	}
	return out
}

func safeMapCol(colMap []int, col int) int {
	if col >= len(colMap) {
		return colMap[len(colMap)-1]
	}
	return colMap[max(col, 0)]
}

// overlayCursorAt renders a line with a block cursor at the given display column.
func overlayCursorAt(display string, regions []highlight.HighlightRegion, col int, styles map[theme.SyntaxCategory]lipgloss.Style, defaultStyle, cursorBg lipgloss.Style) string {
	runes := []rune(display)
	n := len(runes)

	// Cursor past end of line — render content + cursor space.
	if col >= n {
		styled := highlight.RenderLine(display, regions, styles, defaultStyle)
		return styled + cursorBg.Render(" ")
	}
	if col < 0 {
		col = 0
	}

	// Before cursor.
	var before string
	if col > 0 {
		beforeRegions := clipRegions(regions, 0, col)
		before = highlight.RenderLine(string(runes[:col]), beforeRegions, styles, defaultStyle)
	}

	// Cursor character.
	cursorChar := cursorBg.Render(string(runes[col : col+1]))

	// After cursor.
	var after string
	if col+1 < n {
		afterRegions := clipRegions(regions, col+1, n)
		shiftRegions(afterRegions, -(col + 1))
		after = highlight.RenderLine(string(runes[col+1:]), afterRegions, styles, defaultStyle)
	}

	return before + cursorChar + after
}

// clipRegions returns regions clipped to [start, end) in display columns,
// with coordinates relative to the original display string.
func clipRegions(regions []highlight.HighlightRegion, start, end int) []highlight.HighlightRegion {
	out := make([]highlight.HighlightRegion, 0, len(regions))
	for _, r := range regions {
		if r.EndCol <= start || r.StartCol >= end {
			continue
		}
		out = append(out, highlight.HighlightRegion{
			StartCol: max(r.StartCol, start),
			EndCol:   min(r.EndCol, end),
			Category: r.Category,
		})
	}
	return out
}

// shiftRegions offsets all region columns by delta (in place).
func shiftRegions(regions []highlight.HighlightRegion, delta int) {
	for i := range regions {
		regions[i].StartCol += delta
		regions[i].EndCol += delta
	}
}

// applyHorizontalScroll slices the display string and shifts highlight regions
// to account for horizontal scrolling.
func applyHorizontalScroll(display string, regions []highlight.HighlightRegion, scrollX int) (string, []highlight.HighlightRegion) {
	if scrollX <= 0 {
		return display, regions
	}
	runes := []rune(display)
	if scrollX >= len(runes) {
		return "", nil
	}
	scrolled := string(runes[scrollX:])
	scrolledRegions := clipRegions(regions, scrollX, len(runes))
	shiftRegions(scrolledRegions, -scrollX)
	return scrolled, scrolledRegions
}

// truncateStyled truncates a styled string to the given visible width.
func truncateStyled(s string, w int) string {
	if w <= 0 {
		return ""
	}
	// Walk the styled string, counting visible characters (skipping ANSI).
	vis := 0
	inEsc := false
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
		}
		if inEsc {
			b.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
				inEsc = false
			}
			continue
		}
		if vis >= w {
			break
		}
		b.WriteRune(r)
		vis++
	}
	// Close any open ANSI sequences.
	b.WriteString("\x1b[0m")
	return b.String()
}

// applyBounceShift offsets visible lines vertically by bounceOff rows for
// overscroll spring feedback.
func applyBounceShift(lines []string, bounceOff, viewHeight int) []string {
	if bounceOff == 0 || viewHeight <= 0 {
		return lines
	}
	shifted := make([]string, viewHeight)
	for i := range shifted {
		src := i - bounceOff
		if src >= 0 && src < len(lines) {
			shifted[i] = lines[src]
		}
	}
	return shifted
}
