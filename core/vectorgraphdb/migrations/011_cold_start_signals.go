// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// ColdStartSignalsMigrationVersion is the version number for this migration.
	ColdStartSignalsMigrationVersion = 11
	// ColdStartSignalsMigrationName is the human-readable name for this migration.
	ColdStartSignalsMigrationName = "cold_start_signals"
)

// ColdStartSignalsMigration adds cold-start signal tables for adaptive retrieval.
// MD.9.8: Schema Migration for Cold-Start Signals
type ColdStartSignalsMigration struct {
	db *sql.DB
}

// NewColdStartSignalsMigration creates a new migration instance.
func NewColdStartSignalsMigration(db *sql.DB) *ColdStartSignalsMigration {
	return &ColdStartSignalsMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *ColdStartSignalsMigration) Apply() error {
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

func (m *ColdStartSignalsMigration) ensureSchemaVersionTable() error {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (m *ColdStartSignalsMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		ColdStartSignalsMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (m *ColdStartSignalsMigration) applyMigration() error {
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

func (m *ColdStartSignalsMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createNodeColdSignalsTable(tx); err != nil {
		return err
	}
	if err := m.createCodebaseProfileTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

func (m *ColdStartSignalsMigration) createNodeColdSignalsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS node_cold_signals (
			node_id TEXT PRIMARY KEY,
			entity_type TEXT NOT NULL,
			in_degree INTEGER NOT NULL DEFAULT 0,
			out_degree INTEGER NOT NULL DEFAULT 0,
			page_rank REAL NOT NULL DEFAULT 0.0,
			cluster_coeff REAL NOT NULL DEFAULT 0.0,
			betweenness REAL NOT NULL DEFAULT 0.0,
			name_salience REAL NOT NULL DEFAULT 0.0,
			doc_coverage REAL NOT NULL DEFAULT 0.0,
			complexity REAL NOT NULL DEFAULT 0.0,
			type_frequency REAL NOT NULL DEFAULT 0.0,
			type_rarity REAL NOT NULL DEFAULT 0.0,
			created_at TEXT NOT NULL,
			FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("create node_cold_signals table: %w", err)
	}
	return nil
}

func (m *ColdStartSignalsMigration) createCodebaseProfileTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS codebase_profile (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			total_nodes INTEGER NOT NULL,
			avg_in_degree REAL NOT NULL,
			avg_out_degree REAL NOT NULL,
			max_page_rank REAL NOT NULL,
			page_rank_sum REAL NOT NULL,
			betweenness_max REAL NOT NULL,
			entity_counts_json TEXT NOT NULL,
			domain_counts_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create codebase_profile table: %w", err)
	}
	return nil
}

func (m *ColdStartSignalsMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_cold_signals_prior
		 ON node_cold_signals(page_rank DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_cold_signals_entity
		 ON node_cold_signals(entity_type)`,
		`CREATE INDEX IF NOT EXISTS idx_cold_signals_degree
		 ON node_cold_signals(in_degree DESC, out_degree DESC)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

func (m *ColdStartSignalsMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		ColdStartSignalsMigrationVersion,
		ColdStartSignalsMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the cold-start signal tables and indexes.
func (m *ColdStartSignalsMigration) Rollback() error {
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

func (m *ColdStartSignalsMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropTables(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

func (m *ColdStartSignalsMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_cold_signals_prior",
		"idx_cold_signals_entity",
		"idx_cold_signals_degree",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

func (m *ColdStartSignalsMigration) dropTables(tx *sql.Tx) error {
	tables := []string{
		"DROP TABLE IF EXISTS node_cold_signals",
		"DROP TABLE IF EXISTS codebase_profile",
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("drop table: %w", err)
		}
	}
	return nil
}

func (m *ColdStartSignalsMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		ColdStartSignalsMigrationVersion,
	)
	return err
}
