// Package migrations provides database schema migrations for VectorGraphDB.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// EVQLOPQMigrationVersion is the version number for this migration.
	EVQLOPQMigrationVersion = 15
	// EVQLOPQMigrationName is the human-readable name for this migration.
	EVQLOPQMigrationName = "evq_lopq"
)

// EVQLOPQMigration adds tables for storing LOPQ partitions and codebooks.
type EVQLOPQMigration struct {
	db *sql.DB
}

// NewEVQLOPQMigration creates a new migration instance.
func NewEVQLOPQMigration(db *sql.DB) *EVQLOPQMigration {
	return &EVQLOPQMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *EVQLOPQMigration) Apply() error {
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
func (m *EVQLOPQMigration) ensureSchemaVersionTable() error {
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
func (m *EVQLOPQMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		EVQLOPQMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *EVQLOPQMigration) applyMigration() error {
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
func (m *EVQLOPQMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createPartitionsTable(tx); err != nil {
		return err
	}
	if err := m.createCodebooksTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// createPartitionsTable creates the lopq_partitions table for storing
// vector partition assignments and local codes.
func (m *EVQLOPQMigration) createPartitionsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS lopq_partitions (
			vector_id TEXT PRIMARY KEY,
			partition_id INTEGER NOT NULL,
			local_code BLOB NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create lopq_partitions table: %w", err)
	}
	return nil
}

// createCodebooksTable creates the lopq_codebooks table for storing
// trained local codebooks per partition and subspace.
func (m *EVQLOPQMigration) createCodebooksTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS lopq_codebooks (
			partition_id INTEGER NOT NULL,
			subspace_id INTEGER NOT NULL,
			centroids BLOB NOT NULL,
			trained_at TEXT NOT NULL,
			vector_count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (partition_id, subspace_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("create lopq_codebooks table: %w", err)
	}
	return nil
}

// createIndexes creates all indexes for the LOPQ tables.
func (m *EVQLOPQMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_lopq_partition
		 ON lopq_partitions(partition_id)`,
		`CREATE INDEX IF NOT EXISTS idx_lopq_codebook_trained
		 ON lopq_codebooks(trained_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_lopq_codebook_count
		 ON lopq_codebooks(vector_count DESC)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *EVQLOPQMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		EVQLOPQMigrationVersion,
		EVQLOPQMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the LOPQ tables and indexes.
func (m *EVQLOPQMigration) Rollback() error {
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
func (m *EVQLOPQMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropTables(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes all indexes created by this migration.
func (m *EVQLOPQMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_lopq_partition",
		"idx_lopq_codebook_trained",
		"idx_lopq_codebook_count",
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
func (m *EVQLOPQMigration) dropTables(tx *sql.Tx) error {
	tables := []string{
		"DROP TABLE IF EXISTS lopq_codebooks",
		"DROP TABLE IF EXISTS lopq_partitions",
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("drop table: %w", err)
		}
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *EVQLOPQMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		EVQLOPQMigrationVersion,
	)
	return err
}
