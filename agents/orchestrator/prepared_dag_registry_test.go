package orchestrator

import (
	"testing"

	"github.com/adalundhe/sylk/agents/architect"
)

// minimalOrchestratorForRegistry constructs just enough Orchestrator
// surface for the registry methods (registerPreparedDAG /
// takePreparedDAG). Avoids the full New() path which spins up WAL,
// SQLite, and bus subscriptions.
func minimalOrchestratorForRegistry() *Orchestrator {
	return &Orchestrator{
		preparedDAGs: newPreparedDAGRegistry(),
	}
}

func TestPreparedDAGRegistry_TakeReturnsNilWhenAbsent(t *testing.T) {
	o := minimalOrchestratorForRegistry()
	if got := o.takePreparedDAG("plan-missing"); got != nil {
		t.Fatalf("expected nil for missing plan; got %+v", got)
	}
}

func TestPreparedDAGRegistry_RegisterThenTakeRoundtrips(t *testing.T) {
	o := minimalOrchestratorForRegistry()
	attempt := &planHandoffIngestAttempt{
		handoff:    &architect.PlanHandoff{PlanID: "plan-1", SessionID: "sess-1"},
		workflowID: "wf-1",
	}
	o.registerPreparedDAG("plan-1", attempt)

	taken := o.takePreparedDAG("plan-1")
	if taken == nil {
		t.Fatal("expected prepared entry; got nil")
	}
	if taken.planID != "plan-1" {
		t.Fatalf("planID: want plan-1, got %q", taken.planID)
	}
	if taken.workflowID != "wf-1" {
		t.Fatalf("workflowID: want wf-1, got %q", taken.workflowID)
	}

	// Take is destructive — second call must return nil.
	if got := o.takePreparedDAG("plan-1"); got != nil {
		t.Fatalf("expected nil on second take; got %+v", got)
	}
}

func TestPreparedDAGRegistry_ReRegisterSupersedes(t *testing.T) {
	o := minimalOrchestratorForRegistry()
	first := &planHandoffIngestAttempt{
		handoff:    &architect.PlanHandoff{PlanID: "plan-1"},
		workflowID: "wf-old",
	}
	second := &planHandoffIngestAttempt{
		handoff:    &architect.PlanHandoff{PlanID: "plan-1"},
		workflowID: "wf-new",
	}
	o.registerPreparedDAG("plan-1", first)
	o.registerPreparedDAG("plan-1", second)

	taken := o.takePreparedDAG("plan-1")
	if taken == nil {
		t.Fatal("expected entry after re-register")
	}
	if taken.workflowID != "wf-new" {
		t.Fatalf("re-register must supersede; got workflow=%q (expected wf-new)", taken.workflowID)
	}
}

func TestPreparedDAGRegistry_NilOrchestratorSafe(t *testing.T) {
	var o *Orchestrator
	// Methods must not panic on nil receiver — defensive against
	// callers that haven't yet wired the registry.
	o.registerPreparedDAG("plan-1", nil)
	if got := o.takePreparedDAG("plan-1"); got != nil {
		t.Fatalf("nil orchestrator: want nil; got %+v", got)
	}
}
