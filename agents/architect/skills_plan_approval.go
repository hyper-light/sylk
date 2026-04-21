// Phase 2.3 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// plan_acceptance absorbs route_plan_acceptance, handle_plan_acceptance_result,
// and present_plan_approval_dialog into a single verb-typed primitive.
//
// The three underlying handlers stay — routePlanAcceptanceSkill,
// handlePlanAcceptanceResultSkill, and presentPlanApprovalDialogSkill
// remain as private helpers in skills_planning.go — but the LLM only
// sees plan_acceptance with action ∈ {present, route, handle_result}.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

type planAcceptanceInput struct {
	Action string `json:"action"`
	PlanID string `json:"plan_id"`
	// Route / handle_result fields.
	UserResponse  string   `json:"user_response,omitempty"`
	Result        string   `json:"result,omitempty"`
	Modifications []string `json:"modifications,omitempty"`
}

// planAcceptanceSkill is the single LLM-facing primitive for the plan
// approval lifecycle. It routes to action-specific handlers that map
// 1:1 onto the pre-Phase-2 skills (present / route / handle_result).
func planAcceptanceSkill(a *Architect) *skills.Skill {
	type handler = func(context.Context, *planAcceptanceInput) (any, error)
	dispatch := map[string]handler{
		"present":        planAcceptancePresent(a),
		"route":          planAcceptanceRoute(a),
		"handle_result":  planAcceptanceHandleResult(a),
	}

	return skills.NewSkill("plan_acceptance").
		Description("Manage the plan approval handshake. Actions:\n" +
			"- present: Publish the Approve/Modify/Reject dialog for a Ready plan (params: plan_id [required]).\n" +
			"- route: Route the user's verbatim response to the Guide's evaluate-plan-acceptance skill (params: plan_id, user_response [required]).\n" +
			"- handle_result: Act on the Guide's verdict — dispatch, revise, or request clarification (params: plan_id, result [required: accept|modify|reject], user_response [required], modifications).").
		Domain("coordination").
		Keywords("plan", "acceptance", "approval", "dialog", "approve", "modify", "reject", "verdict", "dispatch").
		Priority(100).
		Usage("Drive the post-Ready acceptance flow in order: plan_acceptance(action=present, plan_id=<id>) after start_planning reports Ready, then plan_acceptance(action=route, plan_id=<id>, user_response=<verbatim>) after the user responds, then plan_acceptance(action=handle_result, plan_id=<id>, result=<verdict>, user_response=<verbatim>, modifications=<list>) once the Guide returns a verdict.").
		BestPractice("Never skip any action — present without route leaves the user waiting; route without handle_result strands the verdict.").
		Example(`{"action": "present", "plan_id": "plan_abc"}`).
		Example(`{"action": "route", "plan_id": "plan_abc", "user_response": "Looks good."}`).
		Example(`{"action": "handle_result", "plan_id": "plan_abc", "result": "modify", "user_response": "Swap steps 2 and 3", "modifications": ["Reorder task 2 and task 3"]}`).
		EnumParam("action", "Acceptance action to perform", []string{"present", "route", "handle_result"}, true).
		StringParam("plan_id", "Plan identifier (uses latest Ready plan if omitted for route/handle_result)", false).
		StringParam("user_response", "The user's verbatim response (route / handle_result)", false).
		EnumParam("result", "The Guide's verdict (handle_result)", []string{"accept", "modify", "reject"}, false).
		ArrayParam("modifications", "Modification notes from the Guide (handle_result when result=modify|reject)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params planAcceptanceInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[strings.TrimSpace(params.Action)]
			if !ok {
				return nil, fmt.Errorf("unknown plan_acceptance action: %q (expected present|route|handle_result)", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

// planAcceptancePresent mirrors presentPlanApprovalDialogSkill's handler
// body. We reuse the private per-action handlers defined in
// skills_planning.go / (this file) rather than duplicating the logic.
func planAcceptancePresent(a *Architect) func(context.Context, *planAcceptanceInput) (any, error) {
	return func(ctx context.Context, params *planAcceptanceInput) (any, error) {
		planID := strings.TrimSpace(params.PlanID)
		if planID == "" {
			return nil, fmt.Errorf("plan_id is required for action=present")
		}
		plan := a.planStore.Get(planID)
		if plan == nil {
			return map[string]any{
				"published": false,
				"plan_id":   planID,
				"error":     "plan not found in active plan store",
			}, nil
		}
		if plan.PendingWork != nil && plan.PendingWork.Kind == string(continuationKindPlanApproval) {
			return map[string]any{
				"published":      false,
				"plan_id":        planID,
				"already_pending": true,
				"correlation_id": plan.PendingWork.CorrelationID,
			}, nil
		}
		if err := a.requestPlanApprovalDialog(ctx, plan); err != nil {
			return map[string]any{
				"published": false,
				"plan_id":   planID,
				"error":     err.Error(),
			}, nil
		}
		return map[string]any{
			"published": true,
			"plan_id":   planID,
		}, nil
	}
}

// planAcceptanceRoute forwards the user response to the Guide for
// evaluation. Extracted from routePlanAcceptanceSkill.
func planAcceptanceRoute(a *Architect) func(context.Context, *planAcceptanceInput) (any, error) {
	return func(ctx context.Context, params *planAcceptanceInput) (any, error) {
		if strings.TrimSpace(params.UserResponse) == "" {
			return nil, fmt.Errorf("user_response is required for action=route")
		}
		sessionID := architectSessionIDFromContext(ctx)
		plan, statusReport, err := a.resolveReadyPlanForAcceptance(params.PlanID, sessionID)
		if err != nil {
			return nil, err
		}
		if statusReport != nil {
			return statusReport, nil
		}
		payload := buildPlanAcceptancePayload(plan, params.UserResponse)
		result, err := a.submitPlanAcceptanceEvaluation(ctx, plan, payload)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
}

// planAcceptanceHandleResult acts on the Guide's verdict. Extracted
// from handlePlanAcceptanceResultSkill.
func planAcceptanceHandleResult(a *Architect) func(context.Context, *planAcceptanceInput) (any, error) {
	return func(ctx context.Context, params *planAcceptanceInput) (any, error) {
		if strings.TrimSpace(params.Result) == "" {
			return nil, fmt.Errorf("result is required for action=handle_result")
		}
		if strings.TrimSpace(params.UserResponse) == "" {
			return nil, fmt.Errorf("user_response is required for action=handle_result")
		}
		sessionID := architectSessionIDFromContext(ctx)
		plan, statusReport, err := a.resolveReadyPlanForAcceptance(params.PlanID, sessionID)
		if err != nil {
			return nil, err
		}
		if statusReport != nil {
			return statusReport, nil
		}
		verdict := acceptanceVerdict(params.Result)
		switch verdict {
		case verdictAccept:
			return a.actOnAccept(ctx, plan)
		case verdictModify:
			return a.actOnModify(ctx, plan, params.UserResponse, params.Modifications)
		case verdictReject:
			return a.actOnReject(ctx, plan, params.UserResponse, params.Modifications)
		default:
			return nil, fmt.Errorf("unknown verdict: %q", params.Result)
		}
	}
}
