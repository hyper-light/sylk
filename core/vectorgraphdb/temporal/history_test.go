package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// EdgeHistoryQuerier Tests
// =============================================================================

func TestNewEdgeHistoryQuerier(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	q := NewEdgeHistoryQuerier(db)
	if q == nil {
		t.Fatal("NewEdgeHistoryQuerier returned nil")
	}
	if q.db != db {
		t.Error("NewEdgeHistoryQuerier did not set db correctly")
	}
}

func TestEdgeHistoryQuerier_NilDB(t *testing.T) {
	q := NewEdgeHistoryQuerier(nil)

	ctx := context.Background()
	now := time.Now()

	_, err := q.GetEdgeHistory(ctx, 1)
	if err != ErrNilDB {
		t.Errorf("GetEdgeHistory: expected ErrNilDB, got %v", err)
	}

	_, err = q.GetEdgeVersionAt(ctx, 1, now)
	if err != ErrNilDB {
		t.Errorf("GetEdgeVersionAt: expected ErrNilDB, got %v", err)
	}

	_, err = q.GetEdgeVersionCount(ctx, 1)
	if err != ErrNilDB {
		t.Errorf("GetEdgeVersionCount: expected ErrNilDB, got %v", err)
	}

	_, err = q.GetEdgeHistoryBySourceTarget(ctx, "node1", "node2")
	if err != ErrNilDB {
		t.Errorf("GetEdgeHistoryBySourceTarget: expected ErrNilDB, got %v", err)
	}

	_, err = q.GetEdgeHistoryForNode(ctx, "node1")
	if err != ErrNilDB {
		t.Errorf("GetEdgeHistoryForNode: expected ErrNilDB, got %v", err)
	}

	_, err = q.GetSupersededEdges(ctx)
	if err != ErrNilDB {
		t.Errorf("GetSupersededEdges: expected ErrNilDB, got %v", err)
	}

	_, err = q.GetSoftDeletedEdges(ctx)
	if err != ErrNilDB {
		t.Errorf("GetSoftDeletedEdges: expected ErrNilDB, got %v", err)
	}
}

func TestEdgeHistoryQuerier_GetEdgeHistory_NoEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	// Query for non-existent edge
	_, err := q.GetEdgeHistory(ctx, 999)
	if err != ErrNoResults {
		t.Errorf("expected ErrNoResults for non-existent edge, got %v", err)
	}
}

func TestEdgeHistoryQuerier_GetEdgeHistory_SingleVersion(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)
	edgeID := insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, nil)

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	history, err := q.GetEdgeHistory(ctx, edgeID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(history) != 1 {
		t.Errorf("expected 1 version, got %d", len(history))
	}

	if history[0].SourceID != "node1" {
		t.Errorf("expected source_id 'node1', got '%s'", history[0].SourceID)
	}
}

func TestEdgeHistoryQuerier_GetEdgeHistory_MultipleVersions(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// For version tracking, we use GetEdgeHistoryBySourceTarget which queries
	// all edges between two nodes over time. Each "version" is a separate row
	// with different temporal bounds.

	vf := now.Add(-2 * time.Hour)
	txStart1 := now.Add(-2 * time.Hour)
	txEnd1 := now.Add(-1 * time.Hour)
	txStart2 := now.Add(-1 * time.Hour)

	// First version (superseded)
	result1, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, 1.0, '{}', datetime('now'), ?, NULL, ?, ?)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls, vf.Unix(), txStart1.Unix(), txEnd1.Unix())
	if err != nil {
		t.Fatalf("failed to insert first version: %v", err)
	}

	edgeID1, _ := result1.LastInsertId()

	// Second version (current) - this is a new row representing the updated edge
	result2, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, 1.5, '{}', datetime('now'), ?, NULL, ?, NULL)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls, vf.Unix(), txStart2.Unix())
	if err != nil {
		t.Fatalf("failed to insert second version: %v", err)
	}

	edgeID2, _ := result2.LastInsertId()

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	// Test GetEdgeHistoryBySourceTarget to get all versions
	history, err := q.GetEdgeHistoryBySourceTarget(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("expected 2 versions, got %d", len(history))
	}

	// Should be ordered by tx_start DESC (most recent first)
	if len(history) >= 2 {
		if history[0].Weight != 1.5 {
			t.Errorf("expected first (most recent) version to have weight 1.5, got %.2f", history[0].Weight)
		}
		if history[1].Weight != 1.0 {
			t.Errorf("expected second (older) version to have weight 1.0, got %.2f", history[1].Weight)
		}
	}

	// Individual edge queries should work too
	hist1, err := q.GetEdgeHistory(ctx, edgeID1)
	if err != nil {
		t.Fatalf("unexpected error for edge1: %v", err)
	}
	if len(hist1) != 1 {
		t.Errorf("expected 1 version for edgeID1, got %d", len(hist1))
	}

	hist2, err := q.GetEdgeHistory(ctx, edgeID2)
	if err != nil {
		t.Fatalf("unexpected error for edge2: %v", err)
	}
	if len(hist2) != 1 {
		t.Errorf("expected 1 version for edgeID2, got %d", len(hist2))
	}
}

func TestEdgeHistoryQuerier_GetEdgeVersionAt(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	vf := now.Add(-2 * time.Hour)
	txStart := now.Add(-1 * time.Hour)

	// Insert an edge with tx_start set
	result, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, 1.0, '{}', datetime('now'), ?, NULL, ?, NULL)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls, vf.Unix(), txStart.Unix())
	if err != nil {
		t.Fatalf("failed to insert edge: %v", err)
	}

	edgeID, _ := result.LastInsertId()

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	// Query at a time after the edge was created
	edge, err := q.GetEdgeVersionAt(ctx, edgeID, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if edge == nil {
		t.Fatal("expected edge, got nil")
	}

	if edge.SourceID != "node1" {
		t.Errorf("expected source_id 'node1', got '%s'", edge.SourceID)
	}
}

func TestEdgeHistoryQuerier_GetEdgeVersionAt_BeforeCreation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	vf := now.Add(-1 * time.Hour)
	txStart := now.Add(-30 * time.Minute)

	// Insert an edge with tx_start set to 30 minutes ago
	result, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, 1.0, '{}', datetime('now'), ?, NULL, ?, NULL)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls, vf.Unix(), txStart.Unix())
	if err != nil {
		t.Fatalf("failed to insert edge: %v", err)
	}

	edgeID, _ := result.LastInsertId()

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	// Query at a time before the edge was recorded (tx_start)
	queryTime := now.Add(-1 * time.Hour)
	_, err = q.GetEdgeVersionAt(ctx, edgeID, queryTime)
	if err != ErrNoResults {
		t.Errorf("expected ErrNoResults for time before edge creation, got %v", err)
	}
}

func TestEdgeHistoryQuerier_GetEdgeVersionCount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	vf := now.Add(-1 * time.Hour)
	edgeID := insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf, nil)

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	count, err := q.GetEdgeVersionCount(ctx, edgeID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
}

func TestEdgeHistoryQuerier_GetEdgeVersionCount_NoEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	count, err := q.GetEdgeVersionCount(ctx, 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if count != 0 {
		t.Errorf("expected count 0 for non-existent edge, got %d", count)
	}
}

func TestEdgeHistoryQuerier_GetEdgeHistoryBySourceTarget(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	vf := now.Add(-1 * time.Hour)

	// Edge from node1 to node2
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf, nil)
	// Edge from node1 to node3 (should not be included)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf, nil)
	// Another edge from node1 to node2
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeImports, &vf, nil)

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	history, err := q.GetEdgeHistoryBySourceTarget(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("expected 2 edges between node1 and node2, got %d", len(history))
	}
}

func TestEdgeHistoryQuerier_GetEdgeHistoryForNode(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	vf := now.Add(-1 * time.Hour)

	// Edges from node1
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf, nil)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf, nil)
	// Edge from node2 (should not be included)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeImports, &vf, nil)

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	history, err := q.GetEdgeHistoryForNode(ctx, "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("expected 2 edges from node1, got %d", len(history))
	}
}

func TestEdgeHistoryQuerier_GetSupersededEdges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	vf := now.Add(-2 * time.Hour)
	txStart := now.Add(-2 * time.Hour)
	txEnd := now.Add(-1 * time.Hour)

	// Superseded edge (has tx_end)
	_, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, 1.0, '{}', datetime('now'), ?, NULL, ?, ?)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls, vf.Unix(), txStart.Unix(), txEnd.Unix())
	if err != nil {
		t.Fatalf("failed to insert superseded edge: %v", err)
	}

	// Current edge (no tx_end)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeDefines, &vf, nil)

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	superseded, err := q.GetSupersededEdges(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(superseded) != 1 {
		t.Errorf("expected 1 superseded edge, got %d", len(superseded))
	}

	if len(superseded) > 0 && superseded[0].TxEnd == nil {
		t.Error("superseded edge should have tx_end set")
	}
}

func TestEdgeHistoryQuerier_GetSoftDeletedEdges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	vf := now.Add(-2 * time.Hour)
	vt := now.Add(-1 * time.Hour)

	// Soft-deleted edge (has valid_to)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf, &vt)

	// Current edge (no valid_to)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeDefines, &vf, nil)

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	deleted, err := q.GetSoftDeletedEdges(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(deleted) != 1 {
		t.Errorf("expected 1 soft-deleted edge, got %d", len(deleted))
	}

	if len(deleted) > 0 && deleted[0].ValidTo == nil {
		t.Error("soft-deleted edge should have valid_to set")
	}
}

func TestEdgeHistoryQuerier_ComplexScenario(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Scenario: Track the evolution of relationships
	// T-3h: edge1 created (node1->node2, calls, weight 1.0)
	// T-2h: edge1 superseded, edge1v2 created (weight 2.0), edge2 created (node1->node3, defines)
	// T-1h: edge1v2 soft-deleted, edge2 still active
	// T-0: edge3 created (node1->node2, imports)

	vf1 := now.Add(-3 * time.Hour)
	tx1Start := now.Add(-3 * time.Hour)
	tx1End := now.Add(-2 * time.Hour)

	// edge1 v1 (superseded)
	result1, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, 1.0, '{}', datetime('now'), ?, NULL, ?, ?)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls, vf1.Unix(), tx1Start.Unix(), tx1End.Unix())
	if err != nil {
		t.Fatalf("failed to insert edge1 v1: %v", err)
	}
	edge1v1ID, _ := result1.LastInsertId()

	// edge1 v2 (soft-deleted) - a new row representing the updated version
	tx2Start := now.Add(-2 * time.Hour)
	vt := now.Add(-1 * time.Hour)
	result2, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, 2.0, '{}', datetime('now'), ?, ?, ?, NULL)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls, vf1.Unix(), vt.Unix(), tx2Start.Unix())
	if err != nil {
		t.Fatalf("failed to insert edge1 v2: %v", err)
	}
	edge1v2ID, _ := result2.LastInsertId()

	// edge2 (still active)
	vf2 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, nil)

	// edge3 (newly created)
	vf3 := now
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeImports, &vf3, nil)

	q := NewEdgeHistoryQuerier(db)
	ctx := context.Background()

	// Test 1: Get history by source/target (should have 3 versions: edge1v1, edge1v2, edge3)
	historyByNodes, err := q.GetEdgeHistoryBySourceTarget(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetEdgeHistoryBySourceTarget failed: %v", err)
	}
	if len(historyByNodes) != 3 {
		t.Errorf("expected 3 versions between node1->node2, got %d", len(historyByNodes))
	}

	// Test 2: Get all edges from node1 (should have 4: edge1v1, edge1v2, edge2, edge3)
	allHistory, err := q.GetEdgeHistoryForNode(ctx, "node1")
	if err != nil {
		t.Fatalf("GetEdgeHistoryForNode failed: %v", err)
	}
	if len(allHistory) != 4 {
		t.Errorf("expected 4 edge versions from node1, got %d", len(allHistory))
	}

	// Test 3: Get soft-deleted edges (should have 1: edge1v2)
	deleted, err := q.GetSoftDeletedEdges(ctx)
	if err != nil {
		t.Fatalf("GetSoftDeletedEdges failed: %v", err)
	}
	if len(deleted) != 1 {
		t.Errorf("expected 1 soft-deleted edge, got %d", len(deleted))
	}

	// Test 4: Get superseded edges (should have 1: edge1v1)
	superseded, err := q.GetSupersededEdges(ctx)
	if err != nil {
		t.Fatalf("GetSupersededEdges failed: %v", err)
	}
	if len(superseded) != 1 {
		t.Errorf("expected 1 superseded edge, got %d", len(superseded))
	}

	// Test 5: Get version at T-2.5h (should get edge1v1)
	queryTime := now.Add(-150 * time.Minute)
	version, err := q.GetEdgeVersionAt(ctx, edge1v1ID, queryTime)
	if err != nil {
		t.Fatalf("GetEdgeVersionAt failed: %v", err)
	}
	if version.Weight != 1.0 {
		t.Errorf("expected weight 1.0 at T-2.5h, got %.2f", version.Weight)
	}

	// Test 6: Get version at T-30m (should get edge1v2)
	queryTime2 := now.Add(-30 * time.Minute)
	version2, err := q.GetEdgeVersionAt(ctx, edge1v2ID, queryTime2)
	if err != nil {
		t.Fatalf("GetEdgeVersionAt failed: %v", err)
	}
	if version2.Weight != 2.0 {
		t.Errorf("expected weight 2.0 at T-30m, got %.2f", version2.Weight)
	}
}
