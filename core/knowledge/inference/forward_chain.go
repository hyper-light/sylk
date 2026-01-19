package inference

import (
	"context"
	"fmt"
)

// =============================================================================
// ForwardChainer (IE.3.1)
// =============================================================================

// DefaultMaxIterations is the default maximum number of iterations
// to prevent infinite loops during forward chaining.
const DefaultMaxIterations = 100

// ForwardChainer implements forward chaining inference.
// It repeatedly applies rules until no new edges can be derived (fixpoint)
// or the maximum iteration limit is reached.
type ForwardChainer struct {
	evaluator     *RuleEvaluator
	maxIterations int
}

// NewForwardChainer creates a new ForwardChainer with the given evaluator
// and maximum iterations limit.
func NewForwardChainer(evaluator *RuleEvaluator, maxIterations int) *ForwardChainer {
	if maxIterations <= 0 {
		maxIterations = DefaultMaxIterations
	}
	return &ForwardChainer{
		evaluator:     evaluator,
		maxIterations: maxIterations,
	}
}

// ErrMaxIterationsReached is returned when the forward chainer hits the iteration limit.
var ErrMaxIterationsReached = fmt.Errorf("forward chaining reached maximum iterations limit")

// Evaluate runs forward chaining on the given rules and edges.
// It iterates until either:
// - A fixpoint is reached (no new edges derived)
// - The maximum iteration limit is reached (returns error)
// - The context is cancelled
//
// Returns all derived edges as InferenceResults.
func (fc *ForwardChainer) Evaluate(ctx context.Context, rules []InferenceRule, edges []Edge) ([]InferenceResult, error) {
	if fc.evaluator == nil {
		return nil, fmt.Errorf("forward chainer has no evaluator")
	}

	// Track all derived edges to avoid duplicates
	derived := make(map[string]bool)

	// Mark existing edges as already known
	for _, e := range edges {
		derived[e.Key()] = true
	}

	// Working set of edges (initially just the input edges)
	workingEdges := make([]Edge, len(edges))
	copy(workingEdges, edges)

	// Collect all results across iterations
	var allResults []InferenceResult

	for iteration := 0; iteration < fc.maxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return allResults, ctx.Err()
		default:
		}

		// Run one iteration
		newResults, done, err := fc.IterateOnce(ctx, rules, workingEdges, derived)
		if err != nil {
			return allResults, err
		}

		// Collect new results
		allResults = append(allResults, newResults...)

		// Add newly derived edges to working set
		for _, result := range newResults {
			newEdge := EdgeFromDerivedEdge(result.DerivedEdge)
			workingEdges = append(workingEdges, newEdge)
		}

		// Check for fixpoint
		if done {
			return allResults, nil
		}
	}

	// Reached max iterations without fixpoint
	return allResults, ErrMaxIterationsReached
}

// IterateOnce performs a single iteration of forward chaining.
// It evaluates all rules against the current edges and returns:
// - newResults: InferenceResults for newly derived edges
// - done: true if no new edges were derived (fixpoint reached)
// - error: any error that occurred
func (fc *ForwardChainer) IterateOnce(
	ctx context.Context,
	rules []InferenceRule,
	edges []Edge,
	derived map[string]bool,
) ([]InferenceResult, bool, error) {
	if fc.evaluator == nil {
		return nil, false, fmt.Errorf("forward chainer has no evaluator")
	}

	var newResults []InferenceResult

	// Evaluate each enabled rule
	for _, rule := range rules {
		select {
		case <-ctx.Done():
			return newResults, false, ctx.Err()
		default:
		}

		// Skip disabled rules
		if !rule.Enabled {
			continue
		}

		// Evaluate the rule
		results := fc.evaluator.EvaluateRule(ctx, rule, edges)

		// Filter out already-derived edges
		for _, result := range results {
			derivedEdge := EdgeFromDerivedEdge(result.DerivedEdge)
			key := derivedEdge.Key()

			if !derived[key] {
				derived[key] = true
				newResults = append(newResults, result)
			}
		}
	}

	// If no new results, we've reached a fixpoint
	done := len(newResults) == 0

	return newResults, done, nil
}

// GetMaxIterations returns the maximum iterations limit.
func (fc *ForwardChainer) GetMaxIterations() int {
	return fc.maxIterations
}

// SetMaxIterations updates the maximum iterations limit.
func (fc *ForwardChainer) SetMaxIterations(maxIterations int) {
	if maxIterations > 0 {
		fc.maxIterations = maxIterations
	}
}
