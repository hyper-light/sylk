// Package migrations provides database schema migrations for VectorGraphDB.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// EVQRaBitQMigrationVersion is the version number for this migration.
	EVQRaBitQMigrationVersion = 14
	// EVQRaBitQMigrationName is the human-readable name for this migration.
	EVQRaBitQMigrationName = "evq_rabitq"
)

// EVQRaBitQMigration adds the rabitq_config table for storing
// RaBitQ encoder configurations including rotation matrices.
type EVQRaBitQMigration struct {
	db *sql.DB
}

// NewEVQRaBitQMigration creates a new migration instance.
func NewEVQRaBitQMigration(db *sql.DB) *EVQRaBitQMigration {
	return &EVQRaBitQMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *EVQRaBitQMigration) Apply() error {
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
func (m *EVQRaBitQMigration) ensureSchemaVersionTable() error {
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
func (m *EVQRaBitQMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		EVQRaBitQMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *EVQRaBitQMigration) applyMigration() error {
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
func (m *EVQRaBitQMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createRaBitQConfigTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// createRaBitQConfigTable creates the rabitq_config table for storing
// RaBitQ encoder configurations with rotation matrices as BLOBs.
func (m *EVQRaBitQMigration) createRaBitQConfigTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS rabitq_config (
			id TEXT PRIMARY KEY,
			dimension INTEGER NOT NULL,
			seed INTEGER NOT NULL,
			rotation_matrix BLOB NOT NULL,
			created_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create rabitq_config table: %w", err)
	}
	return nil
}

// createIndexes creates all indexes for the rabitq_config table.
func (m *EVQRaBitQMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_rabitq_dimension
		 ON rabitq_config(dimension)`,
		`CREATE INDEX IF NOT EXISTS idx_rabitq_created
		 ON rabitq_config(created_at DESC)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *EVQRaBitQMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		EVQRaBitQMigrationVersion,
		EVQRaBitQMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the rabitq_config table and indexes.
func (m *EVQRaBitQMigration) Rollback() error {
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
func (m *EVQRaBitQMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropTables(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes all indexes created by this migration.
func (m *EVQRaBitQMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_rabitq_dimension",
		"idx_rabitq_created",
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
func (m *EVQRaBitQMigration) dropTables(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS rabitq_config")
	if err != nil {
		return fmt.Errorf("drop rabitq_config table: %w", err)
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *EVQRaBitQMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		EVQRaBitQMigrationVersion,
	)
	return err
}
