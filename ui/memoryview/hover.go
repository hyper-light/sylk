package memoryview

import (
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/forest"
)

// tooltip geometry — chosen so the overlay fits inside narrow chat
// panels (~60 cols) without clipping.
const (
	tooltipMaxWidth  = 44
	tooltipMinWidth  = 18
	tooltipMaxHeight = 9
	tooltipPadX      = 1
	tooltipPadY      = 0
	hoverMargin      = 1
)

// hoverState carries everything needed to resolve mouse cell → node
// and to render the tooltip. A single hoverState instance lives on
// the Model; it's reset on canvas resize or snapshot change.
type hoverState struct {
	// cell coordinates in canvas-local space (matches positions map).
	// -1 means "no hover".
	cellX int
	cellY int
	// hitNodeID is the resolved node under (cellX, cellY) or "" if
	// the cursor isn't over any node.
	hitNodeID string
	// dirty is set when cell coordinates changed since the last paint;
	// the renderer rehits and rebuilds the tooltip each dirty frame.
	dirty bool
}

func newHoverState() hoverState {
	return hoverState{cellX: -1, cellY: -1}
}

// setCell updates the hover cell and flags the tooltip for rebuild.
// Returns true if the cell changed (caller can use this to coalesce
// mouse events).
func (h *hoverState) setCell(x, y int) bool {
	if h.cellX == x && h.cellY == y {
		return false
	}
	h.cellX, h.cellY = x, y
	h.dirty = true
	return true
}

// clear resets hover state. Called on canvas resize, snapshot change,
// or when the cursor leaves the canvas.
func (h *hoverState) clear() {
	h.cellX, h.cellY = -1, -1
	h.hitNodeID = ""
	h.dirty = true
}

// resolveHover finds the node under the hover cell, if any. The match
// includes a 1-cell halo so mouse precision on tight canopies is
// forgiving. Selection order: exact cell wins over halo; among halo
// hits, closest by Manhattan distance.
func (m *Model) resolveHover() string {
	if m.hover.cellX < 0 || m.hover.cellY < 0 {
		return ""
	}
	best := ""
	bestDist := 1 << 30
	for id, pos := range m.positions {
		dx := pos.x - m.hover.cellX
		dy := pos.y - m.hover.cellY
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx > hoverMargin || dy > hoverMargin {
			continue
		}
		dist := dx + dy
		if dist < bestDist {
			bestDist = dist
			best = id
		}
	}
	return best
}

// tooltipFor builds the wrapped text lines rendered inside the tooltip
// box for the given node. Lines are returned as (text, styleClass)
// pairs so the renderer can style them when painting into the grid.
func (m *Model) tooltipFor(nodeID string) []tooltipLine {
	node, ok := m.nodeByID[nodeID]
	if !ok {
		return nil
	}
	tree, treeLabel := m.lookupTreeLabel(nodeID)
	lines := make([]tooltipLine, 0, 6)

	title := strings.TrimSpace(node.Title)
	if title == "" {
		title = strings.TrimSpace(node.Summary)
	}
	if title == "" {
		title = "Untitled branch"
	}
	for _, l := range wrapTooltipText(title, tooltipMaxWidth-2*tooltipPadX, 2) {
		lines = append(lines, tooltipLine{text: l, style: styleTooltipAccent})
	}

	summary := strings.TrimSpace(node.Summary)
	if summary != "" && summary != title {
		wrapped := wrapTooltipText(summary, tooltipMaxWidth-2*tooltipPadX, 2)
		for _, l := range wrapped {
			lines = append(lines, tooltipLine{text: l, style: styleTooltipText})
		}
	}

	if treeLabel != "" {
		lines = append(lines, tooltipLine{text: "[" + treeLabel + "]", style: styleTooltipDim})
	}
	_ = tree

	meta := tooltipMetaLine(node)
	if meta != "" {
		lines = append(lines, tooltipLine{text: meta, style: styleTooltipDim})
	}
	if updated := formatTooltipAge(node.UpdatedAt); updated != "" {
		lines = append(lines, tooltipLine{text: "updated " + updated, style: styleTooltipDim})
	}
	if len(lines) > tooltipMaxHeight {
		lines = lines[:tooltipMaxHeight]
	}
	return lines
}

type tooltipLine struct {
	text  string
	style styleClass
}

// paintTooltip overlays the tooltip box on the supplied grid anchored
// to the hover cell. The box is auto-flipped left or below the cursor
// so it stays inside canvas bounds, preferring up-and-right.
func (m *Model) paintTooltip(grid *canvasGrid) {
	if m.hover.hitNodeID == "" || grid == nil {
		return
	}
	lines := m.tooltipFor(m.hover.hitNodeID)
	if len(lines) == 0 {
		return
	}
	width := tooltipMinWidth
	for _, l := range lines {
		if w := runeLen(l.text); w+2*tooltipPadX > width {
			width = w + 2*tooltipPadX
		}
	}
	if width > tooltipMaxWidth {
		width = tooltipMaxWidth
	}
	width = clamp(width, tooltipMinWidth, min(tooltipMaxWidth, grid.w-2))
	height := len(lines) + 2 + 2*tooltipPadY // +2 for top/bottom borders

	// Preferred anchor: one cell up-and-right of the hover.
	hx, hy := m.hover.cellX, m.hover.cellY
	x := hx + 2
	y := hy - height - 1
	// Flip horizontally if overflowing right.
	if x+width >= grid.w {
		x = hx - width - 2
	}
	if x < 0 {
		x = max(0, min(grid.w-width, hx-width/2))
	}
	// Flip vertically if clipping the top.
	if y < 0 {
		y = hy + 2
	}
	if y+height >= grid.h {
		y = max(0, grid.h-height)
	}
	x = clamp(x, 0, max(grid.w-width, 0))
	y = clamp(y, 0, max(grid.h-height, 0))

	drawTooltipBox(grid, x, y, width, height, lines)
}

// drawTooltipBox paints a rounded-corner rectangle with the given
// lines centered-left inside. Uses setForced everywhere so the box is
// guaranteed to overdraw the forest beneath it.
func drawTooltipBox(grid *canvasGrid, x, y, width, height int, lines []tooltipLine) {
	// Borders
	grid.setForced(x, y, '╭', styleTooltipBorder)
	grid.setForced(x+width-1, y, '╮', styleTooltipBorder)
	grid.setForced(x, y+height-1, '╰', styleTooltipBorder)
	grid.setForced(x+width-1, y+height-1, '╯', styleTooltipBorder)
	for i := 1; i < width-1; i++ {
		grid.setForced(x+i, y, '─', styleTooltipBorder)
		grid.setForced(x+i, y+height-1, '─', styleTooltipBorder)
	}
	for j := 1; j < height-1; j++ {
		grid.setForced(x, y+j, '│', styleTooltipBorder)
		grid.setForced(x+width-1, y+j, '│', styleTooltipBorder)
	}
	// Fill interior with blanks (so the forest underneath doesn't peek through).
	for j := 1; j < height-1; j++ {
		for i := 1; i < width-1; i++ {
			grid.setForced(x+i, y+j, ' ', styleTooltipText)
		}
	}
	// Paint each line, left-aligned with tooltipPadX.
	for i, line := range lines {
		ly := y + 1 + tooltipPadY + i
		if ly >= y+height-1 {
			break
		}
		text := truncateToWidth(line.text, width-2-2*tooltipPadX)
		grid.drawStringForced(x+1+tooltipPadX, ly, text, line.style)
	}
}

func wrapTooltipText(text string, width, maxLines int) []string {
	text = strings.TrimSpace(text)
	if text == "" || width <= 0 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, maxLines)
	var cur strings.Builder
	for _, word := range words {
		if cur.Len() == 0 {
			if runeLen(word) > width {
				word = truncateToWidth(word, width)
			}
			cur.WriteString(word)
			continue
		}
		// Try to append.
		test := cur.String() + " " + word
		if runeLen(test) <= width {
			cur.Reset()
			cur.WriteString(test)
			continue
		}
		// Flush current line.
		lines = append(lines, cur.String())
		if len(lines) >= maxLines {
			// replace last line with truncated continuation.
			lines[maxLines-1] = truncateToWidth(lines[maxLines-1]+"…", width)
			return lines
		}
		cur.Reset()
		if runeLen(word) > width {
			word = truncateToWidth(word, width)
		}
		cur.WriteString(word)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = truncateToWidth(lines[maxLines-1]+"…", width)
	}
	return lines
}

func truncateToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runeLen(s) <= width {
		return s
	}
	runes := []rune(s)
	if width <= 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "…"
}

func runeLen(s string) int {
	return len([]rune(s))
}

func tooltipMetaLine(node forest.ViewNode) string {
	bits := make([]string, 0, 4)
	if scope := strings.TrimSpace(string(node.Scope)); scope != "" {
		bits = append(bits, scope)
	}
	if state := strings.TrimSpace(string(node.State)); state != "" {
		bits = append(bits, state)
	}
	if node.Confidence > 0 {
		bits = append(bits, fmt.Sprintf("conf %.2f", node.Confidence))
	}
	if node.Salience > 0 {
		bits = append(bits, fmt.Sprintf("sal %.2f", node.Salience))
	}
	return strings.Join(bits, " · ")
}

func formatTooltipAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	since := time.Since(t)
	switch {
	case since < time.Minute:
		return "just now"
	case since < time.Hour:
		return fmt.Sprintf("%dm ago", int(since.Minutes()))
	case since < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(since.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(since.Hours()/24))
	}
}

// lookupTreeLabel returns the containing tree's family + label for a
// node, used in tooltip metadata. The second return is the human
// label shown; first is the family for future use.
func (m *Model) lookupTreeLabel(nodeID string) (forest.TreeFamily, string) {
	idx, ok := m.treeByBranch[nodeID]
	if !ok || m.snapshot == nil || idx < 0 || idx >= len(m.snapshot.Trees) {
		return "", ""
	}
	tree := m.snapshot.Trees[idx]
	return tree.Family, strings.TrimSpace(tree.Label)
}
