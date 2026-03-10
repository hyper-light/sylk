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
