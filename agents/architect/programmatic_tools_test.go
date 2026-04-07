package architect

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

type delegatedConversationControlPlane struct{}

func (delegatedConversationControlPlane) RequestGrant(context.Context, toolruntime.GuardianControlRequest) (*toolruntime.GuardianControlGrant, error) {
	return nil, skills.NewDelegatedError(map[string]any{
		"status":       "guardian_approval_pending",
		"user_message": "I'm routing your plan response through Guardian approval now. I'll continue the acceptance flow and update you shortly.",
	}, "guardian approval pending")
}

func TestInvokeConversationTool_DelegatedGuardianRequestReturnsPendingResult(t *testing.T) {
	registry := skills.NewRegistry()
	registry.Register(skills.NewSkill("route_plan_acceptance").
		Description("Route plan acceptance.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		}).
		Build())

	runtime, err := toolruntime.New(toolruntime.Config{
		Registry: registry,
		Manifest: toolruntime.NewManifest("architect", "architect.default",
			toolruntime.NewToolPolicy(
				"route_plan_acceptance",
				toolruntime.EffectMutating,
				toolruntime.DomainControl,
				toolruntime.ExecutionModeGuardian,
				toolruntime.WithApprovalSensitive(),
				toolruntime.WithVisibleByDefault(),
			),
		),
		State:                toolruntime.NewState(),
		GuardianControlPlane: delegatedConversationControlPlane{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(runtime.Close)

	a := &Architect{tools: runtime}
	result, err := a.invokeConversationTool(context.Background(), "route_plan_acceptance", routePlanAcceptanceParams{
		PlanID:       "plan-1",
		UserResponse: "go ahead",
	}, IntentConverse)
	if err != nil {
		t.Fatalf("invokeConversationTool error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("expected conversation result")
	}
	if result.Response != "I'm routing your plan response through Guardian approval now. I'll continue the acceptance flow and update you shortly." {
		t.Fatalf("response = %q", result.Response)
	}
}

func TestExtractAcceptanceResult_ParsesJSONStringData(t *testing.T) {
	payload := &planAcceptancePayload{
		Plan:         "plan",
		PlanID:       "plan-1",
		PlanName:     "Build feature",
		UserResponse: "go ahead",
	}
	msg := guide.NewResponseMessage("resp-1", &guide.RouteResponse{
		CorrelationID:     "corr-1",
		Success:           true,
		RespondingAgentID: "guide",
		Data:              `{"result":"accept","modifications":[]}`,
	})

	result, err := extractAcceptanceResult(msg, payload)
	if err != nil {
		t.Fatalf("extractAcceptanceResult error = %v", err)
	}
	if result.Result != "accept" {
		t.Fatalf("result = %q, want accept", result.Result)
	}
}

func TestExtractAcceptanceResult_ParsesFencedJSONStringData(t *testing.T) {
	payload := &planAcceptancePayload{
		Plan:         "plan",
		PlanID:       "plan-1",
		PlanName:     "Build feature",
		UserResponse: "go ahead",
	}
	msg := guide.NewResponseMessage("resp-1", &guide.RouteResponse{
		CorrelationID:     "corr-1",
		Success:           true,
		RespondingAgentID: "guide",
		Data:              "```json\n{\"result\":\"accept\",\"modifications\":[]}\n```",
	})

	result, err := extractAcceptanceResult(msg, payload)
	if err != nil {
		t.Fatalf("extractAcceptanceResult error = %v", err)
	}
	if result.Result != "accept" {
		t.Fatalf("result = %q, want accept", result.Result)
	}
}

func TestExtractAcceptanceResult_FallsBackToUserResponseVerdict(t *testing.T) {
	payload := &planAcceptancePayload{
		Plan:         "plan",
		PlanID:       "plan-1",
		PlanName:     "Build feature",
		UserResponse: "yes but reorder steps 2 and 3",
	}
	msg := guide.NewResponseMessage("resp-1", &guide.RouteResponse{
		CorrelationID:     "corr-1",
		Success:           true,
		RespondingAgentID: "guide",
		Data:              map[string]any{"note": "nonstandard output"},
	})

	result, err := extractAcceptanceResult(msg, payload)
	if err != nil {
		t.Fatalf("extractAcceptanceResult error = %v", err)
	}
	if result.Result != "modify" {
		t.Fatalf("result = %q, want modify", result.Result)
	}
}
