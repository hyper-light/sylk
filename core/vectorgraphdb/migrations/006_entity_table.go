// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// EntityTableMigrationVersion is the version number for this migration.
	EntityTableMigrationVersion = 6
	// EntityTableMigrationName is the human-readable name for this migration.
	EntityTableMigrationName = "entity_table"
)

// Entity types enumeration.
const (
	EntityTypeFunction  = 0
	EntityTypeType      = 1
	EntityTypeVariable  = 2
	EntityTypeImport    = 3
	EntityTypeFile      = 4
	EntityTypeModule    = 5
	EntityTypePackage   = 6
	EntityTypeInterface = 7
	EntityTypeMethod    = 8
	EntityTypeConstant  = 9
)

// EntityTableMigration creates the entities table for canonical entity tracking.
type EntityTableMigration struct {
	db *sql.DB
}

// NewEntityTableMigration creates a new migration instance.
func NewEntityTableMigration(db *sql.DB) *EntityTableMigration {
	return &EntityTableMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *EntityTableMigration) Apply() error {
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
func (m *EntityTableMigration) ensureSchemaVersionTable() error {
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
func (m *EntityTableMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		EntityTableMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *EntityTableMigration) applyMigration() error {
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
func (m *EntityTableMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createEntitiesTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// createEntitiesTable creates the entities table if it doesn't exist.
func (m *EntityTableMigration) createEntitiesTable(tx *sql.Tx) error {
	exists, err := m.tableExists(tx, "entities")
	if err != nil {
		return fmt.Errorf("check table exists: %w", err)
	}
	if exists {
		return nil
	}

	_, err = tx.Exec(`
		CREATE TABLE entities (
			id TEXT PRIMARY KEY,
			canonical_name TEXT NOT NULL,
			entity_type INTEGER NOT NULL,
			source_node_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY (source_node_id) REFERENCES nodes(id) ON DELETE CASCADE,
			CHECK (entity_type >= 0 AND entity_type <= 9)
		)
	`)
	if err != nil {
		return fmt.Errorf("create entities table: %w", err)
	}
	return nil
}

// tableExists checks if a table exists in the database.
func (m *EntityTableMigration) tableExists(tx *sql.Tx, name string) (bool, error) {
	var count int
	err := tx.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
		name,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// createIndexes creates indexes for the entities table.
func (m *EntityTableMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_entities_canonical
		 ON entities(canonical_name, entity_type)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_source
		 ON entities(source_node_id)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *EntityTableMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		EntityTableMigrationVersion,
		EntityTableMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the entities table and indexes.
func (m *EntityTableMigration) Rollback() error {
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
func (m *EntityTableMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropEntitiesTable(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes the entities table indexes.
func (m *EntityTableMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_entities_canonical",
		"idx_entities_source",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

// dropEntitiesTable removes the entities table.
func (m *EntityTableMigration) dropEntitiesTable(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS entities")
	if err != nil {
		return fmt.Errorf("drop entities table: %w", err)
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *EntityTableMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		EntityTableMigrationVersion,
	)
	return err
}
