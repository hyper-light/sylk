package architect

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchitect_PlanPersistsAndRestores(t *testing.T) {
	root := t.TempDir()
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 root,
	})

	resp, err := a.Handle(context.Background(), &ArchitectRequest{
		ID:        "req_plan_persist",
		Intent:    IntentPlan,
		Query:     "design a robust cache integration",
		SessionID: "session_restore",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("handle failed: %v", err)
	}
	plan, ok := resp.Data.(*DesignPlan)
	if !ok || plan == nil {
		t.Fatalf("expected *DesignPlan, got %T", resp.Data)
	}

	planPath := filepath.Join(root, ".sylk", "architect", "plans", plan.ID+".json")
	if _, statErr := os.Stat(planPath); statErr != nil {
		t.Fatalf("expected persisted plan at %s: %v", planPath, statErr)
	}

	reloaded := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 root,
	})
	if _, found := reloaded.GetActivePlan(plan.ID); !found {
		t.Fatalf("expected restored plan %s to be active", plan.ID)
	}
}

func TestValidatePlanForExecution_TaskContractFailure(t *testing.T) {
	plan := &DesignPlan{
		Requirements: &Requirements{Query: "q"},
		Architecture: &SolutionArchitecture{Name: "a"},
		Constraints:  &PlanConstraints{},
		Tasks: []*AtomicTask{{
			ID:          "task_1",
			Description: "",
		}},
		Workflow: &WorkflowDAG{Tasks: []*AtomicTask{{ID: "task_1"}}, TotalTasks: 1},
	}
	if err := validatePlanForExecution(plan); err == nil {
		t.Fatal("expected validation error for missing task contract fields")
	}
}
