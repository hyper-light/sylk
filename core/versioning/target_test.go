package versioning

import (
	"testing"
)

func TestNewASTTarget(t *testing.T) {
	target := NewASTTarget(
		[]string{"func", "HandleRequest", "body"},
		"FunctionDecl",
		"node-123",
		"go",
	)

	if !target.IsAST() {
		t.Error("expected IsAST true")
	}
	if len(target.NodePath) != 3 {
		t.Errorf("expected 3 path elements, got %d", len(target.NodePath))
	}
	if target.NodeType != "FunctionDecl" {
		t.Errorf("expected FunctionDecl, got %s", target.NodeType)
	}
	if target.NodeID != "node-123" {
		t.Errorf("expected node-123, got %s", target.NodeID)
	}
	if target.Language != "go" {
		t.Errorf("expected go, got %s", target.Language)
	}
}

func TestNewASTTarget_ClonesPath(t *testing.T) {
	path := []string{"func", "HandleRequest"}
	target := NewASTTarget(path, "FunctionDecl", "node-123", "go")

	path[0] = "modified"
	if target.NodePath[0] == "modified" {
		t.Error("path should be cloned")
	}
}

func TestNewOffsetTarget(t *testing.T) {
	target := NewOffsetTarget(100, 200)

	if !target.IsOffset() {
		t.Error("expected IsOffset true")
	}
	if target.StartOffset != 100 {
		t.Errorf("expected 100, got %d", target.StartOffset)
	}
	if target.EndOffset != 200 {
		t.Errorf("expected 200, got %d", target.EndOffset)
	}
}

func TestNewLineTarget(t *testing.T) {
	target := NewLineTarget(10, 20)

	if !target.IsLine() {
		t.Error("expected IsLine true")
	}
	if target.StartLine != 10 {
		t.Errorf("expected 10, got %d", target.StartLine)
	}
	if target.EndLine != 20 {
		t.Errorf("expected 20, got %d", target.EndLine)
	}
}

func TestTarget_IsAST(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		expected bool
	}{
		{
			name:     "with node path",
			target:   Target{NodePath: []string{"func", "Foo"}},
			expected: true,
		},
		{
			name:     "empty node path",
			target:   Target{NodePath: []string{}},
			expected: false,
		},
		{
			name:     "nil node path",
			target:   Target{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.target.IsAST() != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_IsOffset(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		expected bool
	}{
		{
			name:     "with offsets",
			target:   Target{StartOffset: 0, EndOffset: 100},
			expected: true,
		},
		{
			name:     "start offset only",
			target:   Target{StartOffset: 50},
			expected: true,
		},
		{
			name:     "AST target with offsets",
			target:   Target{NodePath: []string{"func"}, StartOffset: 50},
			expected: false,
		},
		{
			name:     "zero offsets",
			target:   Target{StartOffset: 0, EndOffset: 0},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.target.IsOffset() != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_IsLine(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		expected bool
	}{
		{
			name:     "with lines",
			target:   Target{StartLine: 10, EndLine: 20},
			expected: true,
		},
		{
			name:     "start line only",
			target:   Target{StartLine: 10},
			expected: true,
		},
		{
			name:     "AST target with lines",
			target:   Target{NodePath: []string{"func"}, StartLine: 10},
			expected: false,
		},
		{
			name:     "offset target with lines",
			target:   Target{StartOffset: 100, StartLine: 10},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.target.IsLine() != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_IsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		expected bool
	}{
		{
			name:     "empty target",
			target:   Target{},
			expected: true,
		},
		{
			name:     "AST target",
			target:   Target{NodePath: []string{"func"}},
			expected: false,
		},
		{
			name:     "offset target",
			target:   Target{StartOffset: 100},
			expected: false,
		},
		{
			name:     "line target",
			target:   Target{StartLine: 10},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.target.IsEmpty() != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_Clone(t *testing.T) {
	original := Target{
		NodePath:    []string{"func", "Foo"},
		NodeType:    "FunctionDecl",
		NodeID:      "node-123",
		StartOffset: 100,
		EndOffset:   200,
		StartLine:   10,
		EndLine:     20,
		Language:    "go",
	}

	clone := original.Clone()

	t.Run("same values", func(t *testing.T) {
		if clone.NodeType != original.NodeType {
			t.Error("NodeType should match")
		}
		if clone.StartOffset != original.StartOffset {
			t.Error("StartOffset should match")
		}
	})

	t.Run("independent node path", func(t *testing.T) {
		clone.NodePath[0] = "modified"
		if original.NodePath[0] == "modified" {
			t.Error("NodePath should be independent")
		}
	})
}

func TestTarget_Overlaps_AST(t *testing.T) {
	tests := []struct {
		name     string
		a        Target
		b        Target
		expected bool
	}{
		{
			name:     "exact match",
			a:        Target{NodePath: []string{"func", "Foo"}},
			b:        Target{NodePath: []string{"func", "Foo"}},
			expected: true,
		},
		{
			name:     "a is prefix of b",
			a:        Target{NodePath: []string{"func"}},
			b:        Target{NodePath: []string{"func", "Foo", "body"}},
			expected: true,
		},
		{
			name:     "b is prefix of a",
			a:        Target{NodePath: []string{"func", "Foo", "body"}},
			b:        Target{NodePath: []string{"func"}},
			expected: true,
		},
		{
			name:     "no overlap",
			a:        Target{NodePath: []string{"func", "Foo"}},
			b:        Target{NodePath: []string{"func", "Bar"}},
			expected: false,
		},
		{
			name:     "completely different paths",
			a:        Target{NodePath: []string{"import"}},
			b:        Target{NodePath: []string{"func"}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.a.Overlaps(tt.b) != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_Overlaps_Offset(t *testing.T) {
	tests := []struct {
		name     string
		a        Target
		b        Target
		expected bool
	}{
		{
			name:     "overlapping ranges",
			a:        Target{StartOffset: 0, EndOffset: 100},
			b:        Target{StartOffset: 50, EndOffset: 150},
			expected: true,
		},
		{
			name:     "a contains b",
			a:        Target{StartOffset: 0, EndOffset: 100},
			b:        Target{StartOffset: 25, EndOffset: 75},
			expected: true,
		},
		{
			name:     "adjacent no overlap",
			a:        Target{StartOffset: 0, EndOffset: 100},
			b:        Target{StartOffset: 100, EndOffset: 200},
			expected: false,
		},
		{
			name:     "separate ranges",
			a:        Target{StartOffset: 0, EndOffset: 50},
			b:        Target{StartOffset: 100, EndOffset: 150},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.a.Overlaps(tt.b) != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_Overlaps_Line(t *testing.T) {
	tests := []struct {
		name     string
		a        Target
		b        Target
		expected bool
	}{
		{
			name:     "overlapping lines",
			a:        Target{StartLine: 1, EndLine: 10},
			b:        Target{StartLine: 5, EndLine: 15},
			expected: true,
		},
		{
			name:     "adjacent lines",
			a:        Target{StartLine: 1, EndLine: 10},
			b:        Target{StartLine: 10, EndLine: 20},
			expected: true,
		},
		{
			name:     "separate lines",
			a:        Target{StartLine: 1, EndLine: 10},
			b:        Target{StartLine: 20, EndLine: 30},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.a.Overlaps(tt.b) != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_Overlaps_MixedTypes(t *testing.T) {
	ast := Target{NodePath: []string{"func"}}
	offset := Target{StartOffset: 0, EndOffset: 100}

	if ast.Overlaps(offset) {
		t.Error("different target types should not overlap")
	}
}

func TestTarget_Contains(t *testing.T) {
	tests := []struct {
		name     string
		a        Target
		b        Target
		expected bool
	}{
		{
			name:     "AST parent contains child",
			a:        Target{NodePath: []string{"func"}},
			b:        Target{NodePath: []string{"func", "Foo", "body"}},
			expected: true,
		},
		{
			name:     "AST child does not contain parent",
			a:        Target{NodePath: []string{"func", "Foo"}},
			b:        Target{NodePath: []string{"func"}},
			expected: false,
		},
		{
			name:     "offset contains",
			a:        Target{StartOffset: 0, EndOffset: 100},
			b:        Target{StartOffset: 25, EndOffset: 75},
			expected: true,
		},
		{
			name:     "offset does not contain",
			a:        Target{StartOffset: 25, EndOffset: 75},
			b:        Target{StartOffset: 0, EndOffset: 100},
			expected: false,
		},
		{
			name:     "line contains",
			a:        Target{StartLine: 1, EndLine: 100},
			b:        Target{StartLine: 50, EndLine: 75},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.a.Contains(tt.b) != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_Equal(t *testing.T) {
	tests := []struct {
		name     string
		a        Target
		b        Target
		expected bool
	}{
		{
			name: "equal AST targets",
			a: Target{
				NodePath: []string{"func", "Foo"},
				NodeType: "FunctionDecl",
				NodeID:   "node-123",
			},
			b: Target{
				NodePath: []string{"func", "Foo"},
				NodeType: "FunctionDecl",
				NodeID:   "node-123",
			},
			expected: true,
		},
		{
			name: "different node paths",
			a:    Target{NodePath: []string{"func", "Foo"}},
			b:    Target{NodePath: []string{"func", "Bar"}},

			expected: false,
		},
		{
			name:     "equal offset targets",
			a:        Target{StartOffset: 100, EndOffset: 200},
			b:        Target{StartOffset: 100, EndOffset: 200},
			expected: true,
		},
		{
			name:     "different offsets",
			a:        Target{StartOffset: 100, EndOffset: 200},
			b:        Target{StartOffset: 100, EndOffset: 300},
			expected: false,
		},
		{
			name:     "equal line targets",
			a:        Target{StartLine: 10, EndLine: 20},
			b:        Target{StartLine: 10, EndLine: 20},
			expected: true,
		},
		{
			name:     "empty targets",
			a:        Target{},
			b:        Target{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.a.Equal(tt.b) != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}

func TestTarget_WithLines(t *testing.T) {
	original := NewOffsetTarget(100, 200)
	withLines := original.WithLines(10, 20)

	if withLines.StartLine != 10 || withLines.EndLine != 20 {
		t.Error("lines not set correctly")
	}
	if original.StartLine != 0 {
		t.Error("original should be unchanged")
	}
}

func TestTarget_WithOffsets(t *testing.T) {
	original := NewLineTarget(10, 20)
	withOffsets := original.WithOffsets(100, 200)

	if withOffsets.StartOffset != 100 || withOffsets.EndOffset != 200 {
		t.Error("offsets not set correctly")
	}
	if original.StartOffset != 0 {
		t.Error("original should be unchanged")
	}
}

func TestTarget_Size(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		expected int
	}{
		{
			name:     "offset target",
			target:   Target{StartOffset: 100, EndOffset: 200},
			expected: 100,
		},
		{
			name:     "line target",
			target:   Target{StartLine: 10, EndLine: 20},
			expected: 11,
		},
		{
			name:     "AST target",
			target:   Target{NodePath: []string{"func"}},
			expected: 0,
		},
		{
			name:     "empty target",
			target:   Target{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.target.Size() != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, tt.target.Size())
			}
		})
	}
}
