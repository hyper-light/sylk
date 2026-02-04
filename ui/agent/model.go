package agent

import (
	"strings"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
)

// AgentStatus represents the current operational state of an agent.
type AgentStatus int

const (
	StatusIdle     AgentStatus = iota
	StatusThinking
	StatusActing
	StatusError
	StatusHandoff
	StatusWaiting
	StatusSuccess
)

// statusStrings maps AgentStatus to display names.
var statusStrings = map[AgentStatus]string{
	StatusIdle:     "idle",
	StatusThinking: "thinking",
	StatusActing:   "acting",
	StatusError:    "error",
	StatusHandoff:  "handoff",
	StatusWaiting:  "waiting",
	StatusSuccess:  "success",
}

// String returns the display name for an AgentStatus.
func (s AgentStatus) String() string {
	if name, ok := statusStrings[s]; ok {
		return name
	}
	return "unknown"
}

// AgentState holds the current state of a single agent.
type AgentState struct {
	ID           string
	Name         string
	AgentType    string
	Status       AgentStatus
	TaskSummary  string
	ContextUsage float64 // 0.0 to 1.0
}

// maxAgents is the upper bound on tracked agents to prevent unbounded growth.
// Derived from typical multi-agent systems: 32 concurrent agents covers
// orchestrator + guide + architects + engineers + inspectors + specialists.
const maxAgents = 32

// maxAgentOrder tracks insert-order for consistent display. Same bound as maxAgents.
const maxAgentOrder = maxAgents

// eventTypeToStatus maps core EventType values to the AgentStatus they produce.
// This table-driven dispatch replaces a switch cascade.
var eventTypeToStatus = map[events.EventType]AgentStatus{
	events.EventTypeAgentAction:   StatusActing,
	events.EventTypeAgentDecision: StatusThinking,
	events.EventTypeAgentError:    StatusError,
	events.EventTypeToolCall:      StatusActing,
	events.EventTypeToolResult:    StatusIdle,
	events.EventTypeToolTimeout:   StatusError,
	events.EventTypeLLMRequest:    StatusThinking,
	events.EventTypeLLMResponse:   StatusIdle,
	events.EventTypeSuccess:       StatusSuccess,
	events.EventTypeFailure:       StatusError,
}

// viewState represents which view the agent panel is currently showing.
type viewState int

const (
	viewList        viewState = iota // Agent list with cards.
	viewExpanded                     // Expanded agent with navigable event list.
	viewEventDetail                  // Full content view of a single event.
)

// keyAction is a handler for a single key press in a specific view state.
type keyAction func(m *Model) tea.Cmd

// viewKeyActions maps view states to their key action tables.
func viewKeyActions() map[viewState]map[string]keyAction {
	return map[viewState]map[string]keyAction{
		viewList: {
			"j":     func(m *Model) tea.Cmd { m.moveSelection(1); return nil },
			"down":  func(m *Model) tea.Cmd { m.moveSelection(1); return nil },
			"k":     func(m *Model) tea.Cmd { m.moveSelection(-1); return nil },
			"up":    func(m *Model) tea.Cmd { m.moveSelection(-1); return nil },
			"enter": func(m *Model) tea.Cmd { m.enterExpanded(); return nil },
		},
		viewExpanded: {
			"j":     func(m *Model) tea.Cmd { m.moveEventSelection(1); return nil },
			"down":  func(m *Model) tea.Cmd { m.moveEventSelection(1); return nil },
			"k":     func(m *Model) tea.Cmd { m.moveEventSelection(-1); return nil },
			"up":    func(m *Model) tea.Cmd { m.moveEventSelection(-1); return nil },
			"enter": func(m *Model) tea.Cmd { m.enterEventDetail(); return nil },
			"esc":   func(m *Model) tea.Cmd { m.exitExpanded(); return nil },
		},
		viewEventDetail: {
			"j":     func(m *Model) tea.Cmd { m.scrollDetail(1); return nil },
			"down":  func(m *Model) tea.Cmd { m.scrollDetail(1); return nil },
			"k":     func(m *Model) tea.Cmd { m.scrollDetail(-1); return nil },
			"up":    func(m *Model) tea.Cmd { m.scrollDetail(-1); return nil },
			"esc":   func(m *Model) tea.Cmd { m.exitEventDetail(); return nil },
		},
	}
}

// activeEventTypes is the set of event types that mark an agent as the
// currently active agent. These represent an agent initiating work.
var activeEventTypes = map[events.EventType]bool{
	events.EventTypeAgentAction:   true,
	events.EventTypeAgentDecision: true,
	events.EventTypeLLMRequest:    true,
	events.EventTypeToolCall:      true,
}

// Model is the Bubble Tea model for the agent dashboard panel.
type Model struct {
	agents    map[string]*AgentState
	streams   map[string]*AgentEventStream
	order     []string  // Agent IDs in insertion order (bounded by maxAgentOrder).
	activeID  string    // Agent ID of the currently active agent.
	selected  int       // Index into order for list view navigation.
	expanded  string    // Agent ID of the expanded detail view ("" if none).
	view      viewState // Current view state.
	eventSel  int       // Selected event index in expanded view (logical, 0=oldest, -1=tail-follow).
	scrollOff int       // Scroll offset for event detail view.
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

// New creates an agent panel Model with the given theme.
func New(th *theme.Theme) *Model {
	return &Model{
		agents:   make(map[string]*AgentState, maxAgents),
		streams:  make(map[string]*AgentEventStream, maxAgents),
		order:    make([]string, 0, maxAgentOrder),
		theme:    th,
		view:     viewList,
		eventSel: -1,
	}
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
	case msg.ActivityEventMsg:
		return m, m.handleActivity(typed)
	case tea.KeyMsg:
		return m, m.handleKey(typed)
	default:
		return m, nil
	}
}

// View renders the agent panel based on the current view state.
func (m *Model) View() string {
	renderers := map[viewState]func() string{
		viewList:        m.renderListView,
		viewExpanded:    m.renderExpandedView,
		viewEventDetail: m.renderEventDetailView,
	}
	if render, ok := renderers[m.view]; ok {
		return render()
	}
	return ""
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the focus identifier for the agent panel.
func (m *Model) ID() component.FocusID {
	return component.FocusAgentPanel
}

// Focused returns whether the agent panel has focus.
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

// handleActivity processes an activity event to update agent state.
func (m *Model) handleActivity(ev msg.ActivityEventMsg) tea.Cmd {
	agentID := ev.Event.AgentID
	if agentID == "" {
		return nil
	}

	m.ensureAgent(agentID, ev)
	m.updateAgentStatus(agentID, ev)
	m.pushAgentEvent(agentID, ev)

	if activeEventTypes[ev.Event.EventType] {
		m.activeID = agentID
	}

	return nil
}

// ensureAgent creates an agent entry if it does not exist, respecting the bound.
func (m *Model) ensureAgent(agentID string, ev msg.ActivityEventMsg) {
	if _, exists := m.agents[agentID]; exists {
		return
	}
	if len(m.agents) >= maxAgents {
		return
	}

	agentType := extractString(ev.Event.Data, "agent_type")
	agentName := extractString(ev.Event.Data, "agent_name")
	if agentName == "" {
		agentName = agentID
	}

	m.agents[agentID] = &AgentState{
		ID:        agentID,
		Name:      agentName,
		AgentType: agentType,
		Status:    StatusIdle,
	}
	m.streams[agentID] = NewAgentEventStream()
	m.order = append(m.order, agentID)

	// First agent becomes active by default.
	if m.activeID == "" {
		m.activeID = agentID
	}
}

// updateAgentStatus applies the table-driven EventType->AgentStatus mapping.
func (m *Model) updateAgentStatus(agentID string, ev msg.ActivityEventMsg) {
	agent, ok := m.agents[agentID]
	if !ok {
		return
	}

	if status, found := eventTypeToStatus[ev.Event.EventType]; found {
		agent.Status = status
	}

	m.updateAgentMetadata(agent, ev)
}

// updateAgentMetadata extracts task summary and context usage from event data.
func (m *Model) updateAgentMetadata(agent *AgentState, ev msg.ActivityEventMsg) {
	if ev.Event.Content != "" {
		agent.TaskSummary = ev.Event.Content
	}

	if usage, ok := extractFloat(ev.Event.Data, "context_usage"); ok {
		agent.ContextUsage = usage
	}
}

// pushAgentEvent appends the activity event to the agent's event stream.
// When the expanded agent's stream evicts its oldest entry, the event
// selection index is decremented to keep the cursor on the same event.
func (m *Model) pushAgentEvent(agentID string, ev msg.ActivityEventMsg) {
	stream, ok := m.streams[agentID]
	if !ok {
		return
	}

	willEvict := stream.Full()

	stream.Push(AgentEvent{
		Timestamp: ev.Event.Timestamp,
		EventType: ev.Event.EventType,
		Content:   ev.Event.Content,
		Outcome:   ev.Event.Outcome,
	})

	if agentID == m.expanded && willEvict && m.eventSel >= 0 {
		m.eventSel = max(m.eventSel-1, 0)
	}
}

// handleKey processes keyboard input when the panel is focused.
// Key dispatch is driven by the current view state.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if !m.focused {
		return nil
	}

	tables := viewKeyActions()
	if actions, ok := tables[m.view]; ok {
		if action, ok := actions[key.String()]; ok {
			return action(m)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// CyclePrev moves the agent selection cursor backward (list view only).
func (m *Model) CyclePrev() {
	if m.view != viewList {
		return
	}
	m.moveSelection(-1)
}

// CycleNext moves the agent selection cursor forward (list view only).
func (m *Model) CycleNext() {
	if m.view != viewList {
		return
	}
	m.moveSelection(1)
}

// moveSelection moves the agent selection cursor by delta.
func (m *Model) moveSelection(delta int) {
	count := len(m.order)
	if count == 0 {
		return
	}
	m.selected = clampIndex(m.selected+delta, count)
}

// enterExpanded transitions from list view to expanded view for the selected agent.
func (m *Model) enterExpanded() {
	if len(m.order) == 0 {
		return
	}
	m.expanded = m.order[m.selected]
	m.view = viewExpanded
	m.eventSel = -1
	m.scrollOff = 0
}

// exitExpanded returns from expanded view to list view.
func (m *Model) exitExpanded() {
	m.expanded = ""
	m.view = viewList
	m.eventSel = -1
	m.scrollOff = 0
}

// enterEventDetail opens the full content view for the selected event.
func (m *Model) enterEventDetail() {
	if m.eventSel < 0 {
		return
	}
	m.view = viewEventDetail
	m.scrollOff = 0
}

// exitEventDetail returns to the expanded event list.
func (m *Model) exitEventDetail() {
	m.view = viewExpanded
	m.scrollOff = 0
}

// moveEventSelection moves the event cursor by delta within the expanded view.
// When eventSel is -1 (tail-follow), the first navigation selects the newest event.
func (m *Model) moveEventSelection(delta int) {
	stream, ok := m.streams[m.expanded]
	if !ok {
		return
	}
	count := stream.Len()
	if count == 0 {
		return
	}
	if m.eventSel < 0 {
		m.eventSel = count - 1
		return
	}
	m.eventSel = clampIndex(m.eventSel+delta, count)
}

// scrollDetail scrolls the event detail content by delta lines.
func (m *Model) scrollDetail(delta int) {
	m.scrollOff = max(m.scrollOff+delta, 0)
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// renderListView renders the compact list of agent cards.
// Selected cards occupy selectedCardLines; unselected occupy unselectedCardLines.
func (m *Model) renderListView() string {
	if len(m.order) == 0 {
		return ""
	}

	cards := make([]string, 0, min(len(m.order), m.height))
	var consumedLines int
	for i, agentID := range m.order {
		selected := i == m.selected
		needed := cardLineCount(selected)
		if consumedLines+needed > m.height {
			break
		}
		agent, ok := m.agents[agentID]
		if !ok {
			continue
		}
		cards = append(cards, RenderCard(*agent, m.width, m.theme, selected))
		consumedLines += needed
	}
	return strings.Join(cards, "\n")
}

// expandedSeparatorLines is the separator between the card and event stream.
const expandedSeparatorLines = 1

// expandedCardOverhead is the total lines consumed by the card and separator
// in the expanded view.
// Derived from: selectedCardLines(1) + expandedSeparatorLines(1) = 2.
const expandedCardOverhead = selectedCardLines + expandedSeparatorLines

// renderExpandedView renders the expanded agent with navigable event entries.
// Layout: card header + separator + selectable 2-line event entries.
func (m *Model) renderExpandedView() string {
	agent, ok := m.agents[m.expanded]
	if !ok {
		return ""
	}

	card := RenderCard(*agent, m.width, m.theme, true)
	separator := renderDetailSeparator(m.width, m.theme)

	var evts []AgentEvent
	if stream, ok := m.streams[m.expanded]; ok {
		evts = stream.Last(stream.Len())
	}

	availableLines := m.height - expandedCardOverhead
	eventContent := renderEventEntries(evts, m.width, availableLines, m.eventSel, m.theme)

	return card + "\n" + separator + "\n" + eventContent
}

// detailViewOverhead is the lines consumed by the card and separator
// in the event detail view. Same as expandedCardOverhead.
const detailViewOverhead = expandedCardOverhead

// renderEventDetailView renders the full content of the selected event.
// Layout: card header + separator + word-wrapped event content.
func (m *Model) renderEventDetailView() string {
	stream, ok := m.streams[m.expanded]
	if !ok {
		return ""
	}
	ev, ok := stream.Get(m.eventSel)
	if !ok {
		return ""
	}

	agent, ok := m.agents[m.expanded]
	if !ok {
		return ""
	}

	card := RenderCard(*agent, m.width, m.theme, true)
	separator := renderDetailSeparator(m.width, m.theme)

	availableLines := m.height - detailViewOverhead
	detail := renderEventDetailContent(ev, m.width, availableLines, m.scrollOff, m.theme)

	return card + "\n" + separator + "\n" + detail
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// clampIndex constrains an index to [0, count-1].
func clampIndex(idx, count int) int {
	return max(0, min(idx, count-1))
}

// extractString safely extracts a string value from a map.
func extractString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	val, ok := data[key]
	if !ok {
		return ""
	}
	s, ok := val.(string)
	if !ok {
		return ""
	}
	return s
}

// extractFloat safely extracts a float64 value from a map.
func extractFloat(data map[string]any, key string) (float64, bool) {
	if data == nil {
		return 0, false
	}
	val, ok := data[key]
	if !ok {
		return 0, false
	}
	f, ok := val.(float64)
	return f, ok
}
