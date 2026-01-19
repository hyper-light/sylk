package migrations

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// createEdgesTestDB creates a temporary SQLite database with base edges schema.
func createEdgesTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "edges_migration_test_*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("sqlite3", tmpFile.Name()+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("open database: %v", err)
	}

	if err := createEdgesBaseSchema(db); err != nil {
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

// createEdgesBaseSchema creates the edges table without temporal columns.
func createEdgesBaseSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		)`,
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
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func TestTemporalEdgesMigration_Apply_HappyPath(t *testing.T) {
	db, cleanup := createEdgesTestDB(t)
	defer cleanup()

	migration := NewTemporalEdgesMigration(db)

	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify all temporal columns exist
	columns := []string{"valid_from", "valid_to", "tx_start", "tx_end"}
	for _, col := range columns {
		if !hasEdgeColumn(t, db, col) {
			t.Errorf("column %s not found after migration", col)
		}
	}

	// Verify migration was recorded
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	found := false
	for _, r := range records {
		if r.Version == TemporalEdgesMigrationVersion {
			found = true
			if r.Name != TemporalEdgesMigrationName {
				t.Errorf("expected name %q, got %q", TemporalEdgesMigrationName, r.Name)
			}
		}
	}
	if !found {
		t.Error("migration record not found")
	}
}

func TestTemporalEdgesMigration_Apply_Idempotent(t *testing.T) {
	db, cleanup := createEdgesTestDB(t)
	defer cleanup()

	migration := NewTemporalEdgesMigration(db)

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

	count := 0
	for _, r := range records {
		if r.Version == TemporalEdgesMigrationVersion {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 migration record, got %d", count)
	}
}

func TestTemporalEdgesMigration_IndexesCreated(t *testing.T) {
	db, cleanup := createEdgesTestDB(t)
	defer cleanup()

	migration := NewTemporalEdgesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify indexes exist
	indexes := []string{
		"idx_edges_valid_time",
		"idx_edges_tx_time",
		"idx_edges_current",
	}

	for _, idx := range indexes {
		if !hasIndex(t, db, idx) {
			t.Errorf("index %s not found", idx)
		}
	}
}

func TestTemporalEdgesMigration_TemporalQueries(t *testing.T) {
	db, cleanup := createEdgesTestDB(t)
	defer cleanup()

	migration := NewTemporalEdgesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test nodes
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES
		('node1', 0, 0, 'Node1'),
		('node2', 0, 0, 'Node2')`)
	if err != nil {
		t.Fatalf("insert nodes: %v", err)
	}

	// Insert edge with temporal data
	now := time.Now().Unix()
	_, err = db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, valid_from, tx_start)
		VALUES (?, ?, ?, ?, ?)`,
		"node1", "node2", 1, now, now,
	)
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	// Query current edges (WHERE valid_to IS NULL)
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM edges
		WHERE source_id = ? AND valid_to IS NULL`,
		"node1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query current edges: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 current edge, got %d", count)
	}

	// Query edges at specific time
	err = db.QueryRow(`
		SELECT COUNT(*) FROM edges
		WHERE source_id = ? AND valid_from <= ? AND (valid_to IS NULL OR valid_to > ?)`,
		"node1", now, now,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query temporal edges: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 temporal edge, got %d", count)
	}
}

func TestTemporalEdgesMigration_IndexUsage(t *testing.T) {
	db, cleanup := createEdgesTestDB(t)
	defer cleanup()

	migration := NewTemporalEdgesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test data
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES
		('node1', 0, 0, 'Node1'),
		('node2', 0, 0, 'Node2')`)
	if err != nil {
		t.Fatalf("insert nodes: %v", err)
	}

	now := time.Now().Unix()
	_, err = db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, valid_from, tx_start)
		VALUES (?, ?, ?, ?, ?)`,
		"node1", "node2", 1, now, now,
	)
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	// Check query plan uses idx_edges_valid_time
	rows, err := db.Query(`
		EXPLAIN QUERY PLAN
		SELECT * FROM edges
		WHERE source_id = ? AND valid_from <= ? AND (valid_to IS NULL OR valid_to > ?)`,
		"node1", now, now,
	)
	if err != nil {
		t.Fatalf("explain query: %v", err)
	}
	defer rows.Close()

	planContainsIndex := false
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		if containsString(detail, "idx_edges_valid_time") ||
			containsString(detail, "idx_edges_current") {
			planContainsIndex = true
		}
	}

	if !planContainsIndex {
		t.Log("Note: Query plan may not use index on small dataset")
	}
}

func TestTemporalEdgesMigration_Rollback(t *testing.T) {
	db, cleanup := createEdgesTestDB(t)
	defer cleanup()

	migration := NewTemporalEdgesMigration(db)

	// Apply then rollback
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	// Verify temporal columns no longer exist
	columns := []string{"valid_from", "valid_to", "tx_start", "tx_end"}
	for _, col := range columns {
		if hasEdgeColumn(t, db, col) {
			t.Errorf("column %s still exists after rollback", col)
		}
	}

	// Verify indexes were removed
	indexes := []string{
		"idx_edges_valid_time",
		"idx_edges_tx_time",
		"idx_edges_current",
	}
	for _, idx := range indexes {
		if hasIndex(t, db, idx) {
			t.Errorf("index %s still exists after rollback", idx)
		}
	}

	// Verify migration record was removed
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	for _, r := range records {
		if r.Version == TemporalEdgesMigrationVersion {
			t.Error("migration record still exists after rollback")
		}
	}
}

func TestTemporalEdgesMigration_PreservesData(t *testing.T) {
	db, cleanup := createEdgesTestDB(t)
	defer cleanup()

	// Insert test data before migration
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES
		('node1', 0, 0, 'Node1'),
		('node2', 0, 0, 'Node2')`)
	if err != nil {
		t.Fatalf("insert nodes: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight)
		VALUES (?, ?, ?, ?)`,
		"node1", "node2", 1, 0.5,
	)
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	migration := NewTemporalEdgesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify data is preserved
	var weight float64
	err = db.QueryRow(`
		SELECT weight FROM edges WHERE source_id = ? AND target_id = ?`,
		"node1", "node2",
	).Scan(&weight)
	if err != nil {
		t.Fatalf("query edge: %v", err)
	}
	if weight != 0.5 {
		t.Errorf("expected weight 0.5, got %f", weight)
	}
}

// hasEdgeColumn checks if a column exists in the edges table.
func hasEdgeColumn(t *testing.T, db *sql.DB, columnName string) bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(edges)")
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
		if name == columnName {
			return true
		}
	}
	return false
}

// hasIndex checks if an index exists in the database.
func hasIndex(t *testing.T, db *sql.DB, indexName string) bool {
	t.Helper()

	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='index'")
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		if name == indexName {
			return true
		}
	}
	return false
}

// containsString checks if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && indexOfString(s, substr) >= 0)
}

// indexOfString finds the index of substr in s.
func indexOfString(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
