package orchestrator

import (
	"testing"
	"time"
)

// TestClassifyPlanState_NormalizesStateColumn pins the mapping from
// the orchestrator's internal DAG state vocabulary to the architect-
// facing status enum. Without this pin, a future state-column rename
// (e.g. "completed" → "succeeded") would silently break the
// architect's freshness audit's orchestrator hint.
func TestClassifyPlanState_NormalizesStateColumn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		row      *DAGExecutionRow
		expected string
	}{
		{"nil row → none", nil, "none"},
		{"empty row → queued", &DAGExecutionRow{}, "queued"},
		{"completed", &DAGExecutionRow{State: "completed"}, "completed"},
		{"succeeded → completed", &DAGExecutionRow{State: "succeeded"}, "completed"},
		{"failed", &DAGExecutionRow{State: "failed"}, "failed"},
		{"error → failed", &DAGExecutionRow{State: "error"}, "failed"},
		{"aborted", &DAGExecutionRow{State: "aborted"}, "aborted"},
		{"cancelled → aborted", &DAGExecutionRow{State: "cancelled"}, "aborted"},
		{"running (started, not completed)", &DAGExecutionRow{
			StartedAt: timePtr(time.Now()),
		}, "running"},
		{"running with whitespace + caps", &DAGExecutionRow{
			State: "  RUNNING ",
			StartedAt: timePtr(time.Now()),
		}, "running"},
		{"completed by timestamp without state label", &DAGExecutionRow{
			CompletedAt: timePtr(time.Now()),
		}, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPlanState(tc.row)
			if got != tc.expected {
				t.Fatalf("classifyPlanState = %q, want %q", got, tc.expected)
			}
		})
	}
}

// TestPlanExecutionState_NoStoreReturnsNoneStatus pins the
// graceful-degrade contract: an orchestrator without a store still
// returns a non-nil PlanExecutionState (status="none") so the
// architect's freshness audit can render an orchestrator hint of
// "no record" rather than swallowing the response.
func TestPlanExecutionState_NoStoreReturnsNoneStatus(t *testing.T) {
	o := &Orchestrator{}
	state, err := o.planExecutionState(t.Context(), "plan-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.Status != "none" {
		t.Fatalf("expected status=none for store-less orchestrator, got %q", state.Status)
	}
	if state.PlanID != "plan-x" {
		t.Fatalf("expected plan_id echoed back, got %q", state.PlanID)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
