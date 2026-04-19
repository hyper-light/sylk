package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/adalundhe/sylk/core/database"
)

// SQLiteSchema is the DDL for the agent_activity table. Append-only,
// indexed on the access patterns the lens layer uses.
//
// The schema lives on the existing orchestrator BunSQLite handle so it
// inherits the WAL + busy_timeout + writeMu policy fixed in the
// bun_sqlite DSN repair. There is no separate "fabric database."
const SQLiteSchema = `
CREATE TABLE IF NOT EXISTS agent_activity (
    id                       TEXT PRIMARY KEY,
    session_id               TEXT NOT NULL,
    timestamp_ns             INTEGER NOT NULL,
    resolution               TEXT NOT NULL,
    actor_agent_id           TEXT NOT NULL,
    actor_agent_type         TEXT NOT NULL,
    actor_pipeline_id        TEXT,
    action_kind              TEXT NOT NULL,
    subject_json             TEXT NOT NULL,
    payload_json             TEXT,
    caused                   TEXT,
    resolves                 TEXT,
    causal_chain_json        TEXT,
    state                    TEXT NOT NULL DEFAULT 'point',
    confidence               TEXT,
    evidence_json            TEXT,
    source_table             TEXT,
    source_id                TEXT,
    subject_path_prefix      TEXT,
    subject_target_agent     TEXT,
    subject_target_artifact  TEXT,
    subject_domain           TEXT
);

CREATE INDEX IF NOT EXISTS idx_aa_session_kind_time
    ON agent_activity(session_id, action_kind, timestamp_ns);
CREATE INDEX IF NOT EXISTS idx_aa_actor_time
    ON agent_activity(actor_agent_id, timestamp_ns);
CREATE INDEX IF NOT EXISTS idx_aa_caused
    ON agent_activity(caused);
CREATE INDEX IF NOT EXISTS idx_aa_resolves
    ON agent_activity(resolves);
CREATE INDEX IF NOT EXISTS idx_aa_inflight
    ON agent_activity(state) WHERE state = 'in_flight';
CREATE INDEX IF NOT EXISTS idx_aa_subject_path
    ON agent_activity(session_id, subject_path_prefix);
CREATE INDEX IF NOT EXISTS idx_aa_subject_target_agent
    ON agent_activity(session_id, subject_target_agent);
CREATE INDEX IF NOT EXISTS idx_aa_source
    ON agent_activity(source_table, source_id);
CREATE INDEX IF NOT EXISTS idx_aa_subject_domain
    ON agent_activity(session_id, subject_domain);
`

// SQLiteStore persists durable activities (Fine + Medium + Coarse) on
// the orchestrator's BunSQLite handle. Atomic-tier activities never
// reach this store — they live only in the in-memory ring buffer.
//
// Implements both the Sink interface (write side) and the Source
// interface (read side for lenses).
type SQLiteStore struct {
	db *database.BunSQLiteDB
}

// NewSQLiteStore wraps an existing BunSQLite handle.
func NewSQLiteStore(db *database.BunSQLiteDB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// EnsureSchema applies the SQLiteSchema DDL to the underlying DB.
// Idempotent (uses IF NOT EXISTS). Safe to call from the orchestrator
// store's Migrate step.
func (s *SQLiteStore) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.ExecSchema(ctx, SQLiteSchema)
}

// Append writes a durable activity. Atomic-tier activities are dropped
// (they belong to the Ristretto firehose only). The append uses
// RunInWriteTx so it picks up the chokepoint instrumentation
// automatically — but to avoid recursive emission, the activity-store
// write does NOT itself emit a store_write_committed activity.
func (s *SQLiteStore) Append(ctx context.Context, a AgentActivity) {
	if s == nil || s.db == nil {
		return
	}
	if !a.Resolution.IsDurable() {
		return
	}
	// Best-effort: durable emission failures must not propagate to the
	// caller's chokepoint span. Logging is intentionally absent here
	// because the surrounding chokepoint is already capturing the
	// outer operation; an error in fabric persistence is a fabric-tier
	// concern, surfaced via the same mechanism (an error_emitted
	// activity from the recovery wrapper, captured at the next layer).
	_ = s.append(ctx, a)
}

func (s *SQLiteStore) append(ctx context.Context, a AgentActivity) error {
	subjectJSON, err := json.Marshal(a.Subject)
	if err != nil {
		return fmt.Errorf("marshal subject: %w", err)
	}
	chainJSON, err := json.Marshal(a.CausalChain)
	if err != nil {
		return fmt.Errorf("marshal causal chain: %w", err)
	}
	evidenceJSON, err := json.Marshal(a.Evidence)
	if err != nil {
		return fmt.Errorf("marshal evidence: %w", err)
	}

	caused := nullableString(activityIDPtr(a.Caused))
	resolves := nullableString(activityIDPtr(a.Resolves))

	// Bypass the chokepoint-instrumented RunInWriteTx to avoid
	// emitting a store_write_committed activity for the activity
	// store's own writes (which would recurse). The raw bun handle
	// still respects WAL/busy_timeout because it's the same DB.
	_, execErr := s.db.SQLDB().ExecContext(
		ctx,
		`INSERT OR IGNORE INTO agent_activity (
            id, session_id, timestamp_ns, resolution,
            actor_agent_id, actor_agent_type, actor_pipeline_id,
            action_kind, subject_json, payload_json,
            caused, resolves, causal_chain_json,
            state, confidence, evidence_json,
            source_table, source_id,
            subject_path_prefix, subject_target_agent,
            subject_target_artifact, subject_domain
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(a.ID),
		string(a.SessionID),
		a.Timestamp.UnixNano(),
		string(a.Resolution),
		a.Actor.AgentID,
		a.Actor.AgentType,
		nullableString(stringPtr(a.Actor.PipelineID)),
		string(a.Action),
		string(subjectJSON),
		nullableString(rawJSONString(a.Payload)),
		caused,
		resolves,
		string(chainJSON),
		string(a.State),
		nullableString(stringPtr(string(a.Confidence))),
		string(evidenceJSON),
		nullableString(stringPtr(a.SourceTable)),
		nullableString(stringPtr(a.SourceID)),
		nullableString(stringPtr(a.Subject.PathPrefix)),
		nullableString(stringPtr(a.Subject.TargetAgent)),
		nullableString(stringPtr(a.Subject.TargetArtifact)),
		nullableString(stringPtr(a.Subject.Domain)),
	)
	return execErr
}

// FilterActivities implements Source.
func (s *SQLiteStore) FilterActivities(ctx context.Context, filter QueryFilter) ([]AgentActivity, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	query, args := buildFilterQuery(filter)
	rows, err := s.db.SQLDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("activity filter: %w", err)
	}
	defer rows.Close()

	var out []AgentActivity
	for rows.Next() {
		a, scanErr := scanActivityRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetActivity implements Source.
func (s *SQLiteStore) GetActivity(ctx context.Context, id ActivityID) (*AgentActivity, error) {
	if s == nil || s.db == nil || id == "" {
		return nil, nil
	}
	rows, err := s.db.SQLDB().QueryContext(
		ctx,
		buildSelectColumns()+" FROM agent_activity WHERE id = ? LIMIT 1",
		string(id),
	)
	if err != nil {
		return nil, fmt.Errorf("activity get: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	a, scanErr := scanActivityRow(rows)
	if scanErr != nil {
		return nil, scanErr
	}
	return &a, nil
}

// LatestActivityID implements Source. Returns "" when no activity
// matches the filter.
func (s *SQLiteStore) LatestActivityID(ctx context.Context, filter QueryFilter) (ActivityID, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	query, args := buildFilterQuery(filter)
	// Latest by timestamp_ns desc, limit 1, ID only.
	query = strings.Replace(query, buildSelectColumns(), "SELECT id", 1)
	query = strings.TrimSuffix(query, " ORDER BY timestamp_ns ASC")
	if !strings.Contains(query, "ORDER BY") {
		query += " ORDER BY timestamp_ns DESC LIMIT 1"
	} else {
		query = strings.Replace(query, "ORDER BY timestamp_ns ASC", "ORDER BY timestamp_ns DESC LIMIT 1", 1)
	}
	row := s.db.SQLDB().QueryRowContext(ctx, query, args...)
	var id sql.NullString
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("activity latest: %w", err)
	}
	return ActivityID(id.String), nil
}

// ─── helpers ──────────────────────────────────────────────────────────

func buildSelectColumns() string {
	return `SELECT id, session_id, timestamp_ns, resolution,
        actor_agent_id, actor_agent_type, actor_pipeline_id,
        action_kind, subject_json, payload_json,
        caused, resolves, causal_chain_json,
        state, confidence, evidence_json,
        source_table, source_id,
        subject_path_prefix, subject_target_agent,
        subject_target_artifact, subject_domain`
}

func buildFilterQuery(f QueryFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if f.SessionID != "" {
		clauses = append(clauses, "session_id = ?")
		args = append(args, string(f.SessionID))
	}
	if len(f.ActionKinds) > 0 {
		placeholders := make([]string, len(f.ActionKinds))
		for i, k := range f.ActionKinds {
			placeholders[i] = "?"
			args = append(args, string(k))
		}
		clauses = append(clauses, "action_kind IN ("+strings.Join(placeholders, ", ")+")")
	}
	if len(f.States) > 0 {
		placeholders := make([]string, len(f.States))
		for i, st := range f.States {
			placeholders[i] = "?"
			args = append(args, string(st))
		}
		clauses = append(clauses, "state IN ("+strings.Join(placeholders, ", ")+")")
	}
	if f.ActorAgentID != "" {
		clauses = append(clauses, "actor_agent_id = ?")
		args = append(args, f.ActorAgentID)
	}
	if f.ActorAgentType != "" {
		clauses = append(clauses, "actor_agent_type = ?")
		args = append(args, f.ActorAgentType)
	}
	if f.ActorPipelineID != "" {
		clauses = append(clauses, "actor_pipeline_id = ?")
		args = append(args, f.ActorPipelineID)
	}
	if f.SubjectPathPrefix != "" {
		clauses = append(clauses, "subject_path_prefix LIKE ?")
		args = append(args, f.SubjectPathPrefix+"%")
	}
	if f.SubjectTargetAgent != "" {
		clauses = append(clauses, "subject_target_agent = ?")
		args = append(args, f.SubjectTargetAgent)
	}
	if f.SubjectTargetArtifact != "" {
		clauses = append(clauses, "subject_target_artifact = ?")
		args = append(args, f.SubjectTargetArtifact)
	}
	if f.SubjectDomain != "" {
		clauses = append(clauses, "subject_domain = ?")
		args = append(args, f.SubjectDomain)
	}
	if !f.Since.IsZero() {
		clauses = append(clauses, "timestamp_ns >= ?")
		args = append(args, f.Since.UnixNano())
	}
	if !f.Until.IsZero() {
		clauses = append(clauses, "timestamp_ns <= ?")
		args = append(args, f.Until.UnixNano())
	}
	if len(f.Confidences) > 0 {
		placeholders := make([]string, len(f.Confidences))
		for i, c := range f.Confidences {
			placeholders[i] = "?"
			args = append(args, string(c))
		}
		clauses = append(clauses, "confidence IN ("+strings.Join(placeholders, ", ")+")")
	}
	if f.Caused != nil && *f.Caused != "" {
		clauses = append(clauses, "caused = ?")
		args = append(args, string(*f.Caused))
	}
	if f.Resolves != nil && *f.Resolves != "" {
		clauses = append(clauses, "resolves = ?")
		args = append(args, string(*f.Resolves))
	}

	q := buildSelectColumns() + " FROM agent_activity"
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY timestamp_ns ASC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	return q, args
}

func scanActivityRow(rows *sql.Rows) (AgentActivity, error) {
	var (
		a            AgentActivity
		timestampNs  int64
		resolution   string
		pipelineID   sql.NullString
		subjectJSON  string
		payloadJSON  sql.NullString
		caused       sql.NullString
		resolves     sql.NullString
		chainJSON    sql.NullString
		state        string
		confidence   sql.NullString
		evidenceJSON sql.NullString
		sourceTable  sql.NullString
		sourceID     sql.NullString
		pathPrefix   sql.NullString
		targetAgent  sql.NullString
		targetArt    sql.NullString
		subDomain    sql.NullString
	)
	if err := rows.Scan(
		(*string)(&a.ID),
		(*string)(&a.SessionID),
		&timestampNs,
		&resolution,
		&a.Actor.AgentID,
		&a.Actor.AgentType,
		&pipelineID,
		(*string)(&a.Action),
		&subjectJSON,
		&payloadJSON,
		&caused,
		&resolves,
		&chainJSON,
		&state,
		&confidence,
		&evidenceJSON,
		&sourceTable,
		&sourceID,
		&pathPrefix,
		&targetAgent,
		&targetArt,
		&subDomain,
	); err != nil {
		return AgentActivity{}, fmt.Errorf("scan activity row: %w", err)
	}
	a.Timestamp = time.Unix(0, timestampNs)
	a.Resolution = Resolution(resolution)
	a.State = ActivityState(state)
	a.Actor.PipelineID = pipelineID.String
	if confidence.Valid {
		a.Confidence = Confidence(confidence.String)
	}
	a.SourceTable = sourceTable.String
	a.SourceID = sourceID.String

	if subjectJSON != "" {
		_ = json.Unmarshal([]byte(subjectJSON), &a.Subject)
	}
	// Restore denormalized hot-lookup fields directly so callers don't
	// need to inspect Subject when the indexed columns suffice.
	if pathPrefix.Valid {
		a.Subject.PathPrefix = pathPrefix.String
	}
	if targetAgent.Valid {
		a.Subject.TargetAgent = targetAgent.String
	}
	if targetArt.Valid {
		a.Subject.TargetArtifact = targetArt.String
	}
	if subDomain.Valid {
		a.Subject.Domain = subDomain.String
	}
	if payloadJSON.Valid {
		a.Payload = json.RawMessage(payloadJSON.String)
	}
	if caused.Valid && caused.String != "" {
		c := ActivityID(caused.String)
		a.Caused = &c
	}
	if resolves.Valid && resolves.String != "" {
		r := ActivityID(resolves.String)
		a.Resolves = &r
	}
	if chainJSON.Valid && chainJSON.String != "" {
		_ = json.Unmarshal([]byte(chainJSON.String), &a.CausalChain)
	}
	if evidenceJSON.Valid && evidenceJSON.String != "" {
		_ = json.Unmarshal([]byte(evidenceJSON.String), &a.Evidence)
	}
	return a, nil
}

// ─── small typed-pointer helpers ──────────────────────────────────────

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func activityIDPtr(p *ActivityID) *string {
	if p == nil || *p == "" {
		return nil
	}
	s := string(*p)
	return &s
}

func rawJSONString(raw json.RawMessage) *string {
	if len(raw) == 0 {
		return nil
	}
	s := string(raw)
	return &s
}

func nullableString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// Compile-time enforcement: the SQLiteStore satisfies both Sink and
// Source contracts. The bun.Tx import keeps the package linked to the
// orchestrator's Bun handle even when this file's helpers don't use
// the typed transaction.
var (
	_ Sink   = (*SQLiteStore)(nil)
	_ Source = (*SQLiteStore)(nil)
	_        = (*bun.Tx)(nil)
)
