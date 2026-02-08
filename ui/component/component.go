package component

import tea "github.com/charmbracelet/bubbletea"

// FocusID identifies a focusable component in the layout.
type FocusID int

const (
	FocusInput FocusID = iota
	FocusChat
	FocusSessionPanel
	FocusAgentPanel
	FocusCodeViewer
	FocusKnowledge
	FocusAgentDetail
	FocusModal
	FocusSearch
	FocusEditor
	FocusFileTree
	FocusFieldManual
	FocusPreview

	// FocusPaneBase is the starting FocusID for dynamically allocated
	// editor panes. Each pane gets FocusPaneBase + FocusID(paneID).
	FocusPaneBase FocusID = 1000
)

// Component defines the contract for all TUI sub-models.
// Each component is independently testable.
type Component interface {
	// Init returns an initial command to run.
	Init() tea.Cmd

	// Update processes a message and returns the updated component and a command.
	Update(msg tea.Msg) (Component, tea.Cmd)

	// View renders the component to a string.
	View() string
}

// Focusable extends Component with focus management.
type Focusable interface {
	Component

	// ID returns the focus identifier for this component.
	ID() FocusID

	// Focused returns whether this component currently has focus.
	Focused() bool

	// SetFocused sets the focus state on this component.
	SetFocused(focused bool)
}

// Resizable extends Component with resize handling.
type Resizable interface {
	// SetSize updates the component's available dimensions.
	SetSize(width, height int)
}
