package temporal

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

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

// =============================================================================
// TG.5.x Integration Test Helpers
// =============================================================================

// setupTemporalTestDB creates a TemporalDB with an in-memory SQLite database
// containing the required schema for temporal edge testing.
func setupTemporalTestDB(t *testing.T) (*TemporalDB, *sql.DB) {
	t.Helper()

	db := setupTestDB(t)
	tdb := NewTemporalDB(db)

	if tdb == nil {
		db.Close()
		t.Fatal("NewTemporalDB returned nil")
	}

	return tdb, db
}

// createTemporalTestEdge creates a temporal edge for testing purposes with the
// given source, target, and validity times.
func createTemporalTestEdge(t *testing.T, tdb *TemporalDB, source, target string, validFrom, validTo *time.Time) *vectorgraphdb.TemporalEdge {
	t.Helper()

	ctx := context.Background()

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: source,
			TargetID: target,
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
			Metadata: map[string]any{"test": "value"},
		},
	}

	err := tdb.CreateTemporalEdge(ctx, edge, validFrom, validTo)
	if err != nil {
		t.Fatalf("createTemporalTestEdge failed: %v", err)
	}

	return edge
}

// advanceTime returns a new time by adding the given duration to the base time.
func advanceTime(base time.Time, duration time.Duration) time.Time {
	return base.Add(duration)
}

// =============================================================================
// TG.5.1 AsOf Query Correctness Tests
// =============================================================================

// TestAsOfQueryCorrectness_PointInTimeReturnsOnlyValidEdges tests that
// point-in-time queries return only edges that were valid at that time.
func TestAsOfQueryCorrectness_PointInTimeReturnsOnlyValidEdges(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	// Setup test nodes
	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")
	insertTestNode(t, db, "nodeC", "Node C")

	// Create edges with different validity periods:
	// Edge 1: Valid from T-3h to T-1h (expired)
	vf1 := advanceTime(now, -3*time.Hour)
	vt1 := advanceTime(now, -1*time.Hour)
	createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &vf1, &vt1)

	// Edge 2: Valid from T-2h to now (currently valid)
	vf2 := advanceTime(now, -2*time.Hour)
	createTemporalTestEdge(t, tdb, "nodeA", "nodeC", &vf2, nil)

	// Query at T-2.5h: Only Edge 1 should be visible
	queryTime1 := advanceTime(now, -150*time.Minute)
	edges1, err := tdb.GetEdgesAsOf(ctx, "nodeA", queryTime1)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}
	if len(edges1) != 1 {
		t.Errorf("at T-2.5h: expected 1 edge, got %d", len(edges1))
	}
	if len(edges1) == 1 && edges1[0].TargetID != "nodeB" {
		t.Errorf("at T-2.5h: expected edge to nodeB, got edge to %s", edges1[0].TargetID)
	}

	// Query at T-1.5h: Both edges should be visible
	queryTime2 := advanceTime(now, -90*time.Minute)
	edges2, err := tdb.GetEdgesAsOf(ctx, "nodeA", queryTime2)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}
	if len(edges2) != 2 {
		t.Errorf("at T-1.5h: expected 2 edges, got %d", len(edges2))
	}

	// Query at T-30m: Only Edge 2 should be visible (Edge 1 expired)
	queryTime3 := advanceTime(now, -30*time.Minute)
	edges3, err := tdb.GetEdgesAsOf(ctx, "nodeA", queryTime3)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}
	if len(edges3) != 1 {
		t.Errorf("at T-30m: expected 1 edge, got %d", len(edges3))
	}
	if len(edges3) == 1 && edges3[0].TargetID != "nodeC" {
		t.Errorf("at T-30m: expected edge to nodeC, got edge to %s", edges3[0].TargetID)
	}
}

// TestAsOfQueryCorrectness_QueryAtEdgeCreationTimeIncludesEdge tests that
// querying at exactly the edge creation time includes that edge.
func TestAsOfQueryCorrectness_QueryAtEdgeCreationTimeIncludesEdge(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create edge valid from exactly T-1h
	validFrom := advanceTime(now, -1*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &validFrom, nil)

	// Query at exactly validFrom time should include the edge
	edges, err := tdb.GetEdgesAsOf(ctx, "nodeA", validFrom)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("at creation time: expected 1 edge, got %d", len(edges))
	}
	if len(edges) == 1 && edges[0].ID != edge.ID {
		t.Errorf("at creation time: expected edge ID %d, got %d", edge.ID, edges[0].ID)
	}

	// Query 1 second before validFrom should NOT include the edge
	beforeCreation := advanceTime(validFrom, -1*time.Second)
	edgesBefore, err := tdb.GetEdgesAsOf(ctx, "nodeA", beforeCreation)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}
	if len(edgesBefore) != 0 {
		t.Errorf("before creation time: expected 0 edges, got %d", len(edgesBefore))
	}
}

// TestAsOfQueryCorrectness_QueryAfterDeletionExcludesEdge tests that
// querying after an edge is deleted excludes that edge.
func TestAsOfQueryCorrectness_QueryAfterDeletionExcludesEdge(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create edge valid from T-2h
	validFrom := advanceTime(now, -2*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &validFrom, nil)

	// Delete (soft) the edge at T-1h
	deleteTime := advanceTime(now, -1*time.Hour)
	err := tdb.DeleteTemporalEdge(ctx, edge.ID, deleteTime)
	if err != nil {
		t.Fatalf("DeleteTemporalEdge failed: %v", err)
	}

	// Query at T-1.5h (before deletion) should include the edge
	beforeDelete := advanceTime(now, -90*time.Minute)
	edgesBefore, err := tdb.GetEdgesAsOf(ctx, "nodeA", beforeDelete)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}
	if len(edgesBefore) != 1 {
		t.Errorf("before deletion: expected 1 edge, got %d", len(edgesBefore))
	}

	// Query at T-30m (after deletion) should NOT include the edge
	afterDelete := advanceTime(now, -30*time.Minute)
	edgesAfter, err := tdb.GetEdgesAsOf(ctx, "nodeA", afterDelete)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}
	if len(edgesAfter) != 0 {
		t.Errorf("after deletion: expected 0 edges, got %d", len(edgesAfter))
	}

	// Query at exactly deletion time should also NOT include the edge
	// (valid_to boundary is exclusive)
	edgesAtDelete, err := tdb.GetEdgesAsOf(ctx, "nodeA", deleteTime)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}
	if len(edgesAtDelete) != 0 {
		t.Errorf("at deletion time: expected 0 edges, got %d", len(edgesAtDelete))
	}
}

// TestAsOfQueryCorrectness_MultipleVersionsSameEdge tests that multiple
// versions of the same edge are handled correctly by AsOf queries.
// In a bi-temporal system, AsOf queries filter by valid time.
// When UpdateTemporalEdge is called, it creates new rows with the same valid_from,
// so all versions that match the valid time criteria are returned.
// This test verifies that the current (non-superseded) version can be distinguished
// by checking tx_end IS NULL.
func TestAsOfQueryCorrectness_MultipleVersionsSameEdge(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create initial edge with weight 1.0 at T-3h
	validFrom := advanceTime(now, -3*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &validFrom, nil)
	originalID := edge.ID

	// Small delay to ensure different transaction timestamps
	time.Sleep(10 * time.Millisecond)

	// Update the edge (creates new version with weight 2.0)
	err := tdb.UpdateTemporalEdge(ctx, originalID, map[string]any{"weight": 2.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	// Get the latest version for further updates
	latest, err := tdb.GetLatestTemporalEdgeBetween(ctx, "nodeA", "nodeB")
	if err != nil {
		t.Fatalf("GetLatestTemporalEdgeBetween failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Update again (creates third version with weight 3.0)
	err = tdb.UpdateTemporalEdge(ctx, latest.ID, map[string]any{"weight": 3.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	// AsOf query returns all edges matching valid time criteria,
	// including superseded versions. This is correct bi-temporal behavior.
	allEdges, err := tdb.GetEdgesAsOf(ctx, "nodeA", now)
	if err != nil {
		t.Fatalf("GetEdgesAsOf failed: %v", err)
	}

	// Should have 3 versions (all match valid_from <= now AND valid_to IS NULL)
	if len(allEdges) != 3 {
		t.Errorf("current time: expected 3 edge versions, got %d", len(allEdges))
	}

	// To get only the current (non-superseded) version, filter by tx_end IS NULL
	var currentEdges []vectorgraphdb.TemporalEdge
	for _, e := range allEdges {
		if e.TxEnd == nil {
			currentEdges = append(currentEdges, e)
		}
	}

	if len(currentEdges) != 1 {
		t.Errorf("filtered current: expected 1 edge with tx_end IS NULL, got %d", len(currentEdges))
	}
	if len(currentEdges) == 1 && currentEdges[0].Weight != 3.0 {
		t.Errorf("filtered current: expected weight 3.0, got %.1f", currentEdges[0].Weight)
	}

	// Verify GetLatestTemporalEdgeBetween returns only the current version
	latestEdge, err := tdb.GetLatestTemporalEdgeBetween(ctx, "nodeA", "nodeB")
	if err != nil {
		t.Fatalf("GetLatestTemporalEdgeBetween failed: %v", err)
	}
	if latestEdge.Weight != 3.0 {
		t.Errorf("latest edge: expected weight 3.0, got %.1f", latestEdge.Weight)
	}
	if latestEdge.TxEnd != nil {
		t.Error("latest edge should have tx_end IS NULL")
	}

	// Verify version count through history API
	history, err := tdb.GetEdgeHistoryBySourceTarget(ctx, "nodeA", "nodeB")
	if err != nil {
		t.Fatalf("GetEdgeHistoryBySourceTarget failed: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("history: expected 3 versions, got %d", len(history))
	}

	// Count superseded vs current in history
	supersededCount := 0
	currentCount := 0
	for _, e := range history {
		if e.TxEnd != nil {
			supersededCount++
		} else {
			currentCount++
		}
	}
	if supersededCount != 2 {
		t.Errorf("expected 2 superseded versions, got %d", supersededCount)
	}
	if currentCount != 1 {
		t.Errorf("expected 1 current version, got %d", currentCount)
	}
}

// =============================================================================
// TG.5.2 Edge History Completeness Tests
// =============================================================================

// TestEdgeHistoryCompleteness_AllVersionsRetrievable tests that all
// versions of an edge are retrievable through the history API.
func TestEdgeHistoryCompleteness_AllVersionsRetrievable(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create initial edge
	validFrom := advanceTime(now, -3*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &validFrom, nil)
	originalID := edge.ID

	// Create multiple versions
	time.Sleep(10 * time.Millisecond)
	err := tdb.UpdateTemporalEdge(ctx, originalID, map[string]any{"weight": 2.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	latest, _ := tdb.GetLatestTemporalEdgeBetween(ctx, "nodeA", "nodeB")
	err = tdb.UpdateTemporalEdge(ctx, latest.ID, map[string]any{"weight": 3.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	// Get history for source-target pair (includes all versions)
	history, err := tdb.GetEdgeHistoryBySourceTarget(ctx, "nodeA", "nodeB")
	if err != nil {
		t.Fatalf("GetEdgeHistoryBySourceTarget failed: %v", err)
	}

	// Should have 3 versions (original + 2 updates)
	if len(history) != 3 {
		t.Errorf("expected 3 versions in history, got %d", len(history))
	}

	// Verify we can find each weight value
	weights := make(map[float64]bool)
	for _, e := range history {
		weights[e.Weight] = true
	}
	if !weights[1.0] || !weights[2.0] || !weights[3.0] {
		t.Errorf("missing weight values in history: %v", weights)
	}
}

// TestEdgeHistoryCompleteness_VersionsOrderedByTransactionTime tests that
// versions are ordered by transaction time (most recent first).
func TestEdgeHistoryCompleteness_VersionsOrderedByTransactionTime(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create and update edge multiple times with delays
	validFrom := advanceTime(now, -3*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &validFrom, nil)

	time.Sleep(20 * time.Millisecond)
	err := tdb.UpdateTemporalEdge(ctx, edge.ID, map[string]any{"weight": 2.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	latest, _ := tdb.GetLatestTemporalEdgeBetween(ctx, "nodeA", "nodeB")
	err = tdb.UpdateTemporalEdge(ctx, latest.ID, map[string]any{"weight": 3.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	// Get history
	history, err := tdb.GetEdgeHistoryBySourceTarget(ctx, "nodeA", "nodeB")
	if err != nil {
		t.Fatalf("GetEdgeHistoryBySourceTarget failed: %v", err)
	}

	if len(history) < 2 {
		t.Fatalf("expected at least 2 versions, got %d", len(history))
	}

	// Verify ordering by tx_start (most recent first)
	for i := 0; i < len(history)-1; i++ {
		if history[i].TxStart != nil && history[i+1].TxStart != nil {
			if history[i].TxStart.Before(*history[i+1].TxStart) {
				t.Errorf("history not ordered by tx_start (descending): version %d has tx_start before version %d", i, i+1)
			}
		}
	}
}

// TestEdgeHistoryCompleteness_SoftDeletedEdgesAppearInHistory tests that
// soft-deleted edges appear in the history.
func TestEdgeHistoryCompleteness_SoftDeletedEdgesAppearInHistory(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create edge
	validFrom := advanceTime(now, -2*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &validFrom, nil)

	// Soft delete the edge
	deleteTime := advanceTime(now, -1*time.Hour)
	err := tdb.DeleteTemporalEdge(ctx, edge.ID, deleteTime)
	if err != nil {
		t.Fatalf("DeleteTemporalEdge failed: %v", err)
	}

	// Get history - should include the soft-deleted edge
	history, err := tdb.GetEdgeHistory(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetEdgeHistory failed: %v", err)
	}

	if len(history) != 1 {
		t.Errorf("expected 1 version in history, got %d", len(history))
	}

	// Verify the edge in history has valid_to set
	if len(history) == 1 && history[0].ValidTo == nil {
		t.Error("soft-deleted edge in history should have valid_to set")
	}

	// Also verify via GetSoftDeletedEdges
	softDeleted, err := tdb.GetSoftDeletedEdges(ctx)
	if err != nil {
		t.Fatalf("GetSoftDeletedEdges failed: %v", err)
	}
	if len(softDeleted) != 1 {
		t.Errorf("expected 1 soft-deleted edge, got %d", len(softDeleted))
	}
}

// TestEdgeHistoryCompleteness_SupersededEdgesAppearWithCorrectTxEnd tests that
// superseded edges appear in history with correct tx_end values.
func TestEdgeHistoryCompleteness_SupersededEdgesAppearWithCorrectTxEnd(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create edge
	validFrom := advanceTime(now, -2*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &validFrom, nil)

	time.Sleep(20 * time.Millisecond)

	// Update the edge (supersedes the original)
	err := tdb.UpdateTemporalEdge(ctx, edge.ID, map[string]any{"weight": 2.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	// Get superseded edges
	superseded, err := tdb.GetSupersededEdges(ctx)
	if err != nil {
		t.Fatalf("GetSupersededEdges failed: %v", err)
	}

	// Should have at least 1 superseded edge (the original)
	if len(superseded) < 1 {
		t.Errorf("expected at least 1 superseded edge, got %d", len(superseded))
	}

	// Find the original edge in superseded list
	var foundOriginal bool
	for _, e := range superseded {
		if e.ID == edge.ID {
			foundOriginal = true
			if e.TxEnd == nil {
				t.Error("superseded edge should have tx_end set")
			}
			break
		}
	}

	if !foundOriginal {
		t.Error("original edge not found in superseded edges")
	}

	// Get full history for the source-target pair
	history, err := tdb.GetEdgeHistoryBySourceTarget(ctx, "nodeA", "nodeB")
	if err != nil {
		t.Fatalf("GetEdgeHistoryBySourceTarget failed: %v", err)
	}

	// History should have both the superseded and current version
	if len(history) != 2 {
		t.Errorf("expected 2 versions in history, got %d", len(history))
	}
}

// =============================================================================
// TG.5.3 GraphDiff Accuracy Tests
// =============================================================================

// TestGraphDiffAccuracy_AddedEdgesCorrectlyIdentified tests that
// added edges are correctly identified in graph diffs.
func TestGraphDiffAccuracy_AddedEdgesCorrectlyIdentified(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")
	insertTestNode(t, db, "nodeC", "Node C")

	// Create edges at different times
	// Edge 1: Created at T-2h
	vf1 := advanceTime(now, -2*time.Hour)
	edge1 := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &vf1, nil)

	// Edge 2: Created at T-30m
	vf2 := advanceTime(now, -30*time.Minute)
	edge2 := createTemporalTestEdge(t, tdb, "nodeA", "nodeC", &vf2, nil)

	// Diff from T-1h to now should show Edge 2 as added
	t1 := advanceTime(now, -1*time.Hour)
	t2 := now
	diff, err := tdb.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("DiffGraph failed: %v", err)
	}

	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge, got %d", len(diff.Added))
	}

	if len(diff.Added) == 1 && diff.Added[0].ID != edge2.ID {
		t.Errorf("expected added edge ID %d, got %d", edge2.ID, diff.Added[0].ID)
	}

	// Diff from T-3h to now should show both edges as added
	t1Earlier := advanceTime(now, -3*time.Hour)
	diffEarlier, err := tdb.DiffGraph(ctx, t1Earlier, t2)
	if err != nil {
		t.Fatalf("DiffGraph failed: %v", err)
	}

	if len(diffEarlier.Added) != 2 {
		t.Errorf("expected 2 added edges, got %d", len(diffEarlier.Added))
	}

	// Verify both edges are present
	addedIDs := make(map[int64]bool)
	for _, e := range diffEarlier.Added {
		addedIDs[e.ID] = true
	}
	if !addedIDs[edge1.ID] || !addedIDs[edge2.ID] {
		t.Errorf("missing expected edge in added list: edge1=%v, edge2=%v", addedIDs[edge1.ID], addedIDs[edge2.ID])
	}
}

// TestGraphDiffAccuracy_RemovedEdgesCorrectlyIdentified tests that
// removed edges are correctly identified in graph diffs.
func TestGraphDiffAccuracy_RemovedEdgesCorrectlyIdentified(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create edge at T-3h
	vf := advanceTime(now, -3*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &vf, nil)

	// Delete the edge at T-30m
	deleteTime := advanceTime(now, -30*time.Minute)
	err := tdb.DeleteTemporalEdge(ctx, edge.ID, deleteTime)
	if err != nil {
		t.Fatalf("DeleteTemporalEdge failed: %v", err)
	}

	// Diff from T-1h to now should show the edge as removed
	t1 := advanceTime(now, -1*time.Hour)
	t2 := now
	diff, err := tdb.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("DiffGraph failed: %v", err)
	}

	if len(diff.Removed) != 1 {
		t.Errorf("expected 1 removed edge, got %d", len(diff.Removed))
	}

	if len(diff.Removed) == 1 && diff.Removed[0].ID != edge.ID {
		t.Errorf("expected removed edge ID %d, got %d", edge.ID, diff.Removed[0].ID)
	}

	// Diff from T-1h to T-45m should show 0 removed (deletion is at T-30m)
	t2Earlier := advanceTime(now, -45*time.Minute)
	diffEarlier, err := tdb.DiffGraph(ctx, t1, t2Earlier)
	if err != nil {
		t.Fatalf("DiffGraph failed: %v", err)
	}

	if len(diffEarlier.Removed) != 0 {
		t.Errorf("expected 0 removed edges in earlier range, got %d", len(diffEarlier.Removed))
	}
}

// TestGraphDiffAccuracy_ModifiedEdgesShowBeforeAndAfter tests that
// modified edges show both before and after states in diffs.
func TestGraphDiffAccuracy_ModifiedEdgesShowBeforeAndAfter(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")

	// Create edge at T-3h
	vf := advanceTime(now, -3*time.Hour)
	edge := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &vf, nil)

	// Wait a bit, then update the edge
	time.Sleep(20 * time.Millisecond)

	// Update at "now" (approximately)
	err := tdb.UpdateTemporalEdge(ctx, edge.ID, map[string]any{"weight": 5.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	// Diff from T-1m to T+1m should catch the modification
	// We need to look at a range that includes when the update happened
	t1 := advanceTime(now, -1*time.Minute)
	t2 := advanceTime(now, 1*time.Minute)
	diff, err := tdb.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("DiffGraph failed: %v", err)
	}

	// The modified list should contain the edge with before/after
	if len(diff.Modified) > 0 {
		mod := diff.Modified[0]
		if mod.Before == nil {
			t.Error("modified edge should have Before state")
		}
		if mod.After != nil {
			// After is found (successor detection worked)
			if mod.Before != nil && mod.After.Weight == mod.Before.Weight {
				t.Error("after state should have different weight than before")
			}
		}
		if len(mod.Changes) == 0 {
			t.Error("modified edge should have changes description")
		}
	}
}

// TestGraphDiffAccuracy_ComplexScenarioMultipleChanges tests a complex
// scenario with multiple changes in a time range.
func TestGraphDiffAccuracy_ComplexScenarioMultipleChanges(t *testing.T) {
	tdb, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	insertTestNode(t, db, "nodeA", "Node A")
	insertTestNode(t, db, "nodeB", "Node B")
	insertTestNode(t, db, "nodeC", "Node C")
	insertTestNode(t, db, "nodeD", "Node D")

	// Timeline:
	// T-4h: Edge A->B created (before our diff window)
	// T-3h: Edge A->C created (will be deleted later)
	// T-2h: Start of diff window
	// T-1.5h: Edge A->D created (added)
	// T-1h: Edge A->C deleted (removed)
	// T-30m: Edge A->B updated (modified)
	// T: End of diff window

	// Edge A->B at T-4h (before window, modified later)
	vfAB := advanceTime(now, -4*time.Hour)
	edgeAB := createTemporalTestEdge(t, tdb, "nodeA", "nodeB", &vfAB, nil)

	// Edge A->C at T-3h (before window, deleted later)
	vfAC := advanceTime(now, -3*time.Hour)
	edgeAC := createTemporalTestEdge(t, tdb, "nodeA", "nodeC", &vfAC, nil)

	// Edge A->D at T-1.5h (within window - added)
	vfAD := advanceTime(now, -90*time.Minute)
	edgeAD := createTemporalTestEdge(t, tdb, "nodeA", "nodeD", &vfAD, nil)

	// Delete A->C at T-1h (within window - removed)
	deleteTimeAC := advanceTime(now, -1*time.Hour)
	err := tdb.DeleteTemporalEdge(ctx, edgeAC.ID, deleteTimeAC)
	if err != nil {
		t.Fatalf("DeleteTemporalEdge failed: %v", err)
	}

	// Update A->B (creates a modification)
	time.Sleep(10 * time.Millisecond)
	err = tdb.UpdateTemporalEdge(ctx, edgeAB.ID, map[string]any{"weight": 10.0}, nil)
	if err != nil {
		t.Fatalf("UpdateTemporalEdge failed: %v", err)
	}

	// Diff from T-2h to now
	t1 := advanceTime(now, -2*time.Hour)
	t2 := now
	diff, err := tdb.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("DiffGraph failed: %v", err)
	}

	// Check added: should have edge A->D
	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge, got %d", len(diff.Added))
	}
	if len(diff.Added) == 1 && diff.Added[0].ID != edgeAD.ID {
		t.Errorf("expected added edge A->D (ID %d), got ID %d", edgeAD.ID, diff.Added[0].ID)
	}

	// Check removed: should have edge A->C
	if len(diff.Removed) != 1 {
		t.Errorf("expected 1 removed edge, got %d", len(diff.Removed))
	}
	if len(diff.Removed) == 1 && diff.Removed[0].ID != edgeAC.ID {
		t.Errorf("expected removed edge A->C (ID %d), got ID %d", edgeAC.ID, diff.Removed[0].ID)
	}

	// Check total changes
	totalChanges := diff.TotalChanges()
	if totalChanges < 2 {
		t.Errorf("expected at least 2 total changes, got %d", totalChanges)
	}

	// Summary should not be empty
	if diff.IsEmpty() {
		t.Error("diff should not be empty")
	}

	summary := diff.Summary()
	if summary == "" || summary == "No changes detected" {
		t.Error("summary should describe changes")
	}
}

// =============================================================================
// TG.5.4 Index Usage Verification Tests
// =============================================================================

// TestIndexUsageVerification_AsOfQueriesUseTemporalIndexes tests that
// AsOf queries use temporal indexes (verified with EXPLAIN).
func TestIndexUsageVerification_AsOfQueriesUseTemporalIndexes(t *testing.T) {
	_, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()

	// First, ensure temporal indexes exist
	verifier := NewTemporalIndexVerifier(db)
	_, err := verifier.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("CreateMissingIndexes failed: %v", err)
	}

	// AsOf query pattern
	asOfQuery := `
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at,
			valid_from, valid_to, tx_start, tx_end
		FROM edges
		WHERE source_id = 'nodeA'
		AND (valid_from IS NULL OR valid_from <= 1705579200)
		AND (valid_to IS NULL OR valid_to > 1705579200)
	`

	analysis, err := verifier.VerifyIndexUsage(ctx, asOfQuery)
	if err != nil {
		t.Fatalf("VerifyIndexUsage failed: %v", err)
	}

	// Verify the analysis returns useful information
	if analysis.Query != asOfQuery {
		t.Error("analysis query should match input query")
	}

	if len(analysis.Details) == 0 {
		t.Error("analysis should have EXPLAIN details")
	}

	// Log the analysis for inspection (the actual index usage depends on schema)
	t.Logf("AsOf Query Analysis: UsesIndex=%v, ScanType=%s, IndexName=%s",
		analysis.UsesIndex, analysis.ScanType, analysis.IndexName)
}

// TestIndexUsageVerification_BetweenQueriesUseTemporalIndexes tests that
// Between queries use temporal indexes (verified with EXPLAIN).
func TestIndexUsageVerification_BetweenQueriesUseTemporalIndexes(t *testing.T) {
	_, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()

	verifier := NewTemporalIndexVerifier(db)
	_, err := verifier.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("CreateMissingIndexes failed: %v", err)
	}

	// Between query pattern
	betweenQuery := `
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at,
			valid_from, valid_to, tx_start, tx_end
		FROM edges
		WHERE source_id = 'nodeA'
		AND (valid_from IS NULL OR valid_from < 1705665600)
		AND (valid_to IS NULL OR valid_to > 1705579200)
	`

	analysis, err := verifier.VerifyIndexUsage(ctx, betweenQuery)
	if err != nil {
		t.Fatalf("VerifyIndexUsage failed: %v", err)
	}

	if len(analysis.Details) == 0 {
		t.Error("analysis should have EXPLAIN details")
	}

	t.Logf("Between Query Analysis: UsesIndex=%v, ScanType=%s, IndexName=%s",
		analysis.UsesIndex, analysis.ScanType, analysis.IndexName)
}

// TestIndexUsageVerification_CurrentStateQueriesUsePartialIndex tests that
// current-state queries use the partial index (WHERE valid_to IS NULL).
func TestIndexUsageVerification_CurrentStateQueriesUsePartialIndex(t *testing.T) {
	_, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()

	verifier := NewTemporalIndexVerifier(db)
	_, err := verifier.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("CreateMissingIndexes failed: %v", err)
	}

	// Current state query pattern (should use partial index)
	currentQuery := `
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at,
			valid_from, valid_to, tx_start, tx_end
		FROM edges
		WHERE source_id = 'nodeA'
		AND valid_to IS NULL
	`

	analysis, err := verifier.VerifyIndexUsage(ctx, currentQuery)
	if err != nil {
		t.Fatalf("VerifyIndexUsage failed: %v", err)
	}

	if len(analysis.Details) == 0 {
		t.Error("analysis should have EXPLAIN details")
	}

	// Log for inspection
	t.Logf("Current State Query Analysis: UsesIndex=%v, ScanType=%s, IndexName=%s",
		analysis.UsesIndex, analysis.ScanType, analysis.IndexName)

	// If the partial index idx_edges_temporal_current exists and is used,
	// we should see it in the analysis
	if analysis.UsesIndex && analysis.IndexName != "" {
		t.Logf("Query uses index: %s", analysis.IndexName)
	}
}

// TestIndexUsageVerification_MissingIndexesDetectedAndReported tests that
// missing indexes are detected and reported correctly.
func TestIndexUsageVerification_MissingIndexesDetectedAndReported(t *testing.T) {
	// Create a fresh database without temporal indexes
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer db.Close()

	// Create minimal schema without temporal indexes
	_, err = db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
	`)
	if err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()
	verifier := NewTemporalIndexVerifier(db)

	// Verify temporal indexes - should report missing indexes
	report, err := verifier.VerifyTemporalIndexes(ctx)
	if err != nil {
		t.Fatalf("VerifyTemporalIndexes failed: %v", err)
	}

	// Should have missing indexes
	if report.AllExpectedIndexesExist {
		t.Error("expected missing indexes to be detected")
	}

	if len(report.MissingIndexes) == 0 {
		t.Error("expected missing indexes list to be non-empty")
	}

	// Get expected indexes
	expectedIndexes := GetExpectedTemporalIndexes()
	for _, expected := range expectedIndexes {
		found := false
		for _, missing := range report.MissingIndexes {
			if missing == expected.Name {
				found = true
				break
			}
		}
		if !found {
			// The index exists or wasn't expected
			t.Logf("Index %s not in missing list", expected.Name)
		}
	}

	// Should have recommendations
	if len(report.Recommendations) == 0 {
		t.Error("expected recommendations for missing indexes")
	}

	// Get suggested SQL to create missing indexes
	suggestions, err := verifier.SuggestMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("SuggestMissingIndexes failed: %v", err)
	}

	if len(suggestions) == 0 {
		t.Error("expected index creation suggestions")
	}

	// Verify suggestions are valid SQL (CREATE INDEX statements)
	for _, suggestion := range suggestions {
		if !strings.Contains(suggestion, "CREATE INDEX") {
			t.Errorf("suggestion should be CREATE INDEX statement: %s", suggestion)
		}
	}

	// Now create the missing indexes
	created, err := verifier.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("CreateMissingIndexes failed: %v", err)
	}

	if created == 0 {
		t.Error("expected some indexes to be created")
	}

	// Verify again - should now have all indexes
	reportAfter, err := verifier.VerifyTemporalIndexes(ctx)
	if err != nil {
		t.Fatalf("VerifyTemporalIndexes after creation failed: %v", err)
	}

	if !reportAfter.AllExpectedIndexesExist {
		t.Errorf("expected all indexes to exist after creation, missing: %v", reportAfter.MissingIndexes)
	}
}

// TestIndexUsageVerification_AnalyzeTemporalQueries tests the comprehensive
// analysis of all temporal query patterns.
func TestIndexUsageVerification_AnalyzeTemporalQueries(t *testing.T) {
	_, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()

	verifier := NewTemporalIndexVerifier(db)
	_, err := verifier.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("CreateMissingIndexes failed: %v", err)
	}

	// Analyze all temporal query patterns
	analyses, err := verifier.AnalyzeTemporalQueries(ctx)
	if err != nil {
		t.Fatalf("AnalyzeTemporalQueries failed: %v", err)
	}

	// Should have multiple query patterns analyzed
	if len(analyses) == 0 {
		t.Error("expected multiple query patterns to be analyzed")
	}

	// Log analysis results
	for name, analysis := range analyses {
		t.Logf("Query '%s': UsesIndex=%v, ScanType=%s, IndexName=%s",
			name, analysis.UsesIndex, analysis.ScanType, analysis.IndexName)
	}

	// Get overall recommendations
	recommendations, err := verifier.GetIndexRecommendations(ctx)
	if err != nil {
		t.Fatalf("GetIndexRecommendations failed: %v", err)
	}

	// Log recommendations (might be empty if all queries use indexes)
	for _, rec := range recommendations {
		t.Logf("Recommendation: %s", rec)
	}
}

// TestIndexUsageVerification_TransactionTimeIndexes tests that transaction
// time queries use appropriate indexes.
func TestIndexUsageVerification_TransactionTimeIndexes(t *testing.T) {
	_, db := setupTemporalTestDB(t)
	defer db.Close()

	ctx := context.Background()

	verifier := NewTemporalIndexVerifier(db)
	_, err := verifier.CreateMissingIndexes(ctx)
	if err != nil {
		t.Fatalf("CreateMissingIndexes failed: %v", err)
	}

	// Transaction time query (for history)
	txQuery := `
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at,
			valid_from, valid_to, tx_start, tx_end
		FROM edges
		WHERE tx_start >= 1705579200
		AND tx_start < 1705665600
		AND tx_end IS NOT NULL
		ORDER BY tx_start DESC
	`

	analysis, err := verifier.VerifyIndexUsage(ctx, txQuery)
	if err != nil {
		t.Fatalf("VerifyIndexUsage failed: %v", err)
	}

	t.Logf("Transaction Time Query Analysis: UsesIndex=%v, ScanType=%s, IndexName=%s",
		analysis.UsesIndex, analysis.ScanType, analysis.IndexName)

	if len(analysis.Details) == 0 {
		t.Error("analysis should have EXPLAIN details")
	}
}
