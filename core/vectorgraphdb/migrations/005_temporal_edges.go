// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// TemporalEdgesMigrationVersion is the version number for this migration.
	TemporalEdgesMigrationVersion = 5
	// TemporalEdgesMigrationName is the human-readable name for this migration.
	TemporalEdgesMigrationName = "temporal_edges"
)

// TemporalEdgesMigration adds temporal columns to the edges table
// for bitemporal tracking (valid time and transaction time).
type TemporalEdgesMigration struct {
	db *sql.DB
}

// NewTemporalEdgesMigration creates a new migration instance.
func NewTemporalEdgesMigration(db *sql.DB) *TemporalEdgesMigration {
	return &TemporalEdgesMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *TemporalEdgesMigration) Apply() error {
	if err := m.ensureSchemaVersionTable(); err != nil {
		return fmt.Errorf("ensure schema_version table: %w", err)
	}

	applied, err := m.isApplied()
	if err != nil {
		return fmt.Errorf("check if applied: %w", err)
	}
	if applied {
		return nil // Idempotent: already applied
	}

	return m.applyMigration()
}

// ensureSchemaVersionTable creates the schema_version table if it doesn't exist.
func (m *TemporalEdgesMigration) ensureSchemaVersionTable() error {
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
func (m *TemporalEdgesMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		TemporalEdgesMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *TemporalEdgesMigration) applyMigration() error {
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
func (m *TemporalEdgesMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.addTemporalColumns(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// addTemporalColumns adds temporal columns if they don't exist.
func (m *TemporalEdgesMigration) addTemporalColumns(tx *sql.Tx) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"valid_from", "INTEGER"},
		{"valid_to", "INTEGER"},
		{"tx_start", "INTEGER"},
		{"tx_end", "INTEGER"},
	}

	for _, col := range columns {
		if err := m.addColumn(tx, col.name, col.definition); err != nil {
			return err
		}
	}
	return nil
}

// addColumn adds a single column if it doesn't exist.
func (m *TemporalEdgesMigration) addColumn(tx *sql.Tx, name, def string) error {
	exists, err := m.columnExists(tx, name)
	if err != nil {
		return fmt.Errorf("check column %s: %w", name, err)
	}
	if exists {
		return nil
	}

	query := fmt.Sprintf("ALTER TABLE edges ADD COLUMN %s %s", name, def)
	if _, err := tx.Exec(query); err != nil {
		return fmt.Errorf("add column %s: %w", name, err)
	}
	return nil
}

// columnExists checks if a column exists in the edges table.
func (m *TemporalEdgesMigration) columnExists(tx *sql.Tx, name string) (bool, error) {
	rows, err := tx.Query("PRAGMA table_info(edges)")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	return scanForColumn(rows, name)
}

// createIndexes creates temporal indexes if they don't exist.
func (m *TemporalEdgesMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_edges_valid_time
		 ON edges(source_id, valid_from, valid_to)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_tx_time
		 ON edges(source_id, tx_start, tx_end)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_current
		 ON edges(source_id) WHERE valid_to IS NULL`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *TemporalEdgesMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		TemporalEdgesMigrationVersion,
		TemporalEdgesMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the temporal columns and indexes.
func (m *TemporalEdgesMigration) Rollback() error {
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
func (m *TemporalEdgesMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.removeTemporalColumns(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes the temporal indexes.
func (m *TemporalEdgesMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_edges_valid_time",
		"idx_edges_tx_time",
		"idx_edges_current",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

// removeTemporalColumns removes temporal columns by recreating the table.
func (m *TemporalEdgesMigration) removeTemporalColumns(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE edges_backup AS SELECT
			id, source_id, target_id, edge_type, weight, metadata, created_at
		FROM edges`,
		`DROP TABLE edges`,
		`CREATE TABLE edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			edge_type INTEGER NOT NULL,
			weight REAL DEFAULT 1.0,
			metadata JSON,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE,
			UNIQUE (source_id, target_id, edge_type)
		)`,
		`INSERT INTO edges SELECT * FROM edges_backup`,
		`DROP TABLE edges_backup`,
	}

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("execute statement: %w", err)
		}
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *TemporalEdgesMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		TemporalEdgesMigrationVersion,
	)
	return err
}
