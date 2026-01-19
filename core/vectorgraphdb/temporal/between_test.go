package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// BetweenQuerier Tests
// =============================================================================

func TestNewBetweenQuerier(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	q := NewBetweenQuerier(db)
	if q == nil {
		t.Fatal("NewBetweenQuerier returned nil")
	}
	if q.db != db {
		t.Error("NewBetweenQuerier did not set db correctly")
	}
}

func TestBetweenQuerier_NilDB(t *testing.T) {
	q := NewBetweenQuerier(nil)

	ctx := context.Background()
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()

	_, err := q.GetEdgesBetween(ctx, "node1", start, end)
	if err != ErrNilDB {
		t.Errorf("expected ErrNilDB, got %v", err)
	}

	_, err = q.GetEdgesCreatedBetween(ctx, start, end)
	if err != ErrNilDB {
		t.Errorf("expected ErrNilDB, got %v", err)
	}

	_, err = q.GetEdgesExpiredBetween(ctx, start, end)
	if err != ErrNilDB {
		t.Errorf("expected ErrNilDB, got %v", err)
	}
}

func TestBetweenQuerier_InvalidTimeRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	// start after end should return error
	start := time.Now()
	end := time.Now().Add(-1 * time.Hour)

	_, err := q.GetEdgesBetween(ctx, "node1", start, end)
	if err != ErrInvalidTimeRange {
		t.Errorf("expected ErrInvalidTimeRange, got %v", err)
	}

	_, err = q.GetEdgesCreatedBetween(ctx, start, end)
	if err != ErrInvalidTimeRange {
		t.Errorf("expected ErrInvalidTimeRange, got %v", err)
	}

	_, err = q.GetEdgesExpiredBetween(ctx, start, end)
	if err != ErrInvalidTimeRange {
		t.Errorf("expected ErrInvalidTimeRange, got %v", err)
	}
}

func TestBetweenQuerier_GetEdgesBetween_EmptyResult(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()

	edges, err := q.GetEdgesBetween(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

func TestBetweenQuerier_GetEdgesBetween_EdgeStartedDuringRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// Edge started 30 minutes ago, still valid
	validFrom := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	// Query range: 1 hour ago to now
	start := now.Add(-1 * time.Hour)
	end := now

	edges, err := q.GetEdgesBetween(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge (created during range), got %d", len(edges))
	}
}

func TestBetweenQuerier_GetEdgesBetween_EdgeEndedDuringRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// Edge started 2 hours ago, ended 30 minutes ago
	validFrom := now.Add(-2 * time.Hour)
	validTo := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, &validTo)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	// Query range: 1 hour ago to now (edge ended during this range)
	start := now.Add(-1 * time.Hour)
	end := now

	edges, err := q.GetEdgesBetween(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge (ended during range), got %d", len(edges))
	}
}

func TestBetweenQuerier_GetEdgesBetween_EdgeSpansRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// Edge started 2 hours ago, still valid (spans the query range)
	validFrom := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	// Query range: 1 hour ago to 30 minutes ago
	start := now.Add(-1 * time.Hour)
	end := now.Add(-30 * time.Minute)

	edges, err := q.GetEdgesBetween(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge (spans range), got %d", len(edges))
	}
}

func TestBetweenQuerier_GetEdgesBetween_EdgeOutsideRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// Edge that existed before the range
	vf1 := now.Add(-3 * time.Hour)
	vt1 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, &vt1)

	// Edge that will exist after the range
	vf2 := now.Add(1 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeDefines, &vf2, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	// Query range: 1 hour ago to now
	start := now.Add(-1 * time.Hour)
	end := now

	edges, err := q.GetEdgesBetween(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges (all outside range), got %d", len(edges))
	}
}

func TestBetweenQuerier_GetEdgesCreatedBetween(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Edge created 30 minutes ago
	vf1 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, nil)

	// Edge created 2 hours ago (outside range)
	vf2 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	// Query for edges created in the last hour
	start := now.Add(-1 * time.Hour)
	end := now

	edges, err := q.GetEdgesCreatedBetween(ctx, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge created in range, got %d", len(edges))
	}
	if len(edges) > 0 && edges[0].TargetID != "node2" {
		t.Errorf("expected target 'node2', got '%s'", edges[0].TargetID)
	}
}

func TestBetweenQuerier_GetEdgesExpiredBetween(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Edge expired 30 minutes ago
	vf1 := now.Add(-2 * time.Hour)
	vt1 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, &vt1)

	// Edge expired 2 hours ago (outside range)
	vf2 := now.Add(-3 * time.Hour)
	vt2 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, &vt2)

	// Edge still valid (no expiration)
	vf3 := now.Add(-1 * time.Hour)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeImports, &vf3, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	// Query for edges expired in the last hour
	start := now.Add(-1 * time.Hour)
	end := now

	edges, err := q.GetEdgesExpiredBetween(ctx, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge expired in range, got %d", len(edges))
	}
	if len(edges) > 0 && edges[0].TargetID != "node2" {
		t.Errorf("expected target 'node2', got '%s'", edges[0].TargetID)
	}
}

func TestBetweenQuerier_GetEdgesBetweenWithTarget(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	vf := now.Add(-30 * time.Minute)

	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf, nil)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeCalls, &vf, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	start := now.Add(-1 * time.Hour)
	end := now

	// Query edges from node1 to node2 only
	edges, err := q.GetEdgesBetweenWithTarget(ctx, "node1", "node2", start, end)
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

func TestBetweenQuerier_GetIncomingEdgesBetween(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	vf := now.Add(-30 * time.Minute)

	// Create edges pointing TO node3
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeCalls, &vf, nil)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeDefines, &vf, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	start := now.Add(-1 * time.Hour)
	end := now

	// Query incoming edges to node3
	edges, err := q.GetIncomingEdgesBetween(ctx, "node3", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 incoming edges, got %d", len(edges))
	}
}

func TestBetweenQuerier_GetEdgesBetweenByType(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	vf := now.Add(-30 * time.Minute)

	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf, nil)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeDefines, &vf, nil)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeImports, &vf, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	start := now.Add(-1 * time.Hour)
	end := now

	// Query only EdgeTypeCalls
	edges, err := q.GetEdgesBetweenByType(ctx, "node1", vectorgraphdb.EdgeTypeCalls, start, end)
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

func TestBetweenQuerier_GetEdgesModifiedBetween(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Edge created during the range
	vf1 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, nil)

	// Edge expired during the range
	vf2 := now.Add(-2 * time.Hour)
	vt2 := now.Add(-20 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, &vt2)

	// Edge completely outside the range (created and expired before)
	vf3 := now.Add(-3 * time.Hour)
	vt3 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeImports, &vf3, &vt3)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	start := now.Add(-1 * time.Hour)
	end := now

	edges, err := q.GetEdgesModifiedBetween(ctx, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should find 2: one created, one expired during range
	if len(edges) != 2 {
		t.Errorf("expected 2 modified edges, got %d", len(edges))
	}
}

func TestBetweenQuerier_GetEdgesCreatedBetweenForNode(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Edge from node1 created during range
	vf1 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, nil)

	// Edge from node2 created during range (should not be included)
	vf2 := now.Add(-20 * time.Minute)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, nil)

	// Edge from node1 created before range (should not be included)
	vf3 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeImports, &vf3, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	start := now.Add(-1 * time.Hour)
	end := now

	edges, err := q.GetEdgesCreatedBetweenForNode(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge created in range for node1, got %d", len(edges))
	}
	if len(edges) > 0 && edges[0].TargetID != "node2" {
		t.Errorf("expected target 'node2', got '%s'", edges[0].TargetID)
	}
}

func TestBetweenQuerier_GetEdgesExpiredBetweenForNode(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Edge from node1 expired during range
	vf1 := now.Add(-2 * time.Hour)
	vt1 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, &vt1)

	// Edge from node2 expired during range (should not be included)
	vf2 := now.Add(-2 * time.Hour)
	vt2 := now.Add(-20 * time.Minute)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, &vt2)

	// Edge from node1 still valid (should not be included)
	vf3 := now.Add(-1 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeImports, &vf3, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	start := now.Add(-1 * time.Hour)
	end := now

	edges, err := q.GetEdgesExpiredBetweenForNode(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge expired in range for node1, got %d", len(edges))
	}
	if len(edges) > 0 && edges[0].TargetID != "node2" {
		t.Errorf("expected target 'node2', got '%s'", edges[0].TargetID)
	}
}

func TestBetweenQuerier_EdgeWithNullValidFrom(t *testing.T) {
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

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	now := time.Now()
	start := now.Add(-1 * time.Hour)
	end := now

	// Edge with NULL valid_from should be included in any range query
	edges, err := q.GetEdgesBetween(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge with NULL valid_from, got %d", len(edges))
	}
}

func TestBetweenQuerier_ComplexScenario(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")
	insertTestNode(t, db, "node4", "Test Node 4")

	now := time.Now()

	// Scenario: Graph evolution over time
	// T-3h: edge1 (node1->node2) created
	// T-2h: edge2 (node1->node3) created
	// T-1.5h: edge1 expired
	// T-1h: edge3 (node1->node4) created
	// T-30m: edge2 expired
	// T-0: edge3 still valid

	vf1 := now.Add(-3 * time.Hour)
	vt1 := now.Add(-90 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, &vt1)

	vf2 := now.Add(-2 * time.Hour)
	vt2 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, &vt2)

	vf3 := now.Add(-1 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node4", vectorgraphdb.EdgeTypeImports, &vf3, nil)

	q := NewBetweenQuerier(db)
	ctx := context.Background()

	// Query 1: What edges were valid during T-2.5h to T-1.25h?
	// Should include: edge1 (overlaps), edge2 (overlaps)
	start1 := now.Add(-150 * time.Minute)
	end1 := now.Add(-75 * time.Minute)
	edges, err := q.GetEdgesBetween(ctx, "node1", start1, end1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("Query 1: expected 2 edges, got %d", len(edges))
	}

	// Query 2: What edges were created during T-1.5h to T-45m?
	// Should include: edge3
	start2 := now.Add(-90 * time.Minute)
	end2 := now.Add(-45 * time.Minute)
	edges, err = q.GetEdgesCreatedBetweenForNode(ctx, "node1", start2, end2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("Query 2: expected 1 edge created, got %d", len(edges))
	}

	// Query 3: What edges expired during T-2h to T-1h?
	// Should include: edge1
	start3 := now.Add(-2 * time.Hour)
	end3 := now.Add(-1 * time.Hour)
	edges, err = q.GetEdgesExpiredBetweenForNode(ctx, "node1", start3, end3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("Query 3: expected 1 edge expired, got %d", len(edges))
	}
	if len(edges) > 0 && edges[0].TargetID != "node2" {
		t.Errorf("Query 3: expected target 'node2', got '%s'", edges[0].TargetID)
	}

	// Query 4: What edges are valid now (T-10m to T)?
	// Should include: edge3
	start4 := now.Add(-10 * time.Minute)
	end4 := now
	edges, err = q.GetEdgesBetween(ctx, "node1", start4, end4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("Query 4: expected 1 current edge, got %d", len(edges))
	}
	if len(edges) > 0 && edges[0].TargetID != "node4" {
		t.Errorf("Query 4: expected target 'node4', got '%s'", edges[0].TargetID)
	}
}
