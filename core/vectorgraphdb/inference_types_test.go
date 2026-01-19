package vectorgraphdb

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// InferenceRuleRecord Tests
// =============================================================================

func TestNewInferenceRuleRecord(t *testing.T) {
	bodyAtoms := []RuleAtom{
		NewRuleAtom("?x", "calls", "?y"),
		NewRuleAtom("?y", "calls", "?z"),
	}

	rule, err := NewInferenceRuleRecord(
		"rule-1",
		"TransitiveCalls",
		"?x",
		"indirectly_calls",
		"?z",
		bodyAtoms,
		10,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rule.ID != "rule-1" {
		t.Errorf("expected ID 'rule-1', got %s", rule.ID)
	}
	if rule.Name != "TransitiveCalls" {
		t.Errorf("expected Name 'TransitiveCalls', got %s", rule.Name)
	}
	if rule.HeadSubject != "?x" {
		t.Errorf("expected HeadSubject '?x', got %s", rule.HeadSubject)
	}
	if rule.HeadPredicate != "indirectly_calls" {
		t.Errorf("expected HeadPredicate 'indirectly_calls', got %s", rule.HeadPredicate)
	}
	if rule.HeadObject != "?z" {
		t.Errorf("expected HeadObject '?z', got %s", rule.HeadObject)
	}
	if rule.Priority != 10 {
		t.Errorf("expected Priority 10, got %d", rule.Priority)
	}
	if !rule.Enabled {
		t.Error("expected rule to be enabled by default")
	}
	if rule.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}

	// Verify body can be parsed back
	parsedBody, err := rule.GetBody()
	if err != nil {
		t.Fatalf("failed to get body: %v", err)
	}
	if len(parsedBody) != 2 {
		t.Errorf("expected 2 body atoms, got %d", len(parsedBody))
	}
}

func TestInferenceRuleRecord_GetSetBody(t *testing.T) {
	rule := &InferenceRuleRecord{
		ID:            "rule-1",
		Name:          "TestRule",
		HeadSubject:   "?x",
		HeadPredicate: "related_to",
		HeadObject:    "?y",
		Priority:      1,
		Enabled:       true,
	}

	// Set body
	atoms := []RuleAtom{
		NewRuleAtom("?x", "knows", "?y"),
	}
	if err := rule.SetBody(atoms); err != nil {
		t.Fatalf("failed to set body: %v", err)
	}

	// Get body
	retrieved, err := rule.GetBody()
	if err != nil {
		t.Fatalf("failed to get body: %v", err)
	}
	if len(retrieved) != 1 {
		t.Errorf("expected 1 atom, got %d", len(retrieved))
	}
	if retrieved[0].Subject != "?x" || retrieved[0].Predicate != "knows" || retrieved[0].Object != "?y" {
		t.Errorf("unexpected atom: %v", retrieved[0])
	}
}

func TestInferenceRuleRecord_GetSetHead(t *testing.T) {
	rule := &InferenceRuleRecord{
		ID:            "rule-1",
		HeadSubject:   "?a",
		HeadPredicate: "pred",
		HeadObject:    "?b",
	}

	head := rule.GetHead()
	if head.Subject != "?a" || head.Predicate != "pred" || head.Object != "?b" {
		t.Errorf("unexpected head: %v", head)
	}

	newHead := NewRuleAtom("?x", "new_pred", "?y")
	rule.SetHead(newHead)

	if rule.HeadSubject != "?x" {
		t.Errorf("expected HeadSubject '?x', got %s", rule.HeadSubject)
	}
	if rule.HeadPredicate != "new_pred" {
		t.Errorf("expected HeadPredicate 'new_pred', got %s", rule.HeadPredicate)
	}
	if rule.HeadObject != "?y" {
		t.Errorf("expected HeadObject '?y', got %s", rule.HeadObject)
	}
}

func TestInferenceRuleRecord_IsValid(t *testing.T) {
	validBody := `[{"subject":"?x","predicate":"calls","object":"?y"}]`

	tests := []struct {
		name     string
		rule     *InferenceRuleRecord
		expected bool
	}{
		{
			name: "valid rule",
			rule: &InferenceRuleRecord{
				ID:            "rule-1",
				Name:          "TestRule",
				HeadSubject:   "?x",
				HeadPredicate: "pred",
				HeadObject:    "?y",
				BodyJSON:      validBody,
			},
			expected: true,
		},
		{
			name: "empty ID",
			rule: &InferenceRuleRecord{
				ID:            "",
				Name:          "TestRule",
				HeadSubject:   "?x",
				HeadPredicate: "pred",
				HeadObject:    "?y",
				BodyJSON:      validBody,
			},
			expected: false,
		},
		{
			name: "empty Name",
			rule: &InferenceRuleRecord{
				ID:            "rule-1",
				Name:          "",
				HeadSubject:   "?x",
				HeadPredicate: "pred",
				HeadObject:    "?y",
				BodyJSON:      validBody,
			},
			expected: false,
		},
		{
			name: "empty HeadSubject",
			rule: &InferenceRuleRecord{
				ID:            "rule-1",
				Name:          "TestRule",
				HeadSubject:   "",
				HeadPredicate: "pred",
				HeadObject:    "?y",
				BodyJSON:      validBody,
			},
			expected: false,
		},
		{
			name: "empty HeadPredicate",
			rule: &InferenceRuleRecord{
				ID:            "rule-1",
				Name:          "TestRule",
				HeadSubject:   "?x",
				HeadPredicate: "",
				HeadObject:    "?y",
				BodyJSON:      validBody,
			},
			expected: false,
		},
		{
			name: "empty HeadObject",
			rule: &InferenceRuleRecord{
				ID:            "rule-1",
				Name:          "TestRule",
				HeadSubject:   "?x",
				HeadPredicate: "pred",
				HeadObject:    "",
				BodyJSON:      validBody,
			},
			expected: false,
		},
		{
			name: "empty BodyJSON",
			rule: &InferenceRuleRecord{
				ID:            "rule-1",
				Name:          "TestRule",
				HeadSubject:   "?x",
				HeadPredicate: "pred",
				HeadObject:    "?y",
				BodyJSON:      "",
			},
			expected: false,
		},
		{
			name: "invalid BodyJSON",
			rule: &InferenceRuleRecord{
				ID:            "rule-1",
				Name:          "TestRule",
				HeadSubject:   "?x",
				HeadPredicate: "pred",
				HeadObject:    "?y",
				BodyJSON:      "invalid",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.rule.IsValid() != tt.expected {
				t.Errorf("IsValid() = %v, expected %v", tt.rule.IsValid(), tt.expected)
			}
		})
	}
}

func TestInferenceRuleRecord_JSON(t *testing.T) {
	rule := &InferenceRuleRecord{
		ID:            "rule-1",
		Name:          "TestRule",
		HeadSubject:   "?x",
		HeadPredicate: "derived_from",
		HeadObject:    "?y",
		BodyJSON:      `[{"subject":"?x","predicate":"based_on","object":"?y"}]`,
		Priority:      5,
		Enabled:       true,
		CreatedAt:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled InferenceRuleRecord
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled.ID != rule.ID {
		t.Errorf("expected ID %s, got %s", rule.ID, unmarshaled.ID)
	}
	if unmarshaled.Name != rule.Name {
		t.Errorf("expected Name %s, got %s", rule.Name, unmarshaled.Name)
	}
	if unmarshaled.HeadSubject != rule.HeadSubject {
		t.Errorf("expected HeadSubject %s, got %s", rule.HeadSubject, unmarshaled.HeadSubject)
	}
	if unmarshaled.HeadPredicate != rule.HeadPredicate {
		t.Errorf("expected HeadPredicate %s, got %s", rule.HeadPredicate, unmarshaled.HeadPredicate)
	}
	if unmarshaled.HeadObject != rule.HeadObject {
		t.Errorf("expected HeadObject %s, got %s", rule.HeadObject, unmarshaled.HeadObject)
	}
	if unmarshaled.Priority != rule.Priority {
		t.Errorf("expected Priority %d, got %d", rule.Priority, unmarshaled.Priority)
	}
	if unmarshaled.Enabled != rule.Enabled {
		t.Errorf("expected Enabled %v, got %v", rule.Enabled, unmarshaled.Enabled)
	}
}

// =============================================================================
// RuleAtom Tests
// =============================================================================

func TestNewRuleAtom(t *testing.T) {
	atom := NewRuleAtom("?x", "calls", "?y")

	if atom.Subject != "?x" {
		t.Errorf("expected Subject '?x', got %s", atom.Subject)
	}
	if atom.Predicate != "calls" {
		t.Errorf("expected Predicate 'calls', got %s", atom.Predicate)
	}
	if atom.Object != "?y" {
		t.Errorf("expected Object '?y', got %s", atom.Object)
	}
}

func TestRuleAtom_HasVariable(t *testing.T) {
	tests := []struct {
		atom     RuleAtom
		expected bool
	}{
		{NewRuleAtom("?x", "calls", "?y"), true},
		{NewRuleAtom("func1", "calls", "?y"), true},
		{NewRuleAtom("?x", "calls", "func2"), true},
		{NewRuleAtom("func1", "?p", "func2"), true},
		{NewRuleAtom("func1", "calls", "func2"), false},
	}

	for _, tt := range tests {
		t.Run(tt.atom.String(), func(t *testing.T) {
			if tt.atom.HasVariable() != tt.expected {
				t.Errorf("HasVariable() = %v, expected %v", tt.atom.HasVariable(), tt.expected)
			}
		})
	}
}

func TestRuleAtom_GetVariables(t *testing.T) {
	tests := []struct {
		atom     RuleAtom
		expected []string
	}{
		{NewRuleAtom("?x", "calls", "?y"), []string{"?x", "?y"}},
		{NewRuleAtom("func1", "calls", "?y"), []string{"?y"}},
		{NewRuleAtom("?x", "?p", "?y"), []string{"?x", "?p", "?y"}},
		{NewRuleAtom("func1", "calls", "func2"), nil},
	}

	for _, tt := range tests {
		t.Run(tt.atom.String(), func(t *testing.T) {
			vars := tt.atom.GetVariables()
			if len(vars) != len(tt.expected) {
				t.Errorf("expected %d variables, got %d", len(tt.expected), len(vars))
				return
			}
			for i, v := range tt.expected {
				if vars[i] != v {
					t.Errorf("expected variable %s at index %d, got %s", v, i, vars[i])
				}
			}
		})
	}
}

func TestRuleAtom_IsGround(t *testing.T) {
	groundAtom := NewRuleAtom("func1", "calls", "func2")
	if !groundAtom.IsGround() {
		t.Error("expected atom without variables to be ground")
	}

	nonGroundAtom := NewRuleAtom("?x", "calls", "func2")
	if nonGroundAtom.IsGround() {
		t.Error("expected atom with variable to not be ground")
	}
}

func TestRuleAtom_String(t *testing.T) {
	atom := NewRuleAtom("?x", "calls", "?y")
	expected := "(?x, calls, ?y)"
	if atom.String() != expected {
		t.Errorf("expected %s, got %s", expected, atom.String())
	}
}

func TestRuleAtom_JSON(t *testing.T) {
	atom := NewRuleAtom("?x", "calls", "?y")

	data, err := json.Marshal(atom)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled RuleAtom
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled != atom {
		t.Errorf("expected %v, got %v", atom, unmarshaled)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestIsVariable(t *testing.T) {
	tests := []struct {
		term     string
		expected bool
	}{
		{"?x", true},
		{"?variable", true},
		{"?", true},
		{"x", false},
		{"variable", false},
		{"", false},
		{"x?", false},
	}

	for _, tt := range tests {
		t.Run(tt.term, func(t *testing.T) {
			if IsVariable(tt.term) != tt.expected {
				t.Errorf("IsVariable(%s) = %v, expected %v", tt.term, IsVariable(tt.term), tt.expected)
			}
		})
	}
}

func TestGetRuleVariables(t *testing.T) {
	rule := &InferenceRuleRecord{
		HeadSubject:   "?x",
		HeadPredicate: "derived_from",
		HeadObject:    "?z",
		BodyJSON:      `[{"subject":"?x","predicate":"calls","object":"?y"},{"subject":"?y","predicate":"calls","object":"?z"}]`,
	}

	vars := GetRuleVariables(rule)

	// Should find ?x, ?y, ?z (deduplicated)
	if len(vars) != 3 {
		t.Errorf("expected 3 unique variables, got %d: %v", len(vars), vars)
	}

	varSet := make(map[string]bool)
	for _, v := range vars {
		varSet[v] = true
	}

	for _, expected := range []string{"?x", "?y", "?z"} {
		if !varSet[expected] {
			t.Errorf("expected variable %s to be found", expected)
		}
	}

	// Test with nil rule
	nilVars := GetRuleVariables(nil)
	if nilVars != nil {
		t.Error("expected nil for nil rule")
	}
}

func TestParseBodyJSON(t *testing.T) {
	validJSON := `[{"subject":"?x","predicate":"calls","object":"?y"},{"subject":"?y","predicate":"implements","object":"?z"}]`

	atoms, err := ParseBodyJSON(validJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(atoms) != 2 {
		t.Errorf("expected 2 atoms, got %d", len(atoms))
	}

	// Test first atom
	if atoms[0].Subject != "?x" || atoms[0].Predicate != "calls" || atoms[0].Object != "?y" {
		t.Errorf("unexpected first atom: %v", atoms[0])
	}

	// Test empty string
	_, err = ParseBodyJSON("")
	if err == nil {
		t.Error("expected error for empty JSON")
	}

	// Test invalid JSON
	_, err = ParseBodyJSON("invalid")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestEncodeBodyJSON(t *testing.T) {
	atoms := []RuleAtom{
		NewRuleAtom("?x", "calls", "?y"),
		NewRuleAtom("?y", "implements", "?z"),
	}

	encoded, err := EncodeBodyJSON(atoms)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse back to verify
	decoded, err := ParseBodyJSON(encoded)
	if err != nil {
		t.Fatalf("failed to parse encoded JSON: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("expected 2 atoms, got %d", len(decoded))
	}

	// Test with nil atoms
	nilEncoded, err := EncodeBodyJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error for nil: %v", err)
	}
	if nilEncoded != "[]" {
		t.Errorf("expected '[]' for nil atoms, got %s", nilEncoded)
	}
}

func TestVariableNameWithoutPrefix(t *testing.T) {
	tests := []struct {
		term     string
		expected string
	}{
		{"?x", "x"},
		{"?variable", "variable"},
		{"?", ""},
		{"x", "x"},
		{"variable", "variable"},
	}

	for _, tt := range tests {
		t.Run(tt.term, func(t *testing.T) {
			result := VariableNameWithoutPrefix(tt.term)
			if result != tt.expected {
				t.Errorf("VariableNameWithoutPrefix(%s) = %s, expected %s", tt.term, result, tt.expected)
			}
		})
	}
}

func TestMakeVariable(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"x", "?x"},
		{"variable", "?variable"},
		{"?already", "?already"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeVariable(tt.name)
			if result != tt.expected {
				t.Errorf("MakeVariable(%s) = %s, expected %s", tt.name, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// MaterializedEdge Tests
// =============================================================================

func TestNewMaterializedEdge(t *testing.T) {
	edge := NewMaterializedEdge(42, 123)

	if edge.RuleID != 42 {
		t.Errorf("expected RuleID 42, got %d", edge.RuleID)
	}
	if edge.EdgeID != 123 {
		t.Errorf("expected EdgeID 123, got %d", edge.EdgeID)
	}
	if edge.DerivedAt.IsZero() {
		t.Error("expected DerivedAt to be set")
	}
}

func TestMaterializedEdge_JSON(t *testing.T) {
	edge := &MaterializedEdge{
		RuleID:    42,
		EdgeID:    123,
		DerivedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled MaterializedEdge
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled.RuleID != edge.RuleID {
		t.Errorf("expected RuleID %d, got %d", edge.RuleID, unmarshaled.RuleID)
	}
	if unmarshaled.EdgeID != edge.EdgeID {
		t.Errorf("expected EdgeID %d, got %d", edge.EdgeID, unmarshaled.EdgeID)
	}
}

// =============================================================================
// BindingMap Tests
// =============================================================================

func TestNewBindingMap(t *testing.T) {
	bm := NewBindingMap()
	if bm == nil {
		t.Error("expected non-nil binding map")
	}
	if len(bm) != 0 {
		t.Error("expected empty binding map")
	}
}

func TestBindingMap_Bind(t *testing.T) {
	bm := NewBindingMap()

	// First binding should succeed
	if !bm.Bind("?x", "value1") {
		t.Error("expected first binding to succeed")
	}

	// Binding same value should succeed
	if !bm.Bind("?x", "value1") {
		t.Error("expected binding same value to succeed")
	}

	// Binding different value should fail
	if bm.Bind("?x", "value2") {
		t.Error("expected binding different value to fail")
	}
}

func TestBindingMap_Get(t *testing.T) {
	bm := NewBindingMap()
	bm.Bind("?x", "value1")

	// Get bound variable
	if bm.Get("?x") != "value1" {
		t.Errorf("expected 'value1', got %s", bm.Get("?x"))
	}

	// Get unbound variable returns the variable itself
	if bm.Get("?y") != "?y" {
		t.Errorf("expected '?y', got %s", bm.Get("?y"))
	}

	// Get non-variable returns itself
	if bm.Get("concrete") != "concrete" {
		t.Errorf("expected 'concrete', got %s", bm.Get("concrete"))
	}
}

func TestBindingMap_Clone(t *testing.T) {
	bm := NewBindingMap()
	bm.Bind("?x", "value1")
	bm.Bind("?y", "value2")

	clone := bm.Clone()

	// Clone should have same values
	if clone.Get("?x") != "value1" {
		t.Error("clone missing ?x binding")
	}
	if clone.Get("?y") != "value2" {
		t.Error("clone missing ?y binding")
	}

	// Modifying clone should not affect original
	clone.Bind("?z", "value3")
	if bm.Get("?z") != "?z" {
		t.Error("original should not be affected by clone modification")
	}
}

func TestBindingMap_Apply(t *testing.T) {
	bm := NewBindingMap()
	bm.Bind("?x", "func1")
	bm.Bind("?y", "func2")

	atom := NewRuleAtom("?x", "calls", "?y")
	applied := bm.Apply(atom)

	if applied.Subject != "func1" {
		t.Errorf("expected Subject 'func1', got %s", applied.Subject)
	}
	if applied.Predicate != "calls" {
		t.Errorf("expected Predicate 'calls', got %s", applied.Predicate)
	}
	if applied.Object != "func2" {
		t.Errorf("expected Object 'func2', got %s", applied.Object)
	}

	// Unbound variable should remain as-is
	atom2 := NewRuleAtom("?x", "calls", "?z")
	applied2 := bm.Apply(atom2)
	if applied2.Object != "?z" {
		t.Errorf("expected unbound variable '?z', got %s", applied2.Object)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestInferenceRule_FullWorkflow(t *testing.T) {
	// Create a transitive closure rule:
	// IF ?x calls ?y AND ?y calls ?z THEN ?x indirectly_calls ?z
	bodyAtoms := []RuleAtom{
		NewRuleAtom("?x", "calls", "?y"),
		NewRuleAtom("?y", "calls", "?z"),
	}

	rule, err := NewInferenceRuleRecord(
		"transitive-calls",
		"TransitiveCalls",
		"?x",
		"indirectly_calls",
		"?z",
		bodyAtoms,
		10,
	)
	if err != nil {
		t.Fatalf("failed to create rule: %v", err)
	}

	// Verify rule is valid
	if !rule.IsValid() {
		t.Error("expected rule to be valid")
	}

	// Get all variables
	vars := GetRuleVariables(rule)
	varSet := make(map[string]bool)
	for _, v := range vars {
		varSet[v] = true
	}

	expectedVars := []string{"?x", "?y", "?z"}
	for _, v := range expectedVars {
		if !varSet[v] {
			t.Errorf("expected variable %s", v)
		}
	}

	// Simulate rule application with bindings
	bindings := NewBindingMap()
	bindings.Bind("?x", "funcA")
	bindings.Bind("?y", "funcB")
	bindings.Bind("?z", "funcC")

	// Apply bindings to head
	head := rule.GetHead()
	appliedHead := bindings.Apply(head)

	if appliedHead.Subject != "funcA" {
		t.Errorf("expected applied Subject 'funcA', got %s", appliedHead.Subject)
	}
	if appliedHead.Predicate != "indirectly_calls" {
		t.Errorf("expected applied Predicate 'indirectly_calls', got %s", appliedHead.Predicate)
	}
	if appliedHead.Object != "funcC" {
		t.Errorf("expected applied Object 'funcC', got %s", appliedHead.Object)
	}

	// Create materialized edge record
	materializedEdge := NewMaterializedEdge(1, 100)
	if materializedEdge.RuleID != 1 {
		t.Error("expected materialized edge RuleID to be 1")
	}
}
