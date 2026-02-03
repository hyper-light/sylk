package code

import (
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
)

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

// SetContent sets the source content, file path, and language, then
// triggers a re-highlight of the content.
func (m *Model) SetContent(content, filePath, language string) {
	m.content = content
	m.filePath = filePath
	m.language = language
	m.lines = strings.Split(content, "\n")
	m.scrollOffset = 0
	m.cursorLine = 0
	m.reHighlight()
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
func (m *Model) View() string {
	lineCount := len(m.lines)
	if lineCount == 0 {
		return ""
	}

	viewHeight := m.viewportHeight()
	visibleEnd := min(m.scrollOffset+viewHeight, lineCount)
	visibleStart := m.scrollOffset

	gutterWidth := m.gutterWidth()
	contentWidth := m.contentWidth(gutterWidth)
	gutterStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	var b strings.Builder
	// Pre-allocate: each line needs gutter + separator + content + newline.
	b.Grow((gutterWidth + contentWidth + 2) * (visibleEnd - visibleStart))

	for i := visibleStart; i < visibleEnd; i++ {
		if i > visibleStart {
			b.WriteByte('\n')
		}
		m.renderViewLine(&b, i, gutterWidth, contentWidth, gutterStyle)
	}

	return b.String()
}

// renderViewLine writes a single viewport line (gutter + content) to the builder.
func (m *Model) renderViewLine(b *strings.Builder, lineIdx, gutterWidth, contentWidth int, gutterStyle lipgloss.Style) {
	if m.showLineNumbers {
		// Line numbers are 1-based, right-aligned within the gutter.
		numStr := fmt.Sprintf("%*d", gutterWidth, lineIdx+1)
		b.WriteString(gutterStyle.Render(numStr))
		b.WriteByte(' ')
	}

	styledLine := m.styledLine(lineIdx)
	if m.wordWrap && contentWidth > 0 {
		styledLine = truncateVisible(styledLine, contentWidth)
	}
	b.WriteString(styledLine)
}

// styledLine returns the cached highlighted line or falls back to default styling.
func (m *Model) styledLine(idx int) string {
	if idx < len(m.highlightedLines) {
		return m.highlightedLines[idx]
	}
	return m.lines[idx]
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

// ID returns the focus identifier for the code viewer.
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
	m.cursorLine = min(m.cursorLine+1, len(m.lines)-1)
	m.scrollIntoView()
	return nil
}

func actionScrollUp(m *Model) tea.Cmd {
	m.cursorLine = max(m.cursorLine-1, 0)
	m.scrollIntoView()
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
	m.cursorLine = min(m.cursorLine+pageSize, len(m.lines)-1)
	m.scrollIntoView()
	return nil
}

func actionPageUp(m *Model) tea.Cmd {
	pageSize := m.viewportHeight()
	m.cursorLine = max(m.cursorLine-pageSize, 0)
	m.scrollIntoView()
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
// Scroll management
// ---------------------------------------------------------------------------

// viewportHeight returns the number of lines visible in the viewport.
func (m *Model) viewportHeight() int {
	if m.height <= 0 {
		return 1
	}
	return m.height
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
