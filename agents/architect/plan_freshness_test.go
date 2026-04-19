package architect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/planapproval"
)

// TestAuditPlanFreshness_FreshPlanReportsNoDrift pins the canonical
// happy path: a plan whose AffectedFiles all match the workspace and
// whose UpdatedAt is recent should produce Fresh=true with no drift
// signals and a positive Summary. Without this, a stale-plan flow
// could mask itself as drift-free and the dialog would lose the
// reassurance that the audit actually ran.
func TestAuditPlanFreshness_FreshPlanReportsNoDrift(t *testing.T) {
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
		UpdatedAt: time.Now().Add(-1 * time.Minute),
		Tasks: []*AtomicTask{{
			ID: "t1",
			AffectedFiles: []TaskFileTarget{
				{Path: "main.go", Operation: "modify"},
			},
		}},
	}

	audit := a.auditPlanFreshness(plan)
	if audit == nil {
		t.Fatal("audit must not be nil")
	}
	if !audit.Fresh {
		t.Fatalf("fresh plan should be Fresh=true, got signals=%+v", audit.Signals)
	}
	if len(audit.Signals) != 0 {
		t.Fatalf("fresh plan should have no signals, got %d", len(audit.Signals))
	}
	if audit.Recommendation != RecommendProceed {
		t.Fatalf("fresh plan should recommend proceed, got %q", audit.Recommendation)
	}
	if !strings.Contains(audit.Summary, "no drift detected") {
		t.Fatalf("fresh plan summary should explicitly say no drift, got %q", audit.Summary)
	}
}

// TestAuditPlanFreshness_MissingModifyTargetIsWarning pins the
// canonical drift case: a plan that expected to modify a file which
// no longer exists in the workspace produces a warning-level signal,
// not a silent miss. This is the scenario where the user was about
// to dispatch a plan against a refactored codebase — the audit must
// catch it.
func TestAuditPlanFreshness_MissingModifyTargetIsWarning(t *testing.T) {
	tmpDir := t.TempDir() // file deliberately not created

	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 tmpDir,
	})

	plan := &DesignPlan{
		ID:        "plan-drift",
		SessionID: "ses-1",
		Status:    PlanStatusReady,
		UpdatedAt: time.Now().Add(-1 * time.Minute),
		Tasks: []*AtomicTask{{
			ID: "t1",
			AffectedFiles: []TaskFileTarget{
				{Path: "deleted.go", Operation: "modify"},
			},
		}},
	}

	audit := a.auditPlanFreshness(plan)
	if audit.Fresh {
		t.Fatal("plan with missing modify target must not be Fresh")
	}
	if len(audit.Signals) != 1 {
		t.Fatalf("expected 1 signal, got %d: %+v", len(audit.Signals), audit.Signals)
	}
	if audit.Signals[0].Severity != "warning" {
		t.Fatalf("missing modify target should be a warning, got %q", audit.Signals[0].Severity)
	}
	if audit.Signals[0].SourcePath != "deleted.go" {
		t.Fatalf("signal should carry source path, got %q", audit.Signals[0].SourcePath)
	}
	if audit.Recommendation != RecommendRevise {
		t.Fatalf("single warning should recommend revise, got %q", audit.Recommendation)
	}
}

// TestAuditPlanFreshness_AlreadyExistingCreateTargetIsAdvisory pins
// the create-collision case: if a "create" target file already exists,
// the user should be advised before dispatch so they don't clobber
// existing work. Advisory severity (not warning) since the existing
// file might be intentional.
func TestAuditPlanFreshness_AlreadyExistingCreateTargetIsAdvisory(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "new.go"), []byte("// existing"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 tmpDir,
	})

	plan := &DesignPlan{
		ID:        "plan-create-collision",
		SessionID: "ses-1",
		Status:    PlanStatusReady,
		UpdatedAt: time.Now().Add(-1 * time.Minute),
		Tasks: []*AtomicTask{{
			ID: "t1",
			AffectedFiles: []TaskFileTarget{
				{Path: "new.go", Operation: "create"},
			},
		}},
	}

	audit := a.auditPlanFreshness(plan)
	if audit.Fresh {
		t.Fatal("create-collision should not be Fresh")
	}
	if len(audit.Signals) != 1 || audit.Signals[0].Severity != "advisory" {
		t.Fatalf("expected one advisory signal, got %+v", audit.Signals)
	}
	if audit.Recommendation != RecommendRevise {
		t.Fatalf("advisory-only audit should recommend revise, got %q", audit.Recommendation)
	}
}

// TestAuditPlanFreshness_OldPlanGetsAgeSignal pins the time-decay
// signal: a plan that's been sitting for >30 minutes generates an
// age-based drift signal even when no file drift exists. Even fresh-
// looking files don't guarantee the user's intent or surrounding
// context hasn't shifted; surfacing age explicitly is the user-first
// move (vs silently re-presenting an old plan).
func TestAuditPlanFreshness_OldPlanGetsAgeSignal(t *testing.T) {
	tmpDir := t.TempDir()

	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 tmpDir,
	})

	plan := &DesignPlan{
		ID:        "plan-old",
		SessionID: "ses-1",
		Status:    PlanStatusReady,
		UpdatedAt: time.Now().Add(-3 * time.Hour),
		Tasks:     []*AtomicTask{{ID: "t1"}},
	}

	audit := a.auditPlanFreshness(plan)
	if audit.Fresh {
		t.Fatal("plan older than threshold must not be Fresh")
	}
	if len(audit.Signals) != 1 {
		t.Fatalf("expected 1 age signal, got %+v", audit.Signals)
	}
	sig := audit.Signals[0]
	if sig.Kind != "consultation_age" {
		t.Fatalf("expected consultation_age kind, got %q", sig.Kind)
	}
	if sig.Severity != "warning" {
		t.Fatalf("3-hour-old plan should be warning severity, got %q", sig.Severity)
	}
}

// TestOrchestratorStateDriftSignal_PerStatusBranches pins the
// escalation matrix from orchestrator state into a drift signal: each
// status string the orchestrator can return must produce a signal of
// the right kind/severity (or nil for benign states like "completed
// before this attempt was queued"). Pinning this prevents a future
// orchestrator status enum addition from silently dropping context
// from the dialog.
func TestOrchestratorStateDriftSignal_PerStatusBranches(t *testing.T) {
	plan := &DesignPlan{ID: "plan-state", Status: PlanStatusReady}
	for _, tc := range []struct {
		name         string
		state        *orchestratorPlanState
		wantNil      bool
		wantKind     string
		wantSeverity string
	}{
		{"nil state → no signal", nil, true, "", ""},
		{"none → no signal",
			&orchestratorPlanState{Status: "none"}, true, "", ""},
		{"queued → no signal",
			&orchestratorPlanState{Status: "queued"}, true, "", ""},
		{"running → advisory orchestrator_state",
			&orchestratorPlanState{
				Status:         "running",
				NodesSucceeded: 2,
				NodesTotal:     5,
			},
			false, "orchestrator_state", "advisory"},
		{"stalled → warning orchestrator_state",
			&orchestratorPlanState{
				Status:            "stalled",
				StalledForSeconds: 600,
			},
			false, "orchestrator_state", "warning"},
		{"completed → warning orchestrator_state",
			&orchestratorPlanState{Status: "completed"},
			false, "orchestrator_state", "warning"},
		{"failed → warning orchestrator_state",
			&orchestratorPlanState{
				Status: "failed",
				Error:  "node X timed out",
			},
			false, "orchestrator_state", "warning"},
		{"aborted → no signal (architect handles via verdict routing)",
			&orchestratorPlanState{Status: "aborted"}, true, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signal := orchestratorStateDriftSignal(tc.state, plan)
			if tc.wantNil {
				if signal != nil {
					t.Fatalf("expected nil signal, got %+v", signal)
				}
				return
			}
			if signal == nil {
				t.Fatalf("expected signal, got nil")
			}
			if signal.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", signal.Kind, tc.wantKind)
			}
			if signal.Severity != tc.wantSeverity {
				t.Fatalf("severity = %q, want %q", signal.Severity, tc.wantSeverity)
			}
			if strings.TrimSpace(signal.Description) == "" {
				t.Fatalf("signal description must not be empty")
			}
		})
	}
}

// TestOrchestratorStateDriftSignal_FailedIncludesErrorContext pins
// that the failed branch incorporates the orchestrator's last-error
// string into the user-facing description. Without this evidence the
// user can't tell whether re-approving will hit the same failure.
func TestOrchestratorStateDriftSignal_FailedIncludesErrorContext(t *testing.T) {
	signal := orchestratorStateDriftSignal(&orchestratorPlanState{
		Status: "failed",
		Error:  "node deploy_pipeline exit 137 (oom)",
	}, &DesignPlan{ID: "plan-failed"})
	if signal == nil {
		t.Fatal("expected signal for failed state")
	}
	if !strings.Contains(signal.Description, "deploy_pipeline") {
		t.Fatalf("expected description to surface error context, got %q", signal.Description)
	}
}

// TestPickFreshnessRecommendation_EscalatesToReplan pins the
// recommendation matrix: warnings + multiple advisories indicates
// the plan is sufficiently stale that re-planning is more honest
// than patching it. The dialog defaults selection to Reject in
// that case so the user sees the audit's strongest signal first.
func TestPickFreshnessRecommendation_EscalatesToReplan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		signals []planapproval.DriftSignal
		want    FreshnessRecommendation
	}{
		{"empty → proceed", nil, RecommendProceed},
		{"info only → proceed",
			[]planapproval.DriftSignal{{Severity: "info"}},
			RecommendProceed,
		},
		{"single advisory → revise",
			[]planapproval.DriftSignal{{Severity: "advisory"}},
			RecommendRevise,
		},
		{"single warning → revise",
			[]planapproval.DriftSignal{{Severity: "warning"}},
			RecommendRevise,
		},
		{"warning + 2 advisories → replan",
			[]planapproval.DriftSignal{
				{Severity: "warning"},
				{Severity: "advisory"},
				{Severity: "advisory"},
			},
			RecommendReplan,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pickFreshnessRecommendation(tc.signals)
			if got != tc.want {
				t.Fatalf("recommendation = %q, want %q", got, tc.want)
			}
		})
	}
}
