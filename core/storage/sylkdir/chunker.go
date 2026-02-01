package sylkdir

import (
	"bytes"
	"slices"
	"sort"
)

// ChunkBoundary describes a single chunk's byte and line range within content.
type ChunkBoundary struct {
	ByteStart uint32
	ByteEnd   uint32
	LineStart uint32
	LineEnd   uint32
	Kind      BoundaryKind
}

// Chunker splits document content into boundary-aligned chunks.
// Implementations are stateless, deterministic, and safe for concurrent use.
type Chunker interface {
	Chunk(content []byte) []ChunkBoundary
	Class() ContentClass
}

// ChunkerConfig holds chunk size parameters derived from embedder constraints.
// All sizes are in bytes — derived from maxTokens, never hardcoded.
type ChunkerConfig struct {
	MaxChunkBytes    uint32
	MinChunkBytes    uint32
	OverlapBytes     uint32
	EstBytesPerToken float64
}

// estBytesPerToken maps ContentClass to estimated bytes per token.
var estBytesPerToken = [contentClassCount]float64{
	ContentClassSourceCode:  4.0,
	ContentClassMarkdown:    5.0,
	ContentClassWebContent:  5.0,
	ContentClassLLMPrompt:   5.0,
	ContentClassLLMResponse: 5.0,
	ContentClassGitCommit:   5.0,
	ContentClassConfig:      3.5,
	ContentClassNote:        5.0,
	ContentClassAcademic:    5.0,
}

// NewChunkerConfig creates a ChunkerConfig from embedder max input tokens
// and the target content class. All byte sizes are derived from maxTokens.
func NewChunkerConfig(maxTokens int, cc ContentClass) ChunkerConfig {
	bpt := estBytesPerToken[cc]
	if bpt == 0 {
		bpt = 5.0
	}
	maxBytes := uint32(float64(maxTokens) * bpt)
	return ChunkerConfig{
		MaxChunkBytes:    max(maxBytes, 64),
		MinChunkBytes:    max(maxBytes/4, 16),
		OverlapBytes:     maxBytes / 8,
		EstBytesPerToken: bpt,
	}
}

// ChunkerRegistry maps ContentClass to Chunker implementations.
// All chunkers are registered at construction; Get is lock-free.
type ChunkerRegistry struct {
	chunkers [contentClassCount]Chunker
}

// NewChunkerRegistry creates a registry with all chunkers initialized.
func NewChunkerRegistry(maxTokens int) *ChunkerRegistry {
	r := &ChunkerRegistry{}
	r.chunkers[ContentClassSourceCode] = NewSourceCodeChunker(NewChunkerConfig(maxTokens, ContentClassSourceCode))
	r.chunkers[ContentClassMarkdown] = NewHeadingChunker(NewChunkerConfig(maxTokens, ContentClassMarkdown), ContentClassMarkdown)
	r.chunkers[ContentClassWebContent] = NewHeadingChunker(NewChunkerConfig(maxTokens, ContentClassWebContent), ContentClassWebContent)
	r.chunkers[ContentClassAcademic] = NewHeadingChunker(NewChunkerConfig(maxTokens, ContentClassAcademic), ContentClassAcademic)
	r.chunkers[ContentClassLLMPrompt] = NewConversationalChunker(NewChunkerConfig(maxTokens, ContentClassLLMPrompt), ContentClassLLMPrompt)
	r.chunkers[ContentClassLLMResponse] = NewConversationalChunker(NewChunkerConfig(maxTokens, ContentClassLLMResponse), ContentClassLLMResponse)
	r.chunkers[ContentClassGitCommit] = NewShortDocChunker(NewChunkerConfig(maxTokens, ContentClassGitCommit), ContentClassGitCommit)
	r.chunkers[ContentClassConfig] = NewShortDocChunker(NewChunkerConfig(maxTokens, ContentClassConfig), ContentClassConfig)
	r.chunkers[ContentClassNote] = NewShortDocChunker(NewChunkerConfig(maxTokens, ContentClassNote), ContentClassNote)
	return r
}

// Get returns the Chunker for the given ContentClass.
func (r *ChunkerRegistry) Get(cc ContentClass) Chunker {
	if cc < contentClassCount {
		return r.chunkers[cc]
	}
	return r.chunkers[ContentClassNote]
}

// ---------------------------------------------------------------------------
// lineIndex maps 1-indexed line numbers to byte offsets in content.
// ---------------------------------------------------------------------------

type lineIndex struct {
	starts []uint32
}

func buildLineIndex(content []byte) lineIndex {
	n := bytes.Count(content, []byte{'\n'})
	starts := make([]uint32, 1, n+1)
	starts[0] = 0
	return lineIndex{starts: scanNewlines(starts, content)}
}

func scanNewlines(starts []uint32, content []byte) []uint32 {
	end := uint32(len(content))
	for i, b := range content {
		starts = appendIfNewline(starts, b, uint32(i+1), end)
	}
	return starts
}

func appendIfNewline(starts []uint32, b byte, next, end uint32) []uint32 {
	if b != '\n' {
		return starts
	}
	if next >= end {
		return starts
	}
	return append(starts, next)
}

// lineAt returns the 1-indexed line number containing the byte offset.
func (li lineIndex) lineAt(offset uint32) uint32 {
	idx := sort.Search(len(li.starts), func(i int) bool { return li.starts[i] > offset })
	return uint32(max(idx, 1))
}

// lineAtEnd returns the 1-indexed line number of the last byte before end.
func (li lineIndex) lineAtEnd(end uint32) uint32 {
	if end == 0 {
		return 1
	}
	return li.lineAt(end - 1)
}

// byteAt returns the byte offset where the given 1-indexed line starts.
func (li lineIndex) byteAt(line uint32) uint32 {
	if line == 0 {
		return 0
	}
	idx := int(line - 1)
	if idx >= len(li.starts) {
		return 0
	}
	return li.starts[idx]
}

// lineCount returns the total number of lines.
func (li lineIndex) lineCount() uint32 {
	return uint32(len(li.starts))
}

// snapToLineStart returns the byte offset of the line start at or before offset.
func (li lineIndex) snapToLineStart(offset uint32) uint32 {
	return li.byteAt(li.lineAt(offset))
}

// ---------------------------------------------------------------------------
// Content splitting core
// ---------------------------------------------------------------------------

// splitContent produces boundaries by splitting at candidate byte offsets.
func splitContent(li lineIndex, contentLen uint32, candidates []uint32, cfg ChunkerConfig, kind BoundaryKind) []ChunkBoundary {
	if contentLen <= cfg.MaxChunkBytes {
		return singleChunk(li, contentLen, kind)
	}
	return greedySplit(li, contentLen, candidates, cfg, kind)
}

func singleChunk(li lineIndex, contentLen uint32, kind BoundaryKind) []ChunkBoundary {
	return []ChunkBoundary{makeBoundary(li, 0, contentLen, kind)}
}

func greedySplit(li lineIndex, contentLen uint32, candidates []uint32, cfg ChunkerConfig, kind BoundaryKind) []ChunkBoundary {
	bounds := make([]ChunkBoundary, 0, estimateChunkCount(contentLen, cfg.MaxChunkBytes))
	pos := uint32(0)
	for pos < contentLen {
		end := max(nextChunkEnd(li, contentLen, candidates, pos, cfg), pos+1)
		bounds = append(bounds, makeBoundary(li, pos, end, kind))
		pos = advancePosition(li, end, contentLen, pos, cfg.OverlapBytes)
	}
	return bounds
}

func nextChunkEnd(li lineIndex, contentLen uint32, candidates []uint32, pos uint32, cfg ChunkerConfig) uint32 {
	maxEnd := min(pos+cfg.MaxChunkBytes, contentLen)
	if maxEnd >= contentLen {
		return contentLen
	}
	splitAt, found := bestCandidateInRange(candidates, pos+cfg.MinChunkBytes, maxEnd)
	if found {
		return splitAt
	}
	return fallbackSplit(li, pos, maxEnd)
}

// bestCandidateInRange returns the largest candidate in [lo, hi).
func bestCandidateInRange(candidates []uint32, lo, hi uint32) (uint32, bool) {
	idx := sort.Search(len(candidates), func(i int) bool { return candidates[i] >= hi })
	if idx == 0 {
		return 0, false
	}
	if candidates[idx-1] < lo {
		return 0, false
	}
	return candidates[idx-1], true
}

// fallbackSplit finds the nearest line boundary before maxEnd.
func fallbackSplit(li lineIndex, pos, maxEnd uint32) uint32 {
	lineStart := li.snapToLineStart(maxEnd)
	if lineStart > pos {
		return lineStart
	}
	return maxEnd
}

// advancePosition computes where the next chunk starts after end.
func advancePosition(li lineIndex, end, contentLen, prevStart, overlap uint32) uint32 {
	if end >= contentLen {
		return contentLen
	}
	if overlap == 0 {
		return end
	}
	return snapOverlapStart(li, end, prevStart, overlap)
}

func snapOverlapStart(li lineIndex, end, prevStart, overlap uint32) uint32 {
	target := max(end-overlap, prevStart+1)
	return max(li.snapToLineStart(target), prevStart+1)
}

func makeBoundary(li lineIndex, start, end uint32, kind BoundaryKind) ChunkBoundary {
	return ChunkBoundary{
		ByteStart: start,
		ByteEnd:   end,
		LineStart: li.lineAt(start),
		LineEnd:   li.lineAtEnd(end),
		Kind:      kind,
	}
}

func estimateChunkCount(contentLen, maxChunkBytes uint32) int {
	if maxChunkBytes == 0 {
		return 1
	}
	return int((contentLen + maxChunkBytes - 1) / maxChunkBytes)
}

// ---------------------------------------------------------------------------
// Candidate helpers shared across chunkers
// ---------------------------------------------------------------------------

// chunkWithCandidates is a convenience for chunkers that don't need lineIndex
// for candidate finding.
func chunkWithCandidates(content []byte, candidates []uint32, cfg ChunkerConfig, kind BoundaryKind) []ChunkBoundary {
	li := buildLineIndex(content)
	return splitContent(li, uint32(len(content)), candidates, cfg, kind)
}

// findBlankLinePositions returns byte offsets after blank line sequences.
func findBlankLinePositions(content []byte) []uint32 {
	var positions []uint32
	for i := 0; i < len(content); i++ {
		end := blankLineEnd(content, i)
		if end > 0 {
			positions = append(positions, uint32(end))
			i = end - 1
		}
	}
	return positions
}

func blankLineEnd(content []byte, i int) int {
	if !isBlankLine(content, i) {
		return 0
	}
	return skipConsecutiveNewlines(content, i+2)
}

func isBlankLine(content []byte, i int) bool {
	if i+1 >= len(content) {
		return false
	}
	if content[i] != '\n' {
		return false
	}
	return content[i+1] == '\n'
}

func skipConsecutiveNewlines(content []byte, start int) int {
	j := start
	for j < len(content) {
		if content[j] != '\n' {
			return j
		}
		j++
	}
	return 0
}

// findNewlinePositions returns byte offsets of each line start (from line 2 onward).
func findNewlinePositions(li lineIndex) []uint32 {
	if li.lineCount() <= 1 {
		return nil
	}
	positions := make([]uint32, 0, li.lineCount()-1)
	for i := uint32(2); i <= li.lineCount(); i++ {
		positions = append(positions, li.byteAt(i))
	}
	return positions
}

// mergeCandidates combines two sorted candidate slices, deduplicating.
func mergeCandidates(a, b []uint32) []uint32 {
	combined := make([]uint32, 0, len(a)+len(b))
	combined = append(combined, a...)
	combined = append(combined, b...)
	slices.Sort(combined)
	return slices.Compact(combined)
}
