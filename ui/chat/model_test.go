package chat

import (
	"strings"
	"testing"
	"time"

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

func TestMuteThinkingSetsMutedColor(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("guide")
	m.MuteThinking("")

	last := m.history.Last()
	if last == nil {
		t.Fatal("expected thinking entry")
	}
	if last.ThinkingColor != string(m.theme.Palette.Muted) {
		t.Fatalf("expected muted thinking color %q, got %q", string(m.theme.Palette.Muted), last.ThinkingColor)
	}
}

func TestStreamProgressUpdatesThinkingText(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("architect")

	comp, _ := m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "architect",
		Current:       2,
		Total:         6,
		Message:       "Consulting available knowledge agents...",
	})
	m = comp.(*Model)

	if m.retryText != "Consulting available knowledge agents..." {
		t.Fatalf("retryText = %q", m.retryText)
	}
	comp, _ = m.Update(msg.DecorTickMsg{Time: time.Now().Add(thinkingProgressMinInterval)})
	m = comp.(*Model)
	last := m.history.Last()
	if last == nil {
		t.Fatal("expected thinking entry")
	}
	if last.AgentType != "architect" {
		t.Fatalf("expected thinking agent type architect, got %q", last.AgentType)
	}
	if !strings.Contains(last.ThinkingStatus, "Consulting available knowledge agents...") {
		t.Fatalf("expected immediate thinking status update, got %q", last.ThinkingStatus)
	}
}

func TestStreamProgressSanitizesThinkingMessage(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("guide")

	comp, _ := m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "guide",
		Message:       "line one\r\nline two\t\x00",
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.DecorTickMsg{Time: time.Now().Add(thinkingProgressMinInterval)})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil {
		t.Fatal("expected thinking entry")
	}
	if strings.Contains(last.ThinkingStatus, "\n") || strings.Contains(last.ThinkingStatus, "\r") {
		t.Fatalf("expected single-line thinking status, got %q", last.ThinkingStatus)
	}
	if strings.Contains(last.ThinkingStatus, "\x00") {
		t.Fatalf("expected control chars removed, got %q", last.ThinkingStatus)
	}
	if !strings.Contains(last.ThinkingStatus, "line one line two") {
		t.Fatalf("expected normalized progress status, got %q", last.ThinkingStatus)
	}
}
