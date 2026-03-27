package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/events"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	chatpkg "github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/msg"
	statuspkg "github.com/adalundhe/sylk/ui/status"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
)

func newStreamTelemetryModel() *AppModel {
	th := theme.DefaultDark()
	return &AppModel{
		agentContextTokens:      make(map[string]int),
		agentContextModels:      make(map[string]string),
		streamUsage:             make(map[string]streamUsageEntry),
		streamedResponses:       make(map[string]streamedResponseState),
		activeStreams:           make(map[string]*activeStreamEntry),
		nestedStreams:           make(map[string]*activeStreamEntry),
		reroutedStreamCIDs:      make(map[string]time.Time),
		interruptedCorrelations: make(map[string]struct{}),
		agentPanel:              agentpkg.New(th),
		chat:                    chatpkg.New(th, 32),
		statusBar:               statuspkg.New(th, nil),
	}
}

func TestStreamTelemetry_FinalizeUpdatesAgentContext(t *testing.T) {
	m := newStreamTelemetryModel()

	m.trackStreamStart(msg.StreamStartMsg{CorrelationID: "corr-1", AgentID: "architect", AgentType: "architect"})
	m.trackStreamChunk("corr-1", "design a robust migration plan")
	m.finalizeStreamUsage("corr-1", true, "")

	if _, ok := m.streamUsage["corr-1"]; ok {
		t.Fatal("expected stream state to be removed after finalize")
	}
	if m.agentContextTokens["architect"] <= 0 {
		t.Fatalf("expected architect context tokens > 0, got %d", m.agentContextTokens["architect"])
	}
}

func TestStreamTelemetry_UsesLatestPerTurnInputTokensForPipelineTester(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("task_auth_checkout:tester-pipeline", "tester-pipeline", "Pipeline Tester", nil, "", "")

	start := msg.StreamStartMsg{
		CorrelationID: "corr-tester",
		AgentID:       "runtime-tester",
		AgentType:     "tester-pipeline",
		AgentName:     "Tester",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
	}
	m.trackStreamStart(start)

	model, _ := m.Update(msg.TokenUsageMsg{
		CorrelationID: "corr-tester",
		AgentID:       "runtime-tester",
		Model:         "gpt-5.4-pro",
		InputTokens:   120000,
	})
	m = model.(*AppModel)
	model, _ = m.Update(msg.TokenUsageMsg{
		CorrelationID: "corr-tester",
		AgentID:       "runtime-tester",
		Model:         "gpt-5.4-pro",
		InputTokens:   210000,
	})
	m = model.(*AppModel)

	canonicalID := "task_auth_checkout:tester-pipeline"
	if got := m.agentContextTokens[canonicalID]; got != 210000 {
		t.Fatalf("agentContextTokens[%q] = %d, want 210000", canonicalID, got)
	}
	if got := m.streamUsage["corr-tester"].InputTokens; got != 210000 {
		t.Fatalf("streamUsage input tokens = %d, want 210000", got)
	}

	m.applyRealStreamUsage(msg.StreamCompleteMsg{CorrelationID: "corr-tester", InputTokens: 330000})
	if got := m.streamUsage["corr-tester"].InputTokens; got != 210000 {
		t.Fatalf("streamUsage input tokens after complete = %d, want 210000", got)
	}

	m.finalizeStreamUsage("corr-tester", true, "")
	if got := m.agentContextTokens[canonicalID]; got != 210000 {
		t.Fatalf("finalized agentContextTokens[%q] = %d, want 210000", canonicalID, got)
	}
	if got := m.agentPanel.ContextUsageOf(canonicalID); got < 0.77 || got > 0.78 {
		t.Fatalf("panel context usage = %.4f, want about 0.7721", got)
	}
}

func TestStreamTelemetry_TokenUsageWithoutCorrelationPreservesPipelineEngineerOccupancy(t *testing.T) {
	m := newStreamTelemetryModel()
	canonicalID := "task_auth_checkout:engineer"
	m.agentPanel.SeedAgent(canonicalID, "engineer", "Engineer", nil, "", "")

	start := msg.StreamStartMsg{
		CorrelationID: "corr-engineer",
		AgentID:       "runtime-engineer",
		AgentType:     "engineer",
		AgentName:     "Engineer",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
	}
	m.trackStreamStart(start)

	model, _ := m.Update(msg.TokenUsageMsg{
		AgentID:     canonicalID,
		Model:       "gpt-5.4-pro",
		InputTokens: 210000,
	})
	m = model.(*AppModel)

	if got := m.streamUsage["corr-engineer"].InputTokens; got != 210000 {
		t.Fatalf("streamUsage input tokens = %d, want 210000", got)
	}

	m.applyRealStreamUsage(msg.StreamCompleteMsg{
		CorrelationID: "corr-engineer",
		InputTokens:   540000,
	})
	if got := m.streamUsage["corr-engineer"].InputTokens; got != 210000 {
		t.Fatalf("streamUsage input tokens after complete = %d, want preserved 210000", got)
	}

	m.finalizeStreamUsage("corr-engineer", true, "")
	if got := m.agentContextTokens[canonicalID]; got != 210000 {
		t.Fatalf("finalized agentContextTokens[%q] = %d, want 210000", canonicalID, got)
	}
	if got := m.agentPanel.ContextUsageOf(canonicalID); got < 0.77 || got > 0.78 {
		t.Fatalf("panel context usage = %.4f, want about 0.7721", got)
	}
}

func TestStreamTelemetry_TokenUsageOverridesStalePanelContext(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("task_auth_checkout:tester-pipeline", "tester-pipeline", "Pipeline Tester", nil, "", "")
	m.agentPanel.SyncContextUsage("task_auth_checkout:tester-pipeline", 1.0)

	start := msg.StreamStartMsg{
		CorrelationID: "corr-tester-stale",
		AgentID:       "runtime-tester",
		AgentType:     "tester-pipeline",
		AgentName:     "Tester",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
	}
	m.trackStreamStart(start)

	model, _ := m.Update(msg.TokenUsageMsg{
		CorrelationID: "corr-tester-stale",
		AgentID:       "runtime-tester",
		Model:         "gpt-5.4-pro",
		InputTokens:   27200,
	})
	m = model.(*AppModel)

	if got := m.agentPanel.ContextUsageOf("task_auth_checkout:tester-pipeline"); got < 0.09 || got > 0.11 {
		t.Fatalf("panel context usage = %.4f, want about 0.10 after real token sync", got)
	}
}

func TestStreamTelemetry_TokenUsageOverridesStalePanelContextForStandaloneAgent(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("inspector", "inspector", "Inspector", nil, "", "")
	m.agentPanel.SyncContextUsage("inspector", 1.0)

	model, _ := m.Update(msg.TokenUsageMsg{
		AgentID:     "inspector",
		Model:       "gpt-5.4-pro",
		InputTokens: 27200,
	})
	m = model.(*AppModel)

	if got := m.agentPanel.ContextUsageOf("inspector"); got < 0.09 || got > 0.11 {
		t.Fatalf("panel context usage = %.4f, want about 0.10 after real token sync", got)
	}
}

func TestStreamTelemetry_TokenUsageOverridesStalePanelContextForGuide(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("guide", "guide", "Guide", nil, "", "")
	m.agentPanel.SyncContextUsage("guide", 1.0)

	model, _ := m.Update(msg.TokenUsageMsg{
		AgentID:     "guide",
		Model:       "gpt-5.4-pro",
		InputTokens: 50000,
	})
	m = model.(*AppModel)

	if got := m.agentPanel.ContextUsageOf("guide"); got <= 0 || got >= 1.0 {
		t.Fatalf("panel context usage = %.4f, want reset below 1.0 after real token sync", got)
	}
}

func TestStreamTelemetry_UsesModelSpecificContextLimit(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("tester", "tester", "Tester", nil, "", "")

	ratio := m.setAgentContextUsage("tester", 210000)
	if ratio >= 1.0 {
		t.Fatalf("ratio = %.4f, want < 1.0 for gpt-5.4-pro context window", ratio)
	}
}

func TestStreamTelemetry_HandoffCompletedResetsPipelineContextUsage(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("task_auth_checkout:tester-pipeline", "tester-pipeline", "Pipeline Tester", nil, "", "")
	m.agentContextModels["task_auth_checkout:tester-pipeline"] = "gpt-5.4-pro"
	m.setAgentContextUsage("task_auth_checkout:tester-pipeline", 210000)

	m.applyActivityTelemetry(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_handoff_complete",
			EventType: events.EventTypeSuccess,
			Timestamp: time.Now(),
			AgentID:   "4d6b407a",
			Content:   "Context handoff complete",
			Data: map[string]any{
				"agent_type":     "tester-pipeline",
				"pipeline_id":    "task_auth_checkout",
				"task_id":        "task_auth_checkout",
				"handoff_state":  "completed",
				"context_tokens": 0,
			},
		},
	})

	if got := m.agentContextTokens["task_auth_checkout:tester-pipeline"]; got != 0 {
		t.Fatalf("agentContextTokens = %d, want 0 after handoff completion", got)
	}
	if got := m.agentPanel.ContextUsageOf("task_auth_checkout:tester-pipeline"); got != 0 {
		t.Fatalf("panel context usage = %.4f, want 0 after handoff completion", got)
	}
}

func TestStreamTelemetry_EmptyAgentFallsBackToGuide(t *testing.T) {
	m := newStreamTelemetryModel()

	m.trackStreamStart(msg.StreamStartMsg{CorrelationID: "corr-2"})
	m.trackStreamChunk("corr-2", "hello world")
	m.finalizeStreamUsage("corr-2", true, "")

	if m.agentContextTokens[guideAgentID] <= 0 {
		t.Fatalf("expected guide context tokens > 0, got %d", m.agentContextTokens[guideAgentID])
	}
}

func TestStreamTelemetry_UnknownCorrelationNoop(t *testing.T) {
	m := newStreamTelemetryModel()

	m.trackStreamChunk("missing", "ignored chunk")
	m.finalizeStreamUsage("missing", false, "boom")

	if len(m.streamUsage) != 0 {
		t.Fatalf("expected no stream state, got %d", len(m.streamUsage))
	}
}

func TestStreamTelemetry_SuppressesChunkedRouteResponse(t *testing.T) {
	m := newStreamTelemetryModel()

	m.recordStreamStart("corr-3")
	m.recordStreamChunk("corr-3", "hello")
	m.recordStreamComplete("corr-3")

	if !m.shouldSuppressStreamedRouteResponse("corr-3", false) {
		t.Fatal("expected chunked stream route response to be suppressed")
	}
	if m.shouldSuppressStreamedRouteResponse("corr-3", false) {
		t.Fatal("expected suppression state to be cleared after first check")
	}
}

func TestStreamTelemetry_DoesNotSuppressProgressOnlyRouteResponse(t *testing.T) {
	m := newStreamTelemetryModel()

	m.recordStreamStart("corr-4")
	m.recordStreamComplete("corr-4")

	if m.shouldSuppressStreamedRouteResponse("corr-4", false) {
		t.Fatal("did not expect suppression for stream without content chunks")
	}
}

func TestStreamTelemetry_ShouldSuppressErrorAfterSuccessfulRouteResponse(t *testing.T) {
	m := newStreamTelemetryModel()

	m.recordStreamStart("corr-5")
	m.recordStreamChunk("corr-5", "partial answer")
	m.recordStreamComplete("corr-5")
	m.markSuccessfulRouteResponse("corr-5")

	if !m.shouldSuppressErrorAfterSuccess("corr-5") {
		t.Fatal("expected errors to be suppressed after successful response")
	}
}

func TestStreamTelemetry_DoesNotSuppressErrorBeforeSuccess(t *testing.T) {
	m := newStreamTelemetryModel()

	m.recordStreamStart("corr-6")
	m.recordStreamComplete("corr-6")

	if m.shouldSuppressErrorAfterSuccess("corr-6") {
		t.Fatal("did not expect errors to be suppressed before successful response")
	}
}

func TestStreamTelemetry_RegisterStreamCanonicalizesPipelineAgent(t *testing.T) {
	m := newStreamTelemetryModel()

	start := msg.StreamStartMsg{
		CorrelationID: "corr-pipeline",
		AgentID:       "dc484039",
		AgentType:     "designer",
		AgentName:     "Designer",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		TaskSlug:      "auth-checkout",
	}
	m.trackStreamStart(start)
	m.registerStream(start)

	entry := m.activeStreams["corr-pipeline"]
	if entry == nil {
		t.Fatal("expected active stream entry")
	}
	if entry.AgentID != "task_auth_checkout:designer" {
		t.Fatalf("entry.AgentID = %q, want task_auth_checkout:designer", entry.AgentID)
	}
	if entry.PipelineID != "task_auth_checkout" {
		t.Fatalf("entry.PipelineID = %q, want task_auth_checkout", entry.PipelineID)
	}
}

func TestStreamTelemetry_TaskIDOverridesRuntimePipelineID(t *testing.T) {
	m := newStreamTelemetryModel()

	start := msg.StreamStartMsg{
		CorrelationID: "corr-pipeline-runtime",
		AgentID:       "dc484039",
		AgentType:     "designer",
		AgentName:     "Designer",
		PipelineID:    "runtime-pipeline-123",
		TaskID:        "task_auth_checkout",
		TaskSlug:      "auth-checkout",
	}
	m.trackStreamStart(start)
	m.registerStream(start)

	entry := m.activeStreams["corr-pipeline-runtime"]
	if entry == nil {
		t.Fatal("expected active stream entry")
	}
	if entry.AgentID != "task_auth_checkout:designer" {
		t.Fatalf("entry.AgentID = %q, want task_auth_checkout:designer", entry.AgentID)
	}
	if entry.PipelineID != "task_auth_checkout" {
		t.Fatalf("entry.PipelineID = %q, want task_auth_checkout", entry.PipelineID)
	}
}

func TestStreamTelemetry_RegisterStreamReplacesOlderPipelineWorkerStream(t *testing.T) {
	m := newStreamTelemetryModel()

	first := msg.StreamStartMsg{
		CorrelationID: "corr-old",
		AgentID:       "old-runtime-id",
		AgentType:     "engineer",
		AgentName:     "Engineer",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
	}
	second := msg.StreamStartMsg{
		CorrelationID: "corr-new",
		AgentID:       "new-runtime-id",
		AgentType:     "engineer",
		AgentName:     "Engineer",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
	}

	m.trackStreamStart(first)
	m.registerStream(first)
	m.trackStreamStart(second)
	m.registerStream(second)

	if _, ok := m.activeStreams["corr-old"]; ok {
		t.Fatal("expected older pipeline-worker stream to be evicted")
	}
	if _, ok := m.reroutedStreamCIDs["corr-old"]; !ok {
		t.Fatal("expected older stream correlation to be marked for terminal cleanup")
	}
	entry := m.activeStreams["corr-new"]
	if entry == nil {
		t.Fatal("expected replacement active stream entry")
	}
	if entry.AgentID != "task_auth_checkout:engineer" {
		t.Fatalf("entry.AgentID = %q, want task_auth_checkout:engineer", entry.AgentID)
	}
}

func TestHandleGuideResponse_PreservesSemanticPipelineAgentLabel(t *testing.T) {
	m := newStreamTelemetryModel()
	m.chat.SetSize(80, 20)

	start := msg.StreamStartMsg{
		CorrelationID: "corr-response",
		AgentID:       "dc484039",
		AgentType:     "designer",
		AgentName:     "Designer",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		TaskName:      "Auth checkout",
		TaskSlug:      "auth-checkout",
	}
	m.trackStreamStart(start)
	m.registerStream(start)

	if cmd := m.handleGuideResponse(msg.GuideResponseMsg{
		CorrelationID: "corr-response",
		AgentID:       "dc484039",
		AgentName:     "Designer",
		Content:       "Finished the design pass.",
	}); cmd != nil {
		_ = cmd()
	}

	view := m.chat.View()
	if !strings.Contains(view, "Auth Checkout: Designer") {
		t.Fatalf("expected semantic badge in chat view, got:\n%s", view)
	}
	if strings.Contains(view, "dc484039") {
		t.Fatalf("expected runtime ID to be hidden from chat badge, got:\n%s", view)
	}
}

func TestHandleGuideResponse_NestedBranchDoesNotCreateTopLevelChatEntry(t *testing.T) {
	m := newStreamTelemetryModel()
	m.chat.SetSize(80, 20)
	m.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "parent-entry",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-response",
		Source:        chatpkg.SourceAgent,
		AgentType:     "architect",
		Content:       "Planning.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-1",
				ToolName:    "consult_librarian_style",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"librarian"},
					Summary:    "Checking prior patterns",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	if cmd := m.handleGuideResponse(msg.GuideResponseMsg{
		CorrelationID: "corr-child-response",
		AgentID:       "librarian",
		AgentType:     "librarian",
		Content:       "Found a prior pattern worth reusing.",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-response",
			ParentToolCallKey:   "consult-1",
			Kind:                "consult",
		},
	}); cmd != nil {
		_ = cmd()
	}

	view := m.chat.View()
	if !strings.Contains(view, "Found a prior pattern worth reusing.") {
		t.Fatalf("expected nested child summary in chat view, got:\n%s", view)
	}

	seenChildTopLevel := false
	for y := 0; y < m.chat.ViewportHeight(); y++ {
		entry := m.chat.EntryAtViewLine(y)
		if entry == nil {
			continue
		}
		if entry.CorrelationID == "corr-child-response" {
			seenChildTopLevel = true
			break
		}
	}
	if seenChildTopLevel {
		t.Fatal("expected nested guide response to avoid creating a top-level chat entry")
	}
}

func TestStreamTelemetry_PublishedStreamActivitiesCarryTaskName(t *testing.T) {
	m := newStreamTelemetryModel()
	collector := events.NewTestActivityCollector()
	m.deps.ActivityPub = collector

	start := msg.StreamStartMsg{
		CorrelationID: "corr-task-name",
		AgentID:       "runtime-engineer",
		AgentType:     "engineer",
		AgentName:     "Engineer",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		TaskName:      "Auth checkout",
		TaskSlug:      "auth-checkout",
	}
	m.trackStreamStart(start)
	m.registerStream(start)

	m.publishStreamStartActivity(start)
	m.publishStreamActivity(start.CorrelationID, true, "")

	published := collector.Events()
	if len(published) != 2 {
		t.Fatalf("published event count = %d, want 2", len(published))
	}
	for i, event := range published {
		if got := activityDataString(event.Data, "task_name"); got != "Auth checkout" {
			t.Fatalf("event %d task_name = %q, want Auth checkout", i, got)
		}
	}
}

func TestInterruptedCorrelationRemainsBlockedAfterTerminalEvents(t *testing.T) {
	m := newStreamTelemetryModel()
	m.interruptedCorrelations["corr-interrupted"] = struct{}{}

	if cmd := m.handleStreamCompleteTelemetry(msg.StreamCompleteMsg{
		CorrelationID:     "corr-interrupted",
		AuthoritativeText: "stale completion",
	}); cmd != nil {
		_ = cmd()
	}
	if _, ok := m.interruptedCorrelations["corr-interrupted"]; !ok {
		t.Fatal("expected interrupted correlation tombstone to survive stream completion")
	}

	if cmd := m.handleGuideResponse(msg.GuideResponseMsg{
		CorrelationID: "corr-interrupted",
		Content:       "stale final response",
	}); cmd != nil {
		_ = cmd()
	}
	if _, ok := m.interruptedCorrelations["corr-interrupted"]; !ok {
		t.Fatal("expected interrupted correlation tombstone to survive guide response")
	}

	if cmd := m.handleStreamErrorTelemetry(msg.StreamErrorMsg{
		CorrelationID: "corr-interrupted",
	}); cmd != nil {
		_ = cmd()
	}
	if _, ok := m.interruptedCorrelations["corr-interrupted"]; !ok {
		t.Fatal("expected interrupted correlation tombstone to survive stream error")
	}

	if cmd := m.handleStreamReroute(msg.StreamRerouteMsg{
		OriginalCorrelationID: "corr-interrupted",
		CorrelationID:         "corr-resurrected",
		FromAgentID:           "inspector-pipeline",
		ToAgentID:             "tester-pipeline",
	}); cmd != nil {
		_ = cmd()
	}
	if _, ok := m.activeStreams["corr-resurrected"]; ok {
		t.Fatal("expected reroute for interrupted correlation to be dropped")
	}
}

func TestStreamProgressTelemetryBootstrapsChatEntry(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-progress",
		AgentID:       "a1b2c3d4",
		AgentType:     "inspector-pipeline",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		Message:       "Reviewing carefully...",
	})
	app = model.(*AppModel)

	if !app.chat.IsStreaming() {
		t.Fatal("expected progress-first stream to bootstrap chat streaming state")
	}
	if view := app.chat.View(); !strings.Contains(view, "Reviewing carefully...") {
		t.Fatalf("expected bootstrapped chat view to include progress status, got %q", view)
	}
}

func TestStreamProgressTelemetry_PreservesNestedBranchOnBootstrapStart(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	collector := events.NewTestActivityCollector()
	app.deps.ActivityPub = collector
	_, _ = app.prepareStreamStart(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-progress",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "parent-progress-entry",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-progress",
		Source:        chatpkg.SourceAgent,
		AgentType:     "architect",
		Content:       "Planning.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-1",
				ToolName:    "consult_librarian_style",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"librarian"},
					Summary:    "Checking prior patterns",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	model, _ := app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-progress",
		AgentID:       "librarian",
		AgentType:     "librarian",
		Message:       "Looking for related patterns.",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-progress",
			ParentToolCallKey:   "consult-1",
			Kind:                "consult",
		},
	})
	app = model.(*AppModel)

	view := app.chat.View()
	if !strings.Contains(view, "Looking for related patterns.") {
		t.Fatalf("expected nested progress text in chat view, got:\n%s", view)
	}
	if _, ok := app.activeStreams["corr-child-progress"]; ok {
		t.Fatal("expected nested child stream bootstrap to avoid primary active stream registration")
	}
	if _, ok := app.nestedStreams["corr-child-progress"]; !ok {
		t.Fatal("expected nested child stream bootstrap to stay registered for rendering and cleanup")
	}
	if correlationID, _ := app.resolveInterruptTarget(); correlationID != "corr-parent-progress" {
		t.Fatalf("resolveInterruptTarget() correlation = %q, want corr-parent-progress", correlationID)
	}
	published := collector.Events()
	if len(published) != 1 {
		t.Fatalf("published event count = %d, want 1 parent-only start event", len(published))
	}
	if got := canonicalActivityAgentID(published[0]); got != "architect" {
		t.Fatalf("published start activity agent = %q, want architect", got)
	}
	for y := 0; y < app.chat.ViewportHeight(); y++ {
		entry := app.chat.EntryAtViewLine(y)
		if entry == nil {
			continue
		}
		if entry.CorrelationID == "corr-child-progress" {
			t.Fatal("expected progress-bootstrap child stream to remain nested, not top-level")
		}
	}
}

func TestStreamStartTelemetry_PreservesExistingNestedBranchWhenLaterStartDropsMetadata(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	_, _ = app.prepareStreamStart(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-reset",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "parent-reset-entry",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-reset",
		Source:        chatpkg.SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-1",
				ToolName:    "consult_librarian_style",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"librarian"},
					Summary:    "Checking prior patterns",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-reset",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-reset",
			ParentToolCallKey:   "consult-1",
			Kind:                "consult",
		},
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-reset",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
	})
	app = model.(*AppModel)

	if _, ok := app.activeStreams["corr-child-reset"]; ok {
		t.Fatal("expected metadata-less retry start to preserve nested ownership")
	}
	entry, ok := app.nestedStreams["corr-child-reset"]
	if !ok || entry == nil || entry.BranchRef == nil {
		t.Fatal("expected child stream to remain registered as nested")
	}
	if entry.BranchRef.ParentCorrelationID != "corr-parent-reset" || entry.BranchRef.ParentToolCallKey != "consult-1" {
		t.Fatalf("unexpected nested branch ref after retry start: %+v", entry.BranchRef)
	}
	for y := 0; y < app.chat.ViewportHeight(); y++ {
		entry := app.chat.EntryAtViewLine(y)
		if entry != nil && entry.CorrelationID == "corr-child-reset" {
			t.Fatal("expected metadata-less retry start to avoid a top-level chat row")
		}
	}
}

func TestStreamProgressTelemetry_IgnoresLateNestedProgressAfterTerminalComplete(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-approval-late",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
	})
	app = model.(*AppModel)

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "academic-origin-approval-late",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-approval-late",
		Source:        chatpkg.SourceAgent,
		AgentType:     "academic",
		Content:       "Researching current guidance.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-guardian-1",
				ToolName:    "consult_guardian",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"guardian"},
					Summary:    "Waiting for Guardian approval",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-approval-late",
		ParentToolCallKey:   "consult-guardian-1",
		Kind:                "consult",
	}

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-child-approval-late",
		AgentID:           "guardian",
		AgentType:         "guardian",
		AuthoritativeText: "Fetch approval allowed",
		BranchRef:         branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-approval-late",
		AgentID:       "guardian",
		AgentType:     "guardian",
		Message:       "Validating fetch approval request",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	if _, ok := app.nestedStreams["corr-child-approval-late"]; ok {
		t.Fatal("expected late child progress after terminal completion to be ignored")
	}
	if _, ok := app.activeStreams["corr-child-approval-late"]; ok {
		t.Fatal("expected late child progress to avoid active stream registration")
	}
	if strings.Contains(app.chat.View(), "Validating fetch approval request") {
		t.Fatal("expected late child progress text to stay out of chat after completion")
	}
}

func TestGuideResponse_IgnoresLateNestedProgressAfterSyntheticComplete(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-approval-route",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
	})
	app = model.(*AppModel)

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "academic-origin-approval-route",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-approval-route",
		Source:        chatpkg.SourceAgent,
		AgentType:     "academic",
		Content:       "Researching current guidance.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-guardian-1",
				ToolName:    "consult_guardian",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"guardian"},
					Summary:    "Waiting for Guardian approval",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-approval-route",
		ParentToolCallKey:   "consult-guardian-1",
		Kind:                "consult",
	}

	model, _ = app.Update(msg.GuideResponseMsg{
		CorrelationID: "corr-child-approval-route",
		AgentID:       "guardian",
		AgentType:     "guardian",
		Content:       "Fetch approval allowed",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-approval-route",
		AgentID:       "guardian",
		AgentType:     "guardian",
		Message:       "Validating fetch approval request",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	if _, ok := app.nestedStreams["corr-child-approval-route"]; ok {
		t.Fatal("expected late child progress after synthetic completion to be ignored")
	}
	if _, ok := app.activeStreams["corr-child-approval-route"]; ok {
		t.Fatal("expected late child progress after synthetic completion to avoid active stream registration")
	}
	if strings.Contains(app.chat.View(), "Validating fetch approval request") {
		t.Fatal("expected late child progress text to stay out of chat after synthetic completion")
	}
}

func TestHandleGuideResponse_UsesExistingNestedBranchWhenMetadataDrops(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	_, _ = app.prepareStreamStart(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-response",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "parent-response-entry",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-response",
		Source:        chatpkg.SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-1",
				ToolName:    "consult_librarian_style",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"librarian"},
					Summary:    "Checking prior patterns",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-response",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-response",
			ParentToolCallKey:   "consult-1",
			Kind:                "consult",
		},
	})
	app = model.(*AppModel)

	if cmd := app.handleGuideResponse(msg.GuideResponseMsg{
		CorrelationID: "corr-child-response",
		AgentID:       "librarian",
		AgentType:     "librarian",
		Content:       "Found the prior implementation pattern.",
	}); cmd != nil {
		_ = cmd()
	}

	for y := 0; y < app.chat.ViewportHeight(); y++ {
		entry := app.chat.EntryAtViewLine(y)
		if entry != nil && entry.CorrelationID == "corr-child-response" {
			t.Fatal("expected nested guide response without metadata to avoid a top-level chat row")
		}
	}
	view := app.chat.View()
	if !strings.Contains(view, "Found the prior implementation pattern.") {
		t.Fatalf("expected nested child completion summary in chat view, got:\n%s", view)
	}
}

func TestInterruptAllActiveRoutes_InterruptsNestedChildStreams(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-interrupt",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-interrupt",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-interrupt",
			ParentToolCallKey:   "consult-1",
			Kind:                "consult",
		},
	})
	app = model.(*AppModel)

	if _, ok := app.nestedStreams["corr-child-interrupt"]; !ok {
		t.Fatal("expected nested child stream to be registered before interrupt")
	}

	if cmd := app.interruptAllActiveRoutes("test interrupt all"); cmd != nil {
		_ = cmd()
	}

	if _, ok := app.interruptedCorrelations["corr-parent-interrupt"]; !ok {
		t.Fatal("expected parent correlation tombstone after interrupt all")
	}
	if _, ok := app.interruptedCorrelations["corr-child-interrupt"]; !ok {
		t.Fatal("expected nested child correlation tombstone after interrupt all")
	}
	if _, ok := app.activeStreams["corr-parent-interrupt"]; ok {
		t.Fatal("expected parent stream to be unregistered after interrupt all")
	}
	if _, ok := app.nestedStreams["corr-child-interrupt"]; ok {
		t.Fatal("expected nested child stream to be unregistered after interrupt all")
	}
}

func TestInterruptActiveRoute_InterruptsNestedDescendantsOfPrimaryTarget(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	_, _ = app.prepareStreamStart(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-single-interrupt",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-single-interrupt",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-single-interrupt",
			ParentToolCallKey:   "consult-1",
			Kind:                "consult",
		},
	})
	app = model.(*AppModel)

	if cmd := app.interruptActiveRoute("test single interrupt"); cmd != nil {
		_ = cmd()
	}

	if _, ok := app.interruptedCorrelations["corr-parent-single-interrupt"]; !ok {
		t.Fatal("expected parent correlation tombstone after single interrupt")
	}
	if _, ok := app.interruptedCorrelations["corr-child-single-interrupt"]; !ok {
		t.Fatal("expected nested child correlation tombstone after single interrupt")
	}
}

func TestCommandApprovalRequest_UsesPrimaryInputFlowDuringNestedChildStream(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-approval",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-approval",
		AgentID:       "guardian",
		AgentType:     "guardian",
		AgentName:     "Guardian",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-approval",
			ParentToolCallKey:   "consult-1",
			Kind:                "consult",
		},
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.CommandApprovalRequestMsg{
		Proposal: &commandapproval.Proposal{
			CorrelationID: "corr-child-approval",
			TargetAgentID: "guardian",
			AgentType:     "guardian",
			Command:       "go test ./ui",
		},
	})
	app = model.(*AppModel)

	if app.commandApproval == nil {
		t.Fatal("expected command approval to open in the primary input-panel flow")
	}
	if app.commandApproval.proposal == nil || app.commandApproval.proposal.CorrelationID != "corr-child-approval" {
		t.Fatalf("approval proposal correlation = %+v, want corr-child-approval", app.commandApproval.proposal)
	}
}

func TestStreamStartTelemetry_AllowsConcurrentNotificationStyleStream(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-architect",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "notif-1",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app = model.(*AppModel)

	if _, ok := app.activeStreams["notif-1"]; !ok {
		t.Fatal("expected notification-style stream to register while another stream is active")
	}

	model, _ = app.Update(msg.StreamChunkMsg{
		SessionID:     "s1",
		CorrelationID: "notif-1",
		Text:          "Plan dispatched to the orchestrator.",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "notif-1",
		AgentID:           "architect",
		AgentType:         "architect",
		AgentName:         "Architect",
		AuthoritativeText: "Plan dispatched to the orchestrator.",
	})
	app = model.(*AppModel)

	if _, ok := app.activeStreams["notif-1"]; ok {
		t.Fatal("expected notification-style stream to be finalized")
	}
	if view := app.chat.View(); !strings.Contains(view, "Plan dispatched to the orchestrator.") {
		t.Fatalf("expected notification-style completion text in chat view, got %q", view)
	}
}

func TestStreamRerouteBootstrapsChatEntry(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamRerouteMsg{
		SessionID:             "s1",
		OriginalCorrelationID: "corr-guide",
		CorrelationID:         "corr-inspector",
		FromAgentID:           "guide",
		ToAgentID:             "inspector",
	})
	app = model.(*AppModel)

	if !app.chat.IsStreaming() {
		t.Fatal("expected reroute to bootstrap chat streaming state")
	}
	if view := app.chat.View(); !strings.Contains(view, "Taking a thorough look...") {
		t.Fatalf("expected rerouted chat view to include inspector placeholder, got %q", view)
	}
}

func TestToolCallTelemetry_BootstrapsPrimaryPipelineOwnerBeforeStreamStart(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector",
		AgentID:       "runtime-inspector",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		TaskName:      "Auth Checkout",
		TaskSlug:      "auth-checkout",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tester",
		AgentID:       "runtime-tester",
		AgentType:     "tester-pipeline",
		AgentName:     "Tester",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		TaskName:      "Auth Checkout",
		TaskSlug:      "auth-checkout",
		ToolCallKey:   "tool-1",
		ToolName:      "coord_publish_artifact",
		ArgsSummary:   "type=verification_result",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	app = model.(*AppModel)

	entry, ok := app.activeStreams["corr-tester"]
	if !ok || entry == nil {
		t.Fatal("expected tool-call-first pipeline worker to register as a primary active stream")
	}
	if got := entry.AgentID; got != "task_auth_checkout:tester-pipeline" {
		t.Fatalf("active stream agent = %q, want task_auth_checkout:tester-pipeline", got)
	}
	if _, ok := app.nestedStreams["corr-tester"]; ok {
		t.Fatal("expected tool-call-first pipeline worker to stay out of nested stream ownership")
	}

	foundTesterEntry := false
	for y := 0; y < app.chat.ViewportHeight(); y++ {
		entry := app.chat.EntryAtViewLine(y)
		if entry == nil || entry.CorrelationID != "corr-tester" {
			continue
		}
		foundTesterEntry = true
		if entry.AgentType != "tester-pipeline" {
			t.Fatalf("tester chat entry agent type = %q, want tester-pipeline", entry.AgentType)
		}
	}
	if !foundTesterEntry {
		t.Fatal("expected tester correlation to appear as its own top-level chat entry")
	}
	inspectorEntry := findChatEntryByCorrelation(app.chat, "corr-inspector")
	if inspectorEntry == nil {
		t.Fatal("expected inspector entry to remain present")
	}
	if got := len(inspectorEntry.ToolCalls); got != 0 {
		t.Fatalf("inspector tool call count = %d, want 0", got)
	}
	if view := app.chat.View(); !strings.Contains(view, "coord_publish_artifact") {
		t.Fatalf("expected tester tool call in chat view, got %q", view)
	}
}

func TestToolCallTelemetry_DoesNotReRegisterReroutedSourceCorrelation(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector",
		AgentID:       "runtime-inspector",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		TaskName:      "Auth Checkout",
		TaskSlug:      "auth-checkout",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamRerouteMsg{
		SessionID:             "s1",
		OriginalCorrelationID: "corr-inspector",
		CorrelationID:         "corr-tester",
		FromAgentID:           "inspector-pipeline",
		ToAgentID:             "tester-pipeline",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector",
		AgentID:       "runtime-inspector",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		TaskName:      "Auth Checkout",
		TaskSlug:      "auth-checkout",
		ToolCallKey:   "tool-old",
		ToolName:      "handoff_next",
		Phase:         1,
		StartedAt:     time.Now().Add(-50 * time.Millisecond),
		Duration:      50 * time.Millisecond,
		Success:       true,
	})
	app = model.(*AppModel)

	if _, ok := app.activeStreams["corr-inspector"]; ok {
		t.Fatal("expected rerouted source correlation to stay out of primary active streams after late tool telemetry")
	}
	if _, ok := app.nestedStreams["corr-inspector"]; ok {
		t.Fatal("expected rerouted source correlation to stay out of nested streams after late tool telemetry")
	}
	if _, ok := app.activeStreams["corr-tester"]; !ok {
		t.Fatal("expected rerouted tester stream to remain active")
	}
}

func TestToolCallTelemetry_LateCompletionUpdatesExistingChatEntryWithoutReRegisteringStream(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tool",
		AgentID:       "engineer",
		AgentType:     "engineer",
		AgentName:     "Engineer",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tool",
		AgentID:       "engineer",
		AgentType:     "engineer",
		AgentName:     "Engineer",
		ToolCallKey:   "tool-1",
		ToolName:      "read_file",
		ArgsSummary:   "path=README.md",
		Phase:         0,
		StartedAt:     time.Now().Add(-100 * time.Millisecond),
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-tool",
		AgentID:           "engineer",
		AgentType:         "engineer",
		AgentName:         "Engineer",
		AuthoritativeText: "Done.",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-other",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tool",
		AgentID:       "engineer",
		AgentType:     "engineer",
		AgentName:     "Engineer",
		ToolCallKey:   "tool-1",
		ToolName:      "read_file",
		Phase:         1,
		StartedAt:     time.Now().Add(-100 * time.Millisecond),
		Duration:      100 * time.Millisecond,
		Success:       true,
		Output:        "ok",
	})
	app = model.(*AppModel)

	if _, ok := app.activeStreams["corr-tool"]; ok {
		t.Fatal("expected late tool completion not to re-register completed primary stream")
	}
	if _, ok := app.nestedStreams["corr-tool"]; ok {
		t.Fatal("expected late tool completion not to re-register completed nested stream")
	}

	entry := findChatEntryByCorrelation(app.chat, "corr-tool")
	if entry == nil {
		t.Fatal("expected original chat entry to remain present")
	}
	if len(entry.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(entry.ToolCalls))
	}
	if !entry.ToolCalls[0].Completed || entry.ToolCalls[0].Output != "ok" {
		t.Fatalf("tool call row = %+v, want completed output ok", entry.ToolCalls[0])
	}
}

func TestStreamReroute_AllowsOriginalCompletionToFinalizeChatSlot(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-architect",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app = model.(*AppModel)

	if view := app.chat.View(); !strings.Contains(view, "Sketching out the blueprint...") {
		t.Fatalf("expected architect placeholder before reroute, got %q", view)
	}

	model, _ = app.Update(msg.StreamRerouteMsg{
		SessionID:             "s1",
		OriginalCorrelationID: "corr-architect",
		CorrelationID:         "corr-orchestrator",
		FromAgentID:           "architect",
		ToAgentID:             "orchestrator",
	})
	app = model.(*AppModel)

	if _, ok := app.activeStreams["corr-architect"]; ok {
		t.Fatal("expected architect stream to be removed from active set after reroute")
	}

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-architect",
		AgentID:           "architect",
		AgentType:         "architect",
		AgentName:         "Architect",
		AuthoritativeText: "Plan handoff queued to the orchestrator.",
	})
	app = model.(*AppModel)

	if _, ok := app.reroutedStreamCIDs["corr-architect"]; ok {
		t.Fatal("expected architect reroute completion allowance to be cleared after completion")
	}

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-orchestrator",
		AgentID:           "orchestrator",
		AgentType:         "orchestrator",
		AgentName:         "Orchestrator",
		AuthoritativeText: "Orchestrator accepted the plan handoff.",
	})
	app = model.(*AppModel)

	view := app.chat.View()
	if strings.Contains(view, "Sketching out the blueprint...") {
		t.Fatalf("expected architect thinking placeholder to be cleared after completion, got %q", view)
	}
	if strings.Contains(view, "Coordinating the team...") {
		t.Fatalf("expected orchestrator thinking placeholder to be cleared after completion, got %q", view)
	}
	if !strings.Contains(view, "Plan handoff queued to the orchestrator.") {
		t.Fatalf("expected architect completion text in chat view, got %q", view)
	}
	if !strings.Contains(view, "Orchestrator accepted the plan handoff.") {
		t.Fatalf("expected orchestrator completion text in chat view, got %q", view)
	}
	if app.chat.IsStreaming() {
		t.Fatal("expected all rerouted streams to be finalized")
	}
}

func TestStreamCompleteTelemetry_PropagatesNestedCompletionEvenWhenTopLevelRenderIsSuppressed(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "architect-origin-nested-complete",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-nested-complete",
		Source:        chatpkg.SourceAgent,
		AgentType:     "architect",
		Content:       "Waiting on Academic research.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-academic-1",
				ToolName:    "consult_academic_approach",
				Completed:   true,
				Success:     true,
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare the strongest sources.",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-nested-complete",
		ParentToolCallKey:   "consult-academic-1",
		Kind:                "consult",
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested-complete",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-nested-complete",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		Message:       "Reviewing the strongest sources.",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	app.unregisterStream("corr-child-nested-complete")
	if _, ok := app.nestedStreams["corr-child-nested-complete"]; ok {
		t.Fatal("expected child stream registration to be cleared for the suppression case")
	}

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-child-nested-complete",
		AgentID:           "academic",
		AgentType:         "academic",
		AgentName:         "Academic",
		AuthoritativeText: "Research complete. Official packaging guidance is aligned.",
		BranchRef:         branchRef,
	})
	app = model.(*AppModel)

	parent := findChatEntryByCorrelation(app.chat, "corr-parent-nested-complete")
	if parent == nil || len(parent.ToolCalls) != 1 || parent.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row after nested completion, got %+v", parent)
	}
	row := parent.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity after completion, got %+v", row.Children)
	}
	child := row.Children[0]
	if !child.Completed || child.Failed {
		t.Fatalf("expected nested Academic completion to reach the chat reducer even when top-level render is suppressed, got %+v", child)
	}
	if !strings.Contains(child.ResultSummary, "Official packaging guidance") {
		t.Fatalf("nested child result summary = %q, want propagated completion text", child.ResultSummary)
	}
	if findChatEntryByCorrelation(app.chat, "corr-child-nested-complete") != nil {
		t.Fatal("expected nested completion to avoid creating a top-level child chat row")
	}
}

func TestHandleGuideResponse_TopLevelPendingChildWorkDefersCompletion(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-deferred-route",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-deferred-route",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		ToolCallKey:   "consult-guardian-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"guardian","query":"Check the current safety assumptions."}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	app = model.(*AppModel)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-deferred-route",
		ParentToolCallKey:   "consult-guardian-1",
		Kind:                "consult",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-deferred-route",
		AgentID:       "guardian",
		AgentType:     "guardian",
		AgentName:     "Guardian",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-deferred-route",
		AgentID:       "guardian",
		AgentType:     "guardian",
		AgentName:     "Guardian",
		Message:       "Checking the current safety assumptions.",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.GuideResponseMsg{
		CorrelationID: "corr-parent-deferred-route",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		Content:       "Research synthesis is ready.",
	})
	app = model.(*AppModel)

	parent := findChatEntryByCorrelation(app.chat, "corr-parent-deferred-route")
	if parent == nil {
		t.Fatal("expected parent entry after top-level route response")
	}
	if !parent.Streaming {
		t.Fatalf("expected parent entry to remain streaming while child work is active, got %+v", parent)
	}
	if parent.Content != "Research synthesis is ready." {
		t.Fatalf("parent content = %q, want authoritative route-response content", parent.Content)
	}
	if !strings.Contains(parent.ThinkingStatus, "Waiting for child work to finish...") {
		t.Fatalf("parent thinking status = %q, want deferred completion status", parent.ThinkingStatus)
	}
	if !app.chat.HasPendingCorrelation("corr-parent-deferred-route") {
		t.Fatal("expected route response to stay attached to the existing chat stream slot")
	}
	if findChatEntryByCorrelation(app.chat, "corr-child-deferred-route") != nil {
		t.Fatal("expected child guardian activity to stay nested")
	}
}

func findChatEntryByCorrelation(chat *chatpkg.Model, correlationID string) *chatpkg.ChatEntry {
	if chat == nil {
		return nil
	}
	for y := 0; y < chat.ViewportHeight(); y++ {
		entry := chat.EntryAtViewLine(y)
		if entry == nil {
			continue
		}
		if strings.TrimSpace(entry.CorrelationID) == strings.TrimSpace(correlationID) {
			return entry
		}
	}
	return nil
}
