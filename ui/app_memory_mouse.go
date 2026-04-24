package ui

import (
	"github.com/adalundhe/sylk/ui/component"
	tea "github.com/charmbracelet/bubbletea"
)

// handleMemoryCanvasHoverMouse routes mouse cell-motion events to the
// memory view's hover resolver when memory mode is active. Mouse
// coordinates are converted from screen space into canvas-local
// coordinates (the space used by memoryview.Model.positions).
//
// The handler returns (nil, false) so click/wheel events continue to
// flow through the rest of the mouse pipeline; only hover motion is
// consumed here.
func (m *AppModel) handleMemoryCanvasHoverMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.viewMode != ViewMemory || m.memoryView == nil {
		return nil, false
	}
	if mouse.Action != tea.MouseActionMotion {
		return nil, false
	}
	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	if chatW == 0 || chatH == 0 {
		return nil, false
	}
	chatX := m.chatPanelX()
	contentLeft := chatX + 1
	contentRight := chatX + chatW - 1
	contentTop := 1
	contentBottom := 1 + max(chatH-panelBorderSize, 0)

	if mouse.X < contentLeft || mouse.X >= contentRight ||
		mouse.Y < contentTop || mouse.Y >= contentBottom {
		if m.memoryView.SetHoverCell(-1, -1) {
			m.viewDirty = true
		}
		return nil, false
	}
	canvasX := mouse.X - contentLeft
	canvasY := mouse.Y - contentTop
	if m.memoryView.SetHoverCell(canvasX, canvasY) {
		// Hover changed → mark the view dirty so the next render
		// reflects tooltip movement. No redraw command needed; the
		// Bubble Tea event loop will repaint on the next tick or
		// mouse event.
		m.viewDirty = true
	}
	return nil, false
}
