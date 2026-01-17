package treesitter

import (
	"strings"
)

type NodePath struct {
	Segments []PathSegment
}

type PathSegment struct {
	Type  string
	Name  string
	Index int
}

func ComputeNodePath(tree *Tree, offset uint32) *NodePath {
	if tree == nil {
		return nil
	}
	node := tree.RootNode()
	return computePathFromNode(node, offset)
}

func computePathFromNode(node *Node, offset uint32) *NodePath {
	path := &NodePath{Segments: make([]PathSegment, 0, 8)}
	current := node

	for current != nil && !current.IsNull() {
		path.Segments = append(path.Segments, createSegment(current))
		current = findChildContainingOffset(current, offset)
	}

	return path
}

func createSegment(node *Node) PathSegment {
	return PathSegment{
		Type:  node.Type(),
		Name:  extractNodeName(node),
		Index: computeChildIndex(node),
	}
}

func findChildContainingOffset(parent *Node, offset uint32) *Node {
	count := parent.NamedChildCount()
	for i := uint32(0); i < count; i++ {
		child := parent.NamedChild(i)
		if containsOffset(child, offset) {
			return child
		}
	}
	return nil
}

func containsOffset(node *Node, offset uint32) bool {
	return node.StartByte() <= offset && offset < node.EndByte()
}

func computeChildIndex(node *Node) int {
	parent := node.Parent()
	if parent == nil {
		return 0
	}

	count := parent.NamedChildCount()
	nodeType := node.Type()
	typeIndex := 0

	for i := uint32(0); i < count; i++ {
		child := parent.NamedChild(i)
		if child.Type() == nodeType {
			if isSameNode(child, node) {
				return typeIndex
			}
			typeIndex++
		}
	}
	return 0
}

func isSameNode(a, b *Node) bool {
	return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte()
}

func extractNodeName(node *Node) string {
	nameNode := findNameNode(node)
	if nameNode == nil {
		return ""
	}
	return nameNode.Content()
}

func findNameNode(node *Node) *Node {
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil && !nameNode.IsNull() {
		return nameNode
	}

	nameNode = node.ChildByFieldName("declarator")
	if nameNode != nil && !nameNode.IsNull() {
		return findIdentifier(nameNode)
	}

	return nil
}

func findIdentifier(node *Node) *Node {
	if node.Type() == "identifier" {
		return node
	}

	count := node.NamedChildCount()
	for i := uint32(0); i < count; i++ {
		child := node.NamedChild(i)
		if child.Type() == "identifier" {
			return child
		}
	}
	return nil
}

func ResolveNodePath(tree *Tree, path *NodePath) (*Node, error) {
	if tree == nil || path == nil || len(path.Segments) == 0 {
		return nil, ErrInvalidPath
	}

	node := tree.RootNode()
	for i := 1; i < len(path.Segments); i++ {
		node = resolveSegment(node, path.Segments[i])
		if node == nil || node.IsNull() {
			return nil, ErrPathNotFound
		}
	}

	return node, nil
}

func resolveSegment(parent *Node, seg PathSegment) *Node {
	if seg.Name != "" {
		return findByName(parent, seg.Type, seg.Name)
	}
	return findByIndex(parent, seg.Type, seg.Index)
}

func findByName(parent *Node, nodeType, name string) *Node {
	count := parent.NamedChildCount()
	for i := uint32(0); i < count; i++ {
		child := parent.NamedChild(i)
		if child.Type() != nodeType {
			continue
		}
		if extractNodeName(child) == name {
			return child
		}
	}
	return nil
}

func findByIndex(parent *Node, nodeType string, index int) *Node {
	count := parent.NamedChildCount()
	typeIndex := 0

	for i := uint32(0); i < count; i++ {
		child := parent.NamedChild(i)
		if child.Type() != nodeType {
			continue
		}
		if typeIndex == index {
			return child
		}
		typeIndex++
	}
	return nil
}

func (p *NodePath) String() string {
	if p == nil || len(p.Segments) == 0 {
		return ""
	}

	var parts []string
	for _, seg := range p.Segments {
		parts = append(parts, seg.String())
	}
	return strings.Join(parts, "/")
}

func (s PathSegment) String() string {
	if s.Name != "" {
		return s.Type + "[" + s.Name + "]"
	}
	return s.Type + "[" + uintToString(uint32(s.Index)) + "]"
}

func ParseNodePath(pathStr string) (*NodePath, error) {
	if pathStr == "" {
		return nil, ErrInvalidPath
	}

	parts := strings.Split(pathStr, "/")
	segments := make([]PathSegment, 0, len(parts))

	for _, part := range parts {
		seg, err := parseSegment(part)
		if err != nil {
			return nil, err
		}
		segments = append(segments, seg)
	}

	return &NodePath{Segments: segments}, nil
}

func parseSegment(s string) (PathSegment, error) {
	bracketStart := strings.Index(s, "[")
	if bracketStart == -1 {
		return PathSegment{Type: s}, nil
	}

	bracketEnd := strings.Index(s, "]")
	if bracketEnd == -1 || bracketEnd <= bracketStart+1 {
		return PathSegment{}, ErrInvalidPath
	}

	nodeType := s[:bracketStart]
	indexOrName := s[bracketStart+1 : bracketEnd]

	return createSegmentFromParts(nodeType, indexOrName)
}

func createSegmentFromParts(nodeType, indexOrName string) (PathSegment, error) {
	if len(indexOrName) == 0 {
		return PathSegment{}, ErrInvalidPath
	}

	if indexOrName[0] >= '0' && indexOrName[0] <= '9' {
		index := parseIndex(indexOrName)
		return PathSegment{Type: nodeType, Index: index}, nil
	}

	return PathSegment{Type: nodeType, Name: indexOrName}, nil
}

func parseIndex(s string) int {
	var result int
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		result = result*10 + int(c-'0')
	}
	return result
}

func NodePathEquals(a, b *NodePath) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a.Segments) != len(b.Segments) {
		return false
	}

	for i := range a.Segments {
		if !segmentEquals(a.Segments[i], b.Segments[i]) {
			return false
		}
	}
	return true
}

func segmentEquals(a, b PathSegment) bool {
	return a.Type == b.Type && a.Name == b.Name && a.Index == b.Index
}

func NodePathOverlaps(a, b *NodePath) bool {
	if a == nil || b == nil {
		return false
	}

	minLen := len(a.Segments)
	if len(b.Segments) < minLen {
		minLen = len(b.Segments)
	}

	for i := 0; i < minLen; i++ {
		if !segmentEquals(a.Segments[i], b.Segments[i]) {
			return false
		}
	}
	return true
}

func (p *NodePath) IsAncestorOf(other *NodePath) bool {
	if p == nil || other == nil {
		return false
	}
	if len(p.Segments) >= len(other.Segments) {
		return false
	}

	for i := range p.Segments {
		if !segmentEquals(p.Segments[i], other.Segments[i]) {
			return false
		}
	}
	return true
}

func (p *NodePath) IsDescendantOf(other *NodePath) bool {
	if other == nil {
		return false
	}
	return other.IsAncestorOf(p)
}

func (p *NodePath) Parent() *NodePath {
	if p == nil || len(p.Segments) <= 1 {
		return nil
	}
	return &NodePath{Segments: p.Segments[:len(p.Segments)-1]}
}

func (p *NodePath) LastSegment() *PathSegment {
	if p == nil || len(p.Segments) == 0 {
		return nil
	}
	return &p.Segments[len(p.Segments)-1]
}

func (p *NodePath) Depth() int {
	if p == nil {
		return 0
	}
	return len(p.Segments)
}

var (
	ErrInvalidPath  = &pathError{msg: "invalid path"}
	ErrPathNotFound = &pathError{msg: "path not found"}
)

type pathError struct {
	msg string
}

func (e *pathError) Error() string {
	return e.msg
}
