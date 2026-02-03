package session

import (
	"context"

	coresession "github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
)

// maxSummaries is the upper bound on displayed session summaries.
// Derived from the session manager's default MaxSessions (100).
const maxSummaries = 100

// keyAction is a function that handles a key press on the session model.
type keyAction func(m *Model) tea.Cmd

// keyActionTable returns the table-driven key dispatch map.
func keyActionTable() map[string]keyAction {
	return map[string]keyAction{
		"j":     func(m *Model) tea.Cmd { m.moveSelection(1); return nil },
		"down":  func(m *Model) tea.Cmd { m.moveSelection(1); return nil },
		"k":     func(m *Model) tea.Cmd { m.moveSelection(-1); return nil },
		"up":    func(m *Model) tea.Cmd { m.moveSelection(-1); return nil },
		"enter": func(m *Model) tea.Cmd { return m.switchSession() },
		"n":     func(m *Model) tea.Cmd { return m.createSession() },
		"p":     func(m *Model) tea.Cmd { return m.pauseSession() },
		"r":     func(m *Model) tea.Cmd { return m.resumeSession() },
		"d":     func(m *Model) tea.Cmd { return m.closeSession() },
	}
}

// Model is the Bubble Tea model for the session panel.
type Model struct {
	manager   *coresession.Manager
	summaries []SessionSummary
	selected  int
	theme     *theme.Theme
	width     int
	height    int
	focused   bool
}

// Verify interface compliance at compile time.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a session panel Model with the given manager and theme.
func New(mgr *coresession.Manager, th *theme.Theme) *Model {
	m := &Model{
		manager:   mgr,
		summaries: make([]SessionSummary, 0, maxSummaries),
		theme:     th,
	}
	m.refreshSummaries()
	return m
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns the initial command (none).
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update processes incoming messages.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	switch typed := incoming.(type) {
	case msg.SessionEventMsg:
		return m, m.handleSessionEvent(typed)
	case tea.KeyMsg:
		return m, m.handleKey(typed)
	default:
		return m, nil
	}
}

// View renders the session panel.
func (m *Model) View() string {
	return RenderList(m.summaries, m.selected, m.width, m.height, m.focused, m.theme)
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the focus identifier for the session panel.
func (m *Model) ID() component.FocusID {
	return component.FocusSessionPanel
}

// Focused returns whether the session panel has focus.
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
// Message handlers
// ---------------------------------------------------------------------------

// handleSessionEvent refreshes the session list on any session event.
func (m *Model) handleSessionEvent(_ msg.SessionEventMsg) tea.Cmd {
	m.refreshSummaries()
	return nil
}

// handleKey processes keyboard input when focused.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if !m.focused {
		return nil
	}

	actions := keyActionTable()
	if action, ok := actions[key.String()]; ok {
		return action(m)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// moveSelection moves the cursor by delta, clamped to valid indices.
func (m *Model) moveSelection(delta int) {
	count := len(m.summaries)
	if count == 0 {
		return
	}
	m.selected = clampIndex(m.selected+delta, count)
}

// ---------------------------------------------------------------------------
// Session actions
// ---------------------------------------------------------------------------

// CyclePrev moves the selection cursor backward and activates the session.
func (m *Model) CyclePrev() tea.Cmd {
	m.moveSelection(-1)
	return m.switchSession()
}

// CycleNext moves the selection cursor forward and activates the session.
func (m *Model) CycleNext() tea.Cmd {
	m.moveSelection(1)
	return m.switchSession()
}

// switchSession switches to the selected session.
func (m *Model) switchSession() tea.Cmd {
	summary, ok := m.selectedSummary()
	if !ok {
		return nil
	}

	if err := m.manager.Switch(summary.ID); err != nil {
		return nil
	}
	m.refreshSummaries()
	return nil
}

// createSession creates a new session with a default configuration.
func (m *Model) createSession() tea.Cmd {
	cfg := coresession.DefaultConfig()
	_, err := m.manager.Create(context.Background(), cfg)
	if err != nil {
		return nil
	}
	m.refreshSummaries()
	return nil
}

// pauseSession pauses the selected session.
func (m *Model) pauseSession() tea.Cmd {
	summary, ok := m.selectedSummary()
	if !ok {
		return nil
	}

	if err := m.manager.Pause(summary.ID); err != nil {
		return nil
	}
	m.refreshSummaries()
	return nil
}

// resumeSession resumes the selected session.
func (m *Model) resumeSession() tea.Cmd {
	summary, ok := m.selectedSummary()
	if !ok {
		return nil
	}

	if err := m.manager.Resume(summary.ID); err != nil {
		return nil
	}
	m.refreshSummaries()
	return nil
}

// closeSession closes the selected session.
func (m *Model) closeSession() tea.Cmd {
	summary, ok := m.selectedSummary()
	if !ok {
		return nil
	}

	if err := m.manager.Close(summary.ID); err != nil {
		return nil
	}
	m.refreshSummaries()
	m.selected = clampIndex(m.selected, max(len(m.summaries), 1))
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// selectedSummary returns the currently selected session summary.
func (m *Model) selectedSummary() (SessionSummary, bool) {
	if m.selected < 0 || m.selected >= len(m.summaries) {
		return SessionSummary{}, false
	}
	return m.summaries[m.selected], true
}

// refreshSummaries rebuilds the summary list from the session manager.
func (m *Model) refreshSummaries() {
	sessions := m.manager.List()
	activeSession, hasActive := m.manager.GetActive()

	m.summaries = m.summaries[:0]
	for _, s := range sessions {
		if len(m.summaries) >= maxSummaries {
			break
		}
		active := hasActive && s.ID() == activeSession.ID()
		m.summaries = append(m.summaries, SessionSummary{
			ID:        s.ID(),
			Name:      s.Name(),
			Branch:    s.Branch(),
			State:     s.State(),
			CreatedAt: s.CreatedAt(),
			Active:    active,
		})
	}
}

// clampIndex constrains an index to [0, count-1].
func clampIndex(idx, count int) int {
	return max(0, min(idx, count-1))
}
