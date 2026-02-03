package layout

import "github.com/adalundhe/sylk/ui/component"

// LayoutMode describes how panels are arranged horizontally.
type LayoutMode int

const (
	SingleColumn LayoutMode = iota
	TwoColumn
	ThreeColumn
)

// PanelSpec describes a panel's layout constraints.
type PanelSpec struct {
	ID       component.FocusID
	MinWidth int
	MinHeight int
	FlexGrow float64
	Visible  bool
}

// PanelSizes holds computed widths for each column region and the shared height.
type PanelSizes struct {
	Left   int
	Center int
	Right  int
	Height int
}

// modeForWidth determines layout mode from terminal width and the minimum
// panel width. Breakpoints are derived: TwoColumn requires 2*minPanel,
// ThreeColumn requires 3*minPanel.
func modeForWidth(width, minPanel int) LayoutMode {
	type breakpoint struct {
		threshold int
		mode      LayoutMode
	}

	breakpoints := []breakpoint{
		{threshold: 3 * minPanel, mode: ThreeColumn},
		{threshold: 2 * minPanel, mode: TwoColumn},
	}

	for _, bp := range breakpoints {
		if width >= bp.threshold {
			return bp.mode
		}
	}

	return SingleColumn
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
	}

	cols := modeColumns[mode]

	return min(cols, panelCount)
}

// computePanelSizes distributes available width across columns using
// flex-grow weights from visible panels, and sets a uniform height.
func computePanelSizes(width, height int, mode LayoutMode, panels []PanelSpec) PanelSizes {
	visible := visiblePanels(panels)
	cols := columnCount(mode, len(visible))

	if cols == 0 {
		return PanelSizes{Height: height}
	}

	widths := distributeWidth(width, cols, visible)

	return PanelSizes{
		Left:   widths[0],
		Center: safeIndex(widths, 1),
		Right:  safeIndex(widths, 2),
		Height: height,
	}
}

// distributeWidth allocates width proportionally to flex-grow weights.
// Each column is backed by one visible panel (by index). Panels beyond
// the column count are folded into the last column's weight.
func distributeWidth(width, cols int, visible []PanelSpec) []int {
	weights := gatherWeights(cols, visible)
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

// gatherWeights collects flex-grow weights for each column. If there are
// more visible panels than columns, extra panel weights are added to the
// last column.
func gatherWeights(cols int, visible []PanelSpec) []float64 {
	weights := make([]float64, cols)
	const defaultWeight = 1.0

	for i, p := range visible {
		w := p.FlexGrow
		if w <= 0 {
			w = defaultWeight
		}

		idx := min(i, cols-1)
		weights[idx] += w
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
