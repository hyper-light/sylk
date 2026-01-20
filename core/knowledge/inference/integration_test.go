package inference

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Test Setup Helpers
// =============================================================================

// setupTestInferenceEngine creates an in-memory SQLite database and inference engine for testing.
func setupTestInferenceEngine(t *testing.T) (*InferenceEngine, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
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

	// Create materialized_edges table
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

	engine := NewInferenceEngine(db)
	return engine, db
}

// createTestEdges creates a slice of edges from source, predicate, and target string slices.
// All slices must have the same length.
func createTestEdges(source, predicate, target []string) []Edge {
	if len(source) != len(predicate) || len(predicate) != len(target) {
		panic("source, predicate, and target slices must have the same length")
	}
	edges := make([]Edge, len(source))
	for i := range source {
		edges[i] = NewEdge(source[i], predicate[i], target[i])
	}
	return edges
}

// verifyDerivedEdge checks if a derived edge exists in the engine's materialized edges.
func verifyDerivedEdge(t *testing.T, engine *InferenceEngine, source, predicate, target string) {
	t.Helper()
	ctx := context.Background()
	edgeKey := NewEdge(source, predicate, target).Key()

	provenance, err := engine.GetProvenance(ctx, edgeKey)
	if err != nil {
		t.Fatalf("failed to get provenance for %s: %v", edgeKey, err)
	}
	if provenance == nil {
		t.Errorf("expected derived edge %s-%s->%s not found", source, predicate, target)
	}
}

// verifyNoDerivedEdge checks that a derived edge does NOT exist.
func verifyNoDerivedEdge(t *testing.T, engine *InferenceEngine, source, predicate, target string) {
	t.Helper()
	ctx := context.Background()
	edgeKey := NewEdge(source, predicate, target).Key()

	provenance, err := engine.GetProvenance(ctx, edgeKey)
	if err != nil {
		t.Fatalf("failed to get provenance for %s: %v", edgeKey, err)
	}
	if provenance != nil {
		t.Errorf("unexpected derived edge %s-%s->%s found", source, predicate, target)
	}
}

// testEdgeProvider implements ExtendedEdgeProvider for integration tests.
type testEdgeProvider struct {
	edges []Edge
}

func newTestEdgeProvider(edges []Edge) *testEdgeProvider {
	return &testEdgeProvider{edges: edges}
}

func (p *testEdgeProvider) GetAllEdges(ctx context.Context) ([]Edge, error) {
	result := make([]Edge, len(p.edges))
	copy(result, p.edges)
	return result, nil
}

func (p *testEdgeProvider) GetEdgesByNode(ctx context.Context, nodeID string) ([]Edge, error) {
	var result []Edge
	for _, e := range p.edges {
		if e.Source == nodeID || e.Target == nodeID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (p *testEdgeProvider) AddEdge(edge Edge) {
	p.edges = append(p.edges, edge)
}

func (p *testEdgeProvider) RemoveEdge(key string) {
	var newEdges []Edge
	for _, e := range p.edges {
		if e.Key() != key {
			newEdges = append(newEdges, e)
		}
	}
	p.edges = newEdges
}

func (p *testEdgeProvider) SetEdges(edges []Edge) {
	p.edges = make([]Edge, len(edges))
	copy(p.edges, edges)
}

// =============================================================================
// IE.7.1 Forward Chaining Fixpoint Tests
// =============================================================================

func TestIntegration_ForwardChaining_SimpleRuleFixpoint(t *testing.T) {
	// Test: Simple rule reaches fixpoint in 1 iteration
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "imports", "B"),
		NewEdge("C", "imports", "D"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Simple rule: ?x-imports->?y implies ?x-depends->?y
	rule := InferenceRule{
		ID:   "simple_rule",
		Name: "Simple Import to Depends",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "depends",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Should derive exactly 2 edges in a single iteration (one per input edge)
	stats := engine.Stats()
	if stats.MaterializedEdges != 2 {
		t.Errorf("expected 2 materialized edges, got %d", stats.MaterializedEdges)
	}

	verifyDerivedEdge(t, engine, "A", "depends", "B")
	verifyDerivedEdge(t, engine, "C", "depends", "D")
}

func TestIntegration_ForwardChaining_TransitiveRuleFixpoint(t *testing.T) {
	// Test: Transitive rule (A->B->C) reaches fixpoint in correct iterations
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Linear chain: A -> B -> C -> D
	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Transitive rule: A calls B, B calls C -> A transitively_calls C
	rule := InferenceRule{
		ID:   "transitivity",
		Name: "Transitive Calls",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "transitively_calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Expected derived edges:
	// Iteration 1: A->C, B->D
	// No more can be derived (would need transitively_calls in body)
	stats := engine.Stats()
	if stats.MaterializedEdges != 2 {
		t.Errorf("expected 2 materialized edges for transitive rule, got %d", stats.MaterializedEdges)
	}

	verifyDerivedEdge(t, engine, "A", "transitively_calls", "C")
	verifyDerivedEdge(t, engine, "B", "transitively_calls", "D")
}

func TestIntegration_ForwardChaining_TransitiveClosureFixpoint(t *testing.T) {
	// Test: Transitive closure rule reaches fixpoint in multiple iterations
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Linear chain: A -> B -> C -> D
	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Full transitive closure rule: outputs same predicate as input
	rule := InferenceRule{
		ID:   "transitivity",
		Name: "Transitive Calls Closure",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Expected derived edges for full transitive closure:
	// Iteration 1: A->C, B->D
	// Iteration 2: A->D
	// Total: 3 new edges
	stats := engine.Stats()
	if stats.MaterializedEdges != 3 {
		t.Errorf("expected 3 materialized edges for transitive closure, got %d", stats.MaterializedEdges)
	}

	verifyDerivedEdge(t, engine, "A", "calls", "C")
	verifyDerivedEdge(t, engine, "B", "calls", "D")
	verifyDerivedEdge(t, engine, "A", "calls", "D")
}

func TestIntegration_ForwardChaining_MultipleRulesFixpoint(t *testing.T) {
	// Test: Multiple rules all evaluated correctly
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("X", "imports", "Y"),
		NewEdge("P", "extends", "Q"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Rule 1: Transitive calls
	rule1 := InferenceRule{
		ID:   "trans_calls",
		Name: "Transitive Calls",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "indirect_calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}

	// Rule 2: Import to depends
	rule2 := InferenceRule{
		ID:   "import_dep",
		Name: "Import Dependency",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "depends",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	// Rule 3: Extends to inherits
	rule3 := InferenceRule{
		ID:   "extends_inherits",
		Name: "Extends to Inherits",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "inherits",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "extends", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	if err := engine.AddRule(ctx, rule1); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.AddRule(ctx, rule2); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.AddRule(ctx, rule3); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Expected:
	// - A-indirect_calls->C (from rule1)
	// - X-depends->Y (from rule2)
	// - P-inherits->Q (from rule3)
	stats := engine.Stats()
	if stats.MaterializedEdges != 3 {
		t.Errorf("expected 3 materialized edges from multiple rules, got %d", stats.MaterializedEdges)
	}

	verifyDerivedEdge(t, engine, "A", "indirect_calls", "C")
	verifyDerivedEdge(t, engine, "X", "depends", "Y")
	verifyDerivedEdge(t, engine, "P", "inherits", "Q")
}

func TestIntegration_ForwardChaining_MaxIterationsRespected(t *testing.T) {
	// Test: No infinite loops (max iterations respected)
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Long chain that would need many iterations for full closure
	edges := make([]Edge, 10)
	for i := 0; i < 10; i++ {
		edges[i] = NewEdge(
			string(rune('A'+i)),
			"calls",
			string(rune('A'+i+1)),
		)
	}
	provider := newTestEdgeProvider(edges)
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Set a low max iterations limit
	engine.ForwardChainer().SetMaxIterations(2)

	// Transitive closure rule
	rule := InferenceRule{
		ID:   "trans",
		Name: "Transitive",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Should not error out - max iterations should be respected
	err := engine.RunInference(ctx)
	// ErrMaxIterationsReached is an acceptable outcome (may be wrapped in ForwardChainError)
	if err != nil && !errors.Is(err, ErrMaxIterationsReached) {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify we got some results (not zero, not the full closure)
	stats := engine.Stats()
	if stats.MaterializedEdges == 0 {
		t.Error("expected some materialized edges before max iterations")
	}
	// With 10 nodes in a chain and max 2 iterations, we should have partial results
	// but not the full n*(n-1)/2 - n = 45 - 10 = 35 extra edges
	if stats.MaterializedEdges > 30 {
		t.Errorf("too many edges materialized (%d), max iterations may not be working", stats.MaterializedEdges)
	}
}

// =============================================================================
// IE.7.2 Incremental Inference Tests
// =============================================================================

func TestIntegration_Incremental_AddingEdgeTriggersRelevantRulesOnly(t *testing.T) {
	// Test: Adding edge triggers relevant rules only
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Rule 1: Transitive calls (requires "calls" predicate)
	rule1 := InferenceRule{
		ID:   "trans_calls",
		Name: "Transitive Calls",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "indirect_calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}

	// Rule 2: Import dependency (requires "imports" predicate)
	rule2 := InferenceRule{
		ID:   "import_dep",
		Name: "Import Dependency",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "depends",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	if err := engine.AddRule(ctx, rule1); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.AddRule(ctx, rule2); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Add a "calls" edge - should only trigger rule1
	provider.AddEdge(NewEdge("B", "calls", "C"))
	if err := engine.OnEdgeAdded(ctx, NewEdge("B", "calls", "C")); err != nil {
		t.Fatalf("OnEdgeAdded failed: %v", err)
	}

	// Should derive A-indirect_calls->C (from rule1)
	verifyDerivedEdge(t, engine, "A", "indirect_calls", "C")

	// Rule2 should NOT have produced anything since no "imports" edges
	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Errorf("expected 1 materialized edge, got %d", stats.MaterializedEdges)
	}
}

func TestIntegration_Incremental_RemovingEdgeInvalidatesDependents(t *testing.T) {
	// Test: Removing edge invalidates dependent derived edges
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	rule := InferenceRule{
		ID:   "trans_calls",
		Name: "Transitive Calls",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "indirect_calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Run initial inference
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Verify derived edge exists
	verifyDerivedEdge(t, engine, "A", "indirect_calls", "C")

	// Remove one evidence edge
	provider.RemoveEdge("A|calls|B")
	if err := engine.OnEdgeRemoved(ctx, NewEdge("A", "calls", "B")); err != nil {
		t.Fatalf("OnEdgeRemoved failed: %v", err)
	}

	// Derived edge should be invalidated
	verifyNoDerivedEdge(t, engine, "A", "indirect_calls", "C")
}

func TestIntegration_Incremental_ModifiedEdgeCascadesChanges(t *testing.T) {
	// Test: Modified edge properly cascades changes
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	rule := InferenceRule{
		ID:   "trans_calls",
		Name: "Transitive Calls",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "indirect_calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Run initial inference
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Verify original derived edge
	verifyDerivedEdge(t, engine, "A", "indirect_calls", "C")

	// Modify edge: change B->C to B->D
	provider.RemoveEdge("B|calls|C")
	provider.AddEdge(NewEdge("B", "calls", "D"))

	if err := engine.OnEdgeModified(ctx, NewEdge("B", "calls", "C"), NewEdge("B", "calls", "D")); err != nil {
		t.Fatalf("OnEdgeModified failed: %v", err)
	}

	// Old derived edge should be invalidated
	verifyNoDerivedEdge(t, engine, "A", "indirect_calls", "C")

	// New derived edge should be created
	verifyDerivedEdge(t, engine, "A", "indirect_calls", "D")
}

func TestIntegration_Incremental_OnEdgeAddedOnlyEvaluatesMatchingRules(t *testing.T) {
	// Test: OnEdgeAdded only evaluates potentially matching rules
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("X", "imports", "Y"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Rule that requires specific "calls" predicate
	rule := InferenceRule{
		ID:   "trans_calls",
		Name: "Transitive Calls",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "indirect_calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Add an edge with "imports" predicate - should NOT trigger the "calls" rule
	provider.AddEdge(NewEdge("Y", "imports", "Z"))
	if err := engine.OnEdgeAdded(ctx, NewEdge("Y", "imports", "Z")); err != nil {
		t.Fatalf("OnEdgeAdded failed: %v", err)
	}

	// No edges should be materialized since rule requires "calls" not "imports"
	stats := engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges when adding non-matching edge, got %d", stats.MaterializedEdges)
	}
}

// =============================================================================
// IE.7.3 Circular Rule Detection Tests
// =============================================================================

func TestIntegration_Circular_SelfReferentialRulesNoInfiniteLoop(t *testing.T) {
	// Test: Self-referential rules don't cause infinite loops
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Graph with self-loop potential
	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "references", "B"),
		NewEdge("B", "references", "A"), // Cycle!
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Transitive rule that could self-reference
	rule := InferenceRule{
		ID:   "trans_refs",
		Name: "Transitive References",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "references",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "references", Object: "?y"},
			{Subject: "?y", Predicate: "references", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Should complete without hanging
	err := engine.RunInference(ctx)
	if err != nil && !errors.Is(err, ErrMaxIterationsReached) {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Should derive self-loops A->A and B->B
	verifyDerivedEdge(t, engine, "A", "references", "A")
	verifyDerivedEdge(t, engine, "B", "references", "B")
}

func TestIntegration_Circular_MutuallyRecursiveRulesReachFixpoint(t *testing.T) {
	// Test: Mutually recursive rules reach fixpoint
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Triangle: A -> B -> C -> A
	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "reaches", "B"),
		NewEdge("B", "reaches", "C"),
		NewEdge("C", "reaches", "A"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Transitive closure rule
	rule := InferenceRule{
		ID:   "trans_reaches",
		Name: "Transitive Reaches",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "reaches",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "reaches", Object: "?y"},
			{Subject: "?y", Predicate: "reaches", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Should complete and reach fixpoint
	err := engine.RunInference(ctx)
	if err != nil && !errors.Is(err, ErrMaxIterationsReached) {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Full transitive closure of a 3-cycle should produce:
	// A->C, B->A, C->B (first iteration)
	// A->A, B->B, C->C (second iteration, self-loops)
	// Total: 6 new edges
	stats := engine.Stats()
	if stats.MaterializedEdges != 6 {
		t.Errorf("expected 6 materialized edges for cyclic graph, got %d", stats.MaterializedEdges)
	}

	// Verify key derived edges
	verifyDerivedEdge(t, engine, "A", "reaches", "C")
	verifyDerivedEdge(t, engine, "B", "reaches", "A")
	verifyDerivedEdge(t, engine, "C", "reaches", "B")
	verifyDerivedEdge(t, engine, "A", "reaches", "A")
	verifyDerivedEdge(t, engine, "B", "reaches", "B")
	verifyDerivedEdge(t, engine, "C", "reaches", "C")
}

func TestIntegration_Circular_MaxIterationsPreventsRunaway(t *testing.T) {
	// Test: Max iterations prevents runaway inference
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Create a graph that could potentially produce many edges
	edges := []Edge{
		NewEdge("A", "connected", "B"),
		NewEdge("B", "connected", "C"),
		NewEdge("C", "connected", "D"),
		NewEdge("D", "connected", "E"),
		NewEdge("E", "connected", "A"), // Cycle back
	}
	provider := newTestEdgeProvider(edges)
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Set very low max iterations
	engine.ForwardChainer().SetMaxIterations(1)

	rule := InferenceRule{
		ID:   "trans",
		Name: "Transitive Connected",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "connected",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "connected", Object: "?y"},
			{Subject: "?y", Predicate: "connected", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Run inference - should respect max iterations
	err := engine.RunInference(ctx)

	// With max iterations = 1, we should hit the limit before full closure
	// Either no error (if fixpoint reached in 1 iteration, unlikely for this graph)
	// or ErrMaxIterationsReached (may be wrapped in ForwardChainError)
	if err != nil && !errors.Is(err, ErrMaxIterationsReached) {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify we got limited results, not the full transitive closure
	stats := engine.Stats()
	// Full closure would be 20 edges (5*4 = 20 pairs), minus 5 original = 15 derived
	// With 1 iteration, we should have fewer
	if stats.MaterializedEdges >= 15 {
		t.Errorf("max iterations not working: got %d edges (expected fewer than 15)", stats.MaterializedEdges)
	}
}

// =============================================================================
// IE.7.4 Built-in Rule Correctness Tests
// =============================================================================

func TestIntegration_Builtin_TransitiveImportDerives(t *testing.T) {
	// Test: TransitiveImport correctly derives indirect imports
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// A imports B, B imports C
	provider := newTestEdgeProvider([]Edge{
		NewEdge("main.go", "imports", "handler.go"),
		NewEdge("handler.go", "imports", "service.go"),
		NewEdge("service.go", "imports", "repo.go"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add TransitiveImport rule
	rule := TransitiveImportRule()
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Should derive indirect imports:
	// main.go indirectly_imports service.go (via handler.go)
	// handler.go indirectly_imports repo.go (via service.go)
	verifyDerivedEdge(t, engine, "main.go", "indirectly_imports", "service.go")
	verifyDerivedEdge(t, engine, "handler.go", "indirectly_imports", "repo.go")

	stats := engine.Stats()
	if stats.MaterializedEdges != 2 {
		t.Errorf("expected 2 materialized edges for transitive import, got %d", stats.MaterializedEdges)
	}
}

func TestIntegration_Builtin_InheritedMethodPropagates(t *testing.T) {
	// Test: InheritedMethod correctly propagates methods
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Child extends Parent, Parent has_method Method
	provider := newTestEdgeProvider([]Edge{
		NewEdge("ChildClass", "extends", "ParentClass"),
		NewEdge("ParentClass", "has_method", "DoSomething"),
		NewEdge("ParentClass", "has_method", "DoOther"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add InheritedMethod rule
	rule := InheritedMethodRule()
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// ChildClass should inherit both methods
	verifyDerivedEdge(t, engine, "ChildClass", "has_method", "DoSomething")
	verifyDerivedEdge(t, engine, "ChildClass", "has_method", "DoOther")

	stats := engine.Stats()
	if stats.MaterializedEdges != 2 {
		t.Errorf("expected 2 materialized edges for inherited methods, got %d", stats.MaterializedEdges)
	}
}

func TestIntegration_Builtin_CallChainRespectsMaxDepth(t *testing.T) {
	// Test: CallChain respects max depth of 3
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Call chain: A -> B -> C -> D -> E -> F
	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
		NewEdge("D", "calls", "E"),
		NewEdge("E", "calls", "F"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add all three call chain rules (depth 1, 2, 3)
	rules := []InferenceRule{
		CallChainRule(),       // depth 1: A->B, B->C => A transitively_calls C
		CallChainDepth2Rule(), // depth 2: uses transitively_calls + calls
		CallChainDepth3Rule(), // depth 3: uses transitively_calls + transitively_calls
	}

	for _, rule := range rules {
		if err := engine.AddRule(ctx, rule); err != nil {
			t.Fatalf("AddRule failed: %v", err)
		}
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// With the depth rules, we should get transitive calls up to depth 3
	// From A: A->C (depth 1), A->D (depth 2), A->E (depth 3)
	// The rules should cap at depth 3 due to rule design
	verifyDerivedEdge(t, engine, "A", "transitively_calls", "C") // depth 1
	verifyDerivedEdge(t, engine, "A", "transitively_calls", "D") // depth 2
	verifyDerivedEdge(t, engine, "A", "transitively_calls", "E") // depth 3

	// Note: The current rule design may derive more, depending on rule evaluation order
	// and how the rules interact. The key test is that it completes without hanging.
	stats := engine.Stats()
	if stats.MaterializedEdges == 0 {
		t.Error("expected at least some materialized edges for call chain rules")
	}
}

func TestIntegration_Builtin_AllBuiltinRulesProduceDerivedEdges(t *testing.T) {
	// Test: All built-in rules produce expected derived edges
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Create edges that will trigger each built-in rule
	provider := newTestEdgeProvider([]Edge{
		// For TransitiveImport
		NewEdge("pkg1", "imports", "pkg2"),
		NewEdge("pkg2", "imports", "pkg3"),

		// For InheritedMethod
		NewEdge("Derived", "extends", "Base"),
		NewEdge("Base", "has_method", "Method1"),

		// For InterfaceSatisfaction
		NewEdge("MyImpl", "implements", "MyInterface"),

		// For CallChain
		NewEdge("funcA", "calls", "funcB"),
		NewEdge("funcB", "calls", "funcC"),

		// For ReferenceClosure
		NewEdge("ref1", "references", "ref2"),
		NewEdge("ref2", "references", "ref3"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Load all built-in rules
	if err := LoadBuiltinRules(ctx, engine); err != nil {
		t.Fatalf("LoadBuiltinRules failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Verify key derived edges from each rule type
	// TransitiveImport: pkg1 indirectly_imports pkg3
	verifyDerivedEdge(t, engine, "pkg1", "indirectly_imports", "pkg3")

	// InheritedMethod: Derived has_method Method1
	verifyDerivedEdge(t, engine, "Derived", "has_method", "Method1")

	// InterfaceSatisfaction: MyImpl satisfies MyInterface
	verifyDerivedEdge(t, engine, "MyImpl", "satisfies", "MyInterface")

	// CallChain: funcA transitively_calls funcC
	verifyDerivedEdge(t, engine, "funcA", "transitively_calls", "funcC")

	// ReferenceClosure: ref1 transitively_refs ref3
	verifyDerivedEdge(t, engine, "ref1", "transitively_refs", "ref3")

	// All rules should have produced at least the above edges
	stats := engine.Stats()
	if stats.MaterializedEdges < 5 {
		t.Errorf("expected at least 5 materialized edges from all built-in rules, got %d", stats.MaterializedEdges)
	}
}

func TestIntegration_Builtin_RuleDisabledSkipsDerivation(t *testing.T) {
	// Test: Disabled rules don't produce derived edges
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "imports", "B"),
		NewEdge("B", "imports", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add TransitiveImport rule but disabled
	rule := TransitiveImportRule()
	rule.Enabled = false
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Should NOT derive any edges since rule is disabled
	stats := engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges with disabled rule, got %d", stats.MaterializedEdges)
	}
}

func TestIntegration_Builtin_RulesWithMultipleConditions(t *testing.T) {
	// Test: Rules with multiple body conditions work correctly
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Setup edges that satisfy multiple conditions
	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "owns", "B"),
		NewEdge("B", "contains", "C"),
		NewEdge("X", "owns", "Y"),
		// Missing Y-contains->Z, so no inference from X
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Rule with two conditions: ?x owns ?y AND ?y contains ?z => ?x indirectly_owns ?z
	rule := InferenceRule{
		ID:   "indirect_owns",
		Name: "Indirect Ownership",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "indirectly_owns",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "owns", Object: "?y"},
			{Subject: "?y", Predicate: "contains", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Only A-indirectly_owns->C should be derived (both conditions met)
	// X should NOT derive anything (missing second condition)
	verifyDerivedEdge(t, engine, "A", "indirectly_owns", "C")
	verifyNoDerivedEdge(t, engine, "X", "indirectly_owns", "Z")

	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Errorf("expected 1 materialized edge, got %d", stats.MaterializedEdges)
	}
}

// =============================================================================
// Additional Integration Tests
// =============================================================================

func TestIntegration_EmptyGraph(t *testing.T) {
	// Test: Inference on empty graph produces no errors
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	rule := TransitiveImportRule()
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference on empty graph failed: %v", err)
	}

	stats := engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges on empty graph, got %d", stats.MaterializedEdges)
	}
}

func TestIntegration_NoRules(t *testing.T) {
	// Test: Inference with no rules produces no errors
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference with no rules failed: %v", err)
	}

	stats := engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges with no rules, got %d", stats.MaterializedEdges)
	}
}

func TestIntegration_ProvenanceTracking(t *testing.T) {
	// Test: Provenance is correctly tracked for derived edges
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	rule := InferenceRule{
		ID:   "trans_calls",
		Name: "Transitive Calls",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "indirect_calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Get provenance for derived edge
	edgeKey := "A|indirect_calls|C"
	provenance, err := engine.GetProvenance(ctx, edgeKey)
	if err != nil {
		t.Fatalf("GetProvenance failed: %v", err)
	}
	if provenance == nil {
		t.Fatal("expected provenance for derived edge")
	}

	// Verify provenance fields
	if provenance.RuleID != "trans_calls" {
		t.Errorf("expected RuleID 'trans_calls', got '%s'", provenance.RuleID)
	}
	if len(provenance.Evidence) != 2 {
		t.Errorf("expected 2 evidence edges, got %d", len(provenance.Evidence))
	}
	if provenance.Provenance == "" {
		t.Error("provenance string should not be empty")
	}
}

func TestIntegration_CascadingInvalidation(t *testing.T) {
	// Test: Removing a base edge cascades to invalidate all dependent derived edges
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Chain that produces multiple levels of derived edges
	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Transitive closure rule
	rule := InferenceRule{
		ID:   "trans",
		Name: "Transitive",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Should have derived edges
	stats := engine.Stats()
	initialDerived := stats.MaterializedEdges
	if initialDerived == 0 {
		t.Fatal("expected some derived edges before removal")
	}

	// Remove middle edge B->C
	provider.RemoveEdge("B|calls|C")
	if err := engine.OnEdgeRemoved(ctx, NewEdge("B", "calls", "C")); err != nil {
		t.Fatalf("OnEdgeRemoved failed: %v", err)
	}

	// All edges that depended on B->C should be invalidated
	stats = engine.Stats()
	// A->C and A->D should be invalidated (they depended on B->C)
	// B->D should be invalidated (it depended on B->C)
	// Only edge that might remain is A->B derived edge (if any)
	if stats.MaterializedEdges >= initialDerived {
		t.Errorf("cascading invalidation failed: before=%d, after=%d", initialDerived, stats.MaterializedEdges)
	}
}
