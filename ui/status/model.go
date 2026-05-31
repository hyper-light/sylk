package status

import (
	"fmt"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/adalundhe/sylk/core/diagnostics"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// separatorChar is the visual delimiter between status bar sections.
const separatorChar = " | "

// flashDuration is how long a flash message persists.
const flashDuration = 2 * time.Second

// spinnerFrameInterval controls spinner frame rate (~10fps).
const spinnerFrameInterval = 100 * time.Millisecond

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
	lastSpin      time.Time
	lastTokenSpin time.Time

	// Persistent prompt (takes priority over flash; does not auto-clear).
	prompt string

	// Flash overlay
	flash      string
	flashUntil time.Time

	// Engaged agent badge (empty when no agent is engaged)
	engagedAgent string

	// View ring indicator (pre-formatted by app, empty when no panels collapsed)
	viewRingHint string

	// Right section
	authIcons     *AuthIconDisplay
	tokens        *TokenDisplay
	progress      *ProgressDisplay
	droppedEvents atomic.Int64

	// View cache: avoids re-rendering when no visible state changed.
	viewCache string
	viewDirty bool
}

// ViewDirty reports whether View() would produce new output.
func (m *Model) ViewDirty() bool { return m.viewDirty }

// New creates a status bar Model bound to the given theme and session manager.
func New(t *theme.Theme, mgr *session.Manager) *Model {
	return &Model{
		theme:     t,
		manager:   mgr,
		mode:      "CHAT",
		spinner:   NewSpinner(),
		authIcons: NewAuthIconDisplay(t.StatusBar, t.StatusNormal, t.StatusError),
		tokens:    NewTokenDisplay(t.StatusBar, t.StatusNormal),
		progress:  NewProgressDisplay(t.StatusNormal, t.StatusBar, t.StatusBar),
	}
}

// SetMode updates the mode badge (e.g. "CHAT", "EDIT").
func (m *Model) SetMode(mode string) {
	m.mode = mode
	m.viewDirty = true
}

// Init satisfies tea.Model. The status bar requires no initial command.
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update processes messages and returns the updated model and an optional command.
func (m *Model) Update(raw tea.Msg) (tea.Model, tea.Cmd) {
	switch v := raw.(type) {
	case msg.DecorTickMsg:
		return m.handleDecorTick(v.Time)
	case msg.SessionEventMsg:
		return m.handleSessionEvent(v)
	case msg.EventsDroppedMsg:
		return m.handleEventsDropped(v)
	case msg.IndexProgressMsg:
		return m.handleIndexProgress(v)
	default:
		return m, nil
	}
}

// View renders the status bar as a single styled line with horizontal
// margins matching the panel borders above.
func (m *Model) View() string {
	if !m.viewDirty && m.viewCache != "" {
		return m.viewCache
	}

	contentWidth := max(m.width-statusBarInset*2, 1)
	right := m.renderRightFitted(contentWidth)
	rightWidth := lipgloss.Width(right)
	sep := m.theme.StatusBar.Render(separatorChar)
	sepWidth := lipgloss.Width(sep)
	left := m.renderLeft()
	center := m.renderCenter()
	leftSection := m.renderLeftSectionFitted(contentWidth, left, center, rightWidth, sepWidth)

	content := right
	if leftSection != "" {
		padWidth := max(contentWidth-lipgloss.Width(leftSection)-sepWidth-rightWidth-m.authIcons.WidthCorrection(), 0)
		padding := m.theme.StatusBar.Render(repeatSpace(padWidth))
		content = leftSection + padding + sep + right
	}

	m.viewCache = lipgloss.NewStyle().
		Width(contentWidth).
		MaxHeight(1).
		MarginLeft(statusBarInset).
		Render(content)
	m.viewDirty = false
	return m.viewCache
}

// SetSize updates the available width for the status bar.
// Height is unused because the status bar is always one line.
func (m *Model) SetSize(width, _ int) {
	m.width = width
	m.viewDirty = true
}

// SetFlash displays a temporary message in the center section.
// The flash auto-clears after flashDurationTicks.
func (m *Model) SetFlash(text string) {
	m.flash = text
	m.flashUntil = time.Now().Add(flashDuration)
	m.viewDirty = true
}

// SetPrompt activates a persistent prompt that does not auto-dismiss.
// It takes priority over flash messages and all other center-section content.
func (m *Model) SetPrompt(text string) {
	m.prompt = text
	m.viewDirty = true
}

// ClearPrompt removes the active prompt.
func (m *Model) ClearPrompt() {
	m.prompt = ""
	m.viewDirty = true
}

// HasPrompt reports whether a persistent prompt is active.
func (m *Model) HasPrompt() bool {
	return m.prompt != ""
}

// SetEngagedAgent updates the engaged agent badge in the status bar.
// Pass "" to clear the badge when no agent is engaged.
func (m *Model) SetEngagedAgent(agentID string) {
	m.engagedAgent = agentID
	m.viewDirty = true
}

// SetViewRingHint updates the pre-formatted ring indicator string.
// Pass "" to clear the indicator when all panels are visible.
func (m *Model) SetViewRingHint(hint string) {
	m.viewRingHint = hint
	m.viewDirty = true
}

// SetTokens updates the cumulative prompt, completion, cache-read, and
// reasoning token counts.
func (m *Model) SetTokens(prompt, completion, cacheRead, reasoning int) {
	m.tokens.Update(prompt, completion, cacheRead, reasoning)
	m.viewDirty = true
}

// SetTokenPhase sets which token counter is actively updating.
func (m *Model) SetTokenPhase(phase TokenPhase) {
	m.tokens.SetPhase(phase)
	m.viewDirty = true
}

// SetAuthStatus updates the auth availability for a provider icon.
func (m *Model) SetAuthStatus(provider string, available bool) {
	m.authIcons.SetAvailable(provider, available)
	m.viewDirty = true
}

// SetNerdFonts toggles Nerd Font glyphs on the auth icons.
func (m *Model) SetNerdFonts(detected bool) {
	m.authIcons.SetNerdFonts(detected)
	m.viewDirty = true
}

// -- Message handlers -------------------------------------------------------

// IsAnimating reports whether any decor-driven animation is active.
// Uses m.flash rather than time-based check to guarantee the clearing
// tick fires even when the flash deadline and the tick align exactly.
func (m *Model) IsAnimating() bool {
	return m.spinnerActive || m.flash != "" || m.tokens.IsAnimating() || m.progress.IsActive() || m.progress.NeedsTick()
}

func (m *Model) handleDecorTick(now time.Time) (tea.Model, tea.Cmd) {
	if m.spinnerActive && (m.lastSpin.IsZero() || now.Sub(m.lastSpin) >= spinnerFrameInterval) {
		m.spinner.Tick()
		m.lastSpin = now
		m.viewDirty = true
	}
	if m.tokens.IsAnimating() && (m.lastTokenSpin.IsZero() || now.Sub(m.lastTokenSpin) >= spinnerFrameInterval) {
		m.tokens.Tick()
		m.lastTokenSpin = now
		m.viewDirty = true
	}
	if m.progress.Tick() {
		m.viewDirty = true
	}
	if !m.flashUntil.IsZero() && !now.Before(m.flashUntil) {
		m.flash = ""
		m.flashUntil = time.Time{}
		m.viewDirty = true
	}
	if m.progress.CheckExpiry(now) {
		m.viewDirty = true
	}
	return m, nil
}

func (m *Model) handleSessionEvent(_ msg.SessionEventMsg) (tea.Model, tea.Cmd) {
	m.viewDirty = true
	return m, nil
}

// StopSpinner deactivates the center spinner and clears status text.
func (m *Model) StopSpinner() {
	if !m.spinnerActive {
		return
	}
	m.spinnerActive = false
	m.statusText = ""
	m.viewDirty = true
}

func (m *Model) handleEventsDropped(v msg.EventsDroppedMsg) (tea.Model, tea.Cmd) {
	m.droppedEvents.Add(v.Count)
	m.viewDirty = true
	return m, nil
}

func (m *Model) handleIndexProgress(v msg.IndexProgressMsg) (tea.Model, tea.Cmd) {
	diagnostics.LogStartup("status_index_progress_msg", "phase", v.Phase, "current", v.Current, "total", v.Total, "done", v.Done)
	if v.Done {
		m.progress.Clear()
	} else {
		m.progress.Update(IndexPhase(v.Phase), v.Current, v.Total)
	}
	m.viewDirty = true
	return m, nil
}

// -- Section renderers ------------------------------------------------------

func (m *Model) renderLeft() string {
	modeStyle := m.theme.StatusNormal
	modeBadge := modeStyle.Width(4).Render(m.mode)

	session := m.sessionLabel()
	sessionRendered := m.theme.StatusBar.Render(session)

	parts := []string{modeBadge, " ", sessionRendered}
	if m.engagedAgent != "" {
		agentBadge := m.theme.StatusNormal.Render("[" + m.engagedAgent + "]")
		parts = append(parts, " ", agentBadge)
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, parts...)
}

func (m *Model) renderCenter() string {
	if m.prompt != "" {
		return m.theme.StatusWarning.Render(m.prompt)
	}
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

func (m *Model) renderLeftSectionFitted(contentWidth int, left, center string, rightWidth, sepWidth int) string {
	available := contentWidth - rightWidth - sepWidth - m.authIcons.WidthCorrection()
	if available <= 0 {
		return ""
	}

	leftWidth := lipgloss.Width(left)
	if leftWidth >= available {
		return ansi.Truncate(left, available, "")
	}

	remaining := available - leftWidth
	if remaining <= 0 {
		return left
	}
	if lipgloss.Width(center) == 0 {
		return left
	}

	if remaining <= sepWidth {
		return left
	}

	centerBudget := remaining - sepWidth
	centerFitted := center
	if lipgloss.Width(center) > centerBudget {
		centerFitted = ansi.Truncate(center, centerBudget, "")
	}
	if lipgloss.Width(centerFitted) == 0 {
		return left
	}

	return left + m.theme.StatusBar.Render(separatorChar) + centerFitted
}

func (m *Model) renderRight() string {
	auth := m.authIcons.View()
	sep := m.theme.StatusBar.Render(separatorChar)
	tokens := m.tokens.View()

	result := auth + sep + tokens

	if pv := m.progress.View(); pv != "" {
		result = pv + sep + result
	}

	dropped := m.droppedEvents.Load()
	if dropped > 0 {
		result += m.theme.StatusWarning.Render(
			fmt.Sprintf(" %s%d", droppedWarningPrefix, dropped),
		)
	}

	return result
}

func (m *Model) renderRightFitted(maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}

	auth := m.authIcons.View()
	sep := m.theme.StatusBar.Render(separatorChar)
	tokens := m.tokens.View()
	progress := m.progress.View()

	dropped := ""
	if count := m.droppedEvents.Load(); count > 0 {
		dropped = m.theme.StatusWarning.Render(
			fmt.Sprintf(" %s%d", droppedWarningPrefix, count),
		)
	}

	candidates := []string{
		m.renderRight(),
		auth + sep + tokens,
		tokens + dropped,
		tokens,
	}
	if progress != "" {
		candidates = append([]string{progress + sep + auth + sep + tokens}, candidates...)
	}

	for _, candidate := range candidates {
		if lipgloss.Width(candidate) <= maxWidth {
			return candidate
		}
	}

	return ansi.Truncate(tokens, maxWidth, "")
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
