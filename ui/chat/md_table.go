package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// tableCell holds the rendered inline runs and alignment for a single cell.
type tableCell struct {
	runs  []styledRun
	align east.Alignment
}

// renderTable renders a GFM table with measured column widths,
// alignment indicators, and proportional shrinking.
func renderTable(n *east.Table, ctx *blockContext) {
	cells, colCount := collectTableCells(n, ctx)
	if colCount == 0 {
		return
	}

	colWidths := measureColumns(cells, colCount)
	alignments := extractAlignments(n, colCount)
	shrinkColumns(colWidths, ctx.width, colCount)

	// Render header row.
	if len(cells) > 0 {
		ctx.lines = append(ctx.lines, renderTableRow(cells[0], colWidths, alignments, ctx.styles.tableHeader, ctx.styles)...)
	}

	// Separator line.
	ctx.lines = append(ctx.lines, renderSeparator(colWidths, alignments, ctx.styles))

	// Body rows.
	for i := 1; i < len(cells); i++ {
		ctx.lines = append(ctx.lines, renderTableRow(cells[i], colWidths, alignments, ctx.styles.text, ctx.styles)...)
	}
	ctx.lines = append(ctx.lines, "")
}

// collectTableCells walks the table AST and returns rows of cells plus the column count.
func collectTableCells(n *east.Table, ctx *blockContext) ([][]tableCell, int) {
	colCount := 0
	var rows [][]tableCell

	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch row := child.(type) {
		case *east.TableHeader:
			cells := collectRow(row, ctx)
			if len(cells) > colCount {
				colCount = len(cells)
			}
			rows = append(rows, cells)
		case *east.TableRow:
			cells := collectRow(row, ctx)
			if len(cells) > colCount {
				colCount = len(cells)
			}
			rows = append(rows, cells)
		}
	}
	return rows, colCount
}

// collectRow extracts cells from a table row node.
func collectRow(row ast.Node, ctx *blockContext) []tableCell {
	var cells []tableCell
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		tc, ok := cell.(*east.TableCell)
		if !ok {
			continue
		}
		runs := collectInlineRuns(tc, ctx.source, ctx.styles.text, ctx.styles)
		cells = append(cells, tableCell{
			runs:  runs,
			align: tc.Alignment,
		})
	}
	return cells
}

// measureColumns computes the max visible width per column across all rows.
func measureColumns(rows [][]tableCell, colCount int) []int {
	widths := make([]int, colCount)
	for _, row := range rows {
		for col, cell := range row {
			w := runsVisibleWidth(cell.runs)
			if w > widths[col] {
				widths[col] = w
			}
		}
	}
	// Minimum column width of 3 (for alignment markers in separator).
	for i := range widths {
		widths[i] = max(widths[i], 3)
	}
	return widths
}

// extractAlignments reads column alignment from the table header definition.
func extractAlignments(n *east.Table, colCount int) []east.Alignment {
	aligns := make([]east.Alignment, colCount)
	if len(n.Alignments) > 0 {
		copy(aligns, n.Alignments)
	}
	return aligns
}

// tableSepWidth is the visible width of a column separator: " │ " = 3.
const tableSepWidth = 3

// shrinkColumns proportionally reduces column widths if total exceeds available width.
func shrinkColumns(widths []int, available int, colCount int) {
	sepTotal := (colCount - 1) * tableSepWidth
	total := sepTotal
	for _, w := range widths {
		total += w
	}
	if total <= available || available <= sepTotal {
		return
	}
	contentBudget := available - sepTotal
	oldContent := total - sepTotal
	for i := range widths {
		widths[i] = max(widths[i]*contentBudget/oldContent, 3)
	}
}

// maxCellWrapLines caps the number of wrapped lines per cell to prevent
// pathologically tall rows from a single long cell.
const maxCellWrapLines = 8

// renderTableRow renders a single table row with word-wrapped cell content.
// Returns one terminal line per wrapped row height (the tallest cell
// determines the row's line count). Shorter cells are padded with blanks.
func renderTableRow(cells []tableCell, widths []int, aligns []east.Alignment, cellStyle lipgloss.Style, styles *chatMdStyles) []string {
	sep := styles.tableBorder.Render(" │ ")

	// Wrap each cell's inline runs to fit the column width.
	wrapped := make([][]string, len(widths))
	rowHeight := 1
	for col := range widths {
		if col < len(cells) {
			wrapped[col] = wrapRuns(cells[col].runs, widths[col])
		}
		if len(wrapped[col]) == 0 {
			wrapped[col] = []string{""}
		}
		if len(wrapped[col]) > maxCellWrapLines {
			wrapped[col] = wrapped[col][:maxCellWrapLines]
		}
		if len(wrapped[col]) > rowHeight {
			rowHeight = len(wrapped[col])
		}
	}

	// Build one output line per row height.
	lines := make([]string, 0, rowHeight)
	for lineIdx := range rowHeight {
		var b strings.Builder
		for col := range widths {
			if col > 0 {
				b.WriteString(sep)
			}
			var content string
			if lineIdx < len(wrapped[col]) {
				content = wrapped[col][lineIdx]
			}
			visWidth := lipgloss.Width(content)

			align := east.AlignNone
			if col < len(aligns) {
				align = aligns[col]
			}
			b.WriteString(alignCell(content, visWidth, widths[col], align, cellStyle))
		}
		lines = append(lines, b.String())
	}
	return lines
}


// alignCell pads content to fit the column width with proper alignment.
func alignCell(content string, visWidth, colWidth int, align east.Alignment, style lipgloss.Style) string {
	if visWidth > colWidth {
		// Truncate to fit.
		return truncateStyledCode(content, colWidth)
	}
	pad := colWidth - visWidth
	switch align {
	case east.AlignRight:
		return style.Render(strings.Repeat(" ", pad)) + content
	case east.AlignCenter:
		left := pad / 2
		right := pad - left
		return style.Render(strings.Repeat(" ", left)) + content + style.Render(strings.Repeat(" ", right))
	default:
		return content + style.Render(strings.Repeat(" ", pad))
	}
}

// renderSeparator renders the separator line between header and body.
func renderSeparator(widths []int, aligns []east.Alignment, styles *chatMdStyles) string {
	var b strings.Builder
	sep := styles.tableBorder.Render("─┼─")

	for col := range widths {
		if col > 0 {
			b.WriteString(sep)
		}

		align := east.AlignNone
		if col < len(aligns) {
			align = aligns[col]
		}

		rule := strings.Repeat("─", widths[col])
		switch align {
		case east.AlignLeft:
			rule = ":" + rule[1:]
		case east.AlignRight:
			rule = rule[:len(rule)-1] + ":"
		case east.AlignCenter:
			rule = ":" + rule[1:len(rule)-1] + ":"
		}
		b.WriteString(styles.tableBorder.Render(rule))
	}
	return b.String()
}

// runsVisibleWidth computes the total visible width of a slice of styledRuns.
func runsVisibleWidth(runs []styledRun) int {
	total := 0
	for _, r := range runs {
		total += r.width
	}
	return total
}
