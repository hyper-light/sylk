package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/session"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/bridge"
	"github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/editor/register"
	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/editor"
	inputpkg "github.com/adalundhe/sylk/ui/input"
	"github.com/adalundhe/sylk/ui/interrupt"
	knowledgepkg "github.com/adalundhe/sylk/ui/knowledge"
	"github.com/adalundhe/sylk/ui/layout"
	"github.com/adalundhe/sylk/ui/modal"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/search"
	sessionpkg "github.com/adalundhe/sylk/ui/session"
	"github.com/adalundhe/sylk/ui/status"
	"github.com/adalundhe/sylk/ui/theme"
)

// tickInterval is the period between TickMsg emissions.
// Derived from 60fps cursor blink / spinner cadence (~16ms).
const tickInterval = 16 * time.Millisecond

// shutdownGrace is the grace period for goroutine shutdown.
const shutdownGrace = 3 * time.Second

// shutdownHard is the hard deadline for goroutine shutdown.
const shutdownHard = 5 * time.Second

// sourceAgentTUI identifies the TUI as the source agent for guide routing.
const sourceAgentTUI = "tui"

// ---------------------------------------------------------------------------
// Overlay state
// ---------------------------------------------------------------------------

// overlayState tracks which overlay (if any) is currently active.
type overlayState int

const (
	overlayNone   overlayState = iota
	overlayEditor              // Full-screen editor.
	overlayModal               // Modal dialog stack.
	overlaySearch              // Command palette.
)


// ---------------------------------------------------------------------------
// Panel layout
// ---------------------------------------------------------------------------

// defaultPanels defines the initial panel layout with flex-grow weights.
// Center panel receives 2x weight for the chat area.
var defaultPanels = []layout.PanelSpec{
	{ID: component.FocusSessionPanel, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 1, Visible: true},
	{ID: component.FocusChat, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 2, Visible: true},
	{ID: component.FocusCodeViewer, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 1, Visible: true},
}

// defaultTabOrder defines the focus cycling order.
var defaultTabOrder = []component.FocusID{
	component.FocusInput,
	component.FocusChat,
	component.FocusSessionPanel,
	component.FocusAgentPanel,
	component.FocusCodeViewer,
}

// ---------------------------------------------------------------------------
// Dependencies
// ---------------------------------------------------------------------------

// Deps holds all core system references needed by the TUI.
// The caller (cmd.go) is responsible for constructing and providing these.
type Deps struct {
	ActivityBus    *events.ActivityEventBus
	SessionManager *session.Manager
	GuideBus       guide.EventBus
	StreamManager  *guide.StreamManager
	Scope          *concurrency.GoroutineScope
}

// ---------------------------------------------------------------------------
// AppModel
// ---------------------------------------------------------------------------

// AppModel is the root Bubble Tea model that composes all TUI components.
type AppModel struct {
	// Configuration
	config Config

	// Core dependencies
	deps Deps

	// Layout
	layout *layout.Manager
	focus  *layout.FocusManager

	// Panel components
	chat           *chat.Model
	input          *inputpkg.Model
	statusBar      *status.Model
	sessionPanel   *sessionpkg.Model
	agentPanel     *agentpkg.Model
	codePanel      *codepkg.Model
	knowledgePanel *knowledgepkg.Model

	// Overlay components
	editorOverlay *editor.Model
	modalOverlay  *modal.Model
	searchOverlay *search.Model
	overlay       overlayState

	// Bridges
	activityBridge *bridge.ActivityBridge
	sessionBridge  *bridge.SessionBridge
	streamBridge   *bridge.StreamBridge
	guideBridge    *bridge.GuideBridge

	// Interrupt
	interruptHandler *interrupt.Handler

	// Clipboard
	clipboard register.ClipboardProvider

	// State
	chord  chordState
	width  int
	height int
	ready  bool
}

// New creates a root AppModel from configuration and dependencies.
func New(cfg Config, deps Deps) *AppModel {
	th := cfg.Theme()

	app := &AppModel{
		config:           cfg,
		deps:             deps,
		layout:           layout.NewManager(0, 0, defaultPanels),
		focus:            layout.NewFocusManager(defaultTabOrder),
		chat:             chat.New(th, cfg.ChatHistoryCapacity),
		input:            inputpkg.New(th, cfg.InputHistoryCapacity),
		statusBar:        status.New(th, deps.SessionManager),
		sessionPanel:     sessionpkg.New(deps.SessionManager, th),
		agentPanel:       agentpkg.New(th),
		codePanel:        codepkg.New(th),
		knowledgePanel:   knowledgepkg.New(th),
		editorOverlay:    editor.New(th),
		modalOverlay:     modal.New(th),
		searchOverlay:    search.New(th, search.NewProviderRegistry()),
		activityBridge:   bridge.NewActivityBridge("tui.activity", deps.ActivityBus, deps.Scope),
		sessionBridge:    bridge.NewSessionBridge(deps.SessionManager, deps.Scope),
		streamBridge:     bridge.NewStreamBridge(deps.Scope),
		guideBridge:      bridge.NewGuideBridge(deps.GuideBus, deps.Scope),
		interruptHandler: interrupt.NewHandler(),
		clipboard:        register.NewOSClipboard(),
	}
	app.syncFocusState()
	return app
}

// Init starts all event bridges and the tick timer.
func (m *AppModel) Init() tea.Cmd {
	return tea.Batch(
		m.startBridges(),
		m.tickCmd(),
	)
}

// Update dispatches incoming messages to the appropriate handler.
func (m *AppModel) Update(raw tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := raw.(type) {
	case tea.WindowSizeMsg:
		return m, m.handleResize(typed)
	case tea.KeyMsg:
		return m.handleKey(typed)
	case tea.MouseMsg:
		return m, m.handleMouse(typed)
	case msg.SubmitPromptMsg:
		return m, m.handleSubmit(typed)
	case msg.InterruptMsg:
		return m, m.handleInterrupt()
	case msg.QuitConfirmMsg:
		return m, m.handleQuit()
	case msg.TickMsg:
		return m, m.handleTick(typed)
	case msg.FocusPanelMsg:
		return m, m.handleFocusPanel(typed)
	case msg.OpenEditorMsg:
		return m, m.handleOpenEditor(typed)
	case msg.CloseEditorMsg:
		return m, m.handleCloseEditor()
	case msg.GuideResponseMsg:
		return m, m.handleGuideResponse(typed)
	case modal.ModalClosedMsg:
		return m, m.handleModalClosed()
	default:
		return m, m.propagate(raw)
	}
}

// View renders the complete TUI layout.
func (m *AppModel) View() string {
	if !m.ready {
		return ""
	}

	// Full-screen overlay takes over everything.
	if m.overlay == overlayEditor {
		return m.editorOverlay.View()
	}

	// Base layout.
	main := m.renderMainArea()
	inputView := m.input.View()
	statusView := m.statusBar.View()
	base := lipgloss.JoinVertical(lipgloss.Left, main, inputView, statusView)

	// Partial overlays render on top of the base.
	if m.overlay == overlayModal && m.modalOverlay.Active() {
		return m.modalOverlay.View()
	}
	if m.overlay == overlaySearch && m.searchOverlay.Visible() {
		return m.searchOverlay.View()
	}

	return base
}

// Shutdown gracefully stops all bridges and waits for goroutine cleanup.
func (m *AppModel) Shutdown() error {
	m.activityBridge.Stop()
	m.sessionBridge.Stop()
	m.streamBridge.Stop()
	m.guideBridge.Stop()
	return m.deps.Scope.Shutdown(shutdownGrace, shutdownHard)
}

// PushModal adds a modal to the overlay stack and activates the modal overlay.
func (m *AppModel) PushModal(content modal.ModalContent) {
	m.modalOverlay.Push(content)
	m.overlay = overlayModal
}

// ---------------------------------------------------------------------------
// Message handlers
// ---------------------------------------------------------------------------

func (m *AppModel) handleResize(sz tea.WindowSizeMsg) tea.Cmd {
	m.width = sz.Width
	m.height = sz.Height
	m.ready = true

	m.recalcLayout()
	return nil
}

func (m *AppModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C always goes to the interrupt handler.
	if key.String() == "ctrl+c" {
		result := m.interruptHandler.HandleCtrlC()
		return m, func() tea.Msg { return result }
	}

	// Overlay key capture: active overlays consume all keys.
	if m.overlay == overlayEditor {
		comp, cmd := m.editorOverlay.Update(key)
		m.editorOverlay = comp.(*editor.Model)
		return m, cmd
	}
	if m.overlay == overlayModal && m.modalOverlay.Active() {
		comp, cmd := m.modalOverlay.Update(key)
		m.modalOverlay = comp.(*modal.Model)
		return m, cmd
	}
	if m.overlay == overlaySearch && m.searchOverlay.Visible() {
		comp, cmd := m.searchOverlay.Update(key)
		m.searchOverlay = comp.(*search.Model)
		return m, cmd
	}

	// Ctrl+P toggles the search overlay.
	if key.String() == "ctrl+p" {
		m.toggleSearch()
		return m, nil
	}

	// Two-key chord: S then Left/Right (sessions), A then Left/Right (agents).
	if cmd, handled := m.handleChord(key); handled {
		return m, cmd
	}

	// Shift+arrow moves focus spatially between panels.
	if target, ok := spatialFocusTarget(m.focus.Current(), key.String(), m.layout.Mode()); ok {
		m.focus.SetFocus(target)
		m.syncFocusState()
		return m, nil
	}

	// Delegate to focused component.
	return m, m.propagateToFocused(key)
}

func (m *AppModel) handleSubmit(submit msg.SubmitPromptMsg) tea.Cmd {
	// Push a user entry to chat.
	entry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceUser,
		Content:   submit.Text,
		Height:    -1,
	}
	m.chat.PushEntry(entry)

	// Route through Guide bus.
	return m.publishRouteRequest(submit)
}

func (m *AppModel) handleInterrupt() tea.Cmd {
	// First Ctrl+C: clear input or cancel active stream.
	m.streamBridge.Stop()
	return nil
}

func (m *AppModel) handleQuit() tea.Cmd {
	return tea.Quit
}

func (m *AppModel) handleTick(tick msg.TickMsg) tea.Cmd {
	// Forward tick to status bar, input (cursor blink), and chat (highlight).
	m.statusBar.Update(tick)
	comp, _ := m.input.Update(tick)
	m.input = comp.(*inputpkg.Model)
	chatComp, _ := m.chat.Update(tick)
	m.chat = chatComp.(*chat.Model)
	return m.tickCmd()
}

func (m *AppModel) handleFocusPanel(fp msg.FocusPanelMsg) tea.Cmd {
	m.focus.SetFocus(fp.Target)
	m.syncFocusState()
	return nil
}

func (m *AppModel) handleOpenEditor(o msg.OpenEditorMsg) tea.Cmd {
	comp, cmd := m.editorOverlay.Update(o)
	m.editorOverlay = comp.(*editor.Model)
	m.editorOverlay.SetSize(m.width, m.height)
	m.overlay = overlayEditor
	return cmd
}

func (m *AppModel) handleCloseEditor() tea.Cmd {
	comp, cmd := m.editorOverlay.Update(msg.CloseEditorMsg{})
	m.editorOverlay = comp.(*editor.Model)
	m.overlay = overlayNone
	return cmd
}

func (m *AppModel) handleGuideResponse(r msg.GuideResponseMsg) tea.Cmd {
	entry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceAgent,
		AgentID:   r.AgentID,
		Content:   r.Content,
		Height:    -1,
	}
	m.chat.PushEntry(entry)
	return nil
}

func (m *AppModel) handleModalClosed() tea.Cmd {
	if !m.modalOverlay.Active() {
		m.overlay = overlayNone
	}
	return nil
}

// chordState tracks which chord prefix is pending.
type chordState int

const (
	chordNone    chordState = iota
	chordSession            // Alt+S pressed, waiting for arrow.
	chordAgent              // Alt+A pressed, waiting for arrow.
)

// chordArrowDelta maps arrow key strings (including alt-held variants) to
// a cycle direction. The alt+arrow variants handle the common case where the
// user still holds Alt from the prefix key when pressing the arrow.
var chordArrowDelta = map[string]int{
	"left":      -1,
	"right":     1,
	"alt+left":  -1,
	"alt+right": 1,
}

// chordLabel maps active chord state to a display label for the hint overlay.
var chordLabel = map[chordState]string{
	chordSession: "Session select",
	chordAgent:   "Agent select",
}

// chordHint returns a styled hint string when a chord is active, or "" when idle.
func (m *AppModel) chordHint(th *theme.Theme) string {
	label, ok := chordLabel[m.chord]
	if !ok {
		return ""
	}
	labelStyle := lipgloss.NewStyle().Foreground(th.Palette.Secondary).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	return labelStyle.Render(label) + keyStyle.Render("  ←/→ cycle  any key to exit ")
}

// handleChord processes two-key chord shortcuts: Alt+S then Left/Right for sessions,
// Alt+A then Left/Right for agents. The chord stays active while arrows are pressed,
// allowing repeated cycling. Any non-arrow key cancels the chord and falls
// through to normal handling. Returns (cmd, true) if consumed.
func (m *AppModel) handleChord(key tea.KeyMsg) (tea.Cmd, bool) {
	// Chord triggers work from any state, allowing direct switching.
	switch key.String() {
	case "alt+s":
		m.chord = chordSession
		return nil, true
	case "alt+a":
		m.chord = chordAgent
		return nil, true
	}

	if m.chord == chordNone {
		return nil, false
	}

	// Active chord: arrow keys cycle, anything else cancels.
	if delta, ok := chordArrowDelta[key.String()]; ok {
		return m.dispatchChordCycle(m.chord, delta), true
	}
	m.chord = chordNone
	return nil, true
}

// dispatchChordCycle routes a completed chord to the target panel.
func (m *AppModel) dispatchChordCycle(chord chordState, delta int) tea.Cmd {
	switch chord {
	case chordSession:
		if delta < 0 {
			return m.sessionPanel.CyclePrev()
		}
		return m.sessionPanel.CycleNext()
	case chordAgent:
		if delta < 0 {
			m.agentPanel.CyclePrev()
		} else {
			m.agentPanel.CycleNext()
		}
	}
	return nil
}

// handleMouse forwards mouse wheel events to the chat viewport
// and handles click-to-copy on chat messages.
func (m *AppModel) handleMouse(mouse tea.MouseMsg) tea.Cmd {
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		m.chat.ScrollUp()
	case tea.MouseButtonWheelDown:
		m.chat.ScrollDown()
	case tea.MouseButtonLeft:
		if mouse.Action != tea.MouseActionMotion {
			return m.handleChatClick(mouse.X, mouse.Y)
		}
	}
	return nil
}

// handleChatClick copies the content of the chat entry at the clicked
// position to the system clipboard.
func (m *AppModel) handleChatClick(x, y int) tea.Cmd {
	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	if chatW == 0 || chatH == 0 {
		return nil
	}

	chatX := m.chatPanelX()
	innerH := max(chatH-panelBorderSize, 0)

	// Content area within the bordered chat panel.
	contentLeft := chatX + 1
	contentRight := chatX + chatW - 1

	// Chord hint pushes viewport content down by 2 lines when active.
	chordOffset := 0
	if m.chord != chordNone {
		chordOffset = 2
	}
	viewportTop := 1 + chordOffset // 1 = top border
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight {
		return nil
	}
	if y < viewportTop || y >= contentBottom {
		return nil
	}

	viewportLine := y - viewportTop
	target := m.chat.CopyTargetAtViewLine(viewportLine)
	if target == nil {
		return nil
	}

	if err := m.clipboard.Set(target.Content); err != nil {
		m.statusBar.SetFlash("Copy failed")
		return nil
	}
	m.chat.SetHighlight(target.EntryID, target.HighlightStart, target.HighlightEnd)
	m.statusBar.SetFlash("Copied!")
	return nil
}

// chatPanelX returns the X coordinate where the chat panel starts,
// based on the current layout mode.
func (m *AppModel) chatPanelX() int {
	if m.layout.Mode() >= layout.TwoColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		return leftW
	}
	return 0
}

func (m *AppModel) toggleSearch() {
	if m.searchOverlay.Visible() {
		m.searchOverlay.Hide()
		m.overlay = overlayNone
	} else {
		m.searchOverlay.Show()
		m.overlay = overlaySearch
	}
}

// ---------------------------------------------------------------------------
// Message propagation
// ---------------------------------------------------------------------------

// propagate forwards a message to all components and collects commands.
func (m *AppModel) propagate(raw tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	chatComp, chatCmd := m.chat.Update(raw)
	m.chat = chatComp.(*chat.Model)
	cmds = appendCmd(cmds, chatCmd)

	inputComp, inputCmd := m.input.Update(raw)
	m.input = inputComp.(*inputpkg.Model)
	cmds = appendCmd(cmds, inputCmd)

	_, statusCmd := m.statusBar.Update(raw)
	cmds = appendCmd(cmds, statusCmd)

	sessionComp, sessionCmd := m.sessionPanel.Update(raw)
	m.sessionPanel = sessionComp.(*sessionpkg.Model)
	cmds = appendCmd(cmds, sessionCmd)

	agentComp, agentCmd := m.agentPanel.Update(raw)
	m.agentPanel = agentComp.(*agentpkg.Model)
	cmds = appendCmd(cmds, agentCmd)

	codeComp, codeCmd := m.codePanel.Update(raw)
	m.codePanel = codeComp.(*codepkg.Model)
	cmds = appendCmd(cmds, codeCmd)

	knowledgeComp, knowledgeCmd := m.knowledgePanel.Update(raw)
	m.knowledgePanel = knowledgeComp.(*knowledgepkg.Model)
	cmds = appendCmd(cmds, knowledgeCmd)

	return tea.Batch(cmds...)
}

// propagateToFocused sends a key message only to the currently focused component.
func (m *AppModel) propagateToFocused(key tea.KeyMsg) tea.Cmd {
	focused := m.focus.Current()

	switch focused {
	case component.FocusInput:
		prevLines := m.input.LineCount()
		comp, cmd := m.input.Update(key)
		m.input = comp.(*inputpkg.Model)
		if m.input.LineCount() != prevLines {
			m.recalcLayout()
		}
		return cmd
	case component.FocusChat:
		comp, cmd := m.chat.Update(key)
		m.chat = comp.(*chat.Model)
		return cmd
	case component.FocusSessionPanel:
		comp, cmd := m.sessionPanel.Update(key)
		m.sessionPanel = comp.(*sessionpkg.Model)
		return cmd
	case component.FocusAgentPanel:
		comp, cmd := m.agentPanel.Update(key)
		m.agentPanel = comp.(*agentpkg.Model)
		return cmd
	case component.FocusCodeViewer:
		comp, cmd := m.codePanel.Update(key)
		m.codePanel = comp.(*codepkg.Model)
		return cmd
	case component.FocusKnowledge:
		comp, cmd := m.knowledgePanel.Update(key)
		m.knowledgePanel = comp.(*knowledgepkg.Model)
		return cmd
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// statusBarHeight is the fixed height of the status bar (1 line).
const statusBarHeight = 1

// inputBorderSize is the vertical space consumed by the input border.
// Derived from: 1 char top + 1 char bottom = 2.
const inputBorderSize = 2

// inputMinContentLines is the minimum visible content lines in the input.
const inputMinContentLines = 1

// inputMaxContentLines is the maximum visible content lines before the input scrolls.
// Derived from: user requirement of 3 visible lines.
const inputMaxContentLines = 3

// panelBorderSize is the space consumed by a rounded border on each axis.
// Derived from: 1 char per side × 2 sides = 2.
const panelBorderSize = 2

// leftPanelOverhead is the vertical space consumed by section chrome.
// Derived from: 2 headers (1 line each) + 1 divider (1 line + 1 top padding) = 4.
const leftPanelOverhead = 4

// inputHeight returns the current rendered height of the input area
// based on its content line count, clamped to [min, max] + border.
func (m *AppModel) inputHeight() int {
	lines := clampInt(m.input.LineCount(), inputMinContentLines, inputMaxContentLines)
	return lines + inputBorderSize
}

func (m *AppModel) recalcLayout() {
	// Reserve space for input (dynamic) and status bar.
	inputH := m.inputHeight()
	mainHeight := m.height - inputH - statusBarHeight
	mainHeight = max(mainHeight, 1)

	m.layout.SetSize(m.width, mainHeight)

	// Center panel: chat. Subtract border so content fits inside renderPanel().
	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	m.chat.SetSize(max(chatW-panelBorderSize, 1), max(chatH-panelBorderSize, 1))

	// Left panel: split between session (top) and agent (bottom).
	leftW, leftH := m.layout.GetPanelSize(component.FocusSessionPanel)
	innerLeftW := max(leftW-panelBorderSize, 1)
	innerLeftH := max(leftH-panelBorderSize, 1)
	contentH := max(innerLeftH-leftPanelOverhead, 2)
	sessionH := contentH / 2
	agentH := contentH - sessionH
	m.sessionPanel.SetSize(innerLeftW, sessionH)
	m.agentPanel.SetSize(innerLeftW, agentH)

	// Right panel: code viewer (and knowledge, same dimensions).
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.codePanel.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
	m.knowledgePanel.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))

	// Input: dynamic height based on content.
	m.input.SetSize(m.width, inputH)
	m.statusBar.SetSize(m.width, statusBarHeight)

	// Overlays get full terminal dimensions.
	m.editorOverlay.SetSize(m.width, m.height)
	m.modalOverlay.SetSize(m.width, m.height)
	m.searchOverlay.SetSize(m.width, m.height)
}

func (m *AppModel) renderMainArea() string {
	layoutMode := m.layout.Mode()
	th := m.config.Theme()

	chatContent := m.chat.View()
	if hint := m.chordHint(th); hint != "" {
		chatW, _ := m.layout.GetPanelSize(component.FocusChat)
		innerW := max(chatW-panelBorderSize, 1)
		hintWidth := lipgloss.Width(hint)
		pad := max(innerW-hintWidth, 0)
		chatContent = strings.Repeat(" ", pad) + hint + "\n\n" + chatContent
	}
	chatView := m.renderPanel(chatContent, component.FocusChat, th)

	switch layoutMode {
	case layout.ThreeColumn:
		leftView := m.renderLeftPanelBordered(th)
		rightView := m.renderPanel(m.codePanel.View(), component.FocusCodeViewer, th)
		return m.layout.RenderColumns(leftView, chatView, rightView)
	case layout.TwoColumn:
		leftView := m.renderLeftPanelBordered(th)
		return m.layout.RenderColumns(leftView, chatView)
	default:
		return chatView
	}
}

// isLeftPanelFocused returns true when either sub-section of the left panel has focus.
func (m *AppModel) isLeftPanelFocused() bool {
	return m.focus.IsFocused(component.FocusSessionPanel) || m.focus.IsFocused(component.FocusAgentPanel)
}

// renderLeftPanelBordered wraps the left panel content in a border that activates
// when either sessions or agents is focused.
func (m *AppModel) renderLeftPanelBordered(th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(component.FocusSessionPanel)
	border := th.InactiveBorder
	if m.isLeftPanelFocused() {
		border = th.ActiveBorder
	}
	return border.
		Width(max(w-panelBorderSize, 1)).
		Height(max(h-panelBorderSize, 1)).
		MaxHeight(h).
		Render(m.renderLeftPanel(th))
}

// renderLeftPanel stacks sessions and agents with line-extended headers and a divider.
func (m *AppModel) renderLeftPanel(th *theme.Theme) string {
	leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
	innerW := max(leftW-panelBorderSize, 1)

	sessionsFocused := m.focus.IsFocused(component.FocusSessionPanel)
	agentsFocused := m.focus.IsFocused(component.FocusAgentPanel)

	dividerStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)
	divider := lipgloss.NewStyle().PaddingTop(1).Render(
		dividerStyle.Render(strings.Repeat("─", innerW)),
	)

	return strings.Join([]string{
		sectionHeader("Sessions", innerW, sessionsFocused, th),
		m.sessionPanel.View(),
		divider,
		sectionHeader("Agents", innerW, agentsFocused, th),
		m.agentPanel.View(),
	}, "\n")
}

// sectionHeader renders a label followed by a trailing line.
// When focused, the label uses Primary color to indicate the active sub-section.
func sectionHeader(label string, width int, focused bool, th *theme.Theme) string {
	labelColor := th.Palette.Muted
	lineColor := th.Palette.Border
	if focused {
		labelColor = th.Palette.Secondary
	}
	headerStyle := lipgloss.NewStyle().Foreground(labelColor).Bold(true)
	lineStyle := lipgloss.NewStyle().Foreground(lineColor)

	text := headerStyle.Render(" " + label + " ")
	textWidth := lipgloss.Width(text)
	lineWidth := max(width-textWidth, 0)

	return text + lineStyle.Render(strings.Repeat("─", lineWidth))
}

func (m *AppModel) renderPanel(content string, id component.FocusID, th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(id)
	border := th.InactiveBorder
	if m.focus.IsFocused(id) {
		border = th.ActiveBorder
	}
	return border.
		Width(max(w-panelBorderSize, 1)).
		Height(max(h-panelBorderSize, 1)).
		MaxHeight(h).
		Render(content)
}

// ---------------------------------------------------------------------------
// Focus
// ---------------------------------------------------------------------------

func (m *AppModel) syncFocusState() {
	current := m.focus.Current()
	m.chat.SetFocused(current == component.FocusChat)
	m.input.SetFocused(current == component.FocusInput)
	m.sessionPanel.SetFocused(current == component.FocusSessionPanel)
	m.agentPanel.SetFocused(current == component.FocusAgentPanel)
	m.codePanel.SetFocused(current == component.FocusCodeViewer)
}

// focusEdge encodes a (panel, direction) pair for spatial focus lookup.
type focusEdge struct {
	from component.FocusID
	key  string
}

// spatialFocusMap defines the directional adjacency between panels.
// The layout is:
//
//	┌──────────┬───────────┬──────────┐
//	│ Sessions │ Chat      │ Code     │
//	│──────────│           │ Viewer   │
//	│ Agents   │───────────│          │
//	│          │ Input     │          │
//	└──────────┴───────────┴──────────┘
var spatialFocusMap = map[focusEdge]component.FocusID{
	// From Input
	{component.FocusInput, "shift+up"}:    component.FocusChat,
	{component.FocusInput, "shift+left"}:  component.FocusAgentPanel,
	{component.FocusInput, "shift+right"}: component.FocusCodeViewer,

	// From Chat
	{component.FocusChat, "shift+down"}:  component.FocusInput,
	{component.FocusChat, "shift+left"}:  component.FocusSessionPanel,
	{component.FocusChat, "shift+right"}: component.FocusCodeViewer,

	// From Session Panel
	{component.FocusSessionPanel, "shift+right"}: component.FocusChat,
	{component.FocusSessionPanel, "shift+down"}:  component.FocusAgentPanel,

	// From Agent Panel
	{component.FocusAgentPanel, "shift+right"}: component.FocusChat,
	{component.FocusAgentPanel, "shift+up"}:    component.FocusSessionPanel,
	{component.FocusAgentPanel, "shift+down"}:  component.FocusInput,

	// From Code Viewer
	{component.FocusCodeViewer, "shift+left"}: component.FocusChat,
	{component.FocusCodeViewer, "shift+down"}: component.FocusInput,
}

// visiblePanels returns the set of focusable panels for a given layout mode.
func visiblePanels(mode layout.LayoutMode) map[component.FocusID]struct{} {
	// Chat and Input are always visible.
	panels := map[component.FocusID]struct{}{
		component.FocusChat:  {},
		component.FocusInput: {},
	}
	if mode >= layout.TwoColumn {
		panels[component.FocusSessionPanel] = struct{}{}
		panels[component.FocusAgentPanel] = struct{}{}
	}
	if mode >= layout.ThreeColumn {
		panels[component.FocusCodeViewer] = struct{}{}
	}
	return panels
}

// spatialFocusTarget looks up the target panel for a directional shift+arrow key,
// returning false if the source or target panel is not visible in the current layout.
func spatialFocusTarget(current component.FocusID, key string, mode layout.LayoutMode) (component.FocusID, bool) {
	target, ok := spatialFocusMap[focusEdge{current, key}]
	if !ok {
		return 0, false
	}
	visible := visiblePanels(mode)
	if _, srcOk := visible[current]; !srcOk {
		return 0, false
	}
	if _, dstOk := visible[target]; !dstOk {
		return 0, false
	}
	return target, true
}

// ---------------------------------------------------------------------------
// Bridges
// ---------------------------------------------------------------------------

func (m *AppModel) startBridges() tea.Cmd {
	return func() tea.Msg {
		return bridgeReadyMsg{}
	}
}

// bridgeReadyMsg is an internal message signaling that bridges should be started.
type bridgeReadyMsg struct{}

// StartBridges connects all event bridges to the running tea.Program.
// This must be called after tea.NewProgram is created.
func (m *AppModel) StartBridges(program bridge.TeaProgram) error {
	bridges := []bridge.Bridge{
		m.activityBridge,
		m.sessionBridge,
		m.streamBridge,
		m.guideBridge,
	}
	for _, b := range bridges {
		if err := b.Start(program); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Guide integration
// ---------------------------------------------------------------------------

func (m *AppModel) publishRouteRequest(submit msg.SubmitPromptMsg) tea.Cmd {
	req := &guide.RouteRequest{
		CorrelationID: uuid.New().String(),
		Input:         submit.Text,
		SourceAgentID: sourceAgentTUI,
		TargetAgentID: submit.TargetAgent,
		SessionID:     submit.SessionID,
		Timestamp:     time.Now(),
	}

	busMsg := guide.NewRequestMessage("", req)

	return func() tea.Msg {
		err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, busMsg)
		if err != nil {
			return msg.StreamErrorMsg{
				SessionID:     submit.SessionID,
				CorrelationID: req.CorrelationID,
				Err:           err,
			}
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Tick
// ---------------------------------------------------------------------------

func (m *AppModel) tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return msg.TickMsg{Time: t}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// appendCmd appends a non-nil command to the slice.
// clampInt constrains v to [lo, hi].
func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

func appendCmd(cmds []tea.Cmd, cmd tea.Cmd) []tea.Cmd {
	if cmd != nil {
		return append(cmds, cmd)
	}
	return cmds
}

// programAdapter wraps a *tea.Program to satisfy bridge.TeaProgram.
// This adapter exists because bridge.TeaProgram.Send uses `any` to avoid
// importing bubbletea in the bridge package, while tea.Program.Send
// uses the named type tea.Msg.
type programAdapter struct {
	program *tea.Program
}

func (a *programAdapter) Send(m any) {
	a.program.Send(m)
}

// Run creates and runs the Bubble Tea program. This is the main entry point.
func Run(ctx context.Context, cfg Config, deps Deps) error {
	app := New(cfg, deps)

	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)

	adapter := &programAdapter{program: p}

	// Start bridges with the program reference via adapter.
	if err := app.StartBridges(adapter); err != nil {
		return err
	}

	// In mock mode, seed data and start the mock agent.
	if cfg.MockMode {
		seedMockData(deps)
		mock := NewMockAgent(deps.GuideBus, deps.ActivityBus, adapter, deps.Scope)
		if err := mock.Start(); err != nil {
			return err
		}
		defer mock.Stop()
	}

	_, err := p.Run()
	if err != nil {
		return err
	}

	return app.Shutdown()
}
