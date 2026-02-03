package status

import (
	"fmt"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// separatorChar is the visual delimiter between status bar sections.
const separatorChar = " | "

// droppedWarningPrefix is prepended to the dropped event count indicator.
const droppedWarningPrefix = "dropped:"

// Model is the status bar rendered at the bottom of the TUI.
type Model struct {
	theme *theme.Theme
	width int

	// Left section
	mode          string
	sessionName   string
	sessionBranch string

	// Center section
	spinner       *Spinner
	spinnerActive bool
	statusText    string

	// Right section
	tokens        *TokenDisplay
	droppedEvents atomic.Int64
}

// New creates a status bar Model bound to the given theme.
func New(t *theme.Theme) *Model {
	return &Model{
		theme:   t,
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

// View renders the status bar as a single styled line.
func (m *Model) View() string {
	left := m.renderLeft()
	center := m.renderCenter()
	right := m.renderRight()

	sep := m.theme.StatusBar.Render(separatorChar)

	// Compute the space available for padding between sections.
	leftSection := left + sep + center
	leftWidth := lipgloss.Width(leftSection)
	rightWidth := lipgloss.Width(right)
	padWidth := m.width - leftWidth - rightWidth - lipgloss.Width(sep)

	if padWidth < 0 {
		padWidth = 0
	}

	padding := m.theme.StatusBar.Render(repeatSpace(padWidth))

	return m.theme.StatusBar.
		Width(m.width).
		Render(leftSection + padding + sep + right)
}

// SetSize updates the available width for the status bar.
// Height is unused because the status bar is always one line.
func (m *Model) SetSize(width, _ int) {
	m.width = width
}

// -- Message handlers -------------------------------------------------------

func (m *Model) handleTick() (tea.Model, tea.Cmd) {
	if m.spinnerActive {
		m.spinner.Tick()
	}
	return m, nil
}

func (m *Model) handleSessionEvent(v msg.SessionEventMsg) (tea.Model, tea.Cmd) {
	if v.Event == nil || v.Event.Data == nil {
		return m, nil
	}
	if name, ok := v.Event.Data["name"].(string); ok {
		m.sessionName = name
	}
	if branch, ok := v.Event.Data["branch"].(string); ok {
		m.sessionBranch = branch
	}
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
	if m.spinnerActive {
		return m.theme.StatusBar.Render(m.spinner.Current())
	}
	if m.statusText != "" {
		return m.theme.StatusBar.Render(m.statusText)
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

// sessionLabel builds the "session:branch" display string.
func (m *Model) sessionLabel() string {
	name := m.sessionName
	if name == "" {
		name = "-"
	}

	if m.sessionBranch == "" {
		return name
	}

	return name + ":" + m.sessionBranch
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
