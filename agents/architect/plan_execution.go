package architect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
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
		architectDebugLog().Warn("handoff: LATEST_READY_PLAN_EMPTY_SESSION")
		return nil
	}
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	cutoff := time.Now().Add(-readyPlanMaxAge)
	var best *DesignPlan
	for _, plan := range a.activePlans {
		planState := plan.SM().State()
		planSession := strings.TrimSpace(plan.SessionID)
		if planState != PlanStatusReady {
			architectDebugLog().Debug("handoff: LATEST_READY_PLAN_SKIP_STATE",
				"plan_id", plan.ID,
				"state", planState.String(),
				"session", planSession)
			continue
		}
		if !strings.EqualFold(planSession, trimmed) {
			architectDebugLog().Debug("handoff: LATEST_READY_PLAN_SKIP_SESSION",
				"plan_id", plan.ID,
				"plan_session", planSession,
				"request_session", trimmed)
			continue
		}
		if plan.UpdatedAt.Before(cutoff) {
			architectDebugLog().Debug("handoff: LATEST_READY_PLAN_SKIP_EXPIRED",
				"plan_id", plan.ID,
				"updated_at", plan.UpdatedAt.String(),
				"cutoff", cutoff.String())
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	if best == nil {
		architectDebugLog().Info("handoff: LATEST_READY_PLAN_NONE_FOUND",
			"request_session", trimmed,
			"total_plans", len(a.activePlans))
	} else {
		architectDebugLog().Info("handoff: LATEST_READY_PLAN_FOUND",
			"plan_id", best.ID,
			"plan_session", best.SessionID,
			"plan_age", time.Since(best.UpdatedAt).String())
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
	architectDebugLog().Info("handoff: DISPATCH_ENTRY",
		"plan_id", plan.ID,
		"plan_state", plan.SM().State().String(),
		"task_count", len(plan.Tasks),
		"session_id", plan.SessionID)

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

	// Generate the orchestrator's correlation ID for the sync call.
	orchCID := "corr_" + uuid.NewString()

	a.logInfo("dispatchPlanExecution: BLOCKING CALL to requestRouteSync",
		"plan_id", plan.ID,
		"orch_cid", orchCID,
		"payload_len", len(payload),
		"ctx_deadline", contextDeadlineString(ctx))

	// The orchestrator handles its own push to the TUI via the agent push
	// mechanism — no reroute event needed from the architect.

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
			Response: "I tried to dispatch the plan but hit an error: " + err.Error() + "\nWant me to try again?",
			Intent:   IntentExecute,
		}, true
	}

	if !isHandoffSuccess(response) {
		summary := summarizeAutoHandoffResponse(response)
		a.logWarn("dispatchPlanExecution: orchestrator rejected", "plan_id", plan.ID, "summary", summary)
		return &ConversationResult{
			Response: "The orchestrator rejected the handoff: " + summary + "\nWant me to try again?",
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

	shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventPlanLeased,
		a.id, plan.SessionID, orchCID, "info",
		&agentlog.PlanPayload{PlanID: plan.ID, Status: "executing", Epoch: plan.Epoch})
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

// latestHistoricalPlanForSession returns the most recently updated plan for
// a session regardless of status. Used for conversation context enrichment
// and prior session context — not for execution eligibility.
func (a *Architect) latestHistoricalPlanForSession(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	var best *DesignPlan
	for _, plan := range a.activePlans {
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

// latestConsultingPlan returns the most recently updated plan in Consulting
// or Clarifying state for the given session. Used by ask_user_question to
// attach clarification questions to the in-flight plan.
func (a *Architect) latestConsultingPlan(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	var best *DesignPlan
	for _, plan := range a.activePlans {
		state := plan.SM().State()
		if state != PlanStatusConsulting && state != PlanStatusClarifying &&
			state != PlanStatusAnalyzing && state != PlanStatusDesigning {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

// evaluateAcceptanceVerdict sends the user's response to the Guide's
// purpose-built acceptance evaluator and returns the verdict
// (accept/modify/reject). This is the same evaluator that the
// route_plan_acceptance skill calls internally, extracted for deterministic
// routing in handleConversation so that plan approvals never depend on the
// LLM choosing to invoke the right tool.
func (a *Architect) evaluateAcceptanceVerdict(
	ctx context.Context,
	plan *DesignPlan,
	userResponse string,
) (acceptanceVerdict, error) {
	architectDebugLog().Info("handoff: EVAL_ACCEPTANCE_START",
		"plan_id", plan.ID,
		"user_response", truncateString(userResponse, 300),
		"bus_available", a.running && a.bus != nil)
	if !a.running || a.bus == nil {
		return "", fmt.Errorf("bus unavailable for acceptance evaluation")
	}
	payload := buildPlanAcceptancePayload(plan, userResponse)
	result, err := a.routePlanAcceptanceToGuide(ctx, plan.SessionID, payload)
	if err != nil {
		architectDebugLog().Warn("handoff: EVAL_ACCEPTANCE_BUS_ERROR",
			"plan_id", plan.ID,
			"error", err.Error())
		return "", err
	}
	par, ok := result.(*planAcceptanceResult)
	if !ok || par == nil {
		architectDebugLog().Warn("handoff: EVAL_ACCEPTANCE_BAD_RESULT",
			"plan_id", plan.ID,
			"result_type", fmt.Sprintf("%T", result))
		return "", fmt.Errorf("unexpected result type from acceptance evaluation: %T", result)
	}
	verdict := acceptanceVerdict(strings.TrimSpace(strings.ToLower(par.Result)))
	architectDebugLog().Info("handoff: EVAL_ACCEPTANCE_VERDICT",
		"plan_id", plan.ID,
		"verdict", string(verdict),
		"raw_result", par.Result,
		"user_response_preview", truncateString(userResponse, 100))
	if verdict == "" {
		return "", fmt.Errorf("empty verdict from acceptance evaluation")
	}
	return verdict, nil
}

// stalledPlanMaxAge is the maximum age of a plan eligible for stall recovery.
// Plans older than this were likely abandoned, not stalled by transient failure.
const stalledPlanMaxAge = 5 * time.Minute

// latestStalledPlan returns the most recently updated plan stuck at an
// intermediate state (Pending through Orchestrating) for the given session.
// Excludes Clarifying (intentionally waiting for user), Ready/Executing/
// Completed/Failed (terminal or post-ready). Used to detect plans that the
// conversation tool loop started but couldn't finish due to API errors.
func (a *Architect) latestStalledPlan(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	cutoff := time.Now().Add(-stalledPlanMaxAge)
	var best *DesignPlan
	for _, plan := range a.activePlans {
		if !isStalledState(plan.SM().State()) {
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

func isStalledState(s PlanStatus) bool {
	switch s {
	case PlanStatusPending, PlanStatusAnalyzing, PlanStatusConsulting,
		PlanStatusDesigning, PlanStatusGenerating, PlanStatusOrchestrating:
		return true
	default:
		return false
	}
}

// supersedeStalledPlans transitions all stalled plans for the given session
// to PlanStatusSuperseded. Called on interrupt so the next request starts
// fresh rather than recovering old plans with stale intent.
func (a *Architect) supersedeStalledPlans(sessionID string) {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return
	}
	a.activePlansMu.Lock()
	defer a.activePlansMu.Unlock()
	for _, plan := range a.activePlans {
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if !isStalledState(plan.SM().State()) {
			continue
		}
		a.logInfo("supersedeStalledPlans: superseding plan",
			"plan_id", plan.ID,
			"prior_status", plan.SM().State().String())

		shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventPlanReaped,
			a.id, plan.SessionID, "", "info",
			&agentlog.PlanPayload{PlanID: plan.ID, Status: plan.SM().State().String()})

		plan.sm = NewPlanStateMachine(plan.ID, PlanStatusSuperseded)
		plan.Status = PlanStatusSuperseded
		plan.UpdatedAt = time.Now()
	}
}

// recoverStalledPlan resets a plan stuck at an intermediate state and
// completes it via the deterministic protocol. Called when the conversation
// tool loop created a plan (via start_planning) but couldn't finish it
// because subsequent LLM calls failed.
func (a *Architect) recoverStalledPlan(ctx context.Context, plan *DesignPlan) {
	a.logInfo("recoverStalledPlan: recovering stalled plan",
		"plan_id", plan.ID,
		"stalled_status", plan.SM().State().String(),
		"plan_query", truncateString(plan.Query, 120))
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusPending)
	plan.Status = PlanStatusPending
	plan.Epoch = plan.SM().Epoch()
	plan.UpdatedAt = time.Now()

	diag := openProtocolDiagnostics(plan.ID, a.config.WorkingDirectory)
	defer diag.close()
	diag.log("stalled plan recovery plan=%s prior_query=%q", plan.ID, plan.Query)

	req := &ArchitectRequest{
		ID:        plan.ID,
		Intent:    IntentPlan,
		Query:     plan.Query,
		SessionID: plan.SessionID,
	}
	recovered, err := a.runDeterministicProtocol(ctx, req, plan, diag)
	if err != nil {
		a.logWarn("recoverStalledPlan: deterministic protocol failed",
			"plan_id", plan.ID, "error", err)
		return
	}
	a.logInfo("recoverStalledPlan: plan recovered",
		"plan_id", recovered.ID,
		"status", recovered.SM().State().String(),
		"tasks", len(recovered.Tasks))
}

// isPlanHandoffPayloadValid returns true if the payload is a valid
// PlanHandoff JSON (starts with {"plan_id":), not an error payload.
func isPlanHandoffPayloadValid(payload string) bool {
	trimmed := strings.TrimSpace(payload)
	return strings.HasPrefix(trimmed, `{"plan_id":`) || strings.HasPrefix(trimmed, `{"plan_id" :`)
}
