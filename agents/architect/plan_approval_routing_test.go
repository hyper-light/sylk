package architect

import (
	"strings"
	"testing"
)

// TestClassifyApprovalRouting_StateMatrix pins the full action matrix
// for orchestrator state → routing decision. This is the central
// invariant for state-aware verdict routing: which orchestrator
// statuses block dispatch (notify-only) vs proceed with context
// (dispatch_with_notice) vs proceed silently (dispatch_silent).
//
// A regression that flipped any of these mappings would either
// (1) silently double-dispatch on top of a running plan, or
// (2) refuse to retry a failed plan the user explicitly approved.
// Both are user-trust-violating bugs that the dialog flow exists
// specifically to prevent.
func TestClassifyApprovalRouting_StateMatrix(t *testing.T) {
	for _, tc := range []struct {
		name        string
		state       *orchestratorPlanState
		wantAction  approvalRoutingAction
		wantNoticeContains string
	}{
		{"nil state → silent dispatch (architect without orchestrator)",
			nil, approvalActionDispatchSilent, ""},
		{"none → silent dispatch (orchestrator never saw this plan)",
			&orchestratorPlanState{Status: "none"}, approvalActionDispatchSilent, ""},
		{"queued → silent dispatch (already accepted, not yet started)",
			&orchestratorPlanState{Status: "queued"}, approvalActionDispatchSilent, ""},
		{"aborted → silent dispatch (fresh attempt after cancellation)",
			&orchestratorPlanState{Status: "aborted"}, approvalActionDispatchSilent, ""},
		{"running → notify only (don't double-dispatch)",
			&orchestratorPlanState{Status: "running", NodesSucceeded: 2, NodesTotal: 5},
			approvalActionNotifyOnly, "already running"},
		{"completed → notify only (avoid duplicate run)",
			&orchestratorPlanState{Status: "completed"},
			approvalActionNotifyOnly, "already completed"},
		{"stalled → dispatch with notice (restart context)",
			&orchestratorPlanState{Status: "stalled", StalledForSeconds: 600},
			approvalActionDispatchWithNotice, "stalled"},
		{"failed → dispatch with notice (retry context)",
			&orchestratorPlanState{Status: "failed", Error: "node x oom"},
			approvalActionDispatchWithNotice, "prior failure"},
		{"unknown future status → silent dispatch (failsafe forward-compat)",
			&orchestratorPlanState{Status: "fictional_new_state"},
			approvalActionDispatchSilent, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := classifyApprovalRouting(tc.state)
			if decision.Action != tc.wantAction {
				t.Fatalf("action = %q, want %q (notice=%q)", decision.Action, tc.wantAction, decision.Notice)
			}
			if tc.wantNoticeContains == "" {
				if decision.Notice != "" {
					t.Fatalf("expected empty notice, got %q", decision.Notice)
				}
				return
			}
			if !strings.Contains(decision.Notice, tc.wantNoticeContains) {
				t.Fatalf("notice missing %q: got %q", tc.wantNoticeContains, decision.Notice)
			}
		})
	}
}

// TestClassifyApprovalRouting_RunningNoticeIncludesProgress pins
// that the running-state notice includes the node-count progress
// (X/Y nodes). Without progress context the user can't judge whether
// to wait or to cancel-and-restart.
func TestClassifyApprovalRouting_RunningNoticeIncludesProgress(t *testing.T) {
	decision := classifyApprovalRouting(&orchestratorPlanState{
		Status:         "running",
		NodesSucceeded: 7,
		NodesTotal:     10,
	})
	if decision.Action != approvalActionNotifyOnly {
		t.Fatalf("expected notify_only, got %q", decision.Action)
	}
	if !strings.Contains(decision.Notice, "7/10") {
		t.Fatalf("notice should embed progress count, got %q", decision.Notice)
	}
}

// TestClassifyApprovalRouting_StalledNoticeIncludesElapsedSeconds
// pins that the stalled-state notice quotes the stalled-for duration
// so the user sees specifically how long the orchestrator stopped
// making progress before the architect decided to redispatch.
func TestClassifyApprovalRouting_StalledNoticeIncludesElapsedSeconds(t *testing.T) {
	decision := classifyApprovalRouting(&orchestratorPlanState{
		Status:            "stalled",
		StalledForSeconds: 1234,
	})
	if !strings.Contains(decision.Notice, "1234") {
		t.Fatalf("stalled notice should embed elapsed seconds, got %q", decision.Notice)
	}
}
