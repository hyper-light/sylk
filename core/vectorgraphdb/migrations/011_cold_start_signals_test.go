package migrations

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestColdStartSignalsMigration_Apply(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	migration := NewColdStartSignalsMigration(db)

	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Verify tables were created
	tables := []string{"node_cold_signals", "codebase_profile"}
	for _, table := range tables {
		if !tableExists(t, db, table) {
			t.Errorf("Table %s should exist after migration", table)
		}
	}

	// Verify indexes were created
	indexes := []string{
		"idx_cold_signals_prior",
		"idx_cold_signals_entity",
		"idx_cold_signals_degree",
	}
	for _, idx := range indexes {
		if !indexExists(t, db, idx) {
			t.Errorf("Index %s should exist after migration", idx)
		}
	}

	// Verify migration record was created
	if !migrationRecorded(t, db, ColdStartSignalsMigrationVersion) {
		t.Error("Migration should be recorded in schema_version")
	}
}

func TestColdStartSignalsMigration_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	migration := NewColdStartSignalsMigration(db)

	// Apply twice
	if err := migration.Apply(); err != nil {
		t.Fatalf("First Apply() failed: %v", err)
	}

	if err := migration.Apply(); err != nil {
		t.Fatalf("Second Apply() failed: %v", err)
	}

	// Verify only one record exists
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		ColdStartSignalsMigrationVersion,
	).Scan(&count)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 migration record, got %d", count)
	}
}

func TestColdStartSignalsMigration_TableSchemas(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	migration := NewColdStartSignalsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Test node_cold_signals table schema
	t.Run("node_cold_signals schema", func(t *testing.T) {
		expectedColumns := []string{
			"node_id", "entity_type", "in_degree", "out_degree",
			"page_rank", "cluster_coeff", "betweenness",
			"name_salience", "doc_coverage", "complexity",
			"type_frequency", "type_rarity", "created_at",
		}

		for _, col := range expectedColumns {
			if !columnExists(t, db, "node_cold_signals", col) {
				t.Errorf("Column %s should exist in node_cold_signals", col)
			}
		}
	})

	// Test codebase_profile table schema
	t.Run("codebase_profile schema", func(t *testing.T) {
		expectedColumns := []string{
			"id", "total_nodes", "avg_in_degree", "avg_out_degree",
			"max_page_rank", "page_rank_sum", "betweenness_max",
			"entity_counts_json", "domain_counts_json", "created_at",
		}

		for _, col := range expectedColumns {
			if !columnExists(t, db, "codebase_profile", col) {
				t.Errorf("Column %s should exist in codebase_profile", col)
			}
		}
	})
}

func TestColdStartSignalsMigration_InsertData(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create nodes table first (required for FK)
	_, err := db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create nodes table: %v", err)
	}

	migration := NewColdStartSignalsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Insert a test node
	_, err = db.Exec(`INSERT INTO nodes (id, domain) VALUES (?, ?)`, "test_node", 0)
	if err != nil {
		t.Fatalf("Failed to insert test node: %v", err)
	}

	// Insert cold-start signals
	_, err = db.Exec(`
		INSERT INTO node_cold_signals (
			node_id, entity_type, in_degree, out_degree,
			page_rank, cluster_coeff, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"test_node", "function", 5, 3, 0.15, 0.8, "2025-01-19T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("Failed to insert cold-start signals: %v", err)
	}

	// Verify data was inserted
	var nodeID string
	var pageRank float64
	err = db.QueryRow(
		"SELECT node_id, page_rank FROM node_cold_signals WHERE node_id = ?",
		"test_node",
	).Scan(&nodeID, &pageRank)

	if err != nil {
		t.Fatalf("Failed to query signals: %v", err)
	}

	if nodeID != "test_node" {
		t.Errorf("node_id = %s, want test_node", nodeID)
	}

	if pageRank != 0.15 {
		t.Errorf("page_rank = %f, want 0.15", pageRank)
	}
}

func TestColdStartSignalsMigration_ForeignKey(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Enable foreign keys
	_, err := db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}

	// Create nodes table
	_, err = db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create nodes table: %v", err)
	}

	migration := NewColdStartSignalsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Try to insert signal without corresponding node (should fail)
	_, err = db.Exec(`
		INSERT INTO node_cold_signals (
			node_id, entity_type, in_degree, out_degree, created_at
		) VALUES (?, ?, ?, ?, ?)`,
		"nonexistent_node", "function", 0, 0, "2025-01-19T00:00:00Z",
	)

	if err == nil {
		t.Error("Expected foreign key constraint violation, got nil")
	}
}

func TestColdStartSignalsMigration_Rollback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	migration := NewColdStartSignalsMigration(db)

	// Apply migration
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Rollback migration
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() failed: %v", err)
	}

	// Verify tables were dropped
	tables := []string{"node_cold_signals", "codebase_profile"}
	for _, table := range tables {
		if tableExists(t, db, table) {
			t.Errorf("Table %s should not exist after rollback", table)
		}
	}

	// Verify migration record was removed
	if migrationRecorded(t, db, ColdStartSignalsMigrationVersion) {
		t.Error("Migration record should be removed after rollback")
	}
}

// Helper functions

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	return db
}

func tableExists(t *testing.T, db *sql.DB, tableName string) bool {
	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
		tableName,
	).Scan(&name)
	return err == nil
}

func indexExists(t *testing.T, db *sql.DB, indexName string) bool {
	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name=?",
		indexName,
	).Scan(&name)
	return err == nil
}

func columnExists(t *testing.T, db *sql.DB, tableName, columnName string) bool {
	rows, err := db.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var ctype string
		var notNull int
		var dfltValue sql.NullString
		var pk int

		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return false
		}

		if name == columnName {
			return true
		}
	}
	return false
}

func migrationRecorded(t *testing.T, db *sql.DB, version int) bool {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM schema_version WHERE version = ?",
		version,
	).Scan(&count)
	return err == nil && count > 0
}
