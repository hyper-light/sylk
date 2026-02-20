package mode

import (
	"slices"
	"sort"

	"github.com/adalundhe/sylk/ui/editor/buffer"
)

// maxCursors is the upper bound on simultaneous cursors to prevent unbounded growth.
const maxCursors = 512

// Cursor represents a single cursor position in the buffer.
type Cursor struct {
	Pos  int // Absolute rune offset in the buffer.
	Line int // 0-indexed line number (derived from LineIndex).
	Col  int // 0-indexed column within the line (derived from LineIndex).
}

// CursorSet manages an ordered collection of cursors. The first element
// (index 0) is always the primary cursor. All cursors are sorted ascending
// by Pos and deduplicated after every mutation.
type CursorSet struct {
	cursors     []Cursor      // Sorted ascending by Pos; [0] is primary.
	cursorLines map[int][]int // line → sorted column offsets; rebuilt on mutation.
}

// NewSingleCursor creates a CursorSet with exactly one cursor.
func NewSingleCursor(pos, line, col int) *CursorSet {
	cs := &CursorSet{
		cursors:     []Cursor{{Pos: pos, Line: line, Col: col}},
		cursorLines: map[int][]int{line: {col}},
	}
	return cs
}

// Primary returns the primary cursor (always the first element).
func (cs *CursorSet) Primary() Cursor { return cs.cursors[0] }

// SetPrimary updates the primary cursor's position. If the primary cursor
// moved to a new Pos, re-sort and rebuild the line map.
func (cs *CursorSet) SetPrimary(pos, line, col int) {
	old := cs.cursors[0]
	cs.cursors[0] = Cursor{Pos: pos, Line: line, Col: col}
	if old.Pos != pos {
		cs.sortAndDedup()
		cs.rebuildLineMap()
	} else if old.Line != line || old.Col != col {
		cs.rebuildLineMap()
	}
}

// Len returns the number of cursors.
func (cs *CursorSet) Len() int { return len(cs.cursors) }

// IsSingle reports whether there is exactly one cursor.
func (cs *CursorSet) IsSingle() bool { return len(cs.cursors) == 1 }

// Add inserts a new cursor at the given position. Returns false if the
// cursor already exists at that Pos or maxCursors would be exceeded.
func (cs *CursorSet) Add(pos, line, col int) bool {
	if len(cs.cursors) >= maxCursors {
		return false
	}
	for _, c := range cs.cursors {
		if c.Pos == pos {
			return false
		}
	}
	cs.cursors = append(cs.cursors, Cursor{Pos: pos, Line: line, Col: col})
	cs.sortAndDedup()
	cs.rebuildLineMap()
	return true
}

// Remove removes the cursor at the given Pos. The last remaining cursor
// cannot be removed.
func (cs *CursorSet) Remove(pos int) {
	if len(cs.cursors) <= 1 {
		return
	}
	n := 0
	for _, c := range cs.cursors {
		if c.Pos != pos {
			cs.cursors[n] = c
			n++
		}
	}
	if n == len(cs.cursors) {
		return // Not found.
	}
	cs.cursors = cs.cursors[:n]
	cs.rebuildLineMap()
}

// ClearSecondary removes all cursors except the primary.
func (cs *CursorSet) ClearSecondary() {
	if len(cs.cursors) <= 1 {
		return
	}
	primary := cs.cursors[0]
	cs.cursors = cs.cursors[:1]
	cs.cursors[0] = primary
	cs.rebuildLineMap()
}

// All returns all cursors in ascending Pos order.
func (cs *CursorSet) All() []Cursor {
	return cs.cursors
}

// AllReversed returns all cursors in descending Pos order (highest first).
// Used for reverse-document-order editing.
func (cs *CursorSet) AllReversed() []Cursor {
	out := make([]Cursor, len(cs.cursors))
	copy(out, cs.cursors)
	slices.Reverse(out)
	return out
}

// HasLine reports whether any cursor is on the given line. O(1) via map lookup.
func (cs *CursorSet) HasLine(line int) bool {
	_, ok := cs.cursorLines[line]
	return ok
}

// CursorsOnLine returns the sorted column offsets of all cursors on the
// given line. Returns nil if no cursor is on that line.
func (cs *CursorSet) CursorsOnLine(line int) []int {
	return cs.cursorLines[line]
}

// AdjustAfterEdit shifts all cursor positions at or after editPos by delta.
// For deletions (delta < 0), cursors inside the deleted range collapse to editPos.
func (cs *CursorSet) AdjustAfterEdit(editPos, delta int) {
	deleteEnd := editPos
	if delta < 0 {
		deleteEnd = editPos - delta // end of the deleted range (exclusive)
	}
	for i := range cs.cursors {
		c := &cs.cursors[i]
		if delta < 0 && c.Pos > editPos && c.Pos < deleteEnd {
			c.Pos = editPos
		} else if c.Pos >= editPos {
			c.Pos = max(c.Pos+delta, editPos)
		}
	}
}

// MergeOverlapping deduplicates cursors that ended up at the same position
// after edits. Preserves primary cursor identity.
func (cs *CursorSet) MergeOverlapping() {
	if len(cs.cursors) <= 1 {
		return
	}
	cs.sortAndDedup()
	cs.rebuildLineMap()
}

// Sync recomputes Line and Col for all cursors from the LineIndex.
func (cs *CursorSet) Sync(li *buffer.LineIndex) {
	for i := range cs.cursors {
		c := &cs.cursors[i]
		c.Line, c.Col = li.PosToLineCol(c.Pos)
	}
	cs.rebuildLineMap()
}

// Snapshot returns all cursor positions as a flat slice of ints.
// Format: [primary_pos, secondary1_pos, secondary2_pos, ...]
func (cs *CursorSet) Snapshot() []int {
	out := make([]int, len(cs.cursors))
	for i, c := range cs.cursors {
		out[i] = c.Pos
	}
	return out
}

// Restore restores cursor positions from a snapshot. The first position
// becomes the primary. Line/Col are recomputed from the LineIndex.
func (cs *CursorSet) Restore(positions []int, li *buffer.LineIndex) {
	if len(positions) == 0 {
		return
	}
	cs.cursors = make([]Cursor, len(positions))
	for i, pos := range positions {
		line, col := li.PosToLineCol(pos)
		cs.cursors[i] = Cursor{Pos: pos, Line: line, Col: col}
	}
	cs.sortAndDedup()
	cs.rebuildLineMap()
}

// SetNth updates the cursor at the given reverse-order index (0 = highest Pos).
// This is used during the reverse-iteration fan-out loop.
func (cs *CursorSet) SetNth(reverseIdx int, pos, line, col int) {
	// Reverse index: 0 maps to last element, 1 to second-to-last, etc.
	fwdIdx := len(cs.cursors) - 1 - reverseIdx
	if fwdIdx < 0 || fwdIdx >= len(cs.cursors) {
		return
	}
	cs.cursors[fwdIdx] = Cursor{Pos: pos, Line: line, Col: col}
}

// ShiftBelow shifts all cursors with Pos < threshold by delta.
// Used during reverse-order fan-out: edits at higher positions shift
// lower-position cursors.
func (cs *CursorSet) ShiftBelow(threshold, delta int) {
	for i := range cs.cursors {
		if cs.cursors[i].Pos < threshold {
			cs.cursors[i].Pos += delta
		}
	}
}

// BottomLine returns the maximum line number across all cursors.
func (cs *CursorSet) BottomLine() int {
	maxLine := cs.cursors[0].Line
	for _, c := range cs.cursors[1:] {
		if c.Line > maxLine {
			maxLine = c.Line
		}
	}
	return maxLine
}

// RemoveBottommost removes the cursor on the highest line that is not
// the primary cursor. Does nothing if only one cursor remains.
func (cs *CursorSet) RemoveBottommost() {
	if len(cs.cursors) <= 1 {
		return
	}
	maxIdx := -1
	maxLine := -1
	for i := 1; i < len(cs.cursors); i++ {
		if cs.cursors[i].Line > maxLine {
			maxLine = cs.cursors[i].Line
			maxIdx = i
		}
	}
	if maxIdx < 0 {
		return
	}
	cs.cursors[maxIdx] = cs.cursors[len(cs.cursors)-1]
	cs.cursors = cs.cursors[:len(cs.cursors)-1]
	cs.rebuildLineMap()
}

// Clone returns a deep copy of the CursorSet.
func (cs *CursorSet) Clone() *CursorSet {
	out := &CursorSet{
		cursors:     make([]Cursor, len(cs.cursors)),
		cursorLines: make(map[int][]int, len(cs.cursorLines)),
	}
	copy(out.cursors, cs.cursors)
	for k, v := range cs.cursorLines {
		dup := make([]int, len(v))
		copy(dup, v)
		out.cursorLines[k] = dup
	}
	return out
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// sortAndDedup sorts cursors by Pos ascending and removes duplicates.
// The primary cursor (index 0 before sort) retains its identity by
// tracking its Pos.
func (cs *CursorSet) sortAndDedup() {
	if len(cs.cursors) <= 1 {
		return
	}
	primaryPos := cs.cursors[0].Pos
	sort.Slice(cs.cursors, func(i, j int) bool {
		return cs.cursors[i].Pos < cs.cursors[j].Pos
	})
	// Deduplicate.
	n := 1
	for i := 1; i < len(cs.cursors); i++ {
		if cs.cursors[i].Pos != cs.cursors[i-1].Pos {
			cs.cursors[n] = cs.cursors[i]
			n++
		}
	}
	cs.cursors = cs.cursors[:n]
	// Ensure primary is still at index 0 by rotating if needed.
	for i, c := range cs.cursors {
		if c.Pos == primaryPos && i != 0 {
			// Swap primary to front.
			cs.cursors[0], cs.cursors[i] = cs.cursors[i], cs.cursors[0]
			break
		}
	}
}

// rebuildLineMap reconstructs cursorLines from the current cursor list.
func (cs *CursorSet) rebuildLineMap() {
	clear(cs.cursorLines)
	if cs.cursorLines == nil {
		cs.cursorLines = make(map[int][]int, len(cs.cursors))
	}
	for _, c := range cs.cursors {
		cs.cursorLines[c.Line] = append(cs.cursorLines[c.Line], c.Col)
	}
	for line := range cs.cursorLines {
		cols := cs.cursorLines[line]
		if len(cols) > 1 {
			sort.Ints(cols)
		}
	}
}
