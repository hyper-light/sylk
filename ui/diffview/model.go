package diffview

import (
	"slices"
	"strings"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/editor/findbar"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/tabbar"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// toolbarHeight is the number of lines consumed by the toolbar (border + content).
const toolbarHeight = 2

// maxVisiblePanes caps the number of simultaneously visible pair panes.
// Derived from: 4 stacked panes is the practical limit for readable diffs.
const maxVisiblePanes = 4

// Model is the diff view component state.
type Model struct {
	pairs      []DiffPair
	mode       CompareMode
	sideBySide bool

	// All-pairs data: one PairData per pair, plus union file list.
	pairData   []PairData
	unionFiles []UnionFileEntry

	// Currently displayed file across all panes.
	selectedPath string

	// Tab bar: persistent file tabs for quick switching.
	openTabs      []string // ordered file paths
	activeTabIdx  int
	tabHoverClose int // hovered close button index, -1 = none
	nerdFonts     bool

	// Pane tree for per-pair sub-panes (horizontal splits).
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

	// File list search state (mirrors filetree search UI).
	fileSearchActive bool
	fileSearchQuery  []rune
	fileSearchFocus  fileSearchFocusZone
	fileToggleActive [fileToggleCount]bool
	fileToggleCursor fileToggleIdx
	fileScopeQuery   []rune
	fileScopeCursor  int
	filteredIndices  []int // indices into unionFiles; nil = show all

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

	// Loading state: shown while diff pairs are being fetched.
	loading      bool
	spinnerFrame int

	// In-file find bar for the focused diff pane.
	findBar     *findbar.FindBar
	findActive  bool
	findContent []rune
	findLines   []findLineInfo
}

// New creates a diff view model from pre-computed diff pairs.
func New(pairs []DiffPair, mode CompareMode, th *theme.Theme, nerdFonts bool) *Model {
	hl := codepkg.NewHighlighter(th)
	m := &Model{
		pairs:         pairs,
		mode:          mode,
		sideBySide:    true,
		theme:         th,
		nerdFonts:     nerdFonts,
		hoverBtnIdx:   -1,
		tabHoverClose: -1,
		highlighter:   hl,
		syntaxStyles:  th.Syntax,
		defaultSt:     lipgloss.NewStyle().Foreground(th.Palette.Foreground),
		paneFiles:     make(map[pane.PaneID]*FileDiffPane),
	}
	if len(pairs) > 0 {
		m.loadAllPairs()
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

// SetLoading sets the loading state and resets the spinner frame.
func (m *Model) SetLoading(loading bool) {
	if m.loading == loading {
		return
	}
	m.loading = loading
	m.spinnerFrame = 0
	m.viewDirty = true
}

// Loading returns whether the diff view is in loading state.
func (m *Model) Loading() bool { return m.loading }

// NeedsDecorTick reports whether the loading spinner needs decor-rate ticking.
func (m *Model) NeedsDecorTick() bool { return m.loading }

// AdvanceSpinner advances the loading spinner frame and marks view dirty.
func (m *Model) AdvanceSpinner() {
	m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
	m.viewDirty = true
}

// spinnerFrames are the braille spinner glyphs used during loading.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerColor is the light sky blue used for the loading spinner glyph.
// CSS "lightskyblue" (#87CEFA).
var spinnerColor = lipgloss.Color("#87CEFA")

// renderLoadingSpinner renders a centered "Loading diffs…" spinner
// block of the given height. Uses the frame counter advanced by
// AdvanceSpinner on each decor tick.
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

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// renderFindBarSection returns the rendered find bar string, or empty.
func (m *Model) renderFindBarSection(cursorVisible bool) string {
	if m.findActive && m.findBar != nil {
		return m.findBar.View(m.width, m.theme, cursorVisible)
	}
	return ""
}

// assembleViewParts joins tab bar (or blank filler), middle content, and toolbar.
func (m *Model) assembleViewParts(tabBarStr, middle, toolbar string) string {
	if tabBarStr != "" {
		return tabBarStr + "\n" + middle + "\n" + toolbar
	}
	blank := strings.Repeat(" ", m.width)
	return blank + "\n" + blank + "\n" + middle + "\n" + toolbar
}

// View renders the diff view right panel: tab bar + pane tree + [find bar] + toolbar.
func (m *Model) View(cursorVisible bool) string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	p := m.theme.Palette
	toolbar := renderToolbar(m.mode, m.sideBySide, m.toolbarFocused,
		m.toolbarAction, m.hoverBtnIdx, m.width, p)

	if m.loading {
		area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
		// header filler (2 lines) + spinner content + toolbar
		headerH := tabbar.Height
		spinH := area.H + headerH
		content := m.renderLoadingSpinner(spinH)
		return content + "\n" + toolbar
	}

	tabBarStr := m.renderTabBarIfOpen()
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	content := m.composeDiffPanes(area)
	findBarStr := m.renderFindBarSection(cursorVisible)
	middle := joinWithNewline(content, findBarStr)
	return m.assembleViewParts(tabBarStr, middle, toolbar)
}

// joinWithNewline joins two strings with a newline separator.
// If suffix is empty, returns the prefix unchanged.
func joinWithNewline(prefix, suffix string) string {
	if suffix == "" {
		return prefix
	}
	return prefix + "\n" + suffix
}

// renderTabBarIfOpen returns the rendered tab bar, or empty if no tabs.
func (m *Model) renderTabBarIfOpen() string {
	if m.tabBarHeight() > 0 {
		return m.renderTabBar()
	}
	return ""
}

// tabBarHeight returns the height consumed by the tab bar.
// Always reserves tabbar.Height (2) for layout stability.
func (m *Model) tabBarHeight() int {
	if len(m.openTabs) == 0 {
		return 0
	}
	return tabbar.Height
}

// viewportHeight returns the height available for pane content.
// Layout: tabBar/header (2) + content (H) + [findBar (2)] + toolbar (2) = m.height.
func (m *Model) viewportHeight() int {
	headerH := tabbar.Height // always 2, whether tab bar or blank filler
	h := max(m.height-headerH-toolbarHeight, 1)
	if m.findActive {
		h = max(h-diffFindBarHeight, 1)
	}
	return h
}

// renderTabBar renders the file tab bar using the shared tabbar package.
func (m *Model) renderTabBar() string {
	tabs := make([]tabbar.Tab, len(m.openTabs))
	for i, path := range m.openTabs {
		tabs[i] = tabbar.Tab{Path: path}
	}
	cfg := tabbar.Config{
		Tabs:       tabs,
		Active:     m.activeTabIdx,
		Width:      m.width,
		NerdFonts:  m.nerdFonts,
		Theme:      m.theme,
		HoverClose: m.tabHoverClose,
		Focused:    m.focused,
	}
	return tabbar.View(cfg)
}

// tabBarConfig returns the current tab bar config (for hit testing).
func (m *Model) tabBarConfig() tabbar.Config {
	tabs := make([]tabbar.Tab, len(m.openTabs))
	for i, path := range m.openTabs {
		tabs[i] = tabbar.Tab{Path: path}
	}
	return tabbar.Config{
		Tabs:       tabs,
		Active:     m.activeTabIdx,
		Width:      m.width,
		NerdFonts:  m.nerdFonts,
		Theme:      m.theme,
		HoverClose: m.tabHoverClose,
		Focused:    m.focused,
	}
}

// ---------------------------------------------------------------------------
// composeDiffPanes — split into phases
// ---------------------------------------------------------------------------

// compositeSeg is a positioned string segment for row-by-row composition.
type compositeSeg struct {
	x int
	s string
}

// emptyPaneBlock renders a blank area when no pane tree exists.
func emptyPaneBlock(area pane.Rect) string {
	blank := strings.Repeat(" ", max(area.W, 0))
	rows := make([]string, max(area.H, 0))
	for i := range rows {
		rows[i] = blank
	}
	return strings.Join(rows, "\n")
}

// renderLeafBlank renders blank lines for a leaf with no valid pane data.
func renderLeafBlank(r pane.Rect) []string {
	lines := make([]string, r.H)
	blank := strings.Repeat(" ", r.W)
	for i := range lines {
		lines[i] = blank
	}
	return lines
}

// leafHasData reports whether a leaf pane has valid pair data to render.
func (m *Model) leafHasData(id pane.PaneID) bool {
	fp := m.paneFiles[id]
	return fp != nil && fp.pairIdx < len(m.pairs)
}

// renderLeafPanes renders each leaf pane into lines.
func (m *Model) renderLeafPanes(leaves []pane.PaneID, rects map[pane.PaneID]pane.Rect) map[pane.PaneID][]string {
	p := m.theme.Palette
	leafLines := make(map[pane.PaneID][]string, len(leaves))
	for _, id := range leaves {
		if !m.leafHasData(id) {
			leafLines[id] = renderLeafBlank(rects[id])
			continue
		}
		fp := m.paneFiles[id]
		rendered := fp.View(m.pairs[fp.pairIdx], m.pairData[fp.pairIdx], m.selectedPath, p, id == m.focusedPane)
		leafLines[id] = strings.Split(rendered, "\n")
	}
	return leafLines
}

// rectContainsY reports whether row y falls within rect r.
func rectContainsY(y int, r pane.Rect) bool {
	return y >= r.Y && y < r.Y+r.H
}

// collectPaneSegs collects content segments from leaf panes for a given row Y.
func collectPaneSegs(y int, leaves []pane.PaneID, rects map[pane.PaneID]pane.Rect, leafLines map[pane.PaneID][]string) []compositeSeg {
	var segs []compositeSeg
	for _, id := range leaves {
		r := rects[id]
		if !rectContainsY(y, r) {
			continue
		}
		segs = append(segs, compositeSeg{r.X, lineAtOffset(leafLines[id], y-r.Y)})
	}
	return segs
}

// lineAtOffset returns the line at idx, or empty if out of bounds.
func lineAtOffset(lines []string, idx int) string {
	if idx < len(lines) {
		return lines[idx]
	}
	return ""
}

// collectDividerSegs collects divider segments that intersect a given row Y.
func collectDividerSegs(y int, dividers []pane.Divider, divStyle lipgloss.Style) []compositeSeg {
	var segs []compositeSeg
	for _, d := range dividers {
		seg, ok := dividerSegAt(y, d, divStyle)
		if ok {
			segs = append(segs, seg)
		}
	}
	return segs
}

// vertDividerVisible reports whether a vertical divider intersects row y.
func vertDividerVisible(y int, d pane.Divider) bool {
	return d.Dir == pane.SplitVertical && y >= d.Y && y < d.Y+d.Len
}

// horizDividerVisible reports whether a horizontal divider is at row y.
func horizDividerVisible(y int, d pane.Divider) bool {
	return d.Dir == pane.SplitHorizontal && y == d.Y
}

// dividerSegAt returns the segment for a divider at row y, if visible.
func dividerSegAt(y int, d pane.Divider, divStyle lipgloss.Style) (compositeSeg, bool) {
	if vertDividerVisible(y, d) {
		return compositeSeg{d.X, divStyle.Render("│")}, true
	}
	if horizDividerVisible(y, d) {
		return compositeSeg{d.X, divStyle.Render(strings.Repeat("─", d.Len))}, true
	}
	return compositeSeg{}, false
}

// assembleRow builds one row from sorted segments, padded to width.
func assembleRow(segs []compositeSeg, width int) string {
	slices.SortFunc(segs, func(a, b compositeSeg) int { return a.x - b.x })
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

// paneTreeEmpty reports whether the pane tree is nil or the area is degenerate.
func (m *Model) paneTreeEmpty(area pane.Rect) bool {
	return m.paneTree == nil || area.H <= 0 || area.W <= 0
}

// composeDiffPanes renders the pane tree into a block of text using
// row-by-row composition. Mirrors composePanes() from app.go.
func (m *Model) composeDiffPanes(area pane.Rect) string {
	if m.paneTreeEmpty(area) {
		return emptyPaneBlock(area)
	}

	rects := m.paneTree.ComputeLayout(area)
	dividers := m.paneTree.Dividers(area)
	leaves := m.paneTree.Leaves()
	divStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)
	leafLines := m.renderLeafPanes(leaves, rects)

	rows := make([]string, area.H)
	for y := range area.H {
		segs := collectPaneSegs(y, leaves, rects, leafLines)
		segs = append(segs, collectDividerSegs(y, dividers, divStyle)...)
		rows[y] = assembleRow(segs, area.W)
	}
	return strings.Join(rows, "\n")
}

// ---------------------------------------------------------------------------
// Data loading
// ---------------------------------------------------------------------------

// buildPairData computes PairData for a single pair.
func (m *Model) buildPairData(pair DiffPair) PairData {
	blocks := BuildFileBlocks(pair.Files)
	highlights := buildFileHighlights(blocks, m.highlighter)
	pathIndex := make(map[string]int, len(blocks))
	for j, fb := range blocks {
		pathIndex[fb.Path] = j
	}
	return PairData{Blocks: blocks, Highlights: highlights, PathIndex: pathIndex}
}

// selectFirstFile selects the first file and opens it as a tab.
func (m *Model) selectFirstFile() {
	if len(m.unionFiles) > 0 {
		m.selectedPath = m.unionFiles[0].Path
		m.openTabs = []string{m.selectedPath}
	} else {
		m.selectedPath = ""
		m.openTabs = nil
	}
	m.activeTabIdx = 0
	m.selectedFile = 0
	m.fileListScroll = 0
	m.fileListDirty = true
}

// loadAllPairs computes PairData for every pair, builds the union file list,
// opens the first file as a tab, and constructs the pane tree.
func (m *Model) loadAllPairs() {
	m.pairData = make([]PairData, len(m.pairs))
	for i, pair := range m.pairs {
		m.pairData[i] = m.buildPairData(pair)
	}
	m.unionFiles = buildUnionFileList(m.pairData)
	m.selectFirstFile()
	m.buildPaneTree()
	m.sizePanes()
	m.rebuildAllPanes()
}

// ReloadPairs replaces pair data and mode while preserving open tabs and
// the active tab selection. Tabs whose paths no longer exist in the new
// union file list are pruned. If the previously selected path survives,
// it stays selected; otherwise the first surviving tab is activated.
func (m *Model) ReloadPairs(pairs []DiffPair, mode CompareMode) {
	savedTabs := m.openTabs
	savedPath := m.selectedPath
	savedActiveIdx := m.activeTabIdx

	m.pairs = pairs
	m.mode = mode
	m.pairData = make([]PairData, len(m.pairs))
	for i, pair := range m.pairs {
		m.pairData[i] = m.buildPairData(pair)
	}
	m.unionFiles = buildUnionFileList(m.pairData)

	m.restoreTabs(savedTabs, savedPath, savedActiveIdx)
	m.buildPaneTree()
	m.sizePanes()
	m.rebuildAllPanes()
}

// restoreTabs re-applies saved tabs, pruning any that no longer exist in
// the union file list, and resolves the selected path and file cursor.
func (m *Model) restoreTabs(savedTabs []string, savedPath string, savedActiveIdx int) {
	pathSet := m.unionFilePathSet()
	m.openTabs = pruneToSet(savedTabs, pathSet)

	if len(m.openTabs) == 0 {
		m.selectFirstFile()
		return
	}

	m.activeTabIdx = resolveActiveTab(m.openTabs, savedPath, savedActiveIdx)
	m.selectedPath = m.openTabs[m.activeTabIdx]
	m.selectedFile = m.findUnionFileIdx(m.selectedPath)
	if m.selectedFile < 0 {
		m.selectedFile = 0
	}
	m.fileListDirty = true
}

// unionFilePathSet builds a set of paths from the union file list.
func (m *Model) unionFilePathSet() map[string]struct{} {
	set := make(map[string]struct{}, len(m.unionFiles))
	for _, uf := range m.unionFiles {
		set[uf.Path] = struct{}{}
	}
	return set
}

// pruneToSet filters paths to only those present in the set,
// preserving order.
func pruneToSet(paths []string, set map[string]struct{}) []string {
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, ok := set[p]; ok {
			result = append(result, p)
		}
	}
	return result
}

// resolveActiveTab picks the best active tab index after pruning.
// Prefers the tab matching savedPath; falls back to clamped savedActiveIdx.
func resolveActiveTab(tabs []string, savedPath string, savedActiveIdx int) int {
	for i, t := range tabs {
		if t == savedPath {
			return i
		}
	}
	return min(savedActiveIdx, len(tabs)-1)
}

// mergeIntoEntry merges a file block's stats into an existing union entry.
func mergeIntoEntry(e *UnionFileEntry, fb FileBlock) {
	e.Additions += fb.Additions
	e.Deletions += fb.Deletions
	e.Status = higherPriorityStatus(e.Status, fb.Status)
	e.PairCount++
	if e.OldPath == "" && fb.OldPath != "" {
		e.OldPath = fb.OldPath
	}
}

// newUnionEntry creates a new union file entry from a file block.
func newUnionEntry(fb FileBlock) *UnionFileEntry {
	return &UnionFileEntry{
		Path:      fb.Path,
		OldPath:   fb.OldPath,
		Status:    fb.Status,
		Additions: fb.Additions,
		Deletions: fb.Deletions,
		PairCount: 1,
	}
}

// mergeFileBlock merges a file block into the entries map, appending to order
// for new entries.
func mergeFileBlock(fb FileBlock, entries map[string]*UnionFileEntry, order *[]string) {
	if e, ok := entries[fb.Path]; ok {
		mergeIntoEntry(e, fb)
		return
	}
	entries[fb.Path] = newUnionEntry(fb)
	*order = append(*order, fb.Path)
}

// collectUnionEntries iterates all pair data blocks and merges them into
// entries, appending new paths to order.
func collectUnionEntries(pairData []PairData) (map[string]*UnionFileEntry, []string) {
	entries := make(map[string]*UnionFileEntry)
	var order []string
	for _, pd := range pairData {
		for _, fb := range pd.Blocks {
			mergeFileBlock(fb, entries, &order)
		}
	}
	return entries, order
}

// buildUnionFileList deduplicates file paths across all pairs and aggregates
// stats. Preserves first-seen order.
func buildUnionFileList(pairData []PairData) []UnionFileEntry {
	entries, order := collectUnionEntries(pairData)
	result := make([]UnionFileEntry, len(order))
	for i, path := range order {
		result[i] = *entries[path]
	}
	return result
}

// buildPaneTree creates a balanced horizontal split tree with one pane per
// visible pair.
func (m *Model) buildPaneTree() {
	m.paneFiles = make(map[pane.PaneID]*FileDiffPane)
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
		m.paneFiles[ids[i]] = newFileDiffPane(i)
	}

	m.paneTree = buildQuadrantTree(ids)
	m.focusedPane = ids[0]
}

// verticalPair creates a vertical split node from two pane IDs.
func verticalPair(left, right pane.PaneID) *pane.Node {
	return &pane.Node{
		Dir:   pane.SplitVertical,
		Ratio: 0.5,
		Left:  pane.NewLeaf(left),
		Right: pane.NewLeaf(right),
	}
}

// quadrantBottom creates the bottom half of a quadrant tree.
func quadrantBottom(ids []pane.PaneID) *pane.Node {
	if len(ids) > 3 {
		return verticalPair(ids[2], ids[3])
	}
	return pane.NewLeaf(ids[2])
}

// buildQuadrantTree creates a grid layout tree using both horizontal and
// vertical splits: 1=leaf, 2=side-by-side, 3=top-pair+bottom, 4=2x2 grid.
func buildQuadrantTree(ids []pane.PaneID) *pane.Node {
	if len(ids) == 1 {
		return pane.NewLeaf(ids[0])
	}
	top := verticalPair(ids[0], ids[1])
	if len(ids) == 2 {
		return top
	}
	return &pane.Node{Dir: pane.SplitHorizontal, Ratio: 0.5, Left: top, Right: quadrantBottom(ids)}
}

// hasPaneLayout reports whether the model has a pane tree and valid dimensions.
func (m *Model) hasPaneLayout() bool {
	return m.paneTree != nil && m.width > 0 && m.height > 0
}

// applyPaneRects applies computed rects to each pane in paneFiles.
func (m *Model) applyPaneRects(rects map[pane.PaneID]pane.Rect) {
	for id, fp := range m.paneFiles {
		if r, ok := rects[id]; ok {
			fp.SetSize(r.W, r.H)
		}
	}
}

// sizePanes distributes available space to each pair pane via ComputeLayout.
func (m *Model) sizePanes() {
	if !m.hasPaneLayout() {
		return
	}
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	m.applyPaneRects(m.paneTree.ComputeLayout(area))
}

// rebuildPane rebuilds a single pane's diff lines for the selected file.
func (m *Model) rebuildPane(fp *FileDiffPane, maxOld, maxNew int) {
	if fp.pairIdx >= len(m.pairData) {
		return
	}
	pd := m.pairData[fp.pairIdx]
	fp.RebuildForPath(m.selectedPath, pd, m.sideBySide, maxOld, maxNew,
		m.syntaxStyles, m.defaultSt, m.theme.Palette)
}

// hasDimensions reports whether the model has positive width and height.
func (m *Model) hasDimensions() bool {
	return m.width > 0 && m.height > 0
}

// rebuildAllPanes re-renders every pair pane's diff lines for the selected file.
func (m *Model) rebuildAllPanes() {
	if !m.hasDimensions() {
		return
	}
	maxOld, maxNew := m.maxLineNoForSelectedFile()
	for _, fp := range m.paneFiles {
		m.rebuildPane(fp, maxOld, maxNew)
	}
	m.viewDirty = true
}

// maxLineNoForSelectedFile returns the highest line numbers for the selected
// file across all pairs, ensuring consistent gutter widths.
func (m *Model) maxLineNoForSelectedFile() (maxOld, maxNew int) {
	for _, pd := range m.pairData {
		if idx, ok := pd.PathIndex[m.selectedPath]; ok {
			o, n := maxLineNoForFile(pd.Blocks[idx])
			maxOld = max(maxOld, o)
			maxNew = max(maxNew, n)
		}
	}
	return
}

// validFileIdx reports whether idx is a valid index into unionFiles.
func (m *Model) validFileIdx(idx int) bool {
	return idx >= 0 && idx < len(m.unionFiles)
}

// SelectFile sets the selected file by index in the union file list and
// rebuilds all panes to show it.
func (m *Model) SelectFile(idx int) {
	if !m.validFileIdx(idx) {
		return
	}
	path := m.unionFiles[idx].Path
	if path == m.selectedPath {
		return
	}
	m.selectedFile = idx
	m.selectedPath = path
	m.resetPaneScrolls()
	m.rebuildAllPanes()
	m.rebuildFindIfActive()
	m.fileListDirty = true
	m.viewDirty = true
}

// resetPaneScrolls resets scroll offsets on all panes (for file switch).
func (m *Model) resetPaneScrolls() {
	for _, fp := range m.paneFiles {
		fp.scrollOffset = 0
	}
}

// ---------------------------------------------------------------------------
// Tab management
// ---------------------------------------------------------------------------

// findTabIdx returns the index of path in openTabs, or -1 if not found.
func (m *Model) findTabIdx(path string) int {
	for i, t := range m.openTabs {
		if t == path {
			return i
		}
	}
	return -1
}

// findUnionFileIdx returns the index of path in unionFiles, or -1 if not found.
func (m *Model) findUnionFileIdx(path string) int {
	for i, uf := range m.unionFiles {
		if uf.Path == path {
			return i
		}
	}
	return -1
}

// OpenFileAsTab opens the file at the given union file index as a persistent
// tab, switching to it and updating all panes.
func (m *Model) OpenFileAsTab(idx int) {
	if !m.validFileIdx(idx) {
		return
	}
	path := m.unionFiles[idx].Path
	if ti := m.findTabIdx(path); ti >= 0 {
		m.activeTabIdx = ti
		m.SelectFile(idx)
		return
	}
	m.openTabs = append(m.openTabs, path)
	m.activeTabIdx = len(m.openTabs) - 1
	m.SelectFile(idx)
}

// validTabIdx reports whether idx is a valid index into openTabs.
func (m *Model) validTabIdx(idx int) bool {
	return idx >= 0 && idx < len(m.openTabs)
}

// switchToTab activates the tab at the given index.
func (m *Model) switchToTab(idx int) {
	if !m.validTabIdx(idx) {
		return
	}
	m.activeTabIdx = idx
	if fi := m.findUnionFileIdx(m.openTabs[idx]); fi >= 0 {
		m.SelectFile(fi)
	}
}

// nextTab moves to the next open tab with wraparound.
func (m *Model) nextTab() {
	if len(m.openTabs) <= 1 {
		return
	}
	m.switchToTab((m.activeTabIdx + 1) % len(m.openTabs))
}

// prevTab moves to the previous open tab with wraparound.
func (m *Model) prevTab() {
	if len(m.openTabs) <= 1 {
		return
	}
	m.switchToTab((m.activeTabIdx - 1 + len(m.openTabs)) % len(m.openTabs))
}

// adjustActiveTabAfterClose adjusts activeTabIdx after removing the tab at idx.
func (m *Model) adjustActiveTabAfterClose(idx int) {
	if idx < m.activeTabIdx {
		m.activeTabIdx--
		return
	}
	if idx == m.activeTabIdx {
		m.activeTabIdx = min(idx, len(m.openTabs)-1)
		m.switchToTab(m.activeTabIdx)
	}
}

// closeTab removes the tab at the given index. Closing the last tab
// exits the diff view. Returns a command if an exit message is emitted.
func (m *Model) closeTab(idx int) tea.Cmd {
	if !m.validTabIdx(idx) {
		return nil
	}
	if len(m.openTabs) <= 1 {
		return exitDiffViewCmd
	}
	m.openTabs = slices.Delete(m.openTabs, idx, idx+1)
	m.tabHoverClose = -1
	m.adjustActiveTabAfterClose(idx)
	m.viewDirty = true
	m.fileListDirty = true
	return nil
}

// exitDiffViewCmd emits the ExitDiffViewMsg.
func exitDiffViewCmd() tea.Msg { return ExitDiffViewMsg{} }

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

// ---------------------------------------------------------------------------
// handleKey — dispatch map pattern
// ---------------------------------------------------------------------------

// findBarReady reports whether the find bar is active and ready.
func (m *Model) findBarReady() bool {
	return m.findActive && m.findBar != nil
}

// scrollFocusedPane applies a scroll action to the focused pane if present.
func (m *Model) scrollFocusedPane(action func(*FileDiffPane)) {
	if fp := m.focusedFileDiffPane(); fp != nil {
		action(fp)
		m.viewDirty = true
	}
}

// tabForward cycles Tab through file tabs then toolbar options.
// Order: tab0 → tab1 → … → tabN → toolbar0 → … → toolbarN → tab0.
func (m *Model) tabForward() {
	if m.toolbarFocused {
		if m.toolbarAction+1 < toolbarButtonCount {
			m.toolbarAction++
		} else {
			m.toolbarFocused = false
			m.switchToTab(0)
		}
		m.viewDirty = true
		return
	}
	if m.activeTabIdx+1 < len(m.openTabs) {
		m.switchToTab(m.activeTabIdx + 1)
	} else {
		m.toolbarFocused = true
		m.toolbarAction = 0
	}
	m.viewDirty = true
}

// tabBackward cycles Shift+Tab in reverse through toolbar then file tabs.
func (m *Model) tabBackward() {
	if m.toolbarFocused {
		if m.toolbarAction > 0 {
			m.toolbarAction--
		} else {
			m.toolbarFocused = false
			m.switchToTab(max(len(m.openTabs)-1, 0))
		}
		m.viewDirty = true
		return
	}
	if m.activeTabIdx > 0 {
		m.switchToTab(m.activeTabIdx - 1)
	} else {
		m.toolbarFocused = true
		m.toolbarAction = toolbarButtonCount - 1
	}
	m.viewDirty = true
}

// diffKeyActions maps key strings to actions for the main diff key handler.
var diffKeyActions = map[string]func(*Model) tea.Cmd{
	"j":      func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollDown(1) }); return nil },
	"down":   func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollDown(1) }); return nil },
	"k":      func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollUp(1) }); return nil },
	"up":     func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollUp(1) }); return nil },
	"g":      func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollToTop() }); return nil },
	"home":   func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollToTop() }); return nil },
	"G":      func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollToBottom() }); return nil },
	"end":    func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollToBottom() }); return nil },
	"pgdown": func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollDown(fp.viewportHeight() / 2) }); return nil },
	"ctrl+d": func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollDown(fp.viewportHeight() / 2) }); return nil },
	"pgup":   func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollUp(fp.viewportHeight() / 2) }); return nil },
	"ctrl+u": func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollUp(fp.viewportHeight() / 2) }); return nil },
	"]h":     func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.jumpNextHunk() }); return nil },
	"[h":     func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.jumpPrevHunk() }); return nil },
	"]f":     func(m *Model) tea.Cmd { m.focusNextPane(); return nil },
	"[f":     func(m *Model) tea.Cmd { m.focusPrevPane(); return nil },
	"]t":     func(m *Model) tea.Cmd { m.nextTab(); return nil },
	"[t":     func(m *Model) tea.Cmd { m.prevTab(); return nil },
	"tab":       func(m *Model) tea.Cmd { m.tabForward(); return nil },
	"shift+tab": func(m *Model) tea.Cmd { m.tabBackward(); return nil },
	"t":   func(m *Model) tea.Cmd { m.toggleViewMode(); return nil },
	"m":   func(m *Model) tea.Cmd { return m.cycleCompareMode() },
	"q":   func(m *Model) tea.Cmd { return exitDiffViewCmd },
	"esc": func(m *Model) tea.Cmd { return exitDiffViewCmd },
}

// dispatchDiffKey looks up a key in the dispatch map and executes the action.
func (m *Model) dispatchDiffKey(ks string) tea.Cmd {
	if action, ok := diffKeyActions[ks]; ok {
		return action(m)
	}
	return nil
}

// handleKey processes keyboard input.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if m.findBarReady() {
		return m.handleFindKey(key)
	}
	if m.toolbarFocused {
		return m.handleToolbarKey(key)
	}
	return m.dispatchDiffKey(key.String())
}

// ---------------------------------------------------------------------------
// handleToolbarKey — dispatch map pattern
// ---------------------------------------------------------------------------

// toolbarKeyActions maps key strings to actions when toolbar is focused.
// Vertical scroll keys are included so that scrolling the focused pane
// still works while the toolbar is selected.
var toolbarKeyActions = map[string]func(*Model) tea.Cmd{
	"tab":       func(m *Model) tea.Cmd { m.tabForward(); return nil },
	"l":         func(m *Model) tea.Cmd { m.advanceToolbarAction(); return nil },
	"right":     func(m *Model) tea.Cmd { m.advanceToolbarAction(); return nil },
	"shift+tab": func(m *Model) tea.Cmd { m.tabBackward(); return nil },
	"h":         func(m *Model) tea.Cmd { m.retreatToolbarAction(); return nil },
	"left":      func(m *Model) tea.Cmd { m.retreatToolbarAction(); return nil },
	"esc":       func(m *Model) tea.Cmd { m.toolbarFocused = false; m.viewDirty = true; return nil },
	"enter":     func(m *Model) tea.Cmd { return m.activateToolbarButton(m.toolbarAction) },
	" ":         func(m *Model) tea.Cmd { return m.activateToolbarButton(m.toolbarAction) },
	"q":         func(m *Model) tea.Cmd { return exitDiffViewCmd },
	"j":         func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollDown(1) }); return nil },
	"down":      func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollDown(1) }); return nil },
	"k":         func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollUp(1) }); return nil },
	"up":        func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollUp(1) }); return nil },
	"pgdown":    func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollDown(fp.viewportHeight() / 2) }); return nil },
	"ctrl+d":    func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollDown(fp.viewportHeight() / 2) }); return nil },
	"pgup":      func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollUp(fp.viewportHeight() / 2) }); return nil },
	"ctrl+u":    func(m *Model) tea.Cmd { m.scrollFocusedPane(func(fp *FileDiffPane) { fp.scrollUp(fp.viewportHeight() / 2) }); return nil },
}

// advanceToolbarAction moves to the next toolbar button.
func (m *Model) advanceToolbarAction() {
	m.toolbarAction = (m.toolbarAction + 1) % toolbarButtonCount
	m.viewDirty = true
}

// retreatToolbarAction moves to the previous toolbar button.
func (m *Model) retreatToolbarAction() {
	m.toolbarAction = (m.toolbarAction - 1 + toolbarButtonCount) % toolbarButtonCount
	m.viewDirty = true
}

// handleToolbarKey handles keys when the toolbar is focused.
func (m *Model) handleToolbarKey(key tea.KeyMsg) tea.Cmd {
	if action, ok := toolbarKeyActions[key.String()]; ok {
		return action(m)
	}
	return nil
}

// ---------------------------------------------------------------------------
// handleMouse — split by action
// ---------------------------------------------------------------------------

// effectiveTabBarHeight returns the tab bar height, using tabbar.Height as
// minimum for blank filler.
func (m *Model) effectiveTabBarHeight() int {
	if tbh := m.tabBarHeight(); tbh > 0 {
		return tbh
	}
	return tabbar.Height
}

// handleMouseWheel handles scroll wheel events.
func (m *Model) handleMouseWheel(msg tea.MouseMsg, up bool) {
	fp := m.paneAtY(msg.Y)
	if fp == nil {
		return
	}
	if up {
		fp.scrollUp(3)
	} else {
		fp.scrollDown(3)
	}
	m.viewDirty = true
}

// handleMouseLeftClick handles left mouse button clicks.
func (m *Model) handleMouseLeftClick(msg tea.MouseMsg) tea.Cmd {
	tbh := m.effectiveTabBarHeight()
	if msg.Y < tbh && len(m.openTabs) > 0 {
		return m.handleTabBarClick(msg.X)
	}
	return m.handleLeftClickContent(msg)
}

// handleLeftClickContent handles left clicks in the content or toolbar area.
func (m *Model) handleLeftClickContent(msg tea.MouseMsg) tea.Cmd {
	tbRow := m.height - toolbarHeight
	if msg.Y < tbRow {
		m.focusPaneAtY(msg.Y)
		return nil
	}
	idx := toolbarHitTest(m.mode, m.sideBySide, m.width, msg.X)
	if idx >= 0 {
		return m.activateToolbarButton(idx)
	}
	return nil
}

// mouseButtonPressActions maps mouse buttons to press handlers.
var mouseButtonPressActions = map[tea.MouseButton]func(*Model, tea.MouseMsg) tea.Cmd{
	tea.MouseButtonWheelUp:   func(m *Model, msg tea.MouseMsg) tea.Cmd { m.handleMouseWheel(msg, true); return nil },
	tea.MouseButtonWheelDown: func(m *Model, msg tea.MouseMsg) tea.Cmd { m.handleMouseWheel(msg, false); return nil },
	tea.MouseButtonLeft:      func(m *Model, msg tea.MouseMsg) tea.Cmd { return m.handleMouseLeftClick(msg) },
}

// handleMousePress dispatches press events by button type.
func (m *Model) handleMousePress(msg tea.MouseMsg) tea.Cmd {
	if action, ok := mouseButtonPressActions[msg.Button]; ok {
		return action(m, msg)
	}
	return nil
}

// updateTabHover updates tab bar hover state and returns whether it changed.
func (m *Model) updateTabHover(msg tea.MouseMsg) {
	tbh := m.effectiveTabBarHeight()
	if msg.Y < tbh && len(m.openTabs) > 0 {
		m.updateTabHoverInBar(msg.X)
		return
	}
	m.clearTabHover()
}

// updateTabHoverInBar updates hover state when mouse is in the tab bar.
func (m *Model) updateTabHoverInBar(x int) {
	hit := tabbar.HitTest(m.tabBarConfig(), x)
	newHover := -1
	if hit.IsClose {
		newHover = hit.TabIndex
	}
	if newHover != m.tabHoverClose {
		m.tabHoverClose = newHover
		m.viewDirty = true
	}
}

// clearTabHover clears tab hover if it was set.
func (m *Model) clearTabHover() {
	if m.tabHoverClose >= 0 {
		m.tabHoverClose = -1
		m.viewDirty = true
	}
}

// updateToolbarHover updates toolbar hover state.
func (m *Model) updateToolbarHover(msg tea.MouseMsg) {
	tbRow := m.height - toolbarHeight
	if msg.Y >= tbRow {
		m.setToolbarHoverFromX(msg.X)
		return
	}
	m.clearToolbarHover()
}

// setToolbarHoverFromX sets toolbar hover from mouse X position.
func (m *Model) setToolbarHoverFromX(x int) {
	idx := toolbarHitTest(m.mode, m.sideBySide, m.width, x)
	if idx != m.hoverBtnIdx {
		m.hoverBtnIdx = idx
		m.viewDirty = true
	}
}

// clearToolbarHover clears toolbar hover if it was set.
func (m *Model) clearToolbarHover() {
	if m.hoverBtnIdx >= 0 {
		m.hoverBtnIdx = -1
		m.viewDirty = true
	}
}

// handleMouseMotion handles mouse motion for hover tracking.
func (m *Model) handleMouseMotion(msg tea.MouseMsg) {
	m.updateTabHover(msg)
	m.updateToolbarHover(msg)
}

// handleMouse processes mouse events.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action == tea.MouseActionPress {
		return m.handleMousePress(msg)
	}
	if msg.Action == tea.MouseActionMotion {
		m.handleMouseMotion(msg)
	}
	return nil
}

// handleTabBarClick processes a left-click on the tab bar.
func (m *Model) handleTabBarClick(x int) tea.Cmd {
	hit := tabbar.HitTest(m.tabBarConfig(), x)
	if hit.TabIndex < 0 {
		return nil
	}
	if hit.IsClose {
		return m.closeTab(hit.TabIndex)
	}
	m.switchToTab(hit.TabIndex)
	return nil
}

// ---------------------------------------------------------------------------
// paneAtY / focusPaneAtY — shared logic
// ---------------------------------------------------------------------------

// paneYFromView converts a view Y to a pane-local Y, returning -1 if invalid.
func (m *Model) paneYFromView(viewY int) int {
	paneY := viewY - tabbar.Height
	if paneY < 0 || m.paneTree == nil {
		return -1
	}
	return paneY
}

// lookupPaneInRects finds which pane ID contains paneY in the given rects.
func lookupPaneInRects(paneY int, rects map[pane.PaneID]pane.Rect) (pane.PaneID, bool) {
	for id, r := range rects {
		if rectContainsY(paneY, r) {
			return id, true
		}
	}
	return 0, false
}

// findPaneAtY returns the pane ID containing the given view Y coordinate.
// Returns (0, false) if no pane is found.
func (m *Model) findPaneAtY(viewY int) (pane.PaneID, bool) {
	paneY := m.paneYFromView(viewY)
	if paneY < 0 {
		return 0, false
	}
	area := pane.Rect{X: 0, Y: 0, W: m.width, H: m.viewportHeight()}
	return lookupPaneInRects(paneY, m.paneTree.ComputeLayout(area))
}

// paneAtY returns the FileDiffPane whose rect contains the given view Y
// coordinate (adjusted for the tab bar/header offset). Returns nil if outside.
func (m *Model) paneAtY(viewY int) *FileDiffPane {
	id, ok := m.findPaneAtY(viewY)
	if !ok {
		return nil
	}
	return m.paneFiles[id]
}

// focusPaneAtY sets focus to the pane at the given view Y coordinate.
// Clears toolbar focus so scroll keys route to the pane, not the toolbar.
func (m *Model) focusPaneAtY(viewY int) {
	id, ok := m.findPaneAtY(viewY)
	if !ok || id == m.focusedPane {
		return
	}
	m.focusedPane = id
	m.toolbarFocused = false
	m.viewDirty = true
	m.fileListDirty = true
}

// ---------------------------------------------------------------------------
// activateToolbarButton — dispatch map pattern
// ---------------------------------------------------------------------------

// setCompareMode changes the compare mode and emits a change message.
func (m *Model) setCompareMode(mode CompareMode) tea.Cmd {
	if m.mode == mode {
		return nil
	}
	m.mode = mode
	m.viewDirty = true
	return func() tea.Msg { return ChangeCompareModeMsg{Mode: mode} }
}

// setViewMode sets the side-by-side flag and rebuilds if changed.
func (m *Model) setViewMode(sbs bool) {
	if m.sideBySide == sbs {
		return
	}
	m.sideBySide = sbs
	m.rebuildAllPanes()
}

// toolbarActions maps button IDs to actions.
var toolbarActions = map[int]func(*Model) tea.Cmd{
	tbChain:    func(m *Model) tea.Cmd { return m.setCompareMode(CompareModeChain) },
	tbAllFirst: func(m *Model) tea.Cmd { return m.setCompareMode(CompareModeAllFirst) },
	tbPairs:    func(m *Model) tea.Cmd { return m.setCompareMode(CompareModePairs) },
	tbSBS:      func(m *Model) tea.Cmd { m.setViewMode(true); return nil },
	tbUnified:  func(m *Model) tea.Cmd { m.setViewMode(false); return nil },
	tbClose:    func(m *Model) tea.Cmd { return exitDiffViewCmd },
}

// activateToolbarButton triggers the action for a toolbar button index.
func (m *Model) activateToolbarButton(idx int) tea.Cmd {
	if action, ok := toolbarActions[idx]; ok {
		return action(m)
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

// FocusedPairIdx returns the pair index of the focused pane, or -1.
func (m *Model) FocusedPairIdx() int {
	if fp := m.focusedFileDiffPane(); fp != nil {
		return fp.pairIdx
	}
	return -1
}

// indexOfPaneID returns the index of target in ids, or -1 if not found.
func indexOfPaneID(ids []pane.PaneID, target pane.PaneID) int {
	for i, id := range ids {
		if id == target {
			return i
		}
	}
	return -1
}

// focusedLeafIndex returns the index of the focused pane in the leaf list,
// or -1 if the pane tree is nil or the focused pane is not found.
func (m *Model) focusedLeafIndex() ([]pane.PaneID, int) {
	if m.paneTree == nil {
		return nil, -1
	}
	leaves := m.paneTree.Leaves()
	return leaves, indexOfPaneID(leaves, m.focusedPane)
}

// validLeafIndex reports whether idx is a valid index in the leaf list.
func validLeafIndex(idx int, count int) bool {
	return idx >= 0 && idx < count
}

// findAdjacentLeaf finds the next (delta=+1) or previous (delta=-1) leaf
// relative to the focused pane. Returns the pane ID and true if found.
func (m *Model) findAdjacentLeaf(delta int) (pane.PaneID, bool) {
	leaves, idx := m.focusedLeafIndex()
	adj := idx + delta
	if !validLeafIndex(idx, len(leaves)) || !validLeafIndex(adj, len(leaves)) {
		return 0, false
	}
	return leaves[adj], true
}

// applyPaneFocus changes focus to the given pane and marks dirty flags.
// Clears toolbar focus so scroll keys route to the pane, not the toolbar.
func (m *Model) applyPaneFocus(id pane.PaneID) {
	m.focusedPane = id
	m.toolbarFocused = false
	m.viewDirty = true
	m.fileListDirty = true
	m.rebuildFindIfActive()
}

// focusNextPane moves focus to the next pane in spatial order.
func (m *Model) focusNextPane() {
	if id, ok := m.findAdjacentLeaf(1); ok {
		m.applyPaneFocus(id)
	}
}

// focusPrevPane moves focus to the previous pane in spatial order.
func (m *Model) focusPrevPane() {
	if id, ok := m.findAdjacentLeaf(-1); ok {
		m.applyPaneFocus(id)
	}
}

// ---------------------------------------------------------------------------
// View mode
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

// UnionFiles returns the union file list across all pairs.
func (m *Model) UnionFiles() []UnionFileEntry { return m.unionFiles }

// SelectedPath returns the currently displayed file path.
func (m *Model) SelectedPath() string { return m.selectedPath }

// OpenTabs returns the list of open tab paths.
func (m *Model) OpenTabs() []string { return m.openTabs }

// ActiveTabIdx returns the active tab index.
func (m *Model) ActiveTabIdx() int { return m.activeTabIdx }

// SelectedFile returns the file list cursor position.
func (m *Model) SelectedFile() int { return m.selectedFile }

// SetSelectedFile sets the file list cursor position.
func (m *Model) SetSelectedFile(idx int) {
	if idx >= 0 && idx < len(m.unionFiles) {
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

// FileSearchActive returns whether the file list search is active.
func (m *Model) FileSearchActive() bool { return m.fileSearchActive }

// FileSearchFocusIsQuery reports whether the file search query input has focus.
func (m *Model) FileSearchFocusIsQuery() bool {
	return m.fileSearchActive && m.fileSearchFocus == fileFocusQuery
}

// IsPaneClick reports whether the given local Y coordinate (within the diff
// view panel) falls inside the pane content area rather than the tab bar or
// toolbar. The caller should use this to decide whether to update app-level
// pane focus after forwarding the click.
func (m *Model) IsPaneClick(y int) bool {
	tbh := m.effectiveTabBarHeight()
	tbRow := m.height - toolbarHeight
	return y >= tbh && y < tbRow
}

// ---------------------------------------------------------------------------
// ClickFileList — split into phases
// ---------------------------------------------------------------------------

// clickFileListHeaderH returns the header height for the file list.
func (m *Model) clickFileListHeaderH() int {
	if m.fileSearchActive {
		return fileSearchHeaderHeight
	}
	return fileListHeaderHeight
}

// isSearchHintClick reports whether localY is a click on the search hint row.
func (m *Model) isSearchHintClick(localY int) bool {
	return localY == 1 && !m.fileSearchActive
}

// clickFileListHeader handles clicks in the file list header area.
// Returns true if the click was in the header (consumed).
func (m *Model) clickFileListHeader(localY int) bool {
	if localY >= m.clickFileListHeaderH() {
		return false
	}
	if m.isSearchHintClick(localY) {
		m.enterFileSearch()
	}
	return true
}

// clickFileListEntryH returns the available height for file entries.
func (m *Model) clickFileListEntryH() int {
	entryH := max(m.fileListH-m.clickFileListHeaderH(), 0)
	if m.fileSearchActive {
		entryH = max(entryH-1, 0)
	}
	return entryH
}

// resolveClickedFile maps a clicked visible index to a real union file index.
// Returns -1 if the index is out of bounds.
func (m *Model) resolveClickedFile(clickedVisIdx int) int {
	if m.filteredIndices == nil {
		return clickedVisIdx
	}
	if clickedVisIdx >= len(m.filteredIndices) {
		return -1
	}
	return m.filteredIndices[clickedVisIdx]
}

// clickedVisibleIdx computes the visible file index from a local Y click.
// Returns -1 if out of bounds.
func (m *Model) clickedVisibleIdx(localY int) int {
	entryIdx := localY - m.clickFileListHeaderH()
	total := m.visibleFileCount()
	start, _ := visibleWindow(m.selectedFile, total, m.clickFileListEntryH())
	idx := start + entryIdx
	if idx < 0 || idx >= total {
		return -1
	}
	return idx
}

// ClickFileList handles a mouse click at the given local Y coordinate
// within the file list panel. Opens the clicked file as a tab.
func (m *Model) ClickFileList(localY int) {
	if m.clickFileListHeader(localY) {
		return
	}
	clickedVisIdx := m.clickedVisibleIdx(localY)
	realIdx := m.resolveClickedFile(clickedVisIdx)
	if realIdx < 0 {
		return
	}
	m.selectedFile = clickedVisIdx
	m.OpenFileAsTab(realIdx)
}
