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
