package input

import (
	"testing"
)

func TestOrderedRange_ForwardSelection(t *testing.T) {
	s := selection{anchorRow: 0, anchorCol: 2, active: true}
	sr, sc, er, ec := s.orderedRange(0, 5)
	if sr != 0 || sc != 2 || er != 0 || ec != 5 {
		t.Errorf("got (%d,%d)-(%d,%d), want (0,2)-(0,5)", sr, sc, er, ec)
	}
}

func TestOrderedRange_BackwardSelection(t *testing.T) {
	s := selection{anchorRow: 1, anchorCol: 5, active: true}
	sr, sc, er, ec := s.orderedRange(0, 3)
	if sr != 0 || sc != 3 || er != 1 || ec != 5 {
		t.Errorf("got (%d,%d)-(%d,%d), want (0,3)-(1,5)", sr, sc, er, ec)
	}
}

func TestOrderedRange_SamePosition(t *testing.T) {
	s := selection{anchorRow: 2, anchorCol: 4, active: true}
	sr, sc, er, ec := s.orderedRange(2, 4)
	if sr != 2 || sc != 4 || er != 2 || ec != 4 {
		t.Errorf("got (%d,%d)-(%d,%d), want (2,4)-(2,4)", sr, sc, er, ec)
	}
}

func TestExtractText_SingleLine(t *testing.T) {
	lines := [][]rune{[]rune("hello world")}
	s := selection{anchorRow: 0, anchorCol: 0, active: true}
	got := s.extractText(lines, 0, 5)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestExtractText_MultiLine(t *testing.T) {
	lines := [][]rune{
		[]rune("first line"),
		[]rune("second line"),
		[]rune("third line"),
	}
	s := selection{anchorRow: 0, anchorCol: 6, active: true}
	got := s.extractText(lines, 2, 5)
	want := "line\nsecond line\nthird"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractText_BackwardMultiLine(t *testing.T) {
	lines := [][]rune{
		[]rune("aaa"),
		[]rune("bbb"),
	}
	s := selection{anchorRow: 1, anchorCol: 2, active: true}
	got := s.extractText(lines, 0, 1)
	want := "aa\nbb"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeleteRange_SingleLine(t *testing.T) {
	lines := [][]rune{[]rune("hello world")}
	s := selection{anchorRow: 0, anchorCol: 5, active: true}
	newLines, row, col := s.deleteRange(lines, 0, 11)
	got := string(newLines[0])
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if row != 0 || col != 5 {
		t.Errorf("cursor at (%d,%d), want (0,5)", row, col)
	}
}

func TestDeleteRange_MultiLine(t *testing.T) {
	lines := [][]rune{
		[]rune("first"),
		[]rune("second"),
		[]rune("third"),
	}
	s := selection{anchorRow: 0, anchorCol: 3, active: true}
	newLines, row, col := s.deleteRange(lines, 2, 3)
	if len(newLines) != 1 {
		t.Fatalf("got %d lines, want 1", len(newLines))
	}
	got := string(newLines[0])
	want := "firrd"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if row != 0 || col != 3 {
		t.Errorf("cursor at (%d,%d), want (0,3)", row, col)
	}
}

func TestDeleteRange_FullContent(t *testing.T) {
	lines := [][]rune{
		[]rune("abc"),
		[]rune("def"),
	}
	s := selection{anchorRow: 0, anchorCol: 0, active: true}
	newLines, row, col := s.deleteRange(lines, 1, 3)
	if len(newLines) != 1 {
		t.Fatalf("got %d lines, want 1", len(newLines))
	}
	got := string(newLines[0])
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if row != 0 || col != 0 {
		t.Errorf("cursor at (%d,%d), want (0,0)", row, col)
	}
}

func TestContainsPos(t *testing.T) {
	s := selection{anchorRow: 0, anchorCol: 2, active: true}
	tests := []struct {
		qRow, qCol int
		want       bool
	}{
		{0, 1, false}, // before start
		{0, 2, true},  // at start
		{0, 3, true},  // inside
		{0, 4, true},  // just before end
		{0, 5, false}, // at end (exclusive)
		{0, 6, false}, // after end
	}
	for _, tt := range tests {
		got := s.containsPos(0, 5, tt.qRow, tt.qCol)
		if got != tt.want {
			t.Errorf("containsPos(0,5, %d,%d) = %v, want %v", tt.qRow, tt.qCol, got, tt.want)
		}
	}
}

func TestContainsPos_MultiLine(t *testing.T) {
	s := selection{anchorRow: 0, anchorCol: 3, active: true}
	tests := []struct {
		qRow, qCol int
		want       bool
	}{
		{0, 2, false},
		{0, 3, true},
		{1, 0, true},  // middle line always inside
		{1, 99, true}, // middle line any col
		{2, 0, true},
		{2, 4, true},
		{2, 5, false}, // at cursor (end, exclusive)
		{3, 0, false}, // past end
	}
	for _, tt := range tests {
		got := s.containsPos(2, 5, tt.qRow, tt.qCol)
		if got != tt.want {
			t.Errorf("containsPos(2,5, %d,%d) = %v, want %v", tt.qRow, tt.qCol, got, tt.want)
		}
	}
}
