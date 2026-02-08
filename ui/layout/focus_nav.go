// Package layout provides hierarchical focus navigation for panel grids.
//
// The navigation algorithm is fully generic — it operates on grid geometry
// and never references specific FocusID constants. Panel identity is the
// sole responsibility of the grid builder (in ui/app.go).
package layout

import "github.com/adalundhe/sylk/ui/component"

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// PanelGroup is a focusable region containing a 2D sub-grid of navigable
// areas. The sub-grid must have at least one row, and every row must have
// at least one entry.
type PanelGroup struct {
	SubPanels [][]component.FocusID
}

// GridPos locates a FocusID within the hierarchical grid.
type GridPos struct {
	Row, Col       int // position in the main grid
	SubRow, SubCol int // position within the PanelGroup's sub-grid
}

// Direction encodes a navigation direction.
type Direction int

const (
	DirRight Direction = iota
	DirLeft
	DirDown
	DirUp
)

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// FindInGrid locates id in the hierarchical grid. Returns the position and
// true if found, or a zero GridPos and false if the id is absent.
func FindInGrid(grid [][]PanelGroup, id component.FocusID) (GridPos, bool) {
	for r, row := range grid {
		for c, pg := range row {
			for sr, subRow := range pg.SubPanels {
				for sc, fid := range subRow {
					if fid == id {
						return GridPos{Row: r, Col: c, SubRow: sr, SubCol: sc}, true
					}
				}
			}
		}
	}
	return GridPos{}, false
}

// Navigate returns the target FocusID for a directional move from pos.
// If the grid contains only one unique cell (navigation would be a no-op),
// it returns the current cell's ID and false.
func Navigate(grid [][]PanelGroup, pos GridPos, dir Direction) (component.FocusID, bool) {
	if len(grid) == 0 {
		return 0, false
	}
	var target component.FocusID
	switch dir {
	case DirRight:
		target = navigateRight(grid, pos)
	case DirLeft:
		target = navigateLeft(grid, pos)
	case DirDown:
		target = navigateDown(grid, pos)
	case DirUp:
		target = navigateUp(grid, pos)
	default:
		return idAt(grid, pos), false
	}
	current := idAt(grid, pos)
	if target == current {
		return current, false
	}
	return target, true
}

// ---------------------------------------------------------------------------
// Directional helpers
// ---------------------------------------------------------------------------

// navigateRight: sub-col+1 → sub-row+1 col=0 → next panel top-left →
// next main-row first panel top-left. Wraps last row → first.
func navigateRight(grid [][]PanelGroup, pos GridPos) component.FocusID {
	pg := grid[pos.Row][pos.Col]
	subRow := pg.SubPanels[pos.SubRow]

	// Try moving right within sub-row.
	if pos.SubCol+1 < len(subRow) {
		return subRow[pos.SubCol+1]
	}

	// Try wrapping to first item of next sub-row.
	if pos.SubRow+1 < len(pg.SubPanels) {
		return pg.SubPanels[pos.SubRow+1][0]
	}

	// Exit panel: next panel in the same main row.
	mainRow := grid[pos.Row]
	if pos.Col+1 < len(mainRow) {
		return topLeft(mainRow[pos.Col+1])
	}

	// Wrap to the first panel of the next main row.
	nextRow := grid[wrapIdx(pos.Row+1, len(grid))]
	return topLeft(nextRow[0])
}

// navigateLeft: sub-col-1 → sub-row-1 last-col → previous panel
// bottom-right → previous main-row last panel bottom-right.
// Wraps first row → last.
func navigateLeft(grid [][]PanelGroup, pos GridPos) component.FocusID {
	pg := grid[pos.Row][pos.Col]

	// Try moving left within sub-row.
	if pos.SubCol > 0 {
		return pg.SubPanels[pos.SubRow][pos.SubCol-1]
	}

	// Try wrapping to last item of previous sub-row.
	if pos.SubRow > 0 {
		prev := pg.SubPanels[pos.SubRow-1]
		return prev[len(prev)-1]
	}

	// Exit panel: previous panel in the same main row.
	mainRow := grid[pos.Row]
	if pos.Col > 0 {
		return bottomRight(mainRow[pos.Col-1])
	}

	// Wrap to the last panel of the previous main row.
	prevRow := grid[wrapIdx(pos.Row-1, len(grid))]
	return bottomRight(prevRow[len(prevRow)-1])
}

// navigateDown: sub-row+1 col=0 → next main-row first panel top-left.
// Wraps last row → first.
func navigateDown(grid [][]PanelGroup, pos GridPos) component.FocusID {
	pg := grid[pos.Row][pos.Col]

	// Try moving to leftmost item of next sub-row.
	if pos.SubRow+1 < len(pg.SubPanels) {
		return pg.SubPanels[pos.SubRow+1][0]
	}

	// Exit panel: leftmost panel of next main row.
	nextRow := grid[wrapIdx(pos.Row+1, len(grid))]
	return topLeft(nextRow[0])
}

// navigateUp: sub-row-1 col=0 → previous main-row first panel top-left.
// Wraps first row → last.
func navigateUp(grid [][]PanelGroup, pos GridPos) component.FocusID {
	pg := grid[pos.Row][pos.Col]

	// Try moving to leftmost item of previous sub-row.
	if pos.SubRow > 0 {
		return pg.SubPanels[pos.SubRow-1][0]
	}

	// Exit panel: leftmost panel of previous main row.
	prevRow := grid[wrapIdx(pos.Row-1, len(grid))]
	return topLeft(prevRow[0])
}

// ---------------------------------------------------------------------------
// Grid accessors
// ---------------------------------------------------------------------------

// topLeft returns the FocusID at sub-position (0, 0) of a PanelGroup.
func topLeft(pg PanelGroup) component.FocusID {
	return pg.SubPanels[0][0]
}

// bottomRight returns the FocusID at the last sub-row's last column.
func bottomRight(pg PanelGroup) component.FocusID {
	last := pg.SubPanels[len(pg.SubPanels)-1]
	return last[len(last)-1]
}

// idAt returns the FocusID at the given grid position.
func idAt(grid [][]PanelGroup, pos GridPos) component.FocusID {
	return grid[pos.Row][pos.Col].SubPanels[pos.SubRow][pos.SubCol]
}

// wrapIdx performs safe modular indexing: ((i % n) + n) % n.
func wrapIdx(i, n int) int {
	return ((i % n) + n) % n
}
