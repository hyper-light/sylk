package mergediff

import (
	"strings"

	"github.com/adalundhe/sylk/ui/diffview"
	"github.com/adalundhe/sylk/ui/editor/findbar"
	tea "github.com/charmbracelet/bubbletea"
)

// diffFindBarHeight is the number of lines the find bar occupies.
const diffFindBarHeight = 2

// ToggleFindBar opens or closes the find bar.
func (m *Model) ToggleFindBar() {
	if m.findActive {
		m.closeFindBar()
	} else {
		m.openFindBar()
	}
}

// openFindBar creates a new find bar and builds searchable content.
func (m *Model) openFindBar() {
	m.findBar = findbar.New(0, 0, false)
	m.findActive = true
	m.buildFindContent()
	m.sizePanes()
	m.clampAllScrolls()
	m.viewDirty = true
}

// closeFindBar closes the find bar.
func (m *Model) closeFindBar() {
	m.findBar = nil
	m.findActive = false
	m.findContent = nil
	m.findLines = nil
	m.sizePanes()
	m.clampAllScrolls()
	m.viewDirty = true
}

// clampAllScrolls clamps all pane scroll offsets.
func (m *Model) clampAllScrolls() {
	for _, mp := range m.paneFiles {
		mp.clampScroll()
	}
}

// buildFindContent constructs searchable text from ALL files in the focused
// pane's pair data.
func (m *Model) buildFindContent() {
	mp := m.focusedMergePane()
	if mp == nil || mp.pairIdx >= len(m.pairData) {
		m.findContent = nil
		m.findLines = nil
		return
	}

	pd := m.pairData[mp.pairIdx]
	var buf strings.Builder
	var lines []findLineInfo
	offset := 0

	for fileIdx, fb := range pd.Blocks {
		for i, al := range fb.Lines {
			if al.Kind == diffview.DiffLineHunkSep {
				continue
			}
			text := alignedLineText(al)
			lines = append(lines, findLineInfo{
				fileIdx:    fileIdx,
				alignedIdx: i,
				runeStart:  offset,
			})
			buf.WriteString(text)
			buf.WriteByte('\n')
			offset += len([]rune(text)) + 1
		}
	}

	m.findContent = []rune(buf.String())
	m.findLines = lines
}

// alignedLineText returns the searchable text for an aligned line.
func alignedLineText(al diffview.AlignedLine) string {
	if al.Kind == diffview.DiffLineModified {
		return al.OldText + "\t" + al.NewText
	}
	if al.Kind == diffview.DiffLineAdded {
		return al.NewText
	}
	return al.OldText
}

// handleFindKey routes keyboard input to the find bar.
func (m *Model) handleFindKey(key tea.KeyMsg) tea.Cmd {
	action := m.findBar.HandleKey(key)
	if action == findbar.ActionClose {
		m.closeFindBar()
		return nil
	}
	m.dispatchFindAction(action)
	return nil
}

// dispatchFindAction applies a find bar action.
func (m *Model) dispatchFindAction(action findbar.Action) {
	if action == findbar.ActionQueryChanged {
		m.recomputeFind()
		m.jumpToCurrentFindMatch()
		return
	}
	if action == findbar.ActionNextMatch {
		m.findBar.AdvanceMatch()
	} else if action == findbar.ActionPrevMatch {
		m.findBar.RetreatMatch()
	} else {
		return
	}
	m.jumpToCurrentFindMatch()
}

// recomputeFind runs the find bar's search over current content.
func (m *Model) recomputeFind() {
	if m.findBar == nil || m.findContent == nil {
		return
	}
	m.findBar.Recompute(m.findContent, 0, len(m.findContent))
	m.viewDirty = true
}

// jumpToCurrentFindMatch scrolls to show the current match.
func (m *Model) jumpToCurrentFindMatch() {
	if m.findBar == nil {
		return
	}
	match, ok := m.findBar.CurrentMatch()
	if !ok {
		return
	}
	// Find which aligned line contains this rune offset.
	alignedIdx := 0
	for i := len(m.findLines) - 1; i >= 0; i-- {
		if m.findLines[i].runeStart <= match.Start {
			alignedIdx = m.findLines[i].alignedIdx
			break
		}
	}
	// Scroll the focused pane to approximately center this line.
	mp := m.focusedMergePane()
	if mp == nil {
		return
	}
	vpH := mp.viewportHeight()
	target := max(alignedIdx-vpH/2, 0)
	mp.scrollOffset = max(min(target, mp.maxScroll()), 0)
	mp.viewDirty = true
	m.viewDirty = true
}
