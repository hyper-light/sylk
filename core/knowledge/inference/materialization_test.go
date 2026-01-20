package inference

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	return setupMaterializationTestDBWithFK(t, false)
}

// setupMaterializationTestDBWithFK creates an in-memory SQLite database with
// optional foreign key enforcement enabled.
func setupMaterializationTestDBWithFK(t *testing.T, enforceForeignKeys bool) *sql.DB {
	t.Helper()
	// Use shared cache mode for in-memory database to support concurrent access
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Enable foreign keys if requested
	if enforceForeignKeys {
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		if err != nil {
			db.Close()
			t.Fatalf("failed to enable foreign keys: %v", err)
		}
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

// TestMaterializationManager_GetMaterializedByEvidence_NoFalsePositives tests
// that substring matches don't cause false positives (W3M.15 fix).
func TestMaterializationManager_GetMaterializedByEvidence_NoFalsePositives(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create edges with evidence that could cause substring false positives
	// Evidence: "X|depends|Y" should NOT match "AX|depends|YB" or "X|depends|YZ"
	results := []InferenceResult{
		{
			DerivedEdge: DerivedEdge{SourceID: "A", EdgeType: "calls", TargetID: "B"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "X", EdgeType: "depends", TargetID: "Y"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
		{
			DerivedEdge: DerivedEdge{SourceID: "C", EdgeType: "calls", TargetID: "D"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "AX", EdgeType: "depends", TargetID: "YB"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
		{
			DerivedEdge: DerivedEdge{SourceID: "E", EdgeType: "calls", TargetID: "F"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "X", EdgeType: "depends", TargetID: "YZ"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
	}

	if err := manager.Materialize(ctx, results); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Search for exact match - should only find the first one
	edges, err := manager.GetMaterializedByEvidence(ctx, "X|depends|Y")
	if err != nil {
		t.Fatalf("GetMaterializedByEvidence failed: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge (exact match only), got %d", len(edges))
	}
	if len(edges) > 0 && edges[0].EdgeKey != "A|calls|B" {
		t.Errorf("expected edge A|calls|B, got %s", edges[0].EdgeKey)
	}
}

// TestMaterializationManager_GetMaterializedByEvidence_SimilarKeys tests that
// keys with similar prefixes/suffixes don't cause false positives.
func TestMaterializationManager_GetMaterializedByEvidence_SimilarKeys(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create edges with similar but different evidence keys
	results := []InferenceResult{
		{
			DerivedEdge: DerivedEdge{SourceID: "A", EdgeType: "calls", TargetID: "B"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "foo", EdgeType: "imports", TargetID: "bar"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
		{
			DerivedEdge: DerivedEdge{SourceID: "C", EdgeType: "calls", TargetID: "D"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "foo_ext", EdgeType: "imports", TargetID: "bar_ext"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
		{
			DerivedEdge: DerivedEdge{SourceID: "E", EdgeType: "calls", TargetID: "F"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "prefix_foo", EdgeType: "imports", TargetID: "prefix_bar"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
	}

	if err := manager.Materialize(ctx, results); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Search for exact match - should only find the first one
	edges, err := manager.GetMaterializedByEvidence(ctx, "foo|imports|bar")
	if err != nil {
		t.Fatalf("GetMaterializedByEvidence failed: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge for foo|imports|bar, got %d", len(edges))
	}
}

// TestMaterializationManager_GetMaterializedByEvidence_SpecialCharacters tests
// that special characters in evidence keys are handled correctly via exact matching.
func TestMaterializationManager_GetMaterializedByEvidence_SpecialCharacters(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create edges with special characters in evidence (avoiding backslashes
	// which have complex JSON encoding behavior)
	results := []InferenceResult{
		{
			DerivedEdge: DerivedEdge{SourceID: "A", EdgeType: "calls", TargetID: "B"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "pkg/foo-bar", EdgeType: "imports", TargetID: "pkg/baz.qux"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
		{
			DerivedEdge: DerivedEdge{SourceID: "C", EdgeType: "calls", TargetID: "D"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "test:path", EdgeType: "imports", TargetID: "other:path"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
	}

	if err := manager.Materialize(ctx, results); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Search for key with special characters (hyphen, dot)
	edges, err := manager.GetMaterializedByEvidence(ctx, "pkg/foo-bar|imports|pkg/baz.qux")
	if err != nil {
		t.Fatalf("GetMaterializedByEvidence failed: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge for special char key, got %d", len(edges))
	}

	// Search for key with colons
	edges, err = manager.GetMaterializedByEvidence(ctx, "test:path|imports|other:path")
	if err != nil {
		t.Fatalf("GetMaterializedByEvidence failed: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge for colon key, got %d", len(edges))
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

// =============================================================================
// W3M.14 Lock Contention Tests
// =============================================================================

func TestMaterializationManager_ConcurrentMaterializeDifferentEdges(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Run multiple materializations concurrently with different edges
	numGoroutines := 20
	var wg sync.WaitGroup
	errors := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			source := "Node" + string(rune('A'+idx))
			target := "Node" + string(rune('B'+idx))
			result := createTestInferenceResult("rule1", source, "calls", target, "ev"+source)
			errors[idx] = manager.Materialize(ctx, []InferenceResult{result})
		}(i)
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			t.Errorf("concurrent Materialize %d failed: %v", i, err)
		}
	}

	// Verify all edges were stored
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM materialized_edges").Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != numGoroutines {
		t.Errorf("expected %d records, got %d", numGoroutines, count)
	}
}

func TestMaterializationManager_NoDataCorruption(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create a batch of edges to insert
	numEdges := 50
	var wg sync.WaitGroup
	errors := make([]error, numEdges)

	for i := 0; i < numEdges; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result := InferenceResult{
				DerivedEdge: DerivedEdge{
					SourceID: "Source" + string(rune('A'+idx%26)) + "_" + string(rune('0'+idx/26)),
					EdgeType: "edge_type",
					TargetID: "Target" + string(rune('A'+idx%26)) + "_" + string(rune('0'+idx/26)),
				},
				RuleID: "rule1",
				Evidence: []EvidenceEdge{
					{SourceID: "ev_src", EdgeType: "ev_type", TargetID: "ev_tgt"},
				},
				DerivedAt:  time.Now(),
				Confidence: 1.0,
			}
			errors[idx] = manager.Materialize(ctx, []InferenceResult{result})
		}(i)
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			t.Errorf("Materialize %d failed: %v", i, err)
		}
	}

	// Verify all records are present and properly formed
	edges, err := manager.GetAllMaterializedEdges(ctx)
	if err != nil {
		t.Fatalf("GetAllMaterializedEdges failed: %v", err)
	}
	if len(edges) != numEdges {
		t.Errorf("expected %d edges, got %d", numEdges, len(edges))
	}

	// Verify each edge has valid data
	for _, edge := range edges {
		if edge.ID == 0 {
			t.Error("found edge with zero ID")
		}
		if edge.EdgeKey == "" {
			t.Error("found edge with empty EdgeKey")
		}
		if edge.RuleID != "rule1" {
			t.Errorf("found edge with wrong rule_id: %s", edge.RuleID)
		}
		if len(edge.Evidence) == 0 {
			t.Error("found edge with empty evidence")
		}
	}
}

func TestMaterializationManager_MixedReadWriteContention(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Pre-populate some edges
	for i := 0; i < 10; i++ {
		result := createTestInferenceResult("rule1", "Pre"+string(rune('A'+i)), "calls", "Pre"+string(rune('B'+i)), "ev")
		if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
			t.Fatalf("pre-populate failed: %v", err)
		}
	}

	// Run concurrent reads and writes
	var wg sync.WaitGroup
	numOperations := 30

	// Writers
	writeErrors := make([]error, numOperations/3)
	for i := 0; i < numOperations/3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result := createTestInferenceResult("rule1", "Write"+string(rune('A'+idx)), "calls", "Write"+string(rune('B'+idx)), "ev")
			writeErrors[idx] = manager.Materialize(ctx, []InferenceResult{result})
		}(i)
	}

	// Readers (IsMaterialized)
	readErrors := make([]error, numOperations/3)
	for i := 0; i < numOperations/3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := "PreA|calls|PreB"
			_, readErrors[idx] = manager.IsMaterialized(ctx, key)
		}(i)
	}

	// Readers (GetMaterializedEdges)
	listErrors := make([]error, numOperations/3)
	for i := 0; i < numOperations/3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, listErrors[idx] = manager.GetMaterializedEdges(ctx, "rule1")
		}(i)
	}

	wg.Wait()

	// Check for errors
	for i, err := range writeErrors {
		if err != nil {
			t.Errorf("write %d failed: %v", i, err)
		}
	}
	for i, err := range readErrors {
		if err != nil {
			t.Errorf("read %d failed: %v", i, err)
		}
	}
	for i, err := range listErrors {
		if err != nil {
			t.Errorf("list %d failed: %v", i, err)
		}
	}
}

func TestMaterializationManager_ConcurrentDeleteAndRead(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")
	insertTestRule(t, db, "rule2")

	// Pre-populate edges for both rules
	for i := 0; i < 10; i++ {
		result1 := createTestInferenceResult("rule1", "R1_"+string(rune('A'+i)), "calls", "R1_"+string(rune('B'+i)), "ev")
		result2 := createTestInferenceResult("rule2", "R2_"+string(rune('A'+i)), "calls", "R2_"+string(rune('B'+i)), "ev")
		if err := manager.Materialize(ctx, []InferenceResult{result1, result2}); err != nil {
			t.Fatalf("pre-populate failed: %v", err)
		}
	}

	// Run concurrent deletes and reads
	var wg sync.WaitGroup

	// Delete rule1 edges
	wg.Add(1)
	var deleteErr error
	go func() {
		defer wg.Done()
		deleteErr = manager.DeleteMaterializedByRule(ctx, "rule1")
	}()

	// Read rule2 edges multiple times during delete
	readErrors := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, readErrors[idx] = manager.GetMaterializedEdges(ctx, "rule2")
		}(i)
	}

	wg.Wait()

	if deleteErr != nil {
		t.Errorf("delete failed: %v", deleteErr)
	}
	for i, err := range readErrors {
		if err != nil {
			t.Errorf("read %d failed: %v", i, err)
		}
	}

	// Verify rule1 edges are gone
	edges1, err := manager.GetMaterializedEdges(ctx, "rule1")
	if err != nil {
		t.Fatalf("GetMaterializedEdges rule1 failed: %v", err)
	}
	if len(edges1) != 0 {
		t.Errorf("expected 0 rule1 edges, got %d", len(edges1))
	}

	// Verify rule2 edges are intact
	edges2, err := manager.GetMaterializedEdges(ctx, "rule2")
	if err != nil {
		t.Fatalf("GetMaterializedEdges rule2 failed: %v", err)
	}
	if len(edges2) != 10 {
		t.Errorf("expected 10 rule2 edges, got %d", len(edges2))
	}
}

// =============================================================================
// W4P.20 Retry and Dead-Letter Queue Tests
// =============================================================================

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	if config.MaxRetries != 3 {
		t.Errorf("expected MaxRetries 3, got %d", config.MaxRetries)
	}
	if config.InitialBackoff != 100*time.Millisecond {
		t.Errorf("expected InitialBackoff 100ms, got %v", config.InitialBackoff)
	}
	if config.MaxBackoff != 5*time.Second {
		t.Errorf("expected MaxBackoff 5s, got %v", config.MaxBackoff)
	}
	if config.BackoffMultiple != 2.0 {
		t.Errorf("expected BackoffMultiple 2.0, got %f", config.BackoffMultiple)
	}
}

func TestMaterializationManager_SetRetryConfig(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)

	config := RetryConfig{
		MaxRetries:      5,
		InitialBackoff:  50 * time.Millisecond,
		MaxBackoff:      10 * time.Second,
		BackoffMultiple: 3.0,
	}
	manager.SetRetryConfig(config)

	if manager.retryConfig.MaxRetries != 5 {
		t.Errorf("expected MaxRetries 5, got %d", manager.retryConfig.MaxRetries)
	}
}

func TestMaterializationManager_SuccessfulMaterialization(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Configure fast retry for tests
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      3,
		InitialBackoff:  1 * time.Millisecond,
		MaxBackoff:      10 * time.Millisecond,
		BackoffMultiple: 2.0,
	})

	insertTestRule(t, db, "rule1")

	result := createTestInferenceResult("rule1", "A", "calls", "B", "ev1")
	err := manager.Materialize(ctx, []InferenceResult{result})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Verify edge was stored
	exists, err := manager.IsMaterialized(ctx, "A|calls|B")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if !exists {
		t.Error("edge should exist after successful materialization")
	}

	// Verify dead-letter queue is empty
	dlq := manager.GetDeadLetterQueue()
	if len(dlq) != 0 {
		t.Errorf("expected empty dead-letter queue, got %d entries", len(dlq))
	}
}

func TestMaterializationManager_FailureCallback(t *testing.T) {
	// Use foreign key enforcement to trigger failures
	db := setupMaterializationTestDBWithFK(t, true)
	defer db.Close()

	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Configure fast retry for tests
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      2,
		InitialBackoff:  1 * time.Millisecond,
		MaxBackoff:      5 * time.Millisecond,
		BackoffMultiple: 2.0,
	})

	// Track callback invocations
	var callbackMu sync.Mutex
	callbackCalls := make([]FailedEdge, 0)
	manager.SetFailureCallback(func(edge FailedEdge) {
		callbackMu.Lock()
		callbackCalls = append(callbackCalls, edge)
		callbackMu.Unlock()
	})

	// Don't insert the rule, so the foreign key constraint will fail
	// This simulates a database error

	result := createTestInferenceResult("nonexistent_rule", "A", "calls", "B", "ev1")
	err := manager.Materialize(ctx, []InferenceResult{result})

	// Should fail due to foreign key constraint
	if err == nil {
		t.Fatal("expected error from missing rule foreign key")
	}

	// Verify callback was called for the failed edge
	callbackMu.Lock()
	numCalls := len(callbackCalls)
	callbackMu.Unlock()

	if numCalls != 1 {
		t.Errorf("expected 1 callback call, got %d", numCalls)
	}

	// Verify dead-letter queue has the failed edge
	dlq := manager.GetDeadLetterQueue()
	if len(dlq) != 1 {
		t.Fatalf("expected 1 entry in dead-letter queue, got %d", len(dlq))
	}
	if dlq[0].EdgeKey != "A|calls|B" {
		t.Errorf("expected edge key A|calls|B, got %s", dlq[0].EdgeKey)
	}
	if dlq[0].RuleID != "nonexistent_rule" {
		t.Errorf("expected rule_id nonexistent_rule, got %s", dlq[0].RuleID)
	}
	if dlq[0].Attempts != 3 { // MaxRetries + 1
		t.Errorf("expected 3 attempts, got %d", dlq[0].Attempts)
	}
}

func TestMaterializationManager_DeadLetterQueue(t *testing.T) {
	// Use foreign key enforcement to trigger failures
	db := setupMaterializationTestDBWithFK(t, true)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Configure fast retry for tests
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      1,
		InitialBackoff:  1 * time.Millisecond,
		MaxBackoff:      5 * time.Millisecond,
		BackoffMultiple: 2.0,
	})

	// Attempt to materialize with missing rule (will fail due to FK constraint)
	result := createTestInferenceResult("missing_rule", "X", "calls", "Y", "ev1")
	_ = manager.Materialize(ctx, []InferenceResult{result})

	// Verify dead-letter queue contains the failed edge
	dlq := manager.GetDeadLetterQueue()
	if len(dlq) != 1 {
		t.Fatalf("expected 1 entry in dead-letter queue, got %d", len(dlq))
	}

	failed := dlq[0]
	if failed.EdgeKey != "X|calls|Y" {
		t.Errorf("expected edge key X|calls|Y, got %s", failed.EdgeKey)
	}
	if failed.RuleID != "missing_rule" {
		t.Errorf("expected rule_id missing_rule, got %s", failed.RuleID)
	}
	if failed.Error == "" {
		t.Error("expected error message, got empty string")
	}
	if failed.FailedAt.IsZero() {
		t.Error("expected FailedAt to be set")
	}
	if failed.Attempts != 2 { // MaxRetries + 1
		t.Errorf("expected 2 attempts, got %d", failed.Attempts)
	}

	// Test clearing the dead-letter queue
	manager.ClearDeadLetterQueue()
	dlq = manager.GetDeadLetterQueue()
	if len(dlq) != 0 {
		t.Errorf("expected empty dead-letter queue after clear, got %d entries", len(dlq))
	}
}

func TestMaterializationManager_RetryExhausted(t *testing.T) {
	// Use foreign key enforcement to trigger failures
	db := setupMaterializationTestDBWithFK(t, true)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Configure for 3 retries with fast backoff
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      3,
		InitialBackoff:  1 * time.Millisecond,
		MaxBackoff:      5 * time.Millisecond,
		BackoffMultiple: 2.0,
	})

	// Track callback invocations
	callbackCount := 0
	manager.SetFailureCallback(func(edge FailedEdge) {
		callbackCount++
	})

	// Attempt to materialize with missing rule (will exhaust retries due to FK constraint)
	result := createTestInferenceResult("missing_rule", "A", "calls", "B", "ev1")
	err := manager.Materialize(ctx, []InferenceResult{result})

	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}

	// Verify attempts count in dead-letter queue
	dlq := manager.GetDeadLetterQueue()
	if len(dlq) != 1 {
		t.Fatalf("expected 1 entry in dead-letter queue, got %d", len(dlq))
	}
	if dlq[0].Attempts != 4 { // MaxRetries + 1 = 4 total attempts
		t.Errorf("expected 4 attempts, got %d", dlq[0].Attempts)
	}

	// Verify callback was called exactly once (per failed edge)
	if callbackCount != 1 {
		t.Errorf("expected 1 callback call, got %d", callbackCount)
	}
}

func TestMaterializationManager_ExponentialBackoff(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)

	// Test nextBackoff calculation
	config := RetryConfig{
		InitialBackoff:  100 * time.Millisecond,
		MaxBackoff:      500 * time.Millisecond,
		BackoffMultiple: 2.0,
	}
	manager.SetRetryConfig(config)

	// First backoff: 100ms * 2 = 200ms
	next := manager.nextBackoff(100 * time.Millisecond)
	if next != 200*time.Millisecond {
		t.Errorf("expected 200ms, got %v", next)
	}

	// Second backoff: 200ms * 2 = 400ms
	next = manager.nextBackoff(200 * time.Millisecond)
	if next != 400*time.Millisecond {
		t.Errorf("expected 400ms, got %v", next)
	}

	// Third backoff: 400ms * 2 = 800ms, but capped at 500ms
	next = manager.nextBackoff(400 * time.Millisecond)
	if next != 500*time.Millisecond {
		t.Errorf("expected 500ms (capped), got %v", next)
	}

	// Already at max: stays at 500ms
	next = manager.nextBackoff(500 * time.Millisecond)
	if next != 500*time.Millisecond {
		t.Errorf("expected 500ms (at max), got %v", next)
	}
}

func TestMaterializationManager_BackoffTiming(t *testing.T) {
	// Use foreign key enforcement to trigger failures and force retries
	db := setupMaterializationTestDBWithFK(t, true)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Configure specific backoff timing
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      2,
		InitialBackoff:  50 * time.Millisecond,
		MaxBackoff:      200 * time.Millisecond,
		BackoffMultiple: 2.0,
	})

	// Record timing of materialization attempts
	start := time.Now()
	result := createTestInferenceResult("missing_rule", "A", "calls", "B", "ev1")
	_ = manager.Materialize(ctx, []InferenceResult{result})
	elapsed := time.Since(start)

	// With 2 retries:
	// - Attempt 0: immediate
	// - Attempt 1: 50ms backoff
	// - Attempt 2: 100ms backoff
	// Total expected delay: ~150ms (plus execution time)
	// We allow some tolerance for execution overhead
	minExpected := 140 * time.Millisecond
	maxExpected := 500 * time.Millisecond

	if elapsed < minExpected {
		t.Errorf("elapsed time %v is less than minimum expected %v", elapsed, minExpected)
	}
	if elapsed > maxExpected {
		t.Errorf("elapsed time %v is greater than maximum expected %v", elapsed, maxExpected)
	}
}

func TestMaterializationManager_ContextCancelledDuringBackoff(t *testing.T) {
	// Use foreign key enforcement to trigger failures and force retries
	db := setupMaterializationTestDBWithFK(t, true)
	defer db.Close()
	manager := NewMaterializationManager(db)

	// Configure long backoff to ensure context cancellation happens during backoff
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      5,
		InitialBackoff:  500 * time.Millisecond,
		MaxBackoff:      2 * time.Second,
		BackoffMultiple: 2.0,
	})

	// Create a context that will be cancelled quickly
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Don't insert the rule to force a retry due to FK constraint
	result := createTestInferenceResult("missing_rule", "A", "calls", "B", "ev1")
	err := manager.Materialize(ctx, []InferenceResult{result})

	// Should fail with context error, not retry exhaustion
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if err != context.DeadlineExceeded && err != context.Canceled {
		// The error might be wrapped, check the underlying cause
		if ctx.Err() == nil {
			t.Logf("got error (non-context): %v", err)
		}
	}

	// Dead-letter queue should be empty since we didn't exhaust retries
	dlq := manager.GetDeadLetterQueue()
	if len(dlq) != 0 {
		t.Errorf("expected empty dead-letter queue after context cancel, got %d entries", len(dlq))
	}
}

func TestMaterializationManager_MultipleEdgesPartialFailure(t *testing.T) {
	// Use foreign key enforcement to trigger failures
	db := setupMaterializationTestDBWithFK(t, true)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Configure fast retry
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      1,
		InitialBackoff:  1 * time.Millisecond,
		MaxBackoff:      5 * time.Millisecond,
		BackoffMultiple: 2.0,
	})

	// Try to materialize edges with missing rule (FK constraint will fail)
	// All edges in the batch will fail together
	results := []InferenceResult{
		createTestInferenceResult("missing_rule", "A", "calls", "B", "ev1"),
		createTestInferenceResult("missing_rule", "C", "calls", "D", "ev2"),
		createTestInferenceResult("missing_rule", "E", "calls", "F", "ev3"),
	}

	err := manager.Materialize(ctx, results)
	if err == nil {
		t.Fatal("expected error from missing rule")
	}

	// All edges should be in dead-letter queue
	dlq := manager.GetDeadLetterQueue()
	if len(dlq) != 3 {
		t.Fatalf("expected 3 entries in dead-letter queue, got %d", len(dlq))
	}

	// Verify all edges are captured
	edgeKeys := make(map[string]bool)
	for _, failed := range dlq {
		edgeKeys[failed.EdgeKey] = true
	}
	for _, expectedKey := range []string{"A|calls|B", "C|calls|D", "E|calls|F"} {
		if !edgeKeys[expectedKey] {
			t.Errorf("expected edge %s in dead-letter queue", expectedKey)
		}
	}
}

func TestMaterializationManager_ConcurrentDeadLetterAccess(t *testing.T) {
	// Use foreign key enforcement to trigger failures
	db := setupMaterializationTestDBWithFK(t, true)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Configure fast retry
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      1,
		InitialBackoff:  1 * time.Millisecond,
		MaxBackoff:      5 * time.Millisecond,
		BackoffMultiple: 2.0,
	})

	// Run concurrent operations that add to dead-letter queue
	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result := createTestInferenceResult("missing_rule", "Node"+string(rune('A'+idx)), "calls", "Node"+string(rune('B'+idx)), "ev")
			_ = manager.Materialize(ctx, []InferenceResult{result})
		}(i)
	}

	wg.Wait()

	// Verify all failures are captured (no race conditions)
	dlq := manager.GetDeadLetterQueue()
	if len(dlq) != numGoroutines {
		t.Errorf("expected %d entries in dead-letter queue, got %d", numGoroutines, len(dlq))
	}

	// Test concurrent clearing and reading
	var wg2 sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg2.Add(2)
		go func() {
			defer wg2.Done()
			_ = manager.GetDeadLetterQueue()
		}()
		go func() {
			defer wg2.Done()
			manager.ClearDeadLetterQueue()
		}()
	}
	wg2.Wait()

	// Should not panic due to race conditions
}

// =============================================================================
// W4P.35 Evidence Deserialization Validation Tests
// =============================================================================

func TestUnmarshalAndValidateEvidence_ValidJSON(t *testing.T) {
	testCases := []struct {
		name     string
		json     string
		expected []string
	}{
		{
			name:     "single evidence key",
			json:     `["A|calls|B"]`,
			expected: []string{"A|calls|B"},
		},
		{
			name:     "multiple evidence keys",
			json:     `["A|calls|B", "C|imports|D", "E|extends|F"]`,
			expected: []string{"A|calls|B", "C|imports|D", "E|extends|F"},
		},
		{
			name:     "empty array",
			json:     `[]`,
			expected: []string{},
		},
		{
			name:     "keys with special characters",
			json:     `["pkg/foo|imports|pkg/bar", "test:path|depends|other:path"]`,
			expected: []string{"pkg/foo|imports|pkg/bar", "test:path|depends|other:path"},
		},
		{
			name:     "keys with empty source or target allowed",
			json:     `["|predicate|target", "source|predicate|"]`,
			expected: []string{"|predicate|target", "source|predicate|"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := unmarshalAndValidateEvidence(tc.json)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != len(tc.expected) {
				t.Fatalf("expected %d elements, got %d", len(tc.expected), len(result))
			}
			for i, expected := range tc.expected {
				if result[i] != expected {
					t.Errorf("element %d: expected %q, got %q", i, expected, result[i])
				}
			}
		})
	}
}

func TestUnmarshalAndValidateEvidence_InvalidJSON(t *testing.T) {
	testCases := []struct {
		name        string
		json        string
		errContains string
	}{
		{
			name:        "empty string",
			json:        "",
			errContains: "empty evidence JSON",
		},
		{
			name:        "malformed JSON - missing bracket",
			json:        `["A|calls|B"`,
			errContains: "malformed JSON",
		},
		{
			name:        "malformed JSON - not an array",
			json:        `{"key": "value"}`,
			errContains: "malformed JSON",
		},
		{
			name:        "malformed JSON - wrong type in array",
			json:        `[123, 456]`,
			errContains: "malformed JSON",
		},
		{
			name:        "malformed JSON - null value",
			json:        `null`,
			errContains: "malformed JSON",
		},
		{
			name:        "malformed JSON - plain string",
			json:        `"A|calls|B"`,
			errContains: "malformed JSON",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := unmarshalAndValidateEvidence(tc.json)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, ErrInvalidEvidence) {
				t.Errorf("error should wrap ErrInvalidEvidence, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("error should contain %q, got: %v", tc.errContains, err)
			}
		})
	}
}

func TestUnmarshalAndValidateEvidence_InvalidFormat(t *testing.T) {
	testCases := []struct {
		name        string
		json        string
		errContains string
	}{
		{
			name:        "empty key in array",
			json:        `["A|calls|B", "", "C|imports|D"]`,
			errContains: "evidence[1] is empty",
		},
		{
			name:        "missing delimiters",
			json:        `["no-delimiters"]`,
			errContains: "evidence[0] has invalid format",
		},
		{
			name:        "too few parts",
			json:        `["A|B"]`,
			errContains: "evidence[0] has invalid format",
		},
		{
			name:        "too many parts",
			json:        `["A|B|C|D"]`,
			errContains: "evidence[0] has invalid format",
		},
		{
			name:        "empty predicate",
			json:        `["source||target"]`,
			errContains: "evidence[0] has empty predicate",
		},
		{
			name:        "second key has empty predicate",
			json:        `["A|calls|B", "X||Y"]`,
			errContains: "evidence[1] has empty predicate",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := unmarshalAndValidateEvidence(tc.json)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, ErrInvalidEvidence) {
				t.Errorf("error should wrap ErrInvalidEvidence, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("error should contain %q, got: %v", tc.errContains, err)
			}
		})
	}
}

func TestValidateEvidenceArray_Valid(t *testing.T) {
	testCases := []struct {
		name     string
		evidence []string
	}{
		{
			name:     "single valid key",
			evidence: []string{"A|calls|B"},
		},
		{
			name:     "multiple valid keys",
			evidence: []string{"A|calls|B", "C|imports|D"},
		},
		{
			name:     "empty array",
			evidence: []string{},
		},
		{
			name:     "keys with empty source allowed",
			evidence: []string{"|predicate|target"},
		},
		{
			name:     "keys with empty target allowed",
			evidence: []string{"source|predicate|"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEvidenceArray(tc.evidence)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEvidenceArray_Invalid(t *testing.T) {
	testCases := []struct {
		name        string
		evidence    []string
		errContains string
	}{
		{
			name:        "empty string in array",
			evidence:    []string{"A|calls|B", ""},
			errContains: "evidence[1] is empty",
		},
		{
			name:        "key without delimiters",
			evidence:    []string{"invalid"},
			errContains: "evidence[0] has invalid format",
		},
		{
			name:        "key with empty predicate",
			evidence:    []string{"source||target"},
			errContains: "evidence[0] has empty predicate",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEvidenceArray(tc.evidence)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, ErrInvalidEvidence) {
				t.Errorf("error should wrap ErrInvalidEvidence, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("error should contain %q, got: %v", tc.errContains, err)
			}
		})
	}
}

func TestScanSingleRecord_InvalidEvidenceJSON(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Insert a record with invalid evidence JSON directly
	_, err := db.ExecContext(ctx, `
		INSERT INTO materialized_edges (rule_id, edge_key, evidence_json, derived_at)
		VALUES (?, ?, ?, ?)
	`, "rule1", "A|calls|B", "not-valid-json", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to insert test record: %v", err)
	}

	manager := NewMaterializationManager(db)

	// Try to retrieve - should fail with validation error
	_, err = manager.GetMaterializedEdges(ctx, "rule1")
	if err == nil {
		t.Fatal("expected error from invalid evidence JSON")
	}
	if !errors.Is(err, ErrInvalidEvidence) {
		t.Errorf("error should wrap ErrInvalidEvidence, got: %v", err)
	}
}

func TestScanSingleRecord_EmptyEvidenceJSON(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Insert a record with empty evidence JSON
	_, err := db.ExecContext(ctx, `
		INSERT INTO materialized_edges (rule_id, edge_key, evidence_json, derived_at)
		VALUES (?, ?, ?, ?)
	`, "rule1", "A|calls|B", "", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to insert test record: %v", err)
	}

	manager := NewMaterializationManager(db)

	// Try to retrieve - should fail with validation error
	_, err = manager.GetMaterializedEdges(ctx, "rule1")
	if err == nil {
		t.Fatal("expected error from empty evidence JSON")
	}
	if !errors.Is(err, ErrInvalidEvidence) {
		t.Errorf("error should wrap ErrInvalidEvidence, got: %v", err)
	}
	if !strings.Contains(err.Error(), "empty evidence JSON") {
		t.Errorf("error should mention empty evidence JSON: %v", err)
	}
}

func TestScanSingleRecord_MalformedEvidenceKeys(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Insert a record with evidence containing malformed keys
	_, err := db.ExecContext(ctx, `
		INSERT INTO materialized_edges (rule_id, edge_key, evidence_json, derived_at)
		VALUES (?, ?, ?, ?)
	`, "rule1", "A|calls|B", `["invalid-key-format"]`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to insert test record: %v", err)
	}

	manager := NewMaterializationManager(db)

	// Try to retrieve - should fail with validation error
	_, err = manager.GetMaterializedEdges(ctx, "rule1")
	if err == nil {
		t.Fatal("expected error from malformed evidence key")
	}
	if !errors.Is(err, ErrInvalidEvidence) {
		t.Errorf("error should wrap ErrInvalidEvidence, got: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid format") {
		t.Errorf("error should mention invalid format: %v", err)
	}
}

func TestScanSingleRecord_ValidEvidencePassesValidation(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Use normal Materialize which stores valid evidence
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "A",
			EdgeType: "calls",
			TargetID: "B",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "X", EdgeType: "depends", TargetID: "Y"},
			{SourceID: "M", EdgeType: "imports", TargetID: "N"},
		},
		DerivedAt:  time.Now(),
		Confidence: 1.0,
	}

	if err := manager.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Retrieve - should pass validation
	edges, err := manager.GetMaterializedEdges(ctx, "rule1")
	if err != nil {
		t.Fatalf("GetMaterializedEdges failed: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if len(edges[0].Evidence) != 2 {
		t.Errorf("expected 2 evidence keys, got %d", len(edges[0].Evidence))
	}
}

func TestErrInvalidEvidence_Sentinel(t *testing.T) {
	// Test that ErrInvalidEvidence can be used as a sentinel error
	err := fmt.Errorf("%w: test error", ErrInvalidEvidence)
	if !errors.Is(err, ErrInvalidEvidence) {
		t.Error("wrapped error should match ErrInvalidEvidence sentinel")
	}
}

// =============================================================================
// W4P.39 Write Batching Tests
// =============================================================================

func TestDefaultBatchConfig(t *testing.T) {
	config := DefaultBatchConfig()

	if config.BatchSize != 100 {
		t.Errorf("expected BatchSize 100, got %d", config.BatchSize)
	}
}

func TestMaterializationManager_SetBatchConfig(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)

	config := BatchConfig{BatchSize: 50}
	manager.SetBatchConfig(config)

	if manager.batchConfig.BatchSize != 50 {
		t.Errorf("expected BatchSize 50, got %d", manager.batchConfig.BatchSize)
	}
}

func TestMaterializationManager_MaterializeBatched_Empty(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	results := manager.MaterializeBatched(ctx, []InferenceResult{})

	if results != nil {
		t.Errorf("expected nil results for empty input, got %d batches", len(results))
	}
}

func TestMaterializationManager_MaterializeBatched_SingleBatch(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create 5 edges with batch size of 10 (fits in one batch)
	manager.SetBatchConfig(BatchConfig{BatchSize: 10})

	inferenceResults := make([]InferenceResult, 5)
	for i := 0; i < 5; i++ {
		inferenceResults[i] = createTestInferenceResult(
			"rule1",
			fmt.Sprintf("A%d", i),
			"calls",
			fmt.Sprintf("B%d", i),
			fmt.Sprintf("ev%d", i),
		)
	}

	batchResults := manager.MaterializeBatched(ctx, inferenceResults)

	// Should have exactly 1 batch
	if len(batchResults) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batchResults))
	}

	// Batch should succeed
	if batchResults[0].Error != nil {
		t.Errorf("batch 0 should succeed, got error: %v", batchResults[0].Error)
	}

	// Should have 5 committed keys
	if len(batchResults[0].CommittedKeys) != 5 {
		t.Errorf("expected 5 committed keys, got %d", len(batchResults[0].CommittedKeys))
	}

	// Verify edges are in database
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("A%d|calls|B%d", i, i)
		exists, err := manager.IsMaterialized(ctx, key)
		if err != nil {
			t.Fatalf("IsMaterialized failed: %v", err)
		}
		if !exists {
			t.Errorf("edge %s should exist", key)
		}
	}
}

func TestMaterializationManager_MaterializeBatched_MultipleBatches(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create 25 edges with batch size of 10 (should create 3 batches)
	manager.SetBatchConfig(BatchConfig{BatchSize: 10})

	inferenceResults := make([]InferenceResult, 25)
	for i := 0; i < 25; i++ {
		inferenceResults[i] = createTestInferenceResult(
			"rule1",
			fmt.Sprintf("Node%d", i),
			"calls",
			fmt.Sprintf("Target%d", i),
			fmt.Sprintf("ev%d", i),
		)
	}

	batchResults := manager.MaterializeBatched(ctx, inferenceResults)

	// Should have 3 batches (10 + 10 + 5)
	if len(batchResults) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(batchResults))
	}

	// All batches should succeed
	successCount := CountSuccessfulBatches(batchResults)
	if successCount != 3 {
		t.Errorf("expected 3 successful batches, got %d", successCount)
	}

	// Verify committed keys count
	committedKeys := CollectAllCommittedKeys(batchResults)
	if len(committedKeys) != 25 {
		t.Errorf("expected 25 committed keys, got %d", len(committedKeys))
	}

	// Verify all edges exist in database
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM materialized_edges").Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 25 {
		t.Errorf("expected 25 edges in database, got %d", count)
	}
}

func TestMaterializationManager_MaterializeBatched_PartialFailure(t *testing.T) {
	// Use foreign key enforcement to trigger failures
	db := setupMaterializationTestDBWithFK(t, true)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	// Insert only rule1, not rule2
	insertTestRule(t, db, "rule1")

	// Configure fast retry
	manager.SetRetryConfig(RetryConfig{
		MaxRetries:      1,
		InitialBackoff:  1 * time.Millisecond,
		MaxBackoff:      5 * time.Millisecond,
		BackoffMultiple: 2.0,
	})

	// Batch size of 5: first batch uses rule1 (success), second uses rule2 (fail)
	manager.SetBatchConfig(BatchConfig{BatchSize: 5})

	inferenceResults := make([]InferenceResult, 10)
	// First 5 edges use rule1 (will succeed)
	for i := 0; i < 5; i++ {
		inferenceResults[i] = createTestInferenceResult(
			"rule1",
			fmt.Sprintf("A%d", i),
			"calls",
			fmt.Sprintf("B%d", i),
			"ev",
		)
	}
	// Next 5 edges use rule2 (will fail due to FK constraint)
	for i := 5; i < 10; i++ {
		inferenceResults[i] = createTestInferenceResult(
			"nonexistent_rule",
			fmt.Sprintf("X%d", i),
			"calls",
			fmt.Sprintf("Y%d", i),
			"ev",
		)
	}

	batchResults := manager.MaterializeBatched(ctx, inferenceResults)

	// Should have 2 batches
	if len(batchResults) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(batchResults))
	}

	// First batch should succeed
	if batchResults[0].Error != nil {
		t.Errorf("batch 0 should succeed, got error: %v", batchResults[0].Error)
	}
	if len(batchResults[0].CommittedKeys) != 5 {
		t.Errorf("batch 0: expected 5 committed keys, got %d", len(batchResults[0].CommittedKeys))
	}

	// Second batch should fail
	if batchResults[1].Error == nil {
		t.Error("batch 1 should fail due to FK constraint")
	}
	if len(batchResults[1].FailedKeys) != 5 {
		t.Errorf("batch 1: expected 5 failed keys, got %d", len(batchResults[1].FailedKeys))
	}

	// Verify partial success: only first 5 edges in database
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM materialized_edges").Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 edges in database (partial success), got %d", count)
	}
}

func TestMaterializationManager_MaterializeBatched_ReducesTransactions(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create 100 edges
	numEdges := 100
	inferenceResults := make([]InferenceResult, numEdges)
	for i := 0; i < numEdges; i++ {
		inferenceResults[i] = createTestInferenceResult(
			"rule1",
			fmt.Sprintf("Source%d", i),
			"calls",
			fmt.Sprintf("Target%d", i),
			fmt.Sprintf("ev%d", i),
		)
	}

	// With batch size of 25, should create 4 transactions instead of 100
	manager.SetBatchConfig(BatchConfig{BatchSize: 25})

	batchResults := manager.MaterializeBatched(ctx, inferenceResults)

	// Should have 4 batches (4 transactions vs 100 individual calls)
	if len(batchResults) != 4 {
		t.Errorf("expected 4 batches, got %d", len(batchResults))
	}

	// All should succeed
	for i, br := range batchResults {
		if br.Error != nil {
			t.Errorf("batch %d failed: %v", i, br.Error)
		}
	}

	// Verify all edges committed
	committedKeys := CollectAllCommittedKeys(batchResults)
	if len(committedKeys) != numEdges {
		t.Errorf("expected %d committed keys, got %d", numEdges, len(committedKeys))
	}

	// Verify database has all edges
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM materialized_edges").Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != numEdges {
		t.Errorf("expected %d edges in database, got %d", numEdges, count)
	}
}

func TestMaterializationManager_MaterializeBatched_ConcurrentBatches(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	manager.SetBatchConfig(BatchConfig{BatchSize: 10})

	// Run multiple batched materializations concurrently
	var wg sync.WaitGroup
	numGoroutines := 5
	edgesPerGoroutine := 20

	allResults := make([][]BatchResult, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineIdx int) {
			defer wg.Done()

			inferenceResults := make([]InferenceResult, edgesPerGoroutine)
			for i := 0; i < edgesPerGoroutine; i++ {
				inferenceResults[i] = createTestInferenceResult(
					"rule1",
					fmt.Sprintf("G%d_S%d", goroutineIdx, i),
					"calls",
					fmt.Sprintf("G%d_T%d", goroutineIdx, i),
					"ev",
				)
			}

			allResults[goroutineIdx] = manager.MaterializeBatched(ctx, inferenceResults)
		}(g)
	}

	wg.Wait()

	// Verify all goroutines succeeded
	totalCommitted := 0
	for g, results := range allResults {
		for _, br := range results {
			if br.Error != nil {
				t.Errorf("goroutine %d: batch %d failed: %v", g, br.BatchIndex, br.Error)
			}
			totalCommitted += len(br.CommittedKeys)
		}
	}

	expectedTotal := numGoroutines * edgesPerGoroutine
	if totalCommitted != expectedTotal {
		t.Errorf("expected %d total committed keys, got %d", expectedTotal, totalCommitted)
	}

	// Verify database count
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM materialized_edges").Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != expectedTotal {
		t.Errorf("expected %d edges in database, got %d", expectedTotal, count)
	}
}

func TestSplitIntoBatches(t *testing.T) {
	manager := &MaterializationManager{}

	testCases := []struct {
		name          string
		numItems      int
		batchSize     int
		expectedCount int
		expectedSizes []int
	}{
		{
			name:          "empty",
			numItems:      0,
			batchSize:     10,
			expectedCount: 0,
			expectedSizes: nil,
		},
		{
			name:          "single batch exact",
			numItems:      10,
			batchSize:     10,
			expectedCount: 1,
			expectedSizes: []int{10},
		},
		{
			name:          "single batch partial",
			numItems:      5,
			batchSize:     10,
			expectedCount: 1,
			expectedSizes: []int{5},
		},
		{
			name:          "multiple batches exact",
			numItems:      30,
			batchSize:     10,
			expectedCount: 3,
			expectedSizes: []int{10, 10, 10},
		},
		{
			name:          "multiple batches with remainder",
			numItems:      25,
			batchSize:     10,
			expectedCount: 3,
			expectedSizes: []int{10, 10, 5},
		},
		{
			name:          "one item per batch",
			numItems:      5,
			batchSize:     1,
			expectedCount: 5,
			expectedSizes: []int{1, 1, 1, 1, 1},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			prepared := make([]preparedResult, tc.numItems)
			for i := 0; i < tc.numItems; i++ {
				prepared[i] = preparedResult{edgeKey: fmt.Sprintf("key%d", i)}
			}

			batches := manager.splitIntoBatches(prepared, tc.batchSize)

			if len(batches) != tc.expectedCount {
				t.Errorf("expected %d batches, got %d", tc.expectedCount, len(batches))
				return
			}

			for i, expectedSize := range tc.expectedSizes {
				if len(batches[i]) != expectedSize {
					t.Errorf("batch %d: expected size %d, got %d", i, expectedSize, len(batches[i]))
				}
			}
		})
	}
}

func TestCountSuccessfulBatches(t *testing.T) {
	testCases := []struct {
		name     string
		results  []BatchResult
		expected int
	}{
		{
			name:     "empty",
			results:  []BatchResult{},
			expected: 0,
		},
		{
			name: "all success",
			results: []BatchResult{
				{BatchIndex: 0, CommittedKeys: []string{"a"}},
				{BatchIndex: 1, CommittedKeys: []string{"b"}},
			},
			expected: 2,
		},
		{
			name: "all failed",
			results: []BatchResult{
				{BatchIndex: 0, FailedKeys: []string{"a"}, Error: errors.New("fail")},
				{BatchIndex: 1, FailedKeys: []string{"b"}, Error: errors.New("fail")},
			},
			expected: 0,
		},
		{
			name: "mixed",
			results: []BatchResult{
				{BatchIndex: 0, CommittedKeys: []string{"a"}},
				{BatchIndex: 1, FailedKeys: []string{"b"}, Error: errors.New("fail")},
				{BatchIndex: 2, CommittedKeys: []string{"c"}},
			},
			expected: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := CountSuccessfulBatches(tc.results)
			if result != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}

func TestCollectAllFailedKeys(t *testing.T) {
	results := []BatchResult{
		{BatchIndex: 0, FailedKeys: []string{"a", "b"}, Error: errors.New("fail")},
		{BatchIndex: 1, CommittedKeys: []string{"c"}},
		{BatchIndex: 2, FailedKeys: []string{"d"}, Error: errors.New("fail")},
	}

	failed := CollectAllFailedKeys(results)

	expected := []string{"a", "b", "d"}
	if len(failed) != len(expected) {
		t.Fatalf("expected %d failed keys, got %d", len(expected), len(failed))
	}

	for i, key := range expected {
		if failed[i] != key {
			t.Errorf("failed[%d]: expected %s, got %s", i, key, failed[i])
		}
	}
}

func TestCollectAllCommittedKeys(t *testing.T) {
	results := []BatchResult{
		{BatchIndex: 0, CommittedKeys: []string{"a", "b"}},
		{BatchIndex: 1, FailedKeys: []string{"c"}, Error: errors.New("fail")},
		{BatchIndex: 2, CommittedKeys: []string{"d", "e", "f"}},
	}

	committed := CollectAllCommittedKeys(results)

	expected := []string{"a", "b", "d", "e", "f"}
	if len(committed) != len(expected) {
		t.Fatalf("expected %d committed keys, got %d", len(expected), len(committed))
	}

	for i, key := range expected {
		if committed[i] != key {
			t.Errorf("committed[%d]: expected %s, got %s", i, key, committed[i])
		}
	}
}

func TestMaterializationManager_MaterializeBatched_DefaultBatchSize(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create 150 edges (default batch size is 100)
	inferenceResults := make([]InferenceResult, 150)
	for i := 0; i < 150; i++ {
		inferenceResults[i] = createTestInferenceResult(
			"rule1",
			fmt.Sprintf("S%d", i),
			"calls",
			fmt.Sprintf("T%d", i),
			"ev",
		)
	}

	batchResults := manager.MaterializeBatched(ctx, inferenceResults)

	// With default batch size of 100: should have 2 batches (100 + 50)
	if len(batchResults) != 2 {
		t.Errorf("expected 2 batches with default size, got %d", len(batchResults))
	}

	// Verify all succeeded
	for i, br := range batchResults {
		if br.Error != nil {
			t.Errorf("batch %d failed: %v", i, br.Error)
		}
	}
}

func TestMaterializationManager_MaterializeBatched_ZeroBatchSize(t *testing.T) {
	db := setupMaterializationTestDB(t)
	defer db.Close()
	manager := NewMaterializationManager(db)
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Set invalid batch size of 0 (should fall back to 100)
	manager.SetBatchConfig(BatchConfig{BatchSize: 0})

	inferenceResults := make([]InferenceResult, 150)
	for i := 0; i < 150; i++ {
		inferenceResults[i] = createTestInferenceResult(
			"rule1",
			fmt.Sprintf("S%d", i),
			"calls",
			fmt.Sprintf("T%d", i),
			"ev",
		)
	}

	batchResults := manager.MaterializeBatched(ctx, inferenceResults)

	// Should fall back to default of 100: 2 batches (100 + 50)
	if len(batchResults) != 2 {
		t.Errorf("expected 2 batches with fallback size, got %d", len(batchResults))
	}
}
