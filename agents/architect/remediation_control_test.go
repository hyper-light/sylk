package architect

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	shared "github.com/adalundhe/sylk/agents/shared"
)

func TestHandleRemediationRequestBuildsFixWorkflow(t *testing.T) {
	a := newTestArchitect(t, Config{AllowPlanningWithoutConsultation: true})

	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess",
		Status:    PlanStatusReady,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := a.planStore.Upsert(plan); err != nil {
		t.Fatalf("upsert plan: %v", err)
	}

	req := &shared.RemediationRequest{
		CaseID:    "case-1",
		SessionID: "sess",
		PlanID:    "plan-1",
		Summary:   "Fix failing integration and tighten validation.",
		Findings: []shared.ValidationFinding{
			{
				ID:             "f1",
				Severity:       shared.ValidationSeverityHigh,
				File:           "pkg/auth/service.go",
				Line:           42,
				Summary:        "Shared auth flow is failing under integration load.",
				Recommendation: "Add corrective validation and repair the auth flow.",
			},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal remediation request: %v", err)
	}
	fwd := &guide.ForwardedRequest{
		Input:     string(data),
		SessionID: "sess",
		Metadata: map[string]any{
			"control_plane_kind": shared.ControlPlaneKindRemediationRequest,
		},
	}

	resultAny, handled, err := a.handleControlPlaneForward(context.Background(), fwd)
	if err != nil {
		t.Fatalf("handle remediation request: %v", err)
	}
	if !handled {
		t.Fatal("expected remediation request to be handled")
	}
	result, ok := resultAny.(*shared.RemediationResult)
	if !ok {
		t.Fatalf("expected remediation result, got %T", resultAny)
	}
	if result.Resolution != shared.RemediationResolutionFixWorkflow {
		t.Fatalf("expected fix_workflow resolution, got %s", result.Resolution)
	}
	if result.FixTaskCount == 0 {
		t.Fatal("expected remediation tasks")
	}
	if result.FixWorkflowDAGJSON == "" {
		t.Fatal("expected remediation dag json")
	}

	stored, ok := a.GetActivePlan("plan-1")
	if !ok || stored == nil {
		t.Fatal("expected stored plan")
	}
	if stored.Workflow == nil {
		t.Fatal("expected remediation workflow attached to plan")
	}
}
