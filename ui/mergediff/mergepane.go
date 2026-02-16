package mergediff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/ui/diffview"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// MergePane renders ALL files stacked vertically with dividers.
type MergePane struct {
	pairIdx int

	renderedLines []string // all files concatenated with dividers
	fileStarts    []int    // renderedLines offset where each file section begins
	filePaths     []string // path for each file section (parallel to fileStarts)
	hunkStarts    []int    // hunk separator positions (global across all files)

	scrollOffset  int
	totalLines    int
	width, height int
	viewDirty     bool
}

// newMergePane creates a pane for the pair at the given index.
func newMergePane(pairIdx int) *MergePane {
	return &MergePane{pairIdx: pairIdx}
}

// SetSize updates the pane dimensions.
func (mp *MergePane) SetSize(w, h int) {
	mp.width = w
	mp.height = h
	mp.viewDirty = true
}

// viewportHeight returns the number of scrollable content lines.
func (mp *MergePane) viewportHeight() int {
	return max(mp.height-1, 1) // 1 line for pair header
}

// maxScroll returns the maximum valid scroll offset.
func (mp *MergePane) maxScroll() int {
	return max(mp.totalLines-mp.viewportHeight(), 0)
}

// clampScroll constrains scrollOffset.
func (mp *MergePane) clampScroll() {
	mp.scrollOffset = max(min(mp.scrollOffset, mp.maxScroll()), 0)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View renders the pane header + visible content body.
func (mp *MergePane) View(
	pair diffview.DiffPair,
	pd diffview.PairData,
	highlightPath string,
	p theme.Palette,
	focused bool,
) string {
	if mp.width <= 0 || mp.height <= 0 {
		return ""
	}
	header := renderMergePaneHeader(pair, pd, mp.width, p, focused)
	body := mp.renderContentBody(p)
	return header + "\n" + body
}

// renderMergePaneHeader renders the pane header showing hash range and totals.
func renderMergePaneHeader(pair diffview.DiffPair, pd diffview.PairData, width int, p theme.Palette, focused bool) string {
	hashSt := lipgloss.NewStyle().Foreground(p.Muted)
	ruleSt := lipgloss.NewStyle().Foreground(p.Border)
	if focused {
		hashSt = lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
		ruleSt = lipgloss.NewStyle().Foreground(p.BorderActive)
	}

	hashText := hashSt.Render(" " + pair.FromShort + " → " + pair.ToShort + " ")
	hashW := lipgloss.Width(hashText)

	// Aggregate stats across all files.
	totalAdd, totalDel := 0, 0
	for _, fb := range pd.Blocks {
		totalAdd += fb.Additions
		totalDel += fb.Deletions
	}
	statsStr := diffview.FormatAddDel(totalAdd, totalDel, p) + " "
	statsW := lipgloss.Width(statsStr)

	ruleLen := max(width-hashW-statsW, 0)
	rule := ruleSt.Render(strings.Repeat("─", ruleLen))

	return diffview.ClampLine(hashText+rule+statsStr, width)
}

// renderContentBody renders the scrollable content.
func (mp *MergePane) renderContentBody(p theme.Palette) string {
	contentHeight := max(mp.height-1, 0)
	if len(mp.renderedLines) == 0 {
		noSt := lipgloss.NewStyle().Foreground(p.Muted).Italic(true)
		lines := []string{noSt.Render(" No changes")}
		blank := strings.Repeat(" ", mp.width)
		for len(lines) < contentHeight {
			lines = append(lines, blank)
		}
		return strings.Join(lines, "\n")
	}
	end := min(mp.scrollOffset+contentHeight, len(mp.renderedLines))
	start := min(mp.scrollOffset, end)
	visible := mp.renderedLines[start:end]
	blank := strings.Repeat(" ", mp.width)
	for len(visible) < contentHeight {
		visible = append(visible, blank)
	}
	return strings.Join(visible, "\n")
}

// ---------------------------------------------------------------------------
// RebuildAllFiles
// ---------------------------------------------------------------------------

// RebuildAllFiles renders all files stacked with dividers.
func (mp *MergePane) RebuildAllFiles(
	pd diffview.PairData,
	sideBySide bool,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
) {
	if mp.width <= 0 {
		mp.renderedLines = nil
		mp.totalLines = 0
		return
	}

	// Compute max line number across ALL files for consistent gutter width.
	globalMaxOld, globalMaxNew := 0, 0
	for _, fb := range pd.Blocks {
		o, n := diffview.MaxLineNoForFile(fb)
		globalMaxOld = max(globalMaxOld, o)
		globalMaxNew = max(globalMaxNew, n)
	}

	var allLines []string
	var fileStarts []int
	var filePaths []string
	var hunkStarts []int

	for i, fb := range pd.Blocks {
		// Separator between files: rule line + blank line.
		if i > 0 {
			allLines = append(allLines, renderFileSeparator(mp.width, sideBySide, p))
			allLines = append(allLines, renderBlankWithDivider(mp.width, sideBySide, p))
		}

		fileStarts = append(fileStarts, len(allLines))
		filePaths = append(filePaths, fb.Path)

		// File header divider.
		header := renderFileDivider(fb, mp.width, sideBySide, p)
		allLines = append(allLines, header)

		if fb.Binary {
			binSt := lipgloss.NewStyle().Foreground(p.Muted).Italic(true)
			allLines = append(allLines, binSt.Render(" Binary file"))
			continue
		}

		fh := diffview.SafeHighlight(pd.Highlights, i)
		var lines []string
		var hunks []int
		if sideBySide {
			lines, hunks = diffview.RenderSideBySide(fb.Lines, fh, mp.width,
				globalMaxOld, globalMaxNew, syntaxStyles, defaultSt, p)
		} else {
			lines, hunks = diffview.RenderUnified(fb.Lines, fh, mp.width,
				globalMaxOld, globalMaxNew, syntaxStyles, defaultSt, p)
		}

		// Adjust hunk positions to global offset.
		offset := len(allLines)
		for _, h := range hunks {
			hunkStarts = append(hunkStarts, offset+h)
		}
		allLines = append(allLines, lines...)
	}

	mp.renderedLines = allLines
	mp.fileStarts = fileStarts
	mp.filePaths = filePaths
	mp.hunkStarts = hunkStarts
	mp.totalLines = len(allLines)
	mp.clampScroll()
	mp.viewDirty = true
}

// renderFileDivider renders a file header: ───── path  M  +12 -3 ──┬──
// In side-by-side mode a ┬ is placed at the center divider column.
func renderFileDivider(fb diffview.FileBlock, width int, sideBySide bool, p theme.Palette) string {
	ruleSt := lipgloss.NewStyle().Foreground(p.Border)
	pathSt := lipgloss.NewStyle().Foreground(p.Accent).Bold(true)
	statusSt := lipgloss.NewStyle().Foreground(p.Warning).Bold(true)

	// Build: ─── path  M  +N -M ───
	prefix := ruleSt.Render("───── ")
	path := pathSt.Render(fb.Path)
	status := statusSt.Render(" " + fb.Status + " ")
	stats := diffview.FormatAddDel(fb.Additions, fb.Deletions, p)

	leftPart := prefix + path + status + stats + " "
	leftW := lipgloss.Width(leftPart)
	trailLen := max(width-leftW, 0)

	// Place ┬ at the center divider column within the trailing rule.
	if sideBySide {
		col := (width - 1) / 2
		junctionPos := col - leftW
		if junctionPos >= 0 && junctionPos < trailLen {
			before := strings.Repeat("─", junctionPos)
			after := strings.Repeat("─", max(trailLen-junctionPos-1, 0))
			trail := ruleSt.Render(before+"┬"+after)
			return diffview.ClampLine(leftPart+trail, width)
		}
	}

	trail := ruleSt.Render(strings.Repeat("─", trailLen))
	return diffview.ClampLine(leftPart+trail, width)
}

// renderFileSeparator renders a horizontal rule with a bottom T-junction at
// the central divider column when in side-by-side mode.
func renderFileSeparator(width int, sideBySide bool, p theme.Palette) string {
	ruleSt := lipgloss.NewStyle().Foreground(p.Border)
	if !sideBySide {
		return ruleSt.Render(strings.Repeat("─", width))
	}
	col := (width - 1) / 2
	left := strings.Repeat("─", col)
	right := strings.Repeat("─", max(width-col-1, 0))
	return ruleSt.Render(left + "┴" + right)
}

// renderBlankWithDivider renders a blank spacer line below the file separator.
func renderBlankWithDivider(width int, _ bool, _ theme.Palette) string {
	return strings.Repeat(" ", width)
}

// ---------------------------------------------------------------------------
// Scroll
// ---------------------------------------------------------------------------

// scrollDown scrolls down by n lines.
func (mp *MergePane) scrollDown(n int) {
	mp.scrollOffset = min(mp.scrollOffset+n, mp.maxScroll())
	mp.viewDirty = true
}

// scrollUp scrolls up by n lines.
func (mp *MergePane) scrollUp(n int) {
	mp.scrollOffset = max(mp.scrollOffset-n, 0)
	mp.viewDirty = true
}

// scrollToTop scrolls to the beginning.
func (mp *MergePane) scrollToTop() {
	mp.scrollOffset = 0
	mp.viewDirty = true
}

// scrollToBottom scrolls to the end.
func (mp *MergePane) scrollToBottom() {
	mp.scrollOffset = mp.maxScroll()
	mp.viewDirty = true
}

// ScrollToFile scrolls to show the file at the given index.
func (mp *MergePane) ScrollToFile(idx int) {
	if idx < 0 || idx >= len(mp.fileStarts) {
		return
	}
	mp.scrollOffset = min(mp.fileStarts[idx], mp.maxScroll())
	mp.viewDirty = true
}

// FileIdxAtScroll returns the file index at the current scroll position.
func (mp *MergePane) FileIdxAtScroll() int {
	if len(mp.fileStarts) == 0 {
		return -1
	}
	idx := sort.SearchInts(mp.fileStarts, mp.scrollOffset+1) - 1
	return max(idx, 0)
}

// jumpNextHunk scrolls to the next hunk separator.
func (mp *MergePane) jumpNextHunk() {
	for _, pos := range mp.hunkStarts {
		if pos > mp.scrollOffset {
			mp.scrollOffset = min(pos, mp.maxScroll())
			mp.viewDirty = true
			return
		}
	}
	// No more hunks — try next file.
	mp.jumpNextFile()
}

// jumpPrevHunk scrolls to the previous hunk separator.
func (mp *MergePane) jumpPrevHunk() {
	for i := len(mp.hunkStarts) - 1; i >= 0; i-- {
		if mp.hunkStarts[i] < mp.scrollOffset {
			mp.scrollOffset = mp.hunkStarts[i]
			mp.viewDirty = true
			return
		}
	}
}

// jumpNextFile scrolls to the next file header.
func (mp *MergePane) jumpNextFile() {
	for _, pos := range mp.fileStarts {
		if pos > mp.scrollOffset {
			mp.scrollOffset = min(pos, mp.maxScroll())
			mp.viewDirty = true
			return
		}
	}
}

// jumpPrevFile scrolls to the previous file header.
func (mp *MergePane) jumpPrevFile() {
	for i := len(mp.fileStarts) - 1; i >= 0; i-- {
		if mp.fileStarts[i] < mp.scrollOffset {
			mp.scrollOffset = mp.fileStarts[i]
			mp.viewDirty = true
			return
		}
	}
}

// Suppress unused import warning for fmt package.
var _ = fmt.Sprintf
