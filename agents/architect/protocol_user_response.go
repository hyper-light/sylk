package architect

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
)

// readyUserResponseInline streams plan commentary and the readiness footer
// token-by-token into chat via publishPlanStreamChunk. Returns the combined
// text for persistence in plan.UserResponse.
func (a *Architect) readyUserResponseInline(
	ctx context.Context,
	req *ArchitectRequest,
	plan *DesignPlan,
) string {
	request := buildReadyConversationRequest(req, plan)
	request.OnChunk = func(text string) {
		a.publishPlanStreamChunk(ctx, text)
	}

	var summary string
	if response, err := a.composeUserFacingResponse(ctx, request); err == nil {
		summary = response
	} else {
		fb := fallbackReadyUserResponse(req, plan)
		a.publishPlanStreamChunk(ctx, fb)
		summary = fb
	}

	footer := readinessFooter(plan)
	if footer != "" {
		a.publishPlanStreamChunk(ctx, "\n\n"+footer)
		return summary + "\n\n" + footer
	}
	return summary
}

func buildReadyConversationRequest(
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
		FirstTask:               firstTaskName(plan),
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

// readinessFooter returns a deterministic footer that signals the plan is
// ready for execution. Derived entirely from plan data — no LLM involved.
func readinessFooter(plan *DesignPlan) string {
	tasks := planTaskCount(plan)
	if tasks == 0 {
		return ""
	}
	layers := planLayerCount(plan)
	var detail string
	switch {
	case layers > 1:
		detail = fmt.Sprintf("**Plan ready** — %d tasks across %d execution layers.", tasks, layers)
	default:
		detail = fmt.Sprintf("**Plan ready** — %d tasks.", tasks)
	}
	return detail + "\nHanding off to the orchestrator for execution."
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
