package sylkdir

// HeadingChunker splits content at heading boundaries (lines starting with #).
// Falls back to blank line (paragraph) boundaries for oversized sections.
type HeadingChunker struct {
	cfg   ChunkerConfig
	class ContentClass
}

// NewHeadingChunker creates a heading-based chunker for the given content class.
func NewHeadingChunker(cfg ChunkerConfig, cc ContentClass) *HeadingChunker {
	return &HeadingChunker{cfg: cfg, class: cc}
}

// Chunk splits content at heading and paragraph boundaries.
func (c *HeadingChunker) Chunk(content []byte) []ChunkBoundary {
	if len(content) == 0 {
		return nil
	}
	li := buildLineIndex(content)
	candidates := headingAndParagraphCandidates(content, li)
	return splitContent(li, uint32(len(content)), candidates, c.cfg, BoundaryHeading)
}

// Class returns the content class this chunker handles.
func (c *HeadingChunker) Class() ContentClass {
	return c.class
}

func headingAndParagraphCandidates(content []byte, li lineIndex) []uint32 {
	headings := findHeadingPositions(content, li)
	blanks := findBlankLinePositions(content)
	return mergeCandidates(headings, blanks)
}

func findHeadingPositions(content []byte, li lineIndex) []uint32 {
	var positions []uint32
	for i := uint32(1); i <= li.lineCount(); i++ {
		if isHeadingLine(content, li.byteAt(i)) {
			positions = append(positions, li.byteAt(i))
		}
	}
	return positions
}

func isHeadingLine(content []byte, offset uint32) bool {
	if offset >= uint32(len(content)) {
		return false
	}
	return content[offset] == '#'
}
