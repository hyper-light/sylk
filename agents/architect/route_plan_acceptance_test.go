package architect

import (
	"testing"
	"time"
)

// TestResolveReadyPlanForAcceptance_ExecutingFlowsThroughForResume pins
// the resume-after-restart contract: when route_plan_acceptance is
// invoked against a plan in PlanStatusExecuting (typically because the
// architect was demoted/restarted while the plan was in flight, leaving
// the persisted status at Executing without a live PendingWork
// continuation), the resolver returns the plan for normal acceptance
// routing. actOnAccept → dispatchPlanExecution then handles
// idempotency: if PendingWork still exists it returns "already in
// flight"; if it doesn't it re-attaches with a fresh continuation.
// Returning a status report here would cause the architect to narrate
// "your plan is already running" while the orchestrator never gets the
// dispatch — silent stalled state masquerading as success. The flow-
// through path lets dispatchPlanExecution be the single source of truth
// for "is this plan actually running."
func TestResolveReadyPlanForAcceptance_ExecutingFlowsThroughForResume(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 t.TempDir(),
	})

	plan := &DesignPlan{
		ID:        "plan-executing",
		SessionID: "ses-resume",
		Query:     "implement hello cli package",
		Status:    PlanStatusExecuting,
		CreatedAt: time.Now().Add(-2 * time.Minute).UTC(),
		UpdatedAt: time.Now().Add(-1 * time.Minute).UTC(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusExecuting)
	if err := a.planStore.Upsert(plan); err != nil {
		t.Fatalf("upsert plan: %v", err)
	}

	resolved, statusReport, err := a.resolveReadyPlanForAcceptance(plan.ID, plan.SessionID)
	if err != nil {
		t.Fatalf("resolveReadyPlanForAcceptance returned error for executing plan: %v", err)
	}
	if statusReport != nil {
		t.Fatalf("executing plan must NOT short-circuit with a status report — it must flow through to dispatchPlanExecution; got report=%+v", statusReport)
	}
	if resolved == nil || resolved.ID != plan.ID {
		t.Fatalf("expected executing plan returned for downstream re-dispatch, got %+v", resolved)
	}
}

// TestResolveReadyPlanForAcceptance_OrchestratingFlowsThroughForResume
// pins the same resume contract for plans in PlanStatusOrchestrating
// (the transitional state between Ready and Executing once dispatch has
// queued the plan to the orchestrator). Like Executing, this status
// indicates "the plan was previously approved" and re-dispatch through
// actOnAccept should be idempotent.
func TestResolveReadyPlanForAcceptance_OrchestratingFlowsThroughForResume(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 t.TempDir(),
	})

	plan := &DesignPlan{
		ID:        "plan-orchestrating",
		SessionID: "ses-resume",
		Query:     "implement hello cli package",
		Status:    PlanStatusOrchestrating,
		CreatedAt: time.Now().Add(-2 * time.Minute).UTC(),
		UpdatedAt: time.Now().Add(-1 * time.Minute).UTC(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusOrchestrating)
	if err := a.planStore.Upsert(plan); err != nil {
		t.Fatalf("upsert plan: %v", err)
	}

	resolved, statusReport, err := a.resolveReadyPlanForAcceptance(plan.ID, plan.SessionID)
	if err != nil {
		t.Fatalf("resolveReadyPlanForAcceptance returned error for orchestrating plan: %v", err)
	}
	if statusReport != nil {
		t.Fatalf("orchestrating plan must NOT short-circuit; got report=%+v", statusReport)
	}
	if resolved == nil || resolved.ID != plan.ID {
		t.Fatalf("expected orchestrating plan returned for downstream re-dispatch, got %+v", resolved)
	}
}

// TestResolveReadyPlanForAcceptance_FailedReturnsAdvisoryNotError pins
// the same advisory-shape contract for previously-failed plans. The LLM
// should be able to suggest a retry or a fresh plan, not error out.
func TestResolveReadyPlanForAcceptance_FailedReturnsAdvisoryNotError(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 t.TempDir(),
	})

	plan := &DesignPlan{
		ID:        "plan-failed",
		SessionID: "ses-resume",
		Query:     "broken plan",
		Status:    PlanStatusFailed,
		CreatedAt: time.Now().Add(-5 * time.Minute).UTC(),
		UpdatedAt: time.Now().Add(-3 * time.Minute).UTC(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusFailed)
	if err := a.planStore.Upsert(plan); err != nil {
		t.Fatalf("upsert plan: %v", err)
	}

	resolved, statusReport, err := a.resolveReadyPlanForAcceptance(plan.ID, plan.SessionID)
	if err != nil {
		t.Fatalf("resolveReadyPlanForAcceptance returned hard error for failed plan: %v", err)
	}
	if resolved != nil {
		t.Fatal("failed plan must not be returned as resolved-for-routing")
	}
	if statusReport == nil || statusReport.Status != "failed" {
		t.Fatalf("expected failed-status advisory, got %+v", statusReport)
	}
	if statusReport.Resumable {
		t.Fatal("failed plan should not be marked resumable — LLM should propose a fresh plan")
	}
}

// TestResolveReadyPlanForAcceptance_ReadyStillReturnsPlan pins backwards
// compatibility: the canonical Ready-plan path still returns the plan
// for normal acceptance routing without populating the advisory.
func TestResolveReadyPlanForAcceptance_ReadyStillReturnsPlan(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 t.TempDir(),
	})

	plan := &DesignPlan{
		ID:        "plan-ready",
		SessionID: "ses-1",
		Query:     "ready and waiting",
		Status:    PlanStatusReady,
		CreatedAt: time.Now().Add(-1 * time.Minute).UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusReady)
	if err := a.planStore.Upsert(plan); err != nil {
		t.Fatalf("upsert plan: %v", err)
	}

	resolved, statusReport, err := a.resolveReadyPlanForAcceptance(plan.ID, plan.SessionID)
	if err != nil {
		t.Fatalf("resolveReadyPlanForAcceptance returned error: %v", err)
	}
	if statusReport != nil {
		t.Fatalf("ready plan must not produce a status report; got %+v", statusReport)
	}
	if resolved == nil || resolved.ID != plan.ID {
		t.Fatalf("expected ready plan returned, got %+v", resolved)
	}
}

// TestResolveReadyPlanForAcceptance_NotFoundStillErrors pins that
// genuine "plan doesn't exist" failures remain real errors. The
// advisory shape is for status-mismatch only; ID resolution failures
// are real operator-actionable bugs and should surface as such.
func TestResolveReadyPlanForAcceptance_NotFoundStillErrors(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 t.TempDir(),
	})

	resolved, statusReport, err := a.resolveReadyPlanForAcceptance("nonexistent-plan", "ses-1")
	if err == nil {
		t.Fatal("expected error for nonexistent plan ID")
	}
	if resolved != nil || statusReport != nil {
		t.Fatalf("not-found path should return nil resolved and nil report, got plan=%v report=%v", resolved, statusReport)
	}
}
