package ui

import (
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/chat"
	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/committree"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/editor"
	"github.com/adalundhe/sylk/ui/filetree"
	"github.com/adalundhe/sylk/ui/gitpanel"
	inputpkg "github.com/adalundhe/sylk/ui/input"
	knowledgepkg "github.com/adalundhe/sylk/ui/knowledge"
	"github.com/adalundhe/sylk/ui/layout"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/pane"
	sessionpkg "github.com/adalundhe/sylk/ui/session"
	"github.com/adalundhe/sylk/ui/tabbar"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Preview panel handlers
// ---------------------------------------------------------------------------

// handleFilePreview loads a file into the preview panel (read-only).
func (m *AppModel) handleFilePreview(o msg.FilePreviewMsg) tea.Cmd {
	if o.Path == m.previewPanel.FilePath() && m.hasPreview() {
		return nil // Already showing this file.
	}

	data, err := os.ReadFile(o.Path)
	if err != nil {
		m.statusBar.SetFlash("Cannot preview: " + filepath.Base(o.Path))
		return nil
	}

	// Close the LSP document for the previous preview file (unless it's
	// also open in an editor tab).
	oldPath := m.previewPanel.FilePath()
	if oldPath != "" && oldPath != o.Path && !m.isFileOpenInEditor(oldPath) {
		m.lspDidCloseAsync(oldPath)
	}

	content := string(data)
	m.previewPanel.SetContent(content, o.Path, o.Language)

	wasActive := m.hasPreview()
	if !wasActive {
		// Insert preview as the leftmost leaf at the root level so it's
		// always the leftmost pane regardless of existing splits.
		m.paneCounter++
		m.previewPane = m.paneCounter
		m.paneTree.InsertLeft(m.previewPane, pane.SplitVertical)
	}

	if o.Line > 0 {
		m.previewPanel.ScrollToLine(o.Line - 1)
	}

	// Recompute layout when transitioning into preview.
	if !wasActive {
		m.recalcLayout()
	}

	// Notify LSP about the preview file so hover works. If the file is
	// already open in an editor, the document tracker silently skips the
	// duplicate open.
	lang := detectEditorLanguage(o.Path)
	return m.lspDidOpenCmd(o.Path, lang, content)
}

// dismissPreview closes the preview panel and restores the full code panel.
// Focus moves to the last active editor pane when one exists.
func (m *AppModel) dismissPreview() {
	if !m.hasPreview() {
		return
	}
	wasPreviewFocused := m.focus.Current() == pane.PaneFocusID(m.previewPane)
	previewPath := m.previewPanel.FilePath()

	// Remove preview leaf from the pane tree.
	m.paneTree.Close(m.previewPane)
	m.previewPane = 0
	m.previewPanel.ClearFile()

	// Close the LSP document unless the file is also open in an editor tab.
	if previewPath != "" && !m.isFileOpenInEditor(previewPath) {
		m.lspDidCloseAsync(previewPath)
	}

	// Focus the last active editor pane. Fall back to file tree when
	// no editor pane exists.
	if wasPreviewFocused || !m.isEditorFocused() {
		if len(m.paneEditors) > 0 {
			m.focusCodePanel()
		} else {
			m.focus.SetFocus(component.FocusFileTree)
		}
	}
	m.recalcLayout()
	m.syncFocusState()
}

// ---------------------------------------------------------------------------
// Markdown preview (rendered markdown split-right of the source editor)
// ---------------------------------------------------------------------------

// openMarkdownPreview splits the focused pane to the right and displays the
// rendered markdown content. If the markdown preview is already open, the
// content is updated in place.
func (m *AppModel) openMarkdownPreview(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		m.statusBar.SetFlash("Cannot preview: " + filepath.Base(path))
		return
	}
	content := string(data)

	// Already open — just refresh content.
	if m.mdPreviewPane != 0 {
		m.mdPreviewPanel.SetContent(content, path)
		return
	}

	// Compute the focused pane area for size checking.
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)
	contentH := max(innerH-1, 1)
	area := pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
	rects := m.paneTree.ComputeLayout(area)
	focusedRect := rects[m.focusedPane]

	m.paneCounter++
	m.mdPreviewPane = m.paneCounter

	if !m.paneTree.Split(m.focusedPane, m.mdPreviewPane, pane.SplitVertical, focusedRect) {
		m.paneCounter--
		m.mdPreviewPane = 0
		m.statusBar.SetFlash("Pane too small to split")
		return
	}

	m.mdPreviewPanel.SetContent(content, path)
	m.mdPreviewPanel.SetFocusID(pane.PaneFocusID(m.mdPreviewPane))
	m.resizeInlineEditor()
	m.syncViewState()
	m.statusBar.SetFlash("Markdown preview")
}

// dismissMarkdownPreview closes the markdown preview pane and restores layout.
func (m *AppModel) dismissMarkdownPreview() {
	if m.mdPreviewPane == 0 {
		return
	}
	wasFocused := m.focus.Current() == pane.PaneFocusID(m.mdPreviewPane)
	m.paneTree.Close(m.mdPreviewPane)
	m.mdPreviewPane = 0
	m.mdPreviewPanel.ClearFile()

	if wasFocused {
		m.focusCodePanel()
	}
	m.resizeInlineEditor()
	m.syncViewState()
	m.syncFocusState()
}

// mdPreviewTabBar renders a single tab for the markdown preview pane.
func (m *AppModel) mdPreviewTabBar(width int) string {
	if m.mdPreviewPanel.FilePath() == "" {
		return ""
	}
	hasSplits := m.paneTree.LeafCount() > 1
	isFocused := m.focus.Current() == pane.PaneFocusID(m.mdPreviewPane)
	cfg := tabbar.Config{
		Tabs: []tabbar.Tab{{
			Path:        m.mdPreviewPanel.FilePath(),
			Modified:    false,
			LabelPrefix: "Markdown: ",
		}},
		Active:     0,
		Width:      width,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      m.config.Theme(),
		HoverClose: m.mdPreviewTabHoverClose,
		Focused:    hasSplits && isFocused,
		DimActive:  hasSplits && !isFocused,
	}
	return tabbar.View(cfg)
}

// isFileOpenInEditor reports whether the given file path is currently open
// in any editor pane's tab order.
func (m *AppModel) isFileOpenInEditor(path string) bool {
	for _, ps := range m.paneEditors {
		for _, tabPath := range ps.tabOrder {
			if tabPath == path {
				return true
			}
		}
	}
	return false
}

// splitPane splits the focused editor pane in the given direction, creating
// a new empty editor pane. The original pane becomes Left/Top and the new
// pane becomes Right/Bottom. If the split would violate minimum pane size,
// the operation is silently ignored.
func (m *AppModel) splitPane(dir pane.SplitDir) {
	// Compute the current area of the focused pane for size checking.
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)
	contentH := max(innerH-1, 1) // minus status line
	area := pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
	rects := m.paneTree.ComputeLayout(area)
	focusedRect := rects[m.focusedPane]

	m.paneCounter++
	newID := m.paneCounter

	if !m.paneTree.Split(m.focusedPane, newID, dir, focusedRect) {
		m.paneCounter--
		m.statusBar.SetFlash("Pane too small to split")
		return
	}

	// Create a new empty editor for the new pane.
	th := m.config.Theme()
	m.paneEditors[newID] = &editorPaneState{editor: editor.New(th)}

	// Resize all panes to their new layout rects.
	m.resizeInlineEditor()
	m.syncViewState()
	m.statusBar.SetFlash("Split pane")
}

// closePane closes the currently focused pane. If the active editor has
// unsaved modifications, a save prompt is shown first. The sibling subtree's
// leftmost leaf receives focus. No-op if only one pane remains.
func (m *AppModel) closePane() {
	closingID := m.focusedPane

	// If the focused pane is the preview, delegate to dismissPreview.
	if closingID == m.previewPane {
		m.dismissPreview()
		return
	}

	// If the focused pane is the markdown preview, delegate to dismissMarkdownPreview.
	if closingID == m.mdPreviewPane {
		m.dismissMarkdownPreview()
		return
	}

	// Check for unsaved modifications in the active editor.
	ps := m.paneEditors[closingID]
	if ps.editor.Modified() {
		m.pendingPaneClose = closingID
		m.pendingClosePrompt = true
		name := filepath.Base(ps.editor.FilePath())
		m.statusBar.SetPrompt("Save changes to " + name + " before closing pane? (y)es (n)o")
		return
	}

	// If this is the last editor pane and a markdown preview is active,
	// clear the editor content so the preview takes the full panel.
	if m.mdPreviewPane != 0 && len(m.paneEditors) == 1 {
		ps := m.paneEditors[closingID]
		if ps != nil && ps.editor.FilePath() != "" {
			ws := ps.editor.Detach()
			m.editorCache.Put(ws)
		}
		if ps != nil {
			ps.editor.ClearFile()
			ps.tabOrder = nil
		}
		m.focus.SetFocus(pane.PaneFocusID(m.mdPreviewPane))
		m.resizeInlineEditor()
		m.syncViewState()
		m.syncFocusState()
		m.statusBar.SetFlash("Closed pane")
		return
	}

	m.closePaneForce(closingID)
}

// closePaneForce unconditionally closes the pane with the given ID.
// Detaches the editor to cache and collapses the tree node.
func (m *AppModel) closePaneForce(closingID pane.PaneID) {
	ps := m.paneEditors[closingID]
	if ps != nil && ps.editor.FilePath() != "" {
		ws := ps.editor.Detach()
		m.editorCache.Put(ws)
	}

	newFocus, ok := m.paneTree.Close(closingID)
	if !ok {
		return
	}
	delete(m.paneEditors, closingID)

	// Move focus to the sibling. If the sibling is the preview pane,
	// find the nearest editor pane instead (focusedPane must be an editor).
	if _, isEditor := m.paneEditors[newFocus]; isEditor {
		m.focusedPane = newFocus
	} else {
		for id := range m.paneEditors {
			m.focusedPane = id
			break
		}
	}
	m.focusCodePanel()
	m.resizeInlineEditor()
	m.syncViewState()
	m.syncFocusState()
	m.statusBar.SetFlash("Closed pane")
}

// openFromPreview promotes the previewed file to an editor tab.
// Closes the preview and opens the file via the standard FileOpenMsg pipeline.
func (m *AppModel) openFromPreview() tea.Cmd {
	path := m.previewPanel.FilePath()
	lang := m.previewPanel.Language()
	m.dismissPreview()

	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     path,
			Language: lang,
		}
	}
}

// overlayMdTooltipOnContent overlays the markdown "Preview" tooltip on the
// rendered code panel content when a markdown tab is being hovered.
func (m *AppModel) overlayMdTooltipOnContent(content string) string {
	if m.mdTooltipTab < 0 || m.mdTooltipPane == 0 {
		return content
	}
	rows := m.mdTooltipRows()
	lines := strings.Split(content, "\n")
	// The divider sits at row 1 (second row of the 2-row tab bar).
	// Place the tooltip starting on row 2 (first content row below the divider).
	overlayMdTooltipRows(lines, rows, m.mdTooltipX, tabbar.Height-1)
	return strings.Join(lines, "\n")
}

// overlayBlockedChord prepends the blocked-chord hint bar to content when a
// chord was triggered in a non-chat mode. Trims content from the bottom so
// the total line count stays within the panel's inner height.
func (m *AppModel) overlayBlockedChord(content string) string {
	th := m.config.Theme()
	hint := m.blockedChordHint(th)
	if hint == "" {
		return content
	}
	w, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(w-panelBorderSize, 1)
	hintWidth := lipgloss.Width(hint)
	pad := max(innerW-hintWidth, 0)
	divider := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", innerW))

	// Replace the first two lines instead of prepending (no layout shift).
	lines := strings.Split(content, "\n")
	hintLine := strings.Repeat(" ", pad) + hint
	if len(lines) >= chordHintLines {
		lines[0] = hintLine
		lines[1] = divider
	} else {
		lines = append([]string{hintLine, divider}, lines...)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Tab bar
// ---------------------------------------------------------------------------

// tabBarView renders the tab bar. Returns "" when there are no tabs.
func (m *AppModel) tabBarView() string {
	if len(m.focusedTabOrder()) == 0 {
		return ""
	}
	return tabbar.View(m.tabBarConfig())
}

// appendTab adds a file path to the tab order if not already present.
func (m *AppModel) appendTab(path string) {
	for _, p := range m.focusedTabOrder() {
		if p == path {
			return
		}
	}
	ps := m.paneEditors[m.focusedPane]
	ps.tabOrder = append(ps.tabOrder, path)
}

// isExistingTab reports whether path is already open as a tab in any pane.
func (m *AppModel) isExistingTab(path string) bool {
	pid, _ := m.findPaneWithTab(path)
	return pid != 0
}

// findPaneWithTab searches all editor panes for a tab matching the given
// path. Returns the PaneID and tab index, or (0, -1) if not found.
func (m *AppModel) findPaneWithTab(path string) (pane.PaneID, int) {
	for id, ps := range m.paneEditors {
		for i, p := range ps.tabOrder {
			if p == path {
				return id, i
			}
		}
	}
	return 0, -1
}

// removeTab removes a file path from the tab order.
func (m *AppModel) removeTab(path string) {
	ps := m.paneEditors[m.focusedPane]
	for i, p := range ps.tabOrder {
		if p == path {
			ps.tabOrder = slices.Delete(ps.tabOrder, i, i+1)
			return
		}
	}
}

// appendTabToPane adds path to the given pane's tab order if not already present.
func (m *AppModel) appendTabToPane(pid pane.PaneID, path string) {
	ps := m.paneEditors[pid]
	if ps == nil || slices.Contains(ps.tabOrder, path) {
		return
	}
	ps.tabOrder = append(ps.tabOrder, path)
}

// openFileCmd returns a tea.Cmd that emits a FileOpenMsg for the given path.
func (m *AppModel) openFileCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     path,
			Name:     filepath.Base(path),
			Language: detectEditorLanguage(path),
		}
	}
}

// finalizeCrossPaneDrop executes a cross-pane tab drop if one is pending.
// Returns nil if no cross-pane drop occurred.
func (m *AppModel) finalizeCrossPaneDrop() tea.Cmd {
	if m.tabDragIdx < 0 || m.tabDropTarget == 0 {
		return nil
	}
	if m.tabDragSourcePane == m.previewPane {
		return m.movePreviewToPane(m.tabDropTarget)
	}
	return m.moveTabToPane(m.tabDragSourcePane, m.tabDragIdx, m.tabDropTarget)
}

// moveTabToPane transfers tab at srcIdx from srcPID to dstPID.
func (m *AppModel) moveTabToPane(srcPID pane.PaneID, srcIdx int, dstPID pane.PaneID) tea.Cmd {
	srcPS := m.paneEditors[srcPID]
	if srcPS == nil || srcIdx >= len(srcPS.tabOrder) {
		return nil
	}
	path := srcPS.tabOrder[srcIdx]

	// Transfer: remove from source, append to target.
	srcPS.tabOrder = slices.Delete(srcPS.tabOrder, srcIdx, srcIdx+1)
	m.appendTabToPane(dstPID, path)

	// Handle source pane post-removal.
	m.reconcileSourcePane(srcPID, path)

	// Focus target pane and open the file.
	m.focusedPane = dstPID
	m.focusCodePanel()
	m.syncFocusState()
	m.resizeInlineEditor()
	return m.openFileCmd(path)
}

// movePreviewToPane promotes the previewed file to an editor tab in dstPID.
func (m *AppModel) movePreviewToPane(dstPID pane.PaneID) tea.Cmd {
	path := m.previewPanel.FilePath()
	if path == "" {
		return nil
	}
	m.appendTabToPane(dstPID, path)
	m.dismissPreview()

	m.focusedPane = dstPID
	m.focusCodePanel()
	m.syncFocusState()
	m.resizeInlineEditor()
	return m.openFileCmd(path)
}

// reconcileSourcePane adjusts the source pane after a tab was removed.
// If empty, closes the pane. If the active tab was removed, switches to adjacent.
func (m *AppModel) reconcileSourcePane(srcPID pane.PaneID, removedPath string) {
	srcPS := m.paneEditors[srcPID]
	if len(srcPS.tabOrder) == 0 {
		m.closePaneForce(srcPID)
		return
	}
	if srcPS.editor.FilePath() != removedPath {
		return
	}
	nextIdx := min(m.tabDragIdx, len(srcPS.tabOrder)-1)
	m.switchPaneToTab(srcPID, nextIdx)
}

// switchPaneToTab detaches the current editor in the given pane and loads
// the tab at the specified index from that pane's tab order.
func (m *AppModel) switchPaneToTab(pid pane.PaneID, idx int) {
	ps := m.paneEditors[pid]
	if ps == nil || idx < 0 || idx >= len(ps.tabOrder) {
		return
	}
	if ps.editor.FilePath() != "" {
		ws := ps.editor.Detach()
		m.editorCache.Put(ws)
	}
	m.loadFileIntoPane(pid, ps.tabOrder[idx])
}

// loadFileIntoPane loads a file into the specified pane's editor, using
// the tiered cache if available, otherwise reading from disk.
func (m *AppModel) loadFileIntoPane(pid pane.PaneID, path string) {
	ps := m.paneEditors[pid]
	if ps == nil {
		return
	}
	entry, ok := m.editorCache.Take(path)
	if ok && entry.IsWarm() {
		ps.editor.AttachWarm(entry.Warm())
		return
	}
	if ok {
		ps.editor.AttachCold(entry.Cold())
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	ps.editor.OpenFile(path, string(data), detectEditorLanguage(path))
}

// activeTabIndex returns the index of the active file in tabOrder.
func (m *AppModel) activeTabIndex() int {
	current := m.focusedEditor().FilePath()
	for i, p := range m.focusedTabOrder() {
		if p == current {
			return i
		}
	}
	return 0
}

// handleTabBarClick processes a click on the tab bar: close-icon clicks close
// the tab, other clicks begin a drag (and switch to the tab on release if no
// reordering occurred).
func (m *AppModel) handleTabBarClick(viewX int) (bool, tea.Cmd) {
	cfg := m.tabBarConfig()
	hit := tabbar.HitTest(cfg, viewX)
	if hit.IsLeftNav {
		m.tabArrowFlashLeftUntil = time.Now().Add(tabArrowFlashDuration)
		return true, m.prevTab()
	}
	if hit.IsRightNav {
		m.tabArrowFlashRightUntil = time.Now().Add(tabArrowFlashDuration)
		return true, m.nextTab()
	}
	if hit.TabIndex < 0 || hit.TabIndex >= len(m.focusedTabOrder()) {
		return true, nil
	}
	if hit.IsClose {
		return true, m.closeTab(hit.TabIndex)
	}
	// Begin drag and immediately switch to the pressed tab.
	m.tabDragIdx = hit.TabIndex
	m.tabDragSourcePane = m.focusedPane
	return true, m.switchToTab(hit.TabIndex)
}

// handleTabDragReorder performs intra-pane tab reordering during a drag.
func (m *AppModel) handleTabDragReorder(localX int) (bool, tea.Cmd) {
	ps := m.paneEditors[m.tabDragSourcePane]
	if ps == nil {
		return true, nil
	}
	r, hasPaneRect := m.focusedPaneRect()
	var cfg tabbar.Config
	if hasPaneRect {
		cfg = m.paneTabBarConfig(m.tabDragSourcePane, r.W)
	} else {
		cfg = m.tabBarConfig()
	}
	hit := tabbar.HitTest(cfg, localX)
	if hit.TabIndex >= 0 && hit.TabIndex != m.tabDragIdx && hit.TabIndex < len(ps.tabOrder) {
		ps.tabOrder[m.tabDragIdx], ps.tabOrder[hit.TabIndex] = ps.tabOrder[hit.TabIndex], ps.tabOrder[m.tabDragIdx]
		m.tabDragIdx = hit.TabIndex
	}
	return true, nil
}

type mdTooltipHoverState struct {
	tab  int
	pane pane.PaneID
	x    int
}

// updateTabHoverClose updates the tab close-icon hover state from a motion event.
// Handles multi-pane, split preview, and full-preview modes.
func (m *AppModel) updateTabHoverClose(mouse tea.MouseMsg) {
	savedTooltip := m.captureMdTooltipHoverState()
	m.resetTabHoverState()
	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return
	}

	if m.paneTree != nil && !m.paneTree.IsLeaf() {
		m.updateMultiPaneTabHover(mouse, savedTooltip)
		return
	}

	m.updateSinglePaneTabHover(mouse, savedTooltip)
}

func (m *AppModel) captureMdTooltipHoverState() mdTooltipHoverState {
	return mdTooltipHoverState{tab: m.mdTooltipTab, pane: m.mdTooltipPane, x: m.mdTooltipX}
}

func (m *AppModel) resetTabHoverState() {
	m.tabHoverClose = -1
	m.tabHoverPane = 0
	m.previewTabHoverClose = -1
	m.mdPreviewTabHoverClose = -1
	m.mdTooltipTab = -1
	m.mdTooltipPane = 0
}

func (m *AppModel) restoreMdTooltipHover(saved mdTooltipHoverState, pid pane.PaneID, localX, localY, top int) bool {
	if saved.tab < 0 || saved.pane != pid {
		return false
	}
	if localY >= top+mdTooltipHeight || localX < saved.x || localX >= saved.x+mdTooltipWidth {
		return false
	}
	m.mdTooltipTab = saved.tab
	m.mdTooltipPane = saved.pane
	m.mdTooltipX = saved.x
	return true
}

func (m *AppModel) updateMultiPaneTabHover(mouse tea.MouseMsg, saved mdTooltipHoverState) {
	pid, localX, localY, ok := m.paneViewCoords(mouse.X, mouse.Y)
	if !ok {
		return
	}
	if localY >= tabbar.Height {
		m.restoreMdTooltipHover(saved, pid, localX, localY, tabbar.Height)
		return
	}
	if pid == m.previewPane {
		m.updatePanePreviewTabHover(pid, localX, "Preview: ", m.previewPanel.FilePath(), &m.previewTabHoverClose)
		return
	}
	if pid == m.mdPreviewPane {
		m.updatePanePreviewTabHover(pid, localX, "Markdown: ", m.mdPreviewPanel.FilePath(), &m.mdPreviewTabHoverClose)
		return
	}
	m.updatePaneEditorTabHover(pid, localX)
}

func (m *AppModel) updateSinglePaneTabHover(mouse tea.MouseMsg, saved mdTooltipHoverState) {
	viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
	if viewY >= tabbar.Height {
		m.restoreMdTooltipHover(saved, m.focusedPane, viewX, viewY, m.tabBarHeight())
		return
	}

	if m.hasPreview() && m.viewMode == ViewEdit && len(m.focusedTabOrder()) == 0 {
		m.updatePreviewHoverFromConfig(m.previewTabBarConfig(), viewX, &m.previewTabHoverClose)
		return
	}

	if m.hasPreview() && m.isInsidePreviewHalf(mouse.X) {
		m.updatePreviewHoverFromConfig(m.previewTabBarConfig(), viewX, &m.previewTabHoverClose)
		return
	}

	if m.tabBarHeight() == 0 {
		return
	}
	editorX := viewX
	if pw := m.previewSplitWidth(); pw > 0 {
		editorX -= pw + 1 // offset past preview + divider
	}
	cfg := m.tabBarConfig()
	hit := tabbar.HitTest(cfg, editorX)
	if hit.IsClose {
		m.tabHoverPane = m.focusedPane
		m.tabHoverClose = hit.TabIndex
	}
	m.updateMdTooltip(m.focusedPane, hit, cfg)
}

func (m *AppModel) updatePanePreviewTabHover(
	pid pane.PaneID,
	localX int,
	labelPrefix string,
	path string,
	target *int,
) {
	rect, ok := m.paneRect(pid)
	if !ok {
		return
	}
	cfg := tabbar.Config{
		Tabs: []tabbar.Tab{{
			Path:        path,
			Modified:    false,
			LabelPrefix: labelPrefix,
		}},
		Active:    0,
		Width:     rect.W,
		NerdFonts: m.nerdFontsDetected,
		Theme:     m.config.Theme(),
	}
	m.updatePreviewHoverFromConfig(cfg, localX, target)
}

func (m *AppModel) updatePreviewHoverFromConfig(cfg tabbar.Config, viewX int, target *int) {
	hit := tabbar.HitTest(cfg, viewX)
	if hit.IsClose {
		*target = hit.TabIndex
	}
}

func (m *AppModel) updatePaneEditorTabHover(pid pane.PaneID, localX int) {
	ps := m.paneEditors[pid]
	if ps == nil || len(ps.tabOrder) == 0 {
		return
	}
	rect, ok := m.paneRect(pid)
	if !ok {
		return
	}
	cfg := m.paneTabBarConfig(pid, rect.W)
	hit := tabbar.HitTest(cfg, localX)
	if hit.IsClose {
		m.tabHoverPane = pid
		m.tabHoverClose = hit.TabIndex
	}
	m.updateMdTooltip(pid, hit, cfg)
}

// updateMdTooltip sets the markdown tooltip state when hovering a markdown
// file tab (not the close icon).
func (m *AppModel) updateMdTooltip(pid pane.PaneID, hit tabbar.HitResult, cfg tabbar.Config) {
	if hit.TabIndex < 0 || hit.IsClose {
		return
	}
	ps := m.paneEditors[pid]
	if ps == nil || hit.TabIndex >= len(ps.tabOrder) {
		return
	}
	path := ps.tabOrder[hit.TabIndex]
	if !isMarkdownFile(path) {
		return
	}
	startX, _ := tabbar.TabBounds(cfg, hit.TabIndex)
	m.mdTooltipTab = hit.TabIndex
	m.mdTooltipPane = pid
	m.mdTooltipX = startX
}

// mdTooltipRows returns the 3 rows (top border, content, bottom border) of
// the markdown "Preview" tooltip popup.
func (m *AppModel) mdTooltipRows() [3]string {
	th := m.config.Theme()
	border := lipgloss.NewStyle().
		Background(th.Palette.PopupBg).
		Foreground(th.Palette.Border)
	icon := lipgloss.NewStyle().
		Background(th.Palette.PopupBg).
		Foreground(th.Palette.Accent)
	label := lipgloss.NewStyle().
		Background(th.Palette.PopupBg).
		Foreground(th.Palette.Primary).
		Bold(true)
	pad := lipgloss.NewStyle().
		Background(th.Palette.PopupBg)

	inner := mdTooltipWidth - 2 // subtract left+right border columns
	topBot := border.Render("╭") + border.Render(strings.Repeat("─", inner)) + border.Render("╮")
	bottom := border.Render("╰") + border.Render(strings.Repeat("─", inner)) + border.Render("╯")
	content := border.Render("│") + pad.Render(" ") + icon.Render("◇") + label.Render("  Preview ") + pad.Render(" ") + border.Render("│")
	return [3]string{topBot, content, bottom}
}

// mdTooltipWidth is the visual width of the tooltip.
// Derived from: "│" (1) + " " (1) + "◇" (1) + "  Preview " (10) + " " (1) + "│" (1) = 15.
const mdTooltipWidth = 15

// mdTooltipHeight is the number of rows the tooltip occupies.
const mdTooltipHeight = 3

// overlayMdTooltipRows splices the 3-row tooltip popup into content lines
// below the hovered markdown tab. dividerRow is the tab bar divider row index.
func overlayMdTooltipRows(lines []string, rows [3]string, col, dividerRow int) {
	startRow := dividerRow + 1
	for i, tooltipRow := range rows {
		row := startRow + i
		if row < 0 || row >= len(lines) {
			continue
		}
		orig := lines[row]
		origW := lipgloss.Width(orig)
		if col >= origW {
			continue
		}
		left := truncateStyledN(orig, col)
		right := skipStyledN(orig, col+mdTooltipWidth)
		lines[row] = left + tooltipRow + right
	}
}

// updateFileTreeTabHover updates the tab close-icon hover state in the
// file tree's tabs panel from a mouse motion event.
func (m *AppModel) updateFileTreeTabHover(mouse tea.MouseMsg) {
	if !m.fileTree.InTabsMode() || !m.isFileTreeVisible() {
		m.fileTree.ClearTabCloseHover()
		return
	}
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	if treeW == 0 || treeH == 0 {
		m.fileTree.ClearTabCloseHover()
		return
	}
	treeX := m.fileTreePanelX()
	innerH := max(treeH-panelBorderSize, 0)
	contentLeft := treeX + 1
	contentRight := treeX + treeW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if mouse.X < contentLeft || mouse.X >= contentRight ||
		mouse.Y < contentTop || mouse.Y >= contentBottom {
		m.fileTree.ClearTabCloseHover()
		return
	}
	viewX := mouse.X - contentLeft
	viewY := mouse.Y - contentTop
	m.fileTree.HoverAt(viewX, viewY)
}

// tabBarConfig builds the tabbar.Config for the current state.
func (m *AppModel) tabBarConfig() tabbar.Config {
	rightW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)

	// In split mode the editor tab bar occupies only the right half.
	barWidth := innerW
	if m.hasPreview() && m.viewMode == ViewEdit && len(m.focusedTabOrder()) > 0 {
		dividerWidth := 1
		previewW := (innerW - dividerWidth) / 2
		barWidth = innerW - dividerWidth - previewW
	}

	tabs := make([]tabbar.Tab, len(m.focusedTabOrder()))
	currentPath := m.focusedEditor().FilePath()
	activeIdx := 0
	for i, path := range m.focusedTabOrder() {
		modified := false
		if path == currentPath {
			activeIdx = i
			modified = m.focusedEditor().Modified()
		} else {
			modified = m.editorCache.IsModified(path)
		}
		tabs[i] = tabbar.Tab{Path: path, Modified: modified}
	}

	return tabbar.Config{
		Tabs:       tabs,
		Active:     activeIdx,
		Width:      barWidth,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      m.config.Theme(),
		FlashLeft:  time.Now().Before(m.tabArrowFlashLeftUntil),
		FlashRight: time.Now().Before(m.tabArrowFlashRightUntil),
		HoverClose: m.tabHoverClose,
		Focused:    m.hasPreview() && m.isEditorFocused(),
		DimActive:  m.hasPreview() && !m.isEditorFocused(),
	}
}

// tabBarHeight returns the tab bar height (tabs + divider) when visible, 0 otherwise.
func (m *AppModel) tabBarHeight() int {
	if len(m.focusedTabOrder()) > 0 {
		return tabbar.Height
	}
	return 0
}

// resizeInlineEditor re-applies the correct size to the inline editor,
// accounting for the current tab bar visibility and preview split.
// Call after any tab mutation.
func (m *AppModel) resizeInlineEditor() {
	if m.viewMode != ViewEdit {
		return
	}
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	// Delegate to resizeCodePanelForPreview which handles split-mode sizing.
	m.resizeCodePanelForPreview(rightW, rightH)
}

// switchToTab switches the editor to the tab at the given index.
func (m *AppModel) switchToTab(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.focusedTabOrder()) {
		return nil
	}
	target := m.focusedTabOrder()[idx]
	if target == m.focusedEditor().FilePath() {
		return nil
	}
	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     target,
			Name:     filepath.Base(target),
			Language: detectEditorLanguage(target),
		}
	}
}

// ---------------------------------------------------------------------------
// Warp point methods
// ---------------------------------------------------------------------------

// setWarpPoint stores the current editor position as warp point idx (0-indexed).
func (m *AppModel) setWarpPoint(idx int) {
	fp := m.focusedEditor().FilePath()
	if fp == "" {
		return
	}
	line := m.focusedEditor().CursorLine()
	col := m.focusedEditor().CursorCol()
	startCol, endCol := m.focusedEditor().WordBoundsAt(line, col)
	if startCol == endCol {
		// Cursor not on a word — highlight just the single character.
		endCol = col + 1
		startCol = col
	}
	m.warpPoints[idx] = &WarpPoint{
		Path:     fp,
		Line:     line,
		Col:      col,
		StartCol: startCol,
		EndCol:   endCol,
	}
	m.syncEditorWarpLines()
}

// clearWarpPoint removes warp point idx (0-indexed).
func (m *AppModel) clearWarpPoint(idx int) {
	m.warpPoints[idx] = nil
	m.syncEditorWarpLines()
}

// teleportToWarp navigates to the warp point at idx via FileOpenMsg.
func (m *AppModel) teleportToWarp(idx int) tea.Cmd {
	wp := m.warpPoints[idx]
	if wp == nil {
		return nil
	}
	return func() tea.Msg {
		return msg.FileOpenMsg{
			Path:      wp.Path,
			Name:      filepath.Base(wp.Path),
			Language:  detectEditorLanguage(wp.Path),
			Line:      wp.Line + 1, // FileOpenMsg uses 1-based line.
			CursorCol: wp.Col,
		}
	}
}

// syncEditorWarpLines updates the editor's warp line indicators to reflect
// the current warp points for the active file.
func (m *AppModel) syncEditorWarpLines() {
	fp := m.focusedEditor().FilePath()
	if fp == "" {
		m.focusedEditor().SetWarpLines(nil)
		return
	}
	var warpMap map[int]editor.WarpLineInfo
	for i, wp := range m.warpPoints {
		if wp == nil || wp.Path != fp {
			continue
		}
		if warpMap == nil {
			warpMap = make(map[int]editor.WarpLineInfo)
		}
		warpMap[wp.Line] = editor.WarpLineInfo{
			Slot:     i + 1, // 1-indexed slot number for display.
			StartCol: wp.StartCol,
			EndCol:   wp.EndCol,
		}
	}
	m.focusedEditor().SetWarpLines(warpMap)
}

// nextTab switches to the next tab (wraps around).
func (m *AppModel) nextTab() tea.Cmd {
	if len(m.focusedTabOrder()) < 2 {
		return nil
	}
	idx := (m.activeTabIndex() + 1) % len(m.focusedTabOrder())
	return m.switchToTab(idx)
}

// prevTab switches to the previous tab (wraps around).
func (m *AppModel) prevTab() tea.Cmd {
	if len(m.focusedTabOrder()) < 2 {
		return nil
	}
	idx := (m.activeTabIndex() - 1 + len(m.focusedTabOrder())) % len(m.focusedTabOrder())
	return m.switchToTab(idx)
}

// tabNavRight navigates to the next tab. When the active tab is at the
// right edge of the visible window, it page-jumps to the first tab on
// the next visible page instead of stepping by one.
func (m *AppModel) tabNavRight() tea.Cmd {
	n := len(m.focusedTabOrder())
	if n < 2 {
		return nil
	}
	active := m.activeTabIndex()
	cfg := m.tabBarConfig()
	_, hi := tabbar.VisibleRange(cfg)

	// All tabs visible or not at right edge: simple step.
	if hi >= n-1 || active < hi {
		return m.switchToTab((active + 1) % n)
	}

	// At right edge of overflow window — page forward.
	// Simulate centering on hi+1 to find the new visible page.
	cfg.Active = hi + 1
	newLo, _ := tabbar.VisibleRange(cfg)
	return m.switchToTab(newLo)
}

// tabNavLeft navigates to the previous tab. When the active tab is at the
// left edge of the visible window, it page-jumps to the last tab on the
// previous visible page instead of stepping by one.
func (m *AppModel) tabNavLeft() tea.Cmd {
	n := len(m.focusedTabOrder())
	if n < 2 {
		return nil
	}
	active := m.activeTabIndex()
	cfg := m.tabBarConfig()
	lo, _ := tabbar.VisibleRange(cfg)

	// All tabs visible or not at left edge: simple step.
	if lo <= 0 || active > lo {
		return m.switchToTab((active - 1 + n) % n)
	}

	// At left edge of overflow window — page backward.
	// Simulate centering on lo-1 to find the new visible page.
	cfg.Active = lo - 1
	_, newHi := tabbar.VisibleRange(cfg)
	return m.switchToTab(newHi)
}

// toggleTabsPanel toggles the tabs list view in the file tree panel.
// When entering, saves current focus and focuses the file tree.
// When exiting, restores the previously focused panel.
func (m *AppModel) toggleTabsPanel() tea.Cmd {
	if m.fileTree.InTabsMode() {
		m.fileTree.ExitTabs()
		m.focus.SetFocus(m.preTabsFocus)
		m.syncFocusState()
		return nil
	}
	m.preTabsFocus = m.focus.Current()
	m.fileTree.SetTabs(m.focusedTabOrder(), m.focusedEditor().FilePath(), m.tabModifiedSet())
	if !m.isFileTreeVisible() {
		m.leftRing.setTo(component.FocusFileTree)
	}
	m.focus.SetFocus(component.FocusFileTree)
	m.syncFocusState()
	return nil
}

// handleSavePromptKey processes y/n/esc while the save-before-close prompt
// is active. All other keys are swallowed until the prompt is resolved.
func (m *AppModel) handleSavePromptKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "y":
		m.statusBar.ClearPrompt()
		m.pendingClosePrompt = false
		if pid := m.pendingPaneClose; pid != 0 {
			m.pendingPaneClose = 0
			m.saveEditorBuffer()
			m.closePaneForce(pid)
			return m, nil
		}
		m.saveEditorBuffer()
		return m, m.closeCurrentTab(true)
	case "n":
		m.statusBar.ClearPrompt()
		m.pendingClosePrompt = false
		if pid := m.pendingPaneClose; pid != 0 {
			m.pendingPaneClose = 0
			m.closePaneForce(pid)
			return m, nil
		}
		return m, m.closeCurrentTab(true)
	case "esc":
		m.statusBar.ClearPrompt()
		m.pendingClosePrompt = false
		m.pendingPaneClose = 0
		return m, nil
	default:
		return m, nil
	}
}

func (m *AppModel) handleCommandApprovalRequest(request msg.CommandApprovalRequestMsg) {
	if request.Proposal == nil {
		return
	}
	if m.commandApproval != nil || m.layerDecision != nil {
		m.commandApprovalQ = append(m.commandApprovalQ, request.Proposal)
		return
	}
	m.commandApproval = &commandApprovalState{
		proposal:    request.Proposal,
		selected:    0,
		activated:   -1,
		returnFocus: m.focus.Current(),
	}
	m.focus.SetFocus(component.FocusInput)
	m.syncFocusState()
	m.recalcLayout()
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
}

func (m *AppModel) resolveCommandApproval() {
	if m.commandApproval == nil {
		return
	}
	restore := m.commandApproval.returnFocus
	m.commandApproval = nil
	if len(m.commandApprovalQ) > 0 {
		next := m.commandApprovalQ[0]
		m.commandApprovalQ = m.commandApprovalQ[1:]
		m.commandApproval = &commandApprovalState{
			proposal:    next,
			selected:    0,
			activated:   -1,
			returnFocus: restore,
		}
		m.focus.SetFocus(component.FocusInput)
		m.syncFocusState()
		m.recalcLayout()
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return
	}
	if len(m.layerDecisionQ) > 0 {
		next := m.layerDecisionQ[0]
		m.layerDecisionQ = m.layerDecisionQ[1:]
		m.layerDecision = &layerDecisionState{
			request:     next,
			selected:    0,
			activated:   -1,
			returnFocus: restore,
		}
		m.focus.SetFocus(component.FocusInput)
		m.syncFocusState()
		m.recalcLayout()
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return
	}
	if restore != component.FocusID(0) {
		m.focus.SetFocus(restore)
		m.syncFocusState()
	}
	m.recalcLayout()
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
}

func (m *AppModel) handleCommandApprovalKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commandApproval == nil {
		return m, nil
	}
	switch key.String() {
	case "up", "shift+tab":
		m.commandApproval.selected = wrappedIndex(m.commandApproval.selected-1, len(commandApprovalOptionsForProposal(m.commandApproval.proposal)))
		m.commandApproval.activated = -1
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return m, nil
	case "down", "tab":
		m.commandApproval.selected = wrappedIndex(m.commandApproval.selected+1, len(commandApprovalOptionsForProposal(m.commandApproval.proposal)))
		m.commandApproval.activated = -1
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return m, nil
	case "enter", " ":
		return m, m.activateCommandApprovalOption(m.commandApproval.selected)
	case "esc":
		return m, m.activateCommandApprovalOption(commandApprovalCancelOptionIndex(m.commandApproval.proposal))
	default:
		return m, nil
	}
}

func wrappedIndex(index, size int) int {
	if size <= 0 {
		return 0
	}
	index %= size
	if index < 0 {
		index += size
	}
	return index
}

func (m *AppModel) activateCommandApprovalOption(index int) tea.Cmd {
	options := commandApprovalOptionsForProposal(nil)
	if m.commandApproval != nil {
		options = commandApprovalOptionsForProposal(m.commandApproval.proposal)
	}
	if m.commandApproval == nil || index < 0 || index >= len(options) {
		return nil
	}
	m.commandApproval.selected = index
	m.commandApproval.activated = index
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
	option := options[index]
	proposal := m.commandApproval.proposal
	return tea.Tick(75*time.Millisecond, func(time.Time) tea.Msg {
		return msg.CommandApprovalCommitMsg{
			Proposal: proposal,
			Decision: option.decision,
		}
	})
}

func (m *AppModel) commitCommandApproval(commit msg.CommandApprovalCommitMsg) tea.Cmd {
	approved := commandApprovalDecisionApproved(commit.Proposal, commit.Decision)
	payload := map[string]any{
		"decision": commit.Decision,
		"approved": approved,
	}
	payload["reason"] = commandApprovalDecisionReason(commit.Proposal, commit.Decision, approved)
	if commit.Proposal == nil || strings.TrimSpace(commit.Proposal.TargetAgentID) == "" || m.deps.GuideBus == nil {
		return func() tea.Msg {
			return msg.CommandApprovalResolvedMsg{}
		}
	}
	topic := guide.TopicResponses("guardian", commit.Proposal.TargetAgentID)
	if err := m.deps.GuideBus.Publish(topic, &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: commit.Proposal.CorrelationID,
		Type:          guide.MessageTypeResponse,
		SourceAgentID: "tui",
		Payload:       payload,
		Timestamp:     time.Now(),
	}); err != nil {
		slog.Warn("ui_command_approval_response_publish_failed",
			"topic", topic,
			"correlation_id", commit.Proposal.CorrelationID,
			"target_agent", commit.Proposal.TargetAgentID,
			"approved", approved,
			"error", err.Error(),
		)
	}
	return func() tea.Msg {
		return msg.CommandApprovalResolvedMsg{}
	}
}

func (m *AppModel) handleLayerDecisionRequest(request msg.LayerDecisionMsg) {
	if m.layerDecision != nil || m.commandApproval != nil {
		reqCopy := request
		m.layerDecisionQ = append(m.layerDecisionQ, &reqCopy)
		return
	}
	reqCopy := request
	m.layerDecision = &layerDecisionState{
		request:     &reqCopy,
		selected:    0,
		activated:   -1,
		returnFocus: m.focus.Current(),
	}
	m.focus.SetFocus(component.FocusInput)
	m.syncFocusState()
	m.recalcLayout()
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
}

func (m *AppModel) resolveLayerDecision() {
	if m.layerDecision == nil {
		return
	}
	restore := m.layerDecision.returnFocus
	m.layerDecision = nil
	if len(m.layerDecisionQ) > 0 {
		next := m.layerDecisionQ[0]
		m.layerDecisionQ = m.layerDecisionQ[1:]
		m.layerDecision = &layerDecisionState{
			request:     next,
			selected:    0,
			activated:   -1,
			returnFocus: restore,
		}
		m.focus.SetFocus(component.FocusInput)
		m.syncFocusState()
		m.recalcLayout()
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return
	}
	if len(m.commandApprovalQ) > 0 {
		next := m.commandApprovalQ[0]
		m.commandApprovalQ = m.commandApprovalQ[1:]
		m.commandApproval = &commandApprovalState{
			proposal:    next,
			selected:    0,
			activated:   -1,
			returnFocus: restore,
		}
		m.focus.SetFocus(component.FocusInput)
		m.syncFocusState()
		m.recalcLayout()
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return
	}
	if restore != component.FocusID(0) {
		m.focus.SetFocus(restore)
		m.syncFocusState()
	}
	m.recalcLayout()
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
}

func (m *AppModel) handleLayerDecisionKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.layerDecision == nil {
		return m, nil
	}
	switch key.String() {
	case "up", "shift+tab":
		m.layerDecision.selected = wrappedIndex(m.layerDecision.selected-1, len(layerDecisionOptions))
		m.layerDecision.activated = -1
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return m, nil
	case "down", "tab":
		m.layerDecision.selected = wrappedIndex(m.layerDecision.selected+1, len(layerDecisionOptions))
		m.layerDecision.activated = -1
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return m, nil
	case "enter", " ":
		return m, m.activateLayerDecisionOption(m.layerDecision.selected)
	case "esc":
		return m, m.activateLayerDecisionOption(2)
	default:
		return m, nil
	}
}

func (m *AppModel) activateLayerDecisionOption(index int) tea.Cmd {
	if m.layerDecision == nil || m.layerDecision.request == nil || index < 0 || index >= len(layerDecisionOptions) {
		return nil
	}
	m.layerDecision.selected = index
	m.layerDecision.activated = index
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
	option := layerDecisionOptions[index]
	request := m.layerDecision.request
	return tea.Tick(75*time.Millisecond, func(time.Time) tea.Msg {
		return msg.LayerDecisionCommitMsg{
			DAGID:    request.DAGID,
			LayerIdx: request.LayerIdx,
			Decision: option.decision,
		}
	})
}

func (m *AppModel) commitLayerDecision(commit msg.LayerDecisionCommitMsg) tea.Cmd {
	if strings.TrimSpace(commit.DAGID) == "" || m.deps.GuideBus == nil {
		return func() tea.Msg { return msg.LayerDecisionResolvedMsg{} }
	}
	if err := m.deps.GuideBus.Publish("dag.decision.response", &guide.Message{
		ID:            uuid.New().String(),
		Type:          guide.MessageTypeLayerDecisionResponse,
		SourceAgentID: "tui",
		Payload: map[string]any{
			"dag_id":    commit.DAGID,
			"layer_idx": commit.LayerIdx,
			"decision":  commit.Decision,
		},
		Timestamp: time.Now(),
	}); err != nil {
		slog.Warn("ui_layer_decision_publish_failed",
			"dag_id", commit.DAGID,
			"layer_idx", commit.LayerIdx,
			"decision", commit.Decision,
			"error", err.Error(),
		)
	}
	return func() tea.Msg { return msg.LayerDecisionResolvedMsg{} }
}

// closeCurrentTab closes the current tab and switches to an adjacent one.
// If it was the last tab, clears the editor to show the placeholder.
func (m *AppModel) closeCurrentTab(discard bool) tea.Cmd {
	if !discard && m.focusedEditor().Modified() {
		m.pendingClosePrompt = true
		name := filepath.Base(m.focusedEditor().FilePath())
		m.statusBar.SetPrompt("Save changes to " + name + "? (y)es (n)o")
		return nil
	}

	path := m.focusedEditor().FilePath()
	idx := m.activeTabIndex()

	m.removeTab(path)
	m.editorCache.Delete(path)

	// Clear the editor so the subsequent detachCurrentEditor (in
	// handleFileOpen) sees an empty file path and skips re-caching
	// the discarded state.
	if discard {
		m.focusedEditor().ClearFile()
	}

	// Last tab closed — show placeholder, stay in edit mode.
	if len(m.focusedTabOrder()) == 0 {
		m.focusedEditor().ClearFile()
		m.resizeInlineEditor()
		m.refreshTabsPanel()
		// In split mode, transition focus to the preview sub-panel
		// since the editor sub-panel no longer has content.
		if m.hasPreview() {
			m.focus.SetFocus(pane.PaneFocusID(m.previewPane))
			m.syncFocusState()
		}
		return nil
	}

	// Resize in case tab bar visibility changed.
	m.resizeInlineEditor()
	m.refreshTabsPanel()

	// Switch to adjacent tab: prefer same index (right), fallback left.
	next := min(idx, len(m.focusedTabOrder())-1)
	return m.switchToTab(next)
}

// closeTab closes the tab at the given index. If it's the active tab, behaves
// like closeCurrentTab(false). If it's a background tab, removes it silently.
func (m *AppModel) closeTab(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.focusedTabOrder()) {
		return nil
	}

	path := m.focusedTabOrder()[idx]
	activeIdx := m.activeTabIndex()

	// If closing the active tab, check for unsaved changes.
	if idx == activeIdx {
		return m.closeCurrentTab(false)
	}

	// Background tab with unsaved changes: switch to it and prompt.
	if m.editorCache.IsModified(path) {
		cmd := m.switchToTab(idx)
		m.pendingClosePrompt = true
		name := filepath.Base(path)
		m.statusBar.SetPrompt("Save changes to " + name + "? (y)es (n)o")
		return cmd
	}

	m.removeTab(path)
	m.editorCache.Delete(path)
	m.resizeInlineEditor()
	m.refreshTabsPanel()
	return nil
}

// closeTabByPath finds a tab by path and closes it.
func (m *AppModel) closeTabByPath(path string) tea.Cmd {
	for i, p := range m.focusedTabOrder() {
		if p == path {
			return m.closeTab(i)
		}
	}
	return nil
}

// refreshTabsPanel updates the file tree's tabs panel if it is currently
// visible, keeping it in sync with the app's tabOrder.
func (m *AppModel) refreshTabsPanel() {
	if m.fileTree.InTabsMode() {
		m.fileTree.SetTabs(m.focusedTabOrder(), m.focusedEditor().FilePath(), m.tabModifiedSet())
	}
}

// refreshTabsModified updates the modified indicators in the tabs panel
// without resetting cursor, scroll, or filter state.
func (m *AppModel) refreshTabsModified() {
	if m.fileTree.InTabsMode() {
		m.fileTree.UpdateTabModified(m.tabModifiedSet())
	}
}

// tabModifiedSet returns a set of paths with unsaved changes.
func (m *AppModel) tabModifiedSet() map[string]bool {
	currentPath := m.focusedEditor().FilePath()
	mod := make(map[string]bool, len(m.focusedTabOrder()))
	for _, path := range m.focusedTabOrder() {
		if path == currentPath {
			if m.focusedEditor().Modified() {
				mod[path] = true
			}
		} else if m.editorCache.IsModified(path) {
			mod[path] = true
		}
	}
	return mod
}

// ---------------------------------------------------------------------------
// Spring-based scroll
// ---------------------------------------------------------------------------

// scrollFPS is the simulation frame rate for the spring.
// Derived from: tickFastInterval (16ms) ≈ 60 FPS.
const scrollFPS = 60

// scrollFrequency is the spring's angular frequency (rad/s).
// Higher values produce snappier response; lower values produce smoother easing.
// Derived from: settling in ~8 frames at 60fps ≈ 130ms of visible easing.
const scrollFrequency = 30.0

// scrollDamping is the spring's damping ratio.
// 1.0 = critically damped (fastest approach without overshoot).
// Derived from: critically damped to prevent scroll bounce-back.
const scrollDamping = 1.0

// scrollImpulse is the target displacement per mouse wheel tick.
// Derived from: 3 lines per detent, matching common editor defaults.
const scrollImpulse = 3.0

// scrollKick is the velocity boost per unit of impulse.
// Each wheel event adds impulse * scrollKick to the spring velocity,
// so the first frame crosses an integer boundary immediately.
// Derived from: v₀ = 3 × 15 = 45; with ω=30, B = v₀ + ω(x₀−x_eq) = 45−90 = −45 < 0,
// guaranteeing monotonic approach without overshoot.
const scrollKick = 15.0

// scrollMaxLead is the maximum distance the target can lead the current position.
// Prevents runaway accumulation from rapid scrolling.
// Derived from: comfortable coast of ~1 second ≈ 30 lines.
const scrollMaxLead = 30.0

// scrollSettlePos is the position threshold for considering the spring settled.
// Derived from: less than half a rendered line of residual error.
const scrollSettlePos = 0.01

// scrollSettleVel is the velocity threshold for considering the spring settled.
// Derived from: negligible motion per frame at 60fps.
const scrollSettleVel = 0.01

// ---------------------------------------------------------------------------
// Bounce-back spring (overscroll rubber band)
// ---------------------------------------------------------------------------

// bounceDamping is the bounce spring's damping ratio.
// < 1.0 = underdamped: overshoots equilibrium, producing visible oscillation.
// Derived from: 0.5 produces ~1 visible bounce-back. Low enough for a
// visible elastic effect, high enough that the tail doesn't oscillate
// through integer rounding boundaries (which causes flicker).
const bounceDamping = 0.5

// bounceFrequency is the bounce spring's angular frequency (rad/s).
// Derived from: at 60fps with damping=0.5, frequency=14.5 settles in
// ~22 frames (~375ms). omega_d = 14.5 * sqrt(1 - 0.25) = 12.56 rad/s.
const bounceFrequency = 14.5

// bounceImpulse is the velocity added to the bounce spring per boundary hit.
// Derived from: peak displacement ≈ impulse / omega_d = 6.0 / 14.21 ≈ 0.42
// lines per hit. With 3-5 rapid hits per gesture, accumulates to ~1.3-2.1 lines.
const bounceImpulse = 6.0

// bounceMaxLines is the maximum visual displacement in lines.
// Derived from: 2 lines keeps content visually stable at the boundary while
// still producing a noticeable rubber-band effect.
const bounceMaxLines = 2.0

// bounceMaxVel is the maximum velocity the bounce spring can accumulate.
// Derived from: bounceMaxLines * omega_d = 2.0 * 12.56 ≈ 25.0, ensuring
// peak displacement never exceeds bounceMaxLines.
const bounceMaxVel = 25.0

// bounceSettleThreshold is the combined position+velocity threshold for
// considering the bounce settled.
// Derived from: less than 1% of a rendered line, letting the spring's
// natural exponential decay fully ease the bounce to rest.
const bounceSettleThreshold = 0.01

// ---------------------------------------------------------------------------
// Horizontal swipe (ring cycling)
// ---------------------------------------------------------------------------

// swipeThreshold is the accumulated horizontal scroll ticks required to
// trigger a ring cycle.
// Derived from: 6 ticks requires a deliberate gesture (trackpads emit
// 10-20 events per swipe), preventing accidental triggers.
const swipeThreshold = 6.0

// swipeDecay is the duration after which stale swipe accumulation resets.
// Derived from: 300ms is long enough for a continuous trackpad gesture
// but short enough that separate flicks don't combine.
const swipeDecay = 300 * time.Millisecond

// swipeCooldown is the dead period after a cycle fires during which
// further swipe events are ignored. Prevents the tail end of a single
// scroll gesture from immediately triggering a second cycle.
// Derived from: 500ms exceeds a typical trackpad gesture duration (~300ms)
// while feeling responsive for intentional repeated swipes.
const swipeCooldown = 500 * time.Millisecond

// handleMouse routes mouse wheel events to the panel under the cursor
// using spring-based scrolling, handles click-to-copy on chat messages,
// and handles text selection in the code panel.
// Mouse events are consumed when a full-screen overlay is active.
type appMouseHandler func(*AppModel, tea.MouseMsg) (tea.Cmd, bool)

var appMouseHandlers = []appMouseHandler{
	(*AppModel).handleOverlayMouse,
	(*AppModel).handleHoverPopupWheelMouse,
	(*AppModel).handleEditorMotionHoverMouse,
	(*AppModel).handleGitPanelHoverMouse,
	(*AppModel).handleMergeDiffMotionMouse,
	(*AppModel).handleDiffViewMotionMouse,
	(*AppModel).handleConflictViewMotionMouse,
	(*AppModel).handleCommitTreeHoverMouse,
	(*AppModel).handleSelectorHoverMouse,
	(*AppModel).handleCommandApprovalMouse,
	(*AppModel).handlePlanApprovalMouse,
	(*AppModel).handleLayerDecisionMouse,
	(*AppModel).handleInputWheelMouse,
	(*AppModel).handleMouseButtonMouse,
}

func (m *AppModel) handleMouse(mouse tea.MouseMsg) tea.Cmd {
	for _, handler := range appMouseHandlers {
		if cmd, handled := handler(m, mouse); handled {
			return cmd
		}
	}
	return nil
}

func (m *AppModel) handleOverlayMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.handleSearchOverlayWheel(mouse) || m.handleFieldManualOverlayWheel(mouse) {
		return nil, true
	}
	if m.overlay == overlayModal {
		return nil, true
	}
	return m.handleLoginOverlayMouse(mouse)
}

func (m *AppModel) handleSearchOverlayWheel(mouse tea.MouseMsg) bool {
	return m.handleScrollableOverlayWheel(mouse, m.overlay == overlaySearch && m.searchOverlay.Visible(), m.searchOverlay.ScrollUp, m.searchOverlay.ScrollDown)
}

func (m *AppModel) handleFieldManualOverlayWheel(mouse tea.MouseMsg) bool {
	return m.handleScrollableOverlayWheel(mouse, m.overlay == overlayFieldManual && m.fieldManualOverlay.Visible(), m.fieldManualOverlay.ScrollUp, m.fieldManualOverlay.ScrollDown)
}

func (m *AppModel) handleScrollableOverlayWheel(mouse tea.MouseMsg, active bool, scrollUp, scrollDown func()) bool {
	if !active {
		return false
	}
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		scrollUp()
	case tea.MouseButtonWheelDown:
		scrollDown()
	}
	return true
}

func (m *AppModel) handleLoginOverlayMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	localX, localY, ok := m.loginOverlayMousePoint(mouse)
	if !ok {
		return nil, false
	}
	if mouse.Action == tea.MouseActionMotion {
		m.loginPanel.HandleMouseMotion(localX, localY)
		return nil, true
	}
	if mouse.Button != tea.MouseButtonLeft || mouse.Action != tea.MouseActionPress {
		return nil, true
	}
	done, result, cmd := m.loginPanel.HandleClick(localX, localY)
	if done {
		return tea.Batch(cmd, m.handleLoginPanelResult(result)), true
	}
	return cmd, true
}

func (m *AppModel) loginOverlayMousePoint(mouse tea.MouseMsg) (int, int, bool) {
	if m.overlay != overlayLogin || !m.loginPanel.Active() {
		return 0, 0, false
	}
	localX := mouse.X - 1
	localY := mouse.Y - 1
	if localX < 0 || localY < 0 {
		return 0, 0, false
	}
	return localX, localY, true
}

func (m *AppModel) handleHoverPopupWheelMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.viewMode != ViewEdit || !m.focusedEditor().HoverActive() || !isWheelEvent(mouse) || !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return nil, false
	}
	if m.previewPanel.HoverActive() {
		pvx, pvy := m.previewPaneLocalCoords(mouse.X, mouse.Y)
		pvy -= tabbar.Height
		if m.previewPanel.IsInsideHoverPopup(pvx, pvy) {
			switch mouse.Button {
			case tea.MouseButtonWheelUp:
				m.previewPanel.ScrollHoverUp()
			case tea.MouseButtonWheelDown:
				m.previewPanel.ScrollHoverDown()
			}
			return nil, true
		}
	}

	vx, vy := m.focusedPaneLocalCoords(mouse.X, mouse.Y)
	if len(m.focusedTabOrder()) > 0 {
		vy -= tabbar.Height
	}
	vy -= m.focusedEditor().FindBarHeight()
	if !m.focusedEditor().IsInsideHoverPopup(vx, vy) {
		return nil, false
	}
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		m.focusedEditor().ScrollHoverUp()
	case tea.MouseButtonWheelDown:
		m.focusedEditor().ScrollHoverDown()
	}
	return nil, true
}

func (m *AppModel) handleEditorMotionHoverMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.viewMode != ViewEdit || mouse.Action != tea.MouseActionMotion || m.editorMouseDown || m.tabDragIdx >= 0 {
		return nil, false
	}
	m.updateTabHoverClose(mouse)
	m.updateFileTreeTabHover(mouse)
	return m.handleEditorMouseHover(mouse), true
}

func (m *AppModel) handleGitPanelHoverMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.viewMode != ViewGit || m.diffViewActive || m.gitPanel == nil || mouse.Action != tea.MouseActionMotion || m.gitPanel.IsDragging() {
		return nil, false
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	panelX := m.fileTreePanelX()
	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + max(panelH-panelBorderSize, 0)
	if mouse.X >= contentLeft && mouse.X < contentRight &&
		mouse.Y >= contentTop && mouse.Y < contentBottom {
		viewX := mouse.X - contentLeft
		viewY := mouse.Y - contentTop
		m.gitPanel.HandleMouseHover(viewX, viewY)
	} else {
		m.gitPanel.ClearHover()
	}
	return nil, false
}

func (m *AppModel) handleMergeDiffMotionMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if !m.mergeDiffViewActive || m.mergeDiffView == nil || mouse.Action != tea.MouseActionMotion || !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return nil, false
	}
	panelX := m.codePanelX()
	localMouse := tea.MouseMsg{
		X:      mouse.X - panelX - 1,
		Y:      mouse.Y - 1,
		Action: mouse.Action,
		Button: mouse.Button,
	}
	m.mergeDiffView.Update(localMouse)
	return nil, false
}

func (m *AppModel) handleDiffViewMotionMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if !m.diffViewActive || m.diffView == nil || mouse.Action != tea.MouseActionMotion || !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return nil, false
	}
	panelX := m.codePanelX()
	localMouse := tea.MouseMsg{
		X:      mouse.X - panelX - 1,
		Y:      mouse.Y - 1,
		Action: mouse.Action,
		Button: mouse.Button,
	}
	m.diffView.Update(localMouse)
	return nil, false
}

func (m *AppModel) handleConflictViewMotionMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if !m.conflictViewActive || m.conflictView == nil || mouse.Action != tea.MouseActionMotion || !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return nil, false
	}
	panelX := m.codePanelX()
	localMouse := tea.MouseMsg{
		X:      mouse.X - panelX - 1,
		Y:      mouse.Y - 1,
		Action: mouse.Action,
		Button: mouse.Button,
	}
	m.conflictView.Update(localMouse)
	return nil, false
}

func (m *AppModel) handleCommitTreeHoverMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.viewMode != ViewGit || m.diffViewActive || m.mergeDiffViewActive || m.commitTree == nil || mouse.Action != tea.MouseActionMotion {
		return nil, false
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusCodeViewer)
	panelX := m.codePanelX()
	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + max(panelH-panelBorderSize, 0)
	if mouse.X >= contentLeft && mouse.X < contentRight &&
		mouse.Y >= contentTop && mouse.Y < contentBottom {
		viewX := mouse.X - contentLeft
		viewY := mouse.Y - contentTop
		m.commitTree.HandleToolbarHover(viewX, viewY)
	} else {
		m.commitTree.ClearHover()
	}
	return nil, false
}

func (m *AppModel) handleSelectorHoverMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if mouse.Action != tea.MouseActionMotion {
		return nil, false
	}
	m.updateSelectorHover(mouse)
	return nil, false
}

func (m *AppModel) handleCommandApprovalMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.commandApproval == nil {
		return nil, false
	}
	inputTop := m.height - m.prevInputH - statusBarHeight
	if mouse.Y < inputTop || mouse.Y >= inputTop+m.prevInputH {
		return nil, false
	}
	contentX := mouse.X - 1
	contentY := mouse.Y - inputTop - 1
	if contentX < 0 {
		return nil, true
	}
	layout := m.commandApprovalLayout(max(m.width-2, 1))
	if isWheelEvent(mouse) {
		if !layout.commandApprovalCodeContains(contentX, contentY) {
			return nil, true
		}
		delta := 0
		switch mouse.Button {
		case tea.MouseButtonWheelUp:
			delta = -1
		case tea.MouseButtonWheelDown:
			delta = 1
		}
		if m.scrollCommandApprovalCode(layout, delta) {
			m.markSlotDirty(compositor.SlotInput)
			m.viewDirty = true
		}
		return nil, true
	}
	switch mouse.Action {
	case tea.MouseActionMotion:
		if idx, ok := layout.optionAt(contentX, contentY); ok {
			m.commandApproval.selected = idx
			m.commandApproval.activated = -1
			m.markSlotDirty(compositor.SlotInput)
			m.viewDirty = true
		}
		return nil, true
	case tea.MouseActionPress:
		if mouse.Button == tea.MouseButtonLeft {
			if idx, ok := layout.optionAt(contentX, contentY); ok {
				return m.activateCommandApprovalOption(idx), true
			}
		}
		return nil, true
	default:
		return nil, true
	}
}

func (m *AppModel) commandApprovalOptionAt(contentX, contentY int) (int, bool) {
	if m.commandApproval == nil || contentX < 0 || contentY < 0 {
		return 0, false
	}
	layout := m.commandApprovalLayout(max(m.width-2, 1))
	return layout.optionAt(contentX, contentY)
}

// handlePlanApprovalMouse mirrors handleCommandApprovalMouse for the
// plan-acceptance dialog: hover changes the highlighted option, left-
// click activates it (which fires the verdict commit through the
// 75ms tick → PlanApprovalCommitMsg → commitPlanApproval flow).
//
// Returns handled=true when the dialog is up and the click lands in
// the input panel, so terminal mouse events never leak through to
// the chat input field.
func (m *AppModel) handlePlanApprovalMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.planApproval == nil {
		return nil, false
	}
	inputTop := m.height - m.prevInputH - statusBarHeight
	if mouse.Y < inputTop || mouse.Y >= inputTop+m.prevInputH {
		return nil, false
	}
	contentX := mouse.X - 1
	contentY := mouse.Y - inputTop - 1
	if contentX < 0 {
		return nil, true
	}
	layout := m.planApprovalLayout(max(m.width-2, 1))
	if mouse.Button == tea.MouseButtonWheelUp || mouse.Button == tea.MouseButtonWheelDown {
		if !layout.commandApprovalCodeContains(contentX, contentY) {
			return nil, true
		}
		delta := 0
		switch mouse.Button {
		case tea.MouseButtonWheelUp:
			delta = -1
		case tea.MouseButtonWheelDown:
			delta = 1
		}
		if m.scrollPlanApprovalBody(layout, delta) {
			m.markSlotDirty(compositor.SlotInput)
			m.viewDirty = true
		}
		return nil, true
	}
	switch mouse.Action {
	case tea.MouseActionMotion:
		if idx, ok := layout.optionAt(contentX, contentY); ok {
			m.planApproval.selected = idx
			m.planApproval.activated = -1
			m.markSlotDirty(compositor.SlotInput)
			m.viewDirty = true
		}
		return nil, true
	case tea.MouseActionPress:
		if mouse.Button == tea.MouseButtonLeft {
			if idx, ok := layout.optionAt(contentX, contentY); ok {
				return m.activatePlanApprovalOption(idx), true
			}
		}
		return nil, true
	default:
		return nil, true
	}
}

func (m *AppModel) handleLayerDecisionMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.layerDecision == nil {
		return nil, false
	}
	inputTop := m.height - m.prevInputH - statusBarHeight
	if mouse.Y < inputTop || mouse.Y >= inputTop+m.prevInputH {
		return nil, false
	}
	contentX := mouse.X - 1
	contentY := mouse.Y - inputTop - 1
	if contentX < 0 {
		return nil, true
	}
	switch mouse.Action {
	case tea.MouseActionMotion:
		if idx, ok := m.layerDecisionOptionAt(contentX, contentY); ok {
			m.layerDecision.selected = idx
			m.layerDecision.activated = -1
			m.markSlotDirty(compositor.SlotInput)
			m.viewDirty = true
		}
		return nil, true
	case tea.MouseActionPress:
		if mouse.Button == tea.MouseButtonLeft {
			if idx, ok := m.layerDecisionOptionAt(contentX, contentY); ok {
				return m.activateLayerDecisionOption(idx), true
			}
		}
		return nil, true
	default:
		return nil, true
	}
}

func (m *AppModel) layerDecisionOptionAt(contentX, contentY int) (int, bool) {
	if m.layerDecision == nil || contentX < 0 || contentY < 0 {
		return 0, false
	}
	layout := m.layerDecisionLayout(max(m.width-2, 1))
	for _, hitbox := range layout.hitboxes {
		if contentY != hitbox.y {
			continue
		}
		if contentX < hitbox.x0 || contentX >= hitbox.x1 {
			continue
		}
		return hitbox.option, true
	}
	return 0, false
}

func (m *AppModel) handleInputWheelMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.commandApproval != nil || m.layerDecision != nil {
		return nil, false
	}
	if m.focus.Current() != component.FocusInput || !m.input.CanScroll() {
		return nil, false
	}
	inputTop := m.height - m.prevInputH - statusBarHeight
	if mouse.Y < inputTop || mouse.Y >= inputTop+m.prevInputH {
		return nil, false
	}
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		m.input.ScrollUp()
		return nil, true
	case tea.MouseButtonWheelDown:
		m.input.ScrollDown()
		return nil, true
	default:
		return nil, false
	}
}

func (m *AppModel) handleMouseButtonMouse(mouse tea.MouseMsg) (tea.Cmd, bool) {
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		if mouse.Alt {
			m.applySwipeImpulse(mouse.X, -1)
		} else {
			m.applyScrollImpulse(mouse.X, mouse.Y, -scrollImpulse)
		}
		return nil, true
	case tea.MouseButtonWheelDown:
		if mouse.Alt {
			m.applySwipeImpulse(mouse.X, 1)
		} else {
			m.applyScrollImpulse(mouse.X, mouse.Y, scrollImpulse)
		}
		return nil, true
	case tea.MouseButtonWheelLeft:
		m.applySwipeImpulse(mouse.X, -1)
		return nil, true
	case tea.MouseButtonWheelRight:
		m.applySwipeImpulse(mouse.X, 1)
		return nil, true
	case tea.MouseButtonLeft:
		return m.handleLeftClick(mouse), true
	default:
		return nil, false
	}
}

// isWheelEvent reports whether the mouse event is a scroll wheel action.
func isWheelEvent(mouse tea.MouseMsg) bool {
	return mouse.Button == tea.MouseButtonWheelUp ||
		mouse.Button == tea.MouseButtonWheelDown
}

// handleLeftClick dispatches left-button events to the file tree,
// inline editor (in edit mode), and chat panels.
func (m *AppModel) handleLeftClick(mouse tea.MouseMsg) tea.Cmd {
	if m.viewMode == ViewEdit {
		if consumed, cmd := m.handleEditorMouse(mouse); consumed {
			return cmd
		}
	}
	if cmd, handled := m.handleGitDragLeftClick(mouse); handled {
		return cmd
	}
	if cmd, handled := m.handleInputDragLeftClick(mouse); handled {
		return cmd
	}
	if mouse.Action != tea.MouseActionPress {
		return nil
	}
	if cmd, handled := m.handleOverlayLeftClick(mouse); handled {
		return cmd
	}
	if m.viewMode == ViewGit {
		return m.handleGitModeLeftClick(mouse)
	}
	if handled := m.handleInputPanelPress(mouse); handled {
		return nil
	}
	if cmd := m.handleAgentSelectorClick(mouse.X, mouse.Y); cmd != nil {
		return cmd
	}
	if cmd := m.handleAgentPanelClick(mouse.X, mouse.Y); cmd != nil {
		return cmd
	}

	if cmd := m.handleFileTreeClick(mouse.X, mouse.Y); cmd != nil {
		return cmd
	}
	return m.handleChatClick(mouse.X, mouse.Y)
}

func (m *AppModel) handleGitDragLeftClick(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if m.viewMode != ViewGit || m.gitPanel == nil || !m.gitPanel.IsDragging() {
		return nil, false
	}
	viewX := mouse.X - m.fileTreePanelX() - 1
	switch mouse.Action {
	case tea.MouseActionMotion:
		m.gitPanel.HandleMouseMotion(viewX)
		return nil, true
	case tea.MouseActionRelease:
		m.gitPanel.HandleMouseRelease()
		return nil, true
	default:
		return nil, false
	}
}

func (m *AppModel) handleInputDragLeftClick(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if mouse.Action == tea.MouseActionRelease && m.inputMouseDown {
		m.inputMouseDown = false
		return nil, true
	}
	if mouse.Action != tea.MouseActionMotion || !m.inputMouseDown {
		return nil, false
	}
	inputTop := m.height - m.prevInputH - statusBarHeight
	contentX := max(mouse.X-1, 0)
	contentY := max(mouse.Y-inputTop-1, 0)
	m.input.DragTo(contentX, contentY)
	return nil, true
}

func (m *AppModel) handleOverlayLeftClick(mouse tea.MouseMsg) (tea.Cmd, bool) {
	if mouse.Button != tea.MouseButtonLeft {
		return nil, false
	}
	if m.conflictViewActive && m.conflictView != nil {
		return m.handleConflictOverlayLeftClick(mouse), true
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.handleMergeDiffOverlayLeftClick(mouse), true
	}
	if m.diffViewActive && m.diffView != nil {
		return m.handleDiffOverlayLeftClick(mouse), true
	}
	return nil, false
}

func (m *AppModel) handleConflictOverlayLeftClick(mouse tea.MouseMsg) tea.Cmd {
	m.handleConflictFileListClick(mouse.X, mouse.Y)
	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return nil
	}
	cmd := m.conflictView.Update(m.codePanelLocalMouse(mouse))
	m.focus.SetFocus(component.FocusConflictView)
	m.syncFocusState()
	return cmd
}

func (m *AppModel) handleMergeDiffOverlayLeftClick(mouse tea.MouseMsg) tea.Cmd {
	m.handleMergeDiffFileListClick(mouse.X, mouse.Y)
	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return nil
	}
	localMouse := m.codePanelLocalMouse(mouse)
	isPaneClick := m.mergeDiffView.IsPaneClick(localMouse.Y)
	cmd := m.mergeDiffView.Update(localMouse)
	if isPaneClick {
		m.focus.SetFocus(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
		m.syncFocusState()
	}
	return cmd
}

func (m *AppModel) handleDiffOverlayLeftClick(mouse tea.MouseMsg) tea.Cmd {
	m.handleDiffFileListClick(mouse.X, mouse.Y)
	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return nil
	}
	localMouse := m.codePanelLocalMouse(mouse)
	isPaneClick := m.diffView.IsPaneClick(localMouse.Y)
	cmd := m.diffView.Update(localMouse)
	if isPaneClick {
		m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
		m.syncFocusState()
	}
	return cmd
}

func (m *AppModel) codePanelLocalMouse(mouse tea.MouseMsg) tea.MouseMsg {
	panelX := m.codePanelX()
	return tea.MouseMsg{
		X:      mouse.X - panelX - 1,
		Y:      mouse.Y - 1,
		Action: mouse.Action,
		Button: mouse.Button,
	}
}

func (m *AppModel) handleGitModeLeftClick(mouse tea.MouseMsg) tea.Cmd {
	if cmd := m.handleGitPanelClick(mouse.X, mouse.Y); cmd != nil {
		return cmd
	}
	if cmd := m.handleCommitTreeClick(mouse.X, mouse.Y); cmd != nil {
		return cmd
	}
	return nil
}

func (m *AppModel) handleInputPanelPress(mouse tea.MouseMsg) bool {
	if m.commandApproval != nil {
		contentX := mouse.X - 1
		contentY := mouse.Y - (m.height - m.prevInputH - statusBarHeight) - 1
		if idx, ok := m.commandApprovalOptionAt(contentX, contentY); ok {
			m.commandApproval.selected = idx
			m.markSlotDirty(compositor.SlotInput)
			m.viewDirty = true
		}
		return true
	}
	if m.layerDecision != nil {
		contentX := mouse.X - 1
		contentY := mouse.Y - (m.height - m.prevInputH - statusBarHeight) - 1
		if idx, ok := m.layerDecisionOptionAt(contentX, contentY); ok {
			m.layerDecision.selected = idx
			m.markSlotDirty(compositor.SlotInput)
			m.viewDirty = true
		}
		return true
	}
	inputTop := m.height - m.prevInputH - statusBarHeight
	if mouse.Y < inputTop || mouse.Y >= inputTop+m.prevInputH {
		return false
	}
	contentX := mouse.X - 1
	contentY := mouse.Y - inputTop - 1
	if contentX < 0 || contentY < 0 {
		return false
	}
	if mouse.Shift {
		m.input.ExtendSelectionTo(contentX, contentY)
	} else {
		m.input.DragStart(contentX, contentY)
	}
	m.inputMouseDown = true
	m.focus.SetFocus(component.FocusInput)
	m.syncFocusState()
	return true
}

// agentSelectorScreenY returns the screen Y of the model selector line.
// The selector is the last content line inside the left panel border,
// i.e. the row just above the bottom border = leftH - 2 (0-indexed).
// Returns -1 if the left panel is not visible.
func (m *AppModel) agentSelectorScreenY() int {
	if m.leftPanelSections.selectorY <= 0 {
		return -1
	}
	return m.leftPanelSections.selectorY
}

func (m *AppModel) isInsideLeftPanelContent(x, y int) bool {
	leftW, leftH := m.layout.GetPanelSize(component.FocusSessionPanel)
	rect := pane.Rect{X: 1, Y: 1, W: max(leftW-panelBorderSize, 1), H: max(leftH-panelBorderSize, 1)}
	return x >= rect.X && x < rect.X+rect.W && y >= rect.Y && y < rect.Y+rect.H
}

func (m *AppModel) isInsideAgentsSection(x, y int) bool {
	if m.viewMode == ViewMemory {
		return false
	}
	rect := m.leftPanelSections.agentsRect
	return rect.W > 0 && rect.H > 0 && x >= rect.X && x < rect.X+rect.W && y >= rect.Y && y < rect.Y+rect.H
}

// updateSelectorHover updates arrow hover state when the cursor moves
// over or away from the model selector line in the agent panel.
func (m *AppModel) updateSelectorHover(mouse tea.MouseMsg) {
	selY := m.agentSelectorScreenY()
	if selY < 0 || mouse.Y != selY {
		m.agentPanel.ClearSelectorHover()
		return
	}
	leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
	contentLeft := 1
	contentRight := leftW - 1
	if mouse.X < contentLeft || mouse.X >= contentRight {
		m.agentPanel.ClearSelectorHover()
		return
	}
	m.agentPanel.HandleSelectorHover(mouse.X - contentLeft)
}

// handleAgentSelectorClick routes a press on the model selector line.
func (m *AppModel) handleAgentSelectorClick(screenX, screenY int) tea.Cmd {
	if m.viewMode == ViewMemory {
		return nil
	}
	selY := m.agentSelectorScreenY()
	if selY < 0 || screenY != selY {
		return nil
	}
	leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
	contentLeft := 1 // left border
	contentRight := leftW - 1
	if screenX < contentLeft || screenX >= contentRight {
		return nil
	}
	localX := screenX - contentLeft
	m.focus.SetFocus(component.FocusAgentPanel)
	m.syncFocusState()
	return m.agentPanel.HandleSelectorClick(localX)
}

func (m *AppModel) handleAgentPanelClick(screenX, screenY int) tea.Cmd {
	if m.viewMode == ViewMemory {
		return nil
	}
	if !m.isInsideAgentsSection(screenX, screenY) {
		return nil
	}
	rect := m.leftPanelSections.agentsRect
	localY := screenY - rect.Y
	if localY < 0 || localY >= rect.H {
		return nil
	}
	m.focus.SetFocus(component.FocusAgentPanel)
	m.syncFocusState()
	return m.agentPanel.HandleListClick(localY)
}

// panelForScroll resolves the panel under screen coordinate x, accounting
// for ring-swapped panels that PanelAtX doesn't know about.
// In SingleColumn mode PanelAtX has no candidates and always returns false,
// so we fall back to the active leftRing panel. In TwoColumn/ThreeColumn
// modes the fixed candidate IDs are mapped to the current ring occupant.
func (m *AppModel) panelForScroll(x, y int) (component.FocusID, bool) {
	if panelID, ok := m.overlayPanelForScroll(x, y); ok {
		return panelID, true
	}
	mode := m.layout.Mode()
	if mode == layout.SingleColumn {
		return m.singleColumnPanelForScroll(x, y)
	}
	return m.multiColumnPanelForScroll(x, y, mode)
}

func (m *AppModel) overlayPanelForScroll(x, y int) (component.FocusID, bool) {
	if !m.isInsideCodePanel(x, y) {
		return 0, false
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return pane.PaneFocusID(m.mergeDiffView.FocusedPane()), true
	}
	if m.diffViewActive && m.diffView != nil {
		return pane.PaneFocusID(m.diffView.FocusedPane()), true
	}
	if m.conflictViewActive && m.conflictView != nil {
		return component.FocusConflictView, true
	}
	return 0, false
}

func (m *AppModel) singleColumnPanelForScroll(x, y int) (component.FocusID, bool) {
	if panelID, ok := m.singleColumnOverlayPanelForScroll(); ok {
		return panelID, true
	}
	if m.leftRing.empty() {
		return 0, false
	}
	current := m.leftRing.current()
	if current != component.FocusSessionPanel && current != component.FocusAgentPanel {
		return current, true
	}
	if m.viewMode == ViewMemory {
		return component.FocusSessionPanel, true
	}
	if m.isInsideAgentsSection(x, y) {
		return component.FocusAgentPanel, true
	}
	if m.isInsideLeftPanelContent(x, y) {
		return component.FocusSessionPanel, true
	}
	return current, true
}

func (m *AppModel) singleColumnOverlayPanelForScroll() (component.FocusID, bool) {
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		if m.singleColumnFileTreeVisible() {
			return component.FocusMergeDiffFileList, true
		}
		return component.FocusMergeDiffView, true
	}
	if m.diffViewActive && m.diffView != nil {
		if m.singleColumnFileTreeVisible() {
			return component.FocusDiffFileList, true
		}
		return component.FocusDiffView, true
	}
	if m.conflictViewActive && m.conflictView != nil {
		if m.singleColumnFileTreeVisible() {
			return component.FocusConflictFileList, true
		}
		return component.FocusConflictView, true
	}
	return 0, false
}

func (m *AppModel) singleColumnFileTreeVisible() bool {
	return !m.leftRing.empty() && m.leftRing.current() == component.FocusFileTree
}

func (m *AppModel) multiColumnPanelForScroll(x, y int, mode layout.LayoutMode) (component.FocusID, bool) {
	panelID, ok := m.layout.PanelAtX(x)
	if !ok {
		return 0, false
	}
	resolved := m.resolveRingPanel(panelID, mode)
	if panelID == component.FocusSessionPanel {
		if panelID, ok := m.sessionPanelScrollTarget(resolved, x, y); ok {
			return panelID, true
		}
	}
	if panelID, ok := m.overlayFileListPanelForScroll(resolved); ok {
		return panelID, true
	}
	if panelID, ok := m.editCodePanelForScroll(resolved, x, y); ok {
		return panelID, true
	}
	return resolved, true
}

func (m *AppModel) sessionPanelScrollTarget(
	resolved component.FocusID,
	x, y int,
) (component.FocusID, bool) {
	if resolved != component.FocusSessionPanel && resolved != component.FocusAgentPanel {
		return 0, false
	}
	if m.viewMode == ViewMemory {
		return component.FocusSessionPanel, true
	}
	if m.isInsideAgentsSection(x, y) {
		return component.FocusAgentPanel, true
	}
	if m.isInsideLeftPanelContent(x, y) {
		return component.FocusSessionPanel, true
	}
	return 0, false
}

func (m *AppModel) overlayFileListPanelForScroll(resolved component.FocusID) (component.FocusID, bool) {
	if resolved != component.FocusFileTree {
		return 0, false
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return component.FocusMergeDiffFileList, true
	}
	if m.diffViewActive && m.diffView != nil {
		return component.FocusDiffFileList, true
	}
	if m.conflictViewActive && m.conflictView != nil {
		return component.FocusConflictFileList, true
	}
	return 0, false
}

func (m *AppModel) editCodePanelForScroll(
	resolved component.FocusID,
	x, y int,
) (component.FocusID, bool) {
	if resolved != component.FocusCodeViewer || m.viewMode != ViewEdit || m.paneTree == nil {
		return 0, false
	}
	if m.isFullMdPreview() {
		return pane.PaneFocusID(m.mdPreviewPane), true
	}
	if pid, ok := m.hitTestPane(x, y); ok {
		return pane.PaneFocusID(pid), true
	}
	return 0, false
}

// resolveRingPanel maps a fixed candidate panel ID to the actual panel
// currently showing in that slot due to ring cycling.
func (m *AppModel) resolveRingPanel(id component.FocusID, mode layout.LayoutMode) component.FocusID {
	switch mode {
	case layout.ThreeColumn:
		if id == component.FocusSessionPanel && !m.leftRing.empty() {
			return m.leftRing.current()
		}
	case layout.TwoColumn:
		if id == component.FocusSessionPanel && !m.leftRing.empty() {
			return m.leftRing.current()
		}
		if id == component.FocusChat && !m.rightRing.empty() {
			return m.rightRing.current()
		}
	}
	return id
}

// applyScrollImpulse adds displacement and velocity to the spring for the
// panel at screen coordinates (x, y). The velocity kick ensures the first
// frame crosses an integer line boundary, giving immediate visual feedback.
// Switching panels resets the spring to avoid cross-panel drift.
func (m *AppModel) applyScrollImpulse(x, y int, impulse float64) {
	panelID, ok := m.panelForScroll(x, y)
	if !ok {
		return
	}
	// In edit mode, scroll the inline editor instead of the code viewer.
	// The spring pipeline dispatches to editor.ScrollUp/ScrollDown via scrollOneLine.
	if panelID != m.scroll.panel {
		m.scroll = scrollState{panel: panelID}
		m.bounce = bounceState{panel: panelID}
	}
	m.scroll.target += impulse
	m.scroll.vel += impulse * scrollKick

	// Cap target lead to prevent runaway coast.
	m.scroll.target = clampFloat(m.scroll.target,
		m.scroll.pos-scrollMaxLead,
		m.scroll.pos+scrollMaxLead,
	)
	// Cap velocity to prevent overshoot (B ≤ 0 condition for critically damped).
	maxVel := scrollFrequency * math.Abs(m.scroll.target-m.scroll.pos)
	m.scroll.vel = clampFloat(m.scroll.vel, -maxVel, maxVel)
}

// tickScrollMomentum advances the scroll spring by one frame and applies
// resulting scroll lines. Boundary hits feed the bounce spring.
func (m *AppModel) tickScrollMomentum() {
	s := &m.scroll
	if !s.settled() {
		s.pos, s.vel = m.scrollSpring.Update(s.pos, s.vel, s.target)

		// Snap when close enough to prevent asymptotic crawl.
		if s.settled() {
			s.pos = s.target
			s.vel = 0
		}

		newApplied := int(math.Round(s.pos))
		m.applyScrollDelta(s.panel, newApplied-s.applied)
		s.applied = newApplied
	}

	m.tickBounce()

	// Push bounce offset to panels for rendering.
	m.chat.SetBounceOffset(m.bounceOffset(component.FocusChat))
	m.codePanel.SetBounceOffset(m.bounceOffset(component.FocusCodeViewer))
	// Push pane-specific bounce offsets.
	for id, ps := range m.paneEditors {
		ps.editor.SetBounceOffset(m.bounceOffset(pane.PaneFocusID(id)))
	}
	if m.previewPane != 0 {
		m.previewPanel.SetBounceOffset(m.bounceOffset(pane.PaneFocusID(m.previewPane)))
	}
	m.fileTree.SetBounceOffset(m.bounceOffset(component.FocusFileTree))
	if m.commitTree != nil {
		m.commitTree.SetBounceOffset(m.bounceOffset(component.FocusCommitTree))
	}
	if m.diffView != nil {
		// Diff pane bounce: match the dynamic pane-level ID from panelForScroll.
		paneBO := m.bounceOffset(pane.PaneFocusID(m.diffView.FocusedPane()))
		if paneBO == 0 {
			paneBO = m.bounceOffset(component.FocusDiffView)
		}
		m.diffView.SetBounceOffset(paneBO)
		// File list bounce: separate from pane bounce.
		m.diffView.SetFileListBounceOffset(m.bounceOffset(component.FocusDiffFileList))
	}
	if m.mergeDiffView != nil {
		paneBO := m.bounceOffset(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
		if paneBO == 0 {
			paneBO = m.bounceOffset(component.FocusMergeDiffView)
		}
		m.mergeDiffView.SetBounceOffset(paneBO)
		m.mergeDiffView.SetFileListBounceOffset(m.bounceOffset(component.FocusMergeDiffFileList))
	}
	if m.conflictView != nil {
		m.conflictView.SetBounceOffset(m.bounceOffset(component.FocusConflictView))
		m.conflictView.SetFileListBounceOffset(m.bounceOffset(component.FocusConflictFileList))
	}
}

// settled reports whether the spring is close enough to target to stop.
func (s *scrollState) settled() bool {
	return math.Abs(s.pos-s.target) < scrollSettlePos &&
		math.Abs(s.vel) < scrollSettleVel
}

// applyScrollDelta dispatches individual line scrolls. When a boundary is
// hit, the remaining delta is converted into bounce impulse.
func (m *AppModel) applyScrollDelta(panelID component.FocusID, delta int) {
	direction := 1
	if delta < 0 {
		direction = -1
		delta = -delta
	}
	for range delta {
		if !m.scrollOneLine(panelID, direction) {
			m.applyBounceImpulse(panelID, direction)
		}
	}
}

type panelScrollRoute func(*AppModel, int) bool

func scrollByDirection(direction int, up, down func() bool) bool {
	if direction < 0 {
		return up()
	}
	return down()
}

func scrollByDirectionCount(direction int, up, down func(int) bool) bool {
	if direction < 0 {
		return up(1)
	}
	return down(1)
}

var panelScrollRoutes = map[component.FocusID]panelScrollRoute{
	component.FocusChat: func(m *AppModel, direction int) bool {
		return scrollByDirection(direction, m.chat.ScrollUp, m.chat.ScrollDown)
	},
	component.FocusConflictFileList: func(m *AppModel, direction int) bool {
		if !m.conflictViewActive || m.conflictView == nil {
			return true
		}
		return scrollByDirection(direction, m.conflictView.ScrollFileListUp, m.conflictView.ScrollFileListDown)
	},
	component.FocusConflictView: func(m *AppModel, direction int) bool {
		if !m.conflictViewActive || m.conflictView == nil {
			return true
		}
		return scrollByDirection(direction, m.conflictView.ScrollUp, m.conflictView.ScrollDown)
	},
	component.FocusMergeDiffFileList: func(m *AppModel, direction int) bool {
		if !m.mergeDiffViewActive || m.mergeDiffView == nil {
			return true
		}
		return scrollByDirection(direction, m.mergeDiffView.ScrollFileListUp, m.mergeDiffView.ScrollFileListDown)
	},
	component.FocusMergeDiffView: func(m *AppModel, direction int) bool {
		if m.mergeDiffView == nil {
			return true
		}
		return scrollByDirection(direction, m.mergeDiffView.ScrollUp, m.mergeDiffView.ScrollDown)
	},
	component.FocusDiffFileList: func(m *AppModel, direction int) bool {
		if !m.diffViewActive || m.diffView == nil {
			return true
		}
		return scrollByDirection(direction, m.diffView.ScrollFileListUp, m.diffView.ScrollFileListDown)
	},
	component.FocusDiffView: func(m *AppModel, direction int) bool {
		if m.diffView == nil {
			return true
		}
		return scrollByDirection(direction, m.diffView.ScrollUp, m.diffView.ScrollDown)
	},
	component.FocusCodeViewer: func(m *AppModel, direction int) bool {
		return m.scrollCodeViewerOneLine(direction)
	},
	component.FocusFileTree: func(m *AppModel, direction int) bool {
		return m.scrollFileTreeOneLine(direction)
	},
	component.FocusSessionPanel: func(*AppModel, int) bool {
		return false
	},
	component.FocusAgentPanel: func(m *AppModel, direction int) bool {
		return scrollByDirection(direction, m.agentPanel.ScrollUp, m.agentPanel.ScrollDown)
	},
	component.FocusGitPanel: func(m *AppModel, direction int) bool {
		if m.gitPanel == nil {
			return true
		}
		return scrollByDirection(direction, m.gitPanel.ScrollUp, m.gitPanel.ScrollDown)
	},
	component.FocusCommitTree: func(m *AppModel, direction int) bool {
		if m.commitTree == nil {
			return true
		}
		return scrollByDirection(direction, m.commitTree.ScrollUp, m.commitTree.ScrollDown)
	},
}

func (m *AppModel) scrollCodeViewerOneLine(direction int) bool {
	if m.conflictViewActive && m.conflictView != nil {
		return scrollByDirection(direction, m.conflictView.ScrollUp, m.conflictView.ScrollDown)
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return scrollByDirection(direction, m.mergeDiffView.ScrollUp, m.mergeDiffView.ScrollDown)
	}
	if m.diffViewActive && m.diffView != nil {
		return scrollByDirection(direction, m.diffView.ScrollUp, m.diffView.ScrollDown)
	}
	if m.viewMode == ViewGit && m.commitTree != nil {
		return scrollByDirection(direction, m.commitTree.ScrollUp, m.commitTree.ScrollDown)
	}
	return scrollByDirection(direction, m.codePanel.ScrollUp, m.codePanel.ScrollDown)
}

func (m *AppModel) scrollFileTreeOneLine(direction int) bool {
	if m.viewMode == ViewGit && m.gitPanel != nil {
		return scrollByDirection(direction, m.gitPanel.ScrollUp, m.gitPanel.ScrollDown)
	}
	return scrollByDirection(direction, m.fileTree.ScrollUp, m.fileTree.ScrollDown)
}

func (m *AppModel) scrollPaneOneLine(panelID component.FocusID, direction int) bool {
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return scrollByDirection(direction, m.mergeDiffView.ScrollUp, m.mergeDiffView.ScrollDown)
	}
	if m.diffViewActive && m.diffView != nil {
		return scrollByDirection(direction, m.diffView.ScrollUp, m.diffView.ScrollDown)
	}

	pid := pane.PaneIDFromFocus(panelID)
	if pid == m.previewPane {
		return scrollByDirectionCount(direction, m.previewPanel.ScrollUp, m.previewPanel.ScrollDown)
	}
	if pid == m.mdPreviewPane {
		return scrollByDirectionCount(direction, m.mdPreviewPanel.ScrollUp, m.mdPreviewPanel.ScrollDown)
	}
	if ps, ok := m.paneEditors[pid]; ok {
		return scrollByDirection(direction, ps.editor.ScrollUp, ps.editor.ScrollDown)
	}
	return true
}

// scrollOneLine scrolls the identified panel by one line in the given direction.
// Returns true if the scroll was consumed, false if the panel hit a boundary.
func (m *AppModel) scrollOneLine(panelID component.FocusID, direction int) bool {
	if handler, ok := panelScrollRoutes[panelID]; ok {
		return handler(m, direction)
	}
	if pane.IsPaneFocus(panelID) {
		return m.scrollPaneOneLine(panelID, direction)
	}
	return true
}

// applyBounceImpulse adds velocity to the bounce spring when a scroll
// boundary is hit. Switching panels resets the bounce state.
func (m *AppModel) applyBounceImpulse(panelID component.FocusID, direction int) {
	if panelID != m.bounce.panel {
		m.bounce = bounceState{panel: panelID}
	}
	m.bounce.vel += float64(direction) * bounceImpulse
	m.bounce.vel = clampFloat(m.bounce.vel, -bounceMaxVel, bounceMaxVel)
}

// tickBounce advances the bounce spring by one frame toward equilibrium (pos=0).
// The underdamped spring naturally oscillates past 0, creating the bounce-back.
func (m *AppModel) tickBounce() {
	b := &m.bounce
	if m.bounceSettled() {
		b.pos = 0
		b.vel = 0
		return
	}

	b.pos, b.vel = m.bounceSpring.Update(b.pos, b.vel, 0)
	b.pos = clampFloat(b.pos, -bounceMaxLines, bounceMaxLines)

	if m.bounceSettled() {
		b.pos = 0
		b.vel = 0
	}
}

// bounceSettled reports whether the bounce spring is close enough to
// equilibrium (pos=0, vel=0) to stop.
func (m *AppModel) bounceSettled() bool {
	return math.Abs(m.bounce.pos) < bounceSettleThreshold &&
		math.Abs(m.bounce.vel) < bounceSettleThreshold
}

// bounceOffset returns the integer line displacement for the given panel.
// Positive = content shifted up (bouncing off bottom boundary).
// Negative = content shifted down (bouncing off top boundary).
func (m *AppModel) bounceOffset(panelID component.FocusID) int {
	if m.bounce.panel != panelID {
		return 0
	}
	return int(math.Round(m.bounce.pos))
}

// clampFloat constrains v to [lo, hi].
func clampFloat(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(v, hi))
}

// ---------------------------------------------------------------------------
// Horizontal swipe (ring cycling via mouse wheel)
// ---------------------------------------------------------------------------

// applySwipeImpulse accumulates horizontal scroll delta and triggers a
// view slot cycle (identical to Alt+V chord) when the accumulated magnitude
// reaches swipeThreshold. In TwoColumn mode both rings cycle in lockstep;
// in other modes the single active ring cycles.
func (m *AppModel) applySwipeImpulse(x int, delta float64) {
	now := time.Now()
	if now.Before(m.swipe.cooldownUntil) {
		return
	}
	if m.leftRing.empty() && m.rightRing.empty() {
		return
	}
	m.swipe.accum += delta
	m.swipe.stamp = now
	if math.Abs(m.swipe.accum) >= swipeThreshold {
		direction := 1
		if m.swipe.accum < 0 {
			direction = -1
		}
		m.cycleViewSlot(direction)
		m.swipe.accum = 0
		m.swipe.cooldownUntil = now.Add(swipeCooldown)
	}
}

// tickSwipeDecay resets stale swipe accumulation after swipeDecay elapses
// since the last horizontal scroll event.
func (m *AppModel) tickSwipeDecay() {
	if m.swipe.accum != 0 && time.Since(m.swipe.stamp) > swipeDecay {
		m.swipe.accum = 0
	}
}

// isFileTreeVisible reports whether the FileTree panel is currently rendered on screen.
func (m *AppModel) isFileTreeVisible() bool {
	if m.layout.Mode() == layout.FourColumn {
		return true
	}
	return m.leftRing.current() == component.FocusFileTree
}

// fileTreePanelX returns the screen X offset of the file tree panel's left edge.
func (m *AppModel) fileTreePanelX() int {
	if m.layout.Mode() == layout.FourColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		return leftW
	}
	return 0
}

// handleFileTreeClick dispatches a click inside the file tree panel, activating
// the entry or search result at the clicked row.
func (m *AppModel) handleFileTreeClick(x, y int) tea.Cmd {
	if !m.isFileTreeVisible() {
		return nil
	}
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	if treeW == 0 || treeH == 0 {
		return nil
	}

	treeX := m.fileTreePanelX()
	innerH := max(treeH-panelBorderSize, 0)

	contentLeft := treeX + 1
	contentRight := treeX + treeW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight {
		return nil
	}
	if y < contentTop || y >= contentBottom {
		return nil
	}

	viewX := x - contentLeft
	viewY := y - contentTop
	return m.fileTree.ClickAt(viewX, viewY)
}

// handleGitPanelClick dispatches a click inside the git panel (occupies the
// FileTree slot in git mode).
func (m *AppModel) handleGitPanelClick(x, y int) tea.Cmd {
	if m.gitPanel == nil {
		return nil
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	if panelW == 0 || panelH == 0 {
		return nil
	}
	panelX := m.fileTreePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		// Click outside git panel — close any open gear dropdown.
		m.gitPanel.CloseDropdown()
		return nil
	}

	viewX := x - contentLeft
	viewY := y - contentTop
	m.focus.SetFocus(component.FocusGitPanel)
	m.syncFocusState()
	cmd := m.gitPanel.ClickAt(viewX, viewY)
	m.syncStagedFiles()
	return cmd
}

// handleDiffFileListClick dispatches a click inside the diff file list panel
// (occupies the FileTree slot when the diff view is active). Sets focus and
// opens the clicked file as a tab if the click falls within the panel bounds.
func (m *AppModel) handleDiffFileListClick(x, y int) {
	if m.diffView == nil {
		return
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	if panelW == 0 || panelH == 0 {
		return
	}
	panelX := m.fileTreePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		return
	}

	localY := y - contentTop
	m.focus.SetFocus(component.FocusDiffFileList)
	m.syncFocusState()
	m.diffView.ClickFileList(localY)
}

// handleMergeDiffFileListClick dispatches a click inside the merge diff file list.
func (m *AppModel) handleMergeDiffFileListClick(x, y int) {
	if m.mergeDiffView == nil {
		return
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	if panelW == 0 || panelH == 0 {
		return
	}
	panelX := m.fileTreePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		return
	}

	localY := y - contentTop
	m.focus.SetFocus(component.FocusMergeDiffFileList)
	m.syncFocusState()
	m.mergeDiffView.ClickFileList(localY)
}

// handleConflictFileListClick dispatches a click inside the conflict file list.
func (m *AppModel) handleConflictFileListClick(x, y int) {
	if m.conflictView == nil {
		return
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusFileTree)
	if panelW == 0 || panelH == 0 {
		return
	}
	panelX := m.fileTreePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		return
	}

	localY := y - contentTop
	m.focus.SetFocus(component.FocusConflictFileList)
	m.syncFocusState()
	m.conflictView.ClickFileList(localY)
}

// handleCommitTreeClick dispatches a click inside the commit tree panel
// (occupies the CodeViewer slot in git mode).
func (m *AppModel) handleCommitTreeClick(x, y int) tea.Cmd {
	if m.commitTree == nil {
		return nil
	}
	panelW, panelH := m.layout.GetPanelSize(component.FocusCodeViewer)
	if panelW == 0 || panelH == 0 {
		return nil
	}
	panelX := m.codePanelX()
	innerH := max(panelH-panelBorderSize, 0)

	contentLeft := panelX + 1
	contentRight := panelX + panelW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight || y < contentTop || y >= contentBottom {
		return nil
	}

	viewX := x - contentLeft
	viewY := y - contentTop
	m.focus.SetFocus(component.FocusCommitTree)
	m.syncFocusState()
	cmd := m.commitTree.ClickAt(viewX, viewY)
	return cmd
}

// isChatVisible reports whether the Chat panel is currently rendered on screen.
func (m *AppModel) isChatVisible() bool {
	mode := m.layout.Mode()
	if mode >= layout.ThreeColumn {
		return true
	}
	if mode == layout.TwoColumn {
		return m.rightRing.current() == component.FocusChat
	}
	return m.leftRing.current() == component.FocusChat
}

// handleChatClick toggles expandable chat rows or copies the clicked content
// to the system clipboard.
func (m *AppModel) handleChatClick(x, y int) tea.Cmd {
	if !m.isChatVisible() {
		return nil
	}
	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	if chatW == 0 || chatH == 0 {
		return nil
	}

	chatX := m.chatPanelX()
	innerH := max(chatH-panelBorderSize, 0)

	// Content area within the bordered chat panel.
	contentLeft := chatX + 1
	contentRight := chatX + chatW - 1

	// Chord hint pushes viewport content down by 2 lines when active.
	chordOffset := 0
	if m.chord != chordNone {
		chordOffset = 2
	}
	viewportTop := 1 + chordOffset // 1 = top border
	contentBottom := 1 + innerH

	if x < contentLeft || x >= contentRight {
		return nil
	}
	if y < viewportTop || y >= contentBottom {
		return nil
	}

	viewportLine := y - viewportTop
	if m.chat.ToggleAtRenderedViewLine(viewportLine) {
		m.markSlotDirty(m.chatSlot())
		m.viewDirty = true
		return nil
	}
	target := m.chat.CopyTargetAtRenderedViewLine(viewportLine)
	if target == nil {
		return nil
	}

	if err := m.clipboard.Set(target.Content); err != nil {
		m.statusBar.SetFlash("Copy failed")
		m.markSlotDirty(compositor.SlotStatus)
		m.viewDirty = true
		return nil
	}
	m.chat.SetHighlight(target.EntryID, target.EntryIndex, target.HighlightStart, target.HighlightEnd)
	m.statusBar.SetFlash("Copied!")
	m.markSlotDirty(m.chatSlot())
	m.markSlotDirty(compositor.SlotStatus)
	m.viewDirty = true
	return nil
}

// chatPanelX returns the X coordinate where the chat panel starts,
// based on the current layout mode.
func (m *AppModel) chatPanelX() int {
	mode := m.layout.Mode()
	if mode == layout.FourColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		treeW, _ := m.layout.GetPanelSize(component.FocusFileTree)
		return leftW + treeW
	}
	if mode >= layout.TwoColumn {
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		return leftW
	}
	return 0
}

// codePanelX returns the X coordinate where the code panel starts,
// based on the current layout mode.
func (m *AppModel) codePanelX() int {
	mode := m.layout.Mode()
	switch mode {
	case layout.FourColumn:
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		treeW, _ := m.layout.GetPanelSize(component.FocusFileTree)
		chatW, _ := m.layout.GetPanelSize(component.FocusChat)
		return leftW + treeW + chatW
	case layout.ThreeColumn:
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		chatW, _ := m.layout.GetPanelSize(component.FocusChat)
		return leftW + chatW
	case layout.TwoColumn:
		leftW, _ := m.layout.GetPanelSize(component.FocusSessionPanel)
		return leftW
	default:
		return 0
	}
}

// hasPreview reports whether a preview pane is currently active.
func (m *AppModel) hasPreview() bool {
	return m.previewPane != 0
}

// isFullMdPreview reports whether the markdown preview is taking the full
// code panel (the sole editor pane has no tabs).
func (m *AppModel) isFullMdPreview() bool {
	return m.mdPreviewPane != 0 && len(m.paneEditors) == 1 && len(m.focusedTabOrder()) == 0
}

// focusedEditor returns the editor for the currently focused pane.
func (m *AppModel) focusedEditor() *editor.Model {
	return m.paneEditors[m.focusedPane].editor
}

// focusedTabOrder returns the tab order for the currently focused pane.
func (m *AppModel) focusedTabOrder() []string {
	return m.paneEditors[m.focusedPane].tabOrder
}

// setFocusedTabOrder replaces the tab order for the currently focused pane.
func (m *AppModel) setFocusedTabOrder(to []string) {
	m.paneEditors[m.focusedPane].tabOrder = to
}

// paneEditor returns the editor for the given pane ID.
func (m *AppModel) paneEditor(id pane.PaneID) *editor.Model {
	return m.paneEditors[id].editor
}

// paneTabOrder returns the tab order for the given pane ID.
func (m *AppModel) paneTabOrder(id pane.PaneID) []string {
	return m.paneEditors[id].tabOrder
}

// allTabOrders returns all open file paths across all editor panes.
func (m *AppModel) allTabOrders() []string {
	var all []string
	for _, ps := range m.paneEditors {
		all = append(all, ps.tabOrder...)
	}
	return all
}

// anyPaneHasTabs reports whether any editor pane has open tabs.
func (m *AppModel) anyPaneHasTabs() bool {
	for _, ps := range m.paneEditors {
		if len(ps.tabOrder) > 0 {
			return true
		}
	}
	return false
}

// previewSplitWidth returns the preview half width in split mode.
// Returns 0 when not in split mode or preview is inactive.
func (m *AppModel) previewSplitWidth() int {
	if !m.hasPreview() || m.viewMode != ViewEdit || len(m.focusedTabOrder()) == 0 {
		return 0
	}
	rightW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	// Derived from: 1 divider char, remaining space split evenly.
	dividerWidth := 1
	return (innerW - dividerWidth) / 2
}

// isInsidePreviewHalf reports whether screen x falls within the preview
// half of the code panel in split view.
func (m *AppModel) isInsidePreviewHalf(x int) bool {
	pw := m.previewSplitWidth()
	if pw == 0 {
		return false
	}
	codeX := m.codePanelX()
	contentLeft := codeX + 1 // skip left border
	return x >= contentLeft && x < contentLeft+pw
}

// paneContentArea returns the inner area used by ComputeLayout for panes.
func (m *AppModel) paneContentArea() pane.Rect {
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)
	contentH := max(innerH-1, 1) // minus status line
	return pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
}

// hitTestPane returns the PaneID under screen coordinates (screenX, screenY)
// using the pane tree's ComputeLayout. Converts screen coords to content-local
// before testing against pane rects.
func (m *AppModel) hitTestPane(screenX, screenY int) (pane.PaneID, bool) {
	if m.paneTree == nil {
		return 0, false
	}
	codeX := m.codePanelX()
	viewX := screenX - (codeX + 1) // skip left border
	viewY := screenY - 1           // skip top border

	area := m.paneContentArea()
	rects := m.paneTree.ComputeLayout(area)
	for id, r := range rects {
		if viewX >= r.X && viewX < r.X+r.W && viewY >= r.Y && viewY < r.Y+r.H {
			return id, true
		}
	}
	return 0, false
}

// paneViewCoords converts screen coordinates to pane-local coordinates.
// Returns (paneID, localX, localY, ok). localX/localY are relative to the
// pane's top-left corner within the code panel content area.
func (m *AppModel) paneViewCoords(screenX, screenY int) (pane.PaneID, int, int, bool) {
	if m.paneTree == nil {
		return 0, 0, 0, false
	}
	codeX := m.codePanelX()
	viewX := screenX - (codeX + 1)
	viewY := screenY - 1

	area := m.paneContentArea()
	rects := m.paneTree.ComputeLayout(area)
	for id, r := range rects {
		if viewX >= r.X && viewX < r.X+r.W && viewY >= r.Y && viewY < r.Y+r.H {
			return id, viewX - r.X, viewY - r.Y, true
		}
	}
	return 0, 0, 0, false
}

func (m *AppModel) paneRect(id pane.PaneID) (pane.Rect, bool) {
	if m.paneTree == nil {
		return pane.Rect{}, false
	}
	rects := m.paneTree.ComputeLayout(m.paneContentArea())
	rect, ok := rects[id]
	return rect, ok
}

// handleEditorMouse handles all left-button mouse actions (press, drag,
// release) inside the code panel during edit mode. Returns (consumed, cmd).
func (m *AppModel) handleEditorMouse(mouse tea.MouseMsg) (bool, tea.Cmd) {
	if mouse.Action == tea.MouseActionRelease {
		return m.handleEditorMouseRelease()
	}
	if consumed, cmd := m.handleFullPreviewMouse(mouse); consumed {
		return true, cmd
	}
	if consumed, cmd := m.handleFullMarkdownPreviewMouse(mouse); consumed {
		return true, cmd
	}
	if consumed, cmd := m.handleTabDragMouseMotion(mouse); consumed {
		return true, cmd
	}
	if consumed, cmd := m.handleEditorDragMouseMotion(mouse); consumed {
		return true, cmd
	}
	if mouse.Action != tea.MouseActionPress {
		return false, nil
	}
	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return false, nil
	}
	if m.paneTree != nil && !m.paneTree.IsLeaf() {
		return m.handleMultiPanePress(mouse)
	}
	if consumed, cmd := m.handlePreviewSplitPress(mouse); consumed {
		return true, cmd
	}
	return m.handleEditorPaneClick(mouse)
}

func (m *AppModel) handleEditorMouseRelease() (bool, tea.Cmd) {
	cmd := m.finalizeCrossPaneDrop()
	m.editorMouseDown = false
	m.editorDragging = false
	m.inputMouseDown = false
	m.tabDragIdx = -1
	m.tabDragSourcePane = 0
	m.tabDropTarget = 0
	return true, cmd
}

func (m *AppModel) handleFullPreviewMouse(mouse tea.MouseMsg) (bool, tea.Cmd) {
	if !m.hasPreview() || len(m.focusedTabOrder()) != 0 {
		return false, nil
	}
	if mouse.Action == tea.MouseActionPress && m.isInsideCodePanel(mouse.X, mouse.Y) {
		viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
		if viewY < tabbar.Height {
			codeW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
			cfg := tabbar.Config{
				Tabs: []tabbar.Tab{{
					Path:        m.previewPanel.FilePath(),
					Modified:    false,
					LabelPrefix: "Preview: ",
				}},
				Active:    0,
				Width:     max(codeW-panelBorderSize, 1),
				NerdFonts: m.nerdFontsDetected,
				Theme:     m.config.Theme(),
			}
			hit := tabbar.HitTest(cfg, viewX)
			if hit.TabIndex >= 0 && hit.IsClose {
				m.dismissPreview()
				return true, nil
			}
		} else {
			m.previewPanel.SetCursorFromViewport(viewX, viewY-tabbar.Height)
		}
		m.focus.SetFocus(pane.PaneFocusID(m.previewPane))
		m.syncFocusState()
	}
	return true, nil
}

func (m *AppModel) handleFullMarkdownPreviewMouse(mouse tea.MouseMsg) (bool, tea.Cmd) {
	if !m.isFullMdPreview() {
		return false, nil
	}
	if mouse.Action == tea.MouseActionPress && m.isInsideCodePanel(mouse.X, mouse.Y) {
		viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
		if viewY < tabbar.Height {
			codeW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
			cfg := tabbar.Config{
				Tabs: []tabbar.Tab{{
					Path:        m.mdPreviewPanel.FilePath(),
					Modified:    false,
					LabelPrefix: "Markdown: ",
				}},
				Active:    0,
				Width:     max(codeW-panelBorderSize, 1),
				NerdFonts: m.nerdFontsDetected,
				Theme:     m.config.Theme(),
			}
			hit := tabbar.HitTest(cfg, viewX)
			if hit.TabIndex >= 0 && hit.IsClose {
				m.dismissMarkdownPreview()
				return true, nil
			}
		}
		m.focus.SetFocus(pane.PaneFocusID(m.mdPreviewPane))
		m.syncFocusState()
	}
	return true, nil
}

func (m *AppModel) handleTabDragMouseMotion(mouse tea.MouseMsg) (bool, tea.Cmd) {
	if mouse.Action != tea.MouseActionMotion || m.tabDragIdx < 0 {
		return false, nil
	}
	m.tabDropTarget = 0
	if m.paneTree != nil && !m.paneTree.IsLeaf() {
		pid, localX, _, ok := m.paneViewCoords(mouse.X, mouse.Y)
		if !ok {
			return true, nil
		}
		if pid == m.tabDragSourcePane && pid != m.previewPane {
			return m.handleTabDragReorder(localX)
		}
		if pid != m.tabDragSourcePane && pid != m.previewPane {
			if _, isEditor := m.paneEditors[pid]; isEditor {
				m.tabDropTarget = pid
			}
		}
		return true, nil
	}
	if m.tabDragSourcePane == m.previewPane && !m.isInsidePreviewHalf(mouse.X) {
		m.tabDropTarget = m.focusedPane
		return true, nil
	}
	viewX, _ := m.focusedPaneLocalCoords(mouse.X, mouse.Y)
	return m.handleTabDragReorder(viewX)
}

func (m *AppModel) handleEditorDragMouseMotion(mouse tea.MouseMsg) (bool, tea.Cmd) {
	if mouse.Action != tea.MouseActionMotion || !m.editorMouseDown {
		return false, nil
	}
	if m.focusedEditor().HasMultiCursor() {
		return true, nil
	}
	viewX, viewY := m.focusedPaneLocalCoords(mouse.X, mouse.Y)
	if len(m.focusedTabOrder()) > 0 {
		viewY -= tabbar.Height
	}
	viewY -= m.focusedEditor().FindBarHeight()
	if !m.editorDragging {
		m.focusedEditor().StartDragSelection()
		m.editorDragging = true
	}
	m.focusedEditor().ExtendDragSelection(viewX, viewY)
	return true, nil
}

func (m *AppModel) handlePreviewSplitPress(mouse tea.MouseMsg) (bool, tea.Cmd) {
	if m.isInsidePreviewHalf(mouse.X) {
		viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
		if viewY < tabbar.Height {
			cfg := tabbar.Config{
				Tabs: []tabbar.Tab{{
					Path:        m.previewPanel.FilePath(),
					Modified:    false,
					LabelPrefix: "Preview: ",
				}},
				Active:     0,
				Width:      m.previewSplitWidth(),
				NerdFonts:  m.nerdFontsDetected,
				Theme:      m.config.Theme(),
				HoverClose: -1,
			}
			hit := tabbar.HitTest(cfg, viewX)
			if hit.TabIndex >= 0 && hit.IsClose {
				m.dismissPreview()
				return true, nil
			}
			// Begin preview tab drag.
			if hit.TabIndex >= 0 {
				m.tabDragIdx = 0
				m.tabDragSourcePane = m.previewPane
			}
		} else {
			// Content click: position the cursor.
			m.previewPanel.SetCursorFromViewport(viewX, viewY-tabbar.Height)
		}
		m.focus.SetFocus(pane.PaneFocusID(m.previewPane))
		m.syncFocusState()
		return true, nil
	}
	return false, nil
}

// handleMultiPanePress routes a press event to the correct pane in multi-pane
// mode using tree-based hit testing.
func (m *AppModel) handleMultiPanePress(mouse tea.MouseMsg) (bool, tea.Cmd) {
	pid, localX, localY, ok := m.paneViewCoords(mouse.X, mouse.Y)
	if !ok {
		return false, nil
	}

	m.focusPane(pid)

	if pid == m.previewPane {
		return m.handleSplitPreviewPanePress(pid, localX, localY)
	}

	if pid == m.mdPreviewPane {
		return m.handleSplitMarkdownPreviewPanePress(pid, localX, localY)
	}

	ps, exists := m.paneEditors[pid]
	if !exists {
		return true, nil
	}
	return m.handleSplitEditorPanePress(pid, localX, localY, ps)
}

func (m *AppModel) focusPane(pid pane.PaneID) {
	m.focus.SetFocus(pane.PaneFocusID(pid))
	m.syncFocusState()
}

func (m *AppModel) handleSplitPreviewPanePress(pid pane.PaneID, localX, localY int) (bool, tea.Cmd) {
	if localY < tabbar.Height {
		return m.handleSplitPreviewTabPress(pid, localX, "Preview: ", m.previewPanel.FilePath(), m.dismissPreview, true)
	}
	m.previewPanel.SetCursorFromViewport(localX, localY-tabbar.Height)
	return true, nil
}

func (m *AppModel) handleSplitMarkdownPreviewPanePress(pid pane.PaneID, localX, localY int) (bool, tea.Cmd) {
	if localY < tabbar.Height {
		return m.handleSplitPreviewTabPress(pid, localX, "Markdown: ", m.mdPreviewPanel.FilePath(), m.dismissMarkdownPreview, false)
	}
	return true, nil
}

func (m *AppModel) handleSplitPreviewTabPress(
	pid pane.PaneID,
	localX int,
	labelPrefix string,
	path string,
	dismiss func(),
	allowDrag bool,
) (bool, tea.Cmd) {
	rect, ok := m.paneRect(pid)
	if !ok {
		return true, nil
	}
	cfg := tabbar.Config{
		Tabs: []tabbar.Tab{{
			Path:        path,
			Modified:    false,
			LabelPrefix: labelPrefix,
		}},
		Active:    0,
		Width:     rect.W,
		NerdFonts: m.nerdFontsDetected,
		Theme:     m.config.Theme(),
	}
	hit := tabbar.HitTest(cfg, localX)
	if hit.TabIndex >= 0 && hit.IsClose {
		dismiss()
		return true, nil
	}
	if allowDrag && hit.TabIndex >= 0 {
		m.tabDragIdx = 0
		m.tabDragSourcePane = m.previewPane
	}
	return true, nil
}

func (m *AppModel) handleSplitEditorPanePress(pid pane.PaneID, localX, localY int, ps *editorPaneState) (bool, tea.Cmd) {
	if m.handleMarkdownTooltipPress(pid, localX, localY, ps.tabOrder) {
		return true, nil
	}
	if len(ps.tabOrder) > 0 && localY < tabbar.Height {
		return m.handleSplitEditorTabPress(pid, localX, ps)
	}
	return m.handleSplitEditorContentPress(localX, localY, ps)
}

func (m *AppModel) handleSplitEditorTabPress(pid pane.PaneID, localX int, ps *editorPaneState) (bool, tea.Cmd) {
	rect, ok := m.paneRect(pid)
	if !ok {
		return true, nil
	}
	hit := tabbar.HitTest(m.paneTabBarConfig(pid, rect.W), localX)
	if hit.IsLeftNav {
		m.tabArrowFlashLeftUntil = time.Now().Add(tabArrowFlashDuration)
		return true, m.prevTab()
	}
	if hit.IsRightNav {
		m.tabArrowFlashRightUntil = time.Now().Add(tabArrowFlashDuration)
		return true, m.nextTab()
	}
	if hit.TabIndex < 0 || hit.TabIndex >= len(ps.tabOrder) {
		return true, nil
	}
	if hit.IsClose {
		return true, m.closeTab(hit.TabIndex)
	}
	m.tabDragIdx = hit.TabIndex
	m.tabDragSourcePane = pid
	return true, m.switchToTab(hit.TabIndex)
}

func (m *AppModel) handleSplitEditorContentPress(localX, localY int, ps *editorPaneState) (bool, tea.Cmd) {
	viewY := localY
	if len(ps.tabOrder) > 0 {
		viewY -= tabbar.Height
	}
	return m.handleEditorPaneBodyClick(ps.editor, localX, viewY)
}

// handleEditorPaneClick handles a click in the single editor pane (no splits).
func (m *AppModel) handleEditorPaneClick(mouse tea.MouseMsg) (bool, tea.Cmd) {
	viewX, viewY := m.singlePaneEditorClickCoords(mouse)
	if m.tabBarHeight() > 0 && viewY < m.tabBarHeight() {
		return m.handleTabBarClick(viewX)
	}
	if m.handleMarkdownTooltipPress(m.focusedPane, viewX, viewY, m.focusedTabOrder()) {
		return true, nil
	}
	viewY -= m.tabBarHeight()
	return m.handleEditorPaneBodyClick(m.focusedEditor(), viewX, viewY)
}

func (m *AppModel) singlePaneEditorClickCoords(mouse tea.MouseMsg) (int, int) {
	viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
	if pw := m.previewSplitWidth(); pw > 0 {
		viewX -= pw + 1
	}
	return viewX, viewY
}

func (m *AppModel) handleMarkdownTooltipPress(pid pane.PaneID, viewX, viewY int, tabs []string) bool {
	if m.mdTooltipTab < 0 || m.mdTooltipPane != pid {
		return false
	}
	if viewY < tabbar.Height || viewY >= tabbar.Height+mdTooltipHeight {
		return false
	}
	if viewX < m.mdTooltipX || viewX >= m.mdTooltipX+mdTooltipWidth {
		return false
	}
	if m.mdTooltipTab < len(tabs) {
		m.openMarkdownPreview(tabs[m.mdTooltipTab])
		m.mdTooltipTab = -1
		m.mdTooltipPane = 0
	}
	return true
}

func (m *AppModel) handleEditorPaneBodyClick(ed *editor.Model, viewX, viewY int) (bool, tea.Cmd) {
	if m.handleEditorBarClick(ed, viewX, viewY) {
		return true, nil
	}
	viewY -= m.editorBarHeight(ed)
	if handled, cmd := m.handleEditorOverlayClick(ed, viewX, viewY); handled {
		return true, cmd
	}
	return m.handleEditorContentPress(ed, viewX, viewY)
}

func (m *AppModel) handleEditorBarClick(ed *editor.Model, viewX, viewY int) bool {
	barH := ed.ReplaceBarHeight()
	if barH > 0 && viewY < barH {
		ed.HandleReplaceBarClick(viewX, viewY)
		return true
	}
	if barH == 0 {
		barH = ed.FindBarHeight()
		if barH > 0 && viewY < barH {
			ed.HandleFindBarClick(viewX, viewY)
			return true
		}
	}
	return false
}

func (m *AppModel) editorBarHeight(ed *editor.Model) int {
	if barH := ed.ReplaceBarHeight(); barH > 0 {
		return barH
	}
	return ed.FindBarHeight()
}

func (m *AppModel) handleEditorOverlayClick(ed *editor.Model, viewX, viewY int) (bool, tea.Cmd) {
	if cmd, handled := ed.HandleHoverClick(viewX, viewY); handled {
		return true, cmd
	}
	if ed.HandleCompletionClick(viewX, viewY) {
		return true, nil
	}
	return false, nil
}

func (m *AppModel) handleEditorContentPress(ed *editor.Model, viewX, viewY int) (bool, tea.Cmd) {
	ed.ClickAt(viewX, viewY)
	m.editorMouseDown = true
	m.editorDragging = false
	return true, m.scheduleEditorHighlight(ed)
}

func (m *AppModel) scheduleEditorHighlight(ed *editor.Model) tea.Cmd {
	line := ed.CursorLine()
	col := ed.CursorCol()
	if !ed.IsWordCharAtPos(line, col) {
		ed.ClearHighlightRanges()
		return nil
	}
	m.highlightLine = line
	m.highlightCol = col
	ed.ClearHighlightRanges()
	return tea.Tick(highlightDebounce, func(_ time.Time) tea.Msg {
		return msg.LSPDocHighlightTickMsg{Line: line, Col: col}
	})
}

// isInsideCodePanel reports whether screen coordinates (x, y) fall within
// the content area of the code panel.
func (m *AppModel) isInsideCodePanel(x, y int) bool {
	codeW, codeH := m.layout.GetPanelSize(component.FocusCodeViewer)
	if codeW == 0 || codeH == 0 {
		return false
	}
	codeX := m.codePanelX()
	innerH := max(codeH-panelBorderSize, 0)

	contentLeft := codeX + 1
	contentRight := codeX + codeW - 1
	contentTop := 1
	contentBottom := 1 + innerH

	return x >= contentLeft && x < contentRight && y >= contentTop && y < contentBottom
}

// editorViewCoords converts screen coordinates to content-local viewport
// coordinates for the code panel. Coordinates are clamped to [0, max).
func (m *AppModel) editorViewCoords(screenX, screenY int) (int, int) {
	codeW, codeH := m.layout.GetPanelSize(component.FocusCodeViewer)
	codeX := m.codePanelX()
	innerW := max(codeW-panelBorderSize, 0)
	innerH := max(codeH-panelBorderSize, 0)

	viewX := max(min(screenX-(codeX+1), innerW-1), 0)
	viewY := max(min(screenY-1, innerH-1), 0)
	return viewX, viewY
}

// focusedPaneRect returns the layout rectangle for the currently focused
// editor pane. Returns a zero Rect and false when not in multi-pane mode.
func (m *AppModel) focusedPaneRect() (pane.Rect, bool) {
	if m.paneTree == nil || m.paneTree.IsLeaf() {
		return pane.Rect{}, false
	}
	area := m.paneContentArea()
	rects := m.paneTree.ComputeLayout(area)
	r, ok := rects[m.focusedPane]
	return r, ok
}

// focusedPaneLocalCoords converts screen coordinates to the focused pane's
// local coordinates. In multi-pane mode, coordinates are relative to the
// pane's top-left. In single-pane mode, falls back to editorViewCoords
// with preview offset. Coordinates are clamped for drag safety.
func (m *AppModel) focusedPaneLocalCoords(screenX, screenY int) (int, int) {
	r, ok := m.focusedPaneRect()
	if !ok {
		viewX, viewY := m.editorViewCoords(screenX, screenY)
		if pw := m.previewSplitWidth(); pw > 0 {
			viewX -= pw + 1
		}
		return viewX, viewY
	}
	codeX := m.codePanelX()
	viewX := screenX - (codeX + 1) - r.X
	viewY := screenY - 1 - r.Y
	viewX = max(0, min(viewX, r.W-1))
	viewY = max(0, min(viewY, r.H-1))
	return viewX, viewY
}

// previewPaneLocalCoords converts screen coordinates to the preview pane's
// local coordinates. In multi-pane mode, uses the preview pane's rect from
// ComputeLayout. In single-pane mode, uses the code panel's left half.
func (m *AppModel) previewPaneLocalCoords(screenX, screenY int) (int, int) {
	if m.paneTree != nil && !m.paneTree.IsLeaf() && m.hasPreview() {
		area := m.paneContentArea()
		rects := m.paneTree.ComputeLayout(area)
		if r, ok := rects[m.previewPane]; ok {
			codeX := m.codePanelX()
			viewX := screenX - (codeX + 1) - r.X
			viewY := screenY - 1 - r.Y
			viewX = max(0, min(viewX, r.W-1))
			viewY = max(0, min(viewY, r.H-1))
			return viewX, viewY
		}
	}
	return m.editorViewCoords(screenX, screenY)
}

// hoverDebounce delays before firing an LSP hover request.
// Derived from: 150ms is responsive without flooding the server on fast swipes;
// 50ms retrigger keeps the tooltip "following" the cursor between words.
const (
	hoverInitialDebounce   = 350 * time.Millisecond // first hover when no tooltip is showing
	hoverRetriggerDebounce = 50 * time.Millisecond  // re-trigger when moving between words
)

// highlightDebounce is the delay before firing a documentHighlight request
// after the cursor comes to rest on a symbol.
// Derived from: 100ms feels snappy while still batching rapid j/k navigation.
const highlightDebounce = 100 * time.Millisecond

// handleEditorMouseHover fires a debounced LSP hover request when the
// mouse moves to a new word in edit mode. Only triggers on word characters;
// moving within the same word does not reset the debounce.
func (m *AppModel) handleEditorMouseHover(mouse tea.MouseMsg) tea.Cmd {
	target, ok := m.mouseHoverTarget(mouse)
	if !ok {
		m.dismissAllHover()
		return nil
	}
	if target.preview {
		return m.handlePreviewMouseHover(mouse, target.viewX, target.viewY)
	}
	return m.handleFocusedEditorMouseHover(target.viewX, target.viewY)
}

// handlePreviewMouseHover handles hover detection when the mouse is over
// the preview pane. Uses the preview panel's coordinate conversion and
// word detection methods.
func (m *AppModel) handlePreviewMouseHover(mouse tea.MouseMsg, viewX, viewY int) tea.Cmd {
	if m.previewPanel.FilePath() == "" {
		m.dismissAllHover()
		return nil
	}

	if m.paneTree == nil || m.paneTree.IsLeaf() {
		viewX, viewY = m.singlePanePreviewHoverCoords(mouse)
	}

	if m.previewPanel.HoverActive() && m.previewPanel.IsInsideHoverPopup(viewX, viewY) {
		return nil
	}

	line, col, ok := m.previewPanel.ViewportToBufferPos(viewX, viewY)
	if !ok {
		m.dismissAllHover()
		return nil
	}
	return m.schedulePreviewMouseHover(line, col)
}

type mouseHoverTarget struct {
	viewX   int
	viewY   int
	preview bool
}

func (m *AppModel) mouseHoverTarget(mouse tea.MouseMsg) (mouseHoverTarget, bool) {
	if !m.isInsideCodePanel(mouse.X, mouse.Y) {
		return mouseHoverTarget{}, false
	}
	if m.paneTree != nil && !m.paneTree.IsLeaf() {
		return m.multiPaneMouseHoverTarget(mouse)
	}
	if m.isInsidePreviewHalf(mouse.X) {
		viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
		return mouseHoverTarget{viewX: viewX, viewY: viewY, preview: true}, true
	}
	viewX, viewY := m.focusedPaneLocalCoords(mouse.X, mouse.Y)
	return mouseHoverTarget{viewX: viewX, viewY: viewY}, true
}

func (m *AppModel) multiPaneMouseHoverTarget(mouse tea.MouseMsg) (mouseHoverTarget, bool) {
	pid, localX, localY, ok := m.paneViewCoords(mouse.X, mouse.Y)
	if !ok {
		return mouseHoverTarget{}, false
	}
	if m.hasPreview() && pid == m.previewPane {
		return mouseHoverTarget{
			viewX:   localX,
			viewY:   localY - tabbar.Height,
			preview: true,
		}, true
	}
	if pid != m.focusedPane {
		return mouseHoverTarget{}, false
	}
	return mouseHoverTarget{viewX: localX, viewY: localY}, true
}

func (m *AppModel) handleFocusedEditorMouseHover(viewX, viewY int) tea.Cmd {
	viewY = m.normalizedEditorHoverY(viewY)
	if m.keepEditorHoverActive(viewX, viewY) {
		return nil
	}
	line, col, ok := m.focusedEditor().ViewportToBufferPos(viewX, viewY)
	if !ok {
		m.dismissAllHover()
		return nil
	}
	return m.scheduleEditorMouseHover(line, col)
}

func (m *AppModel) normalizedEditorHoverY(viewY int) int {
	if len(m.focusedTabOrder()) > 0 {
		viewY -= tabbar.Height
	}
	return viewY - m.focusedEditor().FindBarHeight()
}

func (m *AppModel) keepEditorHoverActive(viewX, viewY int) bool {
	if !m.focusedEditor().HoverActive() {
		return false
	}
	return m.focusedEditor().IsInsideHoverPopup(viewX, viewY) ||
		m.focusedEditor().IsInsideOverlayPopup(viewX, viewY)
}

func (m *AppModel) singlePanePreviewHoverCoords(mouse tea.MouseMsg) (int, int) {
	viewX, viewY := m.editorViewCoords(mouse.X, mouse.Y)
	return viewX, viewY - tabbar.Height
}

func (m *AppModel) scheduleEditorMouseHover(line, col int) tea.Cmd {
	if !m.focusedEditor().IsWordCharAtPos(line, col) {
		m.dismissAllHover()
		return nil
	}
	wordStart, _ := m.focusedEditor().WordBoundsAt(line, col)
	return m.scheduleMouseHover(line, col, wordStart, false, m.focusedEditor().HoverActive() || m.previewPanel.HoverActive())
}

func (m *AppModel) schedulePreviewMouseHover(line, col int) tea.Cmd {
	if !m.previewPanel.IsWordCharAtPos(line, col) {
		m.dismissAllHover()
		return nil
	}
	wordStart, _ := m.previewPanel.WordBoundsAt(line, col)
	return m.scheduleMouseHover(line, col, wordStart, true, m.previewPanel.HoverActive() || m.focusedEditor().HoverActive())
}

func (m *AppModel) scheduleMouseHover(line, col, wordStart int, forPreview, hoverActive bool) tea.Cmd {
	if line == m.hoverMouseLine && wordStart == m.hoverMouseWordStart && m.hoverForPreview == forPreview {
		return nil
	}
	debounce := hoverInitialDebounce
	if hoverActive {
		m.dismissAllHover()
		debounce = hoverRetriggerDebounce
	}
	m.recordMouseHover(line, col, wordStart, forPreview)
	return tea.Tick(debounce, func(_ time.Time) tea.Msg {
		return msg.LSPMouseHoverTickMsg{Line: line, Col: col}
	})
}

func (m *AppModel) recordMouseHover(line, col, wordStart int, forPreview bool) {
	m.hoverMouseLine = line
	m.hoverMouseCol = col
	m.hoverMouseWordStart = wordStart
	m.hoverForPreview = forPreview
	m.pendingHoverSymbol = ""
	m.pendingHoverPkgPath = ""
}

// dismissAllHover dismisses both editor and preview hover tooltips and
// resets the hover tracking state.
func (m *AppModel) dismissAllHover() {
	if m.focusedEditor().HoverActive() {
		m.focusedEditor().DismissHover()
	}
	if m.previewPanel.HoverActive() {
		m.previewPanel.DismissHover()
	}
	m.hoverMouseLine = -1
}

// handleMouseHoverTick processes the debounced hover tick. If the mouse
// is still on the same word, fires parallel LSP hover and definition requests.
func (m *AppModel) handleMouseHoverTick(tick msg.LSPMouseHoverTickMsg) tea.Cmd {
	if m.viewMode != ViewEdit {
		return nil
	}

	// When hover is for the preview pane, use preview panel methods.
	if m.hoverForPreview {
		wordStart, _ := m.previewPanel.WordBoundsAt(tick.Line, tick.Col)
		if tick.Line != m.hoverMouseLine || wordStart != m.hoverMouseWordStart {
			return nil
		}
		filePath := m.previewPanel.FilePath()
		if filePath == "" {
			return nil
		}
		m.pendingHoverSymbol = ""
		m.pendingHoverPkgPath = ""
		return tea.Batch(
			m.lspHoverCmd(filePath, tick.Line, tick.Col),
			m.lspDefinitionCmd(filePath, tick.Line, tick.Col, true),
		)
	}

	// Editor pane hover.
	wordStart, _ := m.focusedEditor().WordBoundsAt(tick.Line, tick.Col)
	if tick.Line != m.hoverMouseLine || wordStart != m.hoverMouseWordStart {
		return nil
	}
	filePath := m.focusedEditor().FilePath()
	if filePath == "" {
		return nil
	}
	m.pendingHoverSymbol = ""
	m.pendingHoverPkgPath = ""
	return tea.Batch(
		m.lspHoverCmd(filePath, tick.Line, tick.Col),
		m.lspDefinitionCmd(filePath, tick.Line, tick.Col, true),
	)
}

// handleDocHighlightTick processes the debounced document highlight tick.
// If the cursor is still on the same position and in normal mode, fires
// the LSP documentHighlight request.
func (m *AppModel) handleDocHighlightTick(tick msg.LSPDocHighlightTickMsg) tea.Cmd {
	if m.viewMode != ViewEdit {
		return nil
	}
	if m.focusedEditor().CursorLine() != tick.Line || m.focusedEditor().CursorCol() != tick.Col {
		return nil
	}
	if !m.focusedEditor().IsWordCharAtPos(tick.Line, tick.Col) {
		return nil
	}
	filePath := m.focusedEditor().FilePath()
	if filePath == "" {
		return nil
	}
	return m.lspDocumentHighlightCmd(filePath, tick.Line, tick.Col)
}

func (m *AppModel) toggleSearch() {
	if m.searchOverlay.Visible() {
		m.searchOverlay.Hide()
		m.overlay = overlayNone
	} else {
		// Kill residual scroll/bounce momentum so it doesn't affect the
		// overlay or resume unexpectedly when the overlay closes.
		m.scroll = scrollState{}
		m.bounce = bounceState{}
		m.chat.SetBounceOffset(0)
		m.codePanel.SetBounceOffset(0)
		m.fileTree.SetBounceOffset(0)
		m.searchOverlay.Show()
		m.overlay = overlaySearch
	}
}

func (m *AppModel) toggleFieldManual() {
	if m.fieldManualOverlay.Visible() {
		m.fieldManualOverlay.Hide()
		m.overlay = overlayNone
	} else {
		m.scroll = scrollState{}
		m.bounce = bounceState{}
		m.chat.SetBounceOffset(0)
		m.codePanel.SetBounceOffset(0)
		m.fileTree.SetBounceOffset(0)
		m.fieldManualOverlay.Show()
		m.overlay = overlayFieldManual
	}
}

// ---------------------------------------------------------------------------
// Message propagation
// ---------------------------------------------------------------------------

// propagate forwards a message to all components and collects commands.
func (m *AppModel) propagate(raw tea.Msg) tea.Cmd {
	// Skip global propagation for tick messages to avoid redundant work;
	// fast ticks are handled centrally, decor ticks are dispatched explicitly.
	switch raw.(type) {
	case msg.TickMsg, msg.DecorTickMsg:
		return nil
	}

	var cmds []tea.Cmd

	chatComp, chatCmd := m.chat.Update(raw)
	m.chat = chatComp.(*chat.Model)
	cmds = appendCmd(cmds, chatCmd)

	inputComp, inputCmd := m.input.Update(raw)
	m.input = inputComp.(*inputpkg.Model)
	cmds = appendCmd(cmds, inputCmd)

	_, statusCmd := m.statusBar.Update(raw)
	cmds = appendCmd(cmds, statusCmd)

	sessionComp, sessionCmd := m.sessionPanel.Update(raw)
	m.sessionPanel = sessionComp.(*sessionpkg.Model)
	cmds = appendCmd(cmds, sessionCmd)

	agentComp, agentCmd := m.agentPanel.Update(raw)
	m.agentPanel = agentComp.(*agentpkg.Model)
	cmds = appendCmd(cmds, agentCmd)

	codeComp, codeCmd := m.codePanel.Update(raw)
	m.codePanel = codeComp.(*codepkg.Model)
	cmds = appendCmd(cmds, codeCmd)

	knowledgeComp, knowledgeCmd := m.knowledgePanel.Update(raw)
	m.knowledgePanel = knowledgeComp.(*knowledgepkg.Model)
	cmds = appendCmd(cmds, knowledgeCmd)

	treeComp, treeCmd := m.fileTree.Update(raw)
	m.fileTree = treeComp.(*filetree.Model)
	cmds = appendCmd(cmds, treeCmd)

	if m.gitPanel != nil {
		gitComp, gitCmd := m.gitPanel.Update(raw)
		m.gitPanel = gitComp.(*gitpanel.Model)
		cmds = appendCmd(cmds, gitCmd)
		m.syncStagedFiles()
	}

	if m.commitTree != nil {
		ctComp, ctCmd := m.commitTree.Update(raw)
		m.commitTree = ctComp.(*committree.Model)
		cmds = appendCmd(cmds, ctCmd)
	}

	return tea.Batch(cmds...)
}

func (m *AppModel) propagateWithoutChat(raw tea.Msg) tea.Cmd {
	switch raw.(type) {
	case msg.TickMsg, msg.DecorTickMsg:
		return nil
	}

	var cmds []tea.Cmd

	inputComp, inputCmd := m.input.Update(raw)
	m.input = inputComp.(*inputpkg.Model)
	cmds = appendCmd(cmds, inputCmd)

	_, statusCmd := m.statusBar.Update(raw)
	cmds = appendCmd(cmds, statusCmd)

	sessionComp, sessionCmd := m.sessionPanel.Update(raw)
	m.sessionPanel = sessionComp.(*sessionpkg.Model)
	cmds = appendCmd(cmds, sessionCmd)

	agentComp, agentCmd := m.agentPanel.Update(raw)
	m.agentPanel = agentComp.(*agentpkg.Model)
	cmds = appendCmd(cmds, agentCmd)

	codeComp, codeCmd := m.codePanel.Update(raw)
	m.codePanel = codeComp.(*codepkg.Model)
	cmds = appendCmd(cmds, codeCmd)

	knowledgeComp, knowledgeCmd := m.knowledgePanel.Update(raw)
	m.knowledgePanel = knowledgeComp.(*knowledgepkg.Model)
	cmds = appendCmd(cmds, knowledgeCmd)

	treeComp, treeCmd := m.fileTree.Update(raw)
	m.fileTree = treeComp.(*filetree.Model)
	cmds = appendCmd(cmds, treeCmd)

	if m.gitPanel != nil {
		gitComp, gitCmd := m.gitPanel.Update(raw)
		m.gitPanel = gitComp.(*gitpanel.Model)
		cmds = appendCmd(cmds, gitCmd)
		m.syncStagedFiles()
	}

	if m.commitTree != nil {
		ctComp, ctCmd := m.commitTree.Update(raw)
		m.commitTree = ctComp.(*committree.Model)
		cmds = appendCmd(cmds, ctCmd)
	}

	return tea.Batch(cmds...)
}

type focusedKeyHandler func(*AppModel, tea.KeyMsg) tea.Cmd

var focusedKeyHandlers = map[component.FocusID]focusedKeyHandler{
	component.FocusInput:        (*AppModel).propagateToInput,
	component.FocusChat:         (*AppModel).propagateToChat,
	component.FocusSessionPanel: (*AppModel).propagateToSessionPanel,
	component.FocusAgentPanel:   (*AppModel).propagateToAgentPanel,
	component.FocusCodeViewer:   (*AppModel).propagateToCodeViewer,
	component.FocusFileTree:     (*AppModel).propagateToFileTree,
	component.FocusKnowledge:    (*AppModel).propagateToKnowledgePanel,
	component.FocusGitPanel:     (*AppModel).propagateToGitPanel,
	component.FocusCommitTree:   (*AppModel).propagateToCommitTree,
	component.FocusConflictView: (*AppModel).propagateToConflictView,
	component.FocusConflictFileList: func(m *AppModel, key tea.KeyMsg) tea.Cmd {
		if m.conflictViewActive && m.conflictView != nil {
			return m.conflictView.UpdateFileList(key.String())
		}
		return nil
	},
	component.FocusMergeDiffView: (*AppModel).propagateToMergeDiffView,
	component.FocusMergeDiffFileList: func(m *AppModel, key tea.KeyMsg) tea.Cmd {
		if m.mergeDiffViewActive && m.mergeDiffView != nil {
			m.mergeDiffView.UpdateFileList(key.String())
		}
		return nil
	},
	component.FocusDiffView: (*AppModel).propagateToDiffView,
	component.FocusDiffFileList: func(m *AppModel, key tea.KeyMsg) tea.Cmd {
		if m.diffViewActive && m.diffView != nil {
			m.diffView.UpdateFileList(key.String())
		}
		return nil
	},
}

// propagateToFocused sends a key message only to the currently focused component.
func (m *AppModel) propagateToFocused(key tea.KeyMsg) tea.Cmd {
	focused := m.focus.Current()
	if handler, ok := focusedKeyHandlers[focused]; ok {
		return handler(m, key)
	}
	return m.propagateToPaneFocus(focused, key)
}

func (m *AppModel) propagateToInput(key tea.KeyMsg) tea.Cmd {
	prevVisual := m.input.VisualLineCount()
	comp, cmd := m.input.Update(key)
	m.input = comp.(*inputpkg.Model)
	if m.input.VisualLineCount() != prevVisual {
		m.recalcLayout()
	}
	return cmd
}

func (m *AppModel) propagateToChat(key tea.KeyMsg) tea.Cmd {
	if m.viewMode == ViewMemory && m.memoryView != nil {
		if m.memoryView.HandleCanvasKey(key) {
			m.viewDirty = true
		}
		return nil
	}
	comp, cmd := m.chat.Update(key)
	m.chat = comp.(*chat.Model)
	return cmd
}

func (m *AppModel) propagateToSessionPanel(key tea.KeyMsg) tea.Cmd {
	if m.viewMode == ViewMemory && m.memoryView != nil {
		if m.memoryView.HandleIndexKey(key) {
			m.viewDirty = true
		}
		return nil
	}
	comp, cmd := m.sessionPanel.Update(key)
	m.sessionPanel = comp.(*sessionpkg.Model)
	return cmd
}

func (m *AppModel) propagateToAgentPanel(key tea.KeyMsg) tea.Cmd {
	if m.viewMode == ViewMemory {
		return m.propagateToSessionPanel(key)
	}
	comp, cmd := m.agentPanel.Update(key)
	m.agentPanel = comp.(*agentpkg.Model)
	m.syncManualTargetFromAgentSelection()
	return cmd
}

func (m *AppModel) propagateToCodeViewer(key tea.KeyMsg) tea.Cmd {
	comp, cmd := m.codePanel.Update(key)
	m.codePanel = comp.(*codepkg.Model)
	return cmd
}

func (m *AppModel) propagateToFileTree(key tea.KeyMsg) tea.Cmd {
	comp, cmd := m.fileTree.Update(key)
	m.fileTree = comp.(*filetree.Model)
	return cmd
}

func (m *AppModel) propagateToKnowledgePanel(key tea.KeyMsg) tea.Cmd {
	comp, cmd := m.knowledgePanel.Update(key)
	m.knowledgePanel = comp.(*knowledgepkg.Model)
	return cmd
}

func (m *AppModel) propagateToGitPanel(key tea.KeyMsg) tea.Cmd {
	comp, cmd := m.gitPanel.Update(key)
	m.gitPanel = comp.(*gitpanel.Model)
	m.syncStagedFiles()
	return cmd
}

func (m *AppModel) propagateToCommitTree(key tea.KeyMsg) tea.Cmd {
	comp, cmd := m.commitTree.Update(key)
	m.commitTree = comp.(*committree.Model)
	return cmd
}

func (m *AppModel) propagateToConflictView(key tea.KeyMsg) tea.Cmd {
	if m.conflictViewActive && m.conflictView != nil {
		return m.conflictView.Update(key)
	}
	return nil
}

func (m *AppModel) propagateToMergeDiffView(key tea.KeyMsg) tea.Cmd {
	if m.mergeDiffView == nil {
		return nil
	}
	cmd := m.mergeDiffView.Update(key)
	m.focus.SetFocus(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
	m.syncFocusState()
	return cmd
}

func (m *AppModel) propagateToDiffView(key tea.KeyMsg) tea.Cmd {
	if m.diffView == nil {
		return nil
	}
	cmd := m.diffView.Update(key)
	m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
	m.syncFocusState()
	return cmd
}

func (m *AppModel) propagateToPaneFocus(focused component.FocusID, key tea.KeyMsg) tea.Cmd {
	if !pane.IsPaneFocus(focused) {
		return nil
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.propagateToMergeDiffView(key)
	}
	if m.diffViewActive && m.diffView != nil {
		return m.propagateToDiffView(key)
	}
	pid := pane.PaneIDFromFocus(focused)
	if pid == m.previewPane {
		return m.propagateToPreviewPane(key)
	}
	if pid == m.mdPreviewPane {
		return m.propagateToMarkdownPreviewPane(key)
	}
	return m.propagateToEditorPane(pid, key)
}

func (m *AppModel) propagateToPreviewPane(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "alt+enter":
		m.dismissPreview()
		return nil
	case "enter":
		return m.openFromPreview()
	default:
		_, cmd := m.previewPanel.Update(key)
		return cmd
	}
}

func (m *AppModel) propagateToMarkdownPreviewPane(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "alt+enter", "q":
		m.dismissMarkdownPreview()
		return nil
	default:
		_, cmd := m.mdPreviewPanel.Update(key)
		return cmd
	}
}

func (m *AppModel) propagateToEditorPane(pid pane.PaneID, key tea.KeyMsg) tea.Cmd {
	ps, ok := m.paneEditors[pid]
	if !ok {
		return nil
	}
	wasMod := ps.editor.Modified()
	comp, cmd := ps.editor.Update(key)
	ps.editor = comp.(*editor.Model)
	if ps.editor.Modified() != wasMod {
		m.refreshTabsModified()
	}
	line := ps.editor.CursorLine()
	col := ps.editor.CursorCol()
	if ps.editor.IsWordCharAtPos(line, col) {
		m.highlightLine = line
		m.highlightCol = col
		hlCmd := tea.Tick(highlightDebounce, func(_ time.Time) tea.Msg {
			return msg.LSPDocHighlightTickMsg{Line: line, Col: col}
		})
		return tea.Batch(cmd, hlCmd)
	}
	ps.editor.ClearHighlightRanges()
	return cmd
}
