package input

import (
	"testing"
)

func toLines(strs ...string) [][]rune {
	lines := make([][]rune, len(strs))
	for i, s := range strs {
		lines[i] = []rune(s)
	}
	return lines
}

func TestWrapLineFitsWithinWidth(t *testing.T) {
	ws := computeWrap(toLines("hello"), 20)
	if ws.visualTotal != 1 {
		t.Fatalf("expected 1 visual line, got %d", ws.visualTotal)
	}
	if ws.segments[0][0] != (runeSpan{0, 5}) {
		t.Fatalf("unexpected span: %+v", ws.segments[0][0])
	}
}

func TestWrapLineExactlyAtWidth(t *testing.T) {
	ws := computeWrap(toLines("12345"), 5)
	if ws.visualTotal != 1 {
		t.Fatalf("expected 1 visual line, got %d", ws.visualTotal)
	}
}

func TestWrapLineExceedsWidthAtWordBoundary(t *testing.T) {
	ws := computeWrap(toLines("hello world"), 7)
	if ws.visualTotal != 2 {
		t.Fatalf("expected 2 visual lines, got %d", ws.visualTotal)
	}
	// "hello " fits in 7 runes, "world" on next line.
	sp0 := ws.segments[0][0]
	sp1 := ws.segments[0][1]
	if sp0.End-sp0.Start > 7 {
		t.Fatalf("first segment too wide: %+v", sp0)
	}
	line := []rune("hello world")
	seg1Text := string(line[sp1.Start:sp1.End])
	if seg1Text != "world" {
		t.Fatalf("expected second segment 'world', got %q", seg1Text)
	}
}

func TestWrapSingleWordExceedsWidth(t *testing.T) {
	ws := computeWrap(toLines("abcdefghij"), 4)
	// 10 runes at width 4: 4+4+2 = 3 visual lines.
	if ws.visualTotal != 3 {
		t.Fatalf("expected 3 visual lines, got %d", ws.visualTotal)
	}
	spans := ws.segments[0]
	if spans[0] != (runeSpan{0, 4}) {
		t.Fatalf("first span: %+v", spans[0])
	}
	if spans[1] != (runeSpan{4, 8}) {
		t.Fatalf("second span: %+v", spans[1])
	}
	if spans[2] != (runeSpan{8, 10}) {
		t.Fatalf("third span: %+v", spans[2])
	}
}

func TestWrapMultipleActualLinesMixed(t *testing.T) {
	lines := toLines("hello world", "hi")
	ws := computeWrap(lines, 7)
	// "hello world" wraps to 2 visual lines, "hi" fits in 1.
	if ws.visualTotal != 3 {
		t.Fatalf("expected 3 visual lines, got %d", ws.visualTotal)
	}
	if len(ws.segments[0]) != 2 {
		t.Fatalf("expected 2 segments for line 0, got %d", len(ws.segments[0]))
	}
	if len(ws.segments[1]) != 1 {
		t.Fatalf("expected 1 segment for line 1, got %d", len(ws.segments[1]))
	}
}

func TestWrapEmptyLine(t *testing.T) {
	ws := computeWrap(toLines(""), 10)
	if ws.visualTotal != 1 {
		t.Fatalf("expected 1 visual line for empty, got %d", ws.visualTotal)
	}
	if ws.segments[0][0] != (runeSpan{0, 0}) {
		t.Fatalf("unexpected span for empty: %+v", ws.segments[0][0])
	}
}

func TestWrapWidthOne(t *testing.T) {
	ws := computeWrap(toLines("abc"), 1)
	if ws.visualTotal != 3 {
		t.Fatalf("expected 3 visual lines at width 1, got %d", ws.visualTotal)
	}
}

func TestWrapEmptyInput(t *testing.T) {
	ws := computeWrap([][]rune{nil}, 10)
	if ws.visualTotal != 1 {
		t.Fatalf("expected 1 visual line for nil line, got %d", ws.visualTotal)
	}
}

func TestCursorMappingRoundtrip(t *testing.T) {
	lines := toLines("hello world foo", "bar")
	ws := computeWrap(lines, 7)

	// Test every position in the first line.
	for col := range len(lines[0]) + 1 {
		vr, vc := ws.actualToVisual(0, col)
		ar, ac := ws.visualToActual(vr, vc)
		if ar != 0 || ac != col {
			t.Fatalf("roundtrip failed for (0,%d): visual=(%d,%d) -> actual=(%d,%d)",
				col, vr, vc, ar, ac)
		}
	}

	// Test second actual line.
	for col := range len(lines[1]) + 1 {
		vr, vc := ws.actualToVisual(1, col)
		ar, ac := ws.visualToActual(vr, vc)
		if ar != 1 || ac != col {
			t.Fatalf("roundtrip failed for (1,%d): visual=(%d,%d) -> actual=(%d,%d)",
				col, vr, vc, ar, ac)
		}
	}
}

func TestActualRowStartVisual(t *testing.T) {
	lines := toLines("hello world", "bar")
	ws := computeWrap(lines, 7)

	if v := ws.actualRowStartVisual(0); v != 0 {
		t.Fatalf("expected row 0 starts at vRow 0, got %d", v)
	}
	// "hello world" wraps to 2 visual lines, so row 1 starts at vRow 2.
	if v := ws.actualRowStartVisual(1); v != 2 {
		t.Fatalf("expected row 1 starts at vRow 2, got %d", v)
	}
}

func TestVisualRowToSegment(t *testing.T) {
	lines := toLines("hello world", "bar")
	ws := computeWrap(lines, 7)

	row, seg := ws.visualRowToSegment(0)
	if row != 0 || seg != 0 {
		t.Fatalf("vRow 0: expected (0,0), got (%d,%d)", row, seg)
	}
	row, seg = ws.visualRowToSegment(1)
	if row != 0 || seg != 1 {
		t.Fatalf("vRow 1: expected (0,1), got (%d,%d)", row, seg)
	}
	row, seg = ws.visualRowToSegment(2)
	if row != 1 || seg != 0 {
		t.Fatalf("vRow 2: expected (1,0), got (%d,%d)", row, seg)
	}
}
