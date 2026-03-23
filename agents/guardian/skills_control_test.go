package guardian

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func TestEvaluateToolExecutionControl_AllowsKnownAgentTypeForCanonicalSource(t *testing.T) {
	g := newTestGuardian(t, &stubGuardianProvider{})
	g.SeedKnownAgents([]*guide.AgentAnnouncement{
		{AgentID: "arch-123", AgentType: "architect", AgentName: "Architect"},
	})

	grant, err := g.evaluateToolExecutionControl("arch-123", &toolruntime.GuardianControlRequest{
		AgentID:         "architect",
		CorrelationID:   "corr-1",
		CapabilityScope: "architect.default",
		ToolName:        "route_plan_acceptance",
		Arguments:       `{"plan_id":"plan-1"}`,
		ArgumentsHash:   toolruntime.HashArguments(`{"plan_id":"plan-1"}`),
		Policy: toolruntime.NewToolPolicy(
			"route_plan_acceptance",
			toolruntime.EffectMutating,
			toolruntime.DomainControl,
			toolruntime.ExecutionModeGuardian,
			toolruntime.WithApprovalSensitive(),
		),
	})
	if err != nil {
		t.Fatalf("evaluateToolExecutionControl() error = %v", err)
	}
	if grant == nil || !grant.Approved {
		t.Fatalf("expected approved grant, got %#v", grant)
	}
	if grant.AgentID != "architect" {
		t.Fatalf("grant.AgentID = %q, want architect", grant.AgentID)
	}
}

func TestEvaluateToolExecutionControl_RejectsUnknownSourceMismatch(t *testing.T) {
	g := newTestGuardian(t, &stubGuardianProvider{})

	_, err := g.evaluateToolExecutionControl("arch-123", &toolruntime.GuardianControlRequest{
		AgentID:         "architect",
		CorrelationID:   "corr-1",
		CapabilityScope: "architect.default",
		ToolName:        "route_plan_acceptance",
		Arguments:       `{"plan_id":"plan-1"}`,
		Policy: toolruntime.NewToolPolicy(
			"route_plan_acceptance",
			toolruntime.EffectMutating,
			toolruntime.DomainControl,
			toolruntime.ExecutionModeGuardian,
			toolruntime.WithApprovalSensitive(),
		),
	})
	if err == nil {
		t.Fatal("expected source mismatch error")
	}
}
