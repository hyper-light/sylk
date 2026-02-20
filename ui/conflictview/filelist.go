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

// resolutionLabels maps ConflictResolution to display labels.
var resolutionLabels = [...]string{
	ResUnresolved: "---",
	ResOurs:       "Ours",
	ResTheirs:     "Theirs",
	ResBoth:       "Both",
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

// renderFileEntries renders the visible file entries.
func (m *Model) renderFileEntries(entryH int, width int, p theme.Palette) string {
	total := len(m.data.Entries)
	if total == 0 {
		return renderEmptyFileList(entryH, width, p)
	}

	start, end := visibleWindow(m.selectedFile, total, entryH)
	entries := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		entry := &m.data.Entries[i]
		isCursor := i == m.selectedFile
		entries = append(entries, renderConflictFileEntry(entry, isCursor, width, p))
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

// renderConflictFileEntry renders a single file list entry.
// Format: [cursor] [type-badge] [filename] [resolution-badge]
func renderConflictFileEntry(entry *ConflictFileEntry, isCursor bool, width int, p theme.Palette) string {
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

	// Resolution badge.
	resLabel := "---"
	resColor := p.Muted
	resIdx := int(entry.Resolution)
	if resIdx >= 0 && resIdx < len(resolutionLabels) {
		resLabel = resolutionLabels[resIdx]
	}
	if resIdx >= 0 && resIdx < len(resolutionColorFns) {
		resColor = resolutionColorFns[resIdx](p)
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

// ---------------------------------------------------------------------------
// File list input handling
// ---------------------------------------------------------------------------

// fileListKeyActions maps keys to file list actions that may return a tea.Cmd.
var fileListKeyActions = map[string]func(*Model) tea.Cmd{
	"j":      func(m *Model) tea.Cmd { m.moveFileDown(); return nil },
	"down":   func(m *Model) tea.Cmd { m.moveFileDown(); return nil },
	"k":      func(m *Model) tea.Cmd { m.moveFileUp(); return nil },
	"up":     func(m *Model) tea.Cmd { m.moveFileUp(); return nil },
	"g":      func(m *Model) tea.Cmd { m.moveFileToStart(); return nil },
	"home":   func(m *Model) tea.Cmd { m.moveFileToStart(); return nil },
	"G":      func(m *Model) tea.Cmd { m.moveFileToEnd(); return nil },
	"end":    func(m *Model) tea.Cmd { m.moveFileToEnd(); return nil },
	"o":      func(m *Model) tea.Cmd { return m.resolveFromList(ResOurs) },
	"t":      func(m *Model) tea.Cmd { return m.resolveFromList(ResTheirs) },
	"b":      func(m *Model) tea.Cmd { return m.resolveFromList(ResBoth) },
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
	return max(m.fileListH-fileListHeaderHeight, 1)
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
	entryIdx := localY - fileListHeaderHeight
	total := len(m.data.Entries)
	start, _ := visibleWindow(m.selectedFile, total, entryH)
	idx := start + entryIdx
	if idx < 0 || idx >= total {
		return
	}
	m.selectedFile = idx
	m.onFileSelectionChanged()
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
