package scribe

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

func TestPassesProfileFilters_RejectsForeignAgentType(t *testing.T) {
	s := &Scribe{parentAgentType: "tester-pipeline"}
	profile := ProfileFor("tester-pipeline")
	a := activity.AgentActivity{
		Actor:      activity.Actor{AgentType: "engineer"},
		Action:     activity.ActionDecisionDeclared,
		Resolution: activity.ResolutionCoarse,
	}
	if s.passesProfileFilters(a, profile) {
		t.Fatal("scribe must not accept activities from other agent types")
	}
}

func TestPassesProfileFilters_AcceptsOwnAgentType(t *testing.T) {
	s := &Scribe{parentAgentType: "tester-pipeline"}
	profile := ProfileFor("tester-pipeline")
	a := activity.AgentActivity{
		Actor:      activity.Actor{AgentType: "tester-pipeline"},
		Action:     activity.ActionDecisionDeclared,
		Resolution: activity.ResolutionCoarse,
	}
	if !s.passesProfileFilters(a, profile) {
		t.Fatal("scribe must accept activities from its own agent type")
	}
}

func TestPassesProfileFilters_RejectsAtomicResolution(t *testing.T) {
	s := &Scribe{parentAgentType: "tester-pipeline"}
	profile := ProfileFor("tester-pipeline")
	a := activity.AgentActivity{
		Actor:      activity.Actor{AgentType: "tester-pipeline"},
		Action:     activity.ActionFileRead,
		Resolution: activity.ResolutionAtomic,
	}
	if s.passesProfileFilters(a, profile) {
		t.Fatal("scribe must not accept Atomic activities by default")
	}
}

func TestPassesProfileFilters_RejectsRecursiveScribeNarration(t *testing.T) {
	s := &Scribe{parentAgentType: "tester-pipeline"}
	profile := ProfileFor("tester-pipeline")
	a := activity.AgentActivity{
		Actor: activity.Actor{
			AgentType: "scribe-tester-pipeline", // a scribe of the same type
		},
		Action:     activity.ActionNarrationEmitted,
		Resolution: activity.ResolutionCoarse,
	}
	if s.passesProfileFilters(a, profile) {
		t.Fatal("scribe must never narrate its own (or peer scribes') narrations to avoid recursion")
	}
}

func TestPassesProfileFilters_RespectsIgnoreKinds(t *testing.T) {
	defer cleanupRegisteredProfile(t, "scribe-test-ignore-filter")
	RegisterProfile(ScribeProfile{
		AgentType:   "scribe-test-ignore-filter",
		IgnoreKinds: []activity.ActionKind{activity.ActionToolCallStarted},
	})
	s := &Scribe{parentAgentType: "scribe-test-ignore-filter"}
	profile := ProfileFor("scribe-test-ignore-filter")
	a := activity.AgentActivity{
		Actor:      activity.Actor{AgentType: "scribe-test-ignore-filter"},
		Action:     activity.ActionToolCallStarted,
		Resolution: activity.ResolutionFine,
	}
	if s.passesProfileFilters(a, profile) {
		t.Fatal("ignore-kind filter must reject the activity")
	}
}

func TestBatchKey_PrefersPipelineID(t *testing.T) {
	s := &Scribe{parentAgentType: "tester-pipeline"}
	a := activity.AgentActivity{
		Actor: activity.Actor{
			AgentType:  "tester-pipeline",
			AgentID:    "tester-1",
			PipelineID: "pipe-7",
		},
	}
	if got := s.batchKey(a); got != "pipe-7" {
		t.Fatalf("batchKey = %q, want pipe-7", got)
	}
}

func TestBatchKey_FallsBackToAgentID(t *testing.T) {
	s := &Scribe{parentAgentType: "architect"}
	a := activity.AgentActivity{
		Actor: activity.Actor{
			AgentType: "architect",
			AgentID:   "arch-1",
		},
	}
	if got := s.batchKey(a); got != "arch-1" {
		t.Fatalf("batchKey = %q, want arch-1", got)
	}
}

func TestBatchAdmit_AccumulatesActivities(t *testing.T) {
	s := &Scribe{parentAgentType: "tester-pipeline"}
	for i := 0; i < 5; i++ {
		a := activity.AgentActivity{
			Actor:      activity.Actor{AgentType: "tester-pipeline", PipelineID: "pipe-1"},
			Action:     activity.ActionDecisionDeclared,
			Resolution: activity.ResolutionCoarse,
		}
		s.batchAdmit(a)
	}
	raw, ok := s.fabricBatches().Load("pipe-1")
	if !ok {
		t.Fatal("expected batch for pipe-1")
	}
	batch := raw.(*fabricBatch)
	batch.mu.Lock()
	defer batch.mu.Unlock()
	if len(batch.activities) != 5 {
		t.Fatalf("batch size = %d, want 5", len(batch.activities))
	}
	if batch.firstSeenAt.IsZero() {
		t.Error("firstSeenAt should be set after first activity")
	}
}

func TestBuildBatchUserRequest_IncludesAllActivities(t *testing.T) {
	batch := []activity.AgentActivity{
		{
			ID:         "act-1",
			Action:     activity.ActionDecisionDeclared,
			Resolution: activity.ResolutionCoarse,
			Subject:    activity.Subject{Domain: "test_framework", PathPrefix: "tests/"},
			Confidence: activity.ConfidenceTentative,
		},
		{
			ID:         "act-2",
			Action:     activity.ActionToolCallCompleted,
			Resolution: activity.ResolutionFine,
		},
	}
	got := buildBatchUserRequest("tester-pipeline", "batch_size", batch)
	for _, want := range []string{
		"tester-pipeline",
		"batch_size",
		"Batch size: 2",
		"decision_declared",
		"test_framework",
		"tests/",
		"tentative",
		"tool_call_completed",
	} {
		if !contains(got, want) {
			t.Errorf("buildBatchUserRequest output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestBuildBatchAgentResponse_IsValidJSON(t *testing.T) {
	batch := []activity.AgentActivity{
		{
			ID:     "act-1",
			Action: activity.ActionDecisionDeclared,
			State:  activity.StatePoint,
		},
	}
	got := buildBatchAgentResponse(batch)
	if !contains(got, `"action":"decision_declared"`) {
		t.Errorf("response should contain action field; got %s", got)
	}
	if !contains(got, `"id":"act-1"`) {
		t.Errorf("response should contain id field; got %s", got)
	}
}

func TestIsTurnBoundaryAction(t *testing.T) {
	cases := map[activity.ActionKind]bool{
		activity.ActionDAGAccepted:          true,
		activity.ActionValidationAccepted:   true,
		activity.ActionValidationRejected:   true,
		activity.ActionPlanRatified:         true,
		activity.ActionCharterRatified:      true,
		activity.ActionDecisionDeclared:     false,
		activity.ActionToolCallStarted:      false,
		activity.ActionFileRead:             false,
	}
	for kind, want := range cases {
		if got := isTurnBoundaryAction(kind); got != want {
			t.Errorf("isTurnBoundaryAction(%s) = %v, want %v", kind, got, want)
		}
	}
}

// TestEvaluateBatchTrigger_EmphasizedKindFiresImmediately documents
// that an emphasize-kind activity short-circuits batch buffering and
// triggers a narration as soon as it arrives. The dispatch happens on
// a goroutine so the trigger evaluator returns synchronously; the
// test waits briefly to confirm the dispatch went through.
//
// This test uses a real Scribe but bypasses the LLM by registering a
// profile that emphasizes a kind we then admit. The dispatched
// narration goroutine will fail to call the LLM (no provider wired)
// but the dispatched-batch state we can observe — the batch buffer
// should have been emptied.
func TestEvaluateBatchTrigger_EmphasizedKindFiresImmediately(t *testing.T) {
	defer cleanupRegisteredProfile(t, "scribe-test-emphasize-trigger")
	RegisterProfile(ScribeProfile{
		AgentType:      "scribe-test-emphasize-trigger",
		EmphasizeKinds: []activity.ActionKind{activity.ActionEscalationRequested},
		BatchSize:      99, // ensure size threshold doesn't fire instead
		BatchWindow:    time.Hour,
	})
	// Properly initialize the scribe (workstreams map is required by
	// processFeed). A real scribe would call New(); we bypass New()
	// because the LLM provider isn't available — but we still need
	// the maps so processFeed doesn't nil-panic.
	s := &Scribe{
		parentAgentType: "scribe-test-emphasize-trigger",
		workstreams:     make(map[string]*scribeWorkstream),
		maxMessages:     defaultMaxMessages,
		maxToolRuns:     defaultMaxToolRuns,
	}
	s.running.Store(true)
	defer s.running.Store(false)

	a := activity.AgentActivity{
		Actor:      activity.Actor{AgentType: "scribe-test-emphasize-trigger", PipelineID: "p1"},
		Action:     activity.ActionEscalationRequested,
		Resolution: activity.ResolutionCoarse,
	}
	s.batchAdmit(a)
	// Evaluate manually rather than via Receive (which would invoke
	// the LLM-backed processFeed).
	s.evaluateBatchTrigger(context.Background(), a, ProfileFor("scribe-test-emphasize-trigger"), false)

	// Allow the dispatch goroutine to consume the batch.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		raw, ok := s.fabricBatches().Load("p1")
		if !ok {
			break
		}
		batch := raw.(*fabricBatch)
		batch.mu.Lock()
		empty := len(batch.activities) == 0
		batch.mu.Unlock()
		if empty {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	raw, ok := s.fabricBatches().Load("p1")
	if !ok {
		return // dispatched and removed
	}
	batch := raw.(*fabricBatch)
	batch.mu.Lock()
	defer batch.mu.Unlock()
	if len(batch.activities) != 0 {
		t.Fatalf("emphasize-kind should have dispatched and emptied the batch; %d activities remain", len(batch.activities))
	}
}

// contains is a small substring helper to avoid pulling strings into
// this test file just for one ContainsRune-style check.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOfSubstr(haystack, needle) >= 0
}

func indexOfSubstr(haystack, needle string) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
