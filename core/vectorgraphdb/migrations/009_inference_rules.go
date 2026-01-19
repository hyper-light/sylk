// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// InferenceRulesMigrationVersion is the version number for this migration.
	InferenceRulesMigrationVersion = 9
	// InferenceRulesMigrationName is the human-readable name for this migration.
	InferenceRulesMigrationName = "inference_rules"
)

// InferenceRulesMigration creates inference rule and materialized edge tables.
type InferenceRulesMigration struct {
	db *sql.DB
}

// NewInferenceRulesMigration creates a new migration instance.
func NewInferenceRulesMigration(db *sql.DB) *InferenceRulesMigration {
	return &InferenceRulesMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *InferenceRulesMigration) Apply() error {
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
func (m *InferenceRulesMigration) ensureSchemaVersionTable() error {
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
func (m *InferenceRulesMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		InferenceRulesMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *InferenceRulesMigration) applyMigration() error {
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
func (m *InferenceRulesMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createInferenceRulesTable(tx); err != nil {
		return err
	}
	if err := m.createMaterializedEdgesTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

// createInferenceRulesTable creates the inference_rules table.
func (m *InferenceRulesMigration) createInferenceRulesTable(tx *sql.Tx) error {
	exists, err := m.tableExists(tx, "inference_rules")
	if err != nil {
		return fmt.Errorf("check table exists: %w", err)
	}
	if exists {
		return nil
	}

	_, err = tx.Exec(`
		CREATE TABLE inference_rules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			head_subject TEXT NOT NULL,
			head_predicate TEXT NOT NULL,
			head_object TEXT NOT NULL,
			body_json TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			CHECK (enabled IN (0, 1))
		)
	`)
	if err != nil {
		return fmt.Errorf("create inference_rules table: %w", err)
	}
	return nil
}

// createMaterializedEdgesTable creates the materialized_edges table.
func (m *InferenceRulesMigration) createMaterializedEdgesTable(tx *sql.Tx) error {
	exists, err := m.tableExists(tx, "materialized_edges")
	if err != nil {
		return fmt.Errorf("check table exists: %w", err)
	}
	if exists {
		return nil
	}

	return m.executeMaterializedEdgesCreate(tx)
}

// executeMaterializedEdgesCreate executes the CREATE TABLE statement.
func (m *InferenceRulesMigration) executeMaterializedEdgesCreate(
	tx *sql.Tx,
) error {
	_, err := tx.Exec(`
		CREATE TABLE materialized_edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			rule_id TEXT NOT NULL,
			edge_id INTEGER NOT NULL,
			derived_at TEXT NOT NULL,
			FOREIGN KEY (rule_id) REFERENCES inference_rules(id) ON DELETE CASCADE,
			FOREIGN KEY (edge_id) REFERENCES edges(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("create materialized_edges table: %w", err)
	}
	return nil
}

// tableExists checks if a table exists in the database.
func (m *InferenceRulesMigration) tableExists(
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

// createIndexes creates indexes for the inference tables.
func (m *InferenceRulesMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_inference_enabled
		 ON inference_rules(enabled, priority)`,
		`CREATE INDEX IF NOT EXISTS idx_materialized_rule
		 ON materialized_edges(rule_id)`,
		`CREATE INDEX IF NOT EXISTS idx_materialized_edge
		 ON materialized_edges(edge_id)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// recordMigration inserts a record into schema_version.
func (m *InferenceRulesMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		InferenceRulesMigrationVersion,
		InferenceRulesMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the inference tables and indexes.
func (m *InferenceRulesMigration) Rollback() error {
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
func (m *InferenceRulesMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropMaterializedEdgesTable(tx); err != nil {
		return err
	}
	if err := m.dropInferenceRulesTable(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

// dropIndexes removes the inference table indexes.
func (m *InferenceRulesMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_inference_enabled",
		"idx_materialized_rule",
		"idx_materialized_edge",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

// dropMaterializedEdgesTable removes the materialized_edges table.
func (m *InferenceRulesMigration) dropMaterializedEdgesTable(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS materialized_edges")
	if err != nil {
		return fmt.Errorf("drop materialized_edges table: %w", err)
	}
	return nil
}

// dropInferenceRulesTable removes the inference_rules table.
func (m *InferenceRulesMigration) dropInferenceRulesTable(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS inference_rules")
	if err != nil {
		return fmt.Errorf("drop inference_rules table: %w", err)
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *InferenceRulesMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		InferenceRulesMigrationVersion,
	)
	return err
}
