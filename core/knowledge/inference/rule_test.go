package inference

import (
	"strings"
	"testing"
)

// =============================================================================
// InferenceRule.Validate Tests
// =============================================================================

func TestInferenceRule_Validate_Valid(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
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
	}

	if err := rule.Validate(); err != nil {
		t.Errorf("Valid rule should pass validation: %v", err)
	}
}

func TestInferenceRule_Validate_EmptyID(t *testing.T) {
	rule := InferenceRule{
		ID:   "",
		Name: "Test",
		Head: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
	}

	err := rule.Validate()
	if err == nil {
		t.Error("Should fail with empty ID")
	}
	if !strings.Contains(err.Error(), "ID") {
		t.Errorf("Error should mention ID: %v", err)
	}
}

func TestInferenceRule_Validate_EmptyName(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "",
		Head: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
	}

	err := rule.Validate()
	if err == nil {
		t.Error("Should fail with empty name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("Error should mention name: %v", err)
	}
}

func TestInferenceRule_Validate_EmptyBody(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Test",
		Head: RuleCondition{Subject: "?x", Predicate: "calls", Object: "?y"},
		Body: []RuleCondition{},
	}

	err := rule.Validate()
	if err == nil {
		t.Error("Should fail with empty body")
	}
	if !strings.Contains(err.Error(), "body") {
		t.Errorf("Error should mention body: %v", err)
	}
}

func TestInferenceRule_Validate_HeadVariableNotInBody(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Bad Rule",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
	}

	err := rule.Validate()
	if err == nil {
		t.Error("Should fail when head variable not in body")
	}
	if !strings.Contains(err.Error(), "?z") {
		t.Errorf("Error should mention missing variable ?z: %v", err)
	}
}

func TestInferenceRule_Validate_AllHeadVariablesInBody(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Valid Rule",
		Head: RuleCondition{
			Subject:   "?a",
			Predicate: "derived",
			Object:    "?b",
		},
		Body: []RuleCondition{
			{Subject: "?a", Predicate: "type1", Object: "?x"},
			{Subject: "?x", Predicate: "type2", Object: "?b"},
		},
		Priority: 1,
		Enabled:  true,
	}

	if err := rule.Validate(); err != nil {
		t.Errorf("Should pass when all head vars in body: %v", err)
	}
}

func TestInferenceRule_Validate_HeadWithConstants(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Constant Head",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "inferred_calls",
			Object:    "targetNode",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "targetNode"},
		},
		Priority: 1,
		Enabled:  true,
	}

	if err := rule.Validate(); err != nil {
		t.Errorf("Should allow constants in head: %v", err)
	}
}

// =============================================================================
// InferenceRule.GetVariables Tests
// =============================================================================

func TestInferenceRule_GetVariables_AllVariables(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Test",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
			{Subject: "?y", Predicate: "exports", Object: "?z"},
		},
	}

	vars := rule.GetVariables()
	expectedVars := map[string]bool{
		"?x": true,
		"?y": true,
		"?z": true,
	}

	if len(vars) != len(expectedVars) {
		t.Errorf("Expected %d variables, got %d: %v", len(expectedVars), len(vars), vars)
	}

	for _, v := range vars {
		if !expectedVars[v] {
			t.Errorf("Unexpected variable: %s", v)
		}
	}
}

func TestInferenceRule_GetVariables_WithConstants(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Test",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "derived",
			Object:    "constNode",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "constNode"},
		},
	}

	vars := rule.GetVariables()

	if len(vars) != 1 {
		t.Errorf("Expected 1 variable, got %d: %v", len(vars), vars)
	}
	if vars[0] != "?x" {
		t.Errorf("Expected ?x, got %s", vars[0])
	}
}

func TestInferenceRule_GetVariables_PredicateVariable(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Test",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "?rel",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "?rel", Object: "?y"},
		},
	}

	vars := rule.GetVariables()
	expectedVars := map[string]bool{
		"?x":   true,
		"?y":   true,
		"?rel": true,
	}

	if len(vars) != len(expectedVars) {
		t.Errorf("Expected %d variables, got %d", len(expectedVars), len(vars))
	}

	for _, v := range vars {
		if !expectedVars[v] {
			t.Errorf("Unexpected variable: %s", v)
		}
	}
}

func TestInferenceRule_GetVariables_NoVariables(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Test",
		Head: RuleCondition{
			Subject:   "nodeA",
			Predicate: "calls",
			Object:    "nodeB",
		},
		Body: []RuleCondition{
			{Subject: "nodeA", Predicate: "imports", Object: "nodeC"},
		},
	}

	vars := rule.GetVariables()

	if len(vars) != 0 {
		t.Errorf("Expected no variables, got %v", vars)
	}
}

// =============================================================================
// InferenceRule.String Tests
// =============================================================================

func TestInferenceRule_String_Simple(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
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
	}

	result := rule.String()
	expected := "Transitivity: ?x-calls->?z :- ?x-calls->?y, ?y-calls->?z"

	if result != expected {
		t.Errorf("String() = %q, want %q", result, expected)
	}
}

func TestInferenceRule_String_SingleBody(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Direct",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "related",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
	}

	result := rule.String()
	expected := "Direct: ?x-related->?y :- ?x-imports->?y"

	if result != expected {
		t.Errorf("String() = %q, want %q", result, expected)
	}
}

func TestInferenceRule_String_WithConstants(t *testing.T) {
	rule := InferenceRule{
		ID:   "rule1",
		Name: "Specific",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "uses",
			Object:    "stdlib",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "stdlib"},
		},
	}

	result := rule.String()
	expected := "Specific: ?x-uses->stdlib :- ?x-imports->stdlib"

	if result != expected {
		t.Errorf("String() = %q, want %q", result, expected)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestExtractVariables(t *testing.T) {
	tests := []struct {
		name      string
		condition RuleCondition
		expected  int
	}{
		{
			name: "All variables",
			condition: RuleCondition{
				Subject:   "?x",
				Predicate: "?rel",
				Object:    "?y",
			},
			expected: 3,
		},
		{
			name: "Two variables",
			condition: RuleCondition{
				Subject:   "?x",
				Predicate: "calls",
				Object:    "?y",
			},
			expected: 2,
		},
		{
			name: "One variable",
			condition: RuleCondition{
				Subject:   "?x",
				Predicate: "calls",
				Object:    "nodeB",
			},
			expected: 1,
		},
		{
			name: "No variables",
			condition: RuleCondition{
				Subject:   "nodeA",
				Predicate: "calls",
				Object:    "nodeB",
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := extractVariables(tt.condition)
			if len(vars) != tt.expected {
				t.Errorf("Expected %d variables, got %d: %v", tt.expected, len(vars), vars)
			}
		})
	}
}

func TestFormatCondition(t *testing.T) {
	condition := RuleCondition{
		Subject:   "?caller",
		Predicate: "calls",
		Object:    "?callee",
	}

	result := formatCondition(condition)
	expected := "?caller-calls->?callee"

	if result != expected {
		t.Errorf("formatCondition() = %q, want %q", result, expected)
	}
}
