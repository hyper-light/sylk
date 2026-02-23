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

	// Call executePlanningProtocol directly — Handle now routes IntentPlan
	// through conversation, so the persistence test uses the internal path.
	plan, err := a.executePlanningProtocol(context.Background(), &ArchitectRequest{
		ID:        "req_plan_persist",
		Intent:    IntentPlan,
		Query:     "design a robust cache integration",
		SessionID: "session_restore",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("planning protocol failed: %v", err)
	}
	if plan == nil {
		t.Fatal("expected non-nil plan")
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

func TestValidateDeclarationForPolicy_DoesNotHardFail(t *testing.T) {
	a := newTestArchitect(t, Config{})
	runner := &planningProtocolRunner{
		architect: a,
		plan:      &DesignPlan{},
	}
	declaration := &PreDelegationDeclaration{
		ConsultationChecks: map[string]*ConsultationEvidence{
			"librarian": {
				Target:     "librarian",
				Success:    false,
				Error:      "unsupported consultation payload",
				ReceivedAt: time.Now(),
			},
		},
	}
	if err := runner.validateDeclarationForPolicy(declaration); err != nil {
		t.Fatalf("expected warning-only declaration validation, got error: %v", err)
	}
	if len(runner.plan.RiskSummary) == 0 {
		t.Fatal("expected declaration validation warning to be captured in risk summary")
	}
}
