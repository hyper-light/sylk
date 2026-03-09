package ui

import (
	"testing"

	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func newStreamTelemetryModel() *AppModel {
	return &AppModel{
		agentContextTokens: make(map[string]int),
		streamUsage:        make(map[string]streamUsageEntry),
		streamedResponses:  make(map[string]streamedResponseState),
		activeStreams:      make(map[string]*activeStreamEntry),
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
