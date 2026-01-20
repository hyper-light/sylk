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

// DefaultMaxRuleApplications is the default maximum number of total rule
// applications to prevent runaway inference chains.
const DefaultMaxRuleApplications = 10000

// ForwardChainer implements forward chaining inference.
// It repeatedly applies rules until no new edges can be derived (fixpoint)
// or the maximum iteration limit is reached.
//
// W4P.13: The ForwardChainer now maintains a cached edge index that is shared
// across all rule evaluations within an iteration, avoiding O(n) index rebuilds
// for each rule evaluation.
type ForwardChainer struct {
	evaluator              *RuleEvaluator
	maxIterations          int
	maxRuleApplications    int
	enableCycleDetection   bool
	ruleApplicationTracker *ruleApplicationTracker
	indexCache             *EdgeIndexCache // W4P.13: cached edge index
}

// ruleApplicationTracker tracks rule applications to detect cycles.
type ruleApplicationTracker struct {
	applications map[string]int // ruleID + edgeKey -> count
	totalCount   int
}

// newRuleApplicationTracker creates a new tracker.
func newRuleApplicationTracker() *ruleApplicationTracker {
	return &ruleApplicationTracker{
		applications: make(map[string]int),
		totalCount:   0,
	}
}

// track records a rule application and returns true if it's a repeat.
func (t *ruleApplicationTracker) track(ruleID, edgeKey string) bool {
	key := ruleID + ":" + edgeKey
	count := t.applications[key]
	t.applications[key] = count + 1
	t.totalCount++
	return count > 0
}

// count returns total rule applications.
func (t *ruleApplicationTracker) count() int {
	return t.totalCount
}

// NewForwardChainer creates a new ForwardChainer with the given evaluator
// and maximum iterations limit.
func NewForwardChainer(evaluator *RuleEvaluator, maxIterations int) *ForwardChainer {
	if maxIterations <= 0 {
		maxIterations = DefaultMaxIterations
	}
	return &ForwardChainer{
		evaluator:            evaluator,
		maxIterations:        maxIterations,
		maxRuleApplications:  DefaultMaxRuleApplications,
		enableCycleDetection: true,
		indexCache:           NewEdgeIndexCache(), // W4P.13: initialize cache
	}
}

// NewForwardChainerWithOptions creates a ForwardChainer with custom options.
func NewForwardChainerWithOptions(evaluator *RuleEvaluator, opts ForwardChainerOptions) *ForwardChainer {
	fc := NewForwardChainer(evaluator, opts.MaxIterations)
	if opts.MaxRuleApplications > 0 {
		fc.maxRuleApplications = opts.MaxRuleApplications
	}
	fc.enableCycleDetection = opts.EnableCycleDetection
	return fc
}

// ForwardChainerOptions configures the forward chainer behavior.
type ForwardChainerOptions struct {
	MaxIterations        int
	MaxRuleApplications  int
	EnableCycleDetection bool
}

// ErrMaxIterationsReached is returned when the forward chainer hits the iteration limit.
var ErrMaxIterationsReached = fmt.Errorf("forward chaining reached maximum iterations limit")

// ErrMaxRuleApplicationsReached is returned when rule application limit is exceeded.
var ErrMaxRuleApplicationsReached = fmt.Errorf("forward chaining reached maximum rule applications limit")

// ForwardChainError provides detailed error information for inference failures.
type ForwardChainError struct {
	Err           error
	Iteration     int
	DerivedCount  int
	RuleAppsCount int
}

// Error implements the error interface.
func (e *ForwardChainError) Error() string {
	return fmt.Sprintf("%v (iteration=%d, derived=%d, rule_apps=%d)",
		e.Err, e.Iteration, e.DerivedCount, e.RuleAppsCount)
}

// Unwrap returns the underlying error.
func (e *ForwardChainError) Unwrap() error {
	return e.Err
}

// Evaluate runs forward chaining on the given rules and edges.
// It iterates until either:
// - A fixpoint is reached (no new edges derived)
// - The maximum iteration limit is reached (returns error)
// - The maximum rule applications limit is reached (returns error)
// - The context is cancelled
//
// Returns all derived edges as InferenceResults.
func (fc *ForwardChainer) Evaluate(ctx context.Context, rules []InferenceRule, edges []Edge) ([]InferenceResult, error) {
	if fc.evaluator == nil {
		return nil, fmt.Errorf("forward chainer has no evaluator")
	}

	state := fc.initEvaluationState(edges)

	for state.iteration = 0; state.iteration < fc.maxIterations; state.iteration++ {
		if err := fc.checkContext(ctx); err != nil {
			return state.allResults, err
		}

		done, err := fc.runIteration(ctx, rules, state)
		if err != nil {
			return state.allResults, err
		}

		if done {
			return state.allResults, nil
		}
	}

	return state.allResults, fc.createLimitError(ErrMaxIterationsReached, state)
}

// evaluationState holds the state during forward chaining.
type evaluationState struct {
	derived      map[string]bool
	workingEdges []Edge
	allResults   []InferenceResult
	iteration    int
	tracker      *ruleApplicationTracker
}

// initEvaluationState initializes the evaluation state.
func (fc *ForwardChainer) initEvaluationState(edges []Edge) *evaluationState {
	derived := make(map[string]bool, len(edges))
	for _, e := range edges {
		derived[e.Key()] = true
	}

	workingEdges := make([]Edge, len(edges))
	copy(workingEdges, edges)

	var tracker *ruleApplicationTracker
	if fc.enableCycleDetection {
		tracker = newRuleApplicationTracker()
	}

	return &evaluationState{
		derived:      derived,
		workingEdges: workingEdges,
		allResults:   nil,
		iteration:    0,
		tracker:      tracker,
	}
}

// checkContext checks if context is cancelled.
func (fc *ForwardChainer) checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// runIteration executes a single iteration of forward chaining.
// W4P.13: Invalidates edge index cache when edges are added.
func (fc *ForwardChainer) runIteration(
	ctx context.Context,
	rules []InferenceRule,
	state *evaluationState,
) (bool, error) {
	newResults, done, err := fc.iterateOnceWithTracking(ctx, rules, state)
	if err != nil {
		return false, err
	}

	state.allResults = append(state.allResults, newResults...)

	// W4P.13: Invalidate cache before adding new edges if any were derived
	if len(newResults) > 0 && fc.indexCache != nil {
		fc.indexCache.Invalidate()
	}

	for _, result := range newResults {
		newEdge := EdgeFromDerivedEdge(result.DerivedEdge)
		state.workingEdges = append(state.workingEdges, newEdge)
	}

	return done, nil
}

// iterateOnceWithTracking performs iteration with rule application tracking.
func (fc *ForwardChainer) iterateOnceWithTracking(
	ctx context.Context,
	rules []InferenceRule,
	state *evaluationState,
) ([]InferenceResult, bool, error) {
	if state.tracker != nil && state.tracker.count() >= fc.maxRuleApplications {
		return nil, false, fc.createLimitError(ErrMaxRuleApplicationsReached, state)
	}

	return fc.IterateOnceWithTracker(ctx, rules, state.workingEdges, state.derived, state.tracker)
}

// createLimitError creates a detailed error for limit conditions.
func (fc *ForwardChainer) createLimitError(err error, state *evaluationState) error {
	ruleApps := 0
	if state.tracker != nil {
		ruleApps = state.tracker.count()
	}
	return &ForwardChainError{
		Err:           err,
		Iteration:     state.iteration,
		DerivedCount:  len(state.allResults),
		RuleAppsCount: ruleApps,
	}
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
	return fc.IterateOnceWithTracker(ctx, rules, edges, derived, nil)
}

// IterateOnceWithTracker performs a single iteration with optional rule tracking.
func (fc *ForwardChainer) IterateOnceWithTracker(
	ctx context.Context,
	rules []InferenceRule,
	edges []Edge,
	derived map[string]bool,
	tracker *ruleApplicationTracker,
) ([]InferenceResult, bool, error) {
	if fc.evaluator == nil {
		return nil, false, fmt.Errorf("forward chainer has no evaluator")
	}

	var newResults []InferenceResult

	for _, rule := range rules {
		if err := fc.checkContext(ctx); err != nil {
			return newResults, false, err
		}

		if !rule.Enabled {
			continue
		}

		ruleResults := fc.evaluateRuleWithTracking(ctx, rule, edges, derived, tracker)
		newResults = append(newResults, ruleResults...)
	}

	return newResults, len(newResults) == 0, nil
}

// evaluateRuleWithTracking evaluates a single rule and filters duplicates.
// W4P.13: Uses cached edge index to avoid rebuilding on each rule evaluation.
func (fc *ForwardChainer) evaluateRuleWithTracking(
	ctx context.Context,
	rule InferenceRule,
	edges []Edge,
	derived map[string]bool,
	tracker *ruleApplicationTracker,
) []InferenceResult {
	// W4P.13: Get or build the cached edge index
	var results []InferenceResult
	if fc.indexCache != nil {
		index := fc.indexCache.GetOrBuild(edges)
		results = fc.evaluator.EvaluateRuleWithIndex(ctx, rule, edges, index)
	} else {
		results = fc.evaluator.EvaluateRule(ctx, rule, edges)
	}

	var newResults []InferenceResult

	for _, result := range results {
		derivedEdge := EdgeFromDerivedEdge(result.DerivedEdge)
		key := derivedEdge.Key()

		if derived[key] {
			continue
		}

		// Track rule application if tracker is provided
		if tracker != nil {
			tracker.track(rule.ID, key)
		}

		derived[key] = true
		newResults = append(newResults, result)
	}

	return newResults
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

// GetMaxRuleApplications returns the maximum rule applications limit.
func (fc *ForwardChainer) GetMaxRuleApplications() int {
	return fc.maxRuleApplications
}

// SetMaxRuleApplications updates the maximum rule applications limit.
func (fc *ForwardChainer) SetMaxRuleApplications(max int) {
	if max > 0 {
		fc.maxRuleApplications = max
	}
}

// SetCycleDetection enables or disables cycle detection.
func (fc *ForwardChainer) SetCycleDetection(enabled bool) {
	fc.enableCycleDetection = enabled
}

// IsCycleDetectionEnabled returns whether cycle detection is enabled.
func (fc *ForwardChainer) IsCycleDetectionEnabled() bool {
	return fc.enableCycleDetection
}
