package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/uptrace/bun"
)

func encodeStringSliceJSON(values []string) string {
	if len(values) == 0 {
		return ""
	}
	data, _ := json.Marshal(values)
	return string(data)
}

func decodeStringSliceJSON(raw string) []string {
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func encodeJSONValue(value any) string {
	if value == nil {
		return ""
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func decodeValidationVerdict(raw string) *agentshared.ValidationVerdictPayload {
	if raw == "" {
		return nil
	}
	var verdict agentshared.ValidationVerdictPayload
	if err := json.Unmarshal([]byte(raw), &verdict); err != nil {
		return nil
	}
	return &verdict
}

func decodeRemediationRequest(raw string) *agentshared.RemediationRequest {
	if raw == "" {
		return nil
	}
	var req agentshared.RemediationRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil
	}
	return &req
}

func decodeRemediationResult(raw string) *agentshared.RemediationResult {
	if raw == "" {
		return nil
	}
	var result agentshared.RemediationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil
	}
	return &result
}

func (s *Store) CreateValidationEpoch(record *ValidationEpochRecord) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	const q = `INSERT INTO validation_epochs
		(epoch_id, session_id, status, reason, workflow_ids_json, dag_ids_json, task_ids_json, plan_id, summary, validator_verdict_json, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q,
			record.EpochID,
			record.SessionID,
			string(record.Status),
			record.Reason,
			encodeStringSliceJSON(record.WorkflowIDs),
			encodeStringSliceJSON(record.DAGIDs),
			encodeStringSliceJSON(record.TaskIDs),
			record.PlanID,
			record.Summary,
			encodeJSONValue(record.ValidatorVerdict),
			record.CreatedAt,
			record.UpdatedAt,
			record.CompletedAt,
		)
		return err
	}); err != nil {
		return fmt.Errorf("create validation epoch: %w", err)
	}
	emitValidationStarted(context.Background(), record)
	return nil
}

func (s *Store) UpdateValidationEpoch(record *ValidationEpochRecord) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	record.UpdatedAt = time.Now().UTC()
	const q = `UPDATE validation_epochs
		SET status = ?, reason = ?, workflow_ids_json = ?, dag_ids_json = ?, task_ids_json = ?, plan_id = ?, summary = ?, validator_verdict_json = ?, updated_at = ?, completed_at = ?
		WHERE epoch_id = ?`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q,
			string(record.Status),
			record.Reason,
			encodeStringSliceJSON(record.WorkflowIDs),
			encodeStringSliceJSON(record.DAGIDs),
			encodeStringSliceJSON(record.TaskIDs),
			record.PlanID,
			record.Summary,
			encodeJSONValue(record.ValidatorVerdict),
			record.UpdatedAt,
			record.CompletedAt,
			record.EpochID,
		)
		return err
	}); err != nil {
		return fmt.Errorf("update validation epoch: %w", err)
	}
	emitValidationCompleted(context.Background(), record)
	return nil
}

func (s *Store) GetValidationEpoch(epochID string) (*ValidationEpochRecord, error) {
	const q = `SELECT epoch_id, session_id, status, COALESCE(reason, ''), COALESCE(workflow_ids_json, ''), COALESCE(dag_ids_json, ''), COALESCE(task_ids_json, ''), COALESCE(plan_id, ''), COALESCE(summary, ''), COALESCE(validator_verdict_json, ''), created_at, updated_at, completed_at
		FROM validation_epochs WHERE epoch_id = ?`
	row := &ValidationEpochRecord{}
	var workflowJSON, dagJSON, taskJSON, verdictJSON string
	err := s.db.QueryRow(q, epochID).Scan(
		&row.EpochID,
		&row.SessionID,
		&row.Status,
		&row.Reason,
		&workflowJSON,
		&dagJSON,
		&taskJSON,
		&row.PlanID,
		&row.Summary,
		&verdictJSON,
		&row.CreatedAt,
		&row.UpdatedAt,
		&row.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get validation epoch: %w", err)
	}
	row.WorkflowIDs = decodeStringSliceJSON(workflowJSON)
	row.DAGIDs = decodeStringSliceJSON(dagJSON)
	row.TaskIDs = decodeStringSliceJSON(taskJSON)
	row.ValidatorVerdict = decodeValidationVerdict(verdictJSON)
	return row, nil
}

func (s *Store) CreateExecutionHold(record *ExecutionHoldRecord) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	const q = `INSERT INTO execution_holds
		(hold_id, session_id, plan_id, epoch_id, remediation_case_id, status, reason, summary, blocks_dag_ids, exempt_dag_ids, created_by_agent_id, created_by_agent_type, released_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q,
			record.HoldID,
			record.SessionID,
			record.PlanID,
			record.EpochID,
			record.RemediationCaseID,
			string(record.Status),
			record.Reason,
			record.Summary,
			encodeStringSliceJSON(record.BlocksDAGIDs),
			encodeStringSliceJSON(record.ExemptDAGIDs),
			record.CreatedByAgentID,
			record.CreatedByAgentType,
			record.ReleasedAt,
			record.CreatedAt,
			record.UpdatedAt,
		)
		return err
	}); err != nil {
		return fmt.Errorf("create execution hold: %w", err)
	}
	s.notifyHoldsChanged(record.SessionID)
	emitHoldAcquired(context.Background(), record)
	return nil
}

func (s *Store) UpdateExecutionHold(record *ExecutionHoldRecord) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	record.UpdatedAt = time.Now().UTC()
	const q = `UPDATE execution_holds
		SET plan_id = ?, epoch_id = ?, remediation_case_id = ?, status = ?, reason = ?, summary = ?, blocks_dag_ids = ?, exempt_dag_ids = ?, released_at = ?, updated_at = ?
		WHERE hold_id = ?`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q,
			record.PlanID,
			record.EpochID,
			record.RemediationCaseID,
			string(record.Status),
			record.Reason,
			record.Summary,
			encodeStringSliceJSON(record.BlocksDAGIDs),
			encodeStringSliceJSON(record.ExemptDAGIDs),
			record.ReleasedAt,
			record.UpdatedAt,
			record.HoldID,
		)
		return err
	}); err != nil {
		return fmt.Errorf("update execution hold: %w", err)
	}
	s.notifyHoldsChanged(record.SessionID)
	if record.Status == ExecutionHoldStatusResolved || record.Status == ExecutionHoldStatusAborted || record.Status == ExecutionHoldStatusSuperseded {
		emitHoldReleased(context.Background(), record)
	}
	return nil
}

func (s *Store) GetActiveExecutionHold(sessionID string) (*ExecutionHoldRecord, error) {
	const q = `SELECT hold_id, session_id, COALESCE(plan_id, ''), COALESCE(epoch_id, ''), COALESCE(remediation_case_id, ''), status, reason, COALESCE(summary, ''), COALESCE(blocks_dag_ids, ''), COALESCE(exempt_dag_ids, ''), created_by_agent_id, created_by_agent_type, released_at, created_at, updated_at
		FROM execution_holds
		WHERE session_id = ? AND status = ?
		ORDER BY created_at DESC LIMIT 1`
	row := &ExecutionHoldRecord{}
	var blocksJSON, exemptJSON string
	err := s.db.QueryRow(q, sessionID, string(ExecutionHoldStatusActive)).Scan(
		&row.HoldID,
		&row.SessionID,
		&row.PlanID,
		&row.EpochID,
		&row.RemediationCaseID,
		&row.Status,
		&row.Reason,
		&row.Summary,
		&blocksJSON,
		&exemptJSON,
		&row.CreatedByAgentID,
		&row.CreatedByAgentType,
		&row.ReleasedAt,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active execution hold: %w", err)
	}
	row.BlocksDAGIDs = decodeStringSliceJSON(blocksJSON)
	row.ExemptDAGIDs = decodeStringSliceJSON(exemptJSON)
	return row, nil
}

// GetExecutionHold returns the hold with the given holdID regardless
// of status. Used when a caller already has the holdID in hand —
// most commonly when mutating a specific hold (e.g. exempting a
// remediation DAG).
func (s *Store) GetExecutionHold(holdID string) (*ExecutionHoldRecord, error) {
	const q = `SELECT hold_id, session_id, COALESCE(plan_id, ''), COALESCE(epoch_id, ''), COALESCE(remediation_case_id, ''), status, reason, COALESCE(summary, ''), COALESCE(blocks_dag_ids, ''), COALESCE(exempt_dag_ids, ''), created_by_agent_id, created_by_agent_type, released_at, created_at, updated_at
		FROM execution_holds
		WHERE hold_id = ?`
	row := &ExecutionHoldRecord{}
	var blocksJSON, exemptJSON string
	err := s.db.QueryRow(q, holdID).Scan(
		&row.HoldID,
		&row.SessionID,
		&row.PlanID,
		&row.EpochID,
		&row.RemediationCaseID,
		&row.Status,
		&row.Reason,
		&row.Summary,
		&blocksJSON,
		&exemptJSON,
		&row.CreatedByAgentID,
		&row.CreatedByAgentType,
		&row.ReleasedAt,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get execution hold: %w", err)
	}
	row.BlocksDAGIDs = decodeStringSliceJSON(blocksJSON)
	row.ExemptDAGIDs = decodeStringSliceJSON(exemptJSON)
	return row, nil
}

// DAGIsHeld is the single authoritative gating query for the
// dispatch permit. It returns (true, holdID, reason) if any active
// hold for (sessionID, planID) blocks dagID — i.e. the hold's
// BlocksDAGIDs is empty (blocks every DAG in the plan) or contains
// dagID, AND dagID is not in the hold's ExemptDAGIDs. Otherwise
// (false, "", ""). The gate calls this on every wait() iteration;
// no in-memory state duplicates the result.
//
// When planID is empty the query only matches holds with no plan
// scope — intentionally narrow. Callers that don't have a plan_id
// should supply "" and will be gated only by legacy/unscoped holds,
// which the migration already supersedes.
func (s *Store) DAGIsHeld(ctx context.Context, sessionID, planID, dagID string) (bool, string, string, error) {
	if s == nil || s.db == nil {
		return false, "", "", nil
	}
	// Scope to session + plan. Then evaluate each active hold's
	// blocks_dag_ids / exempt_dag_ids in Go — the list sizes are
	// tiny and this avoids JSON_EACH portability concerns.
	const q = `SELECT hold_id, COALESCE(blocks_dag_ids, ''), COALESCE(exempt_dag_ids, ''), COALESCE(reason, '')
		FROM execution_holds
		WHERE session_id = ? AND plan_id = ? AND status = ?`
	rows, err := s.db.SQLDB().QueryContext(ctx, q, sessionID, planID, string(ExecutionHoldStatusActive))
	if err != nil {
		return false, "", "", fmt.Errorf("query execution holds: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var holdID, blocksJSON, exemptJSON, reason string
		if err := rows.Scan(&holdID, &blocksJSON, &exemptJSON, &reason); err != nil {
			return false, "", "", fmt.Errorf("scan execution hold: %w", err)
		}
		blocks := decodeStringSliceJSON(blocksJSON)
		exempt := decodeStringSliceJSON(exemptJSON)
		if containsString(exempt, dagID) {
			continue
		}
		// Empty blocks list = this hold blocks every DAG in the plan.
		// Non-empty list = only the named DAGs are blocked.
		if len(blocks) == 0 || containsString(blocks, dagID) {
			return true, holdID, reason, nil
		}
	}
	return false, "", "", rows.Err()
}

// SupersedePriorPlans transitions every active hold in the session
// whose plan_id differs from newPlanID to ExecutionHoldStatusSuperseded
// in a single transaction. Called at plan_handoff_ingest time: a
// fresh plan takes over the session, so prior-plan remediation
// context is no longer applicable. Returns the count of superseded
// rows for logging/diagnostics; emits one HoldReleased activity per
// superseded row.
//
// Idempotent: if there are no prior-plan active holds (the common
// case), it's a single read-only query + no-op write.
func (s *Store) SupersedePriorPlans(ctx context.Context, sessionID, newPlanID string) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	const selectQ = `SELECT hold_id, COALESCE(plan_id, ''), COALESCE(epoch_id, ''), COALESCE(remediation_case_id, ''), reason, COALESCE(summary, ''), created_by_agent_id, created_by_agent_type
		FROM execution_holds
		WHERE session_id = ? AND status = ? AND plan_id IS NOT NULL AND plan_id != ''
		  AND plan_id != ?`
	var superseded []*ExecutionHoldRecord
	if err := s.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		rows, err := tx.QueryContext(ctx, selectQ, sessionID, string(ExecutionHoldStatusActive), newPlanID)
		if err != nil {
			return err
		}
		for rows.Next() {
			rec := &ExecutionHoldRecord{SessionID: sessionID, Status: ExecutionHoldStatusSuperseded}
			if err := rows.Scan(&rec.HoldID, &rec.PlanID, &rec.EpochID, &rec.RemediationCaseID, &rec.Reason, &rec.Summary, &rec.CreatedByAgentID, &rec.CreatedByAgentType); err != nil {
				rows.Close()
				return err
			}
			superseded = append(superseded, rec)
		}
		rows.Close()
		if len(superseded) == 0 {
			return nil
		}
		const updateQ = `UPDATE execution_holds
			SET status = ?, released_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
			WHERE session_id = ? AND status = ? AND plan_id IS NOT NULL AND plan_id != ''
			  AND plan_id != ?`
		_, err = tx.ExecContext(ctx, updateQ,
			string(ExecutionHoldStatusSuperseded),
			sessionID,
			string(ExecutionHoldStatusActive),
			newPlanID,
		)
		return err
	}); err != nil {
		return 0, fmt.Errorf("supersede prior plans: %w", err)
	}
	if len(superseded) == 0 {
		return 0, nil
	}
	s.notifyHoldsChanged(sessionID)
	for _, rec := range superseded {
		emitHoldReleased(ctx, rec)
	}
	return len(superseded), nil
}

// HoldsNotifyChannel returns a channel that closes on the next
// execution_holds write for sessionID. Returns a fresh channel every
// call — the caller selects on it once and must re-request to wait
// for the next change. The dispatch gate uses this to unblock
// waiters when holds transition.
func (s *Store) HoldsNotifyChannel(sessionID string) <-chan struct{} {
	if s == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	s.holdNotifyMu.Lock()
	defer s.holdNotifyMu.Unlock()
	if s.holdNotify == nil {
		s.holdNotify = make(map[string]chan struct{})
	}
	ch, ok := s.holdNotify[sessionID]
	if !ok || ch == nil {
		ch = make(chan struct{})
		s.holdNotify[sessionID] = ch
	}
	return ch
}

// notifyHoldsChanged closes the current notification channel for
// sessionID and mints a fresh one for the next waiter. Called from
// every execution_holds write path; never from outside the store.
func (s *Store) notifyHoldsChanged(sessionID string) {
	if s == nil {
		return
	}
	s.holdNotifyMu.Lock()
	defer s.holdNotifyMu.Unlock()
	if s.holdNotify == nil {
		s.holdNotify = make(map[string]chan struct{})
	}
	old, ok := s.holdNotify[sessionID]
	s.holdNotify[sessionID] = make(chan struct{})
	if ok && old != nil {
		close(old)
	}
}

// containsString is a small utility local to this file; avoids
// importing slices for one call and keeps the gate query fast.
func containsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func (s *Store) CreateRemediationCase(record *RemediationCaseRecord) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	const q = `INSERT INTO remediation_cases
		(case_id, session_id, epoch_id, hold_id, plan_id, status, summary, request_json, result_json, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q,
			record.CaseID,
			record.SessionID,
			record.EpochID,
			record.HoldID,
			record.PlanID,
			string(record.Status),
			record.Summary,
			encodeJSONValue(record.Request),
			encodeJSONValue(record.Result),
			record.CreatedAt,
			record.UpdatedAt,
			record.CompletedAt,
		)
		return err
	}); err != nil {
		return fmt.Errorf("create remediation case: %w", err)
	}
	emitRemediationOpened(context.Background(), record)
	return nil
}

func (s *Store) UpdateRemediationCase(record *RemediationCaseRecord) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	record.UpdatedAt = time.Now().UTC()
	const q = `UPDATE remediation_cases
		SET plan_id = ?, status = ?, summary = ?, request_json = ?, result_json = ?, updated_at = ?, completed_at = ?
		WHERE case_id = ?`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q,
			record.PlanID,
			string(record.Status),
			record.Summary,
			encodeJSONValue(record.Request),
			encodeJSONValue(record.Result),
			record.UpdatedAt,
			record.CompletedAt,
			record.CaseID,
		)
		return err
	}); err != nil {
		return fmt.Errorf("update remediation case: %w", err)
	}
	switch record.Status {
	case RemediationCaseStatusApplied, RemediationCaseStatusRejected:
		emitRemediationResolved(context.Background(), record)
	}
	return nil
}

func (s *Store) GetRemediationCase(caseID string) (*RemediationCaseRecord, error) {
	const q = `SELECT case_id, session_id, COALESCE(epoch_id, ''), hold_id, COALESCE(plan_id, ''), status, summary, COALESCE(request_json, ''), COALESCE(result_json, ''), created_at, updated_at, completed_at
		FROM remediation_cases WHERE case_id = ?`
	row := &RemediationCaseRecord{}
	var requestJSON, resultJSON string
	err := s.db.QueryRow(q, caseID).Scan(
		&row.CaseID,
		&row.SessionID,
		&row.EpochID,
		&row.HoldID,
		&row.PlanID,
		&row.Status,
		&row.Summary,
		&requestJSON,
		&resultJSON,
		&row.CreatedAt,
		&row.UpdatedAt,
		&row.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get remediation case: %w", err)
	}
	row.Request = decodeRemediationRequest(requestJSON)
	row.Result = decodeRemediationResult(resultJSON)
	return row, nil
}
