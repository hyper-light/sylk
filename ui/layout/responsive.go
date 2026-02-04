package layout

import "github.com/adalundhe/sylk/ui/component"

// LayoutMode describes how panels are arranged horizontally.
type LayoutMode int

const (
	SingleColumn LayoutMode = iota
	TwoColumn
	ThreeColumn
	FourColumn
)

// PanelSpec describes a panel's layout constraints.
type PanelSpec struct {
	ID            component.FocusID
	MinWidth      int
	MinHeight     int
	FlexGrow      float64
	CollapseWidth int // Allocated width below which the panel collapses.
	Visible       bool
}

// PanelSizes holds computed widths for each column region and the shared height.
type PanelSizes struct {
	Left       int
	CenterLeft int // FileTree column; 0 when mode < FourColumn.
	Center     int
	Right      int
	Height     int
}

// ModeCandidate defines which panels occupy which columns for a given mode.
// Candidates are evaluated from widest to narrowest; the first that fits wins.
type ModeCandidate struct {
	Mode    LayoutMode
	Columns []component.FocusID
}

// modeForCandidates determines the layout mode by iterating candidates from
// widest to narrowest. For each candidate it looks up the PanelSpec for every
// column, distributes width via flex-grow, and checks collapse thresholds.
func modeForCandidates(width int, candidates []ModeCandidate, index map[component.FocusID]PanelSpec) LayoutMode {
	for _, c := range candidates {
		specs := lookupSpecs(c.Columns, index)
		widths := distributeWidth(width, len(specs), specs)
		if panelsFitCollapse(specs, widths) {
			return c.Mode
		}
	}
	return SingleColumn
}

// lookupSpecs resolves column IDs to PanelSpecs via the index map.
func lookupSpecs(columns []component.FocusID, index map[component.FocusID]PanelSpec) []PanelSpec {
	specs := make([]PanelSpec, len(columns))
	for i, id := range columns {
		specs[i] = index[id]
	}
	return specs
}

// panelsFitCollapse returns true when every panel's allocated width meets
// its CollapseWidth threshold. Panels with CollapseWidth <= 0 are unchecked.
func panelsFitCollapse(panels []PanelSpec, widths []int) bool {
	for i, p := range panels {
		if p.CollapseWidth > 0 && i < len(widths) && widths[i] < p.CollapseWidth {
			return false
		}
	}
	return true
}

// visiblePanels returns the subset of panels where Visible is true.
func visiblePanels(panels []PanelSpec) []PanelSpec {
	result := make([]PanelSpec, 0, len(panels))

	for _, p := range panels {
		if p.Visible {
			result = append(result, p)
		}
	}

	return result
}

// columnCount returns the number of columns for a given layout mode, capped
// by the number of visible panels.
func columnCount(mode LayoutMode, panelCount int) int {
	modeColumns := map[LayoutMode]int{
		SingleColumn: 1,
		TwoColumn:    2,
		ThreeColumn:  3,
		FourColumn:   4,
	}

	cols := modeColumns[mode]

	return min(cols, panelCount)
}

// computePanelSizes distributes available width across columns for the active
// mode candidate and sets a uniform height.
func computePanelSizes(width, height int, mode LayoutMode, candidates []ModeCandidate, index map[component.FocusID]PanelSpec) PanelSizes {
	columns := columnsForMode(mode, candidates)
	cols := len(columns)

	if cols == 0 {
		return PanelSizes{Height: height}
	}

	specs := lookupSpecs(columns, index)
	widths := distributeWidth(width, cols, specs)

	return PanelSizes{
		Left:       safeIndex(widths, 0),
		CenterLeft: safeIndex(widths, 1),
		Center:     safeIndex(widths, centerIndex(cols)),
		Right:      safeIndex(widths, rightIndex(cols)),
		Height:     height,
	}
}

// centerIndex returns the width-array index for the Center column.
// 1-col → 0 (reuse Left), 2-col → 1, 3-col → 1, 4-col → 2.
func centerIndex(cols int) int {
	switch cols {
	case 4:
		return 2
	case 3:
		return 1
	case 2:
		return 1
	default:
		return 0
	}
}

// rightIndex returns the width-array index for the Right column.
// 1-col → 0, 2-col → 1, 3-col → 2, 4-col → 3.
func rightIndex(cols int) int {
	return max(cols-1, 0)
}

// columnsForMode finds the candidate matching the given mode.
func columnsForMode(mode LayoutMode, candidates []ModeCandidate) []component.FocusID {
	for _, c := range candidates {
		if c.Mode == mode {
			return c.Columns
		}
	}
	return nil
}

// distributeWidth allocates width proportionally to flex-grow weights.
// Each column maps to exactly one panel by index.
func distributeWidth(width, cols int, panels []PanelSpec) []int {
	weights := gatherWeights(cols, panels)
	totalWeight := sumWeights(weights)

	widths := make([]int, cols)
	allocated := 0

	for i := range cols - 1 {
		widths[i] = int(float64(width) * weights[i] / totalWeight)
		allocated += widths[i]
	}

	// Last column absorbs rounding remainder.
	widths[cols-1] = width - allocated

	return widths
}

// gatherWeights collects flex-grow weights for each column. Each column
// maps to exactly one panel; panels beyond the column count are ignored.
func gatherWeights(cols int, panels []PanelSpec) []float64 {
	weights := make([]float64, cols)
	const defaultWeight = 1.0

	for i := range min(cols, len(panels)) {
		w := panels[i].FlexGrow
		if w <= 0 {
			w = defaultWeight
		}
		weights[i] = w
	}

	return weights
}

// sumWeights returns the total of all weights, with a floor of 1.0 to
// avoid division by zero.
func sumWeights(weights []float64) float64 {
	total := 0.0

	for _, w := range weights {
		total += w
	}

	return max(total, 1.0)
}

// safeIndex returns the value at idx or 0 if out of bounds.
func safeIndex(s []int, idx int) int {
	if idx >= len(s) {
		return 0
	}

	return s[idx]
}
