package sylkdir

import (
	"strings"
	"testing"
)

func TestShortDocChunkerEmpty(t *testing.T) {
	c := NewShortDocChunker(NewChunkerConfig(512, ContentClassGitCommit), ContentClassGitCommit)
	if got := c.Chunk(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestShortDocChunkerSmall(t *testing.T) {
	content := []byte("fix: correct null pointer in auth handler\n\nCloses #123\n")
	c := NewShortDocChunker(NewChunkerConfig(512, ContentClassGitCommit), ContentClassGitCommit)

	bounds := c.Chunk(content)
	if len(bounds) != 1 {
		t.Fatalf("got %d chunks, want 1", len(bounds))
	}
	if bounds[0].ByteEnd != uint32(len(content)) {
		t.Errorf("end: got %d, want %d", bounds[0].ByteEnd, len(content))
	}
}

func TestShortDocChunkerLarge(t *testing.T) {
	// Config content exceeding MaxChunkBytes.
	var lines []string
	for i := range 100 {
		lines = append(lines, strings.Repeat("key=value ", 5)+string(rune('0'+i%10)))
	}
	content := []byte(strings.Join(lines, "\n") + "\n")

	cfg := ChunkerConfig{MaxChunkBytes: 200, MinChunkBytes: 50, OverlapBytes: 0}
	c := NewShortDocChunker(cfg, ContentClassConfig)

	bounds := c.Chunk(content)
	if len(bounds) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(bounds))
	}

	verifyBoundsCoverage(t, bounds, uint32(len(content)))
}

func TestShortDocChunkerClass(t *testing.T) {
	c := NewShortDocChunker(NewChunkerConfig(512, ContentClassNote), ContentClassNote)
	if c.Class() != ContentClassNote {
		t.Errorf("class: got %v, want Note", c.Class())
	}
}
