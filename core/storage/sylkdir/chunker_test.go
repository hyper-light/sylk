package sylkdir

import (
	"testing"
)

func TestBuildLineIndex(t *testing.T) {
	content := []byte("line1\nline2\nline3\n")
	li := buildLineIndex(content)

	if li.lineCount() != 3 {
		t.Fatalf("lineCount: got %d, want 3", li.lineCount())
	}
	if li.byteAt(1) != 0 {
		t.Errorf("line 1 start: got %d, want 0", li.byteAt(1))
	}
	if li.byteAt(2) != 6 {
		t.Errorf("line 2 start: got %d, want 6", li.byteAt(2))
	}
	if li.byteAt(3) != 12 {
		t.Errorf("line 3 start: got %d, want 12", li.byteAt(3))
	}
}

func TestLineAt(t *testing.T) {
	content := []byte("ab\ncd\nef\n")
	li := buildLineIndex(content)

	cases := []struct {
		offset uint32
		want   uint32
	}{
		{0, 1}, {1, 1}, {2, 1},
		{3, 2}, {4, 2}, {5, 2},
		{6, 3}, {7, 3}, {8, 3},
	}

	for _, tc := range cases {
		got := li.lineAt(tc.offset)
		if got != tc.want {
			t.Errorf("lineAt(%d): got %d, want %d", tc.offset, got, tc.want)
		}
	}
}

func TestLineAtEndExclusive(t *testing.T) {
	content := []byte("ab\ncd\nef\n")
	li := buildLineIndex(content)

	if got := li.lineAtEnd(3); got != 1 {
		t.Errorf("lineAtEnd(3): got %d, want 1", got)
	}
	if got := li.lineAtEnd(6); got != 2 {
		t.Errorf("lineAtEnd(6): got %d, want 2", got)
	}
	if got := li.lineAtEnd(9); got != 3 {
		t.Errorf("lineAtEnd(9): got %d, want 3", got)
	}
}
