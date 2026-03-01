package architect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// readyPlanMaxAge is the maximum age of a ready plan eligible for execution.
// Plans older than this are stale (e.g. restored from disk across sessions)
// and should not be dispatched.
const readyPlanMaxAge = 30 * time.Minute

// latestReadyPlan returns the most recently updated plan with PlanStatusReady
// for the given session, provided it was updated within readyPlanMaxAge.
// Session scoping prevents restored plans from prior sessions from misleading
// routing (e.g. triggering handlePlanFeedback for plans the user never saw).
func (a *Architect) latestReadyPlan(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	cutoff := time.Now().Add(-readyPlanMaxAge)
	var best *DesignPlan
	for _, plan := range a.activePlans {
		if plan.SM().State() != PlanStatusReady {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
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

// dispatchPlanExecution validates the handoff payload, routes it synchronously
// to the orchestrator, and transitions the plan to Executing on success.
// Emits a StreamEventReroute BEFORE the sync call so the TUI switches to the
// orchestrator agent while the orchestrator is actively processing.
func (a *Architect) dispatchPlanExecution(
	ctx context.Context,
	_ *ArchitectRequest,
	plan *DesignPlan,
) (*ConversationResult, bool) {
	a.logInfo("dispatchPlanExecution: entry",
		"plan_id", plan.ID,
		"plan_status", plan.SM().State().String(),
		"tasks", len(plan.Tasks),
		"running", a.running,
		"bus_available", a.bus != nil,
		"ctx_deadline", contextDeadlineString(ctx))

	if !a.running || a.bus == nil {
		a.logWarn("dispatchPlanExecution: bus unavailable",
			"plan_id", plan.ID)
		return &ConversationResult{
			Response: "I have a plan ready, but I can't dispatch it right now — the orchestration bus isn't available.",
			Intent:   IntentExecute,
		}, true
	}

	payload := buildHandoffPayload(plan, "user-approved execution")
	if !isPlanHandoffPayloadValid(payload) {
		a.logWarn("dispatchPlanExecution: invalid handoff payload",
			"plan_id", plan.ID,
			"payload_len", len(payload))
		return &ConversationResult{
			Response: "The plan could not be serialized for handoff. This is an internal error — please retry or revise the plan.",
			Intent:   IntentExecute,
		}, true
	}

	a.publishPlanSnapshot(ctx, plan)

	// Generate the orchestrator's correlation ID before the sync call so we
	// can emit a reroute event that tells the TUI to track the orchestrator's
	// stream events. Without this, the TUI would only see the architect's CID.
	orchCID := "corr_" + uuid.NewString()

	// Extract the architect's original CID from stream context for the reroute.
	originalCID := originalCIDFromContext(ctx)

	a.logInfo("dispatchPlanExecution: emitting reroute BEFORE sync call",
		"plan_id", plan.ID,
		"original_cid", originalCID,
		"orch_cid", orchCID,
		"payload_len", len(payload))

	// Emit reroute BEFORE the sync call so the TUI switches to "orchestrator"
	// while the orchestrator is actively processing the plan ingestion.
	a.publishHandoffReroute(ctx, "orchestrator", originalCID, orchCID)

	a.logInfo("dispatchPlanExecution: BLOCKING CALL to requestRouteSync",
		"plan_id", plan.ID,
		"orch_cid", orchCID,
		"ctx_deadline", contextDeadlineString(ctx))

	request := &guide.RouteRequest{
		Input:         payload,
		CorrelationID: orchCID,
		TargetAgentID: "orchestrator",
		SessionID:     plan.SessionID,
	}

	handoffStart := time.Now()
	response, err := a.requestRouteSync(ctx, request)
	handoffElapsed := time.Since(handoffStart)
	a.logInfo("dispatchPlanExecution: requestRouteSync returned",
		"plan_id", plan.ID,
		"blocked_for", handoffElapsed.String(),
		"has_response", response != nil,
		"has_error", err != nil)
	if err != nil {
		a.logWarn("dispatchPlanExecution: route failed", "plan_id", plan.ID, "error", err)
		return &ConversationResult{
			Response: "I tried to dispatch the plan but hit an error: " + err.Error() + "\nSay **go ahead** to retry.",
			Intent:   IntentExecute,
		}, true
	}

	if !isHandoffSuccess(response) {
		summary := summarizeAutoHandoffResponse(response)
		a.logWarn("dispatchPlanExecution: orchestrator rejected", "plan_id", plan.ID, "summary", summary)
		return &ConversationResult{
			Response: "The orchestrator rejected the handoff: " + summary + "\nSay **go ahead** to retry.",
			Intent:   IntentExecute,
		}, true
	}

	// Orchestrator confirmed ingestion — CAS transition Ready→Executing.
	// ReadyDirective is derived from SM state, so transitioning to Executing
	// automatically makes it return nil.
	if err := plan.SM().TransitionTo(PlanStatusExecuting, plan); err != nil {
		a.logWarn("dispatchPlanExecution: transition to Executing failed", "plan_id", plan.ID, "error", err)
		return &ConversationResult{
			Response: "The plan is no longer in a ready state: " + err.Error(),
			Intent:   IntentExecute,
		}, true
	}
	plan.Status = plan.SM().State()
	plan.Epoch = plan.SM().Epoch()
	// Grant executing lease for crash recovery detection.
	if a.leaseManager != nil {
		a.leaseManager.GrantExecutingLease(plan, "orchestrator")
	}
	summary := summarizeAutoHandoffResponse(response)
	plan.RiskSummary = append(plan.RiskSummary, summary)
	_ = a.persistPlanState(plan)
	a.publishPlanSnapshot(ctx, plan)

	return &ConversationResult{
		Response:      fmt.Sprintf("Plan dispatched to the orchestrator. %s", summary),
		Intent:        IntentExecute,
		HandoffTarget: "orchestrator",
	}, true
}

// originalCIDFromContext extracts the architect's original correlation ID
// from the stream context metadata.
func originalCIDFromContext(ctx context.Context) string {
	metadata, ok := architectStreamMetadataFromContext(ctx)
	if !ok {
		return ""
	}
	return metadata.CorrelationID
}

// isPlanHandoffPayloadValid returns true if the payload is a valid
// PlanHandoff JSON (starts with {"plan_id":), not an error payload.
func isPlanHandoffPayloadValid(payload string) bool {
	trimmed := strings.TrimSpace(payload)
	return strings.HasPrefix(trimmed, `{"plan_id":`) || strings.HasPrefix(trimmed, `{"plan_id" :`)
}
