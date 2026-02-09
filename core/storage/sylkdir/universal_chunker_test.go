package sylkdir

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

func TestUniversalChunkerSmallContent(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	ctx := context.Background()

	content := []byte("small text")
	cr := chunker.Chunk(ctx, content)
	got := cr.Boundaries

	if len(got) != 1 {
		t.Fatalf("expected 1 chunk for small content, got %d", len(got))
	}
	if got[0].ByteStart != 0 || got[0].ByteEnd != uint32(len(content)) {
		t.Errorf("chunk: want [0, %d), got [%d, %d)", len(content), got[0].ByteStart, got[0].ByteEnd)
	}
}

func TestUniversalChunkerEmpty(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, 1000)
	ctx := context.Background()

	got := chunker.Chunk(ctx, nil).Boundaries
	if len(got) != 0 {
		t.Errorf("expected 0 chunks for nil content, got %d", len(got))
	}

	got = chunker.Chunk(ctx, []byte{}).Boundaries
	if len(got) != 0 {
		t.Errorf("expected 0 chunks for empty content, got %d", len(got))
	}
}

func TestUniversalChunkerWithSymbols(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	ctx := context.Background()

	content := []byte("package main\n\nfunc Foo() {\n\treturn\n}\n\nfunc Bar() {\n\treturn\n}\n")
	symbols := []SymbolBound{
		{StartLine: 3, EndLine: 5, Strength: 2},
		{StartLine: 7, EndLine: 9, Strength: 2},
	}

	got := chunker.ChunkWithSymbols(ctx, content, symbols).Boundaries

	if len(got) == 0 {
		t.Fatal("expected at least 1 chunk")
	}

	// Verify all chunks cover the content without gaps.
	if got[0].ByteStart != 0 {
		t.Errorf("first chunk should start at 0, got %d", got[0].ByteStart)
	}
	lastEnd := got[len(got)-1].ByteEnd
	if lastEnd != uint32(len(content)) {
		t.Errorf("last chunk should end at %d, got %d", len(content), lastEnd)
	}

	// All chunks should have BoundaryStructural kind.
	for i, b := range got {
		if b.Kind != BoundaryStructural {
			t.Errorf("chunk %d: expected BoundaryStructural, got %d", i, b.Kind)
		}
	}
}

func TestUniversalChunkerWithHeadings(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	ctx := context.Background()

	content := []byte("# Introduction\n\nFirst paragraph of intro.\n\n# Methods\n\nDescription of methods.\n")

	got := chunker.Chunk(ctx, content).Boundaries

	if len(got) == 0 {
		t.Fatal("expected at least 1 chunk for markdown with headings")
	}
}

func TestUniversalChunkerWithBlankLinesOnly(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	ctx := context.Background()

	content := []byte("paragraph one\n\nparagraph two\n\nparagraph three\n")

	got := chunker.Chunk(ctx, content).Boundaries

	if len(got) == 0 {
		t.Fatal("expected at least 1 chunk")
	}
}

type mockCodeBlockParser struct {
	symbols []SymbolBound
}

func (m *mockCodeBlockParser) ParseBlock(_ context.Context, _ string, _ []byte) []SymbolBound {
	return m.symbols
}

func TestUniversalChunkerWithCodeBlockParser(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	chunker.SetCodeBlockParser(&mockCodeBlockParser{
		symbols: []SymbolBound{
			{StartLine: 1, EndLine: 3, Strength: 2},
		},
	})
	ctx := context.Background()

	content := []byte("Some text\n\n```python\ndef foo():\n    pass\n```\n\nMore text\n")

	got := chunker.Chunk(ctx, content).Boundaries

	if len(got) == 0 {
		t.Fatal("expected at least 1 chunk for mixed content")
	}
}

func TestEnrichedChunkText(t *testing.T) {
	content := []byte("func Foo() {\n\treturn\n}\n")
	boundary := ChunkBoundary{ByteStart: 0, ByteEnd: uint32(len(content))}

	// Without context: raw content.
	got := EnrichedChunkText(content, boundary, nil)
	if got != string(content) {
		t.Errorf("without context: expected raw content, got %q", got)
	}

	// With context: enriched.
	chunkCtx := &ChunkContext{
		FilePath: "main.go",
		Language: "go",
		ContainingSyms: []SymbolMetadata{
			{Name: "Foo", Kind: "function", Signature: "func Foo()"},
		},
	}
	got = EnrichedChunkText(content, boundary, chunkCtx)

	if !strContains(got, "# path: main.go") {
		t.Error("enriched text missing path")
	}
	if !strContains(got, "# language: go") {
		t.Error("enriched text missing language")
	}
	if !strContains(got, "# function: Foo func Foo()") {
		t.Error("enriched text missing symbol metadata")
	}
	if !strContains(got, "func Foo() {") {
		t.Error("enriched text missing actual content")
	}
}

func TestSearchKindToStrength(t *testing.T) {
	tests := []struct {
		kind string
		want uint32
	}{
		{"interface", 3},
		{"type", 3},
		{"function", 2},
		{"method", 2},
		{"variable", 1},
		{"const", 1},
		{"unknown", 1},
	}

	for _, tt := range tests {
		got := SearchKindToStrength(tt.kind)
		if got != tt.want {
			t.Errorf("SearchKindToStrength(%q) = %d, want %d", tt.kind, got, tt.want)
		}
	}
}

func TestFormatSymbolKind(t *testing.T) {
	tests := []struct {
		kind uint8
		want string
	}{
		{1, "function"},
		{2, "method"},
		{3, "type"},
		{4, "interface"},
		{5, "const"},
		{6, "var"},
		{0, "symbol(0)"},
	}

	for _, tt := range tests {
		got := FormatSymbolKind(tt.kind)
		if got != tt.want {
			t.Errorf("FormatSymbolKind(%d) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestChunkerPrepare(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	ctx := context.Background()

	content := []byte("package main\n\nfunc Foo() {\n\treturn\n}\n\nfunc Bar() {\n\treturn\n}\n")

	prep := chunker.Prepare(ctx, content)

	if len(prep.Segments) == 0 {
		t.Fatal("Prepare should produce segments")
	}

	// Segments should cover the content.
	if prep.Segments[0].Start != 0 {
		t.Errorf("first segment should start at 0, got %d", prep.Segments[0].Start)
	}
	last := prep.Segments[len(prep.Segments)-1]
	if last.End != uint32(len(content)) {
		t.Errorf("last segment should end at %d, got %d", len(content), last.End)
	}
}

func TestChunkerPrepareWithSymbols(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	ctx := context.Background()

	content := []byte("package main\n\nfunc Foo() {\n\treturn\n}\n\nfunc Bar() {\n\treturn\n}\n")
	symbols := []SymbolBound{
		{StartLine: 3, EndLine: 5, Strength: 2},
		{StartLine: 7, EndLine: 9, Strength: 2},
	}

	prep := chunker.PrepareWithSymbols(ctx, content, symbols)

	if len(prep.Segments) == 0 {
		t.Fatal("PrepareWithSymbols should produce segments")
	}
}

func TestChunkerPrepareEmpty(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, 1000)
	ctx := context.Background()

	prep := chunker.Prepare(ctx, nil)
	if len(prep.Segments) != 0 {
		t.Errorf("Prepare(nil) should return 0 segments, got %d", len(prep.Segments))
	}

	prep = chunker.Prepare(ctx, []byte{})
	if len(prep.Segments) != 0 {
		t.Errorf("Prepare([]) should return 0 segments, got %d", len(prep.Segments))
	}
}

func TestChunkerMergeFromEmbeddings(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	ctx := context.Background()

	content := []byte("package main\n\nfunc Foo() {\n\treturn\n}\n\nfunc Bar() {\n\treturn\n}\n")

	// Prepare segments.
	prep := chunker.Prepare(ctx, content)
	if len(prep.Segments) == 0 {
		t.Fatal("no segments from Prepare")
	}

	// Embed segment texts.
	texts := make([]string, len(prep.Segments))
	for i, seg := range prep.Segments {
		texts[i] = string(content[seg.Start:seg.End])
	}
	embs, err := chunker.SimilarityEmbedder().EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	// Merge with pre-computed embeddings.
	cr := chunker.MergeFromEmbeddings(ctx, content, prep, embs)

	if len(cr.Boundaries) == 0 {
		t.Fatal("MergeFromEmbeddings should produce chunks")
	}
	if cr.Boundaries[0].ByteStart != 0 {
		t.Errorf("first chunk should start at 0, got %d", cr.Boundaries[0].ByteStart)
	}
	lastEnd := cr.Boundaries[len(cr.Boundaries)-1].ByteEnd
	if lastEnd != uint32(len(content)) {
		t.Errorf("last chunk should end at %d, got %d", len(content), lastEnd)
	}
}

func TestChunkerMergeFromEmbeddingsMatchesChunk(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, uint32(mock.MaxInputBytes()))
	ctx := context.Background()

	content := []byte("small text")

	// Full Chunk path (embeds internally).
	cr1 := chunker.Chunk(ctx, content)

	// Split path: Prepare → Embed → MergeFromEmbeddings.
	prep := chunker.Prepare(ctx, content)
	texts := make([]string, len(prep.Segments))
	for i, seg := range prep.Segments {
		texts[i] = string(content[seg.Start:seg.End])
	}
	embs, _ := chunker.SimilarityEmbedder().EmbedBatch(ctx, texts)
	cr2 := chunker.MergeFromEmbeddings(ctx, content, prep, embs)

	// Same number of boundaries.
	if len(cr1.Boundaries) != len(cr2.Boundaries) {
		t.Fatalf("Chunk produced %d boundaries, MergeFromEmbeddings produced %d",
			len(cr1.Boundaries), len(cr2.Boundaries))
	}
	for i := range cr1.Boundaries {
		if cr1.Boundaries[i].ByteStart != cr2.Boundaries[i].ByteStart ||
			cr1.Boundaries[i].ByteEnd != cr2.Boundaries[i].ByteEnd {
			t.Errorf("boundary %d mismatch: Chunk=[%d,%d) Split=[%d,%d)", i,
				cr1.Boundaries[i].ByteStart, cr1.Boundaries[i].ByteEnd,
				cr2.Boundaries[i].ByteStart, cr2.Boundaries[i].ByteEnd)
		}
	}
}

func TestChunkerSimilarityEmbedder(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	chunker := NewUniversalChunker(mock, 1000)

	emb := chunker.SimilarityEmbedder()
	if emb == nil {
		t.Fatal("SimilarityEmbedder returned nil")
	}
	if emb.Dimension() != mock.Dimension() {
		t.Errorf("dimension mismatch: got %d, want %d", emb.Dimension(), mock.Dimension())
	}
}

func strContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
