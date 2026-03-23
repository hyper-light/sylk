package ui

import (
	"strings"
	"testing"
	"time"

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
