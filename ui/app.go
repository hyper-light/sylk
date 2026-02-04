package ui

import (
	"context"
	"math"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/boot"
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
	"github.com/adalundhe/sylk/ui/filetree"
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

// defaultPanels defines the initial panel layout with flex-grow weights and
// collapse thresholds. CollapseWidth is the minimum allocated width before
// the panel is hidden; breakpoints are derived from flex-grow distribution.
//
// Collapse order: FileTree first, then Code, then Session. Chat never collapses.
var defaultPanels = []layout.PanelSpec{
	{ID: component.FocusSessionPanel, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 1, CollapseWidth: sessionCollapseWidth, Visible: true},
	{ID: component.FocusFileTree, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 1, CollapseWidth: fileTreeCollapseWidth, Visible: true},
	{ID: component.FocusChat, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 2, Visible: true},
	{ID: component.FocusCodeViewer, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 2, CollapseWidth: codeCollapseWidth, Visible: true},
}

// sessionCollapseWidth is derived from: 36 content + 2 border + 2 pad + 4 header.
const sessionCollapseWidth = 44

// fileTreeCollapseWidth is derived from: 30 content + 2 border + 2 pad + 4 indent.
const fileTreeCollapseWidth = 38

// codeCollapseWidth is derived from: 56 content + 4 gutter + 1 sep + 2 border + 3 pad.
// At this width, common code lines (≤56 chars) display without truncation.
const codeCollapseWidth = 66

// defaultModeCandidates defines explicit column assignments per layout mode,
// evaluated from widest to narrowest. The first candidate whose panels all
// meet their CollapseWidth threshold wins.
var defaultModeCandidates = []layout.ModeCandidate{
	{Mode: layout.FourColumn, Columns: []component.FocusID{
		component.FocusSessionPanel, component.FocusFileTree, component.FocusChat, component.FocusCodeViewer,
	}},
	{Mode: layout.ThreeColumn, Columns: []component.FocusID{
		component.FocusSessionPanel, component.FocusChat, component.FocusCodeViewer,
	}},
	{Mode: layout.TwoColumn, Columns: []component.FocusID{
		component.FocusSessionPanel, component.FocusChat,
	}},
	// SingleColumn uses Chat as the anchor so all ring-swapped panels
	// inherit the full terminal width via findSharedColumnDims.
	{Mode: layout.SingleColumn, Columns: []component.FocusID{
		component.FocusChat,
	}},
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
	fileTree       *filetree.Model

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
	lastEscTime      time.Time

	// Clipboard
	clipboard register.ClipboardProvider

	// State
	chord             chordState
	leftRing          viewRing // Left slot cycling ring (Session/FileTree).
	rightRing         viewRing // Right slot cycling ring (Chat/Code).
	collapseHintShown bool     // First-collapse flash shown once per session.
	scrollSpring      harmonica.Spring // Spring simulation for smooth scroll.
	scroll            scrollState     // Current scroll animation state.
	bounceSpring      harmonica.Spring // Underdamped spring for overscroll bounce.
	bounce            bounceState     // Current bounce animation state.
	swipe             swipeState      // Horizontal scroll accumulation for ring cycling.
	width             int
	height            int
	ready             bool
}

// viewRing tracks a cycling list of panels for a swappable layout slot.
// leftRing holds panels sharing a left column; rightRing holds panels sharing a right column.
type viewRing struct {
	panels []component.FocusID
	index  int
}

// current returns the panel that currently occupies the slot.
// Returns 0 (invalid) when the ring is empty.
func (r *viewRing) current() component.FocusID {
	if len(r.panels) == 0 {
		return 0
	}
	return r.panels[r.index]
}

// cycle advances the ring by delta (positive = right, negative = left)
// using wrapping modular arithmetic.
func (r *viewRing) cycle(delta int) {
	n := len(r.panels)
	if n == 0 {
		return
	}
	r.index = ((r.index + delta) % n + n) % n
}

// reset rebuilds the ring with new panels. If the previously active panel
// is present in the new ring, it remains selected; otherwise index resets to 0.
func (r *viewRing) reset(panels []component.FocusID) {
	prev := r.current()
	r.panels = panels
	r.index = 0
	for i, p := range panels {
		if p == prev {
			r.index = i
			return
		}
	}
}

// empty reports whether the ring has no panels to cycle.
func (r *viewRing) empty() bool { return len(r.panels) == 0 }

// scrollState tracks the spring-driven scroll animation.
type scrollState struct {
	pos     float64           // Smooth position (fractional line offset).
	vel     float64           // Current spring velocity.
	target  float64           // Target position (accumulated from wheel events).
	applied int               // Lines actually dispatched to the panel.
	panel   component.FocusID // Which panel is being scrolled.
}

// bounceState tracks the overscroll bounce animation for a single panel.
type bounceState struct {
	pos   float64           // Current visual offset (fractional lines).
	vel   float64           // Current spring velocity.
	panel component.FocusID // Which panel is bouncing.
}

// swipeState tracks horizontal scroll accumulation for ring cycling.
type swipeState struct {
	accum         float64   // Accumulated horizontal ticks (+right, -left).
	stamp         time.Time // Time of last horizontal scroll event.
	cooldownUntil time.Time // Events are ignored until this time (post-cycle dead zone).
}

// New creates a root AppModel from configuration and dependencies.
func New(cfg Config, deps Deps) *AppModel {
	th := cfg.Theme()

	app := &AppModel{
		config:           cfg,
		deps:             deps,
		layout:           layout.NewManager(0, 0, defaultPanels, defaultModeCandidates),
		focus:            layout.NewFocusManager(defaultTabOrder),
		chat:             chat.New(th, cfg.ChatHistoryCapacity),
		input:            inputpkg.New(th, cfg.InputHistoryCapacity),
		statusBar:        status.New(th, deps.SessionManager),
		sessionPanel:     sessionpkg.New(deps.SessionManager, th),
		agentPanel:       agentpkg.New(th),
		codePanel:        codepkg.New(th),
		knowledgePanel:   knowledgepkg.New(th),
		fileTree:         filetree.New(th),
		editorOverlay:    editor.New(th),
		modalOverlay:     modal.New(th),
		searchOverlay:    search.New(th, search.NewProviderRegistry()),
		activityBridge:   bridge.NewActivityBridge("tui.activity", deps.ActivityBus, deps.Scope),
		sessionBridge:    bridge.NewSessionBridge(deps.SessionManager, deps.Scope),
		streamBridge:     bridge.NewStreamBridge(deps.Scope),
		guideBridge:      bridge.NewGuideBridge(deps.GuideBus, deps.Scope),
		interruptHandler: interrupt.NewHandler(),
		clipboard:        register.NewOSClipboard(),
		scrollSpring:     harmonica.NewSpring(harmonica.FPS(scrollFPS), scrollFrequency, scrollDamping),
		bounceSpring:     harmonica.NewSpring(harmonica.FPS(scrollFPS), bounceFrequency, bounceDamping),
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
	case msg.FileOpenMsg:
		return m, m.handleFileOpen(typed)
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

	// Escape triggers agent interrupt with two-press confirmation.
	if key.String() == "esc" {
		now := time.Now()
		if !m.lastEscTime.IsZero() && now.Sub(m.lastEscTime) <= time.Second {
			m.lastEscTime = time.Time{}
			m.streamBridge.Stop()
			m.statusBar.SetFlash("Agent interrupted")
			return m, nil
		}
		m.lastEscTime = now
		m.statusBar.SetFlash("Press Esc again to interrupt agent")
		return m, nil
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
	if target, ok := m.spatialFocusTarget(key.String()); ok {
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
	m.tickScrollMomentum()
	m.tickSwipeDecay()

	// Refresh ring hint so streaming activity badge stays current.
	if !m.leftRing.empty() || !m.rightRing.empty() {
		m.statusBar.SetViewRingHint(m.buildRingHint())
	}

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

// handleFileOpen reads a file from disk and displays it in the code viewer.
// Always marks the file as active in the tree regardless of read success so
// the explorer highlights it immediately.
func (m *AppModel) handleFileOpen(o msg.FileOpenMsg) tea.Cmd {
	m.fileTree.SetActiveFile(o.Path)
	m.fileTree.RevealPath(o.Path)

	data, err := os.ReadFile(o.Path)
	if err != nil {
		m.statusBar.SetFlash("Cannot open: " + o.Name)
		return nil
	}
	m.codePanel.SetContent(string(data), o.Path, o.Language)
	if o.Line > 0 {
		m.codePanel.ScrollToLine(o.Line - 1) // convert 1-based to 0-based
	}
	return nil
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
	chordView               // Alt+V pressed, waiting for arrow.
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

// chordDisplay holds the label and color for a chord hint overlay.
type chordDisplay struct {
	label string
	color func(*theme.Palette) lipgloss.Color
}

// chordDisplays maps chord states to their display properties.
// Session select uses Primary (blue), Agent select uses Success (green).
var chordDisplays = map[chordState]chordDisplay{
	chordSession: {"Session select", func(p *theme.Palette) lipgloss.Color { return p.Primary }},
	chordAgent:   {"Agent select", func(p *theme.Palette) lipgloss.Color { return p.Success }},
	chordView:    {"View select", func(p *theme.Palette) lipgloss.Color { return p.Accent }},
}

// chordHint returns a styled hint string when a chord is active, or "" when idle.
func (m *AppModel) chordHint(th *theme.Theme) string {
	disp, ok := chordDisplays[m.chord]
	if !ok {
		return ""
	}
	labelStyle := lipgloss.NewStyle().Foreground(disp.color(&th.Palette)).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	return labelStyle.Render(disp.label) + keyStyle.Render("  ←/→ cycle  any key to exit ")
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
	case "alt+v":
		m.chord = chordView
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
	case chordView:
		m.cycleViewSlot(delta)
	}
	return nil
}

// cycleViewSlot dispatches Alt+V cycling to the appropriate ring(s).
// Two active rings: both cycle in lockstep so the panel pair toggles as a unit.
// One active ring: direction is forward/backward within that ring.
func (m *AppModel) cycleViewSlot(delta int) {
	bothActive := !m.leftRing.empty() && !m.rightRing.empty()
	if bothActive {
		// Paired cycling: both rings advance together so
		// the two-panel layout toggles as a unit.
		current := m.focus.Current()
		oldLeft := m.leftRing.current()
		oldRight := m.rightRing.current()
		m.leftRing.cycle(delta)
		m.rightRing.cycle(delta)
		switch current {
		case oldLeft:
			m.focus.SetFocus(m.leftRing.current())
		case oldRight:
			m.focus.SetFocus(m.rightRing.current())
		}
		m.syncViewState()
		return
	}
	if !m.leftRing.empty() {
		m.cycleRing(&m.leftRing, delta)
	}
	if !m.rightRing.empty() {
		m.cycleRing(&m.rightRing, delta)
	}
}

// cycleRing advances a single ring by delta, transferring focus if needed.
func (m *AppModel) cycleRing(ring *viewRing, delta int) {
	oldPanel := ring.current()
	ring.cycle(delta)
	newPanel := ring.current()
	if m.focus.Current() == oldPanel {
		m.focus.SetFocus(newPanel)
	}
	m.syncViewState()
}

// ---------------------------------------------------------------------------
// Spring-based scroll
// ---------------------------------------------------------------------------

// scrollFPS is the simulation frame rate for the spring.
// Derived from: tickInterval (16ms) ≈ 60 FPS.
const scrollFPS = 60

// scrollFrequency is the spring's angular frequency (rad/s).
// Higher values produce snappier response; lower values produce smoother easing.
// Derived from: settling in ~8 frames at 60fps ≈ 130ms of visible easing.
const scrollFrequency = 30.0

// scrollDamping is the spring's damping ratio.
// 1.0 = critically damped (fastest approach without overshoot).
// Derived from: critically damped to prevent scroll bounce-back.
const scrollDamping = 1.0

// scrollImpulse is the target displacement per mouse wheel tick.
// Derived from: 3 lines per detent, matching common editor defaults.
const scrollImpulse = 3.0

// scrollKick is the velocity boost per unit of impulse.
// Each wheel event adds impulse * scrollKick to the spring velocity,
// so the first frame crosses an integer boundary immediately.
// Derived from: v₀ = 3 × 15 = 45; with ω=30, B = v₀ + ω(x₀−x_eq) = 45−90 = −45 < 0,
// guaranteeing monotonic approach without overshoot.
const scrollKick = 15.0

// scrollMaxLead is the maximum distance the target can lead the current position.
// Prevents runaway accumulation from rapid scrolling.
// Derived from: comfortable coast of ~1 second ≈ 30 lines.
const scrollMaxLead = 30.0

// scrollSettlePos is the position threshold for considering the spring settled.
// Derived from: less than half a rendered line of residual error.
const scrollSettlePos = 0.01

// scrollSettleVel is the velocity threshold for considering the spring settled.
// Derived from: negligible motion per frame at 60fps.
const scrollSettleVel = 0.01

// ---------------------------------------------------------------------------
// Bounce-back spring (overscroll rubber band)
// ---------------------------------------------------------------------------

// bounceDamping is the bounce spring's damping ratio.
// < 1.0 = underdamped: overshoots equilibrium, producing visible oscillation.
// Derived from: 0.5 produces ~1 visible bounce-back. Low enough for a
// visible elastic effect, high enough that the tail doesn't oscillate
// through integer rounding boundaries (which causes flicker).
const bounceDamping = 0.5

// bounceFrequency is the bounce spring's angular frequency (rad/s).
// Derived from: at 60fps with damping=0.5, frequency=14.5 settles in
// ~22 frames (~375ms). omega_d = 14.5 * sqrt(1 - 0.25) = 12.56 rad/s.
const bounceFrequency = 14.5

// bounceImpulse is the velocity added to the bounce spring per boundary hit.
// Derived from: peak displacement ≈ impulse / omega_d = 6.0 / 14.21 ≈ 0.42
// lines per hit. With 3-5 rapid hits per gesture, accumulates to ~1.3-2.1 lines.
const bounceImpulse = 6.0

// bounceMaxLines is the maximum visual displacement in lines.
// Derived from: 2 lines keeps content visually stable at the boundary while
// still producing a noticeable rubber-band effect.
const bounceMaxLines = 2.0

// bounceMaxVel is the maximum velocity the bounce spring can accumulate.
// Derived from: bounceMaxLines * omega_d = 2.0 * 12.56 ≈ 25.0, ensuring
// peak displacement never exceeds bounceMaxLines.
const bounceMaxVel = 25.0

// bounceSettleThreshold is the combined position+velocity threshold for
// considering the bounce settled.
// Derived from: less than 1% of a rendered line, letting the spring's
// natural exponential decay fully ease the bounce to rest.
const bounceSettleThreshold = 0.01

// ---------------------------------------------------------------------------
// Horizontal swipe (ring cycling)
// ---------------------------------------------------------------------------

// swipeThreshold is the accumulated horizontal scroll ticks required to
// trigger a ring cycle.
// Derived from: 6 ticks requires a deliberate gesture (trackpads emit
// 10-20 events per swipe), preventing accidental triggers.
const swipeThreshold = 6.0

// swipeDecay is the duration after which stale swipe accumulation resets.
// Derived from: 300ms is long enough for a continuous trackpad gesture
// but short enough that separate flicks don't combine.
const swipeDecay = 300 * time.Millisecond

// swipeCooldown is the dead period after a cycle fires during which
// further swipe events are ignored. Prevents the tail end of a single
// scroll gesture from immediately triggering a second cycle.
// Derived from: 500ms exceeds a typical trackpad gesture duration (~300ms)
// while feeling responsive for intentional repeated swipes.
const swipeCooldown = 500 * time.Millisecond

// handleMouse routes mouse wheel events to the panel under the cursor
// using spring-based scrolling, handles click-to-copy on chat messages,
// and handles text selection in the code panel.
// Mouse events are consumed when a full-screen overlay is active.
func (m *AppModel) handleMouse(mouse tea.MouseMsg) tea.Cmd {
	if m.overlay == overlaySearch && m.searchOverlay.Visible() {
		switch mouse.Button {
		case tea.MouseButtonWheelUp:
			m.searchOverlay.ScrollUp()
		case tea.MouseButtonWheelDown:
			m.searchOverlay.ScrollDown()
		}
		return nil
	}
	if m.overlay == overlayModal {
		return nil
	}

	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		if mouse.Alt {
			m.applySwipeImpulse(mouse.X, -1)
		} else {
			m.applyScrollImpulse(mouse.X, -scrollImpulse)
		}
	case tea.MouseButtonWheelDown:
		if mouse.Alt {
			m.applySwipeImpulse(mouse.X, 1)
		} else {
			m.applyScrollImpulse(mouse.X, scrollImpulse)
		}
	case tea.MouseButtonWheelLeft:
		m.applySwipeImpulse(mouse.X, -1)
	case tea.MouseButtonWheelRight:
		m.applySwipeImpulse(mouse.X, 1)
	case tea.MouseButtonLeft:
		return m.handleLeftClick(mouse)
	}
	return nil
}

// handleLeftClick dispatches left-button press events to the file tree
// and chat panels. Code panel uses native terminal selection and system
// copy/paste shortcuts (Ctrl+Shift+C/V or Cmd+C/V).
func (m *AppModel) handleLeftClick(mouse tea.MouseMsg) tea.Cmd {
	if mouse.Action != tea.MouseActionPress {
		return nil
	}
	if cmd := m.handleFileTreeClick(mouse.X, mouse.Y); cmd != nil {
		return cmd
	}
	return m.handleChatClick(mouse.X, mouse.Y)
}

// panelForScroll resolves the panel under screen coordinate x, accounting
// for ring-swapped panels that PanelAtX doesn't know about.
// In SingleColumn mode PanelAtX has no candidates and always returns false,
// so we fall back to the active leftRing panel. In TwoColumn/ThreeColumn
// modes the fixed candidate IDs are mapped to the current ring occupant.
func (m *AppModel) panelForScroll(x int) (component.FocusID, bool) {
	mode := m.layout.Mode()

	if mode == layout.SingleColumn {
		if m.leftRing.empty() {
			return 0, false
		}
		return m.leftRing.current(), true
	}

	panelID, ok := m.layout.PanelAtX(x)
	if !ok {
		return 0, false
	}

	return m.resolveRingPanel(panelID, mode), true
}

// resolveRingPanel maps a fixed candidate panel ID to the actual panel
// currently showing in that slot due to ring cycling.
func (m *AppModel) resolveRingPanel(id component.FocusID, mode layout.LayoutMode) component.FocusID {
	switch mode {
	case layout.ThreeColumn:
		if id == component.FocusSessionPanel && !m.leftRing.empty() {
			return m.leftRing.current()
		}
	case layout.TwoColumn:
		if id == component.FocusSessionPanel && !m.leftRing.empty() {
			return m.leftRing.current()
		}
		if id == component.FocusChat && !m.rightRing.empty() {
			return m.rightRing.current()
		}
	}
	return id
}

// applyScrollImpulse adds displacement and velocity to the spring for the
// panel at screen coordinate x. The velocity kick ensures the first frame
// crosses an integer line boundary, giving immediate visual feedback.
// Switching panels resets the spring to avoid cross-panel drift.
func (m *AppModel) applyScrollImpulse(x int, impulse float64) {
	panelID, ok := m.panelForScroll(x)
	if !ok {
		return
	}
	if panelID != m.scroll.panel {
		m.scroll = scrollState{panel: panelID}
		m.bounce = bounceState{panel: panelID}
	}
	m.scroll.target += impulse
	m.scroll.vel += impulse * scrollKick

	// Cap target lead to prevent runaway coast.
	m.scroll.target = clampFloat(m.scroll.target,
		m.scroll.pos-scrollMaxLead,
		m.scroll.pos+scrollMaxLead,
	)
	// Cap velocity to prevent overshoot (B ≤ 0 condition for critically damped).
	maxVel := scrollFrequency * math.Abs(m.scroll.target-m.scroll.pos)
	m.scroll.vel = clampFloat(m.scroll.vel, -maxVel, maxVel)
}

// tickScrollMomentum advances the scroll spring by one frame and applies
// resulting scroll lines. Boundary hits feed the bounce spring.
func (m *AppModel) tickScrollMomentum() {
	s := &m.scroll
	if !s.settled() {
		s.pos, s.vel = m.scrollSpring.Update(s.pos, s.vel, s.target)

		// Snap when close enough to prevent asymptotic crawl.
		if s.settled() {
			s.pos = s.target
			s.vel = 0
		}

		newApplied := int(math.Round(s.pos))
		m.applyScrollDelta(s.panel, newApplied-s.applied)
		s.applied = newApplied
	}

	m.tickBounce()

	// Push bounce offset to panels for rendering.
	m.chat.SetBounceOffset(m.bounceOffset(component.FocusChat))
	m.codePanel.SetBounceOffset(m.bounceOffset(component.FocusCodeViewer))
	m.fileTree.SetBounceOffset(m.bounceOffset(component.FocusFileTree))
}

// settled reports whether the spring is close enough to target to stop.
func (s *scrollState) settled() bool {
	return math.Abs(s.pos-s.target) < scrollSettlePos &&
		math.Abs(s.vel) < scrollSettleVel
}

// applyScrollDelta dispatches individual line scrolls. When a boundary is
// hit, the remaining delta is converted into bounce impulse.
func (m *AppModel) applyScrollDelta(panelID component.FocusID, delta int) {
	direction := 1
	if delta < 0 {
		direction = -1
		delta = -delta
	}
	for range delta {
		if !m.scrollOneLine(panelID, direction) {
			m.applyBounceImpulse(panelID, direction)
		}
	}
}

// scrollOneLine scrolls the identified panel by one line in the given direction.
// Returns true if the scroll was consumed, false if the panel hit a boundary.
func (m *AppModel) scrollOneLine(panelID component.FocusID, direction int) bool {
	switch panelID {
	case component.FocusChat:
		if direction < 0 {
			return m.chat.ScrollUp()
		}
		return m.chat.ScrollDown()
	case component.FocusCodeViewer:
		if direction < 0 {
			return m.codePanel.ScrollUp()
		}
		return m.codePanel.ScrollDown()
	case component.FocusFileTree:
		if direction < 0 {
			return m.fileTree.ScrollUp()
		}
		return m.fileTree.ScrollDown()
	case component.FocusSessionPanel:
		// Left column contains both session and agent panels; scroll goes to agent
		// only when it has scrollable content (expanded/detail views).
		if direction < 0 {
			return m.agentPanel.ScrollUp()
		}
		return m.agentPanel.ScrollDown()
	}
	return true
}

// applyBounceImpulse adds velocity to the bounce spring when a scroll
// boundary is hit. Switching panels resets the bounce state.
func (m *AppModel) applyBounceImpulse(panelID component.FocusID, direction int) {
	if panelID != m.bounce.panel {
		m.bounce = bounceState{panel: panelID}
	}
	m.bounce.vel += float64(direction) * bounceImpulse
	m.bounce.vel = clampFloat(m.bounce.vel, -bounceMaxVel, bounceMaxVel)
}

// tickBounce advances the bounce spring by one frame toward equilibrium (pos=0).
// The underdamped spring naturally oscillates past 0, creating the bounce-back.
func (m *AppModel) tickBounce() {
	b := &m.bounce
	if m.bounceSettled() {
		b.pos = 0
		b.vel = 0
		return
	}

	b.pos, b.vel = m.bounceSpring.Update(b.pos, b.vel, 0)
	b.pos = clampFloat(b.pos, -bounceMaxLines, bounceMaxLines)

	if m.bounceSettled() {
		b.pos = 0
		b.vel = 0
	}
}

// bounceSettled reports whether the bounce spring is close enough to
// equilibrium (pos=0, vel=0) to stop.
func (m *AppModel) bounceSettled() bool {
	return math.Abs(m.bounce.pos) < bounceSettleThreshold &&
		math.Abs(m.bounce.vel) < bounceSettleThreshold
}

// bounceOffset returns the integer line displacement for the given panel.
// Positive = content shifted up (bouncing off bottom boundary).
// Negative = content shifted down (bouncing off top boundary).
func (m *AppModel) bounceOffset(panelID component.FocusID) int {
	if m.bounce.panel != panelID {
		return 0
	}
	return int(math.Round(m.bounce.pos))
}

// clampFloat constrains v to [lo, hi].
func clampFloat(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(v, hi))
}

// ---------------------------------------------------------------------------
// Horizontal swipe (ring cycling via mouse wheel)
// ---------------------------------------------------------------------------

// applySwipeImpulse accumulates horizontal scroll delta and triggers a
// view slot cycle (identical to Alt+V chord) when the accumulated magnitude
// reaches swipeThreshold. In TwoColumn mode both rings cycle in lockstep;
// in other modes the single active ring cycles.
func (m *AppModel) applySwipeImpulse(x int, delta float64) {
	now := time.Now()
	if now.Before(m.swipe.cooldownUntil) {
		return
	}
	if m.leftRing.empty() && m.rightRing.empty() {
		return
	}
	m.swipe.accum += delta
	m.swipe.stamp = now
	if math.Abs(m.swipe.accum) >= swipeThreshold {
		direction := 1
		if m.swipe.accum < 0 {
			direction = -1
		}
		m.cycleViewSlot(direction)
		m.swipe.accum = 0
		m.swipe.cooldownUntil = now.Add(swipeCooldown)
	}
}

// tickSwipeDecay resets stale swipe accumulation after swipeDecay elapses
// since the last horizontal scroll event.
func (m *AppModel) tickSwipeDecay() {
	if m.swipe.accum != 0 && time.Since(m.swipe.stamp) > swipeDecay {
		m.swipe.accum = 0
	}
}

// isFileTreeVisible reports whether the FileTree panel is currently rendered on screen.
func (m *AppModel) isFileTreeVisible() bool {
	if m.layout.Mode() == layout.FourColumn {
		return true
	}
	return m.leftRing.current() == component.FocusFileTree
}

// fileTreePanelX returns the screen X offset of the file tree panel's left edge.
func (m *AppModel) fileTreePanelX() int {
	if m.layout.Mode() == layout.FourColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		return leftW
	}
	return 0
}

// handleFileTreeClick dispatches a click inside the file tree panel, activating
// the entry or search result at the clicked row.
func (m *AppModel) handleFileTreeClick(x, y int) tea.Cmd {
	if !m.isFileTreeVisible() {
		return nil
	}
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	if treeW == 0 || treeH == 0 {
		return nil
	}

	treeX := m.fileTreePanelX()
	innerH := max(treeH-panelBorderSize, 0)

	contentLeft := treeX + 1
	contentRight := treeX + treeW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight {
		return nil
	}
	if y < contentTop || y >= contentBottom {
		return nil
	}

	viewX := x - contentLeft
	viewY := y - contentTop
	return m.fileTree.ClickAt(viewX, viewY)
}

// isChatVisible reports whether the Chat panel is currently rendered on screen.
func (m *AppModel) isChatVisible() bool {
	mode := m.layout.Mode()
	if mode >= layout.ThreeColumn {
		return true
	}
	if mode == layout.TwoColumn {
		return m.rightRing.current() == component.FocusChat
	}
	return m.leftRing.current() == component.FocusChat
}

// handleChatClick copies the content of the chat entry at the clicked
// position to the system clipboard.
func (m *AppModel) handleChatClick(x, y int) tea.Cmd {
	if !m.isChatVisible() {
		return nil
	}
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
	m.chat.SetHighlight(target.EntryID, target.EntryIndex, target.HighlightStart, target.HighlightEnd)
	m.statusBar.SetFlash("Copied!")
	return nil
}

// chatPanelX returns the X coordinate where the chat panel starts,
// based on the current layout mode.
func (m *AppModel) chatPanelX() int {
	mode := m.layout.Mode()
	if mode == layout.FourColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		treeW, _ := m.layout.GetPanelSize(component.FocusFileTree)
		return leftW + treeW
	}
	if mode >= layout.TwoColumn {
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
		// Kill residual scroll/bounce momentum so it doesn't affect the
		// overlay or resume unexpectedly when the overlay closes.
		m.scroll = scrollState{}
		m.bounce = bounceState{}
		m.chat.SetBounceOffset(0)
		m.codePanel.SetBounceOffset(0)
		m.fileTree.SetBounceOffset(0)
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

	treeComp, treeCmd := m.fileTree.Update(raw)
	m.fileTree = treeComp.(*filetree.Model)
	cmds = appendCmd(cmds, treeCmd)

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
	case component.FocusFileTree:
		comp, cmd := m.fileTree.Update(key)
		m.fileTree = comp.(*filetree.Model)
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

	// Sync tab order and focus to the current layout mode so collapsed
	// panels are excluded from keyboard navigation.
	m.syncViewState()

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

	// File tree panel.
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	m.fileTree.SetSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))

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

// syncViewState rebuilds the dual view cycling rings for the current layout
// mode, updates the focus tab order to match visible panels, and pushes the
// ring indicator to the status bar. Called on layout recompute and after cycling.
func (m *AppModel) syncViewState() {
	mode := m.layout.Mode()
	wasEmpty := m.leftRing.empty() && m.rightRing.empty()

	// Rebuild rings per mode (see plan table).
	switch mode {
	case layout.FourColumn:
		m.leftRing.reset(nil)
		m.rightRing.reset(nil)
	case layout.ThreeColumn:
		m.leftRing.reset([]component.FocusID{
			component.FocusSessionPanel,
			component.FocusFileTree,
		})
		m.rightRing.reset(nil)
	case layout.TwoColumn:
		m.leftRing.reset([]component.FocusID{
			component.FocusSessionPanel,
			component.FocusFileTree,
		})
		m.rightRing.reset([]component.FocusID{
			component.FocusChat,
			component.FocusCodeViewer,
		})
	default:
		m.leftRing.reset([]component.FocusID{
			component.FocusSessionPanel,
			component.FocusChat,
			component.FocusFileTree,
			component.FocusCodeViewer,
		})
		m.rightRing.reset(nil)
	}

	// First-collapse flash: show once when panels first collapse.
	nowActive := !m.leftRing.empty() || !m.rightRing.empty()
	if wasEmpty && nowActive && !m.collapseHintShown {
		m.statusBar.SetFlash("Panel collapsed \u2014 Alt+V \u2190/\u2192 to cycle views")
		m.collapseHintShown = true
	}

	// Update tab order based on mode + which panels the rings are showing.
	order := m.tabOrderForView(mode)
	m.focus.SetTabOrder(order)
	m.syncFocusState()

	// Push ring indicator to the status bar.
	m.statusBar.SetViewRingHint(m.buildRingHint())
}

// tabOrderForView returns the focus cycling order for the current mode and
// ring state. Ring-active panels replace fixed panels in the order.
func (m *AppModel) tabOrderForView(mode layout.LayoutMode) []component.FocusID {
	switch mode {
	case layout.FourColumn:
		return []component.FocusID{
			component.FocusInput,
			component.FocusChat,
			component.FocusSessionPanel,
			component.FocusAgentPanel,
			component.FocusFileTree,
			component.FocusCodeViewer,
		}
	case layout.ThreeColumn:
		left := m.leftRing.current()
		order := []component.FocusID{
			component.FocusInput,
			component.FocusChat,
			left,
		}
		if left == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		order = append(order, component.FocusCodeViewer)
		return order
	case layout.TwoColumn:
		left := m.leftRing.current()
		right := m.rightRing.current()
		order := []component.FocusID{
			component.FocusInput,
			right,
			left,
		}
		if left == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		return order
	default:
		// SingleColumn: Input + whatever the left ring shows.
		active := m.leftRing.current()
		order := []component.FocusID{component.FocusInput, active}
		if active == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		return order
	}
}

// panelDisplayNames maps panel IDs to short labels for the status bar ring.
var panelDisplayNames = map[component.FocusID]string{
	component.FocusChat:         "Chat",
	component.FocusCodeViewer:   "Code",
	component.FocusSessionPanel: "Sess",
	component.FocusFileTree:     "Tree",
}

// buildRingHint returns the formatted ring indicator string for the status bar.
// Returns "" when both rings are empty (FourColumn).
// When both rings are active, shows: ◀ Sess ● Tree ○ | Chat ● Code ○ ▶
func (m *AppModel) buildRingHint() string {
	if m.leftRing.empty() && m.rightRing.empty() {
		return ""
	}
	th := m.config.Theme()
	currentStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	activeStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)

	renderRing := func(ring *viewRing) []string {
		var items []string
		for i, pid := range ring.panels {
			name := panelDisplayNames[pid]
			var indicator string
			switch {
			case i == ring.index:
				indicator = currentStyle.Render(name + " \u25cf")
			case pid == component.FocusChat && m.chat.IsStreaming():
				indicator = activeStyle.Render(name + " \u2731")
			default:
				indicator = dimStyle.Render(name + " \u25cb")
			}
			items = append(items, indicator)
		}
		return items
	}

	var parts []string
	parts = append(parts, dimStyle.Render("\u25c0"))

	if !m.leftRing.empty() {
		parts = append(parts, renderRing(&m.leftRing)...)
	}

	if !m.leftRing.empty() && !m.rightRing.empty() {
		parts = append(parts, dimStyle.Render("|"))
	}

	if !m.rightRing.empty() {
		parts = append(parts, renderRing(&m.rightRing)...)
	}

	parts = append(parts, dimStyle.Render("\u25b6"))
	return strings.Join(parts, " ")
}

func (m *AppModel) renderMainArea() string {
	layoutMode := m.layout.Mode()
	th := m.config.Theme()

	switch layoutMode {
	case layout.FourColumn:
		leftView := m.renderLeftPanelBordered(th)
		treeView := m.renderPanel(m.fileTree.View(), component.FocusFileTree, th)
		chatView := m.renderPanel(
			m.overlayChordHint(m.chat.View(), component.FocusChat, th),
			component.FocusChat, th)
		codeView := m.renderPanel(m.codePanel.View(), component.FocusCodeViewer, th)
		return m.layout.RenderColumns(leftView, treeView, chatView, codeView)

	case layout.ThreeColumn:
		left := m.leftRing.current()
		leftView := m.renderLeftSlot(left, th)
		chatView := m.renderPanel(
			m.overlayChordHint(m.chat.View(), component.FocusChat, th),
			component.FocusChat, th)
		codeView := m.renderPanel(m.codePanel.View(), component.FocusCodeViewer, th)
		return m.layout.RenderColumns(leftView, chatView, codeView)

	case layout.TwoColumn:
		left := m.leftRing.current()
		right := m.rightRing.current()
		leftView := m.renderLeftSlot(left, th)
		content := m.overlayChordHint(m.panelContent(right), right, th)
		rightView := m.renderPanel(content, right, th)
		return m.layout.RenderColumns(leftView, rightView)

	default:
		active := m.leftRing.current()
		if active == component.FocusSessionPanel {
			content := m.overlayChordHint(m.renderLeftPanel(th), active, th)
			return m.borderLeftPanel(content, th)
		}
		if active == component.FocusFileTree {
			content := m.overlayChordHint(m.fileTree.View(), active, th)
			return m.renderPanel(content, active, th)
		}
		content := m.overlayChordHint(m.panelContent(active), active, th)
		return m.renderPanel(content, active, th)
	}
}

// renderLeftSlot renders the left column for the given panel ID.
// Session gets the composite left panel with border; FileTree gets a standard panel.
func (m *AppModel) renderLeftSlot(id component.FocusID, th *theme.Theme) string {
	if id == component.FocusFileTree {
		return m.renderPanel(m.fileTree.View(), component.FocusFileTree, th)
	}
	return m.renderLeftPanelBordered(th)
}

// panelContent returns the raw view content for a swappable panel.
func (m *AppModel) panelContent(id component.FocusID) string {
	switch id {
	case component.FocusChat:
		return m.chat.View()
	case component.FocusCodeViewer:
		return m.codePanel.View()
	case component.FocusFileTree:
		return m.fileTree.View()
	default:
		return ""
	}
}

// overlayChordHint prepends the chord hint bar to content when a chord is active.
func (m *AppModel) overlayChordHint(content string, panelID component.FocusID, th *theme.Theme) string {
	hint := m.chordHint(th)
	if hint == "" {
		return content
	}
	w, _ := m.layout.GetPanelSize(panelID)
	innerW := max(w-panelBorderSize, 1)
	hintWidth := lipgloss.Width(hint)
	pad := max(innerW-hintWidth, 0)
	divider := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", innerW))
	return strings.Repeat(" ", pad) + hint + "\n" + divider + "\n" + content
}

// isLeftPanelFocused returns true when either sub-section of the left panel has focus.
func (m *AppModel) isLeftPanelFocused() bool {
	return m.focus.IsFocused(component.FocusSessionPanel) || m.focus.IsFocused(component.FocusAgentPanel)
}

// renderLeftPanelBordered wraps the left panel content in a border that activates
// when either sessions or agents is focused.
func (m *AppModel) renderLeftPanelBordered(th *theme.Theme) string {
	return m.borderLeftPanel(m.renderLeftPanel(th), th)
}

// borderLeftPanel wraps arbitrary content in the left panel's border frame.
func (m *AppModel) borderLeftPanel(content string, th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(component.FocusSessionPanel)
	border := th.InactiveBorder
	if m.isLeftPanelFocused() {
		border = th.ActiveBorder
	}
	return border.
		Width(max(w-panelBorderSize, 1)).
		Height(max(h-panelBorderSize, 1)).
		MaxHeight(h).
		Render(content)
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
	m.fileTree.SetFocused(current == component.FocusFileTree)
}

// focusEdge encodes a (panel, direction) pair for spatial focus lookup.
type focusEdge struct {
	from component.FocusID
	key  string
}

// leftSlotSentinel is a placeholder FocusID in spatial adjacency maps
// representing whichever panel currently occupies the left ring's active slot.
const leftSlotSentinel component.FocusID = -1

// rightSlotSentinel is a placeholder FocusID in spatial adjacency maps
// representing whichever panel currently occupies the right ring's active slot.
const rightSlotSentinel component.FocusID = -2

// slotMapping pairs a sentinel with the actual panel it represents.
type slotMapping struct {
	sentinel component.FocusID
	actual   component.FocusID
}

// fourColumnSpatialMap defines panel adjacency when all 4 columns are visible.
//
//	┌──────────┬──────────┬───────────┬──────────┐
//	│ Sessions │ FileTree │ Chat      │ Code     │
//	│──────────│          │           │ Viewer   │
//	│ Agents   │          │───────────│          │
//	│          │          │ Input     │          │
//	└──────────┴──────────┴───────────┴──────────┘
var fourColumnSpatialMap = map[focusEdge]component.FocusID{
	{component.FocusInput, "shift+up"}:    component.FocusChat,
	{component.FocusInput, "shift+left"}:  component.FocusAgentPanel,
	{component.FocusInput, "shift+right"}: component.FocusCodeViewer,

	{component.FocusChat, "shift+down"}:  component.FocusInput,
	{component.FocusChat, "shift+left"}:  component.FocusFileTree,
	{component.FocusChat, "shift+right"}: component.FocusCodeViewer,

	{component.FocusFileTree, "shift+left"}:  component.FocusSessionPanel,
	{component.FocusFileTree, "shift+right"}: component.FocusChat,
	{component.FocusFileTree, "shift+down"}:  component.FocusInput,

	{component.FocusSessionPanel, "shift+right"}: component.FocusFileTree,
	{component.FocusSessionPanel, "shift+down"}:  component.FocusAgentPanel,

	{component.FocusAgentPanel, "shift+right"}: component.FocusFileTree,
	{component.FocusAgentPanel, "shift+up"}:    component.FocusSessionPanel,
	{component.FocusAgentPanel, "shift+down"}:  component.FocusInput,

	{component.FocusCodeViewer, "shift+left"}: component.FocusChat,
	{component.FocusCodeViewer, "shift+down"}: component.FocusInput,
}

// threeColumnSpatialMap defines adjacency with left column cycling
// (Session/FileTree via leftSlotSentinel), Chat in center, Code on right.
var threeColumnSpatialMap = map[focusEdge]component.FocusID{
	{component.FocusInput, "shift+up"}:    component.FocusChat,
	{component.FocusInput, "shift+left"}:  leftSlotSentinel,
	{component.FocusInput, "shift+right"}: component.FocusCodeViewer,

	{component.FocusChat, "shift+down"}:  component.FocusInput,
	{component.FocusChat, "shift+left"}:  leftSlotSentinel,
	{component.FocusChat, "shift+right"}: component.FocusCodeViewer,

	{leftSlotSentinel, "shift+right"}: component.FocusChat,
	{leftSlotSentinel, "shift+down"}:  component.FocusInput,

	// Session composite: Agent is below Session in the left column.
	{component.FocusSessionPanel, "shift+down"}: component.FocusAgentPanel,
	{component.FocusAgentPanel, "shift+up"}:     component.FocusSessionPanel,
	{component.FocusAgentPanel, "shift+right"}:  component.FocusChat,
	{component.FocusAgentPanel, "shift+down"}:   component.FocusInput,

	{component.FocusCodeViewer, "shift+left"}: component.FocusChat,
	{component.FocusCodeViewer, "shift+down"}: component.FocusInput,
}

// twoColumnSpatialMap defines adjacency with left column cycling
// (leftSlotSentinel) and right column cycling (rightSlotSentinel).
var twoColumnSpatialMap = map[focusEdge]component.FocusID{
	{component.FocusInput, "shift+up"}:   rightSlotSentinel,
	{component.FocusInput, "shift+left"}: leftSlotSentinel,

	{rightSlotSentinel, "shift+down"}: component.FocusInput,
	{rightSlotSentinel, "shift+left"}: leftSlotSentinel,

	{leftSlotSentinel, "shift+right"}: rightSlotSentinel,
	{leftSlotSentinel, "shift+down"}:  component.FocusInput,

	// Session composite sub-navigation.
	{component.FocusSessionPanel, "shift+down"}: component.FocusAgentPanel,
	{component.FocusAgentPanel, "shift+up"}:     component.FocusSessionPanel,
	{component.FocusAgentPanel, "shift+right"}:  rightSlotSentinel,
	{component.FocusAgentPanel, "shift+down"}:   component.FocusInput,
}

// singleColumnSpatialMap defines adjacency when a single swappable panel
// (Chat, Code, or FileTree) is stacked above Input.
var singleColumnSpatialMap = map[focusEdge]component.FocusID{
	{component.FocusInput, "shift+up"}: leftSlotSentinel,
	{leftSlotSentinel, "shift+down"}:   component.FocusInput,
}

// singleColumnSessionMap defines adjacency when the Session composite
// (Session + Agent) fills the single column above Input.
var singleColumnSessionMap = map[focusEdge]component.FocusID{
	{component.FocusInput, "shift+up"}:          component.FocusSessionPanel,
	{component.FocusSessionPanel, "shift+down"}: component.FocusAgentPanel,
	{component.FocusAgentPanel, "shift+up"}:     component.FocusSessionPanel,
	{component.FocusAgentPanel, "shift+down"}:   component.FocusInput,
}

// spatialFocusTarget resolves a shift+arrow key to the panel it should
// navigate to, accounting for the current layout mode and dual ring state.
func (m *AppModel) spatialFocusTarget(key string) (component.FocusID, bool) {
	mode := m.layout.Mode()
	slots := m.buildSlotMappings(mode)
	adjacency := m.spatialAdjacency(mode)
	return resolveSpatial(adjacency, m.focus.Current(), key, slots)
}

// buildSlotMappings returns the sentinel→actual mappings for the current mode.
func (m *AppModel) buildSlotMappings(mode layout.LayoutMode) []slotMapping {
	switch mode {
	case layout.FourColumn:
		return nil
	case layout.ThreeColumn:
		return []slotMapping{
			{leftSlotSentinel, m.leftRing.current()},
		}
	case layout.TwoColumn:
		return []slotMapping{
			{leftSlotSentinel, m.leftRing.current()},
			{rightSlotSentinel, m.rightRing.current()},
		}
	default:
		return []slotMapping{
			{leftSlotSentinel, m.leftRing.current()},
		}
	}
}

// spatialAdjacency selects the adjacency map for the current mode and ring.
func (m *AppModel) spatialAdjacency(mode layout.LayoutMode) map[focusEdge]component.FocusID {
	switch mode {
	case layout.FourColumn:
		return fourColumnSpatialMap
	case layout.ThreeColumn:
		return threeColumnSpatialMap
	case layout.TwoColumn:
		return twoColumnSpatialMap
	case layout.SingleColumn:
		if m.leftRing.current() == component.FocusSessionPanel {
			return singleColumnSessionMap
		}
		return singleColumnSpatialMap
	default:
		return singleColumnSpatialMap
	}
}

// resolveSpatial looks up a target in the adjacency map, substituting
// sentinels for actual panels in both source and target positions.
func resolveSpatial(adjacency map[focusEdge]component.FocusID, from component.FocusID, key string, slots []slotMapping) (component.FocusID, bool) {
	target, found := lookupWithSlots(adjacency, from, key, slots)
	if !found {
		return 0, false
	}
	// Resolve sentinel in target.
	for _, s := range slots {
		if target == s.sentinel {
			return s.actual, true
		}
	}
	return target, true
}

// lookupWithSlots tries a direct map lookup, then for each slot where
// from matches the slot's actual panel, retries with the slot's sentinel.
func lookupWithSlots(adjacency map[focusEdge]component.FocusID, from component.FocusID, key string, slots []slotMapping) (component.FocusID, bool) {
	if target, ok := adjacency[focusEdge{from, key}]; ok {
		return target, true
	}
	for _, s := range slots {
		if from == s.actual {
			if target, ok := adjacency[focusEdge{s.sentinel, key}]; ok {
				return target, ok
			}
		}
	}
	return 0, false
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
	// Resolve project root: explicit → git root → CWD.
	root := cfg.ProjectRoot
	if root == "" {
		cwd, _ := os.Getwd()
		if gitRoot, err := boot.FindGitRoot(cwd); err == nil {
			root = gitRoot
		} else {
			root = cwd
		}
	}

	app := New(cfg, deps)
	app.fileTree.SetRoot(root)

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
		seedCodePanel(app.codePanel)
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
