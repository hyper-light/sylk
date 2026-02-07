package fieldmanual

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Layout constants (derived, not magic)
// ---------------------------------------------------------------------------

// overlayBorderSize is the horizontal/vertical space consumed by the border:
// 1 char per side = 2.
const overlayBorderSize = 2

// overlayPaddingSize is the horizontal/vertical space consumed by inner
// padding: Padding(1) = 1 char per side = 2.
const overlayPaddingSize = 2

// overlayFrameSize is the total non-content space per axis:
// border + padding = 4.
const overlayFrameSize = overlayBorderSize + overlayPaddingSize

// marginLines is the minimum vertical margin between the overlay edge and
// the terminal boundary. Derived from: requirement specifies 1-space margin
// top and bottom.
const marginLines = 1

// sectionGap is the number of blank lines between sections.
// Derived from: visual separation between logical groups.
const sectionGap = 1

// headerLines is the vertical space consumed by the fixed title line.
// Derived from: title(1).
const headerLines = 1

// keyColumnWidth is derived from the maximum key string length across all
// bindings, ensuring consistent column alignment.
var keyColumnWidth = deriveKeyColumnWidth()

func deriveKeyColumnWidth() int {
	maxLen := 0
	for _, sec := range allSections() {
		for _, b := range sec.Bindings {
			if n := len(b.Key); n > maxLen {
				maxLen = n
			}
		}
	}
	return maxLen + 1
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// Model is the Field Manual help overlay. It implements component.Component,
// component.Focusable, and component.Resizable.
type Model struct {
	scrollOffset int
	totalLines   int
	lines        []string

	width   int
	height  int
	focused bool
	visible bool
	theme   *theme.Theme
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a Field Manual overlay Model with the given theme.
func New(th *theme.Theme) *Model {
	m := &Model{theme: th}
	return m
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Show opens the Field Manual overlay and resets scroll to the top.
func (m *Model) Show() {
	m.visible = true
	m.scrollOffset = 0
	m.rebuildContent()
}

// Hide closes the Field Manual overlay.
func (m *Model) Hide() {
	m.visible = false
}

// Visible reports whether the overlay is currently shown.
func (m *Model) Visible() bool {
	return m.visible
}

// ScrollUp moves the viewport up by one line (for mouse wheel).
func (m *Model) ScrollUp() {
	if m.scrollOffset > 0 {
		m.scrollOffset--
	}
}

// ScrollDown moves the viewport down by one line (for mouse wheel).
func (m *Model) ScrollDown() {
	if m.scrollOffset < m.maxScroll() {
		m.scrollOffset++
	}
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns no initial command.
func (m *Model) Init() tea.Cmd { return nil }

// Update processes incoming messages. When visible, the overlay captures all
// key input.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	if !m.visible {
		return m, nil
	}
	key, ok := incoming.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	return m.handleKey(key)
}

// View renders the Field Manual as a full-screen overlay with margin.
func (m *Model) View() string {
	if !m.visible {
		return ""
	}
	return m.renderOverlay()
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (m *Model) ID() component.FocusID  { return component.FocusFieldManual }
func (m *Model) Focused() bool           { return m.focused }
func (m *Model) SetFocused(focused bool) { m.focused = focused }

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates the available dimensions and rebuilds pre-rendered content.
func (m *Model) SetSize(width, height int) {
	m.width = max(width, 0)
	m.height = max(height, 0)
	if m.visible {
		m.rebuildContent()
	}
}

// ---------------------------------------------------------------------------
// Key dispatch (table-driven)
// ---------------------------------------------------------------------------

type keyAction func(m *Model) tea.Cmd

type keyEntry struct {
	match  func(k tea.KeyMsg) bool
	action keyAction
}

var manualKeys = []keyEntry{
	{matchKey("esc"), actionClose},
	{matchKey("q"), actionClose},
	{matchKey("alt+h"), actionClose},
	{matchKey("down"), actionScrollDown},
	{matchKey("j"), actionScrollDown},
	{matchKey("up"), actionScrollUp},
	{matchKey("k"), actionScrollUp},
	{matchKey("pgdown"), actionPageDown},
	{matchKey("pgup"), actionPageUp},
	{matchKey("home"), actionScrollTop},
	{matchKey("g"), actionScrollTop},
	{matchKey("end"), actionScrollBottom},
	{matchKey("G"), actionScrollBottom},
}

func (m *Model) handleKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	for _, e := range manualKeys {
		if e.match(k) {
			return m, e.action(m)
		}
	}
	return m, nil
}

func matchKey(desc string) func(tea.KeyMsg) bool {
	return func(k tea.KeyMsg) bool { return k.String() == desc }
}

// ---------------------------------------------------------------------------
// Key actions
// ---------------------------------------------------------------------------

func actionClose(m *Model) tea.Cmd {
	m.Hide()
	return nil
}

func actionScrollDown(m *Model) tea.Cmd {
	if m.scrollOffset < m.maxScroll() {
		m.scrollOffset++
	}
	return nil
}

func actionScrollUp(m *Model) tea.Cmd {
	if m.scrollOffset > 0 {
		m.scrollOffset--
	}
	return nil
}

func actionPageDown(m *Model) tea.Cmd {
	m.scrollOffset = min(m.scrollOffset+m.viewportHeight(), m.maxScroll())
	return nil
}

func actionPageUp(m *Model) tea.Cmd {
	m.scrollOffset = max(m.scrollOffset-m.viewportHeight(), 0)
	return nil
}

func actionScrollTop(m *Model) tea.Cmd {
	m.scrollOffset = 0
	return nil
}

func actionScrollBottom(m *Model) tea.Cmd {
	m.scrollOffset = m.maxScroll()
	return nil
}

// ---------------------------------------------------------------------------
// Scroll helpers
// ---------------------------------------------------------------------------

func (m *Model) viewportHeight() int {
	return max(m.height-marginLines*2-overlayFrameSize-headerLines, 1)
}

func (m *Model) maxScroll() int {
	return max(m.totalLines-m.viewportHeight(), 0)
}

func (m *Model) canScrollUp() bool {
	return m.scrollOffset > 0
}

func (m *Model) canScrollDown() bool {
	return m.scrollOffset < m.maxScroll()
}

// ---------------------------------------------------------------------------
// Content building
// ---------------------------------------------------------------------------

func (m *Model) rebuildContent() {
	contentW := m.contentWidth()
	if contentW <= 0 {
		m.lines = nil
		m.totalLines = 0
		return
	}
	sections := allSections()

	// Estimate capacity: 2 lines per section header + 1-3 lines per binding.
	est := 0
	for _, sec := range sections {
		est += 2 + len(sec.Bindings)*3
	}

	lines := make([]string, 0, est)
	for i, sec := range sections {
		if i > 0 {
			for range sectionGap {
				lines = append(lines, "")
			}
		}
		lines = append(lines, m.renderSectionHeader(sec.Title))
		lines = append(lines, m.renderSeparator(contentW))
		for _, b := range sec.Bindings {
			lines = append(lines, m.renderBinding(b, contentW)...)
		}
	}
	m.lines = lines
	m.totalLines = len(lines)
}

func (m *Model) contentWidth() int {
	return max(m.width-overlayFrameSize-marginLines*2, 1)
}

// ---------------------------------------------------------------------------
// Line renderers
// ---------------------------------------------------------------------------

func (m *Model) renderSectionHeader(title string) string {
	style := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Primary).
		Bold(true)
	return "  " + style.Render(title)
}

func (m *Model) renderSeparator(width int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Subtle)
	sepW := max(width-4, 1)
	return "  " + style.Render(strings.Repeat("─", sepW))
}

// bindingIndent is the left margin before each key column.
// Derived from: 2 spaces visual indent.
const bindingIndent = 2

// bindingGap is the space between the key column and the description.
// Derived from: 2 spaces visual separation.
const bindingGap = 2

// descIndent is the left margin for a wrapped description line.
// Derived from: bindingIndent + keyColumnWidth + bindingGap to align with
// where the description starts in a single-line layout.
var descIndent = bindingIndent + keyColumnWidth + bindingGap

func (m *Model) renderBinding(b Binding, contentW int) []string {
	keyStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Warning).
		Bold(true).
		Width(keyColumnWidth)
	descStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Foreground)
	prereqStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Muted).
		Italic(true)

	keyPart := strings.Repeat(" ", bindingIndent) + keyStyle.Render(b.Key)
	descPart := descStyle.Render(b.Description)
	if b.Prerequisites != "" {
		descPart += prereqStyle.Render("  [" + b.Prerequisites + "]")
	}

	singleLine := keyPart + strings.Repeat(" ", bindingGap) + descPart
	if lipgloss.Width(singleLine) <= contentW {
		return []string{singleLine}
	}

	// Wrap: key on first line, description indented on second, blank line after.
	indent := strings.Repeat(" ", descIndent)
	return []string{keyPart, indent + descPart, ""}
}

// ---------------------------------------------------------------------------
// Overlay rendering
// ---------------------------------------------------------------------------

func (m *Model) renderOverlay() string {
	contentW := m.contentWidth()
	vpH := m.viewportHeight()

	// Fixed title line with scroll indicators.
	header := m.renderTitleLine(contentW)

	// Slice visible lines from pre-rendered content.
	end := min(m.scrollOffset+vpH, m.totalLines)
	start := min(m.scrollOffset, end)
	visible := m.lines[start:end]
	body := strings.Join(visible, "\n")

	content := header + "\n" + body

	// Render the overlay box. lipgloss Width includes padding but not border,
	// so Width = contentW + overlayPaddingSize.
	outerH := m.height - marginLines*2
	widthParam := max(contentW+overlayPaddingSize, 1)
	heightParam := max(outerH-overlayBorderSize-overlayPaddingSize, 1)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Palette.BorderActive).
		Padding(1).
		Width(widthParam).
		Height(heightParam)

	box := boxStyle.Render(content)

	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	hPad := max((m.width-boxWidth)/2, 0)
	vPad := max((m.height-boxHeight)/2, 0)

	padded := indentLines(box, hPad)
	return strings.Repeat("\n", vPad) + padded
}

func (m *Model) renderTitleLine(contentW int) string {
	titleStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Secondary).
		Bold(true)
	title := "  " + titleStyle.Render("Field Manual")

	indicator := m.renderScrollIndicator()
	if indicator == "" {
		return title
	}

	titleW := lipgloss.Width(title)
	indicatorW := lipgloss.Width(indicator)
	gap := max(contentW-titleW-indicatorW, 1)
	return title + strings.Repeat(" ", gap) + indicator
}

func (m *Model) renderScrollIndicator() string {
	ms := m.maxScroll()
	if ms <= 0 {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	arrows := ""
	if m.canScrollUp() {
		arrows += theme.IconArrowUp
	}
	if m.canScrollDown() {
		arrows += theme.IconArrowDown
	}

	pct := (m.scrollOffset * 100) / ms
	return style.Render(fmt.Sprintf("%s [%d%%]", arrows, pct))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// indentLines prepends each line with n spaces.
func indentLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	prefix := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	var b strings.Builder
	b.Grow(len(s) + n*len(lines))
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(prefix)
		b.WriteString(line)
	}
	return b.String()
}
