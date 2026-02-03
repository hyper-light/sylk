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
// It supports three layout modes: SingleColumn (< threshold), TwoColumn,
// ThreeColumn. Breakpoints are derived from 2*MinPanelWidth, not magic numbers.
type Manager struct {
	mu            sync.RWMutex
	width         int
	height        int
	mode          LayoutMode
	minPanelWidth int
	panels        []PanelSpec
	sizes         PanelSizes
	panelWidths   map[component.FocusID]int
	panelHeights  map[component.FocusID]int
}

// NewManager creates a Manager with the given initial dimensions and panel specs.
func NewManager(width, height int, panels []PanelSpec) *Manager {
	m := &Manager{
		width:         width,
		height:        height,
		minPanelWidth: DefaultMinPanelWidth,
		panels:        panels,
		panelWidths:   make(map[component.FocusID]int, len(panels)),
		panelHeights:  make(map[component.FocusID]int, len(panels)),
	}

	m.recompute()

	return m
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

// recompute recalculates the layout mode, panel sizes, and per-panel
// dimension maps. Must be called with m.mu held.
func (m *Manager) recompute() {
	m.mode = modeForWidth(m.width, m.minPanelWidth)
	m.sizes = computePanelSizes(m.width, m.height, m.mode, m.panels)
	m.assignPanelDimensions()
}

// columnWidths returns an ordered slice of the non-zero column widths
// from the current PanelSizes.
func (m *Manager) columnWidths() []int {
	type entry struct {
		width int
	}

	candidates := []entry{
		{width: m.sizes.Left},
		{width: m.sizes.Center},
		{width: m.sizes.Right},
	}

	widths := make([]int, 0, len(candidates))

	for _, c := range candidates {
		if c.width > 0 {
			widths = append(widths, c.width)
		}
	}

	return widths
}

// assignPanelDimensions maps each visible panel to a column width and
// the shared height. Must be called with m.mu held.
func (m *Manager) assignPanelDimensions() {
	clear(m.panelWidths)
	clear(m.panelHeights)

	widths := m.columnWidths()
	visible := visiblePanels(m.panels)

	for i, p := range visible {
		colIdx := min(i, len(widths)-1)

		if colIdx >= 0 {
			m.panelWidths[p.ID] = widths[colIdx]
			m.panelHeights[p.ID] = m.sizes.Height
		}
	}
}
