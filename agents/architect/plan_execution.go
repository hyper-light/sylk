package architect

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
)

// executionPhrases are user phrases that signal "execute the current plan".
// Matched case-insensitively via substring against the user input.
var executionPhrases = []string{
	"go ahead",
	"execute",
	"proceed",
	"let's do it",
	"let's go",
	"do it",
	"start execution",
	"run the plan",
	"ship it",
	"kick it off",
	"sounds good",
	"looks good",
	"approved",
	"lgtm",
}

// isExecutionRequest returns true if the user message signals intent
// to execute the current ready plan.
func isExecutionRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	for _, phrase := range executionPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// tryExecutePlan checks if a ready plan exists and dispatches it.
// Returns (result, true) if execution was triggered, (nil, false) otherwise.
func (a *Architect) tryExecutePlan(ctx context.Context, req *ArchitectRequest) (*ConversationResult, bool) {
	if !isExecutionRequest(req.Query) {
		return nil, false
	}
	plan := a.latestReadyPlan()
	if plan == nil {
		return nil, false
	}
	return a.dispatchPlanExecution(ctx, req, plan)
}

// latestReadyPlan returns the most recently updated plan with PlanStatusReady.
func (a *Architect) latestReadyPlan() *DesignPlan {
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	var best *DesignPlan
	for _, plan := range a.activePlans {
		if plan.Status != PlanStatusReady {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

// dispatchPlanExecution transitions the plan to Executing and routes to orchestrator.
func (a *Architect) dispatchPlanExecution(
	ctx context.Context,
	_ *ArchitectRequest,
	plan *DesignPlan,
) (*ConversationResult, bool) {
	if !a.running || a.bus == nil {
		return &ConversationResult{
			Response: "I have a plan ready, but I can't dispatch it right now — the orchestration bus isn't available.",
			Intent:   IntentExecute,
		}, true
	}

	plan.Status = PlanStatusExecuting
	_ = a.persistPlanState(plan)

	a.publishPlanSnapshot(ctx, plan)

	payload := buildHandoffPayload(plan, "user-approved execution")
	request := &guide.RouteRequest{
		Input:         payload,
		TargetAgentID: "orchestrator",
		SessionID:     plan.SessionID,
	}

	response, err := a.requestRouteSync(ctx, request)
	if err != nil {
		plan.Status = PlanStatusFailed
		plan.Error = err.Error()
		_ = a.persistPlanState(plan)
		a.publishPlanSnapshot(ctx, plan)
		return &ConversationResult{
			Response: "I tried to dispatch the plan but hit an error: " + err.Error(),
			Intent:   IntentExecute,
		}, true
	}

	summary := summarizeAutoHandoffResponse(response)
	plan.RiskSummary = append(plan.RiskSummary, summary)
	_ = a.persistPlanState(plan)
	a.publishPlanSnapshot(ctx, plan)

	return &ConversationResult{
		Response: fmt.Sprintf("Plan dispatched to the orchestrator. %s", summary),
		Intent:   IntentExecute,
	}, true
}
