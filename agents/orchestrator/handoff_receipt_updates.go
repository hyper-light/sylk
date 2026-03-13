package orchestrator

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

func (o *Orchestrator) publishPlanHandoffReceiptUpdate(
	receipt *PlanHandoffReceiptRecord,
	reason string,
) {
	if !canPublishPlanHandoffReceiptUpdate(o, receipt) {
		return
	}
	update := &agentshared.PlanHandoffReceiptUpdate{
		SessionID:     receipt.SessionID,
		PlanID:        receipt.PlanID,
		Revision:      receipt.Revision,
		CorrelationID: receipt.CorrelationID,
		Found:         true,
		Receipt:       receipt.payload(),
		Reason:        strings.TrimSpace(reason),
	}
	o.publishPlanHandoffReceiptControlRequest(update, o.resolveArchitectTargetAgentID(receipt, ""))
}

func canPublishPlanHandoffReceiptUpdate(o *Orchestrator, receipt *PlanHandoffReceiptRecord) bool {
	if !orchestratorCanPublishReceiptUpdates(o) {
		return false
	}
	if receipt == nil {
		return false
	}
	return strings.TrimSpace(receipt.CorrelationID) != ""
}

func orchestratorCanPublishReceiptUpdates(o *Orchestrator) bool {
	if o == nil || o.bus == nil {
		return false
	}
	return o.running
}

func (o *Orchestrator) publishPlanHandoffReceiptControlRequest(
	update *agentshared.PlanHandoffReceiptUpdate,
	targetAgentID string,
) {
	if !canPublishPlanHandoffReceiptControlRequest(o, update) {
		return
	}
	targetAgentID = strings.TrimSpace(targetAgentID)
	if targetAgentID == "" {
		o.logTrace("plan_handoff_receipt_update_target_missing", agentlog.EventError, map[string]any{
			"plan_id":        update.PlanID,
			"revision":       update.Revision,
			"receipt_status": receiptStatusFromUpdate(update),
		})
		return
	}
	payload, err := marshalPlanHandoffReceiptControlUpdate(update)
	if err != nil {
		o.logTrace("plan_handoff_receipt_update_marshal_failed", agentlog.EventError, map[string]any{
			"plan_id":  update.PlanID,
			"revision": update.Revision,
			"error":    err.Error(),
		})
		return
	}
	if err := o.publishPlanHandoffReceiptRouteRequest(payload, update, targetAgentID); err != nil {
		o.logTrace("plan_handoff_receipt_update_publish_failed", agentlog.EventError, map[string]any{
			"plan_id":         update.PlanID,
			"revision":        update.Revision,
			"receipt_status":  receiptStatusFromUpdate(update),
			"target_agent_id": targetAgentID,
			"error":           err.Error(),
		})
		return
	}
	o.logTrace("plan_handoff_receipt_update_published", agentlog.EventTaskDispatched, map[string]any{
		"plan_id":         update.PlanID,
		"revision":        update.Revision,
		"receipt_status":  receiptStatusFromUpdate(update),
		"target_agent_id": targetAgentID,
		"reason":          strings.TrimSpace(update.Reason),
	})
}

func canPublishPlanHandoffReceiptControlRequest(
	o *Orchestrator,
	update *agentshared.PlanHandoffReceiptUpdate,
) bool {
	if o == nil || o.bus == nil {
		return false
	}
	return update != nil
}

func marshalPlanHandoffReceiptControlUpdate(update *agentshared.PlanHandoffReceiptUpdate) ([]byte, error) {
	payload, err := json.Marshal(update)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (o *Orchestrator) publishPlanHandoffReceiptRouteRequest(
	payload []byte,
	update *agentshared.PlanHandoffReceiptUpdate,
	targetAgentID string,
) error {
	req := &guide.RouteRequest{
		CorrelationID:       "handoff_update_" + uuid.NewString(),
		ParentCorrelationID: strings.TrimSpace(update.CorrelationID),
		Input:               string(payload),
		SourceAgentID:       o.config.AgentID,
		SourceAgentName:     "orchestrator",
		TargetAgentID:       targetAgentID,
		ExplicitTarget:      true,
		FireAndForget:       true,
		SessionID:           update.SessionID,
		Timestamp:           time.Now(),
		Metadata: map[string]any{
			"control_plane_kind": agentshared.ControlPlaneKindPlanHandoffReceiptUpdate,
		},
	}
	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.CorrelationID = req.CorrelationID
	return o.bus.Publish(guide.TopicGuideRequests, msg)
}

func receiptStatusFromUpdate(update *agentshared.PlanHandoffReceiptUpdate) string {
	if update == nil || update.Receipt == nil {
		return ""
	}
	return string(update.Receipt.Status)
}

func decodePlanHandoffReceiptResyncRequest(raw string) (*agentshared.PlanHandoffReceiptResyncRequest, error) {
	var req agentshared.PlanHandoffReceiptResyncRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil, fmt.Errorf("decode plan handoff resync request: %w", err)
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.PlanID = strings.TrimSpace(req.PlanID)
	req.CorrelationID = strings.TrimSpace(req.CorrelationID)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.SessionID == "" || req.PlanID == "" {
		return nil, fmt.Errorf("session_id and plan_id are required")
	}
	return &req, nil
}

func (o *Orchestrator) handlePlanHandoffReceiptResyncForward(
	_ context.Context,
	fwd *guide.ForwardedRequest,
) (any, error) {
	req, err := decodePlanHandoffReceiptResyncRequest(fwd.Input)
	if err != nil {
		return nil, err
	}
	update, targetAgentID, err := o.planHandoffReceiptUpdateFromResync(req, fwd.SourceAgentID)
	if err != nil {
		return nil, err
	}
	o.publishPlanHandoffReceiptControlRequest(update, targetAgentID)
	return map[string]any{"found": update.Found}, nil
}

func (o *Orchestrator) planHandoffReceiptUpdateFromResync(
	req *agentshared.PlanHandoffReceiptResyncRequest,
	sourceAgentID string,
) (*agentshared.PlanHandoffReceiptUpdate, string, error) {
	receipt, err := o.store.GetPlanHandoffReceipt(req.SessionID, req.PlanID, req.Revision)
	if err != nil {
		return nil, "", err
	}
	if receipt == nil {
		return missingPlanHandoffReceiptUpdate(req), concreteAgentID(sourceAgentID, "architect"), nil
	}
	bound, err := o.store.BindPlanHandoffReceiptRequesterAgentID(req.SessionID, req.PlanID, req.Revision, sourceAgentID)
	if err != nil {
		return nil, "", err
	}
	if bound != nil {
		receipt = bound
	}
	normalized, err := o.normalizePlanHandoffReceipt(receipt)
	if err != nil {
		return nil, "", err
	}
	return foundPlanHandoffReceiptUpdate(req, normalized), o.resolveArchitectTargetAgentID(normalized, sourceAgentID), nil
}

func missingPlanHandoffReceiptUpdate(
	req *agentshared.PlanHandoffReceiptResyncRequest,
) *agentshared.PlanHandoffReceiptUpdate {
	return &agentshared.PlanHandoffReceiptUpdate{
		SessionID:     req.SessionID,
		PlanID:        req.PlanID,
		Revision:      req.Revision,
		CorrelationID: req.CorrelationID,
		Found:         false,
		ErrorText:     "no durable handoff receipt found",
		Reason:        firstNonEmpty(req.Reason, "resync"),
	}
}

func foundPlanHandoffReceiptUpdate(
	req *agentshared.PlanHandoffReceiptResyncRequest,
	receipt *PlanHandoffReceiptRecord,
) *agentshared.PlanHandoffReceiptUpdate {
	return &agentshared.PlanHandoffReceiptUpdate{
		SessionID:     receipt.SessionID,
		PlanID:        receipt.PlanID,
		Revision:      receipt.Revision,
		CorrelationID: receipt.CorrelationID,
		Found:         receipt != nil,
		Receipt:       payloadFromPlanHandoffReceipt(receipt),
		Reason:        firstNonEmpty(req.Reason, "resync"),
	}
}
