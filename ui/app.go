package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	coreerrors "github.com/adalundhe/sylk/core/errors"
	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/detect"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/pipeline/tdd"
	"github.com/adalundhe/sylk/core/pipeline/variants"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/core/storage"
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
	"github.com/adalundhe/sylk/ui/fonts"
	"github.com/adalundhe/sylk/ui/gitpanel"
	inputpkg "github.com/adalundhe/sylk/ui/input"
	"github.com/adalundhe/sylk/ui/interrupt"
	knowledgepkg "github.com/adalundhe/sylk/ui/knowledge"
	"github.com/adalundhe/sylk/ui/layout"
	"github.com/adalundhe/sylk/ui/login"
	markdownpkg "github.com/adalundhe/sylk/ui/markdown"
	"github.com/adalundhe/sylk/ui/mergediff"
	"github.com/adalundhe/sylk/ui/modal"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/planview"
	"github.com/adalundhe/sylk/ui/queue"
	"github.com/adalundhe/sylk/ui/redact"
	"github.com/adalundhe/sylk/ui/search"
	sessionpkg "github.com/adalundhe/sylk/ui/session"
	"github.com/adalundhe/sylk/ui/status"
	"github.com/adalundhe/sylk/ui/tabbar"
	"github.com/adalundhe/sylk/ui/theme"
)

// tickFastInterval drives 60fps animations (scroll momentum, bounce, flash).
// Derived from: 1000ms / 60fps ≈ 16ms.
const tickFastInterval = 16 * time.Millisecond

// tickSlowInterval drives transient slow-rate work (swipe decay).
// Derived from: swipe decay timeout is 300ms; 200ms provides ≥1 sample.
const tickSlowInterval = 200 * time.Millisecond

// decorTickInterval drives low-frequency UI effects (spinners, flashes).
// 100ms (~10fps) keeps visuals smooth while reducing idle CPU.
const decorTickInterval = 100 * time.Millisecond

// blinkHalfPeriod is the duration between cursor visibility toggles.
// Derived from: standard terminal cursor blink rate (~530ms per phase).
const blinkHalfPeriod = 530 * time.Millisecond

// tickRate classifies the current tick chain speed.
type tickRate int

const (
	tickIdle tickRate = iota // No tick scheduled.
	tickSlow                 // 200ms — swipe decay.
	tickFast                 // 16ms — scroll, bounce, flash, spinner.
)

// shutdownGrace is the grace period for goroutine shutdown.
// Derived from: once contexts are cancelled, goroutines exit within ms.
const shutdownGrace = 1 * time.Second

// shutdownHard is the hard deadline for goroutine shutdown after force cancel.
const shutdownHard = 2 * time.Second

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
	ActivityBus    *events.ActivityEventBus
	SessionManager *session.Manager
	GuideBus       guide.EventBus
	StreamManager  *guide.StreamManager
	Guide          *guide.Guide
	Scope          *concurrency.GoroutineScope
	AuthRegistry   *credentials.AuthRegistry

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

	// Pipeline system — optional, nil when no pipeline subsystem is active.
	PipelineManager *tdd.PipelineManager
	VariantRegistry variants.Registry

	// SeedAgents pre-populates the agent panel with known agents at startup.
	// Agents are created as idle entries without requiring activity events.
	SeedAgents []AgentSeed
}

// AgentSeed describes an agent to pre-populate in the UI agent panel.
type AgentSeed struct {
	ID        string
	AgentType string
	Name      string
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

	// Panel components
	chat           *chat.Model
	input          *inputpkg.Model
	statusBar      *status.Model
	sessionPanel   *sessionpkg.Model
	agentPanel     *agentpkg.Model
	codePanel      *codepkg.Model
	knowledgePanel *knowledgepkg.Model
	fileTree       *filetree.Model

	// Overlay components
	editorOverlay      *editor.Model
	modalOverlay       *modal.Model
	searchOverlay      *search.Model
	fieldManualOverlay *fieldmanual.Model
	overlay            overlayState

	// Bridges
	activityBridge *bridge.ActivityBridge
	sessionBridge  *bridge.SessionBridge
	streamBridge   *bridge.StreamBridge
	guideBridge    *bridge.GuideBridge
	lspBridge      *bridge.LSPBridge
	pipelineBridge *bridge.PipelineBridge

	// LSP
	lspManager    *lsp.Manager
	lspInstalling map[lsp.ServerID]bool // Tracks in-progress on-demand installs.

	// Interrupt
	interruptHandler *interrupt.Handler
	lastEscTime      time.Time

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
	swipe                 swipeState       // Horizontal scroll accumulation for ring cycling.

	// View mode state machine — exactly one of ViewChat/ViewEdit/ViewGit.
	viewMode       ViewMode
	savedLeftIdx   int               // Saved leftRing.index before edit mode.
	savedRightIdx  int               // Saved rightRing.index before edit mode.
	savedChatFocus component.FocusID // Last focused panel in chat mode.
	savedEditFocus component.FocusID // Last focused panel in edit mode.

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
	focusGradient       *theme.Gradient // Current gradient for focus ring border.
	idleFocusGradient   *theme.Gradient // Subdued blue→white gradient (no active agents).
	activeFocusGradient *theme.Gradient // Full prismatic gradient (active agents).
	focusRingStart      time.Time       // Epoch for focus ring shimmer.

	// Demand-driven tick chain state.
	tickGen  uint64   // Generation counter; incremented on tick chain transitions.
	tickRate tickRate // Current tick chain speed (idle/slow/fast).
	decorGen uint64   // Generation counter for decor tick chain.
	decorOn  bool     // Whether decor tick chain is active.

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
	guideContextTokens int
	guideContextUsage  float64
	agentContextTokens map[string]int
	streamUsage        map[string]streamUsageEntry
	streamedResponses  map[string]streamedResponseState
	activeRoute              activeRouteState
	interruptedCorrelations map[string]struct{} // Correlation IDs killed by interrupt.
	engagedAgentID          string              // Sticky agent the user is conversing with.
	manualTargetAgent  string

	// Prompt queue: stacks follow-up prompts while agents stream.
	promptQueue   queue.Queue
	queueGradient *theme.Gradient

	// Cumulative token counters for the status bar display.
	totalPromptTokens     int
	totalCompletionTokens int

	// TUI-local WAL logger for suppressed/non-UI operational errors.
	walLogger *slog.Logger
	walCloser io.Closer
}

type streamUsageEntry struct {
	AgentID           string
	Tokens            int // Estimated/real output tokens.
	InputTokens       int // Real input tokens from the provider (context window occupancy).
	StartedAt         time.Time
	EarlyInputApplied bool // True if early input tokens were applied during streaming.
}

type streamedResponseState struct {
	HadChunk  bool
	Completed bool
	Succeeded bool
	SeenAt    time.Time
}

type activeRouteState struct {
	CorrelationID string
	AgentID       string
}

const streamedResponseStateTTL = 45 * time.Second

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
	ViewChat ViewMode = iota // Default: chat + panels.
	ViewEdit                 // Inline vim editor active.
	ViewGit                  // Git explorer + commit tree.
)

// String returns the status bar label for this mode.
func (v ViewMode) String() string {
	switch v {
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

// swipeState tracks horizontal scroll accumulation for ring cycling.
type swipeState struct {
	accum         float64   // Accumulated horizontal ticks (+right, -left).
	stamp         time.Time // Time of last horizontal scroll event.
	cooldownUntil time.Time // Events are ignored until this time (post-cycle dead zone).
}

// New creates a root AppModel from configuration and dependencies.
func New(ctx context.Context, cfg Config, deps Deps) *AppModel {
	// Ensure managed LSP binaries are discoverable before any server
	// selection occurs. Must precede NewManager / selector creation.
	lsp.EnsureManagedBinOnPath()

	appCtx, appCancel := context.WithCancel(ctx)
	th := cfg.Theme()

	app := &AppModel{
		ctx:                appCtx,
		cancel:             appCancel,
		config:             cfg,
		deps:               deps,
		layout:             layout.NewManager(0, 0, defaultPanels, defaultModeCandidates),
		focus:              layout.NewFocusManager(defaultTabOrder),
		chat:               chat.New(th, cfg.ChatHistoryCapacity),
		statusBar:          status.New(th, deps.SessionManager),
		sessionPanel:       sessionpkg.New(deps.SessionManager, th),
		agentPanel:         agentpkg.New(th),
		codePanel:          codepkg.New(th),
		knowledgePanel:     knowledgepkg.New(th),
		fileTree:           filetree.New(th),
		editorOverlay:      editor.New(th),
		editorCache:        editor.NewEditorCache(editor.CacheConfig{}),
		modalOverlay:       modal.New(th),
		searchOverlay:      search.New(th, search.NewProviderRegistry()),
		fieldManualOverlay: fieldmanual.New(th),
		loginPanel: login.New(th,
			func(provider string) string {
				return resolveLoginPanelAPIKey(provider)
			},
			llm.ValidateKeyFormat,
			nil, // clipboard — wired after construction (needs m.clipboard)
		),
		activityBridge:         bridge.NewActivityBridge("tui.activity", deps.ActivityBus, deps.Scope),
		sessionBridge:          bridge.NewSessionBridge(deps.SessionManager, deps.Scope),
		streamBridge:           bridge.NewStreamBridge(deps.Scope),
		guideBridge:            bridge.NewGuideBridge(deps.GuideBus, deps.Scope, "default"),
		lspManager:             lsp.NewManager(deps.Scope),
		lspInstalling:          make(map[lsp.ServerID]bool),
		interruptHandler:       interrupt.NewHandlerWithThreshold(time.Duration(cfg.InterruptThresholdMs) * time.Millisecond),
		clipboard:              register.NewOSClipboard(),
		scrollSpring:           harmonica.NewSpring(harmonica.FPS(scrollFPS), scrollFrequency, scrollDamping),
		bounceSpring:           harmonica.NewSpring(harmonica.FPS(scrollFPS), bounceFrequency, bounceDamping),
		hoverMouseLine:         -1,
		highlightLine:          -1,
		tabDragIdx:             -1,
		tabHoverClose:          -1,
		previewTabHoverClose:   -1,
		mdPreviewTabHoverClose: -1,
		mdTooltipTab:           -1,
		viewMode:               ViewChat,
		agentContextTokens:     make(map[string]int),
		streamUsage:            make(map[string]streamUsageEntry),
		streamedResponses:      make(map[string]streamedResponseState),
		oauthSessions:          newOAuthSessionManager(),
		focusGradient:       th.Palette.IdleFocusRingGradient(),
		idleFocusGradient:   th.Palette.IdleFocusRingGradient(),
		activeFocusGradient: th.Palette.FocusRingGradient(),
		focusRingStart:      time.Now(),
	}

	app.promptQueue = queue.New(queue.MaxCapacity)
	app.queueGradient = th.Palette.QueueGradient()

	app.walLogger, app.walCloser = newTUIWALLogger()

	// Wire login panel clipboard now that m.clipboard is available.
	app.loginPanel.SetClipboard(app.clipboard.Get)
	app.loginPanel.SetClipboardWrite(app.clipboard.Set)

	// Pre-populate agent panel with known agents from bootstrap.
	for _, seed := range deps.SeedAgents {
		app.agentPanel.SeedAgent(seed.ID, seed.AgentType, seed.Name)
	}

	app.comp = compositor.New()
	app.previewPanel = preview.New(th)
	app.mdPreviewPanel = markdownpkg.New(th)
	app.paneCounter = 1
	app.focusedPane = 1
	app.paneTree = pane.NewLeaf(1)
	app.paneEditors = map[pane.PaneID]*editorPaneState{
		1: {editor: editor.New(th)},
	}

	app.planView = planview.New(&th.Palette)

	app.lspBridge = bridge.NewLSPBridge(app.lspManager, deps.Scope)
	app.input = inputpkg.New(th, cfg.InputHistoryCapacity,
		&tabCompleter{tabOrderFn: app.focusedTabOrder},
		&slashCommandCompleter{},
	)
	app.input.SetSlashValidator(isKnownSlashCommand)
	app.syncFocusState()

	// Nerd Font detection: use pre-computed result from parallel bootstrap
	// when available, falling back to blocking detection.
	if deps.NerdFontsDetected {
		app.nerdFontsDetected = true
	} else if deps.GitClient == nil {
		// Only run blocking detection if bootstrap didn't pre-compute it.
		app.nerdFontsDetected = fonts.Detected()
	}
	app.fileTree.SetNerdFonts(app.nerdFontsDetected)
	app.statusBar.SetNerdFonts(app.nerdFontsDetected)

	// Probe initial auth state for provider status icons.
	if deps.AuthRegistry != nil {
		for _, provider := range []string{"google", "anthropic", "openai"} {
			app.statusBar.SetAuthStatus(provider, deps.AuthRegistry.IsAvailable(provider))
		}
	}

	// Git status watcher for file tree decorations. Use pre-created
	// objects from parallel bootstrap when available.
	if deps.GitClient != nil && deps.GitClient.IsGitRepo() {
		app.gitClient = deps.GitClient
		app.gitBus = deps.GitBus
		app.gitBridge = bridge.NewGitBridge(app.gitBus, deps.Scope)
		configPath := filepath.Join(cfg.ProjectRoot, ".sylk", "config.yaml")
		app.gitPanel = gitpanel.New(th, app.gitBus, configPath)
		app.commitTree = committree.New(th)
		app.gitWatcher = deps.GitWatcher
		app.safetyGuard = deps.SafetyGuard
	} else if gc, err := git.NewGitClient(cfg.ProjectRoot); err == nil && gc.IsGitRepo() {
		app.gitClient = gc
		app.gitBus = git.NewGitBus(gc)
		app.gitBridge = bridge.NewGitBridge(app.gitBus, deps.Scope)
		configPath := filepath.Join(cfg.ProjectRoot, ".sylk", "config.yaml")
		app.gitPanel = gitpanel.New(th, app.gitBus, configPath)
		app.commitTree = committree.New(th)
		if sw, err := git.NewStatusWatcher(gc); err == nil {
			app.gitWatcher = sw
		}
		if sg, err := git.NewSafetyGuard(gc, app.gitBus, git.DefaultSafetyConfig(), app.gitWatcher); err == nil {
			app.safetyGuard = sg
		}
	}

	// Pipeline bridge — optional, only created when the pipeline subsystem is active.
	if deps.PipelineManager != nil || deps.VariantRegistry != nil {
		app.pipelineBridge = bridge.NewPipelineBridge("tui.pipeline", deps.VariantRegistry, deps.Scope)
		if deps.PipelineManager != nil {
			deps.PipelineManager.SetOnEvent(app.pipelineBridge.OnPipelineEvent)
		}
	}

	return app
}

func newTUIWALLogger() (*slog.Logger, io.Closer) {
	logger, closer, err := agentlog.NewWALLogger("tui")
	if err != nil {
		return nil, nil
	}
	return logger, closer
}

// Init starts all event bridges, the tick/blink timers, and background LSP provisioning.
func (m *AppModel) Init() tea.Cmd {
	m.viewDirty = true
	m.blinkEpoch = time.Now()
	cmds := []tea.Cmd{
		m.startBridges(),
		m.ensureTick(false),
		m.provisionLSPServers(),
	}
	if bc := m.ensureBlinkAfterDispatch(); bc != nil {
		cmds = append(cmds, bc)
	}
	if !m.nerdFontsDetected {
		cmds = append(cmds, m.installNerdFontsCmd())
	}
	if m.gitWatcher != nil {
		m.gitWatcher.Start(m.ctx)
		cmds = append(cmds, m.gitWatchCmd())
	}
	// Run crash recovery + GC for the safety guard in the background.
	if m.safetyGuard != nil {
		cmds = append(cmds, m.safetyRecoveryCmd())
	}
	return tea.Batch(cmds...)
}

// installNerdFontsCmd returns a Cmd that downloads and installs Nerd Font
// symbols in the background, cancellable via the app context.
func (m *AppModel) installNerdFontsCmd() tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		err := fonts.InstallCtx(ctx)
		return msg.NerdFontsResultMsg{Available: err == nil}
	}
}

// gitWatchCmd returns a Cmd that blocks on the git status watcher channel,
// returning the next status update as a GitStatusMsg. Returns nil when the
// watcher is not active.
func (m *AppModel) gitWatchCmd() tea.Cmd {
	if m.gitWatcher == nil {
		return nil
	}
	ch := m.gitWatcher.Events()
	return func() tea.Msg {
		update, ok := <-ch
		if !ok {
			return nil
		}
		return msg.GitStatusMsg{
			StatusMap:   update.StatusMap,
			TrackedSet:  update.TrackedSet,
			TrackedDirs: update.TrackedDirs,
		}
	}
}

// nudgeGitWatcher signals the watcher to refresh soon. Safe to call when
// the watcher is nil (non-git projects).
func (m *AppModel) nudgeGitWatcher() {
	if m.gitWatcher != nil {
		m.gitWatcher.Nudge()
	}
}

// safetyRecoveryDoneMsg signals that background safety recovery + GC completed.
type safetyRecoveryDoneMsg struct{}

// safetyRecoveryCmd returns a Cmd that runs crash recovery and GC in the
// background, keeping the startup path non-blocking.
func (m *AppModel) safetyRecoveryCmd() tea.Cmd {
	sg := m.safetyGuard
	return func() tea.Msg {
		// Phase 1: Recover incomplete operations.
		ops, err := sg.RecoverOnStartup()
		if err == nil {
			for _, op := range ops {
				_ = sg.ResolveIncomplete(op, git.RecoveryResume)
			}
		}

		// Phase 2: GC old snapshots and journal segments.
		_, _ = sg.GCSnapshots()
		_ = sg.GCJournal()

		return safetyRecoveryDoneMsg{}
	}
}

// syncStagedFiles pushes the git panel's staged-files state to the commit tree
// so the [Commit] badge reflects current staging.
func (m *AppModel) syncStagedFiles() {
	if m.commitTree != nil && m.gitPanel != nil {
		m.commitTree.SetHasStagedFiles(m.gitPanel.HasAnyStagedFiles())
	}
}

// Update dispatches incoming messages and ensures the tick/blink/LSP-flush
// chains are running at the appropriate speed for the current activity level.
func (m *AppModel) Update(raw tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.dispatch(raw)

	switch raw.(type) {
	case msg.TickMsg:
		// Tick chain self-manages via continueTickChain in handleTick.
		// viewDirty set by handleTick when visual work occurred.
		return m, cmd
	case msg.BlinkMsg:
		// Blink chain self-manages via handleBlink. Phase sync and
		// viewDirty are handled by centralized blink logic in View().
		return m, cmd
	case msg.LSPFlushMsg:
		// One-shot timer; no view change.
		return m, cmd
	case tea.KeyMsg:
		// Interactive events reset blink phase.
		m.viewDirty = true
		m.blinkGen++
		m.blinkEpoch = time.Now()
		return m, m.postDispatchCmds(cmd, true)
	case tea.MouseMsg:
		typed := raw.(tea.MouseMsg)
		if typed.Action == tea.MouseActionMotion {
			// Motion events: let components set their own dirty flags.
			// Don't reset blink or schedule blink timers — mouse movement
			// should not interfere with cursor blink cadence.
			m.viewDirty = true
			return m, m.postDispatchCmds(cmd, false)
		}
		m.viewDirty = true
		m.blinkGen++
		m.blinkEpoch = time.Now()
		return m, m.postDispatchCmds(cmd, true)
	default:
		// Background messages (git, LSP, streaming, etc.) may change visual
		// state but must NOT reset the blink cycle or schedule new blink
		// timers — otherwise each background message creates a 530ms timer,
		// causing duplicate blink chains and burning CPU at idle.
		m.viewDirty = true
		return m, m.postDispatchCmds(cmd, false)
	}
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
func (m *AppModel) dispatch(raw tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := raw.(type) {
	case tea.WindowSizeMsg:
		return m, m.handleResize(typed)
	case tea.KeyMsg:
		return m.handleKey(typed)
	case tea.MouseMsg:
		return m, m.handleMouse(typed)
	case msg.SubmitPromptMsg:
		if m.editCmdInput {
			m.editCmdInput = false
			m.input.SetLineStyler(nil)
			exCmd := m.handleExCommand(typed.Text)
			if m.viewMode == ViewEdit {
				m.focusCodePanel()
				m.syncFocusState()
			}
			return m, exCmd
		}
		if m.viewMode == ViewEdit && isInlineExCommand(typed.Text) {
			exCmd := m.handleExCommand(typed.Text)
			if m.viewMode == ViewEdit {
				m.focusCodePanel()
				m.syncFocusState()
			}
			return m, exCmd
		}
		if cmd, ok := parseChatCommand(typed.Text); ok {
			return m, m.handleChatCommand(cmd, typed)
		}
		return m, m.handleSubmit(typed)
	case msg.LoginResultMsg:
		return m, m.handleLoginResult(typed)
	case oauthSessionStartedMsg:
		return m, m.handleOAuthSessionStarted(typed)
	case msg.InterruptMsg:
		return m, m.handleInterrupt()
	case msg.QuitConfirmMsg:
		return m, m.handleQuit()
	case msg.TickMsg:
		return m, m.handleTick(typed)
	case msg.DecorTickMsg:
		return m, m.handleDecorTick(typed)
	case msg.BlinkMsg:
		return m, m.handleBlink(typed)
	case msg.LSPFlushMsg:
		return m, m.handleLSPFlush(typed)
	case msg.QueueAdvanceMsg:
		return m, m.dispatchQueueHead()
	case msg.FocusPanelMsg:
		return m, m.handleFocusPanel(typed)
	case msg.PlanUpdateMsg:
		return m, m.handlePlanUpdate(typed)
	case msg.PlanViewToggleMsg:
		return m, m.handlePlanViewToggle()
	case msg.PipelineStateMsg:
		comp, cmd := m.agentPanel.Update(typed)
		m.agentPanel = comp.(*agentpkg.Model)
		m.comp.MarkDirty(compositor.SlotLeft)
		return m, cmd
	case msg.VariantStateMsg:
		comp, cmd := m.agentPanel.Update(typed)
		m.agentPanel = comp.(*agentpkg.Model)
		m.comp.MarkDirty(compositor.SlotLeft)
		return m, cmd
	case msg.OpenEditorMsg:
		return m, m.handleOpenEditor(typed)
	case msg.CloseEditorMsg:
		return m, m.handleCloseEditor()
	case msg.FileOpenMsg:
		// Dismiss preview only when opening a NEW file (not already a tab).
		// Tab switches (shift+tab) should keep the preview visible.
		if m.hasPreview() && !m.isExistingTab(typed.Path) {
			m.dismissPreview()
		}
		return m, m.handleFileOpen(typed)
	case msg.FilePreviewMsg:
		return m, m.handleFilePreview(typed)
	case msg.FileTreeEntryCreatedMsg:
		m.nudgeGitWatcher()
		if !typed.IsDir {
			return m, func() tea.Msg { return msg.FileOpenMsg{Path: typed.Path} }
		}
		return m, nil
	case msg.FileTreeEntryRenamedMsg:
		m.nudgeGitWatcher()
		return m, nil
	case msg.FileTreeEntryDeletedMsg:
		m.nudgeGitWatcher()
		if m.codePanel.FilePath() == typed.Path {
			m.codePanel.ClearFile()
		}
		if m.focusedEditor().FilePath() == typed.Path {
			m.focusedEditor().ClearFile()
		}
		m.fileTree.SetActiveFile("")
		return m, nil
	case msg.GitStatusMsg:
		m.fileTree.SetGitStatus(typed.StatusMap, typed.TrackedSet, typed.TrackedDirs)
		cmds := []tea.Cmd{m.gitWatchCmd(), m.detectSequencerStateCmd()}
		if m.viewMode == ViewGit {
			cmds = append(cmds, m.gitPanel.LoadData(), m.loadGitBranchesCmd())
			if m.commitTree.InCommitView() {
				defaultBranch := m.commitTree.GetDefaultBranch()
				cmds = append(cmds, m.loadBranchDAGCmd(m.commitTree.ActiveBranch(), defaultBranch))
			}
		}
		return m, tea.Batch(cmds...)
	case gitQuickStatusMsg:
		if m.commitTree != nil {
			m.commitTree.SetWorkingTreeStatus(typed.dirty, typed.conflicts)
			m.commitTree.SetHasIndexStaged(typed.hasIndexStaged)
		}
		if m.gitPanel != nil {
			m.gitPanel.SetHasStash(typed.hasStash)
		}
		return m, nil
	case gitBranchesLoadedMsg:
		if m.commitTree != nil {
			m.commitTree.SetBranches(typed.branches, typed.defaultBranch)
			m.commitTree.SetWorkingTreeStatus(typed.dirty, typed.conflicts)
			m.commitTree.SetHasIndexStaged(typed.hasIndexStaged)
		}
		if m.gitPanel != nil {
			m.gitPanel.SetHasStash(typed.hasStash)
		}
		// Fire async divergence + stash badge loading.
		return m, tea.Batch(m.computeDivergenceBatchCmd(), m.loadBranchStashesCmd())

	case msg.DivergenceLoadedMsg:
		if m.gitPanel != nil && len(typed.Info) > 0 {
			m.gitPanel.SetDivergence(typed.Info)
		}
		return m, nil

	case branchStashesLoadedMsg:
		if m.gitPanel != nil && len(typed.stashes) > 0 {
			m.gitPanel.SetBranchStashes(typed.stashes)
		}
		return m, nil

	case msg.SequencerFileStateMsg:
		if typed.State != nil && typed.State.Active {
			step := fmt.Sprintf("%d/%d", typed.State.CurrentStep+1, typed.State.TotalSteps)
			prompt := strings.ToTitle(typed.State.Type[:1]) + typed.State.Type[1:] + "ing " + step
			if typed.State.OntoRef != "" {
				prompt += " onto " + typed.State.OntoRef
			}
			m.statusBar.SetPrompt(prompt)
			m.statusBar.SetMode(strings.ToUpper(typed.State.Type))
		} else {
			m.statusBar.ClearPrompt()
		}
		return m, nil

	case committree.BranchSelectedMsg:
		if m.viewMode == ViewGit && m.gitBus != nil {
			defaultBranch := ""
			if m.commitTree != nil {
				defaultBranch = m.commitTree.GetDefaultBranch()
			}
			return m, m.loadBranchDAGCmd(typed.Name, defaultBranch)
		}
		return m, nil

	case gitpanel.BranchCheckedOutMsg:
		m.statusBar.SetFlash("Switched to " + typed.Name)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		// Check if the new branch has a saved stash.
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
		return m, tea.Batch(cmds...)

	case gitpanel.CommitCheckedOutMsg:
		m.statusBar.SetFlash("Checked out " + typed.Hash[:min(len(typed.Hash), 8)])
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return m, tea.Batch(cmds...)

	case gitpanel.BranchCheckoutBlockedMsg:
		m.statusBar.SetFlash(typed.Reason)
		return m, nil

	case committree.BranchSwitchMsg:
		if m.gitBus != nil {
			bus := m.gitBus
			name := typed.Name
			return m, func() tea.Msg {
				// Auto-stash dirty files for the current branch before switching.
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
		}
		return m, nil

	case committree.BranchDeleteMsg:
		if m.gitBus != nil {
			bus := m.gitBus
			name := typed.Name
			return m, func() tea.Msg {
				if err := bus.DeleteBranch(name); err != nil {
					return branchDeleteFailedMsg{reason: err.Error()}
				}
				return branchDeletedMsg{name: name}
			}
		}
		return m, nil

	case branchDeletedMsg:
		m.statusBar.SetFlash("Deleted branch " + typed.name)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.loadGitBranchesCmd())
		return m, tea.Batch(cmds...)

	case branchDeleteFailedMsg:
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	case mergeBranchDoneMsg:
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
		return m, tea.Batch(cmds...)

	case mergeBranchFailedMsg:
		if m.mergeDiffView != nil {
			m.mergeDiffView.SetMergeError("Merge failed: " + typed.reason)
		} else {
			m.statusBar.SetFlash("Merge failed: " + typed.reason)
		}
		return m, nil

	case committree.CreateBranchRequestMsg:
		if m.gitBus != nil {
			if typed.AtHash == "" {
				m.commitTree.RecordBranchParent(typed.Name, typed.ParentBranch)
			}
			bus := m.gitBus
			name, parent, atHash := typed.Name, typed.ParentBranch, typed.AtHash
			return m, func() tea.Msg {
				var hash string
				if atHash != "" {
					hash = atHash
				} else {
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
		}
		return m, nil

	case branchCreatedMsg:
		m.statusBar.SetFlash("Created branch " + typed.name)
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.loadGitBranchesCmd())
		return m, tea.Batch(cmds...)

	case branchCreateFailedMsg:
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	case committree.CommitRequestMsg:
		if m.gitBus != nil && m.gitPanel != nil {
			paths := m.gitPanel.StagedFilePaths()
			message := typed.Message
			if len(paths) == 0 {
				m.statusBar.SetFlash("No files staged")
				return m, nil
			}
			// Route through pre-commit pipeline (large file + secret scan).
			return m, m.preCommitCheckCmd(paths, message)
		}
		return m, nil

	case msg.PreCommitCleanMsg:
		// Pre-commit checks passed — proceed with actual commit.
		bus := m.gitBus
		paths, message := typed.Paths, typed.Message
		return m, func() tea.Msg {
			if err := bus.CommitFiles(paths, message); err != nil {
				return commitFailedMsg{reason: err.Error()}
			}
			return commitSucceededMsg{message: message}
		}

	case msg.LargeFilesDetectedMsg:
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
		return m, nil

	case msg.SecretsDetectedMsg:
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
		return m, nil

	case commitSucceededMsg:
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
		return m, tea.Batch(cmds...)

	case commitFailedMsg:
		m.statusBar.SetFlash(typed.reason)
		if m.commitTree != nil {
			_, doneCmd := m.commitTree.Update(committree.CommitDoneMsg{OK: false, Message: typed.reason})
			return m, doneCmd
		}
		return m, nil

	case gitpanel.StashRequestMsg:
		if m.gitBus != nil && m.commitTree != nil {
			m.commitTree.SetLoadingMessage(fmt.Sprintf("Stashing %d files...", typed.Count))
			bus := m.gitBus
			paths := typed.Paths
			count := typed.Count
			return m, func() tea.Msg {
				if err := bus.StashFiles(paths); err != nil {
					return stashFailedMsg{reason: err.Error()}
				}
				return stashSucceededMsg{count: count}
			}
		}
		return m, nil

	case stashSucceededMsg:
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
		return m, tea.Batch(cmds...)

	case stashFailedMsg:
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	case gitpanel.UnstashRequestMsg:
		if m.gitBus != nil && m.commitTree != nil {
			m.commitTree.SetLoadingMessage("Unstashing files...")
			bus := m.gitBus
			return m, func() tea.Msg {
				if err := bus.UnstashFiles(); err != nil {
					return unstashFailedMsg{reason: err.Error()}
				}
				return unstashSucceededMsg{}
			}
		}
		return m, nil

	case unstashSucceededMsg:
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
		return m, tea.Batch(cmds...)

	case unstashFailedMsg:
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	case committree.PullBranchMsg:
		if m.gitBus != nil && m.commitTree != nil {
			m.commitTree.SetLoadingMessage("Pulling " + typed.Name + "...")
			bus := m.gitBus
			name := typed.Name
			return m, func() tea.Msg {
				if err := bus.PullBranch(name, ""); err != nil {
					return pullFailedMsg{reason: err.Error()}
				}
				return pullSucceededMsg{name: name}
			}
		}
		return m, nil

	case pullSucceededMsg:
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
		return m, tea.Batch(cmds...)

	case pullFailedMsg:
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	case committree.PushBranchMsg:
		if m.gitBus != nil && m.commitTree != nil {
			m.commitTree.SetLoadingMessage("Pushing " + typed.Name + "...")
			bus := m.gitBus
			name := typed.Name
			return m, func() tea.Msg {
				if err := bus.PushBranch(name, ""); err != nil {
					return pushFailedMsg{reason: err.Error()}
				}
				return pushSucceededMsg{name: name}
			}
		}
		return m, nil

	case pushSucceededMsg:
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
		return m, tea.Batch(cmds...)

	case pushFailedMsg:
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	case committree.ResetRequestMsg:
		if m.gitBus != nil && m.commitTree != nil {
			m.commitTree.SetLoadingMessage("Resetting (" + typed.Mode + ")...")
			bus := m.gitBus
			hash, modeStr := typed.Hash, typed.Mode
			var mode git.ResetMode
			switch modeStr {
			case "hard":
				mode = git.ResetHard
			case "mixed":
				mode = git.ResetMixed
			case "soft":
				mode = git.ResetSoft
			}
			return m, func() tea.Msg {
				if err := bus.Reset(hash, mode); err != nil {
					return resetFailedMsg{reason: err.Error()}
				}
				return resetSucceededMsg{mode: modeStr}
			}
		}
		return m, nil

	case resetSucceededMsg:
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
		return m, tea.Batch(cmds...)

	case resetFailedMsg:
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	case committree.RevertRequestMsg:
		if m.gitBus != nil && m.commitTree != nil {
			m.commitTree.SetLoadingMessage("Reverting...")
			bus := m.gitBus
			hash := typed.Hash
			return m, func() tea.Msg {
				if err := bus.Revert(hash); err != nil {
					return revertFailedMsg{reason: err.Error()}
				}
				return revertSucceededMsg{hash: hash}
			}
		}
		return m, nil

	case revertSucceededMsg:
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
		return m, tea.Batch(cmds...)

	case revertFailedMsg:
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	case committree.CommitCheckoutRequestMsg:
		if m.gitBus != nil {
			bus := m.gitBus
			hash := typed.Hash
			return m, func() tea.Msg {
				if err := bus.CheckoutCommit(hash); err != nil {
					return commitCheckoutFailedMsg{reason: err.Error()}
				}
				return commitCheckoutSucceededMsg{hash: hash}
			}
		}
		return m, nil

	case commitCheckoutSucceededMsg:
		m.statusBar.SetFlash("Checked out " + typed.hash[:min(len(typed.hash), 8)])
		m.nudgeGitWatcher()
		var cmds []tea.Cmd
		if m.gitPanel != nil {
			cmds = append(cmds, m.gitPanel.LoadData())
		}
		cmds = append(cmds, m.quickGitStatusCmd(), m.loadGitBranchesCmd())
		return m, tea.Batch(cmds...)

	case commitCheckoutFailedMsg:
		m.statusBar.SetFlash(typed.reason)
		return m, nil

	// --- Sequencer operations (cherry-pick, rebase, merge) ---

	case committree.CherryPickRequestMsg:
		if m.gitBus != nil && m.commitTree != nil {
			m.commitTree.SetLoadingMessage("Checking cherry-pick...")
			m.mergeLabels = nil
			hashes, target := typed.Hashes, typed.TargetBranch
			m.pendingSeqOp = &pendingSequencerOp{
				op: "cherry-pick", hashes: hashes, target: target,
			}
			// Run integration detection first.
			return m, m.detectIntegrationCmd(hashes, target, nil)
		}
		return m, nil

	case committree.RebaseStartMsg:
		if m.gitBus != nil && m.commitTree != nil {
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
			// Run conflict preview.
			return m, m.conflictPreviewRebaseCmd(onto, typed.SourceBranch)
		}
		return m, nil

	case sequencerResultMsg:
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
			return m, nil
		}
		// nil status = completed cleanly.
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
		return m, tea.Batch(cmds...)

	case sequencerFailedMsg:
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
		return m, tea.Batch(cmds...)

	case sequencerAbortedMsg:
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
		return m, tea.Batch(cmds...)

	case sequencerAbortFailedMsg:
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(typed.reason)
		m.nudgeGitWatcher()
		return m, nil

	case conflictview.ConflictResolveFileMsg:
		bus := m.gitBus
		path := typed.Path
		res := int(typed.Resolution)
		oursHash := typed.OursHash
		theirsHash := typed.TheirsHash
		return m, func() tea.Msg {
			if err := bus.ResolveConflictFile(path, res, oursHash, theirsHash); err != nil {
				return conflictResolveFailedMsg{path: path, reason: err.Error()}
			}
			return conflictResolveWrittenMsg{path: path}
		}

	case conflictview.ConflictWriteContentMsg:
		bus := m.gitBus
		return m, func() tea.Msg {
			if err := bus.WriteWorktreeFile(typed.Path, typed.Content); err != nil {
				return conflictResolveFailedMsg{path: typed.Path, reason: err.Error()}
			}
			return conflictResolveWrittenMsg{path: typed.Path}
		}

	case conflictview.SyntaxValidationRequestMsg:
		if m.conflictView == nil {
			return m, nil
		}
		entries := m.conflictView.Entries()
		return m, func() tea.Msg {
			warnings := conflictview.ValidateResolvedFiles(entries)
			return conflictview.SyntaxValidationResultMsg{Warnings: warnings}
		}

	case conflictview.SyntaxValidationResultMsg:
		if len(typed.Warnings) == 0 || typed.Proceed {
			// No warnings or user confirmed — proceed with continue.
			if m.conflictView != nil {
				m.conflictView.SetLoading(true)
			}
			bus := m.gitBus
			return m, func() tea.Msg {
				status, err := bus.SequencerContinue()
				if err != nil {
					return sequencerFailedMsg{reason: err.Error()}
				}
				return sequencerResultMsg{status: status}
			}
		}
		// Show warning modal.
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
		return m, nil

	case conflictview.SequencerContinueMsg:
		if m.conflictView != nil {
			m.conflictView.SetLoading(true)
		}
		bus := m.gitBus
		return m, func() tea.Msg {
			status, err := bus.SequencerContinue()
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}

	case conflictview.SequencerBypassMsg:
		bus := m.gitBus
		return m, func() tea.Msg {
			status, err := bus.SequencerBypass()
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}

	case conflictview.SequencerAbortMsg:
		// Preserve state before aborting (backup ref + stash dirty files).
		return m, m.preserveAbortCmd("sequencer")

	case conflictview.BaseContentRequestMsg:
		return m, m.fetchBaseContentCmd(typed.Path, typed.BaseHash)

	case conflictview.BaseContentResponseMsg:
		if m.conflictView != nil && typed.Err == nil {
			m.conflictView.SetBaseContent(typed.Path, typed.Content)
		}
		return m, nil

	case conflictview.StepPreviewRequestMsg:
		return m, m.fetchStepPreviewCmd(typed.Hash)

	case conflictview.StepPreviewResponseMsg:
		if m.conflictView != nil && typed.Preview != nil {
			m.conflictView.SetStepPreview(typed.Preview)
		}
		return m, nil

	case conflictview.SequencerUndoStepMsg:
		return m, m.sequencerUndoStepCmd()

	case conflictResolveWrittenMsg:
		m.nudgeGitWatcher()
		return m, nil

	case conflictResolveFailedMsg:
		m.statusBar.SetFlash("Resolve failed: " + typed.reason)
		return m, nil

	case msg.ConflictPreviewMsg:
		return m, m.handleConflictPreview(typed)

	case msg.IntegrationDetectedMsg:
		return m, m.handleIntegrationDetected(typed)

	case msg.AbortPreservedMsg:
		return m, m.handleAbortPreserved(typed)

	case msg.BranchStashAvailableMsg:
		return m, m.handleBranchStashAvailable(typed)

	case msg.BranchStashRestoredMsg:
		if typed.Err != nil {
			m.statusBar.SetFlash("Stash restore failed: " + typed.Err.Error())
		} else {
			m.statusBar.SetFlash("Branch stash restored")
			m.nudgeGitWatcher()
		}
		return m, nil

	case editor.ConflictResolvedMsg:
		if m.conflictViewActive && m.conflictView != nil {
			m.conflictView.MarkResolvedByPath(typed.Path)
		}
		return m, nil

	case msg.GitOpEventMsg:
		// Nudge unconditionally — mutations may have partially changed state
		// even on error (e.g. sequencer processed some steps before failing).
		m.nudgeGitWatcher()
		return m, nil

	case gitBranchDAGLoadedMsg:
		if m.commitTree != nil {
			m.commitTree.SetDAGNodesWithStats(typed.nodes, typed.stats, typed.graphRows, typed.maxGraphLane)
		}
		return m, nil
	case gitBranchFullyLoadedMsg:
		if m.commitTree != nil && typed.branch == m.commitTree.ActiveBranch() {
			if len(typed.nodes) == 0 {
				m.commitTree.ExitToBranches()
				m.statusBar.SetFlash("No commits found for " + typed.branch)
			} else {
				m.commitTree.SetNodesWithStats(typed.nodes, typed.stats, typed.hasMore)
			}
		}
		return m, nil
	case committree.LoadMoreMsg:
		return m, m.loadMoreCommitsCmd()
	case gitMoreCommitsLoadedMsg:
		if m.commitTree != nil && typed.branch == m.commitTree.ActiveBranch() {
			m.commitTree.AppendNodesWithStats(typed.nodes, typed.stats, typed.hasMore)
		}
		return m, nil

	case committree.DiffCompareMsg:
		m.diffHashes = typed.Hashes
		m.diffLabels = nil
		m.setDiffLoading(true)
		return m, m.fetchDiffDataCmd(m.diffHashes, CompareModeChain)

	case committree.BranchDiffCompareMsg:
		m.diffHashes = typed.Hashes
		m.diffLabels = typed.Names
		m.setDiffLoading(true)
		return m, m.fetchDiffDataCmd(m.diffHashes, CompareModeChain)

	case msg.DiffViewDataMsg:
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
		return m, nil

	case diffview.ExitDiffViewMsg:
		m.exitDiffView()
		return m, nil

	case diffview.ChangeCompareModeMsg:
		if len(m.diffHashes) >= 2 {
			m.setDiffLoading(true)
			return m, m.fetchDiffDataCmd(m.diffHashes, typed.Mode)
		}
		return m, nil

	case committree.MergeDiffCompareMsg:
		m.mergeHashes = typed.Hashes
		m.mergeLabels = typed.Names
		m.setMergeDiffLoading(true)
		return m, m.fetchMergeDiffDataCmd(typed.Hashes, typed.Names)

	case msg.MergeDiffViewDataMsg:
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
		return m, nil

	case mergediff.ExitMergeDiffViewMsg:
		m.exitMergeDiffView()
		return m, nil

	case mergediff.MergeBranchMsg:
		if m.gitBus != nil && len(m.mergeLabels) >= 2 {
			m.pendingSeqOp = &pendingSequencerOp{
				op:     "merge",
				hashes: []string{m.mergeLabels[0]},
				target: m.mergeLabels[1],
				delete: typed.DeleteSource,
			}
			return m, m.conflictPreviewMergeCmd(m.mergeLabels[0], m.mergeLabels[1])
		}
		return m, m.executeMergeBranch(typed.DeleteSource)

	case msg.FileReplacedMsg:
		return m, m.handleFileReplaced(typed)
	case msg.MultiFileReplaceDoneMsg:
		return m, m.handleMultiFileReplaceDone(typed)
	case msg.StreamStartMsg:
		return m, m.handleStreamStartTelemetry(typed)
	case msg.StreamChunkMsg:
		return m, m.handleStreamChunkTelemetry(typed)
	case msg.StreamProgressMsg:
		return m, m.handleStreamProgressTelemetry(typed)
	case msg.StreamCompleteMsg:
		return m, m.handleStreamCompleteTelemetry(typed)
	case msg.StreamErrorMsg:
		return m, m.handleStreamErrorTelemetry(typed)
	case msg.StreamRerouteMsg:
		return m, m.handleStreamReroute(typed)
	case msg.GuideResponseMsg:
		return m, m.handleGuideResponse(typed)
	case modal.ModalClosedMsg:
		return m, m.handleModalClosed(typed.Result)
	case msg.LSPDiagnosticMsg:
		return m, m.handleLSPDiagnostic(typed)
	case msg.LSPProvisionDoneMsg:
		if typed.Err == nil {
			detect.ClearCache()
		}
		return m, nil
	case msg.LSPServerMissingMsg:
		return m, m.handleLSPServerMissing(typed)
	case msg.LSPServerInstalledMsg:
		return m, m.handleLSPServerInstalled(typed)
	case mode.StandaloneResult:
		// Forward standalone operator results (gd) to the inline editor.
		_, cmd := m.focusedEditor().Update(typed)
		return m, cmd
	case msg.LSPHoverRequestMsg:
		// Sync hover tracking so the staleness check in LSPHoverMsg passes
		// regardless of whether hover was triggered by K or mouse.
		m.hoverMouseLine = typed.Line
		m.hoverMouseCol = typed.Col
		wordStart, _ := m.focusedEditor().WordBoundsAt(typed.Line, typed.Col)
		m.hoverMouseWordStart = wordStart
		m.hoverForPreview = false
		m.pendingHoverSymbol = ""
		m.pendingHoverPkgPath = ""
		return m, tea.Batch(
			m.lspHoverCmd(typed.FilePath, typed.Line, typed.Col),
			m.lspDefinitionCmd(typed.FilePath, typed.Line, typed.Col, true),
		)
	case msg.LSPMouseHoverTickMsg:
		return m, m.handleMouseHoverTick(typed)
	case msg.LSPHoverMsg:
		if m.hoverForPreview {
			return m, m.handlePreviewHoverMsg(typed)
		}
		// Discard stale hover responses if the mouse has moved to a different word.
		wordStart, _ := m.focusedEditor().WordBoundsAt(typed.Line, typed.Col)
		if typed.Line != m.hoverMouseLine || wordStart != m.hoverMouseWordStart {
			return m, nil
		}
		m.focusedEditor().Update(typed)
		// Apply definition info that arrived before the hover content.
		if m.focusedEditor().HoverActive() && m.pendingHoverSymbol != "" {
			m.focusedEditor().SetHoverDefinition(m.pendingHoverSymbol, m.pendingHoverPkgPath)
			m.pendingHoverSymbol = ""
			m.pendingHoverPkgPath = ""
		}
		return m, nil
	case msg.LSPDefinitionRequestMsg:
		return m, m.lspDefinitionCmd(typed.FilePath, typed.Line, typed.Col, typed.ForHover)
	case msg.LSPDefinitionMsg:
		if typed.ForHover {
			return m, m.handleHoverDefinition(typed)
		}
		_, cmd := m.focusedEditor().Update(typed)
		return m, cmd
	case msg.LSPCompletionRequestMsg:
		// Flush pending didChange so the server sees the latest buffer content.
		var flushContent string
		if m.focusedEditor().LSPDirty() {
			m.focusedEditor().ClearLSPDirty()
			flushContent = m.focusedEditor().Content()
		}
		return m, m.lspCompletionCmd(typed.FilePath, typed.Line, typed.Col, flushContent)
	case msg.LSPCompletionMsg:
		m.focusedEditor().Update(typed)
		return m, nil
	case msg.LSPSignatureHelpRequestMsg:
		// Flush pending didChange so the server sees the trigger character.
		var flushContent string
		if m.focusedEditor().LSPDirty() {
			m.focusedEditor().ClearLSPDirty()
			flushContent = m.focusedEditor().Content()
		}
		return m, m.lspSignatureHelpCmd(typed.FilePath, typed.Line, typed.Col, flushContent)
	case msg.LSPSignatureHelpMsg:
		m.focusedEditor().Update(typed)
		return m, nil
	case msg.LSPDocHighlightTickMsg:
		return m, m.handleDocHighlightTick(typed)
	case msg.LSPDocumentHighlightMsg:
		m.focusedEditor().Update(typed)
		return m, nil
	case msg.LSPReferencesRequestMsg:
		return m, m.lspReferencesCmd(typed.FilePath, typed.Line, typed.Col)
	case msg.LSPReferencesMsg:
		return m, m.handleLSPReferences(typed)
	case msg.LSPDocumentSymbolMsg:
		return m, m.handleLSPDocumentSymbol(typed)
	case msg.LSPFormatRequestMsg:
		var flushContent string
		if m.focusedEditor().LSPDirty() {
			m.focusedEditor().ClearLSPDirty()
			flushContent = m.focusedEditor().Content()
		}
		return m, m.lspFormatCmd(typed.FilePath, flushContent, m.focusedEditor().EditGeneration())
	case msg.LSPPrepareRenameMsg:
		return m, m.handleLSPPrepareRename(typed)
	case msg.LSPRenameMsg:
		return m, m.handleLSPRename(typed)
	case msg.LSPFormatMsg:
		return m, m.handleLSPFormat(typed)
	case msg.TabNextMsg:
		return m, m.nextTab()
	case msg.TabPrevMsg:
		return m, m.prevTab()
	case msg.TabJumpMsg:
		return m, m.switchToTab(typed.Index)
	case msg.TabCloseRequestMsg:
		return m, m.closeTabByPath(typed.Path)
	case msg.EscDisambiguateTickMsg:
		if m.escPending && typed.Gen == m.escGen {
			m.escPending = false
			m.escGen++
			return m.dispatchKey(m.escKey)
		}
		return m, nil
	case msg.ChordBlockedExpireMsg:
		if m.chordBlocked {
			m.chord = chordNone
			m.chordBlocked = false
		}
		return m, nil
	case msg.NerdFontsResultMsg:
		// Don't switch icon mode mid-session — the terminal won't render
		// newly-installed Nerd Font glyphs until restarted. The install
		// takes effect on the next app launch.
		return m, nil
	default:
		return m, m.propagate(raw)
	}
}

// View renders the complete TUI layout using the frame compositor.
func (m *AppModel) View() string {
	if !m.ready {
		return ""
	}

	// Compute cursor visibility once per frame from wall clock.
	if m.needsBlink() {
		m.cursorVisible = m.blinkPhase()
		if m.cursorVisible != m.lastRenderedPhase {
			m.lastRenderedPhase = m.cursorVisible
			m.viewDirty = true
			m.blinkDirty = true
		}
	} else {
		m.cursorVisible = true
	}

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

// Shutdown gracefully stops all bridges and waits for goroutine cleanup.
func (m *AppModel) Shutdown() error {
	m.cancel() // Cancel all in-flight Cmd contexts first.
	m.activityBridge.Stop()
	m.sessionBridge.Stop()
	m.streamBridge.Stop()
	m.guideBridge.Stop()
	m.lspBridge.Stop()
	if m.gitBridge != nil {
		m.gitBridge.Stop()
	}
	if m.pipelineBridge != nil {
		m.pipelineBridge.Stop()
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
	if err := m.deps.Scope.Shutdown(shutdownGrace, shutdownHard); err != nil {
		errs = append(errs, err)
	}
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
	m.width = sz.Width
	m.height = sz.Height
	m.ready = true

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

func (m *AppModel) dispatchKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C / Ctrl+Shift+C: copy selection in edit mode, otherwise interrupt.
	ks := key.String()
	if ks == "ctrl+c" || ks == "ctrl+shift+c" {
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
	}

	// Save-before-close prompt intercepts all keys while active.
	if m.pendingClosePrompt {
		return m.handleSavePromptKey(key)
	}

	// Alt+Shift+U then A/S/P → toggle [All], stash, or unstash.
	// Must precede Select-all so Alt+A doesn't get consumed.
	if m.pendingUncommittedAll {
		m.pendingUncommittedAll = false
		if m.gitPanel != nil {
			switch {
			case ks == "a" || ks == "A" || ks == "alt+a" || ks == "alt+A":
				m.gitPanel.ToggleUncommittedAll()
				return m, nil
			case ks == "s" || ks == "S" || ks == "alt+s" || ks == "alt+S":
				return m, m.gitPanel.StashAll()
			case ks == "p" || ks == "P" || ks == "alt+p" || ks == "alt+P":
				return m, m.gitPanel.TriggerUnstash()
			}
		}
	}

	// Select all in the focused component (Alt+Shift+A).
	if key.String() == "alt+A" {
		switch {
		case m.viewMode == ViewEdit && m.isEditorFocused():
			m.focusedEditor().SelectAll()
		case m.focus.Current() == component.FocusInput:
			m.input.SelectAll()
		}
		return m, nil
	}

	// Ctrl+X / Ctrl+Shift+X: cut selection to clipboard.
	if ks == "ctrl+x" || ks == "ctrl+shift+x" {
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
	}

	// Undo in the editor (Alt+Z, only when editor is focused).
	if key.String() == "alt+z" && m.viewMode == ViewEdit && m.isEditorFocused() {
		m.focusedEditor().Undo()
		return m, nil
	}

	// Redo in the editor (Alt+Shift+Z, only when editor is focused).
	if key.String() == "alt+Z" && m.viewMode == ViewEdit && m.isEditorFocused() {
		m.focusedEditor().Redo()
		return m, nil
	}

	// Queue shortcuts (active regardless of view mode).
	// Alt+Shift+Space: toggle queue pause/resume.
	if ks == "alt+ " && !m.promptQueue.IsEmpty() {
		paused := m.promptQueue.TogglePause()
		if paused {
			m.statusBar.SetFlash("Queue paused")
		} else {
			m.statusBar.SetFlash("Queue resumed")
			// Try to advance if we just unpaused and the route is clear.
			if strings.TrimSpace(m.activeRoute.CorrelationID) == "" {
				return m, m.tryAdvanceQueue()
			}
		}
		m.comp.MarkDirty(compositor.SlotQueue)
		m.viewDirty = true
		return m, nil
	}
	// Alt+K: cancel top pending queue entry.
	if ks == "alt+k" && !m.promptQueue.IsEmpty() {
		pending := m.promptQueue.PendingEntries()
		if len(pending) > 0 {
			m.promptQueue.Cancel(pending[0].ID)
			m.recalcLayout()
			m.comp.MarkDirty(compositor.SlotQueue)
			m.viewDirty = true
			m.statusBar.SetFlash("Cancelled queued prompt")
		}
		return m, nil
	}
	// Alt+Shift+K: cancel all queued entries.
	if ks == "alt+K" && !m.promptQueue.IsEmpty() {
		n := m.promptQueue.CancelAll()
		if n > 0 {
			m.recalcLayout()
			m.comp.MarkDirty(compositor.SlotQueue)
			m.viewDirty = true
			m.statusBar.SetFlash(fmt.Sprintf("Cancelled %d queued prompts", n))
		}
		return m, nil
	}

	// Alt+R: find references for symbol under cursor (edit mode).
	if ks == "alt+r" && m.viewMode == ViewEdit && m.isEditorFocused() {
		fp := m.focusedEditor().FilePath()
		if fp != "" {
			return m, m.lspReferencesCmd(fp, m.focusedEditor().CursorLine(), m.focusedEditor().CursorCol())
		}
		return m, nil
	}

	// Alt+Shift+. (alt+>): toggle document symbols for current file (edit mode).
	if ks == "alt+>" && m.viewMode == ViewEdit {
		if m.fileTree.InDocSymbolsMode() {
			m.fileTree.ExitDocSymbols()
			return m, nil
		}
		fp := m.focusedEditor().FilePath()
		if fp != "" {
			return m, m.lspDocumentSymbolCmd(fp)
		}
		return m, nil
	}

	// Alt+1..9: set or teleport warp point.
	if len(ks) == 5 && ks[:4] == "alt+" && ks[4] >= '1' && ks[4] <= '9' {
		idx := int(ks[4] - '1')
		if m.warpPoints[idx] != nil {
			return m, m.teleportToWarp(idx)
		}
		if m.viewMode == ViewEdit {
			m.setWarpPoint(idx)
		}
		return m, nil
	}

	// Alt+Shift+1..9: clear warp point.
	if len(ks) == 5 && ks[:4] == "alt+" {
		if idx, ok := shiftDigitSlot[ks[4]]; ok {
			m.clearWarpPoint(idx)
			return m, nil
		}
	}

	// Alt+T toggles the tabs panel in the file tree (edit mode only).
	if ks == "alt+t" && m.viewMode == ViewEdit {
		return m, m.toggleTabsPanel()
	}

	// Alt+Enter: dismiss preview when preview focused, close tab otherwise.
	if ks == "alt+enter" && m.viewMode == ViewEdit {
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
	}

	// Alt+C switches to chat mode.
	if key.String() == "alt+c" {
		return m, m.switchToChatMode()
	}

	// Alt+G switches to git mode.
	if key.String() == "alt+g" {
		if m.viewMode == ViewGit {
			return m, nil
		}
		if m.gitPanel == nil {
			m.statusBar.SetFlash("Not a git repository")
			return m, nil
		}
		return m, m.enterGitMode()
	}

	// Alt+E switches to edit mode.
	if key.String() == "alt+e" {
		if m.viewMode == ViewEdit {
			return m, nil
		}
		return m, m.toggleEditMode()
	}

	// Alt+H toggles the Field Manual help overlay.
	if key.String() == "alt+h" {
		m.toggleFieldManual()
		return m, nil
	}

	// Escape: close references panel if active, abort command input,
	// route to editor in edit mode, or double-tap to interrupt agent.
	// When an overlay is active, skip — overlays handle their own Esc.
	if key.String() == "esc" && m.overlay == overlayNone {
		// Git panel: close gear dropdown or exit header focus.
		if m.viewMode == ViewGit && m.gitPanel != nil && m.focus.Current() == component.FocusGitPanel {
			comp, cmd := m.gitPanel.Update(key)
			m.gitPanel = comp.(*gitpanel.Model)
			return m, cmd
		}
		if (m.fileTree.InReferencesMode() || m.fileTree.InDocSymbolsMode() || m.fileTree.InTabsMode()) && m.focus.Current() == component.FocusFileTree {
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
		// Conflict view ESC: content pane → focus file list, file list → abort sequencer.
		if m.conflictViewActive && m.conflictView != nil && m.focus.Current() == component.FocusConflictView {
			m.focus.SetFocus(component.FocusConflictFileList)
			m.syncFocusState()
			return m, nil
		}
		if m.conflictViewActive && m.conflictView != nil && m.focus.Current() == component.FocusConflictFileList {
			return m, m.preserveAbortCmd("sequencer")
		}
		if m.mergeDiffViewActive && m.mergeDiffView != nil && m.focus.Current() == component.FocusMergeDiffFileList && m.mergeDiffView.FileSearchActive() {
			m.mergeDiffView.UpdateFileList("esc")
			return m, nil
		}
		if m.mergeDiffViewActive && m.mergeDiffView != nil && m.focus.Current() == component.FocusMergeDiffFileList {
			m.exitMergeDiffView()
			return m, nil
		}
		if m.mergeDiffViewActive && m.mergeDiffView != nil && m.isMergeDiffPaneFocused() {
			cmd := m.mergeDiffView.Update(key)
			return m, cmd
		}
		if m.diffViewActive && m.diffView != nil && m.focus.Current() == component.FocusDiffFileList && m.diffView.FileSearchActive() {
			m.diffView.UpdateFileList("esc")
			return m, nil
		}
		if m.diffViewActive && m.diffView != nil && m.focus.Current() == component.FocusDiffFileList {
			m.exitDiffView()
			return m, nil
		}
		if m.diffViewActive && m.diffView != nil && m.isDiffPaneFocused() {
			cmd := m.diffView.Update(key)
			return m, cmd
		}
		// Agent panel sub-views consume Esc to back out before double-tap interrupt.
		if m.focus.Current() == component.FocusAgentPanel && m.agentPanel.InSubView() {
			comp, cmd := m.agentPanel.Update(key)
			m.agentPanel = comp.(*agentpkg.Model)
			m.syncManualTargetFromAgentSelection()
			return m, cmd
		}
		now := time.Now()
		if !m.lastEscTime.IsZero() && now.Sub(m.lastEscTime) <= time.Second {
			m.lastEscTime = time.Time{}
			return m, m.interruptActiveRoute("esc")
		}
		m.lastEscTime = now
		m.statusBar.SetFlash("Press Esc again to interrupt agent")
		return m, nil
	}

	// Shift+Tab in edit mode: cycle to next tab.
	if ks == "shift+tab" && m.viewMode == ViewEdit &&
		m.isEditorFocused() &&
		len(m.focusedTabOrder()) > 0 {
		return m, m.nextTab()
	}

	// Colon in edit mode normal: activate command input panel.
	if key.String() == ":" && m.viewMode == ViewEdit &&
		m.isEditorFocused() &&
		m.focusedEditor().IsNormalMode() {
		m.enterCmdInput()
		return m, nil
	}

	// Overlay key capture: active overlays consume all keys.
	if m.overlay == overlayEditor {
		comp, cmd := m.editorOverlay.Update(key)
		m.editorOverlay = comp.(*editor.Model)
		return m, cmd
	}
	if m.overlay == overlayModal && m.modalOverlay.Active() {
		comp, cmd := m.modalOverlay.Update(key)
		m.modalOverlay = comp.(*modal.Model)
		return m, cmd
	}
	if m.overlay == overlaySearch && m.searchOverlay.Visible() {
		comp, cmd := m.searchOverlay.Update(key)
		m.searchOverlay = comp.(*search.Model)
		return m, cmd
	}
	if m.overlay == overlayFieldManual && m.fieldManualOverlay.Visible() {
		comp, cmd := m.fieldManualOverlay.Update(key)
		m.fieldManualOverlay = comp.(*fieldmanual.Model)
		if !m.fieldManualOverlay.Visible() {
			m.overlay = overlayNone
		}
		return m, cmd
	}
	if m.overlay == overlayLogin && m.loginPanel.Active() {
		done, result, cmd := m.loginPanel.Update(key)
		if done {
			return m, tea.Batch(cmd, m.handleLoginPanelResult(result))
		}
		return m, cmd
	}
	// Ctrl+P toggles the search overlay (unless the editor completion popup
	// is active, where Ctrl+P navigates to the previous item).
	if key.String() == "ctrl+p" && !m.focusedEditor().CompletionActive() {
		m.toggleSearch()
		return m, nil
	}

	// Alt+F toggles the in-file find bar when the editor has focus.
	if ks == "alt+f" && m.viewMode == ViewEdit && m.isEditorFocused() {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone // dismiss chord hint — only one overlay at a time
		m.focusedEditor().ToggleFindBar()
		return m, nil
	}

	// Alt+Shift+R toggles the in-file find-and-replace bar when the editor has focus.
	if ks == "alt+R" && m.viewMode == ViewEdit && m.isEditorFocused() {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone
		m.focusedEditor().ToggleReplaceBar()
		return m, nil
	}

	// Alt+F toggles search/filter when the file tree or git panel has focus.
	// In tabs mode it toggles the tab filter; otherwise multi-file search.
	if ks == "alt+f" && m.focus.Current() == component.FocusFileTree {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone
		if m.fileTree.InTabsMode() {
			m.fileTree.ToggleTabFilter()
		} else {
			m.fileTree.ToggleSearch()
		}
		return m, nil
	}

	// Alt+F toggles search/filter when the git explorer panel has focus.
	if ks == "alt+f" && m.viewMode == ViewGit && m.focus.Current() == component.FocusGitPanel && m.gitPanel != nil {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone
		m.gitPanel.ToggleSearch()
		return m, nil
	}

	// Alt+F toggles file search when the merge diff file list has focus.
	if ks == "alt+f" && m.mergeDiffViewActive && m.focus.Current() == component.FocusMergeDiffFileList && m.mergeDiffView != nil {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone
		m.mergeDiffView.ToggleFileSearch()
		return m, nil
	}

	// Alt+F toggles the in-file find bar when a merge diff pane has focus.
	if ks == "alt+f" && m.mergeDiffViewActive && m.isMergeDiffPaneFocused() && m.mergeDiffView != nil {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone
		m.mergeDiffView.ToggleFindBar()
		return m, nil
	}

	// Alt+F toggles file search when the diff file list has focus.
	if ks == "alt+f" && m.diffViewActive && m.focus.Current() == component.FocusDiffFileList && m.diffView != nil {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone
		m.diffView.ToggleFileSearch()
		return m, nil
	}

	// Alt+F toggles the in-file find bar when a diff pane has focus.
	if ks == "alt+f" && m.diffViewActive && m.isDiffPaneFocused() && m.diffView != nil {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone
		m.diffView.ToggleFindBar()
		return m, nil
	}

	// Alt+Shift+R toggles multi-file replace when the file tree has focus.
	if ks == "alt+R" && m.focus.Current() == component.FocusFileTree {
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return m, nil
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now
		m.chord = chordNone
		m.fileTree.ToggleReplace()
		return m, nil
	}

	// Alt+Shift+F triggers LSP formatting when the editor has focus.
	if ks == "alt+F" && m.viewMode == ViewEdit && m.isEditorFocused() {
		if fp := m.focusedEditor().FilePath(); fp != "" {
			var flushContent string
			if m.focusedEditor().LSPDirty() {
				m.focusedEditor().ClearLSPDirty()
				flushContent = m.focusedEditor().Content()
			}
			return m, m.lspFormatCmd(fp, flushContent, m.focusedEditor().EditGeneration())
		}
	}

	// Alt+Shift+S: save the current editor buffer.
	if ks == "alt+S" && m.viewMode == ViewEdit && m.isEditorFocused() {
		m.saveEditorBuffer()
		return m, nil
	}

	// Ctrl+Left/Right in edit mode: pagination-aware tab navigation.
	if m.viewMode == ViewEdit && len(m.focusedTabOrder()) > 1 {
		if ks == "ctrl+left" {
			return m, m.tabNavLeft()
		}
		if ks == "ctrl+right" {
			return m, m.tabNavRight()
		}
	}

	// Alt+Shift+E: toggle between file explorer and last active editor pane.
	if ks == "alt+E" && m.viewMode == ViewEdit {
		if m.focus.Current() == component.FocusFileTree {
			m.focusCodePanel()
		} else {
			m.focus.SetFocus(component.FocusFileTree)
		}
		m.syncFocusState()
		return m, nil
	}

	// Alt+| (alt+shift+\): vertical split on focused editor pane.
	if ks == "alt+|" && m.viewMode == ViewEdit && m.isEditorFocused() {
		m.splitPane(pane.SplitVertical)
		return m, nil
	}

	// Alt+_ (alt+shift+-): horizontal split on focused editor pane.
	if ks == "alt+_" && m.viewMode == ViewEdit && m.isEditorFocused() {
		m.splitPane(pane.SplitHorizontal)
		return m, nil
	}

	// Alt+Shift+M: toggle markdown preview for the active file.
	if ks == "alt+M" && m.viewMode == ViewEdit && m.isEditorFocused() {
		if m.mdPreviewPane != 0 {
			m.dismissMarkdownPreview()
		} else if isMarkdownFile(m.focusedEditor().FilePath()) {
			m.openMarkdownPreview(m.focusedEditor().FilePath())
		}
		return m, nil
	}

	// Alt+Shift+W: close focused pane.
	if ks == "alt+W" && m.viewMode == ViewEdit && m.paneTree != nil && !m.paneTree.IsLeaf() {
		m.closePane()
		return m, nil
	}

	// Alt+]: grow focused pane (take space from sibling).
	if ks == "alt+]" && m.viewMode == ViewEdit && m.paneTree != nil && !m.paneTree.IsLeaf() {
		if m.paneTree.AdjustRatio(m.focusedPane, ratioStep) {
			m.resizeInlineEditor()
		}
		return m, nil
	}

	// Alt+[: shrink focused pane (give space to sibling).
	if ks == "alt+[" && m.viewMode == ViewEdit && m.paneTree != nil && !m.paneTree.IsLeaf() {
		if m.paneTree.AdjustRatio(m.focusedPane, -ratioStep) {
			m.resizeInlineEditor()
		}
		return m, nil
	}

	// Alt+=: equalize all split ratios.
	if ks == "alt+=" && m.viewMode == ViewEdit && m.paneTree != nil && !m.paneTree.IsLeaf() {
		m.paneTree.Equalize()
		m.resizeInlineEditor()
		m.statusBar.SetFlash("Splits equalized")
		return m, nil
	}

	// Two-key chord: S then Left/Right (sessions), A then Left/Right (agents).
	if cmd, handled := m.handleChord(key); handled {
		return m, cmd
	}

	// Git tab navigation shortcuts (must precede spatial focus so
	// Alt+Shift+Left/Right reaches tab cycling when the git panel is focused).
	if m.viewMode == ViewGit && m.gitPanel != nil {
		if cmd, handled := m.handleGitTabShortcut(ks); handled {
			return m, cmd
		}
	}

	// Alt+Shift+arrow moves focus spatially between panels.
	if target, ok := m.spatialFocusTarget(key.String()); ok {
		m.focus.SetFocus(target)
		m.syncFocusState()
		return m, nil
	}

	// Delegate to focused component.
	return m, m.propagateToFocused(key)
}

func (m *AppModel) handleSubmit(submit msg.SubmitPromptMsg) tea.Cmd {
	targetAgent := m.resolveSubmitTarget(submit.TargetAgent)
	submit.TargetAgent = targetAgent
	submit.SessionID = m.resolveRouteSessionID(submit.SessionID)

	// If there's an active route, enqueue instead of dispatching immediately.
	if strings.TrimSpace(m.activeRoute.CorrelationID) != "" {
		_, ok := m.promptQueue.Enqueue(uuid.New().String(), submit.Text, submit.TargetAgent, submit.SessionID)
		if !ok {
			m.pushSystemChat(fmt.Sprintf("%d requests pending. Please wait before sending another message!", m.promptQueue.PendingCount()))
			return nil
		}
		m.recalcLayout() // Queue strip appeared or grew.
		m.comp.MarkDirty(compositor.SlotQueue)
		m.viewDirty = true
		return nil
	}

	// Push a user entry to chat.
	entry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceUser,
		Content:   submit.Text,
		Height:    -1,
	}
	m.chat.PushEntry(entry)

	// Push a system message before the thinking placeholder so it appears above the response.
	sysEntry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceSystem,
		Content:   routingStatusText(targetAgent),
		Height:    -1,
	}
	m.chat.PushEntry(sysEntry)

	// Push a thinking placeholder so the spinner appears immediately.
	m.chat.BeginThinking(thinkingAgentType(targetAgent))

	// Route through Guide bus.
	return m.publishRouteRequest(submit)
}

func isInlineExCommand(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), ":")
}

// ---------------------------------------------------------------------------
// Chat commands (/login, etc.)
// ---------------------------------------------------------------------------

// parseChatCommand returns the command name if text starts with "/".
func parseChatCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	cmd := strings.TrimPrefix(trimmed, "/")
	cmd = strings.SplitN(cmd, " ", 2)[0]
	cmd = strings.ToLower(cmd)
	if cmd == "" {
		return "", false
	}
	return cmd, true
}

// handleChatCommand dispatches a "/command" from chat input.
func (m *AppModel) handleChatCommand(cmd string, submit msg.SubmitPromptMsg) tea.Cmd {
	switch cmd {
	case "clear":
		m.chat.Clear()
		return nil
	case "login":
		return m.handleLoginCommand(submit)
	default:
		m.pushSystemChat(fmt.Sprintf("Unknown command: /%s", cmd))
		return nil
	}
}

// pushSystemChat adds a system message to the chat panel.
func (m *AppModel) pushSystemChat(content string) {
	m.chat.PushEntry(&chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceSystem,
		Content:   content,
		Height:    -1,
	})
}

// handleLoginCommand activates the top-panel login flow for /login.
func (m *AppModel) handleLoginCommand(submit msg.SubmitPromptMsg) tea.Cmd {
	// Echo the user command to chat.
	m.suppressLoginResult = false
	m.loginPanel.Activate()
	inputH := m.inputHeight()
	mainH := max(m.height-inputH-statusBarHeight, 1)
	m.loginPanel.SetSize(m.width, mainH)
	m.overlay = overlayLogin
	m.viewDirty = true
	return nil
}

// deactivateLogin closes the login panel and restores the normal view.
func (m *AppModel) deactivateLogin() {
	m.cancelLoginFlow()
	m.loginPanel.Deactivate()
	m.overlay = overlayNone
	m.viewDirty = true
	m.comp.InvalidateAll()
}

func (m *AppModel) cancelLoginFlow() {
	if m.oauthSessions == nil {
		m.pendingAnthropicOAuth = nil
		return
	}
	m.oauthSessions.CancelCurrent()
	m.pendingAnthropicOAuth = nil
}

// handleLoginPanelResult processes results from the login panel.
func (m *AppModel) handleLoginPanelResult(result login.LoginResult) tea.Cmd {
	switch result.Action {
	case login.ActionAPIKey:
		// Keep panel open — handleLoginResult will close on success
		// or show an inline error on failure.
		return func() tea.Msg {
			err := saveAPIKeySecure(m.ctx, result.Provider, result.APIKey)
			return msg.LoginResultMsg{
				Provider: result.Provider,
				Method:   "apikey",
				Success:  err == nil,
				Error:    loginErrorString(err),
			}
		}

	case login.ActionOAuthStart:
		// Keep panel visible in OAuth step for status updates.
		return m.startOAuthFlow(result.Provider)

	case login.ActionOAuthSubmit:
		m.loginPanel.SetOAuthStatus("Completing OAuth authorization...")
		return m.completeOAuthCodeFlow(result.Provider, result.OAuthCode)

	case login.ActionCancelled:
		m.suppressLoginResult = true
		m.deactivateLogin()
		return nil

	default:
		return nil
	}
}

// startOAuthFlow launches the appropriate OAuth flow for the provider.
func (m *AppModel) startOAuthFlow(provider string) tea.Cmd {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if !supportsOAuthProvider(provider) {
		m.loginPanel.SetError(fmt.Sprintf("OAuth not supported for %s", provider))
		return nil
	}
	if m.oauthSessions == nil {
		m.oauthSessions = newOAuthSessionManager()
	}
	flowID := m.oauthSessions.Begin(provider)
	switch provider {
	case "google":
		return m.startGoogleOAuthCmd(provider, flowID)
	case "openai":
		return m.startOpenAIOAuthCmd(provider, flowID)
	case "anthropic":
		return m.startAnthropicOAuthCmd(provider, flowID)
	default:
		return nil
	}
}

func (m *AppModel) completeOAuthCodeFlow(provider, code string) tea.Cmd {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "anthropic" {
		m.loginPanel.SetError(fmt.Sprintf("OAuth code flow is not supported for %s", provider))
		return nil
	}
	pending := m.pendingAnthropicOAuth
	if pending == nil || pending.service == nil || pending.challenge == nil {
		m.loginPanel.SetError("No active Anthropic OAuth flow. Start OAuth again.")
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		auth, err := pending.service.CompleteAuthCode(ctx, pending.challenge, code)
		if err == nil {
			err = pending.service.Save(ctx, auth)
		}
		if err != nil {
			return msg.LoginResultMsg{
				Provider: provider,
				Method:   "oauth",
				FlowID:   pending.flowID,
				Error:    err.Error(),
			}
		}
		return msg.LoginResultMsg{
			Provider: provider,
			Method:   "oauth",
			FlowID:   pending.flowID,
			Success:  true,
		}
	}
}

func supportsOAuthProvider(provider string) bool {
	switch provider {
	case "google", "openai", "anthropic":
		return true
	default:
		return false
	}
}

// oauthTimeout is the max duration for an OAuth flow before it's considered failed.
// Derived from: browser-based OAuth typically completes within a few minutes;
// 10 minutes is generous enough for slow networks or manual steps.
const oauthTimeout = 10 * time.Minute

type oauthSessionStartedMsg struct {
	Provider           string
	FlowID             uint64
	URL                string
	UserCode           string
	NeedsCode          bool
	Instructions       string
	AnthropicService   oauth.AnthropicAuthService
	AnthropicChallenge *oauth.AnthropicOAuthChallenge
	Wait               tea.Cmd
	Cancel             context.CancelFunc
}

type pendingAnthropicOAuthCode struct {
	flowID    uint64
	service   oauth.AnthropicAuthService
	challenge *oauth.AnthropicOAuthChallenge
	cancel    context.CancelFunc
}

// startGoogleOAuthCmd starts Google OAuth and emits a session-started message.
func (m *AppModel) startGoogleOAuthCmd(provider string, flowID uint64) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		authSvc := oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{})
		session, err := oauth.StartGoogleOAuthSession(ctx, authSvc, oauthTimeout)
		if err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: err.Error()}
		}
		return oauthSessionStartedMsg{
			Provider: provider,
			FlowID:   flowID,
			URL:      session.Challenge.AuthURL,
			Wait:     waitGoogleOAuthResultCmd(provider, flowID, session),
			Cancel:   session.Cancel,
		}
	}
}

// startOpenAIOAuthCmd starts OpenAI device auth and emits a session-started message.
func (m *AppModel) startOpenAIOAuthCmd(provider string, flowID uint64) tea.Cmd {
	ctx := m.ctx

	return func() tea.Msg {
		authSvc := oauth.NewOpenAIAuthService(oauth.OpenAIAuthServiceConfig{})
		session, err := oauth.StartDeviceAuthSession(ctx, authSvc, oauthTimeout)
		if err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: err.Error()}
		}
		return oauthSessionStartedMsg{
			Provider: provider,
			FlowID:   flowID,
			URL:      session.Challenge.VerificationURL,
			UserCode: session.Challenge.UserCode,
			Wait:     waitOpenAIOAuthResultCmd(provider, flowID, session),
			Cancel:   session.Cancel,
		}
	}
}

// startAnthropicOAuthCmd starts Anthropic OAuth and emits a session-started message.
func (m *AppModel) startAnthropicOAuthCmd(provider string, flowID uint64) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		authSvc := oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{})
		flowCtx, cancel := context.WithCancel(ctx)
		challenge, err := authSvc.BeginAuth(flowCtx)
		if err != nil {
			cancel()
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: err.Error()}
		}
		return oauthSessionStartedMsg{
			Provider:           provider,
			FlowID:             flowID,
			URL:                challenge.AuthURL,
			NeedsCode:          true,
			Instructions:       "Paste the authorization code shown in your browser.",
			AnthropicService:   authSvc,
			AnthropicChallenge: challenge,
			Cancel:             cancel,
		}
	}
}

func waitGoogleOAuthResultCmd(
	provider string,
	flowID uint64,
	session *oauth.GoogleOAuthSession,
) tea.Cmd {
	return func() tea.Msg {
		result, ok := <-session.Results
		if !ok {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: "oauth session ended unexpectedly"}
		}
		if result.Err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: result.Err.Error()}
		}
		return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Success: true}
	}
}

func waitOpenAIOAuthResultCmd(
	provider string,
	flowID uint64,
	session *oauth.DeviceAuthSession,
) tea.Cmd {
	return func() tea.Msg {
		result, ok := <-session.Results
		if !ok {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: "oauth session ended unexpectedly"}
		}
		if result.Err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: result.Err.Error()}
		}
		return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Success: true}
	}
}

func waitAnthropicOAuthResultCmd(
	provider string,
	flowID uint64,
	session *oauth.AnthropicOAuthSession,
) tea.Cmd {
	return func() tea.Msg {
		result, ok := <-session.Results
		if !ok {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: "oauth session ended unexpectedly"}
		}
		if result.Err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: result.Err.Error()}
		}
		return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Success: true}
	}
}

func (m *AppModel) handleOAuthSessionStarted(start oauthSessionStartedMsg) tea.Cmd {
	if m.oauthSessions == nil {
		m.oauthSessions = newOAuthSessionManager()
	}
	if m.overlay != overlayLogin || !m.loginPanel.Active() || m.suppressLoginResult {
		m.oauthSessions.Abort(start.Provider, start.FlowID, start.Cancel)
		return nil
	}
	if !m.oauthSessions.Attach(start.Provider, start.FlowID, start.Cancel) {
		return nil
	}
	if start.NeedsCode && strings.EqualFold(start.Provider, "anthropic") {
		if start.AnthropicService == nil || start.AnthropicChallenge == nil {
			m.oauthSessions.Abort(start.Provider, start.FlowID, start.Cancel)
			m.loginPanel.SetError("Anthropic OAuth could not be initialized")
			return nil
		}
		m.pendingAnthropicOAuth = &pendingAnthropicOAuthCode{
			flowID:    start.FlowID,
			service:   start.AnthropicService,
			challenge: start.AnthropicChallenge,
			cancel:    start.Cancel,
		}
		m.loginPanel.SetOAuthCodeEntry(true, start.Instructions)
		m.loginPanel.SetDeviceCode(start.UserCode)
		m.loginPanel.SetOAuthStatus(formatOAuthStatus(start.Provider, start.URL))
		if err := openURL(start.URL); err != nil {
			m.statusBar.SetFlash("Could not open browser automatically. Use the URL shown in the login panel.")
		}
		return nil
	}

	m.pendingAnthropicOAuth = nil
	m.loginPanel.SetOAuthCodeEntry(false, "")
	m.loginPanel.SetDeviceCode(start.UserCode)
	m.loginPanel.SetOAuthStatus(formatOAuthStatus(start.Provider, start.URL))
	if err := openURL(start.URL); err != nil {
		m.statusBar.SetFlash("Could not open browser automatically. Use the URL shown in the login panel.")
	}
	return start.Wait
}

func formatOAuthStatus(provider, url string) string {
	return fmt.Sprintf("Authorize %s in your browser:\n%s", loginProviderLabel(provider), url)
}

// handleLoginResult processes the async LoginResultMsg from an auth flow.
func (m *AppModel) handleLoginResult(result msg.LoginResultMsg) tea.Cmd {
	if result.Method == "oauth" {
		if m.oauthSessions == nil {
			return nil
		}
		keepAnthropicPending := strings.EqualFold(result.Provider, "anthropic") &&
			!result.Success &&
			m.pendingAnthropicOAuth != nil &&
			m.pendingAnthropicOAuth.flowID == result.FlowID
		if !keepAnthropicPending {
			if !m.oauthSessions.Complete(result.Provider, result.FlowID) {
				return nil
			}
			if strings.EqualFold(result.Provider, "anthropic") {
				if m.pendingAnthropicOAuth != nil && m.pendingAnthropicOAuth.cancel != nil {
					m.pendingAnthropicOAuth.cancel()
				}
				m.pendingAnthropicOAuth = nil
				m.loginPanel.SetOAuthCodeEntry(false, "")
			}
		}
	}
	if m.suppressLoginResult && !result.Success {
		m.suppressLoginResult = false
		return nil
	}
	m.suppressLoginResult = false

	if result.Success {
		// Close panel + flash success on the status bar.
		if m.loginPanel.Active() {
			m.deactivateLogin()
		}
		// Persist the auth mode preference so it survives restarts.
		_ = credentials.SaveAuthPref(result.Provider, result.Method)
		m.statusBar.SetFlash(fmt.Sprintf("Authenticated with %s via %s",
			loginProviderLabel(result.Provider), loginMethodLabel(result.Method)))
		// Notify the AuthRegistry so all agents of this provider type
		// receive the credential update via the event bus.
		if m.deps.AuthRegistry != nil {
			m.deps.AuthRegistry.NotifyCredentialChanged(result.Provider, result.Method)
		}
		m.statusBar.SetAuthStatus(result.Provider, true)
		return nil
	}

	// Failure: keep the panel open and show the error inline so the
	// user can correct input and retry.
	if m.loginPanel.Active() {
		m.loginPanel.SetError(redactSecrets(result.Error))
	} else {
		m.statusBar.SetFlash(fmt.Sprintf("Login failed: %s", redactSecrets(result.Error)))
	}
	return nil
}

// loginProviderLabel returns a human-readable label for a provider ID.
func loginProviderLabel(provider string) string {
	switch provider {
	case "google":
		return "Google (Gemini)"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	default:
		return provider
	}
}

// loginMethodLabel returns a human-readable label for an auth method.
func loginMethodLabel(method string) string {
	switch method {
	case "oauth":
		return "OAuth"
	case "apikey":
		return "API key"
	default:
		return method
	}
}

func saveAPIKeySecure(ctx context.Context, provider, key string) error {
	dirs, err := storage.ResolveDirs()
	if err != nil {
		return err
	}
	if err := dirs.EnsureAll(); err != nil {
		return err
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return err
	}
	if err := manager.SetAPIKey(ctx, provider, key, nil); err != nil {
		return err
	}
	return removeLegacyAPIKeyEntry(provider)
}

func resolveLoginPanelAPIKey(provider string) string {
	return resolveLoginPanelAPIKeyWithResolvers(
		provider,
		resolveSecureProviderAPIKey,
		llm.ResolveAPIKey,
	)
}

func resolveLoginPanelAPIKeyWithResolvers(
	provider string,
	secureResolver func(string) (string, error),
	legacyResolver func(string) (string, error),
) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return ""
	}
	if secureResolver != nil {
		if key, err := secureResolver(normalized); err == nil && llm.ValidateKeyFormat(normalized, key) {
			return key
		}
	}
	if legacyResolver == nil {
		return ""
	}
	key, err := legacyResolver(normalized)
	if err != nil || !llm.ValidateKeyFormat(normalized, key) {
		return ""
	}
	return key
}

func resolveSecureProviderAPIKey(provider string) (string, error) {
	dirs, err := storage.ResolveDirs()
	if err != nil {
		return "", err
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return "", err
	}
	return manager.GetAPIKey(provider)
}

func removeLegacyAPIKeyEntry(provider string) error {
	creds, err := llm.LoadCredentials()
	if err != nil {
		return err
	}
	if _, ok := creds[provider]; !ok {
		return nil
	}
	delete(creds, provider)
	if len(creds) == 0 {
		path := strings.TrimSpace(llm.DefaultCredentialsPath())
		if path == "" {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return llm.SaveCredentials(creds)
}

func loginErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func redactSecrets(text string) string {
	return redact.Text(text)
}

// openURL attempts to open an OAuth URL in the user's default browser.
func openURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return fmt.Errorf("url is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme")
	}
	return openURLPlatform(parsed.String())
}

var explicitTargetAliases = map[string]string{
	"arch":    "architect",
	"planner": "architect",
}

func normalizeExplicitTargetAgent(raw string) string {
	target := strings.ToLower(strings.TrimSpace(raw))
	if target == "" {
		return ""
	}
	if alias, ok := explicitTargetAliases[target]; ok {
		target = alias
	}
	if !isSimpleTargetToken(target) {
		return ""
	}
	return target
}

func (m *AppModel) resolveSubmitTarget(explicit string) string {
	target := normalizeExplicitTargetAgent(explicit)
	if target != "" {
		m.manualTargetAgent = target
		// Explicit @agent overrides existing engagement.
		if normalizeAgentID(target) != m.engagedAgentID {
			m.clearEngagedAgent()
		}
		return target
	}
	target = normalizeExplicitTargetAgent(m.manualTargetAgent)
	if target != "" {
		if normalizeAgentID(target) != m.engagedAgentID {
			m.clearEngagedAgent()
		}
		return target
	}
	return ""
}

func (m *AppModel) syncManualTargetFromAgentSelection() {
	if m == nil || m.agentPanel == nil {
		return
	}
	selected := normalizeExplicitTargetAgent(m.agentPanel.SelectedAgentID())
	if selected == "" {
		return
	}
	m.manualTargetAgent = selected
	// Clear engagement when the user explicitly selects a different agent
	// (or the guide, which cannot be engaged). Without this, the previous
	// engaged agent (e.g. architect) stays sticky in the UI even after the
	// user navigates away.
	if normalizeAgentID(selected) != m.engagedAgentID {
		m.clearEngagedAgent()
	}
}

func isSimpleTargetToken(token string) bool {
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func routingStatusText(target string) string {
	if target == "" {
		return "Classifying and routing request"
	}
	return fmt.Sprintf("Routing request to @%s", target)
}

func thinkingAgentType(target string) string {
	if target != "" {
		return target
	}
	return guideAgentType
}

func (m *AppModel) handleInterrupt() tea.Cmd {
	cmd := m.interruptActiveRoute("ctrl+c")
	if cmd != nil {
		return cmd
	}
	m.statusBar.SetFlash("Press Ctrl+C again to quit")
	return nil
}

func (m *AppModel) interruptActiveRoute(reason string) tea.Cmd {
	correlationID, agentID := m.resolveInterruptTarget()
	if correlationID == "" {
		return nil
	}

	m.chat.MuteThinking("")
	m.chat.AbortStream()
	if m.interruptedCorrelations == nil {
		m.interruptedCorrelations = make(map[string]struct{})
	}
	m.interruptedCorrelations[correlationID] = struct{}{}
	m.pushInterruptedChatMessage(agentID)
	m.finalizeStreamUsage(correlationID, false, "interrupted")
	m.markQueueEntryByCorrelation(correlationID, false)
	m.clearActiveRoute(correlationID)
	m.agentPanel.DemoteAllActive()
	if m.statusBar != nil {
		m.statusBar.SetTokenPhase(status.PhaseIdle)
	}
	// Pause the queue on user interrupt — deliberate resume required.
	if !m.promptQueue.IsEmpty() {
		m.promptQueue.SetPaused(true)
		m.recalcLayout()
		m.viewDirty = true
	}
	m.statusBar.SetFlash("Agent interrupted")

	if m.deps.GuideBus == nil {
		return nil
	}

	interruptReq := &guide.UserInterruptRequest{
		CorrelationID: correlationID,
		SourceAgentID: sourceAgentTUI,
		Reason:        strings.TrimSpace(reason),
		Timestamp:     time.Now(),
	}
	busMsg := guide.NewUserInterruptMessage("", interruptReq)
	return func() tea.Msg {
		if err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, busMsg); err != nil {
			return msg.StreamErrorMsg{
				SessionID:     m.resolveRouteSessionID(""),
				CorrelationID: correlationID,
				Err:           err,
			}
		}
		return nil
	}
}

func (m *AppModel) pushInterruptedChatMessage(agentID string) {
	content := fmt.Sprintf("%s interrupted. What would you like to do next?", interruptAgentDisplayName(agentID))
	entry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceSystem,
		AgentType: "system",
		AgentID:   normalizeAgentID(agentID),
		Content:   content,
		Height:    -1,
	}
	m.chat.FinishThinking(entry)
}

func interruptAgentDisplayName(agentID string) string {
	names := map[string]string{
		"guide":       "Guide",
		"architect":   "Architect",
		"librarian":   "Librarian",
		"archivalist": "Archivalist",
		"academic":    "Academic",
	}
	key := strings.ToLower(strings.TrimSpace(agentID))
	if name, ok := names[key]; ok {
		return name
	}
	trimmed := strings.TrimSpace(agentID)
	if trimmed == "" {
		return "Agent"
	}
	return strings.ToUpper(trimmed[:1]) + trimmed[1:]
}

func (m *AppModel) setActiveRoute(correlationID, agentID string) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	if current := strings.TrimSpace(m.activeRoute.CorrelationID); current != "" && current != correlationID {
		return
	}
	m.activeRoute.CorrelationID = correlationID
	if strings.TrimSpace(agentID) != "" {
		m.activeRoute.AgentID = normalizeAgentID(agentID)
	}
}

func (m *AppModel) beginActiveRoute(correlationID, agentID string) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	m.activeRoute.CorrelationID = correlationID
	m.activeRoute.AgentID = normalizeAgentID(agentID)
}

func (m *AppModel) shouldRenderStreamEvent(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	if _, interrupted := m.interruptedCorrelations[correlationID]; interrupted {
		return false
	}
	active := strings.TrimSpace(m.activeRoute.CorrelationID)
	if active == "" {
		return true
	}
	return active == correlationID
}

func (m *AppModel) clearActiveRoute(correlationID string) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	if strings.TrimSpace(m.activeRoute.CorrelationID) != correlationID {
		return
	}
	m.activeRoute = activeRouteState{}
}

// forceResetActiveRoute unconditionally clears the activeRoute when the
// current correlationID matches. Used during reroutes where the old stream
// is abandoned and a new correlationID takes over.
func (m *AppModel) forceResetActiveRoute(oldCorrelationID string) {
	oldCorrelationID = strings.TrimSpace(oldCorrelationID)
	if oldCorrelationID == "" {
		return
	}
	if strings.TrimSpace(m.activeRoute.CorrelationID) == oldCorrelationID {
		m.activeRoute = activeRouteState{}
	}
}

// tryAdvanceQueue dispatches the next pending queue entry if the queue is
// non-empty and not paused. Returns a Cmd that re-enters handleSubmit, or nil.
func (m *AppModel) tryAdvanceQueue() tea.Cmd {
	if m.promptQueue.IsPaused() || m.promptQueue.IsEmpty() {
		return nil
	}
	entry, ok := m.promptQueue.Advance()
	if !ok {
		m.recalcLayout()
		m.viewDirty = true
		return nil
	}
	m.promptQueue.MarkDispatching(entry.ID)
	// Dispatch as a deferred BubbleTea command so it runs in a clean Update cycle.
	return func() tea.Msg {
		return msg.QueueAdvanceMsg{}
	}
}

// dispatchQueueHead sends the head queue entry through the normal submit path.
func (m *AppModel) dispatchQueueHead() tea.Cmd {
	entry, ok := m.promptQueue.Peek()
	if !ok || entry.State != queue.StateDispatching {
		return nil
	}
	submit := msg.SubmitPromptMsg{
		Text:        entry.Text,
		TargetAgent: entry.TargetAgent,
		SessionID:   entry.SessionID,
	}
	// The activeRoute is now clear, so handleSubmit will dispatch normally.
	cmd := m.handleSubmit(submit)
	// Mark the queue entry as active with the new correlation ID.
	if strings.TrimSpace(m.activeRoute.CorrelationID) != "" {
		m.promptQueue.MarkActive(entry.ID, m.activeRoute.CorrelationID)
	}
	m.recalcLayout()
	m.viewDirty = true
	return cmd
}

// markQueueEntryByCorrelation marks a queue entry as completed or failed based
// on its correlation ID. No-op if no queue entry matches the correlation ID.
func (m *AppModel) markQueueEntryByCorrelation(correlationID string, success bool) {
	active, ok := m.promptQueue.ActiveEntry()
	if !ok || active.CorrelationID != correlationID {
		return
	}
	if success {
		m.promptQueue.MarkCompleted(active.ID)
	} else {
		m.promptQueue.MarkFailed(active.ID)
	}
}

// setEngagedAgent updates the sticky engaged agent for conversation continuity.
// Non-guide agents are tracked; "guide" is ignored since the Guide is a router.
func (m *AppModel) setEngagedAgent(agentID string) {
	normalized := normalizeAgentID(agentID)
	if normalized == "" || normalized == "guide" {
		return
	}
	m.engagedAgentID = normalized
	if m.agentPanel != nil {
		m.agentPanel.SetEngagedAgent(normalized)
	}
	if m.statusBar != nil {
		m.statusBar.SetEngagedAgent(normalized)
	}
}

// clearEngagedAgent removes engagement tracking, forcing full classification.
func (m *AppModel) clearEngagedAgent() {
	m.engagedAgentID = ""
	if m.agentPanel != nil {
		m.agentPanel.ClearEngagedAgent()
	}
	if m.statusBar != nil {
		m.statusBar.SetEngagedAgent("")
	}
}

// handleStreamReroute processes a reroute notification from the Guide.
// It transitions the activeRoute from the original correlationID to the
// rerouted one so that stream events from the new target agent are rendered.
func (m *AppModel) handleStreamReroute(reroute msg.StreamRerouteMsg) tea.Cmd {
	// Force-clear the old activeRoute to unblock new stream events.
	if reroute.OriginalCorrelationID != "" {
		m.forceResetActiveRoute(reroute.OriginalCorrelationID)
	}
	// Set up the new activeRoute for the rerouted stream.
	if reroute.CorrelationID != "" {
		m.beginActiveRoute(reroute.CorrelationID, normalizeAgentID(reroute.ToAgentID))
	}
	m.clearEngagedAgent()
	if reroute.ToAgentID != "" {
		m.setEngagedAgent(reroute.ToAgentID)
	}
	if reroute.FromAgentID != "" {
		m.statusBar.SetFlash(reroute.FromAgentID + " -> " + reroute.ToAgentID)
	}
	return nil
}

func (m *AppModel) resolveInterruptTarget() (string, string) {
	if strings.TrimSpace(m.activeRoute.CorrelationID) != "" {
		return m.activeRoute.CorrelationID, m.activeRoute.AgentID
	}
	return m.latestStreamUsage()
}

func (m *AppModel) resolveRouteSessionID(candidate string) string {
	if sessionID := strings.TrimSpace(candidate); sessionID != "" {
		return sessionID
	}
	if m != nil && m.deps.SessionManager != nil {
		if active, ok := m.deps.SessionManager.GetActive(); ok && active != nil {
			if sessionID := strings.TrimSpace(active.ID()); sessionID != "" {
				return sessionID
			}
		}
	}
	return defaultGuideSessionID
}

func (m *AppModel) latestStreamUsage() (string, string) {
	if len(m.streamUsage) == 0 {
		return "", ""
	}
	correlationID := ""
	agentID := ""
	var latest time.Time
	for cid, usage := range m.streamUsage {
		if correlationID == "" || usage.StartedAt.After(latest) {
			correlationID = cid
			agentID = usage.AgentID
			latest = usage.StartedAt
		}
	}
	return correlationID, agentID
}

func (m *AppModel) handleQuit() tea.Cmd {
	return tea.Quit
}

func (m *AppModel) handleTick(tick msg.TickMsg) tea.Cmd {
	// Drop ticks from invalidated chains (e.g., after slow→fast upgrade).
	if tick.Gen != m.tickGen {
		return nil
	}

	// Scroll momentum and bounce.
	if !m.scroll.settled() {
		m.tickScrollMomentum()
		m.viewDirty = true
	}
	m.tickSwipeDecay()

	// Drain pending commands from scroll-triggered pagination.
	var cmds []tea.Cmd
	if m.gitPanel != nil {
		if cmd := m.gitPanel.DrainCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.commitTree != nil {
		if cmd := m.commitTree.DrainCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	cmds = append(cmds, m.continueTickChain())
	return tea.Batch(cmds...)
}

func (m *AppModel) handleDecorTick(tick msg.DecorTickMsg) tea.Cmd {
	if tick.Gen != m.decorGen {
		return nil
	}

	changed := false

	// Status spinner + flash countdown.
	if m.statusBar.IsAnimating() {
		model, cmd := m.statusBar.Update(tick)
		_ = cmd // status.Update never returns a cmd currently.
		m.statusBar = model.(*status.Model)
		changed = changed || m.statusBar.ViewDirty()
	}

	// Chat highlight / edge flash.
	if m.chat.HasActiveAnimation() {
		chatComp, _ := m.chat.Update(tick)
		m.chat = chatComp.(*chat.Model)
		changed = true
	}

	// Tab arrow flash expiry.
	now := tick.Time
	tabFlashChanged := false
	if (m.tabArrowFlashLeftUntil != (time.Time{})) && !now.Before(m.tabArrowFlashLeftUntil) {
		m.tabArrowFlashLeftUntil = time.Time{}
		tabFlashChanged = true
	}
	if (m.tabArrowFlashRightUntil != (time.Time{})) && !now.Before(m.tabArrowFlashRightUntil) {
		m.tabArrowFlashRightUntil = time.Time{}
		tabFlashChanged = true
	}
	if tabFlashChanged {
		changed = true
		m.comp.MarkDirty(compositor.SlotRight)
	}

	// Refresh ring hint during streaming (activity badge changes) at decor rate.
	if (!m.leftRing.empty() || !m.rightRing.empty()) && m.chat.IsStreaming() {
		m.statusBar.SetViewRingHint(m.buildRingHint())
		changed = true
	}

	// Queue strip gradient animation (only when non-empty and not paused).
	if !m.promptQueue.IsEmpty() && !m.promptQueue.IsPaused() {
		m.comp.MarkDirty(compositor.SlotQueue)
		changed = true
	}

	// Commit tree loading spinner (time-based, redraws each decor tick).
	if m.commitTree != nil && m.commitTree.NeedsDecorTick() {
		m.comp.MarkDirty(compositor.SlotRight)
		changed = true
	}

	// Diff view loading spinner (frame-counter, advanced each decor tick).
	if m.diffViewActive && m.diffView != nil && m.diffView.NeedsDecorTick() {
		m.diffView.AdvanceSpinner()
		m.comp.MarkDirty(compositor.SlotRight)
		changed = true
	}

	// Merge diff view loading spinner.
	if m.mergeDiffViewActive && m.mergeDiffView != nil && m.mergeDiffView.NeedsDecorTick() {
		m.mergeDiffView.AdvanceSpinner()
		m.comp.MarkDirty(compositor.SlotRight)
		changed = true
	}

	// Conflict view loading spinner (time-based, redraws each decor tick).
	if m.conflictViewActive && m.conflictView != nil && m.conflictView.NeedsDecorTick() {
		m.comp.MarkDirty(compositor.SlotRight)
		changed = true
	}

	// Git panel loading bar (time-based, redraws each decor tick).
	if m.viewMode == ViewGit && m.gitPanel != nil && m.gitPanel.NeedsDecorTick() {
		m.gitPanel.MarkViewDirty()
		if m.layout.Mode() == layout.FourColumn {
			m.comp.MarkDirty(compositor.SlotCenterLeft)
		} else {
			m.comp.MarkDirty(compositor.SlotLeft)
		}
		changed = true
	}

	// Plan viewer spinner animation.
	if m.planView != nil && m.planView.NeedsDecorTick() {
		m.planView.MarkViewDirty()
		m.comp.MarkDirty(compositor.SlotRight)
		changed = true
	}

	// Agent panel shimmer + dot animation.
	agentActive := m.agentPanel.HasActiveAgent()
	if m.agentPanel.NeedsDecorTick() {
		if agentActive {
			m.agentPanel.AdvanceDotFrame()
		}
		m.comp.MarkDirty(compositor.SlotLeft)
		changed = true
	}

	// Session panel dot animation — sync agent activity and advance frame.
	m.sessionPanel.SetAgentActive(agentActive)
	if agentActive {
		m.sessionPanel.AdvanceDotFrame()
		m.comp.MarkDirty(compositor.SlotLeft)
		changed = true
	}

	// Swap focus ring gradient: full prismatic when active agents, subdued when idle.
	if agentActive {
		m.focusGradient = m.activeFocusGradient
	} else {
		m.focusGradient = m.idleFocusGradient
	}

	// Focus ring border shimmer — mark the focused panel slot and input.
	if m.focusGradient != nil {
		m.comp.MarkDirty(m.focusBorderGroup())
		m.comp.MarkDirty(compositor.SlotInput)
		changed = true
	}

	if changed {
		m.viewDirty = true
	}

	return m.continueDecorTickChain()
}

func (m *AppModel) handleFocusPanel(fp msg.FocusPanelMsg) tea.Cmd {
	m.focus.SetFocus(fp.Target)
	m.syncFocusState()
	return nil
}

func (m *AppModel) handlePlanUpdate(update msg.PlanUpdateMsg) tea.Cmd {
	m.chat.HandlePlanUpdate(update)
	m.comp.MarkDirty(compositor.SlotCenter)
	m.viewDirty = true
	return nil
}

func (m *AppModel) handlePlanViewToggle() tea.Cmd {
	if m.planView == nil {
		return nil
	}
	comp, cmd := m.planView.Update(msg.PlanViewToggleMsg{})
	m.planView = comp.(*planview.Model)
	m.comp.MarkDirty(compositor.SlotRight)
	m.viewDirty = true
	return cmd
}

func (m *AppModel) handleOpenEditor(o msg.OpenEditorMsg) tea.Cmd {
	comp, cmd := m.editorOverlay.Update(o)
	m.editorOverlay = comp.(*editor.Model)
	m.editorOverlay.SetSize(m.width, m.height)
	m.overlay = overlayEditor

	// Notify LSP that the document was opened (fire-and-forget).
	if o.FilePath != "" {
		lang := detectEditorLanguage(o.FilePath)
		lspCmd := m.lspDidOpenCmd(o.FilePath, lang, o.Content)
		return tea.Batch(cmd, lspCmd)
	}
	return cmd
}

func (m *AppModel) handleLSPDiagnostic(d msg.LSPDiagnosticMsg) tea.Cmd {
	// Forward to all views that render diagnostics.
	m.editorOverlay.Update(d)
	m.focusedEditor().Update(d)
	m.codePanel.SetDiagnostics(d.FilePath, d.Diagnostics)
	return nil
}

// handleLSPServerMissing triggers on-demand installation when a language
// server binary is missing. De-duplicates by server ID.
func (m *AppModel) handleLSPServerMissing(d msg.LSPServerMissingMsg) tea.Cmd {
	sid := lsp.ServerID(d.ServerID)
	if m.lspInstalling[sid] {
		return nil // install already in progress
	}
	m.lspInstalling[sid] = true
	m.statusBar.SetFlash("Installing " + d.ServerName + "…")
	return m.installLSPServerCmd(d)
}

// handleLSPServerInstalled handles the result of an on-demand server install.
// On success, clears the detect cache and re-sends didOpen for the file that
// triggered the install.
func (m *AppModel) handleLSPServerInstalled(d msg.LSPServerInstalledMsg) tea.Cmd {
	delete(m.lspInstalling, lsp.ServerID(d.ServerID))
	if d.Err != nil {
		m.statusBar.SetFlash(d.ServerName + " install failed: " + d.Err.Error())
		return nil
	}
	detect.ClearCache()
	m.statusBar.SetFlash(d.ServerName + " installed")
	return m.lspDidOpenCmd(d.FilePath, d.LanguageID, d.Content)
}

// installLSPServerCmd returns a Cmd that installs a single language server
// binary in the background and reports the result.
func (m *AppModel) installLSPServerCmd(d msg.LSPServerMissingMsg) tea.Cmd {
	appCtx := m.ctx
	return func() tea.Msg {
		installer, err := lsp.NewInstaller()
		if err != nil {
			return msg.LSPServerInstalledMsg{
				ServerID:   d.ServerID,
				ServerName: d.ServerName,
				FilePath:   d.FilePath,
				LanguageID: d.LanguageID,
				Content:    d.Content,
				Err:        err,
			}
		}
		ctx, cancel := context.WithTimeout(appCtx, lsp.ProvisionTimeout)
		defer cancel()
		err = installer.EnsureServer(ctx, lsp.ServerID(d.ServerID))
		return msg.LSPServerInstalledMsg{
			ServerID:   d.ServerID,
			ServerName: d.ServerName,
			FilePath:   d.FilePath,
			LanguageID: d.LanguageID,
			Content:    d.Content,
			Err:        err,
		}
	}
}

// provisionLSPServers kicks off background LSP server detection and installation.
func (m *AppModel) provisionLSPServers() tea.Cmd {
	root := m.config.ProjectRoot
	scope := m.deps.Scope
	return func() tea.Msg {
		installer, err := lsp.NewInstaller()
		if err != nil {
			return msg.LSPProvisionDoneMsg{Err: err}
		}
		_ = scope.Go("lsp.provision", lsp.ProvisionTimeout, func(ctx context.Context) error {
			return installer.Provision(ctx, root)
		})
		return msg.LSPProvisionDoneMsg{}
	}
}

// lspDidOpenCmd returns a Cmd that notifies the LSP manager about a file open.
// If no server is available but one could be installed, returns an
// LSPServerMissingMsg to trigger on-demand installation.
func (m *AppModel) lspDidOpenCmd(filePath, languageID, content string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		err := mgr.NotifyDidOpen(ctx, root, filePath, languageID, content)
		if err != nil {
			return nil
		}
		// SuggestServer returns a disabled-but-installable server for this
		// file type. If NotifyDidOpen started a client successfully the
		// server is enabled and SuggestServer returns nil.
		suggested := mgr.SuggestServer(root, filePath)
		if suggested != nil {
			return msg.LSPServerMissingMsg{
				ServerID:   string(suggested.ID),
				ServerName: suggested.Name,
				FilePath:   filePath,
				LanguageID: languageID,
				Content:    content,
			}
		}
		return nil
	}
}

// lspDidChangeCmd returns a Cmd that sends a didChange notification.
func (m *AppModel) lspDidChangeCmd(filePath, content string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		_ = mgr.NotifyDidChange(ctx, root, filePath, content)
		return nil
	}
}

// lspDidSaveAsync sends a didSave notification as a tracked background goroutine.
func (m *AppModel) lspDidSaveAsync(filePath, content string) {
	if filePath == "" {
		return
	}
	mgr := m.lspManager
	root := m.config.ProjectRoot
	_ = m.deps.Scope.Go("lsp.didSave", lspNotifyTimeout, func(ctx context.Context) error {
		return mgr.NotifyDidSave(ctx, root, filePath, content)
	})
}

// lspDidCloseAsync sends a didClose notification as a tracked background goroutine.
func (m *AppModel) lspDidCloseAsync(filePath string) {
	if filePath == "" {
		return
	}
	mgr := m.lspManager
	root := m.config.ProjectRoot
	_ = m.deps.Scope.Go("lsp.didClose", lspNotifyTimeout, func(ctx context.Context) error {
		return mgr.NotifyDidClose(ctx, root, filePath)
	})
}

// lspReopenCmd returns a Cmd that atomically closes then re-opens a document
// in the LSP client. This avoids the race between async didClose (from
// exitEditMode) and async didOpen (from enterEditMode) where didOpen could
// run first, be silently rejected because the document is still tracked,
// and then didClose removes it — leaving the document permanently untracked.
func (m *AppModel) lspReopenCmd(filePath, languageID, content string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		// Close first so the document tracker drops the stale entry.
		_ = mgr.NotifyDidClose(ctx, root, filePath)

		// Now open — guaranteed to succeed because we just closed.
		err := mgr.NotifyDidOpen(ctx, root, filePath, languageID, content)
		if err != nil {
			return nil
		}

		suggested := mgr.SuggestServer(root, filePath)
		if suggested != nil {
			return msg.LSPServerMissingMsg{
				ServerID:   string(suggested.ID),
				ServerName: suggested.Name,
				FilePath:   filePath,
				LanguageID: languageID,
				Content:    content,
			}
		}
		return nil
	}
}

// lspHoverCmd returns a Cmd that requests hover information from the LSP.
func (m *AppModel) lspHoverCmd(filePath string, line, col int) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		result, err := mgr.Hover(ctx, root, filePath, line, col)
		return msg.LSPHoverMsg{
			FilePath: filePath,
			Line:     line,
			Col:      col,
			Result:   result,
			Err:      err,
		}
	}
}

// lspDocumentHighlightCmd returns a Cmd that requests document highlights from the LSP.
func (m *AppModel) lspDocumentHighlightCmd(filePath string, line, col int) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		highlights, err := mgr.DocumentHighlight(ctx, root, filePath, line, col)
		return msg.LSPDocumentHighlightMsg{
			FilePath:   filePath,
			Line:       line,
			Col:        col,
			Highlights: highlights,
			Err:        err,
		}
	}
}

// lspDefinitionCmd returns a Cmd that requests definition locations from the LSP.
// When forHover is true, the result decorates the hover tooltip instead of navigating.
func (m *AppModel) lspDefinitionCmd(filePath string, line, col int, forHover bool) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		locs, err := mgr.Definition(ctx, root, filePath, line, col)
		return msg.LSPDefinitionMsg{
			FilePath:  filePath,
			Locations: locs,
			Err:       err,
			ForHover:  forHover,
		}
	}
}

// lspReferencesCmd returns a Cmd that requests all references of the symbol
// at the given position. Results include the declaration and are enriched
// with line preview text by reading from disk.
func (m *AppModel) lspReferencesCmd(filePath string, line, col int) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		locs, err := mgr.References(ctx, root, filePath, line, col, true)
		return msg.LSPReferencesMsg{
			FilePath:  filePath,
			Line:      line,
			Col:       col,
			Locations: locs,
			Err:       err,
		}
	}
}

// handleLSPReferences builds reference entries and displays them in the file
// tree panel's references mode.
func (m *AppModel) handleLSPReferences(r msg.LSPReferencesMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("References: " + r.Err.Error())
		return nil
	}
	if len(r.Locations) == 0 {
		m.statusBar.SetFlash("No references found")
		return nil
	}

	symbol := m.focusedEditor().WordAt(r.Line, r.Col)
	entries := m.buildReferenceEntries(r.Locations)
	m.fileTree.SetReferences(symbol, entries)

	m.focus.SetFocus(component.FocusFileTree)
	m.syncFocusState()
	return nil
}

// buildReferenceEntries converts LSP locations to display entries, reading
// line preview text from disk and making paths relative to the project root.
func (m *AppModel) buildReferenceEntries(locs []lsp.Location) []filetree.ReferenceEntry {
	entries := make([]filetree.ReferenceEntry, 0, len(locs))
	for _, loc := range locs {
		absPath := lsp.FileURIToPath(loc.URI)
		relPath, err := filepath.Rel(m.config.ProjectRoot, absPath)
		if err != nil {
			relPath = absPath
		}
		preview := readLineFromFile(absPath, loc.Range.Start.Line)
		entries = append(entries, filetree.ReferenceEntry{
			FilePath: relPath,
			AbsPath:  absPath,
			Line:     loc.Range.Start.Line,
			Col:      loc.Range.Start.Character,
			Preview:  preview,
		})
	}
	return entries
}

// readLineFromFile reads a single line (0-indexed) from a file on disk.
// Returns the trimmed line content, or "" on error.
func readLineFromFile(path string, lineNum int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lineNum < 0 || lineNum >= len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[lineNum])
}

// ---------------------------------------------------------------------------
// LSP Document Symbols
// ---------------------------------------------------------------------------

// lspDocumentSymbolCmd returns a Cmd that requests document symbols for the
// given file. Results arrive as msg.LSPDocumentSymbolMsg.
func (m *AppModel) lspDocumentSymbolCmd(filePath string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		syms, err := mgr.DocumentSymbol(ctx, root, filePath)
		return msg.LSPDocumentSymbolMsg{
			FilePath: filePath,
			Symbols:  syms,
			Err:      err,
		}
	}
}

// lspFormatCmd sends a textDocument/formatting request to the language server.
// If flushContent is non-empty, a didChange notification is sent first so the
// server formats the latest buffer content.
func (m *AppModel) lspFormatCmd(filePath, flushContent string, editGen int) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	tabSize := m.focusedEditor().TabWidth()
	return func() tea.Msg {
		if flushContent != "" {
			_ = mgr.NotifyDidChange(ctx, root, filePath, flushContent)
		}
		edits, err := mgr.Format(ctx, root, filePath, tabSize, false)
		return msg.LSPFormatMsg{
			FilePath:       filePath,
			Edits:          edits,
			Err:            err,
			EditGeneration: editGen,
		}
	}
}

// handleLSPFormat applies formatting edits from the language server.
func (m *AppModel) handleLSPFormat(r msg.LSPFormatMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("Format: " + r.Err.Error())
		return nil
	}
	m.statusBar.SetFlash(m.applyFormatEdits(r))
	return nil
}

// applyFormatEdits validates and applies formatting edits, returning a
// status message. Separated from handleLSPFormat to keep CC < 4.
func (m *AppModel) applyFormatEdits(r msg.LSPFormatMsg) string {
	if r.EditGeneration != m.focusedEditor().EditGeneration() {
		return "Format: buffer changed, discarding"
	}
	if len(r.Edits) == 0 {
		return "Already formatted"
	}
	return fmt.Sprintf("Formatted (%d edits)", m.focusedEditor().ApplyTextEdits(r.Edits))
}

// handleLSPDocumentSymbol displays document symbols in the file tree panel.
func (m *AppModel) handleLSPDocumentSymbol(r msg.LSPDocumentSymbolMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("Symbols: " + r.Err.Error())
		return nil
	}
	if len(r.Symbols) == 0 {
		m.statusBar.SetFlash("No symbols found")
		return nil
	}
	title := filepath.Base(r.FilePath)
	entries := flattenSymbols(r.Symbols, 0)
	m.fileTree.SetDocumentSymbols(title, r.FilePath, entries)
	m.focus.SetFocus(component.FocusFileTree)
	m.syncFocusState()
	return nil
}

// flattenSymbols recursively converts hierarchical DocumentSymbol trees into a
// flat, depth-annotated SymbolEntry list for display in the file tree panel.
func flattenSymbols(syms []lsp.DocumentSymbol, depth int) []filetree.SymbolEntry {
	var result []filetree.SymbolEntry
	for _, s := range syms {
		result = append(result, filetree.SymbolEntry{
			Name:   s.Name,
			Kind:   s.Kind,
			Line:   s.SelectionRange.Start.Line,
			Col:    s.SelectionRange.Start.Character,
			Detail: s.Detail,
			Depth:  depth,
		})
		result = append(result, flattenSymbols(s.Children, depth+1)...)
	}
	return result
}

// ---------------------------------------------------------------------------
// LSP Rename
// ---------------------------------------------------------------------------

// lspPrepareRenameCmd sends a textDocument/prepareRename request.
func (m *AppModel) lspPrepareRenameCmd(filePath string, line, col int, newName string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		result, err := mgr.PrepareRename(ctx, root, filePath, line, col)
		return msg.LSPPrepareRenameMsg{
			FilePath: filePath,
			Line:     line,
			Col:      col,
			NewName:  newName,
			Result:   result,
			Err:      err,
		}
	}
}

// lspRenameCmd sends a textDocument/rename request.
func (m *AppModel) lspRenameCmd(filePath string, line, col int, newName string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		edit, err := mgr.Rename(ctx, root, filePath, line, col, newName)
		return msg.LSPRenameMsg{
			FilePath: filePath,
			NewName:  newName,
			Edit:     edit,
			Err:      err,
		}
	}
}

// handleLSPPrepareRename processes the prepareRename response and either
// proceeds with the rename or shows an error.
func (m *AppModel) handleLSPPrepareRename(r msg.LSPPrepareRenameMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("Rename: " + r.Err.Error())
		return nil
	}
	if r.Result == nil {
		m.statusBar.SetFlash("Cannot rename symbol at cursor")
		return nil
	}
	return m.lspRenameCmd(r.FilePath, r.Line, r.Col, r.NewName)
}

// handleLSPRename applies the workspace edit from a rename response.
func (m *AppModel) handleLSPRename(r msg.LSPRenameMsg) tea.Cmd {
	if r.Err != nil {
		m.statusBar.SetFlash("Rename: " + r.Err.Error())
		return nil
	}
	if r.Edit == nil || len(r.Edit.FileEdits) == 0 {
		m.statusBar.SetFlash("Rename: no changes")
		return nil
	}
	n := m.applyWorkspaceEdit(r.Edit)
	m.statusBar.SetFlash(fmt.Sprintf("Renamed to %s in %d file(s)", r.NewName, n))
	return nil
}

// applyWorkspaceEdit applies a workspace edit across files. Open editor
// buffers are edited in-place (with undo support); other files are
// modified on disk. Returns the number of files changed.
func (m *AppModel) applyWorkspaceEdit(edit *lsp.WorkspaceEdit) int {
	editorPath := m.focusedEditor().FilePath()
	changed := 0
	for path, edits := range edit.FileEdits {
		if path == editorPath {
			m.focusedEditor().ApplyTextEdits(edits)
			changed++
			continue
		}
		if applyEditsToFile(path, edits) {
			changed++
		}
	}
	return changed
}

// applyEditsToFile reads a file, applies text edits, and writes it back.
// Edits are applied in reverse order to preserve earlier offsets.
func applyEditsToFile(path string, edits []lsp.TextEdit) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")

	// Sort edits reverse by position so earlier offsets remain valid.
	sorted := make([]lsp.TextEdit, len(edits))
	copy(sorted, edits)
	slices.SortFunc(sorted, func(a, b lsp.TextEdit) int {
		if a.Range.Start.Line != b.Range.Start.Line {
			return b.Range.Start.Line - a.Range.Start.Line
		}
		return b.Range.Start.Character - a.Range.Start.Character
	})

	for _, edit := range sorted {
		lines = spliceLines(lines, edit)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644) == nil
}

// spliceLines applies a single text edit to a line slice.
func spliceLines(lines []string, edit lsp.TextEdit) []string {
	startLine := edit.Range.Start.Line
	endLine := edit.Range.End.Line
	if startLine >= len(lines) {
		return lines
	}
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}

	startCol := clampCol(lines[startLine], edit.Range.Start.Character)
	endCol := clampCol(lines[endLine], edit.Range.End.Character)

	prefix := lines[startLine][:startCol]
	suffix := lines[endLine][endCol:]
	replacement := prefix + edit.NewText + suffix

	newLines := strings.Split(replacement, "\n")

	// Splice: replace lines[startLine..endLine] with newLines.
	result := make([]string, 0, len(lines)-((endLine-startLine)+1)+len(newLines))
	result = append(result, lines[:startLine]...)
	result = append(result, newLines...)
	result = append(result, lines[endLine+1:]...)
	return result
}

// clampCol clamps a column index to the valid range for a line.
func clampCol(line string, col int) int {
	if col < 0 {
		return 0
	}
	if col > len(line) {
		return len(line)
	}
	return col
}

// handleHoverDefinition stores the definition symbol and package path on the
// active hover tooltip. If the hover content hasn't arrived yet, stashes
// the info so it can be applied when the hover activates.
func (m *AppModel) handleHoverDefinition(d msg.LSPDefinitionMsg) tea.Cmd {
	if d.Err != nil || len(d.Locations) == 0 {
		return nil
	}
	loc := d.Locations[0]
	filePath := lsp.FileURIToPath(loc.URI)

	// When hover is for preview, use the preview panel for word lookup.
	var word string
	if m.hoverForPreview {
		word = m.previewPanel.WordAt(m.hoverMouseLine, m.hoverMouseCol)
	} else {
		word = m.focusedEditor().WordAt(m.hoverMouseLine, m.hoverMouseCol)
	}

	pkgName, pkgPath := defPackageInfo(filePath, m.config.ProjectRoot)
	symbol := word
	if pkgName != "" && word != "" {
		symbol = pkgName + "." + word
	}

	if m.hoverForPreview {
		if m.previewPanel.HoverActive() {
			m.previewPanel.SetHoverDefinition(symbol, pkgPath)
		} else {
			m.pendingHoverSymbol = symbol
			m.pendingHoverPkgPath = pkgPath
		}
		return nil
	}

	if m.focusedEditor().HoverActive() {
		m.focusedEditor().SetHoverDefinition(symbol, pkgPath)
	} else {
		// Hover content hasn't arrived yet; stash for when it does.
		m.pendingHoverSymbol = symbol
		m.pendingHoverPkgPath = pkgPath
	}
	return nil
}

// handlePreviewHoverMsg processes an LSP hover response for the preview pane.
func (m *AppModel) handlePreviewHoverMsg(h msg.LSPHoverMsg) tea.Cmd {
	// Staleness check using preview panel methods.
	wordStart, _ := m.previewPanel.WordBoundsAt(h.Line, h.Col)
	if h.Line != m.hoverMouseLine || wordStart != m.hoverMouseWordStart {
		return nil
	}
	if h.Err != nil || h.Result == nil || h.FilePath != m.previewPanel.FilePath() {
		return nil
	}
	m.previewPanel.ShowHover(h.Result.Contents, h.Line, h.Col)
	// Apply definition info that arrived before the hover content.
	if m.previewPanel.HoverActive() && m.pendingHoverSymbol != "" {
		m.previewPanel.SetHoverDefinition(m.pendingHoverSymbol, m.pendingHoverPkgPath)
		m.pendingHoverSymbol = ""
		m.pendingHoverPkgPath = ""
	}
	return nil
}

// defPackageInfo extracts a Go package name and clean display path from a
// definition file path. Strips module versions and GOROOT/GOMODCACHE prefixes.
func defPackageInfo(filePath, projectRoot string) (pkgName, displayPath string) {
	// Local project file.
	if rel, err := filepath.Rel(projectRoot, filePath); err == nil && !strings.HasPrefix(rel, "..") {
		dir := filepath.Dir(rel)
		if dir == "." {
			dir = filepath.Base(projectRoot)
		}
		return filepath.Base(dir), dir
	}

	// Go module cache: .../pkg/mod/github.com/foo/bar@v1.0.0/pkg/file.go
	if idx := strings.Index(filePath, "/pkg/mod/"); idx >= 0 {
		modRel := filePath[idx+len("/pkg/mod/"):]
		dir := filepath.Dir(modRel)
		dir = stripModVersion(dir)
		return filepath.Base(dir), dir
	}

	// Go standard library: .../go/src/context/context.go
	if idx := strings.Index(filePath, "/src/"); idx >= 0 {
		srcRel := filePath[idx+len("/src/"):]
		dir := filepath.Dir(srcRel)
		return filepath.Base(dir), dir
	}

	// Fallback.
	dir := filepath.Dir(filePath)
	return filepath.Base(dir), dir
}

// stripModVersion removes @version suffixes from Go module path components.
func stripModVersion(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if at := strings.Index(p, "@"); at >= 0 {
			parts[i] = p[:at]
		}
	}
	return strings.Join(parts, "/")
}

// lspCompletionCmd returns a Cmd that requests completion items from the LSP.
// If flushContent is non-empty, a didChange notification is sent first so
// the server sees the latest buffer content before the request arrives.
func (m *AppModel) lspCompletionCmd(filePath string, line, col int, flushContent string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		if flushContent != "" {
			_ = mgr.NotifyDidChange(ctx, root, filePath, flushContent)
		}
		items, err := mgr.Completion(ctx, root, filePath, line, col)
		return msg.LSPCompletionMsg{
			FilePath: filePath,
			Items:    items,
			Err:      err,
		}
	}
}

// lspSignatureHelpCmd returns a Cmd that requests signature help from the LSP.
// If flushContent is non-empty, a didChange notification is sent first so
// the server sees the trigger character before the request arrives.
func (m *AppModel) lspSignatureHelpCmd(filePath string, line, col int, flushContent string) tea.Cmd {
	mgr := m.lspManager
	root := m.config.ProjectRoot
	ctx := m.ctx
	return func() tea.Msg {
		if flushContent != "" {
			_ = mgr.NotifyDidChange(ctx, root, filePath, flushContent)
		}
		result, err := mgr.SignatureHelp(ctx, root, filePath, line, col)
		return msg.LSPSignatureHelpMsg{
			FilePath: filePath,
			Line:     line,
			Col:      col,
			Result:   result,
			Err:      err,
		}
	}
}

// extToLSPLanguage maps file extensions to LSP language identifiers.
var extToLSPLanguage = map[string]string{
	".go":   "go",
	".ts":   "typescript",
	".tsx":  "typescriptreact",
	".js":   "javascript",
	".jsx":  "javascriptreact",
	".py":   "python",
	".rs":   "rust",
	".c":    "c",
	".h":    "c",
	".cpp":  "cpp",
	".hpp":  "cpp",
	".rb":   "ruby",
	".java": "java",
	".yaml": "yaml",
	".yml":  "yaml",
	".tf":   "terraform",
	".lua":  "lua",
	".zig":  "zig",
	".ml":   "ocaml",
}

// isMarkdownFile reports whether path has a markdown extension.
func isMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

// detectEditorLanguage returns the LSP language ID for a file path.
func detectEditorLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if lang, ok := extToLSPLanguage[ext]; ok {
		return lang
	}
	return ""
}

// handleFileOpen reads a file from disk and displays it in the code viewer.
// Always marks the file as active in the tree regardless of read success so
// the explorer highlights it immediately.
func (m *AppModel) handleFileOpen(o msg.FileOpenMsg) tea.Cmd {
	m.fileTree.SetActiveFile(o.Path)
	m.fileTree.RevealPath(o.Path)

	data, err := os.ReadFile(o.Path)
	if err != nil {
		m.statusBar.SetFlash("Cannot open: " + o.Name)
		return nil
	}
	content := string(data)
	m.codePanel.SetContent(content, o.Path, o.Language)

	// Track which content the LSP should analyze.
	lspContent := content

	if m.viewMode == ViewEdit {
		oldPath := m.focusedEditor().FilePath()

		// Already on this file in the focused pane — just jump.
		if oldPath == o.Path {
			m.appendTab(o.Path)
			m.focusCodePanel()
			m.syncFocusState()
			if o.Line > 0 {
				targetLine := o.Line - 1
				if o.CursorCol > 0 {
					m.focusedEditor().GoToLineCol(targetLine, o.CursorCol)
				} else {
					m.focusedEditor().GoToLine(targetLine)
				}
				m.codePanel.ScrollToLine(targetLine)
				m.setJumpMarker(targetLine, o.Col, o.EndCol)
			}
			return nil
		}

		// Check if the file is already open in another pane.
		if pid, _ := m.findPaneWithTab(o.Path); pid != 0 && pid != m.focusedPane {
			m.focusedPane = pid
			m.focusCodePanel()
			m.syncFocusState()
			// Switch to the tab within that pane.
			ps := m.paneEditors[pid]
			for i, p := range ps.tabOrder {
				if p == o.Path {
					m.switchToTab(i)
					break
				}
			}
			if o.Line > 0 {
				targetLine := o.Line - 1
				if o.CursorCol > 0 {
					m.focusedEditor().GoToLineCol(targetLine, o.CursorCol)
				} else {
					m.focusedEditor().GoToLine(targetLine)
				}
				m.codePanel.ScrollToLine(targetLine)
				m.setJumpMarker(targetLine, o.Col, o.EndCol)
			}
			return nil
		}

		// Close the previous LSP document before switching.
		if oldPath != "" {
			m.lspDidCloseAsync(oldPath)
		}

		// Detach current editor state into the tiered cache.
		m.detachCurrentEditor()

		// Track the new file in the tab bar and resize the editor
		// so the tab bar doesn't push content past the panel bottom.
		m.appendTab(o.Path)
		m.resizeInlineEditor()

		// Restore from cache if available, otherwise load from disk.
		if m.restoreFromCache(o.Path) {
			lspContent = m.focusedEditor().Content()
		} else {
			m.focusedEditor().OpenFile(o.Path, content, o.Language)
		}

		// Transfer focus to the editor pane receiving the file.
		m.focusCodePanel()
		m.syncFocusState()
	}

	if o.Line > 0 {
		targetLine := o.Line - 1
		m.codePanel.ScrollToLine(targetLine)
		if m.viewMode == ViewEdit {
			if o.CursorCol > 0 {
				m.focusedEditor().GoToLineCol(targetLine, o.CursorCol)
			} else {
				m.focusedEditor().GoToLine(targetLine)
			}
			m.setJumpMarker(targetLine, o.Col, o.EndCol)
		}
	}

	m.syncEditorWarpLines()
	return m.lspDidOpenCmd(o.Path, detectEditorLanguage(o.Path), lspContent)
}

// setJumpMarker activates the visual jump marker on the inline editor when
// column info is available. When endCol is 0, derives bounds from the word
// at the given column.
func (m *AppModel) setJumpMarker(line, col, endCol int) {
	if col <= 0 && endCol <= 0 {
		return
	}
	startCol, ec := col, endCol
	if ec <= startCol {
		startCol, ec = m.focusedEditor().WordBoundsAt(line, col)
	}
	if ec > startCol {
		m.focusedEditor().SetJumpMarker(line, startCol, ec)
	}
}

func (m *AppModel) handleCloseEditor() tea.Cmd {
	comp, cmd := m.editorOverlay.Update(msg.CloseEditorMsg{})
	m.editorOverlay = comp.(*editor.Model)
	m.overlay = overlayNone
	return cmd
}

// handleFileReplaced reloads the editor buffer when a multi-file replace
// modified the currently open file on disk (only if there are no unsaved changes).
func (m *AppModel) handleFileReplaced(r msg.FileReplacedMsg) tea.Cmd {
	if m.viewMode != ViewEdit || m.focusedEditor().FilePath() != r.Path {
		return nil
	}
	if m.focusedEditor().Modified() {
		return nil
	}
	return m.reloadEditorFromDisk(r.Path)
}

// handleMultiFileReplaceDone flashes a status message after multi-file replace-all.
func (m *AppModel) handleMultiFileReplaceDone(r msg.MultiFileReplaceDoneMsg) tea.Cmd {
	status := fmt.Sprintf("Replaced %d occurrences in %d files", r.TotalReplaced, r.FilesChanged)
	if r.Skipped > 0 {
		status += fmt.Sprintf(" (%d skipped)", r.Skipped)
	}
	m.statusBar.SetFlash(status)
	return nil
}

// reloadEditorFromDisk reads the file from disk and reloads the editor buffer.
func (m *AppModel) reloadEditorFromDisk(path string) tea.Cmd {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	lang := detectEditorLanguage(path)
	m.focusedEditor().OpenFile(path, content, lang)
	m.codePanel.SetContent(content, path, lang)
	m.editorCache.Delete(path)
	return nil
}

func (m *AppModel) handleStreamStartTelemetry(start msg.StreamStartMsg) tea.Cmd {
	m.recordStreamStart(start.CorrelationID)
	m.trackStreamStart(start.CorrelationID, start.AgentID)
	m.setActiveRoute(start.CorrelationID, start.AgentID)
	newAgent := normalizeAgentID(start.AgentID)
	if m.engagedAgentID != "" && newAgent != "" && newAgent != m.engagedAgentID && newAgent != "guide" {
		m.statusBar.SetFlash(m.engagedAgentID + " -> " + newAgent)
	}
	m.setEngagedAgent(start.AgentID)
	if !m.shouldRenderStreamEvent(start.CorrelationID) {
		return nil
	}
	m.publishStreamStartActivity(start.AgentID)
	m.statusBar.SetTokenPhase(status.PhaseOutput)
	return m.propagate(start)
}

func (m *AppModel) handleStreamChunkTelemetry(chunk msg.StreamChunkMsg) tea.Cmd {
	chunk.Text = redactSecrets(chunk.Text)
	m.trackStreamChunk(chunk.CorrelationID, chunk.Text)
	if chunk.InputTokens > 0 {
		m.applyEarlyInputTokens(chunk.CorrelationID, chunk.InputTokens)
	}
	if !m.shouldRenderStreamEvent(chunk.CorrelationID) {
		return nil
	}
	if strings.TrimSpace(chunk.Text) == "" {
		return nil // Usage-only chunk; status bar updated, no chat content to render.
	}
	// Record HadChunk only after confirming the chunk will be rendered.
	// Setting HadChunk before this point would cause shouldSuppressStreamedRouteResponse
	// to suppress the GuideResponseMsg even when no chunks were actually displayed.
	m.recordStreamChunk(chunk.CorrelationID, chunk.Text)
	return m.propagate(chunk)
}

func (m *AppModel) handleStreamProgressTelemetry(progress msg.StreamProgressMsg) tea.Cmd {
	m.setActiveRoute(progress.CorrelationID, progress.AgentID)
	progress.Message = redactSecrets(progress.Message)
	if !m.shouldRenderStreamEvent(progress.CorrelationID) {
		return nil
	}
	return m.propagate(progress)
}

func (m *AppModel) handleStreamCompleteTelemetry(done msg.StreamCompleteMsg) tea.Cmd {
	m.recordStreamComplete(done.CorrelationID)
	shouldRender := m.shouldRenderStreamEvent(done.CorrelationID)
	delete(m.interruptedCorrelations, done.CorrelationID)
	m.applyRealStreamUsage(done.CorrelationID, done.InputTokens, done.OutputTokens)
	m.finalizeStreamUsage(done.CorrelationID, true, "")
	m.markQueueEntryByCorrelation(done.CorrelationID, true)
	m.clearActiveRoute(done.CorrelationID)
	if !shouldRender {
		m.statusBar.StopSpinner()
		return m.tryAdvanceQueue()
	}
	// AuthoritativeText in the completion event delivers final content to the
	// chat accumulator, equivalent to having received text chunks. Record it
	// so shouldSuppressStreamedRouteResponse suppresses the duplicate
	// GuideResponseMsg that follows.
	if strings.TrimSpace(done.AuthoritativeText) != "" {
		m.recordStreamChunk(done.CorrelationID, done.AuthoritativeText)
	}
	if advCmd := m.tryAdvanceQueue(); advCmd != nil {
		return tea.Batch(m.propagate(done), advCmd)
	}
	return m.propagate(done)
}

// isTerminalStreamError returns true when the error represents a condition
// that no guide-level retry, reroute, or circuit-breaker recovery can fix.
// For these errors the agent panel animations should stop immediately.
//
// Non-terminal errors (rate-limit exhaustion, server 5xx, timeouts) may still
// be recovered by the Guide via reroute or retry-queue, so animations persist
// until either a terminal event or a successful recovery arrives.
func isTerminalStreamError(err error) bool {
	if err == nil {
		return false
	}

	// Tiered error taxonomy: Permanent and UserFixable are unrecoverable.
	var te *coreerrors.TieredError
	if errors.As(err, &te) {
		return te.Tier == coreerrors.TierPermanent || te.Tier == coreerrors.TierUserFixable
	}

	// Provider errors: non-retryable status codes (401, 402, 403, 400, 404)
	// indicate credential, permission, or configuration problems the Guide
	// cannot route around.
	var pe *providers.ProviderError
	if errors.As(err, &pe) {
		return !pe.Retryable
	}

	// Sentinel errors from the providers package.
	switch {
	case errors.Is(err, providers.ErrAuthenticationError):
		return true
	case errors.Is(err, providers.ErrQuotaExceeded):
		return true
	case errors.Is(err, providers.ErrProviderNotFound):
		return true
	case errors.Is(err, providers.ErrModelNotSupported):
		return true
	case errors.Is(err, providers.ErrInvalidConfig):
		return true
	}

	return false
}

func (m *AppModel) handleStreamErrorTelemetry(streamErr msg.StreamErrorMsg) tea.Cmd {
	streamErr.Err = redactedError(streamErr.Err)
	summary := ""
	if streamErr.Err != nil {
		summary = streamErr.Err.Error()
	}
	if m.shouldSuppressErrorAfterSuccess(streamErr.CorrelationID) {
		m.logSuppressedLLMError("stream", streamErr.CorrelationID, m.streamAgentID(streamErr.CorrelationID), streamErr.Err, "success_already_returned")
		m.discardStreamUsage(streamErr.CorrelationID)
		return nil
	}
	m.clearRecordedStream(streamErr.CorrelationID)
	delete(m.interruptedCorrelations, streamErr.CorrelationID)
	m.finalizeStreamUsage(streamErr.CorrelationID, false, summary)
	m.markQueueEntryByCorrelation(streamErr.CorrelationID, false)
	m.clearActiveRoute(streamErr.CorrelationID)
	if isTerminalStreamError(streamErr.Err) {
		m.agentPanel.DemoteAllActive()
		// Pause the queue on terminal errors to prevent blindly dispatching
		// into a broken agent.
		m.promptQueue.SetPaused(true)
		m.recalcLayout()
		m.viewDirty = true
	}
	if advCmd := m.tryAdvanceQueue(); advCmd != nil {
		return tea.Batch(m.propagate(streamErr), advCmd)
	}
	return m.propagate(streamErr)
}

func (m *AppModel) trackStreamStart(correlationID, agentID string) {
	if correlationID == "" {
		return
	}
	m.streamUsage[correlationID] = streamUsageEntry{
		AgentID:   normalizeAgentID(agentID),
		StartedAt: time.Now(),
	}
}

func (m *AppModel) trackStreamChunk(correlationID, text string) {
	if correlationID == "" {
		return
	}
	state, ok := m.streamUsage[correlationID]
	if !ok {
		return
	}
	added := estimateGuideTokens(text)
	state.Tokens += added
	m.streamUsage[correlationID] = state
	m.totalCompletionTokens += added
	if m.statusBar != nil {
		m.statusBar.SetTokens(m.totalPromptTokens, m.totalCompletionTokens)
	}
}

func (m *AppModel) recordStreamStart(correlationID string) {
	if correlationID == "" {
		return
	}
	m.ensureStreamedResponseState()
	m.pruneRecordedStreams(time.Now())
	m.streamedResponses[correlationID] = streamedResponseState{
		HadChunk:  false,
		Completed: false,
		Succeeded: false,
		SeenAt:    time.Now(),
	}
}

func (m *AppModel) recordStreamChunk(correlationID, text string) {
	if correlationID == "" || strings.TrimSpace(text) == "" {
		return
	}
	m.ensureStreamedResponseState()
	state := m.streamedResponses[correlationID]
	state.HadChunk = true
	state.SeenAt = time.Now()
	m.streamedResponses[correlationID] = state
}

func (m *AppModel) recordStreamComplete(correlationID string) {
	if correlationID == "" {
		return
	}
	m.ensureStreamedResponseState()
	state := m.streamedResponses[correlationID]
	state.Completed = true
	state.SeenAt = time.Now()
	m.streamedResponses[correlationID] = state
	m.pruneRecordedStreams(time.Now())
}

func (m *AppModel) markSuccessfulRouteResponse(correlationID string) {
	if correlationID == "" {
		return
	}
	m.ensureStreamedResponseState()
	state := m.streamedResponses[correlationID]
	state.Succeeded = true
	state.SeenAt = time.Now()
	m.streamedResponses[correlationID] = state
}

func (m *AppModel) shouldSuppressErrorAfterSuccess(correlationID string) bool {
	if correlationID == "" || m.streamedResponses == nil {
		return false
	}
	state, ok := m.streamedResponses[correlationID]
	if !ok {
		return false
	}
	return state.Succeeded
}

func (m *AppModel) streamAgentID(correlationID string) string {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return guideAgentID
	}
	if usage, ok := m.streamUsage[correlationID]; ok {
		return normalizeAgentID(usage.AgentID)
	}
	if strings.TrimSpace(m.activeRoute.CorrelationID) == correlationID {
		return normalizeAgentID(m.activeRoute.AgentID)
	}
	return guideAgentID
}

func (m *AppModel) logSuppressedLLMError(kind, correlationID, agentID string, err error, reason string) {
	if m == nil || m.walLogger == nil || err == nil {
		return
	}
	m.walLogger.Warn(
		"ui llm error suppressed",
		"kind", strings.TrimSpace(kind),
		"correlation_id", strings.TrimSpace(correlationID),
		"agent_id", normalizeAgentID(agentID),
		"reason", strings.TrimSpace(reason),
		"error", err.Error(),
	)
}

func (m *AppModel) clearRecordedStream(correlationID string) {
	if correlationID == "" || m.streamedResponses == nil {
		return
	}
	delete(m.streamedResponses, correlationID)
}

func (m *AppModel) shouldSuppressStreamedRouteResponse(correlationID string, hasErr bool) bool {
	if correlationID == "" || hasErr || m.streamedResponses == nil {
		return false
	}
	state, ok := m.streamedResponses[correlationID]
	if !ok {
		return false
	}
	// Suppress when content was already delivered via stream chunks.
	// Progress-only streams (start→complete with no chunks) are not
	// suppressed since they never delivered user-visible content.
	delivered := state.HadChunk
	if state.Succeeded {
		state.SeenAt = time.Now()
		m.streamedResponses[correlationID] = state
		return delivered
	}
	delete(m.streamedResponses, correlationID)
	return delivered
}

func (m *AppModel) ensureStreamedResponseState() {
	if m.streamedResponses != nil {
		return
	}
	m.streamedResponses = make(map[string]streamedResponseState)
}

func (m *AppModel) pruneRecordedStreams(now time.Time) {
	if m.streamedResponses == nil {
		return
	}
	for correlationID, state := range m.streamedResponses {
		if now.Sub(state.SeenAt) <= streamedResponseStateTTL {
			continue
		}
		delete(m.streamedResponses, correlationID)
	}
}

func (m *AppModel) finalizeStreamUsage(correlationID string, success bool, summary string) {
	if correlationID == "" {
		return
	}
	state, ok := m.streamUsage[correlationID]
	if !ok {
		return
	}
	delete(m.streamUsage, correlationID)

	// Input tokens represent the full conversation context sent to the agent,
	// i.e. the actual context window occupancy. Use them directly when available
	// (no decay — each call sends the full history). Fall back to output-based
	// estimation when real input tokens are unavailable.
	var contextUsage float64
	if state.InputTokens > 0 {
		contextUsage = m.setAgentContextUsage(state.AgentID, state.InputTokens)
	} else {
		contextUsage = m.bumpAgentContextUsage(state.AgentID, state.Tokens+guideResponseOverheadTokens)
	}
	m.publishStreamActivity(state.AgentID, success, summary, contextUsage)

	if m.statusBar != nil && len(m.streamUsage) == 0 {
		m.statusBar.SetTokenPhase(status.PhaseIdle)
	}
}

func (m *AppModel) discardStreamUsage(correlationID string) {
	if correlationID == "" {
		return
	}
	delete(m.streamUsage, correlationID)
	if m.statusBar != nil && len(m.streamUsage) == 0 {
		m.statusBar.SetTokenPhase(status.PhaseIdle)
	}
}

// applyEarlyInputTokens applies real input tokens as soon as the provider
// reports them (at stream start), avoiding the need to wait for completion.
func (m *AppModel) applyEarlyInputTokens(correlationID string, inputTokens int) {
	if inputTokens <= 0 {
		return
	}
	state, ok := m.streamUsage[correlationID]
	if !ok || state.EarlyInputApplied {
		return
	}
	state.EarlyInputApplied = true
	state.InputTokens = inputTokens
	m.streamUsage[correlationID] = state
	m.totalPromptTokens += inputTokens
	if m.statusBar != nil {
		m.statusBar.SetTokens(m.totalPromptTokens, m.totalCompletionTokens)
	}
}

// applyRealStreamUsage corrects the accumulated token estimate with real
// provider-reported values when available. Called before finalizeStreamUsage
// so the corrected values are used for context usage computation.
func (m *AppModel) applyRealStreamUsage(correlationID string, inputTokens, outputTokens int) {
	if inputTokens == 0 && outputTokens == 0 {
		return
	}
	state, ok := m.streamUsage[correlationID]
	if !ok {
		return
	}
	if outputTokens > 0 {
		m.totalCompletionTokens -= state.Tokens
		m.totalCompletionTokens += outputTokens
		state.Tokens = outputTokens
		m.streamUsage[correlationID] = state
	}
	if inputTokens > 0 {
		state.InputTokens = inputTokens
		m.streamUsage[correlationID] = state
		if !state.EarlyInputApplied {
			m.totalPromptTokens += inputTokens
		}
	}
	if m.statusBar != nil {
		m.statusBar.SetTokens(m.totalPromptTokens, m.totalCompletionTokens)
	}
}

func normalizeAgentID(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return guideAgentID
	}
	return normalized
}

func redactedError(err error) error {
	return redact.Error(err)
}

func (m *AppModel) publishStreamActivity(agentID string, success bool, summary string, contextUsage float64) {
	if m.deps.ActivityBus == nil {
		return
	}
	id, name, agentType := resolveAgentIdentity(agentID, "")
	eventType := events.EventTypeLLMResponse
	outcome := events.OutcomeSuccess
	content := "Streaming response complete"
	if !success {
		eventType = events.EventTypeAgentError
		outcome = events.OutcomeFailure
		content = "Streaming response failed"
		if trimmed := strings.TrimSpace(summary); trimmed != "" {
			content = summarizeActivityContent(trimmed)
		}
	}
	m.deps.ActivityBus.Publish(&events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		Timestamp: time.Now(),
		AgentID:   id,
		Content:   content,
		Outcome:   outcome,
		Data: map[string]any{
			"agent_type":    agentType,
			"agent_name":    name,
			"context_usage": contextUsage,
		},
	})
}

func (m *AppModel) publishStreamStartActivity(agentID string) {
	if m.deps.ActivityBus == nil {
		return
	}
	id, name, agentType := resolveAgentIdentity(agentID, "")
	m.deps.ActivityBus.Publish(&events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: events.EventTypeLLMRequest,
		Timestamp: time.Now(),
		AgentID:   id,
		Content:   "Streaming response started",
		Outcome:   events.OutcomePending,
		Data: map[string]any{
			"agent_type": agentType,
			"agent_name": name,
		},
	})
}

func (m *AppModel) handleGuideResponse(r msg.GuideResponseMsg) tea.Cmd {
	if r.Err == nil {
		m.markSuccessfulRouteResponse(r.CorrelationID)
	}
	if r.Err != nil && m.shouldSuppressErrorAfterSuccess(r.CorrelationID) {
		m.logSuppressedLLMError("route", r.CorrelationID, r.AgentID, r.Err, "success_already_returned")
		m.clearRecordedStream(r.CorrelationID)
		m.discardStreamUsage(r.CorrelationID)
		m.statusBar.StopSpinner()
		return nil
	}
	if m.shouldSuppressStreamedRouteResponse(r.CorrelationID, r.Err != nil) {
		m.clearActiveRoute(r.CorrelationID)
		m.discardStreamUsage(r.CorrelationID)
		m.statusBar.StopSpinner()
		return nil
	}
	source := chat.SourceAgent
	content := redactSecrets(r.Content)
	if r.Err != nil {
		source = chat.SourceError
		content = redactSecrets(r.Err.Error())
	}
	m.clearActiveRoute(r.CorrelationID)
	added := estimateGuideTokens(content)
	contextUsage := m.bumpAgentContextUsage(r.AgentID, added+guideResponseOverheadTokens)
	m.totalCompletionTokens += added
	m.statusBar.SetTokens(m.totalPromptTokens, m.totalCompletionTokens)
	m.statusBar.SetTokenPhase(status.PhaseIdle)
	m.publishResponseActivity(r, source, content, contextUsage)
	agentDisplay := r.AgentID
	if r.AgentName != "" {
		agentDisplay = r.AgentName
	}
	entry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    source,
		AgentType: agentDisplay,
		AgentID:   r.AgentID,
		Content:   content,
		Height:    -1,
	}
	m.chat.FinishThinking(entry)
	return nil
}

func (m *AppModel) publishResponseActivity(
	r msg.GuideResponseMsg,
	source chat.ChatSource,
	content string,
	contextUsage float64,
) {
	if m.deps.ActivityBus == nil {
		return
	}
	agentID, agentName, agentType := resolveAgentIdentity(r.AgentID, r.AgentName)
	outcome := events.OutcomeSuccess
	eventType := events.EventTypeLLMResponse
	if source == chat.SourceError {
		outcome = events.OutcomeFailure
		eventType = events.EventTypeAgentError
	}
	m.deps.ActivityBus.Publish(&events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		Timestamp: time.Now(),
		AgentID:   agentID,
		Content:   summarizeActivityContent(content),
		Outcome:   outcome,
		Data: map[string]any{
			"agent_type":    agentType,
			"agent_name":    agentName,
			"context_usage": contextUsage,
		},
	})
}

func resolveAgentIdentity(agentID, agentName string) (string, string, string) {
	id := strings.TrimSpace(agentID)
	name := strings.TrimSpace(agentName)
	if id == "" {
		id = strings.ToLower(name)
	}
	if id == "" {
		id = "agent"
	}
	if name == "" {
		name = id
	}
	agentType := strings.ToLower(id)
	if strings.EqualFold(id, guideAgentID) {
		return guideAgentID, guideAgentName, guideAgentType
	}
	return id, name, agentType
}

func summarizeActivityContent(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "Response generated"
	}
	const maxActivityContentRunes = 160
	runes := []rune(trimmed)
	if len(runes) <= maxActivityContentRunes {
		return trimmed
	}
	return string(runes[:maxActivityContentRunes]) + "..."
}

// handleConflictPreview processes a dry-run merge result. If conflicts are
// found, shows a list modal; otherwise proceeds immediately.
func (m *AppModel) handleConflictPreview(typed msg.ConflictPreviewMsg) tea.Cmd {
	if typed.Result == nil || typed.Result.Clean {
		// No conflicts — proceed directly.
		if m.pendingSeqOp != nil {
			op := m.pendingSeqOp
			m.pendingSeqOp = nil
			return m.executeSequencerOp(op)
		}
		return nil
	}

	conflicts := typed.Result.Conflicts
	items := make([]modal.ListModalItem, len(conflicts))
	for i, c := range conflicts {
		items[i] = modal.ListModalItem{
			Label: c.Path,
			Color: m.config.Theme().Palette.Error,
		}
	}
	footer := fmt.Sprintf("%d file(s) will conflict", len(conflicts))
	lm := modal.NewListModal("Conflicts Detected ("+typed.Op+")", items, footer,
		[]string{"Continue", "Cancel"}, m.config.Theme())
	m.modalOverlay.Push(lm)
	if m.pendingSeqOp != nil {
		m.pendingSeqOp.phase = 2
	}
	return nil
}

// handleIntegrationDetected processes cherry-pick duplicate detection results.
// Shows a list modal when already-integrated commits are found.
func (m *AppModel) handleIntegrationDetected(typed msg.IntegrationDetectedMsg) tea.Cmd {
	// Count integrated commits.
	var integrated int
	for _, r := range typed.Results {
		if r.Integrated {
			integrated++
		}
	}

	if integrated == 0 {
		// No duplicates — proceed to conflict preview.
		if m.pendingSeqOp != nil {
			return m.conflictPreviewCherryPickCmd(m.pendingSeqOp.hashes, nil)
		}
		return nil
	}

	items := make([]modal.ListModalItem, 0, len(typed.Results))
	for _, r := range typed.Results {
		badge := ""
		var color lipgloss.Color
		if r.Integrated {
			badge = "INTEGRATED"
			color = m.config.Theme().Palette.Teal
		}
		items = append(items, modal.ListModalItem{
			Label:  r.CommitHash[:min(len(r.CommitHash), 8)],
			Detail: r.Subject,
			Badge:  badge,
			Color:  color,
		})
	}
	footer := fmt.Sprintf("%d of %d commit(s) already integrated", integrated, len(typed.Results))
	lm := modal.NewListModal("Cherry-Pick Integration Check", items, footer,
		[]string{"Apply All", "Cancel"}, m.config.Theme())
	m.modalOverlay.Push(lm)
	if m.pendingSeqOp != nil {
		m.pendingSeqOp.phase = 1
	}
	return nil
}

// handleAbortPreserved handles the result of a pre-abort preservation.
func (m *AppModel) handleAbortPreserved(typed msg.AbortPreservedMsg) tea.Cmd {
	if typed.Err != nil {
		m.statusBar.SetFlash("Abort preservation failed: " + typed.Err.Error())
	} else if typed.Preservation != nil {
		parts := []string{"State preserved"}
		if typed.Preservation.BackupBranch != "" {
			parts = append(parts, "ref: "+typed.Preservation.BackupBranch)
		}
		if len(typed.Preservation.StashedPaths) > 0 {
			parts = append(parts, fmt.Sprintf("%d file(s) stashed", len(typed.Preservation.StashedPaths)))
		}
		m.statusBar.SetFlash(strings.Join(parts, " — "))
	}

	// Now proceed with the actual abort.
	bus := m.gitBus
	return func() tea.Msg {
		if err := bus.SequencerAbort(); err != nil {
			return sequencerAbortFailedMsg{reason: err.Error()}
		}
		return sequencerAbortedMsg{}
	}
}

// handleBranchStashAvailable offers to restore a branch stash after checkout.
func (m *AppModel) handleBranchStashAvailable(typed msg.BranchStashAvailableMsg) tea.Cmd {
	m.statusBar.SetFlash(
		fmt.Sprintf("Branch stash available (%d files) — restoring", typed.Meta.FileCount))
	bus := m.gitBus
	branch := typed.Meta.BranchName
	return func() tea.Msg {
		err := bus.UnstashForBranch(branch)
		return msg.BranchStashRestoredMsg{Err: err}
	}
}

func (m *AppModel) handleModalClosed(result any) tea.Cmd {
	if !m.modalOverlay.Active() {
		m.overlay = overlayNone
	}

	lr, ok := result.(modal.ListModalResult)
	if !ok {
		return nil
	}

	// Route pre-commit pipeline results.
	if m.pendingCommitPhase != 0 {
		return m.routePreCommitModal(lr)
	}

	// Route sequencer operation modals (integration detection, conflict preview).
	if m.pendingSeqOp != nil {
		return m.routeSequencerModal(lr)
	}

	// Route syntax validation modal.
	if m.pendingSyntaxValidation {
		m.pendingSyntaxValidation = false
		if lr.Action == 0 {
			// "Continue Anyway" — re-emit with Proceed=true.
			return func() tea.Msg {
				return conflictview.SyntaxValidationResultMsg{Proceed: true}
			}
		}
		return nil // Cancel — do nothing.
	}

	return nil
}

// routePreCommitModal handles large-file / secret modal confirmations.
func (m *AppModel) routePreCommitModal(lr modal.ListModalResult) tea.Cmd {
	phase := m.pendingCommitPhase
	paths := m.pendingCommitPaths
	message := m.pendingCommitMessage
	m.pendingCommitPaths = nil
	m.pendingCommitMessage = ""
	m.pendingCommitPhase = 0

	if lr.Action != 0 {
		m.statusBar.SetFlash("Commit cancelled")
		return nil
	}

	bus := m.gitBus
	switch phase {
	case 1:
		// Large-file modal confirmed — proceed to secret scan.
		return func() tea.Msg {
			secrets, err := bus.ScanStagedSecrets(paths)
			if err == nil && len(secrets) > 0 {
				return msg.SecretsDetectedMsg{Findings: secrets, Paths: paths, Message: message}
			}
			return msg.PreCommitCleanMsg{Paths: paths, Message: message}
		}
	case 2:
		// Secrets modal confirmed — proceed to commit.
		return func() tea.Msg {
			if err := bus.CommitFiles(paths, message); err != nil {
				return commitFailedMsg{reason: err.Error()}
			}
			return commitSucceededMsg{message: message}
		}
	}
	return nil
}

// routeSequencerModal handles integration detection and conflict preview
// modal confirmations for pending cherry-pick/rebase/merge operations.
func (m *AppModel) routeSequencerModal(lr modal.ListModalResult) tea.Cmd {
	op := m.pendingSeqOp
	m.pendingSeqOp = nil

	if lr.Action != 0 {
		// Last action = cancel.
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(op.op + " cancelled")
		return nil
	}

	return m.executeSequencerOp(op)
}

// executeSequencerOp dispatches the stored sequencer operation.
func (m *AppModel) executeSequencerOp(op *pendingSequencerOp) tea.Cmd {
	bus := m.gitBus
	switch op.op {
	case "cherry-pick":
		return func() tea.Msg {
			status, err := bus.CherryPickSequence(op.hashes, op.target)
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}
	case "rebase":
		return func() tea.Msg {
			status, err := bus.RebaseInteractive(op.target, op.sourceBranch, op.plan)
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}
	case "merge":
		return m.executeMergeBranch(op.delete)
	}
	return nil
}

// chordState tracks which chord prefix is pending.
type chordState int

const (
	chordNone        chordState = iota
	chordSession                // Alt+S pressed, waiting for arrow.
	chordAgent                  // Alt+A pressed, waiting for arrow.
	chordView                   // Alt+V pressed, waiting for arrow.
	chordMultiCursor            // Alt+Shift+D pressed, waiting for up/down/d.
)

// chordArrowDelta maps arrow key strings (including alt-held variants) to
// a cycle direction. The alt+arrow variants handle the common case where the
// user still holds Alt from the prefix key when pressing the arrow.
var chordArrowDelta = map[string]int{
	"left":      -1,
	"right":     1,
	"alt+left":  -1,
	"alt+right": 1,
}

// chordFocusGuard restricts specific chords to a required focused pane.
// Chords not listed here are global (activate regardless of focus).
var chordFocusGuard = map[chordState]component.FocusID{}

// chordDisplay holds the label, key hints, and color for a chord hint overlay.
type chordDisplay struct {
	label string
	keys  string
	color func(*theme.Palette) lipgloss.Color
}

// chordDisplays maps chord states to their display properties.
// Session select uses Primary (blue), Agent select uses Success (green).
var chordDisplays = map[chordState]chordDisplay{
	chordSession:     {"Session select", "←/→ cycle", func(p *theme.Palette) lipgloss.Color { return p.Primary }},
	chordAgent:       {"Agent select", "←/→ cycle", func(p *theme.Palette) lipgloss.Color { return p.Success }},
	chordView:        {"View select", "←/→ cycle", func(p *theme.Palette) lipgloss.Color { return p.Accent }},
	chordMultiCursor: {"Multi-cursor", "↓ add  ↑ remove  d next word  esc exit", func(p *theme.Palette) lipgloss.Color { return p.Warning }},
}

// chordHint returns a styled hint string when a chord is active, or "" when idle.
// Blocked chords (edit mode) render as "Exit edit mode to <action>" instead of
// the normal cycling hint, so chordBlocked hints are only rendered by
// codePanelView (not overlayChordHint on other panels).
func (m *AppModel) chordHint(th *theme.Theme) string {
	if m.chordBlocked {
		return ""
	}
	disp, ok := chordDisplays[m.chord]
	if !ok {
		return ""
	}
	labelStyle := lipgloss.NewStyle().Foreground(disp.color(&th.Palette)).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	return labelStyle.Render(disp.label) + keyStyle.Render("  "+disp.keys+"  any key to exit ")
}

// blockedChordHint returns a styled hint for a chord blocked by the current mode.
// Returns "" when no blocked chord is active.
func (m *AppModel) blockedChordHint(th *theme.Theme) string {
	if !m.chordBlocked {
		return ""
	}
	disp, ok := chordDisplays[m.chord]
	if !ok {
		return ""
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	labelStyle := lipgloss.NewStyle().Foreground(disp.color(&th.Palette)).Bold(true)
	mode := "edit"
	if m.viewMode == ViewGit {
		mode = "git"
	}
	return mutedStyle.Render("Exit "+mode+" mode to ") + labelStyle.Render(disp.label) + mutedStyle.Render("  (press any key to dismiss) ")
}

// handleChord processes two-key chord shortcuts: Alt+S then Left/Right for sessions,
// Alt+A then Left/Right for agents. The chord stays active while arrows are pressed,
// allowing repeated cycling. Any non-arrow key cancels the chord and falls
// through to normal handling. Returns (cmd, true) if consumed.
// chordKeyMap maps chord trigger keys to their chord state.
var chordKeyMap = map[string]chordState{
	"alt+s": chordSession,
	"alt+a": chordAgent,
	"alt+v": chordView,
}

func (m *AppModel) handleChord(key tea.KeyMsg) (tea.Cmd, bool) {
	// Blocked chord: any key dismisses the hint.
	if m.chordBlocked {
		m.chord = chordNone
		m.chordBlocked = false
		return nil, true
	}

	ks := key.String()

	// Multi-cursor chord trigger: Alt+Shift+D (only in edit mode with editor focused).
	if ks == "alt+D" {
		if m.viewMode == ViewEdit && m.isEditorFocused() {
			if m.chord == chordMultiCursor {
				m.chord = chordNone
			} else {
				m.chord = chordMultiCursor
			}
			return nil, true
		}
		return nil, false
	}

	// Active multi-cursor chord: up/down add cursors, d adds at next word, esc/other cancels.
	if m.chord == chordMultiCursor {
		if m.viewMode != ViewEdit || !m.isEditorFocused() {
			m.chord = chordNone
			return nil, false
		}
		return m.handleMultiCursorChord(ks)
	}

	// Chord triggers: toggle on if idle or switching, toggle off if repeated.
	if target, ok := chordKeyMap[ks]; ok {
		// Mode guard: non-view chords are disabled in edit/git mode.
		// chordView is allowed so the user can cycle between modes.
		if m.viewMode != ViewChat && target != chordView {
			m.chord = target
			m.chordBlocked = true
			cmd := tea.Tick(2*time.Second, func(_ time.Time) tea.Msg {
				return msg.ChordBlockedExpireMsg{}
			})
			return cmd, true
		}
		// Focus guard: some chords only activate with a specific pane focused.
		if required, guarded := chordFocusGuard[target]; guarded && m.focus.Current() != required {
			return nil, false
		}
		// Debounce: absorb key-repeat to prevent rapid toggle cycling.
		now := time.Now()
		if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
			return nil, true
		}
		m.lastToggleKey = ks
		m.lastToggleAt = now

		if m.chord == target {
			m.chord = chordNone
		} else {
			m.chord = target
		}
		return nil, true
	}

	if m.chord == chordNone {
		return nil, false
	}

	// Active chord: arrow keys cycle, anything else cancels.
	if delta, ok := chordArrowDelta[ks]; ok {
		return m.dispatchChordCycle(m.chord, delta), true
	}

	m.chord = chordNone
	return nil, true
}

// multiCursorChordKeys maps key strings accepted during the multi-cursor chord
// to their action. The chord stays active after each action, allowing repeated
// presses. Only "up"/"down" (plain or with alt held) and "d"/"alt+d" are valid.
var multiCursorChordKeys = map[string]string{
	"up":       "remove",
	"down":     "below",
	"alt+up":   "remove",
	"alt+down": "below",
	"d":        "word",
	"alt+d":    "word",
}

// handleMultiCursorChord processes keys while the multi-cursor chord is active.
// Returns (cmd, true) if consumed, or cancels the chord and returns (nil, true)
// so the cancelled key is swallowed (user presses a non-chord key to exit).
func (m *AppModel) handleMultiCursorChord(ks string) (tea.Cmd, bool) {
	// Esc cancels chord and clears all secondary cursors.
	if ks == "esc" {
		m.chord = chordNone
		if ed := m.focusedEditor(); ed != nil {
			ed.ClearSecondaryCursors()
		}
		return nil, true
	}
	action, ok := multiCursorChordKeys[ks]
	if !ok {
		// Any non-chord key cancels the chord (but keeps cursors).
		m.chord = chordNone
		return nil, true
	}
	ed := m.focusedEditor()
	switch action {
	case "remove":
		ed.RemoveBottomCursor()
	case "below":
		ed.AddCursorBelow()
	case "word":
		ed.AddCursorAtNextOccurrence()
	}
	return nil, true
}

// dispatchChordCycle routes a completed chord to the target panel.
func (m *AppModel) dispatchChordCycle(chord chordState, delta int) tea.Cmd {
	switch chord {
	case chordSession:
		if delta < 0 {
			return m.sessionPanel.CyclePrev()
		}
		return m.sessionPanel.CycleNext()
	case chordAgent:
		if delta < 0 {
			m.agentPanel.CyclePrev()
		} else {
			m.agentPanel.CycleNext()
		}
		m.syncManualTargetFromAgentSelection()
	case chordView:
		return m.cycleViewSlot(delta)
	}
	return nil
}

// cycleViewSlot dispatches Alt+V cycling to the appropriate ring(s).
// Two active rings: both cycle in lockstep so the panel pair toggles as a unit.
// One active ring: direction is forward/backward within that ring.
func (m *AppModel) cycleViewSlot(delta int) tea.Cmd {
	// Capture pre-cycle focus so syncGitModeForRing can save it
	// for the outgoing view before the ring changes focus.
	preFocus := m.focus.Current()

	bothActive := !m.leftRing.empty() && !m.rightRing.empty()
	if bothActive {
		oldLeft := m.leftRing.current()
		oldRight := m.rightRing.current()
		m.leftRing.cycle(delta)
		m.rightRing.cycle(delta)
		switch preFocus {
		case oldLeft:
			m.focus.SetFocus(m.leftRing.current())
		case oldRight:
			m.focus.SetFocus(m.rightRing.current())
		}
		cmd := m.syncModeForRing(preFocus)
		m.syncViewState()
		return cmd
	}
	if !m.leftRing.empty() {
		return m.cycleRing(&m.leftRing, delta, preFocus)
	}
	if !m.rightRing.empty() {
		return m.cycleRing(&m.rightRing, delta, preFocus)
	}
	return nil
}

// cycleRing advances a single ring by delta, transferring focus if needed.
func (m *AppModel) cycleRing(ring *viewRing, delta int, preFocus component.FocusID) tea.Cmd {
	oldPanel := ring.current()
	ring.cycle(delta)
	newPanel := ring.current()
	if m.focus.Current() == oldPanel {
		m.focus.SetFocus(newPanel)
	}
	cmd := m.syncModeForRing(preFocus)
	m.syncViewState()
	return cmd
}

// ringTargetMode determines the view mode implied by current ring positions.
func (m *AppModel) ringTargetMode() ViewMode {
	if isGitPanel(m.leftRing.current()) || isGitPanel(m.rightRing.current()) {
		return ViewGit
	}
	active := m.leftRing.current()
	if !m.rightRing.empty() {
		active = m.rightRing.current()
	}
	if active == component.FocusCodeViewer {
		return ViewEdit
	}
	return ViewChat
}

// syncModeForRing detects mode transitions (CHAT / EDIT / GIT) after a
// ring cycle and symmetrically saves outgoing focus / restores incoming
// focus. preFocus is the focus captured before the ring advanced.
func (m *AppModel) syncModeForRing(preFocus component.FocusID) tea.Cmd {
	target := m.ringTargetMode()
	if target == m.viewMode {
		return nil
	}
	old := m.viewMode
	m.viewMode = target

	// Save outgoing focus.
	switch old {
	case ViewGit:
		m.savedGitFocus = preFocus
	case ViewEdit:
		m.savedEditFocus = preFocus
	default:
		m.savedChatFocus = preFocus
	}

	// Exit old mode.
	if old == ViewGit {
		m.viewMode = ViewChat
		m.input.SetPlaceholder("Type a message...")
	}

	// Enter new mode and restore incoming focus.
	var cmd tea.Cmd
	switch target {
	case ViewGit:
		if m.viewMode == ViewEdit {
			m.preGitEditMode = true
			m.exitEditMode()
		}
		m.viewMode = ViewGit
		m.resizeGitPanels()
		m.input.SetPlaceholder("git>")
		if m.savedGitFocus != 0 {
			m.focus.SetFocus(m.savedGitFocus)
		}
		if !m.gitDataLoaded {
			m.gitDataLoaded = true
			cmd = tea.Batch(m.gitPanel.LoadData(), m.loadGitBranchesCmd())
		}
	case ViewEdit:
		if old == ViewGit && m.preGitEditMode {
			m.preGitEditMode = false
		}
		cmd = m.enterEditMode()
	default: // CHAT
		if old == ViewGit && m.preGitEditMode {
			m.preGitEditMode = false
		}
		if m.savedChatFocus != 0 {
			m.focus.SetFocus(m.savedChatFocus)
		}
	}

	m.statusBar.SetMode(target.String())
	return cmd
}

// ---------------------------------------------------------------------------
// Chat mode (Alt+C)
// ---------------------------------------------------------------------------

// switchToChatMode exits git or edit mode to return to chat mode.
// No-op if already in chat mode.
func (m *AppModel) switchToChatMode() tea.Cmd {
	switch m.viewMode {
	case ViewGit:
		return m.exitGitMode()
	case ViewEdit:
		m.exitEditMode()
		return nil
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Edit mode (Alt+E)
// ---------------------------------------------------------------------------

// toggleEditMode switches between inline editing and read-only code viewing.
func (m *AppModel) toggleEditMode() tea.Cmd {
	switch m.viewMode {
	case ViewGit:
		m.preGitEditMode = false
		m.exitGitMode()
		return m.enterEditMode()
	case ViewEdit:
		m.exitEditMode()
		return nil
	default:
		return m.enterEditMode()
	}
}

// enterEditMode activates inline vim editing in the code panel slot.
// Returns a Cmd that sends didOpen so the LSP client tracks the document
// for subsequent didChange notifications. Idempotent if already tracked.
func (m *AppModel) enterEditMode() tea.Cmd {
	// Dismiss non-view chords — chordView is allowed in edit mode.
	if m.chord != chordView {
		m.chord = chordNone
		m.chordBlocked = false
	}

	// Save ring state and chat-mode focus.
	m.savedLeftIdx = m.leftRing.index
	m.savedRightIdx = m.rightRing.index
	m.savedChatFocus = m.focus.Current()

	// Ensure the code panel is visible.
	switch m.layout.Mode() {
	case layout.ThreeColumn:
		m.leftRing.setTo(component.FocusFileTree)
	case layout.TwoColumn:
		m.leftRing.setTo(component.FocusFileTree)
		m.rightRing.setTo(component.FocusCodeViewer)
	case layout.SingleColumn:
		m.leftRing.setTo(component.FocusCodeViewer)
	}

	// Restore tab order from a previous edit session, or initialise from
	// the code panel's current file.
	var lspCmd tea.Cmd
	if len(m.focusedTabOrder()) > 0 {
		// Re-entering edit mode — restore the last-active tab.
		idx := 0
		for i, p := range m.focusedTabOrder() {
			if p == m.savedActiveTab {
				idx = i
				break
			}
		}
		fp := m.focusedTabOrder()[idx]
		var content string
		if m.restoreFromCache(fp) {
			content = m.focusedEditor().Content()
		} else {
			data, _ := os.ReadFile(fp)
			content = string(data)
			m.focusedEditor().OpenFile(fp, content, detectEditorLanguage(fp))
		}
		m.fileTree.SetActiveFile(fp)
		m.fileTree.RevealPath(fp)
		lspCmd = m.lspReopenCmd(fp, detectEditorLanguage(fp), content)
	} else if fp := m.codePanel.FilePath(); fp != "" {
		m.setFocusedTabOrder([]string{fp})
		var content string
		if m.restoreFromCache(fp) {
			content = m.focusedEditor().Content()
		} else {
			content = m.codePanel.Content()
			m.focusedEditor().OpenFile(fp, content, m.codePanel.Language())
		}
		m.fileTree.SetActiveFile(fp)
		m.fileTree.RevealPath(fp)
		lspCmd = m.lspReopenCmd(fp, detectEditorLanguage(fp), content)
	} else {
		m.focusedEditor().ClearFile()
	}

	m.viewMode = ViewEdit
	m.viewMode = ViewEdit

	// Size the editor to match the code panel slot. resizeInlineEditor
	// handles all states (split preview, full preview, editor-only).
	m.resizeInlineEditor()
	m.fileTree.SetEditMode(true)
	m.statusBar.SetMode("EDIT")
	m.input.SetPlaceholder(":")

	// Rebuild tab order for edit mode first, then restore focus.
	// savedEditFocus preserves the last focused pane's dynamic ID
	// across chat→edit toggles. Falls back to focused editor pane.
	m.syncViewState()
	target := m.savedEditFocus
	if !pane.IsPaneFocus(target) || !m.paneTree.Contains(pane.PaneIDFromFocus(target)) {
		target = pane.PaneFocusID(m.focusedPane)
	}
	m.focus.SetFocus(target)
	m.syncFocusState()
	m.statusBar.SetFlash("Edit mode")
	return lspCmd
}

// exitEditMode deactivates inline editing and syncs content back to the
// code viewer.
func (m *AppModel) exitEditMode() {
	// NOTE: We do NOT call lspDidCloseAsync here. The close is handled
	// atomically by lspReopenCmd when the user re-enters edit mode, which
	// avoids a race condition between async didClose and async didOpen.

	// Capture content for the code panel before detaching the editor.
	content := m.focusedEditor().Content()
	fp := m.focusedEditor().FilePath()
	lang := m.focusedEditor().Language()

	// Save current editor state so re-entering edit mode restores it.
	m.savedActiveTab = fp
	m.savedEditFocus = m.focus.Current()
	m.detachCurrentEditor()

	m.codePanel.SetContent(content, fp, lang)

	// Restore ring state.
	m.leftRing.index = m.savedLeftIdx
	m.rightRing.index = m.savedRightIdx

	m.viewMode = ViewChat
	m.viewMode = ViewChat
	m.fileTree.SetEditMode(false)
	m.editCmdInput = false
	// Preserve chordView — it's allowed across mode transitions.
	if m.chord != chordView {
		m.chord = chordNone
		m.chordBlocked = false
	}
	// tabOrder is intentionally preserved so re-entering edit mode
	// restores all previously open tabs.
	m.statusBar.SetMode("CHAT")
	m.input.SetPlaceholder("Type a message...")

	// Rebuild tab order for chat mode first, then restore focus.
	// This ensures savedChatFocus is evaluated against the fresh tab order.
	m.syncViewState()
	m.focus.SetFocus(m.savedChatFocus)
	m.syncFocusState()
	m.statusBar.SetFlash("View mode")
}

// ---------------------------------------------------------------------------
// Git mode (Alt+G)
// ---------------------------------------------------------------------------

// gitQuickStatusMsg carries lightweight working-tree + stash state so that
// UI elements (commit button, unstash badge) can update without waiting for
// the full branch enumeration in loadGitBranchesCmd.
type gitQuickStatusMsg struct {
	dirty          bool
	conflicts      bool
	hasIndexStaged bool
	hasStash       bool
}

// gitBranchesLoadedMsg carries branch data loaded asynchronously for the
// commit tree panel's branch view.
type gitBranchesLoadedMsg struct {
	branches       []committree.BranchNode
	defaultBranch  string
	dirty          bool // working tree has uncommitted changes
	conflicts      bool // working tree has merge conflicts
	hasIndexStaged bool // git index has staged changes (via git add)
	hasStash       bool // stash list is non-empty
}

// gitBranchFullyLoadedMsg carries commit nodes with their diff stats,
// loaded atomically so the UI can transition from spinner to data in one step.
type gitBranchFullyLoadedMsg struct {
	branch  string
	nodes   []committree.TreeNode
	stats   map[string][2]int
	hasMore bool
}

// gitMoreCommitsLoadedMsg carries an additional page of commits appended
// during infinite scroll.
type gitMoreCommitsLoadedMsg struct {
	branch  string
	nodes   []committree.TreeNode
	stats   map[string][2]int
	hasMore bool
}

// gitBranchDAGLoadedMsg carries a full DAG of branch-unique commits with
// graph layout data, loaded atomically.
type gitBranchDAGLoadedMsg struct {
	branch       string
	nodes        []committree.TreeNode
	stats        map[string][2]int
	graphRows    []committree.GraphRow
	maxGraphLane int
}

type branchDeletedMsg struct{ name string }
type branchStashesLoadedMsg struct {
	stashes map[string][]git.BranchStashMeta
}
type branchDeleteFailedMsg struct{ reason string }
type branchCreatedMsg struct{ name string }
type branchCreateFailedMsg struct{ reason string }
type commitSucceededMsg struct{ message string }
type commitFailedMsg struct{ reason string }
type stashSucceededMsg struct{ count int }
type stashFailedMsg struct{ reason string }
type unstashSucceededMsg struct{}
type unstashFailedMsg struct{ reason string }
type pullSucceededMsg struct{ name string }
type pullFailedMsg struct{ reason string }
type pushSucceededMsg struct{ name string }
type pushFailedMsg struct{ reason string }
type resetSucceededMsg struct{ mode string }
type resetFailedMsg struct{ reason string }
type revertSucceededMsg struct{ hash string }
type revertFailedMsg struct{ reason string }
type commitCheckoutSucceededMsg struct{ hash string }
type commitCheckoutFailedMsg struct{ reason string }

type sequencerResultMsg struct{ status *git.SequencerStatus }
type sequencerFailedMsg struct{ reason string }
type sequencerAbortedMsg struct{}
type sequencerAbortFailedMsg struct{ reason string }
type conflictResolveWrittenMsg struct{ path string }
type conflictResolveFailedMsg struct{ path, reason string }

// pendingSequencerOp stores deferred sequencer parameters while the user
// reviews a conflict preview or integration detection modal.
type pendingSequencerOp struct {
	op           string // "cherry-pick", "rebase", "merge"
	phase        int    // 1=integration modal, 2=conflict preview modal
	hashes       []string
	target       string
	sourceBranch string // rebase: branch being rebased (may differ from HEAD)
	plan         []git.RebasePlanEntry
	delete       bool // merge: delete source branch after
}

// loadGitBranchesCmd returns a tea.Cmd that loads all local branches
// via go-git and converts them to BranchNode data for the commit tree panel.
func (m *AppModel) loadGitBranchesCmd() tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		branches, err := bus.ListBranches()
		if err != nil {
			return gitBranchesLoadedMsg{}
		}
		defaultBranch := bus.DefaultBranch()

		// Working tree status.
		statuses, hasIndexStaged, _ := bus.UncommittedFileStatuses()
		dirty := len(statuses) > 0
		var conflicts bool
		for _, s := range statuses {
			if s == "!" {
				conflicts = true
				break
			}
		}

		const commitLimit = 1000
		branchNames := make([]string, len(branches))
		for i, b := range branches {
			branchNames[i] = b.Name
		}
		commitCounts := bus.CountBranchOnlyCommitsBatch(branchNames, defaultBranch, commitLimit)
		inferredParents := bus.InferBranchParents(branches, defaultBranch)

		nodes := make([]committree.BranchNode, len(branches))
		for i, b := range branches {
			nodes[i] = committree.BranchNode{
				Name:        b.Name,
				Hash:        b.Hash,
				ShortHash:   b.ShortHash,
				Subject:     b.Subject,
				AuthorTime:  b.AuthorTime,
				CreatedTime: b.CreatedTime,
				IsHead:      b.IsHead,
				Parent:      inferredParents[b.Name],
			}
			if bc, ok := commitCounts[b.Name]; ok {
				nodes[i].CommitCount = bc.Count
				nodes[i].CommitCountCapped = bc.Capped
				nodes[i].BehindCount = bc.Behind
				nodes[i].BehindCountCapped = bc.BehindCapped
			}
		}

		// Detect detached HEAD: no branch has IsHead=true.
		if detached := buildDetachedNode(bus, nodes); detached != nil {
			nodes = append([]committree.BranchNode{*detached}, nodes...)
		}

		return gitBranchesLoadedMsg{
			branches:       nodes,
			defaultBranch:  defaultBranch,
			dirty:          dirty,
			conflicts:      conflicts,
			hasIndexStaged: hasIndexStaged,
			hasStash:       bus.HasStash(),
		}
	}
}

// buildDetachedNode returns a synthetic BranchNode if HEAD is detached
// (no branch has IsHead=true). Returns nil if HEAD is on a branch.
func buildDetachedNode(bus *git.GitBus, nodes []committree.BranchNode) *committree.BranchNode {
	for _, n := range nodes {
		if n.IsHead {
			return nil
		}
	}
	hash, err := bus.GetHead()
	if err != nil {
		return nil
	}
	short := hash[:min(len(hash), 7)]
	node := committree.BranchNode{
		Name:       "(detached) " + short,
		Hash:       hash,
		ShortHash:  short,
		IsHead:     true,
		IsDetached: true,
	}
	if ci, err := bus.GetCommit(hash); err == nil {
		node.Subject = ci.Subject
		node.AuthorTime = ci.AuthorTime
	}
	return &node
}

// quickGitStatusCmd returns a tea.Cmd that checks only working-tree dirty
// state, index staging, and stash presence. Runs in O(index-scan) time
// without the expensive branch enumeration and commit counting.
func (m *AppModel) quickGitStatusCmd() tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		statuses, hasIndexStaged, _ := bus.UncommittedFileStatuses()
		dirty := len(statuses) > 0
		var conflicts bool
		for _, s := range statuses {
			if s == "!" {
				conflicts = true
				break
			}
		}
		return gitQuickStatusMsg{
			dirty:          dirty,
			conflicts:      conflicts,
			hasIndexStaged: hasIndexStaged,
			hasStash:       bus.HasStash(),
		}
	}
}

// detectSequencerStateCmd checks for an active rebase/merge/cherry-pick
// by reading .git/ filesystem markers.
func (m *AppModel) detectSequencerStateCmd() tea.Cmd {
	bus := m.gitBus
	if bus == nil {
		return nil
	}
	return func() tea.Msg {
		state := bus.DetectSequencerFileState()
		return msg.SequencerFileStateMsg{State: state}
	}
}

// computeDivergenceBatchCmd asynchronously computes ahead/behind counts
// for all branches relative to their configured upstream tracking refs.
func (m *AppModel) computeDivergenceBatchCmd() tea.Cmd {
	bus := m.gitBus
	if bus == nil {
		return nil
	}
	return func() tea.Msg {
		branches, err := bus.ListBranches()
		if err != nil {
			return msg.DivergenceLoadedMsg{}
		}
		names := make([]string, len(branches))
		for i, b := range branches {
			names[i] = b.Name
		}
		info, err := bus.ComputeDivergenceBatch(names)
		if err != nil {
			return msg.DivergenceLoadedMsg{}
		}
		return msg.DivergenceLoadedMsg{Info: info}
	}
}

// loadBranchStashesCmd asynchronously lists branch stashes for stash badges.
func (m *AppModel) loadBranchStashesCmd() tea.Cmd {
	bus := m.gitBus
	if bus == nil {
		return nil
	}
	return func() tea.Msg {
		stashes, err := bus.ListBranchStashes()
		if err != nil {
			return branchStashesLoadedMsg{}
		}
		return branchStashesLoadedMsg{stashes: stashes}
	}
}

// conflictPreviewMergeCmd previews merge conflicts before executing.
func (m *AppModel) conflictPreviewMergeCmd(source, target string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		result, err := bus.PreviewMerge(source, target)
		if err != nil || result.Clean {
			return msg.ConflictPreviewMsg{Result: result, Op: "merge"}
		}
		return msg.ConflictPreviewMsg{Result: result, Op: "merge"}
	}
}

// conflictPreviewRebaseCmd previews rebase conflicts before executing.
func (m *AppModel) conflictPreviewRebaseCmd(onto, sourceBranch string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		result, err := bus.PreviewRebase(onto, sourceBranch)
		if err != nil || result == nil {
			return msg.ConflictPreviewMsg{Result: result, Op: "rebase"}
		}
		return msg.ConflictPreviewMsg{Result: result, Op: "rebase"}
	}
}

// conflictPreviewCherryPickCmd previews cherry-pick conflicts before executing.
func (m *AppModel) conflictPreviewCherryPickCmd(hashes []string, params any) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		result, err := bus.PreviewCherryPick(hashes)
		if err != nil || result == nil {
			return msg.ConflictPreviewMsg{Result: result, Op: "cherry-pick", Params: params}
		}
		return msg.ConflictPreviewMsg{Result: result, Op: "cherry-pick", Params: params}
	}
}

// detectIntegrationCmd checks for already-integrated cherry-pick commits.
func (m *AppModel) detectIntegrationCmd(hashes []string, targetBranch string, params any) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		results, err := bus.DetectIntegratedCommits(hashes, targetBranch)
		if err != nil {
			return msg.IntegrationDetectedMsg{Params: params}
		}
		return msg.IntegrationDetectedMsg{Results: results, Params: params}
	}
}

// preserveAbortCmd creates a backup ref and stash before sequencer abort.
func (m *AppModel) preserveAbortCmd(opName string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		pres, err := bus.PreserveBeforeAbort(opName)
		return msg.AbortPreservedMsg{Preservation: pres, Err: err}
	}
}

// fetchBaseContentCmd reads ancestor blob content for the three-way diff view.
func (m *AppModel) fetchBaseContentCmd(path, baseHash string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		content, err := bus.ReadBlobContent(baseHash)
		return conflictview.BaseContentResponseMsg{
			Path: path, Content: content, Err: err,
		}
	}
}

// fetchStepPreviewCmd computes the commit diff for the step preview overlay.
func (m *AppModel) fetchStepPreviewCmd(hash string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		return m.buildStepPreview(bus, hash)
	}
}

// buildStepPreview resolves a commit's parent and diffs against it.
func (m *AppModel) buildStepPreview(bus *git.GitBus, hash string) conflictview.StepPreviewResponseMsg {
	ci, err := bus.GetCommit(hash)
	if err != nil || len(ci.ParentHashes) == 0 {
		return conflictview.StepPreviewResponseMsg{}
	}
	diffs, err := bus.GetDiff(ci.ParentHashes[0], ci.Hash)
	if err != nil {
		return conflictview.StepPreviewResponseMsg{}
	}
	conflictPaths := m.conflictPathSet()
	fileDiffs := make([]conflictview.FileDiffSummary, len(diffs))
	for i, fd := range diffs {
		fileDiffs[i] = conflictview.FileDiffSummary{
			Path: fd.Path, Additions: fd.Additions, Deletions: fd.Deletions,
		}
	}
	return conflictview.StepPreviewResponseMsg{
		Preview: &conflictview.StepPreview{
			Hash: ci.ShortHash, Subject: ci.Message,
			FileDiffs: fileDiffs, ConflictPaths: conflictPaths,
		},
	}
}

// conflictPathSet returns the set of paths in the current conflict view.
func (m *AppModel) conflictPathSet() map[string]bool {
	if m.conflictView == nil {
		return nil
	}
	entries := m.conflictView.Entries()
	paths := make(map[string]bool, len(entries))
	for _, e := range entries {
		paths[e.Path] = true
	}
	return paths
}

// sequencerUndoStepCmd issues the SequencerUndoStep command via the git bus.
func (m *AppModel) sequencerUndoStepCmd() tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		status, err := bus.SequencerUndoStep()
		if err != nil {
			return sequencerFailedMsg{reason: err.Error()}
		}
		return sequencerResultMsg{status: status}
	}
}

// preCommitSizeThreshold is the file size (bytes) above which a staged file
// is flagged in the pre-commit pipeline. Derived from: 5 MiB is the standard
// GitHub push warning threshold and a common large-file guardrail.
const preCommitSizeThreshold = 5 * 1024 * 1024

// preCommitCheckCmd runs the large-file and secret scan pipeline in sequence.
// If large files are found, returns LargeFilesDetectedMsg (secrets deferred
// to after user confirmation). Otherwise proceeds to secret scan.
func (m *AppModel) preCommitCheckCmd(paths []string, message string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		large, err := bus.ScanStagedLargeFiles(paths, preCommitSizeThreshold)
		if err == nil && len(large) > 0 {
			return msg.LargeFilesDetectedMsg{Files: large, Paths: paths, Message: message}
		}
		secrets, err := bus.ScanStagedSecrets(paths)
		if err == nil && len(secrets) > 0 {
			return msg.SecretsDetectedMsg{Findings: secrets, Paths: paths, Message: message}
		}
		return msg.PreCommitCleanMsg{Paths: paths, Message: message}
	}
}

// formatFileSize renders a byte count as a human-readable string.
func formatFileSize(size int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case size >= gib:
		return fmt.Sprintf("%.1f GB", float64(size)/float64(gib))
	case size >= mib:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(mib))
	case size >= kib:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(kib))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// loadBranchCommitsAndStatsCmd loads the first page of commits with diff
// stats for a branch. Uses flat first-parent pagination for fast initial
// display. Subsequent pages are loaded on demand via loadMoreCommitsCmd.
func (m *AppModel) loadBranchCommitsAndStatsCmd(branchName, defaultBranch string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		commits, hasMore, err := bus.ListBranchOnlyCommits(branchName, defaultBranch, "", commitPageSize)
		if err != nil {
			return gitBranchFullyLoadedMsg{branch: branchName}
		}
		nodes := commitsToTreeNodes(commits)
		if len(nodes) > 0 {
			nodes[0].Branch = branchName
		}
		stats := loadStatsForNodes(bus, nodes)
		return gitBranchFullyLoadedMsg{
			branch:  branchName,
			nodes:   nodes,
			stats:   stats,
			hasMore: hasMore,
		}
	}
}

// loadBranchDAGCmd loads the full DAG of branch-unique commits with graph
// layout. Falls back to flat first-parent mode if the DAG exceeds the limit.
func (m *AppModel) loadBranchDAGCmd(branchName, defaultBranch string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		commits, err := bus.ListBranchDAGCommits(branchName, defaultBranch, git.DagCommitLimit)
		if err != nil || len(commits) == 0 {
			// Fall back to flat first-parent pagination.
			return m.loadBranchCommitsAndStatsFallback(bus, branchName, defaultBranch)
		}
		nodes := commitsToTreeNodes(commits)
		if len(nodes) > 0 {
			nodes[0].Branch = branchName
		}
		graphRows, maxLane := committree.AssignLanes(nodes)
		stats := loadStatsForNodes(bus, nodes)
		return gitBranchDAGLoadedMsg{
			branch:       branchName,
			nodes:        nodes,
			stats:        stats,
			graphRows:    graphRows,
			maxGraphLane: maxLane,
		}
	}
}

// loadBranchCommitsAndStatsFallback is the flat-mode fallback used when DAG
// loading fails or exceeds limits. Called from within the DAG cmd goroutine.
func (m *AppModel) loadBranchCommitsAndStatsFallback(bus *git.GitBus, branchName, defaultBranch string) tea.Msg {
	commits, hasMore, err := bus.ListBranchOnlyCommits(branchName, defaultBranch, "", commitPageSize)
	if err != nil {
		return gitBranchFullyLoadedMsg{branch: branchName}
	}
	nodes := commitsToTreeNodes(commits)
	if len(nodes) > 0 {
		nodes[0].Branch = branchName
	}
	stats := loadStatsForNodes(bus, nodes)
	return gitBranchFullyLoadedMsg{
		branch:  branchName,
		nodes:   nodes,
		stats:   stats,
		hasMore: hasMore,
	}
}

// loadMoreCommitsCmd loads the next page of commits for infinite scroll,
// starting after the last loaded commit hash.
func (m *AppModel) loadMoreCommitsCmd() tea.Cmd {
	if m.commitTree == nil {
		return nil
	}
	branch := m.commitTree.ActiveBranch()
	lastHash := m.commitTree.LastHash()
	defaultBranch := m.commitTree.GetDefaultBranch()
	bus := m.gitBus
	return func() tea.Msg {
		commits, hasMore, err := bus.ListBranchOnlyCommits(branch, defaultBranch, lastHash, commitPageSize)
		if err != nil || len(commits) == 0 {
			return gitMoreCommitsLoadedMsg{branch: branch}
		}
		nodes := commitsToTreeNodes(commits)
		stats := loadStatsForNodes(bus, nodes)
		return gitMoreCommitsLoadedMsg{
			branch:  branch,
			nodes:   nodes,
			stats:   stats,
			hasMore: hasMore,
		}
	}
}

// commitsToTreeNodes converts git TreeCommits to UI TreeNodes.
func commitsToTreeNodes(commits []git.TreeCommit) []committree.TreeNode {
	nodes := make([]committree.TreeNode, len(commits))
	for i, c := range commits {
		nodes[i] = committree.TreeNode{
			Hash:       c.Hash,
			ShortHash:  c.ShortHash,
			Subject:    c.Subject,
			Branch:     c.Branch,
			Author:     c.Author,
			AuthorTime: c.AuthorTime,
			Parents:    c.ParentHashes,
			IsMerge:    c.IsMerge,
		}
	}
	return nodes
}

// loadStatsForNodes fetches diff stats for all node hashes in a single batch
// under one read lock, leveraging the ristretto cache for repeated lookups.
func loadStatsForNodes(bus *git.GitBus, nodes []committree.TreeNode) map[string][2]int {
	hashes := make([]string, len(nodes))
	for i, n := range nodes {
		hashes[i] = n.Hash
	}
	summaries := bus.GetCommitDiffSummaries(hashes)
	stats := make(map[string][2]int, len(summaries))
	for h, ds := range summaries {
		stats[h] = [2]int{ds.Additions, ds.Deletions}
	}
	return stats
}

// ---------------------------------------------------------------------------
// Diff view helpers
// ---------------------------------------------------------------------------

// Compare mode aliases for use in app.go (avoid repeating package prefix).
const (
	CompareModeChain    = diffview.CompareModeChain
	CompareModeAllFirst = diffview.CompareModeAllFirst
	CompareModePairs    = diffview.CompareModePairs
)

// fetchDiffDataCmd launches an async diff fetch for the given hashes and mode.
// Diff pairs are computed concurrently via a WaitGroup.
func (m *AppModel) fetchDiffDataCmd(hashes []string, mode diffview.CompareMode) tea.Cmd {
	bus := m.gitBus
	if bus == nil || len(hashes) < 2 {
		return nil
	}
	// Build hash→label map for branch name overrides (nil when unused).
	var hashToLabel map[string]string
	if len(m.diffLabels) == len(hashes) {
		hashToLabel = make(map[string]string, len(hashes))
		for i, h := range hashes {
			hashToLabel[h] = m.diffLabels[i]
		}
	}
	return func() tea.Msg {
		fromTo := buildDiffPairs(hashes, mode)
		pairs := make([]msg.DiffViewPair, len(fromTo))
		var wg sync.WaitGroup
		wg.Add(len(fromTo))
		for i, ft := range fromTo {
			go func(idx int, pair [2]string) {
				defer wg.Done()
				pairs[idx] = fetchOneDiffPair(bus, pair)
			}(i, ft)
		}
		wg.Wait()
		// Override short labels with branch names when available.
		if hashToLabel != nil {
			for i := range pairs {
				if lbl, ok := hashToLabel[pairs[i].FromHash]; ok {
					pairs[i].FromShort = lbl
				}
				if lbl, ok := hashToLabel[pairs[i].ToHash]; ok {
					pairs[i].ToShort = lbl
				}
			}
		}
		return msg.DiffViewDataMsg{Pairs: pairs, Mode: int(mode)}
	}
}

// fetchOneDiffPair fetches diff data for a single (from, to) hash pair.
func fetchOneDiffPair(bus *git.GitBus, ft [2]string) msg.DiffViewPair {
	files, _ := bus.GetDiff(ft[0], ft[1])
	totalAdd, totalDel := 0, 0
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}
	fromInfo, _ := bus.GetCommit(ft[0])
	toInfo, _ := bus.GetCommit(ft[1])
	fromShort, toShort := shortHash(ft[0]), shortHash(ft[1])
	if fromInfo != nil {
		fromShort = fromInfo.ShortHash
	}
	if toInfo != nil {
		toShort = toInfo.ShortHash
	}
	return msg.DiffViewPair{
		FromHash:  ft[0],
		ToHash:    ft[1],
		FromShort: fromShort,
		ToShort:   toShort,
		Files:     files,
		TotalAdd:  totalAdd,
		TotalDel:  totalDel,
	}
}

// buildDiffPairs produces (from, to) hash pairs based on compare mode.
func buildDiffPairs(hashes []string, mode diffview.CompareMode) [][2]string {
	if len(hashes) < 2 {
		return nil
	}
	switch mode {
	case CompareModeAllFirst:
		pairs := make([][2]string, len(hashes)-1)
		for i := 1; i < len(hashes); i++ {
			pairs[i-1] = [2]string{hashes[0], hashes[i]}
		}
		return pairs
	case CompareModePairs:
		pairs := make([][2]string, 0, len(hashes)/2)
		for i := 0; i+1 < len(hashes); i += 2 {
			pairs = append(pairs, [2]string{hashes[i], hashes[i+1]})
		}
		return pairs
	default: // Chain
		pairs := make([][2]string, len(hashes)-1)
		for i := 0; i+1 < len(hashes); i++ {
			pairs[i] = [2]string{hashes[i], hashes[i+1]}
		}
		return pairs
	}
}

// shortHash returns the first 7 characters of a hash.
func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// enterDiffView creates or updates the diff view model.
// When an established (non-loading) diff view already exists, pairs and
// mode are reloaded in place to preserve open tabs and the active tab.
func (m *AppModel) enterDiffView(pairs []diffview.DiffPair, mode diffview.CompareMode) {
	// Exit any active overlay first.
	if m.conflictViewActive {
		m.exitConflictView()
	}
	if m.mergeDiffViewActive {
		m.exitMergeDiffView()
	}
	if m.diffViewActive && m.diffView != nil && len(m.diffView.OpenTabs()) > 0 {
		m.diffView.ReloadPairs(pairs, mode)
		m.syncViewState()
		m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
		m.syncFocusState()
		m.viewDirty = true
		return
	}
	if m.diffView != nil {
		m.diffView.Close()
	}
	m.diffView = diffview.New(pairs, mode, m.config.Theme(), m.nerdFontsDetected)
	m.diffViewActive = true
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.diffView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
	lw, lh := m.layout.GetPanelSize(component.FocusFileTree)
	m.diffView.SetFileListSize(max(lw-panelBorderSize, 1), max(lh-panelBorderSize, 1))
	m.diffView.SetFocused(true)
	// syncViewState rebuilds rings, tab order, and calls syncFocusState.
	// Focus must be set AFTER so that SetTabOrder (which includes the diff
	// pane ID via appendCodePanelFocus) preserves the diff pane focus.
	m.syncViewState()
	m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
	m.syncFocusState()
	m.viewDirty = true
}

// exitDiffView clears the diff view and restores commit tree focus.
func (m *AppModel) exitDiffView() {
	if m.diffView != nil {
		m.diffView.Close()
	}
	m.diffViewActive = false
	m.diffView = nil
	m.focus.SetFocus(component.FocusCommitTree)
	m.syncViewState()
	m.viewDirty = true
}

// setDiffLoading sets or clears the loading state on the active diff view.
// When no diff view exists yet, activates the diff view in loading mode.
func (m *AppModel) setDiffLoading(loading bool) {
	if loading && m.diffView == nil {
		// Create a placeholder diff view in loading state so the spinner
		// is visible while the first fetch runs.
		m.diffView = diffview.New(nil, diffview.CompareModeChain,
			m.config.Theme(), m.nerdFontsDetected)
		m.diffViewActive = true
		w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
		m.diffView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
		m.syncViewState()
	}
	if m.diffView != nil {
		m.diffView.SetLoading(loading)
		m.viewDirty = true
	}
}

// ---------------------------------------------------------------------------
// Merge diff view
// ---------------------------------------------------------------------------

// fetchMergeDiffDataCmd fetches diff data for a merge selection.
func (m *AppModel) fetchMergeDiffDataCmd(hashes, labels []string) tea.Cmd {
	bus := m.gitBus
	if bus == nil || len(hashes) < 2 {
		return nil
	}
	hashToLabel := make(map[string]string, len(hashes))
	for i, h := range hashes {
		if i < len(labels) {
			hashToLabel[h] = labels[i]
		}
	}
	return func() tea.Msg {
		fromTo := buildDiffPairs(hashes, CompareModeChain)
		pairs := make([]msg.DiffViewPair, len(fromTo))
		var wg sync.WaitGroup
		wg.Add(len(fromTo))
		for i, ft := range fromTo {
			go func(idx int, pair [2]string) {
				defer wg.Done()
				pairs[idx] = fetchOneDiffPair(bus, pair)
			}(i, ft)
		}
		wg.Wait()
		for i := range pairs {
			if lbl, ok := hashToLabel[pairs[i].FromHash]; ok {
				pairs[i].FromShort = lbl
			}
			if lbl, ok := hashToLabel[pairs[i].ToHash]; ok {
				pairs[i].ToShort = lbl
			}
		}
		return msg.MergeDiffViewDataMsg{Pairs: pairs}
	}
}

// enterMergeDiffView creates the merge diff view model.
func (m *AppModel) enterMergeDiffView(pairs []diffview.DiffPair) {
	// Exit any active diff view first.
	if m.diffViewActive {
		m.exitDiffView()
	}
	if m.mergeDiffView != nil {
		m.mergeDiffView.Close()
	}
	m.mergeDiffView = mergediff.New(pairs, m.config.Theme(), m.nerdFontsDetected)
	m.mergeDiffViewActive = true
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.mergeDiffView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
	lw, lh := m.layout.GetPanelSize(component.FocusFileTree)
	m.mergeDiffView.SetFileListSize(max(lw-panelBorderSize, 1), max(lh-panelBorderSize, 1))
	m.mergeDiffView.SetFocused(true)
	m.syncViewState()
	m.focus.SetFocus(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
	m.syncFocusState()
	m.viewDirty = true
}

// exitMergeDiffView clears the merge diff view and restores commit tree focus.
func (m *AppModel) exitMergeDiffView() {
	if m.mergeDiffView != nil {
		m.mergeDiffView.Close()
	}
	m.mergeDiffViewActive = false
	m.mergeDiffView = nil
	m.focus.SetFocus(component.FocusCommitTree)
	m.syncViewState()
	m.viewDirty = true
}

// enterConflictView creates and activates the conflict resolution view.
func (m *AppModel) enterConflictView(data conflictview.ConflictData) {
	// Exit any active overlays first.
	if m.diffViewActive {
		m.exitDiffView()
	}
	if m.mergeDiffViewActive {
		m.exitMergeDiffView()
	}
	if m.conflictView != nil {
		m.conflictView.Close()
	}
	m.conflictView = conflictview.New(data, m.config.Theme(), m.nerdFontsDetected)
	m.conflictViewActive = true
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.conflictView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
	lw, lh := m.layout.GetPanelSize(component.FocusFileTree)
	m.conflictView.SetFileListSize(max(lw-panelBorderSize, 1), max(lh-panelBorderSize, 1))
	m.conflictView.SetFocused(true)
	m.focus.SetFocus(component.FocusConflictView)
	m.syncViewState()
	m.syncFocusState()
	m.viewDirty = true
}

// exitConflictView clears the conflict view and restores commit tree focus.
func (m *AppModel) exitConflictView() {
	if m.conflictView != nil {
		m.conflictView.Close()
	}
	m.conflictViewActive = false
	m.conflictView = nil
	m.focus.SetFocus(component.FocusCommitTree)
	m.syncViewState()
	m.viewDirty = true
}

// executeMergeBranch performs a git merge using the sequencer so that
// conflicts can be resolved interactively in the conflict view.
// mergeLabels[0] is the source branch, mergeLabels[1] is the target branch.
func (m *AppModel) executeMergeBranch(deleteSource bool) tea.Cmd {
	bus := m.gitBus
	if bus == nil || len(m.mergeLabels) < 2 {
		m.statusBar.SetFlash("Merge requires two branches")
		return nil
	}
	source := m.mergeLabels[0]
	target := m.mergeLabels[1]
	m.mergeDeleteSource = deleteSource
	if m.mergeDiffView != nil {
		m.mergeDiffView.SetMerging("Merging — " + source + " to " + target)
	}

	return func() tea.Msg {
		status, err := bus.MergeSequence(source, target)
		if err != nil {
			return mergeBranchFailedMsg{reason: err.Error()}
		}
		// Conflicts — route through sequencer pipeline for conflict view.
		if status != nil && status.State == git.SeqConflict {
			return sequencerResultMsg{status: status}
		}
		// Clean merge — optionally delete source branch.
		if deleteSource {
			if err := bus.DeleteBranch(source); err != nil {
				return mergeBranchDoneMsg{source: source, target: target, deleteErr: err.Error()}
			}
		}
		return mergeBranchDoneMsg{source: source, target: target, deleted: deleteSource}
	}
}

// finishMergeIfPending handles deferred merge completion after sequencer
// finishes (e.g. after conflict resolution). Deletes source branch if
// requested and shows the appropriate flash message.
func (m *AppModel) finishMergeIfPending() {
	if len(m.mergeLabels) < 2 {
		m.statusBar.SetFlash("Sequencer completed")
		return
	}
	source, target := m.mergeLabels[0], m.mergeLabels[1]
	if m.mergeDeleteSource {
		if err := m.gitBus.DeleteBranch(source); err != nil {
			m.statusBar.SetFlash("Merged " + source + " → " + target + " (delete failed: " + err.Error() + ")")
		} else {
			m.statusBar.SetFlash("Merged " + source + " → " + target + " (deleted " + source + ")")
		}
	} else {
		m.statusBar.SetFlash("Merged " + source + " → " + target)
	}
	m.mergeDeleteSource = false
}

// mergeBranchDoneMsg signals a successful merge.
type mergeBranchDoneMsg struct {
	source    string
	target    string
	deleted   bool
	deleteErr string // Non-empty if merge succeeded but delete failed.
}

// mergeBranchFailedMsg signals a failed merge.
type mergeBranchFailedMsg struct {
	reason string
}

// setMergeDiffLoading sets or clears loading on the merge diff view.
func (m *AppModel) setMergeDiffLoading(loading bool) {
	if loading && m.mergeDiffView == nil {
		if m.diffViewActive {
			m.exitDiffView()
		}
		m.mergeDiffView = mergediff.New(nil, m.config.Theme(), m.nerdFontsDetected)
		m.mergeDiffViewActive = true
		w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
		m.mergeDiffView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
		m.syncViewState()
	}
	if m.mergeDiffView != nil {
		m.mergeDiffView.SetLoading(loading)
		m.viewDirty = true
	}
}

// commitPageSize is the number of commits per page.
// Derived from: typical viewport shows ~8 nodes; 3× provides comfortable
// scroll depth per page while keeping load times snappy.
const commitPageSize = 25

// handleGitTabShortcut processes git-panel tab jump shortcuts.
// Returns (cmd, true) if the key was handled.
func (m *AppModel) handleGitTabShortcut(ks string) (tea.Cmd, bool) {
	switch ks {
	case "alt+C":
		m.gitPanel.SetActiveTab(gitpanel.TabCommits)
		m.focusGitPanel()
		return nil, true
	case "alt+B":
		m.gitPanel.SetActiveTab(gitpanel.TabBranches)
		m.focusGitPanel()
		return nil, true
	case "alt+T":
		m.gitPanel.SetActiveTab(gitpanel.TabTags)
		m.focusGitPanel()
		return nil, true
	case "alt+U":
		m.gitPanel.SetActiveTab(gitpanel.TabUncommitted)
		m.focusGitPanel()
		m.pendingUncommittedAll = true
		return nil, true
	}
	return nil, false
}

// focusGitPanel sets focus to the git panel and syncs focus state.
func (m *AppModel) focusGitPanel() {
	m.focus.SetFocus(component.FocusGitPanel)
	m.syncFocusState()
}

// toggleGitMode switches between git mode and the previous mode.
func (m *AppModel) toggleGitMode() tea.Cmd {
	if m.gitPanel == nil {
		m.statusBar.SetFlash("Not a git repository")
		return nil
	}
	if m.viewMode == ViewGit {
		return m.exitGitMode()
	}
	return m.enterGitMode()
}

// enterGitMode activates git mode, displaying the git explorer and commit tree.
func (m *AppModel) enterGitMode() tea.Cmd {
	// If edit mode is active, exit it first and remember for restore.
	m.preGitEditMode = m.viewMode == ViewEdit
	if m.viewMode == ViewEdit {
		m.exitEditMode()
	}

	// Save ring state and current focus.
	m.savedGitLeftIdx = m.leftRing.index
	m.savedGitRightIdx = m.rightRing.index
	m.savedChatFocus = m.focus.Current()

	m.viewMode = ViewGit

	// Size git components to their panel slots.
	m.resizeGitPanels()

	m.statusBar.SetMode("GIT")
	m.input.SetPlaceholder("git>")

	// Rebuild rings — syncViewState positions git panels in the rings
	// and builds the ring hint with correct panel labels.
	m.syncViewState()
	m.focus.SetFocus(component.FocusGitPanel)
	m.syncFocusState()
	m.statusBar.SetFlash("Git mode")

	// Alt+G always force-reloads; mark loaded so cycling skips reloads.
	m.gitDataLoaded = true
	return tea.Batch(m.gitPanel.LoadData(), m.loadGitBranchesCmd())
}

// exitGitMode deactivates git mode and returns to the previous mode.
// Returns a Cmd when edit mode is restored (LSP reopen).
func (m *AppModel) exitGitMode() tea.Cmd {
	m.savedGitFocus = m.focus.Current()

	// Restore ring state.
	m.leftRing.index = m.savedGitLeftIdx
	m.rightRing.index = m.savedGitRightIdx

	m.viewMode = ViewChat
	m.statusBar.SetMode("CHAT")
	m.input.SetPlaceholder("Type a message...")

	m.syncViewState()

	if m.preGitEditMode {
		m.preGitEditMode = false
		return m.enterEditMode()
	}

	m.focus.SetFocus(m.savedChatFocus)
	m.syncFocusState()
	m.statusBar.SetFlash("View mode")
	return nil
}

// resizeGitPanels sizes the git panel and commit tree to their layout slots.
func (m *AppModel) resizeGitPanels() {
	if m.gitPanel == nil {
		return
	}
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	m.gitPanel.SetSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))

	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.commitTree.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
}

// enterCmdInput activates the input panel for ex command entry,
// pre-filling it with ":" and switching focus.
func (m *AppModel) enterCmdInput() {
	m.editCmdInput = true
	m.input.SetText(":")
	m.input.SetLineStyler(m.cmdLineStyler())
	m.focus.SetFocus(component.FocusInput)
	m.syncFocusState()
}

// exitCmdInput aborts command input and returns focus to the code panel.
func (m *AppModel) exitCmdInput() {
	m.editCmdInput = false
	m.input.SetLineStyler(nil)
	m.input.Clear()
	m.focusCodePanel()
	m.syncFocusState()
}

// detachCurrentEditor extracts the current editor's per-file state and
// stores it in the tiered cache. No-op if no file is loaded.
func (m *AppModel) detachCurrentEditor() {
	ws := m.focusedEditor().Detach()
	if ws.FilePath == "" {
		return
	}
	m.editorCache.Put(ws)
}

// restoreFromCache attempts to restore the editor from the tiered cache.
// Returns true if the entry was found and restored, false otherwise.
func (m *AppModel) restoreFromCache(path string) bool {
	entry, ok := m.editorCache.Take(path)
	if !ok {
		return false
	}
	if entry.IsWarm() {
		m.focusedEditor().AttachWarm(entry.Warm())
		return true
	}
	m.focusedEditor().AttachCold(entry.Cold())
	return true
}

// exCmdInfo describes a known ex command and its argument requirement.
type exCmdInfo struct {
	argHint     string // Non-empty means the command accepts an argument.
	optionalArg bool   // When true, command is valid with or without the arg.
}

// knownExCommands maps command words to their validation info.
var knownExCommands = map[string]exCmdInfo{
	"w": {}, "q": {}, "wq": {}, "x": {}, "q!": {},
	"symbols": {}, "format": {}, "fmt": {},
	"rename":   {argHint: "<newname>"},
	"tabn":     {argHint: "[N]", optionalArg: true},
	"tabp":     {},
	"tabclose": {},
	"tab":      {argHint: "<filename>"},
}

// validateExCommand checks the typed text against known commands.
// Returns: known (command word recognized), valid (ready to execute), hint.
func validateExCommand(text string) (known, valid bool, hint string) {
	cmd := strings.TrimPrefix(strings.TrimSpace(text), ":")
	cmd = strings.TrimSpace(cmd)

	word, args, hasArgs := strings.Cut(cmd, " ")
	info, ok := knownExCommands[word]
	if !ok {
		return false, false, ""
	}
	if info.argHint == "" {
		return true, true, ""
	}
	if info.optionalArg {
		return true, true, info.argHint
	}
	if hasArgs && strings.TrimSpace(args) != "" {
		return true, true, ""
	}
	return true, false, info.argHint
}

// cmdLineStyler returns a line styler that colors command text green when
// valid, red when recognized but incomplete, and returns a hint for
// incomplete commands.
func (m *AppModel) cmdLineStyler() func(string) (string, string) {
	th := m.config.Theme()
	validStyle := lipgloss.NewStyle().Foreground(th.Palette.Success)
	invalidStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)
	return func(text string) (string, string) {
		known, valid, hint := validateExCommand(text)
		if !known {
			return text, ""
		}
		if valid {
			return validStyle.Render(text), ""
		}
		return invalidStyle.Render(text), hint
	}
}

// handleExCommand processes a vim ex command entered in the input panel
// during edit mode. Supported: :w (save), :q (quit), :wq (save+quit),
// :q! (force quit).
func (m *AppModel) handleExCommand(text string) tea.Cmd {
	cmd := strings.TrimSpace(text)

	// Strip leading ':' if present.
	cmd = strings.TrimPrefix(cmd, ":")
	cmd = strings.TrimSpace(cmd)

	switch cmd {
	case "w":
		m.saveEditorBuffer()
	case "q":
		return m.closeCurrentTab(false)
	case "wq", "x":
		m.saveEditorBuffer()
		return m.closeCurrentTab(false)
	case "q!":
		return m.closeCurrentTab(true)
	case "tabp":
		return m.prevTab()
	case "tabclose":
		return m.closeCurrentTab(false)
	case "symbols":
		if fp := m.focusedEditor().FilePath(); fp != "" {
			return m.lspDocumentSymbolCmd(fp)
		}
	case "format", "fmt":
		if fp := m.focusedEditor().FilePath(); fp != "" {
			var flushContent string
			if m.focusedEditor().LSPDirty() {
				m.focusedEditor().ClearLSPDirty()
				flushContent = m.focusedEditor().Content()
			}
			return m.lspFormatCmd(fp, flushContent, m.focusedEditor().EditGeneration())
		}
	default:
		if cmd == "tabn" || strings.HasPrefix(cmd, "tabn ") {
			rest := strings.TrimSpace(strings.TrimPrefix(cmd, "tabn"))
			if rest == "" {
				return m.nextTab()
			}
			n, err := strconv.Atoi(rest)
			if err != nil || n < 1 {
				m.statusBar.SetFlash("Usage: :tabn [N]")
				return nil
			}
			return m.switchToTab(n - 1)
		}
		if name, ok := strings.CutPrefix(cmd, "tab "); ok {
			return m.handleTabCommand(strings.TrimSpace(name))
		}
		if newName, ok := strings.CutPrefix(cmd, "rename "); ok {
			return m.handleRenameCommand(strings.TrimSpace(newName))
		}
		m.statusBar.SetFlash("Unknown command: :" + cmd)
	}
	return nil
}

// handleRenameCommand initiates an LSP rename for the symbol under the cursor.
func (m *AppModel) handleRenameCommand(newName string) tea.Cmd {
	if newName == "" {
		m.statusBar.SetFlash("Usage: :rename <newname>")
		return nil
	}
	fp := m.focusedEditor().FilePath()
	if fp == "" {
		return nil
	}
	line := m.focusedEditor().CursorLine()
	col := m.focusedEditor().CursorCol()

	// Flush pending edits so the server sees the latest buffer.
	if m.focusedEditor().LSPDirty() {
		m.focusedEditor().ClearLSPDirty()
		_ = m.lspManager.NotifyDidChange(m.ctx, m.config.ProjectRoot, fp, m.focusedEditor().Content())
	}

	if m.lspManager.PrepareRenameSupported(m.config.ProjectRoot, fp) {
		return m.lspPrepareRenameCmd(fp, line, col, newName)
	}
	return m.lspRenameCmd(fp, line, col, newName)
}

// tabCompleter provides tab-completion candidates from open tabs for the
// :tab ex command. Implements input.CompletionProvider.
type tabCompleter struct {
	tabOrderFn func() []string
}

func (tc *tabCompleter) Name() string { return "tabs" }

func (tc *tabCompleter) Complete(prefix string, limit int) []Candidate {
	tabs := tc.tabOrderFn()
	if len(tabs) == 0 {
		return nil
	}
	lower := strings.ToLower(prefix)
	var out []Candidate
	for _, p := range tabs {
		base := filepath.Base(p)
		if prefix == "" || strings.Contains(strings.ToLower(base), lower) {
			out = append(out, Candidate{
				Text:    base,
				Display: base,
			})
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// slashCommand describes a known chat slash command.
type slashCommand struct {
	name string // Without leading "/".
	desc string
}

// chatSlashCommands is the single source of truth for all chat slash commands.
// Both the validator and the completer derive from this slice.
var chatSlashCommands = []slashCommand{
	{name: "clear", desc: "Clear the chat history"},
	{name: "login", desc: "Open the provider login panel"},
}

// isKnownSlashCommand reports whether cmd (without "/") is a registered command.
func isKnownSlashCommand(cmd string) bool {
	for _, sc := range chatSlashCommands {
		if sc.name == cmd {
			return true
		}
	}
	return false
}

// slashCommandCompleter provides autocomplete candidates for slash commands
// typed in the chat input. Implements input.CompletionProvider.
type slashCommandCompleter struct{}

func (sc *slashCommandCompleter) Name() string { return "commands" }

func (sc *slashCommandCompleter) Complete(prefix string, limit int) []Candidate {
	if !strings.HasPrefix(prefix, "/") {
		return nil
	}
	typed := strings.ToLower(prefix[1:])
	var out []Candidate
	for _, cmd := range chatSlashCommands {
		if strings.HasPrefix(cmd.name, typed) {
			out = append(out, Candidate{
				Text:        "/" + cmd.name,
				Display:     "/" + cmd.name,
				Description: cmd.desc,
				Category:    "command",
			})
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// Candidate is an alias for the input completion candidate type.
type Candidate = inputpkg.Candidate

// handleTabCommand switches to an open tab matching the given filename.
func (m *AppModel) handleTabCommand(name string) tea.Cmd {
	if name == "" {
		m.statusBar.SetFlash("Usage: :tab <filename>")
		return nil
	}
	for i, p := range m.focusedTabOrder() {
		if filepath.Base(p) == name {
			return m.switchToTab(i)
		}
	}
	m.statusBar.SetFlash("No open tab: " + name)
	return nil
}

// saveEditorBuffer writes the inline editor buffer to disk.
func (m *AppModel) saveEditorBuffer() {
	path := m.focusedEditor().FilePath()
	if path == "" {
		m.statusBar.SetFlash("No file path")
		return
	}
	content := m.focusedEditor().Content()
	if err := atomicWriteFile(path, []byte(content), 0o644); err != nil {
		m.statusBar.SetFlash("Write failed: " + err.Error())
		return
	}
	m.focusedEditor().MarkSaved()
	m.lspDidSaveAsync(path, content)
	m.codePanel.SetContent(content, path, m.focusedEditor().Language())
	m.nudgeGitWatcher()
	m.refreshTabsModified()
	m.statusBar.SetFlash("Saved " + path)
}

// atomicWriteFile writes data to a temporary file in the same directory as
// path, then renames it into place. This prevents partial writes from
// corrupting the target file on crash or power loss.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sylk-save-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Ensure cleanup on any error path.
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	tmpName = "" // prevent deferred cleanup after successful rename
	return nil
}

// codePanelView returns the rendered view for the code panel slot.
// Handles read-only viewer, single-pane editor, full preview, and
// multi-pane compositing (iterative, not recursive).
func (m *AppModel) codePanelView() string {
	if m.viewMode != ViewEdit {
		return m.codePanel.View()
	}

	// Full preview mode: preview active, single editor pane with no tabs.
	if m.hasPreview() && len(m.paneEditors) == 1 && len(m.focusedTabOrder()) == 0 {
		bar := m.previewTabBarView()
		content := m.previewPanel.View(m.cursorVisible)
		if bar != "" {
			return lipgloss.JoinVertical(lipgloss.Left, bar, content)
		}
		return content
	}

	// Full markdown preview mode: mdPreview active, sole editor has no tabs.
	if m.isFullMdPreview() {
		rightW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
		bar := m.mdPreviewTabBar(max(rightW-panelBorderSize, 1))
		content := m.mdPreviewPanel.View()
		if bar != "" {
			return lipgloss.JoinVertical(lipgloss.Left, bar, content)
		}
		return content
	}

	// Single editor pane, no splits: direct render (no compositing overhead).
	if m.paneTree.IsLeaf() {
		content := m.focusedEditor().View(m.cursorVisible)
		bar := m.tabBarView()
		if bar != "" {
			content = lipgloss.JoinVertical(lipgloss.Left, bar, content)
		}
		content = m.overlayMdTooltipOnContent(content)
		return m.overlayBlockedChord(content)
	}

	// Multi-pane: iterative compositing via pre-computed layout.
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)
	contentH := max(innerH-1, 1) // Reserve 1 row for status line.

	area := pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
	content := m.composePanes(area)

	// Overlay markdown tooltip in multi-pane mode.
	if m.mdTooltipTab >= 0 && m.mdTooltipPane != 0 {
		rects := m.paneTree.ComputeLayout(area)
		if pr, ok := rects[m.mdTooltipPane]; ok {
			rows := m.mdTooltipRows()
			lines := strings.Split(content, "\n")
			overlayMdTooltipRows(lines, rows, pr.X+m.mdTooltipX, pr.Y+tabbar.Height-1)
			content = strings.Join(lines, "\n")
		}
	}

	statusLine := m.focusedEditor().StatusLineView(innerW)
	return m.overlayBlockedChord(lipgloss.JoinVertical(lipgloss.Left, content, statusLine))
}

// composePanes renders all panes in the tree and composites them into a
// single string using iterative row-by-row assembly. Divider characters
// are placed at positions computed from the tree structure.
func (m *AppModel) composePanes(area pane.Rect) string {
	rects := m.paneTree.ComputeLayout(area)
	dividers := m.paneTree.Dividers(area)
	leaves := m.paneTree.Leaves()

	th := m.config.Theme()
	divStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)
	dropDivStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary)

	// Determine drop-target rect for divider highlighting.
	var dropRect pane.Rect
	hasDropTarget := m.tabDropTarget != 0
	if hasDropTarget {
		dropRect = rects[m.tabDropTarget]
	}

	// Render each leaf pane into a block of lines.
	leafLines := make(map[pane.PaneID][]string, len(leaves))
	for _, id := range leaves {
		r := rects[id]
		leafLines[id] = strings.Split(m.renderLeafPane(id, r), "\n")
	}

	// Build output row by row.
	type seg struct {
		x int
		s string
	}
	rows := make([]string, area.H)
	for y := range area.H {
		var segs []seg

		// Pane content segments.
		for _, id := range leaves {
			r := rects[id]
			if y < r.Y || y >= r.Y+r.H {
				continue
			}
			lineIdx := y - r.Y
			lines := leafLines[id]
			line := ""
			if lineIdx < len(lines) {
				line = lines[lineIdx]
			}
			segs = append(segs, seg{r.X, line})
		}

		// Divider segments (highlight dividers adjacent to the drop target).
		for _, d := range dividers {
			style := divStyle
			if hasDropTarget && isDividerAdjacentToRect(d, dropRect) {
				style = dropDivStyle
			}
			switch {
			case d.Dir == pane.SplitVertical && y >= d.Y && y < d.Y+d.Len:
				segs = append(segs, seg{d.X, style.Render("│")})
			case d.Dir == pane.SplitHorizontal && y == d.Y:
				segs = append(segs, seg{d.X, style.Render(strings.Repeat("─", d.Len))})
			}
		}

		// Sort by X and concatenate.
		slices.SortFunc(segs, func(a, b seg) int { return a.x - b.x })
		var b strings.Builder
		for _, s := range segs {
			b.WriteString(s.s)
		}
		rows[y] = b.String()
	}

	return strings.Join(rows, "\n")
}

// isDividerAdjacentToRect reports whether a divider segment borders the given rect.
func isDividerAdjacentToRect(d pane.Divider, r pane.Rect) bool {
	if d.Dir == pane.SplitVertical {
		adjacent := d.X == r.X-1 || d.X == r.X+r.W
		overlaps := d.Y < r.Y+r.H && d.Y+d.Len > r.Y
		return adjacent && overlaps
	}
	adjacent := d.Y == r.Y-1 || d.Y == r.Y+r.H
	overlaps := d.X < r.X+r.W && d.X+d.Len > r.X
	return adjacent && overlaps
}

// renderLeafPane renders a single pane (editor or preview) as a sized block.
// The returned string has exactly r.H lines, each r.W visual columns wide.
func (m *AppModel) renderLeafPane(id pane.PaneID, r pane.Rect) string {
	sizer := lipgloss.NewStyle().
		Width(r.W).MaxWidth(r.W).
		Height(r.H).MaxHeight(r.H)

	if id == m.tabDropTarget {
		return m.renderDropTargetPane(id, r, sizer)
	}

	if id == m.previewPane {
		bar := m.previewTabBarViewSized(r.W)
		content := m.previewPanel.View(m.cursorVisible)
		return sizer.Render(lipgloss.JoinVertical(lipgloss.Left, bar, content))
	}

	if id == m.mdPreviewPane {
		bar := m.mdPreviewTabBar(r.W)
		content := m.mdPreviewPanel.View()
		return sizer.Render(lipgloss.JoinVertical(lipgloss.Left, bar, content))
	}

	ps := m.paneEditors[id]
	bar := m.paneTabBarView(id, r.W)
	content := ps.editor.ViewContent(m.cursorVisible)
	if bar != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, bar, content)
	}
	return sizer.Render(content)
}

// dropOverlayAlpha is the blend factor for the drop-target background tint.
// Derived from: 15% matches VS Code's drag-and-drop overlay opacity.
const dropOverlayAlpha = 0.15

// renderDropTargetPane renders an editor pane as a drop target: the tab bar
// stays normal, content backgrounds are tinted with a blended overlay color,
// and a centered "Drop tab here" label is composited on top.
func (m *AppModel) renderDropTargetPane(id pane.PaneID, r pane.Rect, sizer lipgloss.Style) string {
	ps := m.paneEditors[id]
	if ps == nil {
		return sizer.Render("")
	}
	bar := m.paneTabBarView(id, r.W)
	content := ps.editor.ViewContent(m.cursorVisible)
	if bar != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, bar, content)
	}
	sized := sizer.Render(content)
	lines := strings.Split(sized, "\n")

	// Compute overlay background by blending base bg with Primary.
	th := m.config.Theme()
	tr, tg, tb := blendColors(th.Palette.Background, th.Palette.Primary, dropOverlayAlpha)
	bgCode := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", tr, tg, tb)

	// Tint content lines below the tab bar.
	barH := tabbar.Height
	if bar == "" {
		barH = 0
	}
	for i := barH; i < len(lines); i++ {
		lines[i] = tintLineBg(lines[i], bgCode)
	}

	// Overlay centered "Drop tab here" indicator.
	label := lipgloss.NewStyle().
		Foreground(th.Palette.Background).
		Background(th.Palette.Primary).
		Bold(true).
		Padding(0, 1).
		Render("Drop tab here")

	centerY := barH + (r.H-barH)/2
	labelW := lipgloss.Width(label)
	centerX := max((r.W-labelW)/2, 0)

	if centerY >= 0 && centerY < len(lines) {
		orig := lines[centerY]
		left := truncateStyledN(orig, centerX)
		right := skipStyledN(orig, centerX+labelW)
		// Re-insert tint background after the reset so the overlay
		// continues past the label to the end of the line.
		const resetSeq = "\x1b[0m"
		if strings.HasPrefix(right, resetSeq) {
			right = resetSeq + bgCode + right[len(resetSeq):]
		}
		lines[centerY] = left + label + right
	}

	return strings.Join(lines, "\n")
}

// tintLineBg replaces all ANSI background colors in a line with bgCode,
// producing a semi-transparent overlay effect where foreground content
// remains visible but backgrounds are uniformly tinted.
func tintLineBg(s string, bgCode string) string {
	var out strings.Builder
	out.Grow(len(s) + len(s)/4)
	out.WriteString(bgCode)

	i := 0
	for i < len(s) {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			out.WriteByte(s[i])
			i++
			continue
		}
		// Find end of CSI sequence.
		j := i + 2
		for j < len(s) && !isCSITerminator(s[j]) {
			j++
		}
		if j >= len(s) {
			out.WriteString(s[i:])
			break
		}
		j++ // include terminator
		out.WriteString(s[i:j])
		// After any SGR sequence (m-terminated), re-insert our bg override.
		if s[j-1] == 'm' {
			out.WriteString(bgCode)
		}
		i = j
	}
	out.WriteString("\x1b[0m")
	return out.String()
}

// blendColors blends two hex colors at the given alpha (0.0–1.0).
func blendColors(base, overlay lipgloss.Color, alpha float64) (uint8, uint8, uint8) {
	br, bg, bb := parseHexColor(string(base))
	or, og, ob := parseHexColor(string(overlay))
	r := uint8(float64(br) + alpha*float64(int(or)-int(br)))
	g := uint8(float64(bg) + alpha*float64(int(og)-int(bg)))
	b := uint8(float64(bb) + alpha*float64(int(ob)-int(bb)))
	return r, g, b
}

// parseHexColor parses a "#RRGGBB" string into RGB components.
func parseHexColor(hex string) (uint8, uint8, uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	return uint8(r), uint8(g), uint8(b)
}

// truncateStyledN truncates a potentially ANSI-styled string to maxW visible
// columns, preserving escape sequences and appending a reset.
func truncateStyledN(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	col := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			b.WriteRune(r)
			continue
		}
		if inEsc {
			b.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if col >= maxW {
			break
		}
		b.WriteRune(r)
		col++
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// skipStyledN drops the first skip visible columns from a styled string
// and returns the remainder with a reset prefix.
func skipStyledN(s string, skip int) string {
	if skip <= 0 {
		return s
	}
	vis := 0
	i := 0
	for i < len(s) && vis < skip {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSITerminator(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j
			continue
		}
		i++
		vis++
	}
	if i >= len(s) {
		return ""
	}
	return "\x1b[0m" + s[i:]
}

// isCSITerminator reports whether b is the final byte of a CSI sequence.
func isCSITerminator(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// paneTabBarConfig builds the tabbar.Config for a specific editor pane.
func (m *AppModel) paneTabBarConfig(id pane.PaneID, width int) tabbar.Config {
	ps := m.paneEditors[id]
	tabs := make([]tabbar.Tab, len(ps.tabOrder))
	currentPath := ps.editor.FilePath()
	activeIdx := 0
	for i, path := range ps.tabOrder {
		modified := false
		if path == currentPath {
			activeIdx = i
			modified = ps.editor.Modified()
		} else {
			modified = m.editorCache.IsModified(path)
		}
		tabs[i] = tabbar.Tab{Path: path, Modified: modified}
	}

	hasSplits := m.paneTree.LeafCount() > 1
	isFocused := id == m.focusedPane && m.isEditorFocused()
	hoverClose := -1
	if id == m.tabHoverPane {
		hoverClose = m.tabHoverClose
	}

	return tabbar.Config{
		Tabs:       tabs,
		Active:     activeIdx,
		Width:      width,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      m.config.Theme(),
		FlashLeft:  isFocused && time.Now().Before(m.tabArrowFlashLeftUntil),
		FlashRight: isFocused && time.Now().Before(m.tabArrowFlashRightUntil),
		HoverClose: hoverClose,
		Focused:    hasSplits && isFocused,
		DimActive:  hasSplits && !isFocused,
	}
}

// paneTabBarView renders the tab bar for a specific editor pane.
func (m *AppModel) paneTabBarView(id pane.PaneID, width int) string {
	ps := m.paneEditors[id]
	if len(ps.tabOrder) == 0 {
		return ""
	}
	return tabbar.View(m.paneTabBarConfig(id, width))
}

// previewTabBarViewSized renders the preview tab bar at the given width.
func (m *AppModel) previewTabBarViewSized(width int) string {
	if m.previewPanel.FilePath() == "" {
		return ""
	}
	hasSplits := m.paneTree.LeafCount() > 1
	cfg := tabbar.Config{
		Tabs: []tabbar.Tab{{
			Path:        m.previewPanel.FilePath(),
			Modified:    false,
			LabelPrefix: "Preview: ",
		}},
		Active:     0,
		Width:      width,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      m.config.Theme(),
		HoverClose: m.previewTabHoverClose,
		Focused:    hasSplits && m.isPreviewFocused(),
		DimActive:  hasSplits && !m.isPreviewFocused(),
	}
	return tabbar.View(cfg)
}

// previewTabBarConfig returns the tabbar.Config for the preview tab bar.
func (m *AppModel) previewTabBarConfig() tabbar.Config {
	th := m.config.Theme()
	rightW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)

	// In split mode, preview gets half the width.
	barWidth := innerW
	if len(m.focusedTabOrder()) > 0 {
		dividerWidth := 1
		barWidth = (innerW - dividerWidth) / 2
	}

	return tabbar.Config{
		Tabs: []tabbar.Tab{{
			Path:        m.previewPanel.FilePath(),
			Modified:    false,
			LabelPrefix: "Preview: ",
		}},
		Active:     0,
		Width:      barWidth,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      th,
		HoverClose: m.previewTabHoverClose,
		Focused:    len(m.focusedTabOrder()) > 0 && m.isPreviewFocused(),
		DimActive:  len(m.focusedTabOrder()) > 0 && !m.isPreviewFocused(),
	}
}

// previewTabBarView renders the tab bar for the preview panel with a single
// "Preview: filename" tab.
func (m *AppModel) previewTabBarView() string {
	if m.previewPanel.FilePath() == "" {
		return ""
	}
	return tabbar.View(m.previewTabBarConfig())
}

// ---------------------------------------------------------------------------
// Preview panel handlers
// ---------------------------------------------------------------------------

// handleFilePreview loads a file into the preview panel (read-only).
func (m *AppModel) handleFilePreview(o msg.FilePreviewMsg) tea.Cmd {
	if o.Path == m.previewPanel.FilePath() && m.hasPreview() {
		return nil // Already showing this file.
	}

	data, err := os.ReadFile(o.Path)
	if err != nil {
		m.statusBar.SetFlash("Cannot preview: " + filepath.Base(o.Path))
		return nil
	}

	// Close the LSP document for the previous preview file (unless it's
	// also open in an editor tab).
	oldPath := m.previewPanel.FilePath()
	if oldPath != "" && oldPath != o.Path && !m.isFileOpenInEditor(oldPath) {
		m.lspDidCloseAsync(oldPath)
	}

	content := string(data)
	m.previewPanel.SetContent(content, o.Path, o.Language)

	wasActive := m.hasPreview()
	if !wasActive {
		// Insert preview as the leftmost leaf at the root level so it's
		// always the leftmost pane regardless of existing splits.
		m.paneCounter++
		m.previewPane = m.paneCounter
		m.paneTree.InsertLeft(m.previewPane, pane.SplitVertical)
	}

	if o.Line > 0 {
		m.previewPanel.ScrollToLine(o.Line - 1)
	}

	// Recompute layout when transitioning into preview.
	if !wasActive {
		m.recalcLayout()
	}

	// Notify LSP about the preview file so hover works. If the file is
	// already open in an editor, the document tracker silently skips the
	// duplicate open.
	lang := detectEditorLanguage(o.Path)
	return m.lspDidOpenCmd(o.Path, lang, content)
}

// dismissPreview closes the preview panel and restores the full code panel.
// Focus moves to the last active editor pane when one exists.
func (m *AppModel) dismissPreview() {
	if !m.hasPreview() {
		return
	}
	wasPreviewFocused := m.focus.Current() == pane.PaneFocusID(m.previewPane)
	previewPath := m.previewPanel.FilePath()

	// Remove preview leaf from the pane tree.
	m.paneTree.Close(m.previewPane)
	m.previewPane = 0
	m.previewPanel.ClearFile()

	// Close the LSP document unless the file is also open in an editor tab.
	if previewPath != "" && !m.isFileOpenInEditor(previewPath) {
		m.lspDidCloseAsync(previewPath)
	}

	// Focus the last active editor pane. Fall back to file tree when
	// no editor pane exists.
	if wasPreviewFocused || !m.isEditorFocused() {
		if len(m.paneEditors) > 0 {
			m.focusCodePanel()
		} else {
			m.focus.SetFocus(component.FocusFileTree)
		}
	}
	m.recalcLayout()
	m.syncFocusState()
}

// ---------------------------------------------------------------------------
// Markdown preview (rendered markdown split-right of the source editor)
// ---------------------------------------------------------------------------

// openMarkdownPreview splits the focused pane to the right and displays the
// rendered markdown content. If the markdown preview is already open, the
// content is updated in place.
func (m *AppModel) openMarkdownPreview(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		m.statusBar.SetFlash("Cannot preview: " + filepath.Base(path))
		return
	}
	content := string(data)

	// Already open — just refresh content.
	if m.mdPreviewPane != 0 {
		m.mdPreviewPanel.SetContent(content, path)
		return
	}

	// Compute the focused pane area for size checking.
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)
	contentH := max(innerH-1, 1)
	area := pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
	rects := m.paneTree.ComputeLayout(area)
	focusedRect := rects[m.focusedPane]

	m.paneCounter++
	m.mdPreviewPane = m.paneCounter

	if !m.paneTree.Split(m.focusedPane, m.mdPreviewPane, pane.SplitVertical, focusedRect) {
		m.paneCounter--
		m.mdPreviewPane = 0
		m.statusBar.SetFlash("Pane too small to split")
		return
	}

	m.mdPreviewPanel.SetContent(content, path)
	m.mdPreviewPanel.SetFocusID(pane.PaneFocusID(m.mdPreviewPane))
	m.resizeInlineEditor()
	m.syncViewState()
	m.statusBar.SetFlash("Markdown preview")
}

// dismissMarkdownPreview closes the markdown preview pane and restores layout.
func (m *AppModel) dismissMarkdownPreview() {
	if m.mdPreviewPane == 0 {
		return
	}
	wasFocused := m.focus.Current() == pane.PaneFocusID(m.mdPreviewPane)
	m.paneTree.Close(m.mdPreviewPane)
	m.mdPreviewPane = 0
	m.mdPreviewPanel.ClearFile()

	if wasFocused {
		m.focusCodePanel()
	}
	m.resizeInlineEditor()
	m.syncViewState()
	m.syncFocusState()
}

// mdPreviewTabBar renders a single tab for the markdown preview pane.
func (m *AppModel) mdPreviewTabBar(width int) string {
	if m.mdPreviewPanel.FilePath() == "" {
		return ""
	}
	hasSplits := m.paneTree.LeafCount() > 1
	isFocused := m.focus.Current() == pane.PaneFocusID(m.mdPreviewPane)
	cfg := tabbar.Config{
		Tabs: []tabbar.Tab{{
			Path:        m.mdPreviewPanel.FilePath(),
			Modified:    false,
			LabelPrefix: "Markdown: ",
		}},
		Active:     0,
		Width:      width,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      m.config.Theme(),
		HoverClose: m.mdPreviewTabHoverClose,
		Focused:    hasSplits && isFocused,
		DimActive:  hasSplits && !isFocused,
	}
	return tabbar.View(cfg)
}

// isFileOpenInEditor reports whether the given file path is currently open
// in any editor pane's tab order.
func (m *AppModel) isFileOpenInEditor(path string) bool {
	for _, ps := range m.paneEditors {
		for _, tabPath := range ps.tabOrder {
			if tabPath == path {
				return true
			}
		}
	}
	return false
}

// splitPane splits the focused editor pane in the given direction, creating
// a new empty editor pane. The original pane becomes Left/Top and the new
// pane becomes Right/Bottom. If the split would violate minimum pane size,
// the operation is silently ignored.
func (m *AppModel) splitPane(dir pane.SplitDir) {
	// Compute the current area of the focused pane for size checking.
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)
	contentH := max(innerH-1, 1) // minus status line
	area := pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
	rects := m.paneTree.ComputeLayout(area)
	focusedRect := rects[m.focusedPane]

	m.paneCounter++
	newID := m.paneCounter

	if !m.paneTree.Split(m.focusedPane, newID, dir, focusedRect) {
		m.paneCounter--
		m.statusBar.SetFlash("Pane too small to split")
		return
	}

	// Create a new empty editor for the new pane.
	th := m.config.Theme()
	m.paneEditors[newID] = &editorPaneState{editor: editor.New(th)}

	// Resize all panes to their new layout rects.
	m.resizeInlineEditor()
	m.syncViewState()
	m.statusBar.SetFlash("Split pane")
}

// closePane closes the currently focused pane. If the active editor has
// unsaved modifications, a save prompt is shown first. The sibling subtree's
// leftmost leaf receives focus. No-op if only one pane remains.
func (m *AppModel) closePane() {
	closingID := m.focusedPane

	// If the focused pane is the preview, delegate to dismissPreview.
	if closingID == m.previewPane {
		m.dismissPreview()
		return
	}

	// If the focused pane is the markdown preview, delegate to dismissMarkdownPreview.
	if closingID == m.mdPreviewPane {
		m.dismissMarkdownPreview()
		return
	}

	// Check for unsaved modifications in the active editor.
	ps := m.paneEditors[closingID]
	if ps.editor.Modified() {
		m.pendingPaneClose = closingID
		m.pendingClosePrompt = true
		name := filepath.Base(ps.editor.FilePath())
		m.statusBar.SetPrompt("Save changes to " + name + " before closing pane? (y)es (n)o")
		return
	}

	// If this is the last editor pane and a markdown preview is active,
	// clear the editor content so the preview takes the full panel.
	if m.mdPreviewPane != 0 && len(m.paneEditors) == 1 {
		ps := m.paneEditors[closingID]
		if ps != nil && ps.editor.FilePath() != "" {
			ws := ps.editor.Detach()
			m.editorCache.Put(ws)
		}
		if ps != nil {
			ps.editor.ClearFile()
			ps.tabOrder = nil
		}
		m.focus.SetFocus(pane.PaneFocusID(m.mdPreviewPane))
		m.resizeInlineEditor()
		m.syncViewState()
		m.syncFocusState()
		m.statusBar.SetFlash("Closed pane")
		return
	}

	m.closePaneForce(closingID)
}

// closePaneForce unconditionally closes the pane with the given ID.
// Detaches the editor to cache and collapses the tree node.
func (m *AppModel) closePaneForce(closingID pane.PaneID) {
	ps := m.paneEditors[closingID]
	if ps != nil && ps.editor.FilePath() != "" {
		ws := ps.editor.Detach()
		m.editorCache.Put(ws)
	}

	newFocus, ok := m.paneTree.Close(closingID)
	if !ok {
		return
	}
	delete(m.paneEditors, closingID)

	// Move focus to the sibling. If the sibling is the preview pane,
	// find the nearest editor pane instead (focusedPane must be an editor).
	if _, isEditor := m.paneEditors[newFocus]; isEditor {
		m.focusedPane = newFocus
	} else {
		for id := range m.paneEditors {
			m.focusedPane = id
			break
		}
	}
	m.focusCodePanel()
	m.resizeInlineEditor()
	m.syncViewState()
	m.syncFocusState()
	m.statusBar.SetFlash("Closed pane")
}

// openFromPreview promotes the previewed file to an editor tab.
// Closes the preview and opens the file via the standard FileOpenMsg pipeline.
func (m *AppModel) openFromPreview() tea.Cmd {
	path := m.previewPanel.FilePath()
	lang := m.previewPanel.Language()
	m.dismissPreview()

	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     path,
			Language: lang,
		}
	}
}

// overlayMdTooltipOnContent overlays the markdown "Preview" tooltip on the
// rendered code panel content when a markdown tab is being hovered.
func (m *AppModel) overlayMdTooltipOnContent(content string) string {
	if m.mdTooltipTab < 0 || m.mdTooltipPane == 0 {
		return content
	}
	rows := m.mdTooltipRows()
	lines := strings.Split(content, "\n")
	// The divider sits at row 1 (second row of the 2-row tab bar).
	// Place the tooltip starting on row 2 (first content row below the divider).
	overlayMdTooltipRows(lines, rows, m.mdTooltipX, tabbar.Height-1)
	return strings.Join(lines, "\n")
}

// overlayBlockedChord prepends the blocked-chord hint bar to content when a
// chord was triggered in a non-chat mode. Trims content from the bottom so
// the total line count stays within the panel's inner height.
func (m *AppModel) overlayBlockedChord(content string) string {
	th := m.config.Theme()
	hint := m.blockedChordHint(th)
	if hint == "" {
		return content
	}
	w, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(w-panelBorderSize, 1)
	hintWidth := lipgloss.Width(hint)
	pad := max(innerW-hintWidth, 0)
	divider := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", innerW))

	// Replace the first two lines instead of prepending (no layout shift).
	lines := strings.Split(content, "\n")
	hintLine := strings.Repeat(" ", pad) + hint
	if len(lines) >= chordHintLines {
		lines[0] = hintLine
		lines[1] = divider
	} else {
		lines = append([]string{hintLine, divider}, lines...)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Tab bar
// ---------------------------------------------------------------------------

// tabBarView renders the tab bar. Returns "" when there are no tabs.
func (m *AppModel) tabBarView() string {
	if len(m.focusedTabOrder()) == 0 {
		return ""
	}
	return tabbar.View(m.tabBarConfig())
}

// appendTab adds a file path to the tab order if not already present.
func (m *AppModel) appendTab(path string) {
	for _, p := range m.focusedTabOrder() {
		if p == path {
			return
		}
	}
	ps := m.paneEditors[m.focusedPane]
	ps.tabOrder = append(ps.tabOrder, path)
}

// isExistingTab reports whether path is already open as a tab in any pane.
func (m *AppModel) isExistingTab(path string) bool {
	pid, _ := m.findPaneWithTab(path)
	return pid != 0
}

// findPaneWithTab searches all editor panes for a tab matching the given
// path. Returns the PaneID and tab index, or (0, -1) if not found.
func (m *AppModel) findPaneWithTab(path string) (pane.PaneID, int) {
	for id, ps := range m.paneEditors {
		for i, p := range ps.tabOrder {
			if p == path {
				return id, i
			}
		}
	}
	return 0, -1
}

// removeTab removes a file path from the tab order.
func (m *AppModel) removeTab(path string) {
	ps := m.paneEditors[m.focusedPane]
	for i, p := range ps.tabOrder {
		if p == path {
			ps.tabOrder = slices.Delete(ps.tabOrder, i, i+1)
			return
		}
	}
}

// appendTabToPane adds path to the given pane's tab order if not already present.
func (m *AppModel) appendTabToPane(pid pane.PaneID, path string) {
	ps := m.paneEditors[pid]
	if ps == nil || slices.Contains(ps.tabOrder, path) {
		return
	}
	ps.tabOrder = append(ps.tabOrder, path)
}

// openFileCmd returns a tea.Cmd that emits a FileOpenMsg for the given path.
func (m *AppModel) openFileCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     path,
			Name:     filepath.Base(path),
			Language: detectEditorLanguage(path),
		}
	}
}

// finalizeCrossPaneDrop executes a cross-pane tab drop if one is pending.
// Returns nil if no cross-pane drop occurred.
func (m *AppModel) finalizeCrossPaneDrop() tea.Cmd {
	if m.tabDragIdx < 0 || m.tabDropTarget == 0 {
		return nil
	}
	if m.tabDragSourcePane == m.previewPane {
		return m.movePreviewToPane(m.tabDropTarget)
	}
	return m.moveTabToPane(m.tabDragSourcePane, m.tabDragIdx, m.tabDropTarget)
}

// moveTabToPane transfers tab at srcIdx from srcPID to dstPID.
func (m *AppModel) moveTabToPane(srcPID pane.PaneID, srcIdx int, dstPID pane.PaneID) tea.Cmd {
	srcPS := m.paneEditors[srcPID]
	if srcPS == nil || srcIdx >= len(srcPS.tabOrder) {
		return nil
	}
	path := srcPS.tabOrder[srcIdx]

	// Transfer: remove from source, append to target.
	srcPS.tabOrder = slices.Delete(srcPS.tabOrder, srcIdx, srcIdx+1)
	m.appendTabToPane(dstPID, path)

	// Handle source pane post-removal.
	m.reconcileSourcePane(srcPID, path)

	// Focus target pane and open the file.
	m.focusedPane = dstPID
	m.focusCodePanel()
	m.syncFocusState()
	m.resizeInlineEditor()
	return m.openFileCmd(path)
}

// movePreviewToPane promotes the previewed file to an editor tab in dstPID.
func (m *AppModel) movePreviewToPane(dstPID pane.PaneID) tea.Cmd {
	path := m.previewPanel.FilePath()
	if path == "" {
		return nil
	}
	m.appendTabToPane(dstPID, path)
	m.dismissPreview()

	m.focusedPane = dstPID
	m.focusCodePanel()
	m.syncFocusState()
	m.resizeInlineEditor()
	return m.openFileCmd(path)
}

// reconcileSourcePane adjusts the source pane after a tab was removed.
// If empty, closes the pane. If the active tab was removed, switches to adjacent.
func (m *AppModel) reconcileSourcePane(srcPID pane.PaneID, removedPath string) {
	srcPS := m.paneEditors[srcPID]
	if len(srcPS.tabOrder) == 0 {
		m.closePaneForce(srcPID)
		return
	}
	if srcPS.editor.FilePath() != removedPath {
		return
	}
	nextIdx := min(m.tabDragIdx, len(srcPS.tabOrder)-1)
	m.switchPaneToTab(srcPID, nextIdx)
}

// switchPaneToTab detaches the current editor in the given pane and loads
// the tab at the specified index from that pane's tab order.
func (m *AppModel) switchPaneToTab(pid pane.PaneID, idx int) {
	ps := m.paneEditors[pid]
	if ps == nil || idx < 0 || idx >= len(ps.tabOrder) {
		return
	}
	if ps.editor.FilePath() != "" {
		ws := ps.editor.Detach()
		m.editorCache.Put(ws)
	}
	m.loadFileIntoPane(pid, ps.tabOrder[idx])
}

// loadFileIntoPane loads a file into the specified pane's editor, using
// the tiered cache if available, otherwise reading from disk.
func (m *AppModel) loadFileIntoPane(pid pane.PaneID, path string) {
	ps := m.paneEditors[pid]
	if ps == nil {
		return
	}
	entry, ok := m.editorCache.Take(path)
	if ok && entry.IsWarm() {
		ps.editor.AttachWarm(entry.Warm())
		return
	}
	if ok {
		ps.editor.AttachCold(entry.Cold())
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	ps.editor.OpenFile(path, string(data), detectEditorLanguage(path))
}

// activeTabIndex returns the index of the active file in tabOrder.
func (m *AppModel) activeTabIndex() int {
	current := m.focusedEditor().FilePath()
	for i, p := range m.focusedTabOrder() {
		if p == current {
			return i
		}
	}
	return 0
}

// handleTabBarClick processes a click on the tab bar: close-icon clicks close
// the tab, other clicks begin a drag (and switch to the tab on release if no
// reordering occurred).
func (m *AppModel) handleTabBarClick(viewX int) (bool, tea.Cmd) {
	cfg := m.tabBarConfig()
	hit := tabbar.HitTest(cfg, viewX)
	if hit.IsLeftNav {
		m.tabArrowFlashLeftUntil = time.Now().Add(tabArrowFlashDuration)
		return true, m.prevTab()
	}
	if hit.IsRightNav {
		m.tabArrowFlashRightUntil = time.Now().Add(tabArrowFlashDuration)
		return true, m.nextTab()
	}
	if hit.TabIndex < 0 || hit.TabIndex >= len(m.focusedTabOrder()) {
		return true, nil
	}
	if hit.IsClose {
		return true, m.closeTab(hit.TabIndex)
	}
	// Begin drag and immediately switch to the pressed tab.
	m.tabDragIdx = hit.TabIndex
	m.tabDragSourcePane = m.focusedPane
	return true, m.switchToTab(hit.TabIndex)
}

// handleTabDragReorder performs intra-pane tab reordering during a drag.
func (m *AppModel) handleTabDragReorder(localX int) (bool, tea.Cmd) {
	ps := m.paneEditors[m.tabDragSourcePane]
	if ps == nil {
		return true, nil
	}
	r, hasPaneRect := m.focusedPaneRect()
	var cfg tabbar.Config
	if hasPaneRect {
		cfg = m.paneTabBarConfig(m.tabDragSourcePane, r.W)
	} else {
		cfg = m.tabBarConfig()
	}
	hit := tabbar.HitTest(cfg, localX)
	if hit.TabIndex >= 0 && hit.TabIndex != m.tabDragIdx && hit.TabIndex < len(ps.tabOrder) {
		ps.tabOrder[m.tabDragIdx], ps.tabOrder[hit.TabIndex] = ps.tabOrder[hit.TabIndex], ps.tabOrder[m.tabDragIdx]
		m.tabDragIdx = hit.TabIndex
	}
	return true, nil
}

// updateTabHoverClose updates the tab close-icon hover state from a motion event.
// Handles multi-pane, split preview, and full-preview modes.
func (m *AppModel) updateTabHoverClose(mouse tea.MouseMsg) {
	// Save tooltip state — restored if the mouse is still inside the tooltip.
	savedTooltipTab := m.mdTooltipTab
	savedTooltipPane := m.mdTooltipPane
	savedTooltipX := m.mdTooltipX

	m.tabHoverClose = -1
	m.tabHoverPane = 0
	m.previewTabHoverClose = -1
	m.mdPreviewTabHoverClose = -1
	m.mdTooltipTab = -1
	m.mdTooltipPane = 0

	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return
	}

	// Multi-pane mode: use tree-based hit testing.
	if m.paneTree != nil && !m.paneTree.IsLeaf() {
		pid, localX, localY, ok := m.paneViewCoords(mouse.X, mouse.Y)
		if !ok {
			return
		}
		// Mouse is below the tab bar — check if it's inside the tooltip.
		if localY >= tabbar.Height {
			if savedTooltipTab >= 0 && savedTooltipPane == pid &&
				localY < tabbar.Height+mdTooltipHeight &&
				localX >= savedTooltipX && localX < savedTooltipX+mdTooltipWidth {
				m.mdTooltipTab = savedTooltipTab
				m.mdTooltipPane = savedTooltipPane
				m.mdTooltipX = savedTooltipX
			}
			return
		}
		if pid == m.previewPane {
			area := m.paneContentArea()
			rects := m.paneTree.ComputeLayout(area)
			r := rects[pid]
			cfg := tabbar.Config{
				Tabs: []tabbar.Tab{{
					Path:        m.previewPanel.FilePath(),
					Modified:    false,
					LabelPrefix: "Preview: ",
				}},
				Active:    0,
				Width:     r.W,
				NerdFonts: m.nerdFontsDetected,
				Theme:     m.config.Theme(),
			}
			hit := tabbar.HitTest(cfg, localX)
			if hit.IsClose {
				m.previewTabHoverClose = hit.TabIndex
			}
			return
		}
		if pid == m.mdPreviewPane {
			area := m.paneContentArea()
			rects := m.paneTree.ComputeLayout(area)
			r := rects[pid]
			cfg := tabbar.Config{
				Tabs: []tabbar.Tab{{
					Path:        m.mdPreviewPanel.FilePath(),
					Modified:    false,
					LabelPrefix: "Markdown: ",
				}},
				Active:    0,
				Width:     r.W,
				NerdFonts: m.nerdFontsDetected,
				Theme:     m.config.Theme(),
			}
			hit := tabbar.HitTest(cfg, localX)
			if hit.IsClose {
				m.mdPreviewTabHoverClose = hit.TabIndex
			}
			return
		}
		ps, exists := m.paneEditors[pid]
		if !exists || len(ps.tabOrder) == 0 {
			return
		}
		area := m.paneContentArea()
		rects := m.paneTree.ComputeLayout(area)
		r := rects[pid]
		cfg := m.paneTabBarConfig(pid, r.W)
		hit := tabbar.HitTest(cfg, localX)
		if hit.IsClose {
			m.tabHoverPane = pid
			m.tabHoverClose = hit.TabIndex
		}
		m.updateMdTooltip(pid, hit, cfg)
		return
	}

	viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
	if viewY >= tabbar.Height {
		// Preserve tooltip if mouse is inside its bounds.
		if savedTooltipTab >= 0 && savedTooltipPane == m.focusedPane &&
			viewY < m.tabBarHeight()+mdTooltipHeight &&
			viewX >= savedTooltipX && viewX < savedTooltipX+mdTooltipWidth {
			m.mdTooltipTab = savedTooltipTab
			m.mdTooltipPane = savedTooltipPane
			m.mdTooltipX = savedTooltipX
		}
		return
	}

	// Full preview mode: entire code panel is preview.
	if m.hasPreview() && m.viewMode == ViewEdit && len(m.focusedTabOrder()) == 0 {
		hit := tabbar.HitTest(m.previewTabBarConfig(), viewX)
		if hit.IsClose {
			m.previewTabHoverClose = hit.TabIndex
		}
		return
	}

	// Split mode: preview on left half.
	if m.hasPreview() && m.isInsidePreviewHalf(mouse.X) {
		hit := tabbar.HitTest(m.previewTabBarConfig(), viewX)
		if hit.IsClose {
			m.previewTabHoverClose = hit.TabIndex
		}
		return
	}

	// Editor tab bar (right half in split mode, or full panel without preview).
	if m.tabBarHeight() == 0 {
		return
	}
	editorX := viewX
	if pw := m.previewSplitWidth(); pw > 0 {
		editorX -= pw + 1 // offset past preview + divider
	}
	cfg := m.tabBarConfig()
	hit := tabbar.HitTest(cfg, editorX)
	if hit.IsClose {
		m.tabHoverPane = m.focusedPane
		m.tabHoverClose = hit.TabIndex
	}
	m.updateMdTooltip(m.focusedPane, hit, cfg)
}

// updateMdTooltip sets the markdown tooltip state when hovering a markdown
// file tab (not the close icon).
func (m *AppModel) updateMdTooltip(pid pane.PaneID, hit tabbar.HitResult, cfg tabbar.Config) {
	if hit.TabIndex < 0 || hit.IsClose {
		return
	}
	ps := m.paneEditors[pid]
	if ps == nil || hit.TabIndex >= len(ps.tabOrder) {
		return
	}
	path := ps.tabOrder[hit.TabIndex]
	if !isMarkdownFile(path) {
		return
	}
	startX, _ := tabbar.TabBounds(cfg, hit.TabIndex)
	m.mdTooltipTab = hit.TabIndex
	m.mdTooltipPane = pid
	m.mdTooltipX = startX
}

// mdTooltipRows returns the 3 rows (top border, content, bottom border) of
// the markdown "Preview" tooltip popup.
func (m *AppModel) mdTooltipRows() [3]string {
	th := m.config.Theme()
	border := lipgloss.NewStyle().
		Background(th.Palette.PopupBg).
		Foreground(th.Palette.Border)
	icon := lipgloss.NewStyle().
		Background(th.Palette.PopupBg).
		Foreground(th.Palette.Accent)
	label := lipgloss.NewStyle().
		Background(th.Palette.PopupBg).
		Foreground(th.Palette.Primary).
		Bold(true)
	pad := lipgloss.NewStyle().
		Background(th.Palette.PopupBg)

	inner := mdTooltipWidth - 2 // subtract left+right border columns
	topBot := border.Render("╭") + border.Render(strings.Repeat("─", inner)) + border.Render("╮")
	bottom := border.Render("╰") + border.Render(strings.Repeat("─", inner)) + border.Render("╯")
	content := border.Render("│") + pad.Render(" ") + icon.Render("◇") + label.Render("  Preview ") + pad.Render(" ") + border.Render("│")
	return [3]string{topBot, content, bottom}
}

// mdTooltipWidth is the visual width of the tooltip.
// Derived from: "│" (1) + " " (1) + "◇" (1) + "  Preview " (10) + " " (1) + "│" (1) = 15.
const mdTooltipWidth = 15

// mdTooltipHeight is the number of rows the tooltip occupies.
const mdTooltipHeight = 3

// overlayMdTooltipRows splices the 3-row tooltip popup into content lines
// below the hovered markdown tab. dividerRow is the tab bar divider row index.
func overlayMdTooltipRows(lines []string, rows [3]string, col, dividerRow int) {
	startRow := dividerRow + 1
	for i, tooltipRow := range rows {
		row := startRow + i
		if row < 0 || row >= len(lines) {
			continue
		}
		orig := lines[row]
		origW := lipgloss.Width(orig)
		if col >= origW {
			continue
		}
		left := truncateStyledN(orig, col)
		right := skipStyledN(orig, col+mdTooltipWidth)
		lines[row] = left + tooltipRow + right
	}
}

// updateFileTreeTabHover updates the tab close-icon hover state in the
// file tree's tabs panel from a mouse motion event.
func (m *AppModel) updateFileTreeTabHover(mouse tea.MouseMsg) {
	if !m.fileTree.InTabsMode() || !m.isFileTreeVisible() {
		m.fileTree.ClearTabCloseHover()
		return
	}
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	if treeW == 0 || treeH == 0 {
		m.fileTree.ClearTabCloseHover()
		return
	}
	treeX := m.fileTreePanelX()
	innerH := max(treeH-panelBorderSize, 0)
	contentLeft := treeX + 1
	contentRight := treeX + treeW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if mouse.X < contentLeft || mouse.X >= contentRight ||
		mouse.Y < contentTop || mouse.Y >= contentBottom {
		m.fileTree.ClearTabCloseHover()
		return
	}
	viewX := mouse.X - contentLeft
	viewY := mouse.Y - contentTop
	m.fileTree.HoverAt(viewX, viewY)
}

// tabBarConfig builds the tabbar.Config for the current state.
func (m *AppModel) tabBarConfig() tabbar.Config {
	rightW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)

	// In split mode the editor tab bar occupies only the right half.
	barWidth := innerW
	if m.hasPreview() && m.viewMode == ViewEdit && len(m.focusedTabOrder()) > 0 {
		dividerWidth := 1
		previewW := (innerW - dividerWidth) / 2
		barWidth = innerW - dividerWidth - previewW
	}

	tabs := make([]tabbar.Tab, len(m.focusedTabOrder()))
	currentPath := m.focusedEditor().FilePath()
	activeIdx := 0
	for i, path := range m.focusedTabOrder() {
		modified := false
		if path == currentPath {
			activeIdx = i
			modified = m.focusedEditor().Modified()
		} else {
			modified = m.editorCache.IsModified(path)
		}
		tabs[i] = tabbar.Tab{Path: path, Modified: modified}
	}

	return tabbar.Config{
		Tabs:       tabs,
		Active:     activeIdx,
		Width:      barWidth,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      m.config.Theme(),
		FlashLeft:  time.Now().Before(m.tabArrowFlashLeftUntil),
		FlashRight: time.Now().Before(m.tabArrowFlashRightUntil),
		HoverClose: m.tabHoverClose,
		Focused:    m.hasPreview() && m.isEditorFocused(),
		DimActive:  m.hasPreview() && !m.isEditorFocused(),
	}
}

// tabBarHeight returns the tab bar height (tabs + divider) when visible, 0 otherwise.
func (m *AppModel) tabBarHeight() int {
	if len(m.focusedTabOrder()) > 0 {
		return tabbar.Height
	}
	return 0
}

// resizeInlineEditor re-applies the correct size to the inline editor,
// accounting for the current tab bar visibility and preview split.
// Call after any tab mutation.
func (m *AppModel) resizeInlineEditor() {
	if m.viewMode != ViewEdit {
		return
	}
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	// Delegate to resizeCodePanelForPreview which handles split-mode sizing.
	m.resizeCodePanelForPreview(rightW, rightH)
}

// switchToTab switches the editor to the tab at the given index.
func (m *AppModel) switchToTab(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.focusedTabOrder()) {
		return nil
	}
	target := m.focusedTabOrder()[idx]
	if target == m.focusedEditor().FilePath() {
		return nil
	}
	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     target,
			Name:     filepath.Base(target),
			Language: detectEditorLanguage(target),
		}
	}
}

// ---------------------------------------------------------------------------
// Warp point methods
// ---------------------------------------------------------------------------

// setWarpPoint stores the current editor position as warp point idx (0-indexed).
func (m *AppModel) setWarpPoint(idx int) {
	fp := m.focusedEditor().FilePath()
	if fp == "" {
		return
	}
	line := m.focusedEditor().CursorLine()
	col := m.focusedEditor().CursorCol()
	startCol, endCol := m.focusedEditor().WordBoundsAt(line, col)
	if startCol == endCol {
		// Cursor not on a word — highlight just the single character.
		endCol = col + 1
		startCol = col
	}
	m.warpPoints[idx] = &WarpPoint{
		Path:     fp,
		Line:     line,
		Col:      col,
		StartCol: startCol,
		EndCol:   endCol,
	}
	m.syncEditorWarpLines()
}

// clearWarpPoint removes warp point idx (0-indexed).
func (m *AppModel) clearWarpPoint(idx int) {
	m.warpPoints[idx] = nil
	m.syncEditorWarpLines()
}

// teleportToWarp navigates to the warp point at idx via FileOpenMsg.
func (m *AppModel) teleportToWarp(idx int) tea.Cmd {
	wp := m.warpPoints[idx]
	if wp == nil {
		return nil
	}
	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:      wp.Path,
			Name:      filepath.Base(wp.Path),
			Language:  detectEditorLanguage(wp.Path),
			Line:      wp.Line + 1, // FileOpenMsg uses 1-based line.
			CursorCol: wp.Col,
		}
	}
}

// syncEditorWarpLines updates the editor's warp line indicators to reflect
// the current warp points for the active file.
func (m *AppModel) syncEditorWarpLines() {
	fp := m.focusedEditor().FilePath()
	if fp == "" {
		m.focusedEditor().SetWarpLines(nil)
		return
	}
	var warpMap map[int]editor.WarpLineInfo
	for i, wp := range m.warpPoints {
		if wp == nil || wp.Path != fp {
			continue
		}
		if warpMap == nil {
			warpMap = make(map[int]editor.WarpLineInfo)
		}
		warpMap[wp.Line] = editor.WarpLineInfo{
			Slot:     i + 1, // 1-indexed slot number for display.
			StartCol: wp.StartCol,
			EndCol:   wp.EndCol,
		}
	}
	m.focusedEditor().SetWarpLines(warpMap)
}

// nextTab switches to the next tab (wraps around).
func (m *AppModel) nextTab() tea.Cmd {
	if len(m.focusedTabOrder()) < 2 {
		return nil
	}
	idx := (m.activeTabIndex() + 1) % len(m.focusedTabOrder())
	return m.switchToTab(idx)
}

// prevTab switches to the previous tab (wraps around).
func (m *AppModel) prevTab() tea.Cmd {
	if len(m.focusedTabOrder()) < 2 {
		return nil
	}
	idx := (m.activeTabIndex() - 1 + len(m.focusedTabOrder())) % len(m.focusedTabOrder())
	return m.switchToTab(idx)
}

// tabNavRight navigates to the next tab. When the active tab is at the
// right edge of the visible window, it page-jumps to the first tab on
// the next visible page instead of stepping by one.
func (m *AppModel) tabNavRight() tea.Cmd {
	n := len(m.focusedTabOrder())
	if n < 2 {
		return nil
	}
	active := m.activeTabIndex()
	cfg := m.tabBarConfig()
	_, hi := tabbar.VisibleRange(cfg)

	// All tabs visible or not at right edge: simple step.
	if hi >= n-1 || active < hi {
		return m.switchToTab((active + 1) % n)
	}

	// At right edge of overflow window — page forward.
	// Simulate centering on hi+1 to find the new visible page.
	cfg.Active = hi + 1
	newLo, _ := tabbar.VisibleRange(cfg)
	return m.switchToTab(newLo)
}

// tabNavLeft navigates to the previous tab. When the active tab is at the
// left edge of the visible window, it page-jumps to the last tab on the
// previous visible page instead of stepping by one.
func (m *AppModel) tabNavLeft() tea.Cmd {
	n := len(m.focusedTabOrder())
	if n < 2 {
		return nil
	}
	active := m.activeTabIndex()
	cfg := m.tabBarConfig()
	lo, _ := tabbar.VisibleRange(cfg)

	// All tabs visible or not at left edge: simple step.
	if lo <= 0 || active > lo {
		return m.switchToTab((active - 1 + n) % n)
	}

	// At left edge of overflow window — page backward.
	// Simulate centering on lo-1 to find the new visible page.
	cfg.Active = lo - 1
	_, newHi := tabbar.VisibleRange(cfg)
	return m.switchToTab(newHi)
}

// toggleTabsPanel toggles the tabs list view in the file tree panel.
// When entering, saves current focus and focuses the file tree.
// When exiting, restores the previously focused panel.
func (m *AppModel) toggleTabsPanel() tea.Cmd {
	if m.fileTree.InTabsMode() {
		m.fileTree.ExitTabs()
		m.focus.SetFocus(m.preTabsFocus)
		m.syncFocusState()
		return nil
	}
	m.preTabsFocus = m.focus.Current()
	m.fileTree.SetTabs(m.focusedTabOrder(), m.focusedEditor().FilePath(), m.tabModifiedSet())
	if !m.isFileTreeVisible() {
		m.leftRing.setTo(component.FocusFileTree)
	}
	m.focus.SetFocus(component.FocusFileTree)
	m.syncFocusState()
	return nil
}

// handleSavePromptKey processes y/n/esc while the save-before-close prompt
// is active. All other keys are swallowed until the prompt is resolved.
func (m *AppModel) handleSavePromptKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "y":
		m.statusBar.ClearPrompt()
		m.pendingClosePrompt = false
		if pid := m.pendingPaneClose; pid != 0 {
			m.pendingPaneClose = 0
			m.saveEditorBuffer()
			m.closePaneForce(pid)
			return m, nil
		}
		m.saveEditorBuffer()
		return m, m.closeCurrentTab(true)
	case "n":
		m.statusBar.ClearPrompt()
		m.pendingClosePrompt = false
		if pid := m.pendingPaneClose; pid != 0 {
			m.pendingPaneClose = 0
			m.closePaneForce(pid)
			return m, nil
		}
		return m, m.closeCurrentTab(true)
	case "esc":
		m.statusBar.ClearPrompt()
		m.pendingClosePrompt = false
		m.pendingPaneClose = 0
		return m, nil
	default:
		return m, nil
	}
}

// closeCurrentTab closes the current tab and switches to an adjacent one.
// If it was the last tab, clears the editor to show the placeholder.
func (m *AppModel) closeCurrentTab(discard bool) tea.Cmd {
	if !discard && m.focusedEditor().Modified() {
		m.pendingClosePrompt = true
		name := filepath.Base(m.focusedEditor().FilePath())
		m.statusBar.SetPrompt("Save changes to " + name + "? (y)es (n)o")
		return nil
	}

	path := m.focusedEditor().FilePath()
	idx := m.activeTabIndex()

	m.removeTab(path)
	m.editorCache.Delete(path)

	// Clear the editor so the subsequent detachCurrentEditor (in
	// handleFileOpen) sees an empty file path and skips re-caching
	// the discarded state.
	if discard {
		m.focusedEditor().ClearFile()
	}

	// Last tab closed — show placeholder, stay in edit mode.
	if len(m.focusedTabOrder()) == 0 {
		m.focusedEditor().ClearFile()
		m.resizeInlineEditor()
		m.refreshTabsPanel()
		// In split mode, transition focus to the preview sub-panel
		// since the editor sub-panel no longer has content.
		if m.hasPreview() {
			m.focus.SetFocus(pane.PaneFocusID(m.previewPane))
			m.syncFocusState()
		}
		return nil
	}

	// Resize in case tab bar visibility changed.
	m.resizeInlineEditor()
	m.refreshTabsPanel()

	// Switch to adjacent tab: prefer same index (right), fallback left.
	next := min(idx, len(m.focusedTabOrder())-1)
	return m.switchToTab(next)
}

// closeTab closes the tab at the given index. If it's the active tab, behaves
// like closeCurrentTab(false). If it's a background tab, removes it silently.
func (m *AppModel) closeTab(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.focusedTabOrder()) {
		return nil
	}

	path := m.focusedTabOrder()[idx]
	activeIdx := m.activeTabIndex()

	// If closing the active tab, check for unsaved changes.
	if idx == activeIdx {
		return m.closeCurrentTab(false)
	}

	// Background tab with unsaved changes: switch to it and prompt.
	if m.editorCache.IsModified(path) {
		cmd := m.switchToTab(idx)
		m.pendingClosePrompt = true
		name := filepath.Base(path)
		m.statusBar.SetPrompt("Save changes to " + name + "? (y)es (n)o")
		return cmd
	}

	m.removeTab(path)
	m.editorCache.Delete(path)
	m.resizeInlineEditor()
	m.refreshTabsPanel()
	return nil
}

// closeTabByPath finds a tab by path and closes it.
func (m *AppModel) closeTabByPath(path string) tea.Cmd {
	for i, p := range m.focusedTabOrder() {
		if p == path {
			return m.closeTab(i)
		}
	}
	return nil
}

// refreshTabsPanel updates the file tree's tabs panel if it is currently
// visible, keeping it in sync with the app's tabOrder.
func (m *AppModel) refreshTabsPanel() {
	if m.fileTree.InTabsMode() {
		m.fileTree.SetTabs(m.focusedTabOrder(), m.focusedEditor().FilePath(), m.tabModifiedSet())
	}
}

// refreshTabsModified updates the modified indicators in the tabs panel
// without resetting cursor, scroll, or filter state.
func (m *AppModel) refreshTabsModified() {
	if m.fileTree.InTabsMode() {
		m.fileTree.UpdateTabModified(m.tabModifiedSet())
	}
}

// tabModifiedSet returns a set of paths with unsaved changes.
func (m *AppModel) tabModifiedSet() map[string]bool {
	currentPath := m.focusedEditor().FilePath()
	mod := make(map[string]bool, len(m.focusedTabOrder()))
	for _, path := range m.focusedTabOrder() {
		if path == currentPath {
			if m.focusedEditor().Modified() {
				mod[path] = true
			}
		} else if m.editorCache.IsModified(path) {
			mod[path] = true
		}
	}
	return mod
}

// ---------------------------------------------------------------------------
// Spring-based scroll
// ---------------------------------------------------------------------------

// scrollFPS is the simulation frame rate for the spring.
// Derived from: tickFastInterval (16ms) ≈ 60 FPS.
const scrollFPS = 60

// scrollFrequency is the spring's angular frequency (rad/s).
// Higher values produce snappier response; lower values produce smoother easing.
// Derived from: settling in ~8 frames at 60fps ≈ 130ms of visible easing.
const scrollFrequency = 30.0

// scrollDamping is the spring's damping ratio.
// 1.0 = critically damped (fastest approach without overshoot).
// Derived from: critically damped to prevent scroll bounce-back.
const scrollDamping = 1.0

// scrollImpulse is the target displacement per mouse wheel tick.
// Derived from: 3 lines per detent, matching common editor defaults.
const scrollImpulse = 3.0

// scrollKick is the velocity boost per unit of impulse.
// Each wheel event adds impulse * scrollKick to the spring velocity,
// so the first frame crosses an integer boundary immediately.
// Derived from: v₀ = 3 × 15 = 45; with ω=30, B = v₀ + ω(x₀−x_eq) = 45−90 = −45 < 0,
// guaranteeing monotonic approach without overshoot.
const scrollKick = 15.0

// scrollMaxLead is the maximum distance the target can lead the current position.
// Prevents runaway accumulation from rapid scrolling.
// Derived from: comfortable coast of ~1 second ≈ 30 lines.
const scrollMaxLead = 30.0

// scrollSettlePos is the position threshold for considering the spring settled.
// Derived from: less than half a rendered line of residual error.
const scrollSettlePos = 0.01

// scrollSettleVel is the velocity threshold for considering the spring settled.
// Derived from: negligible motion per frame at 60fps.
const scrollSettleVel = 0.01

// ---------------------------------------------------------------------------
// Bounce-back spring (overscroll rubber band)
// ---------------------------------------------------------------------------

// bounceDamping is the bounce spring's damping ratio.
// < 1.0 = underdamped: overshoots equilibrium, producing visible oscillation.
// Derived from: 0.5 produces ~1 visible bounce-back. Low enough for a
// visible elastic effect, high enough that the tail doesn't oscillate
// through integer rounding boundaries (which causes flicker).
const bounceDamping = 0.5

// bounceFrequency is the bounce spring's angular frequency (rad/s).
// Derived from: at 60fps with damping=0.5, frequency=14.5 settles in
// ~22 frames (~375ms). omega_d = 14.5 * sqrt(1 - 0.25) = 12.56 rad/s.
const bounceFrequency = 14.5

// bounceImpulse is the velocity added to the bounce spring per boundary hit.
// Derived from: peak displacement ≈ impulse / omega_d = 6.0 / 14.21 ≈ 0.42
// lines per hit. With 3-5 rapid hits per gesture, accumulates to ~1.3-2.1 lines.
const bounceImpulse = 6.0

// bounceMaxLines is the maximum visual displacement in lines.
// Derived from: 2 lines keeps content visually stable at the boundary while
// still producing a noticeable rubber-band effect.
const bounceMaxLines = 2.0

// bounceMaxVel is the maximum velocity the bounce spring can accumulate.
// Derived from: bounceMaxLines * omega_d = 2.0 * 12.56 ≈ 25.0, ensuring
// peak displacement never exceeds bounceMaxLines.
const bounceMaxVel = 25.0

// bounceSettleThreshold is the combined position+velocity threshold for
// considering the bounce settled.
// Derived from: less than 1% of a rendered line, letting the spring's
// natural exponential decay fully ease the bounce to rest.
const bounceSettleThreshold = 0.01

// ---------------------------------------------------------------------------
// Horizontal swipe (ring cycling)
// ---------------------------------------------------------------------------

// swipeThreshold is the accumulated horizontal scroll ticks required to
// trigger a ring cycle.
// Derived from: 6 ticks requires a deliberate gesture (trackpads emit
// 10-20 events per swipe), preventing accidental triggers.
const swipeThreshold = 6.0

// swipeDecay is the duration after which stale swipe accumulation resets.
// Derived from: 300ms is long enough for a continuous trackpad gesture
// but short enough that separate flicks don't combine.
const swipeDecay = 300 * time.Millisecond

// swipeCooldown is the dead period after a cycle fires during which
// further swipe events are ignored. Prevents the tail end of a single
// scroll gesture from immediately triggering a second cycle.
// Derived from: 500ms exceeds a typical trackpad gesture duration (~300ms)
// while feeling responsive for intentional repeated swipes.
const swipeCooldown = 500 * time.Millisecond

// handleMouse routes mouse wheel events to the panel under the cursor
// using spring-based scrolling, handles click-to-copy on chat messages,
// and handles text selection in the code panel.
// Mouse events are consumed when a full-screen overlay is active.
func (m *AppModel) handleMouse(mouse tea.MouseMsg) tea.Cmd {
	if m.overlay == overlaySearch && m.searchOverlay.Visible() {
		switch mouse.Button {
		case tea.MouseButtonWheelUp:
			m.searchOverlay.ScrollUp()
		case tea.MouseButtonWheelDown:
			m.searchOverlay.ScrollDown()
		}
		return nil
	}
	if m.overlay == overlayFieldManual && m.fieldManualOverlay.Visible() {
		switch mouse.Button {
		case tea.MouseButtonWheelUp:
			m.fieldManualOverlay.ScrollUp()
		case tea.MouseButtonWheelDown:
			m.fieldManualOverlay.ScrollDown()
		}
		return nil
	}
	if m.overlay == overlayModal {
		return nil
	}
	if m.overlay == overlayLogin && m.loginPanel.Active() {
		// Content starts at Y=1 (top border), X=1 (left border).
		localX := mouse.X - 1
		localY := mouse.Y - 1
		if mouse.Action == tea.MouseActionMotion && localX >= 0 && localY >= 0 {
			m.loginPanel.HandleMouseMotion(localX, localY)
			return nil
		}
		if mouse.Button == tea.MouseButtonLeft && mouse.Action == tea.MouseActionPress {
			if localX >= 0 && localY >= 0 {
				done, result, cmd := m.loginPanel.HandleClick(localX, localY)
				if done {
					return tea.Batch(cmd, m.handleLoginPanelResult(result))
				}
				return cmd
			}
		}
		return nil
	}

	// Route wheel events to the hover popup when it is active and the
	// cursor is over it, instead of scrolling the underlying panel.
	if m.viewMode == ViewEdit && m.focusedEditor().HoverActive() && isWheelEvent(mouse) {
		if m.isInsideCodePanel(mouse.X, mouse.Y) {
			// Check preview hover popup scroll first.
			if m.previewPanel.HoverActive() {
				pvx, pvy := m.previewPaneLocalCoords(mouse.X, mouse.Y)
				pvy -= tabbar.Height
				if m.previewPanel.IsInsideHoverPopup(pvx, pvy) {
					switch mouse.Button {
					case tea.MouseButtonWheelUp:
						m.previewPanel.ScrollHoverUp()
					case tea.MouseButtonWheelDown:
						m.previewPanel.ScrollHoverDown()
					}
					return nil
				}
			}

			vx, vy := m.focusedPaneLocalCoords(mouse.X, mouse.Y)
			if len(m.focusedTabOrder()) > 0 {
				vy -= tabbar.Height
			}
			vy -= m.focusedEditor().FindBarHeight()
			if m.focusedEditor().IsInsideHoverPopup(vx, vy) {
				switch mouse.Button {
				case tea.MouseButtonWheelUp:
					m.focusedEditor().ScrollHoverUp()
				case tea.MouseButtonWheelDown:
					m.focusedEditor().ScrollHoverDown()
				}
				return nil
			}
		}
	}

	// Handle motion events for hover before button-specific routing.
	// Some terminals send MouseButtonLeft for motion after a click-release
	// instead of MouseButtonNone. Routing hover here ensures it works
	// regardless of reported button, as long as we're not mid-drag.
	if m.viewMode == ViewEdit && mouse.Action == tea.MouseActionMotion && !m.editorMouseDown && m.tabDragIdx < 0 {
		m.updateTabHoverClose(mouse)
		m.updateFileTreeTabHover(mouse)
		return m.handleEditorMouseHover(mouse)
	}

	// Git mode: track divider hover for resize cursor feedback.
	// Skip when diff view is active — the left panel shows the file list.
	if m.viewMode == ViewGit && !m.diffViewActive && m.gitPanel != nil && mouse.Action == tea.MouseActionMotion && !m.gitPanel.IsDragging() {
		panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
		panelX := m.fileTreePanelX()
		contentLeft := panelX + 1
		contentRight := panelX + panelW - 1
		contentTop := 1
		contentBottom := 1 + max(panelH-panelBorderSize, 0)
		if mouse.X >= contentLeft && mouse.X < contentRight &&
			mouse.Y >= contentTop && mouse.Y < contentBottom {
			viewX := mouse.X - contentLeft
			viewY := mouse.Y - contentTop
			m.gitPanel.HandleMouseHover(viewX, viewY)
		} else {
			m.gitPanel.ClearHover()
		}
	}

	// Merge diff view: forward motion events for toolbar hover tracking.
	if m.mergeDiffViewActive && m.mergeDiffView != nil && mouse.Action == tea.MouseActionMotion {
		if m.isInsideCodePanel(mouse.X, mouse.Y) {
			panelX := m.codePanelX()
			viewX := mouse.X - panelX - 1
			viewY := mouse.Y - 1
			localMouse := tea.MouseMsg{
				X: viewX, Y: viewY,
				Action: mouse.Action, Button: mouse.Button,
			}
			m.mergeDiffView.Update(localMouse)
		}
	}

	// Diff view: forward motion events for toolbar hover tracking.
	if m.diffViewActive && m.diffView != nil && mouse.Action == tea.MouseActionMotion {
		if m.isInsideCodePanel(mouse.X, mouse.Y) {
			panelX := m.codePanelX()
			viewX := mouse.X - panelX - 1
			viewY := mouse.Y - 1
			localMouse := tea.MouseMsg{
				X: viewX, Y: viewY,
				Action: mouse.Action, Button: mouse.Button,
			}
			m.diffView.Update(localMouse)
		}
	}

	// Conflict view: forward motion events for toolbar hover tracking.
	if m.conflictViewActive && m.conflictView != nil && mouse.Action == tea.MouseActionMotion {
		if m.isInsideCodePanel(mouse.X, mouse.Y) {
			panelX := m.codePanelX()
			viewX := mouse.X - panelX - 1
			viewY := mouse.Y - 1
			localMouse := tea.MouseMsg{
				X: viewX, Y: viewY,
				Action: mouse.Action, Button: mouse.Button,
			}
			m.conflictView.Update(localMouse)
		}
	}

	// Git mode: track toolbar button hover for commit tree panel.
	// Skip when diff/merge diff view is active — their toolbars handle hover directly.
	if m.viewMode == ViewGit && !m.diffViewActive && !m.mergeDiffViewActive && m.commitTree != nil && mouse.Action == tea.MouseActionMotion {
		panelW, panelH := m.layout.GetPanelSize(component.FocusCodeViewer)
		panelX := m.codePanelX()
		contentLeft := panelX + 1
		contentRight := panelX + panelW - 1
		contentTop := 1
		contentBottom := 1 + max(panelH-panelBorderSize, 0)
		if mouse.X >= contentLeft && mouse.X < contentRight &&
			mouse.Y >= contentTop && mouse.Y < contentBottom {
			viewX := mouse.X - contentLeft
			viewY := mouse.Y - contentTop
			m.commitTree.HandleToolbarHover(viewX, viewY)
		} else {
			m.commitTree.ClearHover()
		}
	}

	// Route wheel events to input when focused and scrollable.
	if m.focus.Current() == component.FocusInput && m.input.CanScroll() {
		inputTop := m.height - m.prevInputH - statusBarHeight
		if mouse.Y >= inputTop && mouse.Y < inputTop+m.prevInputH {
			switch mouse.Button {
			case tea.MouseButtonWheelUp:
				m.input.ScrollUp()
				return nil
			case tea.MouseButtonWheelDown:
				m.input.ScrollDown()
				return nil
			}
		}
	}

	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		if mouse.Alt {
			m.applySwipeImpulse(mouse.X, -1)
		} else {
			m.applyScrollImpulse(mouse.X, mouse.Y, -scrollImpulse)
		}
	case tea.MouseButtonWheelDown:
		if mouse.Alt {
			m.applySwipeImpulse(mouse.X, 1)
		} else {
			m.applyScrollImpulse(mouse.X, mouse.Y, scrollImpulse)
		}
	case tea.MouseButtonWheelLeft:
		m.applySwipeImpulse(mouse.X, -1)
	case tea.MouseButtonWheelRight:
		m.applySwipeImpulse(mouse.X, 1)
	case tea.MouseButtonLeft:
		return m.handleLeftClick(mouse)
	}
	return nil
}

// isWheelEvent reports whether the mouse event is a scroll wheel action.
func isWheelEvent(mouse tea.MouseMsg) bool {
	return mouse.Button == tea.MouseButtonWheelUp ||
		mouse.Button == tea.MouseButtonWheelDown
}

// handleLeftClick dispatches left-button events to the file tree,
// inline editor (in edit mode), and chat panels.
func (m *AppModel) handleLeftClick(mouse tea.MouseMsg) tea.Cmd {
	// Edit mode: handle press, drag, and release in the code panel.
	if m.viewMode == ViewEdit {
		if consumed, cmd := m.handleEditorMouse(mouse); consumed {
			return cmd
		}
	}

	// Git mode: forward motion/release during column drag operations.
	if m.viewMode == ViewGit && m.gitPanel != nil && m.gitPanel.IsDragging() {
		viewX := mouse.X - m.fileTreePanelX() - 1
		if mouse.Action == tea.MouseActionMotion {
			m.gitPanel.HandleMouseMotion(viewX)
			return nil
		}
		if mouse.Action == tea.MouseActionRelease {
			m.gitPanel.HandleMouseRelease()
			return nil
		}
	}

	// Input panel drag: finalize on release, extend selection on motion.
	if mouse.Action == tea.MouseActionRelease && m.inputMouseDown {
		m.inputMouseDown = false
		return nil
	}
	if mouse.Action == tea.MouseActionMotion && m.inputMouseDown {
		inputTop := m.height - m.prevInputH - statusBarHeight
		contentX := max(mouse.X-1, 0)
		contentY := max(mouse.Y-inputTop-1, 0)
		m.input.DragTo(contentX, contentY)
		return nil
	}

	if mouse.Action != tea.MouseActionPress {
		return nil
	}

	// Conflict view: route left-click events to file list and content pane.
	if m.conflictViewActive && m.conflictView != nil && mouse.Button == tea.MouseButtonLeft {
		m.handleConflictFileListClick(mouse.X, mouse.Y)
		if m.isInsideCodePanel(mouse.X, mouse.Y) {
			panelX := m.codePanelX()
			viewX := mouse.X - panelX - 1
			viewY := mouse.Y - 1
			localMouse := tea.MouseMsg{
				X: viewX, Y: viewY,
				Action: mouse.Action, Button: mouse.Button,
			}
			cmd := m.conflictView.Update(localMouse)
			m.focus.SetFocus(component.FocusConflictView)
			m.syncFocusState()
			return cmd
		}
		return nil
	}

	// Merge diff view: route left-click events to file list and panes.
	if m.mergeDiffViewActive && m.mergeDiffView != nil && mouse.Button == tea.MouseButtonLeft {
		m.handleMergeDiffFileListClick(mouse.X, mouse.Y)

		if m.isInsideCodePanel(mouse.X, mouse.Y) {
			panelX := m.codePanelX()
			viewX := mouse.X - panelX - 1
			viewY := mouse.Y - 1
			isPaneClick := m.mergeDiffView.IsPaneClick(viewY)
			localMouse := tea.MouseMsg{
				X: viewX, Y: viewY,
				Action: mouse.Action, Button: mouse.Button,
			}
			cmd := m.mergeDiffView.Update(localMouse)
			if isPaneClick {
				m.focus.SetFocus(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
				m.syncFocusState()
			}
			return cmd
		}
		return nil
	}

	// Diff view: route left-click events to the diff file list and diff panes.
	// Wheel events are routed via applyScrollImpulse → scrollOneLine.
	if m.diffViewActive && m.diffView != nil && mouse.Button == tea.MouseButtonLeft {
		// Diff file list (left panel, occupies the FileTree slot).
		m.handleDiffFileListClick(mouse.X, mouse.Y)

		// Diff panes / tab bar / toolbar (right panel).
		if m.isInsideCodePanel(mouse.X, mouse.Y) {
			panelX := m.codePanelX()
			viewX := mouse.X - panelX - 1
			viewY := mouse.Y - 1
			isPaneClick := m.diffView.IsPaneClick(viewY)
			localMouse := tea.MouseMsg{
				X: viewX, Y: viewY,
				Action: mouse.Action, Button: mouse.Button,
			}
			cmd := m.diffView.Update(localMouse)
			// Only update app-level pane focus for clicks in the pane content
			// area — tab bar and toolbar clicks should not steal pane focus.
			if isPaneClick {
				m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
				m.syncFocusState()
			}
			return cmd
		}
		return nil
	}

	// Git mode: route to git panel and commit tree.
	if m.viewMode == ViewGit {
		if cmd := m.handleGitPanelClick(mouse.X, mouse.Y); cmd != nil {
			return cmd
		}
		if cmd := m.handleCommitTreeClick(mouse.X, mouse.Y); cmd != nil {
			return cmd
		}
		return nil
	}

	// Input panel click: begin drag selection at click point.
	inputTop := m.height - m.prevInputH - statusBarHeight
	if mouse.Y >= inputTop && mouse.Y < inputTop+m.prevInputH {
		contentX := mouse.X - 1           // skip left border
		contentY := mouse.Y - inputTop - 1 // skip top border
		if contentX >= 0 && contentY >= 0 {
			m.input.DragStart(contentX, contentY)
			m.inputMouseDown = true
			m.focus.SetFocus(component.FocusInput)
			m.syncFocusState()
			return nil
		}
	}

	if cmd := m.handleFileTreeClick(mouse.X, mouse.Y); cmd != nil {
		return cmd
	}
	return m.handleChatClick(mouse.X, mouse.Y)
}

// panelForScroll resolves the panel under screen coordinate x, accounting
// for ring-swapped panels that PanelAtX doesn't know about.
// In SingleColumn mode PanelAtX has no candidates and always returns false,
// so we fall back to the active leftRing panel. In TwoColumn/ThreeColumn
// modes the fixed candidate IDs are mapped to the current ring occupant.
func (m *AppModel) panelForScroll(x, y int) (component.FocusID, bool) {
	// When merge diff view is active, route scrolls to its focused pane.
	if m.mergeDiffViewActive && m.mergeDiffView != nil && m.isInsideCodePanel(x, y) {
		return pane.PaneFocusID(m.mergeDiffView.FocusedPane()), true
	}
	// When diff view is active, route scrolls to the diff view's focused pane.
	if m.diffViewActive && m.diffView != nil && m.isInsideCodePanel(x, y) {
		return pane.PaneFocusID(m.diffView.FocusedPane()), true
	}
	// When conflict view is active, route code panel scrolls to the conflict view.
	if m.conflictViewActive && m.conflictView != nil && m.isInsideCodePanel(x, y) {
		return component.FocusConflictView, true
	}

	mode := m.layout.Mode()

	if mode == layout.SingleColumn {
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			if !m.leftRing.empty() && m.leftRing.current() == component.FocusFileTree {
				return component.FocusMergeDiffFileList, true
			}
			return component.FocusMergeDiffView, true
		}
		if m.diffViewActive && m.diffView != nil {
			// File-tree ring slot → diff file list; everything else → diff pane.
			if !m.leftRing.empty() && m.leftRing.current() == component.FocusFileTree {
				return component.FocusDiffFileList, true
			}
			return component.FocusDiffView, true
		}
		if m.conflictViewActive && m.conflictView != nil {
			if !m.leftRing.empty() && m.leftRing.current() == component.FocusFileTree {
				return component.FocusConflictFileList, true
			}
			return component.FocusConflictView, true
		}
		if m.leftRing.empty() {
			return 0, false
		}
		return m.leftRing.current(), true
	}

	panelID, ok := m.layout.PanelAtX(x)
	if !ok {
		return 0, false
	}

	resolved := m.resolveRingPanel(panelID, mode)

	// Merge diff view: route file-tree slot scrolls to the merge diff file list.
	if m.mergeDiffViewActive && m.mergeDiffView != nil && resolved == component.FocusFileTree {
		return component.FocusMergeDiffFileList, true
	}
	// Diff view: route file-tree slot scrolls to the diff file list.
	if m.diffViewActive && m.diffView != nil && resolved == component.FocusFileTree {
		return component.FocusDiffFileList, true
	}
	// Conflict view: route file-tree slot scrolls to the conflict file list.
	if m.conflictViewActive && m.conflictView != nil && resolved == component.FocusFileTree {
		return component.FocusConflictFileList, true
	}

	// In edit mode, route scrolls within the code panel to the pane
	// under the cursor using tree-based hit testing.
	if resolved == component.FocusCodeViewer && m.viewMode == ViewEdit && m.paneTree != nil {
		// Full markdown preview mode: all scrolls go to the mdPreview.
		if m.isFullMdPreview() {
			return pane.PaneFocusID(m.mdPreviewPane), true
		}
		if pid, ok := m.hitTestPane(x, y); ok {
			return pane.PaneFocusID(pid), true
		}
	}

	return resolved, true
}

// resolveRingPanel maps a fixed candidate panel ID to the actual panel
// currently showing in that slot due to ring cycling.
func (m *AppModel) resolveRingPanel(id component.FocusID, mode layout.LayoutMode) component.FocusID {
	switch mode {
	case layout.ThreeColumn:
		if id == component.FocusSessionPanel && !m.leftRing.empty() {
			return m.leftRing.current()
		}
	case layout.TwoColumn:
		if id == component.FocusSessionPanel && !m.leftRing.empty() {
			return m.leftRing.current()
		}
		if id == component.FocusChat && !m.rightRing.empty() {
			return m.rightRing.current()
		}
	}
	return id
}

// applyScrollImpulse adds displacement and velocity to the spring for the
// panel at screen coordinates (x, y). The velocity kick ensures the first
// frame crosses an integer line boundary, giving immediate visual feedback.
// Switching panels resets the spring to avoid cross-panel drift.
func (m *AppModel) applyScrollImpulse(x, y int, impulse float64) {
	panelID, ok := m.panelForScroll(x, y)
	if !ok {
		return
	}
	// In edit mode, scroll the inline editor instead of the code viewer.
	// The spring pipeline dispatches to editor.ScrollUp/ScrollDown via scrollOneLine.
	if panelID != m.scroll.panel {
		m.scroll = scrollState{panel: panelID}
		m.bounce = bounceState{panel: panelID}
	}
	m.scroll.target += impulse
	m.scroll.vel += impulse * scrollKick

	// Cap target lead to prevent runaway coast.
	m.scroll.target = clampFloat(m.scroll.target,
		m.scroll.pos-scrollMaxLead,
		m.scroll.pos+scrollMaxLead,
	)
	// Cap velocity to prevent overshoot (B ≤ 0 condition for critically damped).
	maxVel := scrollFrequency * math.Abs(m.scroll.target-m.scroll.pos)
	m.scroll.vel = clampFloat(m.scroll.vel, -maxVel, maxVel)
}

// tickScrollMomentum advances the scroll spring by one frame and applies
// resulting scroll lines. Boundary hits feed the bounce spring.
func (m *AppModel) tickScrollMomentum() {
	s := &m.scroll
	if !s.settled() {
		s.pos, s.vel = m.scrollSpring.Update(s.pos, s.vel, s.target)

		// Snap when close enough to prevent asymptotic crawl.
		if s.settled() {
			s.pos = s.target
			s.vel = 0
		}

		newApplied := int(math.Round(s.pos))
		m.applyScrollDelta(s.panel, newApplied-s.applied)
		s.applied = newApplied
	}

	m.tickBounce()

	// Push bounce offset to panels for rendering.
	m.chat.SetBounceOffset(m.bounceOffset(component.FocusChat))
	m.codePanel.SetBounceOffset(m.bounceOffset(component.FocusCodeViewer))
	// Push pane-specific bounce offsets.
	for id, ps := range m.paneEditors {
		ps.editor.SetBounceOffset(m.bounceOffset(pane.PaneFocusID(id)))
	}
	if m.previewPane != 0 {
		m.previewPanel.SetBounceOffset(m.bounceOffset(pane.PaneFocusID(m.previewPane)))
	}
	m.fileTree.SetBounceOffset(m.bounceOffset(component.FocusFileTree))
	if m.commitTree != nil {
		m.commitTree.SetBounceOffset(m.bounceOffset(component.FocusCommitTree))
	}
	if m.diffView != nil {
		// Diff pane bounce: match the dynamic pane-level ID from panelForScroll.
		paneBO := m.bounceOffset(pane.PaneFocusID(m.diffView.FocusedPane()))
		if paneBO == 0 {
			paneBO = m.bounceOffset(component.FocusDiffView)
		}
		m.diffView.SetBounceOffset(paneBO)
		// File list bounce: separate from pane bounce.
		m.diffView.SetFileListBounceOffset(m.bounceOffset(component.FocusDiffFileList))
	}
	if m.mergeDiffView != nil {
		paneBO := m.bounceOffset(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
		if paneBO == 0 {
			paneBO = m.bounceOffset(component.FocusMergeDiffView)
		}
		m.mergeDiffView.SetBounceOffset(paneBO)
		m.mergeDiffView.SetFileListBounceOffset(m.bounceOffset(component.FocusMergeDiffFileList))
	}
	if m.conflictView != nil {
		m.conflictView.SetBounceOffset(m.bounceOffset(component.FocusConflictView))
		m.conflictView.SetFileListBounceOffset(m.bounceOffset(component.FocusConflictFileList))
	}
}

// settled reports whether the spring is close enough to target to stop.
func (s *scrollState) settled() bool {
	return math.Abs(s.pos-s.target) < scrollSettlePos &&
		math.Abs(s.vel) < scrollSettleVel
}

// applyScrollDelta dispatches individual line scrolls. When a boundary is
// hit, the remaining delta is converted into bounce impulse.
func (m *AppModel) applyScrollDelta(panelID component.FocusID, delta int) {
	direction := 1
	if delta < 0 {
		direction = -1
		delta = -delta
	}
	for range delta {
		if !m.scrollOneLine(panelID, direction) {
			m.applyBounceImpulse(panelID, direction)
		}
	}
}

// scrollOneLine scrolls the identified panel by one line in the given direction.
// Returns true if the scroll was consumed, false if the panel hit a boundary.
func (m *AppModel) scrollOneLine(panelID component.FocusID, direction int) bool {
	switch panelID {
	case component.FocusChat:
		if direction < 0 {
			return m.chat.ScrollUp()
		}
		return m.chat.ScrollDown()
	case component.FocusConflictFileList:
		if m.conflictViewActive && m.conflictView != nil {
			if direction < 0 {
				return m.conflictView.ScrollFileListUp()
			}
			return m.conflictView.ScrollFileListDown()
		}
		return true
	case component.FocusConflictView:
		if m.conflictViewActive && m.conflictView != nil {
			if direction < 0 {
				return m.conflictView.ScrollUp()
			}
			return m.conflictView.ScrollDown()
		}
		return true
	case component.FocusMergeDiffFileList:
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			if direction < 0 {
				return m.mergeDiffView.ScrollFileListUp()
			}
			return m.mergeDiffView.ScrollFileListDown()
		}
		return true
	case component.FocusMergeDiffView:
		if m.mergeDiffView != nil {
			if direction < 0 {
				return m.mergeDiffView.ScrollUp()
			}
			return m.mergeDiffView.ScrollDown()
		}
		return true
	case component.FocusDiffFileList:
		if m.diffViewActive && m.diffView != nil {
			if direction < 0 {
				return m.diffView.ScrollFileListUp()
			}
			return m.diffView.ScrollFileListDown()
		}
		return true
	case component.FocusDiffView:
		if m.diffView != nil {
			if direction < 0 {
				return m.diffView.ScrollUp()
			}
			return m.diffView.ScrollDown()
		}
		return true
	case component.FocusCodeViewer:
		if m.conflictViewActive && m.conflictView != nil {
			if direction < 0 {
				return m.conflictView.ScrollUp()
			}
			return m.conflictView.ScrollDown()
		}
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			if direction < 0 {
				return m.mergeDiffView.ScrollUp()
			}
			return m.mergeDiffView.ScrollDown()
		}
		if m.diffViewActive && m.diffView != nil {
			if direction < 0 {
				return m.diffView.ScrollUp()
			}
			return m.diffView.ScrollDown()
		}
		if m.viewMode == ViewGit && m.commitTree != nil {
			if direction < 0 {
				return m.commitTree.ScrollUp()
			}
			return m.commitTree.ScrollDown()
		}
		if direction < 0 {
			return m.codePanel.ScrollUp()
		}
		return m.codePanel.ScrollDown()
	case component.FocusFileTree:
		if m.viewMode == ViewGit && m.gitPanel != nil {
			if direction < 0 {
				return m.gitPanel.ScrollUp()
			}
			return m.gitPanel.ScrollDown()
		}
		if direction < 0 {
			return m.fileTree.ScrollUp()
		}
		return m.fileTree.ScrollDown()
	case component.FocusSessionPanel:
		// Left column contains both session and agent panels; scroll goes to agent
		// only when it has scrollable content (expanded/detail views).
		if direction < 0 {
			return m.agentPanel.ScrollUp()
		}
		return m.agentPanel.ScrollDown()
	case component.FocusGitPanel:
		if m.gitPanel == nil {
			return true
		}
		if direction < 0 {
			return m.gitPanel.ScrollUp()
		}
		return m.gitPanel.ScrollDown()
	case component.FocusCommitTree:
		if m.commitTree == nil {
			return true
		}
		if direction < 0 {
			return m.commitTree.ScrollUp()
		}
		return m.commitTree.ScrollDown()
	default:
		if !pane.IsPaneFocus(panelID) {
			return true
		}
		// Merge diff sub-pane scroll.
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			if direction < 0 {
				return m.mergeDiffView.ScrollUp()
			}
			return m.mergeDiffView.ScrollDown()
		}
		// Diff sub-pane scroll.
		if m.diffViewActive && m.diffView != nil {
			if direction < 0 {
				return m.diffView.ScrollUp()
			}
			return m.diffView.ScrollDown()
		}
		pid := pane.PaneIDFromFocus(panelID)
		if pid == m.previewPane {
			if direction < 0 {
				return m.previewPanel.ScrollUp(1)
			}
			return m.previewPanel.ScrollDown(1)
		}
		if pid == m.mdPreviewPane {
			if direction < 0 {
				return m.mdPreviewPanel.ScrollUp(1)
			}
			return m.mdPreviewPanel.ScrollDown(1)
		}
		if ps, ok := m.paneEditors[pid]; ok {
			if direction < 0 {
				return ps.editor.ScrollUp()
			}
			return ps.editor.ScrollDown()
		}
	}
	return true
}

// applyBounceImpulse adds velocity to the bounce spring when a scroll
// boundary is hit. Switching panels resets the bounce state.
func (m *AppModel) applyBounceImpulse(panelID component.FocusID, direction int) {
	if panelID != m.bounce.panel {
		m.bounce = bounceState{panel: panelID}
	}
	m.bounce.vel += float64(direction) * bounceImpulse
	m.bounce.vel = clampFloat(m.bounce.vel, -bounceMaxVel, bounceMaxVel)
}

// tickBounce advances the bounce spring by one frame toward equilibrium (pos=0).
// The underdamped spring naturally oscillates past 0, creating the bounce-back.
func (m *AppModel) tickBounce() {
	b := &m.bounce
	if m.bounceSettled() {
		b.pos = 0
		b.vel = 0
		return
	}

	b.pos, b.vel = m.bounceSpring.Update(b.pos, b.vel, 0)
	b.pos = clampFloat(b.pos, -bounceMaxLines, bounceMaxLines)

	if m.bounceSettled() {
		b.pos = 0
		b.vel = 0
	}
}

// bounceSettled reports whether the bounce spring is close enough to
// equilibrium (pos=0, vel=0) to stop.
func (m *AppModel) bounceSettled() bool {
	return math.Abs(m.bounce.pos) < bounceSettleThreshold &&
		math.Abs(m.bounce.vel) < bounceSettleThreshold
}

// bounceOffset returns the integer line displacement for the given panel.
// Positive = content shifted up (bouncing off bottom boundary).
// Negative = content shifted down (bouncing off top boundary).
func (m *AppModel) bounceOffset(panelID component.FocusID) int {
	if m.bounce.panel != panelID {
		return 0
	}
	return int(math.Round(m.bounce.pos))
}

// clampFloat constrains v to [lo, hi].
func clampFloat(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(v, hi))
}

// ---------------------------------------------------------------------------
// Horizontal swipe (ring cycling via mouse wheel)
// ---------------------------------------------------------------------------

// applySwipeImpulse accumulates horizontal scroll delta and triggers a
// view slot cycle (identical to Alt+V chord) when the accumulated magnitude
// reaches swipeThreshold. In TwoColumn mode both rings cycle in lockstep;
// in other modes the single active ring cycles.
func (m *AppModel) applySwipeImpulse(x int, delta float64) {
	now := time.Now()
	if now.Before(m.swipe.cooldownUntil) {
		return
	}
	if m.leftRing.empty() && m.rightRing.empty() {
		return
	}
	m.swipe.accum += delta
	m.swipe.stamp = now
	if math.Abs(m.swipe.accum) >= swipeThreshold {
		direction := 1
		if m.swipe.accum < 0 {
			direction = -1
		}
		m.cycleViewSlot(direction)
		m.swipe.accum = 0
		m.swipe.cooldownUntil = now.Add(swipeCooldown)
	}
}

// tickSwipeDecay resets stale swipe accumulation after swipeDecay elapses
// since the last horizontal scroll event.
func (m *AppModel) tickSwipeDecay() {
	if m.swipe.accum != 0 && time.Since(m.swipe.stamp) > swipeDecay {
		m.swipe.accum = 0
	}
}

// isFileTreeVisible reports whether the FileTree panel is currently rendered on screen.
func (m *AppModel) isFileTreeVisible() bool {
	if m.layout.Mode() == layout.FourColumn {
		return true
	}
	return m.leftRing.current() == component.FocusFileTree
}

// fileTreePanelX returns the screen X offset of the file tree panel's left edge.
func (m *AppModel) fileTreePanelX() int {
	if m.layout.Mode() == layout.FourColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		return leftW
	}
	return 0
}

// handleFileTreeClick dispatches a click inside the file tree panel, activating
// the entry or search result at the clicked row.
func (m *AppModel) handleFileTreeClick(x, y int) tea.Cmd {
	if !m.isFileTreeVisible() {
		return nil
	}
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	if treeW == 0 || treeH == 0 {
		return nil
	}

	treeX := m.fileTreePanelX()
	innerH := max(treeH-panelBorderSize, 0)

	contentLeft := treeX + 1
	contentRight := treeX + treeW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight {
		return nil
	}
	if y < contentTop || y >= contentBottom {
		return nil
	}

	viewX := x - contentLeft
	viewY := y - contentTop
	return m.fileTree.ClickAt(viewX, viewY)
}

// handleGitPanelClick dispatches a click inside the git panel (occupies the
// FileTree slot in git mode).
func (m *AppModel) handleGitPanelClick(x, y int) tea.Cmd {
	if m.gitPanel == nil {
		return nil
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	if panelW == 0 || panelH == 0 {
		return nil
	}
	panelX := m.fileTreePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		// Click outside git panel — close any open gear dropdown.
		m.gitPanel.CloseDropdown()
		return nil
	}

	viewX := x - contentLeft
	viewY := y - contentTop
	m.focus.SetFocus(component.FocusGitPanel)
	m.syncFocusState()
	cmd := m.gitPanel.ClickAt(viewX, viewY)
	m.syncStagedFiles()
	return cmd
}

// handleDiffFileListClick dispatches a click inside the diff file list panel
// (occupies the FileTree slot when the diff view is active). Sets focus and
// opens the clicked file as a tab if the click falls within the panel bounds.
func (m *AppModel) handleDiffFileListClick(x, y int) {
	if m.diffView == nil {
		return
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	if panelW == 0 || panelH == 0 {
		return
	}
	panelX := m.fileTreePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		return
	}

	localY := y - contentTop
	m.focus.SetFocus(component.FocusDiffFileList)
	m.syncFocusState()
	m.diffView.ClickFileList(localY)
}

// handleMergeDiffFileListClick dispatches a click inside the merge diff file list.
func (m *AppModel) handleMergeDiffFileListClick(x, y int) {
	if m.mergeDiffView == nil {
		return
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	if panelW == 0 || panelH == 0 {
		return
	}
	panelX := m.fileTreePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		return
	}

	localY := y - contentTop
	m.focus.SetFocus(component.FocusMergeDiffFileList)
	m.syncFocusState()
	m.mergeDiffView.ClickFileList(localY)
}

// handleConflictFileListClick dispatches a click inside the conflict file list.
func (m *AppModel) handleConflictFileListClick(x, y int) {
	if m.conflictView == nil {
		return
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	if panelW == 0 || panelH == 0 {
		return
	}
	panelX := m.fileTreePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		return
	}

	localY := y - contentTop
	m.focus.SetFocus(component.FocusConflictFileList)
	m.syncFocusState()
	m.conflictView.ClickFileList(localY)
}

// handleCommitTreeClick dispatches a click inside the commit tree panel
// (occupies the CodeViewer slot in git mode).
func (m *AppModel) handleCommitTreeClick(x, y int) tea.Cmd {
	if m.commitTree == nil {
		return nil
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusCodeViewer)
	if panelW == 0 || panelH == 0 {
		return nil
	}
	panelX := m.codePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		return nil
	}

	viewX := x - contentLeft
	viewY := y - contentTop
	m.focus.SetFocus(component.FocusCommitTree)
	m.syncFocusState()
	cmd := m.commitTree.ClickAt(viewX, viewY)
	return cmd
}

// isChatVisible reports whether the Chat panel is currently rendered on screen.
func (m *AppModel) isChatVisible() bool {
	mode := m.layout.Mode()
	if mode >= layout.ThreeColumn {
		return true
	}
	if mode == layout.TwoColumn {
		return m.rightRing.current() == component.FocusChat
	}
	return m.leftRing.current() == component.FocusChat
}

// handleChatClick copies the content of the chat entry at the clicked
// position to the system clipboard.
func (m *AppModel) handleChatClick(x, y int) tea.Cmd {
	if !m.isChatVisible() {
		return nil
	}
	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	if chatW == 0 || chatH == 0 {
		return nil
	}

	chatX := m.chatPanelX()
	innerH := max(chatH-panelBorderSize, 0)

	// Content area within the bordered chat panel.
	contentLeft := chatX + 1
	contentRight := chatX + chatW - 1

	// Chord hint pushes viewport content down by 2 lines when active.
	chordOffset := 0
	if m.chord != chordNone {
		chordOffset = 2
	}
	viewportTop := 1 + chordOffset // 1 = top border
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight {
		return nil
	}
	if y < viewportTop || y >= contentBottom {
		return nil
	}

	viewportLine := y - viewportTop
	target := m.chat.CopyTargetAtViewLine(viewportLine)
	if target == nil {
		return nil
	}

	if err := m.clipboard.Set(target.Content); err != nil {
		m.statusBar.SetFlash("Copy failed")
		return nil
	}
	m.chat.SetHighlight(target.EntryID, target.EntryIndex, target.HighlightStart, target.HighlightEnd)
	m.statusBar.SetFlash("Copied!")
	return nil
}

// chatPanelX returns the X coordinate where the chat panel starts,
// based on the current layout mode.
func (m *AppModel) chatPanelX() int {
	mode := m.layout.Mode()
	if mode == layout.FourColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		treeW, _ := m.layout.GetPanelSize(component.FocusFileTree)
		return leftW + treeW
	}
	if mode >= layout.TwoColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		return leftW
	}
	return 0
}

// codePanelX returns the X coordinate where the code panel starts,
// based on the current layout mode.
func (m *AppModel) codePanelX() int {
	mode := m.layout.Mode()
	switch mode {
	case layout.FourColumn:
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		treeW, _ := m.layout.GetPanelSize(component.FocusFileTree)
		chatW, _ := m.layout.GetPanelSize(component.FocusChat)
		return leftW + treeW + chatW
	case layout.ThreeColumn:
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		chatW, _ := m.layout.GetPanelSize(component.FocusChat)
		return leftW + chatW
	case layout.TwoColumn:
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		return leftW
	default:
		return 0
	}
}

// hasPreview reports whether a preview pane is currently active.
func (m *AppModel) hasPreview() bool {
	return m.previewPane != 0
}

// isFullMdPreview reports whether the markdown preview is taking the full
// code panel (the sole editor pane has no tabs).
func (m *AppModel) isFullMdPreview() bool {
	return m.mdPreviewPane != 0 && len(m.paneEditors) == 1 && len(m.focusedTabOrder()) == 0
}

// focusedEditor returns the editor for the currently focused pane.
func (m *AppModel) focusedEditor() *editor.Model {
	return m.paneEditors[m.focusedPane].editor
}

// focusedTabOrder returns the tab order for the currently focused pane.
func (m *AppModel) focusedTabOrder() []string {
	return m.paneEditors[m.focusedPane].tabOrder
}

// setFocusedTabOrder replaces the tab order for the currently focused pane.
func (m *AppModel) setFocusedTabOrder(to []string) {
	m.paneEditors[m.focusedPane].tabOrder = to
}

// paneEditor returns the editor for the given pane ID.
func (m *AppModel) paneEditor(id pane.PaneID) *editor.Model {
	return m.paneEditors[id].editor
}

// paneTabOrder returns the tab order for the given pane ID.
func (m *AppModel) paneTabOrder(id pane.PaneID) []string {
	return m.paneEditors[id].tabOrder
}

// allTabOrders returns all open file paths across all editor panes.
func (m *AppModel) allTabOrders() []string {
	var all []string
	for _, ps := range m.paneEditors {
		all = append(all, ps.tabOrder...)
	}
	return all
}

// anyPaneHasTabs reports whether any editor pane has open tabs.
func (m *AppModel) anyPaneHasTabs() bool {
	for _, ps := range m.paneEditors {
		if len(ps.tabOrder) > 0 {
			return true
		}
	}
	return false
}

// previewSplitWidth returns the preview half width in split mode.
// Returns 0 when not in split mode or preview is inactive.
func (m *AppModel) previewSplitWidth() int {
	if !m.hasPreview() || m.viewMode != ViewEdit || len(m.focusedTabOrder()) == 0 {
		return 0
	}
	rightW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	// Derived from: 1 divider char, remaining space split evenly.
	dividerWidth := 1
	return (innerW - dividerWidth) / 2
}

// isInsidePreviewHalf reports whether screen x falls within the preview
// half of the code panel in split view.
func (m *AppModel) isInsidePreviewHalf(x int) bool {
	pw := m.previewSplitWidth()
	if pw == 0 {
		return false
	}
	codeX := m.codePanelX()
	contentLeft := codeX + 1 // skip left border
	return x >= contentLeft && x < contentLeft+pw
}

// paneContentArea returns the inner area used by ComputeLayout for panes.
func (m *AppModel) paneContentArea() pane.Rect {
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)
	contentH := max(innerH-1, 1) // minus status line
	return pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
}

// hitTestPane returns the PaneID under screen coordinates (screenX, screenY)
// using the pane tree's ComputeLayout. Converts screen coords to content-local
// before testing against pane rects.
func (m *AppModel) hitTestPane(screenX, screenY int) (pane.PaneID, bool) {
	if m.paneTree == nil {
		return 0, false
	}
	codeX := m.codePanelX()
	viewX := screenX - (codeX + 1) // skip left border
	viewY := screenY - 1           // skip top border

	area := m.paneContentArea()
	rects := m.paneTree.ComputeLayout(area)
	for id, r := range rects {
		if viewX >= r.X && viewX < r.X+r.W && viewY >= r.Y && viewY < r.Y+r.H {
			return id, true
		}
	}
	return 0, false
}

// paneViewCoords converts screen coordinates to pane-local coordinates.
// Returns (paneID, localX, localY, ok). localX/localY are relative to the
// pane's top-left corner within the code panel content area.
func (m *AppModel) paneViewCoords(screenX, screenY int) (pane.PaneID, int, int, bool) {
	if m.paneTree == nil {
		return 0, 0, 0, false
	}
	codeX := m.codePanelX()
	viewX := screenX - (codeX + 1)
	viewY := screenY - 1

	area := m.paneContentArea()
	rects := m.paneTree.ComputeLayout(area)
	for id, r := range rects {
		if viewX >= r.X && viewX < r.X+r.W && viewY >= r.Y && viewY < r.Y+r.H {
			return id, viewX - r.X, viewY - r.Y, true
		}
	}
	return 0, 0, 0, false
}

// handleEditorMouse handles all left-button mouse actions (press, drag,
// release) inside the code panel during edit mode. Returns (consumed, cmd).
func (m *AppModel) handleEditorMouse(mouse tea.MouseMsg) (bool, tea.Cmd) {
	// Release always clears tracking, even outside the panel.
	if mouse.Action == tea.MouseActionRelease {
		cmd := m.finalizeCrossPaneDrop()
		m.editorMouseDown = false
		m.editorDragging = false
		m.inputMouseDown = false // Cross-panel release safety.
		m.tabDragIdx = -1
		m.tabDragSourcePane = 0
		m.tabDropTarget = 0
		return true, cmd
	}

	// Full-preview mode: all code panel clicks route to the preview.
	// Only split mode needs the per-half routing below.
	if m.hasPreview() && len(m.focusedTabOrder()) == 0 {
		if mouse.Action == tea.MouseActionPress && m.isInsideCodePanel(mouse.X, mouse.Y) {
			viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
			if viewY < tabbar.Height {
				codeW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
				cfg := tabbar.Config{
					Tabs: []tabbar.Tab{{
						Path:        m.previewPanel.FilePath(),
						Modified:    false,
						LabelPrefix: "Preview: ",
					}},
					Active:    0,
					Width:     max(codeW-panelBorderSize, 1),
					NerdFonts: m.nerdFontsDetected,
					Theme:     m.config.Theme(),
				}
				hit := tabbar.HitTest(cfg, viewX)
				if hit.TabIndex >= 0 && hit.IsClose {
					m.dismissPreview()
					return true, nil
				}
			} else {
				// Content click: position the cursor.
				m.previewPanel.SetCursorFromViewport(viewX, viewY-tabbar.Height)
			}
			m.focus.SetFocus(pane.PaneFocusID(m.previewPane))
			m.syncFocusState()
		}
		return true, nil
	}

	// Full markdown preview mode: all code panel clicks route to the mdPreview.
	if m.isFullMdPreview() {
		if mouse.Action == tea.MouseActionPress && m.isInsideCodePanel(mouse.X, mouse.Y) {
			viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
			if viewY < tabbar.Height {
				codeW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
				cfg := tabbar.Config{
					Tabs: []tabbar.Tab{{
						Path:        m.mdPreviewPanel.FilePath(),
						Modified:    false,
						LabelPrefix: "Markdown: ",
					}},
					Active:    0,
					Width:     max(codeW-panelBorderSize, 1),
					NerdFonts: m.nerdFontsDetected,
					Theme:     m.config.Theme(),
				}
				hit := tabbar.HitTest(cfg, viewX)
				if hit.TabIndex >= 0 && hit.IsClose {
					m.dismissMarkdownPreview()
					return true, nil
				}
			}
			m.focus.SetFocus(pane.PaneFocusID(m.mdPreviewPane))
			m.syncFocusState()
		}
		return true, nil
	}

	// Tab bar drag: reorder within pane or detect cross-pane drop target.
	if mouse.Action == tea.MouseActionMotion && m.tabDragIdx >= 0 {
		m.tabDropTarget = 0

		// Multi-pane tree mode: use tree-based hit testing.
		if m.paneTree != nil && !m.paneTree.IsLeaf() {
			pid, localX, _, ok := m.paneViewCoords(mouse.X, mouse.Y)
			if !ok {
				return true, nil
			}
			if pid == m.tabDragSourcePane && pid != m.previewPane {
				return m.handleTabDragReorder(localX)
			}
			if pid != m.tabDragSourcePane && pid != m.previewPane {
				if _, isEditor := m.paneEditors[pid]; isEditor {
					m.tabDropTarget = pid
				}
			}
			return true, nil
		}

		// Legacy split mode: preview-to-editor detection.
		if m.tabDragSourcePane == m.previewPane && !m.isInsidePreviewHalf(mouse.X) {
			m.tabDropTarget = m.focusedPane
			return true, nil
		}

		// Single-pane intra-pane reorder (existing behavior).
		viewX, _ := m.focusedPaneLocalCoords(mouse.X, mouse.Y)
		return m.handleTabDragReorder(viewX)
	}

	// Motion while dragging: extend selection even if the pointer left
	// the panel bounds (clamp handled by the editor).
	// Suppressed when multi-cursor is active — visual mode and multi-cursor
	// are mutually exclusive, so drag-select is disabled until cursors are
	// cleared (Esc).
	if mouse.Action == tea.MouseActionMotion && m.editorMouseDown {
		if m.focusedEditor().HasMultiCursor() {
			return true, nil
		}
		viewX, viewY := m.focusedPaneLocalCoords(mouse.X, mouse.Y)
		if len(m.focusedTabOrder()) > 0 {
			viewY -= tabbar.Height
		}
		viewY -= m.focusedEditor().FindBarHeight()
		if !m.editorDragging {
			m.focusedEditor().StartDragSelection()
			m.editorDragging = true
		}
		m.focusedEditor().ExtendDragSelection(viewX, viewY)
		return true, nil
	}

	// Press: must be inside the code panel.
	if mouse.Action != tea.MouseActionPress {
		return false, nil
	}

	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return false, nil
	}

	// Multi-pane mode: route via tree-based hit test.
	if m.paneTree != nil && !m.paneTree.IsLeaf() {
		return m.handleMultiPanePress(mouse)
	}

	// Single-pane / legacy preview-split path.

	// In preview split mode, clicks on the preview half focus the preview
	// panel. Tab bar close clicks dismiss the preview.
	if m.isInsidePreviewHalf(mouse.X) {
		viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
		if viewY < tabbar.Height {
			cfg := tabbar.Config{
				Tabs: []tabbar.Tab{{
					Path:        m.previewPanel.FilePath(),
					Modified:    false,
					LabelPrefix: "Preview: ",
				}},
				Active:     0,
				Width:      m.previewSplitWidth(),
				NerdFonts:  m.nerdFontsDetected,
				Theme:      m.config.Theme(),
				HoverClose: -1,
			}
			hit := tabbar.HitTest(cfg, viewX)
			if hit.TabIndex >= 0 && hit.IsClose {
				m.dismissPreview()
				return true, nil
			}
			// Begin preview tab drag.
			if hit.TabIndex >= 0 {
				m.tabDragIdx = 0
				m.tabDragSourcePane = m.previewPane
			}
		} else {
			// Content click: position the cursor.
			m.previewPanel.SetCursorFromViewport(viewX, viewY-tabbar.Height)
		}
		m.focus.SetFocus(pane.PaneFocusID(m.previewPane))
		m.syncFocusState()
		return true, nil
	}

	return m.handleEditorPaneClick(mouse)
}

// handleMultiPanePress routes a press event to the correct pane in multi-pane
// mode using tree-based hit testing.
func (m *AppModel) handleMultiPanePress(mouse tea.MouseMsg) (bool, tea.Cmd) {
	pid, localX, localY, ok := m.paneViewCoords(mouse.X, mouse.Y)
	if !ok {
		return false, nil
	}

	// Switch focus to the clicked pane.
	m.focus.SetFocus(pane.PaneFocusID(pid))
	m.syncFocusState()

	// Preview pane click.
	if pid == m.previewPane {
		if localY < tabbar.Height {
			area := m.paneContentArea()
			rects := m.paneTree.ComputeLayout(area)
			r := rects[pid]
			cfg := tabbar.Config{
				Tabs: []tabbar.Tab{{
					Path:        m.previewPanel.FilePath(),
					Modified:    false,
					LabelPrefix: "Preview: ",
				}},
				Active:    0,
				Width:     r.W,
				NerdFonts: m.nerdFontsDetected,
				Theme:     m.config.Theme(),
			}
			hit := tabbar.HitTest(cfg, localX)
			if hit.TabIndex >= 0 && hit.IsClose {
				m.dismissPreview()
				return true, nil
			}
			// Begin preview tab drag.
			if hit.TabIndex >= 0 {
				m.tabDragIdx = 0
				m.tabDragSourcePane = m.previewPane
			}
		} else {
			// Content click: position the cursor.
			viewY := localY - tabbar.Height
			m.previewPanel.SetCursorFromViewport(localX, viewY)
		}
		return true, nil
	}

	// Markdown preview pane click.
	if pid == m.mdPreviewPane {
		if localY < tabbar.Height {
			area := m.paneContentArea()
			rects := m.paneTree.ComputeLayout(area)
			r := rects[pid]
			cfg := tabbar.Config{
				Tabs: []tabbar.Tab{{
					Path:        m.mdPreviewPanel.FilePath(),
					Modified:    false,
					LabelPrefix: "Markdown: ",
				}},
				Active:    0,
				Width:     r.W,
				NerdFonts: m.nerdFontsDetected,
				Theme:     m.config.Theme(),
			}
			hit := tabbar.HitTest(cfg, localX)
			if hit.TabIndex >= 0 && hit.IsClose {
				m.dismissMarkdownPreview()
			}
		}
		return true, nil
	}

	// Editor pane click.
	ps, exists := m.paneEditors[pid]
	if !exists {
		return true, nil
	}

	// Check for markdown tooltip click (within the 3-row tooltip area).
	if m.mdTooltipTab >= 0 && m.mdTooltipPane == pid && localY >= tabbar.Height && localY < tabbar.Height+mdTooltipHeight {
		if localX >= m.mdTooltipX && localX < m.mdTooltipX+mdTooltipWidth {
			if m.mdTooltipTab < len(ps.tabOrder) {
				path := ps.tabOrder[m.mdTooltipTab]
				m.openMarkdownPreview(path)
				m.mdTooltipTab = -1
				m.mdTooltipPane = 0
			}
			return true, nil
		}
	}

	// Tab bar click within pane.
	if len(ps.tabOrder) > 0 && localY < tabbar.Height {
		area := m.paneContentArea()
		rects := m.paneTree.ComputeLayout(area)
		r := rects[pid]
		cfg := m.paneTabBarConfig(pid, r.W)
		hit := tabbar.HitTest(cfg, localX)
		if hit.IsLeftNav {
			m.tabArrowFlashLeftUntil = time.Now().Add(tabArrowFlashDuration)
			return true, m.prevTab()
		}
		if hit.IsRightNav {
			m.tabArrowFlashRightUntil = time.Now().Add(tabArrowFlashDuration)
			return true, m.nextTab()
		}
		if hit.TabIndex < 0 || hit.TabIndex >= len(ps.tabOrder) {
			return true, nil
		}
		if hit.IsClose {
			return true, m.closeTab(hit.TabIndex)
		}
		m.tabDragIdx = hit.TabIndex
		m.tabDragSourcePane = pid
		return true, m.switchToTab(hit.TabIndex)
	}

	// Content click below tab bar.
	viewX := localX
	viewY := localY
	if len(ps.tabOrder) > 0 {
		viewY -= tabbar.Height
	}

	barH := ps.editor.ReplaceBarHeight()
	if barH > 0 && viewY < barH {
		ps.editor.HandleReplaceBarClick(viewX, viewY)
		return true, nil
	}
	if barH == 0 {
		barH = ps.editor.FindBarHeight()
		if barH > 0 && viewY < barH {
			ps.editor.HandleFindBarClick(viewX, viewY)
			return true, nil
		}
	}
	viewY -= barH

	if cmd, handled := ps.editor.HandleHoverClick(viewX, viewY); handled {
		return true, cmd
	}
	if ps.editor.HandleCompletionClick(viewX, viewY) {
		return true, nil
	}

	ps.editor.ClickAt(viewX, viewY)
	m.editorMouseDown = true
	m.editorDragging = false

	line := ps.editor.CursorLine()
	col := ps.editor.CursorCol()
	if ps.editor.IsWordCharAtPos(line, col) {
		m.highlightLine = line
		m.highlightCol = col
		ps.editor.ClearHighlightRanges()
		return true, tea.Tick(highlightDebounce, func(_ time.Time) tea.Msg {
			return msg.LSPDocHighlightTickMsg{Line: line, Col: col}
		})
	}
	ps.editor.ClearHighlightRanges()
	return true, nil
}

// handleEditorPaneClick handles a click in the single editor pane (no splits).
func (m *AppModel) handleEditorPaneClick(mouse tea.MouseMsg) (bool, tea.Cmd) {
	viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)

	// In split mode, offset viewX to be relative to the editor half.
	if pw := m.previewSplitWidth(); pw > 0 {
		dividerWidth := 1
		viewX -= pw + dividerWidth
	}

	// Tab bar click: when the tab bar is visible, clicks within it switch tabs.
	if m.tabBarHeight() > 0 && viewY < m.tabBarHeight() {
		return m.handleTabBarClick(viewX)
	}

	// Markdown tooltip click (within the 3-row tooltip area).
	if m.mdTooltipTab >= 0 && m.mdTooltipPane == m.focusedPane && viewY >= m.tabBarHeight() && viewY < m.tabBarHeight()+mdTooltipHeight {
		if viewX >= m.mdTooltipX && viewX < m.mdTooltipX+mdTooltipWidth {
			tabOrder := m.focusedTabOrder()
			if m.mdTooltipTab < len(tabOrder) {
				m.openMarkdownPreview(tabOrder[m.mdTooltipTab])
				m.mdTooltipTab = -1
				m.mdTooltipPane = 0
			}
			return true, nil
		}
	}

	// Offset for tab bar so editor coordinates remain correct.
	viewY -= m.tabBarHeight()

	// When a search bar (find or replace) is open, clicks in its area
	// toggle badges/buttons; clicks below are offset to content-local coords.
	barH := m.focusedEditor().ReplaceBarHeight()
	if barH > 0 && viewY < barH {
		m.focusedEditor().HandleReplaceBarClick(viewX, viewY)
		return true, nil
	}
	if barH == 0 {
		barH = m.focusedEditor().FindBarHeight()
		if barH > 0 && viewY < barH {
			m.focusedEditor().HandleFindBarClick(viewX, viewY)
			return true, nil
		}
	}
	viewY -= barH

	// When the hover popup is visible, clicks on it trigger go-to-definition.
	if cmd, ok := m.focusedEditor().HandleHoverClick(viewX, viewY); ok {
		return true, cmd
	}

	// When the completion popup is visible, clicks on it select an item
	// instead of moving the cursor.
	if m.focusedEditor().HandleCompletionClick(viewX, viewY) {
		return true, nil
	}

	m.focusedEditor().ClickAt(viewX, viewY)
	m.editorMouseDown = true
	m.editorDragging = false

	// Schedule document highlight debounce after click places cursor on a word.
	line := m.focusedEditor().CursorLine()
	col := m.focusedEditor().CursorCol()
	if m.focusedEditor().IsWordCharAtPos(line, col) {
		m.highlightLine = line
		m.highlightCol = col
		m.focusedEditor().ClearHighlightRanges()
		return true, tea.Tick(highlightDebounce, func(_ time.Time) tea.Msg {
			return msg.LSPDocHighlightTickMsg{Line: line, Col: col}
		})
	}
	m.focusedEditor().ClearHighlightRanges()
	return true, nil
}

// isInsideCodePanel reports whether screen coordinates (x, y) fall within
// the content area of the code panel.
func (m *AppModel) isInsideCodePanel(x, y int) bool {
	codeW, codeH := m.layout.GetPanelSize(component.FocusCodeViewer)
	if codeW == 0 || codeH == 0 {
		return false
	}
	codeX := m.codePanelX()
	innerH := max(codeH-panelBorderSize, 0)

	contentLeft := codeX + 1
	contentRight := codeX + codeW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	return x >= contentLeft && x < contentRight && y >= contentTop && y < contentBottom
}

// editorViewCoords converts screen coordinates to content-local viewport
// coordinates for the code panel. Coordinates are clamped to [0, max).
func (m *AppModel) editorViewCoords(screenX, screenY int) (int, int) {
	codeW, codeH := m.layout.GetPanelSize(component.FocusCodeViewer)
	codeX := m.codePanelX()
	innerW := max(codeW-panelBorderSize, 0)
	innerH := max(codeH-panelBorderSize, 0)

	viewX := max(min(screenX-(codeX+1), innerW-1), 0)
	viewY := max(min(screenY-1, innerH-1), 0)
	return viewX, viewY
}

// focusedPaneRect returns the layout rectangle for the currently focused
// editor pane. Returns a zero Rect and false when not in multi-pane mode.
func (m *AppModel) focusedPaneRect() (pane.Rect, bool) {
	if m.paneTree == nil || m.paneTree.IsLeaf() {
		return pane.Rect{}, false
	}
	area := m.paneContentArea()
	rects := m.paneTree.ComputeLayout(area)
	r, ok := rects[m.focusedPane]
	return r, ok
}

// focusedPaneLocalCoords converts screen coordinates to the focused pane's
// local coordinates. In multi-pane mode, coordinates are relative to the
// pane's top-left. In single-pane mode, falls back to editorViewCoords
// with preview offset. Coordinates are clamped for drag safety.
func (m *AppModel) focusedPaneLocalCoords(screenX, screenY int) (int, int) {
	r, ok := m.focusedPaneRect()
	if !ok {
		viewX, viewY := m.editorViewCoords(screenX, screenY)
		if pw := m.previewSplitWidth(); pw > 0 {
			viewX -= pw + 1
		}
		return viewX, viewY
	}
	codeX := m.codePanelX()
	viewX := screenX - (codeX + 1) - r.X
	viewY := screenY - 1 - r.Y
	viewX = max(0, min(viewX, r.W-1))
	viewY = max(0, min(viewY, r.H-1))
	return viewX, viewY
}

// previewPaneLocalCoords converts screen coordinates to the preview pane's
// local coordinates. In multi-pane mode, uses the preview pane's rect from
// ComputeLayout. In single-pane mode, uses the code panel's left half.
func (m *AppModel) previewPaneLocalCoords(screenX, screenY int) (int, int) {
	if m.paneTree != nil && !m.paneTree.IsLeaf() && m.hasPreview() {
		area := m.paneContentArea()
		rects := m.paneTree.ComputeLayout(area)
		if r, ok := rects[m.previewPane]; ok {
			codeX := m.codePanelX()
			viewX := screenX - (codeX + 1) - r.X
			viewY := screenY - 1 - r.Y
			viewX = max(0, min(viewX, r.W-1))
			viewY = max(0, min(viewY, r.H-1))
			return viewX, viewY
		}
	}
	return m.editorViewCoords(screenX, screenY)
}

// hoverDebounce delays before firing an LSP hover request.
// Derived from: 150ms is responsive without flooding the server on fast swipes;
// 50ms retrigger keeps the tooltip "following" the cursor between words.
const (
	hoverInitialDebounce   = 350 * time.Millisecond // first hover when no tooltip is showing
	hoverRetriggerDebounce = 50 * time.Millisecond  // re-trigger when moving between words
)

// highlightDebounce is the delay before firing a documentHighlight request
// after the cursor comes to rest on a symbol.
// Derived from: 100ms feels snappy while still batching rapid j/k navigation.
const highlightDebounce = 100 * time.Millisecond

// handleEditorMouseHover fires a debounced LSP hover request when the
// mouse moves to a new word in edit mode. Only triggers on word characters;
// moving within the same word does not reset the debounce.
func (m *AppModel) handleEditorMouseHover(mouse tea.MouseMsg) tea.Cmd {
	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		m.dismissAllHover()
		return nil
	}

	// Determine whether the mouse is over the preview pane.
	overPreview := false
	var viewX, viewY int

	if m.paneTree != nil && !m.paneTree.IsLeaf() {
		pid, lx, ly, ok := m.paneViewCoords(mouse.X, mouse.Y)
		if !ok {
			m.dismissAllHover()
			return nil
		}
		if m.hasPreview() && pid == m.previewPane {
			overPreview = true
			viewX, viewY = lx, ly
			// Account for tab bar height in the preview pane.
			viewY -= tabbar.Height
		} else if pid != m.focusedPane {
			m.dismissAllHover()
			return nil
		}
	} else if m.isInsidePreviewHalf(mouse.X) {
		overPreview = true
	}

	if overPreview {
		return m.handlePreviewMouseHover(mouse, viewX, viewY)
	}

	// Editor pane hover (original path).
	viewX, viewY = m.focusedPaneLocalCoords(mouse.X, mouse.Y)
	if len(m.focusedTabOrder()) > 0 {
		viewY -= tabbar.Height
	}
	viewY -= m.focusedEditor().FindBarHeight()

	// Keep hover stable when the mouse is inside the popup.
	if m.focusedEditor().HoverActive() && m.focusedEditor().IsInsideHoverPopup(viewX, viewY) {
		return nil
	}

	// Keep hover stable when crossing a non-hover overlay (signature/completion).
	if m.focusedEditor().HoverActive() && m.focusedEditor().IsInsideOverlayPopup(viewX, viewY) {
		return nil
	}

	line, col, ok := m.focusedEditor().ViewportToBufferPos(viewX, viewY)
	if !ok {
		m.dismissAllHover()
		return nil
	}

	if !m.focusedEditor().IsWordCharAtPos(line, col) {
		m.dismissAllHover()
		return nil
	}

	wordStart, _ := m.focusedEditor().WordBoundsAt(line, col)
	if line == m.hoverMouseLine && wordStart == m.hoverMouseWordStart && !m.hoverForPreview {
		return nil
	}

	debounce := hoverInitialDebounce
	if m.focusedEditor().HoverActive() || m.previewPanel.HoverActive() {
		m.dismissAllHover()
		debounce = hoverRetriggerDebounce
	}

	m.hoverMouseLine = line
	m.hoverMouseCol = col
	m.hoverMouseWordStart = wordStart
	m.hoverForPreview = false
	m.pendingHoverSymbol = ""
	m.pendingHoverPkgPath = ""
	return tea.Tick(debounce, func(_ time.Time) tea.Msg {
		return msg.LSPMouseHoverTickMsg{Line: line, Col: col}
	})
}

// handlePreviewMouseHover handles hover detection when the mouse is over
// the preview pane. Uses the preview panel's coordinate conversion and
// word detection methods.
func (m *AppModel) handlePreviewMouseHover(mouse tea.MouseMsg, viewX, viewY int) tea.Cmd {
	if m.previewPanel.FilePath() == "" {
		m.dismissAllHover()
		return nil
	}

	// In single-pane mode, compute local coords from the code panel.
	if m.paneTree == nil || m.paneTree.IsLeaf() {
		vx, vy := m.editorViewCoords(mouse.X, mouse.Y)
		viewX, viewY = vx, vy
		// Account for tab bar in single-pane preview.
		viewY -= tabbar.Height
	}

	// Keep hover stable when the mouse is inside the preview's hover popup.
	if m.previewPanel.HoverActive() && m.previewPanel.IsInsideHoverPopup(viewX, viewY) {
		return nil
	}

	line, col, ok := m.previewPanel.ViewportToBufferPos(viewX, viewY)
	if !ok {
		m.dismissAllHover()
		return nil
	}

	if !m.previewPanel.IsWordCharAtPos(line, col) {
		m.dismissAllHover()
		return nil
	}

	wordStart, _ := m.previewPanel.WordBoundsAt(line, col)
	if line == m.hoverMouseLine && wordStart == m.hoverMouseWordStart && m.hoverForPreview {
		return nil
	}

	debounce := hoverInitialDebounce
	if m.previewPanel.HoverActive() || m.focusedEditor().HoverActive() {
		m.dismissAllHover()
		debounce = hoverRetriggerDebounce
	}

	m.hoverMouseLine = line
	m.hoverMouseCol = col
	m.hoverMouseWordStart = wordStart
	m.hoverForPreview = true
	m.pendingHoverSymbol = ""
	m.pendingHoverPkgPath = ""
	return tea.Tick(debounce, func(_ time.Time) tea.Msg {
		return msg.LSPMouseHoverTickMsg{Line: line, Col: col}
	})
}

// dismissAllHover dismisses both editor and preview hover tooltips and
// resets the hover tracking state.
func (m *AppModel) dismissAllHover() {
	if m.focusedEditor().HoverActive() {
		m.focusedEditor().DismissHover()
	}
	if m.previewPanel.HoverActive() {
		m.previewPanel.DismissHover()
	}
	m.hoverMouseLine = -1
}

// handleMouseHoverTick processes the debounced hover tick. If the mouse
// is still on the same word, fires parallel LSP hover and definition requests.
func (m *AppModel) handleMouseHoverTick(tick msg.LSPMouseHoverTickMsg) tea.Cmd {
	if m.viewMode != ViewEdit {
		return nil
	}

	// When hover is for the preview pane, use preview panel methods.
	if m.hoverForPreview {
		wordStart, _ := m.previewPanel.WordBoundsAt(tick.Line, tick.Col)
		if tick.Line != m.hoverMouseLine || wordStart != m.hoverMouseWordStart {
			return nil
		}
		filePath := m.previewPanel.FilePath()
		if filePath == "" {
			return nil
		}
		m.pendingHoverSymbol = ""
		m.pendingHoverPkgPath = ""
		return tea.Batch(
			m.lspHoverCmd(filePath, tick.Line, tick.Col),
			m.lspDefinitionCmd(filePath, tick.Line, tick.Col, true),
		)
	}

	// Editor pane hover.
	wordStart, _ := m.focusedEditor().WordBoundsAt(tick.Line, tick.Col)
	if tick.Line != m.hoverMouseLine || wordStart != m.hoverMouseWordStart {
		return nil
	}
	filePath := m.focusedEditor().FilePath()
	if filePath == "" {
		return nil
	}
	m.pendingHoverSymbol = ""
	m.pendingHoverPkgPath = ""
	return tea.Batch(
		m.lspHoverCmd(filePath, tick.Line, tick.Col),
		m.lspDefinitionCmd(filePath, tick.Line, tick.Col, true),
	)
}

// handleDocHighlightTick processes the debounced document highlight tick.
// If the cursor is still on the same position and in normal mode, fires
// the LSP documentHighlight request.
func (m *AppModel) handleDocHighlightTick(tick msg.LSPDocHighlightTickMsg) tea.Cmd {
	if m.viewMode != ViewEdit {
		return nil
	}
	if m.focusedEditor().CursorLine() != tick.Line || m.focusedEditor().CursorCol() != tick.Col {
		return nil
	}
	if !m.focusedEditor().IsWordCharAtPos(tick.Line, tick.Col) {
		return nil
	}
	filePath := m.focusedEditor().FilePath()
	if filePath == "" {
		return nil
	}
	return m.lspDocumentHighlightCmd(filePath, tick.Line, tick.Col)
}

func (m *AppModel) toggleSearch() {
	if m.searchOverlay.Visible() {
		m.searchOverlay.Hide()
		m.overlay = overlayNone
	} else {
		// Kill residual scroll/bounce momentum so it doesn't affect the
		// overlay or resume unexpectedly when the overlay closes.
		m.scroll = scrollState{}
		m.bounce = bounceState{}
		m.chat.SetBounceOffset(0)
		m.codePanel.SetBounceOffset(0)
		m.fileTree.SetBounceOffset(0)
		m.searchOverlay.Show()
		m.overlay = overlaySearch
	}
}

func (m *AppModel) toggleFieldManual() {
	if m.fieldManualOverlay.Visible() {
		m.fieldManualOverlay.Hide()
		m.overlay = overlayNone
	} else {
		m.scroll = scrollState{}
		m.bounce = bounceState{}
		m.chat.SetBounceOffset(0)
		m.codePanel.SetBounceOffset(0)
		m.fileTree.SetBounceOffset(0)
		m.fieldManualOverlay.Show()
		m.overlay = overlayFieldManual
	}
}

// ---------------------------------------------------------------------------
// Message propagation
// ---------------------------------------------------------------------------

// propagate forwards a message to all components and collects commands.
func (m *AppModel) propagate(raw tea.Msg) tea.Cmd {
	// Skip global propagation for tick messages to avoid redundant work;
	// fast ticks are handled centrally, decor ticks are dispatched explicitly.
	switch raw.(type) {
	case msg.TickMsg, msg.DecorTickMsg:
		return nil
	}

	var cmds []tea.Cmd

	chatComp, chatCmd := m.chat.Update(raw)
	m.chat = chatComp.(*chat.Model)
	cmds = appendCmd(cmds, chatCmd)

	inputComp, inputCmd := m.input.Update(raw)
	m.input = inputComp.(*inputpkg.Model)
	cmds = appendCmd(cmds, inputCmd)

	_, statusCmd := m.statusBar.Update(raw)
	cmds = appendCmd(cmds, statusCmd)

	sessionComp, sessionCmd := m.sessionPanel.Update(raw)
	m.sessionPanel = sessionComp.(*sessionpkg.Model)
	cmds = appendCmd(cmds, sessionCmd)

	agentComp, agentCmd := m.agentPanel.Update(raw)
	m.agentPanel = agentComp.(*agentpkg.Model)
	cmds = appendCmd(cmds, agentCmd)

	codeComp, codeCmd := m.codePanel.Update(raw)
	m.codePanel = codeComp.(*codepkg.Model)
	cmds = appendCmd(cmds, codeCmd)

	knowledgeComp, knowledgeCmd := m.knowledgePanel.Update(raw)
	m.knowledgePanel = knowledgeComp.(*knowledgepkg.Model)
	cmds = appendCmd(cmds, knowledgeCmd)

	treeComp, treeCmd := m.fileTree.Update(raw)
	m.fileTree = treeComp.(*filetree.Model)
	cmds = appendCmd(cmds, treeCmd)

	if m.gitPanel != nil {
		gitComp, gitCmd := m.gitPanel.Update(raw)
		m.gitPanel = gitComp.(*gitpanel.Model)
		cmds = appendCmd(cmds, gitCmd)
		m.syncStagedFiles()
	}

	if m.commitTree != nil {
		ctComp, ctCmd := m.commitTree.Update(raw)
		m.commitTree = ctComp.(*committree.Model)
		cmds = appendCmd(cmds, ctCmd)
	}

	return tea.Batch(cmds...)
}

// propagateToFocused sends a key message only to the currently focused component.
func (m *AppModel) propagateToFocused(key tea.KeyMsg) tea.Cmd {
	focused := m.focus.Current()

	switch focused {
	case component.FocusInput:
		prevVisual := m.input.VisualLineCount()
		comp, cmd := m.input.Update(key)
		m.input = comp.(*inputpkg.Model)
		if m.input.VisualLineCount() != prevVisual {
			m.recalcLayout()
		}
		return cmd
	case component.FocusChat:
		comp, cmd := m.chat.Update(key)
		m.chat = comp.(*chat.Model)
		return cmd
	case component.FocusSessionPanel:
		comp, cmd := m.sessionPanel.Update(key)
		m.sessionPanel = comp.(*sessionpkg.Model)
		return cmd
	case component.FocusAgentPanel:
		comp, cmd := m.agentPanel.Update(key)
		m.agentPanel = comp.(*agentpkg.Model)
		m.syncManualTargetFromAgentSelection()
		return cmd
	case component.FocusCodeViewer:
		// Non-edit mode: route to read-only code viewer.
		comp, cmd := m.codePanel.Update(key)
		m.codePanel = comp.(*codepkg.Model)
		return cmd
	case component.FocusFileTree:
		comp, cmd := m.fileTree.Update(key)
		m.fileTree = comp.(*filetree.Model)
		return cmd
	case component.FocusKnowledge:
		comp, cmd := m.knowledgePanel.Update(key)
		m.knowledgePanel = comp.(*knowledgepkg.Model)
		return cmd
	case component.FocusGitPanel:
		comp, cmd := m.gitPanel.Update(key)
		m.gitPanel = comp.(*gitpanel.Model)
		m.syncStagedFiles()
		return cmd
	case component.FocusCommitTree:
		comp, cmd := m.commitTree.Update(key)
		m.commitTree = comp.(*committree.Model)
		return cmd
	case component.FocusConflictView:
		if m.conflictViewActive && m.conflictView != nil {
			return m.conflictView.Update(key)
		}
		return nil
	case component.FocusConflictFileList:
		if m.conflictViewActive && m.conflictView != nil {
			return m.conflictView.UpdateFileList(key.String())
		}
		return nil
	case component.FocusMergeDiffView:
		if m.mergeDiffView != nil {
			cmd := m.mergeDiffView.Update(key)
			m.focus.SetFocus(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
			m.syncFocusState()
			return cmd
		}
		return nil
	case component.FocusMergeDiffFileList:
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			m.mergeDiffView.UpdateFileList(key.String())
		}
		return nil
	case component.FocusDiffView:
		if m.diffView != nil {
			cmd := m.diffView.Update(key)
			m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
			m.syncFocusState()
			return cmd
		}
		return nil
	case component.FocusDiffFileList:
		if m.diffViewActive && m.diffView != nil {
			m.diffView.UpdateFileList(key.String())
		}
		return nil
	default:
		if !pane.IsPaneFocus(focused) {
			return nil
		}
		// Merge diff sub-pane: route keys to the merge diff view.
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			cmd := m.mergeDiffView.Update(key)
			m.focus.SetFocus(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
			m.syncFocusState()
			return cmd
		}
		// Diff sub-pane: route keys to the diff view.
		if m.diffViewActive && m.diffView != nil {
			cmd := m.diffView.Update(key)
			// Re-sync app-level focus after the diff view processes the key,
			// since ]f/[f and other actions may change the focused pane.
			m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
			m.syncFocusState()
			return cmd
		}
		pid := pane.PaneIDFromFocus(focused)
		if pid == m.previewPane {
			// Preview is read-only. Only scroll navigation and close/open actions.
			switch key.String() {
			case "alt+enter":
				m.dismissPreview()
				return nil
			case "enter":
				return m.openFromPreview()
			default:
				_, cmd := m.previewPanel.Update(key)
				return cmd
			}
		}
		if pid == m.mdPreviewPane {
			switch key.String() {
			case "alt+enter", "q":
				m.dismissMarkdownPreview()
				return nil
			default:
				_, cmd := m.mdPreviewPanel.Update(key)
				return cmd
			}
		}
		ps, ok := m.paneEditors[pid]
		if !ok {
			return nil
		}
		wasMod := ps.editor.Modified()
		comp, cmd := ps.editor.Update(key)
		ps.editor = comp.(*editor.Model)
		if ps.editor.Modified() != wasMod {
			m.refreshTabsModified()
		}
		line := ps.editor.CursorLine()
		col := ps.editor.CursorCol()
		if ps.editor.IsWordCharAtPos(line, col) {
			m.highlightLine = line
			m.highlightCol = col
			hlCmd := tea.Tick(highlightDebounce, func(_ time.Time) tea.Msg {
				return msg.LSPDocHighlightTickMsg{Line: line, Col: col}
			})
			return tea.Batch(cmd, hlCmd)
		}
		ps.editor.ClearHighlightRanges()
		return cmd
	}
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// statusBarHeight is the fixed height of the status bar (1 line).
const statusBarHeight = 1

// inputBorderSize is the vertical space consumed by the input border.
// Derived from: 1 char top + 1 char bottom = 2.
const inputBorderSize = 2

// inputMinContentLines is the minimum visible content lines in the input.
const inputMinContentLines = 1

// mainMinContentHeight is the minimum usable height for the main area.
// Derived from: border (2) + minimum 3 content rows for context.
const mainMinContentHeight = panelBorderSize + 3

// panelBorderSize is the space consumed by a rounded border on each axis.
// Derived from: 1 char per side × 2 sides = 2.
const panelBorderSize = 2

// leftPanelOverhead is the vertical space consumed by section chrome.
// Derived from: 2 headers (1 line each) + 1 divider (1 line + 1 top padding) = 4.
const leftPanelOverhead = 4

// inputMaxVisualLines computes the dynamic maximum content lines for the input.
// Derived from: total height - status bar - main area minimum - input border.
func (m *AppModel) inputMaxVisualLines() int {
	available := m.height - statusBarHeight - mainMinContentHeight - inputBorderSize
	proportional := max(m.height/10, inputMinContentLines)
	return max(min(available, proportional), inputMinContentLines)
}

// inputHeight returns the current rendered height of the input area
// based on its visual line count, clamped to [min, dynamic max] + border.
// The result is further constrained to ensure the main area retains
// at least 1 row — the input grows upward, never beyond available space.
func (m *AppModel) inputHeight() int {
	visualLines := m.input.VisualLineCount()
	maxLines := m.inputMaxVisualLines()
	lines := clampInt(visualLines, inputMinContentLines, maxLines)
	h := lines + inputBorderSize
	maxH := m.height - statusBarHeight - 1
	if maxH < inputMinContentLines+inputBorderSize {
		return inputMinContentLines + inputBorderSize
	}
	return min(h, maxH)
}

func (m *AppModel) recalcLayout() {
	// Reserve space for queue strip, input (dynamic), and status bar.
	queueH := m.promptQueue.ViewHeight()
	inputH := m.inputHeight()
	mainHeight := m.height - queueH - inputH - statusBarHeight
	mainHeight = max(mainHeight, 1)

	// Capture old chat viewport height before layout changes.
	oldChatViewH := m.chat.ViewportHeight()

	m.layout.SetSize(m.width, mainHeight)

	// Sync tab order and focus to the current layout mode so collapsed
	// panels are excluded from keyboard navigation.
	m.syncViewState()

	// Center panel: chat. Subtract border so content fits inside renderPanel().
	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	newChatViewH := max(chatH-panelBorderSize, 1)

	// Compensate viewport for input-induced height changes so the top
	// of the chat stays stable (bottom line is dropped, not the top).
	if inputH != m.prevInputH && oldChatViewH > 0 {
		m.chat.CompensateInputGrowth(oldChatViewH, newChatViewH)
	}

	m.chat.SetSize(max(chatW-panelBorderSize, 1), newChatViewH)

	// Left panel: split between session (top) and agent (bottom).
	leftW, leftH := m.layout.GetPanelSize(component.FocusSessionPanel)
	innerLeftW := max(leftW-panelBorderSize, 1)
	innerLeftH := max(leftH-panelBorderSize, 1)
	contentH := max(innerLeftH-leftPanelOverhead, 2)
	sessionH := contentH / 2
	agentH := contentH - sessionH
	m.sessionPanel.SetSize(innerLeftW, sessionH)
	m.agentPanel.SetSize(innerLeftW, agentH)

	// File tree panel.
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	m.fileTree.SetSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))

	// Right panel: code viewer (and knowledge, same dimensions).
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.codePanel.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
	m.knowledgePanel.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
	m.resizeCodePanelForPreview(rightW, rightH)

	// Git mode panels reuse file tree and code viewer slot dimensions.
	if m.gitPanel != nil {
		m.gitPanel.SetSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))
		m.commitTree.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
	}
	if m.diffView != nil {
		m.diffView.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
		m.diffView.SetFileListSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))
	}
	if m.mergeDiffView != nil {
		m.mergeDiffView.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
		m.mergeDiffView.SetFileListSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))
	}
	if m.conflictView != nil {
		m.conflictView.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
		m.conflictView.SetFileListSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))
	}

	// Input: dynamic height based on content.
	m.input.SetSize(m.width, inputH)
	m.statusBar.SetSize(m.width, statusBarHeight)

	// Overlays get full terminal dimensions.
	m.editorOverlay.SetSize(m.width, m.height)
	m.modalOverlay.SetSize(m.width, m.height)
	m.searchOverlay.SetSize(m.width, m.height)
	m.fieldManualOverlay.SetSize(m.width, m.height)
	m.loginPanel.SetSize(m.width, mainHeight)

	// Compositor: rebuild frame structure (full invalidation).
	m.comp.SetStructure(m.compositorColumns(), mainHeight, queueH, inputH, statusBarHeight)
	m.prevInputH = inputH
}

// handleInputGrowth performs a targeted layout update when the input panel
// grows (user added lines). Only the chat slot and input are re-rendered;
// side panels keep their cached output truncated to the new main-area
// height with the bottom border preserved. This limits the terminal diff
// to ~4-5 boundary rows instead of a full-screen redraw.
func (m *AppModel) handleInputGrowth(newInputH int) {
	queueH := m.promptQueue.ViewHeight()
	mainHeight := m.height - queueH - newInputH - statusBarHeight
	mainHeight = max(mainHeight, 1)

	// Capture old chat viewport height.
	oldChatViewH := m.chat.ViewportHeight()

	// Update layout to get correct chat panel dimensions.
	m.layout.SetSize(m.width, mainHeight)

	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	newChatViewH := max(chatH-panelBorderSize, 1)

	// Compensate viewport: keep top line stable, drop bottom.
	if oldChatViewH > 0 {
		m.chat.CompensateInputGrowth(oldChatViewH, newChatViewH)
	}
	m.chat.SetSize(max(chatW-panelBorderSize, 1), newChatViewH)

	// Resize input to new height.
	m.input.SetSize(m.width, newInputH)

	// NOTE: left/right/filetree/session/agent panels are deliberately NOT
	// resized. Their cached compositor output is truncated below, keeping
	// their rendered content byte-identical for all rows except the border.

	// Determine which compositor slot contains the chat panel.
	chatSlot := m.chatCompositorSlot()

	// Targeted compositor update — only chat slot + queue + input dirty.
	m.comp.AdjustVerticalSections(mainHeight, queueH, newInputH, chatSlot)

	// Truncate every main-area side-panel slot EXCEPT the chat slot.
	for _, slot := range []compositor.SlotID{
		compositor.SlotLeft,
		compositor.SlotCenterLeft,
		compositor.SlotCenter,
		compositor.SlotRight,
	} {
		if slot != chatSlot {
			m.comp.TruncateSlot(slot, mainHeight)
		}
	}

	m.prevInputH = newInputH
}

// chatCompositorSlot returns the compositor slot that holds the chat panel
// in the current layout mode.
func (m *AppModel) chatCompositorSlot() compositor.SlotID {
	switch m.layout.Mode() {
	case layout.TwoColumn:
		return compositor.SlotRight
	case layout.SingleColumn:
		return compositor.SlotLeft
	default:
		return compositor.SlotCenter
	}
}

// resizeCodePanelForPreview computes sizes for all panes within the code
// panel slot using the pane tree's ComputeLayout.
func (m *AppModel) resizeCodePanelForPreview(rightW, rightH int) {
	if m.viewMode != ViewEdit {
		return
	}
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)

	// Full preview mode: preview active, single editor pane with no tabs.
	if m.hasPreview() && len(m.paneEditors) == 1 && len(m.focusedTabOrder()) == 0 {
		m.previewPanel.SetSize(innerW, max(innerH-tabbar.Height, 1))
		return
	}

	// Full markdown preview mode: mdPreview active, sole editor has no tabs.
	if m.isFullMdPreview() {
		m.mdPreviewPanel.SetSize(innerW, max(innerH-tabbar.Height, 1))
		return
	}

	// Single editor pane, no splits.
	if m.paneTree.IsLeaf() {
		m.focusedEditor().SetSize(innerW, max(innerH-m.tabBarHeight(), 1))
		return
	}

	// Multi-pane: compute layout and apply sizes.
	contentH := max(innerH-1, 1) // Reserve status line row.
	area := pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
	rects := m.paneTree.ComputeLayout(area)

	for id, r := range rects {
		if id == m.previewPane {
			m.previewPanel.SetSize(r.W, r.H-tabbar.Height)
			continue
		}
		if id == m.mdPreviewPane {
			m.mdPreviewPanel.SetSize(r.W, max(r.H-tabbar.Height, 1))
			continue
		}
		ps := m.paneEditors[id]
		tbH := 0
		if len(ps.tabOrder) > 0 {
			tbH = tabbar.Height
		}
		// +1 because editor.viewportHeight() subtracts 1 for the status
		// line, which is rendered separately at the bottom.
		ps.editor.SetSize(r.W, r.H-tbH+1)
	}
}

// syncViewState rebuilds the dual view cycling rings for the current layout
// mode, updates the focus tab order to match visible panels, and pushes the
// ring indicator to the status bar. Called on layout recompute and after cycling.
func (m *AppModel) syncViewState() {
	mode := m.layout.Mode()
	wasEmpty := m.leftRing.empty() && m.rightRing.empty()

	// Rebuild rings per mode (see plan table).
	// In git mode, FourColumn gets a ring so Alt+V can cycle between panels.
	switch mode {
	case layout.FourColumn:
		m.leftRing.reset([]component.FocusID{
			component.FocusSessionPanel,
			component.FocusFileTree,
			component.FocusChat,
			component.FocusCodeViewer,
			component.FocusGitPanel,
			component.FocusCommitTree,
		})
		m.rightRing.reset(nil)
	case layout.ThreeColumn:
		m.leftRing.reset([]component.FocusID{
			component.FocusSessionPanel,
			component.FocusFileTree,
			component.FocusGitPanel,
		})
		m.rightRing.reset([]component.FocusID{
			component.FocusCodeViewer,
			component.FocusCommitTree,
		})
	case layout.TwoColumn:
		m.leftRing.reset([]component.FocusID{
			component.FocusSessionPanel,
			component.FocusFileTree,
			component.FocusGitPanel,
		})
		m.rightRing.reset([]component.FocusID{
			component.FocusChat,
			component.FocusCodeViewer,
			component.FocusCommitTree,
		})
	default:
		m.leftRing.reset([]component.FocusID{
			component.FocusSessionPanel,
			component.FocusChat,
			component.FocusFileTree,
			component.FocusCodeViewer,
			component.FocusGitPanel,
			component.FocusCommitTree,
		})
		m.rightRing.reset(nil)
	}

	// When chat mode is active, ensure Chat is visible in the right
	// ring. In ThreeColumn+ Chat has a dedicated column and is absent
	// from the right ring, so setTo is a no-op. In TwoColumn, Chat
	// shares the right ring and must be selected — otherwise a
	// ThreeColumn→TwoColumn transition preserves the previous panel
	// (CodeViewer) and the chat panel disappears.
	if m.viewMode == ViewChat {
		if !m.rightRing.empty() {
			m.rightRing.setTo(component.FocusChat)
		}
	}

	// When edit mode is active, ensure CodeViewer is visible in the
	// ring that contains it. For the left ring, prefer CodeViewer
	// (SingleColumn) or fall back to FileTree for navigation.
	if m.viewMode == ViewEdit {
		if !m.rightRing.empty() {
			m.rightRing.setTo(component.FocusCodeViewer)
		}
		if !m.leftRing.empty() {
			if !m.leftRing.setTo(component.FocusCodeViewer) {
				m.leftRing.setTo(component.FocusFileTree)
			}
		}
	}

	// When git mode is active, position rings on git panels so the
	// ring hint shows "Git" and "Tree" instead of chat-mode panels.
	// Skip if the ring is already on a git panel — this allows
	// single-column cycling between GitPanel and CommitTree.
	if m.viewMode == ViewGit {
		if !m.leftRing.empty() && !isGitPanel(m.leftRing.current()) {
			m.leftRing.setTo(component.FocusGitPanel)
		}
		if !m.rightRing.empty() && !isGitPanel(m.rightRing.current()) {
			m.rightRing.setTo(component.FocusCommitTree)
		}
	}

	// When the diff view is active, replace commit tree and git panel with
	// diff-specific panels so Alt+V cycling and the ring hint show them.
	if m.diffViewActive {
		replaceInRing(&m.leftRing, component.FocusCommitTree, component.FocusDiffView)
		replaceInRing(&m.rightRing, component.FocusCommitTree, component.FocusDiffView)
		replaceInRing(&m.leftRing, component.FocusGitPanel, component.FocusDiffFileList)
		replaceInRing(&m.rightRing, component.FocusGitPanel, component.FocusDiffFileList)
	}

	// When the merge diff view is active, replace commit tree and git panel
	// with merge-diff-specific panels.
	if m.mergeDiffViewActive {
		replaceInRing(&m.leftRing, component.FocusCommitTree, component.FocusMergeDiffView)
		replaceInRing(&m.rightRing, component.FocusCommitTree, component.FocusMergeDiffView)
		replaceInRing(&m.leftRing, component.FocusGitPanel, component.FocusMergeDiffFileList)
		replaceInRing(&m.rightRing, component.FocusGitPanel, component.FocusMergeDiffFileList)
	}

	// When the conflict view is active, replace commit tree and git panel.
	if m.conflictViewActive {
		replaceInRing(&m.leftRing, component.FocusCommitTree, component.FocusConflictView)
		replaceInRing(&m.rightRing, component.FocusCommitTree, component.FocusConflictView)
		replaceInRing(&m.leftRing, component.FocusGitPanel, component.FocusConflictFileList)
		replaceInRing(&m.rightRing, component.FocusGitPanel, component.FocusConflictFileList)
	}

	// First-collapse flash: show once when panels first collapse.
	nowActive := !m.leftRing.empty() || !m.rightRing.empty()
	if wasEmpty && nowActive && !m.collapseHintShown {
		m.statusBar.SetFlash("Panel collapsed \u2014 Alt+V \u2190/\u2192 to cycle views")
		m.collapseHintShown = true
	}

	// Update tab order based on mode + which panels the rings are showing.
	order := m.tabOrderForView(mode)
	m.focus.SetTabOrder(order)
	m.syncFocusState()

	// Push ring indicator to the status bar.
	m.statusBar.SetViewRingHint(m.buildRingHint())
}

// tabOrderForView returns the focus cycling order for the current mode and
// ring state. Ring-active panels replace fixed panels in the order.
func (m *AppModel) tabOrderForView(mode layout.LayoutMode) []component.FocusID {
	switch mode {
	case layout.FourColumn:
		order := []component.FocusID{
			component.FocusInput,
			component.FocusChat,
			component.FocusSessionPanel,
			component.FocusAgentPanel,
			component.FocusFileTree,
		}
		return m.appendCodePanelFocus(order)
	case layout.ThreeColumn:
		left := m.leftRing.current()
		order := []component.FocusID{
			component.FocusInput,
			component.FocusChat,
			left,
		}
		if left == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		return m.appendCodePanelFocus(order)
	case layout.TwoColumn:
		left := m.leftRing.current()
		right := m.rightRing.current()
		order := []component.FocusID{
			component.FocusInput,
			right,
			left,
		}
		if left == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		return m.appendCodePanelFocus(order)
	default:
		// SingleColumn: Input + whatever the left ring shows.
		active := m.leftRing.current()
		order := []component.FocusID{component.FocusInput, active}
		if active == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		return m.appendCodePanelFocus(order)
	}
}

// appendCodePanelFocus appends the code panel's focus target to the tab
// order. In edit mode, this is the focused pane's dynamic ID (replacing
// any FocusCodeViewer entry from ring state). In non-edit mode, this is
// FocusCodeViewer. Preview-only mode (no editor tabs) skips the tab order
// since Tab cannot meaningfully target a read-only preview.
func (m *AppModel) appendCodePanelFocus(order []component.FocusID) []component.FocusID {
	// Git mode: replace FileTree/CodeViewer with git-specific panels.
	// When the diff view is active, use diff-specific IDs instead so
	// that SetTabOrder preserves focus on diff sub-panes (matching the
	// edit-mode pattern where the focused editor pane ID is in the order).
	if m.viewMode == ViewGit {
		for i, id := range order {
			switch id {
			case component.FocusFileTree:
				if m.conflictViewActive {
					order[i] = component.FocusConflictFileList
				} else if m.mergeDiffViewActive {
					order[i] = component.FocusMergeDiffFileList
				} else if m.diffViewActive {
					order[i] = component.FocusDiffFileList
				} else {
					order[i] = component.FocusGitPanel
				}
			case component.FocusCodeViewer, component.FocusDiffView, component.FocusMergeDiffView, component.FocusConflictView:
				if m.conflictViewActive {
					order[i] = component.FocusConflictView
				} else if m.mergeDiffViewActive && m.mergeDiffView != nil {
					order[i] = pane.PaneFocusID(m.mergeDiffView.FocusedPane())
				} else if m.diffViewActive && m.diffView != nil {
					order[i] = pane.PaneFocusID(m.diffView.FocusedPane())
				} else {
					order[i] = component.FocusCommitTree
				}
			}
		}
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			fid := pane.PaneFocusID(m.mergeDiffView.FocusedPane())
			if !slices.Contains(order, fid) {
				order = append(order, fid)
			}
		} else if m.diffViewActive && m.diffView != nil {
			fid := pane.PaneFocusID(m.diffView.FocusedPane())
			if !slices.Contains(order, fid) {
				order = append(order, fid)
			}
		} else {
			if !slices.Contains(order, component.FocusCommitTree) {
				order = append(order, component.FocusCommitTree)
			}
		}
		return order
	}
	if m.viewMode == ViewEdit {
		// Full-preview mode: nothing to tab to in the code column.
		if m.hasPreview() && len(m.focusedTabOrder()) == 0 {
			return order
		}
		// Full markdown preview mode: tab to the mdPreview pane.
		if m.isFullMdPreview() {
			fid := pane.PaneFocusID(m.mdPreviewPane)
			for i, id := range order {
				if id == component.FocusCodeViewer {
					order[i] = fid
					return order
				}
			}
			return append(order, fid)
		}
		fid := pane.PaneFocusID(m.focusedPane)
		// Replace any FocusCodeViewer entry injected by the ring.
		for i, id := range order {
			if id == component.FocusCodeViewer {
				order[i] = fid
				return order
			}
		}
		return append(order, fid)
	}
	// Non-edit mode: use FocusCodeViewer.
	for _, id := range order {
		if id == component.FocusCodeViewer {
			return order
		}
	}
	return append(order, component.FocusCodeViewer)
}

// panelDisplayNames maps panel IDs to short labels for the status bar ring.
var panelDisplayNames = map[component.FocusID]string{
	component.FocusChat:         "Chat",
	component.FocusCodeViewer:   "Code",
	component.FocusSessionPanel: "Sess",
	component.FocusFileTree:     "Files",
	component.FocusGitPanel:     "Git",
	component.FocusCommitTree:   "Tree",
	component.FocusDiffView:     "Diff",
	component.FocusDiffFileList: "Files",
}

// buildRingHint returns the formatted ring indicator string for the status bar.
// Returns "" when both rings are empty (FourColumn).
// When both rings are active, shows: ◀ Sess ● Tree ○ | Chat ● Code ○ ▶
func (m *AppModel) buildRingHint() string {
	if m.leftRing.empty() && m.rightRing.empty() {
		return ""
	}
	th := m.config.Theme()
	currentStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	activeStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)

	renderRing := func(ring *viewRing) []string {
		var items []string
		for i, pid := range ring.panels {
			name := panelDisplayNames[pid]
			var indicator string
			switch {
			case i == ring.index:
				indicator = currentStyle.Render(name + " \u25cf")
			case pid == component.FocusChat && m.chat.IsStreaming():
				indicator = activeStyle.Render(name + " \u2731")
			default:
				indicator = dimStyle.Render(name + " \u25cb")
			}
			items = append(items, indicator)
		}
		return items
	}

	var parts []string
	parts = append(parts, dimStyle.Render("\u25c0"))

	if !m.leftRing.empty() {
		parts = append(parts, renderRing(&m.leftRing)...)
	}

	if !m.leftRing.empty() && !m.rightRing.empty() {
		parts = append(parts, dimStyle.Render("|"))
	}

	if !m.rightRing.empty() {
		parts = append(parts, renderRing(&m.rightRing)...)
	}

	parts = append(parts, dimStyle.Render("\u25b6"))
	return strings.Join(parts, " ")
}

// compositorColumns returns the slot IDs for the current layout mode's columns.
func (m *AppModel) compositorColumns() []compositor.SlotID {
	switch m.layout.Mode() {
	case layout.FourColumn:
		return []compositor.SlotID{
			compositor.SlotLeft, compositor.SlotCenterLeft,
			compositor.SlotCenter, compositor.SlotRight,
		}
	case layout.ThreeColumn:
		return []compositor.SlotID{
			compositor.SlotLeft, compositor.SlotCenter, compositor.SlotRight,
		}
	case layout.TwoColumn:
		return []compositor.SlotID{compositor.SlotLeft, compositor.SlotRight}
	default:
		return []compositor.SlotID{compositor.SlotLeft}
	}
}

// focusBorderGroup maps the current focus to the compositor slot whose
// border would change color on focus transitions.
func (m *AppModel) focusBorderGroup() compositor.SlotID {
	fid := m.focus.Current()
	switch fid {
	case component.FocusSessionPanel, component.FocusAgentPanel:
		return compositor.SlotLeft
	case component.FocusFileTree:
		if m.layout.Mode() == layout.FourColumn {
			return compositor.SlotCenterLeft
		}
		return compositor.SlotLeft
	case component.FocusChat:
		switch m.layout.Mode() {
		case layout.TwoColumn:
			return compositor.SlotRight
		case layout.SingleColumn:
			return compositor.SlotLeft
		default:
			return compositor.SlotCenter
		}
	case component.FocusCodeViewer, component.FocusCommitTree, component.FocusDiffView:
		return compositor.SlotRight
	case component.FocusGitPanel, component.FocusDiffFileList:
		if m.layout.Mode() == layout.FourColumn {
			return compositor.SlotCenterLeft
		}
		return compositor.SlotLeft
	case component.FocusInput:
		return compositor.SlotInput
	default:
		if pane.IsPaneFocus(fid) {
			return compositor.SlotRight
		}
		return compositor.SlotCenter
	}
}

// detectDirtySlots checks component dirty state and state transitions,
// marking the appropriate compositor slots for re-rendering.
func (m *AppModel) detectDirtySlots() {
	// Overlay transitions: full invalidation.
	if m.overlay != m.prevOverlay {
		m.comp.InvalidateAll()
		m.prevOverlay = m.overlay
		return
	}

	// Edit mode transitions: code panel changes completely.
	if m.viewMode == ViewEdit != m.prevEditMode {
		m.comp.InvalidateAll()
		m.prevEditMode = m.viewMode == ViewEdit
		return
	}

	// Git mode transitions: panels swap completely.
	if (m.viewMode == ViewGit) != (m.prevGitMode == ViewGit) {
		m.comp.InvalidateAll()
		m.prevGitMode = m.viewMode
		return
	}

	// Input height change: when the input GROWS and all main-area slots
	// have cached output, use a targeted update that only re-renders the
	// center column and input. Side panels keep truncated cached output,
	// avoiding the content shift that causes full-screen flicker.
	// When the input SHRINKS (submit/delete), fall back to full recalcLayout.
	if newInputH := m.inputHeight(); newInputH != m.prevInputH {
		if newInputH > m.prevInputH && m.comp.AllMainSlotsCached() {
			m.handleInputGrowth(newInputH)
		} else {
			m.recalcLayout()
		}
		return
	}

	// Focus border group change: mark old + new groups dirty.
	curGrp := m.focusBorderGroup()
	if curGrp != m.prevFocusGrp {
		m.comp.MarkDirty(m.prevFocusGrp)
		m.comp.MarkDirty(curGrp)
		m.prevFocusGrp = curGrp
	}

	// Chord state change: the chord hint overlay can appear on any content
	// slot depending on layout mode, so invalidate all slots.
	if m.chord != m.prevChord {
		m.comp.InvalidateAll()
		m.prevChord = m.chord
	}

	// Ring cycling: mark affected slot dirty.
	if m.leftRing.index != m.prevLeftRing {
		m.comp.MarkDirty(compositor.SlotLeft)
		m.prevLeftRing = m.leftRing.index
	}
	if m.rightRing.index != m.prevRightRing {
		m.comp.MarkDirty(compositor.SlotRight)
		m.prevRightRing = m.rightRing.index
	}

	// Tab hover state change: close icon highlight, markdown tooltip.
	hoverKey := [5]int{m.tabHoverClose, int(m.tabHoverPane), m.previewTabHoverClose, m.mdPreviewTabHoverClose, m.mdTooltipTab}
	if hoverKey != m.prevHoverKey {
		m.comp.MarkDirty(compositor.SlotRight)
		m.prevHoverKey = hoverKey
	}

	// Component dirty checks.
	if m.chat.ViewDirty() {
		switch m.layout.Mode() {
		case layout.TwoColumn:
			m.comp.MarkDirty(compositor.SlotRight)
		case layout.SingleColumn:
			m.comp.MarkDirty(compositor.SlotLeft)
		default:
			m.comp.MarkDirty(compositor.SlotCenter)
		}
	}
	if m.fileTree.ViewDirty() {
		if m.layout.Mode() == layout.FourColumn {
			m.comp.MarkDirty(compositor.SlotCenterLeft)
		} else {
			m.comp.MarkDirty(compositor.SlotLeft)
		}
	}
	if m.input.ViewDirty() {
		m.comp.MarkDirty(compositor.SlotInput)
	}
	if m.statusBar.ViewDirty() {
		m.comp.MarkDirty(compositor.SlotStatus)
	}

	// Editor dirty check.
	if m.viewMode == ViewEdit {
		if ps := m.paneEditors[m.focusedPane]; ps != nil && ps.editor.ViewDirty() {
			m.comp.MarkDirty(compositor.SlotRight)
		}
	}

	// Git mode dirty checks — skip when diff view is overlaying.
	if m.viewMode == ViewGit && !m.diffViewActive && m.gitPanel != nil {
		// Keep commit tree staged-files state in sync every frame.
		// syncStagedFiles is cheap (iterates a small slice) and ensures
		// the [Commit] badge reacts immediately to staging changes.
		m.syncStagedFiles()
		if m.gitPanel.ViewDirty() {
			if m.layout.Mode() == layout.FourColumn {
				m.comp.MarkDirty(compositor.SlotCenterLeft)
			} else {
				m.comp.MarkDirty(compositor.SlotLeft)
			}
		}
		if m.commitTree.ViewDirty() {
			m.comp.MarkDirty(compositor.SlotRight)
		}
	}

	// Diff view dirty check.
	if m.diffViewActive && m.diffView != nil {
		if m.diffView.ViewDirty() {
			m.comp.MarkDirty(compositor.SlotRight)
		}
		if m.diffView.FileListDirty() {
			if m.layout.Mode() == layout.FourColumn {
				m.comp.MarkDirty(compositor.SlotCenterLeft)
			} else {
				m.comp.MarkDirty(compositor.SlotLeft)
			}
		}
	}

	// Conflict view dirty check.
	if m.conflictViewActive && m.conflictView != nil {
		if m.conflictView.ViewDirty() {
			m.comp.MarkDirty(compositor.SlotRight)
		}
		if m.conflictView.FileListDirty() {
			if m.layout.Mode() == layout.FourColumn {
				m.comp.MarkDirty(compositor.SlotCenterLeft)
			} else {
				m.comp.MarkDirty(compositor.SlotLeft)
			}
		}
	}

	// Merge diff view dirty check.
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		if m.mergeDiffView.ViewDirty() {
			m.comp.MarkDirty(compositor.SlotRight)
		}
		if m.mergeDiffView.FileListDirty() {
			if m.layout.Mode() == layout.FourColumn {
				m.comp.MarkDirty(compositor.SlotCenterLeft)
			} else {
				m.comp.MarkDirty(compositor.SlotLeft)
			}
		}
	}

	// Preview panel: no view cache, so mark dirty whenever it is focused
	// (cursor blink changes its output on every blink cycle).
	if m.hasPreview() && m.isPreviewFocused() {
		m.comp.MarkDirty(compositor.SlotRight)
	}

	// Blink phase changed: mark the slot(s) owning the active cursor.
	if m.blinkDirty {
		m.blinkDirty = false
		if m.viewMode == ViewEdit {
			m.comp.MarkDirty(compositor.SlotRight)
		}
		if m.focus.Current() == component.FocusInput {
			m.comp.MarkDirty(compositor.SlotInput)
		}
		if m.fileTree.NeedsBlink() {
			if m.layout.Mode() == layout.FourColumn {
				m.comp.MarkDirty(compositor.SlotCenterLeft)
			} else {
				m.comp.MarkDirty(compositor.SlotLeft)
			}
		}
		if m.hasPreview() && m.isPreviewFocused() {
			m.comp.MarkDirty(compositor.SlotRight)
		}
	}

	// Left panel (session+agent): no viewDirty — always mark if main dirty.
	// The compositor avoids re-rendering unless the slot is already dirty.
}

// renderDirtySlots re-renders only the compositor slots that are dirty.
func (m *AppModel) renderDirtySlots() {
	th := m.config.Theme()
	m.applyFocusRingShimmer(th)

	if m.comp.IsDirty(compositor.SlotLeft) {
		m.comp.SetSlotLines(compositor.SlotLeft,
			compositor.SplitLines(m.renderSlotLeft(th)))
	}
	if m.comp.IsDirty(compositor.SlotCenterLeft) {
		m.comp.SetSlotLines(compositor.SlotCenterLeft,
			compositor.SplitLines(m.renderSlotCenterLeft(th)))
	}
	if m.comp.IsDirty(compositor.SlotCenter) {
		m.comp.SetSlotLines(compositor.SlotCenter,
			compositor.SplitLines(m.renderSlotCenter(th)))
	}
	if m.comp.IsDirty(compositor.SlotRight) {
		m.comp.SetSlotLines(compositor.SlotRight,
			compositor.SplitLines(m.renderSlotRight(th)))
	}
	if m.comp.IsDirty(compositor.SlotQueue) {
		m.comp.SetSlotLines(compositor.SlotQueue,
			compositor.SplitLines(m.renderQueueStrip()))
	}
	if m.comp.IsDirty(compositor.SlotInput) {
		m.comp.SetSlotLines(compositor.SlotInput,
			compositor.SplitLines(m.input.View(m.cursorVisible)))
	}
	if m.comp.IsDirty(compositor.SlotStatus) {
		m.comp.SetSlotLines(compositor.SlotStatus,
			compositor.SplitLines(m.statusBar.View()))
	}
}

// applyFocusRingShimmer sets per-character gradient border rendering on the
// theme, replacing the old per-side approach that produced visible color
// jumps at corners. Called once per frame before any slot rendering.
func (m *AppModel) applyFocusRingShimmer(th *theme.Theme) {
	if m.focusGradient == nil {
		return
	}
	elapsed := time.Since(m.focusRingStart)
	g := m.focusGradient

	th.ActiveBorderRender = func(content string, innerW, innerH, maxH int) string {
		return theme.RenderGradientBorder(content, g, elapsed, innerW, innerH, maxH)
	}
	m.input.SetFocusBorderRender(th.ActiveBorderRender)
}

// renderQueueStrip renders the prompt queue between chat and input.
// Returns empty string when the queue is empty (0 lines).
func (m *AppModel) renderQueueStrip() string {
	if m.promptQueue.IsEmpty() {
		return ""
	}
	var grad *theme.Gradient
	if !m.promptQueue.IsPaused() {
		grad = m.queueGradient
	}
	elapsed := time.Since(m.focusRingStart) // Reuse app-level animation clock.
	pal := m.config.Theme().Palette
	return m.promptQueue.View(m.width, elapsed, grad, pal)
}

// renderSlotLeft renders the bordered content for the left column slot.
// In FourColumn mode this is always the session+agent panel. In collapsed
// modes the left ring determines which panel occupies this slot.
func (m *AppModel) renderSlotLeft(th *theme.Theme) string {
	switch m.layout.Mode() {
	case layout.FourColumn:
		return m.renderLeftPanelBordered(th)
	case layout.SingleColumn:
		return m.renderSingleColumnSlot(th)
	default:
		return m.renderLeftSlot(m.leftRing.current(), th)
	}
}

// renderSlotCenterLeft renders the bordered file tree for the center-left slot.
// In git mode, renders the git explorer panel instead.
func (m *AppModel) renderSlotCenterLeft(th *theme.Theme) string {
	if m.conflictViewActive && m.conflictView != nil {
		return m.renderConflictFileListBordered(th)
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.renderMergeDiffFileListBordered(th)
	}
	if m.diffViewActive && m.diffView != nil {
		return m.renderDiffFileListBordered(th)
	}
	if m.viewMode == ViewGit {
		return m.renderGitPanelBordered(th)
	}
	return m.renderPanel(m.fileTree.View(m.cursorVisible), component.FocusFileTree, th)
}

// renderSlotCenter renders the bordered chat for the center slot.
func (m *AppModel) renderSlotCenter(th *theme.Theme) string {
	return m.renderPanel(
		m.overlayChordHint(m.chat.View(), component.FocusChat, th),
		component.FocusChat, th)
}

// renderSlotRight renders the bordered code panel for the right slot.
// In TwoColumn mode the right ring may show chat, commit tree, or code.
// In git mode (non-TwoColumn), renders the commit tree instead of the code panel.
func (m *AppModel) renderSlotRight(th *theme.Theme) string {
	if m.conflictViewActive && m.conflictView != nil {
		return m.renderConflictViewBordered(th)
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.renderMergeDiffViewBordered(th)
	}
	if m.diffViewActive {
		return m.renderDiffViewBordered(th)
	}
	if m.layout.Mode() == layout.TwoColumn {
		right := m.rightRing.current()
		switch right {
		case component.FocusCodeViewer:
			return m.renderCodePanelBordered(th)
		case component.FocusCommitTree:
			return m.renderGitCommitTreeBordered(th)
		default:
			content := m.overlayChordHint(m.panelContent(right), right, th)
			return m.renderPanel(content, right, th)
		}
	}
	if m.viewMode == ViewGit {
		return m.renderGitCommitTreeBordered(th)
	}
	return m.renderCodePanelBordered(th)
}

// renderSingleColumnSlot renders the single visible panel in SingleColumn mode.
func (m *AppModel) renderSingleColumnSlot(th *theme.Theme) string {
	active := m.leftRing.current()

	switch active {
	case component.FocusSessionPanel:
		content := m.overlayChordHint(m.renderLeftPanel(th), active, th)
		return m.borderLeftPanel(content, th)
	case component.FocusCodeViewer:
		return m.renderCodePanelBordered(th)
	case component.FocusFileTree:
		if m.conflictViewActive && m.conflictView != nil {
			return m.renderConflictFileListBordered(th)
		}
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			return m.renderMergeDiffFileListBordered(th)
		}
		if m.diffViewActive && m.diffView != nil {
			return m.renderDiffFileListBordered(th)
		}
		content := m.overlayChordHint(m.fileTree.View(m.cursorVisible), active, th)
		return m.renderPanel(content, active, th)
	case component.FocusGitPanel:
		return m.renderGitPanelBordered(th)
	case component.FocusCommitTree:
		return m.renderGitCommitTreeBordered(th)
	case component.FocusConflictView:
		if m.conflictViewActive && m.conflictView != nil {
			return m.renderConflictViewBordered(th)
		}
		return m.renderCodePanelBordered(th)
	case component.FocusConflictFileList:
		if m.conflictViewActive && m.conflictView != nil {
			return m.renderConflictFileListBordered(th)
		}
		return m.renderGitPanelBordered(th)
	case component.FocusMergeDiffView:
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			return m.renderMergeDiffViewBordered(th)
		}
		return m.renderCodePanelBordered(th)
	case component.FocusMergeDiffFileList:
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			return m.renderMergeDiffFileListBordered(th)
		}
		return m.renderGitPanelBordered(th)
	case component.FocusDiffView:
		if m.diffViewActive && m.diffView != nil {
			return m.renderDiffViewBordered(th)
		}
		return m.renderCodePanelBordered(th)
	case component.FocusDiffFileList:
		if m.diffViewActive && m.diffView != nil {
			return m.renderDiffFileListBordered(th)
		}
		return m.renderGitPanelBordered(th)
	default:
		if m.conflictViewActive && m.conflictView != nil {
			return m.renderConflictViewBordered(th)
		}
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			return m.renderMergeDiffViewBordered(th)
		}
		if m.diffViewActive && m.diffView != nil {
			return m.renderDiffViewBordered(th)
		}
		content := m.overlayChordHint(m.panelContent(active), active, th)
		return m.renderPanel(content, active, th)
	}
}

// renderLeftSlot renders the left column for the given panel ID.
// Session gets the composite left panel with border; FileTree and GitPanel
// get their respective panel renderers.
func (m *AppModel) renderLeftSlot(id component.FocusID, th *theme.Theme) string {
	switch id {
	case component.FocusFileTree:
		if m.conflictViewActive && m.conflictView != nil {
			return m.renderConflictFileListBordered(th)
		}
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			return m.renderMergeDiffFileListBordered(th)
		}
		if m.diffViewActive && m.diffView != nil {
			return m.renderDiffFileListBordered(th)
		}
		return m.renderPanel(m.fileTree.View(m.cursorVisible), component.FocusFileTree, th)
	case component.FocusGitPanel:
		return m.renderGitPanelBordered(th)
	case component.FocusConflictFileList:
		if m.conflictViewActive && m.conflictView != nil {
			return m.renderConflictFileListBordered(th)
		}
		return m.renderLeftPanelBordered(th)
	case component.FocusMergeDiffFileList:
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			return m.renderMergeDiffFileListBordered(th)
		}
		return m.renderLeftPanelBordered(th)
	case component.FocusDiffFileList:
		if m.diffViewActive && m.diffView != nil {
			return m.renderDiffFileListBordered(th)
		}
		return m.renderLeftPanelBordered(th)
	default:
		return m.renderLeftPanelBordered(th)
	}
}

// panelContent returns the raw view content for a swappable panel.
func (m *AppModel) panelContent(id component.FocusID) string {
	switch id {
	case component.FocusChat:
		return m.chat.View()
	case component.FocusCodeViewer:
		if m.viewMode == ViewGit {
			return m.commitTree.View(m.cursorVisible)
		}
		return m.codePanelView()
	case component.FocusFileTree:
		if m.viewMode == ViewGit {
			return m.gitPanel.View(m.cursorVisible)
		}
		return m.fileTree.View(m.cursorVisible)
	case component.FocusGitPanel:
		return m.gitPanel.View(m.cursorVisible)
	case component.FocusCommitTree:
		return m.commitTree.View(m.cursorVisible)
	case component.FocusDiffFileList:
		if m.diffView != nil {
			return m.diffView.FileListView(m.cursorVisible)
		}
		return ""
	default:
		return ""
	}
}

// chordHintLines is the number of lines consumed by the chord hint overlay
// (label row + divider row).
const chordHintLines = 2

// overlayChordHint prepends the chord hint bar to content when a chord is active.
// The content is trimmed from the bottom so the total line count stays within
// the panel's inner height budget, preventing border clipping from MaxHeight.
func (m *AppModel) overlayChordHint(content string, panelID component.FocusID, th *theme.Theme) string {
	hint := m.chordHint(th)
	if hint == "" {
		return content
	}
	w, _ := m.layout.GetPanelSize(panelID)
	innerW := max(w-panelBorderSize, 1)
	hintWidth := lipgloss.Width(hint)
	pad := max(innerW-hintWidth, 0)
	divider := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", innerW))

	// Replace the first two lines of content with hint + divider
	// so the remaining content stays in place (no layout shift).
	lines := strings.Split(content, "\n")
	hintLine := strings.Repeat(" ", pad) + hint
	if len(lines) >= chordHintLines {
		lines[0] = hintLine
		lines[1] = divider
	} else {
		lines = append([]string{hintLine, divider}, lines...)
	}
	return strings.Join(lines, "\n")
}

// isLeftPanelFocused returns true when either sub-section of the left panel has focus.
func (m *AppModel) isLeftPanelFocused() bool {
	return m.focus.IsFocused(component.FocusSessionPanel) || m.focus.IsFocused(component.FocusAgentPanel)
}

// renderLeftPanelBordered wraps the left panel content in a border that activates
// when either sessions or agents is focused.
func (m *AppModel) renderLeftPanelBordered(th *theme.Theme) string {
	return m.borderLeftPanel(m.renderLeftPanel(th), th)
}

// borderLeftPanel wraps arbitrary content in the left panel's border frame.
func (m *AppModel) borderLeftPanel(content string, th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(component.FocusSessionPanel)
	return m.renderBordered(content, m.isLeftPanelFocused(), w, h, th)
}

// renderLeftPanel stacks sessions and agents with line-extended headers and a divider.
func (m *AppModel) renderLeftPanel(th *theme.Theme) string {
	leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
	innerW := max(leftW-panelBorderSize, 1)

	sessionsFocused := m.focus.IsFocused(component.FocusSessionPanel)
	agentsFocused := m.focus.IsFocused(component.FocusAgentPanel)

	dividerStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)
	divider := lipgloss.NewStyle().PaddingTop(1).Render(
		dividerStyle.Render(strings.Repeat("─", innerW)),
	)

	return strings.Join([]string{
		sectionHeader("Sessions", innerW, sessionsFocused, th),
		m.sessionPanel.View(),
		divider,
		sectionHeader("Agents", innerW, agentsFocused, th),
		m.agentPanel.View(),
	}, "\n")
}

// sectionHeader renders a label followed by a trailing line.
// When focused, the label uses Primary color to indicate the active sub-section.
func sectionHeader(label string, width int, focused bool, th *theme.Theme) string {
	labelColor := th.Palette.Muted
	lineColor := th.Palette.Border
	if focused {
		labelColor = th.Palette.Secondary
	}
	headerStyle := lipgloss.NewStyle().Foreground(labelColor).Bold(true)
	lineStyle := lipgloss.NewStyle().Foreground(lineColor)

	text := headerStyle.Render(" " + label + " ")
	textWidth := lipgloss.Width(text)
	lineWidth := max(width-textWidth, 0)

	return text + lineStyle.Render(strings.Repeat("─", lineWidth))
}

// isCodePanelFocused returns true when any sub-panel of the code column
// (editor, preview, or read-only code viewer) currently has focus.
func (m *AppModel) isCodePanelFocused() bool {
	c := m.focus.Current()
	return c == component.FocusCodeViewer || pane.IsPaneFocus(c)
}

// isEditorFocused returns true when an editor pane has focus (edit mode).
// Returns false when the diff view is active because diff pane IDs share
// the same namespace and would falsely match editor entries.
func (m *AppModel) isEditorFocused() bool {
	if m.diffViewActive {
		return false
	}
	c := m.focus.Current()
	if !pane.IsPaneFocus(c) {
		return false
	}
	pid := pane.PaneIDFromFocus(c)
	_, ok := m.paneEditors[pid]
	return ok
}

// isPreviewFocused returns true when the preview pane has focus.
func (m *AppModel) isPreviewFocused() bool {
	if m.previewPane == 0 {
		return false
	}
	return m.focus.Current() == pane.PaneFocusID(m.previewPane)
}

// focusCodePanel sets focus to the code panel: in edit mode, the focused
// pane's dynamic ID; otherwise the static FocusCodeViewer.
func (m *AppModel) focusCodePanel() {
	if m.viewMode == ViewEdit {
		m.focus.SetFocus(pane.PaneFocusID(m.focusedPane))
		return
	}
	m.focus.SetFocus(component.FocusCodeViewer)
}

// renderCodePanelBordered wraps the code panel content in a border that
// activates when either the editor or preview sub-panel is focused
// (mirrors renderLeftPanelBordered for the session/agent sub-panels).
func (m *AppModel) renderCodePanelBordered(th *theme.Theme) string {
	content := m.overlayChordHint(m.codePanelView(), component.FocusCodeViewer, th)
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.isCodePanelFocused(), w, h, th)
}

// renderGitPanelBordered renders the git explorer panel using the FileTree slot
// dimensions with border highlight based on FocusGitPanel focus state.
func (m *AppModel) renderGitPanelBordered(th *theme.Theme) string {
	content := m.gitPanel.View(m.cursorVisible)
	if m.layout.Mode() == layout.SingleColumn {
		content = m.overlayChordHint(content, component.FocusFileTree, th)
	}
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	focused := m.focus.Current() == component.FocusGitPanel
	return m.renderBordered(content, focused, w, h, th)
}

// renderGitCommitTreeBordered renders the commit tree panel using the CodeViewer
// slot dimensions with border highlight based on FocusCommitTree focus state.
func (m *AppModel) renderGitCommitTreeBordered(th *theme.Theme) string {
	content := m.overlayBlockedChord(m.overlayChordHint(m.commitTree.View(m.cursorVisible), component.FocusCodeViewer, th))
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.focus.Current() == component.FocusCommitTree, w, h, th)
}

// renderPlanViewBordered renders the plan DAG viewer in the right panel slot
// with border highlight based on FocusPlanView focus state.
func (m *AppModel) renderPlanViewBordered(th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.planView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
	content := m.planView.View(m.cursorVisible)
	return m.renderBordered(content, m.focus.Current() == component.FocusPlanView, w, h, th)
}

// renderDiffFileListBordered renders the diff file list sidebar using the
// FileTree slot dimensions with border highlight based on FocusDiffFileList.
func (m *AppModel) renderDiffFileListBordered(th *theme.Theme) string {
	content := m.diffView.FileListView(m.cursorVisible)
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	return m.renderBordered(content, m.focus.Current() == component.FocusDiffFileList, w, h, th)
}

// renderDiffViewBordered renders the diff view using the CodeViewer slot
// dimensions with border highlight based on FocusDiffView focus state.
func (m *AppModel) renderDiffViewBordered(th *theme.Theme) string {
	content := m.overlayBlockedChord(m.overlayChordHint(m.diffView.View(m.cursorVisible), component.FocusCodeViewer, th))
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.isDiffPaneFocused(), w, h, th)
}

// renderMergeDiffFileListBordered renders the merge diff file list sidebar.
func (m *AppModel) renderMergeDiffFileListBordered(th *theme.Theme) string {
	content := m.mergeDiffView.FileListView(m.cursorVisible)
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	return m.renderBordered(content, m.focus.Current() == component.FocusMergeDiffFileList, w, h, th)
}

// renderMergeDiffViewBordered renders the merge diff view.
func (m *AppModel) renderMergeDiffViewBordered(th *theme.Theme) string {
	content := m.overlayBlockedChord(m.overlayChordHint(m.mergeDiffView.View(m.cursorVisible), component.FocusCodeViewer, th))
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.isMergeDiffPaneFocused(), w, h, th)
}

// isMergeDiffPaneFocused reports whether the focused component is a merge diff pane.
func (m *AppModel) isMergeDiffPaneFocused() bool {
	return m.mergeDiffViewActive && m.mergeDiffView != nil && pane.IsPaneFocus(m.focus.Current())
}

// renderConflictFileListBordered renders the conflict file list sidebar.
func (m *AppModel) renderConflictFileListBordered(th *theme.Theme) string {
	content := m.conflictView.FileListView(m.cursorVisible)
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	return m.renderBordered(content, m.focus.Current() == component.FocusConflictFileList, w, h, th)
}

// renderConflictViewBordered renders the conflict resolution content pane.
func (m *AppModel) renderConflictViewBordered(th *theme.Theme) string {
	content := m.overlayBlockedChord(m.overlayChordHint(m.conflictView.View(m.cursorVisible), component.FocusCodeViewer, th))
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.focus.Current() == component.FocusConflictView, w, h, th)
}

// renderBordered wraps content in a panel border. When focused and a gradient
// border renderer is active, uses per-character gradient coloring; otherwise
// falls back to the standard lipgloss per-side border style.
func (m *AppModel) renderBordered(content string, focused bool, w, h int, th *theme.Theme) string {
	innerW := max(w-panelBorderSize, 1)
	innerH := max(h-panelBorderSize, 1)
	if focused && th.ActiveBorderRender != nil {
		return th.ActiveBorderRender(content, innerW, innerH, h)
	}
	border := th.InactiveBorder
	if focused {
		border = th.ActiveBorder
	}
	return border.
		Width(innerW).
		Height(innerH).
		MaxHeight(h).
		Render(content)
}

func (m *AppModel) renderPanel(content string, id component.FocusID, th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(id)
	return m.renderBordered(content, m.focus.IsFocused(id), w, h, th)
}

// ---------------------------------------------------------------------------
// Focus
// ---------------------------------------------------------------------------

func (m *AppModel) syncFocusState() {
	current := m.focus.Current()

	// Track which editor pane has focus. When focus moves to a different
	// editor pane, update focusedPane so all helpers route correctly.
	// Skip when diff view or merge diff view is active — their panes share
	// the same PaneID namespace (starting from 1) and would falsely match
	// editor entries.
	if pane.IsPaneFocus(current) && !m.diffViewActive && !m.mergeDiffViewActive {
		pid := pane.PaneIDFromFocus(current)
		if _, ok := m.paneEditors[pid]; ok {
			m.focusedPane = pid
		}
	}

	m.chat.SetFocused(current == component.FocusChat)
	m.input.SetFocused(current == component.FocusInput)
	m.sessionPanel.SetFocused(current == component.FocusSessionPanel)
	m.agentPanel.SetFocused(current == component.FocusAgentPanel)
	m.codePanel.SetFocused(current == component.FocusCodeViewer && m.viewMode != ViewEdit && !m.hasPreview())
	for id, ps := range m.paneEditors {
		// When diff/merge diff view is active, pane IDs overlap with editor
		// pane IDs (both start from 1). Force all editors unfocused to prevent
		// false matches — focus is handled by the respective view block below.
		focused := current == pane.PaneFocusID(id) && !m.diffViewActive && !m.mergeDiffViewActive
		ps.editor.SetFocused(focused)
		if !focused {
			ps.editor.DismissAllOverlays()
		}
	}
	m.previewPanel.SetFocused(m.isPreviewFocused())
	m.mdPreviewPanel.SetFocused(m.mdPreviewPane != 0 && current == pane.PaneFocusID(m.mdPreviewPane))
	m.fileTree.SetFocused(current == component.FocusFileTree)

	// Git mode panels.
	if m.gitPanel != nil {
		m.gitPanel.SetFocused(current == component.FocusGitPanel)
		m.commitTree.SetFocused(current == component.FocusCommitTree)
	}

	// Diff view panels — mirrors the editor pane loop above: syncFocusState
	// is the single source of truth for diff focus, just as it is for editors.
	if m.diffViewActive && m.diffView != nil {
		if pane.IsPaneFocus(current) {
			m.diffView.SetFocusedPane(pane.PaneIDFromFocus(current))
		}
		m.diffView.SetFocused(pane.IsPaneFocus(current) || current == component.FocusDiffView)
		m.diffView.SetFileListFocused(current == component.FocusDiffFileList)
	}

	// Merge diff view panels — same pattern as diff view above.
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		if pane.IsPaneFocus(current) {
			m.mergeDiffView.SetFocusedPane(pane.PaneIDFromFocus(current))
		}
		m.mergeDiffView.SetFocused(pane.IsPaneFocus(current) || current == component.FocusMergeDiffView)
		m.mergeDiffView.SetFileListFocused(current == component.FocusMergeDiffFileList)
	}

	// Conflict view panels.
	if m.conflictViewActive && m.conflictView != nil {
		m.conflictView.SetFocused(current == component.FocusConflictView)
		m.conflictView.SetFileListFocused(current == component.FocusConflictFileList)
	}

	// Plan viewer panel.
	if m.planView != nil {
		m.planView.SetFocused(current == component.FocusPlanView)
	}

	// Sync warp line display for the newly focused editor.
	m.syncEditorWarpLines()

	// Override the editor status line mode when preview is focused.
	m.syncPreviewModeDisplay()
}

// syncPreviewModeDisplay sets the editor status line to PREVIEW mode when
// the preview panel is focused, and restores the editor's actual mode otherwise.
func (m *AppModel) syncPreviewModeDisplay() {
	browsing := m.hasPreview() && m.focus.Current() == component.FocusFileTree
	if m.isPreviewFocused() || browsing {
		m.focusedEditor().SetStatusMode(mode.ModePreview)
		return
	}
	if m.isEditorFocused() {
		m.focusedEditor().RestoreStatusMode()
	}
}

// spatialFocusTarget resolves an alt+shift+arrow key to the panel it should
// navigate to. Delegates to the generic layout.Navigate algorithm over a
// hierarchical panel grid built from the current layout mode and state.
func (m *AppModel) spatialFocusTarget(key string) (component.FocusID, bool) {
	dir, ok := keyToDirection(key)
	if !ok {
		return 0, false
	}
	grid := m.buildPanelGrid()
	pos, ok := layout.FindInGrid(grid, m.focus.Current())
	if !ok {
		return 0, false
	}
	return layout.Navigate(grid, pos, dir)
}

// keyToDirection maps an alt+shift+arrow key string to a layout.Direction.
func keyToDirection(key string) (layout.Direction, bool) {
	switch key {
	case "alt+shift+right":
		return layout.DirRight, true
	case "alt+shift+left":
		return layout.DirLeft, true
	case "alt+shift+down":
		return layout.DirDown, true
	case "alt+shift+up":
		return layout.DirUp, true
	}
	return 0, false
}

// buildPanelGrid returns the visible panels as a hierarchical grid of
// layout.PanelGroup entries. Only panels actually rendered on screen are
// included. Sub-panels (e.g. Sessions+Agents, Preview+Editor) are encoded
// within their parent PanelGroup's sub-grid.
func (m *AppModel) buildPanelGrid() [][]layout.PanelGroup {
	// gitResolve translates slot-based focus IDs to git-panel IDs when
	// git mode is active, so spatial navigation finds the correct panels.
	gitResolve := func(id component.FocusID) component.FocusID {
		if m.viewMode != ViewGit {
			return id
		}
		switch id {
		case component.FocusFileTree:
			if m.mergeDiffViewActive {
				return component.FocusMergeDiffFileList
			}
			if m.diffViewActive {
				return component.FocusDiffFileList
			}
			return component.FocusGitPanel
		case component.FocusCodeViewer:
			return component.FocusCommitTree
		}
		return id
	}

	pg := func(id component.FocusID) layout.PanelGroup {
		return layout.PanelGroup{SubPanels: [][]component.FocusID{{gitResolve(id)}}}
	}

	leftSubs := func() layout.PanelGroup {
		return layout.PanelGroup{SubPanels: [][]component.FocusID{
			{component.FocusSessionPanel},
			{component.FocusAgentPanel},
		}}
	}

	var top []layout.PanelGroup

	switch m.layout.Mode() {
	case layout.FourColumn:
		top = []layout.PanelGroup{
			leftSubs(),
			pg(component.FocusFileTree),
			pg(component.FocusChat),
			m.codePanelGroup(),
		}
	case layout.ThreeColumn:
		left := m.leftRing.current()
		if left == component.FocusSessionPanel {
			top = append(top, leftSubs())
		} else {
			top = append(top, pg(left))
		}
		top = append(top, pg(component.FocusChat), m.codePanelGroup())
	case layout.TwoColumn:
		left := m.leftRing.current()
		right := m.rightRing.current()
		if left == component.FocusSessionPanel {
			top = append(top, leftSubs())
		} else {
			top = append(top, pg(left))
		}
		if right == component.FocusCodeViewer || right == component.FocusDiffView || right == component.FocusMergeDiffView {
			top = append(top, m.codePanelGroup())
		} else {
			top = append(top, pg(right))
		}
	default: // SingleColumn
		active := m.leftRing.current()
		switch {
		case active == component.FocusSessionPanel:
			top = append(top, leftSubs())
		case active == component.FocusCodeViewer || active == component.FocusDiffView || active == component.FocusMergeDiffView:
			top = append(top, m.codePanelGroup())
		default:
			top = append(top, pg(active))
		}
	}

	return [][]layout.PanelGroup{top, {pg(component.FocusInput)}}
}

// codePanelGroup returns the PanelGroup for the code column, with sub-panels
// derived from the pane tree for spatial navigation.
func (m *AppModel) codePanelGroup() layout.PanelGroup {
	if m.viewMode == ViewEdit && m.paneTree != nil {
		return layout.PanelGroup{SubPanels: m.paneTree.ToSubGrid()}
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		if dt := m.mergeDiffView.PaneTree(); dt != nil {
			return layout.PanelGroup{SubPanels: dt.ToSubGrid()}
		}
	}
	if m.diffViewActive && m.diffView != nil {
		if dt := m.diffView.PaneTree(); dt != nil {
			return layout.PanelGroup{SubPanels: dt.ToSubGrid()}
		}
	}
	id := component.FocusCodeViewer
	if m.viewMode == ViewGit {
		id = component.FocusCommitTree
	}
	return layout.PanelGroup{SubPanels: [][]component.FocusID{
		{id},
	}}
}

// ---------------------------------------------------------------------------
// Bridges
// ---------------------------------------------------------------------------

func (m *AppModel) startBridges() tea.Cmd {
	return func() tea.Msg {
		return bridgeReadyMsg{}
	}
}

// bridgeReadyMsg is an internal message signaling that bridges should be started.
type bridgeReadyMsg struct{}

// StartBridges connects all event bridges to the running tea.Program.
// This must be called after tea.NewProgram is created.
func (m *AppModel) StartBridges(program bridge.TeaProgram) error {
	bridges := []bridge.Bridge{
		m.activityBridge,
		m.sessionBridge,
		m.streamBridge,
		m.guideBridge,
		m.lspBridge,
	}
	for _, b := range bridges {
		if err := b.Start(program); err != nil {
			return err
		}
	}
	if m.gitBridge != nil {
		if err := m.gitBridge.Start(program); err != nil {
			return err
		}
	}
	if m.pipelineBridge != nil {
		if err := m.pipelineBridge.Start(program); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Guide integration
// ---------------------------------------------------------------------------

func (m *AppModel) publishRouteRequest(submit msg.SubmitPromptMsg) tea.Cmd {
	sessionID := m.resolveRouteSessionID(submit.SessionID)
	submit.SessionID = sessionID
	targetAgent := normalizeExplicitTargetAgent(submit.TargetAgent)
	promptEstimate := estimateGuideTokens(submit.Text) + guideRouteOverheadTokens
	contextUsage := m.bumpAgentContextUsage(guideAgentID, promptEstimate)
	m.statusBar.SetTokenPhase(status.PhaseInput)
	m.publishGuideActivity(
		events.EventTypeLLMRequest,
		events.OutcomePending,
		"Classifying and routing request",
		contextUsage,
	)

	req := &guide.RouteRequest{
		CorrelationID:  uuid.New().String(),
		Input:          submit.Text,
		SourceAgentID:  sourceAgentTUI,
		TargetAgentID:  targetAgent,
		ExplicitTarget: targetAgent != "",
		SessionID:      submit.SessionID,
		Timestamp:      time.Now(),
	}
	m.beginActiveRoute(req.CorrelationID, thinkingAgentType(targetAgent))

	if !m.guideRequestAvailable() {
		return func() tea.Msg {
			return msg.StreamErrorMsg{
				SessionID:     submit.SessionID,
				CorrelationID: req.CorrelationID,
				Err:           errors.New("guide is not running; start with --mock or connect a guide backend"),
			}
		}
	}

	busMsg := guide.NewRequestMessage("", req)

	return func() tea.Msg {
		err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, busMsg)
		if err != nil {
			return msg.StreamErrorMsg{
				SessionID:     submit.SessionID,
				CorrelationID: req.CorrelationID,
				Err:           err,
			}
		}
		return nil
	}
}

func (m *AppModel) bumpGuideContextUsage(addedTokens int) float64 {
	retained := int(float64(m.guideContextTokens) * guideContextRetention)
	tokens := retained + max(addedTokens, 0)
	tokens = min(tokens, guideMaxContextTokens)
	m.guideContextTokens = tokens
	m.guideContextUsage = float64(tokens) / float64(guideMaxContextTokens)
	m.agentContextTokens[guideAgentID] = tokens
	return m.guideContextUsage
}

// setAgentContextUsage directly sets the context usage from real input tokens.
// Input tokens represent the full conversation context sent to the agent on each
// call, so they directly measure context window occupancy — no decay needed.
func (m *AppModel) setAgentContextUsage(agentID string, inputTokens int) float64 {
	normalized := strings.ToLower(strings.TrimSpace(agentID))
	if normalized == "" {
		return 0
	}
	limit := agentContextTokenLimit(normalized)
	tokens := min(max(inputTokens, 0), limit)
	m.agentContextTokens[normalized] = tokens
	ratio := float64(tokens) / float64(limit)
	if normalized == guideAgentID {
		m.guideContextTokens = tokens
		m.guideContextUsage = ratio
	}
	return ratio
}

func (m *AppModel) bumpAgentContextUsage(agentID string, addedTokens int) float64 {
	normalized := strings.ToLower(strings.TrimSpace(agentID))
	if normalized == "" {
		return 0
	}
	if normalized == guideAgentID {
		return m.bumpGuideContextUsage(addedTokens)
	}
	retained := int(float64(m.agentContextTokens[normalized]) * guideContextRetention)
	tokens := retained + max(addedTokens, 0)
	limit := agentContextTokenLimit(normalized)
	tokens = min(tokens, limit)
	m.agentContextTokens[normalized] = tokens
	return float64(tokens) / float64(limit)
}

func agentContextTokenLimit(agentID string) int {
	if strings.EqualFold(agentID, "architect") {
		return defaultAgentMaxContextTokens
	}
	return defaultAgentMaxContextTokens
}

func estimateGuideTokens(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	chars := len([]rune(trimmed))
	return max((chars+3)/4, 1)
}

func (m *AppModel) publishGuideActivity(
	eventType events.EventType,
	outcome events.EventOutcome,
	content string,
	contextUsage float64,
) {
	if m.deps.ActivityBus == nil {
		return
	}
	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		Timestamp: time.Now(),
		AgentID:   guideAgentID,
		Content:   content,
		Outcome:   outcome,
		Data: map[string]any{
			"agent_type":    guideAgentType,
			"agent_name":    guideAgentName,
			"context_usage": contextUsage,
		},
	}
	m.deps.ActivityBus.Publish(event)
}

func (m *AppModel) guideRequestAvailable() bool {
	if m.deps.GuideBus == nil {
		return false
	}
	if m.deps.Guide != nil {
		return true
	}
	if channelBus, ok := m.deps.GuideBus.(*guide.ChannelBus); ok {
		return channelBus.TopicSubscriberCount(guide.TopicGuideRequests) > 0
	}
	return true
}

// ---------------------------------------------------------------------------
// Tick — demand-driven tick chain
// ---------------------------------------------------------------------------

// needsFastTick reports whether any 60fps animation is active.
func (m *AppModel) needsFastTick() bool {
	return !m.scroll.settled() ||
		!m.bounceSettled()
}

// needsDecorTick reports whether low-frequency UI effects are active.
func (m *AppModel) needsDecorTick() bool {
	now := time.Now()
	return m.chat.HasActiveAnimation() ||
		m.statusBar.IsAnimating() ||
		now.Before(m.tabArrowFlashLeftUntil) ||
		now.Before(m.tabArrowFlashRightUntil) ||
		(m.commitTree != nil && m.commitTree.NeedsDecorTick()) ||
		(m.viewMode == ViewGit && m.gitPanel != nil && m.gitPanel.NeedsDecorTick()) ||
		(m.diffViewActive && m.diffView != nil && m.diffView.NeedsDecorTick()) ||
		(m.mergeDiffViewActive && m.mergeDiffView != nil && m.mergeDiffView.NeedsDecorTick()) ||
		(m.conflictViewActive && m.conflictView != nil && m.conflictView.NeedsDecorTick()) ||
		(m.planView != nil && m.planView.NeedsDecorTick()) ||
		m.agentPanel.NeedsDecorTick() ||
		m.focusGradient != nil
}

// needsSlowTick reports whether any non-blink, non-LSP debounce needs
// slow-rate ticking. Cursor blink is handled by BlinkMsg; LSP flush by
// LSPFlushMsg.
func (m *AppModel) needsSlowTick() bool {
	return m.swipe.accum != 0 // Swipe decay pending.
}

// ensureTick starts or upgrades the tick chain. Returns a tea.Cmd only when
// a new chain must be scheduled; nil if the current chain already covers it.
func (m *AppModel) ensureTick(fast bool) tea.Cmd {
	if fast && m.tickRate != tickFast {
		m.tickGen++
		m.tickRate = tickFast
		return m.tickCmdWith(tickFastInterval)
	}
	if m.tickRate == tickIdle {
		m.tickGen++
		if fast {
			m.tickRate = tickFast
			return m.tickCmdWith(tickFastInterval)
		}
		m.tickRate = tickSlow
		return m.tickCmdWith(tickSlowInterval)
	}
	return nil
}

// tickCmdWith schedules a one-shot tick at the given interval, tagged with
// the current generation to detect stale chains.
func (m *AppModel) tickCmdWith(d time.Duration) tea.Cmd {
	gen := m.tickGen
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return msg.TickMsg{Time: t, Gen: gen}
	})
}

// continueTickChain returns the next tick command at the appropriate
// interval, or nil to let the chain stop when nothing needs ticking.
func (m *AppModel) continueTickChain() tea.Cmd {
	if m.needsFastTick() {
		m.tickRate = tickFast
		return m.tickCmdWith(tickFastInterval)
	}
	if m.needsSlowTick() {
		m.tickRate = tickSlow
		return m.tickCmdWith(tickSlowInterval)
	}
	m.tickRate = tickIdle
	return nil
}

// ensureTickAfterDispatch starts or upgrades the tick chain if the
// dispatch changed state that requires ticking.
func (m *AppModel) ensureTickAfterDispatch() tea.Cmd {
	if m.needsFastTick() {
		return m.ensureTick(true)
	}
	if m.needsSlowTick() {
		return m.ensureTick(false)
	}
	return nil
}

// ensureDecorTick starts the decor tick chain if needed.
func (m *AppModel) ensureDecorTick() tea.Cmd {
	if m.decorOn {
		return nil
	}
	m.decorOn = true
	m.decorGen++
	gen := m.decorGen
	return tea.Tick(decorTickInterval, func(t time.Time) tea.Msg {
		return msg.DecorTickMsg{Time: t, Gen: gen}
	})
}

// continueDecorTickChain schedules the next decor tick if effects remain.
func (m *AppModel) continueDecorTickChain() tea.Cmd {
	if !m.needsDecorTick() {
		m.decorOn = false
		return nil
	}
	gen := m.decorGen
	return tea.Tick(decorTickInterval, func(t time.Time) tea.Msg {
		return msg.DecorTickMsg{Time: t, Gen: gen}
	})
}

// ensureDecorTickAfterDispatch starts decor ticking when needed.
func (m *AppModel) ensureDecorTickAfterDispatch() tea.Cmd {
	if m.needsDecorTick() {
		return m.ensureDecorTick()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Blink — one-shot cursor blink timer
// ---------------------------------------------------------------------------

// needsBlink reports whether any component has a cursor that needs blinking.
func (m *AppModel) needsBlink() bool {
	if m.viewMode == ViewEdit {
		return true
	}
	if m.focus.Current() == component.FocusInput {
		return true
	}
	if m.hasPreview() && m.isPreviewFocused() {
		return true
	}
	if m.viewMode == ViewGit && m.gitPanel.NeedsBlink() {
		return true
	}
	if m.viewMode == ViewGit && m.commitTree != nil && m.commitTree.NeedsBlink() {
		return true
	}
	return m.fileTree.NeedsBlink()
}

// blinkPhase computes whether the cursor should be visible based on
// the wall clock. Phase 0 (visible) starts at blinkEpoch; each
// blinkHalfPeriod the phase alternates. Using the clock instead of
// toggle state prevents phase inversion from delayed messages.
func (m *AppModel) blinkPhase() bool {
	elapsed := time.Since(m.blinkEpoch)
	phase := int(elapsed/blinkHalfPeriod) % 2
	return phase == 0 // 0 = visible, 1 = invisible
}

// nextBlinkDeadline returns the absolute time of the next phase boundary.
func (m *AppModel) nextBlinkDeadline() time.Time {
	elapsed := time.Since(m.blinkEpoch)
	periods := elapsed/blinkHalfPeriod + 1
	return m.blinkEpoch.Add(periods * blinkHalfPeriod)
}

// blinkCmd schedules a timer targeting the next phase boundary.
// The goroutine sleeps until the absolute deadline, compensating for
// View() latency and message queue delay.
func (m *AppModel) blinkCmd() tea.Cmd {
	gen := m.blinkGen
	deadline := m.nextBlinkDeadline()
	return func() tea.Msg {
		if d := time.Until(deadline); d > 0 {
			time.Sleep(d)
		}
		return msg.BlinkMsg{Gen: gen, Deadline: deadline}
	}
}

// handleBlink schedules the next blink timer. Phase sync happens in
// View() via centralized blink logic, which sets viewDirty only when
// the phase actually changed — avoiding wasted renders on early/jittered timers.
func (m *AppModel) handleBlink(blink msg.BlinkMsg) tea.Cmd {
	if blink.Gen != m.blinkGen {
		return nil
	}
	if !m.needsBlink() {
		return nil
	}
	if m.commitTree != nil {
		m.commitTree.AdvanceSpinner()
	}
	return m.blinkCmd()
}

// ensureBlinkAfterDispatch starts a blink chain if any component needs
// cursor blinking. The generation counter ensures at most one chain runs.
func (m *AppModel) ensureBlinkAfterDispatch() tea.Cmd {
	if m.needsBlink() {
		return m.blinkCmd()
	}
	return nil
}

// ---------------------------------------------------------------------------
// LSP flush — one-shot debounced didChange
// ---------------------------------------------------------------------------

// ensureLSPFlush schedules a one-shot LSP flush timer if the editor has
// pending changes that haven't been scheduled yet (by editGeneration).
func (m *AppModel) ensureLSPFlush() tea.Cmd {
	if m.viewMode != ViewEdit || !m.focusedEditor().LSPDirty() {
		return nil
	}
	gen := m.focusedEditor().EditGeneration()
	if gen == m.lspFlushGen {
		return nil // Already scheduled for this generation.
	}
	m.lspFlushGen = gen
	return tea.Tick(lspDebounceInterval, func(_ time.Time) tea.Msg {
		return msg.LSPFlushMsg{Gen: gen}
	})
}

// handleLSPFlush fires the debounced LSP didChange notification if the
// editor is still dirty at the same generation as when the timer was scheduled.
func (m *AppModel) handleLSPFlush(flush msg.LSPFlushMsg) tea.Cmd {
	if m.viewMode != ViewEdit || !m.focusedEditor().LSPDirty() {
		return nil
	}
	if m.focusedEditor().EditGeneration() != flush.Gen {
		return nil // Stale — more edits happened; a newer flush is pending.
	}
	m.focusedEditor().ClearLSPDirty()
	return m.lspDidChangeCmd(m.focusedEditor().FilePath(), m.focusedEditor().Content())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// appendCmd appends a non-nil command to the slice.
// clampInt constrains v to [lo, hi].
func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

func appendCmd(cmds []tea.Cmd, cmd tea.Cmd) []tea.Cmd {
	if cmd != nil {
		return append(cmds, cmd)
	}
	return cmds
}

// programAdapter wraps a *tea.Program to satisfy bridge.TeaProgram.
// This adapter exists because bridge.TeaProgram.Send uses `any` to avoid
// importing bubbletea in the bridge package, while tea.Program.Send
// uses the named type tea.Msg.
type programAdapter struct {
	program *tea.Program
}

func (a *programAdapter) Send(m any) {
	a.program.Send(m)
}

// Run creates and runs the Bubble Tea program. This is the main entry point.
func Run(ctx context.Context, cfg Config, deps Deps) error {
	// Resolve project root: explicit → git root → CWD.
	root := cfg.ProjectRoot
	if root == "" {
		cwd, _ := os.Getwd()
		if gitRoot, err := boot.FindGitRoot(cwd); err == nil {
			root = gitRoot
		} else {
			root = cwd
		}
		cfg.ProjectRoot = root
	}

	app := New(ctx, cfg, deps)
	app.fileTree.SetRoot(root)

	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
		tea.WithContext(ctx),
	)

	adapter := &programAdapter{program: p}

	// Start bridges with the program reference via adapter.
	if err := app.StartBridges(adapter); err != nil {
		return err
	}

	// Register live agents so the agent panel displays them. Must run after
	// StartBridges so the activity bridge is subscribed to the bus.
	seedLiveAgents(deps)

	// In mock mode, seed additional demo data. Requests still route through
	// real Guide/Architect agents via the event bus.
	if cfg.MockMode {
		seedMockData(deps)
	}

	_, err := p.Run()

	// Restore default signal handling so a second Ctrl+C during shutdown
	// immediately terminates the process instead of being silently consumed.
	if deps.SignalStop != nil {
		deps.SignalStop()
	}

	if err != nil {
		return err
	}

	return app.Shutdown()
}
