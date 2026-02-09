package buffer

import "sort"

// LineInfo stores the offset and length of a single line within the buffer.
type LineInfo struct {
	Number   int // 0-indexed line number
	StartPos int // rune offset of the first character
	Length   int // number of runes (excluding the trailing '\n')
}

// LineIndex maintains a cached mapping from line numbers to rune offsets.
// The index is rebuilt after each edit by calling Rebuild, or updated
// incrementally via IncrementalUpdate for edits that don't cross line
// boundaries.
type LineIndex struct {
	lines []LineInfo
}

// NewLineIndex builds a line index by scanning the piece table for newlines.
func NewLineIndex(pt *PieceTable) *LineIndex {
	idx := &LineIndex{}
	idx.Rebuild(pt)
	return idx
}

// Rebuild re-scans the piece table and reconstructs the line index.
func (li *LineIndex) Rebuild(pt *PieceTable) {
	li.lines = li.lines[:0]
	lineStart := 0
	lineNum := 0
	pos := 0
	each := pt.runeIterator()
	for {
		r, ok := each()
		if !ok {
			break
		}
		if r == '\n' {
			li.lines = append(li.lines, LineInfo{
				Number:   lineNum,
				StartPos: lineStart,
				Length:   pos - lineStart,
			})
			lineNum++
			lineStart = pos + 1
		}
		pos++
	}
	// Final line (may not end with '\n').
	if pos > 0 || len(li.lines) == 0 {
		li.lines = append(li.lines, LineInfo{
			Number:   lineNum,
			StartPos: lineStart,
			Length:   pos - lineStart,
		})
	}
}

// IncrementalUpdate adjusts the line index in-place for an edit that does
// not add or remove newlines. Returns false if a full Rebuild is needed
// (i.e. the edit crosses line boundaries).
func (li *LineIndex) IncrementalUpdate(meta EditMeta) bool {
	if meta.HadNewline || len(li.lines) == 0 {
		return false
	}
	// Binary search for the line containing the edit position.
	lineIdx := sort.Search(len(li.lines), func(i int) bool {
		return li.lines[i].StartPos > meta.Pos
	}) - 1
	if lineIdx < 0 {
		lineIdx = 0
	}
	// Adjust the edited line's length.
	li.lines[lineIdx].Length += meta.Delta
	// Shift all subsequent lines' start positions.
	for i := lineIdx + 1; i < len(li.lines); i++ {
		li.lines[i].StartPos += meta.Delta
	}
	return true
}

// Get returns the LineInfo for the given 0-indexed line number.
func (li *LineIndex) Get(lineNum int) (LineInfo, bool) {
	if lineNum < 0 || lineNum >= len(li.lines) {
		return LineInfo{}, false
	}
	return li.lines[lineNum], true
}

// Count returns the total number of lines.
func (li *LineIndex) Count() int {
	return len(li.lines)
}

// PosToLineCol converts an absolute rune position to a (line, col) pair.
// Both line and col are 0-indexed. Uses binary search: O(log N).
func (li *LineIndex) PosToLineCol(pos int) (int, int) {
	n := len(li.lines)
	if n == 0 {
		return 0, 0
	}
	// Find the last line whose StartPos <= pos.
	idx := sort.Search(n, func(i int) bool {
		return li.lines[i].StartPos > pos
	}) - 1
	if idx < 0 {
		idx = 0
	}
	return li.lines[idx].Number, pos - li.lines[idx].StartPos
}

// LineColToPos converts a (line, col) pair to an absolute rune position.
// Col is clamped to the line length.
func (li *LineIndex) LineColToPos(line, col int) int {
	info, ok := li.Get(line)
	if !ok {
		return 0
	}
	clamped := min(col, info.Length)
	return info.StartPos + clamped
}
