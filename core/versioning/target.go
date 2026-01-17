package versioning

type Target struct {
	NodePath    []string
	NodeType    string
	NodeID      string
	StartOffset int
	EndOffset   int
	StartLine   int
	EndLine     int
	Language    string
}

func NewASTTarget(nodePath []string, nodeType, nodeID, language string) Target {
	return Target{
		NodePath: cloneStrings(nodePath),
		NodeType: nodeType,
		NodeID:   nodeID,
		Language: language,
	}
}

func NewOffsetTarget(startOffset, endOffset int) Target {
	return Target{
		StartOffset: startOffset,
		EndOffset:   endOffset,
	}
}

func NewLineTarget(startLine, endLine int) Target {
	return Target{
		StartLine: startLine,
		EndLine:   endLine,
	}
}

func (t Target) IsAST() bool {
	return len(t.NodePath) > 0
}

func (t Target) IsOffset() bool {
	return !t.IsAST() && (t.StartOffset > 0 || t.EndOffset > 0)
}

func (t Target) IsLine() bool {
	return !t.IsAST() && !t.IsOffset() && (t.StartLine > 0 || t.EndLine > 0)
}

func (t Target) IsEmpty() bool {
	return !t.IsAST() && !t.IsOffset() && !t.IsLine()
}

func (t Target) Clone() Target {
	return Target{
		NodePath:    cloneStrings(t.NodePath),
		NodeType:    t.NodeType,
		NodeID:      t.NodeID,
		StartOffset: t.StartOffset,
		EndOffset:   t.EndOffset,
		StartLine:   t.StartLine,
		EndLine:     t.EndLine,
		Language:    t.Language,
	}
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	result := make([]string, len(s))
	copy(result, s)
	return result
}

func (t Target) Overlaps(other Target) bool {
	return t.overlapsAST(other) || t.overlapsOffset(other) || t.overlapsLine(other)
}

func (t Target) overlapsAST(other Target) bool {
	return t.IsAST() && other.IsAST() && nodePathOverlaps(t.NodePath, other.NodePath)
}

func (t Target) overlapsOffset(other Target) bool {
	return t.IsOffset() && other.IsOffset() && t.StartOffset < other.EndOffset && other.StartOffset < t.EndOffset
}

func (t Target) overlapsLine(other Target) bool {
	return t.IsLine() && other.IsLine() && t.StartLine <= other.EndLine && other.StartLine <= t.EndLine
}

func nodePathOverlaps(a, b []string) bool {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (t Target) Contains(other Target) bool {
	return t.containsAST(other) || t.containsOffset(other) || t.containsLine(other)
}

func (t Target) containsAST(other Target) bool {
	return t.IsAST() && other.IsAST() && isPrefix(t.NodePath, other.NodePath)
}

func (t Target) containsOffset(other Target) bool {
	return t.IsOffset() && other.IsOffset() && t.StartOffset <= other.StartOffset && t.EndOffset >= other.EndOffset
}

func (t Target) containsLine(other Target) bool {
	return t.IsLine() && other.IsLine() && t.StartLine <= other.StartLine && t.EndLine >= other.EndLine
}

func isPrefix(prefix, path []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, p := range prefix {
		if path[i] != p {
			return false
		}
	}
	return true
}

func (t Target) Equal(other Target) bool {
	return t.equalAST(other) || t.equalOffset(other) || t.equalLine(other) || t.equalEmpty(other)
}

func (t Target) equalAST(other Target) bool {
	bothAST := t.IsAST() && other.IsAST()
	return bothAST && t.astFieldsMatch(other)
}

func (t Target) astFieldsMatch(other Target) bool {
	return stringSliceEqual(t.NodePath, other.NodePath) && t.NodeType == other.NodeType && t.NodeID == other.NodeID
}

func (t Target) equalOffset(other Target) bool {
	return t.IsOffset() && other.IsOffset() && t.StartOffset == other.StartOffset && t.EndOffset == other.EndOffset
}

func (t Target) equalLine(other Target) bool {
	return t.IsLine() && other.IsLine() && t.StartLine == other.StartLine && t.EndLine == other.EndLine
}

func (t Target) equalEmpty(other Target) bool {
	return t.IsEmpty() && other.IsEmpty()
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (t Target) WithLines(startLine, endLine int) Target {
	result := t.Clone()
	result.StartLine = startLine
	result.EndLine = endLine
	return result
}

func (t Target) WithOffsets(startOffset, endOffset int) Target {
	result := t.Clone()
	result.StartOffset = startOffset
	result.EndOffset = endOffset
	return result
}

func (t Target) Size() int {
	if t.IsOffset() {
		return t.EndOffset - t.StartOffset
	}
	if t.IsLine() {
		return t.EndLine - t.StartLine + 1
	}
	return 0
}
