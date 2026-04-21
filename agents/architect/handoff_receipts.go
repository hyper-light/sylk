package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/google/uuid"
)

const planHandoffStatusLookupTimeout = 2 * time.Second

var pendingPlanHandoffReceiptStatuses = map[agentshared.PlanHandoffReceiptStatus]struct{}{
	agentshared.PlanHandoffReceiptStatusAccepted: {},
	agentshared.PlanHandoffReceiptStatusDAGBuilt: {},
}

var executingPlanHandoffReceiptStatuses = map[agentshared.PlanHandoffReceiptStatus]struct{}{
	agentshared.PlanHandoffReceiptStatusSubmitted: {},
	agentshared.PlanHandoffReceiptStatusRunning:   {},
}

func (a *Architect) lookupPlanHandoffReceipt(
	ctx context.Context,
	plan *DesignPlan,
) (*agentshared.PlanHandoffReceipt, bool, error) {
	if !canLookupPlanHandoffReceipt(a, plan) {
		return nil, false, nil
	}
	payload, err := marshalPlanHandoffStatusRequest(plan)
	if err != nil {
		return nil, false, err
	}
	msg, err := a.requestPlanHandoffStatus(ctx, plan, payload)
	if err != nil {
		return nil, false, err
	}
	return decodePlanHandoffStatusMessage(msg)
}

func canLookupPlanHandoffReceipt(a *Architect, plan *DesignPlan) bool {
	if a == nil || plan == nil {
		return false
	}
	return a.bus != nil && a.running
}

func marshalPlanHandoffStatusRequest(plan *DesignPlan) (string, error) {
	raw, err := json.Marshal(&agentshared.PlanHandoffStatusRequest{
		SessionID: plan.SessionID,
		PlanID:    plan.ID,
		Revision:  plan.Revision,
	})
	if err != nil {
		return "", fmt.Errorf("marshal handoff status request: %w", err)
	}
	return string(raw), nil
}

func (a *Architect) requestPlanHandoffStatus(
	ctx context.Context,
	plan *DesignPlan,
	payload string,
) (*guide.Message, error) {
	targetAgentID, err := a.requirePlanHandoffTargetAgentID(plan, correlationIDFromPending(plan.PendingWork), "plan_handoff_status_lookup")
	if err != nil {
		return nil, err
	}
	lookupCtx, cancel := context.WithTimeout(nonNilContext(ctx), planHandoffStatusLookupTimeout)
	defer cancel()
	return a.requestRouteSync(lookupCtx, &guide.RouteRequest{
		Input:         payload,
		TargetAgentID: targetAgentID,
		SessionID:     plan.SessionID,
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindPlanHandoffStatus,
		},
	})
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func decodePlanHandoffStatusMessage(msg *guide.Message) (*agentshared.PlanHandoffReceipt, bool, error) {
	resp, _, err := routeResponseFromMessage(msg)
	if err != nil {
		return nil, false, err
	}
	if !resp.Success {
		return nil, false, fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(resp.Error), "plan handoff status lookup failed"))
	}
	return decodePlanHandoffStatusResponse(resp.Data)
}

func decodePlanHandoffStatusResponse(data any) (*agentshared.PlanHandoffReceipt, bool, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, false, fmt.Errorf("marshal handoff status response: %w", err)
	}
	var decoded agentshared.PlanHandoffStatusResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false, fmt.Errorf("decode handoff status response: %w", err)
	}
	return decoded.Receipt, decoded.Found, nil
}

func (a *Architect) logDecodedPlanHandoffStatus(
	plan *DesignPlan,
	correlationID string,
	receipt *agentshared.PlanHandoffReceipt,
	found bool,
) {
	if a == nil || plan == nil {
		return
	}
	receiptFields := map[string]any{
		"plan_id":     plan.ID,
		"session_id":  plan.SessionID,
		"revision":    plan.Revision,
		"found":       found,
		"receipt_nil": receipt == nil,
	}
	if receipt != nil {
		receiptFields["receipt_id"] = receipt.ReceiptID
		receiptFields["receipt_status"] = string(receipt.Status)
		receiptFields["dag_id"] = receipt.DAGID
		receiptFields["workflow_id"] = receipt.WorkflowID
		receiptFields["task_count"] = receipt.TaskCount
		receiptFields["layer_count"] = receipt.LayerCount
		receiptFields["error_text"] = receipt.ErrorText
		receiptFields["updated_at"] = receipt.UpdatedAt
	}
	a.logInfo("planHandoffStatusLookup: orchestrator response decoded",
		"plan_id", plan.ID,
		"session_id", plan.SessionID,
		"found", found,
		"receipt_nil", receipt == nil,
		"receipt_status", receiptStatusStringForLog(receipt),
		"dag_id", receiptDAGIDForLog(receipt),
		"receipt_id", receiptIDForLog(receipt))
	a.logTrace("architect_plan_handoff_status_decoded", "debug", plan.SessionID, correlationID, agentlog.EventTaskDispatched, receiptFields)
}

func receiptStatusStringForLog(r *agentshared.PlanHandoffReceipt) string {
	if r == nil {
		return ""
	}
	return string(r.Status)
}

func receiptDAGIDForLog(r *agentshared.PlanHandoffReceipt) string {
	if r == nil {
		return ""
	}
	return r.DAGID
}

func receiptIDForLog(r *agentshared.PlanHandoffReceipt) string {
	if r == nil {
		return ""
	}
	return r.ReceiptID
}

func (a *Architect) requestOpenPlanHandoffReceiptResyncs(reason string) {
	if !canRequestOpenPlanHandoffReceiptResyncs(a) {
		return
	}
	for _, plan := range a.planStore.Snapshot() {
		if !planNeedsPlanHandoffResync(plan) {
			continue
		}
		a.requestPlanHandoffReceiptResync(plan, correlationIDFromPending(plan.PendingWork), reason)
	}
}

func canRequestOpenPlanHandoffReceiptResyncs(a *Architect) bool {
	if a == nil {
		return false
	}
	return a.planStore != nil
}

func planNeedsPlanHandoffResync(plan *DesignPlan) bool {
	if plan == nil {
		return false
	}
	if plan.PendingWork != nil && plan.PendingWork.Kind == string(continuationKindPlanHandoff) {
		return true
	}
	return plan.SM().State() == PlanStatusOrchestrating
}

func (a *Architect) requestPlanHandoffReceiptResync(
	plan *DesignPlan,
	correlationID string,
	reason string,
) bool {
	if !canLookupPlanHandoffReceipt(a, plan) {
		return false
	}
	targetAgentID, err := a.requirePlanHandoffTargetAgentID(plan, correlationID, "plan_handoff_receipt_resync")
	if err != nil {
		return false
	}
	payload, err := json.Marshal(&agentshared.PlanHandoffReceiptResyncRequest{
		SessionID:     plan.SessionID,
		PlanID:        plan.ID,
		Revision:      plan.Revision,
		CorrelationID: strings.TrimSpace(correlationID),
		Reason:        strings.TrimSpace(reason),
	})
	if err != nil {
		a.logTrace("architect_plan_handoff_resync_marshal_failed", "error", plan.SessionID, correlationID, agentlog.EventError, map[string]any{
			"plan_id":  plan.ID,
			"revision": plan.Revision,
			"error":    err.Error(),
		})
		return false
	}
	req := &guide.RouteRequest{
		CorrelationID:       "handoff_resync_" + uuid.NewString(),
		ParentCorrelationID: strings.TrimSpace(correlationID),
		Input:               string(payload),
		TargetAgentID:       targetAgentID,
		SessionID:           plan.SessionID,
		FireAndForget:       true,
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindPlanHandoffReceiptResync,
		},
	}
	if err := a.publishRouteRequest(req); err != nil {
		a.logTrace("architect_plan_handoff_resync_publish_failed", "warn", plan.SessionID, correlationID, agentlog.EventError, map[string]any{
			"plan_id":  plan.ID,
			"revision": plan.Revision,
			"reason":   reason,
			"error":    err.Error(),
		})
		return false
	}
	a.logTrace("architect_plan_handoff_resync_requested", "debug", plan.SessionID, correlationID, agentlog.EventTaskDispatched, map[string]any{
		"plan_id":  plan.ID,
		"revision": plan.Revision,
		"reason":   reason,
	})
	return true
}

func (a *Architect) reconcileDurablePlanHandoff(
	ctx context.Context,
	plan *DesignPlan,
	correlationID string,
	reason string,
) bool {
	if !canReconcileDurablePlanHandoff(a, plan) {
		return false
	}
	a.logInfo("reconcileDurablePlanHandoff: looking up receipt from orchestrator",
		"plan_id", plan.ID,
		"session_id", plan.SessionID,
		"correlation_id", correlationID,
		"reason", reason,
		"plan_state", plan.SM().State().String())
	receipt, found, err := a.lookupPlanHandoffReceipt(ctx, plan)
	if err != nil {
		a.logPlanHandoffReceiptLookupFailure(plan, correlationID, reason, err)
		return false
	}
	a.logDecodedPlanHandoffStatus(plan, correlationID, receipt, found)
	if isMissingPlanHandoffReceipt(found, receipt) {
		a.logInfo("reconcileDurablePlanHandoff: no receipt found; scheduling retry",
			"plan_id", plan.ID,
			"session_id", plan.SessionID,
			"correlation_id", correlationID,
			"reason", reason)
		a.recoverPlanHandoffForRetry(plan, correlationID, reason)
		return true
	}
	a.logPlanHandoffReceiptFound(plan, correlationID, receipt)
	return a.applyPlanHandoffReceipt(plan, receipt, correlationID, reason)
}

func canReconcileDurablePlanHandoff(a *Architect, plan *DesignPlan) bool {
	if a == nil {
		return false
	}
	return plan != nil
}

func isMissingPlanHandoffReceipt(found bool, receipt *agentshared.PlanHandoffReceipt) bool {
	if !found {
		return true
	}
	return receipt == nil
}

func (a *Architect) logPlanHandoffReceiptLookupFailure(
	plan *DesignPlan,
	correlationID string,
	reason string,
	err error,
) {
	a.logTrace("architect_plan_handoff_receipt_lookup_failed", "warn", plan.SessionID, correlationID, agentlog.EventError, map[string]any{
		"plan_id":  plan.ID,
		"revision": plan.Revision,
		"reason":   reason,
		"error":    err.Error(),
	})
}

func (a *Architect) logPlanHandoffReceiptFound(
	plan *DesignPlan,
	correlationID string,
	receipt *agentshared.PlanHandoffReceipt,
) {
	a.logTrace("architect_plan_handoff_receipt_found", "debug", plan.SessionID, correlationID, agentlog.EventTaskDispatched, map[string]any{
		"plan_id":        plan.ID,
		"revision":       plan.Revision,
		"receipt_id":     receipt.ReceiptID,
		"receipt_status": receipt.Status,
		"dag_id":         receipt.DAGID,
	})
}

func (a *Architect) applyPlanHandoffReceipt(
	plan *DesignPlan,
	receipt *agentshared.PlanHandoffReceipt,
	correlationID string,
	reason string,
) bool {
	if isPendingPlanHandoffReceiptStatus(receipt.Status) {
		a.refreshPlanPendingFromReceipt(plan, receipt, correlationID)
		return true
	}
	if isExecutingPlanHandoffReceiptStatus(receipt.Status) {
		a.promotePlanExecutingFromReceipt(plan, receipt, correlationID)
		return true
	}
	if receipt.Status == agentshared.PlanHandoffReceiptStatusFailed {
		a.recoverPlanHandoffForRetry(plan, correlationID, firstNonEmpty(strings.TrimSpace(receipt.ErrorText), reason))
		return true
	}
	return false
}

func isPendingPlanHandoffReceiptStatus(status agentshared.PlanHandoffReceiptStatus) bool {
	_, ok := pendingPlanHandoffReceiptStatuses[status]
	return ok
}

func isExecutingPlanHandoffReceiptStatus(status agentshared.PlanHandoffReceiptStatus) bool {
	_, ok := executingPlanHandoffReceiptStatuses[status]
	return ok
}

func (a *Architect) refreshPlanPendingFromReceipt(
	plan *DesignPlan,
	receipt *agentshared.PlanHandoffReceipt,
	correlationID string,
) {
	if !canApplyPlanHandoffReceipt(a, plan, receipt) {
		return
	}
	if err := a.transitionPlanToOrchestratingFromReceipt(plan); err != nil {
		a.logPlanHandoffReceiptTransitionFailure(plan, receipt, correlationID, err)
		return
	}
	pending := ensurePendingContinuation(plan)
	pending.Kind = string(continuationKindPlanHandoff)
	pending.Status = string(continuationStatusPending)
	pending.TargetAgentID = a.planHandoffTargetAgentID(plan)
	pending.CorrelationID = correlationID
	pending.Message = pendingMessageForReceipt(receipt)
	pending.ExpiresAt = time.Time{}
	plan.UpdatedAt = time.Now().UTC()
	plan.Status = plan.SM().State()
	a.persistPlanStateBestEffort(plan, correlationID, "refreshed from durable handoff receipt")
}

func (a *Architect) transitionPlanToOrchestratingFromReceipt(plan *DesignPlan) error {
	state := plan.SM().State()
	if state == PlanStatusOrchestrating || state == PlanStatusExecuting {
		return nil
	}
	return plan.SM().TransitionTo(PlanStatusOrchestrating, plan)
}

func canApplyPlanHandoffReceipt(a *Architect, plan *DesignPlan, receipt *agentshared.PlanHandoffReceipt) bool {
	if a == nil || plan == nil {
		return false
	}
	return receipt != nil
}

func ensurePendingContinuation(plan *DesignPlan) *PendingContinuation {
	if plan.PendingWork == nil {
		plan.PendingWork = &PendingContinuation{}
	}
	return plan.PendingWork
}

func (a *Architect) promotePlanExecutingFromReceipt(
	plan *DesignPlan,
	receipt *agentshared.PlanHandoffReceipt,
	correlationID string,
) {
	if !canApplyPlanHandoffReceipt(a, plan, receipt) {
		return
	}
	message := firstNonEmpty(pendingMessageForExecutingReceipt(receipt), "Plan dispatched to the orchestrator.")
	receiptAge := time.Duration(0)
	if !receipt.UpdatedAt.IsZero() {
		receiptAge = time.Since(receipt.UpdatedAt)
	}
	staleSuspect := receiptAge > 5*time.Minute
	a.logInfo("promotePlanExecutingFromReceipt: ABOUT TO EMIT 'Plan dispatched' message from DURABLE RECEIPT (not a fresh dispatch)",
		"plan_id", plan.ID,
		"session_id", plan.SessionID,
		"correlation_id", correlationID,
		"receipt_id", receipt.ReceiptID,
		"receipt_status", string(receipt.Status),
		"receipt_dag_id", receipt.DAGID,
		"receipt_workflow_id", receipt.WorkflowID,
		"receipt_updated_at", receipt.UpdatedAt,
		"receipt_age_seconds", receiptAge.Seconds(),
		"stale_suspect", staleSuspect,
		"plan_state_before", plan.SM().State().String(),
		"notification_message", message,
		"source", "durable_receipt_promotion")
	a.logTrace("architect_plan_handoff_promote_from_durable_receipt", "info", plan.SessionID, correlationID, agentlog.EventTaskDispatched, map[string]any{
		"plan_id":              plan.ID,
		"receipt_id":           receipt.ReceiptID,
		"receipt_status":       string(receipt.Status),
		"receipt_dag_id":       receipt.DAGID,
		"receipt_workflow_id":  receipt.WorkflowID,
		"receipt_updated_at":   receipt.UpdatedAt,
		"receipt_age_seconds":  receiptAge.Seconds(),
		"stale_suspect":        staleSuspect,
		"plan_state_before":    plan.SM().State().String(),
		"notification_message": message,
		"source":               "durable_receipt_promotion",
	})
	if staleSuspect {
		a.logWarn("promotePlanExecutingFromReceipt: STALE-RECEIPT SUSPECTED — receipt is older than 5 minutes but status claims Executing; the orchestrator may be returning a ghost receipt",
			"plan_id", plan.ID,
			"receipt_dag_id", receipt.DAGID,
			"receipt_age_seconds", receiptAge.Seconds(),
			"receipt_updated_at", receipt.UpdatedAt)
	}
	if err := a.finalizePlanHandoffExecution(
		plan,
		nil,
		correlationID,
		marshalReceiptJSON(receipt),
		message,
		"",
		"promoted from durable handoff receipt",
	); err != nil {
		a.logPlanHandoffReceiptTransitionFailure(plan, receipt, correlationID, err)
		return
	}
}

func (a *Architect) transitionPlanToExecutingFromHandoff(
	plan *DesignPlan,
	correlationID string,
) error {
	state := plan.SM().State()
	if state == PlanStatusExecuting {
		return nil
	}
	if state != PlanStatusOrchestrating {
		if err := a.transitionPlanToOrchestratingFromReceipt(plan); err != nil {
			return err
		}
	}
	return plan.SM().TransitionTo(PlanStatusExecuting, plan)
}

func (a *Architect) logPlanHandoffReceiptTransitionFailure(
	plan *DesignPlan,
	receipt *agentshared.PlanHandoffReceipt,
	correlationID string,
	err error,
) {
	a.logTrace("architect_plan_handoff_receipt_transition_failed", "error", plan.SessionID, correlationID, agentlog.EventError, map[string]any{
		"plan_id":        plan.ID,
		"receipt_status": receipt.Status,
		"error":          err.Error(),
	})
}

func (a *Architect) grantPlanExecutingLease(plan *DesignPlan) {
	if a == nil || a.planStore == nil {
		return
	}
	if lm := a.planStore.LeaseManager(); lm != nil {
		lm.GrantExecutingLease(plan, "orchestrator")
	}
}

func marshalReceiptJSON(receipt *agentshared.PlanHandoffReceipt) string {
	if receipt == nil {
		return ""
	}
	responseJSON, _ := json.Marshal(receipt)
	return string(responseJSON)
}

type continuationCompletionOutcome struct {
	found     bool
	completed bool
}

func (a *Architect) finalizePlanHandoffExecution(
	plan *DesignPlan,
	record *ArchitectContinuation,
	correlationID string,
	responseJSON string,
	notification string,
	riskSummary string,
	persistReason string,
) error {
	if a == nil || plan == nil {
		return nil
	}
	a.logInfo("finalizePlanHandoffExecution: entry",
		"plan_id", plan.ID,
		"session_id", plan.SessionID,
		"correlation_id", correlationID,
		"plan_state_before", plan.SM().State().String(),
		"notification_message", notification,
		"persist_reason", persistReason,
		"has_continuation_record", record != nil)
	if err := a.transitionPlanToExecutingFromHandoff(plan, correlationID); err != nil {
		a.logWarn("finalizePlanHandoffExecution: transition to Executing failed",
			"plan_id", plan.ID,
			"error", err.Error(),
			"persist_reason", persistReason)
		return err
	}
	plan.Status = plan.SM().State()
	plan.Epoch = plan.SM().Epoch()
	plan.PendingWork = nil
	plan.UpdatedAt = time.Now().UTC()
	if summary := strings.TrimSpace(riskSummary); summary != "" {
		plan.RiskSummary = append(plan.RiskSummary, summary)
	}
	a.grantPlanExecutingLease(plan)
	if err := a.persistPlanState(plan); err != nil {
		return err
	}
	outcome, err := a.completePlanHandoffContinuation(record, correlationID, plan.SessionID, responseJSON)
	if err != nil {
		return err
	}
	if outcome.completed || !outcome.found {
		a.logInfo("finalizePlanHandoffExecution: publishing 'Plan dispatched' notification to user",
			"plan_id", plan.ID,
			"session_id", plan.SessionID,
			"correlation_id", correlationID,
			"notification_message", notification,
			"persist_reason", persistReason,
			"continuation_completed", outcome.completed,
			"continuation_found", outcome.found)
		a.logTrace("architect_plan_handoff_notification_published", "info", plan.SessionID, correlationID, agentlog.EventTaskDispatched, map[string]any{
			"plan_id":                plan.ID,
			"notification_message":   notification,
			"persist_reason":         persistReason,
			"continuation_completed": outcome.completed,
			"continuation_found":     outcome.found,
		})
		a.publishNotificationPush(notification)
	} else {
		a.logInfo("finalizePlanHandoffExecution: skipping notification (continuation already handled the push)",
			"plan_id", plan.ID,
			"correlation_id", correlationID,
			"continuation_completed", outcome.completed,
			"continuation_found", outcome.found)
	}
	return nil
}

func (a *Architect) completePlanHandoffContinuation(
	record *ArchitectContinuation,
	correlationID, sessionID string,
	responseJSON string,
) (continuationCompletionOutcome, error) {
	if record != nil {
		completed, err := a.controlStore.CompleteContinuationIfActive(record, continuationStatusCompleted, responseJSON, "")
		if err != nil {
			return continuationCompletionOutcome{found: true}, err
		}
		return continuationCompletionOutcome{found: true, completed: completed}, nil
	}
	return a.completeContinuationByCorrelation(
		correlationID,
		sessionID,
		continuationStatusCompleted,
		responseJSON,
		"",
		"plan handoff reached execution",
	)
}

func pendingMessageForReceipt(receipt *agentshared.PlanHandoffReceipt) string {
	if receipt == nil {
		return "That plan is already being handed off to the orchestrator. I'll update you when it confirms ingestion."
	}
	switch receipt.Status {
	case agentshared.PlanHandoffReceiptStatusAccepted:
		return "The orchestrator accepted the plan handoff and is preparing the DAG."
	case agentshared.PlanHandoffReceiptStatusDAGBuilt:
		return "The orchestrator built the DAG and is preparing execution."
	default:
		return "That plan is already being handed off to the orchestrator. I'll update you when it confirms ingestion."
	}
}

func (a *Architect) completeContinuationByCorrelationBestEffort(
	correlationID, sessionID string,
	state continuationStatus,
	responseJSON string,
	errorText string,
	reason string,
) {
	_, err := a.completeContinuationByCorrelation(correlationID, sessionID, state, responseJSON, errorText, reason)
	if err == nil {
		return
	}
	a.logTrace("architect_continuation_lookup_failed", "error", sessionID, correlationID, agentlog.EventError, map[string]any{
		"reason": reason,
		"error":  err.Error(),
	})
}

func (a *Architect) completeContinuationByCorrelation(
	correlationID, sessionID string,
	state continuationStatus,
	responseJSON string,
	errorText string,
	reason string,
) (continuationCompletionOutcome, error) {
	if !canCompleteContinuationByCorrelation(a, correlationID) {
		return continuationCompletionOutcome{}, nil
	}
	record, err := a.lookupContinuationByCorrelation(correlationID)
	if err != nil {
		return continuationCompletionOutcome{}, err
	}
	if record == nil {
		return continuationCompletionOutcome{}, nil
	}
	completed, err := a.controlStore.CompleteContinuationIfActive(record, state, responseJSON, errorText)
	if err != nil {
		return continuationCompletionOutcome{found: true}, err
	}
	return continuationCompletionOutcome{found: true, completed: completed}, nil
}

func canCompleteContinuationByCorrelation(a *Architect, correlationID string) bool {
	if a == nil || a.controlStore == nil {
		return false
	}
	return strings.TrimSpace(correlationID) != ""
}

func (a *Architect) lookupContinuationByCorrelation(correlationID string) (*ArchitectContinuation, error) {
	return a.controlStore.GetContinuationByResponseCorrelation(correlationID)
}

func decodePlanHandoffReceiptUpdateInput(raw string) (*agentshared.PlanHandoffReceiptUpdate, error) {
	var update agentshared.PlanHandoffReceiptUpdate
	if err := json.Unmarshal([]byte(raw), &update); err != nil {
		return nil, fmt.Errorf("decode plan handoff receipt update: %w", err)
	}
	update.SessionID = strings.TrimSpace(update.SessionID)
	update.PlanID = strings.TrimSpace(update.PlanID)
	update.CorrelationID = strings.TrimSpace(update.CorrelationID)
	update.ErrorText = strings.TrimSpace(update.ErrorText)
	update.Reason = strings.TrimSpace(update.Reason)
	if update.SessionID == "" || update.PlanID == "" {
		return nil, fmt.Errorf("session_id and plan_id are required")
	}
	return &update, nil
}

func (a *Architect) handlePlanHandoffReceiptUpdate(
	_ context.Context,
	fwd *guide.ForwardedRequest,
) (any, error) {
	update, err := decodePlanHandoffReceiptUpdateInput(fwd.Input)
	if err != nil {
		return nil, err
	}
	a.applyPlanHandoffReceiptUpdate(update)
	return map[string]any{"applied": true}, nil
}

func (a *Architect) applyPlanHandoffReceiptUpdate(update *agentshared.PlanHandoffReceiptUpdate) {
	plan := a.planForPlanHandoffReceiptUpdate(update)
	if plan == nil {
		a.logTrace("architect_plan_handoff_receipt_update_plan_missing", "warn", updateSessionID(update), updateCorrelationID(update), agentlog.EventError, map[string]any{
			"plan_id":  updatePlanID(update),
			"revision": updateRevision(update),
		})
		return
	}
	correlationID := firstNonEmpty(strings.TrimSpace(update.CorrelationID), correlationIDFromPending(plan.PendingWork))
	a.recordPlanHandoffReceiptUpdateEvent(correlationID, update)
	a.applyPlanHandoffReceiptUpdateToPlan(plan, correlationID, update)
}

func (a *Architect) planForPlanHandoffReceiptUpdate(update *agentshared.PlanHandoffReceiptUpdate) *DesignPlan {
	if !canResolvePlanForReceiptUpdate(a, update) {
		return nil
	}
	plan := a.planStore.Get(update.PlanID)
	if plan != nil {
		return plan
	}
	return a.restorePlanForPlanHandoffReceiptUpdate(update.PlanID)
}

func (a *Architect) recordPlanHandoffReceiptUpdateEvent(
	correlationID string,
	update *agentshared.PlanHandoffReceiptUpdate,
) {
	if !canRecordPlanHandoffReceiptUpdateEvent(a, correlationID, update) {
		return
	}
	record, err := a.lookupContinuationByCorrelation(correlationID)
	if err != nil {
		return
	}
	if record == nil {
		return
	}
	payloadJSON, _ := json.Marshal(update)
	_ = a.controlStore.appendContinuationEvent(
		record.ID,
		firstNonEmpty(receiptStatusFromReceiptUpdate(update), "update"),
		firstNonEmpty(update.Reason, "plan handoff receipt update"),
		string(payloadJSON),
	)
}

func (a *Architect) applyPlanHandoffReceiptUpdateToPlan(
	plan *DesignPlan,
	correlationID string,
	update *agentshared.PlanHandoffReceiptUpdate,
) {
	if planHandoffReceiptUpdateMissing(update) {
		a.applyMissingPlanHandoffReceiptUpdate(plan, correlationID, update)
		return
	}
	a.applyFoundPlanHandoffReceiptUpdate(plan, correlationID, update)
}

func planHandoffReceiptUpdateMissing(update *agentshared.PlanHandoffReceiptUpdate) bool {
	if update == nil || !update.Found {
		return true
	}
	return update.Receipt == nil
}

func (a *Architect) applyMissingPlanHandoffReceiptUpdate(
	plan *DesignPlan,
	correlationID string,
	update *agentshared.PlanHandoffReceiptUpdate,
) {
	reason := firstNonEmpty(update.ErrorText, update.Reason, "no durable handoff receipt found")
	a.recoverPlanHandoffForRetry(plan, correlationID, reason)
	a.publishNotificationPush("I couldn't confirm orchestrator handoff state. The plan is ready to retry.")
}

func (a *Architect) applyFoundPlanHandoffReceiptUpdate(
	plan *DesignPlan,
	correlationID string,
	update *agentshared.PlanHandoffReceiptUpdate,
) {
	if isPendingPlanHandoffReceiptStatus(update.Receipt.Status) {
		a.refreshPlanPendingFromReceipt(plan, update.Receipt, correlationID)
		return
	}
	if isExecutingPlanHandoffReceiptStatus(update.Receipt.Status) {
		a.promotePlanExecutingFromReceipt(plan, update.Receipt, correlationID)
		return
	}
	if update.Receipt.Status == agentshared.PlanHandoffReceiptStatusFailed {
		a.recoverPlanHandoffForRetry(plan, correlationID, firstNonEmpty(strings.TrimSpace(update.Receipt.ErrorText), update.Reason, "plan handoff failed"))
		a.publishNotificationPush("I couldn't dispatch the plan to the orchestrator: " + firstNonEmpty(strings.TrimSpace(update.Receipt.ErrorText), "handoff failed"))
	}
}

func canResolvePlanForReceiptUpdate(a *Architect, update *agentshared.PlanHandoffReceiptUpdate) bool {
	if a == nil || a.planStore == nil {
		return false
	}
	return update != nil
}

func (a *Architect) restorePlanForPlanHandoffReceiptUpdate(planID string) *DesignPlan {
	if a.controlStore == nil {
		return nil
	}
	plan, err := a.controlStore.GetPlan(planID)
	if err != nil || plan == nil {
		return nil
	}
	_ = a.planStore.Upsert(plan)
	return plan
}

func canRecordPlanHandoffReceiptUpdateEvent(
	a *Architect,
	correlationID string,
	update *agentshared.PlanHandoffReceiptUpdate,
) bool {
	if a == nil || a.controlStore == nil || update == nil {
		return false
	}
	return correlationID != ""
}

func receiptStatusFromReceiptUpdate(update *agentshared.PlanHandoffReceiptUpdate) string {
	if update == nil || update.Receipt == nil {
		return ""
	}
	return string(update.Receipt.Status)
}

func pendingMessageForExecutingReceipt(receipt *agentshared.PlanHandoffReceipt) string {
	if receipt == nil {
		return ""
	}
	if strings.TrimSpace(receipt.DAGID) == "" {
		return "Plan dispatched to the orchestrator."
	}
	return fmt.Sprintf("Plan dispatched to the orchestrator. DAG %s is now running.", receipt.DAGID)
}

func updateSessionID(update *agentshared.PlanHandoffReceiptUpdate) string {
	if update == nil {
		return ""
	}
	return update.SessionID
}

func updateCorrelationID(update *agentshared.PlanHandoffReceiptUpdate) string {
	if update == nil {
		return ""
	}
	return update.CorrelationID
}

func updatePlanID(update *agentshared.PlanHandoffReceiptUpdate) string {
	if update == nil {
		return ""
	}
	return update.PlanID
}

func updateRevision(update *agentshared.PlanHandoffReceiptUpdate) int {
	if update == nil {
		return 0
	}
	return update.Revision
}
