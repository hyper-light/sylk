package memoryview

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
)

// Layout tuning — kept as constants so tests can reason about spacing
// invariants without reaching into private helpers.
const (
	minAnchorMargin  = 8
	treeLaneGap      = 12
	laneInnerPadding = 3
	minLaneWidth     = 12
	maxVisibleTrees  = 3
	virtualSlotScale = 4
)

// point is a (col, row) cell coordinate within the canvas. (0,0) is
// top-left; x grows right, y grows down — matching tea.MouseMsg.
type point struct {
	x int
	y int
}

// canvasTree is a ViewTree bundled with its position in the snapshot's
// family ordering. globalIndex is used for deterministic color assignment
// so a Decision tree always looks the same regardless of how many
// families happen to be non-empty in a given snapshot.
type canvasTree struct {
	tree        forest.ViewTree
	globalIndex int
}

// SectionEntry is a single node row in the left memory index.
type SectionEntry struct {
	BranchID string
	Title    string
}

// Section is a grouped tree section rendered in the left memory index.
type Section struct {
	Family  forest.TreeFamily
	Label   string
	Entries []SectionEntry
}

// Model owns all state for the memory panel: the left index, the
// right canvas, hover, and the ambient animation pool. Exported
// methods form the stable UI-facing API.
type Model struct {
	theme *theme.Theme

	indexW int
	indexH int

	canvasW int
	canvasH int

	snapshot *forest.ViewSnapshot

	sections     []Section
	flatOrder    []string
	selectedID   string
	indexScroll  int
	nodeByID     map[string]forest.ViewNode
	parentByID   map[string]string
	positions    map[string]point
	treeByBranch map[string]int
	errorState   *viewErrorState

	// Frame state: cached forest geometry + per-tick animation.
	staticGrid      *canvasGrid
	staticGridAt    time.Time
	staticDirty     bool
	lastTrees       []canvasTree
	lastLayouts     []canopyLayout
	lastSelectedSet map[string]struct{}

	hover     hoverState
	animation *animationState

	// Session identity drives the deterministic RNG and placement
	// salt. Tracked separately from snapshot.SessionID so a hover
	// clear / reseed happens at the right moment.
	currentSessionID string
}

type viewErrorState struct {
	title  string
	detail string
	err    string
}

// New constructs a memory view model. The animation pool is created
// eagerly so the first frame renders with ground dust already in
// place; reseed() happens on the first snapshot.
func New(th *theme.Theme) *Model {
	return &Model{
		theme:        th,
		nodeByID:     make(map[string]forest.ViewNode),
		parentByID:   make(map[string]string),
		positions:    make(map[string]point),
		treeByBranch: make(map[string]int),
		hover:        newHoverState(),
		animation:    newAnimationState(""),
		staticDirty:  true,
	}
}

// SetIndexSize updates the left index dimensions.
func (m *Model) SetIndexSize(width, height int) {
	m.indexW = max(width, 0)
	m.indexH = max(height, 0)
	m.ensureIndexSelectionVisible()
}

// SetCanvasSize updates the memory canvas dimensions. Invalidates the
// static grid so the next frame rebuilds; clears hover state so a
// stale node reference doesn't outlive a resize.
func (m *Model) SetCanvasSize(width, height int) {
	newW := max(width, 0)
	newH := max(height, 0)
	if newW != m.canvasW || newH != m.canvasH {
		m.staticDirty = true
		m.hover.clear()
	}
	m.canvasW = newW
	m.canvasH = newH
}

// SetSnapshot replaces the current forest snapshot while preserving
// selection when possible. The static grid is invalidated so the next
// frame rebuilds tree geometry; the animation pool is reseeded on
// session change.
func (m *Model) SetSnapshot(snapshot *forest.ViewSnapshot) {
	if snapshot == nil {
		snapshot = &forest.ViewSnapshot{}
	}
	m.errorState = nil
	m.snapshot = snapshot
	m.rebuildIndex()
	m.rebuildParents()
	m.reconcileSelection(snapshot.SelectedBranchID)
	m.staticDirty = true
	if snapshot.SessionID != m.currentSessionID {
		m.currentSessionID = snapshot.SessionID
		if m.animation != nil {
			m.animation.reseed(snapshot.SessionID)
		}
		m.hover.clear()
	}
}

// SetError replaces the current snapshot with an explicit error state.
func (m *Model) SetError(title, detail string, err error) {
	msg := ""
	if err != nil {
		msg = strings.TrimSpace(err.Error())
	}
	m.errorState = &viewErrorState{
		title:  strings.TrimSpace(title),
		detail: strings.TrimSpace(detail),
		err:    msg,
	}
	m.snapshot = nil
	m.sections = nil
	m.flatOrder = nil
	m.selectedID = ""
	m.indexScroll = 0
	m.nodeByID = make(map[string]forest.ViewNode)
	m.parentByID = make(map[string]string)
	m.positions = make(map[string]point)
	m.treeByBranch = make(map[string]int)
	m.staticDirty = true
	m.hover.clear()
}

// ErrorState returns the active memory-view error strings, when present.
func (m *Model) ErrorState() (title, detail, errText string, ok bool) {
	if m == nil || m.errorState == nil {
		return "", "", "", false
	}
	return m.errorState.title, m.errorState.detail, m.errorState.err, true
}

// Sections returns the grouped left-sidebar tree sections.
func (m *Model) Sections() []Section {
	return append([]Section(nil), m.sections...)
}

// SelectedBranchID returns the currently selected branch.
func (m *Model) SelectedBranchID() string {
	return m.selectedID
}

// IndexScroll returns the top visible line offset for the sidebar.
func (m *Model) IndexScroll() int {
	return m.indexScroll
}

// SelectedFamily returns the family containing the current selection.
func (m *Model) SelectedFamily() forest.TreeFamily {
	node, ok := m.nodeByID[m.selectedID]
	if !ok {
		return ""
	}
	return node.Family
}

// SelectBranch focuses a specific branch when it exists in the snapshot.
func (m *Model) SelectBranch(branchID string) bool {
	if _, ok := m.nodeByID[branchID]; !ok {
		return false
	}
	m.selectedID = branchID
	m.ensureIndexSelectionVisible()
	m.staticDirty = true
	return true
}

// SetHoverCell updates the mouse hover position in canvas-local
// coordinates. Negative values clear the hover. Returns true when
// the hover moved to a different node (caller can force redraw).
func (m *Model) SetHoverCell(x, y int) bool {
	if x < 0 || y < 0 || x >= m.canvasW || y >= m.canvasH {
		if m.hover.cellX == -1 && m.hover.cellY == -1 {
			return false
		}
		m.hover.clear()
		return true
	}
	if !m.hover.setCell(x, y) {
		return false
	}
	previous := m.hover.hitNodeID
	m.hover.hitNodeID = m.resolveHover()
	return m.hover.hitNodeID != previous
}

// HoveredNode returns the currently hovered node ID (empty if none).
func (m *Model) HoveredNode() string {
	return m.hover.hitNodeID
}

// DisableAnimation turns the ambient / tick loop off. Keep the
// geometry and hover paths intact; useful for headless tests and for
// a reduced-motion preference.
func (m *Model) DisableAnimation() {
	if m.animation != nil {
		m.animation.enabled = false
	}
}

// AnimationEnabled reports whether the tick loop is scheduling new
// frames. Used by the host to decide whether to wire a ticker.
func (m *Model) AnimationEnabled() bool {
	return m.animation != nil && m.animation.enabled
}

// Tick advances the animation state by one frame. Call once per
// ~60ms from the host app's tick handler. Safe to call when
// animation is disabled (no-op).
func (m *Model) Tick() {
	if m.animation == nil || !m.animation.enabled {
		return
	}
	// Advance particle state using the most recently rendered
	// layout. If no render has happened yet, advance() still runs
	// ground-dust spawning so the first paint isn't empty.
	m.animation.advance(m.snapshot, m.canvasW, m.canvasH, m.lastTrees, m.lastLayouts)
}

// HandleIndexKey moves selection through the grouped node list.
func (m *Model) HandleIndexKey(key tea.KeyMsg) bool {
	if len(m.flatOrder) == 0 {
		return false
	}
	switch key.String() {
	case "j", "down":
		return m.moveFlatSelection(1)
	case "k", "up":
		return m.moveFlatSelection(-1)
	case "g", "home":
		return m.setFlatSelection(0)
	case "G", "end":
		return m.setFlatSelection(len(m.flatOrder) - 1)
	default:
		return false
	}
}

// HandleCanvasKey moves selection spatially through the rendered forest.
func (m *Model) HandleCanvasKey(key tea.KeyMsg) bool {
	if len(m.flatOrder) == 0 {
		return false
	}
	switch key.String() {
	case "j":
		return m.moveFlatSelection(1)
	case "k":
		return m.moveFlatSelection(-1)
	case "left":
		return m.moveSpatial(-1, 0)
	case "right":
		return m.moveSpatial(1, 0)
	case "up":
		return m.moveSpatial(0, -1)
	case "down":
		return m.moveSpatial(0, 1)
	case "g", "home":
		return m.setFlatSelection(0)
	case "G", "end":
		return m.setFlatSelection(len(m.flatOrder) - 1)
	default:
		return false
	}
}

// ViewCanvas renders the right-panel memory forest. The pipeline:
//  1. Error / empty shortcuts.
//  2. Rebuild static grid (forest geometry + ground plane) if dirty.
//  3. Clone the static grid and overlay animation + shimmer.
//  4. Overlay tooltip (if hovering a node).
//  5. Encode to a styled string.
//
// Steps 2 is the only O(total node count) step; it only runs when
// snapshot, canvas size, or selection has changed. Steps 3–5 are
// O(canvas cells + particles + tooltip bytes).
func (m *Model) ViewCanvas() string {
	if m.canvasW <= 0 || m.canvasH <= 0 {
		return ""
	}
	if m.errorState != nil {
		return m.renderMessageCanvas(m.errorState.title, m.errorState.detail, m.errorState.err)
	}
	if m.snapshot == nil || len(m.snapshot.Trees) == 0 {
		return m.renderEmptyCanvas()
	}

	if m.staticDirty {
		m.rebuildStaticGrid()
	}
	if m.staticGrid == nil {
		return m.renderEmptyCanvas()
	}

	frame := m.staticGrid.clone()
	m.animation.paint(frame)
	m.animation.paintShimmer(frame, m.lastTrees, m.lastLayouts, m.lastSelectedSet)
	// Tooltip resolution uses the latest hover state so movement
	// within the same node doesn't stutter.
	if m.hover.hitNodeID == "" {
		m.hover.hitNodeID = m.resolveHover()
	}
	m.paintTooltip(frame)
	return frame.render(m.theme)
}

// rebuildStaticGrid regenerates the forest geometry cache. Runs only
// when dirty; the cost is proportional to total node count across
// the visible trees.
func (m *Model) rebuildStaticGrid() {
	m.positions = make(map[string]point)
	grid := newCanvasGrid(m.canvasW, m.canvasH)

	trees := visibleTreesForCanvas(orderedTreesForCanvas(m.snapshot.Trees), m.selectedID, m.canvasW)
	if len(trees) == 0 {
		m.staticGrid = grid
		m.staticDirty = false
		m.lastTrees = nil
		m.lastLayouts = nil
		m.lastSelectedSet = nil
		m.animation.refreshGroundDust(m.canvasW, m.canvasH)
		m.animation.paint(grid)
		return
	}
	selectedPath := m.selectedPathSet()

	placements := distributeTreePlacements(m.canvasW, m.snapshot.SessionID, trees)
	layouts := make([]canopyLayout, len(trees))
	for idx, tree := range trees {
		params := canopyParams{
			trunkX:      placements[idx].anchorX,
			laneMin:     placements[idx].lane.x0,
			laneMax:     placements[idx].lane.x1,
			bottomY:     m.canvasH - 2, // one row reserved for ground anchor
			maxHeight:   m.canvasH - 4,
			depthRatio:  depthRatioForTree(idx, len(trees)),
			sessionSalt: sessionSeed(m.snapshot.SessionID),
			treeSalt:    treeSeed(m.snapshot.SessionID, tree.tree),
		}
		layout := computeCanopyLayout(tree.tree, params)
		layouts[idx] = layout
		m.paintTree(grid, tree.tree, layout, selectedPath)
	}

	// Ground anchor glyphs on the very bottom row.
	for idx, tree := range trees {
		_ = tree
		trunkX := placements[idx].anchorX
		y := m.canvasH - 1
		style := styleRoot
		if rootID := layouts[idx].rootID; rootID != "" {
			if _, ok := selectedPath[rootID]; ok {
				style = styleSelected
			}
		}
		// Center the 3-char glyph on the trunk column.
		grid.drawString(trunkX-1, y, rootAnchorGlyph, style)
	}

	m.staticGrid = grid
	m.staticDirty = false
	m.lastTrees = trees
	m.lastLayouts = layouts
	m.lastSelectedSet = selectedPath
}

// paintTree draws a single tree's trunk, branches, and nodes onto the
// grid using the precomputed layout. Connectors are drawn before
// nodes so canvas.set's layering rules keep node glyphs on top.
func (m *Model) paintTree(
	grid *canvasGrid,
	tree forest.ViewTree,
	layout canopyLayout,
	selectedPath map[string]struct{},
) {
	// Record positions on the Model for spatial navigation + hover.
	for id, pos := range layout.positions {
		m.positions[id] = pos
	}

	// Draw trunk: vertical spine from root up to trunk top.
	trunkStyle := styleMid
	if rootID := layout.rootID; rootID != "" {
		if _, sel := selectedPath[rootID]; sel {
			trunkStyle = styleSelected
		}
	}
	grid.drawVertical(layout.trunkX, layout.trunkTopY, layout.trunkBase-1, trunkStyle)

	// Draw connectors. Crown leaves visually spoke from the trunk
	// top (even if their data parent is deeper in the tree) so the
	// silhouette reads as a plant. Everything else routes from the
	// actual data parent.
	trunkTopPos, hasTrunkTop := layout.positions[layout.trunkTopID]
	for _, node := range tree.Nodes {
		if node.ParentID == "" {
			continue
		}
		childPos, ok := layout.positions[node.ID]
		if !ok {
			continue
		}
		style := connectorStyleFor(layout, node.ID, selectedPath)
		if _, crown := layout.crownLeafSet[node.ID]; crown && hasTrunkTop {
			drawBranch(grid, trunkTopPos, childPos, style)
			continue
		}
		parentPos, ok := layout.positions[node.ParentID]
		if !ok {
			continue
		}
		drawBranch(grid, parentPos, childPos, style)
	}

	// Paint nodes (except root — root is represented by the ground anchor).
	for _, node := range tree.Nodes {
		pos, ok := layout.positions[node.ID]
		if !ok {
			continue
		}
		if node.ID == layout.rootID {
			continue
		}
		_, selected := selectedPath[node.ID]
		tier := layout.tiers[node.ID]
		glyph := nodeGlyph(tier, selected)
		style := tierStyle(tier, selected)
		grid.set(pos.x, pos.y, glyph, style)
	}
}

// drawBranch picks the right connector style for a parent→child edge.
// Vertical edges draw as a straight │. Near-diagonal edges up from a
// trunk node to a crown leaf draw as angled dashes. Lateral drops
// (internal side-branches) use the rounded-elbow drawConnector path.
func drawBranch(grid *canvasGrid, parent, child point, style styleClass) {
	if parent == child {
		return
	}
	if parent.x == child.x {
		grid.drawVertical(parent.x, min(parent.y, child.y), max(parent.y, child.y), style)
		return
	}
	dy := parent.y - child.y
	if dy <= 0 {
		// Child below parent (side branch hanging down). Use
		// rounded-elbow connector for readability.
		grid.drawConnector(parent, child, connectorJoinY(parent, child, 0, 1), style)
		return
	}
	dx := child.x - parent.x
	if abs(dx) > dy {
		// Near-horizontal span — prefer elbow to avoid visual noise.
		grid.drawConnector(parent, child, connectorJoinY(parent, child, 0, 1), style)
		return
	}
	// True diagonal branch: crown spoke from trunk top to a leaf.
	grid.drawDiagonal(parent, child, style)
}

func connectorStyleFor(layout canopyLayout, childID string, selectedPath map[string]struct{}) styleClass {
	if _, sel := selectedPath[childID]; sel {
		return styleSelected
	}
	tier := layout.tiers[childID]
	return tierStyle(tier, false)
}

// depthRatioForTree returns the parallax depth for tree index i of n.
// 0.0 = farthest (dimmest); 1.0 = frontmost. Single trees render as
// frontmost.
func depthRatioForTree(i, n int) float64 {
	if n <= 1 {
		return 1
	}
	return float64(i) / float64(n-1)
}

func treeSeed(sessionID string, tree forest.ViewTree) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(string(tree.Family)))
	if len(tree.Roots) > 0 {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(tree.Roots[0]))
	}
	return h.Sum64()
}

func (m *Model) rebuildIndex() {
	m.sections = m.sections[:0]
	m.flatOrder = m.flatOrder[:0]
	m.nodeByID = make(map[string]forest.ViewNode)
	m.treeByBranch = make(map[string]int)

	if m.snapshot == nil {
		return
	}
	for treeIdx, tree := range m.snapshot.Trees {
		if len(tree.Nodes) == 0 {
			continue
		}
		section := Section{
			Family:  tree.Family,
			Label:   tree.Label,
			Entries: make([]SectionEntry, 0, len(tree.Nodes)),
		}
		for _, node := range tree.Nodes {
			m.nodeByID[node.ID] = node
			m.treeByBranch[node.ID] = treeIdx
			title := strings.TrimSpace(node.Title)
			if title == "" {
				title = strings.TrimSpace(node.Summary)
			}
			if title == "" {
				title = "Untitled"
			}
			section.Entries = append(section.Entries, SectionEntry{
				BranchID: node.ID,
				Title:    title,
			})
			m.flatOrder = append(m.flatOrder, node.ID)
		}
		m.sections = append(m.sections, section)
	}
}

func (m *Model) rebuildParents() {
	m.parentByID = make(map[string]string, len(m.nodeByID))
	for _, tree := range m.snapshot.Trees {
		for _, node := range tree.Nodes {
			m.parentByID[node.ID] = node.ParentID
		}
	}
}

func (m *Model) reconcileSelection(preferred string) {
	switch {
	case preferred != "" && m.SelectBranch(preferred):
		return
	case m.selectedID != "" && m.SelectBranch(m.selectedID):
		return
	case len(m.flatOrder) > 0:
		m.selectedID = m.flatOrder[len(m.flatOrder)-1]
	default:
		m.selectedID = ""
	}
	m.ensureIndexSelectionVisible()
}

func (m *Model) moveFlatSelection(delta int) bool {
	if len(m.flatOrder) == 0 {
		return false
	}
	idx := 0
	for i, id := range m.flatOrder {
		if id == m.selectedID {
			idx = i
			break
		}
	}
	next := clampIndex(idx+delta, len(m.flatOrder))
	if m.flatOrder[next] == m.selectedID {
		return false
	}
	m.selectedID = m.flatOrder[next]
	m.ensureIndexSelectionVisible()
	m.staticDirty = true
	return true
}

func (m *Model) setFlatSelection(idx int) bool {
	if len(m.flatOrder) == 0 {
		return false
	}
	idx = clampIndex(idx, len(m.flatOrder))
	if m.flatOrder[idx] == m.selectedID {
		return false
	}
	m.selectedID = m.flatOrder[idx]
	m.ensureIndexSelectionVisible()
	m.staticDirty = true
	return true
}

func (m *Model) ensureIndexSelectionVisible() {
	if m.indexH <= 0 || len(m.flatOrder) == 0 {
		return
	}
	selectedLine := 0
	line := 0
	for _, section := range m.sections {
		line++
		for _, entry := range section.Entries {
			if entry.BranchID == m.selectedID {
				selectedLine = line
			}
			line++
		}
	}
	if selectedLine < m.indexScroll {
		m.indexScroll = selectedLine
	}
	visibleBottom := m.indexScroll + max(m.indexH-1, 0)
	if selectedLine > visibleBottom {
		m.indexScroll = selectedLine - max(m.indexH-1, 0)
	}
	if m.indexScroll < 0 {
		m.indexScroll = 0
	}
}

func (m *Model) moveSpatial(dx, dy int) bool {
	if len(m.positions) == 0 {
		_ = m.ViewCanvas()
	}
	current, ok := m.positions[m.selectedID]
	if !ok {
		return m.setFlatSelection(len(m.flatOrder) - 1)
	}

	bestID := ""
	bestScore := math.MaxFloat64
	for branchID, candidate := range m.positions {
		if branchID == m.selectedID {
			continue
		}
		if !isInDirection(current, candidate, dx, dy) {
			continue
		}
		score := directionalScore(current, candidate, dx, dy)
		if score < bestScore {
			bestScore = score
			bestID = branchID
		}
	}
	if bestID == "" {
		return false
	}
	m.selectedID = bestID
	m.ensureIndexSelectionVisible()
	m.staticDirty = true
	return true
}

func (m *Model) selectedPathSet() map[string]struct{} {
	path := make(map[string]struct{}, 8)
	current := m.selectedID
	for current != "" {
		if _, seen := path[current]; seen {
			break
		}
		path[current] = struct{}{}
		current = m.parentByID[current]
	}
	return path
}

func orderedTreesForCanvas(trees []forest.ViewTree) []forest.ViewTree {
	ordered := append([]forest.ViewTree(nil), trees...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].UpdatedAt.Equal(ordered[j].UpdatedAt) {
			return ordered[i].UpdatedAt.Before(ordered[j].UpdatedAt)
		}
		return ordered[i].Family < ordered[j].Family
	})
	return ordered
}

func visibleTreesForCanvas(trees []forest.ViewTree, selectedID string, canvasWidth int) []canvasTree {
	if len(trees) == 0 {
		return nil
	}
	visibleLimit := maxTreesForWidth(canvasWidth, len(trees))
	if visibleLimit <= 0 {
		return nil
	}
	indexByBranch := make(map[string]int, len(trees))
	for treeIdx, tree := range trees {
		for _, node := range tree.Nodes {
			indexByBranch[node.ID] = treeIdx
		}
	}
	selectedIdx := len(trees) - 1
	if idx, ok := indexByBranch[selectedID]; ok {
		selectedIdx = idx
	}
	if len(trees) <= visibleLimit {
		out := make([]canvasTree, 0, len(trees))
		for idx, tree := range trees {
			out = append(out, canvasTree{tree: tree, globalIndex: idx})
		}
		return out
	}

	window := visibleLimit
	start := selectedIdx - window/2
	if start < 0 {
		start = 0
	}
	if end := start + window; end > len(trees) {
		start = len(trees) - window
	}
	out := make([]canvasTree, 0, window)
	for idx := start; idx < start+window; idx++ {
		out = append(out, canvasTree{tree: trees[idx], globalIndex: idx})
	}
	return out
}

type lane struct{ x0, x1 int }

type treePlacement struct {
	lane    lane
	anchorX int
}

type anchoredTree struct {
	treeIndex int
	anchorX   int
	priority  uint64
}

func maxTreesForWidth(width, total int) int {
	if width <= 0 || total <= 0 {
		return 0
	}
	limit := min(total, maxVisibleTrees)
	for count := limit; count >= 1; count-- {
		required := (2 * minAnchorMargin) + (count * (minLaneWidth + 2*laneInnerPadding)) + ((count - 1) * treeLaneGap)
		if width >= required {
			return count
		}
	}
	return 1
}

func distributeTreePlacements(width int, sessionID string, trees []canvasTree) []treePlacement {
	if width <= 0 || len(trees) == 0 {
		return nil
	}
	count := len(trees)
	if count == 1 {
		x0 := minAnchorMargin
		x1 := max(width-minAnchorMargin-1, minAnchorMargin)
		return []treePlacement{{
			lane:    lane{x0: x0, x1: x1},
			anchorX: (x0 + x1) / 2,
		}}
	}
	leftEdge := minAnchorMargin
	rightEdge := max(width-minAnchorMargin-1, leftEdge)
	slotCount := virtualSlotCount(width, count)
	anchors := spreadTreeAnchors(leftEdge, rightEdge, slotCount, sessionID, trees)
	sorted := append([]anchoredTree(nil), anchors...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].anchorX == sorted[j].anchorX {
			return sorted[i].treeIndex < sorted[j].treeIndex
		}
		return sorted[i].anchorX < sorted[j].anchorX
	})

	placements := make([]treePlacement, count)
	for idx, current := range sorted {
		laneMin := leftEdge
		if idx > 0 {
			laneMin = max(leftEdge, (sorted[idx-1].anchorX+current.anchorX)/2+1)
		}
		laneMax := rightEdge
		if idx < len(sorted)-1 {
			laneMax = min(rightEdge, (current.anchorX+sorted[idx+1].anchorX)/2-1)
		}
		placements[current.treeIndex] = treePlacement{
			lane:    lane{x0: laneMin, x1: laneMax},
			anchorX: clamp(current.anchorX, laneMin+1, laneMax-1),
		}
	}
	return placements
}

func virtualSlotCount(width, treeCount int) int {
	if width <= 0 || treeCount <= 0 {
		return 0
	}
	usable := max(width-(2*minAnchorMargin), treeCount*minLaneWidth)
	slotBudget := max(usable/max(minLaneWidth/2, 1), treeCount)
	return max(slotBudget, treeCount*virtualSlotScale)
}

func spreadTreeAnchors(leftEdge, rightEdge, slotCount int, sessionID string, trees []canvasTree) []anchoredTree {
	if len(trees) == 0 {
		return nil
	}
	if len(trees) == 1 {
		return []anchoredTree{{treeIndex: 0, anchorX: (leftEdge + rightEdge) / 2}}
	}

	ranked := make([]anchoredTree, 0, len(trees))
	for idx, tree := range trees {
		ranked = append(ranked, anchoredTree{
			treeIndex: idx,
			priority:  treePlacementPriority(sessionID, tree),
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].priority == ranked[j].priority {
			return ranked[i].treeIndex < ranked[j].treeIndex
		}
		return ranked[i].priority < ranked[j].priority
	})

	lastSlot := max(slotCount-1, 1)
	for rankIdx := range ranked {
		slot := distributedSlot(rankIdx, len(ranked), lastSlot)
		ranked[rankIdx].anchorX = interpolateSlot(leftEdge, rightEdge, slot, lastSlot)
	}
	return ranked
}

func treePlacementPriority(sessionID string, tree canvasTree) uint64 {
	key := strings.TrimSpace(sessionID)
	if key == "" {
		key = "global"
	}
	key += "|" + stableTreePlacementKey(tree)
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(key))
	return hasher.Sum64()
}

func stableTreePlacementKey(tree canvasTree) string {
	if len(tree.tree.Roots) > 0 && strings.TrimSpace(tree.tree.Roots[0]) != "" {
		return strings.TrimSpace(tree.tree.Roots[0])
	}
	if len(tree.tree.Nodes) > 0 && strings.TrimSpace(tree.tree.Nodes[0].ID) != "" {
		return strings.TrimSpace(tree.tree.Nodes[0].ID)
	}
	return string(tree.tree.Family) + "|" + strings.TrimSpace(tree.tree.Label)
}

func distributedSlot(rank, count, lastSlot int) int {
	if count <= 1 || lastSlot <= 1 {
		return lastSlot / 2
	}
	return int(roundHalfUp(float64(rank) * float64(lastSlot) / float64(count-1)))
}

func interpolateSlot(leftEdge, rightEdge, slot, lastSlot int) int {
	if rightEdge <= leftEdge || lastSlot <= 0 {
		return leftEdge
	}
	return leftEdge + int(roundHalfUp(float64(rightEdge-leftEdge)*float64(slot)/float64(lastSlot)))
}

func (m *Model) renderEmptyCanvas() string {
	return m.renderMessageCanvas("MEMORY FOREST EMPTY", "no indexed branches for this session", "")
}

func (m *Model) renderMessageCanvas(title, detail, errText string) string {
	grid := newCanvasGrid(m.canvasW, m.canvasH)
	lines := make([]string, 0, 4)
	if line := strings.TrimSpace(title); line != "" {
		lines = append(lines, line)
	}
	if line := strings.TrimSpace(detail); line != "" {
		lines = append(lines, line)
	}
	if line := strings.TrimSpace(errText); line != "" {
		lines = append(lines, line)
	}
	startY := max((m.canvasH-len(lines))/2, 0)
	for idx, line := range lines {
		x := max((m.canvasW-len([]rune(line)))/2, 0)
		grid.drawString(x, startY+idx, line, styleSelected)
	}
	return grid.render(m.theme)
}

func isInDirection(current, candidate point, dx, dy int) bool {
	switch {
	case dx < 0:
		return candidate.x < current.x
	case dx > 0:
		return candidate.x > current.x
	case dy < 0:
		return candidate.y < current.y
	case dy > 0:
		return candidate.y > current.y
	default:
		return false
	}
}

func directionalScore(current, candidate point, dx, dy int) float64 {
	dxv := float64(candidate.x - current.x)
	dyv := float64(candidate.y - current.y)
	switch {
	case dx != 0:
		return math.Abs(dyv)*4 + math.Abs(dxv)
	case dy != 0:
		return math.Abs(dxv)*4 + math.Abs(dyv)
	default:
		return math.MaxFloat64
	}
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func clampIndex(idx, length int) int {
	if length <= 0 {
		return 0
	}
	if idx < 0 {
		return 0
	}
	if idx >= length {
		return length - 1
	}
	return idx
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func roundHalfUp(v float64) float64 {
	return math.Floor(v + 0.5)
}
