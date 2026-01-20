package relations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// InferenceRule Creation Tests
// =============================================================================

func TestNewInferenceRule_BasicCreation(t *testing.T) {
	head := RuleCondition{
		Subject:   "?x",
		Predicate: "ancestor",
		Object:    "?z",
	}

	body := []RuleCondition{
		{Subject: "?x", Predicate: "parent", Object: "?y"},
		{Subject: "?y", Predicate: "ancestor", Object: "?z"},
	}

	rule := NewInferenceRule("rule-1", "TransitiveAncestor", head, body)

	assert.Equal(t, "rule-1", rule.ID)
	assert.Equal(t, "TransitiveAncestor", rule.Name)
	assert.Equal(t, head, rule.Head)
	assert.Equal(t, body, rule.Body)
	assert.True(t, rule.Enabled)
	assert.Equal(t, 0, rule.Stratum)
	assert.False(t, rule.Negation)
	assert.Equal(t, "ancestor", rule.HeadPredicate)
	assert.Contains(t, rule.BodyPredicates, "parent")
	assert.Contains(t, rule.BodyPredicates, "ancestor")
}

func TestNewInferenceRule_WithNegation(t *testing.T) {
	head := RuleCondition{
		Subject:   "?x",
		Predicate: "canAccess",
		Object:    "?y",
	}

	body := []RuleCondition{
		{Subject: "?x", Predicate: "hasPermission", Object: "?y"},
		{Subject: "?x", Predicate: "banned", Object: "?y", Negated: true},
	}

	rule := NewInferenceRule("rule-2", "AccessWithoutBan", head, body)

	assert.True(t, rule.Negation)
	assert.Contains(t, rule.BodyPredicates, "hasPermission")
	assert.Contains(t, rule.BodyPredicates, "banned")
}

func TestNewInferenceRule_VariablePredicatesNotExtracted(t *testing.T) {
	head := RuleCondition{
		Subject:   "?x",
		Predicate: "related",
		Object:    "?z",
	}

	body := []RuleCondition{
		{Subject: "?x", Predicate: "?p", Object: "?y"}, // Variable predicate
		{Subject: "?y", Predicate: "knows", Object: "?z"},
	}

	rule := NewInferenceRule("rule-3", "GenericTransitive", head, body)

	// Variable predicate ?p should not be in BodyPredicates
	assert.NotContains(t, rule.BodyPredicates, "?p")
	assert.Contains(t, rule.BodyPredicates, "knows")
}

// =============================================================================
// InferenceRule Validation Tests
// =============================================================================

func TestInferenceRule_Validate_Valid(t *testing.T) {
	rule := NewInferenceRule(
		"valid-rule",
		"ValidRule",
		RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "edge", Object: "?y"},
		},
	)

	err := rule.Validate()
	assert.NoError(t, err)
}

func TestInferenceRule_Validate_EmptyID(t *testing.T) {
	rule := &InferenceRule{
		ID:   "",
		Name: "TestRule",
		Head: RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	}

	err := rule.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ID cannot be empty")
}

func TestInferenceRule_Validate_EmptyName(t *testing.T) {
	rule := &InferenceRule{
		ID:   "rule-1",
		Name: "",
		Head: RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	}

	err := rule.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name cannot be empty")
}

func TestInferenceRule_Validate_EmptyBody(t *testing.T) {
	rule := &InferenceRule{
		ID:   "rule-1",
		Name: "TestRule",
		Head: RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		Body: []RuleCondition{},
	}

	err := rule.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "body cannot be empty")
}

func TestInferenceRule_Validate_UnboundHeadVariable(t *testing.T) {
	rule := &InferenceRule{
		ID:   "rule-1",
		Name: "TestRule",
		Head: RuleCondition{Subject: "?x", Predicate: "test", Object: "?z"}, // ?z not in body
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	}

	err := rule.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "head variable ?z not found in body")
}

// =============================================================================
// InferenceRule GetVariables Tests
// =============================================================================

func TestInferenceRule_GetVariables(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"TestRule",
		RuleCondition{Subject: "?x", Predicate: "connected", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "edge", Object: "?y"},
			{Subject: "?y", Predicate: "edge", Object: "?z"},
		},
	)

	vars := rule.GetVariables()

	assert.Len(t, vars, 3)
	assert.Contains(t, vars, "?x")
	assert.Contains(t, vars, "?y")
	assert.Contains(t, vars, "?z")
}

func TestInferenceRule_GetVariables_WithConstants(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"TestRule",
		RuleCondition{Subject: "?x", Predicate: "isPublic", Object: "true"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "visibility", Object: "public"},
		},
	)

	vars := rule.GetVariables()

	assert.Len(t, vars, 1)
	assert.Contains(t, vars, "?x")
}

// =============================================================================
// InferenceRule DependsOn Tests
// =============================================================================

func TestInferenceRule_DependsOn_True(t *testing.T) {
	// Rule A produces "ancestor"
	ruleA := NewInferenceRule(
		"rule-a",
		"RuleA",
		RuleCondition{Subject: "?x", Predicate: "ancestor", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "parent", Object: "?y"},
		},
	)

	// Rule B uses "ancestor" in body
	ruleB := NewInferenceRule(
		"rule-b",
		"RuleB",
		RuleCondition{Subject: "?x", Predicate: "related", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "ancestor", Object: "?y"},
			{Subject: "?y", Predicate: "sibling", Object: "?z"},
		},
	)

	assert.True(t, ruleB.DependsOn(ruleA))
	assert.False(t, ruleA.DependsOn(ruleB)) // ruleA doesn't use "related"
}

func TestInferenceRule_DependsOn_False(t *testing.T) {
	ruleA := NewInferenceRule(
		"rule-a",
		"RuleA",
		RuleCondition{Subject: "?x", Predicate: "typeA", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "sourceA", Object: "?y"},
		},
	)

	ruleB := NewInferenceRule(
		"rule-b",
		"RuleB",
		RuleCondition{Subject: "?x", Predicate: "typeB", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "sourceB", Object: "?y"},
		},
	)

	assert.False(t, ruleA.DependsOn(ruleB))
	assert.False(t, ruleB.DependsOn(ruleA))
}

func TestInferenceRule_DependsOn_Nil(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"TestRule",
		RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	)

	assert.False(t, rule.DependsOn(nil))
}

func TestInferenceRule_DependsOn_SelfDependency(t *testing.T) {
	// Recursive rule: ancestor(x,z) :- parent(x,y), ancestor(y,z)
	rule := NewInferenceRule(
		"rule-1",
		"TransitiveAncestor",
		RuleCondition{Subject: "?x", Predicate: "ancestor", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "parent", Object: "?y"},
			{Subject: "?y", Predicate: "ancestor", Object: "?z"},
		},
	)

	assert.True(t, rule.DependsOn(rule))
}

// =============================================================================
// InferenceRule HasNegativeDependencyOn Tests
// =============================================================================

func TestInferenceRule_HasNegativeDependencyOn_True(t *testing.T) {
	// Rule A produces "banned"
	ruleA := NewInferenceRule(
		"rule-a",
		"RuleA",
		RuleCondition{Subject: "?x", Predicate: "banned", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "violation", Object: "?y"},
		},
	)

	// Rule B negates "banned"
	ruleB := NewInferenceRule(
		"rule-b",
		"RuleB",
		RuleCondition{Subject: "?x", Predicate: "canAccess", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "hasPermission", Object: "?y"},
			{Subject: "?x", Predicate: "banned", Object: "?y", Negated: true},
		},
	)

	assert.True(t, ruleB.HasNegativeDependencyOn(ruleA))
	assert.False(t, ruleA.HasNegativeDependencyOn(ruleB))
}

func TestInferenceRule_HasNegativeDependencyOn_False_NoNegation(t *testing.T) {
	ruleA := NewInferenceRule(
		"rule-a",
		"RuleA",
		RuleCondition{Subject: "?x", Predicate: "typeA", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	)

	ruleB := NewInferenceRule(
		"rule-b",
		"RuleB",
		RuleCondition{Subject: "?x", Predicate: "typeB", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "typeA", Object: "?y"}, // Uses typeA but not negated
		},
	)

	assert.False(t, ruleB.HasNegativeDependencyOn(ruleA))
}

func TestInferenceRule_HasNegativeDependencyOn_Nil(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"TestRule",
		RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y", Negated: true},
		},
	)

	assert.False(t, rule.HasNegativeDependencyOn(nil))
}

// =============================================================================
// InferenceRule String Tests
// =============================================================================

func TestInferenceRule_String(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"TransitiveReachable",
		RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "edge", Object: "?y"},
			{Subject: "?y", Predicate: "reachable", Object: "?z"},
		},
	)
	rule.Stratum = 2

	str := rule.String()

	assert.Contains(t, str, "TransitiveReachable")
	assert.Contains(t, str, "[S2]")
	assert.Contains(t, str, "reachable")
	assert.Contains(t, str, "edge")
	assert.Contains(t, str, ":-")
}

func TestInferenceRule_String_WithNegation(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"AccessRule",
		RuleCondition{Subject: "?x", Predicate: "canAccess", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "hasPermission", Object: "?y"},
			{Subject: "?x", Predicate: "banned", Object: "?y", Negated: true},
		},
	)

	str := rule.String()

	assert.Contains(t, str, "NOT")
	assert.Contains(t, str, "banned")
}

// =============================================================================
// InferenceRule Clone Tests
// =============================================================================

func TestInferenceRule_Clone(t *testing.T) {
	original := NewInferenceRule(
		"rule-1",
		"OriginalRule",
		RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	)
	original.Stratum = 5
	original.Priority = 10

	clone := original.Clone()

	// Should have same values
	assert.Equal(t, original.ID, clone.ID)
	assert.Equal(t, original.Name, clone.Name)
	assert.Equal(t, original.Head, clone.Head)
	assert.Equal(t, original.Body, clone.Body)
	assert.Equal(t, original.Stratum, clone.Stratum)
	assert.Equal(t, original.Priority, clone.Priority)

	// Should be independent
	clone.Stratum = 10
	clone.Body[0].Subject = "?modified"

	assert.Equal(t, 5, original.Stratum)
	assert.Equal(t, "?x", original.Body[0].Subject)
}

// =============================================================================
// RuleCondition Unify Tests
// =============================================================================

func TestRuleCondition_Unify_AllVariables(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "?p",
		Object:    "?y",
	}

	edge := EdgeKey{
		Subject:   "Alice",
		Predicate: "knows",
		Object:    "Bob",
	}

	bindings := make(map[string]string)
	newBindings, ok := condition.Unify(bindings, edge)

	assert.True(t, ok)
	assert.Equal(t, "Alice", newBindings["?x"])
	assert.Equal(t, "knows", newBindings["?p"])
	assert.Equal(t, "Bob", newBindings["?y"])
}

func TestRuleCondition_Unify_WithConstants(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "knows",
		Object:    "?y",
	}

	edge := EdgeKey{
		Subject:   "Alice",
		Predicate: "knows",
		Object:    "Bob",
	}

	bindings := make(map[string]string)
	newBindings, ok := condition.Unify(bindings, edge)

	assert.True(t, ok)
	assert.Equal(t, "Alice", newBindings["?x"])
	assert.Equal(t, "Bob", newBindings["?y"])
}

func TestRuleCondition_Unify_ConstantMismatch(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "likes",
		Object:    "?y",
	}

	edge := EdgeKey{
		Subject:   "Alice",
		Predicate: "knows", // Different from "likes"
		Object:    "Bob",
	}

	bindings := make(map[string]string)
	_, ok := condition.Unify(bindings, edge)

	assert.False(t, ok)
}

func TestRuleCondition_Unify_ConsistentBindings(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "knows",
		Object:    "?x", // Same variable as subject
	}

	// Self-loop: Alice knows Alice
	edge := EdgeKey{
		Subject:   "Alice",
		Predicate: "knows",
		Object:    "Alice",
	}

	bindings := make(map[string]string)
	newBindings, ok := condition.Unify(bindings, edge)

	assert.True(t, ok)
	assert.Equal(t, "Alice", newBindings["?x"])
}

func TestRuleCondition_Unify_InconsistentBindings(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "knows",
		Object:    "?x", // Same variable as subject
	}

	// Not a self-loop: Alice knows Bob
	edge := EdgeKey{
		Subject:   "Alice",
		Predicate: "knows",
		Object:    "Bob",
	}

	bindings := make(map[string]string)
	_, ok := condition.Unify(bindings, edge)

	assert.False(t, ok)
}

func TestRuleCondition_Unify_PreexistingBindings(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "knows",
		Object:    "?y",
	}

	edge := EdgeKey{
		Subject:   "Alice",
		Predicate: "knows",
		Object:    "Bob",
	}

	// Pre-existing binding that matches
	bindings := map[string]string{"?x": "Alice"}
	newBindings, ok := condition.Unify(bindings, edge)

	assert.True(t, ok)
	assert.Equal(t, "Alice", newBindings["?x"])
	assert.Equal(t, "Bob", newBindings["?y"])
}

func TestRuleCondition_Unify_ConflictingPreexistingBinding(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "knows",
		Object:    "?y",
	}

	edge := EdgeKey{
		Subject:   "Alice",
		Predicate: "knows",
		Object:    "Bob",
	}

	// Pre-existing binding that conflicts
	bindings := map[string]string{"?x": "Charlie"}
	_, ok := condition.Unify(bindings, edge)

	assert.False(t, ok)
}

// =============================================================================
// RuleCondition Substitute Tests
// =============================================================================

func TestRuleCondition_Substitute(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "knows",
		Object:    "?y",
	}

	bindings := map[string]string{
		"?x": "Alice",
		"?y": "Bob",
	}

	substituted := condition.Substitute(bindings)

	assert.Equal(t, "Alice", substituted.Subject)
	assert.Equal(t, "knows", substituted.Predicate)
	assert.Equal(t, "Bob", substituted.Object)
}

func TestRuleCondition_Substitute_PartialBindings(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "?p",
		Object:    "?y",
	}

	bindings := map[string]string{
		"?x": "Alice",
		// ?p and ?y not bound
	}

	substituted := condition.Substitute(bindings)

	assert.Equal(t, "Alice", substituted.Subject)
	assert.Equal(t, "?p", substituted.Predicate) // Unchanged
	assert.Equal(t, "?y", substituted.Object)    // Unchanged
}

func TestRuleCondition_Substitute_PreservesNegation(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "banned",
		Object:    "?y",
		Negated:   true,
	}

	bindings := map[string]string{
		"?x": "Alice",
		"?y": "Resource1",
	}

	substituted := condition.Substitute(bindings)

	assert.Equal(t, "Alice", substituted.Subject)
	assert.Equal(t, "Resource1", substituted.Object)
	assert.True(t, substituted.Negated)
}

// =============================================================================
// IsRuleVariable Tests
// =============================================================================

func TestIsRuleVariable(t *testing.T) {
	assert.True(t, IsRuleVariable("?x"))
	assert.True(t, IsRuleVariable("?variable"))
	assert.True(t, IsRuleVariable("?X"))
	assert.True(t, IsRuleVariable("?123"))

	assert.False(t, IsRuleVariable("x"))
	assert.False(t, IsRuleVariable("variable"))
	assert.False(t, IsRuleVariable(""))
	assert.False(t, IsRuleVariable("Alice"))
	assert.False(t, IsRuleVariable("knows"))
}

// =============================================================================
// Stratification Support Tests
// =============================================================================

func TestInferenceRule_Stratum(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"TestRule",
		RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	)

	// Default stratum is 0
	assert.Equal(t, 0, rule.Stratum)

	// Can be modified
	rule.Stratum = 3
	assert.Equal(t, 3, rule.Stratum)
}

func TestInferenceRule_Priority(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"TestRule",
		RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	)

	// Default priority is 0
	assert.Equal(t, 0, rule.Priority)

	// Can be modified
	rule.Priority = 100
	assert.Equal(t, 100, rule.Priority)
}

func TestInferenceRule_Enabled(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"TestRule",
		RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "source", Object: "?y"},
		},
	)

	// Default is enabled
	assert.True(t, rule.Enabled)

	// Can be disabled
	rule.Enabled = false
	assert.False(t, rule.Enabled)
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestInferenceRule_AllConstantsInHead(t *testing.T) {
	// A rule with all constants in head (unusual but valid)
	rule := &InferenceRule{
		ID:   "constant-rule",
		Name: "ConstantRule",
		Head: RuleCondition{Subject: "A", Predicate: "fixed", Object: "B"},
		Body: []RuleCondition{
			{Subject: "X", Predicate: "trigger", Object: "Y"},
		},
	}

	err := rule.Validate()
	assert.NoError(t, err) // Valid because head has no variables to check
}

func TestInferenceRule_MultipleBodyConditions(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"ComplexRule",
		RuleCondition{Subject: "?a", Predicate: "result", Object: "?e"},
		[]RuleCondition{
			{Subject: "?a", Predicate: "step1", Object: "?b"},
			{Subject: "?b", Predicate: "step2", Object: "?c"},
			{Subject: "?c", Predicate: "step3", Object: "?d"},
			{Subject: "?d", Predicate: "step4", Object: "?e"},
		},
	)

	err := rule.Validate()
	assert.NoError(t, err)

	vars := rule.GetVariables()
	assert.Len(t, vars, 5)
	require.Contains(t, vars, "?a")
	require.Contains(t, vars, "?b")
	require.Contains(t, vars, "?c")
	require.Contains(t, vars, "?d")
	require.Contains(t, vars, "?e")
}

func TestInferenceRule_MixedNegation(t *testing.T) {
	rule := NewInferenceRule(
		"rule-1",
		"MixedRule",
		RuleCondition{Subject: "?x", Predicate: "allowed", Object: "?y"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "hasAccess", Object: "?y"},
			{Subject: "?x", Predicate: "blocked", Object: "?y", Negated: true},
			{Subject: "?y", Predicate: "isPublic", Object: "true"},
			{Subject: "?x", Predicate: "suspended", Object: "true", Negated: true},
		},
	)

	assert.True(t, rule.Negation)
	assert.Contains(t, rule.BodyPredicates, "hasAccess")
	assert.Contains(t, rule.BodyPredicates, "blocked")
	assert.Contains(t, rule.BodyPredicates, "isPublic")
	assert.Contains(t, rule.BodyPredicates, "suspended")
}
