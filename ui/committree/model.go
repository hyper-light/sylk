package committree

import (
	"slices"
	"strings"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
	branches       []BranchNode
	branchIdx      int
	branchScrollOff int
	activeBranch   string // Branch being viewed in commit mode.

	// Commit view state.
	nodes       []TreeNode
	selectedIdx int
	scrollOff   int
}

// New creates a Model with the given theme.
func New(th *theme.Theme) *Model {
	return &Model{
		theme:     th,
		viewDirty: true,
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
	km, ok := msg.(tea.KeyMsg)
	if !ok || !m.focused {
		return m, nil
	}
	cmd := m.handleKey(km)
	return m, cmd
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// SetBranches populates the branch tree view and resets to branch mode.
// Branches are sorted so that the HEAD branch appears last (bottom of the
// tree) and other branches are ordered newest-first above it.
func (m *Model) SetBranches(branches []BranchNode) {
	slices.SortStableFunc(branches, func(a, b BranchNode) int {
		if a.IsHead != b.IsHead {
			if a.IsHead {
				return 1
			}
			return -1
		}
		return b.AuthorTime.Compare(a.AuthorTime)
	})

	m.branches = branches
	m.branchIdx = max(len(branches)-1, 0)
	m.branchScrollOff = 0
	m.mode = viewBranches
	m.viewDirty = true
	m.ensureBranchVisible()
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
	_ = cursorVisible
	switch m.mode {
	case viewCommits:
		return m.viewCommitCards()
	default:
		return m.viewBranchTree()
	}
}

// viewBranchTree renders the branch tree view.
func (m *Model) viewBranchTree() string {
	if len(m.branches) == 0 {
		return m.renderPlaceholder("No branches")
	}

	visible := m.branchVisibleRange()
	lines := make([]string, 0, m.height)

	lastIdx := len(m.branches) - 1
	for _, idx := range visible {
		selected := idx == m.branchIdx
		isFirst := idx == 0
		isLast := idx == lastIdx
		nodeLines := renderBranchNode(m.branches[idx], selected, m.width, m.theme, isFirst, isLast)
		lines = append(lines, nodeLines...)
	}

	return m.padViewport(lines)
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
// and truncates if over.
func (m *Model) padViewport(lines []string) string {
	tildeStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	for len(lines) < m.height {
		tilde := tildeStyle.Render("~")
		pad := strings.Repeat(" ", max(m.width-1, 0))
		lines = append(lines, tilde+pad)
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
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
func (m *Model) handleBranchKey(km tea.KeyMsg) tea.Cmd {
	if len(m.branches) == 0 {
		return nil
	}

	switch km.String() {
	case "j", "down", "shift+down":
		m.branchIdx = min(m.branchIdx+1, len(m.branches)-1)
	case "k", "up", "shift+up":
		m.branchIdx = max(m.branchIdx-1, 0)
	case "g":
		m.branchIdx = 0
	case "G":
		m.branchIdx = len(m.branches) - 1
	case "ctrl+d":
		m.branchIdx = min(m.branchIdx+m.halfPage(), len(m.branches)-1)
	case "ctrl+u":
		m.branchIdx = max(m.branchIdx-m.halfPage(), 0)
	case "enter":
		return m.enterBranch()
	default:
		return nil
	}

	m.ensureBranchVisible()
	m.viewDirty = true
	return nil
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

// activeNodeHeight returns the row height per entry for the active view.
func (m *Model) activeNodeHeight() int {
	if m.mode == viewBranches {
		return branchNodeHeight
	}
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

func (m *Model) ensureBranchVisible() {
	if m.branchIdx < m.branchScrollOff {
		m.branchScrollOff = m.branchIdx
		return
	}
	vis := m.visibleNodeCount()
	if m.branchIdx >= m.branchScrollOff+vis {
		m.branchScrollOff = m.branchIdx - vis + 1
	}
	m.clampBranchScroll()
}

func (m *Model) clampBranchScroll() {
	m.branchScrollOff = max(m.branchScrollOff, 0)
	m.branchScrollOff = min(m.branchScrollOff, m.branchMaxScroll())
}

func (m *Model) branchMaxScroll() int {
	return max(len(m.branches)-m.visibleNodeCount(), 0)
}

func (m *Model) branchVisibleRange() []int {
	count := m.visibleNodeCount()
	endIdx := min(m.branchScrollOff+count, len(m.branches))
	indices := make([]int, 0, endIdx-m.branchScrollOff)
	for i := m.branchScrollOff; i < endIdx; i++ {
		indices = append(indices, i)
	}
	return indices
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
