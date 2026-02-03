package buffer

// LineInfo stores the offset and length of a single line within the buffer.
type LineInfo struct {
	Number   int // 0-indexed line number
	StartPos int // rune offset of the first character
	Length   int // number of runes (excluding the trailing '\n')
}

// LineIndex maintains a cached mapping from line numbers to rune offsets.
// The index is rebuilt after each edit by calling Rebuild.
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
// Both line and col are 0-indexed.
func (li *LineIndex) PosToLineCol(pos int) (int, int) {
	for i := len(li.lines) - 1; i >= 0; i-- {
		if pos >= li.lines[i].StartPos {
			return li.lines[i].Number, pos - li.lines[i].StartPos
		}
	}
	return 0, 0
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
