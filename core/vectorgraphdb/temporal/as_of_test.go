package temporal

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// Test Setup Helpers
// =============================================================================

// setupTestDB creates an in-memory SQLite database with the required schema
// for temporal edge testing.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	if err := createTestSchema(db); err != nil {
		db.Close()
		t.Fatalf("failed to create test schema: %v", err)
	}

	return db
}

// createTestSchema creates the necessary tables for testing.
func createTestSchema(db *sql.DB) error {
	schema := `
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
			superseded_by TEXT
		);

		CREATE TABLE edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			edge_type INTEGER NOT NULL,
			weight REAL DEFAULT 1.0,
			metadata JSON,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			valid_from INTEGER,
			valid_to INTEGER,
			tx_start INTEGER,
			tx_end INTEGER,
			FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE
		);

		CREATE INDEX idx_edges_valid_time ON edges(source_id, valid_from, valid_to);
		CREATE INDEX idx_edges_tx_time ON edges(source_id, tx_start, tx_end);
	`

	_, err := db.Exec(schema)
	return err
}

// insertTestNode inserts a node for testing purposes.
func insertTestNode(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()

	_, err := db.Exec(`
		INSERT INTO nodes (id, domain, node_type, name, created_at, updated_at)
		VALUES (?, 0, 0, ?, datetime('now'), datetime('now'))
	`, id, name)
	if err != nil {
		t.Fatalf("failed to insert test node %s: %v", id, err)
	}
}

// insertTemporalEdge inserts a temporal edge for testing purposes.
func insertTemporalEdge(t *testing.T, db *sql.DB, sourceID, targetID string, edgeType vectorgraphdb.EdgeType, validFrom, validTo *time.Time) int64 {
	t.Helper()

	var vfUnix, vtUnix interface{}
	if validFrom != nil {
		vfUnix = validFrom.Unix()
	}
	if validTo != nil {
		vtUnix = validTo.Unix()
	}

	result, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to)
		VALUES (?, ?, ?, 1.0, '{}', datetime('now'), ?, ?)
	`, sourceID, targetID, edgeType, vfUnix, vtUnix)
	if err != nil {
		t.Fatalf("failed to insert temporal edge: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert id: %v", err)
	}
	return id
}

// =============================================================================
// AsOfQuerier Tests
// =============================================================================

func TestNewAsOfQuerier(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	q := NewAsOfQuerier(db)
	if q == nil {
		t.Fatal("NewAsOfQuerier returned nil")
	}
	if q.db != db {
		t.Error("NewAsOfQuerier did not set db correctly")
	}
}

func TestAsOfQuerier_NilDB(t *testing.T) {
	q := NewAsOfQuerier(nil)

	ctx := context.Background()
	now := time.Now()

	_, err := q.GetEdgesAsOf(ctx, "node1", now)
	if err != ErrNilDB {
		t.Errorf("expected ErrNilDB, got %v", err)
	}

	_, err = q.GetNodesAsOf(ctx, now)
	if err != ErrNilDB {
		t.Errorf("expected ErrNilDB, got %v", err)
	}

	_, err = q.GetIncomingEdgesAsOf(ctx, "node1", now)
	if err != ErrNilDB {
		t.Errorf("expected ErrNilDB, got %v", err)
	}
}

func TestAsOfQuerier_GetEdgesAsOf_EmptyResult(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	edges, err := q.GetEdgesAsOf(ctx, "node1", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

func TestAsOfQuerier_GetEdgesAsOf_CurrentEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	// Create an edge that is currently valid (no valid_to)
	validFrom := time.Now().Add(-1 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, nil)

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Query at current time should find the edge
	edges, err := q.GetEdgesAsOf(ctx, "node1", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].SourceID != "node1" {
		t.Errorf("expected source_id 'node1', got '%s'", edges[0].SourceID)
	}
	if edges[0].TargetID != "node2" {
		t.Errorf("expected target_id 'node2', got '%s'", edges[0].TargetID)
	}
}

func TestAsOfQuerier_GetEdgesAsOf_ExpiredEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	// Create an edge that expired in the past
	validFrom := time.Now().Add(-2 * time.Hour)
	validTo := time.Now().Add(-1 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, &validTo)

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Query at current time should NOT find the edge (it expired)
	edges, err := q.GetEdgesAsOf(ctx, "node1", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges (expired), got %d", len(edges))
	}

	// Query at the time when the edge was valid should find it
	queryTime := validFrom.Add(30 * time.Minute)
	edges, err = q.GetEdgesAsOf(ctx, "node1", queryTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge at valid time, got %d", len(edges))
	}
}

func TestAsOfQuerier_GetEdgesAsOf_FutureEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	// Create an edge that starts in the future
	validFrom := time.Now().Add(1 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, nil)

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Query at current time should NOT find the edge (not valid yet)
	edges, err := q.GetEdgesAsOf(ctx, "node1", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges (not valid yet), got %d", len(edges))
	}

	// Query at future time should find the edge
	edges, err = q.GetEdgesAsOf(ctx, "node1", validFrom.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge at future valid time, got %d", len(edges))
	}
}

func TestAsOfQuerier_GetEdgesAsOf_MultipleEdges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Edge 1: Valid from 2 hours ago, still current
	vf1 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, nil)

	// Edge 2: Valid from 2 hours ago, still current
	vf2 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, nil)

	// Edge 3: Expired 30 minutes ago (valid from 2h ago to 30m ago)
	vf3 := now.Add(-2 * time.Hour)
	vt3 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeImports, &vf3, &vt3)

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Query at current time should find 2 edges (not the expired one)
	edges, err := q.GetEdgesAsOf(ctx, "node1", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(edges))
	}

	// Query at 1.5 hours ago should find all 3 edges
	// (Edge 3 was still valid at that time since it expires at T-30m)
	queryTime := now.Add(-90 * time.Minute)
	edges, err = q.GetEdgesAsOf(ctx, "node1", queryTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 3 {
		t.Errorf("expected 3 edges at earlier time, got %d", len(edges))
	}
}

func TestAsOfQuerier_GetEdgesAsOfWithTarget(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	vf := now.Add(-1 * time.Hour)

	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf, nil)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeCalls, &vf, nil)

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Query edges from node1 to node2 only
	edges, err := q.GetEdgesAsOfWithTarget(ctx, "node1", "node2", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].TargetID != "node2" {
		t.Errorf("expected target_id 'node2', got '%s'", edges[0].TargetID)
	}
}

func TestAsOfQuerier_GetIncomingEdgesAsOf(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	vf := now.Add(-1 * time.Hour)

	// Create edges pointing TO node3
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeCalls, &vf, nil)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeDefines, &vf, nil)

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Query incoming edges to node3
	edges, err := q.GetIncomingEdgesAsOf(ctx, "node3", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 incoming edges, got %d", len(edges))
	}
}

func TestAsOfQuerier_GetEdgesAsOfByType(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	vf := now.Add(-1 * time.Hour)

	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf, nil)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeDefines, &vf, nil)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeImports, &vf, nil)

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Query only EdgeTypeCalls
	edges, err := q.GetEdgesAsOfByType(ctx, "node1", vectorgraphdb.EdgeTypeCalls, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].EdgeType != vectorgraphdb.EdgeTypeCalls {
		t.Errorf("expected edge type %v, got %v", vectorgraphdb.EdgeTypeCalls, edges[0].EdgeType)
	}
}

func TestAsOfQuerier_GetNodesAsOf(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert nodes with different creation times
	now := time.Now()

	_, err := db.Exec(`
		INSERT INTO nodes (id, domain, node_type, name, created_at, updated_at)
		VALUES (?, 0, 0, ?, ?, ?)
	`, "node1", "Node 1", now.Add(-2*time.Hour).Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to insert node1: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO nodes (id, domain, node_type, name, created_at, updated_at)
		VALUES (?, 0, 0, ?, ?, ?)
	`, "node2", "Node 2", now.Add(-1*time.Hour).Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to insert node2: %v", err)
	}

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Query at current time should find both nodes
	nodes, err := q.GetNodesAsOf(ctx, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}

	// Query at 1.5 hours ago should find only node1
	queryTime := now.Add(-90 * time.Minute)
	nodes, err = q.GetNodesAsOf(ctx, queryTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("expected 1 node at earlier time, got %d", len(nodes))
	}
}

func TestAsOfQuerier_EdgeWithNullValidFrom(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	// Insert edge with NULL valid_from (always valid from the beginning)
	_, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to)
		VALUES (?, ?, ?, 1.0, '{}', datetime('now'), NULL, NULL)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls)
	if err != nil {
		t.Fatalf("failed to insert edge: %v", err)
	}

	q := NewAsOfQuerier(db)
	ctx := context.Background()

	// Edge with NULL valid_from should be found at any time
	edges, err := q.GetEdgesAsOf(ctx, "node1", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge with NULL valid_from, got %d", len(edges))
	}

	// Also found at a very old time
	oldTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	edges, err = q.GetEdgesAsOf(ctx, "node1", oldTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge at old time, got %d", len(edges))
	}
}
