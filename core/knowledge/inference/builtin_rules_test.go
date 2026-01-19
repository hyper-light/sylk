package inference

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// Rule Validation Tests
// =============================================================================

func TestTransitiveImportRule_Validate(t *testing.T) {
	rule := TransitiveImportRule()

	if err := rule.Validate(); err != nil {
		t.Errorf("TransitiveImportRule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("TransitiveImportRule should pass builtin validation: %v", err)
	}
}

func TestInheritedMethodRule_Validate(t *testing.T) {
	rule := InheritedMethodRule()

	if err := rule.Validate(); err != nil {
		t.Errorf("InheritedMethodRule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("InheritedMethodRule should pass builtin validation: %v", err)
	}
}

func TestInterfaceSatisfactionRule_Validate(t *testing.T) {
	rule := InterfaceSatisfactionRule()

	if err := rule.Validate(); err != nil {
		t.Errorf("InterfaceSatisfactionRule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("InterfaceSatisfactionRule should pass builtin validation: %v", err)
	}
}

func TestCallChainRule_Validate(t *testing.T) {
	rule := CallChainRule()

	if err := rule.Validate(); err != nil {
		t.Errorf("CallChainRule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("CallChainRule should pass builtin validation: %v", err)
	}
}

func TestCallChainDepth2Rule_Validate(t *testing.T) {
	rule := CallChainDepth2Rule()

	if err := rule.Validate(); err != nil {
		t.Errorf("CallChainDepth2Rule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("CallChainDepth2Rule should pass builtin validation: %v", err)
	}
}

func TestCallChainDepth3Rule_Validate(t *testing.T) {
	rule := CallChainDepth3Rule()

	if err := rule.Validate(); err != nil {
		t.Errorf("CallChainDepth3Rule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("CallChainDepth3Rule should pass builtin validation: %v", err)
	}
}

func TestReferenceClosureRule_Validate(t *testing.T) {
	rule := ReferenceClosureRule()

	if err := rule.Validate(); err != nil {
		t.Errorf("ReferenceClosureRule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("ReferenceClosureRule should pass builtin validation: %v", err)
	}
}

func TestReferenceClosureDepth2Rule_Validate(t *testing.T) {
	rule := ReferenceClosureDepth2Rule()

	if err := rule.Validate(); err != nil {
		t.Errorf("ReferenceClosureDepth2Rule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("ReferenceClosureDepth2Rule should pass builtin validation: %v", err)
	}
}

func TestReferenceClosureDepth3Rule_Validate(t *testing.T) {
	rule := ReferenceClosureDepth3Rule()

	if err := rule.Validate(); err != nil {
		t.Errorf("ReferenceClosureDepth3Rule should be valid: %v", err)
	}

	if err := ValidateBuiltinRule(rule); err != nil {
		t.Errorf("ReferenceClosureDepth3Rule should pass builtin validation: %v", err)
	}
}

func TestBuiltinRules_AllValid(t *testing.T) {
	rules := BuiltinRules()

	if len(rules) == 0 {
		t.Error("BuiltinRules should return at least one rule")
	}

	for _, rule := range rules {
		t.Run(rule.Name, func(t *testing.T) {
			if err := rule.Validate(); err != nil {
				t.Errorf("Rule %s should be valid: %v", rule.ID, err)
			}

			if err := ValidateBuiltinRule(rule); err != nil {
				t.Errorf("Rule %s should pass builtin validation: %v", rule.ID, err)
			}
		})
	}
}

// =============================================================================
// Rule Property Tests
// =============================================================================

func TestTransitiveImportRule_Properties(t *testing.T) {
	rule := TransitiveImportRule()

	if rule.ID != BuiltinTransitiveImportID {
		t.Errorf("Expected ID %s, got %s", BuiltinTransitiveImportID, rule.ID)
	}

	if rule.Head.Predicate != "indirectly_imports" {
		t.Errorf("Expected head predicate 'indirectly_imports', got %s", rule.Head.Predicate)
	}

	if len(rule.Body) != 2 {
		t.Errorf("Expected 2 body conditions, got %d", len(rule.Body))
	}

	if rule.Body[0].Predicate != "imports" || rule.Body[1].Predicate != "imports" {
		t.Error("Body conditions should use 'imports' predicate")
	}

	if !rule.Enabled {
		t.Error("Built-in rules should be enabled by default")
	}

	if rule.Priority < 100 {
		t.Errorf("Built-in rule priority should be >= 100, got %d", rule.Priority)
	}
}

func TestInheritedMethodRule_Properties(t *testing.T) {
	rule := InheritedMethodRule()

	if rule.ID != BuiltinInheritedMethodID {
		t.Errorf("Expected ID %s, got %s", BuiltinInheritedMethodID, rule.ID)
	}

	if rule.Head.Predicate != "has_method" {
		t.Errorf("Expected head predicate 'has_method', got %s", rule.Head.Predicate)
	}

	if len(rule.Body) != 2 {
		t.Errorf("Expected 2 body conditions, got %d", len(rule.Body))
	}

	// Check body predicates
	predicates := make(map[string]bool)
	for _, cond := range rule.Body {
		predicates[cond.Predicate] = true
	}

	if !predicates["extends"] {
		t.Error("Body should contain 'extends' predicate")
	}
	if !predicates["has_method"] {
		t.Error("Body should contain 'has_method' predicate")
	}
}

func TestCallChainRule_Properties(t *testing.T) {
	rule := CallChainRule()

	if rule.ID != BuiltinCallChainID {
		t.Errorf("Expected ID %s, got %s", BuiltinCallChainID, rule.ID)
	}

	if rule.Head.Predicate != "transitively_calls" {
		t.Errorf("Expected head predicate 'transitively_calls', got %s", rule.Head.Predicate)
	}

	if len(rule.Body) != 2 {
		t.Errorf("Expected 2 body conditions, got %d", len(rule.Body))
	}

	for _, cond := range rule.Body {
		if cond.Predicate != "calls" {
			t.Errorf("Body conditions should use 'calls' predicate, got %s", cond.Predicate)
		}
	}
}

func TestReferenceClosureRule_Properties(t *testing.T) {
	rule := ReferenceClosureRule()

	if rule.ID != BuiltinReferenceClosureID {
		t.Errorf("Expected ID %s, got %s", BuiltinReferenceClosureID, rule.ID)
	}

	if rule.Head.Predicate != "transitively_refs" {
		t.Errorf("Expected head predicate 'transitively_refs', got %s", rule.Head.Predicate)
	}

	if len(rule.Body) != 2 {
		t.Errorf("Expected 2 body conditions, got %d", len(rule.Body))
	}

	for _, cond := range rule.Body {
		if cond.Predicate != "references" {
			t.Errorf("Body conditions should use 'references' predicate, got %s", cond.Predicate)
		}
	}
}

// =============================================================================
// GetBuiltinRuleByID Tests
// =============================================================================

func TestGetBuiltinRuleByID_Found(t *testing.T) {
	tests := []struct {
		id           string
		expectedName string
	}{
		{BuiltinTransitiveImportID, "Transitive Import"},
		{BuiltinInheritedMethodID, "Inherited Method"},
		{BuiltinInterfaceSatisfactionID, "Interface Satisfaction"},
		{BuiltinCallChainID, "Call Chain"},
		{BuiltinCallChainDepth2ID, "Call Chain Depth 2"},
		{BuiltinCallChainDepth3ID, "Call Chain Depth 3"},
		{BuiltinReferenceClosureID, "Reference Closure"},
		{BuiltinReferenceClosureDepth2ID, "Reference Closure Depth 2"},
		{BuiltinReferenceClosureDepth3ID, "Reference Closure Depth 3"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			rule, found := GetBuiltinRuleByID(tt.id)
			if !found {
				t.Errorf("Expected to find rule %s", tt.id)
			}
			if rule == nil {
				t.Errorf("Expected non-nil rule for %s", tt.id)
				return
			}
			if rule.Name != tt.expectedName {
				t.Errorf("Expected name %s, got %s", tt.expectedName, rule.Name)
			}
		})
	}
}

func TestGetBuiltinRuleByID_NotFound(t *testing.T) {
	rule, found := GetBuiltinRuleByID("nonexistent_rule")
	if found {
		t.Error("Should not find nonexistent rule")
	}
	if rule != nil {
		t.Error("Rule should be nil for nonexistent ID")
	}
}

// =============================================================================
// ValidateBuiltinRule Tests
// =============================================================================

func TestValidateBuiltinRule_InvalidID(t *testing.T) {
	rule := InferenceRule{
		ID:   "custom_rule", // Missing 'builtin_' prefix
		Name: "Custom Rule",
		Head: RuleCondition{Subject: "?A", Predicate: "test", Object: "?B"},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "has", Object: "?B"},
		},
		Priority: 100,
		Enabled:  true,
	}

	err := ValidateBuiltinRule(rule)
	if err == nil {
		t.Error("Should fail validation for non-builtin ID prefix")
	}
}

func TestValidateBuiltinRule_LowPriority(t *testing.T) {
	rule := InferenceRule{
		ID:   "builtin_test",
		Name: "Test Rule",
		Head: RuleCondition{Subject: "?A", Predicate: "test", Object: "?B"},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "has", Object: "?B"},
		},
		Priority: 50, // Below 100
		Enabled:  true,
	}

	err := ValidateBuiltinRule(rule)
	if err == nil {
		t.Error("Should fail validation for priority < 100")
	}
}

func TestValidateBuiltinRule_StandardValidationFails(t *testing.T) {
	rule := InferenceRule{
		ID:       "builtin_test",
		Name:     "", // Empty name should fail standard validation
		Head:     RuleCondition{Subject: "?A", Predicate: "test", Object: "?B"},
		Body:     []RuleCondition{},
		Priority: 100,
		Enabled:  true,
	}

	err := ValidateBuiltinRule(rule)
	if err == nil {
		t.Error("Should fail standard validation for empty name")
	}
}

// =============================================================================
// IsBuiltinRule Tests
// =============================================================================

func TestIsBuiltinRule_True(t *testing.T) {
	builtinIDs := []string{
		BuiltinTransitiveImportID,
		BuiltinInheritedMethodID,
		BuiltinInterfaceSatisfactionID,
		BuiltinCallChainID,
		BuiltinReferenceClosureID,
	}

	for _, id := range builtinIDs {
		if !IsBuiltinRule(id) {
			t.Errorf("IsBuiltinRule(%s) should return true", id)
		}
	}
}

func TestIsBuiltinRule_False(t *testing.T) {
	nonBuiltinIDs := []string{
		"custom_rule",
		"user_defined",
		"builtin_fake", // Not in the index
	}

	for _, id := range nonBuiltinIDs {
		if IsBuiltinRule(id) {
			t.Errorf("IsBuiltinRule(%s) should return false", id)
		}
	}
}

// =============================================================================
// GetBuiltinRuleIDs Tests
// =============================================================================

func TestGetBuiltinRuleIDs(t *testing.T) {
	ids := GetBuiltinRuleIDs()

	if len(ids) == 0 {
		t.Error("GetBuiltinRuleIDs should return at least one ID")
	}

	// Check that all returned IDs are valid
	for _, id := range ids {
		if !IsBuiltinRule(id) {
			t.Errorf("ID %s should be a valid builtin rule", id)
		}
	}

	// Check that expected IDs are present
	expectedIDs := map[string]bool{
		BuiltinTransitiveImportID:      false,
		BuiltinInheritedMethodID:       false,
		BuiltinInterfaceSatisfactionID: false,
		BuiltinCallChainID:             false,
		BuiltinReferenceClosureID:      false,
	}

	for _, id := range ids {
		if _, ok := expectedIDs[id]; ok {
			expectedIDs[id] = true
		}
	}

	for id, found := range expectedIDs {
		if !found {
			t.Errorf("Expected ID %s not found in GetBuiltinRuleIDs", id)
		}
	}
}

// =============================================================================
// LoadBuiltinRules Integration Tests
// =============================================================================

func TestLoadBuiltinRules_Success(t *testing.T) {
	db := setupBuiltinRulesTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	err := LoadBuiltinRules(ctx, engine)
	if err != nil {
		t.Fatalf("LoadBuiltinRules failed: %v", err)
	}

	// Verify rules were loaded
	rules, err := engine.GetRules(ctx)
	if err != nil {
		t.Fatalf("GetRules failed: %v", err)
	}

	expectedCount := len(BuiltinRules())
	if len(rules) != expectedCount {
		t.Errorf("Expected %d rules, got %d", expectedCount, len(rules))
	}

	// Verify each rule exists
	for _, builtinRule := range BuiltinRules() {
		found := false
		for _, rule := range rules {
			if rule.ID == builtinRule.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Rule %s not found after loading", builtinRule.ID)
		}
	}
}

func TestLoadBuiltinRules_SkipsExisting(t *testing.T) {
	db := setupBuiltinRulesTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	// Pre-add a rule with modified state
	modifiedRule := TransitiveImportRule()
	modifiedRule.Enabled = false // Modify the rule
	err := engine.AddRule(ctx, modifiedRule)
	if err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Load built-in rules
	err = LoadBuiltinRules(ctx, engine)
	if err != nil {
		t.Fatalf("LoadBuiltinRules failed: %v", err)
	}

	// Verify the modified rule was not overwritten
	rule, err := engine.RuleStore().GetRule(ctx, BuiltinTransitiveImportID)
	if err != nil {
		t.Fatalf("GetRule failed: %v", err)
	}

	if rule.Enabled {
		t.Error("Existing rule should not have been overwritten")
	}
}

func TestLoadBuiltinRules_NilEngine(t *testing.T) {
	ctx := context.Background()

	err := LoadBuiltinRules(ctx, nil)
	if err == nil {
		t.Error("Should fail with nil engine")
	}
}

// =============================================================================
// ResetBuiltinRule Tests
// =============================================================================

func TestResetBuiltinRule_Success(t *testing.T) {
	db := setupBuiltinRulesTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	// Load built-in rules first
	err := LoadBuiltinRules(ctx, engine)
	if err != nil {
		t.Fatalf("LoadBuiltinRules failed: %v", err)
	}

	// Modify a rule
	rule, err := engine.RuleStore().GetRule(ctx, BuiltinTransitiveImportID)
	if err != nil {
		t.Fatalf("GetRule failed: %v", err)
	}
	rule.Enabled = false
	err = engine.AddRule(ctx, *rule)
	if err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Verify modification
	rule, _ = engine.RuleStore().GetRule(ctx, BuiltinTransitiveImportID)
	if rule.Enabled {
		t.Error("Rule should be disabled after modification")
	}

	// Reset the rule
	err = ResetBuiltinRule(ctx, engine, BuiltinTransitiveImportID)
	if err != nil {
		t.Fatalf("ResetBuiltinRule failed: %v", err)
	}

	// Verify reset
	rule, _ = engine.RuleStore().GetRule(ctx, BuiltinTransitiveImportID)
	if !rule.Enabled {
		t.Error("Rule should be enabled after reset")
	}
}

func TestResetBuiltinRule_NonBuiltin(t *testing.T) {
	db := setupBuiltinRulesTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	err := ResetBuiltinRule(ctx, engine, "custom_rule")
	if err == nil {
		t.Error("Should fail for non-builtin rule")
	}
}

// =============================================================================
// Rule Inference Tests (Expected Edges)
// =============================================================================

func TestTransitiveImportRule_ExpectedEdges(t *testing.T) {
	rule := TransitiveImportRule()
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Create test edges: A imports B, B imports C
	edges := []Edge{
		{Source: "A", Target: "B", Predicate: "imports"},
		{Source: "B", Target: "C", Predicate: "imports"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	// Should derive: A indirectly_imports C
	if len(results) != 1 {
		t.Errorf("Expected 1 derived edge, got %d", len(results))
		return
	}

	derived := results[0].DerivedEdge
	if derived.SourceID != "A" || derived.TargetID != "C" || derived.EdgeType != "indirectly_imports" {
		t.Errorf("Expected A-indirectly_imports->C, got %s-%s->%s",
			derived.SourceID, derived.EdgeType, derived.TargetID)
	}
}

func TestInheritedMethodRule_ExpectedEdges(t *testing.T) {
	rule := InheritedMethodRule()
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Create test edges: Dog extends Animal, Animal has_method Eat
	edges := []Edge{
		{Source: "Dog", Target: "Animal", Predicate: "extends"},
		{Source: "Animal", Target: "Eat", Predicate: "has_method"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	// Should derive: Dog has_method Eat
	if len(results) != 1 {
		t.Errorf("Expected 1 derived edge, got %d", len(results))
		return
	}

	derived := results[0].DerivedEdge
	if derived.SourceID != "Dog" || derived.TargetID != "Eat" || derived.EdgeType != "has_method" {
		t.Errorf("Expected Dog-has_method->Eat, got %s-%s->%s",
			derived.SourceID, derived.EdgeType, derived.TargetID)
	}
}

func TestCallChainRule_ExpectedEdges(t *testing.T) {
	rule := CallChainRule()
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Create test edges: main calls helper, helper calls utility
	edges := []Edge{
		{Source: "main", Target: "helper", Predicate: "calls"},
		{Source: "helper", Target: "utility", Predicate: "calls"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	// Should derive: main transitively_calls utility
	if len(results) != 1 {
		t.Errorf("Expected 1 derived edge, got %d", len(results))
		return
	}

	derived := results[0].DerivedEdge
	if derived.SourceID != "main" || derived.TargetID != "utility" || derived.EdgeType != "transitively_calls" {
		t.Errorf("Expected main-transitively_calls->utility, got %s-%s->%s",
			derived.SourceID, derived.EdgeType, derived.TargetID)
	}
}

func TestReferenceClosureRule_ExpectedEdges(t *testing.T) {
	rule := ReferenceClosureRule()
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	// Create test edges: TypeA references TypeB, TypeB references TypeC
	edges := []Edge{
		{Source: "TypeA", Target: "TypeB", Predicate: "references"},
		{Source: "TypeB", Target: "TypeC", Predicate: "references"},
	}

	results := evaluator.EvaluateRule(ctx, rule, edges)

	// Should derive: TypeA transitively_refs TypeC
	if len(results) != 1 {
		t.Errorf("Expected 1 derived edge, got %d", len(results))
		return
	}

	derived := results[0].DerivedEdge
	if derived.SourceID != "TypeA" || derived.TargetID != "TypeC" || derived.EdgeType != "transitively_refs" {
		t.Errorf("Expected TypeA-transitively_refs->TypeC, got %s-%s->%s",
			derived.SourceID, derived.EdgeType, derived.TargetID)
	}
}

func TestCallChainRule_MaxDepth3(t *testing.T) {
	rules := []InferenceRule{
		CallChainRule(),
		CallChainDepth2Rule(),
		CallChainDepth3Rule(),
	}
	forwardChainer := NewForwardChainer(NewRuleEvaluator(), 10)

	// Create test edges: A calls B, B calls C, C calls D, D calls E
	edges := []Edge{
		{Source: "A", Target: "B", Predicate: "calls"},
		{Source: "B", Target: "C", Predicate: "calls"},
		{Source: "C", Target: "D", Predicate: "calls"},
		{Source: "D", Target: "E", Predicate: "calls"},
	}

	ctx := context.Background()
	results, err := forwardChainer.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	// Check that we get transitive calls at various depths
	foundEdges := make(map[string]bool)
	for _, result := range results {
		key := result.DerivedEdge.SourceID + "->" + result.DerivedEdge.TargetID
		foundEdges[key] = true
	}

	expectedEdges := []string{
		"A->C", // depth 1: A calls B, B calls C
		"B->D", // depth 1: B calls C, C calls D
		"C->E", // depth 1: C calls D, D calls E
		"A->D", // depth 2: A transitively_calls C, C calls D
		"B->E", // depth 2: B transitively_calls D, D calls E
		"A->E", // depth 3: A transitively_calls D, D calls E (or through other paths)
	}

	for _, edge := range expectedEdges {
		if !foundEdges[edge] {
			t.Errorf("Expected derived edge %s not found", edge)
		}
	}
}

// =============================================================================
// Test Helpers
// =============================================================================

func setupBuiltinRulesTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	// Create the inference_rules table
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
			created_at TEXT NOT NULL
		)
	`)
	if err != nil {
		db.Close()
		t.Fatalf("Failed to create table: %v", err)
	}

	// Create the materialized_edges table for the engine
	_, err = db.Exec(`
		CREATE TABLE materialized_edges (
			edge_key TEXT PRIMARY KEY,
			rule_id TEXT NOT NULL,
			evidence_json TEXT NOT NULL,
			derived_at TEXT NOT NULL
		)
	`)
	if err != nil {
		db.Close()
		t.Fatalf("Failed to create materialized_edges table: %v", err)
	}

	return db
}
