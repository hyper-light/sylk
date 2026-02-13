package committree

// LaneGlyph identifies the visual element rendered in a graph gutter cell.
type LaneGlyph int8

const (
	LaneEmpty      LaneGlyph = iota // " "
	LanePass                        // │ — lane passes through this row
	LaneCommit                      // ● — commit occupies this lane
	LaneMergeRight                  // ╮ — incoming merge from a lane to the right
	LaneBranchLeft                  // ╰ — lane merges left toward parent
	LaneHoriz                       // ─ — horizontal connector between lanes
)

// GraphRow describes the graph gutter for one commit in the topo-sorted list.
type GraphRow struct {
	CommitLane int         // lane index where ● appears
	Lanes      []LaneGlyph // glyph per lane position (len = MaxLane+1)
	MaxLane    int         // rightmost active lane index
}

// laneWidth is the number of terminal columns per lane (glyph + space).
const laneWidth = 2

// maxGraphLanes caps the number of visual lanes to prevent runaway width.
// Derived from: typical panels are 40-80 cols; 16 lanes × 2 cols = 32 cols
// leaves room for a card. Excess merges show without visual lane connectors.
const maxGraphLanes = 16

// AssignLanes performs a single-pass streaming lane assignment over
// topo-sorted nodes. Returns one GraphRow per node and the widest lane index.
func AssignLanes(nodes []TreeNode) ([]GraphRow, int) {
	if len(nodes) == 0 {
		return nil, 0
	}

	// activeLanes[i] holds the hash that lane i is reserved for.
	// Empty string means the lane is free.
	activeLanes := make([]string, 0, 4)
	rows := make([]GraphRow, len(nodes))
	globalMax := 0

	for idx, node := range nodes {
		lane := findOrAllocLane(&activeLanes, node.Hash)
		row := initGraphRow(lane, activeLanes)

		// First parent inherits the commit's lane.
		if len(node.Parents) > 0 {
			activeLanes[lane] = node.Parents[0]
		} else {
			activeLanes[lane] = "" // root commit: free the lane
		}

		// Additional parents get merge connectors.
		assignMergeParents(&activeLanes, &row, node.Parents, lane)

		row.MaxLane = maxActiveLane(activeLanes)
		globalMax = max(globalMax, row.MaxLane)
		rows[idx] = row
	}

	return rows, globalMax
}

// findOrAllocLane returns the lane holding targetHash, or allocates a new
// lane (reusing a free slot if possible).
func findOrAllocLane(lanes *[]string, targetHash string) int {
	for i, h := range *lanes {
		if h == targetHash {
			return i
		}
	}
	// Reuse first free lane.
	for i, h := range *lanes {
		if h == "" {
			(*lanes)[i] = targetHash
			return i
		}
	}
	// Allocate new lane.
	idx := len(*lanes)
	*lanes = append(*lanes, targetHash)
	return idx
}

// initGraphRow creates a GraphRow with LanePass for all occupied lanes
// and LaneCommit at the commit lane.
func initGraphRow(commitLane int, activeLanes []string) GraphRow {
	glyphs := make([]LaneGlyph, len(activeLanes))
	for i, h := range activeLanes {
		if h != "" {
			glyphs[i] = LanePass
		}
	}
	glyphs[commitLane] = LaneCommit
	return GraphRow{
		CommitLane: commitLane,
		Lanes:      glyphs,
	}
}

// assignMergeParents handles parents beyond the first, assigning or
// finding lanes and adding merge connector glyphs to the row.
func assignMergeParents(lanes *[]string, row *GraphRow, parents []string, commitLane int) {
	for pi := 1; pi < len(parents); pi++ {
		ph := parents[pi]
		mergeLane := findOrAllocLane(lanes, ph)

		// Grow row glyphs if needed.
		for len(row.Lanes) <= mergeLane {
			row.Lanes = append(row.Lanes, LaneEmpty)
		}

		// Mark the merge endpoint.
		if row.Lanes[mergeLane] == LaneEmpty {
			row.Lanes[mergeLane] = LaneMergeRight
		}

		// Fill horizontal connectors between commit lane and merge lane.
		lo := min(commitLane, mergeLane) + 1
		hi := max(commitLane, mergeLane)
		for c := lo; c < hi; c++ {
			for len(row.Lanes) <= c {
				row.Lanes = append(row.Lanes, LaneEmpty)
			}
			if row.Lanes[c] == LaneEmpty {
				row.Lanes[c] = LaneHoriz
			}
		}
	}
}

// maxActiveLane returns the index of the rightmost occupied lane, or 0.
func maxActiveLane(lanes []string) int {
	m := 0
	for i, h := range lanes {
		if h != "" {
			m = i
		}
	}
	return m
}
