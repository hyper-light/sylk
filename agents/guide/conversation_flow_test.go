package guide

import (
	"fmt"
	"strings"
	"testing"
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
// Phase Tracking Tests
// =============================================================================

func TestPhaseSetAndRetrieve(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPhase("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		TTL:     5 * 60_000_000_000, // 5 minutes
	})

	phase := flow.CurrentPhase("s1")
	if phase == nil {
		t.Fatal("expected non-nil phase")
	}
	if phase.Phase != PhasePlanApproval {
		t.Fatalf("phase = %q, want %q", phase.Phase, PhasePlanApproval)
	}
	if phase.AgentID != "architect" {
		t.Fatalf("agent_id = %q, want architect", phase.AgentID)
	}
}

func TestPhaseClear(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPhase("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		TTL:     5 * 60_000_000_000,
	})

	flow.ClearPhase("s1")
	if flow.CurrentPhase("s1") != nil {
		t.Fatal("expected nil after ClearPhase")
	}
}

func TestPhaseNilDirectiveIgnored(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPhase("s1", nil)
	if flow.CurrentPhase("s1") != nil {
		t.Fatal("expected nil for nil directive")
	}
}

func TestPhaseEmptySessionIgnored(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPhase("", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		TTL:     5 * 60_000_000_000,
	})
	if flow.CurrentPhase("") != nil {
		t.Fatal("expected nil for empty session")
	}
}

func TestPhaseNoPhaseIgnored(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPhase("s1", &ResponseDirective{
		Phase:   PhaseNone,
		AgentID: "architect",
		TTL:     5 * 60_000_000_000,
	})
	if flow.CurrentPhase("s1") != nil {
		t.Fatal("expected nil for PhaseNone directive")
	}
}

func TestSessionClearRemovesPhase(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.ObserveRoutedRequest("s1", "architect")
	flow.SetPhase("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		TTL:     5 * 60_000_000_000,
	})

	// Full session clear should remove the phase.
	flow.Clear("s1")
	if flow.CurrentPhase("s1") != nil {
		t.Fatal("expected nil phase after session Clear")
	}
}

// =============================================================================
// Polarity Classifier Tests
// =============================================================================

func TestClassifyPlanApprovalPolarity(t *testing.T) {
	positives := []string{
		"yes", "Yes", "YES", "y", "yep", "yeah", "yup",
		"ok", "okay", "sure", "right", "great", "perfect",
		"awesome", "absolutely", "affirmative", "roger", "aye",
		"approved", "lgtm",
		// With punctuation.
		"yes!", "sure.", "ok!",
		// Contains matches.
		"go ahead", "do it", "ship it", "sounds good",
		"looks good", "proceed", "execute the plan",
		"let's go", "let's do it", "hand off to orchestrator",
		"kick it off", "run it",
	}
	for _, input := range positives {
		if !classifyPlanApprovalPolarity(input) {
			t.Errorf("expected positive for %q", input)
		}
	}

	negatives := []string{
		"", "   ",
		"change the auth to JWT",
		"what about caching?",
		"I think we should use Redis instead",
		"can you add error handling for the database layer?",
		"the third task seems too complex",
		"explain the architecture decision",
		"why did you choose that approach?",
		// Embedded affirmatives in longer feedback should be negative.
		"yes, but can we change the database layer?",
		"ok so what about the auth flow?",
		"sure, but I have some concerns about scaling",
		// Negated positive phrases must be negative.
		"don't go ahead",
		"don't proceed",
		"do not execute",
		"don't execute the plan",
		"no, don't go ahead with that",
		"hold off on that",
		"wait, I need to review first",
		"nope",
		"stop",
		"cancel",
		"I disagree with this plan",
		"not yet, let me think about it",
	}
	for _, input := range negatives {
		if classifyPlanApprovalPolarity(input) {
			t.Errorf("expected negative for %q", input)
		}
	}
}

func TestIsPlanApprovalTopicEscape(t *testing.T) {
	escapes := []string{
		"find the login component in src/auth/login.go",
		"@librarian search for auth handlers",
		"@tester run the unit tests",
		"search for the database config",
		"/home/user/file.go",
		"show me the file for auth",
	}
	for _, input := range escapes {
		if !isPlanApprovalTopicEscape(input) {
			t.Errorf("expected escape for %q", input)
		}
	}

	nonEscapes := []string{
		"yes",
		"sounds good",
		"change the auth approach",
		"what about caching",
		"I disagree with the third task",
	}
	for _, input := range nonEscapes {
		if isPlanApprovalTopicEscape(input) {
			t.Errorf("expected non-escape for %q", input)
		}
	}
}
