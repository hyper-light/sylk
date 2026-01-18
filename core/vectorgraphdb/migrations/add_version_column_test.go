package migrations

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// createTestDB creates a temporary SQLite database with the base schema.
func createTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "migration_test_*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("sqlite3", tmpFile.Name()+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("open database: %v", err)
	}

	if err := createBaseSchema(db); err != nil {
		db.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("create base schema: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

// createBaseSchema creates the nodes table without the version column.
func createBaseSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE nodes (
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
		)
	`)
	return err
}

func TestAddVersionColumnMigration_Apply_HappyPath(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	migration := NewAddVersionColumnMigration(db)

	// Apply migration
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify version column exists
	if !hasVersionColumn(t, db) {
		t.Error("version column not found after migration")
	}

	// Verify migration was recorded
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 migration record, got %d", len(records))
	}
	if records[0].Version != MigrationVersion {
		t.Errorf("expected version %d, got %d", MigrationVersion, records[0].Version)
	}
	if records[0].Name != MigrationName {
		t.Errorf("expected name %q, got %q", MigrationName, records[0].Name)
	}
}

func TestAddVersionColumnMigration_Apply_Idempotent(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	migration := NewAddVersionColumnMigration(db)

	// Apply migration twice
	if err := migration.Apply(); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if err := migration.Apply(); err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}

	// Verify only one migration record exists
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 migration record after two applies, got %d", len(records))
	}

	// Verify column exists (only once)
	if !hasVersionColumn(t, db) {
		t.Error("version column not found after second migration")
	}
}

func TestAddVersionColumnMigration_Rollback(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	migration := NewAddVersionColumnMigration(db)

	// Apply then rollback
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	// Verify version column no longer exists
	if hasVersionColumn(t, db) {
		t.Error("version column still exists after rollback")
	}

	// Verify migration record was removed
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 migration records after rollback, got %d", len(records))
	}
}

func TestAddVersionColumnMigration_VersionColumnDefault(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert a node before migration
	_, err := db.Exec(`
		INSERT INTO nodes (id, domain, node_type, name)
		VALUES ('test-node', 0, 0, 'TestNode')
	`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	migration := NewAddVersionColumnMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify existing node has version = 1
	var version int
	err = db.QueryRow("SELECT version FROM nodes WHERE id = 'test-node'").Scan(&version)
	if err != nil {
		t.Fatalf("query version: %v", err)
	}
	if version != 1 {
		t.Errorf("expected version 1, got %d", version)
	}
}

func TestAddVersionColumnMigration_NewNodeDefaultVersion(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	migration := NewAddVersionColumnMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert a new node after migration (without specifying version)
	_, err := db.Exec(`
		INSERT INTO nodes (id, domain, node_type, name)
		VALUES ('new-node', 0, 0, 'NewNode')
	`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// Verify new node has version = 1 (default)
	var version int
	err = db.QueryRow("SELECT version FROM nodes WHERE id = 'new-node'").Scan(&version)
	if err != nil {
		t.Fatalf("query version: %v", err)
	}
	if version != 1 {
		t.Errorf("expected version 1, got %d", version)
	}
}

func TestAddVersionColumnMigration_PreExistingColumn(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Manually add version column before migration
	_, err := db.Exec("ALTER TABLE nodes ADD COLUMN version INTEGER NOT NULL DEFAULT 1")
	if err != nil {
		t.Fatalf("manually add column: %v", err)
	}

	migration := NewAddVersionColumnMigration(db)

	// Migration should succeed without error (idempotent)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() with pre-existing column error = %v", err)
	}

	// Verify only one migration record
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 migration record, got %d", len(records))
	}
}

func TestAddVersionColumnMigration_Rollback_NoColumnExists(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Ensure schema_version table exists
	migration := NewAddVersionColumnMigration(db)
	if err := migration.ensureSchemaVersionTable(); err != nil {
		t.Fatalf("ensureSchemaVersionTable() error = %v", err)
	}

	// Rollback without applying should succeed (no-op)
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() without apply error = %v", err)
	}
}

func TestAddVersionColumnMigration_ConcurrentApply(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	migration := NewAddVersionColumnMigration(db)

	// Run multiple applies concurrently
	done := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			done <- migration.Apply()
		}()
	}

	// Collect results - some may fail due to SQLite locking, but that's OK
	successCount := 0
	for i := 0; i < 5; i++ {
		if err := <-done; err == nil {
			successCount++
		}
	}

	// At least one should succeed
	if successCount == 0 {
		t.Error("expected at least one concurrent Apply() to succeed")
	}

	// Verify only one migration record exists (idempotent result)
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 migration record after concurrent applies, got %d", len(records))
	}

	// Verify the version column exists
	if !hasVersionColumn(t, db) {
		t.Error("version column should exist after concurrent applies")
	}
}

func TestAddVersionColumnMigration_VersionColumnNotNull(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	migration := NewAddVersionColumnMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Attempt to insert node with NULL version should fail
	// Note: SQLite does enforce NOT NULL on columns with DEFAULT
	// But we can verify the constraint exists via PRAGMA
	rows, err := db.Query("PRAGMA table_info(nodes)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "version" {
			found = true
			if notNull != 1 {
				t.Errorf("version column should be NOT NULL, got notNull=%d", notNull)
			}
			if !dfltValue.Valid || dfltValue.String != "1" {
				t.Errorf("version column should have DEFAULT 1, got %v", dfltValue)
			}
		}
	}
	if !found {
		t.Error("version column not found")
	}
}

func TestGetAppliedMigrations_EmptyTable(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	migration := NewAddVersionColumnMigration(db)
	if err := migration.ensureSchemaVersionTable(); err != nil {
		t.Fatalf("ensureSchemaVersionTable() error = %v", err)
	}

	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestGetAppliedMigrations_NoTable(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// schema_version table doesn't exist, should return error
	_, err := GetAppliedMigrations(db)
	if err == nil {
		t.Error("expected error when schema_version table doesn't exist")
	}
}

// hasVersionColumn checks if the version column exists in the nodes table.
func hasVersionColumn(t *testing.T, db *sql.DB) bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(nodes)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "version" {
			return true
		}
	}
	return false
}
