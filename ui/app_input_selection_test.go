package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHandleInputPanelPress_ShiftClickExtendsSelection(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 80, Height: 24}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	app.input.SetText("hello world")
	app.recalcLayout()
	app.input.ClickAt(5, 0)

	inputTop := app.height - app.prevInputH - statusBarHeight
	handled := app.handleInputPanelPress(tea.MouseMsg{
		X:      12,
		Y:      inputTop + 1,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		Shift:  true,
	})
	if !handled {
		t.Fatal("expected input panel press to be handled")
	}
	if !app.input.HasSelection() {
		t.Fatal("expected shift+click to activate a selection")
	}
	if got := app.input.SelectedText(); got != " world" {
		t.Fatalf("SelectedText() = %q, want %q", got, " world")
	}
	if !app.inputMouseDown {
		t.Fatal("expected shift+click to keep drag selection armed")
	}
}
