package msg

import (
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/component"
)

// ---------------------------------------------------------------------------
// Activity & session events (from core system bridges)
// ---------------------------------------------------------------------------

// ActivityEventMsg wraps a core activity event for the TUI.
type ActivityEventMsg struct {
	Event *events.ActivityEvent
}

// SessionEventMsg wraps a session lifecycle event.
type SessionEventMsg struct {
	Event *session.Event
}

// ---------------------------------------------------------------------------
// LLM streaming
// ---------------------------------------------------------------------------

// StreamStartMsg signals the start of an LLM response stream.
type StreamStartMsg struct {
	SessionID     string
	CorrelationID string
}

// StreamChunkMsg carries a streaming text chunk from an LLM response.
type StreamChunkMsg struct {
	SessionID     string
	CorrelationID string
	Text          string
}

// StreamProgressMsg reports progress during a streaming operation.
type StreamProgressMsg struct {
	SessionID     string
	CorrelationID string
	Current       int
	Total         int
	Message       string
}

// StreamCompleteMsg signals the end of an LLM response stream.
type StreamCompleteMsg struct {
	SessionID     string
	CorrelationID string
	Result        any
}

// StreamErrorMsg signals an error during streaming.
type StreamErrorMsg struct {
	SessionID     string
	CorrelationID string
	Err           error
}

// ---------------------------------------------------------------------------
// Guide responses
// ---------------------------------------------------------------------------

// GuideResponseMsg wraps a complete response from the Guide router.
type GuideResponseMsg struct {
	CorrelationID string
	AgentID       string
	AgentName     string
	Content       string
	Err           error
}

// ---------------------------------------------------------------------------
// User actions
// ---------------------------------------------------------------------------

// SubmitPromptMsg is sent when the user submits a prompt.
type SubmitPromptMsg struct {
	Text      string
	TargetAgent string // Empty for auto-routing, agent name for @agent syntax
	SessionID string
}

// ---------------------------------------------------------------------------
// UI navigation & control
// ---------------------------------------------------------------------------

// FocusPanelMsg requests focus change to a specific panel.
type FocusPanelMsg struct {
	Target component.FocusID
}

// InterruptMsg signals a user interrupt (first Ctrl+C).
type InterruptMsg struct{}

// QuitConfirmMsg signals a confirmed quit (second Ctrl+C within threshold).
type QuitConfirmMsg struct{}

// ---------------------------------------------------------------------------
// Periodic ticks
// ---------------------------------------------------------------------------

// TickMsg is sent periodically for cursor blink, spinner animation, etc.
type TickMsg struct {
	Time time.Time
}

// ---------------------------------------------------------------------------
// Bridge health
// ---------------------------------------------------------------------------

// EventsDroppedMsg reports that the bridge dropped events due to backpressure.
type EventsDroppedMsg struct {
	BridgeName string
	Count      int64
}

// ---------------------------------------------------------------------------
// Editor
// ---------------------------------------------------------------------------

// OpenEditorMsg requests opening the editor overlay.
type OpenEditorMsg struct {
	FilePath string // Empty for new buffer
	Content  string // Pre-filled content (e.g., from chat code block)
}

// CloseEditorMsg signals the editor overlay should close.
type CloseEditorMsg struct{}

// ---------------------------------------------------------------------------
// File tree
// ---------------------------------------------------------------------------

// FileOpenMsg requests displaying a file in the code viewer.
type FileOpenMsg struct {
	Path     string
	Name     string
	Language string
	Line     int // 0 = top, >0 = scroll to this 1-based line.
}
