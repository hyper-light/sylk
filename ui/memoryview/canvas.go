package memoryview

import (
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// canvasCell is one styled glyph in the 2D grid.
type canvasCell struct {
	ch    rune
	style styleClass
}

// canvasGrid is the backing buffer for the memory canvas. All rendering
// writes into the grid; encoding happens once in render().
type canvasGrid struct {
	w     int
	h     int
	cells [][]canvasCell
}

func newCanvasGrid(w, h int) *canvasGrid {
	cells := make([][]canvasCell, h)
	for y := range cells {
		cells[y] = make([]canvasCell, w)
		for x := range cells[y] {
			cells[y][x] = canvasCell{ch: ' ', style: styleBack}
		}
	}
	return &canvasGrid{w: w, h: h, cells: cells}
}

// clone returns a deep copy of the grid. Used to freeze a static forest
// render so per-frame particle overlays can paint atop it without
// rebuilding tree geometry from scratch.
func (g *canvasGrid) clone() *canvasGrid {
	if g == nil {
		return nil
	}
	copyCells := make([][]canvasCell, g.h)
	for y := range g.cells {
		row := make([]canvasCell, g.w)
		copy(row, g.cells[y])
		copyCells[y] = row
	}
	return &canvasGrid{w: g.w, h: g.h, cells: copyCells}
}

// set paints one cell, respecting the overlap rules: connectors yield
// to nodes, selected style is sticky, and foreground overrides blanks.
func (g *canvasGrid) set(x, y int, ch rune, style styleClass) {
	if x < 0 || x >= g.w || y < 0 || y >= g.h || ch == ' ' {
		return
	}
	current := g.cells[y][x]
	if current.ch != ' ' && isConnector(current.ch) && !isConnector(ch) {
		g.cells[y][x] = canvasCell{ch: ch, style: style}
		return
	}
	if current.ch != ' ' && !isConnector(current.ch) && isConnector(ch) {
		return
	}
	if current.ch == ' ' || current.style != styleSelected {
		g.cells[y][x] = canvasCell{ch: ch, style: style}
	}
}

// setForced writes a cell unconditionally (used by overlay layers that
// intentionally paint atop existing content — tooltip borders, etc.).
func (g *canvasGrid) setForced(x, y int, ch rune, style styleClass) {
	if x < 0 || x >= g.w || y < 0 || y >= g.h {
		return
	}
	g.cells[y][x] = canvasCell{ch: ch, style: style}
}

// setIfBlank paints only if the destination cell is empty. Used by
// ambient layers (ground dust, drifting particles) that must never
// overwrite the forest itself.
func (g *canvasGrid) setIfBlank(x, y int, ch rune, style styleClass) {
	if x < 0 || x >= g.w || y < 0 || y >= g.h || ch == ' ' {
		return
	}
	if g.cells[y][x].ch == ' ' {
		g.cells[y][x] = canvasCell{ch: ch, style: style}
	}
}

func (g *canvasGrid) drawString(x, y int, s string, style styleClass) {
	runes := []rune(s)
	for i, ch := range runes {
		g.set(x+i, y, ch, style)
	}
}

func (g *canvasGrid) drawStringForced(x, y int, s string, style styleClass) {
	runes := []rune(s)
	for i, ch := range runes {
		g.setForced(x+i, y, ch, style)
	}
}

func (g *canvasGrid) drawVertical(x, y0, y1 int, style styleClass) {
	if x < 0 || x >= g.w {
		return
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := max(y0, 0); y <= min(y1, g.h-1); y++ {
		g.set(x, y, '│', style)
	}
}

func (g *canvasGrid) drawHorizontal(y, x0, x1 int, style styleClass) {
	if y < 0 || y >= g.h {
		return
	}
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := max(x0, 0); x <= min(x1, g.w-1); x++ {
		g.set(x, y, '─', style)
	}
}

// drawDiagonal draws a connector between two points using ╲ / ╱ runes.
// The line advances one cell per step along the longer axis; the other
// axis interpolates linearly. For near-horizontal runs this produces a
// zigzag, so the caller should prefer drawConnector for tree branches.
func (g *canvasGrid) drawDiagonal(from, to point, style styleClass) {
	dx := to.x - from.x
	dy := to.y - from.y
	steps := max(abs(dx), abs(dy))
	if steps == 0 {
		return
	}
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := from.x + int(roundHalfUp(float64(dx)*t))
		y := from.y + int(roundHalfUp(float64(dy)*t))
		var glyph rune
		switch {
		case dx == 0:
			glyph = '│'
		case dy == 0:
			glyph = '─'
		case (dx > 0) == (dy > 0):
			glyph = '╲'
		default:
			glyph = '╱'
		}
		g.set(x, y, glyph, style)
	}
}

func connectorJoinY(parent, child point, siblingIndex, siblingCount int) int {
	if child.y >= parent.y {
		return child.y
	}
	upper := child.y + 1
	lower := parent.y - 1
	if lower <= upper {
		return upper
	}
	if siblingCount <= 1 {
		return upper + max((lower-upper)/2, 1)
	}
	span := lower - upper
	offset := int(roundHalfUp(float64(span) * float64(siblingIndex+1) / float64(siblingCount+1)))
	return clamp(upper+offset, upper, lower)
}

func (g *canvasGrid) drawConnector(parent, child point, joinY int, style styleClass) {
	if parent == child {
		return
	}
	if child.y >= parent.y {
		g.drawVertical(parent.x, parent.y, child.y, style)
		return
	}
	joinY = clamp(joinY, child.y+1, max(parent.y-1, child.y+1))
	g.drawVertical(child.x, child.y+1, joinY-1, style)
	if child.x < parent.x {
		g.set(child.x, joinY, '╰', style)
		for x := child.x + 1; x < parent.x; x++ {
			g.set(x, joinY, '─', style)
		}
		g.set(parent.x, joinY, '╮', style)
	} else if child.x > parent.x {
		g.set(child.x, joinY, '╯', style)
		for x := parent.x + 1; x < child.x; x++ {
			g.set(x, joinY, '─', style)
		}
		g.set(parent.x, joinY, '╭', style)
	}
	g.drawVertical(parent.x, joinY+1, parent.y-1, style)
}

// render encodes the grid into a styled string. One pass; each rune is
// wrapped in the style associated with its styleClass by the theme.
func (g *canvasGrid) render(th *theme.Theme) string {
	if g.h == 0 || g.w == 0 {
		return ""
	}
	styles := buildStylesheet(th)
	var b strings.Builder
	b.Grow(g.w * g.h * 4)
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			cell := g.cells[y][x]
			style := styles[cell.style]
			b.WriteString(style.Render(string(cell.ch)))
		}
		if y < g.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// buildStylesheet returns the lipgloss styles keyed by styleClass.
// Materialized once per render so the inner loop does table lookups
// instead of repeated lipgloss allocation.
func buildStylesheet(th *theme.Theme) [styleClassCount]lipgloss.Style {
	var out [styleClassCount]lipgloss.Style
	p := th.Palette
	out[styleBack] = lipgloss.NewStyle().Foreground(p.Muted)
	out[styleMid] = lipgloss.NewStyle().Foreground(p.Secondary)
	out[styleFront] = lipgloss.NewStyle().Foreground(p.Primary)
	out[styleSelected] = lipgloss.NewStyle().Foreground(p.Warning).Bold(true)
	out[styleRoot] = lipgloss.NewStyle().Foreground(p.Border)
	out[styleGround] = lipgloss.NewStyle().Foreground(p.Subtle)
	out[styleDust] = lipgloss.NewStyle().Foreground(p.Muted)
	out[stylePollen] = lipgloss.NewStyle().Foreground(p.Peach)
	out[styleTrail] = lipgloss.NewStyle().Foreground(p.Teal)
	out[styleShimmer] = lipgloss.NewStyle().Foreground(p.HoverAccent).Bold(true)
	out[styleTooltipBorder] = lipgloss.NewStyle().Foreground(p.Primary)
	out[styleTooltipText] = lipgloss.NewStyle().Foreground(p.Foreground)
	out[styleTooltipDim] = lipgloss.NewStyle().Foreground(p.Subtext)
	out[styleTooltipAccent] = lipgloss.NewStyle().Foreground(p.HoverAccent).Bold(true)
	return out
}

func isConnector(ch rune) bool {
	switch ch {
	case '│', '─', '╰', '╮', '╭', '╯', '╱', '╲':
		return true
	default:
		return false
	}
}
