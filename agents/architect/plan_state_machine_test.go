package architect

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

func newTestDAG() *dag.DAG {
	return dag.NewDAG("test", dag.ExecutionPolicy{})
}

// validPlan returns a DesignPlan that passes execution validation guards.
func validPlan() *DesignPlan {
	return &DesignPlan{
		ID:           "test-plan",
		SessionID:    "test-session",
		Query:        "test query",
		Status:       PlanStatusPending,
		Requirements: &Requirements{Query: "test"},
		Architecture: &SolutionArchitecture{Name: "test"},
		Constraints:  &PlanConstraints{MaxTasksPerAgent: 5},
		Tasks: []*AtomicTask{{
			ID:                  "task-1",
			Name:                "Test task",
			Description:         "Test description",
			AgentType:           "engineer",
			SuccessCriteria:     []string{"passes"},
			AcceptanceCriteria:  []AcceptanceCriterion{{Given: "g", When: "w", Then: "t"}},
			ImplementationGuide: "do the thing",
			AffectedFiles:       []TaskFileTarget{{Path: "main.go", Operation: "modify"}},
		}},
		Workflow: &WorkflowDAG{
			DAG:        newTestDAG(),
			Tasks:      []*AtomicTask{{ID: "task-1"}},
			TotalTasks: 1,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestPlanStatusClarifying_String(t *testing.T) {
	if PlanStatusClarifying.String() != "clarifying" {
		t.Fatalf("expected 'clarifying', got %q", PlanStatusClarifying.String())
	}
}

func TestPlanStatusClarifying_Value(t *testing.T) {
	if PlanStatusClarifying != 10 {
		t.Fatalf("expected PlanStatusClarifying == 10, got %d", PlanStatusClarifying)
	}
}

func TestNewPlanStateMachine_InitialState(t *testing.T) {
	sm := NewPlanStateMachine("plan-1", PlanStatusPending)
	if sm.State() != PlanStatusPending {
		t.Fatalf("expected Pending, got %s", sm.State())
	}
}

func TestTransitionTo_ValidTransitions(t *testing.T) {
	edges := []struct {
		from PlanStatus
		to   PlanStatus
	}{
		{PlanStatusPending, PlanStatusAnalyzing},
		{PlanStatusAnalyzing, PlanStatusConsulting},
		{PlanStatusConsulting, PlanStatusDesigning},
		{PlanStatusDesigning, PlanStatusGenerating},
		{PlanStatusGenerating, PlanStatusOrchestrating},
		{PlanStatusOrchestrating, PlanStatusReady},
		{PlanStatusReady, PlanStatusPending},
		{PlanStatusExecuting, PlanStatusCompleted},
		{PlanStatusExecuting, PlanStatusFailed},
		{PlanStatusExecuting, PlanStatusPending},
		{PlanStatusClarifying, PlanStatusPending},
	}
	for _, edge := range edges {
		sm := NewPlanStateMachine("plan", edge.from)
		plan := validPlan()
		plan.Status = edge.from
		// Clear clarification questions for Consulting→Designing guard.
		if edge.from == PlanStatusConsulting && edge.to == PlanStatusDesigning {
			plan.ClarificationQuestions = nil
		}
		if err := sm.TransitionTo(edge.to, plan); err != nil {
			t.Errorf("expected %s -> %s to succeed, got: %v", edge.from, edge.to, err)
		}
		if sm.State() != edge.to {
			t.Errorf("expected state %s, got %s", edge.to, sm.State())
		}
	}
}

func TestTransitionTo_ConsultingToClarifying(t *testing.T) {
	sm := NewPlanStateMachine("plan", PlanStatusConsulting)
	plan := validPlan()
	plan.Status = PlanStatusConsulting
	plan.ClarificationQuestions = []string{"What framework?"}
	if err := sm.TransitionTo(PlanStatusClarifying, plan); err != nil {
		t.Fatalf("expected Consulting -> Clarifying to succeed, got: %v", err)
	}
	if sm.State() != PlanStatusClarifying {
		t.Fatalf("expected Clarifying, got %s", sm.State())
	}
}

func TestTransitionTo_ReadyToExecuting(t *testing.T) {
	sm := NewPlanStateMachine("plan", PlanStatusReady)
	plan := validPlan()
	plan.Status = PlanStatusReady
	if err := sm.TransitionTo(PlanStatusExecuting, plan); err != nil {
		t.Fatalf("expected Ready -> Executing to succeed, got: %v", err)
	}
}

func TestTransitionTo_IllegalTransition(t *testing.T) {
	illegal := []struct {
		from PlanStatus
		to   PlanStatus
	}{
		{PlanStatusPending, PlanStatusReady},
		{PlanStatusPending, PlanStatusExecuting},
		{PlanStatusAnalyzing, PlanStatusDesigning},
		{PlanStatusReady, PlanStatusAnalyzing},
		{PlanStatusClarifying, PlanStatusExecuting},
		{PlanStatusClarifying, PlanStatusDesigning},
		{PlanStatusCompleted, PlanStatusPending},
		{PlanStatusFailed, PlanStatusPending},
	}
	for _, edge := range illegal {
		sm := NewPlanStateMachine("plan", edge.from)
		plan := validPlan()
		plan.Status = edge.from
		err := sm.TransitionTo(edge.to, plan)
		if err == nil {
			t.Errorf("expected %s -> %s to fail, got nil", edge.from, edge.to)
			continue
		}
		if !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("expected ErrIllegalTransition for %s -> %s, got: %v", edge.from, edge.to, err)
		}
	}
}

func TestTransitionTo_TerminalStatesRejectAll(t *testing.T) {
	terminals := []PlanStatus{PlanStatusCompleted, PlanStatusFailed}
	targets := []PlanStatus{
		PlanStatusPending, PlanStatusAnalyzing, PlanStatusConsulting,
		PlanStatusDesigning, PlanStatusGenerating, PlanStatusOrchestrating,
		PlanStatusReady, PlanStatusExecuting, PlanStatusCompleted, PlanStatusFailed,
	}
	for _, terminal := range terminals {
		for _, target := range targets {
			sm := NewPlanStateMachine("plan", terminal)
			plan := validPlan()
			err := sm.TransitionTo(target, plan)
			if err == nil {
				t.Errorf("expected %s -> %s to fail from terminal state", terminal, target)
			}
		}
	}
}

func TestGuard_ConsultingToClarifyingRequiresQuestions(t *testing.T) {
	sm := NewPlanStateMachine("plan", PlanStatusConsulting)
	plan := validPlan()
	plan.Status = PlanStatusConsulting
	plan.ClarificationQuestions = nil
	err := sm.TransitionTo(PlanStatusClarifying, plan)
	if err == nil {
		t.Fatal("expected guard rejection for empty clarification questions")
	}
	if !errors.Is(err, ErrTransitionGuardFailed) {
		t.Fatalf("expected ErrTransitionGuardFailed, got: %v", err)
	}
}

func TestGuard_ConsultingToDesigningRejectsWithQuestions(t *testing.T) {
	sm := NewPlanStateMachine("plan", PlanStatusConsulting)
	plan := validPlan()
	plan.Status = PlanStatusConsulting
	plan.ClarificationQuestions = []string{"What framework?"}
	err := sm.TransitionTo(PlanStatusDesigning, plan)
	if err == nil {
		t.Fatal("expected guard rejection for unresolved clarification questions")
	}
	if !errors.Is(err, ErrTransitionGuardFailed) {
		t.Fatalf("expected ErrTransitionGuardFailed, got: %v", err)
	}
}

func TestGuard_ReadyToExecutingValidation(t *testing.T) {
	sm := NewPlanStateMachine("plan", PlanStatusReady)
	plan := &DesignPlan{
		ID:     "plan-incomplete",
		Status: PlanStatusReady,
	}
	err := sm.TransitionTo(PlanStatusExecuting, plan)
	if err == nil {
		t.Fatal("expected guard rejection for invalid plan")
	}
	if !errors.Is(err, ErrTransitionGuardFailed) {
		t.Fatalf("expected ErrTransitionGuardFailed, got: %v", err)
	}
}

func TestConcurrentCAS_ExactlyOneWins(t *testing.T) {
	sm := NewPlanStateMachine("plan", PlanStatusReady)
	plan := validPlan()
	plan.Status = PlanStatusReady

	var wg sync.WaitGroup
	var wins atomic.Int32
	var losses atomic.Int32

	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := sm.TransitionTo(PlanStatusPending, plan)
			if err == nil {
				wins.Add(1)
			} else {
				losses.Add(1)
			}
		}()
	}
	wg.Wait()

	if wins.Load() != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", wins.Load())
	}
	if losses.Load() != 99 {
		t.Fatalf("expected 99 losses, got %d", losses.Load())
	}
}

func TestCallback_FiresAfterSuccess(t *testing.T) {
	sm := NewPlanStateMachine("plan-cb", PlanStatusPending)
	plan := validPlan()
	plan.Status = PlanStatusPending

	var received PlanStateChangeEvent
	sm.OnStateChange(func(event PlanStateChangeEvent) {
		received = event
	})

	if err := sm.TransitionTo(PlanStatusAnalyzing, plan); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if received.PlanID != "plan-cb" {
		t.Fatalf("expected plan-cb, got %q", received.PlanID)
	}
	if received.From != PlanStatusPending || received.To != PlanStatusAnalyzing {
		t.Fatalf("expected Pending->Analyzing, got %s->%s", received.From, received.To)
	}
}

func TestCallback_DoesNotFireAfterFailure(t *testing.T) {
	sm := NewPlanStateMachine("plan-cb", PlanStatusPending)
	plan := validPlan()

	fired := false
	sm.OnStateChange(func(_ PlanStateChangeEvent) {
		fired = true
	})

	_ = sm.TransitionTo(PlanStatusReady, plan) // illegal
	if fired {
		t.Fatal("callback should not fire after failed transition")
	}
}

func TestTransitionToFailed_FromAnyNonTerminal(t *testing.T) {
	nonTerminals := []PlanStatus{
		PlanStatusPending, PlanStatusAnalyzing, PlanStatusConsulting,
		PlanStatusClarifying, PlanStatusDesigning, PlanStatusGenerating,
		PlanStatusOrchestrating, PlanStatusReady, PlanStatusExecuting,
	}
	for _, status := range nonTerminals {
		sm := NewPlanStateMachine("plan", status)
		plan := validPlan()
		plan.Status = status
		if err := sm.TransitionToFailed(plan, errors.New("test failure")); err != nil {
			t.Errorf("expected %s -> Failed to succeed, got: %v", status, err)
		}
		if sm.State() != PlanStatusFailed {
			t.Errorf("expected Failed, got %s", sm.State())
		}
	}
}

func TestIsTerminal(t *testing.T) {
	sm := NewPlanStateMachine("plan", PlanStatusCompleted)
	if !sm.IsTerminal() {
		t.Fatal("Completed should be terminal")
	}
	sm2 := NewPlanStateMachine("plan", PlanStatusFailed)
	if !sm2.IsTerminal() {
		t.Fatal("Failed should be terminal")
	}
	sm3 := NewPlanStateMachine("plan", PlanStatusReady)
	if sm3.IsTerminal() {
		t.Fatal("Ready should not be terminal")
	}
}

func TestDesignPlan_SM_LazyInit(t *testing.T) {
	plan := &DesignPlan{ID: "lazy", Status: PlanStatusReady}
	sm := plan.SM()
	if sm == nil {
		t.Fatal("SM() should not return nil")
	}
	if sm.State() != PlanStatusReady {
		t.Fatalf("expected Ready, got %s", sm.State())
	}
	if plan.SM() != sm {
		t.Fatal("SM() should return the same instance")
	}
}

func TestDesignPlan_ReadyDirective_ReturnsNilWhenNotReady(t *testing.T) {
	plan := &DesignPlan{ID: "not-ready", Status: PlanStatusPending}
	if plan.ReadyDirective() != nil {
		t.Fatal("expected nil directive for non-Ready plan")
	}
}

func TestDesignPlan_ReadyDirective_ReturnsDirWhenReady(t *testing.T) {
	plan := &DesignPlan{ID: "ready-plan", Status: PlanStatusReady}
	dir := plan.ReadyDirective()
	if dir == nil {
		t.Fatal("expected directive for Ready plan")
	}
	if dir.Metadata["plan_id"] != "ready-plan" {
		t.Fatalf("expected plan_id ready-plan, got %v", dir.Metadata["plan_id"])
	}
}

func TestDesignPlan_IsClarifying(t *testing.T) {
	plan := &DesignPlan{ID: "clar", Status: PlanStatusClarifying}
	if !plan.IsClarifying() {
		t.Fatal("expected IsClarifying to be true")
	}
	plan2 := &DesignPlan{ID: "not-clar", Status: PlanStatusPending}
	if plan2.IsClarifying() {
		t.Fatal("expected IsClarifying to be false for Pending")
	}
}

func TestEpoch_IncrementsOnTransition(t *testing.T) {
	sm := NewPlanStateMachine("plan-epoch", PlanStatusPending)
	plan := validPlan()
	plan.Status = PlanStatusPending

	if sm.Epoch() != 0 {
		t.Fatalf("expected initial epoch 0, got %d", sm.Epoch())
	}
	if err := sm.TransitionTo(PlanStatusAnalyzing, plan); err != nil {
		t.Fatal(err)
	}
	if sm.Epoch() != 1 {
		t.Fatalf("expected epoch 1 after first transition, got %d", sm.Epoch())
	}
}

func TestEpoch_InCallbackEvent(t *testing.T) {
	sm := NewPlanStateMachine("plan-epoch-cb", PlanStatusPending)
	plan := validPlan()
	plan.Status = PlanStatusPending

	var eventEpoch uint64
	sm.OnStateChange(func(event PlanStateChangeEvent) {
		eventEpoch = event.Epoch
	})

	if err := sm.TransitionTo(PlanStatusAnalyzing, plan); err != nil {
		t.Fatal(err)
	}
	if eventEpoch != 1 {
		t.Fatalf("expected epoch 1 in event, got %d", eventEpoch)
	}
}

func TestNewPlanStateMachineWithEpoch(t *testing.T) {
	sm := NewPlanStateMachineWithEpoch("plan-restore", PlanStatusReady, 42)
	if sm.Epoch() != 42 {
		t.Fatalf("expected epoch 42, got %d", sm.Epoch())
	}
	if sm.State() != PlanStatusReady {
		t.Fatalf("expected Ready, got %s", sm.State())
	}
}

func TestReadyDirective_IncludesEpoch(t *testing.T) {
	plan := &DesignPlan{ID: "epoch-dir", Status: PlanStatusReady}
	plan.sm = NewPlanStateMachineWithEpoch(plan.ID, PlanStatusReady, 7)
	dir := plan.ReadyDirective()
	if dir == nil {
		t.Fatal("expected directive")
	}
	epoch, ok := dir.Metadata["epoch"].(uint64)
	if !ok {
		t.Fatalf("expected epoch in metadata, got %v", dir.Metadata["epoch"])
	}
	if epoch != 7 {
		t.Fatalf("expected epoch 7, got %d", epoch)
	}
}

func TestIsValidPlanTransition_Standalone(t *testing.T) {
	if !isValidPlanTransition(PlanStatusPending, PlanStatusAnalyzing) {
		t.Fatal("Pending -> Analyzing should be valid")
	}
	if isValidPlanTransition(PlanStatusPending, PlanStatusReady) {
		t.Fatal("Pending -> Ready should be invalid")
	}
	if !isValidPlanTransition(PlanStatusConsulting, PlanStatusClarifying) {
		t.Fatal("Consulting -> Clarifying should be valid")
	}
	if isValidPlanTransition(PlanStatusClarifying, PlanStatusExecuting) {
		t.Fatal("Clarifying -> Executing should be invalid")
	}
}
