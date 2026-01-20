package inference

import (
	"context"
	"fmt"
)

// =============================================================================
// ChangeType Enum (IE.4.2)
// =============================================================================

// ChangeType represents the type of change that occurred to an edge.
type ChangeType int

const (
	// ChangeTypeAdded indicates an edge was added to the graph.
	ChangeTypeAdded ChangeType = iota
	// ChangeTypeRemoved indicates an edge was removed from the graph.
	ChangeTypeRemoved
	// ChangeTypeModified indicates an edge was modified in the graph.
	ChangeTypeModified
)

// String returns a human-readable representation of the change type.
func (c ChangeType) String() string {
	switch c {
	case ChangeTypeAdded:
		return "added"
	case ChangeTypeRemoved:
		return "removed"
	case ChangeTypeModified:
		return "modified"
	default:
		return "unknown"
	}
}

// =============================================================================
// InvalidationManager (IE.4.2)
// =============================================================================

// InvalidationManager handles invalidation of derived edges when base edges change.
// It cascades invalidations to dependent edges and triggers re-materialization.
type InvalidationManager struct {
	db            interface{} // Database connection (unused, kept for interface compatibility)
	materializer  *MaterializationManager
	forwardChainer *ForwardChainer
	ruleStore     *RuleStore
	edgeProvider  EdgeProvider
}

// EdgeProvider is an interface for retrieving edges from the graph.
// This allows the InvalidationManager to fetch edges for re-materialization.
type EdgeProvider interface {
	// GetAllEdges returns all edges in the graph.
	GetAllEdges(ctx context.Context) ([]Edge, error)
}

// NewInvalidationManager creates a new InvalidationManager.
func NewInvalidationManager(
	materializer *MaterializationManager,
	chainer *ForwardChainer,
	ruleStore *RuleStore,
	edgeProvider EdgeProvider,
) *InvalidationManager {
	return &InvalidationManager{
		materializer:   materializer,
		forwardChainer: chainer,
		ruleStore:      ruleStore,
		edgeProvider:   edgeProvider,
	}
}

// OnEdgeChange is called when an edge is added, removed, or modified.
// It cascades invalidation for dependent derived edges and triggers
// incremental re-materialization if needed.
func (im *InvalidationManager) OnEdgeChange(ctx context.Context, edge Edge, changeType ChangeType) error {
	edgeKey := edge.Key()

	switch changeType {
	case ChangeTypeAdded:
		return im.handleEdgeAdded(ctx, edge)
	case ChangeTypeRemoved:
		return im.handleEdgeRemoved(ctx, edgeKey)
	case ChangeTypeModified:
		return im.handleEdgeModified(ctx, edge, edgeKey)
	default:
		return fmt.Errorf("unknown change type: %v", changeType)
	}
}

// handleEdgeAdded processes a newly added edge.
// It triggers incremental inference to derive new edges.
func (im *InvalidationManager) handleEdgeAdded(ctx context.Context, edge Edge) error {
	if im.forwardChainer == nil || im.ruleStore == nil {
		return nil // No forward chainer or rule store configured
	}

	// Get all enabled rules
	rules, err := im.ruleStore.GetEnabledRules(ctx)
	if err != nil {
		return fmt.Errorf("get enabled rules: %w", err)
	}
	if len(rules) == 0 {
		return nil
	}

	// Get all current edges including the new one
	var edges []Edge
	if im.edgeProvider != nil {
		edges, err = im.edgeProvider.GetAllEdges(ctx)
		if err != nil {
			return fmt.Errorf("get all edges: %w", err)
		}
	} else {
		// If no edge provider, just use the new edge
		edges = []Edge{edge}
	}

	// Run forward chaining
	results, err := im.forwardChainer.Evaluate(ctx, rules, edges)
	if err != nil && err != ErrMaxIterationsReached {
		return fmt.Errorf("forward chaining: %w", err)
	}

	// Materialize new results
	if len(results) > 0 && im.materializer != nil {
		if err := im.materializer.Materialize(ctx, results); err != nil {
			return fmt.Errorf("materialize results: %w", err)
		}
	}

	return nil
}

// handleEdgeRemoved processes a removed edge.
// It invalidates all derived edges that depended on this edge.
func (im *InvalidationManager) handleEdgeRemoved(ctx context.Context, edgeKey string) error {
	// First invalidate all dependents
	if err := im.InvalidateDependents(ctx, edgeKey); err != nil {
		return fmt.Errorf("invalidate dependents: %w", err)
	}
	return nil
}

// handleEdgeModified processes a modified edge.
// It invalidates dependents and then triggers re-materialization.
func (im *InvalidationManager) handleEdgeModified(ctx context.Context, edge Edge, edgeKey string) error {
	// Get affected rule IDs before invalidation
	affectedRuleIDs, err := im.getAffectedRuleIDs(ctx, edgeKey)
	if err != nil {
		return fmt.Errorf("get affected rules: %w", err)
	}

	// Invalidate dependents
	if err := im.InvalidateDependents(ctx, edgeKey); err != nil {
		return fmt.Errorf("invalidate dependents: %w", err)
	}

	// Re-materialize affected rules
	if len(affectedRuleIDs) > 0 {
		if err := im.Rematerialize(ctx, affectedRuleIDs); err != nil {
			return fmt.Errorf("rematerialize: %w", err)
		}
	}

	return nil
}

// getAffectedRuleIDs returns the rule IDs that produced edges depending on the given edge.
func (im *InvalidationManager) getAffectedRuleIDs(ctx context.Context, edgeKey string) ([]string, error) {
	if im.materializer == nil {
		return nil, nil
	}

	records, err := im.materializer.GetMaterializedByEvidence(ctx, edgeKey)
	if err != nil {
		return nil, err
	}

	// Collect unique rule IDs
	ruleIDSet := make(map[string]bool)
	for _, record := range records {
		ruleIDSet[record.RuleID] = true
	}

	ruleIDs := make([]string, 0, len(ruleIDSet))
	for id := range ruleIDSet {
		ruleIDs = append(ruleIDs, id)
	}
	return ruleIDs, nil
}

// InvalidateDependents finds and removes derived edges that depended on the given edge.
// It initializes cycle detection and delegates to the recursive helper.
func (im *InvalidationManager) InvalidateDependents(ctx context.Context, edgeKey string) error {
	if im.materializer == nil {
		return nil
	}

	visited := make(map[string]bool)
	return im.invalidateDependentsRecursive(ctx, edgeKey, visited)
}

// invalidateDependentsRecursive recursively invalidates edges with cycle detection.
// It tracks visited edge keys to prevent infinite recursion on cyclic dependencies.
func (im *InvalidationManager) invalidateDependentsRecursive(
	ctx context.Context,
	edgeKey string,
	visited map[string]bool,
) error {
	// Cycle detection: skip if already visited
	if visited[edgeKey] {
		return nil
	}
	visited[edgeKey] = true

	// Find all materialized edges that used this edge as evidence
	dependents, err := im.materializer.GetMaterializedByEvidence(ctx, edgeKey)
	if err != nil {
		return fmt.Errorf("get dependents: %w", err)
	}

	// Delete each dependent and recursively invalidate its dependents
	for _, dep := range dependents {
		if err := im.materializer.DeleteMaterializedEdge(ctx, dep.EdgeKey); err != nil {
			return fmt.Errorf("delete dependent %s: %w", dep.EdgeKey, err)
		}

		// Recursively invalidate edges that depended on this one
		if err := im.invalidateDependentsRecursive(ctx, dep.EdgeKey, visited); err != nil {
			return fmt.Errorf("invalidate cascade for %s: %w", dep.EdgeKey, err)
		}
	}

	return nil
}

// Rematerialize re-runs inference for the specified rules.
// It fetches all current edges and runs forward chaining for affected rules only.
func (im *InvalidationManager) Rematerialize(ctx context.Context, affectedRuleIDs []string) error {
	if im.forwardChainer == nil || im.ruleStore == nil {
		return nil
	}
	if len(affectedRuleIDs) == 0 {
		return nil
	}

	// Create a set of affected rule IDs for quick lookup
	affectedSet := make(map[string]bool, len(affectedRuleIDs))
	for _, id := range affectedRuleIDs {
		affectedSet[id] = true
	}

	// Get the affected rules
	rules, err := im.getAffectedRules(ctx, affectedSet)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}

	// Get all current edges
	var edges []Edge
	if im.edgeProvider != nil {
		edges, err = im.edgeProvider.GetAllEdges(ctx)
		if err != nil {
			return fmt.Errorf("get all edges: %w", err)
		}
	}
	if len(edges) == 0 {
		return nil
	}

	// Run forward chaining with affected rules
	results, err := im.forwardChainer.Evaluate(ctx, rules, edges)
	if err != nil && err != ErrMaxIterationsReached {
		return fmt.Errorf("forward chaining: %w", err)
	}

	// Materialize results
	if len(results) > 0 && im.materializer != nil {
		if err := im.materializer.Materialize(ctx, results); err != nil {
			return fmt.Errorf("materialize results: %w", err)
		}
	}

	return nil
}

// getAffectedRules retrieves rules that are in the affected set.
func (im *InvalidationManager) getAffectedRules(ctx context.Context, affectedSet map[string]bool) ([]InferenceRule, error) {
	allRules, err := im.ruleStore.GetEnabledRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("get enabled rules: %w", err)
	}

	var rules []InferenceRule
	for _, rule := range allRules {
		if affectedSet[rule.ID] {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// InvalidateByRule invalidates all edges derived by a specific rule.
// This is called when a rule is updated or deleted.
func (im *InvalidationManager) InvalidateByRule(ctx context.Context, ruleID string) error {
	if im.materializer == nil {
		return nil
	}

	// Get all edges derived by this rule
	records, err := im.materializer.GetMaterializedEdges(ctx, ruleID)
	if err != nil {
		return fmt.Errorf("get materialized edges: %w", err)
	}

	// Cascade invalidation for each derived edge
	for _, record := range records {
		if err := im.InvalidateDependents(ctx, record.EdgeKey); err != nil {
			return fmt.Errorf("invalidate cascade for %s: %w", record.EdgeKey, err)
		}
	}

	// Delete all edges derived by this rule
	if err := im.materializer.DeleteMaterializedByRule(ctx, ruleID); err != nil {
		return fmt.Errorf("delete by rule: %w", err)
	}

	return nil
}
