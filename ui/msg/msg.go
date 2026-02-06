package msg

import (
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/lsp"
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
// LSP
// ---------------------------------------------------------------------------

// LSPDiagnosticMsg carries diagnostics from a language server for a file.
type LSPDiagnosticMsg struct {
	ServerID    string
	FilePath    string
	Diagnostics []lsp.Diagnostic
}

// LSPStatusMsg reports a change in LSP client status.
type LSPStatusMsg struct {
	ServerID    string
	ProjectRoot string
	Status      string // "starting", "ready", "error", "stopped"
}

// LSPProvisionDoneMsg signals that background LSP provisioning completed.
type LSPProvisionDoneMsg struct {
	Err error
}

// LSPServerMissingMsg is sent when a file is opened but the matching language
// server binary is not installed. Carries enough context to trigger on-demand
// installation and re-open the document afterwards.
type LSPServerMissingMsg struct {
	ServerID   string
	ServerName string
	FilePath   string
	LanguageID string
	Content    string
}

// LSPServerInstalledMsg reports the outcome of an on-demand server install.
type LSPServerInstalledMsg struct {
	ServerID   string
	ServerName string
	FilePath   string
	LanguageID string
	Content    string
	Err        error
}

// LSPHoverRequestMsg is emitted by the editor when the user presses K
// in normal mode, requesting hover information from the language server.
type LSPHoverRequestMsg struct {
	FilePath string
	Line     int // 0-indexed
	Col      int // 0-indexed (rune offset)
}

// LSPMouseHoverTickMsg is sent after a debounce delay when the mouse
// hovers over a new position in the editor. If the mouse hasn't moved
// since the tick was scheduled, a hover request is fired.
type LSPMouseHoverTickMsg struct {
	Line int
	Col  int
}

// LSPDefinitionRequestMsg is emitted by the editor when the user triggers
// go-to-definition (gd), requesting definition locations from the server.
type LSPDefinitionRequestMsg struct {
	FilePath string
	Line     int
	Col      int
	ForHover bool // true when accompanying a hover tooltip
}

// LSPDocHighlightTickMsg is sent after a debounce delay when the cursor
// rests on a symbol in normal mode, triggering a documentHighlight request.
type LSPDocHighlightTickMsg struct {
	Line int
	Col  int
}

// LSPCompletionRequestMsg is emitted by the editor in insert mode to
// request completion items from the language server.
type LSPCompletionRequestMsg struct {
	FilePath string
	Line     int
	Col      int
}

// LSPDocumentHighlightMsg carries document highlights from a language server.
type LSPDocumentHighlightMsg struct {
	FilePath   string
	Line       int // anchor line for cache invalidation
	Col        int
	Highlights []lsp.DocumentHighlight
	Err        error
}

// LSPCompletionMsg carries completion items from a language server.
type LSPCompletionMsg struct {
	FilePath string
	Items    []lsp.CompletionItem
	Err      error
}

// LSPHoverMsg carries hover information from a language server.
type LSPHoverMsg struct {
	FilePath string
	Line     int // 0-indexed anchor line for positioning the tooltip
	Col      int // 0-indexed anchor column
	Result   *lsp.HoverResult
	Err      error
}

// LSPDefinitionMsg carries definition locations from a language server.
type LSPDefinitionMsg struct {
	FilePath  string
	Locations []lsp.Location
	Err       error
	ForHover  bool // true when accompanying a hover tooltip (don't navigate)
}

// ---------------------------------------------------------------------------
// File tree
// ---------------------------------------------------------------------------

// NerdFontsResultMsg reports whether Nerd Font symbols were installed.
type NerdFontsResultMsg struct {
	Available bool
}

// FileOpenMsg requests displaying a file in the code viewer.
type FileOpenMsg struct {
	Path     string
	Name     string
	Language string
	Line     int // 0 = top, >0 = scroll to this 1-based line.
}

// FileTreeNewFileMsg requests creation of a new file in the given directory.
type FileTreeNewFileMsg struct {
	Dir string // Parent directory for the new file.
}

// FileTreeNewDirMsg requests creation of a new directory in the given directory.
type FileTreeNewDirMsg struct {
	Dir string // Parent directory for the new directory.
}

// FileTreeRenameMsg requests renaming the entry at Path.
type FileTreeRenameMsg struct {
	Path  string
	IsDir bool
}

// FileTreeDeleteMsg requests deletion of the entry at Path.
type FileTreeDeleteMsg struct {
	Path  string
	IsDir bool
}

// FileTreeEntryCreatedMsg is emitted after a new file or directory is
// successfully created via the inline new-entry input.
type FileTreeEntryCreatedMsg struct {
	Path  string
	IsDir bool
}

// FileTreeEntryDeletedMsg is emitted after a file or directory is
// successfully deleted via the inline delete confirmation.
type FileTreeEntryDeletedMsg struct {
	Path  string
	IsDir bool
}

// FileTreeEntryRenamedMsg is emitted after a file or directory is
// successfully renamed via the inline rename input.
type FileTreeEntryRenamedMsg struct {
	OldPath string
	NewPath string
	IsDir   bool
}
