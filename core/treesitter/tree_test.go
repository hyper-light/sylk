package treesitter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathSegmentString(t *testing.T) {
	tests := []struct {
		name     string
		segment  PathSegment
		expected string
	}{
		{
			name:     "with name",
			segment:  PathSegment{Type: "function", Name: "main", Index: 0},
			expected: "function[main]",
		},
		{
			name:     "with index only",
			segment:  PathSegment{Type: "block", Index: 2},
			expected: "block[2]",
		},
		{
			name:     "zero index",
			segment:  PathSegment{Type: "module", Index: 0},
			expected: "module[0]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.segment.String()
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestNodePathString(t *testing.T) {
	t.Run("nil path", func(t *testing.T) {
		var p *NodePath
		assert.Equal(t, "", p.String())
	})

	t.Run("empty segments", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{}}
		assert.Equal(t, "", p.String())
	})

	t.Run("single segment", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{
			{Type: "module", Index: 0},
		}}
		assert.Equal(t, "module[0]", p.String())
	})

	t.Run("multiple segments", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{
			{Type: "module", Index: 0},
			{Type: "function", Name: "main"},
			{Type: "block", Index: 1},
		}}
		assert.Equal(t, "module[0]/function[main]/block[1]", p.String())
	})
}

func TestParseNodePath(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		_, err := ParseNodePath("")
		assert.ErrorIs(t, err, ErrInvalidPath)
	})

	t.Run("single segment with index", func(t *testing.T) {
		p, err := ParseNodePath("module[0]")
		require.NoError(t, err)
		require.Len(t, p.Segments, 1)
		assert.Equal(t, "module", p.Segments[0].Type)
		assert.Equal(t, 0, p.Segments[0].Index)
		assert.Equal(t, "", p.Segments[0].Name)
	})

	t.Run("single segment with name", func(t *testing.T) {
		p, err := ParseNodePath("function[main]")
		require.NoError(t, err)
		require.Len(t, p.Segments, 1)
		assert.Equal(t, "function", p.Segments[0].Type)
		assert.Equal(t, "main", p.Segments[0].Name)
	})

	t.Run("multiple segments", func(t *testing.T) {
		p, err := ParseNodePath("module[0]/function[main]/block[2]")
		require.NoError(t, err)
		require.Len(t, p.Segments, 3)
		assert.Equal(t, "module", p.Segments[0].Type)
		assert.Equal(t, "function", p.Segments[1].Type)
		assert.Equal(t, "main", p.Segments[1].Name)
		assert.Equal(t, "block", p.Segments[2].Type)
		assert.Equal(t, 2, p.Segments[2].Index)
	})

	t.Run("segment without brackets", func(t *testing.T) {
		p, err := ParseNodePath("module")
		require.NoError(t, err)
		require.Len(t, p.Segments, 1)
		assert.Equal(t, "module", p.Segments[0].Type)
	})

	t.Run("invalid bracket - no closing", func(t *testing.T) {
		_, err := ParseNodePath("module[0")
		assert.ErrorIs(t, err, ErrInvalidPath)
	})

	t.Run("invalid bracket - empty", func(t *testing.T) {
		_, err := ParseNodePath("module[]")
		assert.ErrorIs(t, err, ErrInvalidPath)
	})
}

func TestParseNodePathRoundTrip(t *testing.T) {
	original := &NodePath{Segments: []PathSegment{
		{Type: "module", Index: 0},
		{Type: "function", Name: "handleRequest"},
		{Type: "block", Index: 1},
		{Type: "if_statement", Index: 0},
	}}

	str := original.String()
	parsed, err := ParseNodePath(str)
	require.NoError(t, err)

	assert.True(t, NodePathEquals(original, parsed))
}

func TestNodePathEquals(t *testing.T) {
	t.Run("both nil", func(t *testing.T) {
		assert.True(t, NodePathEquals(nil, nil))
	})

	t.Run("one nil", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		assert.False(t, NodePathEquals(p, nil))
		assert.False(t, NodePathEquals(nil, p))
	})

	t.Run("different lengths", func(t *testing.T) {
		a := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		b := &NodePath{Segments: []PathSegment{{Type: "a"}, {Type: "b"}}}
		assert.False(t, NodePathEquals(a, b))
	})

	t.Run("same segments", func(t *testing.T) {
		a := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
			{Type: "func", Name: "main"},
		}}
		b := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
			{Type: "func", Name: "main"},
		}}
		assert.True(t, NodePathEquals(a, b))
	})

	t.Run("different type", func(t *testing.T) {
		a := &NodePath{Segments: []PathSegment{{Type: "a", Index: 0}}}
		b := &NodePath{Segments: []PathSegment{{Type: "b", Index: 0}}}
		assert.False(t, NodePathEquals(a, b))
	})

	t.Run("different name", func(t *testing.T) {
		a := &NodePath{Segments: []PathSegment{{Type: "a", Name: "foo"}}}
		b := &NodePath{Segments: []PathSegment{{Type: "a", Name: "bar"}}}
		assert.False(t, NodePathEquals(a, b))
	})

	t.Run("different index", func(t *testing.T) {
		a := &NodePath{Segments: []PathSegment{{Type: "a", Index: 0}}}
		b := &NodePath{Segments: []PathSegment{{Type: "a", Index: 1}}}
		assert.False(t, NodePathEquals(a, b))
	})
}

func TestNodePathOverlaps(t *testing.T) {
	t.Run("nil paths", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		assert.False(t, NodePathOverlaps(nil, p))
		assert.False(t, NodePathOverlaps(p, nil))
		assert.False(t, NodePathOverlaps(nil, nil))
	})

	t.Run("same path", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
			{Type: "func", Name: "main"},
		}}
		assert.True(t, NodePathOverlaps(p, p))
	})

	t.Run("one is prefix of other", func(t *testing.T) {
		short := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
		}}
		long := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
			{Type: "func", Name: "main"},
		}}
		assert.True(t, NodePathOverlaps(short, long))
		assert.True(t, NodePathOverlaps(long, short))
	})

	t.Run("divergent paths", func(t *testing.T) {
		a := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
			{Type: "func", Name: "foo"},
		}}
		b := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
			{Type: "func", Name: "bar"},
		}}
		assert.False(t, NodePathOverlaps(a, b))
	})

	t.Run("completely different", func(t *testing.T) {
		a := &NodePath{Segments: []PathSegment{{Type: "x"}}}
		b := &NodePath{Segments: []PathSegment{{Type: "y"}}}
		assert.False(t, NodePathOverlaps(a, b))
	})
}

func TestNodePathIsAncestorOf(t *testing.T) {
	t.Run("nil paths", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		assert.False(t, (*NodePath)(nil).IsAncestorOf(p))
		assert.False(t, p.IsAncestorOf(nil))
	})

	t.Run("same path not ancestor", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		assert.False(t, p.IsAncestorOf(p))
	})

	t.Run("longer path not ancestor", func(t *testing.T) {
		short := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		long := &NodePath{Segments: []PathSegment{{Type: "a"}, {Type: "b"}}}
		assert.False(t, long.IsAncestorOf(short))
	})

	t.Run("true ancestor", func(t *testing.T) {
		parent := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
		}}
		child := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
			{Type: "func", Name: "main"},
			{Type: "block", Index: 0},
		}}
		assert.True(t, parent.IsAncestorOf(child))
	})

	t.Run("not ancestor different prefix", func(t *testing.T) {
		a := &NodePath{Segments: []PathSegment{{Type: "x"}}}
		b := &NodePath{Segments: []PathSegment{{Type: "y"}, {Type: "z"}}}
		assert.False(t, a.IsAncestorOf(b))
	})
}

func TestNodePathIsDescendantOf(t *testing.T) {
	t.Run("nil other", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		assert.False(t, p.IsDescendantOf(nil))
	})

	t.Run("true descendant", func(t *testing.T) {
		parent := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
		}}
		child := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
			{Type: "func", Name: "main"},
		}}
		assert.True(t, child.IsDescendantOf(parent))
	})

	t.Run("not descendant", func(t *testing.T) {
		parent := &NodePath{Segments: []PathSegment{
			{Type: "mod", Index: 0},
		}}
		assert.False(t, parent.IsDescendantOf(parent))
	})
}

func TestNodePathParent(t *testing.T) {
	t.Run("nil path", func(t *testing.T) {
		var p *NodePath
		assert.Nil(t, p.Parent())
	})

	t.Run("single segment", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		assert.Nil(t, p.Parent())
	})

	t.Run("multiple segments", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{
			{Type: "a", Index: 0},
			{Type: "b", Index: 1},
			{Type: "c", Index: 2},
		}}
		parent := p.Parent()
		require.NotNil(t, parent)
		require.Len(t, parent.Segments, 2)
		assert.Equal(t, "a", parent.Segments[0].Type)
		assert.Equal(t, "b", parent.Segments[1].Type)
	})
}

func TestNodePathLastSegment(t *testing.T) {
	t.Run("nil path", func(t *testing.T) {
		var p *NodePath
		assert.Nil(t, p.LastSegment())
	})

	t.Run("empty segments", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{}}
		assert.Nil(t, p.LastSegment())
	})

	t.Run("single segment", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{
			{Type: "func", Name: "main"},
		}}
		last := p.LastSegment()
		require.NotNil(t, last)
		assert.Equal(t, "func", last.Type)
		assert.Equal(t, "main", last.Name)
	})

	t.Run("multiple segments", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{
			{Type: "a"},
			{Type: "b"},
			{Type: "c", Name: "last"},
		}}
		last := p.LastSegment()
		require.NotNil(t, last)
		assert.Equal(t, "c", last.Type)
		assert.Equal(t, "last", last.Name)
	})
}

func TestNodePathDepth(t *testing.T) {
	t.Run("nil path", func(t *testing.T) {
		var p *NodePath
		assert.Equal(t, 0, p.Depth())
	})

	t.Run("empty segments", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{}}
		assert.Equal(t, 0, p.Depth())
	})

	t.Run("multiple segments", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{
			{Type: "a"},
			{Type: "b"},
			{Type: "c"},
		}}
		assert.Equal(t, 3, p.Depth())
	})
}

func TestPathErrors(t *testing.T) {
	t.Run("ErrInvalidPath", func(t *testing.T) {
		assert.Equal(t, "invalid path", ErrInvalidPath.Error())
	})

	t.Run("ErrPathNotFound", func(t *testing.T) {
		assert.Equal(t, "path not found", ErrPathNotFound.Error())
	})
}

func TestComputeNodePathNilTree(t *testing.T) {
	result := ComputeNodePath(nil, 0)
	assert.Nil(t, result)
}

func TestResolveNodePathValidation(t *testing.T) {
	t.Run("nil tree", func(t *testing.T) {
		p := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		_, err := ResolveNodePath(nil, p)
		assert.ErrorIs(t, err, ErrInvalidPath)
	})

	t.Run("nil path", func(t *testing.T) {
		tree := &Tree{}
		_, err := ResolveNodePath(tree, nil)
		assert.ErrorIs(t, err, ErrInvalidPath)
	})

	t.Run("empty segments", func(t *testing.T) {
		tree := &Tree{}
		p := &NodePath{Segments: []PathSegment{}}
		_, err := ResolveNodePath(tree, p)
		assert.ErrorIs(t, err, ErrInvalidPath)
	})
}

func TestSegmentEquals(t *testing.T) {
	t.Run("equal segments", func(t *testing.T) {
		a := PathSegment{Type: "func", Name: "main", Index: 0}
		b := PathSegment{Type: "func", Name: "main", Index: 0}
		assert.True(t, segmentEquals(a, b))
	})

	t.Run("different type", func(t *testing.T) {
		a := PathSegment{Type: "func", Name: "main"}
		b := PathSegment{Type: "method", Name: "main"}
		assert.False(t, segmentEquals(a, b))
	})

	t.Run("different name", func(t *testing.T) {
		a := PathSegment{Type: "func", Name: "foo"}
		b := PathSegment{Type: "func", Name: "bar"}
		assert.False(t, segmentEquals(a, b))
	})

	t.Run("different index", func(t *testing.T) {
		a := PathSegment{Type: "block", Index: 0}
		b := PathSegment{Type: "block", Index: 1}
		assert.False(t, segmentEquals(a, b))
	})
}

func TestParseIndex(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"0", 0},
		{"1", 1},
		{"10", 10},
		{"123", 123},
		{"99999", 99999},
		{"5abc", 5},
		{"", 0},
		{"abc", 0},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := parseIndex(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestMinInt(t *testing.T) {
	assert.Equal(t, 5, minInt(5, 10))
	assert.Equal(t, 5, minInt(10, 5))
	assert.Equal(t, 0, minInt(0, 5))
	assert.Equal(t, -1, minInt(-1, 5))
	assert.Equal(t, 5, minInt(5, 5))
}

func TestHelperFunctions(t *testing.T) {
	t.Run("isValidTreePath", func(t *testing.T) {
		tree := &Tree{}
		validPath := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		emptyPath := &NodePath{Segments: []PathSegment{}}

		assert.True(t, isValidTreePath(tree, validPath))
		assert.False(t, isValidTreePath(nil, validPath))
		assert.False(t, isValidTreePath(tree, nil))
		assert.False(t, isValidTreePath(tree, emptyPath))
	})

	t.Run("isValidAncestorCandidate", func(t *testing.T) {
		short := &NodePath{Segments: []PathSegment{{Type: "a"}}}
		long := &NodePath{Segments: []PathSegment{{Type: "a"}, {Type: "b"}}}

		assert.True(t, isValidAncestorCandidate(short, long))
		assert.False(t, isValidAncestorCandidate(nil, long))
		assert.False(t, isValidAncestorCandidate(short, nil))
		assert.False(t, isValidAncestorCandidate(long, short))
		assert.False(t, isValidAncestorCandidate(short, short))
	})

	t.Run("segmentsEqual", func(t *testing.T) {
		a := []PathSegment{{Type: "x", Index: 1}}
		b := []PathSegment{{Type: "x", Index: 1}}
		c := []PathSegment{{Type: "y", Index: 1}}
		d := []PathSegment{{Type: "x", Index: 1}, {Type: "z"}}

		assert.True(t, segmentsEqual(a, b))
		assert.False(t, segmentsEqual(a, c))
		assert.False(t, segmentsEqual(a, d))
	})

	t.Run("segmentsPrefixMatch", func(t *testing.T) {
		a := []PathSegment{{Type: "x"}}
		b := []PathSegment{{Type: "x"}, {Type: "y"}}
		c := []PathSegment{{Type: "z"}}

		assert.True(t, segmentsPrefixMatch(a, b))
		assert.True(t, segmentsPrefixMatch(b, a))
		assert.False(t, segmentsPrefixMatch(a, c))
	})

	t.Run("isPrefixOf", func(t *testing.T) {
		prefix := []PathSegment{{Type: "a"}, {Type: "b"}}
		full := []PathSegment{{Type: "a"}, {Type: "b"}, {Type: "c"}}
		wrong := []PathSegment{{Type: "x"}, {Type: "b"}, {Type: "c"}}

		assert.True(t, isPrefixOf(prefix, full))
		assert.False(t, isPrefixOf(prefix, wrong))
	})
}

func TestTryFieldName(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	node := &Node{raw: TSNode{}, tree: nil, source: []byte{}}

	result := tryFieldName(node, "name")
	assert.Nil(t, result)
}

func TestUpdateTypeIndex(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	t.Run("different type", func(t *testing.T) {
		child := &Node{raw: TSNode{}}
		node := &Node{raw: TSNode{}}
		result := updateTypeIndex(child, node, "different", 5)
		assert.Equal(t, 5, result)
	})
}

func BenchmarkNodePathString(b *testing.B) {
	p := &NodePath{Segments: []PathSegment{
		{Type: "module", Index: 0},
		{Type: "function", Name: "handleRequest"},
		{Type: "block", Index: 1},
		{Type: "if_statement", Index: 0},
		{Type: "block", Index: 0},
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.String()
	}
}

func BenchmarkParseNodePath(b *testing.B) {
	pathStr := "module[0]/function[handleRequest]/block[1]/if_statement[0]/block[0]"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseNodePath(pathStr)
	}
}

func BenchmarkNodePathEquals(b *testing.B) {
	a := &NodePath{Segments: []PathSegment{
		{Type: "module", Index: 0},
		{Type: "function", Name: "main"},
		{Type: "block", Index: 1},
	}}
	c := &NodePath{Segments: []PathSegment{
		{Type: "module", Index: 0},
		{Type: "function", Name: "main"},
		{Type: "block", Index: 1},
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NodePathEquals(a, c)
	}
}

func BenchmarkNodePathOverlaps(b *testing.B) {
	short := &NodePath{Segments: []PathSegment{
		{Type: "module", Index: 0},
	}}
	long := &NodePath{Segments: []PathSegment{
		{Type: "module", Index: 0},
		{Type: "function", Name: "main"},
		{Type: "block", Index: 1},
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NodePathOverlaps(short, long)
	}
}
