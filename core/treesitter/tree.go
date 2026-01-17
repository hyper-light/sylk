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

func (n *Node) Content() string {
	start := n.StartByte()
	end := n.EndByte()
	if end > uint(len(n.source)) {
		end = uint(len(n.source))
	}
	if start >= end {
		return ""
	}
	return string(n.source[start:end])
}

func (n *Node) String() string {
	return n.inner.ToSexp()
}

type Point struct {
	Row    uint32
	Column uint32
}
