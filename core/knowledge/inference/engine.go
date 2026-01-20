package inference

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// =============================================================================
// InferenceStats (IE.5.1)
// =============================================================================

// InferenceStats provides statistics about the inference engine state.
type InferenceStats struct {
	TotalRules        int           `json:"total_rules"`
	EnabledRules      int           `json:"enabled_rules"`
	MaterializedEdges int           `json:"materialized_edges"`
	LastRunTime       time.Time     `json:"last_run_time"`
	LastRunDuration   time.Duration `json:"last_run_duration"`
}

// =============================================================================
// ExtendedEdgeProvider Interface (IE.5.1)
// =============================================================================

// ExtendedEdgeProvider extends EdgeProvider with node-specific edge retrieval.
// This interface is used by InferenceEngine to get edges for specific nodes.
type ExtendedEdgeProvider interface {
	EdgeProvider
	// GetEdgesByNode returns all edges connected to a specific node.
	GetEdgesByNode(ctx context.Context, nodeID string) ([]Edge, error)
}

// =============================================================================
// ForwardChainerInterface (SNE.12)
// =============================================================================

// ForwardChainerInterface defines the interface for forward chaining evaluation.
// This allows the InferenceEngine to use either the standard ForwardChainer
// or the SemiNaiveForwardChainer.
type ForwardChainerInterface interface {
	// Evaluate runs forward chaining on the given rules and edges.
	Evaluate(ctx context.Context, rules []InferenceRule, edges []Edge) ([]InferenceResult, error)
	// GetMaxIterations returns the maximum iterations limit.
	GetMaxIterations() int
	// SetMaxIterations updates the maximum iterations limit.
	SetMaxIterations(maxIterations int)
}

// =============================================================================
// InferenceEngine (IE.5.1)
// =============================================================================

// InferenceEngine coordinates inference operations by wiring together
// the RuleStore, ForwardChainer, MaterializationManager, and InvalidationManager.
// It provides methods for full inference passes, incremental inference,
// and rule management.
type InferenceEngine struct {
	ruleStore      *RuleStore
	forwardChainer *ForwardChainer
	semiNaiveFC    *SemiNaiveForwardChainer
	useSemiNaive   bool
	materializer   *MaterializationManager
	invalidator    *InvalidationManager
	edgeProvider   ExtendedEdgeProvider

	mu              sync.RWMutex
	lastRunTime     time.Time
	lastRunDuration time.Duration
}

// NewInferenceEngine creates a new InferenceEngine with all components wired together.
// The db parameter is used to create the RuleStore and MaterializationManager.
// An optional edgeProvider can be set after construction using SetEdgeProvider.
func NewInferenceEngine(db *sql.DB) *InferenceEngine {
	ruleStore := NewRuleStore(db)
	evaluator := NewRuleEvaluator()
	forwardChainer := NewForwardChainer(evaluator, DefaultMaxIterations)
	materializer := NewMaterializationManager(db)

	engine := &InferenceEngine{
		ruleStore:      ruleStore,
		forwardChainer: forwardChainer,
		materializer:   materializer,
	}

	// Create invalidation manager - edgeProvider will be nil until set
	engine.invalidator = NewInvalidationManager(
		materializer,
		forwardChainer,
		ruleStore,
		nil, // edgeProvider set separately
	)

	return engine
}

// SetEdgeProvider sets the edge provider for the engine and invalidation manager.
// This must be called before performing inference if edges need to be retrieved.
func (e *InferenceEngine) SetEdgeProvider(provider ExtendedEdgeProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.edgeProvider = provider

	// Update invalidation manager with the edge provider
	e.invalidator = NewInvalidationManager(
		e.materializer,
		e.forwardChainer,
		e.ruleStore,
		provider,
	)
}

// =============================================================================
// Semi-Naive Evaluation Configuration (SNE.12)
// =============================================================================

// UseSemiNaiveEvaluation enables or disables semi-naive evaluation.
// When enabled, the engine uses the SemiNaiveForwardChainer which is more
// efficient for recursive rules like transitive closure.
// The semi-naive evaluator must be initialized with the current rules first.
func (e *InferenceEngine) UseSemiNaiveEvaluation(ctx context.Context, enable bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if enable {
		// Get current rules to initialize the semi-naive evaluator
		rules, err := e.ruleStore.GetEnabledRules(ctx)
		if err != nil {
			return fmt.Errorf("get enabled rules: %w", err)
		}

		// Create semi-naive forward chainer
		semiNaiveFC, err := NewSemiNaiveForwardChainer(rules, e.forwardChainer.GetMaxIterations())
		if err != nil {
			return fmt.Errorf("create semi-naive forward chainer: %w", err)
		}

		e.semiNaiveFC = semiNaiveFC
		e.useSemiNaive = true
	} else {
		e.useSemiNaive = false
		e.semiNaiveFC = nil
	}

	return nil
}

// IsSemiNaiveEnabled returns whether semi-naive evaluation is enabled.
func (e *InferenceEngine) IsSemiNaiveEnabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.useSemiNaive
}

// getActiveForwardChainer returns the currently active forward chainer.
// If semi-naive is enabled and initialized, it returns the SemiNaiveForwardChainer.
// Otherwise, it returns the standard ForwardChainer.
func (e *InferenceEngine) getActiveForwardChainer() ForwardChainerInterface {
	if e.useSemiNaive && e.semiNaiveFC != nil {
		return e.semiNaiveFC
	}
	return e.forwardChainer
}

// =============================================================================
// Full Inference
// =============================================================================

// RunInference performs a full forward chaining pass over all enabled rules.
// It retrieves all edges from the edge provider, evaluates all enabled rules,
// and materializes all derived edges.
func (e *InferenceEngine) RunInference(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	startTime := time.Now()
	defer func() {
		e.lastRunTime = startTime
		e.lastRunDuration = time.Since(startTime)
	}()

	// Get all enabled rules
	rules, err := e.ruleStore.GetEnabledRules(ctx)
	if err != nil {
		return fmt.Errorf("get enabled rules: %w", err)
	}
	if len(rules) == 0 {
		return nil // No rules to evaluate
	}

	// Get all edges
	var edges []Edge
	if e.edgeProvider != nil {
		edges, err = e.edgeProvider.GetAllEdges(ctx)
		if err != nil {
			return fmt.Errorf("get all edges: %w", err)
		}
	}
	if len(edges) == 0 {
		return nil // No edges to process
	}

	// Run forward chaining using the active forward chainer (standard or semi-naive)
	fc := e.getActiveForwardChainer()
	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil && !errors.Is(err, ErrMaxIterationsReached) {
		return fmt.Errorf("forward chaining: %w", err)
	}

	// Materialize results
	if len(results) > 0 {
		if err := e.materializer.Materialize(ctx, results); err != nil {
			return fmt.Errorf("materialize results: %w", err)
		}
	}

	return nil
}

// =============================================================================
// Incremental Inference
// =============================================================================

// OnEdgeAdded performs incremental inference when a new edge is added.
// It only evaluates rules whose body might match the new edge.
func (e *InferenceEngine) OnEdgeAdded(ctx context.Context, edge Edge) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Get all enabled rules
	rules, err := e.ruleStore.GetEnabledRules(ctx)
	if err != nil {
		return fmt.Errorf("get enabled rules: %w", err)
	}
	if len(rules) == 0 {
		return nil
	}

	// Filter rules whose body might match this edge
	relevantRules := e.filterRulesForEdge(rules, edge)
	if len(relevantRules) == 0 {
		return nil
	}

	// Get all current edges including the new one
	var edges []Edge
	if e.edgeProvider != nil {
		edges, err = e.edgeProvider.GetAllEdges(ctx)
		if err != nil {
			return fmt.Errorf("get all edges: %w", err)
		}
	} else {
		edges = []Edge{edge}
	}

	// Run forward chaining with relevant rules only using the active forward chainer
	fc := e.getActiveForwardChainer()
	results, err := fc.Evaluate(ctx, relevantRules, edges)
	if err != nil && !errors.Is(err, ErrMaxIterationsReached) {
		return fmt.Errorf("forward chaining: %w", err)
	}

	// Materialize new results
	if len(results) > 0 {
		if err := e.materializer.Materialize(ctx, results); err != nil {
			return fmt.Errorf("materialize results: %w", err)
		}
	}

	return nil
}

// filterRulesForEdge returns rules whose body conditions might match the given edge.
func (e *InferenceEngine) filterRulesForEdge(rules []InferenceRule, edge Edge) []InferenceRule {
	var relevant []InferenceRule
	for _, rule := range rules {
		if e.ruleMatchesEdge(rule, edge) {
			relevant = append(relevant, rule)
		}
	}
	return relevant
}

// ruleMatchesEdge checks if any body condition in the rule might match the edge.
func (e *InferenceEngine) ruleMatchesEdge(rule InferenceRule, edge Edge) bool {
	for _, condition := range rule.Body {
		if e.conditionMightMatch(condition, edge) {
			return true
		}
	}
	return false
}

// conditionMightMatch checks if a condition could potentially match the edge.
// Variables always potentially match, constants must equal.
func (e *InferenceEngine) conditionMightMatch(condition RuleCondition, edge Edge) bool {
	// Check subject
	if !IsVariable(condition.Subject) && condition.Subject != edge.Source {
		return false
	}
	// Check predicate
	if !IsVariable(condition.Predicate) && condition.Predicate != edge.Predicate {
		return false
	}
	// Check object
	if !IsVariable(condition.Object) && condition.Object != edge.Target {
		return false
	}
	return true
}

// OnEdgeRemoved performs incremental invalidation when an edge is removed.
// It removes all derived edges that depended on the removed edge.
func (e *InferenceEngine) OnEdgeRemoved(ctx context.Context, edge Edge) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	edgeKey := edge.Key()

	// Invalidate all derived edges that used this edge as evidence
	if err := e.invalidator.InvalidateDependents(ctx, edgeKey); err != nil {
		return fmt.Errorf("invalidate dependents: %w", err)
	}

	return nil
}

// OnEdgeModified handles edge modification as a remove followed by an add.
// It invalidates edges that depended on the old edge and then derives new edges.
func (e *InferenceEngine) OnEdgeModified(ctx context.Context, oldEdge, newEdge Edge) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	oldEdgeKey := oldEdge.Key()

	// Get affected rule IDs before invalidation
	affectedRuleIDs, err := e.getAffectedRuleIDs(ctx, oldEdgeKey)
	if err != nil {
		return fmt.Errorf("get affected rules: %w", err)
	}

	// Invalidate edges that depended on the old edge
	if err := e.invalidator.InvalidateDependents(ctx, oldEdgeKey); err != nil {
		return fmt.Errorf("invalidate dependents: %w", err)
	}

	// Re-derive edges with affected rules and the new edge
	if len(affectedRuleIDs) > 0 {
		if err := e.rematerializeWithRules(ctx, affectedRuleIDs); err != nil {
			return fmt.Errorf("rematerialize: %w", err)
		}
	}

	// Also check if the new edge triggers new inferences
	rules, err := e.ruleStore.GetEnabledRules(ctx)
	if err != nil {
		return fmt.Errorf("get enabled rules: %w", err)
	}

	relevantRules := e.filterRulesForEdge(rules, newEdge)
	if len(relevantRules) > 0 {
		var edges []Edge
		if e.edgeProvider != nil {
			edges, err = e.edgeProvider.GetAllEdges(ctx)
			if err != nil {
				return fmt.Errorf("get all edges: %w", err)
			}
		} else {
			edges = []Edge{newEdge}
		}

		fc := e.getActiveForwardChainer()
		results, err := fc.Evaluate(ctx, relevantRules, edges)
		if err != nil && !errors.Is(err, ErrMaxIterationsReached) {
			return fmt.Errorf("forward chaining: %w", err)
		}

		if len(results) > 0 {
			if err := e.materializer.Materialize(ctx, results); err != nil {
				return fmt.Errorf("materialize results: %w", err)
			}
		}
	}

	return nil
}

// getAffectedRuleIDs returns the rule IDs that produced edges depending on the given edge.
func (e *InferenceEngine) getAffectedRuleIDs(ctx context.Context, edgeKey string) ([]string, error) {
	records, err := e.materializer.GetMaterializedByEvidence(ctx, edgeKey)
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

// rematerializeWithRules re-runs inference for the specified rules.
func (e *InferenceEngine) rematerializeWithRules(ctx context.Context, ruleIDs []string) error {
	if len(ruleIDs) == 0 {
		return nil
	}

	// Create a set of affected rule IDs for quick lookup
	affectedSet := make(map[string]bool, len(ruleIDs))
	for _, id := range ruleIDs {
		affectedSet[id] = true
	}

	// Get the affected rules
	allRules, err := e.ruleStore.GetEnabledRules(ctx)
	if err != nil {
		return fmt.Errorf("get enabled rules: %w", err)
	}

	var rules []InferenceRule
	for _, rule := range allRules {
		if affectedSet[rule.ID] {
			rules = append(rules, rule)
		}
	}
	if len(rules) == 0 {
		return nil
	}

	// Get all current edges
	var edges []Edge
	if e.edgeProvider != nil {
		edges, err = e.edgeProvider.GetAllEdges(ctx)
		if err != nil {
			return fmt.Errorf("get all edges: %w", err)
		}
	}
	if len(edges) == 0 {
		return nil
	}

	// Run forward chaining with affected rules using the active forward chainer
	fc := e.getActiveForwardChainer()
	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil && !errors.Is(err, ErrMaxIterationsReached) {
		return fmt.Errorf("forward chaining: %w", err)
	}

	// Materialize results
	if len(results) > 0 {
		if err := e.materializer.Materialize(ctx, results); err != nil {
			return fmt.Errorf("materialize results: %w", err)
		}
	}

	return nil
}

// =============================================================================
// Rule Management
// =============================================================================

// AddRule adds a new inference rule to the store.
func (e *InferenceEngine) AddRule(ctx context.Context, rule InferenceRule) error {
	if err := rule.Validate(); err != nil {
		return fmt.Errorf("validate rule: %w", err)
	}
	return e.ruleStore.SaveRule(ctx, rule)
}

// RemoveRule removes an inference rule and invalidates all edges derived by it.
func (e *InferenceEngine) RemoveRule(ctx context.Context, ruleID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Invalidate all edges derived by this rule
	if err := e.invalidator.InvalidateByRule(ctx, ruleID); err != nil {
		return fmt.Errorf("invalidate by rule: %w", err)
	}

	// Delete the rule
	if err := e.ruleStore.DeleteRule(ctx, ruleID); err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}

	return nil
}

// EnableRule enables or disables an inference rule.
// When a rule is disabled, edges it derived remain but no new edges will be derived.
// When a rule is re-enabled, incremental inference may derive new edges.
func (e *InferenceEngine) EnableRule(ctx context.Context, ruleID string, enabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	rule, err := e.ruleStore.GetRule(ctx, ruleID)
	if err != nil {
		return fmt.Errorf("get rule: %w", err)
	}
	if rule == nil {
		return fmt.Errorf("rule not found: %s", ruleID)
	}

	rule.Enabled = enabled

	if err := e.ruleStore.SaveRule(ctx, *rule); err != nil {
		return fmt.Errorf("save rule: %w", err)
	}

	return nil
}

// GetRules returns all inference rules.
func (e *InferenceEngine) GetRules(ctx context.Context) ([]InferenceRule, error) {
	return e.ruleStore.LoadRules(ctx)
}

// =============================================================================
// Query Methods
// =============================================================================

// GetDerivedEdges returns all edges derived by inference for a specific node.
// It looks for materialized edges where the node is either the source or target.
func (e *InferenceEngine) GetDerivedEdges(ctx context.Context, nodeID string) ([]Edge, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Get all materialized edges
	records, err := e.materializer.GetAllMaterializedEdges(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all materialized edges: %w", err)
	}

	// Filter edges connected to the node
	var edges []Edge
	for _, record := range records {
		edge := parseEdgeKey(record.EdgeKey)
		if edge.Source == nodeID || edge.Target == nodeID {
			edges = append(edges, edge)
		}
	}

	return edges, nil
}

// parseEdgeKey converts an edge key (source|predicate|target) back to an Edge.
func parseEdgeKey(key string) Edge {
	// Split by pipe character
	parts := splitEdgeKey(key)
	if len(parts) != 3 {
		return Edge{}
	}
	return Edge{
		Source:    parts[0],
		Predicate: parts[1],
		Target:    parts[2],
	}
}

// splitEdgeKey splits an edge key by the pipe delimiter.
func splitEdgeKey(key string) []string {
	var parts []string
	var current string
	for _, c := range key {
		if c == '|' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	parts = append(parts, current)
	return parts
}

// GetProvenance returns the inference result that derived a specific edge.
// Returns nil if the edge was not derived by inference.
func (e *InferenceEngine) GetProvenance(ctx context.Context, edgeKey string) (*InferenceResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Get all materialized edges and find the one with matching key
	records, err := e.materializer.GetAllMaterializedEdges(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all materialized edges: %w", err)
	}

	for _, record := range records {
		if record.EdgeKey == edgeKey {
			// Get the rule to include its name in provenance
			rule, err := e.ruleStore.GetRule(ctx, record.RuleID)
			if err != nil {
				return nil, fmt.Errorf("get rule: %w", err)
			}

			// Convert record to InferenceResult
			edge := parseEdgeKey(record.EdgeKey)
			evidence := make([]EvidenceEdge, len(record.Evidence))
			for i, evKey := range record.Evidence {
				evEdge := parseEdgeKey(evKey)
				evidence[i] = EvidenceEdge{
					SourceID: evEdge.Source,
					EdgeType: evEdge.Predicate,
					TargetID: evEdge.Target,
				}
			}

			result := &InferenceResult{
				DerivedEdge: DerivedEdge{
					SourceID: edge.Source,
					EdgeType: edge.Predicate,
					TargetID: edge.Target,
				},
				RuleID:     record.RuleID,
				Evidence:   evidence,
				DerivedAt:  record.DerivedAt,
				Confidence: 1.0,
			}

			if rule != nil {
				result.GenerateProvenance(rule.Name)
			}

			return result, nil
		}
	}

	return nil, nil // Edge not found
}

// Stats returns statistics about the inference engine.
func (e *InferenceEngine) Stats() *InferenceStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ctx := context.Background()

	stats := &InferenceStats{
		LastRunTime:     e.lastRunTime,
		LastRunDuration: e.lastRunDuration,
	}

	// Count total rules
	rules, err := e.ruleStore.LoadRules(ctx)
	if err == nil {
		stats.TotalRules = len(rules)
		for _, rule := range rules {
			if rule.Enabled {
				stats.EnabledRules++
			}
		}
	}

	// Count materialized edges
	edges, err := e.materializer.GetAllMaterializedEdges(ctx)
	if err == nil {
		stats.MaterializedEdges = len(edges)
	}

	return stats
}

// =============================================================================
// Component Accessors
// =============================================================================

// RuleStore returns the underlying rule store.
func (e *InferenceEngine) RuleStore() *RuleStore {
	return e.ruleStore
}

// ForwardChainer returns the underlying forward chainer.
func (e *InferenceEngine) ForwardChainer() *ForwardChainer {
	return e.forwardChainer
}

// Materializer returns the underlying materialization manager.
func (e *InferenceEngine) Materializer() *MaterializationManager {
	return e.materializer
}

// Invalidator returns the underlying invalidation manager.
func (e *InferenceEngine) Invalidator() *InvalidationManager {
	return e.invalidator
}
