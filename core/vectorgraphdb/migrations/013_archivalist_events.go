// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// ArchivalistEventsMigrationVersion is the version number for this migration.
	ArchivalistEventsMigrationVersion = 13
	// ArchivalistEventsMigrationName is the human-readable name for this migration.
	ArchivalistEventsMigrationName = "archivalist_events"
)

// ArchivalistEventsMigration adds tables for storing activity events,
// index events, and session summaries for the Archivalist agent.
type ArchivalistEventsMigration struct {
	db *sql.DB
}

// NewArchivalistEventsMigration creates a new migration instance.
func NewArchivalistEventsMigration(db *sql.DB) *ArchivalistEventsMigration {
	return &ArchivalistEventsMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *ArchivalistEventsMigration) Apply() error {
	if err := m.ensureSchemaVersionTable(); err != nil {
		return fmt.Errorf("ensure schema_version table: %w", err)
	}

	applied, err := m.isApplied()
	if err != nil {
		return fmt.Errorf("check if applied: %w", err)
	}
	if applied {
		return nil
	}

	return m.applyMigration()
}

// ensureSchemaVersionTable creates the schema_version table if it doesn't exist.
func (m *ArchivalistEventsMigration) ensureSchemaVersionTable() error {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// isApplied checks if this migration has already been applied.
func (m *ArchivalistEventsMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		ArchivalistEventsMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *ArchivalistEventsMigration) applyMigration() error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := m.executeMigrationSteps(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// executeMigrationSteps runs all migration steps in sequence.
func (m *ArchivalistEventsMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createActivityEventsTable(tx); err != nil {
		return err
	}
	if err := m.createIndexEventsTable(tx); err != nil {
		return err
	}
	if err := m.createSessionSummariesTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// =============================================================================
// AE.6.1 Activity Events Table
// =============================================================================

// createActivityEventsTable creates the activity_events table for storing
// agent activity events with metadata and relationships.
func (m *ArchivalistEventsMigration) createActivityEventsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS activity_events (
			id TEXT PRIMARY KEY,
			event_type INTEGER NOT NULL,
			timestamp TEXT NOT NULL,
			session_id TEXT NOT NULL,
			agent_id TEXT,
			content TEXT NOT NULL,
			summary TEXT,
			category TEXT,
			file_paths_json TEXT,
			keywords_json TEXT,
			related_ids_json TEXT,
			outcome INTEGER NOT NULL DEFAULT 0,
			importance REAL NOT NULL DEFAULT 0.5,
			data_json TEXT,
			created_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create activity_events table: %w", err)
	}
	return nil
}

// =============================================================================
// AE.6.2 Index Events Table
// =============================================================================

// createIndexEventsTable creates the index_events table for storing
// indexing operation events and their metrics.
func (m *ArchivalistEventsMigration) createIndexEventsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS index_events (
			id TEXT PRIMARY KEY,
			event_type INTEGER NOT NULL,
			timestamp TEXT NOT NULL,
			session_id TEXT NOT NULL,
			index_type INTEGER NOT NULL,
			index_version INTEGER NOT NULL,
			prev_version INTEGER NOT NULL,
			root_path TEXT NOT NULL,
			duration_ns INTEGER NOT NULL,
			files_indexed INTEGER NOT NULL DEFAULT 0,
			files_removed INTEGER NOT NULL DEFAULT 0,
			files_updated INTEGER NOT NULL DEFAULT 0,
			entities_found INTEGER NOT NULL DEFAULT 0,
			edges_created INTEGER NOT NULL DEFAULT 0,
			errors_json TEXT,
			created_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create index_events table: %w", err)
	}
	return nil
}

// =============================================================================
// AE.6.3 Session Summaries Table
// =============================================================================

// createSessionSummariesTable creates the session_summaries table for storing
// aggregated session information and key decisions.
func (m *ArchivalistEventsMigration) createSessionSummariesTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS session_summaries (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			project_id TEXT,
			started_at TEXT NOT NULL,
			ended_at TEXT,
			summary TEXT,
			key_decisions_json TEXT,
			total_events INTEGER NOT NULL DEFAULT 0,
			success_count INTEGER NOT NULL DEFAULT 0,
			failure_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create session_summaries table: %w", err)
	}
	return nil
}

// =============================================================================
// Indexes
// =============================================================================

// createIndexes creates all indexes for the new tables.
func (m *ArchivalistEventsMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		// Activity events indexes
		`CREATE INDEX IF NOT EXISTS idx_activity_session_time
		 ON activity_events(session_id, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_type_time
		 ON activity_events(event_type, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_agent_time
		 ON activity_events(agent_id, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_category
		 ON activity_events(category)`,

		// Index events indexes
		`CREATE INDEX IF NOT EXISTS idx_index_session_time
		 ON index_events(session_id, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_index_version
		 ON index_events(index_version DESC)`,

		// Session summaries indexes
		`CREATE INDEX IF NOT EXISTS idx_session_project_ended
		 ON session_summaries(project_id, ended_at DESC)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// =============================================================================
// Migration Recording
// =============================================================================

// recordMigration inserts a record into schema_version.
func (m *ArchivalistEventsMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		ArchivalistEventsMigrationVersion,
		ArchivalistEventsMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// =============================================================================
// Rollback
// =============================================================================

// Rollback removes the archivalist events tables and indexes.
func (m *ArchivalistEventsMigration) Rollback() error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := m.executeRollbackSteps(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// executeRollbackSteps runs all rollback steps in sequence.
func (m *ArchivalistEventsMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropTables(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes all indexes created by this migration.
func (m *ArchivalistEventsMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_activity_session_time",
		"idx_activity_type_time",
		"idx_activity_agent_time",
		"idx_activity_category",
		"idx_index_session_time",
		"idx_index_version",
		"idx_session_project_ended",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

// dropTables removes all tables created by this migration.
func (m *ArchivalistEventsMigration) dropTables(tx *sql.Tx) error {
	tables := []string{
		"DROP TABLE IF EXISTS session_summaries",
		"DROP TABLE IF EXISTS index_events",
		"DROP TABLE IF EXISTS activity_events",
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("drop table: %w", err)
		}
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *ArchivalistEventsMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		ArchivalistEventsMigrationVersion,
	)
	return err
}
