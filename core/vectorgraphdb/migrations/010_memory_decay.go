// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// MemoryDecayMigrationVersion is the version number for this migration.
	MemoryDecayMigrationVersion = 10
	// MemoryDecayMigrationName is the human-readable name for this migration.
	MemoryDecayMigrationName = "memory_decay"
)

// MemoryDecayMigration adds memory decay columns and tables for adaptive retrieval.
type MemoryDecayMigration struct {
	db *sql.DB
}

// NewMemoryDecayMigration creates a new migration instance.
func NewMemoryDecayMigration(db *sql.DB) *MemoryDecayMigration {
	return &MemoryDecayMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *MemoryDecayMigration) Apply() error {
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
func (m *MemoryDecayMigration) ensureSchemaVersionTable() error {
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
func (m *MemoryDecayMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		MemoryDecayMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *MemoryDecayMigration) applyMigration() error {
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
func (m *MemoryDecayMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.addMemoryColumns(tx); err != nil {
		return err
	}
	if err := m.createTables(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// addMemoryColumns adds memory decay columns to nodes and edges tables.
func (m *MemoryDecayMigration) addMemoryColumns(tx *sql.Tx) error {
	if err := m.addColumnsToTable(tx, "nodes"); err != nil {
		return err
	}
	return m.addColumnsToTable(tx, "edges")
}

// addColumnsToTable adds memory decay columns to a specific table.
func (m *MemoryDecayMigration) addColumnsToTable(tx *sql.Tx, tableName string) error {
	columns := []string{
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN memory_activation REAL DEFAULT 0.0", tableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN last_accessed_at INTEGER", tableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN access_count INTEGER DEFAULT 0", tableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN base_offset REAL DEFAULT 0.0", tableName),
	}

	for _, col := range columns {
		if _, err := tx.Exec(col); err != nil {
			if !m.isColumnExistsError(err) {
				return fmt.Errorf("add %s column: %w", tableName, err)
			}
		}
	}
	return nil
}

// createTables creates all memory decay tables.
func (m *MemoryDecayMigration) createTables(tx *sql.Tx) error {
	if err := m.createNodeAccessTracesTable(tx); err != nil {
		return err
	}
	if err := m.createEdgeAccessTracesTable(tx); err != nil {
		return err
	}
	return m.createDecayParametersTable(tx)
}

// createNodeAccessTracesTable creates the node_access_traces table.
func (m *MemoryDecayMigration) createNodeAccessTracesTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS node_access_traces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			accessed_at INTEGER NOT NULL,
			access_type TEXT NOT NULL,
			context TEXT,
			FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("create node_access_traces table: %w", err)
	}
	return nil
}

// createEdgeAccessTracesTable creates the edge_access_traces table.
func (m *MemoryDecayMigration) createEdgeAccessTracesTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS edge_access_traces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			edge_id INTEGER NOT NULL,
			accessed_at INTEGER NOT NULL,
			access_type TEXT NOT NULL,
			context TEXT,
			FOREIGN KEY (edge_id) REFERENCES edges(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("create edge_access_traces table: %w", err)
	}
	return nil
}

// createDecayParametersTable creates the decay_parameters table.
func (m *MemoryDecayMigration) createDecayParametersTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS decay_parameters (
			domain INTEGER PRIMARY KEY,
			decay_exponent_alpha REAL NOT NULL,
			decay_exponent_beta REAL NOT NULL,
			base_offset_mean REAL NOT NULL,
			base_offset_variance REAL NOT NULL,
			effective_samples REAL NOT NULL,
			updated_at TEXT NOT NULL,
			CHECK (domain IN (0, 1, 2))
		)
	`)
	if err != nil {
		return fmt.Errorf("create decay_parameters table: %w", err)
	}
	return nil
}

// createIndexes creates indexes for the new tables.
func (m *MemoryDecayMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_access_traces_node
		 ON node_access_traces(node_id, accessed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_access_traces_edge
		 ON edge_access_traces(edge_id, accessed_at DESC)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *MemoryDecayMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		MemoryDecayMigrationVersion,
		MemoryDecayMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// isColumnExistsError checks if the error is due to duplicate column.
func (m *MemoryDecayMigration) isColumnExistsError(err error) bool {
	if err == nil {
		return false
	}
	// SQLite error for duplicate column name
	errMsg := err.Error()
	return len(errMsg) > 0 && (
		containsSubstring(errMsg, "duplicate column name") ||
		containsSubstring(errMsg, "already exists"))
}

// Rollback removes the memory decay tables, indexes, and columns.
func (m *MemoryDecayMigration) Rollback() error {
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
func (m *MemoryDecayMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropTables(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes the memory decay indexes.
func (m *MemoryDecayMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_access_traces_node",
		"idx_access_traces_edge",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

// dropTables removes the memory decay tables.
func (m *MemoryDecayMigration) dropTables(tx *sql.Tx) error {
	tables := []string{
		"DROP TABLE IF EXISTS edge_access_traces",
		"DROP TABLE IF EXISTS node_access_traces",
		"DROP TABLE IF EXISTS decay_parameters",
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("drop table: %w", err)
		}
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *MemoryDecayMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		MemoryDecayMigrationVersion,
	)
	return err
}

// containsSubstring checks if a string contains a substring.
func containsSubstring(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
