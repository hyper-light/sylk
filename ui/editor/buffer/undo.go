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
	ID        int
	Parent    *UndoNode
	Children  []*UndoNode
	Timestamp time.Time
	Edit      EditOp
}

// UndoTree implements a branching undo history matching neovim semantics.
// New edits branch from the current position; redo follows the most recent
// child. The tree is bounded by maxNodes to prevent unbounded growth.
type UndoTree struct {
	root     *UndoNode
	current  *UndoNode
	nextID   int
	maxNodes int
	nodeCount int
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
		root:      root,
		current:   root,
		nextID:    1,
		maxNodes:  maxNodes,
		nodeCount: 1,
	}
}

// Record appends a new edit as a child of the current node and advances.
func (ut *UndoTree) Record(edit EditOp) {
	node := &UndoNode{
		ID:        ut.nextID,
		Parent:    ut.current,
		Timestamp: time.Now(),
		Edit:      edit,
	}
	ut.nextID++
	ut.current.Children = append(ut.current.Children, node)
	ut.current = node
	ut.nodeCount++
	ut.pruneIfNeeded()
}

// Undo moves to the parent node and returns the edit that should be reversed.
func (ut *UndoTree) Undo() (EditOp, bool) {
	if !ut.CanUndo() {
		return EditOp{}, false
	}
	edit := ut.current.Edit
	ut.current = ut.current.Parent
	return edit, true
}

// Redo moves to the newest child and returns the edit to re-apply.
func (ut *UndoTree) Redo() (EditOp, bool) {
	if !ut.CanRedo() {
		return EditOp{}, false
	}
	newest := ut.newestChild(ut.current)
	ut.current = newest
	return newest.Edit, true
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
