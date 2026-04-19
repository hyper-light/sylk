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
		(hold_id, session_id, epoch_id, remediation_case_id, status, reason, summary, created_by_agent_id, created_by_agent_type, released_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q,
			record.HoldID,
			record.SessionID,
			record.EpochID,
			record.RemediationCaseID,
			string(record.Status),
			record.Reason,
			record.Summary,
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
	return nil
}

func (s *Store) UpdateExecutionHold(record *ExecutionHoldRecord) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	record.UpdatedAt = time.Now().UTC()
	const q = `UPDATE execution_holds
		SET epoch_id = ?, remediation_case_id = ?, status = ?, reason = ?, summary = ?, released_at = ?, updated_at = ?
		WHERE hold_id = ?`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q,
			record.EpochID,
			record.RemediationCaseID,
			string(record.Status),
			record.Reason,
			record.Summary,
			record.ReleasedAt,
			record.UpdatedAt,
			record.HoldID,
		)
		return err
	}); err != nil {
		return fmt.Errorf("update execution hold: %w", err)
	}
	return nil
}

func (s *Store) GetActiveExecutionHold(sessionID string) (*ExecutionHoldRecord, error) {
	const q = `SELECT hold_id, session_id, COALESCE(epoch_id, ''), COALESCE(remediation_case_id, ''), status, reason, COALESCE(summary, ''), created_by_agent_id, created_by_agent_type, released_at, created_at, updated_at
		FROM execution_holds
		WHERE session_id = ? AND status = ?
		ORDER BY created_at DESC LIMIT 1`
	row := &ExecutionHoldRecord{}
	err := s.db.QueryRow(q, sessionID, string(ExecutionHoldStatusActive)).Scan(
		&row.HoldID,
		&row.SessionID,
		&row.EpochID,
		&row.RemediationCaseID,
		&row.Status,
		&row.Reason,
		&row.Summary,
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
	return row, nil
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
