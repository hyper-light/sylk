package mergediff

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/adalundhe/sylk/ui/diffview"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
	devicons "github.com/epilande/go-devicons"
)

// fileListHeaderHeight is the header lines when search is inactive.
const fileListHeaderHeight = 3

// fileSearchHeaderHeight is the header lines when search is active.
const fileSearchHeaderHeight = 4

// ---------------------------------------------------------------------------
// File list rendering
// ---------------------------------------------------------------------------

// FileListView renders the file list sidebar.
func (m *Model) FileListView(cursorVisible bool) string {
	if m.fileListW <= 0 || m.fileListH <= 0 {
		return ""
	}

	p := m.theme.Palette
	w := m.fileListW
	header := renderMergeFileListHeader(w, m.fileListFocused, p)

	if m.fileSearchActive {
		return m.renderSearchFileView(header, w, cursorVisible, p)
	}
	return m.renderNormalFileView(header, w, p)
}

// renderMergeFileListHeader renders the header line.
func renderMergeFileListHeader(width int, focused bool, p theme.Palette) string {
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

// renderNormalFileView renders the file list with search hint.
func (m *Model) renderNormalFileView(header string, w int, p theme.Palette) string {
	searchHint := renderSearchHint(w, p)
	divider := renderDivider(w, p)

	entryH := max(m.fileListH-fileListHeaderHeight, 0)
	body := m.renderFileEntries(entryH, w, p)

	return header + "\n" + searchHint + "\n" + divider + "\n" + body
}

// renderSearchFileView renders the file list with search bar.
func (m *Model) renderSearchFileView(header string, w int, cursorVisible bool, p theme.Palette) string {
	searchBar := m.renderFileSearchBar(w, cursorVisible, p)
	divider := renderDivider(w, p)

	entryH := max(m.fileListH-fileSearchHeaderHeight, 0)
	body := m.renderFileEntries(entryH, w, p)

	return header + "\n" + searchBar + "\n" + divider + "\n" + body
}

// renderSearchHint renders the muted "/ search" hint.
func renderSearchHint(width int, p theme.Palette) string {
	st := lipgloss.NewStyle().Foreground(p.Muted)
	return padLine(st.Render(" / search"), width)
}

// renderDivider renders a horizontal rule.
func renderDivider(width int, p theme.Palette) string {
	return lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", width))
}

// renderFileSearchBar renders "/ <query>".
func (m *Model) renderFileSearchBar(width int, cursorVisible bool, p theme.Palette) string {
	prefix := lipgloss.NewStyle().Foreground(p.Muted).Render("/ ")
	prefixW := lipgloss.Width(prefix)

	queryStyle := lipgloss.NewStyle().Foreground(p.Foreground)
	availableForQuery := max(width-prefixW-1, 0)
	queryRunes := m.fileSearchQuery
	if len(queryRunes) > availableForQuery {
		queryRunes = queryRunes[len(queryRunes)-availableForQuery:]
	}

	cursor := " "
	if cursorVisible {
		cursor = lipgloss.NewStyle().Reverse(true).Render(" ")
	}
	line := prefix + queryStyle.Render(string(queryRunes)) + cursor
	return padLine(line, width)
}

// padLine pads a string to width.
func padLine(s string, width int) string {
	if vis := lipgloss.Width(s); vis < width {
		s += strings.Repeat(" ", width-vis)
	}
	return s
}

// renderFileEntries renders the visible file entries.
func (m *Model) renderFileEntries(entryH int, width int, p theme.Palette) string {
	total := m.visibleFileCount()
	if total == 0 {
		return renderEmptyFileList(entryH, width, p)
	}

	start, end := visibleWindow(m.selectedFile, total, entryH)
	entries := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		realIdx := m.resolveEntryIdx(i)
		uf := m.unionFiles[realIdx]
		isCursor := i == m.selectedFile && m.fileListFocused
		isHighlighted := uf.Path == m.highlightPath
		entries = append(entries, renderMergeFileEntry(uf, isCursor, isHighlighted, width, p))
	}

	blank := strings.Repeat(" ", width)
	for len(entries) < entryH {
		entries = append(entries, blank)
	}

	if m.fileListBounceOffset != 0 {
		entries = applyBounceShiftLines(entries, m.fileListBounceOffset, entryH, blank)
	}

	return strings.Join(entries, "\n")
}

// renderEmptyFileList renders an empty state.
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

// applyBounceShiftLines shifts lines for bounce effect.
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
		shift := min(absOffset, len(lines))
		copied := copy(result, lines[shift:])
		for i := copied; i < viewHeight; i++ {
			result[i] = blankLine
		}
	} else {
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

// statusColorMap maps git diff status codes to palette colors.
var statusColorMap = map[string]func(theme.Palette) lipgloss.Color{
	"M": func(p theme.Palette) lipgloss.Color { return p.Warning },
	"A": func(p theme.Palette) lipgloss.Color { return p.Success },
	"D": func(p theme.Palette) lipgloss.Color { return p.Error },
	"R": func(p theme.Palette) lipgloss.Color { return p.Teal },
	"C": func(p theme.Palette) lipgloss.Color { return p.Teal },
}

// fileStatusColor returns the palette color for a status code.
func fileStatusColor(status string, p theme.Palette) lipgloss.Color {
	if fn, ok := statusColorMap[status]; ok {
		return fn(p)
	}
	return p.Muted
}

// renderMergeFileEntry renders a single file list entry.
func renderMergeFileEntry(uf diffview.UnionFileEntry, isCursor, isHighlighted bool, width int, p theme.Palette) string {
	nameColor := p.Foreground
	iconEntry := devicons.IconForPath(filepath.Base(uf.Path))
	iconColor := lipgloss.Color(iconEntry.Color)

	if isHighlighted {
		nameColor = p.Accent
		iconColor = p.Accent
	}

	hasBg := isCursor
	bg := p.Selection

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

	badgeColor := fileStatusColor(uf.Status, p)
	badgeSt := lipgloss.NewStyle().Foreground(badgeColor).Bold(true)
	if hasBg {
		badgeSt = badgeSt.Background(bg)
	}
	badge := badgeSt.Render(uf.Status)

	sepSt := lipgloss.NewStyle()
	if hasBg {
		sepSt = sepSt.Background(bg)
	}
	sep := sepSt.Render(" ")

	iconSt := lipgloss.NewStyle().Foreground(iconColor)
	if hasBg {
		iconSt = iconSt.Background(bg)
	}
	iconStr := iconSt.Render(iconEntry.Icon)

	// Stats.
	addSt := lipgloss.NewStyle().Foreground(p.Success)
	delSt := lipgloss.NewStyle().Foreground(p.Error)
	if hasBg {
		addSt = addSt.Background(bg)
		delSt = delSt.Background(bg)
	}
	var statParts []string
	if uf.Additions > 0 {
		statParts = append(statParts, addSt.Render(fmt.Sprintf("+%d", uf.Additions)))
	}
	if uf.Deletions > 0 {
		statParts = append(statParts, delSt.Render(fmt.Sprintf("-%d", uf.Deletions)))
	}
	statsSep := sepSt.Render(" ")
	statsStr := strings.Join(statParts, statsSep) + statsSep

	prefixW := lipgloss.Width(prefix) + lipgloss.Width(badge) + 1 + lipgloss.Width(iconStr) + 1
	statsW := lipgloss.Width(statsStr)
	nameW := max(width-prefixW-statsW, 1)

	name := filepath.Base(uf.Path)
	if lipgloss.Width(name) > nameW {
		name = name[:max(nameW-1, 0)] + "..."
	}
	nameSt := lipgloss.NewStyle().Foreground(nameColor)
	if hasBg {
		nameSt = nameSt.Background(bg)
	}

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

	row := b.String()
	if vis := lipgloss.Width(row); vis < width {
		b.WriteString(padSt.Render(strings.Repeat(" ", width-vis)))
		row = b.String()
	}

	return row
}

// ---------------------------------------------------------------------------
// File list input handling
// ---------------------------------------------------------------------------

// UpdateFileList handles keyboard input for the file list.
func (m *Model) UpdateFileList(key string) {
	if m.fileSearchActive {
		m.updateFileSearchKey(key)
		return
	}
	m.dispatchFileListKey(key)
}

// dispatchFileListKey handles normal-mode keys.
func (m *Model) dispatchFileListKey(key string) {
	if fn, ok := mergeFileListKeyActions[key]; ok {
		fn(m)
	}
}

var mergeFileListKeyActions = map[string]func(*Model){
	"j":     (*Model).moveFileDown,
	"down":  (*Model).moveFileDown,
	"k":     (*Model).moveFileUp,
	"up":    (*Model).moveFileUp,
	"g":     (*Model).moveFileToStart,
	"home":  (*Model).moveFileToStart,
	"G":     (*Model).moveFileToEnd,
	"end":   (*Model).moveFileToEnd,
	"enter": (*Model).scrollToSelectedFile,
	"/":     (*Model).enterFileSearch,
}

// moveFileDown moves the cursor down.
func (m *Model) moveFileDown() {
	total := m.visibleFileCount()
	if m.selectedFile+1 < total {
		m.selectedFile++
		m.updateHighlightFromCursor()
	}
}

// moveFileUp moves the cursor up.
func (m *Model) moveFileUp() {
	if m.selectedFile > 0 {
		m.selectedFile--
		m.updateHighlightFromCursor()
	}
}

// moveFileToStart jumps to the first file.
func (m *Model) moveFileToStart() {
	m.selectedFile = 0
	m.updateHighlightFromCursor()
}

// moveFileToEnd jumps to the last file.
func (m *Model) moveFileToEnd() {
	total := m.visibleFileCount()
	if total > 0 {
		m.selectedFile = total - 1
		m.updateHighlightFromCursor()
	}
}

// scrollToSelectedFile scrolls the pane to the selected file.
func (m *Model) scrollToSelectedFile() {
	realIdx := m.resolveEntryIdx(m.selectedFile)
	mp := m.focusedMergePane()
	if mp == nil {
		return
	}
	// Find the file index within the pane's filePaths.
	path := m.unionFiles[realIdx].Path
	for i, p := range mp.filePaths {
		if p == path {
			m.scrollToFile(i)
			return
		}
	}
}

// updateHighlightFromCursor syncs highlightPath from the cursor and scrolls.
func (m *Model) updateHighlightFromCursor() {
	realIdx := m.resolveEntryIdx(m.selectedFile)
	if realIdx >= 0 && realIdx < len(m.unionFiles) {
		m.highlightPath = m.unionFiles[realIdx].Path
		// Auto-scroll pane to the highlighted file.
		mp := m.focusedMergePane()
		if mp != nil {
			for i, p := range mp.filePaths {
				if p == m.highlightPath {
					mp.ScrollToFile(i)
					break
				}
			}
		}
	}
	m.fileListDirty = true
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// File search
// ---------------------------------------------------------------------------

// ToggleFileSearch toggles the file search.
func (m *Model) ToggleFileSearch() {
	if m.fileSearchActive {
		m.exitFileSearch()
	} else {
		m.enterFileSearch()
	}
}

// enterFileSearch activates search mode.
func (m *Model) enterFileSearch() {
	m.fileSearchActive = true
	m.fileSearchQuery = nil
	m.filteredIndices = nil
	m.selectedFile = 0
	m.fileListDirty = true
}

// exitFileSearch deactivates search.
func (m *Model) exitFileSearch() {
	m.fileSearchActive = false
	m.fileSearchQuery = nil
	m.filteredIndices = nil
	m.syncSelectedFileFromPath()
	m.fileListDirty = true
}

// updateFileSearchKey handles keys in search mode.
func (m *Model) updateFileSearchKey(key string) {
	switch key {
	case "esc":
		m.exitFileSearch()
	case "enter":
		m.scrollToSelectedFile()
		m.exitFileSearch()
	case "up":
		m.moveFileUp()
	case "down":
		m.moveFileDown()
	case "backspace":
		m.deleteSearchChar()
	default:
		if r, size := utf8.DecodeRuneInString(key); size == len(key) && size > 0 {
			m.insertSearchChar(r)
		}
	}
}

// deleteSearchChar removes the last query character.
func (m *Model) deleteSearchChar() {
	if len(m.fileSearchQuery) > 0 {
		m.fileSearchQuery = m.fileSearchQuery[:len(m.fileSearchQuery)-1]
		m.refilterFiles()
		return
	}
	m.exitFileSearch()
}

// insertSearchChar appends a rune to the query.
func (m *Model) insertSearchChar(r rune) {
	m.fileSearchQuery = append(m.fileSearchQuery, r)
	m.refilterFiles()
}

// refilterFiles rebuilds filteredIndices.
func (m *Model) refilterFiles() {
	query := strings.ToLower(string(m.fileSearchQuery))
	if query == "" {
		m.filteredIndices = nil
		m.selectedFile = 0
		m.fileListDirty = true
		return
	}

	var result []int
	for i, uf := range m.unionFiles {
		if strings.Contains(strings.ToLower(uf.Path), query) {
			result = append(result, i)
		}
	}
	m.filteredIndices = result
	m.selectedFile = 0
	m.fileListDirty = true
}

// ---------------------------------------------------------------------------
// Navigation helpers
// ---------------------------------------------------------------------------

// visibleFileCount returns the number of visible files.
func (m *Model) visibleFileCount() int {
	if m.filteredIndices != nil {
		return len(m.filteredIndices)
	}
	return len(m.unionFiles)
}

// resolveEntryIdx maps a visible index to a real index.
func (m *Model) resolveEntryIdx(visibleIdx int) int {
	if m.filteredIndices != nil {
		if visibleIdx >= 0 && visibleIdx < len(m.filteredIndices) {
			return m.filteredIndices[visibleIdx]
		}
		return 0
	}
	return visibleIdx
}

// visibleWindow calculates start and end for a scrolling window.
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

// ClickFileList handles a mouse click in the file list.
func (m *Model) ClickFileList(localY int) {
	headerH := fileListHeaderHeight
	if m.fileSearchActive {
		headerH = fileSearchHeaderHeight
	}
	if localY < headerH {
		if localY == 1 && !m.fileSearchActive {
			m.enterFileSearch()
		}
		return
	}
	entryH := max(m.fileListH-headerH, 0)
	entryIdx := localY - headerH
	total := m.visibleFileCount()
	start, _ := visibleWindow(m.selectedFile, total, entryH)
	idx := start + entryIdx
	if idx < 0 || idx >= total {
		return
	}
	m.selectedFile = idx
	m.updateHighlightFromCursor()
}
