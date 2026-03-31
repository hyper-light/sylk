package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

const deferredParentCompletionStatus = "Waiting for child work to finish..."

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
type streamSlot struct {
	accumulator     *StreamAccumulator
	agentID         string
	thinkingIdx     int                // History index of thinking placeholder for this stream.
	thinkingStart   time.Time          // When this stream's thinking phase began.
	retryText       string             // Progress override shown instead of fun messages.
	lastProgressSet time.Time          // Last immediate progress text write time.
	renderState     *streamRenderState // Incremental render state for this stream.
	planID          string             // Plan ID embedded in this stream, if any.
	planMarkdown    string             // Rendered plan markdown for this stream.
	planOffset      int                // Accumulator content length when plan was injected.
	deferCompletion bool               // Stream text is done, but child inter-agent work is still settling.
}

// nestedStreamSlot tracks a child agent stream that belongs to an inter-agent
// consult/challenge branch owned by a parent chat entry.
type nestedStreamSlot struct {
	correlationID   string
	branchRef       msg.InterAgentBranchRefMsg
	thinkingStart   time.Time
	retryText       string
	lastProgressSet time.Time
	content         strings.Builder
	activity        InterAgentChildActivity
	terminalSeen    bool
	terminalFailed  bool
	done            bool
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
	thinkingIdx      int             // History index of thinking placeholder (-1 = inactive).
	thinkingAgentID  string          // Agent currently thinking (for agent-specific messages).
	thinkingStart    time.Time       // When current thinking phase began.
	retryText        string          // Retry/model-fallback status (replaces fun messages when set).
	thinkingGradient *theme.Gradient // Color gradient cycled during thinking animation.
	lastProgressSet  time.Time       // Last immediate progress text write time.

	// Inline plan tracking: the plan renders as a chat entry updated in place.
	planEntryIdx int    // History index of the plan ChatEntry (-1 = no plan entry).
	planID       string // Correlates updates to the correct entry.

	// Render throttle: chunks buffer at full speed, but the history entry
	// is only synced (and viewDirty set) on DecorTick or StreamComplete.
	streamRenderPending bool

	// Completed entries with pending inter-agent rows still need spinner ticks
	// while a consultation/challenge is awaiting a later response.
	pendingInterAgent map[int]struct{}

	// Steering animation: pending entries shimmer with holographic color until acknowledged.
	steeringPending  []steeringPendingEntry
	steeringGradient *theme.Gradient
	steeringStart    time.Time

	// View cache: avoids re-rendering when no visible state changed.
	viewCache string
	viewDirty bool
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
		history:           h,
		viewport:          vp,
		streams:           make(map[string]*streamSlot),
		nestedStreams:     make(map[string]*nestedStreamSlot),
		theme:             th,
		thinkingIdx:       -1,
		planEntryIdx:      -1,
		pendingInterAgent: make(map[int]struct{}),
		thinkingGradient:  th.Palette.ThinkingGradient(),
		steeringGradient:  th.Palette.GroupGradient(),
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
	case msg.ActivityEventMsg:
		m.viewDirty = true
		return m, m.handleActivity(typed)
	case msg.StreamStartMsg:
		m.viewDirty = true
		return m, m.handleStreamStart(typed)
	case msg.StreamChunkMsg:
		return m, m.handleStreamChunk(typed)
	case msg.StreamProgressMsg:
		return m, m.handleStreamProgress(typed)
	case msg.StreamCompleteMsg:
		m.viewDirty = true
		return m, m.handleStreamComplete(typed)
	case msg.StreamErrorMsg:
		m.viewDirty = true
		return m, m.handleStreamError(typed)
	case msg.StreamRerouteMsg:
		m.viewDirty = true
		return m, m.handleStreamReroute(typed)
	case msg.RetryStatusMsg:
		m.viewDirty = true
		return m, m.handleRetryStatus(typed)
	case msg.ToolCallEventMsg:
		m.viewDirty = true
		return m, m.handleToolCallEvent(typed)
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

// handleStreamStart begins accumulation. If a thinking placeholder exists,
// it is reused; otherwise a new entry is pushed. Multiple concurrent streams
// are tracked in the streams map keyed by correlationID.
func (m *Model) handleStreamStart(start msg.StreamStartMsg) tea.Cmd {
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
		"active_streams", len(m.streams),
		"thinking_idx", m.thinkingIdx)
	now := time.Now()
	streamAgentType := streamEntryAgentType(start)

	if slot, ok := m.streams[cid]; ok && slot.accumulator != nil {
		m.refreshExistingStreamSlot(slot, start)
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
	willEvict := m.history.Full()
	m.history.Push(entry)
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
	return nil
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
	if progress.BranchRef != nil {
		m.handleNestedStreamProgress(progress)
		return nil
	}
	if m.handleNestedStreamProgress(progress) {
		return nil
	}
	message := sanitizeThinkingMessage(progress.Message)
	if slot := m.streamSlot(progress.CorrelationID); slot != nil {
		m.updateSlotThinkingAgent(slot, progress.AgentID)
		m.applySlotProgress(slot, message)
		return nil
	}
	m.updateThinkingAgent(progress.AgentID)
	if message == "" {
		return nil
	}
	if m.thinkingIdx < 0 {
		return nil
	}
	if message == m.retryText {
		return nil
	}
	m.retryText = message
	now := time.Now()
	if m.lastProgressSet.IsZero() || now.Sub(m.lastProgressSet) >= thinkingProgressMinInterval {
		m.lastProgressSet = now
		m.setThinkingTextNow(message)
	}
	return nil
}

// handleStreamComplete finalizes a single streaming entry by correlationID.
func (m *Model) handleStreamComplete(done msg.StreamCompleteMsg) tea.Cmd {
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

	if m.deferSlotCompletionIfPending(slot) {
		chatDebugLog().Info("chat.handleStreamComplete: DEFERRED",
			"correlation_id", cid,
			"entry_index", slot.accumulator.EntryIndex())
		return nil
	}

	m.finalizeCompletedStreamSlot(cid, slot, true, "")

	chatDebugLog().Info("chat.handleStreamComplete: DONE",
		"correlation_id", cid,
		"remaining_streams", len(m.streams))
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
			if settlePipelineChallengeRowsForReroute(e) {
				invalidateChatEntryRender(e)
			}
		})
		delete(m.pendingInterAgent, entryIdx)
	}
	slot.deferCompletion = false
	m.resolveSlotThinkingEntry(slot)
	if entryIdx >= 0 {
		m.history.UpdateAt(entryIdx, func(e *ChatEntry) {
			invalidateChatEntryRender(e)
		})
	}
	m.viewDirty = true
	return nil
}

func settlePipelineChallengeRowsForReroute(entry *ChatEntry) bool {
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
		if !strings.HasPrefix(strings.TrimSpace(record.InterAgent.ThreadKey), pipelineThreadPrefix) {
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
		m.applySlotProgress(slot, message)
		return nil
	}
	if m.thinkingIdx >= 0 {
		m.retryText = message
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
	return nil
}

func toolCallRecordMatchesEvent(record ToolCallRecord, ev msg.ToolCallEventMsg) bool {
	recordName := strings.TrimSpace(record.ToolName)
	eventName := strings.TrimSpace(ev.ToolName)
	if recordName != "" && eventName != "" && recordName != eventName {
		return false
	}
	recordKey := strings.TrimSpace(record.ToolCallKey)
	eventKey := strings.TrimSpace(ev.ToolCallKey)
	if recordKey != "" && eventKey != "" {
		if recordKey == eventKey {
			return true
		}
		return toolCallArgumentsMatch(record, ev)
	}
	if toolCallArgumentsMatch(record, ev) {
		return true
	}
	return recordName != "" && recordName == eventName
}

func toolCallRecordCanAcceptCompletion(record ToolCallRecord) bool {
	return !record.Completed || record.SyntheticCompletion
}

func toolCallArgumentsMatch(record ToolCallRecord, ev msg.ToolCallEventMsg) bool {
	recordArgs := toolCallArgumentsIdentity(record.FullArgs, record.ArgsSummary)
	eventArgs := toolCallArgumentsIdentity(ev.FullArgs, ev.ArgsSummary)
	if recordArgs == "" || eventArgs == "" {
		return false
	}
	return recordArgs == eventArgs
}

func toolCallArgumentsIdentity(fullArgs, argsSummary string) string {
	if normalized := normalizeToolCallArgumentsText(fullArgs); normalized != "" {
		return "args:" + normalized
	}
	if normalized := normalizeToolCallArgumentsText(argsSummary); normalized != "" {
		return "summary:" + normalized
	}
	return ""
}

func normalizeToolCallArgumentsText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		if normalized, marshalErr := json.Marshal(parsed); marshalErr == nil {
			return string(normalized)
		}
	}
	return strings.Join(strings.Fields(text), " ")
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
		if strings.TrimSpace(m.history.entries[physical].CorrelationID) == correlationID {
			return idx
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
		if isInterAgentResponseTool(ev.ToolName) {
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
		if m.applyInterAgentOriginUpdate(ev, currentAgentType) {
			m.syncAllNestedInterAgentStreams()
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
		e.ToolCalls[recordIdx].InterAgent.AgentTypes = append([]string(nil), row.AgentTypes...)
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
		m.finalizeDeferredStreamSlotsForEntry(idx)
		return
	}
	m.pendingInterAgent[idx] = struct{}{}
}

func (m *Model) deferSlotCompletionIfPending(slot *streamSlot) bool {
	if slot == nil || slot.accumulator == nil {
		return false
	}
	if !shouldDeferEntryCompletion(m.history.Get(slot.accumulator.EntryIndex())) {
		return false
	}
	slot.deferCompletion = true
	slot.retryText = deferredParentCompletionStatus
	slot.lastProgressSet = time.Time{}
	m.setSlotThinkingTextNow(slot, deferredParentCompletionStatus)
	m.syncSlotToEntry(slot)
	if slot.renderState == nil {
		slot.renderState = &streamRenderState{}
	}
	m.viewport.AddStreamState(slot.accumulator.EntryIndex(), slot.renderState)
	m.viewDirty = true
	return true
}

func (m *Model) finalizeDeferredStreamSlotsForEntry(idx int) {
	if idx < 0 {
		return
	}
	keys := make([]string, 0, 1)
	for correlationID, slot := range m.streams {
		if slot == nil || !slot.deferCompletion || slot.accumulator == nil {
			continue
		}
		if slot.accumulator.EntryIndex() != idx {
			continue
		}
		keys = append(keys, correlationID)
	}
	for _, correlationID := range keys {
		slot := m.streams[correlationID]
		if slot == nil || !slot.deferCompletion || slot.accumulator == nil {
			continue
		}
		if shouldDeferEntryCompletion(m.history.Get(slot.accumulator.EntryIndex())) {
			continue
		}
		m.finalizeCompletedStreamSlot(correlationID, slot, true, "")
	}
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
	slot.retryText = ""
	slot.lastProgressSet = time.Time{}
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
	if message == "" &&
		slot.thinkingStart.IsZero() &&
		strings.TrimSpace(slot.activity.ThinkingText) == "" &&
		strings.TrimSpace(slot.activity.ThinkingStatus) == "" &&
		len(slot.activity.ToolCalls) == 0 &&
		strings.TrimSpace(slot.activity.ResultSummary) == "" {
		return true
	}
	if message != "" {
		slot.retryText = message
	}
	if slot.thinkingStart.IsZero() {
		slot.thinkingStart = time.Now()
	}
	now := time.Now()
	if slot.lastProgressSet.IsZero() || now.Sub(slot.lastProgressSet) >= thinkingProgressMinInterval {
		slot.lastProgressSet = now
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
	slot.activity.ThinkingText = ""
	slot.activity.ThinkingStatus = ""
	slot.activity.ThinkingColor = ""
	slot.thinkingStart = time.Time{}
	slot.retryText = ""
	slot.lastProgressSet = time.Time{}
	slot.terminalSeen = true
	slot.terminalFailed = false
	finalizeNestedStreamToolCallsOnTerminal(slot.activity.ToolCalls, time.Now(), true, summary)
	m.finalizeNestedStreamSlotIfReady(slot, time.Now())
	m.syncPendingNestedStream(slot)
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
	slot.activity.ThinkingText = ""
	slot.activity.ThinkingStatus = ""
	slot.activity.ThinkingColor = ""
	slot.thinkingStart = time.Time{}
	slot.retryText = ""
	slot.lastProgressSet = time.Time{}
	slot.terminalSeen = true
	slot.terminalFailed = true
	finalizeNestedStreamToolCallsOnTerminal(slot.activity.ToolCalls, time.Now(), false, slot.activity.ResultSummary)
	m.finalizeNestedStreamSlotIfReady(slot, time.Now())
	m.syncPendingNestedStream(slot)
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
	if ev.AgentID != "" {
		slot.activity.AgentID = strings.TrimSpace(ev.AgentID)
		if strings.TrimSpace(slot.activity.AgentType) == "" {
			slot.activity.AgentType = strings.TrimSpace(ev.AgentID)
		}
	}
	currentAgentType := nestedActivityAgentType(&slot.activity)
	if handleInterAgentToolCallInList(&slot.activity.ToolCalls, currentAgentType, ev) {
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

func (m *Model) nestedStream(correlationID string) *nestedStreamSlot {
	if strings.TrimSpace(correlationID) == "" {
		return nil
	}
	if m.nestedStreams == nil {
		return nil
	}
	return m.nestedStreams[correlationID]
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
		if m.renderNestedStreamThinking(slot, now) {
			if m.syncPendingNestedStream(slot) {
				updated = true
			}
		}
	}
	return updated
}

func (m *Model) renderNestedStreamThinking(slot *nestedStreamSlot, now time.Time) bool {
	if slot == nil {
		return false
	}
	if slot.thinkingStart.IsZero() {
		return false
	}
	if strings.TrimSpace(slot.retryText) == "" {
		if slot.activity.ThinkingText == "" && slot.activity.ThinkingStatus == "" && slot.activity.ThinkingColor == "" {
			return false
		}
		slot.activity.ThinkingText = ""
		slot.activity.ThinkingStatus = ""
		slot.activity.ThinkingColor = ""
		return true
	}
	elapsed := thinkingElapsed(slot.thinkingStart, now)
	text := fmt.Sprintf("%s  %.1fs", spinnerFrames[thinkingFrameAt(elapsed)], elapsed.Seconds())
	status := thinkingStatusFor(nestedActivityAgentType(&slot.activity), slot.retryText, elapsed)
	color := m.thinkingColorFor(elapsed)
	if slot.activity.ThinkingText == text &&
		slot.activity.ThinkingStatus == status &&
		slot.activity.ThinkingColor == color {
		return false
	}
	slot.activity.ThinkingText = text
	slot.activity.ThinkingStatus = status
	slot.activity.ThinkingColor = color
	return true
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
			if updateInterAgentBranchInToolCalls(entry.CorrelationID, &entry.ToolCalls, parentCorrelationID, parentToolCallKey, threadKey, kind, fn) {
				invalidateChatEntryRender(entry)
				found = true
			}
		})
	}
	return found
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

func toolCallRecordsShareIdentity(left, right ToolCallRecord) bool {
	leftKey := strings.TrimSpace(left.ToolCallKey)
	rightKey := strings.TrimSpace(right.ToolCallKey)
	if leftKey != "" && rightKey != "" && leftKey == rightKey {
		return true
	}
	leftArgs := toolCallArgumentsIdentity(left.FullArgs, left.ArgsSummary)
	rightArgs := toolCallArgumentsIdentity(right.FullArgs, right.ArgsSummary)
	if leftArgs != "" && rightArgs != "" {
		if leftArgs != rightArgs {
			return false
		}
		leftName := strings.TrimSpace(left.ToolName)
		rightName := strings.TrimSpace(right.ToolName)
		return leftName == "" || rightName == "" || leftName == rightName
	}
	leftName := strings.TrimSpace(left.ToolName)
	rightName := strings.TrimSpace(right.ToolName)
	return leftName != "" && leftName == rightName
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

func handleInterAgentToolCallInList(calls *[]ToolCallRecord, currentAgentType string, ev msg.ToolCallEventMsg) bool {
	if calls == nil {
		return false
	}
	switch ev.Phase {
	case 0:
		if isInterAgentResponseTool(ev.ToolName) {
			return true
		}
		record, ok := buildInterAgentStartRecord(ev)
		if !ok {
			return false
		}
		record.StartedAt = ev.StartedAt
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

func toggleTargetMatchesToolCall(target *toggleTarget, record ToolCallRecord, child bool) bool {
	if target == nil {
		return false
	}
	key := strings.TrimSpace(target.toolCallKey)
	name := strings.TrimSpace(target.toolCallName)
	argsID := strings.TrimSpace(target.toolCallArgsID)
	if child {
		key = strings.TrimSpace(target.childToolCallKey)
		name = strings.TrimSpace(target.childToolCallName)
		argsID = strings.TrimSpace(target.childToolCallArgsID)
	}
	if key != "" && strings.TrimSpace(record.ToolCallKey) == key {
		return true
	}
	recordArgsID := toolCallArgumentsIdentity(record.FullArgs, record.ArgsSummary)
	if argsID != "" && recordArgsID == argsID {
		return name == "" || strings.TrimSpace(record.ToolName) == name
	}
	if key == "" && argsID == "" && name != "" && strings.TrimSpace(record.ToolName) == name {
		return true
	}
	return false
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
		target.toolCallArgsID = toolCallArgumentsIdentity(record.FullArgs, record.ArgsSummary)
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
			target.childToolCallArgsID = toolCallArgumentsIdentity(child.ToolCalls[childToolCallIdx].FullArgs, child.ToolCalls[childToolCallIdx].ArgsSummary)
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
		leftChildToolArgsID := strings.TrimSpace(left.childToolCallArgsID)
		rightChildToolArgsID := strings.TrimSpace(right.childToolCallArgsID)
		if leftChildToolArgsID != "" && rightChildToolArgsID != "" {
			return leftChildToolArgsID == rightChildToolArgsID &&
				(strings.TrimSpace(left.childToolCallName) == "" ||
					strings.TrimSpace(right.childToolCallName) == "" ||
					strings.TrimSpace(left.childToolCallName) == strings.TrimSpace(right.childToolCallName))
		}
		return left.childToolCallIdx == right.childToolCallIdx
	default:
		return false
	}
}

func sameToolCallToggleTarget(left, right *toggleTarget) bool {
	leftKey := strings.TrimSpace(left.toolCallKey)
	rightKey := strings.TrimSpace(right.toolCallKey)
	if leftKey != "" && rightKey != "" {
		return leftKey == rightKey
	}
	leftArgsID := strings.TrimSpace(left.toolCallArgsID)
	rightArgsID := strings.TrimSpace(right.toolCallArgsID)
	if leftArgsID != "" && rightArgsID != "" {
		return leftArgsID == rightArgsID &&
			(strings.TrimSpace(left.toolCallName) == "" ||
				strings.TrimSpace(right.toolCallName) == "" ||
				strings.TrimSpace(left.toolCallName) == strings.TrimSpace(right.toolCallName))
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
	if m.renderThinkingEntry(m.thinkingIdx, m.thinkingAgentID, m.retryText, m.thinkingStart, now) {
		updated = true
	}
	for _, slot := range m.streams {
		if slot == nil {
			continue
		}
		if m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.retryText, slot.thinkingStart, now) {
			updated = true
		}
	}
	if m.tickNestedStreamThinking(now) {
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
	m.lastProgressSet = time.Time{}
	m.retryText = ""
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
	m.retryText = ""
	m.lastProgressSet = time.Time{}
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
		m.retryText = ""
	}
	m.thinkingAgentID = agentID
	idx := m.thinkingIdx
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		m.history.entries[physical].AgentType = agentID
		m.history.entries[physical].AgentID = agentID
		m.history.entries[physical].RenderedLines = nil
		m.history.entries[physical].CodeRegions = nil
		m.history.entries[physical].Height = -1
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
	slot.agentID = streamEntryAgentType(start)
	m.updateStreamEntryMetadata(slot.accumulator.EntryIndex(), start)
	if !shouldResetStreamSlot(slot) {
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
	m.renderThinkingEntry(slot.thinkingIdx, slot.agentID, slot.retryText, slot.thinkingStart, slot.thinkingStart)
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
	slot.retryText = m.retryText
	slot.lastProgressSet = m.lastProgressSet
}

func (m *Model) startSlotThinkingAnimation(now time.Time, slot *streamSlot) {
	if slot == nil {
		return
	}
	slot.thinkingStart = now
	slot.retryText = ""
	slot.lastProgressSet = time.Time{}
	slot.deferCompletion = false
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

func (m *Model) applySlotProgress(slot *streamSlot, message string) {
	if slot == nil || slot.thinkingIdx < 0 || message == "" {
		return
	}
	if message == slot.retryText {
		return
	}
	slot.retryText = message
	now := time.Now()
	if slot.lastProgressSet.IsZero() || now.Sub(slot.lastProgressSet) >= thinkingProgressMinInterval {
		slot.lastProgressSet = now
		if m.setSlotThinkingTextNow(slot, message) {
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
		slot.retryText = ""
	}
	slot.agentID = agentID
	m.history.UpdateAt(slot.thinkingIdx, func(entry *ChatEntry) {
		entry.AgentType = agentID
		entry.AgentID = agentID
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
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		m.history.entries[physical].ThinkingElapsed = elapsed
		m.history.entries[physical].ThinkingText = ""
		m.history.entries[physical].ThinkingStatus = ""
		m.history.entries[physical].ThinkingColor = ""
	}
	m.history.mu.Unlock()
	slot.thinkingStart = time.Time{}
	slot.retryText = ""
	slot.lastProgressSet = time.Time{}
}

func (m *Model) renderThinkingEntry(idx int, agentID, retryText string, start, now time.Time) bool {
	if idx < 0 || start.IsZero() {
		return false
	}
	elapsed := thinkingElapsed(start, now)
	text := fmt.Sprintf("%s  %.1fs", spinnerFrames[thinkingFrameAt(elapsed)], elapsed.Seconds())
	return m.writeThinkingEntry(idx, text, thinkingStatusFor(agentID, retryText, elapsed), m.thinkingColorFor(elapsed))
}

func (m *Model) setThinkingEntryNow(idx int, start time.Time, message string) bool {
	message = sanitizeThinkingMessage(message)
	if idx < 0 || message == "" {
		return false
	}
	now := time.Now()
	elapsed := thinkingElapsed(start, now)
	text := fmt.Sprintf("%s  %.1fs", spinnerFrames[thinkingFrameAt(elapsed)], elapsed.Seconds())
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
		entry.CorrelationID = start.CorrelationID
		entry.SessionID = start.SessionID
		entry.TaskID = strings.TrimSpace(start.TaskID)
		entry.TaskName = strings.TrimSpace(start.TaskName)
		entry.TaskSlug = strings.TrimSpace(start.TaskSlug)
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
	slot.deferCompletion = false
	m.finalizeSlotStream(slot)
	delete(m.streams, correlationID)
	if len(m.streams) == 0 {
		m.streamRenderPending = false
	}
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
		"user_prompt":        SourceUser,
		"user_clarification": SourceUser,
		"agent_action":       SourceAgent,
		"agent_decision":     SourceAgent,
		"agent_error":        SourceError,
		"tool_call":          SourceTool,
		"tool_result":        SourceTool,
		"tool_timeout":       SourceError,
		"failure":            SourceError,
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
