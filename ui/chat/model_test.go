package chat

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
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

func TestFinishThinking_FinalizesPendingToolCalls(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("engineer")

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-finish-tool",
		ToolCallKey:   "tool-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=README.md",
		Phase:         0,
		StartedAt:     time.Now().Add(-120 * time.Millisecond),
	})
	m = comp.(*Model)

	m.FinishThinking(&ChatEntry{
		ID:            "finish-entry",
		Timestamp:     time.Now(),
		CorrelationID: "corr-finish-tool",
		Source:        SourceAgent,
		AgentType:     "engineer",
		Content:       "Done.",
		Height:        -1,
	})

	entry := findEntryByCorrelation(m, "corr-finish-tool")
	if entry == nil || len(entry.ToolCalls) != 1 {
		t.Fatalf("expected one tool call row after FinishThinking, got %+v", entry)
	}
	if !entry.ToolCalls[0].Completed || !entry.ToolCalls[0].SyntheticCompletion {
		t.Fatalf("finish-thinking tool row = %+v, want synthetically completed row", entry.ToolCalls[0])
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

func TestHandleActivity_AllowsChatVisibleSuccessEvents(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	comp, _ := m.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt-1",
			EventType: events.EventTypeSuccess,
			Timestamp: time.Now(),
			AgentID:   "orchestrator",
			Content:   "Operational transform merged task auth-checkout into the global VFS.",
			Data: map[string]any{
				"chat_visible": true,
				"agent_type":   "orchestrator",
			},
		},
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil {
		t.Fatal("expected chat entry for chat-visible success event")
	}
	if last.Source != SourceSystem {
		t.Fatalf("source = %v, want %v", last.Source, SourceSystem)
	}
	if !strings.Contains(last.Content, "Operational transform merged") {
		t.Fatalf("content = %q, want merge message", last.Content)
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

func TestHandleToolCallEvent_MatchesGenericToolCompletionByToolCallKey(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "resp-generic-keys",
		Timestamp:     time.Now(),
		CorrelationID: "corr-generic-keys",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Done.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-generic-keys",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=A.md",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-generic-keys",
		ToolCallKey:   "read-2",
		ToolName:      "read_file",
		ArgsSummary:   "path=B.md",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-generic-keys",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		Phase:         1,
		Duration:      25 * time.Millisecond,
		Success:       true,
		Output:        "A",
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || len(last.ToolCalls) != 2 {
		t.Fatalf("expected two generic tool rows, got %+v", last)
	}
	if !last.ToolCalls[0].Completed || last.ToolCalls[0].Output != "A" {
		t.Fatalf("first generic tool row = %+v, want completed output A", last.ToolCalls[0])
	}
	if last.ToolCalls[1].Completed {
		t.Fatalf("second generic tool row = %+v, want still pending", last.ToolCalls[1])
	}
}

func TestHandleToolCallEvent_MatchesGenericToolCompletionWhenKeysDifferButArgsMatch(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "resp-generic-args",
		Timestamp:     time.Now(),
		CorrelationID: "corr-generic-args",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Done.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-generic-args",
		ToolCallKey:   `sig:read_file` + "\x00" + `{"path":"README.md","line":1}`,
		ToolName:      "read_file",
		FullArgs:      "{\n  \"path\": \"README.md\",\n  \"line\": 1\n}",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-generic-args",
		ToolCallKey:   `sig:read_file` + "\x00" + "{\n  \"line\": 1,\n  \"path\": \"README.md\"\n}",
		ToolName:      "read_file",
		FullArgs:      `{"line":1,"path":"README.md"}`,
		Phase:         1,
		Duration:      25 * time.Millisecond,
		Success:       true,
		Output:        "ok",
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || len(last.ToolCalls) != 1 {
		t.Fatalf("expected one generic tool row, got %+v", last)
	}
	if !last.ToolCalls[0].Completed || last.ToolCalls[0].Output != "ok" {
		t.Fatalf("generic tool row = %+v, want completed output ok", last.ToolCalls[0])
	}
}

func TestHandleToolCallEvent_BackfillsArgsFromCompletionWhenStartWasEmpty(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "resp-web-search-backfill",
		Timestamp:     time.Now(),
		CorrelationID: "corr-web-search-backfill",
		Source:        SourceAgent,
		AgentType:     "academic",
		Content:       "Researching.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-web-search-backfill",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-web-search-backfill",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=python packaging pep 621",
		FullArgs:      `{"query":"python packaging pep 621","action":"search"}`,
		Phase:         1,
		Duration:      25 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || len(last.ToolCalls) != 1 {
		t.Fatalf("expected one tool row, got %+v", last)
	}
	if got := last.ToolCalls[0].FullArgs; got != `{"query":"python packaging pep 621","action":"search"}` {
		t.Fatalf("full args = %q, want enriched completion args", got)
	}
	if got := last.ToolCalls[0].ArgsSummary; got != "query=python packaging pep 621" {
		t.Fatalf("args summary = %q, want enriched completion summary", got)
	}
}

func TestHandleStreamComplete_FinalizesPendingToolCallAndAllowsLateCompletionOverwrite(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-stream-tool",
		AgentID:       "engineer",
		AgentType:     "engineer",
	})
	m = comp.(*Model)
	startedAt := time.Now().Add(-150 * time.Millisecond)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-stream-tool",
		ToolCallKey:   "tool-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=README.md",
		Phase:         0,
		StartedAt:     startedAt,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-stream-tool",
		AgentID:           "engineer",
		AgentType:         "engineer",
		AuthoritativeText: "Done.",
	})
	m = comp.(*Model)

	entry := findEntryByCorrelation(m, "corr-stream-tool")
	if entry == nil || len(entry.ToolCalls) != 1 {
		t.Fatalf("expected one tool call row after stream completion, got %+v", entry)
	}
	record := entry.ToolCalls[0]
	if !record.Completed || !record.SyntheticCompletion {
		t.Fatalf("record after stream completion = %+v, want synthetic completed row", record)
	}
	if record.Duration < 0 {
		t.Fatalf("synthetic duration = %v, want non-negative", record.Duration)
	}

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-stream-tool",
		ToolCallKey:   "tool-1",
		ToolName:      "read_file",
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      180 * time.Millisecond,
		Success:       true,
		Output:        "ok",
	})
	m = comp.(*Model)

	entry = findEntryByCorrelation(m, "corr-stream-tool")
	record = entry.ToolCalls[0]
	if !record.Completed || record.SyntheticCompletion {
		t.Fatalf("record after late completion = %+v, want real completed row", record)
	}
	if record.Output != "ok" {
		t.Fatalf("record output = %q, want ok", record.Output)
	}
	if record.Duration != 180*time.Millisecond {
		t.Fatalf("record duration = %v, want 180ms", record.Duration)
	}
}

func TestHandleToolCallEvent_CompletedDurationPersistsBeforeAndAfterStreamChunks(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-duration-stable",
		AgentID:       "academic",
		AgentType:     "academic",
	})
	m = comp.(*Model)

	startedAt := time.Now().Add(-350 * time.Millisecond)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-duration-stable",
		ToolCallKey:   "ws-1",
		ToolName:      "web_search",
		ArgsSummary:   "query=python packaging pep 621",
		Phase:         0,
		StartedAt:     startedAt,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-duration-stable",
		ToolCallKey:   "ws-1",
		ToolName:      "web_search",
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      350 * time.Millisecond,
		Success:       true,
		Output:        "search complete",
	})
	m = comp.(*Model)

	entry := findEntryByCorrelation(m, "corr-duration-stable")
	if entry == nil || len(entry.ToolCalls) != 1 {
		t.Fatalf("expected completed tool row before stream text, got %+v", entry)
	}
	if got := entry.ToolCalls[0].Duration; got != 350*time.Millisecond {
		t.Fatalf("duration before chunk = %v, want 350ms", got)
	}
	if !entry.ToolCalls[0].Completed {
		t.Fatalf("tool row should already be complete before stream text: %+v", entry.ToolCalls[0])
	}

	comp, _ = m.Update(msg.StreamChunkMsg{
		SessionID:     "s1",
		CorrelationID: "corr-duration-stable",
		Text:          "Search-backed answer.",
	})
	m = comp.(*Model)

	entry = findEntryByCorrelation(m, "corr-duration-stable")
	if entry == nil || len(entry.ToolCalls) != 1 {
		t.Fatalf("expected completed tool row after stream text, got %+v", entry)
	}
	if got := entry.ToolCalls[0].Duration; got != 350*time.Millisecond {
		t.Fatalf("duration after chunk = %v, want 350ms", got)
	}
	if !entry.ToolCalls[0].Completed {
		t.Fatalf("tool row lost completed state after chunk: %+v", entry.ToolCalls[0])
	}
}

func TestHandleToolCallEvent_UnknownCorrelationDoesNotAttachToDifferentActiveStream(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector",
		AgentID:       "runtime-inspector",
		AgentType:     "inspector-pipeline",
		TaskID:        "task-auth",
		TaskName:      "Auth Task",
		TaskSlug:      "auth-task",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tester",
		ToolCallKey:   "tool-1",
		ToolName:      "coord_publish_artifact",
		ArgsSummary:   "type=verification_result",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	inspector := findEntryByCorrelation(m, "corr-inspector")
	if inspector == nil {
		t.Fatal("expected inspector stream entry")
	}
	if got := len(inspector.ToolCalls); got != 0 {
		t.Fatalf("inspector tool call count = %d, want 0", got)
	}
	if entry := findEntryByCorrelation(m, "corr-tester"); entry != nil {
		t.Fatalf("unexpected tester entry without a bootstrapped start: %+v", entry)
	}
}

func TestHandleToolCallEvent_ConsultationCompletesAsInterAgentRow(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "resp-consult",
		Timestamp:     time.Now(),
		CorrelationID: "corr-consult",
		Source:        SourceAgent,
		AgentType:     "inspector",
		Content:       "Audit complete.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-consult",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"question":"Is there a cleaner approach?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-consult",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"question":"Is there a cleaner approach?"}`,
		Output:        `{"consulted":true,"target":"academic","response":"A table-driven harness would be cleaner and easier to extend."}`,
		Phase:         1,
		Duration:      180 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || len(last.ToolCalls) != 1 {
		t.Fatalf("expected one consultation tool row, got %+v", last)
	}
	row := last.ToolCalls[0].InterAgent
	if row == nil {
		t.Fatal("expected inter-agent consultation row")
	}
	if row.Status != InterAgentToolDone {
		t.Fatalf("consultation status = %q, want %q", row.Status, InterAgentToolDone)
	}
	if len(row.AgentTypes) != 1 || row.AgentTypes[0] != "academic" {
		t.Fatalf("consultation agent types = %#v, want [academic]", row.AgentTypes)
	}
	if !strings.Contains(row.Summary, "table-driven harness") {
		t.Fatalf("consultation summary = %q, want academic response", row.Summary)
	}
}

func TestToggleAtViewLine_TogglesToolCallExpansion(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)
	m.PushEntry(&ChatEntry{
		ID:        "tool-toggle",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		Content:   "Done.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "write_file",
				ArgsSummary: "path=README.md",
				FullArgs:    `{"path":"README.md","content":"hello"}`,
				Output:      "ok",
				StartedAt:   time.Now().Add(-time.Second),
				Duration:    time.Second,
				Completed:   true,
				Success:     true,
			},
		},
	})

	if !m.ToggleAtViewLine(1) {
		t.Fatal("expected tool row click to toggle expansion")
	}
	last := m.history.Last()
	if last == nil || !last.ToolCalls[0].Expanded {
		t.Fatalf("expected tool row to expand, got %+v", last)
	}
}

func TestKeyboardNavigationAndSpaceToggle_ToolCall(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)
	m.SetFocused(true)
	m.PushEntry(&ChatEntry{
		ID:        "tool-keyboard",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		Content:   "Done.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "write_file",
				ArgsSummary: "path=README.md",
				FullArgs:    `{"path":"README.md","content":"hello"}`,
				Output:      "ok",
				StartedAt:   time.Now().Add(-time.Second),
				Duration:    time.Second,
				Completed:   true,
				Success:     true,
			},
		},
	})

	comp, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = comp.(*Model)
	comp, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = comp.(*Model)
	comp, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || !last.ToolCalls[0].Expanded {
		t.Fatalf("expected keyboard space toggle to expand tool row, got %+v", last)
	}
}

func TestToggleAtViewLine_ExpandsEarlierEventsOverflow(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)
	children := []InterAgentChildActivity{{
		CorrelationID: "child-overflow",
		AgentType:     "academic",
		ToolCalls: []ToolCallRecord{
			{ToolName: "read_file", ArgsSummary: "path=ui/chat/model.go", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "grep", ArgsSummary: "\"tool_call_key\"", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "sed", ArgsSummary: "ui/chat/model.go", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "apply_patch", ArgsSummary: "ui/chat/model.go", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "gofmt", ArgsSummary: "ui/chat/model.go", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "go_test", ArgsSummary: "./ui/chat", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
		},
	}}
	m.PushEntry(&ChatEntry{
		ID:        "overflow-toggle",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector",
		Content:   "Audit complete.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing test harness options",
					Status:     InterAgentToolDone,
					Children:   children,
				},
				Completed: true,
				Success:   true,
			},
		},
	})

	overflowLine := findRenderedLine(t, m.View(), "earlier events")
	if !m.ToggleAtViewLine(overflowLine) {
		t.Fatal("expected overflow row click to expand earlier events")
	}
	last := m.history.Last()
	if last == nil || !last.ToolCalls[0].InterAgent.Children[0].ToolCallsExpanded {
		t.Fatalf("expected overflow row to expand children, got %+v", last)
	}
	if m.viewport.selectedIndex != 0 {
		t.Fatalf("selected entry = %d, want 0", m.viewport.selectedIndex)
	}
	regions := m.viewport.regions(0)
	if m.viewport.selectedRegion < 0 || m.viewport.selectedRegion >= len(regions) {
		t.Fatalf("selected region = %d, want valid region", m.viewport.selectedRegion)
	}
	if regions[m.viewport.selectedRegion].kind != selectionRegionChildToolCall {
		t.Fatalf("selected region kind = %v, want child tool call after overflow expansion", regions[m.viewport.selectedRegion].kind)
	}
}

func TestToggleSelected_TargetsOverflowAndChildRowsForLaterToolCall(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(88, 20)
	m.SetFocused(true)
	m.PushEntry(&ChatEntry{
		ID:        "overflow-toggle-late-tool",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "read_file",
				ArgsSummary: "ui/chat/tool_render.go",
				FullArgs:    `{"path":"ui/chat/tool_render.go","start_line":1}`,
				Output:      "ok",
				StartedAt:   time.Now().Add(-time.Second),
				Completed:   true,
				Success:     true,
			},
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic-late-toggle",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{ToolName: "read_file", ArgsSummary: "path=ui/chat/model.go", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "\"tool_call_key\"", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "ui/chat/model.go", Completed: true, Success: true},
								{ToolName: "apply_patch", ArgsSummary: "ui/chat/model.go", Completed: true, Success: true},
								{ToolName: "gofmt", ArgsSummary: "ui/chat/model.go", Completed: true, Success: true},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})

	regions := m.viewport.regions(0)
	overflowRegionIdx := -1
	grepRegionIdx := -1
	for idx, region := range regions {
		if region.toolCallIndex != 1 {
			continue
		}
		if region.kind == selectionRegionToolCallOverflow {
			overflowRegionIdx = idx
		}
		if region.kind == selectionRegionChildToolCall && region.childToolCallIdx == 1 {
			grepRegionIdx = idx
		}
	}
	if overflowRegionIdx < 0 {
		t.Fatalf("expected overflow region on later tool call, got %#v", regions)
	}
	if grepRegionIdx >= 0 {
		t.Fatalf("expected grep child region to stay hidden before overflow expansion, got %#v", regions)
	}

	m.viewport.selectEntry(0, overflowRegionIdx)
	if !m.ToggleSelected() {
		t.Fatal("expected selected overflow row on later tool call to expand children")
	}
	entry := m.history.Last()
	if entry == nil || len(entry.ToolCalls) < 2 || entry.ToolCalls[1].InterAgent == nil || !entry.ToolCalls[1].InterAgent.Children[0].ToolCallsExpanded {
		t.Fatalf("expected selected overflow row to expand later tool call children, got %+v", entry)
	}

	regions = m.viewport.regions(0)
	grepRegionIdx = -1
	for idx, region := range regions {
		if region.toolCallIndex == 1 && region.kind == selectionRegionChildToolCall && region.childToolCallIdx == 1 {
			grepRegionIdx = idx
			break
		}
	}
	if grepRegionIdx < 0 {
		t.Fatalf("expected grep child region after expansion, got %#v", regions)
	}

	m.viewport.selectEntry(0, grepRegionIdx)
	if !m.ToggleSelected() {
		t.Fatal("expected selected child row on later tool call to expand grep")
	}
	entry = m.history.Last()
	childCalls := entry.ToolCalls[1].InterAgent.Children[0].ToolCalls
	if !childCalls[1].Expanded {
		t.Fatalf("expected grep child row expanded from selection, got %+v", childCalls)
	}
	if childCalls[0].Expanded {
		t.Fatalf("expected read_file child row to stay collapsed, got %+v", childCalls)
	}
}

func TestToggleAtRenderedViewLine_TargetsEarlierEventsOverflowFromRenderedFrame(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)
	children := []InterAgentChildActivity{{
		CorrelationID: "child-overflow-rendered",
		AgentType:     "academic",
		ToolCalls: []ToolCallRecord{
			{ToolName: "read_file", ArgsSummary: "path=ui/chat/model.go", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "grep", ArgsSummary: "\"tool_call_key\"", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "sed", ArgsSummary: "ui/chat/model.go", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "apply_patch", ArgsSummary: "ui/chat/model.go", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "gofmt", ArgsSummary: "ui/chat/model.go", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
			{ToolName: "go_test", ArgsSummary: "./ui/chat", StartedAt: time.Now().Add(-time.Second), Completed: true, Success: true},
		},
	}}
	m.PushEntry(&ChatEntry{
		ID:        "overflow-rendered-frame",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector",
		Content:   "Audit complete.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing test harness options",
					Status:     InterAgentToolDone,
					Children:   children,
				},
				Completed: true,
				Success:   true,
			},
		},
	})

	rendered := m.View()
	overflowLine := findRenderedLine(t, rendered, "earlier events")

	m.history.UpdateAt(0, func(entry *ChatEntry) {
		entry.ToolCalls[0].InterAgent.Children[0].ToolCallsExpanded = true
		invalidateChatEntryRender(entry)
	})
	m.viewDirty = true

	liveTarget := m.viewport.ToggleTargetAtViewLine(overflowLine)
	if liveTarget != nil && liveTarget.kind == toggleTargetOverflow {
		t.Fatalf("expected live line %d to shift away from overflow target after mutation, got %+v", overflowLine, liveTarget)
	}

	if !m.ToggleAtRenderedViewLine(overflowLine) {
		t.Fatal("expected rendered-frame overflow row toggle to succeed")
	}

	last := m.history.Last()
	if last == nil || last.ToolCalls[0].InterAgent.Children[0].ToolCallsExpanded {
		t.Fatalf("expected rendered-frame overflow toggle to retarget and collapse the pre-expanded child branch, got %+v", last)
	}
}

func TestToggleAtViewLine_ExpandsOnlyTargetedChildOverflow(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(96, 24)
	m.PushEntry(&ChatEntry{
		ID:        "overflow-toggle-multi-child",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Reviewing the consult outputs.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_research_support",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"librarian", "archivalist"},
					Summary:    "Gathering references and archived context",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-librarian-overflow",
							AgentType:     "librarian",
							Completed:     true,
							ToolCalls: []ToolCallRecord{
								{ToolName: "search_library", ArgsSummary: "migration notes", Completed: true, Success: true},
								{ToolName: "read_file", ArgsSummary: "docs/migration.md", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "\"migration\"", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "docs/migration.md", Completed: true, Success: true},
								{ToolName: "write_notes", ArgsSummary: "migration references", Completed: true, Success: true},
							},
						},
						{
							CorrelationID: "child-archivalist-overflow",
							AgentType:     "archivalist",
							Completed:     true,
							ToolCalls: []ToolCallRecord{
								{ToolName: "list_archives", ArgsSummary: "project=sylk", Completed: true, Success: true},
								{ToolName: "read_archive", ArgsSummary: "plan-v3", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "\"handoff\"", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "archive/plan-v3.md", Completed: true, Success: true},
								{ToolName: "write_notes", ArgsSummary: "archived plan", Completed: true, Success: true},
							},
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})

	overflowLines := findRenderedLines(t, m.View(), "earlier events")
	if len(overflowLines) != 2 {
		t.Fatalf("expected two child overflow rows before expansion, got %d", len(overflowLines))
	}
	if !m.ToggleAtViewLine(overflowLines[0]) {
		t.Fatal("expected first child overflow row click to expand")
	}

	entry := m.history.Last()
	if entry == nil || len(entry.ToolCalls) != 1 || entry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected consult entry after targeted overflow toggle, got %+v", entry)
	}
	children := entry.ToolCalls[0].InterAgent.Children
	if !children[0].ToolCallsExpanded {
		t.Fatalf("expected first child overflow expanded, got %+v", children)
	}
	if children[1].ToolCallsExpanded {
		t.Fatalf("expected second child overflow to remain collapsed, got %+v", children)
	}
	if got := len(findRenderedLines(t, m.View(), "earlier events")); got != 1 {
		t.Fatalf("expected one remaining overflow row after expanding first child, got %d", got)
	}
}

func TestKeyboardNavigationAndSpaceToggle_ChildInterAgentRow(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)
	m.SetFocused(true)
	m.PushEntry(&ChatEntry{
		ID:        "child-inter-agent-toggle",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "tester",
		Content:   "Validation complete.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "challenge_architect",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"architect"},
					Summary:    "Testing scope needs clarification",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-architect",
							AgentType:     "architect",
							ToolCalls: []ToolCallRecord{
								{
									ToolName: "consult",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"orchestrator"},
										Summary:    "Reviewing DAG progress before revising the test plan.",
										Status:     InterAgentToolDone,
									},
									Completed: true,
									Success:   true,
								},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})

	comp, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = comp.(*Model)
	comp, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = comp.(*Model)
	comp, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = comp.(*Model)
	comp, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || !last.ToolCalls[0].InterAgent.Children[0].ToolCalls[0].Expanded {
		t.Fatalf("expected keyboard space toggle to expand child inter-agent row, got %+v", last)
	}
}

func TestKeyboardNavigationAndSpaceToggle_GenericChildToolCall(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)
	m.SetFocused(true)
	m.PushEntry(&ChatEntry{
		ID:        "child-tool-toggle",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "path=ui/chat/model.go",
									FullArgs:    `{"path":"ui/chat/model.go","start_line":1}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})

	comp, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = comp.(*Model)
	comp, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = comp.(*Model)
	comp, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = comp.(*Model)
	comp, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || !last.ToolCalls[0].InterAgent.Children[0].ToolCalls[0].Expanded {
		t.Fatalf("expected keyboard space toggle to expand generic child tool row, got %+v", last)
	}
}

func TestToggleAtViewLine_TargetsIndividualChildToolCalls(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)
	m.PushEntry(&ChatEntry{
		ID:        "child-tool-targets",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "path=ui/chat/model.go",
									FullArgs:    `{"path":"ui/chat/model.go","start_line":1}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
								{
									ToolName:    "grep",
									ArgsSummary: "\"tool_call_key\"",
									FullArgs:    `{"pattern":"tool_call_key","path":"ui/chat/model.go"}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})

	regions := m.viewport.regions(0)
	childLines := make(map[int]int)
	for _, region := range regions {
		if region.kind != selectionRegionChildToolCall {
			continue
		}
		childLines[region.childToolCallIdx] = region.start
	}
	if len(childLines) != 2 {
		t.Fatalf("child tool regions = %#v, want 2 individual child tool targets", regions)
	}

	if !m.ToggleAtViewLine(childLines[0]) {
		t.Fatal("expected first child tool row toggle to succeed")
	}
	entry := m.history.Last()
	if entry == nil {
		t.Fatal("expected entry after first child toggle")
	}
	if !entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[0].Expanded {
		t.Fatalf("expected first child tool row expanded, got %+v", entry.ToolCalls[0].InterAgent.Children[0].ToolCalls)
	}
	if entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[1].Expanded {
		t.Fatalf("expected second child tool row to stay collapsed, got %+v", entry.ToolCalls[0].InterAgent.Children[0].ToolCalls)
	}

	regions = m.viewport.regions(0)
	childLines = make(map[int]int)
	for _, region := range regions {
		if region.kind != selectionRegionChildToolCall {
			continue
		}
		childLines[region.childToolCallIdx] = region.start
	}
	if !m.ToggleAtViewLine(childLines[1]) {
		t.Fatal("expected second child tool row toggle to succeed")
	}
	entry = m.history.Last()
	if entry == nil {
		t.Fatal("expected entry after second child toggle")
	}
	if !entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[1].Expanded {
		t.Fatalf("expected second child tool row expanded, got %+v", entry.ToolCalls[0].InterAgent.Children[0].ToolCalls)
	}
}

func TestToggleAtRenderedViewLine_TargetsChildToolRowFromRenderedFrame(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(80, 20)
	m.PushEntry(&ChatEntry{
		ID:        "child-tool-rendered-frame",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic-rendered",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "path=ui/chat/model.go",
									FullArgs:    `{"path":"ui/chat/model.go","start_line":1}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
								{
									ToolName:    "grep",
									ArgsSummary: "\"tool_call_key\"",
									FullArgs:    `{"pattern":"tool_call_key","path":"ui/chat/model.go"}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})

	rendered := m.View()
	grepLine := findRenderedLine(t, rendered, "grep")

	m.history.UpdateAt(0, func(entry *ChatEntry) {
		entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[0].Expanded = true
		invalidateChatEntryRender(entry)
	})
	m.viewDirty = true

	liveTarget := m.viewport.ToggleTargetAtViewLine(grepLine)
	if liveTarget == nil || liveTarget.kind != toggleTargetChildToolCall || liveTarget.childToolCallName == "grep" {
		t.Fatalf("expected live line %d to shift away from grep child row after mutation, got %+v", grepLine, liveTarget)
	}

	if !m.ToggleAtRenderedViewLine(grepLine) {
		t.Fatal("expected rendered-frame child tool row toggle to succeed")
	}

	entry := m.history.Last()
	if entry == nil {
		t.Fatal("expected entry after rendered-frame child toggle")
	}
	childCalls := entry.ToolCalls[0].InterAgent.Children[0].ToolCalls
	if !childCalls[1].Expanded {
		t.Fatalf("expected grep child tool row expanded, got %+v", childCalls)
	}
	if !childCalls[0].Expanded {
		t.Fatalf("expected pre-expanded read_file child tool row to stay expanded, got %+v", childCalls)
	}
}

func TestHandleToolCallEvent_MatchesInterAgentConsultCompletionByToolCallKey(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "resp-consult-keys",
		Timestamp:     time.Now(),
		CorrelationID: "corr-consult-keys",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Planning.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-consult-keys",
		ToolCallKey:   "consult-1",
		ToolName:      "consult",
		FullArgs:      `{"mode":"knowledge","target":"librarian","query":"existing packaging patterns?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-consult-keys",
		ToolCallKey:   "consult-2",
		ToolName:      "consult",
		FullArgs:      `{"mode":"knowledge","target":"archivalist","query":"prior packaging failures?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-consult-keys",
		ToolCallKey:   "consult-1",
		ToolName:      "consult",
		FullArgs:      `{"mode":"knowledge","target":"librarian","query":"existing packaging patterns?"}`,
		Output:        `{"target":"librarian","response":"Use the repo's existing PEP 621 package layout."}`,
		Phase:         1,
		Duration:      35 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || len(last.ToolCalls) != 2 {
		t.Fatalf("expected two consult rows, got %+v", last)
	}
	if row := last.ToolCalls[0].InterAgent; row == nil || len(row.AgentTypes) != 1 || row.AgentTypes[0] != "librarian" || row.Status != InterAgentToolDone {
		t.Fatalf("first consult row = %+v, want librarian done", last.ToolCalls[0].InterAgent)
	}
	if row := last.ToolCalls[1].InterAgent; row == nil || len(row.AgentTypes) != 1 || row.AgentTypes[0] != "archivalist" || row.Status != InterAgentToolPending {
		t.Fatalf("second consult row = %+v, want archivalist pending", last.ToolCalls[1].InterAgent)
	}
}

func TestHandleToolCallEvent_UsesExplicitInterAgentMetadataForUnknownTool(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "resp-metadata",
		Timestamp:     time.Now(),
		CorrelationID: "corr-metadata",
		Source:        SourceAgent,
		AgentType:     "inspector",
		Content:       "Audit complete.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-metadata",
		ToolName:      "custom_agent_exchange",
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "consult",
			AgentTypes: []string{"architect"},
			Summary:    "Assess whether the current testing scope is enough.",
			Status:     "pending",
		},
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-metadata",
		ToolName:      "custom_agent_exchange",
		Phase:         1,
		Duration:      120 * time.Millisecond,
		Success:       true,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "consult",
			AgentTypes: []string{"architect"},
			Summary:    "The plan should add integration coverage before final sign-off.",
			Status:     "done",
		},
	})
	m = comp.(*Model)

	last := m.history.Last()
	if last == nil || len(last.ToolCalls) != 1 {
		t.Fatalf("expected one metadata-driven inter-agent row, got %+v", last)
	}
	row := last.ToolCalls[0].InterAgent
	if row == nil {
		t.Fatal("expected metadata-driven inter-agent row")
	}
	if len(row.AgentTypes) != 1 || row.AgentTypes[0] != "architect" {
		t.Fatalf("row agent types = %#v", row.AgentTypes)
	}
	if row.Status != InterAgentToolDone {
		t.Fatalf("row status = %q, want %q", row.Status, InterAgentToolDone)
	}
	if !strings.Contains(row.Summary, "integration coverage") {
		t.Fatalf("row summary = %q", row.Summary)
	}
}

func TestHandleToolCallEvent_GlobalChallengeReplacesOriginRowAcrossResponseAndValidation(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "inspector-origin",
		Timestamp:     time.Now(),
		CorrelationID: "corr-global-challenge",
		Source:        SourceAgent,
		AgentType:     "inspector",
		Content:       "Audit checkpoint.",
		Height:        -1,
	})
	m.PushEntry(&ChatEntry{
		ID:            "architect-response",
		Timestamp:     time.Now(),
		CorrelationID: "corr-global-validate",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Architect response.",
		Height:        -1,
	})
	m.PushEntry(&ChatEntry{
		ID:            "inspector-process",
		Timestamp:     time.Now(),
		CorrelationID: "corr-global-process",
		Source:        SourceAgent,
		AgentType:     "inspector",
		Content:       "Inspector validation.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-global-challenge",
		ToolName:      "challenge_architect",
		FullArgs:      `{"reason":"Need plan clarification","request":"Reassess the testing scope."}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-global-challenge",
		ToolName:      "challenge_architect",
		FullArgs:      `{"reason":"Need plan clarification","request":"Reassess the testing scope."}`,
		Output:        `{"selected":true,"target_agent":"architect","challenge_id":"global-review-123"}`,
		Phase:         1,
		Duration:      120 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-global-validate",
		ToolName:      "validate_global_review",
		FullArgs:      `{"challenge_id":"global-review-123","requesting_agent":"inspector","status":"passed","summary":"The plan should be revised to strengthen integration coverage."}`,
		Output:        `{"validated":true,"challenge_id":"global-review-123","requesting_agent":"inspector","responding_agent":"architect","status":"passed"}`,
		Phase:         1,
		Duration:      95 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-global-challenge")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected origin challenge row, got %+v", origin)
	}
	if got := origin.ToolCalls[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "architect" {
		t.Fatalf("origin response agent types = %#v, want [architect]", got)
	}
	if !strings.Contains(origin.ToolCalls[0].InterAgent.Summary, "strengthen integration coverage") {
		t.Fatalf("origin response summary = %q", origin.ToolCalls[0].InterAgent.Summary)
	}
	if responder := findEntryByCorrelation(m, "corr-global-validate"); responder == nil || len(responder.ToolCalls) != 0 {
		t.Fatalf("expected no duplicate tool row on responder entry, got %+v", responder)
	}

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-global-process",
		ToolName:      "process_global_validation",
		FullArgs:      `{"challenge_id":"global-review-123","decision":"accept","summary":"Accepted the architect response and will proceed with the revised scope."}`,
		Output:        `{"processed":true,"challenge_id":"global-review-123","decision":"accept"}`,
		Phase:         1,
		Duration:      70 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-global-challenge")
	if got := origin.ToolCalls[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "inspector" {
		t.Fatalf("origin validation agent types = %#v, want [inspector]", got)
	}
	if !strings.Contains(origin.ToolCalls[0].InterAgent.Summary, "Accepted the architect response") {
		t.Fatalf("origin validation summary = %q", origin.ToolCalls[0].InterAgent.Summary)
	}
	if processor := findEntryByCorrelation(m, "corr-global-process"); processor == nil || len(processor.ToolCalls) != 0 {
		t.Fatalf("expected no duplicate tool row on processing entry, got %+v", processor)
	}
}

func TestHandleToolCallEvent_PipelineChallengeReplacesOriginRowAcrossResponseAndValidation(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "pipeline-origin",
		Timestamp:     time.Now(),
		CorrelationID: "corr-pipeline-challenge",
		Source:        SourceAgent,
		AgentType:     "inspector-pipeline",
		Content:       "Pipeline audit.",
		Height:        -1,
	})
	m.PushEntry(&ChatEntry{
		ID:            "pipeline-response",
		Timestamp:     time.Now(),
		CorrelationID: "corr-pipeline-validate",
		Source:        SourceAgent,
		AgentType:     "tester-pipeline",
		Content:       "Pipeline tester response.",
		Height:        -1,
	})
	m.PushEntry(&ChatEntry{
		ID:            "pipeline-process",
		Timestamp:     time.Now(),
		CorrelationID: "corr-pipeline-process",
		Source:        SourceAgent,
		AgentType:     "inspector-pipeline",
		Content:       "Pipeline validation processing.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-pipeline-challenge",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"reason":"Need verification","request":"Run the final pipeline audit."}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-pipeline-challenge",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"reason":"Need verification","request":"Run the final pipeline audit."}`,
		Output:        `{"selected":true,"target_agents":["tester-pipeline"],"challenge_id":"pipeline-review-456"}`,
		Phase:         1,
		Duration:      110 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-pipeline-validate",
		ToolName:      "validate_work",
		FullArgs:      `{"challenge_id":"pipeline-review-456","requesting_agent":"inspector-pipeline","status":"passed","summary":"The final pipeline audit passed with solid test evidence."}`,
		Output:        `{"validated":true,"challenge_id":"pipeline-review-456","requesting_agent":"inspector-pipeline","responding_agent":"tester-pipeline","status":"passed"}`,
		Phase:         1,
		Duration:      90 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-pipeline-challenge")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected origin pipeline challenge row, got %+v", origin)
	}
	if got := origin.ToolCalls[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "tester-pipeline" {
		t.Fatalf("pipeline response agent types = %#v, want [tester-pipeline]", got)
	}

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-pipeline-process",
		ToolName:      "process_validation",
		FullArgs:      `{"challenge_id":"pipeline-review-456","decision":"accept","summary":"Accepted the tester validation and will move toward OT handoff."}`,
		Output:        `{"processed":true,"challenge_id":"pipeline-review-456","decision":"accept"}`,
		Phase:         1,
		Duration:      75 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-pipeline-challenge")
	if got := origin.ToolCalls[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "inspector-pipeline" {
		t.Fatalf("pipeline validation agent types = %#v, want [inspector-pipeline]", got)
	}
	if !strings.Contains(origin.ToolCalls[0].InterAgent.Summary, "move toward OT handoff") {
		t.Fatalf("pipeline validation summary = %q", origin.ToolCalls[0].InterAgent.Summary)
	}
}

func TestNestedConsultationStreamAttachesToOriginBranchWithoutCreatingTopLevelEntry(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-nested",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-nested",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"question":"Is there a cleaner harness structure?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-nested",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested",
		AgentID:       "academic",
		AgentType:     "academic",
		Message:       "Comparing harness options.",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested",
		AgentID:       "academic",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=ui/chat/model.go",
		Phase:         0,
		StartedAt:     time.Now(),
		BranchRef:     branchRef,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.StreamChunkMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested",
		Text:          "A table-driven harness would be cleaner.",
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-nested")
	if origin == nil {
		t.Fatal("expected parent entry to remain present")
	}
	if got := len(origin.ToolCalls); got != 1 {
		t.Fatalf("parent tool call count = %d, want 1 root consult row", got)
	}
	if findEntryByCorrelation(m, "corr-child-nested") != nil {
		t.Fatal("expected child stream to stay out of top-level chat history")
	}
	if origin.Content != "Refining the patch plan." {
		t.Fatalf("parent content leaked child text: %q", origin.Content)
	}
	row := origin.ToolCalls[0].InterAgent
	if row == nil || len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity, got %+v", row)
	}
	child := row.Children[0]
	if got := len(child.ToolCalls); got != 1 {
		t.Fatalf("nested child tool call count = %d, want 1", got)
	}
	if child.ToolCalls[0].ToolName != "read_file" {
		t.Fatalf("nested child tool call = %q, want read_file", child.ToolCalls[0].ToolName)
	}
	if !strings.Contains(child.ThinkingStatus, "Comparing harness options.") {
		t.Fatalf("nested child thinking status = %q, want progress message", child.ThinkingStatus)
	}

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-child-nested",
		AgentID:           "academic",
		AgentType:         "academic",
		AuthoritativeText: "A table-driven harness would be cleaner and easier to extend.",
		BranchRef:         branchRef,
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-parent-nested")
	row = origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected nested child activity to persist after completion, got %+v", row.Children)
	}
	child = row.Children[0]
	if !child.Completed {
		t.Fatal("expected nested child activity to be marked complete")
	}
	if len(child.ToolCalls) != 1 {
		t.Fatalf("expected nested child tool calls to persist after completion, got %+v", child.ToolCalls)
	}
	if !child.ToolCalls[0].Completed || !child.ToolCalls[0].SyntheticCompletion {
		t.Fatalf("nested child tool row = %+v, want synthetically completed row", child.ToolCalls[0])
	}
	if child.ThinkingStatus != "" || child.ThinkingText != "" {
		t.Fatalf("expected nested child thinking fields cleared on completion, got text=%q status=%q", child.ThinkingText, child.ThinkingStatus)
	}
	if !strings.Contains(child.ResultSummary, "table-driven harness") {
		t.Fatalf("nested child result summary = %q", child.ResultSummary)
	}
	if origin.Content != "Refining the patch plan." {
		t.Fatalf("parent content changed after child completion: %q", origin.Content)
	}
}

func TestNestedConsultationStreamWaitsForOriginRowInsteadOfCreatingFallbackEntry(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-late",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-late",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-late",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-late",
		AgentID:       "guardian",
		AgentType:     "guardian",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-late",
		AgentID:       "guardian",
		AgentType:     "guardian",
		Message:       "Checking safety assumptions.",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	if findEntryByCorrelation(m, "corr-child-late") != nil {
		t.Fatal("expected unresolved child stream to avoid top-level fallback entry")
	}
	origin := findEntryByCorrelation(m, "corr-parent-late")
	if origin == nil || len(origin.ToolCalls) != 0 {
		t.Fatalf("expected parent entry to remain unchanged before root row exists, got %+v", origin)
	}

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-late",
		ToolCallKey:   "consult-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"guardian","query":"Can you check the current safety assumptions?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-parent-late")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected origin consult row after tool start, got %+v", origin)
	}
	row := origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected deferred child activity to attach once origin row appeared, got %+v", row.Children)
	}
	child := row.Children[0]
	if child.AgentType != "guardian" {
		t.Fatalf("nested child agent type = %q, want guardian", child.AgentType)
	}
	if !strings.Contains(child.ThinkingStatus, "Checking safety assumptions.") {
		t.Fatalf("nested child thinking status = %q, want deferred progress", child.ThinkingStatus)
	}
	if findEntryByCorrelation(m, "corr-child-late") != nil {
		t.Fatal("expected child stream to remain nested after origin row appeared")
	}
}

func TestNestedConsultationStreamStartSuppressesGenericChildPlaceholder(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-placeholder",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-placeholder",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-placeholder",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_librarian_style",
		FullArgs:      `{"query":"What prior patterns should we follow?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-placeholder",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-placeholder",
		AgentID:       "librarian",
		AgentType:     "librarian",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-placeholder")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	row := origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity, got %+v", row.Children)
	}
	child := row.Children[0]
	if child.ThinkingStatus != "" || child.ThinkingText != "" {
		t.Fatalf("expected child placeholder thinking to stay hidden until explicit progress, got text=%q status=%q", child.ThinkingText, child.ThinkingStatus)
	}
	if findEntryByCorrelation(m, "corr-child-placeholder") != nil {
		t.Fatal("expected child stream to remain nested, not top-level")
	}
}

func TestNestedChildInterAgentConsultStartAttachesImmediatelyWhenMetadataDropsTargets(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-consult-start",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-consult-start",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-consult-start",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Compare harness options"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	parentBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-consult-start",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-consult-start",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     parentBranch,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-consult-start",
		AgentID:       "academic",
		ToolCallKey:   "consult-lib-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"librarian","query":"Find relevant UI patterns."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		BranchRef:     parentBranch,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:   "consult",
			Status: "pending",
		},
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-child-consult-start")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	root := origin.ToolCalls[0].InterAgent
	if len(root.Children) != 1 {
		t.Fatalf("expected academic child activity, got %+v", root.Children)
	}
	child := root.Children[0]
	if got := len(child.ToolCalls); got != 1 {
		t.Fatalf("child tool call count = %d, want 1", got)
	}
	row := child.ToolCalls[0].InterAgent
	if row == nil {
		t.Fatalf("expected nested child consult row, got %+v", child.ToolCalls[0])
	}
	if got := row.AgentTypes; len(got) != 1 || got[0] != "librarian" {
		t.Fatalf("nested child consult targets = %#v, want [librarian]", got)
	}
	if got := row.Status; got != InterAgentToolPending {
		t.Fatalf("nested child consult status = %q, want %q", got, InterAgentToolPending)
	}
	if got := row.Summary; got != "Find relevant UI patterns." {
		t.Fatalf("nested child consult summary = %q, want fallback query", got)
	}
}

func TestNestedChildInterAgentConsultStartAttachesByParentCorrelationWhenToolKeyMissing(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-consult-fallback",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-consult-fallback",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-consult-fallback",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Compare harness options"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-consult-fallback",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-consult-fallback",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-child-consult-fallback")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	row := origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity, got %+v", row.Children)
	}
	child := row.Children[0]
	if child.CorrelationID != "corr-child-consult-fallback" {
		t.Fatalf("child correlation id = %q, want corr-child-consult-fallback", child.CorrelationID)
	}
	if child.AgentType != "academic" {
		t.Fatalf("child agent type = %q, want academic", child.AgentType)
	}
	if findEntryByCorrelation(m, "corr-child-consult-fallback") != nil {
		t.Fatal("expected missing-key child stream to remain nested, not top-level")
	}
}

func TestNestedChildInterAgentConsultStartDoesNotAttachByParentCorrelationWhenConsultRowsAreAmbiguous(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-consult-ambiguous",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-consult-ambiguous",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-consult-ambiguous",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Compare harness options"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-consult-ambiguous",
		ToolCallKey:   "consult-2",
		ToolName:      "consult_librarian_style",
		FullArgs:      `{"target":"librarian","query":"Find related patterns"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-consult-ambiguous",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-consult-ambiguous",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-child-consult-ambiguous")
	if origin == nil || len(origin.ToolCalls) != 2 {
		t.Fatalf("expected two parent consult rows, got %+v", origin)
	}
	for idx, record := range origin.ToolCalls {
		if record.InterAgent == nil {
			t.Fatalf("expected consult row %d to be inter-agent, got %+v", idx, record)
		}
		if got := len(record.InterAgent.Children); got != 0 {
			t.Fatalf("consult row %d child count = %d, want 0 for ambiguous parent correlation", idx, got)
		}
	}
	if findEntryByCorrelation(m, "corr-child-consult-ambiguous") != nil {
		t.Fatal("expected ambiguous missing-key child stream to avoid a top-level entry")
	}
}

func TestNestedConsultationChildNativeWebSearchCompletesBeforeChildStreamCompletes(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-web-search",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-web-search",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-web-search",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Compare packaging guidance"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-web-search",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-web-search",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	startedAt := time.Now().Add(-250 * time.Millisecond)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-web-search",
		AgentID:       "academic",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=python packaging pep 621",
		FullArgs:      `{"query":"python packaging pep 621","action":"search"}`,
		Phase:         0,
		StartedAt:     startedAt,
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-web-search",
		AgentID:       "academic",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=python packaging pep 621",
		FullArgs:      `{"query":"python packaging pep 621","action":"search"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      250 * time.Millisecond,
		Success:       true,
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-child-web-search")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	row := origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity, got %+v", row.Children)
	}
	child := row.Children[0]
	if len(child.ToolCalls) != 1 {
		t.Fatalf("expected one nested child tool row, got %+v", child.ToolCalls)
	}
	if !child.ToolCalls[0].Completed || child.ToolCalls[0].SyntheticCompletion {
		t.Fatalf("expected real completed native search row before child stream completion, got %+v", child.ToolCalls[0])
	}
	if child.Completed {
		t.Fatalf("expected child stream itself to remain active until its own completion, got %+v", child)
	}
}

func TestNestedConsultationChildStreamCompleteWaitsForActiveToolCalls(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-complete-after-tools",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-complete-after-tools",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-complete-after-tools",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Compare packaging guidance"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-complete-after-tools",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-complete-after-tools",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	startedAt := time.Now().Add(-250 * time.Millisecond)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-complete-after-tools",
		AgentID:       "academic",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=python packaging pep 621",
		FullArgs:      `{"query":"python packaging pep 621","action":"search"}`,
		Phase:         0,
		StartedAt:     startedAt,
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-child-complete-after-tools",
		AgentID:           "academic",
		AgentType:         "academic",
		AuthoritativeText: "PEP 621 is the modern packaging baseline.",
		BranchRef:         branchRef,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-child-complete-after-tools")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	row := origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity, got %+v", row.Children)
	}
	child := row.Children[0]
	if child.Completed {
		t.Fatalf("expected child consult to remain pending while tool calls are still active, got %+v", child)
	}
	if child.ThinkingStatus != "" || child.ThinkingText != "" {
		t.Fatalf("expected child thinking fields cleared after stream completion, got text=%q status=%q", child.ThinkingText, child.ThinkingStatus)
	}
	if len(child.ToolCalls) != 1 || child.ToolCalls[0].Completed {
		t.Fatalf("expected active child tool call to remain open after stream completion, got %+v", child.ToolCalls)
	}
	if !strings.Contains(child.ResultSummary, "PEP 621") {
		t.Fatalf("child result summary = %q, want stream completion summary", child.ResultSummary)
	}

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-complete-after-tools",
		AgentID:       "academic",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=python packaging pep 621",
		FullArgs:      `{"query":"python packaging pep 621","action":"search"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      250 * time.Millisecond,
		Success:       true,
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-parent-child-complete-after-tools")
	row = origin.ToolCalls[0].InterAgent
	child = row.Children[0]
	if !child.Completed || child.Failed {
		t.Fatalf("expected child consult to complete once tool calls finished, got %+v", child)
	}
	if !strings.Contains(child.ResultSummary, "PEP 621") {
		t.Fatalf("child result summary = %q, want stream completion summary", child.ResultSummary)
	}
	if len(child.ToolCalls) != 1 || !child.ToolCalls[0].Completed || child.ToolCalls[0].SyntheticCompletion {
		t.Fatalf("expected real completed child tool call to persist, got %+v", child.ToolCalls)
	}
}

func TestNestedConsultationChildToolExpansionPersistsAcrossProgressSync(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(80, 20)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-expand",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-expand",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-expand",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Compare harness options"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-expand",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-expand",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-child-expand",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=ui/chat/model.go",
		FullArgs:      `{"path":"ui/chat/model.go","start_line":1}`,
		Phase:         0,
		StartedAt:     time.Now(),
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	regions := m.viewport.regions(0)
	childLine := -1
	for _, region := range regions {
		if region.kind == selectionRegionChildToolCall {
			childLine = region.start
			break
		}
	}
	if childLine < 0 {
		t.Fatalf("expected child tool region, got %#v", regions)
	}
	if !m.ToggleAtViewLine(childLine) {
		t.Fatal("expected child tool row toggle to succeed")
	}

	origin := findEntryByCorrelation(m, "corr-parent-child-expand")
	if origin == nil || !origin.ToolCalls[0].InterAgent.Children[0].ToolCalls[0].Expanded {
		t.Fatalf("expected child tool row expanded before progress sync, got %+v", origin)
	}

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-expand",
		AgentID:       "academic",
		AgentType:     "academic",
		Message:       "Comparing harness options.",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-parent-child-expand")
	if origin == nil || !origin.ToolCalls[0].InterAgent.Children[0].ToolCalls[0].Expanded {
		t.Fatalf("expected child tool row to stay expanded after progress sync, got %+v", origin)
	}
}

func TestUpsertInterAgentChildActivity_PreservesExpandedDuplicateToolRowsByOrder(t *testing.T) {
	row := &InterAgentTool{
		Children: []InterAgentChildActivity{
			{
				CorrelationID: "child-duplicate-tools",
				AgentType:     "academic",
				ToolCalls: []ToolCallRecord{
					{
						ToolName:  "read_file",
						StartedAt: time.Now().Add(-time.Second),
						Expanded:  true,
					},
					{
						ToolName:  "read_file",
						StartedAt: time.Now().Add(-time.Second),
					},
				},
			},
		},
	}

	upsertInterAgentChildActivity(row, InterAgentChildActivity{
		CorrelationID: "child-duplicate-tools",
		AgentType:     "academic",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "read_file",
				StartedAt: time.Now().Add(-time.Second),
			},
			{
				ToolName:  "read_file",
				StartedAt: time.Now().Add(-time.Second),
			},
		},
	})

	child := row.Children[0]
	if !child.ToolCalls[0].Expanded {
		t.Fatalf("expected first duplicate child tool row to stay expanded, got %+v", child.ToolCalls)
	}
	if child.ToolCalls[1].Expanded {
		t.Fatalf("expected second duplicate child tool row to stay collapsed, got %+v", child.ToolCalls)
	}
}

func TestNestedConsultationEmptyProgressSuppressesGenericChildPlaceholder(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-routing",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-routing",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-routing",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_librarian_style",
		FullArgs:      `{"query":"What prior patterns should we follow?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-routing",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-routing",
		AgentID:       "librarian",
		AgentType:     "librarian",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-routing",
		AgentID:       "librarian",
		AgentType:     "librarian",
		BranchRef:     branchRef,
		Message:       "",
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-routing")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	row := origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity, got %+v", row.Children)
	}
	child := row.Children[0]
	if child.ThinkingStatus != "" || child.ThinkingText != "" {
		t.Fatalf("expected empty routing progress to stay hidden, got text=%q status=%q", child.ThinkingText, child.ThinkingStatus)
	}
	if findEntryByCorrelation(m, "corr-child-routing") != nil {
		t.Fatal("expected child routing progress to remain nested, not top-level")
	}
}

func TestNestedSystemProgressIsIgnored(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-store",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-store",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
		ToolCalls: []ToolCallRecord{{
			ToolCallKey: "store-1",
			ToolName:    "store_archivalist",
			StartedAt:   time.Now(),
			InterAgent: &InterAgentTool{
				Kind:       InterAgentToolStore,
				AgentTypes: []string{"archivalist"},
				Summary:    "stored pre-delegation declaration",
				Status:     InterAgentToolDone,
			},
		}},
	})

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-store",
		ParentToolCallKey:   "store-1",
		Kind:                "store",
	}

	comp, _ := m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-store",
		AgentID:       "guide",
		AgentType:     "guide",
		Message:       "Classifying request...",
		Visibility:    events.VisibilitySystem,
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-store")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected origin store row to remain intact, got %+v", origin)
	}
	if got := len(origin.ToolCalls[0].InterAgent.Children); got != 0 {
		t.Fatalf("nested child count = %d, want 0 for ignored system progress", got)
	}
	if _, ok := m.nestedStreams["corr-child-store"]; ok {
		t.Fatal("expected ignored system progress to avoid creating a nested stream slot")
	}
	if findEntryByCorrelation(m, "corr-child-store") != nil {
		t.Fatal("expected ignored system progress to avoid a top-level child entry")
	}
}

func TestNestedConsultationStreamStartKeepsExistingBranchWhenLaterStartDropsMetadata(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-retry",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-retry",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-retry",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_librarian_style",
		FullArgs:      `{"query":"What prior patterns should we follow?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-retry",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-retry",
		AgentID:       "librarian",
		AgentType:     "librarian",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-retry",
		AgentID:       "librarian",
		AgentType:     "librarian",
	})
	m = comp.(*Model)

	if findEntryByCorrelation(m, "corr-child-retry") != nil {
		t.Fatal("expected metadata-less retry start to avoid a top-level child entry")
	}
	origin := findEntryByCorrelation(m, "corr-parent-retry")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row to remain intact, got %+v", origin)
	}
	if got := len(origin.ToolCalls[0].InterAgent.Children); got != 1 {
		t.Fatalf("nested child count = %d, want 1", got)
	}
}

func TestHandleStreamComplete_DefersParentCompletionUntilInterAgentChildrenSettle(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	entry := &ChatEntry{
		ID:             "architect-parent-deferred-complete",
		Timestamp:      time.Now(),
		CorrelationID:  "corr-parent-deferred-complete",
		Source:         SourceAgent,
		AgentType:      "architect",
		AgentID:        "architect",
		Content:        "Architect response is ready.",
		Streaming:      true,
		ThinkingText:   "⠋  2.0s",
		ThinkingStatus: "Drafting the final recommendation...",
		Height:         -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-1",
				Completed:   true,
				Success:     true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare the best harness options.",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:  "corr-child-deferred-complete",
							AgentType:      "academic",
							ThinkingText:   "◜",
							ThinkingStatus: "Still researching source quality.",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "web_search",
									ArgsSummary: "best harness options",
									StartedAt:   time.Now().Add(-400 * time.Millisecond),
								},
							},
						},
					},
				},
			},
		},
	}
	m.PushEntry(entry)
	idx := m.history.Len() - 1

	slot := &streamSlot{
		accumulator:   NewStreamAccumulator(idx),
		agentID:       "architect",
		thinkingIdx:   idx,
		thinkingStart: time.Now().Add(-2 * time.Second),
		renderState:   &streamRenderState{},
	}
	slot.accumulator.Replace(entry.Content)
	m.streams = map[string]*streamSlot{
		entry.CorrelationID: slot,
	}
	m.viewport.AddStreamState(idx, slot.renderState)
	m.syncPendingInterAgentEntry(idx)

	comp, _ := m.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     entry.CorrelationID,
		AgentID:           "architect",
		AgentType:         "architect",
		AuthoritativeText: entry.Content,
	})
	m = comp.(*Model)

	deferred := findEntryByCorrelation(m, entry.CorrelationID)
	if deferred == nil {
		t.Fatal("expected deferred parent entry")
	}
	if !deferred.Streaming {
		t.Fatalf("expected parent completion to remain deferred while child work is active, got %+v", deferred)
	}
	if deferred.Content != "Architect response is ready." {
		t.Fatalf("parent content = %q, want authoritative content while completion is deferred", deferred.Content)
	}
	if strings.TrimSpace(deferred.ThinkingText) == "" {
		t.Fatalf("expected parent thinking footer to remain active while child work is pending, got %+v", deferred)
	}
	if !strings.Contains(deferred.ThinkingStatus, deferredParentCompletionStatus) {
		t.Fatalf("expected deferred completion status %q, got %q", deferredParentCompletionStatus, deferred.ThinkingStatus)
	}
	if _, ok := m.streams[entry.CorrelationID]; !ok {
		t.Fatal("expected parent stream slot to stay alive until child work settles")
	}
	view := m.View()
	if !strings.Contains(view, "Architect response is ready.") {
		t.Fatalf("expected deferred parent content to remain visible in the chat view, got %q", view)
	}
	if !strings.Contains(view, deferredParentCompletionStatus) {
		t.Fatalf("expected deferred parent status footer in the chat view, got %q", view)
	}

	m.history.UpdateAt(idx, func(e *ChatEntry) {
		child := &e.ToolCalls[0].InterAgent.Children[0]
		child.ThinkingText = ""
		child.ThinkingStatus = ""
		child.Completed = true
		child.ToolCalls[0].Completed = true
		child.ToolCalls[0].Success = true
		child.ToolCalls[0].Duration = 400 * time.Millisecond
		invalidateChatEntryRender(e)
	})
	m.syncPendingInterAgentEntry(idx)

	final := findEntryByCorrelation(m, entry.CorrelationID)
	if final == nil {
		t.Fatal("expected finalized parent entry")
	}
	if final.Streaming {
		t.Fatalf("expected parent entry to finalize once child work settles, got %+v", final)
	}
	if strings.TrimSpace(final.ThinkingText) != "" || strings.TrimSpace(final.ThinkingStatus) != "" {
		t.Fatalf("expected parent thinking footer to clear after deferred completion resolves, got %+v", final)
	}
	if _, ok := m.streams[entry.CorrelationID]; ok {
		t.Fatal("expected deferred parent stream slot to be released once child work settles")
	}
}

func findRenderedLine(t *testing.T, rendered, needle string) int {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	for idx, line := range lines {
		if strings.Contains(line, needle) {
			return idx
		}
	}
	t.Fatalf("rendered chat view missing %q in %q", needle, rendered)
	return -1
}

func findRenderedLines(t *testing.T, rendered, needle string) []int {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	out := make([]int, 0, len(lines))
	for idx, line := range lines {
		if strings.Contains(line, needle) {
			out = append(out, idx)
		}
	}
	if len(out) == 0 {
		t.Fatalf("rendered chat view missing %q in %q", needle, rendered)
	}
	return out
}

func findEntryByCorrelation(m *Model, correlationID string) *ChatEntry {
	for idx := 0; idx < m.history.Len(); idx++ {
		entry := m.history.Get(idx)
		if entry != nil && entry.CorrelationID == correlationID {
			return entry
		}
	}
	return nil
}
