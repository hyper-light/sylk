// Package preview provides a lightweight read-only file viewer used as a
// sub-panel within the editor split. It shows syntax-highlighted content with
// line numbers and a block cursor but no editing, diagnostics, or selection.
package preview

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/editor/highlight"
	"github.com/adalundhe/sylk/ui/editor/hover"
	"github.com/adalundhe/sylk/ui/theme"
)

const tabWidth = 4

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
	focused bool
	theme   *theme.Theme
	bounceOff   int
	hoverPopup  *hover.Hover
	hoverActive bool
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
		hoverPopup:  hover.New(),
	}
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (p *Panel) ID() component.FocusID  { return component.FocusPreview }
func (p *Panel) Focused() bool           { return p.focused }
func (p *Panel) SetFocused(focused bool) {
	p.focused = focused
}

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

func (p *Panel) SetSize(w, h int) { p.width = w; p.height = h }

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

func (p *Panel) Init() tea.Cmd { return nil }

// HandleTick is retained for interface compatibility. Cursor blink is now
// driven by the app's centralized BlinkMsg timer.
func (p *Panel) HandleTick(_ time.Time) {}

func (p *Panel) Update(msg tea.Msg) (component.Component, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}

	// Dismiss hover on any keystroke (movement changes viewport context).
	p.DismissHover()

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
	p.DismissHover()
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
	p.DismissHover()
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
func (p *Panel) View(cursorVisible bool) string {
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
		if p.focused && cursorVisible && i == p.cursorLine {
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

	// Overlay hover popup if active.
	if p.hoverActive && p.hoverPopup.Active() {
		p.overlayHoverPopup(result)
	}

	// Apply bounce shift.
	result = applyBounceShift(result, p.bounceOff, p.height)

	return strings.Join(result, "\n")
}

// ---------------------------------------------------------------------------
// Hover popup
// ---------------------------------------------------------------------------

// HoverActive reports whether the hover tooltip is visible.
func (p *Panel) HoverActive() bool { return p.hoverActive }

// DismissHover hides the hover tooltip.
func (p *Panel) DismissHover() {
	p.hoverPopup.Dismiss()
	p.hoverActive = false
}

// ShowHover activates the hover tooltip at the given buffer position.
func (p *Panel) ShowHover(content string, line, col int) {
	p.hoverPopup.Show(content, line, col)
	p.hoverActive = true
}

// SetHoverDefinition stores the qualified symbol name and package path
// on the active hover tooltip for display in the footer.
func (p *Panel) SetHoverDefinition(symbol, pkgPath string) {
	if !p.hoverActive {
		return
	}
	p.hoverPopup.SetDefinition(symbol, pkgPath)
}

// ScrollHoverDown scrolls the hover popup content down by one line.
func (p *Panel) ScrollHoverDown() { p.hoverPopup.ScrollDown() }

// ScrollHoverUp scrolls the hover popup content up by one line.
func (p *Panel) ScrollHoverUp() { p.hoverPopup.ScrollUp() }

// SetCursorFromViewport converts viewport-local (x, y) to a buffer position
// and updates the cursor. Resets blink to visible.
func (p *Panel) SetCursorFromViewport(x, y int) {
	line, bufCol, ok := p.ViewportToBufferPos(x, y)
	if !ok {
		return
	}
	p.cursorLine = line
	// Convert buffer column (rune index) to display column (post-tab-expansion).
	if line >= 0 && line < len(p.lines) {
		_, colMap := expandTabs(p.lines[line], tabWidth)
		p.cursorCol = safeMapCol(colMap, bufCol)
	} else {
		p.cursorCol = bufCol
	}
	p.clampCursorCol()
}

// ViewportToBufferPos converts viewport-local (x, y) coordinates to a
// buffer (line, col) position. Returns false when the position is outside
// the file content.
func (p *Panel) ViewportToBufferPos(x, y int) (line, col int, ok bool) {
	totalLines := len(p.lines)
	gw := gutterWidth(totalLines)
	line = y + p.scrollOff
	if line < 0 || line >= totalLines {
		return 0, 0, false
	}
	screenCol := max(x-gw-1, 0) + p.scrollX // gutter + 1 space separator
	col = p.screenToBufferCol(line, screenCol)
	return line, col, true
}

// screenToBufferCol converts a display column (post-tab-expansion) to the
// corresponding buffer column (rune index) for the given line.
func (p *Panel) screenToBufferCol(lineIdx, screenCol int) int {
	if lineIdx < 0 || lineIdx >= len(p.lines) {
		return screenCol
	}
	runes := []rune(p.lines[lineIdx])
	visCol := 0
	for i, r := range runes {
		charWidth := 1
		if r == '\t' {
			charWidth = tabWidth - (visCol % tabWidth)
		}
		if screenCol < visCol+charWidth {
			return i
		}
		visCol += charWidth
	}
	return len(runes)
}

// IsWordCharAtPos reports whether the rune at (line, col) is a word
// character (letter, digit, or underscore).
func (p *Panel) IsWordCharAtPos(line, col int) bool {
	if line < 0 || line >= len(p.lines) {
		return false
	}
	runes := []rune(p.lines[line])
	if col < 0 || col >= len(runes) {
		return false
	}
	r := runes[col]
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// WordBoundsAt returns the start and end column of the word at (line, col).
// If the position is not on a word character, returns (col, col).
func (p *Panel) WordBoundsAt(line, col int) (start, end int) {
	if line < 0 || line >= len(p.lines) {
		return col, col
	}
	runes := []rune(p.lines[line])
	if col < 0 || col >= len(runes) {
		return col, col
	}
	isWord := func(c int) bool {
		if c < 0 || c >= len(runes) {
			return false
		}
		r := runes[c]
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
	}
	if !isWord(col) {
		return col, col
	}
	start = col
	for start > 0 && isWord(start-1) {
		start--
	}
	end = col + 1
	for end < len(runes) && isWord(end) {
		end++
	}
	return start, end
}

// WordAt returns the word text at (line, col), or "" if not on a word.
func (p *Panel) WordAt(line, col int) string {
	start, end := p.WordBoundsAt(line, col)
	if start == end {
		return ""
	}
	runes := []rune(p.lines[line])
	return string(runes[start:end])
}

// IsInsideHoverPopup reports whether viewport-local (x, y) falls within
// the currently displayed hover popup.
func (p *Panel) IsInsideHoverPopup(x, y int) bool {
	if !p.hoverActive || !p.hoverPopup.Active() {
		return false
	}
	startRow, col, height, width := p.hoverPlacement()
	return y >= startRow && y < startRow+height && x >= col && x < col+width
}

// contentToViewCol converts a buffer (line, col) to a viewport visual column
// (including gutter and separator, minus horizontal scroll).
func (p *Panel) contentToViewCol(lineNum, col int) int {
	totalLines := len(p.lines)
	gw := gutterWidth(totalLines)
	if lineNum < 0 || lineNum >= totalLines {
		return gw + 1
	}
	_, colMap := expandTabs(p.lines[lineNum], tabWidth)
	displayCol := safeMapCol(colMap, col)
	return gw + 1 + max(displayCol-p.scrollX, 0)
}

// hoverPlacement computes the position and size of the hover popup
// within the viewport. Returns (startRow, col, height, width).
func (p *Panel) hoverPlacement() (startRow, col, height, width int) {
	anchorLine := p.hoverPopup.AnchorLine()
	anchorRow := anchorLine - p.scrollOff
	viewHeight := p.height

	spaceAbove := anchorRow
	spaceBelow := viewHeight - anchorRow - 1
	maxZone := max(spaceAbove, spaceBelow)

	popup := p.hoverPopup.View(p.width, maxZone, p.theme)
	if popup == "" {
		return 0, 0, 0, 0
	}
	popupLines := strings.Split(popup, "\n")
	height = len(popupLines)

	for _, pl := range popupLines {
		if w := lipgloss.Width(pl); w > width {
			width = w
		}
	}

	col = p.contentToViewCol(anchorLine, p.hoverPopup.AnchorCol())
	col = max(col, 0)
	if p.width > 0 && width > 0 {
		col = min(col, max(p.width-width, 0))
	}

	// Place above anchor if room, otherwise below.
	startRow = anchorRow - height
	if startRow < 0 {
		startRow = anchorRow + 1
	}
	return startRow, col, height, width
}

// overlayHoverPopup renders the hover tooltip and splices it into the
// viewport lines at the correct position.
func (p *Panel) overlayHoverPopup(lines []string) {
	anchorLine := p.hoverPopup.AnchorLine()
	anchorRow := anchorLine - p.scrollOff
	if anchorRow < 0 || anchorRow >= p.height {
		return
	}

	viewHeight := len(lines)
	spaceAbove := anchorRow
	spaceBelow := viewHeight - anchorRow - 1
	maxZone := max(spaceAbove, spaceBelow)

	popup := p.hoverPopup.View(p.width, maxZone, p.theme)
	if popup == "" {
		return
	}
	popupLines := strings.Split(popup, "\n")
	popupHeight := len(popupLines)

	popupWidth := 0
	for _, pl := range popupLines {
		if w := lipgloss.Width(pl); w > popupWidth {
			popupWidth = w
		}
	}

	col := p.contentToViewCol(anchorLine, p.hoverPopup.AnchorCol())
	col = max(col, 0)
	if p.width > 0 && popupWidth > 0 {
		col = min(col, max(p.width-popupWidth, 0))
	}

	startRow := anchorRow - popupHeight
	if startRow < 0 {
		startRow = anchorRow + 1
	}

	for i, pLine := range popupLines {
		row := startRow + i
		if row < 0 || row >= len(lines) {
			continue
		}
		original := lines[row]
		left := truncateStyled(original, col)
		right := skipStyledCols(original, col+popupWidth)
		lines[row] = left + pLine + right
	}
}

// skipStyledCols drops the first skip visible columns from a styled string
// (containing ANSI escape sequences) and returns the remainder with a reset
// prefix so subsequent styling is clean.
func skipStyledCols(s string, skip int) string {
	if skip <= 0 {
		return s
	}
	vis := 0
	i := 0
	for i < len(s) && vis < skip {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSIEnd(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j
			continue
		}
		i++
		vis++
	}
	if i >= len(s) {
		return ""
	}
	return "\x1b[0m" + s[i:]
}

// isCSIEnd reports whether b is a CSI sequence terminator (ASCII letter or ~).
func isCSIEnd(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~'
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
