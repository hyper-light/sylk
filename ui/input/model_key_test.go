package input

import (
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestModelShiftEnterInsertsNewline(t *testing.T) {
	m := New(theme.DefaultDark(), 8)
	m.SetText("hello")

	if cmd := actionNewline(m); cmd != nil {
		t.Fatalf("unexpected command for shift+enter/newline action: %v", cmd)
	}
	m.insertRunes([]rune("world"))

	if got := m.Text(); got != "hello\nworld" {
		t.Fatalf("Text() = %q, want %q", got, "hello\nworld")
	}
}

func TestActionSelUpAtTopRowSelectsToLineStart(t *testing.T) {
	m := New(theme.DefaultDark(), 8)
	m.SetSize(40, 4)
	m.SetText("hello\nworld")
	m.ClickAt(5, 0)

	if cmd := actionSelUp(m); cmd != nil {
		t.Fatalf("unexpected command for shift+up selection action: %v", cmd)
	}

	if m.cursorRow != 0 || m.cursorCol != 0 {
		t.Fatalf("cursor = (%d,%d), want (0,0)", m.cursorRow, m.cursorCol)
	}
	if !m.HasSelection() {
		t.Fatal("expected selection to be active")
	}
	if got := m.SelectedText(); got != "hello" {
		t.Fatalf("SelectedText() = %q, want %q", got, "hello")
	}
}

func TestExtendSelectionToStartsSelectionFromCurrentCursor(t *testing.T) {
	m := New(theme.DefaultDark(), 8)
	m.SetSize(40, 4)
	m.SetText("hello world")
	m.ClickAt(5, 0)

	m.ExtendSelectionTo(11, 0)

	if !m.HasSelection() {
		t.Fatal("expected selection to be active")
	}
	if got := m.SelectedText(); got != " world" {
		t.Fatalf("SelectedText() = %q, want %q", got, " world")
	}
}
