package diffview

import (
	"strings"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// FileDiffPane holds the diff state for a single pair pane with independent scroll.
type FileDiffPane struct {
	pairIdx       int      // index into parent's pairs/pairData
	renderedLines []string // pre-rendered scrollable lines for the current file
	hunkStarts    []int    // hunk separator positions within this file
	noChanges     bool     // true when the selected file is absent in this pair
	scrollOffset  int
	totalLines    int
	width, height int
	viewDirty     bool
}

// newFileDiffPane creates a pane for the pair at the given index.
func newFileDiffPane(pairIdx int) *FileDiffPane {
	return &FileDiffPane{pairIdx: pairIdx}
}

// SetSize updates the pane dimensions and triggers a rebuild.
func (fp *FileDiffPane) SetSize(w, h int) {
	fp.width = w
	fp.height = h
	fp.viewDirty = true
}

// ViewDirty returns and clears the dirty flag.
func (fp *FileDiffPane) ViewDirty() bool {
	d := fp.viewDirty
	fp.viewDirty = false
	return d
}

// View renders the pair header + visible scrollable diff lines, padded to
// exactly fp.width x fp.height. When focused, the header uses an accent color.
func (fp *FileDiffPane) View(
	pair DiffPair,
	pd PairData,
	selectedPath string,
	p theme.Palette,
	focused bool,
) string {
	if fp.width <= 0 || fp.height <= 0 {
		return ""
	}
	header := renderPairPaneHeader(pair, pd, selectedPath, fp.width, p, focused)
	body := fp.renderContentBody(p)
	return header + "\n" + body
}

// renderContentBody renders the scrollable diff content, padded to height.
func (fp *FileDiffPane) renderContentBody(p theme.Palette) string {
	contentHeight := max(fp.height-1, 0)
	contentLines := fp.visibleLines(contentHeight, p)
	blank := strings.Repeat(" ", fp.width)
	for len(contentLines) < contentHeight {
		contentLines = append(contentLines, blank)
	}
	return strings.Join(contentLines, "\n")
}

// visibleLines returns the content lines for the current scroll position.
func (fp *FileDiffPane) visibleLines(contentHeight int, p theme.Palette) []string {
	if fp.noChanges {
		noSt := lipgloss.NewStyle().Foreground(p.Muted).Italic(true)
		return []string{noSt.Render(" No changes in this pair")}
	}
	if len(fp.renderedLines) == 0 {
		return nil
	}
	end := min(fp.scrollOffset+contentHeight, len(fp.renderedLines))
	start := min(fp.scrollOffset, end)
	return fp.renderedLines[start:end]
}

// RebuildForPath rebuilds this pane's diff content for the given file path
// within the pair's data. If the file is absent in this pair, a "no changes"
// placeholder is displayed.
func (fp *FileDiffPane) RebuildForPath(
	selectedPath string,
	pd PairData,
	sideBySide bool,
	maxOld, maxNew int,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
) {
	fb, fh, ok := lookupPathInPair(selectedPath, pd)
	if !ok {
		fp.setNoChanges()
		return
	}
	fp.noChanges = false
	fp.Rebuild(fb, fh, sideBySide, maxOld, maxNew, syntaxStyles, defaultSt, p)
}

// lookupPathInPair finds the file block and highlight for a path in a pair.
func lookupPathInPair(path string, pd PairData) (FileBlock, FileHighlight, bool) {
	idx, ok := pd.PathIndex[path]
	if !ok || path == "" {
		return FileBlock{}, FileHighlight{}, false
	}
	return pd.Blocks[idx], safeHighlight(pd.Highlights, idx), true
}

// safeHighlight returns the highlight at idx, or an empty one if out of bounds.
func safeHighlight(highlights []FileHighlight, idx int) FileHighlight {
	if idx < len(highlights) {
		return highlights[idx]
	}
	return FileHighlight{}
}

// setNoChanges clears the rendered lines and marks this pane as having
// no changes for the selected file.
func (fp *FileDiffPane) setNoChanges() {
	fp.noChanges = true
	fp.renderedLines = nil
	fp.totalLines = 0
	fp.hunkStarts = nil
	fp.scrollOffset = 0
	fp.viewDirty = true
}

// Rebuild re-renders the diff lines for a file block. Called when the view
// mode (side-by-side vs unified) changes or on initial load.
func (fp *FileDiffPane) Rebuild(
	fb FileBlock,
	fh FileHighlight,
	sideBySide bool,
	maxOld, maxNew int,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
) {
	fp.noChanges = false
	if fp.width <= 0 {
		fp.renderedLines = nil
		fp.totalLines = 0
		return
	}
	if fb.Binary {
		fp.setBinary(p)
		return
	}
	fp.renderDiff(fb, fh, sideBySide, maxOld, maxNew, syntaxStyles, defaultSt, p)
}

// setBinary sets the pane to display a binary file placeholder.
func (fp *FileDiffPane) setBinary(p theme.Palette) {
	binSt := lipgloss.NewStyle().Foreground(p.Muted).Italic(true)
	fp.renderedLines = []string{binSt.Render(" Binary file")}
	fp.totalLines = 1
	fp.hunkStarts = nil
	fp.viewDirty = true
}

// renderDiff renders diff lines for the given file block and stores them.
func (fp *FileDiffPane) renderDiff(
	fb FileBlock, fh FileHighlight, sideBySide bool,
	maxOld, maxNew int,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style, p theme.Palette,
) {
	if sideBySide {
		fp.renderedLines, fp.hunkStarts = renderSideBySide(fb.Lines, fh, fp.width, maxOld, maxNew, syntaxStyles, defaultSt, p)
	} else {
		fp.renderedLines, fp.hunkStarts = renderUnified(fb.Lines, fh, fp.width, maxOld, maxNew, syntaxStyles, defaultSt, p)
	}
	fp.totalLines = len(fp.renderedLines)
	fp.clampScroll()
	fp.viewDirty = true
}

// scrollDown scrolls down by n lines.
func (fp *FileDiffPane) scrollDown(n int) {
	fp.scrollOffset = min(fp.scrollOffset+n, fp.maxScroll())
	fp.viewDirty = true
}

// scrollUp scrolls up by n lines.
func (fp *FileDiffPane) scrollUp(n int) {
	fp.scrollOffset = max(fp.scrollOffset-n, 0)
	fp.viewDirty = true
}

// scrollToTop scrolls to the beginning.
func (fp *FileDiffPane) scrollToTop() {
	fp.scrollOffset = 0
	fp.viewDirty = true
}

// scrollToBottom scrolls to the end.
func (fp *FileDiffPane) scrollToBottom() {
	fp.scrollOffset = fp.maxScroll()
	fp.viewDirty = true
}

// viewportHeight returns the number of scrollable content lines (total height
// minus the 1-line pair header).
func (fp *FileDiffPane) viewportHeight() int {
	return max(fp.height-1, 1)
}

// maxScroll returns the maximum valid scroll offset.
func (fp *FileDiffPane) maxScroll() int {
	return max(fp.totalLines-fp.viewportHeight(), 0)
}

// clampScroll constrains scrollOffset to the valid range.
func (fp *FileDiffPane) clampScroll() {
	fp.scrollOffset = max(min(fp.scrollOffset, fp.maxScroll()), 0)
}

// jumpNextHunk scrolls to the next hunk separator after the current offset.
func (fp *FileDiffPane) jumpNextHunk() {
	for _, pos := range fp.hunkStarts {
		if pos > fp.scrollOffset {
			fp.scrollOffset = min(pos, fp.maxScroll())
			fp.viewDirty = true
			return
		}
	}
}

// jumpPrevHunk scrolls to the previous hunk separator before the current offset.
func (fp *FileDiffPane) jumpPrevHunk() {
	for i := len(fp.hunkStarts) - 1; i >= 0; i-- {
		if fp.hunkStarts[i] < fp.scrollOffset {
			fp.scrollOffset = fp.hunkStarts[i]
			fp.viewDirty = true
			return
		}
	}
}

// maxLineNoForFile returns the highest line numbers in a single file block.
func maxLineNoForFile(fb FileBlock) (maxOld, maxNew int) {
	for _, al := range fb.Lines {
		maxOld = max(maxOld, al.OldLineNo)
		maxNew = max(maxNew, al.NewLineNo)
	}
	return
}

// buildSingleFileHighlight runs syntax highlighting for a single file block.
func buildSingleFileHighlight(fb FileBlock, hl *codepkg.Highlighter) FileHighlight {
	if fb.Binary {
		return FileHighlight{}
	}
	lang := langFromPath(fb.Path)
	return highlightFileBlock(fb, lang, hl)
}
