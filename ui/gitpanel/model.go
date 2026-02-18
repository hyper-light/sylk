package gitpanel

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
)

// listPageSize is the number of entries delivered per page across all tabs.
const listPageSize = 50

// spinnerFramePeriod is the time between spinner frame changes (80ms gives
// a smooth animation at the decor tick's 100ms interval).
const spinnerFramePeriod = 80 * time.Millisecond

// spinnerFrames are the braille spinner glyphs used in the loading bar.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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

// CommitCheckedOutMsg is emitted after a successful commit checkout (detached HEAD).
type CommitCheckedOutMsg struct{ Hash string }

// StashRequestMsg is emitted when the user triggers a stash from the options bar.
type StashRequestMsg struct {
	Paths []string
	Count int
}

// UnstashRequestMsg is emitted when the user triggers an unstash.
type UnstashRequestMsg struct{}

// -------------------------------------------------------------------------
// Async load messages
// -------------------------------------------------------------------------

type commitsLoadedMsg struct {
	entries []commitEntry
	hasMore bool
}
type branchesLoadedMsg struct{ allEntries []branchEntry }
type tagsLoadedMsg struct{ allEntries []tagEntry }
type uncommittedLoadedMsg struct {
	allEntries []uncommittedEntry
	err        error // non-nil when the status read failed; entries should be kept as-is.
}
type checkoutResultMsg struct {
	name string
	err  error
}

type commitCheckoutResultMsg struct {
	hash string
	err  error
}

// "Load more" messages — returned by async page-fetch commands.
type commitsMoreMsg struct {
	entries []commitEntry
	hasMore bool
}
type branchesMoreMsg struct {
	entries []branchEntry
	hasMore bool
}
type tagsMoreMsg struct {
	entries []tagEntry
	hasMore bool
}
type uncommittedMoreMsg struct {
	entries []uncommittedEntry
	hasMore bool
}

// Backward-load messages — returned when scrolling up past the window.
type commitsPrevMsg struct{ entries []commitEntry }
type branchesPrevMsg struct{ entries []branchEntry }
type tagsPrevMsg struct{ entries []tagEntry }
type uncommittedPrevMsg struct{ entries []uncommittedEntry }

// Stat load messages — returned after lazy stat computation completes.
type commitStatsLoadedMsg struct {
	lines map[string]git.DiffSummary  // non-nil for DiffStatLines
	files map[string]git.FileSummary  // non-nil for DiffStatFiles
	level git.DiffStatLevel
}
type branchStatsLoadedMsg struct {
	lines map[string]git.DiffSummary
	files map[string]git.FileSummary
	level git.DiffStatLevel
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

	theme      *theme.Theme
	gitBus     *git.GitBus
	configPath string
	viewDirty  bool

	// pendingCmd stores a command produced by scroll-triggered pagination
	// that must be drained by the parent in handleTick.
	pendingCmd tea.Cmd

	commits     commitsTab
	branches    branchesTab
	tags        tagsTab
	uncommitted uncommittedTab
}

// New creates a new git panel model. configPath is the path to the YAML
// config file for persisting column layout.
func New(th *theme.Theme, bus *git.GitBus, configPath string) *Model {
	m := &Model{
		theme:      th,
		gitBus:     bus,
		configPath: configPath,
		viewDirty:  true,
	}

	cfg := LoadColumnConfig(configPath)

	// Initialize column layouts per tab.
	m.commits.columns = initColumns(commitColumnDefs(), cfg.Commits)
	m.branches.columns = initColumns(branchColumnDefs(), cfg.Branches)
	m.tags.columns = initColumns(tagColumnDefs(), cfg.Tags)
	m.uncommitted.columns = initColumns(uncommittedColumnDefs(), cfg.Uncommitted)

	// Initialize header drag state.
	m.commits.header = newHeaderState()
	m.branches.header = newHeaderState()
	m.tags.header = newHeaderState()
	m.uncommitted.header = newHeaderState()

	return m
}

// initColumns creates a ColumnLayout from definitions and optionally applies
// a persisted config.
func initColumns(defs []*ColumnDef, tabCfg *TabColumnConfig) *ColumnLayout {
	cl := NewColumnLayout(defs)
	if tabCfg != nil {
		cl.ApplyConfig(*tabCfg)
	}
	return &cl
}

// -------------------------------------------------------------------------
// component.Focusable
// -------------------------------------------------------------------------

func (m *Model) ID() component.FocusID { return component.FocusGitPanel }
func (m *Model) Focused() bool          { return m.focused }
func (m *Model) SetFocused(f bool)      { m.focused = f; m.viewDirty = true }

// ActiveTab returns the currently selected tab.
func (m *Model) ActiveTab() GitTab { return m.activeTab }

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
		m.setCommitEntries(typed.entries, typed.hasMore)
		return m, nil

	case branchesLoadedMsg:
		m.viewDirty = true
		m.setBranchEntries(typed.allEntries)
		return m, nil

	case tagsLoadedMsg:
		m.viewDirty = true
		m.setTagEntries(typed.allEntries)
		return m, nil

	case uncommittedLoadedMsg:
		if typed.err != nil {
			// Transient error (e.g. index locked during a git operation).
			// Keep existing entries to avoid a visible flash.
			return m, nil
		}
		m.viewDirty = true
		m.setUncommittedEntries(typed.allEntries)
		return m, nil

	case commitsMoreMsg:
		m.viewDirty = true
		m.appendCommitEntries(typed.entries, typed.hasMore)
		return m, nil

	case branchesMoreMsg:
		m.viewDirty = true
		m.appendBranchEntries(typed.entries, typed.hasMore)
		return m, nil

	case tagsMoreMsg:
		m.viewDirty = true
		m.appendTagEntries(typed.entries, typed.hasMore)
		return m, nil

	case uncommittedMoreMsg:
		m.viewDirty = true
		m.appendUncommittedEntries(typed.entries, typed.hasMore)
		return m, nil

	case commitsPrevMsg:
		m.viewDirty = true
		m.prependCommitEntries(typed.entries)
		return m, nil

	case branchesPrevMsg:
		m.viewDirty = true
		m.prependBranchEntries(typed.entries)
		return m, nil

	case tagsPrevMsg:
		m.viewDirty = true
		m.prependTagEntries(typed.entries)
		return m, nil

	case uncommittedPrevMsg:
		m.viewDirty = true
		m.prependUncommittedEntries(typed.entries)
		return m, nil

	case commitStatsLoadedMsg:
		m.viewDirty = true
		m.handleCommitStatsLoaded(typed)
		return m, nil

	case branchStatsLoadedMsg:
		m.viewDirty = true
		m.handleBranchStatsLoaded(typed)
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

	case commitCheckoutResultMsg:
		if typed.err != nil {
			return m, func() tea.Msg {
				return BranchCheckoutBlockedMsg{Reason: typed.err.Error()}
			}
		}
		return m, func() tea.Msg {
			return CommitCheckedOutMsg{Hash: typed.hash}
		}
	}

	return m, nil
}

// -------------------------------------------------------------------------
// Data loading
// -------------------------------------------------------------------------

// LoadData returns a tea.Cmd that loads the first page of commits and
// full data for branches, tags, and uncommitted files asynchronously.
// Only shows the loading spinner for tabs with no existing entries;
// tabs with data are refreshed silently to prevent flicker from
// redundant loads (e.g. GitStatusMsg arriving after an explicit reload).
func (m *Model) LoadData() tea.Cmd {
	now := time.Now()
	setLoading := func(ls *listState) {
		if len(ls.entries) == 0 && !ls.loadedOnce {
			ls.initialLoading = true
		}
		ls.loadingEpoch = now
	}
	setLoading(&m.commits.listState)
	setLoading(&m.branches.listState)
	setLoading(&m.tags.listState)
	setLoading(&m.uncommitted.listState)

	gc := m.gitBus
	commitStatLevel := m.commits.columns.StatLevel()
	branchStatLevel := m.branches.columns.StatLevel()
	m.commits.statsLevel = commitStatLevel
	m.branches.statsLevel = branchStatLevel
	return tea.Batch(
		func() tea.Msg {
			defaultTipHash := defaultBranchTipHash(gc)
			entries, hasMore := loadCommitsPage(gc, defaultTipHash, 0, listPageSize, commitStatLevel)
			return commitsLoadedMsg{entries: entries, hasMore: hasMore}
		},
		func() tea.Msg { return branchesLoadedMsg{allEntries: loadBranches(gc, branchStatLevel)} },
		func() tea.Msg { return tagsLoadedMsg{allEntries: loadTags(gc)} },
		func() tea.Msg {
			entries, err := loadUncommitted(gc)
			return uncommittedLoadedMsg{allEntries: entries, err: err}
		},
	)
}

// defaultBranchTipHash returns the full commit hash of the default branch tip,
// or an empty string if unavailable. Uses O(1) reference lookup.
func defaultBranchTipHash(gc *git.GitBus) string {
	name := gc.DefaultBranch()
	if name == "" {
		return ""
	}
	hash, err := gc.BranchTipHash(name)
	if err != nil {
		return ""
	}
	return hash
}

// setCommitEntries populates the commits tab with the first page of data.
func (m *Model) setCommitEntries(raw []commitEntry, hasMore bool) {
	ls := &m.commits.listState
	ls.initialLoading = false
	ls.loadedOnce = true
	ls.resetPagination()
	ls.entries = make([]listEntry, len(raw))
	for i, e := range raw {
		ls.entries[i] = e
	}
	ls.hasMore = hasMore
	ls.rebuildFiltered()
}

// appendCommitEntries appends a page of commits to the existing list,
// evicting the oldest page if the window exceeds 2 pages.
func (m *Model) appendCommitEntries(raw []commitEntry, hasMore bool) {
	ls := &m.commits.listState
	ls.loadingMore = false
	for _, e := range raw {
		ls.entries = append(ls.entries, e)
	}
	ls.hasMore = hasMore
	ls.evictOldEntries()
	ls.rebuildFiltered()
}

// setBranchEntries caches all branches and delivers the first page.
func (m *Model) setBranchEntries(all []branchEntry) {
	m.branches.allEntries = all
	m.branches.dirtyTree = branchesDirtyTree(all)
	ls := &m.branches.listState
	ls.initialLoading = false
	ls.loadedOnce = true
	ls.resetPagination()

	page := sliceBranchPage(all, 0, listPageSize)
	ls.entries = make([]listEntry, len(page))
	for i, e := range page {
		ls.entries[i] = e
	}
	ls.hasMore = len(all) > listPageSize
	ls.rebuildFiltered()
}

// branchesDirtyTree reports whether the HEAD branch has uncommitted changes
// or merge conflicts.
func branchesDirtyTree(entries []branchEntry) bool {
	for _, e := range entries {
		if e.isHead {
			return e.uncommitted || e.hasConflicts
		}
	}
	return false
}

// appendBranchEntries appends a page of branches to the existing list,
// evicting the oldest page if the window exceeds 2 pages.
func (m *Model) appendBranchEntries(raw []branchEntry, hasMore bool) {
	ls := &m.branches.listState
	ls.loadingMore = false
	for _, e := range raw {
		ls.entries = append(ls.entries, e)
	}
	ls.hasMore = hasMore
	ls.evictOldEntries()
	ls.rebuildFiltered()
}

// setTagEntries caches all tags and delivers the first page.
func (m *Model) setTagEntries(all []tagEntry) {
	m.tags.allEntries = all
	ls := &m.tags.listState
	ls.initialLoading = false
	ls.loadedOnce = true
	ls.resetPagination()

	page := sliceTagPage(all, 0, listPageSize)
	ls.entries = make([]listEntry, len(page))
	for i, e := range page {
		ls.entries[i] = e
	}
	ls.hasMore = len(all) > listPageSize
	ls.rebuildFiltered()
}

// appendTagEntries appends a page of tags to the existing list,
// evicting the oldest page if the window exceeds 2 pages.
func (m *Model) appendTagEntries(raw []tagEntry, hasMore bool) {
	ls := &m.tags.listState
	ls.loadingMore = false
	for _, e := range raw {
		ls.entries = append(ls.entries, e)
	}
	ls.hasMore = hasMore
	ls.evictOldEntries()
	ls.rebuildFiltered()
}

// setUncommittedEntries caches all uncommitted entries, preserves staging
// states for files that are still present, and delivers the first page.
func (m *Model) setUncommittedEntries(all []uncommittedEntry) {
	// Build lookup from old staging states so we can restore them.
	prev := make(map[string]StagingState, len(m.uncommitted.allStaging))
	for i, s := range m.uncommitted.allStaging {
		if s == StagingDefault || i >= len(m.uncommitted.allEntries) {
			continue
		}
		prev[m.uncommitted.allEntries[i].path] = s
	}

	// Rebuild allEntries + allStaging with preserved states.
	m.uncommitted.allEntries = all
	m.uncommitted.allStaging = make([]StagingState, len(all))
	for i, e := range all {
		if s, ok := prev[e.path]; ok {
			m.uncommitted.allStaging[i] = s
		}
	}

	ls := &m.uncommitted.listState
	ls.initialLoading = false
	ls.loadedOnce = true
	ls.resetPagination()

	page := sliceUncommittedPage(all, 0, listPageSize)
	ls.entries = make([]listEntry, len(page))
	states := make([]StagingState, len(page))
	for i, e := range page {
		e.staging = m.uncommitted.allStaging[i]
		ls.entries[i] = e
		states[i] = m.uncommitted.allStaging[i]
	}
	m.uncommitted.stagingStates = states
	ls.hasMore = len(all) > listPageSize
	ls.rebuildFiltered()
}

// appendUncommittedEntries appends a page of uncommitted entries with their
// staging states, evicting the oldest page if the window exceeds 2 pages.
func (m *Model) appendUncommittedEntries(raw []uncommittedEntry, hasMore bool) {
	ls := &m.uncommitted.listState
	ls.loadingMore = false
	offset := ls.windowStart + len(ls.entries) // absolute offset
	for i, e := range raw {
		staging := StagingDefault
		if offset+i < len(m.uncommitted.allStaging) {
			staging = m.uncommitted.allStaging[offset+i]
		}
		e.staging = staging
		ls.entries = append(ls.entries, e)
		m.uncommitted.stagingStates = append(m.uncommitted.stagingStates, staging)
	}
	ls.hasMore = hasMore

	// Evict oldest page; also trim parallel staging states.
	if len(ls.entries) > maxWindowEntries {
		cursorAbs := ls.cursorAbsoluteIdx()
		evicted := len(ls.entries) - maxWindowEntries
		remaining := make([]listEntry, maxWindowEntries)
		copy(remaining, ls.entries[evicted:])
		ls.entries = remaining
		ls.windowStart += evicted

		if evicted <= len(m.uncommitted.stagingStates) {
			m.uncommitted.stagingStates = m.uncommitted.stagingStates[evicted:]
		}

		ls.rebuildFiltered()
		ls.restoreCursorAbsolute(cursorAbs)
	} else {
		ls.rebuildFiltered()
	}
}

// -------------------------------------------------------------------------
// Backward-page prepend methods
// -------------------------------------------------------------------------

// prependCommitEntries prepends a backward page of commits to the start of
// the entry window, evicting newest entries if the window exceeds 2 pages.
func (m *Model) prependCommitEntries(raw []commitEntry) {
	ls := &m.commits.listState
	ls.loadingPrev = false
	prepended := len(raw)
	newEntries := make([]listEntry, 0, prepended+len(ls.entries))
	for _, e := range raw {
		newEntries = append(newEntries, e)
	}
	newEntries = append(newEntries, ls.entries...)
	ls.entries = newEntries
	ls.windowStart -= prepended
	ls.cursor += prepended
	ls.scrollOff += prepended
	ls.evictNewEntries()
	ls.rebuildFiltered()
}

// prependBranchEntries prepends a backward page of branches.
func (m *Model) prependBranchEntries(raw []branchEntry) {
	ls := &m.branches.listState
	ls.loadingPrev = false
	prepended := len(raw)
	newEntries := make([]listEntry, 0, prepended+len(ls.entries))
	for _, e := range raw {
		newEntries = append(newEntries, e)
	}
	newEntries = append(newEntries, ls.entries...)
	ls.entries = newEntries
	ls.windowStart -= prepended
	ls.cursor += prepended
	ls.scrollOff += prepended
	ls.evictNewEntries()
	ls.rebuildFiltered()
}

// prependTagEntries prepends a backward page of tags.
func (m *Model) prependTagEntries(raw []tagEntry) {
	ls := &m.tags.listState
	ls.loadingPrev = false
	prepended := len(raw)
	newEntries := make([]listEntry, 0, prepended+len(ls.entries))
	for _, e := range raw {
		newEntries = append(newEntries, e)
	}
	newEntries = append(newEntries, ls.entries...)
	ls.entries = newEntries
	ls.windowStart -= prepended
	ls.cursor += prepended
	ls.scrollOff += prepended
	ls.evictNewEntries()
	ls.rebuildFiltered()
}

// prependUncommittedEntries prepends a backward page of uncommitted entries.
func (m *Model) prependUncommittedEntries(raw []uncommittedEntry) {
	ls := &m.uncommitted.listState
	ls.loadingPrev = false
	prepended := len(raw)
	newEntries := make([]listEntry, 0, prepended+len(ls.entries))
	for _, e := range raw {
		newEntries = append(newEntries, e)
	}
	newEntries = append(newEntries, ls.entries...)
	ls.entries = newEntries
	ls.windowStart -= prepended
	ls.cursor += prepended
	ls.scrollOff += prepended

	// Rebuild staging states for the new window and sync entry staging fields.
	newStaging := make([]StagingState, len(ls.entries))
	for i := range ls.entries {
		absIdx := ls.windowStart + i
		if absIdx >= 0 && absIdx < len(m.uncommitted.allStaging) {
			newStaging[i] = m.uncommitted.allStaging[absIdx]
		}
		if ue, ok := ls.entries[i].(uncommittedEntry); ok {
			ue.staging = newStaging[i]
			ls.entries[i] = ue
		}
	}
	m.uncommitted.stagingStates = newStaging

	ls.evictNewEntries()
	ls.rebuildFiltered()
}

// -------------------------------------------------------------------------
// Page slicers
// -------------------------------------------------------------------------

func sliceBranchPage(all []branchEntry, offset, pageSize int) []branchEntry {
	if offset >= len(all) {
		return nil
	}
	end := min(offset+pageSize, len(all))
	return all[offset:end]
}

func sliceTagPage(all []tagEntry, offset, pageSize int) []tagEntry {
	if offset >= len(all) {
		return nil
	}
	end := min(offset+pageSize, len(all))
	return all[offset:end]
}

func sliceUncommittedPage(all []uncommittedEntry, offset, pageSize int) []uncommittedEntry {
	if offset >= len(all) {
		return nil
	}
	end := min(offset+pageSize, len(all))
	return all[offset:end]
}

// -------------------------------------------------------------------------
// ViewDirty / DrainCmd
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

// DrainCmd returns and clears any pending command produced by scroll-triggered
// pagination. Called by the parent in handleTick.
func (m *Model) DrainCmd() tea.Cmd {
	cmd := m.pendingCmd
	m.pendingCmd = nil
	return cmd
}

// -------------------------------------------------------------------------
// Key handling
// -------------------------------------------------------------------------

func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if !m.focused {
		return nil
	}

	ls := m.activeListState()

	if ls.filterActive || ls.filterFocus == focusHeader || ls.filterFocus == focusGearDropdown {
		prevFocus := ls.filterFocus
		if ls.handleFilterKey(key) {
			m.saveConfigIfNeeded()
			// Header Tab exit on uncommitted tab → route to options bar.
			if m.activeTab == TabUncommitted && prevFocus == focusHeader &&
				ls.filterFocus == focusEntries && key.String() == "tab" {
				ls.filterFocus = focusEntries
				m.uncommitted.optionFocus = optionAll
			}
			// Gear dropdown toggle may have changed visible stat columns.
			if prevFocus == focusGearDropdown {
				if cmd := m.maybeLoadStats(); cmd != nil {
					return cmd
				}
			}
			return nil
		}
	}

	return m.handleNavigationKey(key)
}

// handleNavigationKey processes keys when the filter is NOT active (or was
// not consumed by the filter handler).
func (m *Model) handleNavigationKey(key tea.KeyMsg) tea.Cmd {
	// Handle uncommitted options bar focus.
	if m.activeTab == TabUncommitted && m.uncommitted.optionFocus != optionNone {
		return m.handleOptionsKey(key)
	}

	ls := m.activeListState()

	switch key.String() {
	case "shift+tab", "right":
		m.CycleTabForward()
	case "left":
		m.CycleTabBackward()
	case "tab":
		ls.filterFocus = focusHeader
		ls.header.cursor = firstHeaderCol(ls.columns.VisibleColumns())
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

	if cmd := m.maybeLoadMore(); cmd != nil {
		return cmd
	}

	return nil
}

// handleOptionsKey processes keys when focus is on the uncommitted
// options bar ([All] / [Stash] / [Unstash] badges).
func (m *Model) handleOptionsKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "enter", " ":
		switch m.uncommitted.optionFocus {
		case optionAll:
			m.uncommitted.toggleAll()
		case optionStash:
			return m.triggerStash()
		case optionUnstash:
			return m.triggerUnstash()
		}
	case "l", "right", "tab":
		m.uncommitted.optionFocus = m.nextOptionBadge(m.uncommitted.optionFocus)
	case "h", "left", "shift+tab":
		m.uncommitted.optionFocus = m.prevOptionBadge(m.uncommitted.optionFocus)
	case "esc", "k", "up":
		m.uncommitted.optionFocus = optionNone
		m.uncommitted.moveUp(0) // keep cursor in place
	case "j", "down":
		m.uncommitted.optionFocus = optionNone
	}
	return nil
}

// nextOptionBadge returns the next badge in the options bar, wrapping to
// optionNone (exit). The sequence is All → Stash → Unstash (if visible) → exit.
func (m *Model) nextOptionBadge(cur optionFocus) optionFocus {
	switch cur {
	case optionAll:
		return optionStash
	case optionStash:
		if m.uncommitted.hasStash {
			return optionUnstash
		}
		return optionNone
	default:
		return optionNone
	}
}

// prevOptionBadge returns the previous badge, wrapping to optionNone (exit).
func (m *Model) prevOptionBadge(cur optionFocus) optionFocus {
	switch cur {
	case optionUnstash:
		return optionStash
	case optionStash:
		return optionAll
	default:
		return optionNone
	}
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
	next := (m.uncommitted.stagingStates[idx] + 1) % stagingStateCount
	m.uncommitted.stagingStates[idx] = next

	// Sync the entry's staging field for sort consistency.
	if ue, ok := ls.entries[idx].(uncommittedEntry); ok {
		ue.staging = next
		ls.entries[idx] = ue
	}

	// Propagate to allStaging so the state survives window eviction.
	absIdx := ls.windowStart + idx
	if absIdx >= 0 && absIdx < len(m.uncommitted.allStaging) {
		m.uncommitted.allStaging[absIdx] = next
	}
}

// CycleTabForward advances to the next tab, wrapping around.
func (m *Model) CycleTabForward() {
	m.activeTab = GitTab((int(m.activeTab) + 1) % int(tabCount))
	m.viewDirty = true
}

// CycleTabBackward moves to the previous tab, wrapping around.
func (m *Model) CycleTabBackward() {
	m.activeTab = GitTab((int(m.activeTab) + int(tabCount) - 1) % int(tabCount))
	m.viewDirty = true
}

// SetActiveTab switches to the given tab if it is valid.
func (m *Model) SetActiveTab(tab GitTab) {
	if tab >= 0 && tab < tabCount {
		m.activeTab = tab
		m.viewDirty = true
	}
}

// FocusUncommittedOptions moves focus to the [All] options bar on the
// Uncommitted tab. No-op if the tab is not active.
func (m *Model) FocusUncommittedOptions() {
	if m.activeTab == TabUncommitted {
		m.uncommitted.optionFocus = optionAll
		m.viewDirty = true
	}
}

// ToggleUncommittedAll focuses the [All] options bar and toggles the
// staging state of all entries. No-op if the tab is not active.
func (m *Model) ToggleUncommittedAll() {
	if m.activeTab == TabUncommitted {
		m.uncommitted.optionFocus = optionAll
		m.uncommitted.toggleAll()
		m.viewDirty = true
	}
}

// selectEntry handles enter on the currently selected entry.
func (m *Model) selectEntry() tea.Cmd {
	switch m.activeTab {
	case TabBranches:
		return m.checkoutSelectedBranch()
	case TabCommits:
		return m.checkoutSelectedCommit()
	}
	return nil
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

	gc := m.gitBus
	name := entry.name
	return func() tea.Msg {
		return checkoutResultMsg{name: name, err: gc.CheckoutBranch(name)}
	}
}

// checkoutSelectedCommit attempts to checkout the selected commit (detached HEAD).
func (m *Model) checkoutSelectedCommit() tea.Cmd {
	ls := &m.commits.listState
	if len(ls.filtered) == 0 {
		return nil
	}

	idx := ls.filtered[ls.cursor]
	entry, ok := ls.entries[idx].(commitEntry)
	if !ok {
		return nil
	}

	if entry.isHead {
		return nil // already at this commit
	}

	if m.headHasConflicts() {
		return func() tea.Msg {
			return BranchCheckoutBlockedMsg{Reason: "resolve conflicts before checking out"}
		}
	}
	if m.headHasUncommitted() {
		return func() tea.Msg {
			return BranchCheckoutBlockedMsg{Reason: "commit or stash changes before checking out"}
		}
	}

	gc := m.gitBus
	hash := entry.hash
	return func() tea.Msg {
		return commitCheckoutResultMsg{hash: hash, err: gc.CheckoutCommit(hash)}
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
// Checks allStaging (full set) so files outside the current window are included.
func (m *Model) HasAnyStagedFiles() bool {
	for _, s := range m.uncommitted.allStaging {
		if s == StagingStaged {
			return true
		}
	}
	return false
}

// StagedFilePaths returns the paths of all uncommitted files marked StagingStaged.
// Checks allStaging/allEntries (full set) so files outside the current window
// are included.
func (m *Model) StagedFilePaths() []string {
	var paths []string
	for i, s := range m.uncommitted.allStaging {
		if s != StagingStaged {
			continue
		}
		if i < len(m.uncommitted.allEntries) {
			paths = append(paths, m.uncommitted.allEntries[i].path)
		}
	}
	return paths
}

// allUncommittedPaths returns the paths of all uncommitted files.
func (m *Model) allUncommittedPaths() []string {
	paths := make([]string, len(m.uncommitted.allEntries))
	for i, e := range m.uncommitted.allEntries {
		paths[i] = e.path
	}
	return paths
}

// triggerStash collects staged (or all) paths and emits a StashRequestMsg.
func (m *Model) triggerStash() tea.Cmd {
	paths := m.StagedFilePaths()
	if len(paths) == 0 {
		paths = m.allUncommittedPaths()
	}
	if len(paths) == 0 {
		return nil
	}
	count := len(paths)
	return func() tea.Msg {
		return StashRequestMsg{Paths: paths, Count: count}
	}
}

// triggerUnstash emits an UnstashRequestMsg.
func (m *Model) triggerUnstash() tea.Cmd {
	return func() tea.Msg { return UnstashRequestMsg{} }
}

// StashAll stages all files and triggers a stash. Intended for the
// alt+shift+u+s keybind from the app layer.
func (m *Model) StashAll() tea.Cmd {
	if m.activeTab != TabUncommitted {
		return nil
	}
	if !m.uncommitted.allStaged() {
		m.uncommitted.toggleAll()
	}
	m.uncommitted.optionFocus = optionStash
	m.viewDirty = true
	return m.triggerStash()
}

// TriggerUnstash triggers an unstash from the app layer (alt+shift+u+p keybind).
func (m *Model) TriggerUnstash() tea.Cmd {
	if m.activeTab != TabUncommitted {
		return nil
	}
	if !m.uncommitted.hasStash {
		return nil
	}
	m.uncommitted.optionFocus = optionUnstash
	m.viewDirty = true
	return m.triggerUnstash()
}

// SetHasStash updates whether git has stashed changes, controlling the
// [Unstash] badge visibility.
func (m *Model) SetHasStash(has bool) {
	if m.uncommitted.hasStash == has {
		return
	}
	m.uncommitted.hasStash = has
	// If unstash badge disappears while focused, move focus to stash.
	if !has && m.uncommitted.optionFocus == optionUnstash {
		m.uncommitted.optionFocus = optionStash
	}
	m.viewDirty = true
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
	if len(ls.filtered) == 0 {
		return false
	}

	// At the top of the loaded window: trigger backward page fetch.
	if ls.scrollOff <= 0 && ls.needsLoadPrev() {
		ls.loadingPrev = true
		ls.loadingEpoch = time.Now()
		ls.loadingBarUntil = time.Now().Add(minListLoadingBarDuration)
		m.pendingCmd = m.loadPrevCmd()
		m.viewDirty = true
		return true
	}

	if ls.scrollOff <= 0 {
		return false
	}
	ls.scrollOff--
	// Keep cursor within the visible viewport so visibleEntries() doesn't
	// reset scrollOff back to the cursor position.
	h := m.entryHeight()
	if h > 0 && ls.cursor >= ls.scrollOff+h {
		ls.cursor = ls.scrollOff + h - 1
	}
	m.viewDirty = true
	return true
}

// ScrollDown scrolls the active tab's list down by one line.
// Returns true if scroll was consumed (not at boundary).
func (m *Model) ScrollDown() bool {
	ls := m.activeListState()
	n := len(ls.filtered)
	if n == 0 || ls.scrollOff >= n-1 {
		return false
	}
	ls.scrollOff++
	// Keep cursor within the visible viewport so visibleEntries() doesn't
	// reset scrollOff back to the cursor position.
	if ls.cursor < ls.scrollOff {
		ls.cursor = ls.scrollOff
	}
	m.viewDirty = true

	// Scroll-triggered pagination: store command for parent to drain.
	h := m.entryHeight()
	if ls.needsScrollLoadMore(h) {
		ls.loadingMore = true
		ls.loadingEpoch = time.Now()
		ls.loadingBarUntil = time.Now().Add(minListLoadingBarDuration)
		m.pendingCmd = m.loadMoreCmd()
	}
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
// Mouse click handling
// -------------------------------------------------------------------------

// chromeTopHeight is the number of lines above the entry list:
// tab bar(1) + divider(1) + search hint/filter(1) + divider(1) + header(1) + header divider(1).
const chromeTopHeight = 6

// ClickAt handles a left-click at the given content-relative coordinates.
// viewX is the column offset within the panel content area; viewY is the
// row offset (0 = first line inside the panel border).
func (m *Model) ClickAt(viewX, viewY int) tea.Cmd {
	// Close gear dropdown on any click outside its area.
	// The dropdown is handled inside clickEntry and clickHeader (gear icon);
	// all other click targets close it.
	ls := m.activeListState()
	if ls.header.gearOpen {
		isGearIcon := (viewY == chromeTopHeight-2 || viewY == chromeTopHeight-1) &&
			clickHeaderColumn(ls, viewX, m.width) == -1
		isDropdownArea := viewY >= chromeTopHeight && ls.columns != nil &&
			viewX >= max(m.width-gearDropdownWidth(ls.columns)-1, 0) &&
			viewY-chromeTopHeight < len(ls.columns.dropdownIndices())+2
		if !isGearIcon && !isDropdownArea {
			ls.header.gearOpen = false
			ls.filterFocus = focusEntries
			m.viewDirty = true
			return nil
		}
	}

	if viewY == 0 {
		return m.clickTabBar(viewX)
	}

	// Header row is at chromeTopHeight-2 (header labels); the dashed
	// divider at chromeTopHeight-1 also routes to the header handler so
	// that divider-click resize works on both lines.
	if viewY == chromeTopHeight-2 || viewY == chromeTopHeight-1 {
		return m.clickHeader(viewX)
	}

	bottomCount := 0
	if ls.filterActive {
		bottomCount = 2 // divider + filter toggles
	}
	optionsCount := 0
	if m.activeTab == TabUncommitted {
		optionsCount = 2 // divider + options bar
	}
	entryHeight := max(m.height-chromeTopHeight-bottomCount-optionsCount, 0)

	// Options bar click (uncommitted tab only, below entries).
	// Layout: " [All] [Stash] [Unstash]"
	//          1    5 7     13 15      23
	if m.activeTab == TabUncommitted {
		if viewY == chromeTopHeight+entryHeight+1 { // +1 skips the divider
			m.viewDirty = true
			if m.uncommitted.hasStash && viewX >= 15 && viewX <= 23 {
				return m.triggerUnstash()
			}
			if viewX >= 7 && viewX <= 13 {
				return m.triggerStash()
			}
			m.uncommitted.toggleAll()
			return nil
		}
	}

	bodyY := viewY - chromeTopHeight
	if bodyY < 0 || bodyY >= entryHeight {
		return nil
	}
	return m.clickEntry(bodyY, viewX, entryHeight)
}

// clickTabBar maps a click X coordinate to a tab label and switches to it.
func (m *Model) clickTabBar(viewX int) tea.Cmd {
	x := 0
	const sepWidth = 3 // " │ "
	for i := range tabCount {
		labelWidth := len(" " + tabLabels[GitTab(i)] + " ")
		if viewX >= x && viewX < x+labelWidth {
			m.activeTab = GitTab(i)
			m.viewDirty = true
			return nil
		}
		x += labelWidth
		if int(i) < int(tabCount)-1 {
			x += sepWidth
		}
	}
	return nil
}

// clickHeader handles a click on the column header row.
func (m *Model) clickHeader(viewX int) tea.Cmd {
	ls := m.activeListState()
	col := clickHeaderColumn(ls, viewX, m.width)

	switch {
	case col == -1:
		// Gear icon clicked — toggle dropdown.
		if ls.header.gearOpen {
			ls.header.gearOpen = false
			ls.filterFocus = focusHeader
		} else {
			ls.header.gearOpen = true
			ls.header.gearCursor = 0
			ls.filterFocus = focusGearDropdown
		}
	case col <= -3:
		// Divider clicked — start resize drag. Snapshot all visible column
		// widths so the motion handler can redistribute directly.
		divIdx := -(col + 3)
		vis := ls.columns.VisibleColumns()
		if divIdx >= 0 && divIdx+1 < len(vis) {
			ls.header.resizing = true
			ls.header.hoverDivider = -1
			ls.header.resizeDivIdx = divIdx
			ls.header.resizeStartX = viewX
			ls.header.resizeSnapshot = make([]int, len(vis))
			ls.header.resizeVisMap = ls.columns.visToFullIndices()
			for i, c := range vis {
				ls.header.resizeSnapshot[i] = c.Width
			}
		}
	case col >= 0:
		// Column header pressed — start drag (reorder on release if moved,
		// or cycle sort if released on the same column).
		vis := ls.columns.VisibleColumns()
		if col < len(vis) {
			ls.header.dragFrom = col
			ls.header.dragX = viewX
			ls.header.cursor = col
			ls.filterFocus = focusHeader
		}
	}

	m.viewDirty = true
	return nil
}

// ClearHover resets the hover state when the mouse leaves the panel.
func (m *Model) ClearHover() {
	ls := m.activeListState()
	dirty := false
	if ls.header.hoverDivider != -1 {
		ls.header.hoverDivider = -1
		dirty = true
	}
	if ls.header.hoverColumn != -1 {
		ls.header.hoverColumn = -1
		dirty = true
	}
	if dirty {
		m.viewDirty = true
	}
}

// HandleMouseHover updates divider and column hover state from a mouse motion
// event. viewX and viewY are content-relative coordinates within the panel.
func (m *Model) HandleMouseHover(viewX, viewY int) {
	ls := m.activeListState()
	oldDiv := ls.header.hoverDivider
	oldCol := ls.header.hoverColumn
	ls.header.hoverDivider = -1
	ls.header.hoverColumn = -1

	// Header row is at chromeTopHeight-2; the dashed divider below it at
	// chromeTopHeight-1 also counts so hover feedback matches click targets.
	if viewY == chromeTopHeight-2 || viewY == chromeTopHeight-1 {
		col := clickHeaderColumn(ls, viewX, m.width)
		if col <= -3 {
			ls.header.hoverDivider = -(col + 3)
		} else if col >= 0 {
			ls.header.hoverColumn = col
		}
	}

	if ls.header.hoverDivider != oldDiv || ls.header.hoverColumn != oldCol {
		m.viewDirty = true
	}
}

// HandleMouseMotion processes mouse motion during drag operations.
func (m *Model) HandleMouseMotion(viewX int) {
	ls := m.activeListState()
	if ls.header.resizing {
		delta := viewX - ls.header.resizeStartX
		m.applyResizeDelta(ls, delta)
		m.viewDirty = true
		return
	}
	if ls.header.dragFrom >= 0 {
		ls.header.dragX = viewX
		m.viewDirty = true
	}
}

// applyResizeDelta redistributes column widths based on the drag delta.
// Drag RIGHT (delta > 0): vis[divIdx] grows, only vis[divIdx+1] shrinks.
// Drag LEFT  (delta < 0): vis[divIdx+1] grows, shrink vis[divIdx] then
// vis[divIdx-1], etc. until the delta is fully absorbed.
func (m *Model) applyResizeDelta(ls *listState, delta int) {
	snap := ls.header.resizeSnapshot
	visMap := ls.header.resizeVisMap
	divIdx := ls.header.resizeDivIdx
	cols := &ls.columns.Columns

	if len(snap) == 0 || divIdx+1 >= len(snap) {
		return
	}

	// Reset all visible columns to their snapshot widths.
	for i, sw := range snap {
		(*cols)[visMap[i]].Width = sw
	}

	vis := ls.columns.VisibleColumns()

	if delta > 0 {
		// Drag RIGHT: vis[divIdx] grows, only vis[divIdx+1] shrinks.
		rightMin := colMinWidth(vis[divIdx+1].Def)
		available := max(snap[divIdx+1]-rightMin, 0)
		take := min(available, delta)
		(*cols)[visMap[divIdx]].Width = snap[divIdx] + take
		(*cols)[visMap[divIdx+1]].Width = snap[divIdx+1] - take
	} else if delta < 0 {
		// Drag LEFT: vis[divIdx+1] grows, shrink leftward in order.
		need := -delta
		absorbed := 0
		for j := divIdx; j >= 0 && absorbed < need; j-- {
			leftMin := colMinWidth(vis[j].Def)
			available := max(snap[j]-leftMin, 0)
			take := min(available, need-absorbed)
			(*cols)[visMap[j]].Width = snap[j] - take
			absorbed += take
		}
		(*cols)[visMap[divIdx+1]].Width = snap[divIdx+1] + absorbed
	}
}

// HandleMouseRelease processes mouse release to end drag operations.
func (m *Model) HandleMouseRelease() {
	ls := m.activeListState()
	if ls.header.resizing {
		// Persist UserWidth on every column whose width changed.
		snap := ls.header.resizeSnapshot
		visMap := ls.header.resizeVisMap
		for i, sw := range snap {
			cur := ls.columns.Columns[visMap[i]].Width
			if cur != sw {
				ls.columns.Columns[visMap[i]].UserWidth = cur
			}
		}
		ls.header.resizing = false
		ls.header.resizeSnapshot = nil
		ls.header.resizeVisMap = nil
		m.saveConfig()
		m.viewDirty = true
		return
	}
	if ls.header.dragFrom >= 0 {
		// Determine drop target column by X position.
		dropCol := clickHeaderColumn(ls, ls.header.dragX, m.width)
		if dropCol >= 0 && dropCol != ls.header.dragFrom {
			// Dragged to a different column — reorder.
			m.reorderVisibleColumns(ls, ls.header.dragFrom, dropCol)
			m.saveConfig()
		} else if dropCol == ls.header.dragFrom {
			// Click without drag — cycle sort.
			vis := ls.columns.VisibleColumns()
			if ls.header.dragFrom < len(vis) {
				ls.columns.CycleSort(vis[ls.header.dragFrom].Def.ID)
				ls.rebuildFiltered()
				m.saveConfig()
			}
			ls.filterFocus = focusEntries
		}
		ls.header.dragFrom = -1
		ls.header.dragX = 0
		m.viewDirty = true
	}
}

// reorderVisibleColumns reorders columns by visible indices.
func (m *Model) reorderVisibleColumns(ls *listState, fromVis, toVis int) {
	vis := ls.columns.VisibleColumns()
	if fromVis >= len(vis) || toVis >= len(vis) {
		return
	}
	fromID := vis[fromVis].Def.ID
	toID := vis[toVis].Def.ID

	fromFull, toFull := -1, -1
	for i, c := range ls.columns.Columns {
		if c.Def.ID == fromID {
			fromFull = i
		}
		if c.Def.ID == toID {
			toFull = i
		}
	}
	if fromFull >= 0 && toFull >= 0 {
		ls.columns.Reorder(fromFull, toFull)
	}
}

// IsDragging reports whether any column drag operation is in progress.
func (m *Model) IsDragging() bool {
	ls := m.activeListState()
	return ls.header.resizing || ls.header.dragFrom >= 0
}

// CloseDropdown closes the gear dropdown if it is open.
func (m *Model) CloseDropdown() {
	ls := m.activeListState()
	if ls.header.gearOpen {
		ls.header.gearOpen = false
		ls.filterFocus = focusEntries
		m.viewDirty = true
	}
}

// clickEntry maps a body-relative Y coordinate to an entry and performs
// a tab-specific action.
func (m *Model) clickEntry(bodyY, viewX, entryHeight int) tea.Cmd {
	ls := m.activeListState()

	// If gear dropdown is open, check if click is within the dropdown area.
	if ls.header.gearOpen && ls.columns != nil {
		dropW := gearDropdownWidth(ls.columns)
		dropCol := max(m.width-dropW-1, 0)
		indices := ls.columns.dropdownIndices()
		nItems := len(indices)
		// Dropdown lines: row 0 = top border, rows 1..nItems = items, row nItems+1 = bottom border.
		dropHeight := nItems + 2
		if viewX >= dropCol && bodyY < dropHeight {
			itemIdx := bodyY - 1 // offset by top border row
			if itemIdx >= 0 && itemIdx < nItems {
				ls.header.gearCursor = itemIdx
				col := ls.columns.Columns[indices[itemIdx]]
				ls.columns.ToggleVisibility(col.Def.ID)
				ls.rebuildFiltered()
				m.saveConfig()
			}
			m.viewDirty = true
			return m.maybeLoadStats()
		}
		// Click outside dropdown — close it.
		ls.header.gearOpen = false
		ls.filterFocus = focusHeader
		m.viewDirty = true
		return nil
	}

	start, end := ls.visibleEntries(entryHeight)
	idx := start + bodyY
	if idx < start || idx >= end {
		return nil
	}
	ls.cursor = idx
	m.viewDirty = true

	switch m.activeTab {
	case TabUncommitted:
		m.toggleUncommittedStaging()
	case TabBranches:
		return m.selectEntry()
	}
	return nil
}

// -------------------------------------------------------------------------
// Lazy stat loading
// -------------------------------------------------------------------------

// handleCommitStatsLoaded merges loaded stats into existing commit entries.
func (m *Model) handleCommitStatsLoaded(msg commitStatsLoadedMsg) {
	m.commits.statsLoading = false
	m.commits.statsLevel = msg.level
	ls := &m.commits.listState
	for i, e := range ls.entries {
		ce, ok := e.(commitEntry)
		if !ok {
			continue
		}
		if ds, ok := msg.lines[ce.hash]; ok {
			ce.additions = ds.Additions
			ce.deletions = ds.Deletions
			ce.filesAdded = ds.FilesAdded
			ce.filesModified = ds.FilesModified
			ce.filesDeleted = ds.FilesDeleted
			ce.hasFileStats = true
			ce.hasLineStats = true
			ls.entries[i] = ce
		} else if fs, ok := msg.files[ce.hash]; ok {
			ce.filesAdded = fs.FilesAdded
			ce.filesModified = fs.FilesModified
			ce.filesDeleted = fs.FilesDeleted
			ce.hasFileStats = true
			ls.entries[i] = ce
		}
	}
}

// handleBranchStatsLoaded merges loaded stats into existing branch entries.
func (m *Model) handleBranchStatsLoaded(msg branchStatsLoadedMsg) {
	m.branches.statsLoading = false
	m.branches.statsLevel = msg.level
	applyBranchStats := func(entries []listEntry) {
		for i, e := range entries {
			be, ok := e.(branchEntry)
			if !ok {
				continue
			}
			if ds, ok := msg.lines[be.hash]; ok {
				be.additions = ds.Additions
				be.deletions = ds.Deletions
				be.filesAdded = ds.FilesAdded
				be.filesModified = ds.FilesModified
				be.filesDeleted = ds.FilesDeleted
				be.hasFileStats = true
				be.hasLineStats = true
				entries[i] = be
			} else if fs, ok := msg.files[be.hash]; ok {
				be.filesAdded = fs.FilesAdded
				be.filesModified = fs.FilesModified
				be.filesDeleted = fs.FilesDeleted
				be.hasFileStats = true
				entries[i] = be
			}
		}
	}
	applyBranchStats(m.branches.listState.entries)
	// Also update allEntries so subsequent pages have stats.
	for i := range m.branches.allEntries {
		be := &m.branches.allEntries[i]
		if ds, ok := msg.lines[be.hash]; ok {
			be.additions = ds.Additions
			be.deletions = ds.Deletions
			be.filesAdded = ds.FilesAdded
			be.filesModified = ds.FilesModified
			be.filesDeleted = ds.FilesDeleted
			be.hasFileStats = true
			be.hasLineStats = true
		} else if fs, ok := msg.files[be.hash]; ok {
			be.filesAdded = fs.FilesAdded
			be.filesModified = fs.FilesModified
			be.filesDeleted = fs.FilesDeleted
			be.hasFileStats = true
		}
	}
}

// collectCommitHashes returns the hashes of all commit entries in the list.
func collectCommitHashes(ls *listState) []string {
	hashes := make([]string, 0, len(ls.entries))
	for _, e := range ls.entries {
		if ce, ok := e.(commitEntry); ok {
			hashes = append(hashes, ce.hash)
		}
	}
	return hashes
}

// collectBranchHashes returns the hashes of all branch entries.
func collectBranchHashes(entries []branchEntry) []string {
	hashes := make([]string, 0, len(entries))
	for _, e := range entries {
		hashes = append(hashes, e.hash)
	}
	return hashes
}

// loadCommitStatsCmd returns an async command that fetches stats for the
// currently loaded commit entries.
func (m *Model) loadCommitStatsCmd(level git.DiffStatLevel) tea.Cmd {
	gc := m.gitBus
	hashes := collectCommitHashes(&m.commits.listState)
	return func() tea.Msg {
		if level >= git.DiffStatLines {
			return commitStatsLoadedMsg{
				lines: gc.GetCommitDiffSummaries(hashes),
				level: git.DiffStatLines,
			}
		}
		return commitStatsLoadedMsg{
			files: gc.GetCommitFileSummaries(hashes),
			level: git.DiffStatFiles,
		}
	}
}

// loadBranchStatsCmd returns an async command that fetches stats for all
// cached branch entries (full set, not just the current window).
func (m *Model) loadBranchStatsCmd(level git.DiffStatLevel) tea.Cmd {
	gc := m.gitBus
	hashes := collectBranchHashes(m.branches.allEntries)
	return func() tea.Msg {
		if level >= git.DiffStatLines {
			return branchStatsLoadedMsg{
				lines: gc.GetCommitDiffSummaries(hashes),
				level: git.DiffStatLines,
			}
		}
		return branchStatsLoadedMsg{
			files: gc.GetCommitFileSummaries(hashes),
			level: git.DiffStatFiles,
		}
	}
}

// maybeLoadStats checks whether the current column layout requires a higher
// stat level than what is loaded, and if so, starts an async load.
// Returns a tea.Cmd if a load was triggered, nil otherwise.
func (m *Model) maybeLoadStats() tea.Cmd {
	switch m.activeTab {
	case TabCommits:
		needed := m.commits.columns.StatLevel()
		if needed > m.commits.statsLevel && !m.commits.statsLoading {
			m.commits.statsLoading = true
			m.commits.statsEpoch = time.Now()
			return m.loadCommitStatsCmd(needed)
		}
	case TabBranches:
		needed := m.branches.columns.StatLevel()
		if needed > m.branches.statsLevel && !m.branches.statsLoading {
			m.branches.statsLoading = true
			m.branches.statsEpoch = time.Now()
			return m.loadBranchStatsCmd(needed)
		}
	}
	return nil
}

// isStatsLoading reports whether the active tab has a stat load in flight.
func (m *Model) isStatsLoading() bool {
	switch m.activeTab {
	case TabCommits:
		return m.commits.statsLoading
	case TabBranches:
		return m.branches.statsLoading
	default:
		return false
	}
}

// -------------------------------------------------------------------------
// Config persistence
// -------------------------------------------------------------------------

// saveConfigIfNeeded saves column config if the last key action was a sort
// cycle, reorder, or visibility toggle (detected by checking focus zones).
func (m *Model) saveConfigIfNeeded() {
	ls := m.activeListState()
	if ls.filterFocus == focusHeader || ls.filterFocus == focusGearDropdown {
		m.saveConfig()
	}
}

// saveConfig persists the current column layout for all tabs.
func (m *Model) saveConfig() {
	if m.configPath == "" {
		return
	}
	cfg := &GitPanelConfig{
		Commits:     tabConfigPtr(m.commits.columns),
		Branches:    tabConfigPtr(m.branches.columns),
		Tags:        tabConfigPtr(m.tags.columns),
		Uncommitted: tabConfigPtr(m.uncommitted.columns),
	}
	_ = SaveColumnConfig(m.configPath, cfg)
}

func tabConfigPtr(cl *ColumnLayout) *TabColumnConfig {
	if cl == nil {
		return nil
	}
	tc := cl.ToConfig()
	return &tc
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

	// Compute column widths for this render cycle. During active resize
	// the motion handler sets widths directly; skip recomputation so
	// the direct redistribution is preserved.
	if ls.columns != nil && !ls.header.resizing {
		ls.columns.ComputeWidths(ls.entries, m.width)
	}

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
	} else {
		lines = append(lines, renderSearchHint(m.width, m.theme))
		lines = append(lines, divider)
	}

	// 3. Column header row + dashed divider below.
	lines = append(lines, renderHeaderRow(ls, m.width, m.theme))
	headerDivider := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Border).
		Render(strings.Repeat("\u2504", m.width)) // ┄ dashed line
	lines = append(lines, headerDivider)

	// 4. Options bar for uncommitted tab (always visible).
	var optionsLines []string
	if m.activeTab == TabUncommitted {
		optionsLines = append(optionsLines, divider)
		optionsLines = append(optionsLines, renderOptionsBar(&m.uncommitted, m.width, m.theme))
	}

	// 5. Entry list fills remaining height.
	entryHeight := max(m.height-len(lines)-len(bottomLines)-len(optionsLines), 0)
	entryLines := m.renderEntries(ls, entryHeight)
	lines = append(lines, entryLines...)
	lines = append(lines, optionsLines...)
	lines = append(lines, bottomLines...)

	return strings.Join(lines, "\n")
}

// renderEntries renders the visible portion of the active tab's entry list.
// When the gear dropdown is open, it is overlaid on the right side of the
// entry lines without replacing them.
func (m *Model) renderEntries(ls *listState, height int) []string {
	// Initial loading indicator: show spinner centered in the entry area.
	if ls.initialLoading && len(ls.entries) == 0 && height > 0 {
		lines := make([]string, height)
		lines[0] = m.renderInitialLoadingBar(ls)
		empty := padToWidth("", m.width, m.theme.Palette)
		for i := 1; i < height; i++ {
			lines[i] = empty
		}
		return lines
	}

	start, end := ls.visibleEntries(height)
	lines := make([]string, 0, height)
	dropTarget := dragDropTarget(ls, m.width)

	viewHeight := end - start
	for i := start; i < end; i++ {
		idx := ls.filtered[i]
		selected := i == ls.cursor
		viewRow := i - start
		line := m.renderColumnEntry(ls.entries[idx], idx, selected, dropTarget, viewRow, viewHeight)
		lines = append(lines, line)
	}

	// Fill remaining lines with empty padded rows.
	for len(lines) < height {
		lines = append(lines, padToWidth("", m.width, m.theme.Palette))
	}

	// Loading bar overlay — bottom for forward, top for backward.
	if ls.showLoadingBar() && len(lines) > 0 {
		if ls.showLoadingPrev() {
			lines[0] = m.renderListLoadingBar(ls)
		} else {
			lines[len(lines)-1] = m.renderListLoadingBar(ls)
		}
	}

	// Gear dropdown overlay — right-aligned floating panel.
	if ls.header.gearOpen && ls.columns != nil {
		dropW := gearDropdownWidth(ls.columns)
		dropLines := renderGearDropdown(ls, dropW, m.theme)
		applyGearOverlay(lines, dropLines, m.width, dropW)
	}

	return lines
}

// renderColumnEntry renders a single entry row using the column layout.
// viewRow and viewHeight are used for centering stat column spinners.
func (m *Model) renderColumnEntry(entry listEntry, entryIdx int, selected bool, dropTarget, viewRow, viewHeight int) string {
	ls := m.activeListState()
	vis := ls.columns.VisibleColumns()
	p := m.theme.Palette

	padStyle := cellPadStyle(selected, p)
	divStyle := lipgloss.NewStyle().Foreground(p.Border)
	if selected {
		divStyle = divStyle.Background(p.Selection)
	}
	sep := divStyle.Render(" \u2502 ") // " │ "

	dropBg := ""
	if dropTarget >= 0 {
		dropBg = dropHighlightBg(p)
	}

	statsLoading := m.isStatsLoading()

	var b strings.Builder
	for i, col := range vis {
		if hasSepBefore(vis, i) {
			b.WriteString(sep)
		}
		var cell string
		if isStatColumn(col.Def.ID) && !entryHasStats(entry, col.Def.ID) {
			cell = m.renderStatPlaceholder(col.Width, selected, statsLoading, viewRow, viewHeight)
		} else {
			cell = m.renderCell(entry, entryIdx, col.Def.ID, col.Width, selected)
		}
		if i == dropTarget && !selected {
			cell = tintCellBg(cell, dropBg)
		}
		b.WriteString(cell)
	}

	content := b.String()
	contentWidth := lipgloss.Width(content)
	padCount := max(m.width-contentWidth, 0)
	if padCount > 0 {
		content += padStyle.Render(strings.Repeat(" ", padCount))
	}

	return content
}

// entryHasStats reports whether the entry has the stats required by the column.
func entryHasStats(entry listEntry, colID ColumnID) bool {
	switch e := entry.(type) {
	case commitEntry:
		if lineStatIDs[colID] {
			return e.hasLineStats
		}
		return e.hasFileStats
	case branchEntry:
		if lineStatIDs[colID] {
			return e.hasLineStats
		}
		return e.hasFileStats
	default:
		return true // tags/uncommitted don't have stat columns
	}
}

// renderStatPlaceholder renders a placeholder cell for a stat column when
// stats are not yet loaded. Shows a centered spinner on the middle row
// when loading, spaces on other rows, or an em-dash when not loading.
func (m *Model) renderStatPlaceholder(width int, selected, loading bool, viewRow, viewHeight int) string {
	p := m.theme.Palette
	padStyle := cellPadStyle(selected, p)

	if loading && viewRow == viewHeight/2 {
		return m.renderColumnSpinner(width, selected)
	}
	if loading {
		return padStyle.Render(strings.Repeat(" ", width))
	}
	// Not loading — show em-dash.
	style := lipgloss.NewStyle().Foreground(p.Muted)
	if selected {
		style = style.Background(p.Selection)
	}
	return fitCell("\u2014", width, style)
}

// renderColumnSpinner renders a centered braille spinner glyph within the
// given column width, using the stats epoch for animation.
func (m *Model) renderColumnSpinner(width int, selected bool) string {
	p := m.theme.Palette
	padStyle := cellPadStyle(selected, p)
	epoch := m.activeStatsEpoch()
	elapsed := time.Since(epoch)
	frame := spinnerFrames[int(elapsed/spinnerFramePeriod)%len(spinnerFrames)]
	spinStyle := lipgloss.NewStyle().Foreground(p.Primary)
	if selected {
		spinStyle = spinStyle.Background(p.Selection)
	}
	glyph := spinStyle.Render(frame)
	glyphWidth := lipgloss.Width(glyph)
	leftPad := max((width-glyphWidth)/2, 0)
	rightPad := max(width-leftPad-glyphWidth, 0)
	return padStyle.Render(strings.Repeat(" ", leftPad)) + glyph + padStyle.Render(strings.Repeat(" ", rightPad))
}

// activeStatsEpoch returns the stats animation epoch for the active tab.
func (m *Model) activeStatsEpoch() time.Time {
	switch m.activeTab {
	case TabCommits:
		return m.commits.statsEpoch
	case TabBranches:
		return m.branches.statsEpoch
	default:
		return time.Time{}
	}
}

// renderCell dispatches to the correct tab-specific cell renderer.
func (m *Model) renderCell(entry listEntry, entryIdx int, colID ColumnID, width int, selected bool) string {
	switch e := entry.(type) {
	case commitEntry:
		return renderCommitCell(e, colID, width, selected, m.theme)
	case branchEntry:
		return renderBranchCell(e, colID, width, selected, m.theme, m.branches.dirtyTree)
	case tagEntry:
		return renderTagCell(e, colID, width, selected, m.theme)
	case uncommittedEntry:
		staging := StagingDefault
		if entryIdx < len(m.uncommitted.stagingStates) {
			staging = m.uncommitted.stagingStates[entryIdx]
		}
		return renderUncommittedCell(e, staging, colID, width, selected, m.theme)
	default:
		return strings.Repeat(" ", width)
	}
}

// -------------------------------------------------------------------------
// Pagination: load-more trigger + commands
// -------------------------------------------------------------------------

// entryHeight computes the available entry rows (same math as View).
func (m *Model) entryHeight() int {
	ls := m.activeListState()
	chromeLines := chromeTopHeight // tab bar + divider + search/hint + divider + header
	if ls.filterActive {
		chromeLines += 2 // divider + filter toggles
	}
	if m.activeTab == TabUncommitted {
		chromeLines += 2 // divider + options bar
	}
	return max(m.height-chromeLines, 0)
}

// maybeLoadMore checks if pagination should fetch the next page and, if so,
// starts the async load and returns the command. Otherwise returns nil.
func (m *Model) maybeLoadMore() tea.Cmd {
	ls := m.activeListState()
	if !ls.needsLoadMore(m.entryHeight()) {
		return nil
	}
	ls.loadingMore = true
	ls.loadingEpoch = time.Now()
	ls.loadingBarUntil = time.Now().Add(minListLoadingBarDuration)
	m.viewDirty = true
	return m.loadMoreCmd()
}

// loadMoreCmd returns the async command for the active tab's next page.
func (m *Model) loadMoreCmd() tea.Cmd {
	switch m.activeTab {
	case TabCommits:
		return m.loadMoreCommitsCmd()
	case TabBranches:
		return m.loadMoreBranchesCmd()
	case TabTags:
		return m.loadMoreTagsCmd()
	case TabUncommitted:
		return m.loadMoreUncommittedCmd()
	}
	return nil
}

// loadMoreCommitsCmd returns an async command that fetches the next page
// of commits from the git client.
func (m *Model) loadMoreCommitsCmd() tea.Cmd {
	gc := m.gitBus
	// Skip is the absolute offset past all loaded entries (window start + window size).
	skip := m.commits.windowStart + len(m.commits.entries)
	defaultTipHash := defaultBranchTipHash(gc)
	statLevel := m.commits.statsLevel
	return func() tea.Msg {
		entries, hasMore := loadCommitsPage(gc, defaultTipHash, skip, listPageSize, statLevel)
		return commitsMoreMsg{entries: entries, hasMore: hasMore}
	}
}

// loadMoreBranchesCmd returns an async command that delivers the next page
// of branches from the cached allEntries.
func (m *Model) loadMoreBranchesCmd() tea.Cmd {
	offset := m.branches.windowStart + len(m.branches.entries)
	all := m.branches.allEntries
	return func() tea.Msg {
		page := sliceBranchPage(all, offset, listPageSize)
		hasMore := offset+listPageSize < len(all)
		return branchesMoreMsg{entries: page, hasMore: hasMore}
	}
}

// loadMoreTagsCmd returns an async command that delivers the next page
// of tags from the cached allEntries.
func (m *Model) loadMoreTagsCmd() tea.Cmd {
	offset := m.tags.windowStart + len(m.tags.entries)
	all := m.tags.allEntries
	return func() tea.Msg {
		page := sliceTagPage(all, offset, listPageSize)
		hasMore := offset+listPageSize < len(all)
		return tagsMoreMsg{entries: page, hasMore: hasMore}
	}
}

// loadMoreUncommittedCmd returns an async command that delivers the next page
// of uncommitted entries from the cached allEntries.
func (m *Model) loadMoreUncommittedCmd() tea.Cmd {
	offset := m.uncommitted.windowStart + len(m.uncommitted.entries)
	all := m.uncommitted.allEntries
	return func() tea.Msg {
		page := sliceUncommittedPage(all, offset, listPageSize)
		hasMore := offset+listPageSize < len(all)
		return uncommittedMoreMsg{entries: page, hasMore: hasMore}
	}
}

// -------------------------------------------------------------------------
// Backward page load commands
// -------------------------------------------------------------------------

// loadPrevCmd returns the async command for the active tab's previous page.
func (m *Model) loadPrevCmd() tea.Cmd {
	switch m.activeTab {
	case TabCommits:
		return m.loadPrevCommitsCmd()
	case TabBranches:
		return m.loadPrevBranchesCmd()
	case TabTags:
		return m.loadPrevTagsCmd()
	case TabUncommitted:
		return m.loadPrevUncommittedCmd()
	}
	return nil
}

// loadPrevCommitsCmd fetches the page before the current window from git.
func (m *Model) loadPrevCommitsCmd() tea.Cmd {
	gc := m.gitBus
	ws := m.commits.windowStart
	start := max(ws-listPageSize, 0)
	count := ws - start
	defaultTipHash := defaultBranchTipHash(gc)
	statLevel := m.commits.statsLevel
	return func() tea.Msg {
		entries, _ := loadCommitsPage(gc, defaultTipHash, start, count, statLevel)
		return commitsPrevMsg{entries: entries}
	}
}

// loadPrevBranchesCmd re-slices the previous page from cached allEntries.
func (m *Model) loadPrevBranchesCmd() tea.Cmd {
	ws := m.branches.windowStart
	all := m.branches.allEntries
	start := max(ws-listPageSize, 0)
	return func() tea.Msg {
		page := sliceBranchPage(all, start, ws-start)
		return branchesPrevMsg{entries: page}
	}
}

// loadPrevTagsCmd re-slices the previous page from cached allEntries.
func (m *Model) loadPrevTagsCmd() tea.Cmd {
	ws := m.tags.windowStart
	all := m.tags.allEntries
	start := max(ws-listPageSize, 0)
	return func() tea.Msg {
		page := sliceTagPage(all, start, ws-start)
		return tagsPrevMsg{entries: page}
	}
}

// loadPrevUncommittedCmd re-slices the previous page from cached allEntries.
func (m *Model) loadPrevUncommittedCmd() tea.Cmd {
	ws := m.uncommitted.windowStart
	all := m.uncommitted.allEntries
	start := max(ws-listPageSize, 0)
	return func() tea.Msg {
		page := sliceUncommittedPage(all, start, ws-start)
		return uncommittedPrevMsg{entries: page}
	}
}

// -------------------------------------------------------------------------
// Pagination: loading bar rendering
// -------------------------------------------------------------------------

// renderListLoadingBar returns a single line with a centered animated spinner,
// used as a bottom overlay during infinite scroll page loads. Time-based
// animation driven by the decor tick (100ms).
func (m *Model) renderListLoadingBar(ls *listState) string {
	elapsed := time.Since(ls.loadingEpoch)
	frame := spinnerFrames[int(elapsed/spinnerFramePeriod)%len(spinnerFrames)]
	spinSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary)
	textSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	content := spinSt.Render(frame) + " " + textSt.Render("Loading more...")
	contentWidth := lipgloss.Width(content)
	leftPad := max((m.width-contentWidth)/2, 0)
	rightPad := max(m.width-leftPad-contentWidth, 0)
	return strings.Repeat(" ", leftPad) + content + strings.Repeat(" ", rightPad)
}

// renderInitialLoadingBar returns a centered spinner line for the initial
// data load, reusing the same epoch-based animation as the pagination bar.
func (m *Model) renderInitialLoadingBar(ls *listState) string {
	elapsed := time.Since(ls.loadingEpoch)
	frame := spinnerFrames[int(elapsed/spinnerFramePeriod)%len(spinnerFrames)]
	spinSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary)
	textSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	content := spinSt.Render(frame) + " " + textSt.Render("Loading...")
	contentWidth := lipgloss.Width(content)
	leftPad := max((m.width-contentWidth)/2, 0)
	rightPad := max(m.width-leftPad-contentWidth, 0)
	return strings.Repeat(" ", leftPad) + content + strings.Repeat(" ", rightPad)
}

// NeedsDecorTick reports whether the loading bar animation needs decor-rate
// ticking.
func (m *Model) NeedsDecorTick() bool {
	ls := m.activeListState()
	return ls.showLoadingBar() || ls.initialLoading || m.isStatsLoading()
}

// MarkViewDirty sets the viewDirty flag so the next View call re-renders.
func (m *Model) MarkViewDirty() {
	m.viewDirty = true
}
