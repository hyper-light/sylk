package sylkdir

import (
	"strings"
	"testing"
)

func TestHeadingChunkerEmpty(t *testing.T) {
	c := NewHeadingChunker(NewChunkerConfig(512, ContentClassMarkdown), ContentClassMarkdown)
	if got := c.Chunk(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestHeadingChunkerSingleSection(t *testing.T) {
	content := []byte("# Title\nSome paragraph text.\n")
	c := NewHeadingChunker(NewChunkerConfig(512, ContentClassMarkdown), ContentClassMarkdown)

	bounds := c.Chunk(content)
	if len(bounds) != 1 {
		t.Fatalf("got %d chunks, want 1", len(bounds))
	}
	if bounds[0].Kind != BoundaryHeading {
		t.Errorf("kind: got %v, want BoundaryHeading", bounds[0].Kind)
	}
}

func TestHeadingChunkerMultipleSections(t *testing.T) {
	sections := []string{
		"# Section 1\n" + strings.Repeat("paragraph text. ", 30) + "\n",
		"# Section 2\n" + strings.Repeat("more paragraph. ", 30) + "\n",
		"# Section 3\n" + strings.Repeat("final paragraph. ", 30) + "\n",
	}
	content := []byte(strings.Join(sections, ""))

	cfg := ChunkerConfig{MaxChunkBytes: 200, MinChunkBytes: 50, OverlapBytes: 0}
	c := NewHeadingChunker(cfg, ContentClassMarkdown)

	bounds := c.Chunk(content)
	if len(bounds) < 3 {
		t.Fatalf("got %d chunks, want >= 3", len(bounds))
	}

	verifyBoundsCoverage(t, bounds, uint32(len(content)))
}

func TestHeadingChunkerLargeSection(t *testing.T) {
	// One heading followed by content exceeding MaxChunkBytes.
	content := []byte("# Big Section\n" + strings.Repeat("word ", 500) + "\n")

	cfg := ChunkerConfig{MaxChunkBytes: 400, MinChunkBytes: 100, OverlapBytes: 0}
	c := NewHeadingChunker(cfg, ContentClassMarkdown)

	bounds := c.Chunk(content)
	if len(bounds) < 2 {
		t.Fatalf("got %d chunks, want >= 2 (large section split)", len(bounds))
	}

	for _, b := range bounds {
		size := b.ByteEnd - b.ByteStart
		if size > cfg.MaxChunkBytes+1 {
			t.Errorf("chunk too large: %d bytes > max %d", size, cfg.MaxChunkBytes)
		}
	}
}

func TestHeadingChunkerClass(t *testing.T) {
	c := NewHeadingChunker(NewChunkerConfig(512, ContentClassWebContent), ContentClassWebContent)
	if c.Class() != ContentClassWebContent {
		t.Errorf("class: got %v, want WebContent", c.Class())
	}
}
