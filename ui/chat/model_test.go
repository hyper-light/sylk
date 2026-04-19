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

	if m.progress.retryText != "Consulting available knowledge agents..." {
		t.Fatalf("retryText = %q", m.progress.retryText)
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

func TestStreamProgressQueuesHumanProgressBehindThrottle(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("tester-pipeline")

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-progress-throttle",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-progress-throttle",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Pipeline Tester is reasoning deeply...",
		Sequence:      1,
		Watchdog:      true,
	})
	m = comp.(*Model)

	slot := m.streamSlot("corr-progress-throttle")
	if slot == nil {
		t.Fatal("expected active stream slot")
	}
	base := time.Now()
	slot.progress.lastProgressSet = base

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-progress-throttle",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Publishing the validation findings artifact for downstream review.",
		Sequence:      2,
	})
	m = comp.(*Model)

	entry := findEntryByCorrelation(m, "corr-progress-throttle")
	if entry == nil {
		t.Fatal("expected active stream entry")
	}
	if entry.ThinkingStatus != "Pipeline Tester is reasoning deeply..." {
		t.Fatalf("thinking status = %q, want throttle to defer update", entry.ThinkingStatus)
	}

	slot = m.streamSlot("corr-progress-throttle")
	if slot == nil {
		t.Fatal("expected active stream slot after queued progress")
	}
	if slot.progress.pendingRetryText != "Publishing the validation findings artifact for downstream review." {
		t.Fatalf("pending progress = %q", slot.progress.pendingRetryText)
	}
	if slot.progress.pendingRetrySequence != 2 {
		t.Fatalf("pending progress sequence = %d, want 2", slot.progress.pendingRetrySequence)
	}

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-progress-throttle",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Pipeline Tester is reasoning deeply...",
		Sequence:      3,
		Watchdog:      true,
	})
	m = comp.(*Model)

	slot = m.streamSlot("corr-progress-throttle")
	if slot == nil {
		t.Fatal("expected active stream slot after watchdog progress")
	}
	if slot.progress.pendingRetryText != "Publishing the validation findings artifact for downstream review." {
		t.Fatalf("expected watchdog progress not to displace pending human update, got %q", slot.progress.pendingRetryText)
	}
	if slot.progress.pendingRetrySequence != 2 {
		t.Fatalf("expected pending human sequence to remain 2, got %d", slot.progress.pendingRetrySequence)
	}

	comp, _ = m.Update(msg.DecorTickMsg{Time: base.Add(thinkingProgressMinInterval)})
	m = comp.(*Model)

	entry = findEntryByCorrelation(m, "corr-progress-throttle")
	if entry == nil {
		t.Fatal("expected active stream entry after flush")
	}
	if entry.ThinkingStatus != "Publishing the validation findings artifact for downstream review." {
		t.Fatalf("thinking status = %q, want throttled human-authored progress update", entry.ThinkingStatus)
	}
}

func TestStreamProgressToolDerivedCanReplaceRenderedHumanProgress(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("tester-pipeline")

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-progress-tool-replace",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-progress-tool-replace",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Translating task and inspector criteria into executable tests and validating the current failure surface.",
		Sequence:      1,
	})
	m = comp.(*Model)

	slot := m.streamSlot("corr-progress-tool-replace")
	if slot == nil {
		t.Fatal("expected active stream slot")
	}
	base := time.Now()
	slot.progress.lastProgressSet = base

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-progress-tool-replace",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Working through this with read file.",
		ToolDerived:   true,
		Sequence:      2,
	})
	m = comp.(*Model)

	entry := findEntryByCorrelation(m, "corr-progress-tool-replace")
	if entry == nil {
		t.Fatal("expected active stream entry")
	}
	if entry.ThinkingStatus != "Translating task and inspector criteria into executable tests and validating the current failure surface." {
		t.Fatalf("thinking status = %q, want rendered human progress preserved until flush", entry.ThinkingStatus)
	}

	slot = m.streamSlot("corr-progress-tool-replace")
	if slot == nil {
		t.Fatal("expected active stream slot after tool progress")
	}
	if slot.progress.pendingRetryText != "Working through this with read file." || !slot.progress.pendingRetryToolDerived {
		t.Fatalf("pending progress = %+v, want queued tool-derived update", slot.progress)
	}

	comp, _ = m.Update(msg.DecorTickMsg{Time: base.Add(thinkingProgressMinInterval)})
	m = comp.(*Model)

	entry = findEntryByCorrelation(m, "corr-progress-tool-replace")
	if entry == nil {
		t.Fatal("expected active stream entry after flush")
	}
	if entry.ThinkingStatus != "Working through this with read file." {
		t.Fatalf("thinking status = %q, want newer tool-derived progress", entry.ThinkingStatus)
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

func TestAgentStateMsg_SurfacesReasoningOnStreamingEntry(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	// Open a stream for the engineer — this creates the entry and sets the
	// default thinking indicator (spinner + rotating placeholder).
	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "engineer",
		AgentType:     "engineer",
	})
	m = comp.(*Model)

	// Simulate CompleteWithWatchdog firing an AgentStateReasoning transition
	// at the start of an LLM call. The UI bridge turns that into this msg.
	comp, _ = m.Update(msg.AgentStateMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "engineer",
		AgentType:     "engineer",
		State:         "reasoning",
		TransitionID:  1,
	})
	m = comp.(*Model)

	// The streaming entry's ThinkingStatus must now reflect the reasoning
	// state. Without this the user sees the rotating placeholder until the
	// watchdog threshold (15+ s) fires a progress event — the exact gap the
	// activity channel was added to eliminate.
	last := m.history.Last()
	if last == nil {
		t.Fatal("expected streaming entry")
	}
	if last.ThinkingStatus != "Reasoning..." {
		t.Fatalf("ThinkingStatus = %q, want \"Reasoning...\"", last.ThinkingStatus)
	}

	// Run one tick to mimic the 100ms spinner loop. The render loop's
	// rotating placeholder MUST NOT clobber the state-driven status.
	m.tickThinking(time.Now())
	last = m.history.Last()
	if last.ThinkingStatus != "Reasoning..." {
		t.Fatalf("after tick: ThinkingStatus = %q, want \"Reasoning...\" (rotating placeholder clobbered state)", last.ThinkingStatus)
	}

	// A terminal state should evict the correlation's record and clear the
	// entry's status so the finalized row doesn't show stale activity.
	comp, _ = m.Update(msg.AgentStateMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "engineer",
		AgentType:     "engineer",
		State:         "complete",
		TransitionID:  2,
	})
	m = comp.(*Model)
	if _, ok := m.agentStates["c1"]; ok {
		t.Fatal("agentStates still holds correlation after terminal state")
	}
}

func TestAgentStateMsg_ChildEventKeepsParentThinkingAlive(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	// Open a parent stream (tester-pipeline).
	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "parent",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
	})
	m = comp.(*Model)

	// Parent begins reasoning.
	comp, _ = m.Update(msg.AgentStateMsg{
		SessionID:     "s1",
		CorrelationID: "parent",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		State:         "reasoning",
		TransitionID:  1,
	})
	m = comp.(*Model)

	parentEntry := m.history.Last()
	if parentEntry == nil {
		t.Fatal("expected parent streaming entry")
	}
	if parentEntry.ThinkingStatus != "Reasoning..." {
		t.Fatalf("parent ThinkingStatus = %q, want \"Reasoning...\"", parentEntry.ThinkingStatus)
	}

	// Guardian approval lands on its own correlation but carries
	// ParentCorrelationID. The parent's row must surface the child
	// detail as its status so the spinner doesn't go stale.
	comp, _ = m.Update(msg.AgentStateMsg{
		SessionID:           "s1",
		CorrelationID:       "guardian-approval",
		ParentCorrelationID: "parent",
		AgentID:             "guardian",
		AgentType:           "guardian",
		State:               "reasoning",
		Detail:              "Reviewing command",
		TransitionID:        2,
	})
	m = comp.(*Model)

	parentEntry = m.history.Last()
	if parentEntry == nil {
		t.Fatal("expected parent entry to still be present")
	}
	if parentEntry.ThinkingStatus != "Reviewing command" {
		t.Fatalf("parent ThinkingStatus after child non-terminal = %q, want \"Reviewing command\"", parentEntry.ThinkingStatus)
	}

	// Child terminal must NOT clear the parent's indicator — only the
	// child finished. The parent's own last state (reasoning) wins.
	comp, _ = m.Update(msg.AgentStateMsg{
		SessionID:           "s1",
		CorrelationID:       "guardian-approval",
		ParentCorrelationID: "parent",
		AgentID:             "guardian",
		AgentType:           "guardian",
		State:               "complete",
		TransitionID:        3,
	})
	m = comp.(*Model)
	if _, ok := m.agentStates["parent"]; !ok {
		t.Fatal("parent state evicted on child terminal — ownership lost")
	}
	parentEntry = m.history.Last()
	if parentEntry.ThinkingStatus != "Reasoning..." {
		t.Fatalf("parent ThinkingStatus after child terminal = %q, want \"Reasoning...\" (parent's own state)", parentEntry.ThinkingStatus)
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

func TestSameCorrelationResponderTransitionClearsPriorProgressOverride(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("guide")

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-shared-handoff",
		AgentID:       "guide",
		AgentType:     "guide",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-shared-handoff",
		AgentID:       "guide",
		AgentType:     "guide",
		Message:       "Classifying request...",
	})
	m = comp.(*Model)

	before := findEntryByCorrelation(m, "corr-shared-handoff")
	if before == nil {
		t.Fatal("expected shared-correlation guide entry")
	}
	if before.ThinkingStatus != "Classifying request..." {
		t.Fatalf("initial thinking status = %q, want guide progress override", before.ThinkingStatus)
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-shared-handoff",
		AgentID:       "architect",
		AgentType:     "architect",
	})
	m = comp.(*Model)

	after := findEntryByCorrelation(m, "corr-shared-handoff")
	if after == nil {
		t.Fatal("expected shared-correlation architect entry")
	}
	if after.AgentID != "architect" {
		t.Fatalf("AgentID = %q, want architect", after.AgentID)
	}
	if after.AgentType != "architect" {
		t.Fatalf("AgentType = %q, want architect", after.AgentType)
	}
	if after.ThinkingStatus == "Classifying request..." {
		t.Fatalf("expected guide progress override cleared on responder transition, got %q", after.ThinkingStatus)
	}
	if want := thinkingMessagesForAgent("architect")[0]; after.ThinkingStatus != want {
		t.Fatalf("thinking status = %q, want %q", after.ThinkingStatus, want)
	}

	slot := m.streamSlot("corr-shared-handoff")
	if slot == nil {
		t.Fatal("expected shared-correlation stream slot")
	}
	if slot.progress.retryText != "" {
		t.Fatalf("slot retryText = %q, want cleared override", slot.progress.retryText)
	}
	if !slot.progress.lastProgressSet.IsZero() {
		t.Fatalf("expected slot progress throttle reset, got %v", slot.progress.lastProgressSet)
	}

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-shared-handoff",
		AgentID:       "architect",
		AgentType:     "architect",
		Message:       "Designing architecture options...",
	})
	m = comp.(*Model)

	final := findEntryByCorrelation(m, "corr-shared-handoff")
	if final == nil {
		t.Fatal("expected architect entry after progress")
	}
	if final.ThinkingStatus != "Designing architecture options..." {
		t.Fatalf("thinking status = %q, want immediate architect progress", final.ThinkingStatus)
	}
}

func TestStreamCompletePreservesExplicitProgressForProgressOnlyEntry(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(88, 20)

	const status = "Validation accepted. Proceeding to closure gate."

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "task_auth_checkout:inspector-pipeline",
		AgentType:     "inspector-pipeline",
		TaskName:      "Auth checkout",
		TaskSlug:      "auth-checkout",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "task_auth_checkout:inspector-pipeline",
		AgentType:     "inspector-pipeline",
		Message:       status,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:     "s1",
		CorrelationID: "c1",
		AgentID:       "task_auth_checkout:inspector-pipeline",
		AgentType:     "inspector-pipeline",
	})
	m = comp.(*Model)

	entry := findEntryByCorrelation(m, "c1")
	if entry == nil {
		t.Fatal("expected completed progress-only entry")
	}
	if entry.Streaming {
		t.Fatalf("expected progress-only entry to be finalized, got %+v", entry)
	}
	if entry.Content != "" {
		t.Fatalf("expected progress-only entry to stay contentless, got %q", entry.Content)
	}
	if entry.ThinkingElapsed <= 0 {
		t.Fatalf("expected preserved thinking elapsed, got %v", entry.ThinkingElapsed)
	}
	if entry.ThinkingText != "" {
		t.Fatalf("expected spinner text cleared on completion, got %q", entry.ThinkingText)
	}
	if entry.ThinkingStatus != status {
		t.Fatalf("thinking status = %q, want %q", entry.ThinkingStatus, status)
	}
	if view := m.View(); !strings.Contains(view, status) {
		t.Fatalf("expected completed progress-only status to remain visible, got %q", view)
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
		ToolCallKey:   "tc-attach-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=README.md",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-tool",
		ToolCallKey:   "tc-attach-1",
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

func TestHandleToolCallEvent_ClearsToolDerivedProgressOverrideOnCompletion(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("tester-pipeline")

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tool-progress",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
	})
	m = comp.(*Model)

	const toolProgress = "Working through this with read file."
	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tool-progress",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       toolProgress,
		ToolDerived:   true,
	})
	m = comp.(*Model)

	slot := m.streamSlot("corr-tool-progress")
	if slot == nil || slot.progress.retryText != toolProgress || !slot.progress.retryToolDerived {
		t.Fatalf("expected tool-derived slot progress, got %+v", slot)
	}

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tool-progress",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=README.md",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tool-progress",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		Phase:         1,
		Duration:      50 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	slot = m.streamSlot("corr-tool-progress")
	if slot == nil {
		t.Fatal("expected active stream slot to remain present")
	}
	if slot.progress.retryText != "" || slot.progress.retryToolDerived {
		t.Fatalf("expected tool-derived progress override cleared, got retryText=%q toolDerived=%v", slot.progress.retryText, slot.progress.retryToolDerived)
	}

	entry := findEntryByCorrelation(m, "corr-tool-progress")
	if entry == nil {
		t.Fatal("expected stream entry")
	}
	if len(entry.ToolCalls) != 1 || !entry.ToolCalls[0].Completed {
		t.Fatalf("expected completed tool call row, got %+v", entry.ToolCalls)
	}
	if entry.ThinkingStatus == toolProgress {
		t.Fatalf("expected stale tool progress status cleared, got %q", entry.ThinkingStatus)
	}
}

func TestHandleToolCallEvent_ReplacesDisplayedWatchdogProgressWithToolSummary(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.BeginThinking("tester-pipeline")

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-watchdog-tool-progress",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-watchdog-tool-progress",
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Pipeline Tester is reasoning deeply...",
		Watchdog:      true,
		Sequence:      1,
	})
	m = comp.(*Model)

	const toolProgress = "Working through this with read file."
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-watchdog-tool-progress",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=README.md",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	slot := m.streamSlot("corr-watchdog-tool-progress")
	if slot == nil {
		t.Fatal("expected active stream slot")
	}
	if slot.progress.retryText != toolProgress || !slot.progress.retryToolDerived || slot.progress.retryWatchdog {
		t.Fatalf("expected tool summary to replace watchdog progress, got %+v", slot.progress)
	}

	entry := findEntryByCorrelation(m, "corr-watchdog-tool-progress")
	if entry == nil {
		t.Fatal("expected active stream entry")
	}
	if entry.ThinkingStatus != toolProgress {
		t.Fatalf("thinking status = %q, want tool-derived summary", entry.ThinkingStatus)
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

func TestStreamReroute_ClearsDeferredChildWaitWithoutConsumingSourceSlot(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector",
		AgentID:       "runtime-inspector",
		AgentType:     "inspector-pipeline",
	})
	m = comp.(*Model)

	startedAt := time.Now().Add(-50 * time.Millisecond)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Phase:         0,
		StartedAt:     startedAt,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Output:        `{"selected":true,"target_agents":["tester-pipeline"],"challenge_id":"pipeline-review-1"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      50 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamRerouteMsg{
		SessionID:             "s1",
		OriginalCorrelationID: "corr-inspector",
		CorrelationID:         "corr-tester",
		FromAgentID:           "inspector-pipeline",
		ToAgentID:             "tester-pipeline",
	})
	m = comp.(*Model)

	if _, ok := m.streams["corr-inspector"]; !ok {
		t.Fatal("expected rerouted source stream slot to remain available for terminal completion")
	}
	entry := findEntryByCorrelation(m, "corr-inspector")
	if entry == nil {
		t.Fatal("expected inspector entry to remain in history")
	}
	if strings.TrimSpace(entry.ThinkingStatus) != "" {
		t.Fatalf("expected rerouted source thinking status to clear, got %q", entry.ThinkingStatus)
	}
	if strings.TrimSpace(entry.ThinkingText) != "" {
		t.Fatalf("expected rerouted source thinking text to clear, got %q", entry.ThinkingText)
	}
	if idx := m.historyIndexForCorrelation("corr-inspector"); idx >= 0 {
		if _, pending := m.pendingInterAgent[idx]; pending {
			t.Fatal("expected rerouted source entry to stop counting as pending child work")
		}
	}
	if len(entry.ToolCalls) != 1 || entry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected rerouted source to retain a single inter-agent row, got %+v", entry.ToolCalls)
	}
	if status := entry.ToolCalls[0].InterAgent.Status; status != InterAgentToolDone {
		t.Fatalf("expected pipeline challenge row to settle on reroute, got %q", status)
	}
	if view := m.View(); strings.Contains(view, "Waiting for child work to finish...") {
		t.Fatalf("unexpected deferred child-wait status after reroute: %q", view)
	}
}

func TestTopLevelTransferStartClearsStaleNestedChildState(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	startedAt := time.Now()

	m.PushEntry(&ChatEntry{
		ID:             "resp-inspector-stale-nested",
		Timestamp:      startedAt,
		CorrelationID:  "corr-inspector",
		Source:         SourceAgent,
		AgentType:      "inspector-pipeline",
		AgentID:        "inspector-pipeline",
		Content:        "",
		Streaming:      true,
		ThinkingText:   "⠋  0.2s",
		ThinkingStatus: "Waiting on tester progress.",
		Height:         -1,
		ToolCalls: []ToolCallRecord{{
			ToolCallKey: "challenge-1",
			ToolName:    "challenge_agent",
			StartedAt:   startedAt,
			Completed:   true,
			Success:     true,
			InterAgent: &InterAgentTool{
				Kind:       InterAgentToolChallenge,
				AgentTypes: []string{"tester-pipeline"},
				Summary:    "Run the pipeline audit.",
				ThreadKey:  "pipeline:pipeline-review-stale",
				Status:     InterAgentToolDone,
				Children: []InterAgentChildActivity{{
					CorrelationID:  "corr-tester",
					AgentID:        "runtime-tester",
					AgentType:      "tester-pipeline",
					ThinkingText:   "⠋  0.1s",
					ThinkingStatus: "stale nested progress",
				}},
			},
		}},
	})
	idx := m.history.Len() - 1
	slot := &streamSlot{
		accumulator:   NewStreamAccumulator(idx),
		agentID:       "inspector-pipeline",
		thinkingIdx:   idx,
		thinkingStart: startedAt,
		progress: progressOverrideState{
			retryText: "Waiting on tester progress.",
		},
		renderState: &streamRenderState{},
	}
	m.streams["corr-inspector"] = slot
	m.pendingInterAgent[idx] = struct{}{}
	m.viewport.AddStreamState(idx, slot.renderState)
	m.nestedStreams["corr-tester"] = &nestedStreamSlot{
		correlationID: "corr-tester",
		branchRef: msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-inspector",
			ParentToolCallKey:   "challenge-1",
			ThreadKey:           "pipeline:pipeline-review-stale",
			Kind:                "challenge",
		},
		activity: InterAgentChildActivity{
			CorrelationID:  "corr-tester",
			AgentID:        "runtime-tester",
			AgentType:      "tester-pipeline",
			ThinkingText:   "⠋  0.1s",
			ThinkingStatus: "stale nested progress",
		},
	}

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-tester",
		ParentCorrelationID: "corr-inspector",
		TopLevelTransfer:    true,
		AgentID:             "runtime-tester",
		AgentType:           "tester-pipeline",
		AgentName:           "Tester",
	})
	m = comp.(*Model)

	if _, ok := m.nestedStreams["corr-tester"]; ok {
		t.Fatal("expected explicit top-level transfer to clear stale nested stream slot")
	}
	tester := findEntryByCorrelation(m, "corr-tester")
	if tester == nil {
		t.Fatal("expected tester to bootstrap as a top-level entry")
	}
	origin := findEntryByCorrelation(m, "corr-inspector")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected inspector challenge row, got %+v", origin)
	}
	if got := len(origin.ToolCalls[0].InterAgent.Children); got != 0 {
		t.Fatalf("stale nested child count = %d, want 0 after top-level transfer", got)
	}
	if _, pending := m.pendingInterAgent[m.historyIndexForCorrelation("corr-inspector")]; pending {
		t.Fatal("expected stale nested child removal to clear pending child-work state")
	}
	if view := m.View(); strings.Contains(view, "stale nested progress") {
		t.Fatalf("unexpected stale nested child render after top-level transfer: %q", view)
	}
}

func TestTopLevelTransferReturn_CreatesNewInspectorRowAcrossHandoffChain(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector-initial",
		AgentID:       "runtime-inspector",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector-initial",
		AgentID:       "runtime-inspector",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
	})
	m = comp.(*Model)

	inspector := findEntryByCorrelation(m, "corr-inspector-initial")
	if inspector == nil {
		t.Fatal("expected initial inspector entry")
	}
	if inspector.Streaming {
		t.Fatalf("expected initial inspector entry to complete before handoff return, got %+v", inspector)
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-tester",
		ParentCorrelationID: "corr-inspector-initial",
		TopLevelTransfer:    true,
		AgentID:             "runtime-tester",
		AgentType:           "tester-pipeline",
		AgentName:           "Tester",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tester",
		AgentID:       "runtime-tester",
		AgentType:     "tester-pipeline",
		AgentName:     "Tester",
	})
	m = comp.(*Model)

	historyBefore := m.history.Len()

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-inspector-return",
		ParentCorrelationID: "corr-tester",
		TopLevelTransfer:    true,
		AgentID:             "runtime-inspector",
		AgentType:           "inspector-pipeline",
		AgentName:           "Inspector",
	})
	m = comp.(*Model)

	if m.history.Len() != historyBefore+1 {
		t.Fatalf("history len = %d, want %d after creating a fresh inspector handoff row", m.history.Len(), historyBefore+1)
	}
	if original := findEntryByCorrelation(m, "corr-inspector-initial"); original == nil {
		t.Fatal("expected original inspector row to remain in the transcript")
	}
	resumed := findEntryByCorrelation(m, "corr-inspector-return")
	if resumed == nil {
		t.Fatal("expected returning inspector to create a new row")
	}
	if !resumed.Streaming {
		t.Fatalf("expected resumed inspector row to be live, got %+v", resumed)
	}
	if strings.TrimSpace(resumed.ThinkingText) == "" {
		t.Fatalf("expected resumed inspector row to restart its thinking footer, got %+v", resumed)
	}
	if tester := findEntryByCorrelation(m, "corr-tester"); tester == nil {
		t.Fatal("expected tester handoff row to remain top-level")
	}
}

func TestTopLevelTransferReturn_ReusesCompletedOwnerRowAcrossRawAgentIDMismatch(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector-parent",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		TaskID:        "task_auth_checkout",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector-parent",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "challenge",
			Status:     "pending",
			AgentTypes: []string{"tester-pipeline"},
			Summary:    "Re-run the audit.",
			ThreadKey:  "pipeline:task_auth_checkout-challenge-1",
		},
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-inspector-parent",
		ParentToolCallKey:   "challenge-1",
		Kind:                "challenge",
		ThreadKey:           "pipeline:task_auth_checkout-challenge-1",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tester-child",
		AgentID:       "task_auth_checkout:tester-pipeline",
		AgentType:     "tester-pipeline",
		AgentName:     "Tester",
		TaskID:        "task_auth_checkout",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-inspector-parent",
		AgentID:           "inspector-pipeline",
		AgentType:         "inspector-pipeline",
		AgentName:         "Inspector",
		TaskID:            "task_auth_checkout",
		AuthoritativeText: "Waiting on the tester challenge result.",
	})
	m = comp.(*Model)

	if !m.HasPendingCorrelation("corr-inspector-parent") {
		t.Fatal("expected completed inspector row to remain resumable")
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-inspector-return",
		ParentCorrelationID: "corr-tester-child",
		TopLevelTransfer:    true,
		AgentID:             "task_auth_checkout:inspector-pipeline",
		RuntimeAgentID:      "runtime-inspector",
		AgentType:           "inspector-pipeline",
		AgentName:           "Inspector",
		TaskID:              "task_auth_checkout",
	})
	m = comp.(*Model)

	if old := findEntryByCorrelation(m, "corr-inspector-parent"); old != nil {
		t.Fatalf("expected original resumable correlation to be replaced after visible-identity-matched return, got %+v", old)
	}
	resumed := findEntryByCorrelation(m, "corr-inspector-return")
	if resumed == nil {
		t.Fatal("expected resumed inspector row")
	}
	if resumed.Content != "Waiting on the tester challenge result." {
		t.Fatalf("resumed inspector content = %q, want preserved content", resumed.Content)
	}
	if !resumed.Streaming {
		t.Fatalf("expected resumed inspector row to be live after return, got %+v", resumed)
	}
}

func TestTopLevelTransferReturn_ReusesCompletedOwnerRowWhenTaskIdentityArrivesOnResume(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector-parent-unscoped",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector-parent-unscoped",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "challenge",
			Status:     "pending",
			AgentTypes: []string{"tester-pipeline"},
			Summary:    "Re-run the audit.",
			ThreadKey:  "pipeline:task_auth_checkout-challenge-1",
		},
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-inspector-parent-unscoped",
		ParentToolCallKey:   "challenge-1",
		Kind:                "challenge",
		ThreadKey:           "pipeline:task_auth_checkout-challenge-1",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tester-child-unscoped",
		AgentID:       "task_auth_checkout:tester-pipeline",
		AgentType:     "tester-pipeline",
		AgentName:     "Tester",
		TaskID:        "task_auth_checkout",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-inspector-parent-unscoped",
		AgentID:           "inspector-pipeline",
		AgentType:         "inspector-pipeline",
		AgentName:         "Inspector",
		AuthoritativeText: "Waiting on the tester challenge result.",
	})
	m = comp.(*Model)

	if !m.HasPendingCorrelation("corr-inspector-parent-unscoped") {
		t.Fatal("expected unscoped inspector row to remain resumable")
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-inspector-return-unscoped",
		ParentCorrelationID: "corr-tester-child-unscoped",
		TopLevelTransfer:    true,
		AgentID:             "task_auth_checkout:inspector-pipeline",
		RuntimeAgentID:      "runtime-inspector",
		AgentType:           "inspector-pipeline",
		AgentName:           "Inspector",
		TaskID:              "task_auth_checkout",
	})
	m = comp.(*Model)

	if old := findEntryByCorrelation(m, "corr-inspector-parent-unscoped"); old != nil {
		t.Fatalf("expected original unscoped inspector correlation to be replaced on child-owner resume, got %+v", old)
	}
	resumed := findEntryByCorrelation(m, "corr-inspector-return-unscoped")
	if resumed == nil {
		t.Fatal("expected resumed inspector row after task-scoped identity arrived")
	}
	if resumed.Content != "Waiting on the tester challenge result." {
		t.Fatalf("resumed inspector content = %q, want preserved content", resumed.Content)
	}
	if resumed.TaskID != "task_auth_checkout" {
		t.Fatalf("resumed inspector task_id = %q, want task_auth_checkout", resumed.TaskID)
	}
	if !resumed.Streaming {
		t.Fatalf("expected resumed inspector row to be live after child-owner resume, got %+v", resumed)
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
		ToolCallKey:   "tc-consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"question":"Is there a cleaner approach?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-consult",
		ToolCallKey:   "tc-consult-1",
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

func TestHandleStreamReroute_SettlesGlobalReviewChallengeRows(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	startedAt := time.Now()

	m.PushEntry(&ChatEntry{
		ID:             "resp-global-review-reroute",
		Timestamp:      startedAt,
		CorrelationID:  "corr-global-inspector",
		Source:         SourceAgent,
		AgentType:      "inspector",
		AgentID:        "inspector",
		Content:        "",
		Streaming:      true,
		ThinkingText:   "⠋  0.2s",
		ThinkingStatus: "Reviewing...",
		Height:         -1,
	})
	idx := m.history.Len() - 1
	slot := &streamSlot{
		accumulator:   NewStreamAccumulator(idx),
		agentID:       "inspector",
		thinkingIdx:   idx,
		thinkingStart: startedAt,
		renderState:   &streamRenderState{},
	}
	m.streams["corr-global-inspector"] = slot
	m.viewport.AddStreamState(idx, slot.renderState)

	comp, _ := m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-global-inspector",
		ToolCallKey:   "challenge-global-1",
		ToolName:      "challenge_global_tester",
		FullArgs:      `{"request":"Audit the merged state."}`,
		Phase:         0,
		StartedAt:     startedAt,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-global-inspector",
		ToolCallKey:   "challenge-global-1",
		ToolName:      "challenge_global_tester",
		FullArgs:      `{"request":"Audit the merged state."}`,
		Output:        `{"selected":true,"target_agent":"tester","challenge_id":"global-review-1"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      50 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamRerouteMsg{
		SessionID:             "s1",
		OriginalCorrelationID: "corr-global-inspector",
		CorrelationID:         "corr-global-tester",
		FromAgentID:           "inspector",
		ToAgentID:             "tester",
	})
	m = comp.(*Model)

	entry := findEntryByCorrelation(m, "corr-global-inspector")
	if entry == nil {
		t.Fatal("expected global inspector entry to remain in history")
	}
	if strings.TrimSpace(entry.ThinkingStatus) != "" {
		t.Fatalf("expected rerouted global inspector thinking status to clear, got %q", entry.ThinkingStatus)
	}
	if idx := m.historyIndexForCorrelation("corr-global-inspector"); idx >= 0 {
		if _, pending := m.pendingInterAgent[idx]; pending {
			t.Fatal("expected rerouted global-review source entry to stop counting as pending child work")
		}
	}
	if len(entry.ToolCalls) != 1 || entry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected rerouted global-review source to retain a single inter-agent row, got %+v", entry.ToolCalls)
	}
	if status := entry.ToolCalls[0].InterAgent.Status; status != InterAgentToolDone {
		t.Fatalf("expected global-review challenge row to settle on reroute, got %q", status)
	}
	if view := m.View(); strings.Contains(view, "Waiting for child work to finish...") {
		t.Fatalf("unexpected deferred child-wait status after global-review reroute: %q", view)
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
	if regions[m.viewport.selectedRegion].kind != selectionRegionToolCallOverflow {
		t.Fatalf("selected region kind = %v, want overflow control after overflow expansion", regions[m.viewport.selectedRegion].kind)
	}
	if got := len(findRenderedLines(t, m.View(), "hide")); got != 1 {
		t.Fatalf("expected expanded child branch to keep a single hide control row, got %d", got)
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
	if liveTarget == nil || liveTarget.kind != toggleTargetOverflow {
		t.Fatalf("expected live line %d to remain on overflow control after mutation, got %+v", overflowLine, liveTarget)
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
	if got := len(findRenderedLines(t, m.View(), "hide")); got != 1 {
		t.Fatalf("expected expanded first child to render one hide control row, got %d", got)
	}
	if got := len(findRenderedLines(t, m.View(), "earlier events")); got != 1 {
		t.Fatalf("expected second child to keep one remaining earlier-events control row, got %d", got)
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

func TestToggleAtViewLine_TargetsDeepNestedChildToolCall(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(100, 20)
	m.PushEntry(&ChatEntry{
		ID:        "deep-child-tool-targets",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the research plan.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare evidence collection approaches",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic-deep-click",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-librarian-deep-click",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Collect benchmark evidence",
										Status:     InterAgentToolDone,
										Children: []InterAgentChildActivity{
											{
												CorrelationID: "child-librarian-deep-click",
												AgentType:     "librarian",
												ToolCalls: []ToolCallRecord{
													{
														ToolName:    "read_file",
														ArgsSummary: "path=docs/benchmarks.md",
														FullArgs:    `{"path":"docs/benchmarks.md"}`,
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
	deepLine := -1
	for _, region := range regions {
		if region.kind != selectionRegionChildToolCall {
			continue
		}
		if len(region.childPath) == 2 && len(region.interAgentPath) == 1 && region.childPath[0] == 0 && region.childPath[1] == 0 && region.interAgentPath[0] == 0 {
			deepLine = region.start
			break
		}
	}
	if deepLine < 0 {
		t.Fatalf("expected deep nested child tool region, got %#v", regions)
	}
	target := m.viewport.ToggleTargetAtViewLine(deepLine)
	if target == nil {
		t.Fatal("expected deep nested child tool toggle target")
	}
	if got := target.childPath; len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Fatalf("toggle target childPath = %#v, want [0 0]", got)
	}
	if got := target.interAgentPath; len(got) != 1 || got[0] != 0 {
		t.Fatalf("toggle target interAgentPath = %#v, want [0]", got)
	}
	if target.childToolCallName != "read_file" {
		t.Fatalf("toggle target childToolCallName = %q, want read_file", target.childToolCallName)
	}

	if !m.ToggleAtViewLine(deepLine) {
		t.Fatal("expected deep nested child tool row toggle to succeed")
	}

	entry := m.history.Last()
	if entry == nil {
		t.Fatal("expected entry after deep nested child toggle")
	}
	consult := entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[0]
	if consult.Expanded {
		t.Fatalf("expected intermediate consult row to stay collapsed, got %+v", consult)
	}
	deepCalls := consult.InterAgent.Children[0].ToolCalls
	if len(deepCalls) != 1 || !deepCalls[0].Expanded {
		t.Fatalf("expected deep nested child tool row expanded, got %+v", deepCalls)
	}
}

func TestKeyboardNavigationAndSpaceToggle_DeepNestedChildToolCall(t *testing.T) {
	m := New(theme.DefaultDark(), 32)
	m.SetSize(100, 20)
	m.SetFocused(true)
	m.PushEntry(&ChatEntry{
		ID:        "deep-child-tool-space-toggle",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the research plan.",
		Height:    -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare evidence collection approaches",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic-deep-space",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-librarian-deep-space",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Collect benchmark evidence",
										Status:     InterAgentToolDone,
										Children: []InterAgentChildActivity{
											{
												CorrelationID: "child-librarian-deep-space",
												AgentType:     "librarian",
												ToolCalls: []ToolCallRecord{
													{
														ToolName:    "read_file",
														ArgsSummary: "path=docs/benchmarks.md",
														FullArgs:    `{"path":"docs/benchmarks.md"}`,
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
	selectedRegion := -1
	for idx, region := range regions {
		if region.kind != selectionRegionChildToolCall {
			continue
		}
		if len(region.childPath) == 2 && len(region.interAgentPath) == 1 && region.childPath[0] == 0 && region.childPath[1] == 0 && region.interAgentPath[0] == 0 && region.childToolCallIdx == 0 {
			selectedRegion = idx
			break
		}
	}
	if selectedRegion < 0 {
		t.Fatalf("expected deep nested child tool selection region, got %#v", regions)
	}
	m.viewport.selectEntry(0, selectedRegion)

	comp, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = comp.(*Model)

	entry := m.history.Last()
	if entry == nil {
		t.Fatal("expected entry after deep nested keyboard toggle")
	}
	consult := entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[0]
	if consult.Expanded {
		t.Fatalf("expected intermediate consult row to stay collapsed after space toggle, got %+v", consult)
	}
	deepCalls := consult.InterAgent.Children[0].ToolCalls
	if len(deepCalls) != 1 || !deepCalls[0].Expanded {
		t.Fatalf("expected deep nested child tool row expanded after space toggle, got %+v", deepCalls)
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
		ToolCallKey:   "tc-metadata-1",
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
		ToolCallKey:   "tc-metadata-1",
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

func TestHandleToolCallEvent_GlobalChallengeKeepsResponderOnOriginRowAcrossResponseAndValidation(t *testing.T) {
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
		ToolCallKey:   "tc-global-challenge-1",
		ToolName:      "challenge_architect",
		FullArgs:      `{"reason":"Need plan clarification","request":"Reassess the testing scope.","protocol_scope":"global_review"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-global-challenge",
		ToolCallKey:   "tc-global-challenge-1",
		ToolName:      "challenge_architect",
		FullArgs:      `{"reason":"Need plan clarification","request":"Reassess the testing scope.","protocol_scope":"global_review"}`,
		Output:        `{"selected":true,"target_agent":"architect","challenge_id":"global-review-123","protocol_scope":"global_review"}`,
		Phase:         1,
		Duration:      120 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-global-validate",
		ToolName:      "validate_global_review",
		FullArgs:      `{"challenge_id":"global-review-123","requesting_agent":"inspector","status":"passed","summary":"The plan should be revised to strengthen integration coverage.","protocol_scope":"global_review"}`,
		Output:        `{"validated":true,"challenge_id":"global-review-123","requesting_agent":"inspector","responding_agent":"architect","status":"passed","protocol_scope":"global_review"}`,
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
		FullArgs:      `{"challenge_id":"global-review-123","decision":"accept","summary":"Accepted the architect response and will proceed with the revised scope.","protocol_scope":"global_review"}`,
		Output:        `{"processed":true,"challenge_id":"global-review-123","decision":"accept","protocol_scope":"global_review"}`,
		Phase:         1,
		Duration:      70 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-global-challenge")
	if got := origin.ToolCalls[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "architect" {
		t.Fatalf("origin validation agent types = %#v, want [architect]", got)
	}
	if !strings.Contains(origin.ToolCalls[0].InterAgent.Summary, "Accepted the architect response") {
		t.Fatalf("origin validation summary = %q", origin.ToolCalls[0].InterAgent.Summary)
	}
	if processor := findEntryByCorrelation(m, "corr-global-process"); processor == nil || len(processor.ToolCalls) != 0 {
		t.Fatalf("expected no duplicate tool row on processing entry, got %+v", processor)
	}
}

func TestHandleToolCallEvent_PipelineChallengeKeepsResponderOnOriginRowAcrossResponseAndValidation(t *testing.T) {
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
		ToolCallKey:   "tc-pipeline-challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"reason":"Need verification","request":"Run the final pipeline audit.","protocol_scope":"pipeline"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-pipeline-challenge",
		ToolCallKey:   "tc-pipeline-challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"reason":"Need verification","request":"Run the final pipeline audit.","protocol_scope":"pipeline"}`,
		Output:        `{"selected":true,"target_agents":["tester-pipeline"],"challenge_id":"pipeline-review-456","protocol_scope":"pipeline"}`,
		Phase:         1,
		Duration:      110 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-pipeline-validate",
		ToolName:      "validate_work",
		FullArgs:      `{"challenge_id":"pipeline-review-456","requesting_agent":"inspector-pipeline","status":"passed","summary":"The final pipeline audit passed with solid test evidence.","protocol_scope":"pipeline"}`,
		Output:        `{"validated":true,"challenge_id":"pipeline-review-456","requesting_agent":"inspector-pipeline","responding_agent":"tester-pipeline","status":"passed","protocol_scope":"pipeline"}`,
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
		FullArgs:      `{"challenge_id":"pipeline-review-456","decision":"accept","summary":"Accepted the tester validation and will move toward OT handoff.","protocol_scope":"pipeline"}`,
		Output:        `{"processed":true,"challenge_id":"pipeline-review-456","decision":"accept","protocol_scope":"pipeline"}`,
		Phase:         1,
		Duration:      75 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-pipeline-challenge")
	if got := origin.ToolCalls[0].InterAgent.AgentTypes; len(got) != 1 || got[0] != "tester-pipeline" {
		t.Fatalf("pipeline validation agent types = %#v, want [tester-pipeline]", got)
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

func TestNestedToolCompletion_ClearsToolDerivedChildProgressOverride(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-progress",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-nested-progress",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Checking the supporting evidence.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-nested-progress",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"question":"Is there a cleaner harness structure?"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-nested-progress",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested-progress",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	const toolProgress = "Working through this with read file."
	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested-progress",
		AgentID:       "academic",
		AgentType:     "academic",
		Message:       toolProgress,
		ToolDerived:   true,
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	slot := m.nestedStream("corr-child-nested-progress")
	if slot == nil || slot.progress.retryText != toolProgress || !slot.progress.retryToolDerived {
		t.Fatalf("expected nested tool-derived progress, got %+v", slot)
	}

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested-progress",
		AgentID:       "academic",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=ui/chat/model.go",
		Phase:         0,
		StartedAt:     time.Now(),
		BranchRef:     branchRef,
	})
	m = comp.(*Model)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested-progress",
		AgentID:       "academic",
		ToolCallKey:   "read-1",
		ToolName:      "read_file",
		Phase:         1,
		Duration:      25 * time.Millisecond,
		Success:       true,
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	slot = m.nestedStream("corr-child-nested-progress")
	if slot == nil {
		t.Fatal("expected nested stream slot to remain active")
	}
	if slot.progress.retryText != "" || slot.progress.retryToolDerived {
		t.Fatalf("expected nested tool-derived progress override cleared, got retryText=%q toolDerived=%v", slot.progress.retryText, slot.progress.retryToolDerived)
	}

	origin := findEntryByCorrelation(m, "corr-parent-nested-progress")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil || len(origin.ToolCalls[0].InterAgent.Children) != 1 {
		t.Fatalf("expected one nested child row, got %+v", origin)
	}
	child := origin.ToolCalls[0].InterAgent.Children[0]
	if len(child.ToolCalls) != 1 || !child.ToolCalls[0].Completed {
		t.Fatalf("expected completed nested child tool row, got %+v", child.ToolCalls)
	}
	if child.ThinkingStatus == toolProgress {
		t.Fatalf("expected stale nested tool progress status cleared, got %q", child.ThinkingStatus)
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

func TestNestedChildConsultRowsRemainVisibleAfterChildStreamEmitsText(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(100, 24)
	m.PushEntry(&ChatEntry{
		ID:             "architect-origin-child-consult-after-text",
		Timestamp:      time.Now(),
		CorrelationID:  "corr-parent-child-consult-after-text",
		Source:         SourceAgent,
		AgentType:      "architect",
		Content:        "Architect draft text should stay hidden while the consult runs.",
		Streaming:      true,
		ThinkingText:   "⠋  0.3s",
		ThinkingStatus: "Waiting on academic consult.",
		Height:         -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-1",
				StartedAt:   time.Now().Add(-500 * time.Millisecond),
				Completed:   true,
				Success:     true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Build the research evidence base.",
					Status:     InterAgentToolDone,
				},
			},
		},
	})

	parentBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-consult-after-text",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-consult-after-text",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     parentBranch,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamChunkMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-consult-after-text",
		Text:          "Initial academic draft text that should not leak into the parent entry.",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-consult-after-text",
		AgentID:       "academic",
		AgentType:     "academic",
		ToolCallKey:   "consult-lib-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"librarian","query":"Find benchmark and methodology sources."}`,
		Phase:         0,
		StartedAt:     time.Now().Add(-250 * time.Millisecond),
		BranchRef:     parentBranch,
	})
	m = comp.(*Model)

	librarianBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-child-academic-consult-after-text",
		ParentToolCallKey:   "consult-lib-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-librarian-consult-after-text",
		AgentID:       "librarian",
		AgentType:     "librarian",
		BranchRef:     librarianBranch,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-librarian-consult-after-text",
		AgentID:       "librarian",
		AgentType:     "librarian",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=framework benchmark methodology",
		FullArgs:      `{"query":"framework benchmark methodology"}`,
		Phase:         0,
		StartedAt:     time.Now().Add(-150 * time.Millisecond),
		BranchRef:     librarianBranch,
	})
	m = comp.(*Model)

	view := m.View()
	if !strings.Contains(view, "Architect draft text should stay hidden while the consult runs.") {
		t.Fatalf("expected parent content to remain visible while nested consult work is active, got %q", view)
	}
	for _, needle := range []string{
		"academic",
		"librarian",
		"Find benchmark and methodology sources.",
		"web_search",
		"framework benchmark methodology",
	} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected nested consult view to contain %q, got %q", needle, view)
		}
	}
}

func TestPrimaryChildSyncPreservesNestedGrandchildState(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-primary-child-merge",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-primary-child-merge",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the research plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-primary-child-merge",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Build the research evidence base."}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	parentBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-primary-child-merge",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-primary-child-merge",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     parentBranch,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-primary-child-merge",
		AgentID:       "academic",
		AgentType:     "academic",
		ToolCallKey:   "consult-lib-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"librarian","query":"Find benchmark and methodology sources."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		BranchRef:     parentBranch,
	})
	m = comp.(*Model)

	librarianBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-child-academic-primary-child-merge",
		ParentToolCallKey:   "consult-lib-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-librarian-primary-child-merge",
		AgentID:       "librarian",
		AgentType:     "librarian",
		BranchRef:     librarianBranch,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-librarian-primary-child-merge",
		AgentID:       "librarian",
		AgentType:     "librarian",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=framework benchmark methodology",
		FullArgs:      `{"query":"framework benchmark methodology"}`,
		Phase:         0,
		StartedAt:     time.Now(),
		BranchRef:     librarianBranch,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-primary-child-merge")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	academic := origin.ToolCalls[0].InterAgent.Children[0]
	consult := academic.ToolCalls[0].InterAgent
	if consult == nil || len(consult.Children) != 1 {
		t.Fatalf("expected nested librarian child before academic resync, got %+v", academic.ToolCalls)
	}
	if got := len(consult.Children[0].ToolCalls); got != 1 {
		t.Fatalf("expected librarian child tool row before academic resync, got %+v", consult.Children[0].ToolCalls)
	}

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-primary-child-merge",
		AgentID:       "academic",
		AgentType:     "academic",
		Message:       "Refining the evidence synthesis.",
		BranchRef:     parentBranch,
	})
	m = comp.(*Model)

	origin = findEntryByCorrelation(m, "corr-parent-primary-child-merge")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row after academic resync, got %+v", origin)
	}
	academic = origin.ToolCalls[0].InterAgent.Children[0]
	if got := len(academic.ToolCalls); got != 1 {
		t.Fatalf("expected academic child tool calls to survive resync, got %+v", academic.ToolCalls)
	}
	consult = academic.ToolCalls[0].InterAgent
	if consult == nil || len(consult.Children) != 1 {
		t.Fatalf("expected nested librarian child to survive academic resync, got %+v", academic.ToolCalls[0])
	}
	librarian := consult.Children[0]
	if librarian.CorrelationID != "corr-child-librarian-primary-child-merge" {
		t.Fatalf("librarian correlation = %q, want corr-child-librarian-primary-child-merge", librarian.CorrelationID)
	}
	if got := len(librarian.ToolCalls); got != 1 || librarian.ToolCalls[0].ToolName != "web_search" {
		t.Fatalf("expected librarian child tool row to survive academic resync, got %+v", librarian.ToolCalls)
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

func TestNestedChildInterAgentConsultStartAttachesByParentCorrelationWhenToolKeyIsStale(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-consult-stale-key",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-consult-stale-key",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-consult-stale-key",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Compare harness options"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-consult-stale-key",
		ParentToolCallKey:   "consult-stale",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-consult-stale-key",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-child-consult-stale-key")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	row := origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity, got %+v", row.Children)
	}
	child := row.Children[0]
	if child.CorrelationID != "corr-child-consult-stale-key" {
		t.Fatalf("child correlation id = %q, want corr-child-consult-stale-key", child.CorrelationID)
	}
	if child.AgentType != "academic" {
		t.Fatalf("child agent type = %q, want academic", child.AgentType)
	}
	if findEntryByCorrelation(m, "corr-child-consult-stale-key") != nil {
		t.Fatal("expected stale-key child stream to remain nested, not top-level")
	}
}

func TestNestedChildInterAgentConsultStartDoesNotAttachByParentCorrelationWhenStaleKeyIsAmbiguous(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-consult-stale-key-ambiguous",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-consult-stale-key-ambiguous",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-consult-stale-key-ambiguous",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Compare harness options"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-consult-stale-key-ambiguous",
		ToolCallKey:   "consult-2",
		ToolName:      "consult_librarian_style",
		FullArgs:      `{"target":"librarian","query":"Find related patterns"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-consult-stale-key-ambiguous",
		ParentToolCallKey:   "consult-stale",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-consult-stale-key-ambiguous",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-child-consult-stale-key-ambiguous")
	if origin == nil || len(origin.ToolCalls) != 2 {
		t.Fatalf("expected two parent consult rows, got %+v", origin)
	}
	for idx, record := range origin.ToolCalls {
		if record.InterAgent == nil {
			t.Fatalf("expected consult row %d to be inter-agent, got %+v", idx, record)
		}
		if got := len(record.InterAgent.Children); got != 0 {
			t.Fatalf("consult row %d child count = %d, want 0 for ambiguous stale parent key", idx, got)
		}
	}
	if findEntryByCorrelation(m, "corr-child-consult-stale-key-ambiguous") != nil {
		t.Fatal("expected ambiguous stale-key child stream to avoid a top-level entry")
	}
}

func TestNestedGrandchildGuardianCompletionAttachesToImmediateApprovalBranch(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-grandchild-guardian",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-grandchild-guardian",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-grandchild-guardian",
		ToolCallKey:   "consult-academic-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"academic","query":"Research implementation options."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "consult",
			Status:     "pending",
			AgentTypes: []string{"academic"},
			Summary:    "Research implementation options.",
		},
	})
	m = comp.(*Model)

	parentBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-grandchild-guardian",
		ParentToolCallKey:   "consult-academic-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-grandchild-guardian",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     parentBranch,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-grandchild-guardian",
		AgentID:       "academic",
		AgentType:     "academic",
		ToolCallKey:   "approval-guardian-1",
		ToolName:      "approval_guardian",
		FullArgs:      `{"target":"guardian","tool_name":"web_fetch","domain":"example.com","summary":"Requesting Guardian approval for example.com"}`,
		Phase:         0,
		StartedAt:     time.Now(),
		BranchRef:     parentBranch,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			Status:     "pending",
			AgentTypes: []string{"guardian"},
			Summary:    "Requesting Guardian approval for example.com",
		},
	})
	m = comp.(*Model)

	approvalBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-child-academic-grandchild-guardian",
		ParentToolCallKey:   "approval-guardian-1",
		Kind:                "approval",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-grandchild-guardian-approval",
		AgentID:       "guardian",
		AgentType:     "guardian",
		BranchRef:     approvalBranch,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-grandchild-guardian-approval",
		AgentID:           "guardian",
		AgentType:         "guardian",
		AuthoritativeText: "Fetch approval allowed",
		BranchRef:         approvalBranch,
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-grandchild-guardian")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	root := origin.ToolCalls[0].InterAgent
	if len(root.Children) != 1 {
		t.Fatalf("expected one academic child activity, got %+v", root.Children)
	}
	academic := root.Children[0]
	if got := len(academic.ToolCalls); got != 1 {
		t.Fatalf("academic child tool call count = %d, want 1", got)
	}
	approval := academic.ToolCalls[0].InterAgent
	if approval == nil {
		t.Fatalf("expected approval branch, got %+v", academic.ToolCalls[0])
	}
	if len(approval.Children) != 1 {
		t.Fatalf("expected one guardian grandchild activity, got %+v", approval.Children)
	}
	guardian := approval.Children[0]
	if guardian.CorrelationID != "corr-grandchild-guardian-approval" {
		t.Fatalf("guardian correlation = %q, want corr-grandchild-guardian-approval", guardian.CorrelationID)
	}
	if guardian.AgentType != "guardian" {
		t.Fatalf("guardian agent type = %q, want guardian", guardian.AgentType)
	}
	if !guardian.Completed {
		t.Fatalf("expected guardian grandchild to be completed, got %+v", guardian)
	}
	if guardian.ResultSummary != "Fetch approval allowed" {
		t.Fatalf("guardian result summary = %q, want %q", guardian.ResultSummary, "Fetch approval allowed")
	}
	if findEntryByCorrelation(m, "corr-grandchild-guardian-approval") != nil {
		t.Fatal("expected guardian grandchild stream to remain nested, not top-level")
	}
}

func TestGuardianApprovalCompletionClearsTopLevelProgressFooter(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-top-guardian-approval",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-top-guardian-approval",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Waiting for Guardian approval for run_command",
	})
	m = comp.(*Model)

	startedAt := time.Now().Add(-250 * time.Millisecond)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-top-guardian-approval",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		ToolCallKey:   "approval-1",
		ToolName:      "approval_guardian",
		FullArgs:      `{"target":"guardian","tool_name":"run_command","summary":"Waiting for Guardian approval for run_command"}`,
		Phase:         0,
		StartedAt:     startedAt,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			Status:     "pending",
			AgentTypes: []string{"guardian"},
			Summary:    "Waiting for Guardian approval for run_command",
		},
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-top-guardian-approval",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Guardian approval received for run_command",
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-top-guardian-approval",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		ToolCallKey:   "approval-1",
		ToolName:      "approval_guardian",
		FullArgs:      `{"target":"guardian","tool_name":"run_command","summary":"Waiting for Guardian approval for run_command"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      250 * time.Millisecond,
		Success:       true,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			Status:     "done",
			AgentTypes: []string{"guardian"},
			Summary:    "Guardian approval received for run_command",
		},
	})
	m = comp.(*Model)

	entry := findEntryByCorrelation(m, "corr-top-guardian-approval")
	if entry == nil {
		t.Fatal("expected top-level guardian approval entry")
	}
	if strings.TrimSpace(entry.ThinkingText) != "" || strings.TrimSpace(entry.ThinkingStatus) != "" {
		t.Fatalf("expected guardian approval footer to clear after approval completion, got text=%q status=%q", entry.ThinkingText, entry.ThinkingStatus)
	}
	if len(entry.ToolCalls) != 1 || !entry.ToolCalls[0].Completed || entry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected completed guardian approval tool call, got %+v", entry.ToolCalls)
	}
	if entry.ToolCalls[0].InterAgent.Status != InterAgentToolDone {
		t.Fatalf("expected guardian approval branch to be done, got %+v", entry.ToolCalls[0].InterAgent)
	}
}

func TestGuardianApprovalCompletionClearsNestedChildProgressFooter(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.PushEntry(&ChatEntry{
		ID:            "architect-origin-child-guardian-progress",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-child-guardian-progress",
		Source:        SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
	})

	comp, _ := m.Update(msg.ToolCallEventMsg{
		CorrelationID: "corr-parent-child-guardian-progress",
		ToolCallKey:   "consult-1",
		ToolName:      "consult_academic_approach",
		FullArgs:      `{"target":"academic","query":"Research implementation options."}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-child-guardian-progress",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	comp, _ = m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-guardian-progress",
		AgentID:       "academic",
		AgentType:     "academic",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-guardian-progress",
		AgentID:       "academic",
		AgentType:     "academic",
		Message:       "Waiting for Guardian approval for run_command",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	startedAt := time.Now().Add(-250 * time.Millisecond)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-guardian-progress",
		AgentID:       "academic",
		AgentType:     "academic",
		ToolCallKey:   "approval-1",
		ToolName:      "approval_guardian",
		FullArgs:      `{"target":"guardian","tool_name":"run_command","summary":"Waiting for Guardian approval for run_command"}`,
		Phase:         0,
		StartedAt:     startedAt,
		BranchRef:     branchRef,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			Status:     "pending",
			AgentTypes: []string{"guardian"},
			Summary:    "Waiting for Guardian approval for run_command",
		},
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-guardian-progress",
		AgentID:       "academic",
		AgentType:     "academic",
		Message:       "Guardian approval received for run_command",
		BranchRef:     branchRef,
	})
	m = comp.(*Model)

	comp, _ = m.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-guardian-progress",
		AgentID:       "academic",
		AgentType:     "academic",
		ToolCallKey:   "approval-1",
		ToolName:      "approval_guardian",
		FullArgs:      `{"target":"guardian","tool_name":"run_command","summary":"Waiting for Guardian approval for run_command"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      250 * time.Millisecond,
		Success:       true,
		BranchRef:     branchRef,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			Status:     "done",
			AgentTypes: []string{"guardian"},
			Summary:    "Guardian approval received for run_command",
		},
	})
	m = comp.(*Model)

	origin := findEntryByCorrelation(m, "corr-parent-child-guardian-progress")
	if origin == nil || len(origin.ToolCalls) != 1 || origin.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row, got %+v", origin)
	}
	row := origin.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity, got %+v", row.Children)
	}
	child := row.Children[0]
	if strings.TrimSpace(child.ThinkingText) != "" || strings.TrimSpace(child.ThinkingStatus) != "" {
		t.Fatalf("expected nested guardian approval footer to clear after approval completion, got text=%q status=%q", child.ThinkingText, child.ThinkingStatus)
	}
	if len(child.ToolCalls) != 1 || !child.ToolCalls[0].Completed || child.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected completed nested guardian approval tool call, got %+v", child.ToolCalls)
	}
	if child.ToolCalls[0].InterAgent.Status != InterAgentToolDone {
		t.Fatalf("expected nested guardian approval branch to be done, got %+v", child.ToolCalls[0].InterAgent)
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

func TestHandleStreamComplete_FinalizesParentWhileInterAgentChildrenRemainPending(t *testing.T) {
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

	final := findEntryByCorrelation(m, entry.CorrelationID)
	if final == nil {
		t.Fatal("expected completed parent entry")
	}
	if final.Streaming {
		t.Fatalf("expected parent completion to finalize immediately, got %+v", final)
	}
	if final.Content != "Architect response is ready." {
		t.Fatalf("parent content = %q, want authoritative finalized content", final.Content)
	}
	if strings.TrimSpace(final.ThinkingText) != "" || strings.TrimSpace(final.ThinkingStatus) != "" {
		t.Fatalf("expected parent waiting footer to clear after completion, got %+v", final)
	}
	if slot := m.streamSlot(entry.CorrelationID); slot != nil {
		t.Fatalf("expected parent stream slot to be cleared after completion, got %+v", slot)
	}
	if !m.HasPendingCorrelation(entry.CorrelationID) {
		t.Fatal("expected completed parent correlation to remain resumable")
	}
	view := m.View()
	if !strings.Contains(view, "Architect response is ready.") {
		t.Fatalf("expected parent content to stay visible in the chat view, got %q", view)
	}
	if !strings.Contains(view, "Still researching source quality.") {
		t.Fatalf("expected nested child activity to stay visible after parent completion, got %q", view)
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

	final = findEntryByCorrelation(m, entry.CorrelationID)
	if final == nil {
		t.Fatal("expected completed parent entry after child settlement")
	}
	if final.Streaming {
		t.Fatalf("expected completed parent entry to stay finalized once child settles, got %+v", final)
	}
	if !m.HasPendingCorrelation(entry.CorrelationID) {
		t.Fatal("expected completed parent correlation to remain resumable for follow-up")
	}
}

func TestHandleStreamComplete_FinalizesProgressOnlyParentWhileNestedChildPending(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	entry := &ChatEntry{
		ID:             "inspector-parent-deferred-child-pending-no-content",
		Timestamp:      time.Now(),
		CorrelationID:  "corr-parent-deferred-no-content",
		Source:         SourceAgent,
		AgentType:      "inspector-pipeline",
		AgentID:        "task_auth_checkout:inspector-pipeline",
		Content:        "",
		Streaming:      true,
		ThinkingText:   "⠋  2.0s",
		ThinkingStatus: "Drafting the closure decision...",
		Height:         -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "challenge_agent",
				ToolCallKey: "challenge-1",
				Completed:   true,
				Success:     true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"tester-pipeline"},
					Summary:    "Re-run the audit against the corrected workspace.",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:     "corr-child-tester-pending-no-content",
							AgentType:         "tester-pipeline",
							Completed:         false,
							ThinkingStartedAt: time.Now().Add(-200 * time.Millisecond),
							ThinkingText:      "⠋  0.2s",
							ThinkingStatus:    "Running the audit again.",
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
		agentID:       "inspector-pipeline",
		thinkingIdx:   idx,
		thinkingStart: time.Now().Add(-2 * time.Second),
		renderState:   &streamRenderState{},
	}
	m.streams = map[string]*streamSlot{
		entry.CorrelationID: slot,
	}
	m.viewport.AddStreamState(idx, slot.renderState)
	m.syncPendingInterAgentEntry(idx)

	comp, _ := m.Update(msg.StreamCompleteMsg{
		SessionID:     "s1",
		CorrelationID: entry.CorrelationID,
		AgentID:       entry.AgentID,
		AgentType:     entry.AgentType,
	})
	m = comp.(*Model)

	final := findEntryByCorrelation(m, entry.CorrelationID)
	if final == nil {
		t.Fatal("expected completed progress-only parent entry")
	}
	if final.Streaming {
		t.Fatalf("expected progress-only parent completion to finalize, got %+v", final)
	}
	if strings.TrimSpace(final.ThinkingText) != "" || strings.TrimSpace(final.ThinkingStatus) != "" {
		t.Fatalf("expected parent waiting footer to clear, got %+v", final)
	}
	if slot := m.streamSlot(entry.CorrelationID); slot != nil {
		t.Fatalf("expected progress-only parent stream slot to be cleared, got %+v", slot)
	}
	if !m.HasPendingCorrelation(entry.CorrelationID) {
		t.Fatal("expected progress-only parent correlation to remain resumable")
	}

	beforeText := final.ToolCalls[0].InterAgent.Children[0].ThinkingText
	comp, _ = m.Update(msg.DecorTickMsg{Time: time.Now().Add(600 * time.Millisecond)})
	m = comp.(*Model)
	final = findEntryByCorrelation(m, entry.CorrelationID)
	if final == nil {
		t.Fatal("expected completed progress-only parent entry after decor tick")
	}
	child := final.ToolCalls[0].InterAgent.Children[0]
	if child.ThinkingText == beforeText {
		t.Fatalf("expected nested child spinner to continue animating after parent completion, got %q", child.ThinkingText)
	}
	if view := m.View(); !strings.Contains(view, "Re-run the audit against the corrected workspace.") {
		t.Fatalf("expected nested child branch to remain visible, got %q", view)
	}

	m.history.UpdateAt(idx, func(e *ChatEntry) {
		child := &e.ToolCalls[0].InterAgent.Children[0]
		child.Completed = true
		child.ThinkingText = ""
		child.ThinkingStatus = ""
		invalidateChatEntryRender(e)
	})
	m.syncPendingInterAgentEntry(idx)

	final = findEntryByCorrelation(m, entry.CorrelationID)
	if final == nil {
		t.Fatal("expected completed progress-only parent entry")
	}
	if final.Streaming {
		t.Fatalf("expected progress-only parent entry to remain finalized once child challenge completes, got %+v", final)
	}
	if !m.HasPendingCorrelation(entry.CorrelationID) {
		t.Fatal("expected progress-only parent correlation to remain resumable for follow-up")
	}
}

func TestDecorTick_AnimatesHistoryBackedPendingNestedChildThinking(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	startedAt := time.Now().Add(-200 * time.Millisecond)
	entry := &ChatEntry{
		ID:            "inspector-history-backed-child-pending",
		Timestamp:     startedAt,
		CorrelationID: "corr-inspector-history-backed-child-pending",
		Source:        SourceAgent,
		AgentType:     "inspector-pipeline",
		AgentID:       "task_auth_checkout:inspector-pipeline",
		Content:       "Inspector is waiting for the tester handoff.",
		Height:        -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "challenge_agent",
				ToolCallKey: "challenge-1",
				StartedAt:   startedAt,
				Completed:   true,
				Success:     true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"tester-pipeline"},
					Summary:    "Prepare the pipeline tester handoff.",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:     "corr-history-backed-tester",
							AgentType:         "tester-pipeline",
							ThinkingStartedAt: startedAt,
							ThinkingText:      "⠋  0.2s",
							ThinkingStatus:    "Waiting for challenge instructions.",
							ThinkingColor:     "#7dcfff",
							Completed:         false,
						},
					},
				},
			},
		},
	}
	m.PushEntry(entry)
	idx := m.history.Len() - 1
	m.pendingInterAgent[idx] = struct{}{}

	before := findEntryByCorrelation(m, entry.CorrelationID)
	if before == nil {
		t.Fatal("expected pending parent entry")
	}
	beforeChild := before.ToolCalls[0].InterAgent.Children[0]

	comp, _ := m.Update(msg.DecorTickMsg{Time: startedAt.Add(900 * time.Millisecond)})
	m = comp.(*Model)

	after := findEntryByCorrelation(m, entry.CorrelationID)
	if after == nil {
		t.Fatal("expected pending parent entry after decor tick")
	}
	child := after.ToolCalls[0].InterAgent.Children[0]
	if child.ThinkingText == beforeChild.ThinkingText {
		t.Fatalf("expected history-backed child spinner/timer to animate, got %q", child.ThinkingText)
	}
	if child.ThinkingStatus != beforeChild.ThinkingStatus {
		t.Fatalf("expected history-backed child status to be preserved, got %q want %q", child.ThinkingStatus, beforeChild.ThinkingStatus)
	}
	if child.ThinkingColor == "" {
		t.Fatalf("expected history-backed child color to remain active, got %+v", child)
	}
	if child.ThinkingStartedAt.IsZero() {
		t.Fatalf("expected history-backed child thinking start to remain set, got %+v", child)
	}
}

func TestHandleStreamStart_ReusesDeferredPipelineWorkerEntryForSameRawAgentID(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	entry := &ChatEntry{
		ID:             "inspector-parent-reuse-deferred",
		Timestamp:      time.Now(),
		CorrelationID:  "corr-old-inspector",
		Source:         SourceAgent,
		AgentType:      "inspector-pipeline",
		AgentID:        "inspector-pipeline",
		TaskID:         "task_auth_checkout",
		Content:        "Existing audit context.",
		Streaming:      true,
		ThinkingText:   "⠋  1.8s",
		ThinkingStatus: deferredParentCompletionStatus,
		Height:         -1,
	}
	m.PushEntry(entry)
	idx := m.history.Len() - 1

	slot := &streamSlot{
		accumulator:   NewStreamAccumulator(idx),
		agentID:       "inspector-pipeline",
		thinkingIdx:   idx,
		thinkingStart: time.Now().Add(-2 * time.Second),
		progress: progressOverrideState{
			retryText:       deferredParentCompletionStatus,
			lastProgressSet: time.Now(),
		},
		renderState:     &streamRenderState{},
		deferCompletion: true,
	}
	m.streams = map[string]*streamSlot{
		entry.CorrelationID: slot,
	}
	m.viewport.AddStreamState(idx, slot.renderState)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-new-inspector",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		TaskID:        "task_auth_checkout",
	})
	m = comp.(*Model)

	if m.history.Len() != 1 {
		t.Fatalf("history len = %d, want 1 reused entry", m.history.Len())
	}
	if _, ok := m.streams["corr-old-inspector"]; ok {
		t.Fatal("expected old deferred inspector correlation to be replaced")
	}
	newSlot := m.streamSlot("corr-new-inspector")
	if newSlot == nil {
		t.Fatal("expected new inspector correlation to reuse deferred slot")
	}
	if newSlot.accumulator.EntryIndex() != idx {
		t.Fatalf("reused slot entry index = %d, want %d", newSlot.accumulator.EntryIndex(), idx)
	}
	if newSlot.deferCompletion {
		t.Fatal("expected reused slot to resume active streaming instead of staying deferred")
	}

	reused := findEntryByCorrelation(m, "corr-new-inspector")
	if reused == nil {
		t.Fatal("expected deferred inspector entry correlation to update")
	}
	if reused.Content != "Existing audit context." {
		t.Fatalf("reused entry content = %q, want preserved prior context", reused.Content)
	}
	if strings.Contains(reused.ThinkingStatus, deferredParentCompletionStatus) {
		t.Fatalf("expected deferred waiting status to clear on resumed stream, got %q", reused.ThinkingStatus)
	}
}

// TestHandleStreamStart_OriginatorContinuationResumesExistingEntry covers the
// pipeline challenge → validate_work → inspector-resumption flow. When a
// StreamStartMsg carries ContinuationOfCorrelationID, the TUI must append
// the new correlation's work into the existing entry (no new chat block) and
// settle any pending challenge row whose child's response just arrived.
func TestHandleStreamStart_OriginatorContinuationResumesExistingEntry(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	// Seed an inspector entry that already contains a challenge_agent row
	// whose nested child (tester-pipeline) is still Pending.
	entry := &ChatEntry{
		ID:            "inspector-origin-continuation",
		Timestamp:     time.Now(),
		CorrelationID: "corr-inspector",
		Source:        SourceAgent,
		AgentType:     "inspector-pipeline",
		AgentID:       "inspector-pipeline",
		TaskID:        "task_auth",
		Content:       "Pre-challenge audit work.",
		Streaming:     false,
		Height:        -1,
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "challenge_agent",
				Completed: false,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"tester-pipeline"},
					Status:     InterAgentToolPending,
					ThreadKey:  "pipeline:challenge-1",
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "corr-tester",
							AgentType:     "tester-pipeline",
						},
					},
				},
			},
		},
	}
	m.PushEntry(entry)

	// validate_work dispatched from tester now arrives as a new forwarded
	// request to the inspector. The routing layer stamps:
	//   ContinuationOfCorrelationID = originator (inspector) CID
	//   ParentCorrelationID          = responder (tester) CID
	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:                   "s1",
		CorrelationID:               "corr-inspector-resume",
		ContinuationOfCorrelationID: "corr-inspector",
		ParentCorrelationID:         "corr-tester",
		AgentID:                     "inspector-pipeline",
		AgentType:                   "inspector-pipeline",
		TaskID:                      "task_auth",
	})
	m = comp.(*Model)

	// No second entry — the inspector resumed its existing one.
	if m.history.Len() != 1 {
		t.Fatalf("history len = %d, want 1 (continuation must not create a new entry)", m.history.Len())
	}

	// The new correlation must resolve to the same existing entry.
	newSlot := m.streamSlot("corr-inspector-resume")
	if newSlot == nil {
		t.Fatal("expected new correlation to register a stream slot")
	}
	if newSlot.accumulator == nil || newSlot.accumulator.EntryIndex() != 0 {
		t.Fatalf("slot entry index = %v, want 0 (same entry)",
			func() any {
				if newSlot.accumulator == nil {
					return "nil"
				}
				return newSlot.accumulator.EntryIndex()
			}())
	}

	// The pending challenge row for the responder child must be settled.
	resumed := m.history.Get(0)
	if resumed == nil || len(resumed.ToolCalls) == 0 || resumed.ToolCalls[0].InterAgent == nil {
		t.Fatal("expected resumed entry to preserve challenge_agent row")
	}
	if resumed.ToolCalls[0].InterAgent.Status == InterAgentToolPending {
		t.Fatalf("challenge row status = %q, want settled (Done)",
			resumed.ToolCalls[0].InterAgent.Status)
	}

	// The original correlation should still resolve to the same entry (both
	// pre- and post-continuation CIDs point to the inspector entry).
	if idx := m.historyIndexForCorrelation("corr-inspector"); idx != 0 {
		t.Fatalf("original CID resolution = %d, want 0", idx)
	}
	if idx := m.historyIndexForCorrelation("corr-inspector-resume"); idx != 0 {
		t.Fatalf("new CID resolution = %d, want 0", idx)
	}
}

func TestHandleStreamStart_ReusesDeferredPipelineWorkerEntryAcrossRawAgentIDMismatchWhenVisibleIdentityMatches(t *testing.T) {
	m := New(theme.DefaultDark(), 16)
	m.SetSize(96, 24)

	entry := &ChatEntry{
		ID:             "inspector-parent-reuse-deferred-raw-id",
		Timestamp:      time.Now(),
		CorrelationID:  "corr-old-inspector-raw-id",
		Source:         SourceAgent,
		AgentType:      "inspector-pipeline",
		AgentID:        "inspector-pipeline",
		TaskID:         "task_auth_checkout",
		Content:        "Existing audit context.",
		Streaming:      true,
		ThinkingText:   "⠋  1.8s",
		ThinkingStatus: deferredParentCompletionStatus,
		Height:         -1,
	}
	m.PushEntry(entry)
	idx := m.history.Len() - 1

	slot := &streamSlot{
		accumulator:   NewStreamAccumulator(idx),
		agentID:       "inspector-pipeline",
		thinkingIdx:   idx,
		thinkingStart: time.Now().Add(-2 * time.Second),
		progress: progressOverrideState{
			retryText:       deferredParentCompletionStatus,
			lastProgressSet: time.Now(),
		},
		renderState:     &streamRenderState{},
		deferCompletion: true,
	}
	m.streams = map[string]*streamSlot{
		entry.CorrelationID: slot,
	}
	m.viewport.AddStreamState(idx, slot.renderState)

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-new-inspector-raw-id",
		AgentID:       "task_auth_checkout:inspector-pipeline",
		AgentType:     "inspector-pipeline",
		TaskID:        "task_auth_checkout",
	})
	m = comp.(*Model)

	if m.history.Len() != 1 {
		t.Fatalf("history len = %d, want 1 reused entry after visible-identity match", m.history.Len())
	}
	if _, ok := m.streams["corr-old-inspector-raw-id"]; ok {
		t.Fatal("expected old deferred inspector correlation to be replaced after visible-identity match")
	}
	newSlot := m.streamSlot("corr-new-inspector-raw-id")
	if newSlot == nil {
		t.Fatal("expected visible-identity-matched inspector correlation to reuse the deferred slot")
	}
	if newSlot.accumulator.EntryIndex() != idx {
		t.Fatalf("reused slot entry index = %d, want %d", newSlot.accumulator.EntryIndex(), idx)
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
