package inference

import (
	"context"
	"sync"
	"testing"
	"time"
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

// =============================================================================
// W4P.28: Cache Isolation Tests
// =============================================================================

func TestCloneConditionMatches(t *testing.T) {
	original := []conditionMatch{
		{
			edge:     Edge{Source: "A", Predicate: "rel", Target: "B"},
			bindings: map[string]string{"?x": "A", "?y": "B"},
		},
		{
			edge:     Edge{Source: "C", Predicate: "rel", Target: "D"},
			bindings: map[string]string{"?x": "C", "?y": "D"},
		},
	}

	cloned := cloneConditionMatches(original)

	// Verify length matches
	if len(cloned) != len(original) {
		t.Errorf("Expected %d matches, got %d", len(original), len(cloned))
	}

	// Verify contents are equal
	for i := range original {
		if cloned[i].edge != original[i].edge {
			t.Errorf("Edge mismatch at index %d", i)
		}
		for k, v := range original[i].bindings {
			if cloned[i].bindings[k] != v {
				t.Errorf("Bindings mismatch at index %d, key %s", i, k)
			}
		}
	}

	// Verify modification isolation - modify clone bindings
	cloned[0].bindings["?x"] = "MODIFIED"
	if original[0].bindings["?x"] == "MODIFIED" {
		t.Error("Modifying cloned bindings affected original")
	}

	// Verify modification isolation - modify clone edge
	cloned[0].edge.Source = "MODIFIED"
	if original[0].edge.Source == "MODIFIED" {
		t.Error("Modifying cloned edge affected original")
	}

	// Verify modification isolation - add to clone bindings
	cloned[0].bindings["?new"] = "NEW"
	if _, exists := original[0].bindings["?new"]; exists {
		t.Error("Adding to cloned bindings affected original")
	}
}

func TestCloneConditionMatches_Nil(t *testing.T) {
	cloned := cloneConditionMatches(nil)
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestCloneConditionMatches_Empty(t *testing.T) {
	cloned := cloneConditionMatches([]conditionMatch{})
	if cloned == nil {
		t.Error("Clone of empty slice should not be nil")
	}
	if len(cloned) != 0 {
		t.Error("Clone of empty slice should be empty")
	}
}

func TestCloneBindings(t *testing.T) {
	original := map[string]string{
		"?x": "A",
		"?y": "B",
		"?z": "C",
	}

	cloned := cloneBindings(original)

	// Verify length matches
	if len(cloned) != len(original) {
		t.Errorf("Expected %d bindings, got %d", len(original), len(cloned))
	}

	// Verify contents are equal
	for k, v := range original {
		if cloned[k] != v {
			t.Errorf("Binding mismatch for key %s", k)
		}
	}

	// Verify modification isolation
	cloned["?x"] = "MODIFIED"
	if original["?x"] == "MODIFIED" {
		t.Error("Modifying cloned bindings affected original")
	}

	cloned["?new"] = "NEW"
	if _, exists := original["?new"]; exists {
		t.Error("Adding to cloned bindings affected original")
	}
}

func TestCloneBindings_Nil(t *testing.T) {
	cloned := cloneBindings(nil)
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestCloneBindings_Empty(t *testing.T) {
	cloned := cloneBindings(map[string]string{})
	if cloned == nil {
		t.Error("Clone of empty map should not be nil")
	}
	if len(cloned) != 0 {
		t.Error("Clone of empty map should be empty")
	}
}

// TestMatchConditionCached_Isolation verifies that caller modifications
// to returned matches do not affect cached data (W4P.28).
func TestMatchConditionCached_Isolation(t *testing.T) {
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

	// First call - populates cache
	matches1 := evaluator.matchConditionCached(condition, bindings)
	if len(matches1) != 2 {
		t.Fatalf("Expected 2 matches, got %d", len(matches1))
	}

	// Corrupt the returned matches
	matches1[0].bindings["?x"] = "CORRUPTED"
	matches1[0].edge.Source = "CORRUPTED"
	matches1 = append(matches1, conditionMatch{
		edge:     Edge{Source: "EXTRA", Predicate: "EXTRA", Target: "EXTRA"},
		bindings: map[string]string{"?extra": "EXTRA"},
	})

	// Second call - should get uncorrupted data from cache
	matches2 := evaluator.matchConditionCached(condition, bindings)

	if len(matches2) != 2 {
		t.Errorf("Expected 2 matches from cache, got %d (cache was corrupted)", len(matches2))
	}

	for _, m := range matches2 {
		if m.bindings["?x"] == "CORRUPTED" {
			t.Error("Cache was corrupted: bindings contain CORRUPTED value")
		}
		if m.edge.Source == "CORRUPTED" {
			t.Error("Cache was corrupted: edge source contains CORRUPTED value")
		}
	}

	// Verify original binding value is preserved
	foundOriginalBinding := false
	for _, m := range matches2 {
		if m.bindings["?x"] == "A" {
			foundOriginalBinding = true
			break
		}
	}
	if !foundOriginalBinding {
		t.Error("Original binding value ?x=A not found in cached matches")
	}
}

// TestMatchConditionCached_IsolationBetweenCalls verifies that modifications
// to one returned result don't affect other callers' results (W4P.28).
func TestMatchConditionCached_IsolationBetweenCalls(t *testing.T) {
	evaluator := NewRuleEvaluator()

	edges := []Edge{
		{Source: "A", Predicate: "rel", Target: "B"},
	}

	evaluator.clearCache()
	evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "rel",
		Object:    "?y",
	}

	bindings := map[string]string{}

	// Get two separate results
	result1 := evaluator.matchConditionCached(condition, bindings)
	result2 := evaluator.matchConditionCached(condition, bindings)

	if len(result1) != 1 || len(result2) != 1 {
		t.Fatal("Expected 1 match each")
	}

	// Modify result1
	result1[0].bindings["?x"] = "MODIFIED_BY_CALLER1"

	// result2 should be unaffected
	if result2[0].bindings["?x"] == "MODIFIED_BY_CALLER1" {
		t.Error("Modification to result1 affected result2 - isolation failure")
	}
}

// TestMatchConditionCached_HappyPath verifies matches are returned correctly (W4P.28).
func TestMatchConditionCached_HappyPath(t *testing.T) {
	evaluator := NewRuleEvaluator()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "C"},
		{Source: "B", Predicate: "calls", Target: "D"},
	}

	evaluator.clearCache()
	evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

	// Test 1: Match with bound source
	condition1 := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}
	bindings1 := map[string]string{"?x": "A"}

	matches1 := evaluator.matchConditionCached(condition1, bindings1)
	if len(matches1) != 2 {
		t.Errorf("Expected 2 matches for source A, got %d", len(matches1))
	}

	for _, m := range matches1 {
		if m.edge.Source != "A" {
			t.Errorf("Expected source A, got %s", m.edge.Source)
		}
		if m.bindings["?x"] != "A" {
			t.Errorf("Expected ?x=A in bindings, got ?x=%s", m.bindings["?x"])
		}
	}

	// Test 2: Match with no bindings (all edges with predicate "calls")
	bindings2 := map[string]string{}
	matches2 := evaluator.matchConditionCached(condition1, bindings2)
	if len(matches2) != 3 {
		t.Errorf("Expected 3 matches for predicate calls, got %d", len(matches2))
	}

	// Test 3: Verify cache hit returns same data
	matches3 := evaluator.matchConditionCached(condition1, bindings1)
	if len(matches3) != len(matches1) {
		t.Errorf("Cache hit returned different count: %d vs %d", len(matches3), len(matches1))
	}
}

// TestMatchConditionCached_ConcurrentModification verifies that concurrent
// modifications to returned matches by different callers don't interfere
// with each other (W4P.28). Each goroutine uses its own evaluator to avoid
// the pre-existing issue of unsynchronized cache map access.
// This test is designed to be run with -race flag.
func TestMatchConditionCached_ConcurrentModification(t *testing.T) {
	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "C"},
		{Source: "B", Predicate: "calls", Target: "D"},
	}

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}
	bindings := map[string]string{"?x": "A"}

	const numGoroutines = 10
	const numIterations = 100

	// Channel to collect error messages
	errCh := make(chan string, numGoroutines*numIterations)

	// Use WaitGroup for proper synchronization
	var wg sync.WaitGroup

	// Start goroutines that each use their own evaluator
	// and verify that modifications to returned results don't affect
	// subsequent queries on the same evaluator
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			// Each goroutine has its own evaluator
			evaluator := NewRuleEvaluator()
			evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

			for i := 0; i < numIterations; i++ {
				// Get cached matches
				matches := evaluator.matchConditionCached(condition, bindings)

				// Verify we got the expected number of matches
				if len(matches) != 2 {
					errCh <- "unexpected match count"
					continue
				}

				// Verify data is not corrupted from previous iteration
				for _, m := range matches {
					if m.edge.Source != "A" {
						errCh <- "corrupted source: got " + m.edge.Source + ", expected A"
					}
					if m.bindings["?x"] != "A" {
						errCh <- "corrupted binding: got " + m.bindings["?x"] + ", expected A"
					}
				}

				// Modify our local copy (should not affect cache or future calls)
				matches[0].bindings["?x"] = "MODIFIED_BY_GOROUTINE"
				matches[0].edge.Source = "MODIFIED_BY_GOROUTINE"
			}
		}(g)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines completed
	case <-time.After(30 * time.Second):
		t.Fatal("Test timed out waiting for goroutines")
	}

	// Check for errors
	close(errCh)
	var errors []string
	for errMsg := range errCh {
		errors = append(errors, errMsg)
	}

	if len(errors) > 0 {
		t.Errorf("Concurrent modification test failed with %d errors", len(errors))
		// Show first few errors
		for i, errMsg := range errors {
			if i >= 5 {
				t.Errorf("... and %d more errors", len(errors)-5)
				break
			}
			t.Errorf("Error %d: %s", i+1, errMsg)
		}
	}
}

// TestMatchConditionCached_RapidSequentialModification verifies that rapid
// sequential calls with modifications don't corrupt the cache (W4P.28).
func TestMatchConditionCached_RapidSequentialModification(t *testing.T) {
	evaluator := NewRuleEvaluator()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "C"},
	}

	evaluator.edgeIndex = evaluator.buildEdgeIndex(edges)

	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}
	bindings := map[string]string{"?x": "A"}

	const numIterations = 1000

	for i := 0; i < numIterations; i++ {
		// Get matches from cache
		matches := evaluator.matchConditionCached(condition, bindings)

		// Verify we got correct data
		if len(matches) != 2 {
			t.Fatalf("Iteration %d: expected 2 matches, got %d", i, len(matches))
		}

		for j, m := range matches {
			if m.edge.Source != "A" {
				t.Fatalf("Iteration %d, match %d: corrupted source %s", i, j, m.edge.Source)
			}
			if m.bindings["?x"] != "A" {
				t.Fatalf("Iteration %d, match %d: corrupted binding %s", i, j, m.bindings["?x"])
			}
		}

		// Aggressively corrupt the returned data
		matches[0].bindings["?x"] = "CORRUPTED"
		matches[0].bindings["?y"] = "CORRUPTED"
		matches[0].edge.Source = "CORRUPTED"
		matches[0].edge.Target = "CORRUPTED"
		matches[0].edge.Predicate = "CORRUPTED"
		matches = append(matches, conditionMatch{
			edge:     Edge{Source: "INJECTED", Predicate: "INJECTED", Target: "INJECTED"},
			bindings: map[string]string{"?injected": "INJECTED"},
		})
	}
}

// TestEvaluateRule_CacheIsolation verifies the full evaluation flow
// maintains cache isolation (W4P.28).
func TestEvaluateRule_CacheIsolation(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "test_rule",
		Name: "Test Isolation",
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
		{Source: "C", Predicate: "rel", Target: "D"},
	}

	// First evaluation
	results1 := evaluator.EvaluateRule(ctx, rule, edges)
	if len(results1) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results1))
	}

	// Try to corrupt the internal state through results (shouldn't be possible
	// because results are new objects, but this verifies the full flow)
	for i := range results1 {
		results1[i].DerivedEdge.SourceID = "CORRUPTED"
	}

	// Second evaluation should still work correctly
	results2 := evaluator.EvaluateRule(ctx, rule, edges)
	if len(results2) != 2 {
		t.Fatalf("Expected 2 results on second evaluation, got %d", len(results2))
	}

	for _, r := range results2 {
		if r.DerivedEdge.SourceID == "CORRUPTED" {
			t.Error("Second evaluation returned corrupted data")
		}
	}
}

// =============================================================================
// W4P.40: Hash-Based Cache Key Tests
// =============================================================================

// TestConditionCacheKey_Uniqueness verifies that different inputs produce different keys.
func TestConditionCacheKey_Uniqueness(t *testing.T) {
	evaluator := NewRuleEvaluator()

	testCases := []struct {
		name       string
		condition1 RuleCondition
		bindings1  map[string]string
		condition2 RuleCondition
		bindings2  map[string]string
	}{
		{
			name:       "DifferentSubject",
			condition1: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings1:  map[string]string{},
			condition2: RuleCondition{Subject: "?z", Predicate: "calls", Object: "?y"},
			bindings2:  map[string]string{},
		},
		{
			name:       "DifferentPredicate",
			condition1: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings1:  map[string]string{},
			condition2: RuleCondition{Subject: "?x", Predicate: "imports", Object: "?y"},
			bindings2:  map[string]string{},
		},
		{
			name:       "DifferentObject",
			condition1: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings1:  map[string]string{},
			condition2: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?z"},
			bindings2:  map[string]string{},
		},
		{
			name:       "DifferentBindingValue",
			condition1: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings1:  map[string]string{"?x": "A"},
			condition2: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings2:  map[string]string{"?x": "B"},
		},
		{
			name:       "BoundVsUnbound",
			condition1: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings1:  map[string]string{"?x": "A"},
			condition2: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings2:  map[string]string{},
		},
		{
			name:       "DifferentBoundVariable",
			condition1: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings1:  map[string]string{"?x": "A"},
			condition2: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings2:  map[string]string{"?y": "A"},
		},
		{
			name:       "ConstantVsVariable",
			condition1: RuleCondition{Subject: "NodeA", Predicate: "calls", Object: "?y"},
			bindings1:  map[string]string{},
			condition2: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings2:  map[string]string{},
		},
		{
			name:       "MultipleBindings",
			condition1: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings1:  map[string]string{"?x": "A", "?y": "B"},
			condition2: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings2:  map[string]string{"?x": "A", "?y": "C"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			key1 := evaluator.conditionCacheKey(tc.condition1, tc.bindings1)
			key2 := evaluator.conditionCacheKey(tc.condition2, tc.bindings2)

			if key1 == key2 {
				t.Errorf("Expected different keys for different inputs:\n  key1: %s\n  key2: %s", key1, key2)
			}
		})
	}
}

// TestConditionCacheKey_Consistency verifies that same inputs always produce same key.
func TestConditionCacheKey_Consistency(t *testing.T) {
	evaluator := NewRuleEvaluator()

	testCases := []struct {
		name      string
		condition RuleCondition
		bindings  map[string]string
	}{
		{
			name:      "SimpleCondition",
			condition: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings:  map[string]string{},
		},
		{
			name:      "WithBinding",
			condition: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			bindings:  map[string]string{"?x": "NodeA"},
		},
		{
			name:      "WithMultipleBindings",
			condition: RuleCondition{Subject: "?x", Predicate: "?p", Object: "?y"},
			bindings:  map[string]string{"?x": "A", "?y": "B", "?p": "calls"},
		},
		{
			name:      "ConstantCondition",
			condition: RuleCondition{Subject: "NodeA", Predicate: "calls", Object: "NodeB"},
			bindings:  map[string]string{},
		},
		{
			name:      "MixedCondition",
			condition: RuleCondition{Subject: "?x", Predicate: "calls", Object: "NodeB"},
			bindings:  map[string]string{"?x": "NodeA"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate key multiple times
			const iterations = 100
			keys := make([]string, iterations)

			for i := 0; i < iterations; i++ {
				keys[i] = evaluator.conditionCacheKey(tc.condition, tc.bindings)
			}

			// Verify all keys are identical
			for i := 1; i < iterations; i++ {
				if keys[i] != keys[0] {
					t.Errorf("Inconsistent key at iteration %d:\n  expected: %s\n  got: %s", i, keys[0], keys[i])
				}
			}
		})
	}
}

// TestConditionCacheKey_BindingOrderIndependent verifies that binding order doesn't affect key.
func TestConditionCacheKey_BindingOrderIndependent(t *testing.T) {
	evaluator := NewRuleEvaluator()

	condition := RuleCondition{Subject: "?x", Predicate: "?p", Object: "?y"}

	// Create bindings in different orders
	bindings1 := map[string]string{"?x": "A", "?y": "B", "?p": "calls"}
	bindings2 := map[string]string{"?p": "calls", "?x": "A", "?y": "B"}
	bindings3 := map[string]string{"?y": "B", "?p": "calls", "?x": "A"}

	key1 := evaluator.conditionCacheKey(condition, bindings1)
	key2 := evaluator.conditionCacheKey(condition, bindings2)
	key3 := evaluator.conditionCacheKey(condition, bindings3)

	if key1 != key2 || key2 != key3 {
		t.Errorf("Keys should be identical regardless of binding order:\n  key1: %s\n  key2: %s\n  key3: %s", key1, key2, key3)
	}
}

// TestConditionCacheKey_IrrelevantBindingsIgnored verifies bindings for
// non-variables in the condition are ignored.
func TestConditionCacheKey_IrrelevantBindingsIgnored(t *testing.T) {
	evaluator := NewRuleEvaluator()

	condition := RuleCondition{Subject: "NodeA", Predicate: "calls", Object: "NodeB"}

	// All constants - bindings should be ignored
	bindings1 := map[string]string{}
	bindings2 := map[string]string{"?x": "ignored", "?y": "also_ignored"}

	key1 := evaluator.conditionCacheKey(condition, bindings1)
	key2 := evaluator.conditionCacheKey(condition, bindings2)

	if key1 != key2 {
		t.Errorf("Irrelevant bindings should not affect key:\n  key1: %s\n  key2: %s", key1, key2)
	}
}

// TestCollectRelevantVars tests the helper function.
func TestCollectRelevantVars(t *testing.T) {
	testCases := []struct {
		name      string
		condition RuleCondition
		expected  int
	}{
		{
			name:      "AllVariables",
			condition: RuleCondition{Subject: "?x", Predicate: "?p", Object: "?y"},
			expected:  3,
		},
		{
			name:      "NoVariables",
			condition: RuleCondition{Subject: "A", Predicate: "calls", Object: "B"},
			expected:  0,
		},
		{
			name:      "OneVariable",
			condition: RuleCondition{Subject: "?x", Predicate: "calls", Object: "B"},
			expected:  1,
		},
		{
			name:      "TwoVariables",
			condition: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
			expected:  2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vars := collectRelevantVars(tc.condition)
			if len(vars) != tc.expected {
				t.Errorf("Expected %d variables, got %d", tc.expected, len(vars))
			}
		})
	}
}

// TestFormatHashKey tests the hash formatting function.
func TestFormatHashKey(t *testing.T) {
	testCases := []struct {
		hash     uint64
		expected string
	}{
		{0, "0000000000000000"},
		{1, "0000000000000001"},
		{15, "000000000000000f"},
		{16, "0000000000000010"},
		{255, "00000000000000ff"},
		{0xdeadbeef, "00000000deadbeef"},
		{0xffffffffffffffff, "ffffffffffffffff"},
	}

	for _, tc := range testCases {
		result := formatHashKey(tc.hash)
		if result != tc.expected {
			t.Errorf("formatHashKey(%x) = %s, expected %s", tc.hash, result, tc.expected)
		}
	}
}

// TestConditionCacheKey_HashCollisionResistance verifies that similar inputs
// don't produce hash collisions.
func TestConditionCacheKey_HashCollisionResistance(t *testing.T) {
	evaluator := NewRuleEvaluator()
	seen := make(map[string]string)

	// Generate many different condition/binding combinations
	predicates := []string{"calls", "imports", "uses", "extends", "implements"}
	nodes := []string{"A", "B", "C", "D", "E", "node1", "node2", "main", "helper"}

	for _, subj := range append(nodes, "?x", "?a", "?s") {
		for _, pred := range append(predicates, "?p") {
			for _, obj := range append(nodes, "?y", "?b", "?o") {
				condition := RuleCondition{Subject: subj, Predicate: pred, Object: obj}

				// Collect relevant variables in the condition
				relevantVars := collectRelevantVars(condition)

				// Test with various binding combinations
				bindingCombos := []map[string]string{
					{},
					{"?x": "A"},
					{"?y": "B"},
					{"?x": "A", "?y": "B"},
					{"?p": "calls"},
					{"?a": "NodeA"},
					{"?s": "NodeS"},
					{"?b": "NodeB"},
					{"?o": "NodeO"},
				}

				for _, bindings := range bindingCombos {
					key := evaluator.conditionCacheKey(condition, bindings)

					// Create a descriptor that only includes relevant bindings
					// (matches the actual key generation logic)
					desc := condition.Subject + "|" + condition.Predicate + "|" + condition.Object
					for _, v := range relevantVars {
						if val, ok := bindings[v]; ok {
							desc += "|" + v + "=" + val
						}
					}

					if prevDesc, exists := seen[key]; exists && prevDesc != desc {
						t.Errorf("Hash collision detected:\n  key: %s\n  input1: %s\n  input2: %s", key, prevDesc, desc)
					}
					seen[key] = desc
				}
			}
		}
	}

	t.Logf("Tested %d unique key generations without collision", len(seen))
}
