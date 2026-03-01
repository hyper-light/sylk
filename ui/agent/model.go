package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	debugLog     *slog.Logger
	debugLogOnce sync.Once
)

func eventDebugLog() *slog.Logger {
	debugLogOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "ui_events.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			debugLog = slog.Default()
			return
		}
		debugLog = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	return debugLog
}

// AgentStatus represents the current operational state of an agent.
type AgentStatus int

const (
	StatusIdle AgentStatus = iota
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
	Category     string // "standalone", "pipeline", "knowledge" — resolved from agentCategoryByType.
	PipelineID   string // Non-empty when the agent is a pipeline member.
	Status       AgentStatus
	TaskSummary  string
	ContextUsage float64 // 0.0 to 1.0
	ModelID      string  // Currently assigned model ID (e.g. "claude-opus-4-6").

	// SupportedModels is the per-agent model list from the backend.
	// When non-empty, overrides the static provider-based model table.
	SupportedModels []ModelEntry
}

// ---------------------------------------------------------------------------
// Row model for tree-nested list rendering
// ---------------------------------------------------------------------------

// rowKind classifies a row in the flat list layout.
type rowKind int

const (
	rowSection rowKind = iota // Non-selectable section header.
	rowSpacer                 // Non-selectable spacer with left border.
	rowAgent                  // Selectable agent card.
	rowPipeline               // Selectable pipeline header.
	rowVariant                // Selectable variant sub-row.
)

// isNonSelectable reports whether a row kind cannot be selected by the user.
func (k rowKind) isNonSelectable() bool {
	return k == rowSection || k == rowSpacer
}

// listRow is an entry in the flattened display list.
type listRow struct {
	Kind       rowKind
	ID         string // Agent ID, pipeline ID, or variant ID depending on Kind.
	Label      string // Display label for section headers.
	PipelineID string // For rowVariant: owning pipeline.
}

// PipelineState holds the current state of a TDD pipeline.
type PipelineState struct {
	ID         string
	TaskID     string
	Status     string
	LoopCount  int
	MaxLoops   int
	WorkerType string
	Members    []string // Agent IDs belonging to this pipeline.
	CreatedAt  time.Time
}

// VariantState holds the current state of a pipeline variant.
type VariantState struct {
	ID         string
	PipelineID string
	Name       string
	State      string // created, active, suspended, complete, failed, merging, merged, cancelled.
	Message    string
}

// maxPipelines is the upper bound on tracked pipelines.
const maxPipelines = 8

// maxVariantsPerPipeline is the upper bound on variants per pipeline.
const maxVariantsPerPipeline = 4

// agentCategoryByType maps agent type strings to their display category.
// Mirrors core/handoff descriptor categories without importing the core package.
var agentCategoryByType = map[string]string{
	"guide":              "standalone",
	"orchestrator":       "standalone",
	"inspector":          "standalone",
	"tester":             "standalone",
	"engineer":           "pipeline",
	"designer":           "pipeline",
	"inspector-pipeline": "pipeline",
	"tester-pipeline":    "pipeline",
	"librarian":          "knowledge",
	"archivalist":        "knowledge",
	"academic":           "knowledge",
	"architect":          "standalone",
}

// agentDisplayOrder defines the canonical display position for each agent type
// within its section. Lower values sort first. Types not listed sort after
// all listed types, preserving insertion order among themselves.
var agentDisplayOrder = map[string]int{
	"guide":              0,
	"architect":          1,
	"orchestrator":       2,
	"inspector":          3,
	"tester":             4,
	"engineer":           5,
	"designer":           6,
	"inspector-pipeline": 7,
	"tester-pipeline":    8,
	"librarian":          9,
	"archivalist":        10,
	"academic":           11,
}

// agentDisplayOrderSentinel is the sort key for agent types not in agentDisplayOrder.
// Must exceed all values in the map.
const agentDisplayOrderSentinel = 100

// sortAgentsByDisplayOrder sorts agent IDs by their canonical display position.
// Agents whose type is not in the display order map sort after all listed types,
// preserving their relative insertion order (stable sort).
func (m *Model) sortAgentsByDisplayOrder(ids []string) {
	slices.SortStableFunc(ids, func(a, b string) int {
		return m.agentSortKey(a) - m.agentSortKey(b)
	})
}

// agentSortKey returns the display order for an agent ID.
func (m *Model) agentSortKey(id string) int {
	agent := m.agents[id]
	if agent == nil {
		return agentDisplayOrderSentinel
	}
	if key, ok := agentDisplayOrder[agent.AgentType]; ok {
		return key
	}
	return agentDisplayOrderSentinel
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
			"tab":   func(m *Model) tea.Cmd { m.enterSelector(); return nil },
		},
		viewExpanded: {
			"j":     func(m *Model) tea.Cmd { m.moveEventSelection(1); return nil },
			"down":  func(m *Model) tea.Cmd { m.moveEventSelection(1); return nil },
			"k":     func(m *Model) tea.Cmd { m.moveEventSelection(-1); return nil },
			"up":    func(m *Model) tea.Cmd { m.moveEventSelection(-1); return nil },
			"enter": func(m *Model) tea.Cmd { m.enterEventDetail(); return nil },
			"esc":   func(m *Model) tea.Cmd { m.exitExpanded(); return nil },
			"tab":   func(m *Model) tea.Cmd { m.enterSelector(); return nil },
		},
		viewEventDetail: {
			"j":    func(m *Model) tea.Cmd { m.scrollDetail(1); return nil },
			"down": func(m *Model) tea.Cmd { m.scrollDetail(1); return nil },
			"k":    func(m *Model) tea.Cmd { m.scrollDetail(-1); return nil },
			"up":   func(m *Model) tea.Cmd { m.scrollDetail(-1); return nil },
			"esc":  func(m *Model) tea.Cmd { m.exitEventDetail(); return nil },
			"tab":  func(m *Model) tea.Cmd { m.enterSelector(); return nil },
		},
	}
}

// activeStatuses is the set of agent statuses that represent active work.
// Used for animated dot icons, ripple text, and conditional shimmer intensity.
var activeStatuses = map[AgentStatus]bool{
	StatusThinking: true,
	StatusActing:   true,
	StatusHandoff:  true,
}

// isActiveStatus reports whether the status represents active work.
func isActiveStatus(s AgentStatus) bool { return activeStatuses[s] }

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
	engagedID string    // Agent ID the user is conversing with (sticky until reroute/override).
	selected     int       // Index into m.rows for list view navigation.
	userSelected bool      // True when the user manually changed selection; suppresses auto-follow.
	expanded     string    // Agent ID of the expanded detail view ("" if none).
	view         viewState // Current view state.
	eventSel     int       // Selected event index in expanded view (logical, 0=oldest, -1=tail-follow).
	scrollOff    int       // Scroll offset for event detail view.
	theme        *theme.Theme
	width        int
	height       int
	focused      bool

	dotFrame int // Current frame index for the filling-circle dot animation.

	// Model selector state.
	selector modelSelector

	// Pipeline & variant state.
	pipelines     map[string]*PipelineState  // Pipeline ID → state.
	variants      map[string]*VariantState   // Variant ID → state.
	pipelineOrder []string                   // Pipeline IDs in insertion order.
	rows          []listRow                  // Flattened display list, rebuilt lazily.
	rowsDirty     bool                       // True when rows need rebuilding.
	shimmerStart       time.Time     // Epoch for gradient shimmer animation.
	gradient           *theme.Gradient // Pipeline progress shimmer gradient.
	groupGradient      *theme.Gradient // Active: full prismatic. Swapped on HasActiveAgent.
	idleGroupGradient  *theme.Gradient // Idle: green→jade→blue→white.
	activeGroupGradient *theme.Gradient // Active: full prismatic spectrum.
	rippleGradient     *theme.Gradient // Per-character ripple for active agent text.
}

// Verify interface compliance at compile time.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates an agent panel Model with the given theme.
func New(th *theme.Theme) *Model {
	idleGroup := th.Palette.IdleGroupGradient()
	return &Model{
		agents:              make(map[string]*AgentState, maxAgents),
		streams:             make(map[string]*AgentEventStream, maxAgents),
		order:               make([]string, 0, maxAgentOrder),
		pipelines:           make(map[string]*PipelineState, maxPipelines),
		variants:            make(map[string]*VariantState, maxPipelines*maxVariantsPerPipeline),
		pipelineOrder:       make([]string, 0, maxPipelines),
		theme:               th,
		view:                viewList,
		eventSel:            -1,
		rowsDirty:           true,
		shimmerStart:        time.Now(),
		gradient:            th.Palette.PipelineGradient(),
		groupGradient:       idleGroup,
		idleGroupGradient:   idleGroup,
		activeGroupGradient: th.Palette.GroupGradient(),
		rippleGradient:      th.Palette.RippleGradient(),
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
	case msg.PipelineStateMsg:
		return m, m.handlePipelineState(typed)
	case msg.VariantStateMsg:
		return m, m.handleVariantState(typed)
	case msg.StreamProgressMsg:
		return m, m.handleStreamProgress(typed)
	case msg.StreamCompleteMsg:
		return m, m.handleStreamComplete(typed)
	case tea.KeyMsg:
		return m, m.handleKey(typed)
	default:
		return m, nil
	}
}

// View renders the agent panel based on the current view state.
// Output is always padded to exactly m.height lines for stable frame sizes.
func (m *Model) View() string {
	renderers := map[viewState]func() string{
		viewList:        m.renderListView,
		viewExpanded:    m.renderExpandedView,
		viewEventDetail: m.renderEventDetailView,
	}
	if render, ok := renderers[m.view]; ok {
		return padToHeight(render(), m.height)
	}
	return padToHeight("", m.height)
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

// SetFocused sets the focus state. Losing focus exits the selector.
func (m *Model) SetFocused(focused bool) {
	if !focused && m.selector.active {
		m.exitSelector()
	}
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
	vis := ev.Event.Visibility
	eventDebugLog().Info("agent_panel: activity",
		"agent_id", agentID,
		"event_type", ev.Event.EventType,
		"visibility", vis,
		"content", ev.Event.Content,
		"outcome", ev.Event.Outcome,
		"timestamp", ev.Event.Timestamp.Format(time.RFC3339Nano))
	if agentID == "" {
		return nil
	}

	// System events (health checks, fire-and-forget) are recorded for
	// debugging but never update panel status or promote agents.
	if vis == events.VisibilitySystem {
		m.pushAgentEvent(agentID, ev)
		return nil
	}

	m.ensureAgent(agentID, ev)

	// Agent-to-agent events update status (so the card shows work) but
	// do NOT demote the previous active agent or auto-select.
	if vis == events.VisibilityAgent {
		m.updateAgentStatus(agentID, ev)
		m.pushAgentEvent(agentID, ev)
		return nil
	}

	// User-visible: full promotion path.
	m.updateAgentStatus(agentID, ev)
	m.pushAgentEvent(agentID, ev)

	if activeEventTypes[ev.Event.EventType] {
		m.demotePreviousActive(agentID)
		m.activeID = agentID
		if !m.focused && !m.userSelected {
			m.SelectByID(agentID)
		}
	}

	return nil
}

// handleStreamProgress updates an agent's status and task summary from a
// stream progress event. This surfaces Architect/Orchestrator/Guide progress
// messages (e.g. "Analyzing requirements...", "Dispatching task 1/3...") in
// the agent panel cards alongside the chat panel's thinking placeholder.
func (m *Model) handleStreamProgress(progress msg.StreamProgressMsg) tea.Cmd {
	agentID := strings.TrimSpace(progress.AgentID)
	vis := progress.Visibility
	eventDebugLog().Info("agent_panel: stream_progress",
		"agent_id", agentID,
		"visibility", vis,
		"message", progress.Message,
		"correlation_id", progress.CorrelationID,
		"session_id", progress.SessionID,
		"current", progress.Current,
		"total", progress.Total)
	if agentID == "" || strings.TrimSpace(progress.Message) == "" {
		return nil
	}

	// System events: no panel updates.
	if vis == events.VisibilitySystem {
		return nil
	}

	agent := m.agents[agentID]
	if agent == nil {
		return nil
	}
	agent.Status = StatusThinking
	agent.TaskSummary = progress.Message
	m.rowsDirty = true

	// Agent-to-agent: update status text but don't promote.
	if vis == events.VisibilityAgent {
		return nil
	}

	// User-visible: full promotion.
	m.demotePreviousActive(agentID)
	m.activeID = agentID
	return nil
}

// handleStreamComplete transitions the responding agent back to StatusIdle
// when a stream finishes. This is the normal completion counterpart to
// handleStreamProgress (which sets StatusThinking on progress events).
func (m *Model) handleStreamComplete(done msg.StreamCompleteMsg) tea.Cmd {
	agentID := strings.TrimSpace(done.AgentID)
	if agentID == "" {
		return nil
	}
	agent, ok := m.agents[agentID]
	if !ok {
		return nil
	}
	agent.Status = StatusIdle
	m.rowsDirty = true
	return nil
}

// demotePreviousActive transitions the previous active agent from an active
// status to Waiting when a different agent takes over. This prevents stale
// active indicators on agents that handed off control (e.g. guide → architect)
// without receiving a terminal event.
func (m *Model) demotePreviousActive(newActiveID string) {
	if m.activeID == "" || m.activeID == newActiveID {
		return
	}
	prev, ok := m.agents[m.activeID]
	if !ok {
		return
	}
	if isActiveStatus(prev.Status) {
		prev.Status = StatusIdle
	}
}

// ensureAgent creates an agent entry if it does not exist, respecting the bound.
// For standalone agents (one-per-type), re-keys the existing entry to the new
// UUID instead of creating a duplicate. This handles two cases:
//  1. Seeded placeholder (ID == AgentType) promoted on first activation.
//  2. Re-activation after demotion: old UUID entry promoted to new UUID.
func (m *Model) ensureAgent(agentID string, ev msg.ActivityEventMsg) {
	if _, exists := m.agents[agentID]; exists {
		return
	}

	agentType := extractString(ev.Event.Data, "agent_type")

	// For standalone agents there should be exactly one panel entry per type.
	// Find any existing entry with the same type and re-key it to the new
	// UUID. This covers both seeded placeholders (ID == AgentType) and
	// previously-activated entries whose UUID became stale after demotion.
	if agentType != "" && agentCategoryByType[agentType] == "standalone" {
		if existing := m.findAgentByType(agentType); existing != nil {
			m.promoteSeededAgent(existing, agentID, ev)
			return
		}
	}

	if len(m.agents) >= maxAgents {
		return
	}

	agentName := extractString(ev.Event.Data, "agent_name")
	if agentName == "" {
		agentName = agentID
	}

	category := agentCategoryByType[agentType]
	pipelineID := extractString(ev.Event.Data, "pipeline_id")

	agent := &AgentState{
		ID:         agentID,
		Name:       agentName,
		AgentType:  agentType,
		Category:   category,
		PipelineID: pipelineID,
		Status:     StatusIdle,
		ModelID:    defaultModelForAgent(agentType),
	}
	m.agents[agentID] = agent
	m.streams[agentID] = NewAgentEventStream()
	m.order = append(m.order, agentID)
	m.rowsDirty = true

	// Register as pipeline member if the pipeline already exists.
	if pipelineID != "" {
		if pl, ok := m.pipelines[pipelineID]; ok {
			if len(pl.Members) < maxAgents {
				pl.Members = append(pl.Members, agentID)
			}
		}
	}

	// First agent becomes active by default.
	if m.activeID == "" {
		m.activeID = agentID
	}
}

// promoteSeededAgent replaces a seeded placeholder entry with the real agent ID.
// The existing stream is preserved; only the map key and order slice are updated.
func (m *Model) promoteSeededAgent(placeholder *AgentState, realID string, ev msg.ActivityEventMsg) {
	oldID := placeholder.ID

	// Update the agent state. Preserve the existing display name (e.g.
	// "Tester") — only adopt the event name when the placeholder has none.
	placeholder.ID = realID
	if placeholder.Name == "" {
		if name := extractString(ev.Event.Data, "agent_name"); name != "" {
			placeholder.Name = name
		}
	}

	// Re-key in maps.
	delete(m.agents, oldID)
	m.agents[realID] = placeholder

	stream := m.streams[oldID]
	delete(m.streams, oldID)
	if stream == nil {
		stream = NewAgentEventStream()
	}
	m.streams[realID] = stream

	// Update order slice.
	for i, id := range m.order {
		if id == oldID {
			m.order[i] = realID
			break
		}
	}

	// Update tracking references.
	if m.activeID == oldID {
		m.activeID = realID
	}
	if m.engagedID == oldID {
		m.engagedID = realID
	}
	if m.expanded == oldID {
		m.expanded = realID
	}

	m.rowsDirty = true
}

// findAgentByType returns the first agent entry with the given type, or nil.
// Used to locate existing standalone entries for re-keying on re-activation.
// Bounded by maxAgents (≤32).
func (m *Model) findAgentByType(agentType string) *AgentState {
	for _, agent := range m.agents {
		if agent.AgentType == agentType {
			return agent
		}
	}
	return nil
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

// handlePipelineState creates or updates a pipeline state entry.
func (m *Model) handlePipelineState(ps msg.PipelineStateMsg) tea.Cmd {
	pl, exists := m.pipelines[ps.PipelineID]
	if !exists {
		if len(m.pipelines) >= maxPipelines {
			return nil
		}
		pl = &PipelineState{
			ID:        ps.PipelineID,
			CreatedAt: time.Now(),
		}
		m.pipelines[ps.PipelineID] = pl
		m.pipelineOrder = append(m.pipelineOrder, ps.PipelineID)

		// Populate members from agents that arrived before the pipeline.
		for _, agentID := range m.order {
			if a := m.agents[agentID]; a != nil && a.PipelineID == ps.PipelineID {
				pl.Members = append(pl.Members, agentID)
			}
		}
	}

	pl.TaskID = ps.TaskID
	pl.Status = ps.Status
	pl.LoopCount = ps.LoopCount
	pl.MaxLoops = ps.MaxLoops
	pl.WorkerType = ps.WorkerType
	m.rowsDirty = true

	return nil
}

// handleVariantState creates or updates a variant state entry.
func (m *Model) handleVariantState(vs msg.VariantStateMsg) tea.Cmd {
	v, exists := m.variants[vs.VariantID]
	if !exists {
		// Enforce per-pipeline variant limit.
		count := 0
		for _, existing := range m.variants {
			if existing.PipelineID == vs.PipelineID {
				count++
			}
		}
		if count >= maxVariantsPerPipeline {
			return nil
		}

		v = &VariantState{ID: vs.VariantID}
		m.variants[vs.VariantID] = v
	}

	v.PipelineID = vs.PipelineID
	v.Name = vs.Name
	v.State = vs.State
	v.Message = vs.Message
	m.rowsDirty = true

	return nil
}

// HasActiveAgent reports whether at least one agent is in an active working
// state (Thinking, Acting, or Handoff). Used to gate full-spectrum shimmer
// and dot/ripple animations.
func (m *Model) HasActiveAgent() bool {
	for _, agent := range m.agents {
		if isActiveStatus(agent.Status) {
			return true
		}
	}
	return false
}

// DemoteAllActive transitions every agent in an active status (Thinking,
// Acting, Handoff) to StatusIdle. Called by the app when an interrupt or
// stream error makes active agents stale before a terminal event arrives.
func (m *Model) DemoteAllActive() {
	for _, agent := range m.agents {
		if isActiveStatus(agent.Status) {
			agent.Status = StatusIdle
		}
	}
}

// AdvanceDotFrame increments the filling-circle dot animation frame counter.
// Called once per decor tick when active agents exist.
func (m *Model) AdvanceDotFrame() {
	m.dotFrame = (m.dotFrame + 1) % dotAnimFrameCount
	m.DecrementSelectorFlash()
}

// NeedsDecorTick reports whether the agent panel has active shimmer animations.
// True when any agent is actively working (dot/ripple/full shimmer) or a
// pipeline is active. Idle agents still shimmer with the subdued gradient,
// so agents existing is sufficient.
func (m *Model) NeedsDecorTick() bool {
	if len(m.agents) > 0 {
		return true
	}
	for _, pl := range m.pipelines {
		if !isTerminalPipelineStatus(pl.Status) {
			return true
		}
	}
	return false
}

// handleKey processes keyboard input when the panel is focused.
// Key dispatch is driven by the current view state.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if !m.focused {
		return nil
	}

	// Selector mode intercepts all keys while active.
	if m.selector.active {
		return m.handleSelectorKey(key)
	}

	tables := viewKeyActions()
	if actions, ok := tables[m.view]; ok {
		if action, ok := actions[key.String()]; ok {
			return action(m)
		}
	}
	return nil
}

// selectorKeyActions maps key strings to selector actions.
// Table-driven dispatch matching the viewKeyActions pattern.
var selectorKeyActions = map[string]func(m *Model) tea.Cmd{
	"tab":   func(m *Model) tea.Cmd { m.toggleSelectorFocus(); return nil },
	"enter": func(m *Model) tea.Cmd { return m.triggerSelectorFocus() },
	" ":     func(m *Model) tea.Cmd { return m.triggerSelectorFocus() },
	"left":  func(m *Model) tea.Cmd { return m.cycleModelPrev() },
	"h":     func(m *Model) tea.Cmd { return m.cycleModelPrev() },
	"right": func(m *Model) tea.Cmd { return m.cycleModelNext() },
	"l":     func(m *Model) tea.Cmd { return m.cycleModelNext() },
	"esc":   func(m *Model) tea.Cmd { m.exitSelector(); return nil },
}

// handleSelectorKey dispatches keys while the selector is active.
func (m *Model) handleSelectorKey(key tea.KeyMsg) tea.Cmd {
	if action, ok := selectorKeyActions[key.String()]; ok {
		return action(m)
	}
	return nil
}

// enterSelector activates the model selector for the current agent.
func (m *Model) enterSelector() {
	agent := m.selectedAgent()
	if agent == nil {
		return
	}
	models := agentModels(agent)
	if len(models) <= 1 {
		return
	}
	m.selector.active = true
	m.selector.focus = selectorFocusLeft
}

// exitSelector deactivates the model selector.
func (m *Model) exitSelector() {
	m.selector.active = false
	m.selector.focus = selectorFocusNone
}

// toggleSelectorFocus cycles focus: left → right → exit.
func (m *Model) toggleSelectorFocus() {
	if m.selector.focus == selectorFocusLeft {
		m.selector.focus = selectorFocusRight
		return
	}
	m.exitSelector()
}

// triggerSelectorFocus activates the focused arrow direction.
func (m *Model) triggerSelectorFocus() tea.Cmd {
	switch m.selector.focus {
	case selectorFocusLeft:
		return m.cycleModelPrev()
	case selectorFocusRight:
		return m.cycleModelNext()
	}
	return nil
}

// selectedAgent returns the agent state for the current context:
// expanded/detail views use m.expanded; list view uses the selected row.
func (m *Model) selectedAgent() *AgentState {
	if m.view != viewList && m.expanded != "" {
		return m.agents[m.expanded]
	}
	id := m.SelectedAgentID()
	if id == "" {
		return nil
	}
	return m.agents[id]
}

// cycleModelPrev cycles the selected agent's model to the previous entry.
func (m *Model) cycleModelPrev() tea.Cmd {
	agent := m.selectedAgent()
	if agent == nil {
		return nil
	}
	models := agentModels(agent)
	if len(models) <= 1 {
		return nil
	}
	idx := modelIndex(models, agent.ModelID)
	agent.ModelID = models[cyclePrev(idx, len(models))].ID
	m.selector.flash = flashFrames
	return m.emitModelChange(agent)
}

// cycleModelNext cycles the selected agent's model to the next entry.
func (m *Model) cycleModelNext() tea.Cmd {
	agent := m.selectedAgent()
	if agent == nil {
		return nil
	}
	models := agentModels(agent)
	if len(models) <= 1 {
		return nil
	}
	idx := modelIndex(models, agent.ModelID)
	agent.ModelID = models[cycleNext(idx, len(models))].ID
	m.selector.flash = flashFrames
	return m.emitModelChange(agent)
}

// emitModelChange returns a command that produces a ModelChangeMsg.
func (m *Model) emitModelChange(agent *AgentState) tea.Cmd {
	change := msg.ModelChangeMsg{
		AgentID:   agent.ID,
		AgentType: agent.AgentType,
		ModelID:   agent.ModelID,
	}
	return func() tea.Msg { return change }
}

// DecrementSelectorFlash decrements the flash counter. Called per decor tick.
func (m *Model) DecrementSelectorFlash() {
	if m.selector.flash > 0 {
		m.selector.flash--
	}
}

// SelectorActive reports whether the model selector is in active mode.
func (m *Model) SelectorActive() bool {
	return m.selector.active
}

// ---------------------------------------------------------------------------
// Row layout
// ---------------------------------------------------------------------------

// rebuildRows computes the flat row list from agents, pipelines, and variants.
// Sections: standalone → pipeline groups → knowledge.
func (m *Model) rebuildRows() {
	m.rowsDirty = false

	// Categorize agents.
	var standalone, knowledge []string
	pipelineMembers := make(map[string][]string, len(m.pipelineOrder))
	for _, plID := range m.pipelineOrder {
		pipelineMembers[plID] = nil
	}

	for _, agentID := range m.order {
		agent := m.agents[agentID]
		if agent == nil {
			continue
		}
		if agent.PipelineID != "" {
			if _, ok := pipelineMembers[agent.PipelineID]; ok {
				pipelineMembers[agent.PipelineID] = append(pipelineMembers[agent.PipelineID], agentID)
				continue
			}
		}
		switch agent.Category {
		case "knowledge":
			knowledge = append(knowledge, agentID)
		default:
			standalone = append(standalone, agentID)
		}
	}

	// Sort each section by canonical display order.
	m.sortAgentsByDisplayOrder(standalone)
	m.sortAgentsByDisplayOrder(knowledge)

	// Estimate capacity: sections + agents + pipelines + variants.
	capacity := 3 + len(m.order) + len(m.pipelineOrder) + len(m.variants)
	rows := make([]listRow, 0, capacity)

	// Standalone section.
	if len(standalone) > 0 {
		rows = append(rows, listRow{Kind: rowSpacer})
		rows = append(rows, listRow{Kind: rowSection, Label: "global"})
		for _, id := range standalone {
			rows = append(rows, listRow{Kind: rowAgent, ID: id})
		}
	}

	// Pipeline sections.
	for _, plID := range m.pipelineOrder {
		pl := m.pipelines[plID]
		if pl == nil {
			continue
		}
		rows = append(rows, listRow{Kind: rowSpacer})
		rows = append(rows, listRow{Kind: rowPipeline, ID: plID, Label: pl.TaskID})
		for _, agentID := range pipelineMembers[plID] {
			rows = append(rows, listRow{Kind: rowAgent, ID: agentID, PipelineID: plID})
		}
		// Append variants for this pipeline.
		for _, v := range m.variantsForPipeline(plID) {
			rows = append(rows, listRow{Kind: rowVariant, ID: v.ID, PipelineID: plID})
		}
	}

	// Knowledge section.
	if len(knowledge) > 0 {
		rows = append(rows, listRow{Kind: rowSpacer})
		rows = append(rows, listRow{Kind: rowSection, Label: "knowledge"})
		for _, id := range knowledge {
			rows = append(rows, listRow{Kind: rowAgent, ID: id})
		}
	}

	m.rows = rows
}

// variantsForPipeline returns variants belonging to the given pipeline in stable order.
func (m *Model) variantsForPipeline(pipelineID string) []*VariantState {
	var result []*VariantState
	for _, v := range m.variants {
		if v.PipelineID == pipelineID {
			result = append(result, v)
		}
	}
	return result
}

// ensureRows rebuilds the row list if dirty.
func (m *Model) ensureRows() {
	if m.rowsDirty {
		m.rebuildRows()
		m.clampToSelectable()
	}
}

// clampToSelectable adjusts the selection to the nearest selectable row.
func (m *Model) clampToSelectable() {
	if len(m.rows) == 0 {
		m.selected = 0
		return
	}
	m.selected = clampIndex(m.selected, len(m.rows))

	// If on a non-selectable row, advance forward to the next selectable row.
	if m.rows[m.selected].Kind.isNonSelectable() {
		for i := m.selected + 1; i < len(m.rows); i++ {
			if !m.rows[i].Kind.isNonSelectable() {
				m.selected = i
				return
			}
		}
		// Try backward.
		for i := m.selected - 1; i >= 0; i-- {
			if !m.rows[i].Kind.isNonSelectable() {
				m.selected = i
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// ScrollUp scrolls the agent panel up by one step, respecting the current view state.
// Returns true if scroll was consumed. List view returns false because mouse
// scroll should not change agent selection.
func (m *Model) ScrollUp() bool {
	switch m.view {
	case viewExpanded:
		m.moveEventSelection(-1)
		return true
	case viewEventDetail:
		m.scrollDetail(-1)
		return true
	default:
		return false
	}
}

// ScrollDown scrolls the agent panel down by one step, respecting the current view state.
// Returns true if scroll was consumed. List view returns false because mouse
// scroll should not change agent selection.
func (m *Model) ScrollDown() bool {
	switch m.view {
	case viewExpanded:
		m.moveEventSelection(1)
		return true
	case viewEventDetail:
		m.scrollDetail(1)
		return true
	default:
		return false
	}
}

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

// InSubView reports whether the agent panel is in a navigable sub-view
// (expanded agent or event detail) that should consume Esc before the app.
func (m *Model) InSubView() bool {
	return m.view != viewList
}

// SelectByID moves the list selection to the row with the given agent ID.
// Returns true if the agent was found.
func (m *Model) SelectByID(agentID string) bool {
	m.ensureRows()
	for i, row := range m.rows {
		if !row.Kind.isNonSelectable() && row.ID == agentID {
			m.selected = i
			return true
		}
	}
	return false
}

// SeedAgent creates an idle agent entry without requiring an activity event.
// Used during bootstrap to pre-populate the panel with known agents.
// When supportedModels is non-empty, it overrides the static model table.
// No-op if the agent already exists or the panel is at capacity.
func (m *Model) SeedAgent(id, agentType, name string, supportedModels []ModelEntry) {
	if _, exists := m.agents[id]; exists {
		return
	}
	if len(m.agents) >= maxAgents {
		return
	}
	category := agentCategoryByType[agentType]
	modelID := defaultModelForAgent(agentType)
	if len(supportedModels) > 0 {
		modelID = supportedModels[0].ID
	}
	agent := &AgentState{
		ID:              id,
		Name:            name,
		AgentType:       agentType,
		Category:        category,
		Status:          StatusIdle,
		ModelID:         modelID,
		SupportedModels: supportedModels,
	}
	m.agents[id] = agent
	m.streams[id] = NewAgentEventStream()
	m.order = append(m.order, id)
	m.rowsDirty = true
}

// SetEngagedAgent sets the agent the user is conversing with. This is sticky
// across messages until a reroute or explicit @agent override clears it.
// Resets userSelected so auto-follow resumes for the new conversation turn.
func (m *Model) SetEngagedAgent(agentID string) {
	m.engagedID = strings.ToLower(strings.TrimSpace(agentID))
	m.userSelected = false
}

// ClearEngagedAgent removes the current engagement, forcing full classification
// on the next user message.
func (m *Model) ClearEngagedAgent() {
	m.engagedID = ""
}

// EngagedAgentID returns the currently engaged agent ID, or "" if none.
func (m *Model) EngagedAgentID() string {
	return m.engagedID
}

// SelectedAgentID returns the agent ID of the currently selected row.
// Returns "" when no selectable row is selected or the row is not an agent.
func (m *Model) SelectedAgentID() string {
	m.ensureRows()
	if len(m.rows) == 0 {
		return ""
	}
	index := clampIndex(m.selected, len(m.rows))
	row := m.rows[index]
	if row.Kind == rowAgent {
		return row.ID
	}
	return ""
}

// moveSelection moves the selection cursor by delta, skipping non-selectable rows.
func (m *Model) moveSelection(delta int) {
	m.ensureRows()
	count := len(m.rows)
	if count == 0 {
		return
	}
	next := clampIndex(m.selected+delta, count)

	// Skip non-selectable rows in the direction of movement.
	for next >= 0 && next < count && m.rows[next].Kind.isNonSelectable() {
		if delta > 0 {
			next++
		} else {
			next--
		}
	}
	next = clampIndex(next, count)

	// If we still landed on a non-selectable row, stay put.
	if m.rows[next].Kind.isNonSelectable() {
		return
	}
	m.selected = next
	m.userSelected = true
}

// enterExpanded transitions from list view to expanded view for the selected row.
func (m *Model) enterExpanded() {
	m.ensureRows()
	if len(m.rows) == 0 {
		return
	}
	idx := clampIndex(m.selected, len(m.rows))
	row := m.rows[idx]
	if row.Kind.isNonSelectable() {
		return
	}
	m.expanded = row.ID
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

// renderListView renders the tree-nested list of sections, agents, pipelines, and variants.
func (m *Model) renderListView() string {
	m.ensureRows()
	if len(m.rows) == 0 {
		return ""
	}

	elapsed := time.Since(m.shimmerStart)
	hasActive := m.HasActiveAgent()
	ripple := m.focused && hasActive

	// Swap group gradient: full prismatic when focused + active, subdued otherwise.
	if ripple {
		m.groupGradient = m.activeGroupGradient
	} else {
		m.groupGradient = m.idleGroupGradient
	}

	anim := AnimState{
		DotFrame:   m.dotFrame,
		Elapsed:    elapsed,
		HasActive:  hasActive,
		Ripple:     ripple,
		RippleGrad: m.rippleGradient,
	}

	contentHeight := m.height
	activeStart, activeEnd := m.selectedGroupRange()
	lines := make([]string, 0, min(len(m.rows)*2, contentHeight))
	var consumedLines int

	for i, row := range m.rows {
		if consumedLines >= contentHeight {
			break
		}
		selected := i == m.selected
		inActiveGroup := i >= activeStart && i <= activeEnd

		// Per-row phase offset creates a downward-flowing prismatic wave.
		var activeColor lipgloss.Color
		if inActiveGroup {
			phase := elapsed - time.Duration(i-activeStart)*groupFlowStep
			activeColor = m.groupGradient.Sample(phase)
		}

		switch row.Kind {
		case rowSection:
			lines = append(lines, renderSectionHeader(row.Label, activeColor, m.theme))
			consumedLines++

		case rowSpacer:
			lines = append(lines, renderSpacer(activeColor, m.theme))
			consumedLines++

		case rowAgent:
			agent, ok := m.agents[row.ID]
			if !ok {
				continue
			}
			engaged := m.engagedID != "" && row.ID == m.engagedID
			prefix := renderTreePrefix(pipelinePrefix, activeColor, m.theme)
			lines = append(lines, RenderCard(*agent, m.width, m.theme, selected, engaged, prefix, anim))
			consumedLines++

		case rowPipeline:
			pl := m.pipelines[row.ID]
			if pl == nil {
				continue
			}
			lines = append(lines, renderPipelineRow(pl, m.width, elapsed, m.gradient, m.theme, selected))
			consumedLines++

		case rowVariant:
			v := m.variants[row.ID]
			if v == nil {
				continue
			}
			lines = append(lines, renderVariantRow(v, m.width, m.theme, selected))
			consumedLines++
		}

		// Footer gets the next phase step after the last content row.
		if consumedLines < contentHeight && m.isGroupEnd(i) {
			var footerColor lipgloss.Color
			if inActiveGroup {
				phase := elapsed - time.Duration(i-activeStart+1)*groupFlowStep
				footerColor = m.groupGradient.Sample(phase)
			}
			lines = append(lines, renderSectionFooter(m.width, footerColor, m.theme))
			consumedLines++
		}
	}

	return strings.Join(lines, "\n")
}

// selectedGroupRange returns the inclusive [start, end] row indices of the group
// containing the currently selected row. A group starts at a section or pipeline
// header and extends until the next header or end of list.
func (m *Model) selectedGroupRange() (int, int) {
	if len(m.rows) == 0 {
		return 0, 0
	}
	sel := clampIndex(m.selected, len(m.rows))

	// Walk backward to find the group header.
	start := sel
	for start > 0 {
		if m.rows[start].Kind == rowSection || m.rows[start].Kind == rowPipeline {
			break
		}
		start--
	}

	// Include the spacer row immediately before the group header.
	if start > 0 && m.rows[start-1].Kind == rowSpacer {
		start--
	}

	// Walk forward to find the end of the group.
	end := sel
	for end+1 < len(m.rows) {
		next := m.rows[end+1]
		if next.Kind == rowSection || next.Kind == rowPipeline || next.Kind == rowSpacer {
			break
		}
		end++
	}

	return start, end
}

// renderSectionHeader renders a section label. When activeColor is non-empty,
// the label uses that color (holographic shimmer); otherwise it falls back to Muted.
func renderSectionHeader(label string, activeColor lipgloss.Color, th *theme.Theme) string {
	color := th.Palette.Muted
	if activeColor != "" {
		color = activeColor
	}
	style := lipgloss.NewStyle().Foreground(color).Bold(true)
	return " " + style.Render(label)
}

// renderSpacer renders an empty line with a left border glyph.
// Uses the same color animation as section headers: activeColor when the
// group is focused+active, Subtle otherwise.
func renderSpacer(activeColor lipgloss.Color, th *theme.Theme) string {
	color := th.Palette.Subtle
	if activeColor != "" {
		color = activeColor
	}
	return lipgloss.NewStyle().Foreground(color).Render(" \u2502") // " │"
}

// renderTreePrefix renders a tree connector glyph. When activeColor is non-empty,
// the glyph uses that color; otherwise it falls back to Subtle.
func renderTreePrefix(glyph string, activeColor lipgloss.Color, th *theme.Theme) string {
	color := th.Palette.Subtle
	if activeColor != "" {
		color = activeColor
	}
	return lipgloss.NewStyle().Foreground(color).Render(glyph)
}

// groupFlowStep is the phase offset between adjacent rows in the holographic
// shimmer. Each row samples the gradient this much behind the row above,
// creating a downward-flowing prismatic wave.
// At 10fps with a 6-second cycle (8 colors, 750ms per segment), 250ms per
// row produces a visible color shift across 3-5 rows without strobing.
const groupFlowStep = 250 * time.Millisecond

// isGroupEnd reports whether row at index i is the last content row before
// the next section header, pipeline header, or end of list.
func (m *Model) isGroupEnd(i int) bool {
	row := m.rows[i]
	// Non-selectable rows (section headers, spacers) don't get footers.
	if row.Kind.isNonSelectable() {
		return false
	}

	// Last row in the entire list.
	if i+1 >= len(m.rows) {
		return true
	}

	next := m.rows[i+1]
	// A section or pipeline header starts a new group.
	return next.Kind == rowSection || next.Kind == rowPipeline
}

// sectionFooterFraction is the fraction of panel width used for the footer rule.
// Derived from the design spec: 1/4 to 1/3. We use 1/4 as the minimum for compactness.
const sectionFooterFraction = 4

// renderSectionFooter renders a rounded bottom-left corner followed by a short
// horizontal rule, spanning ~1/4 of the panel width. When activeColor is non-empty,
// the footer uses that color; otherwise it falls back to Subtle.
func renderSectionFooter(width int, activeColor lipgloss.Color, th *theme.Theme) string {
	ruleLen := max(width/sectionFooterFraction, 2) - 1 // -1 for the corner glyph
	color := th.Palette.Subtle
	if activeColor != "" {
		color = activeColor
	}
	style := lipgloss.NewStyle().Foreground(color)
	return style.Render(" \u2570" + strings.Repeat("\u2500", ruleLen))
}

// expandedSeparatorLines is the separator between the card and event stream.
const expandedSeparatorLines = 1

// expandedCardOverhead is the total lines consumed by the card and separator
// in the expanded view.
// Derived from: selectedCardLines(1) + expandedSeparatorLines(1) = 2.
const expandedCardOverhead = selectedCardLines + expandedSeparatorLines

// renderExpandedView renders the expanded view for the selected row.
// For agents: card header + separator + selectable event entries.
// For pipelines/variants: detail view.
func (m *Model) renderExpandedView() string {
	// Check if expanded ID is a pipeline.
	if pl, ok := m.pipelines[m.expanded]; ok {
		return renderExpandedPipeline(pl, m.width, m.height, m.theme)
	}
	// Check if expanded ID is a variant.
	if v, ok := m.variants[m.expanded]; ok {
		return renderExpandedVariant(v, m.width, m.height, m.theme)
	}

	// Default: agent expanded view.
	agent, ok := m.agents[m.expanded]
	if !ok {
		return ""
	}

	engaged := m.engagedID != "" && m.expanded == m.engagedID
	card := RenderCard(*agent, m.width, m.theme, true, engaged, "", AnimState{})
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

	engaged := m.engagedID != "" && m.expanded == m.engagedID
	card := RenderCard(*agent, m.width, m.theme, true, engaged, "", AnimState{})
	separator := renderDetailSeparator(m.width, m.theme)

	availableLines := m.height - detailViewOverhead
	detail := renderEventDetailContent(ev, m.width, availableLines, m.scrollOff, m.theme)

	return card + "\n" + separator + "\n" + detail
}

// ---------------------------------------------------------------------------
// Model selector rendering & mouse
// ---------------------------------------------------------------------------

// RenderSelectorLine renders the bottom-line model selector for the current agent.
// Exported so the app can place it at the absolute bottom of the left panel.
func (m *Model) RenderSelectorLine() string {
	agent := m.selectedAgent()
	if agent == nil {
		return ""
	}
	models := agentModels(agent)
	if len(models) == 0 {
		return ""
	}
	idx := modelIndex(models, agent.ModelID)
	return renderModelSelector(models, idx, m.width, m.selector, m.theme)
}

// HandleSelectorClick processes a mouse click at local x-coordinate on the
// selector line. Returns a command if a model change was triggered.
func (m *Model) HandleSelectorClick(x int) tea.Cmd {
	agent := m.selectedAgent()
	if agent == nil {
		return nil
	}
	models := agentModels(agent)
	idx := modelIndex(models, agent.ModelID)
	hit := selectorArrowHitTest(x, m.width, models, idx)
	switch hit {
	case selectorFocusLeft:
		return m.cycleModelPrev()
	case selectorFocusRight:
		return m.cycleModelNext()
	}
	return nil
}

// HandleSelectorHover updates hover state for the selector arrows.
func (m *Model) HandleSelectorHover(x int) {
	agent := m.selectedAgent()
	if agent == nil {
		return
	}
	models := agentModels(agent)
	idx := modelIndex(models, agent.ModelID)
	hit := selectorArrowHitTest(x, m.width, models, idx)
	m.selector.hoverLeft = hit == selectorFocusLeft
	m.selector.hoverRight = hit == selectorFocusRight
}

// ClearSelectorHover resets all hover state on the selector.
func (m *Model) ClearSelectorHover() {
	m.selector.hoverLeft = false
	m.selector.hoverRight = false
}

// SelectorLineCount returns the number of lines the selector occupies.
func (m *Model) SelectorLineCount() int {
	return selectorLineCount
}

// RevertModelID sets the agent's ModelID back to a previous value.
// Used when a backend swap fails to undo the optimistic UI update.
func (m *Model) RevertModelID(agentID, previousModelID string) {
	agent, ok := m.agents[agentID]
	if !ok {
		return
	}
	agent.ModelID = previousModelID
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// padToHeight ensures s contains exactly targetHeight lines by appending empty lines.
func padToHeight(s string, targetHeight int) string {
	if targetHeight <= 0 {
		return s
	}
	if s == "" {
		return strings.Repeat("\n", max(targetHeight-1, 0))
	}
	current := strings.Count(s, "\n") + 1
	if current < targetHeight {
		s += strings.Repeat("\n", targetHeight-current)
	}
	return s
}

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
