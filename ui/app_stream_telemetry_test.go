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
		agentRuntimeContexts:    make(map[string]runtimeContextState),
		agentReplicaCounts:      make(map[string]int),
		streamUsage:             make(map[string]streamUsageEntry),
		streamedResponses:       make(map[string]streamedResponseState),
		activeStreams:           make(map[string]*activeStreamEntry),
		deferredStreams:         make(map[string]*activeStreamEntry),
		nestedStreams:           make(map[string]*activeStreamEntry),
		delayedPrimaryBootstrap: make(map[string][]tea.Msg),
		reroutedStreamCIDs:      make(map[string]time.Time),
		interruptedCorrelations: make(map[string]struct{}),
		agentPanel:              agentpkg.New(th),
		chat:                    chatpkg.New(th, 32),
		statusBar:               statuspkg.New(th, nil),
	}
}

func registerPipelineWorkerRow(m *AppModel, start msg.StreamStartMsg) {
	if m == nil || m.agentPanel == nil {
		return
	}
	model, _ := m.agentPanel.Update(start)
	if updated, ok := model.(*agentpkg.Model); ok {
		m.agentPanel = updated
	}
}

func seedDeferredInspectorChallengeReturn(t *testing.T) *AppModel {
	t.Helper()
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-parent-inspector-challenge",
		AgentID:        "task_1:inspector-pipeline",
		RuntimeAgentID: "runtime-inspector",
		AgentType:      "inspector-pipeline",
		AgentName:      "Inspector",
		TaskID:         "task_1",
		TaskName:       "Create hello.py CLI module with argparse and greet function",
		TaskSlug:       "create-cli-module",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-inspector-challenge",
		AgentID:       "task_1:inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		TaskID:        "task_1",
		TaskName:      "Create hello.py CLI module with argparse and greet function",
		TaskSlug:      "create-cli-module",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target":"tester-pipeline","prompt":"Fix the failing test file."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "challenge",
			Status:     "pending",
			AgentTypes: []string{"tester-pipeline"},
			Summary:    "Fix the failing test file.",
			ThreadKey:  "pipeline:task_1-challenge-seed",
		},
	})
	app = model.(*AppModel)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-inspector-challenge",
		ParentToolCallKey:   "challenge-1",
		Kind:                "challenge",
		ThreadKey:           "pipeline:task_1-challenge-seed",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-child-tester-challenge",
		AgentID:        "task_1:tester-pipeline",
		RuntimeAgentID: "runtime-tester",
		AgentType:      "tester-pipeline",
		AgentName:      "Tester",
		TaskID:         "task_1",
		TaskName:       "Create hello.py CLI module with argparse and greet function",
		TaskSlug:       "create-cli-module",
		BranchRef:      branchRef,
	})
	app = model.(*AppModel)

	chatComp, _ := app.chat.Update(msg.StreamCompleteMsg{
		CorrelationID:     "corr-parent-inspector-challenge",
		AgentID:           "task_1:inspector-pipeline",
		AgentType:         "inspector-pipeline",
		AuthoritativeText: "Waiting for the tester challenge result.",
	})
	app.chat = chatComp.(*chatpkg.Model)
	app.deferPrimaryStream("corr-parent-inspector-challenge")

	if entry := app.streamEntryForCorrelation("corr-parent-inspector-challenge"); entry == nil {
		t.Fatal("expected parent inspector stream to remain registered after deferred challenge response")
	}

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-child-tester-challenge",
		AgentID:           "task_1:tester-pipeline",
		RuntimeAgentID:    "runtime-tester",
		AgentType:         "tester-pipeline",
		AgentName:         "Tester",
		TaskID:            "task_1",
		TaskName:          "Create hello.py CLI module with argparse and greet function",
		TaskSlug:          "create-cli-module",
		AuthoritativeText: "Fixed the test file defect.",
		BranchRef:         branchRef,
	})
	app = model.(*AppModel)

	if entry := app.streamEntryForCorrelation("corr-parent-inspector-challenge"); entry == nil {
		t.Fatal("expected parent inspector stream to remain registered after child challenge completion")
	}
	return app
}

func seedDeferredInspectorChallengeReturnUnscopedParent(t *testing.T) *AppModel {
	t.Helper()
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-parent-inspector-unscoped",
		AgentID:        "inspector-pipeline",
		RuntimeAgentID: "runtime-inspector",
		AgentType:      "inspector-pipeline",
		AgentName:      "Inspector",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-inspector-unscoped",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target":"tester-pipeline","prompt":"Fix the failing test file."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "challenge",
			Status:     "pending",
			AgentTypes: []string{"tester-pipeline"},
			Summary:    "Fix the failing test file.",
			ThreadKey:  "pipeline:task_1-challenge-seed",
		},
	})
	app = model.(*AppModel)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-inspector-unscoped",
		ParentToolCallKey:   "challenge-1",
		Kind:                "challenge",
		ThreadKey:           "pipeline:task_1-challenge-seed",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-child-tester-unscoped",
		AgentID:        "task_1:tester-pipeline",
		RuntimeAgentID: "runtime-tester",
		AgentType:      "tester-pipeline",
		AgentName:      "Tester",
		TaskID:         "task_1",
		TaskName:       "Create hello.py CLI module with argparse and greet function",
		TaskSlug:       "create-cli-module",
		BranchRef:      branchRef,
	})
	app = model.(*AppModel)

	chatComp, _ := app.chat.Update(msg.StreamCompleteMsg{
		CorrelationID:     "corr-parent-inspector-unscoped",
		AgentID:           "inspector-pipeline",
		AgentType:         "inspector-pipeline",
		AuthoritativeText: "Waiting for the tester challenge result.",
	})
	app.chat = chatComp.(*chatpkg.Model)
	app.deferPrimaryStream("corr-parent-inspector-unscoped")

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-child-tester-unscoped",
		AgentID:           "task_1:tester-pipeline",
		RuntimeAgentID:    "runtime-tester",
		AgentType:         "tester-pipeline",
		AgentName:         "Tester",
		TaskID:            "task_1",
		TaskName:          "Create hello.py CLI module with argparse and greet function",
		TaskSlug:          "create-cli-module",
		AuthoritativeText: "Fixed the test file defect.",
		BranchRef:         branchRef,
	})
	app = model.(*AppModel)
	return app
}

func TestPrepareStreamStart_PreservesRawAgentIDWhileRegisteringCanonicalPipelineWorker(t *testing.T) {
	m := newStreamTelemetryModel()

	start, created := m.prepareStreamStart(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-inspector-raw-preserved",
		AgentID:        "inspector-pipeline",
		RuntimeAgentID: "inspector-pipeline",
		AgentType:      "inspector-pipeline",
		AgentName:      "Pipeline Inspector",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
		TaskName:       "Auth Checkout",
		TaskSlug:       "auth-checkout",
	})
	if !created {
		t.Fatal("expected prepareStreamStart to register a new stream")
	}
	if start.AgentID != "inspector-pipeline" {
		t.Fatalf("prepared start AgentID = %q, want raw inspector-pipeline", start.AgentID)
	}
	if start.RuntimeAgentID != "inspector-pipeline" {
		t.Fatalf("prepared start RuntimeAgentID = %q, want inspector-pipeline", start.RuntimeAgentID)
	}

	entry := m.streamEntryForCorrelation("corr-inspector-raw-preserved")
	if entry == nil {
		t.Fatal("expected active stream entry to be registered")
	}
	if entry.AgentID != "task_auth_checkout:inspector-pipeline" {
		t.Fatalf("active stream entry AgentID = %q, want canonical task_auth_checkout:inspector-pipeline", entry.AgentID)
	}
	if entry.RuntimeAgentID != "inspector-pipeline" {
		t.Fatalf("active stream entry RuntimeAgentID = %q, want raw inspector-pipeline", entry.RuntimeAgentID)
	}
}

func TestEffectiveStreamUIAgentID_PrefersVisibleIdentity(t *testing.T) {
	entry := &activeStreamEntry{
		AgentID:        "task_auth_checkout:inspector-pipeline",
		RuntimeAgentID: "runtime-inspector",
		AgentType:      "inspector-pipeline",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
	}

	if got := effectiveStreamUIAgentID(entry, "inspector-pipeline", "runtime-inspector", "inspector-pipeline", "task_auth_checkout", "task_auth_checkout"); got != "task_auth_checkout:inspector-pipeline" {
		t.Fatalf("effectiveStreamUIAgentID = %q, want task_auth_checkout:inspector-pipeline", got)
	}

	if got := effectiveStreamUIAgentID(entry, "", "", "inspector-pipeline", "task_auth_checkout", "task_auth_checkout"); got != "task_auth_checkout:inspector-pipeline" {
		t.Fatalf("effectiveStreamUIAgentID without explicit agent = %q, want task_auth_checkout:inspector-pipeline", got)
	}
}

func TestEffectiveStreamUIAgentID_DoesNotReuseGuidePlaceholderForResolvedAgent(t *testing.T) {
	entry := &activeStreamEntry{
		AgentID:        "guide",
		RuntimeAgentID: "guide",
		AgentType:      "guide",
	}

	if got := effectiveStreamUIAgentID(entry, "architect", "architect", "", "", ""); got != "architect" {
		t.Fatalf("effectiveStreamUIAgentID = %q, want architect", got)
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

func TestCanonicalActivityAgentID_PrefersCanonicalVisibleReplicaIdentity(t *testing.T) {
	ev := &events.ActivityEvent{
		AgentID: "librarian#replica-corr-1",
		Data: map[string]any{
			"agent_type":         "librarian",
			"canonical_agent_id": "librarian",
			"runtime_agent_id":   "librarian#replica-corr-1",
		},
	}

	if got := canonicalActivityAgentID(ev); got != "librarian" {
		t.Fatalf("canonicalActivityAgentID = %q, want librarian", got)
	}
}

func TestStreamTelemetry_AggregatesKnowledgeReplicaContextAcrossLiveReplicas(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("academic", "academic", "Academic", nil, "", "")

	_, _ = m.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_academic_replicas",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "academic",
			Content:   "Serving parallel consults.",
			Data: map[string]any{
				"agent_type":      "academic",
				"agent_name":      "Academic",
				"active_replicas": 3,
			},
		},
	})

	first := msg.StreamStartMsg{
		CorrelationID:  "corr-academic-1",
		AgentID:        "academic",
		RuntimeAgentID: "academic#replica-corr-1",
		AgentType:      "academic",
		AgentName:      "Academic",
	}
	second := msg.StreamStartMsg{
		CorrelationID:  "corr-academic-2",
		AgentID:        "academic",
		RuntimeAgentID: "academic#replica-corr-2",
		AgentType:      "academic",
		AgentName:      "Academic",
	}
	m.trackStreamStart(first)
	m.trackStreamStart(second)

	model, _ := m.Update(msg.TokenUsageMsg{
		CorrelationID:  "corr-academic-1",
		AgentID:        "academic",
		RuntimeAgentID: "academic#replica-corr-1",
		AgentType:      "academic",
		Model:          "gpt-5.4-pro",
		InputTokens:    100000,
	})
	m = model.(*AppModel)
	model, _ = m.Update(msg.TokenUsageMsg{
		CorrelationID:  "corr-academic-2",
		AgentID:        "academic",
		RuntimeAgentID: "academic#replica-corr-2",
		AgentType:      "academic",
		Model:          "gpt-5.4-pro",
		InputTokens:    150000,
	})
	m = model.(*AppModel)

	if got := m.agentContextTokens["academic"]; got != 250000 {
		t.Fatalf("agentContextTokens[academic] = %d, want 250000", got)
	}
	if got := m.agentPanel.ContextUsageOf("academic"); got < 0.30 || got > 0.31 {
		t.Fatalf("panel context usage = %.4f, want about 0.3064", got)
	}

	m.finalizeStreamUsage("corr-academic-1", true, "")
	if got := m.agentContextTokens["academic"]; got != 150000 {
		t.Fatalf("agentContextTokens[academic] after first finalize = %d, want 150000", got)
	}

	_, _ = m.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_academic_replicas_down",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "academic",
			Content:   "One consult completed.",
			Data: map[string]any{
				"agent_type":      "academic",
				"agent_name":      "Academic",
				"active_replicas": 2,
			},
		},
	})
	if got := m.agentPanel.ContextUsageOf("academic"); got < 0.27 || got > 0.28 {
		t.Fatalf("panel context usage after spin-down = %.4f, want about 0.2757", got)
	}
}

func TestStreamTelemetry_KnowledgeReplicaSpinUpEventRecomputesDisplayedUsage(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("academic", "academic", "Academic", nil, "", "")

	first := msg.StreamStartMsg{
		CorrelationID:  "corr-academic-spin-up-1",
		AgentID:        "academic",
		RuntimeAgentID: "academic#replica-corr-spin-up-1",
		AgentType:      "academic",
		AgentName:      "Academic",
	}
	m.trackStreamStart(first)

	model, _ := m.Update(msg.TokenUsageMsg{
		CorrelationID:  "corr-academic-spin-up-1",
		AgentID:        "academic",
		RuntimeAgentID: "academic#replica-corr-spin-up-1",
		AgentType:      "academic",
		Model:          "gpt-5.4-pro",
		InputTokens:    100000,
	})
	m = model.(*AppModel)

	before := m.agentPanel.ContextUsageOf("academic")
	if before < 0.36 || before > 0.38 {
		t.Fatalf("panel context usage before spin-up = %.4f, want about 0.3676", before)
	}

	_, _ = m.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_academic_replicas_up",
			EventType: events.EventTypeAgentAction,
			Timestamp: time.Now(),
			AgentID:   "academic",
			Content:   "Scaling consult replicas.",
			Data: map[string]any{
				"agent_type":      "academic",
				"agent_name":      "Academic",
				"active_replicas": 2,
			},
		},
	})

	after := m.agentPanel.ContextUsageOf("academic")
	if after < 0.18 || after > 0.19 {
		t.Fatalf("panel context usage after spin-up = %.4f, want about 0.1838", after)
	}
}

func TestStreamTelemetry_PipelineRuntimeReplacementDoesNotDoubleCountContext(t *testing.T) {
	m := newStreamTelemetryModel()
	canonicalID := "task_auth_checkout:engineer"
	m.agentPanel.SeedAgent(canonicalID, "engineer", "Engineer", nil, "", "")

	first := msg.StreamStartMsg{
		CorrelationID:  "corr-eng-1",
		AgentID:        "runtime-engineer-1",
		RuntimeAgentID: "runtime-engineer-1",
		AgentType:      "engineer",
		AgentName:      "Engineer",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
	}
	second := msg.StreamStartMsg{
		CorrelationID:  "corr-eng-2",
		AgentID:        "runtime-engineer-2",
		RuntimeAgentID: "runtime-engineer-2",
		AgentType:      "engineer",
		AgentName:      "Engineer",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
	}
	m.trackStreamStart(first)

	model, _ := m.Update(msg.TokenUsageMsg{
		CorrelationID:  "corr-eng-1",
		AgentID:        canonicalID,
		RuntimeAgentID: "runtime-engineer-1",
		AgentType:      "engineer",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
		Model:          "gpt-5.4-pro",
		InputTokens:    100000,
	})
	m = model.(*AppModel)

	m.trackStreamStart(second)
	model, _ = m.Update(msg.TokenUsageMsg{
		CorrelationID:  "corr-eng-2",
		AgentID:        canonicalID,
		RuntimeAgentID: "runtime-engineer-2",
		AgentType:      "engineer",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
		Model:          "gpt-5.4-pro",
		InputTokens:    120000,
	})
	m = model.(*AppModel)

	if got := m.agentContextTokens[canonicalID]; got != 120000 {
		t.Fatalf("agentContextTokens[%q] = %d, want 120000 after runtime replacement", canonicalID, got)
	}
	if got := m.agentPanel.ContextUsageOf(canonicalID); got < 0.44 || got > 0.45 {
		t.Fatalf("panel context usage = %.4f, want about 0.4412", got)
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
	registerPipelineWorkerRow(m, start)
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
	logicalID := "task_auth_checkout:engineer"
	m.agentPanel.SeedAgent(logicalID, "engineer", "Engineer", nil, "", "")

	start := msg.StreamStartMsg{
		CorrelationID: "corr-engineer",
		AgentID:       "runtime-engineer",
		AgentType:     "engineer",
		AgentName:     "Engineer",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
	}
	registerPipelineWorkerRow(m, start)
	m.trackStreamStart(start)

	model, _ := m.Update(msg.TokenUsageMsg{
		AgentID:     logicalID,
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
	canonicalID := "task_auth_checkout:engineer"
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
	registerPipelineWorkerRow(m, start)
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

func TestStreamTelemetry_RuntimeModelOverridesStalePanelModelForEngineer(t *testing.T) {
	m := newStreamTelemetryModel()
	m.agentPanel.SeedAgent("task_auth_checkout:engineer", "engineer", "Engineer", []agentpkg.ModelEntry{
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	}, "claude-opus-4-6", "anthropic")

	model, _ := m.Update(msg.TokenUsageMsg{
		AgentID:     "task_auth_checkout:engineer",
		Model:       "gpt-5.4-pro",
		InputTokens: 210000,
	})
	m = model.(*AppModel)

	if got := m.agentPanel.ContextUsageOf("task_auth_checkout:engineer"); got < 0.77 || got > 0.78 {
		t.Fatalf("panel context usage = %.4f, want about 0.7721 from observed runtime model", got)
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

func TestHandleGuideResponse_EmptySuccessfulResponseDoesNotPublishOrRender(t *testing.T) {
	m := newStreamTelemetryModel()
	m.chat.SetSize(80, 20)
	collector := events.NewTestActivityCollector()
	m.deps.ActivityPub = collector

	if cmd := m.handleGuideResponse(msg.GuideResponseMsg{
		CorrelationID: "corr-empty-response",
		AgentID:       "inspector",
		AgentType:     "inspector",
		AgentName:     "Inspector",
		Content:       "",
	}); cmd != nil {
		_ = cmd()
	}

	if got := len(collector.Events()); got != 0 {
		t.Fatalf("published activity count = %d, want 0", got)
	}
	for y := 0; y < m.chat.ViewportHeight(); y++ {
		if entry := m.chat.EntryAtViewLine(y); entry != nil {
			t.Fatalf("expected no top-level chat entry for empty successful response, got %+v", entry)
		}
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

func TestPrepareStreamStart_PreservesExistingNestedOwnershipWhenSyntheticStartDropsBranch(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	_, _ = app.prepareStreamStart(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-synthetic-reset",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-synthetic-reset",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-synthetic-reset",
			ParentToolCallKey:   "consult-1",
			Kind:                "consult",
		},
	})
	app = model.(*AppModel)

	start, created := app.prepareStreamStart(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-synthetic-reset",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
	})
	if created {
		t.Fatal("expected synthetic metadata-less bootstrap to reuse existing nested registration")
	}
	if start.BranchRef != nil {
		t.Fatalf("synthetic start BranchRef = %+v, want nil so registerPrimaryStream preserves the nested slot itself", start.BranchRef)
	}
	if _, ok := app.activeStreams["corr-child-synthetic-reset"]; ok {
		t.Fatal("expected metadata-less synthetic bootstrap to avoid primary active stream registration")
	}
	entry, ok := app.nestedStreams["corr-child-synthetic-reset"]
	if !ok || entry == nil || entry.BranchRef == nil {
		t.Fatal("expected child stream to remain registered as nested after synthetic bootstrap")
	}
	if entry.BranchRef.ParentCorrelationID != "corr-parent-synthetic-reset" || entry.BranchRef.ParentToolCallKey != "consult-1" {
		t.Fatalf("unexpected nested branch ref after synthetic bootstrap: %+v", entry.BranchRef)
	}
}

func TestToolCallTelemetry_PreservesExistingNestedBranchWhenLaterEventDropsMetadata(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	_, _ = app.prepareStreamStart(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-tool-reset",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "parent-tool-reset-entry",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-tool-reset",
		Source:        chatpkg.SourceAgent,
		AgentType:     "architect",
		Content:       "Refining the patch plan.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "challenge-1",
				ToolName:    "challenge_agent",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolChallenge,
					AgentTypes: []string{"tester-pipeline"},
					Summary:    "Fix the failing test file.",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-tool-reset",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		AgentName:     "Tester",
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-parent-tool-reset",
			ParentToolCallKey:   "challenge-1",
			Kind:                "challenge",
		},
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-child-tool-reset",
		ParentCorrelationID: "corr-parent-tool-reset",
		AgentID:             "runtime-tester",
		AgentType:           "tester-pipeline",
		AgentName:           "Tester",
		ToolCallKey:         "tool-1",
		ToolName:            "run_test_suite",
		Phase:               0,
		StartedAt:           time.Now(),
	})
	app = model.(*AppModel)

	if _, ok := app.activeStreams["corr-child-tool-reset"]; ok {
		t.Fatal("expected metadata-less child tool event to preserve nested ownership")
	}
	entry, ok := app.nestedStreams["corr-child-tool-reset"]
	if !ok || entry == nil || entry.BranchRef == nil {
		t.Fatal("expected child tool event to remain registered as nested")
	}
	if entry.BranchRef.ParentCorrelationID != "corr-parent-tool-reset" || entry.BranchRef.ParentToolCallKey != "challenge-1" {
		t.Fatalf("unexpected nested branch ref after metadata-less tool event: %+v", entry.BranchRef)
	}
	for y := 0; y < app.chat.ViewportHeight(); y++ {
		entry := app.chat.EntryAtViewLine(y)
		if entry != nil && entry.CorrelationID == "corr-child-tool-reset" {
			t.Fatal("expected metadata-less child tool event to avoid a top-level chat row")
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

func TestHandleGuideResponse_UsesRecordedNestedBranchAfterTerminalComplete(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "parent-entry-terminal-nested-response",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-terminal-response",
		Source:        chatpkg.SourceAgent,
		AgentType:     "architect",
		Content:       "Waiting on Academic confirmation.",
		Height:        -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-1",
				ToolName:    "consult_academic_approach",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Verify the proposed approach",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-terminal-response",
		ParentToolCallKey:   "consult-1",
		Kind:                "consult",
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-terminal-response",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-terminal-response",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		BranchRef:     branchRef,
	})
	app = model.(*AppModel)

	if _, ok := app.nestedStreams["corr-child-terminal-response"]; ok {
		t.Fatal("expected nested stream registration to clear after terminal completion")
	}

	if cmd := app.handleGuideResponse(msg.GuideResponseMsg{
		CorrelationID: "corr-child-terminal-response",
		AgentID:       "academic",
		AgentType:     "academic",
		Content:       "The proposed approach is sound and low risk.",
	}); cmd != nil {
		_ = cmd()
	}

	for y := 0; y < app.chat.ViewportHeight(); y++ {
		entry := app.chat.EntryAtViewLine(y)
		if entry != nil && entry.CorrelationID == "corr-child-terminal-response" {
			t.Fatal("expected late nested route response after terminal completion to avoid a top-level chat row")
		}
	}

	parent := findChatEntryByCorrelation(app.chat, "corr-parent-terminal-response")
	if parent == nil || len(parent.ToolCalls) != 1 || parent.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row after late nested route response, got %+v", parent)
	}
	row := parent.ToolCalls[0].InterAgent
	if len(row.Children) != 1 {
		t.Fatalf("expected one nested child activity after late route response, got %+v", row.Children)
	}
	child := row.Children[0]
	if !child.Completed || child.Failed {
		t.Fatalf("expected nested child to remain completed after late route response, got %+v", child)
	}
	if !strings.Contains(child.ResultSummary, "approach is sound and low risk") {
		t.Fatalf("nested child result summary = %q, want late route-response content", child.ResultSummary)
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

func TestCommandApprovalRequest_KeepsDeferredPipelineInspectorAnimating(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-inspector-approval",
		AgentID:       "task_1:inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Pipeline Inspector",
		PipelineID:    "task_1",
		TaskID:        "task_1",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-inspector-approval",
		AgentID:       "task_1:inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Pipeline Inspector",
		PipelineID:    "task_1",
		TaskID:        "task_1",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target":"tester-pipeline","request":"Validate the implementation against the criteria contract."}`,
		Phase:         0,
		StartedAt:     time.Now().Add(-1500 * time.Millisecond),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "challenge",
			Status:     "pending",
			AgentTypes: []string{"tester-pipeline"},
			Summary:    "Validate the implementation against the criteria contract.",
			ThreadKey:  "pipeline:task_1-challenge-1",
		},
	})
	app = model.(*AppModel)

	challengeBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-inspector-approval",
		ParentToolCallKey:   "challenge-1",
		Kind:                "challenge",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-tester-approval",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		AgentName:     "Pipeline Tester",
		PipelineID:    "task_1",
		TaskID:        "task_1",
		BranchRef:     challengeBranch,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-tester-approval",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		AgentName:     "Pipeline Tester",
		PipelineID:    "task_1",
		TaskID:        "task_1",
		Message:       "Waiting for Guardian approval for run_command",
		BranchRef:     challengeBranch,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-tester-approval",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		AgentName:     "Pipeline Tester",
		PipelineID:    "task_1",
		TaskID:        "task_1",
		ToolCallKey:   "approval-1",
		ToolName:      "approval_guardian",
		FullArgs:      `{"target":"guardian","tool_name":"run_command","summary":"Waiting for Guardian approval for run_command"}`,
		Phase:         0,
		StartedAt:     time.Now().Add(-1200 * time.Millisecond),
		BranchRef:     challengeBranch,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			Status:     "pending",
			AgentTypes: []string{"guardian"},
			Summary:    "Waiting for Guardian approval for run_command",
		},
	})
	app = model.(*AppModel)

	approvalBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-child-tester-approval",
		ParentToolCallKey:   "approval-1",
		Kind:                "approval",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-grandchild-guardian-approval",
		AgentID:       "guardian",
		AgentType:     "guardian",
		AgentName:     "Guardian",
		BranchRef:     approvalBranch,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-inspector-approval",
		AgentID:       "task_1:inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Pipeline Inspector",
		PipelineID:    "task_1",
		TaskID:        "task_1",
	})
	app = model.(*AppModel)

	parent := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-approval")
	if parent == nil {
		t.Fatal("expected inspector parent entry")
	}
	if parent.Streaming {
		t.Fatalf("expected inspector entry to finalize while tester child is pending, got %+v", parent)
	}
	if strings.TrimSpace(parent.ThinkingStatus) != "" || strings.TrimSpace(parent.ThinkingText) != "" {
		t.Fatalf("expected parent waiting footer to clear after completion, got %+v", parent)
	}
	if got := len(parent.ToolCalls); got != 1 {
		t.Fatalf("parent tool call count = %d, want 1 challenge row", got)
	}
	if parent.ToolCalls[0].InterAgent == nil || len(parent.ToolCalls[0].InterAgent.Children) != 1 {
		t.Fatalf("expected nested tester child activity, got %+v", parent.ToolCalls[0])
	}
	beforeChildText := parent.ToolCalls[0].InterAgent.Children[0].ThinkingText
	if strings.TrimSpace(beforeChildText) == "" {
		t.Fatalf("expected nested tester child spinner text before approval, got %+v", parent.ToolCalls[0].InterAgent.Children[0])
	}

	model, _ = app.Update(msg.CommandApprovalRequestMsg{
		Proposal: &commandapproval.Proposal{
			CorrelationID: "corr-grandchild-guardian-approval",
			TargetAgentID: "guardian",
			AgentType:     "guardian",
			Command:       "python -m pytest tools/hello-cli/test_hello.py",
		},
	})
	app = model.(*AppModel)

	if app.commandApproval == nil {
		t.Fatal("expected command approval to open while tester child is pending")
	}
	beforeView := app.View()

	model, _ = app.Update(msg.DecorTickMsg{
		Time: time.Now().Add(900 * time.Millisecond),
		Gen:  app.decorGen,
	})
	app = model.(*AppModel)

	parent = findChatEntryByCorrelation(app.chat, "corr-parent-inspector-approval")
	if parent == nil {
		t.Fatal("expected inspector parent entry after decor tick")
	}
	if parent.Streaming {
		t.Fatalf("expected inspector parent to remain finalized across approval flow, got %+v", parent)
	}
	child := parent.ToolCalls[0].InterAgent.Children[0]
	if child.ThinkingText == beforeChildText {
		t.Fatalf("expected nested tester child spinner/timer to keep animating across approval flow, got %q", child.ThinkingText)
	}
	if child.ThinkingStatus != "Waiting for Guardian approval for run_command" {
		t.Fatalf("child thinking status = %q, want waiting-for-approval status", child.ThinkingStatus)
	}
	afterView := app.View()
	if afterView == beforeView {
		t.Fatal("expected rendered frame to change when nested child timers advance during approval flow")
	}
	if entry := app.streamEntryForCorrelation("corr-parent-inspector-approval"); entry == nil {
		t.Fatal("expected deferred inspector parent stream entry to remain registered across approval flow")
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

func TestStreamProgressTelemetry_DoesNotRebootstrapKnownPrimaryStream(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-engineer-live-progress",
		AgentID:        "task_2:engineer",
		RuntimeAgentID: "runtime-engineer",
		AgentType:      "engineer",
		AgentName:      "Engineer",
		TaskID:         "task_2",
		TaskName:       "Create pyproject.toml",
		TaskSlug:       "create-pyproject-toml",
	})
	app = model.(*AppModel)

	app.recordStreamChunk("corr-engineer-live-progress", "implementation is underway")

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-engineer-live-progress",
		AgentID:        "task_2:engineer",
		RuntimeAgentID: "runtime-engineer",
		AgentType:      "engineer",
		AgentName:      "Engineer",
		TaskID:         "task_2",
		TaskName:       "Create pyproject.toml",
		TaskSlug:       "create-pyproject-toml",
		Message:        "Publishing the validation findings artifact for downstream review.",
	})
	app = model.(*AppModel)

	state, ok := app.streamedResponses["corr-engineer-live-progress"]
	if !ok {
		t.Fatal("expected recorded stream state for active engineer correlation")
	}
	if !state.HadChunk {
		t.Fatalf("expected progress on an active primary stream to preserve recorded output state, got %+v", state)
	}
	if entry := app.streamEntryForCorrelation("corr-engineer-live-progress"); entry == nil || entry.AgentID != "task_2:engineer" {
		t.Fatalf("expected active engineer stream to remain registered under the same primary row, got %+v", entry)
	}
}

func TestToolCallTelemetry_DoesNotRebootstrapKnownPrimaryStream(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-inspector-live-tool",
		AgentID:        "task_3:inspector-pipeline",
		RuntimeAgentID: "runtime-inspector",
		AgentType:      "inspector-pipeline",
		AgentName:      "Inspector",
		TaskID:         "task_3",
		TaskName:       "Create hello.py CLI entrypoint",
		TaskSlug:       "create-cli-entrypoint",
	})
	app = model.(*AppModel)

	app.recordStreamChunk("corr-inspector-live-tool", "auditing the returned implementation")

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-inspector-live-tool",
		AgentID:       "task_3:inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		TaskID:        "task_3",
		TaskName:      "Create hello.py CLI entrypoint",
		TaskSlug:      "create-cli-entrypoint",
		ToolCallKey:   "finalize-1",
		ToolName:      "finalize_pipeline",
		ArgsSummary:   "summary=Inspector audit complete.",
		Phase:         0,
		StartedAt:     time.Now(),
	})
	app = model.(*AppModel)

	state, ok := app.streamedResponses["corr-inspector-live-tool"]
	if !ok {
		t.Fatal("expected recorded stream state for active inspector correlation")
	}
	if !state.HadChunk {
		t.Fatalf("expected tool start on an active primary stream to preserve recorded output state, got %+v", state)
	}
	if entry := app.streamEntryForCorrelation("corr-inspector-live-tool"); entry == nil || entry.AgentID != "task_3:inspector-pipeline" {
		t.Fatalf("expected active inspector stream to remain registered under the same primary row, got %+v", entry)
	}
}

func TestResolveIncomingStreamBranchRef_PreservesRecordedNestedBranchAfterUnregister(t *testing.T) {
	app := newStreamTelemetryModel()
	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent",
		ParentToolCallKey:   "challenge-1",
		Kind:                "challenge",
		ThreadKey:           "pipeline:task_1-challenge-1",
	}

	app.recordStreamStart("corr-child")
	app.recordStreamBranchRef("corr-child", branchRef)
	app.nestedStreams["corr-child"] = &activeStreamEntry{
		CorrelationID: "corr-child",
		AgentID:       "task_1:tester-pipeline",
		AgentType:     "tester-pipeline",
		BranchRef:     cloneInterAgentBranchRef(branchRef),
	}
	app.unregisterStream("corr-child")

	resolved := app.resolveIncomingStreamBranchRef("corr-child", "", nil, false)
	if resolved == nil {
		t.Fatal("expected recorded nested branch metadata to survive stream unregister")
	}
	if resolved.ParentCorrelationID != "corr-parent" || resolved.ParentToolCallKey != "challenge-1" || resolved.Kind != "challenge" {
		t.Fatalf("resolved branch ref = %+v, want preserved recorded branch metadata", resolved)
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

func TestPipelineHandoffDoesNotLeaveInspectorWaitingForChildWork(t *testing.T) {
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

	startedAt := time.Now().Add(-50 * time.Millisecond)
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
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Phase:         0,
		StartedAt:     startedAt,
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
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Output:        `{"selected":true,"target_agents":["tester-pipeline"],"challenge_id":"pipeline-review-1"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      50 * time.Millisecond,
		Success:       true,
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

	model, _ = app.Update(msg.StreamCompleteMsg{
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

	inspectorEntry := findChatEntryByCorrelation(app.chat, "corr-inspector")
	if inspectorEntry == nil {
		t.Fatal("expected inspector entry to remain present")
	}
	if inspectorEntry.Streaming {
		t.Fatalf("expected inspector entry to stop streaming after handoff: %+v", inspectorEntry)
	}
	if strings.TrimSpace(inspectorEntry.ThinkingStatus) != "" {
		t.Fatalf("expected inspector waiting status to clear after handoff, got %q", inspectorEntry.ThinkingStatus)
	}
	if view := app.chat.View(); strings.Contains(view, "Waiting for child work to finish...") {
		t.Fatalf("unexpected deferred child-work status after tester handoff: %q", view)
	}
	if !strings.Contains(app.chat.View(), "coord_publish_artifact") {
		t.Fatalf("expected tester coordination tool call to remain visible, got %q", app.chat.View())
	}
}

func TestTopLevelTransferToolCallClearsStaleNestedTesterChildState(t *testing.T) {
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

	startedAt := time.Now().Add(-50 * time.Millisecond)
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
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Phase:         0,
		StartedAt:     startedAt,
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
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Output:        `{"selected":true,"target_agents":["tester-pipeline"],"challenge_id":"pipeline-review-1"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      50 * time.Millisecond,
		Success:       true,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-tester",
		AgentID:       "runtime-tester",
		AgentType:     "tester-pipeline",
		AgentName:     "Tester",
		PipelineID:    "task_auth_checkout",
		TaskID:        "task_auth_checkout",
		TaskName:      "Auth Checkout",
		TaskSlug:      "auth-checkout",
		Message:       "stale nested progress",
		Visibility:    events.VisibilityUser,
		BranchRef: &msg.InterAgentBranchRefMsg{
			ParentCorrelationID: "corr-inspector",
			ParentToolCallKey:   "challenge-1",
			ThreadKey:           "pipeline:pipeline-review-1",
			Kind:                "challenge",
		},
	})
	app = model.(*AppModel)

	inspectorEntry := findChatEntryByCorrelation(app.chat, "corr-inspector")
	if inspectorEntry == nil || len(inspectorEntry.ToolCalls) != 1 || inspectorEntry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected inspector challenge row before top-level transfer, got %+v", inspectorEntry)
	}
	if got := len(inspectorEntry.ToolCalls[0].InterAgent.Children); got != 1 {
		t.Fatalf("expected stale nested tester child before transfer, got %d", got)
	}

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-tester",
		ParentCorrelationID: "corr-inspector",
		TopLevelTransfer:    true,
		AgentID:             "runtime-tester",
		AgentType:           "tester-pipeline",
		AgentName:           "Tester",
		PipelineID:          "task_auth_checkout",
		TaskID:              "task_auth_checkout",
		TaskName:            "Auth Checkout",
		TaskSlug:            "auth-checkout",
		ToolCallKey:         "tool-1",
		ToolName:            "coord_publish_artifact",
		ArgsSummary:         "type=verification_result",
		Phase:               0,
		StartedAt:           time.Now(),
	})
	app = model.(*AppModel)

	if _, ok := app.activeStreams["corr-tester"]; !ok {
		t.Fatal("expected tester to register as a primary active stream after top-level transfer")
	}
	if _, ok := app.nestedStreams["corr-tester"]; ok {
		t.Fatal("expected explicit top-level transfer to clear stale nested stream state")
	}

	testerEntry := findChatEntryByCorrelation(app.chat, "corr-tester")
	if testerEntry == nil {
		t.Fatal("expected tester to appear as a top-level chat entry")
	}
	if got := len(testerEntry.ToolCalls); got != 1 {
		t.Fatalf("tester tool call count = %d, want 1", got)
	}
	if testerEntry.ToolCalls[0].InterAgent != nil {
		t.Fatalf("coord_publish_artifact should stay a normal tool row, got %+v", testerEntry.ToolCalls[0].InterAgent)
	}

	inspectorEntry = findChatEntryByCorrelation(app.chat, "corr-inspector")
	if inspectorEntry == nil || len(inspectorEntry.ToolCalls) != 1 || inspectorEntry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected inspector challenge row after transfer, got %+v", inspectorEntry)
	}
	if got := len(inspectorEntry.ToolCalls[0].InterAgent.Children); got != 0 {
		t.Fatalf("expected stale nested tester child to be removed after top-level transfer, got %d", got)
	}
	if view := app.chat.View(); strings.Contains(view, "stale nested progress") {
		t.Fatalf("unexpected stale nested tester child render after top-level transfer: %q", view)
	}
}

func TestTopLevelHandoffStart_ClearsDeferredThinkingWithoutExplicitReroute(t *testing.T) {
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

	startedAt := time.Now().Add(-50 * time.Millisecond)
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
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Phase:         0,
		StartedAt:     startedAt,
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
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Output:        `{"selected":true,"target_agents":["tester-pipeline"],"challenge_id":"pipeline-review-1"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      50 * time.Millisecond,
		Success:       true,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-tester",
		ParentCorrelationID: "corr-inspector",
		TopLevelTransfer:    true,
		AgentID:             "runtime-tester",
		AgentType:           "tester-pipeline",
		AgentName:           "Tester",
		PipelineID:          "task_auth_checkout",
		TaskID:              "task_auth_checkout",
		TaskName:            "Auth Checkout",
		TaskSlug:            "auth-checkout",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
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

	inspectorEntry := findChatEntryByCorrelation(app.chat, "corr-inspector")
	if inspectorEntry == nil {
		t.Fatal("expected inspector entry to remain present")
	}
	if inspectorEntry.Streaming {
		t.Fatalf("expected inspector entry to stop streaming after top-level handoff: %+v", inspectorEntry)
	}
	if strings.TrimSpace(inspectorEntry.ThinkingStatus) != "" {
		t.Fatalf("expected inspector waiting status to clear after top-level handoff, got %q", inspectorEntry.ThinkingStatus)
	}
	if view := app.chat.View(); strings.Contains(view, "Waiting for child work to finish...") {
		t.Fatalf("unexpected deferred child-work status after top-level handoff: %q", view)
	}
}

func TestTopLevelHandoffProgressBootstrap_ClearsDeferredThinkingWithoutExplicitReroute(t *testing.T) {
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

	startedAt := time.Now().Add(-50 * time.Millisecond)
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
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Phase:         0,
		StartedAt:     startedAt,
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
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_agent",
		FullArgs:      `{"target_agents":["tester-pipeline"],"request":"Run the pipeline audit."}`,
		Output:        `{"selected":true,"target_agents":["tester-pipeline"],"challenge_id":"pipeline-review-1"}`,
		Phase:         1,
		StartedAt:     startedAt,
		Duration:      50 * time.Millisecond,
		Success:       true,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-tester",
		ParentCorrelationID: "corr-inspector",
		TopLevelTransfer:    true,
		AgentID:             "runtime-tester",
		AgentType:           "tester-pipeline",
		AgentName:           "Tester",
		PipelineID:          "task_auth_checkout",
		TaskID:              "task_auth_checkout",
		TaskName:            "Auth Checkout",
		TaskSlug:            "auth-checkout",
		Message:             "Running the top-level validation pass.",
		Visibility:          events.VisibilityUser,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
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

	if view := app.chat.View(); strings.Contains(view, "Waiting for child work to finish...") {
		t.Fatalf("unexpected deferred child-work status after progress-bootstrap handoff: %q", view)
	}
}

func TestTopLevelHandoffReturn_CreatesNewInspectorChatEntry(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-inspector-initial",
		AgentID:        "runtime-inspector",
		RuntimeAgentID: "runtime-inspector",
		AgentType:      "inspector-pipeline",
		AgentName:      "Inspector",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
		TaskName:       "Auth Checkout",
		TaskSlug:       "auth-checkout",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-inspector-initial",
		AgentID:        "runtime-inspector",
		RuntimeAgentID: "runtime-inspector",
		AgentType:      "inspector-pipeline",
		AgentName:      "Inspector",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
		TaskName:       "Auth Checkout",
		TaskSlug:       "auth-checkout",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-tester-top-level",
		ParentCorrelationID: "corr-inspector-initial",
		TopLevelTransfer:    true,
		AgentID:             "runtime-tester",
		RuntimeAgentID:      "runtime-tester",
		AgentType:           "tester-pipeline",
		AgentName:           "Tester",
		PipelineID:          "task_auth_checkout",
		TaskID:              "task_auth_checkout",
		TaskName:            "Auth Checkout",
		TaskSlug:            "auth-checkout",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-tester-top-level",
		AgentID:        "runtime-tester",
		RuntimeAgentID: "runtime-tester",
		AgentType:      "tester-pipeline",
		AgentName:      "Tester",
		PipelineID:     "task_auth_checkout",
		TaskID:         "task_auth_checkout",
		TaskName:       "Auth Checkout",
		TaskSlug:       "auth-checkout",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-inspector-return",
		ParentCorrelationID: "corr-tester-top-level",
		TopLevelTransfer:    true,
		AgentID:             "runtime-inspector",
		RuntimeAgentID:      "runtime-inspector",
		AgentType:           "inspector-pipeline",
		AgentName:           "Inspector",
		PipelineID:          "task_auth_checkout",
		TaskID:              "task_auth_checkout",
		TaskName:            "Auth Checkout",
		TaskSlug:            "auth-checkout",
	})
	app = model.(*AppModel)

	if old := findChatEntryByCorrelation(app.chat, "corr-inspector-initial"); old == nil {
		t.Fatal("expected old inspector correlation to remain in the transcript")
	}
	resumed := findChatEntryByCorrelation(app.chat, "corr-inspector-return")
	if resumed == nil {
		t.Fatal("expected new inspector chat entry after handoff return")
	}
	if !resumed.Streaming {
		t.Fatalf("expected resumed inspector row to be live, got %+v", resumed)
	}
	if strings.TrimSpace(resumed.ThinkingText) == "" {
		t.Fatalf("expected resumed inspector footer to restart, got %+v", resumed)
	}
	if tester := findChatEntryByCorrelation(app.chat, "corr-tester-top-level"); tester == nil {
		t.Fatal("expected top-level tester handoff row to remain present")
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

func TestLateNestedChildToolAndGrandchildStreamAfterRouteResponseRemainVisible(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:            "inspector-origin-late-nested-after-response",
		Timestamp:     time.Now(),
		CorrelationID: "corr-parent-late-nested-after-response",
		Source:        chatpkg.SourceAgent,
		AgentType:     "inspector",
		Content:       "Reviewing the strongest alternatives.",
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
					Summary:    "Challenge the current implementation.",
					Status:     chatpkg.InterAgentToolPending,
				},
			},
		},
	})

	parentBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-late-nested-after-response",
		ParentToolCallKey:   "consult-academic-1",
		Kind:                "consult",
	}

	if cmd := app.handleGuideResponse(msg.GuideResponseMsg{
		CorrelationID: "corr-child-academic-late-after-response",
		AgentID:       "academic",
		AgentType:     "academic",
		Content:       "The current approach is probably acceptable.",
		BranchRef:     parentBranch,
	}); cmd != nil {
		_ = cmd()
	}

	if _, ok := app.nestedStreams["corr-child-academic-late-after-response"]; ok {
		t.Fatal("expected nested stream registration to stay cleared after synthetic child completion")
	}

	model, _ := app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-late-after-response",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		ToolCallKey:   "consult-lib-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"librarian","query":"Find benchmark and methodology sources."}`,
		Phase:         0,
		StartedAt:     time.Now().Add(-300 * time.Millisecond),
		BranchRef:     parentBranch,
	})
	app = model.(*AppModel)

	librarianBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-child-academic-late-after-response",
		ParentToolCallKey:   "consult-lib-1",
		Kind:                "consult",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-grandchild-librarian-late-after-response",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		BranchRef:     librarianBranch,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-grandchild-librarian-late-after-response",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=framework benchmark methodology",
		FullArgs:      `{"query":"framework benchmark methodology"}`,
		Phase:         0,
		StartedAt:     time.Now().Add(-150 * time.Millisecond),
		BranchRef:     librarianBranch,
	})
	app = model.(*AppModel)

	parent := findChatEntryByCorrelation(app.chat, "corr-parent-late-nested-after-response")
	if parent == nil || len(parent.ToolCalls) != 1 || parent.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected parent consult row after late nested events, got %+v", parent)
	}
	root := parent.ToolCalls[0].InterAgent
	if len(root.Children) != 1 {
		t.Fatalf("expected one academic child activity, got %+v", root.Children)
	}
	academic := root.Children[0]
	if got := len(academic.ToolCalls); got != 1 {
		t.Fatalf("expected academic child consult row after late events, got %+v", academic.ToolCalls)
	}
	consult := academic.ToolCalls[0].InterAgent
	if consult == nil {
		t.Fatalf("expected academic child consult branch, got %+v", academic.ToolCalls[0])
	}
	if got := len(consult.Children); got != 1 {
		t.Fatalf("expected one librarian grandchild activity, got %+v", consult.Children)
	}
	librarian := consult.Children[0]
	if librarian.CorrelationID != "corr-grandchild-librarian-late-after-response" {
		t.Fatalf("librarian correlation = %q, want corr-grandchild-librarian-late-after-response", librarian.CorrelationID)
	}
	if got := len(librarian.ToolCalls); got != 1 {
		t.Fatalf("expected librarian grandchild tool call after late events, got %+v", librarian.ToolCalls)
	}
	if librarian.ToolCalls[0].ToolName != "web_search" {
		t.Fatalf("librarian grandchild tool = %q, want web_search", librarian.ToolCalls[0].ToolName)
	}
	if findChatEntryByCorrelation(app.chat, "corr-child-academic-late-after-response") != nil {
		t.Fatal("expected academic child to remain nested after late events")
	}
	if findChatEntryByCorrelation(app.chat, "corr-grandchild-librarian-late-after-response") != nil {
		t.Fatal("expected librarian grandchild to remain nested after late events")
	}
}

func TestHandleGuideResponse_TopLevelPendingChildWorkKeepsParentResumable(t *testing.T) {
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
	if parent.Streaming {
		t.Fatalf("expected parent entry to finalize while child work remains nested, got %+v", parent)
	}
	if parent.Content != "Research synthesis is ready." {
		t.Fatalf("parent content = %q, want authoritative route-response content", parent.Content)
	}
	if strings.TrimSpace(parent.ThinkingStatus) != "" || strings.TrimSpace(parent.ThinkingText) != "" {
		t.Fatalf("expected parent waiting footer to clear after completion, got %+v", parent)
	}
	if !app.chat.HasPendingCorrelation("corr-parent-deferred-route") {
		t.Fatal("expected completed parent correlation to remain resumable for follow-up work")
	}
	if entry := app.streamEntryForCorrelation("corr-parent-deferred-route"); entry == nil {
		t.Fatal("expected route-response parent stream entry to remain registered for follow-up work")
	}
	if findChatEntryByCorrelation(app.chat, "corr-child-deferred-route") != nil {
		t.Fatal("expected child guardian activity to stay nested")
	}

	beforeChildText := parent.ToolCalls[0].InterAgent.Children[0].ThinkingText
	model, _ = app.Update(msg.DecorTickMsg{
		Time: time.Now().Add(700 * time.Millisecond),
		Gen:  app.decorGen,
	})
	app = model.(*AppModel)

	parent = findChatEntryByCorrelation(app.chat, "corr-parent-deferred-route")
	if parent == nil {
		t.Fatal("expected parent entry after decor tick")
	}
	if got := parent.ToolCalls[0].InterAgent.Children[0].ThinkingText; got == beforeChildText {
		t.Fatalf("expected nested child spinner/timer to keep animating, got %q", got)
	}
}

func TestPostConsultParentResume_StreamStartKeepsDeferredParentCorrelatedByRuntimeID(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-parent-runtime-consult",
		AgentID:        "academic",
		RuntimeAgentID: "runtime-academic",
		AgentType:      "academic",
		AgentName:      "Academic",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-runtime-consult",
		AgentID:       "runtime-academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		ToolCallKey:   "consult-guardian-runtime",
		ToolName:      "consult",
		FullArgs:      `{"target":"guardian","query":"Check the current safety assumptions."}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	app = model.(*AppModel)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-runtime-consult",
		ParentToolCallKey:   "consult-guardian-runtime",
		Kind:                "consult",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-child-runtime-consult",
		AgentID:        "guardian",
		RuntimeAgentID: "guardian",
		AgentType:      "guardian",
		AgentName:      "Guardian",
		BranchRef:      branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.GuideResponseMsg{
		CorrelationID: "corr-parent-runtime-consult",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		Content:       "Research synthesis is ready.",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-child-runtime-consult",
		AgentID:           "guardian",
		RuntimeAgentID:    "guardian",
		AgentType:         "guardian",
		AgentName:         "Guardian",
		AuthoritativeText: "Safety assumptions confirmed.",
		BranchRef:         branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-parent-runtime-followup",
		AgentID:        "academic",
		RuntimeAgentID: "runtime-academic",
		AgentType:      "academic",
		AgentName:      "Academic",
	})
	app = model.(*AppModel)

	if old := findChatEntryByCorrelation(app.chat, "corr-parent-runtime-consult"); old != nil {
		t.Fatalf("expected deferred parent correlation to be replaced on consult resume, got %+v", old)
	}
	resumed := findChatEntryByCorrelation(app.chat, "corr-parent-runtime-followup")
	if resumed == nil {
		t.Fatal("expected resumed parent entry after consult completion")
	}
	if resumed.AgentID != "academic" {
		t.Fatalf("resumed parent AgentID = %q, want academic", resumed.AgentID)
	}
	if resumed.Content != "Research synthesis is ready." {
		t.Fatalf("resumed parent content = %q, want preserved deferred content", resumed.Content)
	}
}

func TestPostConsultParentResume_ProgressBootstrapKeepsDeferredParentCorrelatedByRuntimeID(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-parent-runtime-progress",
		AgentID:        "academic",
		RuntimeAgentID: "runtime-academic",
		AgentType:      "academic",
		AgentName:      "Academic",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-runtime-progress",
		AgentID:       "runtime-academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		ToolCallKey:   "consult-guardian-progress",
		ToolName:      "consult",
		FullArgs:      `{"target":"guardian","query":"Check the current safety assumptions."}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	app = model.(*AppModel)

	branchRef := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-runtime-progress",
		ParentToolCallKey:   "consult-guardian-progress",
		Kind:                "consult",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-child-runtime-progress",
		AgentID:        "guardian",
		RuntimeAgentID: "guardian",
		AgentType:      "guardian",
		AgentName:      "Guardian",
		BranchRef:      branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.GuideResponseMsg{
		CorrelationID: "corr-parent-runtime-progress",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		Content:       "Research synthesis is ready.",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamCompleteMsg{
		SessionID:         "s1",
		CorrelationID:     "corr-child-runtime-progress",
		AgentID:           "guardian",
		RuntimeAgentID:    "guardian",
		AgentType:         "guardian",
		AgentName:         "Guardian",
		AuthoritativeText: "Safety assumptions confirmed.",
		BranchRef:         branchRef,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamProgressMsg{
		SessionID:      "s1",
		CorrelationID:  "corr-parent-runtime-progress-followup",
		AgentID:        "academic",
		RuntimeAgentID: "runtime-academic",
		AgentType:      "academic",
		AgentName:      "Academic",
		Message:        "Refining the final recommendation.",
	})
	app = model.(*AppModel)

	if old := findChatEntryByCorrelation(app.chat, "corr-parent-runtime-progress"); old != nil {
		t.Fatalf("expected deferred parent correlation to be replaced after progress bootstrap, got %+v", old)
	}
	resumed := findChatEntryByCorrelation(app.chat, "corr-parent-runtime-progress-followup")
	if resumed == nil {
		t.Fatal("expected resumed parent entry after progress bootstrap")
	}
	if resumed.AgentID != "academic" {
		t.Fatalf("resumed parent AgentID = %q, want academic", resumed.AgentID)
	}
	if resumed.Content != "Research synthesis is ready." {
		t.Fatalf("resumed parent content = %q, want preserved deferred content", resumed.Content)
	}
	if strings.TrimSpace(resumed.ThinkingStatus) == "" {
		t.Fatalf("expected resumed parent to keep active progress after bootstrap, got %+v", resumed)
	}
}

func TestChallengeReturn_ProgressBootstrapWaitsForAuthoritativeStart(t *testing.T) {
	app := seedDeferredInspectorChallengeReturn(t)

	model, _ := app.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-inspector-followup-progress",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		Message:       "Processing the returned challenge evidence.",
	})
	app = model.(*AppModel)

	if duplicate := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-followup-progress"); duplicate != nil {
		t.Fatalf("expected ambiguous challenge-return progress to avoid creating a duplicate inspector row, got %+v", duplicate)
	}
	if pending := len(app.delayedPrimaryBootstrap["corr-parent-inspector-followup-progress"]); pending != 1 {
		t.Fatalf("delayed primary bootstrap count = %d, want 1 buffered progress event", pending)
	}
	if original := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-challenge"); original == nil {
		t.Fatal("expected original deferred inspector row to remain visible")
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-parent-inspector-followup-progress",
		ParentCorrelationID: "corr-child-tester-challenge",
		TopLevelTransfer:    true,
		AgentID:             "task_1:inspector-pipeline",
		RuntimeAgentID:      "runtime-inspector",
		AgentType:           "inspector-pipeline",
		AgentName:           "Inspector",
		TaskID:              "task_1",
		TaskName:            "Create hello.py CLI module with argparse and greet function",
		TaskSlug:            "create-cli-module",
	})
	app = model.(*AppModel)

	if old := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-challenge"); old != nil {
		t.Fatalf("expected original deferred inspector correlation to be replaced after authoritative resume, got %+v", old)
	}
	resumed := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-followup-progress")
	if resumed == nil {
		t.Fatal("expected authoritative inspector resume to reuse the original row")
	}
	if len(app.delayedPrimaryBootstrap["corr-parent-inspector-followup-progress"]) != 0 {
		t.Fatalf("expected delayed progress buffer to clear after authoritative start, got %d pending events", len(app.delayedPrimaryBootstrap["corr-parent-inspector-followup-progress"]))
	}
	if strings.TrimSpace(resumed.ThinkingText) == "" {
		t.Fatalf("expected resumed inspector row to remain live after authoritative resume, got %+v", resumed)
	}
}

func TestChallengeReturn_ToolCallBootstrapWaitsForAuthoritativeStart(t *testing.T) {
	app := seedDeferredInspectorChallengeReturn(t)

	model, _ := app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-parent-inspector-followup-tool",
		AgentID:       "inspector-pipeline",
		AgentType:     "inspector-pipeline",
		AgentName:     "Inspector",
		ToolCallKey:   "process-validation-1",
		ToolName:      "process_validation",
		FullArgs:      `{"validation_id":"val-1","decision":"accept"}`,
		Phase:         0,
		StartedAt:     time.Now(),
	})
	app = model.(*AppModel)

	if duplicate := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-followup-tool"); duplicate != nil {
		t.Fatalf("expected ambiguous challenge-return tool bootstrap to avoid creating a duplicate inspector row, got %+v", duplicate)
	}
	if pending := len(app.delayedPrimaryBootstrap["corr-parent-inspector-followup-tool"]); pending != 1 {
		t.Fatalf("delayed primary bootstrap count = %d, want 1 buffered tool event", pending)
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-parent-inspector-followup-tool",
		ParentCorrelationID: "corr-child-tester-challenge",
		TopLevelTransfer:    true,
		AgentID:             "task_1:inspector-pipeline",
		RuntimeAgentID:      "runtime-inspector",
		AgentType:           "inspector-pipeline",
		AgentName:           "Inspector",
		TaskID:              "task_1",
		TaskName:            "Create hello.py CLI module with argparse and greet function",
		TaskSlug:            "create-cli-module",
	})
	app = model.(*AppModel)

	resumed := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-followup-tool")
	if resumed == nil {
		t.Fatal("expected authoritative inspector resume after buffered tool bootstrap")
	}
	if len(app.delayedPrimaryBootstrap["corr-parent-inspector-followup-tool"]) != 0 {
		t.Fatalf("expected delayed tool buffer to clear after authoritative start, got %d pending events", len(app.delayedPrimaryBootstrap["corr-parent-inspector-followup-tool"]))
	}
	if strings.TrimSpace(resumed.ThinkingText) == "" {
		t.Fatalf("expected resumed inspector row to remain live after buffered tool bootstrap, got %+v", resumed)
	}
}

func TestChallengeReturn_AuthoritativeStartReusesDeferredInspectorWhenTaskIdentityArrivesOnResume(t *testing.T) {
	app := seedDeferredInspectorChallengeReturnUnscopedParent(t)

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:           "s1",
		CorrelationID:       "corr-parent-inspector-followup-unscoped",
		ParentCorrelationID: "corr-child-tester-unscoped",
		TopLevelTransfer:    true,
		AgentID:             "task_1:inspector-pipeline",
		RuntimeAgentID:      "runtime-inspector",
		AgentType:           "inspector-pipeline",
		AgentName:           "Inspector",
		TaskID:              "task_1",
		TaskName:            "Create hello.py CLI module with argparse and greet function",
		TaskSlug:            "create-cli-module",
	})
	app = model.(*AppModel)

	if old := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-unscoped"); old != nil {
		t.Fatalf("expected original unscoped inspector correlation to be replaced after authoritative resume, got %+v", old)
	}
	resumed := findChatEntryByCorrelation(app.chat, "corr-parent-inspector-followup-unscoped")
	if resumed == nil {
		t.Fatal("expected authoritative inspector resume to reuse the original row when task identity arrives late")
	}
	if resumed.Content != "Waiting for the tester challenge result." {
		t.Fatalf("resumed inspector content = %q, want preserved deferred content", resumed.Content)
	}
	if resumed.TaskID != "task_1" {
		t.Fatalf("resumed inspector task_id = %q, want task_1", resumed.TaskID)
	}
	if strings.TrimSpace(resumed.ThinkingText) == "" {
		t.Fatalf("expected resumed inspector row to remain live after authoritative resume, got %+v", resumed)
	}
}

func TestHandleGuideResponse_NestedConsultRowsRemainVisibleAfterChildStreamText(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 120, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:             "architect-origin-nested-consult-after-text",
		Timestamp:      time.Now(),
		CorrelationID:  "corr-parent-nested-consult-after-text",
		Source:         chatpkg.SourceAgent,
		AgentType:      "architect",
		Content:        "Architect draft text should stay hidden while the consult runs.",
		Streaming:      true,
		ThinkingText:   "⠋  0.3s",
		ThinkingStatus: "Waiting on academic consult.",
		Height:         -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolCallKey: "consult-academic-1",
				ToolName:    "consult_academic_approach",
				Completed:   true,
				Success:     true,
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Build the research evidence base.",
					Status:     chatpkg.InterAgentToolDone,
				},
			},
		},
	})

	parentBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-parent-nested-consult-after-text",
		ParentToolCallKey:   "consult-academic-1",
		Kind:                "consult",
	}

	model, _ := app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-after-text",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		BranchRef:     parentBranch,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.StreamChunkMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-after-text",
		Text:          "Initial academic draft text that should remain nested.",
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-academic-after-text",
		AgentID:       "academic",
		AgentType:     "academic",
		AgentName:     "Academic",
		ToolCallKey:   "consult-lib-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"librarian","query":"Find benchmark and methodology sources."}`,
		Phase:         0,
		StartedAt:     time.Now().Add(-250 * time.Millisecond),
		BranchRef:     parentBranch,
	})
	app = model.(*AppModel)

	librarianBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-child-academic-after-text",
		ParentToolCallKey:   "consult-lib-1",
		Kind:                "consult",
	}

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-librarian-after-text",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		BranchRef:     librarianBranch,
	})
	app = model.(*AppModel)

	model, _ = app.Update(msg.ToolCallEventMsg{
		SessionID:     "s1",
		CorrelationID: "corr-child-librarian-after-text",
		AgentID:       "librarian",
		AgentType:     "librarian",
		AgentName:     "Librarian",
		ToolCallKey:   "ws_1",
		ToolName:      "web_search",
		ArgsSummary:   "query=framework benchmark methodology",
		FullArgs:      `{"query":"framework benchmark methodology"}`,
		Phase:         0,
		StartedAt:     time.Now().Add(-150 * time.Millisecond),
		BranchRef:     librarianBranch,
	})
	app = model.(*AppModel)

	rendered := app.chat.View()
	if !strings.Contains(rendered, "Architect draft text should stay hidden while the consult runs.") {
		t.Fatalf("expected parent draft text to remain visible while nested consult work is active, got %q", rendered)
	}
	for _, needle := range []string{
		"academic",
		"librarian",
		"Find benchmark and methodology sources.",
		"web_search",
		"framework benchmark methodology",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected nested consult chat render to contain %q, got %q", needle, rendered)
		}
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
