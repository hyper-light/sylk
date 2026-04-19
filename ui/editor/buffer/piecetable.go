// Package buffer provides the core text storage for the editor using a
// piece-table data structure. All positions are rune-indexed (Unicode-aware).
package buffer

import (
	"errors"
	"slices"
)

// ErrPositionOutOfBounds is returned by Insert / Delete when the requested
// position (or range) is outside the buffer's current contents. Callers can
// errors.Is against this sentinel to detect validation failures. The prior
// implementation silently clamped or faulted at the piece-lookup stage.
var ErrPositionOutOfBounds = errors.New("piecetable: position out of bounds")

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

// EditMeta describes the last mutation for incremental index updates.
type EditMeta struct {
	Pos        int  // rune position where the edit occurred
	Delta      int  // positive for insert, negative for delete
	HadNewline bool // whether the affected text contained '\n'
}

// PieceTable implements a two-buffer piece table for efficient text editing.
// The original buffer is immutable; new text is appended to the add buffer.
// Only the pieces slice is mutated during edits.
type PieceTable struct {
	original  []rune
	add       []rune
	pieces    []Piece
	length    int    // cached total rune count
	version   uint64 // incremented on every Insert/Delete
	lastEdit  EditMeta
	cachedStr string // cached Content() result
	cachedVer uint64 // version when cachedStr was built
}

// NewPieceTable creates a PieceTable initialised with content.
func NewPieceTable(content string) *PieceTable {
	runes := []rune(content)
	pt := &PieceTable{
		original:  runes,
		add:       make([]rune, 0, len(runes)),
		length:    len(runes),
		cachedStr: content,
		// cachedVer 0 matches initial version 0, so Content() hits cache.
	}
	if len(runes) > 0 {
		pt.pieces = []Piece{{Source: Original, Offset: 0, Length: len(runes)}}
	}
	return pt
}

// Length returns the total number of runes across all pieces. O(1).
func (pt *PieceTable) Length() int { return pt.length }

// Insert inserts text at the given rune position. Returns
// ErrPositionOutOfBounds if pos is outside [0, Length()]. Inserting at
// Length() (append) is valid. An empty text returns nil without mutation.
func (pt *PieceTable) Insert(pos int, text string) error {
	if pos < 0 || pos > pt.length {
		return ErrPositionOutOfBounds
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	addOffset := len(pt.add)
	pt.add = append(pt.add, runes...)
	newPiece := Piece{Source: Add, Offset: addOffset, Length: len(runes)}

	idx, offset := pt.findPiece(pos)
	pt.spliceInsert(idx, offset, newPiece)
	pt.length += len(runes)
	pt.version++
	pt.lastEdit = EditMeta{
		Pos:        pos,
		Delta:      len(runes),
		HadNewline: containsNewline(runes),
	}
	return nil
}

// Delete removes length runes starting at pos. Returns
// ErrPositionOutOfBounds if [pos, pos+length) is not fully contained in
// [0, Length()). A non-positive length returns nil without mutation.
func (pt *PieceTable) Delete(pos, length int) error {
	if length <= 0 {
		return nil
	}
	if pos < 0 || pos+length > pt.length {
		return ErrPositionOutOfBounds
	}
	// Check for newlines before mutating pieces.
	hadNL := pt.rangeHasNewline(pos, length)

	startIdx, startOff := pt.findPiece(pos)
	endIdx, endOff := pt.findPiece(pos + length)
	pt.spliceDelete(startIdx, startOff, endIdx, endOff)
	pt.length -= length
	pt.version++
	pt.lastEdit = EditMeta{
		Pos:        pos,
		Delta:      -length,
		HadNewline: hadNL,
	}
	return nil
}

// Version returns a monotonically increasing counter that changes on every
// Insert or Delete. Callers can compare versions to detect content changes
// even when the buffer length stays the same (e.g. character replace).
func (pt *PieceTable) Version() uint64 {
	return pt.version
}

// LastEdit returns metadata about the most recent Insert or Delete.
func (pt *PieceTable) LastEdit() EditMeta { return pt.lastEdit }

// Content materialises the full text by walking all pieces. The result
// is cached and returned in O(1) on repeated calls without intervening edits.
func (pt *PieceTable) Content() string {
	if pt.version == pt.cachedVer {
		return pt.cachedStr
	}
	buf := make([]rune, 0, pt.length)
	for _, p := range pt.pieces {
		buf = append(buf, pt.sourceSlice(p)...)
	}
	s := string(buf)
	pt.cachedStr = s
	pt.cachedVer = pt.version
	return s
}

// Substring returns the text in the rune range [start, end).
func (pt *PieceTable) Substring(start, end int) string {
	if start >= end {
		return ""
	}
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
				return pt.Substring(lineStart, cur)
			}
			lineNum++
			lineStart = cur + 1
		}
		cur++
	}
	if lineNum == n {
		return pt.Substring(lineStart, cur)
	}
	return ""
}

// LineCount returns the number of lines (at least 1 for non-empty content).
func (pt *PieceTable) LineCount() int {
	if pt.length == 0 {
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

// RuneAt returns the rune at the absolute rune position and ok=true. If pos
// is outside [0, Length()), it returns (0, false) so callers can branch on
// the bounds result rather than mis-interpreting a genuine NUL rune. This
// replaces the prior silent-0-return signature that masked cursor bugs.
func (pt *PieceTable) RuneAt(pos int) (rune, bool) {
	if pos < 0 || pos >= pt.length {
		return 0, false
	}
	offset := 0
	for _, p := range pt.pieces {
		if pos < offset+p.Length {
			src := pt.sourceBuffer(p.Source)
			return src[p.Offset+(pos-offset)], true
		}
		offset += p.Length
	}
	return 0, false
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
// (idx, offset) when necessary. Adjacent same-source pieces are coalesced.
func (pt *PieceTable) spliceInsert(idx, offset int, newPiece Piece) {
	// Append at end.
	if idx == len(pt.pieces) {
		pt.pieces = append(pt.pieces, newPiece)
		pt.tryCoalesce(len(pt.pieces) - 1)
		return
	}
	target := pt.pieces[idx]
	// Insert at the beginning of a piece.
	if offset == 0 {
		pt.pieces = slices.Insert(pt.pieces, idx, newPiece)
		pt.tryCoalesce(idx)
		return
	}
	// Insert at the end of a piece.
	if offset == target.Length {
		pt.pieces = slices.Insert(pt.pieces, idx+1, newPiece)
		pt.tryCoalesce(idx + 1)
		return
	}
	// Split target into left + newPiece + right.
	left := Piece{Source: target.Source, Offset: target.Offset, Length: offset}
	right := Piece{Source: target.Source, Offset: target.Offset + offset, Length: target.Length - offset}
	pt.pieces = slices.Replace(pt.pieces, idx, idx+1, left, newPiece, right)
	// left/right derive from the original piece — they can't merge with the
	// new Add-buffer piece, so coalescing is a no-op in the split case.
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
	pt.pieces = slices.Replace(pt.pieces, startIdx, end, keep...)
	// Coalesce at the splice boundary.
	if startIdx < len(pt.pieces) {
		pt.tryCoalesce(startIdx)
	}
}

// tryCoalesce merges the piece at idx with its left and right neighbours
// when they reference contiguous regions of the same source buffer.
func (pt *PieceTable) tryCoalesce(idx int) {
	// Merge with right neighbour first (may shift indices).
	if idx+1 < len(pt.pieces) {
		a, b := pt.pieces[idx], pt.pieces[idx+1]
		if a.Source == b.Source && a.Offset+a.Length == b.Offset {
			pt.pieces[idx].Length += b.Length
			pt.pieces = slices.Delete(pt.pieces, idx+1, idx+2)
		}
	}
	// Merge with left neighbour.
	if idx > 0 && idx < len(pt.pieces) {
		a, b := pt.pieces[idx-1], pt.pieces[idx]
		if a.Source == b.Source && a.Offset+a.Length == b.Offset {
			pt.pieces[idx-1].Length += b.Length
			pt.pieces = slices.Delete(pt.pieces, idx, idx+1)
		}
	}
}

// rangeHasNewline reports whether any rune in [pos, pos+length) is '\n'.
func (pt *PieceTable) rangeHasNewline(pos, length int) bool {
	end := pos + length
	cur := 0
	for _, p := range pt.pieces {
		pEnd := cur + p.Length
		if pEnd <= pos {
			cur = pEnd
			continue
		}
		if cur >= end {
			break
		}
		src := pt.sourceBuffer(p.Source)
		lo := max(pos-cur, 0)
		hi := min(end-cur, p.Length)
		for i := lo; i < hi; i++ {
			if src[p.Offset+i] == '\n' {
				return true
			}
		}
		cur = pEnd
	}
	return false
}

// containsNewline reports whether any rune in the slice is '\n'.
func containsNewline(runes []rune) bool {
	for _, r := range runes {
		if r == '\n' {
			return true
		}
	}
	return false
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
