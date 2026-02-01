package sylkdir

import "github.com/adalundhe/sylk/core/search"

// SourceCodeChunker splits source code at structural boundaries.
// Priority: symbol boundaries → blank lines → line boundaries.
type SourceCodeChunker struct {
	cfg ChunkerConfig
}

// NewSourceCodeChunker creates a chunker for source code content.
func NewSourceCodeChunker(cfg ChunkerConfig) *SourceCodeChunker {
	return &SourceCodeChunker{cfg: cfg}
}

// Chunk splits content at blank line boundaries.
func (c *SourceCodeChunker) Chunk(content []byte) []ChunkBoundary {
	if len(content) == 0 {
		return nil
	}
	candidates := findBlankLinePositions(content)
	return chunkWithCandidates(content, candidates, c.cfg, BoundaryStructural)
}

// ChunkWithSymbols splits content using symbol boundaries as primary candidates.
func (c *SourceCodeChunker) ChunkWithSymbols(content []byte, symbols []search.SymbolInfo) []ChunkBoundary {
	if len(content) == 0 {
		return nil
	}
	li := buildLineIndex(content)
	candidates := symbolCandidates(li, content, symbols)
	return splitContent(li, uint32(len(content)), candidates, c.cfg, BoundaryStructural)
}

// Class returns ContentClassSourceCode.
func (c *SourceCodeChunker) Class() ContentClass {
	return ContentClassSourceCode
}

func symbolCandidates(li lineIndex, content []byte, symbols []search.SymbolInfo) []uint32 {
	structural := symbolBoundaries(li, symbols)
	blanks := findBlankLinePositions(content)
	return mergeCandidates(structural, blanks)
}

func symbolBoundaries(li lineIndex, symbols []search.SymbolInfo) []uint32 {
	bounds := make([]uint32, 0, len(symbols))
	for _, sym := range symbols {
		if sym.Line >= 1 {
			bounds = append(bounds, li.byteAt(uint32(sym.Line)))
		}
	}
	return bounds
}
