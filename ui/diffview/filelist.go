package diffview

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
	devicons "github.com/epilande/go-devicons"
)

// fileListHeaderHeight is the number of lines for the file list title bar.
const fileListHeaderHeight = 1

// FileListView renders the file list sidebar for the left panel.
func (m *Model) FileListView() string {
	if m.fileListW <= 0 || m.fileListH <= 0 {
		return ""
	}
	return renderFileList(
		m.fileBlocks, m.selectedFile, m.FocusedFileIdx(),
		m.fileListScroll, m.fileListW, m.fileListH,
		m.fileListFocused, m.theme,
	)
}

// UpdateFileList handles keyboard input when the file list has focus.
func (m *Model) UpdateFileList(key string) {
	switch key {
	case "j", "down":
		if m.selectedFile+1 < len(m.fileBlocks) {
			m.selectedFile++
			m.fileListDirty = true
		}
	case "k", "up":
		if m.selectedFile > 0 {
			m.selectedFile--
			m.fileListDirty = true
		}
	case "g", "home":
		m.selectedFile = 0
		m.fileListDirty = true
	case "G", "end":
		if n := len(m.fileBlocks); n > 0 {
			m.selectedFile = n - 1
			m.fileListDirty = true
		}
	case "enter":
		m.LoadFileIntoFocusedPane(m.selectedFile)
	}
}

// renderFileList renders the scrollable file list with header.
func renderFileList(
	blocks []FileBlock,
	selected, focusedFileIdx, scroll int,
	w, h int,
	focused bool,
	th *theme.Theme,
) string {
	p := th.Palette

	// Header.
	headerSt := lipgloss.NewStyle().
		Bold(true).
		Foreground(p.Foreground)
	count := lipgloss.NewStyle().Foreground(p.Muted).Render(fmt.Sprintf(" (%d)", len(blocks)))
	headerText := headerSt.Render(" Diff Files") + count
	if vis := lipgloss.Width(headerText); vis < w {
		headerText += strings.Repeat(" ", w-vis)
	}

	// Scrollable entries.
	entryH := max(h-fileListHeaderHeight, 0)
	if len(blocks) == 0 {
		empty := lipgloss.NewStyle().Foreground(p.Muted).Italic(true).Render(" No files")
		lines := make([]string, entryH)
		lines[0] = empty
		blank := strings.Repeat(" ", w)
		for i := 1; i < entryH; i++ {
			lines[i] = blank
		}
		return headerText + "\n" + strings.Join(lines, "\n")
	}

	start, end := visibleWindow(selected, len(blocks), entryH)
	scroll = start // sync scroll

	var entries []string
	for i := start; i < end; i++ {
		isCursor := i == selected
		isFocusedPane := i == focusedFileIdx
		entries = append(entries, renderFileEntry(blocks[i], isCursor, isFocusedPane, w, focused, p))
	}

	// Pad remaining lines.
	blank := strings.Repeat(" ", w)
	for len(entries) < entryH {
		entries = append(entries, blank)
	}

	_ = scroll
	return headerText + "\n" + strings.Join(entries, "\n")
}

// renderFileEntry renders a single file list entry:
// [▸] [M] icon path          +N -M
func renderFileEntry(fb FileBlock, isCursor, isFocusedPane bool, width int, focused bool, p theme.Palette) string {
	var b strings.Builder

	// Cursor indicator.
	if isCursor {
		b.WriteString(lipgloss.NewStyle().Foreground(p.Primary).Render(" ▸ "))
	} else {
		b.WriteString("   ")
	}

	// Status badge.
	badgeColor := fileStatusColor(fb.Status, p)
	badge := lipgloss.NewStyle().Foreground(badgeColor).Bold(true).Render(fb.Status)
	b.WriteString(badge)
	b.WriteString(" ")

	// File icon.
	icon := devicons.IconForPath(filepath.Base(fb.Path))
	iconSt := lipgloss.NewStyle().Foreground(lipgloss.Color(icon.Color))
	b.WriteString(iconSt.Render(icon.Icon))
	b.WriteString(" ")

	// Stats (right-aligned).
	var statsStr string
	if fb.Additions > 0 {
		statsStr += lipgloss.NewStyle().Foreground(p.Success).Render(fmt.Sprintf("+%d", fb.Additions))
	}
	if fb.Deletions > 0 {
		if statsStr != "" {
			statsStr += " "
		}
		statsStr += lipgloss.NewStyle().Foreground(p.Error).Render(fmt.Sprintf("-%d", fb.Deletions))
	}
	statsStr += " "
	statsW := lipgloss.Width(statsStr)

	// File name (truncated to fit).
	prefixW := lipgloss.Width(b.String())
	nameW := max(width-prefixW-statsW, 1)
	name := filepath.Base(fb.Path)
	if lipgloss.Width(name) > nameW {
		name = name[:max(nameW-1, 0)] + "…"
	}

	nameSt := lipgloss.NewStyle().Foreground(p.Foreground)
	if isFocusedPane {
		nameSt = nameSt.Foreground(p.Primary).Bold(true)
	}
	b.WriteString(nameSt.Render(name))

	// Gap + stats.
	contentW := lipgloss.Width(b.String())
	gap := max(width-contentW-statsW, 0)
	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(statsStr)

	// Apply selection background.
	row := b.String()
	if isCursor && focused {
		row = lipgloss.NewStyle().Background(p.Selection).Render(row)
	}

	// Pad to full width.
	if vis := lipgloss.Width(row); vis < width {
		row += strings.Repeat(" ", width-vis)
	}

	return row
}

// fileStatusColor returns the palette color for a git diff status code.
func fileStatusColor(status string, p theme.Palette) lipgloss.Color {
	switch status {
	case "M":
		return p.Warning
	case "A":
		return p.Success
	case "D":
		return p.Error
	case "R", "C":
		return p.Teal
	default:
		return p.Muted
	}
}

// visibleWindow calculates the start and end indices for a scrolling window
// centered on the selected item. Mirrors session/list.go:visibleWindow.
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
