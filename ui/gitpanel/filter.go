package gitpanel

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/theme"
)

// filterFocusZone identifies which interactive zone owns keyboard input
// when the filter bar is active.
type filterFocusZone int

const (
	focusEntries      filterFocusZone = iota // list entries (normal navigation)
	focusFilter                              // filter text input
	focusToggles                             // case/word/regex toggle badges
	focusHeader                              // column header row
	focusGearDropdown                        // column visibility dropdown
)

// toggleIndex identifies a filter toggle badge.
type toggleIndex int

const (
	toggleCase  toggleIndex = iota // case sensitivity
	toggleWord                     // whole-word matching
	toggleRegex                    // regex mode
	toggleCount                    // sentinel: total toggle count
)

// listEntry is the common interface that every tab's entry type must satisfy.
type listEntry interface {
	FilterText() string  // text to match against filter
	SortKey() string     // for alphabetical sorting
	SortTime() time.Time // for age sorting
}

// minListLoadingBarDuration is the minimum time the bottom loading bar stays
// visible. Prevents flicker when pages load faster than a few spinner frames.
// Derived from: 5 spinner frames × 80ms = 400ms ≈ half a braille cycle.
const minListLoadingBarDuration = 400 * time.Millisecond

// headerState holds column header interaction state.
type headerState struct {
	cursor   int // focused visible-column index
	dragFrom int // column being dragged (-1 = none); must init to -1
	dragX    int // current X during drag

	gearOpen   bool // visibility dropdown active
	gearCursor int  // cursor in dropdown

	// Divider drag-resize state.
	resizing       bool  // true while dragging a divider
	resizeDivIdx   int   // index into VisibleColumns of the left column
	resizeStartX   int   // X at drag start
	resizeSnapshot []int // widths of all visible columns at drag start
	resizeVisMap   []int // vis index → full Columns slice index

	// Hover state: visible column index of the divider under the mouse,
	// or -1 when not hovering a divider.
	hoverDivider int
}

// newHeaderState returns a properly initialized headerState.
func newHeaderState() headerState {
	return headerState{dragFrom: -1, hoverDivider: -1}
}

// listState holds the shared scrollable-list + filter state used by all four
// tabs. Each tab embeds one of these.
type listState struct {
	entries  []listEntry
	filtered []int // indices into entries after filter+sort
	cursor   int
	scrollOff int

	filterActive bool
	filterQuery  []rune
	toggleActive [toggleCount]bool
	toggleCursor toggleIndex
	filterFocus  filterFocusZone

	// Column layout (set per-tab, holds sort state).
	columns *ColumnLayout

	// Header interaction state.
	header headerState

	// Pagination state.
	hasMore         bool      // more pages available after window
	loadingMore     bool      // true while a forward "load more" page is in flight
	loadingEpoch    time.Time // start time for spinner animation
	loadingBarUntil time.Time // minimum display deadline for loading bar

	// Initial load indicator: true from LoadData() until first page arrives.
	initialLoading bool

	// Sliding window: entries holds at most 2×listPageSize entries.
	// windowStart is the absolute index of entries[0] in the full data set.
	windowStart int  // absolute offset of first loaded entry
	loadingPrev bool // true while a backward page is in flight
}

// showLoadingBar reports whether a loading bar should be visible.
// True while a page (forward or backward) is in flight OR until the
// minimum display time elapses.
func (ls *listState) showLoadingBar() bool {
	return ls.loadingMore || ls.loadingPrev || time.Now().Before(ls.loadingBarUntil)
}

// showLoadingPrev reports whether the loading bar should appear at the top
// (backward page fetch in progress).
func (ls *listState) showLoadingPrev() bool {
	return ls.loadingPrev || (ls.windowStart > 0 && time.Now().Before(ls.loadingBarUntil) && !ls.loadingMore)
}

// needsLoadMore reports whether the cursor is near enough to the bottom
// that the next page should be fetched.
func (ls *listState) needsLoadMore(height int) bool {
	return ls.hasMore && !ls.loadingMore && ls.cursor >= len(ls.filtered)-height
}

// needsScrollLoadMore reports whether the scroll position is near enough
// to the bottom that the next page should be fetched.
func (ls *listState) needsScrollLoadMore(height int) bool {
	return ls.hasMore && !ls.loadingMore && ls.scrollOff+height >= len(ls.filtered)-height
}

// resetPagination clears all pagination state for a full reload.
func (ls *listState) resetPagination() {
	ls.hasMore = false
	ls.loadingMore = false
	ls.loadingPrev = false
	ls.loadingEpoch = time.Time{}
	ls.loadingBarUntil = time.Time{}
	ls.windowStart = 0
}

// needsLoadPrev reports whether a backward page fetch is needed.
func (ls *listState) needsLoadPrev() bool {
	return ls.windowStart > 0 && !ls.loadingMore && !ls.loadingPrev &&
		ls.scrollOff == 0
}

// maxWindowEntries is the maximum entries kept in the sliding window
// (current page + previous page).
const maxWindowEntries = 2 * listPageSize

// evictOldEntries trims entries from the front when the window exceeds
// 2 pages. Cursor and scroll are adjusted to preserve the user's position.
func (ls *listState) evictOldEntries() {
	if len(ls.entries) <= maxWindowEntries {
		return
	}

	// Remember the absolute index under the cursor.
	cursorAbs := ls.cursorAbsoluteIdx()

	evicted := len(ls.entries) - maxWindowEntries
	remaining := make([]listEntry, maxWindowEntries)
	copy(remaining, ls.entries[evicted:])
	ls.entries = remaining
	ls.windowStart += evicted

	ls.rebuildFiltered()
	ls.restoreCursorAbsolute(cursorAbs)
}

// evictNewEntries trims entries from the back when the window exceeds
// 2 pages (used after prepending a backward page).
func (ls *listState) evictNewEntries() {
	if len(ls.entries) <= maxWindowEntries {
		return
	}
	ls.entries = ls.entries[:maxWindowEntries]
	// Entries trimmed from the end → hasMore is true since data exists beyond.
	ls.hasMore = true
}

// cursorAbsoluteIdx returns the absolute entry index (relative to the full
// data set) that the cursor currently points to.
func (ls *listState) cursorAbsoluteIdx() int {
	if ls.cursor < len(ls.filtered) {
		return ls.filtered[ls.cursor] + ls.windowStart
	}
	return ls.windowStart
}

// restoreCursorAbsolute sets the cursor to the filtered entry closest to
// the given absolute index.
func (ls *listState) restoreCursorAbsolute(absIdx int) {
	relIdx := absIdx - ls.windowStart
	for i, fi := range ls.filtered {
		if fi >= relIdx {
			ls.cursor = i
			ls.clampCursor()
			return
		}
	}
	ls.cursor = max(len(ls.filtered)-1, 0)
	ls.clampCursor()
}

// rebuildFiltered applies the current filter query, toggles, and column sort
// to produce the filtered index slice.
func (ls *listState) rebuildFiltered() {
	ls.filtered = ls.filtered[:0]

	matcher := ls.buildMatcher()

	for i, e := range ls.entries {
		if matcher(e.FilterText()) {
			ls.filtered = append(ls.filtered, i)
		}
	}

	ls.applySortOrder()
	ls.clampCursor()
}

// buildMatcher returns a function that tests whether a text matches the
// current filter configuration.
func (ls *listState) buildMatcher() func(string) bool {
	if len(ls.filterQuery) == 0 {
		return func(string) bool { return true }
	}

	query := string(ls.filterQuery)

	if ls.toggleActive[toggleRegex] {
		return ls.buildRegexMatcher(query)
	}

	return ls.buildTextMatcher(query)
}

// buildRegexMatcher compiles a regex pattern and returns a match function.
func (ls *listState) buildRegexMatcher(query string) func(string) bool {
	flags := ""
	if !ls.effectiveCaseSensitive(query) {
		flags = "(?i)"
	}

	re, err := regexp.Compile(flags + query)
	if err != nil {
		return func(string) bool { return false }
	}

	return re.MatchString
}

// buildTextMatcher returns a substring or whole-word match function.
func (ls *listState) buildTextMatcher(query string) func(string) bool {
	caseSensitive := ls.effectiveCaseSensitive(query)

	if !caseSensitive {
		query = strings.ToLower(query)
	}

	if ls.toggleActive[toggleWord] {
		return func(text string) bool {
			return containsWholeWord(text, query, caseSensitive)
		}
	}

	return func(text string) bool {
		if !caseSensitive {
			text = strings.ToLower(text)
		}
		return strings.Contains(text, query)
	}
}

// effectiveCaseSensitive returns true when matching should be case-sensitive.
// When the toggle is off, smart-case is used: case-insensitive unless the
// query contains an uppercase letter.
func (ls *listState) effectiveCaseSensitive(query string) bool {
	if ls.toggleActive[toggleCase] {
		return true
	}

	for _, r := range query {
		if unicode.IsUpper(r) {
			return true
		}
	}

	return false
}

// containsWholeWord checks if text contains query as a whole word.
func containsWholeWord(text, query string, caseSensitive bool) bool {
	if !caseSensitive {
		text = strings.ToLower(text)
	}

	idx := 0
	for {
		pos := strings.Index(text[idx:], query)
		if pos < 0 {
			return false
		}
		absPos := idx + pos
		endPos := absPos + len(query)

		leftOk := absPos == 0 || !isWordChar(rune(text[absPos-1]))
		rightOk := endPos == len(text) || !isWordChar(rune(text[endPos]))

		if leftOk && rightOk {
			return true
		}
		idx = absPos + 1
	}
}

// isWordChar returns true for alphanumeric characters and underscore.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// applySortOrder sorts the filtered indices using the column layout's active
// sort function.
func (ls *listState) applySortOrder() {
	if ls.columns == nil {
		return
	}
	ls.columns.ApplySortToFiltered(ls.entries, ls.filtered)
}

// clampCursor ensures cursor and scrollOff remain within valid bounds.
func (ls *listState) clampCursor() {
	n := len(ls.filtered)
	if n == 0 {
		ls.cursor = 0
		ls.scrollOff = 0
		return
	}
	ls.cursor = clamp(ls.cursor, 0, n-1)
}

// moveDown moves the cursor down by n positions, adjusting scroll.
func (ls *listState) moveDown(n int) {
	if len(ls.filtered) == 0 {
		return
	}
	ls.cursor = clamp(ls.cursor+n, 0, len(ls.filtered)-1)
}

// moveUp moves the cursor up by n positions, adjusting scroll.
func (ls *listState) moveUp(n int) {
	if len(ls.filtered) == 0 {
		return
	}
	ls.cursor = clamp(ls.cursor-n, 0, len(ls.filtered)-1)
}

// visibleEntries returns the start (inclusive) and end (exclusive) indices into
// the filtered slice that should be rendered for the given viewport height.
func (ls *listState) visibleEntries(height int) (start, end int) {
	n := len(ls.filtered)
	if n == 0 || height <= 0 {
		return 0, 0
	}

	// Adjust scroll offset so cursor is always visible.
	if ls.cursor < ls.scrollOff {
		ls.scrollOff = ls.cursor
	}
	if ls.cursor >= ls.scrollOff+height {
		ls.scrollOff = ls.cursor - height + 1
	}
	ls.scrollOff = clamp(ls.scrollOff, 0, max(n-height, 0))

	start = ls.scrollOff
	end = min(start+height, n)
	return start, end
}

// handleFilterKey processes a key event when the filter bar is active.
// Returns true if the key was consumed.
func (ls *listState) handleFilterKey(key tea.KeyMsg) bool {
	switch ls.filterFocus {
	case focusFilter:
		return ls.handleFilterInputKey(key)
	case focusToggles:
		return ls.handleToggleKey(key)
	case focusHeader:
		return ls.handleHeaderKey(key)
	case focusGearDropdown:
		return ls.handleGearDropdownKey(key)
	default:
		return ls.handleFilterEntriesKey(key)
	}
}

// handleFilterInputKey processes keys directed at the text input.
func (ls *listState) handleFilterInputKey(key tea.KeyMsg) bool {
	switch key.String() {
	case "tab":
		ls.filterFocus = focusToggles
		ls.toggleCursor = 0
	case "shift+tab":
		ls.filterFocus = focusHeader
		ls.header.cursor = lastHeaderCol(ls.columns.VisibleColumns())
	case "esc":
		ls.deactivateFilter()
	case "enter", "down":
		ls.filterFocus = focusEntries
	case "backspace":
		if len(ls.filterQuery) > 0 {
			ls.filterQuery = ls.filterQuery[:len(ls.filterQuery)-1]
			ls.rebuildFiltered()
		} else {
			ls.deactivateFilter()
		}
	default:
		if key.Type == tea.KeyRunes {
			ls.filterQuery = append(ls.filterQuery, key.Runes...)
			ls.rebuildFiltered()
		} else if key.Type == tea.KeySpace {
			ls.filterQuery = append(ls.filterQuery, ' ')
			ls.rebuildFiltered()
		} else {
			return false
		}
	}
	return true
}

// handleToggleKey processes keys directed at the filter toggle badges.
// Tab/right advance through individual badges; after the last badge,
// focus moves to the header row. Shift+tab/left reverse; before the first
// badge, focus returns to the filter input.
func (ls *listState) handleToggleKey(key tea.KeyMsg) bool {
	switch key.String() {
	case "tab", "right", "l":
		if ls.toggleCursor < toggleCount-1 {
			ls.toggleCursor++
		} else {
			ls.filterFocus = focusHeader
			ls.header.cursor = firstHeaderCol(ls.columns.VisibleColumns())
		}
	case "shift+tab", "left", "h":
		if ls.toggleCursor > 0 {
			ls.toggleCursor--
		} else {
			ls.filterFocus = focusFilter
		}
	case "enter", " ":
		ls.toggleActive[ls.toggleCursor] = !ls.toggleActive[ls.toggleCursor]
		ls.rebuildFiltered()
	case "esc":
		ls.deactivateFilter()
	default:
		return false
	}
	return true
}

// handleHeaderKey processes keys directed at the column header row.
// Cursor positions 0..vis-1 are columns; position vis is the gear icon.
// Navigation between columns uses Tab/Shift+Tab only (not arrows).
func (ls *listState) handleHeaderKey(key tea.KeyMsg) bool {
	vis := ls.columns.VisibleColumns()
	if len(vis) == 0 {
		return false
	}

	switch key.String() {
	case "tab":
		if ls.header.cursor >= len(vis) {
			// Past gear icon — exit header.
			if ls.filterActive {
				ls.filterFocus = focusFilter
			} else {
				ls.filterFocus = focusEntries
			}
		} else {
			ls.header.cursor = nextHeaderCol(vis, ls.header.cursor)
		}
	case "enter", " ":
		if ls.header.cursor >= len(vis) {
			// Toggle gear dropdown.
			if ls.header.gearOpen {
				ls.header.gearOpen = false
			} else {
				ls.header.gearOpen = true
				ls.header.gearCursor = 0
				ls.filterFocus = focusGearDropdown
			}
		} else {
			ls.cycleSortOnHeaderCursor()
		}
	case "shift+left":
		ls.reorderHeaderColumn(-1)
	case "shift+right":
		ls.reorderHeaderColumn(1)
	case "shift+tab":
		if ls.header.cursor >= len(vis) {
			// At gear icon — go to last navigable column.
			last := lastHeaderCol(vis)
			if last < len(vis) {
				ls.header.cursor = last
			} else {
				ls.exitHeaderBack()
			}
		} else {
			prev := prevHeaderCol(vis, ls.header.cursor)
			if prev >= 0 {
				ls.header.cursor = prev
			} else {
				ls.exitHeaderBack()
			}
		}
	case "esc":
		ls.filterFocus = focusEntries
	default:
		return false
	}
	return true
}

// exitHeaderBack moves focus out of the header to the previous zone.
func (ls *listState) exitHeaderBack() {
	if ls.filterActive {
		ls.filterFocus = focusToggles
		ls.toggleCursor = toggleCount - 1
	} else {
		ls.filterFocus = focusEntries
	}
}

// firstHeaderCol returns the index of the first visible column with a
// non-empty label, or len(vis) if none exist (gear icon position).
func firstHeaderCol(vis []ColumnState) int {
	for i, c := range vis {
		if c.Def.Label != "" {
			return i
		}
	}
	return len(vis)
}

// lastHeaderCol returns the index of the last visible column with a
// non-empty label, or len(vis) if none exist (gear icon position).
func lastHeaderCol(vis []ColumnState) int {
	for i := len(vis) - 1; i >= 0; i-- {
		if vis[i].Def.Label != "" {
			return i
		}
	}
	return len(vis)
}

// nextHeaderCol returns the index of the next navigable visible column
// after from, or len(vis) for the gear icon position.
func nextHeaderCol(vis []ColumnState, from int) int {
	for i := from + 1; i < len(vis); i++ {
		if vis[i].Def.Label != "" {
			return i
		}
	}
	return len(vis)
}

// prevHeaderCol returns the index of the previous navigable visible column
// before from, or -1 if none.
func prevHeaderCol(vis []ColumnState, from int) int {
	for i := from - 1; i >= 0; i-- {
		if vis[i].Def.Label != "" {
			return i
		}
	}
	return -1
}

// cycleSortOnHeaderCursor cycles sorting on the column under the header cursor.
func (ls *listState) cycleSortOnHeaderCursor() {
	vis := ls.columns.VisibleColumns()
	if ls.header.cursor >= len(vis) {
		return
	}
	ls.columns.CycleSort(vis[ls.header.cursor].Def.ID)
	ls.rebuildFiltered()
}

// reorderHeaderColumn moves the column under the header cursor by delta
// positions (-1 = left, +1 = right) within the full Columns slice.
func (ls *listState) reorderHeaderColumn(delta int) {
	vis := ls.columns.VisibleColumns()
	if ls.header.cursor >= len(vis) {
		return
	}

	// Find the full-slice index of the visible column at cursor.
	colID := vis[ls.header.cursor].Def.ID
	fullIdx := -1
	for i, c := range ls.columns.Columns {
		if c.Def.ID == colID {
			fullIdx = i
			break
		}
	}
	if fullIdx < 0 {
		return
	}

	newIdx := clamp(fullIdx+delta, 0, len(ls.columns.Columns)-1)
	ls.columns.Reorder(fullIdx, newIdx)

	// Adjust header cursor to track the moved column.
	newVis := ls.columns.VisibleColumns()
	for i, c := range newVis {
		if c.Def.ID == colID {
			ls.header.cursor = i
			break
		}
	}
}

// handleGearDropdownKey processes keys when the gear dropdown is open.
// The cursor indexes into the dropdown-visible columns (excludes decorative
// empty-label columns).
func (ls *listState) handleGearDropdownKey(key tea.KeyMsg) bool {
	indices := ls.columns.dropdownIndices()
	n := len(indices)
	if n == 0 {
		return false
	}

	switch key.String() {
	case "up", "k":
		if ls.header.gearCursor > 0 {
			ls.header.gearCursor--
		} else {
			// At top — close dropdown.
			ls.header.gearOpen = false
			ls.filterFocus = focusHeader
		}
	case "down", "j":
		ls.header.gearCursor = min(ls.header.gearCursor+1, n-1)
	case "enter", " ":
		colIdx := indices[ls.header.gearCursor]
		col := ls.columns.Columns[colIdx]
		ls.columns.ToggleVisibility(col.Def.ID)
		ls.rebuildFiltered()
	case "esc", "tab":
		ls.header.gearOpen = false
		ls.filterFocus = focusHeader
	default:
		return false
	}
	return true
}

// handleFilterEntriesKey processes keys when focus is on entries but filter is
// visible.
func (ls *listState) handleFilterEntriesKey(key tea.KeyMsg) bool {
	switch key.String() {
	case "tab":
		ls.filterFocus = focusHeader
		ls.header.cursor = firstHeaderCol(ls.columns.VisibleColumns())
	case "shift+tab":
		ls.filterFocus = focusHeader
		ls.header.cursor = lastHeaderCol(ls.columns.VisibleColumns())
	case "up", "k":
		ls.moveUp(1)
	case "down", "j":
		ls.moveDown(1)
	case "esc":
		ls.deactivateFilter()
	default:
		return false
	}
	return true
}

// deactivateFilter turns off the filter bar and resets focus.
func (ls *listState) deactivateFilter() {
	ls.filterActive = false
	ls.filterFocus = focusEntries
	ls.filterQuery = ls.filterQuery[:0]
	ls.rebuildFiltered()
}

// activateFilter turns on the filter bar and sets focus to the input.
func (ls *listState) activateFilter() {
	ls.filterActive = true
	ls.filterFocus = focusFilter
}

// -------------------------------------------------------------------------
// Rendering
// -------------------------------------------------------------------------

// renderSearchHint renders the muted "/ search" hint when the filter is
// inactive, matching the file tree's search hint style.
func renderSearchHint(width int, th *theme.Theme) string {
	text := lipgloss.NewStyle().Foreground(th.Palette.Muted).Render("/ search")
	padCount := max(width-lipgloss.Width(text), 0)
	if padCount > 0 {
		return text + strings.Repeat(" ", padCount)
	}
	return text
}

// renderFilterBar renders the filter text input line, matching the file
// tree's search bar style: "/ " prefix (Muted) + query + block cursor.
func renderFilterBar(ls *listState, width int, th *theme.Theme, cursorVisible bool) string {
	p := th.Palette

	prefix := lipgloss.NewStyle().Foreground(p.Muted).Render("/ ")
	queryStyle := lipgloss.NewStyle().Foreground(p.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	// Reserve 1 column for cursor so layout is stable.
	prefixWidth := lipgloss.Width(prefix)
	availableForQuery := max(width-prefixWidth-1, 0)

	// Pre-truncate query; show tail so recent keystrokes stay visible.
	queryStr := string(ls.filterQuery)
	queryRunes := []rune(queryStr)
	if len(queryRunes) > availableForQuery {
		queryStr = string(queryRunes[len(queryRunes)-availableForQuery:])
	}

	// Block cursor when focused and visible; space otherwise.
	cursor := " "
	if ls.filterFocus == focusFilter && cursorVisible {
		cursor = cursorStyle.Render(" ")
	}

	line := prefix + queryStyle.Render(queryStr) + cursor
	return padToWidth(line, width, p)
}

// renderFilterToggles renders the " [Aa] [ab] [.*]" toggle badges line,
// matching the file tree's toolbar style with a leading space.
func renderFilterToggles(ls *listState, width int, th *theme.Theme) string {
	p := th.Palette

	labels := [toggleCount]string{"Aa", "ab", ".*"}
	var b strings.Builder
	b.WriteByte(' ')

	for i := range toggleCount {
		if i > 0 {
			b.WriteByte(' ')
		}
		ti := toggleIndex(i)
		style := badgeStyle(p, ls.toggleActive[ti], ls.filterFocus == focusToggles && ls.toggleCursor == ti)
		b.WriteString(style.Render("[" + labels[ti] + "]"))
	}

	return padToWidth(b.String(), width, p)
}

// badgeStyle returns the lipgloss style for a toggle/sort badge given its
// active and focused states.
func badgeStyle(p theme.Palette, active, focused bool) lipgloss.Style {
	s := lipgloss.NewStyle()

	if active {
		s = s.Foreground(p.Primary)
	} else {
		s = s.Foreground(p.Muted)
	}

	if focused {
		s = s.Background(p.Selection)
	}

	return s
}

// padToWidth pads a rendered string to the given width using background-
// colored spaces to prevent visual artifacts.
func padToWidth(content string, width int, p theme.Palette) string {
	contentWidth := lipgloss.Width(content)
	if contentWidth >= width {
		return content
	}

	pad := strings.Repeat(" ", width-contentWidth)
	return content + lipgloss.NewStyle().Foreground(p.Muted).Render(pad)
}

// -------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
