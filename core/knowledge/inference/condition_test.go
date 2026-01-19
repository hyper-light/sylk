package inference

import (
	"testing"
)

// =============================================================================
// IsVariable Tests
// =============================================================================

func TestIsVariable(t *testing.T) {
	tests := []struct {
		term     string
		expected bool
	}{
		{"?x", true},
		{"?y", true},
		{"?subject", true},
		{"?123", true},
		{"x", false},
		{"node1", false},
		{"calls", false},
		{"", false},
		{"??x", true}, // starts with ?
	}

	for _, tt := range tests {
		got := IsVariable(tt.term)
		if got != tt.expected {
			t.Errorf("IsVariable(%q) = %v, want %v", tt.term, got, tt.expected)
		}
	}
}

// =============================================================================
// RuleCondition.Unify Tests
// =============================================================================

func TestRuleCondition_Unify_AllConstants(t *testing.T) {
	condition := RuleCondition{
		Subject:   "node1",
		Predicate: "calls",
		Object:    "node2",
	}

	bindings := make(map[string]string)
	newBindings, success := condition.Unify(bindings, "node1", "node2", "calls")

	if !success {
		t.Fatal("Unify should succeed for matching constants")
	}
	if len(newBindings) != 0 {
		t.Errorf("Expected no bindings, got %v", newBindings)
	}
}

func TestRuleCondition_Unify_AllConstants_Mismatch(t *testing.T) {
	condition := RuleCondition{
		Subject:   "node1",
		Predicate: "calls",
		Object:    "node2",
	}

	bindings := make(map[string]string)
	_, success := condition.Unify(bindings, "node1", "node3", "calls")

	if success {
		t.Error("Unify should fail for mismatched object")
	}
}

func TestRuleCondition_Unify_SubjectVariable(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "node2",
	}

	bindings := make(map[string]string)
	newBindings, success := condition.Unify(bindings, "node1", "node2", "calls")

	if !success {
		t.Fatal("Unify should succeed")
	}
	if newBindings["?x"] != "node1" {
		t.Errorf("Expected ?x=node1, got %v", newBindings)
	}
}

func TestRuleCondition_Unify_AllVariables(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "?rel",
		Object:    "?y",
	}

	bindings := make(map[string]string)
	newBindings, success := condition.Unify(bindings, "funcA", "funcB", "calls")

	if !success {
		t.Fatal("Unify should succeed")
	}
	if newBindings["?x"] != "funcA" {
		t.Errorf("Expected ?x=funcA, got %v", newBindings["?x"])
	}
	if newBindings["?rel"] != "calls" {
		t.Errorf("Expected ?rel=calls, got %v", newBindings["?rel"])
	}
	if newBindings["?y"] != "funcB" {
		t.Errorf("Expected ?y=funcB, got %v", newBindings["?y"])
	}
}

func TestRuleCondition_Unify_ExistingBindings_Match(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	bindings := map[string]string{
		"?x": "node1",
	}
	newBindings, success := condition.Unify(bindings, "node1", "node2", "calls")

	if !success {
		t.Fatal("Unify should succeed when bindings match")
	}
	if newBindings["?x"] != "node1" {
		t.Errorf("Expected ?x=node1, got %v", newBindings["?x"])
	}
	if newBindings["?y"] != "node2" {
		t.Errorf("Expected ?y=node2, got %v", newBindings["?y"])
	}
}

func TestRuleCondition_Unify_ExistingBindings_Mismatch(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	bindings := map[string]string{
		"?x": "node1",
	}
	_, success := condition.Unify(bindings, "node3", "node2", "calls")

	if success {
		t.Error("Unify should fail when bindings conflict")
	}
}

func TestRuleCondition_Unify_OriginalBindingsUnchanged(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "?rel",
		Object:    "?y",
	}

	original := map[string]string{
		"?z": "existing",
	}
	newBindings, success := condition.Unify(original, "a", "b", "c")

	if !success {
		t.Fatal("Unify should succeed")
	}
	if len(original) != 1 {
		t.Errorf("Original bindings modified: %v", original)
	}
	if len(newBindings) != 4 {
		t.Errorf("Expected 4 bindings, got %d", len(newBindings))
	}
}

// =============================================================================
// RuleCondition.Substitute Tests
// =============================================================================

func TestRuleCondition_Substitute_NoVariables(t *testing.T) {
	condition := RuleCondition{
		Subject:   "node1",
		Predicate: "calls",
		Object:    "node2",
	}

	bindings := map[string]string{
		"?x": "ignored",
	}
	result := condition.Substitute(bindings)

	if result.Subject != "node1" {
		t.Errorf("Subject changed: %s", result.Subject)
	}
	if result.Predicate != "calls" {
		t.Errorf("Predicate changed: %s", result.Predicate)
	}
	if result.Object != "node2" {
		t.Errorf("Object changed: %s", result.Object)
	}
}

func TestRuleCondition_Substitute_AllVariables(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "?rel",
		Object:    "?y",
	}

	bindings := map[string]string{
		"?x":   "nodeA",
		"?rel": "imports",
		"?y":   "nodeB",
	}
	result := condition.Substitute(bindings)

	if result.Subject != "nodeA" {
		t.Errorf("Subject = %s, want nodeA", result.Subject)
	}
	if result.Predicate != "imports" {
		t.Errorf("Predicate = %s, want imports", result.Predicate)
	}
	if result.Object != "nodeB" {
		t.Errorf("Object = %s, want nodeB", result.Object)
	}
}

func TestRuleCondition_Substitute_UnboundVariables(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?x",
		Predicate: "calls",
		Object:    "?y",
	}

	bindings := map[string]string{
		"?x": "nodeA",
	}
	result := condition.Substitute(bindings)

	if result.Subject != "nodeA" {
		t.Errorf("Subject = %s, want nodeA", result.Subject)
	}
	if result.Predicate != "calls" {
		t.Errorf("Predicate = %s, want calls", result.Predicate)
	}
	if result.Object != "?y" {
		t.Errorf("Object = %s, want ?y (unbound)", result.Object)
	}
}

func TestRuleCondition_Substitute_MixedTerms(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?caller",
		Predicate: "calls",
		Object:    "specificFunc",
	}

	bindings := map[string]string{
		"?caller": "main",
	}
	result := condition.Substitute(bindings)

	if result.Subject != "main" {
		t.Errorf("Subject = %s, want main", result.Subject)
	}
	if result.Predicate != "calls" {
		t.Errorf("Predicate = %s, want calls", result.Predicate)
	}
	if result.Object != "specificFunc" {
		t.Errorf("Object = %s, want specificFunc", result.Object)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestCopyBindings(t *testing.T) {
	original := map[string]string{
		"?x": "a",
		"?y": "b",
	}

	copy := copyBindings(original)
	copy["?x"] = "modified"
	copy["?z"] = "new"

	if original["?x"] != "a" {
		t.Errorf("Original modified: %v", original)
	}
	if _, exists := original["?z"]; exists {
		t.Errorf("Original has new key: %v", original)
	}
}

func TestCopyBindings_Empty(t *testing.T) {
	original := make(map[string]string)
	copy := copyBindings(original)

	if copy == nil {
		t.Error("Copy should not be nil")
	}
	if len(copy) != 0 {
		t.Errorf("Copy should be empty, got %v", copy)
	}
}
