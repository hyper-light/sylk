package conflictview

import (
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
	ctbPrev
	ctbNext
	ctbOurs
	ctbTheirs
	ctbBoth
	ctbUndo
	ctbCount
)

// ctbDef defines a toolbar button's display properties.
type ctbDef struct {
	icon   string
	label  string
	accent func(theme.Palette) lipgloss.Color
}

// ctbStaticDefs holds the definitions for buttons with fixed labels.
var ctbStaticDefs = [ctbCount]ctbDef{
	ctbContinue: {icon: "▶", label: "Continue", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	ctbBypass:   {icon: "⏭", label: "Bypass", accent: func(p theme.Palette) lipgloss.Color { return p.Warning }},
	ctbAbort:    {icon: "✕", label: "Abort", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
	ctbPrev:     {icon: "◀", label: "Prev", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	ctbNext:     {icon: "▶", label: "Next", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	ctbOurs:     {icon: "↑", label: "Dest", accent: func(p theme.Palette) lipgloss.Color { return p.Secondary }},
	ctbTheirs:   {icon: "↓", label: "Source", accent: func(p theme.Palette) lipgloss.Color { return p.Teal }},
	ctbBoth:     {icon: "↕", label: "Both", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	ctbUndo:     {icon: "↩", label: "Undo", accent: func(p theme.Palette) lipgloss.Color { return p.Warning }},
}

// ctbDefFor returns the button definition for a given ID, substituting
// dynamic labels for the ours/theirs buttons.
func (m *Model) ctbDefFor(id int) ctbDef {
	def := ctbStaticDefs[id]
	switch id {
	case ctbOurs:
		def.label = m.oursLabel
	case ctbTheirs:
		def.label = m.theirsLabel
	}
	return def
}

// ctbCellWidth returns the visual width of a toolbar button cell.
func (m *Model) ctbCellWidth(id int) int {
	def := m.ctbDefFor(id)
	return lipgloss.Width(" " + def.icon + " " + def.label + " ")
}

// ctbPrimaryCount is the number of always-visible primary buttons.
const ctbPrimaryCount = 3

// ctbFittingCount returns how many buttons fit in the current width.
// The first 3 (Continue/Bypass/Abort) are always shown; the rest are
// added as width permits.
func (m *Model) ctbFittingCount() int {
	used := 0
	for i := range ctbPrimaryCount {
		if i > 0 {
			used++ // separator
		}
		used += m.ctbCellWidth(i)
	}
	used++ // trailing border

	count := ctbPrimaryCount
	for i := ctbPrimaryCount; i < ctbCount; i++ {
		need := 1 + m.ctbCellWidth(i) // separator + cell
		if used+need > m.width {
			break
		}
		used += need
		count++
	}
	return count
}

// selectedEntry returns the currently selected entry, or nil.
func (m *Model) selectedEntry() *ConflictFileEntry {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return nil
	}
	return &m.data.Entries[m.selectedFile]
}

// renderToolbar renders the conflict toolbar with dynamic button count.
func (m *Model) renderToolbar() string {
	p := m.theme.Palette
	bSt := lipgloss.NewStyle().Foreground(p.Border)
	fitting := m.ctbFittingCount()

	type btnCell struct {
		inner string
		width int
	}

	cells := make([]btnCell, fitting)
	for i := range fitting {
		def := m.ctbDefFor(i)
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
		w := m.ctbCellWidth(i)
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
	switch id {
	case ctbContinue:
		return m.AllResolved()
	case ctbPrev, ctbNext:
		return m.hasHunks()
	case ctbOurs, ctbTheirs:
		e := m.selectedEntry()
		return e != nil && e.Resolution == ResUnresolved
	case ctbBoth:
		e := m.selectedEntry()
		return e != nil && e.Resolution == ResUnresolved && supportsResolution(e, ResBoth)
	case ctbUndo:
		return m.canUndo()
	default:
		return true
	}
}

// handleToolbarKey handles keys when toolbar is focused.
func (m *Model) handleToolbarKey(key tea.KeyMsg) tea.Cmd {
	fitting := m.ctbFittingCount()
	switch key.String() {
	case "l", "right":
		m.toolbarAction = (m.toolbarAction + 1) % fitting
		m.viewDirty = true
	case "h", "left":
		m.toolbarAction = (m.toolbarAction - 1 + fitting) % fitting
		m.viewDirty = true
	case "enter", " ":
		return m.activateToolbarButton(m.toolbarAction)
	case "esc":
		m.toolbarFocused = false
		m.viewDirty = true
	case "tab":
		if m.toolbarAction+1 < fitting {
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
	case ctbPrev:
		m.prevHunk()
		return nil
	case ctbNext:
		m.nextHunk()
		return nil
	case ctbOurs:
		cmd := m.resolveKey(ResOurs)
		m.preserveToolbarFocus(idx)
		return cmd
	case ctbTheirs:
		cmd := m.resolveKey(ResTheirs)
		m.preserveToolbarFocus(idx)
		return cmd
	case ctbBoth:
		cmd := m.resolveKey(ResBoth)
		m.preserveToolbarFocus(idx)
		return cmd
	case ctbUndo:
		cmd := m.undoResolution()
		m.preserveToolbarFocus(idx)
		return cmd
	}
	return nil
}

// preserveToolbarFocus keeps the toolbar focused after a resolve action.
// If all conflicts just became resolved, advanceToNextUnresolved already
// moved focus to Continue — don't overwrite that.
func (m *Model) preserveToolbarFocus(fallback int) {
	if m.toolbarAction == ctbContinue && m.AllResolved() {
		return
	}
	m.toolbarFocused = true
	m.toolbarAction = fallback
}

// conflictToolbarHitTest returns the button index for a click at column x,
// or -1 if outside all buttons.
func (m *Model) conflictToolbarHitTest(x int) int {
	fitting := m.ctbFittingCount()
	col := 0
	for i := range fitting {
		w := m.ctbCellWidth(i)
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
