package queue

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestViewHeightEmpty(t *testing.T) {
	q := New(MaxCapacity)
	if h := q.ViewHeight(); h != 0 {
		t.Fatalf("expected 0 for empty queue, got %d", h)
	}
}

func TestViewHeightOnePending(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "hello", "", "s1")
	if h := q.ViewHeight(); h != 1 {
		t.Fatalf("expected 1 for single pending, got %d", h)
	}
}

func TestViewHeightTwoPending(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")
	if h := q.ViewHeight(); h != 2 {
		t.Fatalf("expected 2 for two pending, got %d", h)
	}
}

func TestViewHeightThreePending(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")
	q.Enqueue("3", "c", "", "s1")
	if h := q.ViewHeight(); h != 2 {
		t.Fatalf("expected 2 for three pending, got %d", h)
	}
}

func TestViewHeightFourPending(t *testing.T) {
	q := New(MaxCapacity)
	for i := range 4 {
		q.Enqueue(string(rune('a'+i)), "text", "", "s1")
	}
	if h := q.ViewHeight(); h != 3 {
		t.Fatalf("expected 3 for four pending, got %d", h)
	}
}

func TestViewEmptyReturnsEmpty(t *testing.T) {
	q := New(MaxCapacity)
	v := q.View(80, 0, nil, theme.DarkPalette)
	if v != "" {
		t.Fatalf("expected empty view, got %q", v)
	}
}

func TestViewSingleEntry(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "implement feature X", "", "s1")

	v := q.View(80, 0, nil, theme.DarkPalette)
	if !strings.Contains(v, "[1]") {
		t.Fatal("expected position marker [1]")
	}
	if !strings.Contains(v, "implement feature X") {
		t.Fatal("expected prompt text")
	}
}

func TestViewWithTargetAgent(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "fix bug", "engineer", "s1")

	v := q.View(80, 0, nil, theme.DarkPalette)
	if !strings.Contains(v, "@engineer") {
		t.Fatal("expected @engineer in view")
	}
}

func TestViewOverflowSummary(t *testing.T) {
	q := New(MaxCapacity)
	for i := range 5 {
		q.Enqueue(string(rune('a'+i)), "text", "", "s1")
	}

	v := q.View(80, 0, nil, theme.DarkPalette)
	if !strings.Contains(v, "+3 more") {
		t.Fatalf("expected +3 more, got %q", v)
	}
}

func TestViewPausedRendering(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "test", "", "s1")
	q.SetPaused(true)

	v := q.View(80, 0, nil, theme.DarkPalette)
	if !strings.Contains(v, "paused") {
		t.Fatal("expected paused indicator")
	}
}

func TestViewWithGradient(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "animated text", "", "s1")

	grad := theme.DarkPalette.QueueGradient()
	v := q.View(80, 500*time.Millisecond, grad, theme.DarkPalette)
	// Should contain ANSI escape sequences from ripple rendering.
	if !strings.Contains(v, "\x1b[") {
		t.Fatal("expected ANSI color codes in animated view")
	}
}

func TestTruncateText(t *testing.T) {
	short := "hello"
	if got := truncateText(short, 10); got != "hello" {
		t.Fatalf("short text should be unchanged, got %q", got)
	}

	long := "this is a very long prompt that exceeds the limit"
	got := truncateText(long, 20)
	if len([]rune(got)) > 20 {
		t.Fatalf("truncated text too long: %q (%d runes)", got, len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ... suffix, got %q", got)
	}
}

func TestTruncateTextNewlines(t *testing.T) {
	text := "line one\nline two\nline three"
	got := truncateText(text, 48)
	if strings.Contains(got, "\n") {
		t.Fatal("newlines should be replaced with spaces")
	}
}

func TestViewSummaryShowsShortcuts(t *testing.T) {
	q := New(MaxCapacity)
	for i := range 5 {
		q.Enqueue(string(rune('a'+i)), "text", "", "s1")
	}

	v := q.View(120, 0, nil, theme.DarkPalette)
	if !strings.Contains(v, "Alt+Shift+Space") {
		t.Fatal("expected pause shortcut in summary for 4+ entries")
	}
	if !strings.Contains(v, "Alt+K") {
		t.Fatal("expected cancel shortcut in summary")
	}
}
