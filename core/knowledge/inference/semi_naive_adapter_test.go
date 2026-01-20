package inference

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/knowledge/relations"
)

// =============================================================================
// SNE.12: SemiNaiveAdapter Integration Tests
// =============================================================================

func TestSemiNaiveAdapter_SimpleRule(t *testing.T) {
	// Test basic rule evaluation with semi-naive adapter
	rules := []InferenceRule{
		{
			ID:   "simple",
			Name: "Simple Copy Rule",
			Head: RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "base", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	adapter, err := NewSemiNaiveAdapter(rules)
	if err != nil {
		t.Fatalf("NewSemiNaiveAdapter failed: %v", err)
	}

	edges := []Edge{
		{Source: "A", Predicate: "base", Target: "B"},
		{Source: "C", Predicate: "base", Target: "D"},
	}

	ctx := context.Background()
	results, err := adapter.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	// Should derive 2 edges
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Verify derived edges
	derivedEdges := make(map[string]bool)
	for _, r := range results {
		key := r.DerivedEdge.SourceID + "|" + r.DerivedEdge.EdgeType + "|" + r.DerivedEdge.TargetID
		derivedEdges[key] = true
	}

	if !derivedEdges["A|derived|B"] {
		t.Error("expected derived edge A|derived|B")
	}
	if !derivedEdges["C|derived|D"] {
		t.Error("expected derived edge C|derived|D")
	}
}

func TestSemiNaiveAdapter_TransitiveClosure(t *testing.T) {
	// Test transitive closure (the main use case for semi-naive)
	rules := []InferenceRule{
		{
			ID:   "base_reachable",
			Name: "Base Reachable",
			Head: RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "edge", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
		{
			ID:   "trans_reachable",
			Name: "Transitive Reachable",
			Head: RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	adapter, err := NewSemiNaiveAdapter(rules)
	if err != nil {
		t.Fatalf("NewSemiNaiveAdapter failed: %v", err)
	}

	// Linear chain A -> B -> C -> D
	edges := []Edge{
		{Source: "A", Predicate: "edge", Target: "B"},
		{Source: "B", Predicate: "edge", Target: "C"},
		{Source: "C", Predicate: "edge", Target: "D"},
	}

	ctx := context.Background()
	results, err := adapter.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	// Should derive: A->B, A->C, A->D, B->C, B->D, C->D (all reachability pairs)
	derivedEdges := make(map[string]bool)
	for _, r := range results {
		key := r.DerivedEdge.SourceID + "|" + r.DerivedEdge.EdgeType + "|" + r.DerivedEdge.TargetID
		derivedEdges[key] = true
	}

	expected := []string{
		"A|reachable|B",
		"A|reachable|C",
		"A|reachable|D",
		"B|reachable|C",
		"B|reachable|D",
		"C|reachable|D",
	}

	for _, exp := range expected {
		if !derivedEdges[exp] {
			t.Errorf("expected derived edge %s", exp)
		}
	}
}

func TestSemiNaiveAdapter_IncrementalEvaluation(t *testing.T) {
	// Test incremental evaluation when new edges are added
	rules := []InferenceRule{
		{
			ID:   "base_reachable",
			Name: "Base Reachable",
			Head: RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "edge", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
		{
			ID:   "trans_reachable",
			Name: "Transitive Reachable",
			Head: RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	adapter, err := NewSemiNaiveAdapter(rules)
	if err != nil {
		t.Fatalf("NewSemiNaiveAdapter failed: %v", err)
	}

	// Initial edges: A -> B
	existingEdges := []Edge{
		{Source: "A", Predicate: "edge", Target: "B"},
	}

	// New edge: B -> C
	newEdges := []Edge{
		{Source: "B", Predicate: "edge", Target: "C"},
	}

	ctx := context.Background()
	results, err := adapter.EvaluateIncremental(ctx, rules, existingEdges, newEdges)
	if err != nil {
		t.Fatalf("EvaluateIncremental failed: %v", err)
	}

	// Should derive new edges from the incremental change
	// At minimum, B->C should trigger new derivations
	if len(results) == 0 {
		t.Error("expected some derived edges from incremental evaluation")
	}
}

func TestSemiNaiveAdapter_DisabledRule(t *testing.T) {
	// Test that disabled rules are not evaluated
	rules := []InferenceRule{
		{
			ID:   "disabled",
			Name: "Disabled Rule",
			Head: RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "base", Object: "?y"},
			},
			Priority: 1,
			Enabled:  false, // Disabled!
		},
	}

	adapter, err := NewSemiNaiveAdapter(rules)
	if err != nil {
		t.Fatalf("NewSemiNaiveAdapter failed: %v", err)
	}

	edges := []Edge{
		{Source: "A", Predicate: "base", Target: "B"},
	}

	ctx := context.Background()
	results, err := adapter.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	// Should derive 0 edges since rule is disabled
	if len(results) != 0 {
		t.Errorf("expected 0 results for disabled rule, got %d", len(results))
	}
}

func TestSemiNaiveAdapter_MaxIterations(t *testing.T) {
	// Test that max iterations is respected
	rules := []InferenceRule{
		{
			ID:   "base_reachable",
			Name: "Base Reachable",
			Head: RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "edge", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
		{
			ID:   "trans_reachable",
			Name: "Transitive Reachable",
			Head: RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	adapter, err := NewSemiNaiveAdapter(rules)
	if err != nil {
		t.Fatalf("NewSemiNaiveAdapter failed: %v", err)
	}

	// Set low max iterations
	adapter.SetMaxIterations(2)

	// Long chain that would need many iterations
	edges := make([]Edge, 10)
	for i := 0; i < 10; i++ {
		edges[i] = Edge{
			Source:    string(rune('A' + i)),
			Predicate: "edge",
			Target:    string(rune('A' + i + 1)),
		}
	}

	ctx := context.Background()
	results, err := adapter.Evaluate(ctx, rules, edges)

	// Should complete without error (max iterations prevents infinite loop)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	// Should have some results but not the full transitive closure
	if len(results) == 0 {
		t.Error("expected some results")
	}
}

func TestSemiNaiveForwardChainer_Basic(t *testing.T) {
	// Test the SemiNaiveForwardChainer wrapper
	rules := []InferenceRule{
		{
			ID:   "simple",
			Name: "Simple Rule",
			Head: RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "base", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	fc, err := NewSemiNaiveForwardChainer(rules, 100)
	if err != nil {
		t.Fatalf("NewSemiNaiveForwardChainer failed: %v", err)
	}

	edges := []Edge{
		{Source: "A", Predicate: "base", Target: "B"},
	}

	ctx := context.Background()
	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSemiNaiveForwardChainer_SetMaxIterations(t *testing.T) {
	rules := []InferenceRule{
		{
			ID:      "r1",
			Name:    "Rule 1",
			Head:    RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			Body:    []RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
			Enabled: true,
		},
	}

	fc, err := NewSemiNaiveForwardChainer(rules, 100)
	if err != nil {
		t.Fatalf("NewSemiNaiveForwardChainer failed: %v", err)
	}

	if fc.GetMaxIterations() != 100 {
		t.Errorf("expected max iterations 100, got %d", fc.GetMaxIterations())
	}

	fc.SetMaxIterations(50)
	if fc.GetMaxIterations() != 50 {
		t.Errorf("expected max iterations 50, got %d", fc.GetMaxIterations())
	}
}

func TestSemiNaiveForwardChainer_RuleChange(t *testing.T) {
	// Test that forward chainer handles rule changes correctly
	rules1 := []InferenceRule{
		{
			ID:      "r1",
			Name:    "Rule 1",
			Head:    RuleCondition{Subject: "?x", Predicate: "derived1", Object: "?y"},
			Body:    []RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
			Enabled: true,
		},
	}

	fc, err := NewSemiNaiveForwardChainer(rules1, 100)
	if err != nil {
		t.Fatalf("NewSemiNaiveForwardChainer failed: %v", err)
	}

	edges := []Edge{
		{Source: "A", Predicate: "base", Target: "B"},
	}

	ctx := context.Background()

	// First evaluation with rules1
	results1, err := fc.Evaluate(ctx, rules1, edges)
	if err != nil {
		t.Fatalf("First Evaluate failed: %v", err)
	}
	if len(results1) != 1 {
		t.Errorf("expected 1 result from rules1, got %d", len(results1))
	}

	// Change rules
	rules2 := []InferenceRule{
		{
			ID:      "r2",
			Name:    "Rule 2",
			Head:    RuleCondition{Subject: "?x", Predicate: "derived2", Object: "?y"},
			Body:    []RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
			Enabled: true,
		},
	}

	// Second evaluation with rules2
	results2, err := fc.Evaluate(ctx, rules2, edges)
	if err != nil {
		t.Fatalf("Second Evaluate failed: %v", err)
	}
	if len(results2) != 1 {
		t.Errorf("expected 1 result from rules2, got %d", len(results2))
	}

	// Verify different predicates
	if results1[0].DerivedEdge.EdgeType == results2[0].DerivedEdge.EdgeType {
		t.Error("expected different edge types for different rules")
	}
}

// =============================================================================
// Conversion Helper Tests
// =============================================================================

func TestConvertEdgesToDatabase(t *testing.T) {
	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
	}

	db := ConvertEdgesToDatabase(edges)

	dbEdges := db.GetAllEdges("calls")
	if len(dbEdges) != 2 {
		t.Errorf("expected 2 edges in database, got %d", len(dbEdges))
	}
}

func TestConvertDatabaseToEdges(t *testing.T) {
	db := relations.NewInMemoryDatabase()
	db.AddEdge(relations.EdgeKey{Subject: "A", Predicate: "calls", Object: "B"})
	db.AddEdge(relations.EdgeKey{Subject: "B", Predicate: "imports", Object: "C"})

	edges := ConvertDatabaseToEdges(db)

	if len(edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(edges))
	}
}

func TestConvertDeltaToEdges(t *testing.T) {
	delta := relations.NewDeltaTable()
	delta.Add(relations.EdgeKey{Subject: "A", Predicate: "calls", Object: "B"})
	delta.Add(relations.EdgeKey{Subject: "C", Predicate: "calls", Object: "D"})

	edges := ConvertDeltaToEdges(delta)

	if len(edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(edges))
	}
}

func TestConvertToRelationsRule(t *testing.T) {
	rule := InferenceRule{
		ID:   "test",
		Name: "Test Rule",
		Head: RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?z"},
			{Subject: "?z", Predicate: "imports", Object: "?y"},
		},
		Priority: 5,
		Enabled:  true,
	}

	relRule := convertToRelationsRule(rule)

	if relRule.ID != "test" {
		t.Errorf("expected ID 'test', got '%s'", relRule.ID)
	}
	if relRule.Name != "Test Rule" {
		t.Errorf("expected Name 'Test Rule', got '%s'", relRule.Name)
	}
	if relRule.Head.Predicate != "derived" {
		t.Errorf("expected head predicate 'derived', got '%s'", relRule.Head.Predicate)
	}
	if len(relRule.Body) != 2 {
		t.Errorf("expected 2 body conditions, got %d", len(relRule.Body))
	}
	if relRule.Priority != 5 {
		t.Errorf("expected priority 5, got %d", relRule.Priority)
	}
	if !relRule.Enabled {
		t.Error("expected rule to be enabled")
	}
	if relRule.HeadPredicate != "derived" {
		t.Errorf("expected HeadPredicate 'derived', got '%s'", relRule.HeadPredicate)
	}
	if len(relRule.BodyPredicates) != 2 {
		t.Errorf("expected 2 body predicates, got %d", len(relRule.BodyPredicates))
	}
}

// =============================================================================
// Integration with InferenceEngine Tests
// =============================================================================

func TestSemiNaiveAdapter_WithInferenceEngine(t *testing.T) {
	// Test using SemiNaiveAdapter within the context of InferenceEngine workflow
	engine, db := setupTestInferenceEngine(t)
	defer db.Close()

	// Create test edges
	provider := newTestEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Define rules similar to engine rules
	rules := []InferenceRule{
		{
			ID:   "trans_calls",
			Name: "Transitive Calls",
			Head: RuleCondition{Subject: "?x", Predicate: "indirect_calls", Object: "?z"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "calls", Object: "?y"},
				{Subject: "?y", Predicate: "calls", Object: "?z"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	// Use semi-naive adapter directly
	adapter, err := NewSemiNaiveAdapter(rules)
	if err != nil {
		t.Fatalf("NewSemiNaiveAdapter failed: %v", err)
	}

	providerEdges, err := provider.GetAllEdges(ctx)
	if err != nil {
		t.Fatalf("GetAllEdges failed: %v", err)
	}

	results, err := adapter.Evaluate(ctx, rules, providerEdges)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	// Should derive indirect calls: A->C, B->D
	if len(results) < 2 {
		t.Errorf("expected at least 2 results, got %d", len(results))
	}

	// Verify results contain expected edges
	found := make(map[string]bool)
	for _, r := range results {
		key := r.DerivedEdge.SourceID + "->" + r.DerivedEdge.TargetID
		found[key] = true
	}

	if !found["A->C"] {
		t.Error("expected A->C to be derived")
	}
	if !found["B->D"] {
		t.Error("expected B->D to be derived")
	}
}

func TestSemiNaiveForwardChainer_IncrementalUpdate(t *testing.T) {
	// Test incremental updates with the forward chainer
	rules := []InferenceRule{
		{
			ID:   "base_reachable",
			Name: "Base Reachable",
			Head: RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "edge", Object: "?y"},
			},
			Enabled: true,
		},
		{
			ID:   "trans_reachable",
			Name: "Transitive Reachable",
			Head: RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "reachable", Object: "?y"},
				{Subject: "?y", Predicate: "edge", Object: "?z"},
			},
			Enabled: true,
		},
	}

	fc, err := NewSemiNaiveForwardChainer(rules, 100)
	if err != nil {
		t.Fatalf("NewSemiNaiveForwardChainer failed: %v", err)
	}

	ctx := context.Background()

	// Initial state: A -> B -> C
	existingEdges := []Edge{
		{Source: "A", Predicate: "edge", Target: "B"},
		{Source: "B", Predicate: "edge", Target: "C"},
	}

	// Add new edge: C -> D
	newEdges := []Edge{
		{Source: "C", Predicate: "edge", Target: "D"},
	}

	results, err := fc.EvaluateIncremental(ctx, rules, existingEdges, newEdges)
	if err != nil {
		t.Fatalf("EvaluateIncremental failed: %v", err)
	}

	// Should derive new reachability edges involving D
	if len(results) == 0 {
		t.Error("expected some results from incremental evaluation")
	}
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestSemiNaiveAdapter_ConcurrentEvaluations(t *testing.T) {
	rules := []InferenceRule{
		{
			ID:      "r1",
			Name:    "Rule 1",
			Head:    RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			Body:    []RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
			Enabled: true,
		},
	}

	adapter, err := NewSemiNaiveAdapter(rules)
	if err != nil {
		t.Fatalf("NewSemiNaiveAdapter failed: %v", err)
	}

	ctx := context.Background()
	done := make(chan bool)

	// Run multiple concurrent evaluations
	for i := 0; i < 10; i++ {
		go func(id int) {
			edges := []Edge{
				{Source: string(rune('A' + id)), Predicate: "base", Target: "Z"},
			}
			_, err := adapter.Evaluate(ctx, rules, edges)
			if err != nil {
				t.Errorf("Concurrent evaluation %d failed: %v", id, err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
