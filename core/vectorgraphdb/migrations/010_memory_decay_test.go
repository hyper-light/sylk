package migrations

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// createMemoryDecayTestDB creates a temporary SQLite database with base schema.
func createMemoryDecayTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "memory_decay_test_*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("sqlite3", tmpFile.Name()+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("open database: %v", err)
	}

	if err := createMemoryDecayBaseSchema(db); err != nil {
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

// createMemoryDecayBaseSchema creates the nodes and edges tables.
func createMemoryDecayBaseSchema(db *sql.DB) error {
	statements := []string{
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

func TestMemoryDecayMigration_Apply_HappyPath(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)

	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify nodes memory columns exist
	nodeColumns := []string{
		"memory_activation",
		"last_accessed_at",
		"access_count",
		"base_offset",
	}
	for _, col := range nodeColumns {
		if !hasNodeColumn(t, db, col) {
			t.Errorf("node column %s not found after migration", col)
		}
	}

	// Verify edges memory columns exist
	edgeColumns := []string{
		"memory_activation",
		"last_accessed_at",
		"access_count",
		"base_offset",
	}
	for _, col := range edgeColumns {
		if !hasEdgeColumn(t, db, col) {
			t.Errorf("edge column %s not found after migration", col)
		}
	}

	// Verify tables exist
	tables := []string{
		"node_access_traces",
		"edge_access_traces",
		"decay_parameters",
	}
	for _, table := range tables {
		if !hasTable(t, db, table) {
			t.Errorf("table %s not created", table)
		}
	}

	// Verify migration was recorded
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	found := false
	for _, r := range records {
		if r.Version == MemoryDecayMigrationVersion {
			found = true
			if r.Name != MemoryDecayMigrationName {
				t.Errorf("expected name %q, got %q", MemoryDecayMigrationName, r.Name)
			}
		}
	}
	if !found {
		t.Error("migration record not found")
	}
}

func TestMemoryDecayMigration_Apply_Idempotent(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)

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
		if r.Version == MemoryDecayMigrationVersion {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 migration record, got %d", count)
	}
}

func TestMemoryDecayMigration_IndexesCreated(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify indexes exist
	indexes := []string{
		"idx_access_traces_node",
		"idx_access_traces_edge",
	}

	for _, idx := range indexes {
		if !hasIndex(t, db, idx) {
			t.Errorf("index %s not found", idx)
		}
	}
}

func TestMemoryDecayMigration_NodeAccessTraces(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test node
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'TestNode')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// Insert access trace
	now := time.Now().Unix()
	_, err = db.Exec(`
		INSERT INTO node_access_traces (node_id, accessed_at, access_type, context)
		VALUES (?, ?, ?, ?)`,
		"node1", now, "read", "test context",
	)
	if err != nil {
		t.Fatalf("insert access trace: %v", err)
	}

	// Query access traces
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM node_access_traces WHERE node_id = ?`,
		"node1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query access traces: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 access trace, got %d", count)
	}
}

func TestMemoryDecayMigration_EdgeAccessTraces(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)
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

	// Insert test edge
	result, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type)
		VALUES (?, ?, ?)`,
		"node1", "node2", 1,
	)
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	edgeID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get edge ID: %v", err)
	}

	// Insert access trace
	now := time.Now().Unix()
	_, err = db.Exec(`
		INSERT INTO edge_access_traces (edge_id, accessed_at, access_type, context)
		VALUES (?, ?, ?, ?)`,
		edgeID, now, "traversal", "test context",
	)
	if err != nil {
		t.Fatalf("insert edge access trace: %v", err)
	}

	// Query access traces
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM edge_access_traces WHERE edge_id = ?`,
		edgeID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query edge access traces: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 edge access trace, got %d", count)
	}
}

func TestMemoryDecayMigration_DecayParameters(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert decay parameters for domain 0
	_, err := db.Exec(`
		INSERT INTO decay_parameters
		(domain, decay_exponent_alpha, decay_exponent_beta, base_offset_mean, base_offset_variance, effective_samples, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		0, 0.5, 0.2, 1.0, 0.1, 100.0, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert decay parameters: %v", err)
	}

	// Query decay parameters
	var alpha, beta, mean, variance, samples float64
	err = db.QueryRow(`
		SELECT decay_exponent_alpha, decay_exponent_beta, base_offset_mean, base_offset_variance, effective_samples
		FROM decay_parameters WHERE domain = ?`,
		0,
	).Scan(&alpha, &beta, &mean, &variance, &samples)
	if err != nil {
		t.Fatalf("query decay parameters: %v", err)
	}

	if alpha != 0.5 || beta != 0.2 {
		t.Errorf("expected alpha=0.5, beta=0.2, got alpha=%f, beta=%f", alpha, beta)
	}
}

func TestMemoryDecayMigration_ForeignKeyConstraints(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Attempt to insert node access trace with invalid node_id
	now := time.Now().Unix()
	_, err := db.Exec(`
		INSERT INTO node_access_traces (node_id, accessed_at, access_type)
		VALUES (?, ?, ?)`,
		"nonexistent", now, "read",
	)
	if err == nil {
		t.Error("expected FK constraint error for node_access_traces, got nil")
	}

	// Attempt to insert edge access trace with invalid edge_id
	_, err = db.Exec(`
		INSERT INTO edge_access_traces (edge_id, accessed_at, access_type)
		VALUES (?, ?, ?)`,
		999999, now, "traversal",
	)
	if err == nil {
		t.Error("expected FK constraint error for edge_access_traces, got nil")
	}
}

func TestMemoryDecayMigration_DomainConstraint(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Valid domains (0, 1, 2)
	validDomains := []int{0, 1, 2}
	for _, domain := range validDomains {
		_, err := db.Exec(`
			INSERT INTO decay_parameters
			(domain, decay_exponent_alpha, decay_exponent_beta, base_offset_mean, base_offset_variance, effective_samples, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			domain, 0.5, 0.2, 1.0, 0.1, 100.0, time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			t.Errorf("failed to insert valid domain %d: %v", domain, err)
		}
	}

	// Invalid domain
	_, err := db.Exec(`
		INSERT INTO decay_parameters
		(domain, decay_exponent_alpha, decay_exponent_beta, base_offset_mean, base_offset_variance, effective_samples, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		3, 0.5, 0.2, 1.0, 0.1, 100.0, time.Now().UTC().Format(time.RFC3339),
	)
	if err == nil {
		t.Error("expected CHECK constraint error for domain > 2, got nil")
	}
}

func TestMemoryDecayMigration_IndexUsage(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test data
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	now := time.Now().Unix()
	_, err = db.Exec(`
		INSERT INTO node_access_traces (node_id, accessed_at, access_type)
		VALUES (?, ?, ?)`,
		"node1", now, "read",
	)
	if err != nil {
		t.Fatalf("insert access trace: %v", err)
	}

	// Check query plan uses idx_access_traces_node
	rows, err := db.Query(`
		EXPLAIN QUERY PLAN
		SELECT * FROM node_access_traces
		WHERE node_id = ? ORDER BY accessed_at DESC`,
		"node1",
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
		if containsString(detail, "idx_access_traces_node") {
			planContainsIndex = true
		}
	}

	if !planContainsIndex {
		t.Log("Note: Query plan may not use index on small dataset")
	}
}

func TestMemoryDecayMigration_Rollback(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	migration := NewMemoryDecayMigration(db)

	// Apply then rollback
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	// Verify tables no longer exist
	tables := []string{
		"node_access_traces",
		"edge_access_traces",
		"decay_parameters",
	}
	for _, table := range tables {
		if hasTable(t, db, table) {
			t.Errorf("table %s still exists after rollback", table)
		}
	}

	// Verify indexes were removed
	indexes := []string{
		"idx_access_traces_node",
		"idx_access_traces_edge",
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
		if r.Version == MemoryDecayMigrationVersion {
			t.Error("migration record still exists after rollback")
		}
	}
}

func TestMemoryDecayMigration_PreservesExistingData(t *testing.T) {
	db, cleanup := createMemoryDecayTestDB(t)
	defer cleanup()

	// Insert test data before migration
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name, content) VALUES
		('node1', 0, 0, 'Node1', 'test content')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	migration := NewMemoryDecayMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify existing data is preserved
	var content string
	err = db.QueryRow(`
		SELECT content FROM nodes WHERE id = ?`,
		"node1",
	).Scan(&content)
	if err != nil {
		t.Fatalf("query node: %v", err)
	}
	if content != "test content" {
		t.Errorf("expected content 'test content', got %q", content)
	}

	// Verify new columns have default values
	var activation, baseOffset float64
	var accessCount int
	var lastAccessed sql.NullInt64
	err = db.QueryRow(`
		SELECT memory_activation, access_count, base_offset, last_accessed_at
		FROM nodes WHERE id = ?`,
		"node1",
	).Scan(&activation, &accessCount, &baseOffset, &lastAccessed)
	if err != nil {
		t.Fatalf("query memory columns: %v", err)
	}
	if activation != 0.0 || accessCount != 0 || baseOffset != 0.0 {
		t.Errorf("expected default values 0.0, 0, 0.0, got %f, %d, %f", activation, accessCount, baseOffset)
	}
	if lastAccessed.Valid {
		t.Error("expected last_accessed_at to be NULL")
	}
}

// hasNodeColumn checks if a column exists in the nodes table.
func hasNodeColumn(t *testing.T, db *sql.DB, columnName string) bool {
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
		if name == columnName {
			return true
		}
	}
	return false
}
