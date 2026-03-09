package architect

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/database"
	"github.com/uptrace/bun"
)

//go:embed control_store_schema.sql
var controlStoreSchemaSQL string

type continuationKind string

const (
	continuationKindGuardianApproval continuationKind = "guardian_tool_approval"
	continuationKindAcceptanceEval   continuationKind = "plan_acceptance_eval"
	continuationKindPlanHandoff      continuationKind = "plan_handoff"
	continuationKindAcademicHandoff  continuationKind = "academic_requirements_handoff"
)

type continuationStatus string

const (
	continuationStatusPending   continuationStatus = "pending"
	continuationStatusCompleted continuationStatus = "completed"
	continuationStatusFailed    continuationStatus = "failed"
)

type ArchitectControlStore struct {
	db   *database.BunSQLiteDB
	path string
}

type ArchitectControlStoreConfig struct {
	Path              string
	MaxOpenConns      int
	MaxIdleConns      int
	ConnMaxLifetime   time.Duration
	BusyTimeout       time.Duration
	WriteRetryMax     int
	WriteRetryBackoff time.Duration
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

type architectPlanRow struct {
	bun.BaseModel `bun:"table:plans,alias:plans"`

	PlanID    string    `bun:"plan_id,pk"`
	SessionID string    `bun:"session_id,notnull"`
	Status    string    `bun:"status,notnull"`
	Epoch     int64     `bun:"epoch,notnull"`
	PlanJSON  string    `bun:"plan_json,notnull"`
	UpdatedAt time.Time `bun:"updated_at,notnull"`
	SyncedAt  time.Time `bun:"synced_at,nullzero"`
}

type architectContinuationRow struct {
	bun.BaseModel `bun:"table:continuations,alias:continuations"`

	ContinuationID          string     `bun:"continuation_id,pk"`
	Kind                    string     `bun:"kind,notnull"`
	State                   string     `bun:"state,notnull"`
	PlanID                  *string    `bun:"plan_id"`
	SessionID               string     `bun:"session_id,notnull"`
	TargetAgentID           string     `bun:"target_agent_id,notnull"`
	ResponseCorrelationID   string     `bun:"response_correlation_id,notnull"`
	InvocationCorrelationID *string    `bun:"invocation_correlation_id"`
	ToolName                *string    `bun:"tool_name"`
	ToolCallID              *string    `bun:"tool_call_id"`
	RawArguments            *string    `bun:"raw_arguments"`
	RequestJSON             *string    `bun:"request_json"`
	ResponseJSON            *string    `bun:"response_json"`
	ErrorText               *string    `bun:"error_text"`
	CreatedAt               time.Time  `bun:"created_at,notnull"`
	ExpiresAt               *time.Time `bun:"expires_at"`
	CompletedAt             *time.Time `bun:"completed_at"`
}

type architectContinuationEventRow struct {
	bun.BaseModel `bun:"table:continuation_events,alias:continuation_events"`

	ID             int64     `bun:"id,pk,autoincrement"`
	ContinuationID string    `bun:"continuation_id,notnull"`
	State          string    `bun:"state,notnull"`
	Note           *string   `bun:"note"`
	PayloadJSON    *string   `bun:"payload_json"`
	CreatedAt      time.Time `bun:"created_at,nullzero"`
}

func defaultArchitectControlStoreConfig(dbPath string) ArchitectControlStoreConfig {
	cfg := database.DefaultBunSQLiteConfig(dbPath)
	return ArchitectControlStoreConfig{
		Path:              dbPath,
		MaxOpenConns:      cfg.MaxOpen,
		MaxIdleConns:      cfg.MaxIdle,
		ConnMaxLifetime:   cfg.MaxLifetime,
		BusyTimeout:       cfg.BusyTimeout,
		WriteRetryMax:     cfg.WriteRetryMax,
		WriteRetryBackoff: cfg.WriteRetryBackoff,
	}
}

func OpenArchitectControlStore(cfg ArchitectControlStoreConfig) (*ArchitectControlStore, error) {
	db, err := database.OpenBunSQLite(database.BunSQLiteConfig{
		Path:              cfg.Path,
		MaxOpen:           cfg.MaxOpenConns,
		MaxIdle:           cfg.MaxIdleConns,
		MaxLifetime:       cfg.ConnMaxLifetime,
		BusyTimeout:       cfg.BusyTimeout,
		EnableWAL:         true,
		ForeignKeys:       true,
		CacheSize:         -2000,
		Synchronous:       "normal",
		WriteRetryMax:     cfg.WriteRetryMax,
		WriteRetryBackoff: cfg.WriteRetryBackoff,
	})
	if err != nil {
		return nil, fmt.Errorf("architect control store: open: %w", err)
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
	if err := s.db.ExecSchema(context.Background(), controlStoreSchemaSQL); err != nil {
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
	row := newArchitectPlanRow(plan, string(encoded))

	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewInsert().
			Model(row).
			On("CONFLICT(plan_id) DO UPDATE").
			Set("session_id = EXCLUDED.session_id").
			Set("status = EXCLUDED.status").
			Set("epoch = EXCLUDED.epoch").
			Set("plan_json = EXCLUDED.plan_json").
			Set("updated_at = EXCLUDED.updated_at").
			Set("synced_at = CURRENT_TIMESTAMP").
			Returning("").
			Exec(ctx)
		return err
	}); err != nil {
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
	if record.State == "" {
		record.State = continuationStatusPending
	}

	row := newArchitectContinuationRow(record)
	event := newArchitectContinuationEventRow(record.ID, string(record.State), "upsert", record.RequestJSON)

	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := upsertContinuationRow(ctx, tx, row); err != nil {
			return err
		}
		return insertContinuationEvent(ctx, tx, event)
	}); err != nil {
		return fmt.Errorf("architect control store: put continuation: %w", err)
	}

	return nil
}

func (s *ArchitectControlStore) GetContinuationByResponseCorrelation(correlationID string) (*ArchitectContinuation, error) {
	if s == nil || s.db == nil || correlationID == "" {
		return nil, nil
	}

	row := new(architectContinuationRow)
	err := s.db.Bun().
		NewSelect().
		Model(row).
		Where("response_correlation_id = ?", correlationID).
		Limit(1).
		Scan(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("architect control store: get continuation: %w", err)
	}
	return row.toContinuation(), nil
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

	note := "completed"
	if state == continuationStatusFailed {
		note = "failed"
	}
	event := newArchitectContinuationEventRow(record.ID, string(state), note, responseJSON)

	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewUpdate().
			Model((*architectContinuationRow)(nil)).
			Set("state = ?", string(state)).
			Set("response_json = ?", nullableString(record.ResponseJSON)).
			Set("error_text = ?", nullableString(record.ErrorText)).
			Set("completed_at = ?", nullableTimePtr(record.CompletedAt)).
			Where("continuation_id = ?", record.ID).
			Returning("").
			Exec(ctx)
		if err != nil {
			return err
		}
		return insertContinuationEvent(ctx, tx, event)
	}); err != nil {
		return fmt.Errorf("architect control store: complete continuation: %w", err)
	}

	return nil
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

	event := newArchitectContinuationEventRow(continuationID, state, note, payloadJSON)
	if err := s.db.RunInWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return insertContinuationEvent(ctx, tx, event)
	}); err != nil {
		return fmt.Errorf("architect control store: append continuation event: %w", err)
	}

	return nil
}

func newArchitectPlanRow(plan *DesignPlan, encoded string) *architectPlanRow {
	return &architectPlanRow{
		PlanID:    plan.ID,
		SessionID: plan.SessionID,
		Status:    plan.Status.String(),
		Epoch:     int64(plan.Epoch),
		PlanJSON:  encoded,
		UpdatedAt: plan.UpdatedAt.UTC(),
	}
}

func newArchitectContinuationRow(record *ArchitectContinuation) *architectContinuationRow {
	return &architectContinuationRow{
		ContinuationID:          record.ID,
		Kind:                    string(record.Kind),
		State:                   string(record.State),
		PlanID:                  nullableString(record.PlanID),
		SessionID:               record.SessionID,
		TargetAgentID:           record.TargetAgentID,
		ResponseCorrelationID:   record.ResponseCorrelationID,
		InvocationCorrelationID: nullableString(record.InvocationCorrelationID),
		ToolName:                nullableString(record.ToolName),
		ToolCallID:              nullableString(record.ToolCallID),
		RawArguments:            nullableString(record.RawArguments),
		RequestJSON:             nullableString(record.RequestJSON),
		ResponseJSON:            nullableString(record.ResponseJSON),
		ErrorText:               nullableString(record.ErrorText),
		CreatedAt:               record.CreatedAt.UTC(),
		ExpiresAt:               nullableTimePtr(record.ExpiresAt),
		CompletedAt:             nullableTimePtr(record.CompletedAt),
	}
}

func (r *architectContinuationRow) toContinuation() *ArchitectContinuation {
	if r == nil {
		return nil
	}
	return &ArchitectContinuation{
		ID:                      r.ContinuationID,
		Kind:                    continuationKind(r.Kind),
		State:                   continuationStatus(r.State),
		PlanID:                  derefString(r.PlanID),
		SessionID:               r.SessionID,
		TargetAgentID:           r.TargetAgentID,
		ResponseCorrelationID:   r.ResponseCorrelationID,
		InvocationCorrelationID: derefString(r.InvocationCorrelationID),
		ToolName:                derefString(r.ToolName),
		ToolCallID:              derefString(r.ToolCallID),
		RawArguments:            derefString(r.RawArguments),
		RequestJSON:             derefString(r.RequestJSON),
		ResponseJSON:            derefString(r.ResponseJSON),
		ErrorText:               derefString(r.ErrorText),
		CreatedAt:               r.CreatedAt,
		ExpiresAt:               derefTime(r.ExpiresAt),
		CompletedAt:             derefTime(r.CompletedAt),
	}
}

func newArchitectContinuationEventRow(
	continuationID string,
	state string,
	note string,
	payloadJSON string,
) *architectContinuationEventRow {
	return &architectContinuationEventRow{
		ContinuationID: continuationID,
		State:          state,
		Note:           nullableString(note),
		PayloadJSON:    nullableString(payloadJSON),
	}
}

func upsertContinuationRow(ctx context.Context, tx bun.Tx, row *architectContinuationRow) error {
	if row == nil {
		return nil
	}
	_, err := tx.NewInsert().
		Model(row).
		On("CONFLICT(response_correlation_id) DO UPDATE").
		Set("continuation_id = EXCLUDED.continuation_id").
		Set("kind = EXCLUDED.kind").
		Set("state = EXCLUDED.state").
		Set("plan_id = EXCLUDED.plan_id").
		Set("session_id = EXCLUDED.session_id").
		Set("target_agent_id = EXCLUDED.target_agent_id").
		Set("invocation_correlation_id = EXCLUDED.invocation_correlation_id").
		Set("tool_name = EXCLUDED.tool_name").
		Set("tool_call_id = EXCLUDED.tool_call_id").
		Set("raw_arguments = EXCLUDED.raw_arguments").
		Set("request_json = EXCLUDED.request_json").
		Set("response_json = EXCLUDED.response_json").
		Set("error_text = EXCLUDED.error_text").
		Set("created_at = EXCLUDED.created_at").
		Set("expires_at = EXCLUDED.expires_at").
		Set("completed_at = EXCLUDED.completed_at").
		Returning("").
		Exec(ctx)
	return err
}

func insertContinuationEvent(ctx context.Context, tx bun.Tx, event *architectContinuationEventRow) error {
	if event == nil {
		return nil
	}
	_, err := tx.NewInsert().
		Model(event).
		Returning("").
		Exec(ctx)
	return err
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableTimePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
