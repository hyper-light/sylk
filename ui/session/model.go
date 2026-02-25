package session

import (
	"context"
	"slices"
	"time"

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
	manager      *coresession.Manager
	summaries    []SessionSummary
	displayOrder []string // Session IDs in stable display order.
	selected     int
	theme        *theme.Theme
	width        int
	height       int
	focused      bool

	// Dot animation for active session when agents are working.
	dotFrame       int
	hasActiveAgent bool
	shimmerStart   time.Time
	dotGradient    *theme.Gradient

	// Border gradients for tree connectors and section footer.
	// Selected at render time: active when focused + agents working, idle
	// when focused without activity, nil when unfocused (static fallback).
	idleGroupGradient   *theme.Gradient // Subdued: green → jade → blue → white.
	activeGroupGradient *theme.Gradient // Full prismatic spectrum.
}

// Verify interface compliance at compile time.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a session panel Model with the given manager and theme.
func New(mgr *coresession.Manager, th *theme.Theme) *Model {
	idleGroup := th.Palette.IdleGroupGradient()
	m := &Model{
		manager:             mgr,
		summaries:           make([]SessionSummary, 0, maxSummaries),
		theme:               th,
		shimmerStart:        time.Now(),
		dotGradient:         th.Palette.RippleGradient(),
		idleGroupGradient:   idleGroup,
		activeGroupGradient: th.Palette.GroupGradient(),
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

// sessionDotPhaseOffset shifts the session dot's gradient phase relative to
// the agent dot. Half the 4-second ripple cycle ensures the two dots are
// never color-synchronized, creating organic visual depth.
const sessionDotPhaseOffset = 2 * time.Second

// View renders the session panel.
func (m *Model) View() string {
	// Border gradient: full prismatic when focused + active, subdued idle
	// shimmer otherwise. Tree connectors and footer always animate — focus
	// only controls whether the palette is prismatic or subdued.
	borderGrad := m.idleGroupGradient
	if m.focused && m.hasActiveAgent {
		borderGrad = m.activeGroupGradient
	}

	anim := SessionAnimState{
		DotFrame:       m.dotFrame,
		Elapsed:        time.Since(m.shimmerStart) + sessionDotPhaseOffset,
		HasActiveAgent: m.hasActiveAgent,
		Focused:        m.focused,
		Gradient:       m.dotGradient,
		BorderGradient: borderGrad,
	}
	return RenderList(m.summaries, m.selected, m.width, m.height, m.theme, anim)
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

// switchSession switches to the selected session, updating Active flags
// in place without re-sorting the list.
func (m *Model) switchSession() tea.Cmd {
	summary, ok := m.selectedSummary()
	if !ok {
		return nil
	}

	if err := m.manager.Switch(summary.ID); err != nil {
		return nil
	}

	for i := range m.summaries {
		m.summaries[i].Active = m.summaries[i].ID == summary.ID
	}
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
// Dot animation
// ---------------------------------------------------------------------------

// SetAgentActive updates whether agents are currently working. Called by the
// app during decor ticks to sync agent panel activity into the session dot.
func (m *Model) SetAgentActive(active bool) {
	m.hasActiveAgent = active
}

// AdvanceDotFrame increments the origami-bloom dot animation frame counter.
func (m *Model) AdvanceDotFrame() {
	m.dotFrame = (m.dotFrame + 1) % theme.DotAnimFrameCount
}

// HasActiveAgent reports whether the session dot should animate.
func (m *Model) HasActiveAgent() bool {
	return m.hasActiveAgent
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
// On initial load, sessions are sorted by UpdatedAt descending and
// the cursor is synced to the active session. On subsequent refreshes,
// the display order is preserved and the cursor is restored by session ID.
func (m *Model) refreshSummaries() {
	sessions := m.manager.List()
	activeSession, hasActive := m.manager.GetActive()

	fresh := make(map[string]*coresession.Session, len(sessions))
	for _, s := range sessions {
		fresh[s.ID()] = s
	}

	selectedID := m.selectedID()
	m.syncDisplayOrder(fresh, sessions)
	m.rebuildSummaries(fresh, activeSession, hasActive)
	m.restoreSelected(selectedID)
}

// selectedID returns the ID of the currently selected session, or empty.
func (m *Model) selectedID() string {
	if m.selected >= 0 && m.selected < len(m.summaries) {
		return m.summaries[m.selected].ID
	}
	return ""
}

// syncDisplayOrder updates the stable ID list. On initial load, it builds
// the order sorted by UpdatedAt descending. On subsequent calls, it keeps
// the existing order, removes deleted sessions, and prepends new ones.
func (m *Model) syncDisplayOrder(fresh map[string]*coresession.Session, sessions []*coresession.Session) {
	if len(m.displayOrder) == 0 {
		sorted := slices.Clone(sessions)
		slices.SortFunc(sorted, func(a, b *coresession.Session) int {
			return b.UpdatedAt().Compare(a.UpdatedAt())
		})
		m.displayOrder = make([]string, 0, len(sorted))
		for _, s := range sorted {
			m.displayOrder = append(m.displayOrder, s.ID())
		}
		return
	}

	existing := make(map[string]bool, len(m.displayOrder))
	for _, id := range m.displayOrder {
		existing[id] = true
	}

	var added []string
	for _, s := range sessions {
		if !existing[s.ID()] {
			added = append(added, s.ID())
		}
	}

	kept := m.displayOrder[:0]
	for _, id := range m.displayOrder {
		if _, ok := fresh[id]; ok {
			kept = append(kept, id)
		}
	}

	m.displayOrder = append(added, kept...)
}

// rebuildSummaries populates summaries from the display order.
func (m *Model) rebuildSummaries(fresh map[string]*coresession.Session, activeSession *coresession.Session, hasActive bool) {
	m.summaries = m.summaries[:0]
	for _, id := range m.displayOrder {
		s, ok := fresh[id]
		if !ok {
			continue
		}
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
			UpdatedAt: s.UpdatedAt(),
			Active:    active,
		})
	}
}

// restoreSelected sets the cursor to the session matching selectedID.
// Falls back to the active session, then clamps to bounds.
func (m *Model) restoreSelected(selectedID string) {
	for i, s := range m.summaries {
		if s.ID == selectedID {
			m.selected = i
			return
		}
	}
	for i, s := range m.summaries {
		if s.Active {
			m.selected = i
			return
		}
	}
	m.selected = clampIndex(m.selected, max(len(m.summaries), 1))
}

// clampIndex constrains an index to [0, count-1].
func clampIndex(idx, count int) int {
	return max(0, min(idx, count-1))
}
