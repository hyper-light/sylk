package sylkdir

// ConversationalChunker splits LLM prompt/response content at turn boundaries.
// Uses blank lines as turn boundary indicators.
type ConversationalChunker struct {
	cfg   ChunkerConfig
	class ContentClass
}

// NewConversationalChunker creates a chunker for conversational content.
func NewConversationalChunker(cfg ChunkerConfig, cc ContentClass) *ConversationalChunker {
	return &ConversationalChunker{cfg: cfg, class: cc}
}

// Chunk splits content at blank line (turn) boundaries.
func (c *ConversationalChunker) Chunk(content []byte) []ChunkBoundary {
	if len(content) == 0 {
		return nil
	}
	candidates := findBlankLinePositions(content)
	return chunkWithCandidates(content, candidates, c.cfg, BoundarySentence)
}

// Class returns the content class this chunker handles.
func (c *ConversationalChunker) Class() ContentClass {
	return c.class
}
