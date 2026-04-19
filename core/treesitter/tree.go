package treesitter

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

type Tree struct {
	inner  *sitter.Tree
	source []byte
}

func (t *Tree) RootNode() *Node {
	return &Node{
		inner:  t.inner.RootNode(),
		source: t.source,
	}
}

func (t *Tree) Copy() *Tree {
	return &Tree{
		inner:  t.inner.Clone(),
		source: t.source,
	}
}

func (t *Tree) Edit(edit *InputEdit) {
	if t == nil || t.inner == nil || edit == nil {
		return
	}
	t.inner.Edit(edit.toSitter())
}

func (t *Tree) ChangedRanges(other *Tree) []Range {
	if t == nil || other == nil || t.inner == nil || other.inner == nil {
		return nil
	}
	changed := t.inner.ChangedRanges(other.inner)
	ranges := make([]Range, 0, len(changed))
	for _, r := range changed {
		ranges = append(ranges, Range{
			StartPoint: Point{Row: uint32(r.StartPoint.Row), Column: uint32(r.StartPoint.Column)},
			EndPoint:   Point{Row: uint32(r.EndPoint.Row), Column: uint32(r.EndPoint.Column)},
			StartByte:  uint(r.StartByte),
			EndByte:    uint(r.EndByte),
		})
	}
	return ranges
}

func (t *Tree) Close() {
	t.inner.Close()
}

func (t *Tree) Source() []byte {
	return t.source
}

type Node struct {
	inner  *sitter.Node
	source []byte
}

func (n *Node) Type() string {
	return n.inner.Kind()
}

func (n *Node) StartByte() uint {
	return n.inner.StartByte()
}

func (n *Node) EndByte() uint {
	return n.inner.EndByte()
}

func (n *Node) StartPosition() Point {
	p := n.inner.StartPosition()
	return Point{Row: uint32(p.Row), Column: uint32(p.Column)}
}

func (n *Node) EndPosition() Point {
	p := n.inner.EndPosition()
	return Point{Row: uint32(p.Row), Column: uint32(p.Column)}
}

func (n *Node) ChildCount() uint {
	return uint(n.inner.ChildCount())
}

func (n *Node) Child(index uint) *Node {
	child := n.inner.Child(index)
	if child == nil {
		return nil
	}
	return &Node{inner: child, source: n.source}
}

func (n *Node) NamedChildCount() uint {
	return uint(n.inner.NamedChildCount())
}

func (n *Node) NamedChild(index uint) *Node {
	child := n.inner.NamedChild(index)
	if child == nil {
		return nil
	}
	return &Node{inner: child, source: n.source}
}

func (n *Node) Parent() *Node {
	parent := n.inner.Parent()
	if parent == nil {
		return nil
	}
	return &Node{inner: parent, source: n.source}
}

func (n *Node) NextSibling() *Node {
	sibling := n.inner.NextSibling()
	if sibling == nil {
		return nil
	}
	return &Node{inner: sibling, source: n.source}
}

func (n *Node) PrevSibling() *Node {
	sibling := n.inner.PrevSibling()
	if sibling == nil {
		return nil
	}
	return &Node{inner: sibling, source: n.source}
}

func (n *Node) NextNamedSibling() *Node {
	sibling := n.inner.NextNamedSibling()
	if sibling == nil {
		return nil
	}
	return &Node{inner: sibling, source: n.source}
}

func (n *Node) PrevNamedSibling() *Node {
	sibling := n.inner.PrevNamedSibling()
	if sibling == nil {
		return nil
	}
	return &Node{inner: sibling, source: n.source}
}

func (n *Node) ChildByFieldName(name string) *Node {
	child := n.inner.ChildByFieldName(name)
	if child == nil {
		return nil
	}
	return &Node{inner: child, source: n.source}
}

func (n *Node) IsNull() bool {
	return n.inner == nil
}

func (n *Node) IsNamed() bool {
	return n.inner.IsNamed()
}

func (n *Node) HasError() bool {
	return n.inner.HasError()
}

// DescendantForByteRange returns the smallest node within this node that
// spans the given byte range.
func (n *Node) DescendantForByteRange(start, end uint) *Node {
	result := n.inner.DescendantForByteRange(start, end)
	if result == nil {
		return nil
	}
	return &Node{inner: result, source: n.source}
}

// NamedDescendantForByteRange returns the smallest named node within this
// node that spans the given byte range.
func (n *Node) NamedDescendantForByteRange(start, end uint) *Node {
	result := n.inner.NamedDescendantForByteRange(start, end)
	if result == nil {
		return nil
	}
	return &Node{inner: result, source: n.source}
}

// DescendantForPointRange returns the smallest node within this node that
// spans the given row/column range.
func (n *Node) DescendantForPointRange(startRow, startCol, endRow, endCol uint32) *Node {
	start := sitter.Point{Row: uint(startRow), Column: uint(startCol)}
	end := sitter.Point{Row: uint(endRow), Column: uint(endCol)}
	result := n.inner.DescendantForPointRange(start, end)
	if result == nil {
		return nil
	}
	return &Node{inner: result, source: n.source}
}

// NamedDescendantForPointRange returns the smallest named node within this
// node that spans the given row/column range.
func (n *Node) NamedDescendantForPointRange(startRow, startCol, endRow, endCol uint32) *Node {
	start := sitter.Point{Row: uint(startRow), Column: uint(startCol)}
	end := sitter.Point{Row: uint(endRow), Column: uint(endCol)}
	result := n.inner.NamedDescendantForPointRange(start, end)
	if result == nil {
		return nil
	}
	return &Node{inner: result, source: n.source}
}

func (n *Node) Content() string {
	start := n.StartByte()
	end := min(n.EndByte(), uint(len(n.source)))
	if start >= end {
		return ""
	}
	return string(n.source[start:end])
}

func (n *Node) String() string {
	return n.inner.ToSexp()
}

// Walk creates a TreeCursor starting from the tree's root node.
func (t *Tree) Walk() *TreeCursor {
	cursor := t.inner.Walk()
	return &TreeCursor{inner: cursor, source: t.source}
}

// Walk creates a TreeCursor starting from this node.
func (n *Node) Walk() *TreeCursor {
	cursor := n.inner.Walk()
	return &TreeCursor{inner: cursor, source: n.source}
}

// TreeCursor is a stateful cursor for efficient depth-first tree traversal.
type TreeCursor struct {
	inner  *sitter.TreeCursor
	source []byte
}

// GotoFirstChild moves the cursor to the first child of the current node.
// Returns false if the current node has no children.
func (c *TreeCursor) GotoFirstChild() bool {
	return c.inner.GotoFirstChild()
}

// GotoNextSibling moves the cursor to the next sibling of the current node.
// Returns false if there is no next sibling.
func (c *TreeCursor) GotoNextSibling() bool {
	return c.inner.GotoNextSibling()
}

// GotoParent moves the cursor to the parent of the current node.
// Returns false if the cursor is already at the root.
func (c *TreeCursor) GotoParent() bool {
	return c.inner.GotoParent()
}

// Node returns the current node the cursor is pointing to.
func (c *TreeCursor) Node() *Node {
	n := c.inner.Node()
	return &Node{inner: n, source: c.source}
}

// FieldName returns the field name of the current node within its parent.
func (c *TreeCursor) FieldName() string {
	return c.inner.FieldName()
}

// Close releases resources held by the cursor.
func (c *TreeCursor) Close() {
	c.inner.Close()
}

type Point struct {
	Row    uint32
	Column uint32
}

type Range struct {
	StartPoint Point
	EndPoint   Point
	StartByte  uint
	EndByte    uint
}

type InputEdit struct {
	StartByte      uint
	OldEndByte     uint
	NewEndByte     uint
	StartPosition  Point
	OldEndPosition Point
	NewEndPosition Point
}

func (e *InputEdit) toSitter() *sitter.InputEdit {
	if e == nil {
		return nil
	}
	return &sitter.InputEdit{
		StartByte:  e.StartByte,
		OldEndByte: e.OldEndByte,
		NewEndByte: e.NewEndByte,
		StartPosition: sitter.Point{
			Row:    uint(e.StartPosition.Row),
			Column: uint(e.StartPosition.Column),
		},
		OldEndPosition: sitter.Point{
			Row:    uint(e.OldEndPosition.Row),
			Column: uint(e.OldEndPosition.Column),
		},
		NewEndPosition: sitter.Point{
			Row:    uint(e.NewEndPosition.Row),
			Column: uint(e.NewEndPosition.Column),
		},
	}
}

// CollectParseErrors walks the tree and returns all parse error positions.
// Returns nil if the tree has no errors.
func CollectParseErrors(root *Node) []ParseError {
	if !root.HasError() {
		return nil
	}
	var errors []ParseError
	stack := make([]*Node, 0, 64)
	count := root.ChildCount()
	for i := range count {
		stack = append(stack, root.Child(uint(i)))
	}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node.HasError() && node.Type() == "ERROR" {
			errors = append(errors, ParseError{
				Line:    node.StartPosition().Row + 1,
				Column:  node.StartPosition().Column,
				Message: "syntax error",
			})
		}
		childCount := node.ChildCount()
		for i := range childCount {
			stack = append(stack, node.Child(uint(i)))
		}
	}
	return errors
}
