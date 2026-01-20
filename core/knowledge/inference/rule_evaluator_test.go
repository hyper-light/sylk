package inference

import (
	"context"
	"testing"
)

// =============================================================================
// NewRuleEvaluator Tests
// =============================================================================

func TestNewRuleEvaluator(t *testing.T) {
	evaluator := NewRuleEvaluator()
	if evaluator == nil {
		t.Error("NewRuleEvaluator should return non-nil evaluator")
	}
}

// =============================================================================
// EvaluateRule Tests - Single Condition Rules
// =============================================================================

func TestRuleEvaluator_EvaluateRule_SingleCondition(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule: ?x-imports->?y implies ?x-depends_on->?y
	rule := InferenceRule{
		ID:   "r1",
		Name: "Import to Dependency",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "depends_on",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "main", Predicate: "imports", Target: "fmt"},
		{Source: "main", Predicate: "imports", Target: "os"},
		{Source: "utils", Predicate: "imports", Target: "strings"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(results))
	}

	// Verify each result
	expectedDerived := map[string]bool{
		"main|depends_on|fmt":     false,
		"main|depends_on|os":      false,
		"utils|depends_on|strings": false,
	}

	for _, r := range results {
		key := r.DerivedEdge.SourceID + "|" + r.DerivedEdge.EdgeType + "|" + r.DerivedEdge.TargetID
		if _, ok := expectedDerived[key]; !ok {
			t.Errorf("Unexpected derived edge: %s", key)
		}
		expectedDerived[key] = true

		if r.RuleID != "r1" {
			t.Errorf("Expected rule ID 'r1', got '%s'", r.RuleID)
		}
		if len(r.Evidence) != 1 {
			t.Errorf("Expected 1 evidence edge, got %d", len(r.Evidence))
		}
	}

	for key, found := range expectedDerived {
		if !found {
			t.Errorf("Missing expected derived edge: %s", key)
		}
	}
}

func TestRuleEvaluator_EvaluateRule_NoMatches(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule looking for "calls" edges
	rule := InferenceRule{
		ID:   "r1",
		Name: "Call Inference",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "invokes",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	// Edges only have "imports", no "calls"
	edges := []Edge{
		{Source: "main", Predicate: "imports", Target: "fmt"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 0 {
		t.Errorf("Expected 0 results when no edges match, got %d", len(results))
	}
}

// =============================================================================
// EvaluateRule Tests - Multi-Condition Rules
// =============================================================================

func TestRuleEvaluator_EvaluateRule_TwoConditions(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule: ?x-calls->?y AND ?y-calls->?z implies ?x-indirect_calls->?z
	rule := InferenceRule{
		ID:   "transitivity",
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

	edges := []Edge{
		{Source: "main", Predicate: "calls", Target: "helper"},
		{Source: "helper", Predicate: "calls", Target: "util"},
		{Source: "util", Predicate: "calls", Target: "lib"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	// Expected: main->helper->util, helper->util->lib
	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	expectedDerived := map[string]bool{
		"main|indirect_calls|util":   false,
		"helper|indirect_calls|lib": false,
	}

	for _, r := range results {
		key := r.DerivedEdge.SourceID + "|" + r.DerivedEdge.EdgeType + "|" + r.DerivedEdge.TargetID
		if _, ok := expectedDerived[key]; !ok {
			t.Errorf("Unexpected derived edge: %s", key)
		}
		expectedDerived[key] = true

		// Each result should have 2 evidence edges
		if len(r.Evidence) != 2 {
			t.Errorf("Expected 2 evidence edges, got %d", len(r.Evidence))
		}
	}

	for key, found := range expectedDerived {
		if !found {
			t.Errorf("Missing expected derived edge: %s", key)
		}
	}
}

func TestRuleEvaluator_EvaluateRule_ThreeConditions(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule: ?a-calls->?b AND ?b-calls->?c AND ?c-calls->?d implies ?a-deep_calls->?d
	rule := InferenceRule{
		ID:   "deep_transitivity",
		Name: "Deep Transitive Calls",
		Head: RuleCondition{
			Subject:   "?a",
			Predicate: "deep_calls",
			Object:    "?d",
		},
		Body: []RuleCondition{
			{Subject: "?a", Predicate: "calls", Object: "?b"},
			{Subject: "?b", Predicate: "calls", Object: "?c"},
			{Subject: "?c", Predicate: "calls", Object: "?d"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
		{Source: "C", Predicate: "calls", Target: "D"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.DerivedEdge.SourceID != "A" || r.DerivedEdge.TargetID != "D" {
		t.Errorf("Expected A-deep_calls->D, got %s-%s->%s",
			r.DerivedEdge.SourceID, r.DerivedEdge.EdgeType, r.DerivedEdge.TargetID)
	}

	if len(r.Evidence) != 3 {
		t.Errorf("Expected 3 evidence edges, got %d", len(r.Evidence))
	}
}

func TestRuleEvaluator_EvaluateRule_MultiConditionPartialMatch(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule requiring two conditions
	rule := InferenceRule{
		ID:   "r1",
		Name: "Two Condition Rule",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "related",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "knows", Object: "?y"},
			{Subject: "?y", Predicate: "knows", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}

	// Only partial chain - no complete match
	edges := []Edge{
		{Source: "A", Predicate: "knows", Target: "B"},
		{Source: "C", Predicate: "knows", Target: "D"}, // Disconnected
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 0 {
		t.Errorf("Expected 0 results when chain is incomplete, got %d", len(results))
	}
}

// =============================================================================
// EvaluateRule Tests - Constants in Rules
// =============================================================================

func TestRuleEvaluator_EvaluateRule_ConstantInBody(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule: ?x-imports->"fmt" implies ?x-uses_stdlib->true
	rule := InferenceRule{
		ID:   "stdlib_detection",
		Name: "Stdlib Detection",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "uses_stdlib",
			Object:    "true",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "fmt"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "main", Predicate: "imports", Target: "fmt"},
		{Source: "helper", Predicate: "imports", Target: "custom"},
		{Source: "utils", Predicate: "imports", Target: "fmt"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// Both results should have Object = "true"
	for _, r := range results {
		if r.DerivedEdge.TargetID != "true" {
			t.Errorf("Expected target 'true', got '%s'", r.DerivedEdge.TargetID)
		}
		if r.DerivedEdge.EdgeType != "uses_stdlib" {
			t.Errorf("Expected edge type 'uses_stdlib', got '%s'", r.DerivedEdge.EdgeType)
		}
	}
}

func TestRuleEvaluator_EvaluateRule_ConstantPredicate(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule with constant predicate in body
	rule := InferenceRule{
		ID:   "r1",
		Name: "Const Predicate",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "derived",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "specific_rel", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "A", Predicate: "specific_rel", Target: "B"},
		{Source: "C", Predicate: "other_rel", Target: "D"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0].DerivedEdge.SourceID != "A" || results[0].DerivedEdge.TargetID != "B" {
		t.Errorf("Unexpected result: %+v", results[0].DerivedEdge)
	}
}

// =============================================================================
// EvaluateRule Tests - Variable Binding Consistency
// =============================================================================

func TestRuleEvaluator_EvaluateRule_SharedVariable(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule: ?x-likes->?y AND ?x-knows->?y implies ?x-friend->?y
	// Same variables in both conditions must bind to same values
	rule := InferenceRule{
		ID:   "friendship",
		Name: "Friendship Inference",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "friend",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "likes", Object: "?y"},
			{Subject: "?x", Predicate: "knows", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "Alice", Predicate: "likes", Target: "Bob"},
		{Source: "Alice", Predicate: "knows", Target: "Bob"},
		{Source: "Alice", Predicate: "likes", Target: "Carol"},  // No matching knows
		{Source: "Alice", Predicate: "knows", Target: "Dave"},   // No matching likes
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 1 {
		t.Fatalf("Expected 1 result (only Alice-Bob satisfies both), got %d", len(results))
	}

	r := results[0]
	if r.DerivedEdge.SourceID != "Alice" || r.DerivedEdge.TargetID != "Bob" {
		t.Errorf("Expected Alice-friend->Bob, got %s-%s->%s",
			r.DerivedEdge.SourceID, r.DerivedEdge.EdgeType, r.DerivedEdge.TargetID)
	}
}

func TestRuleEvaluator_EvaluateRule_SameVariableTwice(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Rule: ?x-ref->?x implies ?x-self_ref->?x (self-referential edges)
	rule := InferenceRule{
		ID:   "self_ref",
		Name: "Self Reference",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "self_ref",
			Object:    "?x",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "ref", Object: "?x"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "A", Predicate: "ref", Target: "A"},  // Self-ref
		{Source: "B", Predicate: "ref", Target: "C"},  // Not self-ref
		{Source: "D", Predicate: "ref", Target: "D"},  // Self-ref
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 2 {
		t.Fatalf("Expected 2 results (A and D), got %d", len(results))
	}

	foundA := false
	foundD := false
	for _, r := range results {
		if r.DerivedEdge.SourceID == "A" && r.DerivedEdge.TargetID == "A" {
			foundA = true
		}
		if r.DerivedEdge.SourceID == "D" && r.DerivedEdge.TargetID == "D" {
			foundD = true
		}
	}

	if !foundA {
		t.Error("Missing result for A-self_ref->A")
	}
	if !foundD {
		t.Error("Missing result for D-self_ref->D")
	}
}

// =============================================================================
// EvaluateRule Tests - Context Cancellation
// =============================================================================

func TestRuleEvaluator_EvaluateRule_ContextCancelled(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	rule := InferenceRule{
		ID:   "r1",
		Name: "Test Rule",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "derived",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "rel", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "A", Predicate: "rel", Target: "B"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	// Should return early, possibly with partial or no results
	// The exact behavior depends on when the cancellation is detected
	_ = results
}

// =============================================================================
// EvaluateRule Tests - Multiple Results
// =============================================================================

func TestRuleEvaluator_EvaluateRule_MultipleBindings(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Simple rule that matches many edges
	rule := InferenceRule{
		ID:   "copy",
		Name: "Copy Relation",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "copied",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "original", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "A", Predicate: "original", Target: "1"},
		{Source: "B", Predicate: "original", Target: "2"},
		{Source: "C", Predicate: "original", Target: "3"},
		{Source: "D", Predicate: "original", Target: "4"},
		{Source: "E", Predicate: "original", Target: "5"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 5 {
		t.Errorf("Expected 5 results, got %d", len(results))
	}
}

// =============================================================================
// EvaluateRule Tests - Provenance Generation
// =============================================================================

func TestRuleEvaluator_EvaluateRule_ProvenanceGenerated(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "r1",
		Name: "Test Provenance Rule",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "result",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "input", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges := []Edge{
		{Source: "NodeA", Predicate: "input", Target: "NodeB"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Provenance == "" {
		t.Error("Provenance should be generated")
	}
	if r.RuleID != "r1" {
		t.Errorf("RuleID should be 'r1', got '%s'", r.RuleID)
	}
}

// =============================================================================
// findBindings Tests
// =============================================================================

func TestRuleEvaluator_findBindings_EmptyConditions(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	edges := []Edge{
		{Source: "A", Predicate: "rel", Target: "B"},
	}

	bindings := evaluator.findBindings(ctx, []RuleCondition{}, edges)

	if bindings != nil {
		t.Errorf("Expected nil for empty conditions, got %v", bindings)
	}
}

func TestRuleEvaluator_findBindings_SingleCondition(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	conditions := []RuleCondition{
		{Subject: "?x", Predicate: "rel", Object: "?y"},
	}

	edges := []Edge{
		{Source: "A", Predicate: "rel", Target: "B"},
		{Source: "C", Predicate: "rel", Target: "D"},
	}

	bindings := evaluator.findBindings(ctx, conditions, edges)

	if len(bindings) != 2 {
		t.Errorf("Expected 2 binding sets, got %d", len(bindings))
	}
}

// =============================================================================
// applyBindings Tests
// =============================================================================

func TestRuleEvaluator_applyBindings(t *testing.T) {
	evaluator := NewRuleEvaluator()

	head := RuleCondition{
		Subject:   "?x",
		Predicate: "derived",
		Object:    "?y",
	}

	bindings := map[string]string{
		"?x": "NodeA",
		"?y": "NodeB",
	}

	edge := evaluator.applyBindings(head, bindings)

	if edge.Source != "NodeA" {
		t.Errorf("Expected Source 'NodeA', got '%s'", edge.Source)
	}
	if edge.Predicate != "derived" {
		t.Errorf("Expected Predicate 'derived', got '%s'", edge.Predicate)
	}
	if edge.Target != "NodeB" {
		t.Errorf("Expected Target 'NodeB', got '%s'", edge.Target)
	}
}

func TestRuleEvaluator_applyBindings_WithConstant(t *testing.T) {
	evaluator := NewRuleEvaluator()

	head := RuleCondition{
		Subject:   "?x",
		Predicate: "uses",
		Object:    "stdlib",
	}

	bindings := map[string]string{
		"?x": "main",
	}

	edge := evaluator.applyBindings(head, bindings)

	if edge.Source != "main" {
		t.Errorf("Expected Source 'main', got '%s'", edge.Source)
	}
	if edge.Target != "stdlib" {
		t.Errorf("Expected Target 'stdlib' (constant), got '%s'", edge.Target)
	}
}

// =============================================================================
// cloneEdges Tests
// =============================================================================

func TestCloneEdges(t *testing.T) {
	original := []Edge{
		{Source: "A", Predicate: "rel", Target: "B"},
		{Source: "C", Predicate: "rel", Target: "D"},
	}

	cloned := cloneEdges(original)

	if len(cloned) != len(original) {
		t.Errorf("Clone length mismatch: %d vs %d", len(cloned), len(original))
	}

	// Modify clone and ensure original unchanged
	cloned[0].Source = "MODIFIED"
	if original[0].Source == "MODIFIED" {
		t.Error("Original was modified when clone was changed")
	}
}

func TestCloneEdges_Nil(t *testing.T) {
	cloned := cloneEdges(nil)
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestCloneEdges_Empty(t *testing.T) {
	cloned := cloneEdges([]Edge{})
	if cloned == nil {
		t.Error("Clone of empty slice should not be nil")
	}
	if len(cloned) != 0 {
		t.Error("Clone of empty slice should be empty")
	}
}

// =============================================================================
// PF.4.8: Edge Index Tests
// =============================================================================

func TestRuleEvaluator_buildEdgeIndex(t *testing.T) {
	evaluator := NewRuleEvaluator()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "C"},
		{Source: "B", Predicate: "calls", Target: "D"},
		{Source: "B", Predicate: "imports", Target: "E"},
	}

	idx := evaluator.buildEdgeIndex(edges)

	// Test bySource
	if len(idx.bySource["A"]) != 2 {
		t.Errorf("Expected 2 edges from source A, got %d", len(idx.bySource["A"]))
	}
	if len(idx.bySource["B"]) != 2 {
		t.Errorf("Expected 2 edges from source B, got %d", len(idx.bySource["B"]))
	}

	// Test byTarget
	if len(idx.byTarget["B"]) != 1 {
		t.Errorf("Expected 1 edge to target B, got %d", len(idx.byTarget["B"]))
	}

	// Test byPredicate
	if len(idx.byPredicate["calls"]) != 3 {
		t.Errorf("Expected 3 edges with predicate 'calls', got %d", len(idx.byPredicate["calls"]))
	}
	if len(idx.byPredicate["imports"]) != 1 {
		t.Errorf("Expected 1 edge with predicate 'imports', got %d", len(idx.byPredicate["imports"]))
	}

	// Test bySourcePred
	if len(idx.bySourcePred["A:calls"]) != 2 {
		t.Errorf("Expected 2 edges for A:calls, got %d", len(idx.bySourcePred["A:calls"]))
	}
	if len(idx.bySourcePred["B:imports"]) != 1 {
		t.Errorf("Expected 1 edge for B:imports, got %d", len(idx.bySourcePred["B:imports"]))
	}

	// Test byTargetPred
	if len(idx.byTargetPred["B:calls"]) != 1 {
		t.Errorf("Expected 1 edge for B:calls (as target), got %d", len(idx.byTargetPred["B:calls"]))
	}

	// Test allEdges
	if len(idx.allEdges) != 4 {
		t.Errorf("Expected 4 edges in allEdges, got %d", len(idx.allEdges))
	}
}

func TestRuleEvaluator_buildEdgeIndex_Empty(t *testing.T) {
	evaluator := NewRuleEvaluator()
	idx := evaluator.buildEdgeIndex([]Edge{})

	if idx == nil {
		t.Error("Index should not be nil for empty edges")
	}
	if len(idx.allEdges) != 0 {
		t.Error("allEdges should be empty")
	}
}

func TestRuleEvaluator_matchConditionIndexed_BoundSource(t *testing.T) {
	evaluator := NewRuleEvaluator()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "C"},
		{Source: "B", Predicate: "calls", Target: "D"},
	}

	evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	// With ?x bound to "A"
	bindings := map[string]string{"?x": "A"}
	matches := evaluator.matchConditionIndexed(condition, bindings)

	if len(matches) != 2 {
		t.Errorf("Expected 2 matches with source A, got %d", len(matches))
	}

	for _, m := range matches {
		if m.edge.Source != "A" {
			t.Errorf("Expected source A, got %s", m.edge.Source)
		}
	}
}

func TestRuleEvaluator_matchConditionIndexed_BoundTarget(t *testing.T) {
	evaluator := NewRuleEvaluator()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "C", Predicate: "calls", Target: "B"},
		{Source: "D", Predicate: "calls", Target: "E"},
	}

	evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	// With ?y bound to "B"
	bindings := map[string]string{"?y": "B"}
	matches := evaluator.matchConditionIndexed(condition, bindings)

	if len(matches) != 2 {
		t.Errorf("Expected 2 matches with target B, got %d", len(matches))
	}

	for _, m := range matches {
		if m.edge.Target != "B" {
			t.Errorf("Expected target B, got %s", m.edge.Target)
		}
	}
}

func TestRuleEvaluator_matchConditionIndexed_ConstantPredicate(t *testing.T) {
	evaluator := NewRuleEvaluator()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "imports", Target: "C"},
		{Source: "B", Predicate: "calls", Target: "D"},
	}

	evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "imports", // constant predicate
		Object:    "?y",
	}

	bindings := map[string]string{}
	matches := evaluator.matchConditionIndexed(condition, bindings)

	if len(matches) != 1 {
		t.Errorf("Expected 1 match with predicate 'imports', got %d", len(matches))
	}

	if matches[0].edge.Predicate != "imports" {
		t.Errorf("Expected predicate 'imports', got %s", matches[0].edge.Predicate)
	}
}

func TestRuleEvaluator_matchConditionIndexed_NoIndex(t *testing.T) {
	evaluator := NewRuleEvaluator()
	evaluator.edgeIndex = nil

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	matches := evaluator.matchConditionIndexed(condition, map[string]string{})

	if matches != nil {
		t.Error("Expected nil matches when no index")
	}
}

// =============================================================================
// PF.4.7: Memoization Tests
// =============================================================================

func TestRuleEvaluator_conditionCacheKey(t *testing.T) {
	evaluator := NewRuleEvaluator()

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	// Same condition with same bindings should have same key
	bindings1 := map[string]string{"?x": "A"}
	bindings2 := map[string]string{"?x": "A"}

	key1 := evaluator.conditionCacheKey(condition, bindings1)
	key2 := evaluator.conditionCacheKey(condition, bindings2)

	if key1 != key2 {
		t.Errorf("Same bindings should produce same key: %s != %s", key1, key2)
	}

	// Different bindings should have different keys
	bindings3 := map[string]string{"?x": "B"}
	key3 := evaluator.conditionCacheKey(condition, bindings3)

	if key1 == key3 {
		t.Error("Different bindings should produce different keys")
	}

	// Multiple variables bound should produce consistent keys
	bindings4 := map[string]string{"?x": "A", "?y": "B"}
	bindings5 := map[string]string{"?y": "B", "?x": "A"} // order shouldn't matter

	key4 := evaluator.conditionCacheKey(condition, bindings4)
	key5 := evaluator.conditionCacheKey(condition, bindings5)

	if key4 != key5 {
		t.Errorf("Binding order should not affect key: %s != %s", key4, key5)
	}
}

func TestRuleEvaluator_conditionCacheKey_ConstantCondition(t *testing.T) {
	evaluator := NewRuleEvaluator()

	condition := RuleCondition{
		Subject:   "NodeA",
		Predicate: "calls",
		Object:    "NodeB",
	}

	// No variables, so bindings don't matter
	key1 := evaluator.conditionCacheKey(condition, map[string]string{})
	key2 := evaluator.conditionCacheKey(condition, map[string]string{"?x": "ignored"})

	if key1 != key2 {
		t.Errorf("Irrelevant bindings should not affect key: %s != %s", key1, key2)
	}
}

func TestRuleEvaluator_matchConditionCached(t *testing.T) {
	evaluator := NewRuleEvaluator()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "C"},
	}

	evaluator.clearCache()
	evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	bindings := map[string]string{"?x": "A"}

	// First call should compute and cache
	matches1 := evaluator.matchConditionCached(condition, bindings)

	// Second call should return cached result
	matches2 := evaluator.matchConditionCached(condition, bindings)

	if len(matches1) != len(matches2) {
		t.Errorf("Cached result should match: %d != %d", len(matches1), len(matches2))
	}

	if len(matches1) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(matches1))
	}

	// Verify cache was populated
	cacheKey := evaluator.conditionCacheKey(condition, bindings)
	if _, ok := evaluator.matchCache[cacheKey]; !ok {
		t.Error("Cache should contain the computed result")
	}
}

func TestRuleEvaluator_clearCache(t *testing.T) {
	evaluator := NewRuleEvaluator()

	// Add something to cache
	evaluator.matchCache["test_key"] = []conditionMatch{
		{edge: Edge{Source: "A", Predicate: "rel", Target: "B"}},
	}

	if len(evaluator.matchCache) != 1 {
		t.Error("Cache should have 1 entry")
	}

	evaluator.clearCache()

	if len(evaluator.matchCache) != 0 {
		t.Error("Cache should be empty after clear")
	}
}

func TestRuleEvaluator_CacheIsClearedPerEvaluation(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "r1",
		Name: "Test",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "derived",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "rel", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}

	edges1 := []Edge{
		{Source: "A", Predicate: "rel", Target: "B"},
	}

	// First evaluation
	results1 := evaluator.EvaluateRule(ctx, rule, edges1)
	if len(results1) != 1 {
		t.Fatalf("Expected 1 result from first evaluation, got %d", len(results1))
	}

	// Second evaluation with different edges
	edges2 := []Edge{
		{Source: "C", Predicate: "rel", Target: "D"},
		{Source: "E", Predicate: "rel", Target: "F"},
	}

	results2 := evaluator.EvaluateRule(ctx, rule, edges2)
	if len(results2) != 2 {
		t.Fatalf("Expected 2 results from second evaluation, got %d", len(results2))
	}

	// Verify results are from the correct edge set
	foundC := false
	foundE := false
	for _, r := range results2 {
		if r.DerivedEdge.SourceID == "C" {
			foundC = true
		}
		if r.DerivedEdge.SourceID == "E" {
			foundE = true
		}
		if r.DerivedEdge.SourceID == "A" {
			t.Error("Result from first edge set should not appear in second evaluation")
		}
	}

	if !foundC || !foundE {
		t.Error("Missing expected results from second evaluation")
	}
}

// =============================================================================
// Performance Tests (ensure optimizations work correctly)
// =============================================================================

func TestRuleEvaluator_IndexedMatchingProducesCorrectResults(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Complex rule with multiple conditions
	rule := InferenceRule{
		ID:   "transitive",
		Name: "Transitive Closure",
		Head: RuleCondition{
			Subject:   "?a",
			Predicate: "reaches",
			Object:    "?c",
		},
		Body: []RuleCondition{
			{Subject: "?a", Predicate: "edge", Object: "?b"},
			{Subject: "?b", Predicate: "edge", Object: "?c"},
		},
		Priority: 1,
		Enabled:  true,
	}

	// Create a graph: A->B->C->D
	edges := []Edge{
		{Source: "A", Predicate: "edge", Target: "B"},
		{Source: "B", Predicate: "edge", Target: "C"},
		{Source: "C", Predicate: "edge", Target: "D"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	// Expected: A reaches C, B reaches D
	if len(results) != 2 {
		t.Fatalf("Expected 2 transitive results, got %d", len(results))
	}

	expected := map[string]string{
		"A": "C",
		"B": "D",
	}

	for _, r := range results {
		expectedTarget, ok := expected[r.DerivedEdge.SourceID]
		if !ok {
			t.Errorf("Unexpected source: %s", r.DerivedEdge.SourceID)
			continue
		}
		if r.DerivedEdge.TargetID != expectedTarget {
			t.Errorf("Expected %s->%s, got %s->%s",
				r.DerivedEdge.SourceID, expectedTarget,
				r.DerivedEdge.SourceID, r.DerivedEdge.TargetID)
		}
	}
}

func TestRuleEvaluator_filterMatches(t *testing.T) {
	evaluator := NewRuleEvaluator()

	candidates := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "C"},
		{Source: "A", Predicate: "imports", Target: "D"},
	}

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	bindings := map[string]string{"?x": "A"}

	matches := evaluator.filterMatches(candidates, condition, bindings)

	// Should filter out the imports edge
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(matches))
	}

	for _, m := range matches {
		if m.edge.Predicate != "calls" {
			t.Errorf("Expected predicate 'calls', got %s", m.edge.Predicate)
		}
	}
}
