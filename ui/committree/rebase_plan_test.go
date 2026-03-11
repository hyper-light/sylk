package committree

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHandleRebasePlanKeyEscapeUnfocusesToolbarBeforeExit(t *testing.T) {
	m := &Model{
		mode:           viewRebasePlan,
		height:         20,
		toolbarFocused: true,
		rebasePlan: []rebasePlanEntry{
			{action: 0, hash: "abc1234", subject: "first"},
		},
	}

	m.handleRebasePlanKey(tea.KeyMsg{Type: tea.KeyEsc})

	if m.toolbarFocused {
		t.Fatal("expected toolbar focus to clear")
	}
	if m.mode != viewRebasePlan {
		t.Fatalf("mode = %v, want %v", m.mode, viewRebasePlan)
	}
}

func TestHandleRebasePlanKeyCapitalJReordersVisibleEntry(t *testing.T) {
	m := &Model{
		height: 20,
		rebasePlan: []rebasePlanEntry{
			{hash: "aaa1111", subject: "first"},
			{hash: "bbb2222", subject: "second"},
		},
	}

	m.handleRebasePlanKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})

	if got := m.rebasePlan[0].hash; got != "bbb2222" {
		t.Fatalf("first hash = %q, want %q", got, "bbb2222")
	}
	if got := m.rebaseCursor; got != 1 {
		t.Fatalf("cursor = %d, want %d", got, 1)
	}
}

func TestHandleRebasePlanKeySlashActivatesFilter(t *testing.T) {
	m := &Model{
		height: 20,
		rebasePlan: []rebasePlanEntry{
			{hash: "aaa1111", subject: "first"},
		},
		toolbarFocused: true,
	}

	m.handleRebasePlanKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	if !m.rebaseFilterActive {
		t.Fatal("expected rebase filter to activate")
	}
	if m.toolbarFocused {
		t.Fatal("expected toolbar focus to clear when filter activates")
	}
}
