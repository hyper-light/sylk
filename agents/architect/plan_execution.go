package architect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

// executionSubstrings are user phrases matched via substring containment.
var executionSubstrings = []string{
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
	"run it",
	"fire away",
	"begin execution",
	"make it so",
	"let's start",
	"start building",
	"confirmed",
}

// executionExactPhrases are short affirmatives that match only when the
// entire message (after trimming/lowering) equals the phrase. Substring
// matching would cause false positives (e.g. "yes, but can we change X?").
var executionExactPhrases = []string{
	"yes",
	"y",
	"yep",
	"yeah",
	"yup",
	"ok",
	"okay",
	"sure",
	"right",
	"great",
	"perfect",
	"awesome",
	"absolutely",
	"affirmative",
	"roger",
	"aye",
}

// isExecutionRequest returns true if the user message signals intent
// to execute the current ready plan.
func isExecutionRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	// Strip trailing punctuation for exact matching (e.g. "yes!" → "yes").
	stripped := strings.TrimRight(lower, ".!?,;:")
	for _, phrase := range executionExactPhrases {
		if stripped == phrase {
			return true
		}
	}
	for _, phrase := range executionSubstrings {
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
		a.logInfo("tryExecutePlan: execution request but no ready plan found")
		return nil, false
	}
	a.logInfo("tryExecutePlan: dispatching plan",
		"plan_id", plan.ID,
		"query", truncateString(plan.Query, 80),
		"tasks", len(plan.Tasks),
		"created_at", plan.CreatedAt.String())
	return a.dispatchPlanExecution(ctx, req, plan)
}

// readyPlanMaxAge is the maximum age of a ready plan eligible for execution.
// Plans older than this are stale (e.g. restored from disk across sessions)
// and should not be dispatched.
const readyPlanMaxAge = 30 * time.Minute

// latestReadyPlan returns the most recently updated plan with PlanStatusReady,
// provided it was updated within readyPlanMaxAge. Stale restored plans are
// skipped to prevent dispatching outdated generic plans.
func (a *Architect) latestReadyPlan() *DesignPlan {
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	cutoff := time.Now().Add(-readyPlanMaxAge)
	var best *DesignPlan
	for _, plan := range a.activePlans {
		if plan.Status != PlanStatusReady {
			continue
		}
		if plan.UpdatedAt.Before(cutoff) {
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
