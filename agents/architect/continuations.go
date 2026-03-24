package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

func (a *Architect) hasPendingBusWait(correlationID string) bool {
	a.pendingMu.Lock()
	_, ok := a.pendingBus[correlationID]
	a.pendingMu.Unlock()
	return ok
}

func (a *Architect) recordPendingContinuation(
	plan *DesignPlan,
	record *ArchitectContinuation,
	message string,
) error {
	if a == nil || a.controlStore == nil || record == nil {
		return fmt.Errorf("architect continuation store is not configured")
	}
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = "cont_" + uuid.NewString()
	}
	if record.State == "" {
		record.State = continuationStatusPending
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if err := a.controlStore.PutContinuation(record); err != nil {
		return err
	}
	if plan == nil {
		return nil
	}
	plan.PendingWork = &PendingContinuation{
		Kind:          string(record.Kind),
		Status:        string(record.State),
		TargetAgentID: record.TargetAgentID,
		CorrelationID: record.ResponseCorrelationID,
		ToolName:      record.ToolName,
		Message:       strings.TrimSpace(message),
		CreatedAt:     record.CreatedAt,
		ExpiresAt:     record.ExpiresAt,
	}
	plan.UpdatedAt = now
	return a.persistPlanState(plan)
}

func pendingContinuationFromRecord(record *ArchitectContinuation) *PendingContinuation {
	if record == nil {
		return nil
	}
	return &PendingContinuation{
		Kind:          string(record.Kind),
		Status:        string(record.State),
		TargetAgentID: record.TargetAgentID,
		CorrelationID: record.ResponseCorrelationID,
		ToolName:      record.ToolName,
		CreatedAt:     record.CreatedAt,
		ExpiresAt:     record.ExpiresAt,
	}
}

func (a *Architect) reconcilePlanPendingContinuation(plan *DesignPlan, record *ArchitectContinuation) (*DesignPlan, error) {
	if a == nil || plan == nil || record == nil {
		return plan, nil
	}
	now := time.Now().UTC()
	if !recordIsActive(record, now) {
		return plan, nil
	}
	if plan.PendingWork != nil &&
		plan.PendingWork.CorrelationID == record.ResponseCorrelationID &&
		planHasActivePendingWork(plan, now) {
		return plan, nil
	}
	if plan.PendingWork != nil && planHasActivePendingWork(plan, now) {
		currentCreated := plan.PendingWork.CreatedAt
		if !currentCreated.IsZero() && currentCreated.After(record.CreatedAt) {
			return plan, nil
		}
	}
	plan.PendingWork = pendingContinuationFromRecord(record)
	if plan.UpdatedAt.Before(record.CreatedAt) {
		plan.UpdatedAt = record.CreatedAt
	}
	return plan, a.persistPlanState(plan)
}

func recordIsActive(record *ArchitectContinuation, now time.Time) bool {
	if record == nil {
		return false
	}
	switch record.State {
	case continuationStatusPending, continuationStatusProcessing:
	default:
		return false
	}
	if !record.ExpiresAt.IsZero() && now.After(record.ExpiresAt) {
		return false
	}
	return true
}

func (a *Architect) restorePlanForContinuation(record *ArchitectContinuation) *DesignPlan {
	if a == nil || a.controlStore == nil || a.planStore == nil || record == nil || strings.TrimSpace(record.PlanID) == "" {
		return nil
	}
	plan, err := a.controlStore.GetPlan(record.PlanID)
	if err != nil || plan == nil {
		return nil
	}
	if err := a.planStore.Upsert(plan); err != nil {
		return nil
	}
	return plan
}

func (a *Architect) reconcileContinuationRecord(record *ArchitectContinuation) *DesignPlan {
	if a == nil || record == nil || strings.TrimSpace(record.PlanID) == "" {
		return nil
	}
	plan := a.planForContinuation(record)
	if plan == nil {
		plan = a.restorePlanForContinuation(record)
	}
	if plan == nil {
		return nil
	}
	reconciled, err := a.reconcilePlanPendingContinuation(plan, record)
	if err != nil {
		return plan
	}
	return reconciled
}

func (a *Architect) reconcilePendingContinuations(ctx context.Context) {
	if !canReconcilePendingContinuations(a) {
		return
	}
	now := time.Now().UTC()
	records, err := a.controlStore.ListActiveContinuations(now)
	if err != nil {
		a.logWarn("reconcilePendingContinuations: list failed", "error", err)
		return
	}
	for _, record := range records {
		a.reconcilePendingContinuationRecord(record)
	}
	a.recoverAllSessionPendingWork(ctx, now)
}

func canReconcilePendingContinuations(a *Architect) bool {
	if a == nil {
		return false
	}
	return a.controlStore != nil
}

func (a *Architect) reconcilePendingContinuationRecord(record *ArchitectContinuation) {
	if record == nil {
		return
	}
	if record.State != continuationStatusProcessing {
		a.reconcileContinuationRecord(record)
		return
	}
	if record.Kind == continuationKindPlanHandoff {
		return
	}
	a.recoverStaleContinuationRecord(record, "architect restarted while continuation was processing")
}

func (a *Architect) clearPlanPendingContinuation(plan *DesignPlan, correlationID string) error {
	if a == nil || plan == nil || plan.PendingWork == nil {
		return nil
	}
	if correlationID != "" && plan.PendingWork.CorrelationID != correlationID {
		return nil
	}
	plan.PendingWork = nil
	plan.UpdatedAt = time.Now().UTC()
	return a.persistPlanState(plan)
}

func (a *Architect) recoverAllSessionPendingWork(ctx context.Context, now time.Time) {
	if a == nil || a.planStore == nil {
		return
	}
	for _, plan := range a.planStore.Snapshot() {
		a.recoverStalePendingWork(ctx, plan, now)
	}
}

func (a *Architect) recoverSessionPendingWork(ctx context.Context, sessionID string, now time.Time) {
	if a == nil || a.planStore == nil {
		return
	}
	for _, plan := range a.planStore.AllForSession(sessionID) {
		a.recoverStalePendingWork(ctx, plan, now)
	}
}

func (a *Architect) recoverStalePendingWork(ctx context.Context, plan *DesignPlan, now time.Time) {
	if !canRecoverStalePendingWork(a, plan) {
		return
	}
	if a.handlePlanPendingWork(ctx, plan, now) {
		return
	}
	if plan.SM().State() == PlanStatusOrchestrating {
		a.reconcileOrRecoverPlanHandoff(ctx, plan, correlationIDFromPending(plan.PendingWork), "plan stuck in orchestrating without active handoff continuation")
	}
}

func canRecoverStalePendingWork(a *Architect, plan *DesignPlan) bool {
	if a == nil {
		return false
	}
	return plan != nil
}

func (a *Architect) handlePlanPendingWork(ctx context.Context, plan *DesignPlan, now time.Time) bool {
	pending := pendingWorkForPlan(plan)
	if pending == nil {
		return false
	}
	if a.handlePlanHandoffPendingWork(ctx, plan, pending, now) {
		return true
	}
	return a.handleExpiredPendingWork(plan, pending, now)
}

func (a *Architect) handlePlanHandoffPendingWork(
	ctx context.Context,
	plan *DesignPlan,
	pending *PendingContinuation,
	now time.Time,
) bool {
	if isProcessingPlanHandoff(pending) {
		a.reconcileOrRecoverPlanHandoff(ctx, plan, pending.CorrelationID, "stale processing plan handoff continuation")
		return true
	}
	if isExpiredPlanHandoff(plan, pending, now) {
		a.reconcileOrRecoverPlanHandoff(ctx, plan, pending.CorrelationID, "expired plan handoff continuation")
		return true
	}
	return false
}

func (a *Architect) handleExpiredPendingWork(
	plan *DesignPlan,
	pending *PendingContinuation,
	now time.Time,
) bool {
	if planHasActivePendingWork(plan, now) {
		return false
	}
	a.expirePendingWork(plan, pending)
	return true
}

func pendingWorkForPlan(plan *DesignPlan) *PendingContinuation {
	if plan == nil {
		return nil
	}
	return plan.PendingWork
}

func isProcessingPlanHandoff(pending *PendingContinuation) bool {
	if pending == nil || pending.Kind != string(continuationKindPlanHandoff) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(pending.Status), string(continuationStatusProcessing))
}

func isExpiredPlanHandoff(plan *DesignPlan, pending *PendingContinuation, now time.Time) bool {
	if pending == nil || pending.Kind != string(continuationKindPlanHandoff) {
		return false
	}
	return !planHasActivePendingWork(plan, now)
}

func (a *Architect) expirePendingWork(plan *DesignPlan, pending *PendingContinuation) {
	a.logTrace("architect_pending_work_expired", "warn", plan.SessionID, pending.CorrelationID, agentlog.EventError, map[string]any{
		"plan_id":       plan.ID,
		"plan_status":   plan.SM().State().String(),
		"kind":          pending.Kind,
		"pending_state": pending.Status,
	})
	a.failContinuationByCorrelationBestEffort(pending.CorrelationID, plan.SessionID, "pending continuation expired")
	a.clearPlanPendingContinuationBestEffort(plan, pending.CorrelationID, "pending continuation expired")
}

func (a *Architect) reconcileOrRecoverPlanHandoff(
	ctx context.Context,
	plan *DesignPlan,
	correlationID string,
	reason string,
) {
	if ctx != nil {
		a.requestPlanHandoffReceiptResync(plan, correlationID, reason)
	}
	if ctx != nil && a.reconcileDurablePlanHandoff(ctx, plan, correlationID, reason) {
		return
	}
	if ctx == nil {
		a.logTrace("architect_plan_handoff_recovery_deferred", "warn", plan.SessionID, correlationID, agentlog.EventError, map[string]any{
			"plan_id": plan.ID,
			"reason":  reason,
		})
		return
	}
	a.recoverPlanHandoffForRetry(plan, correlationID, reason)
}

func (a *Architect) recoverStaleContinuationRecord(record *ArchitectContinuation, reason string) {
	if a == nil || record == nil {
		return
	}

	plan := a.planForContinuation(record)
	if plan == nil {
		plan = a.restorePlanForContinuation(record)
	}

	if record.Kind == continuationKindPlanHandoff {
		a.recoverPlanHandoffForRetry(plan, record.ResponseCorrelationID, reason)
		return
	}

	a.logTrace("architect_continuation_recovered", "warn", record.SessionID, record.ResponseCorrelationID, agentlog.EventError, map[string]any{
		"continuation_id": record.ID,
		"kind":            string(record.Kind),
		"plan_id":         record.PlanID,
		"reason":          reason,
	})
	if plan != nil {
		a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, reason)
	}
	a.completeContinuationBestEffort(record, continuationStatusFailed, record.ResponseJSON, reason, reason)
}

func (a *Architect) recoverPlanHandoffForRetry(plan *DesignPlan, correlationID string, reason string) {
	sessionID, planID, planStatus := planRecoverySnapshot(plan)
	a.logTrace("architect_plan_handoff_recovered", "warn", sessionID, correlationID, agentlog.EventError, map[string]any{
		"plan_id":     planID,
		"plan_status": planStatus,
		"reason":      reason,
	})
	a.failContinuationByCorrelationBestEffort(correlationID, sessionID, reason)
	if plan == nil {
		return
	}
	if err := a.transitionRecoveredPlanHandoffToReady(plan, correlationID, reason); err != nil {
		a.logPlanHandoffRecoveryTransitionFailure(plan, correlationID, reason, err)
		return
	}
	plan.Status = plan.SM().State()
	plan.Epoch = plan.SM().Epoch()
	plan.UpdatedAt = time.Now().UTC()
	plan.PendingWork = nil
	a.persistPlanStateBestEffort(plan, correlationID, reason)
}

func planRecoverySnapshot(plan *DesignPlan) (string, string, string) {
	if plan == nil {
		return "", "", ""
	}
	return plan.SessionID, plan.ID, plan.SM().State().String()
}

func (a *Architect) transitionRecoveredPlanHandoffToReady(
	plan *DesignPlan,
	_ string,
	_ string,
) error {
	if plan.SM().State() != PlanStatusOrchestrating {
		return nil
	}
	return plan.SM().TransitionTo(PlanStatusReady, plan)
}

func (a *Architect) logPlanHandoffRecoveryTransitionFailure(
	plan *DesignPlan,
	correlationID string,
	reason string,
	err error,
) {
	a.logTrace("architect_plan_handoff_recovery_transition_failed", "error", plan.SessionID, correlationID, agentlog.EventError, map[string]any{
		"plan_id":     plan.ID,
		"plan_status": plan.SM().State().String(),
		"reason":      reason,
		"error":       err.Error(),
	})
}

func (a *Architect) failContinuationByCorrelationBestEffort(correlationID, sessionID, reason string) {
	a.completeContinuationByCorrelationBestEffort(correlationID, sessionID, continuationStatusFailed, "", reason, reason)
}

func correlationIDFromPending(pending *PendingContinuation) string {
	if pending == nil {
		return ""
	}
	return strings.TrimSpace(pending.CorrelationID)
}

func (a *Architect) publishNotificationPush(content string) {
	content = strings.TrimSpace(content)
	if content == "" || a == nil || a.bus == nil || a.channels == nil {
		return
	}

	pushID := "notif_" + uuid.NewString()
	push := &guide.AgentPush{
		PushID:   pushID,
		AgentID:  a.id,
		PushType: guide.PushTypeNotification,
		Content:  content,
	}
	stream := &guide.StreamResponse{
		CorrelationID:     pushID,
		RespondingAgentID: a.id,
		TargetAgentID:     a.id,
		Event: &guide.StreamEvent{
			Type:      guide.StreamEventPush,
			Data:      push,
			Timestamp: time.Now(),
		},
	}
	msg := &guide.Message{
		ID:            a.generateMessageID(),
		CorrelationID: pushID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: a.id,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	if err := a.bus.Publish(a.channels.Responses, msg); err != nil {
		a.logTrace("architect_notification_push_publish_failed", "error", "", pushID, agentlog.EventError, map[string]any{
			"error":   err.Error(),
			"content": content,
		})
	}
}

func (a *Architect) handleContinuationResponse(msg *guide.Message) error {
	if a == nil || a.controlStore == nil || msg == nil || strings.TrimSpace(msg.CorrelationID) == "" {
		return nil
	}
	if !isTerminalContinuationMessage(msg) {
		return nil
	}
	record, err := a.controlStore.ClaimPendingContinuationByResponseCorrelation(msg.CorrelationID)
	if err != nil || record == nil {
		return err
	}
	switch record.Kind {
	case continuationKindGuardianApproval:
		return a.handleGuardianApprovalContinuation(msg, record)
	case continuationKindAcceptanceEval:
		return a.handleAcceptanceEvaluationContinuation(msg, record)
	case continuationKindPlanHandoff:
		return a.handlePlanHandoffContinuation(msg, record)
	case continuationKindAcademicHandoff:
		return a.handleAcademicRequirementsHandoffContinuation(msg, record)
	default:
		return nil
	}
}

func (a *Architect) handleGuardianApprovalContinuation(
	msg *guide.Message,
	record *ArchitectContinuation,
) error {
	resp, respJSON, err := routeResponseFromMessage(msg)
	if err != nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "guardian approval response decode failed")
		return err
	}
	req, err := decodeGuardianControlRequest(record.RequestJSON)
	if err != nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, err.Error(), "guardian control request decode failed")
		return err
	}
	plan := a.planForContinuation(record)
	if !resp.Success {
		if plan != nil {
			a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "guardian approval denied")
		}
		msgText := fmt.Sprintf("Guardian denied %s: %s", record.ToolName, resp.Error)
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, resp.Error, "guardian approval denied")
		a.publishNotificationPush(msgText)
		return nil
	}
	grant, err := decodeGuardianControlGrant(resp.Data)
	if err != nil {
		if plan != nil {
			a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "guardian grant decode failed")
		}
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, err.Error(), "guardian grant decode failed")
		a.publishNotificationPush("Guardian returned an invalid approval response.")
		return err
	}
	if plan != nil {
		a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "guardian approval completed")
	}
	if _, activateErr := a.toolRuntime().Activate(record.ToolName); activateErr != nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, activateErr.Error(), "approved tool activation failed")
		a.publishNotificationPush("I couldn't activate the approved tool: " + activateErr.Error())
		return activateErr
	}
	execResult, execErr := a.toolRuntime().ExecuteApproved(context.Background(), toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        record.ToolCallID,
			Name:      record.ToolName,
			Arguments: record.RawArguments,
		},
		AgentID:         a.toolRuntime().AgentID(),
		CorrelationID:   req.CorrelationID,
		CapabilityScope: req.CapabilityScope,
	}, grant)
	if execErr != nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, execErr.Error(), "approved tool execution failed")
		a.publishNotificationPush("I couldn't complete the approved operation: " + execErr.Error())
		return execErr
	}
	if err := a.controlStore.CompleteContinuation(record, continuationStatusCompleted, respJSON, ""); err != nil {
		return err
	}
	if record.ToolName == "ask_user_question" {
		if message := toolOutputUserMessage(execResult.Output); message != "" {
			a.publishNotificationPush(message)
		}
	}
	return nil
}

func (a *Architect) handleAcceptanceEvaluationContinuation(
	msg *guide.Message,
	record *ArchitectContinuation,
) error {
	resp, respJSON, err := routeResponseFromMessage(msg)
	plan := a.planForContinuation(record)
	if err != nil {
		if plan != nil {
			a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "acceptance response decode failed")
		}
		a.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "acceptance response decode failed")
		return err
	}
	if plan != nil {
		a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "acceptance response received")
	}
	if !resp.Success {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, resp.Error, "acceptance evaluation unsuccessful")
		a.publishNotificationPush("I couldn't complete plan acceptance evaluation: " + strings.TrimSpace(resp.Error))
		return nil
	}
	payload, err := decodePlanAcceptancePayload(record.RequestJSON)
	if err != nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, err.Error(), "acceptance payload decode failed")
		return err
	}
	result, err := extractAcceptanceResult(msg, payload)
	if err != nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, err.Error(), "acceptance result extraction failed")
		a.publishNotificationPush("I couldn't interpret the plan acceptance response.")
		return err
	}
	if err := a.controlStore.CompleteContinuation(record, continuationStatusCompleted, respJSON, ""); err != nil {
		return err
	}
	if plan == nil {
		a.publishNotificationPush("The plan changed before acceptance evaluation completed.")
		return nil
	}
	switch acceptanceVerdict(strings.TrimSpace(strings.ToLower(result.Result))) {
	case verdictAccept:
		dispatchResult, _ := a.dispatchPlanExecution(context.Background(), &ArchitectRequest{
			ID:        uuid.NewString(),
			Intent:    IntentExecute,
			Query:     "guide-approved execution",
			SessionID: plan.SessionID,
			Timestamp: time.Now(),
		}, plan)
		if dispatchResult != nil {
			a.publishNotificationPush(dispatchResult.Response)
		}
	case verdictModify:
		a.applyPlanRevision(plan, formatModificationReason(result.Modifications), nil)
		a.publishNotificationPush(formatModifyResponse(result.Modifications))
	case verdictReject:
		reason := "plan rejected by user"
		if len(result.Modifications) > 0 {
			reason = "plan rejected: " + strings.Join(result.Modifications, "; ")
		}
		a.applyPlanRevision(plan, reason, nil)
		a.publishNotificationPush(formatRejectResponse(result.Modifications))
	default:
		a.publishNotificationPush("I couldn't determine whether the plan was accepted or needs changes.")
	}
	return nil
}

func (a *Architect) handlePlanHandoffContinuation(
	msg *guide.Message,
	record *ArchitectContinuation,
) error {
	resp, respJSON, err := routeResponseFromMessage(msg)
	plan := a.planForContinuation(record)
	if err != nil {
		if plan != nil {
			a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "plan handoff response decode failed")
		}
		a.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "plan handoff response decode failed")
		return err
	}
	if plan == nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, "plan not found", "plan handoff continuation lost plan")
		return nil
	}
	if !resp.Success || !isHandoffSuccess(msg) {
		summary := strings.TrimSpace(resp.Error)
		if summary == "" {
			summary = summarizeAutoHandoffResponse(msg)
		}
		a.recoverPlanHandoffForRetry(plan, record.ResponseCorrelationID, summary)
		a.publishNotificationPush("I couldn't dispatch the plan to the orchestrator: " + summary)
		return nil
	}
	summary := summarizeAutoHandoffResponse(msg)
	message := "Plan dispatched to the orchestrator."
	if strings.TrimSpace(summary) != "" {
		message = fmt.Sprintf("Plan dispatched to the orchestrator. %s", summary)
	}
	return a.finalizePlanHandoffExecution(
		plan,
		record,
		record.ResponseCorrelationID,
		respJSON,
		message,
		summary,
		"promoted from handoff continuation",
	)
}

func (a *Architect) handleAcademicRequirementsHandoffContinuation(
	msg *guide.Message,
	record *ArchitectContinuation,
) error {
	resp, respJSON, err := routeResponseFromMessage(msg)
	plan := a.planForContinuation(record)
	if err != nil {
		if plan != nil {
			a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "academic handoff response decode failed")
		}
		a.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "academic handoff response decode failed")
		return err
	}
	if plan != nil {
		a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "academic handoff response received")
	}
	if !resp.Success {
		summary := strings.TrimSpace(resp.Error)
		if summary == "" {
			summary = "the Academic handoff did not complete successfully"
		}
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, summary, "academic handoff unsuccessful")
		a.publishNotificationPush("I couldn't complete the Academic handoff: " + summary)
		return nil
	}
	return a.controlStore.CompleteContinuation(record, continuationStatusCompleted, respJSON, "")
}

func (a *Architect) planForContinuation(record *ArchitectContinuation) *DesignPlan {
	if a == nil || a.planStore == nil || record == nil || strings.TrimSpace(record.PlanID) == "" {
		return nil
	}
	return a.planStore.Get(record.PlanID)
}

func routeResponseFromMessage(msg *guide.Message) (*guide.RouteResponse, string, error) {
	if msg == nil {
		return nil, "", fmt.Errorf("route response message is nil")
	}
	if msg.Type == guide.MessageTypeError {
		errText, ok := msg.GetError()
		if !ok || strings.TrimSpace(errText) == "" {
			errText = "route error"
		}
		resp := &guide.RouteResponse{
			CorrelationID:     msg.CorrelationID,
			Success:           false,
			Error:             strings.TrimSpace(errText),
			RespondingAgentID: msg.SourceAgentID,
		}
		encoded, _ := json.Marshal(resp)
		return resp, string(encoded), nil
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		return nil, "", fmt.Errorf("message %q did not contain a route response", msg.CorrelationID)
	}
	encoded, _ := json.Marshal(resp)
	return resp, string(encoded), nil
}

func isTerminalContinuationMessage(msg *guide.Message) bool {
	if msg == nil {
		return false
	}
	switch msg.Type {
	case guide.MessageTypeResponse, guide.MessageTypeError:
		return true
	default:
		return false
	}
}

func decodeGuardianControlRequest(raw string) (toolruntime.GuardianControlRequest, error) {
	var req toolruntime.GuardianControlRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return req, fmt.Errorf("decode guardian control request: %w", err)
	}
	return req, nil
}

func decodeGuardianControlGrant(data any) (*toolruntime.GuardianControlGrant, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal guardian control grant: %w", err)
	}
	var grant toolruntime.GuardianControlGrant
	if err := json.Unmarshal(encoded, &grant); err != nil {
		return nil, fmt.Errorf("decode guardian control grant: %w", err)
	}
	return &grant, nil
}

func decodePlanAcceptancePayload(raw string) (*planAcceptancePayload, error) {
	var payload planAcceptancePayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode plan acceptance payload: %w", err)
	}
	return &payload, nil
}

func toolOutputUserMessage(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return output
	}
	message, _ := payload["user_message"].(string)
	return strings.TrimSpace(message)
}
