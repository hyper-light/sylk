package inference

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Test Helpers
// =============================================================================

// mockEdgeProvider implements EdgeProvider for testing.
type mockEdgeProvider struct {
	edges []Edge
}

func (m *mockEdgeProvider) GetAllEdges(ctx context.Context) ([]Edge, error) {
	return m.edges, nil
}

// setupInvalidationTestDB creates an in-memory SQLite database with all required tables.
func setupInvalidationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create inference_rules table
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

	// Create extended materialized_edges table
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

// createInvalidationTestSetup creates all components needed for invalidation tests.
func createInvalidationTestSetup(t *testing.T) (
	*sql.DB,
	*MaterializationManager,
	*RuleStore,
	*ForwardChainer,
	*mockEdgeProvider,
	*InvalidationManager,
) {
	t.Helper()
	db := setupInvalidationTestDB(t)

	materializer := NewMaterializationManager(db)
	ruleStore := NewRuleStore(db)
	evaluator := NewRuleEvaluator()
	forwardChainer := NewForwardChainer(evaluator, 10)
	edgeProvider := &mockEdgeProvider{edges: []Edge{}}

	invalidationManager := NewInvalidationManager(materializer, forwardChainer, ruleStore, edgeProvider)

	return db, materializer, ruleStore, forwardChainer, edgeProvider, invalidationManager
}

// insertInvalidationTestRule inserts a transitive rule for testing.
func insertInvalidationTestRule(t *testing.T, db *sql.DB, ruleID string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, '?x', 'indirectly_calls', '?z', '[{"subject":"?x","predicate":"calls","object":"?y"},{"subject":"?y","predicate":"calls","object":"?z"}]', 0, 1, ?)
	`, ruleID, "Transitive Call Rule", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to insert test rule: %v", err)
	}
}

// =============================================================================
// ChangeType Tests
// =============================================================================

func TestChangeType_String(t *testing.T) {
	testCases := []struct {
		changeType ChangeType
		expected   string
	}{
		{ChangeTypeAdded, "added"},
		{ChangeTypeRemoved, "removed"},
		{ChangeTypeModified, "modified"},
		{ChangeType(99), "unknown"},
	}

	for _, tc := range testCases {
		result := tc.changeType.String()
		if result != tc.expected {
			t.Errorf("ChangeType(%d).String() = %s, want %s", tc.changeType, result, tc.expected)
		}
	}
}

// =============================================================================
// NewInvalidationManager Tests
// =============================================================================

func TestNewInvalidationManager(t *testing.T) {
	db := setupInvalidationTestDB(t)
	defer db.Close()

	materializer := NewMaterializationManager(db)
	evaluator := NewRuleEvaluator()
	forwardChainer := NewForwardChainer(evaluator, 10)
	ruleStore := NewRuleStore(db)
	edgeProvider := &mockEdgeProvider{}

	manager := NewInvalidationManager(materializer, forwardChainer, ruleStore, edgeProvider)

	if manager == nil {
		t.Fatal("NewInvalidationManager returned nil")
	}
	if manager.materializer != materializer {
		t.Error("materializer not set correctly")
	}
	if manager.forwardChainer != forwardChainer {
		t.Error("forwardChainer not set correctly")
	}
	if manager.ruleStore != ruleStore {
		t.Error("ruleStore not set correctly")
	}
	if manager.edgeProvider != edgeProvider {
		t.Error("edgeProvider not set correctly")
	}
}

func TestNewInvalidationManager_NilComponents(t *testing.T) {
	// Should not panic with nil components
	manager := NewInvalidationManager(nil, nil, nil, nil)
	if manager == nil {
		t.Fatal("NewInvalidationManager returned nil with nil components")
	}
}

// =============================================================================
// OnEdgeChange Tests
// =============================================================================

func TestInvalidationManager_OnEdgeChange_Added(t *testing.T) {
	db, materializer, _, _, edgeProvider, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertInvalidationTestRule(t, db, "rule1")

	// Set up edges that will trigger the transitive rule
	edgeProvider.edges = []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
	}

	// Add a new edge (the trigger)
	newEdge := Edge{Source: "A", Predicate: "calls", Target: "B"}
	err := manager.OnEdgeChange(ctx, newEdge, ChangeTypeAdded)
	if err != nil {
		t.Fatalf("OnEdgeChange failed: %v", err)
	}

	// Check that inference produced a materialized edge
	edges, err := materializer.GetAllMaterializedEdges(ctx)
	if err != nil {
		t.Fatalf("GetAllMaterializedEdges failed: %v", err)
	}

	// Should have derived A indirectly_calls C
	if len(edges) != 1 {
		t.Errorf("expected 1 materialized edge, got %d", len(edges))
	}
}

func TestInvalidationManager_OnEdgeChange_Removed(t *testing.T) {
	db, materializer, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Manually insert a materialized edge with evidence
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "A",
			EdgeType: "indirectly_calls",
			TargetID: "C",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "A", EdgeType: "calls", TargetID: "B"},
			{SourceID: "B", EdgeType: "calls", TargetID: "C"},
		},
		DerivedAt:  time.Now(),
		Confidence: 1.0,
	}
	if err := materializer.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Remove an edge that was evidence
	removedEdge := Edge{Source: "A", Predicate: "calls", Target: "B"}
	err := manager.OnEdgeChange(ctx, removedEdge, ChangeTypeRemoved)
	if err != nil {
		t.Fatalf("OnEdgeChange failed: %v", err)
	}

	// Check that the dependent edge was invalidated
	exists, err := materializer.IsMaterialized(ctx, "A|indirectly_calls|C")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if exists {
		t.Error("derived edge should have been invalidated")
	}
}

func TestInvalidationManager_OnEdgeChange_Modified(t *testing.T) {
	db, materializer, _, _, edgeProvider, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertInvalidationTestRule(t, db, "rule1")

	// Set up initial materialized edge with evidence
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "A",
			EdgeType: "indirectly_calls",
			TargetID: "C",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "A", EdgeType: "calls", TargetID: "B"},
			{SourceID: "B", EdgeType: "calls", TargetID: "C"},
		},
		DerivedAt:  time.Now(),
		Confidence: 1.0,
	}
	if err := materializer.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Set up edges for re-materialization
	edgeProvider.edges = []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "D"}, // Changed: C -> D
	}

	// Modify the edge (B->C becomes B->D)
	modifiedEdge := Edge{Source: "B", Predicate: "calls", Target: "C"}
	err := manager.OnEdgeChange(ctx, modifiedEdge, ChangeTypeModified)
	if err != nil {
		t.Fatalf("OnEdgeChange failed: %v", err)
	}

	// Old derived edge should be invalidated
	exists, err := materializer.IsMaterialized(ctx, "A|indirectly_calls|C")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if exists {
		t.Error("old derived edge should have been invalidated")
	}
}

func TestInvalidationManager_OnEdgeChange_UnknownType(t *testing.T) {
	db, _, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	edge := Edge{Source: "A", Predicate: "calls", Target: "B"}
	err := manager.OnEdgeChange(ctx, edge, ChangeType(99))
	if err == nil {
		t.Error("expected error for unknown change type")
	}
}

// =============================================================================
// InvalidateDependents Tests
// =============================================================================

func TestInvalidationManager_InvalidateDependents_NoMaterializer(t *testing.T) {
	manager := NewInvalidationManager(nil, nil, nil, nil)
	ctx := context.Background()

	// Should not error with nil materializer
	err := manager.InvalidateDependents(ctx, "any|edge|key")
	if err != nil {
		t.Fatalf("InvalidateDependents failed with nil materializer: %v", err)
	}
}

func TestInvalidationManager_InvalidateDependents_NoDependents(t *testing.T) {
	db, materializer, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Materialize an edge with different evidence
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
	if err := materializer.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Invalidate with different edge key - no dependents
	err := manager.InvalidateDependents(ctx, "P|unrelated|Q")
	if err != nil {
		t.Fatalf("InvalidateDependents failed: %v", err)
	}

	// Original edge should still exist
	exists, err := materializer.IsMaterialized(ctx, "A|calls|B")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if !exists {
		t.Error("unrelated edge should not have been invalidated")
	}
}

func TestInvalidationManager_InvalidateDependents_CascadeInvalidation(t *testing.T) {
	db, materializer, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create a chain of dependencies: A -> B -> C
	// Edge B depends on evidence A
	// Edge C depends on evidence B

	resultA := InferenceResult{
		DerivedEdge: DerivedEdge{SourceID: "X", EdgeType: "derived", TargetID: "Y"},
		RuleID:      "rule1",
		Evidence:    []EvidenceEdge{{SourceID: "base", EdgeType: "ev", TargetID: "edge"}},
		DerivedAt:   time.Now(),
		Confidence:  1.0,
	}
	resultB := InferenceResult{
		DerivedEdge: DerivedEdge{SourceID: "Y", EdgeType: "derived", TargetID: "Z"},
		RuleID:      "rule1",
		Evidence:    []EvidenceEdge{{SourceID: "X", EdgeType: "derived", TargetID: "Y"}}, // Depends on A
		DerivedAt:   time.Now(),
		Confidence:  1.0,
	}

	if err := materializer.Materialize(ctx, []InferenceResult{resultA}); err != nil {
		t.Fatalf("Materialize A failed: %v", err)
	}
	if err := materializer.Materialize(ctx, []InferenceResult{resultB}); err != nil {
		t.Fatalf("Materialize B failed: %v", err)
	}

	// Invalidate the base evidence edge
	err := manager.InvalidateDependents(ctx, "base|ev|edge")
	if err != nil {
		t.Fatalf("InvalidateDependents failed: %v", err)
	}

	// A should be invalidated
	exists, err := materializer.IsMaterialized(ctx, "X|derived|Y")
	if err != nil {
		t.Fatalf("IsMaterialized A failed: %v", err)
	}
	if exists {
		t.Error("edge A should have been invalidated")
	}

	// B should be cascade-invalidated
	exists, err = materializer.IsMaterialized(ctx, "Y|derived|Z")
	if err != nil {
		t.Fatalf("IsMaterialized B failed: %v", err)
	}
	if exists {
		t.Error("edge B should have been cascade-invalidated")
	}
}

// =============================================================================
// Rematerialize Tests
// =============================================================================

func TestInvalidationManager_Rematerialize_NoComponents(t *testing.T) {
	manager := NewInvalidationManager(nil, nil, nil, nil)
	ctx := context.Background()

	// Should not error with nil components
	err := manager.Rematerialize(ctx, []string{"rule1"})
	if err != nil {
		t.Fatalf("Rematerialize failed with nil components: %v", err)
	}
}

func TestInvalidationManager_Rematerialize_EmptyRuleIDs(t *testing.T) {
	db, _, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	// Should not error with empty rule IDs
	err := manager.Rematerialize(ctx, []string{})
	if err != nil {
		t.Fatalf("Rematerialize failed with empty rule IDs: %v", err)
	}
}

func TestInvalidationManager_Rematerialize_WithEdges(t *testing.T) {
	db, materializer, _, _, edgeProvider, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertInvalidationTestRule(t, db, "rule1")

	// Set up edges for re-materialization
	edgeProvider.edges = []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
	}

	// Rematerialize rule1
	err := manager.Rematerialize(ctx, []string{"rule1"})
	if err != nil {
		t.Fatalf("Rematerialize failed: %v", err)
	}

	// Should have materialized A indirectly_calls C
	edges, err := materializer.GetAllMaterializedEdges(ctx)
	if err != nil {
		t.Fatalf("GetAllMaterializedEdges failed: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 materialized edge, got %d", len(edges))
	}
}

// =============================================================================
// InvalidateByRule Tests
// =============================================================================

func TestInvalidationManager_InvalidateByRule_NoMaterializer(t *testing.T) {
	manager := NewInvalidationManager(nil, nil, nil, nil)
	ctx := context.Background()

	// Should not error with nil materializer
	err := manager.InvalidateByRule(ctx, "rule1")
	if err != nil {
		t.Fatalf("InvalidateByRule failed with nil materializer: %v", err)
	}
}

func TestInvalidationManager_InvalidateByRule_WithEdges(t *testing.T) {
	db, materializer, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")
	insertTestRule(t, db, "rule2")

	// Materialize edges from different rules
	results := []InferenceResult{
		{
			DerivedEdge: DerivedEdge{SourceID: "A", EdgeType: "calls", TargetID: "B"},
			RuleID:      "rule1",
			Evidence:    []EvidenceEdge{{SourceID: "X", EdgeType: "ev", TargetID: "Y"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
		{
			DerivedEdge: DerivedEdge{SourceID: "P", EdgeType: "calls", TargetID: "Q"},
			RuleID:      "rule2",
			Evidence:    []EvidenceEdge{{SourceID: "M", EdgeType: "ev", TargetID: "N"}},
			DerivedAt:   time.Now(),
			Confidence:  1.0,
		},
	}

	for _, result := range results {
		if err := materializer.Materialize(ctx, []InferenceResult{result}); err != nil {
			t.Fatalf("Materialize failed: %v", err)
		}
	}

	// Invalidate rule1
	err := manager.InvalidateByRule(ctx, "rule1")
	if err != nil {
		t.Fatalf("InvalidateByRule failed: %v", err)
	}

	// rule1 edge should be invalidated
	exists, err := materializer.IsMaterialized(ctx, "A|calls|B")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if exists {
		t.Error("rule1 edge should have been invalidated")
	}

	// rule2 edge should still exist
	exists, err = materializer.IsMaterialized(ctx, "P|calls|Q")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if !exists {
		t.Error("rule2 edge should still exist")
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestInvalidationManager_FullInferenceAndInvalidationCycle(t *testing.T) {
	db, materializer, _, _, edgeProvider, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertInvalidationTestRule(t, db, "transitive")

	// Step 1: Add edges that trigger inference
	edgeProvider.edges = []Edge{
		{Source: "func_A", Predicate: "calls", Target: "func_B"},
		{Source: "func_B", Predicate: "calls", Target: "func_C"},
	}

	// Trigger inference with added edge
	err := manager.OnEdgeChange(ctx, edgeProvider.edges[0], ChangeTypeAdded)
	if err != nil {
		t.Fatalf("OnEdgeChange (add) failed: %v", err)
	}

	// Verify inference result
	exists, err := materializer.IsMaterialized(ctx, "func_A|indirectly_calls|func_C")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if !exists {
		t.Error("expected inferred edge after adding base edges")
	}

	// Step 2: Remove one of the base edges
	removedEdge := Edge{Source: "func_B", Predicate: "calls", Target: "func_C"}

	// First, manually add the derived edge with proper evidence
	// (since the automatic materialization might not have the exact evidence)
	if err := materializer.DeleteMaterializedByRule(ctx, "transitive"); err != nil {
		t.Fatalf("DeleteMaterializedByRule failed: %v", err)
	}
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "func_A",
			EdgeType: "indirectly_calls",
			TargetID: "func_C",
		},
		RuleID: "transitive",
		Evidence: []EvidenceEdge{
			{SourceID: "func_A", EdgeType: "calls", TargetID: "func_B"},
			{SourceID: "func_B", EdgeType: "calls", TargetID: "func_C"},
		},
		DerivedAt:  time.Now(),
		Confidence: 1.0,
	}
	if err := materializer.Materialize(ctx, []InferenceResult{result}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// Now remove the edge
	err = manager.OnEdgeChange(ctx, removedEdge, ChangeTypeRemoved)
	if err != nil {
		t.Fatalf("OnEdgeChange (remove) failed: %v", err)
	}

	// Verify the derived edge was invalidated
	exists, err = materializer.IsMaterialized(ctx, "func_A|indirectly_calls|func_C")
	if err != nil {
		t.Fatalf("IsMaterialized failed: %v", err)
	}
	if exists {
		t.Error("derived edge should have been invalidated after removing base edge")
	}
}

func TestInvalidationManager_HandleEdgeAdded_NoEdgeProvider(t *testing.T) {
	db := setupInvalidationTestDB(t)
	defer db.Close()

	materializer := NewMaterializationManager(db)
	ruleStore := NewRuleStore(db)
	evaluator := NewRuleEvaluator()
	forwardChainer := NewForwardChainer(evaluator, 10)

	// Create manager without edge provider
	manager := NewInvalidationManager(materializer, forwardChainer, ruleStore, nil)
	ctx := context.Background()

	insertInvalidationTestRule(t, db, "rule1")

	// Should not error, but won't produce results without edge provider
	edge := Edge{Source: "A", Predicate: "calls", Target: "B"}
	err := manager.OnEdgeChange(ctx, edge, ChangeTypeAdded)
	if err != nil {
		t.Fatalf("OnEdgeChange failed without edge provider: %v", err)
	}
}

// =============================================================================
// W3H.2 Cycle Detection Tests
// =============================================================================

func TestInvalidationManager_InvalidateDependents_CyclicDependencies(t *testing.T) {
	db, materializer, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create a cyclic dependency: A -> B -> C -> A
	// Edge A depends on evidence from C
	// Edge B depends on evidence from A
	// Edge C depends on evidence from B
	resultA := InferenceResult{
		DerivedEdge: DerivedEdge{SourceID: "A", EdgeType: "derived", TargetID: "X"},
		RuleID:      "rule1",
		Evidence:    []EvidenceEdge{{SourceID: "C", EdgeType: "derived", TargetID: "Z"}},
		DerivedAt:   time.Now(),
		Confidence:  1.0,
	}
	resultB := InferenceResult{
		DerivedEdge: DerivedEdge{SourceID: "B", EdgeType: "derived", TargetID: "Y"},
		RuleID:      "rule1",
		Evidence:    []EvidenceEdge{{SourceID: "A", EdgeType: "derived", TargetID: "X"}},
		DerivedAt:   time.Now(),
		Confidence:  1.0,
	}
	resultC := InferenceResult{
		DerivedEdge: DerivedEdge{SourceID: "C", EdgeType: "derived", TargetID: "Z"},
		RuleID:      "rule1",
		Evidence:    []EvidenceEdge{{SourceID: "B", EdgeType: "derived", TargetID: "Y"}},
		DerivedAt:   time.Now(),
		Confidence:  1.0,
	}

	if err := materializer.Materialize(ctx, []InferenceResult{resultA}); err != nil {
		t.Fatalf("Materialize A failed: %v", err)
	}
	if err := materializer.Materialize(ctx, []InferenceResult{resultB}); err != nil {
		t.Fatalf("Materialize B failed: %v", err)
	}
	if err := materializer.Materialize(ctx, []InferenceResult{resultC}); err != nil {
		t.Fatalf("Materialize C failed: %v", err)
	}

	// This should NOT cause infinite recursion due to cycle detection
	// Invalidating A should cascade to B, B to C, but C should not re-trigger A
	err := manager.InvalidateDependents(ctx, "A|derived|X")
	if err != nil {
		t.Fatalf("InvalidateDependents failed (should handle cycle): %v", err)
	}

	// Edge B should be invalidated (it depended on A)
	exists, err := materializer.IsMaterialized(ctx, "B|derived|Y")
	if err != nil {
		t.Fatalf("IsMaterialized B failed: %v", err)
	}
	if exists {
		t.Error("edge B should have been invalidated")
	}

	// Edge C should be invalidated (it depended on B)
	exists, err = materializer.IsMaterialized(ctx, "C|derived|Z")
	if err != nil {
		t.Fatalf("IsMaterialized C failed: %v", err)
	}
	if exists {
		t.Error("edge C should have been invalidated")
	}
}

func TestInvalidationManager_InvalidateDependents_SelfReferentialCycle(t *testing.T) {
	db, materializer, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create a self-referential edge: A depends on itself
	resultA := InferenceResult{
		DerivedEdge: DerivedEdge{SourceID: "Self", EdgeType: "refers", TargetID: "Self"},
		RuleID:      "rule1",
		Evidence:    []EvidenceEdge{{SourceID: "Self", EdgeType: "refers", TargetID: "Self"}},
		DerivedAt:   time.Now(),
		Confidence:  1.0,
	}

	if err := materializer.Materialize(ctx, []InferenceResult{resultA}); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// This should NOT cause infinite recursion
	err := manager.InvalidateDependents(ctx, "Self|refers|Self")
	if err != nil {
		t.Fatalf("InvalidateDependents failed (should handle self-reference): %v", err)
	}
}

func TestInvalidationManager_InvalidateDependents_DeepRecursion(t *testing.T) {
	db, materializer, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create a deep chain of dependencies: E0 -> E1 -> E2 -> ... -> E99
	const depth = 100
	for i := 0; i < depth; i++ {
		var evidence []EvidenceEdge
		if i > 0 {
			evidence = []EvidenceEdge{{
				SourceID: fmt.Sprintf("Node%d", i-1),
				EdgeType: "chain",
				TargetID: fmt.Sprintf("Target%d", i-1),
			}}
		} else {
			evidence = []EvidenceEdge{{SourceID: "base", EdgeType: "ev", TargetID: "edge"}}
		}

		result := InferenceResult{
			DerivedEdge: DerivedEdge{
				SourceID: fmt.Sprintf("Node%d", i),
				EdgeType: "chain",
				TargetID: fmt.Sprintf("Target%d", i),
			},
			RuleID:     "rule1",
			Evidence:   evidence,
			DerivedAt:  time.Now(),
			Confidence: 1.0,
		}

		if err := materializer.Materialize(ctx, []InferenceResult{result}); err != nil {
			t.Fatalf("Materialize edge %d failed: %v", i, err)
		}
	}

	// Invalidate the base edge - should cascade through all 100 edges without stack overflow
	err := manager.InvalidateDependents(ctx, "base|ev|edge")
	if err != nil {
		t.Fatalf("InvalidateDependents failed on deep recursion: %v", err)
	}

	// Verify first edge was invalidated
	exists, err := materializer.IsMaterialized(ctx, "Node0|chain|Target0")
	if err != nil {
		t.Fatalf("IsMaterialized Node0 failed: %v", err)
	}
	if exists {
		t.Error("first edge should have been invalidated")
	}

	// Verify last edge was invalidated
	exists, err = materializer.IsMaterialized(ctx, fmt.Sprintf("Node%d|chain|Target%d", depth-1, depth-1))
	if err != nil {
		t.Fatalf("IsMaterialized last node failed: %v", err)
	}
	if exists {
		t.Error("last edge should have been invalidated")
	}
}

func TestInvalidationManager_InvalidateDependents_NormalInvalidationNoCycles(t *testing.T) {
	db, materializer, _, _, _, manager := createInvalidationTestSetup(t)
	defer db.Close()
	ctx := context.Background()

	insertTestRule(t, db, "rule1")

	// Create a simple linear dependency: Base -> A -> B (no cycles)
	resultA := InferenceResult{
		DerivedEdge: DerivedEdge{SourceID: "A", EdgeType: "derived", TargetID: "X"},
		RuleID:      "rule1",
		Evidence:    []EvidenceEdge{{SourceID: "base", EdgeType: "ev", TargetID: "edge"}},
		DerivedAt:   time.Now(),
		Confidence:  1.0,
	}
	resultB := InferenceResult{
		DerivedEdge: DerivedEdge{SourceID: "B", EdgeType: "derived", TargetID: "Y"},
		RuleID:      "rule1",
		Evidence:    []EvidenceEdge{{SourceID: "A", EdgeType: "derived", TargetID: "X"}},
		DerivedAt:   time.Now(),
		Confidence:  1.0,
	}

	if err := materializer.Materialize(ctx, []InferenceResult{resultA}); err != nil {
		t.Fatalf("Materialize A failed: %v", err)
	}
	if err := materializer.Materialize(ctx, []InferenceResult{resultB}); err != nil {
		t.Fatalf("Materialize B failed: %v", err)
	}

	// Invalidate base edge
	err := manager.InvalidateDependents(ctx, "base|ev|edge")
	if err != nil {
		t.Fatalf("InvalidateDependents failed: %v", err)
	}

	// A should be invalidated
	exists, err := materializer.IsMaterialized(ctx, "A|derived|X")
	if err != nil {
		t.Fatalf("IsMaterialized A failed: %v", err)
	}
	if exists {
		t.Error("edge A should have been invalidated")
	}

	// B should be cascade-invalidated
	exists, err = materializer.IsMaterialized(ctx, "B|derived|Y")
	if err != nil {
		t.Fatalf("IsMaterialized B failed: %v", err)
	}
	if exists {
		t.Error("edge B should have been cascade-invalidated")
	}
}
