package conflictview

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fileListHeaderHeight is the number of header lines above entries.
const fileListHeaderHeight = 3

// conflictTypeLabels maps ConflictType to display labels.
var conflictTypeLabels = [...]string{
	TypeContent:      "Content",
	TypeDeleteModify: "D/Modify",
	TypeBothAdded:    "BothAdd",
	TypeBinary:       "Binary",
}

// resolutionColorFns maps ConflictResolution to palette color accessors.
var resolutionColorFns = [...]func(theme.Palette) lipgloss.Color{
	ResUnresolved: func(p theme.Palette) lipgloss.Color { return p.Muted },
	ResOurs:       func(p theme.Palette) lipgloss.Color { return p.Primary },
	ResTheirs:     func(p theme.Palette) lipgloss.Color { return p.Teal },
	ResBoth:       func(p theme.Palette) lipgloss.Color { return p.Success },
}

// FileListView renders the file list sidebar.
func (m *Model) FileListView(cursorVisible bool) string {
	if m.fileListW <= 0 || m.fileListH <= 0 {
		return ""
	}

	p := m.theme.Palette
	w := m.fileListW
	header := renderConflictFileListHeader(w, m.fileListFocused, m.resolvedCount(), len(m.data.Entries), p)
	divider := renderDivider(w, p)

	entryH := max(m.fileListH-fileListHeaderHeight, 0)
	body := m.renderFileEntries(entryH, w, p)

	return header + "\n" + divider + "\n" + body
}

// resolvedCount returns the number of resolved entries.
func (m *Model) resolvedCount() int {
	n := 0
	for i := range m.data.Entries {
		if m.data.Entries[i].Resolution != ResUnresolved {
			n++
		}
	}
	return n
}

// renderConflictFileListHeader renders the header line with resolution progress.
func renderConflictFileListHeader(width int, focused bool, resolved, total int, p theme.Palette) string {
	labelColor := p.Muted
	if focused {
		labelColor = p.Secondary
	}
	headerSt := lipgloss.NewStyle().Foreground(labelColor).Bold(true)
	lineSt := lipgloss.NewStyle().Foreground(p.Border)

	title := fmt.Sprintf(" Conflicts (%d/%d resolved) ", resolved, total)
	text := headerSt.Render(title)
	textW := lipgloss.Width(text)
	lineW := max(width-textW, 0)

	hintSt := lipgloss.NewStyle().Foreground(p.Muted)
	hint := hintSt.Render(" o/t/b to resolve")
	hintLine := padLine(hint, width)

	return text + lineSt.Render(strings.Repeat("─", lineW)) + "\n" + hintLine
}

// renderDivider renders a horizontal rule.
func renderDivider(width int, p theme.Palette) string {
	return lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", width))
}

// entryLineCount returns the display height of file entry fileIdx.
// Each entry has 1 header line plus one line per hunk (minimum 1).
func (m *Model) entryLineCount(fileIdx int) int {
	n := len(m.data.Entries[fileIdx].Hunks)
	return 1 + max(n, 1)
}

// renderFileEntries renders the visible file entries (variable height each).
func (m *Model) renderFileEntries(entryH int, width int, p theme.Palette) string {
	total := len(m.data.Entries)
	if total == 0 {
		return renderEmptyFileList(entryH, width, p)
	}

	start, end := m.visibleFileWindow(entryH)
	lines := make([]string, 0, entryH)
	for i := start; i < end; i++ {
		entry := &m.data.Entries[i]
		isCursor := i == m.selectedFile
		lines = append(lines, m.renderConflictFileEntry(entry, isCursor, width, p))
		lines = append(lines, m.renderHunkLines(i, isCursor, width, p)...)
	}

	blank := strings.Repeat(" ", width)
	for len(lines) < entryH {
		lines = append(lines, blank)
	}
	if len(lines) > entryH {
		lines = lines[:entryH]
	}

	if m.fileListBounceOffset != 0 {
		lines = applyBounceShiftLines(lines, m.fileListBounceOffset, entryH, blank)
	}

	return strings.Join(lines, "\n")
}

// visibleFileWindow returns the range [start, end) of entries that fit
// in entryH display lines, anchored on the selected file.
func (m *Model) visibleFileWindow(entryH int) (int, int) {
	total := len(m.data.Entries)
	if total == 0 {
		return 0, 0
	}
	start := m.selectedFile
	end := m.selectedFile + 1
	used := m.entryLineCount(m.selectedFile)
	start, used = m.expandWindowUp(start, used, entryH)
	end, used = m.expandWindowDown(end, total, used, entryH)
	start, _ = m.expandWindowUp(start, used, entryH)
	return start, end
}

// expandWindowUp expands the visible window upward while entries fit.
func (m *Model) expandWindowUp(start, used, limit int) (int, int) {
	for start > 0 {
		h := m.entryLineCount(start - 1)
		if used+h > limit {
			break
		}
		start--
		used += h
	}
	return start, used
}

// expandWindowDown expands the visible window downward while entries fit.
func (m *Model) expandWindowDown(end, total, used, limit int) (int, int) {
	for end < total {
		h := m.entryLineCount(end)
		if used+h > limit {
			break
		}
		end++
		used += h
	}
	return end, used
}

// renderEmptyFileList renders an empty state.
func renderEmptyFileList(entryH, width int, p theme.Palette) string {
	empty := lipgloss.NewStyle().Foreground(p.Muted).Italic(true).Render(" No conflicts")
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

// renderConflictFileEntry renders a single file list entry header line.
// Only this row receives the selection background when the file is selected.
// Format: [cursor] [type-badge] [filename] [resolution-badge]
func (m *Model) renderConflictFileEntry(entry *ConflictFileEntry, isCursor bool, width int, p theme.Palette) string {
	hasBg := isCursor
	bg := p.Selection

	// Cursor prefix.
	prefixSt := lipgloss.NewStyle()
	if hasBg {
		prefixSt = prefixSt.Background(bg)
	}
	var prefix string
	if isCursor {
		prefix = prefixSt.Foreground(p.Primary).Render("▸ ")
	} else {
		prefix = prefixSt.Render("  ")
	}

	// Type badge.
	typeLabel := "Content"
	if int(entry.Type) >= 0 && int(entry.Type) < len(conflictTypeLabels) {
		typeLabel = conflictTypeLabels[entry.Type]
	}
	typeSt := lipgloss.NewStyle().Foreground(p.Warning).Bold(true)
	if hasBg {
		typeSt = typeSt.Background(bg)
	}
	typeBadge := typeSt.Render(fitCell(typeLabel, 9))

	sepSt := lipgloss.NewStyle()
	if hasBg {
		sepSt = sepSt.Background(bg)
	}
	sep := sepSt.Render(" ")

	// Resolution badge with contextual labels.
	resLabel := "---"
	resColor := p.Muted
	resIdx := int(entry.Resolution)
	if resIdx >= 0 && resIdx < len(resolutionColorFns) {
		resColor = resolutionColorFns[resIdx](p)
	}
	switch entry.Resolution {
	case ResOurs:
		resLabel = m.oursLabel
	case ResTheirs:
		resLabel = m.theirsLabel
	case ResBoth:
		resLabel = "Both"
	}
	resSt := lipgloss.NewStyle().Foreground(resColor)
	if hasBg {
		resSt = resSt.Background(bg)
	}
	resBadge := resSt.Render(fitCell(resLabel, 7))

	// File name (fills remaining space).
	prefixW := 2 + 9 + 1   // cursor + type + sep
	suffixW := 1 + 7        // sep + resolution
	nameW := max(width-prefixW-suffixW, 1)

	name := filepath.Base(entry.Path)
	if lipgloss.Width(name) > nameW {
		name = name[:max(nameW-3, 0)] + "..."
	}
	nameColor := p.Foreground
	if isCursor {
		nameColor = p.Accent
	}
	nameSt := lipgloss.NewStyle().Foreground(nameColor)
	if hasBg {
		nameSt = nameSt.Background(bg)
	}

	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(typeBadge)
	b.WriteString(sep)
	b.WriteString(nameSt.Render(name))

	lineW := lipgloss.Width(b.String())
	gap := max(width-lineW-suffixW, 0)
	padSt := lipgloss.NewStyle()
	if hasBg {
		padSt = padSt.Background(bg)
	}
	if gap > 0 {
		b.WriteString(padSt.Render(strings.Repeat(" ", gap)))
	}
	b.WriteString(sep)
	b.WriteString(resBadge)

	row := b.String()
	if vis := lipgloss.Width(row); vis < width {
		b.WriteString(padSt.Render(strings.Repeat(" ", width-vis)))
		row = b.String()
	}

	return row
}

// renderHunkLines renders one line per hunk below a file entry.
// Returns at least one line (blank if no hunks) for visual spacing.
// Hunk lines do NOT receive the selection background — only the
// filename header row is highlighted when the file is selected.
func (m *Model) renderHunkLines(fileIdx int, isCursor bool, width int, p theme.Palette) []string {
	var hunks []ConflictHunk
	if fileIdx >= 0 && fileIdx < len(m.data.Entries) {
		hunks = m.data.Entries[fileIdx].Hunks
	}

	if len(hunks) == 0 {
		return []string{strings.Repeat(" ", width)}
	}

	lines := make([]string, len(hunks))
	for j, h := range hunks {
		lines[j] = m.renderSingleHunkLine(j, h, isCursor, width, p)
	}
	return lines
}

// hunkSizeLabel returns a compact "ours/theirs" line-count string.
func hunkSizeLabel(h ConflictHunk) string {
	ours := h.DivLine - h.StartLine - 1
	theirs := h.EndLine - h.DivLine - 1
	return fmt.Sprintf("%d/%d", ours, theirs)
}

// renderSingleHunkLine renders one hunk line: "  ▸ L12  3/5  snippet…".
// The current hunk gets an arrow indicator and primary text color.
func (m *Model) renderSingleHunkLine(hunkIdx int, h ConflictHunk, isCursor bool, width int, p theme.Palette) string {
	isCurrent := isCursor && hunkIdx == m.currentHunk

	fg := p.Muted
	if isCurrent {
		fg = p.Primary
	}

	// Arrow indicator for current hunk, plain indent otherwise.
	var prefix string
	if isCurrent {
		prefix = lipgloss.NewStyle().Foreground(p.Primary).Render("  ▸ ")
	} else {
		prefix = "    "
	}

	// Line number label.
	locSt := lipgloss.NewStyle().Foreground(fg)
	if isCurrent {
		locSt = locSt.Bold(true)
	}

	// Hunk size.
	sizeSt := lipgloss.NewStyle().Foreground(fg)

	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(locSt.Render(fmt.Sprintf("L%d", h.StartLine+1)))
	b.WriteString("  ")
	b.WriteString(sizeSt.Render(hunkSizeLabel(h)))

	// Content snippet — fill remaining width.
	col := lipgloss.Width(b.String())
	snippetW := width - col - 3 // 2 gap + 1 trailing padding
	if snippetW > 0 && h.Snippet != "" {
		snip := h.Snippet
		if lipgloss.Width(snip) > snippetW {
			snip = truncateToWidth(snip, snippetW)
		}
		snippetSt := lipgloss.NewStyle().Foreground(fg).Italic(true)
		b.WriteString("  ")
		b.WriteString(snippetSt.Render(snip))
	}

	row := b.String()
	if vis := lipgloss.Width(row); vis < width {
		row += strings.Repeat(" ", width-vis)
	}
	return row
}

// truncateToWidth truncates a string to fit within maxWidth visual columns,
// appending "…" if truncated.
func truncateToWidth(s string, maxWidth int) string {
	if maxWidth <= 1 {
		return "…"
	}
	runes := []rune(s)
	w := 0
	for i, r := range runes {
		rw := lipgloss.Width(string(r))
		if w+rw > maxWidth-1 { // reserve 1 for "…"
			return string(runes[:i]) + "…"
		}
		w += rw
	}
	return s
}

// ---------------------------------------------------------------------------
// File list input handling
// ---------------------------------------------------------------------------

// fileListKeyActions maps keys to file list actions that may return a tea.Cmd.
var fileListKeyActions = map[string]func(*Model) tea.Cmd{
	"j":      func(m *Model) tea.Cmd { m.moveFileDown(); return nil },
	"down":   func(m *Model) tea.Cmd { m.moveFileDown(); return nil },
	"k":      func(m *Model) tea.Cmd { m.moveFileUp(); return nil },
	"up":     func(m *Model) tea.Cmd { m.moveFileUp(); return nil },
	"l":      func(m *Model) tea.Cmd { m.nextHunk(); return nil },
	"right":  func(m *Model) tea.Cmd { m.nextHunk(); return nil },
	"h":      func(m *Model) tea.Cmd { m.prevHunk(); return nil },
	"left":   func(m *Model) tea.Cmd { m.prevHunk(); return nil },
	"g":      func(m *Model) tea.Cmd { m.moveFileToStart(); return nil },
	"home":   func(m *Model) tea.Cmd { m.moveFileToStart(); return nil },
	"G":      func(m *Model) tea.Cmd { m.moveFileToEnd(); return nil },
	"end":    func(m *Model) tea.Cmd { m.moveFileToEnd(); return nil },
	"o":      func(m *Model) tea.Cmd { return m.resolveFromList(ResOurs) },
	"t":      func(m *Model) tea.Cmd { return m.resolveFromList(ResTheirs) },
	"b":      func(m *Model) tea.Cmd { return m.resolveFromList(ResBoth) },
	"u":      func(m *Model) tea.Cmd { return m.undoResolution() },
	"ctrl+d": func(m *Model) tea.Cmd { m.fileListHalfPageDown(); return nil },
	"ctrl+u": func(m *Model) tea.Cmd { m.fileListHalfPageUp(); return nil },
}

func (m *Model) moveFileDown() {
	if m.selectedFile+1 < len(m.data.Entries) {
		m.selectedFile++
		m.onFileSelectionChanged()
	}
}

func (m *Model) moveFileUp() {
	if m.selectedFile > 0 {
		m.selectedFile--
		m.onFileSelectionChanged()
	}
}

func (m *Model) moveFileToStart() {
	m.selectedFile = 0
	m.onFileSelectionChanged()
}

func (m *Model) moveFileToEnd() {
	if n := len(m.data.Entries); n > 0 {
		m.selectedFile = n - 1
		m.onFileSelectionChanged()
	}
}

func (m *Model) fileListHalfPageDown() {
	half := max(m.fileListEntryH()/2, 1)
	n := len(m.data.Entries)
	for range half {
		if m.selectedFile+1 < n {
			m.selectedFile++
		}
	}
	m.onFileSelectionChanged()
}

func (m *Model) fileListHalfPageUp() {
	half := max(m.fileListEntryH()/2, 1)
	for range half {
		if m.selectedFile > 0 {
			m.selectedFile--
		}
	}
	m.onFileSelectionChanged()
}

func (m *Model) fileListEntryH() int {
	entryH := max(m.fileListH-fileListHeaderHeight, 0)
	start, end := m.visibleFileWindow(entryH)
	return max(end-start, 1)
}

// resolveFromList applies a resolution from the file list, returning the tea.Cmd.
func (m *Model) resolveFromList(r ConflictResolution) tea.Cmd {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return nil
	}
	e := &m.data.Entries[m.selectedFile]
	if !supportsResolution(e, r) {
		return nil
	}
	return m.setResolution(r)
}

// ClickFileList handles a mouse click in the file list.
func (m *Model) ClickFileList(localY int) {
	if localY < fileListHeaderHeight {
		return
	}
	entryH := max(m.fileListH-fileListHeaderHeight, 0)
	lineIdx := localY - fileListHeaderHeight
	if lineIdx >= entryH {
		return
	}
	idx := m.fileListHitTest(lineIdx, entryH)
	if idx < 0 || idx >= len(m.data.Entries) {
		return
	}
	m.selectedFile = idx
	m.onFileSelectionChanged()
}

// fileListHitTest maps a line offset within the entry area to a file index.
func (m *Model) fileListHitTest(lineOffset, entryH int) int {
	start, end := m.visibleFileWindow(entryH)
	cumulative := 0
	for i := start; i < end; i++ {
		h := m.entryLineCount(i)
		if lineOffset < cumulative+h {
			return i
		}
		cumulative += h
	}
	return -1
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fitCell pads or truncates a string to exactly the given width.
func fitCell(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s[:min(len(s), width)]
	}
	return s + strings.Repeat(" ", width-w)
}

// padLine pads a string to width.
func padLine(s string, width int) string {
	if vis := lipgloss.Width(s); vis < width {
		s += strings.Repeat(" ", width-vis)
	}
	return s
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
