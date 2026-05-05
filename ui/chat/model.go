package chat

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

// highlightDuration is how long a copied-entry highlight persists.
const highlightDuration = 2 * time.Second

// thinkingRotateInterval is how often the fun thinking message rotates.
const thinkingRotateInterval = 3 * time.Second

// thinkingFrameInterval controls spinner frame advancement for all thinking rows.
const thinkingFrameInterval = 100 * time.Millisecond

// thinkingProgressMinInterval limits immediate UI updates from progress messages.
// Decor ticks still animate every 100ms; this only dampens bursty progress input.
const thinkingProgressMinInterval = 250 * time.Millisecond

// spinnerFrames is a Braille dot animation sequence (matches status/spinner.go).
var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// thinkingMessages are generic rotating messages shown while waiting for the
// first chunk. Used as a fallback when no agent-specific messages exist.
var thinkingMessages = [...]string{
	"Thinking...",
	"Consulting the docs...",
	"Refactoring my thoughts...",
	"Compiling wisdom...",
	"Brewing some ideas...",
	"Connecting the dots...",
	"Parsing your intent...",
	"Chasing a thought...",
	"Resolving dependencies...",
	"Optimizing neural pathways...",
}

// agentThinkingMessages provides fun, agent-specific status messages shown
// while the agent is working. Each agent has a distinct personality inspired
// by RTS unit voice lines.
// Keyed by agent ID prefix (e.g. "architect" matches "architect_abc123").
var agentThinkingMessages = map[string][]string{
	"architect": {
		"Sketching out the blueprint...",
		"Thinking through the structure...",
		"Mapping out the dependencies...",
		"Finding the right shape for this...",
		"Laying the foundation...",
		"I see the vision. Give me a moment...",
		"Working out the layers...",
		"Balancing the design tradeoffs...",
		"This calls for a thoughtful approach...",
		"Planning something solid...",
	},
	"orchestrator": {
		"Coordinating the team...",
		"Getting everyone on the same page...",
		"I love it when a plan comes together...",
		"Figuring out the best order of operations...",
		"Lining up the next steps...",
		"Making sure everyone has what they need...",
		"Sorting through the queue...",
		"Connecting the pieces...",
		"Keeping things moving smoothly...",
		"Almost ready to hand things off...",
	},
	"engineer": {
		"Rolling up my sleeves...",
		"I've got the tools for this...",
		"Let me work through this...",
		"Writing something clean...",
		"Should be a one-liner... famous last words...",
		"Piecing together a solution...",
		"Almost there, just polishing...",
		"Just one more edge case...",
		"Working through the details...",
		"Building it out now...",
	},
	"designer": {
		"Rendering the schematic...",
		"Refining the layout...",
		"Giving this interface some love...",
		"Form follows function...",
		"Aligning to the grid...",
		"Finding the right visual balance...",
		"Sketching... hold on, this is good...",
		"Shaping the component tree...",
		"Making it feel just right...",
		"Less is more...",
	},
	"inspector": {
		"Taking a thorough look...",
		"Let me check on something...",
		"Reviewing carefully...",
		"Noting a few things down...",
		"Running the full diagnostic...",
		"Going through the checklist...",
		"Looking at this from every angle...",
		"Checking every corner...",
		"Making sure the details are right...",
		"Almost done reviewing...",
	},
	"tester": {
		"Running through the scenarios...",
		"All systems looking good so far...",
		"Checking the edge cases...",
		"Exploring a few more paths...",
		"Making sure the happy path works...",
		"Trying some creative inputs...",
		"Almost done with the test suite...",
		"Coverage is looking solid...",
		"Testing one more thing...",
		"Results coming in...",
	},
	"librarian": {
		"I know just where to find that...",
		"Cross-referencing the records...",
		"Pulling the relevant files...",
		"Quietly indexing away...",
		"One moment, checking the archive...",
		"Found something promising...",
		"Following the references...",
		"Dewey would be proud...",
	},
	"academic": {
		"Diving into the research...",
		"The literature has something on this...",
		"Fascinating. Let me dig a little deeper...",
		"Pulling together the findings...",
		"Results are looking promising...",
		"Connecting the dots...",
		"Learning something new here...",
		"A clear picture is forming...",
	},
	"archivalist": {
		"Adding to the chronicle...",
		"Keeping a good record of this...",
		"Saving this for later...",
		"Filing under 'lessons learned'...",
		"Making sure nothing gets lost...",
		"Timestamped and cataloged...",
		"Another chapter written...",
		"Preserving the good stuff...",
	},
	"guardian": {
		"Running the safety checklist...",
		"Keeping an eye on things...",
		"Double-checking everything looks good...",
		"Making sure nothing slipped through...",
		"Quietly watching over the workspace...",
		"All systems nominal...",
		"Taking a careful look around...",
		"Just doing my rounds...",
	},
}

// thinkingMessagesForAgent returns the agent-specific message list, falling
// back to the generic list. Agent IDs like "architect_abc123" are matched by
// prefix against the map keys. Pipeline agent IDs like "task_x:inspector" are
// reduced to their agent suffix before lookup.
func thinkingMessagesForAgent(agentID string) []string {
	if agentID == "" {
		return thinkingMessages[:]
	}
	// Exact match first, then prefix match (agent IDs often have suffixes).
	if msgs, ok := lookupThinkingMessages(agentID); ok {
		return msgs
	}
	if _, agentPart, ok := splitPipelineBadgeIdentity(agentID); ok {
		if msgs, ok := lookupThinkingMessages(agentPart); ok {
			return msgs
		}
	}
	for prefix, msgs := range agentThinkingMessages {
		if strings.HasPrefix(agentID, prefix) {
			return msgs
		}
	}
	return thinkingMessages[:]
}

func lookupThinkingMessages(agentID string) ([]string, bool) {
	if msgs, ok := agentThinkingMessages[agentID]; ok {
		return msgs, true
	}
	trimmed := strings.TrimSuffix(agentID, "-pipeline")
	if trimmed != agentID {
		if msgs, ok := agentThinkingMessages[trimmed]; ok {
			return msgs, true
		}
	}
	return nil, false
}

// streamSlot tracks per-stream accumulation state. Multiple slots can be
// active concurrently when several agents stream in parallel (e.g. architect
// and orchestrator).
type progressOverrideState struct {
	retryText               string
	retryToolDerived        bool
	retryWatchdog           bool
	retrySequence           int64
	pendingRetryText        string
	pendingRetryToolDerived bool
	pendingRetryWatchdog    bool
	pendingRetrySequence    int64
	lastProgressSet         time.Time
}

type streamSlot struct {
	accumulator     *StreamAccumulator
	agentID         string
	thinkingIdx     int       // History index of thinking placeholder for this stream.
	thinkingStart   time.Time // When this stream's thinking phase began.
	progress        progressOverrideState
	renderState     *streamRenderState // Incremental render state for this stream.
	planID          string             // Plan ID embedded in this stream, if any.
	planMarkdown    string             // Rendered plan markdown for this stream.
	planOffset      int                // Accumulator content length when plan was injected.
	deferCompletion bool               // Parent stream finished, but child inter-agent work still owns the row.
}

const deferredParentCompletionStatus = "Waiting for child work to finish..."

// nestedStreamSlot tracks a child agent stream that belongs to an inter-agent
// consult/challenge branch owned by a parent chat entry.
type nestedStreamSlot struct {
	correlationID  string
	branchRef      msg.InterAgentBranchRefMsg
	thinkingStart  time.Time
	progress       progressOverrideState
	content        strings.Builder
	activity       InterAgentChildActivity
	terminalSeen   bool
	terminalFailed bool
	done           bool
}

type resumablePrimaryState struct {
	visibleAgentID string
	agentType      string
	// runtimeAgentID pins this primary to a specific replica of its
	// parent agent. Two concurrent librarian replicas share
	// visibleAgentID/agentType but carry distinct RuntimeAgentID
	// values; requiring match here keeps their rows independent so
	// one never resumes into another.
	runtimeAgentID string
	sessionID      string
	taskID         string
	children       map[string]struct{}
}

// Model is the Bubble Tea model for the chat panel.
// It displays a scrollable history of chat entries with virtual scrolling,
// supports LLM streaming, and handles keyboard navigation.
type Model struct {
	history       *History
	viewport      *Viewport
	streams       map[string]*streamSlot // key = correlationID; nil slots cleaned on complete.
	nestedStreams map[string]*nestedStreamSlot
	theme         *theme.Theme
	width         int
	height        int
	focused       bool

	// Transient highlight for copy feedback.
	highlightID    string
	highlightUntil time.Time

	// Thinking animation state (active between prompt submit and first content).
	thinkingIdx      int       // History index of thinking placeholder (-1 = inactive).
	thinkingAgentID  string    // Agent currently thinking (for agent-specific messages).
	thinkingStart    time.Time // When current thinking phase began.
	progress         progressOverrideState
	thinkingGradient *theme.Gradient // Color gradient cycled during thinking animation.

	// Inline plan tracking: the plan renders as a chat entry updated in place.
	planEntryIdx int    // History index of the plan ChatEntry (-1 = no plan entry).
	planID       string // Correlates updates to the correct entry.

	// Render throttle: chunks buffer at full speed, but the history entry
	// is only synced (and viewDirty set) on DecorTick or StreamComplete.
	streamRenderPending bool

	// Completed entries with pending inter-agent rows still need spinner ticks
	// while a consultation/challenge is awaiting a later response.
	pendingInterAgent map[int]struct{}

	// Completed parent turns that are awaiting a same-agent continuation after
	// nested consult/challenge work remain resumable without staying visually live.
	resumablePrimaries   map[string]resumablePrimaryState
	resumableChildOwners map[string]string

	// Steering animation: pending entries shimmer with holographic color until acknowledged.
	steeringPending  []steeringPendingEntry
	steeringGradient *theme.Gradient
	steeringStart    time.Time

	// View cache: avoids re-rendering when no visible state changed.
	viewCache string
	viewDirty bool

	// Agent activity state: the canonical "what is happening now" signal.
	// agentStates maps correlationID to the most recent AgentStateMsg so the
	// renderer can surface per-entry indicators for primary streaming
	// entries (via ThinkingStatus) and for nested inter-agent branch rows
	// (via the branch's Summary/Status fields). Terminal states evict the
	// correlation; concurrent pipelines each own their own entry so their
	// states remain independent.
	agentStates map[string]*agentActivityState

	// claimRows holds the claims-native row index (cycle, artifact,
	// testament). The chat panel projects these into ChatEntry rows
	// directly, replacing the legacy "convert ClaimArtifactAddedMsg to
	// synthetic ToolCallEventMsg" path that bypassed bridge identity.
	// docs/CLAIMS_UI.md.
	claimRows *claimRowIndex
}

// agentActivityState captures the last observed state transition for a
// correlation so the chat renderer can surface activity even between
// text-stream boundaries. TransitionID orders late-arriving transitions
// deterministically so an out-of-order delivery cannot overwrite a newer
// state with a stale one.
type agentActivityState struct {
	CorrelationID     string
	AgentID           string
	AgentType         string
	State             string
	Detail            string
	PeerAgentType     string
	PeerCorrelationID string
	TransitionID      int64
	ObservedAt        time.Time
}

func progressPriority(toolDerived, watchdog bool) int {
	if watchdog {
		return 0
	}
	if toolDerived {
		return 1
	}
	return 2
}

func (s *progressOverrideState) clear() {
	if s == nil {
		return
	}
	s.retryText = ""
	s.retryToolDerived = false
	s.retryWatchdog = false
	s.retrySequence = 0
	s.pendingRetryText = ""
	s.pendingRetryToolDerived = false
	s.pendingRetryWatchdog = false
	s.pendingRetrySequence = 0
	s.lastProgressSet = time.Time{}
}

func (s *progressOverrideState) hasPending() bool {
	if s == nil {
		return false
	}
	return s.pendingRetrySequence > s.retrySequence && strings.TrimSpace(s.pendingRetryText) != ""
}

func (s *progressOverrideState) hasDisplayed() bool {
	if s == nil {
		return false
	}
	return strings.TrimSpace(s.retryText) != ""
}

func (s *progressOverrideState) normalizeSequence(sequence int64) int64 {
	if s == nil {
		return sequence
	}
	if sequence > 0 {
		return sequence
	}
	maxSeq := s.retrySequence
	if s.pendingRetrySequence > maxSeq {
		maxSeq = s.pendingRetrySequence
	}
	return maxSeq + 1
}

func (s *progressOverrideState) queue(message string, sequence int64, toolDerived, watchdog bool) bool {
	if s == nil {
		return false
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return false
	}
	sequence = s.normalizeSequence(sequence)
	nextPriority := progressPriority(toolDerived, watchdog)
	if s.hasPending() {
		if sequence <= s.pendingRetrySequence {
			return false
		}
		if nextPriority < progressPriority(s.pendingRetryToolDerived, s.pendingRetryWatchdog) {
			return false
		}
	}
	if s.hasDisplayed() {
		if sequence <= s.retrySequence {
			return false
		}
		if watchdog && !s.retryWatchdog {
			return false
		}
	} else if !s.hasPending() && sequence <= s.retrySequence {
		return false
	}
	s.pendingRetryText = message
	s.pendingRetryToolDerived = toolDerived
	s.pendingRetryWatchdog = watchdog
	s.pendingRetrySequence = sequence
	return true
}

func (s *progressOverrideState) canPromote(now time.Time) bool {
	if s == nil {
		return false
	}
	return s.lastProgressSet.IsZero() || now.Sub(s.lastProgressSet) >= thinkingProgressMinInterval
}

func (s *progressOverrideState) promotePending(now time.Time) bool {
	if !s.hasPending() {
		return false
	}
	s.retryText = s.pendingRetryText
	s.retryToolDerived = s.pendingRetryToolDerived
	s.retryWatchdog = s.pendingRetryWatchdog
	s.retrySequence = s.pendingRetrySequence
	s.pendingRetryText = ""
	s.pendingRetryToolDerived = false
	s.pendingRetryWatchdog = false
	s.pendingRetrySequence = 0
	s.lastProgressSet = now
	return true
}

func (s *progressOverrideState) clearDisplayed(now time.Time) bool {
	if s == nil {
		return false
	}
	if strings.TrimSpace(s.retryText) == "" && !s.retryToolDerived && !s.retryWatchdog {
		return false
	}
	s.retryText = ""
	s.retryToolDerived = false
	s.retryWatchdog = false
	s.lastProgressSet = now
	return true
}

func (s *progressOverrideState) clearPending() {
	if s == nil {
		return
	}
	s.pendingRetryText = ""
	s.pendingRetryToolDerived = false
	s.pendingRetryWatchdog = false
	s.pendingRetrySequence = 0
}

func (s *progressOverrideState) clearPendingDisplayed() bool {
	if s == nil {
		return false
	}
	if strings.TrimSpace(s.pendingRetryText) == "" &&
		!s.pendingRetryToolDerived &&
		!s.pendingRetryWatchdog &&
		s.pendingRetrySequence == 0 {
		return false
	}
	s.clearPending()
	return true
}

func (s *progressOverrideState) reconcileToolDerived(next string, now time.Time) bool {
	if s == nil {
		return false
	}
	if strings.TrimSpace(next) == "" {
		if s.hasPending() && (s.pendingRetryToolDerived || s.pendingRetryWatchdog) {
			s.clearPending()
		}
		if s.retryToolDerived {
			return s.clearDisplayed(now)
		}
		return false
	}
	if s.hasPending() {
		if !s.pendingRetryToolDerived && !s.pendingRetryWatchdog {
			return false
		}
		if next == s.pendingRetryText && s.pendingRetryToolDerived && !s.pendingRetryWatchdog {
			return false
		}
		s.pendingRetryText = next
		s.pendingRetryToolDerived = true
		s.pendingRetryWatchdog = false
		if s.pendingRetrySequence == 0 {
			s.pendingRetrySequence = s.normalizeSequence(0)
		}
		if s.canPromote(now) {
			return s.promotePending(now)
		}
		return false
	}
	if !s.hasDisplayed() {
		s.retryText = next
		s.retryToolDerived = true
		s.retryWatchdog = false
		if s.retrySequence == 0 {
			s.retrySequence = s.normalizeSequence(0)
		}
		s.lastProgressSet = now
		return true
	}
	if !s.retryToolDerived && !s.retryWatchdog {
		return false
	}
	if next == s.retryText && s.retryToolDerived && !s.retryWatchdog {
		return false
	}
	s.retryText = next
	s.retryToolDerived = true
	s.retryWatchdog = false
	if s.retrySequence == 0 {
		s.retrySequence = s.normalizeSequence(0)
	}
	s.lastProgressSet = now
	return true
}

// steeringPendingEntry tracks a single steering chat entry awaiting acknowledgment.
type steeringPendingEntry struct {
	idx           int    // History index (adjusted on eviction).
	correlationID string // Matches steering_inject activity event.
}

// Verify interface compliance at compile time.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a chat Model with the given theme and history capacity.
func New(th *theme.Theme, historyCapacity int) *Model {
	h := NewHistory(historyCapacity)
	vp := NewViewport(h, th)
	return &Model{
		history:              h,
		viewport:             vp,
		streams:              make(map[string]*streamSlot),
		nestedStreams:        make(map[string]*nestedStreamSlot),
		theme:                th,
		thinkingIdx:          -1,
		planEntryIdx:         -1,
		pendingInterAgent:    make(map[int]struct{}),
		resumablePrimaries:   make(map[string]resumablePrimaryState),
		resumableChildOwners: make(map[string]string),
		thinkingGradient:     th.Palette.ThinkingGradient(),
		steeringGradient:     th.Palette.GroupGradient(),
		agentStates:          make(map[string]*agentActivityState),
		claimRows:            newClaimRowIndex(),
	}
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns the initial command (none).
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update processes incoming messages and returns the updated component.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	switch typed := incoming.(type) {
	case msg.DecorTickMsg:
		m.handleDecorTick(typed.Time)
		return m, nil
	case msg.ClaimsAgentStatusMsg:
		m.viewDirty = true
		return m, m.handleClaimsAgentStatus(typed)
	case msg.ClaimArtifactAddedMsg:
		m.viewDirty = true
		return m, m.handleClaimArtifactAdded(typed)
	case msg.ClaimArtifactCompletedMsg:
		m.viewDirty = true
		return m, m.handleClaimArtifactCompleted(typed)
	case msg.ClaimResponseTextMsg:
		m.viewDirty = true
		return m, m.handleClaimResponseText(typed)
	case msg.ClaimContextMsg:
		m.viewDirty = true
		return m, m.handleClaimContext(typed)
	case msg.TestamentContextMsg:
		m.viewDirty = true
		return m, m.handleTestamentContext(typed)
	case tea.KeyMsg:
		m.viewDirty = true
		return m, m.handleKey(typed)
	default:
		return m, nil
	}
}

// View renders the chat viewport.
// ViewDirty reports whether View() would produce new output.
func (m *Model) ViewDirty() bool { return m.viewDirty }

func (m *Model) View() string {
	if !m.viewDirty && m.viewCache != "" {
		return m.viewCache
	}
	m.viewCache = m.viewport.View()
	m.viewDirty = false
	return m.viewCache
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the focus identifier for the chat panel.
func (m *Model) ID() component.FocusID {
	return component.FocusChat
}

// Focused returns whether the chat panel has focus.
func (m *Model) Focused() bool {
	return m.focused
}

// SetFocused sets the focus state. Selection is cleared on blur.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
	m.viewDirty = true
	if !focused {
		m.viewport.ClearSelection()
	}
}

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates the available dimensions for the chat panel.
func (m *Model) SetSize(width, height int) {
	m.width = max(width, 0)
	m.height = max(height, 0)
	m.viewDirty = true
	m.viewport.SetSize(m.width, m.height)
}

// ViewportHeight returns the current viewport height in lines.
func (m *Model) ViewportHeight() int {
	return m.viewport.viewHeight
}

// CompensateInputGrowth adjusts the viewport scroll to keep the top stable
// when the chat panel height changes due to input panel growth or shrink.
func (m *Model) CompensateInputGrowth(oldHeight, newHeight int) {
	m.viewport.CompensateInputGrowth(oldHeight, newHeight)
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// Message handlers
// ---------------------------------------------------------------------------

// chatSuppressedEvents lists activity event types that are already
// represented in chat by dedicated paths (streaming, GuideResponseMsg,
// user input) and should not create duplicate entries.
var chatSuppressedEvents = map[string]bool{
	"llm_response":        true, // Duplicates streaming/FinishThinking response.
	"llm_request":         true, // Internal LLM call, not user-facing.
	"user_prompt":         true, // Duplicates user input entry.
	"user_clarification":  true, // Duplicates user input entry.
	"agent_action":        true, // Internal agent operation.
	"agent_decision":      true, // Internal routing decision.
	"tool_call":           true, // Internal tool invocation.
	"tool_result":         true, // Internal tool output.
	"index_start":         true, // Internal indexing lifecycle.
	"index_complete":      true,
	"index_file_added":    true,
	"index_file_removed":  true,
	"context_eviction":    true, // Internal context management.
	"context_restore":     true,
	"success":             true, // Generic outcome, visible from response.
	"steering_checkpoint": true, // Internal checkpoint — visible in agent panel only.
	"steering_inject":     true, // Steering inject shown via dedicated chat entry.
	"steering_edit":       true, // Steering edit shown via dedicated chat entry.
	"steering_rollback":   true, // Steering rollback shown via dedicated chat entry.
}

// handleActivity converts an ActivityEventMsg into a chat entry.
// Events already represented by dedicated chat paths are suppressed.
func (m *Model) handleActivity(ev msg.ActivityEventMsg) tea.Cmd {
	// Steering acknowledgment: transition holographic → static before suppression.
	if ev.Event.EventType.String() == "steering_inject" && len(m.steeringPending) > 0 {
		if corrID, _ := ev.Event.Data["correlation_id"].(string); corrID != "" {
			m.acknowledgeSteering(corrID)
		}
		return nil
	}
	// Parent-correlation preservation: when this event is from a child
	// (guardian approval, peer consult activity, pipeline challenge
	// activity), touch the parent's slot to keep its thinking
	// indicator alive. This runs before the suppression check so the
	// parent's spinner refreshes even if the child's own row would be
	// suppressed from the main chat timeline.
	if shouldSuppressActivity(ev) {
		return nil
	}
	entry := &ChatEntry{
		ID:         ev.Event.ID,
		Timestamp:  ev.Event.Timestamp,
		Source:     activitySource(ev),
		AgentType:  agentTypeFromData(ev),
		AgentID:    ev.Event.AgentID,
		TaskID:     activityStringData(ev, "task_id"),
		TaskName:   activityStringData(ev, "task_name"),
		TaskSlug:   activityStringData(ev, "task_slug"),
		SessionID:  ev.Event.SessionID,
		Content:    ev.Event.Content,
		Height:     -1,
		Importance: ev.Event.Importance,
	}
	m.PushEntry(entry)
	return nil
}

func shouldSuppressActivity(ev msg.ActivityEventMsg) bool {
	if ev.Event == nil {
		return true
	}
	if activityBoolData(ev, "chat_visible") {
		return false
	}
	return chatSuppressedEvents[ev.Event.EventType.String()]
}

func isExplicitTopLevelTransfer(parentCorrelationID string, branchRef *msg.InterAgentBranchRefMsg, topLevelTransfer bool) bool {
	return topLevelTransfer && branchRef == nil && strings.TrimSpace(parentCorrelationID) != ""
}

func appendChatBranchRefLogAttrs(attrs []any, prefix string, ref *msg.InterAgentBranchRefMsg) []any {
	attrs = append(attrs, prefix+"_present", ref != nil)
	if ref == nil {
		return attrs
	}
	return append(attrs,
		prefix+"_parent_correlation_id", strings.TrimSpace(ref.ParentCorrelationID),
		prefix+"_parent_tool_call_key", strings.TrimSpace(ref.ParentToolCallKey),
		prefix+"_kind", strings.TrimSpace(ref.Kind),
		prefix+"_thread_key", strings.TrimSpace(ref.ThreadKey),
	)
}

// handleStreamStart begins accumulation. If a thinking placeholder exists,
// it is reused; otherwise a new entry is pushed. Multiple concurrent streams
// are tracked in the streams map keyed by correlationID.
func (m *Model) handleStreamStart(start msg.StreamStartMsg) tea.Cmd {
	// Control-plane routes (plan-handoff receipt lookups, command-
	// approval-hold begin/resolve, protocol reconciliation) synthesize
	// a StreamStartMsg as a side effect of their guide.RouteRequest
	// delivery. The route serves a real coordination purpose — pausing
	// a DAG, resyncing a receipt — but its UI echo MUST NOT materialize
	// as a user-visible agent entry, nested or top-level, because the
	// target agent (typically the orchestrator) isn't taking a
	// conversational turn. The paired system_coordination ActivityEvent
	// published by the sender via guide.EmitControlPlaneCoordination
	// renders the semantic meaning as a SourceSystem chat row instead.
	//
	// Early-return drops the stream before any slot allocation or
	// history mutation so there's no ghost entry to clean up later.
	if start.ControlPlane {
		chatDebugLog().Info("chat.handleStreamStart: SUPPRESSED_CONTROL_PLANE",
			"correlation_id", start.CorrelationID,
			"agent_id", start.AgentID,
			"agent_type", start.AgentType)
		return nil
	}
	m.clearExplicitTopLevelTransferState(start.CorrelationID, start.ParentCorrelationID, start.BranchRef, start.TopLevelTransfer)
	if start.BranchRef == nil {
		if slot := m.nestedStream(start.CorrelationID); slot != nil {
			ref := slot.branchRef
			start.BranchRef = &ref
		}
	}
	if start.BranchRef != nil {
		m.handleNestedStreamStart(start)
		return nil
	}
	cid := start.CorrelationID
	if m.streams == nil {
		m.streams = make(map[string]*streamSlot)
	}
	chatDebugLog().Info("chat.handleStreamStart: ENTRY",
		"correlation_id", cid,
		"agent_id", start.AgentID,
		"runtime_agent_id", start.RuntimeAgentID,
		"agent_type", start.AgentType,
		"parent_correlation_id", start.ParentCorrelationID,
		"active_streams", len(m.streams),
		"thinking_idx", m.thinkingIdx)
	chatDebugLog().Info("chat.handleStreamStart: CONTINUITY",
		appendChatBranchRefLogAttrs([]any{
			"correlation_id", cid,
			"agent_id", start.AgentID,
			"runtime_agent_id", start.RuntimeAgentID,
			"agent_type", start.AgentType,
			"parent_correlation_id", start.ParentCorrelationID,
			"task_id", strings.TrimSpace(start.TaskID),
			"session_id", strings.TrimSpace(start.SessionID),
			"stream_count", len(m.streams),
			"thinking_idx", m.thinkingIdx,
		}, "branch_ref", start.BranchRef)...)
	now := time.Now()
	streamAgentType := streamEntryAgentType(start)

	if slot, ok := m.streams[cid]; ok && slot.accumulator != nil {
		chatDebugLog().Info("chat.handleStreamStart: REUSE_EXISTING_SLOT",
			"correlation_id", cid,
			"entry_index", slot.accumulator.EntryIndex(),
			"agent_id", start.AgentID,
			"runtime_agent_id", start.RuntimeAgentID,
			"agent_type", streamAgentType)
		m.refreshExistingStreamSlot(slot, start)
		return nil
	}

	// Explicit originator-continuation: the routing layer told us this new
	// correlation is the resumption of an earlier entry on the same agent
	// (e.g. inspector receiving validate_work after issuing a challenge).
	// Resolve to the existing entry directly and append inline — skipping
	// the resumable-primary heuristics below, which key off tester→inspector
	// child lineage inference and can miss.
	if originatorCID := strings.TrimSpace(start.ContinuationOfCorrelationID); originatorCID != "" {
		if idx := m.historyIndexForCorrelation(originatorCID); idx >= 0 {
			chatDebugLog().Info("chat.handleStreamStart: RESUME_ORIGINATOR_CONTINUATION",
				"correlation_id", cid,
				"originator_correlation_id", originatorCID,
				"responder_correlation_id", strings.TrimSpace(start.ParentCorrelationID),
				"entry_index", idx,
				"agent_id", start.AgentID,
				"agent_type", streamAgentType)
			if responderCID := strings.TrimSpace(start.ParentCorrelationID); responderCID != "" {
				m.settleChallengeRowsForChildCIDInEntry(idx, responderCID, true)
			}
			m.clearResumablePrimary(originatorCID)
			m.streams[cid] = m.resumeCompletedPrimaryEntry(idx, start, now)
			return nil
		}
		chatDebugLog().Warn("chat.handleStreamStart: CONTINUATION_ENTRY_MISSING",
			"correlation_id", cid,
			"originator_correlation_id", originatorCID)
	}

	if oldCID, slot := m.reusableDeferredPrimaryStreamSlot(start); slot != nil {
		chatDebugLog().Info("chat.handleStreamStart: REUSE_DEFERRED_SLOT",
			"correlation_id", cid,
			"reused_correlation_id", oldCID,
			"entry_index", slot.accumulator.EntryIndex(),
			"agent_id", start.AgentID,
			"runtime_agent_id", start.RuntimeAgentID,
			"agent_type", streamAgentType)
		delete(m.streams, oldCID)
		m.resumeDeferredPrimaryStreamSlot(slot, start, now)
		m.streams[cid] = slot
		return nil
	}
	if oldCID, idx := m.reusableResumablePrimaryEntry(start); idx >= 0 {
		chatDebugLog().Info("chat.handleStreamStart: REUSE_RESUMABLE_ENTRY",
			"correlation_id", cid,
			"reused_correlation_id", oldCID,
			"entry_index", idx,
			"agent_id", start.AgentID,
			"runtime_agent_id", start.RuntimeAgentID,
			"agent_type", streamAgentType)
		// When the resumption arrives via an explicit top-level transfer, the
		// transfer's ParentCorrelationID is the CID of the child whose response
		// has now materialized. Settle any Pending challenge rows in the parent
		// entry that were waiting on that child — without this, the rows spin
		// forever (the StreamRerouteMsg-only settle path is bypassed by the
		// validation flow which uses StreamStartMsg+chat_top_level_transfer).
		if isExplicitTopLevelTransfer(start.ParentCorrelationID, start.BranchRef, start.TopLevelTransfer) {
			m.settleChallengeRowsForChildCIDInEntry(idx, start.ParentCorrelationID, true)
		}
		m.clearResumablePrimary(oldCID)
		delete(m.streams, oldCID)
		m.streams[cid] = m.resumeCompletedPrimaryEntry(idx, start, now)
		return nil
	}

	// Reuse existing thinking placeholder for the first stream only.
	if m.thinkingIdx >= 0 && len(m.streams) == 0 {
		idx := m.thinkingIdx
		slot := &streamSlot{
			accumulator: NewStreamAccumulator(idx),
			agentID:     streamAgentType,
			thinkingIdx: idx,
			renderState: &streamRenderState{},
		}
		m.updateStreamEntryMetadata(idx, start)
		m.adoptGlobalThinkingState(now, slot)
		m.streams[cid] = slot
		m.clearThinkingState()
		chatDebugLog().Info("chat.handleStreamStart: REUSE_GLOBAL_THINKING",
			"correlation_id", cid,
			"entry_index", idx,
			"agent_id", start.AgentID,
			"runtime_agent_id", start.RuntimeAgentID,
			"agent_type", streamAgentType)
		m.viewDirty = true
		return nil
	}

	// New concurrent stream or no thinking placeholder — create a new entry.
	entry := &ChatEntry{
		ID:             uuid.New().String(),
		Timestamp:      now,
		CorrelationID:  cid,
		Source:         SourceAgent,
		AgentType:      streamAgentType,
		AgentID:        start.AgentID,
		RuntimeAgentID: strings.TrimSpace(start.RuntimeAgentID),
		TaskID:         strings.TrimSpace(start.TaskID),
		TaskName:       strings.TrimSpace(start.TaskName),
		TaskSlug:       strings.TrimSpace(start.TaskSlug),
		SessionID:      start.SessionID,
		Content:        "",
		Height:         -1,
		Streaming:      true,
		ThinkingText:   spinnerFrames[0] + "  0.0s",
		ThinkingStatus: thinkingMessagesForAgent(streamAgentType)[0],
	}
	// If the buffer is full and the entry about to be evicted is
	// still streaming, grow the buffer instead of dropping in-flight
	// chunks. Same principle as the pipeline-lifecycle-disk-commit
	// rule: work isn't safe to tear down until it completes. Growth
	// is bounded in practice because streams finish in seconds and
	// the newly-expanded slots get re-used by subsequent evictions
	// once the streaming entries complete.
	for m.history.Full() && m.history.OldestIsStreaming() {
		m.history.Grow(1)
	}
	willEvict := m.history.Full()
	m.history.Push(entry)
	m.evictStaleResumablePrimariesForAppend(cid)
	m.viewport.OnNewEntry()
	if willEvict {
		m.viewport.AdjustSelectionForEviction()
		m.adjustSteeringIndices()
		m.adjustStreamSlotIndices()
	}
	idx := m.history.Len() - 1
	slot := &streamSlot{
		accumulator: NewStreamAccumulator(idx),
		agentID:     streamAgentType,
		thinkingIdx: idx,
		renderState: &streamRenderState{},
	}
	m.startSlotThinkingAnimation(now, slot)
	m.streams[cid] = slot
	chatDebugLog().Info("chat.handleStreamStart: NEW_ENTRY",
		"correlation_id", cid,
		"entry_index", idx,
		"agent_id", start.AgentID,
		"runtime_agent_id", start.RuntimeAgentID,
		"agent_type", streamAgentType)
	return nil
}

func (m *Model) reusableDeferredPrimaryStreamSlot(start msg.StreamStartMsg) (string, *streamSlot) {
	agentType := streamEntryAgentType(start)
	visibleAgentID := streamVisibleAgentID(start)
	sessionID := strings.TrimSpace(start.SessionID)
	if visibleAgentID == "" {
		return "", nil
	}
	bestCID := ""
	var bestSlot *streamSlot
	bestIdx := -1
	for correlationID, slot := range m.streams {
		if slot == nil || !slot.deferCompletion || slot.accumulator == nil {
			continue
		}
		entry := m.history.Get(slot.accumulator.EntryIndex())
		if entry == nil || !entry.Streaming {
			continue
		}
		if entryVisibleAgentID(entry) != visibleAgentID {
			continue
		}
		if badgeAgentType(entry) != agentType {
			continue
		}
		if sessionID != "" && strings.TrimSpace(entry.SessionID) != "" && strings.TrimSpace(entry.SessionID) != sessionID {
			continue
		}
		if idx := slot.accumulator.EntryIndex(); idx > bestIdx {
			bestIdx = idx
			bestCID = correlationID
			bestSlot = slot
		}
	}
	return bestCID, bestSlot
}

func (m *Model) reusableResumablePrimaryEntry(start msg.StreamStartMsg) (string, int) {
	if m == nil || len(m.resumablePrimaries) == 0 {
		return "", -1
	}
	if isExplicitTopLevelTransfer(start.ParentCorrelationID, start.BranchRef, start.TopLevelTransfer) {
		ownerCID := strings.TrimSpace(m.resumableChildOwners[strings.TrimSpace(start.ParentCorrelationID)])
		if ownerCID == "" || !m.resumablePrimaryMatchesChildOwner(ownerCID, start) {
			return "", -1
		}
		if idx := m.historyIndexForCorrelation(ownerCID); idx >= 0 {
			return ownerCID, idx
		}
		m.clearResumablePrimary(ownerCID)
		return "", -1
	}
	bestCID := ""
	bestIdx := -1
	for ownerCID := range m.resumablePrimaries {
		if !m.resumablePrimaryMatches(ownerCID, start) {
			continue
		}
		if idx := m.historyIndexForCorrelation(ownerCID); idx >= 0 && idx > bestIdx {
			bestCID = ownerCID
			bestIdx = idx
		}
	}
	return bestCID, bestIdx
}

func (m *Model) resumablePrimaryMatchesChildOwner(correlationID string, start msg.StreamStartMsg) bool {
	state, entry, ok := m.loadResumablePrimaryMatch(correlationID)
	if !ok {
		return false
	}
	if agentType := streamEntryAgentType(start); agentType != "" && badgeAgentType(entry) != agentType {
		return false
	}
	// Replica scoping (see resumablePrimaryMatches): mismatched
	// runtime IDs must not cross-consolidate.
	if runtimeID := strings.TrimSpace(start.RuntimeAgentID); runtimeID != "" && state.runtimeAgentID != "" && state.runtimeAgentID != runtimeID {
		return false
	}
	if sessionID := strings.TrimSpace(start.SessionID); sessionID != "" && state.sessionID != "" && state.sessionID != sessionID {
		return false
	}
	if taskID := strings.TrimSpace(start.TaskID); taskID != "" && state.taskID != "" && state.taskID != taskID {
		return false
	}
	return true
}

func (m *Model) resumeDeferredPrimaryStreamSlot(slot *streamSlot, start msg.StreamStartMsg, now time.Time) {
	if slot == nil || slot.accumulator == nil {
		return
	}
	chatDebugLog().Info("chat.resumeDeferredPrimaryStreamSlot",
		"correlation_id", strings.TrimSpace(start.CorrelationID),
		"entry_index", slot.accumulator.EntryIndex(),
		"agent_id", strings.TrimSpace(start.AgentID),
		"runtime_agent_id", strings.TrimSpace(start.RuntimeAgentID),
		"agent_type", streamEntryAgentType(start))
	slot.agentID = streamEntryAgentType(start)
	slot.deferCompletion = false
	m.updateStreamEntryMetadata(slot.accumulator.EntryIndex(), start)
	m.startSlotThinkingAnimation(now, slot)
	if slot.renderState == nil {
		slot.renderState = &streamRenderState{}
	}
	m.viewport.AddStreamState(slot.accumulator.EntryIndex(), slot.renderState)
	m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.progress.retryText, slot.thinkingStart, now)
	m.viewDirty = true
}

func (m *Model) resumeCompletedPrimaryEntry(idx int, start msg.StreamStartMsg, now time.Time) *streamSlot {
	chatDebugLog().Info("chat.resumeCompletedPrimaryEntry",
		"correlation_id", strings.TrimSpace(start.CorrelationID),
		"entry_index", idx,
		"agent_id", strings.TrimSpace(start.AgentID),
		"runtime_agent_id", strings.TrimSpace(start.RuntimeAgentID),
		"agent_type", streamEntryAgentType(start),
		"parent_correlation_id", strings.TrimSpace(start.ParentCorrelationID))
	slot := &streamSlot{
		accumulator: NewStreamAccumulator(idx),
		agentID:     streamEntryAgentType(start),
		thinkingIdx: idx,
		renderState: &streamRenderState{},
	}
	m.updateStreamEntryMetadata(idx, start)
	m.startSlotThinkingAnimation(now, slot)
	m.viewport.AddStreamState(idx, slot.renderState)
	m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.progress.retryText, slot.thinkingStart, now)
	m.viewDirty = true
	return slot
}

// handleStreamChunk appends text to the accumulator. The entry is NOT synced
// immediately — instead streamRenderPending is set and the actual sync happens
// on the next DecorTick (100ms) or StreamComplete, reducing per-token renders.
//
// Thinking is NOT resolved on the first text chunk. The thinking indicator
// stays active (thinkingIdx >= 0) so that handleStreamProgress continues to
// receive progress messages and tickThinking keeps the spinner alive. The
// streaming renderer shows a status footer alongside content while streaming.
// Thinking resolves at StreamComplete (which sets ThinkingElapsed for the
// collapsed summary) or StreamError.
func (m *Model) handleStreamChunk(chunk msg.StreamChunkMsg) tea.Cmd {
	if m.handleNestedStreamChunk(chunk) {
		return nil
	}
	slot, ok := m.streams[chunk.CorrelationID]
	if !ok || slot.accumulator == nil {
		chatDebugLog().Warn("chat.handleStreamChunk: NO_SLOT — chunk dropped",
			"correlation_id", chunk.CorrelationID,
			"text_len", len(chunk.Text))
		return nil
	}

	slot.accumulator.Append(chunk.Text)
	m.streamRenderPending = true
	return nil
}

func (m *Model) handleStreamProgress(progress msg.StreamProgressMsg) tea.Cmd {
	if progress.Visibility == events.VisibilitySystem {
		return nil
	}
	m.clearExplicitTopLevelTransferState(progress.CorrelationID, progress.ParentCorrelationID, progress.BranchRef, progress.TopLevelTransfer)
	if progress.BranchRef != nil {
		m.handleNestedStreamProgress(progress)
		return nil
	}
	if m.handleNestedStreamProgress(progress) {
		return nil
	}
	displayAgentID := streamEntryAgentType(msg.StreamStartMsg{
		AgentID:   progress.AgentID,
		AgentType: progress.AgentType,
	})
	message := sanitizeThinkingMessage(progress.Message)
	if slot := m.streamSlot(progress.CorrelationID); slot != nil {
		m.updateSlotThinkingAgent(slot, displayAgentID)
		m.applySlotProgress(slot, message, progress.Sequence, progress.ToolDerived, progress.Watchdog)
		return nil
	}
	m.updateThinkingAgent(displayAgentID)
	if message == "" {
		return nil
	}
	if m.thinkingIdx < 0 {
		return nil
	}
	now := time.Now()
	if entry := m.history.Get(m.thinkingIdx); entry != nil && staleGuardianInterAgentProgress(message, entry.ToolCalls) {
		// Redundant guardian progress text; inline guardian row
		// already surfaces it. Suppress the message, preserve the
		// spinner. See applySlotProgress for the full rationale.
		return nil
	}
	if m.thinkingStart.IsZero() {
		m.thinkingStart = now
	}
	if m.progress.queue(message, progress.Sequence, progress.ToolDerived, progress.Watchdog) && m.progress.canPromote(now) {
		if m.progress.promotePending(now) {
			m.setThinkingTextNow(m.progress.retryText)
		}
	}
	return nil
}

// handleAgentState applies an explicit agent-activity state transition.
//
// State events are the canonical source of "what is happening now" for the
// UI activity indicator. Each correlation's most recent state is retained
// (ordered by TransitionID to survive out-of-order delivery). Terminal
// states (complete, errored) evict the correlation's record so a late
// non-terminal state cannot resurrect a finished request. When the entry
// for this correlation is still streaming, the state is attached to that
// entry; otherwise the latest non-terminal state becomes the inter-stream
// bridge indicator surfaced beneath the viewport.
func (m *Model) handleAgentState(state msg.AgentStateMsg) tea.Cmd {
	cid := strings.TrimSpace(state.CorrelationID)
	if cid == "" {
		return nil
	}
	if m.agentStates == nil {
		m.agentStates = make(map[string]*agentActivityState)
	}
	// Preserve per-correlation monotonicity: reject any transition whose
	// TransitionID is not strictly greater than what we already observed
	// for this correlation. The publisher's counter is monotonic across
	// the entire process, so equal/lesser IDs on the same correlation
	// indicate out-of-order delivery.
	if existing, ok := m.agentStates[cid]; ok && existing != nil {
		if state.TransitionID > 0 && existing.TransitionID > state.TransitionID {
			return nil
		}
	}
	record := &agentActivityState{
		CorrelationID:     cid,
		AgentID:           strings.TrimSpace(state.AgentID),
		AgentType:         strings.TrimSpace(state.AgentType),
		State:             strings.TrimSpace(state.State),
		Detail:            strings.TrimSpace(state.Detail),
		PeerAgentType:     strings.TrimSpace(state.PeerAgentType),
		PeerCorrelationID: strings.TrimSpace(state.PeerCorrelationID),
		TransitionID:      state.TransitionID,
		ObservedAt:        state.Timestamp,
	}
	if record.ObservedAt.IsZero() {
		record.ObservedAt = time.Now()
	}
	terminal := isTerminalAgentState(record.State)
	if terminal {
		// Terminal states evict the correlation from the active map so
		// stale activity does not persist after the request finishes.
		delete(m.agentStates, cid)
	} else {
		m.agentStates[cid] = record
	}

	// Attach the state detail to the entry currently holding this
	// correlation's stream (if any) so the existing thinking-status
	// rendering path surfaces the transition without the caller having to
	// emit a separate progress event. Terminal states clear the status so
	// the finalized entry doesn't show stale activity. The mutation is
	// done under History.UpdateAt so it's race-free with concurrent
	// renders and invalidates cached render lines.
	if slot, ok := m.streams[cid]; ok && slot.accumulator != nil {
		entryIdx := slot.accumulator.EntryIndex()
		m.history.UpdateAt(entryIdx, func(e *ChatEntry) {
			if terminal {
				e.ThinkingStatus = ""
			} else if detail := record.Detail; detail != "" {
				e.ThinkingStatus = detail
			} else {
				e.ThinkingStatus = humanizeAgentState(record.State)
			}
			invalidateChatEntryRender(e)
		})
	}
	// When the correlation matches a nested inter-agent branch slot (consult
	// or challenge rendered as a child tool row inside the parent's entry),
	// write the state detail to the child activity's ThinkingStatus. The
	// existing tickNestedStreamThinking path picks it up and renders the
	// status inline beneath the child's spinner + elapsed — no bottom
	// bridge needed, no duplication. Terminal states clear the status to
	// avoid stale activity lingering after the peer finishes.
	if nested := m.nestedStream(cid); nested != nil {
		if terminal {
			nested.activity.ThinkingStatus = ""
		} else if detail := record.Detail; detail != "" {
			nested.activity.ThinkingStatus = detail
		} else {
			nested.activity.ThinkingStatus = humanizeAgentState(record.State)
		}
		m.syncPendingNestedStream(nested)
	}
	// Parent-correlation preservation: when this state event is from a
	// child (guardian approval, pipeline challenge, peer consult), look
	// up the parent slot and keep its thinking indicator alive. Without
	// this, the parent's spinner goes stale the moment a child event
	// publishes on its own correlation.
	//
	// Non-terminal: bump parent's thinkingStart so the elapsed timer
	// keeps running (no visible reset because we only bump when it's
	// already zero); surface the child's status on the parent's entry
	// so the user sees "Waiting on guardian..." instead of a dead row.
	//
	// Terminal: intentionally do NOT evict the parent — only the child
	// has finished. The parent's own state event (complete/errored)
	// retires its indicator.
	return nil
}

// humanizeAgentState maps a canonical state string to a short user-facing
// phrase. Used when the publisher didn't supply a detail — e.g., when the
// state was auto-emitted by PublishStreamStart/Complete. Consistent
// phrasing across agents keeps the UI indicator readable.
func humanizeAgentState(state string) string {
	switch state {
	case "receiving":
		return "Receiving request..."
	case "reasoning":
		return "Reasoning..."
	case "tool_executing":
		return "Executing tool..."
	case "consulting_peer":
		return "Consulting peer..."
	case "dispatching_to_peer":
		return "Dispatching to peer..."
	case "awaiting_peer_response":
		return "Awaiting peer response..."
	case "composing_response":
		return "Composing response..."
	default:
		return ""
	}
}

// isTerminalAgentState reports whether the named state ends the
// correlation's activity for bridge-indicator purposes. Kept as a string
// comparison so the chat model does not take a dependency on the guide
// package's enum.
func isTerminalAgentState(state string) bool {
	switch state {
	case "complete", "errored":
		return true
	default:
		return false
	}
}

// handleStreamComplete finalizes a single streaming entry by correlationID.
func (m *Model) handleStreamComplete(done msg.StreamCompleteMsg) tea.Cmd {
	m.clearExplicitTopLevelTransferState(done.CorrelationID, done.ParentCorrelationID, done.BranchRef, done.TopLevelTransfer)
	if done.BranchRef != nil {
		m.handleNestedStreamComplete(done)
		return nil
	}
	if m.handleNestedStreamComplete(done) {
		return nil
	}
	cid := done.CorrelationID
	chatDebugLog().Info("chat.handleStreamComplete: ENTRY",
		"correlation_id", cid,
		"agent_id", done.AgentID,
		"authoritative_text_len", len(done.AuthoritativeText),
		"active_streams", len(m.streams),
		"thinking_idx", m.thinkingIdx,
		"stream_render_pending", m.streamRenderPending)
	slot, ok := m.streams[cid]
	if !ok || slot.accumulator == nil {
		chatDebugLog().Warn("chat.handleStreamComplete: NO_SLOT — thinking NOT cleared",
			"correlation_id", cid,
			"thinking_idx", m.thinkingIdx)
		return nil
	}

	if done.AuthoritativeText != "" {
		slot.accumulator.Replace(done.AuthoritativeText)
		slot.planOffset = 0
	}
	slot.accumulator.Complete()

	// Acknowledge any pending steering entries for this correlation.
	if len(m.steeringPending) > 0 && cid != "" {
		m.acknowledgeSteering(cid)
	}

	if m.shouldRecordResumablePrimary(slot) {
		m.recordResumableCompletedPrimary(cid, slot)
		chatDebugLog().Info("chat.handleStreamComplete: RESUMABLE",
			"correlation_id", cid,
			"pending_inter_agent", true)
	}

	m.finalizeCompletedStreamSlot(cid, slot, true, "")

	// If this slot owned the global thinking indicator and no streams remain,
	// the thinking phase is over — resolve the placeholder and clear
	// m.thinkingIdx. Without this, the last progress message ("Plan is ready
	// for your review.", etc.) renders as a live spinner indefinitely even
	// after the stream finished. handleStreamError has the symmetric guard;
	// this restores parity after commit 4eb0d6e5 dropped it from the Complete
	// path.
	if m.thinkingIdx >= 0 && len(m.streams) == 0 {
		m.resolveThinkingEntry()
	}

	chatDebugLog().Info("chat.handleStreamComplete: DONE",
		"correlation_id", cid,
		"remaining_streams", len(m.streams),
		"thinking_idx", m.thinkingIdx)
	return nil
}

func (m *Model) handleStreamReroute(reroute msg.StreamRerouteMsg) tea.Cmd {
	original := strings.TrimSpace(reroute.OriginalCorrelationID)
	if original == "" {
		return nil
	}
	slot := m.streamSlot(original)
	if slot == nil {
		return nil
	}
	entryIdx := -1
	if slot.accumulator != nil {
		entryIdx = slot.accumulator.EntryIndex()
	}
	if entryIdx >= 0 {
		m.history.UpdateAt(entryIdx, func(e *ChatEntry) {
			if settleProtocolChallengeRowsForReroute(e) {
				invalidateChatEntryRender(e)
			}
		})
		delete(m.pendingInterAgent, entryIdx)
	}
	m.resolveSlotThinkingEntry(slot)
	if entryIdx >= 0 {
		m.history.UpdateAt(entryIdx, func(e *ChatEntry) {
			invalidateChatEntryRender(e)
		})
	}
	m.viewDirty = true
	return nil
}

// settleProtocolChallengeRowsForReroute flips ALL Pending challenge rows in
// entry to Done. A reroute means the conversation has handed back to a new
// stream — at that point any in-flight challenge rooted in this entry is
// over. This is the coarse settle path used when we don't have a specific
// child CID to match (the precise path lives in
// settleChallengeRowsForChildCID, used by top-level-transfer and
// nested-complete handlers). The previous thread-key prefix gate
// (pipeline:/global_review:) was overly conservative and left non-protocol
// challenges spinning forever.
func settleProtocolChallengeRowsForReroute(entry *ChatEntry) bool {
	if entry == nil {
		return false
	}
	changed := false
	for i := range entry.ToolCalls {
		record := &entry.ToolCalls[i]
		if record.InterAgent == nil || record.InterAgent.Kind != InterAgentToolChallenge {
			continue
		}
		if record.InterAgent.Status != InterAgentToolPending {
			continue
		}
		record.InterAgent.Status = InterAgentToolDone
		record.Completed = true
		record.Success = true
		changed = true
	}
	return changed
}

// handleStreamError adds an error entry and cleans up the accumulator.
func (m *Model) handleStreamError(errMsg msg.StreamErrorMsg) tea.Cmd {
	if errMsg.BranchRef != nil {
		m.handleNestedStreamError(errMsg)
		return nil
	}
	if m.handleNestedStreamError(errMsg) {
		return nil
	}
	m.streamRenderPending = false
	// Clean up the specific slot if it exists.
	if slot, ok := m.streams[errMsg.CorrelationID]; ok && slot.accumulator != nil {
		m.finalizeCompletedStreamSlot(errMsg.CorrelationID, slot, false, formatErrorForChat(errMsg.Err))
	}
	// If all streams are gone, clear global viewport stream state.
	if len(m.streams) == 0 {
		m.viewport.ClearAllStreamStates()
	}
	if m.thinkingIdx >= 0 && len(m.streams) == 0 {
		m.resolveThinkingEntry()
	}

	errEntry := &ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    SourceError,
		SessionID: errMsg.SessionID,
		Content:   formatErrorForChat(errMsg.Err),
		Height:    -1,
	}
	m.PushEntry(errEntry)
	return nil
}

// formatErrorForChat returns a human-readable error message suitable for the
// chat panel. Delegates to providers.FriendlyErrorMessage which handles
// ProviderError, anthropic.Error, embedded JSON, and raw error strings.
func formatErrorForChat(err error) string {
	return providers.FriendlyErrorMessage(err)
}

// handleRetryStatus logs each retry error as a chat entry and updates
// the thinking indicator so the user sees progress during backoff.
func (m *Model) handleRetryStatus(retry msg.RetryStatusMsg) tea.Cmd {
	content := formatRetryMessage(retry)

	errEntry := &ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    SourceSystem,
		SessionID: retry.SessionID,
		Content:   content,
		Height:    -1,
	}
	m.PushEntry(errEntry)

	// Also update the thinking spinner so it reflects the retry state.
	delayStr := formatRetryDelay(retry.Delay)
	message := sanitizeThinkingMessage(fmt.Sprintf("retrying (%d/%d) in %s...", retry.Attempt, retry.MaxAttempts, delayStr))
	if slot := m.streamSlot(retry.CorrelationID); slot != nil {
		m.applySlotProgress(slot, message, 0, false, false)
		return nil
	}
	if m.thinkingIdx >= 0 {
		now := time.Now()
		if m.progress.queue(message, 0, false, false) && m.progress.canPromote(now) {
			if m.progress.promotePending(now) {
				m.setThinkingTextNow(m.progress.retryText)
			}
		}
	}
	return nil
}

// formatRetryMessage builds a concise, human-readable retry status line.
// Example: "Retrying (1/5) in 2s — quota resets after 55s"
func formatRetryMessage(retry msg.RetryStatusMsg) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Retrying (%d/%d)", retry.Attempt, retry.MaxAttempts))

	if retry.Delay > 0 {
		b.WriteString(" in ")
		b.WriteString(formatRetryDelay(retry.Delay))
	}

	if retry.Error != "" {
		b.WriteString(" — ")
		b.WriteString(retry.Error)
	}

	return b.String()
}

// formatRetryDelay renders a duration as a compact human string (e.g. "2s", "1m30s").
// Uses Round instead of Truncate so sub-second values like 0.8s display as "1s" not "0s".
func formatRetryDelay(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Second {
		return "<1s"
	}
	return d.String()
}

// handleToolCallEvent processes a tool call start or completion event by
// updating the matching streaming entry's ToolCalls list. Matches by
// correlationID first, then falls back to the thinking placeholder.
func (m *Model) handleToolCallEvent(ev msg.ToolCallEventMsg) tea.Cmd {
	m.clearExplicitTopLevelTransferState(ev.CorrelationID, ev.ParentCorrelationID, ev.BranchRef, ev.TopLevelTransfer)
	if ev.BranchRef != nil {
		m.handleNestedToolCallEvent(ev)
		return nil
	}
	if m.handleNestedToolCallEvent(ev) {
		return nil
	}
	idx := m.streamingIndexForCorrelation(ev.CorrelationID)
	if idx < 0 {
		return nil
	}

	current := m.history.Get(idx)
	currentAgentType := ""
	if current != nil {
		currentAgentType = badgeAgentType(current)
	}
	if m.handleInterAgentToolCallEvent(idx, currentAgentType, ev) {
		if slot := m.streamSlot(ev.CorrelationID); slot != nil {
			m.reconcileToolDerivedSlotProgress(slot)
		} else if idx == m.thinkingIdx {
			m.reconcileToolDerivedThinkingProgress(idx)
		}
		return nil
	}

	m.history.UpdateAt(idx, func(e *ChatEntry) {
		switch ev.Phase {
		case 0: // ToolCallStart
			e.ToolCalls = append(e.ToolCalls, ToolCallRecord{
				ToolCallKey: ev.ToolCallKey,
				ToolName:    ev.ToolName,
				ArgsSummary: ev.ArgsSummary,
				FullArgs:    ev.FullArgs,
				StartedAt:   ev.StartedAt,
			})
		case 1: // ToolCallComplete
			// Find the last incomplete record for this tool call. Prefer the
			// stable per-call key; fall back to the historical tool-name match
			// for older events that predate the key.
			for i := len(e.ToolCalls) - 1; i >= 0; i-- {
				if !toolCallRecordCanAcceptCompletion(e.ToolCalls[i]) {
					continue
				}
				if !toolCallRecordMatchesEvent(e.ToolCalls[i], ev) {
					continue
				}
				if e.ToolCalls[i].StartedAt.IsZero() {
					e.ToolCalls[i].StartedAt = ev.StartedAt
				}
				e.ToolCalls[i].Duration = ev.Duration
				e.ToolCalls[i].Success = ev.Success
				e.ToolCalls[i].Completed = true
				e.ToolCalls[i].SyntheticCompletion = false
				e.ToolCalls[i].Output = ev.Output
				e.ToolCalls[i].ErrorMsg = ev.ErrorMsg
				if strings.TrimSpace(e.ToolCalls[i].ToolCallKey) == "" {
					e.ToolCalls[i].ToolCallKey = strings.TrimSpace(ev.ToolCallKey)
				}
				if shouldBackfillToolCallArgs(e.ToolCalls[i].FullArgs, ev.FullArgs) {
					e.ToolCalls[i].FullArgs = ev.FullArgs
				}
				if shouldBackfillToolCallArgs(e.ToolCalls[i].ArgsSummary, ev.ArgsSummary) {
					e.ToolCalls[i].ArgsSummary = ev.ArgsSummary
				}
				if !ev.Success {
					e.ToolCalls[i].Expanded = true // Auto-expand failures.
				}
				break
			}
		}
		invalidateChatEntryRender(e)
	})
	if slot := m.streamSlot(ev.CorrelationID); slot != nil {
		m.reconcileToolDerivedSlotProgress(slot)
	} else if idx == m.thinkingIdx {
		m.reconcileToolDerivedThinkingProgress(idx)
	}
	return nil
}

// toolCallRecordMatchesEvent pairs a stored start record with an incoming
// complete event by exact ToolCallKey (lifecycle ID) equality.
//
// Every emitted tool-call event carries the provider-stamped lifecycle ID
// (see providers.EnsureToolCallID and agents/shared.toolCallEventKey), so
// key equality is necessary and sufficient. Earlier fallbacks over tool
// name or normalized args existed to paper over a bug where start/complete
// keys drifted between streaming phases; that bug is gone, and the
// fallbacks would only ever mask regressions if they returned now.
func toolCallRecordMatchesEvent(record ToolCallRecord, ev msg.ToolCallEventMsg) bool {
	recordKey := strings.TrimSpace(record.ToolCallKey)
	eventKey := strings.TrimSpace(ev.ToolCallKey)
	return recordKey != "" && recordKey == eventKey
}

func toolCallRecordCanAcceptCompletion(record ToolCallRecord) bool {
	return !record.Completed || record.SyntheticCompletion
}

func shouldBackfillToolCallArgs(current, incoming string) bool {
	current = strings.TrimSpace(current)
	incoming = strings.TrimSpace(incoming)
	if incoming == "" || incoming == "{}" {
		return false
	}
	if current == "" || current == "{}" {
		return true
	}
	return false
}

// streamingIndexForCorrelation returns the history index for a specific
// correlationID. Checks stream slots first (exact match), then falls back
// to the thinking placeholder (tool calls may arrive before StreamStart).
func (m *Model) streamingIndexForCorrelation(correlationID string) int {
	if slot, ok := m.streams[correlationID]; ok && slot.accumulator != nil {
		return slot.accumulator.EntryIndex()
	}
	if idx := m.historyIndexForCorrelation(correlationID); idx >= 0 {
		return idx
	}
	if m.thinkingIdx >= 0 && len(m.streams) == 0 {
		return m.thinkingIdx
	}
	return -1
}

func (m *Model) historyIndexForCorrelation(correlationID string) int {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return -1
	}
	m.history.mu.RLock()
	defer m.history.mu.RUnlock()
	for idx := m.history.count - 1; idx >= 0; idx-- {
		physical := m.history.logicalToPhysical(idx)
		entry := &m.history.entries[physical]
		if strings.TrimSpace(entry.CorrelationID) == correlationID {
			return idx
		}
		for _, alt := range entry.AdditionalCorrelationIDs {
			if strings.TrimSpace(alt) == correlationID {
				return idx
			}
		}
	}
	return -1
}

// activeStreamingIndices returns the history indices of entries currently
// receiving streaming content or tool-call activity.
func (m *Model) activeStreamingIndices() []int {
	seen := make(map[int]struct{}, len(m.streams)+len(m.pendingInterAgent)+1)
	indices := make([]int, 0, len(m.streams)+len(m.pendingInterAgent)+1)
	for _, slot := range m.streams {
		if slot.accumulator != nil {
			idx := slot.accumulator.EntryIndex()
			if _, ok := seen[idx]; ok {
				continue
			}
			seen[idx] = struct{}{}
			indices = append(indices, idx)
		}
	}
	for idx := range m.pendingInterAgent {
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		indices = append(indices, idx)
	}
	if len(indices) == 0 && m.thinkingIdx >= 0 {
		indices = append(indices, m.thinkingIdx)
	}
	return indices
}

func (m *Model) invalidateEntryToolCalls(idx int) bool {
	hasActive := false
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		for i := range e.ToolCalls {
			if toolCallHasActiveVisual(e.ToolCalls[i]) {
				hasActive = true
				break
			}
		}
		if !hasActive {
			return
		}
		invalidateChatEntryRender(e)
	})
	return hasActive
}

// handleKey processes keyboard input when the chat panel is focused.
// Arrow keys select entries; j/k scroll by line.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if !m.focused {
		return nil
	}
	switch key.String() {
	case "up":
		m.viewport.SelectUp()
	case "down":
		m.viewport.SelectDown()
	case "k":
		m.viewport.ScrollUp()
	case "j":
		m.viewport.ScrollDown()
	case "pgup":
		m.viewport.PageUp()
	case "pgdown":
		m.viewport.PageDown()
	case "home":
		m.viewport.ToTop()
	case "end":
		m.viewport.ToBottom()
	case "esc":
		m.viewport.ClearSelection()
	case " ", "space":
		if m.ToggleSelected() {
			m.viewDirty = true
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Inline plan display
// ---------------------------------------------------------------------------

// HandlePlanUpdate renders the plan as a markdown chat entry and updates it
// in place on subsequent snapshots. During the planning phase (active stream,
// first "ready" snapshot), the plan is embedded into the stream entry for a
// cohesive agent response. During execution (no active stream), the plan
// appears as a separate entry for live task status tracking.
func (m *Model) HandlePlanUpdate(update msg.PlanUpdateMsg) {
	content := formatPlanMarkdown(update)

	// Active stream + ready plan + no plan already embedded → embed in stream.
	// Find a slot without a plan already embedded.
	if update.Status == "ready" {
		if slot, ok := m.streams[update.CorrelationID]; ok && slot.accumulator != nil {
			slot.planID = update.PlanID
			slot.planMarkdown = content
			slot.planOffset = len(slot.accumulator.Content())
			m.planID = update.PlanID
			return
		}
		for _, slot := range m.streams {
			if slot.accumulator != nil && (slot.planMarkdown == "" || slot.planID == update.PlanID) {
				slot.planID = update.PlanID
				slot.planMarkdown = content
				slot.planOffset = len(slot.accumulator.Content())
				m.planID = update.PlanID
				return
			}
		}
	}

	// No active stream (or non-ready status) — use the existing separate-entry path.
	entryID := "plan-" + update.PlanID

	if m.planEntryIdx < 0 || m.planID != update.PlanID {
		m.pushPlanEntry(entryID, content, update)
		return
	}

	// Same plan — update existing entry in place via UpdateAt.
	// Verify the entry ID to detect if the plan entry was evicted and
	// its slot reused by a different entry.
	matched := false
	ok := m.history.UpdateAt(m.planEntryIdx, func(e *ChatEntry) {
		if e.ID != entryID {
			return // Slot reused after eviction; stale index.
		}
		matched = true
		e.Content = content
		e.RenderedLines = nil
		e.CodeRegions = nil
		e.Height = -1
	})

	if !ok || !matched {
		// Index out of range or entry was evicted — re-push.
		m.pushPlanEntry(entryID, content, update)
		return
	}
	m.viewDirty = true
}

// pushPlanEntry appends a new plan chat entry and records its index.
func (m *Model) pushPlanEntry(id, content string, update msg.PlanUpdateMsg) {
	ts := update.StartTime
	if ts.IsZero() {
		ts = time.Now()
	}
	entry := &ChatEntry{
		ID:        id,
		Timestamp: ts,
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   content,
		Height:    -1,
	}
	m.PushEntry(entry)
	m.planEntryIdx = m.history.Len() - 1
	m.planID = update.PlanID
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// PushEntry appends an entry and notifies the viewport.
// If the ring buffer is full, the oldest entry is evicted and the
// viewport selection index is adjusted to compensate.
func (m *Model) PushEntry(entry *ChatEntry) {
	willEvict := m.history.Full()
	m.history.Push(entry)
	if entry != nil {
		m.evictStaleResumablePrimariesForAppend(entry.CorrelationID)
	}
	m.viewport.OnNewEntry()
	if willEvict {
		m.viewport.AdjustSelectionForEviction()
		// All logical indices shift down by 1 when the oldest entry is evicted.
		// A tracked index reaching -1 means that entry was the evicted one.
		if m.thinkingIdx >= 0 {
			m.thinkingIdx--
		}
		if m.planEntryIdx >= 0 {
			m.planEntryIdx--
		}
		nextPending := make(map[int]struct{}, len(m.pendingInterAgent))
		for idx := range m.pendingInterAgent {
			idx--
			if idx >= 0 {
				nextPending[idx] = struct{}{}
			}
		}
		m.pendingInterAgent = nextPending
		for _, slot := range m.streams {
			if slot.accumulator != nil {
				slot.accumulator.AdjustIndex(-1)
			}
			if slot.thinkingIdx >= 0 {
				slot.thinkingIdx--
			}
		}
		m.adjustSteeringIndices()
	}
	m.viewDirty = true
}

func (m *Model) handleInterAgentToolCallEvent(idx int, currentAgentType string, ev msg.ToolCallEventMsg) bool {
	switch ev.Phase {
	case 0:
		if eventIsInterAgentOriginUpdate(ev) {
			return true
		}
		record, ok := buildInterAgentStartRecord(ev)
		if !ok {
			return false
		}
		record.StartedAt = ev.StartedAt
		m.history.UpdateAt(idx, func(e *ChatEntry) {
			e.ToolCalls = append(e.ToolCalls, record)
			invalidateChatEntryRender(e)
		})
		m.syncPendingInterAgentEntry(idx)
		m.syncAllNestedInterAgentStreams()
		return true
	case 1:
		if m.completeLocalInterAgentToolRecord(idx, currentAgentType, ev) {
			m.syncPendingInterAgentEntry(idx)
			m.syncAllNestedInterAgentStreams()
			return true
		}
		// Origin-updates are identity-addressed (ThreadKey) cross-scope
		// patches to an existing row. They must never fall through to
		// buildInterAgentCompletionFallback — that path synthesizes a
		// phantom row and is the root cause of duplicate-agent-row bugs.
		// On miss we log and drop; a missing originator row is a routing
		// defect upstream, not a display problem to paper over.
		if eventIsInterAgentOriginUpdate(ev) {
			if m.applyInterAgentOriginUpdateAnywhere(ev, currentAgentType) {
				m.syncAllNestedInterAgentStreams()
				return true
			}
			threadKey := ""
			if ev.InterAgent != nil {
				threadKey = strings.TrimSpace(ev.InterAgent.ThreadKey)
			}
			chatDebugLog().Warn("interAgent origin-update dropped: no row with matching ThreadKey",
				"tool_name", ev.ToolName,
				"thread_key", threadKey,
				"correlation_id", ev.CorrelationID)
			return true
		}
		record, ok := buildInterAgentCompletionFallback(ev, currentAgentType)
		if !ok {
			return false
		}
		m.history.UpdateAt(idx, func(e *ChatEntry) {
			e.ToolCalls = append(e.ToolCalls, record)
			invalidateChatEntryRender(e)
		})
		m.syncPendingInterAgentEntry(idx)
		m.syncAllNestedInterAgentStreams()
		return true
	default:
		return false
	}
}

// SnapshotEntries returns a stable copy of the current chat history in
// chronological order.
func (m *Model) SnapshotEntries() []ChatEntry {
	if m == nil || m.history == nil {
		return nil
	}
	total := m.history.Len()
	if total == 0 {
		return nil
	}
	entries := make([]ChatEntry, 0, total)
	for idx := 0; idx < total; idx++ {
		entry := m.history.Get(idx)
		if entry == nil {
			continue
		}
		entries = append(entries, *entry)
	}
	return entries
}

func (m *Model) completeLocalInterAgentToolRecord(idx int, currentAgentType string, ev msg.ToolCallEventMsg) bool {
	matched := false
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		for i := len(e.ToolCalls) - 1; i >= 0; i-- {
			if !toolCallRecordCanAcceptCompletion(e.ToolCalls[i]) {
				continue
			}
			if !toolCallRecordMatchesEvent(e.ToolCalls[i], ev) {
				continue
			}
			if !updateInterAgentCompletion(&e.ToolCalls[i], ev) {
				return
			}
			invalidateChatEntryRender(e)
			matched = true
			return
		}
	})
	return matched
}

func (m *Model) applyInterAgentOriginUpdate(ev msg.ToolCallEventMsg, currentAgentType string) bool {
	row, ok := interAgentOriginUpdate(ev, currentAgentType)
	if !ok || row == nil || strings.TrimSpace(row.ThreadKey) == "" {
		return false
	}
	entryIdx, recordIdx, found := m.findInterAgentThread(row.ThreadKey)
	if !found {
		return false
	}
	m.history.UpdateAt(entryIdx, func(e *ChatEntry) {
		if recordIdx < 0 || recordIdx >= len(e.ToolCalls) || e.ToolCalls[recordIdx].InterAgent == nil {
			return
		}
		if len(row.AgentTypes) > 0 {
			e.ToolCalls[recordIdx].InterAgent.AgentTypes = append([]string(nil), row.AgentTypes...)
		}
		e.ToolCalls[recordIdx].InterAgent.Summary = row.Summary
		e.ToolCalls[recordIdx].InterAgent.Status = row.Status
		e.ToolCalls[recordIdx].InterAgent.ThreadKey = row.ThreadKey
		e.ToolCalls[recordIdx].Success = row.Status != InterAgentToolFailed
		e.ToolCalls[recordIdx].Completed = true
		invalidateChatEntryRender(e)
	})
	m.syncPendingInterAgentEntry(entryIdx)
	return true
}

// applyInterAgentOriginUpdateAnywhere is the unified cross-scope handler for
// origin-update events. An origin-update semantically targets the *challenger's*
// row (e.g. validate_work from tester → inspector's challenge_agent row). That
// row can live in a top-level entry's ToolCalls, in a nested stream slot's
// activity.ToolCalls, or in the recursive children of either. Scanning only one
// scope — which is what the legacy list-scoped helpers did — silently missed
// matches in the other scopes and the fallback-append path synthesized phantom
// duplicate rows to fill the gap. This helper searches all three scopes by
// ThreadKey and returns true if any row was updated.
func (m *Model) applyInterAgentOriginUpdateAnywhere(ev msg.ToolCallEventMsg, currentAgentType string) bool {
	if m.applyInterAgentOriginUpdate(ev, currentAgentType) {
		return true
	}
	row, ok := interAgentOriginUpdate(ev, currentAgentType)
	if !ok || row == nil || strings.TrimSpace(row.ThreadKey) == "" {
		return false
	}
	threadKey := strings.TrimSpace(row.ThreadKey)
	for _, slot := range m.nestedStreams {
		if slot == nil {
			continue
		}
		if applyInterAgentOriginUpdateToList(&slot.activity.ToolCalls, threadKey, row) {
			m.syncPendingNestedStream(slot)
			return true
		}
	}
	// After a nested slot is merged into its parent row and the slot is
	// cleared, the only remaining scope holding the ThreadKey is inside
	// entry.ToolCalls[i].InterAgent.Children[j].ToolCalls[k]. findInterAgentThread
	// only inspects top-level entry.ToolCalls, so we walk each entry's calls
	// recursively here as the final scope before giving up.
	matched := false
	for idx := m.history.Len() - 1; idx >= 0 && !matched; idx-- {
		entryIdx := idx
		m.history.UpdateAt(entryIdx, func(e *ChatEntry) {
			if applyInterAgentOriginUpdateToList(&e.ToolCalls, threadKey, row) {
				invalidateChatEntryRender(e)
				matched = true
			}
		})
		if matched {
			m.syncPendingInterAgentEntry(entryIdx)
			return true
		}
	}
	return false
}

// applyInterAgentOriginUpdateToList walks a tool-call list (including its
// InterAgent children recursively) looking for a row whose ThreadKey matches.
// Returns true on the first match after updating it in place.
func applyInterAgentOriginUpdateToList(calls *[]ToolCallRecord, threadKey string, row *InterAgentTool) bool {
	if calls == nil || row == nil {
		return false
	}
	for i := range *calls {
		tc := &(*calls)[i]
		if tc.InterAgent == nil {
			continue
		}
		if strings.TrimSpace(tc.InterAgent.ThreadKey) == threadKey {
			if len(row.AgentTypes) > 0 {
				tc.InterAgent.AgentTypes = append([]string(nil), row.AgentTypes...)
			}
			tc.InterAgent.Summary = row.Summary
			tc.InterAgent.Status = row.Status
			tc.InterAgent.ThreadKey = row.ThreadKey
			tc.Success = row.Status != InterAgentToolFailed
			tc.Completed = true
			return true
		}
		for j := range tc.InterAgent.Children {
			child := &tc.InterAgent.Children[j]
			if applyInterAgentOriginUpdateToList(&child.ToolCalls, threadKey, row) {
				return true
			}
		}
	}
	return false
}

func (m *Model) findInterAgentThread(threadKey string) (int, int, bool) {
	threadKey = strings.TrimSpace(threadKey)
	if threadKey == "" {
		return -1, -1, false
	}
	for idx := m.history.Len() - 1; idx >= 0; idx-- {
		entry := m.history.Get(idx)
		if entry == nil {
			continue
		}
		for recordIdx := len(entry.ToolCalls) - 1; recordIdx >= 0; recordIdx-- {
			row := entry.ToolCalls[recordIdx].InterAgent
			if row == nil {
				continue
			}
			if strings.TrimSpace(row.ThreadKey) == threadKey {
				return idx, recordIdx, true
			}
		}
	}
	return -1, -1, false
}

func (m *Model) syncPendingInterAgentEntry(idx int) {
	if idx < 0 {
		return
	}
	entry := m.history.Get(idx)
	if entry == nil || !entryHasPendingInterAgentToolCalls(entry) {
		delete(m.pendingInterAgent, idx)
		return
	}
	m.pendingInterAgent[idx] = struct{}{}
}

func (m *Model) handleNestedStreamStart(start msg.StreamStartMsg) {
	slot := m.ensureNestedStreamSlot(start.CorrelationID, start.BranchRef)
	if slot == nil {
		return
	}
	now := time.Now()
	if start.AgentID != "" {
		slot.activity.AgentID = strings.TrimSpace(start.AgentID)
	}
	if agentType := streamEntryAgentType(start); agentType != "" {
		slot.activity.AgentType = agentType
	}
	if parentID := strings.TrimSpace(start.BranchRef.ParentCorrelationID); parentID != "" {
		m.noteResumableChildOwner(start.CorrelationID, parentID)
	}
	slot.terminalSeen = false
	slot.terminalFailed = false
	if slot.done {
		slot.content.Reset()
		slot.done = false
		slot.activity.ToolCalls = nil
		slot.activity.ResultSummary = ""
		slot.activity.Completed = false
		slot.activity.Failed = false
	}
	slot.thinkingStart = now
	slot.progress.clear()
	slot.activity.ThinkingStartedAt = now
	m.renderNestedStreamThinking(slot, now)
	m.syncPendingNestedStream(slot)
}

func (m *Model) handleNestedStreamChunk(chunk msg.StreamChunkMsg) bool {
	slot := m.nestedStream(chunk.CorrelationID)
	if slot == nil {
		return false
	}
	slot.content.WriteString(chunk.Text)
	return true
}

func (m *Model) handleNestedStreamProgress(progress msg.StreamProgressMsg) bool {
	slot := m.nestedStream(progress.CorrelationID)
	if slot == nil && progress.BranchRef != nil {
		slot = m.ensureNestedStreamSlot(progress.CorrelationID, progress.BranchRef)
	}
	if slot == nil {
		return false
	}
	if progress.AgentID != "" {
		slot.activity.AgentID = strings.TrimSpace(progress.AgentID)
	}
	if agentType := streamEntryAgentType(msg.StreamStartMsg{
		AgentID:   progress.AgentID,
		AgentType: progress.AgentType,
	}); agentType != "" {
		slot.activity.AgentType = agentType
	}
	message := sanitizeThinkingMessage(progress.Message)
	if staleGuardianInterAgentProgress(message, slot.activity.ToolCalls) {
		// Suppress redundant guardian progress text; preserve the
		// nested child's spinner. See applySlotProgress for rationale.
		return true
	}
	if message == "" &&
		slot.thinkingStart.IsZero() &&
		strings.TrimSpace(slot.activity.ThinkingText) == "" &&
		strings.TrimSpace(slot.activity.ThinkingStatus) == "" &&
		len(slot.activity.ToolCalls) == 0 &&
		strings.TrimSpace(slot.activity.ResultSummary) == "" {
		return true
	}
	if message != "" {
		slot.progress.queue(message, progress.Sequence, progress.ToolDerived, progress.Watchdog)
	}
	if slot.thinkingStart.IsZero() {
		slot.thinkingStart = time.Now()
	}
	now := time.Now()
	if slot.progress.canPromote(now) && slot.progress.promotePending(now) {
		m.renderNestedStreamThinking(slot, now)
		m.syncPendingNestedStream(slot)
	}
	return true
}

func (m *Model) handleNestedStreamComplete(done msg.StreamCompleteMsg) bool {
	slot := m.nestedStream(done.CorrelationID)
	if slot == nil && done.BranchRef != nil {
		slot = m.ensureNestedStreamSlot(done.CorrelationID, done.BranchRef)
	}
	if slot == nil {
		return false
	}
	if done.AgentID != "" {
		slot.activity.AgentID = strings.TrimSpace(done.AgentID)
	}
	if agentType := streamEntryAgentType(msg.StreamStartMsg{
		AgentID:   done.AgentID,
		AgentType: done.AgentType,
	}); agentType != "" {
		slot.activity.AgentType = agentType
	}
	summary := summarizeNestedStreamText(firstNonEmptyString(
		done.AuthoritativeText,
		slot.content.String(),
	))
	slot.activity.ResultSummary = summary
	slot.activity.ThinkingStartedAt = time.Time{}
	slot.activity.ThinkingText = ""
	slot.activity.ThinkingStatus = ""
	slot.activity.ThinkingColor = ""
	slot.thinkingStart = time.Time{}
	slot.progress.clear()
	slot.terminalSeen = true
	slot.terminalFailed = false
	finalizeNestedStreamToolCallsOnTerminal(slot.activity.ToolCalls, time.Now(), true, summary)
	m.finalizeNestedStreamSlotIfReady(slot, time.Now())
	m.syncPendingNestedStream(slot)
	// Settle any Pending challenge rows in the parent entry that were waiting
	// on this nested child. The parent CID is on the branchRef. Without this
	// the row spins forever for non-protocol-prefixed thread keys (the
	// reroute-only settle path doesn't fire for direct nested completions).
	if parentCID := strings.TrimSpace(slot.branchRef.ParentCorrelationID); parentCID != "" {
		if parentIdx := m.historyIndexForCorrelation(parentCID); parentIdx >= 0 {
			m.settleChallengeRowsForChildCIDInEntry(parentIdx, strings.TrimSpace(done.CorrelationID), true)
		}
	}
	return true
}

func (m *Model) handleNestedStreamError(errMsg msg.StreamErrorMsg) bool {
	slot := m.nestedStream(errMsg.CorrelationID)
	if slot == nil && errMsg.BranchRef != nil {
		slot = m.ensureNestedStreamSlot(errMsg.CorrelationID, errMsg.BranchRef)
	}
	if slot == nil {
		return false
	}
	slot.activity.ResultSummary = summarizeNestedStreamText(formatErrorForChat(errMsg.Err))
	slot.activity.ThinkingStartedAt = time.Time{}
	slot.activity.ThinkingText = ""
	slot.activity.ThinkingStatus = ""
	slot.activity.ThinkingColor = ""
	slot.thinkingStart = time.Time{}
	slot.progress.clear()
	slot.terminalSeen = true
	slot.terminalFailed = true
	finalizeNestedStreamToolCallsOnTerminal(slot.activity.ToolCalls, time.Now(), false, slot.activity.ResultSummary)
	m.finalizeNestedStreamSlotIfReady(slot, time.Now())
	m.syncPendingNestedStream(slot)
	if parentCID := strings.TrimSpace(slot.branchRef.ParentCorrelationID); parentCID != "" {
		if parentIdx := m.historyIndexForCorrelation(parentCID); parentIdx >= 0 {
			m.settleChallengeRowsForChildCIDInEntry(parentIdx, strings.TrimSpace(errMsg.CorrelationID), false)
		}
	}
	return true
}

func (m *Model) handleNestedToolCallEvent(ev msg.ToolCallEventMsg) bool {
	slot := m.nestedStream(ev.CorrelationID)
	if slot == nil && ev.BranchRef != nil {
		slot = m.ensureNestedStreamSlot(ev.CorrelationID, ev.BranchRef)
	}
	if slot == nil {
		return false
	}
	// Parent-correlation preservation: every nested tool-call event
	// (guardian approval start/complete, consult lifecycle, challenge
	// roundtrip) carries the parent correlation via BranchRef. Refresh
	// the parent's thinking indicator so the spinner stays alive for
	// the full duration of the child's work, not just during the
	// parent's own LLM streaming windows. Without this, the parent's
	// row goes dark the moment its LLM stops streaming — even though
	// the turn is still in progress via the child.
	if ev.AgentID != "" {
		slot.activity.AgentID = strings.TrimSpace(ev.AgentID)
		if strings.TrimSpace(slot.activity.AgentType) == "" {
			slot.activity.AgentType = strings.TrimSpace(ev.AgentID)
		}
	}
	currentAgentType := nestedActivityAgentType(&slot.activity)
	cb := interAgentDispatchCallbacks{
		crossScopeOriginUpdate: func(ev msg.ToolCallEventMsg) bool {
			return m.applyInterAgentOriginUpdateAnywhere(ev, currentAgentType)
		},
	}
	if handleInterAgentToolCallInList(&slot.activity.ToolCalls, currentAgentType, ev, cb) {
		m.reconcileToolDerivedNestedProgress(slot)
		m.syncPendingNestedStream(slot)
		return true
	}

	switch ev.Phase {
	case 0:
		slot.activity.ToolCalls = append(slot.activity.ToolCalls, ToolCallRecord{
			ToolCallKey: ev.ToolCallKey,
			ToolName:    ev.ToolName,
			ArgsSummary: ev.ArgsSummary,
			FullArgs:    ev.FullArgs,
			StartedAt:   ev.StartedAt,
		})
	case 1:
		for i := len(slot.activity.ToolCalls) - 1; i >= 0; i-- {
			if !toolCallRecordCanAcceptCompletion(slot.activity.ToolCalls[i]) {
				continue
			}
			if !toolCallRecordMatchesEvent(slot.activity.ToolCalls[i], ev) {
				continue
			}
			if slot.activity.ToolCalls[i].StartedAt.IsZero() {
				slot.activity.ToolCalls[i].StartedAt = ev.StartedAt
			}
			slot.activity.ToolCalls[i].Duration = ev.Duration
			slot.activity.ToolCalls[i].Success = ev.Success
			slot.activity.ToolCalls[i].Completed = true
			slot.activity.ToolCalls[i].SyntheticCompletion = false
			slot.activity.ToolCalls[i].Output = ev.Output
			slot.activity.ToolCalls[i].ErrorMsg = ev.ErrorMsg
			if strings.TrimSpace(slot.activity.ToolCalls[i].ToolCallKey) == "" {
				slot.activity.ToolCalls[i].ToolCallKey = strings.TrimSpace(ev.ToolCallKey)
			}
			if !ev.Success {
				slot.activity.ToolCalls[i].Expanded = true
			}
			break
		}
	}
	m.reconcileToolDerivedNestedProgress(slot)
	m.finalizeNestedStreamSlotIfReady(slot, time.Now())
	m.syncPendingNestedStream(slot)
	return true
}

func (m *Model) finalizeNestedStreamSlotIfReady(slot *nestedStreamSlot, doneAt time.Time) {
	if slot == nil || !slot.terminalSeen || slot.done {
		return
	}
	for i := range slot.activity.ToolCalls {
		if toolCallHasActiveVisual(slot.activity.ToolCalls[i]) {
			return
		}
	}
	finalizeToolCallsSynthetic(slot.activity.ToolCalls, doneAt, !slot.terminalFailed, slot.activity.ResultSummary)
	slot.activity.Completed = true
	slot.activity.Failed = slot.terminalFailed
	slot.done = true
}

func staleGuardianInterAgentProgress(text string, calls []ToolCallRecord) bool {
	text = strings.TrimSpace(text)
	if text == "" || hasActiveToolCallVisual(calls) || !hasResolvedGuardianInterAgentToolCall(calls) {
		return false
	}
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "guardian") {
		return false
	}
	for _, keyword := range []string{"approval", "check", "consult", "review", "validation", "validate", "received"} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func hasActiveToolCallVisual(calls []ToolCallRecord) bool {
	for i := range calls {
		if toolCallHasActiveVisual(calls[i]) {
			return true
		}
	}
	return false
}

func hasResolvedGuardianInterAgentToolCall(calls []ToolCallRecord) bool {
	type toolCallFrame struct {
		calls []ToolCallRecord
	}

	stack := []toolCallFrame{{calls: calls}}
	for len(stack) > 0 {
		frame := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for i := range frame.calls {
			record := frame.calls[i]
			if record.InterAgent != nil {
				if record.Completed &&
					record.InterAgent.Status != InterAgentToolPending &&
					interAgentTargetsAgent(record.InterAgent, "guardian") {
					return true
				}
				for childIdx := len(record.InterAgent.Children) - 1; childIdx >= 0; childIdx-- {
					childCalls := record.InterAgent.Children[childIdx].ToolCalls
					if len(childCalls) == 0 {
						continue
					}
					stack = append(stack, toolCallFrame{calls: childCalls})
				}
			}
		}
	}
	return false
}

func interAgentTargetsAgent(row *InterAgentTool, target string) bool {
	if row == nil {
		return false
	}
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return false
	}
	for _, agentType := range row.AgentTypes {
		if strings.TrimSpace(strings.ToLower(agentType)) == target {
			return true
		}
	}
	return false
}

func (m *Model) nestedStream(correlationID string) *nestedStreamSlot {
	if strings.TrimSpace(correlationID) == "" {
		return nil
	}
	if m.nestedStreams == nil {
		return nil
	}
	return m.nestedStreams[correlationID]
}

func (m *Model) clearExplicitTopLevelTransferState(correlationID, parentCorrelationID string, branchRef *msg.InterAgentBranchRefMsg, topLevelTransfer bool) {
	if !isExplicitTopLevelTransfer(parentCorrelationID, branchRef, topLevelTransfer) || m == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	delete(m.resumableChildOwners, correlationID)
	parentIDs := make([]string, 0, 2)
	if slot := m.nestedStream(correlationID); slot != nil {
		if parentID := strings.TrimSpace(slot.branchRef.ParentCorrelationID); parentID != "" {
			parentIDs = append(parentIDs, parentID)
		}
	}
	if parentID := strings.TrimSpace(parentCorrelationID); parentID != "" {
		seen := false
		for _, existing := range parentIDs {
			if existing == parentID {
				seen = true
				break
			}
		}
		if !seen {
			parentIDs = append(parentIDs, parentID)
		}
	}
	delete(m.nestedStreams, correlationID)
	changed := false
	for _, parentID := range parentIDs {
		if m.pruneInterAgentChildActivity(parentID, correlationID) {
			changed = true
		}
	}
	if changed {
		m.syncPendingInterAgentIndices()
		m.viewDirty = true
	}
}

func (m *Model) ensureNestedStreamSlot(correlationID string, ref *msg.InterAgentBranchRefMsg) *nestedStreamSlot {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" || ref == nil {
		return nil
	}
	if m.nestedStreams == nil {
		m.nestedStreams = make(map[string]*nestedStreamSlot)
	}
	if slot, ok := m.nestedStreams[correlationID]; ok {
		slot.branchRef = *ref
		if slot.activity.CorrelationID == "" {
			slot.activity.CorrelationID = correlationID
		}
		return slot
	}
	slot := &nestedStreamSlot{
		correlationID: correlationID,
		branchRef:     *ref,
		activity: InterAgentChildActivity{
			CorrelationID: correlationID,
		},
	}
	m.nestedStreams[correlationID] = slot
	return slot
}

func (m *Model) pruneInterAgentChildActivity(parentCorrelationID, childCorrelationID string) bool {
	parentCorrelationID = strings.TrimSpace(parentCorrelationID)
	childCorrelationID = strings.TrimSpace(childCorrelationID)
	if parentCorrelationID == "" || childCorrelationID == "" {
		return false
	}
	changed := false
	for idx := 0; idx < m.history.Len(); idx++ {
		entryChanged := false
		m.history.UpdateAt(idx, func(entry *ChatEntry) {
			if entry == nil {
				return
			}
			// Try the entry's primary CID and any additional CIDs recorded
			// from later top-level-transfer resumptions. The branch ref's
			// parentCorrelationID may match either.
			pruned := false
			for _, ownerCID := range entryCorrelationIDs(entry) {
				if pruneInterAgentChildActivityInToolCalls(
					ownerCID,
					entry.ToolCalls,
					parentCorrelationID,
					childCorrelationID,
				) {
					pruned = true
					break
				}
			}
			if !pruned {
				return
			}
			invalidateChatEntryRender(entry)
			entryChanged = true
		})
		if entryChanged {
			changed = true
		}
	}
	return changed
}

func pruneInterAgentChildActivityInToolCalls(
	ownerCorrelationID string,
	calls []ToolCallRecord,
	parentCorrelationID, childCorrelationID string,
) bool {
	ownerCorrelationID = strings.TrimSpace(ownerCorrelationID)
	parentCorrelationID = strings.TrimSpace(parentCorrelationID)
	childCorrelationID = strings.TrimSpace(childCorrelationID)
	if len(calls) == 0 || parentCorrelationID == "" || childCorrelationID == "" {
		return false
	}
	changed := false
	for i := range calls {
		row := calls[i].InterAgent
		if row == nil {
			continue
		}
		if ownerCorrelationID == parentCorrelationID && len(row.Children) > 0 {
			kept := row.Children[:0]
			for _, child := range row.Children {
				if strings.TrimSpace(child.CorrelationID) == childCorrelationID {
					changed = true
					continue
				}
				kept = append(kept, child)
			}
			row.Children = kept
		}
		for j := range row.Children {
			if pruneInterAgentChildActivityInToolCalls(
				strings.TrimSpace(row.Children[j].CorrelationID),
				row.Children[j].ToolCalls,
				parentCorrelationID,
				childCorrelationID,
			) {
				changed = true
			}
		}
	}
	return changed
}

func (m *Model) syncPendingNestedStream(slot *nestedStreamSlot) bool {
	if slot == nil {
		return false
	}
	synced := m.updateInterAgentBranch(slot.branchRef, func(row *InterAgentTool) {
		upsertInterAgentChildActivity(row, slot.activity)
	})
	if synced {
		m.syncPendingInterAgentIndices()
		m.viewDirty = true
		if slot.done {
			delete(m.nestedStreams, slot.correlationID)
		}
	}
	return synced
}

func (m *Model) syncAllNestedInterAgentStreams() {
	for _, slot := range m.nestedStreams {
		m.syncPendingNestedStream(slot)
	}
}

func (m *Model) syncPendingInterAgentIndices() {
	for idx := 0; idx < m.history.Len(); idx++ {
		m.syncPendingInterAgentEntry(idx)
	}
}

func (m *Model) tickNestedStreamThinking(now time.Time) bool {
	updated := false
	for _, slot := range m.nestedStreams {
		if slot == nil || slot.done {
			continue
		}
		if slot.progress.canPromote(now) && slot.progress.promotePending(now) {
			updated = true
		}
		if m.renderNestedStreamThinking(slot, now) {
			if m.syncPendingNestedStream(slot) {
				updated = true
			}
		}
	}
	return updated
}

func (m *Model) tickPendingInterAgentHistory(now time.Time) bool {
	if len(m.pendingInterAgent) == 0 {
		return false
	}
	liveNested := make(map[string]struct{}, len(m.nestedStreams))
	for correlationID, slot := range m.nestedStreams {
		if slot == nil || slot.done {
			continue
		}
		correlationID = strings.TrimSpace(correlationID)
		if correlationID == "" {
			continue
		}
		liveNested[correlationID] = struct{}{}
	}

	updated := false
	for idx := range m.pendingInterAgent {
		entryUpdated := false
		m.history.UpdateAt(idx, func(entry *ChatEntry) {
			if entry == nil {
				return
			}
			if tickPendingInterAgentHistoryToolCalls(entry.ToolCalls, now, liveNested, m.thinkingColorFor) {
				invalidateChatEntryRender(entry)
				entryUpdated = true
			}
		})
		if entryUpdated {
			updated = true
		}
	}
	return updated
}

func tickPendingInterAgentHistoryToolCalls(
	calls []ToolCallRecord,
	now time.Time,
	liveNested map[string]struct{},
	colorFor func(time.Duration) string,
) bool {
	updated := false
	for i := range calls {
		if tickPendingInterAgentHistoryRow(calls[i].InterAgent, now, liveNested, colorFor) {
			updated = true
		}
	}
	return updated
}

func tickPendingInterAgentHistoryRow(
	row *InterAgentTool,
	now time.Time,
	liveNested map[string]struct{},
	colorFor func(time.Duration) string,
) bool {
	if row == nil {
		return false
	}
	updated := false
	for i := range row.Children {
		if tickPendingInterAgentHistoryChild(&row.Children[i], now, liveNested, colorFor) {
			updated = true
		}
		if tickPendingInterAgentHistoryToolCalls(row.Children[i].ToolCalls, now, liveNested, colorFor) {
			updated = true
		}
	}
	return updated
}

func tickPendingInterAgentHistoryChild(
	child *InterAgentChildActivity,
	now time.Time,
	liveNested map[string]struct{},
	colorFor func(time.Duration) string,
) bool {
	if child == nil || child.Completed || child.Failed || child.ThinkingStartedAt.IsZero() {
		return false
	}
	if correlationID := strings.TrimSpace(child.CorrelationID); correlationID != "" {
		if _, ok := liveNested[correlationID]; ok {
			return false
		}
	}
	elapsed := thinkingElapsed(child.ThinkingStartedAt, now)
	text := fmt.Sprintf("%s  %s", spinnerFrames[thinkingFrameAt(elapsed)], formatToolDuration(elapsed))
	color := colorFor(elapsed)
	if child.ThinkingText == text && child.ThinkingColor == color {
		return false
	}
	child.ThinkingText = text
	child.ThinkingColor = color
	return true
}

func (m *Model) renderNestedStreamThinking(slot *nestedStreamSlot, now time.Time) bool {
	if slot == nil {
		return false
	}
	if slot.thinkingStart.IsZero() {
		return false
	}
	if strings.TrimSpace(slot.progress.retryText) == "" {
		if slot.activity.ThinkingText == "" && slot.activity.ThinkingStatus == "" && slot.activity.ThinkingColor == "" {
			return false
		}
		slot.activity.ThinkingStartedAt = time.Time{}
		slot.activity.ThinkingText = ""
		slot.activity.ThinkingStatus = ""
		slot.activity.ThinkingColor = ""
		return true
	}
	elapsed := thinkingElapsed(slot.thinkingStart, now)
	text := fmt.Sprintf("%s  %s", spinnerFrames[thinkingFrameAt(elapsed)], formatToolDuration(elapsed))
	status := thinkingStatusFor(nestedActivityAgentType(&slot.activity), slot.progress.retryText, elapsed)
	// Agent-state detail (explicit transition from the peer agent) takes
	// precedence over the rotating placeholder. Matches the primary-stream
	// path in renderThinkingEntry so peers narrate identically inline and
	// nested, with no silent gaps during their reasoning / tool-execution
	// phases. Retry text still wins because transient-failure messaging
	// is more urgent than ordinary state progress.
	if strings.TrimSpace(slot.progress.retryText) == "" {
		if detail := m.agentStateDetailForNestedCorrelation(slot.correlationID); detail != "" {
			status = detail
		}
	}
	color := m.thinkingColorFor(elapsed)
	if slot.activity.ThinkingText == text &&
		slot.activity.ThinkingStatus == status &&
		slot.activity.ThinkingColor == color {
		return false
	}
	slot.activity.ThinkingStartedAt = slot.thinkingStart
	slot.activity.ThinkingText = text
	slot.activity.ThinkingStatus = status
	slot.activity.ThinkingColor = color
	return true
}

// agentStateDetailForNestedCorrelation returns the agent-state detail for
// a nested child correlation so tickNestedStreamThinking surfaces the
// peer's current state in the inline inter-agent row. Returns "" when no
// non-terminal state is attached to the correlation; callers then fall
// back to the rotating placeholder message. Mirrors the primary-stream
// equivalent, agentStateDetailForEntry.
func (m *Model) agentStateDetailForNestedCorrelation(correlationID string) string {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" || m.agentStates == nil || len(m.agentStates) == 0 {
		return ""
	}
	state, ok := m.agentStates[correlationID]
	if !ok || state == nil {
		return ""
	}
	if detail := strings.TrimSpace(state.Detail); detail != "" {
		return detail
	}
	return humanizeAgentState(state.State)
}

func nestedActivityAgentType(activity *InterAgentChildActivity) string {
	if activity == nil {
		return ""
	}
	if agentType := strings.TrimSpace(activity.AgentType); agentType != "" {
		return agentType
	}
	return strings.TrimSpace(activity.AgentID)
}

func summarizeNestedStreamText(text string) string {
	return normalizeInlineText(text)
}

func (m *Model) updateInterAgentBranch(ref msg.InterAgentBranchRefMsg, fn func(*InterAgentTool)) bool {
	parentCorrelationID := strings.TrimSpace(ref.ParentCorrelationID)
	parentToolCallKey := strings.TrimSpace(ref.ParentToolCallKey)
	threadKey := strings.TrimSpace(ref.ThreadKey)
	kind := strings.TrimSpace(ref.Kind)
	if parentCorrelationID == "" {
		return false
	}
	found := false
	for idx := m.history.Len() - 1; idx >= 0 && !found; idx-- {
		m.history.UpdateAt(idx, func(entry *ChatEntry) {
			if found || entry == nil {
				return
			}
			// Try the entry's primary CID first, then any additional CIDs
			// recorded from later top-level-transfer resumptions. The branch
			// ref's parentCorrelationID may match either, depending on which
			// stream pass created the inter-agent row.
			for _, ownerCID := range entryCorrelationIDs(entry) {
				if updateInterAgentBranchInToolCalls(ownerCID, &entry.ToolCalls, parentCorrelationID, parentToolCallKey, threadKey, kind, fn) {
					invalidateChatEntryRender(entry)
					found = true
					return
				}
			}
		})
	}
	return found
}

// entryCorrelationIDs returns the entry's primary CID followed by any
// additional CIDs recorded across resumptions. Empty CIDs are skipped.
func entryCorrelationIDs(entry *ChatEntry) []string {
	if entry == nil {
		return nil
	}
	out := make([]string, 0, 1+len(entry.AdditionalCorrelationIDs))
	if cid := strings.TrimSpace(entry.CorrelationID); cid != "" {
		out = append(out, cid)
	}
	for _, alt := range entry.AdditionalCorrelationIDs {
		if cid := strings.TrimSpace(alt); cid != "" {
			out = append(out, cid)
		}
	}
	return out
}

func updateInterAgentBranchInToolCalls(
	ownerCorrelationID string,
	calls *[]ToolCallRecord,
	parentCorrelationID, parentToolCallKey, threadKey string,
	kind string,
	fn func(*InterAgentTool),
) bool {
	if calls == nil {
		return false
	}
	ownerCorrelationID = strings.TrimSpace(ownerCorrelationID)
	parentCorrelationID = strings.TrimSpace(parentCorrelationID)
	parentToolCallKey = strings.TrimSpace(parentToolCallKey)
	threadKey = strings.TrimSpace(threadKey)
	kind = strings.TrimSpace(kind)

	refs := collectInterAgentRowBindingRefs(ownerCorrelationID, calls)
	if len(refs) == 0 {
		return false
	}

	if parentToolCallKey != "" {
		for i := range refs {
			if refs[i].ownerCorrelationID != parentCorrelationID {
				continue
			}
			if refs[i].toolCallKey != parentToolCallKey {
				continue
			}
			fn(refs[i].row)
			return true
		}
	}

	if threadKey != "" {
		for i := range refs {
			if refs[i].threadKey != threadKey {
				continue
			}
			fn(refs[i].row)
			return true
		}
	}

	var fallbackCandidate *InterAgentTool
	for i := range refs {
		if refs[i].ownerCorrelationID != parentCorrelationID {
			continue
		}
		if kind != "" && refs[i].kind != kind {
			continue
		}
		if fallbackCandidate != nil {
			return false
		}
		fallbackCandidate = refs[i].row
	}
	if fallbackCandidate == nil {
		return false
	}
	fn(fallbackCandidate)
	return true
}

type interAgentRowBindingRef struct {
	ownerCorrelationID string
	toolCallKey        string
	threadKey          string
	kind               string
	row                *InterAgentTool
}

func collectInterAgentRowBindingRefs(ownerCorrelationID string, calls *[]ToolCallRecord) []interAgentRowBindingRef {
	if calls == nil {
		return nil
	}

	type interAgentRowBindingFrame struct {
		ownerCorrelationID string
		calls              *[]ToolCallRecord
		index              int
	}

	stack := []interAgentRowBindingFrame{{
		ownerCorrelationID: strings.TrimSpace(ownerCorrelationID),
		calls:              calls,
	}}
	refs := make([]interAgentRowBindingRef, 0, len(*calls))

	for len(stack) > 0 {
		frameIdx := len(stack) - 1
		frame := &stack[frameIdx]
		if frame.calls == nil {
			stack = stack[:frameIdx]
			continue
		}
		if frame.index >= len(*frame.calls) {
			stack = stack[:frameIdx]
			continue
		}

		record := &(*frame.calls)[frame.index]
		frame.index++
		row := record.InterAgent
		if row == nil {
			continue
		}
		refs = append(refs, interAgentRowBindingRef{
			ownerCorrelationID: frame.ownerCorrelationID,
			toolCallKey:        strings.TrimSpace(record.ToolCallKey),
			threadKey:          strings.TrimSpace(row.ThreadKey),
			kind:               strings.TrimSpace(string(row.Kind)),
			row:                row,
		})
		for childIdx := len(row.Children) - 1; childIdx >= 0; childIdx-- {
			child := &row.Children[childIdx]
			stack = append(stack, interAgentRowBindingFrame{
				ownerCorrelationID: strings.TrimSpace(child.CorrelationID),
				calls:              &child.ToolCalls,
			})
		}
	}

	return refs
}

func upsertInterAgentChildActivity(row *InterAgentTool, activity InterAgentChildActivity) {
	if row == nil {
		return
	}
	cloned := cloneInterAgentChildActivity(activity)
	childCorrelationID := strings.TrimSpace(cloned.CorrelationID)
	if childCorrelationID != "" {
		for i := range row.Children {
			if strings.TrimSpace(row.Children[i].CorrelationID) == childCorrelationID {
				row.Children[i] = mergeInterAgentChildActivity(row.Children[i], cloned)
				return
			}
		}
	}
	row.Children = append(row.Children, cloned)
}

func mergeInterAgentChildActivity(prev, next InterAgentChildActivity) InterAgentChildActivity {
	merged := cloneInterAgentChildActivity(prev)

	if correlationID := strings.TrimSpace(next.CorrelationID); correlationID != "" {
		merged.CorrelationID = correlationID
	}
	if agentID := strings.TrimSpace(next.AgentID); agentID != "" {
		merged.AgentID = agentID
	}
	if agentType := strings.TrimSpace(next.AgentType); agentType != "" {
		merged.AgentType = agentType
	}
	if !next.ThinkingStartedAt.IsZero() {
		merged.ThinkingStartedAt = next.ThinkingStartedAt
	} else if next.Completed {
		merged.ThinkingStartedAt = time.Time{}
	}

	merged.ThinkingText = next.ThinkingText
	merged.ThinkingStatus = next.ThinkingStatus
	merged.ThinkingColor = next.ThinkingColor

	if next.ResultSummary != "" || next.Completed || next.Failed {
		merged.ResultSummary = next.ResultSummary
	}

	if next.Completed {
		merged.Completed = true
		merged.Failed = next.Failed
	} else if !prev.Completed {
		merged.Completed = false
		merged.Failed = false
	}

	merged.ToolCallsExpanded = prev.ToolCallsExpanded || next.ToolCallsExpanded
	merged.ToolCalls = mergeToolCallRecords(prev.ToolCalls, next.ToolCalls)
	preserveInterAgentChildUIState(&merged, prev)
	return merged
}

func preserveInterAgentChildUIState(next *InterAgentChildActivity, prev InterAgentChildActivity) {
	if next == nil {
		return
	}
	if prev.ToolCallsExpanded {
		next.ToolCallsExpanded = true
	}
	preserveToolCallTreeUIState(next.ToolCalls, prev.ToolCalls)
}

func preserveToolCallSliceUIState(next []ToolCallRecord, prev []ToolCallRecord) {
	preserveToolCallTreeUIState(next, prev)
}

func preserveToolCallTreeUIState(nextRoot []ToolCallRecord, prevRoot []ToolCallRecord) {
	if len(nextRoot) == 0 || len(prevRoot) == 0 {
		return
	}
	type toolCallUIPreserveFrame struct {
		next []ToolCallRecord
		prev []ToolCallRecord
	}

	stack := []toolCallUIPreserveFrame{{
		next: nextRoot,
		prev: prevRoot,
	}}
	for len(stack) > 0 {
		frame := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(frame.next) == 0 || len(frame.prev) == 0 {
			continue
		}
		used := make([]bool, len(frame.prev))
		for i := range frame.next {
			match := -1
			for j := 0; j < len(frame.prev); j++ {
				if used[j] {
					continue
				}
				if !toolCallRecordsShareIdentity(frame.next[i], frame.prev[j]) {
					continue
				}
				match = j
				break
			}
			if match < 0 {
				continue
			}
			used[match] = true
			if frame.prev[match].Expanded {
				frame.next[i].Expanded = true
			}
			nextInterAgent := frame.next[i].InterAgent
			prevInterAgent := frame.prev[match].InterAgent
			if nextInterAgent == nil || prevInterAgent == nil || len(nextInterAgent.Children) == 0 || len(prevInterAgent.Children) == 0 {
				continue
			}
			prevByCorrelation := make(map[string]int, len(prevInterAgent.Children))
			for j := range prevInterAgent.Children {
				correlationID := strings.TrimSpace(prevInterAgent.Children[j].CorrelationID)
				if correlationID == "" {
					continue
				}
				prevByCorrelation[correlationID] = j
			}
			for j := range nextInterAgent.Children {
				correlationID := strings.TrimSpace(nextInterAgent.Children[j].CorrelationID)
				if correlationID == "" {
					continue
				}
				prevChildIdx, ok := prevByCorrelation[correlationID]
				if !ok {
					continue
				}
				if prevInterAgent.Children[prevChildIdx].ToolCallsExpanded {
					nextInterAgent.Children[j].ToolCallsExpanded = true
				}
				stack = append(stack, toolCallUIPreserveFrame{
					next: nextInterAgent.Children[j].ToolCalls,
					prev: prevInterAgent.Children[prevChildIdx].ToolCalls,
				})
			}
		}
	}
}

func mergeToolCallRecords(prev, next []ToolCallRecord) []ToolCallRecord {
	if len(prev) == 0 {
		return cloneToolCallRecords(next)
	}
	if len(next) == 0 {
		return cloneToolCallRecords(prev)
	}

	merged := cloneToolCallRecords(prev)
	usedNext := make([]bool, len(next))
	for i := range merged {
		match := -1
		for j := range next {
			if usedNext[j] {
				continue
			}
			if !toolCallRecordsShareIdentity(merged[i], next[j]) {
				continue
			}
			match = j
			break
		}
		if match < 0 {
			continue
		}
		usedNext[match] = true
		mergeToolCallRecord(&merged[i], next[match])
	}
	for i := range next {
		if usedNext[i] {
			continue
		}
		merged = append(merged, cloneToolCallRecords(next[i:i+1])...)
	}
	return merged
}

func mergeToolCallRecord(prev *ToolCallRecord, next ToolCallRecord) {
	if prev == nil {
		return
	}
	if key := strings.TrimSpace(next.ToolCallKey); key != "" {
		prev.ToolCallKey = key
	}
	if name := strings.TrimSpace(next.ToolName); name != "" {
		prev.ToolName = name
	}
	if summary := strings.TrimSpace(next.ArgsSummary); summary != "" {
		prev.ArgsSummary = next.ArgsSummary
	}
	if args := strings.TrimSpace(next.FullArgs); args != "" {
		prev.FullArgs = next.FullArgs
	}
	if output := strings.TrimSpace(next.Output); output != "" || next.Completed {
		prev.Output = next.Output
	}
	if errMsg := strings.TrimSpace(next.ErrorMsg); errMsg != "" || next.Completed {
		prev.ErrorMsg = next.ErrorMsg
	}
	if !next.StartedAt.IsZero() {
		prev.StartedAt = next.StartedAt
	}
	if next.Duration > 0 {
		prev.Duration = next.Duration
	}
	if next.Completed {
		prev.Completed = true
		prev.Success = next.Success
		prev.SyntheticCompletion = next.SyntheticCompletion
	} else if !prev.Completed {
		prev.Success = next.Success
	}
	if next.Expanded {
		prev.Expanded = true
	}
	if next.InterAgent != nil {
		if prev.InterAgent == nil {
			row := *next.InterAgent
			row.AgentTypes = append([]string(nil), next.InterAgent.AgentTypes...)
			row.Children = cloneInterAgentChildren(next.InterAgent.Children)
			prev.InterAgent = &row
			return
		}
		mergeInterAgentTool(prev.InterAgent, *next.InterAgent)
	}
}

func mergeInterAgentTool(prev *InterAgentTool, next InterAgentTool) {
	if prev == nil {
		return
	}
	if next.Kind != "" {
		prev.Kind = next.Kind
	}
	if threadKey := strings.TrimSpace(next.ThreadKey); threadKey != "" {
		prev.ThreadKey = threadKey
	}
	if len(next.AgentTypes) > 0 {
		prev.AgentTypes = append([]string(nil), next.AgentTypes...)
	}
	if summary := strings.TrimSpace(next.Summary); summary != "" {
		prev.Summary = summary
	}
	prev.Status = mergeInterAgentToolStatus(prev.Status, next.Status)
	prev.Children = mergeInterAgentChildren(prev.Children, next.Children)
}

func mergeInterAgentToolStatus(prev, next InterAgentToolStatus) InterAgentToolStatus {
	if strings.TrimSpace(string(next)) == "" {
		return prev
	}
	if (prev == InterAgentToolDone || prev == InterAgentToolFailed) && next == InterAgentToolPending {
		return prev
	}
	return next
}

func mergeInterAgentChildren(prev, next []InterAgentChildActivity) []InterAgentChildActivity {
	if len(prev) == 0 {
		return cloneInterAgentChildren(next)
	}
	if len(next) == 0 {
		return cloneInterAgentChildren(prev)
	}

	merged := cloneInterAgentChildren(prev)
	usedNext := make([]bool, len(next))
	for i := range merged {
		match := -1
		prevCorrelationID := strings.TrimSpace(merged[i].CorrelationID)
		for j := range next {
			if usedNext[j] {
				continue
			}
			nextCorrelationID := strings.TrimSpace(next[j].CorrelationID)
			if prevCorrelationID == "" || nextCorrelationID == "" || prevCorrelationID != nextCorrelationID {
				continue
			}
			match = j
			break
		}
		if match < 0 {
			continue
		}
		usedNext[match] = true
		merged[i] = mergeInterAgentChildActivity(merged[i], next[match])
	}
	for i := range next {
		if usedNext[i] {
			continue
		}
		merged = append(merged, cloneInterAgentChildActivity(next[i]))
	}
	return merged
}

// toolCallRecordsShareIdentity reports whether two records describe the same
// tool call lifecycle. Every record carries the provider-stamped lifecycle
// ID in ToolCallKey, so this is exact-key equality.
func toolCallRecordsShareIdentity(left, right ToolCallRecord) bool {
	leftKey := strings.TrimSpace(left.ToolCallKey)
	rightKey := strings.TrimSpace(right.ToolCallKey)
	return leftKey != "" && leftKey == rightKey
}

func cloneInterAgentChildActivity(activity InterAgentChildActivity) InterAgentChildActivity {
	out := activity
	out.ToolCalls = cloneToolCallRecords(activity.ToolCalls)
	return out
}

func cloneToolCallRecords(in []ToolCallRecord) []ToolCallRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]ToolCallRecord, len(in))
	populateClonedInterAgentTree(toolCloneFrame{src: in, dst: out})
	return out
}

func cloneInterAgentChildren(in []InterAgentChildActivity) []InterAgentChildActivity {
	if len(in) == 0 {
		return nil
	}
	out := make([]InterAgentChildActivity, len(in))
	populateClonedInterAgentTree(childCloneFrame{src: in, dst: out})
	return out
}

type toolCloneFrame struct {
	src []ToolCallRecord
	dst []ToolCallRecord
}

type childCloneFrame struct {
	src []InterAgentChildActivity
	dst []InterAgentChildActivity
}

func populateClonedInterAgentTree(root any) {
	stack := []any{root}
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch frame := item.(type) {
		case toolCloneFrame:
			for i := range frame.src {
				frame.dst[i] = frame.src[i]
				if frame.src[i].InterAgent == nil {
					frame.dst[i].InterAgent = nil
					continue
				}
				row := *frame.src[i].InterAgent
				row.AgentTypes = append([]string(nil), frame.src[i].InterAgent.AgentTypes...)
				if len(frame.src[i].InterAgent.Children) > 0 {
					row.Children = make([]InterAgentChildActivity, len(frame.src[i].InterAgent.Children))
					stack = append(stack, childCloneFrame{
						src: frame.src[i].InterAgent.Children,
						dst: row.Children,
					})
				} else {
					row.Children = nil
				}
				frame.dst[i].InterAgent = &row
			}
		case childCloneFrame:
			for i := range frame.src {
				frame.dst[i] = frame.src[i]
				if len(frame.src[i].ToolCalls) > 0 {
					frame.dst[i].ToolCalls = make([]ToolCallRecord, len(frame.src[i].ToolCalls))
					stack = append(stack, toolCloneFrame{
						src: frame.src[i].ToolCalls,
						dst: frame.dst[i].ToolCalls,
					})
				} else {
					frame.dst[i].ToolCalls = nil
				}
			}
		}
	}
}

// interAgentDispatchCallbacks gives a list-scoped handler a way to reach
// outside its list when it encounters an event that is semantically
// cross-scope (origin-updates, primarily). Without these, the list-scoped
// handler would fall through to buildInterAgentCompletionFallback and
// synthesize a phantom duplicate row — the root cause of the duplicate-
// agent-row bug class we're trying to eliminate.
type interAgentDispatchCallbacks struct {
	// crossScopeOriginUpdate attempts to apply an origin-update to any row
	// in the history (top-level entries, nested stream slots, or nested
	// child activities). Returns true if some row was patched.
	crossScopeOriginUpdate func(ev msg.ToolCallEventMsg) bool
}

func handleInterAgentToolCallInList(calls *[]ToolCallRecord, currentAgentType string, ev msg.ToolCallEventMsg, cb interAgentDispatchCallbacks) bool {
	if calls == nil {
		return false
	}
	switch ev.Phase {
	case 0:
		if eventIsInterAgentOriginUpdate(ev) {
			return true
		}
		record, ok := buildInterAgentStartRecord(ev)
		if !ok {
			return false
		}
		record.StartedAt = ev.StartedAt
		if idx, match := lastInterAgentTemplateMatch(*calls, &record); match {
			current := (*calls)[idx].InterAgent.RepeatCount
			if current < 2 {
				(*calls)[idx].InterAgent.RepeatCount = 2
			} else {
				(*calls)[idx].InterAgent.RepeatCount = current + 1
			}
			if record.StartedAt.After((*calls)[idx].StartedAt) {
				(*calls)[idx].StartedAt = record.StartedAt
			}
			(*calls)[idx].Completed = false
			(*calls)[idx].SyntheticCompletion = false
			(*calls)[idx].Duration = 0
			(*calls)[idx].InterAgent.Status = InterAgentToolPending
			(*calls)[idx].ToolCallKey = strings.TrimSpace(record.ToolCallKey)
			(*calls)[idx].FullArgs = record.FullArgs
			(*calls)[idx].ArgsSummary = record.ArgsSummary
			return true
		}
		*calls = append(*calls, record)
		return true
	case 1:
		for i := len(*calls) - 1; i >= 0; i-- {
			if !toolCallRecordCanAcceptCompletion((*calls)[i]) {
				continue
			}
			if !toolCallRecordMatchesEvent((*calls)[i], ev) {
				continue
			}
			if updateInterAgentCompletion(&(*calls)[i], ev) {
				return true
			}
		}
		row, ok := interAgentOriginUpdate(ev, currentAgentType)
		if ok && row != nil && strings.TrimSpace(row.ThreadKey) != "" {
			for i := len(*calls) - 1; i >= 0; i-- {
				if (*calls)[i].InterAgent == nil {
					continue
				}
				if strings.TrimSpace((*calls)[i].InterAgent.ThreadKey) != strings.TrimSpace(row.ThreadKey) {
					continue
				}
				(*calls)[i].InterAgent.AgentTypes = append([]string(nil), row.AgentTypes...)
				(*calls)[i].InterAgent.Summary = row.Summary
				(*calls)[i].InterAgent.Status = row.Status
				(*calls)[i].InterAgent.ThreadKey = row.ThreadKey
				(*calls)[i].Success = row.Status != InterAgentToolFailed
				(*calls)[i].Completed = true
				return true
			}
		}
		// Origin-updates are identity-addressed (ThreadKey) and must route
		// cross-scope when the local list doesn't contain the originator.
		// Falling through to buildInterAgentCompletionFallback here was
		// creating duplicate phantom rows in the nested stream's
		// activity.ToolCalls (e.g. an "inspector-pipeline" row appearing
		// as a child of the tester-pipeline nested slot). The caller's
		// crossScopeOriginUpdate callback walks every scope by ThreadKey;
		// on miss we log + drop because a missing originator row is a
		// routing defect upstream, not a display problem to paper over.
		if eventIsInterAgentOriginUpdate(ev) {
			if cb.crossScopeOriginUpdate != nil && cb.crossScopeOriginUpdate(ev) {
				return true
			}
			chatDebugLog().Warn("interAgent origin-update dropped from list scope: no row with matching ThreadKey",
				"tool_name", ev.ToolName,
				"thread_key", interAgentEventThreadKey(ev),
				"correlation_id", ev.CorrelationID)
			return true
		}
		record, ok := buildInterAgentCompletionFallback(ev, currentAgentType)
		if !ok {
			return false
		}
		*calls = append(*calls, record)
		return true
	default:
		return false
	}
}

// interAgentEventThreadKey extracts the ThreadKey carried on an event's
// InterAgent metadata, trimmed. Used for telemetry on origin-update misses.
func interAgentEventThreadKey(ev msg.ToolCallEventMsg) string {
	if ev.InterAgent == nil {
		return ""
	}
	return strings.TrimSpace(ev.InterAgent.ThreadKey)
}

func invalidateChatEntryRender(e *ChatEntry) {
	if e == nil {
		return
	}
	e.RenderedLines = nil
	e.CodeRegions = nil
	e.ToolCallRegions = nil
	e.Height = -1
}

// Clear discards all chat entries and resets the viewport to its initial state.
// Active streams and thinking indicators are cancelled.
func (m *Model) Clear() {
	m.history.Clear()
	m.clearThinkingState()
	m.clearSteeringState()
	clear(m.streams)
	clear(m.nestedStreams)
	m.streamRenderPending = false
	m.planEntryIdx = -1
	m.planID = ""
	m.highlightID = ""
	m.viewport.Reset()
	m.viewDirty = true
}

// ScrollUp scrolls the chat viewport up by one line.
// Returns true if the scroll was applied, false if at boundary.
func (m *Model) ScrollUp() bool {
	if m.viewport.ScrollUp() {
		m.viewDirty = true
		return true
	}
	return false
}

// ScrollDown scrolls the chat viewport down by one line.
// Returns true if the scroll was applied, false if at boundary.
func (m *Model) ScrollDown() bool {
	if m.viewport.ScrollDown() {
		m.viewDirty = true
		return true
	}
	return false
}

// SetBounceOffset updates the visual bounce displacement for rendering.
func (m *Model) SetBounceOffset(offset int) {
	m.viewport.SetBounceOffset(offset)
	m.viewDirty = true
}

// AbortStream discards the active accumulator and stream render state
// without finalizing content. Used when an interrupt makes the stream
// obsolete before a StreamCompleteMsg arrives.
func (m *Model) AbortStream() {
	if len(m.streams) == 0 {
		return
	}
	clear(m.streams)
	m.streamRenderPending = false

	// Clear plan tracking — prevents late plan updates from embedding
	// into the next agent's stream.
	m.planEntryIdx = -1
	m.planID = ""

	// Clear thinking color on the active entry so it doesn't persist
	// as muted after FinishThinking replaces the content.
	if m.thinkingIdx >= 0 {
		idx := m.thinkingIdx
		m.history.mu.Lock()
		if idx >= 0 && idx < m.history.count {
			physical := m.history.logicalToPhysical(idx)
			m.history.entries[physical].ThinkingColor = ""
		}
		m.history.mu.Unlock()
	}

	// Clear steering pending entries — prevents holographic shimmer
	// from persisting on stale entries.
	for _, sp := range m.steeringPending {
		idx := sp.idx
		m.history.mu.Lock()
		if idx >= 0 && idx < m.history.count {
			physical := m.history.logicalToPhysical(idx)
			m.history.entries[physical].SteeringPending = false
		}
		m.history.mu.Unlock()
	}
	m.steeringPending = nil

	m.viewport.ClearAllStreamStates()
	m.viewDirty = true
}

// IsStreaming reports whether a response is currently being streamed.
func (m *Model) IsStreaming() bool { return len(m.streams) > 0 }

// HasActiveAnimation reports whether any tick-driven animation is running
// (thinking spinner, highlight countdown, edge flash, or streaming).
// Uses !IsZero rather than Before for the highlight check so the decor tick
// chain keeps running until tickHighlight actually clears the viewport state.
func (m *Model) HasActiveAnimation() bool {
	now := time.Now()
	return m.thinkingIdx >= 0 ||
		len(m.streams) > 0 ||
		len(m.nestedStreams) > 0 ||
		len(m.pendingInterAgent) > 0 ||
		len(m.steeringPending) > 0 ||
		!m.highlightUntil.IsZero() ||
		m.viewport.HasEdgeFlash(now)
}

// EntryAtViewLine returns the chat entry visible at the given viewport-relative
// line (0 = top visible line). Returns nil if out of bounds.
func (m *Model) EntryAtViewLine(y int) *ChatEntry {
	return m.viewport.EntryAtViewLine(y)
}

// CopyTargetAtViewLine resolves a viewport-relative line to a CopyTarget
// describing what content to copy and what line range to highlight.
func (m *Model) CopyTargetAtViewLine(y int) *CopyTarget {
	return m.viewport.CopyTargetAtViewLine(y)
}

// CopyTargetAtRenderedViewLine resolves a viewport-relative line using the
// last rendered frame snapshot, falling back to live resolution when needed.
func (m *Model) CopyTargetAtRenderedViewLine(y int) *CopyTarget {
	if target := m.viewport.FrameCopyTargetAtViewLine(y); target != nil {
		return target
	}
	return m.CopyTargetAtViewLine(y)
}

// ToggleAtViewLine toggles the expandable tool or child inter-agent row at the
// given viewport-relative line. Returns true when a toggle was applied.
func (m *Model) ToggleAtViewLine(y int) bool {
	target := m.viewport.ToggleTargetAtViewLine(y)
	return m.applyToggleTarget(target)
}

// ToggleAtRenderedViewLine toggles the item that occupied the given
// viewport-relative line in the last rendered frame snapshot, falling back to
// live resolution if no frame target is available.
func (m *Model) ToggleAtRenderedViewLine(y int) bool {
	target := m.viewport.FrameToggleTargetAtViewLine(y)
	if target == nil {
		target = m.viewport.ToggleTargetAtViewLine(y)
	}
	return m.applyToggleTarget(target)
}

func (m *Model) applyToggleTarget(target *toggleTarget) bool {
	if target == nil {
		return false
	}
	if !m.toggleTarget(target) {
		return false
	}
	m.reselectAfterToggle(target)
	m.viewDirty = true
	return true
}

// ToggleSelected toggles the currently selected expandable region, if any.
func (m *Model) ToggleSelected() bool {
	if m.viewport.selectedIndex < 0 {
		return false
	}
	regions := m.viewport.regions(m.viewport.selectedIndex)
	if m.viewport.selectedRegion < 0 || m.viewport.selectedRegion >= len(regions) {
		return false
	}
	region := regions[m.viewport.selectedRegion]
	target := targetForSelectionRegion(m.viewport.selectedIndex, region)
	if target == nil {
		return false
	}
	if !m.toggleTarget(target) {
		return false
	}
	m.reselectAfterToggle(target)
	m.viewDirty = true
	return true
}

func targetForSelectionRegion(entryIndex int, region selectionRegion) *toggleTarget {
	switch region.kind {
	case selectionRegionToolCall:
		return &toggleTarget{
			entryIndex:    entryIndex,
			kind:          toggleTargetToolCall,
			toolCallIndex: region.toolCallIndex,
		}
	case selectionRegionToolCallOverflow:
		return &toggleTarget{
			entryIndex:     entryIndex,
			kind:           toggleTargetOverflow,
			toolCallIndex:  region.toolCallIndex,
			childIndex:     region.childIndex,
			childPath:      cloneIntSlice(region.childPath),
			interAgentPath: cloneIntSlice(region.interAgentPath),
		}
	case selectionRegionChildToolCall:
		return &toggleTarget{
			entryIndex:       entryIndex,
			kind:             toggleTargetChildToolCall,
			toolCallIndex:    region.toolCallIndex,
			childIndex:       region.childIndex,
			childToolCallIdx: region.childToolCallIdx,
			childPath:        cloneIntSlice(region.childPath),
			interAgentPath:   cloneIntSlice(region.interAgentPath),
		}
	default:
		return nil
	}
}

func (m *Model) resolveToggleEntryIndex(target *toggleTarget) int {
	if target == nil {
		return -1
	}
	if target.entryIndex >= 0 {
		if entry := m.history.Get(target.entryIndex); entry != nil {
			if strings.TrimSpace(target.entryID) == "" || strings.TrimSpace(entry.ID) == strings.TrimSpace(target.entryID) {
				return target.entryIndex
			}
		}
	}
	if entryID := strings.TrimSpace(target.entryID); entryID != "" {
		for idx := m.history.Len() - 1; idx >= 0; idx-- {
			entry := m.history.Get(idx)
			if entry != nil && strings.TrimSpace(entry.ID) == entryID {
				return idx
			}
		}
	}
	return target.entryIndex
}

func resolveToolCallTargetIndex(target *toggleTarget, calls []ToolCallRecord) int {
	if target == nil || len(calls) == 0 {
		return -1
	}
	if idx := target.toolCallIndex; idx >= 0 && idx < len(calls) && toggleTargetMatchesToolCall(target, calls[idx], false) {
		return idx
	}
	for idx := range calls {
		if toggleTargetMatchesToolCall(target, calls[idx], false) {
			return idx
		}
	}
	if idx := target.toolCallIndex; idx >= 0 && idx < len(calls) {
		return idx
	}
	return -1
}

func resolveChildTargetIndex(target *toggleTarget, children []InterAgentChildActivity) int {
	if target == nil || len(children) == 0 {
		return -1
	}
	if len(target.childPath) > 0 {
		if idx := target.childPath[0]; idx >= 0 && idx < len(children) {
			return idx
		}
	}
	childID := strings.TrimSpace(target.childID)
	if childID != "" {
		for idx := range children {
			if strings.TrimSpace(children[idx].CorrelationID) == childID {
				return idx
			}
		}
	}
	if idx := target.childIndex; idx >= 0 && idx < len(children) {
		return idx
	}
	return -1
}

func resolveInterAgentToggleChild(row *InterAgentTool, target *toggleTarget) (int, *InterAgentChildActivity, bool) {
	if row == nil || target == nil {
		return -1, nil, false
	}
	if len(target.childPath) == 0 {
		childIndex := resolveChildTargetIndex(target, row.Children)
		if childIndex < 0 || childIndex >= len(row.Children) {
			return -1, nil, false
		}
		return childIndex, &row.Children[childIndex], true
	}
	currentRow := row
	var child *InterAgentChildActivity
	var childIndex int
	for depth, pathIndex := range target.childPath {
		if pathIndex < 0 || pathIndex >= len(currentRow.Children) {
			return -1, nil, false
		}
		childIndex = pathIndex
		child = &currentRow.Children[childIndex]
		if depth == len(target.childPath)-1 {
			return childIndex, child, true
		}
		if depth >= len(target.interAgentPath) {
			return -1, nil, false
		}
		nextToolCallIdx := target.interAgentPath[depth]
		if nextToolCallIdx < 0 || nextToolCallIdx >= len(child.ToolCalls) {
			return -1, nil, false
		}
		next := child.ToolCalls[nextToolCallIdx].InterAgent
		if next == nil {
			return -1, nil, false
		}
		currentRow = next
	}
	return -1, nil, false
}

func resolveChildToolCallTargetIndex(target *toggleTarget, calls []ToolCallRecord) int {
	if target == nil || len(calls) == 0 {
		return -1
	}
	if idx := target.childToolCallIdx; idx >= 0 && idx < len(calls) && toggleTargetMatchesToolCall(target, calls[idx], true) {
		return idx
	}
	for idx := range calls {
		if toggleTargetMatchesToolCall(target, calls[idx], true) {
			return idx
		}
	}
	if idx := target.childToolCallIdx; idx >= 0 && idx < len(calls) {
		return idx
	}
	return -1
}

// toggleTargetMatchesToolCall pairs a persisted toggle target with a
// ToolCallRecord by exact ToolCallKey (lifecycle ID) equality. Every record
// carries the provider-stamped ID, so key equality is necessary and
// sufficient; there is no args- or name-based fallback.
func toggleTargetMatchesToolCall(target *toggleTarget, record ToolCallRecord, child bool) bool {
	if target == nil {
		return false
	}
	key := strings.TrimSpace(target.toolCallKey)
	if child {
		key = strings.TrimSpace(target.childToolCallKey)
	}
	return key != "" && strings.TrimSpace(record.ToolCallKey) == key
}

func (m *Model) toggleTarget(target *toggleTarget) bool {
	entryIndex := m.resolveToggleEntryIndex(target)
	if entryIndex < 0 {
		return false
	}
	toggled := false
	m.history.UpdateAt(entryIndex, func(e *ChatEntry) {
		toolCallIndex := resolveToolCallTargetIndex(target, e.ToolCalls)
		if toolCallIndex < 0 || toolCallIndex >= len(e.ToolCalls) {
			return
		}
		record := &e.ToolCalls[toolCallIndex]
		target.entryIndex = entryIndex
		target.entryID = strings.TrimSpace(e.ID)
		target.toolCallIndex = toolCallIndex
		target.toolCallKey = strings.TrimSpace(record.ToolCallKey)
		target.toolCallName = strings.TrimSpace(record.ToolName)
		switch target.kind {
		case toggleTargetToolCall:
			record.Expanded = !record.Expanded
			toggled = true
		case toggleTargetOverflow:
			if record.InterAgent == nil {
				return
			}
			childIndex, child, ok := resolveInterAgentToggleChild(record.InterAgent, target)
			if !ok || child == nil {
				return
			}
			target.childIndex = childIndex
			target.childPath = cloneIntSlice(target.childPath)
			target.childID = strings.TrimSpace(child.CorrelationID)
			child.ToolCallsExpanded = !child.ToolCallsExpanded
			toggled = true
		case toggleTargetChildToolCall:
			if record.InterAgent == nil {
				return
			}
			childIndex, child, ok := resolveInterAgentToggleChild(record.InterAgent, target)
			if !ok || child == nil {
				return
			}
			childToolCallIdx := resolveChildToolCallTargetIndex(target, child.ToolCalls)
			if childToolCallIdx < 0 || childToolCallIdx >= len(child.ToolCalls) {
				return
			}
			target.childIndex = childIndex
			target.childPath = cloneIntSlice(target.childPath)
			target.childID = strings.TrimSpace(child.CorrelationID)
			target.childToolCallIdx = childToolCallIdx
			target.childToolCallKey = strings.TrimSpace(child.ToolCalls[childToolCallIdx].ToolCallKey)
			target.childToolCallName = strings.TrimSpace(child.ToolCalls[childToolCallIdx].ToolName)
			child.ToolCalls[childToolCallIdx].Expanded = !child.ToolCalls[childToolCallIdx].Expanded
			toggled = true
		}
		if toggled {
			invalidateChatEntryRender(e)
		}
	})
	return toggled
}

func (m *Model) reselectAfterToggle(target *toggleTarget) {
	if target == nil {
		return
	}
	entryIndex := m.resolveToggleEntryIndex(target)
	if entryIndex < 0 {
		return
	}
	regions := m.viewport.regions(entryIndex)
	for idx, region := range regions {
		candidate := targetForSelectionRegion(entryIndex, region)
		if candidate == nil {
			continue
		}
		if resolved := m.resolveToggleEntryIndex(candidate); resolved != entryIndex {
			continue
		}
		if toggleTargetsReferToSameItem(candidate, target) {
			m.viewport.selectEntry(entryIndex, idx)
			return
		}
	}
	if target.kind == toggleTargetOverflow {
		for idx, region := range regions {
			if region.kind == selectionRegionChildToolCall &&
				region.childIndex == target.childIndex &&
				intSlicesEqual(region.childPath, target.childPath) &&
				intSlicesEqual(region.interAgentPath, target.interAgentPath) {
				m.viewport.selectEntry(entryIndex, idx)
				return
			}
		}
	}
}

func toggleTargetsReferToSameItem(left, right *toggleTarget) bool {
	if left == nil || right == nil || left.kind != right.kind {
		return false
	}
	switch left.kind {
	case toggleTargetToolCall:
		return sameToolCallToggleTarget(left, right)
	case toggleTargetOverflow:
		if !sameToolCallToggleTarget(left, right) {
			return false
		}
		if !intSlicesEqual(left.childPath, right.childPath) || !intSlicesEqual(left.interAgentPath, right.interAgentPath) {
			return false
		}
		leftChildID := strings.TrimSpace(left.childID)
		rightChildID := strings.TrimSpace(right.childID)
		if leftChildID != "" && rightChildID != "" {
			return leftChildID == rightChildID
		}
		return left.childIndex == right.childIndex
	case toggleTargetChildToolCall:
		if !sameToolCallToggleTarget(left, right) {
			return false
		}
		if !intSlicesEqual(left.childPath, right.childPath) || !intSlicesEqual(left.interAgentPath, right.interAgentPath) {
			return false
		}
		leftChildID := strings.TrimSpace(left.childID)
		rightChildID := strings.TrimSpace(right.childID)
		if leftChildID != "" && rightChildID != "" {
			if leftChildID != rightChildID {
				return false
			}
		} else if left.childIndex != right.childIndex {
			return false
		}
		leftChildToolKey := strings.TrimSpace(left.childToolCallKey)
		rightChildToolKey := strings.TrimSpace(right.childToolCallKey)
		if leftChildToolKey != "" && rightChildToolKey != "" {
			return leftChildToolKey == rightChildToolKey
		}
		return left.childToolCallIdx == right.childToolCallIdx
	default:
		return false
	}
}

// sameToolCallToggleTarget compares two toggle targets by lifecycle ID
// (ToolCallKey). When keys are present on both (the normal case), equality
// is definitive. When either key is missing (legacy target from before
// reload), we fall back to the positional tool-call index.
func sameToolCallToggleTarget(left, right *toggleTarget) bool {
	leftKey := strings.TrimSpace(left.toolCallKey)
	rightKey := strings.TrimSpace(right.toolCallKey)
	if leftKey != "" && rightKey != "" {
		return leftKey == rightKey
	}
	return left.toolCallIndex == right.toolCallIndex
}

func intSlicesEqual(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// SetHighlight marks an entry for transient visual highlight (copy feedback).
// When the chat panel is focused, the selection is always moved to the copied
// region so arrow keys continue from there after the highlight fades.
// When unfocused, no selection is created.
func (m *Model) SetHighlight(entryID string, entryIndex, start, end int) {
	m.highlightID = entryID
	m.highlightUntil = time.Now().Add(highlightDuration)
	m.viewDirty = true
	if m.focused {
		m.viewport.SelectRegionContaining(entryIndex, start)
	}
	m.viewport.SetHighlight(entryID, start, end)
}

// tickHighlight decrements the highlight countdown and clears when expired.
// The selection is only cleared alongside the highlight when the chat panel
// is not focused; otherwise the selection persists for continued navigation.
func (m *Model) tickHighlight(now time.Time) {
	if m.highlightUntil.IsZero() || now.Before(m.highlightUntil) {
		return
	}
	m.highlightID = ""
	m.highlightUntil = time.Time{}
	m.viewport.SetHighlight("", 0, 0)
	if !m.focused {
		m.viewport.ClearSelection()
	}
}

func (m *Model) handleDecorTick(now time.Time) {
	prevHL := !m.highlightUntil.IsZero()
	prevFlash := m.viewport.HasEdgeFlash(now)
	m.tickHighlight(now)
	m.viewport.TickEdgeFlash(now)
	afterHL := !m.highlightUntil.IsZero()
	afterFlash := m.viewport.HasEdgeFlash(now)
	if prevHL != afterHL || prevFlash != afterFlash {
		m.viewDirty = true
	}

	// Animate thinking indicator while streaming with no content yet.
	m.tickThinking(now)

	// Animate pending steering entries with holographic shimmer.
	m.tickSteering(now)

	// Invalidate entries with active (incomplete) tool calls for live timer.
	m.tickActiveToolCalls()

	// Flush buffered stream chunks to the history entry.
	m.flushStreamRender()
}

// tickActiveToolCalls invalidates the render cache for entries with in-progress
// tool calls so the live elapsed timer updates on each DecorTick.
func (m *Model) tickActiveToolCalls() {
	hasActive := false
	for _, idx := range m.activeStreamingIndices() {
		if m.invalidateEntryToolCalls(idx) {
			hasActive = true
		}
	}
	if hasActive {
		m.viewDirty = true
	}
}

// flushStreamRender syncs accumulated stream content to the history entry
// and marks the view dirty. Called on DecorTick (100ms) and StreamComplete.
func (m *Model) flushStreamRender() {
	if !m.streamRenderPending || len(m.streams) == 0 {
		return
	}
	m.streamRenderPending = false
	for _, slot := range m.streams {
		if slot.accumulator == nil {
			continue
		}
		m.syncSlotToEntry(slot)
		if slot.renderState != nil {
			m.viewport.AddStreamState(slot.accumulator.EntryIndex(), slot.renderState)
		}
	}
	m.viewDirty = true
}

// tickThinking advances the thinking spinner and rotates the fun message.
// Active whenever a thinking placeholder exists (streaming or non-streaming).
func (m *Model) tickThinking(now time.Time) {
	updated := false
	if m.progress.canPromote(now) && m.progress.promotePending(now) {
		updated = true
	}
	if m.renderThinkingEntry(m.thinkingIdx, m.thinkingAgentID, m.progress.retryText, m.thinkingStart, now) {
		updated = true
	}
	for _, slot := range m.streams {
		if slot == nil {
			continue
		}
		if slot.progress.canPromote(now) && slot.progress.promotePending(now) {
			updated = true
		}
		if m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.progress.retryText, slot.thinkingStart, now) {
			updated = true
		}
	}
	if m.tickNestedStreamThinking(now) {
		updated = true
	}
	if m.tickPendingInterAgentHistory(now) {
		updated = true
	}
	if updated {
		m.viewDirty = true
	}
}

// ---------------------------------------------------------------------------
// Thinking lifecycle (called from app.go)
// ---------------------------------------------------------------------------

// BeginThinking pushes a placeholder agent entry with a spinner animation.
// Called when the user submits a prompt, before any response arrives.
func (m *Model) BeginThinking(agentType string) {
	now := time.Now()
	entry := &ChatEntry{
		ID:             uuid.New().String(),
		Timestamp:      now,
		Source:         SourceAgent,
		AgentType:      agentType,
		Content:        "",
		Height:         -1,
		Streaming:      true,
		ThinkingText:   spinnerFrames[0] + "  0.0s",
		ThinkingStatus: thinkingMessagesForAgent(agentType)[0],
	}
	willEvict := m.history.Full()
	m.history.Push(entry)
	m.viewport.OnNewEntry()
	if willEvict {
		m.viewport.AdjustSelectionForEviction()
		m.adjustSteeringIndices()
	}
	idx := m.history.Len() - 1
	m.startThinkingAnimation(now, idx)
	m.viewDirty = true
}

// MuteThinking updates the active thinking placeholder to use a muted color.
// This is used when the active task is interrupted.
func (m *Model) MuteThinking(color string) {
	if m.thinkingIdx < 0 {
		return
	}
	if strings.TrimSpace(color) == "" {
		color = string(m.theme.Palette.Muted)
	}
	idx := m.thinkingIdx
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		entry := &m.history.entries[physical]
		entry.ThinkingColor = color
		entry.RenderedLines = nil
		entry.CodeRegions = nil
		entry.Height = -1
	}
	m.history.mu.Unlock()
	m.viewDirty = true
}

// FinishThinking fills the thinking placeholder with a complete response.
// Used for non-streaming responses (e.g. GuideResponseMsg).
func (m *Model) FinishThinking(entry *ChatEntry) {
	if m.thinkingIdx < 0 {
		m.PushEntry(entry)
		return
	}

	elapsed := time.Since(m.thinkingStart)
	idx := m.thinkingIdx

	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		e := &m.history.entries[physical]
		finalizeToolCallsSynthetic(e.ToolCalls, time.Now(), true, "")
		e.CorrelationID = entry.CorrelationID
		e.Content = entry.Content
		e.Source = entry.Source
		e.AgentType = entry.AgentType
		e.AgentID = entry.AgentID
		e.TaskID = entry.TaskID
		e.TaskName = entry.TaskName
		e.TaskSlug = entry.TaskSlug
		e.Timestamp = entry.Timestamp
		e.ThinkingText = ""
		e.ThinkingStatus = ""
		e.ThinkingColor = ""
		e.ThinkingElapsed = elapsed
		e.Streaming = false
		e.RenderedLines = nil
		e.CodeRegions = nil
		e.Height = -1
	}
	m.history.mu.Unlock()

	m.clearThinkingState()
	m.viewDirty = true
}

// startThinkingAnimation initializes the animation fields for a thinking entry.
func (m *Model) startThinkingAnimation(now time.Time, idx int) {
	m.thinkingIdx = idx
	m.thinkingStart = now
	m.progress.clear()
}

// resolveThinkingEntry transitions the thinking placeholder to content phase,
// recording the elapsed thinking time.
func (m *Model) resolveThinkingEntry() {
	elapsed := thinkingElapsed(m.thinkingStart, time.Now())
	idx := m.thinkingIdx
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		m.history.entries[physical].ThinkingElapsed = elapsed
		m.history.entries[physical].ThinkingText = ""
		m.history.entries[physical].ThinkingStatus = ""
		m.history.entries[physical].ThinkingColor = ""
	}
	m.history.mu.Unlock()
	m.clearThinkingState()
}

// clearThinkingState resets all thinking animation fields.
func (m *Model) clearThinkingState() {
	m.thinkingIdx = -1
	m.thinkingAgentID = ""
	m.thinkingStart = time.Time{}
	m.progress.clear()
}

// HasPendingCorrelation reports whether the chat model still has an active or
// deferred stream slot for correlationID. App-level routing uses this to keep
// late terminal messages attached to the existing chat entry instead of
// creating a separate completed response entry.
func (m *Model) HasPendingCorrelation(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	if _, ok := m.streams[correlationID]; ok {
		return true
	}
	if slot, ok := m.nestedStreams[correlationID]; ok && slot != nil && !slot.done {
		return true
	}
	if _, ok := m.resumablePrimaries[correlationID]; ok {
		return true
	}
	return false
}

func (m *Model) updateThinkingAgent(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || m.thinkingIdx < 0 {
		return
	}
	// When the active agent changes (e.g. guide → architect), clear the
	// progress override so agent-specific rotating messages take over.
	if agentID != m.thinkingAgentID {
		m.progress.clear()
	}
	m.thinkingAgentID = agentID
	idx := m.thinkingIdx
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		entry := &m.history.entries[physical]
		preserveDistinctAgentID := strings.TrimSpace(entry.AgentID) != "" && strings.TrimSpace(entry.AgentID) != strings.TrimSpace(entry.AgentType)
		entry.AgentType = agentID
		if !preserveDistinctAgentID {
			entry.AgentID = agentID
		}
		entry.RenderedLines = nil
		entry.CodeRegions = nil
		entry.Height = -1
	}
	m.history.mu.Unlock()
	m.viewDirty = true
}

func (m *Model) setThinkingTextNow(message string) {
	if m.thinkingIdx < 0 {
		return
	}
	message = sanitizeThinkingMessage(message)
	if message == "" {
		return
	}
	if m.setThinkingEntryNow(m.thinkingIdx, m.thinkingStart, message) {
		m.viewDirty = true
	}
}

// ---------------------------------------------------------------------------
// Steering animation lifecycle
// ---------------------------------------------------------------------------

// maxSteeringPending bounds the tracked pending steering entries.
// Derived from steering mailbox capacity (16) — a user cannot produce more
// concurrent pending commands than the mailbox can hold across all agents.
const maxSteeringPending = 16

// PushSteeringEntry adds a steering message entry with holographic shimmer.
// The entry remains animated until acknowledged by the target agent
// (steering_inject activity) or the stream completes.
func (m *Model) PushSteeringEntry(text, correlationID string) {
	// Evict oldest pending entry if at capacity to prevent unbounded growth.
	if len(m.steeringPending) >= maxSteeringPending {
		oldest := m.steeringPending[0]
		m.history.UpdateAt(oldest.idx, func(e *ChatEntry) {
			e.SteeringPending = false
			e.ThinkingColor = ""
			e.RenderedLines = nil
			e.CodeRegions = nil
			e.Height = -1
		})
		m.steeringPending = m.steeringPending[1:]
	}

	entry := &ChatEntry{
		ID:              uuid.New().String(),
		Timestamp:       time.Now(),
		Source:          SourceUser,
		Content:         theme.IconSteer + " " + text,
		Height:          -1,
		SteeringPending: true,
	}
	willEvict := m.history.Full()
	m.history.Push(entry)
	m.viewport.OnNewEntry()
	if willEvict {
		m.viewport.AdjustSelectionForEviction()
		if m.thinkingIdx >= 0 {
			m.thinkingIdx--
		}
		if m.planEntryIdx >= 0 {
			m.planEntryIdx--
		}
		for _, slot := range m.streams {
			if slot.accumulator != nil {
				slot.accumulator.AdjustIndex(-1)
			}
			if slot.thinkingIdx >= 0 {
				slot.thinkingIdx--
			}
		}
		m.adjustSteeringIndices()
	}
	idx := m.history.Len() - 1
	if len(m.steeringPending) == 0 {
		m.steeringStart = time.Now()
	}
	m.steeringPending = append(m.steeringPending, steeringPendingEntry{
		idx:           idx,
		correlationID: correlationID,
	})
	m.viewDirty = true
}

// acknowledgeSteering transitions all pending steering entries for the given
// correlation from holographic to static rendering.
func (m *Model) acknowledgeSteering(correlationID string) {
	remaining := m.steeringPending[:0]
	for _, sp := range m.steeringPending {
		if sp.correlationID == correlationID {
			m.history.UpdateAt(sp.idx, func(e *ChatEntry) {
				e.SteeringPending = false
				e.ThinkingColor = ""
				e.RenderedLines = nil
				e.CodeRegions = nil
				e.Height = -1
			})
		} else {
			remaining = append(remaining, sp)
		}
	}
	m.steeringPending = remaining
	if len(m.steeringPending) == 0 {
		m.steeringStart = time.Time{}
	}
	m.viewDirty = true
}

// tickSteering updates the holographic gradient color on pending steering entries.
func (m *Model) tickSteering(now time.Time) {
	if len(m.steeringPending) == 0 {
		return
	}
	color := ""
	if m.steeringGradient != nil && !m.steeringStart.IsZero() {
		color = string(m.steeringGradient.Sample(now.Sub(m.steeringStart)))
	}
	for _, sp := range m.steeringPending {
		m.history.mu.Lock()
		if sp.idx >= 0 && sp.idx < m.history.count {
			physical := m.history.logicalToPhysical(sp.idx)
			m.history.entries[physical].ThinkingColor = color
			m.history.entries[physical].RenderedLines = nil
			m.history.entries[physical].CodeRegions = nil
			m.history.entries[physical].Height = -1
		}
		m.history.mu.Unlock()
	}
	m.viewDirty = true
}

// adjustSteeringIndices decrements all tracked steering entry indices after
// a ring buffer eviction. Entries that fall off (idx < 0) are removed.
func (m *Model) adjustSteeringIndices() {
	n := 0
	for _, sp := range m.steeringPending {
		sp.idx--
		if sp.idx >= 0 {
			m.steeringPending[n] = sp
			n++
		}
	}
	m.steeringPending = m.steeringPending[:n]
	if len(m.steeringPending) == 0 {
		m.steeringStart = time.Time{}
	}
}

// clearSteeringState resets all steering animation fields.
func (m *Model) clearSteeringState() {
	m.steeringPending = m.steeringPending[:0]
	m.steeringStart = time.Time{}
}

func sanitizeThinkingMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	message = strings.ReplaceAll(message, "\r\n", " ")
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	var cleaned strings.Builder
	cleaned.Grow(len(message))
	for _, r := range message {
		switch {
		case r == '\t':
			cleaned.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Drop control characters that can disturb terminal rendering.
		default:
			cleaned.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(cleaned.String()), " ")
}

func (m *Model) refreshExistingStreamSlot(slot *streamSlot, start msg.StreamStartMsg) {
	if slot == nil || slot.accumulator == nil {
		return
	}
	nextAgentID := streamEntryAgentType(start)
	agentChanged := normalizeChatAgentID(slot.agentID) != normalizeChatAgentID(nextAgentID)
	slot.agentID = nextAgentID
	slot.deferCompletion = false
	if agentChanged {
		// Shared-correlation handoffs (for example Guide -> Architect) reuse the
		// same row, but the previous speaker's progress override must not leak
		// into the new responder's thinking state.
		slot.progress.clear()
	}
	m.updateStreamEntryMetadata(slot.accumulator.EntryIndex(), start)
	if !shouldResetStreamSlot(slot) {
		if agentChanged {
			m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.progress.retryText, slot.thinkingStart, time.Now())
		}
		m.viewDirty = true
		return
	}
	slot.accumulator.Replace("")
	slot.planID = ""
	slot.planMarkdown = ""
	slot.planOffset = 0
	slot.renderState = &streamRenderState{}
	m.startSlotThinkingAnimation(time.Now(), slot)
	m.streamRenderPending = false
	m.syncSlotToEntry(slot)
	if slot.renderState != nil {
		m.viewport.AddStreamState(slot.accumulator.EntryIndex(), slot.renderState)
	}
	m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.progress.retryText, slot.thinkingStart, slot.thinkingStart)
	m.viewDirty = true
}

func shouldResetStreamSlot(slot *streamSlot) bool {
	if slot == nil || slot.accumulator == nil {
		return false
	}
	return slot.accumulator.Content() != "" || slot.planMarkdown != ""
}

func (m *Model) adoptGlobalThinkingState(now time.Time, slot *streamSlot) {
	if slot == nil {
		return
	}
	if m.thinkingStart.IsZero() {
		m.startSlotThinkingAnimation(now, slot)
		return
	}
	slot.thinkingStart = m.thinkingStart
	slot.progress = m.progress
}

func (m *Model) startSlotThinkingAnimation(now time.Time, slot *streamSlot) {
	if slot == nil {
		return
	}
	slot.thinkingStart = now
	slot.deferCompletion = false
	slot.progress.clear()
}

func (m *Model) streamSlot(correlationID string) *streamSlot {
	if correlationID == "" {
		return nil
	}
	if slot, ok := m.streams[correlationID]; ok {
		return slot
	}
	return nil
}

func (m *Model) applySlotProgress(slot *streamSlot, message string, sequence int64, toolDerived, watchdog bool) {
	if slot == nil || slot.thinkingIdx < 0 || message == "" {
		return
	}
	now := time.Now()
	if entry := m.history.Get(slot.thinkingIdx); entry != nil && staleGuardianInterAgentProgress(message, entry.ToolCalls) {
		// Stale guardian-mention progress text: the inline guardian row
		// already shows this to the user, so surfacing it again on the
		// parent's thinking bar is redundant. Drop the MESSAGE only —
		// do not clear the spinner. Zeroing slot.thinkingStart here is
		// the canonical cause of parent agents' thinking indicators
		// vanishing after every guardian approval.
		return
	}
	if slot.thinkingStart.IsZero() {
		slot.thinkingStart = now
	}
	if slot.progress.queue(message, sequence, toolDerived, watchdog) && slot.progress.canPromote(now) {
		if slot.progress.promotePending(now) && m.setSlotThinkingTextNow(slot, slot.progress.retryText) {
			m.viewDirty = true
		}
	}
}

func (m *Model) updateSlotThinkingAgent(slot *streamSlot, agentID string) {
	agentID = strings.TrimSpace(agentID)
	if slot == nil || agentID == "" || slot.thinkingIdx < 0 {
		return
	}
	if agentID != slot.agentID {
		slot.progress.clear()
	}
	slot.agentID = agentID
	m.history.UpdateAt(slot.thinkingIdx, func(entry *ChatEntry) {
		oldAgentType := strings.TrimSpace(entry.AgentType)
		oldAgentID := strings.TrimSpace(entry.AgentID)
		entry.AgentType = agentID
		if oldAgentID == "" || oldAgentID == oldAgentType {
			entry.AgentID = agentID
		}
		entry.RenderedLines = nil
		entry.CodeRegions = nil
		entry.Height = -1
	})
	m.viewDirty = true
}

func (m *Model) setSlotThinkingTextNow(slot *streamSlot, message string) bool {
	if slot == nil {
		return false
	}
	return m.setThinkingEntryNow(slot.thinkingIdx, slot.thinkingStart, message)
}

func (m *Model) resolveSlotThinkingEntry(slot *streamSlot) {
	if slot == nil {
		return
	}
	elapsed := thinkingElapsed(slot.thinkingStart, time.Now())
	idx := slot.thinkingIdx
	preservedStatus := ""
	if idx >= 0 && strings.TrimSpace(slot.progress.retryText) != "" {
		if entry := m.history.Get(idx); entry != nil && strings.TrimSpace(entry.Content) == "" && !slot.progress.retryToolDerived {
			preservedStatus = sanitizeThinkingMessage(firstNonEmptyString(entry.ThinkingStatus, slot.progress.retryText))
		}
	}
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		m.history.entries[physical].ThinkingElapsed = elapsed
		m.history.entries[physical].ThinkingText = ""
		m.history.entries[physical].ThinkingStatus = preservedStatus
		m.history.entries[physical].ThinkingColor = ""
	}
	m.history.mu.Unlock()
	slot.thinkingStart = time.Time{}
	slot.progress.clear()
}

func (m *Model) clearPrimaryThinkingDisplay(idx int, now time.Time) bool {
	if idx < 0 {
		return false
	}
	changed := false
	if m.progress.clearPendingDisplayed() {
		changed = true
	}
	if m.progress.clearDisplayed(now) {
		changed = true
	}
	if !m.thinkingStart.IsZero() {
		m.thinkingStart = time.Time{}
		changed = true
	}
	if changed {
		m.writeThinkingEntry(idx, "", "", "")
	}
	return changed
}

func (m *Model) clearSlotThinkingDisplay(slot *streamSlot, now time.Time) bool {
	if slot == nil {
		return false
	}
	changed := false
	if slot.progress.clearPendingDisplayed() {
		changed = true
	}
	if slot.progress.clearDisplayed(now) {
		changed = true
	}
	if !slot.thinkingStart.IsZero() {
		slot.thinkingStart = time.Time{}
		changed = true
	}
	if changed {
		m.writeThinkingEntry(slot.thinkingIdx, "", "", "")
	}
	return changed
}

func (m *Model) clearNestedThinkingDisplay(slot *nestedStreamSlot, now time.Time) bool {
	if slot == nil {
		return false
	}
	changed := false
	if slot.progress.clearPendingDisplayed() {
		changed = true
	}
	if slot.progress.clearDisplayed(now) {
		changed = true
	}
	if !slot.thinkingStart.IsZero() {
		slot.thinkingStart = time.Time{}
		changed = true
	}
	if !slot.activity.ThinkingStartedAt.IsZero() ||
		strings.TrimSpace(slot.activity.ThinkingText) != "" ||
		strings.TrimSpace(slot.activity.ThinkingStatus) != "" ||
		strings.TrimSpace(slot.activity.ThinkingColor) != "" {
		slot.activity.ThinkingStartedAt = time.Time{}
		slot.activity.ThinkingText = ""
		slot.activity.ThinkingStatus = ""
		slot.activity.ThinkingColor = ""
		changed = true
	}
	return changed
}

func (m *Model) reconcileToolDerivedThinkingProgress(idx int) {
	if idx < 0 || idx != m.thinkingIdx {
		return
	}
	entry := m.history.Get(idx)
	if entry == nil {
		return
	}
	next := toolDerivedProgressSummary(entry.ToolCalls)
	now := time.Now()
	if next != "" && m.thinkingStart.IsZero() {
		m.thinkingStart = now
	}
	if m.progress.reconcileToolDerived(next, now) {
		if m.thinkingStart.IsZero() {
			m.writeThinkingEntry(idx, "", "", "")
		} else {
			m.renderThinkingEntry(idx, m.thinkingAgentID, m.progress.retryText, m.thinkingStart, now)
		}
		m.viewDirty = true
	}
	if staleGuardianInterAgentProgress(m.progress.retryText, entry.ToolCalls) ||
		staleGuardianInterAgentProgress(m.progress.pendingRetryText, entry.ToolCalls) {
		// Stored progress text became stale (guardian row now shows it
		// inline). Clear the text so the thinking bar stops repeating
		// it, but KEEP the spinner alive — the parent agent's turn
		// hasn't ended.
		changed := m.progress.clearPendingDisplayed()
		if m.progress.clearDisplayed(now) {
			changed = true
		}
		if changed {
			m.renderThinkingEntry(idx, m.thinkingAgentID, "", m.thinkingStart, now)
			m.viewDirty = true
		}
	}
}

func (m *Model) reconcileToolDerivedSlotProgress(slot *streamSlot) {
	if slot == nil || slot.thinkingIdx < 0 || slot.accumulator == nil {
		return
	}
	entry := m.history.Get(slot.accumulator.EntryIndex())
	if entry == nil {
		return
	}
	next := toolDerivedProgressSummary(entry.ToolCalls)
	now := time.Now()
	if next != "" && slot.thinkingStart.IsZero() {
		slot.thinkingStart = now
	}
	if slot.progress.reconcileToolDerived(next, now) {
		if slot.thinkingStart.IsZero() {
			m.writeThinkingEntry(slot.thinkingIdx, "", "", "")
		} else {
			m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.progress.retryText, slot.thinkingStart, now)
		}
		m.viewDirty = true
	}
	if staleGuardianInterAgentProgress(slot.progress.retryText, entry.ToolCalls) ||
		staleGuardianInterAgentProgress(slot.progress.pendingRetryText, entry.ToolCalls) {
		// Clear stale guardian-mention text; keep the spinner alive.
		changed := slot.progress.clearPendingDisplayed()
		if slot.progress.clearDisplayed(now) {
			changed = true
		}
		if changed {
			m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, "", slot.thinkingStart, now)
			m.viewDirty = true
		}
	}
}

func (m *Model) reconcileToolDerivedNestedProgress(slot *nestedStreamSlot) {
	if slot == nil {
		return
	}
	next := toolDerivedProgressSummary(slot.activity.ToolCalls)
	now := time.Now()
	if next != "" && slot.thinkingStart.IsZero() {
		slot.thinkingStart = now
	}
	if slot.progress.reconcileToolDerived(next, now) && m.renderNestedStreamThinking(slot, now) {
		m.syncPendingNestedStream(slot)
	}
	if staleGuardianInterAgentProgress(slot.progress.retryText, slot.activity.ToolCalls) ||
		staleGuardianInterAgentProgress(slot.progress.pendingRetryText, slot.activity.ToolCalls) {
		// Clear stale guardian text on nested slot; keep spinner alive.
		changed := slot.progress.clearPendingDisplayed()
		if slot.progress.clearDisplayed(now) {
			changed = true
		}
		if changed {
			m.renderNestedStreamThinking(slot, now)
			m.syncPendingNestedStream(slot)
		}
	}
}

func toolDerivedProgressSummary(calls []ToolCallRecord) string {
	if len(calls) == 0 {
		return ""
	}
	names := make([]string, 0, len(calls))
	seen := make(map[string]struct{}, len(calls))
	for i := range calls {
		if !toolCallHasActiveVisual(calls[i]) {
			continue
		}
		name := humanizeToolCallName(calls[i].ToolName)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	switch len(names) {
	case 1:
		return "Working through this with " + names[0] + "."
	case 2:
		return "Working through this with " + names[0] + " and " + names[1] + "."
	default:
		head := strings.Join(names[:len(names)-1], ", ")
		return "Working through this with " + head + ", and " + names[len(names)-1] + "."
	}
}

func humanizeToolCallName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "_", " ")
	return strings.Join(strings.Fields(name), " ")
}

func (m *Model) renderThinkingEntry(idx int, agentID, retryText string, start, now time.Time) bool {
	if idx < 0 || start.IsZero() {
		return false
	}
	elapsed := thinkingElapsed(start, now)
	text := fmt.Sprintf("%s  %s", spinnerFrames[thinkingFrameAt(elapsed)], formatToolDuration(elapsed))
	// Agent-state detail (explicit activity transition from the agent) takes
	// precedence over the rotating placeholder messages — when present, the
	// user sees exactly what the agent is doing instead of a generic phrase.
	// Retry text still wins over state detail because retries indicate a
	// transient failure the user should see first.
	status := thinkingStatusFor(agentID, retryText, elapsed)
	if retryText == "" {
		if detail := m.agentStateDetailForEntry(idx); detail != "" {
			status = detail
		}
	}
	return m.writeThinkingEntry(idx, text, status, m.thinkingColorFor(elapsed))
}

// agentStateDetailForEntry returns the current agent-state detail for the
// entry at idx, or "" if no non-terminal state is attached. Looks up the
// entry's correlation via the stream slot map and consults m.agentStates.
// The detail comes from the most recent PublishAgentState call; terminal
// states (complete/errored) have already been evicted.
func (m *Model) agentStateDetailForEntry(idx int) string {
	if idx < 0 || m.agentStates == nil || len(m.agentStates) == 0 {
		return ""
	}
	// Find the correlation id owning this entry index.
	for cid, slot := range m.streams {
		if slot == nil || slot.accumulator == nil {
			continue
		}
		if slot.accumulator.EntryIndex() != idx {
			continue
		}
		state, ok := m.agentStates[cid]
		if !ok || state == nil {
			return ""
		}
		if detail := strings.TrimSpace(state.Detail); detail != "" {
			return detail
		}
		return humanizeAgentState(state.State)
	}
	return ""
}

func (m *Model) setThinkingEntryNow(idx int, start time.Time, message string) bool {
	message = sanitizeThinkingMessage(message)
	if idx < 0 || message == "" {
		return false
	}
	now := time.Now()
	elapsed := thinkingElapsed(start, now)
	text := fmt.Sprintf("%s  %s", spinnerFrames[thinkingFrameAt(elapsed)], formatToolDuration(elapsed))
	return m.writeThinkingEntry(idx, text, message, m.thinkingColorFor(elapsed))
}

func (m *Model) writeThinkingEntry(idx int, text, status, color string) bool {
	wrote := false
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		m.history.entries[physical].ThinkingText = text
		m.history.entries[physical].ThinkingStatus = status
		m.history.entries[physical].ThinkingColor = color
		m.history.entries[physical].RenderedLines = nil
		m.history.entries[physical].CodeRegions = nil
		m.history.entries[physical].Height = -1
		wrote = true
	}
	m.history.mu.Unlock()
	return wrote
}

func (m *Model) thinkingColorFor(elapsed time.Duration) string {
	if m.thinkingGradient == nil {
		return string(m.theme.Palette.Info)
	}
	return string(m.thinkingGradient.Sample(elapsed))
}

func thinkingElapsed(start, now time.Time) time.Duration {
	if start.IsZero() || now.Before(start) {
		return 0
	}
	return now.Sub(start)
}

func thinkingFrameAt(elapsed time.Duration) int {
	if len(spinnerFrames) == 0 {
		return 0
	}
	steps := int(elapsed / thinkingFrameInterval)
	if steps < 0 {
		steps = 0
	}
	return steps % len(spinnerFrames)
}

func thinkingStatusFor(agentID, retryText string, elapsed time.Duration) string {
	if retryText != "" {
		return retryText
	}
	msgs := thinkingMessagesForAgent(agentID)
	if len(msgs) == 0 {
		return ""
	}
	idx := int(elapsed / thinkingRotateInterval)
	if idx < 0 {
		idx = 0
	}
	return msgs[idx%len(msgs)]
}

func (m *Model) updateStreamEntryMetadata(idx int, start msg.StreamStartMsg) {
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		entry := &m.history.entries[physical]
		if start.AgentID != "" {
			entry.AgentID = start.AgentID
		}
		if agentType := streamEntryAgentType(start); agentType != "" {
			entry.AgentType = agentType
		}
		// The entry's primary CID always reflects the LATEST stream that
		// took over the entry. Any prior CIDs accumulate in
		// AdditionalCorrelationIDs so in-flight tool events keyed by the
		// older CID still resolve to this entry via historyIndexForCorrelation.
		// Without this, Phase=1 of tools that started under the old CID
		// would strand and their spinners would never clear.
		newCID := strings.TrimSpace(start.CorrelationID)
		oldCID := strings.TrimSpace(entry.CorrelationID)
		if newCID != "" && oldCID != "" && newCID != oldCID {
			already := false
			for _, existing := range entry.AdditionalCorrelationIDs {
				if strings.TrimSpace(existing) == oldCID {
					already = true
					break
				}
			}
			if !already {
				entry.AdditionalCorrelationIDs = append(entry.AdditionalCorrelationIDs, oldCID)
			}
		}
		entry.CorrelationID = start.CorrelationID
		entry.SessionID = start.SessionID
		entry.TaskID = strings.TrimSpace(start.TaskID)
		entry.TaskName = strings.TrimSpace(start.TaskName)
		entry.TaskSlug = strings.TrimSpace(start.TaskSlug)
		entry.Streaming = true
		entry.RenderedLines = nil
		entry.CodeRegions = nil
		entry.Height = -1
	}
	m.history.mu.Unlock()
}

func streamEntryAgentType(start msg.StreamStartMsg) string {
	if agentType := strings.TrimSpace(start.AgentType); agentType != "" {
		return agentType
	}
	return strings.TrimSpace(start.AgentID)
}

func normalizeChatAgentID(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func visiblePrimaryAgentID(agentID, agentType, taskID string) string {
	taskID = normalizeChatAgentID(taskID)
	agentType = normalizeChatAgentID(agentType)
	if taskID != "" && agentType != "" {
		return taskID + ":" + agentType
	}
	if agentType != "" {
		return agentType
	}
	return normalizeChatAgentID(agentID)
}

func streamVisibleAgentID(start msg.StreamStartMsg) string {
	return visiblePrimaryAgentID(start.AgentID, streamEntryAgentType(start), start.TaskID)
}

func entryVisibleAgentID(entry *ChatEntry) string {
	if entry == nil {
		return ""
	}
	return visiblePrimaryAgentID(entry.AgentID, badgeAgentType(entry), entry.TaskID)
}

// composeSlotContent returns the full entry content for a stream slot by
// splicing the embedded plan markdown (if any) into the accumulated LLM text.
func composeSlotContent(slot *streamSlot) string {
	content := slot.accumulator.Content()
	if slot.planMarkdown == "" {
		return content
	}
	offset := slot.planOffset
	if offset > len(content) {
		offset = len(content)
	}
	return content[:offset] + "\n\n" + slot.planMarkdown + "\n\n" + content[offset:]
}

// syncSlotToEntry writes the slot's accumulated content back into the
// History entry and invalidates its height.
func (m *Model) syncSlotToEntry(slot *streamSlot) {
	idx := slot.accumulator.EntryIndex()
	content := composeSlotContent(slot)

	m.history.mu.Lock()
	defer m.history.mu.Unlock()

	if idx < 0 || idx >= m.history.count {
		return
	}
	physical := m.history.logicalToPhysical(idx)
	m.history.entries[physical].Content = content
	m.history.entries[physical].Height = -1
}

// finalizeSlotStream marks the streaming entry for a specific slot as complete.
// When a plan was embedded in the stream, the entry is promoted to a
// first-class plan entry.
func (m *Model) finalizeSlotStream(slot *streamSlot) {
	idx := slot.accumulator.EntryIndex()
	content := composeSlotContent(slot)
	hadPlan := slot.planMarkdown != ""

	m.history.mu.Lock()
	if idx < 0 || idx >= m.history.count {
		m.history.mu.Unlock()
		return
	}
	physical := m.history.logicalToPhysical(idx)
	m.history.entries[physical].Content = content
	m.history.entries[physical].Streaming = false
	m.history.entries[physical].RenderedLines = nil
	m.history.entries[physical].CodeRegions = nil
	m.history.entries[physical].Height = -1
	if hadPlan && slot.planID != "" {
		m.history.entries[physical].ID = "plan-" + slot.planID
		m.planEntryIdx = idx
		m.planID = slot.planID
	}
	m.history.mu.Unlock()
}

func (m *Model) finalizeCompletedStreamSlot(correlationID string, slot *streamSlot, success bool, errorMsg string) {
	if slot == nil || slot.accumulator == nil {
		return
	}
	m.viewport.RemoveStreamState(slot.accumulator.EntryIndex())
	m.resolveSlotThinkingEntry(slot)
	m.finalizeSlotToolCalls(slot, success, errorMsg)
	slot.accumulator.Complete()
	m.finalizeSlotStream(slot)
	delete(m.streams, correlationID)
	if len(m.streams) == 0 {
		m.streamRenderPending = false
	}
}

func (m *Model) shouldRecordResumablePrimary(slot *streamSlot) bool {
	if slot == nil || slot.accumulator == nil {
		return false
	}
	entry := m.history.Get(slot.accumulator.EntryIndex())
	return entryHasPendingInterAgentToolCalls(entry)
}

func (m *Model) recordResumableCompletedPrimary(correlationID string, slot *streamSlot) {
	if m == nil || slot == nil || slot.accumulator == nil {
		return
	}
	entry := m.history.Get(slot.accumulator.EntryIndex())
	if entry == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	state := resumablePrimaryState{
		visibleAgentID: entryVisibleAgentID(entry),
		agentType:      badgeAgentType(entry),
		runtimeAgentID: strings.TrimSpace(entry.RuntimeAgentID),
		sessionID:      strings.TrimSpace(entry.SessionID),
		taskID:         strings.TrimSpace(entry.TaskID),
		children:       make(map[string]struct{}),
	}
	if m.resumablePrimaries == nil {
		m.resumablePrimaries = make(map[string]resumablePrimaryState)
	}
	m.resumablePrimaries[correlationID] = state
	for _, childCID := range interAgentChildCorrelations(entry) {
		m.noteResumableChildOwner(childCID, correlationID)
	}
}

// settleChallengeRowsForChildCIDInEntry settles Pending challenge rows in
// the entry at idx whose nested children include childCID. success=true
// marks them Done; success=false marks them Failed. If the entry no longer
// has any pending inter-agent rows after settling, the resumable-primary
// registration for the entry's primary CID is also cleared so the entry can
// finalize cleanly on its next StreamComplete instead of being kept
// perpetually open. Returns true if the entry was modified.
func (m *Model) settleChallengeRowsForChildCIDInEntry(idx int, childCID string, success bool) bool {
	if m == nil || idx < 0 {
		return false
	}
	childCID = strings.TrimSpace(childCID)
	if childCID == "" {
		return false
	}
	changed := false
	var entryCID string
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		if e == nil {
			return
		}
		if !settleChallengeRowsForChildCID(e, childCID, success) {
			return
		}
		invalidateChatEntryRender(e)
		entryCID = strings.TrimSpace(e.CorrelationID)
		changed = true
	})
	if !changed {
		return false
	}
	m.syncPendingInterAgentEntry(idx)
	if entry := m.history.Get(idx); entry != nil && !entryHasPendingInterAgentToolCalls(entry) {
		if entryCID != "" {
			m.clearResumablePrimary(entryCID)
		}
	}
	delete(m.resumableChildOwners, childCID)
	m.viewDirty = true
	return true
}

// noteResumableChildOwner records the child→owner linkage in
// m.resumableChildOwners and, if the owner's resumable-primary state already
// exists, also registers the child within state.children. The linkage is
// populated regardless of resumable-primary state so a top-level transfer or
// nested completion that arrives BEFORE the parent's stream completes can
// still resolve the right entry.
func (m *Model) noteResumableChildOwner(childCorrelationID, ownerCorrelationID string) {
	if m == nil {
		return
	}
	childCorrelationID = strings.TrimSpace(childCorrelationID)
	ownerCorrelationID = strings.TrimSpace(ownerCorrelationID)
	if childCorrelationID == "" || ownerCorrelationID == "" {
		return
	}
	if m.resumableChildOwners == nil {
		m.resumableChildOwners = make(map[string]string)
	}
	m.resumableChildOwners[childCorrelationID] = ownerCorrelationID
	if state, ok := m.resumablePrimaries[ownerCorrelationID]; ok {
		if state.children == nil {
			state.children = make(map[string]struct{})
		}
		state.children[childCorrelationID] = struct{}{}
		m.resumablePrimaries[ownerCorrelationID] = state
	}
}

func (m *Model) clearResumablePrimary(correlationID string) {
	if m == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	state, ok := m.resumablePrimaries[correlationID]
	if !ok {
		return
	}
	for childCID := range state.children {
		if owner := strings.TrimSpace(m.resumableChildOwners[childCID]); owner == correlationID {
			delete(m.resumableChildOwners, childCID)
		}
	}
	delete(m.resumablePrimaries, correlationID)
}

// evictStaleResumablePrimariesForAppend is invoked immediately after
// a new top-level entry is pushed onto history. It clears any
// resumable primary whose recorded entry now sits strictly above the
// newly-appended entry AND whose children set does not contain the
// new entry's correlation ID. The purpose is to preserve within-turn
// continuity — consult/challenge responses still correctly resume
// the parent agent's block because those children are recorded in
// state.children / resumableChildOwners before the respondent's
// entry lands — while preventing the "jump back" behavior when a
// genuinely new top-level turn from another agent (or a fresh
// same-agent re-invocation via pipeline handoff) appears below.
//
// Concretely: in Inspector → Tester → Inspector, the Tester's
// entry arrives here. Tester's correlation ID is not in the
// Inspector's resumable-primary children (Tester's top-level turn
// is not a consult/challenge child of the Inspector), so the
// Inspector's primary is evicted. The second Inspector turn then
// falls through reusableResumablePrimaryEntry and appends a new
// block at the tail instead of jumping back to the original
// Inspector entry.
func (m *Model) evictStaleResumablePrimariesForAppend(newCorrelationID string) {
	if m == nil || len(m.resumablePrimaries) == 0 {
		return
	}
	newCorrelationID = strings.TrimSpace(newCorrelationID)
	if newCorrelationID == "" {
		return
	}
	// Collect victims first so we don't mutate the map while walking.
	// Eviction is rare (bounded by active resumable primaries, which
	// is typically ≤ one per active agent turn), so a small fixed
	// allocation is fine.
	var stale []string
	for ownerCID, state := range m.resumablePrimaries {
		if ownerCID == newCorrelationID {
			// The new entry IS the primary (shouldn't happen via
			// push — resume uses UpdateAt — but guard anyway).
			continue
		}
		if _, isChild := state.children[newCorrelationID]; isChild {
			// Legitimate child (consult/challenge branch). Keep the
			// primary so its round-trip continuation can still
			// resume the parent block.
			continue
		}
		if owner := strings.TrimSpace(m.resumableChildOwners[newCorrelationID]); owner == ownerCID {
			// Same as above, via the owner-side linkage.
			continue
		}
		// Sibling top-level entry. The primary is now behind a
		// non-child entry in history; reusing it would produce the
		// "jump back to the previous instance" bug.
		stale = append(stale, ownerCID)
	}
	for _, ownerCID := range stale {
		chatDebugLog().Info("chat.evictStaleResumablePrimary",
			"owner_correlation_id", ownerCID,
			"new_correlation_id", newCorrelationID)
		m.clearResumablePrimary(ownerCID)
	}
}

func (m *Model) resumablePrimaryMatches(correlationID string, start msg.StreamStartMsg) bool {
	state, entry, ok := m.loadResumablePrimaryMatch(correlationID)
	if !ok {
		return false
	}
	if visibleAgentID := streamVisibleAgentID(start); visibleAgentID != "" && state.visibleAgentID != "" && state.visibleAgentID != visibleAgentID {
		return false
	}
	if agentType := streamEntryAgentType(start); agentType != "" && badgeAgentType(entry) != agentType {
		return false
	}
	// Replica scoping: if either side names a runtime agent, both
	// must agree. Keeps parallel librarian replicas on independent
	// rows even though they share visibleAgentID + agentType.
	if runtimeID := strings.TrimSpace(start.RuntimeAgentID); runtimeID != "" && state.runtimeAgentID != "" && state.runtimeAgentID != runtimeID {
		return false
	}
	if sessionID := strings.TrimSpace(start.SessionID); sessionID != "" && state.sessionID != "" && state.sessionID != sessionID {
		return false
	}
	if taskID := strings.TrimSpace(start.TaskID); taskID != "" && state.taskID != "" && state.taskID != taskID {
		return false
	}
	return true
}

func (m *Model) loadResumablePrimaryMatch(correlationID string) (resumablePrimaryState, *ChatEntry, bool) {
	state, ok := m.resumablePrimaries[correlationID]
	if !ok {
		return resumablePrimaryState{}, nil, false
	}
	idx := m.historyIndexForCorrelation(correlationID)
	if idx < 0 {
		m.clearResumablePrimary(correlationID)
		return resumablePrimaryState{}, nil, false
	}
	entry := m.history.Get(idx)
	if entry == nil || entry.Streaming {
		m.clearResumablePrimary(correlationID)
		return resumablePrimaryState{}, nil, false
	}
	return state, entry, true
}

func interAgentChildCorrelations(entry *ChatEntry) []string {
	if entry == nil {
		return nil
	}
	seen := make(map[string]struct{})
	collected := make([]string, 0, 4)
	var visit func([]InterAgentChildActivity)
	visit = func(children []InterAgentChildActivity) {
		for _, child := range children {
			if childCID := strings.TrimSpace(child.CorrelationID); childCID != "" {
				if _, ok := seen[childCID]; !ok {
					seen[childCID] = struct{}{}
					collected = append(collected, childCID)
				}
			}
			for _, toolCall := range child.ToolCalls {
				if toolCall.InterAgent != nil {
					visit(toolCall.InterAgent.Children)
				}
			}
		}
	}
	for _, toolCall := range entry.ToolCalls {
		if toolCall.InterAgent != nil {
			visit(toolCall.InterAgent.Children)
		}
	}
	return collected
}

func (m *Model) deferCompletedStreamSlot(slot *streamSlot) {
	if slot == nil || slot.accumulator == nil {
		return
	}
	now := time.Now()
	chatDebugLog().Info("chat.deferCompletedStreamSlot",
		"entry_index", slot.accumulator.EntryIndex(),
		"agent_id", slot.agentID,
		"thinking_idx", slot.thinkingIdx)
	slot.deferCompletion = true
	if slot.thinkingStart.IsZero() {
		slot.thinkingStart = now
	}
	slot.progress.retryText = deferredParentCompletionStatus
	slot.progress.retryToolDerived = false
	slot.progress.retryWatchdog = false
	slot.progress.retrySequence = slot.progress.normalizeSequence(0)
	slot.progress.clearPending()
	slot.progress.lastProgressSet = now
	m.syncSlotToEntry(slot)
	m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.progress.retryText, slot.thinkingStart, now)
	if idx := slot.accumulator.EntryIndex(); idx >= 0 {
		m.history.UpdateAt(idx, func(e *ChatEntry) {
			e.Streaming = true
			invalidateChatEntryRender(e)
		})
	}
	m.viewDirty = true
}

func (m *Model) finalizeSlotToolCalls(slot *streamSlot, success bool, errorMsg string) {
	if slot == nil || slot.accumulator == nil {
		return
	}
	idx := slot.accumulator.EntryIndex()
	doneAt := time.Now()
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		finalizeToolCallsSynthetic(e.ToolCalls, doneAt, success, errorMsg)
		invalidateChatEntryRender(e)
	})
}

func finalizeToolCallsSynthetic(calls []ToolCallRecord, doneAt time.Time, success bool, errorMsg string) {
	for i := range calls {
		finalizeToolCallRecordSynthetic(&calls[i], doneAt, success, errorMsg)
	}
}

func finalizeNestedStreamToolCallsOnTerminal(calls []ToolCallRecord, doneAt time.Time, success bool, errorMsg string) {
	for i := range calls {
		if allowNestedToolCompletionAfterStreamTerminal(calls[i]) {
			continue
		}
		finalizeToolCallRecordSynthetic(&calls[i], doneAt, success, errorMsg)
	}
}

func allowNestedToolCompletionAfterStreamTerminal(record ToolCallRecord) bool {
	switch strings.TrimSpace(record.ToolName) {
	case "web_search":
		return !record.Completed
	default:
		return false
	}
}

func finalizeToolCallRecordSynthetic(record *ToolCallRecord, doneAt time.Time, success bool, errorMsg string) {
	if record == nil || record.Completed {
		return
	}
	record.Duration = syntheticToolCallDuration(record.StartedAt, doneAt)
	record.Success = success
	record.Completed = true
	record.SyntheticCompletion = true
	if !success && strings.TrimSpace(record.ErrorMsg) == "" {
		record.ErrorMsg = strings.TrimSpace(errorMsg)
	}
	if record.InterAgent != nil {
		if record.InterAgent.Status == InterAgentToolPending {
			if success {
				record.InterAgent.Status = InterAgentToolDone
			} else {
				record.InterAgent.Status = InterAgentToolFailed
			}
		}
	}
}

func syntheticToolCallDuration(startedAt, doneAt time.Time) time.Duration {
	if startedAt.IsZero() || doneAt.Before(startedAt) {
		return 0
	}
	return doneAt.Sub(startedAt)
}

// adjustStreamSlotIndices decrements all stream slot indices after a history
// eviction. Called alongside adjustSteeringIndices when the ring buffer wraps.
func (m *Model) adjustStreamSlotIndices() {
	for _, slot := range m.streams {
		if slot.accumulator != nil {
			slot.accumulator.AdjustIndex(-1)
		}
		if slot.thinkingIdx >= 0 {
			slot.thinkingIdx--
		}
	}
}

// activitySource maps an ActivityEventMsg to the appropriate ChatSource.
func activitySource(ev msg.ActivityEventMsg) ChatSource {
	sourceMap := activitySourceTable()
	if src, ok := sourceMap[ev.Event.EventType.String()]; ok {
		return src
	}
	return SourceSystem
}

type activitySourceMap map[string]ChatSource

func activitySourceTable() activitySourceMap {
	return activitySourceMap{
		"user_prompt":         SourceUser,
		"user_clarification":  SourceUser,
		"agent_action":        SourceAgent,
		"agent_decision":      SourceAgent,
		"agent_error":         SourceError,
		"tool_call":           SourceTool,
		"tool_result":         SourceTool,
		"tool_timeout":        SourceError,
		"failure":             SourceError,
		"system_coordination": SourceSystem,
	}
}

// agentTypeFromData extracts the agent_type from the event's Data map.
func agentTypeFromData(ev msg.ActivityEventMsg) string {
	if ev.Event.Data == nil {
		return ""
	}
	val, ok := ev.Event.Data["agent_type"]
	if !ok {
		return ""
	}
	s, ok := val.(string)
	if !ok {
		return ""
	}
	return s
}

func activityStringData(ev msg.ActivityEventMsg, key string) string {
	if ev.Event.Data == nil {
		return ""
	}
	val, ok := ev.Event.Data[key]
	if !ok {
		return ""
	}
	s, ok := val.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func activityBoolData(ev msg.ActivityEventMsg, key string) bool {
	if ev.Event == nil || ev.Event.Data == nil {
		return false
	}
	value, ok := ev.Event.Data[key]
	if !ok {
		return false
	}
	typed, ok := value.(bool)
	return ok && typed
}

func (m *Model) handleClaimsAgentStatus(ev msg.ClaimsAgentStatusMsg) tea.Cmd {
	if m.claimRows == nil {
		m.claimRows = newClaimRowIndex()
	}
	cycleID := strings.TrimSpace(ev.CycleID)
	if cycleID == "" {
		return nil
	}
	// System-internal action types (boot/activation/shutdown/archival/
	// testament/checkpoint/consult_continuation) are housekeeping
	// cycles and MUST NOT open a chat row. The agent panel suppresses
	// their TaskSummary updates with the same predicate; this guard
	// is the chat-side equivalent that stops "System: Activation"
	// rows from flooding the chat tree.
	if claims.IsSystemInternalAction(claims.ActionType(ev.ActionType)) {
		return nil
	}
	if ev.Active {
		m.claimRows.openCycleRow(cycleID, ev.AgentID, ev.SessionID, ev.Reason, ev.ActionType)
		// Render the cycle as a ChatEntry so the chat tree has a
		// real row to attach artifacts to. Idempotent — a duplicate
		// open does not create a duplicate entry.
		m.ensureCycleChatEntry(cycleID, ev.AgentID, ev.SessionID, ev.StreamCorrelationID)
	} else {
		m.claimRows.closeCycleRow(cycleID, ev.TerminalOutcome)
		m.finalizeClaimEntry(cycleID, time.Now())
	}
	return nil
}

// ensureCycleChatEntry creates the cycle-row ChatEntry if it doesn't
// yet exist. Idempotent — re-opening with the same cycleID is a
// no-op. Uses the cycle owner's identity (passed in from
// ClaimsAgentStatusMsg.AgentID, which is the bridge's resolved cycle
// owner — NOT the issuer).
func (m *Model) ensureCycleChatEntry(cycleID, ownerAgentID, sessionID, streamCorrelationID string) {
	if strings.TrimSpace(cycleID) == "" {
		return
	}
	streamCID := strings.TrimSpace(streamCorrelationID)
	// Look for an existing entry under EITHER the cycle ID or the
	// route-level stream correlation. The stream bridge may have
	// pushed a "thinking" row keyed by streamCID before the cycle
	// opened (StreamStartMsg always lands first); folding the cycle
	// into that pre-existing entry instead of pushing a sibling is
	// what eliminates the split-row regression.
	if idx := m.historyIndexForCorrelation(cycleID); idx >= 0 {
		m.history.UpdateAt(idx, func(e *ChatEntry) {
			adoptCycleAttribution(e, ownerAgentID, sessionID, streamCID)
		})
		return
	}
	if streamCID != "" {
		if idx := m.historyIndexForCorrelation(streamCID); idx >= 0 {
			m.history.UpdateAt(idx, func(e *ChatEntry) {
				if e.CorrelationID != cycleID {
					e.AdditionalCorrelationIDs = appendUniqueCorrelationID(e.AdditionalCorrelationIDs, cycleID)
				}
				adoptCycleAttribution(e, ownerAgentID, sessionID, streamCID)
			})
			return
		}
	}
	entry := &ChatEntry{
		ID:            uuid.NewString(),
		Timestamp:     time.Now(),
		CorrelationID: cycleID,
		Source:        SourceAgent,
		AgentID:       ownerAgentID,
		AgentType:     ownerAgentID,
		SessionID:     sessionID,
		Streaming:     true,
		Height:        -1,
	}
	if streamCID != "" {
		entry.AdditionalCorrelationIDs = []string{streamCID}
	}
	m.history.Push(entry)
	m.viewDirty = true
}

// adoptCycleAttribution fills missing attribution on a cycle entry
// without overwriting existing values. The cycle's owner/session
// identity is canonical (the bridge resolved it from the claim
// graph) but pre-existing values from the stream layer remain
// preferred so a transient empty-string in the cycle update does
// not blow away accurate attribution.
func adoptCycleAttribution(e *ChatEntry, ownerAgentID, sessionID, streamCID string) {
	if e == nil {
		return
	}
	if e.AgentID == "" && ownerAgentID != "" {
		e.AgentID = ownerAgentID
	}
	if e.AgentType == "" && ownerAgentID != "" {
		e.AgentType = ownerAgentID
	}
	if e.SessionID == "" && sessionID != "" {
		e.SessionID = sessionID
	}
	if streamCID != "" && streamCID != e.CorrelationID {
		e.AdditionalCorrelationIDs = appendUniqueCorrelationID(e.AdditionalCorrelationIDs, streamCID)
	}
	invalidateChatEntryRender(e)
}

// appendUniqueCorrelationID returns the slice with id added iff it
// is not already present. Cap with the existing slice's underlying
// array; the slice typically holds ≤2 entries (cycle + stream) so
// the linear scan is trivial.
func appendUniqueCorrelationID(existing []string, id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return existing
	}
	for _, v := range existing {
		if v == id {
			return existing
		}
	}
	return append(existing, id)
}

// handleClaimArtifactAdded materializes a ClaimArtifactAddedMsg
// directly into the claims-native row index AND projects it into the
// chat history as a ToolCallRecord on the cycle's ChatEntry (creating
// the entry on first artifact). Row identity is (ArtifactID,
// ParentRowID, CycleID, ClaimID, OwnerAgentID) — none of it is
// squashed through the legacy ToolCallEventMsg shape.
//
// agent_state artifacts are NOT rendered as their own rows: they are
// the immutable trace of Context transitions, and the live UI signal
// is the claim's Context field (delivered separately via
// ClaimContextMsg). See docs/CLAIMS_UI.md.
func (m *Model) handleClaimArtifactAdded(ev msg.ClaimArtifactAddedMsg) tea.Cmd {
	if m.claimRows == nil {
		m.claimRows = newClaimRowIndex()
	}
	if ev.Kind == claims.ArtifactKindAgentState {
		// State trace: record on the cycle row's metadata if needed,
		// but never render as a row — the Context field is the
		// canonical surface for agent state.
		return nil
	}
	// response_text is the agent's final answer, not evidence: it
	// writes the cycle entry's Content. The bridge already routes
	// these as ClaimResponseTextMsg, but tests + replay paths can
	// also deliver them as a generic ClaimArtifactAddedMsg, so
	// promote here to keep the contract uniform regardless of
	// delivery path.
	if ev.Kind == claims.ArtifactKindResponseText {
		return m.handleClaimResponseText(msg.ClaimResponseTextMsg{
			SessionID: "",
			CycleID:   ev.CycleID,
			ClaimID:   ev.ClaimID,
			AgentID:   ev.AgentID,
			Content:   ev.Reference,
			CreatedAt: ev.CreatedAt,
		})
	}
	// Defensive: the bridge already filters telemetry kinds out of
	// the visible artifact stream (UI_DESIGN.md §2.4). If anything
	// upstream regresses and these leak through anyway, never let
	// them become rows here either.
	if !isVisibleArtifactKindForChat(ev.Kind) {
		return nil
	}
	agentID := ev.AgentID
	if agentID == "" {
		agentID = ev.OwnerAgentID
	}
	row := &ArtifactRow{
		ArtifactID:  ev.ArtifactID,
		ParentRowID: ev.ParentRowID,
		CycleID:     ev.CycleID,
		ClaimID:     ev.ClaimID,
		Kind:        ev.Kind,
		Reference:   ev.Reference,
		ArgsSummary: shortenArtifactSummary(ev),
		AgentID:     agentID,
		AgentType:   firstNonEmptyString(agentID, ev.OwnerAgentType),
		TargetAgent: ev.TargetAgentID,
		Status:      ArtifactRowStatusInFlight,
		StartedAt:   ev.CreatedAt,
		Metadata:    ev.Metadata,
	}
	stored := m.claimRows.addArtifactRow(row)
	if stored != nil {
		m.projectClaimArtifactToHistory(stored)
	}
	// Stamp claim-row identity onto the cycle (auto-created if not
	// yet seen) so the cycle row carries the agent/claim/title
	// metadata even when the bridge's claim_created delta hasn't
	// arrived yet.
	if cycle := m.claimRows.ensureClaimRow(ev.CycleID); cycle != nil {
		if cycle.OwnerAgentID == "" && ev.OwnerAgentID != "" {
			cycle.OwnerAgentID = ev.OwnerAgentID
		}
		if cycle.OwnerAgentType == "" && ev.OwnerAgentType != "" {
			cycle.OwnerAgentType = ev.OwnerAgentType
		}
		if cycle.TargetAgentID == "" && ev.TargetAgentID != "" {
			cycle.TargetAgentID = ev.TargetAgentID
		}
		if cycle.ClaimID == "" && ev.ClaimID != "" {
			cycle.ClaimID = ev.ClaimID
		}
	}
	return nil
}

// handleClaimArtifactCompleted flips an in-flight artifact row to its
// terminal state. The row is keyed by StartArtifactID — the same
// ArtifactID that opened the row in handleClaimArtifactAdded.
func (m *Model) handleClaimArtifactCompleted(ev msg.ClaimArtifactCompletedMsg) tea.Cmd {
	if m.claimRows == nil {
		m.claimRows = newClaimRowIndex()
	}
	status := ArtifactRowStatusSuccess
	switch ev.Outcome {
	case "success":
		status = ArtifactRowStatusSuccess
	case "failure":
		status = ArtifactRowStatusFailure
	case "timeout":
		status = ArtifactRowStatusTimeout
	case "cancelled":
		status = ArtifactRowStatusCancelled
	default:
		// Unknown outcome — preserve the prior status semantically as
		// "completed" by mapping to success; an explicit error on the
		// metadata still flips it via the error field below.
		status = ArtifactRowStatusSuccess
	}
	errMsg := asString(ev.Metadata["error"])
	if errMsg != "" && status == ArtifactRowStatusSuccess {
		status = ArtifactRowStatusFailure
	}
	completed := m.claimRows.completeArtifactRow(
		ev.StartArtifactID,
		status,
		ev.CompletedAt,
		ev.Duration,
		ev.Summary,
		errMsg,
		ev.Metadata,
	)
	if completed != nil {
		m.completeClaimArtifactInHistory(completed)
	}
	return nil
}

// handleClaimContext updates the cycle row's narrative status text
// in place. ContextSequence guards against out-of-order delivery.
// The narrative also threads through the projected ChatEntry's
// ThinkingStatus so users see the live status under the agent's row.
func (m *Model) handleClaimContext(ev msg.ClaimContextMsg) tea.Cmd {
	if m.claimRows == nil {
		m.claimRows = newClaimRowIndex()
	}
	r := m.claimRows.updateClaimContext(ev.CycleID, ev.Context, ev.ContextTransition)
	if r != nil {
		if r.OwnerAgentID == "" && ev.OwnerAgentID != "" {
			r.OwnerAgentID = ev.OwnerAgentID
		}
		if r.ClaimID == "" && ev.ClaimID != "" {
			r.ClaimID = ev.ClaimID
		}
		if r.SessionID == "" && ev.SessionID != "" {
			r.SessionID = ev.SessionID
		}
		m.applyClaimContextToHistory(ev.CycleID, ev.Context)
	}
	return nil
}

// handleClaimResponseText writes the agent's final answer onto the
// cycle's ChatEntry. response_text is the answer, not evidence —
// the spec lists *_started / *_completed as the visible artifact
// lifecycle and excludes response_text. Without this handler, the
// answer either leaks as a fake "tool" row (when it falls back
// through projectClaimArtifactToHistory) or is invisible entirely.
func (m *Model) handleClaimResponseText(ev msg.ClaimResponseTextMsg) tea.Cmd {
	cycleID := strings.TrimSpace(ev.CycleID)
	content := strings.TrimSpace(ev.Content)
	if cycleID == "" || content == "" {
		return nil
	}
	idx := m.historyIndexForCorrelation(cycleID)
	if idx < 0 {
		return nil
	}
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		e.Content = content
		invalidateChatEntryRender(e)
	})
	m.viewDirty = true
	return nil
}

// isVisibleArtifactKindForChat is the chat panel's defensive copy
// of the bridge's visible-kind allowlist (ui/bridge/claims.go
// isVisibleStartedArtifactKind / isVisibleCompletedArtifactKind).
// Keeping a parallel filter here means a regression upstream that
// emits llm_started or response_text as a regular artifact event
// cannot revive the "fake claude-opus-4-6 tool row" bug class.
func isVisibleArtifactKindForChat(kind string) bool {
	switch kind {
	case "tool_started", "consult_started", "challenge_started", "guardian_check_started",
		"tool_completed", "consult_completed", "challenge_completed", "guardian_check_completed":
		return true
	}
	return false
}

// handleTestamentContext updates an in-flight testament row's
// developing-conclusion narrative. Resolves by AccumulatorID first
// (in-flight anchor) then TestamentID (post-flush rebind).
func (m *Model) handleTestamentContext(ev msg.TestamentContextMsg) tea.Cmd {
	if m.claimRows == nil {
		m.claimRows = newClaimRowIndex()
	}
	m.claimRows.updateTestamentContext(
		ev.AccumulatorID,
		ev.TestamentID,
		ev.ClaimID,
		ev.CycleID,
		ev.AgentID,
		ev.Context,
		ev.ContextTransition,
	)
	return nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func shortenArtifactSummary(ev msg.ClaimArtifactAddedMsg) string {
	if ev.Reference != "" {
		return ev.Reference
	}
	return ev.Kind
}
