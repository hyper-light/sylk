package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// TemporalEdgeWriter Tests (TG.3.1)
// =============================================================================

func TestNewTemporalEdgeWriter(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	if w == nil {
		t.Fatal("NewTemporalEdgeWriter returned nil")
	}
	if w.db != db {
		t.Error("NewTemporalEdgeWriter did not set db correctly")
	}
}

func TestTemporalEdgeWriter_NilDB(t *testing.T) {
	w := NewTemporalEdgeWriter(nil)
	ctx := context.Background()
	now := time.Now()

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	// Test all methods return ErrNilDB
	if err := w.CreateEdge(ctx, edge, nil, nil); err != ErrNilDB {
		t.Errorf("CreateEdge: expected ErrNilDB, got %v", err)
	}

	if err := w.UpdateEdge(ctx, 1, map[string]any{"weight": 2.0}, nil); err != ErrNilDB {
		t.Errorf("UpdateEdge: expected ErrNilDB, got %v", err)
	}

	if err := w.DeleteEdge(ctx, 1, now); err != ErrNilDB {
		t.Errorf("DeleteEdge: expected ErrNilDB, got %v", err)
	}

	if err := w.RestoreEdge(ctx, 1); err != ErrNilDB {
		t.Errorf("RestoreEdge: expected ErrNilDB, got %v", err)
	}

	if err := w.HardDeleteEdge(ctx, 1); err != ErrNilDB {
		t.Errorf("HardDeleteEdge: expected ErrNilDB, got %v", err)
	}

	if err := w.SupersedeEdge(ctx, 1, edge); err != ErrNilDB {
		t.Errorf("SupersedeEdge: expected ErrNilDB, got %v", err)
	}

	if err := w.SetValidTimeRange(ctx, 1, nil, nil); err != ErrNilDB {
		t.Errorf("SetValidTimeRange: expected ErrNilDB, got %v", err)
	}

	if _, err := w.GetCurrentEdge(ctx, 1); err != ErrNilDB {
		t.Errorf("GetCurrentEdge: expected ErrNilDB, got %v", err)
	}

	if _, err := w.GetLatestEdgeBetween(ctx, "node1", "node2"); err != ErrNilDB {
		t.Errorf("GetLatestEdgeBetween: expected ErrNilDB, got %v", err)
	}
}

// =============================================================================
// CreateEdge Tests
// =============================================================================

func TestTemporalEdgeWriter_CreateEdge_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.5,
			Metadata: map[string]any{"key": "value"},
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Verify the edge was created
	if edge.ID == 0 {
		t.Error("edge ID should be set after create")
	}

	// Verify temporal fields
	if edge.ValidFrom == nil || !edge.ValidFrom.Equal(validFrom) {
		t.Error("ValidFrom not set correctly")
	}
	if edge.ValidTo != nil {
		t.Error("ValidTo should be nil for current edge")
	}
	if edge.TxStart == nil {
		t.Error("TxStart should be set")
	}
	if edge.TxEnd != nil {
		t.Error("TxEnd should be nil for current version")
	}

	// Verify edge can be retrieved
	retrieved, err := w.GetCurrentEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetCurrentEdge failed: %v", err)
	}
	if retrieved.SourceID != "node1" {
		t.Errorf("expected source_id 'node1', got '%s'", retrieved.SourceID)
	}
	if retrieved.Weight != 1.5 {
		t.Errorf("expected weight 1.5, got %.2f", retrieved.Weight)
	}
}

func TestTemporalEdgeWriter_CreateEdge_NilEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	err := w.CreateEdge(ctx, nil, nil, nil)
	if err != ErrNilEdge {
		t.Errorf("expected ErrNilEdge, got %v", err)
	}
}

func TestTemporalEdgeWriter_CreateEdge_InvalidTimeRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now
	validTo := now.Add(-1 * time.Hour) // validTo before validFrom

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, &validTo)
	if err != ErrInvalidValidTimeRange {
		t.Errorf("expected ErrInvalidValidTimeRange, got %v", err)
	}
}

func TestTemporalEdgeWriter_CreateEdge_WithValidTimeRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-2 * time.Hour)
	validTo := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, &validTo)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Verify both valid times are set
	if edge.ValidFrom == nil || !edge.ValidFrom.Equal(validFrom) {
		t.Error("ValidFrom not set correctly")
	}
	if edge.ValidTo == nil || !edge.ValidTo.Equal(validTo) {
		t.Error("ValidTo not set correctly")
	}
}

func TestTemporalEdgeWriter_CreateEdge_EqualValidTimes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now
	validTo := now // Equal times should be allowed

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, &validTo)
	if err != nil {
		t.Errorf("CreateEdge with equal times should succeed, got %v", err)
	}
}

// =============================================================================
// UpdateEdge Tests
// =============================================================================

func TestTemporalEdgeWriter_UpdateEdge_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	originalID := edge.ID

	// Small delay to ensure different timestamps
	time.Sleep(10 * time.Millisecond)

	// Update the edge with new weight
	newData := map[string]any{
		"weight": 2.5,
	}

	err = w.UpdateEdge(ctx, originalID, newData, nil)
	if err != nil {
		t.Fatalf("UpdateEdge failed: %v", err)
	}

	// The original edge should now be superseded (tx_end set)
	oldEdge, err := w.getEdgeByID(ctx, originalID)
	if err != nil {
		t.Fatalf("failed to get old edge: %v", err)
	}
	if oldEdge.TxEnd == nil {
		t.Error("old edge should have tx_end set after update")
	}

	// Get the latest edge between the nodes
	newEdge, err := w.GetLatestEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetLatestEdgeBetween failed: %v", err)
	}

	if newEdge.Weight != 2.5 {
		t.Errorf("expected new weight 2.5, got %.2f", newEdge.Weight)
	}
	if newEdge.TxEnd != nil {
		t.Error("new edge should have nil tx_end")
	}
}

func TestTemporalEdgeWriter_UpdateEdge_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	err := w.UpdateEdge(ctx, 999, map[string]any{"weight": 2.0}, nil)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

func TestTemporalEdgeWriter_UpdateEdge_AlreadySuperseded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	// Create and update edge
	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	originalID := edge.ID

	// First update should succeed
	err = w.UpdateEdge(ctx, originalID, map[string]any{"weight": 2.0}, nil)
	if err != nil {
		t.Fatalf("first UpdateEdge failed: %v", err)
	}

	// Second update on same edge should fail (already superseded)
	err = w.UpdateEdge(ctx, originalID, map[string]any{"weight": 3.0}, nil)
	if err != ErrEdgeSuperseded {
		t.Errorf("expected ErrEdgeSuperseded, got %v", err)
	}
}

func TestTemporalEdgeWriter_UpdateEdge_WithNewValidFrom(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-2 * time.Hour)

	// Create initial edge
	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Update with new valid_from
	newValidFrom := now.Add(-1 * time.Hour)
	err = w.UpdateEdge(ctx, edge.ID, map[string]any{"weight": 2.0}, &newValidFrom)
	if err != nil {
		t.Fatalf("UpdateEdge failed: %v", err)
	}

	// Verify new edge has updated valid_from
	newEdge, err := w.GetLatestEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetLatestEdgeBetween failed: %v", err)
	}

	if newEdge.ValidFrom == nil {
		t.Fatal("ValidFrom should not be nil")
	}

	// Compare Unix timestamps (accounting for potential precision differences)
	if newEdge.ValidFrom.Unix() != newValidFrom.Unix() {
		t.Errorf("expected ValidFrom %v, got %v", newValidFrom.Unix(), newEdge.ValidFrom.Unix())
	}
}

// =============================================================================
// DeleteEdge Tests
// =============================================================================

func TestTemporalEdgeWriter_DeleteEdge_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	deletedAt := now
	err = w.DeleteEdge(ctx, edge.ID, deletedAt)
	if err != nil {
		t.Fatalf("DeleteEdge failed: %v", err)
	}

	// Verify the edge is soft-deleted
	deletedEdge, err := w.getEdgeByID(ctx, edge.ID)
	if err != nil {
		t.Fatalf("failed to get deleted edge: %v", err)
	}

	if deletedEdge.ValidTo == nil {
		t.Error("ValidTo should be set after soft delete")
	}
	if deletedEdge.ValidTo.Unix() != deletedAt.Unix() {
		t.Errorf("ValidTo should match deletedAt time")
	}
}

func TestTemporalEdgeWriter_DeleteEdge_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	err := w.DeleteEdge(ctx, 999, time.Now())
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

func TestTemporalEdgeWriter_DeleteEdge_AlreadyDeleted(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// First delete
	err = w.DeleteEdge(ctx, edge.ID, now)
	if err != nil {
		t.Fatalf("first DeleteEdge failed: %v", err)
	}

	// Second delete should fail
	err = w.DeleteEdge(ctx, edge.ID, now.Add(time.Hour))
	if err != ErrEdgeAlreadyDeleted {
		t.Errorf("expected ErrEdgeAlreadyDeleted, got %v", err)
	}
}

func TestTemporalEdgeWriter_DeleteEdge_InvalidTimeRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Try to delete with time before valid_from
	deletedAt := now.Add(-1 * time.Hour)
	err = w.DeleteEdge(ctx, edge.ID, deletedAt)
	if err != ErrInvalidValidTimeRange {
		t.Errorf("expected ErrInvalidValidTimeRange, got %v", err)
	}
}

// =============================================================================
// RestoreEdge Tests
// =============================================================================

func TestTemporalEdgeWriter_RestoreEdge_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Delete the edge
	err = w.DeleteEdge(ctx, edge.ID, now)
	if err != nil {
		t.Fatalf("DeleteEdge failed: %v", err)
	}

	// Restore the edge
	err = w.RestoreEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("RestoreEdge failed: %v", err)
	}

	// Verify the edge is restored
	restoredEdge, err := w.GetCurrentEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetCurrentEdge failed: %v", err)
	}

	if restoredEdge.ValidTo != nil {
		t.Error("ValidTo should be nil after restore")
	}
}

func TestTemporalEdgeWriter_RestoreEdge_NotDeleted(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Try to restore an edge that isn't deleted
	err = w.RestoreEdge(ctx, edge.ID)
	if err != ErrEdgeNotDeleted {
		t.Errorf("expected ErrEdgeNotDeleted, got %v", err)
	}
}

func TestTemporalEdgeWriter_RestoreEdge_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	err := w.RestoreEdge(ctx, 999)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

// =============================================================================
// HardDeleteEdge Tests
// =============================================================================

func TestTemporalEdgeWriter_HardDeleteEdge_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	err = w.HardDeleteEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("HardDeleteEdge failed: %v", err)
	}

	// Verify the edge is physically removed
	_, err = w.getEdgeByID(ctx, edge.ID)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound after hard delete, got %v", err)
	}
}

func TestTemporalEdgeWriter_HardDeleteEdge_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	err := w.HardDeleteEdge(ctx, 999)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

// =============================================================================
// SupersedeEdge Tests
// =============================================================================

func TestTemporalEdgeWriter_SupersedeEdge_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	originalID := edge.ID

	// Supersede with new edge (different target)
	newValidFrom := now
	newEdge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node3", // Different target
			EdgeType: vectorgraphdb.EdgeTypeDefines,
			Weight:   2.5,
			Metadata: map[string]any{"reason": "refactored"},
		},
		ValidFrom: &newValidFrom,
	}

	err = w.SupersedeEdge(ctx, originalID, newEdge)
	if err != nil {
		t.Fatalf("SupersedeEdge failed: %v", err)
	}

	// Verify old edge is superseded
	oldEdge, err := w.getEdgeByID(ctx, originalID)
	if err != nil {
		t.Fatalf("failed to get old edge: %v", err)
	}
	if oldEdge.TxEnd == nil {
		t.Error("old edge should have tx_end set")
	}

	// Verify new edge has ID and tx_start
	if newEdge.ID == 0 {
		t.Error("new edge should have ID set")
	}
	if newEdge.TxStart == nil {
		t.Error("new edge should have TxStart set")
	}
	if newEdge.TxEnd != nil {
		t.Error("new edge should have nil TxEnd")
	}
	if newEdge.ValidFrom == nil || newEdge.ValidFrom.Unix() != newValidFrom.Unix() {
		t.Error("new edge should preserve ValidFrom")
	}
}

func TestTemporalEdgeWriter_SupersedeEdge_NilNewEdge(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	err = w.SupersedeEdge(ctx, edge.ID, nil)
	if err != ErrNilEdge {
		t.Errorf("expected ErrNilEdge, got %v", err)
	}
}

func TestTemporalEdgeWriter_SupersedeEdge_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	newEdge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.SupersedeEdge(ctx, 999, newEdge)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

func TestTemporalEdgeWriter_SupersedeEdge_AlreadySuperseded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	originalID := edge.ID

	// First supersede
	newEdge1 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   2.0,
		},
	}
	err = w.SupersedeEdge(ctx, originalID, newEdge1)
	if err != nil {
		t.Fatalf("first SupersedeEdge failed: %v", err)
	}

	// Second supersede on same edge should fail
	newEdge2 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   3.0,
		},
	}
	err = w.SupersedeEdge(ctx, originalID, newEdge2)
	if err != ErrEdgeSuperseded {
		t.Errorf("expected ErrEdgeSuperseded, got %v", err)
	}
}

func TestTemporalEdgeWriter_SupersedeEdge_InvalidTimeRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// New edge with invalid time range
	invalidFrom := now
	invalidTo := now.Add(-1 * time.Hour)
	newEdge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   2.0,
		},
		ValidFrom: &invalidFrom,
		ValidTo:   &invalidTo,
	}

	err = w.SupersedeEdge(ctx, edge.ID, newEdge)
	if err != ErrInvalidValidTimeRange {
		t.Errorf("expected ErrInvalidValidTimeRange, got %v", err)
	}
}

// =============================================================================
// SetValidTimeRange Tests
// =============================================================================

func TestTemporalEdgeWriter_SetValidTimeRange_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Set new time range
	newFrom := now.Add(-1 * time.Hour)
	newTo := now
	err = w.SetValidTimeRange(ctx, edge.ID, &newFrom, &newTo)
	if err != nil {
		t.Fatalf("SetValidTimeRange failed: %v", err)
	}

	// Verify the time range was updated
	updatedEdge, err := w.getEdgeByID(ctx, edge.ID)
	if err != nil {
		t.Fatalf("failed to get updated edge: %v", err)
	}

	if updatedEdge.ValidFrom == nil || updatedEdge.ValidFrom.Unix() != newFrom.Unix() {
		t.Error("ValidFrom not updated correctly")
	}
	if updatedEdge.ValidTo == nil || updatedEdge.ValidTo.Unix() != newTo.Unix() {
		t.Error("ValidTo not updated correctly")
	}
}

func TestTemporalEdgeWriter_SetValidTimeRange_InvalidRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Try to set invalid range
	invalidFrom := now
	invalidTo := now.Add(-1 * time.Hour)
	err = w.SetValidTimeRange(ctx, edge.ID, &invalidFrom, &invalidTo)
	if err != ErrInvalidValidTimeRange {
		t.Errorf("expected ErrInvalidValidTimeRange, got %v", err)
	}
}

func TestTemporalEdgeWriter_SetValidTimeRange_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	err := w.SetValidTimeRange(ctx, 999, &now, nil)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

func TestTemporalEdgeWriter_SetValidTimeRange_Superseded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	originalID := edge.ID

	// Supersede the edge
	newEdge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   2.0,
		},
	}
	err = w.SupersedeEdge(ctx, originalID, newEdge)
	if err != nil {
		t.Fatalf("SupersedeEdge failed: %v", err)
	}

	// Try to set time range on superseded edge
	newFrom := now
	err = w.SetValidTimeRange(ctx, originalID, &newFrom, nil)
	if err != ErrEdgeSuperseded {
		t.Errorf("expected ErrEdgeSuperseded, got %v", err)
	}
}

func TestTemporalEdgeWriter_SetValidTimeRange_ClearRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-2 * time.Hour)
	validTo := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, &validTo)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Clear time range (set both to nil)
	err = w.SetValidTimeRange(ctx, edge.ID, nil, nil)
	if err != nil {
		t.Fatalf("SetValidTimeRange failed: %v", err)
	}

	// Verify the time range was cleared
	updatedEdge, err := w.getEdgeByID(ctx, edge.ID)
	if err != nil {
		t.Fatalf("failed to get updated edge: %v", err)
	}

	if updatedEdge.ValidFrom != nil {
		t.Error("ValidFrom should be nil")
	}
	if updatedEdge.ValidTo != nil {
		t.Error("ValidTo should be nil")
	}
}

// =============================================================================
// GetCurrentEdge Tests
// =============================================================================

func TestTemporalEdgeWriter_GetCurrentEdge_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.5,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	current, err := w.GetCurrentEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetCurrentEdge failed: %v", err)
	}

	if current.ID != edge.ID {
		t.Errorf("expected ID %d, got %d", edge.ID, current.ID)
	}
	if current.Weight != 1.5 {
		t.Errorf("expected weight 1.5, got %.2f", current.Weight)
	}
}

func TestTemporalEdgeWriter_GetCurrentEdge_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	_, err := w.GetCurrentEdge(ctx, 999)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

func TestTemporalEdgeWriter_GetCurrentEdge_Superseded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

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

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	originalID := edge.ID

	// Supersede the edge
	newEdge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   2.0,
		},
	}
	err = w.SupersedeEdge(ctx, originalID, newEdge)
	if err != nil {
		t.Fatalf("SupersedeEdge failed: %v", err)
	}

	// GetCurrentEdge on superseded edge should return not found
	_, err = w.GetCurrentEdge(ctx, originalID)
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound for superseded edge, got %v", err)
	}
}

// =============================================================================
// GetLatestEdgeBetween Tests
// =============================================================================

func TestTemporalEdgeWriter_GetLatestEdgeBetween_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.5,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	latest, err := w.GetLatestEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetLatestEdgeBetween failed: %v", err)
	}

	if latest.SourceID != "node1" {
		t.Errorf("expected source_id 'node1', got '%s'", latest.SourceID)
	}
	if latest.TargetID != "node2" {
		t.Errorf("expected target_id 'node2', got '%s'", latest.TargetID)
	}
}

func TestTemporalEdgeWriter_GetLatestEdgeBetween_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	_, err := w.GetLatestEdgeBetween(ctx, "node1", "node2")
	if err != ErrEdgeNotFound {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

func TestTemporalEdgeWriter_GetLatestEdgeBetween_MultipleEdges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	// Create first edge
	edge1 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}
	err := w.CreateEdge(ctx, edge1, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge 1 failed: %v", err)
	}

	// Small delay
	time.Sleep(10 * time.Millisecond)

	// Create second edge with different type
	edge2 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeDefines,
			Weight:   2.0,
		},
	}
	err = w.CreateEdge(ctx, edge2, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge 2 failed: %v", err)
	}

	// GetLatestEdgeBetween should return the most recent (edge2)
	latest, err := w.GetLatestEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetLatestEdgeBetween failed: %v", err)
	}

	if latest.Weight != 2.0 {
		t.Errorf("expected weight 2.0 (latest), got %.2f", latest.Weight)
	}
}

// =============================================================================
// Complex Scenario Tests
// =============================================================================

func TestTemporalEdgeWriter_ComplexVersioningScenario(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()

	// T0: Create edge with weight 1.0
	vf := now.Add(-3 * time.Hour)
	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}
	err := w.CreateEdge(ctx, edge, &vf, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}
	v1ID := edge.ID

	// T1: Update edge (creates v2 with weight 2.0)
	time.Sleep(10 * time.Millisecond)
	err = w.UpdateEdge(ctx, v1ID, map[string]any{"weight": 2.0}, nil)
	if err != nil {
		t.Fatalf("UpdateEdge failed: %v", err)
	}

	// Get v2
	v2, err := w.GetLatestEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetLatestEdgeBetween failed: %v", err)
	}
	v2ID := v2.ID

	if v2.Weight != 2.0 {
		t.Errorf("v2 weight should be 2.0, got %.2f", v2.Weight)
	}

	// T2: Soft delete v2
	deletedAt := now.Add(-1 * time.Hour)
	err = w.DeleteEdge(ctx, v2ID, deletedAt)
	if err != nil {
		t.Fatalf("DeleteEdge failed: %v", err)
	}

	// T3: Restore v2
	err = w.RestoreEdge(ctx, v2ID)
	if err != nil {
		t.Fatalf("RestoreEdge failed: %v", err)
	}

	// T4: Supersede v2 with v3
	time.Sleep(10 * time.Millisecond)
	v3 := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   3.0,
			Metadata: map[string]any{"version": 3},
		},
	}
	err = w.SupersedeEdge(ctx, v2ID, v3)
	if err != nil {
		t.Fatalf("SupersedeEdge failed: %v", err)
	}

	// Verify final state
	// v1 should be superseded
	v1Final, err := w.getEdgeByID(ctx, v1ID)
	if err != nil {
		t.Fatalf("failed to get v1: %v", err)
	}
	if v1Final.TxEnd == nil {
		t.Error("v1 should be superseded")
	}

	// v2 should be superseded
	v2Final, err := w.getEdgeByID(ctx, v2ID)
	if err != nil {
		t.Fatalf("failed to get v2: %v", err)
	}
	if v2Final.TxEnd == nil {
		t.Error("v2 should be superseded")
	}

	// v3 should be current
	if v3.TxEnd != nil {
		t.Error("v3 should not be superseded")
	}
	if v3.Weight != 3.0 {
		t.Errorf("v3 weight should be 3.0, got %.2f", v3.Weight)
	}

	// GetLatestEdgeBetween should return v3
	latest, err := w.GetLatestEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetLatestEdgeBetween failed: %v", err)
	}
	if latest.ID != v3.ID {
		t.Errorf("latest should be v3 (ID %d), got ID %d", v3.ID, latest.ID)
	}
}

func TestTemporalEdgeWriter_MetadataPreservation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	// Create edge with metadata
	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
			Metadata: map[string]any{
				"caller":   "functionA",
				"callee":   "functionB",
				"line":     42,
				"verified": true,
			},
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Retrieve and verify metadata
	retrieved, err := w.GetCurrentEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetCurrentEdge failed: %v", err)
	}

	if retrieved.Metadata == nil {
		t.Fatal("metadata should not be nil")
	}

	if caller, ok := retrieved.Metadata["caller"].(string); !ok || caller != "functionA" {
		t.Errorf("expected caller 'functionA', got %v", retrieved.Metadata["caller"])
	}

	// Update with new metadata
	newMetadata := map[string]any{
		"caller":   "functionC",
		"callee":   "functionD",
		"line":     100,
		"verified": false,
	}
	err = w.UpdateEdge(ctx, edge.ID, map[string]any{"metadata": newMetadata}, nil)
	if err != nil {
		t.Fatalf("UpdateEdge failed: %v", err)
	}

	// Verify updated metadata
	latest, err := w.GetLatestEdgeBetween(ctx, "node1", "node2")
	if err != nil {
		t.Fatalf("GetLatestEdgeBetween failed: %v", err)
	}

	if caller, ok := latest.Metadata["caller"].(string); !ok || caller != "functionC" {
		t.Errorf("expected caller 'functionC', got %v", latest.Metadata["caller"])
	}
}

func TestTemporalEdgeWriter_TemporalIntegrityMaintained(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	w := NewTemporalEdgeWriter(db)
	ctx := context.Background()

	now := time.Now()
	validFrom := now.Add(-1 * time.Hour)

	// Create edge
	edge := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.0,
		},
	}

	err := w.CreateEdge(ctx, edge, &validFrom, nil)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Verify tx_start is set
	retrieved, err := w.GetCurrentEdge(ctx, edge.ID)
	if err != nil {
		t.Fatalf("GetCurrentEdge failed: %v", err)
	}

	if retrieved.TxStart == nil {
		t.Error("tx_start should always be set on insert")
	}

	if retrieved.TxEnd != nil {
		t.Error("tx_end should be nil for current versions")
	}

	// After update, old version should have tx_end set
	err = w.UpdateEdge(ctx, edge.ID, map[string]any{"weight": 2.0}, nil)
	if err != nil {
		t.Fatalf("UpdateEdge failed: %v", err)
	}

	oldVersion, err := w.getEdgeByID(ctx, edge.ID)
	if err != nil {
		t.Fatalf("failed to get old version: %v", err)
	}

	if oldVersion.TxEnd == nil {
		t.Error("old version should have tx_end set after update")
	}

	// tx_start of old version should be before tx_end
	if oldVersion.TxStart != nil && oldVersion.TxEnd != nil {
		if oldVersion.TxStart.After(*oldVersion.TxEnd) {
			t.Error("tx_start should be before tx_end")
		}
	}
}
