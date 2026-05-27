package msg

import (
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/core/search/git"
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

// SessionSwitchedMsg is the narrowed UI-13 signal emitted whenever
// the session manager switches the active session (EventSwitched).
// AppModel subscribes to this instead of filtering every
// SessionEventMsg because switch is the only event that invalidates
// the in-memory editor / chat buffers — other lifecycle transitions
// (paused, resumed, completed, failed) leave the surface intact.
//
// The PreviousID is empty on the very first activation. ActiveID is
// always populated — the msg is never sent for a switch that
// couldn't activate a target.
type SessionSwitchedMsg struct {
	PreviousID string
	ActiveID   string
	Event      *session.Event
}

// ---------------------------------------------------------------------------
// Token usage (accountant-owned)
// ---------------------------------------------------------------------------

// TokenDeltaMsg is the typed-identity token-usage delta published by
// AccountantBridge. It flows directly from accounting.Accountant.Subscribe()
// and carries the full typed *AgentIdentity + *TaskRef pair used as
// the aggregator's primary key. UI consumers that want per-replica,
// per-generation, or per-task breakdown key on Identity/Task
// directly; AgentID-string fallbacks are derivable via Identity.Panel().
type TokenDeltaMsg struct {
	Timestamp        time.Time
	Identity         *identity.AgentIdentity
	Task             *identity.TaskRef
	Model            string
	Phase            string // "request", "response", "error"
	Billable         bool
	InputTokens      int
	OutputTokens     int
	ReasoningTokens  int
	CacheReadTokens  int
	CacheWriteTokens int
	ElapsedMS        int64
}

// AccountingSnapshotMsg is broadcast by AccountantBridge periodically
// (or on first connect) so the status bar can render a total-tokens
// view without maintaining its own running counter. The status bar
// consumer reads TotalInput+TotalOutput.
type AccountingSnapshotMsg struct {
	Timestamp      time.Time
	TotalInput     int64
	TotalOutput    int64
	TotalReasoning int64
	TotalRequests  int64
	TotalErrors    int64
}

// ---------------------------------------------------------------------------
// LLM streaming
// ---------------------------------------------------------------------------

// StreamStartMsg signals the start of an LLM response stream.
type StreamStartMsg struct {
	SessionID           string
	CorrelationID       string
	ParentCorrelationID string
	TopLevelTransfer    bool
	// ContinuationOfCorrelationID, when non-empty, marks this start as the
	// resumption of an earlier turn on the originator agent (e.g. the
	// inspector receiving a validate_work response to its own challenge).
	// The TUI routes the new correlation into the existing chat entry for
	// this CID instead of opening a new top-level entry.
	ContinuationOfCorrelationID string
	AgentID                     string // Raw responding agent ID; UI layers canonicalize separately when needed.
	RuntimeAgentID              string
	AgentType                   string
	AgentName                   string
	PipelineID                  string
	TaskID                      string
	TaskName                    string
	TaskSlug                    string
	Visibility                  events.EventVisibility
	BranchRef                   *InterAgentBranchRefMsg
	// ControlPlane is true when the stream was synthesized from a route
	// bearing a non-empty `control_plane_kind` metadata — i.e., internal
	// coordination (plan-handoff receipt lookup, command-approval-hold
	// begin/resolve, protocol reconciliation) that should NOT materialize
	// as a user-visible agent entry. The chat model's handleStreamStart
	// early-returns when this is true; the paired system_coordination
	// activity event (published by the sender via
	// guide.EmitControlPlaneCoordination) carries the user-visible
	// semantic meaning as a SourceSystem chat row instead.
	ControlPlane bool
}

// StreamChunkMsg carries a streaming text chunk from an LLM response.
type StreamChunkMsg struct {
	SessionID     string
	CorrelationID string
	Text          string
	InputTokens   int // Real provider input tokens (set once on first chunk, 0 = not yet known).
}

// StreamProgressMsg reports progress during a streaming operation.
type StreamProgressMsg struct {
	SessionID           string
	CorrelationID       string
	ParentCorrelationID string
	TopLevelTransfer    bool
	Sequence            int64
	AgentID             string // Raw responding agent ID; UI layers canonicalize separately when needed.
	RuntimeAgentID      string
	AgentName           string // Display name for UI attribution.
	AgentType           string
	PipelineID          string
	TaskID              string
	TaskName            string
	TaskSlug            string
	Current             int
	Total               int
	Message             string
	ToolDerived         bool
	Watchdog            bool
	UIState             events.AgentUIState
	Visibility          events.EventVisibility
	BranchRef           *InterAgentBranchRefMsg
}

// StreamCompleteMsg signals the end of an LLM response stream.
type StreamCompleteMsg struct {
	SessionID           string
	CorrelationID       string
	ParentCorrelationID string
	TopLevelTransfer    bool
	AgentID             string // Raw responding agent ID; UI layers canonicalize separately when needed.
	RuntimeAgentID      string
	AgentName           string // Display name for UI attribution.
	AgentType           string
	PipelineID          string
	TaskID              string
	TaskName            string
	TaskSlug            string
	Result              any
	InputTokens         int // Real provider input tokens (0 = unavailable).
	OutputTokens        int // Real provider output tokens (0 = unavailable).

	// Extended token fields for accurate cost/usage tracking.
	ReasoningTokens  int // Reasoning/thinking tokens (OpenAI o-series, Google thinking).
	CacheReadTokens  int // Prompt cache hits (Anthropic, OpenAI).
	CacheWriteTokens int // Prompt cache writes (Anthropic).

	// AuthoritativeText is the canonical response text sent by the agent.
	// When non-empty, the chat model replaces accumulated streaming content
	// with this value to correct any dropped or reordered chunks.
	AuthoritativeText string
	Visibility        events.EventVisibility
	BranchRef         *InterAgentBranchRefMsg
}

// StreamErrorMsg signals an error during streaming.
type StreamErrorMsg struct {
	SessionID     string
	CorrelationID string
	Err           error
	BranchRef     *InterAgentBranchRefMsg
}

// AgentStateMsg carries an explicit agent activity-state transition from
// the bus to the chat model. Unlike text stream messages this is routed
// even when no prior StreamStart has been observed — state events are the
// canonical source for "what is happening right now" and must reach the
// UI regardless of text-stream lifecycle (see StreamEventAgentState in
// agents/guide).
type AgentStateMsg struct {
	SessionID           string
	CorrelationID       string
	ParentCorrelationID string
	AgentID             string
	AgentType           string
	State               string
	Detail              string
	TransitionID        int64
	PeerAgentType       string
	PeerCorrelationID   string
	Timestamp           time.Time
}

// ---------------------------------------------------------------------------
// Guide responses
// ---------------------------------------------------------------------------

// GuideResponseMsg wraps a complete response from the Guide router.
type GuideResponseMsg struct {
	CorrelationID string
	AgentID       string
	AgentName     string
	AgentType     string
	Content       string
	Err           error
	BranchRef     *InterAgentBranchRefMsg
}

type CommandApprovalRequestMsg struct {
	Proposal *commandapproval.Proposal
}

type CommandApprovalCommitMsg struct {
	Proposal *commandapproval.Proposal
	Decision string
}

type CommandApprovalResolvedMsg struct{}

// PlanApprovalRequestMsg carries a plan acceptance proposal from the bus
// to the AppModel. Triggers the input panel to substitute the editor
// with the three-button (Approve / Modify / Reject) dialog. Mirrors
// CommandApprovalRequestMsg's role for command approvals.
type PlanApprovalRequestMsg struct {
	Proposal *planapproval.Proposal
}

// PlanApprovalCommitMsg is produced when the user clicks one of the
// three dialog buttons. The AppModel handler publishes the structured
// Decision back to Guardian (via TopicResponses("guardian", g.id)) so
// Guardian's pending channel unblocks and returns the verdict to the
// architect's continuation handler.
type PlanApprovalCommitMsg struct {
	Proposal *planapproval.Proposal
	Verdict  planapproval.Verdict
}

// PlanApprovalResolvedMsg signals that the dialog has been dismissed
// (verdict committed, response published). Triggers AppModel to clear
// its planApproval state and restore the input panel to the editor.
type PlanApprovalResolvedMsg struct{}

// StreamRerouteMsg signals that an agent-initiated reroute is occurring.
// The Guide broadcasts this when an agent invokes the reroute_request skill
// and the request is being re-classified to a different agent.
type StreamRerouteMsg struct {
	SessionID             string
	CorrelationID         string // New correlation ID for the rerouted request.
	OriginalCorrelationID string // Correlation ID of the original request being rerouted.
	FromAgentID           string // Agent that requested the reroute.
	ToAgentID             string // New target agent (may be empty if not yet classified).
	Reason                string // Why the reroute happened.
}

// RetryStatusMsg reports a retry or model-fallback attempt during Guide processing.
type RetryStatusMsg struct {
	SessionID     string
	CorrelationID string
	Attempt       int
	MaxAttempts   int
	Delay         time.Duration // Time until next retry attempt.
	Error         string        // Human-readable summary (not raw error).
}

// ---------------------------------------------------------------------------
// User actions
// ---------------------------------------------------------------------------

// SubmitPromptMsg is sent when the user submits a prompt.
type SubmitPromptMsg struct {
	Text        string
	TargetAgent string // Empty for auto-routing, agent name for @agent syntax
	SessionID   string
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

// TickMsg is sent periodically for scroll momentum, spinner animation, etc.
type TickMsg struct {
	Time time.Time
	Gen  uint64 // Tick chain generation; stale ticks are dropped.
}

// DecorTickMsg is sent at a slower rate for low-frequency UI effects
// (status spinner, chat highlight/edge flash, tab arrow flash).
// Separated from TickMsg to keep 60fps physics while reducing idle CPU.
type DecorTickMsg struct {
	Time time.Time
	Gen  uint64 // Tick chain generation; stale ticks are dropped.
}

// MemoryCanvasTickMsg drives the memory-view ambient animation loop
// (particle drift, shimmer, pollen). Only live while ViewMemory is
// active; stale ticks (mismatched Gen) are dropped so exiting the
// mode cleanly terminates the chain.
type MemoryCanvasTickMsg struct {
	Time time.Time
	Gen  uint64
}

// BlinkMsg fires on a one-shot timer at the next phase boundary.
// The handler computes the correct blink phase from the wall clock,
// so delayed messages cannot cause phase inversion.
type BlinkMsg struct {
	Gen      uint64    // Generation counter; stale blinks are dropped.
	Deadline time.Time // Absolute time this blink was targeted at.
}

// LSPFlushMsg fires on a one-shot 300ms timer to flush debounced LSP
// didChange notifications. The Gen field matches the editor's editGeneration
// at scheduling time so stale flushes (superseded by newer edits) are dropped.
type LSPFlushMsg struct {
	Gen int
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

// LSPReferencesRequestMsg is emitted by the editor when the user presses gr
// in normal mode, requesting all references of the symbol under the cursor.
type LSPReferencesRequestMsg struct {
	FilePath string
	Line     int // 0-indexed
	Col      int // 0-indexed (rune offset)
}

// LSPReferencesMsg carries reference locations from a language server.
type LSPReferencesMsg struct {
	FilePath  string
	Line      int
	Col       int
	Locations []lsp.Location
	Err       error
}

// LSPDocumentSymbolMsg carries document symbols from a language server.
type LSPDocumentSymbolMsg struct {
	FilePath string
	Symbols  []lsp.DocumentSymbol
	Err      error
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

// LSPSignatureHelpRequestMsg is emitted by the editor in insert mode
// when a trigger character is typed, requesting signature help.
type LSPSignatureHelpRequestMsg struct {
	FilePath string
	Line     int // 0-indexed
	Col      int // 0-indexed (rune offset)
}

// LSPSignatureHelpMsg carries signature help from a language server.
type LSPSignatureHelpMsg struct {
	FilePath string
	Line     int
	Col      int
	Result   *lsp.SignatureHelp
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

// LSPFormatRequestMsg is emitted by the editor when the user runs :format,
// requesting textDocument/formatting from the language server.
type LSPFormatRequestMsg struct {
	FilePath string
}

// LSPFormatMsg carries formatting edits from a language server.
type LSPFormatMsg struct {
	FilePath       string
	Edits          []lsp.TextEdit
	Err            error
	EditGeneration int // buffer generation at request time for staleness detection
}

// LSPPrepareRenameMsg carries the result of a prepareRename request.
type LSPPrepareRenameMsg struct {
	FilePath string
	Line     int
	Col      int
	NewName  string // Carried through to the rename step.
	Result   *lsp.PrepareRenameResult
	Err      error
}

// LSPRenameMsg carries the workspace edit from a rename request.
type LSPRenameMsg struct {
	FilePath string
	NewName  string
	Edit     *lsp.WorkspaceEdit
	Err      error
}

// TabNextMsg requests switching to the next editor tab.
type TabNextMsg struct{}

// TabPrevMsg requests switching to the previous editor tab.
type TabPrevMsg struct{}

// TabJumpMsg requests switching to a specific tab by 0-indexed position.
type TabJumpMsg struct{ Index int }

// TabCloseRequestMsg requests closing the tab for the given file path.
type TabCloseRequestMsg struct{ Path string }

// EscDisambiguateTickMsg fires after the ESC disambiguation timeout to
// flush a buffered standalone ESC when no follow-up rune arrived. The
// Gen field matches the generation counter at buffer time so stale ticks
// (from already-resolved ESC events) are silently ignored.
type EscDisambiguateTickMsg struct{ Gen uint64 }

// ChordBlockedExpireMsg signals that the blocked-chord hint should auto-dismiss.
type ChordBlockedExpireMsg struct{}

// ---------------------------------------------------------------------------
// File tree
// ---------------------------------------------------------------------------

// NerdFontsResultMsg reports whether Nerd Font symbols were installed.
type NerdFontsResultMsg struct {
	Available bool
}

// FileOpenMsg requests displaying a file in the code viewer.
type FileOpenMsg struct {
	Path      string
	Name      string
	Language  string
	Line      int // 0 = top, >0 = scroll to this 1-based line.
	Col       int // 0-indexed start column for jump marker. 0 = no marker.
	EndCol    int // 0-indexed end column (exclusive). 0 = derive from word bounds.
	CursorCol int // If > 0, position cursor at this 0-indexed column after line jump.
}

// FilePreviewMsg requests displaying a file in preview mode (read-only).
// Distinguished from FileOpenMsg, which opens the file for editing.
type FilePreviewMsg struct {
	Path     string
	Name     string
	Language string
	Line     int // 0 = top, >0 = scroll to this 1-based line.
	Col      int // 0-indexed column for positioning.
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

// FileReplacedMsg notifies the app that a file was modified on disk
// by multi-file replace, so the editor can reload if it's the active file.
type FileReplacedMsg struct {
	Path string
}

// MultiFileReplaceDoneMsg reports the outcome of a multi-file replace-all.
type MultiFileReplaceDoneMsg struct {
	FilesChanged  int
	TotalReplaced int
	Skipped       int // Files skipped (unsaved changes in editor).
}

// ---------------------------------------------------------------------------
// Git status
// ---------------------------------------------------------------------------

// GitStatusMsg carries the resolved git status snapshot for the file tree.
// Paths in all maps are relative to the repository root.
type GitStatusMsg struct {
	StatusMap   map[string]git.GitFileState
	TrackedSet  map[string]struct{}
	TrackedDirs map[string]struct{}
}

// GitOpEventMsg carries a completed git mutation event from the GitBridge.
// Only emitted for mutating operations (commit, checkout, merge, etc.).
type GitOpEventMsg struct {
	Op       string        // Operation name (e.g. "checkout_branch").
	Err      error         // nil on success.
	Duration time.Duration // Wall-clock time of the operation.
	Params   any           // Operation-specific parameters for context.
}

// ---------------------------------------------------------------------------
// Diff view
// ---------------------------------------------------------------------------

// DiffViewDataMsg carries computed diff pairs from the background fetch.
type DiffViewDataMsg struct {
	Pairs []DiffViewPair
	Mode  int // CompareMode (int to avoid import cycle)
}

// MergeDiffViewDataMsg carries computed diff pairs for a merge diff view.
type MergeDiffViewDataMsg struct {
	Pairs []DiffViewPair
}

// DiffViewPair is a wire-format diff pair for the message layer.
type DiffViewPair struct {
	FromHash  string
	ToHash    string
	FromShort string
	ToShort   string
	Files     []git.FileDiff
	TotalAdd  int
	TotalDel  int
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

// AuthStatusMsg carries per-provider auth availability after the background
// ProbeAll completes. Dispatched by a deferred Init cmd so the status bar
// icons update once the AuthRegistry is populated.
type AuthStatusMsg struct {
	Providers map[string]bool   // provider → available
	Methods   map[string]string // provider → active auth method
}

// ---------------------------------------------------------------------------
// Plan viewer
// ---------------------------------------------------------------------------

// PlanUpdateMsg carries an updated plan snapshot for the plan viewer.
// Deprecated fallback during claims-presentation migration; canonical
// review content is a presentable plan_markdown artifact. Removal ref:
// CLAIMS_VISIBILITY Phase 6.2 / claims-plan-sidechannels.
type PlanUpdateMsg struct {
	PlanID          string
	CorrelationID   string // Correlation of the request that owns this plan.
	Status          string // "pending", "analyzing", ..., "ready", "executing", "completed", "failed"
	Tasks           []PlanTaskSnapshot
	ExecutionLayers [][]string // Task IDs per layer
	TotalTokensIn   int
	TotalTokensOut  int
	StartTime       time.Time
}

// PlanAcceptanceCriterion defines a single verifiable acceptance condition
// using Given/When/Then structure.
type PlanAcceptanceCriterion struct {
	Given    string
	When     string
	Then     string
	Priority string // "must" | "should" | "could"
}

// PlanFileTarget identifies a file affected by a plan task.
type PlanFileTarget struct {
	Path      string
	Operation string // "create" | "modify" | "delete"
	Reason    string
}

// PlanTaskExample is a compact code, usage, or diagram example attached to a task.
type PlanTaskExample struct {
	Label       string
	Language    string
	Code        string
	Explanation string
}

// PlanTaskSnapshot is a point-in-time view of a single task.
type PlanTaskSnapshot struct {
	ID            string
	Name          string
	Description   string
	AgentType     string
	Status        string // "pending", "queued", "running", "completed", "failed", "blocked", "skipped"
	Dependencies  []string
	TokensIn      int
	TokensOut     int
	Duration      time.Duration
	StatusMessage string

	// Rich specification fields.
	AcceptanceCriteria  []PlanAcceptanceCriterion
	AffectedFiles       []PlanFileTarget
	ImplementationGuide string
	Guidelines          []string
	Examples            []PlanTaskExample
}

// PlanViewToggleMsg requests toggling the plan viewer panel visibility.
type PlanViewToggleMsg struct{}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Pipeline & variant state
// ---------------------------------------------------------------------------

// PipelineStateMsg carries a pipeline state update for the agent panel.
type PipelineStateMsg struct {
	PipelineID        string
	RuntimePipelineID string
	TaskID            string
	TaskSlug          string
	TaskLabel         string
	Status            string // TDD phase or lifecycle status.
	WorkerType        string // Agent type running the pipeline.
	LoopCount         int
	MaxLoops          int
	Timestamp         time.Time
}

// ClaimsProjectionMsg carries claims board projection updates for the
// pipeline counter display (accepted/total).
type ClaimsProjectionMsg struct {
	SessionID     string
	BoardID       string
	TaskID        string
	AcceptedCount int
	TotalClaims   int
}

// ClaimsAgentStatusMsg is the authoritative active/idle signal for an
// agent. ClaimsBridge emits one per agent on each cycle edge
// transition (cycle open / cycle close). Per UI_DESIGN.md §3.1 the
// agent panel uses this as the SOLE authoritative source of
// Status=Acting vs Status=Idle / Success / Error — no other code path
// writes Status (UI_DESIGN.md §7 P7.1).
type ClaimsAgentStatusMsg struct {
	AgentID   string
	SessionID string
	Active    bool   // true on cycle open; false on cycle close (drain or handoff)
	CycleID   string // root claim ID anchoring this cycle (empty when Active=false from session reset)
	OpenCount int    // count of open subject claims for the owner (debugging)
	Reason    string

	// State is the categorical agent activity state to use for this
	// cycle edge when the claim carries a UI-specific state that is
	// more precise than ActionType (for example Guide classification).
	State string

	// SuppressChat means this claim is an agent-panel/status signal
	// only. The chat panel must not open, update, or reserve a cycle
	// entry for it; other panels may still consume it.
	SuppressChat bool

	// TerminalOutcome carries the cycle's verdict on close. Populated
	// only when Active=false. Maps to the agent panel's terminal
	// status visual: "success" → StatusSuccess, "failure" → StatusError,
	// "" → StatusIdle (clean close with no explicit outcome).
	TerminalOutcome string

	// ActionType carries the originating claim's classification
	// ("prompt", "handoff", "consultation", "task", "archival", ...).
	// The agent panel uses this to filter which cycles drive
	// user-visible TaskSummary updates: only user-or-truly-agent-driven
	// work (prompt/handoff/consultation) writes to the left panel —
	// background archival/boot/activation/testament/etc. cycles do not,
	// so the panel never shows "Processing X request" for internal
	// recordkeeping. The chat panel uses the same field to suppress
	// system-internal cycles (claims.IsSystemInternalAction) so they
	// never open a chat row. Empty when the bridge couldn't resolve
	// the underlying claim (treated as non-allowlisted).
	ActionType string

	// StreamCorrelationID is the route-level stream correlation that
	// the prompt/handoff was dispatched under, propagated from the
	// originating claim's metadata. The chat panel folds this into
	// the cycle ChatEntry's AdditionalCorrelationIDs so subsequent
	// StreamStartMsg/StreamChunkMsg/StreamCompleteMsg events resolve
	// onto the same row as claim artifacts. Empty when the cycle did
	// not originate from a routed stream (e.g., system-internal
	// cycles or peer-issued claims with no UI-side route).
	StreamCorrelationID string
}

// ClaimContextMsg updates a claim's developing-narrative status text.
// Emitted by the bridge whenever a "claim_context_changed" delta lands
// on the active session's board (i.e. an agent called
// board.SetClaimContext to narrate a state transition). The agent
// panel keys by OwnerAgentID — the row's TaskSummary picks up the
// new Context. The chat panel keys by ClaimID/CycleID — the row's
// status line refreshes without creating a new row. See
// docs/CLAIMS_UI.md.
type ClaimContextMsg struct {
	SessionID         string
	ClaimID           string
	OwnerAgentID      string
	CycleID           string
	ParentRowID       string
	Context           string
	ContextTransition int64
	SuppressChat      bool
	// State is the categorical agent activity state (the canonical
	// taxonomy from agents/shared.AgentActivityState — "tool_executing",
	// "awaiting_peer_response", "challenging_peer", "awaiting_guardian",
	// etc.). Populated by the bridge from the most recent agent_state
	// artifact's Metadata["state"] for the cycle. The agent panel maps
	// this to events.AgentUIState for the left-panel icon. Empty when
	// no agent_state artifact has been observed for the cycle yet.
	State string
}

// ClaimPeerInteractionMsg projects a peer-interaction claim into the chat
// tree. It is emitted from canonical claims deltas and keyed by ClaimID, so
// consult/challenge/guardian rows are rendered from the claim graph rather
// than inferred from tool-call arguments or streaming metadata.
type ClaimPeerInteractionMsg struct {
	SessionID      string
	CycleID        string
	ClaimID        string
	ActionType     string // "consultation" | "challenge" | "guardian_check"
	IssuerAgentID  string
	SubjectAgentID string
	Title          string
	Context        string
	Status         string // "pending" | "done" | "failed"
	TestamentID    string
	ValidationID   string
	Sequence       uint64
	DeltaKey       string
	OccurredAt     time.Time
	SuppressChat   bool
}

// TestamentContextMsg updates an in-flight testament's developing-
// conclusion narrative. Emitted by the bridge whenever a
// "testament_context_changed" delta lands. The chat panel keys by
// AccumulatorID before the testament is sealed (the in-flight anchor
// the accumulator stamps onto every emission), then rebinds onto
// TestamentID after the testament is submitted. Both IDs travel on
// every delta so the chat can resolve a single row across the flush
// boundary. See docs/CLAIMS_UI.md "Accumulator-anchored testament
// context".
type TestamentContextMsg struct {
	SessionID         string
	AccumulatorID     string
	TestamentID       string
	ClaimID           string
	AgentID           string
	CycleID           string
	ParentRowID       string
	Context           string
	ContextTransition int64
}

// ClaimArtifactAddedMsg opens a row in the chat tree. Emitted by the
// bridge when a *_started artifact lands. Per UI_DESIGN.md §3.2 the
// chat panel renders mechanically: top-level rows are keyed by
// CycleID, nested rows are keyed by ParentRowID — no relation walking.
type ClaimArtifactAddedMsg struct {
	ArtifactID     string         // unique ID — the row key
	CycleID        string         // which top-level cycle this row lives under
	ParentRowID    string         // immediate parent within the cycle (artifact ID, or empty for cycle-root rows)
	ClaimID        string         // claim this artifact's testament responds to
	OwnerAgentID   string         // canonical owner of the parent claim
	OwnerAgentType string         // type label rendered in child-agent blocks ("guardian", "tester-pipeline", ...)
	TargetAgentID  string         // target of the parent claim (subject)
	AgentID        string         // record-issuing agent (Artifact.AgentID — replica/instance)
	Kind           string         // "tool_started" | "consult_started" | "challenge_started" | "guardian_check_started" | "llm_started" | "cycle_root"
	Reference      string         // tool name, consult title, etc.
	Metadata       map[string]any // kind-specific structured detail
	CreatedAt      time.Time
	SuppressChat   bool
}

// ClaimResponseTextMsg carries the agent's final assistant message
// to the chat panel. Emitted by the bridge when a response_text
// artifact lands. The chat panel writes Content onto the cycle's
// ChatEntry rather than rendering as a tool row — response_text is
// the answer, not evidence. UI_DESIGN.md treats *_started/_completed
// as the visible artifact lifecycle; response_text falls outside
// that lifecycle and needs its own delivery path.
type ClaimResponseTextMsg struct {
	SessionID    string
	CycleID      string
	ClaimID      string
	ParentRowID  string
	AgentID      string
	Content      string
	CreatedAt    time.Time
	SuppressChat bool
}

// ClaimPresentationMsg carries user-facing content projected from a
// claims-board testament or artifact. During legacy replay, SourceType
// may be "synthetic" for a transient row derived from stored board
// state; Metadata["synthetic"] marks that case. The board remains the
// source of truth.
type ClaimPresentationMsg struct {
	SessionID   string
	CycleID     string
	ClaimID     string
	SourceType  string // "testament" | "artifact" | "synthetic"
	SourceID    string
	TestamentID string
	AgentID     string
	Title       string
	Content     string
	Format      string
	Placement   string
	ReplaceKey  string
	Metadata    map[string]any
	CreatedAt   time.Time
	Sequence    uint64
}

// ClaimArtifactCompletedMsg closes a row in the chat tree. Emitted by
// the bridge when a *_completed artifact lands carrying
// Relation{completes}. The chat panel matches by StartArtifactID and
// flips the row to its terminal visual. UI_DESIGN.md §3.3.
type ClaimArtifactCompletedMsg struct {
	StartArtifactID string        // matches the ClaimArtifactAddedMsg.ArtifactID
	CycleID         string        // same cycle as the started artifact (denormalized for routing convenience)
	Outcome         string        // "success" | "failure" | "timeout" | "cancelled"
	Duration        time.Duration // wall-clock from started to completed
	Summary         string        // one-line result summary (truncated tool output, child testament summary)
	Metadata        map[string]any
	CompletedAt     time.Time
	SuppressChat    bool
}

// VariantStateMsg carries a variant state update for the agent panel.
type VariantStateMsg struct {
	VariantID  string
	PipelineID string
	Name       string
	State      string // created, active, suspended, complete, failed, merging, merged, cancelled.
	Message    string
}

// LoginResultMsg is sent when an auth flow completes (OAuth callback or API key save).
type LoginResultMsg struct {
	Provider string // "google", "openai", "anthropic"
	Method   string // "oauth", "apikey"
	Success  bool
	Error    string // Non-empty on failure.
	FlowID   uint64 // OAuth flow identifier (0 for API-key auth).
}

// ---------------------------------------------------------------------------
// Model swap
// ---------------------------------------------------------------------------

// ModelChangeMsg signals the user changed an agent's LLM model via the selector.
type ModelChangeMsg struct {
	AgentID   string
	AgentType string
	ModelID   string
}

// ModelSwapResultMsg reports the outcome of a backend model swap.
type ModelSwapResultMsg struct {
	AgentID   string
	AgentType string
	ModelID   string
	Err       error
}

// ---------------------------------------------------------------------------
// Prompt queue
// ---------------------------------------------------------------------------

// QueueAdvanceMsg signals that the prompt queue should dispatch one or more
// pending entries. EntryIDs lists the specific entries to dispatch; when
// empty, the handler dispatches all ready entries (backward-compatible).
type QueueAdvanceMsg struct {
	EntryIDs []string
}

// ---------------------------------------------------------------------------
// Tool call visualization
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Layer decision (DAG failure classification)
// ---------------------------------------------------------------------------

// LayerDecisionMsg is sent when a DAG layer encounters a blocking failure
// and requires a user decision to proceed.
type LayerDecisionMsg struct {
	DAGID       string
	LayerIdx    int
	FailedNodes []LayerFailedNode
}

type LayerDecisionCommitMsg struct {
	DAGID    string
	LayerIdx int
	Decision string
}

type LayerDecisionResolvedMsg struct{}

// LayerFailedNode describes a single failed node in a layer decision.
type LayerFailedNode struct {
	NodeID    string
	NodeName  string
	AgentType string
	Error     string
}

// ---------------------------------------------------------------------------
// Tool call visualization
// ---------------------------------------------------------------------------

type InterAgentToolEventMsg struct {
	Kind         string
	AgentTypes   []string
	Summary      string
	ThreadKey    string
	Status       string
	UpdateOrigin bool
}

type InterAgentBranchRefMsg struct {
	ParentCorrelationID string
	ParentToolCallKey   string
	ThreadKey           string
	Kind                string
}

// ToolCallEventMsg is the retired pre-claims tool-call shape. New UI surfaces
// render tool, consult, challenge, and guardian rows from claim artifacts.
type ToolCallEventMsg struct {
	SessionID           string
	CorrelationID       string
	ParentCorrelationID string
	TopLevelTransfer    bool
	AgentID             string
	AgentType           string
	AgentName           string
	PipelineID          string
	TaskID              string
	TaskName            string
	TaskSlug            string
	ToolCallKey         string
	Phase               int // 0 = start, 1 = complete.
	ToolName            string
	ArgsSummary         string
	FullArgs            string // Pretty-printed JSON (for expanded view).
	Output              string // Truncated result (for expanded view, max 512 chars).
	ErrorMsg            string // Error text on failure.
	StartedAt           time.Time
	Duration            time.Duration
	Success             bool
	InterAgent          *InterAgentToolEventMsg
	BranchRef           *InterAgentBranchRefMsg
}

// TimePressureMsg signals that an agent is approaching its operation deadline.
// The UI can display this to inform the user and offer options (cancel, wait).
type TimePressureMsg struct {
	AgentType string
	Stage     string
	Elapsed   time.Duration
	Remaining time.Duration
	Message   string
}

// ---------------------------------------------------------------------------
// Knowledge indexing progress
// ---------------------------------------------------------------------------

// IndexProgressMsg carries progress updates from the background knowledge
// indexer. Sent periodically by a polling goroutine so the status bar can
// render a thin progress bar.
type IndexProgressMsg struct {
	Phase   int // Maps to status.IndexPhase.
	Current int64
	Total   int64
	Done    bool
}
