package mergediff

import (
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// Merge diff toolbar button IDs.
// Left group: action buttons. Right group: view mode toggles.
const (
	// Left group (action buttons).
	mtbMerge       = iota // Merge into target branch.
	mtbMergeDelete        // Merge and delete source branch.
	mtbAbort              // Abort / exit merge diff view.

	// Right group (view mode toggles).
	mtbSBS     // Side-by-side
	mtbUnified // Unified

	mtbCount // sentinel
)

// Group boundaries.
const (
	mtbLeftStart  = mtbMerge
	mtbLeftEnd    = mtbAbort + 1 // exclusive
	mtbRightStart = mtbSBS
	mtbRightEnd   = mtbCount // exclusive
	mtbLeftCount  = mtbLeftEnd - mtbLeftStart
	mtbRightCount = mtbRightEnd - mtbRightStart
)

// mergeToolbarButtonCount is the total number of toolbar buttons.
const mergeToolbarButtonCount = mtbCount

// mtbDef defines a toolbar button's display properties.
type mtbDef struct {
	icon   string
	label  string
	accent func(theme.Palette) lipgloss.Color
}

var mtbDefs = [mtbCount]mtbDef{
	mtbMerge:       {icon: "⇞", label: "Merge", accent: func(p theme.Palette) lipgloss.Color { return p.Success }},
	mtbMergeDelete: {icon: "⇞", label: "Merge & Delete", accent: func(p theme.Palette) lipgloss.Color { return p.Peach }},
	mtbAbort:       {icon: "✕", label: "Abort", accent: func(p theme.Palette) lipgloss.Color { return p.Error }},
	mtbSBS:         {icon: "▥", label: "Side-by-side", accent: func(p theme.Palette) lipgloss.Color { return p.Secondary }},
	mtbUnified:     {icon: "▤", label: "Unified", accent: func(p theme.Palette) lipgloss.Color { return p.Secondary }},
}

// mtbCellWidth returns the visual width of a toolbar button cell.
func mtbCellWidth(id int) int {
	def := mtbDefs[id]
	return lipgloss.Width(" " + def.icon + " " + def.label + " ")
}

// mtbSBSToActiveRight converts the side-by-side flag to the active right
// button index (absolute index in the unified button space).
func mtbSBSToActiveRight(sideBySide bool) int {
	if sideBySide {
		return mtbSBS
	}
	return mtbUnified
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// renderMergeToolbar renders the toolbar with left action buttons and right
// view mode buttons. Uses a unified index space: 0..mtbLeftEnd-1 = left,
// mtbRightStart..mtbCount-1 = right.
func renderMergeToolbar(sideBySide bool, focused bool,
	selectedIdx int, hoverIdx int, width int, p theme.Palette) string {

	bSt := lipgloss.NewStyle().Foreground(p.Border)
	activeRight := mtbSBSToActiveRight(sideBySide)

	// Build right group cells.
	type btnCell struct {
		inner string
		width int
	}
	rCells := make([]btnCell, mtbRightCount)
	rTotal := 0
	for i := range mtbRightCount {
		id := mtbRightStart + i
		def := mtbDefs[id]
		active := id == activeRight
		selected := focused && id == selectedIdx
		hovered := id == hoverIdx
		fg := mtbAccentOrMuted(id, active || selected || hovered, p)
		text := lipgloss.NewStyle().Foreground(fg).Bold(active).Render(def.icon + " " + def.label)
		w := mtbCellWidth(id)
		rCells[i] = btnCell{inner: " " + text + " ", width: w}
		rTotal += w
	}
	rGroupWidth := mtbRightCount + rTotal // separators + content

	// Build left group cells.
	lCells := make([]btnCell, mtbLeftCount)
	for i := range mtbLeftCount {
		id := mtbLeftStart + i
		def := mtbDefs[id]
		selected := focused && id == selectedIdx
		hovered := id == hoverIdx
		fg := mtbAccentOrMuted(id, selected || hovered, p)
		text := lipgloss.NewStyle().Foreground(fg).Render(def.icon + " " + def.label)
		w := mtbCellWidth(id)
		lCells[i] = btnCell{inner: " " + text + " ", width: w}
	}

	rightLeftPad := max(width-rGroupWidth, 0)

	// Row 1: top borders for left group (left-aligned) and right group (right-aligned).
	var row1 strings.Builder
	var lDiv strings.Builder
	for i, c := range lCells {
		if i > 0 {
			lDiv.WriteString("┬")
		}
		lDiv.WriteString(strings.Repeat("─", c.width))
	}
	lDiv.WriteString("╮")
	lBorder := bSt.Render(lDiv.String())
	row1.WriteString(lBorder)
	lBorderVis := lipgloss.Width(lBorder)
	row1.WriteString(strings.Repeat(" ", max(rightLeftPad-lBorderVis, 0)))

	var rDiv strings.Builder
	for i, c := range rCells {
		if i == 0 {
			rDiv.WriteString("╭")
		} else {
			rDiv.WriteString("┬")
		}
		rDiv.WriteString(strings.Repeat("─", c.width))
	}
	row1.WriteString(bSt.Render(rDiv.String()))
	row1Str := row1.String()
	if vis := lipgloss.Width(row1Str); vis < width {
		row1Str += strings.Repeat(" ", width-vis)
	}

	// Row 2: left button content + gap + right button content.
	var row2 strings.Builder
	for i, c := range lCells {
		if i > 0 {
			row2.WriteString(bSt.Render("│"))
		}
		row2.WriteString(c.inner)
	}
	row2.WriteString(bSt.Render("│"))
	leftContentVis := lipgloss.Width(row2.String())
	row2.WriteString(strings.Repeat(" ", max(rightLeftPad-leftContentVis, 0)))
	for _, c := range rCells {
		row2.WriteString(bSt.Render("│"))
		row2.WriteString(c.inner)
	}
	row2Str := row2.String()
	if vis := lipgloss.Width(row2Str); vis < width {
		row2Str += strings.Repeat(" ", width-vis)
	}

	return row1Str + "\n" + row2Str
}

// mtbAccentOrMuted returns accent color when highlighted, muted otherwise.
func mtbAccentOrMuted(id int, highlighted bool, p theme.Palette) lipgloss.Color {
	if highlighted {
		return mtbDefs[id].accent(p)
	}
	return p.Muted
}

// ---------------------------------------------------------------------------
// Merge error toolbar (single "Ok" button)
// ---------------------------------------------------------------------------

// mergeErrorOkDef defines the Ok button appearance.
var mergeErrorOkDef = mtbDef{
	icon:   "✓",
	label:  "Ok",
	accent: func(p theme.Palette) lipgloss.Color { return p.Primary },
}

// mergeErrorOkWidth returns the visual width of the Ok button.
func mergeErrorOkWidth() int {
	return lipgloss.Width(" " + mergeErrorOkDef.icon + " " + mergeErrorOkDef.label + " ")
}

// renderMergeErrorToolbar renders a simplified toolbar with a single Ok button.
func renderMergeErrorToolbar(focused bool, selectedIdx int, hoverIdx int,
	width int, p theme.Palette) string {

	bSt := lipgloss.NewStyle().Foreground(p.Border)

	highlighted := (focused && selectedIdx == 0) || hoverIdx == 0
	fg := p.Muted
	if highlighted {
		fg = mergeErrorOkDef.accent(p)
	}
	text := lipgloss.NewStyle().Foreground(fg).
		Render(mergeErrorOkDef.icon + " " + mergeErrorOkDef.label)
	w := mergeErrorOkWidth()
	inner := " " + text + " "

	// Row 1: top border.
	border := bSt.Render(strings.Repeat("─", w) + "╮")
	row1 := border
	if vis := lipgloss.Width(row1); vis < width {
		row1 += strings.Repeat(" ", width-vis)
	}

	// Row 2: button content.
	row2 := inner + bSt.Render("│")
	if vis := lipgloss.Width(row2); vis < width {
		row2 += strings.Repeat(" ", width-vis)
	}

	return row1 + "\n" + row2
}

// mergeErrorToolbarHitTest returns 0 if x hits the Ok button, -1 otherwise.
func mergeErrorToolbarHitTest(width, x int) int {
	okW := mergeErrorOkWidth() + 1 // +1 for trailing │
	if x < okW {
		return 0
	}
	return -1
}

// ---------------------------------------------------------------------------
// Hit testing
// ---------------------------------------------------------------------------

// mtbGroupSpans precomputes cumulative column boundaries for a contiguous
// button range [start, start+count). Each entry is the right edge (exclusive)
// of that button, incorporating leading separators.
func mtbGroupSpans(start, count int, leadingSep bool) []int {
	spans := make([]int, count)
	col := 0
	for i := range count {
		if leadingSep || i > 0 {
			col++ // separator
		}
		col += mtbCellWidth(start + i)
		spans[i] = col
	}
	return spans
}

// hitTestGroup returns the button ID hit within a group, or -1.
func hitTestGroup(rel int, start int, spans []int) int {
	for i, edge := range spans {
		if rel < edge {
			return start + i
		}
	}
	return -1
}

// mergeToolbarHitTest returns the button index for a click at column x,
// or -1 if outside all buttons.
func mergeToolbarHitTest(width, x int) int {
	lSpans := mtbGroupSpans(mtbLeftStart, mtbLeftCount, false)
	lTotal := lSpans[len(lSpans)-1] + 1 // +1 for trailing │
	if x < lTotal {
		return hitTestGroup(x, mtbLeftStart, lSpans)
	}

	rSpans := mtbGroupSpans(mtbRightStart, mtbRightCount, true)
	rTotal := rSpans[len(rSpans)-1]
	rStart := max(width-rTotal, 0)
	if x >= rStart {
		return hitTestGroup(x-rStart, mtbRightStart, rSpans)
	}

	return -1
}
