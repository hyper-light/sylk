package guide

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestGuideApplyConversationFlow_RemapsFollowUpToActiveAgent(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	if err := g.Register(&AgentRoutingInfo{
		ID:   "architect",
		Type: "architect",
		Name: "architect",
		Registration: &AgentRegistration{
			ID:   "architect",
			Name: "architect",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentPlan, IntentDesign, IntentCheck, IntentHelp},
				Domains: []Domain{DomainDesign, DomainTasks, DomainGeneral},
			},
		},
	}); err != nil {
		t.Fatalf("register architect: %v", err)
	}

	g.conversation.ObserveRoutedRequest("session-1", "architect")
	classification := &RouteResult{
		Intent:               IntentChat,
		Domain:               DomainGeneral,
		TargetAgent:          TargetGuide,
		Confidence:           0.61,
		Action:               RouteActionExecute,
		ClassificationMethod: "llm",
	}
	request := &RouteRequest{
		Input:         "Can we tweak that plan?",
		SessionID:     "session-1",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	}

	updated, target := g.applyConversationFlow(context.Background(), request, classification, "guide")
	if target != "architect" {
		t.Fatalf("target = %q, want architect", target)
	}
	if updated == nil || updated.TargetAgent != TargetAgent("architect") {
		t.Fatalf("updated target = %v, want architect", updated.TargetAgent)
	}
	if updated.Intent != IntentPlan {
		t.Fatalf("intent = %q, want %q", updated.Intent, IntentPlan)
	}
}

func TestClearStickiness_PreservesHistory(t *testing.T) {
	m := NewConversationFlowManager(ConversationFlowConfig{})

	m.ObserveRoutedRequest("sess-1", "architect")
	m.RecordUserInput("sess-1", "architect", "design a REST API")
	m.RecordAgentReply("sess-1", "architect", "here is the plan")

	// Verify stickiness is set.
	snap, ok := m.Snapshot("sess-1")
	if !ok || snap.ActiveAgentID != "architect" {
		t.Fatalf("pre-clear: want active=architect, got ok=%v active=%q", ok, snap.ActiveAgentID)
	}

	// ClearStickiness should clear ActiveAgentID but preserve history.
	m.ClearStickiness("sess-1")

	// ActiveAgentID should be empty.
	state, stateOK := m.sessionState("sess-1")
	if !stateOK {
		t.Fatal("session state should still exist after ClearStickiness")
	}
	if state.ActiveAgentID != "" {
		t.Fatalf("ActiveAgentID = %q, want empty", state.ActiveAgentID)
	}

	// History should still be present.
	history := m.HistoryForSessionAgent("sess-1", "architect")
	if len(history) == 0 {
		t.Fatal("history should be preserved after ClearStickiness")
	}
	if history[0].UserInput != "design a REST API" {
		t.Fatalf("UserInput = %q, want %q", history[0].UserInput, "design a REST API")
	}
	if history[0].AgentReply != "here is the plan" {
		t.Fatalf("AgentReply = %q, want %q", history[0].AgentReply, "here is the plan")
	}
}

func TestRecordInterruption_AddsMarker(t *testing.T) {
	m := NewConversationFlowManager(ConversationFlowConfig{})

	m.ObserveRoutedRequest("sess-1", "architect")
	m.RecordUserInput("sess-1", "architect", "build a cache layer")

	m.RecordInterruption("sess-1", "architect")

	history := m.HistoryForSessionAgent("sess-1", "architect")
	if len(history) == 0 {
		t.Fatal("history should contain the interrupted turn")
	}
	last := history[len(history)-1]
	if !strings.Contains(last.AgentReply, "(interrupted by user)") {
		t.Fatalf("AgentReply = %q, want to contain %q", last.AgentReply, "(interrupted by user)")
	}
}

func TestClearStickiness_NilAndEmpty(t *testing.T) {
	// nil receiver should not panic.
	var m *ConversationFlowManager
	m.ClearStickiness("sess-1")

	// Empty session ID should be a no-op.
	m2 := NewConversationFlowManager(ConversationFlowConfig{})
	m2.ClearStickiness("")
	m2.ClearStickiness("   ")
}

func TestClear_DestroysHistory(t *testing.T) {
	m := NewConversationFlowManager(ConversationFlowConfig{})

	m.ObserveRoutedRequest("sess-1", "architect")
	m.RecordUserInput("sess-1", "architect", "design a REST API")
	m.RecordAgentReply("sess-1", "architect", "here is the plan")

	// Full Clear should destroy everything.
	m.Clear("sess-1")

	history := m.HistoryForSessionAgent("sess-1", "architect")
	if len(history) != 0 {
		t.Fatalf("history should be empty after Clear, got %d turns", len(history))
	}
}

func TestGuideApplyConversationFlow_UsesWorkPreferenceForLowConfidenceGuideTarget(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	if err := g.Register(&AgentRoutingInfo{
		ID:   "engineer",
		Type: "engineer",
		Name: "engineer",
		Registration: &AgentRegistration{
			ID:   "engineer",
			Name: "engineer",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentComplete, IntentCheck, IntentHelp},
				Domains: []Domain{DomainCode, DomainFiles, DomainGeneral},
			},
		},
	}); err != nil {
		t.Fatalf("register engineer: %v", err)
	}

	req := &RouteRequest{
		Input:         "can you keep going on that?",
		SessionID:     "session-1",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
		Metadata: map[string]any{
			"dag_id":  "dag-123",
			"task_id": "task-123",
		},
	}
	g.observeRoutedConversationTarget(req, &RouteResult{
		Intent:      IntentComplete,
		Domain:      DomainCode,
		TargetAgent: TargetAgent("engineer"),
		Confidence:  0.95,
	}, "engineer")

	classification := &RouteResult{
		Intent:               IntentUnknown,
		Domain:               DomainGeneral,
		TargetAgent:          TargetGuide,
		Confidence:           0.42,
		Action:               RouteActionSuggest,
		ClassificationMethod: "llm",
	}

	updated, target := g.applyConversationFlow(context.Background(), req, classification, "guide")
	if target != "engineer" {
		t.Fatalf("target = %q, want engineer", target)
	}
	if updated == nil || updated.TargetAgent != TargetAgent("engineer") {
		t.Fatalf("updated target = %v, want engineer", updated.TargetAgent)
	}
	if !strings.Contains(updated.ClassificationMethod, "work_state") {
		t.Fatalf("classification method = %q, want work_state suffix", updated.ClassificationMethod)
	}
}
