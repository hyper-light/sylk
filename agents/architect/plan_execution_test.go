package architect

import "testing"

func TestIsExecutionRequest(t *testing.T) {
	positives := []string{
		"go ahead",
		"Go Ahead",
		"execute",
		"Execute the plan",
		"proceed",
		"let's do it",
		"let's go",
		"do it",
		"start execution",
		"run the plan",
		"ship it",
		"kick it off",
		"sounds good",
		"looks good",
		"approved",
		"lgtm",
		"LGTM",
		"  go ahead  ",
		"yes, go ahead and execute",
		"Sounds good, proceed with it",
	}
	for _, input := range positives {
		if !isExecutionRequest(input) {
			t.Errorf("expected true for %q", input)
		}
	}

	negatives := []string{
		"",
		"   ",
		"tell me more",
		"what does the plan look like",
		"how many tasks",
		"explain the architecture",
		"plan a new feature",
		"design this for me",
	}
	for _, input := range negatives {
		if isExecutionRequest(input) {
			t.Errorf("expected false for %q", input)
		}
	}
}

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
	a := &Architect{
		activePlans: map[string]*DesignPlan{
			"old": {ID: "old", Status: PlanStatusReady},
			"new": {ID: "new", Status: PlanStatusReady},
		},
	}
	// Set UpdatedAt so "new" is more recent.
	a.activePlans["new"].UpdatedAt = a.activePlans["old"].UpdatedAt.Add(1)

	plan := a.latestReadyPlan()
	if plan == nil || plan.ID != "new" {
		t.Errorf("expected 'new', got %v", plan)
	}
}
