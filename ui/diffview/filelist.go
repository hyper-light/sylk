package diffview

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
	devicons "github.com/epilande/go-devicons"
)

// ---------------------------------------------------------------------------
// Search types (mirror filetree search UI)
// ---------------------------------------------------------------------------

// fileSearchFocusZone identifies which element has keyboard focus during search.
type fileSearchFocusZone int

const (
	fileFocusQuery   fileSearchFocusZone = iota // The search query input.
	fileFocusScope                              // The path scope input.
	fileFocusToggles                            // The toggle badges.
)

// fileFocusOrder defines Tab-cycling order: query → scope → toggles.
var fileFocusOrder = [3]fileSearchFocusZone{fileFocusQuery, fileFocusScope, fileFocusToggles}

// fileToggleIdx identifies a toggle badge.
type fileToggleIdx int

const (
	fileToggleCase  fileToggleIdx = iota // [Aa] case sensitivity.
	fileToggleWord                       // [ab] whole word.
	fileToggleRegex                      // [.*] regex mode for query.
	fileToggleGlob                       // [re] regex mode for scope.
)

// fileToggleCount is the number of toggles.
const fileToggleCount = 4

// fileToolbarToggleCount is the number of toggles on the toolbar line.
// [re] renders on the scope line instead.
const fileToolbarToggleCount = 3

// fileBadgeLabels maps each toggle to its display string.
var fileBadgeLabels = [fileToggleCount]string{"Aa", "ab", ".*", "re"}

// fileToggleTabOrder defines Tab-cycling for toggles:
// [re] first (adjacent to scope), then [Aa] [ab] [.*].
var fileToggleTabOrder = [fileToggleCount]fileToggleIdx{
	fileToggleGlob, fileToggleCase, fileToggleWord, fileToggleRegex,
}

// fileSearchHeaderHeight is the number of header lines when search is active:
// header + search bar + divider + scope line + divider = 5.
const fileSearchHeaderHeight = 5

// fileListHeaderHeight is the number of header lines when search is inactive:
// header + search hint + divider = 3.
const fileListHeaderHeight = 3

// ---------------------------------------------------------------------------
// Dispatch maps (package-level, allocated once)
// ---------------------------------------------------------------------------

// fileListKeyActions dispatches normal-mode file list keys to handlers.
var fileListKeyActions = map[string]func(*Model){
	"j":     (*Model).moveFileDown,
	"down":  (*Model).moveFileDown,
	"k":     (*Model).moveFileUp,
	"up":    (*Model).moveFileUp,
	"g":     (*Model).moveFileToStart,
	"home":  (*Model).moveFileToStart,
	"G":     (*Model).moveFileToEnd,
	"end":   (*Model).moveFileToEnd,
	"enter": (*Model).openSelectedFileAsTab,
	"]t":    (*Model).nextTab,
	"[t":    (*Model).prevTab,
	"/":     (*Model).enterFileSearch,
}

// fileSearchKeyActions dispatches search-mode keys to handlers.
var fileSearchKeyActions = map[string]func(*Model){
	"esc":       (*Model).exitFileSearch,
	"enter":     (*Model).handleSearchEnter,
	"tab":       (*Model).advanceSearchTab,
	"shift+tab": (*Model).retreatSearchTab,
	"up":        (*Model).moveFileUp,
	"down":      (*Model).moveFileDown,
}

// fileSearchZoneDispatch routes input to the focused search element.
var fileSearchZoneDispatch = map[fileSearchFocusZone]func(*Model, string){
	fileFocusQuery:   (*Model).handleFileQueryInput,
	fileFocusScope:   (*Model).handleFileScopeInput,
	fileFocusToggles: (*Model).handleFileToggleInput,
}

// scopeInputKeyActions dispatches scope-input keys to handlers.
var scopeInputKeyActions = map[string]func(*Model){
	"left":      (*Model).moveScopeCursorLeft,
	"right":     (*Model).moveScopeCursorRight,
	"backspace": (*Model).deleteScopeChar,
}

// toggleInputKeyActions dispatches toggle-input keys to handlers.
var toggleInputKeyActions = map[string]func(*Model){
	"left":  (*Model).moveToggleLeft,
	"right": (*Model).moveToggleRight,
	"h":     (*Model).moveToggleLeft,
	"l":     (*Model).moveToggleRight,
	" ":     (*Model).toggleFocusedBadge,
}

// statusColorMap maps git diff status codes to palette color accessors.
var statusColorMap = map[string]func(theme.Palette) lipgloss.Color{
	"M": func(p theme.Palette) lipgloss.Color { return p.Warning },
	"A": func(p theme.Palette) lipgloss.Color { return p.Success },
	"D": func(p theme.Palette) lipgloss.Color { return p.Error },
	"R": func(p theme.Palette) lipgloss.Color { return p.Teal },
	"C": func(p theme.Palette) lipgloss.Color { return p.Teal },
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// hasFileListDimensions returns true when the file list has positive size.
func (m *Model) hasFileListDimensions() bool {
	return m.fileListW > 0 && m.fileListH > 0
}

// FileListView renders the file list sidebar for the left panel.
func (m *Model) FileListView(cursorVisible bool) string {
	if !m.hasFileListDimensions() {
		return ""
	}

	p := m.theme.Palette
	w := m.fileListW
	header := renderFileListHeader(w, m.fileListFocused, p)

	if m.fileSearchActive {
		return m.renderSearchView(header, w, cursorVisible, p)
	}
	return m.renderNormalView(header, w, p)
}

// renderNormalView renders the file list with a search hint row.
func (m *Model) renderNormalView(header string, w int, p theme.Palette) string {
	searchHint := renderSearchHint(w, p)
	divider := renderDivider(w, p)

	entryH := max(m.fileListH-fileListHeaderHeight, 0)
	body := m.renderFileEntries(entryH, w, p)

	return header + "\n" + searchHint + "\n" + divider + "\n" + body
}

// renderSearchView renders the file list with full search chrome:
// search bar + divider + scope line + divider + entries + toolbar.
func (m *Model) renderSearchView(header string, w int, cursorVisible bool, p theme.Palette) string {
	searchBar := m.renderSearchBar(w, cursorVisible, p)
	divider := renderDivider(w, p)
	scopeLine := m.renderScopeLine(w, cursorVisible, p)

	// Body: entries above, toolbar at bottom.
	entryH := max(m.fileListH-fileSearchHeaderHeight, 0)

	// Reserve 1 line for toolbar at the bottom of the body.
	listH := max(entryH-1, 0)
	body := m.renderFileEntries(listH, w, p)
	toolbar := m.renderSearchToolbar(w, p)

	return header + "\n" + searchBar + "\n" + divider + "\n" +
		scopeLine + "\n" + divider + "\n" + body + "\n" + toolbar
}

// ---------------------------------------------------------------------------
// Search bar rendering
// ---------------------------------------------------------------------------

// isSearchBarCursorActive returns true when the search bar cursor should blink.
func (m *Model) isSearchBarCursorActive(cursorVisible bool) bool {
	return m.fileSearchFocus == fileFocusQuery && cursorVisible
}

// truncateQueryLeft returns the tail of queryRunes fitting within maxW.
func truncateQueryLeft(queryRunes []rune, maxW int) []rune {
	if len(queryRunes) > maxW {
		return queryRunes[len(queryRunes)-maxW:]
	}
	return queryRunes
}

// renderSearchBar renders "/ <query>" with a blinking reverse-video cursor.
func (m *Model) renderSearchBar(width int, cursorVisible bool, p theme.Palette) string {
	prefix := lipgloss.NewStyle().Foreground(p.Muted).Render("/ ")
	prefixW := lipgloss.Width(prefix)

	queryStyle := lipgloss.NewStyle().Foreground(p.Foreground)
	availableForQuery := max(width-prefixW-1, 0)
	queryRunes := truncateQueryLeft(m.fileSearchQuery, availableForQuery)

	cursor := renderSearchBarCursor(m.isSearchBarCursorActive(cursorVisible))
	line := prefix + queryStyle.Render(string(queryRunes)) + cursor
	return padLine(line, width)
}

// renderSearchBarCursor renders the cursor character for the search bar.
func renderSearchBarCursor(active bool) string {
	if active {
		return lipgloss.NewStyle().Reverse(true).Render(" ")
	}
	return " "
}

// ---------------------------------------------------------------------------
// Scope line rendering
// ---------------------------------------------------------------------------

// renderScopeLine renders " In: <path>  [re] ".
func (m *Model) renderScopeLine(width int, cursorVisible bool, p theme.Palette) string {
	badge := m.renderGlobBadge(p)
	badgeW := lipgloss.Width(badge)

	const leadingSpace = 1
	const gapBeforeBadge = 2
	const trailingSpace = 1
	scopeRegion := max(width-leadingSpace-gapBeforeBadge-badgeW-trailingSpace, 0)

	scopeStr := m.renderScopeInput(scopeRegion, cursorVisible, p)

	var b strings.Builder
	b.WriteByte(' ')
	b.WriteString(scopeStr)

	used := leadingSpace + lipgloss.Width(scopeStr)
	targetBadgeCol := width - badgeW - trailingSpace
	if pad := targetBadgeCol - used; pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	b.WriteString(badge)

	return padLine(b.String(), width)
}

// scopeVisibleWindow computes the visible slice of scope runes, keeping cursor in view.
func scopeVisibleWindow(n, textW, cur int) (start, end int) {
	start, end = 0, n
	if n > textW {
		start = clampInt(cur-textW/2, 0, n-textW)
		end = start + textW
	}
	return start, end
}

// scopeCursorChar returns the character under the cursor, or " " at end.
func scopeCursorChar(query []rune, cur, n int) string {
	if cur < n {
		return string(query[cur])
	}
	return " "
}

// scopeBeforeSlice returns the string before the cursor in the visible runes.
func scopeBeforeSlice(visible []rune, beforeLen int) string {
	if beforeLen > 0 && beforeLen <= len(visible) {
		return string(visible[:beforeLen])
	}
	return ""
}

// scopeAfterSlice returns the string after the cursor in the visible runes.
func scopeAfterSlice(visible []rune, afterStart int) string {
	if afterStart >= 0 && afterStart < len(visible) {
		return string(visible[afterStart:])
	}
	return ""
}

// renderScopeCursorChar renders the cursor character with active or inactive style.
func renderScopeCursorChar(ch string, active bool, p theme.Palette) string {
	if active {
		return lipgloss.NewStyle().Reverse(true).Render(ch)
	}
	return lipgloss.NewStyle().Foreground(p.Foreground).Render(ch)
}

// renderScopeInput renders "In: " prefix + scrolling text + cursor.
func (m *Model) renderScopeInput(availableWidth int, cursorVisible bool, p theme.Palette) string {
	prefix := lipgloss.NewStyle().Foreground(p.Muted).Render("In: ")
	prefixW := lipgloss.Width(prefix)
	textStyle := lipgloss.NewStyle().Foreground(p.Foreground)

	textW := max(availableWidth-prefixW-1, 0)
	n := len(m.fileScopeQuery)
	cur := clampInt(m.fileScopeCursor, 0, n)

	start, end := scopeVisibleWindow(n, textW, cur)
	visible := m.fileScopeQuery[start:end]

	cursorChar := scopeCursorChar(m.fileScopeQuery, cur, n)
	before := scopeBeforeSlice(visible, cur-start)
	after := scopeAfterSlice(visible, cur+1-start)
	active := m.fileSearchFocus == fileFocusScope && cursorVisible
	cursorRendered := renderScopeCursorChar(cursorChar, active, p)

	return prefix + textStyle.Render(before) + cursorRendered + textStyle.Render(after)
}

// renderGlobBadge renders the [re] badge on the scope line.
func (m *Model) renderGlobBadge(p theme.Palette) string {
	return m.renderFileBadge(fileToggleGlob, p)
}

// ---------------------------------------------------------------------------
// Toolbar rendering
// ---------------------------------------------------------------------------

// renderSearchToolbar renders " [Aa] [ab] [.*]" badges at the bottom.
func (m *Model) renderSearchToolbar(width int, p theme.Palette) string {
	var b strings.Builder
	b.WriteByte(' ')
	for i := range fileToolbarToggleCount {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(m.renderFileBadge(fileToggleIdx(i), p))
	}
	return padLine(b.String(), width)
}

// isBadgeFocused returns true when the badge at idx has keyboard focus.
func (m *Model) isBadgeFocused(idx fileToggleIdx) bool {
	return m.fileSearchFocus == fileFocusToggles && m.fileToggleCursor == idx
}

// renderFileBadge renders a single toggle badge with appropriate styling.
func (m *Model) renderFileBadge(idx fileToggleIdx, p theme.Palette) string {
	fg := p.Muted
	if m.fileToggleActive[idx] {
		fg = p.Primary
	}
	style := badgeStyle(m.isBadgeFocused(idx), fg, p.Selection)
	return style.Render("[" + fileBadgeLabels[idx] + "]")
}

// badgeStyle returns the lipgloss style for a badge given focus state.
func badgeStyle(focused bool, fg lipgloss.Color, selectionBg lipgloss.Color) lipgloss.Style {
	style := lipgloss.NewStyle().Foreground(fg)
	if focused {
		style = style.Background(selectionBg)
	}
	return style
}

// ---------------------------------------------------------------------------
// Common rendering helpers
// ---------------------------------------------------------------------------

// renderFileListHeader renders the header: Secondary when focused, Muted when not.
func renderFileListHeader(width int, focused bool, p theme.Palette) string {
	labelColor := p.Muted
	if focused {
		labelColor = p.Secondary
	}
	headerSt := lipgloss.NewStyle().Foreground(labelColor).Bold(true)
	lineSt := lipgloss.NewStyle().Foreground(p.Border)

	title := " Files "
	text := headerSt.Render(title)
	textW := lipgloss.Width(text)
	lineW := max(width-textW, 0)

	return text + lineSt.Render(strings.Repeat("─", lineW))
}

// renderSearchHint renders the muted "/ search" hint row.
func renderSearchHint(width int, p theme.Palette) string {
	st := lipgloss.NewStyle().Foreground(p.Muted)
	return padLine(st.Render(" / search"), width)
}

// renderDivider renders a horizontal rule.
func renderDivider(width int, p theme.Palette) string {
	return lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", width))
}

// visibleFileTotal returns the number of display entries.
func (m *Model) visibleFileTotal() int {
	if m.filteredIndices != nil {
		return len(m.filteredIndices)
	}
	return len(m.unionFiles)
}

// renderEmptyFileList renders an empty state padded to entryH lines.
func renderEmptyFileList(entryH, width int, p theme.Palette) string {
	empty := lipgloss.NewStyle().Foreground(p.Muted).Italic(true).Render(" No files")
	lines := make([]string, entryH)
	blank := strings.Repeat(" ", width)
	for i := range entryH {
		lines[i] = blank
	}
	if entryH > 0 {
		lines[0] = empty
	}
	return strings.Join(lines, "\n")
}

// resolveEntryIdx maps a visible index to a real unionFiles index.
func (m *Model) resolveEntryIdx(visibleIdx int) int {
	if m.filteredIndices != nil {
		return m.filteredIndices[visibleIdx]
	}
	return visibleIdx
}

// renderVisibleEntries renders file entries in the [start, end) range.
func (m *Model) renderVisibleEntries(start, end, width int, p theme.Palette) []string {
	openSet := m.openTabSet()
	entries := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		uf := m.unionFiles[m.resolveEntryIdx(i)]
		isCursor := i == m.selectedFile && m.fileListFocused
		isActive := uf.Path == m.selectedPath
		_, isOpen := openSet[uf.Path]
		entries = append(entries, renderUnionFileEntry(uf, isCursor, isActive, isOpen, width, p))
	}
	return entries
}

// openTabSet returns the set of file paths currently open as tabs.
func (m *Model) openTabSet() map[string]struct{} {
	set := make(map[string]struct{}, len(m.openTabs))
	for _, p := range m.openTabs {
		set[p] = struct{}{}
	}
	return set
}

// renderFileEntries renders the visible file entries, padded to entryH lines.
func (m *Model) renderFileEntries(entryH int, width int, p theme.Palette) string {
	total := m.visibleFileTotal()
	if total == 0 {
		return renderEmptyFileList(entryH, width, p)
	}

	start, end := visibleWindow(m.selectedFile, total, entryH)
	entries := m.renderVisibleEntries(start, end, width, p)

	blank := strings.Repeat(" ", width)
	for len(entries) < entryH {
		entries = append(entries, blank)
	}

	// Apply bounce shift for rubber-band visual at scroll boundaries.
	if m.fileListBounceOffset != 0 {
		entries = applyBounceShiftLines(entries, m.fileListBounceOffset, entryH, blank)
	}

	return strings.Join(entries, "\n")
}

// applyBounceShiftLines shifts rendered lines by offset for the bounce
// rubber-band effect. Positive offset shifts content up (bottom overscroll),
// negative offset shifts content down (top overscroll). blankLine fills
// exposed space so every line spans the full panel width.
func applyBounceShiftLines(lines []string, offset, viewHeight int, blankLine string) []string {
	if offset == 0 || viewHeight <= 0 {
		return lines
	}
	absOffset := offset
	if absOffset < 0 {
		absOffset = -absOffset
	}
	absOffset = min(absOffset, viewHeight)

	result := make([]string, viewHeight)
	if offset > 0 {
		// Bottom bounce: shift content up, pad bottom with blank lines.
		shift := min(absOffset, len(lines))
		copied := copy(result, lines[shift:])
		for i := copied; i < viewHeight; i++ {
			result[i] = blankLine
		}
	} else {
		// Top bounce: blank lines at top, content fills remaining space.
		for i := range absOffset {
			result[i] = blankLine
		}
		remaining := viewHeight - absOffset
		src := lines
		if len(src) > remaining {
			src = src[:remaining]
		}
		copy(result[absOffset:], src)
		for i := absOffset + len(src); i < viewHeight; i++ {
			result[i] = blankLine
		}
	}
	return result
}

func padLine(s string, width int) string {
	if vis := lipgloss.Width(s); vis < width {
		s += strings.Repeat(" ", width-vis)
	}
	return s
}

// clampInt clamps v to [lo, hi].
func clampInt(v, lo, hi int) int {
	return max(min(v, hi), lo)
}

// ---------------------------------------------------------------------------
// File entry rendering
// ---------------------------------------------------------------------------

// renderEntryStats builds the "+N -M " stats string for a file entry.
func renderEntryStats(uf UnionFileEntry, bg lipgloss.Color, hasBg bool, p theme.Palette) string {
	parts := make([]string, 0, 2)
	addSt := lipgloss.NewStyle().Foreground(p.Success)
	delSt := lipgloss.NewStyle().Foreground(p.Error)
	if hasBg {
		addSt = addSt.Background(bg)
		delSt = delSt.Background(bg)
	}
	if uf.Additions > 0 {
		parts = append(parts, addSt.Render(fmt.Sprintf("+%d", uf.Additions)))
	}
	if uf.Deletions > 0 {
		parts = append(parts, delSt.Render(fmt.Sprintf("-%d", uf.Deletions)))
	}
	spaceSt := lipgloss.NewStyle()
	if hasBg {
		spaceSt = spaceSt.Background(bg)
	}
	sep := spaceSt.Render(" ")
	return strings.Join(parts, sep) + sep
}

// renderUnionFileEntry renders a single file list entry with file-tree-style
// highlighting: cursor row gets a full-width Selection background; open-tab
// files get Accent foreground; the active file gets Accent foreground.
// [cursor] [status] icon name          +N -M
func renderUnionFileEntry(uf UnionFileEntry, isCursor, isActive, isOpen bool, width int, p theme.Palette) string {
	// Determine name and icon colors based on state priority.
	nameColor := p.Foreground
	iconEntry := devicons.IconForPath(filepath.Base(uf.Path))
	iconColor := lipgloss.Color(iconEntry.Color)

	// Open-tab files: Accent color for name + icon (like file tree active).
	if isOpen {
		nameColor = p.Accent
		iconColor = p.Accent
	}

	// The active viewing file uses the same Accent treatment.
	if isActive {
		nameColor = p.Accent
		iconColor = p.Accent
	}

	// Build the selection background for cursor row.
	hasBg := isCursor
	bg := p.Selection

	// Cursor prefix.
	prefixSt := lipgloss.NewStyle()
	if hasBg {
		prefixSt = prefixSt.Background(bg)
	}
	var prefix string
	if isCursor {
		prefix = prefixSt.Foreground(p.Primary).Render(" > ")
	} else {
		prefix = prefixSt.Render("   ")
	}

	// Status badge.
	badgeColor := fileStatusColor(uf.Status, p)
	badgeSt := lipgloss.NewStyle().Foreground(badgeColor).Bold(true)
	if hasBg {
		badgeSt = badgeSt.Background(bg)
	}
	badge := badgeSt.Render(uf.Status)

	// Separator.
	sepSt := lipgloss.NewStyle()
	if hasBg {
		sepSt = sepSt.Background(bg)
	}
	sep := sepSt.Render(" ")

	// Icon.
	iconSt := lipgloss.NewStyle().Foreground(iconColor)
	if hasBg {
		iconSt = iconSt.Background(bg)
	}
	iconStr := iconSt.Render(iconEntry.Icon)

	// Stats.
	statsStr := renderEntryStats(uf, bg, hasBg, p)
	statsW := lipgloss.Width(statsStr)

	// Compute available width for the file name.
	prefixW := lipgloss.Width(prefix) + lipgloss.Width(badge) + 1 + lipgloss.Width(iconStr) + 1
	nameW := max(width-prefixW-statsW, 1)

	// File name.
	name := filepath.Base(uf.Path)
	if lipgloss.Width(name) > nameW {
		name = name[:max(nameW-1, 0)] + "..."
	}
	nameSt := lipgloss.NewStyle().Foreground(nameColor)
	if hasBg {
		nameSt = nameSt.Background(bg)
	}

	// Assemble the row.
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(badge)
	b.WriteString(sep)
	b.WriteString(iconStr)
	b.WriteString(sep)
	b.WriteString(nameSt.Render(name))

	lineW := lipgloss.Width(b.String())
	gap := max(width-lineW-statsW, 0)
	padSt := lipgloss.NewStyle()
	if hasBg {
		padSt = padSt.Background(bg)
	}
	if gap > 0 {
		b.WriteString(padSt.Render(strings.Repeat(" ", gap)))
	}
	b.WriteString(statsStr)

	// Pad trailing space to full width with selection background.
	row := b.String()
	if vis := lipgloss.Width(row); vis < width {
		b.WriteString(padSt.Render(strings.Repeat(" ", width-vis)))
		row = b.String()
	}

	return row
}

// fileStatusColor returns the palette color for a git diff status code.
func fileStatusColor(status string, p theme.Palette) lipgloss.Color {
	if fn, ok := statusColorMap[status]; ok {
		return fn(p)
	}
	return p.Muted
}

// ---------------------------------------------------------------------------
// Input handling
// ---------------------------------------------------------------------------

// UpdateFileList handles keyboard input when the file list has focus.
func (m *Model) UpdateFileList(key string) {
	if m.fileSearchActive {
		m.updateFileSearchKey(key)
		return
	}
	m.dispatchFileListKey(key)
}

// dispatchFileListKey looks up and executes the handler for a file list key.
func (m *Model) dispatchFileListKey(key string) {
	if fn, ok := fileListKeyActions[key]; ok {
		fn(m)
	}
}

// moveFileDown moves the file cursor down by one.
func (m *Model) moveFileDown() {
	if m.selectedFile+1 < m.visibleFileCount() {
		m.selectedFile++
		m.selectVisibleFile(m.selectedFile)
	}
}

// moveFileUp moves the file cursor up by one.
func (m *Model) moveFileUp() {
	if m.selectedFile > 0 {
		m.selectedFile--
		m.selectVisibleFile(m.selectedFile)
	}
}

// moveFileToStart moves the file cursor to the first file.
func (m *Model) moveFileToStart() {
	m.selectedFile = 0
	m.selectVisibleFile(0)
}

// moveFileToEnd moves the file cursor to the last file.
func (m *Model) moveFileToEnd() {
	total := m.visibleFileCount()
	if total > 0 {
		m.selectedFile = total - 1
		m.selectVisibleFile(m.selectedFile)
	}
}

// openSelectedFileAsTab opens the currently selected file as a tab.
func (m *Model) openSelectedFileAsTab() {
	m.openVisibleFileAsTab(m.selectedFile)
}

// enterFileSearch activates search mode with clean state.
func (m *Model) enterFileSearch() {
	m.fileSearchActive = true
	m.fileSearchQuery = nil
	m.fileSearchFocus = fileFocusQuery
	m.fileToggleActive = [fileToggleCount]bool{}
	m.fileToggleCursor = fileToggleCase
	m.fileScopeQuery = nil
	m.fileScopeCursor = 0
	m.filteredIndices = nil
	m.selectedFile = 0
	m.fileListDirty = true
}

// exitFileSearch deactivates search and restores cursor to selectedPath.
func (m *Model) exitFileSearch() {
	m.fileSearchActive = false
	m.fileSearchQuery = nil
	m.fileScopeQuery = nil
	m.filteredIndices = nil
	for i, uf := range m.unionFiles {
		if uf.Path == m.selectedPath {
			m.selectedFile = i
			break
		}
	}
	m.fileListDirty = true
}

// updateFileSearchKey handles keyboard input in file search mode.
func (m *Model) updateFileSearchKey(key string) {
	if fn, ok := fileSearchKeyActions[key]; ok {
		fn(m)
		return
	}
	m.dispatchToFileSearchZone(key)
}

// handleSearchEnter handles the enter key in search mode.
func (m *Model) handleSearchEnter() {
	if m.fileSearchFocus == fileFocusToggles {
		m.toggleFileBadge(m.fileToggleCursor)
		return
	}
	m.openVisibleFileAsTab(m.selectedFile)
	m.exitFileSearch()
}

// advanceSearchTab wraps advanceFileSearchFocus and marks dirty.
func (m *Model) advanceSearchTab() {
	m.advanceFileSearchFocus()
	m.fileListDirty = true
}

// retreatSearchTab wraps retreatFileSearchFocus and marks dirty.
func (m *Model) retreatSearchTab() {
	m.retreatFileSearchFocus()
	m.fileListDirty = true
}

// dispatchToFileSearchZone routes input to the focused search element.
func (m *Model) dispatchToFileSearchZone(key string) {
	if fn, ok := fileSearchZoneDispatch[m.fileSearchFocus]; ok {
		fn(m, key)
	}
}

// decodeSingleRune decodes a single valid rune from key. Returns (rune, true)
// on success, or (0, false) when key is not a single rune.
func decodeSingleRune(key string) (rune, bool) {
	r, size := utf8.DecodeRuneInString(key)
	return r, size == len(key) && size > 0
}

// deleteQueryChar removes the last query character, or exits search if empty.
func (m *Model) deleteQueryChar() {
	if len(m.fileSearchQuery) > 0 {
		m.fileSearchQuery = m.fileSearchQuery[:len(m.fileSearchQuery)-1]
		m.refilterFiles()
		return
	}
	m.exitFileSearch()
}

// insertQueryChar appends a rune to the query and refilters.
func (m *Model) insertQueryChar(r rune) {
	m.fileSearchQuery = append(m.fileSearchQuery, r)
	m.refilterFiles()
}

// handleFileQueryInput handles keys when the query input has focus.
func (m *Model) handleFileQueryInput(key string) {
	if key == "backspace" {
		m.deleteQueryChar()
		return
	}
	if r, ok := decodeSingleRune(key); ok {
		m.insertQueryChar(r)
	}
}

// moveScopeCursorLeft moves the scope cursor one position left.
func (m *Model) moveScopeCursorLeft() {
	m.fileScopeCursor = max(m.fileScopeCursor-1, 0)
	m.fileListDirty = true
}

// moveScopeCursorRight moves the scope cursor one position right.
func (m *Model) moveScopeCursorRight() {
	m.fileScopeCursor = min(m.fileScopeCursor+1, len(m.fileScopeQuery))
	m.fileListDirty = true
}

// deleteScopeChar removes the character before the scope cursor.
func (m *Model) deleteScopeChar() {
	if m.fileScopeCursor <= 0 || m.fileScopeCursor > len(m.fileScopeQuery) {
		return
	}
	m.fileScopeQuery = append(
		m.fileScopeQuery[:m.fileScopeCursor-1],
		m.fileScopeQuery[m.fileScopeCursor:]...,
	)
	m.fileScopeCursor--
	m.refilterFiles()
}

// insertScopeChar inserts a rune at the scope cursor position.
func (m *Model) insertScopeChar(r rune) {
	tail := append([]rune{r}, m.fileScopeQuery[m.fileScopeCursor:]...)
	m.fileScopeQuery = append(m.fileScopeQuery[:m.fileScopeCursor], tail...)
	m.fileScopeCursor++
	m.refilterFiles()
}

// handleFileScopeInput handles keys when the scope input has focus.
func (m *Model) handleFileScopeInput(key string) {
	if fn, ok := scopeInputKeyActions[key]; ok {
		fn(m)
		return
	}
	if r, ok := decodeSingleRune(key); ok {
		m.insertScopeChar(r)
	}
}

// moveToggleLeft moves the toggle cursor one position left.
func (m *Model) moveToggleLeft() {
	m.moveFileToggleCursor(-1)
	m.fileListDirty = true
}

// moveToggleRight moves the toggle cursor one position right.
func (m *Model) moveToggleRight() {
	m.moveFileToggleCursor(1)
	m.fileListDirty = true
}

// toggleFocusedBadge flips the currently focused toggle badge.
func (m *Model) toggleFocusedBadge() {
	m.toggleFileBadge(m.fileToggleCursor)
}

// handleFileToggleInput handles keys when the toggle badges have focus.
func (m *Model) handleFileToggleInput(key string) {
	if fn, ok := toggleInputKeyActions[key]; ok {
		fn(m)
		return
	}
	// Typing while on toggles jumps back to query input.
	if r, ok := decodeSingleRune(key); ok {
		m.fileSearchFocus = fileFocusQuery
		m.insertQueryChar(r)
	}
}

// ---------------------------------------------------------------------------
// Focus cycling
// ---------------------------------------------------------------------------

// advanceToggle advances within the toggle group. Returns true if handled.
func (m *Model) advanceToggle() bool {
	if m.fileSearchFocus != fileFocusToggles {
		return false
	}
	pos := fileToggleTabPos(m.fileToggleCursor)
	if pos >= fileToggleCount-1 {
		return false
	}
	m.fileToggleCursor = fileToggleTabOrder[pos+1]
	return true
}

// retreatToggle retreats within the toggle group. Returns true if handled.
func (m *Model) retreatToggle() bool {
	if m.fileSearchFocus != fileFocusToggles {
		return false
	}
	pos := fileToggleTabPos(m.fileToggleCursor)
	if pos <= 0 {
		return false
	}
	m.fileToggleCursor = fileToggleTabOrder[pos-1]
	return true
}

// setToggleCursorForFocus sets the toggle cursor to the appropriate end
// when focus enters the toggles zone.
func (m *Model) setToggleCursorForFocus(toFirst bool) {
	if m.fileSearchFocus != fileFocusToggles {
		return
	}
	idx := fileToggleCount - 1
	if toFirst {
		idx = 0
	}
	m.fileToggleCursor = fileToggleTabOrder[idx]
}

// advanceFileSearchFocus moves Tab focus forward: query → scope → toggles.
// Within toggles, Tab cycles through individual badges before advancing.
func (m *Model) advanceFileSearchFocus() {
	if m.advanceToggle() {
		return
	}
	m.fileSearchFocus = m.nextFileSearchFocus(1)
	m.setToggleCursorForFocus(true)
}

// retreatFileSearchFocus moves Tab focus backward.
func (m *Model) retreatFileSearchFocus() {
	if m.retreatToggle() {
		return
	}
	m.fileSearchFocus = m.nextFileSearchFocus(-1)
	m.setToggleCursorForFocus(false)
}

// nextFileSearchFocus returns the next focus zone in the cycling order.
func (m *Model) nextFileSearchFocus(delta int) fileSearchFocusZone {
	for i, f := range fileFocusOrder {
		if f == m.fileSearchFocus {
			next := (i + delta + len(fileFocusOrder)) % len(fileFocusOrder)
			return fileFocusOrder[next]
		}
	}
	return fileFocusQuery
}

// fileToggleTabPos returns the position of idx in fileToggleTabOrder.
func fileToggleTabPos(idx fileToggleIdx) int {
	for i, t := range fileToggleTabOrder {
		if t == idx {
			return i
		}
	}
	return 0
}

// moveFileToggleCursor shifts the toggle cursor by delta within bounds.
func (m *Model) moveFileToggleCursor(delta int) {
	pos := fileToggleTabPos(m.fileToggleCursor)
	next := clampInt(pos+delta, 0, fileToggleCount-1)
	m.fileToggleCursor = fileToggleTabOrder[next]
}

// toggleFileBadge flips a toggle and refilters.
func (m *Model) toggleFileBadge(idx fileToggleIdx) {
	m.fileToggleActive[idx] = !m.fileToggleActive[idx]
	m.refilterFiles()
}

// ---------------------------------------------------------------------------
// Filtering
// ---------------------------------------------------------------------------

// buildQueryMatcher creates a query-matching function from current state.
// Returns nil when there is no query.
func (m *Model) buildQueryMatcher() func(string) bool {
	query := string(m.fileSearchQuery)
	if query == "" {
		return nil
	}
	return buildMatcher(query, m.fileToggleActive[fileToggleCase], m.fileToggleActive[fileToggleWord], m.fileToggleActive[fileToggleRegex])
}

// buildScopeMatcher creates a scope-matching function from current state.
// Returns nil when there is no scope.
func (m *Model) buildScopeMatcher() func(string) bool {
	scope := strings.TrimSpace(string(m.fileScopeQuery))
	if scope == "" {
		return nil
	}
	if m.fileToggleActive[fileToggleGlob] {
		return buildRegexMatcher(scope, m.fileToggleActive[fileToggleCase])
	}
	return buildPrefixMatcher(scope, m.fileToggleActive[fileToggleCase])
}

// filterUnionFiles returns the indices of unionFiles matching both matchers.
func (m *Model) filterUnionFiles(queryMatcher, scopeMatcher func(string) bool) []int {
	var result []int
	for i, uf := range m.unionFiles {
		if !matchesBoth(uf.Path, queryMatcher, scopeMatcher) {
			continue
		}
		result = append(result, i)
	}
	return result
}

// matchesOrNil returns true when matcher is nil or matches the path.
func matchesOrNil(path string, matcher func(string) bool) bool {
	if matcher == nil {
		return true
	}
	return matcher(path)
}

// matchesBoth returns true when path passes both matchers (nil = pass).
func matchesBoth(path string, queryMatcher, scopeMatcher func(string) bool) bool {
	return matchesOrNil(path, scopeMatcher) && matchesOrNil(path, queryMatcher)
}

// refilterFiles rebuilds filteredIndices using query, toggles, and scope.
func (m *Model) refilterFiles() {
	queryMatcher := m.buildQueryMatcher()
	scopeMatcher := m.buildScopeMatcher()

	if queryMatcher == nil && scopeMatcher == nil {
		m.clearFilter()
		return
	}

	m.filteredIndices = m.filterUnionFiles(queryMatcher, scopeMatcher)
	m.selectedFile = 0
	m.postFilterSelect()
}

// clearFilter resets filter state to show all files.
func (m *Model) clearFilter() {
	m.filteredIndices = nil
	m.selectedFile = 0
	m.fileListDirty = true
}

// postFilterSelect selects the first filtered file if available and marks dirty.
func (m *Model) postFilterSelect() {
	if len(m.filteredIndices) > 0 {
		m.selectVisibleFile(0)
	}
	m.fileListDirty = true
}

// buildMatcher creates a path-matching function from query and toggle state.
func buildMatcher(query string, caseSensitive, wholeWord, useRegex bool) func(string) bool {
	if useRegex {
		return buildRegexMatcher(query, caseSensitive)
	}
	if wholeWord {
		return buildWholeWordMatcher(query, caseSensitive)
	}
	return buildSubstringMatcher(query, caseSensitive)
}

// buildWholeWordMatcher creates a whole-word matching function.
func buildWholeWordMatcher(query string, caseSensitive bool) func(string) bool {
	flags := ""
	if !caseSensitive {
		flags = "(?i)"
	}
	pattern := flags + `\b` + regexp.QuoteMeta(query) + `\b`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return func(string) bool { return false }
	}
	return re.MatchString
}

// buildSubstringMatcher creates a case-aware substring matching function.
func buildSubstringMatcher(query string, caseSensitive bool) func(string) bool {
	if !caseSensitive {
		query = strings.ToLower(query)
	}
	return func(path string) bool {
		target := path
		if !caseSensitive {
			target = strings.ToLower(path)
		}
		return strings.Contains(target, query)
	}
}

// buildRegexMatcher compiles a regex pattern for matching.
func buildRegexMatcher(pattern string, caseSensitive bool) func(string) bool {
	flags := ""
	if !caseSensitive {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return func(string) bool { return false }
	}
	return re.MatchString
}

// buildPrefixMatcher creates a directory prefix matcher.
func buildPrefixMatcher(scope string, caseSensitive bool) func(string) bool {
	if !caseSensitive {
		scope = strings.ToLower(scope)
	}
	return func(path string) bool {
		target := path
		if !caseSensitive {
			target = strings.ToLower(path)
		}
		return strings.Contains(target, scope)
	}
}

// ---------------------------------------------------------------------------
// Navigation helpers
// ---------------------------------------------------------------------------

// inBounds returns true when idx is a valid index for the slice.
func inBounds(idx, length int) bool {
	return idx >= 0 && idx < length
}

// resolveVisibleIdx resolves a visible index to a real unionFiles index.
// Returns the real index and true on success, or (0, false) on out-of-range.
func (m *Model) resolveVisibleIdx(idx int) (int, bool) {
	if m.filteredIndices == nil {
		return idx, true
	}
	if !inBounds(idx, len(m.filteredIndices)) {
		return 0, false
	}
	return m.filteredIndices[idx], true
}

// selectVisibleFile selects the file at the given visible index and updates panes.
func (m *Model) selectVisibleFile(idx int) {
	if realIdx, ok := m.resolveVisibleIdx(idx); ok {
		m.SelectFile(realIdx)
	}
}

// openVisibleFileAsTab opens the file at the given visible index as a tab.
func (m *Model) openVisibleFileAsTab(idx int) {
	if realIdx, ok := m.resolveVisibleIdx(idx); ok {
		m.OpenFileAsTab(realIdx)
	}
}

// visibleFileCount returns the number of files currently shown.
func (m *Model) visibleFileCount() int {
	if m.filteredIndices != nil {
		return len(m.filteredIndices)
	}
	return len(m.unionFiles)
}

// visibleWindow calculates the start and end indices for a scrolling window
// centered on the selected item.
func visibleWindow(selected, total, height int) (start, end int) {
	if total <= height {
		return 0, total
	}
	half := height / 2
	start = selected - half
	start = max(start, 0)
	end = start + height
	if end > total {
		end = total
		start = max(end-height, 0)
	}
	return start, end
}
