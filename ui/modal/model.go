package modal

import (
	"strings"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// maxModalStack is the upper bound on nested modals to prevent unbounded growth.
// Derived from UI usability: 5 levels of modal nesting covers confirm dialogs
// spawned from pickers spawned from forms.
const maxModalStack = 5

// ModalContent defines the interface for modal implementations.
type ModalContent interface {
	// View renders the modal content within the given dimensions.
	View(width, height int) string

	// Update processes a key press. Returns done=true when the modal should close,
	// an optional result value, and an optional command.
	Update(key tea.KeyMsg) (done bool, result any, cmd tea.Cmd)
}

// ModalClosedMsg is sent when a modal closes, carrying its result.
type ModalClosedMsg struct {
	Result any
}

// Model is the Bubble Tea model for the modal overlay system.
type Model struct {
	stack   []ModalContent // Bounded by maxModalStack.
	theme   *theme.Theme
	width   int
	height  int
	focused bool
}

// Verify interface compliance at compile time.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a modal overlay Model with the given theme.
func New(th *theme.Theme) *Model {
	return &Model{
		stack: make([]ModalContent, 0, maxModalStack),
		theme: th,
	}
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns the initial command (none).
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update processes incoming messages. When active, the modal captures
// all key input.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	if len(m.stack) == 0 {
		return m, nil
	}

	key, ok := incoming.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	return m, m.handleKey(key)
}

// View renders the topmost modal as a centered overlay.
func (m *Model) View() string {
	if len(m.stack) == 0 {
		return ""
	}

	top := m.stack[len(m.stack)-1]
	content := top.View(m.modalContentWidth(), m.modalContentHeight())
	return m.renderOverlay(content)
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the focus identifier for the modal overlay.
func (m *Model) ID() component.FocusID {
	return component.FocusModal
}

// Focused returns whether the modal overlay has focus.
func (m *Model) Focused() bool {
	return m.focused
}

// SetFocused sets the focus state.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
}

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates the available dimensions.
func (m *Model) SetSize(width, height int) {
	m.width = max(width, 0)
	m.height = max(height, 0)
}

// ---------------------------------------------------------------------------
// Stack operations
// ---------------------------------------------------------------------------

// Push adds a modal to the stack. If the stack is full, the push is ignored.
func (m *Model) Push(modal ModalContent) {
	if len(m.stack) >= maxModalStack {
		return
	}
	m.stack = append(m.stack, modal)
}

// Pop removes and returns the topmost modal. Returns nil if the stack is empty.
func (m *Model) Pop() ModalContent {
	if len(m.stack) == 0 {
		return nil
	}
	top := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	return top
}

// Clear removes all modals from the stack.
func (m *Model) Clear() {
	m.stack = m.stack[:0]
}

// Active returns true if there are any modals on the stack.
func (m *Model) Active() bool {
	return len(m.stack) > 0
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

// handleKey delegates the key to the topmost modal.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	top := m.stack[len(m.stack)-1]
	done, result, cmd := top.Update(key)
	if done {
		m.Pop()
		return tea.Batch(cmd, func() tea.Msg { return ModalClosedMsg{Result: result} })
	}
	return cmd
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// modalContentWidth returns the available width for modal content.
// Reserves space for the overlay border (2 chars per side).
// Derived from: border(1) + padding(1) each side = 4 total.
const overlayBorderWidth = 4

// modalContentHeight reserves vertical space for the overlay border.
// Derived from: border(1) + padding(1) top and bottom = 4 total.
const overlayBorderHeight = 4

func (m *Model) modalContentWidth() int {
	return max(m.width-overlayBorderWidth, 0)
}

func (m *Model) modalContentHeight() int {
	return max(m.height-overlayBorderHeight, 0)
}

// renderOverlay centers the content in the terminal and draws a border.
func (m *Model) renderOverlay(content string) string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Palette.BorderActive).
		Padding(1).
		Width(m.modalContentWidth()).
		MaxHeight(m.height)

	box := boxStyle.Render(content)

	// Center the box vertically and horizontally.
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	hPad := max((m.width-boxWidth)/2, 0)
	vPad := max((m.height-boxHeight)/2, 0)

	padded := indent(box, hPad)
	topPadding := strings.Repeat("\n", vPad)
	return topPadding + padded
}

// indent prepends each line with n spaces.
func indent(s string, n int) string {
	if n <= 0 {
		return s
	}
	prefix := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
