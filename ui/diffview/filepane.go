package diffview

import (
	"strings"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// FileDiffPane holds the diff state for a single file with independent scroll.
type FileDiffPane struct {
	fileIdx       int      // index into parent's fileBlocks/highlights
	renderedLines []string // pre-rendered scrollable lines for this file
	hunkStarts    []int    // hunk separator positions within this file
	scrollOffset  int
	totalLines    int
	width, height int
	viewDirty     bool
}

// newFileDiffPane creates a pane for the file at the given index.
func newFileDiffPane(fileIdx int) *FileDiffPane {
	return &FileDiffPane{fileIdx: fileIdx}
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

// View renders the file header + visible scrollable diff lines, padded to
// exactly fp.width x fp.height. When focused, the header uses an accent color.
func (fp *FileDiffPane) View(
	fb FileBlock,
	p theme.Palette,
	focused bool,
) string {
	if fp.width <= 0 || fp.height <= 0 {
		return ""
	}

	// File header (1 line), with focus accent when active.
	var header string
	if focused {
		header = renderFocusedFileBlockHeader(fb, fp.width, p)
	} else {
		header = renderFileBlockHeader(fb, fp.width, p)
	}
	contentHeight := max(fp.height-1, 0) // 1 line for header

	// Scrollable content.
	var contentLines []string
	if len(fp.renderedLines) > 0 {
		end := min(fp.scrollOffset+contentHeight, len(fp.renderedLines))
		start := min(fp.scrollOffset, end)
		contentLines = fp.renderedLines[start:end]
	}

	// Pad to fill viewport.
	for len(contentLines) < contentHeight {
		contentLines = append(contentLines, strings.Repeat(" ", fp.width))
	}

	return header + "\n" + strings.Join(contentLines, "\n")
}

// Rebuild re-renders the diff lines for this file. Called when the view mode
// (side-by-side vs unified) changes or on initial load.
func (fp *FileDiffPane) Rebuild(
	fb FileBlock,
	fh FileHighlight,
	sideBySide bool,
	maxOld, maxNew int,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
) {
	if fp.width <= 0 {
		fp.renderedLines = nil
		fp.totalLines = 0
		return
	}

	if fb.Binary {
		binSt := lipgloss.NewStyle().Foreground(p.Muted).Italic(true)
		fp.renderedLines = []string{binSt.Render(" Binary file")}
		fp.totalLines = 1
		fp.hunkStarts = nil
		fp.viewDirty = true
		return
	}

	var diffLines []string
	var hunkPositions []int
	if sideBySide {
		diffLines, hunkPositions = renderSideBySide(fb.Lines, fh, fp.width, maxOld, maxNew, syntaxStyles, defaultSt, p)
	} else {
		diffLines, hunkPositions = renderUnified(fb.Lines, fh, fp.width, maxOld, maxNew, syntaxStyles, defaultSt, p)
	}

	fp.renderedLines = diffLines
	fp.totalLines = len(diffLines)
	fp.hunkStarts = hunkPositions
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
// minus the 1-line file header).
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
		if al.OldLineNo > maxOld {
			maxOld = al.OldLineNo
		}
		if al.NewLineNo > maxNew {
			maxNew = al.NewLineNo
		}
	}
	return
}

// buildFileHighlight runs syntax highlighting for a single file block.
func buildSingleFileHighlight(fb FileBlock, hl *codepkg.Highlighter) FileHighlight {
	if fb.Binary {
		return FileHighlight{}
	}
	lang := langFromPath(fb.Path)
	return highlightFileBlock(fb, lang, hl)
}
