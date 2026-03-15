package chat

import (
	"errors"
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

func TestConcurrentStreamThinkingAnimatesSecondaryPipelineEntry(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("architect")

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "architect",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "c2",
		AgentID:       "task_auth_checkout:inspector-pipeline",
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil {
		t.Fatal("expected concurrent stream entry")
	}
	initialText := last.ThinkingText
	wantStatus := agentThinkingMessages["inspector"][0]
	if last.ThinkingStatus != wantStatus {
		t.Fatalf("initial thinking status = %q, want %q", last.ThinkingStatus, wantStatus)
	}

	comp, _ = m.Update(msg.DecorTickMsg{Time: time.Now().Add(400 * time.Millisecond)})
	m = comp.(*Model)

	last = m.history.Last()
	if last == nil {
		t.Fatal("expected concurrent stream entry after tick")
	}
	if last.ThinkingText == initialText {
		t.Fatalf("expected secondary stream thinking text to animate, got %q", last.ThinkingText)
	}
}

func TestStreamStartUsesAgentTypeForPipelineBadge(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "a8c45a7f",
		AgentType:     "tester-pipeline",
		TaskName:      "Implement Cli Module",
		TaskSlug:      "implement-cli-module",
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil {
		t.Fatal("expected streaming entry")
	}
	if last.AgentType != "tester-pipeline" {
		t.Fatalf("AgentType = %q, want tester-pipeline", last.AgentType)
	}
	if got := badgeLabel(last); got != "Implement Cli Module: Tester" {
		t.Fatalf("badgeLabel = %q, want %q", got, "Implement Cli Module: Tester")
	}
}

func TestDuplicateStartDoesNotResetProgressOnlyStreamSlot(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "inspector",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "inspector",
		Message:       "Reviewing carefully...",
	})
	m = comp.(*Model)

	before := m.history.Last()
	if before == nil {
		t.Fatal("expected streaming entry")
	}
	beforeText := before.ThinkingText
	beforeStatus := before.ThinkingStatus

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "inspector",
	})
	m = comp.(*Model)

	after := m.history.Last()
	if after == nil {
		t.Fatal("expected streaming entry after duplicate start")
	}
	if after.ThinkingStatus != beforeStatus {
		t.Fatalf("thinking status = %q, want %q", after.ThinkingStatus, beforeStatus)
	}
	if after.ThinkingText != beforeText {
		t.Fatalf("thinking text = %q, want %q", after.ThinkingText, beforeText)
	}
}

func TestFormatErrorForChat_BugReportError(t *testing.T) {
	raw := `planning protocol: architect llm: anthropic stream: received error while streaming: {"type":"error","error":{"details":null,"type":"overloaded_error","message":"Overloaded"},"request_id":"req_011CYeT1GYt7Kg27RdtV9zeo"} (conversation unavailable: architect planner: anthropic stream: received error while streaming: {"type":"error","error":{"details":null,"type":"overloaded_error","message":"Overloaded"},"request_id":"req_011CYeSzHBJdCParu7tGNPhn"})`
	got := formatErrorForChat(errors.New(raw))
	want := "Overloaded (overloaded_error) [req_011CYeT1GYt7Kg27RdtV9zeo]"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestFormatErrorForChat_PlainError(t *testing.T) {
	got := formatErrorForChat(errors.New("connection timeout"))
	if got != "connection timeout" {
		t.Fatalf("expected plain message, got %q", got)
	}
}

func TestHandlePlanUpdate_DoesNotDuplicateEmbeddedReadyPlan(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("architect")

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-plan",
		AgentID:       "architect",
	})
	m = comp.(*Model)

	update := msg.PlanUpdateMsg{
		PlanID:        "plan-1",
		CorrelationID: "corr-plan",
		Status:        "ready",
		Tasks: []msg.PlanTaskSnapshot{
			{ID: "task-1", Name: "Create CLI", AgentType: "engineer"},
		},
	}
	m.HandlePlanUpdate(update)
	m.HandlePlanUpdate(update)

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-plan",
		AgentID:           "architect",
		AuthoritativeText: "Plan ready for approval.",
	})
	m = comp.(*Model)

	if m.history.Len() != 1 {
		t.Fatalf("history len = %d, want 1", m.history.Len())
	}
	view := m.View()
	if strings.Count(view, "## Plan") > 1 {
		t.Fatalf("expected embedded plan to render once, got view %q", view)
	}
}

func TestHandleToolCallEvent_AttachesToCompletedEntryByCorrelation(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("architect")
	m.FinishThinking(&ChatEntry{
		ID:            "resp-1",
		Timestamp:     time.Now(),
		CorrelationID: "corr-tool",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Done.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-tool",
		ToolName:      "read_file",
		ArgsSummary:   "path=README.md",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-tool",
		ToolName:      "read_file",
		Phase:         1,
		Duration:      250 * time.Millisecond,
		Success:       true,
		Output:        "ok",
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil {
		t.Fatal("expected completed entry")
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(last.ToolCalls))
	}
	record := last.ToolCalls[0]
	if record.ToolName != "read_file" {
		t.Fatalf("tool name = %q, want read_file", record.ToolName)
	}
	if !record.Completed || !record.Success {
		t.Fatalf("tool completion = %+v, want completed success", record)
	}
	if record.Output != "ok" {
		t.Fatalf("tool output = %q, want ok", record.Output)
	}
}
