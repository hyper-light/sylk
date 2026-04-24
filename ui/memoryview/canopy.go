package memoryview

import (
	"hash/fnv"
	"math"
	"sort"

	"github.com/adalundhe/sylk/core/forest"
)

// canopyLayout is the full positional plan for a single tree. The
// renderer walks these maps once to paint the grid; no further
// recursion is needed. Every node in the tree gets exactly one entry
// in positions and tiers.
type canopyLayout struct {
	positions   map[string]point
	tiers       map[string]nodeTier
	children    map[string][]string
	nodeByID    map[string]forest.ViewNode
	rootID      string
	trunkTopID  string // node at the upper end of the trunk spine
	trunkX      int
	trunkTopY   int
	trunkBase   int
	crownLeaves []string // ordered left→right at crown height
	// crownLeafSet lets the renderer route crown-leaf connectors from
	// the trunk top (visual crown origin) rather than the leaf's
	// actual data parent — preserving the plant-like silhouette even
	// when the underlying tree is flat (root with many direct leaves).
	crownLeafSet map[string]struct{}
}

// canopyParams tunes the geometric parameters of a single tree.
// depthRatio in [0,1] (0 = furthest back, 1 = frontmost) biases both
// parallax shear and the node-tier ramp so the viewer reads the
// side-by-side trees as occupying different depth planes.
type canopyParams struct {
	trunkX       int
	laneMin      int
	laneMax      int
	bottomY      int
	maxHeight    int
	depthRatio   float64
	sessionSalt  uint64 // deterministic jitter per session
	treeSalt     uint64 // deterministic jitter per tree (stable across frames)
}

// computeCanopyLayout produces positional data for every node in a
// tree using trunk + radial-canopy geometry.
//
//   - The primary root is placed at (trunkX, bottomY).
//   - The longest root→leaf path forms the trunk (spine), walked
//     upward in evenly-spaced steps.
//   - Leaves (no children) fan out radially at the trunk top in a
//     clustered crown, widths capped by the available lane.
//   - Internal, off-trunk nodes hang laterally from their parent
//     position along the trunk or mid-crown.
//   - Secondary roots (rare: usually 1 root per family tree) spawn
//     a miniature side-trunk offset from the primary.
//
// Positions are clamped to the lane and to the canvas rectangle.
func computeCanopyLayout(tree forest.ViewTree, p canopyParams) canopyLayout {
	nodeByID := make(map[string]forest.ViewNode, len(tree.Nodes))
	childrenOf := make(map[string][]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		nodeByID[node.ID] = node
	}
	for _, node := range tree.Nodes {
		if node.ParentID != "" {
			childrenOf[node.ParentID] = append(childrenOf[node.ParentID], node.ID)
		}
	}
	for id := range childrenOf {
		sort.SliceStable(childrenOf[id], func(i, j int) bool {
			a, b := nodeByID[childrenOf[id][i]], nodeByID[childrenOf[id][j]]
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.ID < b.ID
		})
	}

	roots := append([]string(nil), tree.Roots...)
	if len(roots) == 0 {
		for _, node := range tree.Nodes {
			if node.ParentID == "" {
				roots = append(roots, node.ID)
			}
		}
	}
	if len(roots) == 0 {
		return canopyLayout{}
	}

	// Longest descendant path for trunk selection.
	longest := make(map[string]int, len(tree.Nodes))
	var measure func(id string) int
	measure = func(id string) int {
		if d, ok := longest[id]; ok {
			return d
		}
		best := 0
		for _, child := range childrenOf[id] {
			d := measure(child) + 1
			if d > best {
				best = d
			}
		}
		longest[id] = best
		return best
	}
	for _, id := range roots {
		measure(id)
	}

	layout := canopyLayout{
		positions:    make(map[string]point, len(tree.Nodes)),
		tiers:        make(map[string]nodeTier, len(tree.Nodes)),
		children:     childrenOf,
		nodeByID:     nodeByID,
		rootID:       roots[0],
		trunkX:       p.trunkX,
		crownLeafSet: make(map[string]struct{}, len(tree.Nodes)/2+1),
	}

	// Allocate trunk height by node count (bounded).
	primary := roots[0]
	trunkChain := buildTrunkChain(primary, childrenOf, longest)

	// `onTrunk` snapshots the spine membership BEFORE any
	// displacement so the off-trunk partition below correctly skips
	// the terminal leaf when we relocate it to the crown. Without
	// this, the terminal ends up in crownLeaves twice — once via the
	// partition loop and once via the explicit displacement append —
	// which then convinces placeCrownLeaves it has two leaves and
	// inflates the canopy radius.
	onTrunk := make(map[string]struct{}, len(trunkChain))
	for _, id := range trunkChain {
		onTrunk[id] = struct{}{}
	}

	// If the terminal trunk node is a leaf AND no other descendants
	// off the root would populate the crown on their own, displace
	// it into the crown so every tree gets at least one visible
	// branch. Without this, a purely linear root→child→leaf chain
	// would render as a straight pole.
	var displacedTerminal string
	if len(trunkChain) >= 2 {
		terminal := trunkChain[len(trunkChain)-1]
		if len(childrenOf[terminal]) == 0 && !hasOffTrunkLeaves(tree.Nodes, childrenOf, trunkChain) {
			displacedTerminal = terminal
			trunkChain = trunkChain[:len(trunkChain)-1]
		}
	}

	if len(trunkChain) > 0 {
		layout.trunkTopID = trunkChain[len(trunkChain)-1]
	}
	trunkHeight := clamp(len(trunkChain)*2+3, 6, p.maxHeight)
	layout.trunkBase = p.bottomY
	layout.trunkTopY = p.bottomY - trunkHeight
	if layout.trunkTopY < 1 {
		layout.trunkTopY = 1
		trunkHeight = p.bottomY - layout.trunkTopY
	}

	placeTrunkChain(trunkChain, p, trunkHeight, &layout)

	// Partition remaining nodes into crown leaves vs side branches.
	var crownLeaves []string
	var sideBranches []string
	for _, node := range tree.Nodes {
		if _, spine := onTrunk[node.ID]; spine {
			continue
		}
		if len(childrenOf[node.ID]) == 0 {
			crownLeaves = append(crownLeaves, node.ID)
		} else {
			sideBranches = append(sideBranches, node.ID)
		}
	}
	if displacedTerminal != "" {
		crownLeaves = append(crownLeaves, displacedTerminal)
	}

	// Crown arrangement: sort leaves by parent location along the
	// trunk so clusters of leaves from the same parent stay adjacent.
	sort.SliceStable(crownLeaves, func(i, j int) bool {
		a := nodeByID[crownLeaves[i]]
		b := nodeByID[crownLeaves[j]]
		pa, ok := layout.positions[a.ParentID]
		pb, ok2 := layout.positions[b.ParentID]
		switch {
		case ok && ok2 && pa.y != pb.y:
			return pa.y > pb.y // lower-trunk parents first (left in crown)
		case a.ParentID != b.ParentID:
			return a.ParentID < b.ParentID
		case !a.CreatedAt.Equal(b.CreatedAt):
			return a.CreatedAt.Before(b.CreatedAt)
		default:
			return a.ID < b.ID
		}
	})
	layout.crownLeaves = append(layout.crownLeaves, crownLeaves...)
	for _, id := range crownLeaves {
		layout.crownLeafSet[id] = struct{}{}
	}

	placeCrownLeaves(crownLeaves, p, &layout)

	// Side branches (internal nodes hanging off the trunk). Each is
	// placed near its parent's screen position, offset laterally with
	// a deterministic side choice so successive frames don't jitter.
	for _, id := range sideBranches {
		placeSideBranch(id, p, &layout)
	}

	// Handle secondary roots — extremely rare in the session forest
	// but possible. They get a short side-trunk offset from primary.
	for i, rootID := range roots {
		if i == 0 {
			continue
		}
		placeSecondaryRoot(rootID, i, p, &layout)
	}

	return layout
}

// hasOffTrunkLeaves reports whether any node outside the trunk chain
// has no children (and would populate the crown on its own). Used to
// decide whether the trunk terminal needs to be displaced for the
// tree to have a visible crown.
func hasOffTrunkLeaves(nodes []forest.ViewNode, childrenOf map[string][]string, trunkChain []string) bool {
	onTrunk := make(map[string]struct{}, len(trunkChain))
	for _, id := range trunkChain {
		onTrunk[id] = struct{}{}
	}
	for _, n := range nodes {
		if _, spine := onTrunk[n.ID]; spine {
			continue
		}
		if len(childrenOf[n.ID]) == 0 {
			return true
		}
	}
	return false
}

// buildTrunkChain returns the node IDs along the spine of the tree,
// starting from the root and always descending via the child with the
// longest remaining path. Deterministic tiebreaker: child ID.
func buildTrunkChain(root string, children map[string][]string, longest map[string]int) []string {
	chain := []string{root}
	cur := root
	for {
		kids := children[cur]
		if len(kids) == 0 {
			break
		}
		best := kids[0]
		bestLen := longest[best]
		for _, k := range kids[1:] {
			if longest[k] > bestLen || (longest[k] == bestLen && k < best) {
				best = k
				bestLen = longest[k]
			}
		}
		chain = append(chain, best)
		cur = best
	}
	return chain
}

// placeTrunkChain puts the trunk nodes at evenly spaced heights along
// the trunk column. The root is at the bottom; deeper descendants walk
// upward. A small deterministic horizontal jitter per tree keeps
// stacked trees from looking like carbon copies when they share depth.
func placeTrunkChain(chain []string, p canopyParams, trunkHeight int, layout *canopyLayout) {
	if len(chain) == 0 {
		return
	}
	if len(chain) == 1 {
		layout.positions[chain[0]] = point{x: p.trunkX, y: p.bottomY}
		layout.tiers[chain[0]] = tierForTrunk(0.0, p.depthRatio)
		return
	}
	for i, id := range chain {
		ratio := float64(i) / float64(len(chain)-1)
		y := p.bottomY - int(roundHalfUp(ratio*float64(trunkHeight-1)))
		y = clamp(y, layout.trunkTopY, p.bottomY)
		layout.positions[id] = point{x: p.trunkX, y: y}
		layout.tiers[id] = tierForTrunk(ratio, p.depthRatio)
	}
}

// tierForTrunk picks a node tier based on height along the trunk and
// the tree's global depth plane. Nodes near the crown read as "front"
// for front-plane trees and "mid" for back-plane trees, preserving
// parallax depth cueing.
func tierForTrunk(trunkRatio, depthRatio float64) nodeTier {
	// Combined score biased toward crown AND front-plane.
	score := trunkRatio*0.55 + depthRatio*0.45
	switch {
	case score < 0.2:
		return tierFar
	case score < 0.4:
		return tierBackMid
	case score < 0.6:
		return tierMid
	case score < 0.8:
		return tierNear
	default:
		return tierFront
	}
}

// placeCrownLeaves fans leaves out around the top of the trunk in a
// compressed semicircle. Horizontal spacing is scaled by the lane
// width so crowns stay inside their lane, and vertical spacing uses a
// 0.6 aspect compensation for terminal cells being ~2:1 tall.
func placeCrownLeaves(leaves []string, p canopyParams, layout *canopyLayout) {
	if len(leaves) == 0 {
		return
	}
	crownY := layout.trunkTopY
	// Canopy radius scales with leaf count, capped by available lane.
	laneHalfWidth := max((p.laneMax-p.laneMin)/2-1, 2)
	rawRadius := int(math.Ceil(math.Sqrt(float64(len(leaves))))) + 1
	canopyRadius := clamp(rawRadius, 2, laneHalfWidth)

	// Spread across 75% of a semicircle; leaves beyond that fill a
	// second inner ring above the first so we don't overflow.
	const outerFraction = 0.78
	outerCount := len(leaves)
	innerCount := 0
	// Approx 8 slots per unit radius fit without overlap.
	outerCapacity := clamp(canopyRadius*3+2, 3, 24)
	if outerCount > outerCapacity {
		innerCount = outerCount - outerCapacity
		outerCount = outerCapacity
	}

	place := func(idx, count, radius, yOffset int) {
		if count <= 0 {
			return
		}
		id := leaves[idx]
		var angle float64
		if count == 1 {
			// A lone leaf needs a clear lateral displacement so the
			// branch connector is a diagonal, not collinear with the
			// trunk. Bias the direction toward whichever side of the
			// lane has more room: a leftmost tree would otherwise
			// land its offset outside the lane and get clamped back
			// onto the trunk column, producing a visible "pole".
			angle = chooseSingletonAngle(p)
		} else {
			spread := math.Pi * outerFraction
			start := (math.Pi - spread) / 2
			angle = start + spread*float64(idx)/float64(count-1)
		}
		rx := math.Cos(angle) * float64(radius)
		ry := math.Sin(angle) * float64(radius) * 0.55 // aspect compensation
		x := p.trunkX + int(roundHalfUp(rx))
		y := crownY - int(roundHalfUp(ry)) + yOffset
		// Deterministic per-leaf jitter so clusters don't line up in a
		// perfect arc when the same number of leaves repeats across
		// neighboring trees. Singletons skip jitter — they'd otherwise
		// risk overlapping the trunk-top node.
		jx, jy := crownJitter(p.treeSalt, idx, count)
		x += jx
		y += jy
		x = clamp(x, p.laneMin+1, p.laneMax-1)
		// Never allow a crown leaf to land on the trunk-top row — it
		// would overwrite the trunk-top node's glyph.
		y = clamp(y, 1, crownY-1)
		layout.positions[id] = point{x: x, y: y}
		layout.tiers[id] = tierForCrown(idx, count, p.depthRatio)
	}

	for i := 0; i < outerCount; i++ {
		place(i, outerCount, canopyRadius, 0)
	}
	for i := 0; i < innerCount; i++ {
		place(outerCount+i, innerCount, clamp(canopyRadius-1, 1, canopyRadius), -2)
	}
}

// chooseSingletonAngle picks the crown angle for a lone leaf so the
// offset always lands inside the lane. Trees near the lane's left
// edge push right; trees near the right edge push left; trees centered
// in the lane fall back to a hash-seeded random choice so successive
// centered trees don't all lean the same way.
func chooseSingletonAngle(p canopyParams) float64 {
	laneWidth := p.laneMax - p.laneMin
	if laneWidth <= 0 {
		return math.Pi / 3
	}
	leftRoom := p.trunkX - p.laneMin
	rightRoom := p.laneMax - p.trunkX
	// At least 2 cells of room is what the offset needs; otherwise
	// force the opposite direction regardless of hash.
	const minRoom = 2
	if leftRoom < minRoom && rightRoom >= minRoom {
		return math.Pi / 3.0 // upper-right
	}
	if rightRoom < minRoom && leftRoom >= minRoom {
		return math.Pi * 2.0 / 3.0 // upper-left
	}
	// Both sides have room: hash the tree salt to stagger directions
	// across neighboring trees so the canopy doesn't all lean one way.
	if p.treeSalt&1 == 0 {
		return math.Pi * 2.0 / 3.0
	}
	return math.Pi / 3.0
}

// tierForCrown ramps crown leaves from near to front, with the middle
// of the arc being the brightest. Back-plane trees get demoted by one
// tier so far trees still read as background.
func tierForCrown(idx, count int, depthRatio float64) nodeTier {
	mid := float64(count-1) / 2
	var distance float64
	if count <= 1 {
		distance = 0
	} else {
		distance = math.Abs(float64(idx)-mid) / math.Max(mid, 1)
	}
	// center = 0, edges = 1
	brightness := 1 - distance*0.5 // 0.5 .. 1.0
	brightness *= 0.5 + 0.5*depthRatio
	switch {
	case brightness < 0.35:
		return tierFar
	case brightness < 0.55:
		return tierBackMid
	case brightness < 0.7:
		return tierMid
	case brightness < 0.85:
		return tierNear
	default:
		return tierFront
	}
}

// crownJitter returns a small deterministic perturbation in [-1, +1]
// per axis, seeded by tree+leaf identity. Keeps the crown from looking
// mechanically regular without introducing frame-to-frame flicker.
// Singletons pass through with zero jitter so they can't land on top
// of the trunk top node.
func crownJitter(salt uint64, idx, count int) (int, int) {
	if count <= 1 {
		return 0, 0
	}
	h := fnv.New64a()
	var buf [16]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(salt >> (8 * i))
	}
	buf[8] = byte(idx)
	buf[9] = byte(idx >> 8)
	_, _ = h.Write(buf[:10])
	v := h.Sum64()
	// (v % 3) - 1 yields exactly {-1, 0, +1}.
	return int(v%3) - 1, int((v/3)%3) - 1
}

// placeSideBranch positions an internal (non-leaf, non-trunk) node at
// its parent's trunk height, laterally offset. Offset side is chosen
// by a stable per-node hash of the parent+child IDs so the branch
// doesn't hop sides when sibling ordering shifts.
func placeSideBranch(id string, p canopyParams, layout *canopyLayout) {
	node, ok := layout.nodeByID[id]
	if !ok {
		return
	}
	parent, ok := layout.positions[node.ParentID]
	if !ok {
		parent = point{x: p.trunkX, y: p.bottomY - 2}
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(node.ParentID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(node.ID))
	v := h.Sum64()
	side := 1
	if v&1 == 0 {
		side = -1
	}
	offset := 2 + int(v>>1)%3
	x := parent.x + side*offset
	y := parent.y - 1
	x = clamp(x, p.laneMin+1, p.laneMax-1)
	y = clamp(y, 1, p.bottomY-1)
	layout.positions[id] = point{x: x, y: y}
	layout.tiers[id] = tierMid
}

// placeSecondaryRoot handles multi-root trees by offsetting secondary
// roots as mini-trunks in the lane. The primary tree already owns the
// center; secondaries flank at a few cells' distance.
func placeSecondaryRoot(id string, index int, p canopyParams, layout *canopyLayout) {
	side := 1
	if index%2 == 0 {
		side = -1
	}
	offset := 3 + (index/2)*2
	x := p.trunkX + side*offset
	x = clamp(x, p.laneMin+1, p.laneMax-1)
	layout.positions[id] = point{x: x, y: p.bottomY}
	layout.tiers[id] = tierMid
}
