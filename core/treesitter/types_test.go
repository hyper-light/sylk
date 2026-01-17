package treesitter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPointConversion(t *testing.T) {
	t.Run("point to ts point", func(t *testing.T) {
		p := Point{Row: 10, Column: 20}
		ts := PointToTSPoint(p)
		assert.Equal(t, uint32(10), ts.Row)
		assert.Equal(t, uint32(20), ts.Column)
	})

	t.Run("ts point to point", func(t *testing.T) {
		ts := TSPoint{Row: 5, Column: 15}
		p := TSPointToPoint(ts)
		assert.Equal(t, uint32(5), p.Row)
		assert.Equal(t, uint32(15), p.Column)
	})
}

func TestUintToString(t *testing.T) {
	tests := []struct {
		input    uint32
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{123, "123"},
		{999, "999"},
		{1000, "1000"},
		{4294967295, "4294967295"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			result := uintToString(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestQueryError(t *testing.T) {
	err := &QueryError{
		Offset:  42,
		Type:    1,
		Pattern: "(test)",
	}

	errStr := err.Error()
	assert.Contains(t, errStr, "42")
	assert.Contains(t, errStr, "query error")
}

func TestInputEditZeroValue(t *testing.T) {
	var edit InputEdit
	assert.Equal(t, uint32(0), edit.StartByte)
	assert.Equal(t, uint32(0), edit.OldEndByte)
	assert.Equal(t, uint32(0), edit.NewEndByte)
	assert.Equal(t, uint32(0), edit.StartPoint.Row)
	assert.Equal(t, uint32(0), edit.StartPoint.Column)
}

func TestRangeZeroValue(t *testing.T) {
	var r Range
	assert.Equal(t, uint32(0), r.StartByte)
	assert.Equal(t, uint32(0), r.EndByte)
	assert.Equal(t, uint32(0), r.StartPoint.Row)
	assert.Equal(t, uint32(0), r.EndPoint.Row)
}

func TestNodeInfoZeroValue(t *testing.T) {
	var info NodeInfo
	assert.Equal(t, "", info.Type)
	assert.Equal(t, uint32(0), info.StartLine)
	assert.Equal(t, uint32(0), info.EndLine)
	assert.Nil(t, info.Children)
}

func TestFunctionInfoZeroValue(t *testing.T) {
	var info FunctionInfo
	assert.Equal(t, "", info.Name)
	assert.Equal(t, uint32(0), info.StartLine)
	assert.Equal(t, uint32(0), info.EndLine)
	assert.False(t, info.IsMethod)
}

func TestTypeInfoZeroValue(t *testing.T) {
	var info TypeInfo
	assert.Equal(t, "", info.Name)
	assert.Equal(t, "", info.Kind)
	assert.Nil(t, info.Fields)
}

func TestImportInfoZeroValue(t *testing.T) {
	var info ImportInfo
	assert.Equal(t, "", info.Path)
	assert.Equal(t, "", info.Alias)
	assert.Equal(t, uint32(0), info.Line)
}

func TestParseErrorZeroValue(t *testing.T) {
	var err ParseError
	assert.Equal(t, uint32(0), err.Line)
	assert.Equal(t, uint32(0), err.Column)
	assert.Equal(t, "", err.Message)
}

func TestParseResultZeroValue(t *testing.T) {
	var result ParseResult
	assert.Equal(t, "", result.Language)
	assert.Equal(t, "", result.FilePath)
	assert.Nil(t, result.RootNode)
	assert.Nil(t, result.Functions)
	assert.Nil(t, result.Types)
	assert.Nil(t, result.Imports)
	assert.Nil(t, result.Errors)
}

func TestQueryMatchZeroValue(t *testing.T) {
	var match QueryMatch
	assert.Equal(t, uint16(0), match.PatternIndex)
	assert.Nil(t, match.Captures)
}

func TestQueryCaptureZeroValue(t *testing.T) {
	var cap QueryCapture
	assert.Equal(t, "", cap.Name)
	assert.Nil(t, cap.Node)
	assert.Equal(t, "", cap.Content)
}

func TestNewParser(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	parser := NewParser()
	require.NotNil(t, parser)
	assert.NotEqual(t, TSParser(0), parser.ptr)

	parser.Close()
	assert.Equal(t, TSParser(0), parser.ptr)
}

func TestParserCloseIdempotent(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	parser := NewParser()
	require.NotNil(t, parser)

	parser.Close()
	parser.Close()

	assert.Equal(t, TSParser(0), parser.ptr)
}

func TestParserReset(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	parser := NewParser()
	require.NotNil(t, parser)
	defer parser.Close()

	parser.Reset()
}

func TestLanguageStruct(t *testing.T) {
	lang := &Language{
		name:    "go",
		libPath: "/path/to/lib",
	}

	assert.Equal(t, "go", lang.Name())
}

func TestTreeCloseIdempotent(t *testing.T) {
	tree := &Tree{ptr: 0}
	tree.Close()
	tree.Close()
}

func TestNodeNullChecks(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	node := &Node{
		raw:    TSNode{},
		tree:   nil,
		source: []byte{},
	}

	assert.True(t, node.IsNull())
}

func TestNodeContent(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	source := []byte("hello world")
	node := &Node{
		raw: TSNode{
			Context: [4]uint32{0, 5, 0, 0},
		},
		tree:   nil,
		source: source,
	}

	t.Run("with NodeStartByte/EndByte returning 0", func(t *testing.T) {
		content := node.Content()
		assert.Equal(t, "", content)
	})
}

func TestNodeContentBoundsCheck(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	source := []byte("hello")
	node := &Node{
		raw:    TSNode{},
		tree:   nil,
		source: source,
	}

	content := node.Content()
	assert.Equal(t, "", content)
}

func TestQueryCursorCloseIdempotent(t *testing.T) {
	cursor := &QueryCursor{ptr: 0}
	cursor.Close()
	cursor.Close()
}

func TestQueryCloseIdempotent(t *testing.T) {
	query := &Query{ptr: 0}
	query.Close()
	query.Close()
}

func TestConvertMatchEmptyCaptures(t *testing.T) {
	raw := &TSQueryMatch{
		ID:           1,
		PatternIndex: 2,
		CaptureCount: 0,
		Captures:     0,
	}

	query := &Query{ptr: 0}
	node := &Node{raw: TSNode{}}

	match := convertMatch(raw, query, node)
	assert.Equal(t, uint16(2), match.PatternIndex)
	assert.Empty(t, match.Captures)
}

func TestNodeToInfo(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	node := &Node{
		raw:    TSNode{},
		tree:   nil,
		source: []byte("test"),
	}

	info := node.ToInfo()
	assert.NotNil(t, info)
	assert.Equal(t, uint32(1), info.StartLine)
	assert.Equal(t, uint32(1), info.EndLine)
}

func BenchmarkUintToString(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = uintToString(uint32(i % 10000))
	}
}

func BenchmarkPointConversion(b *testing.B) {
	p := Point{Row: 100, Column: 50}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := PointToTSPoint(p)
		_ = TSPointToPoint(ts)
	}
}
