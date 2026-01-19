package inference

import "strings"

// =============================================================================
// RuleCondition Type (IE.1.1)
// =============================================================================

// RuleCondition represents a triple pattern that can contain variables.
// Variables are prefixed with '?' (e.g., ?x, ?y, ?z) and can be unified
// with actual values from graph edges during inference.
type RuleCondition struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

// IsVariable checks if a term is a variable (starts with '?').
func IsVariable(term string) bool {
	return strings.HasPrefix(term, "?")
}

// Unify attempts to match this condition against an edge, binding variables
// to concrete values. Returns updated bindings and success status.
// Edge fields map to condition fields as: SourceID=Subject, EdgeType=Predicate, TargetID=Object.
func (rc RuleCondition) Unify(bindings map[string]string, sourceID, targetID, edgeType string) (map[string]string, bool) {
	newBindings := copyBindings(bindings)

	if !unifyTerm(rc.Subject, sourceID, newBindings) {
		return bindings, false
	}
	if !unifyTerm(rc.Predicate, edgeType, newBindings) {
		return bindings, false
	}
	if !unifyTerm(rc.Object, targetID, newBindings) {
		return bindings, false
	}

	return newBindings, true
}

// unifyTerm attempts to unify a pattern term with a value.
// If term is a variable, it binds it (or checks existing binding).
// If term is a constant, it checks for equality.
func unifyTerm(term, value string, bindings map[string]string) bool {
	if IsVariable(term) {
		return unifyVariable(term, value, bindings)
	}
	return term == value
}

// unifyVariable binds a variable to a value or checks consistency.
func unifyVariable(variable, value string, bindings map[string]string) bool {
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
		Subject:   substituteTerm(rc.Subject, bindings),
		Predicate: substituteTerm(rc.Predicate, bindings),
		Object:    substituteTerm(rc.Object, bindings),
	}
}

// substituteTerm replaces a term with its binding if it's a variable.
func substituteTerm(term string, bindings map[string]string) string {
	if IsVariable(term) {
		if value, ok := bindings[term]; ok {
			return value
		}
	}
	return term
}

// copyBindings creates a deep copy of the bindings map.
func copyBindings(bindings map[string]string) map[string]string {
	result := make(map[string]string, len(bindings))
	for k, v := range bindings {
		result[k] = v
	}
	return result
}
