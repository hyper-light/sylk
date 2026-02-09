package code

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
)

// textSelection tracks a mouse-driven text selection in document coordinates.
// Byte offsets are used for columns (matching HighlightRegion).
type textSelection struct {
	startLine int
	startCol  int
	endLine   int
	endCol    int
	dragging  bool // Mouse button is currently held.
	set       bool // A selection has been established.
}

// active reports whether a non-empty selection exists.
func (s textSelection) active() bool {
	return s.set && (s.startLine != s.endLine || s.startCol != s.endCol)
}

// normalize returns selection bounds in forward order (start <= end).
func (s textSelection) normalize() (sl, sc, el, ec int) {
	if s.startLine < s.endLine || (s.startLine == s.endLine && s.startCol <= s.endCol) {
		return s.startLine, s.startCol, s.endLine, s.endCol
	}
	return s.endLine, s.endCol, s.startLine, s.startCol
}

// colRange returns the selected byte range for a given line.
// Returns (-1, -1) if the line has no selection.
func (s textSelection) colRange(lineIdx, lineLen int) (int, int) {
	sl, sc, el, ec := s.normalize()
	if lineIdx < sl || lineIdx > el {
		return -1, -1
	}
	start, end := 0, lineLen
	if lineIdx == sl {
		start = min(sc, lineLen)
	}
	if lineIdx == el {
		end = min(ec, lineLen)
	}
	return start, end
}

// Model is the Bubble Tea model for the code viewer panel.
// It displays source files with syntax highlighting, line numbers,
// and virtual scrolling (only visible lines are rendered).
type Model struct {
	content          string
	lines            []string
	filePath         string
	language         string
	highlightedLines []string // cached rendered output per line
	highlightRegions [][]HighlightRegion
	scrollOffset     int
	cursorLine       int
	width            int
	height           int
	focused          bool
	theme            *theme.Theme
	highlighter      *Highlighter
	showLineNumbers  bool
	wordWrap         bool
	bounceOffset     int           // Visual line displacement from overscroll bounce.
	selection        textSelection // Mouse-driven text selection state.
	diagnostics      []lsp.Diagnostic // LSP diagnostics for the current file.
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a code viewer Model with the given theme.
func New(th *theme.Theme) *Model {
	return &Model{
		theme:           th,
		highlighter:     NewHighlighter(th),
		showLineNumbers: true,
	}
}

// FilePath returns the current file path.
func (m *Model) FilePath() string { return m.filePath }

// Content returns the current raw content.
func (m *Model) Content() string { return m.content }

// Language returns the current language identifier.
func (m *Model) Language() string { return m.language }

// SetContent sets the source content, file path, and language, then
// triggers a re-highlight of the content.
func (m *Model) SetContent(content, filePath, language string) {
	// Normalize line endings: \r\n → \n, stray \r → \n.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	m.content = content
	m.filePath = filePath
	m.language = language
	m.lines = strings.Split(content, "\n")
	m.scrollOffset = 0
	m.cursorLine = 0
	m.selection = textSelection{}
	m.diagnostics = nil
	m.reHighlight()
}

// ClearFile resets the panel to an empty state with no file loaded.
func (m *Model) ClearFile() {
	m.content = ""
	m.filePath = ""
	m.language = ""
	m.lines = nil
	m.highlightedLines = nil
	m.highlightRegions = nil
	m.scrollOffset = 0
	m.cursorLine = 0
	m.selection = textSelection{}
	m.diagnostics = nil
}

// SetDiagnostics updates the diagnostics for the given file. If the file
// matches the currently displayed file, the diagnostics are stored; otherwise
// they are ignored.
func (m *Model) SetDiagnostics(filePath string, diags []lsp.Diagnostic) {
	if filePath != m.filePath {
		return
	}
	m.diagnostics = diags
}

// reHighlight runs the highlighter on the current content and caches
// the per-line rendered output.
func (m *Model) reHighlight() {
	m.highlightRegions = m.highlighter.HighlightContent(m.content, m.language)
	m.rebuildHighlightCache()
}

// rebuildHighlightCache renders each line through the highlighter and
// stores the styled output.
func (m *Model) rebuildHighlightCache() {
	lineCount := len(m.lines)
	m.highlightedLines = make([]string, lineCount)

	for i, line := range m.lines {
		var regions []HighlightRegion
		if i < len(m.highlightRegions) {
			regions = m.highlightRegions[i]
		}
		m.highlightedLines[i] = m.highlighter.HighlightLine(line, i, regions)
	}
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns no initial command.
func (m *Model) Init() tea.Cmd { return nil }

// Update processes incoming messages and returns the updated component.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	keyMsg, ok := incoming.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	return m.handleKey(keyMsg)
}

// View renders the visible portion of the code with optional line numbers.
// When a file is loaded, a header line showing the filename is prepended.
// Output is always padded to exactly m.height lines for stable frame sizes.
func (m *Model) View() string {
	viewHeight := m.viewportHeight()
	lineCount := len(m.lines)

	if m.filePath == "" {
		return m.renderPlaceholder()
	}

	var header string
	header = m.renderFileHeader()

	if lineCount == 0 {
		code := padLines("", viewHeight)
		if header != "" {
			return header + "\n" + code
		}
		return code
	}

	visibleEnd := min(m.scrollOffset+viewHeight, lineCount)
	visibleStart := m.scrollOffset

	gutterWidth := m.gutterWidth()
	contentWidth := m.contentWidth(gutterWidth)
	gutterStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	var b strings.Builder
	// Pre-allocate: each line needs gutter + separator + content + newline.
	b.Grow((gutterWidth + contentWidth + 2) * viewHeight)

	for i := visibleStart; i < visibleEnd; i++ {
		if i > visibleStart {
			b.WriteByte('\n')
		}
		m.renderViewLine(&b, i, gutterWidth, contentWidth, gutterStyle)
	}

	code := padLines(applyCodeBounceShift(b.String(), m.bounceOffset, viewHeight), viewHeight)
	if header != "" {
		return header + "\n" + code
	}
	return code
}

// renderFileHeader renders a header line: " filename.ext ──────────────".
func (m *Model) renderFileHeader() string {
	name := filepath.Base(m.filePath)
	nameStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted).Bold(true)
	lineStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)

	text := nameStyle.Render(" " + name + " ")
	textWidth := lipgloss.Width(text)
	lineWidth := max(m.width-textWidth, 0)

	return text + lineStyle.Render(strings.Repeat("─", lineWidth))
}

// renderPlaceholder renders a vertically and horizontally centered placeholder
// message when no file is open.
func (m *Model) renderPlaceholder() string {
	const placeholder = "Select a file to view."
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	textWidth := lipgloss.Width(placeholder)
	padLeft := max((m.width-textWidth)/2, 0)
	centeredLine := strings.Repeat(" ", padLeft) + style.Render(placeholder)

	lines := make([]string, m.height)
	midRow := m.height / 2
	for i := range lines {
		if i == midRow {
			lines[i] = centeredLine
		}
	}
	return strings.Join(lines, "\n")
}

// padLines ensures s contains exactly targetLines lines by appending empty lines.
func padLines(s string, targetLines int) string {
	if targetLines <= 0 {
		return s
	}
	if s == "" {
		return strings.Repeat("\n", max(targetLines-1, 0))
	}
	current := strings.Count(s, "\n") + 1
	if current < targetLines {
		s += strings.Repeat("\n", targetLines-current)
	}
	return s
}

// renderViewLine writes a single viewport line (gutter + content) to the builder,
// padded or truncated to exactly m.width visual characters so that the border
// wrapper sees consistent line widths regardless of wide-character content.
func (m *Model) renderViewLine(b *strings.Builder, lineIdx, gutterWidth, contentWidth int, gutterStyle lipgloss.Style) {
	var lb strings.Builder

	if m.showLineNumbers {
		if diag, ok := m.diagnosticForLine(lineIdx); ok {
			sign := severitySign[diag.Severity]
			if sign == "" {
				sign = "?"
			}
			color := m.diagnosticColor(diag.Severity)
			signStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
			numStr := fmt.Sprintf("%*s", gutterWidth, sign)
			lb.WriteString(signStyle.Render(numStr))
		} else {
			numStr := fmt.Sprintf("%*d", gutterWidth, lineIdx+1)
			lb.WriteString(gutterStyle.Render(numStr))
		}
		lb.WriteByte(' ')
	}

	lb.WriteString(m.styledLine(lineIdx))

	line := lb.String()
	visWidth := lipgloss.Width(line)

	switch {
	case visWidth > m.width && m.width > 0:
		line = truncateStyledLine(line, m.width)
	case visWidth < m.width:
		line += strings.Repeat(" ", m.width-visWidth)
	}

	b.WriteString(line)
}

// styledLine returns the highlighted line, overlaying selection background
// when the line overlaps the active selection.
func (m *Model) styledLine(idx int) string {
	if m.lineHasSelection(idx) {
		return m.styledLineWithSelection(idx)
	}
	if idx < len(m.highlightedLines) {
		return m.highlightedLines[idx]
	}
	return m.lineAt(idx)
}

// lineHasSelection reports whether line idx overlaps the active selection.
func (m *Model) lineHasSelection(idx int) bool {
	if !m.selection.active() {
		return false
	}
	start, _ := m.selection.colRange(idx, m.lineLen(idx))
	return start >= 0
}

// styledLineWithSelection re-renders a line with selection background.
func (m *Model) styledLineWithSelection(idx int) string {
	line := m.lineAt(idx)
	regions := m.regionsAt(idx)
	selStart, selEnd := m.selection.colRange(idx, len(line))
	return m.highlighter.HighlightLineWithSelection(line, regions, selStart, selEnd, m.theme.Palette.Selection)
}

func (m *Model) lineAt(idx int) string {
	if idx < len(m.lines) {
		return m.lines[idx]
	}
	return ""
}

func (m *Model) regionsAt(idx int) []HighlightRegion {
	if idx < len(m.highlightRegions) {
		return m.highlightRegions[idx]
	}
	return nil
}

func (m *Model) lineLen(idx int) int {
	if idx < len(m.lines) {
		return len(m.lines[idx])
	}
	return 0
}

// ---------------------------------------------------------------------------
// Diagnostic rendering
// ---------------------------------------------------------------------------

// severitySign maps diagnostic severity to a gutter sign character.
var severitySign = map[lsp.DiagnosticSeverity]string{
	lsp.SeverityError:       "E",
	lsp.SeverityWarning:     "W",
	lsp.SeverityInformation: "I",
	lsp.SeverityHint:        "H",
}

// diagnosticForLine returns the highest-severity diagnostic on a given line.
func (m *Model) diagnosticForLine(line int) (lsp.Diagnostic, bool) {
	var best lsp.Diagnostic
	found := false
	for _, d := range m.diagnostics {
		if d.Range.Start.Line != line {
			continue
		}
		if !found || d.Severity < best.Severity {
			best = d
			found = true
		}
	}
	return best, found
}

// diagnosticColor returns the palette color for a diagnostic severity.
func (m *Model) diagnosticColor(severity lsp.DiagnosticSeverity) lipgloss.Color {
	switch severity {
	case lsp.SeverityError:
		return m.theme.Palette.Error
	case lsp.SeverityWarning:
		return m.theme.Palette.Warning
	case lsp.SeverityInformation:
		return m.theme.Palette.Info
	default:
		return m.theme.Palette.Muted
	}
}

// truncateVisible limits visible content to the given width. This is a simple
// rune-count truncation for word-wrap mode.
func truncateVisible(s string, maxWidth int) string {
	count := 0
	for i := range s {
		count++
		if count > maxWidth {
			return s[:i]
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the focus identifier for this code viewer instance.
// Returns the focusOverride if set, otherwise FocusCodeViewer.
func (m *Model) ID() component.FocusID { return component.FocusCodeViewer }

// Focused returns whether the code viewer has focus.
func (m *Model) Focused() bool { return m.focused }

// SetFocused sets the focus state.
func (m *Model) SetFocused(focused bool) { m.focused = focused }

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates the available dimensions for the code viewer.
func (m *Model) SetSize(width, height int) {
	m.width = max(width, 0)
	m.height = max(height, 0)
	m.clampScroll()
}

// ---------------------------------------------------------------------------
// Key dispatch (table-driven)
// ---------------------------------------------------------------------------

// keyAction is a function that handles a specific key and returns a command.
type keyAction func(m *Model) tea.Cmd

// keyEntry maps a key description to an action.
type keyEntry struct {
	match  func(k tea.KeyMsg) bool
	action keyAction
}

// keyTable defines the key bindings for the code viewer when focused.
var keyTable = []keyEntry{
	{matchKey("j"), actionScrollDown},
	{matchKey("down"), actionScrollDown},
	{matchKey("k"), actionScrollUp},
	{matchKey("up"), actionScrollUp},
	{matchKey("g"), actionScrollTop},
	{matchKey("home"), actionScrollTop},
	{matchKey("G"), actionScrollBottom},
	{matchKey("end"), actionScrollBottom},
	{matchKey("pgdown"), actionPageDown},
	{matchKey("pgup"), actionPageUp},
	{matchKey("ctrl+l"), actionToggleLineNumbers},
	{matchKey("ctrl+w"), actionToggleWordWrap},
}

// handleKey dispatches a key message through the table.
func (m *Model) handleKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	if !m.focused {
		return m, nil
	}
	if cmd, handled := m.dispatch(k); handled {
		return m, cmd
	}
	return m, nil
}

// dispatch tries each entry in keyTable and returns (cmd, true) on first match.
func (m *Model) dispatch(k tea.KeyMsg) (tea.Cmd, bool) {
	for _, e := range keyTable {
		if e.match(k) {
			return e.action(m), true
		}
	}
	return nil, false
}

// matchKey returns a predicate that matches a key by its string representation.
func matchKey(desc string) func(tea.KeyMsg) bool {
	return func(k tea.KeyMsg) bool { return k.String() == desc }
}

// ---------------------------------------------------------------------------
// Key actions
// ---------------------------------------------------------------------------

func actionScrollDown(m *Model) tea.Cmd {
	maxOffset := max(len(m.lines)-m.viewportHeight(), 0)
	m.scrollOffset = min(m.scrollOffset+1, maxOffset)
	m.cursorLine = min(m.scrollOffset+m.viewportHeight()-1, max(len(m.lines)-1, 0))
	return nil
}

func actionScrollUp(m *Model) tea.Cmd {
	m.scrollOffset = max(m.scrollOffset-1, 0)
	m.cursorLine = m.scrollOffset
	return nil
}

func actionScrollTop(m *Model) tea.Cmd {
	m.cursorLine = 0
	m.scrollOffset = 0
	return nil
}

func actionScrollBottom(m *Model) tea.Cmd {
	m.cursorLine = max(len(m.lines)-1, 0)
	m.scrollToEnd()
	return nil
}

func actionPageDown(m *Model) tea.Cmd {
	pageSize := m.viewportHeight()
	maxOffset := max(len(m.lines)-m.viewportHeight(), 0)
	m.scrollOffset = min(m.scrollOffset+pageSize, maxOffset)
	m.cursorLine = min(m.scrollOffset+m.viewportHeight()-1, max(len(m.lines)-1, 0))
	return nil
}

func actionPageUp(m *Model) tea.Cmd {
	pageSize := m.viewportHeight()
	m.scrollOffset = max(m.scrollOffset-pageSize, 0)
	m.cursorLine = m.scrollOffset
	return nil
}

func actionToggleLineNumbers(m *Model) tea.Cmd {
	m.showLineNumbers = !m.showLineNumbers
	return nil
}

func actionToggleWordWrap(m *Model) tea.Cmd {
	m.wordWrap = !m.wordWrap
	return nil
}

// ---------------------------------------------------------------------------
// Public scroll API (for mouse routing from app level)
// ---------------------------------------------------------------------------

// ScrollUp scrolls the viewport up by one line.
// Returns true if the scroll was applied, false if at the top boundary.
// Keeps cursorLine within the visible range to prevent jumps on keyboard resume.
func (m *Model) ScrollUp() bool {
	prev := m.scrollOffset
	m.scrollOffset = max(m.scrollOffset-1, 0)
	m.clampCursorToView()
	return m.scrollOffset != prev
}

// ScrollDown scrolls the viewport down by one line.
// Returns true if the scroll was applied, false if at the bottom boundary.
// Keeps cursorLine within the visible range to prevent jumps on keyboard resume.
func (m *Model) ScrollDown() bool {
	prev := m.scrollOffset
	maxOffset := max(len(m.lines)-m.viewportHeight(), 0)
	m.scrollOffset = min(m.scrollOffset+1, maxOffset)
	m.clampCursorToView()
	return m.scrollOffset != prev
}

// ScrollToLine scrolls the viewport so the given 0-based line is visible
// and positions the cursor there.
func (m *Model) ScrollToLine(line int) {
	line = min(line, max(len(m.lines)-1, 0))
	line = max(line, 0)
	m.cursorLine = line
	m.scrollIntoView()
}

// SetBounceOffset updates the visual bounce displacement for rendering.
func (m *Model) SetBounceOffset(offset int) {
	m.bounceOffset = offset
}

// ---------------------------------------------------------------------------
// Public selection API (for mouse routing from app level)
// ---------------------------------------------------------------------------

// StartDrag begins a text selection at the given document coordinates.
func (m *Model) StartDrag(line, col int) {
	m.selection = textSelection{
		startLine: line,
		startCol:  col,
		endLine:   line,
		endCol:    col,
		dragging:  true,
		set:       true,
	}
}

// ExtendDrag updates the selection endpoint during a drag.
func (m *Model) ExtendDrag(line, col int) {
	if !m.selection.dragging {
		return
	}
	m.selection.endLine = line
	m.selection.endCol = col
}

// EndDrag finalizes the selection. The highlight persists until cleared.
func (m *Model) EndDrag() {
	m.selection.dragging = false
}

// ClearSelection removes any active selection.
func (m *Model) ClearSelection() {
	m.selection = textSelection{}
}

// IsDragging reports whether a drag is currently in progress.
func (m *Model) IsDragging() bool {
	return m.selection.dragging
}

// HasSelection reports whether a non-empty text selection exists.
func (m *Model) HasSelection() bool {
	return m.selection.active()
}

// SelectedText returns the text within the selection range.
func (m *Model) SelectedText() string {
	if !m.selection.active() {
		return ""
	}
	sl, sc, el, ec := m.selection.normalize()
	sl = min(sl, max(len(m.lines)-1, 0))
	el = min(el, max(len(m.lines)-1, 0))
	if sl == el {
		return extractRange(m.lines[sl], sc, ec)
	}
	return m.extractMultiLine(sl, sc, el, ec)
}

// extractRange returns the byte slice [start, end) from a line, clamped.
func extractRange(line string, start, end int) string {
	start = min(start, len(line))
	end = min(end, len(line))
	if start >= end {
		return ""
	}
	return line[start:end]
}

// extractMultiLine builds the selected text across multiple lines.
func (m *Model) extractMultiLine(sl, sc, el, ec int) string {
	var b strings.Builder
	b.WriteString(extractRange(m.lines[sl], sc, len(m.lines[sl])))
	for i := sl + 1; i < el; i++ {
		b.WriteByte('\n')
		b.WriteString(m.lines[i])
	}
	b.WriteByte('\n')
	b.WriteString(extractRange(m.lines[el], 0, ec))
	return b.String()
}

// clampCursorToView ensures cursorLine stays within [scrollOffset, scrollOffset+viewportHeight).
func (m *Model) clampCursorToView() {
	vh := m.viewportHeight()
	m.cursorLine = max(m.cursorLine, m.scrollOffset)
	m.cursorLine = min(m.cursorLine, m.scrollOffset+vh-1)
	m.cursorLine = min(m.cursorLine, max(len(m.lines)-1, 0))
}

// ---------------------------------------------------------------------------
// Scroll management
// ---------------------------------------------------------------------------

// fileHeaderHeight returns 1 when a file is loaded (header shown), 0 otherwise.
func (m *Model) fileHeaderHeight() int {
	if m.filePath != "" {
		return 1
	}
	return 0
}

// viewportHeight returns the number of lines visible in the viewport,
// accounting for the file header when a file is loaded.
func (m *Model) viewportHeight() int {
	if m.height <= 0 {
		return 1
	}
	return max(m.height-m.fileHeaderHeight(), 1)
}

// scrollIntoView adjusts scrollOffset so the cursorLine is visible.
func (m *Model) scrollIntoView() {
	vh := m.viewportHeight()
	if m.cursorLine < m.scrollOffset {
		m.scrollOffset = m.cursorLine
	}
	if m.cursorLine >= m.scrollOffset+vh {
		m.scrollOffset = m.cursorLine - vh + 1
	}
	m.clampScroll()
}

// scrollToEnd positions the viewport at the bottom of the content.
func (m *Model) scrollToEnd() {
	vh := m.viewportHeight()
	m.scrollOffset = max(len(m.lines)-vh, 0)
}

// clampScroll ensures scrollOffset stays within valid bounds.
func (m *Model) clampScroll() {
	maxOffset := max(len(m.lines)-m.viewportHeight(), 0)
	m.scrollOffset = min(m.scrollOffset, maxOffset)
	m.scrollOffset = max(m.scrollOffset, 0)
}

// ---------------------------------------------------------------------------
// Bounce rendering
// ---------------------------------------------------------------------------

// applyCodeBounceShift applies the bounce visual offset to rendered code output,
// simulating iOS rubber-band overscroll.
// Positive offset (bottom bounce): content slides up, empty at bottom.
// Negative offset (top bounce): empty at top, content slides down.
func applyCodeBounceShift(rendered string, offset, viewHeight int) string {
	if offset == 0 || viewHeight <= 0 {
		return rendered
	}

	lines := strings.Split(rendered, "\n")

	absOffset := offset
	if absOffset < 0 {
		absOffset = -absOffset
	}
	absOffset = min(absOffset, viewHeight)

	if offset > 0 {
		shift := min(absOffset, len(lines))
		lines = lines[shift:]
	} else {
		pad := make([]string, absOffset, absOffset+len(lines))
		lines = append(pad, lines...)
	}

	// Truncate to viewHeight so shifted content never overflows the panel border.
	if len(lines) > viewHeight {
		lines = lines[:viewHeight]
	}

	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

// gutterWidth returns the column width needed for line numbers, derived from
// the total number of lines. Returns 0 when line numbers are hidden.
func (m *Model) gutterWidth() int {
	if !m.showLineNumbers {
		return 0
	}
	lineCount := len(m.lines)
	if lineCount == 0 {
		return 1
	}
	// Number of digits = floor(log10(lineCount)) + 1.
	return int(math.Log10(float64(lineCount))) + 1
}

// contentWidth returns the width available for code content after accounting
// for the gutter and gutter separator.
func (m *Model) contentWidth(gutterWidth int) int {
	separatorWidth := 0
	if gutterWidth > 0 {
		separatorWidth = 1 // single space between gutter and content
	}
	w := m.width - gutterWidth - separatorWidth
	if w < 1 {
		return 1
	}
	return w
}

// truncateStyledLine clips a styled string (with ANSI escape codes) to fit
// within the given visual width. ANSI CSI sequences are copied verbatim.
func truncateStyledLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var buf strings.Builder
	visWidth := 0
	i := 0
	for i < len(s) && visWidth < w {
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
			buf.WriteString(s[i:j])
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		buf.WriteString(s[i : i+size])
		visWidth++
		i += size
	}
	buf.WriteString("\x1b[0m")
	return buf.String()
}

// isCSIEnd reports whether b is a CSI sequence final byte.
func isCSIEnd(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}
