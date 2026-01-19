package migrations

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// Test Setup
// =============================================================================

func setupArchivalistTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	return db
}

// =============================================================================
// AE.6.1 Tests - Activity Events Table
// =============================================================================

func TestActivityEventsTableCreation(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	// Verify table exists
	var tableName string
	err := db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='activity_events'
	`).Scan(&tableName)

	if err != nil {
		t.Fatalf("activity_events table not found: %v", err)
	}

	if tableName != "activity_events" {
		t.Errorf("expected table name 'activity_events', got '%s'", tableName)
	}
}

func TestActivityEventsInsertAndQuery(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	// Insert test data
	_, err := db.Exec(`
		INSERT INTO activity_events
		(id, event_type, timestamp, session_id, content, outcome, importance, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "test-1", 2, "2024-01-01T10:00:00Z", "session-1", "test content", 1, 0.8, "2024-01-01T10:00:00Z")

	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	// Query and verify
	var content string
	var importance float64
	err = db.QueryRow(`
		SELECT content, importance FROM activity_events WHERE id = ?
	`, "test-1").Scan(&content, &importance)

	if err != nil {
		t.Fatalf("failed to query test data: %v", err)
	}

	if content != "test content" {
		t.Errorf("expected content 'test content', got '%s'", content)
	}

	if importance != 0.8 {
		t.Errorf("expected importance 0.8, got %v", importance)
	}
}

// =============================================================================
// AE.6.2 Tests - Index Events Table
// =============================================================================

func TestIndexEventsTableCreation(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	// Verify table exists
	var tableName string
	err := db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='index_events'
	`).Scan(&tableName)

	if err != nil {
		t.Fatalf("index_events table not found: %v", err)
	}

	if tableName != "index_events" {
		t.Errorf("expected table name 'index_events', got '%s'", tableName)
	}
}

func TestIndexEventsInsertAndQuery(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	// Insert test data
	_, err := db.Exec(`
		INSERT INTO index_events
		(id, event_type, timestamp, session_id, index_type, index_version,
		 prev_version, root_path, duration_ns, files_indexed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "idx-1", 11, "2024-01-01T10:00:00Z", "session-1", 0, 2, 1, "/test/path", 1000000, 10, "2024-01-01T10:00:00Z")

	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	// Query and verify
	var filesIndexed int
	var indexVersion int
	err = db.QueryRow(`
		SELECT files_indexed, index_version FROM index_events WHERE id = ?
	`, "idx-1").Scan(&filesIndexed, &indexVersion)

	if err != nil {
		t.Fatalf("failed to query test data: %v", err)
	}

	if filesIndexed != 10 {
		t.Errorf("expected files_indexed 10, got %v", filesIndexed)
	}

	if indexVersion != 2 {
		t.Errorf("expected index_version 2, got %v", indexVersion)
	}
}

// =============================================================================
// AE.6.3 Tests - Session Summaries Table
// =============================================================================

func TestSessionSummariesTableCreation(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	// Verify table exists
	var tableName string
	err := db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='session_summaries'
	`).Scan(&tableName)

	if err != nil {
		t.Fatalf("session_summaries table not found: %v", err)
	}

	if tableName != "session_summaries" {
		t.Errorf("expected table name 'session_summaries', got '%s'", tableName)
	}
}

func TestSessionSummariesInsertAndQuery(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	// Insert test data
	_, err := db.Exec(`
		INSERT INTO session_summaries
		(id, session_id, project_id, started_at, summary,
		 total_events, success_count, failure_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "sum-1", "session-1", "proj-1", "2024-01-01T10:00:00Z", "test summary", 100, 80, 5, "2024-01-01T10:00:00Z")

	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	// Query and verify
	var summary string
	var totalEvents int
	err = db.QueryRow(`
		SELECT summary, total_events FROM session_summaries WHERE id = ?
	`, "sum-1").Scan(&summary, &totalEvents)

	if err != nil {
		t.Fatalf("failed to query test data: %v", err)
	}

	if summary != "test summary" {
		t.Errorf("expected summary 'test summary', got '%s'", summary)
	}

	if totalEvents != 100 {
		t.Errorf("expected total_events 100, got %v", totalEvents)
	}
}

// =============================================================================
// Index Tests
// =============================================================================

func TestIndexesCreated(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	expectedIndexes := []string{
		"idx_activity_session_time",
		"idx_activity_type_time",
		"idx_activity_agent_time",
		"idx_activity_category",
		"idx_index_session_time",
		"idx_index_version",
		"idx_session_project_ended",
	}

	for _, indexName := range expectedIndexes {
		var name string
		err := db.QueryRow(`
			SELECT name FROM sqlite_master
			WHERE type='index' AND name=?
		`, indexName).Scan(&name)

		if err != nil {
			t.Errorf("index %s not found: %v", indexName, err)
		}
	}
}

// =============================================================================
// Migration Tests
// =============================================================================

func TestMigrationIdempotent(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)

	// Apply first time
	if err := migration.Apply(); err != nil {
		t.Fatalf("first migration apply failed: %v", err)
	}

	// Apply second time - should be idempotent
	if err := migration.Apply(); err != nil {
		t.Fatalf("second migration apply failed: %v", err)
	}

	// Verify schema_version has only one entry
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM schema_version WHERE version = ?
	`, ArchivalistEventsMigrationVersion).Scan(&count)

	if err != nil {
		t.Fatalf("failed to query schema_version: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 schema_version entry, got %d", count)
	}
}

func TestMigrationRollback(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)

	// Apply migration
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	// Rollback migration
	if err := migration.Rollback(); err != nil {
		t.Fatalf("migration rollback failed: %v", err)
	}

	// Verify tables are dropped
	tables := []string{"activity_events", "index_events", "session_summaries"}
	for _, table := range tables {
		var name string
		err := db.QueryRow(`
			SELECT name FROM sqlite_master
			WHERE type='table' AND name=?
		`, table).Scan(&name)

		if err == nil {
			t.Errorf("table %s should have been dropped", table)
		}
	}

	// Verify schema_version record is removed
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM schema_version WHERE version = ?
	`, ArchivalistEventsMigrationVersion).Scan(&count)

	if err != nil {
		t.Fatalf("failed to query schema_version: %v", err)
	}

	if count != 0 {
		t.Errorf("expected 0 schema_version entries, got %d", count)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestFullMigrationWorkflow(t *testing.T) {
	db := setupArchivalistTestDB(t)
	defer db.Close()

	migration := NewArchivalistEventsMigration(db)

	// Apply migration
	if err := migration.Apply(); err != nil {
		t.Fatalf("migration apply failed: %v", err)
	}

	// Insert data into all tables
	_, err := db.Exec(`
		INSERT INTO activity_events
		(id, event_type, timestamp, session_id, content, created_at)
		VALUES ('a1', 2, '2024-01-01T10:00:00Z', 's1', 'content', '2024-01-01T10:00:00Z')
	`)
	if err != nil {
		t.Fatalf("failed to insert activity event: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO index_events
		(id, event_type, timestamp, session_id, index_type, index_version,
		 prev_version, root_path, duration_ns, created_at)
		VALUES ('i1', 11, '2024-01-01T10:00:00Z', 's1', 0, 1, 0, '/path', 1000, '2024-01-01T10:00:00Z')
	`)
	if err != nil {
		t.Fatalf("failed to insert index event: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO session_summaries
		(id, session_id, started_at, created_at)
		VALUES ('sum1', 's1', '2024-01-01T10:00:00Z', '2024-01-01T10:00:00Z')
	`)
	if err != nil {
		t.Fatalf("failed to insert session summary: %v", err)
	}

	// Verify all data exists
	var activityCount, indexCount, summaryCount int
	db.QueryRow("SELECT COUNT(*) FROM activity_events").Scan(&activityCount)
	db.QueryRow("SELECT COUNT(*) FROM index_events").Scan(&indexCount)
	db.QueryRow("SELECT COUNT(*) FROM session_summaries").Scan(&summaryCount)

	if activityCount != 1 || indexCount != 1 || summaryCount != 1 {
		t.Errorf("expected 1 record in each table, got: activity=%d, index=%d, summary=%d",
			activityCount, indexCount, summaryCount)
	}
}
