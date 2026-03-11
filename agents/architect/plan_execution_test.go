package architect

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/dag"
)

func testArchitectWithStore(t *testing.T, plans ...*DesignPlan) *Architect {
	t.Helper()
	store := testPlanStore(t)
	for _, p := range plans {
		if p.sm == nil {
			p.sm = NewPlanStateMachine(p.ID, p.Status)
		}
		_ = store.Upsert(p)
	}
	return &Architect{
		planStore: store,
		logger:    slog.Default(),
	}
}

func TestLatestReadyPlan_NoPlans(t *testing.T) {
	a := testArchitectWithStore(t)
	if a.latestReadyPlan("sess1") != nil {
		t.Error("expected nil with no plans")
	}
}

func TestLatestReadyPlan_EmptySessionID(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "a", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now},
	)
	if a.latestReadyPlan("") != nil {
		t.Error("expected nil with empty session ID")
	}
}

func TestLatestReadyPlan_NoneReady(t *testing.T) {
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "a", SessionID: "sess1", Status: PlanStatusAnalyzing, UpdatedAt: time.Now()},
		&DesignPlan{ID: "b", SessionID: "sess1", Status: PlanStatusDesigning, UpdatedAt: time.Now()},
	)
	if a.latestReadyPlan("sess1") != nil {
		t.Error("expected nil with no ready plans")
	}
}

func TestLatestReadyPlan_FiltersSession(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "other", SessionID: "sess-old", Status: PlanStatusReady, UpdatedAt: now},
		&DesignPlan{ID: "mine", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now},
	)

	plan := a.latestReadyPlan("sess1")
	if plan == nil || plan.ID != "mine" {
		t.Errorf("expected 'mine', got %v", plan)
	}

	plan = a.latestReadyPlan("sess-old")
	if plan == nil || plan.ID != "other" {
		t.Errorf("expected 'other', got %v", plan)
	}

	if a.latestReadyPlan("sess-unknown") != nil {
		t.Error("expected nil for unknown session")
	}
}

func TestLatestReadyPlan_SelectsMostRecent(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "old", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now.Add(-5 * time.Minute)},
		&DesignPlan{ID: "new", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now.Add(-1 * time.Minute)},
	)

	plan := a.latestReadyPlan("sess1")
	if plan == nil || plan.ID != "new" {
		t.Errorf("expected 'new', got %v", plan)
	}
}

func TestLatestStalledPlan_FindsConsultingPlan(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "stalled", SessionID: "sess1", Status: PlanStatusConsulting, UpdatedAt: now},
	)
	plan := a.latestStalledPlan("sess1")
	if plan == nil || plan.ID != "stalled" {
		t.Errorf("expected stalled plan, got %v", plan)
	}
}

func TestLatestStalledPlan_IgnoresReadyPlan(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "ready", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now},
	)
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for ready plan")
	}
}

func TestLatestStalledPlan_IgnoresClarifyingPlan(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "clarifying", SessionID: "sess1", Status: PlanStatusClarifying, UpdatedAt: now},
	)
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for clarifying plan")
	}
}

func TestLatestStalledPlan_IgnoresOldPlan(t *testing.T) {
	old := time.Now().Add(-10 * time.Minute)
	a := testArchitectWithStore(t,
		&DesignPlan{ID: "old", SessionID: "sess1", Status: PlanStatusConsulting, UpdatedAt: old},
	)
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for plan older than stalledPlanMaxAge")
	}
}

func TestLatestStalledPlanForRequest_FiltersByCorrelation(t *testing.T) {
	now := time.Now()
	a := testArchitectWithStore(t,
		&DesignPlan{
			ID:                   "wanted",
			SessionID:            "sess1",
			Status:               PlanStatusConsulting,
			RequestCorrelationID: "corr-1",
			UpdatedAt:            now,
		},
		&DesignPlan{
			ID:                   "other",
			SessionID:            "sess1",
			Status:               PlanStatusGenerating,
			RequestCorrelationID: "corr-2",
			UpdatedAt:            now.Add(time.Second),
		},
	)
	plan := a.latestStalledPlanForRequest("sess1", "corr-1")
	if plan == nil || plan.ID != "wanted" {
		t.Fatalf("latestStalledPlanForRequest = %v, want wanted", plan)
	}
}

func TestLatestActivePendingPlan_SelectsMostRecent(t *testing.T) {
	now := time.Now().UTC()
	a := testArchitectWithStore(t,
		&DesignPlan{
			ID:        "older",
			SessionID: "sess1",
			Status:    PlanStatusReady,
			UpdatedAt: now.Add(-time.Minute),
			PendingWork: &PendingContinuation{
				Kind:      string(continuationKindGuardianApproval),
				Status:    string(continuationStatusPending),
				Message:   "older pending",
				ExpiresAt: now.Add(time.Minute),
			},
		},
		&DesignPlan{
			ID:        "newer",
			SessionID: "sess1",
			Status:    PlanStatusReady,
			UpdatedAt: now,
			PendingWork: &PendingContinuation{
				Kind:      string(continuationKindAcceptanceEval),
				Status:    string(continuationStatusPending),
				Message:   "newer pending",
				ExpiresAt: now.Add(time.Minute),
			},
		},
	)

	plan := a.latestActivePendingPlan("sess1")
	if plan == nil || plan.ID != "newer" {
		t.Fatalf("latestActivePendingPlan = %v, want newer", plan)
	}
}

func TestLatestActivePendingPlan_IgnoresExpiredPendingWork(t *testing.T) {
	now := time.Now().UTC()
	a := testArchitectWithStore(t,
		&DesignPlan{
			ID:        "expired",
			SessionID: "sess1",
			Status:    PlanStatusReady,
			UpdatedAt: now,
			PendingWork: &PendingContinuation{
				Kind:      string(continuationKindGuardianApproval),
				Status:    string(continuationStatusPending),
				Message:   "expired pending",
				ExpiresAt: now.Add(-time.Second),
			},
		},
	)

	if plan := a.latestActivePendingPlan("sess1"); plan != nil {
		t.Fatalf("latestActivePendingPlan = %v, want nil", plan)
	}
}

func TestHandleExecute_ReturnsPendingMessageWhenPlanWorkIsInFlight(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess1",
		Status:    PlanStatusReady,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:      string(continuationKindGuardianApproval),
			Status:    string(continuationStatusPending),
			Message:   "Guardian is reviewing the plan response.",
			ExpiresAt: now.Add(time.Minute),
		},
	}
	a := testArchitectWithStore(t, plan)

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: "sess1",
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if conv.Response != "Guardian is reviewing the plan response." {
		t.Fatalf("response = %q, want pending message", conv.Response)
	}
	if conv.Intent != IntentExecute {
		t.Fatalf("intent = %s, want execute", conv.Intent)
	}
}

func TestHandleExecute_RehydratesOrphanedPendingContinuationFromControlStore(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-orphan",
		SessionID: "sess1",
		Status:    PlanStatusReady,
		UpdatedAt: now,
	}
	a := testArchitectWithControlStore(t, plan)
	record := &ArchitectContinuation{
		ID:                    "cont-1",
		Kind:                  continuationKindGuardianApproval,
		State:                 continuationStatusPending,
		PlanID:                plan.ID,
		SessionID:             plan.SessionID,
		TargetAgentID:         "guardian",
		ResponseCorrelationID: "corr-1",
		CreatedAt:             now.Add(5 * time.Second),
		ExpiresAt:             now.Add(time.Minute),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: "sess1",
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if conv.Response != "The latest plan response is still going through Guardian approval. I'll update you shortly." {
		t.Fatalf("response = %q, want guardian pending message", conv.Response)
	}
	stored := a.planStore.Get(plan.ID)
	if stored == nil || stored.PendingWork == nil {
		t.Fatal("expected orphaned continuation to be reattached to plan")
	}
	if stored.PendingWork.CorrelationID != "corr-1" {
		t.Fatalf("correlation_id = %q, want corr-1", stored.PendingWork.CorrelationID)
	}
}

func TestHandleExecute_RecoversExpiredPlanHandoffAndRedispatches(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess1",
		Query:     "build the feature",
		Status:    PlanStatusOrchestrating,
		Tasks: []*AtomicTask{
			{ID: "task-1", Name: "Task", AgentType: "engineer", Description: "Ship it"},
		},
		Workflow:  &WorkflowDAG{TotalTasks: 1, Tasks: []*AtomicTask{{ID: "task-1"}}, DAG: &dag.DAG{}},
		CreatedAt: now,
		UpdatedAt: now,
		PendingWork: &PendingContinuation{
			Kind:          string(continuationKindPlanHandoff),
			Status:        string(continuationStatusPending),
			TargetAgentID: "orchestrator",
			CorrelationID: "corr-expired",
			Message:       "handoff queued",
			CreatedAt:     now.Add(-2 * time.Minute),
			ExpiresAt:     now.Add(-time.Minute),
		},
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	a.running = true
	a.bus = &testBus{}

	record := &ArchitectContinuation{
		ID:                    "cont-expired",
		Kind:                  continuationKindPlanHandoff,
		State:                 continuationStatusPending,
		PlanID:                plan.ID,
		SessionID:             plan.SessionID,
		TargetAgentID:         "orchestrator",
		ResponseCorrelationID: "corr-expired",
		RequestJSON:           `{"plan_id":"plan-1"}`,
		CreatedAt:             now.Add(-2 * time.Minute),
		ExpiresAt:             now.Add(-time.Minute),
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		t.Fatalf("put continuation: %v", err)
	}

	result, err := a.handleExecute(context.Background(), &guide.ForwardedRequest{
		Input:     "go ahead",
		SessionID: plan.SessionID,
	})
	if err != nil {
		t.Fatalf("handleExecute error = %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("handleExecute result type = %T, want *ConversationResult", result)
	}
	if !strings.Contains(conv.Response, "Plan handoff queued") {
		t.Fatalf("response = %q, want redispatch acknowledgment", conv.Response)
	}

	stored := a.planStore.Get(plan.ID)
	if stored == nil {
		t.Fatal("expected plan to remain present")
	}
	if stored.SM().State() != PlanStatusOrchestrating {
		t.Fatalf("plan state = %s, want orchestrating after redispatch", stored.SM().State())
	}
	if stored.PendingWork == nil || stored.PendingWork.CorrelationID == "corr-expired" {
		t.Fatalf("expected new pending handoff continuation, got %+v", stored.PendingWork)
	}

	expiredRecord, err := a.controlStore.GetContinuationByResponseCorrelation("corr-expired")
	if err != nil {
		t.Fatalf("load expired continuation: %v", err)
	}
	if expiredRecord == nil || expiredRecord.State != continuationStatusFailed {
		t.Fatalf("expired continuation = %+v, want failed", expiredRecord)
	}

	bus := a.bus.(*testBus)
	if len(bus.published) != 1 {
		t.Fatalf("expected one published route request, got %d", len(bus.published))
	}
}

func TestIsStalledState(t *testing.T) {
	stalled := []PlanStatus{
		PlanStatusPending, PlanStatusAnalyzing, PlanStatusConsulting,
		PlanStatusDesigning, PlanStatusGenerating, PlanStatusOrchestrating,
	}
	for _, s := range stalled {
		if !isStalledState(s) {
			t.Errorf("expected %s to be stalled", s)
		}
	}
	notStalled := []PlanStatus{
		PlanStatusReady, PlanStatusExecuting, PlanStatusCompleted,
		PlanStatusFailed, PlanStatusClarifying,
	}
	for _, s := range notStalled {
		if isStalledState(s) {
			t.Errorf("expected %s to NOT be stalled", s)
		}
	}
}
