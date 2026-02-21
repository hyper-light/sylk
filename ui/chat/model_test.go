package chat

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestPushEntryInvalidatesCachedView(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)

	comp, _ := m.Update(msg.StreamStartMsg{SessionID: "s1", CorrelationID: "c1"})
	m = comp.(*Model)
	comp, _ = m.Update(msg.StreamChunkMsg{SessionID: "s1", CorrelationID: "c1", Text: "first"})
	m = comp.(*Model)
	comp, _ = m.Update(msg.StreamCompleteMsg{SessionID: "s1", CorrelationID: "c1"})
	m = comp.(*Model)

	firstView := m.View()
	if !strings.Contains(firstView, "first") {
		t.Fatalf("expected first view to contain initial content")
	}

	m.PushEntry(&ChatEntry{
		ID:      "u2",
		Source:  SourceUser,
		Content: "second",
		Height:  -1,
	})

	secondView := m.View()
	if firstView == secondView {
		t.Fatalf("expected view to change after PushEntry")
	}
	if !strings.Contains(secondView, "second") {
		t.Fatalf("expected updated view to contain pushed content")
	}
}
