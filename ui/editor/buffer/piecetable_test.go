package buffer

import (
	"errors"
	"testing"
)

// TestPieceTable_RuneAtBounds closes UI-08. RuneAt now returns (rune, bool);
// the bool is false for any out-of-range position instead of silently
// returning rune(0). The prior signature masked cursor bugs.
func TestPieceTable_RuneAtBounds(t *testing.T) {
	pt := NewPieceTable("abc")

	t.Run("InBounds", func(t *testing.T) {
		r, ok := pt.RuneAt(0)
		if !ok || r != 'a' {
			t.Fatalf("RuneAt(0) = (%q, %v); want ('a', true)", r, ok)
		}
		r, ok = pt.RuneAt(2)
		if !ok || r != 'c' {
			t.Fatalf("RuneAt(2) = (%q, %v); want ('c', true)", r, ok)
		}
	})

	t.Run("NegativePos", func(t *testing.T) {
		if r, ok := pt.RuneAt(-1); ok || r != 0 {
			t.Fatalf("RuneAt(-1) = (%q, %v); want (0, false)", r, ok)
		}
	})

	t.Run("AtLength", func(t *testing.T) {
		if r, ok := pt.RuneAt(3); ok || r != 0 {
			t.Fatalf("RuneAt(Length) = (%q, %v); want (0, false)", r, ok)
		}
	})

	t.Run("BeyondLength", func(t *testing.T) {
		if r, ok := pt.RuneAt(100); ok || r != 0 {
			t.Fatalf("RuneAt(100) = (%q, %v); want (0, false)", r, ok)
		}
	})

	t.Run("EmptyBuffer", func(t *testing.T) {
		empty := NewPieceTable("")
		if r, ok := empty.RuneAt(0); ok || r != 0 {
			t.Fatalf("RuneAt(0) on empty = (%q, %v); want (0, false)", r, ok)
		}
	})
}

// TestPieceTable_InsertBounds closes UI-08b. Insert returns
// ErrPositionOutOfBounds when pos is outside [0, Length()]. Appending at
// Length() is valid; anything beyond is rejected.
func TestPieceTable_InsertBounds(t *testing.T) {
	t.Run("AtStart", func(t *testing.T) {
		pt := NewPieceTable("bcd")
		if err := pt.Insert(0, "a"); err != nil {
			t.Fatalf("Insert(0) unexpected error: %v", err)
		}
		if pt.Content() != "abcd" {
			t.Fatalf("content = %q; want %q", pt.Content(), "abcd")
		}
	})

	t.Run("AtLength_Append", func(t *testing.T) {
		pt := NewPieceTable("abc")
		if err := pt.Insert(3, "d"); err != nil {
			t.Fatalf("Insert at Length() unexpected error: %v", err)
		}
		if pt.Content() != "abcd" {
			t.Fatalf("content = %q; want %q", pt.Content(), "abcd")
		}
	})

	t.Run("BeyondLength_Rejected", func(t *testing.T) {
		pt := NewPieceTable("abc")
		if err := pt.Insert(10, "x"); !errors.Is(err, ErrPositionOutOfBounds) {
			t.Fatalf("Insert(10): err = %v; want ErrPositionOutOfBounds", err)
		}
		if pt.Content() != "abc" {
			t.Fatalf("content must be unchanged after rejected insert; got %q", pt.Content())
		}
	})

	t.Run("NegativePos_Rejected", func(t *testing.T) {
		pt := NewPieceTable("abc")
		if err := pt.Insert(-1, "x"); !errors.Is(err, ErrPositionOutOfBounds) {
			t.Fatalf("Insert(-1): err = %v; want ErrPositionOutOfBounds", err)
		}
	})

	t.Run("EmptyText_NoOp", func(t *testing.T) {
		pt := NewPieceTable("abc")
		if err := pt.Insert(1, ""); err != nil {
			t.Fatalf("Insert(,\"\") unexpected error: %v", err)
		}
		if pt.Content() != "abc" {
			t.Fatalf("empty insert must not mutate; got %q", pt.Content())
		}
	})
}

// TestPieceTable_DeleteBounds closes UI-08b for Delete. Range must be fully
// contained in [0, Length()); otherwise ErrPositionOutOfBounds.
func TestPieceTable_DeleteBounds(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		pt := NewPieceTable("abcdef")
		if err := pt.Delete(1, 2); err != nil {
			t.Fatalf("Delete(1,2) unexpected error: %v", err)
		}
		if pt.Content() != "adef" {
			t.Fatalf("content = %q; want %q", pt.Content(), "adef")
		}
	})

	t.Run("NonPositiveLength_NoOp", func(t *testing.T) {
		pt := NewPieceTable("abc")
		if err := pt.Delete(0, 0); err != nil {
			t.Fatalf("Delete(0,0) unexpected error: %v", err)
		}
		if err := pt.Delete(0, -5); err != nil {
			t.Fatalf("Delete(0,-5) unexpected error: %v", err)
		}
		if pt.Content() != "abc" {
			t.Fatalf("content must be unchanged; got %q", pt.Content())
		}
	})

	t.Run("NegativePos_Rejected", func(t *testing.T) {
		pt := NewPieceTable("abc")
		if err := pt.Delete(-1, 1); !errors.Is(err, ErrPositionOutOfBounds) {
			t.Fatalf("Delete(-1,1): err = %v; want ErrPositionOutOfBounds", err)
		}
		if pt.Content() != "abc" {
			t.Fatalf("content must be unchanged after rejected delete; got %q", pt.Content())
		}
	})

	t.Run("RangeBeyondLength_Rejected", func(t *testing.T) {
		pt := NewPieceTable("abc")
		if err := pt.Delete(2, 5); !errors.Is(err, ErrPositionOutOfBounds) {
			t.Fatalf("Delete(2,5): err = %v; want ErrPositionOutOfBounds", err)
		}
		if pt.Content() != "abc" {
			t.Fatalf("content must be unchanged; got %q", pt.Content())
		}
	})

	t.Run("AtLengthExactly_Valid", func(t *testing.T) {
		pt := NewPieceTable("abc")
		if err := pt.Delete(0, 3); err != nil {
			t.Fatalf("Delete(0,3) deleting whole buffer: err = %v", err)
		}
		if pt.Content() != "" {
			t.Fatalf("content = %q; want empty", pt.Content())
		}
	})
}

// TestPieceTable_VersionOnRejection ensures rejected operations do NOT bump
// the version counter. Any code that relies on version changes to detect
// content mutation would otherwise get spurious invalidations.
func TestPieceTable_VersionOnRejection(t *testing.T) {
	pt := NewPieceTable("abc")
	v0 := pt.Version()

	if err := pt.Insert(-1, "x"); !errors.Is(err, ErrPositionOutOfBounds) {
		t.Fatalf("want ErrPositionOutOfBounds on Insert(-1), got %v", err)
	}
	if err := pt.Delete(-1, 1); !errors.Is(err, ErrPositionOutOfBounds) {
		t.Fatalf("want ErrPositionOutOfBounds on Delete(-1,1), got %v", err)
	}

	if pt.Version() != v0 {
		t.Fatalf("version must not change on rejected mutations: was %d, now %d", v0, pt.Version())
	}
}
