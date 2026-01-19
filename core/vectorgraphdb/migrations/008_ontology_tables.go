// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// OntologyTablesMigrationVersion is the version number for this migration.
	OntologyTablesMigrationVersion = 8
	// OntologyTablesMigrationName is the human-readable name for this migration.
	OntologyTablesMigrationName = "ontology_tables"
)

// Constraint types enumeration.
const (
	ConstraintTypeAllowedEdges      = "allowed_edges"
	ConstraintTypeRequiredProperties = "required_properties"
	ConstraintTypeCardinality       = "cardinality"
)

// OntologyTablesMigration creates ontology hierarchy and constraint tables.
type OntologyTablesMigration struct {
	db *sql.DB
}

// NewOntologyTablesMigration creates a new migration instance.
func NewOntologyTablesMigration(db *sql.DB) *OntologyTablesMigration {
	return &OntologyTablesMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *OntologyTablesMigration) Apply() error {
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
func (m *OntologyTablesMigration) ensureSchemaVersionTable() error {
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
func (m *OntologyTablesMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		OntologyTablesMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *OntologyTablesMigration) applyMigration() error {
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
func (m *OntologyTablesMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createOntologyClassesTable(tx); err != nil {
		return err
	}
	if err := m.createOntologyConstraintsTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// createOntologyClassesTable creates the ontology_classes table.
func (m *OntologyTablesMigration) createOntologyClassesTable(tx *sql.Tx) error {
	exists, err := m.tableExists(tx, "ontology_classes")
	if err != nil {
		return fmt.Errorf("check table exists: %w", err)
	}
	if exists {
		return nil
	}

	_, err = tx.Exec(`
		CREATE TABLE ontology_classes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			parent_id TEXT,
			description TEXT,
			created_at TEXT NOT NULL,
			FOREIGN KEY (parent_id) REFERENCES ontology_classes(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("create ontology_classes table: %w", err)
	}
	return nil
}

// createOntologyConstraintsTable creates the ontology_constraints table.
func (m *OntologyTablesMigration) createOntologyConstraintsTable(
	tx *sql.Tx,
) error {
	exists, err := m.tableExists(tx, "ontology_constraints")
	if err != nil {
		return fmt.Errorf("check table exists: %w", err)
	}
	if exists {
		return nil
	}

	_, err = tx.Exec(`
		CREATE TABLE ontology_constraints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			class_id TEXT NOT NULL,
			constraint_type TEXT NOT NULL,
			constraint_value TEXT NOT NULL,
			FOREIGN KEY (class_id) REFERENCES ontology_classes(id) ON DELETE CASCADE,
			CHECK (constraint_type IN ('allowed_edges', 'required_properties', 'cardinality'))
		)
	`)
	if err != nil {
		return fmt.Errorf("create ontology_constraints table: %w", err)
	}
	return nil
}

// tableExists checks if a table exists in the database.
func (m *OntologyTablesMigration) tableExists(
	tx *sql.Tx,
	name string,
) (bool, error) {
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

// createIndexes creates indexes for the ontology tables.
func (m *OntologyTablesMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_ontology_hierarchy
		 ON ontology_classes(parent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_constraints_class
		 ON ontology_constraints(class_id)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *OntologyTablesMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		OntologyTablesMigrationVersion,
		OntologyTablesMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the ontology tables and indexes.
func (m *OntologyTablesMigration) Rollback() error {
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
func (m *OntologyTablesMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropOntologyConstraintsTable(tx); err != nil {
		return err
	}
	if err := m.dropOntologyClassesTable(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes the ontology table indexes.
func (m *OntologyTablesMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_ontology_hierarchy",
		"idx_ontology_constraints_class",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

// dropOntologyConstraintsTable removes the ontology_constraints table.
func (m *OntologyTablesMigration) dropOntologyConstraintsTable(
	tx *sql.Tx,
) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS ontology_constraints")
	if err != nil {
		return fmt.Errorf("drop ontology_constraints table: %w", err)
	}
	return nil
}

// dropOntologyClassesTable removes the ontology_classes table.
func (m *OntologyTablesMigration) dropOntologyClassesTable(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS ontology_classes")
	if err != nil {
		return fmt.Errorf("drop ontology_classes table: %w", err)
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *OntologyTablesMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		OntologyTablesMigrationVersion,
	)
	return err
}
