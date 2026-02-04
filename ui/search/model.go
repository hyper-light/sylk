package search

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Layout constants (derived, not magic)
// ---------------------------------------------------------------------------

// overlayPadding is the total horizontal and vertical space consumed by the
// overlay border and padding: border(1) + padding(1) per side = 4.
const overlayPadding = 4

// inputLineHeight is the vertical space consumed by the query input line
// and its separator. Derived from: input(1) + separator(1) = 2.
const inputLineHeight = 2

// resultLineHeight is the vertical space consumed by a single result entry.
// Derived from: title line(1).
const resultLineHeight = 1

// minOverlayWidth prevents the overlay from collapsing below usable size.
// Derived from: a reasonable minimum for displaying a file path.
const minOverlayWidth = 20

// overlayWidthFraction is the proportion of terminal width the overlay occupies.
// Derived from: typical command palette covers ~60% of the viewport.
const overlayWidthFraction = 0.6

// overlayHeightFraction is the proportion of terminal height the overlay occupies.
const overlayHeightFraction = 0.6

// defaultSearchLimit caps the number of results requested from providers.
// Derived from: enough to fill a typical viewport while remaining bounded.
const defaultSearchLimit = 50

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// Model is the command palette / fuzzy finder overlay. It implements
// component.Focusable and component.Resizable.
type Model struct {
	// Query state.
	query   []rune
	cursor  int
	results []Result

	// Navigation.
	selected     int
	scrollOffset int

	// Providers.
	registry *ProviderRegistry

	// Dimensions.
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

// New creates a command palette Model with the given theme and provider
// registry.
func New(th *theme.Theme, registry *ProviderRegistry) *Model {
	return &Model{
		query:    nil,
		registry: registry,
		theme:    th,
	}
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Show opens the command palette overlay.
func (m *Model) Show() {
	m.visible = true
	m.query = nil
	m.cursor = 0
	m.selected = 0
	m.scrollOffset = 0
	m.results = nil
}

// Hide closes the command palette overlay.
func (m *Model) Hide() {
	m.visible = false
	m.query = nil
	m.results = nil
}

// Visible reports whether the palette is currently shown.
func (m *Model) Visible() bool {
	return m.visible
}

// ScrollUp moves the selected result up by one, scrolling the result list
// within the fixed overlay box. The overlay position itself does not move.
func (m *Model) ScrollUp() {
	if m.selected > 0 {
		m.selected--
		m.scrollSelectedIntoView()
	}
}

// ScrollDown moves the selected result down by one, scrolling the result list
// within the fixed overlay box. The overlay position itself does not move.
func (m *Model) ScrollDown() {
	if m.selected < len(m.results)-1 {
		m.selected++
		m.scrollSelectedIntoView()
	}
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns no initial command.
func (m *Model) Init() tea.Cmd { return nil }

// Update processes incoming messages.
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

// View renders the command palette as a centered overlay. The search bar
// (query + separator) and the results list are structurally independent
// blocks so that scrolling only affects the results content.
func (m *Model) View() string {
	if !m.visible {
		return ""
	}
	overlayW, overlayH := m.overlayDimensions()
	contentW := max(overlayW-overlayPadding, 1)
	contentH := max(overlayH-overlayPadding, 1)

	// Fixed header: query input + separator. Never moves.
	sepStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)
	header := m.renderQueryLine(contentW) + "\n" +
		sepStyle.Render(strings.Repeat("─", contentW))

	// Scrollable results in a fixed-height block.
	resultsH := max(contentH-inputLineHeight, 1)
	maxVisible := resultsH / resultLineHeight
	resultsBlock := lipgloss.NewStyle().
		Width(contentW).
		Height(resultsH).
		MaxHeight(resultsH).
		Render(m.renderResults(contentW, maxVisible))

	content := lipgloss.JoinVertical(lipgloss.Left, header, resultsBlock)
	return m.renderOverlay(content, overlayW, overlayH)
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (m *Model) ID() component.FocusID  { return component.FocusSearch }
func (m *Model) Focused() bool           { return m.focused }
func (m *Model) SetFocused(focused bool) { m.focused = focused }

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

func (m *Model) SetSize(width, height int) {
	m.width = max(width, 0)
	m.height = max(height, 0)
}

// ---------------------------------------------------------------------------
// Key dispatch (table-driven)
// ---------------------------------------------------------------------------

type keyAction func(m *Model) tea.Cmd

type keyEntry struct {
	match  func(k tea.KeyMsg) bool
	action keyAction
}

var paletteKeys = []keyEntry{
	{matchKey("esc"), actionClose},
	{matchKey("enter"), actionSelect},
	{matchKey("up"), actionMoveUp},
	{matchKey("k"), nil}, // handled specially: only when ctrl
	{matchKey("down"), actionMoveDown},
	{matchKey("j"), nil}, // handled specially: only when ctrl
	{matchKey("ctrl+k"), actionMoveUp},
	{matchKey("ctrl+j"), actionMoveDown},
	{matchKey("ctrl+n"), actionMoveDown},
	{matchKey("ctrl+p"), actionMoveUp},
	{matchKey("backspace"), actionBackspace},
	{matchKey("ctrl+u"), actionClearQuery},
}

func (m *Model) handleKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	// Try table-driven dispatch first.
	for _, e := range paletteKeys {
		if e.match(k) && e.action != nil {
			return m, e.action(m)
		}
	}
	// Fall through: insert rune.
	if k.Type == tea.KeyRunes {
		m.insertRune([]rune(k.String()))
		m.refilter()
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

func actionSelect(m *Model) tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.results) {
		return nil
	}
	r := m.results[m.selected]
	m.Hide()
	if r.Action != nil {
		r.Action()
	}
	return nil
}

func actionMoveUp(m *Model) tea.Cmd {
	if m.selected > 0 {
		m.selected--
		m.scrollSelectedIntoView()
	}
	return nil
}

func actionMoveDown(m *Model) tea.Cmd {
	if m.selected < len(m.results)-1 {
		m.selected++
		m.scrollSelectedIntoView()
	}
	return nil
}

func actionBackspace(m *Model) tea.Cmd {
	if len(m.query) == 0 {
		return nil
	}
	m.query = m.query[:len(m.query)-1]
	m.cursor = len(m.query)
	m.refilter()
	return nil
}

func actionClearQuery(m *Model) tea.Cmd {
	m.query = nil
	m.cursor = 0
	m.refilter()
	return nil
}

// ---------------------------------------------------------------------------
// Query manipulation
// ---------------------------------------------------------------------------

func (m *Model) insertRune(rs []rune) {
	m.query = append(m.query, rs...)
	m.cursor = len(m.query)
}

func (m *Model) refilter() {
	queryStr := string(m.query)
	ctx := context.Background()
	m.results = m.registry.Search(ctx, queryStr, defaultSearchLimit)
	m.selected = 0
	m.scrollOffset = 0
}

// ---------------------------------------------------------------------------
// Scroll management
// ---------------------------------------------------------------------------

func (m *Model) scrollSelectedIntoView() {
	_, overlayH := m.overlayDimensions()
	contentH := max(overlayH-overlayPadding, 1)
	maxVis := m.maxVisibleResults(contentH)

	if m.selected < m.scrollOffset {
		m.scrollOffset = m.selected
	}
	if m.selected >= m.scrollOffset+maxVis {
		m.scrollOffset = m.selected - maxVis + 1
	}
}

func (m *Model) maxVisibleResults(contentHeight int) int {
	available := contentHeight - inputLineHeight
	if available <= 0 {
		return 1
	}
	return available / resultLineHeight
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (m *Model) renderQueryLine(contentWidth int) string {
	promptStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary).Bold(true)
	queryStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	prompt := promptStyle.Render(theme.IconSearch + " ")
	queryText := string(m.query)
	cursor := cursorStyle.Render(" ")

	text := queryStyle.Render(queryText) + cursor
	_ = contentWidth
	return prompt + text
}

func (m *Model) renderResults(contentWidth, maxVisible int) string {
	if len(m.results) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted).Italic(true)
		return "\n" + emptyStyle.Render("  No results")
	}

	end := min(m.scrollOffset+maxVisible, len(m.results))
	visible := m.results[m.scrollOffset:end]

	var b strings.Builder
	b.Grow(contentWidth * len(visible))

	for i, r := range visible {
		b.WriteByte('\n')
		absIdx := m.scrollOffset + i
		b.WriteString(m.renderResultEntry(r, absIdx, contentWidth))
	}
	return b.String()
}

func (m *Model) renderResultEntry(r Result, absIdx, contentWidth int) string {
	isSelected := absIdx == m.selected

	catStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	titleStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	selectedStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Primary).
		Bold(true)
	pointerStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary)

	var b strings.Builder

	if isSelected {
		b.WriteString(pointerStyle.Render(theme.IconArrowRight + " "))
		b.WriteString(selectedStyle.Render(r.Title))
	} else {
		b.WriteString("  ")
		b.WriteString(titleStyle.Render(r.Title))
	}

	if r.Category != "" {
		badge := catStyle.Render(" [" + r.Category + "]")
		b.WriteString(badge)
	}

	rendered := b.String()
	if lipgloss.Width(rendered) > contentWidth {
		rendered = truncateToWidth(rendered, contentWidth)
	}
	return rendered
}

// truncateToWidth truncates a string to fit within maxWidth visible columns.
func truncateToWidth(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	// Reserve space for ellipsis (3 runes).
	ellipsisLen := 3
	cutoff := max(maxWidth-ellipsisLen, 0)
	return string(runes[:cutoff]) + "..."
}

func (m *Model) overlayDimensions() (int, int) {
	w := int(float64(m.width) * overlayWidthFraction)
	h := int(float64(m.height) * overlayHeightFraction)
	w = max(w, minOverlayWidth)
	h = max(h, inputLineHeight+overlayPadding+1)
	return w, h
}

func (m *Model) renderOverlay(content string, overlayW, overlayH int) string {
	innerH := max(overlayH-overlayPadding, 1)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Palette.BorderActive).
		Padding(1).
		Width(max(overlayW-overlayPadding, 1)).
		Height(innerH)

	box := boxStyle.Render(content)

	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	hPad := max((m.width-boxWidth)/2, 0)
	vPad := max((m.height-boxHeight)/2, 0)

	padded := indentLines(box, hPad)
	topPadding := strings.Repeat("\n", vPad)
	return topPadding + padded
}

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
