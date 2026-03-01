package sylkdir

import "testing"

func TestBuildLineIndex(t *testing.T) {
	content := "line1\nline2\nline3\n"
	li := BuildLineIndex(content)

	// Trailing newline does not create an extra line.
	if li.LineCount() != 3 {
		t.Errorf("LineCount: got %d, want 3", li.LineCount())
	}

	// Line 1 starts at byte 0
	if li.starts[1] != 0 {
		t.Errorf("line 1 start: got %d, want 0", li.starts[1])
	}
	// Line 2 starts at byte 6
	if li.starts[2] != 6 {
		t.Errorf("line 2 start: got %d, want 6", li.starts[2])
	}
	// Line 3 starts at byte 12
	if li.starts[3] != 12 {
		t.Errorf("line 3 start: got %d, want 12", li.starts[3])
	}
}

func TestLineIndexExtract(t *testing.T) {
	content := "aaa\nbbb\nccc\nddd\neee\n"
	li := BuildLineIndex(content)

	got := li.Extract(content, 2, 4)
	want := "bbb\nccc\nddd\n"
	if got != want {
		t.Errorf("Extract(2,4): got %q, want %q", got, want)
	}
}

func TestLineIndexExtractWithWindow(t *testing.T) {
	content := "L1\nL2\nL3\nL4\nL5\nL6\nL7\n"
	li := BuildLineIndex(content)

	text, ws, we := li.ExtractWithWindow(content, 4, 4, 2)

	if ws != 2 {
		t.Errorf("WindowStart: got %d, want 2", ws)
	}
	if we != 6 {
		t.Errorf("WindowEnd: got %d, want 6", we)
	}

	want := "L2\nL3\nL4\nL5\nL6\n"
	if text != want {
		t.Errorf("ExtractWithWindow: got %q, want %q", text, want)
	}
}

func TestLineIndexClampBounds(t *testing.T) {
	content := "A\nB\nC\n"
	li := BuildLineIndex(content)

	// Padding exceeds document bounds — should clamp.
	text, ws, we := li.ExtractWithWindow(content, 1, 1, 100)

	if ws != 1 {
		t.Errorf("WindowStart: got %d, want 1", ws)
	}
	if we != li.LineCount() {
		t.Errorf("WindowEnd: got %d, want %d", we, li.LineCount())
	}
	if text != content {
		t.Errorf("text: got %q, want %q", text, content)
	}
}

func TestLineIndexEmptyContent(t *testing.T) {
	li := BuildLineIndex("")

	if li.LineCount() != 0 {
		t.Errorf("LineCount: got %d, want 0", li.LineCount())
	}

	got := li.Extract("", 1, 1)
	if got != "" {
		t.Errorf("Extract on empty: got %q, want empty", got)
	}
}

func TestLineIndexSingleLine(t *testing.T) {
	content := "hello world"
	li := BuildLineIndex(content)

	if li.LineCount() != 1 {
		t.Errorf("LineCount: got %d, want 1", li.LineCount())
	}

	got := li.Extract(content, 1, 1)
	if got != content {
		t.Errorf("Extract: got %q, want %q", got, content)
	}
}

func TestClampLineMin(t *testing.T) {
	if got := clampLineMin(5, 3); got != 2 {
		t.Errorf("clampLineMin(5,3): got %d, want 2", got)
	}
	if got := clampLineMin(2, 5); got != 1 {
		t.Errorf("clampLineMin(2,5): got %d, want 1", got)
	}
	if got := clampLineMin(1, 0); got != 1 {
		t.Errorf("clampLineMin(1,0): got %d, want 1", got)
	}
}

func TestClampLineMax(t *testing.T) {
	if got := clampLineMax(5, 3, 10); got != 8 {
		t.Errorf("clampLineMax(5,3,10): got %d, want 8", got)
	}
	if got := clampLineMax(5, 100, 10); got != 10 {
		t.Errorf("clampLineMax(5,100,10): got %d, want 10", got)
	}
}
