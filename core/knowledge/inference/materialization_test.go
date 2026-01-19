package inference

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Test Helpers
// =============================================================================

// setupMaterializationTestDB creates an in-memory SQLite database with the
// materialized_edges table including the extended columns.
func setupMaterializationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use shared cache mode for in-memory database to support concurrent access
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create inference_rules table (required for foreign key)
	_, err = db.Exec(`
		CREATE TABLE inference_rules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			head_subject TEXT NOT NULL,
			head_predicate TEXT NOT NULL,
			head_object TEXT NOT NULL,
			body_json TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			CHECK (enabled IN (0, 1))
		)
	`)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create inference_rules table: %v", err)
	}

	// Create extended materialized_edges table with edge_key and evidence_json
	_, err = db.Exec(`
		CREATE TABLE materialized_edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			rule_id TEXT NOT NULL,
			edge_key TEXT NOT NULL,
			evidence_json TEXT NOT NULL,
			derived_at TEXT NOT NULL,
			FOREIGN KEY (rule_id) REFERENCES inference_rules(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create materialized_edges table: %v", err)
	}

	// Create indexes
	_, err = db.Exec(`
		CREATE INDEX idx_materialized_rule ON materialized_edges(rule_id);
		CREATE INDEX idx_materialized_edge_key ON materialized_edges(edge_key);
	`)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create indexes: %v", err)
	}

	return db
}

// createTestInferenceResult creates a test inference result.
func createTestInferenceResult(ruleID, source, predicate, target string, evidenceKeys ...string) InferenceResult {
	evidence := make([]EvidenceEdge, len(evidenceKeys))
	for i, key := range evidenceKeys {
		// Parse key format: source:predicate:target
		evidence[i] = EvidenceEdge{
			SourceID: "ev_source_" + key,
			EdgeType: "ev_pred_" + key,
			TargetID: "ev_target_" + key,
		}
	}

	return InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: source,
			EdgeType: predicate,
			TargetID: target,
		},
		RuleID:     ruleID,
		Evidence:   evidence,
		DerivedAt:  time.Now(),
		Confidence: 1.0,
	}
}

// insertTestRule inserts a minimal test rule into the database.
func insertTestRule(t *testing.T, db *sql.DB, ruleID string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, '?x', 'test', '?y', '[]', 0, 1, ?)
	`, ruleID, "Test Rule "+ruleID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to insert test rule: %v", err)
	}
}

// =============================================================================
// NewMaterializationManager Tests
// =============================================================================

func TestNewMaterializationManager(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()

	manager := NewMaterializationManager(db)

	if manager == nil {
		t.Fatal("NewMaterializationManager returned nil")
	}
	if manager.db != db {
		t.Error("database not set correctly")
	}
}

// =============================================================================
// Materialize Tests
// =============================================================================

func TestMaterializationManager_Materialize_Single(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	result := createTestInferenceResult("rule1", "A", "calls", "B", "ev1")
	err := manager.Materialize(ctx, []InferenceResult{result})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Verify the edge was stored
	edgeKey := "A|calls|B"
	exists, err := manager.IsMaterialized(ctx, edgeKey)
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if !exists {
		t.Error("edge was not materialized")
	}
}

func TestMaterializationManager_Materialize_Multiple(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	results := []InferenceResult{
		createTestInferenceResult("rule1", "A", "calls", "B", "ev1"),
		createTestInferenceResult("rule1", "B", "calls", "C", "ev2"),
		createTestInferenceResult("rule1", "C", "calls", "D", "ev3"),
	}

	err := manager.Materialize(ctx, results)
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Verify all edges were stored
	for _, key := range []string{"A|calls|B", "B|calls|C", "C|calls|D"} {
		exists, err := manager.IsMaterialized(ctx, key)
		if err != nil {
			t.Fatalf("IsMaterialized failed for %s: %v", key, err)
		}
		if !exists {
			t.Errorf("edge %s was not materialized", key)
		}
	}
}

func TestMaterializationManager_Materialize_AvoidsDuplicates(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	result := createTestInferenceResult("rule1", "A", "calls", "B", "ev1")

	// Materialize the same edge twice
	if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("first Materialize failed: %v", err)
	}
	if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("second Materialize failed: %v", err)
	}

	// Count records in database
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM materialized_edges WHERE edge_key = ?", "A|calls|B").Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 record, got %d", count)
	}
}

func TestMaterializationManager_Materialize_Empty(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Materialize with empty slice should succeed without error
	err := manager.Materialize(ctx, []InferenceResult{})
	if err != nil {
		t.Fatalf("Materialize with empty slice failed: %v", err)
	}
}

// =============================================================================
// IsMaterialized Tests
// =============================================================================

func TestMaterializationManager_IsMaterialized_Exists(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	result := createTestInferenceResult("rule1", "A", "calls", "B", "ev1")
	if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	exists, err := manager.IsMaterialized(ctx, "A|calls|B")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if !exists {
		t.Error("expected edge to exist")
	}
}

func TestMaterializationManager_IsMaterialized_NotExists(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	exists, err := manager.IsMaterialized(ctx, "nonexistent|edge|key")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if exists {
		t.Error("expected edge to not exist")
	}
}

// =============================================================================
// GetMaterializedEdges Tests
// =============================================================================

func TestMaterializationManager_GetMaterializedEdges_Empty(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	edges, err := manager.GetMaterializedEdges(ctx, "rule1")
	if err != nil {
		t.Fatalf("GetMaterializedEdges failed: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected empty slice, got %d edges", len(edges))
	}
}

func TestMaterializationManager_GetMaterializedEdges_Multiple(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")
	insertTestRule(t, db, "rule2")

	// Materialize edges from different rules
	results := []InferenceResult{
		createTestInferenceResult("rule1", "A", "calls", "B", "ev1"),
		createTestInferenceResult("rule1", "B", "calls", "C", "ev2"),
		createTestInferenceResult("rule2", "X", "calls", "Y", "ev3"),
	}

	if err := manager.Materialize(ctx, results); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Get edges for rule1
	edges, err := manager.GetMaterializedEdges(ctx, "rule1")
	if err != nil {
		t.Fatalf("GetMaterializedEdges failed: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 edges for rule1, got %d", len(edges))
	}

	// Verify all returned edges are from rule1
	for _, edge := range edges {
		if edge.RuleID != "rule1" {
			t.Errorf("got edge from rule %s, expected rule1", edge.RuleID)
		}
	}
}

func TestMaterializationManager_GetMaterializedEdges_VerifiesFields(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	result := createTestInferenceResult("rule1", "A", "calls", "B", "ev1", "ev2")
	if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	edges, err := manager.GetMaterializedEdges(ctx, "rule1")
	if err != nil {
		t.Fatalf("GetMaterializedEdges failed: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}

	edge := edges[0]
	if edge.ID == 0 {
		t.Error("ID should not be 0")
	}
	if edge.RuleID != "rule1" {
		t.Errorf("RuleID mismatch: got %s, want rule1", edge.RuleID)
	}
	if edge.EdgeKey != "A|calls|B" {
		t.Errorf("EdgeKey mismatch: got %s, want A|calls|B", edge.EdgeKey)
	}
	if edge.DerivedAt.IsZero() {
		t.Error("DerivedAt should not be zero")
	}
	if len(edge.Evidence) != 2 {
		t.Errorf("Evidence length mismatch: got %d, want 2", len(edge.Evidence))
	}
}

// =============================================================================
// DeleteMaterializedByRule Tests
// =============================================================================

func TestMaterializationManager_DeleteMaterializedByRule_Exists(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")
	insertTestRule(t, db, "rule2")

	// Materialize edges from different rules
	results := []InferenceResult{
		createTestInferenceResult("rule1", "A", "calls", "B", "ev1"),
		createTestInferenceResult("rule1", "B", "calls", "C", "ev2"),
		createTestInferenceResult("rule2", "X", "calls", "Y", "ev3"),
	}

	if err := manager.Materialize(ctx, results); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Delete edges from rule1
	if err := manager.DeleteMaterializedByRule(ctx, "rule1"); err != nil {
		t.Fatalf("DeleteMaterializedByRule failed: %v", err)
	}

	// Verify rule1 edges are deleted
	edges, err := manager.GetMaterializedEdges(ctx, "rule1")
	if err != nil {
		t.Fatalf("GetMaterializedEdges failed: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for rule1, got %d", len(edges))
	}

	// Verify rule2 edges are still there
	edges, err = manager.GetMaterializedEdges(ctx, "rule2")
	if err != nil {
		t.Fatalf("GetMaterializedEdges failed: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge for rule2, got %d", len(edges))
	}
}

func TestMaterializationManager_DeleteMaterializedByRule_NotExists(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Deleting non-existent rule should not error
	err := manager.DeleteMaterializedByRule(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("DeleteMaterializedByRule failed: %v", err)
	}
}

// =============================================================================
// GetAllMaterializedEdges Tests
// =============================================================================

func TestMaterializationManager_GetAllMaterializedEdges_Empty(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	edges, err := manager.GetAllMaterializedEdges(ctx)
	if err != nil {
		t.Fatalf("GetAllMaterializedEdges failed: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected empty slice, got %d edges", len(edges))
	}
}

func TestMaterializationManager_GetAllMaterializedEdges_Multiple(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")
	insertTestRule(t, db, "rule2")

	results := []InferenceResult{
		createTestInferenceResult("rule1", "A", "calls", "B", "ev1"),
		createTestInferenceResult("rule2", "X", "calls", "Y", "ev2"),
	}

	if err := manager.Materialize(ctx, results); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	edges, err := manager.GetAllMaterializedEdges(ctx)
	if err != nil {
		t.Fatalf("GetAllMaterializedEdges failed: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(edges))
	}
}

// =============================================================================
// GetMaterializedByEvidence Tests
// =============================================================================

func TestMaterializationManager_GetMaterializedByEvidence_Found(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create result with specific evidence
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "A",
			EdgeType: "calls",
			TargetID: "B",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "X", EdgeType: "depends", TargetID: "Y"},
		},
		DerivedAt:  time.Now(),
		Confidence: 1.0,
	}

	if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Search by evidence key
	edges, err := manager.GetMaterializedByEvidence(ctx, "X|depends|Y")
	if err != nil {
		t.Fatalf("GetMaterializedByEvidence failed: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(edges))
	}
}

func TestMaterializationManager_GetMaterializedByEvidence_NotFound(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	edges, err := manager.GetMaterializedByEvidence(ctx, "nonexistent|evidence|key")
	if err != nil {
		t.Fatalf("GetMaterializedByEvidence failed: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

// =============================================================================
// DeleteMaterializedEdge Tests
// =============================================================================

func TestMaterializationManager_DeleteMaterializedEdge_Exists(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	result := createTestInferenceResult("rule1", "A", "calls", "B", "ev1")
	if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Delete the edge
	if err := manager.DeleteMaterializedEdge(ctx, "A|calls|B"); err != nil {
		t.Fatalf("DeleteMaterializedEdge failed: %v", err)
	}

	// Verify it's deleted
	exists, err := manager.IsMaterialized(ctx, "A|calls|B")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if exists {
		t.Error("edge should not exist after deletion")
	}
}

func TestMaterializationManager_DeleteMaterializedEdge_NotExists(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Deleting non-existent edge should not error
	err := manager.DeleteMaterializedEdge(ctx, "nonexistent|edge|key")
	if err != nil {
		t.Fatalf("DeleteMaterializedEdge failed: %v", err)
	}
}

// =============================================================================
// BuildEdgeKey Tests
// =============================================================================

func TestMaterializationManager_BuildEdgeKey(t *testing.T) {
	manager := &MaterializationManager{}

	testCases := []struct {
		edge     DerivedEdge
		expected string
	}{
		{
			edge:     DerivedEdge{SourceID: "A", EdgeType: "calls", TargetID: "B"},
			expected: "A|calls|B",
		},
		{
			edge:     DerivedEdge{SourceID: "pkg/foo", EdgeType: "imports", TargetID: "pkg/bar"},
			expected: "pkg/foo|imports|pkg/bar",
		},
		{
			edge:     DerivedEdge{SourceID: "", EdgeType: "empty", TargetID: ""},
			expected: "|empty|",
		},
	}

	for _, tc := range testCases {
		result := manager.buildEdgeKey(tc.edge)
		if result != tc.expected {
			t.Errorf("buildEdgeKey(%+v) = %s, want %s", tc.edge, result, tc.expected)
		}
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestMaterializationManager_ConcurrentMaterialize(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Run multiple materializations concurrently
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			result := createTestInferenceResult("rule1", "A", "calls", "B", "ev1")
			done <- manager.Materialize(ctx, []InferenceResult{result})
		}(i)
	}

	// Collect errors
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent Materialize failed: %v", err)
		}
	}

	// Verify only one record exists (deduplication)
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM materialized_edges").Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 record after concurrent inserts, got %d", count)
	}
}

func TestMaterializationManager_ConcurrentReads(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	result := createTestInferenceResult("rule1", "A", "calls", "B", "ev1")
	if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Run multiple reads concurrently using sync.WaitGroup
	var wg sync.WaitGroup
	errors := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := manager.IsMaterialized(ctx, "A|calls|B")
			errors[idx] = err
		}(i)
	}

	// Wait for all goroutines to complete before checking errors
	wg.Wait()

	// Now check for errors
	for i, err := range errors {
		if err != nil {
			t.Errorf("concurrent IsMaterialized %d failed: %v", i, err)
		}
	}
}
