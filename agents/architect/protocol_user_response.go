package architect

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
)

// readyUserResponseInline streams plan commentary token-by-token into chat
// via publishPlanStreamChunk. Returns the text for persistence in
// plan.UserResponse.
func (a *Architect) readyUserResponseInline(
	ctx context.Context,
	req *ArchitectRequest,
	plan *DesignPlan,
) string {
	request := a.buildReadyConversationRequest(req, plan)
	request.OnChunk = func(text string) {
		a.publishPlanStreamChunk(ctx, text)
	}
	response, err := a.composeUserFacingResponse(ctx, request)
	if err != nil {
		fb := fallbackReadyUserResponse(req, plan)
		a.publishPlanStreamChunk(ctx, fb)
		return fb
	}
	return response
}

func (a *Architect) buildReadyConversationRequest(
	req *ArchitectRequest,
	plan *DesignPlan,
) plannerConversationRequest {
	requirements := requirementsFromPlan(plan)
	return plannerConversationRequest{
		Mode:                    plannerConversationModeReady,
		UserQuery:               reqQuery(req),
		PriorQuery:              requirementsQuery(requirements),
		Scope:                   requirementsScope(requirements),
		RecommendationNarrative: clarificationRecommendationNarrative(requirements),
		Recommendations:         clarificationRecommendationItems(requirements),
		Tradeoffs:               clarificationTradeoffItems(requirements),
		Assumptions:             assumptionsFromPlan(plan),
		TaskCount:               planTaskCount(plan),
		LayerCount:              planLayerCount(plan),
		FirstTask:               firstTaskName(plan),
		ApprovalRequired:        !a.config.AutoApprove,
		SessionID:               reqSessionID(req),
		ConversationHistory:     reqConversationHistory(req),
	}
}

func reqConversationHistory(req *ArchitectRequest) []guide.ConversationTurn {
	if req == nil {
		return nil
	}
	return req.ConversationHistory
}

func fallbackReadyUserResponse(_ *ArchitectRequest, _ *DesignPlan) string {
	return "The plan is ready. Let me know if you want to refine anything before execution."
}

func reqSessionID(req *ArchitectRequest) string {
	if req == nil {
		return ""
	}
	return req.SessionID
}

func planLayerCount(plan *DesignPlan) int {
	if plan == nil || plan.Workflow == nil {
		return 0
	}
	return len(plan.Workflow.ExecutionLayers)
}

func requirementsFromPlan(plan *DesignPlan) *Requirements {
	if plan == nil {
		return nil
	}
	return plan.Requirements
}

func assumptionsFromPlan(plan *DesignPlan) []string {
	if plan == nil {
		return nil
	}
	return append([]string(nil), plan.Assumptions...)
}

func planTaskCount(plan *DesignPlan) int {
	if plan == nil {
		return 0
	}
	return len(plan.Tasks)
}

func firstTaskName(plan *DesignPlan) string {
	if plan == nil || len(plan.Tasks) == 0 || plan.Tasks[0] == nil {
		return ""
	}
	return strings.TrimSpace(plan.Tasks[0].Name)
}
