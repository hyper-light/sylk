package gitpanel

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
)

// Compile-time interface assertions.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// GitTab identifies one of the tabs in the git explorer.
type GitTab int

const (
	TabCommits     GitTab = iota
	TabBranches
	TabTags
	TabUncommitted
	tabCount // sentinel: total tab count
)

// -------------------------------------------------------------------------
// Public messages (handled by the parent)
// -------------------------------------------------------------------------

// BranchCheckedOutMsg is emitted after a successful branch checkout.
type BranchCheckedOutMsg struct{ Name string }

// BranchCheckoutBlockedMsg is emitted when checkout is blocked.
type BranchCheckoutBlockedMsg struct{ Reason string }

// -------------------------------------------------------------------------
// Async load messages
// -------------------------------------------------------------------------

type commitsLoadedMsg struct{ entries []commitEntry }
type branchesLoadedMsg struct{ entries []branchEntry }
type tagsLoadedMsg struct{ entries []tagEntry }
type uncommittedLoadedMsg struct{ entries []uncommittedEntry }
type checkoutResultMsg struct {
	name string
	err  error
}

// -------------------------------------------------------------------------
// Model
// -------------------------------------------------------------------------

// Model is the git explorer panel shown on the left side of Git mode.
type Model struct {
	focused   bool
	activeTab GitTab
	width     int
	height    int

	theme     *theme.Theme
	gitClient *git.GitClient
	viewDirty bool

	commits     commitsTab
	branches    branchesTab
	tags        tagsTab
	uncommitted uncommittedTab
}

// New creates a new git panel model.
func New(th *theme.Theme, gc *git.GitClient) *Model {
	m := &Model{
		theme:     th,
		gitClient: gc,
		viewDirty: true,
	}

	// Initialize sort defaults (newest first).
	m.commits.sortMode = sortAgeDesc
	m.branches.sortMode = sortAgeDesc
	m.tags.sortMode = sortAgeDesc
	m.uncommitted.sortMode = sortAlphaAsc

	return m
}

// -------------------------------------------------------------------------
// component.Focusable
// -------------------------------------------------------------------------

func (m *Model) ID() component.FocusID { return component.FocusGitPanel }
func (m *Model) Focused() bool          { return m.focused }
func (m *Model) SetFocused(f bool)      { m.focused = f; m.viewDirty = true }

// NeedsBlink reports whether the cursor needs to blink (filter input active).
func (m *Model) NeedsBlink() bool {
	return m.focused && m.activeListState().filterActive
}

// -------------------------------------------------------------------------
// component.Resizable
// -------------------------------------------------------------------------

func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewDirty = true
}

// -------------------------------------------------------------------------
// component.Component
// -------------------------------------------------------------------------

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (component.Component, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.KeyMsg:
		m.viewDirty = true
		return m, m.handleKey(typed)

	case commitsLoadedMsg:
		m.viewDirty = true
		m.setCommitEntries(typed.entries)
		return m, nil

	case branchesLoadedMsg:
		m.viewDirty = true
		m.setBranchEntries(typed.entries)
		return m, nil

	case tagsLoadedMsg:
		m.viewDirty = true
		m.setTagEntries(typed.entries)
		return m, nil

	case uncommittedLoadedMsg:
		m.viewDirty = true
		m.setUncommittedEntries(typed.entries)
		return m, nil

	case checkoutResultMsg:
		if typed.err != nil {
			return m, func() tea.Msg {
				return BranchCheckoutBlockedMsg{Reason: typed.err.Error()}
			}
		}
		return m, func() tea.Msg {
			return BranchCheckedOutMsg{Name: typed.name}
		}
	}

	return m, nil
}

// -------------------------------------------------------------------------
// Data loading
// -------------------------------------------------------------------------

// LoadData returns a tea.Cmd that loads commits, branches, and uncommitted
// files from the git client asynchronously.
func (m *Model) LoadData() tea.Cmd {
	gc := m.gitClient
	return tea.Batch(
		func() tea.Msg { return commitsLoadedMsg{entries: loadCommits(gc)} },
		func() tea.Msg { return branchesLoadedMsg{entries: loadBranches(gc)} },
		func() tea.Msg { return tagsLoadedMsg{entries: loadTags(gc)} },
		func() tea.Msg { return uncommittedLoadedMsg{entries: loadUncommitted(gc)} },
	)
}

// setCommitEntries populates the commits tab with loaded data.
func (m *Model) setCommitEntries(raw []commitEntry) {
	ls := &m.commits.listState
	ls.entries = make([]listEntry, len(raw))
	for i, e := range raw {
		ls.entries[i] = e
	}
	m.commits.computeColWidths()
	ls.rebuildFiltered()
}

// setBranchEntries populates the branches tab with loaded data.
func (m *Model) setBranchEntries(raw []branchEntry) {
	ls := &m.branches.listState
	ls.entries = make([]listEntry, len(raw))
	for i, e := range raw {
		ls.entries[i] = e
	}
	m.branches.computeColWidths()
	ls.rebuildFiltered()
}

// setTagEntries populates the tags tab with loaded data.
func (m *Model) setTagEntries(raw []tagEntry) {
	ls := &m.tags.listState
	ls.entries = make([]listEntry, len(raw))
	for i, e := range raw {
		ls.entries[i] = e
	}
	m.tags.computeColWidths()
	ls.rebuildFiltered()
}

// setUncommittedEntries populates the uncommitted tab with loaded data
// and preserves staging states for files that are still present.
func (m *Model) setUncommittedEntries(raw []uncommittedEntry) {
	// Build lookup from old entries so we can restore staging states.
	prev := make(map[string]StagingState, len(m.uncommitted.stagingStates))
	for i, s := range m.uncommitted.stagingStates {
		if s == StagingDefault || i >= len(m.uncommitted.entries) {
			continue
		}
		if ue, ok := m.uncommitted.entries[i].(uncommittedEntry); ok {
			prev[ue.path] = s
		}
	}

	ls := &m.uncommitted.listState
	ls.entries = make([]listEntry, len(raw))
	states := make([]StagingState, len(raw))
	for i, e := range raw {
		ls.entries[i] = e
		if s, ok := prev[e.path]; ok {
			states[i] = s
		}
	}
	m.uncommitted.stagingStates = states
	ls.rebuildFiltered()
}

// -------------------------------------------------------------------------
// ViewDirty
// -------------------------------------------------------------------------

// ViewDirty reports whether the view needs to be re-rendered, and clears
// the dirty flag.
func (m *Model) ViewDirty() bool {
	if m.viewDirty {
		m.viewDirty = false
		return true
	}
	return false
}

// -------------------------------------------------------------------------
// Key handling
// -------------------------------------------------------------------------

func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if !m.focused {
		return nil
	}

	ls := m.activeListState()

	if ls.filterActive {
		if ls.handleFilterKey(key) {
			return nil
		}
	}

	return m.handleNavigationKey(key)
}

// handleNavigationKey processes keys when the filter is NOT active (or was
// not consumed by the filter handler).
func (m *Model) handleNavigationKey(key tea.KeyMsg) tea.Cmd {
	// Handle uncommitted options bar focus.
	if m.activeTab == TabUncommitted && m.uncommitted.optionsFocused {
		return m.handleOptionsKey(key)
	}

	ls := m.activeListState()

	switch key.String() {
	case "shift+tab":
		m.cycleTab()
	case "tab":
		if m.activeTab == TabUncommitted && !ls.filterActive {
			m.uncommitted.optionsFocused = true
		}
	case "/":
		ls.activateFilter()
	case "esc":
		// Propagate to parent (return nil so app can handle it).
		return nil
	case "j", "down":
		ls.moveDown(1)
	case "k", "up":
		ls.moveUp(1)
	case "g":
		ls.cursor = 0
		ls.clampCursor()
	case "G":
		ls.cursor = max(len(ls.filtered)-1, 0)
		ls.clampCursor()
	case " ":
		if m.activeTab == TabUncommitted {
			m.toggleUncommittedStaging()
		}
	case "enter":
		return m.selectEntry()
	}

	return nil
}

// handleOptionsKey processes keys when focus is on the uncommitted
// options bar ([All] badge).
func (m *Model) handleOptionsKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "enter", " ":
		m.uncommitted.toggleAll()
	case "tab", "shift+tab", "esc":
		m.uncommitted.optionsFocused = false
	case "j", "down":
		m.uncommitted.optionsFocused = false
	case "k", "up":
		m.uncommitted.optionsFocused = false
		m.uncommitted.moveUp(0) // keep cursor in place
	}
	return nil
}

// toggleUncommittedStaging cycles the staging state of the selected
// uncommitted entry: Default → Staged → Excluded → Default.
func (m *Model) toggleUncommittedStaging() {
	ls := &m.uncommitted.listState
	if len(ls.filtered) == 0 {
		return
	}
	idx := ls.filtered[ls.cursor]
	if idx >= len(m.uncommitted.stagingStates) {
		return
	}
	current := m.uncommitted.stagingStates[idx]
	m.uncommitted.stagingStates[idx] = (current + 1) % stagingStateCount
}

// cycleTab advances activeTab to the next tab, wrapping around.
func (m *Model) cycleTab() {
	m.activeTab = GitTab((int(m.activeTab) + 1) % int(tabCount))
}

// selectEntry handles enter on the currently selected entry.
func (m *Model) selectEntry() tea.Cmd {
	if m.activeTab != TabBranches {
		return nil
	}
	return m.checkoutSelectedBranch()
}

// checkoutSelectedBranch attempts to checkout the selected branch.
// Returns a blocked message if the working tree is dirty or conflicted,
// or if the branch is already checked out.
func (m *Model) checkoutSelectedBranch() tea.Cmd {
	ls := &m.branches.listState
	if len(ls.filtered) == 0 {
		return nil
	}

	idx := ls.filtered[ls.cursor]
	entry, ok := ls.entries[idx].(branchEntry)
	if !ok {
		return nil
	}

	if entry.isHead {
		return nil // already on this branch
	}

	// Check if working tree blocks checkout.
	if m.headHasConflicts() {
		return func() tea.Msg {
			return BranchCheckoutBlockedMsg{Reason: "resolve conflicts before switching branches"}
		}
	}
	if m.headHasUncommitted() {
		return func() tea.Msg {
			return BranchCheckoutBlockedMsg{Reason: "commit or stash changes before switching branches"}
		}
	}

	gc := m.gitClient
	name := entry.name
	return func() tea.Msg {
		return checkoutResultMsg{name: name, err: gc.CheckoutBranch(name)}
	}
}

// headHasConflicts reports whether the HEAD branch entry has merge conflicts.
func (m *Model) headHasConflicts() bool {
	for _, e := range m.branches.entries {
		if be, ok := e.(branchEntry); ok && be.isHead {
			return be.hasConflicts
		}
	}
	return false
}

// headHasUncommitted reports whether the HEAD branch entry has uncommitted changes.
func (m *Model) headHasUncommitted() bool {
	for _, e := range m.branches.entries {
		if be, ok := e.(branchEntry); ok && be.isHead {
			return be.uncommitted
		}
	}
	return false
}

// HasAnyStagedFiles reports whether any uncommitted file is marked StagingStaged.
func (m *Model) HasAnyStagedFiles() bool {
	for _, s := range m.uncommitted.stagingStates {
		if s == StagingStaged {
			return true
		}
	}
	return false
}

// StagedFilePaths returns the paths of all uncommitted files marked StagingStaged.
func (m *Model) StagedFilePaths() []string {
	var paths []string
	for i, s := range m.uncommitted.stagingStates {
		if s != StagingStaged {
			continue
		}
		if i < len(m.uncommitted.entries) {
			if ue, ok := m.uncommitted.entries[i].(uncommittedEntry); ok {
				paths = append(paths, ue.path)
			}
		}
	}
	return paths
}

// ToggleSearch activates or deactivates the filter bar on the active tab.
func (m *Model) ToggleSearch() {
	ls := m.activeListState()
	if ls.filterActive {
		ls.deactivateFilter()
	} else {
		ls.activateFilter()
	}
	m.viewDirty = true
}

// ScrollUp scrolls the active tab's list up by one line.
// Returns true if scroll was consumed (not at boundary).
func (m *Model) ScrollUp() bool {
	ls := m.activeListState()
	if len(ls.filtered) == 0 || ls.scrollOff <= 0 {
		return false
	}
	ls.scrollOff--
	m.viewDirty = true
	return true
}

// ScrollDown scrolls the active tab's list down by one line.
// Returns true if scroll was consumed (not at boundary).
func (m *Model) ScrollDown() bool {
	ls := m.activeListState()
	n := len(ls.filtered)
	if n == 0 {
		return false
	}
	// Estimate visible height: total height minus chrome (tab bar + divider + search).
	// Use scrollOff+1 and check against entry count.
	if ls.scrollOff >= n-1 {
		return false
	}
	ls.scrollOff++
	m.viewDirty = true
	return true
}

// activeListState returns a pointer to the listState of the currently active
// tab.
func (m *Model) activeListState() *listState {
	switch m.activeTab {
	case TabBranches:
		return &m.branches.listState
	case TabTags:
		return &m.tags.listState
	case TabUncommitted:
		return &m.uncommitted.listState
	default:
		return &m.commits.listState
	}
}

// -------------------------------------------------------------------------
// View
// -------------------------------------------------------------------------

// View renders the git explorer panel.
func (m *Model) View(cursorVisible bool) string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	var lines []string
	ls := m.activeListState()
	divider := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Border).
		Render(strings.Repeat("\u2500", m.width))

	// 1. Tab bar + divider.
	lines = append(lines, renderTabBar(m.activeTab, m.width, m.theme))
	lines = append(lines, divider)

	// 2. Search bar at top; toggles at bottom (matching file tree layout).
	var bottomLines []string
	if ls.filterActive {
		lines = append(lines, renderFilterBar(ls, m.width, m.theme, cursorVisible))
		lines = append(lines, divider)
		bottomLines = append(bottomLines, divider)
		bottomLines = append(bottomLines, renderFilterToggles(ls, m.width, m.theme))
		bottomLines = append(bottomLines, renderSortToggles(ls, m.width, m.theme))
	} else {
		lines = append(lines, renderSearchHint(m.width, m.theme))
		lines = append(lines, divider)
	}

	// 3. Options bar for uncommitted tab (always visible).
	var optionsLines []string
	if m.activeTab == TabUncommitted {
		optionsLines = append(optionsLines, divider)
		optionsLines = append(optionsLines, renderOptionsBar(&m.uncommitted, m.width, m.theme))
	}

	// 4. Entry list fills remaining height.
	entryHeight := max(m.height-len(lines)-len(bottomLines)-len(optionsLines), 0)
	entryLines := m.renderEntries(ls, entryHeight)
	lines = append(lines, entryLines...)
	lines = append(lines, optionsLines...)
	lines = append(lines, bottomLines...)

	return strings.Join(lines, "\n")
}

// renderEntries renders the visible portion of the active tab's entry list.
func (m *Model) renderEntries(ls *listState, height int) []string {
	start, end := ls.visibleEntries(height)
	lines := make([]string, 0, height)

	for i := start; i < end; i++ {
		idx := ls.filtered[i]
		selected := i == ls.cursor
		line := m.renderSingleEntry(ls.entries[idx], idx, selected)
		lines = append(lines, line)
	}

	// Fill remaining lines with empty padded rows.
	for len(lines) < height {
		lines = append(lines, padToWidth("", m.width, m.theme.Palette))
	}

	return lines
}

// renderSingleEntry dispatches to the correct tab-specific renderer.
func (m *Model) renderSingleEntry(entry listEntry, entryIdx int, selected bool) string {
	switch e := entry.(type) {
	case commitEntry:
		return renderCommitEntry(e, selected, m.width, m.theme, m.commits.colWidths)
	case branchEntry:
		dirtyTree := m.headHasUncommitted() || m.headHasConflicts()
		return renderBranchEntry(e, selected, m.width, m.theme, m.branches.colWidths, dirtyTree)
	case tagEntry:
		return renderTagEntry(e, selected, m.width, m.theme, m.tags.colWidths)
	case uncommittedEntry:
		staging := StagingDefault
		if entryIdx < len(m.uncommitted.stagingStates) {
			staging = m.uncommitted.stagingStates[entryIdx]
		}
		return renderUncommittedEntry(e, staging, selected, m.width, m.theme)
	default:
		return padToWidth("", m.width, m.theme.Palette)
	}
}
