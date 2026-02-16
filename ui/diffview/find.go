package diffview

import (
	"strings"

	"github.com/adalundhe/sylk/ui/editor/findbar"
	tea "github.com/charmbracelet/bubbletea"
)

// diffFindBarHeight is the number of terminal lines the find bar occupies.
// Matches the editor findbar height (label + divider).
const diffFindBarHeight = 2

// findLineInfo maps a searchable text line to its source in the file block.
type findLineInfo struct {
	alignedIdx int // index in FileBlock.Lines
	runeStart  int // starting rune offset in findContent
}

// ToggleFileSearch opens file search if closed, or closes it if open.
func (m *Model) ToggleFileSearch() {
	if m.fileSearchActive {
		m.exitFileSearch()
	} else {
		m.enterFileSearch()
	}
}

// ToggleFindBar opens the diff find bar if closed, or closes it if open.
func (m *Model) ToggleFindBar() {
	if m.findActive {
		m.closeFindBar()
	} else {
		m.openFindBar()
	}
}

// FindBarActive reports whether the in-file find bar is open.
func (m *Model) FindBarActive() bool { return m.findActive }

// openFindBar creates a new find bar and prepares searchable content from
// the focused pane's current file.
func (m *Model) openFindBar() {
	m.findBar = findbar.New(0, 0, false)
	m.findActive = true
	m.buildFindContent()
	m.sizePanes()
	m.clampAllScrolls()
	m.viewDirty = true
}

// closeFindBar closes the find bar and restores viewport space.
func (m *Model) closeFindBar() {
	m.findBar = nil
	m.findActive = false
	m.clearFindState()
	m.sizePanes()
	m.clampAllScrolls()
	m.viewDirty = true
}

// clearFindState resets the searchable content and line mapping.
func (m *Model) clearFindState() {
	m.findContent = nil
	m.findLines = nil
}

// clampAllScrolls constrains scroll offsets on all panes after a viewport
// height change.
func (m *Model) clampAllScrolls() {
	for _, fp := range m.paneFiles {
		fp.clampScroll()
	}
}

// alignedLineText returns the searchable text for a single aligned line.
func alignedLineText(al AlignedLine) string {
	if al.Kind == DiffLineModified {
		return al.OldText + "\t" + al.NewText
	}
	if al.Kind == DiffLineAdded {
		return al.NewText
	}
	return al.OldText
}

// buildFindContent constructs searchable text from the focused pane's
// current file block.
func (m *Model) buildFindContent() {
	fb, ok := m.focusedFileBlock()
	if !ok {
		m.clearFindState()
		return
	}
	m.collectFindLines(fb)
}

// paneHasFileData reports whether a pane has valid pair data available.
func paneHasFileData(fp *FileDiffPane, pairCount int) bool {
	return fp != nil && fp.pairIdx < pairCount && !fp.noChanges
}

// focusedFileBlock returns the file block for the selected path in the
// focused pane. Returns false when no block is available.
func (m *Model) focusedFileBlock() (FileBlock, bool) {
	fp := m.focusedFileDiffPane()
	if !paneHasFileData(fp, len(m.pairData)) {
		return FileBlock{}, false
	}
	idx, ok := m.pairData[fp.pairIdx].PathIndex[m.selectedPath]
	if !ok {
		return FileBlock{}, false
	}
	return m.pairData[fp.pairIdx].Blocks[idx], true
}

// collectFindLines concatenates aligned line text into a flat rune slice
// with newline separators, building the line-to-offset mapping.
func (m *Model) collectFindLines(fb FileBlock) {
	var buf strings.Builder
	var lines []findLineInfo
	offset := 0
	for i, al := range fb.Lines {
		if al.Kind == DiffLineHunkSep {
			continue
		}
		text := alignedLineText(al)
		lines = append(lines, findLineInfo{alignedIdx: i, runeStart: offset})
		buf.WriteString(text)
		buf.WriteByte('\n')
		offset += len([]rune(text)) + 1
	}
	m.findContent = []rune(buf.String())
	m.findLines = lines
}

// handleFindKey routes keyboard input to the find bar and handles actions.
func (m *Model) handleFindKey(key tea.KeyMsg) tea.Cmd {
	action := m.findBar.HandleKey(key)
	if action == findbar.ActionClose {
		m.closeFindBar()
		return nil
	}
	m.dispatchFindAction(action)
	return nil
}

// dispatchFindAction applies a non-close find bar action.
func (m *Model) dispatchFindAction(action findbar.Action) {
	if action == findbar.ActionQueryChanged {
		m.recomputeFind()
		m.jumpToCurrentFindMatch()
		return
	}
	m.stepFindMatch(action)
}

// stepFindMatch advances or retreats the current match.
func (m *Model) stepFindMatch(action findbar.Action) {
	if action == findbar.ActionNextMatch {
		m.findBar.AdvanceMatch()
	} else if action == findbar.ActionPrevMatch {
		m.findBar.RetreatMatch()
	} else {
		return
	}
	m.jumpToCurrentFindMatch()
}

// recomputeFind runs the find bar's search over the current content.
func (m *Model) recomputeFind() {
	if m.findBar == nil || m.findContent == nil {
		return
	}
	m.findBar.Recompute(m.findContent, 0, len(m.findContent))
	m.viewDirty = true
}

// jumpToCurrentFindMatch scrolls the focused pane to show the current match.
func (m *Model) jumpToCurrentFindMatch() {
	if m.findBar == nil {
		return
	}
	match, ok := m.findBar.CurrentMatch()
	if !ok {
		return
	}
	alignedIdx := m.findMatchAlignedIdx(match.Start)
	m.scrollPaneToLine(alignedIdx)
}

// findMatchAlignedIdx returns the aligned line index containing the given
// rune offset in the find content.
func (m *Model) findMatchAlignedIdx(runeOffset int) int {
	for i := len(m.findLines) - 1; i >= 0; i-- {
		if m.findLines[i].runeStart <= runeOffset {
			return m.findLines[i].alignedIdx
		}
	}
	return 0
}

// scrollPaneToLine scrolls the focused pane so that the given aligned line
// index is approximately centered in the viewport.
func (m *Model) scrollPaneToLine(alignedIdx int) {
	fp := m.focusedFileDiffPane()
	if fp == nil {
		return
	}
	vpH := fp.viewportHeight()
	target := max(alignedIdx-vpH/2, 0)
	fp.scrollOffset = clampInt(target, 0, fp.maxScroll())
	fp.viewDirty = true
	m.viewDirty = true
}

// rebuildFindIfActive rebuilds the find bar's searchable content when the
// displayed file or focused pane changes.
func (m *Model) rebuildFindIfActive() {
	if !m.findActive || m.findBar == nil {
		return
	}
	m.buildFindContent()
	m.recomputeFind()
}
