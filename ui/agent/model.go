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

// keyActions maps key strings to handler methods for focused keyboard input.
type keyAction func(m *Model) tea.Cmd

func keyActionTable() map[string]keyAction {
	return map[string]keyAction{
		"j":     func(m *Model) tea.Cmd { m.moveSelection(1); return nil },
		"down":  func(m *Model) tea.Cmd { m.moveSelection(1); return nil },
		"k":     func(m *Model) tea.Cmd { m.moveSelection(-1); return nil },
		"up":    func(m *Model) tea.Cmd { m.moveSelection(-1); return nil },
		"enter": func(m *Model) tea.Cmd { m.toggleExpand(); return nil },
		"esc":   func(m *Model) tea.Cmd { m.collapse(); return nil },
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
	order     []string // Agent IDs in insertion order (bounded by maxAgentOrder).
	activeID  string   // Agent ID of the currently active agent.
	selected  int      // Index into order for keyboard navigation.
	expanded  string   // Agent ID of the expanded detail view ("" if none).
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
		agents:  make(map[string]*AgentState, maxAgents),
		streams: make(map[string]*AgentEventStream, maxAgents),
		order:   make([]string, 0, maxAgentOrder),
		theme:   th,
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

// View renders the agent panel.
func (m *Model) View() string {
	if m.expanded != "" {
		return m.renderExpandedView()
	}
	return m.renderListView()
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
func (m *Model) pushAgentEvent(agentID string, ev msg.ActivityEventMsg) {
	stream, ok := m.streams[agentID]
	if !ok {
		return
	}

	stream.Push(AgentEvent{
		Timestamp: ev.Event.Timestamp,
		EventType: ev.Event.EventType,
		Content:   ev.Event.Content,
		Outcome:   ev.Event.Outcome,
	})
}

// handleKey processes keyboard input when the panel is focused.
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

// CyclePrev moves the agent selection cursor backward.
func (m *Model) CyclePrev() {
	m.moveSelection(-1)
}

// CycleNext moves the agent selection cursor forward.
func (m *Model) CycleNext() {
	m.moveSelection(1)
}

// moveSelection moves the selection cursor by delta (positive = down, negative = up).
func (m *Model) moveSelection(delta int) {
	count := len(m.order)
	if count == 0 {
		return
	}
	m.selected = clampIndex(m.selected+delta, count)
}

// toggleExpand expands the selected agent or collapses if already expanded.
func (m *Model) toggleExpand() {
	if len(m.order) == 0 {
		return
	}
	agentID := m.order[m.selected]
	if m.expanded == agentID {
		m.expanded = ""
		return
	}
	m.expanded = agentID
}

// collapse closes the detail view.
func (m *Model) collapse() {
	m.expanded = ""
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// renderListView renders the compact list of agent cards.
func (m *Model) renderListView() string {
	if len(m.order) == 0 {
		return ""
	}

	lines := make([]string, 0, min(len(m.order), m.height))
	for i, agentID := range m.order {
		if len(lines) >= m.height {
			break
		}
		agent, ok := m.agents[agentID]
		if !ok {
			continue
		}
		selected := i == m.selected
		active := agentID == m.activeID
		lines = append(lines, RenderCard(*agent, m.width, m.theme, selected, m.focused, active))
	}
	return strings.Join(lines, "\n")
}

// expandedCardOverhead is the number of lines consumed by the card and separator
// in the expanded view.
// Derived from: 1 (card) + 1 (separator) = 2.
const expandedCardOverhead = 2

// renderExpandedView renders the detail view for the expanded agent.
// The selected agent card stays visible at the top, followed by a separator
// and the event stream below.
func (m *Model) renderExpandedView() string {
	agent, ok := m.agents[m.expanded]
	if !ok {
		return ""
	}

	active := m.expanded == m.activeID
	card := RenderCard(*agent, m.width, m.theme, true, m.focused, active)

	var evts []AgentEvent
	if stream, ok := m.streams[m.expanded]; ok {
		evts = stream.Last(m.height)
	}

	separator := renderDetailSeparator(m.width, m.theme)
	availableLines := m.height - expandedCardOverhead
	eventContent := renderEventLines(evts, m.width, availableLines, m.theme)

	return card + "\n" + separator + "\n" + eventContent
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
