package architect

import (
	"testing"
	"time"
)

func TestIsExecutionRequest(t *testing.T) {
	positives := []string{
		// Substring matches.
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
		"run it",
		"fire away",
		"confirmed",
		"make it so",
		"let's start",
		"start building",
		// Exact affirmatives.
		"yes",
		"Yes",
		"YES",
		"y",
		"yep",
		"yeah",
		"yup",
		"ok",
		"okay",
		"sure",
		"right",
		"great",
		"perfect",
		"awesome",
		"absolutely",
		"affirmative",
		"roger",
		"aye",
		// Exact with trailing punctuation.
		"yes!",
		"sure.",
		"ok!",
		"Yeah!",
		"absolutely!",
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
		// Short affirmatives embedded in longer questions should NOT match.
		"yes, but can we change the database layer?",
		"ok so what about the auth flow?",
		"sure, but I have some concerns about scaling",
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
