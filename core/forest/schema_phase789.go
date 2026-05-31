package forest

import (
	"database/sql"
	"fmt"
)

const (
	forestSchemaVersionPhase789     = 9
	forestSchemaMigrationPhase789   = "memory_forest_phase_7_8_9"
	forestProjectionVersionPhase789 = forestProjectionVersionPhase456 + "_antigenic_v1_retrieval_v2_cursor_v1_agent_projection_v1"
)

func ensureForestPhase789Schema(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{name: "antigenic", fn: ensureForestAntigenicSchema},
		{name: "cursors", fn: ensureForestCursorSchema},
	}
	for _, step := range steps {
		if err := step.fn(db); err != nil {
			return fmt.Errorf("ensure phase 7-9 schema %s: %w", step.name, err)
		}
	}
	return nil
}

func ensureForestAntigenicSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_antigenic_vectors (
			vector_id TEXT PRIMARY KEY,
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			dimension TEXT NOT NULL,
			source_key TEXT NOT NULL,
			magnitude REAL NOT NULL,
			quarantine_factor REAL NOT NULL DEFAULT 0,
			evidence_refs TEXT NOT NULL DEFAULT '',
			policy_version TEXT NOT NULL,
			recorded_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(scope_type, scope_id, dimension, source_key, policy_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_antigenic_scope
			ON forest_antigenic_vectors(scope_type, scope_id, dimension, recorded_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_antigenic_source
			ON forest_antigenic_vectors(source_key, policy_version)`,
		`CREATE TABLE IF NOT EXISTS forest_immunity_vectors (
			vector_id TEXT PRIMARY KEY,
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			dimension TEXT NOT NULL,
			source_key TEXT NOT NULL,
			magnitude REAL NOT NULL,
			recovery_factor REAL NOT NULL DEFAULT 0,
			evidence_refs TEXT NOT NULL DEFAULT '',
			policy_version TEXT NOT NULL,
			recorded_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(scope_type, scope_id, dimension, source_key, policy_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_immunity_scope
			ON forest_immunity_vectors(scope_type, scope_id, dimension, recorded_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_immunity_source
			ON forest_immunity_vectors(source_key, policy_version)`,
		`CREATE TABLE IF NOT EXISTS forest_outbreaks (
			outbreak_id TEXT PRIMARY KEY,
			cluster_id TEXT NOT NULL,
			dimension TEXT NOT NULL,
			growth_rate REAL NOT NULL,
			evidence_refs TEXT NOT NULL DEFAULT '',
			counter_evidence_refs TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			proposed_claim_id TEXT NOT NULL DEFAULT '',
			guardian_review_required INTEGER NOT NULL DEFAULT 0,
			source_seq INTEGER NOT NULL DEFAULT 0,
			policy_version TEXT NOT NULL,
			first_seen_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(cluster_id, dimension, policy_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_outbreaks_cluster
			ON forest_outbreaks(cluster_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_outbreaks_status
			ON forest_outbreaks(status, guardian_review_required, updated_at DESC)`,
	}
	return execForestSchemaStatements(db, stmts)
}

func ensureForestCursorSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_cursors (
			cursor_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			turn_id TEXT NOT NULL DEFAULT '',
			active_cluster_ids TEXT NOT NULL DEFAULT '',
			active_node_ids TEXT NOT NULL DEFAULT '',
			poi_refs TEXT NOT NULL DEFAULT '',
			bridge_refs TEXT NOT NULL DEFAULT '',
			risk_flags TEXT NOT NULL DEFAULT '',
			no_cursor_reason TEXT NOT NULL DEFAULT '',
			policy_version TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_cursors_session
			ON forest_cursors(session_id, task_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_cursors_agent
			ON forest_cursors(agent_id, created_at DESC)`,
		`CREATE TRIGGER IF NOT EXISTS forest_cursors_no_update
			BEFORE UPDATE ON forest_cursors
			BEGIN
				SELECT RAISE(ABORT, 'forest_cursors are immutable snapshots');
			END`,
		`CREATE TRIGGER IF NOT EXISTS forest_cursors_no_delete
			BEFORE DELETE ON forest_cursors
			BEGIN
				SELECT RAISE(ABORT, 'forest_cursors are immutable snapshots');
			END`,
	}
	return execForestSchemaStatements(db, stmts)
}
