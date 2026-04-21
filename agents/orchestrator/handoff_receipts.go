package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
)

var receiptStatusesRequiringSubmission = map[agentshared.PlanHandoffReceiptStatus]struct{}{
	agentshared.PlanHandoffReceiptStatusAccepted: {},
	agentshared.PlanHandoffReceiptStatusDAGBuilt: {},
}

var receiptStatusesRequiringDAGRow = map[agentshared.PlanHandoffReceiptStatus]struct{}{
	agentshared.PlanHandoffReceiptStatusSubmitted: {},
	agentshared.PlanHandoffReceiptStatusRunning:   {},
}

var receiptStatusByDAGState = map[string]agentshared.PlanHandoffReceiptStatus{
	"running":   agentshared.PlanHandoffReceiptStatusRunning,
	"succeeded": agentshared.PlanHandoffReceiptStatusRunning,
	"failed":    agentshared.PlanHandoffReceiptStatusFailed,
	"cancelled": agentshared.PlanHandoffReceiptStatusFailed,
}

func handoffCorrelationIDFromContext(ctx context.Context) string {
	metadata, ok := orchestratorStreamMetadataFromContext(ctx)
	if !ok {
		return ""
	}
	return strings.TrimSpace(metadata.CorrelationID)
}

func (o *Orchestrator) normalizePlanHandoffReceipt(receipt *PlanHandoffReceiptRecord) (*PlanHandoffReceiptRecord, error) {
	if !canNormalizePlanHandoffReceipt(o, receipt) {
		return receipt, nil
	}
	if receiptHasDAGID(receipt) {
		return o.normalizePlanHandoffReceiptWithDAG(receipt)
	}
	return o.normalizePlanHandoffReceiptWithoutDAG(receipt)
}

func canNormalizePlanHandoffReceipt(o *Orchestrator, receipt *PlanHandoffReceiptRecord) bool {
	if o == nil || o.store == nil {
		return false
	}
	return receipt != nil
}

func receiptHasDAGID(receipt *PlanHandoffReceiptRecord) bool {
	return receipt != nil && strings.TrimSpace(receipt.DAGID) != ""
}

func (o *Orchestrator) normalizePlanHandoffReceiptWithDAG(
	receipt *PlanHandoffReceiptRecord,
) (*PlanHandoffReceiptRecord, error) {
	row, err := o.store.GetDAGExecution(receipt.DAGID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return o.normalizePlanHandoffReceiptWithoutExecutionRow(receipt)
	}
	return o.normalizePlanHandoffReceiptWithExecutionRow(receipt, row)
}

func (o *Orchestrator) normalizePlanHandoffReceiptWithExecutionRow(
	receipt *PlanHandoffReceiptRecord,
	row *DAGExecutionRow,
) (*PlanHandoffReceiptRecord, error) {
	status, ok := handoffReceiptStatusForDAGState(row.State)
	if !ok {
		return receipt, nil
	}
	return o.syncPlanHandoffReceiptStatus(receipt, status, dagErrorText(row))
}

func handoffReceiptStatusForDAGState(state string) (agentshared.PlanHandoffReceiptStatus, bool) {
	status, ok := receiptStatusByDAGState[strings.TrimSpace(strings.ToLower(state))]
	return status, ok
}

func dagErrorText(row *DAGExecutionRow) string {
	if row == nil {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(row.Error), "dag execution no longer active")
}

func (o *Orchestrator) syncPlanHandoffReceiptStatus(
	receipt *PlanHandoffReceiptRecord,
	status agentshared.PlanHandoffReceiptStatus,
	errorText string,
) (*PlanHandoffReceiptRecord, error) {
	if receipt == nil {
		return nil, nil
	}
	if receipt.Status == status && strings.TrimSpace(errorText) == "" {
		return receipt, nil
	}
	return o.store.UpdatePlanHandoffReceiptStage(
		receipt.SessionID,
		receipt.PlanID,
		receipt.Revision,
		status,
		receipt.WorkflowID,
		receipt.DAGID,
		receipt.TaskCount,
		receipt.LayerCount,
		errorText,
	)
}

func (o *Orchestrator) normalizePlanHandoffReceiptWithoutExecutionRow(
	receipt *PlanHandoffReceiptRecord,
) (*PlanHandoffReceiptRecord, error) {
	if requiresDurableDAGExecutionRow(receipt) {
		return o.failPlanHandoffReceipt(receipt, "durable dag execution row missing for submitted handoff")
	}
	return o.normalizePlanHandoffReceiptWithoutDAG(receipt)
}

func requiresDurableDAGExecutionRow(receipt *PlanHandoffReceiptRecord) bool {
	if receipt == nil {
		return false
	}
	_, ok := receiptStatusesRequiringDAGRow[receipt.Status]
	return ok
}

func (o *Orchestrator) normalizePlanHandoffReceiptWithoutDAG(
	receipt *PlanHandoffReceiptRecord,
) (*PlanHandoffReceiptRecord, error) {
	if !requiresDurableSubmission(receipt) {
		return receipt, nil
	}
	return o.failPlanHandoffReceipt(receipt, "handoff did not reach durable DAG submission")
}

func requiresDurableSubmission(receipt *PlanHandoffReceiptRecord) bool {
	if receipt == nil {
		return false
	}
	_, ok := receiptStatusesRequiringSubmission[receipt.Status]
	return ok
}

func (o *Orchestrator) failPlanHandoffReceipt(
	receipt *PlanHandoffReceiptRecord,
	errorText string,
) (*PlanHandoffReceiptRecord, error) {
	return o.store.UpdatePlanHandoffReceiptStage(
		receipt.SessionID,
		receipt.PlanID,
		receipt.Revision,
		agentshared.PlanHandoffReceiptStatusFailed,
		receipt.WorkflowID,
		receipt.DAGID,
		receipt.TaskCount,
		receipt.LayerCount,
		errorText,
	)
}

func (o *Orchestrator) reconcilePlanHandoffReceipts() {
	if !canReconcilePlanHandoffReceipts(o) {
		return
	}
	receipts, err := o.store.ListOpenPlanHandoffReceipts()
	if err != nil {
		o.logTrace("plan_handoff_receipts_list_failed", agentlog.EventError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	for _, receipt := range receipts {
		o.reconcileSinglePlanHandoffReceipt(receipt)
	}
}

func canReconcilePlanHandoffReceipts(o *Orchestrator) bool {
	if o == nil {
		return false
	}
	return o.store != nil
}

func (o *Orchestrator) reconcileSinglePlanHandoffReceipt(receipt *PlanHandoffReceiptRecord) {
	normalized, err := o.normalizePlanHandoffReceipt(receipt)
	if err != nil {
		o.logTrace("plan_handoff_receipt_reconcile_failed", agentlog.EventError, map[string]any{
			"plan_id":  receipt.PlanID,
			"revision": receipt.Revision,
			"error":    err.Error(),
		})
		return
	}
	o.publishPlanHandoffReceiptUpdate(normalized, "reconcile")
	if receiptStatusChanged(receipt, normalized) {
		o.logPlanHandoffReceiptReconciled(receipt, normalized)
	}
}

func receiptStatusChanged(before, after *PlanHandoffReceiptRecord) bool {
	if before == nil || after == nil {
		return false
	}
	return after.Status != before.Status
}

func (o *Orchestrator) logPlanHandoffReceiptReconciled(
	before *PlanHandoffReceiptRecord,
	after *PlanHandoffReceiptRecord,
) {
	o.logTrace("plan_handoff_receipt_reconciled", agentlog.EventRegistryEvent, map[string]any{
		"plan_id":    after.PlanID,
		"revision":   after.Revision,
		"old_status": before.Status,
		"new_status": after.Status,
		"dag_id":     after.DAGID,
	})
}

func (o *Orchestrator) handoffResultFromReceipt(receipt *PlanHandoffReceiptRecord, duplicate bool) map[string]any {
	if receipt == nil {
		return map[string]any{"ingested": false}
	}
	return map[string]any{
		"ingested":       receipt.Status != agentshared.PlanHandoffReceiptStatusFailed,
		"duplicate":      duplicate,
		"receipt_id":     receipt.ReceiptID,
		"receipt_status": string(receipt.Status),
		"plan_id":        receipt.PlanID,
		"dag_id":         receipt.DAGID,
		"workflow_id":    receipt.WorkflowID,
		"task_count":     receipt.TaskCount,
		"layer_count":    receipt.LayerCount,
		"error":          receipt.ErrorText,
	}
}

func decodePlanHandoffStatusRequest(raw string) (*agentshared.PlanHandoffStatusRequest, error) {
	var req agentshared.PlanHandoffStatusRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil, fmt.Errorf("decode plan handoff status request: %w", err)
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.PlanID = strings.TrimSpace(req.PlanID)
	if req.SessionID == "" || req.PlanID == "" {
		return nil, fmt.Errorf("session_id and plan_id are required")
	}
	return &req, nil
}

func (o *Orchestrator) handlePlanHandoffStatusForward(_ context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req, err := decodePlanHandoffStatusRequest(fwd.Input)
	if err != nil {
		o.logTrace("plan_handoff_status_forward_decode_failed", agentlog.EventError, map[string]any{
			"input_len": len(fwd.Input),
			"error":     err.Error(),
		})
		return nil, err
	}
	o.logInfo("handlePlanHandoffStatusForward: lookup requested by architect",
		"plan_id", req.PlanID,
		"session_id", req.SessionID,
		"revision", req.Revision)
	normalized, found, err := o.lookupNormalizedPlanHandoffReceipt(req)
	if err != nil {
		o.logTrace("plan_handoff_status_forward_lookup_failed", agentlog.EventError, map[string]any{
			"plan_id":    req.PlanID,
			"session_id": req.SessionID,
			"revision":   req.Revision,
			"error":      err.Error(),
		})
		return nil, err
	}
	o.logPlanHandoffStatusLookupResult(req, normalized, found)
	return &agentshared.PlanHandoffStatusResponse{
		Found:   found,
		Receipt: payloadFromPlanHandoffReceipt(normalized),
	}, nil
}

func (o *Orchestrator) logPlanHandoffStatusLookupResult(
	req *agentshared.PlanHandoffStatusRequest,
	receipt *PlanHandoffReceiptRecord,
	found bool,
) {
	fields := map[string]any{
		"plan_id":     req.PlanID,
		"session_id":  req.SessionID,
		"revision":    req.Revision,
		"found":       found,
		"receipt_nil": receipt == nil,
	}
	dagAlive := false
	if receipt != nil {
		fields["receipt_id"] = receipt.ReceiptID
		fields["receipt_status"] = string(receipt.Status)
		fields["dag_id"] = receipt.DAGID
		fields["workflow_id"] = receipt.WorkflowID
		fields["task_count"] = receipt.TaskCount
		fields["layer_count"] = receipt.LayerCount
		fields["error_text"] = receipt.ErrorText
		fields["updated_at"] = receipt.UpdatedAt
		if o.store != nil && strings.TrimSpace(receipt.DAGID) != "" {
			row, rowErr := o.store.GetDAGExecution(receipt.DAGID)
			if rowErr == nil && row != nil {
				dagAlive = true
				fields["dag_state"] = row.State
			}
			if rowErr != nil {
				fields["dag_lookup_error"] = rowErr.Error()
			}
			fields["dag_alive"] = dagAlive
		}
	}
	o.logInfo("handlePlanHandoffStatusForward: returning receipt to architect",
		"plan_id", req.PlanID,
		"session_id", req.SessionID,
		"found", found,
		"receipt_status", receiptRecordStatusForLog(receipt),
		"dag_id", receiptRecordDAGIDForLog(receipt),
		"dag_alive", dagAlive)
	o.logTrace("plan_handoff_status_forward_result", agentlog.EventTaskDispatched, fields)
	if receipt != nil && strings.TrimSpace(receipt.DAGID) != "" && !dagAlive {
		o.logWarnMsg("handlePlanHandoffStatusForward: GHOST RECEIPT — returning receipt with dag_id that no longer exists in DAGExecution store; architect will falsely announce 'DAG X is now running'",
			"plan_id", req.PlanID,
			"session_id", req.SessionID,
			"dag_id", receipt.DAGID,
			"receipt_status", string(receipt.Status),
			"receipt_updated_at", receipt.UpdatedAt)
	}
}

func receiptRecordStatusForLog(r *PlanHandoffReceiptRecord) string {
	if r == nil {
		return ""
	}
	return string(r.Status)
}

func receiptRecordDAGIDForLog(r *PlanHandoffReceiptRecord) string {
	if r == nil {
		return ""
	}
	return r.DAGID
}

func (o *Orchestrator) lookupNormalizedPlanHandoffReceipt(
	req *agentshared.PlanHandoffStatusRequest,
) (*PlanHandoffReceiptRecord, bool, error) {
	receipt, err := o.store.GetPlanHandoffReceipt(req.SessionID, req.PlanID, req.Revision)
	if err != nil {
		return nil, false, err
	}
	if receipt == nil {
		return nil, false, nil
	}
	normalized, err := o.normalizePlanHandoffReceipt(receipt)
	if err != nil {
		return nil, false, err
	}
	return normalized, normalized != nil, nil
}

func payloadFromPlanHandoffReceipt(receipt *PlanHandoffReceiptRecord) *agentshared.PlanHandoffReceipt {
	if receipt == nil {
		return nil
	}
	return receipt.payload()
}
