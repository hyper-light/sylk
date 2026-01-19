package inference

import (
	"context"
)

// =============================================================================
// RuleEvaluator (IE.3.2)
// =============================================================================

// RuleEvaluator evaluates inference rules against a set of edges.
// It performs pattern matching to find all valid variable bindings
// and produces derived edges when rules match.
type RuleEvaluator struct {
	// Stateless evaluator - no fields needed
}

// NewRuleEvaluator creates a new RuleEvaluator.
func NewRuleEvaluator() *RuleEvaluator {
	return &RuleEvaluator{}
}

// EvaluateRule evaluates a single rule against a set of edges.
// It finds all valid variable bindings that satisfy the rule body
// and returns InferenceResults for each derived edge.
func (re *RuleEvaluator) EvaluateRule(ctx context.Context, rule InferenceRule, edges []Edge) []InferenceResult {
	// Find all valid bindings for the rule body
	bindings := re.findBindings(ctx, rule.Body, edges)

	var results []InferenceResult
	for _, binding := range bindings {
		select {
		case <-ctx.Done():
			return results
		default:
		}

		// Apply bindings to the rule head to derive a new edge
		derivedEdge := re.applyBindings(rule.Head, binding.variables)

		// Collect evidence edges that supported this inference
		evidence := make([]EvidenceEdge, len(binding.matchedEdges))
		for i, e := range binding.matchedEdges {
			evidence[i] = e.ToEvidenceEdge()
		}

		// Create the inference result
		result := NewInferenceResult(
			derivedEdge.ToDerivedEdge(),
			rule.ID,
			evidence,
			1.0, // Default confidence for deterministic inference
		)
		result.GenerateProvenance(rule.Name)

		results = append(results, result)
	}

	return results
}

// bindingResult holds a set of variable bindings along with the edges
// that were matched to produce those bindings.
type bindingResult struct {
	variables    map[string]string
	matchedEdges []Edge
}

// findBindings performs backtracking search to find all valid variable bindings
// that satisfy all conditions in the rule body.
func (re *RuleEvaluator) findBindings(ctx context.Context, conditions []RuleCondition, edges []Edge) []bindingResult {
	if len(conditions) == 0 {
		return nil
	}

	// Start with empty bindings
	initialBindings := []bindingResult{
		{
			variables:    make(map[string]string),
			matchedEdges: make([]Edge, 0),
		},
	}

	return re.findBindingsRecursive(ctx, conditions, edges, initialBindings, 0)
}

// findBindingsRecursive recursively finds all valid bindings by processing
// conditions one at a time and backtracking when no match is found.
func (re *RuleEvaluator) findBindingsRecursive(
	ctx context.Context,
	conditions []RuleCondition,
	edges []Edge,
	currentBindings []bindingResult,
	conditionIndex int,
) []bindingResult {
	// Base case: all conditions processed
	if conditionIndex >= len(conditions) {
		return currentBindings
	}

	select {
	case <-ctx.Done():
		return nil
	default:
	}

	condition := conditions[conditionIndex]
	var newBindings []bindingResult

	// For each current binding, try to extend it with matches for this condition
	for _, binding := range currentBindings {
		// Find all edges that match this condition with current bindings
		matches := re.matchCondition(condition, edges, binding.variables)

		for _, match := range matches {
			// Create new binding result with extended bindings and matched edge
			newBinding := bindingResult{
				variables:    match.bindings,
				matchedEdges: append(cloneEdges(binding.matchedEdges), match.edge),
			}
			newBindings = append(newBindings, newBinding)
		}
	}

	// If no matches found, this branch fails (backtrack)
	if len(newBindings) == 0 {
		return nil
	}

	// Continue with next condition
	return re.findBindingsRecursive(ctx, conditions, edges, newBindings, conditionIndex+1)
}

// conditionMatch holds the result of matching a condition against an edge.
type conditionMatch struct {
	edge     Edge
	bindings map[string]string
}

// matchCondition finds all edges that match a condition given current bindings.
// Returns all valid (edge, extended-bindings) pairs.
func (re *RuleEvaluator) matchCondition(condition RuleCondition, edges []Edge, bindings map[string]string) []conditionMatch {
	var matches []conditionMatch

	for _, edge := range edges {
		// Try to unify the condition with this edge
		newBindings, ok := condition.Unify(bindings, edge.Source, edge.Target, edge.Predicate)
		if ok {
			matches = append(matches, conditionMatch{
				edge:     edge,
				bindings: newBindings,
			})
		}
	}

	return matches
}

// applyBindings creates a derived edge by substituting variables in the rule head
// with their bound values.
func (re *RuleEvaluator) applyBindings(head RuleCondition, bindings map[string]string) Edge {
	substituted := head.Substitute(bindings)
	return Edge{
		Source:    substituted.Subject,
		Predicate: substituted.Predicate,
		Target:    substituted.Object,
	}
}

// cloneEdges creates a shallow copy of an edge slice.
func cloneEdges(edges []Edge) []Edge {
	if edges == nil {
		return nil
	}
	result := make([]Edge, len(edges))
	copy(result, edges)
	return result
}
