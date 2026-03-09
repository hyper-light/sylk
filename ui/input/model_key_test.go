package input

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestModelShiftEnterInsertsNewline(t *testing.T) {
	m := New(theme.DefaultDark(), 8)
	m.SetText("hello")

	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftEnter}); cmd != nil {
		t.Fatalf("unexpected command for shift+enter: %v", cmd)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("world")}); cmd != nil {
		t.Fatalf("unexpected command for rune insert: %v", cmd)
	}

	if got := m.Text(); got != "hello\nworld" {
		t.Fatalf("Text() = %q, want %q", got, "hello\nworld")
	}
}
