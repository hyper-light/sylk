package layout

import (
	"sync"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/charmbracelet/lipgloss"
)

// DefaultMinPanelWidth is derived from the minimum usable content area:
// 36 chars content + 2 border + 2 padding = 40.
const DefaultMinPanelWidth = 40

// Manager computes panel positions based on terminal size.
// It supports four layout modes: SingleColumn, TwoColumn, ThreeColumn, FourColumn.
// Breakpoints are derived from each panel's CollapseWidth threshold.
type Manager struct {
	mu           sync.RWMutex
	width        int
	height       int
	mode         LayoutMode
	maxMode      LayoutMode // upper bound on mode selection; -1 = uncapped.
	panels       []PanelSpec
	candidates   []ModeCandidate
	panelIndex   map[component.FocusID]PanelSpec
	sizes        PanelSizes
	panelWidths  map[component.FocusID]int
	panelHeights map[component.FocusID]int
}

// NewManager creates a Manager with the given initial dimensions, panel specs,
// and mode candidates (ordered widest to narrowest).
func NewManager(width, height int, panels []PanelSpec, candidates []ModeCandidate) *Manager {
	m := &Manager{
		width:        width,
		height:       height,
		maxMode:      -1,
		panels:       panels,
		candidates:   candidates,
		panelIndex:   buildPanelIndex(panels),
		panelWidths:  make(map[component.FocusID]int, len(panels)),
		panelHeights: make(map[component.FocusID]int, len(panels)),
	}

	m.recompute()

	return m
}

// buildPanelIndex creates a lookup map from panel ID to PanelSpec.
func buildPanelIndex(panels []PanelSpec) map[component.FocusID]PanelSpec {
	idx := make(map[component.FocusID]PanelSpec, len(panels))
	for _, p := range panels {
		idx[p.ID] = p
	}
	return idx
}

// SetSize updates terminal dimensions and recomputes all panel sizes.
func (m *Manager) SetSize(width, height int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.width = width
	m.height = height
	m.recompute()
}

// Mode returns the current layout mode.
func (m *Manager) Mode() LayoutMode {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.mode
}

// SetMaxMode caps the layout mode to at most the given value. Subsequent
// calls to SetSize or recompute will not select a mode wider than this.
// Use ClearMaxMode to remove the cap.
func (m *Manager) SetMaxMode(mode LayoutMode) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.maxMode = mode
	m.recompute()
}

// ClearMaxMode removes the mode cap set by SetMaxMode, allowing the layout
// to select the widest mode that fits.
func (m *Manager) ClearMaxMode() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.maxMode = -1
	m.recompute()
}

// GetPanelSize returns the computed width and height for a panel by ID.
// Returns (0, 0) for unknown or invisible panels.
func (m *Manager) GetPanelSize(id component.FocusID) (width, height int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.panelWidths[id], m.panelHeights[id]
}

// Panels returns a copy of the current panel specs.
func (m *Manager) Panels() []PanelSpec {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]PanelSpec, len(m.panels))
	copy(out, m.panels)

	return out
}

// SetPanelVisible updates the visibility of a panel and recomputes layout.
func (m *Manager) SetPanelVisible(id component.FocusID, visible bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.panels {
		if m.panels[i].ID == id {
			m.panels[i].Visible = visible
			break
		}
	}

	m.recompute()
}

// Sizes returns the current computed panel sizes.
func (m *Manager) Sizes() PanelSizes {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.sizes
}

// RenderColumns arranges rendered panel strings into a horizontal row
// using lipgloss.JoinHorizontal, respecting the current layout mode.
func (m *Manager) RenderColumns(columns ...string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cols := min(len(columns), columnCount(m.mode, len(columns)))

	if cols == 0 {
		return ""
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, columns[:cols]...)
}

// PanelAtX returns the FocusID of the panel whose column contains screen
// coordinate x. Returns (id, true) on hit, or (0, false) if x is out of bounds.
func (m *Manager) PanelAtX(x int) (component.FocusID, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	columns := columnsForMode(m.mode, m.candidates)
	widths := m.columnWidths()
	cols := min(len(widths), len(columns))

	cumulative := 0
	for i := range cols {
		cumulative += widths[i]
		if x < cumulative {
			return columns[i], true
		}
	}
	return 0, false
}

// recompute recalculates the layout mode, panel sizes, and per-panel
// dimension maps. Must be called with m.mu held.
func (m *Manager) recompute() {
	m.mode = modeForCandidates(m.width, m.candidates, m.panelIndex)
	if m.maxMode >= 0 && m.mode > m.maxMode {
		m.mode = m.maxMode
	}
	m.sizes = computePanelSizes(m.width, m.height, m.mode, m.candidates, m.panelIndex)
	m.assignPanelDimensions()
}

// columnWidths returns an ordered slice of the non-zero column widths
// from the current PanelSizes.
func (m *Manager) columnWidths() []int {
	candidates := []int{
		m.sizes.Left,
		m.sizes.CenterLeft,
		m.sizes.Center,
		m.sizes.Right,
	}

	widths := make([]int, 0, len(candidates))

	for _, w := range candidates {
		if w > 0 {
			widths = append(widths, w)
		}
	}

	return widths
}

// assignPanelDimensions maps each column panel to its width and the
// shared height, plus assigns off-screen ring panels the width of the
// column they share. Must be called with m.mu held.
func (m *Manager) assignPanelDimensions() {
	clear(m.panelWidths)
	clear(m.panelHeights)

	columns := columnsForMode(m.mode, m.candidates)
	widths := m.columnWidths()

	// Assign on-screen column panels.
	for i, id := range columns {
		if i < len(widths) {
			m.panelWidths[id] = widths[i]
			m.panelHeights[id] = m.sizes.Height
		}
	}

	// Assign off-screen panels the width of their shared column so that
	// GetPanelSize returns usable dimensions for ring-swapped panels.
	m.assignOffScreenPanels(columns)
}

// assignOffScreenPanels gives each panel not in the current column set
// the dimensions of its shared column. This lets ring-cycled panels
// render at the correct width before being displayed.
func (m *Manager) assignOffScreenPanels(onScreen []component.FocusID) {
	onScreenSet := make(map[component.FocusID]bool, len(onScreen))
	for _, id := range onScreen {
		onScreenSet[id] = true
	}

	for _, p := range m.panels {
		if onScreenSet[p.ID] {
			continue
		}
		if _, assigned := m.panelWidths[p.ID]; assigned {
			continue
		}
		sharedW, sharedH := m.findSharedColumnDims(p.ID)
		m.panelWidths[p.ID] = sharedW
		m.panelHeights[p.ID] = sharedH
	}
}

// findSharedColumnDims returns the dimensions of the column that an
// off-screen panel shares with. The sharing rules are:
//   - FileTree shares with Session (Left column)
//   - Code shares with Chat (Center column)
//   - Session shares with Chat (Left column in SingleColumn)
func (m *Manager) findSharedColumnDims(id component.FocusID) (int, int) {
	switch id {
	case component.FocusFileTree:
		return m.panelWidths[component.FocusSessionPanel], m.panelHeights[component.FocusSessionPanel]
	case component.FocusCodeViewer:
		return m.panelWidths[component.FocusChat], m.panelHeights[component.FocusChat]
	case component.FocusSessionPanel:
		return m.panelWidths[component.FocusChat], m.panelHeights[component.FocusChat]
	default:
		// Fallback: use the first on-screen column.
		if m.sizes.Left > 0 {
			return m.sizes.Left, m.sizes.Height
		}
		return 0, 0
	}
}
