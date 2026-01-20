package inference

import (
	"context"
	"errors"
	"testing"
)

// =============================================================================
// NewForwardChainer Tests
// =============================================================================

func TestNewForwardChainer(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 50)

	if fc == nil {
		t.Fatal("NewForwardChainer should return non-nil")
	}
	if fc.evaluator != evaluator {
		t.Error("Evaluator not set correctly")
	}
	if fc.maxIterations != 50 {
		t.Errorf("Expected maxIterations 50, got %d", fc.maxIterations)
	}
}

func TestNewForwardChainer_DefaultMaxIterations(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 0)

	if fc.maxIterations != DefaultMaxIterations {
		t.Errorf("Expected default maxIterations %d, got %d", DefaultMaxIterations, fc.maxIterations)
	}
}

func TestNewForwardChainer_NegativeMaxIterations(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, -10)

	if fc.maxIterations != DefaultMaxIterations {
		t.Errorf("Expected default maxIterations %d for negative input, got %d", DefaultMaxIterations, fc.maxIterations)
	}
}

// =============================================================================
// Evaluate Tests - Single Rule
// =============================================================================

func TestForwardChainer_Evaluate_SingleRule(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Simple rule: ?x-imports->?y implies ?x-depends->?y
	rules := []InferenceRule{
		{
			ID:   "r1",
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
		},
	}

	edges := []Edge{
		{Source: "main", Predicate: "imports", Target: "fmt"},
		{Source: "main", Predicate: "imports", Target: "os"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
}

func TestForwardChainer_Evaluate_NoRules(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	edges := []Edge{
		{Source: "A", Predicate: "rel", Target: "B"},
	}

	results, err := fc.Evaluate(ctx, []InferenceRule{}, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results with no rules, got %d", len(results))
	}
}

func TestForwardChainer_Evaluate_NoEdges(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Test",
			Head: RuleCondition{Subject: "?x", Predicate: "out", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "in", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	results, err := fc.Evaluate(ctx, rules, []Edge{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results with no edges, got %d", len(results))
	}
}

// =============================================================================
// Evaluate Tests - Transitive Closure (Fixpoint)
// =============================================================================

func TestForwardChainer_Evaluate_TransitiveClosure(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Transitive rule: ?x-calls->?y AND ?y-calls->?z implies ?x-calls->?z
	// This should compute the transitive closure
	rules := []InferenceRule{
		{
			ID:   "transitivity",
			Name: "Call Transitivity",
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
		},
	}

	// Linear chain: A -> B -> C -> D
	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
		{Source: "C", Predicate: "calls", Target: "D"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Expected derived edges:
	// Iteration 1: A->C, B->D
	// Iteration 2: A->D
	// Total: 3 new edges
	if len(results) != 3 {
		t.Errorf("Expected 3 derived edges for transitive closure, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s-%s->%s", r.DerivedEdge.SourceID, r.DerivedEdge.EdgeType, r.DerivedEdge.TargetID)
		}
	}

	// Verify specific edges
	expected := map[string]bool{
		"A|calls|C": false,
		"B|calls|D": false,
		"A|calls|D": false,
	}

	for _, r := range results {
		key := r.DerivedEdge.SourceID + "|" + r.DerivedEdge.EdgeType + "|" + r.DerivedEdge.TargetID
		if _, ok := expected[key]; ok {
			expected[key] = true
		}
	}

	for key, found := range expected {
		if !found {
			t.Errorf("Missing expected derived edge: %s", key)
		}
	}
}

func TestForwardChainer_Evaluate_FixpointDetection(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 10) // Low limit to test fixpoint, not iterations
	ctx := context.Background()

	// Rule that should only fire once
	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Single Fire",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "derived",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "original", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "original", Target: "B"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have exactly 1 result (A-derived->B)
	// Fixpoint should be reached after 1 iteration
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

// =============================================================================
// Evaluate Tests - Duplicate Prevention
// =============================================================================

func TestForwardChainer_Evaluate_NoDuplicates(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Two rules that could derive the same edge
	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Rule 1",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "related",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "knows", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
		{
			ID:   "r2",
			Name: "Rule 2",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "related",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "likes", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	// Both rules match but should only produce one derived edge
	edges := []Edge{
		{Source: "A", Predicate: "knows", Target: "B"},
		{Source: "A", Predicate: "likes", Target: "B"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have exactly 1 unique derived edge
	if len(results) != 1 {
		t.Errorf("Expected 1 unique result (no duplicates), got %d", len(results))
	}
}

func TestForwardChainer_Evaluate_NoExistingEdgeDuplicates(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Rule that would derive an already existing edge
	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Test",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "calls",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "invokes", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	// Edge that would be derived already exists
	edges := []Edge{
		{Source: "A", Predicate: "invokes", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "B"}, // Already exists
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have 0 results since derived edge already exists
	if len(results) != 0 {
		t.Errorf("Expected 0 results (edge already exists), got %d", len(results))
	}
}

// =============================================================================
// Evaluate Tests - Max Iterations
// =============================================================================

func TestForwardChainer_Evaluate_MaxIterationsReached(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 2) // Very low limit
	ctx := context.Background()

	// Transitive rule on a long chain
	rules := []InferenceRule{
		{
			ID:   "transitivity",
			Name: "Transitivity",
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
		},
	}

	// Long chain that needs many iterations
	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
		{Source: "C", Predicate: "calls", Target: "D"},
		{Source: "D", Predicate: "calls", Target: "E"},
		{Source: "E", Predicate: "calls", Target: "F"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)

	if !errors.Is(err, ErrMaxIterationsReached) {
		t.Errorf("Expected ErrMaxIterationsReached, got %v", err)
	}

	// Should have partial results
	if len(results) == 0 {
		t.Error("Expected partial results before max iterations")
	}
}

// =============================================================================
// Evaluate Tests - Disabled Rules
// =============================================================================

func TestForwardChainer_Evaluate_DisabledRules(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	rules := []InferenceRule{
		{
			ID:   "enabled",
			Name: "Enabled Rule",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "enabled_out",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "in", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
		{
			ID:   "disabled",
			Name: "Disabled Rule",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "disabled_out",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "in", Object: "?y"},
			},
			Priority: 1,
			Enabled:  false,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "in", Target: "B"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should only have results from enabled rule
	if len(results) != 1 {
		t.Errorf("Expected 1 result (only enabled rule), got %d", len(results))
	}

	if len(results) > 0 && results[0].DerivedEdge.EdgeType != "enabled_out" {
		t.Errorf("Expected edge type 'enabled_out', got '%s'", results[0].DerivedEdge.EdgeType)
	}
}

// =============================================================================
// Evaluate Tests - Multiple Rules
// =============================================================================

func TestForwardChainer_Evaluate_MultipleRules(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Rule 1",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "type1_out",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "type1", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
		{
			ID:   "r2",
			Name: "Rule 2",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "type2_out",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "type2", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "type1", Target: "B"},
		{Source: "C", Predicate: "type2", Target: "D"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results (one per rule), got %d", len(results))
	}
}

// =============================================================================
// Evaluate Tests - Context Cancellation
// =============================================================================

func TestForwardChainer_Evaluate_ContextCancelled(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Test",
			Head: RuleCondition{Subject: "?x", Predicate: "out", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "in", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "in", Target: "B"},
	}

	_, err := fc.Evaluate(ctx, rules, edges)

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

// =============================================================================
// Evaluate Tests - Nil Evaluator
// =============================================================================

func TestForwardChainer_Evaluate_NilEvaluator(t *testing.T) {
	fc := &ForwardChainer{
		evaluator:     nil,
		maxIterations: 100,
	}
	ctx := context.Background()

	_, err := fc.Evaluate(ctx, []InferenceRule{}, []Edge{})

	if err == nil {
		t.Error("Expected error for nil evaluator")
	}
}

// =============================================================================
// IterateOnce Tests
// =============================================================================

func TestForwardChainer_IterateOnce_SingleIteration(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Test",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "out",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "in", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "in", Target: "B"},
		{Source: "C", Predicate: "in", Target: "D"},
	}

	derived := make(map[string]bool)
	for _, e := range edges {
		derived[e.Key()] = true
	}

	results, done, err := fc.IterateOnce(ctx, rules, edges, derived)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	if done {
		t.Error("Should not be done on first iteration with new results")
	}
}

func TestForwardChainer_IterateOnce_FixpointReached(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Test",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "out",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "in", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "in", Target: "B"},
	}

	// Pre-mark the derived edge
	derived := map[string]bool{
		"A|in|B":  true,
		"A|out|B": true, // Already derived
	}

	results, done, err := fc.IterateOnce(ctx, rules, edges, derived)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results (already derived), got %d", len(results))
	}

	if !done {
		t.Error("Should be done when no new results")
	}
}

func TestForwardChainer_IterateOnce_NilEvaluator(t *testing.T) {
	fc := &ForwardChainer{
		evaluator:     nil,
		maxIterations: 100,
	}
	ctx := context.Background()

	_, _, err := fc.IterateOnce(ctx, []InferenceRule{}, []Edge{}, make(map[string]bool))

	if err == nil {
		t.Error("Expected error for nil evaluator")
	}
}

// =============================================================================
// GetMaxIterations / SetMaxIterations Tests
// =============================================================================

func TestForwardChainer_GetMaxIterations(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 75)

	if fc.GetMaxIterations() != 75 {
		t.Errorf("Expected 75, got %d", fc.GetMaxIterations())
	}
}

func TestForwardChainer_SetMaxIterations(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 50)

	fc.SetMaxIterations(200)
	if fc.maxIterations != 200 {
		t.Errorf("Expected 200, got %d", fc.maxIterations)
	}
}

func TestForwardChainer_SetMaxIterations_Invalid(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 50)

	fc.SetMaxIterations(0)
	if fc.maxIterations != 50 {
		t.Errorf("Should not change for 0, expected 50, got %d", fc.maxIterations)
	}

	fc.SetMaxIterations(-10)
	if fc.maxIterations != 50 {
		t.Errorf("Should not change for negative, expected 50, got %d", fc.maxIterations)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestForwardChainer_Integration_ComplexGraph(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Multiple rules for a more complex scenario
	rules := []InferenceRule{
		// Transitive calls
		{
			ID:   "trans_calls",
			Name: "Transitive Calls",
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
		},
		// Import dependency
		{
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
		},
	}

	edges := []Edge{
		// Call chain
		{Source: "main", Predicate: "calls", Target: "handler"},
		{Source: "handler", Predicate: "calls", Target: "service"},
		{Source: "service", Predicate: "calls", Target: "repo"},
		// Imports
		{Source: "main", Predicate: "imports", Target: "handler"},
		{Source: "handler", Predicate: "imports", Target: "service"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify we got results from both rules
	callResults := 0
	dependsResults := 0
	for _, r := range results {
		switch r.DerivedEdge.EdgeType {
		case "calls":
			callResults++
		case "depends":
			dependsResults++
		}
	}

	// Transitive calls: main->service, handler->repo, main->repo (3)
	// Import deps: main->handler, handler->service (2)
	expectedCalls := 3
	expectedDepends := 2

	if callResults != expectedCalls {
		t.Errorf("Expected %d transitive calls, got %d", expectedCalls, callResults)
	}
	if dependsResults != expectedDepends {
		t.Errorf("Expected %d depends results, got %d", expectedDepends, dependsResults)
	}
}

func TestForwardChainer_Integration_CyclicGraph(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Transitive rule
	rules := []InferenceRule{
		{
			ID:   "transitivity",
			Name: "Transitivity",
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
		},
	}

	// Cyclic graph: A -> B -> C -> A
	edges := []Edge{
		{Source: "A", Predicate: "reaches", Target: "B"},
		{Source: "B", Predicate: "reaches", Target: "C"},
		{Source: "C", Predicate: "reaches", Target: "A"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should compute full transitive closure without infinite loop
	// A->C, B->A, C->B, A->A, B->B, C->C (6 new edges)
	if len(results) != 6 {
		t.Errorf("Expected 6 derived edges for cyclic graph, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s-%s->%s", r.DerivedEdge.SourceID, r.DerivedEdge.EdgeType, r.DerivedEdge.TargetID)
		}
	}
}

// =============================================================================
// W4P.14: Soft Iteration Limit Tests
// =============================================================================

func TestForwardChainer_MaxRuleApplicationsReached(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 1000) // High iteration limit
	fc.SetMaxRuleApplications(5)              // Low rule application limit
	ctx := context.Background()

	// Transitive rule that will generate many applications
	rules := []InferenceRule{
		{
			ID:   "transitivity",
			Name: "Transitivity",
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
		},
	}

	// Long chain that needs many rule applications
	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
		{Source: "C", Predicate: "calls", Target: "D"},
		{Source: "D", Predicate: "calls", Target: "E"},
		{Source: "E", Predicate: "calls", Target: "F"},
		{Source: "F", Predicate: "calls", Target: "G"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)

	if !errors.Is(err, ErrMaxRuleApplicationsReached) {
		t.Errorf("Expected ErrMaxRuleApplicationsReached, got %v", err)
	}

	// Should have partial results before limit hit
	if len(results) == 0 {
		t.Error("Expected partial results before max rule applications")
	}
}

func TestForwardChainer_ForwardChainError_Details(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 2) // Very low limit
	ctx := context.Background()

	rules := []InferenceRule{
		{
			ID:   "transitivity",
			Name: "Transitivity",
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
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
		{Source: "C", Predicate: "calls", Target: "D"},
		{Source: "D", Predicate: "calls", Target: "E"},
	}

	_, err := fc.Evaluate(ctx, rules, edges)

	if err == nil {
		t.Fatal("Expected error")
	}

	var fcErr *ForwardChainError
	if !errors.As(err, &fcErr) {
		t.Fatalf("Expected ForwardChainError, got %T", err)
	}

	// Verify error details
	if fcErr.Iteration < 1 {
		t.Errorf("Expected iteration >= 1, got %d", fcErr.Iteration)
	}
	if fcErr.DerivedCount == 0 {
		t.Error("Expected non-zero derived count")
	}

	// Error string should contain details
	errStr := fcErr.Error()
	if errStr == "" {
		t.Error("Expected non-empty error string")
	}
}

func TestForwardChainer_CycleDetectionEnabled(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)

	// Cycle detection should be enabled by default
	if !fc.IsCycleDetectionEnabled() {
		t.Error("Cycle detection should be enabled by default")
	}

	// Test disabling
	fc.SetCycleDetection(false)
	if fc.IsCycleDetectionEnabled() {
		t.Error("Cycle detection should be disabled")
	}

	// Test enabling
	fc.SetCycleDetection(true)
	if !fc.IsCycleDetectionEnabled() {
		t.Error("Cycle detection should be enabled")
	}
}

func TestForwardChainer_CyclicRulesWithTracking(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	fc.SetMaxRuleApplications(100)
	ctx := context.Background()

	// Cyclic graph: A -> B -> C -> A
	// The transitive closure will terminate naturally due to duplicate detection
	rules := []InferenceRule{
		{
			ID:   "transitivity",
			Name: "Transitivity",
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
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "reaches", Target: "B"},
		{Source: "B", Predicate: "reaches", Target: "C"},
		{Source: "C", Predicate: "reaches", Target: "A"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should complete successfully computing transitive closure
	// A->C, B->A, C->B, A->A, B->B, C->C (6 edges)
	if len(results) != 6 {
		t.Errorf("Expected 6 derived edges, got %d", len(results))
	}
}

func TestForwardChainer_EarlyTerminationNoNewFacts(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Rule that only fires once
	rules := []InferenceRule{
		{
			ID:   "once",
			Name: "Single Fire",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "derived",
				Object:    "constant",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "original", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "original", Target: "B"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have exactly 1 result and terminate early
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestForwardChainer_GetSetMaxRuleApplications(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)

	// Default should be the constant
	if fc.GetMaxRuleApplications() != DefaultMaxRuleApplications {
		t.Errorf("Expected default %d, got %d", DefaultMaxRuleApplications, fc.GetMaxRuleApplications())
	}

	// Set to new value
	fc.SetMaxRuleApplications(500)
	if fc.GetMaxRuleApplications() != 500 {
		t.Errorf("Expected 500, got %d", fc.GetMaxRuleApplications())
	}

	// Invalid values should be ignored
	fc.SetMaxRuleApplications(0)
	if fc.GetMaxRuleApplications() != 500 {
		t.Errorf("Expected 500 (unchanged), got %d", fc.GetMaxRuleApplications())
	}

	fc.SetMaxRuleApplications(-10)
	if fc.GetMaxRuleApplications() != 500 {
		t.Errorf("Expected 500 (unchanged), got %d", fc.GetMaxRuleApplications())
	}
}

func TestForwardChainer_WithOptions(t *testing.T) {
	evaluator := NewRuleEvaluator()

	opts := ForwardChainerOptions{
		MaxIterations:        50,
		MaxRuleApplications:  200,
		EnableCycleDetection: false,
	}

	fc := NewForwardChainerWithOptions(evaluator, opts)

	if fc.GetMaxIterations() != 50 {
		t.Errorf("Expected maxIterations 50, got %d", fc.GetMaxIterations())
	}
	if fc.GetMaxRuleApplications() != 200 {
		t.Errorf("Expected maxRuleApplications 200, got %d", fc.GetMaxRuleApplications())
	}
	if fc.IsCycleDetectionEnabled() {
		t.Error("Expected cycle detection to be disabled")
	}
}

func TestForwardChainer_StrictIterationLimit(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 3) // Exactly 3 iterations allowed
	ctx := context.Background()

	// Create a scenario that needs more than 3 iterations
	rules := []InferenceRule{
		{
			ID:   "chain",
			Name: "Chain Rule",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "connects",
				Object:    "?z",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "connects", Object: "?y"},
				{Subject: "?y", Predicate: "connects", Object: "?z"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	// Long chain: 1->2->3->4->5->6->7->8
	edges := []Edge{
		{Source: "1", Predicate: "connects", Target: "2"},
		{Source: "2", Predicate: "connects", Target: "3"},
		{Source: "3", Predicate: "connects", Target: "4"},
		{Source: "4", Predicate: "connects", Target: "5"},
		{Source: "5", Predicate: "connects", Target: "6"},
		{Source: "6", Predicate: "connects", Target: "7"},
		{Source: "7", Predicate: "connects", Target: "8"},
	}

	_, err := fc.Evaluate(ctx, rules, edges)

	// Should hit iteration limit
	if !errors.Is(err, ErrMaxIterationsReached) {
		t.Errorf("Expected ErrMaxIterationsReached, got %v", err)
	}
}

func TestForwardChainer_ErrorUnwrap(t *testing.T) {
	err := &ForwardChainError{
		Err:           ErrMaxIterationsReached,
		Iteration:     5,
		DerivedCount:  10,
		RuleAppsCount: 50,
	}

	// Test Unwrap
	if !errors.Is(err, ErrMaxIterationsReached) {
		t.Error("Expected error to unwrap to ErrMaxIterationsReached")
	}

	// Test Error string
	errStr := err.Error()
	if errStr == "" {
		t.Error("Error string should not be empty")
	}
}

func TestForwardChainer_WithCycleDetectionDisabled(t *testing.T) {
	evaluator := NewRuleEvaluator()
	opts := ForwardChainerOptions{
		MaxIterations:        100,
		EnableCycleDetection: false,
	}
	fc := NewForwardChainerWithOptions(evaluator, opts)
	ctx := context.Background()

	// Simple rule
	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Simple",
			Head: RuleCondition{
				Subject:   "?x",
				Predicate: "related",
				Object:    "?y",
			},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "knows", Object: "?y"},
			},
			Priority: 1,
			Enabled:  true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "knows", Target: "B"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestRuleApplicationTracker(t *testing.T) {
	tracker := newRuleApplicationTracker()

	// First application
	repeat := tracker.track("rule1", "A|rel|B")
	if repeat {
		t.Error("First application should not be a repeat")
	}
	if tracker.count() != 1 {
		t.Errorf("Expected count 1, got %d", tracker.count())
	}

	// Different rule, same edge
	repeat = tracker.track("rule2", "A|rel|B")
	if repeat {
		t.Error("Different rule should not be a repeat")
	}
	if tracker.count() != 2 {
		t.Errorf("Expected count 2, got %d", tracker.count())
	}

	// Same rule, different edge
	repeat = tracker.track("rule1", "C|rel|D")
	if repeat {
		t.Error("Different edge should not be a repeat")
	}
	if tracker.count() != 3 {
		t.Errorf("Expected count 3, got %d", tracker.count())
	}

	// Repeat application
	repeat = tracker.track("rule1", "A|rel|B")
	if !repeat {
		t.Error("Same rule+edge should be a repeat")
	}
	if tracker.count() != 4 {
		t.Errorf("Expected count 4, got %d", tracker.count())
	}
}
