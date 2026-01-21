// Package migrations provides database schema migrations for VectorGraphDB.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// EVQTrainingQueueMigrationVersion is the version number for this migration.
	EVQTrainingQueueMigrationVersion = 17
	// EVQTrainingQueueMigrationName is the human-readable name for this migration.
	EVQTrainingQueueMigrationName = "evq_training_queue"
)

// EVQTrainingQueueMigration adds the training_queue table for managing
// background codebook training jobs.
type EVQTrainingQueueMigration struct {
	db *sql.DB
}

// NewEVQTrainingQueueMigration creates a new migration instance.
func NewEVQTrainingQueueMigration(db *sql.DB) *EVQTrainingQueueMigration {
	return &EVQTrainingQueueMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *EVQTrainingQueueMigration) Apply() error {
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
func (m *EVQTrainingQueueMigration) ensureSchemaVersionTable() error {
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
func (m *EVQTrainingQueueMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		EVQTrainingQueueMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *EVQTrainingQueueMigration) applyMigration() error {
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
func (m *EVQTrainingQueueMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createTrainingQueueTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// createTrainingQueueTable creates the training_queue table for managing
// background training jobs with status tracking.
func (m *EVQTrainingQueueMigration) createTrainingQueueTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS training_queue (
			id TEXT PRIMARY KEY,
			partition_id INTEGER NOT NULL,
			job_type TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'running', 'complete', 'failed', 'cancelled')),
			created_at TEXT NOT NULL,
			started_at TEXT,
			completed_at TEXT,
			error TEXT,
			vector_count INTEGER NOT NULL DEFAULT 0,
			duration_ns INTEGER
		)
	`)
	if err != nil {
		return fmt.Errorf("create training_queue table: %w", err)
	}
	return nil
}

// createIndexes creates all indexes for the training_queue table.
func (m *EVQTrainingQueueMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_training_status_priority
		 ON training_queue(status, priority DESC, created_at ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_training_partition
		 ON training_queue(partition_id)`,
		`CREATE INDEX IF NOT EXISTS idx_training_created
		 ON training_queue(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_training_pending
		 ON training_queue(status) WHERE status = 'pending'`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *EVQTrainingQueueMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		EVQTrainingQueueMigrationVersion,
		EVQTrainingQueueMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the training_queue table and indexes.
func (m *EVQTrainingQueueMigration) Rollback() error {
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
func (m *EVQTrainingQueueMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropTables(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes all indexes created by this migration.
func (m *EVQTrainingQueueMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_training_status_priority",
		"idx_training_partition",
		"idx_training_created",
		"idx_training_pending",
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
func (m *EVQTrainingQueueMigration) dropTables(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS training_queue")
	if err != nil {
		return fmt.Errorf("drop training_queue table: %w", err)
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *EVQTrainingQueueMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		EVQTrainingQueueMigrationVersion,
	)
	return err
}
