package mode

import (
	"testing"

	"github.com/adalundhe/sylk/ui/editor/buffer"
)

func TestNewSingleCursor(t *testing.T) {
	cs := NewSingleCursor(10, 2, 5)
	if cs.Len() != 1 {
		t.Fatalf("expected 1 cursor, got %d", cs.Len())
	}
	if !cs.IsSingle() {
		t.Fatal("expected IsSingle true")
	}
	p := cs.Primary()
	if p.Pos != 10 || p.Line != 2 || p.Col != 5 {
		t.Fatalf("primary = %+v, want {10, 2, 5}", p)
	}
}

func TestAdd(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	ok := cs.Add(10, 1, 5)
	if !ok {
		t.Fatal("Add should succeed")
	}
	if cs.Len() != 2 {
		t.Fatalf("expected 2 cursors, got %d", cs.Len())
	}
	if cs.IsSingle() {
		t.Fatal("expected IsSingle false")
	}
}

func TestAddDuplicate(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	ok := cs.Add(0, 0, 0) // Same position as primary.
	if ok {
		t.Fatal("Add duplicate should return false")
	}
	if cs.Len() != 1 {
		t.Fatalf("expected 1 cursor, got %d", cs.Len())
	}
}

func TestAddMaxCursors(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	for i := 1; i < maxCursors; i++ {
		cs.Add(i, 0, i)
	}
	if cs.Len() != maxCursors {
		t.Fatalf("expected %d cursors, got %d", maxCursors, cs.Len())
	}
	ok := cs.Add(maxCursors+1, 0, 0)
	if ok {
		t.Fatal("should reject when at maxCursors")
	}
}

func TestRemove(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	cs.Add(10, 1, 0)
	cs.Add(20, 2, 0)
	cs.Remove(10)
	if cs.Len() != 2 {
		t.Fatalf("expected 2 cursors, got %d", cs.Len())
	}
	// Cannot remove last cursor.
	cs.Remove(20)
	cs.Remove(0)
	if cs.Len() != 1 {
		t.Fatal("should not remove the last cursor")
	}
}

func TestClearSecondary(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	cs.Add(10, 1, 5)
	cs.Add(20, 2, 3)
	cs.ClearSecondary()
	if cs.Len() != 1 {
		t.Fatalf("expected 1 cursor after ClearSecondary, got %d", cs.Len())
	}
	if cs.Primary().Pos != 0 {
		t.Fatalf("primary should be at 0, got %d", cs.Primary().Pos)
	}
}

func TestAllReversed(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	cs.Add(10, 1, 0)
	cs.Add(20, 2, 0)
	rev := cs.AllReversed()
	if len(rev) != 3 {
		t.Fatalf("expected 3 reversed, got %d", len(rev))
	}
	if rev[0].Pos != 20 || rev[1].Pos != 10 || rev[2].Pos != 0 {
		t.Fatalf("unexpected reverse order: %v", rev)
	}
}

func TestHasLineAndCursorsOnLine(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	cs.Add(5, 0, 5) // Same line, different col.
	cs.Add(20, 2, 3)

	if !cs.HasLine(0) {
		t.Fatal("expected HasLine(0) true")
	}
	if !cs.HasLine(2) {
		t.Fatal("expected HasLine(2) true")
	}
	if cs.HasLine(1) {
		t.Fatal("expected HasLine(1) false")
	}
	cols := cs.CursorsOnLine(0)
	if len(cols) != 2 {
		t.Fatalf("expected 2 cursors on line 0, got %d", len(cols))
	}
	if cols[0] != 0 || cols[1] != 5 {
		t.Fatalf("unexpected cols on line 0: %v", cols)
	}
}

func TestAdjustAfterEditInsert(t *testing.T) {
	cs := NewSingleCursor(5, 0, 5)
	cs.Add(10, 1, 0)
	cs.Add(20, 2, 0)

	// Insert 3 chars at pos 7 — cursors at 10 and 20 should shift.
	cs.AdjustAfterEdit(7, 3)
	all := cs.All()
	if all[0].Pos != 5 {
		t.Fatalf("cursor at 5 should not shift, got %d", all[0].Pos)
	}
	// Find the cursor that was at 10.
	found := false
	for _, c := range all {
		if c.Pos == 13 {
			found = true
		}
	}
	if !found {
		t.Fatalf("cursor at 10 should shift to 13, got %v", all)
	}
}

func TestAdjustAfterEditDelete(t *testing.T) {
	cs := NewSingleCursor(5, 0, 5)
	cs.Add(8, 0, 8)
	cs.Add(20, 2, 0)

	// Delete 5 chars at pos 6 (range [6, 11)) — cursor at 8 collapses to 6.
	cs.AdjustAfterEdit(6, -5)
	all := cs.All()
	collapsedFound := false
	for _, c := range all {
		if c.Pos == 6 {
			collapsedFound = true
		}
	}
	if !collapsedFound {
		t.Fatalf("cursor at 8 should collapse to 6, got %v", all)
	}
	// Cursor at 20 should shift to 15.
	shifted := false
	for _, c := range all {
		if c.Pos == 15 {
			shifted = true
		}
	}
	if !shifted {
		t.Fatalf("cursor at 20 should shift to 15, got %v", all)
	}
}

func TestMergeOverlapping(t *testing.T) {
	cs := NewSingleCursor(10, 0, 10)
	cs.Add(20, 1, 0)
	// Force two cursors to same position.
	cs.cursors[0].Pos = 20
	cs.MergeOverlapping()
	if cs.Len() != 1 {
		t.Fatalf("expected 1 cursor after merge, got %d", cs.Len())
	}
}

func TestSnapshotAndRestore(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	cs.Add(10, 1, 0)
	cs.Add(20, 2, 0)
	snap := cs.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3-element snapshot, got %d", len(snap))
	}

	// Create a mini buffer to get a real LineIndex for Restore.
	pt := buffer.NewPieceTable("hello\nworld\nfoo bar")
	li := buffer.NewLineIndex(pt)

	cs2 := NewSingleCursor(0, 0, 0)
	cs2.Restore(snap, li)
	if cs2.Len() != 3 {
		t.Fatalf("expected 3 cursors after restore, got %d", cs2.Len())
	}
}

func TestSetNthAndShiftBelow(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	cs.Add(10, 1, 0)
	cs.Add(20, 2, 0)
	// Sorted: [0, 10, 20]
	// Reverse index 0 → forward index 2 → cursor at 20.
	cs.SetNth(0, 25, 2, 5)
	if cs.cursors[2].Pos != 25 {
		t.Fatalf("SetNth(0) should update last cursor, got %d", cs.cursors[2].Pos)
	}

	// ShiftBelow(20, 3) shifts cursors < 20 by +3.
	cs.ShiftBelow(20, 3)
	if cs.cursors[0].Pos != 3 {
		t.Fatalf("cursor at 0 should shift to 3, got %d", cs.cursors[0].Pos)
	}
	if cs.cursors[1].Pos != 13 {
		t.Fatalf("cursor at 10 should shift to 13, got %d", cs.cursors[1].Pos)
	}
}

func TestSortAndPrimaryPreservation(t *testing.T) {
	cs := NewSingleCursor(20, 2, 0)
	cs.Add(5, 0, 5)
	cs.Add(10, 1, 0)
	// Primary is at Pos 20, should still be at index 0 after sort.
	p := cs.Primary()
	if p.Pos != 20 {
		t.Fatalf("primary should be at 20, got %d", p.Pos)
	}
}

func TestClone(t *testing.T) {
	cs := NewSingleCursor(0, 0, 0)
	cs.Add(10, 1, 0)
	clone := cs.Clone()
	if clone.Len() != 2 {
		t.Fatalf("clone should have 2 cursors, got %d", clone.Len())
	}
	// Mutating clone should not affect original.
	clone.Add(20, 2, 0)
	if cs.Len() != 2 {
		t.Fatalf("original should still have 2 cursors, got %d", cs.Len())
	}
}
