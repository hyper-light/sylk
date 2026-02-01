package sylkdir

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/search"
)

func TestSourceCodeChunkerEmpty(t *testing.T) {
	c := NewSourceCodeChunker(NewChunkerConfig(512, ContentClassSourceCode))
	if got := c.Chunk(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestSourceCodeChunkerSingleChunk(t *testing.T) {
	c := NewSourceCodeChunker(NewChunkerConfig(512, ContentClassSourceCode))
	content := []byte("func main() {\n\tprintln(\"hello\")\n}\n")

	bounds := c.Chunk(content)
	if len(bounds) != 1 {
		t.Fatalf("got %d chunks, want 1", len(bounds))
	}
	if bounds[0].ByteEnd != uint32(len(content)) {
		t.Errorf("end: got %d, want %d", bounds[0].ByteEnd, len(content))
	}
}

func TestSourceCodeChunkerBlankLineSplit(t *testing.T) {
	// Two functions separated by a blank line, each > MinChunkBytes.
	fn := strings.Repeat("// comment line\n", 20)
	content := []byte(fn + "\n\n" + fn)

	cfg := NewChunkerConfig(100, ContentClassSourceCode)
	c := NewSourceCodeChunker(cfg)

	bounds := c.Chunk(content)
	if len(bounds) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(bounds))
	}
	if bounds[0].Kind != BoundaryStructural {
		t.Errorf("kind: got %v, want BoundaryStructural", bounds[0].Kind)
	}
}

func TestSourceCodeChunkerWithSymbols(t *testing.T) {
	lines := []string{
		"package main",
		"",
		"func foo() {",
		"    println(\"foo\")",
		"}",
		"",
		"func bar() {",
		"    println(\"bar\")",
		"}",
		"",
	}
	content := []byte(strings.Join(lines, "\n"))

	symbols := []search.SymbolInfo{
		{Name: "foo", Kind: search.SymbolKindFunction, Line: 3},
		{Name: "bar", Kind: search.SymbolKindFunction, Line: 7},
	}

	cfg := ChunkerConfig{MaxChunkBytes: 60, MinChunkBytes: 10, OverlapBytes: 0}
	c := NewSourceCodeChunker(cfg)

	bounds := c.ChunkWithSymbols(content, symbols)
	if len(bounds) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(bounds))
	}
	if bounds[0].ByteStart != 0 {
		t.Errorf("first chunk should start at 0, got %d", bounds[0].ByteStart)
	}
}

func TestSourceCodeChunkerDeterminism(t *testing.T) {
	content := []byte(strings.Repeat("// line\n", 200))
	c := NewSourceCodeChunker(NewChunkerConfig(100, ContentClassSourceCode))

	first := c.Chunk(content)
	second := c.Chunk(content)

	if len(first) != len(second) {
		t.Fatalf("non-deterministic: %d vs %d chunks", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("chunk %d differs", i)
		}
	}
}

func TestSourceCodeChunkerClass(t *testing.T) {
	c := NewSourceCodeChunker(NewChunkerConfig(512, ContentClassSourceCode))
	if c.Class() != ContentClassSourceCode {
		t.Errorf("class: got %v, want SourceCode", c.Class())
	}
}
