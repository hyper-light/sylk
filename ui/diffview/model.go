package diffview

import (
	"slices"
	"strings"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// toolbarHeight is the number of lines consumed by the toolbar (border + content).
const toolbarHeight = 2

// diffHeaderHeight is the number of lines consumed by the diff header (hash + divider).
const diffHeaderHeight = 2

// maxVisiblePanes caps the number of simultaneously visible file panes.
// Derived from: 4 stacked panes is the practical limit for readable diffs.
const maxVisiblePanes = 4

// Model is the diff view component state.
type Model struct {
	pairs      []DiffPair
	mode       CompareMode
	sideBySide bool

	activePairIdx int
	fileBlocks    []FileBlock
	highlights    []FileHighlight

	// Pane tree for per-file sub-panes (horizontal splits).
	paneTree    *pane.Node
	paneFiles   map[pane.PaneID]*FileDiffPane
	focusedPane pane.PaneID
	paneCounter pane.PaneID

	// File list sidebar (left panel).
	selectedFile    int
	fileListScroll  int
	fileListW       int
	fileListH       int
	fileListFocused bool
	fileListDirty   bool

	width  int
	height int

	focused        bool
	theme          *theme.Theme
	toolbarFocused bool
	toolbarAction  int
	hoverBtnIdx    int
	viewDirty      bool

	highlighter  *codepkg.Highlighter
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style
	defaultSt    lipgloss.Style
}

// New creates a diff view model from pre-computed diff pairs.
func New(pairs []DiffPair, mode CompareMode, th *theme.Theme) *Model {
	hl := codepkg.NewHighlighter(th)
	m := &Model{
		pairs:        pairs,
		mode:         mode,
		sideBySide:   true,
		theme:        th,
		hoverBtnIdx:  -1,
		highlighter:  hl,
		syntaxStyles: th.Syntax,
		defaultSt:    lipgloss.NewStyle().Foreground(th.Palette.Foreground),
		paneFiles:    make(map[pane.PaneID]*FileDiffPane),
	}
	if len(pairs) > 0 {
		m.loadPair(0)
	}
	return m
}

// SetSize sets the available width and height for the diff view (right panel).
func (m *Model) SetSize(w, h int) {
	if m.width == w && m.height == h {
		return
	}
	m.width = w
	m.height = h
	m.sizePanes()
	m.rebuildAllPanes()
}

// SetFileListSize sets the available dimensions for the file list sidebar.
func (m *Model) SetFileListSize(w, h int) {
	if m.fileListW == w && m.fileListH == h {
		return
	}
	m.fileListW = w
	m.fileListH = h
	m.fileListDirty = true
}

// Focused returns whether the diff view has focus.
func (m *Model) Focused() bool { return m.focused }

// SetFocused sets focus state.
func (m *Model) SetFocused(f bool) {
	m.focused = f
	m.viewDirty = true
}

// ViewDirty returns and clears the dirty flag for the right panel.
func (m *Model) ViewDirty() bool {
	d := m.viewDirty
	for _, fp := range m.paneFiles {
		if fp.ViewDirty() {
			d = true
		}
	}
	m.viewDirty = false
	return d
}

// FileListDirty returns and clears the dirty flag for the file list sidebar.
func (m *Model) FileListDirty() bool {
	d := m.fileListDirty
	m.fileListDirty = false
	return d
}

// Close releases tree-sitter resources held by the highlighter.
func (m *Model) Close() {
	if m.highlighter != nil {
		m.highlighter.Close()
		m.highlighter = nil
	}
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View renders the diff view right panel: header + pane tree + toolbar.
func (m *Model) View(cursorVisible bool) string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	p := m.theme.Palette

	// Pinned header.
	var header string
	if m.activePairIdx < len(m.pairs) {
		header = renderDiffHeader(m.pairs[m.activePairIdx], m.width, p)
	} else {
		header = strings.Repeat(" ", m.width) + "\n" + strings.Repeat(" ", m.width)
	}

	// Pane tree content.
	contentH := m.viewportHeight()
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: contentH}
	content := m.composeDiffPanes(area)

	// Toolbar.
	toolbar := renderToolbar(m.mode, m.sideBySide, m.toolbarFocused,
		m.toolbarAction, m.hoverBtnIdx, m.width, p)

	return header + "\n" + content + "\n" + toolbar
}

// viewportHeight returns the height available for pane content.
// Layout: diffHeader (2 lines) + content (H lines) + toolbar (2 lines) = m.height.
func (m *Model) viewportHeight() int {
	return max(m.height-diffHeaderHeight-toolbarHeight, 1)
}

// composeDiffPanes renders the pane tree into a block of text using
// row-by-row composition. Mirrors composePanes() from app.go.
func (m *Model) composeDiffPanes(area pane.Rect) string {
	if m.paneTree == nil || area.H <= 0 || area.W <= 0 {
		blank := strings.Repeat(" ", max(area.W, 0))
		rows := make([]string, max(area.H, 0))
		for i := range rows {
			rows[i] = blank
		}
		return strings.Join(rows, "\n")
	}

	rects := m.paneTree.ComputeLayout(area)
	dividers := m.paneTree.Dividers(area)
	leaves := m.paneTree.Leaves()

	p := m.theme.Palette
	divStyle := lipgloss.NewStyle().Foreground(p.Border)

	// Render each leaf pane into lines.
	leafLines := make(map[pane.PaneID][]string, len(leaves))
	for _, id := range leaves {
		fp := m.paneFiles[id]
		if fp == nil || fp.fileIdx >= len(m.fileBlocks) {
			r := rects[id]
			lines := make([]string, r.H)
			blank := strings.Repeat(" ", r.W)
			for i := range lines {
				lines[i] = blank
			}
			leafLines[id] = lines
			continue
		}
		fb := m.fileBlocks[fp.fileIdx]
		rendered := fp.View(fb, p, id == m.focusedPane)
		leafLines[id] = strings.Split(rendered, "\n")
	}

	// Row-by-row assembly.
	type seg struct {
		x int
		s string
	}
	rows := make([]string, area.H)
	for y := range area.H {
		var segs []seg

		// Pane content segments.
		for _, id := range leaves {
			r := rects[id]
			if y < r.Y || y >= r.Y+r.H {
				continue
			}
			lineIdx := y - r.Y
			lines := leafLines[id]
			line := ""
			if lineIdx < len(lines) {
				line = lines[lineIdx]
			}
			segs = append(segs, seg{r.X, line})
		}

		// Divider segments.
		for _, d := range dividers {
			switch {
			case d.Dir == pane.SplitVertical && y >= d.Y && y < d.Y+d.Len:
				segs = append(segs, seg{d.X, divStyle.Render("│")})
			case d.Dir == pane.SplitHorizontal && y == d.Y:
				segs = append(segs, seg{d.X, divStyle.Render(strings.Repeat("─", d.Len))})
			}
		}

		// Sort by X and concatenate.
		slices.SortFunc(segs, func(a, b seg) int { return a.x - b.x })
		var b strings.Builder
		for _, s := range segs {
			b.WriteString(s.s)
		}
		row := b.String()
		if vis := lipgloss.Width(row); vis < area.W {
			row += strings.Repeat(" ", area.W-vis)
		}
		rows[y] = row
	}

	return strings.Join(rows, "\n")
}

// ---------------------------------------------------------------------------
// Pair & pane tree management
// ---------------------------------------------------------------------------

// loadPair computes FileBlocks, highlights, and builds the pane tree.
func (m *Model) loadPair(idx int) {
	if idx < 0 || idx >= len(m.pairs) {
		return
	}
	m.activePairIdx = idx
	m.fileBlocks = BuildFileBlocks(m.pairs[idx].Files)
	m.highlights = buildFileHighlights(m.fileBlocks, m.highlighter)
	m.selectedFile = 0
	m.fileListScroll = 0
	m.fileListDirty = true
	m.buildPaneTree()
	m.sizePanes()
	m.rebuildAllPanes()
}

// buildPaneTree creates a balanced horizontal split tree for visible files.
func (m *Model) buildPaneTree() {
	m.paneFiles = make(map[pane.PaneID]*FileDiffPane)
	m.paneCounter = 0

	n := min(len(m.fileBlocks), maxVisiblePanes)
	if n == 0 {
		m.paneTree = pane.NewLeaf(1)
		m.focusedPane = 1
		return
	}

	ids := make([]pane.PaneID, n)
	for i := range n {
		m.paneCounter++
		ids[i] = m.paneCounter
		m.paneFiles[ids[i]] = newFileDiffPane(i)
	}

	m.paneTree = buildBalancedHTree(ids)
	m.focusedPane = ids[0]
}

// buildBalancedHTree creates a balanced binary tree of horizontal splits.
func buildBalancedHTree(ids []pane.PaneID) *pane.Node {
	if len(ids) == 1 {
		return pane.NewLeaf(ids[0])
	}
	mid := len(ids) / 2
	return &pane.Node{
		Dir:   pane.SplitHorizontal,
		Ratio: 0.5,
		Left:  buildBalancedHTree(ids[:mid]),
		Right: buildBalancedHTree(ids[mid:]),
	}
}

// sizePanes distributes available space to each file pane via ComputeLayout.
func (m *Model) sizePanes() {
	if m.paneTree == nil || m.width <= 0 || m.height <= 0 {
		return
	}
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	rects := m.paneTree.ComputeLayout(area)
	for id, fp := range m.paneFiles {
		if r, ok := rects[id]; ok {
			fp.SetSize(r.W, r.H)
		}
	}
}

// rebuildAllPanes re-renders every file pane's diff lines.
func (m *Model) rebuildAllPanes() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	maxOld, maxNew := maxLineNo(m.fileBlocks)
	p := m.theme.Palette
	for _, fp := range m.paneFiles {
		if fp.fileIdx >= len(m.fileBlocks) {
			continue
		}
		fb := m.fileBlocks[fp.fileIdx]
		var fh FileHighlight
		if fp.fileIdx < len(m.highlights) {
			fh = m.highlights[fp.fileIdx]
		}
		fp.Rebuild(fb, fh, m.sideBySide, maxOld, maxNew, m.syntaxStyles, m.defaultSt, p)
	}
	m.viewDirty = true
}

// LoadFileIntoFocusedPane replaces the focused pane's file with the given index.
func (m *Model) LoadFileIntoFocusedPane(fileIdx int) {
	if fileIdx < 0 || fileIdx >= len(m.fileBlocks) {
		return
	}
	fp := m.focusedFileDiffPane()
	if fp == nil {
		return
	}
	fp.fileIdx = fileIdx
	fp.scrollOffset = 0

	maxOld, maxNew := maxLineNo(m.fileBlocks)
	fb := m.fileBlocks[fileIdx]
	var fh FileHighlight
	if fileIdx < len(m.highlights) {
		fh = m.highlights[fileIdx]
	}
	fp.Rebuild(fb, fh, m.sideBySide, maxOld, maxNew, m.syntaxStyles, m.defaultSt, m.theme.Palette)
	m.viewDirty = true
	m.fileListDirty = true
}

// ---------------------------------------------------------------------------
// Input handling
// ---------------------------------------------------------------------------

// Update handles input messages.
func (m *Model) Update(raw tea.Msg) tea.Cmd {
	switch msg := raw.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return nil
}

// handleKey processes keyboard input.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if m.toolbarFocused {
		return m.handleToolbarKey(key)
	}

	fp := m.focusedFileDiffPane()

	switch key.String() {
	case "j", "down":
		if fp != nil {
			fp.scrollDown(1)
			m.viewDirty = true
		}
	case "k", "up":
		if fp != nil {
			fp.scrollUp(1)
			m.viewDirty = true
		}
	case "g", "home":
		if fp != nil {
			fp.scrollToTop()
			m.viewDirty = true
		}
	case "G", "end":
		if fp != nil {
			fp.scrollToBottom()
			m.viewDirty = true
		}
	case "pgdown", "ctrl+d":
		if fp != nil {
			fp.scrollDown(fp.viewportHeight() / 2)
			m.viewDirty = true
		}
	case "pgup", "ctrl+u":
		if fp != nil {
			fp.scrollUp(fp.viewportHeight() / 2)
			m.viewDirty = true
		}
	case "]h":
		if fp != nil {
			fp.jumpNextHunk()
			m.viewDirty = true
		}
	case "[h":
		if fp != nil {
			fp.jumpPrevHunk()
			m.viewDirty = true
		}
	case "]f":
		m.focusNextPane()
	case "[f":
		m.focusPrevPane()
	case "]p":
		m.nextPair()
	case "[p":
		m.prevPair()
	case "t":
		m.toggleViewMode()
	case "m":
		return m.cycleCompareMode()
	case "tab":
		m.toolbarFocused = true
		m.viewDirty = true
	case "q", "esc":
		return func() tea.Msg { return ExitDiffViewMsg{} }
	}

	return nil
}

// handleToolbarKey handles keys when the toolbar is focused.
func (m *Model) handleToolbarKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "tab":
		m.toolbarFocused = false
		m.viewDirty = true
		return func() tea.Msg { return FocusFileListMsg{} }
	case "shift+tab", "esc":
		m.toolbarFocused = false
		m.viewDirty = true
	case "h", "left":
		m.toolbarAction = max(m.toolbarAction-1, 0)
		m.viewDirty = true
	case "l", "right":
		m.toolbarAction = min(m.toolbarAction+1, toolbarButtonCount-1)
		m.viewDirty = true
	case "enter", " ":
		return m.activateToolbarButton(m.toolbarAction)
	case "q":
		return func() tea.Msg { return ExitDiffViewMsg{} }
	}
	return nil
}

// handleMouse processes mouse events.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonWheelUp {
			if fp := m.paneAtY(msg.Y); fp != nil {
				fp.scrollUp(3)
				m.viewDirty = true
			}
		} else if msg.Button == tea.MouseButtonWheelDown {
			if fp := m.paneAtY(msg.Y); fp != nil {
				fp.scrollDown(3)
				m.viewDirty = true
			}
		} else if msg.Button == tea.MouseButtonLeft {
			tbRow := m.height - toolbarHeight
			if msg.Y >= tbRow {
				idx := toolbarHitTest(m.mode, m.sideBySide, m.width, msg.X)
				if idx >= 0 {
					return m.activateToolbarButton(idx)
				}
			} else {
				m.focusPaneAtY(msg.Y)
			}
		}
	case tea.MouseActionMotion:
		tbRow := m.height - toolbarHeight
		if msg.Y >= tbRow {
			idx := toolbarHitTest(m.mode, m.sideBySide, m.width, msg.X)
			if idx != m.hoverBtnIdx {
				m.hoverBtnIdx = idx
				m.viewDirty = true
			}
		} else if m.hoverBtnIdx >= 0 {
			m.hoverBtnIdx = -1
			m.viewDirty = true
		}
	}
	return nil
}

// paneAtY returns the FileDiffPane whose rect contains the given view Y
// coordinate (adjusted for the diff header offset). Returns nil if outside.
func (m *Model) paneAtY(viewY int) *FileDiffPane {
	paneY := viewY - diffHeaderHeight
	if paneY < 0 || m.paneTree == nil {
		return nil
	}
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	rects := m.paneTree.ComputeLayout(area)
	for id, r := range rects {
		if paneY >= r.Y && paneY < r.Y+r.H {
			return m.paneFiles[id]
		}
	}
	return nil
}

// focusPaneAtY sets focus to the pane at the given view Y coordinate.
func (m *Model) focusPaneAtY(viewY int) {
	paneY := viewY - diffHeaderHeight
	if paneY < 0 || m.paneTree == nil {
		return
	}
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	rects := m.paneTree.ComputeLayout(area)
	for id, r := range rects {
		if paneY >= r.Y && paneY < r.Y+r.H && id != m.focusedPane {
			m.focusedPane = id
			m.viewDirty = true
			m.fileListDirty = true
			return
		}
	}
}

// activateToolbarButton triggers the action for a toolbar button index.
func (m *Model) activateToolbarButton(idx int) tea.Cmd {
	switch idx {
	case tbChain:
		if m.mode != CompareModeChain {
			m.mode = CompareModeChain
			m.viewDirty = true
			return func() tea.Msg { return ChangeCompareModeMsg{Mode: CompareModeChain} }
		}
	case tbAllFirst:
		if m.mode != CompareModeAllFirst {
			m.mode = CompareModeAllFirst
			m.viewDirty = true
			return func() tea.Msg { return ChangeCompareModeMsg{Mode: CompareModeAllFirst} }
		}
	case tbPairs:
		if m.mode != CompareModePairs {
			m.mode = CompareModePairs
			m.viewDirty = true
			return func() tea.Msg { return ChangeCompareModeMsg{Mode: CompareModePairs} }
		}
	case tbSBS:
		if !m.sideBySide {
			m.sideBySide = true
			m.rebuildAllPanes()
		}
	case tbUnified:
		if m.sideBySide {
			m.sideBySide = false
			m.rebuildAllPanes()
		}
	case tbClose:
		return func() tea.Msg { return ExitDiffViewMsg{} }
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pane navigation
// ---------------------------------------------------------------------------

// focusedFileDiffPane returns the currently focused pane, or nil.
func (m *Model) focusedFileDiffPane() *FileDiffPane {
	return m.paneFiles[m.focusedPane]
}

// FocusedFileIdx returns the file index of the focused pane, or -1.
func (m *Model) FocusedFileIdx() int {
	if fp := m.focusedFileDiffPane(); fp != nil {
		return fp.fileIdx
	}
	return -1
}

// focusNextPane moves focus to the next pane in spatial order.
func (m *Model) focusNextPane() {
	if m.paneTree == nil {
		return
	}
	leaves := m.paneTree.Leaves()
	for i, id := range leaves {
		if id == m.focusedPane && i+1 < len(leaves) {
			m.focusedPane = leaves[i+1]
			m.viewDirty = true
			m.fileListDirty = true
			return
		}
	}
}

// focusPrevPane moves focus to the previous pane in spatial order.
func (m *Model) focusPrevPane() {
	if m.paneTree == nil {
		return
	}
	leaves := m.paneTree.Leaves()
	for i, id := range leaves {
		if id == m.focusedPane && i > 0 {
			m.focusedPane = leaves[i-1]
			m.viewDirty = true
			m.fileListDirty = true
			return
		}
	}
}

// ---------------------------------------------------------------------------
// View mode & pair navigation
// ---------------------------------------------------------------------------

// toggleViewMode switches between side-by-side and unified for all panes.
func (m *Model) toggleViewMode() {
	m.sideBySide = !m.sideBySide
	m.rebuildAllPanes()
}

// cycleCompareMode cycles through compare modes and emits a message so
// app.go re-fetches diffs with the new pairing logic.
func (m *Model) cycleCompareMode() tea.Cmd {
	m.mode = CompareMode((int(m.mode) + 1) % int(compareModeCount))
	m.viewDirty = true
	mode := m.mode
	return func() tea.Msg { return ChangeCompareModeMsg{Mode: mode} }
}

// nextPair switches to the next diff pair.
func (m *Model) nextPair() {
	if m.activePairIdx+1 < len(m.pairs) {
		m.loadPair(m.activePairIdx + 1)
	}
}

// prevPair switches to the previous diff pair.
func (m *Model) prevPair() {
	if m.activePairIdx > 0 {
		m.loadPair(m.activePairIdx - 1)
	}
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// PaneTree returns the pane tree so the app can include diff sub-panes in
// the spatial navigation grid.
func (m *Model) PaneTree() *pane.Node { return m.paneTree }

// SetFocusedPane sets the internally focused pane by ID. Used when the app's
// spatial navigation changes focus to a diff sub-pane.
func (m *Model) SetFocusedPane(id pane.PaneID) {
	if _, ok := m.paneFiles[id]; ok && id != m.focusedPane {
		m.focusedPane = id
		m.viewDirty = true
		m.fileListDirty = true
	}
}

// FocusedPane returns the currently focused pane ID.
func (m *Model) FocusedPane() pane.PaneID { return m.focusedPane }

// Mode returns the current compare mode.
func (m *Model) Mode() CompareMode { return m.mode }

// ActivePairIdx returns the index of the currently displayed pair.
func (m *Model) ActivePairIdx() int { return m.activePairIdx }

// FileBlocks returns the current pair's file blocks (for file list rendering).
func (m *Model) FileBlocks() []FileBlock { return m.fileBlocks }

// SelectedFile returns the file list cursor position.
func (m *Model) SelectedFile() int { return m.selectedFile }

// SetSelectedFile sets the file list cursor position.
func (m *Model) SetSelectedFile(idx int) {
	if idx >= 0 && idx < len(m.fileBlocks) {
		m.selectedFile = idx
		m.fileListDirty = true
	}
}

// FileListFocused returns whether the file list has focus.
func (m *Model) FileListFocused() bool { return m.fileListFocused }

// SetFileListFocused sets the file list focus state.
func (m *Model) SetFileListFocused(f bool) {
	m.fileListFocused = f
	m.fileListDirty = true
}
