package architect

import (
	"testing"
	"time"
)

func TestLatestReadyPlan_NoPlans(t *testing.T) {
	a := &Architect{
		activePlans: map[string]*DesignPlan{},
	}
	if a.latestReadyPlan() != nil {
		t.Error("expected nil with no plans")
	}
}

func TestLatestReadyPlan_NoneReady(t *testing.T) {
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"a": {ID: "a", Status: PlanStatusAnalyzing},
			"b": {ID: "b", Status: PlanStatusDesigning},
		},
	}
	if a.latestReadyPlan() != nil {
		t.Error("expected nil with no ready plans")
	}
}

func TestLatestReadyPlan_SelectsMostRecent(t *testing.T) {
	now := time.Now()
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"old": {ID: "old", Status: PlanStatusReady, UpdatedAt: now.Add(-5 * time.Minute)},
			"new": {ID: "new", Status: PlanStatusReady, UpdatedAt: now.Add(-1 * time.Minute)},
		},
	}

	plan := a.latestReadyPlan()
	if plan == nil || plan.ID != "new" {
		t.Errorf("expected 'new', got %v", plan)
	}
}
