package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
	"github.com/google/uuid"
)

func controlPlaneKindFromMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	if kind, ok := metadata["control_plane_kind"].(string); ok {
		return strings.TrimSpace(kind)
	}
	return ""
}

func (o *Orchestrator) handleControlPlaneForward(ctx context.Context, fwd *guide.ForwardedRequest) (any, bool, error) {
	switch controlPlaneKindFromMetadata(fwd.Metadata) {
	case agentshared.ControlPlaneKindValidationVerdict:
		result, err := o.handleValidationVerdictForward(ctx, fwd)
		return result, true, err
	case agentshared.ControlPlaneKindPlanHandoffStatus:
		result, err := o.handlePlanHandoffStatusForward(ctx, fwd)
		return result, true, err
	case agentshared.ControlPlaneKindPlanHandoffReceiptResync:
		result, err := o.handlePlanHandoffReceiptResyncForward(ctx, fwd)
		return result, true, err
	default:
		return nil, false, nil
	}
}

func decodeValidationVerdictInput(raw string) (*agentshared.ValidationVerdictPayload, error) {
	var payload agentshared.ValidationVerdictPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode validation verdict: %w", err)
	}
	return &payload, nil
}

func remediationResultFromRouteResponse(resp *guide.RouteResponse) (*agentshared.RemediationResult, error) {
	if resp == nil {
		return nil, fmt.Errorf("missing remediation response")
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", firstNonEmpty(resp.Error, "architect remediation failed"))
	}
	switch typed := resp.Data.(type) {
	case *agentshared.RemediationResult:
		return typed, nil
	case agentshared.RemediationResult:
		copy := typed
		return &copy, nil
	case map[string]any:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("marshal remediation response: %w", err)
		}
		var result agentshared.RemediationResult
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("decode remediation response: %w", err)
		}
		return &result, nil
	default:
		return nil, fmt.Errorf("unexpected remediation response type %T", resp.Data)
	}
}

func statusForVerdict(kind agentshared.ValidationVerdictKind) ValidationEpochStatus {
	switch kind {
	case agentshared.ValidationVerdictPass, agentshared.ValidationVerdictPassWithFindings:
		return ValidationEpochStatusPassed
	case agentshared.ValidationVerdictRetryableFailure:
		return ValidationEpochStatusFailedRetryable
	case agentshared.ValidationVerdictBlockingFailure,
		agentshared.ValidationVerdictNeedsArchitectRemediation,
		agentshared.ValidationVerdictNeedsUserDecision:
		return ValidationEpochStatusFailedBlocking
	default:
		return ValidationEpochStatusOpen
	}
}

func verdictNeedsHold(kind agentshared.ValidationVerdictKind) bool {
	switch kind {
	case agentshared.ValidationVerdictRetryableFailure,
		agentshared.ValidationVerdictBlockingFailure,
		agentshared.ValidationVerdictNeedsArchitectRemediation,
		agentshared.ValidationVerdictNeedsUserDecision:
		return true
	default:
		return false
	}
}

func (o *Orchestrator) resolvePlanIDForVerdict(payload *agentshared.ValidationVerdictPayload) string {
	if payload == nil {
		return ""
	}
	if strings.TrimSpace(payload.PlanID) != "" {
		return strings.TrimSpace(payload.PlanID)
	}
	for _, dagID := range payload.DAGIDs {
		row, err := o.store.GetDAGExecution(strings.TrimSpace(dagID))
		if err == nil && row != nil && strings.TrimSpace(row.PlanID) != "" {
			return strings.TrimSpace(row.PlanID)
		}
	}
	return ""
}

func (o *Orchestrator) handleValidationVerdictForward(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	payload, err := decodeValidationVerdictInput(fwd.Input)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if payload.CreatedAt.IsZero() {
		payload.CreatedAt = now
	}
	payload.SessionID = firstNonEmpty(strings.TrimSpace(payload.SessionID), strings.TrimSpace(fwd.SessionID), o.config.SessionID)
	payload.ValidatorAgentID = firstNonEmpty(strings.TrimSpace(payload.ValidatorAgentID), strings.TrimSpace(fwd.SourceAgentID))
	payload.ValidatorType = firstNonEmpty(strings.TrimSpace(payload.ValidatorType), strings.TrimSpace(fwd.SourceAgentName), strings.TrimSpace(fwd.SourceAgentID))
	payload.PlanID = o.resolvePlanIDForVerdict(payload)
	if strings.TrimSpace(payload.EpochID) == "" {
		payload.EpochID = "epoch_" + uuid.NewString()
	}

	epoch := &ValidationEpochRecord{
		EpochID:          payload.EpochID,
		SessionID:        payload.SessionID,
		Status:           statusForVerdict(payload.Kind),
		Reason:           string(payload.Kind),
		WorkflowIDs:      append([]string(nil), payload.WorkflowIDs...),
		DAGIDs:           append([]string(nil), payload.DAGIDs...),
		TaskIDs:          append([]string(nil), payload.AffectedTasks...),
		PlanID:           payload.PlanID,
		Summary:          strings.TrimSpace(payload.Summary),
		ValidatorVerdict: payload,
		CreatedAt:        now,
	}
	existingEpoch, err := o.store.GetValidationEpoch(epoch.EpochID)
	if err != nil {
		return nil, err
	}
	if existingEpoch != nil {
		existingEpoch.Status = epoch.Status
		existingEpoch.Reason = epoch.Reason
		existingEpoch.WorkflowIDs = epoch.WorkflowIDs
		existingEpoch.DAGIDs = epoch.DAGIDs
		existingEpoch.TaskIDs = epoch.TaskIDs
		existingEpoch.PlanID = epoch.PlanID
		existingEpoch.Summary = epoch.Summary
		existingEpoch.ValidatorVerdict = epoch.ValidatorVerdict
		epoch = existingEpoch
		if err := o.store.UpdateValidationEpoch(epoch); err != nil {
			return nil, err
		}
	} else if err := o.store.CreateValidationEpoch(epoch); err != nil {
		return nil, err
	}

	o.pushEvent(&busEvent{
		Topic:     "validation.verdict",
		Timestamp: now,
		Severity:  severityInfo,
		Summary:   fmt.Sprintf("Validation verdict received: %s", payload.Kind),
		Data: map[string]any{
			"epoch_id":        payload.EpochID,
			"validator_type":  payload.ValidatorType,
			"validator_agent": payload.ValidatorAgentID,
			"summary":         payload.Summary,
		},
	})

	o.publishValidationActivity(
		events.EventTypeAgentDecision,
		fmt.Sprintf("%s submitted a %s validation verdict: %s", payload.ValidatorType, payload.Kind, payload.Summary),
		map[string]any{
			"validation_epoch_id": payload.EpochID,
			"validator_agent_id":  payload.ValidatorAgentID,
			"validator_type":      payload.ValidatorType,
			"validation_kind":     string(payload.Kind),
			"validation_summary":  payload.Summary,
		},
	)

	if !verdictNeedsHold(payload.Kind) {
		if err := o.store.UpdateValidationEpoch(epoch); err != nil {
			return nil, err
		}
		holdState := "none"
		if hold, holdErr := o.store.GetActiveExecutionHold(payload.SessionID); holdErr == nil && hold != nil {
			holdState = string(hold.Status)
			o.broadcastControlPlaneStatus(
				fmt.Sprintf("Validation pass recorded while execution hold %s remains active for session %s", hold.HoldID, payload.SessionID),
				"warning",
			)
			o.publishValidationActivity(
				events.EventTypeAgentAction,
				fmt.Sprintf("Validation pass recorded, but execution hold %s remains active pending orchestrator resolution.", hold.HoldID),
				map[string]any{
					"validation_epoch_id": payload.EpochID,
					"execution_hold_id":   hold.HoldID,
					"hold_status":         string(hold.Status),
				},
			)
		} else {
			o.publishValidationActivity(
				events.EventTypeSuccess,
				fmt.Sprintf("Validation accepted without opening an execution hold: %s", payload.Summary),
				map[string]any{
					"validation_epoch_id": payload.EpochID,
					"validation_kind":     string(payload.Kind),
				},
			)
		}
		return map[string]any{
			"accepted":   true,
			"epoch_id":   payload.EpochID,
			"hold_state": holdState,
			"status":     string(epoch.Status),
		}, nil
	}

	hold, err := o.ensureExecutionHold(payload, epoch, now)
	if err != nil {
		return nil, err
	}
	if o.dispatchGate != nil {
		o.dispatchGate.activate(payload.SessionID)
	}
	o.publishValidationActivity(
		events.EventTypeAgentError,
		fmt.Sprintf("Execution hold opened for session %s: %s", payload.SessionID, payload.Summary),
		map[string]any{
			"validation_epoch_id": payload.EpochID,
			"execution_hold_id":   hold.HoldID,
			"validator_type":      payload.ValidatorType,
			"validation_kind":     string(payload.Kind),
		},
	)

	remediationRequest := &agentshared.RemediationRequest{
		CaseID:             "rem_" + uuid.NewString(),
		HoldID:             hold.HoldID,
		EpochID:            epoch.EpochID,
		SessionID:          payload.SessionID,
		PlanID:             payload.PlanID,
		WorkflowIDs:        append([]string(nil), payload.WorkflowIDs...),
		DAGIDs:             append([]string(nil), payload.DAGIDs...),
		AffectedTasks:      append([]string(nil), payload.AffectedTasks...),
		Summary:            payload.Summary,
		Details:            payload.Details,
		Findings:           append([]agentshared.ValidationFinding(nil), payload.Findings...),
		RecommendedActions: append([]string(nil), payload.RecommendedActions...),
		SuggestedFixes:     append([]string(nil), payload.SuggestedFixes...),
		CreatedAt:          now,
	}
	caseRecord := &RemediationCaseRecord{
		CaseID:    remediationRequest.CaseID,
		SessionID: payload.SessionID,
		EpochID:   epoch.EpochID,
		HoldID:    hold.HoldID,
		PlanID:    payload.PlanID,
		Status:    RemediationCaseStatusOpen,
		Summary:   payload.Summary,
		Request:   remediationRequest,
		CreatedAt: now,
	}
	if err := o.store.CreateRemediationCase(caseRecord); err != nil {
		return nil, err
	}
	hold.RemediationCaseID = caseRecord.CaseID
	if err := o.store.UpdateExecutionHold(hold); err != nil {
		return nil, err
	}

	epoch.Status = ValidationEpochStatusAwaitingArchitect
	if err := o.store.UpdateValidationEpoch(epoch); err != nil {
		return nil, err
	}

	o.broadcastControlPlaneStatus(
		fmt.Sprintf("Validation hold opened: %s", payload.Summary),
		"warning",
	)
	o.publishValidationActivity(
		events.EventTypeAgentAction,
		fmt.Sprintf("Architect remediation requested for validation case %s.", caseRecord.CaseID),
		map[string]any{
			"validation_epoch_id": payload.EpochID,
			"execution_hold_id":   hold.HoldID,
			"remediation_case_id": caseRecord.CaseID,
		},
	)

	caseRecord.Status = RemediationCaseStatusArchitectAnalyzing
	if err := o.store.UpdateRemediationCase(caseRecord); err != nil {
		return nil, err
	}

	remediationResult, err := o.requestArchitectRemediation(ctx, remediationRequest)
	if err != nil {
		caseRecord.Status = RemediationCaseStatusRejected
		caseRecord.CompletedAt = &now
		caseRecord.Summary = firstNonEmpty(caseRecord.Summary, payload.Summary)
		_ = o.store.UpdateRemediationCase(caseRecord)
		o.publishValidationActivity(
			events.EventTypeAgentError,
			fmt.Sprintf("Architect remediation request failed for case %s: %s", caseRecord.CaseID, err.Error()),
			map[string]any{
				"validation_epoch_id": payload.EpochID,
				"execution_hold_id":   hold.HoldID,
				"remediation_case_id": caseRecord.CaseID,
			},
		)
		return map[string]any{
			"accepted":          true,
			"epoch_id":          payload.EpochID,
			"hold_id":           hold.HoldID,
			"remediation_case":  caseRecord.CaseID,
			"architect_pending": true,
			"error":             err.Error(),
		}, nil
	}

	caseRecord.Result = remediationResult
	caseRecord.PlanID = firstNonEmpty(strings.TrimSpace(remediationResult.PlanID), caseRecord.PlanID)
	if applyErr := o.applyRemediationResult(ctx, payload.SessionID, remediationRequest, caseRecord, remediationResult); applyErr != nil {
		return nil, applyErr
	}

	return map[string]any{
		"accepted":         true,
		"epoch_id":         payload.EpochID,
		"hold_id":          hold.HoldID,
		"remediation_case": caseRecord.CaseID,
		"resolution":       remediationResult.Resolution,
	}, nil
}

func (o *Orchestrator) ensureExecutionHold(payload *agentshared.ValidationVerdictPayload, epoch *ValidationEpochRecord, now time.Time) (*ExecutionHoldRecord, error) {
	if hold, err := o.store.GetActiveExecutionHold(payload.SessionID); err == nil && hold != nil {
		return hold, nil
	}
	hold := &ExecutionHoldRecord{
		HoldID:             "hold_" + uuid.NewString(),
		SessionID:          payload.SessionID,
		EpochID:            epoch.EpochID,
		Status:             ExecutionHoldStatusActive,
		Reason:             string(payload.Kind),
		Summary:            payload.Summary,
		CreatedByAgentID:   payload.ValidatorAgentID,
		CreatedByAgentType: payload.ValidatorType,
		CreatedAt:          now,
	}
	if err := o.store.CreateExecutionHold(hold); err != nil {
		return nil, err
	}
	return hold, nil
}

func (o *Orchestrator) requestArchitectRemediation(ctx context.Context, req *agentshared.RemediationRequest) (*agentshared.RemediationResult, error) {
	targetAgentID := o.resolveArchitectTargetAgentID(nil, "")
	if targetAgentID == "" {
		return nil, fmt.Errorf("no registered architect agent id is available")
	}
	respMsg, err := o.requestRouteSync(ctx, targetAgentID, req, map[string]any{
		"control_plane_kind": agentshared.ControlPlaneKindRemediationRequest,
		"remediation_case":   req.CaseID,
		"epoch_id":           req.EpochID,
		"hold_id":            req.HoldID,
	})
	if err != nil {
		return nil, err
	}
	resp, ok := respMsg.GetRouteResponse()
	if !ok {
		return nil, fmt.Errorf("architect remediation returned invalid response payload")
	}
	return remediationResultFromRouteResponse(resp)
}

func (o *Orchestrator) applyRemediationResult(
	ctx context.Context,
	sessionID string,
	req *agentshared.RemediationRequest,
	caseRecord *RemediationCaseRecord,
	result *agentshared.RemediationResult,
) error {
	now := time.Now().UTC()
	caseRecord.Result = result
	caseRecord.PlanID = firstNonEmpty(caseRecord.PlanID, result.PlanID)

	switch result.Resolution {
	case agentshared.RemediationResolutionFixWorkflow:
		caseRecord.Status = RemediationCaseStatusAwaitingApply
		if err := o.store.UpdateRemediationCase(caseRecord); err != nil {
			return err
		}
		if strings.TrimSpace(result.FixWorkflowDAGJSON) == "" {
			return fmt.Errorf("architect remediation returned no fix workflow dag")
		}
		remediationDAG := &dag.DAG{}
		if err := remediationDAG.UnmarshalJSON([]byte(result.FixWorkflowDAGJSON)); err != nil {
			return fmt.Errorf("decode remediation dag: %w", err)
		}
		if o.dispatchGate != nil {
			o.dispatchGate.allowDAG(sessionID, remediationDAG.ID())
		}
		if _, err := o.dagBridge.Execute(ctx, remediationDAG, firstNonEmpty(result.PlanID, req.PlanID), sessionID); err != nil {
			return fmt.Errorf("execute remediation dag: %w", err)
		}
		caseRecord.Status = RemediationCaseStatusApplied
		caseRecord.CompletedAt = &now
		if err := o.store.UpdateRemediationCase(caseRecord); err != nil {
			return err
		}
		o.publishValidationActivity(
			events.EventTypeAgentDecision,
			fmt.Sprintf("Architect launched remediation workflow for case %s.", caseRecord.CaseID),
			map[string]any{
				"execution_hold_id":   caseRecord.HoldID,
				"remediation_case_id": caseRecord.CaseID,
				"remediation_plan_id": result.PlanID,
				"fix_task_count":      result.FixTaskCount,
			},
		)
		o.broadcastControlPlaneStatus(
			fmt.Sprintf("Architect launched remediation workflow for case %s", caseRecord.CaseID),
			"warning",
		)
		return nil
	case agentshared.RemediationResolutionCancelAndReplan:
		for _, dagID := range req.DAGIDs {
			_ = o.dagBridge.Cancel(strings.TrimSpace(dagID), "architect remediation requested cancel and replan")
		}
		caseRecord.Status = RemediationCaseStatusApplied
		caseRecord.CompletedAt = &now
		if err := o.store.UpdateRemediationCase(caseRecord); err != nil {
			return err
		}
		o.publishValidationActivity(
			events.EventTypeAgentDecision,
			fmt.Sprintf("Architect requested cancel and replan for remediation case %s.", caseRecord.CaseID),
			map[string]any{
				"execution_hold_id":   caseRecord.HoldID,
				"remediation_case_id": caseRecord.CaseID,
			},
		)
		o.broadcastControlPlaneStatus(
			fmt.Sprintf("Architect requested cancel/replan for remediation case %s", caseRecord.CaseID),
			"warning",
		)
		return nil
	case agentshared.RemediationResolutionNeedsUserInput:
		caseRecord.Status = RemediationCaseStatusNeedsUserInput
		caseRecord.CompletedAt = &now
		if err := o.store.UpdateRemediationCase(caseRecord); err != nil {
			return err
		}
		o.publishValidationActivity(
			events.EventTypeAgentDecision,
			fmt.Sprintf("Remediation case %s needs user input.", caseRecord.CaseID),
			map[string]any{
				"execution_hold_id":   caseRecord.HoldID,
				"remediation_case_id": caseRecord.CaseID,
			},
		)
		o.broadcastControlPlaneStatus(
			fmt.Sprintf("Remediation case %s needs user input", caseRecord.CaseID),
			"warning",
		)
		return nil
	case agentshared.RemediationResolutionUnrecoverable:
		caseRecord.Status = RemediationCaseStatusRejected
		caseRecord.CompletedAt = &now
		if err := o.store.UpdateRemediationCase(caseRecord); err != nil {
			return err
		}
		o.publishValidationActivity(
			events.EventTypeFailure,
			fmt.Sprintf("Remediation case %s is unrecoverable.", caseRecord.CaseID),
			map[string]any{
				"execution_hold_id":   caseRecord.HoldID,
				"remediation_case_id": caseRecord.CaseID,
			},
		)
		o.broadcastControlPlaneStatus(
			fmt.Sprintf("Remediation case %s is unrecoverable", caseRecord.CaseID),
			"error",
		)
		return nil
	default:
		caseRecord.Status = RemediationCaseStatusApplied
		caseRecord.CompletedAt = &now
		return o.store.UpdateRemediationCase(caseRecord)
	}
}

func (o *Orchestrator) broadcastControlPlaneStatus(message, level string) {
	if strings.TrimSpace(message) == "" || o.bus == nil || !o.running {
		return
	}
	msg := &guide.Message{
		ID:            generateMessageID(),
		Type:          guide.MessageTypeResponse,
		SourceAgentID: o.config.AgentID,
		Payload:       message,
		Metadata: map[string]any{
			"level":  level,
			"source": "orchestrator",
			"kind":   "validation_control",
		},
		Timestamp: time.Now(),
	}
	_ = o.bus.Publish("orchestrator.status", msg)
}

func (o *Orchestrator) publishValidationActivity(eventType events.EventType, content string, data map[string]any) {
	if o.activityPub == nil || strings.TrimSpace(content) == "" {
		return
	}
	evt := events.NewActivityEvent(eventType, o.config.SessionID, content)
	evt.AgentID = o.config.AgentID
	evt.Visibility = events.VisibilityUser
	evt.Category = "validation_control"
	evt.Data["agent_type"] = "orchestrator"
	evt.Data["agent_name"] = "Orchestrator"
	evt.Data["source"] = "validation_control"
	for key, value := range data {
		evt.Data[key] = value
	}
	o.activityPub.PublishActivity(evt)
}
