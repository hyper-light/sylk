// Package migrations provides database schema migrations for VectorGraphDB.
// Migrations are idempotent and track their history in a schema_version table.
package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// MigrationVersion is the version number for this migration.
	MigrationVersion = 3
	// MigrationName is the human-readable name for this migration.
	MigrationName = "add_version_column"
)

var (
	// ErrMigrationFailed indicates the migration could not be applied.
	ErrMigrationFailed = errors.New("migration failed")
	// ErrAlreadyApplied indicates the migration was already applied.
	ErrAlreadyApplied = errors.New("migration already applied")
)

// MigrationRecord represents a row in the schema_version table.
type MigrationRecord struct {
	Version   int
	Name      string
	AppliedAt time.Time
}

// AddVersionColumnMigration adds a version column to the nodes table
// for optimistic concurrency control.
type AddVersionColumnMigration struct {
	db *sql.DB
}

// NewAddVersionColumnMigration creates a new migration instance.
func NewAddVersionColumnMigration(db *sql.DB) *AddVersionColumnMigration {
	return &AddVersionColumnMigration{db: db}
}

// Apply executes the migration within a transaction.
func (m *AddVersionColumnMigration) Apply() error {
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
func (m *AddVersionColumnMigration) ensureSchemaVersionTable() error {
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
func (m *AddVersionColumnMigration) isApplied() (bool, error) {
	var count int
	err := m.db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		MigrationVersion,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyMigration runs the migration in a transaction.
func (m *AddVersionColumnMigration) applyMigration() error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := m.addVersionColumn(tx); err != nil {
		return err
	}
	if err := m.recordMigration(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// addVersionColumn adds the version column if it doesn't exist.
func (m *AddVersionColumnMigration) addVersionColumn(tx *sql.Tx) error {
	if exists, err := m.columnExists(tx); err != nil {
		return fmt.Errorf("check column exists: %w", err)
	} else if exists {
		return nil // Column already exists, nothing to do
	}

	_, err := tx.Exec(`
		ALTER TABLE nodes ADD COLUMN version INTEGER NOT NULL DEFAULT 1
	`)
	if err != nil {
		return fmt.Errorf("add version column: %w", err)
	}
	return nil
}

// columnExists checks if the version column already exists in the nodes table.
func (m *AddVersionColumnMigration) columnExists(tx *sql.Tx) (bool, error) {
	rows, err := tx.Query("PRAGMA table_info(nodes)")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	return scanForColumn(rows, "version")
}

// scanForColumn scans PRAGMA table_info rows looking for a specific column name.
func scanForColumn(rows *sql.Rows, targetColumn string) (bool, error) {
	for rows.Next() {
		name, err := scanColumnName(rows)
		if err != nil {
			return false, err
		}
		if name == targetColumn {
			return true, nil
		}
	}
	return false, rows.Err()
}

// scanColumnName extracts the column name from a PRAGMA table_info row.
func scanColumnName(rows *sql.Rows) (string, error) {
	var cid int
	var name, colType string
	var notNull, pk int
	var dfltValue sql.NullString
	err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk)
	return name, err
}

// recordMigration inserts a record into schema_version.
func (m *AddVersionColumnMigration) recordMigration(tx *sql.Tx) error {
	_, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		MigrationVersion, MigrationName, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

// Rollback removes the version column (for testing purposes).
// Note: SQLite does not support DROP COLUMN in older versions.
// This recreates the table without the version column.
func (m *AddVersionColumnMigration) Rollback() error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := m.removeVersionColumn(tx); err != nil {
		return err
	}
	if err := m.removeMigrationRecord(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// removeVersionColumn removes the version column by recreating the table.
func (m *AddVersionColumnMigration) removeVersionColumn(tx *sql.Tx) error {
	exists, err := m.columnExists(tx)
	if err != nil {
		return fmt.Errorf("check column exists: %w", err)
	}
	if !exists {
		return nil // Column doesn't exist, nothing to do
	}

	return m.recreateTableWithoutVersion(tx)
}

// recreateTableWithoutVersion rebuilds the nodes table without the version column.
func (m *AddVersionColumnMigration) recreateTableWithoutVersion(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE nodes_backup AS SELECT
			id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by
		FROM nodes`,
		`DROP TABLE nodes`,
		`CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL,
			path TEXT,
			package TEXT,
			line_start INTEGER,
			line_end INTEGER,
			signature TEXT,
			session_id TEXT,
			timestamp DATETIME,
			category TEXT,
			url TEXT,
			source TEXT,
			authors JSON,
			published_at DATETIME,
			content TEXT,
			content_hash TEXT,
			metadata JSON,
			verified BOOLEAN DEFAULT FALSE,
			verification_type INTEGER,
			confidence REAL DEFAULT 1.0,
			trust_level INTEGER DEFAULT 50,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME,
			superseded_by TEXT,
			CHECK (domain IN (0, 1, 2)),
			FOREIGN KEY (superseded_by) REFERENCES nodes(id) ON DELETE SET NULL
		)`,
		`INSERT INTO nodes SELECT * FROM nodes_backup`,
		`DROP TABLE nodes_backup`,
	}

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("execute statement: %w", err)
		}
	}
	return nil
}

// removeMigrationRecord deletes this migration from schema_version.
func (m *AddVersionColumnMigration) removeMigrationRecord(tx *sql.Tx) error {
	_, err := tx.Exec(
		"DELETE FROM schema_version WHERE version = ?",
		MigrationVersion,
	)
	return err
}

// GetAppliedMigrations returns all applied migrations.
func GetAppliedMigrations(db *sql.DB) ([]MigrationRecord, error) {
	rows, err := db.Query(
		"SELECT version, name, applied_at FROM schema_version ORDER BY version",
	)
	if err != nil {
		return nil, fmt.Errorf("query migrations: %w", err)
	}
	defer rows.Close()

	return scanMigrationRecords(rows)
}

// scanMigrationRecords scans rows into MigrationRecord slice.
func scanMigrationRecords(rows *sql.Rows) ([]MigrationRecord, error) {
	var records []MigrationRecord
	for rows.Next() {
		var r MigrationRecord
		var appliedAt string
		if err := rows.Scan(&r.Version, &r.Name, &appliedAt); err != nil {
			return nil, fmt.Errorf("scan migration: %w", err)
		}
		r.AppliedAt, _ = time.Parse(time.RFC3339, appliedAt)
		records = append(records, r)
	}
	return records, rows.Err()
}
