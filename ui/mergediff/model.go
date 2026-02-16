package mergediff

import (
	"strings"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/diffview"
	"github.com/adalundhe/sylk/ui/editor/findbar"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// toolbarHeight is the number of lines consumed by the toolbar (border + content).
const toolbarHeight = 2

// Model is the merge diff view component state. It renders all changed files
// stacked vertically in a single scrollable pane (no per-file tabs).
type Model struct {
	pairs      []diffview.DiffPair
	sideBySide bool

	pairData   []diffview.PairData
	unionFiles []diffview.UnionFileEntry

	// Pane tree (1 pane per pair, horizontal splits).
	paneTree    *pane.Node
	paneFiles   map[pane.PaneID]*MergePane
	focusedPane pane.PaneID
	paneCounter pane.PaneID

	// File list sidebar.
	selectedFile   int
	highlightPath  string
	fileListScroll int
	fileListW      int
	fileListH      int
	fileListFocused bool
	fileListDirty   bool

	// File list search state.
	fileSearchActive bool
	fileSearchQuery  []rune
	filteredIndices  []int

	width  int
	height int

	focused        bool
	theme          *theme.Theme
	nerdFonts      bool
	toolbarFocused bool
	toolbarAction  int
	hoverBtnIdx    int
	viewDirty      bool
	bounceOffset   int

	fileListBounceOffset int

	highlighter  *codepkg.Highlighter
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style
	defaultSt    lipgloss.Style

	loading      bool
	spinnerFrame int

	merging  bool   // True while merge is in progress.
	mergeMsg string // "Merging — source to target"
	mergeErr string // Non-empty when merge failed.

	findBar     *findbar.FindBar
	findActive  bool
	findContent []rune
	findLines   []findLineInfo
}

// findLineInfo maps a searchable text line to its source.
type findLineInfo struct {
	fileIdx    int // index in the pair's Blocks
	alignedIdx int // index in FileBlock.Lines
	runeStart  int // starting rune offset in findContent
}

// New creates a merge diff view from pre-computed diff pairs.
func New(pairs []diffview.DiffPair, th *theme.Theme, nerdFonts bool) *Model {
	hl := codepkg.NewHighlighter(th)
	m := &Model{
		pairs:        pairs,
		sideBySide:   true,
		theme:        th,
		nerdFonts:    nerdFonts,
		hoverBtnIdx:  -1,
		highlighter:  hl,
		syntaxStyles: th.Syntax,
		defaultSt:    lipgloss.NewStyle().Foreground(th.Palette.Foreground),
		paneFiles:    make(map[pane.PaneID]*MergePane),
	}
	if len(pairs) > 0 {
		m.loadAllPairs()
	}
	return m
}

// ---------------------------------------------------------------------------
// Size & focus
// ---------------------------------------------------------------------------

// SetSize sets the available width and height for the merge diff view.
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

// Focused returns whether the merge diff view has focus.
func (m *Model) Focused() bool { return m.focused }

// SetFocused sets focus state.
func (m *Model) SetFocused(f bool) {
	m.focused = f
	m.viewDirty = true
}

// ViewDirty returns and clears the dirty flag.
func (m *Model) ViewDirty() bool {
	d := m.viewDirty
	for _, mp := range m.paneFiles {
		if mp.viewDirty {
			d = true
			mp.viewDirty = false
		}
	}
	m.viewDirty = false
	return d
}

// FileListDirty returns and clears the file list dirty flag.
func (m *Model) FileListDirty() bool {
	d := m.fileListDirty
	m.fileListDirty = false
	return d
}

// Close releases tree-sitter resources.
func (m *Model) Close() {
	if m.highlighter != nil {
		m.highlighter.Close()
		m.highlighter = nil
	}
}

// ---------------------------------------------------------------------------
// Loading state
// ---------------------------------------------------------------------------

// SetLoading sets the loading state.
func (m *Model) SetLoading(loading bool) {
	if m.loading == loading {
		return
	}
	m.loading = loading
	m.spinnerFrame = 0
	m.viewDirty = true
}

// Loading returns whether the view is loading.
func (m *Model) Loading() bool { return m.loading }

// NeedsDecorTick reports whether the spinner needs ticking.
func (m *Model) NeedsDecorTick() bool { return m.loading || m.merging }

// SetMerging sets the merge-in-progress state.
func (m *Model) SetMerging(msg string) {
	m.merging = true
	m.mergeMsg = msg
	m.mergeErr = ""
	m.spinnerFrame = 0
	m.viewDirty = true
}

// SetMergeError transitions from merging to error state.
func (m *Model) SetMergeError(err string) {
	m.mergeErr = err
	m.merging = false
	m.toolbarFocused = true
	m.toolbarAction = 0
	m.viewDirty = true
}

// IsMerging reports whether a merge is in progress.
func (m *Model) IsMerging() bool { return m.merging }

// HasMergeError reports whether the merge failed.
func (m *Model) HasMergeError() bool { return m.mergeErr != "" }

// spinnerFrames are the braille spinner glyphs.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerColor is the loading spinner color.
var spinnerColor = lipgloss.Color("#87CEFA")

// AdvanceSpinner advances the spinner frame.
func (m *Model) AdvanceSpinner() {
	m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// viewportHeight returns the height available for pane content.
func (m *Model) viewportHeight() int {
	h := max(m.height-toolbarHeight, 1)
	if m.findActive {
		h = max(h-diffFindBarHeight, 1)
	}
	return h
}

// View renders the merge diff view: pane tree + [find bar] + toolbar.
func (m *Model) View(cursorVisible bool) string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	p := m.theme.Palette

	if m.mergeErr != "" {
		spinH := m.viewportHeight()
		content := m.renderMergeError(spinH)
		toolbar := renderMergeErrorToolbar(m.toolbarFocused,
			m.toolbarAction, m.hoverBtnIdx, m.width, p)
		return content + "\n" + toolbar
	}

	if m.merging {
		spinH := m.viewportHeight()
		content := m.renderMergeSpinner(spinH)
		emptyTB := emptyBlock(pane.Rect{W: m.width, H: toolbarHeight})
		return content + "\n" + emptyTB
	}

	toolbar := renderMergeToolbar(m.sideBySide, m.toolbarFocused,
		m.toolbarAction, m.hoverBtnIdx, m.width, p)

	if m.loading {
		spinH := m.viewportHeight()
		content := m.renderLoadingSpinner(spinH)
		return content + "\n" + toolbar
	}

	vpH := m.viewportHeight()
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: vpH}
	content := m.composePanes(area)
	if m.bounceOffset != 0 {
		content = applyBounceShift(content, m.bounceOffset, vpH)
	}
	findBarStr := m.renderFindBarSection(cursorVisible)
	if findBarStr != "" {
		content = content + "\n" + findBarStr
	}
	return content + "\n" + toolbar
}

// renderLoadingSpinner renders a centered spinner.
func (m *Model) renderLoadingSpinner(height int) string {
	frame := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	spinSt := lipgloss.NewStyle().Foreground(spinnerColor)
	textSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	label := spinSt.Render(frame) + " " + textSt.Render("Loading diffs…")
	labelW := lipgloss.Width(label)

	emptyLine := strings.Repeat(" ", max(m.width, 0))
	midRow := height / 2
	leftPad := max((m.width-labelW)/2, 0)
	rightPad := max(m.width-leftPad-labelW, 0)
	centeredLine := strings.Repeat(" ", leftPad) + label + strings.Repeat(" ", rightPad)

	lines := make([]string, height)
	for i := range lines {
		if i == midRow {
			lines[i] = centeredLine
		} else {
			lines[i] = emptyLine
		}
	}
	return strings.Join(lines, "\n")
}

// renderMergeSpinner renders a centered spinner with the merge message.
func (m *Model) renderMergeSpinner(height int) string {
	frame := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	spinSt := lipgloss.NewStyle().Foreground(spinnerColor)
	textSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	label := spinSt.Render(frame) + " " + textSt.Render(m.mergeMsg)
	labelW := lipgloss.Width(label)

	emptyLine := strings.Repeat(" ", max(m.width, 0))
	midRow := height / 2
	leftPad := max((m.width-labelW)/2, 0)
	rightPad := max(m.width-leftPad-labelW, 0)
	centeredLine := strings.Repeat(" ", leftPad) + label + strings.Repeat(" ", rightPad)

	lines := make([]string, height)
	for i := range lines {
		if i == midRow {
			lines[i] = centeredLine
		} else {
			lines[i] = emptyLine
		}
	}
	return strings.Join(lines, "\n")
}

// renderMergeError renders a centered error message.
func (m *Model) renderMergeError(height int) string {
	errSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Error)
	label := errSt.Render(m.mergeErr)
	labelW := lipgloss.Width(label)

	emptyLine := strings.Repeat(" ", max(m.width, 0))
	midRow := height / 2
	leftPad := max((m.width-labelW)/2, 0)
	rightPad := max(m.width-leftPad-labelW, 0)
	centeredLine := strings.Repeat(" ", leftPad) + label + strings.Repeat(" ", rightPad)

	lines := make([]string, height)
	for i := range lines {
		if i == midRow {
			lines[i] = centeredLine
		} else {
			lines[i] = emptyLine
		}
	}
	return strings.Join(lines, "\n")
}

// renderFindBarSection returns the rendered find bar string, or empty.
func (m *Model) renderFindBarSection(cursorVisible bool) string {
	if m.findActive && m.findBar != nil {
		return m.findBar.View(m.width, m.theme, cursorVisible)
	}
	return ""
}

// applyBounceShift shifts content by offset lines for rubber-band effect.
func applyBounceShift(content string, offset, viewHeight int) string {
	lines := strings.Split(content, "\n")
	absOffset := offset
	if absOffset < 0 {
		absOffset = -absOffset
	}
	absOffset = min(absOffset, viewHeight)

	if offset > 0 {
		shift := min(absOffset, len(lines))
		lines = lines[shift:]
	} else {
		pad := make([]string, absOffset, absOffset+len(lines))
		lines = append(pad, lines...)
	}
	for len(lines) < viewHeight {
		lines = append(lines, "")
	}
	if len(lines) > viewHeight {
		lines = lines[:viewHeight]
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Pane composition
// ---------------------------------------------------------------------------

// composePanes renders the pane tree into a block of text.
func (m *Model) composePanes(area pane.Rect) string {
	if m.paneTree == nil || area.H <= 0 || area.W <= 0 {
		return emptyBlock(area)
	}

	rects := m.paneTree.ComputeLayout(area)
	dividers := m.paneTree.Dividers(area)
	leaves := m.paneTree.Leaves()
	divStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)

	leafLines := make(map[pane.PaneID][]string, len(leaves))
	for _, id := range leaves {
		mp := m.paneFiles[id]
		if mp == nil || mp.pairIdx >= len(m.pairs) {
			r := rects[id]
			leafLines[id] = blankLines(r.W, r.H)
			continue
		}
		rendered := mp.View(m.pairs[mp.pairIdx], m.pairData[mp.pairIdx],
			m.highlightPath, m.theme.Palette, id == m.focusedPane)
		leafLines[id] = strings.Split(rendered, "\n")
	}

	rows := make([]string, area.H)
	for y := range area.H {
		var segs []posSeg
		for _, id := range leaves {
			r := rects[id]
			if y >= r.Y && y < r.Y+r.H {
				line := ""
				if idx := y - r.Y; idx < len(leafLines[id]) {
					line = leafLines[id][idx]
				}
				segs = append(segs, posSeg{r.X, line})
			}
		}
		for _, d := range dividers {
			if seg, ok := dividerSeg(y, d, divStyle); ok {
				segs = append(segs, seg)
			}
		}
		rows[y] = assembleRow(segs, area.W)
	}
	return strings.Join(rows, "\n")
}

// posSeg is a positioned string segment.
type posSeg struct {
	x int
	s string
}

// assembleRow builds one row from sorted segments.
func assembleRow(segs []posSeg, width int) string {
	// Sort by X position.
	for i := 1; i < len(segs); i++ {
		for j := i; j > 0 && segs[j].x < segs[j-1].x; j-- {
			segs[j], segs[j-1] = segs[j-1], segs[j]
		}
	}
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.s)
	}
	row := b.String()
	if vis := lipgloss.Width(row); vis < width {
		row += strings.Repeat(" ", width-vis)
	}
	return row
}

// dividerSeg returns a segment for a pane divider at row y.
func dividerSeg(y int, d pane.Divider, divStyle lipgloss.Style) (posSeg, bool) {
	if d.Dir == pane.SplitVertical && y >= d.Y && y < d.Y+d.Len {
		return posSeg{d.X, divStyle.Render("│")}, true
	}
	if d.Dir == pane.SplitHorizontal && y == d.Y {
		return posSeg{d.X, divStyle.Render(strings.Repeat("─", d.Len))}, true
	}
	return posSeg{}, false
}

// emptyBlock renders a blank area.
func emptyBlock(area pane.Rect) string {
	blank := strings.Repeat(" ", max(area.W, 0))
	rows := make([]string, max(area.H, 0))
	for i := range rows {
		rows[i] = blank
	}
	return strings.Join(rows, "\n")
}

// blankLines creates h blank lines of width w.
func blankLines(w, h int) []string {
	blank := strings.Repeat(" ", max(w, 0))
	lines := make([]string, max(h, 0))
	for i := range lines {
		lines[i] = blank
	}
	return lines
}

// ---------------------------------------------------------------------------
// Data loading
// ---------------------------------------------------------------------------

// loadAllPairs computes PairData for every pair, builds the union file list,
// selects the first file, and constructs the pane tree.
func (m *Model) loadAllPairs() {
	m.pairData = make([]diffview.PairData, len(m.pairs))
	for i, pair := range m.pairs {
		m.pairData[i] = m.buildPairData(pair)
	}
	m.unionFiles = diffview.BuildUnionFileList(m.pairData)
	m.selectFirstFile()
	m.buildPaneTree()
	m.sizePanes()
	m.rebuildAllPanes()
}

// buildPairData computes PairData for a single pair.
func (m *Model) buildPairData(pair diffview.DiffPair) diffview.PairData {
	blocks := diffview.BuildFileBlocks(pair.Files)
	highlights := diffview.BuildFileHighlights(blocks, m.highlighter)
	pathIndex := make(map[string]int, len(blocks))
	for j, fb := range blocks {
		pathIndex[fb.Path] = j
	}
	return diffview.PairData{Blocks: blocks, Highlights: highlights, PathIndex: pathIndex}
}

// selectFirstFile selects the first file.
func (m *Model) selectFirstFile() {
	m.selectedFile = 0
	if len(m.unionFiles) > 0 {
		m.highlightPath = m.unionFiles[0].Path
	} else {
		m.highlightPath = ""
	}
	m.fileListDirty = true
}

// maxVisiblePanes caps the number of simultaneously visible pair panes.
const maxVisiblePanes = 4

// buildPaneTree creates the pane tree.
func (m *Model) buildPaneTree() {
	m.paneFiles = make(map[pane.PaneID]*MergePane)
	m.paneCounter = 0

	n := min(len(m.pairs), maxVisiblePanes)
	if n == 0 {
		m.paneTree = pane.NewLeaf(1)
		m.focusedPane = 1
		return
	}

	ids := make([]pane.PaneID, n)
	for i := range n {
		m.paneCounter++
		ids[i] = m.paneCounter
		m.paneFiles[ids[i]] = newMergePane(i)
	}

	m.paneTree = buildBalancedTree(ids)
	m.focusedPane = ids[0]
}

// buildBalancedTree creates a balanced split tree from pane IDs.
func buildBalancedTree(ids []pane.PaneID) *pane.Node {
	if len(ids) == 1 {
		return pane.NewLeaf(ids[0])
	}
	top := &pane.Node{
		Dir:   pane.SplitVertical,
		Ratio: 0.5,
		Left:  pane.NewLeaf(ids[0]),
		Right: pane.NewLeaf(ids[1]),
	}
	if len(ids) == 2 {
		return top
	}
	var bottom *pane.Node
	if len(ids) > 3 {
		bottom = &pane.Node{
			Dir:   pane.SplitVertical,
			Ratio: 0.5,
			Left:  pane.NewLeaf(ids[2]),
			Right: pane.NewLeaf(ids[3]),
		}
	} else {
		bottom = pane.NewLeaf(ids[2])
	}
	return &pane.Node{Dir: pane.SplitHorizontal, Ratio: 0.5, Left: top, Right: bottom}
}

// sizePanes distributes available space to each pane.
func (m *Model) sizePanes() {
	if m.paneTree == nil || m.width <= 0 || m.height <= 0 {
		return
	}
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	rects := m.paneTree.ComputeLayout(area)
	for id, mp := range m.paneFiles {
		if r, ok := rects[id]; ok {
			mp.SetSize(r.W, r.H)
		}
	}
}

// rebuildAllPanes re-renders every pane's content.
func (m *Model) rebuildAllPanes() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	for _, mp := range m.paneFiles {
		if mp.pairIdx >= len(m.pairData) {
			continue
		}
		mp.RebuildAllFiles(m.pairData[mp.pairIdx], m.sideBySide,
			m.syntaxStyles, m.defaultSt, m.theme.Palette)
	}
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// PaneTree returns the pane tree for spatial navigation.
func (m *Model) PaneTree() *pane.Node { return m.paneTree }

// FocusedPane returns the currently focused pane ID.
func (m *Model) FocusedPane() pane.PaneID { return m.focusedPane }

// SetFocusedPane sets the focused pane by ID.
func (m *Model) SetFocusedPane(id pane.PaneID) {
	if _, ok := m.paneFiles[id]; ok && id != m.focusedPane {
		m.focusedPane = id
		m.viewDirty = true
		m.fileListDirty = true
	}
}

// UnionFiles returns the union file list.
func (m *Model) UnionFiles() []diffview.UnionFileEntry { return m.unionFiles }

// SelectedFile returns the file list cursor position.
func (m *Model) SelectedFile() int { return m.selectedFile }

// SetSelectedFile sets the file list cursor.
func (m *Model) SetSelectedFile(idx int) {
	if idx >= 0 && idx < len(m.unionFiles) {
		m.selectedFile = idx
		m.fileListDirty = true
	}
}

// HighlightPath returns the path highlighted in the pane.
func (m *Model) HighlightPath() string { return m.highlightPath }

// FileListFocused returns whether the file list has focus.
func (m *Model) FileListFocused() bool { return m.fileListFocused }

// SetFileListFocused sets file list focus state.
func (m *Model) SetFileListFocused(f bool) {
	m.fileListFocused = f
	m.fileListDirty = true
}

// FileSearchActive returns whether file search is active.
func (m *Model) FileSearchActive() bool { return m.fileSearchActive }

// FindBarActive reports whether the find bar is open.
func (m *Model) FindBarActive() bool { return m.findActive }

// SetBounceOffset updates the visual bounce displacement.
func (m *Model) SetBounceOffset(offset int) {
	if m.bounceOffset == offset {
		return
	}
	m.bounceOffset = offset
	m.viewDirty = true
}

// BounceOffset returns the current bounce offset.
func (m *Model) BounceOffset() int { return m.bounceOffset }

// SetFileListBounceOffset updates the file list bounce displacement.
func (m *Model) SetFileListBounceOffset(offset int) {
	if m.fileListBounceOffset == offset {
		return
	}
	m.fileListBounceOffset = offset
	m.fileListDirty = true
}

// ---------------------------------------------------------------------------
// Scroll
// ---------------------------------------------------------------------------

// ScrollUp scrolls the focused pane up. Returns false at boundary.
func (m *Model) ScrollUp() bool {
	mp := m.focusedMergePane()
	if mp == nil || mp.scrollOffset <= 0 {
		return false
	}
	mp.scrollUp(1)
	m.syncHighlightFromScroll()
	m.viewDirty = true
	return true
}

// ScrollDown scrolls the focused pane down. Returns false at boundary.
func (m *Model) ScrollDown() bool {
	mp := m.focusedMergePane()
	if mp == nil || mp.scrollOffset >= mp.maxScroll() {
		return false
	}
	mp.scrollDown(1)
	m.syncHighlightFromScroll()
	m.viewDirty = true
	return true
}

// ScrollFileListUp moves the file cursor up. Returns false at boundary.
func (m *Model) ScrollFileListUp() bool {
	if m.selectedFile <= 0 {
		return false
	}
	m.selectedFile--
	m.fileListDirty = true
	return true
}

// ScrollFileListDown moves the file cursor down. Returns false at boundary.
func (m *Model) ScrollFileListDown() bool {
	total := m.visibleFileCount()
	if m.selectedFile+1 >= total {
		return false
	}
	m.selectedFile++
	m.fileListDirty = true
	return true
}

// syncHighlightFromScroll updates highlightPath from the file at scroll top.
func (m *Model) syncHighlightFromScroll() {
	mp := m.focusedMergePane()
	if mp == nil {
		return
	}
	idx := mp.FileIdxAtScroll()
	if idx >= 0 && idx < len(mp.filePaths) {
		m.highlightPath = mp.filePaths[idx]
		m.syncSelectedFileFromPath()
		m.fileListDirty = true
	}
}

// syncSelectedFileFromPath updates selectedFile to match highlightPath.
func (m *Model) syncSelectedFileFromPath() {
	for i, uf := range m.unionFiles {
		if uf.Path == m.highlightPath {
			m.selectedFile = i
			return
		}
	}
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

// focusedMergePane returns the currently focused pane, or nil.
func (m *Model) focusedMergePane() *MergePane {
	return m.paneFiles[m.focusedPane]
}

// handleKey processes keyboard input.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if m.merging {
		return nil // Consume all keys while merge runs.
	}
	if m.mergeErr != "" {
		return m.handleMergeErrorKey(key)
	}
	if m.findActive && m.findBar != nil {
		return m.handleFindKey(key)
	}
	if m.toolbarFocused {
		return m.handleToolbarKey(key)
	}
	return m.dispatchMainKey(key.String())
}

// handleMergeErrorKey handles keys during merge error state.
func (m *Model) handleMergeErrorKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "enter", " ":
		return exitMergeDiffCmd
	case "esc", "q":
		return exitMergeDiffCmd
	case "tab":
		m.toolbarAction = 0
		m.toolbarFocused = true
		m.viewDirty = true
	case "shift+tab":
		m.toolbarAction = 0
		m.toolbarFocused = true
		m.viewDirty = true
	}
	return nil
}

// dispatchMainKey dispatches a key to the main key handler.
func (m *Model) dispatchMainKey(ks string) tea.Cmd {
	if action, ok := mergeKeyActions[ks]; ok {
		return action(m)
	}
	return nil
}

// scrollFocused applies a scroll action to the focused pane.
func (m *Model) scrollFocused(action func(*MergePane)) {
	if mp := m.focusedMergePane(); mp != nil {
		action(mp)
		m.syncHighlightFromScroll()
		m.viewDirty = true
	}
}

// mergeKeyActions maps keys to actions.
var mergeKeyActions = map[string]func(*Model) tea.Cmd{
	"j":      func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollDown(1) }); return nil },
	"down":   func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollDown(1) }); return nil },
	"k":      func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollUp(1) }); return nil },
	"up":     func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollUp(1) }); return nil },
	"g":      func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollToTop() }); return nil },
	"home":   func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollToTop() }); return nil },
	"G":      func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollToBottom() }); return nil },
	"end":    func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollToBottom() }); return nil },
	"pgdown": func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollDown(mp.viewportHeight() / 2) }); return nil },
	"ctrl+d": func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollDown(mp.viewportHeight() / 2) }); return nil },
	"pgup":   func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollUp(mp.viewportHeight() / 2) }); return nil },
	"ctrl+u": func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollUp(mp.viewportHeight() / 2) }); return nil },
	"]h":     func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.jumpNextHunk() }); return nil },
	"[h":     func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.jumpPrevHunk() }); return nil },
	"]f":     func(m *Model) tea.Cmd { m.focusNextPane(); return nil },
	"[f":     func(m *Model) tea.Cmd { m.focusPrevPane(); return nil },
	"t":      func(m *Model) tea.Cmd { m.toggleViewMode(); return nil },
	"q":      func(m *Model) tea.Cmd { return exitMergeDiffCmd },
	"esc":    func(m *Model) tea.Cmd { return exitMergeDiffCmd },
	"tab":    func(m *Model) tea.Cmd { m.tabForward(); return nil },
	"shift+tab": func(m *Model) tea.Cmd { m.tabBackward(); return nil },
}

// exitMergeDiffCmd emits the ExitMergeDiffViewMsg.
func exitMergeDiffCmd() tea.Msg { return ExitMergeDiffViewMsg{} }

// toggleViewMode switches between side-by-side and unified.
func (m *Model) toggleViewMode() {
	m.sideBySide = !m.sideBySide
	m.rebuildAllPanes()
}

// ---------------------------------------------------------------------------
// Toolbar key handling
// ---------------------------------------------------------------------------

// handleToolbarKey handles keys when toolbar is focused.
func (m *Model) handleToolbarKey(key tea.KeyMsg) tea.Cmd {
	if action, ok := mergeToolbarKeyActions[key.String()]; ok {
		return action(m)
	}
	return nil
}

var mergeToolbarKeyActions = map[string]func(*Model) tea.Cmd{
	"tab":       func(m *Model) tea.Cmd { m.tabForward(); return nil },
	"shift+tab": func(m *Model) tea.Cmd { m.tabBackward(); return nil },
	"l":         func(m *Model) tea.Cmd { m.advanceToolbarAction(); return nil },
	"right":     func(m *Model) tea.Cmd { m.advanceToolbarAction(); return nil },
	"h":         func(m *Model) tea.Cmd { m.retreatToolbarAction(); return nil },
	"left":      func(m *Model) tea.Cmd { m.retreatToolbarAction(); return nil },
	"esc":       func(m *Model) tea.Cmd { m.toolbarFocused = false; m.viewDirty = true; return nil },
	"enter":     func(m *Model) tea.Cmd { return m.activateToolbarButton(m.toolbarAction) },
	" ":         func(m *Model) tea.Cmd { return m.activateToolbarButton(m.toolbarAction) },
	"q":         func(m *Model) tea.Cmd { return exitMergeDiffCmd },
	"j":         func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollDown(1) }); return nil },
	"down":      func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollDown(1) }); return nil },
	"k":         func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollUp(1) }); return nil },
	"up":        func(m *Model) tea.Cmd { m.scrollFocused(func(mp *MergePane) { mp.scrollUp(1) }); return nil },
}

// advanceToolbarAction moves to the next button.
func (m *Model) advanceToolbarAction() {
	m.toolbarAction = (m.toolbarAction + 1) % mergeToolbarButtonCount
	m.viewDirty = true
}

// retreatToolbarAction moves to the previous button.
func (m *Model) retreatToolbarAction() {
	m.toolbarAction = (m.toolbarAction - 1 + mergeToolbarButtonCount) % mergeToolbarButtonCount
	m.viewDirty = true
}

// activateToolbarButton triggers a toolbar action.
func (m *Model) activateToolbarButton(idx int) tea.Cmd {
	switch idx {
	case mtbMerge:
		return func() tea.Msg { return MergeBranchMsg{DeleteSource: false} }
	case mtbMergeDelete:
		return func() tea.Msg { return MergeBranchMsg{DeleteSource: true} }
	case mtbAbort:
		return exitMergeDiffCmd
	case mtbSBS:
		m.sideBySide = true
		m.rebuildAllPanes()
	case mtbUnified:
		m.sideBySide = false
		m.rebuildAllPanes()
	}
	return nil
}

// tabForward cycles Tab through toolbar.
func (m *Model) tabForward() {
	if m.toolbarFocused {
		if m.toolbarAction+1 < mergeToolbarButtonCount {
			m.toolbarAction++
		} else {
			m.toolbarFocused = false
		}
	} else {
		m.toolbarFocused = true
		m.toolbarAction = 0
	}
	m.viewDirty = true
}

// tabBackward cycles Shift+Tab in reverse.
func (m *Model) tabBackward() {
	if m.toolbarFocused {
		if m.toolbarAction > 0 {
			m.toolbarAction--
		} else {
			m.toolbarFocused = false
		}
	} else {
		m.toolbarFocused = true
		m.toolbarAction = mergeToolbarButtonCount - 1
	}
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// Pane navigation
// ---------------------------------------------------------------------------

// focusNextPane moves focus to the next pane.
func (m *Model) focusNextPane() {
	if id, ok := m.findAdjacentLeaf(1); ok {
		m.applyPaneFocus(id)
	}
}

// focusPrevPane moves focus to the previous pane.
func (m *Model) focusPrevPane() {
	if id, ok := m.findAdjacentLeaf(-1); ok {
		m.applyPaneFocus(id)
	}
}

// findAdjacentLeaf finds the adjacent leaf pane.
func (m *Model) findAdjacentLeaf(delta int) (pane.PaneID, bool) {
	if m.paneTree == nil {
		return 0, false
	}
	leaves := m.paneTree.Leaves()
	idx := -1
	for i, id := range leaves {
		if id == m.focusedPane {
			idx = i
			break
		}
	}
	adj := idx + delta
	if idx < 0 || adj < 0 || adj >= len(leaves) {
		return 0, false
	}
	return leaves[adj], true
}

// applyPaneFocus sets focus to a pane.
func (m *Model) applyPaneFocus(id pane.PaneID) {
	m.focusedPane = id
	m.toolbarFocused = false
	m.viewDirty = true
	m.fileListDirty = true
}

// ---------------------------------------------------------------------------
// Mouse handling
// ---------------------------------------------------------------------------

// handleMouse processes mouse events.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.merging {
		return nil // Consume all mouse events while merge runs.
	}
	if m.mergeErr != "" {
		return m.handleMergeErrorMouse(msg)
	}
	if msg.Action == tea.MouseActionPress {
		return m.handleMousePress(msg)
	}
	if msg.Action == tea.MouseActionMotion {
		m.handleMouseMotion(msg)
	}
	return nil
}

// handleMergeErrorMouse handles mouse events during merge error state.
func (m *Model) handleMergeErrorMouse(msg tea.MouseMsg) tea.Cmd {
	tbRow := m.height - toolbarHeight
	if msg.Action == tea.MouseActionMotion && msg.Y >= tbRow {
		idx := mergeErrorToolbarHitTest(m.width, msg.X)
		if idx != m.hoverBtnIdx {
			m.hoverBtnIdx = idx
			m.viewDirty = true
		}
		return nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y >= tbRow {
		if mergeErrorToolbarHitTest(m.width, msg.X) == 0 {
			return exitMergeDiffCmd
		}
	}
	return nil
}

// handleMousePress dispatches press events.
func (m *Model) handleMousePress(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.handleMouseWheel(msg, true)
	case tea.MouseButtonWheelDown:
		m.handleMouseWheel(msg, false)
	case tea.MouseButtonLeft:
		return m.handleMouseLeftClick(msg)
	}
	return nil
}

// handleMouseWheel handles scroll wheel events.
func (m *Model) handleMouseWheel(msg tea.MouseMsg, up bool) {
	mp := m.paneAtY(msg.Y)
	if mp == nil {
		return
	}
	if up {
		mp.scrollUp(3)
	} else {
		mp.scrollDown(3)
	}
	m.syncHighlightFromScroll()
	m.viewDirty = true
}

// handleMouseLeftClick handles left clicks.
func (m *Model) handleMouseLeftClick(msg tea.MouseMsg) tea.Cmd {
	tbRow := m.height - toolbarHeight
	if msg.Y < tbRow {
		m.focusPaneAtY(msg.Y)
		return nil
	}
	idx := mergeToolbarHitTest(m.width, msg.X)
	if idx >= 0 {
		return m.activateToolbarButton(idx)
	}
	return nil
}

// handleMouseMotion handles mouse motion for hover tracking.
func (m *Model) handleMouseMotion(msg tea.MouseMsg) {
	tbRow := m.height - toolbarHeight
	if msg.Y >= tbRow {
		idx := mergeToolbarHitTest(m.width, msg.X)
		if idx != m.hoverBtnIdx {
			m.hoverBtnIdx = idx
			m.viewDirty = true
		}
		return
	}
	if m.hoverBtnIdx >= 0 {
		m.hoverBtnIdx = -1
		m.viewDirty = true
	}
}

// paneAtY returns the MergePane at the given view Y coordinate.
func (m *Model) paneAtY(viewY int) *MergePane {
	if m.paneTree == nil || viewY < 0 {
		return nil
	}
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	rects := m.paneTree.ComputeLayout(area)
	for id, r := range rects {
		if viewY >= r.Y && viewY < r.Y+r.H {
			return m.paneFiles[id]
		}
	}
	return nil
}

// focusPaneAtY sets focus to the pane at the given view Y.
func (m *Model) focusPaneAtY(viewY int) {
	if m.paneTree == nil || viewY < 0 {
		return
	}
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	rects := m.paneTree.ComputeLayout(area)
	for id, r := range rects {
		if viewY >= r.Y && viewY < r.Y+r.H && id != m.focusedPane {
			m.applyPaneFocus(id)
			return
		}
	}
}

// IsPaneClick reports whether a view Y is inside the pane content area.
func (m *Model) IsPaneClick(y int) bool {
	tbRow := m.height - toolbarHeight
	return y >= 0 && y < tbRow
}

// scrollToFile scrolls the focused pane to show the file at the given index.
func (m *Model) scrollToFile(idx int) {
	mp := m.focusedMergePane()
	if mp == nil {
		return
	}
	mp.ScrollToFile(idx)
	if idx >= 0 && idx < len(mp.filePaths) {
		m.highlightPath = mp.filePaths[idx]
	}
	m.viewDirty = true
	m.fileListDirty = true
}
