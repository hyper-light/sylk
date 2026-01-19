package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// TemporalDB Tests (TG.4.1)
// =============================================================================

func TestNewTemporalDB(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	if tdb == nil {
		t.Fatal("NewTemporalDB returned nil")
	}
	if tdb.db != db {
		t.Error("NewTemporalDB did not set db correctly")
	}
	if tdb.asOfQuerier == nil {
		t.Error("NewTemporalDB did not initialize asOfQuerier")
	}
	if tdb.betweenQuerier == nil {
		t.Error("NewTemporalDB did not initialize betweenQuerier")
	}
	if tdb.historyQuerier == nil {
		t.Error("NewTemporalDB did not initialize historyQuerier")
	}
	if tdb.differ == nil {
		t.Error("NewTemporalDB did not initialize differ")
	}
	if tdb.writer == nil {
		t.Error("NewTemporalDB did not initialize writer")
	}
}

func TestNewTemporalDB_NilDB(t *testing.T) {
	tdb := NewTemporalDB(nil)
	if tdb != nil {
		t.Error("NewTemporalDB should return nil for nil db")
	}
}

func TestTemporalDB_DB(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	if tdb.DB() != db {
		t.Error("DB() should return the underlying database")
	}
}

// =============================================================================
// Component Access Tests
// =============================================================================

func TestTemporalDB_ComponentAccess(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)

	if tdb.AsOfQuerier() == nil {
		t.Error("AsOfQuerier() returned nil")
	}
	if tdb.BetweenQuerier() == nil {
		t.Error("BetweenQuerier() returned nil")
	}
	if tdb.HistoryQuerier() == nil {
		t.Error("HistoryQuerier() returned nil")
	}
	if tdb.Differ() == nil {
		t.Error("Differ() returned nil")
	}
	if tdb.Writer() == nil {
		t.Error("Writer() returned nil")
	}
}

// =============================================================================
// Create/Update/Delete Tests
// =============================================================================

func TestTemporalDB_CreateTemporalEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	// Create test nodes first
	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.5,
			Metadata: map[string]any{"test": "value"},
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	if edge.ID == 0 {
		t.Error("edge ID should be set after create")
	}
}

func TestTemporalDB_UpdateTemporalEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Update the edge
	err = tdb.UpdateTemporalEdge(ctx, edge.ID, map[string]any{"weight": 2.5}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	// Get the latest edge
	latest, err := tdb.GetLatestTemporalEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetLatestTemporalEdgeBetween failed: %v", err)
	}

	if latest.Weight != 2.5 {
		t.Errorf("expected weight 2.5, got %.2f", latest.Weight)
	}
}

func TestTemporalDB_DeleteTemporalEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Soft delete the edge
	deletedAt := now
	err = tdb.DeleteTemporalEdge(ctx, edge.ID, deletedAt)
	if err != nil {
		t.Fatalf("DeleteTemporalEdge failed: %v", err)
	}

	// Get the edge and verify it's soft deleted
	current, err := tdb.GetCurrentTemporalEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetCurrentTemporalEdge failed: %v", err)
	}

	if current.ValidTo == nil {
		t.Error("ValidTo should be set after soft delete")
	}
}

func TestTemporalDB_RestoreTemporalEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Delete and restore
	err = tdb.DeleteTemporalEdge(ctx, edge.ID, now)
	if err != nil {
		t.Fatalf("DeleteTemporalEdge failed: %v", err)
	}

	err = tdb.RestoreTemporalEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("RestoreTemporalEdge failed: %v", err)
	}

	// Verify restored
	current, err := tdb.GetCurrentTemporalEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetCurrentTemporalEdge failed: %v", err)
	}

	if current.ValidTo != nil {
		t.Error("ValidTo should be nil after restore")
	}
}

func TestTemporalDB_HardDeleteTemporalEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	edgeID := edge.ID

	// Hard delete
	err = tdb.HardDeleteTemporalEdge(ctx, edgeID)
	if err != nil {
		t.Fatalf("HardDeleteTemporalEdge failed: %v", err)
	}

	// Verify it's gone
	_, err = tdb.GetCurrentTemporalEdge(ctx, edgeID)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

// =============================================================================
// AsOf Query Tests
// =============================================================================

func TestTemporalDB_GetEdgesAsOf(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-2 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Query as of 1 hour ago (edge should be valid)
	asOf := now.Add(-1 * time.Hour)
	edges, err := tdb.GetEdgesAsOf(ctx, "node1", asOf)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}

	if len(edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(edges))
	}

	// Query as of 3 hours ago (edge should not exist yet)
	asOfBefore := now.Add(-3 * time.Hour)
	edgesBefore, err := tdb.GetEdgesAsOf(ctx, "node1", asOfBefore)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}

	if len(edgesBefore) != 0 {
		t.Errorf("expected 0 edges before valid_from, got %d", len(edgesBefore))
	}
}

func TestTemporalDB_GetEdgesAsOfWithTarget(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	// Create two edges from node1
	edge1 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}
	edge2 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node3",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   2.0,
		},
	}

	_ = tdb.CreateTemporalEdge(ctx, edge1, &validFrom, nil)
	_ = tdb.CreateTemporalEdge(ctx, edge2, &validFrom, nil)

	// Query specific target
	edges, err := tdb.GetEdgesAsOfWithTarget(ctx, "node1", "node2", now)
	if err != nil {
		t.Fatalf("GetEdgesAsOfWithTarget failed: %v", err)
	}

	if len(edges) != 1 {
		t.Errorf("expected 1 edge to node2, got %d", len(edges))
	}
}

func TestTemporalDB_GetIncomingEdgesAsOf(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Query incoming edges to node2
	edges, err := tdb.GetIncomingEdgesAsOf(ctx, "node2", now)
	if err != nil {
		t.Fatalf("GetIncomingEdgesAsOf failed: %v", err)
	}

	if len(edges) != 1 {
		t.Errorf("expected 1 incoming edge, got %d", len(edges))
	}
}

func TestTemporalDB_GetEdgesAsOfByType(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	// Create edges of different types
	edge1 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}
	edge2 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node3",
			EdgeType: vectorgraphdb.EdgeTypeDefines,
			Weight:   2.0,
		},
	}

	_ = tdb.CreateTemporalEdge(ctx, edge1, &validFrom, nil)
	_ = tdb.CreateTemporalEdge(ctx, edge2, &validFrom, nil)

	// Query by type
	edges, err := tdb.GetEdgesAsOfByType(ctx, "node1", vectorgraphdb.EdgeTypeCalls, now)
	if err != nil {
		t.Fatalf("GetEdgesAsOfByType failed: %v", err)
	}

	if len(edges) != 1 {
		t.Errorf("expected 1 edge of type Calls, got %d", len(edges))
	}
}

// =============================================================================
// Between Query Tests
// =============================================================================

func TestTemporalDB_GetEdgesBetween(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-2 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Query range that includes the edge
	start := now.Add(-3 * time.Hour)
	end := now
	edges, err := tdb.GetEdgesBetween(ctx, "node1", start, end)
	if err != nil {
		t.Fatalf("GetEdgesBetween failed: %v", err)
	}

	if len(edges) != 1 {
		t.Errorf("expected 1 edge in range, got %d", len(edges))
	}
}

func TestTemporalDB_GetEdgesCreatedBetween(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Query for edges created in the last 2 hours
	start := now.Add(-2 * time.Hour)
	end := now
	edges, err := tdb.GetEdgesCreatedBetween(ctx, start, end)
	if err != nil {
		t.Fatalf("GetEdgesCreatedBetween failed: %v", err)
	}

	if len(edges) != 1 {
		t.Errorf("expected 1 edge created in range, got %d", len(edges))
	}
}

func TestTemporalDB_GetEdgesExpiredBetween(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-3 * time.Hour)
	validTo := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, &validTo)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Query for edges expired in the last 2 hours
	start := now.Add(-2 * time.Hour)
	end := now
	edges, err := tdb.GetEdgesExpiredBetween(ctx, start, end)
	if err != nil {
		t.Fatalf("GetEdgesExpiredBetween failed: %v", err)
	}

	if len(edges) != 1 {
		t.Errorf("expected 1 expired edge, got %d", len(edges))
	}
}

// =============================================================================
// History Query Tests
// =============================================================================

func TestTemporalDB_GetEdgeHistory(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	history, err := tdb.GetEdgeHistory(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetEdgeHistory failed: %v", err)
	}

	if len(history) != 1 {
		t.Errorf("expected 1 version in history, got %d", len(history))
	}
}

func TestTemporalDB_GetEdgeVersionCount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	count, err := tdb.GetEdgeVersionCount(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetEdgeVersionCount failed: %v", err)
	}

	if count != 1 {
		t.Errorf("expected version count 1, got %d", count)
	}
}

// =============================================================================
// Diff Tests
// =============================================================================

func TestTemporalDB_DiffGraph(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-30 * time.Minute)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Diff from 1 hour ago to now
	t1 := now.Add(-1 * time.Hour)
	t2 := now
	diff, err := tdb.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("DiffGraph failed: %v", err)
	}

	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge in diff, got %d", len(diff.Added))
	}
}

func TestTemporalDB_DiffNode(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	validFrom := now.Add(-30 * time.Minute)

	// Create edges from different nodes
	edge1 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}
	edge2 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node3",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   2.0,
		},
	}

	_ = tdb.CreateTemporalEdge(ctx, edge1, &validFrom, nil)
	_ = tdb.CreateTemporalEdge(ctx, edge2, &validFrom, nil)

	// Diff only node1's changes
	t1 := now.Add(-1 * time.Hour)
	t2 := now
	diff, err := tdb.DiffNode(ctx, "node1", t1, t2)
	if err != nil {
		t.Fatalf("DiffNode failed: %v", err)
	}

	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge for node1, got %d", len(diff.Added))
	}
}

func TestTemporalDB_DiffBetweenNodes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	validFrom := now.Add(-30 * time.Minute)

	// Create edges to different targets
	edge1 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}
	edge2 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node3",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   2.0,
		},
	}

	_ = tdb.CreateTemporalEdge(ctx, edge1, &validFrom, nil)
	_ = tdb.CreateTemporalEdge(ctx, edge2, &validFrom, nil)

	// Diff only between node1 and node2
	t1 := now.Add(-1 * time.Hour)
	t2 := now
	diff, err := tdb.DiffBetweenNodes(ctx, "node1", "node2", t1, t2)
	if err != nil {
		t.Fatalf("DiffBetweenNodes failed: %v", err)
	}

	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge between node1 and node2, got %d", len(diff.Added))
	}
}

// =============================================================================
// Supersede Test
// =============================================================================

func TestTemporalDB_SupersedeTemporalEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	// Create initial edge
	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	originalID := edge.ID

	// Supersede with new edge
	newEdge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node3",
			EdgeType: vectorgraphdb.EdgeTypeDefines,
			Weight:   2.5,
		},
	}

	err = tdb.SupersedeTemporalEdge(ctx, originalID, newEdge)
	if err != nil {
		t.Fatalf("SupersedeTemporalEdge failed: %v", err)
	}

	// Verify old edge is superseded
	_, err = tdb.GetCurrentTemporalEdge(ctx, originalID)
	if err != ErrEdgeNotFound {
		t.Errorf("original edge should not be current, got err: %v", err)
	}

	// Verify new edge exists
	if newEdge.ID == 0 {
		t.Error("new edge should have ID set")
	}
}

// =============================================================================
// SetValidTimeRange Test
// =============================================================================

func TestTemporalDB_SetTemporalValidTimeRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()
	validFrom := now.Add(-2 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateTemporalEdge failed: %v", err)
	}

	// Set new time range
	newFrom := now.Add(-1 * time.Hour)
	newTo := now
	err = tdb.SetTemporalValidTimeRange(ctx, edge.ID, &newFrom, &newTo)
	if err != nil {
		t.Fatalf("SetTemporalValidTimeRange failed: %v", err)
	}

	// Verify
	current, err := tdb.GetCurrentTemporalEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetCurrentTemporalEdge failed: %v", err)
	}

	if current.ValidFrom == nil || current.ValidFrom.Unix() != newFrom.Unix() {
		t.Error("ValidFrom not updated correctly")
	}
	if current.ValidTo == nil || current.ValidTo.Unix() != newTo.Unix() {
		t.Error("ValidTo not updated correctly")
	}
}

// =============================================================================
// Integration Scenario Tests
// =============================================================================

func TestTemporalDB_CompleteWorkflow(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tdb := NewTemporalDB(db)
	ctx := context.Background()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// 1. Create an edge
	validFrom := now.Add(-3 * time.Hour)
	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}
	err := tdb.CreateTemporalEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("step 1 - CreateTemporalEdge failed: %v", err)
	}
	originalID := edge.ID

	// 2. Query at different points in time
	edges1h, _ := tdb.GetEdgesAsOf(ctx, "node1", now.Add(-1*time.Hour))
	edges4h, _ := tdb.GetEdgesAsOf(ctx, "node1", now.Add(-4*time.Hour))
	if len(edges1h) != 1 || len(edges4h) != 0 {
		t.Errorf("step 2 - as-of queries incorrect: 1h=%d, 4h=%d", len(edges1h), len(edges4h))
	}

	// 3. Update the edge
	time.Sleep(10 * time.Millisecond)
	err = tdb.UpdateTemporalEdge(ctx, originalID, map[string]any{"weight": 2.0}, nil)
	if err != nil {
		t.Fatalf("step 3 - UpdateTemporalEdge failed: %v", err)
	}

	// 4. Verify history has 2 versions now (original is superseded, new version exists)
	// Note: Original edge is superseded (tx_end set), new edge has different ID
	latest, err := tdb.GetLatestTemporalEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("step 4 - GetLatestTemporalEdgeBetween failed: %v", err)
	}
	if latest.Weight != 2.0 {
		t.Errorf("step 4 - expected weight 2.0, got %.2f", latest.Weight)
	}

	// 5. Soft delete
	err = tdb.DeleteTemporalEdge(ctx, latest.ID, now)
	if err != nil {
		t.Fatalf("step 5 - DeleteTemporalEdge failed: %v", err)
	}

	// 6. Verify soft deleted edges can be found
	softDeleted, err := tdb.GetSoftDeletedEdges(ctx)
	if err != nil {
		t.Fatalf("step 6 - GetSoftDeletedEdges failed: %v", err)
	}
	if len(softDeleted) != 1 {
		t.Errorf("step 6 - expected 1 soft deleted edge, got %d", len(softDeleted))
	}

	// 7. Restore
	err = tdb.RestoreTemporalEdge(ctx, latest.ID)
	if err != nil {
		t.Fatalf("step 7 - RestoreTemporalEdge failed: %v", err)
	}

	// 8. Verify restored
	restored, err := tdb.GetCurrentTemporalEdge(ctx, latest.ID)
	if err != nil {
		t.Fatalf("step 8 - GetCurrentTemporalEdge failed: %v", err)
	}
	if restored.ValidTo != nil {
		t.Error("step 8 - restored edge should have nil ValidTo")
	}
}
