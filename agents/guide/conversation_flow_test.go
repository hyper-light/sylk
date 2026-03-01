package guide

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestConversationFlow_SuggestsActiveAgentForGuideFollowup(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("session-1", "architect")

	target, ok := flow.SuggestedTarget(
		"session-1",
		"guide",
		&RouteResult{Intent: IntentChat, Confidence: 0.6},
	)
	if !ok {
		t.Fatal("expected suggested target")
	}
	if target != "architect" {
		t.Fatalf("target = %q, want architect", target)
	}
}

func TestConversationFlow_DoesNotOverrideHighConfidenceSwitch(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("session-1", "architect")

	_, ok := flow.SuggestedTarget(
		"session-1",
		"engineer",
		&RouteResult{Intent: IntentSearch, Confidence: 0.97},
	)
	if ok {
		t.Fatal("did not expect active-agent override for high-confidence switch")
	}
}

func TestConversationFlow_UserDoneClearsSession(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("session-1", "architect")
	flow.ObserveUserInput("session-1", "we are done")

	_, ok := flow.SuggestedTarget(
		"session-1",
		"guide",
		&RouteResult{Intent: IntentChat, Confidence: 0.2},
	)
	if ok {
		t.Fatal("expected no suggested target after user completion signal")
	}
}

func TestConversationFlow_AgentDoneClearsSession(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("session-1", "architect")
	flow.ObserveResponse("session-1", "architect", "All tasks completed.")

	_, ok := flow.SuggestedTarget(
		"session-1",
		"guide",
		&RouteResult{Intent: IntentChat, Confidence: 0.3},
	)
	if ok {
		t.Fatal("expected no suggested target after agent completion signal")
	}
}

func TestConversationHistory_RecordAndRetrieve(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")

	flow.RecordUserInput("s1", "architect", "plan auth")
	flow.RecordAgentReply("s1", "architect", "here is the plan")

	flow.RecordUserInput("s1", "architect", "add rate limiting")
	flow.RecordAgentReply("s1", "architect", "rate limiting added")

	flow.RecordUserInput("s1", "architect", "review dependencies")
	flow.RecordAgentReply("s1", "architect", "deps look good")

	history := flow.HistoryForSession("s1")
	if len(history) != 3 {
		t.Fatalf("len(history) = %d, want 3", len(history))
	}
	if history[0].UserInput != "plan auth" {
		t.Fatalf("history[0].UserInput = %q, want %q", history[0].UserInput, "plan auth")
	}
	if history[2].AgentReply != "deps look good" {
		t.Fatalf("history[2].AgentReply = %q, want %q", history[2].AgentReply, "deps look good")
	}
	for i, turn := range history {
		if turn.AgentID != "architect" {
			t.Fatalf("history[%d].AgentID = %q, want architect", i, turn.AgentID)
		}
	}
}

func TestConversationHistory_ObserveRoutedRequestRetainsHistoryForSameAgent(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})

	flow.ObserveRoutedRequest("s1", "architect")
	flow.RecordUserInput("s1", "architect", "plan auth")
	flow.RecordAgentReply("s1", "architect", "here is the plan")

	// Typical runtime flow: each prompt observes a routed target first.
	flow.ObserveRoutedRequest("s1", "architect")
	flow.RecordUserInput("s1", "architect", "add rate limiting")

	history := flow.HistoryForSession("s1")
	if len(history) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(history))
	}
	if history[0].UserInput != "plan auth" {
		t.Fatalf("history[0].UserInput = %q, want plan auth", history[0].UserInput)
	}
	if history[0].AgentReply != "here is the plan" {
		t.Fatalf("history[0].AgentReply = %q, want here is the plan", history[0].AgentReply)
	}
	if history[1].UserInput != "add rate limiting" {
		t.Fatalf("history[1].UserInput = %q, want add rate limiting", history[1].UserInput)
	}
}

func TestConversationHistory_RingEviction(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")

	for i := range maxConversationTurns + 4 {
		flow.RecordUserInput("s1", "architect", fmt.Sprintf("msg-%d", i))
		flow.RecordAgentReply("s1", "architect", fmt.Sprintf("reply-%d", i))
	}

	history := flow.HistoryForSession("s1")
	if len(history) != maxConversationTurns {
		t.Fatalf("len(history) = %d, want %d", len(history), maxConversationTurns)
	}
	// Oldest surviving turn should be msg-4 (0-3 evicted).
	if history[0].UserInput != "msg-4" {
		t.Fatalf("history[0].UserInput = %q, want msg-4", history[0].UserInput)
	}
	last := history[maxConversationTurns-1]
	if last.UserInput != fmt.Sprintf("msg-%d", maxConversationTurns+3) {
		t.Fatalf("last.UserInput = %q, want msg-%d", last.UserInput, maxConversationTurns+3)
	}
}

func TestConversationHistory_ClearRemovesHistory(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")
	flow.RecordUserInput("s1", "architect", "hello")
	flow.RecordAgentReply("s1", "architect", "hi")

	flow.Clear("s1")

	history := flow.HistoryForSession("s1")
	if len(history) != 0 {
		t.Fatalf("len(history) = %d after Clear, want 0", len(history))
	}
}

func TestConversationHistory_TruncatesLongContent(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")

	longInput := strings.Repeat("x", maxConversationTurnContentLen+500)
	flow.RecordUserInput("s1", "architect", longInput)

	history := flow.HistoryForSession("s1")
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if len([]rune(history[0].UserInput)) != maxConversationTurnContentLen {
		t.Fatalf("truncated rune len = %d, want %d", len([]rune(history[0].UserInput)), maxConversationTurnContentLen)
	}
}

func TestConversationHistory_AgentSwitchTracksSeparateActiveHistory(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")
	flow.RecordUserInput("s1", "architect", "plan auth")
	flow.RecordAgentReply("s1", "architect", "done")

	// Switch to librarian
	flow.ObserveRoutedRequest("s1", "librarian")
	flow.RecordUserInput("s1", "librarian", "find auth files")

	history := flow.HistoryForSession("s1")
	if len(history) != 1 {
		t.Fatalf("len(history) = %d after active-agent switch, want 1", len(history))
	}
	if history[0].UserInput != "find auth files" {
		t.Fatalf("history[0].UserInput = %q, want %q", history[0].UserInput, "find auth files")
	}
}

func TestConversationHistory_AgentSwitchPreservesPriorAgentHistory(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")
	flow.RecordUserInput("s1", "architect", "plan auth")
	flow.RecordAgentReply("s1", "architect", "done")

	flow.ObserveRoutedRequest("s1", "librarian")
	flow.RecordUserInput("s1", "librarian", "find auth files")
	flow.RecordAgentReply("s1", "librarian", "found references")

	flow.ObserveRoutedRequest("s1", "architect")
	flow.RecordUserInput("s1", "architect", "refine auth plan")

	architectHistory := flow.HistoryForSessionAgent("s1", "architect")
	if len(architectHistory) != 2 {
		t.Fatalf("len(architectHistory) = %d, want 2", len(architectHistory))
	}
	if architectHistory[0].UserInput != "plan auth" {
		t.Fatalf("architectHistory[0].UserInput = %q, want plan auth", architectHistory[0].UserInput)
	}
	if architectHistory[1].UserInput != "refine auth plan" {
		t.Fatalf("architectHistory[1].UserInput = %q, want refine auth plan", architectHistory[1].UserInput)
	}

	librarianHistory := flow.HistoryForSessionAgent("s1", "librarian")
	if len(librarianHistory) != 1 {
		t.Fatalf("len(librarianHistory) = %d, want 1", len(librarianHistory))
	}
	if librarianHistory[0].UserInput != "find auth files" {
		t.Fatalf("librarianHistory[0].UserInput = %q, want find auth files", librarianHistory[0].UserInput)
	}
}

func TestConversationHistory_MismatchedReplyDropped(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")
	flow.RecordUserInput("s1", "architect", "plan auth")
	// Reply from a different agent should be silently dropped.
	flow.RecordAgentReply("s1", "librarian", "wrong agent reply")

	history := flow.HistoryForSession("s1")
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if history[0].AgentReply != "" {
		t.Fatalf("AgentReply = %q, want empty (mismatched reply should be dropped)", history[0].AgentReply)
	}
}

// =============================================================================
// Pending Plan Tracking Tests
// =============================================================================

func TestSetPendingPlan_StoresFromDirective(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-123",
			"epoch":   uint64(5),
		},
		TTL: 5 * time.Minute,
	})

	pp := flow.PendingPlan("s1")
	if pp == nil {
		t.Fatal("expected non-nil pending plan")
	}
	if pp.PlanID != "plan-123" {
		t.Fatalf("PlanID = %q, want plan-123", pp.PlanID)
	}
	if pp.AgentID != "architect" {
		t.Fatalf("AgentID = %q, want architect", pp.AgentID)
	}
	if pp.Epoch != 5 {
		t.Fatalf("Epoch = %d, want 5", pp.Epoch)
	}
}

func TestPendingPlan_NilWhenExpired(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-old",
			"epoch":   uint64(1),
		},
		TTL: 1 * time.Nanosecond, // expires immediately
	})

	// The plan expires immediately due to nanosecond TTL.
	pp := flow.PendingPlan("s1")
	if pp != nil {
		t.Fatal("expected nil for expired pending plan")
	}
}

func TestClearPendingPlan_RemovesPlan(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-del",
			"epoch":   uint64(2),
		},
		TTL: 5 * time.Minute,
	})

	flow.ClearPendingPlan("s1")
	if flow.PendingPlan("s1") != nil {
		t.Fatal("expected nil after ClearPendingPlan")
	}
}

func TestSetPendingPlan_NilDirectiveIgnored(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", nil)
	if flow.PendingPlan("s1") != nil {
		t.Fatal("expected nil for nil directive")
	}
}

func TestSetPendingPlan_EmptySessionIgnored(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-nope",
			"epoch":   uint64(1),
		},
		TTL: 5 * time.Minute,
	})
	if flow.PendingPlan("") != nil {
		t.Fatal("expected nil for empty session")
	}
}

func TestSetPendingPlan_NonApprovalPhaseIgnored(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhaseNone,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-nophase",
			"epoch":   uint64(1),
		},
		TTL: 5 * time.Minute,
	})
	if flow.PendingPlan("s1") != nil {
		t.Fatal("expected nil for PhaseNone directive")
	}
}

func TestSessionClear_RemovesPendingPlan(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-clear",
			"epoch":   uint64(1),
		},
		TTL: 5 * time.Minute,
	})

	flow.Clear("s1")
	if flow.PendingPlan("s1") != nil {
		t.Fatal("expected nil pending plan after session Clear")
	}
}

func TestPendingPlanFromDirective_Float64Epoch(t *testing.T) {
	directive := &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-float",
			"epoch":   float64(99),
		},
		TTL: 5 * time.Minute,
	}

	pp := pendingPlanFromDirective(directive)
	if pp == nil {
		t.Fatal("expected non-nil pending plan")
	}
	if pp.Epoch != 99 {
		t.Fatalf("Epoch = %d, want 99 (from float64)", pp.Epoch)
	}
}

