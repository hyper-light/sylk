package chat

import (
	"time"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

// highlightDurationTicks is how many ticks a copied-entry highlight persists.
// Derived from: 2 seconds at 16ms per tick ≈ 125 ticks.
const highlightDurationTicks = 125

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
	highlightTicks int
}

// Verify interface compliance at compile time.
var (
	_ component.Focusable  = (*Model)(nil)
	_ component.Resizable  = (*Model)(nil)
	_ component.Component  = (*Model)(nil)
)

// New creates a chat Model with the given theme and history capacity.
func New(th *theme.Theme, historyCapacity int) *Model {
	h := NewHistory(historyCapacity)
	vp := NewViewport(h, th)
	return &Model{
		history:  h,
		viewport: vp,
		theme:    th,
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
	case msg.TickMsg:
		m.tickHighlight()
		m.viewport.TickEdgeFlash()
		return m, nil
	case msg.ActivityEventMsg:
		return m, m.handleActivity(typed)
	case msg.StreamStartMsg:
		return m, m.handleStreamStart(typed)
	case msg.StreamChunkMsg:
		return m, m.handleStreamChunk(typed)
	case msg.StreamCompleteMsg:
		return m, m.handleStreamComplete(typed)
	case msg.StreamErrorMsg:
		return m, m.handleStreamError(typed)
	case tea.KeyMsg:
		return m, m.handleKey(typed)
	default:
		return m, nil
	}
}

// View renders the chat viewport.
func (m *Model) View() string {
	return m.viewport.View()
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
	m.viewport.SetSize(m.width, m.height)
}

// ---------------------------------------------------------------------------
// Message handlers
// ---------------------------------------------------------------------------

// handleActivity converts an ActivityEventMsg into a system chat entry.
func (m *Model) handleActivity(ev msg.ActivityEventMsg) tea.Cmd {
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

// handleStreamStart creates a placeholder entry and begins accumulation.
func (m *Model) handleStreamStart(start msg.StreamStartMsg) tea.Cmd {
	entry := &ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    SourceAgent,
		SessionID: start.SessionID,
		Content:   "",
		Height:    -1,
		Streaming: true,
	}
	willEvict := m.history.Full()
	m.history.Push(entry)
	m.viewport.OnNewEntry()
	if willEvict {
		m.viewport.AdjustSelectionForEviction()
	}
	idx := m.history.Len() - 1
	m.accumulator = NewStreamAccumulator(idx)
	return nil
}

// handleStreamChunk appends text to the accumulator and updates the entry.
func (m *Model) handleStreamChunk(chunk msg.StreamChunkMsg) tea.Cmd {
	if m.accumulator == nil {
		return nil
	}
	m.accumulator.Append(chunk.Text)
	m.syncAccumulatorToEntry()
	return nil
}

// handleStreamComplete finalizes the streaming entry.
func (m *Model) handleStreamComplete(_ msg.StreamCompleteMsg) tea.Cmd {
	if m.accumulator == nil {
		return nil
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
}

// ScrollUp scrolls the chat viewport up by one line.
// Returns true if the scroll was applied, false if at boundary.
func (m *Model) ScrollUp() bool {
	return m.viewport.ScrollUp()
}

// ScrollDown scrolls the chat viewport down by one line.
// Returns true if the scroll was applied, false if at boundary.
func (m *Model) ScrollDown() bool {
	return m.viewport.ScrollDown()
}

// SetBounceOffset updates the visual bounce displacement for rendering.
func (m *Model) SetBounceOffset(offset int) {
	m.viewport.SetBounceOffset(offset)
}

// IsStreaming reports whether a response is currently being streamed.
func (m *Model) IsStreaming() bool { return m.accumulator != nil }

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
	m.highlightTicks = highlightDurationTicks
	if m.focused {
		m.viewport.SelectRegionContaining(entryIndex, start)
	}
	m.viewport.SetHighlight(entryID, start, end)
}

// tickHighlight decrements the highlight countdown and clears when expired.
// The selection is only cleared alongside the highlight when the chat panel
// is not focused; otherwise the selection persists for continued navigation.
func (m *Model) tickHighlight() {
	if m.highlightTicks <= 0 {
		return
	}
	m.highlightTicks--
	if m.highlightTicks == 0 {
		m.highlightID = ""
		m.viewport.SetHighlight("", 0, 0)
		if !m.focused {
			m.viewport.ClearSelection()
		}
	}
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
