package architect

import (
	"context"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/google/uuid"
)

// ReadyPlanMaxAge is the maximum age of a ready plan eligible for execution.
// Plans older than this are stale (e.g. restored from disk across sessions)
// and should not be dispatched.
const ReadyPlanMaxAge = 30 * time.Minute

// latestReadyPlan returns the most recently updated plan with PlanStatusReady
// for the given session, provided it was updated within ReadyPlanMaxAge.
func (a *Architect) latestReadyPlan(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		architectDebugLog().Warn("handoff: LATEST_READY_PLAN_EMPTY_SESSION")
		return nil
	}
	best := a.planStore.LatestByStatus(trimmed, PlanStatusReady, ReadyPlanMaxAge)
	if best == nil {
		architectDebugLog().Info("handoff: LATEST_READY_PLAN_NONE_FOUND",
			"request_session", trimmed,
			"total_plans", a.planStore.Count())
	} else {
		architectDebugLog().Info("handoff: LATEST_READY_PLAN_FOUND",
			"plan_id", best.ID,
			"plan_session", best.SessionID,
			"plan_age", time.Since(best.UpdatedAt).String())
	}
	return best
}

// dispatchPlanExecution validates the handoff payload, persists a pending
// handoff continuation, and publishes an explicit-target request to the
// orchestrator through the Guide without blocking the architect loop. The
// eventual orchestrator response is handled asynchronously in handleBusResponse.
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

	if plan.PendingWork != nil && plan.PendingWork.Kind == string(continuationKindPlanHandoff) {
		return &ConversationResult{
			Response: "The plan is already being handed off to the orchestrator. I'll update you when it confirms ingestion.",
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

	orchCID := "corr_" + uuid.NewString()
	if plan.SM().State() == PlanStatusReady {
		if err := plan.SM().TransitionTo(PlanStatusOrchestrating, plan); err != nil {
			return &ConversationResult{
				Response: "The plan is no longer in a ready state: " + err.Error(),
				Intent:   IntentExecute,
			}, true
		}
	}
	plan.Status = plan.SM().State()
	plan.Epoch = plan.SM().Epoch()
	plan.UpdatedAt = time.Now().UTC()
	userMessage := "Plan handoff queued to the orchestrator. I'll update you when it confirms ingestion."
	record := &ArchitectContinuation{
		ID:                      "cont_" + uuid.NewString(),
		Kind:                    continuationKindPlanHandoff,
		State:                   continuationStatusPending,
		PlanID:                  plan.ID,
		SessionID:               plan.SessionID,
		TargetAgentID:           "orchestrator",
		ResponseCorrelationID:   orchCID,
		InvocationCorrelationID: originalCIDFromContext(ctx),
		RequestJSON:             payload,
		CreatedAt:               time.Now().UTC(),
		ExpiresAt:               time.Now().UTC().Add(routeSyncTimeout),
	}
	if err := a.recordPendingContinuation(plan, record, userMessage); err != nil {
		return &ConversationResult{
			Response: "I couldn't persist the plan handoff state: " + err.Error(),
			Intent:   IntentExecute,
		}, true
	}
	if _, ok := architectStreamMetadataFromContext(ctx); ok {
		a.publishPlanSnapshot(ctx, plan)
	}
	request := &guide.RouteRequest{
		Input:               payload,
		CorrelationID:       orchCID,
		ParentCorrelationID: originalCIDFromContext(ctx),
		TargetAgentID:       "orchestrator",
		SessionID:           plan.SessionID,
	}
	if err := a.publishRouteRequest(request); err != nil {
		a.logWarn("dispatchPlanExecution: async publish failed", "plan_id", plan.ID, "error", err)
		if plan.SM().State() == PlanStatusOrchestrating {
			if transitionErr := plan.SM().TransitionTo(PlanStatusReady, plan); transitionErr == nil {
				plan.Status = plan.SM().State()
				plan.Epoch = plan.SM().Epoch()
			}
		}
		_ = a.clearPlanPendingContinuation(plan, orchCID)
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, "", err.Error())
		_ = a.persistPlanState(plan)
		return &ConversationResult{
			Response: "I tried to dispatch the plan but hit an error: " + err.Error() + "\nWant me to try again?",
			Intent:   IntentExecute,
		}, true
	}

	shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventPlanLeased,
		a.id, plan.SessionID, orchCID, "info",
		&agentlog.PlanPayload{PlanID: plan.ID, Status: "orchestrating", Epoch: plan.Epoch})
	return &ConversationResult{
		Response:      userMessage,
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
	return a.planStore.LatestHistorical(sessionID)
}

// latestConsultingPlan returns the most recently updated plan in Consulting
// or Clarifying state for the given session. Used by ask_user_question to
// attach clarification questions to the in-flight plan.
func (a *Architect) latestConsultingPlan(sessionID string) *DesignPlan {
	return a.planStore.LatestConsulting(sessionID)
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
	return a.planStore.LatestStalled(sessionID, stalledPlanMaxAge)
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
	for _, plan := range a.planStore.AllForSession(trimmed) {
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
		_ = a.planStore.Upsert(plan)
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
