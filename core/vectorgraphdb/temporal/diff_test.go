package temporal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// TemporalDiffer Tests
// =============================================================================

func TestNewTemporalDiffer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	d := NewTemporalDiffer(db)
	if d == nil {
		t.Fatal("NewTemporalDiffer returned nil")
	}
	if d.db != db {
		t.Error("NewTemporalDiffer did not set db correctly")
	}
}

func TestTemporalDiffer_NilDB(t *testing.T) {
	d := NewTemporalDiffer(nil)

	ctx := context.Background()
	t1 := time.Now().Add(-1 * time.Hour)
	t2 := time.Now()

	_, err := d.DiffGraph(ctx, t1, t2)
	if err != ErrNilDB {
		t.Errorf("DiffGraph: expected ErrNilDB, got %v", err)
	}

	_, err = d.DiffNode(ctx, "node1", t1, t2)
	if err != ErrNilDB {
		t.Errorf("DiffNode: expected ErrNilDB, got %v", err)
	}

	_, err = d.DiffBetweenNodes(ctx, "node1", "node2", t1, t2)
	if err != ErrNilDB {
		t.Errorf("DiffBetweenNodes: expected ErrNilDB, got %v", err)
	}
}

func TestTemporalDiffer_InvalidTimeRange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	// t1 after t2 should return error
	t1 := time.Now()
	t2 := time.Now().Add(-1 * time.Hour)

	_, err := d.DiffGraph(ctx, t1, t2)
	if err != ErrInvalidTimeRange {
		t.Errorf("DiffGraph: expected ErrInvalidTimeRange, got %v", err)
	}

	_, err = d.DiffNode(ctx, "node1", t1, t2)
	if err != ErrInvalidTimeRange {
		t.Errorf("DiffNode: expected ErrInvalidTimeRange, got %v", err)
	}

	_, err = d.DiffBetweenNodes(ctx, "node1", "node2", t1, t2)
	if err != ErrInvalidTimeRange {
		t.Errorf("DiffBetweenNodes: expected ErrInvalidTimeRange, got %v", err)
	}
}

func TestTemporalDiffer_DiffGraph_EmptyResult(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	t1 := time.Now().Add(-1 * time.Hour)
	t2 := time.Now()

	diff, err := d.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff == nil {
		t.Fatal("expected diff, got nil")
	}

	if !diff.IsEmpty() {
		t.Error("expected empty diff")
	}

	if diff.TotalChanges() != 0 {
		t.Errorf("expected 0 total changes, got %d", diff.TotalChanges())
	}
}

func TestTemporalDiffer_DiffGraph_AddedEdges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// Edge created 30 minutes ago
	validFrom := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, nil)

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	// Diff from 1 hour ago to now
	t1 := now.Add(-1 * time.Hour)
	t2 := now

	diff, err := d.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge, got %d", len(diff.Added))
	}

	if len(diff.Removed) != 0 {
		t.Errorf("expected 0 removed edges, got %d", len(diff.Removed))
	}

	if len(diff.Modified) != 0 {
		t.Errorf("expected 0 modified edges, got %d", len(diff.Modified))
	}
}

func TestTemporalDiffer_DiffGraph_RemovedEdges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// Edge that expired 30 minutes ago
	validFrom := now.Add(-2 * time.Hour)
	validTo := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &validFrom, &validTo)

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	// Diff from 1 hour ago to now
	t1 := now.Add(-1 * time.Hour)
	t2 := now

	diff, err := d.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(diff.Added) != 0 {
		t.Errorf("expected 0 added edges, got %d", len(diff.Added))
	}

	if len(diff.Removed) != 1 {
		t.Errorf("expected 1 removed edge, got %d", len(diff.Removed))
	}
}

func TestTemporalDiffer_DiffGraph_ModifiedEdges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")

	now := time.Now()

	// Insert a superseded edge (modified during the range)
	vf := now.Add(-2 * time.Hour)
	txStart := now.Add(-30 * time.Minute)
	txEnd := now.Add(-15 * time.Minute)

	_, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, 1.0, '{}', datetime('now'), ?, NULL, ?, ?)
	`, "node1", "node2", vectorgraphdb.EdgeTypeCalls, vf.Unix(), txStart.Unix(), txEnd.Unix())
	if err != nil {
		t.Fatalf("failed to insert edge: %v", err)
	}

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	// Diff from 1 hour ago to now
	t1 := now.Add(-1 * time.Hour)
	t2 := now

	diff, err := d.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(diff.Modified) != 1 {
		t.Errorf("expected 1 modified edge, got %d", len(diff.Modified))
	}
}

func TestTemporalDiffer_DiffGraph_MixedChanges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Added edge (created during range)
	vf1 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, nil)

	// Removed edge (expired during range)
	vf2 := now.Add(-2 * time.Hour)
	vt2 := now.Add(-20 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, &vt2)

	// Edge outside range (should not appear)
	vf3 := now.Add(-3 * time.Hour)
	vt3 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeImports, &vf3, &vt3)

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	// Diff from 1 hour ago to now
	t1 := now.Add(-1 * time.Hour)
	t2 := now

	diff, err := d.DiffGraph(ctx, t1, t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge, got %d", len(diff.Added))
	}

	if len(diff.Removed) != 1 {
		t.Errorf("expected 1 removed edge, got %d", len(diff.Removed))
	}

	if diff.TotalChanges() != 2 {
		t.Errorf("expected 2 total changes, got %d", diff.TotalChanges())
	}

	if diff.IsEmpty() {
		t.Error("diff should not be empty")
	}
}

func TestTemporalDiffer_DiffNode(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Edge from node1 (should be included)
	vf1 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, nil)

	// Edge from node2 (should NOT be included)
	vf2 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node2", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, nil)

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	// Diff for node1 from 1 hour ago to now
	t1 := now.Add(-1 * time.Hour)
	t2 := now

	diff, err := d.DiffNode(ctx, "node1", t1, t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge for node1, got %d", len(diff.Added))
	}

	if diff.Added[0].SourceID != "node1" {
		t.Errorf("expected source_id 'node1', got '%s'", diff.Added[0].SourceID)
	}
}

func TestTemporalDiffer_DiffBetweenNodes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")

	now := time.Now()

	// Edge from node1 to node2 (should be included)
	vf1 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, nil)

	// Edge from node1 to node3 (should NOT be included)
	vf2 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, nil)

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	// Diff between node1 and node2 from 1 hour ago to now
	t1 := now.Add(-1 * time.Hour)
	t2 := now

	diff, err := d.DiffBetweenNodes(ctx, "node1", "node2", t1, t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added edge between node1 and node2, got %d", len(diff.Added))
	}

	if diff.Added[0].TargetID != "node2" {
		t.Errorf("expected target_id 'node2', got '%s'", diff.Added[0].TargetID)
	}
}

// =============================================================================
// GraphDiff Tests
// =============================================================================

func TestGraphDiff_Summary_Empty(t *testing.T) {
	diff := &GraphDiff{
		T1:       time.Now().Add(-1 * time.Hour),
		T2:       time.Now(),
		Added:    []vectorgraphdb.TemporalEdge{},
		Removed:  []vectorgraphdb.TemporalEdge{},
		Modified: []EdgeModification{},
	}

	summary := diff.Summary()

	if !strings.Contains(summary, "No changes detected") {
		t.Errorf("expected 'No changes detected' in summary, got: %s", summary)
	}
}

func TestGraphDiff_Summary_WithChanges(t *testing.T) {
	now := time.Now()
	vf := now.Add(-1 * time.Hour)

	diff := &GraphDiff{
		T1: now.Add(-1 * time.Hour),
		T2: now,
		Added: []vectorgraphdb.TemporalEdge{
			{
				GraphEdge: vectorgraphdb.GraphEdge{
					ID:       1,
					SourceID: "node1",
					TargetID: "node2",
					EdgeType: vectorgraphdb.EdgeTypeCalls,
				},
				ValidFrom: &vf,
			},
		},
		Removed: []vectorgraphdb.TemporalEdge{
			{
				GraphEdge: vectorgraphdb.GraphEdge{
					ID:       2,
					SourceID: "node2",
					TargetID: "node3",
					EdgeType: vectorgraphdb.EdgeTypeDefines,
				},
				ValidFrom: &vf,
			},
		},
		Modified: []EdgeModification{},
	}

	summary := diff.Summary()

	if !strings.Contains(summary, "Added: 1 edge(s)") {
		t.Errorf("expected 'Added: 1 edge(s)' in summary, got: %s", summary)
	}

	if !strings.Contains(summary, "Removed: 1 edge(s)") {
		t.Errorf("expected 'Removed: 1 edge(s)' in summary, got: %s", summary)
	}

	if !strings.Contains(summary, "node1 -> node2") {
		t.Errorf("expected 'node1 -> node2' in summary, got: %s", summary)
	}
}

func TestGraphDiff_Summary_Nil(t *testing.T) {
	var diff *GraphDiff = nil

	summary := diff.Summary()

	if summary != "No diff available" {
		t.Errorf("expected 'No diff available', got: %s", summary)
	}
}

func TestGraphDiff_Summary_ManyChanges(t *testing.T) {
	now := time.Now()
	vf := now.Add(-1 * time.Hour)

	// Create 5 added edges (should truncate to 3 with "... and 2 more")
	added := make([]vectorgraphdb.TemporalEdge, 5)
	for i := 0; i < 5; i++ {
		added[i] = vectorgraphdb.TemporalEdge{
			GraphEdge: vectorgraphdb.GraphEdge{
				ID:       int64(i + 1),
				SourceID: "node1",
				TargetID: "node2",
				EdgeType: vectorgraphdb.EdgeTypeCalls,
			},
			ValidFrom: &vf,
		}
	}

	diff := &GraphDiff{
		T1:       now.Add(-1 * time.Hour),
		T2:       now,
		Added:    added,
		Removed:  []vectorgraphdb.TemporalEdge{},
		Modified: []EdgeModification{},
	}

	summary := diff.Summary()

	if !strings.Contains(summary, "... and 2 more") {
		t.Errorf("expected truncation message '... and 2 more' in summary, got: %s", summary)
	}
}

func TestGraphDiff_IsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		diff     *GraphDiff
		expected bool
	}{
		{
			name:     "nil diff",
			diff:     nil,
			expected: true,
		},
		{
			name: "empty diff",
			diff: &GraphDiff{
				Added:    []vectorgraphdb.TemporalEdge{},
				Removed:  []vectorgraphdb.TemporalEdge{},
				Modified: []EdgeModification{},
			},
			expected: true,
		},
		{
			name: "with added",
			diff: &GraphDiff{
				Added:    []vectorgraphdb.TemporalEdge{{}},
				Removed:  []vectorgraphdb.TemporalEdge{},
				Modified: []EdgeModification{},
			},
			expected: false,
		},
		{
			name: "with removed",
			diff: &GraphDiff{
				Added:    []vectorgraphdb.TemporalEdge{},
				Removed:  []vectorgraphdb.TemporalEdge{{}},
				Modified: []EdgeModification{},
			},
			expected: false,
		},
		{
			name: "with modified",
			diff: &GraphDiff{
				Added:    []vectorgraphdb.TemporalEdge{},
				Removed:  []vectorgraphdb.TemporalEdge{},
				Modified: []EdgeModification{{}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.diff.IsEmpty()
			if result != tt.expected {
				t.Errorf("expected IsEmpty() = %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGraphDiff_TotalChanges(t *testing.T) {
	tests := []struct {
		name     string
		diff     *GraphDiff
		expected int
	}{
		{
			name:     "nil diff",
			diff:     nil,
			expected: 0,
		},
		{
			name: "empty diff",
			diff: &GraphDiff{
				Added:    []vectorgraphdb.TemporalEdge{},
				Removed:  []vectorgraphdb.TemporalEdge{},
				Modified: []EdgeModification{},
			},
			expected: 0,
		},
		{
			name: "mixed changes",
			diff: &GraphDiff{
				Added:    []vectorgraphdb.TemporalEdge{{}, {}},
				Removed:  []vectorgraphdb.TemporalEdge{{}},
				Modified: []EdgeModification{{}, {}, {}},
			},
			expected: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.diff.TotalChanges()
			if result != tt.expected {
				t.Errorf("expected TotalChanges() = %d, got %d", tt.expected, result)
			}
		})
	}
}

// =============================================================================
// EdgeModification Tests
// =============================================================================

func TestComputeEdgeChanges(t *testing.T) {
	now := time.Now()
	vf := now.Add(-1 * time.Hour)
	vfNew := now.Add(-30 * time.Minute)

	tests := []struct {
		name           string
		before         *vectorgraphdb.TemporalEdge
		after          *vectorgraphdb.TemporalEdge
		expectedChange string
	}{
		{
			name: "edge type changed",
			before: &vectorgraphdb.TemporalEdge{
				GraphEdge: vectorgraphdb.GraphEdge{
					EdgeType: vectorgraphdb.EdgeTypeCalls,
					Weight:   1.0,
				},
				ValidFrom: &vf,
			},
			after: &vectorgraphdb.TemporalEdge{
				GraphEdge: vectorgraphdb.GraphEdge{
					EdgeType: vectorgraphdb.EdgeTypeDefines,
					Weight:   1.0,
				},
				ValidFrom: &vf,
			},
			expectedChange: "edge type changed",
		},
		{
			name: "weight changed",
			before: &vectorgraphdb.TemporalEdge{
				GraphEdge: vectorgraphdb.GraphEdge{
					EdgeType: vectorgraphdb.EdgeTypeCalls,
					Weight:   1.0,
				},
				ValidFrom: &vf,
			},
			after: &vectorgraphdb.TemporalEdge{
				GraphEdge: vectorgraphdb.GraphEdge{
					EdgeType: vectorgraphdb.EdgeTypeCalls,
					Weight:   2.0,
				},
				ValidFrom: &vf,
			},
			expectedChange: "weight changed",
		},
		{
			name: "valid_from changed",
			before: &vectorgraphdb.TemporalEdge{
				GraphEdge: vectorgraphdb.GraphEdge{
					EdgeType: vectorgraphdb.EdgeTypeCalls,
					Weight:   1.0,
				},
				ValidFrom: &vf,
			},
			after: &vectorgraphdb.TemporalEdge{
				GraphEdge: vectorgraphdb.GraphEdge{
					EdgeType: vectorgraphdb.EdgeTypeCalls,
					Weight:   1.0,
				},
				ValidFrom: &vfNew,
			},
			expectedChange: "valid_from changed",
		},
		{
			name:           "nil before",
			before:         nil,
			after:          &vectorgraphdb.TemporalEdge{},
			expectedChange: "edge replaced",
		},
		{
			name:           "nil after",
			before:         &vectorgraphdb.TemporalEdge{},
			after:          nil,
			expectedChange: "edge replaced",
		},
		{
			name: "no changes",
			before: &vectorgraphdb.TemporalEdge{
				GraphEdge: vectorgraphdb.GraphEdge{
					EdgeType: vectorgraphdb.EdgeTypeCalls,
					Weight:   1.0,
				},
				ValidFrom: &vf,
			},
			after: &vectorgraphdb.TemporalEdge{
				GraphEdge: vectorgraphdb.GraphEdge{
					EdgeType: vectorgraphdb.EdgeTypeCalls,
					Weight:   1.0,
				},
				ValidFrom: &vf,
			},
			expectedChange: "edge updated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := computeEdgeChanges(tt.before, tt.after)

			found := false
			for _, change := range changes {
				if strings.Contains(change, tt.expectedChange) {
					found = true
					break
				}
			}

			if !found {
				t.Errorf("expected change containing '%s', got %v", tt.expectedChange, changes)
			}
		})
	}
}

func TestCopyTemporalEdge(t *testing.T) {
	now := time.Now()
	vf := now.Add(-1 * time.Hour)
	vt := now
	txStart := now.Add(-1 * time.Hour)
	txEnd := now.Add(-30 * time.Minute)

	original := &vectorgraphdb.TemporalEdge{
		GraphEdge: vectorgraphdb.GraphEdge{
			ID:       1,
			SourceID: "node1",
			TargetID: "node2",
			EdgeType: vectorgraphdb.EdgeTypeCalls,
			Weight:   1.5,
			Metadata: map[string]any{
				"key": "value",
			},
		},
		ValidFrom: &vf,
		ValidTo:   &vt,
		TxStart:   &txStart,
		TxEnd:     &txEnd,
	}

	copy := copyTemporalEdge(original)

	// Verify basic fields
	if copy.ID != original.ID {
		t.Errorf("ID mismatch: expected %d, got %d", original.ID, copy.ID)
	}

	if copy.SourceID != original.SourceID {
		t.Errorf("SourceID mismatch: expected %s, got %s", original.SourceID, copy.SourceID)
	}

	// Verify deep copy of pointers (modifying copy shouldn't affect original)
	newTime := now.Add(1 * time.Hour)
	copy.ValidFrom = &newTime

	if original.ValidFrom.Equal(*copy.ValidFrom) {
		t.Error("modifying copy's ValidFrom affected original")
	}

	// Verify deep copy of metadata
	copy.Metadata["key"] = "modified"

	if original.Metadata["key"] == "modified" {
		t.Error("modifying copy's Metadata affected original")
	}
}

func TestCopyTemporalEdge_Nil(t *testing.T) {
	result := copyTemporalEdge(nil)
	if result != nil {
		t.Error("expected nil for nil input")
	}
}

func TestTimeEqual(t *testing.T) {
	now := time.Now()
	later := now.Add(1 * time.Hour)

	tests := []struct {
		name     string
		t1       *time.Time
		t2       *time.Time
		expected bool
	}{
		{
			name:     "both nil",
			t1:       nil,
			t2:       nil,
			expected: true,
		},
		{
			name:     "first nil",
			t1:       nil,
			t2:       &now,
			expected: false,
		},
		{
			name:     "second nil",
			t1:       &now,
			t2:       nil,
			expected: false,
		},
		{
			name:     "equal times",
			t1:       &now,
			t2:       &now,
			expected: true,
		},
		{
			name:     "different times",
			t1:       &now,
			t2:       &later,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := timeEqual(tt.t1, tt.t2)
			if result != tt.expected {
				t.Errorf("expected timeEqual() = %v, got %v", tt.expected, result)
			}
		})
	}
}

// =============================================================================
// Complex Scenario Tests
// =============================================================================

func TestTemporalDiffer_ComplexScenario(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestNode(t, db, "node1", "Test Node 1")
	insertTestNode(t, db, "node2", "Test Node 2")
	insertTestNode(t, db, "node3", "Test Node 3")
	insertTestNode(t, db, "node4", "Test Node 4")

	now := time.Now()

	// Scenario: Graph evolution over 3 hours
	// T-3h to T-2h: Initial state with edge1 (node1->node2)
	// T-2h to T-1h: edge1 modified, edge2 added (node1->node3)
	// T-1h to T-0:  edge1 removed, edge2 still active, edge3 added (node2->node4)

	// edge1: Created T-3h, removed T-1h
	vf1 := now.Add(-3 * time.Hour)
	vt1 := now.Add(-1 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node2", vectorgraphdb.EdgeTypeCalls, &vf1, &vt1)

	// edge2: Created T-2h, still active
	vf2 := now.Add(-2 * time.Hour)
	insertTemporalEdge(t, db, "node1", "node3", vectorgraphdb.EdgeTypeDefines, &vf2, nil)

	// edge3: Created T-30m, still active
	vf3 := now.Add(-30 * time.Minute)
	insertTemporalEdge(t, db, "node2", "node4", vectorgraphdb.EdgeTypeImports, &vf3, nil)

	d := NewTemporalDiffer(db)
	ctx := context.Background()

	// Test 1: Diff from T-3h to T-2h (edge1 added)
	t.Run("T-3h to T-2h", func(t *testing.T) {
		t1 := now.Add(-3 * time.Hour)
		t2 := now.Add(-2 * time.Hour)

		diff, err := d.DiffGraph(ctx, t1, t2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(diff.Added) != 1 {
			t.Errorf("expected 1 added edge, got %d", len(diff.Added))
		}
		if len(diff.Removed) != 0 {
			t.Errorf("expected 0 removed edges, got %d", len(diff.Removed))
		}
	})

	// Test 2: Diff from T-2h to T-1h (edge2 added)
	t.Run("T-2h to T-1h", func(t *testing.T) {
		t1 := now.Add(-2 * time.Hour)
		t2 := now.Add(-1 * time.Hour)

		diff, err := d.DiffGraph(ctx, t1, t2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(diff.Added) != 1 {
			t.Errorf("expected 1 added edge (edge2), got %d", len(diff.Added))
		}
		if len(diff.Removed) != 0 {
			t.Errorf("expected 0 removed edges, got %d", len(diff.Removed))
		}
	})

	// Test 3: Diff from T-1h to T-0 (edge1 removed, edge3 added)
	t.Run("T-1h to T-0", func(t *testing.T) {
		t1 := now.Add(-1 * time.Hour)
		t2 := now

		diff, err := d.DiffGraph(ctx, t1, t2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(diff.Added) != 1 {
			t.Errorf("expected 1 added edge (edge3), got %d", len(diff.Added))
		}
		if len(diff.Removed) != 1 {
			t.Errorf("expected 1 removed edge (edge1), got %d", len(diff.Removed))
		}
	})

	// Test 4: Diff for node1 only from T-3h to T-0
	t.Run("node1 from T-3h to T-0", func(t *testing.T) {
		t1 := now.Add(-3 * time.Hour)
		t2 := now

		diff, err := d.DiffNode(ctx, "node1", t1, t2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should show edge1 added and removed, edge2 added
		if len(diff.Added) != 2 {
			t.Errorf("expected 2 added edges for node1 (edge1 + edge2), got %d", len(diff.Added))
		}
		if len(diff.Removed) != 1 {
			t.Errorf("expected 1 removed edge for node1 (edge1), got %d", len(diff.Removed))
		}
	})

	// Test 5: Full summary
	t.Run("summary", func(t *testing.T) {
		t1 := now.Add(-3 * time.Hour)
		t2 := now

		diff, err := d.DiffGraph(ctx, t1, t2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		summary := diff.Summary()

		if !strings.Contains(summary, "Time range:") {
			t.Error("summary should contain time range")
		}

		if !strings.Contains(summary, "Added:") {
			t.Error("summary should contain added edges")
		}

		if !strings.Contains(summary, "Removed:") {
			t.Error("summary should contain removed edges")
		}
	})
}
