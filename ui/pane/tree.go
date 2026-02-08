// Package pane provides a binary split tree for managing editor panes.
//
// Each leaf in the tree represents a visible pane (editor or preview).
// Internal nodes represent splits — either vertical (left | right) or
// horizontal (top / bottom). The tree supports recursive layout computation,
// spatial navigation, and dynamic splitting/closing of panes.
package pane

import (
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/layout"
)

// PaneID uniquely identifies a leaf pane. Internal nodes have ID 0.
type PaneID int

// SplitDir indicates the orientation of a split node.
type SplitDir int

const (
	// SplitVertical arranges children side by side: Left | Right.
	SplitVertical SplitDir = iota
	// SplitHorizontal stacks children: Top / Bottom.
	SplitHorizontal
)

// Rect describes a rectangular region within the code panel's content area.
type Rect struct {
	X, Y, W, H int
}

// minPaneW is the minimum width a pane can have after a split. Derived from
// the smallest usable editor width (gutter + ~15 content columns).
const minPaneW = 20

// minPaneH is the minimum height a pane can have after a split. Derived from
// tab bar (2 rows) + at least 3 content lines.
const minPaneH = 5

// dividerSize is the character width/height consumed by a split divider.
const dividerSize = 1

// defaultRatio is the initial split ratio (equal halves).
const defaultRatio = 0.5

// Node is either a leaf pane or an internal split node.
//
// Leaf: Left == nil && Right == nil, ID > 0.
// Internal: Left != nil && Right != nil, ID == 0.
type Node struct {
	// Leaf fields.
	ID PaneID

	// Internal node fields.
	Dir   SplitDir
	Ratio float64 // Fraction [0.1, 0.9] of space for Left/Top child.
	Left  *Node   // Left (vertical) or Top (horizontal).
	Right *Node   // Right (vertical) or Bottom (horizontal).
}

// NewLeaf creates a leaf node with the given pane ID.
func NewLeaf(id PaneID) *Node {
	return &Node{ID: id}
}

// ---------------------------------------------------------------------------
// Predicates
// ---------------------------------------------------------------------------

// IsLeaf reports whether n is a leaf node (has no children).
func (n *Node) IsLeaf() bool {
	return n.Left == nil && n.Right == nil
}

// LeafCount returns the total number of leaf panes in the subtree.
func (n *Node) LeafCount() int {
	if n.IsLeaf() {
		return 1
	}
	return n.Left.LeafCount() + n.Right.LeafCount()
}

// ---------------------------------------------------------------------------
// Lookup
// ---------------------------------------------------------------------------

// Find returns the node with the given pane ID, or nil if not found.
func (n *Node) Find(id PaneID) *Node {
	if n.IsLeaf() {
		if n.ID == id {
			return n
		}
		return nil
	}
	if found := n.Left.Find(id); found != nil {
		return found
	}
	return n.Right.Find(id)
}

// Contains reports whether a leaf with the given ID exists in the subtree.
func (n *Node) Contains(id PaneID) bool {
	return n.Find(id) != nil
}

// findParent returns the parent node of the leaf with the given ID and
// whether the leaf is the left child. Returns nil if id is the root or
// not found.
func (n *Node) findParent(id PaneID) (parent *Node, isLeft bool) {
	if n.IsLeaf() {
		return nil, false
	}
	if n.Left.IsLeaf() && n.Left.ID == id {
		return n, true
	}
	if n.Right.IsLeaf() && n.Right.ID == id {
		return n, false
	}
	if parent, isLeft = n.Left.findParent(id); parent != nil {
		return parent, isLeft
	}
	return n.Right.findParent(id)
}

// ---------------------------------------------------------------------------
// Mutation
// ---------------------------------------------------------------------------

// Split replaces the leaf with ID `target` with an internal node. The
// original leaf becomes Left/Top and a new leaf with `newID` becomes
// Right/Bottom. Returns false if the target is not found or if the
// resulting panes would be smaller than minimum dimensions.
//
// The caller must provide the current area of the target pane so minimum
// size checks can be enforced. Pass a zero Rect to skip the check.
func (n *Node) Split(target, newID PaneID, dir SplitDir, area Rect) bool {
	// Enforce minimum pane size.
	if area.W > 0 || area.H > 0 {
		if !canSplit(area, dir) {
			return false
		}
	}
	return n.splitRecursive(target, newID, dir)
}

func canSplit(area Rect, dir SplitDir) bool {
	if dir == SplitVertical {
		halfW := (area.W - dividerSize) / 2
		return halfW >= minPaneW
	}
	halfH := (area.H - dividerSize) / 2
	return halfH >= minPaneH
}

func (n *Node) splitRecursive(target, newID PaneID, dir SplitDir) bool {
	if n.IsLeaf() {
		if n.ID != target {
			return false
		}
		// Convert this leaf into an internal node.
		n.Left = &Node{ID: n.ID}
		n.Right = &Node{ID: newID}
		n.ID = 0
		n.Dir = dir
		n.Ratio = defaultRatio
		return true
	}
	if n.Left.splitRecursive(target, newID, dir) {
		return true
	}
	return n.Right.splitRecursive(target, newID, dir)
}

// Close removes the leaf with ID `target` from the tree. The target's
// sibling replaces their parent node, effectively collapsing the split.
// Returns the leftmost leaf ID of the sibling subtree (suitable as new
// focus target) and true, or (0, false) if target was not found or is
// the root.
func (n *Node) Close(target PaneID) (PaneID, bool) {
	parent, isLeft := n.findParent(target)
	if parent == nil {
		return 0, false
	}

	// Determine sibling.
	sibling := parent.Right
	if !isLeft {
		sibling = parent.Left
	}

	// Replace parent's content with sibling's content.
	*parent = *sibling

	// Return the leftmost leaf of the (now-promoted) sibling.
	return parent.leftmostLeaf(), true
}

// leftmostLeaf returns the ID of the leftmost (or topmost) leaf.
func (n *Node) leftmostLeaf() PaneID {
	if n.IsLeaf() {
		return n.ID
	}
	return n.Left.leftmostLeaf()
}

// rightmostLeaf returns the ID of the rightmost (or bottommost) leaf.
func (n *Node) rightmostLeaf() PaneID {
	if n.IsLeaf() {
		return n.ID
	}
	return n.Right.rightmostLeaf()
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// ComputeLayout assigns a Rect to each leaf pane given the root's available
// area. The returned map contains one entry per leaf.
func (n *Node) ComputeLayout(area Rect) map[PaneID]Rect {
	out := make(map[PaneID]Rect, n.LeafCount())
	n.computeLayout(area, out)
	return out
}

func (n *Node) computeLayout(area Rect, out map[PaneID]Rect) {
	if n.IsLeaf() {
		out[n.ID] = area
		return
	}
	if n.Dir == SplitVertical {
		leftW := int(float64(area.W-dividerSize) * n.Ratio)
		rightW := area.W - dividerSize - leftW
		n.Left.computeLayout(Rect{area.X, area.Y, leftW, area.H}, out)
		n.Right.computeLayout(Rect{area.X + leftW + dividerSize, area.Y, rightW, area.H}, out)
		return
	}
	// SplitHorizontal.
	topH := int(float64(area.H-dividerSize) * n.Ratio)
	bottomH := area.H - dividerSize - topH
	n.Left.computeLayout(Rect{area.X, area.Y, area.W, topH}, out)
	n.Right.computeLayout(Rect{area.X, area.Y + topH + dividerSize, area.W, bottomH}, out)
}

// ---------------------------------------------------------------------------
// Dividers
// ---------------------------------------------------------------------------

// Divider describes a split divider line between two child subtrees.
type Divider struct {
	X, Y int      // Top-left position of the divider.
	Len  int      // Length in characters (height for vertical, width for horizontal).
	Dir  SplitDir // SplitVertical → column of │, SplitHorizontal → row of ─.
}

// Dividers returns the positions of all split dividers in the tree for the
// given root area. Uses an explicit stack (no recursion).
func (n *Node) Dividers(area Rect) []Divider {
	type entry struct {
		node *Node
		area Rect
	}
	stack := []entry{{n, area}}
	var out []Divider

	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if e.node.IsLeaf() {
			continue
		}

		a := e.area
		if e.node.Dir == SplitVertical {
			leftW := int(float64(a.W-dividerSize) * e.node.Ratio)
			divX := a.X + leftW
			out = append(out, Divider{X: divX, Y: a.Y, Len: a.H, Dir: SplitVertical})
			rightW := a.W - dividerSize - leftW
			stack = append(stack, entry{e.node.Left, Rect{a.X, a.Y, leftW, a.H}})
			stack = append(stack, entry{e.node.Right, Rect{divX + dividerSize, a.Y, rightW, a.H}})
		} else {
			topH := int(float64(a.H-dividerSize) * e.node.Ratio)
			divY := a.Y + topH
			out = append(out, Divider{X: a.X, Y: divY, Len: a.W, Dir: SplitHorizontal})
			bottomH := a.H - dividerSize - topH
			stack = append(stack, entry{e.node.Left, Rect{a.X, a.Y, a.W, topH}})
			stack = append(stack, entry{e.node.Right, Rect{a.X, divY + dividerSize, a.W, bottomH}})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Traversal
// ---------------------------------------------------------------------------

// Leaves returns all leaf PaneIDs in spatial order: left-to-right for
// vertical splits, top-to-bottom for horizontal splits.
func (n *Node) Leaves() []PaneID {
	if n.IsLeaf() {
		return []PaneID{n.ID}
	}
	left := n.Left.Leaves()
	right := n.Right.Leaves()
	return append(left, right...)
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// Navigate returns the adjacent pane in the given direction from the leaf
// with ID `from`. Uses tree-based spatial adjacency — works for arbitrary
// tree shapes including mixed vertical/horizontal splits.
//
// Returns (targetID, true) if a neighbor was found, or (from, false) if
// navigation in that direction is not possible (e.g., already at the edge).
func (n *Node) Navigate(from PaneID, dir layout.Direction) (PaneID, bool) {
	// Build path from root to the target leaf.
	path := n.pathTo(from)
	if len(path) == 0 {
		return from, false
	}

	// Walk up the path looking for a split whose direction matches the
	// navigation direction and where `from` is on the correct side.
	for i := len(path) - 1; i > 0; i-- {
		ancestor := path[i-1]
		child := path[i]
		if ancestor.IsLeaf() {
			continue
		}

		inLeft := ancestor.Left == child

		switch {
		case dir == layout.DirRight && ancestor.Dir == SplitVertical && inLeft:
			return ancestor.Right.leftmostLeaf(), true
		case dir == layout.DirLeft && ancestor.Dir == SplitVertical && !inLeft:
			return ancestor.Left.rightmostLeaf(), true
		case dir == layout.DirDown && ancestor.Dir == SplitHorizontal && inLeft:
			return ancestor.Right.leftmostLeaf(), true
		case dir == layout.DirUp && ancestor.Dir == SplitHorizontal && !inLeft:
			return ancestor.Left.rightmostLeaf(), true
		}
	}
	return from, false
}

// pathTo returns the chain of nodes from root to the leaf with the given
// ID. Returns nil if the leaf is not found.
func (n *Node) pathTo(id PaneID) []*Node {
	if n.IsLeaf() {
		if n.ID == id {
			return []*Node{n}
		}
		return nil
	}
	if p := n.Left.pathTo(id); p != nil {
		return append([]*Node{n}, p...)
	}
	if p := n.Right.pathTo(id); p != nil {
		return append([]*Node{n}, p...)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sub-grid (for layout.PanelGroup integration)
// ---------------------------------------------------------------------------

// ToSubGrid converts the tree into a 2D grid of FocusIDs suitable for
// use in layout.PanelGroup.SubPanels. Vertical splits produce columns
// within a row; horizontal splits produce separate rows.
func (n *Node) ToSubGrid() [][]component.FocusID {
	if n.IsLeaf() {
		return [][]component.FocusID{{n.FocusID()}}
	}
	left := n.Left.ToSubGrid()
	right := n.Right.ToSubGrid()
	if n.Dir == SplitVertical {
		return mergeColumns(left, right)
	}
	return append(left, right...)
}

// FocusID returns the component.FocusID for this leaf node.
// All panes (editor and preview) use dynamic IDs: FocusPaneBase + PaneID.
func (n *Node) FocusID() component.FocusID {
	return PaneFocusID(n.ID)
}

// mergeColumns concatenates the columns of two grids. If the grids have
// different row counts, the shorter grid's rows are repeated to match.
func mergeColumns(left, right [][]component.FocusID) [][]component.FocusID {
	lr := len(left)
	rr := len(right)
	rows := max(lr, rr)
	out := make([][]component.FocusID, rows)
	for i := range rows {
		li := min(i, lr-1)
		ri := min(i, rr-1)
		row := make([]component.FocusID, 0, len(left[li])+len(right[ri]))
		row = append(row, left[li]...)
		row = append(row, right[ri]...)
		out[i] = row
	}
	return out
}

// ---------------------------------------------------------------------------
// FocusID helpers
// ---------------------------------------------------------------------------

// PaneFocusID returns the dynamic FocusID for the given PaneID.
func PaneFocusID(id PaneID) component.FocusID {
	return component.FocusPaneBase + component.FocusID(id)
}

// IsPaneFocus reports whether the FocusID is a dynamic pane ID.
func IsPaneFocus(fid component.FocusID) bool {
	return fid >= component.FocusPaneBase
}

// PaneIDFromFocus extracts the PaneID from a dynamic FocusID.
func PaneIDFromFocus(fid component.FocusID) PaneID {
	return PaneID(fid - component.FocusPaneBase)
}
