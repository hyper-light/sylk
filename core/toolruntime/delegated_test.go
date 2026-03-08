package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

type delegatedGuardianControlPlane struct{}

func (delegatedGuardianControlPlane) RequestGrant(context.Context, GuardianControlRequest) (*GuardianControlGrant, error) {
	return nil, skills.NewDelegatedError(map[string]any{
		"status":       "guardian_approval_pending",
		"user_message": "approval pending",
	}, "approval pending")
}

func TestExecute_PreservesDelegatedPayloadOutput(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, skills.NewSkill("route_plan_acceptance").
		Description("Route plan acceptance.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		}).
		Build())

	rt := mustNewRuntimeWithConfig(t, Config{
		Registry: registry,
		Manifest: manifestWithSearch("architect", "architect.default",
			NewToolPolicy("route_plan_acceptance", EffectMutating, DomainControl, ExecutionModeGuardian, WithApprovalSensitive(), WithVisibleByDefault()),
		),
		State:                NewState(),
		GuardianControlPlane: delegatedGuardianControlPlane{},
	})

	result, err := rt.Execute(context.Background(), Invocation{
		ToolCall: providers.ToolCall{
			ID:        "tool-1",
			Name:      "route_plan_acceptance",
			Arguments: `{"plan_id":"plan-1","user_response":"yes"}`,
		},
		AgentID:         "architect",
		CorrelationID:   "corr-1",
		CapabilityScope: "architect.default",
	})
	if !errors.Is(err, skills.ErrDelegatedRequested) {
		t.Fatalf("expected delegated sentinel, got %v", err)
	}
	assertJSONContains(t, result.Output, "status", "guardian_approval_pending")
	assertJSONContains(t, result.Output, "user_message", "approval pending")
}
