package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type PlanHandoffReceiptRecord struct {
	ReceiptID        string
	SessionID        string
	PlanID           string
	Revision         int
	CorrelationID    string
	RequesterAgentID string
	Attempt          int
	Status           agentshared.PlanHandoffReceiptStatus
	WorkflowID       string
	DAGID            string
	TaskCount        int
	LayerCount       int
	ErrorText        string
	AcceptedAt       time.Time
	DAGBuiltAt       time.Time
	SubmittedAt      time.Time
	RunningAt        time.Time
	FailedAt         time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type beginPlanHandoffReceiptResult struct {
	receipt   *PlanHandoffReceiptRecord
	duplicate bool
}

var handoffReceiptTimestampSetters = map[agentshared.PlanHandoffReceiptStatus]func(*PlanHandoffReceiptRecord, time.Time){
	agentshared.PlanHandoffReceiptStatusAccepted: func(record *PlanHandoffReceiptRecord, now time.Time) {
		record.AcceptedAt = now
	},
	agentshared.PlanHandoffReceiptStatusDAGBuilt: func(record *PlanHandoffReceiptRecord, now time.Time) {
		record.DAGBuiltAt = now
	},
	agentshared.PlanHandoffReceiptStatusSubmitted: func(record *PlanHandoffReceiptRecord, now time.Time) {
		record.SubmittedAt = now
	},
	agentshared.PlanHandoffReceiptStatusRunning: func(record *PlanHandoffReceiptRecord, now time.Time) {
		record.RunningAt = now
	},
	agentshared.PlanHandoffReceiptStatusFailed: func(record *PlanHandoffReceiptRecord, now time.Time) {
		record.FailedAt = now
	},
}

func (r *PlanHandoffReceiptRecord) payload() *agentshared.PlanHandoffReceipt {
	if r == nil {
		return nil
	}
	return &agentshared.PlanHandoffReceipt{
		ReceiptID:   r.ReceiptID,
		SessionID:   r.SessionID,
		PlanID:      r.PlanID,
		Revision:    r.Revision,
		Attempt:     r.Attempt,
		Status:      r.Status,
		WorkflowID:  r.WorkflowID,
		DAGID:       r.DAGID,
		TaskCount:   r.TaskCount,
		LayerCount:  r.LayerCount,
		ErrorText:   r.ErrorText,
		AcceptedAt:  r.AcceptedAt,
		DAGBuiltAt:  r.DAGBuiltAt,
		SubmittedAt: r.SubmittedAt,
		RunningAt:   r.RunningAt,
		FailedAt:    r.FailedAt,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

func (s *Store) BeginPlanHandoffReceipt(sessionID, planID string, revision int, correlationID string, requesterAgentID string) (*PlanHandoffReceiptRecord, bool, error) {
	if err := requirePlanHandoffReceiptStore(s); err != nil {
		return nil, false, err
	}
	normalizedSessionID, normalizedPlanID, normalizedCorrelationID, err := normalizePlanHandoffReceiptKey(sessionID, planID, correlationID)
	if err != nil {
		return nil, false, err
	}
	result, err := s.beginPlanHandoffReceiptTx(
		normalizedSessionID,
		normalizedPlanID,
		revision,
		normalizedCorrelationID,
		strings.TrimSpace(requesterAgentID),
		time.Now().UTC(),
	)
	if err != nil {
		return nil, false, fmt.Errorf("begin handoff receipt: %w", err)
	}
	return result.receipt, result.duplicate, nil
}

func (s *Store) GetPlanHandoffReceipt(sessionID, planID string, revision int) (*PlanHandoffReceiptRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	return queryPlanHandoffReceipt(context.Background(), s.db.Bun(), strings.TrimSpace(sessionID), strings.TrimSpace(planID), revision)
}

func (s *Store) UpdatePlanHandoffReceiptStage(
	sessionID, planID string,
	revision int,
	status agentshared.PlanHandoffReceiptStatus,
	workflowID, dagID string,
	taskCount, layerCount int,
	errorText string,
) (*PlanHandoffReceiptRecord, error) {
	if err := requirePlanHandoffReceiptStore(s); err != nil {
		return nil, err
	}
	normalizedSessionID, normalizedPlanID, _, err := normalizePlanHandoffReceiptKey(sessionID, planID, "")
	if err != nil {
		return nil, err
	}
	updated, err := s.updatePlanHandoffReceiptStageTx(
		normalizedSessionID,
		normalizedPlanID,
		revision,
		status,
		workflowID,
		dagID,
		taskCount,
		layerCount,
		errorText,
		time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("update handoff receipt stage: %w", err)
	}
	return updated, nil
}

func (s *Store) MarkPlanHandoffReceiptRunningByDAGID(dagID string) (*PlanHandoffReceiptRecord, error) {
	if err := requirePlanHandoffReceiptStore(s); err != nil {
		return nil, err
	}
	normalizedDAGID := strings.TrimSpace(dagID)
	if normalizedDAGID == "" {
		return nil, nil
	}
	updated, err := s.markPlanHandoffReceiptRunningByDAGIDTx(normalizedDAGID, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("mark handoff receipt running: %w", err)
	}
	return updated, nil
}

func (s *Store) ListOpenPlanHandoffReceipts() ([]*PlanHandoffReceiptRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.queryOpenPlanHandoffReceiptRows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlanHandoffReceiptRows(rows)
}

func requirePlanHandoffReceiptStore(s *Store) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("orchestrator store is not initialized")
	}
	return nil
}

func normalizePlanHandoffReceiptKey(sessionID, planID, correlationID string) (string, string, string, error) {
	normalizedSessionID := strings.TrimSpace(sessionID)
	normalizedPlanID := strings.TrimSpace(planID)
	if normalizedSessionID == "" || normalizedPlanID == "" {
		return "", "", "", fmt.Errorf("session_id and plan_id are required")
	}
	return normalizedSessionID, normalizedPlanID, strings.TrimSpace(correlationID), nil
}

func (s *Store) beginPlanHandoffReceiptTx(
	sessionID, planID string,
	revision int,
	correlationID string,
	requesterAgentID string,
	now time.Time,
) (*beginPlanHandoffReceiptResult, error) {
	var result *beginPlanHandoffReceiptResult
	err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		next, txErr := s.beginPlanHandoffReceiptInTx(ctx, tx, sessionID, planID, revision, correlationID, requesterAgentID, now)
		if txErr != nil {
			return txErr
		}
		result = next
		return nil
	})
	return result, err
}

func (s *Store) beginPlanHandoffReceiptInTx(
	ctx context.Context,
	tx bun.Tx,
	sessionID, planID string,
	revision int,
	correlationID string,
	requesterAgentID string,
	now time.Time,
) (*beginPlanHandoffReceiptResult, error) {
	existing, err := queryPlanHandoffReceipt(ctx, tx, sessionID, planID, revision)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		record := newPlanHandoffReceiptRecord(sessionID, planID, revision, correlationID, requesterAgentID, now)
		return newBeginPlanHandoffReceiptResult(record, false, insertPlanHandoffReceipt(ctx, tx, record))
	}
	return reopenOrReusePlanHandoffReceipt(ctx, tx, existing, correlationID, requesterAgentID, now)
}

func newPlanHandoffReceiptRecord(
	sessionID, planID string,
	revision int,
	correlationID string,
	requesterAgentID string,
	now time.Time,
) *PlanHandoffReceiptRecord {
	return &PlanHandoffReceiptRecord{
		ReceiptID:        "handoff_" + uuid.NewString(),
		SessionID:        sessionID,
		PlanID:           planID,
		Revision:         revision,
		CorrelationID:    correlationID,
		RequesterAgentID: requesterAgentID,
		Attempt:          1,
		Status:           agentshared.PlanHandoffReceiptStatusAccepted,
		AcceptedAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func reopenOrReusePlanHandoffReceipt(
	ctx context.Context,
	tx bun.Tx,
	existing *PlanHandoffReceiptRecord,
	correlationID string,
	requesterAgentID string,
	now time.Time,
) (*beginPlanHandoffReceiptResult, error) {
	if existing.Status != agentshared.PlanHandoffReceiptStatusFailed {
		if !assignPlanHandoffReceiptRequesterAgentID(existing, requesterAgentID, now) {
			return &beginPlanHandoffReceiptResult{receipt: existing, duplicate: true}, nil
		}
		return newBeginPlanHandoffReceiptResult(existing, true, updatePlanHandoffReceipt(ctx, tx, existing))
	}
	record := reopenFailedPlanHandoffReceipt(existing, correlationID, requesterAgentID, now)
	return newBeginPlanHandoffReceiptResult(record, false, updatePlanHandoffReceipt(ctx, tx, record))
}

func reopenFailedPlanHandoffReceipt(
	record *PlanHandoffReceiptRecord,
	correlationID string,
	requesterAgentID string,
	now time.Time,
) *PlanHandoffReceiptRecord {
	record.Attempt++
	record.CorrelationID = correlationID
	assignPlanHandoffReceiptRequesterAgentID(record, requesterAgentID, now)
	record.Status = agentshared.PlanHandoffReceiptStatusAccepted
	record.WorkflowID = ""
	record.DAGID = ""
	record.TaskCount = 0
	record.LayerCount = 0
	record.ErrorText = ""
	record.AcceptedAt = now
	record.DAGBuiltAt = time.Time{}
	record.SubmittedAt = time.Time{}
	record.RunningAt = time.Time{}
	record.FailedAt = time.Time{}
	record.UpdatedAt = now
	return record
}

func assignPlanHandoffReceiptRequesterAgentID(
	record *PlanHandoffReceiptRecord,
	requesterAgentID string,
	now time.Time,
) bool {
	if record == nil {
		return false
	}
	normalized := strings.TrimSpace(requesterAgentID)
	if normalized == "" || normalized == record.RequesterAgentID {
		return false
	}
	record.RequesterAgentID = normalized
	record.UpdatedAt = now
	return true
}

func newBeginPlanHandoffReceiptResult(
	record *PlanHandoffReceiptRecord,
	duplicate bool,
	err error,
) (*beginPlanHandoffReceiptResult, error) {
	if err != nil {
		return nil, err
	}
	return &beginPlanHandoffReceiptResult{receipt: record, duplicate: duplicate}, nil
}

func (s *Store) updatePlanHandoffReceiptStageTx(
	sessionID, planID string,
	revision int,
	status agentshared.PlanHandoffReceiptStatus,
	workflowID, dagID string,
	taskCount, layerCount int,
	errorText string,
	now time.Time,
) (*PlanHandoffReceiptRecord, error) {
	var updated *PlanHandoffReceiptRecord
	err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		record, txErr := s.updatePlanHandoffReceiptStageInTx(ctx, tx, sessionID, planID, revision, status, workflowID, dagID, taskCount, layerCount, errorText, now)
		if txErr != nil {
			return txErr
		}
		updated = record
		return nil
	})
	return updated, err
}

func (s *Store) updatePlanHandoffReceiptStageInTx(
	ctx context.Context,
	tx bun.Tx,
	sessionID, planID string,
	revision int,
	status agentshared.PlanHandoffReceiptStatus,
	workflowID, dagID string,
	taskCount, layerCount int,
	errorText string,
	now time.Time,
) (*PlanHandoffReceiptRecord, error) {
	record, err := queryPlanHandoffReceipt(ctx, tx, sessionID, planID, revision)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("plan handoff receipt not found for %s/%s rev %d", sessionID, planID, revision)
	}
	applyPlanHandoffReceiptStage(record, status, workflowID, dagID, taskCount, layerCount, errorText, now)
	return record, updatePlanHandoffReceipt(ctx, tx, record)
}

func applyPlanHandoffReceiptStage(
	record *PlanHandoffReceiptRecord,
	status agentshared.PlanHandoffReceiptStatus,
	workflowID, dagID string,
	taskCount, layerCount int,
	errorText string,
	now time.Time,
) {
	record.Status = status
	applyPlanHandoffReceiptWorkflow(record, workflowID)
	applyPlanHandoffReceiptDAG(record, dagID)
	applyPlanHandoffReceiptCounts(record, taskCount, layerCount)
	record.ErrorText = strings.TrimSpace(errorText)
	record.UpdatedAt = now
	applyPlanHandoffReceiptTimestamp(record, status, now)
}

func applyPlanHandoffReceiptWorkflow(record *PlanHandoffReceiptRecord, workflowID string) {
	if workflow := strings.TrimSpace(workflowID); workflow != "" {
		record.WorkflowID = workflow
	}
}

func applyPlanHandoffReceiptDAG(record *PlanHandoffReceiptRecord, dagID string) {
	if dag := strings.TrimSpace(dagID); dag != "" {
		record.DAGID = dag
	}
}

func applyPlanHandoffReceiptCounts(record *PlanHandoffReceiptRecord, taskCount, layerCount int) {
	if taskCount > 0 {
		record.TaskCount = taskCount
	}
	if layerCount > 0 {
		record.LayerCount = layerCount
	}
}

func applyPlanHandoffReceiptTimestamp(
	record *PlanHandoffReceiptRecord,
	status agentshared.PlanHandoffReceiptStatus,
	now time.Time,
) {
	if setter, ok := handoffReceiptTimestampSetters[status]; ok {
		setter(record, now)
	}
}

func (s *Store) markPlanHandoffReceiptRunningByDAGIDTx(
	dagID string,
	now time.Time,
) (*PlanHandoffReceiptRecord, error) {
	var updated *PlanHandoffReceiptRecord
	err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		record, txErr := markPlanHandoffReceiptRunningByDAGIDInTx(ctx, tx, dagID, now)
		if txErr != nil {
			return txErr
		}
		updated = record
		return nil
	})
	return updated, err
}

func markPlanHandoffReceiptRunningByDAGIDInTx(
	ctx context.Context,
	tx bun.Tx,
	dagID string,
	now time.Time,
) (*PlanHandoffReceiptRecord, error) {
	record, err := queryPlanHandoffReceiptByDAGID(ctx, tx, dagID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, nil
	}
	record.Status = agentshared.PlanHandoffReceiptStatusRunning
	record.RunningAt = now
	record.UpdatedAt = now
	return record, updatePlanHandoffReceipt(ctx, tx, record)
}

func (s *Store) queryOpenPlanHandoffReceiptRows() (*sql.Rows, error) {
	rows, err := s.db.Query(
		`SELECT receipt_id, session_id, plan_id, revision, COALESCE(correlation_id, ''), COALESCE(requester_agent_id, ''), attempt, status,
		        COALESCE(workflow_id, ''), COALESCE(dag_id, ''), task_count, layer_count, COALESCE(error_text, ''),
		        accepted_at, dag_built_at, submitted_at, running_at, failed_at, created_at, updated_at
		   FROM plan_handoff_receipts
		  WHERE status IN (?, ?, ?, ?)
		  ORDER BY updated_at DESC`,
		string(agentshared.PlanHandoffReceiptStatusAccepted),
		string(agentshared.PlanHandoffReceiptStatusDAGBuilt),
		string(agentshared.PlanHandoffReceiptStatusSubmitted),
		string(agentshared.PlanHandoffReceiptStatusRunning),
	)
	if err != nil {
		return nil, fmt.Errorf("list open handoff receipts: %w", err)
	}
	return rows, nil
}

func scanPlanHandoffReceiptRows(rows *sql.Rows) ([]*PlanHandoffReceiptRecord, error) {
	var result []*PlanHandoffReceiptRecord
	for rows.Next() {
		record, err := scanPlanHandoffReceipt(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func queryPlanHandoffReceipt(ctx context.Context, db bun.IDB, sessionID, planID string, revision int) (*PlanHandoffReceiptRecord, error) {
	row := db.QueryRowContext(ctx,
		`SELECT receipt_id, session_id, plan_id, revision, COALESCE(correlation_id, ''), COALESCE(requester_agent_id, ''), attempt, status,
		        COALESCE(workflow_id, ''), COALESCE(dag_id, ''), task_count, layer_count, COALESCE(error_text, ''),
		        accepted_at, dag_built_at, submitted_at, running_at, failed_at, created_at, updated_at
		   FROM plan_handoff_receipts
		  WHERE session_id = ? AND plan_id = ? AND revision = ?
		  LIMIT 1`,
		sessionID, planID, revision,
	)
	record, err := scanPlanHandoffReceipt(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query handoff receipt: %w", err)
	}
	return record, nil
}

func queryPlanHandoffReceiptByDAGID(ctx context.Context, db bun.IDB, dagID string) (*PlanHandoffReceiptRecord, error) {
	row := db.QueryRowContext(ctx,
		`SELECT receipt_id, session_id, plan_id, revision, COALESCE(correlation_id, ''), COALESCE(requester_agent_id, ''), attempt, status,
		        COALESCE(workflow_id, ''), COALESCE(dag_id, ''), task_count, layer_count, COALESCE(error_text, ''),
		        accepted_at, dag_built_at, submitted_at, running_at, failed_at, created_at, updated_at
		   FROM plan_handoff_receipts
		  WHERE dag_id = ?
		  LIMIT 1`,
		dagID,
	)
	record, err := scanPlanHandoffReceipt(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query handoff receipt by dag: %w", err)
	}
	return record, nil
}

func insertPlanHandoffReceipt(ctx context.Context, tx bun.Tx, record *PlanHandoffReceiptRecord) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO plan_handoff_receipts
		   (receipt_id, session_id, plan_id, revision, correlation_id, requester_agent_id, attempt, status, workflow_id, dag_id,
		    task_count, layer_count, error_text, accepted_at, dag_built_at, submitted_at, running_at, failed_at,
		    created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ReceiptID, record.SessionID, record.PlanID, record.Revision, nullableString(record.CorrelationID),
		nullableString(record.RequesterAgentID), record.Attempt, string(record.Status), nullableString(record.WorkflowID), nullableString(record.DAGID),
		record.TaskCount, record.LayerCount, nullableString(record.ErrorText),
		nullableTime(record.AcceptedAt), nullableTime(record.DAGBuiltAt), nullableTime(record.SubmittedAt),
		nullableTime(record.RunningAt), nullableTime(record.FailedAt), record.CreatedAt, record.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert handoff receipt: %w", err)
	}
	return nil
}

func updatePlanHandoffReceipt(ctx context.Context, tx bun.Tx, record *PlanHandoffReceiptRecord) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE plan_handoff_receipts
		    SET correlation_id = ?, requester_agent_id = ?, attempt = ?, status = ?, workflow_id = ?, dag_id = ?, task_count = ?, layer_count = ?,
		        error_text = ?, accepted_at = ?, dag_built_at = ?, submitted_at = ?, running_at = ?, failed_at = ?,
		        updated_at = ?
		  WHERE receipt_id = ?`,
		nullableString(record.CorrelationID), nullableString(record.RequesterAgentID), record.Attempt, string(record.Status), nullableString(record.WorkflowID),
		nullableString(record.DAGID), record.TaskCount, record.LayerCount, nullableString(record.ErrorText),
		nullableTime(record.AcceptedAt), nullableTime(record.DAGBuiltAt), nullableTime(record.SubmittedAt),
		nullableTime(record.RunningAt), nullableTime(record.FailedAt), record.UpdatedAt, record.ReceiptID,
	)
	if err != nil {
		return fmt.Errorf("update handoff receipt: %w", err)
	}
	return nil
}

func scanPlanHandoffReceipt(scan func(dest ...any) error) (*PlanHandoffReceiptRecord, error) {
	var (
		record                 PlanHandoffReceiptRecord
		status                 string
		acceptedAt, dagBuiltAt sql.NullTime
		submittedAt, runningAt sql.NullTime
		failedAt               sql.NullTime
	)
	if err := scan(
		&record.ReceiptID,
		&record.SessionID,
		&record.PlanID,
		&record.Revision,
		&record.CorrelationID,
		&record.RequesterAgentID,
		&record.Attempt,
		&status,
		&record.WorkflowID,
		&record.DAGID,
		&record.TaskCount,
		&record.LayerCount,
		&record.ErrorText,
		&acceptedAt,
		&dagBuiltAt,
		&submittedAt,
		&runningAt,
		&failedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	record.Status = agentshared.PlanHandoffReceiptStatus(status)
	record.AcceptedAt = nullableTimeValue(acceptedAt)
	record.DAGBuiltAt = nullableTimeValue(dagBuiltAt)
	record.SubmittedAt = nullableTimeValue(submittedAt)
	record.RunningAt = nullableTimeValue(runningAt)
	record.FailedAt = nullableTimeValue(failedAt)
	return &record, nil
}

func (s *Store) BindPlanHandoffReceiptRequesterAgentID(
	sessionID, planID string,
	revision int,
	requesterAgentID string,
) (*PlanHandoffReceiptRecord, error) {
	if err := requirePlanHandoffReceiptStore(s); err != nil {
		return nil, err
	}
	normalizedSessionID, normalizedPlanID, _, err := normalizePlanHandoffReceiptKey(sessionID, planID, "")
	if err != nil {
		return nil, err
	}
	normalizedRequester := strings.TrimSpace(requesterAgentID)
	if normalizedRequester == "" {
		return s.GetPlanHandoffReceipt(normalizedSessionID, normalizedPlanID, revision)
	}
	var updated *PlanHandoffReceiptRecord
	err = s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		record, txErr := queryPlanHandoffReceipt(ctx, tx, normalizedSessionID, normalizedPlanID, revision)
		if txErr != nil {
			return txErr
		}
		if record == nil {
			return nil
		}
		if !assignPlanHandoffReceiptRequesterAgentID(record, normalizedRequester, time.Now().UTC()) {
			updated = record
			return nil
		}
		if txErr := updatePlanHandoffReceipt(ctx, tx, record); txErr != nil {
			return txErr
		}
		updated = record
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("bind handoff receipt requester agent id: %w", err)
	}
	return updated, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func nullableTimeValue(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func nullableString(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
