package architect

import (
	"testing"
	"time"
)

func TestLatestReadyPlan_NoPlans(t *testing.T) {
	a := &Architect{
		activePlans: map[string]*DesignPlan{},
	}
	if a.latestReadyPlan("sess1") != nil {
		t.Error("expected nil with no plans")
	}
}

func TestLatestReadyPlan_EmptySessionID(t *testing.T) {
	now := time.Now()
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"a": {ID: "a", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now},
		},
	}
	if a.latestReadyPlan("") != nil {
		t.Error("expected nil with empty session ID")
	}
}

func TestLatestReadyPlan_NoneReady(t *testing.T) {
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"a": {ID: "a", SessionID: "sess1", Status: PlanStatusAnalyzing},
			"b": {ID: "b", SessionID: "sess1", Status: PlanStatusDesigning},
		},
	}
	if a.latestReadyPlan("sess1") != nil {
		t.Error("expected nil with no ready plans")
	}
}

func TestLatestReadyPlan_FiltersSession(t *testing.T) {
	now := time.Now()
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"other": {ID: "other", SessionID: "sess-old", Status: PlanStatusReady, UpdatedAt: now},
			"mine":  {ID: "mine", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now},
		},
	}

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
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"old": {ID: "old", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now.Add(-5 * time.Minute)},
			"new": {ID: "new", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now.Add(-1 * time.Minute)},
		},
	}

	plan := a.latestReadyPlan("sess1")
	if plan == nil || plan.ID != "new" {
		t.Errorf("expected 'new', got %v", plan)
	}
}

func TestLatestStalledPlan_FindsConsultingPlan(t *testing.T) {
	now := time.Now()
	sm := NewPlanStateMachine("stalled", PlanStatusConsulting)
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"stalled": {ID: "stalled", SessionID: "sess1", Status: PlanStatusConsulting, UpdatedAt: now, sm: sm},
		},
	}
	plan := a.latestStalledPlan("sess1")
	if plan == nil || plan.ID != "stalled" {
		t.Errorf("expected stalled plan, got %v", plan)
	}
}

func TestLatestStalledPlan_IgnoresReadyPlan(t *testing.T) {
	now := time.Now()
	sm := NewPlanStateMachine("ready", PlanStatusReady)
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"ready": {ID: "ready", SessionID: "sess1", Status: PlanStatusReady, UpdatedAt: now, sm: sm},
		},
	}
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for ready plan")
	}
}

func TestLatestStalledPlan_IgnoresClarifyingPlan(t *testing.T) {
	now := time.Now()
	sm := NewPlanStateMachine("clarifying", PlanStatusClarifying)
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"clarifying": {ID: "clarifying", SessionID: "sess1", Status: PlanStatusClarifying, UpdatedAt: now, sm: sm},
		},
	}
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for clarifying plan")
	}
}

func TestLatestStalledPlan_IgnoresOldPlan(t *testing.T) {
	old := time.Now().Add(-10 * time.Minute)
	sm := NewPlanStateMachine("old", PlanStatusConsulting)
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"old": {ID: "old", SessionID: "sess1", Status: PlanStatusConsulting, UpdatedAt: old, sm: sm},
		},
	}
	if a.latestStalledPlan("sess1") != nil {
		t.Error("expected nil for plan older than stalledPlanMaxAge")
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
