package sylkdir

import (
	"bytes"
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

func makeBoundary(li lineIndex, start, end uint32, kind BoundaryKind) ChunkBoundary {
	return ChunkBoundary{
		ByteStart: start,
		ByteEnd:   end,
		LineStart: li.lineAt(start),
		LineEnd:   li.lineAtEnd(end),
		Kind:      kind,
	}
}
