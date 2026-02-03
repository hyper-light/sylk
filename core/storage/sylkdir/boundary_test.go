package sylkdir

import (
	"testing"
)

func TestFindBlankLineBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []TaggedBoundary
	}{
		{
			name:    "no blank lines",
			content: "hello\nworld\n",
			want:    nil,
		},
		{
			name:    "single blank line",
			content: "hello\n\nworld\n",
			want:    []TaggedBoundary{{Offset: 7, Strength: 1}},
		},
		{
			name:    "double blank line",
			content: "hello\n\n\nworld\n",
			want:    []TaggedBoundary{{Offset: 8, Strength: 2}},
		},
		{
			name:    "triple blank line",
			content: "hello\n\n\n\nworld\n",
			want:    []TaggedBoundary{{Offset: 9, Strength: 3}},
		},
		{
			name:    "multiple blank line groups",
			content: "a\n\nb\n\n\nc\n",
			want: []TaggedBoundary{
				{Offset: 3, Strength: 1},
				{Offset: 7, Strength: 2},
			},
		},
		{
			name:    "blank lines at end ignored",
			content: "hello\n\n\n",
			want:    nil, // No content follows the blank lines.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findBlankLineBoundaries([]byte(tt.content))
			assertBoundariesEqual(t, tt.want, got)
		})
	}
}

func TestFindHeadingBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []TaggedBoundary
	}{
		{
			name:    "no headings",
			content: "hello\nworld\n",
			want:    nil,
		},
		{
			name:    "markdown h1",
			content: "# Title\nbody\n",
			want:    []TaggedBoundary{{Offset: 0, Strength: 3}},
		},
		{
			name:    "markdown h2 not first line",
			content: "intro\n## Section\nbody\n",
			want:    []TaggedBoundary{{Offset: 6, Strength: 3}},
		},
		{
			name:    "setext heading with equals",
			content: "Title\n===\nbody\n",
			want:    []TaggedBoundary{{Offset: 6, Strength: 3}},
		},
		{
			name:    "setext heading with dashes",
			content: "Title\n---\nbody\n",
			want:    []TaggedBoundary{{Offset: 6, Strength: 3}},
		},
		{
			name:    "short dashes not a heading",
			content: "Title\n--\nbody\n",
			want:    nil, // Only 2 dashes, need 3.
		},
		{
			name:    "multiple headings",
			content: "# Title\ntext\n## Section\nmore\n",
			want: []TaggedBoundary{
				{Offset: 0, Strength: 3},
				{Offset: 13, Strength: 3},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := []byte(tt.content)
			li := buildLineIndex(content)
			got := findHeadingBoundaries(content, li)
			assertBoundariesEqual(t, tt.want, got)
		})
	}
}

func TestSymbolKindStrength(t *testing.T) {
	// ingestion.SymbolKind values: 0=Unknown, 1=Function, 2=Method, 3=Type, 4=Interface, 5=Const, 6=Var
	tests := []struct {
		kind uint8
		want uint32
	}{
		{0, 1}, // Unknown → 1
		{1, 2}, // Function → 2
		{2, 2}, // Method → 2
		{3, 3}, // Type → 3
		{4, 3}, // Interface → 3
		{5, 1}, // Const → 1
		{6, 1}, // Var → 1
	}

	for _, tt := range tests {
		got := SymbolKindStrength(tt.kind)
		if got != tt.want {
			t.Errorf("SymbolKindStrength(%d) = %d, want %d", tt.kind, got, tt.want)
		}
	}
}

func TestSymbolTaggedBoundaries(t *testing.T) {
	content := []byte("line1\nline2\nline3\nline4\nline5\n")
	li := buildLineIndex(content)

	tests := []struct {
		name    string
		symbols []SymbolBound
		want    []TaggedBoundary
	}{
		{
			name:    "empty symbols",
			symbols: nil,
			want:    nil,
		},
		{
			name: "function with start and end",
			symbols: []SymbolBound{
				{StartLine: 2, EndLine: 4, Strength: 2},
			},
			want: []TaggedBoundary{
				{Offset: li.byteAt(2), Strength: 2}, // Function start.
				{Offset: li.byteAt(5), Strength: 1}, // Scope closure at EndLine+1.
			},
		},
		{
			name: "interface with start only",
			symbols: []SymbolBound{
				{StartLine: 3, EndLine: 0, Strength: 3},
			},
			want: []TaggedBoundary{
				{Offset: li.byteAt(3), Strength: 3},
			},
		},
		{
			name: "multiple symbols",
			symbols: []SymbolBound{
				{StartLine: 1, EndLine: 2, Strength: 3},
				{StartLine: 4, EndLine: 5, Strength: 2},
			},
			want: []TaggedBoundary{
				{Offset: li.byteAt(1), Strength: 3},
				{Offset: li.byteAt(3), Strength: 1},
				{Offset: li.byteAt(4), Strength: 2},
				// EndLine 5 + 1 = 6 exceeds lineCount 5, so no closing boundary.
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := symbolTaggedBoundaries(li, tt.symbols)
			assertBoundariesEqual(t, tt.want, got)
		})
	}
}

func TestMergeTaggedBoundaries(t *testing.T) {
	tests := []struct {
		name string
		sets [][]TaggedBoundary
		want []TaggedBoundary
	}{
		{
			name: "empty",
			sets: nil,
			want: nil,
		},
		{
			name: "single set",
			sets: [][]TaggedBoundary{
				{{Offset: 10, Strength: 2}},
			},
			want: []TaggedBoundary{{Offset: 10, Strength: 2}},
		},
		{
			name: "same offset takes max strength",
			sets: [][]TaggedBoundary{
				{{Offset: 10, Strength: 1}},
				{{Offset: 10, Strength: 3}},
			},
			want: []TaggedBoundary{{Offset: 10, Strength: 3}},
		},
		{
			name: "different offsets preserved",
			sets: [][]TaggedBoundary{
				{{Offset: 5, Strength: 1}},
				{{Offset: 15, Strength: 2}},
			},
			want: []TaggedBoundary{
				{Offset: 5, Strength: 1},
				{Offset: 15, Strength: 2},
			},
		},
		{
			name: "three sets merged",
			sets: [][]TaggedBoundary{
				{{Offset: 10, Strength: 1}},
				{{Offset: 10, Strength: 2}, {Offset: 20, Strength: 1}},
				{{Offset: 10, Strength: 3}},
			},
			want: []TaggedBoundary{
				{Offset: 10, Strength: 3},
				{Offset: 20, Strength: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeTaggedBoundaries(tt.sets...)
			assertBoundariesEqual(t, tt.want, got)
		})
	}
}

func TestDetectBoundaries(t *testing.T) {
	content := []byte("# Title\n\nparagraph one\n\n\nparagraph two\n")
	li := buildLineIndex(content)

	got := DetectBoundaries(content, li)

	// Should have: heading at offset 0 (strength 3), blank line at offset 9 (strength 1),
	// double blank line at offset 24 (strength 2).
	if len(got) < 2 {
		t.Fatalf("expected at least 2 boundaries, got %d: %+v", len(got), got)
	}

	// Heading boundary at offset 0.
	foundHeading := false
	for _, b := range got {
		if b.Offset == 0 && b.Strength == 3 {
			foundHeading = true
		}
	}
	if !foundHeading {
		t.Error("missing heading boundary at offset 0 with strength 3")
	}
}

func TestDetectBoundariesWithSymbols(t *testing.T) {
	content := []byte("package main\n\nfunc Foo() {\n}\n\nfunc Bar() {\n}\n")
	li := buildLineIndex(content)

	symbols := []SymbolBound{
		{StartLine: 3, EndLine: 4, Strength: 2}, // func Foo
		{StartLine: 6, EndLine: 7, Strength: 2}, // func Bar
	}

	got := DetectBoundariesWithSymbols(content, li, symbols)

	if len(got) == 0 {
		t.Fatal("expected boundaries, got none")
	}

	// Should contain symbol boundaries for Foo and Bar.
	fooOffset := li.byteAt(3)
	barOffset := li.byteAt(6)

	foundFoo := false
	foundBar := false
	for _, b := range got {
		if b.Offset == fooOffset && b.Strength >= 2 {
			foundFoo = true
		}
		if b.Offset == barOffset && b.Strength >= 2 {
			foundBar = true
		}
	}

	if !foundFoo {
		t.Errorf("missing Foo symbol boundary at offset %d", fooOffset)
	}
	if !foundBar {
		t.Errorf("missing Bar symbol boundary at offset %d", barOffset)
	}
}

func TestDetectCodeBlocks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int    // Expected number of code blocks.
		lang    string // Expected language of first block.
	}{
		{
			name:    "no code blocks",
			content: "hello world\n",
			want:    0,
		},
		{
			name:    "single fenced block with language",
			content: "before\n```python\ndef foo():\n    pass\n```\nafter\n",
			want:    1,
			lang:    "python",
		},
		{
			name:    "fenced block without language",
			content: "text\n```\ncode here\n```\nmore text\n",
			want:    1,
			lang:    "",
		},
		{
			name:    "tilde fence",
			content: "text\n~~~go\nfmt.Println()\n~~~\nmore\n",
			want:    1,
			lang:    "go",
		},
		{
			name:    "multiple code blocks",
			content: "```python\nfoo\n```\ntext\n```rust\nbar\n```\n",
			want:    2,
			lang:    "python",
		},
		{
			name:    "unclosed fence ignored",
			content: "```python\ndef foo():\n",
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := []byte(tt.content)
			li := buildLineIndex(content)
			blocks := DetectCodeBlocks(content, li)

			if len(blocks) != tt.want {
				t.Fatalf("expected %d code blocks, got %d: %+v", tt.want, len(blocks), blocks)
			}
			if tt.want > 0 && blocks[0].Language != tt.lang {
				t.Errorf("expected language %q, got %q", tt.lang, blocks[0].Language)
			}
		})
	}
}

func TestCodeBlockBoundaries(t *testing.T) {
	content := []byte("text\n```go\ncode\n```\nmore\n")
	li := buildLineIndex(content)

	got := codeBlockBoundaries(content, li)

	if len(got) != 2 {
		t.Fatalf("expected 2 boundaries (fence open + fence close), got %d: %+v", len(got), got)
	}

	// Both should have strength 3.
	for i, b := range got {
		if b.Strength != 3 {
			t.Errorf("boundary %d: expected strength 3, got %d", i, b.Strength)
		}
	}
}

func TestSymbolAndBlankLineCoincidence(t *testing.T) {
	// Symbol boundary and blank line at same offset: max strength wins.
	content := []byte("line1\n\nline3\n")
	li := buildLineIndex(content)

	blanks := findBlankLineBoundaries(content)
	// Blank line creates boundary at offset 7 with strength 1.

	symbols := []SymbolBound{
		{StartLine: 3, EndLine: 0, Strength: 3}, // Interface at line 3 = offset 7.
	}
	symBounds := symbolTaggedBoundaries(li, symbols)

	merged := mergeTaggedBoundaries(blanks, symBounds)

	// Should have one entry at offset 7 with strength 3 (max).
	for _, b := range merged {
		if b.Offset == 7 && b.Strength != 3 {
			t.Errorf("expected strength 3 at offset 7, got %d", b.Strength)
		}
	}
}

// assertBoundariesEqual compares two boundary slices.
func assertBoundariesEqual(t *testing.T, want, got []TaggedBoundary) {
	t.Helper()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if len(want) != len(got) {
		t.Errorf("boundary count: want %d, got %d\nwant: %+v\ngot:  %+v", len(want), len(got), want, got)
		return
	}
	for i := range want {
		if want[i].Offset != got[i].Offset || want[i].Strength != got[i].Strength {
			t.Errorf("boundary[%d]: want %+v, got %+v", i, want[i], got[i])
		}
	}
}
