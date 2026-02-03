package chat

import (
	"time"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

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

// SetFocused sets the focus state.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
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
	m.history.Push(entry)
	m.viewport.OnNewEntry()
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
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if !m.focused {
		return nil
	}
	switch key.String() {
	case "up", "k":
		m.viewport.ScrollUp()
	case "down", "j":
		m.viewport.ScrollDown()
	case "pgup":
		m.viewport.PageUp()
	case "pgdown":
		m.viewport.PageDown()
	case "home":
		m.viewport.ToTop()
	case "end":
		m.viewport.ToBottom()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// PushEntry appends an entry and notifies the viewport.
func (m *Model) PushEntry(entry *ChatEntry) {
	m.history.Push(entry)
	m.viewport.OnNewEntry()
}

// ScrollUp scrolls the chat viewport up by one entry.
func (m *Model) ScrollUp() {
	m.viewport.ScrollUp()
}

// ScrollDown scrolls the chat viewport down by one entry.
func (m *Model) ScrollDown() {
	m.viewport.ScrollDown()
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
