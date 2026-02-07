package filetree

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	devicons "github.com/epilande/go-devicons"
)

// indentWidth is the number of spaces per nesting level.
// Derived from: 2 spaces per level is standard for compact tree views.
const indentWidth = 2

// headerHeight is the vertical space consumed by the title line alone.
// Derived from: 1 title line = 1.
const headerHeight = 1

// treeChromeHeight is the vertical space consumed by the top section in
// tree browsing mode, references mode, and document symbols mode.
// Derived from: header(1) + hint-or-input(1) + divider(1) = 3.
const treeChromeHeight = 3

// topChromeHeight is the vertical space consumed by the top section in
// search mode. Scope line is part of top chrome, separated by dividers.
// Derived from: header(1) + search bar(1) + divider(1) + scope line(1) + divider(1) = 5.
const topChromeHeight = 5

// replaceChromeHeight is the vertical space consumed by the top section in
// replace mode, which adds a replacement input row below the search bar.
// Derived from: header(1) + search bar(1) + replace bar(1) + divider(1) + scope line(1) + divider(1) = 6.
const replaceChromeHeight = 6

// searchFooterHeight is the vertical space consumed by the search-mode footer.
// Derived from: toolbar separator(1) + toolbar(1) + match summary(1) = 3.
const searchFooterHeight = 3

// footerToolbarOffset is the toolbar's row offset from the footer base (separator).
const footerToolbarOffset = 1

// refsFooterHeight is the vertical space consumed by the references/symbols footer.
// Derived from: separator(1) + hint line(1) = 2.
const refsFooterHeight = 2

// tabsChromeHeight is the vertical space consumed by the top section in
// tabs mode when the filter is active (filter input moves to top).
// Derived from: header(1) + title(1) + separator(1) + filter input(1) + divider(1) = 5.
const tabsChromeHeight = 5

// tabsFooterHeight is the vertical space consumed by the tabs-mode footer
// when the filter is active (toggle badges move to bottom).
// Derived from: divider(1) + toolbar(1) + divider(1) + hint line(1) = 4.
const tabsFooterHeight = 4

// tabsHintHeight is the vertical space consumed by the tabs-mode footer
// when the filter is NOT active (hint line only).
// Derived from: hint line(1) = 1.
const tabsHintHeight = 1

// newEntryFooterHeight is the vertical space consumed by the new-entry input.
// Derived from: separator(1) + input line(1) = 2.
const newEntryFooterHeight = 2

// iconDir is the prefix glyph for expanded directories.
const iconDir = theme.IconCollapse

// iconDirClosed is the prefix glyph for collapsed directories.
const iconDirClosed = theme.IconExpand

// iconFile is the prefix glyph for regular files.
const iconFile = " "

// viewMode tracks whether the panel is showing the tree or search results.
type viewMode int

const (
	viewTree       viewMode = iota
	viewSearch
	viewReferences  // LSP references results.
	viewDocSymbols  // LSP document symbol outline.
	viewReplace     // Multi-file find and replace.
	viewTabs        // Open tab list.
)

// tabFocusZone identifies which zone has focus in the tabs panel.
type tabFocusZone int

const (
	tabFocusFilter  tabFocusZone = iota // The "/" filter input line.
	tabFocusToggles                     // The [Aa] [ab] [.*] toggle badges.
)

// tabToggleOrder defines the Tab-cycling sequence for tab filter toggles.
// Derived from: [Aa] → [ab] → [.*] (left to right).
var tabToggleOrder = [toolbarToggleCount]toggleIndex{toggleCase, toggleWord, toggleRegex}

// searchItemKind classifies entries in the search results list.
type searchItemKind int

const (
	searchItemFile  searchItemKind = iota // File header: • path/to/file.go
	searchItemMatch                       // Match line:   42  content...
	searchItemGap                         // Visual separator between file blocks.
)

// searchItem is a single row in the VS Code-style search results.
type searchItem struct {
	kind searchItemKind
	path string // Absolute file path (file headers and match lines).
	line int    // 1-based line number (match lines only).
	text string // Line content (match lines only).
}

// searchMatch is a single content match returned by grepFileWithConfig.
type searchMatch struct {
	line int    // 1-based.
	text string // Full line content.
}

// ReferenceEntry represents a single reference location with an optional
// line preview, passed from app.go to the file tree via SetReferences.
type ReferenceEntry struct {
	// FilePath is the display path (relative to project root).
	FilePath string

	// AbsPath is the absolute file path used for navigation.
	AbsPath string

	// Line is the 0-indexed line number.
	Line int

	// Col is the 0-indexed column (rune offset).
	Col int

	// Preview is the trimmed content of the referenced line.
	Preview string
}

// SymbolEntry represents a single document symbol in a flat, depth-annotated list.
type SymbolEntry struct {
	Name   string
	Kind   lsp.SymbolKind
	Line   int    // 0-indexed start line.
	Col    int    // 0-indexed start column.
	Detail string // Optional detail (e.g., return type).
	Depth  int    // Nesting depth (0 = top-level).
}

// searchFocus identifies which element in the search footer has keyboard focus.
type searchFocus int

const (
	focusQuery   searchFocus = iota // The search query input line.
	focusReplace                    // The replacement text input (replace mode only).
	focusToggles                    // The toggle badges row.
	focusBtnOne                     // The [→1] replace-one button (replace mode only).
	focusBtnAll                     // The [→*] replace-all button (replace mode only).
	focusScope                      // The path scope text input.
)

// searchFocusOrder defines the Tab-cycling order for search mode.
// Derived from: query → scope (top chrome) → toggles (footer).
var searchFocusOrder = [3]searchFocus{focusQuery, focusScope, focusToggles}

// replaceFocusOrder defines the Tab-cycling order for replace mode.
// Derived from: query → replace → scope (top chrome) → toggles → buttons (footer).
var replaceFocusOrder = [6]searchFocus{focusQuery, focusReplace, focusScope, focusToggles, focusBtnOne, focusBtnAll}

// toggleIndex identifies a specific toggle badge within the toolbar.
type toggleIndex int

const (
	toggleCase  toggleIndex = iota // [Aa] case sensitivity.
	toggleWord                     // [ab] whole word.
	toggleRegex                    // [.*] regex mode.
	toggleGlob                     // [re] glob pattern for scope filter.
)

// toggleCount is the total number of toggles.
// Derived from: the four toggleIndex values above.
const toggleCount = 4

// toolbarToggleCount is the number of toggles rendered on the toolbar line.
// The glob toggle [re] renders on the scope line instead.
// Derived from: [Aa], [ab], [.*] = 3.
const toolbarToggleCount = 3

// badgeLabels maps each toggle index to its display string.
// Derived from: the four toggle indicators [Aa], [ab], [.*], [re].
var badgeLabels = [toggleCount]string{"Aa", "ab", ".*", "re"}

// toggleTabOrder defines the Tab-cycling sequence for toggle badges.
// [re] comes first (adjacent to the scope input), then [Aa] [ab] [.*].
// Derived from: user-facing order In: → [re] → [Aa] → [ab] → [.*].
var toggleTabOrder = [toggleCount]toggleIndex{toggleGlob, toggleCase, toggleWord, toggleRegex}

// scopePrefix is the label rendered before the scope input field.
const scopePrefix = "In: "

// matchConfig controls how search queries are matched against file contents.
type matchConfig struct {
	caseSensitive bool
	wholeWord     bool
	useRegex      bool
	compiled      *regexp.Regexp // Pre-compiled regex (nil unless useRegex).
}

// minSearchQueryLen is the minimum query length before content search runs.
// Derived from: 2 characters avoids scanning every file for single-char queries.
const minSearchQueryLen = 2

// maxSearchFileSize is the largest file we'll read for content search (64KB).
// Derived from: 65536 bytes covers most source files while skipping binaries.
const maxSearchFileSize = 65536

// maxMatchesPerFile caps matches per file to prevent flooding from one file.
// Derived from: 10 keeps results compact per file block.
const maxMatchesPerFile = 10

// maxTotalMatches caps total match lines across all files.
// Derived from: 100 keeps the results list navigable.
const maxTotalMatches = 100

// binaryProbeSize is the number of bytes checked for null bytes (binary detection).
// Derived from: 512 bytes covers headers of most binary formats.
const binaryProbeSize = 512

// maxInputLen caps the length of user text inputs (search query, scope path)
// to prevent unbounded memory growth from paste.
// Derived from: 1024 characters covers any realistic search or path query.
const maxInputLen = 1024

// searchDebounceInterval is the delay after the last keystroke before running
// the search grep pipeline. Batches rapid sequential keystrokes into one scan.
// Derived from: 50ms ≈ 3 frames at 60fps, imperceptible to users.
const searchDebounceInterval = 50 * time.Millisecond

// btnFlashDuration is how long a replace button stays highlighted after activation.
// Derived from: 200ms matches the editor's replacebar flash duration.
const btnFlashDuration = 200 * time.Millisecond

// maxFileCacheSize caps total cached file content to prevent unbounded memory.
// Derived from: 128MB covers large project file sets with headroom.
const maxFileCacheSize = 128 << 20

// searchTickMsg is the debounce timer message. The version field gates stale
// ticks so only the latest query triggers a grep.
type searchTickMsg struct{ version int }

// cachedFile holds a pre-read, pre-split file's lines for reuse across
// multiple queries within a single search session.
type cachedFile struct {
	lines []string
	size  int64
}

// fileCache provides bounded, session-scoped caching of file content to
// eliminate redundant disk I/O across keystrokes. Uses FIFO eviction when
// the cache exceeds maxFileCacheSize.
type fileCache struct {
	entries   map[string]*cachedFile
	order     []string // insertion order for FIFO eviction
	totalSize int64
}

// newFileCache creates an empty file cache.
func newFileCache() *fileCache {
	return &fileCache{
		entries: make(map[string]*cachedFile, 512),
		order:   make([]string, 0, 512),
	}
}

// get returns the cached lines for path, or nil if not cached.
func (c *fileCache) get(path string) []string {
	if e, ok := c.entries[path]; ok {
		return e.lines
	}
	return nil
}

// put stores file lines in the cache, evicting oldest entries if the new
// entry would exceed the budget.
func (c *fileCache) put(path string, lines []string, size int64) {
	if _, ok := c.entries[path]; ok {
		return
	}
	for c.totalSize+size > maxFileCacheSize && len(c.order) > 0 {
		c.evictOldest()
	}
	if c.totalSize+size > maxFileCacheSize {
		return // single entry exceeds budget
	}
	c.entries[path] = &cachedFile{lines: lines, size: size}
	c.order = append(c.order, path)
	c.totalSize += size
}

// evictOldest removes the oldest cache entry.
func (c *fileCache) evictOldest() {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	c.order = c.order[1:]
	if e, ok := c.entries[oldest]; ok {
		c.totalSize -= e.size
		delete(c.entries, oldest)
	}
}

// evict removes a single path from the cache so the next access re-reads disk.
func (c *fileCache) evict(path string) {
	e, ok := c.entries[path]
	if !ok {
		return
	}
	c.totalSize -= e.size
	delete(c.entries, path)
	for i, p := range c.order {
		if p == path {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// Entry represents a single visible node in the flattened tree.
type Entry struct {
	Name     string
	Path     string
	IsDir    bool
	Depth    int
	Expanded bool
}

// iconFileActive is the prefix glyph for the currently open file.
const iconFileActive = theme.IconModified

// Model is the Bubble Tea model for the file tree panel.
// It displays a scrollable, collapsible directory tree with cursor navigation
// and an inline search bar activated by '/'.
type Model struct {
	// Tree state.
	entries   []Entry
	pathIndex map[string]int // path → entries index for O(1) lookups.
	cursor    int
	scroll    int
	width     int
	height    int
	focused   bool
	bounceOffset   int
	theme          *theme.Theme
	activeFilePath string // Path of the file currently open in the code viewer.
	rootPath       string // Root directory for relative path display.

	// Search state.
	mode           viewMode
	searchQuery    []rune
	searchSource   []string     // All searchable file paths (gitignore-filtered, built on enter).
	searchItems    []searchItem // Flat list of file headers, match lines, and gaps.
	searchCursor   int
	searchScroll   int
	searchNumWidth int // Digit width of the largest line number in results.

	// Async search worker and state.
	searchWorker *SearchWorker // Parallel async scanner (nil when not searching).
	searchDone   bool          // True when the latest async scan is complete.
	searchStale  bool          // True when a new scan is in-flight but no batch has arrived yet.

	// Search performance: debounce, caching, pre-indexed scope.
	searchVersion int        // Monotonic counter; gates stale debounce ticks.
	searchCache   *fileCache // Session-scoped file content cache.
	scopeIndex    map[string][]int // dir prefix → indices into searchSource.
	scopeSorted   []string         // sorted keys of scopeIndex for binary search.

	// Search toolbar state.
	searchFocus    searchFocus        // Active focus zone within search footer.
	toggleActive   [toggleCount]bool  // State of [Aa], [ab], [.*] toggles.
	toggleCursor   toggleIndex        // Which badge is selected when focusToggles.
	scopeQuery     []rune             // Path scope text input contents.
	scopeCursor    int                // Rune-based insertion position within scopeQuery.
	compiledRegexp *regexp.Regexp     // Cached compiled regex (nil if invalid/unused).

	// Cursor blink state for search inputs.
	cursorBlink bool
	lastBlinkAt time.Time

	// Snapshot of tree state before entering search or references, restored on exit.
	savedEntries []Entry
	savedCursor  int
	savedScroll  int

	// References state (viewReferences mode).
	refTitle    string           // Symbol name shown in header.
	refEntries  []ReferenceEntry // Raw reference locations.
	refItems    []searchItem     // Flat list reusing searchItem infrastructure.
	refCursor   int
	refScroll   int
	refNumWidth int // Digit width of the largest line number in results.

	// Document symbols state (viewDocSymbols mode).
	symTitle    string        // File name shown in header.
	symFilePath string        // Absolute file path for navigation.
	symEntries  []SymbolEntry // Flat, depth-annotated symbol list.
	symItems    []searchItem  // Flat searchItem list for rendering.
	symCursor   int
	symScroll   int
	symNumWidth int // Digit width for line numbers.

	// Tabs state (viewTabs mode).
	tabPaths      []string        // Absolute paths of all open tabs.
	tabModified   map[string]bool // Paths with unsaved changes.
	tabFiltered   []int           // Indices into tabPaths matching current filter.
	tabDupBases   map[string]bool // Base filenames that appear more than once (for disambiguation).
	tabActive     string          // Path of the currently active tab (for highlight).
	tabCursor     int             // Cursor within tabFiltered.
	tabScroll     int
	tabFilter       []rune               // Filter query typed after '/'.
	tabFiltering    bool                 // true when the filter input is active.
	tabToggleActive [toolbarToggleCount]bool // [Aa], [ab], [.*] toggles for tab filtering.
	tabFocus        tabFocusZone         // Which zone has focus in tabs mode.
	tabToggleCursor toggleIndex          // Which toggle badge is focused when tabFocus == tabFocusToggles.
	tabCloseHover   int                  // Filtered index whose close icon is hovered (-1 = none).

	// Replace state (viewReplace mode).
	replaceInput []rune       // Replacement text input contents.
	btnFlash     searchFocus  // Which replace button is flashing (focusBtnOne or focusBtnAll).
	btnFlashAt   time.Time    // When the button flash started.
	btnOneCol    int          // Absolute column of [→1] button start (for click handling).
	btnAllCol    int          // Absolute column of [→*] button start (for click handling).

	// New-entry state: two-phase (pending chord → active input).
	pendingNewEntry bool   // Alt+N pressed, waiting for F (file) or D (dir).
	newEntryActive  bool   // Whether the input footer is visible.
	newEntryIsDir   bool   // true = directory, false = file.
	newEntryDir     string // Parent directory for the new entry.
	newEntryInput   []rune // Text being typed (capped at maxInputLen).

	// Delete confirmation state.
	deleteConfirm     bool   // Whether the delete confirmation footer is visible.
	deleteConfirmPath string // Absolute path of the entry to delete.
	deleteConfirmDir  bool   // true if the target is a directory.

	// Rename inline input state.
	renameActive bool   // Whether the rename input footer is visible.
	renamePath   string // Absolute path of the entry being renamed.
	renameIsDir  bool   // true if the target is a directory.
	renameInput  []rune // New name being typed (capped at maxInputLen).

	// Font capability: true when Nerd Font symbols are available.
	nerdFonts bool

	// Git status decorations: maps relative path → visual state.
	// Updated externally via SetGitStatus(). Nil means no git info available.
	gitStatus map[string]git.GitFileState

	// Tracked file set from git index: paths present here are tracked.
	// Used to distinguish ignored files (not in gitStatus AND not tracked).
	trackedSet  map[string]struct{}
	trackedDirs map[string]struct{}
}

// Verify interface compliance at compile time.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a file tree Model with the given theme.
func New(th *theme.Theme) *Model {
	return &Model{
		theme:         th,
		tabCloseHover: -1,
	}
}

// SetNerdFonts enables or disables Nerd Font icon rendering.
func (m *Model) SetNerdFonts(available bool) { m.nerdFonts = available }

// SetGitStatus replaces the git status decoration map and tracked-file sets.
// Paths in all maps are relative to rootPath. Passing nil clears all decorations.
func (m *Model) SetGitStatus(status map[string]git.GitFileState, tracked map[string]struct{}, trackedDirs map[string]struct{}) {
	m.gitStatus = status
	m.trackedSet = tracked
	m.trackedDirs = trackedDirs
}

// gitStateForEntry returns the GitFileState for an entry by computing its
// path relative to rootPath and looking it up in the status map. Falls back
// to ancestor directory lookup for files inside ignored/untracked directories,
// and uses the tracked set to detect ignored files.
func (m *Model) gitStateForEntry(entry *Entry) git.GitFileState {
	if m.gitStatus == nil {
		return git.GitClean
	}
	rel, err := filepath.Rel(m.rootPath, entry.Path)
	if err != nil {
		return git.GitClean
	}
	if state, ok := m.gitStatus[rel]; ok {
		return state
	}
	if inherited := m.inheritedGitState(rel); inherited != git.GitClean {
		return inherited
	}
	return m.inferIgnored(rel, entry.IsDir)
}

// inheritedGitState walks up parent directories looking for the nearest
// ancestor with a propagated Ignored or Untracked state. Only these two
// states cascade to all children. Per-file states (Modified, Added,
// Deleted, Conflict) do not propagate to siblings.
func (m *Model) inheritedGitState(rel string) git.GitFileState {
	for dir := filepath.Dir(rel); dir != "." && dir != ""; dir = filepath.Dir(dir) {
		state, ok := m.gitStatus[dir]
		if !ok {
			continue
		}
		if state <= git.GitUntracked {
			return state
		}
		return git.GitClean
	}
	return git.GitClean
}

// inferIgnored detects ignored files by checking the tracked set from the
// git index. A file not in the status map AND not in the tracked set is
// ignored. Directories use trackedDirs instead.
func (m *Model) inferIgnored(rel string, isDir bool) git.GitFileState {
	if m.trackedSet == nil {
		return git.GitClean
	}
	if isDir {
		if _, ok := m.trackedDirs[rel]; !ok {
			return git.GitIgnored
		}
	} else {
		if _, ok := m.trackedSet[rel]; !ok {
			return git.GitIgnored
		}
	}
	return git.GitClean
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns the initial command (none).
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update processes incoming messages and returns the updated component.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	switch typed := incoming.(type) {
	case msg.TickMsg:
		if m.focused && (m.mode == viewSearch || m.newEntryActive || m.renameActive || m.tabFiltering) {
			const blinkHalfPeriod = 530 * time.Millisecond
			if typed.Time.Sub(m.lastBlinkAt) >= blinkHalfPeriod {
				m.cursorBlink = !m.cursorBlink
				m.lastBlinkAt = typed.Time
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(typed)
	case searchTickMsg:
		return m, m.handleSearchTick(typed)
	case searchBatchMsg:
		return m, m.handleSearchBatch(typed)
	default:
		return m, nil
	}
}

// handleSearchTick processes a debounced search tick. If the version matches
// the current searchVersion, launches an async scan via the search worker.
// Old results stay visible (searchStale flag) until the first new batch
// arrives, preventing a "no results" flicker between queries.
func (m *Model) handleSearchTick(tick searchTickMsg) tea.Cmd {
	if tick.version != m.searchVersion || m.searchWorker == nil {
		return nil
	}

	m.searchStale = true
	m.searchDone = false
	m.compiledRegexp = nil

	query := string(m.searchQuery)
	cfg := m.buildMatchConfig(query)
	m.compiledRegexp = cfg.compiled

	m.searchWorker.Start(m.scopedSourcePaths(), query, cfg, m.searchVersion)
	return searchWatchCmd(m.searchWorker)
}

// handleSearchBatch merges progressive async results into the model.
func (m *Model) handleSearchBatch(batch searchBatchMsg) tea.Cmd {
	if m.searchWorker == nil || batch.version != m.searchVersion {
		return nil
	}

	// First batch for a new query: replace stale results from the previous query.
	if m.searchStale {
		m.searchItems = nil
		m.searchNumWidth = 0
		m.searchCursor = 0
		m.searchScroll = 0
		m.searchStale = false
	}

	// Merge items: add cross-batch gap if appending to existing results.
	if len(m.searchItems) > 0 && len(batch.items) > 0 {
		m.searchItems = append(m.searchItems, searchItem{kind: searchItemGap})
	}
	m.searchItems = append(m.searchItems, batch.items...)

	// Update match summary width.
	m.searchNumWidth = max(m.searchNumWidth, digitCount(batch.maxLine))

	// Cache file content from batch.
	for _, entry := range batch.cached {
		m.searchCache.put(entry.path, entry.lines, entry.size)
	}

	// Position cursor on first match if this is the first batch with results.
	if batch.matches > 0 && m.searchCursor == 0 {
		m.seekFirstMatch()
	}

	m.searchDone = batch.done
	if !batch.done {
		return searchWatchCmd(m.searchWorker)
	}
	return nil
}

// searchWatchCmd returns a Bubble Tea command that subscribes to the search
// worker's output channel, following the git status watcher subscription pattern.
func searchWatchCmd(worker *SearchWorker) tea.Cmd {
	return func() tea.Msg {
		batch, ok := <-worker.Output()
		if !ok {
			return nil
		}
		return batch
	}
}

// View dispatches to the active view mode.
func (m *Model) View() string {
	switch m.mode {
	case viewSearch:
		return m.viewSearchMode()
	case viewReplace:
		return m.viewReplaceMode()
	case viewReferences:
		return m.viewReferencesMode()
	case viewDocSymbols:
		return m.viewDocSymbolsMode()
	case viewTabs:
		return m.viewTabsMode()
	default:
		return m.viewTreeMode()
	}
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the focus identifier for the file tree panel.
func (m *Model) ID() component.FocusID {
	return component.FocusFileTree
}

// Focused returns whether the file tree panel has focus.
func (m *Model) Focused() bool {
	return m.focused
}

// SetFocused sets the focus state.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
}

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates the available dimensions for the file tree panel.
func (m *Model) SetSize(width, height int) {
	m.width = max(width, 0)
	m.height = max(height, 0)
	m.clampScroll()
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// SetRoot loads a directory tree rooted at the given path.
// Only the immediate children of expanded directories are shown.
func (m *Model) SetRoot(root string) {
	m.rootPath = root
	m.entries = nil
	m.loadDir(root, 0)
	m.rebuildPathIndex()
	m.cursor = 0
	m.scroll = 0
}

// SetEntries directly sets the flattened entry list (for mock/test seeding).
func (m *Model) SetEntries(entries []Entry) {
	m.entries = entries
	m.rebuildPathIndex()
	m.cursor = 0
	m.scroll = 0
}

// ScrollUp scrolls the view up by one line.
// Returns true if the scroll was applied, false if at boundary.
func (m *Model) ScrollUp() bool {
	scroll := m.activeScroll()
	if *scroll <= 0 {
		return false
	}
	(*scroll)--
	return true
}

// ScrollDown scrolls the view down by one line.
// Returns true if the scroll was applied, false if at boundary.
func (m *Model) ScrollDown() bool {
	scroll := m.activeScroll()
	maxScroll := max(m.visibleItemCount()-m.bodyHeight(), 0)
	if *scroll >= maxScroll {
		return false
	}
	(*scroll)++
	return true
}

// activeScroll returns a pointer to the scroll offset for the current mode.
func (m *Model) activeScroll() *int {
	switch m.mode {
	case viewSearch:
		return &m.searchScroll
	case viewReferences:
		return &m.refScroll
	case viewDocSymbols:
		return &m.symScroll
	case viewTabs:
		return &m.tabScroll
	default:
		return &m.scroll
	}
}

// visibleItemCount returns the number of items in the active list.
func (m *Model) visibleItemCount() int {
	switch m.mode {
	case viewSearch:
		return len(m.searchItems)
	case viewReferences:
		return len(m.refItems)
	case viewDocSymbols:
		return len(m.symItems)
	case viewTabs:
		return len(m.tabFiltered)
	default:
		return len(m.entries)
	}
}

// SetBounceOffset updates the visual bounce displacement for rendering.
func (m *Model) SetBounceOffset(offset int) {
	m.bounceOffset = offset
}

// SetActiveFile records the path of the currently open file so it can be
// rendered with a distinct icon and color in the tree.
func (m *Model) SetActiveFile(path string) {
	m.activeFilePath = path
}

// InSearchMode reports whether the file tree is currently showing search results.
func (m *Model) InSearchMode() bool {
	return m.mode == viewSearch
}

// InReferencesMode reports whether the file tree is showing LSP references.
func (m *Model) InReferencesMode() bool {
	return m.mode == viewReferences
}

// InDocSymbolsMode reports whether the file tree is showing document symbols.
func (m *Model) InDocSymbolsMode() bool {
	return m.mode == viewDocSymbols
}

// InTabsMode reports whether the file tree is showing the open tabs list.
func (m *Model) InTabsMode() bool {
	return m.mode == viewTabs
}

// TabCursorPath returns the absolute path of the tab under the cursor,
// or empty string if no valid selection exists.
func (m *Model) TabCursorPath() string {
	if m.mode != viewTabs || m.tabCursor >= len(m.tabFiltered) {
		return ""
	}
	idx := m.tabFiltered[m.tabCursor]
	if idx >= len(m.tabPaths) {
		return ""
	}
	return m.tabPaths[idx]
}

// ToggleTabFilter toggles the filter input in tabs mode.
// If already filtering, exits the filter; otherwise enters it.
func (m *Model) ToggleTabFilter() {
	if m.mode != viewTabs {
		return
	}
	if m.tabFiltering {
		m.tabFiltering = false
		m.tabFilter = nil
		m.tabFocus = tabFocusFilter
		m.tabFiltered = m.buildTabFiltered()
		m.tabCursor = clampInt(m.tabCursor, 0, max(len(m.tabFiltered)-1, 0))
		m.ensureTabCursorVisible()
		return
	}
	m.tabFiltering = true
	m.tabFilter = nil
	m.tabFocus = tabFocusFilter
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()
}

// SetReferences switches the panel to references mode, displaying the given
// entries grouped by file. Snapshots the current tree state for restore on exit.
func (m *Model) SetReferences(title string, entries []ReferenceEntry) {
	// Snapshot tree state (same as enterSearch).
	if m.mode == viewSearch {
		m.exitSearch()
	}
	if m.mode != viewReferences {
		m.savedEntries = make([]Entry, len(m.entries))
		copy(m.savedEntries, m.entries)
		m.savedCursor = m.cursor
		m.savedScroll = m.scroll
	}

	m.refTitle = title
	m.refEntries = entries
	m.refItems, m.refNumWidth = buildRefItems(entries)
	m.refCursor = 0
	m.refScroll = 0
	m.mode = viewReferences
}

// buildRefItems converts reference entries into a flat searchItem list grouped
// by file, with gap separators between groups.
func buildRefItems(entries []ReferenceEntry) ([]searchItem, int) {
	if len(entries) == 0 {
		return nil, 0
	}

	// Group entries by AbsPath preserving order of first occurrence.
	type fileGroup struct {
		path    string
		entries []ReferenceEntry
	}
	orderMap := make(map[string]int, len(entries))
	var groups []fileGroup
	for _, e := range entries {
		idx, ok := orderMap[e.AbsPath]
		if !ok {
			idx = len(groups)
			orderMap[e.AbsPath] = idx
			groups = append(groups, fileGroup{path: e.AbsPath})
		}
		groups[idx].entries = append(groups[idx].entries, e)
	}

	var items []searchItem
	maxLine := 0
	for gi, g := range groups {
		if gi > 0 {
			items = append(items, searchItem{kind: searchItemGap})
		}
		items = append(items, searchItem{kind: searchItemFile, path: g.path})
		for _, e := range g.entries {
			displayLine := e.Line + 1 // 1-based for display.
			items = append(items, searchItem{
				kind: searchItemMatch,
				path: g.path,
				line: displayLine,
				text: e.Preview,
			})
			maxLine = max(maxLine, displayLine)
		}
	}
	return items, digitCount(maxLine)
}

// exitReferences restores the tree state from before references mode.
func (m *Model) exitReferences() {
	m.entries = m.savedEntries
	m.rebuildPathIndex()
	m.cursor = m.savedCursor
	m.scroll = m.savedScroll
	m.mode = viewTree

	m.savedEntries = nil
	m.refTitle = ""
	m.refEntries = nil
	m.refItems = nil
	m.refCursor = 0
	m.refScroll = 0
	m.refNumWidth = 0
}

// SetDocumentSymbols switches the panel to document symbols mode, displaying
// the given entries as a flat outline. Snapshots tree state for restore on exit.
func (m *Model) SetDocumentSymbols(title, filePath string, entries []SymbolEntry) {
	if m.mode == viewSearch {
		m.exitSearch()
	}
	if m.mode == viewReferences {
		m.exitReferences()
	}
	if m.mode != viewDocSymbols {
		m.savedEntries = make([]Entry, len(m.entries))
		copy(m.savedEntries, m.entries)
		m.savedCursor = m.cursor
		m.savedScroll = m.scroll
	}

	m.symTitle = title
	m.symFilePath = filePath
	m.symEntries = entries
	m.symItems, m.symNumWidth = buildSymItems(entries)
	m.symCursor = 0
	m.symScroll = 0
	m.mode = viewDocSymbols
}

// buildSymItems converts symbol entries into a flat searchItem list.
// Each symbol becomes a match line with kind label and indented name.
func buildSymItems(entries []SymbolEntry) ([]searchItem, int) {
	if len(entries) == 0 {
		return nil, 0
	}
	items := make([]searchItem, len(entries))
	maxLine := 0
	for i, e := range entries {
		displayLine := e.Line + 1 // 1-based for display.
		indent := strings.Repeat("  ", e.Depth)
		label := lsp.SymbolKindLabel(e.Kind)
		text := indent + label + " " + e.Name
		if e.Detail != "" {
			text += " " + e.Detail
		}
		items[i] = searchItem{
			kind: searchItemMatch,
			line: displayLine,
			text: text,
		}
		maxLine = max(maxLine, displayLine)
	}
	return items, digitCount(maxLine)
}

// ExitDocSymbols restores the tree state from before document symbols mode.
func (m *Model) ExitDocSymbols() {
	m.entries = m.savedEntries
	m.rebuildPathIndex()
	m.cursor = m.savedCursor
	m.scroll = m.savedScroll
	m.mode = viewTree

	m.savedEntries = nil
	m.symTitle = ""
	m.symFilePath = ""
	m.symEntries = nil
	m.symItems = nil
	m.symCursor = 0
	m.symScroll = 0
	m.symNumWidth = 0
}

// SetTabs switches the panel to tabs mode, displaying the given open tabs.
// modified maps paths with unsaved changes. Snapshots tree state for restore.
func (m *Model) SetTabs(tabs []string, active string, modified map[string]bool) {
	if m.mode == viewSearch {
		m.exitSearch()
	}
	if m.mode == viewReferences {
		m.exitReferences()
	}
	if m.mode == viewDocSymbols {
		m.ExitDocSymbols()
	}
	if m.mode != viewTabs {
		m.savedEntries = make([]Entry, len(m.entries))
		copy(m.savedEntries, m.entries)
		m.savedCursor = m.cursor
		m.savedScroll = m.scroll
	}

	m.tabPaths = tabs
	m.tabModified = modified
	m.tabActive = active
	m.tabDupBases = buildDupBasenames(tabs)
	m.tabFiltered = m.buildTabFiltered()
	m.tabCursor = 0
	m.tabScroll = 0
	m.tabFilter = nil
	m.tabFiltering = false
	m.tabToggleActive = [toolbarToggleCount]bool{}
	m.tabFocus = tabFocusFilter
	m.tabToggleCursor = toggleCase
	m.tabCloseHover = -1
	m.mode = viewTabs

	// Pre-select the active tab if present.
	for i, fi := range m.tabFiltered {
		if m.tabPaths[fi] == active {
			m.tabCursor = i
			m.ensureTabCursorVisible()
			break
		}
	}
}

// UpdateTabModified refreshes which tabs have unsaved changes without
// resetting cursor, scroll, or filter state.
func (m *Model) UpdateTabModified(modified map[string]bool) {
	m.tabModified = modified
}

// ExitTabs restores the tree state from before tabs mode.
func (m *Model) ExitTabs() {
	m.entries = m.savedEntries
	m.rebuildPathIndex()
	m.cursor = m.savedCursor
	m.scroll = m.savedScroll
	m.mode = viewTree

	m.savedEntries = nil
	m.tabPaths = nil
	m.tabModified = nil
	m.tabFiltered = nil
	m.tabDupBases = nil
	m.tabActive = ""
	m.tabCursor = 0
	m.tabScroll = 0
	m.tabFilter = nil
	m.tabFiltering = false
	m.tabToggleActive = [toolbarToggleCount]bool{}
	m.tabFocus = tabFocusFilter
	m.tabToggleCursor = toggleCase
	m.tabCloseHover = -1
}

// buildDupBasenames returns a set of base filenames that appear more than
// once in tabs, used to disambiguate entries by showing relative paths.
func buildDupBasenames(tabs []string) map[string]bool {
	counts := make(map[string]int, len(tabs))
	for _, p := range tabs {
		counts[filepath.Base(p)]++
	}
	dups := make(map[string]bool)
	for base, n := range counts {
		if n > 1 {
			dups[base] = true
		}
	}
	return dups
}

// buildTabFiltered returns indices into tabPaths matching the current filter.
// When no filter is active, returns all indices.
func (m *Model) buildTabFiltered() []int {
	query := string(m.tabFilter)
	if query == "" {
		out := make([]int, 0, len(m.tabPaths))
		for i := range m.tabPaths {
			out = append(out, i)
		}
		return out
	}

	caseSensitive := m.tabToggleActive[toggleCase] || hasUpperCase(query)
	wholeWord := m.tabToggleActive[toggleWord]
	useRegex := m.tabToggleActive[toggleRegex]

	var compiled *regexp.Regexp
	if useRegex {
		pattern := query
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		compiled, _ = regexp.Compile(pattern)
	}

	out := make([]int, 0, len(m.tabPaths))
	for i, p := range m.tabPaths {
		name := filepath.Base(p)
		if m.tabMatchesFilter(name, query, caseSensitive, wholeWord, useRegex, compiled) {
			out = append(out, i)
		}
	}
	return out
}

// tabMatchesFilter checks whether a tab filename matches the current filter
// query, respecting the active toggles (case sensitivity, whole word, regex).
func (m *Model) tabMatchesFilter(name, query string, caseSensitive, wholeWord, useRegex bool, compiled *regexp.Regexp) bool {
	if useRegex && compiled != nil {
		if wholeWord {
			return compiled.MatchString(name) && compiled.FindString(name) == name
		}
		return compiled.MatchString(name)
	}
	if !caseSensitive {
		name = strings.ToLower(name)
		query = strings.ToLower(query)
	}
	if wholeWord {
		return name == query
	}
	return strings.Contains(name, query)
}

// ensureTabCursorVisible keeps the tab cursor within the visible window.
func (m *Model) ensureTabCursorVisible() {
	bh := m.bodyHeight()
	if bh <= 0 {
		return
	}
	if m.tabCursor < m.tabScroll {
		m.tabScroll = m.tabCursor
	}
	if m.tabCursor >= m.tabScroll+bh {
		m.tabScroll = m.tabCursor - bh + 1
	}
	maxScroll := max(len(m.tabFiltered)-bh, 0)
	m.tabScroll = clampInt(m.tabScroll, 0, maxScroll)
}

// HoverAt updates hover state for the given content-relative coordinates.
// Returns true if the visual state changed and a redraw is needed.
func (m *Model) HoverAt(viewX, viewY int) bool {
	if m.mode != viewTabs {
		prev := m.tabCloseHover
		m.tabCloseHover = -1
		return prev != -1
	}
	top := treeChromeHeight
	if m.tabFiltering {
		top = tabsChromeHeight
	}
	bodyY := viewY - top
	contentWidth := max(m.width, 1)
	prev := m.tabCloseHover
	if bodyY < 0 || viewX < contentWidth-2 {
		m.tabCloseHover = -1
		return prev != m.tabCloseHover
	}
	idx := m.tabScroll + bodyY
	if idx < 0 || idx >= len(m.tabFiltered) {
		m.tabCloseHover = -1
		return prev != m.tabCloseHover
	}
	m.tabCloseHover = idx
	return prev != m.tabCloseHover
}

// ClearTabCloseHover resets the close-icon hover state.
func (m *Model) ClearTabCloseHover() {
	m.tabCloseHover = -1
}

// ClickAt handles a left-click at the given content-relative coordinates.
// viewX is the column offset within the panel content area; viewY is the
// row offset (0 = first line inside the panel border).
func (m *Model) ClickAt(viewX, viewY int) tea.Cmd {
	switch m.mode {
	case viewSearch:
		return m.clickSearchMode(viewX, viewY)
	case viewReplace:
		return m.clickReplaceMode(viewX, viewY)
	case viewReferences:
		return m.clickReferencesMode(viewY)
	case viewDocSymbols:
		return m.clickDocSymbolsMode(viewY)
	case viewTabs:
		return m.clickTabsMode(viewX, viewY)
	default:
		return m.clickTreeMode(viewY)
	}
}

// clickReferencesMode handles clicks in references mode body area.
func (m *Model) clickReferencesMode(viewY int) tea.Cmd {
	bodyY := viewY - treeChromeHeight
	if bodyY < 0 {
		return nil
	}
	idx := m.refScroll + bodyY
	if idx < 0 || idx >= len(m.refItems) {
		return nil
	}
	if m.refItems[idx].kind == searchItemGap {
		return nil
	}
	m.refCursor = idx
	return m.activateRefResult()
}

// clickTreeMode handles clicks in tree browsing mode.
func (m *Model) clickTreeMode(viewY int) tea.Cmd {
	// Click on search hint (row 1) → enter search mode.
	if viewY == headerHeight {
		m.enterSearch()
		return nil
	}

	// Click in body area (rows treeChromeHeight .. treeChromeHeight+bh-1).
	bodyY := viewY - treeChromeHeight
	if bodyY < 0 {
		return nil
	}
	idx := m.scroll + bodyY
	if idx < 0 || idx >= len(m.entries) {
		return nil
	}
	m.cursor = idx
	return m.activateEntry()
}

// clickSearchMode handles clicks in search mode, dispatching to search
// input, body, or toolbar based on Y position.
func (m *Model) clickSearchMode(viewX, viewY int) tea.Cmd {
	bh := m.bodyHeight()

	// Layout: header(0) | search bar(1) | divider(2) | scope line(3) | divider(4) |
	//         body(5..5+bh-1) | separator(+0) | toolbar(+1) | summary(+2).
	footerBase := topChromeHeight + bh

	if viewY == headerHeight {
		m.searchFocus = focusQuery
		return nil
	}
	if viewY == headerHeight+2 {
		return m.clickScopeLine(viewX)
	}

	bodyY := viewY - topChromeHeight
	if bodyY >= 0 && bodyY < bh {
		return m.clickSearchBody(bodyY)
	}

	if viewY-footerBase == footerToolbarOffset {
		return m.clickToolbar(viewX)
	}

	return nil
}

// clickSearchBody activates a search result at the given body-relative row,
// accounting for replacement preview lines that expand match items.
func (m *Model) clickSearchBody(bodyY int) tea.Cmd {
	idx := m.visualRowToIndex(bodyY)
	if idx < 0 || idx >= len(m.searchItems) {
		return nil
	}
	if m.searchItems[idx].kind == searchItemGap {
		return nil
	}
	m.searchCursor = idx
	return m.activateSearchResult()
}

// visualRowToIndex converts a body-relative visual row to an item index,
// accounting for replacement preview lines.
func (m *Model) visualRowToIndex(bodyY int) int {
	preview := m.hasReplacePreview()
	row := 0
	for i := m.searchScroll; i < len(m.searchItems); i++ {
		if row == bodyY {
			return i
		}
		row++
		if preview && m.searchItems[i].kind == searchItemMatch {
			if row == bodyY {
				return i // Click on preview line → same item.
			}
			row++
		}
	}
	return -1
}

// clickToolbar dispatches a click on the toolbar line to toggle badges,
// replace buttons, or focus the scope input based on X position.
func (m *Model) clickToolbar(viewX int) tea.Cmd {
	// Toolbar layout: " [Aa] [ab] [.*]" or " [Aa] [ab] [.*]  [→1] [→*]".
	// The [re] toggle is on the scope line, not here.
	x := 1 // leading space
	for i := range toolbarToggleCount {
		if i > 0 {
			x++
		}
		badgeW := lipgloss.Width("[" + badgeLabels[i] + "]")
		if viewX >= x && viewX < x+badgeW {
			m.searchFocus = focusToggles
			m.toggleCursor = toggleIndex(i)
			return m.toggleBadge(toggleIndex(i))
		}
		x += badgeW
	}

	if m.mode == viewReplace {
		if cmd := m.clickReplaceButtons(viewX); cmd != nil {
			return cmd
		}
	}

	return nil
}

// clickScopeLine handles a click on the scope line " In: <text>  [re]".
// Clicks on the [re] badge toggle glob mode; anything else focuses scope input.
func (m *Model) clickScopeLine(viewX int) tea.Cmd {
	contentWidth := max(m.width, 1)
	badgeW := lipgloss.Width("[" + badgeLabels[toggleGlob] + "]")
	const trailingSpace = 1
	badgeStart := contentWidth - badgeW - trailingSpace
	if viewX >= badgeStart && viewX < badgeStart+badgeW {
		m.searchFocus = focusToggles
		m.toggleCursor = toggleGlob
		return m.toggleBadge(toggleGlob)
	}
	m.searchFocus = focusScope
	return nil
}

// clickReplaceButtons checks if a click hit [→1] or [→*] and activates it.
func (m *Model) clickReplaceButtons(viewX int) tea.Cmd {
	// btnOneCol/btnAllCol are absolute columns recorded during render.
	// Derive widths from the canonical labels to avoid hardcoded magic numbers.
	btnOneW := lipgloss.Width("[" + theme.IconArrowRight + "1]")
	btnAllW := lipgloss.Width("[" + theme.IconArrowRight + "*]")
	if viewX >= m.btnOneCol && viewX < m.btnOneCol+btnOneW {
		m.searchFocus = focusBtnOne
		m.flashBtn(focusBtnOne)
		return m.replaceCurrentSearchMatch()
	}
	if viewX >= m.btnAllCol && viewX < m.btnAllCol+btnAllW {
		m.searchFocus = focusBtnAll
		m.flashBtn(focusBtnAll)
		return m.replaceAllSearchMatches()
	}
	return nil
}

// RevealPath expands the directory chain to the given file path and positions
// the cursor on it, like VS Code's "reveal in explorer". Works with both
// absolute paths (when rootPath is set) and relative paths (mock/SetEntries mode).
func (m *Model) RevealPath(targetPath string) {
	switch m.mode {
	case viewSearch:
		m.exitSearch()
	case viewReferences:
		m.exitReferences()
	case viewDocSymbols:
		m.ExitDocSymbols()
	}

	// Fast path: file is already visible in the flat entry list.
	if idx := m.findEntryByPath(targetPath); idx >= 0 {
		m.cursor = idx
		m.ensureCursorVisible()
		return
	}

	// Compute the path relative to root so we can split into components.
	// When rootPath is empty (mock mode), the target path is used as-is.
	var rel string
	if m.rootPath != "" {
		var err error
		rel, err = filepath.Rel(m.rootPath, targetPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return
		}
	} else {
		rel = targetPath
	}

	// Split into path components; all but last are directories.
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	dirParts := parts[:len(parts)-1]

	// Expand each directory along the chain.
	currentPath := m.rootPath
	for _, part := range dirParts {
		currentPath = filepath.Join(currentPath, part)
		idx := m.findEntryByPath(currentPath)
		if idx < 0 {
			return
		}
		if m.entries[idx].IsDir && !m.entries[idx].Expanded {
			m.expandAt(idx)
		}
	}

	// Position cursor on the target.
	if idx := m.findEntryByPath(targetPath); idx >= 0 {
		m.cursor = idx
		m.ensureCursorVisible()
	}
}

// rebuildPathIndex builds the path → index map from the current entries slice.
// Called after any operation that changes entries (SetRoot, SetEntries,
// expandAt, collapseAt).
func (m *Model) rebuildPathIndex() {
	m.pathIndex = make(map[string]int, len(m.entries))
	for i, e := range m.entries {
		m.pathIndex[e.Path] = i
	}
}

// findEntryByPath returns the index of the entry with the given absolute path,
// or -1 if not found. Uses the O(1) path index.
func (m *Model) findEntryByPath(path string) int {
	if idx, ok := m.pathIndex[path]; ok {
		return idx
	}
	return -1
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if !m.focused {
		return nil
	}
	if m.deleteConfirm {
		return m.handleDeleteConfirmKey(key)
	}
	if m.renameActive {
		return m.handleRenameKey(key)
	}
	if m.newEntryActive {
		return m.handleNewEntryKey(key)
	}
	if m.pendingNewEntry {
		return m.handlePendingNewEntry(key)
	}
	if m.mode == viewTabs {
		return m.handleTabsKey(key)
	}
	if m.mode == viewDocSymbols {
		return m.handleDocSymbolsKey(key)
	}
	if m.mode == viewReferences {
		return m.handleReferencesKey(key)
	}
	if m.mode == viewReplace {
		return m.handleReplaceKey(key)
	}
	if m.mode == viewSearch {
		return m.handleSearchKey(key)
	}
	return m.handleTreeKey(key)
}

// handleTreeKey processes keys in normal tree browsing mode.
func (m *Model) handleTreeKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "enter":
		return m.activateEntry()
	case "right", "l":
		m.expandDir()
	case "left", "h":
		m.collapseOrParent()
	case "g":
		m.cursor = 0
		m.ensureCursorVisible()
	case "G":
		m.cursor = max(len(m.entries)-1, 0)
		m.ensureCursorVisible()
	case "/":
		m.enterSearch()
	case "alt+n":
		m.pendingNewEntry = true
		return nil
	case "alt+r":
		return m.requestRename()
	case "alt+backspace", "alt+delete":
		return m.requestDelete()
	}
	return nil
}

// handleReferencesKey processes keys in references mode.
func (m *Model) handleReferencesKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc", "q":
		m.exitReferences()
	case "up", "k":
		m.moveRefCursor(-1)
	case "down", "j":
		m.moveRefCursor(1)
	case "enter":
		return m.activateRefResult()
	case "g":
		m.refCursor = 0
		m.refScroll = 0
	case "G":
		if len(m.refItems) > 0 {
			m.refCursor = len(m.refItems) - 1
			m.ensureRefCursorVisible()
		}
	}
	return nil
}

// moveRefCursor moves the cursor within reference items, skipping gap entries.
func (m *Model) moveRefCursor(delta int) {
	n := len(m.refItems)
	if n == 0 {
		return
	}
	next := clampInt(m.refCursor+delta, 0, n-1)
	step := 1
	if delta < 0 {
		step = -1
	}
	for next >= 0 && next < n && m.refItems[next].kind == searchItemGap {
		next += step
	}
	if next < 0 || next >= n {
		return
	}
	m.refCursor = next
	m.ensureRefCursorVisible()
}

// activateRefResult handles Enter on a reference file header or match line.
// Exits references mode so the tree view is restored before navigation.
func (m *Model) activateRefResult() tea.Cmd {
	if m.refCursor >= len(m.refItems) {
		return nil
	}
	item := m.refItems[m.refCursor]
	if item.kind == searchItemGap {
		return nil
	}
	line := 0
	if item.kind == searchItemMatch {
		line = item.line
	}

	path := item.path
	name := filepath.Base(path)
	lang := langFromPath(path)

	// Look up column from the matching reference entry.
	col := 0
	if line > 0 {
		for _, e := range m.refEntries {
			if e.AbsPath == path && e.Line+1 == line {
				col = e.Col
				break
			}
		}
	}

	m.exitReferences()

	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     path,
			Name:     name,
			Language: lang,
			Line:     line,
			Col:      col,
		}
	}
}

// ensureRefCursorVisible keeps the ref cursor within the visible window.
func (m *Model) ensureRefCursorVisible() {
	bh := m.bodyHeight()
	if bh <= 0 {
		return
	}
	if m.refCursor < m.refScroll {
		m.refScroll = m.refCursor
	}
	if m.refCursor >= m.refScroll+bh {
		m.refScroll = m.refCursor - bh + 1
	}
	maxScroll := max(len(m.refItems)-bh, 0)
	m.refScroll = clampInt(m.refScroll, 0, maxScroll)
}

// ---------------------------------------------------------------------------
// Document symbols key handling
// ---------------------------------------------------------------------------

// handleDocSymbolsKey processes keys in document symbols mode.
func (m *Model) handleDocSymbolsKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc", "q":
		m.ExitDocSymbols()
	case "up", "k":
		m.moveSymCursor(-1)
	case "down", "j":
		m.moveSymCursor(1)
	case "enter":
		return m.activateSymResult()
	case "g":
		m.symCursor = 0
		m.symScroll = 0
	case "G":
		if len(m.symItems) > 0 {
			m.symCursor = len(m.symItems) - 1
			m.ensureSymCursorVisible()
		}
	}
	return nil
}

// moveSymCursor moves the cursor within symbol items (no gaps to skip).
func (m *Model) moveSymCursor(delta int) {
	n := len(m.symItems)
	if n == 0 {
		return
	}
	m.symCursor = clampInt(m.symCursor+delta, 0, n-1)
	m.ensureSymCursorVisible()
}

// activateSymResult handles Enter on a symbol entry.
// Exits doc symbols mode and returns a FileOpenMsg for navigation.
func (m *Model) activateSymResult() tea.Cmd {
	if m.symCursor >= len(m.symItems) || m.symCursor >= len(m.symEntries) {
		return nil
	}
	entry := m.symEntries[m.symCursor]
	item := m.symItems[m.symCursor]
	line := item.line // 1-based

	path := m.symFilePath
	name := filepath.Base(path)
	lang := langFromPath(path)

	col := entry.Col
	endCol := col + len([]rune(entry.Name))

	m.ExitDocSymbols()

	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     path,
			Name:     name,
			Language: lang,
			Line:     line,
			Col:      col,
			EndCol:   endCol,
		}
	}
}

// ensureSymCursorVisible keeps the symbol cursor within the visible window.
func (m *Model) ensureSymCursorVisible() {
	bh := m.bodyHeight()
	if bh <= 0 {
		return
	}
	if m.symCursor < m.symScroll {
		m.symScroll = m.symCursor
	}
	if m.symCursor >= m.symScroll+bh {
		m.symScroll = m.symCursor - bh + 1
	}
	maxScroll := max(len(m.symItems)-bh, 0)
	m.symScroll = clampInt(m.symScroll, 0, maxScroll)
}

// clickDocSymbolsMode handles clicks in document symbols mode body area.
func (m *Model) clickDocSymbolsMode(viewY int) tea.Cmd {
	bodyY := viewY - treeChromeHeight
	if bodyY < 0 {
		return nil
	}
	idx := m.symScroll + bodyY
	if idx < 0 || idx >= len(m.symItems) {
		return nil
	}
	m.symCursor = idx
	return m.activateSymResult()
}

// ---------------------------------------------------------------------------
// Tabs key handling
// ---------------------------------------------------------------------------

// handleTabsKey processes keys in tabs list mode.
func (m *Model) handleTabsKey(key tea.KeyMsg) tea.Cmd {
	if m.tabFiltering {
		return m.handleTabsFilterKey(key)
	}
	ks := key.String()
	if cmd, ok := m.handleTabToggleKey(ks); ok {
		return cmd
	}
	switch ks {
	case "esc", "q":
		m.ExitTabs()
	case "up", "k":
		m.moveTabCursor(-1)
	case "down", "j":
		m.moveTabCursor(1)
	case "enter":
		return m.activateTabResult()
	case "g":
		m.tabCursor = 0
		m.tabScroll = 0
	case "G":
		if len(m.tabFiltered) > 0 {
			m.tabCursor = len(m.tabFiltered) - 1
			m.ensureTabCursorVisible()
		}
	case "/":
		m.tabFiltering = true
		m.tabFilter = nil
		m.tabFocus = tabFocusFilter
		m.cursorBlink = true
		m.lastBlinkAt = time.Now()
	}
	return nil
}

// handleTabsFilterKey processes keys while the tab filter input is active.
// Dispatches to the focused zone (filter input or toggle badges).
func (m *Model) handleTabsFilterKey(key tea.KeyMsg) tea.Cmd {
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()
	ks := key.String()

	// Alt shortcuts for toggles work regardless of focus zone.
	if cmd, ok := m.handleTabToggleKey(ks); ok {
		return cmd
	}

	// Tab/Shift+Tab cycle between filter input and toggle badges.
	switch ks {
	case "tab":
		m.advanceTabFocus()
		return nil
	case "shift+tab":
		m.retreatTabFocus()
		return nil
	}

	// Dispatch to the focused zone.
	if m.tabFocus == tabFocusToggles {
		return m.handleTabToggleInput(key)
	}
	return m.handleTabFilterInput(key)
}

// handleTabFilterInput processes keys for the "/" filter text input.
func (m *Model) handleTabFilterInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		m.tabFiltering = false
		m.tabFilter = nil
		m.tabFocus = tabFocusFilter
		m.tabFiltered = m.buildTabFiltered()
		m.tabCursor = clampInt(m.tabCursor, 0, max(len(m.tabFiltered)-1, 0))
		m.ensureTabCursorVisible()
	case "enter":
		m.tabFiltering = false
		m.tabFocus = tabFocusFilter
		if len(m.tabFiltered) > 0 {
			return m.activateTabResult()
		}
	case "backspace":
		if len(m.tabFilter) > 0 {
			m.tabFilter = m.tabFilter[:len(m.tabFilter)-1]
			m.tabFiltered = m.buildTabFiltered()
			m.tabCursor = clampInt(m.tabCursor, 0, max(len(m.tabFiltered)-1, 0))
			m.ensureTabCursorVisible()
		}
	case "up":
		m.moveTabCursor(-1)
	case "down":
		m.moveTabCursor(1)
	default:
		changed := false
		for _, r := range key.Runes {
			if !unicode.IsPrint(r) {
				continue
			}
			if len(m.tabFilter) >= maxInputLen {
				break
			}
			m.tabFilter = append(m.tabFilter, r)
			changed = true
		}
		if changed {
			m.tabFiltered = m.buildTabFiltered()
			m.tabCursor = 0
			m.tabScroll = 0
		}
	}
	return nil
}

// tabToggleKeys maps key strings to their corresponding tab filter toggle.
var tabToggleKeys = map[string]toggleIndex{
	"alt+c": toggleCase,
	"alt+w": toggleWord,
	"alt+.": toggleRegex,
}

// handleTabToggleKey checks if a key string matches a tab filter toggle
// shortcut and flips the toggle, triggering a refilter.
func (m *Model) handleTabToggleKey(ks string) (tea.Cmd, bool) {
	idx, ok := tabToggleKeys[ks]
	if !ok {
		return nil, false
	}
	m.tabToggleActive[idx] = !m.tabToggleActive[idx]
	m.tabFiltered = m.buildTabFiltered()
	m.tabCursor = clampInt(m.tabCursor, 0, max(len(m.tabFiltered)-1, 0))
	m.ensureTabCursorVisible()
	return nil, true
}

// handleTabToggleInput processes keys when the toggle badges zone has focus.
// Left/Right moves the cursor across badges; Space flips the focused toggle.
func (m *Model) handleTabToggleInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "left":
		m.moveTabToggleCursor(-1)
	case "right":
		m.moveTabToggleCursor(1)
	case " ":
		m.tabToggleActive[m.tabToggleCursor] = !m.tabToggleActive[m.tabToggleCursor]
		m.tabFiltered = m.buildTabFiltered()
		m.tabCursor = clampInt(m.tabCursor, 0, max(len(m.tabFiltered)-1, 0))
		m.ensureTabCursorVisible()
	case "esc":
		m.tabFiltering = false
		m.tabFilter = nil
		m.tabFocus = tabFocusFilter
		m.tabFiltered = m.buildTabFiltered()
		m.tabCursor = clampInt(m.tabCursor, 0, max(len(m.tabFiltered)-1, 0))
		m.ensureTabCursorVisible()
	default:
		// Printable runes switch focus back to filter input and insert.
		if key.Type == tea.KeyRunes {
			m.tabFocus = tabFocusFilter
			return m.handleTabFilterInput(key)
		}
	}
	return nil
}

// advanceTabFocus moves focus forward through the tabs panel zones.
// Within the toggles zone, Tab advances to the next badge before cycling.
func (m *Model) advanceTabFocus() {
	if m.tabFocus == tabFocusToggles {
		pos := tabToggleTabPos(m.tabToggleCursor)
		if pos < toolbarToggleCount-1 {
			m.tabToggleCursor = tabToggleOrder[pos+1]
			return
		}
	}
	switch m.tabFocus {
	case tabFocusFilter:
		m.tabFocus = tabFocusToggles
		m.tabToggleCursor = tabToggleOrder[0]
	case tabFocusToggles:
		m.tabFocus = tabFocusFilter
	}
}

// retreatTabFocus moves focus backward through the tabs panel zones.
// Within the toggles zone, Shift+Tab retreats to the previous badge.
func (m *Model) retreatTabFocus() {
	if m.tabFocus == tabFocusToggles {
		pos := tabToggleTabPos(m.tabToggleCursor)
		if pos > 0 {
			m.tabToggleCursor = tabToggleOrder[pos-1]
			return
		}
	}
	switch m.tabFocus {
	case tabFocusFilter:
		m.tabFocus = tabFocusToggles
		m.tabToggleCursor = tabToggleOrder[toolbarToggleCount-1]
	case tabFocusToggles:
		m.tabFocus = tabFocusFilter
	}
}

// moveTabToggleCursor moves the toggle cursor left or right, clamped.
func (m *Model) moveTabToggleCursor(delta int) {
	pos := tabToggleTabPos(m.tabToggleCursor)
	next := clampInt(pos+delta, 0, toolbarToggleCount-1)
	m.tabToggleCursor = tabToggleOrder[next]
}

// tabToggleTabPos returns the position of idx within tabToggleOrder.
func tabToggleTabPos(idx toggleIndex) int {
	for i, t := range tabToggleOrder {
		if t == idx {
			return i
		}
	}
	return 0
}

// moveTabCursor moves the cursor within filtered tab items.
func (m *Model) moveTabCursor(delta int) {
	n := len(m.tabFiltered)
	if n == 0 {
		return
	}
	m.tabCursor = clampInt(m.tabCursor+delta, 0, n-1)
	m.ensureTabCursorVisible()
}

// activateTabResult handles Enter on a tab entry.
// Exits tabs mode and returns a FileOpenMsg for the selected tab.
func (m *Model) activateTabResult() tea.Cmd {
	if m.tabCursor >= len(m.tabFiltered) {
		return nil
	}
	idx := m.tabFiltered[m.tabCursor]
	if idx >= len(m.tabPaths) {
		return nil
	}
	path := m.tabPaths[idx]
	name := filepath.Base(path)
	lang := langFromPath(path)

	m.ExitTabs()

	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     path,
			Name:     name,
			Language: lang,
		}
	}
}

// clickTabsMode handles clicks in tabs mode body area.
func (m *Model) clickTabsMode(viewX, viewY int) tea.Cmd {
	top := treeChromeHeight
	if m.tabFiltering {
		top = tabsChromeHeight
	}
	bodyY := viewY - top
	if bodyY < 0 {
		return nil
	}
	idx := m.tabScroll + bodyY
	if idx < 0 || idx >= len(m.tabFiltered) {
		return nil
	}
	m.tabCursor = idx
	// Close icon occupies the last 2 columns ("✕" + " ").
	contentWidth := max(m.width, 1)
	if viewX >= contentWidth-2 {
		return m.closeTabEntry(idx)
	}
	return m.activateTabResult()
}

// closeTabEntry emits a TabCloseRequestMsg for the tab at filteredIdx.
func (m *Model) closeTabEntry(filteredIdx int) tea.Cmd {
	if filteredIdx >= len(m.tabFiltered) {
		return nil
	}
	pathIdx := m.tabFiltered[filteredIdx]
	if pathIdx >= len(m.tabPaths) {
		return nil
	}
	path := m.tabPaths[pathIdx]
	return func() tea.Msg {
		return msg.TabCloseRequestMsg{Path: path}
	}
}

// handleSearchKey processes keys in search mode, dispatching to the
// appropriate focus zone handler.
func (m *Model) handleSearchKey(key tea.KeyMsg) tea.Cmd {
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()
	switch key.String() {
	case "ctrl+u":
		m.exitSearch()
		return nil
	case "up":
		m.moveSearchCursor(-1)
		return nil
	case "down":
		m.moveSearchCursor(1)
		return nil
	case "tab":
		m.advanceSearchFocus()
		return nil
	case "shift+tab":
		m.retreatSearchFocus()
		return nil
	case "enter":
		return m.handleSearchEnter()
	}
	return m.dispatchToFocusZone(key)
}

// advanceSearchFocus moves focus forward. Within the toggles zone, Tab
// advances through badges in tab order: [re]→[Aa]→[ab]→[.*] before
// jumping to the next focus zone.
func (m *Model) advanceSearchFocus() {
	if m.searchFocus == focusToggles {
		pos := toggleTabPos(m.toggleCursor)
		if pos < toggleCount-1 {
			m.toggleCursor = toggleTabOrder[pos+1]
			return
		}
	}
	m.searchFocus = m.nextFocus(1)
	if m.searchFocus == focusToggles {
		m.toggleCursor = toggleTabOrder[0]
	}
}

// retreatSearchFocus moves focus backward. Within the toggles zone,
// Shift+Tab retreats through badges in reverse tab order:
// [.*]→[ab]→[Aa]→[re] before jumping to the previous focus zone.
func (m *Model) retreatSearchFocus() {
	if m.searchFocus == focusToggles {
		pos := toggleTabPos(m.toggleCursor)
		if pos > 0 {
			m.toggleCursor = toggleTabOrder[pos-1]
			return
		}
	}
	m.searchFocus = m.nextFocus(-1)
	if m.searchFocus == focusToggles {
		m.toggleCursor = toggleTabOrder[toggleCount-1]
	}
}

// nextFocus cycles through the active focus order by the given delta (+1/-1).
func (m *Model) nextFocus(delta int) searchFocus {
	order := m.focusOrder()
	n := len(order)
	idx := 0
	for i, f := range order {
		if f == m.searchFocus {
			idx = i
			break
		}
	}
	return order[(idx+delta+n)%n]
}

// focusOrder returns the focus cycling order for the current mode.
func (m *Model) focusOrder() []searchFocus {
	if m.mode == viewReplace {
		return replaceFocusOrder[:]
	}
	return searchFocusOrder[:]
}

// handleSearchEnter dispatches Enter based on the current focus zone.
func (m *Model) handleSearchEnter() tea.Cmd {
	switch m.searchFocus {
	case focusToggles:
		return m.toggleBadge(m.toggleCursor)
	default:
		return m.activateSearchResult()
	}
}

// dispatchToFocusZone routes key input to the handler for the active zone.
func (m *Model) dispatchToFocusZone(key tea.KeyMsg) tea.Cmd {
	switch m.searchFocus {
	case focusQuery:
		return m.handleQueryInput(key)
	case focusToggles:
		return m.handleToggleInput(key)
	case focusScope:
		return m.handleScopeInput(key)
	}
	return nil
}

// handleQueryInput processes keys when the search query input is focused.
func (m *Model) handleQueryInput(key tea.KeyMsg) tea.Cmd {
	if key.String() == "backspace" {
		return m.deleteQueryChar()
	}
	if key.Type != tea.KeyRunes && key.Type != tea.KeySpace {
		return nil
	}
	m.appendToQuery([]rune(key.String()))
	return m.refilter()
}

// appendToQuery appends runes to the search query, capped at maxInputLen.
func (m *Model) appendToQuery(runes []rune) {
	remaining := maxInputLen - len(m.searchQuery)
	if remaining <= 0 {
		return
	}
	if len(runes) > remaining {
		runes = runes[:remaining]
	}
	m.searchQuery = append(m.searchQuery, runes...)
}

// deleteQueryChar removes the last character from the search query,
// or exits search mode if the query is empty.
func (m *Model) deleteQueryChar() tea.Cmd {
	if len(m.searchQuery) == 0 {
		m.exitSearch()
		return nil
	}
	m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
	return m.refilter()
}

// handleToggleInput processes keys when the toggle badges are focused.
func (m *Model) handleToggleInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "left":
		m.moveToggleCursor(-1)
	case "right":
		m.moveToggleCursor(1)
	case " ":
		return m.toggleBadge(m.toggleCursor)
	default:
		if key.Type == tea.KeyRunes {
			m.searchFocus = focusQuery
			m.appendToQuery([]rune(key.String()))
			return m.refilter()
		}
	}
	return nil
}

// handleScopeInput processes keys when the scope path input is focused.
// Supports cursor movement via arrow keys, insertion at cursor, and
// backspace-before-cursor deletion.
func (m *Model) handleScopeInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "left":
		m.scopeCursor = max(m.scopeCursor-1, 0)
		return nil
	case "right":
		m.scopeCursor = min(m.scopeCursor+1, len(m.scopeQuery))
		return nil
	case "backspace":
		if m.scopeCursor > 0 && m.scopeCursor <= len(m.scopeQuery) {
			m.scopeQuery = append(m.scopeQuery[:m.scopeCursor-1], m.scopeQuery[m.scopeCursor:]...)
			m.scopeCursor--
			return m.refilter()
		}
		return nil
	}
	if key.Type != tea.KeyRunes && key.Type != tea.KeySpace {
		return nil
	}
	m.insertAtScopeCursor([]rune(key.String()))
	return m.refilter()
}

// insertAtScopeCursor inserts runes at the current scope cursor position,
// capped at maxInputLen.
func (m *Model) insertAtScopeCursor(runes []rune) {
	remaining := maxInputLen - len(m.scopeQuery)
	if remaining <= 0 {
		return
	}
	if len(runes) > remaining {
		runes = runes[:remaining]
	}
	tail := append(runes, m.scopeQuery[m.scopeCursor:]...)
	m.scopeQuery = append(m.scopeQuery[:m.scopeCursor], tail...)
	m.scopeCursor += len(runes)
}

// toggleTabPos returns the position of idx within toggleTabOrder.
func toggleTabPos(idx toggleIndex) int {
	for i, t := range toggleTabOrder {
		if t == idx {
			return i
		}
	}
	return 0
}

// moveToggleCursor moves the toggle badge cursor left or right through
// the tab order, clamping to the valid range.
func (m *Model) moveToggleCursor(delta int) {
	pos := toggleTabPos(m.toggleCursor)
	next := clampInt(pos+delta, 0, toggleCount-1)
	m.toggleCursor = toggleTabOrder[next]
}

// toggleBadge flips the state of the given toggle and triggers refilter.
func (m *Model) toggleBadge(idx toggleIndex) tea.Cmd {
	m.toggleActive[idx] = !m.toggleActive[idx]
	return m.refilter()
}

// ---------------------------------------------------------------------------
// Tree navigation
// ---------------------------------------------------------------------------

// moveCursor moves the cursor by delta, clamping to bounds and scrolling.
func (m *Model) moveCursor(delta int) {
	m.cursor = clampInt(m.cursor+delta, 0, max(len(m.entries)-1, 0))
	m.ensureCursorVisible()
}

// activateEntry handles Enter: toggles directories, opens files in the code viewer.
func (m *Model) activateEntry() tea.Cmd {
	if m.cursor >= len(m.entries) {
		return nil
	}
	entry := &m.entries[m.cursor]

	if entry.IsDir {
		m.toggleExpand()
		return nil
	}

	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     entry.Path,
			Name:     entry.Name,
			Language: langFromPath(entry.Path),
		}
	}
}

// SelectedDir returns the directory path for the current cursor position.
// If the cursor is on a directory, that directory's path is returned.
// If the cursor is on a file, the file's parent directory is returned.
// Falls back to rootPath when there are no entries.
func (m *Model) SelectedDir() string {
	if m.cursor >= len(m.entries) {
		return m.rootPath
	}
	entry := &m.entries[m.cursor]
	if entry.IsDir {
		return entry.Path
	}
	return filepath.Dir(entry.Path)
}

// requestRename activates the inline rename input footer, pre-filled with
// the current entry name.
func (m *Model) requestRename() tea.Cmd {
	if m.cursor >= len(m.entries) {
		return nil
	}
	entry := &m.entries[m.cursor]
	m.renameActive = true
	m.renamePath = entry.Path
	m.renameIsDir = entry.IsDir
	m.renameInput = []rune(entry.Name)
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()
	return nil
}

// requestDelete activates the inline delete confirmation footer.
func (m *Model) requestDelete() tea.Cmd {
	if m.cursor >= len(m.entries) {
		return nil
	}
	entry := &m.entries[m.cursor]
	m.deleteConfirm = true
	m.deleteConfirmPath = entry.Path
	m.deleteConfirmDir = entry.IsDir
	return nil
}

// ---------------------------------------------------------------------------
// New-entry inline input
// ---------------------------------------------------------------------------

// handlePendingNewEntry resolves the Alt+N chord: F activates file input,
// D activates directory input, anything else cancels.
func (m *Model) handlePendingNewEntry(key tea.KeyMsg) tea.Cmd {
	m.pendingNewEntry = false
	switch key.String() {
	case "f":
		m.EnterNewEntry(m.SelectedDir(), false)
	case "d":
		m.EnterNewEntry(m.SelectedDir(), true)
	}
	return nil
}

// EnterNewEntry activates the inline input footer for creating a new file
// or directory inside dir.
func (m *Model) EnterNewEntry(dir string, isDir bool) {
	m.newEntryActive = true
	m.newEntryIsDir = isDir
	m.newEntryDir = dir
	m.newEntryInput = nil
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()
}

// exitNewEntry cancels the inline input without creating anything.
func (m *Model) exitNewEntry() {
	m.newEntryActive = false
	m.newEntryInput = nil
	m.newEntryDir = ""
}

// handleNewEntryKey processes keys while the new-entry input is active.
func (m *Model) handleNewEntryKey(key tea.KeyMsg) tea.Cmd {
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()

	switch key.String() {
	case "escape":
		m.exitNewEntry()
		return nil
	case "enter":
		return m.commitNewEntry()
	case "backspace":
		if len(m.newEntryInput) > 0 {
			m.newEntryInput = m.newEntryInput[:len(m.newEntryInput)-1]
		}
		return nil
	}
	// Append printable runes.
	for _, r := range key.Runes {
		if len(m.newEntryInput) >= maxInputLen {
			break
		}
		m.newEntryInput = append(m.newEntryInput, r)
	}
	return nil
}

// commitNewEntry creates the file or directory on disk, inserts it into the
// tree at the correct position, and emits a result message.
func (m *Model) commitNewEntry() tea.Cmd {
	name := strings.TrimSpace(string(m.newEntryInput))
	if name == "" {
		m.exitNewEntry()
		return nil
	}
	fullPath := filepath.Join(m.newEntryDir, name)
	isDir := m.newEntryIsDir

	if isDir {
		if err := os.MkdirAll(fullPath, 0o755); err != nil {
			m.exitNewEntry()
			return nil
		}
	} else {
		// Ensure parent directory exists for nested paths.
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			m.exitNewEntry()
			return nil
		}
		if err := os.WriteFile(fullPath, nil, 0o644); err != nil {
			m.exitNewEntry()
			return nil
		}
	}

	dir := m.newEntryDir
	m.exitNewEntry()

	// Insert the new entry into the live tree at the correct sorted position
	// within its parent directory, preserving existing expand/collapse state.
	m.insertNewTreeEntry(dir, fullPath, name, isDir)

	return func() tea.Msg {
		return msg.FileTreeEntryCreatedMsg{Path: fullPath, IsDir: isDir}
	}
}

// insertNewTreeEntry inserts a newly created entry into the flattened tree
// at the correct sorted position among its siblings, then moves the cursor
// to the new entry.
func (m *Model) insertNewTreeEntry(parentDir, fullPath, name string, isDir bool) {
	depth := 0
	startPos := 0

	if parentDir == m.rootPath {
		// Root-level insertion: siblings are all depth-0 entries.
		depth = 0
		startPos = 0
	} else {
		parentIdx, parentFound := m.pathIndex[parentDir]
		if !parentFound {
			return
		}
		parent := &m.entries[parentIdx]
		if !parent.IsDir {
			return
		}
		// Ensure parent is expanded so the child is visible.
		if !parent.Expanded {
			m.expandAt(parentIdx)
			parentIdx = m.pathIndex[parentDir]
		}
		depth = parent.Depth + 1
		startPos = parentIdx + 1
	}

	newEntry := Entry{
		Name:  name,
		Path:  fullPath,
		IsDir: isDir,
		Depth: depth,
	}

	// Walk siblings to find the sorted insertion point.
	// Directories sort before files; within each group, sort alphabetically.
	insertPos := startPos
	for insertPos < len(m.entries) && m.entries[insertPos].Depth >= depth {
		sib := &m.entries[insertPos]
		if sib.Depth > depth {
			insertPos++
			continue
		}
		if isDir && !sib.IsDir {
			break
		}
		if !isDir && sib.IsDir {
			insertPos++
			continue
		}
		if strings.ToLower(name) < strings.ToLower(sib.Name) {
			break
		}
		insertPos++
	}

	m.entries = sliceInsert(m.entries, insertPos, []Entry{newEntry})
	m.rebuildPathIndex()
	m.cursor = insertPos
	m.ensureCursorVisible()
}

// ---------------------------------------------------------------------------
// Delete confirmation
// ---------------------------------------------------------------------------

// exitDeleteConfirm cancels the delete confirmation.
func (m *Model) exitDeleteConfirm() {
	m.deleteConfirm = false
	m.deleteConfirmPath = ""
}

// handleDeleteConfirmKey processes keys while the delete confirmation is shown.
func (m *Model) handleDeleteConfirmKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "y":
		return m.commitDelete()
	case "n", "escape":
		m.exitDeleteConfirm()
	}
	return nil
}

// commitDelete removes the file or directory from disk and the tree.
func (m *Model) commitDelete() tea.Cmd {
	path := m.deleteConfirmPath
	isDir := m.deleteConfirmDir
	m.exitDeleteConfirm()

	if isDir {
		if err := os.RemoveAll(path); err != nil {
			return nil
		}
	} else {
		if err := os.Remove(path); err != nil {
			return nil
		}
	}

	m.removeTreeEntry(path)

	return func() tea.Msg {
		return msg.FileTreeEntryDeletedMsg{Path: path, IsDir: isDir}
	}
}

// removeTreeEntry removes an entry (and its descendants if a directory) from
// the flattened tree and adjusts the cursor.
func (m *Model) removeTreeEntry(path string) {
	idx, ok := m.pathIndex[path]
	if !ok {
		return
	}

	// Determine range to remove: the entry itself plus any expanded children.
	end := idx + 1
	depth := m.entries[idx].Depth
	for end < len(m.entries) && m.entries[end].Depth > depth {
		end++
	}

	m.entries = sliceRemove(m.entries, idx, end)
	m.rebuildPathIndex()

	if m.cursor >= len(m.entries) {
		m.cursor = max(len(m.entries)-1, 0)
	}
	m.ensureCursorVisible()
}

// ---------------------------------------------------------------------------
// Rename inline input
// ---------------------------------------------------------------------------

// exitRename cancels the rename input.
func (m *Model) exitRename() {
	m.renameActive = false
	m.renamePath = ""
	m.renameInput = nil
}

// handleRenameKey processes keys while the rename input is active.
func (m *Model) handleRenameKey(key tea.KeyMsg) tea.Cmd {
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()

	switch key.String() {
	case "escape":
		m.exitRename()
		return nil
	case "enter":
		return m.commitRename()
	case "backspace":
		if len(m.renameInput) > 0 {
			m.renameInput = m.renameInput[:len(m.renameInput)-1]
		}
		return nil
	}
	for _, r := range key.Runes {
		if len(m.renameInput) >= maxInputLen {
			break
		}
		m.renameInput = append(m.renameInput, r)
	}
	return nil
}

// commitRename renames the file or directory on disk and updates the tree.
func (m *Model) commitRename() tea.Cmd {
	newName := strings.TrimSpace(string(m.renameInput))
	if newName == "" || newName == filepath.Base(m.renamePath) {
		m.exitRename()
		return nil
	}

	oldPath := m.renamePath
	isDir := m.renameIsDir
	newPath := filepath.Join(filepath.Dir(oldPath), newName)

	if err := os.Rename(oldPath, newPath); err != nil {
		m.exitRename()
		return nil
	}

	m.exitRename()

	// Update the entry in-place in the tree.
	if idx, ok := m.pathIndex[oldPath]; ok {
		m.entries[idx].Name = newName
		m.entries[idx].Path = newPath
		m.rebuildPathIndex()
		m.cursor = idx
		m.ensureCursorVisible()
	}

	return func() tea.Msg {
		return msg.FileTreeEntryRenamedMsg{OldPath: oldPath, NewPath: newPath, IsDir: isDir}
	}
}

// expandDir expands the directory at the cursor if it is collapsed.
func (m *Model) expandDir() {
	if m.cursor >= len(m.entries) {
		return
	}
	entry := &m.entries[m.cursor]
	if entry.IsDir && !entry.Expanded {
		m.expandAt(m.cursor)
	}
}

// toggleExpand expands or collapses the directory at the cursor.
func (m *Model) toggleExpand() {
	if m.cursor >= len(m.entries) {
		return
	}
	entry := &m.entries[m.cursor]
	if !entry.IsDir {
		return
	}

	if entry.Expanded {
		m.collapseAt(m.cursor)
	} else {
		m.expandAt(m.cursor)
	}
}

// collapseOrParent collapses the current directory or moves to parent.
func (m *Model) collapseOrParent() {
	if m.cursor >= len(m.entries) {
		return
	}
	entry := &m.entries[m.cursor]

	// If it's an expanded dir, collapse it.
	if entry.IsDir && entry.Expanded {
		m.collapseAt(m.cursor)
		return
	}

	// Otherwise navigate to parent directory.
	m.navigateToParent()
}


// navigateToParent moves cursor to the parent directory entry.
func (m *Model) navigateToParent() {
	parentIdx := m.findParent()
	if parentIdx < 0 {
		return
	}
	m.cursor = parentIdx
	m.ensureCursorVisible()
}

// findParent returns the index of the parent directory of the current cursor entry,
// or -1 if no parent exists (cursor is at root depth).
func (m *Model) findParent() int {
	entry := m.entries[m.cursor]
	parentDepth := entry.Depth - 1
	for i := m.cursor - 1; i >= 0; i-- {
		if m.entries[i].IsDir && m.entries[i].Depth == parentDepth {
			return i
		}
	}
	return -1
}

// expandAt inserts children of the directory at idx.
func (m *Model) expandAt(idx int) {
	entry := &m.entries[idx]
	entry.Expanded = true

	children := m.readDir(entry.Path, entry.Depth+1)
	if len(children) == 0 {
		return
	}

	// Insert children after idx.
	insertPos := idx + 1
	m.entries = sliceInsert(m.entries, insertPos, children)
	m.rebuildPathIndex()
}

// collapseAt removes all descendants of the directory at idx.
func (m *Model) collapseAt(idx int) {
	entry := &m.entries[idx]
	entry.Expanded = false

	// Remove all entries after idx with depth > entry.Depth.
	removeStart := idx + 1
	removeEnd := removeStart
	for removeEnd < len(m.entries) && m.entries[removeEnd].Depth > entry.Depth {
		removeEnd++
	}
	m.entries = sliceRemove(m.entries, removeStart, removeEnd)
	m.rebuildPathIndex()

	// Clamp cursor if it was in the removed range.
	m.cursor = clampInt(m.cursor, 0, max(len(m.entries)-1, 0))
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// enterSearch snapshots the current tree state, walks the filesystem
// (gitignore-aware) to build a flat file index, creates the async search
// worker, and switches to search mode.
func (m *Model) enterSearch() {
	m.savedEntries = make([]Entry, len(m.entries))
	copy(m.savedEntries, m.entries)
	m.savedCursor = m.cursor
	m.savedScroll = m.scroll

	// Gitignore-aware walk: prunes entire ignored directory trees.
	root := m.rootPath
	if root == "" {
		root = "."
	}
	m.searchSource = collectSearchableFiles(root)

	m.mode = viewSearch
	m.searchQuery = nil
	m.searchItems = nil
	m.searchCursor = 0
	m.searchScroll = 0
	m.searchNumWidth = 0
	m.searchDone = true
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()

	// Create async search worker.
	m.searchWorker = NewSearchWorker()

	// Initialize performance caches.
	m.searchVersion = 0
	m.searchCache = newFileCache()
	m.scopeIndex, m.scopeSorted = buildScopeIndex(m.searchSource, m.rootPath)

	// Initialize toolbar state.
	m.searchFocus = focusQuery
	m.toggleActive = [toggleCount]bool{}
	m.toggleCursor = toggleCase
	m.scopeQuery = nil
	m.scopeCursor = 0
	m.compiledRegexp = nil
}

// exitSearch stops the async worker, restores the tree state, and clears
// all search-related fields.
func (m *Model) exitSearch() {
	// Stop async worker and release.
	if m.searchWorker != nil {
		m.searchWorker.Stop()
		m.searchWorker = nil
	}

	m.entries = m.savedEntries
	m.rebuildPathIndex()
	m.cursor = m.savedCursor
	m.scroll = m.savedScroll
	m.mode = viewTree
	m.searchQuery = nil
	m.searchSource = nil
	m.searchItems = nil
	m.savedEntries = nil
	m.searchNumWidth = 0
	m.searchDone = true

	// Release caches.
	m.searchVersion = 0
	m.searchCache = nil
	m.scopeIndex = nil
	m.scopeSorted = nil

	// Clear toolbar state.
	m.searchFocus = focusQuery
	m.toggleActive = [toggleCount]bool{}
	m.toggleCursor = toggleCase
	m.scopeQuery = nil
	m.scopeCursor = 0
	m.compiledRegexp = nil
}

// ---------------------------------------------------------------------------
// Replace mode (multi-file find and replace)
// ---------------------------------------------------------------------------

// enterReplace enters multi-file replace mode, reusing the search
// infrastructure for file collection and match display.
func (m *Model) enterReplace() {
	m.enterSearch()
	m.mode = viewReplace
	m.replaceInput = nil
	m.searchFocus = focusQuery
}

// exitReplace cleans up replace-specific state and delegates to exitSearch.
func (m *Model) exitReplace() {
	m.replaceInput = nil
	m.exitSearch()
}

// ToggleReplace enters or exits multi-file replace mode.
func (m *Model) ToggleSearch() {
	if m.mode == viewSearch {
		m.exitSearch()
		return
	}
	m.enterSearch()
}

func (m *Model) ToggleReplace() {
	if m.mode == viewReplace {
		m.exitReplace()
		return
	}
	m.enterReplace()
}

// InReplaceMode reports whether the file tree is in multi-file replace mode.
func (m *Model) InReplaceMode() bool { return m.mode == viewReplace }

// handleReplaceKey processes keys in replace mode. It reuses the search
// infrastructure for match display and navigation, adding replace actions.
func (m *Model) handleReplaceKey(key tea.KeyMsg) tea.Cmd {
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()
	switch key.String() {
	case "ctrl+u":
		m.exitReplace()
		return nil
	case "up":
		m.moveSearchCursor(-1)
		return nil
	case "down":
		m.moveSearchCursor(1)
		return nil
	case "tab":
		m.advanceSearchFocus()
		return nil
	case "shift+tab":
		m.retreatSearchFocus()
		return nil
	case "enter":
		return m.handleReplaceEnter()
	case "ctrl+enter":
		return m.replaceAllSearchMatches()
	}
	return m.dispatchToReplaceFocusZone(key)
}

// handleReplaceEnter dispatches Enter based on the current focus zone.
func (m *Model) handleReplaceEnter() tea.Cmd {
	switch m.searchFocus {
	case focusToggles:
		return m.toggleBadge(m.toggleCursor)
	case focusReplace:
		return m.replaceCurrentSearchMatch()
	case focusBtnOne:
		m.flashBtn(focusBtnOne)
		return m.replaceCurrentSearchMatch()
	case focusBtnAll:
		m.flashBtn(focusBtnAll)
		return m.replaceAllSearchMatches()
	default:
		return m.activateSearchResult()
	}
}

// dispatchToReplaceFocusZone routes key input to the handler for the active
// focus zone in replace mode.
func (m *Model) dispatchToReplaceFocusZone(key tea.KeyMsg) tea.Cmd {
	switch m.searchFocus {
	case focusQuery:
		return m.handleQueryInput(key)
	case focusReplace:
		return m.handleReplaceInput(key)
	case focusToggles:
		return m.handleToggleInput(key)
	case focusBtnOne, focusBtnAll:
		return m.handleReplaceBtnInput(key)
	case focusScope:
		return m.handleScopeInput(key)
	}
	return nil
}

// handleReplaceInput processes keys when the replacement text input is focused.
func (m *Model) handleReplaceInput(key tea.KeyMsg) tea.Cmd {
	if key.String() == "backspace" {
		if len(m.replaceInput) > 0 {
			m.replaceInput = m.replaceInput[:len(m.replaceInput)-1]
		}
		return nil
	}
	if key.Type != tea.KeyRunes && key.Type != tea.KeySpace {
		return nil
	}
	m.appendToReplace([]rune(key.String()))
	return nil
}

// appendToReplace appends runes to the replacement input, capped at maxInputLen.
func (m *Model) appendToReplace(runes []rune) {
	remaining := maxInputLen - len(m.replaceInput)
	if remaining <= 0 {
		return
	}
	if len(runes) > remaining {
		runes = runes[:remaining]
	}
	m.replaceInput = append(m.replaceInput, runes...)
}

// handleReplaceBtnInput processes keys when a replace button is focused.
// Space activates the button; left/right navigates between [→1] and [→*].
func (m *Model) handleReplaceBtnInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case " ":
		m.flashBtn(m.searchFocus)
		if m.searchFocus == focusBtnOne {
			return m.replaceCurrentSearchMatch()
		}
		return m.replaceAllSearchMatches()
	case "left":
		m.searchFocus = focusBtnOne
	case "right":
		m.searchFocus = focusBtnAll
	}
	return nil
}

// flashBtn records a flash on the given replace button for visual feedback.
func (m *Model) flashBtn(btn searchFocus) {
	m.btnFlash = btn
	m.btnFlashAt = time.Now()
}

// replaceCurrentSearchMatch replaces the match at the current search cursor
// position on disk and advances to the next match. When the replacement
// changes the file, the affected file is re-grepped and its items patched
// in place (no full refilter). When the replacement is a no-op (identical
// text), the item is removed from the list and the cursor advances so the
// user sees consistent step-through behavior.
func (m *Model) replaceCurrentSearchMatch() tea.Cmd {
	if len(m.searchQuery) == 0 || m.searchCursor >= len(m.searchItems) {
		return nil
	}
	item := m.searchItems[m.searchCursor]
	if item.kind != searchItemMatch {
		return nil
	}
	cfg := m.buildMatchConfig(string(m.searchQuery))
	replaced := m.replaceInFile(item.path, string(m.searchQuery), string(m.replaceInput), cfg, item.line)
	if replaced {
		m.searchCache.evict(item.path)
		m.patchFileItems(item.path, cfg)
	} else {
		m.removeSearchItem(m.searchCursor)
	}
	m.seekFirstMatchFrom(m.searchCursor)
	if replaced {
		return func() tea.Msg {
			return msg.FileReplacedMsg{Path: item.path}
		}
	}
	return nil
}

// patchFileItems re-greps a single file and splices its updated match items
// into searchItems, replacing the old entries for that path. Preserves the
// position of the file group within the list. O(n) where n ≤ maxTotalMatches.
func (m *Model) patchFileItems(path string, cfg matchConfig) {
	query := string(m.searchQuery)
	newMatches := m.grepFileWithCache(path, query, cfg)

	// Locate the span [start, end) covering the old file group (header + matches).
	start, end := m.fileGroupSpan(path)
	if start < 0 {
		return
	}

	// Build replacement slice: header + new matches (if any).
	var replacement []searchItem
	if len(newMatches) > 0 {
		replacement = make([]searchItem, 0, len(newMatches)+1)
		replacement = append(replacement, searchItem{kind: searchItemFile, path: path})
		for _, match := range newMatches {
			replacement = append(replacement, searchItem{
				kind: searchItemMatch,
				path: path,
				line: match.line,
				text: match.text,
			})
		}
	}

	// Splice: remove old [start, end), insert replacement at start.
	tail := append(replacement, m.searchItems[end:]...)
	m.searchItems = append(m.searchItems[:start], tail...)
	m.compactSearchItems()
	m.recalcSearchNumWidth()
}

// fileGroupSpan returns the half-open index range [start, end) covering
// the file header and all its match lines for the given path. The preceding
// gap (if any) is included in start. Returns (-1, -1) if not found.
func (m *Model) fileGroupSpan(path string) (int, int) {
	headerIdx := -1
	for i, item := range m.searchItems {
		if item.kind == searchItemFile && item.path == path {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 {
		return -1, -1
	}
	// Include preceding gap.
	start := headerIdx
	if start > 0 && m.searchItems[start-1].kind == searchItemGap {
		start--
	}
	// Find end: first item after the match run.
	end := headerIdx + 1
	for end < len(m.searchItems) && m.searchItems[end].kind == searchItemMatch {
		end++
	}
	return start, end
}

// removeSearchItem removes the item at idx from searchItems and cleans up
// any orphaned file headers or gaps that result. O(n) where n ≤ maxTotalMatches.
func (m *Model) removeSearchItem(idx int) {
	if idx < 0 || idx >= len(m.searchItems) {
		return
	}
	m.searchItems = append(m.searchItems[:idx], m.searchItems[idx+1:]...)
	m.compactSearchItems()
}

// compactSearchItems removes orphaned file headers (headers with no
// subsequent match before the next gap/header/end) and normalizes gaps
// (no leading gaps, no trailing gaps, no consecutive gaps). In-place,
// single pass, O(n) where n ≤ maxTotalMatches.
func (m *Model) compactSearchItems() {
	items := m.searchItems
	w := 0
	for r := 0; r < len(items); r++ {
		switch items[r].kind {
		case searchItemGap:
			if w > 0 && items[w-1].kind != searchItemGap {
				items[w] = items[r]
				w++
			}
		case searchItemFile:
			if fileHasMatches(items, r) {
				items[w] = items[r]
				w++
			}
		case searchItemMatch:
			items[w] = items[r]
			w++
		}
	}
	// Trim trailing gap.
	if w > 0 && items[w-1].kind == searchItemGap {
		w--
	}
	clear(items[w:])
	m.searchItems = items[:w]
}

// fileHasMatches reports whether the file header at idx has at least one
// match item before the next gap, header, or end of list.
func fileHasMatches(items []searchItem, idx int) bool {
	for i := idx + 1; i < len(items); i++ {
		if items[i].kind == searchItemMatch {
			return true
		}
		if items[i].kind == searchItemGap || items[i].kind == searchItemFile {
			return false
		}
	}
	return false
}

// recalcSearchNumWidth recomputes searchNumWidth from the current items.
func (m *Model) recalcSearchNumWidth() {
	maxLine := 0
	for _, item := range m.searchItems {
		if item.kind == searchItemMatch && item.line > maxLine {
			maxLine = item.line
		}
	}
	m.searchNumWidth = digitCount(maxLine)
}

// seekFirstMatchFrom positions the cursor on the nearest match item at or
// after pos. If no match exists at or after pos, wraps to the first match.
// If no matches remain, resets cursor and scroll to 0.
func (m *Model) seekFirstMatchFrom(pos int) {
	n := len(m.searchItems)
	if n == 0 {
		m.searchCursor = 0
		m.searchScroll = 0
		return
	}
	pos = clampInt(pos, 0, n-1)
	for i := pos; i < n; i++ {
		if m.searchItems[i].kind == searchItemMatch {
			m.searchCursor = i
			m.ensureSearchCursorVisible()
			return
		}
	}
	for i := range pos {
		if m.searchItems[i].kind == searchItemMatch {
			m.searchCursor = i
			m.ensureSearchCursorVisible()
			return
		}
	}
	m.searchCursor = 0
	m.ensureSearchCursorVisible()
}

// replaceAllSearchMatches replaces all matches across all files on disk.
func (m *Model) replaceAllSearchMatches() tea.Cmd {
	if len(m.searchQuery) == 0 || len(m.searchItems) == 0 {
		return nil
	}
	cfg := m.buildMatchConfig(string(m.searchQuery))
	query := string(m.searchQuery)
	replacement := string(m.replaceInput)

	// Collect unique file paths from match items.
	paths := m.collectMatchedPaths()
	filesChanged := 0
	totalReplaced := 0
	for _, path := range paths {
		n := m.replaceAllInFile(path, query, replacement, cfg)
		if n > 0 {
			filesChanged++
			totalReplaced += n
			m.searchCache.evict(path)
		}
	}
	m.refilterSync()
	return func() tea.Msg {
		return msg.MultiFileReplaceDoneMsg{
			FilesChanged:  filesChanged,
			TotalReplaced: totalReplaced,
		}
	}
}

// collectMatchedPaths returns the unique file paths from the current search results.
func (m *Model) collectMatchedPaths() []string {
	seen := make(map[string]struct{}, len(m.searchItems)/2)
	paths := make([]string, 0, len(m.searchItems)/2)
	for _, item := range m.searchItems {
		if item.path == "" {
			continue
		}
		if _, ok := seen[item.path]; ok {
			continue
		}
		seen[item.path] = struct{}{}
		paths = append(paths, item.path)
	}
	return paths
}

// replaceInFile replaces the first match on the given line in a file on disk.
// Returns true if a replacement was made. Preserves original line endings
// and file permissions.
func (m *Model) replaceInFile(path, query, replacement string, cfg matchConfig, lineNum int) bool {
	if cfg.compiled == nil {
		return false
	}
	data, mode, err := readFileWithMode(path)
	if err != nil {
		return false
	}
	eol := detectLineEnding(data)
	lines := splitLines(string(data), eol)
	idx := lineNum - 1 // searchMatch uses 1-based line numbers.
	if idx < 0 || idx >= len(lines) {
		return false
	}
	newLine := replaceFirstWithRe(lines[idx], cfg.compiled, replacement, cfg.useRegex)
	if newLine == lines[idx] {
		return false
	}
	lines[idx] = newLine
	return os.WriteFile(path, []byte(strings.Join(lines, eol)), mode) == nil
}

// replaceAllInFile replaces all matches in a file on disk.
// Returns the number of line replacements made. Preserves original line
// endings and file permissions.
func (m *Model) replaceAllInFile(path, query, replacement string, cfg matchConfig) int {
	if cfg.compiled == nil {
		return 0
	}
	data, mode, err := readFileWithMode(path)
	if err != nil {
		return 0
	}
	eol := detectLineEnding(data)
	lines := splitLines(string(data), eol)
	count := 0
	for i, line := range lines {
		newLine := replaceAllWithRe(line, cfg.compiled, replacement, cfg.useRegex)
		if newLine != line {
			lines[i] = newLine
			count++
		}
	}
	if count == 0 {
		return 0
	}
	if os.WriteFile(path, []byte(strings.Join(lines, eol)), mode) != nil {
		return 0
	}
	return count
}

// readFileWithMode reads a file and returns its contents along with the
// original file permissions, for faithful round-trip writes.
func readFileWithMode(path string) ([]byte, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}

// detectLineEnding returns the line-ending sequence used in data.
// Returns "\r\n" if CRLF is present, "\n" otherwise.
func detectLineEnding(data []byte) string {
	if bytes.Contains(data, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// splitLines splits content by the given line ending sequence.
func splitLines(content, eol string) []string {
	return strings.Split(content, eol)
}

// replaceFirstWithRe replaces only the first match on a line using a
// pre-compiled regex. For regex mode, backreferences in the replacement
// are expanded; for literal mode, the replacement is inserted verbatim.
func replaceFirstWithRe(line string, re *regexp.Regexp, replacement string, isRegex bool) string {
	loc := re.FindStringIndex(line)
	if loc == nil {
		return line
	}
	if isRegex {
		matched := line[loc[0]:loc[1]]
		expanded := re.ReplaceAllString(matched, replacement)
		return line[:loc[0]] + expanded + line[loc[1]:]
	}
	return line[:loc[0]] + replacement + line[loc[1]:]
}

// replaceAllWithRe replaces all matches on a line using a pre-compiled regex.
// For regex mode, backreferences are expanded; for literal mode, the
// replacement is inserted verbatim.
func replaceAllWithRe(line string, re *regexp.Regexp, replacement string, isRegex bool) string {
	if isRegex {
		return re.ReplaceAllString(line, replacement)
	}
	return re.ReplaceAllLiteralString(line, replacement)
}

// refilterSync runs the search pipeline synchronously (no debounce),
// used after replacements to immediately refresh results.
func (m *Model) refilterSync() {
	m.searchVersion++
	m.searchItems = nil
	m.searchNumWidth = 0
	m.compiledRegexp = nil

	query := string(m.searchQuery)
	if len(query) < minSearchQueryLen {
		m.searchCursor = 0
		m.searchScroll = 0
		return
	}

	cfg := m.buildMatchConfig(query)
	m.compiledRegexp = cfg.compiled
	m.grepSources(m.scopedSourcePaths(), query, cfg)
}

// clickReplaceMode handles clicks in replace mode, dispatching to search
// input, replace input, body, or toolbar based on Y position.
func (m *Model) clickReplaceMode(viewX, viewY int) tea.Cmd {
	bh := m.bodyHeight()

	// Layout: header(0) | search bar(1) | replace bar(2) | scope line(3) | divider(4) |
	//         body(5..5+bh-1) | separator(+0) | toolbar(+1) | summary(+2).
	footerBase := replaceChromeHeight + bh

	if viewY == headerHeight {
		m.searchFocus = focusQuery
		return nil
	}
	if viewY == headerHeight+1 {
		m.searchFocus = focusReplace
		return nil
	}
	if viewY == headerHeight+3 {
		return m.clickScopeLine(viewX)
	}

	bodyY := viewY - replaceChromeHeight
	if bodyY >= 0 && bodyY < bh {
		return m.clickSearchBody(bodyY)
	}

	if viewY-footerBase == footerToolbarOffset {
		return m.clickToolbar(viewX)
	}
	return nil
}


// refilter schedules a debounced search. Each call increments the version
// counter and starts a timer; only the latest version actually runs the grep.
// For short queries (below minSearchQueryLen), clears results immediately.
func (m *Model) refilter() tea.Cmd {
	m.searchVersion++

	if len(m.searchQuery) < minSearchQueryLen {
		m.searchItems = nil
		m.searchNumWidth = 0
		m.compiledRegexp = nil
		m.searchCursor = 0
		m.searchScroll = 0
		m.searchDone = true
		return nil
	}

	version := m.searchVersion
	return tea.Tick(searchDebounceInterval, func(time.Time) tea.Msg {
		return searchTickMsg{version: version}
	})
}


// buildMatchConfig constructs a matchConfig from the current toggle state
// and query content, implementing smart-case behavior. Always pre-compiles
// a regex for both search matching and replacement, applying case-insensitive
// and whole-word flags via regex syntax for correct Unicode handling.
func (m *Model) buildMatchConfig(query string) matchConfig {
	cfg := matchConfig{
		caseSensitive: m.toggleActive[toggleCase] || hasUpperCase(query),
		wholeWord:     m.toggleActive[toggleWord],
		useRegex:      m.toggleActive[toggleRegex],
	}
	cfg.compiled = compileMatchRegex(query, cfg)
	return cfg
}

// compileMatchRegex compiles the query into a regex suitable for both matching
// and replacement. For regex mode, compiles the raw pattern; for literal mode,
// escapes the query and adds \b word boundaries if needed. Case insensitivity
// is applied via (?i) flag for correct Unicode case folding.
func compileMatchRegex(query string, cfg matchConfig) *regexp.Regexp {
	pattern := query
	if !cfg.useRegex {
		pattern = regexp.QuoteMeta(query)
	}
	if cfg.wholeWord {
		pattern = `\b` + pattern + `\b`
	}
	if !cfg.caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

// scopedSourcePaths returns the subset of searchSource paths that match
// the scope prefix. Used by the async worker pipeline.
func (m *Model) scopedSourcePaths() []string {
	scope := strings.TrimSpace(string(m.scopeQuery))
	if scope == "" {
		return m.searchSource
	}
	if m.toggleActive[toggleGlob] {
		return m.filterByRegex(scope)
	}
	return m.lookupScope(scope)
}

// filterByRegex returns paths whose names match the given regex pattern.
// Patterns containing a path separator are matched against the relative
// path; otherwise matched against the base name only.
func (m *Model) filterByRegex(pattern string) []string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	matchPath := strings.ContainsRune(pattern, filepath.Separator) ||
		strings.ContainsRune(pattern, '/')
	filtered := make([]string, 0, len(m.searchSource)/4)
	for _, path := range m.searchSource {
		name := filepath.Base(path)
		if matchPath {
			name = m.relativePath(path)
		}
		if re.MatchString(name) {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

// lookupScope returns paths under the given scope using the directory index.
// Uses binary search on sorted keys for O(log N + K) instead of O(N).
func (m *Model) lookupScope(scope string) []string {
	scope = filepath.FromSlash(scope)
	prefix := filepath.Join(m.rootPath, scope)
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	// Binary search for the first key >= prefix.
	start := sort.SearchStrings(m.scopeSorted, prefix)
	var filtered []string
	for i := start; i < len(m.scopeSorted); i++ {
		key := m.scopeSorted[i]
		if !strings.HasPrefix(key, prefix) {
			break
		}
		for _, idx := range m.scopeIndex[key] {
			filtered = append(filtered, m.searchSource[idx])
		}
	}
	return filtered
}

// buildScopeIndex groups searchSource paths by their parent directory
// for efficient scope filtering. Returns the index map and a sorted key
// list for binary-search-based prefix matching. Built once at search entry.
func buildScopeIndex(sources []string, rootPath string) (map[string][]int, []string) {
	index := make(map[string][]int, len(sources)/4)
	for i, path := range sources {
		dir := filepath.Dir(path)
		if !strings.HasSuffix(dir, string(filepath.Separator)) {
			dir += string(filepath.Separator)
		}
		index[dir] = append(index[dir], i)
	}
	keys := make([]string, 0, len(index))
	for k := range index {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return index, keys
}

// grepSources scans files synchronously and builds the searchItems list,
// using the session file cache to avoid redundant disk I/O. Used for
// synchronous refilter after replace operations.
func (m *Model) grepSources(sources []string, query string, cfg matchConfig) {
	totalMatches := 0
	maxLine := 0

	for _, path := range sources {
		if totalMatches >= maxTotalMatches {
			break
		}
		matches := m.grepFileWithCache(path, query, cfg)
		if len(matches) == 0 {
			continue
		}
		m.appendFileResults(path, matches, &totalMatches, &maxLine)
	}
	m.searchNumWidth = digitCount(maxLine)
	m.searchScroll = 0
	m.seekFirstMatch()
}

// seekFirstMatch positions searchCursor on the first match item in
// searchItems. Falls back to 0 if no match items exist.
func (m *Model) seekFirstMatch() {
	for i, item := range m.searchItems {
		if item.kind == searchItemMatch {
			m.searchCursor = i
			return
		}
	}
	m.searchCursor = 0
}

// appendFileResults adds a file header and its match lines to searchItems.
func (m *Model) appendFileResults(path string, matches []searchMatch, totalMatches, maxLine *int) {
	if len(m.searchItems) > 0 {
		m.searchItems = append(m.searchItems, searchItem{kind: searchItemGap})
	}
	m.searchItems = append(m.searchItems, searchItem{kind: searchItemFile, path: path})
	for _, match := range matches {
		if *totalMatches >= maxTotalMatches {
			break
		}
		m.searchItems = append(m.searchItems, searchItem{
			kind: searchItemMatch,
			path: path,
			line: match.line,
			text: match.text,
		})
		*maxLine = max(*maxLine, match.line)
		(*totalMatches)++
	}
}

// grepFileWithCache reads a file (or uses the session cache) and returns
// lines matching the query according to the given match configuration.
func (m *Model) grepFileWithCache(path, query string, cfg matchConfig) []searchMatch {
	lines := m.cachedFileLines(path)
	if lines == nil {
		return nil
	}
	return matchLines(lines, query, cfg)
}

// cachedFileLines returns the pre-split lines for a file, reading from disk
// on cache miss and storing the result for subsequent queries.
func (m *Model) cachedFileLines(path string) []string {
	if lines := m.searchCache.get(path); lines != nil {
		return lines
	}
	data := readSearchableFile(path)
	if data == nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	m.searchCache.put(path, lines, int64(len(data)))
	return lines
}

// readSearchableFile reads a file's bytes, skipping files that are too large,
// unreadable, or binary. Returns nil if the file should be skipped.
// Uses a single open + fstat on the file descriptor to avoid TOCTOU races
// and reduce syscall count (vs. separate Stat + ReadFile).
func readSearchableFile(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > maxSearchFileSize {
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	probe := data
	if len(probe) > binaryProbeSize {
		probe = probe[:binaryProbeSize]
	}
	if bytes.ContainsRune(probe, 0) {
		return nil
	}
	return data
}

// matchLines scans lines for matches using the given config.
func matchLines(lines []string, query string, cfg matchConfig) []searchMatch {
	var matches []searchMatch
	for i, line := range lines {
		if len(matches) >= maxMatchesPerFile {
			break
		}
		if isBlankLine(line) {
			continue
		}
		if lineMatches(line, cfg) {
			matches = append(matches, searchMatch{line: i + 1, text: line})
		}
	}
	return matches
}

// isBlankLine reports whether a line contains only whitespace, without
// allocating a trimmed copy.
func isBlankLine(s string) bool {
	for i := range len(s) {
		if s[i] != ' ' && s[i] != '\t' && s[i] != '\r' {
			return false
		}
	}
	return true
}

// lineMatches reports whether a single line matches using the pre-compiled
// regex from matchConfig (covers all flag combinations).
func lineMatches(line string, cfg matchConfig) bool {
	if cfg.compiled == nil {
		return false
	}
	return cfg.compiled.MatchString(line)
}

// hasUpperCase reports whether s contains any uppercase ASCII letter.
func hasUpperCase(s string) bool {
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

// digitCount returns the number of decimal digits in n, minimum 1.
// Uses integer division to avoid floating-point precision issues at
// power-of-10 boundaries.
func digitCount(n int) int {
	if n <= 0 {
		return 1
	}
	count := 0
	for n > 0 {
		n /= 10
		count++
	}
	return count
}

// moveSearchCursor moves the cursor within search items, skipping gap entries.
func (m *Model) moveSearchCursor(delta int) {
	n := len(m.searchItems)
	if n == 0 {
		return
	}
	next := clampInt(m.searchCursor+delta, 0, n-1)
	step := 1
	if delta < 0 {
		step = -1
	}
	for next >= 0 && next < n && m.searchItems[next].kind == searchItemGap {
		next += step
	}
	if next < 0 || next >= n {
		return
	}
	m.searchCursor = next
	m.ensureSearchCursorVisible()
}

// activateSearchResult handles Enter on a file header or match line.
// Exits search mode immediately so the tree view is restored before
// the FileOpenMsg is processed.
func (m *Model) activateSearchResult() tea.Cmd {
	if m.searchCursor >= len(m.searchItems) {
		return nil
	}
	item := m.searchItems[m.searchCursor]
	line := 0
	if item.kind == searchItemMatch {
		line = item.line
	}

	// Capture values before exitSearch clears search state.
	path := item.path
	name := filepath.Base(path)
	lang := langFromPath(path)

	m.exitSearch()

	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     path,
			Name:     name,
			Language: lang,
			Line:     line,
		}
	}
}

// itemVisualLines returns the number of visual rows an item occupies.
// Match items occupy 2 rows when replacement preview is active, all others 1.
func (m *Model) itemVisualLines(idx int) int {
	if m.hasReplacePreview() && m.searchItems[idx].kind == searchItemMatch {
		return 2
	}
	return 1
}

// ensureSearchCursorVisible keeps the search cursor within the visible window,
// accounting for replacement preview lines that expand match items to 2 rows.
// Single O(n) forward pass from scroll to cursor; trims from the front when
// the cursor's visual bottom exceeds the viewport.
func (m *Model) ensureSearchCursorVisible() {
	bh := m.bodyHeight()
	if bh <= 0 {
		return
	}

	// Scroll up: cursor above viewport.
	if m.searchCursor < m.searchScroll {
		m.searchScroll = m.searchCursor
	}

	// Scroll down: accumulate visual lines from scroll to cursor inclusive.
	// If the total exceeds bh, trim items from the front until it fits.
	vis := 0
	for i := m.searchScroll; i <= m.searchCursor; i++ {
		vis += m.itemVisualLines(i)
	}
	for vis > bh && m.searchScroll < m.searchCursor {
		vis -= m.itemVisualLines(m.searchScroll)
		m.searchScroll++
	}

	// Clamp to maximum: walk backwards from end to find last valid scroll.
	ms := m.computeMaxScroll(bh)
	m.searchScroll = clampInt(m.searchScroll, 0, ms)
}

// computeMaxScroll returns the maximum valid scroll index such that items
// from that index through the end fit within bh visual rows.
func (m *Model) computeMaxScroll(bh int) int {
	vis := 0
	for i := len(m.searchItems) - 1; i >= 0; i-- {
		vis += m.itemVisualLines(i)
		if vis > bh {
			return i + 1
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Directory reading
// ---------------------------------------------------------------------------

// loadDir reads a directory and populates entries, expanding the first level.
func (m *Model) loadDir(root string, depth int) {
	children := m.readDir(root, depth)
	m.entries = append(m.entries, children...)
}

// readDir reads directory contents and returns sorted Entry slices.
// Directories are listed before files, both sorted alphabetically.
// Hidden files (dot-prefixed) are excluded.
func (m *Model) readDir(dirPath string, depth int) []Entry {
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}

	var dirs, files []Entry
	for _, de := range dirEntries {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		entry := Entry{
			Name:  name,
			Path:  filepath.Join(dirPath, name),
			IsDir: de.IsDir(),
			Depth: depth,
		}
		if de.IsDir() {
			dirs = append(dirs, entry)
		} else {
			files = append(files, entry)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })

	return append(dirs, files...)
}

// ---------------------------------------------------------------------------
// Rendering: tree mode
// ---------------------------------------------------------------------------

// viewTreeMode renders the normal tree view with header, entries, and search hint.
func (m *Model) viewTreeMode() string {
	contentWidth := max(m.width, 1)
	header := m.renderHeader(contentWidth)
	bh := m.bodyHeight()
	emptyLine := strings.Repeat(" ", contentWidth)

	// Render body lines without bounce offset.
	var bodyLines []string
	if len(m.entries) == 0 {
		bodyLines = make([]string, bh)
		bodyLines[0] = m.emptyView(contentWidth)
		for i := 1; i < bh; i++ {
			bodyLines[i] = emptyLine
		}
	} else {
		start := clampInt(m.scroll, 0, max(len(m.entries)-1, 0))
		end := min(start+bh, len(m.entries))
		bodyLines = make([]string, 0, bh)
		for i := start; i < end; i++ {
			bodyLines = append(bodyLines, m.renderEntry(i, contentWidth, m.entries, m.cursor))
		}
		for range bh - (end - start) {
			bodyLines = append(bodyLines, emptyLine)
		}
	}

	// Apply bounce shift (same approach as code panel's applyCodeBounceShift).
	bodyLines = applyBounceShift(bodyLines, m.bounceOffset, bh, emptyLine)

	// Combine header + search hint + divider + body + optional new-entry footer.
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(m.renderSearchHint(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchDivider(contentWidth))
	for _, line := range bodyLines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	if m.deleteConfirm {
		b.WriteByte('\n')
		b.WriteString(m.renderToolbarSeparator(contentWidth))
		b.WriteByte('\n')
		b.WriteString(m.renderDeleteConfirm(contentWidth))
	} else if m.pendingNewEntry {
		b.WriteByte('\n')
		b.WriteString(m.renderToolbarSeparator(contentWidth))
		b.WriteByte('\n')
		b.WriteString(m.renderNewEntryHint(contentWidth))
	} else if m.renameActive {
		b.WriteByte('\n')
		b.WriteString(m.renderToolbarSeparator(contentWidth))
		b.WriteByte('\n')
		b.WriteString(m.renderRenameInput(contentWidth))
	} else if m.newEntryActive {
		b.WriteByte('\n')
		b.WriteString(m.renderToolbarSeparator(contentWidth))
		b.WriteByte('\n')
		b.WriteString(m.renderNewEntryInput(contentWidth))
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Rendering: search mode
// ---------------------------------------------------------------------------

// viewSearchMode renders the header, search bar, divider, search results,
// and toolbar.
func (m *Model) viewSearchMode() string {
	contentWidth := max(m.width, 1)
	header := m.renderHeader(contentWidth)
	bh := m.bodyHeight()
	emptyLine := strings.Repeat(" ", contentWidth)

	bodyLines := m.renderSearchBody(bh, contentWidth, emptyLine)
	bodyLines = applyBounceShift(bodyLines, m.bounceOffset, bh, emptyLine)

	// Compose: header + search bar + divider + scope line + divider + body + separator + toolbar + summary.
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(m.renderSearchBar(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderToolbarSeparator(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderScopeLine(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchDivider(contentWidth))
	for _, line := range bodyLines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	b.WriteByte('\n')
	b.WriteString(m.renderToolbarSeparator(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchToolbar(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderMatchSummary(contentWidth))
	return b.String()
}

// ---------------------------------------------------------------------------
// Rendering: replace mode
// ---------------------------------------------------------------------------

// viewReplaceMode renders the header, search bar, replace bar, divider,
// search results, and toolbar.
func (m *Model) viewReplaceMode() string {
	contentWidth := max(m.width, 1)
	header := m.renderHeader(contentWidth)
	bh := m.bodyHeight()
	emptyLine := strings.Repeat(" ", contentWidth)

	bodyLines := m.renderSearchBody(bh, contentWidth, emptyLine)
	bodyLines = applyBounceShift(bodyLines, m.bounceOffset, bh, emptyLine)

	// Compose: header + search bar + replace bar + divider + scope line + divider + body + separator + toolbar + summary.
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(m.renderSearchBar(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderReplaceBar(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderToolbarSeparator(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderScopeLine(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchDivider(contentWidth))
	for _, line := range bodyLines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	b.WriteByte('\n')
	b.WriteString(m.renderToolbarSeparator(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchToolbar(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderMatchSummary(contentWidth))
	return b.String()
}

// renderReplaceBar renders the replacement text input line with a prefix
// and blinking cursor, matching the style of the search bar.
func (m *Model) renderReplaceBar(contentWidth int) string {
	textStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	prefix := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Primary).
		Bold(true).
		Render(" Replace: ")
	prefixWidth := lipgloss.Width(prefix)

	// Always reserve 1 column for the cursor so layout is stable.
	availableForText := max(contentWidth-prefixWidth-1, 0)

	// Pre-truncate text; show tail so recent keystrokes stay visible.
	textStr := string(m.replaceInput)
	textRunes := []rune(textStr)
	if len(textRunes) > availableForText {
		textStr = string(textRunes[len(textRunes)-availableForText:])
	}

	// Show blinking cursor only when this input has focus.
	cursor := " "
	if m.searchFocus == focusReplace && m.cursorBlink {
		cursor = cursorStyle.Render(" ")
	}

	line := prefix + textStyle.Render(textStr) + cursor
	lineWidth := lipgloss.Width(line)

	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += strings.Repeat(" ", padCount)
	}
	return line
}

// ---------------------------------------------------------------------------
// Rendering: references mode
// ---------------------------------------------------------------------------

// viewReferencesMode renders the header, symbol title bar, divider, reference
// results body, and a key hint footer.
func (m *Model) viewReferencesMode() string {
	contentWidth := max(m.width, 1)
	header := m.renderHeader(contentWidth)
	bh := m.bodyHeight()
	emptyLine := strings.Repeat(" ", contentWidth)

	bodyLines := m.renderReferencesBody(bh, contentWidth, emptyLine)
	bodyLines = applyBounceShift(bodyLines, m.bounceOffset, bh, emptyLine)

	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(m.renderReferencesTitle(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchDivider(contentWidth))
	for _, line := range bodyLines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	b.WriteByte('\n')
	b.WriteString(m.renderToolbarSeparator(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderReferencesHint(contentWidth))
	return b.String()
}

// renderReferencesTitle renders the symbol title: " ▸ SymbolName (N refs)".
func (m *Model) renderReferencesTitle(contentWidth int) string {
	arrowStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	titleStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary).Bold(true)
	countStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	arrow := arrowStyle.Render(theme.IconArrowRight + " ")
	title := titleStyle.Render(m.refTitle)
	count := countStyle.Render(fmt.Sprintf(" (%d)", len(m.refEntries)))

	line := " " + arrow + title + count
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderReferencesBody renders the references results body lines.
func (m *Model) renderReferencesBody(bh, contentWidth int, emptyLine string) []string {
	if len(m.refItems) == 0 {
		lines := make([]string, bh)
		style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
		lines[0] = style.Render("No references found")
		padCount := max(contentWidth-lipgloss.Width(lines[0]), 0)
		if padCount > 0 {
			lines[0] += strings.Repeat(" ", padCount)
		}
		for i := 1; i < bh; i++ {
			lines[i] = emptyLine
		}
		return lines
	}
	start := clampInt(m.refScroll, 0, max(len(m.refItems)-1, 0))
	end := min(start+bh, len(m.refItems))
	lines := make([]string, 0, bh)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderListItem(i, contentWidth))
	}
	for range bh - (end - start) {
		lines = append(lines, emptyLine)
	}
	return lines
}

// renderReferencesHint renders the key hint footer for references mode.
func (m *Model) renderReferencesHint(contentWidth int) string {
	keyStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	line := keyStyle.Render(" Esc close  Enter open")
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// ---------------------------------------------------------------------------
// Document symbols rendering
// ---------------------------------------------------------------------------

// viewDocSymbolsMode renders the complete document symbols panel.
func (m *Model) viewDocSymbolsMode() string {
	contentWidth := max(m.width, 1)
	header := m.renderHeader(contentWidth)
	bh := m.bodyHeight()
	emptyLine := strings.Repeat(" ", contentWidth)

	bodyLines := m.renderDocSymbolsBody(bh, contentWidth, emptyLine)
	bodyLines = applyBounceShift(bodyLines, m.bounceOffset, bh, emptyLine)

	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(m.renderDocSymbolsTitle(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchDivider(contentWidth))
	for _, line := range bodyLines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	b.WriteByte('\n')
	b.WriteString(m.renderToolbarSeparator(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderDocSymbolsHint(contentWidth))
	return b.String()
}

// renderDocSymbolsTitle renders the file title: " ▸ filename.go (N symbols)".
func (m *Model) renderDocSymbolsTitle(contentWidth int) string {
	arrowStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	titleStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary).Bold(true)
	countStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	arrow := arrowStyle.Render(theme.IconArrowRight + " ")
	title := titleStyle.Render(m.symTitle)
	count := countStyle.Render(fmt.Sprintf(" (%d)", len(m.symEntries)))

	line := " " + arrow + title + count
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderDocSymbolsBody renders the symbol outline body lines.
func (m *Model) renderDocSymbolsBody(bh, contentWidth int, emptyLine string) []string {
	if len(m.symItems) == 0 {
		lines := make([]string, bh)
		style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
		lines[0] = style.Render("No symbols found")
		padCount := max(contentWidth-lipgloss.Width(lines[0]), 0)
		if padCount > 0 {
			lines[0] += strings.Repeat(" ", padCount)
		}
		for i := 1; i < bh; i++ {
			lines[i] = emptyLine
		}
		return lines
	}
	start := clampInt(m.symScroll, 0, max(len(m.symItems)-1, 0))
	end := min(start+bh, len(m.symItems))
	lines := make([]string, 0, bh)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderListItem(i, contentWidth))
	}
	for range bh - (end - start) {
		lines = append(lines, emptyLine)
	}
	return lines
}

// renderDocSymbolsHint renders the key hint footer for document symbols mode.
func (m *Model) renderDocSymbolsHint(contentWidth int) string {
	keyStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	line := keyStyle.Render(" Esc close  Enter open")
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// ---------------------------------------------------------------------------
// Tabs rendering
// ---------------------------------------------------------------------------

// viewTabsMode renders the open tabs list view.
// Layout mirrors the search mode: filter input is at the top (below the
// subtitle), with toggle badges at the bottom — both only when filtering.
func (m *Model) viewTabsMode() string {
	contentWidth := max(m.width, 1)
	header := m.renderHeader(contentWidth)
	bh := m.bodyHeight()
	emptyLine := strings.Repeat(" ", contentWidth)

	bodyLines := m.renderTabsBody(bh, contentWidth, emptyLine)
	bodyLines = applyBounceShift(bodyLines, m.bounceOffset, bh, emptyLine)

	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(m.renderTabsTitle(contentWidth))
	if m.tabFiltering {
		b.WriteByte('\n')
		b.WriteString(m.renderTabsDivider(contentWidth))
		b.WriteByte('\n')
		b.WriteString(m.renderTabsFilterInput(contentWidth))
	}
	b.WriteByte('\n')
	b.WriteString(m.renderTabsDivider(contentWidth))
	for _, line := range bodyLines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	if m.tabFiltering {
		b.WriteByte('\n')
		b.WriteString(m.renderTabsDivider(contentWidth))
		b.WriteByte('\n')
		b.WriteString(m.renderTabsToolbar(contentWidth))
		b.WriteByte('\n')
		b.WriteString(m.renderTabsDivider(contentWidth))
	}
	b.WriteByte('\n')
	b.WriteString(m.renderTabsHint(contentWidth))
	return b.String()
}

// renderTabsTitle renders the subtitle line showing how many tabs are open.
func (m *Model) renderTabsTitle(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	label := fmt.Sprintf(" %d open", len(m.tabPaths))
	line := style.Render(label)
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderTabsBody renders the tab list body lines.
func (m *Model) renderTabsBody(bh, contentWidth int, emptyLine string) []string {
	if len(m.tabFiltered) == 0 {
		lines := make([]string, bh)
		style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
		if m.tabFiltering && len(m.tabFilter) > 0 {
			lines[0] = style.Render("No matching tabs")
		} else {
			lines[0] = style.Render("No open tabs")
		}
		padCount := max(contentWidth-lipgloss.Width(lines[0]), 0)
		if padCount > 0 {
			lines[0] += strings.Repeat(" ", padCount)
		}
		for i := 1; i < bh; i++ {
			lines[i] = emptyLine
		}
		return lines
	}
	start := clampInt(m.tabScroll, 0, max(len(m.tabFiltered)-1, 0))
	end := min(start+bh, len(m.tabFiltered))
	lines := make([]string, 0, bh)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderTabEntry(i, contentWidth))
	}
	for range bh - (end - start) {
		lines = append(lines, emptyLine)
	}
	return lines
}

// renderTabEntry renders a single tab entry with icon and filename.
func (m *Model) renderTabEntry(filteredIdx, contentWidth int) string {
	pathIdx := m.tabFiltered[filteredIdx]
	path := m.tabPaths[pathIdx]
	selected := filteredIdx == m.tabCursor && m.focused
	active := path == m.tabActive

	icon, iconColor := FileIcon(path, false, m.nerdFonts, m.theme)
	nameColor := m.theme.Palette.Foreground

	// Show relative path when multiple tabs share the same base name.
	displayName := filepath.Base(path)
	if m.tabDupBases[displayName] {
		displayName = m.relativePath(path)
	}

	if active {
		nameColor = m.theme.Palette.Accent
		iconColor = m.theme.Palette.Accent
	}

	hoverClose := filteredIdx == m.tabCloseHover
	modified := m.tabModified[path]
	closeColor := m.theme.Palette.Muted
	if modified {
		closeColor = m.theme.Palette.Warning
	}
	if hoverClose {
		closeColor = m.theme.Palette.Error
	}
	closeGlyph := "✕"
	if modified {
		closeGlyph = "●"
	}
	nameStyle := lipgloss.NewStyle().Foreground(nameColor)
	iconStyle := lipgloss.NewStyle().Foreground(iconColor)
	closeStyle := lipgloss.NewStyle().Foreground(closeColor)
	padStyle := lipgloss.NewStyle()
	if selected {
		bg := m.theme.Palette.Selection
		nameStyle = nameStyle.Background(bg)
		iconStyle = iconStyle.Background(bg)
		closeStyle = closeStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}

	prefix := iconStyle.Render(" " + icon + " ")
	prefixWidth := lipgloss.Width(prefix)
	suffix := closeStyle.Render(closeGlyph) + padStyle.Render(" ")
	suffixWidth := 2 // glyph + " "

	nameSpace := max(contentWidth-prefixWidth-suffixWidth, 0)
	name := truncatePlain(displayName, nameSpace)
	line := prefix + nameStyle.Render(name)

	lineWidth := lipgloss.Width(line)
	padCount := max(contentWidth-lineWidth-suffixWidth, 0)
	if padCount > 0 {
		line += padStyle.Render(strings.Repeat(" ", padCount))
	}
	line += suffix
	return line
}

// renderTabsFilterInput renders the filter input line: " / query|".
func (m *Model) renderTabsFilterInput(contentWidth int) string {
	queryStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	prefix := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Muted).
		Render("/ ")
	prefixWidth := lipgloss.Width(prefix)

	// Reserve 1 column for the cursor so layout is stable.
	availableForQuery := max(contentWidth-prefixWidth-1, 0)

	// Pre-truncate: show tail so recent keystrokes stay visible.
	queryRunes := m.tabFilter
	if len(queryRunes) > availableForQuery {
		queryRunes = queryRunes[len(queryRunes)-availableForQuery:]
	}

	// Blinking cursor when the filter input is focused.
	cursor := " "
	if m.tabFocus == tabFocusFilter && m.cursorBlink {
		cursor = cursorStyle.Render(" ")
	}

	line := prefix + queryStyle.Render(string(queryRunes)) + cursor
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderTabsHint renders the key hint footer for tabs mode.
func (m *Model) renderTabsHint(contentWidth int) string {
	keyStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	var hint string
	if m.tabFiltering {
		hint = " Esc clear  Enter open"
	} else {
		hint = " Esc close  / filter  Enter open"
	}
	line := keyStyle.Render(hint)
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderTabsToolbar renders the toolbar line with [Aa] [ab] [.*] toggle badges
// for tab filtering. The badges use tabToggleActive state.
func (m *Model) renderTabsToolbar(contentWidth int) string {
	var b strings.Builder
	b.WriteByte(' ')
	for i := range toolbarToggleCount {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(m.renderTabBadge(toggleIndex(i)))
	}
	line := b.String()
	padCount := max(contentWidth-lipgloss.Width(line), 0)
	if padCount > 0 {
		line += strings.Repeat(" ", padCount)
	}
	return line
}

// renderTabBadge renders a single toggle badge for the tabs toolbar.
func (m *Model) renderTabBadge(idx toggleIndex) string {
	fg := m.theme.Palette.Muted
	if m.tabToggleActive[idx] {
		fg = m.theme.Palette.Primary
	}
	style := lipgloss.NewStyle().Foreground(fg)
	if m.tabFocus == tabFocusToggles && m.tabToggleCursor == idx {
		style = style.Background(m.theme.Palette.Selection)
	}
	return style.Render("[" + badgeLabels[idx] + "]")
}

// renderSearchBody renders the search results body lines for the given height.
func (m *Model) renderSearchBody(bh, contentWidth int, emptyLine string) []string {
	if len(m.searchItems) == 0 {
		return m.renderEmptySearchBody(bh, contentWidth, emptyLine)
	}
	return m.renderPopulatedSearchBody(bh, contentWidth, emptyLine)
}

// renderEmptySearchBody renders a placeholder or "no results" message.
func (m *Model) renderEmptySearchBody(bh, contentWidth int, emptyLine string) []string {
	lines := make([]string, bh)
	if len(m.searchQuery) < minSearchQueryLen {
		lines[0] = m.searchPlaceholder(contentWidth)
	} else {
		lines[0] = m.noResultsView(contentWidth)
	}
	for i := 1; i < bh; i++ {
		lines[i] = emptyLine
	}
	return lines
}

// renderPopulatedSearchBody renders actual search result lines.
// In replace mode with replacement text, match lines are followed by a
// replacement preview line, consuming 2 visual rows per match item.
func (m *Model) renderPopulatedSearchBody(bh, contentWidth int, emptyLine string) []string {
	preview := m.hasReplacePreview()
	start := clampInt(m.searchScroll, 0, max(len(m.searchItems)-1, 0))
	lines := make([]string, 0, bh)
	for i := start; i < len(m.searchItems) && len(lines) < bh; i++ {
		lines = append(lines, m.renderListItem(i, contentWidth))
		if preview && m.searchItems[i].kind == searchItemMatch && len(lines) < bh {
			selected := i == m.activeCursor() && m.focused
			isTarget := m.isReplaceTarget(i)
			lines = append(lines, m.renderReplaceLine(m.searchItems[i], contentWidth, selected, isTarget))
		}
	}
	for len(lines) < bh {
		lines = append(lines, emptyLine)
	}
	return lines
}

// renderSearchBar renders the search input line with a blinking block cursor
// that stays visible regardless of which search element has keyboard focus.
func (m *Model) renderSearchBar(contentWidth int) string {
	queryStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	var prefix string
	if m.mode == viewReplace {
		prefix = lipgloss.NewStyle().
			Foreground(m.theme.Palette.Primary).
			Bold(true).
			Render(" Find: ")
	} else {
		prefix = lipgloss.NewStyle().
			Foreground(m.theme.Palette.Muted).
			Render("/ ")
	}
	prefixWidth := lipgloss.Width(prefix)

	// Always reserve 1 column for the cursor so layout is stable.
	availableForQuery := max(contentWidth-prefixWidth-1, 0)

	// Pre-truncate query text; show tail so recent keystrokes stay visible.
	queryStr := string(m.searchQuery)
	queryRunes := []rune(queryStr)
	if len(queryRunes) > availableForQuery {
		queryStr = string(queryRunes[len(queryRunes)-availableForQuery:])
	}

	// Show blinking cursor only when this input has focus.
	cursor := " "
	if m.searchFocus == focusQuery && m.cursorBlink {
		cursor = cursorStyle.Render(" ")
	}

	line := prefix + queryStyle.Render(queryStr) + cursor
	lineWidth := lipgloss.Width(line)

	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += strings.Repeat(" ", padCount)
	}

	return line
}

// renderSearchHint renders the muted hint line shown at the bottom in tree mode.
func (m *Model) renderSearchHint(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	text := style.Render("/ search")
	padCount := max(contentWidth-lipgloss.Width(text), 0)
	if padCount > 0 {
		return text + strings.Repeat(" ", padCount)
	}
	return text
}

// renderSearchDivider renders a horizontal line separating the search input
// above from the results body below.
func (m *Model) renderSearchDivider(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)
	return style.Render(strings.Repeat("─", contentWidth))
}

// renderToolbarSeparator renders a thin dotted line between the search input
// and the toolbar for visual separation.
func (m *Model) renderToolbarSeparator(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Subtle)
	return style.Render(strings.Repeat("╌", contentWidth))
}

// renderTabsDivider renders a short-dash divider for the tabs panel.
func (m *Model) renderTabsDivider(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)
	return style.Render(strings.Repeat("-", contentWidth))
}

// renderSearchToolbar renders the toolbar line with toggle badges [Aa] [ab] [.*].
// In replace mode, also renders [→1] and [→*] buttons after badges.
// The [re] glob toggle is on the scope line, not here.
func (m *Model) renderSearchToolbar(contentWidth int) string {
	var b strings.Builder
	b.WriteByte(' ')

	for i := range toolbarToggleCount {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(m.renderBadge(toggleIndex(i)))
	}

	if m.mode == viewReplace {
		b.WriteString("  ")
		col := lipgloss.Width(b.String())
		b.WriteString(m.renderReplaceButtons(col))
	}

	line := b.String()
	padCount := max(contentWidth-lipgloss.Width(line), 0)
	if padCount > 0 {
		line += strings.Repeat(" ", padCount)
	}
	return line
}

// renderScopeLine renders " In: <text>  [re]" with the [re] badge pinned
// to the far right. The scope text scrolls within a fixed-width region
// so the badge never shifts.
func (m *Model) renderScopeLine(contentWidth int) string {
	badge := m.renderGlobBadge()
	badgeW := lipgloss.Width(badge)

	// Layout: " In: <text>  [re] " — leading space, prefix, text, gap, badge, trailing space.
	const leadingSpace = 1
	const gapBeforeBadge = 2
	const trailingSpace = 1
	scopeRegion := max(contentWidth-leadingSpace-gapBeforeBadge-badgeW-trailingSpace, 0)

	scopeStr := m.renderScopeInput(scopeRegion)

	var b strings.Builder
	b.Grow(contentWidth)
	b.WriteByte(' ')
	b.WriteString(scopeStr)

	// Pad between scope input and badge to push badge to fixed position.
	used := leadingSpace + lipgloss.Width(scopeStr)
	targetBadgeCol := contentWidth - badgeW - trailingSpace
	if pad := targetBadgeCol - used; pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	b.WriteString(badge)

	// Trailing pad to fill line.
	line := b.String()
	if pad := contentWidth - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderGlobBadge renders the [re] toggle badge for glob mode.
// Styled like toolbar badges: muted when off, primary when on,
// selection background when focused via toggle cursor.
func (m *Model) renderGlobBadge() string {
	fg := m.theme.Palette.Muted
	if m.toggleActive[toggleGlob] {
		fg = m.theme.Palette.Primary
	}
	style := lipgloss.NewStyle().Foreground(fg)
	if m.searchFocus == focusToggles && m.toggleCursor == toggleGlob {
		style = style.Background(m.theme.Palette.Selection)
	}
	return style.Render("[" + badgeLabels[toggleGlob] + "]")
}

func (m *Model) renderMatchSummary(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	matches, files := m.countMatchesAndFiles()
	var text string
	switch {
	case len(m.searchQuery) < minSearchQueryLen:
		text = ""
	case !m.searchDone && matches == 0:
		text = style.Render(" Searching...")
	case !m.searchDone:
		text = style.Render(fmt.Sprintf(" %d results in %d files (searching...)", matches, files))
	case matches == 0:
		text = style.Render(" No results")
	default:
		text = style.Render(fmt.Sprintf(" %d results in %d files", matches, files))
	}

	padCount := max(contentWidth-lipgloss.Width(text), 0)
	if padCount > 0 {
		text += strings.Repeat(" ", padCount)
	}
	return text
}

// countMatchesAndFiles counts searchItemMatch and searchItemFile entries
// in the current search results.
func (m *Model) countMatchesAndFiles() (matches, files int) {
	for _, item := range m.searchItems {
		switch item.kind {
		case searchItemMatch:
			matches++
		case searchItemFile:
			files++
		}
	}
	return matches, files
}

// renderBadge renders a single toggle badge like "[Aa]".
func (m *Model) renderBadge(idx toggleIndex) string {
	return m.badgeStyle(idx).Render("[" + badgeLabels[idx] + "]")
}

// badgeStyle returns the lipgloss.Style for a toggle badge based on
// its active state and whether it is the focused cursor target.
func (m *Model) badgeStyle(idx toggleIndex) lipgloss.Style {
	fg := m.theme.Palette.Muted
	if m.toggleActive[idx] {
		fg = m.theme.Palette.Primary
	}
	style := lipgloss.NewStyle().Foreground(fg)
	if m.searchFocus == focusToggles && m.toggleCursor == idx {
		style = style.Background(m.theme.Palette.Selection)
	}
	return style
}

// renderReplaceButtons renders the [→1] and [→*] buttons for replace mode,
// recording their absolute column positions for click handling.
func (m *Model) renderReplaceButtons(startCol int) string {
	btnOneLabel := "[" + theme.IconArrowRight + "1]"
	btnAllLabel := "[" + theme.IconArrowRight + "*]"

	m.btnOneCol = startCol
	btnOneStr := m.replaceButtonStyle(focusBtnOne).Render(btnOneLabel)

	// "[→1]" is 4 visible columns, plus 1 space separator.
	m.btnAllCol = startCol + lipgloss.Width(btnOneStr) + 1
	btnAllStr := m.replaceButtonStyle(focusBtnAll).Render(btnAllLabel)

	return btnOneStr + " " + btnAllStr
}

// replaceButtonStyle returns the style for a replace button based on
// flash state and focus.
func (m *Model) replaceButtonStyle(btn searchFocus) lipgloss.Style {
	fg := m.theme.Palette.Muted
	if m.btnFlash == btn && time.Since(m.btnFlashAt) < btnFlashDuration {
		fg = m.theme.Palette.Primary
	}
	style := lipgloss.NewStyle().Foreground(fg)
	if m.searchFocus == btn {
		style = style.Background(m.theme.Palette.Selection)
	}
	return style
}

// renderScopeInput renders the "In: " prefix plus a cursor-aware scrollable
// text region. The cursor position determines which portion of text is visible.
func (m *Model) renderScopeInput(availableWidth int) string {
	prefixStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	textStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	prefix := prefixStyle.Render(scopePrefix)
	prefixW := lipgloss.Width(prefix)

	// Text region: availableWidth minus prefix minus 1 cursor column.
	textW := max(availableWidth-prefixW-1, 0)
	n := len(m.scopeQuery)
	cur := clampInt(m.scopeCursor, 0, n)

	// Compute visible window [start, end) that keeps cursor in view.
	start := 0
	end := n
	if n > textW {
		// Try to center cursor, then clamp.
		start = cur - textW/2
		start = clampInt(start, 0, n-textW)
		end = start + textW
	}
	visible := string(m.scopeQuery[start:end])

	// Cursor character: the rune at cursor position (or space if at end).
	cursorChar := " "
	if cur < n {
		cursorChar = string(m.scopeQuery[cur])
	}

	// Build: prefix + text-before-cursor + cursor-char + text-after-cursor.
	beforeLen := cur - start
	afterStart := cur + 1 - start

	before := ""
	after := ""
	visRunes := []rune(visible)
	if beforeLen > 0 && beforeLen <= len(visRunes) {
		before = string(visRunes[:beforeLen])
	}
	if afterStart >= 0 && afterStart < len(visRunes) {
		after = string(visRunes[afterStart:])
	}

	// Render cursor: blinking block when focused, plain text otherwise.
	var cursorRendered string
	if m.searchFocus == focusScope && m.cursorBlink {
		cursorRendered = cursorStyle.Render(cursorChar)
	} else {
		cursorRendered = textStyle.Render(cursorChar)
	}

	return prefix + textStyle.Render(before) + cursorRendered + textStyle.Render(after)
}

// renderRenameInput renders the inline rename input with a blinking cursor,
// pre-filled with the current entry name.
func (m *Model) renderRenameInput(contentWidth int) string {
	labelStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	textStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	prefix := " " + labelStyle.Render("Rename: ")
	prefixW := lipgloss.Width(prefix)

	maxTextW := max(contentWidth-prefixW-1, 0)
	inputStr := string(m.renameInput)
	inputRunes := m.renameInput
	if len(inputRunes) > maxTextW {
		inputStr = string(inputRunes[len(inputRunes)-maxTextW:])
	}

	cursor := " "
	if m.cursorBlink {
		cursor = cursorStyle.Render(" ")
	}

	line := prefix + textStyle.Render(inputStr) + cursor
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderDeleteConfirm renders the delete confirmation line showing the
// entry name and Y/N options.
func (m *Model) renderDeleteConfirm(contentWidth int) string {
	labelStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Error).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	keyStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	name := filepath.Base(m.deleteConfirmPath)
	line := " " + labelStyle.Render("Delete") + " " + nameStyle.Render(name) + keyStyle.Render("  Y yes  N no")
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderNewEntryHint renders the chord hint shown after Alt+N is pressed,
// prompting the user to press F (file) or D (directory).
func (m *Model) renderNewEntryHint(contentWidth int) string {
	labelStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Warning).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	line := " " + labelStyle.Render("New entry") + keyStyle.Render("  F file  D dir")
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderNewEntryInput renders the inline input line for creating a new file
// or directory, including a label prefix and blinking cursor.
func (m *Model) renderNewEntryInput(contentWidth int) string {
	labelStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	textStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	label := "New file: "
	if m.newEntryIsDir {
		label = "New dir:  "
	}
	prefix := " " + labelStyle.Render(label)
	prefixW := lipgloss.Width(prefix)

	maxTextW := max(contentWidth-prefixW-1, 0)
	inputStr := string(m.newEntryInput)
	inputRunes := m.newEntryInput
	if len(inputRunes) > maxTextW {
		inputStr = string(inputRunes[len(inputRunes)-maxTextW:])
	}

	cursor := " "
	if m.cursorBlink {
		cursor = cursorStyle.Render(" ")
	}

	line := prefix + textStyle.Render(inputStr) + cursor
	lineWidth := lipgloss.Width(line)
	if pad := contentWidth - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// activeCursor returns the cursor index for the active list mode.
func (m *Model) activeCursor() int {
	switch m.mode {
	case viewReferences:
		return m.refCursor
	case viewDocSymbols:
		return m.symCursor
	default:
		return m.searchCursor
	}
}

// activeNumWidth returns the line number digit width for the active mode.
func (m *Model) activeNumWidth() int {
	switch m.mode {
	case viewReferences:
		return m.refNumWidth
	case viewDocSymbols:
		return m.symNumWidth
	default:
		return m.searchNumWidth
	}
}

// activeItems returns the searchItem slice for the active list mode.
func (m *Model) activeItems() []searchItem {
	switch m.mode {
	case viewReferences:
		return m.refItems
	case viewDocSymbols:
		return m.symItems
	default:
		return m.searchItems
	}
}

// renderListItem renders a single result row (file header, match line, or gap)
// using mode-aware cursor and item list.
func (m *Model) renderListItem(idx, contentWidth int) string {
	items := m.activeItems()
	item := items[idx]
	selected := idx == m.activeCursor() && m.focused

	switch item.kind {
	case searchItemFile:
		return m.renderFileHeader(item, contentWidth, selected)
	case searchItemMatch:
		isTarget := m.isReplaceTarget(idx)
		return m.renderMatchLine(item, contentWidth, selected, isTarget)
	case searchItemGap:
		return strings.Repeat(" ", contentWidth)
	default:
		return strings.Repeat(" ", contentWidth)
	}
}

// renderFileHeader renders a file header: " • relative/path".
func (m *Model) renderFileHeader(item searchItem, contentWidth int, selected bool) string {
	dotStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	pathStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	padStyle := lipgloss.NewStyle()

	if selected {
		bg := m.theme.Palette.Selection
		dotStyle = dotStyle.Background(bg)
		pathStyle = pathStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}

	prefix := dotStyle.Render(" • ")
	prefixWidth := lipgloss.Width(prefix)
	relPath := truncatePlain(m.relativePath(item.path), max(contentWidth-prefixWidth, 0))

	line := prefix + pathStyle.Render(relPath)
	lineWidth := lipgloss.Width(line)

	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += padStyle.Render(strings.Repeat(" ", padCount))
	}
	return line
}

// renderMatchLine renders a match line: "   42  content" with query highlighted.
// When isTarget is true (current replacement target in replace mode), uses a
// Highlight background to distinguish this match from others.
func (m *Model) renderMatchLine(item searchItem, contentWidth int, selected, isTarget bool) string {
	numStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	textStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Subtext)
	hlStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Warning)
	padStyle := lipgloss.NewStyle()

	applyBg := func(bg lipgloss.Color) {
		numStyle = numStyle.Background(bg)
		textStyle = textStyle.Background(bg)
		hlStyle = hlStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}
	switch {
	case isTarget:
		applyBg(m.theme.Palette.Highlight)
	case selected:
		applyBg(m.theme.Palette.Selection)
	}

	// Indent + right-aligned line number + gap.
	numStr := fmt.Sprintf("  %*d  ", m.activeNumWidth(), item.line)
	prefix := numStyle.Render(numStr)
	prefixWidth := lipgloss.Width(prefix)

	// Trim leading whitespace, pre-truncate to available width, then highlight.
	trimmed := strings.TrimLeft(item.text, " \t")
	trimmed = truncatePlain(trimmed, max(contentWidth-prefixWidth, 0))

	// In replace mode with replacement text, show matches with strikethrough.
	// The replacement preview is rendered on a separate line below.
	var content string
	if m.mode == viewReplace && len(m.replaceInput) > 0 {
		strikeStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Error).Strikethrough(true)
		switch {
		case isTarget:
			strikeStyle = strikeStyle.Background(m.theme.Palette.Highlight)
		case selected:
			strikeStyle = strikeStyle.Background(m.theme.Palette.Selection)
		}
		content = m.highlightQuery(trimmed, textStyle, strikeStyle)
	} else {
		content = m.highlightQuery(trimmed, textStyle, hlStyle)
	}

	line := prefix + content
	lineWidth := lipgloss.Width(line)

	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += padStyle.Render(strings.Repeat(" ", padCount))
	}
	return line
}

// renderReplaceLine renders the replacement preview line below a match line.
// Shows the post-replacement text in success (green) color, indented to align
// with the match content above. When isTarget is true, uses Highlight bg.
func (m *Model) renderReplaceLine(item searchItem, contentWidth int, selected, isTarget bool) string {
	replStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Success)
	padStyle := lipgloss.NewStyle()

	switch {
	case isTarget:
		bg := m.theme.Palette.Highlight
		replStyle = replStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	case selected:
		bg := m.theme.Palette.Selection
		replStyle = replStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}

	// Measure the same prefix string used in renderMatchLine, then blank it.
	numStr := fmt.Sprintf("  %*d  ", m.activeNumWidth(), item.line)
	prefixWidth := lipgloss.Width(numStr)
	prefix := padStyle.Render(strings.Repeat(" ", prefixWidth))

	trimmed := strings.TrimLeft(item.text, " \t")
	replaced := m.replaceText(trimmed)
	replaced = truncatePlain(replaced, max(contentWidth-prefixWidth, 0))

	line := prefix + replStyle.Render(replaced)
	lineWidth := lipgloss.Width(line)

	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += padStyle.Render(strings.Repeat(" ", padCount))
	}
	return line
}

// hasReplacePreview reports whether the current mode should show replacement
// preview lines below match lines.
func (m *Model) hasReplacePreview() bool {
	return m.mode == viewReplace && len(m.replaceInput) > 0
}

// isReplaceTarget reports whether the item at idx is the current replacement
// target — the match line the cursor is on in replace mode with replacement text.
func (m *Model) isReplaceTarget(idx int) bool {
	return m.hasReplacePreview() && idx == m.searchCursor && m.searchItems[idx].kind == searchItemMatch
}

// highlightQuery splits text at query occurrences and highlights them,
// respecting the current match configuration for case sensitivity.
// In references mode (no query), renders plain text.
func (m *Model) highlightQuery(text string, normalStyle, hlStyle lipgloss.Style) string {
	if m.mode == viewReferences || m.mode == viewDocSymbols {
		return normalStyle.Render(text)
	}
	query := string(m.searchQuery)
	if len(query) == 0 {
		return normalStyle.Render(text)
	}
	if m.toggleActive[toggleRegex] {
		return m.highlightRegex(text, normalStyle, hlStyle)
	}
	caseSensitive := m.toggleActive[toggleCase] || hasUpperCase(query)
	return highlightSubstring(text, query, caseSensitive, normalStyle, hlStyle)
}

// highlightRegex highlights regex match locations in text.
func (m *Model) highlightRegex(text string, normalStyle, hlStyle lipgloss.Style) string {
	if m.compiledRegexp == nil {
		return normalStyle.Render(text)
	}
	locs := m.compiledRegexp.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return normalStyle.Render(text)
	}
	var b strings.Builder
	pos := 0
	for _, loc := range locs {
		if loc[0] > pos {
			b.WriteString(normalStyle.Render(text[pos:loc[0]]))
		}
		b.WriteString(hlStyle.Render(text[loc[0]:loc[1]]))
		pos = loc[1]
	}
	if pos < len(text) {
		b.WriteString(normalStyle.Render(text[pos:]))
	}
	return b.String()
}

// highlightSubstring highlights substring occurrences in text, optionally
// case-sensitive.
func highlightSubstring(text, query string, caseSensitive bool, normalStyle, hlStyle lipgloss.Style) string {
	haystack, needle := text, query
	if !caseSensitive {
		haystack = strings.ToLower(haystack)
		needle = strings.ToLower(needle)
	}
	qLen := len(needle)

	var b strings.Builder
	pos := 0
	for pos < len(text) {
		idx := strings.Index(haystack[pos:], needle)
		if idx < 0 {
			b.WriteString(normalStyle.Render(text[pos:]))
			break
		}
		if idx > 0 {
			b.WriteString(normalStyle.Render(text[pos : pos+idx]))
		}
		b.WriteString(hlStyle.Render(text[pos+idx : pos+idx+qLen]))
		pos += idx + qLen
	}
	return b.String()
}

// replaceText applies the current query→replacement substitution to text,
// respecting case/regex/word toggles. Used to produce the preview line.
func (m *Model) replaceText(text string) string {
	query := string(m.searchQuery)
	replacement := string(m.replaceInput)
	if m.toggleActive[toggleRegex] && m.compiledRegexp != nil {
		return m.compiledRegexp.ReplaceAllString(text, replacement)
	}
	caseSensitive := m.toggleActive[toggleCase] || hasUpperCase(query)
	return replaceAllLiteral(text, query, replacement, caseSensitive)
}

// replaceAllLiteral replaces all literal occurrences of needle in text,
// optionally case-insensitive, preserving original casing in non-match spans.
func replaceAllLiteral(text, needle, replacement string, caseSensitive bool) string {
	if caseSensitive {
		return strings.ReplaceAll(text, needle, replacement)
	}
	haystack := strings.ToLower(text)
	search := strings.ToLower(needle)
	nLen := len(search)

	var b strings.Builder
	pos := 0
	for pos < len(text) {
		idx := strings.Index(haystack[pos:], search)
		if idx < 0 {
			b.WriteString(text[pos:])
			break
		}
		b.WriteString(text[pos : pos+idx])
		b.WriteString(replacement)
		pos += idx + nLen
	}
	return b.String()
}

// relativePath returns the path relative to rootPath for display.
func (m *Model) relativePath(absPath string) string {
	if m.rootPath == "" {
		return absPath
	}
	prefix := m.rootPath + string(filepath.Separator)
	if rel, ok := strings.CutPrefix(absPath, prefix); ok {
		return rel
	}
	return absPath
}

func (m *Model) searchPlaceholder(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	text := style.Render("Type 2+ chars to search...")
	padCount := max(contentWidth-lipgloss.Width(text), 0)
	return text + strings.Repeat(" ", padCount)
}

func (m *Model) noResultsView(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	text := style.Render("No matches")
	padCount := max(contentWidth-lipgloss.Width(text), 0)
	return text + strings.Repeat(" ", padCount)
}

// ---------------------------------------------------------------------------
// Rendering: shared
// ---------------------------------------------------------------------------

// renderHeader renders the section header: title label + trailing separator line.
func (m *Model) renderHeader(contentWidth int) string {
	labelColor := m.theme.Palette.Muted
	if m.focused {
		labelColor = m.theme.Palette.Secondary
	}
	headerStyle := lipgloss.NewStyle().Foreground(labelColor).Bold(true)
	lineStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)

	title := " Files "
	switch m.mode {
	case viewReferences:
		title = " References "
	case viewDocSymbols:
		title = " Symbols "
	case viewTabs:
		title = " Tabs "
	}
	text := headerStyle.Render(title)
	textWidth := lipgloss.Width(text)
	lineWidth := max(contentWidth-textWidth, 0)

	return text + lineStyle.Render(strings.Repeat("─", lineWidth))
}

// renderEntry renders a single entry line from the given entries slice,
// highlighting the line at cursorIdx.
func (m *Model) renderEntry(idx, contentWidth int, entries []Entry, cursorIdx int) string {
	entry := entries[idx]
	selected := idx == cursorIdx && m.focused
	active := !entry.IsDir && entry.Path == m.activeFilePath

	nameColor := m.theme.Palette.Foreground
	icon, iconColor := FileIcon(entry.Path, entry.IsDir, m.nerdFonts, m.theme)
	if entry.IsDir {
		nameColor = m.theme.Palette.Primary
		iconColor = m.theme.Palette.Muted
		if entry.Expanded {
			icon = iconDir
		} else {
			icon = iconDirClosed
		}
	}

	// Git status: override name color and compute badge suffix.
	badge, badgeColor := m.gitBadge(&entry)
	if badgeColor != "" {
		nameColor = badgeColor
	}

	// Active file overrides name color but preserves badge color.
	if active {
		nameColor = m.theme.Palette.Accent
		iconColor = m.theme.Palette.Accent
	}

	// When selected, every segment carries the selection background
	// so the highlight spans the entire row without ANSI gaps.
	nameStyle := lipgloss.NewStyle().Foreground(nameColor)
	iconStyle := lipgloss.NewStyle().Foreground(iconColor)
	padStyle := lipgloss.NewStyle()
	if selected {
		bg := m.theme.Palette.Selection
		nameStyle = nameStyle.Background(bg)
		iconStyle = iconStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}

	indent := strings.Repeat(" ", entry.Depth*indentWidth)
	prefixStr := indent + icon + " "
	prefix := iconStyle.Render(prefixStr)
	prefixWidth := lipgloss.Width(prefix)

	// Reserve space for badge when truncating the file name.
	badgeWidth := len([]rune(badge))
	name := truncatePlain(entry.Name, max(contentWidth-prefixWidth-badgeWidth, 0))
	line := prefix + nameStyle.Render(name)

	// Append badge with its own color (not overridden by active state).
	if badge != "" {
		badgeStyle := lipgloss.NewStyle().Foreground(badgeColor).Bold(true)
		if selected {
			badgeStyle = badgeStyle.Background(m.theme.Palette.Selection)
		}
		line += badgeStyle.Render(badge)
	}

	lineWidth := lipgloss.Width(line)
	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += padStyle.Render(strings.Repeat(" ", padCount))
	}

	return line
}

// gitBadge returns the badge suffix and color for a file tree entry based on
// its git status. The returned color applies to both the badge and the entry
// name. Returns ("", "") when no git decoration applies.
func (m *Model) gitBadge(entry *Entry) (badge string, color lipgloss.Color) {
	switch m.gitStateForEntry(entry) {
	case git.GitIgnored:
		return "", m.theme.Palette.Muted
	case git.GitUntracked:
		return " U", m.theme.Palette.Success
	case git.GitAdded:
		return " A", m.theme.Palette.Teal
	case git.GitModified:
		return " M", m.theme.Palette.Warning
	case git.GitDeleted:
		return " D", m.theme.Palette.Peach
	case git.GitConflict:
		return " !", m.theme.Palette.Error
	default:
		return "", ""
	}
}

// FileIcon returns an icon glyph and color for a file path. When Nerd Fonts
// are available, it uses go-devicons; otherwise it falls back to simple Unicode
// glyphs colored with the theme palette.
func FileIcon(path string, isDir, nerdFonts bool, th *theme.Theme) (string, lipgloss.Color) {
	if isDir {
		return iconDir, th.Palette.Muted
	}
	if nerdFonts {
		style := devicons.IconForPath(path)
		return style.Icon, lipgloss.Color(style.Color)
	}
	return fallbackIcon(filepath.Base(path)), th.Palette.Muted
}

// fileCategory groups file extensions for fallback icon assignment.
type fileCategory int

const (
	catGeneric fileCategory = iota
	catCode
	catMarkup
	catConfig
	catData
	catImage
	catMedia
	catArchive
)

// categoryGlyph maps each category to a universally-supported Unicode glyph.
// Derived from: geometric shapes block (U+25A0–U+25FF) and musical symbols.
var categoryGlyph = [...]string{
	catGeneric: "○",
	catCode:    "◆",
	catMarkup:  "◇",
	catConfig:  "◈",
	catData:    "▪",
	catImage:   "◐",
	catMedia:   "♪",
	catArchive: "●",
}

// extCategoryMap maps common file extensions to a display category.
var extCategoryMap = map[string]fileCategory{
	// Source code
	"go": catCode, "py": catCode, "js": catCode, "ts": catCode,
	"tsx": catCode, "jsx": catCode, "rs": catCode, "c": catCode,
	"cpp": catCode, "cc": catCode, "h": catCode, "hpp": catCode,
	"java": catCode, "kt": catCode, "kts": catCode, "swift": catCode,
	"rb": catCode, "php": catCode, "cs": catCode, "fs": catCode,
	"lua": catCode, "zig": catCode, "nim": catCode, "v": catCode,
	"ex": catCode, "exs": catCode, "erl": catCode, "hs": catCode,
	"ml": catCode, "mli": catCode, "elm": catCode, "clj": catCode,
	"scala": catCode, "dart": catCode, "r": catCode, "jl": catCode,
	"sh": catCode, "bash": catCode, "zsh": catCode, "fish": catCode,
	"ps1": catCode, "bat": catCode, "cmd": catCode, "asm": catCode,
	"s": catCode, "pl": catCode, "pm": catCode, "cr": catCode,
	"d": catCode, "pas": catCode, "lisp": catCode, "rkt": catCode,
	// Markup / documentation
	"md": catMarkup, "markdown": catMarkup, "rst": catMarkup,
	"adoc": catMarkup, "tex": catMarkup, "html": catMarkup,
	"htm": catMarkup, "xml": catMarkup, "xhtml": catMarkup,
	"svg": catMarkup, "vue": catMarkup, "svelte": catMarkup,
	"astro": catMarkup, "erb": catMarkup, "haml": catMarkup,
	"pug": catMarkup, "slim": catMarkup, "njk": catMarkup,
	// Configuration
	"json": catConfig, "yaml": catConfig, "yml": catConfig,
	"toml": catConfig, "ini": catConfig, "cfg": catConfig,
	"conf": catConfig, "env": catConfig, "properties": catConfig,
	"editorconfig": catConfig, "eslintrc": catConfig,
	"prettierrc": catConfig, "babelrc": catConfig,
	// Data
	"csv": catData, "tsv": catData, "sql": catData, "db": catData,
	"sqlite": catData, "parquet": catData, "avro": catData,
	"proto": catData, "graphql": catData, "gql": catData,
	// Images
	"png": catImage, "jpg": catImage, "jpeg": catImage, "gif": catImage,
	"bmp": catImage, "ico": catImage, "webp": catImage, "avif": catImage,
	"tiff": catImage, "psd": catImage, "xcf": catImage,
	// Media (audio/video)
	"mp3": catMedia, "wav": catMedia, "ogg": catMedia, "flac": catMedia,
	"aac": catMedia, "m4a": catMedia, "wma": catMedia,
	"mp4": catMedia, "mkv": catMedia, "avi": catMedia, "mov": catMedia,
	"webm": catMedia, "flv": catMedia,
	// Archives / binaries
	"zip": catArchive, "tar": catArchive, "gz": catArchive,
	"bz2": catArchive, "xz": catArchive, "7z": catArchive,
	"rar": catArchive, "deb": catArchive, "rpm": catArchive,
	"dmg": catArchive, "iso": catArchive, "jar": catArchive,
	"whl": catArchive, "exe": catArchive, "dll": catArchive,
	"so": catArchive, "dylib": catArchive, "wasm": catArchive,
}

// fallbackIcon returns a simple Unicode glyph for a filename based on its
// extension category.
func fallbackIcon(name string) string {
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	cat, ok := extCategoryMap[strings.ToLower(ext)]
	if !ok {
		return categoryGlyph[catGeneric]
	}
	return categoryGlyph[cat]
}

func (m *Model) emptyView(contentWidth int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	text := style.Render("No files loaded")
	padCount := max(contentWidth-lipgloss.Width(text), 0)
	return text + strings.Repeat(" ", padCount)
}

// ---------------------------------------------------------------------------
// Scroll helpers
// ---------------------------------------------------------------------------

// bodyHeight returns the number of entry lines visible between the top
// chrome (header + input/hint + divider) and the mode-specific footer.
func (m *Model) bodyHeight() int {
	footer := 0
	top := treeChromeHeight
	switch m.mode {
	case viewSearch:
		top = topChromeHeight
		footer = searchFooterHeight
	case viewReplace:
		top = replaceChromeHeight
		footer = searchFooterHeight
	case viewReferences:
		footer = refsFooterHeight
	case viewDocSymbols:
		footer = refsFooterHeight
	case viewTabs:
		if m.tabFiltering {
			top = tabsChromeHeight
			footer = tabsFooterHeight
		} else {
			footer = tabsHintHeight
		}
	}
	if m.pendingNewEntry || m.newEntryActive || m.deleteConfirm || m.renameActive {
		footer += newEntryFooterHeight
	}
	return max(m.height-top-footer, 1)
}

func (m *Model) ensureCursorVisible() {
	bh := m.bodyHeight()
	if bh <= 0 {
		return
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+bh {
		m.scroll = m.cursor - bh + 1
	}
	m.clampScroll()
}

func (m *Model) clampScroll() {
	maxScroll := max(len(m.entries)-m.bodyHeight(), 0)
	m.scroll = clampInt(m.scroll, 0, maxScroll)
}

// ---------------------------------------------------------------------------
// Language detection
// ---------------------------------------------------------------------------

// langExtMap maps file extensions to language identifiers matching
// the keyword tables in code/highlight.go.
var langExtMap = map[string]string{
	".go":   "go",
	".py":   "python",
	".js":   "javascript",
	".ts":   "typescript",
	".rs":   "rust",
	".rb":   "ruby",
	".java": "java",
	".c":    "c",
	".h":    "c",
	".cpp":  "cpp",
	".hpp":  "cpp",
	".md":   "markdown",
	".yaml": "yaml",
	".yml":  "yaml",
	".json": "json",
	".toml": "toml",
	".sh":   "bash",
	".css":  "css",
	".html": "html",
}

// langFromPath returns the language identifier for a file path based on its extension.
func langFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if lang, ok := langExtMap[ext]; ok {
		return lang
	}
	return ""
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func sliceInsert(s []Entry, pos int, items []Entry) []Entry {
	result := make([]Entry, 0, len(s)+len(items))
	result = append(result, s[:pos]...)
	result = append(result, items...)
	result = append(result, s[pos:]...)
	return result
}

func sliceRemove(s []Entry, start, end int) []Entry {
	result := make([]Entry, 0, len(s)-(end-start))
	result = append(result, s[:start]...)
	result = append(result, s[end:]...)
	return result
}

// applyBounceShift shifts body lines to create overscroll bounce visuals.
// The returned slice always has exactly maxLines entries so downstream
// layout (search bar, toolbar) is never displaced.
// Positive offset (bottom bounce): content shifts up, empty lines at bottom.
// Negative offset (top bounce): empty lines at top, content truncated at bottom.
func applyBounceShift(lines []string, offset, maxLines int, emptyLine string) []string {
	if offset == 0 || maxLines <= 0 {
		return lines
	}
	absOffset := offset
	if absOffset < 0 {
		absOffset = -absOffset
	}
	absOffset = min(absOffset, maxLines)

	result := make([]string, maxLines)

	if offset > 0 {
		// Bottom bounce: shift content up, pad bottom with empty lines.
		shift := min(absOffset, len(lines))
		copied := copy(result, lines[shift:])
		for i := copied; i < maxLines; i++ {
			result[i] = emptyLine
		}
	} else {
		// Top bounce: empty lines at top, content fills remaining space.
		for i := range absOffset {
			result[i] = emptyLine
		}
		remaining := maxLines - absOffset
		src := lines
		if len(src) > remaining {
			src = src[:remaining]
		}
		copy(result[absOffset:], src)
		for i := absOffset + len(src); i < maxLines; i++ {
			result[i] = emptyLine
		}
	}

	return result
}

// truncatePlain clips a plain (unstyled) string so its visual width
// does not exceed maxWidth, appending "…" when truncated.
// Safe for strings without ANSI escape codes (file paths, names, code text).
func truncatePlain(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

// truncateToWidth clips a styled string (possibly containing ANSI escape
// codes) to fit within the given visual width, appending "…" when truncated.
// ANSI CSI sequences are copied verbatim without counting toward width.
func truncateToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	target := max(w-1, 0)
	var buf strings.Builder
	visWidth := 0
	i := 0
	for i < len(s) && visWidth < target {
		if s[i] == '\x1b' {
			// Copy ANSI escape sequence verbatim.
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
			buf.WriteString(s[i:j])
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		buf.WriteString(s[i : i+size])
		visWidth++
		i += size
	}
	buf.WriteString("\x1b[0m…")
	return buf.String()
}

// isCSITerminator reports whether b is a CSI sequence final byte.
func isCSITerminator(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}
