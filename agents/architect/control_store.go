package architect

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed control_store_schema.sql
var controlStoreSchemaSQL string

type continuationKind string

const (
	continuationKindGuardianApproval continuationKind = "guardian_tool_approval"
	continuationKindAcceptanceEval   continuationKind = "plan_acceptance_eval"
	continuationKindPlanHandoff      continuationKind = "plan_handoff"
)

type continuationStatus string

const (
	continuationStatusPending   continuationStatus = "pending"
	continuationStatusCompleted continuationStatus = "completed"
	continuationStatusFailed    continuationStatus = "failed"
)

type ArchitectControlStore struct {
	db   *sql.DB
	path string
}

type ArchitectControlStoreConfig struct {
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type ArchitectContinuation struct {
	ID                      string
	Kind                    continuationKind
	State                   continuationStatus
	PlanID                  string
	SessionID               string
	TargetAgentID           string
	ResponseCorrelationID   string
	InvocationCorrelationID string
	ToolName                string
	ToolCallID              string
	RawArguments            string
	RequestJSON             string
	ResponseJSON            string
	ErrorText               string
	CreatedAt               time.Time
	ExpiresAt               time.Time
	CompletedAt             time.Time
}

func defaultArchitectControlStoreConfig(dbPath string) ArchitectControlStoreConfig {
	return ArchitectControlStoreConfig{
		Path:            dbPath,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
	}
}

func OpenArchitectControlStore(cfg ArchitectControlStoreConfig) (*ArchitectControlStore, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_synchronous=normal", cfg.Path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("architect control store: open: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("architect control store: ping: %w", err)
	}
	store := &ArchitectControlStore{db: db, path: cfg.Path}
	if err := store.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *ArchitectControlStore) Migrate() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("architect control store is not initialized")
	}
	if _, err := s.db.Exec(controlStoreSchemaSQL); err != nil {
		return fmt.Errorf("architect control store: migrate: %w", err)
	}
	return nil
}

func (s *ArchitectControlStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *ArchitectControlStore) UpsertPlan(plan *DesignPlan) error {
	if s == nil || s.db == nil || plan == nil {
		return nil
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("architect control store: marshal plan: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO plans (plan_id, session_id, status, epoch, plan_json, updated_at, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(plan_id) DO UPDATE SET
			session_id = excluded.session_id,
			status = excluded.status,
			epoch = excluded.epoch,
			plan_json = excluded.plan_json,
			updated_at = excluded.updated_at,
			synced_at = CURRENT_TIMESTAMP
	`, plan.ID, plan.SessionID, plan.Status.String(), int64(plan.Epoch), string(encoded), plan.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("architect control store: upsert plan: %w", err)
	}
	return nil
}

func (s *ArchitectControlStore) PutContinuation(record *ArchitectContinuation) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		INSERT INTO continuations (
			continuation_id, kind, state, plan_id, session_id, target_agent_id,
			response_correlation_id, invocation_correlation_id, tool_name, tool_call_id,
			raw_arguments, request_json, response_json, error_text, created_at, expires_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(response_correlation_id) DO UPDATE SET
			continuation_id = excluded.continuation_id,
			kind = excluded.kind,
			state = excluded.state,
			plan_id = excluded.plan_id,
			session_id = excluded.session_id,
			target_agent_id = excluded.target_agent_id,
			invocation_correlation_id = excluded.invocation_correlation_id,
			tool_name = excluded.tool_name,
			tool_call_id = excluded.tool_call_id,
			raw_arguments = excluded.raw_arguments,
			request_json = excluded.request_json,
			response_json = excluded.response_json,
			error_text = excluded.error_text,
			created_at = excluded.created_at,
			expires_at = excluded.expires_at,
			completed_at = excluded.completed_at
	`, record.ID, string(record.Kind), string(record.State), record.PlanID, record.SessionID,
		record.TargetAgentID, record.ResponseCorrelationID, record.InvocationCorrelationID,
		record.ToolName, record.ToolCallID, record.RawArguments, record.RequestJSON,
		record.ResponseJSON, record.ErrorText, record.CreatedAt.UTC(), nullableTime(record.ExpiresAt),
		nullableTime(record.CompletedAt))
	if err != nil {
		return fmt.Errorf("architect control store: put continuation: %w", err)
	}
	return s.appendContinuationEvent(record.ID, string(record.State), "upsert", record.RequestJSON)
}

func (s *ArchitectControlStore) GetContinuationByResponseCorrelation(correlationID string) (*ArchitectContinuation, error) {
	if s == nil || s.db == nil || correlationID == "" {
		return nil, nil
	}
	row := s.db.QueryRow(`
		SELECT continuation_id, kind, state, plan_id, session_id, target_agent_id,
		       response_correlation_id, invocation_correlation_id, tool_name, tool_call_id,
		       raw_arguments, request_json, response_json, error_text, created_at, expires_at, completed_at
		FROM continuations
		WHERE response_correlation_id = ?
	`, correlationID)
	record := &ArchitectContinuation{}
	var planID, invocationCorrelationID, toolName, toolCallID, rawArgs, requestJSON, responseJSON, errorText sql.NullString
	var expiresAt, completedAt sql.NullTime
	var kind, state string
	if err := row.Scan(
		&record.ID, &kind, &state, &planID, &record.SessionID, &record.TargetAgentID,
		&record.ResponseCorrelationID, &invocationCorrelationID, &toolName, &toolCallID,
		&rawArgs, &requestJSON, &responseJSON, &errorText, &record.CreatedAt, &expiresAt, &completedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("architect control store: get continuation: %w", err)
	}
	record.Kind = continuationKind(kind)
	record.State = continuationStatus(state)
	record.PlanID = planID.String
	record.InvocationCorrelationID = invocationCorrelationID.String
	record.ToolName = toolName.String
	record.ToolCallID = toolCallID.String
	record.RawArguments = rawArgs.String
	record.RequestJSON = requestJSON.String
	record.ResponseJSON = responseJSON.String
	record.ErrorText = errorText.String
	if expiresAt.Valid {
		record.ExpiresAt = expiresAt.Time
	}
	if completedAt.Valid {
		record.CompletedAt = completedAt.Time
	}
	return record, nil
}

func (s *ArchitectControlStore) CompleteContinuation(
	record *ArchitectContinuation,
	state continuationStatus,
	responseJSON string,
	errText string,
) error {
	if s == nil || s.db == nil || record == nil {
		return nil
	}
	record.State = state
	record.ResponseJSON = responseJSON
	record.ErrorText = errText
	record.CompletedAt = time.Now().UTC()
	_, err := s.db.Exec(`
		UPDATE continuations
		SET state = ?, response_json = ?, error_text = ?, completed_at = ?
		WHERE continuation_id = ?
	`, string(state), responseJSON, errText, record.CompletedAt, record.ID)
	if err != nil {
		return fmt.Errorf("architect control store: complete continuation: %w", err)
	}
	note := "completed"
	if state == continuationStatusFailed {
		note = "failed"
	}
	return s.appendContinuationEvent(record.ID, string(state), note, responseJSON)
}

func (s *ArchitectControlStore) appendContinuationEvent(
	continuationID string,
	state string,
	note string,
	payloadJSON string,
) error {
	if s == nil || s.db == nil || continuationID == "" {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO continuation_events (continuation_id, state, note, payload_json)
		VALUES (?, ?, ?, ?)
	`, continuationID, state, note, payloadJSON)
	if err != nil {
		return fmt.Errorf("architect control store: append continuation event: %w", err)
	}
	return nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
