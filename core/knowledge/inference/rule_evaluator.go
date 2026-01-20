package inference

import (
	"context"
	"sort"
	"strings"
)

// =============================================================================
// RuleEvaluator (IE.3.2)
// =============================================================================

// RuleEvaluator evaluates inference rules against a set of edges.
// It performs pattern matching to find all valid variable bindings
// and produces derived edges when rules match.
//
// PF.4.7-4.8: Enhanced with memoization cache and edge indexes for
// efficient condition matching. The cache is cleared per evaluation session.
type RuleEvaluator struct {
	// matchCache stores memoized condition matches keyed by condition+bindings.
	// Cleared at the start of each EvaluateRule call.
	matchCache map[string][]conditionMatch

	// edgeIndex provides O(1) lookups for edges by source, target, or type.
	// Built once per edge set during evaluation.
	edgeIndex *EdgeIndex
}

// EdgeIndex provides efficient edge lookups by various criteria.
// PF.4.8: This replaces full edge scans with O(1) map lookups.
type EdgeIndex struct {
	bySource     map[string][]Edge // source -> edges with that source
	byTarget     map[string][]Edge // target -> edges with that target
	byPredicate  map[string][]Edge // predicate -> edges with that predicate
	bySourcePred map[string][]Edge // source:predicate -> edges
	byTargetPred map[string][]Edge // target:predicate -> edges
	allEdges     []Edge            // fallback: all edges for full scans
}

// NewRuleEvaluator creates a new RuleEvaluator.
func NewRuleEvaluator() *RuleEvaluator {
	return &RuleEvaluator{
		matchCache: make(map[string][]conditionMatch),
	}
}

// EvaluateRule evaluates a single rule against a set of edges.
// It finds all valid variable bindings that satisfy the rule body
// and returns InferenceResults for each derived edge.
//
// PF.4.7-4.8: Now uses memoization and edge indexing for improved performance.
// The memoization cache is cleared at the start of each evaluation session.
func (re *RuleEvaluator) EvaluateRule(ctx context.Context, rule InferenceRule, edges []Edge) []InferenceResult {
	// Clear memoization cache for this evaluation session (PF.4.7)
	re.clearCache()

	// Build edge index for efficient lookups (PF.4.8)
	re.edgeIndex = re.buildEdgeIndex(edges)

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

	// Ensure edge index is available (PF.4.8)
	// This handles the case when findBindings is called directly without EvaluateRule
	if re.edgeIndex == nil {
		re.edgeIndex = re.buildEdgeIndex(edges)
	}

	// Ensure cache is initialized (PF.4.7)
	if re.matchCache == nil {
		re.matchCache = make(map[string][]conditionMatch)
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
//
// PF.4.7-4.8: Uses memoized and indexed matching for improved performance.
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
		// PF.4.7: Use memoized matching with cache
		matches := re.matchConditionCached(condition, binding.variables)

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

// =============================================================================
// PF.4.7: Memoization Cache
// =============================================================================

// clearCache resets the memoization cache for a new evaluation session.
func (re *RuleEvaluator) clearCache() {
	re.matchCache = make(map[string][]conditionMatch)
}

// conditionCacheKey generates a unique cache key for a condition and bindings.
// The key combines the condition's triple pattern with the current bound variables.
func (re *RuleEvaluator) conditionCacheKey(condition RuleCondition, bindings map[string]string) string {
	var sb strings.Builder

	// Include the condition pattern
	sb.WriteString(condition.Subject)
	sb.WriteByte('|')
	sb.WriteString(condition.Predicate)
	sb.WriteByte('|')
	sb.WriteString(condition.Object)
	sb.WriteByte('|')

	// Include relevant bindings in sorted order for deterministic keys
	// Only include bindings that are variables in this condition
	relevantVars := make([]string, 0, 3)
	if IsVariable(condition.Subject) {
		relevantVars = append(relevantVars, condition.Subject)
	}
	if IsVariable(condition.Predicate) {
		relevantVars = append(relevantVars, condition.Predicate)
	}
	if IsVariable(condition.Object) {
		relevantVars = append(relevantVars, condition.Object)
	}

	sort.Strings(relevantVars)
	for _, v := range relevantVars {
		if val, ok := bindings[v]; ok {
			sb.WriteString(v)
			sb.WriteByte('=')
			sb.WriteString(val)
			sb.WriteByte(',')
		}
	}

	return sb.String()
}

// matchConditionCached finds all edges that match a condition given current bindings,
// using memoization to avoid redundant computation.
// PF.4.7: Returns cached results if available, otherwise computes and caches.
func (re *RuleEvaluator) matchConditionCached(condition RuleCondition, bindings map[string]string) []conditionMatch {
	cacheKey := re.conditionCacheKey(condition, bindings)

	if cached, ok := re.matchCache[cacheKey]; ok {
		return cached
	}

	// PF.4.8: Use indexed matching instead of full edge scan
	matches := re.matchConditionIndexed(condition, bindings)
	re.matchCache[cacheKey] = matches
	return matches
}

// =============================================================================
// PF.4.8: Edge Index
// =============================================================================

// buildEdgeIndex creates an EdgeIndex from a slice of edges for efficient lookups.
func (re *RuleEvaluator) buildEdgeIndex(edges []Edge) *EdgeIndex {
	idx := &EdgeIndex{
		bySource:     make(map[string][]Edge),
		byTarget:     make(map[string][]Edge),
		byPredicate:  make(map[string][]Edge),
		bySourcePred: make(map[string][]Edge),
		byTargetPred: make(map[string][]Edge),
		allEdges:     edges,
	}

	for _, e := range edges {
		idx.bySource[e.Source] = append(idx.bySource[e.Source], e)
		idx.byTarget[e.Target] = append(idx.byTarget[e.Target], e)
		idx.byPredicate[e.Predicate] = append(idx.byPredicate[e.Predicate], e)

		sourcePredKey := e.Source + ":" + e.Predicate
		idx.bySourcePred[sourcePredKey] = append(idx.bySourcePred[sourcePredKey], e)

		targetPredKey := e.Target + ":" + e.Predicate
		idx.byTargetPred[targetPredKey] = append(idx.byTargetPred[targetPredKey], e)
	}

	return idx
}

// matchConditionIndexed finds all edges that match a condition using the edge index.
// It selects the most selective index based on which variables are already bound.
// PF.4.8: Replaces full edge scans with indexed lookups.
func (re *RuleEvaluator) matchConditionIndexed(condition RuleCondition, bindings map[string]string) []conditionMatch {
	if re.edgeIndex == nil {
		return nil
	}

	// Determine which parts of the condition are bound vs variable
	var boundSource, boundTarget, boundPred string
	var hasSource, hasTarget, hasPred bool

	// Check if subject is bound (either a constant or a bound variable)
	if !IsVariable(condition.Subject) {
		boundSource = condition.Subject
		hasSource = true
	} else if val, ok := bindings[condition.Subject]; ok {
		boundSource = val
		hasSource = true
	}

	// Check if object is bound
	if !IsVariable(condition.Object) {
		boundTarget = condition.Object
		hasTarget = true
	} else if val, ok := bindings[condition.Object]; ok {
		boundTarget = val
		hasTarget = true
	}

	// Check if predicate is bound
	if !IsVariable(condition.Predicate) {
		boundPred = condition.Predicate
		hasPred = true
	} else if val, ok := bindings[condition.Predicate]; ok {
		boundPred = val
		hasPred = true
	}

	// Select the most selective index to use
	var candidateEdges []Edge

	switch {
	case hasSource && hasPred:
		// Most selective: source + predicate
		key := boundSource + ":" + boundPred
		candidateEdges = re.edgeIndex.bySourcePred[key]
	case hasTarget && hasPred:
		// Second most selective: target + predicate
		key := boundTarget + ":" + boundPred
		candidateEdges = re.edgeIndex.byTargetPred[key]
	case hasSource:
		candidateEdges = re.edgeIndex.bySource[boundSource]
	case hasTarget:
		candidateEdges = re.edgeIndex.byTarget[boundTarget]
	case hasPred:
		candidateEdges = re.edgeIndex.byPredicate[boundPred]
	default:
		// Fallback to full scan
		candidateEdges = re.edgeIndex.allEdges
	}

	// Filter candidates through unification
	return re.filterMatches(candidateEdges, condition, bindings)
}

// filterMatches filters candidate edges through unification with the condition.
func (re *RuleEvaluator) filterMatches(candidates []Edge, condition RuleCondition, bindings map[string]string) []conditionMatch {
	var matches []conditionMatch

	for _, edge := range candidates {
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
