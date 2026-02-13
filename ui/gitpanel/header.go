package gitpanel

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/theme"
)

// dropOverlayAlpha is the blend factor for the column drag-drop highlight.
const dropOverlayAlpha = 0.15

// renderHeaderRow renders the column header line between the search divider
// and the entry list. Columns are left-aligned within their computed widths,
// separated by two spaces. Every sortable column shows a sort indicator:
// ▲ (ascending) or ▼ (descending) for the active sort column, ↕ for
// inactive sortable columns. A gear icon is rendered flush-right.
func renderHeaderRow(ls *listState, width int, th *theme.Theme) string {
	if ls.columns == nil {
		return padToWidth("", width, th.Palette)
	}

	p := th.Palette
	vis := ls.columns.VisibleColumns()
	headerFocused := ls.filterFocus == focusHeader

	var b strings.Builder

	divStyle := lipgloss.NewStyle().Foreground(p.Border)
	owners := headerFocusOwner(vis)

	hoverDivStyle := lipgloss.NewStyle().Foreground(p.Secondary)

	for i, col := range vis {
		if hasSepBefore(vis, i) {
			ds := divStyle
			if ls.header.hoverDivider == i-1 {
				ds = hoverDivStyle
			}
			b.WriteString(ds.Render(" \u2502 ")) // " │ "
		}

		// Determine focus and hover: empty-label columns inherit from owner.
		ownerIdx := owners[i]
		isFocused := headerFocused && ownerIdx == ls.header.cursor
		isHovered := ls.header.hoverColumn >= 0 && ownerIdx == ls.header.hoverColumn
		highlighted := isFocused || isHovered

		style := lipgloss.NewStyle().Foreground(p.Muted).Bold(true)
		if highlighted {
			style = style.Foreground(p.Foreground)
		}

		if col.Def.Label == "" {
			// Decorative column — render as highlighted padding.
			b.WriteString(fitHeaderCell("", col.Width, style))
			continue
		}

		label := col.Def.Label
		indicator := sortIndicator(col.Def, ls.columns.SortKeys)
		indicatorW := len([]rune(indicator))
		labelSpace := max(col.Width-indicatorW, 0)
		truncLabel := truncateString(label, labelSpace)

		// Active sort indicators keep their accent color regardless of
		// hover/focus state; inactive indicators follow the label style.
		if isSortActive(col.Def, ls.columns.SortKeys) {
			sortStyle := lipgloss.NewStyle().Foreground(p.Secondary).Bold(true)
			b.WriteString(fitHeaderCellSplit(truncLabel, indicator, col.Width, style, sortStyle))
		} else {
			cellText := truncLabel + indicator
			b.WriteString(fitHeaderCell(cellText, col.Width, style))
		}
	}

	// Gear icon flush-right with divider separator.
	gearStyle := lipgloss.NewStyle().Foreground(p.Muted)
	if (headerFocused && ls.header.cursor >= len(vis)) || ls.header.gearOpen {
		gearStyle = gearStyle.Foreground(p.Primary)
	}
	gearDiv := divStyle.Render(" \u2502 ")
	gear := gearStyle.Render("\u2699 ")

	content := b.String()
	contentWidth := lipgloss.Width(content)
	gearWidth := lipgloss.Width(gearDiv) + lipgloss.Width(gear)

	padCount := max(width-contentWidth-gearWidth, 0)
	content += strings.Repeat(" ", padCount) + gearDiv + gear

	return content
}

// isSortActive reports whether the column has an active sort key.
func isSortActive(def *ColumnDef, sortKeys []SortKey) bool {
	for _, sk := range sortKeys {
		if sk.Col == def.ID {
			return true
		}
	}
	return false
}

// sortIndicator returns the sort indicator suffix for a column header.
// Active sort columns show ▲ or ▼ (with priority number when multi-sort);
// inactive sortable columns show ↕; non-sortable columns return empty string.
func sortIndicator(def *ColumnDef, sortKeys []SortKey) string {
	if def.SortAsc == nil {
		return ""
	}

	for i, sk := range sortKeys {
		if sk.Col != def.ID {
			continue
		}
		arrow := " \u25b2" // ▲
		if sk.Dir == SortDesc {
			arrow = " \u25bc" // ▼
		}
		// Show priority number only when multiple sort keys are active.
		if len(sortKeys) > 1 {
			arrow += string(rune('₁' + i))
		}
		return arrow
	}

	return " \u2195" // ↕
}

// gearDropdownWidth computes the total dropdown width including borders.
// Inner: " [x] <label> ". Outer adds 2 for left+right border chars.
// Only counts columns that appear in the dropdown (non-empty labels).
func gearDropdownWidth(cl *ColumnLayout) int {
	const innerPad = 6 // " [x] " (5) + trailing " " (1)
	maxLabel := 0
	for _, idx := range cl.dropdownIndices() {
		col := cl.Columns[idx]
		label := col.Def.Label
		maxLabel = max(maxLabel, len(label))
	}
	return maxLabel + innerPad + 2 // +2 for │ borders
}

// renderGearDropdown renders the column visibility dropdown as fixed-width
// lines with solid box borders, suitable for overlay. Only columns with
// non-empty labels are shown (decorative columns are excluded).
func renderGearDropdown(ls *listState, dropW int, th *theme.Theme) []string {
	if ls.columns == nil {
		return nil
	}

	p := th.Palette
	indices := ls.columns.dropdownIndices()
	innerW := dropW - 2 // subtract left+right border
	borderStyle := lipgloss.NewStyle().Foreground(p.Border).Background(p.PopupBg)
	bgStyle := lipgloss.NewStyle().Background(p.PopupBg)

	lines := make([]string, 0, len(indices)+2) // +2 for top/bottom

	// Top border: ┌───┐
	topBar := "\u250c" + strings.Repeat("\u2500", innerW) + "\u2510"
	lines = append(lines, borderStyle.Render(topBar))

	for cursor, colIdx := range indices {
		col := ls.columns.Columns[colIdx]
		isFocused := ls.filterFocus == focusGearDropdown && cursor == ls.header.gearCursor

		check := "[ ]"
		if col.Visible {
			check = "[x]"
		}

		bg := p.PopupBg
		fg := p.Foreground
		if !col.Def.Hideable {
			fg = p.Muted
		}
		if isFocused {
			bg = p.Selection
		}

		contentStyle := lipgloss.NewStyle().Foreground(fg).Background(bg)
		inner := fitHeaderCell(" "+check+" "+col.Def.Label, innerW, contentStyle)
		line := borderStyle.Render("\u2502") + inner + borderStyle.Render("\u2502")
		lines = append(lines, line)
	}

	// Bottom border: └───┘
	bottomBar := "\u2514" + strings.Repeat("\u2500", innerW) + "\u2518"
	lines = append(lines, bgStyle.Foreground(p.Border).Render(bottomBar))

	return lines
}

// applyGearOverlay splices dropdown lines onto entry lines at the right edge.
// Entry content to the left of the dropdown and to the right is preserved.
func applyGearOverlay(entryLines, dropLines []string, panelWidth, dropWidth int) {
	col := max(panelWidth-dropWidth-1, 0)
	for i, dLine := range dropLines {
		if i >= len(entryLines) {
			break
		}
		left := truncateStyledLine(entryLines[i], col)
		right := skipStyledCols(entryLines[i], col+dropWidth)
		entryLines[i] = left + dLine + right
	}
}

// truncateStyledLine returns the first w visible columns of a styled string,
// preserving ANSI escape sequences and appending a reset.
func truncateStyledLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var buf strings.Builder
	vis := 0
	i := 0
	for i < len(s) && vis < w {
		if s[i] == '\x1b' {
			j := skipCSI(s, i)
			buf.WriteString(s[i:j])
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		buf.WriteString(s[i : i+size])
		vis++
		i += size
	}
	buf.WriteString("\x1b[0m")
	return buf.String()
}

// skipStyledCols drops the first skip visible columns from a styled string
// and returns the remainder with a leading ANSI reset.
func skipStyledCols(s string, skip int) string {
	if skip <= 0 {
		return s
	}
	vis := 0
	i := 0
	for i < len(s) && vis < skip {
		if s[i] == '\x1b' {
			i = skipCSI(s, i)
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		vis++
		i += size
	}
	if i >= len(s) {
		return ""
	}
	return "\x1b[0m" + s[i:]
}

// skipCSI advances past a CSI escape sequence starting at position i.
func skipCSI(s string, i int) int {
	j := i + 1
	if j < len(s) && s[j] == '[' {
		j++
		for j < len(s) && !isCSIFinal(s[j]) {
			j++
		}
		if j < len(s) {
			j++
		}
	}
	return j
}

// isCSIFinal reports whether b is a CSI final byte.
func isCSIFinal(b byte) bool { return b >= 0x40 && b <= 0x7E }

// headerFocusOwner maps each visible column index to the navigable column
// that owns its header space. Empty-label columns (decorative icons/markers)
// are owned by the next non-empty-label column.
func headerFocusOwner(vis []ColumnState) []int {
	owners := make([]int, len(vis))
	next := len(vis)
	for i := len(vis) - 1; i >= 0; i-- {
		if vis[i].Def.Label != "" {
			next = i
		}
		owners[i] = next
	}
	return owners
}

// clickHeaderColumn maps an X coordinate on the header row to:
//
//	column index (>=0) for a column cell,
//	-1 for the gear icon,
//	-2 for nothing,
//	-3 - dividerIdx for a divider (e.g. -3 = divider after column 0).
//
// Clicks on empty-label columns are mapped to their focus owner.
func clickHeaderColumn(ls *listState, viewX, width int) int {
	if ls.columns == nil {
		return -2
	}

	// Check gear icon region (last gearIconWidth columns).
	if viewX >= width-gearIconWidth {
		return -1
	}

	vis := ls.columns.VisibleColumns()
	owners := headerFocusOwner(vis)
	x := 0
	for i, col := range vis {
		if hasSepBefore(vis, i) {
			// Divider occupies [x, x+colSepWidth).
			if viewX >= x && viewX < x+colSepWidth {
				return -3 - (i - 1) // divider after column i-1
			}
			x += colSepWidth
		}
		if viewX >= x && viewX < x+col.Width {
			owner := owners[i]
			if owner < len(vis) {
				return owner
			}
			return -2
		}
		x += col.Width
	}

	return -2
}

// fitCell renders text into a cell of exactly the given width, truncating
// if too long and padding with spaces if too short. The style is applied to
// the entire cell (text + padding) so selection background fills the width.
func fitCell(text string, width int, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	text = truncateString(text, width)
	if pad := width - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return style.Render(text)
}

// fitHeaderCell renders text into a header cell of exactly the given width.
// Only the text content carries the style; padding is unstyled so that
// header highlight does not bleed into empty space.
func fitHeaderCell(text string, width int, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	text = truncateString(text, width)
	rendered := style.Render(text)
	if actual := lipgloss.Width(rendered); actual < width {
		rendered += strings.Repeat(" ", width-actual)
	}
	return rendered
}

// fitHeaderCellSplit renders a header cell where the label and suffix use
// different styles. The total cell is exactly width columns, with unstyled
// padding after the suffix.
func fitHeaderCellSplit(label, suffix string, width int, labelStyle, suffixStyle lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	label = truncateString(label, max(width-len([]rune(suffix)), 0))
	rendered := labelStyle.Render(label) + suffixStyle.Render(suffix)
	if actual := lipgloss.Width(rendered); actual < width {
		rendered += strings.Repeat(" ", width-actual)
	}
	return rendered
}

// dragDropTarget returns the visible column index under the current drag
// position, or -1 if no column reorder drag is active.
func dragDropTarget(ls *listState, width int) int {
	if ls.header.dragFrom < 0 {
		return -1
	}
	col := clickHeaderColumn(ls, ls.header.dragX, width)
	if col < 0 || col == ls.header.dragFrom {
		return -1
	}
	return col
}

// dropHighlightBg computes the 24-bit ANSI background code for the
// drag-drop column highlight: Primary blended onto Background at 15%.
func dropHighlightBg(p theme.Palette) string {
	r, g, b := blendHexColors(p.Background, p.Primary, dropOverlayAlpha)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// tintCellBg replaces all ANSI background colors in a rendered cell with
// bgCode, producing a semi-transparent overlay effect.
func tintCellBg(s string, bgCode string) string {
	var out strings.Builder
	out.Grow(len(s) + len(s)/4)
	out.WriteString(bgCode)

	i := 0
	for i < len(s) {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			out.WriteByte(s[i])
			i++
			continue
		}
		j := i + 2
		for j < len(s) && !isCSIFinal(s[j]) {
			j++
		}
		if j >= len(s) {
			out.WriteString(s[i:])
			break
		}
		j++ // include terminator
		out.WriteString(s[i:j])
		if s[j-1] == 'm' {
			out.WriteString(bgCode)
		}
		i = j
	}
	out.WriteString("\x1b[0m")
	return out.String()
}

// blendHexColors blends two lipgloss hex colors at the given alpha (0–1).
func blendHexColors(base, overlay lipgloss.Color, alpha float64) (uint8, uint8, uint8) {
	br, bg, bb := parseHexColor(string(base))
	or, og, ob := parseHexColor(string(overlay))
	r := uint8(float64(br) + alpha*float64(int(or)-int(br)))
	g := uint8(float64(bg) + alpha*float64(int(og)-int(bg)))
	b := uint8(float64(bb) + alpha*float64(int(ob)-int(bb)))
	return r, g, b
}

// parseHexColor parses a "#RRGGBB" string into RGB components.
func parseHexColor(hex string) (uint8, uint8, uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	return uint8(r), uint8(g), uint8(b)
}
