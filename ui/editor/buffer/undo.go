package buffer

import "time"

// EditType distinguishes insert from delete operations.
type EditType int

const (
	// EditInsert is a text insertion.
	EditInsert EditType = iota
	// EditDelete is a text deletion.
	EditDelete
)

// EditOp records a single atomic edit for undo/redo.
type EditOp struct {
	Type    EditType
	Pos     int
	Text    string // inserted text (Insert) or deleted text (Delete)
	OldText string // for Delete: the text that was removed
}

// UndoNode is a single node in the branching undo tree.
type UndoNode struct {
	ID             int
	Parent         *UndoNode
	Children       []*UndoNode
	Timestamp      time.Time
	Edit           EditOp
	GroupID        int   // non-zero when part of a grouped operation (e.g. formatting)
	CursorSnapshot []int // Multi-cursor positions at group start; nil for single-cursor edits.
}

// UndoTree implements a branching undo history matching neovim semantics.
// New edits branch from the current position; redo follows the most recent
// child. The tree is bounded by maxNodes to prevent unbounded growth.
type UndoTree struct {
	root           *UndoNode
	current        *UndoNode
	nextID         int
	maxNodes       int
	nodeCount      int
	activeGroup    int   // non-zero while recording a grouped operation
	nextGroupID    int   // monotonic source for group IDs
	pendingCursors []int // cursor snapshot to attach to the first Record in this group
}

// defaultMaxUndoNodes is the default upper bound on tree size.
const defaultMaxUndoNodes = 10000

// NewUndoTree creates an undo tree bounded to maxNodes entries.
// A maxNodes of zero or negative uses the default.
func NewUndoTree(maxNodes int) *UndoTree {
	if maxNodes <= 0 {
		maxNodes = defaultMaxUndoNodes
	}
	root := &UndoNode{ID: 0, Timestamp: time.Now()}
	return &UndoTree{
		root:        root,
		current:     root,
		nextID:      1,
		maxNodes:    maxNodes,
		nodeCount:   1,
		nextGroupID: 1,
	}
}

// BeginGroup starts a grouped operation. All edits recorded until EndGroup
// share the same GroupID and are undone/redone as a single step.
func (ut *UndoTree) BeginGroup() {
	ut.activeGroup = ut.nextGroupID
	ut.nextGroupID++
}

// BeginGroupWithCursors starts a grouped operation and stores a multi-cursor
// snapshot. The snapshot is stamped on the first Record() in this group.
func (ut *UndoTree) BeginGroupWithCursors(cursors []int) {
	ut.BeginGroup()
	ut.pendingCursors = cursors
}

// EndGroup ends the current grouped operation.
func (ut *UndoTree) EndGroup() {
	ut.activeGroup = 0
	ut.pendingCursors = nil
}

// Record appends a new edit as a child of the current node and advances.
func (ut *UndoTree) Record(edit EditOp) {
	node := &UndoNode{
		ID:        ut.nextID,
		Parent:    ut.current,
		Timestamp: time.Now(),
		Edit:      edit,
		GroupID:   ut.activeGroup,
	}
	if ut.pendingCursors != nil {
		node.CursorSnapshot = ut.pendingCursors
		ut.pendingCursors = nil
	}
	ut.nextID++
	ut.current.Children = append(ut.current.Children, node)
	ut.current = node
	ut.nodeCount++
	ut.pruneIfNeeded()
}

// Undo moves backward through the tree and returns edits to reverse.
// When the current node belongs to a group, all nodes in that group are
// collected so the caller can reverse the entire grouped operation at once.
// The returned cursor snapshot (may be nil) is from the first node in
// the group, for restoring multi-cursor positions.
func (ut *UndoTree) Undo() ([]EditOp, []int, bool) {
	if !ut.CanUndo() {
		return nil, nil, false
	}
	var cursorSnap []int
	edits := []EditOp{ut.current.Edit}
	if ut.current.CursorSnapshot != nil {
		cursorSnap = ut.current.CursorSnapshot
	}
	gid := ut.current.GroupID
	ut.current = ut.current.Parent
	for gid != 0 && ut.CanUndo() && ut.current.GroupID == gid {
		edits = append(edits, ut.current.Edit)
		if cursorSnap == nil && ut.current.CursorSnapshot != nil {
			cursorSnap = ut.current.CursorSnapshot
		}
		ut.current = ut.current.Parent
	}
	return edits, cursorSnap, true
}

// Redo moves forward through the tree and returns edits to re-apply.
// When the next node belongs to a group, all consecutive nodes in that
// group are collected so the caller can re-apply the entire operation.
// The returned cursor snapshot (may be nil) is from the first node in
// the group, for restoring multi-cursor positions.
func (ut *UndoTree) Redo() ([]EditOp, []int, bool) {
	if !ut.CanRedo() {
		return nil, nil, false
	}
	newest := ut.newestChild(ut.current)
	ut.current = newest
	edits := []EditOp{newest.Edit}
	var cursorSnap []int
	if newest.CursorSnapshot != nil {
		cursorSnap = newest.CursorSnapshot
	}
	gid := newest.GroupID
	for gid != 0 && ut.CanRedo() {
		next := ut.newestChild(ut.current)
		if next.GroupID != gid {
			break
		}
		ut.current = next
		edits = append(edits, next.Edit)
	}
	return edits, cursorSnap, true
}

// CanUndo reports whether there is an undo step available.
func (ut *UndoTree) CanUndo() bool {
	return ut.current != ut.root
}

// CanRedo reports whether there is a redo step available.
func (ut *UndoTree) CanRedo() bool {
	return len(ut.current.Children) > 0
}

// newestChild returns the child with the highest ID (most recent).
func (ut *UndoTree) newestChild(node *UndoNode) *UndoNode {
	best := node.Children[0]
	for _, c := range node.Children[1:] {
		if c.ID > best.ID {
			best = c
		}
	}
	return best
}

// pruneIfNeeded removes the oldest leaf nodes until nodeCount <= maxNodes.
func (ut *UndoTree) pruneIfNeeded() {
	for ut.nodeCount > ut.maxNodes {
		leaf := ut.findOldestLeaf(ut.root)
		if leaf == nil || leaf == ut.current || leaf == ut.root {
			return
		}
		ut.removeLeaf(leaf)
	}
}

// findOldestLeaf walks the tree to find the leaf with the smallest ID.
func (ut *UndoTree) findOldestLeaf(node *UndoNode) *UndoNode {
	if len(node.Children) == 0 {
		return node
	}
	var oldest *UndoNode
	for _, child := range node.Children {
		candidate := ut.findOldestLeaf(child)
		if candidate == nil {
			continue
		}
		if oldest == nil || candidate.ID < oldest.ID {
			oldest = candidate
		}
	}
	return oldest
}

// removeLeaf detaches a leaf node from its parent.
func (ut *UndoTree) removeLeaf(leaf *UndoNode) {
	parent := leaf.Parent
	if parent == nil {
		return
	}
	children := parent.Children[:0]
	for _, c := range parent.Children {
		if c != leaf {
			children = append(children, c)
		}
	}
	parent.Children = children
	ut.nodeCount--
}
