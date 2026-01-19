// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// AliasTableMigrationVersion is the version number for this migration.
	AliasTableMigrationVersion = 7
	// AliasTableMigrationName is the human-readable name for this migration.
	AliasTableMigrationName = "alias_table"
)

// Alias types enumeration.
const (
	AliasTypeCanonical = "canonical"
	AliasTypeImport    = "import_alias"
	AliasTypeType      = "type_alias"
	AliasTypeShorthand = "shorthand"
	AliasTypeQualified = "qualified"
)

// AliasTableMigration creates the entity_aliases table for entity alias resolution.
type AliasTableMigration struct {
	db *sql.DB
}

// NewAliasTableMigration creates a new migration instance.
func NewAliasTableMigration(db *sql.DB) *AliasTableMigration {
	return &AliasTableMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *AliasTableMigration) Apply() error {
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
func (m *AliasTableMigration) ensureSchemaVersionTable() error {
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
func (m *AliasTableMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		AliasTableMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *AliasTableMigration) applyMigration() error {
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
func (m *AliasTableMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createAliasesTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// createAliasesTable creates the entity_aliases table if it doesn't exist.
func (m *AliasTableMigration) createAliasesTable(tx *sql.Tx) error {
	exists, err := m.tableExists(tx, "entity_aliases")
	if err != nil {
		return fmt.Errorf("check table exists: %w", err)
	}
	if exists {
		return nil
	}

	_, err = tx.Exec(`
		CREATE TABLE entity_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			entity_id TEXT NOT NULL,
			alias TEXT NOT NULL,
			alias_type TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 1.0,
			created_at TEXT NOT NULL,
			FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE,
			CHECK (confidence >= 0.0 AND confidence <= 1.0),
			CHECK (alias_type IN ('canonical', 'import_alias', 'type_alias', 'shorthand', 'qualified'))
		)
	`)
	if err != nil {
		return fmt.Errorf("create entity_aliases table: %w", err)
	}
	return nil
}

// tableExists checks if a table exists in the database.
func (m *AliasTableMigration) tableExists(tx *sql.Tx, name string) (bool, error) {
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

// createIndexes creates indexes for the entity_aliases table.
func (m *AliasTableMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_aliases_lookup
		 ON entity_aliases(alias, entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_aliases_entity
		 ON entity_aliases(entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_aliases_type
		 ON entity_aliases(alias_type, alias)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *AliasTableMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		AliasTableMigrationVersion,
		AliasTableMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the entity_aliases table and indexes.
func (m *AliasTableMigration) Rollback() error {
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
func (m *AliasTableMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropAliasesTable(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes the entity_aliases table indexes.
func (m *AliasTableMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_aliases_lookup",
		"idx_aliases_entity",
		"idx_aliases_type",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

// dropAliasesTable removes the entity_aliases table.
func (m *AliasTableMigration) dropAliasesTable(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS entity_aliases")
	if err != nil {
		return fmt.Errorf("drop entity_aliases table: %w", err)
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *AliasTableMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		AliasTableMigrationVersion,
	)
	return err
}
