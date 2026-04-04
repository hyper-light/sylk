package memoryview

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	minAnchorMargin  = 8
	treeLaneGap      = 12
	laneInnerPadding = 3
	minLaneWidth     = 12
	maxVisibleTrees  = 3
	rootAnchorGlyph  = "<⟡>"
	localLeafStep    = 5
	localRootGap     = 3
	virtualSlotScale = 4
)

type styleClass int

const (
	styleBack styleClass = iota
	styleMid
	styleFront
	styleSelected
	styleRoot
)

type point struct {
	x int
	y int
}

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

// Model holds the synchronized state for the memory index and canvas.
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
}

type viewErrorState struct {
	title  string
	detail string
	err    string
}

// New constructs a memory view model.
func New(th *theme.Theme) *Model {
	return &Model{
		theme:        th,
		nodeByID:     make(map[string]forest.ViewNode),
		parentByID:   make(map[string]string),
		positions:    make(map[string]point),
		treeByBranch: make(map[string]int),
	}
}

// SetIndexSize updates the left index dimensions.
func (m *Model) SetIndexSize(width, height int) {
	m.indexW = max(width, 0)
	m.indexH = max(height, 0)
	m.ensureIndexSelectionVisible()
}

// SetCanvasSize updates the memory canvas dimensions.
func (m *Model) SetCanvasSize(width, height int) {
	m.canvasW = max(width, 0)
	m.canvasH = max(height, 0)
}

// SetSnapshot replaces the current forest snapshot while preserving selection when possible.
func (m *Model) SetSnapshot(snapshot *forest.ViewSnapshot) {
	if snapshot == nil {
		snapshot = &forest.ViewSnapshot{}
	}
	m.errorState = nil
	m.snapshot = snapshot
	m.rebuildIndex()
	m.rebuildParents()
	m.reconcileSelection(snapshot.SelectedBranchID)
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
	return true
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

// ViewCanvas renders the right-panel memory forest.
func (m *Model) ViewCanvas() string {
	if m.canvasW <= 0 || m.canvasH <= 0 {
		return ""
	}
	m.positions = make(map[string]point)
	if m.errorState != nil {
		return m.renderMessageCanvas(m.errorState.title, m.errorState.detail, m.errorState.err)
	}
	if m.snapshot == nil || len(m.snapshot.Trees) == 0 {
		return m.renderEmptyCanvas()
	}

	grid := newCanvasGrid(m.canvasW, m.canvasH)
	trees := visibleTreesForCanvas(orderedTreesForCanvas(m.snapshot.Trees), m.selectedID, m.canvasW)
	if len(trees) == 0 {
		return m.renderEmptyCanvas()
	}
	selectedPath := m.selectedPathSet()

	placements := distributeTreePlacements(m.canvasW, m.snapshot.SessionID, trees)
	for idx, tree := range trees {
		planeClass := planeStyle(idx, len(trees))
		placement := placements[idx]
		m.renderTree(grid, tree.tree, placement.anchorX, placement.lane.x0, placement.lane.x1, idx, len(trees), planeClass, selectedPath)
	}
	return grid.render(m.theme)
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
	return int(math.Round(float64(rank) * float64(lastSlot) / float64(count-1)))
}

func interpolateSlot(leftEdge, rightEdge, slot, lastSlot int) int {
	if rightEdge <= leftEdge || lastSlot <= 0 {
		return leftEdge
	}
	return leftEdge + int(math.Round(float64(rightEdge-leftEdge)*float64(slot)/float64(lastSlot)))
}

func planeStyle(idx, total int) styleClass {
	if total <= 1 {
		return styleFront
	}
	ratio := float64(idx) / float64(total-1)
	switch {
	case ratio < 0.34:
		return styleBack
	case ratio < 0.67:
		return styleMid
	default:
		return styleFront
	}
}

func (m *Model) renderTree(
	grid *canvasGrid,
	tree forest.ViewTree,
	anchorX int,
	laneMin int,
	laneMax int,
	treeIndex int,
	treeCount int,
	baseClass styleClass,
	selectedPath map[string]struct{},
) {
	nodeByID := make(map[string]forest.ViewNode, len(tree.Nodes))
	children := make(map[string][]string, len(tree.Nodes))
	roots := append([]string(nil), tree.Roots...)
	if len(roots) == 0 {
		for _, node := range tree.Nodes {
			if node.ParentID == "" {
				roots = append(roots, node.ID)
			}
		}
	}
	for _, node := range tree.Nodes {
		nodeByID[node.ID] = node
		if node.ParentID != "" {
			children[node.ParentID] = append(children[node.ParentID], node.ID)
		}
	}
	for parentID := range children {
		sort.SliceStable(children[parentID], func(i, j int) bool {
			left := nodeByID[children[parentID][i]]
			right := nodeByID[children[parentID][j]]
			if !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			return left.ID < right.ID
		})
	}

	layout := make(map[string]point, len(tree.Nodes))
	nextLeaf := 0
	var maxDepth int
	var walk func(id string, depth int) float64
	walk = func(id string, depth int) float64 {
		if depth > maxDepth {
			maxDepth = depth
		}
		kids := children[id]
		if len(kids) == 0 {
			x := float64(nextLeaf)
			nextLeaf += localLeafStep
			layout[id] = point{x: int(math.Round(x)), y: depth}
			return x
		}
		minX := math.MaxFloat64
		maxX := -math.MaxFloat64
		for _, childID := range kids {
			childX := walk(childID, depth+1)
			if childX < minX {
				minX = childX
			}
			if childX > maxX {
				maxX = childX
			}
		}
		x := (minX + maxX) / 2
		layout[id] = point{x: int(math.Round(x)), y: depth}
		return x
	}
	for i, rootID := range roots {
		walk(rootID, 0)
		if i < len(roots)-1 {
			nextLeaf += localRootGap
		}
	}
	if len(layout) == 0 {
		return
	}

	minLocalX, maxLocalX := math.MaxFloat64, -math.MaxFloat64
	for _, pos := range layout {
		x := float64(pos.x)
		if x < minLocalX {
			minLocalX = x
		}
		if x > maxLocalX {
			maxLocalX = x
		}
	}
	centerX := (minLocalX + maxLocalX) / 2
	localWidth := maxLocalX - minLocalX + 1
	depthRatio := 1.0
	if treeCount > 1 {
		depthRatio = float64(treeIndex) / float64(treeCount-1)
	}
	contentMin := min(laneMin+laneInnerPadding, laneMax)
	contentMax := max(laneMax-laneInnerPadding, laneMin)
	if contentMin >= contentMax {
		contentMin, contentMax = laneMin, laneMax
	}
	availableWidth := max(contentMax-contentMin+1, minLaneWidth)
	fitScaleX := float64(availableWidth-2) / math.Max(localWidth, 1)
	scaleX := math.Min(math.Max(fitScaleX*(0.92+0.08*depthRatio), 1.0), 1.8)
	availableHeight := max(m.canvasH-6, 6)
	fitScaleY := float64(availableHeight) / math.Max(float64(maxDepth+2), 1)
	scaleY := math.Min(math.Max(fitScaleY*(0.48+0.32*depthRatio), 1.4), 4.2)
	shear := 0.22 * (1.0 - depthRatio)
	parallaxLift := 0.45 * (1.0 - depthRatio)
	bottom := m.canvasH - 1

	for _, rootID := range roots {
		rootLocal := layout[rootID]
		rootX := anchorX + int(math.Round((float64(rootLocal.x)-centerX)*scaleX))
		rootX = clamp(rootX, max(contentMin+1, 0), min(contentMax-1, m.canvasW-1))
		rootPos := point{x: rootX, y: bottom}
		m.positions[rootID] = rootPos

		rootChildren := children[rootID]
		for childIdx, childID := range rootChildren {
			m.renderSubtree(contentMin, contentMax, rootPos, float64(rootLocal.x), scaleX, scaleY, shear, parallaxLift, layout, nodeByID, children, childID, rootPos, childIdx, len(rootChildren), grid, baseClass, selectedPath)
		}

		rootStyle := styleRoot
		if _, ok := selectedPath[rootID]; ok {
			rootStyle = styleSelected
		}
		grid.drawString(rootX-1, bottom, rootAnchorGlyph, rootStyle)
	}
}

func (m *Model) projectNode(
	laneMin int,
	laneMax int,
	root point,
	rootLocalX float64,
	scaleX, scaleY, shear, parallaxLift float64,
	layout map[string]point,
	node forest.ViewNode,
	parent point,
	siblingIndex int,
	siblingCount int,
	grid *canvasGrid,
	baseClass styleClass,
	selectedPath map[string]struct{},
) point {
	local := layout[node.ID]
	localX := float64(local.x) - rootLocalX
	localDepth := float64(local.y)
	screenX := root.x + int(math.Round(localX*scaleX-localDepth*shear))
	screenY := root.y - int(math.Round(localDepth*scaleY+localDepth*parallaxLift))
	if siblingCount == 1 && parent.y != screenY {
		screenX = nudgeSingletonBranch(screenX, parent.x, parent.x, laneMin, laneMax, local.y)
	}
	screenX = clamp(screenX, max(laneMin+1, 0), min(laneMax-1, m.canvasW-1))
	screenY = clamp(screenY, 0, max(root.y-1, 0))
	pos := point{x: screenX, y: screenY}
	m.positions[node.ID] = pos

	style := baseClass
	if _, ok := selectedPath[node.ID]; ok {
		style = styleSelected
	}
	nodeGlyph := '⬡'
	if baseClass != styleBack {
		nodeGlyph = '⬢'
	}
	if style == styleSelected {
		nodeGlyph = '◈'
	}

	grid.drawConnector(parent, pos, connectorJoinY(parent, pos, siblingIndex, siblingCount), style)
	grid.set(pos.x, pos.y, nodeGlyph, style)
	return pos
}

func nudgeSingletonBranch(screenX, parentX, anchorX, laneMin, laneMax, depth int) int {
	left := max(laneMin+1, 0)
	right := laneMax - 1
	if right <= left {
		return screenX
	}
	offset := 3
	preferRight := false
	switch {
	case parentX < anchorX:
		preferRight = true
	case parentX > anchorX:
		preferRight = false
	default:
		preferRight = depth%2 == 0
	}
	if preferRight {
		candidate := min(screenX+offset, right)
		if candidate != parentX {
			return candidate
		}
		candidate = max(screenX-offset, left)
		if candidate != parentX {
			return candidate
		}
		return screenX
	}
	candidate := max(screenX-offset, left)
	if candidate != parentX {
		return candidate
	}
	candidate = min(screenX+offset, right)
	if candidate != parentX {
		return candidate
	}
	return screenX
}

func (m *Model) renderSubtree(
	laneMin int,
	laneMax int,
	root point,
	rootLocalX float64,
	scaleX, scaleY, shear, parallaxLift float64,
	layout map[string]point,
	nodeByID map[string]forest.ViewNode,
	children map[string][]string,
	nodeID string,
	parent point,
	siblingIndex int,
	siblingCount int,
	grid *canvasGrid,
	baseClass styleClass,
	selectedPath map[string]struct{},
) {
	node := nodeByID[nodeID]
	pos := m.projectNode(laneMin, laneMax, root, rootLocalX, scaleX, scaleY, shear, parallaxLift, layout, node, parent, siblingIndex, siblingCount, grid, baseClass, selectedPath)
	for childIdx, childID := range children[nodeID] {
		m.renderSubtree(laneMin, laneMax, root, rootLocalX, scaleX, scaleY, shear, parallaxLift, layout, nodeByID, children, childID, pos, childIdx, len(children[nodeID]), grid, baseClass, selectedPath)
	}
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

type canvasCell struct {
	ch    rune
	style styleClass
}

type canvasGrid struct {
	w     int
	h     int
	cells [][]canvasCell
}

func newCanvasGrid(w, h int) *canvasGrid {
	cells := make([][]canvasCell, h)
	for y := range cells {
		cells[y] = make([]canvasCell, w)
		for x := range cells[y] {
			cells[y][x] = canvasCell{ch: ' ', style: styleBack}
		}
	}
	return &canvasGrid{w: w, h: h, cells: cells}
}

func (g *canvasGrid) set(x, y int, ch rune, style styleClass) {
	if x < 0 || x >= g.w || y < 0 || y >= g.h || ch == ' ' {
		return
	}
	current := g.cells[y][x]
	if current.ch != ' ' && isConnector(current.ch) && !isConnector(ch) {
		g.cells[y][x] = canvasCell{ch: ch, style: style}
		return
	}
	if current.ch != ' ' && !isConnector(current.ch) && isConnector(ch) {
		return
	}
	if current.ch == ' ' || current.style != styleSelected {
		g.cells[y][x] = canvasCell{ch: ch, style: style}
	}
}

func (g *canvasGrid) drawString(x, y int, s string, style styleClass) {
	runes := []rune(s)
	for i, ch := range runes {
		g.set(x+i, y, ch, style)
	}
}

func (g *canvasGrid) drawVertical(x, y0, y1 int, style styleClass) {
	if x < 0 || x >= g.w {
		return
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := max(y0, 0); y <= min(y1, g.h-1); y++ {
		g.set(x, y, '│', style)
	}
}

func connectorJoinY(parent, child point, siblingIndex, siblingCount int) int {
	if child.y >= parent.y {
		return child.y
	}
	upper := child.y + 1
	lower := parent.y - 1
	if lower <= upper {
		return upper
	}
	if siblingCount <= 1 {
		return upper + max((lower-upper)/2, 1)
	}
	span := lower - upper
	offset := int(math.Round(float64(span) * float64(siblingIndex+1) / float64(siblingCount+1)))
	return clamp(upper+offset, upper, lower)
}

func (g *canvasGrid) drawConnector(parent, child point, joinY int, style styleClass) {
	if parent == child {
		return
	}
	if child.y >= parent.y {
		g.drawVertical(parent.x, parent.y, child.y, style)
		return
	}
	joinY = clamp(joinY, child.y+1, max(parent.y-1, child.y+1))
	g.drawVertical(child.x, child.y+1, joinY-1, style)
	if child.x < parent.x {
		g.set(child.x, joinY, '╰', style)
		for x := child.x + 1; x < parent.x; x++ {
			g.set(x, joinY, '─', style)
		}
		g.set(parent.x, joinY, '╮', style)
	} else if child.x > parent.x {
		g.set(child.x, joinY, '╯', style)
		for x := parent.x + 1; x < child.x; x++ {
			g.set(x, joinY, '─', style)
		}
		g.set(parent.x, joinY, '╭', style)
	}
	g.drawVertical(parent.x, joinY+1, parent.y-1, style)
}

func (g *canvasGrid) render(th *theme.Theme) string {
	if g.h == 0 || g.w == 0 {
		return ""
	}
	backStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	midStyle := lipgloss.NewStyle().Foreground(th.Palette.Secondary)
	frontStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary)
	selectedStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning).Bold(true)
	rootStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)

	var b strings.Builder
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			cell := g.cells[y][x]
			ch := string(cell.ch)
			switch cell.style {
			case styleSelected:
				b.WriteString(selectedStyle.Render(ch))
			case styleFront:
				b.WriteString(frontStyle.Render(ch))
			case styleMid:
				b.WriteString(midStyle.Render(ch))
			case styleRoot:
				b.WriteString(rootStyle.Render(ch))
			default:
				b.WriteString(backStyle.Render(ch))
			}
		}
		if y < g.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func isConnector(ch rune) bool {
	switch ch {
	case '│', '─', '╰', '╮', '╭', '╯':
		return true
	default:
		return false
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
