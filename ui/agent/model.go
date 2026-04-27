package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/agentidentity"
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
//
// Identity keying: every row has a stable panel `ID` string (used as
// the map key in `Model.agents`) and — once the first event with a
// typed `*identity.AgentIdentity` arrives — a populated `Identity`
// pointer. The panel map is seeded with placeholder rows keyed on
// the agent-type string; when an event arrives the matching
// placeholder is bound to the concrete Identity. Replicas (Owner !=
// nil) always create a NEW row keyed on `Identity.UID()` — they
// never absorb an existing row. Generation bumps (SwapModel) keep
// the same UID → same row, with `Identity.Model()` and `Generation()`
// advancing in place.
type AgentState struct {
	ID                        string
	RoutingID                 string
	Name                      string
	AgentType                 string
	Category                  string // "standalone", "pipeline", "knowledge" — resolved from agentCategoryByType.
	PipelineID                string // Non-empty when the agent is a pipeline member.
	Status                    AgentStatus
	ActivityState             events.AgentUIState
	TaskSummary               string
	ContextUsage              float64 // 0.0 to 1.0
	ModelID                   string  // Currently assigned model ID (e.g. "claude-opus-4-6").
	ProviderID                string  // Provider for ModelID (e.g. "anthropic", "google", "openai").
	ActiveReplicas            int
	MaxReplicas               int
	QueuedRequests            int
	MaxQueuedRequests         int
	toolSummaryPinned         bool
	pinnedToolCallKey         string
	activeCorrelationID       string
	lastTerminalCorrelationID string

	// Identity carries the typed AgentIdentity once an activity event
	// or token-delta with an attached Identity value has reached this
	// row. Populated from ActivityEvent.Identity or TokenDeltaMsg.Identity.
	// Stable reference: same UID across model swaps (Generation bumps).
	// Non-nil indicates the row is bound to a live typed identity —
	// seeded placeholders have Identity == nil until first activation.
	// Always points at the canonical agent (Owner == nil); replicas
	// are tracked via replicaUIDs, not this field.
	Identity *identity.AgentIdentity
	// Generation mirrors Identity.Generation() for cheap comparison
	// when detecting SwapModel transitions.
	Generation uint64
	// replicaUIDs is the set of active replica UIDs bound to this
	// canonical row. Len(replicaUIDs) == ActiveReplicas. Replicas do
	// not get their own panel rows — they accumulate here so the
	// replicaSuperscriptSuffix display can show "⁺³" counts.
	replicaUIDs map[identity.UID]struct{}

	// SupportedModels is the raw per-agent model list from the backend.
	// When non-empty, it overrides the static provider-based model table and
	// is filtered at render/selection time for auth-aware model availability.
	SupportedModels []ModelEntry
}

// ---------------------------------------------------------------------------
// Row model for tree-nested list rendering
// ---------------------------------------------------------------------------

// rowKind classifies a row in the flat list layout.
type rowKind int

const (
	rowSection  rowKind = iota // Non-selectable section header.
	rowSpacer                  // Non-selectable spacer with left border.
	rowAgent                   // Selectable agent card.
	rowPipeline                // Selectable pipeline header.
	rowVariant                 // Selectable variant sub-row.
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
	ID               string
	TaskID           string
	TaskSlug         string
	TaskLabel        string
	Status           string
	LoopCount        int
	MaxLoops         int
	WorkerType       string
	ClaimsAccepted   int
	ClaimsTotalCount int
	Members          []string // Agent IDs belonging to this pipeline.
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// VariantState holds the current state of a pipeline variant.
type VariantState struct {
	ID         string
	PipelineID string
	Name       string
	State      string // created, active, suspended, complete, failed, merging, merged, cancelled.
	Message    string
}

// initialPipelineCapacity is the initial allocation size for tracked pipelines.
const initialPipelineCapacity = 8

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

func resolveAgentCategory(agentType, pipelineID string) string {
	agentType = strings.TrimSpace(agentType)
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID != "" && isPipelineAgentType(agentType) {
		return "pipeline"
	}
	return agentCategoryByType[agentType]
}

func isPipelineAgentType(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case "engineer", "designer", "inspector-pipeline", "tester-pipeline":
		return true
	default:
		return false
	}
}

func inferSeedPipelineIdentity(agentID, agentType string) (resolvedAgentType, pipelineID string) {
	agentID = strings.TrimSpace(agentID)
	agentType = strings.TrimSpace(agentType)
	if scopedPipelineID, scopedAgentType, ok := parseTaskScopedPipelineAgentID(agentID); ok {
		if pipelineID == "" {
			pipelineID = scopedPipelineID
		}
		if agentType == "" {
			agentType = scopedAgentType
		}
	}
	if canonicalPipelineID, canonicalAgentType, ok := parseCanonicalPipelinePanelAgentID(agentID); ok {
		if pipelineID == "" {
			pipelineID = canonicalPipelineID
		}
		if agentType == "" {
			agentType = canonicalAgentType
		}
	}
	return agentType, strings.TrimSpace(pipelineID)
}

func canonicalPipelineAgentID(agentType, pipelineID string) string {
	if resolveAgentCategory(agentType, pipelineID) != "pipeline" {
		return ""
	}
	return agentidentity.CanonicalPipelinePanelID(agentType, pipelineID)
}

func shouldUseCanonicalSingletonRow(agentType, category string) bool {
	agentType = strings.TrimSpace(agentType)
	category = strings.TrimSpace(category)
	if agentType == "" {
		return false
	}
	switch category {
	case "standalone", "knowledge":
		return true
	default:
		return false
	}
}

func isCanonicalSingletonAgentType(agentType string) bool {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		return false
	}
	switch resolveAgentCategory(agentType, "") {
	case "standalone", "knowledge":
		return true
	default:
		return false
	}
}

func isProtectedCanonicalSingletonID(agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	return isCanonicalSingletonAgentType(agentID)
}

func isInternalSidecarAgent(agentID, agentType string) bool {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	agentType = strings.ToLower(strings.TrimSpace(agentType))
	if agentType == "scribe" || strings.HasPrefix(agentType, "scribe-") {
		return true
	}
	return agentID == "scribe" || strings.HasPrefix(agentID, "scribe-")
}

func parseTaskScopedPipelineAgentID(agentID string) (taskID, agentType string, ok bool) {
	return agentidentity.ParseTaskScopedPipelineAgentID(agentID)
}

func parseCanonicalPipelinePanelAgentID(agentID string) (pipelineID, agentType string, ok bool) {
	return agentidentity.ParseCanonicalPipelinePanelAgentID(agentID)
}

func canonicalStreamAgentID(agentID, agentType, pipelineID, taskID string) string {
	return agentidentity.VisibleAgentID(agentID, "", agentType, pipelineID, taskID)
}

func defaultRoutingAgentID(agentID, agentType, pipelineID string) string {
	agentID = strings.TrimSpace(agentID)
	agentType = strings.TrimSpace(agentType)
	pipelineID = strings.TrimSpace(pipelineID)
	if resolveAgentCategory(agentType, pipelineID) == "pipeline" && pipelineID != "" {
		switch agentType {
		case "engineer", "designer", "inspector-pipeline", "tester-pipeline":
			return pipelineID + "-" + agentType
		}
	}
	return agentID
}

// initialAgentCapacity is the initial allocation size for tracked agents.
const initialAgentCapacity = 32

// initialAgentOrderCapacity tracks insert-order for consistent display.
const initialAgentOrderCapacity = initialAgentCapacity

// eventTypeToStatus maps core EventType values to the AgentStatus they produce.
// This table-driven dispatch replaces a switch cascade.
var eventTypeToStatus = map[events.EventType]AgentStatus{
	events.EventTypeAgentAction:     StatusActing,
	events.EventTypeAgentDecision:   StatusThinking,
	events.EventTypeAgentError:      StatusError,
	events.EventTypeToolCall:        StatusActing,
	events.EventTypeToolResult:      StatusIdle,
	events.EventTypeToolTimeout:     StatusError,
	events.EventTypeLLMRequest:      StatusThinking,
	events.EventTypeLLMResponse:     StatusIdle,
	events.EventTypeSuccess:         StatusSuccess,
	events.EventTypeFailure:         StatusError,
	events.EventTypeAgentRegistered:    StatusWaiting,
	events.EventTypeClaimReceived:      StatusWaiting,
	events.EventTypeTestamentSubmitted: StatusSuccess,
	events.EventTypeValidationPassed:   StatusSuccess,
	events.EventTypeClaimAccepted:      StatusSuccess,
	events.EventTypeValidationFailed:   StatusError,
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
			" ":     func(m *Model) tea.Cmd { m.toggleSelectedPipelineCollapse(); return nil },
			"left":  func(m *Model) tea.Cmd { m.collapseSelectedPipeline(); return nil },
			"right": func(m *Model) tea.Cmd { m.expandSelectedPipeline(); return nil },
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

// Model is the Bubble Tea model for the agent dashboard panel.
type Model struct {
	agents    map[string]*AgentState
	aliases   map[string]string // Runtime/ephemeral agent IDs → canonical panel row IDs.
	// byUID indexes the agents map by identity.UID once a row has
	// been bound to a typed *AgentIdentity. Used to route ActivityEvents
	// carrying event.Identity directly to their row, and to enforce
	// per-replica / per-generation panel semantics:
	//   - Replicas (Owner != nil) always get their own row keyed on UID.
	//   - SwapModel (Generation bump) keeps the same UID → same row.
	// Seeded placeholder rows have no UID entry until first activation.
	byUID     map[identity.UID]string
	streams   map[string]*AgentEventStream
	order     []string  // Agent IDs in insertion order.
	engagedID string    // Agent ID the user is conversing with (sticky until reroute/override).
	selected  int       // Index into m.rows for list view navigation.
	expanded  string    // Agent ID of the expanded detail view ("" if none).
	view      viewState // Current view state.
	eventSel  int       // Selected event index in expanded view (logical, 0=oldest, -1=tail-follow).
	scrollOff int       // Scroll offset for event detail view.
	theme     *theme.Theme
	width     int
	height    int
	focused   bool
	nerdFonts bool

	dotFrame int // Current frame index for the filling-circle dot animation.

	// Model selector state.
	selector         modelSelector
	modelStore       *AgentModelStore // Persisted model selections (nil-safe).
	openAIAuthMethod string

	// Pipeline & variant state.
	pipelines           map[string]*PipelineState // Pipeline ID → state.
	variants            map[string]*VariantState  // Variant ID → state.
	pipelineOrder       []string                  // Pipeline IDs in insertion order.
	pipelineScroll      int                       // Scroll offset for the pipelines subsection in list view.
	collapsedPipelines  map[string]bool           // Pipeline ID → collapsed state in list view.
	lineRowMap          []int                     // Rendered list-view line → row index (-1 when not a selectable row line).
	rows                []listRow                 // Flattened display list, rebuilt lazily.
	rowsDirty           bool                      // True when rows need rebuilding.
	shimmerStart        time.Time                 // Epoch for gradient shimmer animation.
	gradient            *theme.Gradient           // Pipeline progress shimmer gradient.
	groupGradient       *theme.Gradient           // Active: full prismatic. Swapped on HasActiveAgent.
	idleGroupGradient   *theme.Gradient           // Idle: green→jade→blue→white.
	activeGroupGradient *theme.Gradient           // Active: full prismatic spectrum.
	rippleGradient      *theme.Gradient           // Per-character ripple for active agent text.
	idleDecorBucket     int64                     // Quantized idle shimmer phase to avoid redundant repaints.
}

type pipelineViewportLayout struct {
	bodyStart      int
	viewportHeight int
	rowHeights     []int
	rowOffsets     []int
	totalLines     int
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
		agents:              make(map[string]*AgentState, initialAgentCapacity),
		aliases:             make(map[string]string, initialAgentCapacity*2),
		byUID:               make(map[identity.UID]string, initialAgentCapacity),
		streams:             make(map[string]*AgentEventStream, initialAgentCapacity),
		order:               make([]string, 0, initialAgentOrderCapacity),
		pipelines:           make(map[string]*PipelineState, initialPipelineCapacity),
		variants:            make(map[string]*VariantState, initialPipelineCapacity*maxVariantsPerPipeline),
		pipelineOrder:       make([]string, 0, initialPipelineCapacity),
		collapsedPipelines:  make(map[string]bool, initialPipelineCapacity),
		theme:               th,
		view:                viewList,
		eventSel:            -1,
		rowsDirty:           true,
		shimmerStart:        time.Now(),
		gradient:            th.Palette.PipelineGradient(),
		groupGradient:       idleGroup,
		idleGroupGradient:   idleGroup,
		activeGroupGradient: th.Palette.GroupGradient(),
		rippleGradient:      th.Palette.ThinkingGradient(),
		idleDecorBucket:     -1,
		openAIAuthMethod:    credentials.DefaultAuthMethod("openai"),
	}
}

// SetModelStore injects the persisted model store so dynamically-created
// agents pick up the user's previous model selection.
func (m *Model) SetModelStore(store *AgentModelStore) {
	m.modelStore = store
}

// SetOpenAIAuthMethod updates the panel's auth-aware OpenAI model view.
func (m *Model) SetOpenAIAuthMethod(method string) {
	canonical := credentials.CanonicalAuthMethod("openai", method)
	if canonical == "" {
		canonical = credentials.DefaultAuthMethod("openai")
	}
	if m.openAIAuthMethod == canonical {
		return
	}
	m.openAIAuthMethod = canonical
	for _, agent := range m.agents {
		m.reconcileAgentModel(agent)
	}
	if agent := m.selectedAgent(); agent != nil && len(m.agentModels(agent)) <= 1 && m.selector.active {
		m.exitSelector()
	}
}

// SetNerdFonts toggles Nerd Font badge rendering for agent icons.
func (m *Model) SetNerdFonts(detected bool) {
	m.nerdFonts = detected
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
	case msg.StreamStartMsg:
		return m, m.handleStreamStart(typed)
	case msg.StreamProgressMsg:
		return m, m.handleStreamProgress(typed)
	case msg.StreamCompleteMsg:
		return m, m.handleStreamComplete(typed)
	case msg.ToolCallEventMsg:
		return m, m.handleToolCallEvent(typed)
	case msg.ClaimsProjectionMsg:
		return m, m.handleClaimsProjection(typed)
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
	// Typed-identity fast path: when the provider-gateway hook
	// stamped a *AgentIdentity on the event (post-FIX_ID_AND_TOKENS),
	// route directly to the UID-indexed row. This bypasses string
	// canonicalization entirely — replicas and generation bumps land
	// on the correct row by construction, not by string matching.
	if ev.Event != nil && ev.Event.Identity != nil {
		rowID := m.bindIdentityToRow(ev.Event.Identity)
		if rowID != "" {
			ev.Event.AgentID = rowID
		}
	}

	rawAgentID := strings.TrimSpace(ev.Event.AgentID)
	if agentType, _ := m.inferActivityAgentIdentity(rawAgentID, ev); isInternalSidecarAgent(rawAgentID, agentType) {
		return nil
	}
	agentID := m.canonicalizeAgentID(rawAgentID, ev)
	if m.shouldIgnoreAmbiguousPipelineActivity(rawAgentID, ev) {
		return nil
	}
	vis := ev.Event.Visibility
	eventDebugLog().Info("agent_panel: activity",
		"agent_id", rawAgentID,
		"panel_agent_id", agentID,
		"event_type", ev.Event.EventType,
		"visibility", vis,
		"content", ev.Event.Content,
		"outcome", ev.Event.Outcome,
		"timestamp", ev.Event.Timestamp.Format(time.RFC3339Nano))
	if rawAgentID == "" && agentID == "" {
		return nil
	}
	if agentID == "" {
		agentID = rawAgentID
	}

	m.rehomeDuplicateAgentRows(agentID, ev)
	m.rememberAgentAliases(agentID, rawAgentID, ev)

	// System events (health checks, fire-and-forget) are recorded for
	// debugging but never update panel status or promote agents.
	if vis == events.VisibilitySystem {
		m.pushAgentEvent(agentID, ev)
		return nil
	}

	agentID = m.ensureAgent(agentID, ev)
	m.rememberAgentAliases(agentID, rawAgentID, ev)

	// Agent-to-agent events update status (so the card shows work) but
	// do NOT demote the previous active agent or auto-select.
	if vis == events.VisibilityAgent {
		m.updateAgentStatus(agentID, ev)
		m.pushAgentEvent(agentID, ev)
		return nil
	}

	// User-visible: update status only. No auto-follow or active promotion.
	// Agent status is already set by updateAgentStatus above.
	// Multiple agents can be active simultaneously; isActiveStatus() on each
	// agent's Status is the source of truth.
	m.updateAgentStatus(agentID, ev)
	m.pushAgentEvent(agentID, ev)

	return nil
}

func (m *Model) shouldIgnoreAmbiguousPipelineActivity(rawAgentID string, ev msg.ActivityEventMsg) bool {
	agentType, pipelineID := m.inferActivityAgentIdentity(rawAgentID, ev)
	return isPipelineAgentType(agentType) && strings.TrimSpace(pipelineID) == ""
}

// handleStreamProgress updates an agent's status and task summary from a
// stream progress event. This surfaces Architect/Orchestrator/Guide progress
// messages (e.g. "Analyzing requirements...", "Dispatching task 1/3...") in
// the agent panel cards alongside the chat panel's thinking placeholder.
func (m *Model) handleStreamProgress(progress msg.StreamProgressMsg) tea.Cmd {
	candidateID := canonicalStreamAgentID(progress.AgentID, progress.AgentType, progress.PipelineID, progress.TaskID)
	if isInternalSidecarAgent(candidateID, progress.AgentType) || isInternalSidecarAgent(progress.AgentID, progress.AgentType) {
		return nil
	}
	agentID := m.resolveAgentID(candidateID)
	vis := progress.Visibility
	eventDebugLog().Info("agent_panel: stream_progress",
		"agent_id", progress.AgentID,
		"panel_agent_id", agentID,
		"visibility", vis,
		"message", progress.Message,
		"correlation_id", progress.CorrelationID,
		"session_id", progress.SessionID,
		"current", progress.Current,
		"total", progress.Total)
	if agentID == "" {
		agentID = m.ensureAgentForLiveEvent(candidateID, progress.AgentID, progress.AgentType, progress.AgentName, progress.PipelineID, progress.TaskID, progress.TaskName, progress.TaskSlug)
	}
	if agentID == "" {
		return nil
	}

	// System events: no panel updates.
	if vis == events.VisibilitySystem {
		return nil
	}

	agent := m.agents[agentID]
	if agent == nil {
		agentID = m.ensureAgentForLiveEvent(candidateID, progress.AgentID, progress.AgentType, progress.AgentName, progress.PipelineID, progress.TaskID, progress.TaskName, progress.TaskSlug)
		agent = m.agents[agentID]
		if agent == nil {
			return nil
		}
	}
	if shouldIgnoreStaleAgentCorrelation(agent, progress.CorrelationID) {
		return nil
	}
	next := *agent
	next.Status = StatusThinking
	next.ActivityState = inferUIStateFromStreamProgress(&next, progress)
	applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
		Source:        agentLifecycleSourceStreamProgress,
		CorrelationID: progress.CorrelationID,
		Status:        next.Status,
		ActivityState: next.ActivityState,
	})
	if raw := strings.TrimSpace(progress.Message); raw != "" {
		if progress.Watchdog && strings.TrimSpace(agent.TaskSummary) != "" {
			m.rowsDirty = true
			return nil
		}
		payload := compactAgentPanelPayload(raw)
		shouldApply := shouldApplyStructuredProgressSummary(agent.TaskSummary, payload)
		if !progress.ToolDerived && !progress.Watchdog && shouldApply && agent.toolSummaryPinned {
			agent.toolSummaryPinned = false
			agent.pinnedToolCallKey = ""
		}
		if shouldApply && !agent.toolSummaryPinned {
			agent.TaskSummary = payload.summary
		}
	}
	m.rowsDirty = true

	// No auto-promotion for any visibility level.
	// Status is already set to StatusThinking above — multiple agents can be active.
	return nil
}

func (m *Model) handleStreamStart(start msg.StreamStartMsg) tea.Cmd {
	if start.Visibility == events.VisibilitySystem {
		return nil
	}
	candidateID := canonicalStreamAgentID(start.AgentID, start.AgentType, start.PipelineID, start.TaskID)
	if isInternalSidecarAgent(candidateID, start.AgentType) || isInternalSidecarAgent(start.AgentID, start.AgentType) {
		return nil
	}
	agentID := m.resolveAgentID(candidateID)
	if agentID == "" {
		agentID = m.ensureAgentForLiveEvent(candidateID, start.AgentID, start.AgentType, start.AgentName, start.PipelineID, start.TaskID, start.TaskName, start.TaskSlug)
	}
	if agentID == "" {
		return nil
	}
	agent := m.agents[agentID]
	if agent == nil {
		agentID = m.ensureAgentForLiveEvent(candidateID, start.AgentID, start.AgentType, start.AgentName, start.PipelineID, start.TaskID, start.TaskName, start.TaskSlug)
		agent = m.agents[agentID]
		if agent == nil {
			return nil
		}
	}
	if shouldIgnoreStaleAgentCorrelation(agent, start.CorrelationID) {
		return nil
	}
	applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
		Source:        agentLifecycleSourceStreamStart,
		CorrelationID: start.CorrelationID,
		Status:        StatusActing,
		ActivityState: events.AgentUIStateResponding,
	})
	m.rowsDirty = true
	return nil
}

// handleStreamComplete transitions the responding agent back to StatusIdle
// when a stream finishes. This is the normal completion counterpart to
// handleStreamProgress (which sets StatusThinking on progress events).
func (m *Model) handleStreamComplete(done msg.StreamCompleteMsg) tea.Cmd {
	if done.Visibility == events.VisibilitySystem {
		return nil
	}
	if isInternalSidecarAgent(done.AgentID, done.AgentType) {
		return nil
	}
	agentID := m.resolveAgentID(canonicalStreamAgentID(done.AgentID, done.AgentType, done.PipelineID, done.TaskID))
	if agentID == "" {
		return nil
	}
	agent, ok := m.agents[agentID]
	if !ok {
		return nil
	}
	consumeKnowledgeReplicaOnStreamComplete(agent)
	if knowledgeAgentHasPendingReplicaLoad(agent) {
		markAgentCorrelationTerminal(agent, done.CorrelationID)
		m.rowsDirty = true
		return nil
	}
	applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
		Source:        agentLifecycleSourceStreamComplete,
		CorrelationID: done.CorrelationID,
		Status:        StatusIdle,
		ActivityState: events.AgentUIStateNone,
	})
	markAgentCorrelationTerminal(agent, done.CorrelationID)
	m.rowsDirty = true
	return nil
}

func consumeKnowledgeReplicaOnStreamComplete(agent *AgentState) {
	if agent == nil || agent.Category != "knowledge" {
		return
	}
	if agent.ActiveReplicas > 0 {
		agent.ActiveReplicas--
	}
}

func (m *Model) handleToolCallEvent(event msg.ToolCallEventMsg) tea.Cmd {
	candidateID := canonicalStreamAgentID(event.AgentID, event.AgentType, event.PipelineID, event.TaskID)
	if isInternalSidecarAgent(candidateID, event.AgentType) || isInternalSidecarAgent(event.AgentID, event.AgentType) {
		return nil
	}
	agentID := m.resolveAgentID(strings.TrimSpace(candidateID))
	if agentID == "" {
		agentID = m.ensureAgentForLiveEvent(candidateID, event.AgentID, event.AgentType, event.AgentName, event.PipelineID, event.TaskID, event.TaskName, event.TaskSlug)
	}
	if agentID == "" {
		agentID = strings.TrimSpace(event.AgentID)
	}
	if agentID == "" {
		return nil
	}
	agent := m.agents[agentID]
	if agent == nil {
		agentID = m.ensureAgentForLiveEvent(candidateID, event.AgentID, event.AgentType, event.AgentName, event.PipelineID, event.TaskID, event.TaskName, event.TaskSlug)
		agent = m.agents[agentID]
		if agent == nil {
			return nil
		}
	}
	switch event.Phase {
	case 0:
		if shouldIgnoreStaleAgentCorrelation(agent, event.CorrelationID) {
			return nil
		}
		next := *agent
		next.Status = StatusActing
		next.ActivityState = inferUIStateFromToolStart(&next, event.ToolName, event.ArgsSummary)
		applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
			Source:        agentLifecycleSourceToolStart,
			CorrelationID: event.CorrelationID,
			Status:        next.Status,
			ActivityState: next.ActivityState,
		})
		if summary := summarizeAgentPanelToolEvent(event); summary != "" {
			agent.TaskSummary = summary
			agent.toolSummaryPinned = true
			agent.pinnedToolCallKey = strings.TrimSpace(event.ToolCallKey)
		}
	case 1:
		if !agent.toolSummaryPinned || strings.TrimSpace(agent.pinnedToolCallKey) == "" || agent.pinnedToolCallKey == strings.TrimSpace(event.ToolCallKey) {
			agent.toolSummaryPinned = false
			agent.pinnedToolCallKey = ""
		}
		if !event.Success {
			next := *agent
			next.Status = StatusError
			next.ActivityState = inferUIStateFromToolFailure(&next, event)
			if !applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
				Source:        agentLifecycleSourceToolComplete,
				CorrelationID: event.CorrelationID,
				Status:        next.Status,
				ActivityState: next.ActivityState,
			}) {
				m.rowsDirty = true
				return nil
			}
			if text := summarizeAgentPanelToolEvent(event); text != "" {
				agent.TaskSummary = text
			}
		} else {
			next := *agent
			next.Status = StatusThinking
			next.ActivityState = fallbackUIStateAfterToolSuccess(&next)
			if !applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
				Source:        agentLifecycleSourceToolComplete,
				CorrelationID: event.CorrelationID,
				Status:        next.Status,
				ActivityState: next.ActivityState,
			}) {
				m.rowsDirty = true
				return nil
			}
			if text := summarizeAgentPanelToolEvent(event); text != "" {
				agent.TaskSummary = text
			}
		}
	default:
		return nil
	}
	m.rowsDirty = true
	return nil
}

func (m *Model) ensureAgentForLiveEvent(panelID, rawAgentID, agentType, agentName, pipelineID, taskID, taskName, taskSlug string) string {
	panelID = strings.TrimSpace(panelID)
	rawAgentID = strings.TrimSpace(rawAgentID)
	identity := panelID
	if identity == "" {
		identity = rawAgentID
	}
	if isInternalSidecarAgent(identity, agentType) {
		return ""
	}
	if panelID == "" {
		panelID = rawAgentID
	}
	if panelID == "" {
		return ""
	}
	if resolved := m.resolveAgentID(panelID); resolved != "" && m.agents[resolved] != nil {
		return resolved
	}

	data := map[string]any{}
	if trimmed := strings.TrimSpace(agentType); trimmed != "" {
		data["agent_type"] = trimmed
	}
	if trimmed := strings.TrimSpace(agentName); trimmed != "" {
		data["agent_name"] = trimmed
	}
	if trimmed := strings.TrimSpace(pipelineID); trimmed != "" {
		data["pipeline_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(taskID); trimmed != "" {
		data["task_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(taskName); trimmed != "" {
		data["task_name"] = trimmed
	}
	if trimmed := strings.TrimSpace(taskSlug); trimmed != "" {
		data["task_slug"] = trimmed
	}
	if rawAgentID != "" && rawAgentID != panelID {
		data["runtime_agent_id"] = rawAgentID
	}

	ev := msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			EventType:  events.EventTypeAgentAction,
			Timestamp:  time.Now(),
			AgentID:    panelID,
			Data:       data,
			Visibility: events.VisibilityUser,
		},
	}
	agentID := m.ensureAgent(panelID, ev)
	m.rememberAgentAliases(agentID, rawAgentID, ev)
	return agentID
}

func (m *Model) canonicalizeAgentID(agentID string, ev msg.ActivityEventMsg) string {
	candidateID := firstNonEmptyTrimmed(
		extractString(ev.Event.Data, "canonical_agent_id"),
		agentID,
		extractString(ev.Event.Data, "runtime_agent_id"),
	)
	agentType, pipelineID := m.inferActivityAgentIdentity(candidateID, ev)
	if panelID := canonicalPipelineAgentID(agentType, pipelineID); panelID != "" {
		return m.resolveAgentID(panelID)
	}
	return m.resolveAgentID(candidateID)
}

// bindIdentityToRow routes a typed *AgentIdentity to its panel row.
// Panel rows are singletons per agent Kind (for standalone/knowledge
// agents) and singletons per (Kind, PipelineID) (for pipeline workers).
// Replicas do NOT get their own rows — they bind to the same row as
// their canonical parent and are counted via AgentState.ActiveReplicas.
//
// Cases:
//
//  1. UID already indexed in byUID → update the row's Identity +
//     Generation + ModelID in place. Absorbs SwapModel transitions
//     (same UID, bumped Generation, new Model) without creating
//     duplicates.
//
//  2. Replica (Owner != nil) whose parent UID is indexed → bind to
//     the parent's row. The row's ActiveReplicas counter tracks the
//     current active set by UID; map in byUID points the replica's
//     UID at the parent's row key so subsequent events route correctly.
//
//  3. Canonical agent (or replica whose parent is not yet indexed) →
//     find an existing row for the same Kind (seed placeholder or
//     activated row). For pipeline workers, match on (Kind, PipelineID).
//     If found, bind this Identity to that row. Otherwise create a new
//     row keyed on Kind.String() (or the canonical pipeline form).
//
// Returns the panel row key string (the map key in m.agents).
func (m *Model) bindIdentityToRow(id *identity.AgentIdentity) string {
	if id == nil {
		return ""
	}
	uid := id.UID()

	// Case 1: UID already indexed — in-place update (handles SwapModel).
	if rowID, ok := m.byUID[uid]; ok {
		if agent, exists := m.agents[rowID]; exists && agent != nil {
			m.applyIdentityToAgent(agent, id)
		}
		return rowID
	}

	// Case 2: Replica whose parent is indexed — bind to parent row.
	if owner := id.Owner(); owner != nil {
		if parentRow, ok := m.byUID[owner.UID]; ok {
			if agent, exists := m.agents[parentRow]; exists && agent != nil {
				m.trackReplicaOnParent(agent, id)
				m.byUID[uid] = parentRow
				return parentRow
			}
		}
	}

	// Case 3: Bind to a matching row by Kind (seed or activated).
	// Pipeline workers are singleton per (Kind, PipelineID); singletons
	// and knowledge agents are singleton per Kind.
	kindKey := id.Kind().String()
	if row := m.matchingRowForKind(kindKey); row != nil {
		m.applyIdentityToAgent(row, id)
		m.byUID[uid] = row.ID
		if id.Owner() != nil {
			m.trackReplicaOnParent(row, id)
		}
		return row.ID
	}

	// No existing row — create a fresh canonical row keyed on Kind.
	rowID := kindKey
	if rowID == "" {
		rowID = string(uid)
	}
	if existing, ok := m.agents[rowID]; ok && existing != nil {
		// Race with another path that created the row. Bind and return.
		m.applyIdentityToAgent(existing, id)
		m.byUID[uid] = rowID
		return rowID
	}
	agent := m.newAgentFromIdentity(rowID, id)
	m.agents[rowID] = agent
	if _, exists := m.streams[rowID]; !exists {
		m.streams[rowID] = NewAgentEventStream()
	}
	if !slices.Contains(m.order, rowID) {
		m.order = append(m.order, rowID)
	}
	m.byUID[uid] = rowID
	m.rowsDirty = true
	return rowID
}

// matchingRowForKind finds the canonical singleton row matching the
// given kind. Prefers an existing row with Identity already bound
// (active); falls back to an unbound seeded placeholder. Returns
// nil when no row matches.
//
// Pipeline workers are NOT matched here — their per-(Kind, PipelineID)
// grouping is resolved separately when the caller supplies a
// pipeline id (not yet plumbed through the typed path; pipeline
// events still flow through the legacy string route).
func (m *Model) matchingRowForKind(kindKey string) *AgentState {
	kindKey = strings.TrimSpace(kindKey)
	if kindKey == "" {
		return nil
	}
	// Pipeline workers are singleton per (Kind, PipelineID); the
	// typed path does not yet know the pipeline id so skip them here.
	if isPipelineAgentType(kindKey) {
		return nil
	}
	// Prefer an already-bound row (Identity != nil) with the same Kind.
	for _, rowID := range m.order {
		agent, ok := m.agents[rowID]
		if !ok || agent == nil {
			continue
		}
		if !strings.EqualFold(agent.AgentType, kindKey) {
			continue
		}
		if agent.Identity != nil {
			return agent
		}
	}
	// Otherwise use the first seeded placeholder with the same Kind.
	for _, rowID := range m.order {
		agent, ok := m.agents[rowID]
		if !ok || agent == nil || agent.Identity != nil {
			continue
		}
		if strings.EqualFold(agent.AgentType, kindKey) {
			return agent
		}
	}
	return nil
}

// trackReplicaOnParent records a replica UID on its parent's row.
// Uses AgentState.replicaUIDs as a set so the same replica is not
// double-counted, and mirrors the cardinality into
// AgentState.ActiveReplicas for display. Never increments past the
// number of distinct replica UIDs observed.
func (m *Model) trackReplicaOnParent(parent *AgentState, replica *identity.AgentIdentity) {
	if parent == nil || replica == nil || !replica.IsReplica() {
		return
	}
	if parent.replicaUIDs == nil {
		parent.replicaUIDs = make(map[identity.UID]struct{}, 2)
	}
	if _, exists := parent.replicaUIDs[replica.UID()]; exists {
		return
	}
	parent.replicaUIDs[replica.UID()] = struct{}{}
	parent.ActiveReplicas = len(parent.replicaUIDs)
	m.rowsDirty = true
}

// applyIdentityToAgent binds a *AgentIdentity onto an AgentState.
// Updates the Identity pointer, Generation, AgentType, Category,
// ModelID/ProviderID, and Name when appropriate. Used by both
// fresh-bind (seeded placeholder → concrete identity) and
// SwapModel-update (existing row → bumped generation).
//
// Replicas do not overwrite the canonical parent's Identity — the
// parent row's Identity tracks the canonical agent (Owner == nil).
// Generation bumps on the canonical agent (SwapModel) do update.
func (m *Model) applyIdentityToAgent(agent *AgentState, id *identity.AgentIdentity) {
	if agent == nil || id == nil {
		return
	}
	// Only the canonical agent claims the Identity pointer. A replica
	// event routed to the parent row must not clobber the parent's
	// canonical identity.
	if id.IsReplica() && agent.Identity != nil {
		return
	}
	priorGen := agent.Generation
	agent.Identity = id
	agent.Generation = uint64(id.Generation())
	kind := id.Kind().String()
	if agent.AgentType == "" {
		agent.AgentType = kind
	}
	if agent.Category == "" {
		agent.Category = resolveAgentCategory(kind, agent.PipelineID)
	}
	if model := string(id.Model()); model != "" {
		agent.ModelID = model
		if agent.ProviderID == "" {
			agent.ProviderID = deriveProvider(model)
		}
	}
	if agent.Name == "" {
		agent.Name = agentDisplayNameFromIdentity(id)
	}
	if priorGen != agent.Generation {
		m.rowsDirty = true
	}
}

// newAgentFromIdentity constructs an AgentState from a typed Identity
// for canonical agents that have no existing seed or activated row.
// Replicas never call this — they always bind to an existing parent
// row via trackReplicaOnParent.
func (m *Model) newAgentFromIdentity(rowID string, id *identity.AgentIdentity) *AgentState {
	kind := id.Kind().String()
	agent := &AgentState{
		ID:         rowID,
		RoutingID:  rowID,
		Name:       agentDisplayNameFromIdentity(id),
		AgentType:  kind,
		Category:   resolveAgentCategory(kind, ""),
		Status:     StatusIdle,
		Identity:   id,
		Generation: uint64(id.Generation()),
		ModelID:    string(id.Model()),
	}
	if agent.ModelID != "" {
		agent.ProviderID = deriveProvider(agent.ModelID)
	}
	return agent
}

// agentDisplayNameFromIdentity returns the human-friendly name for a
// canonical (non-replica) identity: the agent Kind title-cased.
// Replicas never claim a row's display name — they bind to the
// parent row whose Name is already set.
func agentDisplayNameFromIdentity(id *identity.AgentIdentity) string {
	if id == nil {
		return ""
	}
	kind := id.Kind().String()
	if kind == "" {
		return ""
	}
	// Title-case "guide" → "Guide", "inspector-pipeline" → "Inspector-pipeline".
	return strings.ToUpper(kind[:1]) + kind[1:]
}

func (m *Model) resolveAgentID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	if _, ok := m.agents[agentID]; ok {
		return agentID
	}
	seen := make(map[string]struct{}, 4)
	current := agentID
	for current != "" {
		if _, ok := seen[current]; ok {
			break
		}
		seen[current] = struct{}{}
		next, ok := m.aliases[current]
		if !ok || strings.TrimSpace(next) == "" || next == current {
			break
		}
		current = strings.TrimSpace(next)
	}
	return current
}

func (m *Model) rememberAgentAliases(canonicalID, rawAgentID string, ev msg.ActivityEventMsg) {
	canonicalID = strings.TrimSpace(canonicalID)
	rawAgentID = strings.TrimSpace(rawAgentID)
	if canonicalID == "" {
		return
	}
	m.aliases[canonicalID] = canonicalID
	if agent := m.agents[canonicalID]; agent != nil {
		if runtimeID := strings.TrimSpace(extractString(ev.Event.Data, "runtime_agent_id")); runtimeID != "" {
			agent.RoutingID = runtimeID
		} else if rawAgentID != "" && rawAgentID != canonicalID {
			agent.RoutingID = rawAgentID
		} else if agent.RoutingID == "" {
			agent.RoutingID = canonicalID
		}
	}
	if rawAgentID != "" &&
		shouldRememberRawAgentAlias(rawAgentID) &&
		(!isProtectedCanonicalSingletonID(rawAgentID) || rawAgentID == canonicalID) {
		m.aliases[rawAgentID] = canonicalID
	}
	if runtimeID := extractString(ev.Event.Data, "runtime_agent_id"); runtimeID != "" {
		m.aliases[runtimeID] = canonicalID
	}
}

func shouldRememberRawAgentAlias(rawAgentID string) bool {
	rawAgentID = strings.TrimSpace(rawAgentID)
	if rawAgentID == "" {
		return false
	}
	if _, _, ok := parseTaskScopedPipelineAgentID(rawAgentID); ok {
		return true
	}
	if _, _, ok := parseCanonicalPipelinePanelAgentID(rawAgentID); ok {
		return true
	}
	return !isPipelineAgentType(rawAgentID)
}

func (m *Model) rehomeDuplicateAgentRows(canonicalID string, ev msg.ActivityEventMsg) {
	canonicalID = strings.TrimSpace(canonicalID)
	if canonicalID == "" {
		return
	}
	for _, candidateID := range []string{
		strings.TrimSpace(ev.Event.AgentID),
		extractString(ev.Event.Data, "runtime_agent_id"),
	} {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" || candidateID == canonicalID {
			continue
		}
		if isProtectedCanonicalSingletonID(candidateID) && candidateID != canonicalID {
			continue
		}
		candidate := m.agents[candidateID]
		if candidate == nil {
			continue
		}
		if existing := m.agents[canonicalID]; existing != nil {
			m.absorbDuplicateAgent(candidateID, canonicalID)
			continue
		}
		m.promoteSeededAgent(candidate, canonicalID, ev)
	}
}

// ensureAgent creates an agent entry if it does not exist, respecting the bound.
// Standalone and knowledge agents stay anchored to stable singleton panel rows
// keyed by agent type; concrete runtime IDs are tracked as routing aliases only.
// Pipeline workers stay anchored to their stable task-scoped panel row and only
// update routing metadata when a concrete runtime worker ID is observed.
func (m *Model) ensureAgent(agentID string, ev msg.ActivityEventMsg) string {
	if existing, exists := m.agents[agentID]; exists {
		m.reconcileExistingAgent(existing, ev)
		return agentID
	}

	agentType, pipelineID := m.inferActivityAgentIdentity(agentID, ev)
	if isInternalSidecarAgent(agentID, agentType) {
		return ""
	}
	if isPipelineAgentType(agentType) && strings.TrimSpace(pipelineID) == "" {
		return ""
	}

	// Standalone and knowledge agents are singleton rows per type. Pipeline
	// rows are singleton per (type, pipeline_id). Standalone placeholders are
	// re-keyed on first activation; pipeline rows stay stable and only refresh
	// their routing metadata when a concrete runtime worker ID is observed.
	// This covers:
	//  1. Singleton rows (standalone/knowledge) remain stable across activations.
	//  2. Pipeline workers keep their stable task-scoped row IDs.
	category := resolveAgentCategory(agentType, pipelineID)
	if category != "pipeline" {
		pipelineID = ""
	}
	if shouldUseCanonicalSingletonRow(agentType, category) {
		agentID = strings.TrimSpace(agentType)
	}
	if agentType != "" {
		var existing *AgentState
		switch category {
		case "pipeline":
			existing = m.findPipelineAgent(agentType, pipelineID)
		case "standalone", "knowledge":
			existing = m.findAgentByType(agentType)
		}
		if existing != nil {
			m.reconcileExistingAgent(existing, ev)
			return existing.ID
		}
	}

	agentName := extractString(ev.Event.Data, "agent_name")
	if agentName == "" {
		agentName = agentID
	}

	modelID := defaultModelForAgent(agentType)
	models := m.modelsForAgentType(agentType)
	if len(models) > 0 {
		modelID = models[0].ID
	}
	providerID := deriveProvider(modelID)
	if m.modelStore != nil {
		entry := m.modelStore.EntryFor(agentType)
		if entry.Model != "" {
			modelID = m.resolveAgentModelID(models, entry.Model)
			providerID = entry.Provider
			if providerID == "" {
				providerID = deriveProvider(modelID)
			}
		}
	}

	agent := &AgentState{
		ID:         agentID,
		RoutingID:  defaultRoutingAgentID(agentID, agentType, pipelineID),
		Name:       agentName,
		AgentType:  agentType,
		Category:   category,
		PipelineID: pipelineID,
		Status:     StatusIdle,
		ModelID:    modelID,
		ProviderID: providerID,
	}
	m.agents[agentID] = agent
	m.streams[agentID] = NewAgentEventStream()
	m.order = append(m.order, agentID)
	m.rowsDirty = true

	// Register as pipeline member, creating the pipeline entry on-demand when
	// activity wins the race with the canonical pipeline-state message.
	if pipelineID != "" {
		pl, ok := m.pipelines[pipelineID]
		if !ok {
			taskID := extractPipelineTaskID(ev.Event.Data)
			if taskID == "" {
				taskID = pipelineID
			}
			status := pipelineStatusFromEvent(ev.Event.Data)
			if status == "" {
				status = "pending"
			}
			pl = &PipelineState{
				ID:     pipelineID,
				TaskID: taskID,
				TaskLabel: preferredPipelineTaskLabel(
					"",
					pipelineTaskLabelFromEvent(ev.Event.Data),
					taskID,
					pipelineID,
				),
				Status:    status,
				CreatedAt: time.Now(),
			}
			m.pipelines[pipelineID] = pl
			m.pipelineOrder = append(m.pipelineOrder, pipelineID)
		}
		if pl != nil {
			applyPipelineActivityState(pl, ev)
			if pl.TaskID == "" {
				pl.TaskID = extractPipelineTaskID(ev.Event.Data)
			}
			pl.TaskLabel = preferredPipelineTaskLabel(
				pl.TaskLabel,
				pipelineTaskLabelFromEvent(ev.Event.Data),
				pl.TaskID,
				pipelineID,
			)
			pl.TaskLabel = preferredPipelineTaskLabel(
				pl.TaskLabel,
				fallbackPipelineTaskLabel(pl.TaskID, pipelineID),
				pl.TaskID,
				pipelineID,
			)
			if !slices.Contains(pl.Members, agentID) {
				pl.Members = append(pl.Members, agentID)
			}
		}
	}

	return agentID
}

func (m *Model) reconcileExistingAgent(agent *AgentState, ev msg.ActivityEventMsg) {
	if agent == nil {
		return
	}
	agentType, pipelineID := m.inferActivityAgentIdentity(agent.ID, ev)
	if isPipelineAgentType(agentType) && strings.TrimSpace(pipelineID) == "" {
		return
	}
	if agentType != "" {
		agent.AgentType = agentType
	}
	category := resolveAgentCategory(agent.AgentType, pipelineID)
	if category != "" {
		agent.Category = category
	}
	if category == "pipeline" && pipelineID != "" {
		agent.PipelineID = pipelineID
	} else if category != "pipeline" {
		agent.PipelineID = ""
	}
	if name := extractString(ev.Event.Data, "agent_name"); name != "" {
		if agent.Name == "" || agent.Name == agent.ID || agent.Name == agent.AgentType {
			agent.Name = name
		}
	}
	if category == "pipeline" && pipelineID != "" {
		m.ensurePipelineMembership(agent.ID, pipelineID, ev)
	}
}

// promoteSeededAgent replaces a seeded placeholder entry with the real agent ID.
// The existing stream is preserved; only the map key and order slice are updated.
func (m *Model) promoteSeededAgent(placeholder *AgentState, realID string, ev msg.ActivityEventMsg) string {
	oldID := placeholder.ID
	realID = strings.TrimSpace(realID)
	if realID == "" {
		m.reconcileExistingAgent(placeholder, ev)
		return oldID
	}

	if oldID == realID {
		m.reconcileExistingAgent(placeholder, ev)
		return realID
	}

	if !isPlaceholderAgentID(oldID, placeholder.AgentType, placeholder.PipelineID) &&
		placeholder.PipelineID != "" &&
		isPlaceholderAgentID(realID, extractString(ev.Event.Data, "agent_type"), extractPipelineID(ev.Event.Data)) {
		m.reconcileExistingAgent(placeholder, ev)
		return oldID
	}

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
	if m.engagedID == oldID {
		m.engagedID = realID
	}
	if m.expanded == oldID {
		m.expanded = realID
	}

	for pipelineID, pl := range m.pipelines {
		replaced := false
		for i, id := range pl.Members {
			if id == oldID {
				pl.Members[i] = realID
				replaced = true
			}
		}
		if replaced {
			m.dedupePipelineMembers(pipelineID)
		}
	}

	m.reconcileExistingAgent(placeholder, ev)
	m.rowsDirty = true
	return realID
}

func (m *Model) rekeyAgentState(oldID, newID string) {
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	if oldID == "" || newID == "" || oldID == newID {
		return
	}
	agent := m.agents[oldID]
	if agent == nil {
		return
	}

	agent.ID = newID
	delete(m.agents, oldID)
	m.agents[newID] = agent

	stream := m.streams[oldID]
	delete(m.streams, oldID)
	if stream == nil {
		stream = NewAgentEventStream()
	}
	m.streams[newID] = stream

	for i, id := range m.order {
		if id == oldID {
			m.order[i] = newID
			break
		}
	}

	if m.engagedID == oldID {
		m.engagedID = newID
	}
	if m.expanded == oldID {
		m.expanded = newID
	}

	for alias, target := range m.aliases {
		if target == oldID {
			m.aliases[alias] = newID
		}
	}
	delete(m.aliases, oldID)
	m.aliases[newID] = newID

	for pipelineID, pl := range m.pipelines {
		if pl == nil {
			continue
		}
		replaced := false
		for i, id := range pl.Members {
			if id == oldID {
				pl.Members[i] = newID
				replaced = true
			}
		}
		if replaced {
			m.dedupePipelineMembers(pipelineID)
		}
	}

	m.rowsDirty = true
}

func (m *Model) ensurePipelineMembership(agentID, pipelineID string, ev msg.ActivityEventMsg) {
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return
	}
	pl, ok := m.pipelines[pipelineID]
	if !ok {
		taskID := extractPipelineTaskID(ev.Event.Data)
		if taskID == "" {
			taskID = pipelineID
		}
		status := pipelineStatusFromEvent(ev.Event.Data)
		if status == "" {
			status = "pending"
		}
		pl = &PipelineState{
			ID:     pipelineID,
			TaskID: taskID,
			TaskLabel: preferredPipelineTaskLabel(
				"",
				pipelineTaskLabelFromEvent(ev.Event.Data),
				taskID,
				pipelineID,
			),
			Status:    status,
			CreatedAt: time.Now(),
		}
		m.pipelines[pipelineID] = pl
		m.pipelineOrder = append(m.pipelineOrder, pipelineID)
	}
	applyPipelineActivityState(pl, ev)
	if pl.TaskID == "" {
		pl.TaskID = extractPipelineTaskID(ev.Event.Data)
	}
	pl.TaskLabel = preferredPipelineTaskLabel(
		pl.TaskLabel,
		pipelineTaskLabelFromEvent(ev.Event.Data),
		pl.TaskID,
		pipelineID,
	)
	pl.TaskLabel = preferredPipelineTaskLabel(
		pl.TaskLabel,
		fallbackPipelineTaskLabel(pl.TaskID, pipelineID),
		pl.TaskID,
		pipelineID,
	)
	if !slices.Contains(pl.Members, agentID) {
		pl.Members = append(pl.Members, agentID)
	}
}

func pipelineStatusFromEvent(data map[string]any) string {
	if data == nil {
		return ""
	}
	status, _ := data["pipeline_status"].(string)
	return strings.TrimSpace(status)
}

func applyPipelineActivityState(pl *PipelineState, ev msg.ActivityEventMsg) {
	if pl == nil {
		return
	}
	if status := pipelineStatusFromEvent(ev.Event.Data); status != "" && pipelineEventIsCurrent(pl, ev.Event.Timestamp) {
		pl.Status = status
		recordPipelineUpdate(pl, ev.Event.Timestamp)
	}
}

func applyPipelineStateUpdate(pl *PipelineState, ps msg.PipelineStateMsg) {
	if pl == nil {
		return
	}
	if ps.Status != "" {
		pl.Status = ps.Status
	}
	if ps.WorkerType != "" {
		pl.WorkerType = ps.WorkerType
	}
	if ps.LoopCount > 0 || pl.LoopCount == 0 {
		pl.LoopCount = ps.LoopCount
	}
	if ps.MaxLoops > 0 || pl.MaxLoops == 0 {
		pl.MaxLoops = ps.MaxLoops
	}
	recordPipelineUpdate(pl, ps.Timestamp)
}

func pipelineEventIsCurrent(pl *PipelineState, ts time.Time) bool {
	if pl == nil || pl.UpdatedAt.IsZero() || ts.IsZero() {
		return true
	}
	return !ts.Before(pl.UpdatedAt)
}

func recordPipelineUpdate(pl *PipelineState, ts time.Time) {
	if pl == nil {
		return
	}
	if ts.IsZero() {
		if pl.UpdatedAt.IsZero() {
			pl.UpdatedAt = time.Now()
		}
		return
	}
	if pl.UpdatedAt.IsZero() || ts.After(pl.UpdatedAt) {
		pl.UpdatedAt = ts
	}
}

func (m *Model) dedupePipelineMembers(pipelineID string) {
	pl := m.pipelines[pipelineID]
	if pl == nil || len(pl.Members) < 2 {
		return
	}
	seen := make(map[string]struct{}, len(pl.Members))
	filtered := pl.Members[:0]
	for _, id := range pl.Members {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		filtered = append(filtered, id)
	}
	pl.Members = filtered
}

func isPlaceholderAgentID(agentID, agentType, pipelineID string) bool {
	agentID = strings.TrimSpace(agentID)
	agentType = strings.TrimSpace(agentType)
	pipelineID = strings.TrimSpace(pipelineID)
	if agentID == "" {
		return false
	}
	if _, _, ok := parseTaskScopedPipelineAgentID(agentID); ok {
		return true
	}
	if agentType != "" && agentID == agentType {
		return true
	}
	return agentID == canonicalPipelineAgentID(agentType, pipelineID)
}

func (m *Model) absorbDuplicateAgent(srcID, dstID string) {
	src := m.agents[srcID]
	dst := m.agents[dstID]
	if src == nil || dst == nil || srcID == dstID {
		return
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.AgentType == "" {
		dst.AgentType = src.AgentType
	}
	if dst.Category == "" {
		dst.Category = src.Category
	}
	if dst.PipelineID == "" {
		dst.PipelineID = src.PipelineID
	}
	if dst.TaskSummary == "" {
		dst.TaskSummary = src.TaskSummary
	}
	if !dst.toolSummaryPinned && src.toolSummaryPinned {
		dst.toolSummaryPinned = true
		dst.pinnedToolCallKey = src.pinnedToolCallKey
	}
	if dst.ContextUsage == 0 {
		dst.ContextUsage = src.ContextUsage
	}
	if dst.ActiveReplicas == 0 {
		dst.ActiveReplicas = src.ActiveReplicas
	}
	if dst.MaxReplicas == 0 {
		dst.MaxReplicas = src.MaxReplicas
	}
	if dst.QueuedRequests == 0 {
		dst.QueuedRequests = src.QueuedRequests
	}
	if dst.MaxQueuedRequests == 0 {
		dst.MaxQueuedRequests = src.MaxQueuedRequests
	}
	if dst.Status == StatusIdle && src.Status != StatusIdle {
		dst.Status = src.Status
	}
	if srcStream := m.streams[srcID]; srcStream != nil {
		dstStream := m.streams[dstID]
		if dstStream == nil {
			dstStream = NewAgentEventStream()
			m.streams[dstID] = dstStream
		}
		for _, event := range srcStream.Last(srcStream.Len()) {
			dstStream.Push(event)
		}
	}
	delete(m.agents, srcID)
	delete(m.streams, srcID)
	delete(m.aliases, srcID)
	for i, id := range m.order {
		if id == srcID {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	for pipelineID, pl := range m.pipelines {
		if pl == nil {
			continue
		}
		for i, id := range pl.Members {
			if id == srcID {
				pl.Members[i] = dstID
			}
		}
		m.dedupePipelineMembers(pipelineID)
	}
	if m.engagedID == srcID {
		m.engagedID = dstID
	}
	if m.expanded == srcID {
		m.expanded = dstID
	}
	m.rowsDirty = true
}

// findAgentByType returns the first agent entry with the given type, or nil.
// Used to locate existing standalone entries for re-keying on re-activation.
func (m *Model) findAgentByType(agentType string) *AgentState {
	for _, agent := range m.agents {
		if agent.AgentType == agentType {
			return agent
		}
	}
	return nil
}

func (m *Model) findPipelineAgent(agentType, pipelineID string) *AgentState {
	for _, agent := range m.agents {
		if agent.AgentType == agentType && agent.PipelineID == pipelineID {
			return agent
		}
	}
	return nil
}

func (m *Model) uniquePipelineAgentForType(agentType string) *AgentState {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		return nil
	}
	var match *AgentState
	for _, agent := range m.agents {
		if agent == nil || agent.AgentType != agentType || strings.TrimSpace(agent.PipelineID) == "" {
			continue
		}
		if match != nil && match.PipelineID != agent.PipelineID {
			return nil
		}
		match = agent
	}
	return match
}

func (m *Model) uniqueKnownPipelineID() string {
	pipelineID := ""
	for _, candidateID := range m.pipelineOrder {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" || m.pipelines[candidateID] == nil {
			continue
		}
		if pipelineID != "" && pipelineID != candidateID {
			return ""
		}
		pipelineID = candidateID
	}
	return pipelineID
}

func (m *Model) inferActivityAgentIdentity(rawAgentID string, ev msg.ActivityEventMsg) (agentType, pipelineID string) {
	agentType = extractString(ev.Event.Data, "agent_type")
	pipelineID = extractPipelineID(ev.Event.Data)
	rawAgentID = strings.TrimSpace(rawAgentID)

	if scopedPipelineID, scopedAgentType, ok := parseTaskScopedPipelineAgentID(rawAgentID); ok {
		if pipelineID == "" {
			pipelineID = scopedPipelineID
		}
		if agentType == "" {
			agentType = scopedAgentType
		}
	}
	if canonicalPipelineID, canonicalAgentType, ok := parseCanonicalPipelinePanelAgentID(rawAgentID); ok {
		if pipelineID == "" {
			pipelineID = canonicalPipelineID
		}
		if agentType == "" {
			agentType = canonicalAgentType
		}
	}

	if resolvedID := m.resolveAgentID(rawAgentID); resolvedID != "" {
		if existing := m.agents[resolvedID]; existing != nil {
			if agentType == "" {
				agentType = existing.AgentType
			}
			if pipelineID == "" {
				pipelineID = strings.TrimSpace(existing.PipelineID)
			}
		}
	}

	if agentType == "" && isPipelineAgentType(rawAgentID) {
		agentType = rawAgentID
	}
	if pipelineID == "" && isPipelineAgentType(agentType) {
		if existing := m.uniquePipelineAgentForType(agentType); existing != nil {
			pipelineID = strings.TrimSpace(existing.PipelineID)
		} else {
			pipelineID = m.uniqueKnownPipelineID()
		}
	}

	return strings.TrimSpace(agentType), strings.TrimSpace(pipelineID)
}

// AgentTypeOf returns the agent type for a given canonical ID.
// Returns "" when the ID is not known to the panel.
func (m *Model) AgentTypeOf(agentID string) string {
	agentID = m.resolveAgentID(agentID)
	if a, ok := m.agents[agentID]; ok {
		return a.AgentType
	}
	return ""
}

// ModelIDOf returns the currently assigned model ID for a given agent.
// Returns "" when the agent is not known to the panel.
func (m *Model) ModelIDOf(agentID string) string {
	agentID = m.resolveAgentID(agentID)
	if a, ok := m.agents[agentID]; ok {
		return a.ModelID
	}
	return ""
}

// SyncContextUsage updates the displayed context usage for an existing agent row.
// It intentionally does not create or re-key rows; callers should pass the
// canonical panel agent ID they want to refresh from real token telemetry.
func (m *Model) SyncContextUsage(agentID string, usage float64) {
	agentID = m.resolveAgentID(agentID)
	if agentID == "" {
		return
	}
	agent, ok := m.agents[agentID]
	if !ok || agent == nil {
		return
	}
	switch {
	case usage < 0:
		usage = 0
	case usage > 1:
		usage = 1
	}
	agent.ContextUsage = usage
	m.rowsDirty = true
}

// ContextUsageOf returns the current displayed context usage for the given row.
func (m *Model) ContextUsageOf(agentID string) float64 {
	agentID = m.resolveAgentID(agentID)
	if a, ok := m.agents[agentID]; ok && a != nil {
		return a.ContextUsage
	}
	return 0
}

// updateAgentStatus applies the table-driven EventType->AgentStatus mapping.
func (m *Model) updateAgentStatus(agentID string, ev msg.ActivityEventMsg) {
	agent, ok := m.agents[agentID]
	if !ok {
		return
	}

	next := *agent
	if status, found := handoffActivityStatus(ev); found {
		next.Status = status
	} else if status, found := eventTypeToStatus[ev.Event.EventType]; found {
		next.Status = status
	}
	next.ActivityState = inferUIStateFromActivity(&next, ev)
	if active, queued, ok := extractReplicaLoad(ev.Event.Data); ok && (active > 0 || queued > 0) {
		next.Status = StatusThinking
		if next.ActivityState == events.AgentUIStateNone {
			next.ActivityState = events.AgentUIStateSearching
		}
	}
	if isActiveStatus(next.Status) && shouldIgnoreStaleAgentCorrelation(agent, ev.Event.CorrelationID) {
		return
	}
	applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
		Source:        agentLifecycleSourceActivity,
		CorrelationID: ev.Event.CorrelationID,
		Status:        next.Status,
		ActivityState: next.ActivityState,
	})
	trackAgentActivityCorrelation(agent, ev)
	m.updateAgentMetadata(agent, ev)
}

// updateAgentMetadata extracts task summary and context usage from event data.
func (m *Model) updateAgentMetadata(agent *AgentState, ev msg.ActivityEventMsg) {
	if summary := summarizeAgentPanelActivity(ev.Event); summary != "" && !agent.toolSummaryPinned {
		agent.TaskSummary = summary
	}
	if active, queued, ok := extractReplicaLoad(ev.Event.Data); ok {
		agent.ActiveReplicas = active
		agent.QueuedRequests = queued
		agent.MaxReplicas, _ = extractInt(ev.Event.Data, "max_replicas")
		agent.MaxQueuedRequests, _ = extractInt(ev.Event.Data, "max_queued_requests")
	}

	handoffState := extractString(ev.Event.Data, "handoff_state")
	// Panel context usage is the active LLM context-window occupancy. Handoff
	// bridge context_usage describes handoff readiness/utilization, which is a
	// different metric and should not overwrite the displayed context-window %.
	if handoffState == "completed" {
		agent.ContextUsage = 0
	}
	if tokens, ok := extractInt(ev.Event.Data, "context_tokens"); ok && tokens == 0 {
		agent.ContextUsage = 0
	}
}

func extractReplicaLoad(data map[string]any) (int, int, bool) {
	active, activeOK := extractInt(data, "active_replicas")
	queued, queuedOK := extractInt(data, "queued_requests")
	if !activeOK && !queuedOK {
		return 0, 0, false
	}
	return active, queued, true
}

func knowledgeAgentHasPendingReplicaLoad(agent *AgentState) bool {
	if agent == nil || agent.Category != "knowledge" {
		return false
	}
	return agent.ActiveReplicas > 0 || agent.QueuedRequests > 0
}

func handoffActivityStatus(ev msg.ActivityEventMsg) (AgentStatus, bool) {
	switch extractString(ev.Event.Data, "handoff_state") {
	case "recommended", "triggered", "executing", "shifting":
		return StatusHandoff, true
	case "completed":
		return StatusWaiting, true
	case "failed":
		return StatusError, true
	default:
		return 0, false
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
	canonicalID := strings.TrimSpace(ps.PipelineID)
	if canonicalID == "" {
		canonicalID = strings.TrimSpace(ps.TaskID)
	}
	if canonicalID == "" {
		canonicalID = strings.TrimSpace(ps.RuntimePipelineID)
	}
	if canonicalID == "" {
		return nil
	}
	if runtimeID := strings.TrimSpace(ps.RuntimePipelineID); runtimeID != "" && runtimeID != canonicalID {
		m.rehomePipelineState(runtimeID, canonicalID)
	}

	pl, exists := m.pipelines[canonicalID]
	if !exists {
		pl = &PipelineState{
			ID:        canonicalID,
			CreatedAt: time.Now(),
		}
		m.pipelines[canonicalID] = pl
		m.pipelineOrder = append(m.pipelineOrder, canonicalID)

		// Populate members from agents that arrived before the pipeline.
		for _, agentID := range m.order {
			if a := m.agents[agentID]; a != nil && a.PipelineID == canonicalID {
				pl.Members = append(pl.Members, agentID)
			}
		}
	}

	if ps.TaskID != "" {
		pl.TaskID = ps.TaskID
	}
	if ps.TaskSlug != "" {
		pl.TaskSlug = ps.TaskSlug
	}
	if ps.TaskLabel != "" {
		pl.TaskLabel = ps.TaskLabel
	} else {
		pl.TaskLabel = preferredPipelineTaskLabel(
			pl.TaskLabel,
			fallbackPipelineTaskLabel(pl.TaskID, canonicalID),
			pl.TaskID,
			canonicalID,
		)
	}
	applyPipelineStateUpdate(pl, ps)
	m.rowsDirty = true

	return nil
}

func (m *Model) handleClaimsProjection(proj msg.ClaimsProjectionMsg) tea.Cmd {
	taskID := strings.TrimSpace(proj.TaskID)
	if taskID == "" {
		return nil
	}
	for _, pl := range m.pipelines {
		if pl.TaskID == taskID || pl.ID == taskID {
			pl.ClaimsAccepted = proj.AcceptedCount
			pl.ClaimsTotalCount = proj.TotalClaims
			m.rowsDirty = true
			return nil
		}
	}
	return nil
}

func (m *Model) rehomePipelineState(sourceID, targetID string) {
	sourceID = strings.TrimSpace(sourceID)
	targetID = strings.TrimSpace(targetID)
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return
	}

	src := m.pipelines[sourceID]
	if src == nil {
		return
	}

	dst := m.pipelines[targetID]
	if dst == nil {
		delete(m.pipelines, sourceID)
		src.ID = targetID
		m.pipelines[targetID] = src
		for i, id := range m.pipelineOrder {
			if id == sourceID {
				m.pipelineOrder[i] = targetID
				break
			}
		}
		if m.expanded == sourceID {
			m.expanded = targetID
		}
		if m.collapsedPipelines[sourceID] {
			m.collapsedPipelines[targetID] = true
			delete(m.collapsedPipelines, sourceID)
		}
		dst = src
	} else {
		m.mergePipelineState(dst, src)
		delete(m.pipelines, sourceID)
		delete(m.collapsedPipelines, sourceID)
		for i := 0; i < len(m.pipelineOrder); i++ {
			if m.pipelineOrder[i] != sourceID {
				continue
			}
			m.pipelineOrder = append(m.pipelineOrder[:i], m.pipelineOrder[i+1:]...)
			i--
		}
		if m.expanded == sourceID {
			m.expanded = targetID
		}
	}

	agentIDs := make([]string, 0, len(m.agents))
	for agentID := range m.agents {
		agentIDs = append(agentIDs, agentID)
	}
	for _, agentID := range agentIDs {
		agent := m.agents[agentID]
		if agent == nil || agent.PipelineID != sourceID {
			continue
		}
		agent.PipelineID = targetID
		canonicalAgentID := canonicalPipelineAgentID(agent.AgentType, targetID)
		if canonicalAgentID == "" || agent.ID == canonicalAgentID {
			continue
		}
		if existing := m.agents[canonicalAgentID]; existing != nil && existing != agent {
			m.absorbDuplicateAgent(agent.ID, canonicalAgentID)
			continue
		}
		m.rekeyAgentState(agent.ID, canonicalAgentID)
	}
	for _, variant := range m.variants {
		if variant != nil && variant.PipelineID == sourceID {
			variant.PipelineID = targetID
		}
	}
	m.dedupePipelineMembers(targetID)
	m.rowsDirty = true
}

func (m *Model) mergePipelineState(dst, src *PipelineState) {
	if dst == nil || src == nil || dst == src {
		return
	}
	if dst.TaskID == "" {
		dst.TaskID = src.TaskID
	}
	dst.TaskLabel = preferredPipelineTaskLabel(
		dst.TaskLabel,
		src.TaskLabel,
		firstNonEmptyTrimmed(dst.TaskID, src.TaskID),
		firstNonEmptyTrimmed(dst.ID, src.ID),
	)
	if dst.Status == "" {
		dst.Status = src.Status
	}
	if dst.LoopCount == 0 && src.LoopCount > 0 {
		dst.LoopCount = src.LoopCount
	}
	if dst.MaxLoops == 0 && src.MaxLoops > 0 {
		dst.MaxLoops = src.MaxLoops
	}
	if dst.WorkerType == "" {
		dst.WorkerType = src.WorkerType
	}
	if dst.CreatedAt.IsZero() || (!src.CreatedAt.IsZero() && src.CreatedAt.Before(dst.CreatedAt)) {
		dst.CreatedAt = src.CreatedAt
	}
	dst.Members = append(dst.Members, src.Members...)
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
		applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
			Source:        agentLifecycleSourceDemotion,
			Status:        StatusIdle,
			ActivityState: events.AgentUIStateNone,
		})
	}
}

// DemoteAgent transitions a specific agent to StatusIdle if it is currently
// in an active status. Used on handoff to clear the routing agent (e.g. guide)
// without disturbing other concurrent active agents.
func (m *Model) DemoteAgent(agentID string) {
	agentID = m.resolveAgentID(agentID)
	agent, ok := m.agents[agentID]
	if !ok {
		agent = m.findAgentByType(agentID)
	}
	if agent != nil {
		if before := agent.Status; before != StatusIdle || agent.ActivityState != defaultUIStateForStatus(agent) {
			applyAgentLifecycleUpdate(agent, agentLifecycleUpdate{
				Source:        agentLifecycleSourceDemotion,
				Status:        StatusIdle,
				ActivityState: events.AgentUIStateNone,
			})
		}
		m.rowsDirty = true
	}
}

// AdvanceDotFrame increments the filling-circle dot animation frame counter.
// Called once per decor tick when active agents exist.
func (m *Model) AdvanceDotFrame() {
	m.dotFrame = (m.dotFrame + 1) % dotAnimFrameCount
	m.DecrementSelectorFlash()
}

const idleDecorPhaseStep = 600 * time.Millisecond

// AdvanceDecor updates the panel's decor state and reports whether a repaint is
// visually necessary at the given tick time.
func (m *Model) AdvanceDecor(now time.Time) bool {
	if m == nil {
		return false
	}
	hasActiveAgents := m.HasActiveAgent()
	if m.selector.flash > 0 {
		if hasActiveAgents {
			m.AdvanceDotFrame()
		} else {
			m.DecrementSelectorFlash()
		}
		return true
	}
	hasActivePipelines := false
	for _, pl := range m.pipelines {
		if !isTerminalPipelineStatus(pl.Status) {
			hasActivePipelines = true
			break
		}
	}
	if hasActiveAgents {
		m.AdvanceDotFrame()
		return true
	}
	if hasActivePipelines {
		return true
	}
	if len(m.agents) == 0 {
		return false
	}
	bucket := int64(now.Sub(m.shimmerStart) / idleDecorPhaseStep)
	if bucket == m.idleDecorBucket {
		return false
	}
	m.idleDecorBucket = bucket
	return true
}

// NextIdleDecorDelay returns the time until the next visible idle shimmer
// phase change. Active animations and selector flash are handled elsewhere.
func (m *Model) NextIdleDecorDelay(now time.Time) time.Duration {
	if m == nil || len(m.agents) == 0 || m.HasActiveAgent() || m.selector.flash > 0 {
		return 0
	}
	elapsed := now.Sub(m.shimmerStart)
	bucket := elapsed / idleDecorPhaseStep
	next := m.shimmerStart.Add((bucket + 1) * idleDecorPhaseStep)
	delay := next.Sub(now)
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}

// NeedsDecorTick reports whether the agent panel has any decor-driven
// animation demand. Idle agents keep their subdued shimmer, so the presence
// of agents is enough to require low-frequency decor updates.
func (m *Model) NeedsDecorTick() bool {
	if len(m.agents) > 0 {
		return true
	}
	if m.selector.flash > 0 {
		return true
	}
	for _, pl := range m.pipelines {
		if !isTerminalPipelineStatus(pl.Status) {
			return true
		}
	}
	return false
}

// NeedsHighFrequencyDecorTick reports whether the agent panel needs the
// active decor cadence. This is reserved for visibly dynamic states:
// active agents, active pipelines, and selector flash transitions.
func (m *Model) NeedsHighFrequencyDecorTick() bool {
	if m == nil {
		return false
	}
	if m.selector.flash > 0 || m.HasActiveAgent() {
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
	models := m.agentModels(agent)
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
	models := m.agentModels(agent)
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
	models := m.agentModels(agent)
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
// Sections: standalone → knowledge → pipelines.
func (m *Model) rebuildRows() {
	m.rowsDirty = false

	// Categorize agents.
	var standalone, knowledge []string
	pipelineMembers := make(map[string][]string, len(m.pipelineOrder))
	for _, plID := range m.pipelineOrder {
		if pl := m.pipelines[plID]; pl != nil {
			pipelineMembers[plID] = append([]string(nil), pl.Members...)
			continue
		}
		pipelineMembers[plID] = nil
	}

	for _, agentID := range m.order {
		agent := m.agents[agentID]
		if agent == nil {
			continue
		}
		if isPipelineAgentType(agent.AgentType) && strings.TrimSpace(agent.PipelineID) == "" {
			continue
		}
		if agent.PipelineID != "" {
			if _, ok := pipelineMembers[agent.PipelineID]; ok {
				pipelineMembers[agent.PipelineID] = appendUniqueAgentID(pipelineMembers[agent.PipelineID], agentID)
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
	for plID := range pipelineMembers {
		m.sortAgentsByDisplayOrder(pipelineMembers[plID])
	}

	// Estimate capacity: sections + agents + pipelines + variants + spacers.
	capacity := 4 + len(m.order) + len(m.pipelineOrder)*2 + len(m.variants)
	rows := make([]listRow, 0, capacity)

	// Standalone section.
	if len(standalone) > 0 {
		rows = append(rows, listRow{Kind: rowSpacer})
		rows = append(rows, listRow{Kind: rowSection, Label: "global"})
		for _, id := range standalone {
			rows = append(rows, listRow{Kind: rowAgent, ID: id})
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

	// Pipelines section.
	if len(m.pipelineOrder) > 0 {
		rows = append(rows, listRow{Kind: rowSpacer})
		rows = append(rows, listRow{Kind: rowSection, Label: "pipelines"})
		for idx, plID := range m.pipelineOrder {
			pl := m.pipelines[plID]
			if pl == nil {
				continue
			}
			rows = append(rows, listRow{Kind: rowPipeline, ID: plID, Label: pipelineDisplayLabel(pl)})
			if !m.collapsedPipelines[plID] {
				for _, agentID := range pipelineMembers[plID] {
					rows = append(rows, listRow{Kind: rowAgent, ID: agentID, PipelineID: plID})
				}
				for _, v := range m.variantsForPipeline(plID) {
					rows = append(rows, listRow{Kind: rowVariant, ID: v.ID, PipelineID: plID})
				}
			}
			if idx < len(m.pipelineOrder)-1 {
				rows = append(rows, listRow{Kind: rowSpacer})
			}
		}
	}

	m.rows = rows
}

func appendUniqueAgentID(ids []string, agentID string) []string {
	for _, existing := range ids {
		if existing == agentID {
			return ids
		}
	}
	return append(ids, agentID)
}

// variantsForPipeline returns variants belonging to the given pipeline in stable order.
func (m *Model) variantsForPipeline(pipelineID string) []*VariantState {
	var result []*VariantState
	for _, v := range m.variants {
		if v == nil {
			continue
		}
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
		m.pipelineScroll = 0
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
				m.ensureSelectedPipelineRowVisible()
				return
			}
		}
	}
	m.ensureSelectedPipelineRowVisible()
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// ScrollUp scrolls the agent panel up by one step, respecting the current view state.
// Returns true if scroll was consumed. List view returns false because mouse
// scroll should not change agent selection.
func (m *Model) ScrollUp() bool {
	switch m.view {
	case viewList:
		return m.scrollPipelineViewport(-1)
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
	case viewList:
		return m.scrollPipelineViewport(1)
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
// When supportedModels is non-empty, it stores the raw backend model list and
// overrides the static model table.
// persistedModelID, when non-empty and present in the model list, overrides the
// default model so the UI shows the user's persisted selection on boot.
// persistedProviderID is stored alongside the model; when empty, derived from modelID.
// No-op if the agent already exists.
func (m *Model) SeedAgent(id, agentType, name string, supportedModels []ModelEntry, persistedModelID, persistedProviderID string) {
	if isInternalSidecarAgent(id, agentType) {
		return
	}
	id = strings.TrimSpace(id)
	agentType, pipelineID := inferSeedPipelineIdentity(id, agentType)
	if isPipelineAgentType(agentType) && pipelineID == "" {
		return
	}
	if _, exists := m.agents[id]; exists {
		return
	}
	category := resolveAgentCategory(agentType, pipelineID)
	models := m.visibleModelEntries(agentType, supportedModels)
	modelID := m.resolveAgentModelID(models, "")
	if persistedModelID != "" {
		modelID = m.resolveAgentModelID(models, persistedModelID)
	}
	providerID := persistedProviderID
	if providerID == "" {
		providerID = deriveProvider(modelID)
	}
	agent := &AgentState{
		ID:              id,
		RoutingID:       defaultRoutingAgentID(id, agentType, pipelineID),
		Name:            name,
		AgentType:       agentType,
		Category:        category,
		PipelineID:      pipelineID,
		Status:          StatusIdle,
		ModelID:         modelID,
		ProviderID:      providerID,
		SupportedModels: supportedModels,
	}
	m.agents[id] = agent
	m.streams[id] = NewAgentEventStream()
	m.order = append(m.order, id)
	if pipelineID != "" {
		pl, ok := m.pipelines[pipelineID]
		if !ok {
			pl = &PipelineState{
				ID:        pipelineID,
				TaskID:    pipelineID,
				TaskLabel: fallbackPipelineTaskLabel(pipelineID, pipelineID),
				Status:    "pending",
				CreatedAt: time.Now(),
			}
			m.pipelines[pipelineID] = pl
			m.pipelineOrder = append(m.pipelineOrder, pipelineID)
		}
		if !slices.Contains(pl.Members, id) {
			pl.Members = append(pl.Members, id)
		}
	}
	m.rowsDirty = true
}

// modelInList returns true if modelID is present in the model list.
func modelInList(models []ModelEntry, modelID string) bool {
	for _, m := range models {
		if m.ID == modelID {
			return true
		}
	}
	return false
}

// SetEngagedAgent sets the agent the user is conversing with. This is sticky
// across messages until a reroute or explicit @agent override clears it.
// Panel selection is NOT affected — it is always user-driven.
func (m *Model) SetEngagedAgent(agentID string) {
	m.engagedID = strings.ToLower(strings.TrimSpace(agentID))
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

// SelectedTargetAgentID returns the concrete routing target for the selected
// agent row. Pipeline rows keep a stable panel ID, so this prefers the latest
// runtime worker ID when one has been observed.
func (m *Model) SelectedTargetAgentID() string {
	agentID := m.SelectedAgentID()
	if agentID == "" {
		return ""
	}
	return m.ResolveTargetAgentID(agentID)
}

// ResolveTargetAgentID maps a panel-facing agent row ID to the concrete worker
// ID Guide should route to. Standalone rows usually return their own ID;
// pipeline rows prefer the latest runtime worker ID.
func (m *Model) ResolveTargetAgentID(agentID string) string {
	agentID = m.resolveAgentID(strings.TrimSpace(agentID))
	if agentID == "" {
		return ""
	}
	if agent := m.agents[agentID]; agent != nil {
		if strings.TrimSpace(agent.RoutingID) != "" {
			return strings.TrimSpace(agent.RoutingID)
		}
		return defaultRoutingAgentID(agent.ID, agent.AgentType, agent.PipelineID)
	}
	return agentID
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
	m.ensureSelectedPipelineRowVisible()
}

func (m *Model) toggleSelectedPipelineCollapse() {
	m.setSelectedPipelineCollapsed(nil)
}

func (m *Model) collapseSelectedPipeline() {
	collapsed := true
	m.setSelectedPipelineCollapsed(&collapsed)
}

func (m *Model) expandSelectedPipeline() {
	collapsed := false
	m.setSelectedPipelineCollapsed(&collapsed)
}

func (m *Model) setSelectedPipelineCollapsed(forceState *bool) {
	m.ensureRows()
	if len(m.rows) == 0 {
		return
	}
	idx := clampIndex(m.selected, len(m.rows))
	row := m.rows[idx]
	if row.Kind != rowPipeline {
		return
	}

	current := m.collapsedPipelines[row.ID]
	next := !current
	if forceState != nil {
		next = *forceState
	}
	if next == current {
		return
	}
	if next {
		m.collapsedPipelines[row.ID] = true
	} else {
		delete(m.collapsedPipelines, row.ID)
	}
	m.rowsDirty = true
	m.ensureRows()
	m.selectRow(row.Kind, row.ID)
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
	ripple := hasActive // Active agents always shimmer, regardless of focus.

	// Swap group gradient: full prismatic when focused + active, subdued otherwise.
	if m.focused && hasActive {
		m.groupGradient = m.activeGroupGradient
	} else {
		m.groupGradient = m.idleGroupGradient
	}

	anim := AnimState{
		DotFrame:        m.dotFrame,
		Elapsed:         elapsed,
		HasActive:       hasActive,
		Ripple:          ripple,
		RippleGrad:      m.rippleGradient,      // ThinkingGradient for active agents.
		HolographicGrad: m.activeGroupGradient, // Full prismatic for selected active agent.
		NerdFonts:       m.nerdFonts,
	}

	if m.pipelineSectionIndex() >= 0 {
		return m.renderSectionedListView(elapsed, anim)
	}

	return m.renderFlatListView(elapsed, anim)
}

func (m *Model) renderFlatListView(elapsed time.Duration, anim AnimState) string {
	contentHeight := m.height
	rowBudget := max(contentHeight-m.listFooterHeight(contentHeight), 0)
	m.resetLineRowMap()
	lines := make([]string, 0, min(len(m.rows)*2, contentHeight))
	var consumedLines int
	lastVisibleIdx := -1
	start, end := m.visibleListWindow(rowBudget)

	for i := start; i < end; i++ {
		if consumedLines >= rowBudget {
			break
		}
		line, ok, isContent := m.renderListRow(m.rows[i], i, elapsed, anim)
		if !ok {
			continue
		}
		appended := m.appendRenderedRowLines(&lines, line, i, rowBudget-consumedLines)
		if appended == 0 {
			break
		}
		consumedLines += appended
		if isContent || appended > 0 {
			lastVisibleIdx = i
		}
	}

	lines = m.finalizeListLines(lines, contentHeight, lastVisibleIdx, elapsed)
	return strings.Join(lines, "\n")
}

func (m *Model) renderSectionedListView(elapsed time.Duration, anim AnimState) string {
	contentHeight := max(m.height, 0)
	if contentHeight == 0 {
		return ""
	}

	contentBudget := max(contentHeight-m.listFooterHeight(contentHeight), 0)
	preEnd, preludeStart, bodyStart, ok := m.pipelineSectionLayout()
	if !ok {
		return m.renderFlatListView(elapsed, anim)
	}
	preludeCount := bodyStart - preludeStart
	bodyCount := len(m.rows) - bodyStart
	pipelineReserve := m.pipelineReserve(contentBudget, preludeCount, bodyCount)
	preVisible := m.compactPrePipelineRows(preEnd, max(contentBudget-pipelineReserve, 0))
	m.resetLineRowMap()
	lines := make([]string, 0, min(len(m.rows)+1, contentHeight))
	lastVisibleIdx := -1

	for _, rowIdx := range preVisible {
		line, renderOK, isContent := m.renderListRow(m.rows[rowIdx], rowIdx, elapsed, anim)
		if !renderOK {
			continue
		}
		appended := m.appendRenderedRowLines(&lines, line, rowIdx, contentHeight-len(lines))
		if appended == 0 {
			break
		}
		if isContent || appended > 0 {
			lastVisibleIdx = rowIdx
		}
	}

	remainingHeight := contentBudget - len(lines)
	for rowIdx := preludeStart; rowIdx < bodyStart && remainingHeight > 0; rowIdx++ {
		line, renderOK, isContent := m.renderListRow(m.rows[rowIdx], rowIdx, elapsed, anim)
		if !renderOK {
			continue
		}
		appended := m.appendRenderedRowLines(&lines, line, rowIdx, remainingHeight)
		if appended == 0 {
			break
		}
		remainingHeight -= appended
		if isContent || appended > 0 {
			lastVisibleIdx = rowIdx
		}
	}

	if remainingHeight > 0 && bodyCount > 0 {
		layout, layoutOK := m.buildPipelineViewportLayout(bodyStart, bodyCount, remainingHeight)
		if layoutOK {
			m.clampPipelineScroll(layout)
			startLine, endLine := m.visiblePipelineWindow(layout)
			for localIdx, rowHeight := range layout.rowHeights {
				rowStart := layout.rowOffsets[localIdx]
				rowEnd := rowStart + rowHeight
				if rowEnd <= startLine {
					continue
				}
				if rowStart >= endLine {
					break
				}
				rowIdx := layout.bodyStart + localIdx
				line, renderOK, isContent := m.renderListRow(m.rows[rowIdx], rowIdx, elapsed, anim)
				if !renderOK {
					continue
				}
				rowLines := strings.Split(line, "\n")
				sliceStart := max(startLine-rowStart, 0)
				sliceEnd := min(endLine-rowStart, len(rowLines))
				if sliceStart >= sliceEnd {
					continue
				}
				appended := m.appendRenderedLineSlice(&lines, rowLines[sliceStart:sliceEnd], rowIdx, remainingHeight)
				if appended == 0 {
					break
				}
				remainingHeight -= appended
				if isContent || appended > 0 {
					lastVisibleIdx = rowIdx
				}
				if remainingHeight <= 0 {
					break
				}
			}
		} else {
			for localIdx := 0; localIdx < bodyCount && remainingHeight > 0; localIdx++ {
				rowIdx := bodyStart + localIdx
				line, renderOK, isContent := m.renderListRow(m.rows[rowIdx], rowIdx, elapsed, anim)
				if !renderOK {
					continue
				}
				appended := m.appendRenderedRowLines(&lines, line, rowIdx, remainingHeight)
				if appended == 0 {
					break
				}
				remainingHeight -= appended
				if isContent || appended > 0 {
					lastVisibleIdx = rowIdx
				}
			}
		}
	}

	lines = m.finalizeListLines(lines, contentHeight, lastVisibleIdx, elapsed)
	return strings.Join(lines, "\n")
}

func (m *Model) renderListRow(row listRow, rowIndex int, elapsed time.Duration, anim AnimState) (string, bool, bool) {
	selected := rowIndex == m.selected
	phase := elapsed - time.Duration(rowIndex)*groupFlowStep
	activeColor := m.groupGradient.Sample(phase)

	switch row.Kind {
	case rowSection:
		return renderSectionHeader(row.Label, activeColor, m.theme), true, false
	case rowSpacer:
		return renderSpacer(activeColor, m.theme), true, false
	case rowAgent:
		agent, ok := m.agentState(row.ID)
		if !ok {
			return "", false, false
		}
		engaged := m.engagedID != "" && row.ID == m.engagedID
		prefix := renderTreePrefix(pipelinePrefix, activeColor, m.theme)
		return RenderCard(*agent, m.width, m.theme, selected, engaged, prefix, anim), true, true
	case rowPipeline:
		pl, ok := m.pipelineState(row.ID)
		if !ok {
			return "", false, false
		}
		return renderPipelineRow(pl, m.width, elapsed, m.gradient, m.theme, selected, activeColor, anim, m.collapsedPipelines[row.ID]), true, true
	case rowVariant:
		v, ok := m.variantState(row.ID)
		if !ok {
			return "", false, false
		}
		return renderVariantRow(v, m.width, m.theme, selected, activeColor, anim), true, true
	default:
		return "", false, false
	}
}

func (m *Model) resetLineRowMap() {
	if m.height <= 0 {
		m.lineRowMap = nil
		return
	}
	if cap(m.lineRowMap) < m.height {
		m.lineRowMap = make([]int, m.height)
	} else {
		m.lineRowMap = m.lineRowMap[:m.height]
	}
	for i := range m.lineRowMap {
		m.lineRowMap[i] = -1
	}
}

func (m *Model) recordRenderedRow(lineIdx, rowIdx int) {
	if lineIdx < 0 || lineIdx >= len(m.lineRowMap) {
		return
	}
	m.lineRowMap[lineIdx] = rowIdx
}

func (m *Model) appendRenderedRowLines(lines *[]string, rendered string, rowIdx, budget int) int {
	if budget <= 0 {
		return 0
	}
	rowLines := strings.Split(rendered, "\n")
	if len(rowLines) > budget {
		return 0
	}
	for _, rowLine := range rowLines {
		lineIdx := len(*lines)
		*lines = append(*lines, rowLine)
		m.recordRenderedRow(lineIdx, rowIdx)
	}
	return len(rowLines)
}

func (m *Model) appendRenderedLineSlice(lines *[]string, rowLines []string, rowIdx, budget int) int {
	if budget <= 0 || len(rowLines) == 0 {
		return 0
	}
	if len(rowLines) > budget {
		rowLines = rowLines[:budget]
	}
	for _, rowLine := range rowLines {
		lineIdx := len(*lines)
		*lines = append(*lines, rowLine)
		m.recordRenderedRow(lineIdx, rowIdx)
	}
	return len(rowLines)
}

func (m *Model) finalizeListLines(lines []string, targetHeight, lastContentIdx int, elapsed time.Duration) []string {
	if targetHeight <= 0 {
		return lines
	}
	if lastContentIdx >= 0 && len(lines) < targetHeight {
		footerPhase := elapsed - time.Duration(lastContentIdx+1)*groupFlowStep
		footerColor := m.groupGradient.Sample(footerPhase)
		lines = append(lines, renderSectionFooter(m.width, footerColor, m.theme))
	}
	return lines
}

func (m *Model) pipelineSectionIndex() int {
	for idx, row := range m.rows {
		if row.Kind == rowSection && row.Label == "pipelines" {
			return idx
		}
	}
	return -1
}

func (m *Model) pipelineSectionLayout() (preEnd, preludeStart, bodyStart int, ok bool) {
	sectionIdx := m.pipelineSectionIndex()
	if sectionIdx < 0 {
		return 0, 0, 0, false
	}
	preludeStart = sectionIdx
	if preludeStart > 0 && m.rows[preludeStart-1].Kind == rowSpacer {
		preludeStart--
	}
	bodyStart = min(sectionIdx+1, len(m.rows))
	return preludeStart, preludeStart, bodyStart, true
}

func (m *Model) pipelineReserve(contentBudget, preludeCount, bodyCount int) int {
	if contentBudget <= 0 {
		return 0
	}
	reserve := preludeCount
	if bodyCount > 0 {
		reserve++
	}
	if reserve > contentBudget {
		return contentBudget
	}
	return reserve
}

func (m *Model) compactPrePipelineRows(preEnd, budget int) []int {
	if budget <= 0 || preEnd <= 0 {
		return nil
	}
	if preEnd <= budget {
		rows := make([]int, preEnd)
		for i := range preEnd {
			rows[i] = i
		}
		return rows
	}

	type rowBlock struct {
		indices []int
		minKeep int
	}

	var blocks []rowBlock
	for idx := 0; idx < preEnd; {
		start := idx
		idx++
		for idx < preEnd {
			if m.rows[idx].Kind == rowSpacer && idx+1 < preEnd && m.rows[idx+1].Kind == rowSection {
				break
			}
			idx++
		}
		block := rowBlock{
			indices: make([]int, 0, idx-start),
			minKeep: min(idx-start, 2),
		}
		for rowIdx := start; rowIdx < idx; rowIdx++ {
			block.indices = append(block.indices, rowIdx)
		}
		blocks = append(blocks, block)
	}

	total := 0
	lengths := make([]int, len(blocks))
	for i, block := range blocks {
		lengths[i] = len(block.indices)
		total += lengths[i]
	}
	for total > budget {
		trimmed := false
		for blockIdx := len(blocks) - 1; blockIdx >= 0 && total > budget; blockIdx-- {
			if lengths[blockIdx] > blocks[blockIdx].minKeep {
				lengths[blockIdx]--
				total--
				trimmed = true
			}
		}
		if trimmed {
			continue
		}
		for blockIdx := len(blocks) - 1; blockIdx >= 0 && total > budget; blockIdx-- {
			if lengths[blockIdx] > 0 {
				lengths[blockIdx]--
				total--
			}
		}
	}

	rows := make([]int, 0, budget)
	for blockIdx, block := range blocks {
		rows = append(rows, block.indices[:lengths[blockIdx]]...)
	}
	return rows
}

func (m *Model) buildPipelineViewportLayout(bodyStart, bodyCount, viewportHeight int) (pipelineViewportLayout, bool) {
	if bodyCount <= 0 || viewportHeight <= 0 {
		return pipelineViewportLayout{
			bodyStart:      bodyStart,
			viewportHeight: max(viewportHeight, 0),
		}, bodyCount > 0
	}
	layout := pipelineViewportLayout{
		bodyStart:      bodyStart,
		viewportHeight: viewportHeight,
		rowHeights:     make([]int, 0, bodyCount),
		rowOffsets:     make([]int, 0, bodyCount),
	}
	totalLines := 0
	for localIdx := 0; localIdx < bodyCount; localIdx++ {
		rowIdx := bodyStart + localIdx
		layout.rowOffsets = append(layout.rowOffsets, totalLines)
		rowHeight := m.listRowLineCount(m.rows[rowIdx], rowIdx)
		layout.rowHeights = append(layout.rowHeights, rowHeight)
		totalLines += rowHeight
	}
	layout.totalLines = totalLines
	return layout, true
}

func (m *Model) listRowLineCount(row listRow, rowIdx int) int {
	switch row.Kind {
	case rowPipeline:
		if pl, ok := m.pipelineState(row.ID); ok {
			return max(pipelineRowLineCount(pl, m.width), 1)
		}
	case rowAgent:
		selected := rowIdx == m.selected
		return max(cardLineCount(selected), 1)
	}
	return 1
}

func (m *Model) clampPipelineScroll(layout pipelineViewportLayout) {
	if layout.viewportHeight <= 0 {
		m.pipelineScroll = 0
		return
	}
	maxScroll := max(layout.totalLines-layout.viewportHeight, 0)
	if m.pipelineScroll > maxScroll {
		m.pipelineScroll = maxScroll
	}
	if m.pipelineScroll < 0 {
		m.pipelineScroll = 0
	}
}

func (m *Model) visiblePipelineWindow(layout pipelineViewportLayout) (start, end int) {
	if layout.viewportHeight <= 0 || layout.totalLines == 0 {
		return 0, 0
	}
	if layout.totalLines <= layout.viewportHeight {
		m.pipelineScroll = 0
		return 0, layout.totalLines
	}

	maxScroll := layout.totalLines - layout.viewportHeight
	scroll := m.pipelineScroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	m.pipelineScroll = scroll
	return scroll, scroll + layout.viewportHeight
}

func (m *Model) pipelineViewportLayout() (pipelineViewportLayout, bool) {
	m.ensureRows()
	preEnd, preludeStart, pipelineBodyStart, found := m.pipelineSectionLayout()
	if !found {
		return pipelineViewportLayout{}, false
	}
	bodyCount := len(m.rows) - pipelineBodyStart
	if bodyCount == 0 {
		return pipelineViewportLayout{bodyStart: pipelineBodyStart}, true
	}
	preludeCount := pipelineBodyStart - preludeStart
	contentHeight := max(m.height, 0)
	contentBudget := max(contentHeight-m.listFooterHeight(contentHeight), 0)
	preVisible := m.compactPrePipelineRows(preEnd, max(contentBudget-m.pipelineReserve(contentBudget, preludeCount, bodyCount), 0))
	remaining := max(contentBudget-len(preVisible), 0)
	preludeVisible := min(preludeCount, remaining)
	viewportHeight := max(remaining-preludeVisible, 0)
	return m.buildPipelineViewportLayout(pipelineBodyStart, bodyCount, viewportHeight)
}

func (m *Model) rowIndexAtLine(line int) (int, bool) {
	if m.view != viewList || line < 0 || line >= len(m.lineRowMap) {
		return 0, false
	}
	rowIdx := m.lineRowMap[line]
	if rowIdx < 0 || rowIdx >= len(m.rows) {
		return 0, false
	}
	return rowIdx, true
}

// HandleListClick processes a click inside the list view area.
// Pipeline headers toggle collapse; other selectable rows are simply selected.
func (m *Model) HandleListClick(y int) tea.Cmd {
	m.ensureRows()
	rowIdx, ok := m.rowIndexAtLine(y)
	if !ok {
		return nil
	}
	row := m.rows[rowIdx]
	if row.Kind.isNonSelectable() {
		return nil
	}
	m.selected = rowIdx
	m.ensureSelectedPipelineRowVisible()
	if row.Kind == rowPipeline {
		m.toggleSelectedPipelineCollapse()
	}
	return nil
}

func (m *Model) ensureSelectedPipelineRowVisible() {
	layout, ok := m.pipelineViewportLayout()
	if !ok || layout.viewportHeight <= 0 || len(layout.rowHeights) == 0 {
		return
	}
	if m.selected < layout.bodyStart {
		return
	}
	localIdx := m.selected - layout.bodyStart
	if localIdx < 0 || localIdx >= len(layout.rowHeights) {
		return
	}
	rowStart := layout.rowOffsets[localIdx]
	rowEnd := rowStart + layout.rowHeights[localIdx]
	if rowStart < m.pipelineScroll {
		m.pipelineScroll = rowStart
		return
	}
	if rowEnd > m.pipelineScroll+layout.viewportHeight {
		m.pipelineScroll = rowEnd - layout.viewportHeight
	}
}

func (m *Model) scrollPipelineViewport(delta int) bool {
	layout, ok := m.pipelineViewportLayout()
	if !ok || layout.viewportHeight <= 0 || layout.totalLines <= layout.viewportHeight || delta == 0 {
		return false
	}
	maxScroll := layout.totalLines - layout.viewportHeight
	next := m.pipelineScroll + delta
	if next < 0 {
		next = 0
	}
	if next > maxScroll {
		next = maxScroll
	}
	if next == m.pipelineScroll {
		return false
	}
	m.pipelineScroll = next
	return true
}

func (m *Model) selectRow(kind rowKind, id string) bool {
	for idx, row := range m.rows {
		if row.Kind == kind && row.ID == id {
			m.selected = idx
			m.ensureSelectedPipelineRowVisible()
			return true
		}
	}
	return false
}

func (m *Model) visibleListWindow(rowBudget int) (start, end int) {
	total := len(m.rows)
	if rowBudget <= 0 || total == 0 {
		return 0, 0
	}
	if total <= rowBudget {
		return 0, total
	}

	start = clampIndex(m.selected-rowBudget/2, total)
	maxStart := total - rowBudget
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	end = start + rowBudget
	if end > total {
		end = total
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
	if pl, ok := m.pipelineState(m.expanded); ok {
		return renderExpandedPipeline(pl, m.width, m.height, m.theme)
	}
	// Check if expanded ID is a variant.
	if v, ok := m.variantState(m.expanded); ok {
		return renderExpandedVariant(v, m.width, m.height, m.theme)
	}

	// Default: agent expanded view.
	agent, ok := m.agentState(m.expanded)
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

	agent, ok := m.agentState(m.expanded)
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
	models := m.agentModels(agent)
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
	models := m.agentModels(agent)
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
	models := m.agentModels(agent)
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

func (m *Model) currentOpenAIAuthMethod() string {
	canonical := credentials.CanonicalAuthMethod("openai", m.openAIAuthMethod)
	if canonical == "" {
		return credentials.DefaultAuthMethod("openai")
	}
	return canonical
}

func (m *Model) modelsForAgentType(agentType string) []ModelEntry {
	return modelsForAgentForAuth(agentType, m.currentOpenAIAuthMethod())
}

func (m *Model) visibleModelEntries(agentType string, supportedModels []ModelEntry) []ModelEntry {
	if len(supportedModels) > 0 {
		return authAwareModelEntries(supportedModels, m.currentOpenAIAuthMethod())
	}
	return m.modelsForAgentType(agentType)
}

func (m *Model) agentModels(agent *AgentState) []ModelEntry {
	return agentModelsForAuth(agent, m.currentOpenAIAuthMethod())
}

func (m *Model) resolveAgentModelID(models []ModelEntry, preferredID string) string {
	if preferredID != "" {
		resolved := resolveModelForOpenAIAuth(preferredID, m.currentOpenAIAuthMethod())
		if modelInList(models, resolved) {
			return resolved
		}
		if modelInList(models, preferredID) {
			return preferredID
		}
	}
	if len(models) > 0 {
		return models[0].ID
	}
	return preferredID
}

func (m *Model) reconcileAgentModel(agent *AgentState) {
	if agent == nil {
		return
	}
	models := m.agentModels(agent)
	if len(models) == 0 {
		return
	}
	agent.ModelID = m.resolveAgentModelID(models, agent.ModelID)
	if agent.ProviderID == "" || agent.ProviderID != deriveProvider(agent.ModelID) {
		agent.ProviderID = deriveProvider(agent.ModelID)
	}
}

// PersistedModelFor returns the persisted model for agentType, falling back to
// the hardcoded default when the store is absent or has no entry.
func (m *Model) PersistedModelFor(agentType string) string {
	if m.modelStore != nil {
		if persisted := m.modelStore.ModelFor(agentType); persisted != "" {
			return persisted
		}
	}
	return DefaultModelForAgentType(agentType)
}

// RevertModelID sets the agent's ModelID back to a previous value.
// Used when a backend swap fails to undo the optimistic UI update.
func (m *Model) RevertModelID(agentID, previousModelID string) {
	agent, ok := m.agentState(agentID)
	if !ok {
		return
	}
	agent.ModelID = previousModelID
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// padToHeight ensures s contains exactly targetHeight lines by clipping or appending empty lines.
func padToHeight(s string, targetHeight int) string {
	if targetHeight <= 0 {
		return ""
	}
	lines := []string{""}
	if s != "" {
		lines = strings.Split(s, "\n")
	}
	switch {
	case len(lines) > targetHeight:
		lines = lines[:targetHeight]
	case len(lines) < targetHeight:
		lines = append(lines, make([]string, targetHeight-len(lines))...)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) listFooterHeight(contentHeight int) int {
	if contentHeight <= 1 || len(m.rows) == 0 {
		return 0
	}
	return 1
}

// clampIndex constrains an index to [0, count-1].
func clampIndex(idx, count int) int {
	return max(0, min(idx, count-1))
}

func (m *Model) agentState(agentID string) (*AgentState, bool) {
	if m == nil || m.agents == nil {
		return nil, false
	}
	agent, ok := m.agents[agentID]
	if !ok || agent == nil {
		return nil, false
	}
	return agent, true
}

func (m *Model) pipelineState(pipelineID string) (*PipelineState, bool) {
	if m == nil || m.pipelines == nil {
		return nil, false
	}
	pl, ok := m.pipelines[pipelineID]
	if !ok || pl == nil {
		return nil, false
	}
	return pl, true
}

func (m *Model) variantState(variantID string) (*VariantState, bool) {
	if m == nil || m.variants == nil {
		return nil, false
	}
	v, ok := m.variants[variantID]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
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

func extractPipelineTaskID(data map[string]any) string {
	return extractString(data, "task_id")
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func pipelineTaskLabelFromEvent(data map[string]any) string {
	return firstNonEmptyTrimmed(
		extractString(data, "task_slug"),
		extractString(data, "task_name"),
	)
}

func fallbackPipelineTaskLabel(taskID, pipelineID string) string {
	if taskID = strings.TrimSpace(taskID); taskID != "" {
		return taskID
	}
	if pipelineID = strings.TrimSpace(pipelineID); pipelineID != "" {
		return pipelineShortID(pipelineID)
	}
	return ""
}

func isWeakPipelineTaskLabel(label, taskID, pipelineID string) bool {
	label = strings.TrimSpace(label)
	if label == "" {
		return true
	}
	if taskID = strings.TrimSpace(taskID); taskID != "" && label == taskID {
		return true
	}
	if pipelineID = strings.TrimSpace(pipelineID); pipelineID != "" && label == pipelineShortID(pipelineID) {
		return true
	}
	return false
}

func preferredPipelineTaskLabel(current, candidate, taskID, pipelineID string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return strings.TrimSpace(current)
	}
	if isWeakPipelineTaskLabel(current, taskID, pipelineID) {
		return candidate
	}
	return strings.TrimSpace(current)
}

func extractPipelineID(data map[string]any) string {
	if taskID := extractPipelineTaskID(data); taskID != "" {
		return taskID
	}
	return extractString(data, "pipeline_id")
}

// pipelineShortID derives a short display label from a pipeline/DAG ID.
// For UUIDs (36 chars), returns the first 8 hex characters. For shorter
// IDs, returns the full string.
func pipelineShortID(id string) string {
	const shortLen = 8
	clean := strings.ReplaceAll(id, "-", "")
	if len(clean) > shortLen {
		return clean[:shortLen]
	}
	return id
}

func pipelineDisplayLabel(pl *PipelineState) string {
	if pl == nil {
		return ""
	}
	slug := strings.TrimSpace(pl.TaskSlug)
	label := strings.TrimSpace(pl.TaskLabel)
	if slug != "" && label != "" && slug != label {
		return slug + " " + label
	}
	if slug != "" {
		return slug
	}
	if label != "" {
		return label
	}
	if strings.TrimSpace(pl.TaskID) != "" {
		return pl.TaskID
	}
	return pipelineShortID(pl.ID)
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

func extractInt(data map[string]any, key string) (int, bool) {
	if data == nil {
		return 0, false
	}
	val, ok := data[key]
	if !ok {
		return 0, false
	}
	switch typed := val.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
