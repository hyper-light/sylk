package ui

import (
	"context"
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

	// State
	width  int
	height int
	ready  bool
}

// New creates a root AppModel from configuration and dependencies.
func New(cfg Config, deps Deps) *AppModel {
	th := cfg.Theme()

	return &AppModel{
		config:           cfg,
		deps:             deps,
		layout:           layout.NewManager(0, 0, defaultPanels),
		focus:            layout.NewFocusManager(defaultTabOrder),
		chat:             chat.New(th, cfg.ChatHistoryCapacity),
		input:            inputpkg.New(th, cfg.InputHistoryCapacity),
		statusBar:        status.New(th),
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
	}
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

	// Tab cycles focus.
	if key.String() == "tab" && !m.focus.IsFocused(component.FocusInput) {
		m.focus.Next()
		m.syncFocusState()
		return m, nil
	}

	// Shift+tab reverse-cycles focus.
	if key.String() == "shift+tab" {
		m.focus.Previous()
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
	// Forward tick to status bar.
	m.statusBar.Update(tick)
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
		comp, cmd := m.input.Update(key)
		m.input = comp.(*inputpkg.Model)
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

// inputAreaHeight is the estimated height reserved for the input area.
// Derived from: 1 border top + up to maxHeight lines + 1 border bottom.
const inputAreaMinHeight = 3

func (m *AppModel) recalcLayout() {
	// Reserve space for input and status bar.
	mainHeight := m.height - inputAreaMinHeight - statusBarHeight
	mainHeight = max(mainHeight, 1)

	m.layout.SetSize(m.width, mainHeight)

	// Center panel: chat.
	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	m.chat.SetSize(chatW, chatH)

	// Left panel: split between session (top half) and agent (bottom half).
	leftW, leftH := m.layout.GetPanelSize(component.FocusSessionPanel)
	sessionH := leftH / 2
	agentH := leftH - sessionH
	m.sessionPanel.SetSize(leftW, sessionH)
	m.agentPanel.SetSize(leftW, agentH)

	// Right panel: code viewer (and knowledge, same dimensions).
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.codePanel.SetSize(rightW, rightH)
	m.knowledgePanel.SetSize(rightW, rightH)

	// Fixed-height components.
	m.input.SetSize(m.width, inputAreaMinHeight)
	m.statusBar.SetSize(m.width, statusBarHeight)

	// Overlays get full terminal dimensions.
	m.editorOverlay.SetSize(m.width, m.height)
	m.modalOverlay.SetSize(m.width, m.height)
	m.searchOverlay.SetSize(m.width, m.height)
}

func (m *AppModel) renderMainArea() string {
	layoutMode := m.layout.Mode()
	th := m.config.Theme()

	chatView := m.renderPanel(m.chat.View(), component.FocusChat, th)

	switch layoutMode {
	case layout.ThreeColumn:
		leftView := m.renderPanel(m.renderLeftPanel(), component.FocusSessionPanel, th)
		rightView := m.renderPanel(m.codePanel.View(), component.FocusCodeViewer, th)
		return m.layout.RenderColumns(leftView, chatView, rightView)
	case layout.TwoColumn:
		leftView := m.renderPanel(m.renderLeftPanel(), component.FocusSessionPanel, th)
		return m.layout.RenderColumns(leftView, chatView)
	default:
		return chatView
	}
}

// renderLeftPanel stacks the session panel (top) and agent panel (bottom).
func (m *AppModel) renderLeftPanel() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		m.sessionPanel.View(),
		m.agentPanel.View(),
	)
}

func (m *AppModel) renderPanel(content string, id component.FocusID, th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(id)
	border := th.InactiveBorder
	if m.focus.IsFocused(id) {
		border = th.ActiveBorder
	}
	return border.Width(w - 2).Height(h - 2).Render(content)
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
