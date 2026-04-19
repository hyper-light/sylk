// End-to-end matrix tests for the plan acceptance flow.
//
// These tests don't spin up the full bus stack — that would require
// guardian + orchestrator + UI peers wired through real ChannelBus
// fixtures, which the existing helpers don't provide. Instead they
// exercise the integration BOUNDARIES: audit + drift + reconsult
// candidate selection feeding state-aware routing decisions, with
// hand-built orchestrator state and DesignPlan fixtures so each
// scenario from the matrix (drift, no-drift, cancel-in-flight,
// modify-with-drift, replan-after-stale) is pinned end-to-end at
// the level where the rubber meets the road.
package architect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/planapproval"
)

// TestMatrix_NoDriftFreshPlan_ProceedsAndDispatchesSilent pins the
// canonical happy path: a fresh plan whose AffectedFiles match the
// workspace, with an orchestrator that says "no record" (queued
// status), produces a Fresh=true audit, recommends Proceed, picks no
// reconsult candidates, and the routing classifier returns silent
// dispatch. This is the scenario the user clicks Approve on with
// no surprises.
func TestMatrix_NoDriftFreshPlan_ProceedsAndDispatchesSilent(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 tmpDir,
	})
	plan := &DesignPlan{
		ID:        "plan-fresh",
		SessionID: "ses-1",
		Status:    PlanStatusReady,
		Query:     "add health check endpoint",
		UpdatedAt: time.Now().Add(-1 * time.Minute),
		Tasks: []*AtomicTask{{
			ID: "t1",
			AffectedFiles: []TaskFileTarget{
				{Path: "main.go", Operation: "modify"},
			},
		}},
		Consultations: map[string]*ConsultationEvidence{
			"librarian": {Target: "librarian", Success: true, ReceivedAt: time.Now().Add(-1 * time.Hour)},
		},
	}

	audit := a.auditPlanFreshness(plan)
	if !audit.Fresh {
		t.Fatalf("fresh plan should be Fresh=true, got signals=%+v", audit.Signals)
	}
	if audit.Recommendation != RecommendProceed {
		t.Fatalf("expected Proceed, got %q", audit.Recommendation)
	}
	if got := pickReconsultationCandidates(audit, plan, false); len(got) != 0 {
		t.Fatalf("fresh audit should pick no reconsult candidates, got %+v", got)
	}
	decision := classifyApprovalRouting(&orchestratorPlanState{Status: "queued"})
	if decision.Action != approvalActionDispatchSilent {
		t.Fatalf("queued orchestrator state should produce silent dispatch, got %q", decision.Action)
	}
}

// TestMatrix_FileDriftDetected_ReviseAndReconsultLibrarian pins the
// "you wrote a plan to modify auth.go but auth.go is gone" scenario:
// the audit produces a warning-severity codebase signal, the
// recommendation escalates to Revise, and the reconsult picker
// targets the librarian (the agent whose codebase model is now
// stale).
func TestMatrix_FileDriftDetected_ReviseAndReconsultLibrarian(t *testing.T) {
	tmpDir := t.TempDir()
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 tmpDir,
	})
	plan := &DesignPlan{
		ID:        "plan-drift",
		SessionID: "ses-1",
		Status:    PlanStatusReady,
		Query:     "refactor auth flow",
		UpdatedAt: time.Now().Add(-2 * time.Minute),
		Tasks: []*AtomicTask{{
			ID: "t1",
			AffectedFiles: []TaskFileTarget{
				{Path: "auth.go", Operation: "modify"},
			},
		}},
		Consultations: map[string]*ConsultationEvidence{
			"librarian": {Target: "librarian", Success: true, ReceivedAt: time.Now().Add(-1 * time.Hour)},
		},
	}

	audit := a.auditPlanFreshness(plan)
	if audit.Fresh {
		t.Fatalf("audit should NOT be fresh when modify target is missing")
	}
	if audit.Recommendation != RecommendRevise {
		t.Fatalf("single warning should recommend Revise, got %q", audit.Recommendation)
	}
	hasCodebaseWarning := false
	for _, signal := range audit.Signals {
		if signal.Kind == "codebase" && signal.Severity == "warning" {
			hasCodebaseWarning = true
			break
		}
	}
	if !hasCodebaseWarning {
		t.Fatalf("expected codebase warning signal, got %+v", audit.Signals)
	}
	candidates := pickReconsultationCandidates(audit, plan, false)
	if len(candidates) != 1 || candidates[0].Target != "librarian" {
		t.Fatalf("expected librarian reconsult candidate, got %+v", candidates)
	}
}

// TestMatrix_CancelInFlight_NotifyOnlyNoDispatch pins the
// "user clicked Approve while orchestrator was already running this
// plan" scenario: the routing classifier MUST return notify_only so
// dispatch is skipped (preventing a parallel duplicate run). The
// notice text must include the progress count so the user knows
// what's actually happening.
func TestMatrix_CancelInFlight_NotifyOnlyNoDispatch(t *testing.T) {
	state := &orchestratorPlanState{
		Status:         "running",
		NodesSucceeded: 3,
		NodesTotal:     8,
	}
	decision := classifyApprovalRouting(state)
	if decision.Action != approvalActionNotifyOnly {
		t.Fatalf("running orchestrator state must produce notify_only, got %q", decision.Action)
	}
	if !strings.Contains(decision.Notice, "3/8") {
		t.Fatalf("notice must include progress count, got %q", decision.Notice)
	}
	if !strings.Contains(decision.Notice, "cancel") {
		t.Fatalf("notice should mention cancel as user's recourse, got %q", decision.Notice)
	}
}

// TestMatrix_ModifyWithDrift_VerdictDecodeRoundTrips pins that the
// dialog's Modify verdict decode round-trips end-to-end through
// decodePlanApprovalVerdict. The Modify branch is what the architect
// uses to ask "what would you like changed?" — if the verdict decode
// loses the verdict identity, the architect would silently fall
// through to the unknown-verdict branch and confuse the user.
func TestMatrix_ModifyWithDrift_VerdictDecodeRoundTrips(t *testing.T) {
	for _, encoded := range []any{
		&planApprovalGateResult{Verdict: planapproval.VerdictModify, Reason: "rename t1 to t2"},
		planApprovalGateResult{Verdict: planapproval.VerdictModify, Reason: "rename t1 to t2"},
		map[string]any{"verdict": "modify", "reason": "rename t1 to t2"},
	} {
		verdict, reason, err := decodePlanApprovalVerdict(encoded)
		if err != nil {
			t.Fatalf("decode failed for %T: %v", encoded, err)
		}
		if verdict != planapproval.VerdictModify {
			t.Fatalf("decode produced %q, want modify", verdict)
		}
		if !strings.Contains(reason, "rename") {
			t.Fatalf("reason lost in decode, got %q", reason)
		}
	}
}

// TestMatrix_ReplanAfterStale_FansOutReconsult pins the severe-drift
// scenario: a 3-hour-old plan with multiple originally-consulted
// agents triggers a Replan recommendation AND a reconsult fan-out
// touching every originally-consulted agent. Without the fan-out
// the user would see "audit recommends replan" but no fresh evidence
// to base the replan on.
func TestMatrix_ReplanAfterStale_FansOutReconsult(t *testing.T) {
	tmpDir := t.TempDir()
	// Seed two existing files so the "create"-op tasks below produce
	// advisory-severity signals (existing-create-target). Combined with
	// the missing-modify warning that's a warning + 2 advisories →
	// Replan recommendation per the audit's escalation matrix.
	for _, name := range []string{"existing_a.go", "existing_b.go"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("package x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 tmpDir,
	})
	old := time.Now().Add(-3 * time.Hour)
	plan := &DesignPlan{
		ID:        "plan-stale",
		SessionID: "ses-1",
		Status:    PlanStatusReady,
		Query:     "rework caching layer",
		UpdatedAt: old,
		Tasks: []*AtomicTask{{
			ID: "t1",
			AffectedFiles: []TaskFileTarget{
				{Path: "missing-cache.go", Operation: "modify"},
				{Path: "existing_a.go", Operation: "create"},
				{Path: "existing_b.go", Operation: "create"},
			},
		}},
		Consultations: map[string]*ConsultationEvidence{
			"librarian":   {Target: "librarian", Success: true, ReceivedAt: old},
			"academic":    {Target: "academic", Success: true, ReceivedAt: old},
			"archivalist": {Target: "archivalist", Success: true, ReceivedAt: old},
		},
	}

	audit := a.auditPlanFreshness(plan)
	if audit.Recommendation != RecommendReplan {
		t.Fatalf("warning + multiple advisories should escalate to Replan, got %q (signals=%+v)",
			audit.Recommendation, audit.Signals)
	}
	candidates := pickReconsultationCandidates(audit, plan, false)
	if len(candidates) != 3 {
		t.Fatalf("Replan recommendation should fan out to all 3 originally-consulted targets, got %d (%+v)",
			len(candidates), candidates)
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		seen[c.Target] = true
	}
	for _, want := range []string{"librarian", "academic", "archivalist"} {
		if !seen[want] {
			t.Fatalf("Replan fan-out missing %q (got %v)", want, seen)
		}
	}
}

// TestMatrix_ResumeAfterStalled_DispatchWithStallNotice pins the
// "plan was stalled, user approved, architect resumes via the
// dispatch_with_notice action" scenario. The notice must explain
// what's happening (re-dispatch) so the user understands the
// approve click triggered a restart, not a parallel run.
func TestMatrix_ResumeAfterStalled_DispatchWithStallNotice(t *testing.T) {
	state := &orchestratorPlanState{
		Status:            "stalled",
		StalledForSeconds: 720,
	}
	decision := classifyApprovalRouting(state)
	if decision.Action != approvalActionDispatchWithNotice {
		t.Fatalf("stalled state must produce dispatch_with_notice, got %q", decision.Action)
	}
	if !strings.Contains(decision.Notice, "stalled") {
		t.Fatalf("notice should explain why we're redispatching, got %q", decision.Notice)
	}
	if !strings.Contains(decision.Notice, "720") {
		t.Fatalf("notice should embed elapsed seconds, got %q", decision.Notice)
	}
}

// TestMatrix_RetryAfterFailure_DispatchWithFailureNotice pins the
// "plan failed previously, user approved a retry" scenario. The
// notice acknowledges the prior failure so the user has explicit
// confirmation the architect noticed and is choosing to retry.
func TestMatrix_RetryAfterFailure_DispatchWithFailureNotice(t *testing.T) {
	state := &orchestratorPlanState{
		Status: "failed",
		Error:  "node deploy_pipeline exit 137",
	}
	decision := classifyApprovalRouting(state)
	if decision.Action != approvalActionDispatchWithNotice {
		t.Fatalf("failed state must produce dispatch_with_notice, got %q", decision.Action)
	}
	if !strings.Contains(decision.Notice, "failure") {
		t.Fatalf("notice should acknowledge the prior failure, got %q", decision.Notice)
	}
}
