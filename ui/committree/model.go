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

	// Commit input state (HEAD branch expanded card).
	commitInputActive bool
	commitPhase       commitPhase
	commitMsg         string
	commitCursor      int
	commitSpinner     int // spinner frame index

	// Commit view state.
	nodes       []TreeNode
	selectedIdx int
	scrollOff   int

	// Visual bounce offset from overscroll physics.
	bounceOffset int
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
	m.branchIdx = max(len(branches)-1, 0)
	if prevName != "" {
		for i, b := range m.branches {
			if b.Name == prevName {
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
	m.mode = viewBranches
	m.viewDirty = true
	m.ensureBranchVisible()
}

// SetWorkingTreeStatus updates the working tree dirty/conflicts flags used
// to determine whether branch actions (switch, delete) are enabled.
func (m *Model) SetWorkingTreeStatus(dirty, conflicts bool) {
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
	m.viewDirty = true
}

// UpdateStats merges diff statistics into existing nodes by hash.
func (m *Model) UpdateStats(stats map[string][2]int) {
	for i := range m.nodes {
		if s, ok := stats[m.nodes[i].Hash]; ok {
			m.nodes[i].Additions = s[0]
			m.nodes[i].Deletions = s[1]
		}
	}
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

// ActiveBranch returns the name of the branch being viewed in commit mode,
// or an empty string if in branch view.
func (m *Model) ActiveBranch() string {
	if m.mode == viewCommits {
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
	return true
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

// NeedsBlink reports whether the commit input cursor or spinner needs ticking.
func (m *Model) NeedsBlink() bool {
	return m.focused && (m.commitInputActive || m.commitPhase == commitInProgress)
}

// AdvanceSpinner advances the commit spinner frame and marks the view dirty.
// Called from the blink tick so the spinner animates.
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
// View
// ---------------------------------------------------------------------------

func (m *Model) View(cursorVisible bool) string {
	switch m.mode {
	case viewCommits:
		return m.viewCommitCards()
	default:
		return m.viewBranchTree(cursorVisible)
	}
}

// viewBranchTree renders the branch tree view with side-by-side offshoot cards.
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

	wt := m.buildWorkingTreeState()
	exp := m.buildExpansion(cursorVisible)
	lines := make([]string, 0, m.height)

	for rowIdx := m.branchScrollOff; rowIdx < endRow; rowIdx++ {
		if rowIdx < offRows {
			// Offshoot row.
			rowStart := rowIdx * cols
			rowEnd := min(rowStart+cols, oc)
			row := m.branches[rowStart:rowEnd]

			selectedCol := -1
			if m.branchIdx >= rowStart && m.branchIdx < rowEnd {
				selectedCol = m.branchIdx - rowStart
			}

			expandedCol := -1
			if m.expandedIdx >= rowStart && m.expandedIdx < rowEnd {
				expandedCol = m.expandedIdx - rowStart
			}

			hasTrunkAbove := rowIdx > 0
			rowLines := renderOffshootRow(row, selectedCol, expandedCol, exp, cardWidth, m.width, m.theme, hasTrunkAbove, wt)
			lines = append(lines, rowLines...)
		} else {
			// Primary row (always last).
			primary := m.branches[len(m.branches)-1]
			selected := m.branchIdx == oc
			expanded := m.expandedIdx == oc
			rowLines := renderPrimaryRow(primary, selected, expanded, exp, m.width, m.theme, oc > 0, wt)
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
		hasStagedFiles:   m.hasStagedFiles,
		commitInput:      m.commitInputActive,
		commitPhase:      m.commitPhase,
		commitMsg:        m.commitMsg,
		commitCursor:     m.commitCursor,
		commitSpinner:    m.commitSpinner,
		cursorVisible:    cursorVisible,
	}
}

// viewCommitCards renders the commit cards view (existing behavior).
func (m *Model) viewCommitCards() string {
	if len(m.nodes) == 0 {
		return m.renderPlaceholder("Loading commits...")
	}

	visible := m.commitVisibleRange()
	lines := make([]string, 0, m.height)

	lastIdx := len(m.nodes) - 1
	for _, idx := range visible {
		selected := idx == m.selectedIdx
		isLast := idx == lastIdx
		nodeLines := renderNode(m.nodes[idx], selected, m.width, m.theme, isLast)
		lines = append(lines, nodeLines...)
	}

	return m.padViewport(lines)
}

// padViewport pads lines to fill the viewport height with tilde filler,
// truncates if over, and applies bounce offset.
func (m *Model) padViewport(lines []string) string {
	tildeStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	emptyLine := strings.Repeat(" ", max(m.width, 0))
	for len(lines) < m.height {
		tilde := tildeStyle.Render("~")
		pad := strings.Repeat(" ", max(m.width-1, 0))
		lines = append(lines, tilde+pad)
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	lines = applyBounceShift(lines, m.bounceOffset, m.height, emptyLine)
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (m *Model) handleKey(km tea.KeyMsg) tea.Cmd {
	switch m.mode {
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

// handleExpandedKey processes keys when a branch card is expanded.
// Tab cycles between action badges; space/enter triggers the focused action;
// shift+tab or esc collapses the card. When the commit input is active,
// keys route to the inline text input.
func (m *Model) handleExpandedKey(km tea.KeyMsg) tea.Cmd {
	if m.commitInputActive {
		return m.handleCommitInputKey(km)
	}

	actionMax := len(m.expandedVisibleActions())
	if actionMax == 0 {
		return nil
	}

	switch km.String() {
	case "h", "left":
		m.expandedAction = max(m.expandedAction-1, 0)
	case "l", "right":
		m.expandedAction = min(m.expandedAction+1, actionMax-1)
	case "tab":
		m.expandedAction = (m.expandedAction + 1) % actionMax
	case "shift+tab":
		m.collapseBranch()
	case "enter", " ":
		return m.executeExpandedAction()
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

// expandBranch toggles expansion on the currently selected branch card.
func (m *Model) expandBranch() {
	if m.expandedIdx == m.branchIdx {
		m.collapseBranch()
		return
	}
	m.expandedIdx = m.branchIdx
	m.clearCommitInput()
	m.expandedAction = branchActionSwitch
}

// collapseBranch closes any expanded card.
func (m *Model) collapseBranch() {
	m.expandedIdx = -1
	m.expandedAction = 0
	m.clearCommitInput()
}

// executeExpandedAction runs the selected action on the expanded branch.
func (m *Model) executeExpandedAction() tea.Cmd {
	actionID := m.expandedActionID()
	if actionID < 0 {
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
	}

	m.collapseBranch()
	m.viewDirty = true

	switch actionID {
	case branchActionSwitch:
		return func() tea.Msg { return BranchSwitchMsg{Name: name} }
	case branchActionDelete:
		return func() tea.Msg { return BranchDeleteMsg{Name: name} }
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

	if m.selectedIdx != prev {
		return m.selectionCmd()
	}
	return nil
}

// enterBranch transitions to the commit view for the selected branch.
func (m *Model) enterBranch() tea.Cmd {
	branch := m.branches[m.branchIdx]
	m.activeBranch = branch.Name
	m.nodes = nil
	m.selectedIdx = 0
	m.scrollOff = 0
	m.mode = viewCommits
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
