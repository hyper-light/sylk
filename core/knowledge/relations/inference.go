package relations

import (
	"fmt"
	"strings"
)

// =============================================================================
// InferenceRule Type (SNE.3) - Stratified Semi-Naive Evaluation Support
// =============================================================================

// InferenceRule represents a logical rule for deriving new edges from existing ones.
// Enhanced with stratification support for semi-naive evaluation.
// A rule has the form: Head :- Body[0], Body[1], ..., Body[N]
// Meaning "if all body conditions match, then the head edge can be inferred".
//
// Stratification enables efficient evaluation by grouping rules into layers (strata)
// where rules in the same stratum can run in parallel, and negation is properly
// handled by ensuring negated predicates are fully computed before use.
type InferenceRule struct {
	// ID uniquely identifies this rule
	ID string `json:"id"`

	// Name is a human-readable name for the rule
	Name string `json:"name"`

	// Head is the triple pattern that will be derived when the body matches
	Head RuleCondition `json:"head"`

	// Body contains the conditions that must all match for the rule to fire
	Body []RuleCondition `json:"body"`

	// Priority determines evaluation order within a stratum (higher = first)
	Priority int `json:"priority"`

	// Enabled indicates whether the rule should be evaluated
	Enabled bool `json:"enabled"`

	// Stratum indicates which layer this rule belongs to for stratified evaluation.
	// Rules in the same stratum can run in parallel.
	// Lower strata are evaluated before higher strata.
	// Default is 0 (base stratum).
	Stratum int `json:"stratum"`

	// Negation indicates whether this rule uses negation in any body condition.
	// Rules with negation affect stratification - the negated predicate must be
	// fully computed before this rule can be evaluated.
	Negation bool `json:"negation"`

	// HeadPredicate is the predicate (edge type) that this rule produces.
	// Extracted for dependency analysis during stratification.
	HeadPredicate string `json:"head_predicate"`

	// BodyPredicates lists all predicates referenced in the rule body.
	// Used for dependency analysis to determine rule ordering.
	BodyPredicates []string `json:"body_predicates"`
}

// RuleCondition represents a triple pattern that can contain variables.
// Variables are prefixed with '?' (e.g., ?x, ?y, ?z) and can be unified
// with actual values from graph edges during inference.
type RuleCondition struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	Negated   bool   `json:"negated"` // If true, this condition must NOT match
}

// NewInferenceRule creates a new InferenceRule with the given parameters.
// It automatically extracts head and body predicates for dependency analysis.
func NewInferenceRule(id, name string, head RuleCondition, body []RuleCondition) *InferenceRule {
	rule := &InferenceRule{
		ID:       id,
		Name:     name,
		Head:     head,
		Body:     body,
		Priority: 0,
		Enabled:  true,
		Stratum:  0,
		Negation: false,
	}

	// Extract head predicate
	rule.HeadPredicate = head.Predicate

	// Extract body predicates and check for negation
	predicateSet := make(map[string]struct{})
	for _, cond := range body {
		if !IsRuleVariable(cond.Predicate) {
			predicateSet[cond.Predicate] = struct{}{}
		}
		if cond.Negated {
			rule.Negation = true
		}
	}

	rule.BodyPredicates = make([]string, 0, len(predicateSet))
	for pred := range predicateSet {
		rule.BodyPredicates = append(rule.BodyPredicates, pred)
	}

	return rule
}

// Validate checks if the rule is well-formed.
// All variables in the head must appear in at least one body condition.
// This ensures that all derived edges have concrete values.
func (r *InferenceRule) Validate() error {
	if err := r.validateBasicFields(); err != nil {
		return err
	}
	return r.validateHeadVariables()
}

// validateBasicFields checks ID, Name, and Body are not empty.
func (r *InferenceRule) validateBasicFields() error {
	if r.ID == "" {
		return fmt.Errorf("rule ID cannot be empty")
	}
	if r.Name == "" {
		return fmt.Errorf("rule name cannot be empty")
	}
	if len(r.Body) == 0 {
		return fmt.Errorf("rule body cannot be empty")
	}
	return nil
}

// validateHeadVariables ensures all head variables appear in body.
func (r *InferenceRule) validateHeadVariables() error {
	headVars := extractRuleVariables(r.Head)
	bodyVars := r.collectBodyVariables()

	for _, headVar := range headVars {
		if !bodyVars[headVar] {
			return fmt.Errorf("head variable %s not found in body", headVar)
		}
	}
	return nil
}

// collectBodyVariables returns a set of all variables in the body.
func (r *InferenceRule) collectBodyVariables() map[string]bool {
	bodyVars := make(map[string]bool)
	for _, condition := range r.Body {
		for _, v := range extractRuleVariables(condition) {
			bodyVars[v] = true
		}
	}
	return bodyVars
}

// GetVariables returns all unique variables used in the rule.
func (r *InferenceRule) GetVariables() []string {
	varSet := r.collectAllVariables()
	return convertVarSetToSlice(varSet)
}

// collectAllVariables gathers all variables from head and body.
func (r *InferenceRule) collectAllVariables() map[string]bool {
	varSet := make(map[string]bool)
	addRuleVariables(varSet, r.Head)
	for _, condition := range r.Body {
		addRuleVariables(varSet, condition)
	}
	return varSet
}

// addRuleVariables adds variables from a condition to the set.
func addRuleVariables(varSet map[string]bool, condition RuleCondition) {
	for _, v := range extractRuleVariables(condition) {
		varSet[v] = true
	}
}

// convertVarSetToSlice converts a variable set to a slice.
func convertVarSetToSlice(varSet map[string]bool) []string {
	vars := make([]string, 0, len(varSet))
	for v := range varSet {
		vars = append(vars, v)
	}
	return vars
}

// extractRuleVariables returns all variables in a condition.
func extractRuleVariables(condition RuleCondition) []string {
	var vars []string
	if IsRuleVariable(condition.Subject) {
		vars = append(vars, condition.Subject)
	}
	if IsRuleVariable(condition.Predicate) {
		vars = append(vars, condition.Predicate)
	}
	if IsRuleVariable(condition.Object) {
		vars = append(vars, condition.Object)
	}
	return vars
}

// IsRuleVariable checks if a term is a variable (starts with '?').
func IsRuleVariable(term string) bool {
	return strings.HasPrefix(term, "?")
}

// DependsOn checks if this rule depends on another rule.
// Rule A depends on rule B if A uses a predicate that B produces.
// This is used for computing rule ordering during stratification.
//
// A dependency exists when:
// - The other rule's head predicate appears in this rule's body predicates
// - If this rule uses negation on the other's head predicate, it's a "negative dependency"
//
// Negative dependencies require special handling in stratification to ensure
// the negated predicate is fully computed before evaluating this rule.
func (r *InferenceRule) DependsOn(other *InferenceRule) bool {
	if other == nil {
		return false
	}

	// Check if this rule's body references the other rule's head predicate
	for _, bodyPred := range r.BodyPredicates {
		if bodyPred == other.HeadPredicate {
			return true
		}
	}

	return false
}

// HasNegativeDependencyOn checks if this rule has a negative dependency on another rule.
// A negative dependency exists when this rule uses negation on a predicate that
// the other rule produces. This is critical for stratification correctness.
func (r *InferenceRule) HasNegativeDependencyOn(other *InferenceRule) bool {
	if other == nil || !r.Negation {
		return false
	}

	// Check if any negated body condition references the other rule's head predicate
	for _, cond := range r.Body {
		if cond.Negated && cond.Predicate == other.HeadPredicate {
			return true
		}
	}

	return false
}

// String returns a human-readable representation of the rule.
func (r *InferenceRule) String() string {
	var b strings.Builder
	b.WriteString(r.Name)
	b.WriteString(" [S")
	b.WriteString(fmt.Sprintf("%d", r.Stratum))
	b.WriteString("]: ")
	b.WriteString(formatRuleCondition(r.Head))
	b.WriteString(" :- ")
	for i, cond := range r.Body {
		if i > 0 {
			b.WriteString(", ")
		}
		if cond.Negated {
			b.WriteString("NOT ")
		}
		b.WriteString(formatRuleCondition(cond))
	}
	return b.String()
}

// formatRuleCondition formats a condition as "Subject-Predicate->Object".
func formatRuleCondition(c RuleCondition) string {
	return fmt.Sprintf("%s-%s->%s", c.Subject, c.Predicate, c.Object)
}

// Clone creates a deep copy of this rule.
func (r *InferenceRule) Clone() *InferenceRule {
	bodyCopy := make([]RuleCondition, len(r.Body))
	copy(bodyCopy, r.Body)

	bodyPredsCopy := make([]string, len(r.BodyPredicates))
	copy(bodyPredsCopy, r.BodyPredicates)

	return &InferenceRule{
		ID:             r.ID,
		Name:           r.Name,
		Head:           r.Head,
		Body:           bodyCopy,
		Priority:       r.Priority,
		Enabled:        r.Enabled,
		Stratum:        r.Stratum,
		Negation:       r.Negation,
		HeadPredicate:  r.HeadPredicate,
		BodyPredicates: bodyPredsCopy,
	}
}

// =============================================================================
// Unification Support for Rule Conditions
// =============================================================================

// Unify attempts to match this condition against an edge, binding variables
// to concrete values. Returns updated bindings and success status.
// Edge fields map to condition fields as: Subject=Subject, Predicate=Predicate, Object=Object.
func (rc RuleCondition) Unify(bindings map[string]string, edge EdgeKey) (map[string]string, bool) {
	newBindings := copyRuleBindings(bindings)

	if !unifyRuleTerm(rc.Subject, edge.Subject, newBindings) {
		return bindings, false
	}
	if !unifyRuleTerm(rc.Predicate, edge.Predicate, newBindings) {
		return bindings, false
	}
	if !unifyRuleTerm(rc.Object, edge.Object, newBindings) {
		return bindings, false
	}

	return newBindings, true
}

// unifyRuleTerm attempts to unify a pattern term with a value.
// If term is a variable, it binds it (or checks existing binding).
// If term is a constant, it checks for equality.
func unifyRuleTerm(term, value string, bindings map[string]string) bool {
	if IsRuleVariable(term) {
		return unifyRuleVariable(term, value, bindings)
	}
	return term == value
}

// unifyRuleVariable binds a variable to a value or checks consistency.
func unifyRuleVariable(variable, value string, bindings map[string]string) bool {
	if existing, bound := bindings[variable]; bound {
		return existing == value
	}
	bindings[variable] = value
	return true
}

// Substitute replaces all variables in the condition with their bound values.
// Returns a new RuleCondition with variables replaced by their bindings.
func (rc RuleCondition) Substitute(bindings map[string]string) RuleCondition {
	return RuleCondition{
		Subject:   substituteRuleTerm(rc.Subject, bindings),
		Predicate: substituteRuleTerm(rc.Predicate, bindings),
		Object:    substituteRuleTerm(rc.Object, bindings),
		Negated:   rc.Negated,
	}
}

// substituteRuleTerm replaces a term with its binding if it's a variable.
func substituteRuleTerm(term string, bindings map[string]string) string {
	if IsRuleVariable(term) {
		if value, ok := bindings[term]; ok {
			return value
		}
	}
	return term
}

// copyRuleBindings creates a deep copy of the bindings map.
func copyRuleBindings(bindings map[string]string) map[string]string {
	result := make(map[string]string, len(bindings))
	for k, v := range bindings {
		result[k] = v
	}
	return result
}
