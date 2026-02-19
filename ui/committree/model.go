package committree

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// commitFlashDuration is how long the success message stays visible.
const commitFlashDuration = 3 * time.Second

// minLoadingBarDuration is the minimum time the bottom loading bar stays
// visible. Prevents flicker when pages load faster than a few spinner frames.
// Derived from: 5 spinner frames × 80ms = 400ms ≈ half a braille cycle.
const minLoadingBarDuration = 400 * time.Millisecond

// toolbarHeight is the number of terminal rows reserved for the bottom toolbar.
// Derived from: top border(1) + content(1) = 2.
// The panel border provides the right and bottom edges.
const toolbarHeight = 2

// Compile-time interface assertions.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// viewMode tracks which sub-view is active in the commit tree panel.
type viewMode int

const (
	viewBranches   viewMode = iota // Branch tree (default).
	viewLoading                    // Spinner while loading commits+stats.
	viewCommits                    // Commit cards for a single branch.
	viewRebasePlan                 // Interactive rebase plan editor.
	viewBranchPick                 // Full-window branch picker for cherry-pick/rebase target.
)

// rebasePlanEntry describes one entry in the interactive rebase plan editor.
type rebasePlanEntry struct {
	action  int    // Maps to git.RebaseAction constants (0=pick..5=drop).
	hash    string // Short commit hash.
	subject string // First line of commit message.
	author  string // Author name.
}

// dagSection groups one trunk commit with its offshoot rows above it.
// The trunk commit is on the first-parent chain; offshoots are commits
// reachable from merge second-parents that are not on the trunk.
type dagSection struct {
	trunkIdx int     // index into m.nodes
	offIdxs  []int   // offshoot node indices (topo order, newest first)
	offRows  [][]int // offIdxs grouped into grid rows
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// SelectionMsg is emitted when the selected commit changes.
type SelectionMsg struct {
	Hash string
}

// BranchSelectedMsg is emitted when the user presses Enter on a branch,
// signaling that commits should be loaded for the named branch.
type BranchSelectedMsg struct {
	Name string
}

// BranchSwitchMsg is emitted when the user selects Switch in an expanded card.
type BranchSwitchMsg struct {
	Name string
}

// BranchDeleteMsg is emitted when the user selects Delete in an expanded card.
type BranchDeleteMsg struct {
	Name string
}

// LoadMoreMsg is emitted when the user scrolls near the bottom of the commit
// list and more pages are available, signaling the app to fetch the next page.
type LoadMoreMsg struct{}

// ---------------------------------------------------------------------------
// Branch expansion
// ---------------------------------------------------------------------------

// branchAction identifies an action button in an expanded card.
const (
	branchActionCommit = iota // HEAD only: commit staged changes
	branchActionSwitch
	branchActionDelete
)

// commitPhase tracks the inline commit lifecycle.
type commitPhase int

const (
	commitIdle       commitPhase = iota // text input or no input
	commitInProgress                    // async commit running
	commitSucceeded                     // brief success flash
	commitFailed                        // brief error flash
)

// CommitRequestMsg is emitted when the user confirms a commit in the expanded
// card's inline input.
type CommitRequestMsg struct {
	Message string
}

// CommitDoneMsg is sent by the app after a commit succeeds or fails, so the
// commit tree can update its inline display.
type CommitDoneMsg struct {
	OK      bool
	Message string // success: commit subject; failure: error reason
}

// commitDismissMsg is an internal timer message to clear the success flash.
type commitDismissMsg struct{}

// ---------------------------------------------------------------------------
// Toolbar buttons
// ---------------------------------------------------------------------------

// toolbarButton identifies a toolbar button in the bottom bar.
const (
	toolbarCreate     = iota // Branch view: create a new branch.
	toolbarMerge             // Branch view: merge selected branch.
	toolbarBack              // Commit view: return to branch tree.
	toolbarDiff              // Commit view: show diff for selected commit.
	toolbarDiffOk            // Diff mode: confirm diff.
	toolbarDiffCancel        // Diff mode: cancel diff.
	toolbarMergeOk           // Merge mode: confirm merge.
	toolbarMergeAbort        // Merge mode: abort merge.
	toolbarPull              // Branch view: pull highlighted branch from remote.
	toolbarPush              // Branch view: push highlighted branch to remote.
	toolbarReset             // Commit view: enter reset mode.
	toolbarResetHard         // Reset mode: hard reset.
	toolbarResetMixed        // Reset mode: mixed reset.
	toolbarResetSoft         // Reset mode: soft reset.
	toolbarResetAbort        // Reset mode: cancel.
	toolbarRevert            // Commit view: revert selected commit.
	toolbarRevertOk          // Revert mode: confirm revert.
	toolbarRevertAbort       // Revert mode: cancel revert.
	toolbarCherryPick        // Commit view: enter cherry-pick mode.
	toolbarCherryPickOk      // Cherry-pick mode: confirm.
	toolbarCherryPickAbort   // Cherry-pick mode: cancel.
	toolbarRebase            // Commit view: enter interactive rebase.
	toolbarConflictContinue  // Conflict mode: continue after resolution.
	toolbarConflictBypass    // Conflict mode: bypass (commit as-is).
	toolbarConflictAbort     // Conflict mode: abort operation.
	toolbarRebasePlanOk      // Rebase plan: confirm plan.
	toolbarRebasePlanAbort   // Rebase plan: cancel.
)

// CreateBranchRequestMsg is emitted when the user confirms a branch name
// in the create input. ParentBranch is the currently selected branch whose
// tip commit the new branch points at.
type CreateBranchRequestMsg struct {
	Name         string
	ParentBranch string
	AtHash       string // Non-empty when creating from detached HEAD.
}

// MergeBranchMsg is emitted when the user activates the Merge toolbar button.
type MergeBranchMsg struct {
	Name string
}

// PullBranchMsg is emitted when the user activates the Pull toolbar button.
type PullBranchMsg struct {
	Name string
}

// PushBranchMsg is emitted when the user activates the Push toolbar button.
type PushBranchMsg struct {
	Name string
}

// DiffRequestMsg is emitted when the user activates the Diff toolbar button.
type DiffRequestMsg struct {
	Hash string
}

// DiffCompareMsg is emitted when the user confirms a multi-commit diff via Ok.
type DiffCompareMsg struct {
	Hashes []string // ordered by selection
}

// BranchDiffCompareMsg is emitted when the user confirms a multi-branch diff
// via Ok. Hashes are the tip commit hashes; Names are the branch names used
// as pane title labels instead of short hashes.
type BranchDiffCompareMsg struct {
	Hashes []string // Tip commit hashes (for diff computation).
	Names  []string // Branch names (for pane titles).
}

// MergeDiffCompareMsg is emitted when the user confirms a merge selection via
// Ok. Hashes are the tip commit hashes; Names are the branch names used as
// pane title labels.
type MergeDiffCompareMsg struct {
	Hashes []string // Tip commit hashes (for diff computation).
	Names  []string // Branch names (for pane titles).
}

// ResetRequestMsg is emitted when the user selects a reset mode (hard/mixed/soft)
// on a target commit.
type ResetRequestMsg struct {
	Hash string
	Mode string // "hard", "mixed", "soft"
}

// CommitCheckoutRequestMsg is emitted when the user presses Enter on a commit.
type CommitCheckoutRequestMsg struct {
	Hash string
}

// RevertRequestMsg is emitted when the user activates the Revert toolbar button.
type RevertRequestMsg struct {
	Hash string
}

// CherryPickRequestMsg is emitted when the user confirms cherry-pick.
type CherryPickRequestMsg struct {
	Hashes       []string
	TargetBranch string
}

// RebaseStartMsg is emitted when the user confirms an interactive rebase plan.
type RebaseStartMsg struct {
	OntoBranch string
	Plan       []RebasePlanItem
}

// RebasePlanItem describes one step in the interactive rebase plan.
type RebasePlanItem struct {
	Action  int    // Maps to git.RebaseAction
	Hash    string
	Subject string
}

// SequencerContinueMsg requests the app continue the sequencer.
type SequencerContinueMsg struct{}

// SequencerBypassMsg requests the app bypass the current conflict.
type SequencerBypassMsg struct{}

// SequencerAbortMsg requests the app abort the sequencer.
type SequencerAbortMsg struct{}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// Model is the core state for the commit tree visualization panel.
type Model struct {
	focused   bool
	width     int
	height    int
	viewDirty bool
	theme     *theme.Theme

	mode               viewMode
	branchPickPrevMode viewMode // Saved mode to restore after branch pick.

	// Branch view state.
	branches        []BranchNode
	branchIdx       int
	branchScrollOff int
	activeBranch    string            // Branch being viewed in commit mode.
	defaultBranch   string            // Repository default branch name.
	branchParents   map[string]string // child → parent branch name.

	// Depth-aware offshoot layout (recomputed on data/resize change).
	offRows      [][]int // offRows[rowIdx] = flat offshoot indices in that row
	offRowOf     []int   // offRowOf[flatIdx] = row index for offshoot
	offColOf     []int   // offColOf[flatIdx] = sequential position within row (navigation)
	offGridCol   []int   // offGridCol[flatIdx] = grid column for rendering alignment
	offDepth     []int   // offDepth[flatIdx] = depth tier (0 = child of default)
	offExtraConn []bool  // offExtraConn[rowIdx] = row has extra off-center connector line

	// Expansion state.
	expandedIdx      int  // Flat branch index of expanded card, -1 = none.
	expandedAction   int  // Selected action within visible actions list.
	workingDirty     bool // Working tree has uncommitted changes.
	workingConflicts bool // Working tree has merge conflicts.
	hasStagedFiles   bool // Uncommitted tab has files marked StagingStaged.
	hasIndexStaged   bool // Git index has staged changes (external git add).

	// Loading / pagination state.
	loadingEpoch    time.Time
	loadingBarUntil time.Time // minimum display deadline for loading bar
	hasMore         bool      // more commit pages available beyond loaded nodes
	loadingMore     bool      // true while a "load more" page is in flight
	lastHash        string    // hash of the last loaded commit (cursor for next page)
	pendingCmd      tea.Cmd   // scroll-triggered pagination cmd for parent to drain
	loadingMessage  string    // custom loading message (empty = default)
	prevMode        viewMode  // mode before SetLoadingMessage, for restore

	// Commit input state (HEAD branch expanded card).
	commitInputActive bool
	commitPhase       commitPhase
	commitMsg         string
	commitCursor      int
	commitSpinner     int // spinner frame index

	// Delete confirmation state.
	deleteConfirmActive bool
	deleteConfirmYes   bool // true = (y)es highlighted, false = (n)o highlighted

	// Commit view state.
	nodes       []TreeNode
	selectedIdx int
	scrollOff   int

	// DAG visualization state.
	graphRows    []GraphRow // parallel to m.nodes, one per commit
	maxGraphLane int        // widest lane index (for gutter width)
	dagMode      bool       // true when viewing full DAG (vs flat first-parent)

	// Trunk-and-offshoots DAG layout (recomputed on data/resize change).
	dagSections    []dagSection // one per trunk commit, newest-first
	dagSelectedRow int          // selected visual row in DAG view
	dagSelectedCol int          // column within offshoot row (-1 = trunk)

	// Visual bounce offset from overscroll physics.
	bounceOffset int

	// Per-card rendering cache indexed by flat branch index.
	cardCache []cardCacheEntry

	// Per-node rendering cache indexed by commit node index.
	nodeCache []nodeCacheEntry

	// Per-commit-card rendering cache for DAG trunk/offshoot cards.
	commitCardCache []commitCardCacheEntry

	// Diff selection mode state.
	diffMode       bool
	diffSelections []diffSelection

	// Merge selection mode state.
	mergeMode       bool
	mergeSelections []diffSelection

	// Reset mode state.
	resetMode bool

	// Revert mode state.
	revertMode bool

	// Cherry-pick mode state.
	cherryPickMode       bool
	cherryPickSelections []diffSelection
	cherryPickTarget     string // Selected target branch name.
	branchPickerActive       bool     // Target branch dropdown open.
	branchPickerCursor       int      // Highlighted item index.
	branchPickerScroll       int      // Scroll offset for the picker list.
	branchPickerItems        []string // Available branch names.
	branchPickerFilter       string   // Typing filter for branch picker.
	branchPickerFilterActive bool     // Filter input is active (/ or alt+f).

	// Conflict resolution mode state.
	conflictMode    bool
	conflictOp      int // SequencerOp that caused the conflict.
	conflictTotal   int
	conflictCurrent int
	conflictSubject string   // Subject of the conflicting commit.
	conflictFiles   []string // Paths with conflicts.
	conflictResolved map[string]bool // path → resolved.

	// Interactive rebase plan state.
	rebasePlan       []rebasePlanEntry
	rebaseCursor     int
	rebaseSelAnchor  int    // Multi-select anchor; -1 = no selection.
	rebaseOnto       string // Target branch name for rebase.

	// Toolbar state.
	toolbarFocused bool // True when tab-focus is on the toolbar, not card actions.
	toolbarAction  int  // Selected toolbar button index.
	hoverButtonIdx int  // Toolbar button under mouse, -1 = none.

	// Create branch input state (toolbar Create action).
	createInputActive bool
	createBranchName  string
	createCursor      int
}

// New creates a Model with the given theme.
func New(th *theme.Theme) *Model {
	return &Model{
		theme:          th,
		viewDirty:      true,
		expandedIdx:    -1,
		hoverButtonIdx: -1,
		branchParents:  make(map[string]string),
	}
}

// RecordBranchParent stores a parent relationship so the branch tree
// can display child branches beneath their parent after reload.
func (m *Model) RecordBranchParent(child, parent string) {
	m.branchParents[child] = parent
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (m *Model) ID() component.FocusID { return component.FocusCommitTree }
func (m *Model) Focused() bool         { return m.focused }

func (m *Model) SetFocused(focused bool) {
	m.focused = focused
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

func (m *Model) SetSize(width, height int) {
	if width != m.width {
		m.invalidateCardCache()
		m.invalidateNodeCache()
		m.invalidateCommitCardCache()
		defer m.recomputeOffshootLayout()
		defer m.recomputeCommitLayout()
	}
	m.width = max(width, 0)
	m.height = max(height, 0)
	m.clampScroll()
	m.clampBranchScroll()
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (component.Component, tea.Cmd) {
	switch typed := msg.(type) {
	case CommitDoneMsg:
		return m, m.handleCommitDone(typed)
	case commitDismissMsg:
		m.handleCommitDismiss()
		return m, nil
	case tea.KeyMsg:
		if m.focused {
			return m, m.handleKey(typed)
		}
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// SetBranches populates the branch tree view and resets to branch mode.
// The default branch (detected from the repository) is placed last (bottom
// of the tree) and other branches are ordered newest-first above it.
func (m *Model) SetBranches(branches []BranchNode, defaultBranch string) {
	// Remember the currently selected and expanded branch names so we can
	// restore them after the data refresh (reloads happen on every git
	// status change).
	var prevName, expandedName string
	if m.branchIdx >= 0 && m.branchIdx < len(m.branches) {
		prevName = m.branches[m.branchIdx].Name
	}
	if m.expandedIdx >= 0 && m.expandedIdx < len(m.branches) {
		expandedName = m.branches[m.expandedIdx].Name
	}
	prevAction := m.expandedAction
	prevScroll := m.branchScrollOff

	m.defaultBranch = defaultBranch

	// Apply known parent relationships from UI-initiated branch creation.
	// Only fill in when inference left the parent empty; inferred parents
	// are derived from the current topology and take precedence.
	for i := range branches {
		if branches[i].Parent != "" {
			continue
		}
		if p, ok := m.branchParents[branches[i].Name]; ok && p != branches[i].Name {
			branches[i].Parent = p
		}
	}

	// Sort: default branch last, newest-first by AuthorTime. Sibling
	// ordering within depth tiers is handled by recomputeOffshootLayout
	// (which uses CreatedTime for stable ordering when reflogs exist).
	slices.SortStableFunc(branches, func(a, b BranchNode) int {
		// Detached HEAD always sorts first.
		if a.IsDetached != b.IsDetached {
			if a.IsDetached {
				return -1
			}
			return 1
		}
		ap := a.Name == defaultBranch
		bp := b.Name == defaultBranch
		if ap != bp {
			if ap {
				return 1
			}
			return -1
		}
		return b.AuthorTime.Compare(a.AuthorTime) // newest first
	})
	branches = groupChildrenAfterParent(branches)

	m.branches = branches
	m.recomputeOffshootLayout()

	// Restore selection to the previously selected branch if still present.
	// On first load (no prevName), default to the HEAD branch.
	m.branchIdx = 0
	if prevName != "" {
		for i, b := range m.branches {
			if b.Name == prevName {
				m.branchIdx = i
				break
			}
		}
	} else {
		for i, b := range m.branches {
			if b.IsHead {
				m.branchIdx = i
				break
			}
		}
	}

	// Restore expanded card if the branch still exists.
	m.expandedIdx = -1
	m.expandedAction = 0
	if expandedName != "" {
		for i, b := range m.branches {
			if b.Name == expandedName {
				m.expandedIdx = i
				m.expandedAction = prevAction
				break
			}
		}
	}

	m.branchScrollOff = prevScroll
	m.invalidateCardCache()
	// Preserve active views; only reset to branches when no drill-down,
	// picker, or plan is active.
	switch m.mode {
	case viewCommits, viewBranchPick, viewRebasePlan:
		// Keep the current view.
	default:
		m.mode = viewBranches
	}
	m.viewDirty = true
	m.ensureBranchVisible()
}

// SetWorkingTreeStatus updates the working tree dirty/conflicts flags used
// to determine whether branch actions (switch, delete) are enabled.
func (m *Model) SetWorkingTreeStatus(dirty, conflicts bool) {
	if m.workingDirty == dirty && m.workingConflicts == conflicts {
		return
	}
	m.workingDirty = dirty
	m.workingConflicts = conflicts
	m.viewDirty = true
}

// SetNodes replaces the commit data for the current branch drill-down.
func (m *Model) SetNodes(nodes []TreeNode) {
	m.nodes = nodes
	m.selectedIdx = 0
	m.scrollOff = 0
	m.mode = viewCommits
	m.invalidateNodeCache()
	m.viewDirty = true
}

// SetNodesWithStats atomically replaces commit data and applies diff stats,
// transitioning from the loading spinner to the commit view in one frame.
// Clears any DAG state — this is the flat first-parent mode path.
// Preserves the selected commit across reloads when the hash still exists.
func (m *Model) SetNodesWithStats(nodes []TreeNode, stats map[string][2]int, hasMore bool) {
	prevHash := m.SelectedHash()
	applyStats(nodes, stats)
	m.nodes = nodes
	m.selectedIdx = 0
	m.scrollOff = 0
	m.hasMore = hasMore
	m.loadingMore = false
	m.lastHash = lastNodeHash(nodes)
	m.graphRows = nil
	m.maxGraphLane = 0
	m.dagMode = false
	m.dagSections = m.dagSections[:0]
	m.mode = viewCommits
	m.restoreSelectionByHash(prevHash)
	m.ensureVisible()
	m.invalidateNodeCache()
	m.invalidateCommitCardCache()
	m.viewDirty = true
}

// SetDAGNodesWithStats atomically sets commit data with full DAG graph layout,
// transitioning from the loading spinner to the DAG commit view in one frame.
// DAG mode disables pagination (all branch-unique commits loaded at once).
// Preserves the selected commit across reloads when the hash still exists.
func (m *Model) SetDAGNodesWithStats(nodes []TreeNode, stats map[string][2]int,
	graphRows []GraphRow, maxLane int) {
	prevHash := m.SelectedHash()
	applyStats(nodes, stats)
	m.nodes = nodes
	m.selectedIdx = 0
	m.scrollOff = 0
	m.hasMore = false
	m.loadingMore = false
	m.lastHash = lastNodeHash(nodes)
	m.graphRows = graphRows
	m.maxGraphLane = maxLane
	m.dagMode = true
	m.dagSelectedRow = 0
	m.dagSelectedCol = -1 // trunk
	m.mode = viewCommits
	m.invalidateNodeCache()
	m.invalidateCommitCardCache()
	m.recomputeCommitLayout()
	m.restoreSelectionByHash(prevHash)
	m.restoreDAGSelection()
	m.ensureDAGVisible()
	m.viewDirty = true
}

// AppendNodesWithStats appends a page of commits (with stats applied) to the
// existing node list. Used for infinite scroll pagination.
func (m *Model) AppendNodesWithStats(nodes []TreeNode, stats map[string][2]int, hasMore bool) {
	applyStats(nodes, stats)
	m.nodes = append(m.nodes, nodes...)
	m.hasMore = hasMore
	m.loadingMore = false
	m.lastHash = lastNodeHash(m.nodes)
	m.invalidateNodeCache()
	m.viewDirty = true
}

// LastHash returns the hash of the last loaded commit (pagination cursor).
func (m *Model) LastHash() string {
	return m.lastHash
}

// GetDefaultBranch returns the stored default branch name.
func (m *Model) GetDefaultBranch() string {
	return m.defaultBranch
}

// applyStats merges diff stats into node slices by hash.
func applyStats(nodes []TreeNode, stats map[string][2]int) {
	for i := range nodes {
		if s, ok := stats[nodes[i].Hash]; ok {
			nodes[i].Additions = s[0]
			nodes[i].Deletions = s[1]
		}
	}
}

// lastNodeHash returns the hash of the last node, or empty if none.
func lastNodeHash(nodes []TreeNode) string {
	if len(nodes) == 0 {
		return ""
	}
	return nodes[len(nodes)-1].Hash
}

// UpdateStats merges diff statistics into existing nodes by hash.
func (m *Model) UpdateStats(stats map[string][2]int) {
	for i := range m.nodes {
		if s, ok := stats[m.nodes[i].Hash]; ok {
			m.nodes[i].Additions = s[0]
			m.nodes[i].Deletions = s[1]
		}
	}
	m.invalidateNodeCache()
	m.viewDirty = true
}

// SelectedHash returns the hash of the currently selected commit,
// or an empty string if not in commit view or no nodes.
func (m *Model) SelectedHash() string {
	if m.mode != viewCommits || len(m.nodes) == 0 {
		return ""
	}
	return m.nodes[m.selectedIdx].Hash
}

// ActiveBranch returns the name of the branch being viewed in commit or
// loading mode, or an empty string if in branch view.
func (m *Model) ActiveBranch() string {
	if m.mode == viewCommits || m.mode == viewLoading {
		return m.activeBranch
	}
	return ""
}

// InCommitView reports whether the panel is showing commits (not branches).
func (m *Model) InCommitView() bool {
	return m.mode == viewCommits
}

// InCreateInput reports whether the create branch input is active.
func (m *Model) InCreateInput() bool {
	return m.createInputActive
}

// ScrollUp scrolls the active view up by one node.
func (m *Model) ScrollUp() bool {
	off := m.activeScrollOff()
	if *off <= 0 {
		return false
	}
	(*off)--
	m.viewDirty = true
	return true
}

// ScrollDown scrolls the active view down by one node.
func (m *Model) ScrollDown() bool {
	off := m.activeScrollOff()
	if *off >= m.activeMaxScroll() {
		return false
	}
	(*off)++
	m.viewDirty = true

	// Scroll-triggered pagination: store command for parent to drain.
	if m.needsScrollLoadMore() {
		m.loadingMore = true
		m.loadingEpoch = time.Now()
		m.loadingBarUntil = time.Now().Add(minLoadingBarDuration)
		m.pendingCmd = func() tea.Msg { return LoadMoreMsg{} }
	}
	return true
}

// DrainCmd returns and clears any pending command produced by scroll-triggered
// pagination. Called by the parent in handleTick.
func (m *Model) DrainCmd() tea.Cmd {
	cmd := m.pendingCmd
	m.pendingCmd = nil
	return cmd
}

// SetHasStagedFiles updates whether the uncommitted tab has files marked
// for staging. This controls the [Commit] badge accent color.
func (m *Model) SetHasStagedFiles(staged bool) {
	if m.hasStagedFiles == staged {
		return
	}
	m.hasStagedFiles = staged
	m.viewDirty = true
}

// SetHasIndexStaged updates whether the git index has staged changes
// (e.g. via external `git add`). Combined with SetHasStagedFiles to
// determine overall commit readiness.
func (m *Model) SetHasIndexStaged(staged bool) {
	if m.hasIndexStaged == staged {
		return
	}
	m.hasIndexStaged = staged
	m.viewDirty = true
}

// SetLoadingMessage switches to the loading spinner with a custom message.
// The previous view mode is saved so ClearLoadingMessage can restore it.
func (m *Model) SetLoadingMessage(msg string) {
	m.loadingMessage = msg
	m.prevMode = m.mode
	m.loadingEpoch = time.Now()
	m.mode = viewLoading
	m.viewDirty = true
}

// ClearLoadingMessage clears the custom loading message and restores the
// view mode that was active before SetLoadingMessage.
func (m *Model) ClearLoadingMessage() {
	m.loadingMessage = ""
	m.mode = m.prevMode
	m.viewDirty = true
}

// NeedsBlink reports whether the commit input cursor or commit progress
// spinner needs blink-rate ticking.
func (m *Model) NeedsBlink() bool {
	return m.focused && (m.commitInputActive || m.commitPhase == commitInProgress || m.createInputActive)
}

// NeedsDecorTick reports whether the loading spinner or loading bar needs
// decor-rate ticking.
func (m *Model) NeedsDecorTick() bool {
	return m.mode == viewLoading || m.showLoadingBar()
}

// AdvanceSpinner advances the commit-progress spinner frame and marks dirty.
// Called from the blink tick so the spinner animates at cursor blink rate.
func (m *Model) AdvanceSpinner() {
	if m.commitPhase == commitInProgress {
		m.commitSpinner++
		m.viewDirty = true
	}
}

// SetBounceOffset updates the visual bounce displacement for rendering.
func (m *Model) SetBounceOffset(offset int) {
	if m.bounceOffset == offset {
		return
	}
	m.bounceOffset = offset
	m.viewDirty = true
}

// ViewDirty reports whether View() would produce new output.
func (m *Model) ViewDirty() bool {
	if m.viewDirty {
		m.viewDirty = false
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Mouse click handling
// ---------------------------------------------------------------------------

// ClickAt handles a left-click at the given content-relative coordinates.
// viewX is the column offset within the panel content area; viewY is the
// row offset (0 = first line inside the panel border).
func (m *Model) ClickAt(viewX, viewY int) tea.Cmd {
	// Toolbar region: last toolbarHeight rows of the panel.
	ch := m.contentHeight()
	if viewY >= ch && viewY < ch+toolbarHeight {
		return m.clickToolbar(viewX, viewY-ch)
	}
	switch m.mode {
	case viewLoading:
		return nil // absorb clicks during loading
	case viewRebasePlan:
		return m.clickRebasePlanView(viewY)
	case viewCommits:
		return m.clickCommitView(viewX, viewY)
	default:
		return m.clickBranchView(viewX, viewY)
	}
}

// clickCommitView maps a click coordinate to a commit node index.
// Only registers a hit when viewX falls within the card bounds.
func (m *Model) clickCommitView(viewX, viewY int) tea.Cmd {
	if len(m.nodes) == 0 {
		return nil
	}

	if m.dagMode && len(m.dagSections) > 0 {
		return m.clickCommitDAGView(viewX, viewY)
	}

	// Bounds-check X against the centered card area.
	cardWidth := primaryCardWidth(m.width)
	leftPad := trunkAlignedMargin(m.width, cardWidth)
	if viewX < leftPad || viewX >= leftPad+cardWidth {
		return nil
	}

	viewY -= m.viewTopPad()
	if viewY < 0 {
		return nil
	}
	idx := m.scrollOff + viewY/nodeHeight
	if idx < 0 || idx >= len(m.nodes) {
		return nil
	}
	prev := m.selectedIdx
	m.selectedIdx = idx
	m.ensureVisible()
	m.viewDirty = true
	if m.diffMode {
		m.toggleDiffSelection()
	}
	if m.selectedIdx != prev {
		return m.selectionCmd()
	}
	return nil
}

// clickCommitDAGView maps a click coordinate to a section/row in DAG mode.
// Only registers a hit when viewX falls within a card's bounds.
func (m *Model) clickCommitDAGView(viewX, viewY int) tea.Cmd {
	viewY -= m.viewTopPad()
	if viewY < 0 {
		return nil
	}

	cols := m.dagEffectiveCols()
	offCardWidth := offshootCardWidth(m.width, cols)
	totalGrid := cols*offCardWidth + max(cols-1, 0)*branchCardGap
	gridMargin := trunkAlignedMargin(m.width, totalGrid)

	trunkCardW := primaryCardWidth(m.width)
	trunkLeft := trunkAlignedMargin(m.width, trunkCardW)

	// Walk visual rows from scroll offset, accumulating heights.
	y := 0
	visRow := 0
	for _, sec := range m.dagSections {
		// Offshoot rows.
		for _, offRow := range sec.offRows {
			rowIdx := visRow
			if rowIdx < m.scrollOff {
				visRow++
				continue
			}
			rh := branchRowHeight
			if viewY >= y && viewY < y+rh {
				// Hit-test X against each card in this offshoot row.
				hitCol := -1
				for colPos, nodeIdx := range offRow {
					_ = nodeIdx
					cl := gridMargin + colPos*(offCardWidth+branchCardGap)
					if viewX >= cl && viewX < cl+offCardWidth {
						hitCol = colPos
						break
					}
				}
				if hitCol < 0 {
					return nil
				}
				prev := m.selectedIdx
				m.dagSelectedRow = rowIdx
				m.dagSelectedCol = hitCol
				m.selectedIdx = m.dagNodeIdxAt(m.dagSelectedRow, m.dagSelectedCol)
				m.viewDirty = true
				if m.diffMode {
					m.toggleDiffSelection()
				}
				if m.selectedIdx != prev {
					return m.selectionCmd()
				}
				return nil
			}
			y += rh
			visRow++
		}

		// Trunk row: check X against centered primary card.
		rowIdx := visRow
		if rowIdx < m.scrollOff {
			visRow++
			continue
		}
		rh := nodeHeight
		if viewY >= y && viewY < y+rh {
			if viewX < trunkLeft || viewX >= trunkLeft+trunkCardW {
				return nil
			}
			prev := m.selectedIdx
			m.dagSelectedRow = rowIdx
			m.dagSelectedCol = -1
			m.selectedIdx = sec.trunkIdx
			m.viewDirty = true
			if m.diffMode {
				m.toggleDiffSelection()
			}
			if m.selectedIdx != prev {
				return m.selectionCmd()
			}
			return nil
		}
		y += rh
		visRow++
	}
	return nil
}

// branchRowLines returns the height (in terminal rows) of a rendered branch
// row, accounting for expanded cards and off-center connector lines.
func (m *Model) branchRowLines(rowIdx int) int {
	if rowIdx >= len(m.offRows) {
		return max(m.branchCardHeight(m.offshootCount()), branchRowHeight)
	}
	tallest := 0
	for _, fi := range m.offRows[rowIdx] {
		tallest = max(tallest, m.branchCardHeight(fi))
	}
	return tallest + m.offRowConnectors(rowIdx)
}

// branchCardHeight returns the rendered line count for a single branch card.
// Normal cards: 4 lines. Expanded: base + divider + stats + badge lines +
// optional subview, where badge lines vary with card width.
func (m *Model) branchCardHeight(flatIdx int) int {
	const baseLines = 4 // top border + header + subject + bottom border
	if m.expandedIdx != flatIdx {
		return baseLines
	}
	b := m.branches[flatIdx]
	actions := visibleActions(b, m.defaultBranch)
	innerWidth := m.expandedCardInnerWidth()
	statsWidth := actionLinePrefixWidth(b, m.workingDirty, m.workingConflicts)
	_, badgeLineCount := computeBadgeLayout(actions, innerWidth, statsWidth)
	badgesInline := badgeLineCount <= 1
	if !badgesInline {
		const badgeIndent = 1
		_, badgeLineCount = computeBadgeLayout(actions, innerWidth, badgeIndent)
	}
	// divider(1) + content lines. Always at least the stats line.
	contentLines := max(badgeLineCount, 1)
	if !badgesInline {
		contentLines += 3 // stats + top divider + bottom divider
	}
	h := baseLines + 1 + contentLines
	// Optional subview line (delete confirm / commit input / blocked reason).
	if m.deleteConfirmActive {
		h++
	} else if m.commitInputActive && b.IsHead {
		h++
	} else {
		exp := m.buildExpansion(false)
		if exp != nil {
			if reason := exp.actionBlockedReason(b.IsHead); reason != "" {
				h += len(wrapText(reason, m.expandedCardInnerWidth()-1))
			}
		}
	}
	return h
}

// clickBranchView maps click coordinates to a branch card in the tree.
// Clicking an already-selected card toggles the expansion dropdown.
// Clicking an action badge on an expanded card executes that action.
func (m *Model) clickBranchView(viewX, viewY int) tea.Cmd {
	m.debugLogClick(viewX, viewY)
	if len(m.branches) == 0 {
		return nil
	}
	viewY -= m.viewTopPad()
	if viewY < 0 {
		return nil
	}

	oc := m.offshootCount()
	offRows := m.offshootRowCount()
	cols := m.effectiveCols()

	// Walk rows to find which row the click lands in, tracking cumulative Y.
	y := 0
	targetRow := -1
	localY := 0
	endRow := m.totalBranchRows()
	for rowIdx := m.branchScrollOff; rowIdx < endRow; rowIdx++ {
		rh := m.branchRowLines(rowIdx)
		if viewY >= y && viewY < y+rh {
			targetRow = rowIdx
			localY = viewY - y
			break
		}
		y += rh
	}
	if targetRow < 0 {
		return nil
	}

	// Determine the flat branch index from row + column.
	targetIdx := -1
	cardLeft := 0
	cardW := 0

	if targetRow < offRows {
		// Offshoot row — use grid-based positioning matching render.
		row := m.offRows[targetRow]
		cardW = offshootCardWidth(m.width, cols)
		totalGrid := cols*cardW + max(cols-1, 0)*branchCardGap
		leftMargin := trunkAlignedMargin(m.width, totalGrid)

		hitIdx := -1
		for i, flatIdx := range row {
			ci := m.offGridCol[flatIdx]
			cl := leftMargin + ci*(cardW+branchCardGap)
			if viewX >= cl && viewX < cl+cardW {
				hitIdx = i
				cardLeft = cl
				break
			}
		}
		if hitIdx < 0 {
			return nil
		}
		targetIdx = row[hitIdx]
	} else if targetRow == offRows {
		// Primary branch — check X bounds same as offshoots.
		cardW = primaryCardWidth(m.width)
		cardLeft = trunkAlignedMargin(m.width, cardW)
		if viewX < cardLeft || viewX >= cardLeft+cardW {
			return nil
		}
		targetIdx = oc
	} else {
		return nil
	}

	// If clicking the expanded card's expansion area (divider, action line,
	// confirm/input, bottom border), test against computed hit regions with
	// both X and Y coordinates to prevent cross-line interference.
	if m.expandedIdx == targetIdx {
		cardH := m.branchCardHeight(targetIdx)
		localX := viewX - cardLeft - 1 // -1 for left border "│"
		m.debugLogCardHit(targetIdx, localY, localX, cardLeft, cardW, cardH)
		if localY >= 3 && localY < cardH {
			if localX >= 0 {
				for _, r := range m.expandedHitRegions() {
					if localY == r.Y && localX >= r.XMin && localX < r.XMax {
						return m.handleCardHit(r, localX)
					}
				}
			}
			m.viewDirty = true
			return nil
		}
	}

	if m.branchIdx == targetIdx {
		m.expandBranch()
	} else {
		// Collapse any expanded card before switching selection.
		if m.expandedIdx >= 0 {
			m.collapseBranch()
		}
		m.branchIdx = targetIdx
	}

	m.ensureBranchVisible()
	m.viewDirty = true
	return nil
}

// clickActionBadge selects the badge at the given action index and executes
// or toggles its sub-view. Switch executes immediately; Commit/Delete toggle
// their inline sub-views.
func (m *Model) clickActionBadge(idx int) tea.Cmd {
	// Clear stale sub-view state when switching to a different action,
	// so toggleExpandedSubview starts fresh instead of toggling off
	// a sub-view that belonged to the previous action.
	if m.expandedAction != idx {
		m.deleteConfirmActive = false
		m.deleteConfirmYes = false
		m.clearCommitInput()
	}
	m.expandedAction = idx
	m.viewDirty = true

	actionID := m.expandedActionID()
	if !m.isActionEnabled(actionID) {
		return nil
	}
	if actionID == branchActionSwitch {
		return m.executeExpandedAction()
	}
	m.toggleExpandedSubview()
	return nil
}

// expandedCardInnerWidth returns the inner content width of the expanded card.
func (m *Model) expandedCardInnerWidth() int {
	oc := m.offshootCount()
	const borderCols = 2
	if m.expandedIdx >= oc {
		return max(primaryCardWidth(m.width)-borderCols, 0)
	}
	return max(offshootCardWidth(m.width, m.effectiveCols())-borderCols, 0)
}

// expandedHitRegions computes all clickable regions for the currently expanded
// branch card. Badge positions and the subview line are derived dynamically
// from computeBadgeLayout so they stay correct when badges wrap.
func (m *Model) expandedHitRegions() []cardHitRegion {
	if m.expandedIdx < 0 || m.expandedIdx >= len(m.branches) {
		return nil
	}
	b := m.branches[m.expandedIdx]
	actions := visibleActions(b, m.defaultBranch)
	innerWidth := m.expandedCardInnerWidth()
	statsWidth := actionLinePrefixWidth(b, m.workingDirty, m.workingConflicts)

	// Mirror buildExpandedLines: try inline first, if badges overflow
	// move them all to a dedicated row below stats.
	placements, badgeLineCount := computeBadgeLayout(actions, innerWidth, statsWidth)
	badgesInline := badgeLineCount <= 1
	if !badgesInline {
		const badgeIndent = 1
		placements, badgeLineCount = computeBadgeLayout(actions, innerWidth, badgeIndent)
	}

	// When badges are inline, they start at cardActionLine (stats line).
	// When on a dedicated row, stats + top divider precede badges.
	badgeBaseLine := cardActionLine
	if !badgesInline {
		badgeBaseLine = cardActionLine + 2 // stats + top divider
	}

	var regions []cardHitRegion
	for i, bp := range placements {
		regions = append(regions, cardHitRegion{
			Y:    badgeBaseLine + bp.line,
			XMin: bp.x,
			XMax: bp.x + bp.w,
			Kind: hitActionBadge,
			Idx:  i,
		})
	}

	// Total content lines: stats + dividers + badge lines.
	totalContentLines := badgeLineCount
	if !badgesInline {
		totalContentLines += 3 // stats + top divider + bottom divider
	}

	// Sub-view line sits immediately after the last content line.
	subviewLine := cardActionLine + totalContentLines
	if m.deleteConfirmActive {
		lay := computeDeleteConfirmLayout(innerWidth)
		regions = append(regions,
			cardHitRegion{
				Y:    subviewLine,
				XMin: lay.yesStart,
				XMax: lay.yesStart + len(deleteConfirmYesLabel),
				Kind: hitDeleteYes,
			},
			cardHitRegion{
				Y:    subviewLine,
				XMin: lay.noStart,
				XMax: lay.noStart + len(deleteConfirmNoLabel),
				Kind: hitDeleteNo,
			},
		)
	} else if m.commitInputActive && m.commitPhase == commitIdle {
		regions = append(regions, cardHitRegion{
			Y:    subviewLine,
			XMin: commitInputLabelWidth,
			XMax: innerWidth,
			Kind: hitCommitInput,
		})
	}

	return regions
}

// handleCardHit dispatches a click on a resolved hit region.
func (m *Model) handleCardHit(r cardHitRegion, localX int) tea.Cmd {
	switch r.Kind {
	case hitActionBadge:
		return m.clickActionBadge(r.Idx)
	case hitDeleteYes:
		m.deleteConfirmYes = true
		m.viewDirty = true
	case hitDeleteNo:
		m.deleteConfirmYes = false
		m.viewDirty = true
	case hitCommitInput:
		runes := []rune(m.commitMsg)
		m.commitCursor = clampInt(localX-commitInputLabelWidth, 0, len(runes))
		m.viewDirty = true
	}
	return nil
}

// ---------------------------------------------------------------------------
// Toolbar hover and click
// ---------------------------------------------------------------------------

// HandleToolbarHover updates the hovered toolbar button from mouse position.
// viewX, viewY are content-relative coordinates within the full panel.
func (m *Model) HandleToolbarHover(viewX, viewY int) {
	ch := m.contentHeight()
	// Only the button content row (second toolbar row) is interactive.
	if viewY != ch+1 || m.createInputActive {
		m.setHoverButton(-1)
		return
	}
	m.setHoverButton(m.toolbarButtonAtX(viewX))
}

// ClearHover resets hover state when the mouse leaves the panel.
func (m *Model) ClearHover() {
	m.setHoverButton(-1)
}

// setHoverButton updates hoverButtonIdx, marking dirty only on change.
func (m *Model) setHoverButton(idx int) {
	if m.hoverButtonIdx == idx {
		return
	}
	m.hoverButtonIdx = idx
	m.viewDirty = true
}

// toolbarButtonAtX returns the unified toolbar button index at the given X
// coordinate, or -1 if no button is hit. In diff mode, indices 0..len(left)-1
// map to left buttons, len(left)..len(left)+len(right)-1 map to right buttons.
func (m *Model) toolbarButtonAtX(viewX int) int {
	left := m.diffToolbarLeftButtons()
	right := m.toolbarButtons()

	// Hit-test left button group (left-aligned, no leading border).
	// Layout: content₀ │ content₁ │
	if len(left) > 0 {
		lWidths := toolbarCellWidths(left)
		x := 0
		for i, w := range lWidths {
			if viewX >= x && viewX < x+w {
				return i
			}
			x += w + 1 // cell width + │ separator
		}
	}

	// Hit-test right button group (right-aligned).
	if len(right) == 0 {
		return -1
	}
	rWidths := toolbarCellWidths(right)
	totalInner := 0
	for _, w := range rWidths {
		totalInner += w
	}
	groupWidth := len(right) + totalInner
	leftPad := max(m.width-groupWidth, 0)

	x := leftPad
	for i, w := range rWidths {
		x++ // │ separator
		if viewX >= x && viewX < x+w {
			return len(left) + i
		}
		x += w
	}
	return -1
}

// clickToolbar handles a click within the toolbar area.
// localY is relative to the toolbar top (0 = border row, 1 = content row).
func (m *Model) clickToolbar(viewX, localY int) tea.Cmd {
	if localY != 1 {
		return nil
	}
	if m.createInputActive {
		idx := m.toolbarButtonAtX(viewX)
		left := m.diffToolbarLeftButtons()
		right := m.toolbarButtons()
		all := slices.Concat(left, right)
		if idx >= 0 && idx < len(all) && all[idx] == toolbarCreate {
			m.clearCreateInput()
			m.toolbarFocused = false
			m.viewDirty = true
		}
		return nil
	}
	idx := m.toolbarButtonAtX(viewX)
	if idx < 0 {
		return nil
	}
	// Resolve the button ID to check enabled state.
	left := m.diffToolbarLeftButtons()
	right := m.toolbarButtons()
	all := slices.Concat(left, right)
	if idx < len(all) && !m.isToolbarButtonEnabled(all[idx]) {
		return nil
	}
	if m.expandedIdx >= 0 {
		m.collapseBranch()
	}
	if m.toolbarFocused && m.toolbarAction == idx {
		// Re-clicking the focused button clears focus and executes its
		// action (e.g. toggle diff mode off).
		m.toolbarFocused = false
		m.viewDirty = true
		return m.executeToolbarAction()
	}
	m.toolbarFocused = true
	m.toolbarAction = idx
	m.viewDirty = true
	return m.executeToolbarAction()
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m *Model) View(cursorVisible bool) string {
	var content string
	switch m.mode {
	case viewLoading:
		content = m.viewLoadingSpinner()
	case viewCommits:
		content = m.viewCommitCards()
	case viewRebasePlan:
		content = m.viewRebasePlan()
	case viewBranchPick:
		content = m.viewBranchPicker()
	default:
		content = m.viewBranchTree(cursorVisible)
	}

	toolbar := m.toolbarView(cursorVisible)
	return content + "\n" + toolbar
}

// toolbarView renders the bottom toolbar bar with mode-aware buttons.
func (m *Model) toolbarView(cursorVisible bool) string {
	if m.createInputActive {
		return renderCreateInputToolbar(m.toolbarButtons(), m.createBranchName, m.createCursor, cursorVisible, m.width, m.theme.Palette)
	}
	hasLeftButtons := m.diffMode || m.mergeMode || m.resetMode || m.revertMode ||
		m.cherryPickMode || m.conflictMode || m.mode == viewRebasePlan
	if hasLeftButtons {
		left := m.diffToolbarLeftButtons()
		right := m.toolbarButtons()
		hint := ""
		if len(left) == 0 {
			if m.mergeMode {
				hint = "Select 2 branches to merge"
			} else if m.mode == viewBranches {
				hint = "Select 2+ branches to diff"
			} else {
				hint = "Select 2+ commits to diff"
			}
		}
		if m.cherryPickMode && len(left) > 0 {
			if m.mode == viewBranchPick {
				hint = "Select target branch"
			} else {
				hint = fmt.Sprintf("%d selected", len(m.cherryPickSelections))
			}
		}
		if m.conflictMode {
			hint = fmt.Sprintf("Step %d/%d: %s", m.conflictCurrent+1, m.conflictTotal, m.conflictSubject)
		}
		return renderDiffToolbar(left, right, m.toolbarAction, m.toolbarFocused,
			m.hoverButtonIdx, m.toolbarActiveIdx(), hint, m.width, m.theme.Palette)
	}
	label := "Branches"
	if m.mode == viewCommits {
		label = "Commits"
	}
	buttons := m.toolbarButtons()
	return renderToolbarButtons(buttons, m.toolbarAction, m.toolbarFocused, m.hoverButtonIdx, m.toolbarActiveIdx(), m.disabledToolbarSet(buttons), label, m.width, m.theme.Palette)
}

// toolbarActiveIdx returns the index of the currently toggled-on toolbar
// button, or -1 if none. Diff and Merge buttons support toggle state.
// The index is offset by the left button count.
func (m *Model) toolbarActiveIdx() int {
	if !m.diffMode && !m.mergeMode && !m.resetMode && !m.revertMode && !m.cherryPickMode {
		return -1
	}
	leftCount := len(m.diffToolbarLeftButtons())
	target := toolbarDiff
	if m.mergeMode {
		target = toolbarMerge
	}
	if m.resetMode {
		target = toolbarReset
	}
	if m.revertMode {
		target = toolbarRevert
	}
	if m.cherryPickMode {
		target = toolbarCherryPick
	}
	for i, id := range m.toolbarButtons() {
		if id == target {
			return leftCount + i
		}
	}
	return -1
}

// viewBranchTree renders the branch tree view with side-by-side offshoot cards.
// Individual cards are cached and only re-rendered when their inputs change.
func (m *Model) viewBranchTree(cursorVisible bool) string {
	if len(m.branches) == 0 {
		return m.renderPlaceholder("No branches")
	}

	cols := m.effectiveCols()
	cardWidth := offshootCardWidth(m.width, cols)
	oc := m.offshootCount()
	offRows := m.offshootRowCount()
	endRow := m.visibleEndRow()
	trunkPos := m.width / 2

	const borderCols = 2

	wt := m.buildWorkingTreeState()
	exp := m.buildExpansion(cursorVisible)
	lines := make([]string, 0, m.height)

	// Precompute which branches have a child offshoot above them.
	hasChildAbove := make(map[string]bool, oc)
	for i := range oc {
		if m.offDepth[i] > 0 {
			hasChildAbove[m.branches[i].Parent] = true
		}
	}

	// Precompute which rows have a trunk line above them. A trunk line
	// exists above row R if any row before R connects to the trunk
	// (depth 0 row with a merge line). Depth > 0 rows never produce
	// trunk lines, so their presence doesn't set the flag.
	trunkAbove := make([]bool, offRows)
	seenTrunk := false
	for r := range offRows {
		trunkAbove[r] = seenTrunk
		if m.offDepth[m.offRows[r][0]] == 0 {
			seenTrunk = true
		}
	}

	// Build name→flat-index map for parent position lookup.
	offNameIdx := make(map[string]int, oc)
	for i := range oc {
		offNameIdx[m.branches[i].Name] = i
	}

	// Track active merge connector columns from rows above. When sibling
	// rows wrap, the merge target connector must pass through subsequent
	// sibling rows' card area. A column is "consumed" when a card in the
	// current row has its center at that position (it enters the card's
	// top border via hasTrunkTop).
	activeMergeCols := map[int]struct{}{}

	for rowIdx := m.branchScrollOff; rowIdx < endRow; rowIdx++ {
		if rowIdx < offRows {
			// Offshoot row — build cards via cache, compose into row.
			row := m.offRows[rowIdx]
			rowCols := len(row)
			hasTrunkAbove := trunkAbove[rowIdx]

			// All cards in a row share the same depth tier.
			rowDepth := m.offDepth[row[0]]

			// Use fixed grid for consistent column alignment across rows.
			totalGrid := cols*cardWidth + max(cols-1, 0)*branchCardGap
			leftMargin := trunkAlignedMargin(m.width, totalGrid)
			innerWidth := max(cardWidth-borderCols, 0)

			// Merge target: trunk for depth 0, parent's card center for depth > 0.
			mergeTarget := trunkPos
			if rowDepth > 0 {
				parentName := m.branches[row[0]].Parent
				if pi, ok := offNameIdx[parentName]; ok {
					parentGridCol := m.offGridCol[pi]
					mergeTarget = leftMargin + parentGridCol*(cardWidth+branchCardGap) + cardWidth/2
				}
			}

			// Consume merge columns that enter a card in this row
			// (the card's top border handles the connector via hasTrunkTop).
			for _, flatIdx := range row {
				gc := m.offGridCol[flatIdx]
				center := leftMargin + gc*(cardWidth+branchCardGap) + cardWidth/2
				delete(activeMergeCols, center)
			}

			// Build merge-above columns for pass-through.
			mergeAboveCols := make([]int, 0, len(activeMergeCols))
			for col := range activeMergeCols {
				mergeAboveCols = append(mergeAboveCols, col)
			}

			colIndices := make([]int, rowCols)
			cardSlices := make([][]string, rowCols)
			for i, flatIdx := range row {
				ci := m.offGridCol[flatIdx]
				colIndices[i] = ci
				b := m.branches[flatIdx]
				selected := flatIdx == m.branchIdx
				isExpanded := flatIdx == m.expandedIdx
				cardLeft := leftMargin + ci*(cardWidth+branchCardGap)

				// Determine top/bottom border connectors.
				cardCenter := innerWidth / 2
				trunkAtCard := trunkPos - cardLeft - 1

				// Top connector: children merge into card center;
				// trunk from previous depth-0 row enters at trunkAtCard.
				childAbove := hasChildAbove[b.Name]
				hasTrunkTop := childAbove || hasTrunkAbove
				trunkInnerTop := cardCenter
				if !childAbove {
					trunkInnerTop = trunkAtCard
				}

				// Bottom connector: all offshoots connect to the
				// merge line below at their card center.
				hasTrunkBot := true
				trunkInnerBot := cardCenter

				var cardExp *branchExpansion
				if isExpanded {
					cardExp = exp
				}
				cardSlices[i] = m.getCachedCard(flatIdx, b, selected,
					innerWidth, trunkInnerTop, trunkInnerBot, hasTrunkTop, hasTrunkBot,
					isExpanded, cardExp, wt, m.branchOverlay(flatIdx))
			}

			rowLines := composeOffshootRow(cardSlices, colIndices, cardWidth, cols, m.width, m.theme, hasTrunkAbove, mergeTarget, mergeAboveCols)
			lines = append(lines, rowLines...)

			// This row's merge target is now active going down.
			activeMergeCols[mergeTarget] = struct{}{}
		} else {
			// Primary row — build card via cache, compose into row.
			flatIdx := oc
			primary := m.branches[len(m.branches)-1]
			selected := m.branchIdx == oc
			isExpanded := m.expandedIdx == oc

			primaryCW := primaryCardWidth(m.width)
			innerWidth := max(primaryCW-borderCols, 0)
			leftPad := trunkAlignedMargin(m.width, primaryCW)
			trunkInner := trunkPos - leftPad - 1

			hasTrunkTop := oc > 0
			trunkInnerTop := trunkInner
			if hasChildAbove[primary.Name] {
				trunkInnerTop = innerWidth / 2
			}

			var cardExp *branchExpansion
			if isExpanded {
				cardExp = exp
			}
			cardLines := m.getCachedCard(flatIdx, primary, selected,
				innerWidth, trunkInnerTop, trunkInner, hasTrunkTop, false,
				isExpanded, cardExp, wt, m.branchOverlay(flatIdx))

			rowLines := composePrimaryRow(cardLines, m.width)
			lines = append(lines, rowLines...)
		}
	}

	return m.padViewport(lines)
}

// buildWorkingTreeState returns the working tree state for rendering.
func (m *Model) buildWorkingTreeState() workingTreeState {
	return workingTreeState{
		dirty:     m.workingDirty,
		conflicts: m.workingConflicts,
	}
}

// buildExpansion returns the current expansion state for rendering.
func (m *Model) buildExpansion(cursorVisible bool) *branchExpansion {
	if m.expandedIdx < 0 {
		return nil
	}
	return &branchExpansion{
		wt:               m.buildWorkingTreeState(),
		defaultBranch:    m.defaultBranch,
		selectedActionID: m.expandedActionID(),
		hasStagedFiles:   m.hasStagedFiles || m.hasIndexStaged,
		commitInput:      m.commitInputActive,
		commitPhase:      m.commitPhase,
		commitMsg:        m.commitMsg,
		commitCursor:     m.commitCursor,
		commitSpinner:    m.commitSpinner,
		cursorVisible:    cursorVisible,
		deleteConfirm:    m.deleteConfirmActive,
		deleteConfirmYes: m.deleteConfirmYes,
	}
}

// spinnerFramePeriod is the duration each spinner frame is displayed.
// Derived from: 10 braille frames × 80ms = 800ms per full cycle.
const spinnerFramePeriod = 80 * time.Millisecond

// viewLoadingSpinner renders an animated spinner while commits+stats load.
// The frame is derived from wall-clock elapsed time since loading started,
// so it advances at a consistent rate driven by the decor tick (100ms).
func (m *Model) viewLoadingSpinner() string {
	elapsed := time.Since(m.loadingEpoch)
	frame := spinnerFrames[int(elapsed/spinnerFramePeriod)%len(spinnerFrames)]
	spinSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary)
	textSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	label := "Loading commits..."
	if m.loadingMessage != "" {
		label = m.loadingMessage
	}
	msg := spinSt.Render(frame) + " " + textSt.Render(label)
	msgWidth := lipgloss.Width(msg)

	ch := m.contentHeight()
	lines := make([]string, ch)
	emptyLine := strings.Repeat(" ", max(m.width, 0))
	midRow := ch / 2
	leftPad := max((m.width-msgWidth)/2, 0)
	rightPad := max(m.width-leftPad-msgWidth, 0)
	centeredLine := strings.Repeat(" ", leftPad) + msg + strings.Repeat(" ", rightPad)

	for i := range lines {
		if i == midRow {
			lines[i] = centeredLine
		} else {
			lines[i] = emptyLine
		}
	}
	return strings.Join(lines, "\n")
}

// renderLoadingBar returns a single line with a centered animated spinner,
// used as a top overlay during infinite scroll page loads. Time-based
// animation driven by the decor tick (100ms).
func (m *Model) renderLoadingBar() string {
	elapsed := time.Since(m.loadingEpoch)
	frame := spinnerFrames[int(elapsed/spinnerFramePeriod)%len(spinnerFrames)]
	spinSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary)
	textSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	content := spinSt.Render(frame) + " " + textSt.Render("Loading more...")
	contentWidth := lipgloss.Width(content)
	leftPad := max((m.width-contentWidth)/2, 0)
	rightPad := max(m.width-leftPad-contentWidth, 0)
	return strings.Repeat(" ", leftPad) + content + strings.Repeat(" ", rightPad)
}

// viewCommitCards renders the commit cards view with centered cards and trunk
// connectors matching the branch tree layout. Individual nodes are cached
// and only re-rendered when their inputs change (typically 0-2 per frame).
func (m *Model) viewCommitCards() string {
	if len(m.nodes) == 0 {
		return m.renderPlaceholder("Loading commits...")
	}

	if m.dagMode && len(m.dagSections) > 0 {
		return m.viewCommitDAG()
	}

	visible := m.commitVisibleRange()
	lines := make([]string, 0, m.height)

	lastIdx := len(m.nodes) - 1
	for _, idx := range visible {
		selected := idx == m.selectedIdx
		isFirst := idx == 0
		isLast := idx == lastIdx && !m.hasMore
		diff := m.diffOverlayFor(m.nodes[idx].Hash)
		nodeLines := m.getCachedNode(idx, m.nodes[idx], selected, isFirst, isLast, nil, 0, diff)
		lines = append(lines, nodeLines...)
	}

	return m.padViewport(lines)
}

// viewCommitDAG renders the commit DAG with trunk-and-offshoots layout.
// Trunk commits are centered primary cards; merge offshoots appear as
// side-by-side grid rows above each merge point.
func (m *Model) viewCommitDAG() string {
	cols := m.dagEffectiveCols()
	cardWidth := offshootCardWidth(m.width, cols)
	trunkPos := m.width / 2
	const borderCols = 2

	lines := make([]string, 0, m.height)
	cacheIdx := 0

	selectedNode := m.dagNodeIdxAt(m.dagSelectedRow, m.dagSelectedCol)

	// Scroll: skip dagScrollOff visual rows.
	visRow := 0
	for si, sec := range m.dagSections {
		isLastSection := si == len(m.dagSections)-1

		// Offshoot rows above this merge point.
		for ri, offRow := range sec.offRows {
			if visRow < m.scrollOff {
				visRow++
				cacheIdx += len(offRow)
				continue
			}
			if len(lines) >= m.contentHeight() {
				return m.padViewport(lines)
			}

			rowCols := len(offRow)
			innerWidth := max(cardWidth-borderCols, 0)

			// All offshoots merge to the trunk.
			mergeTarget := trunkPos
			hasTrunkAbove := ri > 0 || (si > 0)

			colIndices := make([]int, rowCols)
			cardSlices := make([][]string, rowCols)
			for i, nodeIdx := range offRow {
				colIndices[i] = i
				n := m.nodes[nodeIdx]
				selected := nodeIdx == selectedNode

				cardCenter := innerWidth / 2
				// Offshoot cards connect downward to the merge line (hasTrunkBot).
				// They only have a top connector if a trunk line passes through
				// the card position itself (not just the gap).
				hasTrunkBot := true
				trunkInnerBot := cardCenter

				diff := m.diffOverlayFor(n.Hash)
				cardSlices[i] = m.getCachedCommitCard(cacheIdx, n, selected,
					innerWidth, cardCenter, trunkInnerBot, false, hasTrunkBot, diff)
				cacheIdx++
			}

			rowLines := composeOffshootRow(cardSlices, colIndices, cardWidth, cols, m.width, m.theme, hasTrunkAbove, mergeTarget, nil)
			lines = append(lines, rowLines...)
			visRow++
		}

		// Trunk card.
		if visRow < m.scrollOff {
			visRow++
			cacheIdx++
			continue
		}
		if len(lines) >= m.contentHeight() {
			return m.padViewport(lines)
		}

		n := m.nodes[sec.trunkIdx]
		selected := sec.trunkIdx == selectedNode
		primaryCW := primaryCardWidth(m.width)
		innerWidth := max(primaryCW-borderCols, 0)
		leftPad := trunkAlignedMargin(m.width, primaryCW)
		trunkInner := trunkPos - leftPad - 1

		hasTrunkTop := si > 0 || len(sec.offRows) > 0
		hasTrunkBot := !isLastSection
		trunkInnerTop := trunkInner
		trunkInnerBot := trunkInner

		diff := m.diffOverlayFor(n.Hash)
		cardLines := m.getCachedCommitCard(cacheIdx, n, selected,
			innerWidth, trunkInnerTop, trunkInnerBot, hasTrunkTop, hasTrunkBot, diff)
		cacheIdx++

		// Compose trunk card with centering and a trunk connector below.
		pad := strings.Repeat(" ", leftPad)
		vis := leftPad + primaryCW
		for _, cl := range cardLines {
			lines = append(lines, padRight(pad+cl, vis, m.width))
		}
		// Trunk connector line between sections.
		if hasTrunkBot {
			trunkSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)
			trunk := strings.Repeat(" ", trunkPos) + trunkSt.Render("│")
			lines = append(lines, padRight(trunk, trunkPos+1, m.width))
		} else {
			lines = append(lines, strings.Repeat(" ", max(m.width, 0)))
		}
		visRow++
	}

	return m.padViewport(lines)
}

// dagEffectiveCols returns the max offshoot cards per row in DAG mode.
func (m *Model) dagEffectiveCols() int {
	maxInRow := 0
	for _, sec := range m.dagSections {
		for _, row := range sec.offRows {
			maxInRow = max(maxInRow, len(row))
		}
	}
	return max(maxInRow, 1)
}

// padViewport vertically centers lines within the viewport height and
// applies bounce offset. When a page load is in flight, overlays a
// loading bar on the last line (no layout shift).
func (m *Model) padViewport(lines []string) string {
	ch := m.contentHeight()
	emptyLine := strings.Repeat(" ", max(m.width, 0))
	if len(lines) < ch {
		topPad := (ch - len(lines)) / 2
		centered := make([]string, ch)
		for i := range centered {
			centered[i] = emptyLine
		}
		copy(centered[topPad:], lines)
		lines = centered
	}
	if len(lines) > ch {
		lines = lines[:ch]
	}
	if m.showLoadingBar() && len(lines) > 0 {
		lines[0] = m.renderLoadingBar()
	}
	lines = applyBounceShift(lines, m.bounceOffset, ch, emptyLine)
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (m *Model) handleKey(km tea.KeyMsg) tea.Cmd {
	switch m.mode {
	case viewLoading:
		if km.String() == "esc" {
			m.ExitToBranches()
		}
		return nil
	case viewCommits:
		return m.handleCommitKey(km)
	case viewRebasePlan:
		return m.handleRebasePlanKey(km)
	case viewBranchPick:
		return m.handleBranchPickerKey(km)
	default:
		return m.handleBranchKey(km)
	}
}

// handleBranchKey processes keys in branch tree view.
// Navigation is grid-based: j/k move between rows, h/l move within a row.
// When a card is expanded, h/l move between actions, enter executes.
// Tab cycles through toolbar buttons; enter executes when toolbar is focused.
func (m *Model) handleBranchKey(km tea.KeyMsg) tea.Cmd {
	if m.createInputActive {
		return m.handleCreateInputKey(km)
	}
	if len(m.branches) == 0 {
		return nil
	}

	// Diff/merge mode (not toolbar-focused): Enter/Space toggle selection, Esc exits.
	if (m.diffMode || m.mergeMode) && !m.toolbarFocused && m.expandedIdx < 0 {
		switch km.String() {
		case "enter", " ":
			if m.mergeMode {
				m.toggleMergeSelection()
			} else {
				m.toggleBranchDiffSelection()
			}
		case "esc":
			if m.mergeMode {
				m.exitMergeMode()
			} else {
				m.exitBranchDiffMode()
			}
		case "j", "down", "shift+down":
			m.moveBranchDown()
		case "k", "up", "shift+up":
			m.moveBranchUp()
		case "l", "right":
			m.moveBranchRight()
		case "h", "left":
			m.moveBranchLeft()
		case "g":
			m.branchIdx = 0
		case "G":
			m.branchIdx = len(m.branches) - 1
		case "ctrl+d":
			for range m.halfBranchPage() {
				m.moveBranchDown()
			}
		case "ctrl+u":
			for range m.halfBranchPage() {
				m.moveBranchUp()
			}
		case "tab":
			m.cycleCommitToolbar(1)
			m.viewDirty = true
			return nil
		case "shift+tab":
			m.cycleCommitToolbar(-1)
			m.viewDirty = true
			return nil
		default:
			return nil
		}
		m.ensureBranchVisible()
		m.viewDirty = true
		return nil
	}

	// Expanded card: route to action handler.
	if m.expandedIdx >= 0 {
		return m.handleExpandedKey(km)
	}

	// Toolbar-focused: handle toolbar keys.
	if m.toolbarFocused {
		switch km.String() {
		case "tab":
			m.cycleCommitToolbar(1)
			m.viewDirty = true
			return nil
		case "shift+tab":
			m.cycleCommitToolbar(-1)
			m.viewDirty = true
			return nil
		case "h", "left":
			m.cycleCommitToolbar(-1)
			if !m.toolbarFocused {
				m.toolbarFocused = true // left/right don't exit focus
			}
			m.viewDirty = true
			return nil
		case "l", "right":
			m.cycleCommitToolbar(1)
			if !m.toolbarFocused {
				m.toolbarFocused = true // left/right don't exit focus
			}
			m.viewDirty = true
			return nil
		case "enter", " ":
			m.viewDirty = true
			return m.executeToolbarAction()
		case "esc", "q":
			m.toolbarFocused = false
			m.viewDirty = true
			return nil
		default:
			m.toolbarFocused = false
			return m.handleBranchKey(km)
		}
	}

	switch km.String() {
	case "j", "down", "shift+down":
		m.moveBranchDown()
	case "k", "up", "shift+up":
		m.moveBranchUp()
	case "l", "right":
		m.moveBranchRight()
	case "h", "left":
		m.moveBranchLeft()
	case "g":
		m.branchIdx = 0
	case "G":
		m.branchIdx = len(m.branches) - 1
	case "ctrl+d":
		for range m.halfBranchPage() {
			m.moveBranchDown()
		}
	case "ctrl+u":
		for range m.halfBranchPage() {
			m.moveBranchUp()
		}
	case "tab":
		buttons := m.toolbarButtons()
		first := m.firstEnabledToolbar(buttons)
		if first < 0 {
			return nil
		}
		m.toolbarFocused = true
		m.toolbarAction = first
		m.viewDirty = true
		return nil
	case "shift+tab":
		m.expandBranch()
	case "enter":
		return m.enterBranch()
	default:
		return nil
	}

	m.ensureBranchVisible()
	m.viewDirty = true
	return nil
}

// nextEnabledToolbar returns the next enabled toolbar index from `from` in the
// given direction (+1 or -1), skipping disabled buttons. Returns -1 if none.
func (m *Model) nextEnabledToolbar(from, delta int, buttons []int) int {
	for i := from + delta; i >= 0 && i < len(buttons); i += delta {
		if m.isToolbarButtonEnabled(buttons[i]) {
			return i
		}
	}
	return -1
}

// firstEnabledToolbar returns the index of the first enabled button, or -1.
func (m *Model) firstEnabledToolbar(buttons []int) int {
	for i, id := range buttons {
		if m.isToolbarButtonEnabled(id) {
			return i
		}
	}
	return -1
}

// lastEnabledToolbar returns the index of the last enabled button, or -1.
func (m *Model) lastEnabledToolbar(buttons []int) int {
	for i := len(buttons) - 1; i >= 0; i-- {
		if m.isToolbarButtonEnabled(buttons[i]) {
			return i
		}
	}
	return -1
}

// expandedVisibleActions returns the ordered action IDs for the currently
// expanded branch. Delegates to visibleActions for the pure logic.
func (m *Model) expandedVisibleActions() []int {
	if m.expandedIdx < 0 || m.expandedIdx >= len(m.branches) {
		return nil
	}
	return visibleActions(m.branches[m.expandedIdx], m.defaultBranch)
}

// expandedActionID returns the action ID for the current selection index.
func (m *Model) expandedActionID() int {
	actions := m.expandedVisibleActions()
	if m.expandedAction >= 0 && m.expandedAction < len(actions) {
		return actions[m.expandedAction]
	}
	return -1
}

// isActionEnabled reports whether the given action ID is currently usable.
func (m *Model) isActionEnabled(actionID int) bool {
	if m.expandedIdx < 0 || m.expandedIdx >= len(m.branches) {
		return false
	}
	b := m.branches[m.expandedIdx]
	switch actionID {
	case branchActionCommit:
		return b.IsHead && (m.hasStagedFiles || m.hasIndexStaged)
	case branchActionSwitch:
		return !b.IsHead && !m.workingDirty && !m.workingConflicts
	case branchActionDelete:
		return !b.IsHead && b.Name != m.defaultBranch
	}
	return false
}

// nextAction returns the next action index after from, or from if already
// at the last action.
func (m *Model) nextAction(from int) int {
	actionMax := len(m.expandedVisibleActions())
	if from+1 < actionMax {
		return from + 1
	}
	return from
}

// prevAction returns the previous action index before from, or from if
// already at the first action.
func (m *Model) prevAction(from int) int {
	if from > 0 {
		return from - 1
	}
	return from
}

// handleExpandedKey processes keys when a branch card is expanded.
// Tab/shift+tab cycle between card action badges (wrapping); they do NOT
// escape to the toolbar. Space/enter triggers the focused action; esc
// collapses the card. When the commit input is active, keys route to the
// inline text input.
func (m *Model) handleExpandedKey(km tea.KeyMsg) tea.Cmd {
	if m.deleteConfirmActive {
		return m.handleDeleteConfirmKey(km)
	}
	if m.commitInputActive {
		return m.handleCommitInputKey(km)
	}
	if m.toolbarFocused {
		return m.handleExpandedToolbarKey(km)
	}

	actionMax := len(m.expandedVisibleActions())
	if actionMax == 0 {
		return nil
	}

	switch km.String() {
	case "h", "left":
		m.expandedAction = m.prevAction(m.expandedAction)
	case "l", "right":
		m.expandedAction = m.nextAction(m.expandedAction)
	case "tab":
		m.expandedAction = (m.expandedAction + 1) % actionMax
	case "shift+tab":
		m.collapseBranch()
	case "enter":
		return m.executeExpandedAction()
	case " ":
		return m.toggleExpandedSubview()
	case "esc", "q":
		m.collapseBranch()
	case "j", "down":
		return m.toggleExpandedSubview()
	case "k", "up":
		m.collapseBranch()
	default:
		return nil
	}
	m.viewDirty = true
	return nil
}

// handleExpandedToolbarKey processes keys when toolbar is focused within
// an expanded branch card. Tab wraps back to card actions.
func (m *Model) handleExpandedToolbarKey(km tea.KeyMsg) tea.Cmd {
	buttons := m.toolbarButtons()

	switch km.String() {
	case "tab", "shift+tab":
		// Return focus to card actions — tab is constrained to
		// card options while a card is expanded.
		m.toolbarFocused = false
		m.expandedAction = 0
	case "h", "left":
		if prev := m.nextEnabledToolbar(m.toolbarAction, -1, buttons); prev >= 0 {
			m.toolbarAction = prev
		}
	case "l", "right":
		if next := m.nextEnabledToolbar(m.toolbarAction, +1, buttons); next >= 0 {
			m.toolbarAction = next
		}
	case "enter", " ":
		m.viewDirty = true
		return m.executeToolbarAction()
	case "esc", "q":
		m.collapseBranch()
	case "j", "down":
		m.collapseBranch()
		m.moveBranchDown()
		m.ensureBranchVisible()
	case "k", "up":
		m.collapseBranch()
		m.moveBranchUp()
		m.ensureBranchVisible()
	default:
		return nil
	}
	m.viewDirty = true
	return nil
}

// handleCommitInputKey processes keys when the commit message input is active.
func (m *Model) handleCommitInputKey(km tea.KeyMsg) tea.Cmd {
	// During progress or success flash, only esc cancels.
	if m.commitPhase != commitIdle {
		if km.String() == "esc" {
			m.clearCommitInput()
		}
		return nil
	}

	switch km.String() {
	case "enter":
		msg := strings.TrimSpace(m.commitMsg)
		if msg == "" {
			return nil
		}
		m.commitPhase = commitInProgress
		m.commitSpinner = 0
		m.viewDirty = true
		return func() tea.Msg { return CommitRequestMsg{Message: msg} }
	case "shift+tab":
		m.collapseBranch()
	case "esc", "tab", "up":
		m.clearCommitInput()
	case "backspace":
		if m.commitCursor > 0 {
			runes := []rune(m.commitMsg)
			runes = append(runes[:m.commitCursor-1], runes[m.commitCursor:]...)
			m.commitMsg = string(runes)
			m.commitCursor--
		}
	case "delete":
		runes := []rune(m.commitMsg)
		if m.commitCursor < len(runes) {
			runes = append(runes[:m.commitCursor], runes[m.commitCursor+1:]...)
			m.commitMsg = string(runes)
		}
	case "left":
		m.commitCursor = max(m.commitCursor-1, 0)
	case "right":
		m.commitCursor = min(m.commitCursor+1, len([]rune(m.commitMsg)))
	case "home", "ctrl+a":
		m.commitCursor = 0
	case "end", "ctrl+e":
		m.commitCursor = len([]rune(m.commitMsg))
	case " ":
		m.insertCommitRunes([]rune{' '})
	default:
		if km.Type == tea.KeyRunes {
			m.insertCommitRunes(km.Runes)
		}
	}
	m.viewDirty = true
	return nil
}

// handleDeleteConfirmKey processes keys when the delete confirmation is active.
// Tab and arrow keys toggle between (y)es and (n)o; enter confirms the
// highlighted choice; y/n act as direct shortcuts.
func (m *Model) handleDeleteConfirmKey(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "left", "right":
		m.deleteConfirmYes = !m.deleteConfirmYes
	case "shift+tab":
		m.collapseBranch()
	case "k", "up", "tab":
		m.deleteConfirmActive = false
		m.deleteConfirmYes = false
	case "y":
		return m.confirmDelete()
	case "enter":
		if m.deleteConfirmYes {
			return m.confirmDelete()
		}
		m.deleteConfirmActive = false
		m.deleteConfirmYes = false
	case " ":
		m.deleteConfirmActive = false
		m.deleteConfirmYes = false
	case "n", "esc":
		m.deleteConfirmActive = false
		m.deleteConfirmYes = false
	default:
		return nil
	}
	m.viewDirty = true
	return nil
}

// confirmDelete executes the branch deletion for the expanded card.
func (m *Model) confirmDelete() tea.Cmd {
	if m.expandedIdx >= 0 && m.expandedIdx < len(m.branches) {
		name := m.branches[m.expandedIdx].Name
		m.deleteConfirmActive = false
		m.deleteConfirmYes = false
		m.collapseBranch()
		m.viewDirty = true
		return func() tea.Msg { return BranchDeleteMsg{Name: name} }
	}
	m.deleteConfirmActive = false
	m.deleteConfirmYes = false
	m.viewDirty = true
	return nil
}

// clearCommitInput resets the commit input state and returns focus to the
// action badges.
func (m *Model) clearCommitInput() {
	m.commitInputActive = false
	m.commitPhase = commitIdle
	m.commitMsg = ""
	m.commitCursor = 0
	m.commitSpinner = 0
	m.viewDirty = true
}

// handleCommitDone processes the result of an async commit operation.
func (m *Model) handleCommitDone(done CommitDoneMsg) tea.Cmd {
	if !done.OK {
		m.commitPhase = commitFailed
		m.commitMsg = done.Message
		m.viewDirty = true
		return m.commitDismissCmd()
	}
	m.commitPhase = commitSucceeded
	m.commitMsg = done.Message
	m.viewDirty = true
	return m.commitDismissCmd()
}

// handleCommitDismiss clears the flash after the timer fires.
func (m *Model) handleCommitDismiss() {
	if m.commitPhase == commitSucceeded || m.commitPhase == commitFailed {
		m.clearCommitInput()
	}
}

// commitDismissCmd schedules auto-dismissal of the success flash.
func (m *Model) commitDismissCmd() tea.Cmd {
	return tea.Tick(commitFlashDuration, func(time.Time) tea.Msg {
		return commitDismissMsg{}
	})
}

// insertCommitRunes inserts runes at the current cursor position in the
// commit message.
func (m *Model) insertCommitRunes(inserted []rune) {
	runes := []rune(m.commitMsg)
	newRunes := make([]rune, 0, len(runes)+len(inserted))
	newRunes = append(newRunes, runes[:m.commitCursor]...)
	newRunes = append(newRunes, inserted...)
	newRunes = append(newRunes, runes[m.commitCursor:]...)
	m.commitMsg = string(newRunes)
	m.commitCursor += len(inserted)
}

// ---------------------------------------------------------------------------
// Create branch input
// ---------------------------------------------------------------------------

// activateCreateInput opens the inline branch name input on the toolbar row.
func (m *Model) activateCreateInput() {
	m.createInputActive = true
	m.createBranchName = ""
	m.createCursor = 0
	m.viewDirty = true
}

// clearCreateInput closes the create branch input and returns focus to toolbar.
func (m *Model) clearCreateInput() {
	m.createInputActive = false
	m.createBranchName = ""
	m.createCursor = 0
	m.viewDirty = true
}

// insertCreateRunes inserts runes at the current cursor position in the
// branch name input.
func (m *Model) insertCreateRunes(inserted []rune) {
	runes := []rune(m.createBranchName)
	newRunes := make([]rune, 0, len(runes)+len(inserted))
	newRunes = append(newRunes, runes[:m.createCursor]...)
	newRunes = append(newRunes, inserted...)
	newRunes = append(newRunes, runes[m.createCursor:]...)
	m.createBranchName = string(newRunes)
	m.createCursor += len(inserted)
}

// isToolbarButtonEnabled reports whether a toolbar button ID is currently
// usable. Pull, Merge, and Push are disabled when the working tree is dirty
// or the selected branch is a synthetic detached HEAD entry.
func (m *Model) isToolbarButtonEnabled(buttonID int) bool {
	detached := m.selectedBranchIsDetached()
	switch buttonID {
	case toolbarPull, toolbarMerge:
		return !detached && !m.workingDirty && !m.workingConflicts
	case toolbarPush:
		return !detached
	case toolbarReset, toolbarResetHard, toolbarResetMixed, toolbarResetSoft,
		toolbarRevert, toolbarRevertOk:
		return !m.workingConflicts
	case toolbarCherryPick:
		return !m.workingConflicts
	case toolbarCherryPickOk:
		return len(m.cherryPickSelections) > 0
	case toolbarConflictContinue:
		return m.AllConflictsResolved()
	case toolbarRebase:
		return !m.workingConflicts
	}
	return true
}

// disabledToolbarSet returns the set of toolbar-index positions that are
// disabled, given a slice of button IDs.
func (m *Model) disabledToolbarSet(buttons []int) map[int]bool {
	var s map[int]bool
	for i, id := range buttons {
		if !m.isToolbarButtonEnabled(id) {
			if s == nil {
				s = make(map[int]bool, len(buttons))
			}
			s[i] = true
		}
	}
	return s
}

// HighlightedBranch returns the name of the currently highlighted branch.
func (m *Model) HighlightedBranch() string {
	return m.selectedBranchName()
}

// selectedBranchName returns the name of the currently selected branch.
func (m *Model) selectedBranchName() string {
	if m.branchIdx >= 0 && m.branchIdx < len(m.branches) {
		return m.branches[m.branchIdx].Name
	}
	return ""
}

// selectedBranchIsDetached reports whether the currently selected branch
// is a synthetic detached HEAD entry.
func (m *Model) selectedBranchIsDetached() bool {
	if m.branchIdx >= 0 && m.branchIdx < len(m.branches) {
		return m.branches[m.branchIdx].IsDetached
	}
	return false
}

// handleCreateInputKey processes keys when the create branch input is active.
func (m *Model) handleCreateInputKey(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "enter":
		name := strings.TrimSpace(m.createBranchName)
		if name == "" {
			return nil
		}
		parent := m.selectedBranchName()
		var atHash string
		if m.selectedBranchIsDetached() {
			atHash = m.branches[m.branchIdx].Hash
		}
		m.clearCreateInput()
		return func() tea.Msg {
			return CreateBranchRequestMsg{Name: name, ParentBranch: parent, AtHash: atHash}
		}
	case "esc":
		m.clearCreateInput()
	case "backspace":
		if m.createCursor > 0 {
			runes := []rune(m.createBranchName)
			runes = append(runes[:m.createCursor-1], runes[m.createCursor:]...)
			m.createBranchName = string(runes)
			m.createCursor--
		}
	case "delete":
		runes := []rune(m.createBranchName)
		if m.createCursor < len(runes) {
			runes = append(runes[:m.createCursor], runes[m.createCursor+1:]...)
			m.createBranchName = string(runes)
		}
	case "left":
		m.createCursor = max(m.createCursor-1, 0)
	case "right":
		m.createCursor = min(m.createCursor+1, len([]rune(m.createBranchName)))
	case "home", "ctrl+a":
		m.createCursor = 0
	case "end", "ctrl+e":
		m.createCursor = len([]rune(m.createBranchName))
	case " ":
		m.insertCreateRunes([]rune{'-'})
	default:
		if km.Type == tea.KeyRunes {
			m.insertCreateRunes(km.Runes)
		}
	}
	m.viewDirty = true
	return nil
}

// toggleExpandedSubview opens or closes the sub-view (confirmation prompt,
// commit input) for the currently selected action badge. Actions without a
// sub-view (Switch) are no-ops.
func (m *Model) toggleExpandedSubview() tea.Cmd {
	actionID := m.expandedActionID()
	if !m.isActionEnabled(actionID) {
		return nil
	}
	if m.expandedIdx < 0 || m.expandedIdx >= len(m.branches) {
		return nil
	}
	b := m.branches[m.expandedIdx]

	switch actionID {
	case branchActionCommit:
		if !b.IsHead {
			return nil
		}
		if m.commitInputActive {
			m.clearCommitInput()
		} else {
			m.commitInputActive = true
			m.commitMsg = ""
			m.commitCursor = 0
		}
	case branchActionDelete:
		if b.IsHead || b.Name == m.defaultBranch {
			return nil
		}
		if m.deleteConfirmActive {
			m.deleteConfirmActive = false
			m.deleteConfirmYes = false
		} else {
			m.deleteConfirmActive = true
			m.deleteConfirmYes = false
		}
	}
	m.viewDirty = true
	return nil
}

// expandBranch toggles expansion on the currently selected branch card.
func (m *Model) expandBranch() {
	if m.expandedIdx == m.branchIdx {
		m.collapseBranch()
		return
	}
	m.expandedIdx = m.branchIdx
	m.clearCommitInput()
	m.expandedAction = 0
}

// collapseBranch closes any expanded card.
func (m *Model) collapseBranch() {
	m.expandedIdx = -1
	m.expandedAction = 0
	m.toolbarFocused = false
	m.deleteConfirmActive = false
	m.deleteConfirmYes = false
	m.clearCommitInput()
}

// executeExpandedAction runs the selected action on the expanded branch.
func (m *Model) executeExpandedAction() tea.Cmd {
	actionID := m.expandedActionID()
	if actionID < 0 || !m.isActionEnabled(actionID) {
		return nil
	}
	if m.expandedIdx < 0 || m.expandedIdx >= len(m.branches) {
		return nil
	}

	b := m.branches[m.expandedIdx]
	name := b.Name

	switch actionID {
	case branchActionCommit:
		// Activate commit input; don't collapse.
		m.commitInputActive = true
		m.commitMsg = ""
		m.commitCursor = 0
		m.viewDirty = true
		return nil
	case branchActionSwitch:
		if b.IsHead || m.workingDirty || m.workingConflicts {
			return nil
		}
	case branchActionDelete:
		if b.IsHead || name == m.defaultBranch {
			return nil
		}
		// Show confirmation prompt instead of deleting immediately.
		m.deleteConfirmActive = true
		m.deleteConfirmYes = false // default to (n)o for safety
		m.viewDirty = true
		return nil
	}

	m.collapseBranch()
	m.viewDirty = true

	switch actionID {
	case branchActionSwitch:
		return func() tea.Msg { return BranchSwitchMsg{Name: name} }
	}
	return nil
}

// executeToolbarAction emits the message for the currently selected toolbar
// button. toolbarAction uses the unified index space: left buttons first,
// then right buttons.
func (m *Model) executeToolbarAction() tea.Cmd {
	left := m.diffToolbarLeftButtons()
	right := m.toolbarButtons()
	all := slices.Concat(left, right)
	if m.toolbarAction < 0 || m.toolbarAction >= len(all) {
		return nil
	}
	if !m.isToolbarButtonEnabled(all[m.toolbarAction]) {
		return nil
	}
	switch all[m.toolbarAction] {
	case toolbarCreate:
		if m.diffMode {
			m.exitBranchDiffMode()
		}
		if m.mergeMode {
			m.exitMergeMode()
		}
		m.activateCreateInput()
		return nil
	case toolbarMerge:
		if m.diffMode {
			m.exitBranchDiffMode()
		}
		if m.mergeMode {
			m.exitMergeMode()
		} else {
			m.enterMergeMode()
		}
		return nil
	case toolbarBack:
		if m.diffMode {
			m.exitDiffMode()
		}
		if m.resetMode {
			m.exitResetMode()
		}
		if m.revertMode {
			m.exitRevertMode()
		}
		m.ExitToBranches()
		return nil
	case toolbarReset:
		if m.diffMode {
			m.exitDiffMode()
		}
		if m.resetMode {
			m.exitResetMode()
		} else {
			m.enterResetMode()
		}
		return nil
	case toolbarResetHard:
		hash := m.SelectedHash()
		m.exitResetMode()
		if hash == "" {
			return nil
		}
		return func() tea.Msg { return ResetRequestMsg{Hash: hash, Mode: "hard"} }
	case toolbarResetMixed:
		hash := m.SelectedHash()
		m.exitResetMode()
		if hash == "" {
			return nil
		}
		return func() tea.Msg { return ResetRequestMsg{Hash: hash, Mode: "mixed"} }
	case toolbarResetSoft:
		hash := m.SelectedHash()
		m.exitResetMode()
		if hash == "" {
			return nil
		}
		return func() tea.Msg { return ResetRequestMsg{Hash: hash, Mode: "soft"} }
	case toolbarResetAbort:
		m.exitResetMode()
		return nil
	case toolbarRevert:
		if m.resetMode {
			m.exitResetMode()
		}
		if m.revertMode {
			m.exitRevertMode()
		} else {
			m.enterRevertMode()
		}
		return nil
	case toolbarRevertOk:
		hash := m.SelectedHash()
		m.exitRevertMode()
		if hash == "" {
			return nil
		}
		return func() tea.Msg { return RevertRequestMsg{Hash: hash} }
	case toolbarRevertAbort:
		m.exitRevertMode()
		return nil
	case toolbarDiff:
		if m.mergeMode {
			m.exitMergeMode()
		}
		if m.mode == viewBranches {
			if m.diffMode {
				m.exitBranchDiffMode()
			} else {
				m.enterBranchDiffMode()
			}
		} else {
			if m.diffMode {
				m.exitDiffMode()
			} else {
				m.enterDiffMode()
			}
		}
		return nil
	case toolbarDiffOk:
		if m.mode == viewBranches {
			hashes := m.BranchDiffHashes()
			names := m.BranchDiffSelections()
			m.exitBranchDiffMode()
			return func() tea.Msg { return BranchDiffCompareMsg{Hashes: hashes, Names: names} }
		}
		hashes := m.DiffSelections()
		m.exitDiffMode()
		return func() tea.Msg { return DiffCompareMsg{Hashes: hashes} }
	case toolbarDiffCancel:
		if m.mode == viewBranches {
			m.exitBranchDiffMode()
		} else {
			m.exitDiffMode()
		}
		return nil
	case toolbarMergeOk:
		hashes := m.MergeHashes()
		names := m.MergeNames()
		m.exitMergeMode()
		return func() tea.Msg { return MergeDiffCompareMsg{Hashes: hashes, Names: names} }
	case toolbarMergeAbort:
		m.exitMergeMode()
		return nil
	case toolbarPull:
		name := m.selectedBranchName()
		if name == "" {
			return nil
		}
		return func() tea.Msg { return PullBranchMsg{Name: name} }
	case toolbarPush:
		name := m.selectedBranchName()
		if name == "" {
			return nil
		}
		return func() tea.Msg { return PushBranchMsg{Name: name} }

	case toolbarCherryPick:
		if m.cherryPickMode {
			m.exitCherryPickMode()
		} else {
			m.enterCherryPickMode()
		}
		return nil
	case toolbarCherryPickOk:
		if m.branchPickerActive {
			// Confirm currently highlighted branch.
			items := m.filteredBranchItems()
			if len(items) == 0 {
				return nil
			}
			selected := items[m.branchPickerCursor]
			m.closeBranchPicker()
			hashes := m.CherryPickSelections()
			m.exitCherryPickMode()
			return func() tea.Msg {
				return CherryPickRequestMsg{Hashes: hashes, TargetBranch: selected}
			}
		}
		// Open branch picker.
		m.openBranchPicker()
		return nil
	case toolbarCherryPickAbort:
		m.exitCherryPickMode()
		return nil

	case toolbarRebase:
		// Open branch picker for rebase onto target.
		m.openBranchPicker()
		return nil

	case toolbarConflictContinue:
		return func() tea.Msg { return SequencerContinueMsg{} }
	case toolbarConflictBypass:
		return func() tea.Msg { return SequencerBypassMsg{} }
	case toolbarConflictAbort:
		return func() tea.Msg { return SequencerAbortMsg{} }

	case toolbarRebasePlanOk:
		plan := make([]RebasePlanItem, len(m.rebasePlan))
		for i, e := range m.rebasePlan {
			plan[i] = RebasePlanItem{Action: e.action, Hash: e.hash, Subject: e.subject}
		}
		onto := m.rebaseOnto
		m.ExitRebasePlan()
		return func() tea.Msg { return RebaseStartMsg{OntoBranch: onto, Plan: plan} }
	case toolbarRebasePlanAbort:
		m.ExitRebasePlan()
		return nil
	}
	return nil
}

// moveBranchDown moves to the same column in the next row.
func (m *Model) moveBranchDown() {
	oc := m.offshootCount()
	if m.branchIdx >= oc {
		return // already on primary
	}
	row := m.offRowOf[m.branchIdx]
	col := m.offColOf[m.branchIdx]
	if row+1 >= len(m.offRows) {
		m.branchIdx = oc // move to primary
		return
	}
	nextRow := m.offRows[row+1]
	m.branchIdx = nextRow[min(col, len(nextRow)-1)]
}

// moveBranchUp moves to the same column in the previous row.
func (m *Model) moveBranchUp() {
	oc := m.offshootCount()
	if m.branchIdx == oc && len(m.offRows) > 0 {
		// On primary, move to first item of last offshoot row.
		lastRow := m.offRows[len(m.offRows)-1]
		m.branchIdx = lastRow[0]
		return
	}
	if m.branchIdx >= len(m.offRowOf) {
		return
	}
	row := m.offRowOf[m.branchIdx]
	if row == 0 {
		return // already on first row
	}
	col := m.offColOf[m.branchIdx]
	prevRow := m.offRows[row-1]
	m.branchIdx = prevRow[min(col, len(prevRow)-1)]
}

// moveBranchRight moves to the next card in the same row.
func (m *Model) moveBranchRight() {
	oc := m.offshootCount()
	if m.branchIdx >= oc || m.branchIdx >= len(m.offColOf) {
		return // primary is a single card
	}
	row := m.offRows[m.offRowOf[m.branchIdx]]
	col := m.offColOf[m.branchIdx]
	if col+1 < len(row) {
		m.branchIdx = row[col+1]
	}
}

// moveBranchLeft moves to the previous card in the same row.
func (m *Model) moveBranchLeft() {
	oc := m.offshootCount()
	if m.branchIdx >= oc || m.branchIdx >= len(m.offColOf) {
		return // primary is a single card
	}
	col := m.offColOf[m.branchIdx]
	if col > 0 {
		row := m.offRows[m.offRowOf[m.branchIdx]]
		m.branchIdx = row[col-1]
	}
}

// handleCommitKey processes keys in commit cards view.
// Tab/shift+tab cycle through toolbar buttons; enter executes when focused.
func (m *Model) handleCommitKey(km tea.KeyMsg) tea.Cmd {
	// Esc exits the active mode before returning to branches.
	if km.String() == "esc" {
		if m.cherryPickMode {
			m.exitCherryPickMode()
			return nil
		}
		if m.revertMode {
			m.exitRevertMode()
			return nil
		}
		if m.resetMode {
			m.exitResetMode()
			return nil
		}
		if m.diffMode {
			m.exitDiffMode()
			return nil
		}
		m.ExitToBranches()
		return nil
	}

	if len(m.nodes) == 0 {
		return nil
	}

	// In cherry-pick mode, Enter/Space toggle selection.
	if m.cherryPickMode && !m.toolbarFocused {
		switch km.String() {
		case "enter", " ":
			m.toggleCherryPickSelection()
			return nil
		}
	}

	// In diff mode, Enter/Space toggle the current commit's selection.
	if m.diffMode && !m.toolbarFocused {
		switch km.String() {
		case "enter", " ":
			m.toggleDiffSelection()
			return nil
		}
	}

	switch km.String() {
	case "tab":
		m.cycleCommitToolbar(1)
		m.viewDirty = true
		return nil
	case "shift+tab":
		m.cycleCommitToolbar(-1)
		m.viewDirty = true
		return nil
	case "enter", " ":
		if m.toolbarFocused {
			m.viewDirty = true
			return m.executeToolbarAction()
		}
		// Non-toolbar Enter: checkout the selected commit.
		hash := m.SelectedHash()
		if hash != "" {
			return func() tea.Msg { return CommitCheckoutRequestMsg{Hash: hash} }
		}
	}

	m.toolbarFocused = false

	if m.dagMode && len(m.dagSections) > 0 {
		return m.handleCommitDAGKey(km)
	}

	prev := m.selectedIdx

	switch km.String() {
	case "j", "down", "shift+down":
		m.moveDown(1)
	case "k", "up", "shift+up":
		m.moveUp(1)
	case "g":
		m.selectedIdx = 0
	case "G":
		m.selectedIdx = len(m.nodes) - 1
	case "ctrl+d":
		m.moveDown(m.halfPage())
	case "ctrl+u":
		m.moveUp(m.halfPage())
	default:
		return nil
	}

	m.ensureVisible()
	m.viewDirty = true

	var cmds []tea.Cmd
	if m.selectedIdx != prev {
		cmds = append(cmds, m.selectionCmd())
	}
	if m.needsLoadMore() {
		m.loadingMore = true
		m.loadingEpoch = time.Now()
		m.loadingBarUntil = time.Now().Add(minLoadingBarDuration)
		m.viewDirty = true
		cmds = append(cmds, func() tea.Msg { return LoadMoreMsg{} })
	}
	return tea.Batch(cmds...)
}

// handleCommitDAGKey processes keys when in DAG trunk-and-offshoots mode.
func (m *Model) handleCommitDAGKey(km tea.KeyMsg) tea.Cmd {
	prev := m.selectedIdx

	switch km.String() {
	case "j", "down", "shift+down":
		m.moveCommitDAGDown()
	case "k", "up", "shift+up":
		m.moveCommitDAGUp()
	case "l", "right":
		m.moveCommitDAGRight()
	case "h", "left":
		m.moveCommitDAGLeft()
	case "g":
		m.dagSelectedRow = 0
		m.dagSelectedCol = -1
		m.selectedIdx = m.dagNodeIdxAt(m.dagSelectedRow, m.dagSelectedCol)
		m.ensureDAGVisible()
	case "G":
		m.dagSelectedRow = max(m.dagTotalRows()-1, 0)
		m.dagSelectedCol = -1
		m.selectedIdx = m.dagNodeIdxAt(m.dagSelectedRow, m.dagSelectedCol)
		m.ensureDAGVisible()
	case "ctrl+d":
		visRows := max(m.contentHeight()/branchRowHeight/2, 1)
		for range visRows {
			m.moveCommitDAGDown()
		}
	case "ctrl+u":
		visRows := max(m.contentHeight()/branchRowHeight/2, 1)
		for range visRows {
			m.moveCommitDAGUp()
		}
	default:
		return nil
	}

	m.viewDirty = true
	if m.selectedIdx != prev {
		return m.selectionCmd()
	}
	return nil
}

// cycleCommitToolbar advances toolbar focus by delta (+1 or -1).
// Exits toolbar focus when tabbing past the last or before the first button.
// When left action buttons (Ok/Abort) are present, Tab cycles only through
// those buttons. Otherwise, cycles through the right-side buttons.
func (m *Model) cycleCommitToolbar(delta int) {
	left := m.diffToolbarLeftButtons()
	var all []int
	if len(left) > 0 {
		all = left
	} else {
		all = m.toolbarButtons()
	}
	if len(all) == 0 {
		return
	}
	if !m.toolbarFocused {
		m.toolbarFocused = true
		var start int
		if delta > 0 {
			start = m.firstEnabledToolbar(all)
		} else {
			start = m.lastEnabledToolbar(all)
		}
		if start < 0 {
			m.toolbarFocused = false
			return
		}
		m.toolbarAction = start
		return
	}
	next := m.nextEnabledToolbar(m.toolbarAction, delta, all)
	if next < 0 {
		m.toolbarFocused = false
		return
	}
	m.toolbarAction = next
}

// enterBranch transitions to the loading view and emits BranchSelectedMsg
// so the app can start fetching commits + stats.
func (m *Model) enterBranch() tea.Cmd {
	branch := m.branches[m.branchIdx]
	m.activeBranch = branch.Name
	m.nodes = nil
	m.selectedIdx = 0
	m.scrollOff = 0
	m.loadingEpoch = time.Now()
	m.hasMore = false
	m.loadingMore = false
	m.lastHash = ""
	m.toolbarFocused = false
	m.mode = viewLoading
	m.viewDirty = true
	name := branch.Name
	return func() tea.Msg {
		return BranchSelectedMsg{Name: name}
	}
}

// ExitToBranches transitions back to the branch tree view.
func (m *Model) ExitToBranches() {
	m.mode = viewBranches
	m.activeBranch = ""
	m.nodes = nil
	m.hasMore = false
	m.loadingMore = false
	m.lastHash = ""
	m.graphRows = nil
	m.maxGraphLane = 0
	m.dagMode = false
	m.dagSections = m.dagSections[:0]
	m.dagSelectedRow = 0
	m.dagSelectedCol = -1
	m.toolbarFocused = false
	m.diffMode = false
	m.diffSelections = m.diffSelections[:0]
	m.resetMode = false
	m.revertMode = false
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// Diff selection mode
// ---------------------------------------------------------------------------

// InDiffMode reports whether the panel is in multi-commit diff selection mode.
func (m *Model) InDiffMode() bool {
	return m.diffMode
}

// DiffSelections returns the hashes of all currently diff-selected commits.
func (m *Model) DiffSelections() []string {
	hashes := make([]string, len(m.diffSelections))
	for i, ds := range m.diffSelections {
		hashes[i] = ds.hash
	}
	return hashes
}

// diffOverlayFor returns the diff overlay state for a commit hash.
// Returns an inactive overlay if the hash is not in the selection list.
func (m *Model) diffOverlayFor(hash string) diffOverlay {
	if !m.diffMode {
		return diffOverlay{}
	}
	for i, ds := range m.diffSelections {
		if ds.hash == hash {
			return diffOverlay{active: true, idx: i}
		}
	}
	return diffOverlay{}
}

// enterDiffMode activates diff selection mode, adding the current commit
// as selection #1. Unfocuses the toolbar so navigation keys work normally.
func (m *Model) enterDiffMode() {
	m.diffMode = true
	m.diffSelections = m.diffSelections[:0]
	if hash := m.SelectedHash(); hash != "" {
		m.diffSelections = append(m.diffSelections, diffSelection{
			hash:    hash,
			nodeIdx: m.selectedIdx,
		})
	}
	m.toolbarFocused = false
	m.invalidateNodeCache()
	m.invalidateCommitCardCache()
	m.viewDirty = true
}

// exitDiffMode deactivates diff selection mode and clears all selections.
func (m *Model) exitDiffMode() {
	if len(m.diffSelections) > 0 {
		m.selectedIdx = m.diffSelections[0].nodeIdx
		m.restoreDAGSelection()
	}
	m.diffMode = false
	m.diffSelections = m.diffSelections[:0]
	m.toolbarFocused = false
	m.invalidateNodeCache()
	m.invalidateCommitCardCache()
	m.ensureVisible()
	m.viewDirty = true
}

// enterResetMode activates reset mode, showing reset sub-buttons.
func (m *Model) enterResetMode() {
	m.resetMode = true
	m.toolbarFocused = false
	m.viewDirty = true
}

// exitResetMode deactivates reset mode.
func (m *Model) exitResetMode() {
	m.resetMode = false
	m.toolbarFocused = false
	m.viewDirty = true
}

// enterRevertMode activates revert confirmation mode.
func (m *Model) enterRevertMode() {
	m.revertMode = true
	m.toolbarFocused = false
	m.viewDirty = true
}

// exitRevertMode deactivates revert confirmation mode.
func (m *Model) exitRevertMode() {
	m.revertMode = false
	m.toolbarFocused = false
	m.viewDirty = true
}

// enterCherryPickMode activates cherry-pick selection mode, adding the
// current commit as the first selection.
func (m *Model) enterCherryPickMode() {
	m.cherryPickMode = true
	m.cherryPickSelections = m.cherryPickSelections[:0]
	if hash := m.SelectedHash(); hash != "" {
		m.cherryPickSelections = append(m.cherryPickSelections, diffSelection{
			hash:    hash,
			nodeIdx: m.selectedIdx,
		})
	}
	m.toolbarFocused = false
	m.invalidateNodeCache()
	m.invalidateCommitCardCache()
	m.viewDirty = true
}

// exitCherryPickMode deactivates cherry-pick mode and clears state.
func (m *Model) exitCherryPickMode() {
	if len(m.cherryPickSelections) > 0 {
		m.selectedIdx = m.cherryPickSelections[0].nodeIdx
		m.restoreDAGSelection()
	}
	m.cherryPickMode = false
	m.cherryPickSelections = m.cherryPickSelections[:0]
	m.cherryPickTarget = ""
	if m.branchPickerActive {
		m.branchPickerActive = false
		m.mode = m.branchPickPrevMode
	}
	m.branchPickerFilter = ""
	m.branchPickerFilterActive = false
	m.toolbarFocused = false
	m.invalidateNodeCache()
	m.invalidateCommitCardCache()
	m.ensureVisible()
	m.viewDirty = true
}

// toggleCherryPickSelection adds or removes the current commit from cherry-pick.
func (m *Model) toggleCherryPickSelection() {
	hash := m.SelectedHash()
	if hash == "" {
		return
	}
	for i, ds := range m.cherryPickSelections {
		if ds.hash == hash {
			m.cherryPickSelections = append(m.cherryPickSelections[:i], m.cherryPickSelections[i+1:]...)
			if len(m.cherryPickSelections) == 0 {
				m.exitCherryPickMode()
			}
			m.invalidateNodeCache()
			m.invalidateCommitCardCache()
			m.viewDirty = true
			return
		}
	}
	if len(m.cherryPickSelections) >= len(diffSelectionColors) {
		return
	}
	m.cherryPickSelections = append(m.cherryPickSelections, diffSelection{
		hash:    hash,
		nodeIdx: m.selectedIdx,
	})
	m.invalidateNodeCache()
	m.invalidateCommitCardCache()
	m.viewDirty = true
}

// CherryPickSelections returns the hashes of cherry-pick-selected commits.
func (m *Model) CherryPickSelections() []string {
	hashes := make([]string, len(m.cherryPickSelections))
	for i, ds := range m.cherryPickSelections {
		hashes[i] = ds.hash
	}
	return hashes
}

// SetBranchPickerItems sets the available branches for the picker.
func (m *Model) SetBranchPickerItems(items []string) {
	m.branchPickerItems = items
}

// openBranchPicker opens the branch picker dropdown, populating
// available branches from the already-loaded branch list.
func (m *Model) openBranchPicker() {
	m.branchPickerItems = m.branchPickerItems[:0]
	for _, b := range m.branches {
		m.branchPickerItems = append(m.branchPickerItems, b.Name)
	}
	m.branchPickerActive = true
	m.branchPickerCursor = 0
	m.branchPickerScroll = 0
	m.branchPickerFilter = ""
	m.branchPickerFilterActive = false
	m.branchPickPrevMode = m.mode
	m.mode = viewBranchPick
	m.toolbarFocused = false
	m.viewDirty = true
}

// closeBranchPicker closes the branch picker and restores the previous view.
func (m *Model) closeBranchPicker() {
	m.branchPickerActive = false
	m.branchPickerFilter = ""
	m.branchPickerFilterActive = false
	m.mode = m.branchPickPrevMode
	m.viewDirty = true
}

// filteredBranchItems returns branches matching the current filter.
func (m *Model) filteredBranchItems() []string {
	if m.branchPickerFilter == "" {
		return m.branchPickerItems
	}
	filter := strings.ToLower(m.branchPickerFilter)
	var filtered []string
	for _, name := range m.branchPickerItems {
		if strings.Contains(strings.ToLower(name), filter) {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

// EnterConflictMode enters conflict resolution mode with the given state.
func (m *Model) EnterConflictMode(op, total, current int, subject string, files []string) {
	m.conflictMode = true
	m.conflictOp = op
	m.conflictTotal = total
	m.conflictCurrent = current
	m.conflictSubject = subject
	m.conflictFiles = files
	m.conflictResolved = make(map[string]bool, len(files))
	m.toolbarFocused = false
	m.viewDirty = true
}

// ExitConflictMode leaves conflict resolution mode.
func (m *Model) ExitConflictMode() {
	m.conflictMode = false
	m.conflictFiles = nil
	m.conflictResolved = nil
	m.toolbarFocused = false
	m.viewDirty = true
}

// MarkConflictResolved marks a file as resolved in conflict mode.
func (m *Model) MarkConflictResolved(path string) {
	if m.conflictResolved != nil {
		m.conflictResolved[path] = true
		m.viewDirty = true
	}
}

// AllConflictsResolved reports whether all conflict files are resolved.
func (m *Model) AllConflictsResolved() bool {
	for _, f := range m.conflictFiles {
		if !m.conflictResolved[f] {
			return false
		}
	}
	return true
}

// InConflictMode reports whether the model is in conflict resolution mode.
func (m *Model) InConflictMode() bool {
	return m.conflictMode
}

// EnterRebasePlan sets up the interactive rebase plan editor.
func (m *Model) EnterRebasePlan(onto string, entries []rebasePlanEntry) {
	m.mode = viewRebasePlan
	m.rebaseOnto = onto
	m.rebasePlan = entries
	m.rebaseCursor = 0
	m.rebaseSelAnchor = -1
	m.toolbarFocused = false
	m.viewDirty = true
}

// ExitRebasePlan returns to the commit view.
func (m *Model) ExitRebasePlan() {
	m.mode = viewCommits
	m.rebasePlan = nil
	m.rebaseCursor = 0
	m.rebaseSelAnchor = -1
	m.rebaseOnto = ""
	m.toolbarFocused = false
	m.viewDirty = true
}

// handleBranchPickerKey processes keys when the branch picker dropdown is open.
// Filter is activated with / or alt+f (matching other panels). Navigation uses
// j/k, Tab cycles the toolbar, Enter confirms the highlighted branch.
func (m *Model) handleBranchPickerKey(km tea.KeyMsg) tea.Cmd {
	// When the filter input is active, route typing there.
	if m.branchPickerFilterActive {
		return m.handlePickerFilterKey(km)
	}

	items := m.filteredBranchItems()
	ks := km.String()

	switch ks {
	case "tab":
		m.hoverButtonIdx = -1
		m.cycleCommitToolbar(1)
		m.viewDirty = true
		return nil
	case "shift+tab":
		m.hoverButtonIdx = -1
		m.cycleCommitToolbar(-1)
		m.viewDirty = true
		return nil
	case "esc":
		if m.toolbarFocused {
			m.toolbarFocused = false
			m.viewDirty = true
			return nil
		}
		if m.branchPickerFilter != "" {
			m.branchPickerFilter = ""
			m.branchPickerCursor = 0
			m.viewDirty = true
			return nil
		}
		m.closeBranchPicker()
		if m.cherryPickMode {
			m.exitCherryPickMode()
		}
		return nil
	case "enter", " ":
		if m.toolbarFocused {
			m.viewDirty = true
			return m.executeToolbarAction()
		}
		if len(items) == 0 {
			return nil
		}
		return m.confirmBranchPicker(items[m.branchPickerCursor])
	case "j", "down":
		m.toolbarFocused = false
		if m.branchPickerCursor < len(items)-1 {
			m.branchPickerCursor++
		}
		m.viewDirty = true
		return nil
	case "k", "up":
		m.toolbarFocused = false
		if m.branchPickerCursor > 0 {
			m.branchPickerCursor--
		}
		m.viewDirty = true
		return nil
	case "g":
		m.toolbarFocused = false
		m.branchPickerCursor = 0
		m.viewDirty = true
		return nil
	case "G":
		m.toolbarFocused = false
		if len(items) > 0 {
			m.branchPickerCursor = len(items) - 1
		}
		m.viewDirty = true
		return nil
	case "ctrl+d":
		m.toolbarFocused = false
		half := max(m.contentHeight()/2, 1)
		for range half {
			if m.branchPickerCursor < len(items)-1 {
				m.branchPickerCursor++
			}
		}
		m.viewDirty = true
		return nil
	case "ctrl+u":
		m.toolbarFocused = false
		half := max(m.contentHeight()/2, 1)
		for range half {
			if m.branchPickerCursor > 0 {
				m.branchPickerCursor--
			}
		}
		m.viewDirty = true
		return nil
	case "/", "alt+f":
		m.branchPickerFilterActive = true
		m.toolbarFocused = false
		m.viewDirty = true
		return nil
	}
	return nil
}

// handlePickerFilterKey processes keys when the branch picker filter input is active.
func (m *Model) handlePickerFilterKey(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "esc":
		m.branchPickerFilterActive = false
		m.viewDirty = true
		return nil
	case "enter":
		m.branchPickerFilterActive = false
		m.branchPickerCursor = 0
		m.viewDirty = true
		return nil
	case "backspace":
		if len(m.branchPickerFilter) > 0 {
			m.branchPickerFilter = m.branchPickerFilter[:len(m.branchPickerFilter)-1]
			m.branchPickerCursor = 0
			m.viewDirty = true
		}
		return nil
	default:
		if len(km.String()) == 1 && km.String()[0] >= ' ' {
			m.branchPickerFilter += km.String()
			m.branchPickerCursor = 0
			m.viewDirty = true
		}
		return nil
	}
}

// confirmBranchPicker confirms the selected branch and dispatches the
// appropriate action (cherry-pick or rebase plan).
func (m *Model) confirmBranchPicker(selected string) tea.Cmd {
	m.closeBranchPicker()

	if m.cherryPickMode {
		hashes := m.CherryPickSelections()
		m.exitCherryPickMode()
		return func() tea.Msg {
			return CherryPickRequestMsg{Hashes: hashes, TargetBranch: selected}
		}
	}

	// Rebase: generate default plan and enter plan view.
	plan := make([]rebasePlanEntry, len(m.nodes))
	for i, n := range m.nodes {
		plan[i] = rebasePlanEntry{
			action:  0, // Pick
			hash:    n.Hash[:min(len(n.Hash), 7)],
			subject: n.Subject,
			author:  n.Author,
		}
	}
	m.EnterRebasePlan(selected, plan)
	return nil
}

// rebaseSelRange returns the inclusive [lo, hi] range of selected entries.
// When no multi-select is active, both equal the cursor.
func (m *Model) rebaseSelRange() (int, int) {
	if m.rebaseSelAnchor < 0 {
		return m.rebaseCursor, m.rebaseCursor
	}
	lo, hi := m.rebaseSelAnchor, m.rebaseCursor
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// setRebaseAction sets the action for all entries in the current selection.
func (m *Model) setRebaseAction(action int) {
	lo, hi := m.rebaseSelRange()
	for i := lo; i <= hi; i++ {
		m.rebasePlan[i].action = action
	}
	m.viewDirty = true
}

// cycleRebaseAction cycles the action forward or backward for all selected entries.
func (m *Model) cycleRebaseAction(delta int) {
	lo, hi := m.rebaseSelRange()
	n := len(rebaseActionNames)
	for i := lo; i <= hi; i++ {
		m.rebasePlan[i].action = (m.rebasePlan[i].action + delta + n) % n
	}
	m.viewDirty = true
}

// handleRebasePlanKey processes keys in the interactive rebase plan editor.
func (m *Model) handleRebasePlanKey(km tea.KeyMsg) tea.Cmd {
	if len(m.rebasePlan) == 0 {
		return nil
	}

	switch km.String() {
	case "esc":
		if m.rebaseSelAnchor >= 0 {
			m.rebaseSelAnchor = -1
			m.viewDirty = true
			return nil
		}
		m.ExitRebasePlan()
		return nil
	case "shift+down":
		if m.rebaseSelAnchor < 0 {
			m.rebaseSelAnchor = m.rebaseCursor
		}
		if m.rebaseCursor < len(m.rebasePlan)-1 {
			m.rebaseCursor++
		}
		m.viewDirty = true
		return nil
	case "shift+up":
		if m.rebaseSelAnchor < 0 {
			m.rebaseSelAnchor = m.rebaseCursor
		}
		if m.rebaseCursor > 0 {
			m.rebaseCursor--
		}
		m.viewDirty = true
		return nil
	case "j", "down":
		m.rebaseSelAnchor = -1
		if m.rebaseCursor < len(m.rebasePlan)-1 {
			m.rebaseCursor++
		}
		m.viewDirty = true
		return nil
	case "k", "up":
		m.rebaseSelAnchor = -1
		if m.rebaseCursor > 0 {
			m.rebaseCursor--
		}
		m.viewDirty = true
		return nil
	case "J":
		m.rebaseSelAnchor = -1
		if m.rebaseCursor < len(m.rebasePlan)-1 {
			m.rebasePlan[m.rebaseCursor], m.rebasePlan[m.rebaseCursor+1] =
				m.rebasePlan[m.rebaseCursor+1], m.rebasePlan[m.rebaseCursor]
			m.rebaseCursor++
			m.viewDirty = true
		}
		return nil
	case "K":
		m.rebaseSelAnchor = -1
		if m.rebaseCursor > 0 {
			m.rebasePlan[m.rebaseCursor], m.rebasePlan[m.rebaseCursor-1] =
				m.rebasePlan[m.rebaseCursor-1], m.rebasePlan[m.rebaseCursor]
			m.rebaseCursor--
			m.viewDirty = true
		}
		return nil
	case "p":
		m.setRebaseAction(0)
		return nil
	case "r":
		m.setRebaseAction(1)
		return nil
	case "s":
		m.setRebaseAction(2)
		return nil
	case "f":
		m.setRebaseAction(3)
		return nil
	case "e":
		m.setRebaseAction(4)
		return nil
	case "d":
		m.setRebaseAction(5)
		return nil
	case "l", "right", "enter", " ":
		if m.toolbarFocused {
			m.viewDirty = true
			return m.executeToolbarAction()
		}
		m.cycleRebaseAction(1)
		return nil
	case "h", "left":
		m.cycleRebaseAction(-1)
		return nil
	case "tab":
		m.cycleCommitToolbar(1)
		m.viewDirty = true
		return nil
	case "shift+tab":
		m.cycleCommitToolbar(-1)
		m.viewDirty = true
		return nil
	}
	return nil
}

// clickRebasePlanView handles clicks on the rebase plan. Clicking an entry
// within the selection cycles actions for all selected; clicking outside
// selects the clicked entry and clears multi-select.
func (m *Model) clickRebasePlanView(viewY int) tea.Cmd {
	// Layout: header(1) + blank(1) + entries.
	idx := viewY - 2
	if idx < 0 || idx >= len(m.rebasePlan) {
		return nil
	}
	lo, hi := m.rebaseSelRange()
	if idx >= lo && idx <= hi {
		m.cycleRebaseAction(1)
	} else {
		m.rebaseSelAnchor = -1
		m.rebaseCursor = idx
		m.viewDirty = true
	}
	return nil
}

// cherryPickOverlayFor returns the overlay state for a commit in cherry-pick mode.
func (m *Model) cherryPickOverlayFor(hash string) diffOverlay {
	if !m.cherryPickMode {
		return diffOverlay{}
	}
	for i, ds := range m.cherryPickSelections {
		if ds.hash == hash {
			return diffOverlay{active: true, idx: i}
		}
	}
	return diffOverlay{}
}

// toggleDiffSelection adds or removes the currently selected commit from
// the diff selection list. New selections are bounded by the palette length.
func (m *Model) toggleDiffSelection() {
	hash := m.SelectedHash()
	if hash == "" {
		return
	}
	// Remove if already selected.
	for i, ds := range m.diffSelections {
		if ds.hash == hash {
			m.diffSelections = append(m.diffSelections[:i], m.diffSelections[i+1:]...)
			if len(m.diffSelections) == 0 {
				m.exitDiffMode()
				return
			}
			m.invalidateNodeCache()
			m.invalidateCommitCardCache()
			m.viewDirty = true
			return
		}
	}
	// Append if below capacity.
	if len(m.diffSelections) >= len(diffSelectionColors) {
		return
	}
	m.diffSelections = append(m.diffSelections, diffSelection{
		hash:    hash,
		nodeIdx: m.selectedIdx,
	})
	m.invalidateNodeCache()
	m.invalidateCommitCardCache()
	m.viewDirty = true
}

// diffOverlayForBranch returns the diff overlay state for a branch index.
// Unlike diffOverlayFor (which matches by hash), this matches by nodeIdx so
// sibling branches sharing a tip commit are treated independently.
func (m *Model) diffOverlayForBranch(branchIdx int) diffOverlay {
	if !m.diffMode {
		return diffOverlay{}
	}
	for i, ds := range m.diffSelections {
		if ds.nodeIdx == branchIdx {
			return diffOverlay{active: true, idx: i}
		}
	}
	return diffOverlay{}
}

// ---------------------------------------------------------------------------
// Branch diff selection mode
// ---------------------------------------------------------------------------

// enterBranchDiffMode activates diff selection mode for branches, adding the
// currently selected branch as selection #1.
func (m *Model) enterBranchDiffMode() {
	m.diffMode = true
	m.diffSelections = m.diffSelections[:0]
	if m.branchIdx >= 0 && m.branchIdx < len(m.branches) {
		b := m.branches[m.branchIdx]
		m.diffSelections = append(m.diffSelections, diffSelection{
			hash:    b.Hash,
			nodeIdx: m.branchIdx,
		})
	}
	m.toolbarFocused = false
	m.invalidateCardCache()
	m.viewDirty = true
}

// exitBranchDiffMode deactivates branch diff selection mode and restores the
// cursor to the first selected branch.
func (m *Model) exitBranchDiffMode() {
	if len(m.diffSelections) > 0 {
		m.branchIdx = m.diffSelections[0].nodeIdx
	}
	m.diffMode = false
	m.diffSelections = m.diffSelections[:0]
	m.toolbarFocused = false
	m.invalidateCardCache()
	m.ensureBranchVisible()
	m.viewDirty = true
}

// toggleBranchDiffSelection adds or removes the currently selected branch
// from the diff selection list. Matches by branch index (not hash) so that
// sibling branches sharing a tip commit are treated independently.
func (m *Model) toggleBranchDiffSelection() {
	if m.branchIdx < 0 || m.branchIdx >= len(m.branches) {
		return
	}
	// Remove if already selected (match by index, not hash).
	for i, ds := range m.diffSelections {
		if ds.nodeIdx == m.branchIdx {
			m.diffSelections = append(m.diffSelections[:i], m.diffSelections[i+1:]...)
			if len(m.diffSelections) == 0 {
				m.exitBranchDiffMode()
				return
			}
			m.invalidateCardCache()
			m.viewDirty = true
			return
		}
	}
	// Append if below capacity.
	if len(m.diffSelections) >= len(diffSelectionColors) {
		return
	}
	m.diffSelections = append(m.diffSelections, diffSelection{
		hash:    m.branches[m.branchIdx].Hash,
		nodeIdx: m.branchIdx,
	})
	m.invalidateCardCache()
	m.viewDirty = true
}

// BranchDiffSelections returns the branch names in selection order.
func (m *Model) BranchDiffSelections() []string {
	names := make([]string, len(m.diffSelections))
	for i, ds := range m.diffSelections {
		if ds.nodeIdx >= 0 && ds.nodeIdx < len(m.branches) {
			names[i] = m.branches[ds.nodeIdx].Name
		}
	}
	return names
}

// BranchDiffHashes returns the tip commit hashes in selection order.
func (m *Model) BranchDiffHashes() []string {
	hashes := make([]string, len(m.diffSelections))
	for i, ds := range m.diffSelections {
		hashes[i] = ds.hash
	}
	return hashes
}

// MergeHashes returns the merge selection tip commit hashes in order.
func (m *Model) MergeHashes() []string {
	hashes := make([]string, len(m.mergeSelections))
	for i, ds := range m.mergeSelections {
		hashes[i] = ds.hash
	}
	return hashes
}

// MergeNames returns the merge selection branch names in order.
func (m *Model) MergeNames() []string {
	names := make([]string, len(m.mergeSelections))
	for i, ds := range m.mergeSelections {
		if ds.nodeIdx >= 0 && ds.nodeIdx < len(m.branches) {
			names[i] = m.branches[ds.nodeIdx].Name
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// Merge selection mode
// ---------------------------------------------------------------------------

// maxMergeSelections is the maximum number of branches that can be selected
// in merge mode. Derived from: a merge operation always involves exactly 2
// branches (source and target).
const maxMergeSelections = 2

// InMergeMode reports whether the model is in merge selection mode.
func (m *Model) InMergeMode() bool {
	return m.mergeMode
}

// enterMergeMode activates merge selection mode, adding the currently selected
// branch as selection #1. Unfocuses the toolbar so navigation keys work.
func (m *Model) enterMergeMode() {
	m.mergeMode = true
	m.mergeSelections = m.mergeSelections[:0]
	if m.branchIdx >= 0 && m.branchIdx < len(m.branches) {
		m.mergeSelections = append(m.mergeSelections, diffSelection{
			hash:    m.branches[m.branchIdx].Hash,
			nodeIdx: m.branchIdx,
		})
	}
	m.toolbarFocused = false
	m.invalidateCardCache()
	m.viewDirty = true
}

// exitMergeMode deactivates merge selection mode and restores the cursor to
// the first selected branch.
func (m *Model) exitMergeMode() {
	if len(m.mergeSelections) > 0 {
		m.branchIdx = m.mergeSelections[0].nodeIdx
	}
	m.mergeMode = false
	m.mergeSelections = m.mergeSelections[:0]
	m.toolbarFocused = false
	m.invalidateCardCache()
	m.ensureBranchVisible()
	m.viewDirty = true
}

// toggleMergeSelection adds or removes the currently selected branch from
// the merge selection list. Capped at maxMergeSelections.
func (m *Model) toggleMergeSelection() {
	if m.branchIdx < 0 || m.branchIdx >= len(m.branches) {
		return
	}
	for i, ds := range m.mergeSelections {
		if ds.nodeIdx == m.branchIdx {
			m.mergeSelections = append(m.mergeSelections[:i], m.mergeSelections[i+1:]...)
			if len(m.mergeSelections) == 0 {
				m.exitMergeMode()
				return
			}
			m.invalidateCardCache()
			m.viewDirty = true
			return
		}
	}
	if len(m.mergeSelections) >= maxMergeSelections {
		return
	}
	m.mergeSelections = append(m.mergeSelections, diffSelection{
		hash:    m.branches[m.branchIdx].Hash,
		nodeIdx: m.branchIdx,
	})
	m.invalidateCardCache()
	m.viewDirty = true
}

// mergeOverlayForBranch returns the overlay state for a branch in merge mode.
func (m *Model) mergeOverlayForBranch(branchIdx int) diffOverlay {
	if !m.mergeMode {
		return diffOverlay{}
	}
	for i, ds := range m.mergeSelections {
		if ds.nodeIdx == branchIdx {
			return diffOverlay{active: true, idx: i}
		}
	}
	return diffOverlay{}
}

// branchOverlay returns the active overlay for a branch card, dispatching to
// diff or merge mode as appropriate.
func (m *Model) branchOverlay(branchIdx int) diffOverlay {
	if m.diffMode {
		return m.diffOverlayForBranch(branchIdx)
	}
	if m.mergeMode {
		return m.mergeOverlayForBranch(branchIdx)
	}
	return diffOverlay{}
}

// ---------------------------------------------------------------------------
// Selection restoration
// ---------------------------------------------------------------------------

// restoreSelectionByHash finds a commit hash in the current node list and
// sets selectedIdx to its position. Falls back to 0 if not found.
func (m *Model) restoreSelectionByHash(hash string) {
	if hash == "" {
		return
	}
	for i, n := range m.nodes {
		if n.Hash == hash {
			m.selectedIdx = i
			return
		}
	}
}

// restoreDAGSelection reverse-maps selectedIdx to dagSelectedRow/dagSelectedCol.
// Must be called after recomputeCommitLayout and restoreSelectionByHash.
func (m *Model) restoreDAGSelection() {
	if len(m.dagSections) == 0 {
		return
	}
	target := m.selectedIdx
	row := 0
	for _, sec := range m.dagSections {
		for _, offRow := range sec.offRows {
			for col, nodeIdx := range offRow {
				if nodeIdx == target {
					m.dagSelectedRow = row
					m.dagSelectedCol = col
					return
				}
			}
			row++
		}
		if sec.trunkIdx == target {
			m.dagSelectedRow = row
			m.dagSelectedCol = -1
			return
		}
		row++
	}
}

// ---------------------------------------------------------------------------
// Navigation helpers (commit view)
// ---------------------------------------------------------------------------

func (m *Model) moveDown(n int) {
	m.selectedIdx = min(m.selectedIdx+n, len(m.nodes)-1)
}

func (m *Model) moveUp(n int) {
	m.selectedIdx = max(m.selectedIdx-n, 0)
}

func (m *Model) halfPage() int {
	h := m.activeNodeHeight()
	return max(m.contentHeight()/h/2, 1)
}

// activeNodeHeight returns the row height per entry for the commit view.
func (m *Model) activeNodeHeight() int {
	return nodeHeight
}

func (m *Model) ensureVisible() {
	if m.selectedIdx < m.scrollOff {
		m.scrollOff = m.selectedIdx
		return
	}
	vis := m.visibleNodeCount()
	if m.selectedIdx >= m.scrollOff+vis {
		m.scrollOff = m.selectedIdx - vis + 1
	}
	m.clampScroll()
}

func (m *Model) clampScroll() {
	m.scrollOff = max(m.scrollOff, 0)
	m.scrollOff = min(m.scrollOff, m.commitMaxScroll())
}

func (m *Model) commitMaxScroll() int {
	return max(len(m.nodes)-m.visibleNodeCount(), 0)
}

// needsLoadMore returns true when the selection is near the bottom of the
// loaded node list and more pages are available.
func (m *Model) needsLoadMore() bool {
	return m.hasMore && !m.loadingMore && m.mode == viewCommits &&
		m.selectedIdx >= len(m.nodes)-m.visibleNodeCount()
}

// needsScrollLoadMore returns true when the scroll position is near the
// bottom and more pages are available. Used by ScrollDown for mouse-wheel
// triggered pagination.
func (m *Model) needsScrollLoadMore() bool {
	vis := m.visibleNodeCount()
	return m.hasMore && !m.loadingMore && m.mode == viewCommits &&
		m.scrollOff+vis >= len(m.nodes)-vis
}

// showLoadingBar reports whether the bottom loading bar should be visible.
// True while a page is in flight OR until the minimum display time elapses.
func (m *Model) showLoadingBar() bool {
	return m.loadingMore || time.Now().Before(m.loadingBarUntil)
}

// contentHeight returns the viewport height available for cards/branches,
// excluding the toolbar row.
func (m *Model) contentHeight() int {
	return max(m.height-toolbarHeight, 0)
}

// viewTopPad returns the vertical centering offset applied by padViewport.
// Click handlers must subtract this from viewY to map screen coordinates
// to content coordinates.
func (m *Model) viewTopPad() int {
	ch := m.contentHeight()
	var cl int
	switch m.mode {
	case viewCommits:
		if m.dagMode && len(m.dagSections) > 0 {
			cl = m.dagContentHeight()
		} else {
			cl = len(m.commitVisibleRange()) * nodeHeight
		}
	default:
		endRow := m.visibleEndRow()
		for rowIdx := m.branchScrollOff; rowIdx < endRow; rowIdx++ {
			cl += m.branchRowLines(rowIdx)
		}
	}
	if cl >= ch {
		return 0
	}
	return (ch - cl) / 2
}

// toolbarButtons returns the toolbar button IDs for the current view mode.
// In diff mode these are the right-side buttons only.
func (m *Model) toolbarButtons() []int {
	if m.conflictMode {
		return nil // Conflict mode uses left buttons only.
	}
	switch m.mode {
	case viewBranches:
		return []int{toolbarCreate, toolbarMerge, toolbarPull, toolbarPush, toolbarDiff}
	case viewCommits:
		return []int{toolbarBack, toolbarReset, toolbarRevert, toolbarCherryPick, toolbarRebase, toolbarDiff}
	case viewRebasePlan, viewBranchPick:
		return nil // These views use left buttons only.
	default:
		return nil
	}
}

// diffToolbarLeftButtons returns the left-side toolbar buttons.
// In reset mode, shows hard/mixed/soft/abort immediately.
// In merge mode, shows ok/abort when selections are complete.
// In diff mode, shows ok/cancel when 2+ commits are selected.
func (m *Model) diffToolbarLeftButtons() []int {
	if m.conflictMode {
		return []int{toolbarConflictContinue, toolbarConflictBypass, toolbarConflictAbort}
	}
	if m.mode == viewRebasePlan {
		return []int{toolbarRebasePlanOk, toolbarRebasePlanAbort}
	}
	if m.cherryPickMode {
		return []int{toolbarCherryPickOk, toolbarCherryPickAbort}
	}
	if m.resetMode {
		return []int{toolbarResetHard, toolbarResetMixed, toolbarResetSoft, toolbarResetAbort}
	}
	if m.revertMode {
		return []int{toolbarRevertOk, toolbarRevertAbort}
	}
	if m.mergeMode {
		if len(m.mergeSelections) >= maxMergeSelections {
			return []int{toolbarMergeOk, toolbarMergeAbort}
		}
		return nil
	}
	if !m.diffMode || len(m.diffSelections) < 2 {
		return nil
	}
	return []int{toolbarDiffOk, toolbarDiffCancel}
}

func (m *Model) visibleNodeCount() int {
	h := m.activeNodeHeight()
	return max(m.contentHeight()/h, 1)
}

func (m *Model) commitVisibleRange() []int {
	count := m.visibleNodeCount()
	endIdx := min(m.scrollOff+count, len(m.nodes))
	indices := make([]int, 0, endIdx-m.scrollOff)
	for i := m.scrollOff; i < endIdx; i++ {
		indices = append(indices, i)
	}
	return indices
}

func (m *Model) selectionCmd() tea.Cmd {
	hash := m.nodes[m.selectedIdx].Hash
	return func() tea.Msg {
		return SelectionMsg{Hash: hash}
	}
}

// groupChildrenAfterParent ensures each child branch appears above (lower
// index than) its parent. Only moves children that are currently below their
// parent; children already above are left in their time-sorted position.
// This avoids displacing children of the default branch (always last) from
// their natural sort order.
func groupChildrenAfterParent(branches []BranchNode) []BranchNode {
	parentOf := make(map[string]string, len(branches))
	for _, b := range branches {
		if b.Parent != "" {
			parentOf[b.Name] = b.Parent
		}
	}
	if len(parentOf) == 0 {
		return branches
	}

	out := slices.Clone(branches)
	// Bound iterations to len(out)² to guard against circular parents.
	limit := len(out) * len(out)
	for range limit {
		idx := make(map[string]int, len(out))
		for i, b := range out {
			idx[b.Name] = i
		}
		moved := false
		for ci, b := range out {
			pi, ok := idx[parentOf[b.Name]]
			if !ok || ci <= pi {
				continue
			}
			// Child is below parent — move child to parent's position.
			child := out[ci]
			out = slices.Delete(out, ci, ci+1)
			out = slices.Insert(out, pi, child)
			moved = true
			break
		}
		if !moved {
			break
		}
	}
	return out
}

// recomputeOffshootLayout builds a depth-aware row assignment for offshoots.
// Children (higher depth) occupy rows above their parents. Within each depth
// tier branches fill rows up to branchCols columns. O(n) compute, O(1)
// row/column lookup via offRowOf/offColOf.
func (m *Model) recomputeOffshootLayout() {
	oc := m.offshootCount()
	if oc == 0 {
		m.offRows = m.offRows[:0]
		m.offRowOf = m.offRowOf[:0]
		m.offColOf = m.offColOf[:0]
		m.offGridCol = m.offGridCol[:0]
		m.offDepth = m.offDepth[:0]
		m.offExtraConn = m.offExtraConn[:0]
		return
	}
	cols := branchCols(m.width)

	// 1. Compute child depth for each offshoot.
	//    depth 0 = parent is default/external/missing
	//    depth N+1 = parent is an offshoot with depth N
	offNames := make(map[string]int, oc)
	for i := range oc {
		offNames[m.branches[i].Name] = i
	}
	depths := make([]int, oc)
	memo := make(map[int]int, oc)
	var depthOf func(int) int
	depthOf = func(i int) int {
		if d, ok := memo[i]; ok {
			return d
		}
		memo[i] = 0 // cycle guard
		parent := m.branches[i].Parent
		if pi, ok := offNames[parent]; ok {
			d := depthOf(pi) + 1
			memo[i] = d
			depths[i] = d
			return d
		}
		return 0
	}
	maxDepth := 0
	for i := range oc {
		d := depthOf(i)
		depths[i] = d
		maxDepth = max(maxDepth, d)
	}

	// 2. Group by depth, sorted oldest-first within each tier so
	//    the leftmost card in a row is the oldest branch.
	groups := make([][]int, maxDepth+1)
	for i := range oc {
		groups[depths[i]] = append(groups[depths[i]], i)
	}
	for _, group := range groups {
		slices.SortStableFunc(group, func(a, b int) int {
			ta := m.branches[a].CreatedTime
			tb := m.branches[b].CreatedTime
			if ta.IsZero() {
				ta = m.branches[a].AuthorTime
			}
			if tb.IsZero() {
				tb = m.branches[b].AuthorTime
			}
			return ta.Compare(tb)
		})
	}

	// 3. Build rows: highest depth (children) first → topmost rows.
	//    Grid columns are sequential within each row, matching the
	//    sorted order (oldest left, newest right).
	gridCol := make([]int, oc)
	m.offRows = m.offRows[:0]
	for d := maxDepth; d >= 0; d-- {
		group := groups[d]
		for start := 0; start < len(group); start += cols {
			end := min(start+cols, len(group))
			rowGroup := group[start:end]
			m.offRows = append(m.offRows, rowGroup)
			for i, fi := range rowGroup {
				gridCol[fi] = i
			}
		}
	}

	// 5. Build reverse maps.
	if cap(m.offRowOf) >= oc {
		m.offRowOf = m.offRowOf[:oc]
	} else {
		m.offRowOf = make([]int, oc)
	}
	if cap(m.offColOf) >= oc {
		m.offColOf = m.offColOf[:oc]
	} else {
		m.offColOf = make([]int, oc)
	}
	if cap(m.offGridCol) >= oc {
		m.offGridCol = m.offGridCol[:oc]
	} else {
		m.offGridCol = make([]int, oc)
	}
	if cap(m.offDepth) >= oc {
		m.offDepth = m.offDepth[:oc]
	} else {
		m.offDepth = make([]int, oc)
	}
	for ri, row := range m.offRows {
		for ci, flatIdx := range row {
			m.offRowOf[flatIdx] = ri
			m.offColOf[flatIdx] = ci
			m.offGridCol[flatIdx] = gridCol[flatIdx]
			m.offDepth[flatIdx] = depths[flatIdx]
		}
	}

	// Precompute which rows have an extra off-center connector line.
	// composeOffshootRow adds an extra vertical connector for single-card
	// rows whose card center doesn't align with the merge target.
	m.offExtraConn = m.offExtraConn[:0]
	effCols := m.effectiveCols()
	cardWidth := offshootCardWidth(m.width, effCols)
	for _, row := range m.offRows {
		m.offExtraConn = append(m.offExtraConn,
			len(row) == 1 && m.offshootCardCenter(row[0], effCols, cardWidth) != m.offshootMergeTarget(row[0], effCols, cardWidth))
	}

	// Row count may have changed — clamp scroll so it doesn't point past
	// the last row (SetSize clamps before the deferred recompute runs).
	m.clampBranchScroll()
}

// offshootCardCenter returns the horizontal center column of an offshoot card.
func (m *Model) offshootCardCenter(flatIdx, cols, cardWidth int) int {
	totalGrid := cols*cardWidth + max(cols-1, 0)*branchCardGap
	leftMargin := trunkAlignedMargin(m.width, totalGrid)
	gc := m.offGridCol[flatIdx]
	return leftMargin + gc*(cardWidth+branchCardGap) + cardWidth/2
}

// offshootMergeTarget returns the column the merge line connects to for
// the given offshoot's row. Depth 0 merges to the trunk; deeper rows
// merge to their parent's card center.
func (m *Model) offshootMergeTarget(flatIdx, cols, cardWidth int) int {
	if m.offDepth[flatIdx] == 0 {
		return m.width / 2
	}
	parentName := m.branches[flatIdx].Parent
	for pi := range m.offshootCount() {
		if m.branches[pi].Name == parentName {
			return m.offshootCardCenter(pi, cols, cardWidth)
		}
	}
	return m.width / 2
}

// offRowConnectors returns the number of connector lines below card content
// in an offshoot row. Usually 2 (merge + trunk); single off-center cards
// get an extra vertical connector (3 total).
func (m *Model) offRowConnectors(rowIdx int) int {
	if rowIdx < len(m.offExtraConn) && m.offExtraConn[rowIdx] {
		return 3
	}
	return 2
}

// ---------------------------------------------------------------------------
// Commit DAG layout
// ---------------------------------------------------------------------------

// recomputeCommitLayout builds the trunk-and-offshoots section layout from
// the current DAG nodes. The first-parent chain forms the trunk; merge
// second-parents contribute offshoot commits grouped into grid rows above
// each merge point.
func (m *Model) recomputeCommitLayout() {
	if !m.dagMode || len(m.nodes) == 0 {
		m.dagSections = m.dagSections[:0]
		return
	}

	// Build hash→index map.
	hashIdx := make(map[string]int, len(m.nodes))
	for i, n := range m.nodes {
		hashIdx[n.Hash] = i
	}

	// Walk first-parent chain from the first node to build the trunk set.
	trunkSet := make(map[int]bool, len(m.nodes))
	trunkOrder := make([]int, 0, len(m.nodes))
	cur := 0
	for {
		trunkSet[cur] = true
		trunkOrder = append(trunkOrder, cur)
		n := m.nodes[cur]
		if len(n.Parents) == 0 {
			break
		}
		next, ok := hashIdx[n.Parents[0]]
		if !ok {
			break
		}
		cur = next
	}

	// For each merge on the trunk, BFS from second parents to collect
	// non-trunk offshoot indices.
	cols := branchCols(m.width)
	m.dagSections = m.dagSections[:0]
	for _, ti := range trunkOrder {
		n := m.nodes[ti]
		sec := dagSection{trunkIdx: ti}
		if n.IsMerge && len(n.Parents) > 1 {
			// BFS from second parents.
			visited := make(map[int]bool)
			queue := make([]int, 0, len(n.Parents)-1)
			for _, ph := range n.Parents[1:] {
				if pi, ok := hashIdx[ph]; ok && !trunkSet[pi] {
					queue = append(queue, pi)
					visited[pi] = true
				}
			}
			for len(queue) > 0 {
				ci := queue[0]
				queue = queue[1:]
				sec.offIdxs = append(sec.offIdxs, ci)
				for _, ph := range m.nodes[ci].Parents {
					if pi, ok := hashIdx[ph]; ok && !trunkSet[pi] && !visited[pi] {
						visited[pi] = true
						queue = append(queue, pi)
					}
				}
			}
			// Group offshoots into grid rows.
			for start := 0; start < len(sec.offIdxs); start += cols {
				end := min(start+cols, len(sec.offIdxs))
				sec.offRows = append(sec.offRows, sec.offIdxs[start:end])
			}
		}
		m.dagSections = append(m.dagSections, sec)
	}
}

// dagTotalRows returns the total number of visual rows across all sections.
// Each section contributes len(offRows) offshoot rows + 1 trunk row.
func (m *Model) dagTotalRows() int {
	total := 0
	for _, sec := range m.dagSections {
		total += len(sec.offRows) + 1
	}
	return total
}

// dagContentHeight returns the total rendered height in terminal lines.
// Offshoot rows are branchRowHeight (6); trunk rows are nodeHeight (5).
func (m *Model) dagContentHeight() int {
	h := 0
	for _, sec := range m.dagSections {
		h += len(sec.offRows) * branchRowHeight
		h += nodeHeight
	}
	return h
}

// dagRowAt maps a linear visual row index to (sectionIdx, localRow).
// localRow < len(sec.offRows) means offshoot row; localRow == len(sec.offRows)
// means the trunk card. Returns (-1, -1) if out of range.
func (m *Model) dagRowAt(rowIdx int) (secIdx, localRow int) {
	pos := 0
	for si, sec := range m.dagSections {
		sectionRows := len(sec.offRows) + 1
		if rowIdx < pos+sectionRows {
			return si, rowIdx - pos
		}
		pos += sectionRows
	}
	return -1, -1
}

// dagNodeIdxAt returns the node index for a given visual row and column.
// col == -1 is the trunk; col >= 0 indexes into the offshoot row.
func (m *Model) dagNodeIdxAt(row, col int) int {
	si, lr := m.dagRowAt(row)
	if si < 0 {
		return 0
	}
	sec := m.dagSections[si]
	if lr >= len(sec.offRows) || col < 0 {
		return sec.trunkIdx
	}
	offRow := sec.offRows[lr]
	if len(offRow) == 0 {
		return sec.trunkIdx
	}
	return offRow[clampInt(col, 0, len(offRow)-1)]
}

// moveCommitDAGDown moves selection down one visual row.
func (m *Model) moveCommitDAGDown() {
	total := m.dagTotalRows()
	if m.dagSelectedRow+1 < total {
		m.dagSelectedRow++
		// If entering a trunk row, set col to -1; if entering offshoot, clamp col.
		si, lr := m.dagRowAt(m.dagSelectedRow)
		if si >= 0 {
			sec := m.dagSections[si]
			if lr >= len(sec.offRows) {
				m.dagSelectedCol = -1
			} else {
				offRow := sec.offRows[lr]
				m.dagSelectedCol = clampInt(max(m.dagSelectedCol, 0), 0, len(offRow)-1)
			}
		}
	}
	m.selectedIdx = m.dagNodeIdxAt(m.dagSelectedRow, m.dagSelectedCol)
	m.ensureDAGVisible()
}

// moveCommitDAGUp moves selection up one visual row.
func (m *Model) moveCommitDAGUp() {
	if m.dagSelectedRow > 0 {
		m.dagSelectedRow--
		si, lr := m.dagRowAt(m.dagSelectedRow)
		if si >= 0 {
			sec := m.dagSections[si]
			if lr >= len(sec.offRows) {
				m.dagSelectedCol = -1
			} else {
				offRow := sec.offRows[lr]
				m.dagSelectedCol = clampInt(max(m.dagSelectedCol, 0), 0, len(offRow)-1)
			}
		}
	}
	m.selectedIdx = m.dagNodeIdxAt(m.dagSelectedRow, m.dagSelectedCol)
	m.ensureDAGVisible()
}

// moveCommitDAGRight moves selection right within an offshoot row.
func (m *Model) moveCommitDAGRight() {
	si, lr := m.dagRowAt(m.dagSelectedRow)
	if si < 0 {
		return
	}
	sec := m.dagSections[si]
	if lr >= len(sec.offRows) {
		return // trunk row, single card
	}
	offRow := sec.offRows[lr]
	if m.dagSelectedCol+1 < len(offRow) {
		m.dagSelectedCol++
	}
	m.selectedIdx = m.dagNodeIdxAt(m.dagSelectedRow, m.dagSelectedCol)
}

// moveCommitDAGLeft moves selection left within an offshoot row.
func (m *Model) moveCommitDAGLeft() {
	if m.dagSelectedCol > 0 {
		m.dagSelectedCol--
	}
	m.selectedIdx = m.dagNodeIdxAt(m.dagSelectedRow, m.dagSelectedCol)
}

// ensureDAGVisible scrolls the DAG view so the selected row is visible.
func (m *Model) ensureDAGVisible() {
	// Estimate rows per viewport using branchRowHeight.
	visRows := max(m.contentHeight()/branchRowHeight, 1)
	if m.dagSelectedRow < m.scrollOff {
		m.scrollOff = m.dagSelectedRow
	} else if m.dagSelectedRow >= m.scrollOff+visRows {
		m.scrollOff = m.dagSelectedRow - visRows + 1
	}
	m.clampDAGScroll()
}

// clampDAGScroll constrains the DAG scroll offset.
func (m *Model) clampDAGScroll() {
	visRows := max(m.contentHeight()/branchRowHeight, 1)
	maxScroll := max(m.dagTotalRows()-visRows, 0)
	m.scrollOff = clampInt(m.scrollOff, 0, maxScroll)
}

// ---------------------------------------------------------------------------
// Navigation helpers (branch view)
// ---------------------------------------------------------------------------

// branchRow returns the visual row index for a flat branch index.
// Offshoot rows are looked up from the precomputed layout; the primary
// branch occupies the final row after all offshoots.
func (m *Model) branchRow(idx int) int {
	if idx >= len(m.offRowOf) {
		return len(m.offRows) // primary is always the last row
	}
	return m.offRowOf[idx]
}

// effectiveCols returns the widest offshoot row size, used for card width
// and grid positioning. Cards expand to fill the space based on the actual
// maximum branches in any single row, not the total offshoot count.
func (m *Model) effectiveCols() int {
	maxInRow := 0
	for _, row := range m.offRows {
		maxInRow = max(maxInRow, len(row))
	}
	return max(maxInRow, 1)
}

// offshootCount returns the number of non-primary branches.
func (m *Model) offshootCount() int {
	return max(len(m.branches)-1, 0)
}

// offshootRowCount returns how many visual rows the offshoots occupy.
func (m *Model) offshootRowCount() int {
	return len(m.offRows)
}

// totalBranchRows returns the total visual row count (offshoots + primary).
func (m *Model) totalBranchRows() int {
	if len(m.branches) == 0 {
		return 0
	}
	return m.offshootRowCount() + 1
}

// visibleEndRow returns the exclusive end row index that fits in the viewport
// starting from branchScrollOff, using actual row heights.
func (m *Model) visibleEndRow() int {
	ch := m.contentHeight()
	total := m.totalBranchRows()
	accumulated := 0
	for end := m.branchScrollOff; end < total; end++ {
		accumulated += m.branchRowLines(end)
		if accumulated >= ch {
			return end + 1
		}
	}
	return total
}

// visibleBranchRows returns how many branch rows fit in the viewport.
func (m *Model) visibleBranchRows() int {
	return max(m.contentHeight()/branchRowHeight, 1)
}

// halfBranchPage returns the number of rows for a half-page scroll.
func (m *Model) halfBranchPage() int {
	return max(m.visibleBranchRows()/2, 1)
}

func (m *Model) ensureBranchVisible() {
	row := m.branchRow(m.branchIdx)
	if row < m.branchScrollOff {
		m.branchScrollOff = row
	} else if row >= m.branchScrollOff+m.visibleBranchRows() {
		m.branchScrollOff = row - m.visibleBranchRows() + 1
	}
	m.clampBranchScroll()
}

func (m *Model) clampBranchScroll() {
	m.branchScrollOff = max(m.branchScrollOff, 0)
	m.branchScrollOff = min(m.branchScrollOff, m.branchMaxScroll())
}

func (m *Model) branchMaxScroll() int {
	return max(m.totalBranchRows()-m.visibleBranchRows(), 0)
}

// ---------------------------------------------------------------------------
// Shared scroll helpers (for ScrollUp/ScrollDown public API)
// ---------------------------------------------------------------------------

// activeScrollOff returns a pointer to the scroll offset for the active view.
func (m *Model) activeScrollOff() *int {
	switch m.mode {
	case viewCommits:
		return &m.scrollOff
	case viewBranchPick:
		return &m.branchPickerScroll
	default:
		return &m.branchScrollOff
	}
}

// activeMaxScroll returns the max scroll for the active view.
func (m *Model) activeMaxScroll() int {
	switch m.mode {
	case viewCommits:
		if m.dagMode && len(m.dagSections) > 0 {
			visRows := max(m.contentHeight()/branchRowHeight, 1)
			return max(m.dagTotalRows()-visRows, 0)
		}
		return m.commitMaxScroll()
	case viewBranchPick:
		items := m.filteredBranchItems()
		headerLines := 4 // title + border + filter/hint + border
		visible := max(m.contentHeight()-headerLines, 1)
		return max(len(items)-visible, 0)
	default:
		return m.branchMaxScroll()
	}
}

// ---------------------------------------------------------------------------
// Placeholder
// ---------------------------------------------------------------------------

func (m *Model) renderPlaceholder(msg string) string {
	ch := m.contentHeight()
	lines := make([]string, ch)
	emptyLine := strings.Repeat(" ", max(m.width, 0))

	msgStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	text := msgStyle.Render(msg)
	textWidth := lipgloss.Width(text)

	midRow := ch / 2
	leftPad := max((m.width-textWidth)/2, 0)
	rightPad := max(m.width-leftPad-textWidth, 0)
	centeredLine := strings.Repeat(" ", leftPad) + text + strings.Repeat(" ", rightPad)

	for i := range lines {
		if i == midRow {
			lines[i] = centeredLine
		} else {
			lines[i] = emptyLine
		}
	}

	return strings.Join(lines, "\n")
}
