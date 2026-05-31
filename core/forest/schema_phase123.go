package forest

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	forestSchemaVersionPhase123     = forestSchemaVersionPhase456
	forestSchemaReplacesVersion     = 1
	forestSchemaMigrationPhase123   = forestSchemaMigrationPhase456
	forestProjectionVersionPhase123 = forestProjectionVersionPhase456
)

func ensurePhase123Schema(db *sql.DB) error {
	return runForestSchemaMigrations(db)
}

func ensureForestSchemaMeta(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS forest_schema_meta (
			meta_key TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL,
			replaces_version INTEGER NOT NULL,
			projection_version TEXT NOT NULL,
			migration_id TEXT NOT NULL,
			code_version TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create forest_schema_meta: %w", err)
	}
	if _, err := db.Exec(`
		INSERT INTO forest_schema_meta
			(meta_key, schema_version, replaces_version, projection_version, migration_id, code_version, applied_at)
		VALUES
			('active', ?, ?, ?, ?, ?, ?)
		ON CONFLICT(meta_key) DO NOTHING
	`, forestSchemaVersionPhase123, forestSchemaReplacesVersion, forestProjectionVersionPhase123, forestSchemaMigrationPhase123, expectedSchemaHash(), time.Now().UTC().Unix()); err != nil {
		return fmt.Errorf("record forest_schema_meta: %w", err)
	}
	return verifyForestSchemaMeta(db)
}

func verifyForestSchemaMeta(db *sql.DB) error {
	row := db.QueryRow(`
		SELECT schema_version, replaces_version, projection_version, migration_id
		FROM forest_schema_meta
		WHERE meta_key = 'active'
	`)
	var schemaVersion, replacesVersion int
	var projectionVersion, migrationID string
	if err := row.Scan(&schemaVersion, &replacesVersion, &projectionVersion, &migrationID); err != nil {
		return fmt.Errorf("verify forest_schema_meta: %w", err)
	}
	if schemaVersion != forestSchemaVersionPhase123 {
		return fmt.Errorf("unsupported forest schema version %d", schemaVersion)
	}
	if replacesVersion != forestSchemaReplacesVersion {
		return fmt.Errorf("unsupported forest replaces_version %d", replacesVersion)
	}
	if projectionVersion != forestProjectionVersionPhase123 {
		return fmt.Errorf("unsupported forest projection version %q", projectionVersion)
	}
	if migrationID != forestSchemaMigrationPhase123 {
		return fmt.Errorf("unsupported forest migration id %q", migrationID)
	}
	return nil
}

func ensureForestLedgerSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_ledger (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			source_kind TEXT NOT NULL,
			source_id TEXT NOT NULL,
			source_key TEXT NOT NULL UNIQUE,
			event_kind TEXT NOT NULL,
			session_id TEXT NOT NULL,
			task_id TEXT NOT NULL DEFAULT '',
			board_id TEXT NOT NULL DEFAULT '',
			subject_type TEXT NOT NULL DEFAULT '',
			subject_id TEXT NOT NULL DEFAULT '',
			actor_uid TEXT NOT NULL DEFAULT '',
			actor_type TEXT NOT NULL DEFAULT '',
			actor_category TEXT NOT NULL DEFAULT '',
			delivery_relationship TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			occurred_at INTEGER NOT NULL,
			payload_hash TEXT NOT NULL DEFAULT '',
			inserted_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_ledger_source
			ON forest_ledger(source_kind, source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_ledger_event
			ON forest_ledger(event_kind, occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_ledger_session
			ON forest_ledger(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_ledger_subject
			ON forest_ledger(subject_type, subject_id, seq)`,
		`CREATE TABLE IF NOT EXISTS forest_ledger_payloads (
			ledger_id TEXT PRIMARY KEY,
			payload_hash TEXT NOT NULL,
			payload TEXT NOT NULL,
			inserted_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS forest_ledger_refs (
			ledger_id TEXT NOT NULL,
			ref_index INTEGER NOT NULL,
			role TEXT NOT NULL,
			ref_type TEXT NOT NULL,
			ref_id TEXT NOT NULL,
			PRIMARY KEY (ledger_id, ref_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_ledger_refs_lookup
			ON forest_ledger_refs(ref_type, ref_id, role)`,
		`CREATE TABLE IF NOT EXISTS forest_ledger_delivery (
			ledger_id TEXT NOT NULL,
			delivery_index INTEGER NOT NULL,
			relationship TEXT NOT NULL DEFAULT '',
			participant_uid TEXT NOT NULL DEFAULT '',
			participant_type TEXT NOT NULL DEFAULT '',
			participant_category TEXT NOT NULL DEFAULT '',
			participant_route_key TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (ledger_id, delivery_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_ledger_delivery_route
			ON forest_ledger_delivery(participant_route_key, relationship)`,
		`CREATE TABLE IF NOT EXISTS forest_projection_offsets (
			projection_name TEXT PRIMARY KEY,
			source_kind TEXT NOT NULL,
			last_seq INTEGER NOT NULL DEFAULT 0,
			last_applied_at INTEGER NOT NULL DEFAULT 0,
			policy_version TEXT NOT NULL DEFAULT '',
			health_status TEXT NOT NULL DEFAULT 'idle',
			last_error TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("ensure forest ledger schema: %w", err)
		}
	}
	return ensureForestLedgerAppendOnly(db)
}

func ensureForestEvidenceSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_artifacts (
			artifact_id TEXT PRIMARY KEY,
			claim_id TEXT NOT NULL DEFAULT '',
			testament_id TEXT NOT NULL DEFAULT '',
			validation_id TEXT NOT NULL DEFAULT '',
			generator_participant TEXT NOT NULL DEFAULT '',
			artifact_name TEXT NOT NULL DEFAULT '',
			artifact_kind TEXT NOT NULL DEFAULT '',
			data_type TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			content_ref TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			validation_status TEXT NOT NULL DEFAULT '',
			last_sequence INTEGER NOT NULL DEFAULT 0,
			first_seen_at INTEGER NOT NULL DEFAULT 0,
			last_seen_at INTEGER NOT NULL DEFAULT 0,
			payload_hash TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_artifacts_claim
			ON forest_artifacts(claim_id, last_sequence DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_artifacts_testament
			ON forest_artifacts(testament_id, last_sequence DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_artifacts_status
			ON forest_artifacts(status, validation_status, last_sequence DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_artifact_edges (
			artifact_id TEXT NOT NULL,
			edge_kind TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT NOT NULL,
			source_ledger_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (artifact_id, edge_kind, target_type, target_id, source_ledger_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_artifact_edges_target
			ON forest_artifact_edges(target_type, target_id, edge_kind)`,
		`CREATE TABLE IF NOT EXISTS forest_validations (
			validation_id TEXT PRIMARY KEY,
			claim_id TEXT NOT NULL DEFAULT '',
			target_artifact_id TEXT NOT NULL DEFAULT '',
			evaluator_participant TEXT NOT NULL DEFAULT '',
			validation_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			result_artifact_id TEXT NOT NULL DEFAULT '',
			failure_reason TEXT NOT NULL DEFAULT '',
			required INTEGER NOT NULL DEFAULT 0,
			last_sequence INTEGER NOT NULL DEFAULT 0,
			first_seen_at INTEGER NOT NULL DEFAULT 0,
			last_seen_at INTEGER NOT NULL DEFAULT 0,
			payload_hash TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_validations_claim
			ON forest_validations(claim_id, last_sequence DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_validations_artifact
			ON forest_validations(target_artifact_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_validations_status
			ON forest_validations(status, required, last_sequence DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_validation_patterns (
			pattern_key TEXT PRIMARY KEY,
			claim_action TEXT NOT NULL DEFAULT '',
			artifact_kind TEXT NOT NULL DEFAULT '',
			validation_type TEXT NOT NULL DEFAULT '',
			success_count INTEGER NOT NULL DEFAULT 0,
			failure_count INTEGER NOT NULL DEFAULT 0,
			last_validation_id TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_validation_patterns_lookup
			ON forest_validation_patterns(claim_action, artifact_kind, validation_type)`,
		`CREATE TABLE IF NOT EXISTS forest_validation_pattern_observations (
			validation_id TEXT PRIMARY KEY,
			pattern_key TEXT NOT NULL,
			status TEXT NOT NULL,
			recorded_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_validation_pattern_observations_pattern
			ON forest_validation_pattern_observations(pattern_key, recorded_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_delta_projection_audit (
			ledger_id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			classification TEXT NOT NULL,
			reason TEXT NOT NULL,
			side_effect TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_delta_projection_audit_action
			ON forest_delta_projection_audit(action, classification)`,
		`CREATE TABLE IF NOT EXISTS forest_evidence_errors (
			id TEXT PRIMARY KEY,
			ledger_id TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			error_kind TEXT NOT NULL,
			message TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_evidence_errors_entity
			ON forest_evidence_errors(entity_type, entity_id, sequence DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("ensure forest evidence schema: %w", err)
		}
	}
	return nil
}

func ensureForestLedgerAppendOnly(db *sql.DB) error {
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS forest_ledger_no_update
			BEFORE UPDATE ON forest_ledger
			BEGIN
				SELECT RAISE(ABORT, 'forest_ledger is append-only');
			END`,
		`CREATE TRIGGER IF NOT EXISTS forest_ledger_no_delete
			BEFORE DELETE ON forest_ledger
			BEGIN
				SELECT RAISE(ABORT, 'forest_ledger is append-only');
			END`,
		`CREATE TRIGGER IF NOT EXISTS forest_ledger_payloads_no_update
			BEFORE UPDATE ON forest_ledger_payloads
			BEGIN
				SELECT RAISE(ABORT, 'forest_ledger_payloads is immutable');
			END`,
		`CREATE TRIGGER IF NOT EXISTS forest_ledger_payloads_no_delete
			BEFORE DELETE ON forest_ledger_payloads
			BEGIN
				SELECT RAISE(ABORT, 'forest_ledger_payloads is immutable');
			END`,
		`CREATE TRIGGER IF NOT EXISTS forest_ledger_refs_no_update
			BEFORE UPDATE ON forest_ledger_refs
			BEGIN
				SELECT RAISE(ABORT, 'forest_ledger_refs is immutable');
			END`,
		`CREATE TRIGGER IF NOT EXISTS forest_ledger_refs_no_delete
			BEFORE DELETE ON forest_ledger_refs
			BEGIN
				SELECT RAISE(ABORT, 'forest_ledger_refs is immutable');
			END`,
		`CREATE TRIGGER IF NOT EXISTS forest_ledger_delivery_no_update
			BEFORE UPDATE ON forest_ledger_delivery
			BEGIN
				SELECT RAISE(ABORT, 'forest_ledger_delivery is immutable');
			END`,
		`CREATE TRIGGER IF NOT EXISTS forest_ledger_delivery_no_delete
			BEFORE DELETE ON forest_ledger_delivery
			BEGIN
				SELECT RAISE(ABORT, 'forest_ledger_delivery is immutable');
			END`,
	}
	for _, trigger := range triggers {
		if _, err := db.Exec(trigger); err != nil {
			return fmt.Errorf("install forest ledger append-only trigger: %w", err)
		}
	}
	return nil
}

func ensureNoProhibitedSQLiteObjects(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT name, type, COALESCE(sql, '')
		FROM sqlite_master
		WHERE sql IS NOT NULL
	`)
	if err != nil {
		return fmt.Errorf("audit sqlite schema objects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, objectType, sqlText string
		if err := rows.Scan(&name, &objectType, &sqlText); err != nil {
			return fmt.Errorf("scan sqlite schema object: %w", err)
		}
		if prohibitedSQLiteObjectSQL(sqlText) {
			return fmt.Errorf("prohibited sqlite extension object %s (%s)", name, objectType)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite schema objects: %w", err)
	}
	return nil
}

func prohibitedSQLiteObjectSQL(sqlText string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(sqlText), " "))
	if strings.Contains(normalized, "create virtual table") {
		return true
	}
	prohibited := []string{
		" using fts3",
		" using fts4",
		" using fts5",
		" using rtree",
		"load_extension(",
		" using sqlite_vec",
		" using sqlite-vss",
		" using spatialite",
	}
	for _, needle := range prohibited {
		if strings.Contains(normalized, needle) {
			return true
		}
	}
	return false
}
