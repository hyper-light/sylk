package gitpanel

import (
	"regexp"
	"sort"
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
	focusEntries     filterFocusZone = iota // list entries (normal navigation)
	focusFilter                             // filter text input
	focusToggles                            // case/word/regex toggle badges
	focusSortToggles                        // sort mode badges
)

// filterFocusZoneCount is the total number of focus zones (derived, not magic).
const filterFocusZoneCount = int(focusSortToggles) + 1

// toggleIndex identifies a filter toggle badge.
type toggleIndex int

const (
	toggleCase  toggleIndex = iota // case sensitivity
	toggleWord                     // whole-word matching
	toggleRegex                    // regex mode
	toggleCount                    // sentinel: total toggle count
)

// sortMode identifies the active sort ordering.
type sortMode int

const (
	sortAgeDesc  sortMode = iota // newest first (default)
	sortAgeAsc                   // oldest first
	sortAlphaAsc                 // A-Z
	sortAlphaDesc                // Z-A
	sortModeCount                // sentinel: total sort mode count
)

// listEntry is the common interface that every tab's entry type must satisfy.
type listEntry interface {
	FilterText() string  // text to match against filter
	SortKey() string     // for alphabetical sorting
	SortTime() time.Time // for age sorting
}

// listState holds the shared scrollable-list + filter state used by all three
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

	sortMode   sortMode
	sortCursor sortMode // focused badge (independent of active sort)
}

// rebuildFiltered applies the current filter query, toggles, and sort mode to
// produce the filtered index slice.
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

// applySortOrder sorts the filtered indices according to the active sort mode.
func (ls *listState) applySortOrder() {
	entries := ls.entries

	sort.SliceStable(ls.filtered, func(i, j int) bool {
		a, b := entries[ls.filtered[i]], entries[ls.filtered[j]]
		return ls.sortLess(a, b)
	})
}

// sortLess returns true when a should appear before b under the active sort.
func (ls *listState) sortLess(a, b listEntry) bool {
	switch ls.sortMode {
	case sortAgeAsc:
		return a.SortTime().Before(b.SortTime())
	case sortAlphaAsc:
		return a.SortKey() < b.SortKey()
	case sortAlphaDesc:
		return a.SortKey() > b.SortKey()
	default: // sortAgeDesc
		return a.SortTime().After(b.SortTime())
	}
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
	case focusSortToggles:
		return ls.handleSortToggleKey(key)
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
		ls.filterFocus = focusSortToggles
		ls.sortCursor = sortModeCount - 1
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
// focus moves to sort toggles. Shift+tab/left reverse; before the first
// badge, focus returns to the filter input.
func (ls *listState) handleToggleKey(key tea.KeyMsg) bool {
	switch key.String() {
	case "tab", "right", "l":
		if ls.toggleCursor < toggleCount-1 {
			ls.toggleCursor++
		} else {
			ls.filterFocus = focusSortToggles
			ls.sortCursor = 0
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

// handleSortToggleKey processes keys directed at the sort mode badges.
// Tab/right move the sort cursor between badges; enter/space activates
// the focused badge. After the last badge, focus moves to entries.
// Shift+tab/left reverse; before the first badge, focus returns to
// filter toggles (at their last badge).
func (ls *listState) handleSortToggleKey(key tea.KeyMsg) bool {
	switch key.String() {
	case "tab", "right", "l":
		if ls.sortCursor < sortModeCount-1 {
			ls.sortCursor++
		} else {
			ls.filterFocus = focusFilter
		}
	case "shift+tab", "left", "h":
		if ls.sortCursor > 0 {
			ls.sortCursor--
		} else {
			ls.filterFocus = focusToggles
			ls.toggleCursor = toggleCount - 1
		}
	case "enter", " ":
		ls.sortMode = ls.sortCursor
		ls.rebuildFiltered()
	case "esc":
		ls.deactivateFilter()
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
		ls.filterFocus = focusFilter
	case "shift+tab":
		ls.filterFocus = focusSortToggles
		ls.sortCursor = sortModeCount - 1
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
	ls.sortCursor = ls.sortMode
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

// renderSortToggles renders the sort mode radio badges on a separate line.
func renderSortToggles(ls *listState, width int, th *theme.Theme) string {
	p := th.Palette

	labels := [sortModeCount]string{"t\u2193", "t\u2191", "A\u2193", "A\u2191"}
	var b strings.Builder
	b.WriteByte(' ')

	for i := range sortModeCount {
		if i > 0 {
			b.WriteByte(' ')
		}
		sm := sortMode(i)
		active := ls.sortMode == sm
		focused := ls.filterFocus == focusSortToggles && ls.sortCursor == sm
		style := badgeStyle(p, active, focused)
		b.WriteString(style.Render("[" + labels[sm] + "]"))
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
