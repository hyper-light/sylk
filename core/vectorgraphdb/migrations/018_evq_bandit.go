package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	EVQBanditMigrationVersion = 18
	EVQBanditMigrationName    = "evq_bandit"
)

type EVQBanditMigration struct {
	db *sql.DB
}

func NewEVQBanditMigration(db *sql.DB) *EVQBanditMigration {
	return &EVQBanditMigration{db: db}
}

func (m *EVQBanditMigration) Apply() error {
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

func (m *EVQBanditMigration) ensureSchemaVersionTable() error {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (m *EVQBanditMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		EVQBanditMigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (m *EVQBanditMigration) applyMigration() error {
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

func (m *EVQBanditMigration) executeMigrationSteps(tx *sql.Tx) error {
	if err := m.createBanditPosteriorsTable(tx); err != nil {
		return err
	}
	if err := m.createIndexes(tx); err != nil {
		return err
	}
	return m.recordMigration(tx)
}

func (m *EVQBanditMigration) createBanditPosteriorsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS evq_bandit_posteriors (
			codebase_id TEXT NOT NULL,
			arm_id TEXT NOT NULL,
			choice_idx INTEGER NOT NULL,
			alpha REAL NOT NULL DEFAULT 1.0,
			beta REAL NOT NULL DEFAULT 1.0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (codebase_id, arm_id, choice_idx)
		)
	`)
	if err != nil {
		return fmt.Errorf("create evq_bandit_posteriors table: %w", err)
	}
	return nil
}

func (m *EVQBanditMigration) createIndexes(tx *sql.Tx) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_bandit_codebase ON evq_bandit_posteriors(codebase_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bandit_updated ON evq_bandit_posteriors(updated_at DESC)`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

func (m *EVQBanditMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		EVQBanditMigrationVersion,
		EVQBanditMigrationName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

func (m *EVQBanditMigration) Rollback() error {
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

func (m *EVQBanditMigration) executeRollbackSteps(tx *sql.Tx) error {
	if err := m.dropIndexes(tx); err != nil {
		return err
	}
	if err := m.dropTables(tx); err != nil {
		return err
	}
	return m.removeMigrationRecord(tx)
}

func (m *EVQBanditMigration) dropIndexes(tx *sql.Tx) error {
	indexes := []string{
		"idx_bandit_codebase",
		"idx_bandit_updated",
	}

	for _, idx := range indexes {
		query := fmt.Sprintf("DROP INDEX IF EXISTS %s", idx)
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	return nil
}

func (m *EVQBanditMigration) dropTables(tx *sql.Tx) error {
	_, err := tx.Exec("DROP TABLE IF EXISTS evq_bandit_posteriors")
	if err != nil {
		return fmt.Errorf("drop evq_bandit_posteriors table: %w", err)
	}
	return nil
}

func (m *EVQBanditMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		EVQBanditMigrationVersion,
	)
	return err
}
