package inference

import (
	"context"
	"fmt"
)

// =============================================================================
// Built-in Rule IDs (IE.6.1)
// =============================================================================

// Built-in rule IDs are prefixed with "builtin_" to distinguish them from user-defined rules.
const (
	// BuiltinTransitiveImportID is the ID for the transitive import rule.
	BuiltinTransitiveImportID = "builtin_transitive_import"

	// BuiltinInheritedMethodID is the ID for the inherited method rule.
	BuiltinInheritedMethodID = "builtin_inherited_method"

	// BuiltinInterfaceSatisfactionID is the ID for the interface satisfaction rule.
	BuiltinInterfaceSatisfactionID = "builtin_interface_satisfaction"

	// BuiltinCallChainID is the ID for the call chain rule.
	BuiltinCallChainID = "builtin_call_chain"

	// BuiltinReferenceClosureID is the ID for the reference closure rule.
	BuiltinReferenceClosureID = "builtin_reference_closure"

	// BuiltinCallChainDepth2ID is the ID for the call chain depth 2 rule.
	BuiltinCallChainDepth2ID = "builtin_call_chain_depth2"

	// BuiltinCallChainDepth3ID is the ID for the call chain depth 3 rule.
	BuiltinCallChainDepth3ID = "builtin_call_chain_depth3"

	// BuiltinReferenceClosureDepth2ID is the ID for the reference closure depth 2 rule.
	BuiltinReferenceClosureDepth2ID = "builtin_reference_closure_depth2"

	// BuiltinReferenceClosureDepth3ID is the ID for the reference closure depth 3 rule.
	BuiltinReferenceClosureDepth3ID = "builtin_reference_closure_depth3"
)

// =============================================================================
// Built-in Rule Priority Constants
// =============================================================================

// Built-in rules use priority values in the 100+ range to give precedence
// to user-defined rules (which typically use lower priority values).
const (
	// PriorityTransitiveImport is the priority for transitive import rules.
	PriorityTransitiveImport = 100

	// PriorityInheritedMethod is the priority for inherited method rules.
	PriorityInheritedMethod = 110

	// PriorityInterfaceSatisfaction is the priority for interface satisfaction rules.
	PriorityInterfaceSatisfaction = 120

	// PriorityCallChain is the priority for call chain rules.
	PriorityCallChain = 130

	// PriorityReferenceClosure is the priority for reference closure rules.
	PriorityReferenceClosure = 140
)

// =============================================================================
// Built-in Rule Functions (IE.6.1)
// =============================================================================

// TransitiveImportRule creates a rule that derives indirect imports.
// Rule: A imports B, B imports C -> A indirectly_imports C
func TransitiveImportRule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinTransitiveImportID,
		Name: "Transitive Import",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "indirectly_imports",
			Object:    "?C",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "imports", Object: "?B"},
			{Subject: "?B", Predicate: "imports", Object: "?C"},
		},
		Priority: PriorityTransitiveImport,
		Enabled:  true,
	}
}

// InheritedMethodRule creates a rule that derives inherited methods from parent types.
// Rule: A extends B, B has_method M -> A has_method M
func InheritedMethodRule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinInheritedMethodID,
		Name: "Inherited Method",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "has_method",
			Object:    "?M",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "extends", Object: "?B"},
			{Subject: "?B", Predicate: "has_method", Object: "?M"},
		},
		Priority: PriorityInheritedMethod,
		Enabled:  true,
	}
}

// InterfaceSatisfactionRule creates a rule that derives interface satisfaction.
// This is a simplified rule: A declares_method M, I requires_method M -> A satisfies I
// Note: Full interface satisfaction requires checking ALL methods. This rule
// handles the common case where method compatibility is tracked explicitly.
// For complex cases, multiple rules or custom logic may be needed.
func InterfaceSatisfactionRule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinInterfaceSatisfactionID,
		Name: "Interface Satisfaction",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "satisfies",
			Object:    "?I",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "implements", Object: "?I"},
		},
		Priority: PriorityInterfaceSatisfaction,
		Enabled:  true,
	}
}

// CallChainRule creates a rule that derives transitive call relationships.
// Rule: A calls B, B calls C -> A transitively_calls C
func CallChainRule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinCallChainID,
		Name: "Call Chain",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "transitively_calls",
			Object:    "?C",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "calls", Object: "?B"},
			{Subject: "?B", Predicate: "calls", Object: "?C"},
		},
		Priority: PriorityCallChain,
		Enabled:  true,
	}
}

// CallChainDepth2Rule creates a rule that extends call chain to depth 2.
// Rule: A transitively_calls B, B calls C -> A transitively_calls C
// This builds on the base CallChainRule to extend transitive calls.
func CallChainDepth2Rule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinCallChainDepth2ID,
		Name: "Call Chain Depth 2",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "transitively_calls",
			Object:    "?C",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "transitively_calls", Object: "?B"},
			{Subject: "?B", Predicate: "calls", Object: "?C"},
		},
		Priority: PriorityCallChain + 1,
		Enabled:  true,
	}
}

// CallChainDepth3Rule creates a rule that extends call chain to depth 3.
// Rule: A transitively_calls B, B transitively_calls C -> A transitively_calls C
// This allows the call chain to grow further through derived edges.
func CallChainDepth3Rule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinCallChainDepth3ID,
		Name: "Call Chain Depth 3",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "transitively_calls",
			Object:    "?C",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "transitively_calls", Object: "?B"},
			{Subject: "?B", Predicate: "transitively_calls", Object: "?C"},
		},
		Priority: PriorityCallChain + 2,
		Enabled:  true,
	}
}

// ReferenceClosureRule creates a rule that derives transitive reference relationships.
// Rule: A references B, B references C -> A transitively_refs C
func ReferenceClosureRule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinReferenceClosureID,
		Name: "Reference Closure",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "transitively_refs",
			Object:    "?C",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "references", Object: "?B"},
			{Subject: "?B", Predicate: "references", Object: "?C"},
		},
		Priority: PriorityReferenceClosure,
		Enabled:  true,
	}
}

// ReferenceClosureDepth2Rule creates a rule that extends reference closure to depth 2.
// Rule: A transitively_refs B, B references C -> A transitively_refs C
func ReferenceClosureDepth2Rule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinReferenceClosureDepth2ID,
		Name: "Reference Closure Depth 2",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "transitively_refs",
			Object:    "?C",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "transitively_refs", Object: "?B"},
			{Subject: "?B", Predicate: "references", Object: "?C"},
		},
		Priority: PriorityReferenceClosure + 1,
		Enabled:  true,
	}
}

// ReferenceClosureDepth3Rule creates a rule that extends reference closure to depth 3.
// Rule: A transitively_refs B, B transitively_refs C -> A transitively_refs C
func ReferenceClosureDepth3Rule() InferenceRule {
	return InferenceRule{
		ID:   BuiltinReferenceClosureDepth3ID,
		Name: "Reference Closure Depth 3",
		Head: RuleCondition{
			Subject:   "?A",
			Predicate: "transitively_refs",
			Object:    "?C",
		},
		Body: []RuleCondition{
			{Subject: "?A", Predicate: "transitively_refs", Object: "?B"},
			{Subject: "?B", Predicate: "transitively_refs", Object: "?C"},
		},
		Priority: PriorityReferenceClosure + 2,
		Enabled:  true,
	}
}

// =============================================================================
// Built-in Rules Collection
// =============================================================================

// BuiltinRules returns all built-in inference rules.
// These rules provide common code analysis patterns for deriving relationships.
func BuiltinRules() []InferenceRule {
	return []InferenceRule{
		TransitiveImportRule(),
		InheritedMethodRule(),
		InterfaceSatisfactionRule(),
		CallChainRule(),
		CallChainDepth2Rule(),
		CallChainDepth3Rule(),
		ReferenceClosureRule(),
		ReferenceClosureDepth2Rule(),
		ReferenceClosureDepth3Rule(),
	}
}

// builtinRuleIndex is a map of rule IDs to their constructor functions.
var builtinRuleIndex = map[string]func() InferenceRule{
	BuiltinTransitiveImportID:      TransitiveImportRule,
	BuiltinInheritedMethodID:       InheritedMethodRule,
	BuiltinInterfaceSatisfactionID: InterfaceSatisfactionRule,
	BuiltinCallChainID:             CallChainRule,
	BuiltinCallChainDepth2ID:       CallChainDepth2Rule,
	BuiltinCallChainDepth3ID:       CallChainDepth3Rule,
	BuiltinReferenceClosureID:      ReferenceClosureRule,
	BuiltinReferenceClosureDepth2ID: ReferenceClosureDepth2Rule,
	BuiltinReferenceClosureDepth3ID: ReferenceClosureDepth3Rule,
}

// GetBuiltinRuleByID returns a built-in rule by its ID.
// Returns the rule and true if found, or nil and false if not found.
func GetBuiltinRuleByID(id string) (*InferenceRule, bool) {
	constructor, ok := builtinRuleIndex[id]
	if !ok {
		return nil, false
	}
	rule := constructor()
	return &rule, true
}

// =============================================================================
// Validation
// =============================================================================

// ValidateBuiltinRule checks if a built-in rule is well-formed.
// This performs the same validation as InferenceRule.Validate() plus
// additional checks specific to built-in rules (e.g., ID prefix).
func ValidateBuiltinRule(rule InferenceRule) error {
	// First, perform standard validation
	if err := rule.Validate(); err != nil {
		return fmt.Errorf("standard validation failed: %w", err)
	}

	// Check that the rule ID has the builtin prefix
	if !isBuiltinID(rule.ID) {
		return fmt.Errorf("built-in rule ID must start with 'builtin_': got %q", rule.ID)
	}

	// Check that priority is in the built-in range (100+)
	if rule.Priority < 100 {
		return fmt.Errorf("built-in rule priority must be >= 100: got %d", rule.Priority)
	}

	return nil
}

// isBuiltinID checks if an ID has the built-in prefix.
func isBuiltinID(id string) bool {
	const prefix = "builtin_"
	return len(id) > len(prefix) && id[:len(prefix)] == prefix
}

// =============================================================================
// Loading Built-in Rules
// =============================================================================

// LoadBuiltinRules loads all built-in rules into the inference engine.
// Rules that already exist in the engine are skipped to preserve any
// user modifications (e.g., disabled state).
func LoadBuiltinRules(ctx context.Context, engine *InferenceEngine) error {
	if engine == nil {
		return fmt.Errorf("engine cannot be nil")
	}

	rules := BuiltinRules()
	for _, rule := range rules {
		if err := loadSingleBuiltinRule(ctx, engine, rule); err != nil {
			return fmt.Errorf("load rule %s: %w", rule.ID, err)
		}
	}

	return nil
}

// loadSingleBuiltinRule loads a single built-in rule if it doesn't exist.
func loadSingleBuiltinRule(ctx context.Context, engine *InferenceEngine, rule InferenceRule) error {
	// Check if rule already exists
	existing, err := engine.RuleStore().GetRule(ctx, rule.ID)
	if err != nil {
		return fmt.Errorf("check existing rule: %w", err)
	}

	// Skip if rule already exists
	if existing != nil {
		return nil
	}

	// Validate before adding
	if err := ValidateBuiltinRule(rule); err != nil {
		return fmt.Errorf("validate rule: %w", err)
	}

	// Add the rule
	if err := engine.AddRule(ctx, rule); err != nil {
		return fmt.Errorf("add rule: %w", err)
	}

	return nil
}

// =============================================================================
// Utility Functions
// =============================================================================

// IsBuiltinRule checks if a rule ID corresponds to a built-in rule.
func IsBuiltinRule(id string) bool {
	_, ok := builtinRuleIndex[id]
	return ok
}

// GetBuiltinRuleIDs returns the IDs of all built-in rules.
func GetBuiltinRuleIDs() []string {
	ids := make([]string, 0, len(builtinRuleIndex))
	for id := range builtinRuleIndex {
		ids = append(ids, id)
	}
	return ids
}

// ResetBuiltinRule resets a built-in rule to its default configuration.
// This is useful when a user wants to restore a modified built-in rule.
func ResetBuiltinRule(ctx context.Context, engine *InferenceEngine, id string) error {
	rule, ok := GetBuiltinRuleByID(id)
	if !ok {
		return fmt.Errorf("not a built-in rule: %s", id)
	}

	// AddRule uses upsert, so this will update the existing rule
	if err := engine.AddRule(ctx, *rule); err != nil {
		return fmt.Errorf("reset rule: %w", err)
	}

	return nil
}
