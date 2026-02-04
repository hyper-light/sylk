package filetree

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// indentWidth is the number of spaces per nesting level.
// Derived from: 2 spaces per level is standard for compact tree views.
const indentWidth = 2

// headerHeight is the vertical space consumed by the header.
// Derived from: 1 title line = 1.
const headerHeight = 1

// footerHeight is the vertical space consumed by the bottom line in tree mode.
// Derived from: 1 search hint line = 1.
const footerHeight = 1

// searchFooterHeight is the vertical space consumed by the search-mode footer.
// Derived from: divider(1) + search input(1) + toolbar separator(1) + toolbar(1) = 4.
const searchFooterHeight = 4

// iconDir is the prefix glyph for expanded directories.
const iconDir = theme.IconCollapse

// iconDirClosed is the prefix glyph for collapsed directories.
const iconDirClosed = theme.IconExpand

// iconFile is the prefix glyph for regular files.
const iconFile = " "

// viewMode tracks whether the panel is showing the tree or search results.
type viewMode int

const (
	viewTree   viewMode = iota
	viewSearch
)

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

// searchFocus identifies which element in the search footer has keyboard focus.
type searchFocus int

const (
	focusQuery   searchFocus = iota // The search query input line.
	focusToggles                    // The toggle badges row.
	focusScope                      // The path scope text input.
)

// searchFocusCount is the number of focus zones in the search footer.
// Derived from: the three searchFocus values above.
const searchFocusCount = 3

// toggleIndex identifies a specific toggle badge within the toolbar.
type toggleIndex int

const (
	toggleCase  toggleIndex = iota // [Aa] case sensitivity.
	toggleWord                     // [ab] whole word.
	toggleRegex                    // [.*] regex mode.
)

// toggleCount is the number of toggle badges.
// Derived from: the three toggleIndex values above.
const toggleCount = 3

// badgeLabels maps each toggle index to its display string.
// Derived from: the three search mode indicators [Aa], [ab], [.*].
var badgeLabels = [toggleCount]string{"Aa", "ab", ".*"}

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

// maxSearchScanFiles bounds the number of files whose contents are read.
// Derived from: 256 files keeps per-keystroke I/O under ~16MB.
const maxSearchScanFiles = 256

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

// maxFileCacheSize caps total cached file content to prevent unbounded memory.
// Derived from: 64MB covers 256 files × 256KB average with headroom.
const maxFileCacheSize = 64 << 20

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
// eliminate redundant disk I/O across keystrokes.
type fileCache struct {
	entries   map[string]*cachedFile
	totalSize int64
}

// newFileCache creates an empty file cache.
func newFileCache() *fileCache {
	return &fileCache{entries: make(map[string]*cachedFile, maxSearchScanFiles)}
}

// get returns the cached lines for path, or nil if not cached.
func (c *fileCache) get(path string) []string {
	if e, ok := c.entries[path]; ok {
		return e.lines
	}
	return nil
}

// put stores file lines in the cache if space permits.
func (c *fileCache) put(path string, lines []string, size int64) {
	if c.totalSize+size > maxFileCacheSize {
		return
	}
	c.entries[path] = &cachedFile{lines: lines, size: size}
	c.totalSize += size
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
	searchSource   []Entry      // All files found recursively (built on search enter).
	searchItems    []searchItem // Flat list of file headers, match lines, and gaps.
	searchCursor   int
	searchScroll   int
	searchNumWidth int // Digit width of the largest line number in results.

	// Search performance: debounce, caching, pre-indexed scope.
	searchVersion int        // Monotonic counter; gates stale debounce ticks.
	searchCache   *fileCache // Session-scoped file content cache.
	scopeIndex    map[string][]int // dir prefix → indices into searchSource.

	// Search toolbar state.
	searchFocus    searchFocus        // Active focus zone within search footer.
	toggleActive   [toggleCount]bool  // State of [Aa], [ab], [.*] toggles.
	toggleCursor   toggleIndex        // Which badge is selected when focusToggles.
	scopeQuery     []rune             // Path scope text input contents.
	compiledRegexp *regexp.Regexp     // Cached compiled regex (nil if invalid/unused).

	// Snapshot of tree state before entering search, restored on exit.
	savedEntries []Entry
	savedCursor  int
	savedScroll  int
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
		theme: th,
	}
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
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(typed)
	case searchTickMsg:
		return m, m.handleSearchTick(typed)
	default:
		return m, nil
	}
}

// handleSearchTick processes a debounced search tick. If the version matches
// the current searchVersion, runs the grep pipeline; otherwise discards it.
func (m *Model) handleSearchTick(tick searchTickMsg) tea.Cmd {
	if tick.version != m.searchVersion {
		return nil
	}
	m.runSearch()
	return nil
}

// View dispatches to the active view mode.
func (m *Model) View() string {
	if m.mode == viewSearch {
		return m.viewSearchMode()
	}
	return m.viewTreeMode()
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
	if m.mode == viewSearch {
		return &m.searchScroll
	}
	return &m.scroll
}

// visibleItemCount returns the number of items in the active list.
func (m *Model) visibleItemCount() int {
	if m.mode == viewSearch {
		return len(m.searchItems)
	}
	return len(m.entries)
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

// ClickAt handles a left-click at the given content-relative coordinates.
// viewX is the column offset within the panel content area; viewY is the
// row offset (0 = first line inside the panel border).
func (m *Model) ClickAt(viewX, viewY int) tea.Cmd {
	if m.mode == viewSearch {
		return m.clickSearchMode(viewX, viewY)
	}
	return m.clickTreeMode(viewY)
}

// clickTreeMode handles clicks in tree browsing mode.
func (m *Model) clickTreeMode(viewY int) tea.Cmd {
	bodyY := viewY - headerHeight
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

// clickSearchMode handles clicks in search mode, dispatching to body,
// search input, or toolbar based on Y position.
func (m *Model) clickSearchMode(viewX, viewY int) tea.Cmd {
	bh := m.bodyHeight()
	bodyY := viewY - headerHeight

	// Click in body area: activate search result.
	if bodyY >= 0 && bodyY < bh {
		return m.clickSearchBody(bodyY)
	}

	// Footer layout (relative to headerHeight + bh):
	// +0 divider, +1 input, +2 toolbar separator, +3 toolbar.
	footerBase := headerHeight + bh
	switch viewY {
	case footerBase + 1:
		m.searchFocus = focusQuery
	case footerBase + 3:
		return m.clickToolbar(viewX)
	}
	return nil
}

// clickSearchBody activates a search result at the given body-relative row.
func (m *Model) clickSearchBody(bodyY int) tea.Cmd {
	idx := m.searchScroll + bodyY
	if idx < 0 || idx >= len(m.searchItems) {
		return nil
	}
	if m.searchItems[idx].kind == searchItemGap {
		return nil
	}
	m.searchCursor = idx
	return m.activateSearchResult()
}

// clickToolbar dispatches a click on the toolbar line to toggle badges
// or focus the scope input based on X position.
func (m *Model) clickToolbar(viewX int) tea.Cmd {
	// Badge layout: " [Aa] [ab] [.*]  In: path"
	// Compute cumulative X ranges for each badge.
	x := 1 // leading space
	for i := range toggleCount {
		if i > 0 {
			x++ // space between badges
		}
		badgeW := len(badgeLabels[i]) + 2 // "[" + label + "]"
		if viewX >= x && viewX < x+badgeW {
			m.searchFocus = focusToggles
			m.toggleCursor = toggleIndex(i)
			return m.toggleBadge(toggleIndex(i))
		}
		x += badgeW
	}
	// Past badges: scope input area.
	m.searchFocus = focusScope
	return nil
}

// RevealPath expands the directory chain to the given file path and positions
// the cursor on it, like VS Code's "reveal in explorer". Works with both
// absolute paths (when rootPath is set) and relative paths (mock/SetEntries mode).
func (m *Model) RevealPath(targetPath string) {
	if m.mode == viewSearch {
		m.exitSearch()
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
	}
	return nil
}

// handleSearchKey processes keys in search mode, dispatching to the
// appropriate focus zone handler.
func (m *Model) handleSearchKey(key tea.KeyMsg) tea.Cmd {
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

// advanceSearchFocus moves focus to the next zone in the toolbar.
func (m *Model) advanceSearchFocus() {
	m.searchFocus = (m.searchFocus + 1) % searchFocusCount
}

// retreatSearchFocus moves focus to the previous zone in the toolbar.
func (m *Model) retreatSearchFocus() {
	m.searchFocus = (m.searchFocus + searchFocusCount - 1) % searchFocusCount
}

// handleSearchEnter dispatches Enter based on the current focus zone.
func (m *Model) handleSearchEnter() tea.Cmd {
	if m.searchFocus == focusToggles {
		return m.toggleBadge(m.toggleCursor)
	}
	return m.activateSearchResult()
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
	if key.Type != tea.KeyRunes {
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
func (m *Model) handleScopeInput(key tea.KeyMsg) tea.Cmd {
	if key.String() == "backspace" {
		if len(m.scopeQuery) > 0 {
			m.scopeQuery = m.scopeQuery[:len(m.scopeQuery)-1]
			return m.refilter()
		}
		return nil
	}
	if key.Type != tea.KeyRunes {
		return nil
	}
	m.appendToScope([]rune(key.String()))
	return m.refilter()
}

// appendToScope appends runes to the scope query, capped at maxInputLen.
func (m *Model) appendToScope(runes []rune) {
	remaining := maxInputLen - len(m.scopeQuery)
	if remaining <= 0 {
		return
	}
	if len(runes) > remaining {
		runes = runes[:remaining]
	}
	m.scopeQuery = append(m.scopeQuery, runes...)
}

// moveToggleCursor moves the toggle badge cursor left or right, clamping.
func (m *Model) moveToggleCursor(delta int) {
	next := int(m.toggleCursor) + delta
	m.toggleCursor = toggleIndex(clampInt(next, 0, toggleCount-1))
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

// maxSearchFiles bounds the recursive file walk to prevent unbounded growth.
// Derived from: 4096 files covers most medium-to-large repos.
const maxSearchFiles = 4096

// maxSearchDepth bounds the recursive walk depth.
// Derived from: 16 levels covers virtually all project structures.
const maxSearchDepth = 16

// enterSearch snapshots the current tree state, walks the filesystem to
// build a flat file index, and switches to search mode. When rootPath is
// set, walks from the project root so all project files are searchable
// regardless of the current tree expansion state.
func (m *Model) enterSearch() {
	m.savedEntries = make([]Entry, len(m.entries))
	copy(m.savedEntries, m.entries)
	m.savedCursor = m.cursor
	m.savedScroll = m.scroll

	if m.rootPath != "" {
		m.searchSource = m.collectFromRoot()
	} else {
		m.searchSource = m.collectFiles(m.entries)
	}

	m.mode = viewSearch
	m.searchQuery = nil
	m.searchItems = nil
	m.searchCursor = 0
	m.searchScroll = 0
	m.searchNumWidth = 0

	// Initialize performance caches.
	m.searchVersion = 0
	m.searchCache = newFileCache()
	m.scopeIndex = buildScopeIndex(m.searchSource, m.rootPath)

	// Initialize toolbar state.
	m.searchFocus = focusQuery
	m.toggleActive = [toggleCount]bool{}
	m.toggleCursor = toggleCase
	m.scopeQuery = nil
	m.compiledRegexp = nil
}

// collectFromRoot walks the rootPath recursively to collect all files,
// providing a consistent search source regardless of tree expansion state.
func (m *Model) collectFromRoot() []Entry {
	files := make([]Entry, 0, maxSearchFiles)
	m.walkDir(m.rootPath, 0, &files)
	return files
}

// exitSearch restores the tree state from before search was entered.
func (m *Model) exitSearch() {
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

	// Release caches.
	m.searchVersion = 0
	m.searchCache = nil
	m.scopeIndex = nil

	// Clear toolbar state.
	m.searchFocus = focusQuery
	m.toggleActive = [toggleCount]bool{}
	m.toggleCursor = toggleCase
	m.scopeQuery = nil
	m.compiledRegexp = nil
}

// collectFiles walks the root entries recursively, collecting all files
// (not directories) into a flat list bounded by maxSearchFiles.
func (m *Model) collectFiles(roots []Entry) []Entry {
	files := make([]Entry, 0, min(len(roots)*8, maxSearchFiles))
	for _, root := range roots {
		if len(files) >= maxSearchFiles {
			break
		}
		if !root.IsDir {
			files = append(files, Entry{
				Name: root.Name,
				Path: root.Path,
			})
			continue
		}
		m.walkDir(root.Path, 0, &files)
	}
	return files
}

// walkDir recursively collects files from a directory, respecting depth
// and count bounds. Hidden files (dot-prefixed) are excluded.
func (m *Model) walkDir(dirPath string, depth int, files *[]Entry) {
	if depth >= maxSearchDepth || len(*files) >= maxSearchFiles {
		return
	}
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		return
	}
	for _, de := range dirEntries {
		if len(*files) >= maxSearchFiles {
			return
		}
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		fullPath := filepath.Join(dirPath, name)
		if de.IsDir() {
			m.walkDir(fullPath, depth+1, files)
		} else {
			*files = append(*files, Entry{
				Name: name,
				Path: fullPath,
			})
		}
	}
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
		return nil
	}

	version := m.searchVersion
	return tea.Tick(searchDebounceInterval, func(time.Time) tea.Msg {
		return searchTickMsg{version: version}
	})
}

// runSearch executes the grep pipeline synchronously. Called from
// handleSearchTick when the debounce fires and the version is still current.
func (m *Model) runSearch() {
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
	m.grepSources(m.scopedSources(), query, cfg)
}

// buildMatchConfig constructs a matchConfig from the current toggle state
// and query content, implementing smart-case behavior.
func (m *Model) buildMatchConfig(query string) matchConfig {
	cfg := matchConfig{
		caseSensitive: m.toggleActive[toggleCase] || hasUpperCase(query),
		wholeWord:     m.toggleActive[toggleWord],
		useRegex:      m.toggleActive[toggleRegex],
	}
	if cfg.useRegex {
		cfg.compiled = compileSearchRegex(query, cfg.caseSensitive)
	}
	return cfg
}

// compileSearchRegex compiles the query as a regex pattern.
// Returns nil if the pattern is invalid.
func compileSearchRegex(pattern string, caseSensitive bool) *regexp.Regexp {
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

// scopedSources returns the subset of searchSource files whose paths
// match the scope prefix. Uses the pre-built scopeIndex for O(matched)
// lookups instead of O(n) full scan.
func (m *Model) scopedSources() []Entry {
	scope := strings.TrimSpace(string(m.scopeQuery))
	if scope == "" {
		return m.searchSource
	}
	return m.lookupScope(scope)
}

// lookupScope returns entries under the given scope using the directory index.
// Collects all index entries whose directory key starts with the scope prefix.
func (m *Model) lookupScope(scope string) []Entry {
	scope = filepath.FromSlash(scope)
	prefix := filepath.Join(m.rootPath, scope)
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	var filtered []Entry
	for dir, indices := range m.scopeIndex {
		if !strings.HasPrefix(dir, prefix) {
			continue
		}
		for _, idx := range indices {
			filtered = append(filtered, m.searchSource[idx])
		}
	}
	return filtered
}

// buildScopeIndex groups searchSource entries by their parent directory
// for efficient scope filtering. Built once at search entry.
func buildScopeIndex(sources []Entry, rootPath string) map[string][]int {
	index := make(map[string][]int, len(sources)/4)
	for i, entry := range sources {
		dir := filepath.Dir(entry.Path)
		if !strings.HasSuffix(dir, string(filepath.Separator)) {
			dir += string(filepath.Separator)
		}
		index[dir] = append(index[dir], i)
	}
	return index
}

// grepSources scans files and builds the searchItems list, using the
// session file cache to avoid redundant disk I/O.
func (m *Model) grepSources(sources []Entry, query string, cfg matchConfig) {
	totalMatches := 0
	maxLine := 0
	scanLimit := min(len(sources), maxSearchScanFiles)

	for i := range scanLimit {
		if totalMatches >= maxTotalMatches {
			break
		}
		matches := m.grepFileWithCache(sources[i].Path, query, cfg)
		if len(matches) == 0 {
			continue
		}
		m.appendFileResults(sources[i].Path, matches, &totalMatches, &maxLine)
	}
	m.searchNumWidth = digitCount(maxLine)
	m.searchCursor = 0
	m.searchScroll = 0
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
func readSearchableFile(path string) []byte {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxSearchFileSize {
		return nil
	}
	data, err := os.ReadFile(path)
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
		if lineMatches(line, query, cfg) {
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

// lineMatches reports whether a single line matches the query with the
// given configuration (regex, case-sensitive, whole-word).
func lineMatches(line, query string, cfg matchConfig) bool {
	if cfg.useRegex {
		return regexLineMatch(line, cfg.compiled)
	}
	return substringLineMatch(line, query, cfg)
}

// regexLineMatch checks whether a compiled regex matches anywhere in line.
func regexLineMatch(line string, re *regexp.Regexp) bool {
	if re == nil {
		return false
	}
	return re.MatchString(line)
}

// substringLineMatch checks for a substring match, honoring case and
// whole-word settings.
func substringLineMatch(line, query string, cfg matchConfig) bool {
	haystack, needle := line, query
	if !cfg.caseSensitive {
		haystack = strings.ToLower(haystack)
		needle = strings.ToLower(needle)
	}
	if !cfg.wholeWord {
		return strings.Contains(haystack, needle)
	}
	return containsWholeWord(haystack, needle)
}

// containsWholeWord reports whether haystack contains needle bounded by
// non-word characters on both sides.
func containsWholeWord(haystack, needle string) bool {
	pos := 0
	for {
		idx := strings.Index(haystack[pos:], needle)
		if idx < 0 {
			return false
		}
		if isWordBoundary(haystack, pos+idx, len(needle)) {
			return true
		}
		pos += idx + 1
	}
}

// isWordBoundary reports whether the substring at [start, start+length)
// is bounded by non-word characters (or string edges).
func isWordBoundary(s string, start, length int) bool {
	if start > 0 && isWordChar(rune(s[start-1])) {
		return false
	}
	end := start + length
	if end < len(s) && isWordChar(rune(s[end])) {
		return false
	}
	return true
}

// isWordChar reports whether r is a word character (letter, digit, or
// underscore). Matches the convention in code/highlight.go.
func isWordChar(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
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
func digitCount(n int) int {
	if n <= 0 {
		return 1
	}
	return int(math.Log10(float64(n))) + 1
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

// ensureSearchCursorVisible keeps the search cursor within the visible window.
func (m *Model) ensureSearchCursorVisible() {
	bh := m.bodyHeight()
	if bh <= 0 {
		return
	}
	if m.searchCursor < m.searchScroll {
		m.searchScroll = m.searchCursor
	}
	if m.searchCursor >= m.searchScroll+bh {
		m.searchScroll = m.searchCursor - bh + 1
	}
	maxScroll := max(len(m.searchItems)-bh, 0)
	m.searchScroll = clampInt(m.searchScroll, 0, maxScroll)
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

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

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

	// Combine header + body + footer.
	var b strings.Builder
	b.WriteString(header)
	for _, line := range bodyLines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	b.WriteByte('\n')
	b.WriteString(m.renderSearchHint(contentWidth))
	return b.String()
}

// ---------------------------------------------------------------------------
// Rendering: search mode
// ---------------------------------------------------------------------------

// viewSearchMode renders the header, search results, divider, search bar,
// and toolbar.
func (m *Model) viewSearchMode() string {
	contentWidth := max(m.width, 1)
	header := m.renderHeader(contentWidth)
	bh := m.bodyHeight()
	emptyLine := strings.Repeat(" ", contentWidth)

	bodyLines := m.renderSearchBody(bh, contentWidth, emptyLine)
	bodyLines = applyBounceShift(bodyLines, m.bounceOffset, bh, emptyLine)

	// Compose: header + body + divider + input + toolbar separator + toolbar.
	var b strings.Builder
	b.WriteString(header)
	for _, line := range bodyLines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	b.WriteByte('\n')
	b.WriteString(m.renderSearchDivider(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchBar(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderToolbarSeparator(contentWidth))
	b.WriteByte('\n')
	b.WriteString(m.renderSearchToolbar(contentWidth))
	return b.String()
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
func (m *Model) renderPopulatedSearchBody(bh, contentWidth int, emptyLine string) []string {
	start := clampInt(m.searchScroll, 0, max(len(m.searchItems)-1, 0))
	end := min(start+bh, len(m.searchItems))
	lines := make([]string, 0, bh)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderSearchItem(i, contentWidth))
	}
	for range bh - (end - start) {
		lines = append(lines, emptyLine)
	}
	return lines
}

// renderSearchBar renders the search input line. The cursor block is only
// shown when the query input is the active focus zone.
func (m *Model) renderSearchBar(contentWidth int) string {
	prefixStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	queryStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)

	prefix := prefixStyle.Render("/ ")
	prefixWidth := lipgloss.Width(prefix)

	cursor := ""
	cursorWidth := 0
	if m.searchFocus == focusQuery {
		cursorStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary)
		cursor = cursorStyle.Render("█")
		cursorWidth = lipgloss.Width(cursor)
	}

	// Pre-truncate query text; show tail so recent keystrokes stay visible.
	queryStr := string(m.searchQuery)
	availableForQuery := max(contentWidth-prefixWidth-cursorWidth, 0)
	queryRunes := []rune(queryStr)
	if len(queryRunes) > availableForQuery {
		queryStr = string(queryRunes[len(queryRunes)-availableForQuery:])
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

// renderSearchDivider renders a horizontal line separating the results body
// from the search input area below.
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

// renderSearchToolbar renders the toolbar line: toggle badges + scope input.
func (m *Model) renderSearchToolbar(contentWidth int) string {
	var b strings.Builder
	b.WriteByte(' ')

	for i := range toggleCount {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(m.renderBadge(toggleIndex(i)))
	}

	// Separator between badges and scope.
	b.WriteString("  ")
	b.WriteString(m.renderScopeInput(max(contentWidth-lipgloss.Width(b.String()), 0)))

	line := b.String()
	lineWidth := lipgloss.Width(line)
	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += strings.Repeat(" ", padCount)
	}
	return line
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

// renderScopeInput renders the path scope field: "In: path█" or "In: path".
func (m *Model) renderScopeInput(availableWidth int) string {
	prefixStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	textStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)

	prefix := prefixStyle.Render(scopePrefix)
	prefixW := lipgloss.Width(prefix)

	cursor := ""
	if m.searchFocus == focusScope {
		cursorStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary)
		cursor = cursorStyle.Render("█")
	}

	scopeStr := string(m.scopeQuery)
	maxTextW := max(availableWidth-prefixW-lipgloss.Width(cursor), 0)
	scopeRunes := []rune(scopeStr)
	if len(scopeRunes) > maxTextW {
		scopeStr = string(scopeRunes[len(scopeRunes)-maxTextW:])
	}

	return prefix + textStyle.Render(scopeStr) + cursor
}

// renderSearchItem renders a single search result row (file header, match line, or gap).
func (m *Model) renderSearchItem(idx, contentWidth int) string {
	item := m.searchItems[idx]
	selected := idx == m.searchCursor && m.focused

	switch item.kind {
	case searchItemFile:
		return m.renderFileHeader(item, contentWidth, selected)
	case searchItemMatch:
		return m.renderMatchLine(item, contentWidth, selected)
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
func (m *Model) renderMatchLine(item searchItem, contentWidth int, selected bool) string {
	numStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	textStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Subtext)
	hlStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Warning)
	padStyle := lipgloss.NewStyle()

	if selected {
		bg := m.theme.Palette.Selection
		numStyle = numStyle.Background(bg)
		textStyle = textStyle.Background(bg)
		hlStyle = hlStyle.Background(bg)
		padStyle = padStyle.Background(bg)
	}

	// Indent + right-aligned line number + gap.
	numStr := fmt.Sprintf("  %*d  ", m.searchNumWidth, item.line)
	prefix := numStyle.Render(numStr)
	prefixWidth := lipgloss.Width(prefix)

	// Trim leading whitespace, pre-truncate to available width, then highlight.
	trimmed := strings.TrimLeft(item.text, " \t")
	trimmed = truncatePlain(trimmed, max(contentWidth-prefixWidth, 0))
	content := m.highlightQuery(trimmed, textStyle, hlStyle)

	line := prefix + content
	lineWidth := lipgloss.Width(line)

	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += padStyle.Render(strings.Repeat(" ", padCount))
	}
	return line
}

// highlightQuery splits text at query occurrences and highlights them,
// respecting the current match configuration for case sensitivity.
func (m *Model) highlightQuery(text string, normalStyle, hlStyle lipgloss.Style) string {
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

	text := headerStyle.Render(" Files ")
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

	icon := iconFile
	nameColor := m.theme.Palette.Foreground
	iconColor := m.theme.Palette.Muted
	if entry.IsDir {
		nameColor = m.theme.Palette.Primary
		if entry.Expanded {
			icon = iconDir
		} else {
			icon = iconDirClosed
		}
	}
	if active {
		icon = iconFileActive
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

	name := truncatePlain(entry.Name, max(contentWidth-prefixWidth, 0))
	line := prefix + nameStyle.Render(name)

	lineWidth := lipgloss.Width(line)
	padCount := max(contentWidth-lineWidth, 0)
	if padCount > 0 {
		line += padStyle.Render(strings.Repeat(" ", padCount))
	}

	return line
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

// bodyHeight returns the number of entry lines visible between the header
// and footer. In search mode the footer is taller (divider + input + toolbar).
func (m *Model) bodyHeight() int {
	footer := footerHeight
	if m.mode == viewSearch {
		footer = searchFooterHeight
	}
	return max(m.height-headerHeight-footer, 1)
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
