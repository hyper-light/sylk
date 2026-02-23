package chat

import (
	"fmt"
	"strings"
	"time"

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

// thinkingMessages are rotating messages shown while waiting for the first chunk.
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

// Model is the Bubble Tea model for the chat panel.
// It displays a scrollable history of chat entries with virtual scrolling,
// supports LLM streaming, and handles keyboard navigation.
type Model struct {
	history     *History
	viewport    *Viewport
	accumulator *StreamAccumulator // nil when not streaming.
	theme       *theme.Theme
	width       int
	height      int
	focused     bool

	// Transient highlight for copy feedback.
	highlightID    string
	highlightUntil time.Time

	// Thinking animation state (active between prompt submit and first content).
	thinkingIdx      int             // History index of thinking placeholder (-1 = inactive).
	thinkingFrame    int             // Current spinner frame index.
	thinkingMsgIdx   int             // Current fun message index.
	thinkingStart    time.Time       // When current thinking phase began.
	thinkingRotateAt time.Time       // Next message rotation time.
	retryText        string          // Retry/model-fallback status (replaces fun messages when set).
	thinkingGradient *theme.Gradient // Color gradient cycled during thinking animation.
	lastProgressSet  time.Time       // Last immediate progress text write time.

	// Inline plan tracking: the plan renders as a chat entry updated in place.
	planEntryIdx int    // History index of the plan ChatEntry (-1 = no plan entry).
	planID       string // Correlates updates to the correct entry.

	// View cache: avoids re-rendering when no visible state changed.
	viewCache string
	viewDirty bool
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
		theme:            th,
		thinkingIdx:      -1,
		planEntryIdx:     -1,
		thinkingGradient: th.Palette.ThinkingGradient(),
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
		m.viewDirty = true
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
	"context_eviction":   true, // Internal context management.
	"context_restore":    true,
	"success":            true, // Generic outcome, visible from response.
}

// handleActivity converts an ActivityEventMsg into a chat entry.
// Events already represented by dedicated chat paths are suppressed.
func (m *Model) handleActivity(ev msg.ActivityEventMsg) tea.Cmd {
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
// it is reused; otherwise a new entry is pushed.
func (m *Model) handleStreamStart(start msg.StreamStartMsg) tea.Cmd {
	// Reuse existing thinking placeholder if present.
	if m.thinkingIdx >= 0 {
		idx := m.thinkingIdx
		m.history.mu.Lock()
		if idx >= 0 && idx < m.history.count {
			physical := m.history.logicalToPhysical(idx)
			if start.AgentID != "" {
				m.history.entries[physical].AgentType = start.AgentID
			}
			m.history.entries[physical].SessionID = start.SessionID
		}
		m.history.mu.Unlock()
		m.accumulator = NewStreamAccumulator(idx)
		m.viewDirty = true
		return nil
	}

	// No thinking placeholder — create a new entry (streaming without prior submit).
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
	}
	idx := m.history.Len() - 1
	m.accumulator = NewStreamAccumulator(idx)
	m.startThinkingAnimation(now, idx)
	return nil
}

// handleStreamChunk appends text to the accumulator and updates the entry.
func (m *Model) handleStreamChunk(chunk msg.StreamChunkMsg) tea.Cmd {
	if m.accumulator == nil {
		return nil
	}

	// On the first chunk, transition from thinking to content phase.
	if m.thinkingIdx >= 0 && m.accumulator.Content() == "" {
		m.resolveThinkingEntry()
	}

	m.accumulator.Append(chunk.Text)
	m.syncAccumulatorToEntry()
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

// handleStreamComplete finalizes the streaming entry.
func (m *Model) handleStreamComplete(done msg.StreamCompleteMsg) tea.Cmd {
	if m.accumulator == nil {
		return nil
	}
	if m.thinkingIdx >= 0 {
		m.resolveThinkingEntry()
	}
	if done.AuthoritativeText != "" {
		m.accumulator.Replace(done.AuthoritativeText)
	}
	m.accumulator.Complete()
	m.finalizeStream()
	m.accumulator = nil
	return nil
}

// handleStreamError adds an error entry and cleans up the accumulator.
func (m *Model) handleStreamError(errMsg msg.StreamErrorMsg) tea.Cmd {
	// Finalize any partial stream.
	if m.accumulator != nil {
		m.accumulator.Complete()
		m.finalizeStream()
		m.accumulator = nil
	}
	if m.thinkingIdx >= 0 {
		m.clearThinkingState()
	}

	errEntry := &ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    SourceError,
		SessionID: errMsg.SessionID,
		Content:   errMsg.Err.Error(),
		Height:    -1,
	}
	m.PushEntry(errEntry)
	return nil
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
func formatRetryDelay(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d < time.Second {
		return "<1s"
	}
	return d.String()
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
// in place on subsequent snapshots. The first update for a given PlanID pushes
// a new entry; later updates rewrite the same entry's content.
func (m *Model) HandlePlanUpdate(update msg.PlanUpdateMsg) {
	content := formatPlanMarkdown(update)

	// New plan or different plan ID — push a fresh entry.
	if m.planEntryIdx < 0 || m.planID != update.PlanID {
		entry := &ChatEntry{
			ID:        "plan-" + update.PlanID,
			Timestamp: update.StartTime,
			Source:    SourceAgent,
			AgentType: "architect",
			Content:   content,
			Height:    -1,
		}
		m.PushEntry(entry)
		m.planEntryIdx = m.history.Len() - 1
		m.planID = update.PlanID
		return
	}

	// Same plan — update existing entry in place.
	idx := m.planEntryIdx
	m.history.mu.Lock()
	if idx >= 0 && idx < m.history.count {
		physical := m.history.logicalToPhysical(idx)
		m.history.entries[physical].Content = content
		m.history.entries[physical].RenderedLines = nil
		m.history.entries[physical].CodeRegions = nil
		m.history.entries[physical].Height = -1
	}
	m.history.mu.Unlock()
	m.viewDirty = true
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
	}
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

// IsStreaming reports whether a response is currently being streamed.
func (m *Model) IsStreaming() bool { return m.accumulator != nil }

// HasActiveAnimation reports whether any tick-driven animation is running
// (thinking spinner, highlight countdown, edge flash, or streaming).
// Uses !IsZero rather than Before for the highlight check so the decor tick
// chain keeps running until tickHighlight actually clears the viewport state.
func (m *Model) HasActiveAnimation() bool {
	now := time.Now()
	return m.thinkingIdx >= 0 ||
		m.accumulator != nil ||
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
	if !now.Before(m.thinkingRotateAt) {
		m.thinkingMsgIdx = (m.thinkingMsgIdx + 1) % len(thinkingMessages)
		m.thinkingRotateAt = now.Add(thinkingRotateInterval)
	}

	elapsed := now.Sub(m.thinkingStart).Seconds()
	var status string
	if m.retryText != "" {
		status = m.retryText
	} else {
		status = thinkingMessages[m.thinkingMsgIdx]
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
	}
	m.history.mu.Unlock()
	m.clearThinkingState()
}

// clearThinkingState resets all thinking animation fields.
func (m *Model) clearThinkingState() {
	m.thinkingIdx = -1
	m.thinkingStart = time.Time{}
	m.retryText = ""
	m.lastProgressSet = time.Time{}
}

func (m *Model) updateThinkingAgent(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || m.thinkingIdx < 0 {
		return
	}
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

// syncAccumulatorToEntry writes the accumulated content back into the
// History entry and invalidates its render cache.
func (m *Model) syncAccumulatorToEntry() {
	idx := m.accumulator.EntryIndex()
	content := m.accumulator.Content()

	m.history.mu.Lock()
	defer m.history.mu.Unlock()

	if idx < 0 || idx >= m.history.count {
		return
	}
	physical := m.history.logicalToPhysical(idx)
	m.history.entries[physical].Content = content
	m.history.entries[physical].RenderedLines = nil
	m.history.entries[physical].CodeRegions = nil
	m.history.entries[physical].Height = -1
}

// finalizeStream marks the streaming entry as complete.
func (m *Model) finalizeStream() {
	idx := m.accumulator.EntryIndex()
	content := m.accumulator.Content()

	m.history.mu.Lock()
	defer m.history.mu.Unlock()

	if idx < 0 || idx >= m.history.count {
		return
	}
	physical := m.history.logicalToPhysical(idx)
	m.history.entries[physical].Content = content
	m.history.entries[physical].Streaming = false
	m.history.entries[physical].RenderedLines = nil
	m.history.entries[physical].CodeRegions = nil
	m.history.entries[physical].Height = -1
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
