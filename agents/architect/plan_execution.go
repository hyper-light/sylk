package architect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
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

func planIDFromMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata["plan_id"].(string)
	return strings.TrimSpace(value)
}

func hasPlanContinuationMetadata(metadata map[string]any) bool {
	return planIDFromMetadata(metadata) != ""
}

func planWithinMaxAge(plan *DesignPlan, maxAge time.Duration, now time.Time) bool {
	if plan == nil {
		return false
	}
	if maxAge <= 0 {
		return true
	}
	if plan.UpdatedAt.IsZero() {
		return false
	}
	return !plan.UpdatedAt.Before(now.Add(-maxAge))
}

func isRecoverableReadyPlan(plan *DesignPlan, now time.Time) bool {
	return plan != nil &&
		plan.SM().State() == PlanStatusReady &&
		planWithinMaxAge(plan, ReadyPlanMaxAge, now)
}

func (a *Architect) resolveReadyPlanFromMetadata(metadata map[string]any, now time.Time) *DesignPlan {
	if plan := a.planFromMetadata(metadata); isRecoverableReadyPlan(plan, now) {
		architectDebugLog().Info("handoff: READY_PLAN_RECOVERED_FROM_METADATA",
			"plan_id", plan.ID,
			"plan_session", plan.SessionID)
		return plan
	}
	return nil
}

func (a *Architect) planFromMetadata(metadata map[string]any) *DesignPlan {
	if a == nil || a.planStore == nil {
		return nil
	}
	planID := planIDFromMetadata(metadata)
	if planID == "" {
		return nil
	}
	plan := a.planStore.Get(planID)
	if plan == nil {
		architectDebugLog().Warn("handoff: PLAN_METADATA_LOOKUP_MISS",
			"plan_id", planID)
		return nil
	}
	return plan
}

func (a *Architect) latestUniqueRecentPlan(match func(*DesignPlan) bool) *DesignPlan {
	if a == nil || a.planStore == nil {
		return nil
	}
	var (
		best  *DesignPlan
		count int
	)
	for _, plan := range a.planStore.Snapshot() {
		if !match(plan) {
			continue
		}
		count++
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	if count != 1 {
		if count > 1 {
			architectDebugLog().Info("handoff: UNIQUE_PLAN_RECOVERY_AMBIGUOUS",
				"candidate_count", count)
		}
		return nil
	}
	return best
}

func (a *Architect) resolveReadyPlanForContinuation(sessionID string, metadata map[string]any, now time.Time) *DesignPlan {
	if plan := a.resolveReadyPlanFromMetadata(metadata, now); plan != nil {
		return plan
	}
	if plan := a.latestReadyPlan(sessionID); plan != nil {
		return plan
	}
	if plan := a.latestUniqueRecentPlan(func(candidate *DesignPlan) bool {
		return isRecoverableReadyPlan(candidate, now)
	}); plan != nil {
		architectDebugLog().Info("handoff: READY_PLAN_RECOVERED_UNIQUELY",
			"plan_id", plan.ID,
			"plan_session", plan.SessionID)
		return plan
	}
	return nil
}

func (a *Architect) resolveRecoverableReadyPlanForConversation(sessionID string, now time.Time) *DesignPlan {
	if plan := a.latestReadyPlan(sessionID); plan != nil {
		return plan
	}
	if plan := a.latestUniqueRecentPlan(func(candidate *DesignPlan) bool {
		return isRecoverableReadyPlan(candidate, now)
	}); plan != nil {
		architectDebugLog().Info("handoff: READY_PLAN_CONVERSATION_RECOVERED_UNIQUELY",
			"plan_id", plan.ID,
			"plan_session", plan.SessionID)
		return plan
	}
	return nil
}

func (a *Architect) resolveActivePendingPlanForExecute(sessionID string, metadata map[string]any, now time.Time) *DesignPlan {
	if plan := a.planFromMetadata(metadata); planHasActivePendingWork(plan, now) {
		architectDebugLog().Info("handoff: PENDING_PLAN_RECOVERED_FROM_METADATA",
			"plan_id", plan.ID,
			"plan_session", plan.SessionID)
		return plan
	}
	if plan := a.latestActivePendingPlan(sessionID); plan != nil {
		return plan
	}
	if plan := a.latestUniqueRecentPlan(func(candidate *DesignPlan) bool {
		return planHasActivePendingWork(candidate, now)
	}); plan != nil {
		architectDebugLog().Info("handoff: PENDING_PLAN_RECOVERED_UNIQUELY",
			"plan_id", plan.ID,
			"plan_session", plan.SessionID)
		return plan
	}
	return nil
}

func (a *Architect) resolvePlanByStatusForExecute(sessionID string, metadata map[string]any, status PlanStatus, now time.Time) *DesignPlan {
	if plan := a.planFromMetadata(metadata); plan != nil &&
		plan.SM().State() == status &&
		planWithinMaxAge(plan, ReadyPlanMaxAge, now) {
		architectDebugLog().Info("handoff: STATUS_PLAN_RECOVERED_FROM_METADATA",
			"plan_id", plan.ID,
			"plan_session", plan.SessionID,
			"status", status.String())
		return plan
	}
	if a != nil && a.planStore != nil {
		if plan := a.planStore.LatestByStatus(sessionID, status, ReadyPlanMaxAge); plan != nil {
			return plan
		}
	}
	if plan := a.latestUniqueRecentPlan(func(candidate *DesignPlan) bool {
		return candidate != nil &&
			candidate.SM().State() == status &&
			planWithinMaxAge(candidate, ReadyPlanMaxAge, now)
	}); plan != nil {
		architectDebugLog().Info("handoff: STATUS_PLAN_RECOVERED_UNIQUELY",
			"plan_id", plan.ID,
			"plan_session", plan.SessionID,
			"status", status.String())
		return plan
	}
	return nil
}

// latestActivePendingPlan returns the most recently updated plan for the
// session that still has a live pending continuation attached.
func (a *Architect) latestActivePendingPlan(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" || a == nil || a.planStore == nil {
		return nil
	}
	now := time.Now().UTC()
	var best *DesignPlan
	for _, plan := range a.planStore.AllForSession(trimmed) {
		if !planHasActivePendingWork(plan, now) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	if best == nil && a.controlStore != nil {
		record, err := a.controlStore.LatestActiveContinuationForSession(trimmed, now)
		if err == nil && record != nil {
			if record.State == continuationStatusProcessing {
				return nil
			}
			best = a.reconcileContinuationRecord(record)
		}
	}
	return best
}

func planHasActivePendingWork(plan *DesignPlan, now time.Time) bool {
	if plan == nil || plan.PendingWork == nil {
		return false
	}
	switch plan.SM().State() {
	case PlanStatusCompleted, PlanStatusFailed, PlanStatusSuperseded:
		return false
	}
	status := strings.ToLower(strings.TrimSpace(plan.PendingWork.Status))
	if status != "" &&
		status != string(continuationStatusPending) &&
		status != string(continuationStatusProcessing) {
		return false
	}
	if !plan.PendingWork.ExpiresAt.IsZero() && now.After(plan.PendingWork.ExpiresAt) {
		return false
	}
	return true
}

func pendingPlanUserMessage(plan *DesignPlan) string {
	if plan == nil || plan.PendingWork == nil {
		return "I already have work in flight for this plan and will update you shortly."
	}
	if message := strings.TrimSpace(plan.PendingWork.Message); message != "" {
		return message
	}
	switch strings.TrimSpace(plan.PendingWork.Kind) {
	case string(continuationKindGuardianApproval):
		return "The latest plan response is still going through Guardian approval. I'll update you shortly."
	case string(continuationKindAcceptanceEval):
		return "I'm still reviewing your response against the current plan and will update you shortly."
	case string(continuationKindPlanHandoff):
		return "That plan is already being handed off to the orchestrator. I'll update you when it confirms ingestion."
	case string(continuationKindAcademicHandoff):
		return "The latest plan is still waiting on requirements research. I'll update you shortly."
	default:
		return "I already have work in flight for this plan and will update you shortly."
	}
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
	// Ensure the plan_handoff_payload artifact exists. publishPreparedHandoff
	// (called at plan-finalize) submits the testament+artifact and stamps
	// plan.HandoffPayloadArtifactID. If that path didn't run (rare —
	// architect/board crash between finalize and accept), submit a fresh
	// testament+artifact here so the dispatch claim has something to
	// reference. Either way, by the time we post the dispatch claim
	// below, the artifact is on the board.
	if strings.TrimSpace(plan.HandoffPayloadArtifactID) == "" {
		if err := a.publishPreparedHandoff(ctx, plan); err != nil {
			a.logWarn("dispatchPlanExecution: failed to publish prepared handoff",
				"plan_id", plan.ID,
				"error", err.Error())
		}
	}

	// Post dispatch claim: architect handing off plan to orchestrator.
	// This is a handoff in the cycle-ownership sense — once the plan
	// is dispatched, the orchestrator owns the executing top-level
	// cycle and the architect's planning cycle closes (UI_DESIGN.md
	// §2.2).
	//
	// The claim carries a depends_on Relation pointing at the
	// plan_handoff_payload artifact (submitted by publishPreparedHandoff
	// at plan-finalize). The orchestrator's claim intake follows this
	// relation, resolves the artifact off the board, and runs ingestPlan
	// deterministically — no LLM tool loop, no parallel bus message.
	// Single transport (the claim), single dispatch (deterministic).
	handoffClaim := claims.BuildHandoffClaim(
		"Dispatch plan "+plan.ID+" to orchestrator",
		fmt.Sprintf("Plan with %d tasks ready for execution", len(plan.Tasks)),
		"architect", "orchestrator",
		claims.ParentClaimIDFromContext(ctx),
		[]claims.ClaimScopeEntry{{Kind: "plan", Key: plan.ID}},
		[]*claims.Validation{
			architectValidation(claims.ValidationTypeReceipt, true, "Orchestrator acknowledges plan receipt", "PlanHandoffReceipt.Status >= Accepted"),
		},
	)
	if artifactID := strings.TrimSpace(plan.HandoffPayloadArtifactID); artifactID != "" {
		handoffClaim.Relations = append(handoffClaim.Relations, claims.Relation{
			Related:      artifactID,
			RelatedType:  claims.RelatedTypeArtifact,
			Relationship: claims.RelationshipDependsOn,
		})
	}
	a.architectPostClaim(ctx,
		architectClaimAction(claims.ActionTypeHandoff),
		handoffClaim,
	)

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
	a.logTrace("architect_plan_handoff_begin", "debug", plan.SessionID, "", agentlog.EventTaskDispatched, map[string]any{
		"plan_id":    plan.ID,
		"plan_state": plan.SM().State().String(),
		"task_count": len(plan.Tasks),
	})

	if !a.running || a.bus == nil {
		a.logWarn("dispatchPlanExecution: bus unavailable",
			"plan_id", plan.ID)
		return &ConversationResult{
			Response: "I have a plan ready, but I can't dispatch it right now — the orchestration bus isn't available.",
			Intent:   IntentExecute,
		}, true
	}

	targetAgentID, err := a.requirePlanHandoffTargetAgentID(plan, correlationIDFromPending(plan.PendingWork), "plan_handoff_dispatch")
	if err != nil {
		return &ConversationResult{
			Response: "I have a plan ready, but no registered orchestrator instance is available to accept it right now.",
			Intent:   IntentExecute,
		}, true
	}

	if plan.PendingWork != nil && plan.PendingWork.Kind == string(continuationKindPlanHandoff) {
		// Idempotency guard: if a plan-handoff continuation is still
		// fresh (ExpiresAt in the future), the orchestrator already
		// owns this dispatch — return early to avoid double-handoff.
		// If the continuation has expired, it's stale state from a
		// prior architect run that never received a confirmation
		// (typically: process restart between dispatch and confirm).
		// In that case clear the stale continuation and fall through
		// to a fresh dispatch so the user's resume actually executes
		// instead of getting "already being handed off" forever.
		if plan.PendingWork.ExpiresAt.IsZero() || time.Now().UTC().Before(plan.PendingWork.ExpiresAt) {
			a.logInfo("dispatchPlanExecution: short-circuit — plan already has fresh plan_handoff continuation; NOT publishing a new handoff",
				"plan_id", plan.ID,
				"session_id", plan.SessionID,
				"continuation_corr_id", plan.PendingWork.CorrelationID,
				"continuation_expires_at", plan.PendingWork.ExpiresAt,
				"continuation_message", plan.PendingWork.Message)
			a.logTrace("architect_plan_handoff_dispatch_idempotent_skip", "warn", plan.SessionID, plan.PendingWork.CorrelationID, agentlog.EventTaskDispatched, map[string]any{
				"plan_id":                 plan.ID,
				"continuation_corr_id":    plan.PendingWork.CorrelationID,
				"continuation_expires_at": plan.PendingWork.ExpiresAt,
				"continuation_kind":       plan.PendingWork.Kind,
				"source":                  "dispatchPlanExecution",
				"reason":                  "fresh_plan_handoff_continuation_present",
			})
			return &ConversationResult{
				Response: "The plan is already being handed off to the orchestrator. I'll update you when it confirms ingestion.",
				Intent:   IntentExecute,
			}, true
		}
		a.logInfo("dispatchPlanExecution: clearing stale plan_handoff continuation",
			"plan_id", plan.ID,
			"session_id", plan.SessionID,
			"continuation_corr_id", plan.PendingWork.CorrelationID,
			"expired_at", plan.PendingWork.ExpiresAt)
		a.logTrace("architect_plan_handoff_clearing_stale_continuation", "info", plan.SessionID, plan.PendingWork.CorrelationID, agentlog.EventTaskDispatched, map[string]any{
			"plan_id":              plan.ID,
			"continuation_corr_id": plan.PendingWork.CorrelationID,
			"expired_at":           plan.PendingWork.ExpiresAt,
			"source":               "dispatchPlanExecution",
		})
		plan.PendingWork = nil
	}

	// Two-phase ingest: tag the approval dispatch with
	// Phase=ExecutePrepared. Orchestrator looks up prepared state
	// (populated by an earlier publishPreparedHandoff fire-and-forget
	// during plan-finalize) and just runs scheduler.Submit. If the
	// prepared state is missing (architect/orchestrator crash, lost
	// publish, etc.), executePrepared falls back to full ingest using
	// the same payload — graceful degradation, no observable change.
	payload := buildPhasedHandoffPayload(plan, "user-approved execution", PlanHandoffPhaseExecutePrepared)
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
		TargetAgentID:           targetAgentID,
		ResponseCorrelationID:   orchCID,
		InvocationCorrelationID: originalCIDFromContext(ctx),
		RequestJSON:             payload,
		CreatedAt:               time.Now().UTC(),
		ExpiresAt:               time.Now().UTC().Add(routeSyncTimeout),
	}
	if err := a.recordPendingContinuation(plan, record, userMessage); err != nil {
		a.logTrace("architect_plan_handoff_record_failed", "error", plan.SessionID, orchCID, agentlog.EventError, map[string]any{
			"plan_id": plan.ID,
			"error":   err.Error(),
		})
		return &ConversationResult{
			Response: "I couldn't persist the plan handoff state: " + err.Error(),
			Intent:   IntentExecute,
		}, true
	}
	a.logTrace("architect_plan_handoff_recorded", "debug", plan.SessionID, orchCID, agentlog.EventTaskDispatched, map[string]any{
		"plan_id":         plan.ID,
		"continuation_id": record.ID,
		"expires_at":      record.ExpiresAt,
	})
	if originalCID := originalCIDFromContext(ctx); originalCID != "" {
		a.publishHandoffReroute(ctx, "orchestrator", "execution plan handoff", originalCID, orchCID)
	}
	if _, ok := architectStreamMetadataFromContext(ctx); ok {
		a.publishPlanSnapshot(ctx, plan)
	}
	// Dispatch transport: the handoff claim posted above (with depends_on
	// → plan_handoff_payload artifact) IS the dispatch. The orchestrator's
	// claim intake resolves the artifact and runs ingestPlan
	// deterministically. No bus RouteRequest is published — it would race
	// the claim path's deterministic dispatch and produce the dual-signal
	// LLM tool loop bug observed at 2026-05-04.
	a.logTrace("architect_plan_handoff_dispatched_via_claim", "debug", plan.SessionID, orchCID, agentlog.EventTaskDispatched, map[string]any{
		"plan_id":         plan.ID,
		"target_agent_id": targetAgentID,
		"artifact_id":     plan.HandoffPayloadArtifactID,
	})

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

func (a *Architect) latestStalledPlanForRequest(sessionID, requestCorrelationID string) *DesignPlan {
	if strings.TrimSpace(requestCorrelationID) == "" {
		return a.latestStalledPlan(sessionID)
	}
	return a.planStore.LatestStalledForRequest(sessionID, requestCorrelationID, stalledPlanMaxAge)
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

func isReusablePlanningState(s PlanStatus) bool {
	switch s {
	case PlanStatusPending, PlanStatusAnalyzing, PlanStatusConsulting,
		PlanStatusClarifying, PlanStatusDesigning, PlanStatusGenerating,
		PlanStatusOrchestrating, PlanStatusReady:
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
