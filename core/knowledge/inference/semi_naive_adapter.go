package inference

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/core/knowledge/relations"
)

// =============================================================================
// SNE.12: SemiNaiveAdapter - Integration with Inference Engine
// =============================================================================

// SemiNaiveAdapter adapts the relations.SemiNaiveEvaluator to work with the
// inference engine's data structures. It provides a bridge between the
// edge-based semi-naive evaluation and the inference engine's InferenceRule
// and Edge types.
type SemiNaiveAdapter struct {
	evaluator     *relations.SemiNaiveEvaluator
	maxIterations int
}

// NewSemiNaiveAdapter creates a new adapter that wraps a SemiNaiveEvaluator.
// The adapter converts between inference package types (InferenceRule, Edge)
// and relations package types (InferenceRule, EdgeKey).
func NewSemiNaiveAdapter(rules []InferenceRule) (*SemiNaiveAdapter, error) {
	// Convert inference rules to relations rules
	relRules := make([]*relations.InferenceRule, len(rules))
	for i, rule := range rules {
		relRules[i] = convertToRelationsRule(rule)
	}

	evaluator, err := relations.NewSemiNaiveEvaluator(relRules)
	if err != nil {
		return nil, fmt.Errorf("create semi-naive evaluator: %w", err)
	}

	return &SemiNaiveAdapter{
		evaluator:     evaluator,
		maxIterations: 1000,
	}, nil
}

// SetMaxIterations sets the maximum number of iterations for fixpoint computation.
func (a *SemiNaiveAdapter) SetMaxIterations(max int) {
	if max > 0 {
		a.maxIterations = max
		a.evaluator.SetMaxIterations(max)
	}
}

// Evaluate runs semi-naive evaluation on the given edges using the configured rules.
// Returns InferenceResults for all derived edges.
func (a *SemiNaiveAdapter) Evaluate(ctx context.Context, rules []InferenceRule, edges []Edge) ([]InferenceResult, error) {
	// Convert edges to database
	db := relations.NewInMemoryDatabase()
	for _, edge := range edges {
		db.AddEdge(relations.EdgeKey{
			Subject:   edge.Source,
			Predicate: edge.Predicate,
			Object:    edge.Target,
		})
	}

	// Run semi-naive evaluation with context
	derived, err := a.evaluator.EvaluateWithContext(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("semi-naive evaluation: %w", err)
	}

	// Convert derived edges to InferenceResults
	results := make([]InferenceResult, 0, derived.Size())
	for _, edgeKey := range derived.Edges() {
		// Find which rule produced this edge (simplified: use first matching rule)
		// In production, track provenance during evaluation
		ruleID := a.findProducingRule(rules, edgeKey)

		result := NewInferenceResult(
			DerivedEdge{
				SourceID: edgeKey.Subject,
				EdgeType: edgeKey.Predicate,
				TargetID: edgeKey.Object,
			},
			ruleID,
			nil, // Evidence would need to be tracked during evaluation
			1.0, // Default confidence
		)

		results = append(results, result)
	}

	return results, nil
}

// EvaluateIncremental performs incremental semi-naive evaluation when new edges are added.
// Returns InferenceResults for newly derived edges.
func (a *SemiNaiveAdapter) EvaluateIncremental(ctx context.Context, rules []InferenceRule, existingEdges []Edge, newEdges []Edge) ([]InferenceResult, error) {
	// Convert existing edges to database
	db := relations.NewInMemoryDatabase()
	for _, edge := range existingEdges {
		db.AddEdge(relations.EdgeKey{
			Subject:   edge.Source,
			Predicate: edge.Predicate,
			Object:    edge.Target,
		})
	}

	// Create delta table with new edges
	changes := relations.NewDeltaTable()
	for _, edge := range newEdges {
		edgeKey := relations.EdgeKey{
			Subject:   edge.Source,
			Predicate: edge.Predicate,
			Object:    edge.Target,
		}
		changes.Add(edgeKey)
		db.AddEdge(edgeKey)
	}

	// Run incremental evaluation
	derived, err := a.evaluator.EvaluateIncremental(ctx, db, changes)
	if err != nil {
		return nil, fmt.Errorf("incremental semi-naive evaluation: %w", err)
	}

	// Convert derived edges to InferenceResults
	results := make([]InferenceResult, 0, derived.Size())
	for _, edgeKey := range derived.Edges() {
		ruleID := a.findProducingRule(rules, edgeKey)

		result := NewInferenceResult(
			DerivedEdge{
				SourceID: edgeKey.Subject,
				EdgeType: edgeKey.Predicate,
				TargetID: edgeKey.Object,
			},
			ruleID,
			nil,
			1.0,
		)

		results = append(results, result)
	}

	return results, nil
}

// findProducingRule finds the rule that could have produced an edge based on head predicate.
func (a *SemiNaiveAdapter) findProducingRule(rules []InferenceRule, edge relations.EdgeKey) string {
	for _, rule := range rules {
		if rule.Enabled && matchesPredicate(rule.Head, edge.Predicate) {
			return rule.ID
		}
	}
	return ""
}

// matchesPredicate checks if a rule head matches a predicate.
func matchesPredicate(head RuleCondition, predicate string) bool {
	if IsVariable(head.Predicate) {
		return true
	}
	return head.Predicate == predicate
}

// convertToRelationsRule converts an inference.InferenceRule to a relations.InferenceRule.
func convertToRelationsRule(rule InferenceRule) *relations.InferenceRule {
	// Convert body conditions
	body := make([]relations.RuleCondition, len(rule.Body))
	bodyPredicates := make([]string, 0, len(rule.Body))
	hasNegation := false

	for i, cond := range rule.Body {
		body[i] = relations.RuleCondition{
			Subject:   cond.Subject,
			Predicate: cond.Predicate,
			Object:    cond.Object,
			Negated:   false, // inference.RuleCondition doesn't have Negated field
		}
		if !IsVariable(cond.Predicate) {
			bodyPredicates = append(bodyPredicates, cond.Predicate)
		}
	}

	return &relations.InferenceRule{
		ID:   rule.ID,
		Name: rule.Name,
		Head: relations.RuleCondition{
			Subject:   rule.Head.Subject,
			Predicate: rule.Head.Predicate,
			Object:    rule.Head.Object,
			Negated:   false,
		},
		Body:           body,
		Priority:       rule.Priority,
		Enabled:        rule.Enabled,
		Stratum:        0, // Will be computed by stratifier
		Negation:       hasNegation,
		HeadPredicate:  rule.Head.Predicate,
		BodyPredicates: bodyPredicates,
	}
}

// =============================================================================
// SemiNaiveForwardChainer - Alternative ForwardChainer using Semi-Naive Evaluation
// =============================================================================

// SemiNaiveForwardChainer wraps the SemiNaiveAdapter to implement a forward chaining
// interface compatible with the InferenceEngine. It provides an alternative to the
// standard ForwardChainer that uses semi-naive optimization for better performance
// on recursive rules.
type SemiNaiveForwardChainer struct {
	adapter       *SemiNaiveAdapter
	maxIterations int
	rules         []InferenceRule
}

// NewSemiNaiveForwardChainer creates a new semi-naive forward chainer with the given rules.
func NewSemiNaiveForwardChainer(rules []InferenceRule, maxIterations int) (*SemiNaiveForwardChainer, error) {
	adapter, err := NewSemiNaiveAdapter(rules)
	if err != nil {
		return nil, err
	}

	if maxIterations <= 0 {
		maxIterations = DefaultMaxIterations
	}
	adapter.SetMaxIterations(maxIterations)

	return &SemiNaiveForwardChainer{
		adapter:       adapter,
		maxIterations: maxIterations,
		rules:         rules,
	}, nil
}

// Evaluate runs semi-naive forward chaining on the given edges.
// It returns all derived edges as InferenceResults.
func (fc *SemiNaiveForwardChainer) Evaluate(ctx context.Context, rules []InferenceRule, edges []Edge) ([]InferenceResult, error) {
	// If rules have changed, create a new adapter
	if !rulesMatch(fc.rules, rules) {
		adapter, err := NewSemiNaiveAdapter(rules)
		if err != nil {
			return nil, err
		}
		adapter.SetMaxIterations(fc.maxIterations)
		fc.adapter = adapter
		fc.rules = rules
	}

	return fc.adapter.Evaluate(ctx, rules, edges)
}

// EvaluateIncremental performs incremental evaluation with new edges.
func (fc *SemiNaiveForwardChainer) EvaluateIncremental(ctx context.Context, rules []InferenceRule, existingEdges []Edge, newEdges []Edge) ([]InferenceResult, error) {
	if !rulesMatch(fc.rules, rules) {
		adapter, err := NewSemiNaiveAdapter(rules)
		if err != nil {
			return nil, err
		}
		adapter.SetMaxIterations(fc.maxIterations)
		fc.adapter = adapter
		fc.rules = rules
	}

	return fc.adapter.EvaluateIncremental(ctx, rules, existingEdges, newEdges)
}

// GetMaxIterations returns the maximum iterations limit.
func (fc *SemiNaiveForwardChainer) GetMaxIterations() int {
	return fc.maxIterations
}

// SetMaxIterations updates the maximum iterations limit.
func (fc *SemiNaiveForwardChainer) SetMaxIterations(maxIterations int) {
	if maxIterations > 0 {
		fc.maxIterations = maxIterations
		if fc.adapter != nil {
			fc.adapter.SetMaxIterations(maxIterations)
		}
	}
}

// rulesMatch checks if two rule slices represent the same set of rules.
func rulesMatch(a, b []InferenceRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

// =============================================================================
// Integration Helper Functions
// =============================================================================

// ConvertEdgesToDatabase converts inference.Edge slice to a relations.InMemoryDatabase.
func ConvertEdgesToDatabase(edges []Edge) *relations.InMemoryDatabase {
	db := relations.NewInMemoryDatabase()
	for _, edge := range edges {
		db.AddEdge(relations.EdgeKey{
			Subject:   edge.Source,
			Predicate: edge.Predicate,
			Object:    edge.Target,
		})
	}
	return db
}

// ConvertDatabaseToEdges converts a relations.InMemoryDatabase to inference.Edge slice.
func ConvertDatabaseToEdges(db *relations.InMemoryDatabase) []Edge {
	edges := make([]Edge, 0)
	for _, pred := range db.GetAllPredicates() {
		for _, edgeKey := range db.GetAllEdges(pred) {
			edges = append(edges, Edge{
				Source:    edgeKey.Subject,
				Predicate: edgeKey.Predicate,
				Target:    edgeKey.Object,
			})
		}
	}
	return edges
}

// ConvertDeltaToEdges converts a relations.DeltaTable to inference.Edge slice.
func ConvertDeltaToEdges(delta *relations.DeltaTable) []Edge {
	edgeKeys := delta.Edges()
	edges := make([]Edge, len(edgeKeys))
	for i, ek := range edgeKeys {
		edges[i] = Edge{
			Source:    ek.Subject,
			Predicate: ek.Predicate,
			Target:    ek.Object,
		}
	}
	return edges
}
