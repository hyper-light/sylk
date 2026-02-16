package diffview

import (
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// toolbarButton IDs for the diff view toolbar.
const (
	tbChain    = iota // Compare mode: chain
	tbAllFirst        // Compare mode: all vs first
	tbPairs           // Compare mode: sequential pairs
	tbSBS             // View mode: side-by-side
	tbUnified         // View mode: unified
	tbClose           // Close diff view
	tbCount           // sentinel
)

// tbDef defines a toolbar button's display properties.
type tbDef struct {
	icon   string
	label  string
	accent func(theme.Palette) lipgloss.Color
}

var tbDefs = [tbCount]tbDef{
	tbChain:    {icon: "⇢", label: "Chain", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	tbAllFirst: {icon: "⇶", label: "All vs 1st", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	tbPairs:    {icon: "⇔", label: "Pairs", accent: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	tbSBS:      {icon: "▥", label: "Side-by-side", accent: func(p theme.Palette) lipgloss.Color { return p.Secondary }},
	tbUnified:  {icon: "▤", label: "Unified", accent: func(p theme.Palette) lipgloss.Color { return p.Secondary }},
	tbClose:    {icon: "✕", label: "Close", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
}

// toolbarButtonCount is the total number of toolbar buttons.
const toolbarButtonCount = tbCount

// tbCellWidth returns the visual width of a toolbar button cell.
func tbCellWidth(id int) int {
	def := tbDefs[id]
	return lipgloss.Width(" " + def.icon + " " + def.label + " ")
}

// renderToolbar renders the diff view toolbar with two groups:
// left (compare modes, no left edge) and right (view mode toggle + close).
// Matches the committree renderDiffToolbar pattern: left group has no left
// border, separators between buttons, right cap; right group has left cap.
func renderToolbar(mode CompareMode, sideBySide bool, focused bool,
	selectedIdx int, hoverIdx int, width int, p theme.Palette) string {

	bSt := lipgloss.NewStyle().Foreground(p.Border)

	leftIDs := []int{tbChain, tbAllFirst, tbPairs}
	rightIDs := []int{tbSBS, tbUnified, tbClose}

	// Determine active buttons.
	leftActive := int(mode) // 0=chain, 1=allFirst, 2=pairs
	var rightActive int
	if sideBySide {
		rightActive = 0 // tbSBS index within right group
	} else {
		rightActive = 1 // tbUnified index within right group
	}

	// Measure button groups.
	type btnCell struct {
		inner string
		width int
	}

	buildCells := func(ids []int, offset int, activeInGroup int) ([]btnCell, []int, int) {
		cells := make([]btnCell, len(ids))
		widths := make([]int, len(ids))
		total := 0
		for i, id := range ids {
			def := tbDefs[id]
			w := tbCellWidth(id)
			uIdx := offset + i
			active := i == activeInGroup
			selected := focused && uIdx == selectedIdx
			hovered := uIdx == hoverIdx
			fg := p.Muted
			if active || selected || hovered {
				fg = def.accent(p)
			}
			text := lipgloss.NewStyle().Foreground(fg).Bold(active).Render(def.icon + " " + def.label)
			padded := " " + text + " "
			cells[i] = btnCell{inner: padded, width: w}
			widths[i] = w
			total += w
		}
		return cells, widths, total
	}

	lCells, lWidths, _ := buildCells(leftIDs, 0, leftActive)
	rCells, rWidths, rTotal := buildCells(rightIDs, len(leftIDs), rightActive)

	// Group widths: separators between + caps + cell widths.
	rGroupWidth := len(rCells) + rTotal

	// Row 1: left group top border (no left edge, right cap ╮),
	// gap, right group top border (left cap ╭).
	var lDiv strings.Builder
	for i, w := range lWidths {
		if i > 0 {
			lDiv.WriteString("┬")
		}
		lDiv.WriteString(strings.Repeat("─", w))
	}
	lDiv.WriteString("╮")
	leftBorderStr := bSt.Render(lDiv.String())
	leftBorderVis := lipgloss.Width(leftBorderStr)

	var rDiv strings.Builder
	for i, w := range rWidths {
		if i == 0 {
			rDiv.WriteString("╭")
		} else {
			rDiv.WriteString("┬")
		}
		rDiv.WriteString(strings.Repeat("─", w))
	}
	rightBorderStr := bSt.Render(rDiv.String())
	rightBorderVis := lipgloss.Width(rightBorderStr)

	row1Gap := max(width-leftBorderVis-rightBorderVis, 0)
	row1Final := leftBorderStr + strings.Repeat(" ", row1Gap) + rightBorderStr
	if vis := lipgloss.Width(row1Final); vis < width {
		row1Final += strings.Repeat(" ", width-vis)
	}

	// Row 2: left buttons (no left separator, │ between, │ after last),
	// gap, right buttons (│ before each).
	var row2 strings.Builder
	for i, c := range lCells {
		if i > 0 {
			row2.WriteString(bSt.Render("│"))
		}
		row2.WriteString(c.inner)
	}
	row2.WriteString(bSt.Render("│"))
	leftContentVis := lipgloss.Width(row2.String())

	row2Gap := max(width-leftContentVis-rGroupWidth, 0)
	row2.WriteString(strings.Repeat(" ", row2Gap))

	for _, c := range rCells {
		row2.WriteString(bSt.Render("│"))
		row2.WriteString(c.inner)
	}
	row2Final := row2.String()
	if vis := lipgloss.Width(row2Final); vis < width {
		row2Final += strings.Repeat(" ", width-vis)
	}

	return row1Final + "\n" + row2Final
}

// toolbarHitTest returns the unified button index for a click at column x,
// or -1 if outside all buttons.
func toolbarHitTest(mode CompareMode, sideBySide bool, width int, x int) int {
	leftIDs := []int{tbChain, tbAllFirst, tbPairs}
	rightIDs := []int{tbSBS, tbUnified, tbClose}

	// Left group starts at column 0, no left separator.
	col := 0
	for i, id := range leftIDs {
		w := tbCellWidth(id)
		if i > 0 {
			w++ // +1 for │ separator between buttons
		}
		if x >= col && x < col+w {
			return i
		}
		col += w
	}
	col++ // right cap │

	// Right group: calculate start position.
	lTotal := 0
	for _, id := range leftIDs {
		lTotal += tbCellWidth(id)
	}
	lGroupWidth := len(leftIDs) + lTotal // (len-1) seps + 1 cap + cells
	rTotal := 0
	for _, id := range rightIDs {
		rTotal += tbCellWidth(id)
	}
	rGroupWidth := len(rightIDs) + rTotal
	rStart := max(width-rGroupWidth, lGroupWidth)

	col = rStart
	for i, id := range rightIDs {
		w := tbCellWidth(id) + 1 // +1 for │ separator
		if x >= col && x < col+w {
			return len(leftIDs) + i
		}
		col += w
	}

	return -1
}
