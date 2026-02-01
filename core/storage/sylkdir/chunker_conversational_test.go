package sylkdir

import (
	"strings"
	"testing"
)

func TestConversationalChunkerEmpty(t *testing.T) {
	c := NewConversationalChunker(NewChunkerConfig(512, ContentClassLLMPrompt), ContentClassLLMPrompt)
	if got := c.Chunk(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestConversationalChunkerSingleTurn(t *testing.T) {
	content := []byte("User: Hello, how are you?\n")
	c := NewConversationalChunker(NewChunkerConfig(512, ContentClassLLMPrompt), ContentClassLLMPrompt)

	bounds := c.Chunk(content)
	if len(bounds) != 1 {
		t.Fatalf("got %d chunks, want 1", len(bounds))
	}
	if bounds[0].Kind != BoundarySentence {
		t.Errorf("kind: got %v, want BoundarySentence", bounds[0].Kind)
	}
}

func TestConversationalChunkerMultipleTurns(t *testing.T) {
	turns := []string{
		strings.Repeat("User says a lot of words. ", 20),
		strings.Repeat("Assistant replies at length. ", 20),
		strings.Repeat("User follows up with more. ", 20),
	}
	content := []byte(strings.Join(turns, "\n\n"))

	cfg := ChunkerConfig{MaxChunkBytes: 300, MinChunkBytes: 50, OverlapBytes: 0}
	c := NewConversationalChunker(cfg, ContentClassLLMPrompt)

	bounds := c.Chunk(content)
	if len(bounds) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(bounds))
	}

	verifyBoundsCoverage(t, bounds, uint32(len(content)))
}

func TestConversationalChunkerClass(t *testing.T) {
	c := NewConversationalChunker(NewChunkerConfig(512, ContentClassLLMResponse), ContentClassLLMResponse)
	if c.Class() != ContentClassLLMResponse {
		t.Errorf("class: got %v, want LLMResponse", c.Class())
	}
}
