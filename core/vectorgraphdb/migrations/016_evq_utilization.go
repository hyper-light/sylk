// Package migrations provides database schema migrations for VectorGraphDB.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// EVQUtilizationMigrationVersion is the version number for this migration.
	EVQUtilizationMigrationVersion = 16
	// EVQUtilizationMigrationName is the human-readable name for this migration.
	EVQUtilizationMigrationName = "evq_utilization"
)

// EVQUtilizationMigration adds the centroid_utilization table for tracking
// centroid usage patterns in the Remove-Birth adaptation system.
type EVQUtilizationMigration struct {
	db *sql.DB
}

// NewEVQUtilizationMigration creates a new migration instance.
func NewEVQUtilizationMigration(db *sql.DB) *EVQUtilizationMigration {
	return &EVQUtilizationMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *EVQUtilizationMigration) Apply() error {
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
func (m *EVQUtilizationMigration) ensureSchemaVersionTable() error {
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
func (m *EVQUtilizationMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		EVQUtilizationMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *EVQUtilizationMigration) applyMigration() error {
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
func (m *EVQUtilizationMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createUtilizationTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// createUtilizationTable creates the centroid_utilization table for tracking
// centroid usage with composite primary key.
func (m *EVQUtilizationMigration) createUtilizationTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS centroid_utilization (
			codebook_type TEXT NOT NULL,
			subspace_id INTEGER NOT NULL,
			centroid_id INTEGER NOT NULL,
			utilization REAL NOT NULL DEFAULT 0.0,
			last_updated TEXT NOT NULL,
			PRIMARY KEY (codebook_type, subspace_id, centroid_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("create centroid_utilization table: %w", err)
	}
	return nil
}

// createIndexes creates all indexes for the centroid_utilization table.
func (m *EVQUtilizationMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_utilization_low
		 ON centroid_utilization(utilization ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_utilization_type_subspace
		 ON centroid_utilization(codebook_type, subspace_id)`,
		`CREATE INDEX IF NOT EXISTS idx_utilization_updated
		 ON centroid_utilization(last_updated DESC)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *EVQUtilizationMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		EVQUtilizationMigrationVersion,
		EVQUtilizationMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the centroid_utilization table and indexes.
func (m *EVQUtilizationMigration) Rollback() error {
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
func (m *EVQUtilizationMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropTables(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes all indexes created by this migration.
func (m *EVQUtilizationMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_utilization_low",
		"idx_utilization_type_subspace",
		"idx_utilization_updated",
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
func (m *EVQUtilizationMigration) dropTables(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS centroid_utilization")
	if err != nil {
		return fmt.Errorf("drop centroid_utilization table: %w", err)
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *EVQUtilizationMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		EVQUtilizationMigrationVersion,
	)
	return err
}
