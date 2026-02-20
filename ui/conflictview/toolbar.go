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
	key    string // keyboard shortcut hint, e.g. "o"
	accent func(theme.Palette) lipgloss.Color
}

// ctbStaticDefs holds the definitions for buttons with fixed labels.
var ctbStaticDefs = [ctbCount]ctbDef{
	ctbContinue: {icon: "▶", label: "Continue", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	ctbBypass:   {icon: "⏭", label: "Bypass", accent: func(p theme.Palette) lipgloss.Color { return p.Warning }},
	ctbAbort:    {icon: "✕", label: "Abort", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
	ctbPrev:     {icon: "◀", label: "Prev", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	ctbNext:     {icon: "▶", label: "Next", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	ctbOurs:     {icon: "↑", label: "Dest", key: "o", accent: func(p theme.Palette) lipgloss.Color { return p.Secondary }},
	ctbTheirs:   {icon: "↓", label: "Source", key: "t", accent: func(p theme.Palette) lipgloss.Color { return p.Teal }},
	ctbBoth:     {icon: "↕", label: "Both", key: "b", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	ctbUndo:     {icon: "↩", label: "Undo", key: "u", accent: func(p theme.Palette) lipgloss.Color { return p.Warning }},
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
	inner := def.icon + " " + def.label
	if def.key != "" {
		inner += " (" + def.key + ")"
	}
	return lipgloss.Width(" " + inner + " ")
}

// ctbLeftCount is the number of buttons in the left toolbar group
// (Continue, Bypass, Abort, Prev, Next).
const ctbLeftCount = 5

// ctbGroupGap is the minimum gap between left and right toolbar groups.
const ctbGroupGap = 2

// ctbFittingCount returns how many total buttons fit across both groups.
// The left group (Continue..Next) is always shown; right-group buttons
// (Ours..Undo) are added as width permits.
func (m *Model) ctbFittingCount() int {
	leftW := m.toolbarGroupWidth(0, ctbLeftCount)

	available := m.width - leftW - ctbGroupGap
	rightUsed := 1 // leading ╭/│
	count := ctbLeftCount
	for i := ctbLeftCount; i < ctbCount; i++ {
		need := m.ctbCellWidth(i)
		if i > ctbLeftCount {
			need++ // separator
		}
		if rightUsed+need > available {
			break
		}
		rightUsed += need
		count++
	}
	return count
}

// toolbarGroupWidth returns the total rendered width of buttons [start, end),
// including inter-button separators and one trailing border character.
func (m *Model) toolbarGroupWidth(start, end int) int {
	w := 0
	for i := start; i < end; i++ {
		if i > start {
			w++ // separator
		}
		w += m.ctbCellWidth(i)
	}
	w++ // trailing border
	return w
}

// selectedEntry returns the currently selected entry, or nil.
func (m *Model) selectedEntry() *ConflictFileEntry {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return nil
	}
	return &m.data.Entries[m.selectedFile]
}

// btnCell holds the pre-rendered content and visual width of a toolbar button.
type btnCell struct {
	inner string
	width int
}

// buildBtnCell builds a single toolbar button cell.
func (m *Model) buildBtnCell(id int, p theme.Palette) btnCell {
	def := m.ctbDefFor(id)
	enabled := m.isToolbarButtonEnabled(id)
	selected := m.toolbarFocused && id == m.toolbarAction
	hovered := id == m.hoverBtnIdx

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

	mainText := lipgloss.NewStyle().Foreground(fg).Bold(bold).Render(def.icon + " " + def.label)
	text := mainText
	if def.key != "" {
		text += lipgloss.NewStyle().Foreground(p.Muted).Render(" (" + def.key + ")")
	}
	return btnCell{inner: " " + text + " ", width: m.ctbCellWidth(id)}
}

// renderToolbar renders the two-group toolbar: navigation on the left,
// resolution actions right-aligned.
func (m *Model) renderToolbar() string {
	p := m.theme.Palette
	bSt := lipgloss.NewStyle().Foreground(p.Border)
	fitting := m.ctbFittingCount()
	leftEnd := min(ctbLeftCount, fitting)
	rightCount := max(fitting-ctbLeftCount, 0)

	// Build all button cells.
	cells := make([]btnCell, fitting)
	for i := range fitting {
		cells[i] = m.buildBtnCell(i, p)
	}

	// --- Left group ---
	var lTop, lBot strings.Builder
	for i := 0; i < leftEnd; i++ {
		if i > 0 {
			lTop.WriteString(bSt.Render("┬"))
			lBot.WriteString(bSt.Render("│"))
		}
		lTop.WriteString(bSt.Render(strings.Repeat("─", cells[i].width)))
		lBot.WriteString(cells[i].inner)
	}
	lTop.WriteString(bSt.Render("╮"))
	lBot.WriteString(bSt.Render("│"))

	if rightCount <= 0 {
		return padLine(lTop.String(), m.width) + "\n" + padLine(lBot.String(), m.width)
	}

	// --- Right group ---
	var rTop, rBot strings.Builder
	rTop.WriteString(bSt.Render("╭"))
	rBot.WriteString(bSt.Render("│"))
	for i := ctbLeftCount; i < fitting; i++ {
		if i > ctbLeftCount {
			rTop.WriteString(bSt.Render("┬"))
			rBot.WriteString(bSt.Render("│"))
		}
		rTop.WriteString(bSt.Render(strings.Repeat("─", cells[i].width)))
		rBot.WriteString(cells[i].inner)
	}

	leftW := lipgloss.Width(lTop.String())
	rightW := lipgloss.Width(rTop.String())
	gap := max(m.width-leftW-rightW, 0)
	spacer := strings.Repeat(" ", gap)

	row1 := lTop.String() + spacer + rTop.String()
	row2 := lBot.String() + spacer + rBot.String()

	return padLine(row1, m.width) + "\n" + padLine(row2, m.width)
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
// or -1 if outside all buttons. Accounts for the gap between left and
// right toolbar groups.
func (m *Model) conflictToolbarHitTest(x int) int {
	fitting := m.ctbFittingCount()
	leftEnd := min(ctbLeftCount, fitting)
	rightCount := max(fitting-ctbLeftCount, 0)

	// Hit test left group.
	col := 0
	for i := 0; i < leftEnd; i++ {
		w := m.ctbCellWidth(i)
		if i > 0 {
			col++ // separator
		}
		if x >= col && x < col+w {
			return i
		}
		col += w
	}

	if rightCount <= 0 {
		return -1
	}

	// Right group is right-aligned. Compute its start column.
	rightW := 1 // leading ╭/│
	for i := ctbLeftCount; i < fitting; i++ {
		if i > ctbLeftCount {
			rightW++ // separator
		}
		rightW += m.ctbCellWidth(i)
	}
	rightX := m.width - rightW

	col = rightX + 1 // skip leading ╭/│
	for i := ctbLeftCount; i < fitting; i++ {
		w := m.ctbCellWidth(i)
		if i > ctbLeftCount {
			col++ // separator
		}
		if x >= col && x < col+w {
			return i
		}
		col += w
	}

	return -1
}
