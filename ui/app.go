package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/detect"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/llm/accounting"
	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/core/pipeline/variants"
	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/session"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/bridge"
	"github.com/adalundhe/sylk/ui/chat"
	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/committree"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/conflictview"
	"github.com/adalundhe/sylk/ui/diffview"
	"github.com/adalundhe/sylk/ui/editor"
	"github.com/adalundhe/sylk/ui/editor/mode"
	"github.com/adalundhe/sylk/ui/editor/preview"
	"github.com/adalundhe/sylk/ui/editor/register"
	"github.com/adalundhe/sylk/ui/fieldmanual"
	"github.com/adalundhe/sylk/ui/filetree"
	"github.com/adalundhe/sylk/ui/gitpanel"
	inputpkg "github.com/adalundhe/sylk/ui/input"
	"github.com/adalundhe/sylk/ui/interrupt"
	knowledgepkg "github.com/adalundhe/sylk/ui/knowledge"
	"github.com/adalundhe/sylk/ui/layout"
	"github.com/adalundhe/sylk/ui/login"
	markdownpkg "github.com/adalundhe/sylk/ui/markdown"
	"github.com/adalundhe/sylk/ui/memoryview"
	"github.com/adalundhe/sylk/ui/mergediff"
	"github.com/adalundhe/sylk/ui/modal"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/planview"
	"github.com/adalundhe/sylk/ui/queue"
	"github.com/adalundhe/sylk/ui/search"
	sessionpkg "github.com/adalundhe/sylk/ui/session"
	"github.com/adalundhe/sylk/ui/status"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
)

// tickFastInterval drives 60fps animations (scroll momentum, bounce, flash).
// Derived from: 1000ms / 60fps ≈ 16ms.
const tickFastInterval = 16 * time.Millisecond

// tickSlowInterval drives transient slow-rate work. Currently unused
// (swipe decay was the original driver; swipe gestures were removed),
// but retained so future debounce work can opt into the slow-tick
// chain without reintroducing the rate state machine.
const tickSlowInterval = 200 * time.Millisecond

// decorTickActiveInterval drives high-frequency low-cost UI effects
// (spinners, flashes, holographic activity, queue shimmer).
// 100ms (~10fps) keeps active motion smooth.
const decorTickActiveInterval = 100 * time.Millisecond

// decorTickIdleInterval drives resting shimmer and focus-ring motion when the
// UI is otherwise idle. Slowing this path cuts resting CPU substantially
// while preserving visible motion.
const decorTickIdleInterval = 300 * time.Millisecond

// idleFocusBorderPhaseStep is the resting focus-ring redraw cadence. Keeping
// it slower than the active path cuts idle CPU, while the border renderer's
// full-perimeter phase spread preserves continuous circulation.
const idleFocusBorderPhaseStep = 200 * time.Millisecond

var agentContextCounter = providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig())

// blinkHalfPeriod is the duration between cursor visibility toggles.
// Derived from: standard terminal cursor blink rate (~530ms per phase).
const blinkHalfPeriod = 530 * time.Millisecond

// resizeAnimationQuiesce pauses purely cosmetic motion briefly after a resize
// so animation ticks cannot repaint against stale geometry during drag-resize.
const resizeAnimationQuiesce = 120 * time.Millisecond

// tickRate classifies the current tick chain speed.
type tickRate int

const (
	tickIdle tickRate = iota // No tick scheduled.
	tickSlow                 // 200ms — transient slow-rate debounce (reserved).
	tickFast                 // 16ms — scroll, bounce, flash, spinner.
)

type decorCadence uint8

const (
	decorCadenceOff decorCadence = iota
	decorCadenceIdle
	decorCadenceActive
)

type slotBorderMeta struct {
	focused bool
	w       int
	h       int
}

type leftPanelSections struct {
	sessionRect   pane.Rect
	agentsRect    pane.Rect
	agentsHeaderY int
	selectorY     int
}

// lspDebounceInterval is the delay after the last keystroke before sending
// a didChange notification. Derived from: typical typing speed ~5 chars/sec
// = 200ms between keystrokes; 300ms batches rapid edits while staying
// responsive. Standard for LSP clients (VSCode uses 300ms).
const lspDebounceInterval = 300 * time.Millisecond

// lspNotifyTimeout bounds fire-and-forget LSP notifications (didSave, didClose).
// Derived from: these are thin JSON-RPC writes to a local process; 5s is ample.
const lspNotifyTimeout = 5 * time.Second

// overlayToggleDebounce prevents hold-to-repeat from rapidly toggling
// overlay elements (chord hints, find bar). Derived from: typical terminal
// key repeat starts at ~30ms intervals; 150ms absorbs repeats while still
// feeling responsive for intentional double-taps.
const overlayToggleDebounce = 150 * time.Millisecond

// tabArrowFlashDuration is how long the overflow arrow stays highlighted.
// Roughly matches the prior 63×16ms tick timing (~1.0s).
const tabArrowFlashDuration = 1 * time.Second

// escDisambiguateTimeout is the maximum delay between a standalone ESC byte
// and a follow-up rune before the ESC is flushed as a real Escape keypress.
// Derived from: standard terminal practice — vim ttimeoutlen=50, tmux
// escape-time=50. 50 ms balances fast ESC response with reliable Alt+key
// detection across SSH and varied terminal emulators.
const escDisambiguateTimeout = 50 * time.Millisecond

// sourceAgentTUI identifies the TUI as the source agent for guide routing.
const sourceAgentTUI = "tui"

const defaultGuideSessionID = "default"

// guideAgent constants identify the guide activity stream in the agent panel.
const (
	guideAgentID   = "guide"
	guideAgentName = "Guide"
	guideAgentType = "guide"
)

// guideContext model for lightweight runtime context usage estimation.
// We retain a high fraction of prior tokens to represent rolling context.
const (
	guideMaxContextTokens        = 2_000_000
	defaultAgentMaxContextTokens = 200_000
	guideContextRetention        = 0.92
	guideRouteOverheadTokens     = 1200
	guideResponseOverheadTokens  = 120
)

// ---------------------------------------------------------------------------
// Warp points
// ---------------------------------------------------------------------------

// warpSlotCount is the number of warp point slots (Alt+1 through Alt+9).
// Derived from: 9 digit keys on the keyboard.
const warpSlotCount = 9

// ratioStep is the per-keypress adjustment to a split ratio.
// Derived from: 5% per press gives 16 discrete positions between 0.1–0.9.
const ratioStep = 0.05

// WarpPoint stores a cursor position bookmark for instant teleportation.
type WarpPoint struct {
	Path     string // Absolute file path.
	Line     int    // 0-indexed line number.
	Col      int    // 0-indexed column (rune offset).
	StartCol int    // 0-indexed start of symbol at cursor (rune offset).
	EndCol   int    // 0-indexed end of symbol at cursor (exclusive, rune offset).
}

// shiftDigitSlot maps shifted digit characters to warp slot indices.
// Derived from: US keyboard Shift+1..9 produces !@#$%^&*(.
var shiftDigitSlot = map[byte]int{
	'!': 0, '@': 1, '#': 2, '$': 3, '%': 4,
	'^': 5, '&': 6, '*': 7, '(': 8,
}

// ---------------------------------------------------------------------------
// Overlay state
// ---------------------------------------------------------------------------

// overlayState tracks which overlay (if any) is currently active.
type overlayState int

const (
	overlayNone        overlayState = iota
	overlayEditor                   // Full-screen editor.
	overlayModal                    // Modal dialog stack.
	overlaySearch                   // Command palette.
	overlayFieldManual              // Field Manual help overlay.
	overlayLogin                    // Top-panel login flow.
)

// ---------------------------------------------------------------------------
// Panel layout
// ---------------------------------------------------------------------------

// defaultPanels defines the initial panel layout with flex-grow weights and
// collapse thresholds. CollapseWidth is the minimum allocated width before
// the panel is hidden; breakpoints are derived from flex-grow distribution.
//
// Collapse order: FileTree first, then Code, then Session. Chat never collapses.
var defaultPanels = []layout.PanelSpec{
	{ID: component.FocusSessionPanel, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 1, CollapseWidth: sessionCollapseWidth, Visible: true},
	{ID: component.FocusFileTree, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 1, CollapseWidth: fileTreeCollapseWidth, Visible: true},
	{ID: component.FocusChat, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 2, Visible: true},
	{ID: component.FocusCodeViewer, MinWidth: layout.DefaultMinPanelWidth, FlexGrow: 2, CollapseWidth: codeCollapseWidth, Visible: true},
}

// sessionCollapseWidth is derived from: 36 content + 2 border + 2 pad + 4 header.
const sessionCollapseWidth = 44

// fileTreeCollapseWidth collapses the panel before the git tab bar wraps.
// Base: 30 content + 2 border + 2 pad + 4 indent = 38.
const fileTreeBaseCollapseWidth = 38

var fileTreeCollapseWidth = max(fileTreeBaseCollapseWidth, gitpanel.TabBarNaturalWidth()+panelBorderSize)

// codeCollapseWidth is derived from: 56 content + 4 gutter + 1 sep + 2 border + 3 pad.
// At this width, common code lines (≤56 chars) display without truncation.
const codeCollapseWidth = 66

// defaultModeCandidates defines explicit column assignments per layout mode,
// evaluated from widest to narrowest. The first candidate whose panels all
// meet their CollapseWidth threshold wins.
var defaultModeCandidates = []layout.ModeCandidate{
	{Mode: layout.FourColumn, Columns: []component.FocusID{
		component.FocusSessionPanel, component.FocusFileTree, component.FocusChat, component.FocusCodeViewer,
	}},
	{Mode: layout.ThreeColumn, Columns: []component.FocusID{
		component.FocusSessionPanel, component.FocusChat, component.FocusCodeViewer,
	}},
	{Mode: layout.TwoColumn, Columns: []component.FocusID{
		component.FocusSessionPanel, component.FocusChat,
	}},
	// SingleColumn uses Chat as the anchor so all ring-swapped panels
	// inherit the full terminal width via findSharedColumnDims.
	{Mode: layout.SingleColumn, Columns: []component.FocusID{
		component.FocusChat,
	}},
}

// defaultTabOrder defines the focus cycling order.
var defaultTabOrder = []component.FocusID{
	component.FocusInput,
	component.FocusChat,
	component.FocusSessionPanel,
	component.FocusAgentPanel,
	component.FocusCodeViewer,
}

// ---------------------------------------------------------------------------
// Dependencies
// ---------------------------------------------------------------------------

// Deps holds all core system references needed by the TUI.
// The caller (cmd.go) is responsible for constructing and providing these.
type Deps struct {
	ActivityPub        events.ActivityPublisher
	SessionManager     *session.Manager
	GuideBus           guide.EventBus
	StreamManager      *guide.StreamManager
	Guide              *guide.Guide
	Scope              *concurrency.GoroutineScope
	AuthRegistry       *credentials.AuthRegistry
	Accountant         *accounting.Accountant
	InterruptAllAgents func(sessionID, reason string) error

	// SignalStop restores default signal handling and cancels the parent
	// context. Called after the Bubble Tea program exits so that a second
	// Ctrl+C immediately terminates the process during slow shutdown.
	SignalStop func()

	// Pre-computed results from parallel bootstrap. These fields are
	// optional; when non-nil they bypass the corresponding blocking
	// detection/creation in New().
	NerdFontsDetected bool
	GitClient         *git.GitClient
	GitWatcher        *git.StatusWatcher
	GitBus            *git.GitBus
	SafetyGuard       *git.SafetyGuard

	// Pipeline system — optional, nil when no pipeline/variant subsystem is active.
	VariantRegistry variants.Registry

	// SeedAgents pre-populates the agent panel with known agents at startup.
	// Agents are created as idle entries without requiring activity events.
	SeedAgents []AgentSeed

	// ModelSwap swaps an agent's LLM model at runtime (within-provider).
	// agentType is the canonical type (e.g. "engineer", "architect").
	// Returns nil on success or the swap error.
	ModelSwap func(ctx context.Context, agentType, modelID string) error

	// ModelSave persists a successful model selection to the config file.
	// Called after a successful swap; errors are logged but non-fatal.
	ModelSave func(agentType, provider, modelID string)

	// AgentModelStore provides persisted model selections so dynamically-
	// created agents (engineer, designer, etc.) pick up previous choices.
	AgentModelStore *agentpkg.AgentModelStore

	// KnowledgeStore exposes background indexing progress for the status bar.
	KnowledgeStore *knowledge.KnowledgeStore

	// Forest exposes structural memory snapshots for the MEMR view.
	Forest MemoryViewService
}

// AgentSeed describes an agent to pre-populate in the UI agent panel.
type AgentSeed struct {
	ID                  string
	AgentType           string
	Name                string
	SupportedModels     []agentpkg.ModelEntry
	PersistedModelID    string // From config file; overrides default when valid.
	PersistedProviderID string // From config file; provider for PersistedModelID.
}

// ---------------------------------------------------------------------------
// AppModel
// ---------------------------------------------------------------------------

// editorPaneState holds the per-pane state for an editor leaf in the
// split tree. Each pane has its own editor.Model and tab order.
type editorPaneState struct {
	editor   *editor.Model
	tabOrder []string
}

type commandApprovalOption struct {
	label    string
	hint     string
	decision string
}

type commandApprovalState struct {
	proposal    *commandapproval.Proposal
	selected    int
	activated   int
	codeScroll  int
	returnFocus component.FocusID
	returnInput bool
}

// planApprovalState is the AppModel-side state for the plan acceptance
// dialog. Mirrors commandApprovalState's shape so the same render +
// input-handling primitives can be reused with minimal duplication.
// Three options (Approve / Modify / Reject) instead of the four-button
// command-approval set; planScroll tracks the user's position when the
// plan markdown exceeds the visible area.
type planApprovalState struct {
	proposal    *planapproval.Proposal
	selected    int
	activated   int
	returnFocus component.FocusID
	returnInput bool
	// bodyScroll tracks scroll position within the markdown-rendered
	// plan-name body, matching the guardian command-approval dialog's
	// codeScroll pattern. When the rendered body exceeds the visible
	// budget the user can scroll; when it fits the value is clamped
	// to zero by visiblePlanApprovalBodyLines.
	bodyScroll int
}

type layerDecisionState struct {
	request     *msg.LayerDecisionMsg
	selected    int
	activated   int
	returnFocus component.FocusID
}

type commandApprovalHitbox struct {
	option int
	y      int
	x0     int
	x1     int
}

type commandApprovalViewLayout struct {
	lines            []string
	hitboxes         []commandApprovalHitbox
	codeStartY       int
	codeEndY         int
	codeTotalLines   int
	codeVisibleLines int
	codeScroll       int
}

var commandApprovalOptions = []commandApprovalOption{
	{label: "Allow Once", hint: "this run", decision: "allow_once"},
	{label: "Allow Always", hint: "save allow rule", decision: "allow_always"},
	{label: "Deny Once", hint: "block this run", decision: "deny_once"},
	{label: "Deny Always", hint: "save deny rule", decision: "deny_always"},
}

var fetchApprovalOptions = []commandApprovalOption{
	{label: "Allow Once", hint: "this fetch", decision: "allow_once"},
	{label: "Allow Always", hint: "save exact URL rule", decision: "allow_always"},
	{label: "Deny Once", hint: "block this fetch", decision: "deny_once"},
	{label: "Deny Always", hint: "block this exact URL", decision: "deny_always"},
}

var layerDecisionOptions = []commandApprovalOption{
	{label: "Retry Layer", hint: "rerun failed nodes", decision: "retry"},
	{label: "Skip Layer", hint: "continue past failures", decision: "skip"},
	{label: "Abort DAG", hint: "cancel this workflow", decision: "abort"},
}

// planApprovalOptions is the three-button option set for the plan
// acceptance dialog. Per the agentic design (skill code is mechanism,
// not policy), the dialog produces an explicit verdict the architect
// can act on directly — no Guide-classifier interpretation of
// free-form text. Reject is also the cancel verb (no separate dismiss).
var planApprovalOptions = []commandApprovalOption{
	{label: "Approve", hint: "launch the plan", decision: "approve"},
	{label: "Modify", hint: "ask for changes", decision: "modify"},
	{label: "Reject", hint: "scrap, choose different direction", decision: "reject"},
}

// AppModel is the root Bubble Tea model that composes all TUI components.
type AppModel struct {
	// Lifecycle context — cancelled on Shutdown to abort in-flight Cmds.
	ctx    context.Context
	cancel context.CancelFunc

	// Configuration
	config Config

	// Core dependencies
	deps Deps

	// Layout
	layout *layout.Manager
	focus  *layout.FocusManager

	leftPanelSections leftPanelSections

	// Panel components
	chat           *chat.Model
	input          *inputpkg.Model
	statusBar      *status.Model
	sessionPanel   *sessionpkg.Model
	agentPanel     *agentpkg.Model
	codePanel      *codepkg.Model
	knowledgePanel *knowledgepkg.Model
	memoryView     *memoryview.Model
	fileTree       *filetree.Model

	// Overlay components
	editorOverlay      *editor.Model
	modalOverlay       *modal.Model
	searchOverlay      *search.Model
	fieldManualOverlay *fieldmanual.Model
	overlay            overlayState

	// Bridges
	accountantBridge *bridge.AccountantBridge
	sessionBridge    *bridge.SessionBridge
	lspBridge        *bridge.LSPBridge
	pipelineBridge   *bridge.PipelineBridge
	claimsBridge     *bridge.ClaimsBridge

	// LSP
	lspManager    *lsp.Manager
	lspInstalling map[lsp.ServerID]bool // Tracks in-progress on-demand installs.

	// Interrupt
	interruptHandler *interrupt.Handler
	lastEscTime      time.Time
	escPressCount    int // consecutive Esc presses within the 1s window

	// Clipboard
	clipboard register.ClipboardProvider

	// State
	chord                 chordState
	chordBlocked          bool             // Chord triggered in edit mode (display-only, no cycling).
	lastToggleKey         string           // Last overlay toggle key (debounce guard).
	lastToggleAt          time.Time        // When lastToggleKey was pressed.
	leftRing              viewRing         // Left slot cycling ring (Session/FileTree).
	rightRing             viewRing         // Right slot cycling ring (Chat/Code).
	collapseHintShown     bool             // First-collapse flash shown once per session.
	pendingUncommittedAll bool             // Alt+U pressed; next 'a' focuses [All].
	scrollSpring          harmonica.Spring // Spring simulation for smooth scroll.
	scroll                scrollState      // Current scroll animation state.
	bounceSpring          harmonica.Spring // Underdamped spring for overscroll bounce.
	bounce                bounceState      // Current bounce animation state.

	// View mode state machine — exactly one of ViewChat/ViewMemory/ViewEdit/ViewGit.
	viewMode            ViewMode
	savedLeftIdx        int               // Saved leftRing.index before edit mode.
	savedRightIdx       int               // Saved rightRing.index before edit mode.
	savedMemoryLeftIdx  int               // Saved leftRing.index before memory mode.
	savedMemoryRightIdx int               // Saved rightRing.index before memory mode.
	savedChatFocus      component.FocusID // Last focused panel in chat mode.
	savedEditFocus      component.FocusID // Last focused panel in edit mode.
	memoryLoadedAt      time.Time
	memoryIndexedIDs    map[string]struct{}

	// Git mode resources.
	gitClient        *git.GitClient    // Git client for StatusWatcher and direct low-level use (nil if not a repo).
	gitBus           *git.GitBus       // All operations route through the bus.
	gitBridge        *bridge.GitBridge // Forwards mutation events to Bubble Tea.
	gitPanel         *gitpanel.Model   // Git explorer panel (left slot in git mode).
	commitTree       *committree.Model // Commit tree visualization (right slot in git mode).
	savedGitLeftIdx  int               // Saved leftRing.index before git mode.
	savedGitRightIdx int               // Saved rightRing.index before git mode.
	savedGitFocus    component.FocusID // Last focused panel in git mode.
	preGitEditMode   bool              // Whether edit mode was active before entering git mode.
	prevGitMode      ViewMode          // Detect git mode transitions for dirty detection.
	gitDataLoaded    bool              // True after first LoadData; skips reload on cycling.

	// Diff view overlay (replaces commit tree when active).
	diffView       *diffview.Model // Diff view component (nil when inactive).
	diffViewActive bool            // True while diff view is displayed.
	diffHashes     []string        // Saved commit hashes for mode switching.
	diffLabels     []string        // Branch names for pane titles (nil for commit diffs).

	// Merge diff view overlay (replaces commit tree when active).
	mergeDiffView       *mergediff.Model // Merge diff view (nil when inactive).
	mergeDiffViewActive bool             // True while merge diff view is displayed.
	mergeHashes         []string         // Saved merge commit hashes.
	mergeLabels         []string         // Branch names for merge pane titles.
	mergeDeleteSource   bool             // Delete source branch after merge completes.

	// Conflict resolution view overlay (replaces commit tree when active).
	conflictView       *conflictview.Model // Conflict view (nil when inactive).
	conflictViewActive bool                // True while conflict view is displayed.

	// Plan DAG viewer panel (visible when a plan is active).
	planView *planview.Model

	// Mouse drag tracking for inline editor selection.
	editorMouseDown bool // Left button pressed inside the code panel.
	editorDragging  bool // Actual drag motion detected after press.

	// Mouse drag tracking for input panel text selection.
	inputMouseDown bool // Left button pressed inside the input panel.

	// Tab bar drag-and-drop reordering.
	tabDragIdx        int         // Tab index being dragged (-1 = none).
	tabDragSourcePane pane.PaneID // Pane the drag originated from (0 = none).
	tabDropTarget     pane.PaneID // Pane highlighted as drop target (0 = none).

	// Tab bar close-icon hover highlight.
	tabHoverClose        int         // Tab index whose close icon is hovered (-1 = none).
	tabHoverPane         pane.PaneID // Pane whose close icon is being hovered.
	previewTabHoverClose int         // Preview tab close icon hover (-1 = none).

	// Overflow arrow flash windows.
	tabArrowFlashLeftUntil  time.Time
	tabArrowFlashRightUntil time.Time

	// Focus ring shimmer.
	focusGradient         *theme.Gradient // Current gradient for focus ring border.
	idleFocusGradient     *theme.Gradient // Subdued blue→white gradient (no active agents).
	activeFocusGradient   *theme.Gradient // Full prismatic gradient (active agents).
	focusRingStart        time.Time       // Epoch for focus ring shimmer.
	lastFocusBorderBucket int64           // Quantized idle focus-ring phase.

	// Demand-driven tick chain state.
	tickGen      uint64       // Generation counter; incremented on tick chain transitions.
	tickRate     tickRate     // Current tick chain speed (idle/slow/fast).
	decorGen     uint64       // Generation counter for decor tick chain.
	decorOn      bool         // Whether decor tick chain is active.
	decorCadence decorCadence // Current decor cadence (idle vs active).

	// Memory-view ambient animation tick chain. Generation bump
	// invalidates any in-flight ticks when exiting memory mode.
	memoryTickGen uint64
	memoryTickOn  bool

	// Centralized cursor blink timer (one-shot at blinkHalfPeriod).
	blinkGen   uint64    // Generation counter; bumped on interactive events to reset blink.
	blinkEpoch time.Time // Wall-clock reference: cursor visible at epoch, phase derived from elapsed time.

	cursorVisible     bool // Computed once per frame in View().
	lastRenderedPhase bool // Phase at last render; drives dirty detection.
	blinkDirty        bool // True when phase changed this frame; drives slot marking.

	// One-shot LSP flush timer (fires lspDebounceInterval after last edit).
	lspFlushGen int // editGeneration at last scheduled flush; prevents duplicates.

	// Frame compositor: line-level cached composition.
	comp      compositor.Compositor
	viewDirty bool

	// Border-only redraw reuse for focused main slots. When only the animated
	// border changes, cached inner content can be reused instead of rerunning
	// the underlying panel render path.
	slotBodyCache       map[compositor.SlotID]string
	slotBorderOnlyDirty map[compositor.SlotID]bool

	// Compositor dirty-detection state.
	prevFocusGrp  compositor.SlotID // Border group of previous focus.
	prevOverlay   overlayState      // Detect overlay transitions.
	prevEditMode  bool              // Detect edit mode transitions.
	prevChord     chordState        // Detect chord hint changes.
	prevLeftRing  int               // Detect left ring cycling.
	prevRightRing int               // Detect right ring cycling.
	prevInputH    int               // Detect input height changes.
	prevHoverKey  [5]int            // Detect tab hover state changes.

	// Mouse hover tracking for LSP hover tooltips.
	hoverMouseLine      int // last buffer line the mouse was over (-1 = none)
	hoverMouseCol       int // last buffer col for LSP request precision
	hoverMouseWordStart int // start col of the word under cursor
	hoverPending        bool
	hoverForPreview     bool // true when hover was triggered over the preview pane

	// Pending hover definition: stashed when the definition response arrives
	// before the hover content. Applied when the hover becomes active.
	pendingHoverSymbol  string
	pendingHoverPkgPath string

	// Document highlight tracking (cursor-rest symbol highlighting).
	highlightLine int // last line a highlight debounce was scheduled for
	highlightCol  int // last col a highlight debounce was scheduled for

	// Command input tracking: true when ':' activated the input panel.
	editCmdInput bool

	// pendingClosePrompt is true while the status bar shows a save-before-close
	// prompt. While active, handleKey intercepts y/n/esc to resolve it.
	pendingClosePrompt bool
	commandApproval    *commandApprovalState
	commandApprovalQ   []*commandapproval.Proposal
	planApproval       *planApprovalState
	planApprovalQ      []*planapproval.Proposal
	layerDecision      *layerDecisionState
	layerDecisionQ     []*msg.LayerDecisionMsg
	// pendingPaneClose is non-zero when the save prompt is for a pane close
	// operation. The value is the PaneID being closed.
	pendingPaneClose pane.PaneID

	// Tiered LRU cache for background tab editor state (undo, buffer, cursor).
	editorCache *editor.EditorCache

	savedActiveTab string // Path of the active tab saved on exitEditMode for restore.

	// Preview panel (read-only file preview, rendered in code panel slot).
	previewPanel *preview.Panel // Independent read-only preview sub-panel.

	// Markdown preview (rendered markdown split-right of the source editor).
	mdPreviewPane          pane.PaneID        // PaneID of the markdown viewer (0 = none).
	mdPreviewPanel         *markdownpkg.Panel // Rendered markdown viewer panel.
	mdPreviewTabHoverClose int                // Close hover on markdown preview tab (-1).
	mdTooltipTab           int                // Tab index showing "View" tooltip (-1 = none).
	mdTooltipPane          pane.PaneID        // Pane whose tab shows the tooltip.
	mdTooltipX             int                // Local X of tooltip for overlay placement.

	// Pane tree: binary split tree tracking editor + preview layout.
	// Each editor pane has its own editor.Model and tab order.
	paneTree    *pane.Node                       // Split tree root (always non-nil in edit mode).
	paneEditors map[pane.PaneID]*editorPaneState // Per-pane editor state.
	focusedPane pane.PaneID                      // Currently focused pane.
	previewPane pane.PaneID                      // PaneID of the preview leaf (0 = no preview).
	paneCounter pane.PaneID                      // Monotonic ID allocator for new panes.

	// Warp points: numbered teleport bookmarks (nil = empty slot).
	warpPoints [warpSlotCount]*WarpPoint

	// Focus state saved when entering the tabs panel, restored on exit.
	preTabsFocus component.FocusID

	width  int
	height int
	ready  bool

	// Cosmetic animations are briefly quiesced after a resize so layout
	// changes render against a stable visual state.
	resizeFreezeUntil time.Time

	// Font detection result cached from New() to avoid repeated fc-list calls.
	nerdFontsDetected bool

	// Event-driven git status watcher. Nil when project root is not a git repo.
	gitWatcher *git.StatusWatcher

	// Safety guard for pre/post-operation snapshots and crash recovery.
	// Nil when project root is not a git repo.
	safetyGuard *git.SafetyGuard

	// Pre-commit pipeline: staged paths and message while awaiting user
	// confirmation from large-file or secret-detection modals.
	// pendingCommitPhase distinguishes large-file (1) vs secret (2) modal.
	pendingCommitPaths   []string
	pendingCommitMessage string
	pendingCommitPhase   int // 0=none, 1=large-file modal, 2=secrets modal

	// Pending sequencer operation: stores params while conflict preview
	// or integration detection modals are shown.
	pendingSeqOp *pendingSequencerOp

	// pendingSyntaxValidation is true while the syntax-warning modal is displayed.
	pendingSyntaxValidation bool

	// Login flow: top-panel overlay component.
	loginPanel            *login.Panel
	oauthSessions         *oauthSessionManager
	pendingAnthropicOAuth *pendingAnthropicOAuthCode
	suppressLoginResult   bool

	// ESC disambiguation: buffer a standalone ESC for a short window so a
	// follow-up rune can be merged into an Alt+rune KeyMsg. This mirrors
	// vim's ttimeoutlen / tmux's escape-time mechanism.
	escPending bool
	escKey     tea.KeyMsg
	escAt      time.Time
	escGen     uint64

	// Guide context usage estimate for agent panel rendering.
	guideContextTokens   int
	guideContextUsage    float64
	agentContextTokens   map[string]int
	agentContextModels   map[string]string
	agentRuntimeContexts map[string]runtimeContextState
	agentReplicaCounts   map[string]int
	engagedAgentID       string // Sticky agent the user is conversing with.
	manualTargetAgent    string

	// Prompt queue: stacks follow-up prompts while the user pauses dispatch.
	promptQueue   queue.Queue
	queueGradient *theme.Gradient

	// Accountant-delta fallback counters. AccountingSnapshotMsg is canonical
	// once live; these keep the status bar responsive before the first snapshot.
	busInputTokens      int
	busOutputTokens     int
	busCacheReadTokens  int
	busCacheWriteTokens int
	busReasoningTokens  int

	// Accountant-sourced snapshot totals (item 52). Refreshed on
	// every AccountingSnapshotMsg which the AccountantBridge emits
	// on a ticker. These are the billable, session-wide totals
	// aggregated by accounting.Accountant — the canonical source.
	// The bus-sourced counters above remain for per-replica
	// per-correlation views the status bar does not own.
	accountantTotalInput     int64
	accountantTotalOutput    int64
	accountantTotalReasoning int64
	accountantTotalRequests  int64
	accountantTotalErrors    int64

	// TUI-local WAL logger for suppressed/non-UI operational errors.
	walLogger *slog.Logger
	walCloser io.Closer

	// Optional plain-text capture of rendered chat updates for UI debugging.
	chatDebugCapture *uiChatDebugCapture
}

type runtimeContextState struct {
	PanelAgentID   string
	RuntimeAgentID string
	ModelID        string
	Tokens         int
	Ephemeral      bool
	UpdatedAt      time.Time
}

// viewRing tracks a cycling list of panels for a swappable layout slot.
// leftRing holds panels sharing a left column; rightRing holds panels sharing a right column.
type viewRing struct {
	panels []component.FocusID
	index  int
}

// current returns the panel that currently occupies the slot.
// Returns 0 (invalid) when the ring is empty.
func (r *viewRing) current() component.FocusID {
	if len(r.panels) == 0 {
		return 0
	}
	return r.panels[r.index]
}

// cycle advances the ring by delta (positive = right, negative = left)
// using wrapping modular arithmetic.
func (r *viewRing) cycle(delta int) {
	n := len(r.panels)
	if n == 0 {
		return
	}
	r.index = ((r.index+delta)%n + n) % n
}

// reset rebuilds the ring with new panels. If the previously active panel
// is present in the new ring, it remains selected; otherwise index resets to 0.
func (r *viewRing) reset(panels []component.FocusID) {
	prev := r.current()
	r.panels = panels
	r.index = 0
	for i, p := range panels {
		if p == prev {
			r.index = i
			return
		}
	}
}

// setTo positions the ring on the given panel. Returns true if found.
func (r *viewRing) setTo(id component.FocusID) bool {
	for i, p := range r.panels {
		if p == id {
			r.index = i
			return true
		}
	}
	return false
}

// empty reports whether the ring has no panels to cycle.
func (r *viewRing) empty() bool { return len(r.panels) == 0 }

// replaceInRing swaps the first occurrence of old with new in the ring's panels.
func replaceInRing(ring *viewRing, old, new component.FocusID) {
	for i, p := range ring.panels {
		if p == old {
			ring.panels[i] = new
			return
		}
	}
}

// isGitPanel reports whether the given focus ID is a git-specific panel.
func isGitPanel(id component.FocusID) bool {
	return id == component.FocusGitPanel || id == component.FocusCommitTree || id == component.FocusDiffView || id == component.FocusDiffFileList
}

// isDiffPaneFocused reports whether the current focus is the diff view panel
// or any of its sub-panes.
func (m *AppModel) isDiffPaneFocused() bool {
	cur := m.focus.Current()
	if cur == component.FocusDiffView {
		return true
	}
	if m.diffViewActive && pane.IsPaneFocus(cur) {
		return true
	}
	return false
}

// ViewMode is the current top-level UI mode. Exactly one is active at a time.
type ViewMode int

const (
	ViewChat   ViewMode = iota // Default: chat + panels.
	ViewMemory                 // Memory forest view in the chat slot.
	ViewEdit                   // Inline vim editor active.
	ViewGit                    // Git explorer + commit tree.
)

// String returns the status bar label for this mode.
func (v ViewMode) String() string {
	switch v {
	case ViewMemory:
		return "MEMR"
	case ViewEdit:
		return "EDIT"
	case ViewGit:
		return "GIT"
	default:
		return "CHAT"
	}
}

// scrollState tracks the spring-driven scroll animation.
type scrollState struct {
	pos     float64           // Smooth position (fractional line offset).
	vel     float64           // Current spring velocity.
	target  float64           // Target position (accumulated from wheel events).
	applied int               // Lines actually dispatched to the panel.
	panel   component.FocusID // Which panel is being scrolled.
}

// bounceState tracks the overscroll bounce animation for a single panel.
type bounceState struct {
	pos   float64           // Current visual offset (fractional lines).
	vel   float64           // Current spring velocity.
	panel component.FocusID // Which panel is bouncing.
}

// New creates a root AppModel from configuration and dependencies.

// Update dispatches incoming messages and ensures the tick/blink/LSP-flush
// chains are running at the appropriate speed for the current activity level.
func (m *AppModel) Update(raw tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.dispatch(raw)
	return m.finishUpdate(raw, cmd)
}

func (m *AppModel) finishUpdate(raw tea.Msg, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	passive := m.passiveUpdate(raw)
	if !passive {
		m.flushChatDebugCapture()
	}
	if passive {
		return m, cmd
	}
	if _, ok := raw.(tea.KeyMsg); ok {
		return m, m.finishInteractiveUpdate(cmd, true)
	}
	if typed, ok := raw.(tea.MouseMsg); ok {
		return m, m.finishMouseUpdate(typed, cmd)
	}
	m.viewDirty = true
	return m, m.postDispatchCmds(cmd, false)
}

func (m *AppModel) passiveUpdate(raw tea.Msg) bool {
	switch raw.(type) {
	case tea.WindowSizeMsg, msg.TickMsg, msg.DecorTickMsg, msg.BlinkMsg, msg.LSPFlushMsg:
		return true
	default:
		return false
	}
}

func (m *AppModel) finishInteractiveUpdate(cmd tea.Cmd, scheduleBlink bool) tea.Cmd {
	m.viewDirty = true
	m.blinkGen++
	m.blinkEpoch = time.Now()
	return m.postDispatchCmds(cmd, scheduleBlink)
}

func (m *AppModel) finishMouseUpdate(typed tea.MouseMsg, cmd tea.Cmd) tea.Cmd {
	if typed.Action == tea.MouseActionMotion {
		m.viewDirty = true
		return m.postDispatchCmds(cmd, false)
	}
	return m.finishInteractiveUpdate(cmd, true)
}

// postDispatchCmds collects tick, blink, and LSP-flush commands that may be
// needed after a non-tick dispatch. scheduleBlink is true only for
// interactive events (key/mouse) that bumped blinkGen.
func (m *AppModel) postDispatchCmds(dispatchCmd tea.Cmd, scheduleBlink bool) tea.Cmd {
	cmds := make([]tea.Cmd, 0, 5)
	if dispatchCmd != nil {
		cmds = append(cmds, dispatchCmd)
	}
	if tc := m.ensureTickAfterDispatch(); tc != nil {
		cmds = append(cmds, tc)
	}
	if dc := m.ensureDecorTickAfterDispatch(); dc != nil {
		cmds = append(cmds, dc)
	}
	if scheduleBlink {
		if bc := m.ensureBlinkAfterDispatch(); bc != nil {
			cmds = append(cmds, bc)
		}
	}
	if lf := m.ensureLSPFlush(); lf != nil {
		cmds = append(cmds, lf)
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

// dispatch is the main message handler.
type appMsgDispatchRoute func(*AppModel, tea.Msg) (tea.Model, tea.Cmd)

func appMsgCmdRoute[T any](fn func(*AppModel, T) tea.Cmd) appMsgDispatchRoute {
	return func(m *AppModel, raw tea.Msg) (tea.Model, tea.Cmd) {
		return m, fn(m, raw.(T))
	}
}

func appMsgModelRoute[T any](fn func(*AppModel, T) (tea.Model, tea.Cmd)) appMsgDispatchRoute {
	return func(m *AppModel, raw tea.Msg) (tea.Model, tea.Cmd) {
		return fn(m, raw.(T))
	}
}

func appMsgStateRoute[T any](fn func(*AppModel, T)) appMsgDispatchRoute {
	return func(m *AppModel, raw tea.Msg) (tea.Model, tea.Cmd) {
		fn(m, raw.(T))
		return m, nil
	}
}

var appMsgDispatchRoutes = map[reflect.Type]appMsgDispatchRoute{
	reflect.TypeFor[tea.WindowSizeMsg](): appMsgCmdRoute((*AppModel).handleResize),
	reflect.TypeFor[tea.KeyMsg]():        appMsgModelRoute((*AppModel).handleKey),
	reflect.TypeFor[tea.MouseMsg]():      appMsgCmdRoute((*AppModel).handleMouse),
	reflect.TypeFor[msg.SubmitPromptMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.SubmitPromptMsg) tea.Cmd {
		if m.editCmdInput {
			m.editCmdInput = false
			m.input.SetLineStyler(nil)
			exCmd := m.handleExCommand(typed.Text)
			if m.viewMode == ViewEdit {
				m.focusCodePanel()
				m.syncFocusState()
			}
			return exCmd
		}
		if m.viewMode == ViewEdit && isInlineExCommand(typed.Text) {
			exCmd := m.handleExCommand(typed.Text)
			if m.viewMode == ViewEdit {
				m.focusCodePanel()
				m.syncFocusState()
			}
			return exCmd
		}
		if cmd, ok := parseChatCommand(typed.Text); ok {
			return m.handleChatCommand(cmd, typed)
		}
		return m.handleSubmit(typed)
	}),
	reflect.TypeFor[msg.LoginResultMsg]():     appMsgCmdRoute((*AppModel).handleLoginResult),
	reflect.TypeFor[msg.ModelChangeMsg]():     appMsgCmdRoute((*AppModel).handleModelChange),
	reflect.TypeFor[msg.ModelSwapResultMsg](): appMsgCmdRoute((*AppModel).handleModelSwapResult),
	reflect.TypeFor[oauthSessionStartedMsg](): appMsgCmdRoute((*AppModel).handleOAuthSessionStarted),
	reflect.TypeFor[msg.AuthStatusMsg](): appMsgStateRoute(func(m *AppModel, typed msg.AuthStatusMsg) {
		for provider, available := range typed.Providers {
			m.statusBar.SetAuthStatus(provider, available)
		}
		if method, ok := typed.Methods["openai"]; ok {
			m.agentPanel.SetOpenAIAuthMethod(method)
		}
	}),
	reflect.TypeFor[msg.InterruptMsg]():        appMsgCmdRoute(func(m *AppModel, _ msg.InterruptMsg) tea.Cmd { return m.handleInterrupt() }),
	reflect.TypeFor[msg.QuitConfirmMsg]():      appMsgCmdRoute(func(m *AppModel, _ msg.QuitConfirmMsg) tea.Cmd { return m.handleQuit() }),
	reflect.TypeFor[msg.TickMsg]():             appMsgCmdRoute((*AppModel).handleTick),
	reflect.TypeFor[msg.DecorTickMsg]():        appMsgCmdRoute((*AppModel).handleDecorTick),
	reflect.TypeFor[msg.MemoryCanvasTickMsg](): appMsgCmdRoute((*AppModel).handleMemoryCanvasTick),
	reflect.TypeFor[msg.BlinkMsg]():            appMsgCmdRoute((*AppModel).handleBlink),
	reflect.TypeFor[msg.LSPFlushMsg]():         appMsgCmdRoute((*AppModel).handleLSPFlush),
	reflect.TypeFor[msg.QueueAdvanceMsg]():     appMsgCmdRoute(func(m *AppModel, typed msg.QueueAdvanceMsg) tea.Cmd { return m.dispatchQueueEntries(typed.EntryIDs) }),
	reflect.TypeFor[msg.FocusPanelMsg]():       appMsgCmdRoute((*AppModel).handleFocusPanel),
	reflect.TypeFor[msg.PlanUpdateMsg]():       appMsgCmdRoute((*AppModel).handlePlanUpdate),
	reflect.TypeFor[msg.ClaimPresentationMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.ClaimPresentationMsg) tea.Cmd {
		return m.propagate(typed)
	}),
	reflect.TypeFor[msg.PlanViewToggleMsg](): appMsgCmdRoute(func(m *AppModel, _ msg.PlanViewToggleMsg) tea.Cmd {
		return m.handlePlanViewToggle()
	}),
	reflect.TypeFor[msg.PipelineStateMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.PipelineStateMsg) tea.Cmd {
		comp, cmd := m.agentPanel.Update(typed)
		m.agentPanel = comp.(*agentpkg.Model)
		m.markSlotDirty(compositor.SlotLeft)
		return cmd
	}),
	reflect.TypeFor[msg.VariantStateMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.VariantStateMsg) tea.Cmd {
		comp, cmd := m.agentPanel.Update(typed)
		m.agentPanel = comp.(*agentpkg.Model)
		m.markSlotDirty(compositor.SlotLeft)
		return cmd
	}),
	reflect.TypeFor[msg.ClaimsProjectionMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.ClaimsProjectionMsg) tea.Cmd {
		comp, cmd := m.agentPanel.Update(typed)
		m.agentPanel = comp.(*agentpkg.Model)
		m.markSlotDirty(compositor.SlotLeft)
		return cmd
	}),
	reflect.TypeFor[msg.SessionEventMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.SessionEventMsg) tea.Cmd {
		if m.claimsBridge != nil && typed.Event != nil {
			m.claimsBridge.SwitchSession(typed.Event.SessionID)
		}
		return m.propagate(typed)
	}),
	reflect.TypeFor[msg.OpenEditorMsg]():  appMsgCmdRoute((*AppModel).handleOpenEditor),
	reflect.TypeFor[msg.CloseEditorMsg](): appMsgCmdRoute(func(m *AppModel, _ msg.CloseEditorMsg) tea.Cmd { return m.handleCloseEditor() }),
	reflect.TypeFor[msg.FileOpenMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.FileOpenMsg) tea.Cmd {
		// Dismiss preview only when opening a NEW file (not already a tab).
		// Tab switches (shift+tab) should keep the preview visible.
		if m.hasPreview() && !m.isExistingTab(typed.Path) {
			m.dismissPreview()
		}
		return m.handleFileOpen(typed)
	}),
	reflect.TypeFor[msg.FilePreviewMsg](): appMsgCmdRoute((*AppModel).handleFilePreview),
	reflect.TypeFor[msg.FileTreeEntryCreatedMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.FileTreeEntryCreatedMsg) tea.Cmd {
		m.nudgeGitWatcher()
		if typed.IsDir {
			return nil
		}
		return func() tea.Msg { return msg.FileOpenMsg{Path: typed.Path} }
	}),
	reflect.TypeFor[msg.FileTreeEntryRenamedMsg](): appMsgStateRoute(func(m *AppModel, _ msg.FileTreeEntryRenamedMsg) {
		m.nudgeGitWatcher()
	}),
	reflect.TypeFor[msg.FileTreeEntryDeletedMsg](): appMsgStateRoute(func(m *AppModel, typed msg.FileTreeEntryDeletedMsg) {
		m.nudgeGitWatcher()
		if m.codePanel.FilePath() == typed.Path {
			m.codePanel.ClearFile()
		}
		if m.focusedEditor().FilePath() == typed.Path {
			m.focusedEditor().ClearFile()
		}
		m.fileTree.SetActiveFile("")
	}),
	reflect.TypeFor[msg.GitStatusMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.GitStatusMsg) tea.Cmd {
		m.fileTree.SetGitStatus(typed.StatusMap, typed.TrackedSet, typed.TrackedDirs)
		cmds := []tea.Cmd{m.gitWatchCmd(), m.detectSequencerStateCmd()}
		if m.viewMode == ViewGit {
			cmds = append(cmds, m.gitPanel.LoadData(), m.loadGitBranchesCmd())
			if m.commitTree.InCommitView() {
				defaultBranch := m.commitTree.GetDefaultBranch()
				cmds = append(cmds, m.loadBranchDAGCmd(m.commitTree.ActiveBranch(), defaultBranch))
			}
		}
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[gitQuickStatusMsg](): appMsgStateRoute(func(m *AppModel, typed gitQuickStatusMsg) {
		if m.commitTree != nil {
			m.commitTree.SetWorkingTreeStatus(typed.dirty, typed.conflicts)
			m.commitTree.SetHasIndexStaged(typed.hasIndexStaged)
		}
		if m.gitPanel != nil {
			m.gitPanel.SetHasStash(typed.hasStash)
		}
	}),
	reflect.TypeFor[gitBranchesLoadedMsg](): appMsgCmdRoute(func(m *AppModel, typed gitBranchesLoadedMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.SetBranches(typed.branches, typed.defaultBranch)
			m.commitTree.SetWorkingTreeStatus(typed.dirty, typed.conflicts)
			m.commitTree.SetHasIndexStaged(typed.hasIndexStaged)
		}
		if m.gitPanel != nil {
			m.gitPanel.SetHasStash(typed.hasStash)
		}
		return tea.Batch(m.computeDivergenceBatchCmd(), m.loadBranchStashesCmd())
	}),
	reflect.TypeFor[msg.DivergenceLoadedMsg](): appMsgStateRoute(func(m *AppModel, typed msg.DivergenceLoadedMsg) {
		if m.gitPanel != nil && len(typed.Info) > 0 {
			m.gitPanel.SetDivergence(typed.Info)
		}
	}),
	reflect.TypeFor[branchStashesLoadedMsg](): appMsgStateRoute(func(m *AppModel, typed branchStashesLoadedMsg) {
		if m.gitPanel != nil && len(typed.stashes) > 0 {
			m.gitPanel.SetBranchStashes(typed.stashes)
		}
	}),
	reflect.TypeFor[msg.SequencerFileStateMsg](): appMsgStateRoute(func(m *AppModel, typed msg.SequencerFileStateMsg) {
		if typed.State != nil && typed.State.Active {
			step := fmt.Sprintf("%d/%d", typed.State.CurrentStep+1, typed.State.TotalSteps)
			prompt := strings.ToTitle(typed.State.Type[:1]) + typed.State.Type[1:] + "ing " + step
			if typed.State.OntoRef != "" {
				prompt += " onto " + typed.State.OntoRef
			}
			m.statusBar.SetPrompt(prompt)
			m.statusBar.SetMode(strings.ToUpper(typed.State.Type))
			return
		}
		m.statusBar.ClearPrompt()
	}),
	reflect.TypeFor[committree.BranchSelectedMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.BranchSelectedMsg) tea.Cmd {
		if m.viewMode != ViewGit || m.gitBus == nil {
			return nil
		}
		defaultBranch := ""
		if m.commitTree != nil {
			defaultBranch = m.commitTree.GetDefaultBranch()
		}
		return m.loadBranchDAGCmd(typed.Name, defaultBranch)
	}),
	reflect.TypeFor[gitpanel.BranchCheckedOutMsg](): appMsgCmdRoute(func(m *AppModel, typed gitpanel.BranchCheckedOutMsg) tea.Cmd {
		m.statusBar.SetFlash("Switched to " + typed.Name)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		if m.gitBus != nil {
			bus := m.gitBus
			name := typed.Name
			cmds = append(cmds, func() tea.Msg {
				if bus.HasBranchStash(name) {
					stashes, err := bus.ListBranchStashes()
					if err == nil {
						if metas, ok := stashes[name]; ok && len(metas) > 0 {
							return msg.BranchStashAvailableMsg{Meta: metas[0]}
						}
					}
				}
				return nil
			})
		}
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[gitpanel.CommitCheckedOutMsg](): appMsgCmdRoute(func(m *AppModel, typed gitpanel.CommitCheckedOutMsg) tea.Cmd {
		m.statusBar.SetFlash("Checked out " + typed.Hash[:min(len(typed.Hash), 8)])
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[gitpanel.BranchCheckoutBlockedMsg](): appMsgStateRoute(func(m *AppModel, typed gitpanel.BranchCheckoutBlockedMsg) {
		m.statusBar.SetFlash(typed.Reason)
	}),
	reflect.TypeFor[committree.BranchSwitchMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.BranchSwitchMsg) tea.Cmd {
		if m.gitBus == nil {
			return nil
		}
		bus := m.gitBus
		name := typed.Name
		return func() tea.Msg {
			branches, bErr := bus.ListBranches()
			if bErr == nil {
				for _, b := range branches {
					if !b.IsHead {
						continue
					}
					statuses, _, _ := bus.UncommittedFileStatuses()
					if len(statuses) > 0 {
						paths := make([]string, 0, len(statuses))
						for p := range statuses {
							paths = append(paths, p)
						}
						_, _ = bus.StashForBranch(b.Name, paths)
					}
					break
				}
			}
			if err := bus.CheckoutBranch(name); err != nil {
				return gitpanel.BranchCheckoutBlockedMsg{Reason: err.Error()}
			}
			return gitpanel.BranchCheckedOutMsg{Name: name}
		}
	}),
	reflect.TypeFor[committree.BranchDeleteMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.BranchDeleteMsg) tea.Cmd {
		if m.gitBus == nil {
			return nil
		}
		bus := m.gitBus
		name := typed.Name
		return func() tea.Msg {
			if err := bus.DeleteBranch(name); err != nil {
				return branchDeleteFailedMsg{reason: err.Error()}
			}
			return branchDeletedMsg{name: name}
		}
	}),
	reflect.TypeFor[branchDeletedMsg](): appMsgCmdRoute(func(m *AppModel, typed branchDeletedMsg) tea.Cmd {
		m.statusBar.SetFlash("Deleted branch " + typed.name)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[branchDeleteFailedMsg](): appMsgStateRoute(func(m *AppModel, typed branchDeleteFailedMsg) {
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[mergeBranchDoneMsg](): appMsgCmdRoute(func(m *AppModel, typed mergeBranchDoneMsg) tea.Cmd {
		m.exitMergeDiffView()
		flash := "Merged " + typed.source + " → " + typed.target
		if typed.deleted {
			flash += " (deleted " + typed.source + ")"
		}
		if typed.deleteErr != "" {
			flash += " (delete failed: " + typed.deleteErr + ")"
		}
		m.statusBar.SetFlash(flash)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[mergeBranchFailedMsg](): appMsgStateRoute(func(m *AppModel, typed mergeBranchFailedMsg) {
		if m.mergeDiffView != nil {
			m.mergeDiffView.SetMergeError("Merge failed: " + typed.reason)
			return
		}
		m.statusBar.SetFlash("Merge failed: " + typed.reason)
	}),
	reflect.TypeFor[committree.CreateBranchRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.CreateBranchRequestMsg) tea.Cmd {
		if m.gitBus == nil {
			return nil
		}
		if typed.AtHash == "" {
			m.commitTree.RecordBranchParent(typed.Name, typed.ParentBranch)
		}
		bus := m.gitBus
		name, parent, atHash := typed.Name, typed.ParentBranch, typed.AtHash
		return func() tea.Msg {
			hash := atHash
			if hash == "" {
				var err error
				hash, err = bus.BranchTipHash(parent)
				if err != nil {
					return branchCreateFailedMsg{reason: err.Error()}
				}
			}
			if err := bus.CreateBranch(name, hash); err != nil {
				return branchCreateFailedMsg{reason: err.Error()}
			}
			return branchCreatedMsg{name: name}
		}
	}),
	reflect.TypeFor[branchCreatedMsg](): appMsgCmdRoute(func(m *AppModel, typed branchCreatedMsg) tea.Cmd {
		m.statusBar.SetFlash("Created branch " + typed.name)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[branchCreateFailedMsg](): appMsgStateRoute(func(m *AppModel, typed branchCreateFailedMsg) {
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[committree.CommitRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.CommitRequestMsg) tea.Cmd {
		if m.gitBus == nil || m.gitPanel == nil {
			return nil
		}
		paths := m.gitPanel.StagedFilePaths()
		if len(paths) == 0 {
			m.statusBar.SetFlash("No files staged")
			return nil
		}
		return m.preCommitCheckCmd(paths, typed.Message)
	}),
	reflect.TypeFor[msg.PreCommitCleanMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.PreCommitCleanMsg) tea.Cmd {
		bus := m.gitBus
		paths, message := typed.Paths, typed.Message
		return func() tea.Msg {
			if err := bus.CommitFiles(paths, message); err != nil {
				return commitFailedMsg{reason: err.Error()}
			}
			return commitSucceededMsg{message: message}
		}
	}),
	reflect.TypeFor[msg.LargeFilesDetectedMsg](): appMsgStateRoute(func(m *AppModel, typed msg.LargeFilesDetectedMsg) {
		items := make([]modal.ListModalItem, len(typed.Files))
		for i, f := range typed.Files {
			badge := formatFileSize(f.Size)
			if f.Binary {
				badge = "BIN"
			}
			items[i] = modal.ListModalItem{
				Label: f.Path,
				Badge: badge,
				Color: m.config.Theme().Palette.Peach,
			}
		}
		footer := fmt.Sprintf("%d file(s) flagged", len(typed.Files))
		lm := modal.NewListModal("Large/Binary Files Detected", items, footer,
			[]string{"Continue", "Cancel"}, m.config.Theme())
		m.modalOverlay.Push(lm)
		m.pendingCommitPaths = typed.Paths
		m.pendingCommitMessage = typed.Message
		m.pendingCommitPhase = 1
	}),
	reflect.TypeFor[msg.SecretsDetectedMsg](): appMsgStateRoute(func(m *AppModel, typed msg.SecretsDetectedMsg) {
		items := make([]modal.ListModalItem, len(typed.Findings))
		for i, f := range typed.Findings {
			items[i] = modal.ListModalItem{
				Label:  fmt.Sprintf("%s:%d", f.Path, f.Line),
				Detail: f.PatternName,
				Badge:  f.Snippet,
				Color:  m.config.Theme().Palette.Error,
			}
		}
		footer := fmt.Sprintf("%d secret(s) detected", len(typed.Findings))
		lm := modal.NewListModal("Potential Secrets Detected", items, footer,
			[]string{"Continue Anyway", "Cancel"}, m.config.Theme())
		m.modalOverlay.Push(lm)
		m.pendingCommitPaths = typed.Paths
		m.pendingCommitMessage = typed.Message
		m.pendingCommitPhase = 2
	}),
	reflect.TypeFor[commitSucceededMsg](): appMsgCmdRoute(func(m *AppModel, typed commitSucceededMsg) tea.Cmd {
		m.statusBar.SetFlash("Committed: " + typed.message)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		if m.commitTree != nil {
			_, doneCmd := m.commitTree.Update(committree.CommitDoneMsg{OK: true, Message: typed.message})
			cmds = append(cmds, doneCmd)
		}
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[commitFailedMsg](): appMsgCmdRoute(func(m *AppModel, typed commitFailedMsg) tea.Cmd {
		m.statusBar.SetFlash(typed.reason)
		if m.commitTree == nil {
			return nil
		}
		_, doneCmd := m.commitTree.Update(committree.CommitDoneMsg{OK: false, Message: typed.reason})
		return doneCmd
	}),
	reflect.TypeFor[gitpanel.StashRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed gitpanel.StashRequestMsg) tea.Cmd {
		if m.gitBus == nil || m.commitTree == nil {
			return nil
		}
		m.commitTree.SetLoadingMessage(fmt.Sprintf("Stashing %d files...", typed.Count))
		bus := m.gitBus
		paths := typed.Paths
		count := typed.Count
		return func() tea.Msg {
			if err := bus.StashFiles(paths); err != nil {
				return stashFailedMsg{reason: err.Error()}
			}
			return stashSucceededMsg{count: count}
		}
	}),
	reflect.TypeFor[stashSucceededMsg](): appMsgCmdRoute(func(m *AppModel, typed stashSucceededMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(fmt.Sprintf("Stashed %d files", typed.count))
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[stashFailedMsg](): appMsgStateRoute(func(m *AppModel, typed stashFailedMsg) {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[gitpanel.UnstashRequestMsg](): appMsgCmdRoute(func(m *AppModel, _ gitpanel.UnstashRequestMsg) tea.Cmd {
		if m.gitBus == nil || m.commitTree == nil {
			return nil
		}
		m.commitTree.SetLoadingMessage("Unstashing files...")
		bus := m.gitBus
		return func() tea.Msg {
			if err := bus.UnstashFiles(); err != nil {
				return unstashFailedMsg{reason: err.Error()}
			}
			return unstashSucceededMsg{}
		}
	}),
	reflect.TypeFor[unstashSucceededMsg](): appMsgCmdRoute(func(m *AppModel, _ unstashSucceededMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash("Unstashed files")
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[unstashFailedMsg](): appMsgStateRoute(func(m *AppModel, typed unstashFailedMsg) {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[committree.PullBranchMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.PullBranchMsg) tea.Cmd {
		if m.gitBus == nil || m.commitTree == nil {
			return nil
		}
		m.commitTree.SetLoadingMessage("Pulling " + typed.Name + "...")
		bus := m.gitBus
		name := typed.Name
		return func() tea.Msg {
			if err := bus.PullBranch(name, ""); err != nil {
				return pullFailedMsg{reason: err.Error()}
			}
			return pullSucceededMsg{name: name}
		}
	}),
	reflect.TypeFor[pullSucceededMsg](): appMsgCmdRoute(func(m *AppModel, typed pullSucceededMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash("Pulled branch " + typed.name)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[pullFailedMsg](): appMsgStateRoute(func(m *AppModel, typed pullFailedMsg) {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[committree.PushBranchMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.PushBranchMsg) tea.Cmd {
		if m.gitBus == nil || m.commitTree == nil {
			return nil
		}
		m.commitTree.SetLoadingMessage("Pushing " + typed.Name + "...")
		bus := m.gitBus
		name := typed.Name
		return func() tea.Msg {
			if err := bus.PushBranch(name, ""); err != nil {
				return pushFailedMsg{reason: err.Error()}
			}
			return pushSucceededMsg{name: name}
		}
	}),
	reflect.TypeFor[pushSucceededMsg](): appMsgCmdRoute(func(m *AppModel, typed pushSucceededMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash("Pushed branch " + typed.name)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[pushFailedMsg](): appMsgStateRoute(func(m *AppModel, typed pushFailedMsg) {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[committree.ResetRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.ResetRequestMsg) tea.Cmd {
		if m.gitBus == nil || m.commitTree == nil {
			return nil
		}
		m.commitTree.SetLoadingMessage("Resetting (" + typed.Mode + ")...")
		bus := m.gitBus
		hash, modeStr := typed.Hash, typed.Mode
		mode := git.ResetMode(0)
		switch modeStr {
		case "hard":
			mode = git.ResetHard
		case "mixed":
			mode = git.ResetMixed
		case "soft":
			mode = git.ResetSoft
		}
		return func() tea.Msg {
			if err := bus.Reset(hash, mode); err != nil {
				return resetFailedMsg{reason: err.Error()}
			}
			return resetSucceededMsg{mode: modeStr}
		}
	}),
	reflect.TypeFor[resetSucceededMsg](): appMsgCmdRoute(func(m *AppModel, typed resetSucceededMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash("Reset (" + typed.mode + ") succeeded")
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[resetFailedMsg](): appMsgStateRoute(func(m *AppModel, typed resetFailedMsg) {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[committree.RevertRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.RevertRequestMsg) tea.Cmd {
		if m.gitBus == nil || m.commitTree == nil {
			return nil
		}
		m.commitTree.SetLoadingMessage("Reverting...")
		bus := m.gitBus
		hash := typed.Hash
		return func() tea.Msg {
			if err := bus.Revert(hash); err != nil {
				return revertFailedMsg{reason: err.Error()}
			}
			return revertSucceededMsg{hash: hash}
		}
	}),
	reflect.TypeFor[revertSucceededMsg](): appMsgCmdRoute(func(m *AppModel, typed revertSucceededMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash("Reverted " + typed.hash[:min(len(typed.hash), 7)])
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[revertFailedMsg](): appMsgStateRoute(func(m *AppModel, typed revertFailedMsg) {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[committree.CommitCheckoutRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.CommitCheckoutRequestMsg) tea.Cmd {
		if m.gitBus == nil {
			return nil
		}
		bus := m.gitBus
		hash := typed.Hash
		return func() tea.Msg {
			if err := bus.CheckoutCommit(hash); err != nil {
				return commitCheckoutFailedMsg{reason: err.Error()}
			}
			return commitCheckoutSucceededMsg{hash: hash}
		}
	}),
	reflect.TypeFor[commitCheckoutSucceededMsg](): appMsgCmdRoute(func(m *AppModel, typed commitCheckoutSucceededMsg) tea.Cmd {
		m.statusBar.SetFlash("Checked out " + typed.hash[:min(len(typed.hash), 8)])
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[commitCheckoutFailedMsg](): appMsgStateRoute(func(m *AppModel, typed commitCheckoutFailedMsg) {
		m.statusBar.SetFlash(typed.reason)
	}),
	reflect.TypeFor[committree.CherryPickRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.CherryPickRequestMsg) tea.Cmd {
		if m.gitBus == nil || m.commitTree == nil {
			return nil
		}
		m.commitTree.SetLoadingMessage("Checking cherry-pick...")
		m.mergeLabels = nil
		hashes, target := typed.Hashes, typed.TargetBranch
		m.pendingSeqOp = &pendingSequencerOp{op: "cherry-pick", hashes: hashes, target: target}
		return m.detectIntegrationCmd(hashes, target, nil)
	}),
	reflect.TypeFor[committree.RebaseStartMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.RebaseStartMsg) tea.Cmd {
		if m.gitBus == nil || m.commitTree == nil {
			return nil
		}
		m.commitTree.SetLoadingMessage("Checking rebase...")
		m.mergeLabels = nil
		onto := typed.OntoBranch
		plan := make([]git.RebasePlanEntry, len(typed.Plan))
		for i, p := range typed.Plan {
			plan[i] = git.RebasePlanEntry{Action: git.RebaseAction(p.Action), Hash: p.Hash}
		}
		m.pendingSeqOp = &pendingSequencerOp{
			op: "rebase", target: onto, sourceBranch: typed.SourceBranch, plan: plan,
		}
		return m.conflictPreviewRebaseCmd(onto, typed.SourceBranch)
	}),
	reflect.TypeFor[sequencerResultMsg](): appMsgCmdRoute(func(m *AppModel, typed sequencerResultMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		status := typed.status
		if status != nil && status.State == git.SeqConflict {
			entries := make([]conflictview.ConflictFileEntry, len(status.Conflicts))
			for i, c := range status.Conflicts {
				entries[i] = conflictview.ConflictFileEntry{
					Path:          c.Path,
					Type:          conflictview.ConflictType(c.Type),
					OursHash:      c.OursHash.String(),
					TheirsHash:    c.TheirsHash.String(),
					BaseHash:      c.BaseHash.String(),
					MergedContent: c.MergedContent,
				}
			}
			data := conflictview.ConflictData{
				Op:         int(status.Op),
				Total:      status.TotalSteps,
				Current:    status.CurrentStep,
				Hash:       status.CurrentHash,
				Subject:    status.Subject,
				SourceName: status.SourceName,
				DestName:   status.DestName,
				Entries:    entries,
			}
			m.enterConflictView(data)
			m.statusBar.SetFlash("Conflict at step " + fmt.Sprintf("%d/%d", status.CurrentStep+1, status.TotalSteps))
			return nil
		}
		m.exitConflictView()
		m.nudgeGitWatcher()
		m.finishMergeIfPending()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		if m.commitTree != nil {
			branch := m.commitTree.ActiveBranch()
			if branch != "" {
				cmds = append(cmds, m.loadBranchDAGCmd(branch, m.commitTree.GetDefaultBranch()))
			}
		}
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[sequencerFailedMsg](): appMsgCmdRoute(func(m *AppModel, typed sequencerFailedMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.exitConflictView()
		m.statusBar.SetFlash(typed.reason)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[sequencerAbortedMsg](): appMsgCmdRoute(func(m *AppModel, _ sequencerAbortedMsg) tea.Cmd {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.exitConflictView()
		m.mergeDeleteSource = false
		m.statusBar.SetFlash("Sequencer aborted")
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return tea.Batch(cmds...)
	}),
	reflect.TypeFor[sequencerAbortFailedMsg](): appMsgStateRoute(func(m *AppModel, typed sequencerAbortFailedMsg) {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
		m.nudgeGitWatcher()
	}),
	reflect.TypeFor[conflictview.ConflictResolveFileMsg](): appMsgCmdRoute(func(m *AppModel, typed conflictview.ConflictResolveFileMsg) tea.Cmd {
		bus := m.gitBus
		path := typed.Path
		res := int(typed.Resolution)
		oursHash := typed.OursHash
		theirsHash := typed.TheirsHash
		return func() tea.Msg {
			if err := bus.ResolveConflictFile(path, res, oursHash, theirsHash); err != nil {
				return conflictResolveFailedMsg{path: path, reason: err.Error()}
			}
			return conflictResolveWrittenMsg{path: path}
		}
	}),
	reflect.TypeFor[conflictview.ConflictWriteContentMsg](): appMsgCmdRoute(func(m *AppModel, typed conflictview.ConflictWriteContentMsg) tea.Cmd {
		bus := m.gitBus
		return func() tea.Msg {
			if err := bus.WriteWorktreeFile(typed.Path, typed.Content); err != nil {
				return conflictResolveFailedMsg{path: typed.Path, reason: err.Error()}
			}
			return conflictResolveWrittenMsg{path: typed.Path}
		}
	}),
	reflect.TypeFor[conflictview.SyntaxValidationRequestMsg](): appMsgCmdRoute(func(m *AppModel, _ conflictview.SyntaxValidationRequestMsg) tea.Cmd {
		if m.conflictView == nil {
			return nil
		}
		entries := m.conflictView.Entries()
		return func() tea.Msg {
			warnings := conflictview.ValidateResolvedFiles(entries)
			return conflictview.SyntaxValidationResultMsg{Warnings: warnings}
		}
	}),
	reflect.TypeFor[conflictview.SyntaxValidationResultMsg](): appMsgCmdRoute(func(m *AppModel, typed conflictview.SyntaxValidationResultMsg) tea.Cmd {
		if len(typed.Warnings) == 0 || typed.Proceed {
			if m.conflictView != nil {
				m.conflictView.SetLoading(true)
			}
			bus := m.gitBus
			return func() tea.Msg {
				status, err := bus.SequencerContinue()
				if err != nil {
					return sequencerFailedMsg{reason: err.Error()}
				}
				return sequencerResultMsg{status: status}
			}
		}
		items := make([]modal.ListModalItem, len(typed.Warnings))
		for i, w := range typed.Warnings {
			items[i] = modal.ListModalItem{
				Label:  w.Path,
				Detail: fmt.Sprintf("%d parse error(s)", len(w.Errors)),
				Badge:  "ERR",
				Color:  m.config.Theme().Palette.Error,
			}
		}
		lm := modal.NewListModal("Syntax Errors in Resolved Files", items,
			"Files may contain invalid syntax after resolution.",
			[]string{"Continue Anyway", "Cancel"}, m.config.Theme())
		m.modalOverlay.Push(lm)
		m.overlay = overlayModal
		m.pendingSyntaxValidation = true
		return nil
	}),
	reflect.TypeFor[conflictview.SequencerContinueMsg](): appMsgCmdRoute(func(m *AppModel, _ conflictview.SequencerContinueMsg) tea.Cmd {
		if m.conflictView != nil {
			m.conflictView.SetLoading(true)
		}
		bus := m.gitBus
		return func() tea.Msg {
			status, err := bus.SequencerContinue()
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}
	}),
	reflect.TypeFor[conflictview.SequencerBypassMsg](): appMsgCmdRoute(func(m *AppModel, _ conflictview.SequencerBypassMsg) tea.Cmd {
		bus := m.gitBus
		return func() tea.Msg {
			status, err := bus.SequencerBypass()
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}
	}),
	reflect.TypeFor[conflictview.SequencerAbortMsg](): appMsgCmdRoute(func(m *AppModel, _ conflictview.SequencerAbortMsg) tea.Cmd {
		return m.preserveAbortCmd("sequencer")
	}),
	reflect.TypeFor[conflictview.BaseContentRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed conflictview.BaseContentRequestMsg) tea.Cmd {
		return m.fetchBaseContentCmd(typed.Path, typed.BaseHash)
	}),
	reflect.TypeFor[conflictview.BaseContentResponseMsg](): appMsgStateRoute(func(m *AppModel, typed conflictview.BaseContentResponseMsg) {
		if m.conflictView != nil && typed.Err == nil {
			m.conflictView.SetBaseContent(typed.Path, typed.Content)
		}
	}),
	reflect.TypeFor[conflictview.StepPreviewRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed conflictview.StepPreviewRequestMsg) tea.Cmd {
		return m.fetchStepPreviewCmd(typed.Hash)
	}),
	reflect.TypeFor[conflictview.StepPreviewResponseMsg](): appMsgStateRoute(func(m *AppModel, typed conflictview.StepPreviewResponseMsg) {
		if m.conflictView != nil && typed.Preview != nil {
			m.conflictView.SetStepPreview(typed.Preview)
		}
	}),
	reflect.TypeFor[conflictview.SequencerUndoStepMsg](): appMsgCmdRoute(func(m *AppModel, _ conflictview.SequencerUndoStepMsg) tea.Cmd {
		return m.sequencerUndoStepCmd()
	}),
	reflect.TypeFor[conflictResolveWrittenMsg](): appMsgStateRoute(func(m *AppModel, _ conflictResolveWrittenMsg) {
		m.nudgeGitWatcher()
	}),
	reflect.TypeFor[conflictResolveFailedMsg](): appMsgStateRoute(func(m *AppModel, typed conflictResolveFailedMsg) {
		m.statusBar.SetFlash("Resolve failed: " + typed.reason)
	}),
	reflect.TypeFor[msg.ConflictPreviewMsg]():      appMsgCmdRoute((*AppModel).handleConflictPreview),
	reflect.TypeFor[msg.IntegrationDetectedMsg]():  appMsgCmdRoute((*AppModel).handleIntegrationDetected),
	reflect.TypeFor[msg.AbortPreservedMsg]():       appMsgCmdRoute((*AppModel).handleAbortPreserved),
	reflect.TypeFor[msg.BranchStashAvailableMsg](): appMsgCmdRoute((*AppModel).handleBranchStashAvailable),
	reflect.TypeFor[msg.BranchStashRestoredMsg](): appMsgStateRoute(func(m *AppModel, typed msg.BranchStashRestoredMsg) {
		if typed.Err != nil {
			m.statusBar.SetFlash("Stash restore failed: " + typed.Err.Error())
			return
		}
		m.statusBar.SetFlash("Branch stash restored")
		m.nudgeGitWatcher()
	}),
	reflect.TypeFor[editor.ConflictResolvedMsg](): appMsgStateRoute(func(m *AppModel, typed editor.ConflictResolvedMsg) {
		if m.conflictViewActive && m.conflictView != nil {
			m.conflictView.MarkResolvedByPath(typed.Path)
		}
	}),
	reflect.TypeFor[msg.GitOpEventMsg](): appMsgStateRoute(func(m *AppModel, _ msg.GitOpEventMsg) {
		m.nudgeGitWatcher()
	}),
	reflect.TypeFor[gitBranchDAGLoadedMsg](): appMsgStateRoute(func(m *AppModel, typed gitBranchDAGLoadedMsg) {
		if m.commitTree != nil {
			m.commitTree.SetDAGNodesWithStats(typed.nodes, typed.stats, typed.graphRows, typed.maxGraphLane)
		}
	}),
	reflect.TypeFor[gitBranchFullyLoadedMsg](): appMsgStateRoute(func(m *AppModel, typed gitBranchFullyLoadedMsg) {
		if m.commitTree == nil || typed.branch != m.commitTree.ActiveBranch() {
			return
		}
		if len(typed.nodes) == 0 {
			m.commitTree.ExitToBranches()
			m.statusBar.SetFlash("No commits found for " + typed.branch)
			return
		}
		m.commitTree.SetNodesWithStats(typed.nodes, typed.stats, typed.hasMore)
	}),
	reflect.TypeFor[committree.LoadMoreMsg](): appMsgCmdRoute(func(m *AppModel, _ committree.LoadMoreMsg) tea.Cmd {
		return m.loadMoreCommitsCmd()
	}),
	reflect.TypeFor[gitMoreCommitsLoadedMsg](): appMsgStateRoute(func(m *AppModel, typed gitMoreCommitsLoadedMsg) {
		if m.commitTree != nil && typed.branch == m.commitTree.ActiveBranch() {
			m.commitTree.AppendNodesWithStats(typed.nodes, typed.stats, typed.hasMore)
		}
	}),
	reflect.TypeFor[committree.DiffCompareMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.DiffCompareMsg) tea.Cmd {
		m.diffHashes = typed.Hashes
		m.diffLabels = nil
		m.setDiffLoading(true)
		return m.fetchDiffDataCmd(m.diffHashes, CompareModeChain)
	}),
	reflect.TypeFor[committree.BranchDiffCompareMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.BranchDiffCompareMsg) tea.Cmd {
		m.diffHashes = typed.Hashes
		m.diffLabels = typed.Names
		m.setDiffLoading(true)
		return m.fetchDiffDataCmd(m.diffHashes, CompareModeChain)
	}),
	reflect.TypeFor[msg.DiffViewDataMsg](): appMsgStateRoute(func(m *AppModel, typed msg.DiffViewDataMsg) {
		pairs := make([]diffview.DiffPair, len(typed.Pairs))
		for i, p := range typed.Pairs {
			pairs[i] = diffview.DiffPair{
				FromHash:  p.FromHash,
				ToHash:    p.ToHash,
				FromShort: p.FromShort,
				ToShort:   p.ToShort,
				Files:     p.Files,
				TotalAdd:  p.TotalAdd,
				TotalDel:  p.TotalDel,
			}
		}
		m.setDiffLoading(false)
		m.enterDiffView(pairs, diffview.CompareMode(typed.Mode))
	}),
	reflect.TypeFor[diffview.ExitDiffViewMsg](): appMsgStateRoute(func(m *AppModel, _ diffview.ExitDiffViewMsg) {
		m.exitDiffView()
	}),
	reflect.TypeFor[diffview.ChangeCompareModeMsg](): appMsgCmdRoute(func(m *AppModel, typed diffview.ChangeCompareModeMsg) tea.Cmd {
		if len(m.diffHashes) < 2 {
			return nil
		}
		m.setDiffLoading(true)
		return m.fetchDiffDataCmd(m.diffHashes, typed.Mode)
	}),
	reflect.TypeFor[committree.MergeDiffCompareMsg](): appMsgCmdRoute(func(m *AppModel, typed committree.MergeDiffCompareMsg) tea.Cmd {
		m.mergeHashes = typed.Hashes
		m.mergeLabels = typed.Names
		m.setMergeDiffLoading(true)
		return m.fetchMergeDiffDataCmd(typed.Hashes, typed.Names)
	}),
	reflect.TypeFor[msg.MergeDiffViewDataMsg](): appMsgStateRoute(func(m *AppModel, typed msg.MergeDiffViewDataMsg) {
		pairs := make([]diffview.DiffPair, len(typed.Pairs))
		for i, p := range typed.Pairs {
			pairs[i] = diffview.DiffPair{
				FromHash:  p.FromHash,
				ToHash:    p.ToHash,
				FromShort: p.FromShort,
				ToShort:   p.ToShort,
				Files:     p.Files,
				TotalAdd:  p.TotalAdd,
				TotalDel:  p.TotalDel,
			}
		}
		m.setMergeDiffLoading(false)
		m.enterMergeDiffView(pairs)
	}),
	reflect.TypeFor[mergediff.ExitMergeDiffViewMsg](): appMsgStateRoute(func(m *AppModel, _ mergediff.ExitMergeDiffViewMsg) {
		m.exitMergeDiffView()
	}),
	reflect.TypeFor[mergediff.MergeBranchMsg](): appMsgCmdRoute(func(m *AppModel, typed mergediff.MergeBranchMsg) tea.Cmd {
		if m.gitBus != nil && len(m.mergeLabels) >= 2 {
			m.pendingSeqOp = &pendingSequencerOp{
				op:     "merge",
				hashes: []string{m.mergeLabels[0]},
				target: m.mergeLabels[1],
				delete: typed.DeleteSource,
			}
			return m.conflictPreviewMergeCmd(m.mergeLabels[0], m.mergeLabels[1])
		}
		return m.executeMergeBranch(typed.DeleteSource)
	}),
	reflect.TypeFor[msg.FileReplacedMsg]():         appMsgCmdRoute((*AppModel).handleFileReplaced),
	reflect.TypeFor[msg.MultiFileReplaceDoneMsg](): appMsgCmdRoute((*AppModel).handleMultiFileReplaceDone),
	reflect.TypeFor[msg.CommandApprovalRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.CommandApprovalRequestMsg) tea.Cmd {
		m.handleCommandApprovalRequest(typed)
		return nil
	}),
	reflect.TypeFor[msg.CommandApprovalCommitMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.CommandApprovalCommitMsg) tea.Cmd {
		return m.commitCommandApproval(typed)
	}),
	reflect.TypeFor[msg.CommandApprovalResolvedMsg](): appMsgCmdRoute(func(m *AppModel, _ msg.CommandApprovalResolvedMsg) tea.Cmd {
		m.resolveCommandApproval()
		return nil
	}),
	reflect.TypeFor[msg.PlanApprovalRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.PlanApprovalRequestMsg) tea.Cmd {
		m.handlePlanApprovalRequest(typed)
		return nil
	}),
	reflect.TypeFor[msg.PlanApprovalCommitMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.PlanApprovalCommitMsg) tea.Cmd {
		return m.commitPlanApproval(typed)
	}),
	reflect.TypeFor[msg.PlanApprovalResolvedMsg](): appMsgCmdRoute(func(m *AppModel, _ msg.PlanApprovalResolvedMsg) tea.Cmd {
		m.resolvePlanApproval()
		return nil
	}),
	reflect.TypeFor[msg.LayerDecisionMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LayerDecisionMsg) tea.Cmd {
		m.handleLayerDecisionRequest(typed)
		return nil
	}),
	reflect.TypeFor[msg.LayerDecisionCommitMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LayerDecisionCommitMsg) tea.Cmd {
		return m.commitLayerDecision(typed)
	}),
	reflect.TypeFor[msg.LayerDecisionResolvedMsg](): appMsgCmdRoute(func(m *AppModel, _ msg.LayerDecisionResolvedMsg) tea.Cmd {
		m.resolveLayerDecision()
		return nil
	}),
	reflect.TypeFor[msg.AccountingSnapshotMsg](): appMsgStateRoute(func(m *AppModel, typed msg.AccountingSnapshotMsg) {
		// Item 52: status bar reads accountant-aggregated totals via
		// accountant.Billable() snapshots published on each tick. These
		// are the canonical session-wide totals, aggregating every
		// provider × agent × replica × task under a typed
		// AccountingKey. updateTokenDisplay consumes them in preference
		// to the legacy stream-derived counters.
		m.accountantTotalInput = typed.TotalInput
		m.accountantTotalOutput = typed.TotalOutput
		m.accountantTotalReasoning = typed.TotalReasoning
		m.accountantTotalRequests = typed.TotalRequests
		m.accountantTotalErrors = typed.TotalErrors
		m.updateTokenDisplay()
	}),
	reflect.TypeFor[msg.TokenDeltaMsg](): appMsgStateRoute(func(m *AppModel, typed msg.TokenDeltaMsg) {
		m.busInputTokens += typed.InputTokens
		m.busOutputTokens += typed.OutputTokens
		m.busCacheReadTokens += typed.CacheReadTokens
		m.busCacheWriteTokens += typed.CacheWriteTokens
		m.busReasoningTokens += typed.ReasoningTokens

		canonicalID := ""
		runtimeAgentID := ""
		if typed.Identity != nil {
			canonicalID = normalizeAgentID(typed.Identity.Kind().String())
			runtimeAgentID = strings.TrimSpace(typed.Identity.Panel())
		}
		runtimeAgentID = normalizeRuntimeAgentID(canonicalID, runtimeAgentID)
		if typed.Model != "" && canonicalID != "" {
			m.agentContextModels[canonicalID] = typed.Model
		}
		if typed.InputTokens > 0 && canonicalID != "" {
			m.setAgentReplicaContextUsage(canonicalID, runtimeAgentID, typed.Model, typed.InputTokens)
		}
		m.updateTokenDisplay()
	}),
	reflect.TypeFor[modal.ModalClosedMsg](): appMsgCmdRoute(func(m *AppModel, typed modal.ModalClosedMsg) tea.Cmd {
		return m.handleModalClosed(typed.Result)
	}),
	reflect.TypeFor[msg.LSPDiagnosticMsg](): appMsgCmdRoute((*AppModel).handleLSPDiagnostic),
	reflect.TypeFor[msg.LSPProvisionDoneMsg](): appMsgStateRoute(func(_ *AppModel, typed msg.LSPProvisionDoneMsg) {
		if typed.Err == nil {
			detect.ClearCache()
		}
	}),
	reflect.TypeFor[msg.LSPServerMissingMsg]():   appMsgCmdRoute((*AppModel).handleLSPServerMissing),
	reflect.TypeFor[msg.LSPServerInstalledMsg](): appMsgCmdRoute((*AppModel).handleLSPServerInstalled),
	reflect.TypeFor[mode.StandaloneResult](): appMsgCmdRoute(func(m *AppModel, typed mode.StandaloneResult) tea.Cmd {
		_, cmd := m.focusedEditor().Update(typed)
		return cmd
	}),
	reflect.TypeFor[msg.LSPHoverRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LSPHoverRequestMsg) tea.Cmd {
		m.hoverMouseLine = typed.Line
		m.hoverMouseCol = typed.Col
		wordStart, _ := m.focusedEditor().WordBoundsAt(typed.Line, typed.Col)
		m.hoverMouseWordStart = wordStart
		m.hoverForPreview = false
		m.pendingHoverSymbol = ""
		m.pendingHoverPkgPath = ""
		return tea.Batch(
			m.lspHoverCmd(typed.FilePath, typed.Line, typed.Col),
			m.lspDefinitionCmd(typed.FilePath, typed.Line, typed.Col, true),
		)
	}),
	reflect.TypeFor[msg.LSPMouseHoverTickMsg](): appMsgCmdRoute((*AppModel).handleMouseHoverTick),
	reflect.TypeFor[msg.LSPHoverMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LSPHoverMsg) tea.Cmd {
		if m.hoverForPreview {
			return m.handlePreviewHoverMsg(typed)
		}
		wordStart, _ := m.focusedEditor().WordBoundsAt(typed.Line, typed.Col)
		if typed.Line != m.hoverMouseLine || wordStart != m.hoverMouseWordStart {
			return nil
		}
		m.focusedEditor().Update(typed)
		if m.focusedEditor().HoverActive() && m.pendingHoverSymbol != "" {
			m.focusedEditor().SetHoverDefinition(m.pendingHoverSymbol, m.pendingHoverPkgPath)
			m.pendingHoverSymbol = ""
			m.pendingHoverPkgPath = ""
		}
		return nil
	}),
	reflect.TypeFor[msg.LSPDefinitionRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LSPDefinitionRequestMsg) tea.Cmd {
		return m.lspDefinitionCmd(typed.FilePath, typed.Line, typed.Col, typed.ForHover)
	}),
	reflect.TypeFor[msg.LSPDefinitionMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LSPDefinitionMsg) tea.Cmd {
		if typed.ForHover {
			return m.handleHoverDefinition(typed)
		}
		_, cmd := m.focusedEditor().Update(typed)
		return cmd
	}),
	reflect.TypeFor[msg.LSPCompletionRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LSPCompletionRequestMsg) tea.Cmd {
		var flushContent string
		if m.focusedEditor().LSPDirty() {
			m.focusedEditor().ClearLSPDirty()
			flushContent = m.focusedEditor().Content()
		}
		return m.lspCompletionCmd(typed.FilePath, typed.Line, typed.Col, flushContent)
	}),
	reflect.TypeFor[msg.LSPCompletionMsg](): appMsgStateRoute(func(m *AppModel, typed msg.LSPCompletionMsg) {
		m.focusedEditor().Update(typed)
	}),
	reflect.TypeFor[msg.LSPSignatureHelpRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LSPSignatureHelpRequestMsg) tea.Cmd {
		var flushContent string
		if m.focusedEditor().LSPDirty() {
			m.focusedEditor().ClearLSPDirty()
			flushContent = m.focusedEditor().Content()
		}
		return m.lspSignatureHelpCmd(typed.FilePath, typed.Line, typed.Col, flushContent)
	}),
	reflect.TypeFor[msg.LSPSignatureHelpMsg](): appMsgStateRoute(func(m *AppModel, typed msg.LSPSignatureHelpMsg) {
		m.focusedEditor().Update(typed)
	}),
	reflect.TypeFor[msg.LSPDocHighlightTickMsg](): appMsgCmdRoute((*AppModel).handleDocHighlightTick),
	reflect.TypeFor[msg.LSPDocumentHighlightMsg](): appMsgStateRoute(func(m *AppModel, typed msg.LSPDocumentHighlightMsg) {
		m.focusedEditor().Update(typed)
	}),
	reflect.TypeFor[msg.LSPReferencesRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LSPReferencesRequestMsg) tea.Cmd {
		return m.lspReferencesCmd(typed.FilePath, typed.Line, typed.Col)
	}),
	reflect.TypeFor[msg.LSPReferencesMsg]():     appMsgCmdRoute((*AppModel).handleLSPReferences),
	reflect.TypeFor[msg.LSPDocumentSymbolMsg](): appMsgCmdRoute((*AppModel).handleLSPDocumentSymbol),
	reflect.TypeFor[msg.LSPFormatRequestMsg](): appMsgCmdRoute(func(m *AppModel, typed msg.LSPFormatRequestMsg) tea.Cmd {
		var flushContent string
		if m.focusedEditor().LSPDirty() {
			m.focusedEditor().ClearLSPDirty()
			flushContent = m.focusedEditor().Content()
		}
		return m.lspFormatCmd(typed.FilePath, flushContent, m.focusedEditor().EditGeneration())
	}),
	reflect.TypeFor[msg.LSPPrepareRenameMsg](): appMsgCmdRoute((*AppModel).handleLSPPrepareRename),
	reflect.TypeFor[msg.LSPRenameMsg]():        appMsgCmdRoute((*AppModel).handleLSPRename),
	reflect.TypeFor[msg.LSPFormatMsg]():        appMsgCmdRoute((*AppModel).handleLSPFormat),
	reflect.TypeFor[msg.TabNextMsg]():          appMsgCmdRoute(func(m *AppModel, _ msg.TabNextMsg) tea.Cmd { return m.nextTab() }),
	reflect.TypeFor[msg.TabPrevMsg]():          appMsgCmdRoute(func(m *AppModel, _ msg.TabPrevMsg) tea.Cmd { return m.prevTab() }),
	reflect.TypeFor[msg.TabJumpMsg]():          appMsgCmdRoute(func(m *AppModel, typed msg.TabJumpMsg) tea.Cmd { return m.switchToTab(typed.Index) }),
	reflect.TypeFor[msg.TabCloseRequestMsg]():  appMsgCmdRoute(func(m *AppModel, typed msg.TabCloseRequestMsg) tea.Cmd { return m.closeTabByPath(typed.Path) }),
	reflect.TypeFor[msg.EscDisambiguateTickMsg](): appMsgModelRoute(func(m *AppModel, typed msg.EscDisambiguateTickMsg) (tea.Model, tea.Cmd) {
		if m.escPending && typed.Gen == m.escGen {
			m.escPending = false
			m.escGen++
			return m.dispatchKey(m.escKey)
		}
		return m, nil
	}),
	reflect.TypeFor[msg.ChordBlockedExpireMsg](): appMsgStateRoute(func(m *AppModel, _ msg.ChordBlockedExpireMsg) {
		if m.chordBlocked {
			m.chord = chordNone
			m.chordBlocked = false
		}
	}),
	reflect.TypeFor[msg.NerdFontsResultMsg](): appMsgStateRoute(func(*AppModel, msg.NerdFontsResultMsg) {}),
}

func (m *AppModel) dispatch(raw tea.Msg) (tea.Model, tea.Cmd) {
	if raw == nil {
		return m, nil
	}
	logClaimsUIMessage(raw)
	if handler, ok := appMsgDispatchRoutes[reflect.TypeOf(raw)]; ok {
		return handler(m, raw)
	}
	return m, m.propagate(raw)
}

func logClaimsUIMessage(raw tea.Msg) {
	switch typed := raw.(type) {
	case msg.ClaimsAgentStatusMsg:
		uiDebugFileLog().Info("CLAIMS_UI_DEBUG: app_received_claims_agent_status",
			"agent_id", typed.AgentID,
			"session_id", typed.SessionID,
			"cycle_id", typed.CycleID,
			"active", typed.Active,
			"reason", typed.Reason,
			"action_type", typed.ActionType,
		)
	case msg.ClaimContextMsg:
		uiDebugFileLog().Info("CLAIMS_UI_DEBUG: app_received_claim_context",
			"owner", typed.OwnerAgentID,
			"session_id", typed.SessionID,
			"claim_id", typed.ClaimID,
			"cycle_id", typed.CycleID,
			"parent_row_id", typed.ParentRowID,
			"context", typed.Context,
			"transition", typed.ContextTransition,
		)
	case msg.ClaimArtifactAddedMsg:
		uiDebugFileLog().Info("CLAIMS_UI_DEBUG: app_received_claim_artifact_added",
			"agent_id", typed.AgentID,
			"owner", typed.OwnerAgentID,
			"claim_id", typed.ClaimID,
			"cycle_id", typed.CycleID,
			"artifact_id", typed.ArtifactID,
			"kind", typed.Kind,
			"parent_row_id", typed.ParentRowID,
		)
	case msg.ClaimArtifactCompletedMsg:
		uiDebugFileLog().Info("CLAIMS_UI_DEBUG: app_received_claim_artifact_completed",
			"cycle_id", typed.CycleID,
			"start_artifact_id", typed.StartArtifactID,
			"outcome", typed.Outcome,
		)
	case msg.ClaimResponseTextMsg:
		uiDebugFileLog().Info("CLAIMS_UI_DEBUG: app_received_claim_response_text",
			"agent_id", typed.AgentID,
			"session_id", typed.SessionID,
			"claim_id", typed.ClaimID,
			"cycle_id", typed.CycleID,
			"parent_row_id", typed.ParentRowID,
			"content_len", len(typed.Content),
		)
	case msg.TestamentContextMsg:
		uiDebugFileLog().Info("CLAIMS_UI_DEBUG: app_received_testament_context",
			"agent_id", typed.AgentID,
			"session_id", typed.SessionID,
			"claim_id", typed.ClaimID,
			"cycle_id", typed.CycleID,
			"testament_id", typed.TestamentID,
			"context", typed.Context,
			"transition", typed.ContextTransition,
		)
	}
}

// View renders the complete TUI layout using the frame compositor.
func (m *AppModel) View() string {
	if !m.ready {
		return ""
	}
	m.syncCursorFrame()

	// Full-screen overlays bypass the compositor entirely.
	if m.overlay == overlayEditor {
		return m.editorOverlay.View(m.cursorVisible)
	}
	if m.overlay == overlayModal && m.modalOverlay.Active() {
		return m.modalOverlay.View()
	}
	if m.overlay == overlaySearch && m.searchOverlay.Visible() {
		return m.searchOverlay.View()
	}
	if m.overlay == overlayFieldManual && m.fieldManualOverlay.Visible() {
		return m.fieldManualOverlay.View()
	}

	// Login panel: replaces the main column area, keeps input + status.
	// Cursor blinks in the login panel; input panel below gets no cursor.
	if m.overlay == overlayLogin && m.loginPanel.Active() {
		m.viewDirty = false
		return m.loginPanel.View(m.cursorVisible) + "\n" +
			m.input.View(false) + "\n" +
			m.statusBar.View()
	}

	// Fast path: nothing dirty.
	if !m.viewDirty && m.comp.HasCache() {
		return m.comp.CachedFrame()
	}

	m.detectDirtySlots()
	m.renderDirtySlots()
	m.viewDirty = false
	return m.comp.Compose()
}

// FrameData exposes structured frame metadata for the most recently rendered
// composited view. Overlay modes fall back to a zero-value snapshot.
func (m *AppModel) FrameData() compositor.FrameSnapshot {
	if !m.ready {
		return compositor.FrameSnapshot{}
	}
	switch m.overlay {
	case overlayNone:
		snapshot := m.comp.Snapshot()
		return compositor.FrameSnapshot{
			Lines:     snapshot.Lines,
			DirtyRows: snapshot.DirtyRows,
			Version:   snapshot.Version,
			Full:      snapshot.Full,
		}
	default:
		return compositor.FrameSnapshot{}
	}
}

func (m *AppModel) syncCursorFrame() {
	m.blinkDirty = false
	if !m.needsBlink() {
		m.cursorVisible = true
		m.lastRenderedPhase = true
		return
	}
	if m.animationsSuspended(time.Now()) {
		m.cursorVisible = true
		m.lastRenderedPhase = true
		return
	}
	m.applyCursorPhase(m.blinkPhase())
}

func (m *AppModel) applyCursorPhase(visible bool) {
	m.cursorVisible = visible
	if visible == m.lastRenderedPhase {
		return
	}
	m.lastRenderedPhase = visible
	m.viewDirty = true
	m.blinkDirty = true
}

// Shutdown gracefully stops all bridges and waits for goroutine cleanup.
func (m *AppModel) Shutdown() error {
	m.cancel() // Cancel all in-flight Cmd contexts first.
	m.accountantBridge.Stop()
	m.sessionBridge.Stop()
	m.lspBridge.Stop()
	if m.gitBridge != nil {
		m.gitBridge.Stop()
	}
	if m.pipelineBridge != nil {
		m.pipelineBridge.Stop()
	}
	if m.claimsBridge != nil {
		m.claimsBridge.Stop()
	}

	var errs []error
	if err := m.lspManager.Shutdown(); err != nil {
		errs = append(errs, err)
	}
	if m.safetyGuard != nil {
		if err := m.safetyGuard.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if m.walCloser != nil {
		if err := m.walCloser.Close(); err != nil {
			errs = append(errs, err)
		}
		m.walCloser = nil
	}
	if err := m.stopChatDebugCapture(); err != nil {
		errs = append(errs, err)
	}
	// NOTE: m.deps.Scope.Shutdown is intentionally NOT called here. The
	// scope is owned by cmd/tui.go's bootstrap and must outlive the UI's
	// shutdown so that per-component Close() calls registered in the
	// cmd cleanup function (knowledgeSync, knowledgeStore, forest, etc.)
	// can drain their scope-tracked workers via cooperative cancellation
	// before the scope itself reports a leak. The cmd cleanup function
	// invokes scope.Shutdown after every per-component Close has fired.
	return errors.Join(errs...)
}

// PushModal adds a modal to the overlay stack and activates the modal overlay.
func (m *AppModel) PushModal(content modal.ModalContent) {
	m.modalOverlay.Push(content)
	m.overlay = overlayModal
}

// ---------------------------------------------------------------------------
// Message handlers
// ---------------------------------------------------------------------------

func (m *AppModel) handleResize(sz tea.WindowSizeMsg) tea.Cmd {
	if m.width == sz.Width && m.height == sz.Height && m.ready {
		return nil
	}
	m.width = sz.Width
	m.height = sz.Height
	m.ready = true
	m.viewDirty = true
	m.beginResizeQuiesce(time.Now())
	m.recalcLayout()
	return nil
}

// handleKey wraps dispatchKey with ESC/Alt disambiguation. Terminals encode
// Alt+<rune> as ESC (0x1b) followed by the rune byte. When these arrive in a
// single read, Bubble Tea correctly sets Key.Alt. When they arrive in
// separate reads (SSH latency, slow terminals), Bubble Tea emits a standalone
// KeyEscape followed by a KeyRunes — losing the Alt modifier. This layer
// buffers a standalone ESC for up to escDisambiguateTimeout, and if a single
// printable rune follows within that window it synthesises the correct
// Alt+rune KeyMsg. The mechanism mirrors vim's ttimeoutlen and tmux's
// escape-time.
func (m *AppModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Resolve a previously buffered ESC.
	if m.escPending {
		m.escPending = false
		m.escGen++

		if time.Since(m.escAt) < escDisambiguateTimeout &&
			key.Type == tea.KeyRunes && len(key.Runes) == 1 && !key.Alt {
			// Synthesise Alt+rune from the buffered ESC + this rune.
			synth := tea.KeyMsg(tea.Key{
				Type:  tea.KeyRunes,
				Runes: key.Runes,
				Alt:   true,
			})
			return m.dispatchKey(synth)
		}

		// ESC followed by a non-rune key or after the timeout — flush ESC
		// first so it takes effect (e.g. exit insert mode), then process the
		// current key with updated state.
		_, escCmd := m.dispatchKey(m.escKey)
		model, keyCmd := m.dispatchKey(key)
		return model, tea.Batch(escCmd, keyCmd)
	}

	// Buffer a standalone ESC for disambiguation.
	if key.Type == tea.KeyEscape && !key.Alt {
		m.escPending = true
		m.escKey = key
		m.escAt = time.Now()
		gen := m.escGen
		return m, tea.Tick(escDisambiguateTimeout, func(_ time.Time) tea.Msg {
			return msg.EscDisambiguateTickMsg{Gen: gen}
		})
	}

	return m.dispatchKey(key)
}

type appKeyDispatchRoute func(*AppModel, tea.KeyMsg, string) (tea.Model, tea.Cmd, bool)

func keyPredicateRoute(
	pred func(*AppModel, tea.KeyMsg, string) bool,
	fn func(*AppModel, tea.KeyMsg, string) (tea.Model, tea.Cmd),
) appKeyDispatchRoute {
	return func(m *AppModel, key tea.KeyMsg, ks string) (tea.Model, tea.Cmd, bool) {
		if !pred(m, key, ks) {
			return nil, nil, false
		}
		model, cmd := fn(m, key, ks)
		return model, cmd, true
	}
}

func keyStringRoute(
	expected string,
	fn func(*AppModel, tea.KeyMsg, string) (tea.Model, tea.Cmd),
) appKeyDispatchRoute {
	return keyPredicateRoute(
		func(_ *AppModel, _ tea.KeyMsg, ks string) bool { return ks == expected },
		fn,
	)
}

func (m *AppModel) allowOverlayToggle(ks string) bool {
	now := time.Now()
	if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
		return false
	}
	m.lastToggleKey = ks
	m.lastToggleAt = now
	m.chord = chordNone
	return true
}

func warpSlotFromKey(ks string) (int, bool) {
	if len(ks) != 5 || !strings.HasPrefix(ks, "alt+") {
		return 0, false
	}
	slot := ks[4]
	if slot < '1' || slot > '9' {
		return 0, false
	}
	return int(slot - '1'), true
}

func shiftedWarpSlotFromKey(ks string) (int, bool) {
	if len(ks) != 5 || !strings.HasPrefix(ks, "alt+") {
		return 0, false
	}
	idx, ok := shiftDigitSlot[ks[4]]
	return idx, ok
}

var appKeyDispatchRoutes = []appKeyDispatchRoute{
	keyPredicateRoute(
		func(_ *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "ctrl+c" || ks == "ctrl+shift+c" },
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.viewMode == ViewEdit && m.focusedEditor().HasSelection() {
				text := m.focusedEditor().SelectedText()
				if err := m.clipboard.Set(text); err != nil {
					m.statusBar.SetFlash("Copy failed")
				} else {
					m.statusBar.SetFlash("Copied!")
				}
				m.focusedEditor().ClearSelection()
				return m, nil
			}
			if m.focus.Current() == component.FocusInput && m.input.HasSelection() {
				text := m.input.SelectedText()
				if err := m.clipboard.Set(text); err != nil {
					m.statusBar.SetFlash("Copy failed")
				} else {
					m.statusBar.SetFlash("Copied!")
				}
				m.input.ClearSelection()
				return m, nil
			}
			if ks == "ctrl+c" {
				result := m.interruptHandler.HandleCtrlC()
				return m, func() tea.Msg { return result }
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool { return m.commandApproval != nil },
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			return m.handleCommandApprovalKey(key)
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool { return m.planApproval != nil },
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			return m.handlePlanApprovalKey(key)
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool { return m.layerDecision != nil },
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			return m.handleLayerDecisionKey(key)
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool { return m.pendingClosePrompt },
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			return m.handleSavePromptKey(key)
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool { return m.pendingUncommittedAll },
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			m.pendingUncommittedAll = false
			if m.gitPanel == nil {
				return m, nil
			}
			switch ks {
			case "a", "A", "alt+a", "alt+A":
				m.gitPanel.ToggleUncommittedAll()
				return m, nil
			case "s", "S", "alt+s", "alt+S":
				return m, m.gitPanel.StashAll()
			case "p", "P", "alt+p", "alt+P":
				return m, m.gitPanel.TriggerUnstash()
			default:
				return m, nil
			}
		},
	),
	keyStringRoute("alt+A", func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
		switch {
		case m.viewMode == ViewEdit && m.isEditorFocused():
			m.focusedEditor().SelectAll()
		case m.focus.Current() == component.FocusInput:
			m.input.SelectAll()
		}
		return m, nil
	}),
	keyPredicateRoute(
		func(_ *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "ctrl+x" || ks == "ctrl+shift+x" },
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			var text string
			switch {
			case m.viewMode == ViewEdit && m.isEditorFocused():
				text = m.focusedEditor().CutSelection()
			case m.focus.Current() == component.FocusInput && m.input.HasSelection():
				text = m.input.CutSelection()
			}
			if text != "" {
				if err := m.clipboard.Set(text); err != nil {
					m.statusBar.SetFlash("Cut failed")
				} else {
					m.statusBar.SetFlash("Cut!")
				}
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+z" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.focusedEditor().Undo()
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+Z" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.focusedEditor().Redo()
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "alt+ " && !m.promptQueue.IsEmpty() },
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			paused := m.promptQueue.TogglePause()
			if paused {
				m.statusBar.SetFlash("Queue paused")
			} else {
				m.statusBar.SetFlash("Queue resumed")
				return m, m.tryAdvanceQueue()
			}
			m.markSlotDirty(compositor.SlotQueue)
			m.viewDirty = true
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "alt+k" && !m.promptQueue.IsEmpty() },
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			pending := m.promptQueue.PendingEntries()
			if len(pending) > 0 {
				m.promptQueue.Cancel(pending[0].ID)
				m.recalcLayout()
				m.markSlotDirty(compositor.SlotQueue)
				m.viewDirty = true
				m.statusBar.SetFlash("Cancelled queued prompt")
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "alt+K" && !m.promptQueue.IsEmpty() },
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			n := m.promptQueue.CancelAll()
			if n > 0 {
				m.recalcLayout()
				m.markSlotDirty(compositor.SlotQueue)
				m.viewDirty = true
				m.statusBar.SetFlash(fmt.Sprintf("Cancelled %d queued prompts", n))
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+r" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			fp := m.focusedEditor().FilePath()
			if fp == "" {
				return m, nil
			}
			return m, m.lspReferencesCmd(fp, m.focusedEditor().CursorLine(), m.focusedEditor().CursorCol())
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "alt+>" && m.viewMode == ViewEdit },
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			if m.fileTree.InDocSymbolsMode() {
				m.fileTree.ExitDocSymbols()
				return m, nil
			}
			fp := m.focusedEditor().FilePath()
			if fp == "" {
				return m, nil
			}
			return m, m.lspDocumentSymbolCmd(fp)
		},
	),
	keyPredicateRoute(
		func(_ *AppModel, _ tea.KeyMsg, ks string) bool {
			_, ok := warpSlotFromKey(ks)
			return ok
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			idx, _ := warpSlotFromKey(ks)
			if m.warpPoints[idx] != nil {
				return m, m.teleportToWarp(idx)
			}
			if m.viewMode == ViewEdit {
				m.setWarpPoint(idx)
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(_ *AppModel, _ tea.KeyMsg, ks string) bool {
			_, ok := shiftedWarpSlotFromKey(ks)
			return ok
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			idx, _ := shiftedWarpSlotFromKey(ks)
			m.clearWarpPoint(idx)
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "alt+t" && m.viewMode == ViewEdit },
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			return m, m.toggleTabsPanel()
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "alt+enter" && m.viewMode == ViewEdit },
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			if m.isPreviewFocused() {
				m.dismissPreview()
				return m, nil
			}
			if m.mdPreviewPane != 0 && m.focus.Current() == pane.PaneFocusID(m.mdPreviewPane) {
				m.dismissMarkdownPreview()
				return m, nil
			}
			if m.focus.Current() == component.FocusFileTree && m.fileTree.InTabsMode() {
				if p := m.fileTree.TabCursorPath(); p != "" {
					return m, m.closeTabByPath(p)
				}
				return m, nil
			}
			if m.isEditorFocused() {
				idx := m.activeTabIndex()
				if idx >= 0 {
					return m, m.closeTab(idx)
				}
			}
			return m, nil
		},
	),
	keyStringRoute("alt+c", func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
		return m, m.switchToChatMode()
	}),
	keyStringRoute("alt+m", func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
		return m, m.toggleMemoryMode()
	}),
	keyStringRoute("alt+g", func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
		if m.viewMode == ViewGit {
			return m, nil
		}
		if m.gitPanel == nil {
			m.statusBar.SetFlash("Not a git repository")
			return m, nil
		}
		return m, m.enterGitMode()
	}),
	keyStringRoute("alt+e", func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
		if m.viewMode == ViewEdit {
			return m, nil
		}
		return m, m.toggleEditMode()
	}),
	keyStringRoute("alt+h", func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
		m.toggleFieldManual()
		return m, nil
	}),
	// UI-06: `?` is the conventional help key. Routes to the field
	// manual only when the editor is NOT focused — inside a vim-mode
	// editor, `?` performs a reverse search and must not be
	// intercepted. The AUDIT spec calls for `?`; this predicate
	// coexists with Alt+H so both entry points work.
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return shouldRouteHelpKey(ks, m.viewMode == ViewEdit, m.isEditorFocused())
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.toggleFieldManual()
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "esc" && m.overlay == overlayNone },
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			if m.viewMode == ViewGit && m.gitPanel != nil && m.focus.Current() == component.FocusGitPanel {
				comp, cmd := m.gitPanel.Update(key)
				m.gitPanel = comp.(*gitpanel.Model)
				return m, cmd
			}
			if (m.fileTree.InReferencesMode() || m.fileTree.InDocSymbolsMode() || m.fileTree.InTabsMode()) &&
				m.focus.Current() == component.FocusFileTree {
				m.fileTree.Update(key)
				return m, nil
			}
			if m.editCmdInput {
				m.exitCmdInput()
				return m, nil
			}
			if m.viewMode == ViewEdit && m.isEditorFocused() {
				comp, cmd := m.focusedEditor().Update(key)
				m.paneEditors[m.focusedPane].editor = comp.(*editor.Model)
				return m, cmd
			}
			if m.viewMode == ViewGit && m.focus.Current() == component.FocusCommitTree &&
				(m.commitTree.InCommitView() || m.commitTree.InCreateInput() || m.commitTree.NeedsEscRouting()) {
				comp, cmd := m.commitTree.Update(key)
				m.commitTree = comp.(*committree.Model)
				return m, cmd
			}
			if m.conflictViewActive && m.conflictView != nil && m.focus.Current() == component.FocusConflictView {
				m.focus.SetFocus(component.FocusConflictFileList)
				m.syncFocusState()
				return m, nil
			}
			if m.conflictViewActive && m.conflictView != nil && m.focus.Current() == component.FocusConflictFileList {
				return m, m.preserveAbortCmd("sequencer")
			}
			if m.mergeDiffViewActive && m.mergeDiffView != nil &&
				m.focus.Current() == component.FocusMergeDiffFileList &&
				m.mergeDiffView.FileSearchActive() {
				m.mergeDiffView.UpdateFileList("esc")
				return m, nil
			}
			if m.mergeDiffViewActive && m.mergeDiffView != nil && m.focus.Current() == component.FocusMergeDiffFileList {
				m.exitMergeDiffView()
				return m, nil
			}
			if m.mergeDiffViewActive && m.mergeDiffView != nil && m.isMergeDiffPaneFocused() {
				return m, m.mergeDiffView.Update(key)
			}
			if m.diffViewActive && m.diffView != nil &&
				m.focus.Current() == component.FocusDiffFileList &&
				m.diffView.FileSearchActive() {
				m.diffView.UpdateFileList("esc")
				return m, nil
			}
			if m.diffViewActive && m.diffView != nil && m.focus.Current() == component.FocusDiffFileList {
				m.exitDiffView()
				return m, nil
			}
			if m.diffViewActive && m.diffView != nil && m.isDiffPaneFocused() {
				return m, m.diffView.Update(key)
			}
			if m.focus.Current() == component.FocusAgentPanel && m.agentPanel.InSubView() {
				comp, cmd := m.agentPanel.Update(key)
				m.agentPanel = comp.(*agentpkg.Model)
				m.syncManualTargetFromAgentSelection()
				return m, cmd
			}
			if !m.input.IsEmpty() {
				m.input.ClearInput()
				return m, nil
			}

			now := time.Now()
			if !m.lastEscTime.IsZero() && now.Sub(m.lastEscTime) <= time.Second {
				m.escPressCount++
				m.lastEscTime = now
				if m.escPressCount == 2 {
					cmd := m.interruptActiveRoute("esc")
					m.statusBar.SetFlash("Agent interrupted · Esc again to interrupt all")
					return m, cmd
				}
				m.lastEscTime = time.Time{}
				m.escPressCount = 0
				return m, m.interruptAllActiveRoutes("esc-all")
			}
			m.lastEscTime = now
			m.escPressCount = 1
			m.statusBar.SetFlash("Press Esc again to interrupt agent")
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "shift+tab" && m.viewMode == ViewEdit && m.isEditorFocused() && len(m.focusedTabOrder()) > 0
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			return m, m.nextTab()
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == ":" && m.viewMode == ViewEdit && m.isEditorFocused() && m.focusedEditor().IsNormalMode()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.enterCmdInput()
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool { return m.overlay == overlayEditor },
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			comp, cmd := m.editorOverlay.Update(key)
			m.editorOverlay = comp.(*editor.Model)
			return m, cmd
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool {
			return m.overlay == overlayModal && m.modalOverlay.Active()
		},
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			comp, cmd := m.modalOverlay.Update(key)
			m.modalOverlay = comp.(*modal.Model)
			return m, cmd
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool {
			return m.overlay == overlaySearch && m.searchOverlay.Visible()
		},
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			comp, cmd := m.searchOverlay.Update(key)
			m.searchOverlay = comp.(*search.Model)
			return m, cmd
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool {
			return m.overlay == overlayFieldManual && m.fieldManualOverlay.Visible()
		},
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			comp, cmd := m.fieldManualOverlay.Update(key)
			m.fieldManualOverlay = comp.(*fieldmanual.Model)
			if !m.fieldManualOverlay.Visible() {
				m.overlay = overlayNone
			}
			return m, cmd
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, _ string) bool {
			return m.overlay == overlayLogin && m.loginPanel.Active()
		},
		func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			done, result, cmd := m.loginPanel.Update(key)
			if done {
				return m, tea.Batch(cmd, m.handleLoginPanelResult(result))
			}
			return m, cmd
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "ctrl+p" && !m.focusedEditor().CompletionActive()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.toggleSearch()
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+f" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				m.focusedEditor().ToggleFindBar()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+R" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				m.focusedEditor().ToggleReplaceBar()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+f" && m.focus.Current() == component.FocusFileTree
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				if m.fileTree.InTabsMode() {
					m.fileTree.ToggleTabFilter()
				} else {
					m.fileTree.ToggleSearch()
				}
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+f" && m.viewMode == ViewGit && m.focus.Current() == component.FocusGitPanel && m.gitPanel != nil
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				m.gitPanel.ToggleSearch()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+f" && m.mergeDiffViewActive &&
				m.focus.Current() == component.FocusMergeDiffFileList &&
				m.mergeDiffView != nil
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				m.mergeDiffView.ToggleFileSearch()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+f" && m.mergeDiffViewActive && m.isMergeDiffPaneFocused() && m.mergeDiffView != nil
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				m.mergeDiffView.ToggleFindBar()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+f" && m.diffViewActive &&
				m.focus.Current() == component.FocusDiffFileList &&
				m.diffView != nil
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				m.diffView.ToggleFileSearch()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+f" && m.diffViewActive && m.isDiffPaneFocused() && m.diffView != nil
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				m.diffView.ToggleFindBar()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+R" && m.focus.Current() == component.FocusFileTree
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if m.allowOverlayToggle(ks) {
				m.fileTree.ToggleReplace()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+F" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			fp := m.focusedEditor().FilePath()
			if fp == "" {
				return m, nil
			}
			var flushContent string
			if m.focusedEditor().LSPDirty() {
				m.focusedEditor().ClearLSPDirty()
				flushContent = m.focusedEditor().Content()
			}
			return m, m.lspFormatCmd(fp, flushContent, m.focusedEditor().EditGeneration())
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+S" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.saveEditorBuffer()
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return m.viewMode == ViewEdit && len(m.focusedTabOrder()) > 1 && (ks == "ctrl+left" || ks == "ctrl+right")
		},
		func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd) {
			if ks == "ctrl+left" {
				return m, m.tabNavLeft()
			}
			return m, m.tabNavRight()
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool { return ks == "alt+E" && m.viewMode == ViewEdit },
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			if m.focus.Current() == component.FocusFileTree {
				m.focusCodePanel()
			} else {
				m.focus.SetFocus(component.FocusFileTree)
			}
			m.syncFocusState()
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+|" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.splitPane(pane.SplitVertical)
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+_" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.splitPane(pane.SplitHorizontal)
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+M" && m.viewMode == ViewEdit && m.isEditorFocused()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			if m.mdPreviewPane != 0 {
				m.dismissMarkdownPreview()
			} else if isMarkdownFile(m.focusedEditor().FilePath()) {
				m.openMarkdownPreview(m.focusedEditor().FilePath())
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+W" && m.viewMode == ViewEdit && m.paneTree != nil && !m.paneTree.IsLeaf()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.closePane()
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+]" && m.viewMode == ViewEdit && m.paneTree != nil && !m.paneTree.IsLeaf()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			if m.paneTree.AdjustRatio(m.focusedPane, ratioStep) {
				m.resizeInlineEditor()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+[" && m.viewMode == ViewEdit && m.paneTree != nil && !m.paneTree.IsLeaf()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			if m.paneTree.AdjustRatio(m.focusedPane, -ratioStep) {
				m.resizeInlineEditor()
			}
			return m, nil
		},
	),
	keyPredicateRoute(
		func(m *AppModel, _ tea.KeyMsg, ks string) bool {
			return ks == "alt+=" && m.viewMode == ViewEdit && m.paneTree != nil && !m.paneTree.IsLeaf()
		},
		func(m *AppModel, _ tea.KeyMsg, _ string) (tea.Model, tea.Cmd) {
			m.paneTree.Equalize()
			m.resizeInlineEditor()
			m.statusBar.SetFlash("Splits equalized")
			return m, nil
		},
	),
	func(m *AppModel, key tea.KeyMsg, _ string) (tea.Model, tea.Cmd, bool) {
		cmd, handled := m.handleChord(key)
		if !handled {
			return nil, nil, false
		}
		return m, cmd, true
	},
	func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd, bool) {
		if m.viewMode != ViewGit || m.gitPanel == nil {
			return nil, nil, false
		}
		cmd, handled := m.handleGitTabShortcut(ks)
		if !handled {
			return nil, nil, false
		}
		return m, cmd, true
	},
	func(m *AppModel, _ tea.KeyMsg, ks string) (tea.Model, tea.Cmd, bool) {
		target, ok := m.spatialFocusTarget(ks)
		if !ok {
			return nil, nil, false
		}
		m.focus.SetFocus(target)
		m.syncFocusState()
		return m, nil, true
	},
}

func (m *AppModel) dispatchKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	ks := key.String()
	for _, route := range appKeyDispatchRoutes {
		if model, cmd, handled := route(m, key, ks); handled {
			return model, cmd
		}
	}
	return m, m.propagateToFocused(key)
}
