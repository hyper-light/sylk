package architect

import (
	"log/slog"
	"testing"
	"time"
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
