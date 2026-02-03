package sylkdir

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

// CodeBlockParser extracts tree-sitter symbols from code blocks found in mixed content.
// Implemented by a wrapper around tree-sitter (handles grammar download).
// The UniversalChunker uses this interface to avoid depending on the treesitter package.
type CodeBlockParser interface {
	// ParseBlock parses a code block body and returns symbol boundaries.
	// Language is the tree-sitter grammar name (from fenced code block tag).
	// Returns nil if grammar unavailable or parsing fails (graceful degradation).
	ParseBlock(ctx context.Context, language string, content []byte) []SymbolBound
}

// ChunkContext carries tree-sitter metadata for embedding enrichment.
type ChunkContext struct {
	FilePath       string
	Language       string
	ContainingSyms []SymbolMetadata
}

// SymbolMetadata carries symbol info for embedding text enrichment.
type SymbolMetadata struct {
	Name      string
	Kind      string
	Signature string
}

// UniversalChunker combines boundary detection + segment splitting + agglomerative merge.
type UniversalChunker struct {
	merger          *SegmentMerger
	codeBlockParser CodeBlockParser
}

// NewUniversalChunker creates a universal chunker using the given similarity embedder
// and byte ceiling (from production embedder's MaxInputBytes).
func NewUniversalChunker(similarity embedder.Embedder, ceiling uint32) *UniversalChunker {
	return &UniversalChunker{
		merger: NewSegmentMerger(similarity, ceiling),
	}
}

// SetCodeBlockParser enables mixed content handling.
// When set, Chunk detects fenced code blocks, parses them with tree-sitter,
// and creates AST-aware boundaries within code blocks.
func (c *UniversalChunker) SetCodeBlockParser(p CodeBlockParser) {
	c.codeBlockParser = p
}

// ChunkResult holds chunk boundaries and their corresponding embeddings.
// Embeddings are parallel to Boundaries — result.Embeddings[i] is the
// cached embedding for the content in result.Boundaries[i]. Embeddings may be
// nil if the merger failed to produce them (graceful degradation).
type ChunkResult struct {
	Boundaries []ChunkBoundary
	Embeddings [][]float32
}

// Chunk splits content using text boundary detection (blank lines + headings + code block fences).
// When a CodeBlockParser is set, also detects fenced code blocks, parses them with tree-sitter,
// and creates AST-aware symbol boundaries within code regions.
func (c *UniversalChunker) Chunk(ctx context.Context, content []byte) ChunkResult {
	if len(content) == 0 {
		return ChunkResult{}
	}

	li := buildLineIndex(content)
	boundaries := DetectBoundaries(content, li)

	if c.codeBlockParser != nil {
		symBounds := c.parseCodeBlockSymbols(ctx, content, li)
		if len(symBounds) > 0 {
			extra := symbolTaggedBoundaries(li, symBounds)
			boundaries = mergeTaggedBoundaries(boundaries, extra)
		}
	}

	return c.chunkFromBoundaries(ctx, content, li, boundaries)
}

// ChunkWithSymbols splits content using text boundaries + tree-sitter symbol boundaries.
// Used for ALL content where tree-sitter has parsed the entire content as code.
func (c *UniversalChunker) ChunkWithSymbols(ctx context.Context, content []byte, symbols []SymbolBound) ChunkResult {
	if len(content) == 0 {
		return ChunkResult{}
	}

	li := buildLineIndex(content)
	boundaries := DetectBoundariesWithSymbols(content, li, symbols)
	return c.chunkFromBoundaries(ctx, content, li, boundaries)
}

// chunkFromBoundaries splits at boundaries, merges, and converts to ChunkBoundary.
func (c *UniversalChunker) chunkFromBoundaries(ctx context.Context, content []byte, li lineIndex, boundaries []TaggedBoundary) ChunkResult {
	segments := SplitAtBoundaries(content, boundaries)
	if len(segments) == 0 {
		return ChunkResult{}
	}

	mr := c.merger.Merge(ctx, content, segments)
	return ChunkResult{
		Boundaries: segmentsToBoundaries(li, mr.Segments),
		Embeddings: mr.Embeddings,
	}
}

// parseCodeBlockSymbols detects fenced code blocks and parses them with tree-sitter.
func (c *UniversalChunker) parseCodeBlockSymbols(ctx context.Context, content []byte, li lineIndex) []SymbolBound {
	blocks := DetectCodeBlocks(content, li)
	if len(blocks) == 0 {
		return nil
	}

	var allSymbols []SymbolBound
	for _, blk := range blocks {
		if blk.Language == "" || blk.BodyStart >= blk.BodyEnd {
			continue
		}
		body := content[blk.BodyStart:blk.BodyEnd]
		syms := c.codeBlockParser.ParseBlock(ctx, blk.Language, body)

		// Adjust symbol line numbers from block-relative to content-relative.
		blockStartLine := li.lineAt(blk.BodyStart)
		for _, sym := range syms {
			adjusted := SymbolBound{
				StartLine: sym.StartLine + blockStartLine - 1,
				Strength:  sym.Strength,
			}
			if sym.EndLine > 0 {
				adjusted.EndLine = sym.EndLine + blockStartLine - 1
			}
			allSymbols = append(allSymbols, adjusted)
		}
	}

	return allSymbols
}

// segmentsToBoundaries converts merged segments to ChunkBoundary slice.
func segmentsToBoundaries(li lineIndex, segments []Segment) []ChunkBoundary {
	boundaries := make([]ChunkBoundary, len(segments))
	for i, seg := range segments {
		boundaries[i] = makeBoundary(li, seg.Start, seg.End, BoundaryStructural)
	}
	return boundaries
}

// EnrichedChunkText generates embedding text with structural metadata prefix.
func EnrichedChunkText(content []byte, boundary ChunkBoundary, chunkCtx *ChunkContext) string {
	if chunkCtx == nil {
		return string(content[boundary.ByteStart:boundary.ByteEnd])
	}

	var b strings.Builder
	b.WriteString("# path: ")
	b.WriteString(chunkCtx.FilePath)
	b.WriteByte('\n')

	if chunkCtx.Language != "" {
		b.WriteString("# language: ")
		b.WriteString(chunkCtx.Language)
		b.WriteByte('\n')
	}

	for _, sym := range chunkCtx.ContainingSyms {
		b.WriteString("# ")
		b.WriteString(sym.Kind)
		b.WriteString(": ")
		b.WriteString(sym.Name)
		if sym.Signature != "" {
			b.WriteByte(' ')
			b.WriteString(sym.Signature)
		}
		b.WriteByte('\n')
	}

	b.Write(content[boundary.ByteStart:boundary.ByteEnd])
	return b.String()
}

// BuildSymbolIntervalIndex returns a function that finds symbols overlapping a line range.
func BuildSymbolIntervalIndex(symbols []SymbolMetadata, startLines, endLines []uint32) func(startLine, endLine uint32) []SymbolMetadata {
	return func(startLine, endLine uint32) []SymbolMetadata {
		var result []SymbolMetadata
		for i, sl := range startLines {
			el := endLines[i]
			if el == 0 {
				el = sl // Single-line symbol.
			}
			if sl <= endLine && el >= startLine {
				result = append(result, symbols[i])
			}
		}
		return result
	}
}

// SearchKindToStrength maps search.SymbolKind (string) to boundary strength.
// Uses the same scale as SymbolKindStrength.
func SearchKindToStrength(kind string) uint32 {
	switch kind {
	case "interface", "type":
		return 3
	case "function", "method":
		return 2
	default: // "variable", "const", unknown
		return 1
	}
}

// FormatSymbolKind returns a human-readable kind string for embedding enrichment.
func FormatSymbolKind(kind uint8) string {
	// ingestion.SymbolKind: 0=Unknown, 1=Function, 2=Method, 3=Type, 4=Interface, 5=Const, 6=Var
	switch kind {
	case 1:
		return "function"
	case 2:
		return "method"
	case 3:
		return "type"
	case 4:
		return "interface"
	case 5:
		return "const"
	case 6:
		return "var"
	default:
		return fmt.Sprintf("symbol(%d)", kind)
	}
}
