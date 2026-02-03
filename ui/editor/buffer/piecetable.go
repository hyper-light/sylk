// Package buffer provides the core text storage for the editor using a
// piece-table data structure. All positions are rune-indexed (Unicode-aware).
package buffer

// Source indicates which buffer a piece references.
type Source int

const (
	// Original references the immutable initial-content buffer.
	Original Source = iota
	// Add references the append-only insert buffer.
	Add
)

// Piece describes a contiguous span within one of the two source buffers.
type Piece struct {
	Source Source
	Offset int // rune offset into the source buffer
	Length int // number of runes
}

// PieceTable implements a two-buffer piece table for efficient text editing.
// The original buffer is immutable; new text is appended to the add buffer.
// Only the pieces slice is mutated during edits.
type PieceTable struct {
	original []rune
	add      []rune
	pieces   []Piece
}

// NewPieceTable creates a PieceTable initialised with content.
func NewPieceTable(content string) *PieceTable {
	runes := []rune(content)
	pt := &PieceTable{
		original: runes,
		add:      make([]rune, 0, len(runes)),
	}
	if len(runes) > 0 {
		pt.pieces = []Piece{{Source: Original, Offset: 0, Length: len(runes)}}
	}
	return pt
}

// Length returns the total number of runes across all pieces.
func (pt *PieceTable) Length() int {
	total := 0
	for _, p := range pt.pieces {
		total += p.Length
	}
	return total
}

// Insert inserts text at the given rune position.
func (pt *PieceTable) Insert(pos int, text string) {
	runes := []rune(text)
	if len(runes) == 0 {
		return
	}
	addOffset := len(pt.add)
	pt.add = append(pt.add, runes...)
	newPiece := Piece{Source: Add, Offset: addOffset, Length: len(runes)}

	idx, offset := pt.findPiece(pos)
	pt.spliceInsert(idx, offset, newPiece)
}

// Delete removes length runes starting at pos.
func (pt *PieceTable) Delete(pos, length int) {
	if length <= 0 {
		return
	}
	startIdx, startOff := pt.findPiece(pos)
	endIdx, endOff := pt.findPiece(pos + length)
	pt.spliceDelete(startIdx, startOff, endIdx, endOff)
}

// Content materialises the full text by walking all pieces.
func (pt *PieceTable) Content() string {
	buf := make([]rune, 0, pt.Length())
	for _, p := range pt.pieces {
		buf = append(buf, pt.sourceSlice(p)...)
	}
	return string(buf)
}

// Line returns the n-th line (0-indexed). Lines are delimited by '\n'.
func (pt *PieceTable) Line(n int) string {
	var lineStart, cur, lineNum int
	each := pt.runeIterator()
	for {
		r, ok := each()
		if !ok {
			break
		}
		if r == '\n' {
			if lineNum == n {
				return pt.substringRunes(lineStart, cur)
			}
			lineNum++
			lineStart = cur + 1
		}
		cur++
	}
	if lineNum == n {
		return pt.substringRunes(lineStart, cur)
	}
	return ""
}

// LineCount returns the number of lines (at least 1 for non-empty content).
func (pt *PieceTable) LineCount() int {
	if pt.Length() == 0 {
		return 0
	}
	count := 1
	each := pt.runeIterator()
	for {
		r, ok := each()
		if !ok {
			break
		}
		if r == '\n' {
			count++
		}
	}
	return count
}

// RuneAt returns the rune at the absolute rune position.
func (pt *PieceTable) RuneAt(pos int) rune {
	offset := 0
	for _, p := range pt.pieces {
		if pos < offset+p.Length {
			src := pt.sourceBuffer(p.Source)
			return src[p.Offset+(pos-offset)]
		}
		offset += p.Length
	}
	return 0
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// sourceBuffer returns the rune slice for the given source.
func (pt *PieceTable) sourceBuffer(s Source) []rune {
	if s == Original {
		return pt.original
	}
	return pt.add
}

// sourceSlice returns the rune slice referenced by a piece.
func (pt *PieceTable) sourceSlice(p Piece) []rune {
	src := pt.sourceBuffer(p.Source)
	return src[p.Offset : p.Offset+p.Length]
}

// findPiece locates the piece and intra-piece offset for a rune position.
// Returns (pieceIndex, offsetWithinPiece). When pos equals the total length
// the returned index equals len(pieces) with offset 0 (append position).
func (pt *PieceTable) findPiece(pos int) (int, int) {
	cur := 0
	for i, p := range pt.pieces {
		if pos <= cur+p.Length {
			return i, pos - cur
		}
		cur += p.Length
	}
	return len(pt.pieces), 0
}

// spliceInsert inserts newPiece into the piece list, splitting the piece at
// (idx, offset) when necessary.
func (pt *PieceTable) spliceInsert(idx, offset int, newPiece Piece) {
	// Append at end.
	if idx == len(pt.pieces) {
		pt.pieces = append(pt.pieces, newPiece)
		return
	}
	target := pt.pieces[idx]
	// Insert at the beginning of a piece.
	if offset == 0 {
		pt.insertPiecesAt(idx, newPiece)
		return
	}
	// Insert at the end of a piece.
	if offset == target.Length {
		pt.insertPiecesAt(idx+1, newPiece)
		return
	}
	// Split target into left + newPiece + right.
	left := Piece{Source: target.Source, Offset: target.Offset, Length: offset}
	right := Piece{Source: target.Source, Offset: target.Offset + offset, Length: target.Length - offset}
	pt.replacePieces(idx, 1, left, newPiece, right)
}

// spliceDelete removes the rune range described by the start/end piece
// boundaries, keeping any partial pieces on both sides.
func (pt *PieceTable) spliceDelete(startIdx, startOff, endIdx, endOff int) {
	var keep []Piece
	if startOff > 0 {
		orig := pt.pieces[startIdx]
		keep = append(keep, Piece{Source: orig.Source, Offset: orig.Offset, Length: startOff})
	}
	if endIdx < len(pt.pieces) && endOff < pt.pieces[endIdx].Length {
		orig := pt.pieces[endIdx]
		keep = append(keep, Piece{Source: orig.Source, Offset: orig.Offset + endOff, Length: orig.Length - endOff})
	}
	end := endIdx + 1
	if endIdx >= len(pt.pieces) {
		end = len(pt.pieces)
	}
	pt.replacePieces(startIdx, end-startIdx, keep...)
}

// insertPiecesAt inserts one or more pieces before the given index.
func (pt *PieceTable) insertPiecesAt(idx int, add ...Piece) {
	result := make([]Piece, 0, len(pt.pieces)+len(add))
	result = append(result, pt.pieces[:idx]...)
	result = append(result, add...)
	result = append(result, pt.pieces[idx:]...)
	pt.pieces = result
}

// replacePieces replaces count pieces starting at idx with the given pieces.
func (pt *PieceTable) replacePieces(idx, count int, replacement ...Piece) {
	result := make([]Piece, 0, len(pt.pieces)-count+len(replacement))
	result = append(result, pt.pieces[:idx]...)
	result = append(result, replacement...)
	result = append(result, pt.pieces[idx+count:]...)
	pt.pieces = result
}

// substringRunes extracts a rune substring [start, end) from the content.
func (pt *PieceTable) substringRunes(start, end int) string {
	buf := make([]rune, 0, end-start)
	pos := 0
	for _, p := range pt.pieces {
		pEnd := pos + p.Length
		if pEnd <= start {
			pos = pEnd
			continue
		}
		if pos >= end {
			break
		}
		src := pt.sourceBuffer(p.Source)
		lo := max(start-pos, 0)
		hi := min(end-pos, p.Length)
		buf = append(buf, src[p.Offset+lo:p.Offset+hi]...)
		pos = pEnd
	}
	return string(buf)
}

// runeIterator returns a closure that yields runes in document order.
func (pt *PieceTable) runeIterator() func() (rune, bool) {
	pi := 0
	ri := 0
	return func() (rune, bool) {
		for pi < len(pt.pieces) {
			p := pt.pieces[pi]
			if ri < p.Length {
				src := pt.sourceBuffer(p.Source)
				r := src[p.Offset+ri]
				ri++
				return r, true
			}
			pi++
			ri = 0
		}
		return 0, false
	}
}
