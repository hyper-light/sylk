package status

import (
	"fmt"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// separatorChar is the visual delimiter between status bar sections.
const separatorChar = " | "

// flashDurationTicks is how many ticks a flash message persists.
// Derived from: 2 seconds at 16ms per tick ≈ 125 ticks.
const flashDurationTicks = 125

// droppedWarningPrefix is prepended to the dropped event count indicator.
const droppedWarningPrefix = "dropped:"

// statusBarInset is the horizontal inset on each side to align with
// panel content above. Derived from: rounded border = 1 char per side.
const statusBarInset = 1

// Model is the status bar rendered at the bottom of the TUI.
type Model struct {
	theme   *theme.Theme
	manager *session.Manager
	width   int

	// Left section
	mode string

	// Center section
	spinner       *Spinner
	spinnerActive bool
	statusText    string

	// Flash overlay
	flash      string
	flashTicks int

	// View ring indicator (pre-formatted by app, empty when no panels collapsed)
	viewRingHint string

	// Right section
	tokens        *TokenDisplay
	droppedEvents atomic.Int64
}

// New creates a status bar Model bound to the given theme and session manager.
func New(t *theme.Theme, mgr *session.Manager) *Model {
	return &Model{
		theme:   t,
		manager: mgr,
		mode:    "CHAT",
		spinner: NewSpinner(),
		tokens:  NewTokenDisplay(t.StatusBar),
	}
}

// Init satisfies tea.Model. The status bar requires no initial command.
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update processes messages and returns the updated model and an optional command.
func (m *Model) Update(raw tea.Msg) (tea.Model, tea.Cmd) {
	switch v := raw.(type) {
	case msg.TickMsg:
		return m.handleTick()
	case msg.SessionEventMsg:
		return m.handleSessionEvent(v)
	case msg.StreamStartMsg:
		return m.handleStreamStart(v)
	case msg.StreamCompleteMsg:
		return m.handleStreamComplete()
	case msg.StreamErrorMsg:
		return m.handleStreamError(v)
	case msg.EventsDroppedMsg:
		return m.handleEventsDropped(v)
	default:
		return m, nil
	}
}

// View renders the status bar as a single styled line with horizontal
// margins matching the panel borders above.
func (m *Model) View() string {
	left := m.renderLeft()
	center := m.renderCenter()
	right := m.renderRight()

	sep := m.theme.StatusBar.Render(separatorChar)

	contentWidth := max(m.width-statusBarInset*2, 1)

	leftSection := left + sep + center
	fixedWidth := lipgloss.Width(leftSection) + lipgloss.Width(sep) + lipgloss.Width(right)
	padWidth := max(contentWidth-fixedWidth, 0)

	padding := m.theme.StatusBar.Render(repeatSpace(padWidth))
	content := leftSection + padding + sep + right

	return lipgloss.NewStyle().
		Width(contentWidth).
		MarginLeft(statusBarInset).
		Render(content)
}

// SetSize updates the available width for the status bar.
// Height is unused because the status bar is always one line.
func (m *Model) SetSize(width, _ int) {
	m.width = width
}

// SetFlash displays a temporary message in the center section.
// The flash auto-clears after flashDurationTicks.
func (m *Model) SetFlash(text string) {
	m.flash = text
	m.flashTicks = flashDurationTicks
}

// SetViewRingHint updates the pre-formatted ring indicator string.
// Pass "" to clear the indicator when all panels are visible.
func (m *Model) SetViewRingHint(hint string) {
	m.viewRingHint = hint
}

// -- Message handlers -------------------------------------------------------

func (m *Model) handleTick() (tea.Model, tea.Cmd) {
	if m.spinnerActive {
		m.spinner.Tick()
	}
	if m.flashTicks > 0 {
		m.flashTicks--
		if m.flashTicks == 0 {
			m.flash = ""
		}
	}
	return m, nil
}

func (m *Model) handleSessionEvent(_ msg.SessionEventMsg) (tea.Model, tea.Cmd) {
	// Session events just trigger a re-render; the active session is read
	// directly from the manager in sessionLabel().
	return m, nil
}

func (m *Model) handleStreamStart(v msg.StreamStartMsg) (tea.Model, tea.Cmd) {
	m.spinnerActive = true
	m.spinner.Reset()
	m.statusText = v.CorrelationID
	return m, nil
}

func (m *Model) handleStreamComplete() (tea.Model, tea.Cmd) {
	m.spinnerActive = false
	m.statusText = ""
	return m, nil
}

func (m *Model) handleStreamError(v msg.StreamErrorMsg) (tea.Model, tea.Cmd) {
	m.spinnerActive = false
	m.statusText = v.Err.Error()
	return m, nil
}

func (m *Model) handleEventsDropped(v msg.EventsDroppedMsg) (tea.Model, tea.Cmd) {
	m.droppedEvents.Add(v.Count)
	return m, nil
}

// -- Section renderers ------------------------------------------------------

func (m *Model) renderLeft() string {
	modeStyle := m.theme.StatusNormal
	modeBadge := modeStyle.Render(m.mode)

	session := m.sessionLabel()
	sessionRendered := m.theme.StatusBar.Render(session)

	return lipgloss.JoinHorizontal(lipgloss.Center, modeBadge, " ", sessionRendered)
}

func (m *Model) renderCenter() string {
	if m.flash != "" {
		return m.theme.StatusNormal.Render(m.flash)
	}
	if m.spinnerActive {
		spinner := m.theme.StatusBar.Render(m.spinner.Current())
		if m.viewRingHint != "" {
			return spinner + " " + m.viewRingHint
		}
		return spinner
	}
	if m.statusText != "" {
		return m.theme.StatusBar.Render(m.statusText)
	}
	if m.viewRingHint != "" {
		return m.viewRingHint
	}
	return m.theme.StatusBar.Render("ready")
}

func (m *Model) renderRight() string {
	parts := []string{m.tokens.View()}

	dropped := m.droppedEvents.Load()
	if dropped > 0 {
		indicator := m.theme.StatusWarning.Render(
			fmt.Sprintf(" %s%d", droppedWarningPrefix, dropped),
		)
		parts = append(parts, indicator)
	}

	return lipgloss.JoinHorizontal(lipgloss.Center, parts...)
}

// -- Helpers ----------------------------------------------------------------

// sessionLabel builds the "session:branch" display string
// by reading the active session directly from the manager.
func (m *Model) sessionLabel() string {
	active, ok := m.manager.GetActive()
	if !ok {
		return "-"
	}

	name := active.Name()
	if name == "" {
		name = "-"
	}

	branch := active.Branch()
	if branch == "" {
		return name
	}

	return name + ":" + branch
}

// repeatSpace returns a string of n spaces. Returns empty for n <= 0.
func repeatSpace(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = ' '
	}
	return string(buf)
}
