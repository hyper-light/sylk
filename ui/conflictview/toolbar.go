package conflictview

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Conflict toolbar button IDs.
const (
	ctbContinue = iota
	ctbBypass
	ctbAbort
	ctbCount
)

// ctbDef defines a toolbar button's display properties.
type ctbDef struct {
	icon   string
	label  string
	accent func(theme.Palette) lipgloss.Color
}

var ctbDefs = [ctbCount]ctbDef{
	ctbContinue: {icon: "▶", label: "Continue", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	ctbBypass:   {icon: "⏭", label: "Bypass", accent: func(p theme.Palette) lipgloss.Color { return p.Warning }},
	ctbAbort:    {icon: "✕", label: "Abort", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
}

// ctbCellWidth returns the visual width of a toolbar button cell.
func ctbCellWidth(id int) int {
	def := ctbDefs[id]
	return lipgloss.Width(" " + def.icon + " " + def.label + " ")
}

// renderNavBar renders the hunk navigation bar (1 line between content and toolbar)
// and records click zones in m.navZones.
func (m *Model) renderNavBar() string {
	m.navZones = navBarZones{} // reset
	p := m.theme.Palette
	keySt := lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
	mutedSt := lipgloss.NewStyle().Foreground(p.Muted)
	successSt := lipgloss.NewStyle().Foreground(p.Success).Bold(true)

	entry := m.selectedEntry()
	if entry == nil {
		return padLine("", m.width)
	}

	// Delete/modify or binary: no hunk navigation, only ours/theirs.
	if entry.Type == TypeDeleteModify || entry.Type == TypeBinary {
		label := "Delete/modify conflict"
		if entry.Type == TypeBinary {
			label = "Binary conflict"
		}
		var b strings.Builder
		b.WriteString("  ")
		col := 2
		desc := mutedSt.Render(label)
		b.WriteString(desc)
		col += lipgloss.Width(desc)
		b.WriteString("   ")
		col += 3
		m.navZones.ours, col = m.writeNavButton(&b, col, keySt, mutedSt, "[o]", " Ours")
		b.WriteString("  ")
		col += 2
		m.navZones.theirs, _ = m.writeNavButton(&b, col, keySt, mutedSt, "[t]", " Theirs")
		return padLine(b.String(), m.width)
	}

	// All hunks resolved.
	if len(m.hunks) == 0 {
		return padLine("  "+successSt.Render("All conflicts resolved"), m.width)
	}

	// Hunk navigation with click targets.
	var b strings.Builder
	b.WriteString("  ")
	col := 2

	// [n] prev button.
	m.navZones.prevHunk, col = m.writeNavButton(&b, col, keySt, mutedSt, "[n]", "")
	arrow := mutedSt.Render(" \u25C0 ")
	b.WriteString(arrow)
	col += lipgloss.Width(arrow)

	counter := mutedSt.Render(fmt.Sprintf("Conflict %d/%d", m.currentHunk+1, len(m.hunks)))
	b.WriteString(counter)
	col += lipgloss.Width(counter)

	arrow = mutedSt.Render(" \u25B6 ")
	b.WriteString(arrow)
	col += lipgloss.Width(arrow)

	// [N] next button.
	m.navZones.nextHunk, col = m.writeNavButton(&b, col, keySt, mutedSt, "[N]", "")
	b.WriteString("   ")
	col += 3

	// Resolution buttons.
	m.navZones.ours, col = m.writeNavButton(&b, col, keySt, mutedSt, "[o]", " Ours")
	b.WriteString("  ")
	col += 2
	m.navZones.theirs, col = m.writeNavButton(&b, col, keySt, mutedSt, "[t]", " Theirs")
	b.WriteString("  ")
	col += 2
	m.navZones.both, _ = m.writeNavButton(&b, col, keySt, mutedSt, "[b]", " Both")

	return padLine(b.String(), m.width)
}

// writeNavButton appends a key badge and optional label to the builder,
// returning the click zone [start, end) and the new column position.
func (m *Model) writeNavButton(b *strings.Builder, col int, keySt, labelSt lipgloss.Style, key, label string) ([2]int, int) {
	start := col
	badge := keySt.Render(key)
	b.WriteString(badge)
	col += lipgloss.Width(badge)
	if label != "" {
		lbl := labelSt.Render(label)
		b.WriteString(lbl)
		col += lipgloss.Width(lbl)
	}
	return [2]int{start, col}, col
}

// selectedEntry returns the currently selected entry, or nil.
func (m *Model) selectedEntry() *ConflictFileEntry {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return nil
	}
	return &m.data.Entries[m.selectedFile]
}

// renderToolbar renders the conflict toolbar.
func (m *Model) renderToolbar() string {
	p := m.theme.Palette
	bSt := lipgloss.NewStyle().Foreground(p.Border)

	type btnCell struct {
		inner string
		width int
	}

	cells := make([]btnCell, ctbCount)
	for i := range ctbCount {
		def := ctbDefs[i]
		enabled := m.isToolbarButtonEnabled(i)
		selected := m.toolbarFocused && i == m.toolbarAction
		hovered := i == m.hoverBtnIdx

		fg := p.Muted
		bold := false
		if enabled {
			fg = p.Foreground
			if hovered {
				fg = def.accent(p)
			}
			if selected {
				fg = def.accent(p)
				bold = true
			}
		}

		text := lipgloss.NewStyle().Foreground(fg).Bold(bold).Render(def.icon + " " + def.label)
		w := ctbCellWidth(i)
		cells[i] = btnCell{inner: " " + text + " ", width: w}
	}

	// Row 1: top borders.
	var row1 strings.Builder
	for i, c := range cells {
		if i > 0 {
			row1.WriteString(bSt.Render("┬"))
		}
		row1.WriteString(bSt.Render(strings.Repeat("─", c.width)))
	}
	row1.WriteString(bSt.Render("╮"))
	row1Str := row1.String()
	if vis := lipgloss.Width(row1Str); vis < m.width {
		row1Str += strings.Repeat(" ", m.width-vis)
	}

	// Row 2: button content.
	var row2 strings.Builder
	for i, c := range cells {
		if i > 0 {
			row2.WriteString(bSt.Render("│"))
		}
		row2.WriteString(c.inner)
	}
	row2.WriteString(bSt.Render("│"))
	row2Str := row2.String()
	if vis := lipgloss.Width(row2Str); vis < m.width {
		row2Str += strings.Repeat(" ", m.width-vis)
	}

	return row1Str + "\n" + row2Str
}

// isToolbarButtonEnabled returns whether a toolbar button is enabled.
func (m *Model) isToolbarButtonEnabled(id int) bool {
	if id == ctbContinue {
		return m.AllResolved()
	}
	return true
}

// handleToolbarKey handles keys when toolbar is focused.
func (m *Model) handleToolbarKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "l", "right":
		m.toolbarAction = (m.toolbarAction + 1) % ctbCount
		m.viewDirty = true
	case "h", "left":
		m.toolbarAction = (m.toolbarAction - 1 + ctbCount) % ctbCount
		m.viewDirty = true
	case "enter", " ":
		return m.activateToolbarButton(m.toolbarAction)
	case "esc":
		m.toolbarFocused = false
		m.viewDirty = true
	case "tab":
		if m.toolbarAction+1 < ctbCount {
			m.toolbarAction++
		} else {
			m.toolbarFocused = false
		}
		m.viewDirty = true
	case "shift+tab":
		if m.toolbarAction > 0 {
			m.toolbarAction--
		} else {
			m.toolbarFocused = false
		}
		m.viewDirty = true
	case "j", "down":
		m.scrollOffset = min(m.scrollOffset+1, m.maxScroll())
		m.viewDirty = true
	case "k", "up":
		m.scrollOffset = max(m.scrollOffset-1, 0)
		m.viewDirty = true
	}
	return nil
}

// activateToolbarButton triggers a toolbar action.
func (m *Model) activateToolbarButton(idx int) tea.Cmd {
	if !m.isToolbarButtonEnabled(idx) {
		return nil
	}
	switch idx {
	case ctbContinue:
		return func() tea.Msg { return SequencerContinueMsg{} }
	case ctbBypass:
		return func() tea.Msg { return SequencerBypassMsg{} }
	case ctbAbort:
		return func() tea.Msg { return SequencerAbortMsg{} }
	}
	return nil
}

// conflictToolbarHitTest returns the button index for a click at column x,
// or -1 if outside all buttons.
func conflictToolbarHitTest(width, x int) int {
	col := 0
	for i := range ctbCount {
		w := ctbCellWidth(i)
		if i > 0 {
			col++ // separator
		}
		if x >= col && x < col+w {
			return i
		}
		col += w
	}
	return -1
}