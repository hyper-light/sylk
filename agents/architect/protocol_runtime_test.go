package architect

import (
	"context"
	"errors"
	"math"
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

	// Ready-state plans are intentionally skipped on restore (the Guide's
	// phase gate is in-memory-only). Transition back to Pending so the
	// second architect can restore the plan.
	if err := plan.sm.TransitionTo(PlanStatusPending, plan); err != nil {
		t.Fatalf("transition Ready→Pending failed: %v", err)
	}
	plan.Status = PlanStatusPending
	plan.UpdatedAt = time.Now()
	if err := a.persistPlanState(plan); err != nil {
		t.Fatalf("re-persist failed: %v", err)
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

// newTestRunner builds a minimal planningProtocolRunner for reservation tests.
func newTestRunner(t *testing.T, ctx context.Context, llmTimeout, consultTimeout time.Duration) *planningProtocolRunner {
	t.Helper()
	a := newTestArchitect(t, Config{
		LLMRequestTimeout:   llmTimeout,
		ConsultationTimeout: consultTimeout,
	})
	plan := &DesignPlan{ID: "test-plan"}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusPending)
	return &planningProtocolRunner{
		architect: a,
		ctx:       ctx,
		plan:      plan,
		diag:      &protocolDiagnostics{},
	}
}

func TestReservationForCost(t *testing.T) {
	llmTimeout := 120 * time.Second
	consultTimeout := 20 * time.Second
	runner := newTestRunner(t, context.Background(), llmTimeout, consultTimeout)

	cases := []struct {
		cost stageCost
		want time.Duration
	}{
		{stageCostLocal, 0},
		{stageCostLLM, llmTimeout},
		{stageCostConsultation, consultTimeout},
	}
	for _, tc := range cases {
		got := runner.reservationForCost(tc.cost)
		if got != tc.want {
			t.Errorf("reservationForCost(%d) = %v, want %v", tc.cost, got, tc.want)
		}
	}
}

func TestDownstreamReserve(t *testing.T) {
	llmTimeout := 120 * time.Second
	consultTimeout := 20 * time.Second
	runner := newTestRunner(t, context.Background(), llmTimeout, consultTimeout)
	steps := runner.allStepsWithCosts()

	// Expected downstream sums at each index:
	// steps: LLM(120), Consult(20), LLM(120), LLM(120), LLM(120), Local(0), Local(0), LLM(120)
	// idx 0: 20+120+120+120+0+0+120 = 500
	// idx 1: 120+120+120+0+0+120 = 480
	// idx 2: 120+120+0+0+120 = 360
	// idx 3: 120+0+0+120 = 240
	// idx 4: 0+0+120 = 120
	// idx 5: 0+120 = 120
	// idx 6: 120
	// idx 7: 0
	expected := []time.Duration{
		500 * time.Second,
		480 * time.Second,
		360 * time.Second,
		240 * time.Second,
		120 * time.Second,
		120 * time.Second,
		120 * time.Second,
		0,
	}
	for i, want := range expected {
		got := runner.downstreamReserve(steps, i)
		if got != want {
			t.Errorf("downstreamReserve(steps, %d) = %v, want %v", i, got, want)
		}
	}
}

func TestRunStep_BudgetExhausted(t *testing.T) {
	// Deadline in 10s — not enough for LLM (120s) + downstream (240s).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := newTestRunner(t, ctx, 120*time.Second, 20*time.Second)

	called := false
	err := runner.runStep("testStep", func() error {
		called = true
		return nil
	}, stageCostLLM, 240*time.Second)

	if !errors.Is(err, errBudgetExhausted) {
		t.Fatalf("expected errBudgetExhausted, got %v", err)
	}
	if called {
		t.Fatal("step function should not have been called")
	}
}

func TestRunStep_LocalStepPasses(t *testing.T) {
	// Deadline in 5s — local cost (0) + zero downstream → should pass.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runner := newTestRunner(t, ctx, 120*time.Second, 20*time.Second)

	called := false
	err := runner.runStep("testLocal", func() error {
		called = true
		return nil
	}, stageCostLocal, 0)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("step function should have been called")
	}
}

func TestRunStep_LocalStepBlockedByDownstream(t *testing.T) {
	// Deadline in 10s — local cost (0) but downstream needs 120s.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := newTestRunner(t, ctx, 120*time.Second, 20*time.Second)

	called := false
	err := runner.runStep("testLocal", func() error {
		called = true
		return nil
	}, stageCostLocal, 120*time.Second)

	if !errors.Is(err, errBudgetExhausted) {
		t.Fatalf("expected errBudgetExhausted, got %v", err)
	}
	if called {
		t.Fatal("step function should not have been called when downstream reserve exceeds remaining")
	}
}

func TestBudgetExhaustedError_Unwrap(t *testing.T) {
	err := &budgetExhaustedError{
		step:              "stepDesign",
		remaining:         50 * time.Second,
		stepReservation:   120 * time.Second,
		downstreamReserve: 240 * time.Second,
	}
	if !errors.Is(err, errBudgetExhausted) {
		t.Fatal("expected errors.Is(err, errBudgetExhausted) to be true")
	}
	msg := err.Error()
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestRemainingBudget_NoDeadline(t *testing.T) {
	runner := newTestRunner(t, context.Background(), 120*time.Second, 20*time.Second)
	got := runner.remainingBudget()
	if got != time.Duration(math.MaxInt64) {
		t.Fatalf("expected math.MaxInt64 duration, got %v", got)
	}
}
