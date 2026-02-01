package sylkdir

// ShortDocChunker handles short documents (git commits, config, notes).
// Single chunk for small content; falls back to newline splitting if oversized.
type ShortDocChunker struct {
	cfg   ChunkerConfig
	class ContentClass
}

// NewShortDocChunker creates a chunker for short document content.
func NewShortDocChunker(cfg ChunkerConfig, cc ContentClass) *ShortDocChunker {
	return &ShortDocChunker{cfg: cfg, class: cc}
}

// Chunk returns a single chunk for small content, or splits at newlines.
func (c *ShortDocChunker) Chunk(content []byte) []ChunkBoundary {
	if len(content) == 0 {
		return nil
	}
	li := buildLineIndex(content)
	candidates := findNewlinePositions(li)
	return splitContent(li, uint32(len(content)), candidates, c.cfg, BoundarySentence)
}

// Class returns the content class this chunker handles.
func (c *ShortDocChunker) Class() ContentClass {
	return c.class
}
