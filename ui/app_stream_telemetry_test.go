package ui

import (
	"strings"
	"testing"
	"time"

	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
)

func newStreamTelemetryModel() *AppModel {
	return &AppModel{
		agentContextTokens: make(map[string]int),
		agentContextModels: make(map[string]string),
		streamUsage:        make(map[string]streamUsageEntry),
		streamedResponses:  make(map[string]streamedResponseState),
		activeStreams:      make(map[string]*activeStreamEntry),
		reroutedStreamCIDs: make(map[string]time.Time),
		agentPanel:         agentpkg.New(theme.DefaultDark()),
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
}

func TestStreamTelemetry_UsesModelSpecificContextLimit(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("tester", "tester", "Tester", nil, "", "")

	ratio := m.setAgentContextUsage("tester", 210000)
	if ratio >= 1.0 {
		t.Fatalf("ratio = %.4f, want < 1.0 for gpt-5.4-pro context window", ratio)
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
