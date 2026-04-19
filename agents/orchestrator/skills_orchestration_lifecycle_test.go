package orchestrator

import (
	"testing"
)

// TestCancelOrchestrationForPlan_StorelessReturnsNoActiveExecution
// pins the graceful-degrade contract: an orchestrator with no store
// (test fixture, partial init) returns success+no_active_execution
// rather than erroring. The architect's cancel-on-Approve path
// treats this as an idempotent "nothing to cancel."
func TestCancelOrchestrationForPlan_StorelessReturnsNoActiveExecution(t *testing.T) {
	o := &Orchestrator{}
	result, err := o.cancelOrchestrationForPlan("plan-1", "user requested")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if !result.NoActiveExecution {
		t.Fatalf("expected NoActiveExecution=true, got %+v", result)
	}
	if result.Cancelled {
		t.Fatalf("expected Cancelled=false when no active execution, got %+v", result)
	}
	if result.PlanID != "plan-1" {
		t.Fatalf("expected plan_id echoed back, got %q", result.PlanID)
	}
	if result.Reason != "user requested" {
		t.Fatalf("expected reason preserved, got %q", result.Reason)
	}
}

// TestResumeOrchestrationForPlan_StorelessReturnsNoPlan pins the
// equivalent contract for resume: no-store orchestrator returns
// action=no_plan so the architect's fallback path triggers a fresh
// dispatch rather than thinking the plan is mid-execution.
func TestResumeOrchestrationForPlan_StorelessReturnsNoPlan(t *testing.T) {
	o := &Orchestrator{}
	result, err := o.resumeOrchestrationForPlan(t.Context(), "plan-1", "auto", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.Action != "no_plan" {
		t.Fatalf("expected action=no_plan, got %q (%+v)", result.Action, result)
	}
}
