package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/google/uuid"
)

func decodeCommandApprovalHoldInput(raw string) (*agentshared.CommandApprovalHoldRequest, error) {
	var payload agentshared.CommandApprovalHoldRequest
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode command approval hold: %w", err)
	}
	return &payload, nil
}

func (o *Orchestrator) handleCommandApprovalHoldForward(_ context.Context, fwd *guide.ForwardedRequest) (any, error) {
	payload, err := decodeCommandApprovalHoldInput(fwd.Input)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	payload.SessionID = firstNonEmpty(strings.TrimSpace(payload.SessionID), strings.TrimSpace(fwd.SessionID), o.config.SessionID)
	payload.DAGID = strings.TrimSpace(payload.DAGID)
	payload.NodeID = strings.TrimSpace(payload.NodeID)
	payload.TaskID = strings.TrimSpace(payload.TaskID)
	payload.PipelineID = firstNonEmpty(strings.TrimSpace(payload.PipelineID), strings.TrimSpace(payload.TaskID))
	payload.SourceAgentID = firstNonEmpty(strings.TrimSpace(payload.SourceAgentID), strings.TrimSpace(fwd.SourceAgentID))
	payload.SourceAgentType = firstNonEmpty(strings.TrimSpace(payload.SourceAgentType), strings.TrimSpace(fwd.SourceAgentName), strings.TrimSpace(fwd.SourceAgentID))
	if payload.RequestedAt.IsZero() {
		payload.RequestedAt = now
	}

	result := &agentshared.CommandApprovalHoldResult{
		Accepted:   true,
		HoldID:     strings.TrimSpace(payload.HoldID),
		SessionID:  payload.SessionID,
		DAGID:      payload.DAGID,
		NodeID:     payload.NodeID,
		TaskID:     payload.TaskID,
		PipelineID: payload.PipelineID,
		UpdatedAt:  now,
	}

	switch payload.Action {
	case agentshared.CommandApprovalHoldBegin:
		if result.HoldID == "" {
			result.HoldID = "approval_hold_" + uuid.NewString()
		}
		if o.dispatchGate != nil && result.SessionID != "" && result.DAGID != "" {
			o.dispatchGate.activateDAG(result.SessionID, result.DAGID, result.HoldID)
		}
		if o.taskRouter != nil {
			o.taskRouter.PauseActiveRoutes(
				result.SessionID,
				result.DAGID,
				fmt.Sprintf("command approval hold %s is active", result.HoldID),
			)
		}
		result.State = "active"
		return result, nil
	case agentshared.CommandApprovalHoldResolve:
		if o.dispatchGate != nil && result.SessionID != "" && result.HoldID != "" {
			o.dispatchGate.resolveHold(result.SessionID, result.HoldID)
		}
		if o.taskRouter != nil {
			o.taskRouter.ResumeActiveRoutes(
				result.SessionID,
				result.DAGID,
				fmt.Sprintf("command approval hold %s resolved", result.HoldID),
			)
		}
		result.State = "resolved"
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported command approval hold action %q", payload.Action)
	}
}
