package chat

import (
	"fmt"
	"strings"
	"time"

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
		"New construction options...",
		"Cannot deploy here... recalculating...",
		"Expanding the base...",
		"That's a load-bearing abstraction...",
		"Hmm, needs more layers...",
		"Whoever built this... interesting choices...",
		"I see the vision. Give me a moment...",
		"Rearranging the dependency graph... again...",
		"This calls for a hexagonal approach...",
		"One does not simply skip the design phase...",
	},
	"orchestrator": {
		"All units, move out...",
		"Establishing command link...",
		"Unit ready. Awaiting orders...",
		"Rally point set. Dispatching...",
		"I love it when a plan comes together...",
		"Assigning agents to sectors...",
		"Queue is deep. Prioritizing...",
		"Everyone hold formation...",
		"Too many cooks? I'll manage the kitchen...",
		"Syncing the pipeline... almost there...",
	},
	"engineer": {
		"I've got the tools for this...",
		"Yes sir, right away!",
		"Need a repair? Say no more...",
		"Let me just... refactor that real quick...",
		"Should be a one-liner... famous last words...",
		"Who wrote this? ...oh wait, that was me...",
		"Compiling. Again. Naturally...",
		"Just one more edge case...",
		"It works on my machine...",
		"Trust me, I know what I'm doing...",
	},
	"designer": {
		"Rendering the schematic...",
		"Optimizing the layout...",
		"That interface could use some love...",
		"Form follows function... usually...",
		"Aligning to the grid...",
		"No, the OTHER shade of blue...",
		"The whiteboard has spoken...",
		"Sketching... hold on, this is good...",
		"Component tree is looking elegant...",
		"Less is more. Mostly...",
	},
	"inspector": {
		"Scanning... I see everything...",
		"Something's off here...",
		"Nothing gets past me...",
		"That's a code smell. Noted...",
		"Running full diagnostic...",
		"Hmm. This passed review?",
		"Found one. There's always one...",
		"Checking every corner...",
		"Trust but verify. Mostly verify...",
		"The cyclomatic complexity... it's over 9000...",
	},
	"tester": {
		"If it can break, I'll find out...",
		"All systems nominal... so far...",
		"Let's see how this handles null...",
		"Edge case #347. My favorite...",
		"The happy path is boring anyway...",
		"Fuzzing with malicious intent...",
		"Oh this is going to be fun...",
		"100% coverage is a state of mind...",
		"What happens if I do THIS...",
		"Results incoming... brace yourself...",
	},
	"librarian": {
		"I know exactly where that is...",
		"Cross-referencing the records...",
		"Pulling files from the vault...",
		"Shh... I'm indexing...",
		"It's in the archive. Give me a second...",
		"Classified material retrieved...",
		"The codex has an entry for this...",
		"Dewey would be proud...",
	},
	"academic": {
		"Researching... do not disturb...",
		"The literature suggests...",
		"Fascinating. Let me dig deeper...",
		"According to my findings...",
		"Lab results are promising...",
		"Theoretical models converging...",
		"Peer review is a lonely business...",
		"New hypothesis forming...",
	},
	"archivalist": {
		"Committing to the chronicle...",
		"History will remember this...",
		"Preserving for future generations...",
		"Filing under 'lessons learned'...",
		"The record must be complete...",
		"Timestamped and cataloged...",
		"Another chapter written...",
		"Nothing is ever truly deleted...",
	},
	"guardian": {
		"Perimeter secure...",
		"All defenses online...",
		"Holding the line...",
		"Nothing gets through...",
		"You shall not pass... unchecked...",
		"Validating credentials...",
		"I don't trust that input...",
		"Guardian protocol engaged...",
	},
}

// thinkingMessagesForAgent returns the agent-specific message list, falling
// back to the generic list. Agent IDs like "architect_abc123" are matched by
// prefix against the map keys.
func thinkingMessagesForAgent(agentID string) []string {
	if agentID == "" {
		return thinkingMessages[:]
	}
	// Exact match first, then prefix match (agent IDs often have suffixes).
	if msgs, ok := agentThinkingMessages[agentID]; ok {
		return msgs
	}
	for prefix, msgs := range agentThinkingMessages {
		if strings.HasPrefix(agentID, prefix) {
			return msgs
		}
	}
	return thinkingMessages[:]
}

// streamSlot tracks per-stream accumulation state. Multiple slots can be
// active concurrently when several agents stream in parallel (e.g. architect
// and orchestrator).
type streamSlot struct {
	accumulator     *StreamAccumulator
	agentID         string
	thinkingIdx     int                // History index of thinking placeholder for this stream.
	renderState     *streamRenderState // Incremental render state for this stream.
	planMarkdown    string             // Rendered plan markdown for this stream.
	planOffset      int                // Accumulator content length when plan was injected.
}

// Model is the Bubble Tea model for the chat panel.
// It displays a scrollable history of chat entries with virtual scrolling,
// supports LLM streaming, and handles keyboard navigation.
type Model struct {
	history  *History
	viewport *Viewport
	streams  map[string]*streamSlot // key = correlationID; nil slots cleaned on complete.
	theme    *theme.Theme
	width    int
	height   int
	focused  bool

	// Transient highlight for copy feedback.
	highlightID    string
	highlightUntil time.Time

	// Thinking animation state (active between prompt submit and first content).
	thinkingIdx      int             // History index of thinking placeholder (-1 = inactive).
	thinkingFrame    int             // Current spinner frame index.
	thinkingMsgIdx   int             // Current fun message index.
	thinkingAgentID  string          // Agent currently thinking (for agent-specific messages).
	thinkingStart    time.Time       // When current thinking phase began.
	thinkingRotateAt time.Time       // Next message rotation time.
	retryText        string          // Retry/model-fallback status (replaces fun messages when set).
	thinkingGradient *theme.Gradient // Color gradient cycled during thinking animation.
	lastProgressSet  time.Time       // Last immediate progress text write time.

	// Inline plan tracking: the plan renders as a chat entry updated in place.
	planEntryIdx int    // History index of the plan ChatEntry (-1 = no plan entry).
	planID       string // Correlates updates to the correct entry.

	// Render throttle: chunks buffer at full speed, but the history entry
	// is only synced (and viewDirty set) on DecorTick or StreamComplete.
	streamRenderPending bool

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
		history:          h,
		viewport:         vp,
		streams:          make(map[string]*streamSlot),
		theme:            th,
		thinkingIdx:      -1,
		planEntryIdx:     -1,
		thinkingGradient: th.Palette.ThinkingGradient(),
		steeringGradient: th.Palette.GroupGradient(),
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
	"llm_response":       true, // Duplicates streaming/FinishThinking response.
	"llm_request":        true, // Internal LLM call, not user-facing.
	"user_prompt":        true, // Duplicates user input entry.
	"user_clarification": true, // Duplicates user input entry.
	"agent_action":       true, // Internal agent operation.
	"agent_decision":     true, // Internal routing decision.
	"tool_call":          true, // Internal tool invocation.
	"tool_result":        true, // Internal tool output.
	"index_start":        true, // Internal indexing lifecycle.
	"index_complete":     true,
	"index_file_added":   true,
	"index_file_removed": true,
	"context_eviction":      true, // Internal context management.
	"context_restore":       true,
	"success":               true, // Generic outcome, visible from response.
	"steering_checkpoint":   true, // Internal checkpoint — visible in agent panel only.
	"steering_inject":       true, // Steering inject shown via dedicated chat entry.
	"steering_edit":         true, // Steering edit shown via dedicated chat entry.
	"steering_rollback":     true, // Steering rollback shown via dedicated chat entry.
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
	if chatSuppressedEvents[ev.Event.EventType.String()] {
		return nil
	}
	entry := &ChatEntry{
		ID:         ev.Event.ID,
		Timestamp:  ev.Event.Timestamp,
		Source:     activitySource(ev),
		AgentType:  agentTypeFromData(ev),
		AgentID:    ev.Event.AgentID,
		SessionID:  ev.Event.SessionID,
		Content:    ev.Event.Content,
		Height:     -1,
		Importance: ev.Event.Importance,
	}
	m.PushEntry(entry)
	return nil
}

// handleStreamStart begins accumulation. If a thinking placeholder exists,
// it is reused; otherwise a new entry is pushed. Multiple concurrent streams
// are tracked in the streams map keyed by correlationID.
func (m *Model) handleStreamStart(start msg.StreamStartMsg) tea.Cmd {
	cid := start.CorrelationID
	if m.streams == nil {
		m.streams = make(map[string]*streamSlot)
	}
	chatDebugLog().Info("chat.handleStreamStart: ENTRY",
		"correlation_id", cid,
		"agent_id", start.AgentID,
		"active_streams", len(m.streams),
		"thinking_idx", m.thinkingIdx)

	// Retry path: provider retried an existing stream. Reset the slot's
	// accumulator and render state instead of creating a new entry.
	if slot, ok := m.streams[cid]; ok && slot.accumulator != nil {
		slot.accumulator.Replace("")
		slot.planOffset = 0
		slot.renderState = &streamRenderState{}
		m.streamRenderPending = false
		m.syncSlotToEntry(slot)
		m.viewport.AddStreamState(slot.accumulator.EntryIndex(), slot.renderState)
		m.viewDirty = true
		return nil
	}

	// Reuse existing thinking placeholder for the first stream only.
	if m.thinkingIdx >= 0 && len(m.streams) == 0 {
		idx := m.thinkingIdx
		if start.AgentID != "" {
			m.thinkingAgentID = start.AgentID
		}
		m.history.mu.Lock()
		if idx >= 0 && idx < m.history.count {
			physical := m.history.logicalToPhysical(idx)
			if start.AgentID != "" {
				m.history.entries[physical].AgentType = start.AgentID
			}
			m.history.entries[physical].SessionID = start.SessionID
		}
		m.history.mu.Unlock()
		slot := &streamSlot{
			accumulator: NewStreamAccumulator(idx),
			agentID:     start.AgentID,
			thinkingIdx: idx,
			renderState: &streamRenderState{},
		}
		m.streams[cid] = slot
		m.viewDirty = true
		return nil
	}

	// New concurrent stream or no thinking placeholder — create a new entry.
	now := time.Now()
	entry := &ChatEntry{
		ID:             uuid.New().String(),
		Timestamp:      now,
		Source:         SourceAgent,
		AgentType:      start.AgentID,
		SessionID:      start.SessionID,
		Content:        "",
		Height:         -1,
		Streaming:      true,
		ThinkingText:   spinnerFrames[0] + "  0.0s",
		ThinkingStatus: thinkingMessages[0],
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
		agentID:     start.AgentID,
		thinkingIdx: idx,
		renderState: &streamRenderState{},
	}
	m.streams[cid] = slot
	// Only start the global thinking animation for the first stream.
	if len(m.streams) == 1 {
		m.startThinkingAnimation(now, idx)
	}
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
	message := sanitizeThinkingMessage(progress.Message)
	if message == "" {
		return nil
	}
	m.updateThinkingAgent(progress.AgentID)
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

	// Remove this slot's stream state from the viewport.
	m.viewport.RemoveStreamState(slot.accumulator.EntryIndex())

	// Resolve thinking for this slot's entry if it holds the global thinking index.
	if m.thinkingIdx >= 0 && slot.thinkingIdx == m.thinkingIdx {
		chatDebugLog().Info("chat.handleStreamComplete: RESOLVING_THINKING",
			"correlation_id", cid,
			"thinking_idx", m.thinkingIdx)
		m.resolveThinkingEntry()
	}

	if done.AuthoritativeText != "" {
		slot.accumulator.Replace(done.AuthoritativeText)
		slot.planOffset = 0
	}
	slot.accumulator.Complete()
	m.finalizeSlotStream(slot)
	delete(m.streams, cid)

	// When all streams are done, clear remaining render state.
	if len(m.streams) == 0 {
		m.streamRenderPending = false
	}

	// Acknowledge any pending steering entries for this correlation.
	if len(m.steeringPending) > 0 && cid != "" {
		m.acknowledgeSteering(cid)
	}

	chatDebugLog().Info("chat.handleStreamComplete: DONE",
		"correlation_id", cid,
		"remaining_streams", len(m.streams))
	return nil
}

// handleStreamError adds an error entry and cleans up the accumulator.
func (m *Model) handleStreamError(errMsg msg.StreamErrorMsg) tea.Cmd {
	m.streamRenderPending = false
	// Clean up the specific slot if it exists.
	if slot, ok := m.streams[errMsg.CorrelationID]; ok && slot.accumulator != nil {
		m.viewport.RemoveStreamState(slot.accumulator.EntryIndex())
		slot.accumulator.Complete()
		m.finalizeSlotStream(slot)
		delete(m.streams, errMsg.CorrelationID)
	}
	// If all streams are gone, clear global viewport stream state.
	if len(m.streams) == 0 {
		m.viewport.ClearAllStreamStates()
	}
	if m.thinkingIdx >= 0 && len(m.streams) == 0 {
		m.clearThinkingState()
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
	if m.thinkingIdx >= 0 {
		delayStr := formatRetryDelay(retry.Delay)
		m.retryText = sanitizeThinkingMessage(fmt.Sprintf("retrying (%d/%d) in %s...", retry.Attempt, retry.MaxAttempts, delayStr))
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
// updating the active streaming entry's ToolCalls list.
func (m *Model) handleToolCallEvent(ev msg.ToolCallEventMsg) tea.Cmd {
	idx := m.activeStreamingIndex()
	if idx < 0 {
		return nil
	}

	m.history.UpdateAt(idx, func(e *ChatEntry) {
		switch ev.Phase {
		case 0: // ToolCallStart
			e.ToolCalls = append(e.ToolCalls, ToolCallRecord{
				ToolName:    ev.ToolName,
				ArgsSummary: ev.ArgsSummary,
				FullArgs:    ev.FullArgs,
				StartedAt:   ev.StartedAt,
			})
		case 1: // ToolCallComplete
			// Find the last incomplete record with the same tool name.
			for i := len(e.ToolCalls) - 1; i >= 0; i-- {
				if e.ToolCalls[i].ToolName == ev.ToolName && !e.ToolCalls[i].Completed {
					e.ToolCalls[i].Duration = ev.Duration
					e.ToolCalls[i].Success = ev.Success
					e.ToolCalls[i].Completed = true
					e.ToolCalls[i].Output = ev.Output
					e.ToolCalls[i].ErrorMsg = ev.ErrorMsg
					if !ev.Success {
						e.ToolCalls[i].Expanded = true // Auto-expand failures.
					}
					break
				}
			}
		}
		// Invalidate render cache.
		e.RenderedLines = nil
		e.CodeRegions = nil
		e.ToolCallRegions = nil
		e.Height = -1
	})
	return nil
}

// activeStreamingIndex returns the history index of the entry currently receiving
// streaming content. Checks active stream slots first, then the thinking placeholder.
func (m *Model) activeStreamingIndex() int {
	for _, slot := range m.streams {
		if slot.accumulator != nil {
			return slot.accumulator.EntryIndex()
		}
	}
	if m.thinkingIdx >= 0 {
		return m.thinkingIdx
	}
	return -1
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
		for _, slot := range m.streams {
			if slot.accumulator != nil && slot.planMarkdown == "" {
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

// Clear discards all chat entries and resets the viewport to its initial state.
// Active streams and thinking indicators are cancelled.
func (m *Model) Clear() {
	m.history.Clear()
	m.clearThinkingState()
	m.clearSteeringState()
	clear(m.streams)
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
	idx := m.activeStreamingIndex()
	if idx < 0 {
		return
	}
	hasActive := false
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		for i := range e.ToolCalls {
			if !e.ToolCalls[i].Completed {
				hasActive = true
				break
			}
		}
		if hasActive {
			e.RenderedLines = nil
			e.CodeRegions = nil
			e.ToolCallRegions = nil
			e.Height = -1
		}
	})
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
	if m.thinkingIdx < 0 || m.thinkingStart.IsZero() {
		return
	}

	// Advance spinner frame.
	m.thinkingFrame = (m.thinkingFrame + 1) % len(spinnerFrames)

	// Rotate message every thinkingRotateInterval.
	msgs := thinkingMessagesForAgent(m.thinkingAgentID)
	if !now.Before(m.thinkingRotateAt) {
		m.thinkingMsgIdx = (m.thinkingMsgIdx + 1) % len(msgs)
		m.thinkingRotateAt = now.Add(thinkingRotateInterval)
	}

	elapsed := now.Sub(m.thinkingStart).Seconds()
	var status string
	if m.retryText != "" {
		status = m.retryText
	} else {
		status = msgs[m.thinkingMsgIdx%len(msgs)]
	}
	text := fmt.Sprintf("%s  %.1fs",
		spinnerFrames[m.thinkingFrame],
		elapsed,
	)

	// Sample gradient color for this tick.
	gradientColor := string(m.thinkingGradient.Sample(now.Sub(m.thinkingStart)))

	idx := m.thinkingIdx
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		m.history.entries[physical].ThinkingText = text
		m.history.entries[physical].ThinkingStatus = status
		m.history.entries[physical].ThinkingColor = gradientColor
		m.history.entries[physical].RenderedLines = nil
		m.history.entries[physical].CodeRegions = nil
		m.history.entries[physical].Height = -1
	}
	m.history.mu.Unlock()
	m.viewDirty = true
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
		ThinkingStatus: thinkingMessages[0],
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
		e.Content = entry.Content
		e.Source = entry.Source
		e.AgentType = entry.AgentType
		e.AgentID = entry.AgentID
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
	m.thinkingFrame = 0
	m.thinkingMsgIdx = 0
	m.thinkingRotateAt = now.Add(thinkingRotateInterval)
	m.lastProgressSet = time.Time{}
}

// resolveThinkingEntry transitions the thinking placeholder to content phase,
// recording the elapsed thinking time.
func (m *Model) resolveThinkingEntry() {
	elapsed := time.Since(m.thinkingStart)
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

func (m *Model) updateThinkingAgent(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || m.thinkingIdx < 0 {
		return
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
	elapsed := 0.0
	if !m.thinkingStart.IsZero() {
		elapsed = time.Since(m.thinkingStart).Seconds()
	}
	text := fmt.Sprintf("%s  %.1fs", spinnerFrames[m.thinkingFrame], elapsed)
	color := string(m.theme.Palette.Info)
	if m.thinkingGradient != nil {
		color = string(m.thinkingGradient.Sample(time.Since(m.thinkingStart)))
	}
	idx := m.thinkingIdx
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		m.history.entries[physical].ThinkingText = text
		m.history.entries[physical].ThinkingStatus = message
		m.history.entries[physical].ThinkingColor = color
		m.history.entries[physical].RenderedLines = nil
		m.history.entries[physical].CodeRegions = nil
		m.history.entries[physical].Height = -1
	}
	m.history.mu.Unlock()
	m.viewDirty = true
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
	if hadPlan && m.planID != "" {
		m.history.entries[physical].ID = "plan-" + m.planID
		m.planEntryIdx = idx
	}
	m.history.mu.Unlock()
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
