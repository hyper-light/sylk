package inference

import (
	"fmt"
	"strings"
)

// =============================================================================
// InferenceRule Type (IE.1.2)
// =============================================================================

// InferenceRule represents a logical rule for deriving new edges from existing ones.
// A rule has the form: Head :- Body[0], Body[1], ..., Body[N]
// Meaning "if all body conditions match, then the head edge can be inferred".
type InferenceRule struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Head     RuleCondition   `json:"head"`
	Body     []RuleCondition `json:"body"`
	Priority int             `json:"priority"`
	Enabled  bool            `json:"enabled"`
}

// Validate checks if the rule is well-formed.
// All variables in the head must appear in at least one body condition.
// This ensures that all derived edges have concrete values.
func (r InferenceRule) Validate() error {
	if err := r.validateBasicFields(); err != nil {
		return err
	}
	return r.validateHeadVariables()
}

// validateBasicFields checks ID, Name, and Body are not empty.
func (r InferenceRule) validateBasicFields() error {
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
func (r InferenceRule) validateHeadVariables() error {
	headVars := extractVariables(r.Head)
	bodyVars := r.collectBodyVariables()

	for _, headVar := range headVars {
		if !bodyVars[headVar] {
			return fmt.Errorf("head variable %s not found in body", headVar)
		}
	}
	return nil
}

// collectBodyVariables returns a set of all variables in the body.
func (r InferenceRule) collectBodyVariables() map[string]bool {
	bodyVars := make(map[string]bool)
	for _, condition := range r.Body {
		for _, v := range extractVariables(condition) {
			bodyVars[v] = true
		}
	}
	return bodyVars
}

// GetVariables returns all unique variables used in the rule.
func (r InferenceRule) GetVariables() []string {
	varSet := r.collectAllVariables()
	return convertSetToSlice(varSet)
}

// collectAllVariables gathers all variables from head and body.
func (r InferenceRule) collectAllVariables() map[string]bool {
	varSet := make(map[string]bool)
	addVariables(varSet, r.Head)
	for _, condition := range r.Body {
		addVariables(varSet, condition)
	}
	return varSet
}

// addVariables adds variables from a condition to the set.
func addVariables(varSet map[string]bool, condition RuleCondition) {
	for _, v := range extractVariables(condition) {
		varSet[v] = true
	}
}

// convertSetToSlice converts a variable set to a slice.
func convertSetToSlice(varSet map[string]bool) []string {
	vars := make([]string, 0, len(varSet))
	for v := range varSet {
		vars = append(vars, v)
	}
	return vars
}

// extractVariables returns all variables in a condition.
func extractVariables(condition RuleCondition) []string {
	var vars []string
	if IsVariable(condition.Subject) {
		vars = append(vars, condition.Subject)
	}
	if IsVariable(condition.Predicate) {
		vars = append(vars, condition.Predicate)
	}
	if IsVariable(condition.Object) {
		vars = append(vars, condition.Object)
	}
	return vars
}

// String returns a human-readable representation of the rule.
func (r InferenceRule) String() string {
	var b strings.Builder
	b.WriteString(r.Name)
	b.WriteString(": ")
	b.WriteString(formatCondition(r.Head))
	b.WriteString(" :- ")
	for i, cond := range r.Body {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(formatCondition(cond))
	}
	return b.String()
}

// formatCondition formats a condition as "Subject-Predicate->Object".
func formatCondition(c RuleCondition) string {
	return fmt.Sprintf("%s-%s->%s", c.Subject, c.Predicate, c.Object)
}
