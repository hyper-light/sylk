package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// DAGExecutionRow mirrors the dag_executions table.
type DAGExecutionRow struct {
	ID             string     `json:"id"`
	PlanID         string     `json:"plan_id"`
	SessionID      string     `json:"session_id"`
	Name           string     `json:"name"`
	State          string     `json:"state"`
	PolicyJSON     string     `json:"policy_json"`
	DAGJSON        string     `json:"dag_json"`
	CurrentLayer   int        `json:"current_layer"`
	TotalLayers    int        `json:"total_layers"`
	NodesTotal     int        `json:"nodes_total"`
	NodesSucceeded int        `json:"nodes_succeeded"`
	NodesFailed    int        `json:"nodes_failed"`
	NodesSkipped   int        `json:"nodes_skipped"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// TaskUpdateEntry represents a single task pipeline update for buffer/store.
type TaskUpdateEntry struct {
	ID        string    `json:"id"`
	DAGID     string    `json:"dag_id"`
	TaskID    string    `json:"task_id"`
	NodeID    string    `json:"node_id"`
	AgentID   string    `json:"agent_id"`
	AgentType string    `json:"agent_type"`
	Status    string    `json:"status"`
	Progress  float64   `json:"progress"`
	Message   string    `json:"message"`
	Output    any       `json:"output,omitempty"`
	Error     string    `json:"error,omitempty"`
	Attempt   int       `json:"attempt"`
	Timestamp time.Time `json:"timestamp"`
}

// Cost estimates the Ristretto storage cost derived from field sizes.
func (e *TaskUpdateEntry) Cost() int64 {
	// Base struct overhead (pointers, numerics, time)
	cost := int64(96)
	cost += int64(len(e.ID))
	cost += int64(len(e.DAGID))
	cost += int64(len(e.TaskID))
	cost += int64(len(e.NodeID))
	cost += int64(len(e.AgentID))
	cost += int64(len(e.AgentType))
	cost += int64(len(e.Status))
	cost += int64(len(e.Message))
	cost += int64(len(e.Error))
	if e.Output != nil {
		data, _ := json.Marshal(e.Output)
		cost += int64(len(data))
	}
	return cost
}

// --- DAG Executions ---

// InsertDAGExecution inserts a new DAG execution row.
func (s *Store) InsertDAGExecution(dagID, planID, sessionID, name, policyJSON, dagJSON string, totalLayers, nodesTotal int) error {
	const q = `INSERT INTO dag_executions
		(id, plan_id, session_id, name, state, policy_json, dag_json, total_layers, nodes_total, started_at)
		VALUES (?, ?, ?, ?, 'running', ?, ?, ?, ?, CURRENT_TIMESTAMP)`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q, dagID, planID, sessionID, name, policyJSON, dagJSON, totalLayers, nodesTotal)
		return err
	}); err != nil {
		return fmt.Errorf("insert dag execution: %w", err)
	}
	return nil
}

// UpdateDAGState updates the terminal state and error of a DAG execution.
func (s *Store) UpdateDAGState(dagID, state, errMsg string) error {
	const q = `UPDATE dag_executions SET state = ?, error = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q, state, errMsg, dagID)
		return err
	}); err != nil {
		return fmt.Errorf("update dag state: %w", err)
	}
	return nil
}

// UpdateDAGProgress updates layer and node counters for a running DAG.
func (s *Store) UpdateDAGProgress(dagID string, currentLayer, succeeded, failed, skipped int) error {
	const q = `UPDATE dag_executions
		SET current_layer = ?, nodes_succeeded = ?, nodes_failed = ?, nodes_skipped = ?
		WHERE id = ?`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q, currentLayer, succeeded, failed, skipped, dagID)
		return err
	}); err != nil {
		return fmt.Errorf("update dag progress: %w", err)
	}
	return nil
}

// GetDAGExecution retrieves a DAG execution by ID.
func (s *Store) GetDAGExecution(dagID string) (*DAGExecutionRow, error) {
	const q = `SELECT id, plan_id, session_id, name, state, policy_json, dag_json,
		current_layer, total_layers, nodes_total, nodes_succeeded, nodes_failed, nodes_skipped,
		COALESCE(error, ''), created_at, started_at, completed_at
		FROM dag_executions WHERE id = ?`
	row := &DAGExecutionRow{}
	err := s.db.QueryRow(q, dagID).Scan(
		&row.ID, &row.PlanID, &row.SessionID, &row.Name, &row.State,
		&row.PolicyJSON, &row.DAGJSON, &row.CurrentLayer, &row.TotalLayers,
		&row.NodesTotal, &row.NodesSucceeded, &row.NodesFailed, &row.NodesSkipped,
		&row.Error, &row.CreatedAt, &row.StartedAt, &row.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get dag execution: %w", err)
	}
	return row, nil
}

// ListDAGExecutions lists DAG executions for a session, newest first.
func (s *Store) ListDAGExecutions(sessionID string, limit int) ([]*DAGExecutionRow, error) {
	const q = `SELECT id, plan_id, session_id, name, state, policy_json, dag_json,
		current_layer, total_layers, nodes_total, nodes_succeeded, nodes_failed, nodes_skipped,
		COALESCE(error, ''), created_at, started_at, completed_at
		FROM dag_executions WHERE session_id = ? ORDER BY created_at DESC LIMIT ?`
	rows, err := s.db.Query(q, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list dag executions: %w", err)
	}
	defer rows.Close()

	var results []*DAGExecutionRow
	for rows.Next() {
		row := &DAGExecutionRow{}
		if err := rows.Scan(
			&row.ID, &row.PlanID, &row.SessionID, &row.Name, &row.State,
			&row.PolicyJSON, &row.DAGJSON, &row.CurrentLayer, &row.TotalLayers,
			&row.NodesTotal, &row.NodesSucceeded, &row.NodesFailed, &row.NodesSkipped,
			&row.Error, &row.CreatedAt, &row.StartedAt, &row.CompletedAt,
		); err != nil {
			return results, fmt.Errorf("scan dag execution: %w", err)
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// --- DAG Revisions ---

// InsertDAGRevision records a mid-flight DAG modification.
func (s *Store) InsertDAGRevision(dagID string, revision int, diffJSON, reason string) error {
	const q = `INSERT INTO dag_revisions (dag_id, revision, diff_json, reason) VALUES (?, ?, ?, ?)`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q, dagID, revision, diffJSON, reason)
		return err
	}); err != nil {
		return fmt.Errorf("insert dag revision: %w", err)
	}
	return nil
}

// --- Task Updates (cold store) ---

// InsertTaskUpdates batch-inserts task updates in a single transaction.
func (s *Store) InsertTaskUpdates(updates []TaskUpdateEntry) error {
	if len(updates) == 0 {
		return nil
	}

	const q = `INSERT INTO task_updates
		(dag_id, task_id, node_id, agent_id, agent_type, status, progress, message, output_json, error, attempt, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		stmt, err := tx.PrepareContext(ctx, q)
		if err != nil {
			return fmt.Errorf("prepare task update: %w", err)
		}
		defer stmt.Close()

		for i := range updates {
			u := &updates[i]
			var outputJSON string
			if u.Output != nil {
				data, _ := json.Marshal(u.Output)
				outputJSON = string(data)
			}
			if _, err := stmt.ExecContext(ctx,
				u.DAGID, u.TaskID, u.NodeID, u.AgentID, u.AgentType,
				u.Status, u.Progress, u.Message, outputJSON, u.Error,
				u.Attempt, u.Timestamp,
			); err != nil {
				return fmt.Errorf("exec task update: %w", err)
			}
		}
		return nil
	})
}

// QueryTaskUpdates retrieves recent task updates by task ID.
func (s *Store) QueryTaskUpdates(taskID string, limit int) ([]TaskUpdateEntry, error) {
	const q = `SELECT dag_id, task_id, node_id, agent_id, agent_type, status,
		progress, COALESCE(message, ''), COALESCE(output_json, ''), COALESCE(error, ''),
		attempt, created_at
		FROM task_updates WHERE task_id = ? ORDER BY created_at DESC LIMIT ?`

	rows, err := s.db.Query(q, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("query task updates: %w", err)
	}
	defer rows.Close()

	return scanTaskUpdates(rows)
}

// QueryTaskUpdatesSince retrieves task updates after a given time.
func (s *Store) QueryTaskUpdatesSince(taskID string, since time.Time) ([]TaskUpdateEntry, error) {
	const q = `SELECT dag_id, task_id, node_id, agent_id, agent_type, status,
		progress, COALESCE(message, ''), COALESCE(output_json, ''), COALESCE(error, ''),
		attempt, created_at
		FROM task_updates WHERE task_id = ? AND created_at > ? ORDER BY created_at DESC`

	rows, err := s.db.Query(q, taskID, since)
	if err != nil {
		return nil, fmt.Errorf("query task updates since: %w", err)
	}
	defer rows.Close()

	return scanTaskUpdates(rows)
}

func scanTaskUpdates(rows *sql.Rows) ([]TaskUpdateEntry, error) {
	var results []TaskUpdateEntry
	for rows.Next() {
		var e TaskUpdateEntry
		var outputJSON string
		if err := rows.Scan(
			&e.DAGID, &e.TaskID, &e.NodeID, &e.AgentID, &e.AgentType,
			&e.Status, &e.Progress, &e.Message, &outputJSON, &e.Error,
			&e.Attempt, &e.Timestamp,
		); err != nil {
			return results, fmt.Errorf("scan task update: %w", err)
		}
		if outputJSON != "" {
			var out any
			if json.Unmarshal([]byte(outputJSON), &out) == nil {
				e.Output = out
			}
		}
		results = append(results, e)
	}
	return results, rows.Err()
}

// --- Pipeline State ---

// UpsertPipelineState inserts or updates agent pipeline state.
func (s *Store) UpsertPipelineState(agentID, dagID, nodeID, stateJSON string) error {
	const q = `INSERT INTO pipeline_state (agent_id, dag_id, node_id, state_json, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(agent_id, dag_id, node_id) DO UPDATE SET state_json = excluded.state_json, updated_at = CURRENT_TIMESTAMP`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q, agentID, dagID, nodeID, stateJSON)
		return err
	}); err != nil {
		return fmt.Errorf("upsert pipeline state: %w", err)
	}
	return nil
}

// GetPipelineState retrieves pipeline state JSON.
func (s *Store) GetPipelineState(agentID, dagID, nodeID string) (string, error) {
	const q = `SELECT state_json FROM pipeline_state WHERE agent_id = ? AND dag_id = ? AND node_id = ?`
	var stateJSON string
	err := s.db.QueryRow(q, agentID, dagID, nodeID).Scan(&stateJSON)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get pipeline state: %w", err)
	}
	return stateJSON, nil
}

// --- Plan Versions ---

// InsertPlanVersion records a plan version snapshot.
func (s *Store) InsertPlanVersion(planID string, version int, sessionID, status, planJSON string, taskCount int) error {
	const q = `INSERT OR REPLACE INTO plan_versions (plan_id, version, session_id, status, plan_json, task_count)
		VALUES (?, ?, ?, ?, ?, ?)`
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, q, planID, version, sessionID, status, planJSON, taskCount)
		return err
	}); err != nil {
		return fmt.Errorf("insert plan version: %w", err)
	}
	return nil
}

// --- GC ---

// DeleteOldTaskUpdates removes task updates older than the given time.
func (s *Store) DeleteOldTaskUpdates(before time.Time) (int64, error) {
	const q = `DELETE FROM task_updates WHERE created_at < ?`
	var rows int64
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.ExecContext(ctx, q, before)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		rows = n
		return nil
	}); err != nil {
		return 0, fmt.Errorf("delete old task updates: %w", err)
	}
	return rows, nil
}
