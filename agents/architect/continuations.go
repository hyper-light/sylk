package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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
	_ = a.bus.Publish(a.channels.Responses, msg)
}

func (a *Architect) handleContinuationResponse(msg *guide.Message) error {
	if a == nil || a.controlStore == nil || msg == nil || strings.TrimSpace(msg.CorrelationID) == "" {
		return nil
	}
	record, err := a.controlStore.GetContinuationByResponseCorrelation(msg.CorrelationID)
	if err != nil || record == nil || record.State != continuationStatusPending {
		return err
	}
	switch record.Kind {
	case continuationKindGuardianApproval:
		return a.handleGuardianApprovalContinuation(msg, record)
	case continuationKindAcceptanceEval:
		return a.handleAcceptanceEvaluationContinuation(msg, record)
	case continuationKindPlanHandoff:
		return a.handlePlanHandoffContinuation(msg, record)
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
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, "", err.Error())
		return err
	}
	req, err := decodeGuardianControlRequest(record.RequestJSON)
	if err != nil {
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, err.Error())
		return err
	}
	plan := a.planForContinuation(record)
	if !resp.Success {
		if plan != nil {
			_ = a.clearPlanPendingContinuation(plan, record.ResponseCorrelationID)
		}
		msgText := fmt.Sprintf("Guardian denied %s: %s", record.ToolName, resp.Error)
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, resp.Error)
		a.publishNotificationPush(msgText)
		return nil
	}
	grant, err := decodeGuardianControlGrant(resp.Data)
	if err != nil {
		if plan != nil {
			_ = a.clearPlanPendingContinuation(plan, record.ResponseCorrelationID)
		}
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, err.Error())
		a.publishNotificationPush("Guardian returned an invalid approval response.")
		return err
	}
	if plan != nil {
		_ = a.clearPlanPendingContinuation(plan, record.ResponseCorrelationID)
	}
	if _, activateErr := a.toolRuntime().Activate(record.ToolName); activateErr != nil {
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, activateErr.Error())
		a.publishNotificationPush("I couldn't activate the approved tool: " + activateErr.Error())
		return activateErr
	}
	execResult, execErr := a.toolRuntime().ExecuteApproved(context.Background(), toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        record.ToolCallID,
			Name:      record.ToolName,
			Arguments: record.RawArguments,
		},
		AgentID:         a.id,
		CorrelationID:   req.CorrelationID,
		CapabilityScope: req.CapabilityScope,
	}, grant)
	if execErr != nil {
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, execErr.Error())
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
			_ = a.clearPlanPendingContinuation(plan, record.ResponseCorrelationID)
		}
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, "", err.Error())
		return err
	}
	if plan != nil {
		_ = a.clearPlanPendingContinuation(plan, record.ResponseCorrelationID)
	}
	if !resp.Success {
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, resp.Error)
		a.publishNotificationPush("I couldn't complete plan acceptance evaluation: " + strings.TrimSpace(resp.Error))
		return nil
	}
	payload, err := decodePlanAcceptancePayload(record.RequestJSON)
	if err != nil {
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, err.Error())
		return err
	}
	result, err := extractAcceptanceResult(msg, payload)
	if err != nil {
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, err.Error())
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
			_ = a.clearPlanPendingContinuation(plan, record.ResponseCorrelationID)
		}
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, "", err.Error())
		return err
	}
	if plan == nil {
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, "plan not found")
		return nil
	}
	_ = a.clearPlanPendingContinuation(plan, record.ResponseCorrelationID)
	if !resp.Success || !isHandoffSuccess(msg) {
		if plan.SM().State() == PlanStatusOrchestrating {
			if transitionErr := plan.SM().TransitionTo(PlanStatusReady, plan); transitionErr == nil {
				plan.Status = plan.SM().State()
				plan.Epoch = plan.SM().Epoch()
			}
		}
		plan.UpdatedAt = time.Now().UTC()
		_ = a.persistPlanState(plan)
		summary := strings.TrimSpace(resp.Error)
		if summary == "" {
			summary = summarizeAutoHandoffResponse(msg)
		}
		_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, summary)
		a.publishNotificationPush("I couldn't dispatch the plan to the orchestrator: " + summary)
		return nil
	}
	if plan.SM().State() == PlanStatusOrchestrating {
		if transitionErr := plan.SM().TransitionTo(PlanStatusExecuting, plan); transitionErr != nil {
			_ = a.controlStore.CompleteContinuation(record, continuationStatusFailed, respJSON, transitionErr.Error())
			return transitionErr
		}
	}
	plan.Status = plan.SM().State()
	plan.Epoch = plan.SM().Epoch()
	plan.UpdatedAt = time.Now().UTC()
	if lm := a.planStore.LeaseManager(); lm != nil {
		lm.GrantExecutingLease(plan, "orchestrator")
	}
	summary := summarizeAutoHandoffResponse(msg)
	if strings.TrimSpace(summary) != "" {
		plan.RiskSummary = append(plan.RiskSummary, summary)
	}
	if err := a.persistPlanState(plan); err != nil {
		return err
	}
	if err := a.controlStore.CompleteContinuation(record, continuationStatusCompleted, respJSON, ""); err != nil {
		return err
	}
	message := "Plan dispatched to the orchestrator."
	if strings.TrimSpace(summary) != "" {
		message = fmt.Sprintf("Plan dispatched to the orchestrator. %s", summary)
	}
	a.publishNotificationPush(message)
	return nil
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
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		return nil, "", fmt.Errorf("message %q did not contain a route response", msg.CorrelationID)
	}
	encoded, _ := json.Marshal(resp)
	return resp, string(encoded), nil
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
