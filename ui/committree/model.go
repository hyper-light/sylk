package committree

import (
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

// Compile-time interface assertions.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// viewMode tracks which sub-view is active in the commit tree panel.
type viewMode int

const (
	viewBranches viewMode = iota // Branch tree (default).
	viewLoading                  // Spinner while loading commits+stats.
	viewCommits                  // Commit cards for a single branch.
)

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
// Model
// ---------------------------------------------------------------------------

// Model is the core state for the commit tree visualization panel.
type Model struct {
	focused   bool
	width     int
	height    int
	viewDirty bool
	theme     *theme.Theme

	mode viewMode

	// Branch view state.
	branches        []BranchNode
	branchIdx       int
	branchScrollOff int
	activeBranch    string // Branch being viewed in commit mode.
	defaultBranch   string // Repository default branch name.

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

	// Visual bounce offset from overscroll physics.
	bounceOffset int

	// Per-card rendering cache indexed by flat branch index.
	cardCache []cardCacheEntry

	// Per-node rendering cache indexed by commit node index.
	nodeCache []nodeCacheEntry
}

// New creates a Model with the given theme.
func New(th *theme.Theme) *Model {
	return &Model{
		theme:       th,
		viewDirty:   true,
		expandedIdx: -1,
	}
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

	slices.SortStableFunc(branches, func(a, b BranchNode) int {
		ap := a.Name == defaultBranch
		bp := b.Name == defaultBranch
		if ap != bp {
			if ap {
				return 1
			}
			return -1
		}
		return b.AuthorTime.Compare(a.AuthorTime)
	})

	m.branches = branches

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
	// Preserve commit view if the user drilled into a branch; only reset
	// to branches when no drill-down is active.
	if m.mode != viewCommits {
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
func (m *Model) SetNodesWithStats(nodes []TreeNode, stats map[string][2]int, hasMore bool) {
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
	m.mode = viewCommits
	m.invalidateNodeCache()
	m.viewDirty = true
}

// SetDAGNodesWithStats atomically sets commit data with full DAG graph layout,
// transitioning from the loading spinner to the DAG commit view in one frame.
// DAG mode disables pagination (all branch-unique commits loaded at once).
func (m *Model) SetDAGNodesWithStats(nodes []TreeNode, stats map[string][2]int,
	graphRows []GraphRow, maxLane int) {
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
	m.mode = viewCommits
	m.invalidateNodeCache()
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

// NeedsBlink reports whether the commit input cursor or commit progress
// spinner needs blink-rate ticking.
func (m *Model) NeedsBlink() bool {
	return m.focused && (m.commitInputActive || m.commitPhase == commitInProgress)
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
	switch m.mode {
	case viewLoading:
		return nil // absorb clicks during loading
	case viewCommits:
		return m.clickCommitView(viewY)
	default:
		return m.clickBranchView(viewX, viewY)
	}
}

// clickCommitView maps a click Y coordinate to a commit node index.
func (m *Model) clickCommitView(viewY int) tea.Cmd {
	if len(m.nodes) == 0 {
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
	if m.selectedIdx != prev {
		return m.selectionCmd()
	}
	return nil
}

// branchRowLines returns the height (in terminal rows) of a rendered branch
// row, accounting for expanded cards that are taller than branchRowHeight.
func (m *Model) branchRowLines(rowIdx int) int {
	orc := m.offshootRowCount()
	cols := m.effectiveCols()

	if rowIdx >= orc {
		// Primary row: card height + padding to branchRowHeight minimum.
		cardH := m.branchCardHeight(orc) // primary is at flat index = orc
		return max(cardH, branchRowHeight)
	}

	// Offshoot row: tallest card in this row + 2 connector lines.
	rowStart := rowIdx * cols
	rowEnd := min(rowStart+cols, orc)
	tallest := 0
	for i := rowStart; i < rowEnd; i++ {
		tallest = max(tallest, m.branchCardHeight(i))
	}
	return tallest + 2 // +2 for merge/trunk connector lines
}

// branchCardHeight returns the rendered line count for a single branch card.
// Normal cards: 4 lines. Expanded: 6–7 depending on inline prompts.
func (m *Model) branchCardHeight(flatIdx int) int {
	const baseLines = 4 // top border + header + subject + bottom border
	if m.expandedIdx != flatIdx {
		return baseLines
	}
	// Expanded: +2 for divider + action line.
	h := baseLines + 2
	// Optional extra line (delete confirm / commit input / blocked reason).
	b := m.branches[flatIdx]
	if m.deleteConfirmActive {
		h++
	} else if m.commitInputActive && b.IsHead {
		h++
	} else {
		exp := m.buildExpansion(false)
		if exp != nil && exp.actionBlockedReason(b.IsHead) != "" {
			h++
		}
	}
	return h
}

// clickBranchView maps click coordinates to a branch card in the tree.
// Clicking an already-selected card toggles the expansion dropdown.
// Clicking an action badge on an expanded card executes that action.
func (m *Model) clickBranchView(viewX, viewY int) tea.Cmd {
	if len(m.branches) == 0 {
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
		// Offshoot row.
		cardW = offshootCardWidth(m.width, cols)
		totalContent := cols*cardW + max(cols-1, 0)*branchCardGap
		leftMargin := max((m.width-totalContent)/2, 0)

		col := -1
		for c := range cols {
			cl := leftMargin + c*(cardW+branchCardGap)
			if viewX >= cl && viewX < cl+cardW {
				col = c
				cardLeft = cl
				break
			}
		}
		if col < 0 {
			return nil
		}
		idx := targetRow*cols + col
		if idx >= oc {
			return nil
		}
		targetIdx = idx
	} else if targetRow == offRows {
		// Primary branch — check X bounds same as offshoots.
		cardW = primaryCardWidth(m.width)
		cardLeft = max((m.width-cardW)/2, 0)
		if viewX < cardLeft || viewX >= cardLeft+cardW {
			return nil
		}
		targetIdx = oc
	} else {
		return nil
	}

	// If clicking the expanded card's expansion area (divider, action line,
	// confirm/input, bottom border), handle it without toggling expansion.
	if m.expandedIdx == targetIdx {
		cardH := m.branchCardHeight(targetIdx)
		if localY >= 3 && localY < cardH {
			if localY == 4 {
				if cmd := m.clickActionBadge(viewX, cardLeft, cardW); cmd != nil {
					return cmd
				}
			}
			// Absorb click in expansion area.
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

// clickActionBadge maps a click X coordinate on the expanded action line
// to a specific action badge and executes it. Returns nil if the click
// didn't land on a badge.
func (m *Model) clickActionBadge(screenX, cardLeft, cardWidth int) tea.Cmd {
	if m.expandedIdx < 0 || m.expandedIdx >= len(m.branches) {
		return nil
	}
	b := m.branches[m.expandedIdx]

	// The action line content starts after the left border character.
	// Reconstruct badge positions by measuring the same segments as
	// buildExpContentLine. All X positions are relative to the card left edge.
	localX := screenX - cardLeft - 1 // -1 for left border "│"
	if localX < 0 {
		return nil
	}

	// Walk the action line to find badge X ranges.
	// Prefix: " N commits" (+ optional status badge for HEAD).
	count := itoa(b.CommitCount)
	if b.CommitCountCapped {
		count += "+"
	}
	x := 1 + len(count+" commits") // leading space + count text

	if b.IsHead {
		if m.workingConflicts {
			x += 2 + len("[conflicts]")
		} else if m.workingDirty {
			x += 2 + len("[dirty]")
		}
	}

	// Check each visible action badge.
	actions := m.expandedVisibleActions()
	for i, actionID := range actions {
		gap := 2
		if actionID == branchActionDelete {
			gap = 1
		}
		x += gap
		var label string
		switch actionID {
		case branchActionCommit:
			label = "[Commit]"
		case branchActionSwitch:
			label = "[Switch]"
		case branchActionDelete:
			label = "[Delete]"
		}
		badgeW := len(label)
		if localX >= x && localX < x+badgeW {
			alreadySelected := m.expandedAction == i
			m.expandedAction = i
			m.viewDirty = true
			if !m.isActionEnabled(actionID) {
				return nil
			}
			if alreadySelected {
				return m.toggleExpandedSubview()
			}
			// Close any open sub-view from a previously selected action.
			m.deleteConfirmActive = false
			m.deleteConfirmYes = false
			if m.commitInputActive {
				m.clearCommitInput()
			}
			return nil
		}
		x += badgeW
		_ = i
	}

	return nil
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m *Model) View(cursorVisible bool) string {
	switch m.mode {
	case viewLoading:
		return m.viewLoadingSpinner()
	case viewCommits:
		return m.viewCommitCards()
	default:
		return m.viewBranchTree(cursorVisible)
	}
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
	totalRows := m.totalBranchRows()
	visRows := m.visibleBranchRows()
	endRow := min(m.branchScrollOff+visRows, totalRows)
	trunkPos := m.width / 2

	const borderCols = 2

	wt := m.buildWorkingTreeState()
	exp := m.buildExpansion(cursorVisible)
	lines := make([]string, 0, m.height)

	for rowIdx := m.branchScrollOff; rowIdx < endRow; rowIdx++ {
		if rowIdx < offRows {
			// Offshoot row — build cards via cache, compose into row.
			rowStart := rowIdx * cols
			rowEnd := min(rowStart+cols, oc)
			hasTrunkAbove := rowIdx > 0

			totalContent := len(m.branches[rowStart:rowEnd])*cardWidth + max(len(m.branches[rowStart:rowEnd])-1, 0)*branchCardGap
			leftMargin := max((m.width-totalContent)/2, 0)
			innerWidth := max(cardWidth-borderCols, 0)

			cardSlices := make([][]string, rowEnd-rowStart)
			for i := range rowEnd - rowStart {
				flatIdx := rowStart + i
				b := m.branches[flatIdx]
				selected := flatIdx == m.branchIdx
				isExpanded := flatIdx == m.expandedIdx
				hasTrunkBot := (rowEnd - rowStart) == 1
				cardLeft := leftMargin + i*(cardWidth+branchCardGap)
				trunkInner := trunkPos - cardLeft - 1

				var cardExp *branchExpansion
				if isExpanded {
					cardExp = exp
				}
				cardSlices[i] = m.getCachedCard(flatIdx, b, selected,
					innerWidth, trunkInner, hasTrunkAbove && (rowEnd-rowStart) == 1, hasTrunkBot,
					isExpanded, cardExp, wt)
			}

			rowLines := composeOffshootRow(cardSlices, cardWidth, m.width, m.theme, hasTrunkAbove)
			lines = append(lines, rowLines...)
		} else {
			// Primary row — build card via cache, compose into row.
			flatIdx := oc
			primary := m.branches[len(m.branches)-1]
			selected := m.branchIdx == oc
			isExpanded := m.expandedIdx == oc

			primaryCW := primaryCardWidth(m.width)
			innerWidth := max(primaryCW-borderCols, 0)
			leftPad := max((m.width-primaryCW)/2, 0)
			trunkInner := trunkPos - leftPad - 1

			var cardExp *branchExpansion
			if isExpanded {
				cardExp = exp
			}
			cardLines := m.getCachedCard(flatIdx, primary, selected,
				innerWidth, trunkInner, oc > 0, false,
				isExpanded, cardExp, wt)

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
	msg := spinSt.Render(frame) + " " + textSt.Render("Loading commits...")
	msgWidth := lipgloss.Width(msg)

	lines := make([]string, m.height)
	emptyLine := strings.Repeat(" ", max(m.width, 0))
	midRow := m.height / 2
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
// used as a bottom overlay during infinite scroll page loads. Time-based
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

	visible := m.commitVisibleRange()
	lines := make([]string, 0, m.height)

	lastIdx := len(m.nodes) - 1
	for _, idx := range visible {
		selected := idx == m.selectedIdx
		isFirst := idx == 0
		isLast := idx == lastIdx && !m.hasMore
		nodeLines := m.getCachedNode(idx, m.nodes[idx], selected, isFirst, isLast, nil, 0)
		lines = append(lines, nodeLines...)
	}

	return m.padViewport(lines)
}

// padViewport vertically centers lines within the viewport height and
// applies bounce offset. When a page load is in flight, overlays a
// loading bar on the last line (no layout shift).
func (m *Model) padViewport(lines []string) string {
	emptyLine := strings.Repeat(" ", max(m.width, 0))
	if len(lines) < m.height {
		topPad := (m.height - len(lines)) / 2
		centered := make([]string, m.height)
		for i := range centered {
			centered[i] = emptyLine
		}
		copy(centered[topPad:], lines)
		lines = centered
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	if m.showLoadingBar() && len(lines) > 0 {
		lines[len(lines)-1] = m.renderLoadingBar()
	}
	lines = applyBounceShift(lines, m.bounceOffset, m.height, emptyLine)
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (m *Model) handleKey(km tea.KeyMsg) tea.Cmd {
	switch m.mode {
	case viewLoading:
		if km.String() == "esc" {
			m.exitToBranches()
		}
		return nil
	case viewCommits:
		return m.handleCommitKey(km)
	default:
		return m.handleBranchKey(km)
	}
}

// handleBranchKey processes keys in branch tree view.
// Navigation is grid-based: j/k move between rows, h/l move within a row.
// When a card is expanded, h/l move between actions, enter executes.
func (m *Model) handleBranchKey(km tea.KeyMsg) tea.Cmd {
	if len(m.branches) == 0 {
		return nil
	}

	// Expanded card: route to action handler.
	if m.expandedIdx >= 0 {
		return m.handleExpandedKey(km)
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

// expandedVisibleActions returns the ordered action IDs for the currently
// expanded branch. HEAD shows [Commit]; default shows [Switch];
// other branches show [Switch, Delete].
func (m *Model) expandedVisibleActions() []int {
	if m.expandedIdx < 0 || m.expandedIdx >= len(m.branches) {
		return nil
	}
	b := m.branches[m.expandedIdx]
	if b.IsHead {
		return []int{branchActionCommit}
	}
	if b.Name == m.defaultBranch {
		return []int{branchActionSwitch}
	}
	return []int{branchActionSwitch, branchActionDelete}
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
// Tab cycles between action badges; space/enter triggers the focused action;
// shift+tab or esc collapses the card. When the commit input is active,
// keys route to the inline text input.
func (m *Model) handleExpandedKey(km tea.KeyMsg) tea.Cmd {
	if m.deleteConfirmActive {
		return m.handleDeleteConfirmKey(km)
	}
	if m.commitInputActive {
		return m.handleCommitInputKey(km)
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
		next := m.expandedAction + 1
		if next >= actionMax {
			next = 0
		}
		m.expandedAction = next
	case "shift+tab":
		m.collapseBranch()
	case "enter":
		return m.executeExpandedAction()
	case " ":
		return m.toggleExpandedSubview()
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
	case "esc", "tab", "shift+tab":
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
	case "tab", "shift+tab":
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

// moveBranchDown moves to the same column in the next row.
func (m *Model) moveBranchDown() {
	cols := m.effectiveCols()
	oc := m.offshootCount()

	if m.branchIdx >= oc {
		return // already on primary
	}
	next := m.branchIdx + cols
	if next >= oc {
		m.branchIdx = oc // move to primary
	} else {
		m.branchIdx = next
	}
}

// moveBranchUp moves to the same column in the previous row.
func (m *Model) moveBranchUp() {
	cols := m.effectiveCols()
	oc := m.offshootCount()

	if m.branchIdx == oc && oc > 0 {
		// On primary, move to first item of last offshoot row.
		lastRowStart := ((oc - 1) / cols) * cols
		m.branchIdx = lastRowStart
		return
	}
	if m.branchIdx < cols {
		return // already on first row
	}
	m.branchIdx -= cols
}

// moveBranchRight moves to the next card in the same row.
func (m *Model) moveBranchRight() {
	cols := m.effectiveCols()
	oc := m.offshootCount()
	if m.branchIdx >= oc {
		return // primary is a single card
	}
	rowStart := (m.branchIdx / cols) * cols
	rowEnd := min(rowStart+cols, oc)
	if m.branchIdx+1 < rowEnd {
		m.branchIdx++
	}
}

// moveBranchLeft moves to the previous card in the same row.
func (m *Model) moveBranchLeft() {
	cols := m.effectiveCols()
	oc := m.offshootCount()
	if m.branchIdx >= oc {
		return // primary is a single card
	}
	rowStart := (m.branchIdx / cols) * cols
	if m.branchIdx > rowStart {
		m.branchIdx--
	}
}

// handleCommitKey processes keys in commit cards view.
func (m *Model) handleCommitKey(km tea.KeyMsg) tea.Cmd {
	if km.String() == "esc" {
		m.exitToBranches()
		return nil
	}

	if len(m.nodes) == 0 {
		return nil
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
	m.mode = viewLoading
	m.viewDirty = true
	name := branch.Name
	return func() tea.Msg {
		return BranchSelectedMsg{Name: name}
	}
}

// exitToBranches transitions back to the branch tree view.
func (m *Model) exitToBranches() {
	m.mode = viewBranches
	m.activeBranch = ""
	m.nodes = nil
	m.hasMore = false
	m.loadingMore = false
	m.lastHash = ""
	m.graphRows = nil
	m.maxGraphLane = 0
	m.dagMode = false
	m.viewDirty = true
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
	return max(m.height/h/2, 1)
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

func (m *Model) visibleNodeCount() int {
	h := m.activeNodeHeight()
	return max(m.height/h, 1)
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

// ---------------------------------------------------------------------------
// Navigation helpers (branch view)
// ---------------------------------------------------------------------------

// branchRow returns the visual row index for a flat branch index.
// Offshoot branches (indices 0..offshootCount-1) are arranged in rows of
// effectiveCols; the primary branch occupies the final row.
func (m *Model) branchRow(idx int) int {
	oc := m.offshootCount()
	if idx >= oc {
		return m.offshootRowCount() // primary is always the last row
	}
	return idx / m.effectiveCols()
}

// effectiveCols returns the number of offshoot columns for the current width
// and branch count.
func (m *Model) effectiveCols() int {
	return min(branchCols(m.width), max(m.offshootCount(), 1))
}

// offshootCount returns the number of non-primary branches.
func (m *Model) offshootCount() int {
	return max(len(m.branches)-1, 0)
}

// offshootRowCount returns how many visual rows the offshoots occupy.
func (m *Model) offshootRowCount() int {
	oc := m.offshootCount()
	cols := m.effectiveCols()
	return (oc + cols - 1) / cols
}

// totalBranchRows returns the total visual row count (offshoots + primary).
func (m *Model) totalBranchRows() int {
	if len(m.branches) == 0 {
		return 0
	}
	return m.offshootRowCount() + 1
}

// visibleBranchRows returns how many branch rows fit in the viewport.
func (m *Model) visibleBranchRows() int {
	return max(m.height/branchRowHeight, 1)
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
	if m.mode == viewCommits {
		return &m.scrollOff
	}
	return &m.branchScrollOff
}

// activeMaxScroll returns the max scroll for the active view.
func (m *Model) activeMaxScroll() int {
	if m.mode == viewCommits {
		return m.commitMaxScroll()
	}
	return m.branchMaxScroll()
}

// ---------------------------------------------------------------------------
// Placeholder
// ---------------------------------------------------------------------------

func (m *Model) renderPlaceholder(msg string) string {
	lines := make([]string, m.height)
	emptyLine := strings.Repeat(" ", max(m.width, 0))

	msgStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	text := msgStyle.Render(msg)
	textWidth := lipgloss.Width(text)

	midRow := m.height / 2
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
