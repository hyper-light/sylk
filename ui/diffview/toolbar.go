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

// btnCell is a styled toolbar button cell with its rendered content and width.
type btnCell struct {
	inner string
	width int
}

// sbsToActiveIdx converts the side-by-side flag to the active button index
// within the right group (0 for side-by-side, 1 for unified).
func sbsToActiveIdx(sideBySide bool) int {
	if sideBySide {
		return 0
	}
	return 1
}

// isHighlighted returns true when any of the three state flags is set.
func isHighlighted(active, selected, hovered bool) bool {
	if active {
		return true
	}
	if selected {
		return true
	}
	return hovered
}

// accentOrMuted returns the button's accent color when highlighted,
// or the muted palette color otherwise.
func accentOrMuted(id int, highlighted bool, p theme.Palette) lipgloss.Color {
	if highlighted {
		return tbDefs[id].accent(p)
	}
	return p.Muted
}

// buildToolbarCells builds styled button cells for a group of toolbar buttons.
func buildToolbarCells(ids []int, offset, activeInGroup int, focused bool,
	selectedIdx, hoverIdx int, p theme.Palette) []btnCell {

	cells := make([]btnCell, len(ids))
	for i, id := range ids {
		def := tbDefs[id]
		uIdx := offset + i
		active := i == activeInGroup
		selected := focused && uIdx == selectedIdx
		fg := accentOrMuted(id, isHighlighted(active, selected, uIdx == hoverIdx), p)
		text := lipgloss.NewStyle().Foreground(fg).Bold(active).Render(def.icon + " " + def.label)
		cells[i] = btnCell{inner: " " + text + " ", width: tbCellWidth(id)}
	}
	return cells
}

// cellWidths extracts the width of each button cell into an int slice.
func cellWidths(cells []btnCell) []int {
	ws := make([]int, len(cells))
	for i, c := range cells {
		ws[i] = c.width
	}
	return ws
}

// sumWidths returns the total width of all button cells.
func sumWidths(cells []btnCell) int {
	total := 0
	for _, c := range cells {
		total += c.width
	}
	return total
}

// buildLeftBorder builds the top border for the left button group:
// no left edge, ┬ separators between buttons, ╮ right cap.
func buildLeftBorder(widths []int) string {
	var b strings.Builder
	for i, w := range widths {
		if i > 0 {
			b.WriteString("┬")
		}
		b.WriteString(strings.Repeat("─", w))
	}
	b.WriteString("╮")
	return b.String()
}

// buildRightBorder builds the top border for the right button group:
// ╭ left cap, ┬ separators between buttons.
func buildRightBorder(widths []int) string {
	var b strings.Builder
	for i, w := range widths {
		if i == 0 {
			b.WriteString("╭")
		} else {
			b.WriteString("┬")
		}
		b.WriteString(strings.Repeat("─", w))
	}
	return b.String()
}

// padToWidth appends spaces to s until it reaches the given visual width.
func padToWidth(s string, width int) string {
	if vis := lipgloss.Width(s); vis < width {
		return s + strings.Repeat(" ", width-vis)
	}
	return s
}

// renderToolbarBorderRow builds the top border row with left and right groups
// separated by a gap.
func renderToolbarBorderRow(lWidths, rWidths []int, width int, bSt lipgloss.Style) string {
	left := bSt.Render(buildLeftBorder(lWidths))
	right := bSt.Render(buildRightBorder(rWidths))
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return padToWidth(left+strings.Repeat(" ", gap)+right, width)
}

// joinCellsSep joins button cells with a separator between them (no leading separator).
func joinCellsSep(cells []btnCell, sep string) string {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(c.inner)
	}
	return b.String()
}

// prefixedCells joins button cells with a separator before each cell.
func prefixedCells(cells []btnCell, sep string) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteString(sep)
		b.WriteString(c.inner)
	}
	return b.String()
}

// renderToolbarContentRow builds the content row with left and right button
// groups separated by a gap.
func renderToolbarContentRow(lCells, rCells []btnCell, rGroupWidth, width int, bSt lipgloss.Style) string {
	sep := bSt.Render("│")
	left := joinCellsSep(lCells, sep) + sep
	gap := max(width-lipgloss.Width(left)-rGroupWidth, 0)
	right := prefixedCells(rCells, sep)
	return padToWidth(left+strings.Repeat(" ", gap)+right, width)
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

	lCells := buildToolbarCells(leftIDs, 0, int(mode), focused, selectedIdx, hoverIdx, p)
	rCells := buildToolbarCells(rightIDs, len(leftIDs), sbsToActiveIdx(sideBySide), focused, selectedIdx, hoverIdx, p)

	lWidths := cellWidths(lCells)
	rWidths := cellWidths(rCells)

	row1 := renderToolbarBorderRow(lWidths, rWidths, width, bSt)
	row2 := renderToolbarContentRow(lCells, rCells, len(rCells)+sumWidths(rCells), width, bSt)
	return row1 + "\n" + row2
}

// sepWidth returns 1 when a separator should precede a button at the given
// index, or 0 otherwise. With leadingSep every button gets a separator;
// without it only buttons after the first do.
func sepWidth(leadingSep bool, idx int) int {
	if leadingSep || idx > 0 {
		return 1
	}
	return 0
}

// buttonSpans returns the visual column span of each button including its
// preceding separator (if any).
func buttonSpans(ids []int, leadingSep bool) []int {
	spans := make([]int, len(ids))
	for i, id := range ids {
		spans[i] = tbCellWidth(id) + sepWidth(leadingSep, i)
	}
	return spans
}

// sumInts returns the sum of all values in the slice.
func sumInts(vals []int) int {
	total := 0
	for _, v := range vals {
		total += v
	}
	return total
}

// hitInSpans returns the index of the span that contains the relative
// position relX, or -1 if relX falls outside all spans.
func hitInSpans(spans []int, relX int) int {
	col := 0
	for i, w := range spans {
		col += w
		if relX < col {
			return i
		}
	}
	return -1
}

// rightGroupStart computes the starting column of the right button group.
func rightGroupStart(leftIDs, rightIDs []int, width int) int {
	lGroupWidth := sumInts(buttonSpans(leftIDs, false)) + 1 // +1 for trailing cap │
	rGroupWidth := sumInts(buttonSpans(rightIDs, true))
	return max(width-rGroupWidth, lGroupWidth)
}

// hitRightGroup tests whether column x falls within the right button group
// and returns the unified button index, or -1.
func hitRightGroup(ids []int, offset, startCol, x int) int {
	if x < startCol {
		return -1
	}
	idx := hitInSpans(buttonSpans(ids, true), x-startCol)
	if idx >= 0 {
		return offset + idx
	}
	return -1
}

// toolbarHitTest returns the unified button index for a click at column x,
// or -1 if outside all buttons.
func toolbarHitTest(mode CompareMode, sideBySide bool, width int, x int) int {
	leftIDs := []int{tbChain, tbAllFirst, tbPairs}
	rightIDs := []int{tbSBS, tbUnified, tbClose}

	idx := hitInSpans(buttonSpans(leftIDs, false), x)
	if idx >= 0 {
		return idx
	}
	return hitRightGroup(rightIDs, len(leftIDs), rightGroupStart(leftIDs, rightIDs, width), x)
}
