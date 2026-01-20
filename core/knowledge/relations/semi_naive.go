package relations

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// =============================================================================
// Error Definitions
// =============================================================================

// ErrNotStratified indicates that the evaluator has no stratifier configured,
// which means rules have not been properly stratified for evaluation.
var ErrNotStratified = errors.New("evaluator has no stratifier configured")

// =============================================================================
// EdgeKey Type - Efficient edge tracking for delta tables
// =============================================================================

// EdgeKey represents a unique edge in a knowledge graph, used for efficient
// membership tracking in delta tables during semi-naive evaluation.
// The struct is designed to be used as a map key (hashable by Go runtime).
type EdgeKey struct {
	Subject   string // The source entity of the edge
	Predicate string // The relationship type
	Object    string // The target entity of the edge
}

// String returns a human-readable string representation of the edge for debugging.
func (e EdgeKey) String() string {
	return fmt.Sprintf("(%s)-[%s]->(%s)", e.Subject, e.Predicate, e.Object)
}

// =============================================================================
// DeltaTable - Tracks new inferences during semi-naive evaluation
// =============================================================================

// DeltaTable tracks new edges discovered during a single iteration of
// semi-naive evaluation. It uses a map for O(1) membership checking.
//
// In semi-naive evaluation, new edges are computed as:
//   (Δ × All) ∪ (All × Δ)
// rather than the naive:
//   (All × All)
//
// This reduces redundant computation by only considering edges that involve
// at least one newly derived fact.
type DeltaTable struct {
	edges map[EdgeKey]struct{}
}

// NewDeltaTable creates a new empty DeltaTable.
func NewDeltaTable() *DeltaTable {
	return &DeltaTable{
		edges: make(map[EdgeKey]struct{}),
	}
}

// Add adds an edge to the delta table.
func (d *DeltaTable) Add(edge EdgeKey) {
	d.edges[edge] = struct{}{}
}

// Contains checks if an edge exists in the delta table with O(1) complexity.
func (d *DeltaTable) Contains(edge EdgeKey) bool {
	_, exists := d.edges[edge]
	return exists
}

// Merge combines another delta table into this one.
// All edges from the other table are added to this table.
func (d *DeltaTable) Merge(other *DeltaTable) {
	if other == nil {
		return
	}
	for edge := range other.edges {
		d.edges[edge] = struct{}{}
	}
}

// Clear removes all edges from the delta table, preparing it for the next iteration.
func (d *DeltaTable) Clear() {
	d.edges = make(map[EdgeKey]struct{})
}

// Size returns the number of edges in the delta table.
func (d *DeltaTable) Size() int {
	return len(d.edges)
}

// Edges returns a slice of all edges in the delta table.
// The order of edges is not guaranteed.
func (d *DeltaTable) Edges() []EdgeKey {
	result := make([]EdgeKey, 0, len(d.edges))
	for edge := range d.edges {
		result = append(result, edge)
	}
	return result
}

// IsEmpty returns true if the delta table contains no edges.
// This is used to detect fixpoint (iteration termination) in semi-naive evaluation.
func (d *DeltaTable) IsEmpty() bool {
	return len(d.edges) == 0
}

// =============================================================================
// ConcurrentDeltaTable - Thread-safe variant for parallel evaluation
// =============================================================================

// ConcurrentDeltaTable is a thread-safe variant of DeltaTable that uses
// a RWMutex to allow concurrent reads and exclusive writes.
// This is useful when multiple goroutines are computing new inferences
// in parallel during semi-naive evaluation.
type ConcurrentDeltaTable struct {
	mu    sync.RWMutex
	edges map[EdgeKey]struct{}
}

// NewConcurrentDeltaTable creates a new empty ConcurrentDeltaTable.
func NewConcurrentDeltaTable() *ConcurrentDeltaTable {
	return &ConcurrentDeltaTable{
		edges: make(map[EdgeKey]struct{}),
	}
}

// Add adds an edge to the delta table (thread-safe).
func (d *ConcurrentDeltaTable) Add(edge EdgeKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.edges[edge] = struct{}{}
}

// Contains checks if an edge exists in the delta table with O(1) complexity (thread-safe).
func (d *ConcurrentDeltaTable) Contains(edge EdgeKey) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, exists := d.edges[edge]
	return exists
}

// Merge combines another delta table into this one (thread-safe).
// All edges from the other table are added to this table.
func (d *ConcurrentDeltaTable) Merge(other *ConcurrentDeltaTable) {
	if other == nil {
		return
	}

	// Lock other for reading first, then this for writing
	other.mu.RLock()
	defer other.mu.RUnlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	for edge := range other.edges {
		d.edges[edge] = struct{}{}
	}
}

// MergeFromDeltaTable merges a non-concurrent DeltaTable into this ConcurrentDeltaTable.
func (d *ConcurrentDeltaTable) MergeFromDeltaTable(other *DeltaTable) {
	if other == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for edge := range other.edges {
		d.edges[edge] = struct{}{}
	}
}

// Clear removes all edges from the delta table (thread-safe).
func (d *ConcurrentDeltaTable) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.edges = make(map[EdgeKey]struct{})
}

// Size returns the number of edges in the delta table (thread-safe).
func (d *ConcurrentDeltaTable) Size() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.edges)
}

// Edges returns a slice of all edges in the delta table (thread-safe).
// The order of edges is not guaranteed.
func (d *ConcurrentDeltaTable) Edges() []EdgeKey {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]EdgeKey, 0, len(d.edges))
	for edge := range d.edges {
		result = append(result, edge)
	}
	return result
}

// IsEmpty returns true if the delta table contains no edges (thread-safe).
func (d *ConcurrentDeltaTable) IsEmpty() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.edges) == 0
}

// ToDeltaTable creates a non-concurrent copy of this ConcurrentDeltaTable.
// This is useful when iteration is complete and concurrent access is no longer needed.
func (d *ConcurrentDeltaTable) ToDeltaTable() *DeltaTable {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := NewDeltaTable()
	for edge := range d.edges {
		result.edges[edge] = struct{}{}
	}
	return result
}

// =============================================================================
// Database Interface (SNE.4) - Semi-Naive Evaluation Database
// =============================================================================

// Database defines the interface for accessing and modifying edges during
// semi-naive evaluation. It provides predicate-based access to edges,
// which is essential for efficient rule evaluation.
type Database interface {
	// GetAllEdges retrieves all edges for a specific predicate.
	// Returns an empty slice if the predicate has no edges.
	GetAllEdges(predicate string) []EdgeKey

	// AddEdge adds an edge to the database.
	// Returns true if the edge is new (was not already present),
	// false if it already existed.
	AddEdge(edge EdgeKey) bool

	// ContainsEdge checks if an edge exists in the database.
	// This is O(1) for predicate lookup + O(1) for edge lookup.
	ContainsEdge(edge EdgeKey) bool

	// PredicateCount returns the number of distinct predicates in the database.
	PredicateCount() int

	// EdgeCount returns the total number of edges in the database.
	EdgeCount() int
}

// =============================================================================
// InMemoryDatabase - Thread-Safe Database Implementation
// =============================================================================

// InMemoryDatabase implements the Database interface using in-memory maps.
// It is thread-safe for concurrent access using RWMutex.
//
// Data structure: map[predicate]map[EdgeKey]struct{}
// This allows efficient retrieval of all edges for a given predicate,
// which is critical for semi-naive evaluation where rules often
// select edges by predicate type.
type InMemoryDatabase struct {
	mu    sync.RWMutex
	edges map[string]map[EdgeKey]struct{} // predicate -> edges
	count int                              // total edge count
}

// NewInMemoryDatabase creates a new empty InMemoryDatabase.
func NewInMemoryDatabase() *InMemoryDatabase {
	return &InMemoryDatabase{
		edges: make(map[string]map[EdgeKey]struct{}),
		count: 0,
	}
}

// GetAllEdges retrieves all edges for a specific predicate.
// Returns an empty slice if the predicate has no edges.
// Thread-safe for concurrent reads.
func (db *InMemoryDatabase) GetAllEdges(predicate string) []EdgeKey {
	db.mu.RLock()
	defer db.mu.RUnlock()

	predicateEdges, exists := db.edges[predicate]
	if !exists {
		return []EdgeKey{}
	}

	result := make([]EdgeKey, 0, len(predicateEdges))
	for edge := range predicateEdges {
		result = append(result, edge)
	}
	return result
}

// AddEdge adds an edge to the database.
// Returns true if the edge is new (was not already present),
// false if it already existed.
// Thread-safe for concurrent writes.
func (db *InMemoryDatabase) AddEdge(edge EdgeKey) bool {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Get or create the predicate map
	predicateEdges, exists := db.edges[edge.Predicate]
	if !exists {
		predicateEdges = make(map[EdgeKey]struct{})
		db.edges[edge.Predicate] = predicateEdges
	}

	// Check if edge already exists
	if _, edgeExists := predicateEdges[edge]; edgeExists {
		return false
	}

	// Add the edge
	predicateEdges[edge] = struct{}{}
	db.count++
	return true
}

// ContainsEdge checks if an edge exists in the database.
// Thread-safe for concurrent reads.
func (db *InMemoryDatabase) ContainsEdge(edge EdgeKey) bool {
	db.mu.RLock()
	defer db.mu.RUnlock()

	predicateEdges, exists := db.edges[edge.Predicate]
	if !exists {
		return false
	}

	_, edgeExists := predicateEdges[edge]
	return edgeExists
}

// PredicateCount returns the number of distinct predicates in the database.
// Thread-safe for concurrent reads.
func (db *InMemoryDatabase) PredicateCount() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.edges)
}

// EdgeCount returns the total number of edges in the database.
// Thread-safe for concurrent reads.
func (db *InMemoryDatabase) EdgeCount() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.count
}

// GetAllPredicates returns all distinct predicates in the database.
// Thread-safe for concurrent reads.
func (db *InMemoryDatabase) GetAllPredicates() []string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	predicates := make([]string, 0, len(db.edges))
	for pred := range db.edges {
		predicates = append(predicates, pred)
	}
	return predicates
}

// RemoveEdge removes an edge from the database.
// Returns true if the edge was present and removed,
// false if it did not exist.
// Thread-safe for concurrent writes.
func (db *InMemoryDatabase) RemoveEdge(edge EdgeKey) bool {
	db.mu.Lock()
	defer db.mu.Unlock()

	predicateEdges, exists := db.edges[edge.Predicate]
	if !exists {
		return false
	}

	if _, edgeExists := predicateEdges[edge]; !edgeExists {
		return false
	}

	delete(predicateEdges, edge)
	db.count--

	// Clean up empty predicate map
	if len(predicateEdges) == 0 {
		delete(db.edges, edge.Predicate)
	}

	return true
}

// Clear removes all edges from the database.
// Thread-safe for concurrent writes.
func (db *InMemoryDatabase) Clear() {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.edges = make(map[string]map[EdgeKey]struct{})
	db.count = 0
}

// Clone creates a deep copy of the database.
// Useful for snapshotting state before evaluation.
// Thread-safe for concurrent reads.
func (db *InMemoryDatabase) Clone() *InMemoryDatabase {
	db.mu.RLock()
	defer db.mu.RUnlock()

	clone := NewInMemoryDatabase()
	for pred, predEdges := range db.edges {
		clone.edges[pred] = make(map[EdgeKey]struct{}, len(predEdges))
		for edge := range predEdges {
			clone.edges[pred][edge] = struct{}{}
		}
	}
	clone.count = db.count

	return clone
}

// AddEdges adds multiple edges to the database.
// Returns the number of new edges that were added.
// Thread-safe for concurrent writes.
func (db *InMemoryDatabase) AddEdges(edges []EdgeKey) int {
	db.mu.Lock()
	defer db.mu.Unlock()

	added := 0
	for _, edge := range edges {
		// Get or create the predicate map
		predicateEdges, exists := db.edges[edge.Predicate]
		if !exists {
			predicateEdges = make(map[EdgeKey]struct{})
			db.edges[edge.Predicate] = predicateEdges
		}

		// Check if edge already exists
		if _, edgeExists := predicateEdges[edge]; !edgeExists {
			predicateEdges[edge] = struct{}{}
			db.count++
			added++
		}
	}
	return added
}

// MergeFrom merges all edges from another database into this one.
// Returns the number of new edges that were added.
// Thread-safe for concurrent access.
func (db *InMemoryDatabase) MergeFrom(other *InMemoryDatabase) int {
	if other == nil {
		return 0
	}

	// Lock other for reading first
	other.mu.RLock()
	defer other.mu.RUnlock()

	db.mu.Lock()
	defer db.mu.Unlock()

	added := 0
	for pred, predEdges := range other.edges {
		// Get or create the predicate map
		localPredEdges, exists := db.edges[pred]
		if !exists {
			localPredEdges = make(map[EdgeKey]struct{})
			db.edges[pred] = localPredEdges
		}

		for edge := range predEdges {
			if _, edgeExists := localPredEdges[edge]; !edgeExists {
				localPredEdges[edge] = struct{}{}
				db.count++
				added++
			}
		}
	}

	return added
}

// =============================================================================
// Stratification Algorithm (SNE.5) - Rule Stratification for Semi-Naive Evaluation
// =============================================================================

// StratificationError indicates rules cannot be stratified due to circular negative dependency.
// In Datalog semantics, a circular negative dependency means the rules have no valid
// stratification and cannot be evaluated using standard semi-naive evaluation.
type StratificationError struct {
	Rules   []*InferenceRule // Rules involved in the cycle
	Message string
}

// Error implements the error interface for StratificationError.
func (e *StratificationError) Error() string {
	return e.Message
}

// Stratifier computes valid stratification of inference rules.
// Stratification is the process of assigning rules to layers (strata) such that:
// 1. Rules in the same stratum can be evaluated in parallel
// 2. Rules with negative dependencies on predicates must be in a higher stratum
//    than the rules that produce those predicates
// 3. Circular negative dependencies are detected and reported as errors
type Stratifier struct {
	rules    []*InferenceRule
	ruleByID map[string]*InferenceRule
}

// NewStratifier creates a stratifier for the given rules.
func NewStratifier(rules []*InferenceRule) *Stratifier {
	ruleByID := make(map[string]*InferenceRule, len(rules))
	for _, r := range rules {
		ruleByID[r.ID] = r
	}
	return &Stratifier{
		rules:    rules,
		ruleByID: ruleByID,
	}
}

// Stratify computes stratum assignments for all rules.
// Returns error if a negative cycle is detected (rules cannot be stratified).
//
// Algorithm:
// 1. Build dependency graph (rule -> rules it depends on)
// 2. Identify negative dependencies (affects stratum ordering)
// 3. Check for negative cycles using DFS
// 4. Assign strata: rules with no negation deps = stratum 0
//    Rules with negation dep on stratum N = stratum N+1
func (s *Stratifier) Stratify() error {
	if len(s.rules) == 0 {
		return nil
	}

	// Build the negative dependency graph
	negDeps := s.buildNegativeDependencyGraph()

	// Check for negative cycles
	if cycle := s.findNegativeCycle(negDeps); cycle != nil {
		return &StratificationError{
			Rules:   cycle,
			Message: "circular negative dependency detected - rules cannot be stratified",
		}
	}

	// Assign strata via topological sort on negative dependencies
	s.assignStrata(negDeps)
	return nil
}

// buildDependencyGraph returns rule ID -> [rules it depends on].
// A rule depends on another if it references that rule's head predicate in its body.
func (s *Stratifier) buildDependencyGraph() map[string][]*InferenceRule {
	// Build a map of predicate -> rules that produce it
	predicateProducers := make(map[string][]*InferenceRule)
	for _, r := range s.rules {
		predicateProducers[r.HeadPredicate] = append(predicateProducers[r.HeadPredicate], r)
	}

	// Build dependency graph
	deps := make(map[string][]*InferenceRule)
	for _, r := range s.rules {
		for _, bodyPred := range r.BodyPredicates {
			if producers, exists := predicateProducers[bodyPred]; exists {
				deps[r.ID] = append(deps[r.ID], producers...)
			}
		}
	}

	return deps
}

// buildNegativeDependencyGraph returns rule ID -> [rules it negatively depends on].
// A rule negatively depends on another if it has a negated body condition that
// references that rule's head predicate.
func (s *Stratifier) buildNegativeDependencyGraph() map[string][]*InferenceRule {
	// Build a map of predicate -> rules that produce it
	predicateProducers := make(map[string][]*InferenceRule)
	for _, r := range s.rules {
		predicateProducers[r.HeadPredicate] = append(predicateProducers[r.HeadPredicate], r)
	}

	// Build negative dependency graph
	negDeps := make(map[string][]*InferenceRule)
	for _, r := range s.rules {
		if !r.Negation {
			continue
		}
		// Check each body condition for negation
		for _, cond := range r.Body {
			if cond.Negated {
				if producers, exists := predicateProducers[cond.Predicate]; exists {
					negDeps[r.ID] = append(negDeps[r.ID], producers...)
				}
			}
		}
	}

	return negDeps
}

// findNegativeCycle detects cycles in the negative dependency graph.
// Returns the rules involved in the cycle if found, nil otherwise.
// Uses DFS with coloring: white (unvisited), gray (in progress), black (done).
func (s *Stratifier) findNegativeCycle(negDeps map[string][]*InferenceRule) []*InferenceRule {
	const (
		white = 0 // unvisited
		gray  = 1 // in current path
		black = 2 // finished
	)

	color := make(map[string]int)
	parent := make(map[string]*InferenceRule)

	// Initialize all as white
	for _, r := range s.rules {
		color[r.ID] = white
	}

	// DFS to find cycle
	var cyclePath []*InferenceRule

	var dfs func(rule *InferenceRule) bool
	dfs = func(rule *InferenceRule) bool {
		color[rule.ID] = gray

		for _, dep := range negDeps[rule.ID] {
			if color[dep.ID] == gray {
				// Found a cycle - reconstruct the path
				cyclePath = []*InferenceRule{dep}
				for curr := rule; curr.ID != dep.ID; {
					cyclePath = append(cyclePath, curr)
					if p, ok := parent[curr.ID]; ok {
						curr = p
					} else {
						break
					}
				}
				return true
			}
			if color[dep.ID] == white {
				parent[dep.ID] = rule
				if dfs(dep) {
					return true
				}
			}
		}

		color[rule.ID] = black
		return false
	}

	// Run DFS from each unvisited node
	for _, r := range s.rules {
		if color[r.ID] == white {
			if dfs(r) {
				return cyclePath
			}
		}
	}

	return nil
}

// assignStrata assigns stratum numbers using topological order on negative dependencies.
// Rules with no negative dependencies get stratum 0.
// Rules with negative dependencies get stratum = max(deps stratum) + 1.
func (s *Stratifier) assignStrata(negDeps map[string][]*InferenceRule) {
	// Initialize all rules to stratum 0
	for _, r := range s.rules {
		r.Stratum = 0
	}

	// Iteratively update strata until fixpoint
	changed := true
	for changed {
		changed = false
		for _, r := range s.rules {
			for _, dep := range negDeps[r.ID] {
				// Rule r negatively depends on dep, so r.Stratum must be > dep.Stratum
				if r.Stratum <= dep.Stratum {
					r.Stratum = dep.Stratum + 1
					changed = true
				}
			}
		}
	}
}

// GetRulesByStratum returns rules grouped by stratum.
// The returned map has stratum number as key and slice of rules as value.
func (s *Stratifier) GetRulesByStratum() map[int][]*InferenceRule {
	result := make(map[int][]*InferenceRule)
	for _, r := range s.rules {
		result[r.Stratum] = append(result[r.Stratum], r)
	}
	return result
}

// MaxStratum returns the highest stratum number among all rules.
// Returns -1 if there are no rules.
func (s *Stratifier) MaxStratum() int {
	if len(s.rules) == 0 {
		return -1
	}

	maxS := 0
	for _, r := range s.rules {
		if r.Stratum > maxS {
			maxS = r.Stratum
		}
	}
	return maxS
}

// GetRulesInOrder returns all rules sorted by stratum (ascending),
// then by priority within each stratum (descending).
func (s *Stratifier) GetRulesInOrder() []*InferenceRule {
	if len(s.rules) == 0 {
		return []*InferenceRule{}
	}

	// Group by stratum
	byStratum := s.GetRulesByStratum()

	// Determine order of strata
	maxS := s.MaxStratum()

	// Collect rules in order
	result := make([]*InferenceRule, 0, len(s.rules))
	for stratum := 0; stratum <= maxS; stratum++ {
		rules := byStratum[stratum]
		// Sort by priority within stratum (higher priority first)
		for i := 0; i < len(rules); i++ {
			for j := i + 1; j < len(rules); j++ {
				if rules[j].Priority > rules[i].Priority {
					rules[i], rules[j] = rules[j], rules[i]
				}
			}
		}
		result = append(result, rules...)
	}

	return result
}

// =============================================================================
// SemiNaiveEvaluator (SNE.7 & SNE.8) - Semi-Naive Evaluation Engine
// =============================================================================

// SemiNaiveEvaluator implements the semi-naive evaluation algorithm for
// computing the fixpoint of a set of inference rules over a database.
//
// Semi-naive evaluation is an optimization over naive evaluation that avoids
// redundant computation by tracking "delta" (newly derived) tuples and only
// computing joins that involve at least one delta tuple.
//
// The key insight is:
//   - Naive: T_new = apply_rule(T_all)
//   - Semi-naive: T_new = apply_rule(T_delta, T_all) ∪ apply_rule(T_all, T_delta)
//
// This can result in order-of-magnitude speedups for recursive rules.
type SemiNaiveEvaluator struct {
	// rules is the set of inference rules to evaluate
	rules []*InferenceRule

	// stratifier handles rule stratification for proper evaluation order
	stratifier *Stratifier

	// maxIterations prevents infinite loops in case of bugs
	maxIterations int
}

// NewSemiNaiveEvaluator creates a new semi-naive evaluator for the given rules.
// The rules will be stratified to ensure proper evaluation order.
func NewSemiNaiveEvaluator(rules []*InferenceRule) (*SemiNaiveEvaluator, error) {
	stratifier := NewStratifier(rules)
	if err := stratifier.Stratify(); err != nil {
		return nil, fmt.Errorf("failed to stratify rules: %w", err)
	}

	return &SemiNaiveEvaluator{
		rules:         rules,
		stratifier:    stratifier,
		maxIterations: 1000, // Default max iterations
	}, nil
}

// SetMaxIterations sets the maximum number of iterations for fixpoint computation.
func (e *SemiNaiveEvaluator) SetMaxIterations(max int) {
	if max > 0 {
		e.maxIterations = max
	}
}

// Evaluate runs semi-naive evaluation to fixpoint, returning the total number
// of new edges derived.
func (e *SemiNaiveEvaluator) Evaluate(db Database) (int, error) {
	totalDerived := 0

	// Process each stratum in order
	maxStratum := e.stratifier.MaxStratum()
	for stratum := 0; stratum <= maxStratum; stratum++ {
		derived, err := e.evaluateStratum(stratum, db)
		if err != nil {
			return totalDerived, fmt.Errorf("evaluation failed at stratum %d: %w", stratum, err)
		}
		totalDerived += derived
	}

	return totalDerived, nil
}

// evaluateStratum evaluates all rules in a single stratum to fixpoint.
func (e *SemiNaiveEvaluator) evaluateStratum(stratum int, db Database) (int, error) {
	rulesByStratum := e.stratifier.GetRulesByStratum()
	rules := rulesByStratum[stratum]
	if len(rules) == 0 {
		return 0, nil
	}

	totalDerived := 0

	// Initialize deltas for each predicate produced by rules in this stratum
	deltas := make(map[string]*DeltaTable)
	for _, rule := range rules {
		if _, exists := deltas[rule.HeadPredicate]; !exists {
			deltas[rule.HeadPredicate] = NewDeltaTable()
		}
	}

	// Initial application: apply all rules once to get initial delta
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		newDelta, err := e.applyRuleInitial(rule, db)
		if err != nil {
			return totalDerived, err
		}
		for _, edge := range newDelta.Edges() {
			if db.AddEdge(edge) {
				deltas[rule.HeadPredicate].Add(edge)
				totalDerived++
			}
		}
	}

	// Iterative semi-naive evaluation
	for iteration := 0; iteration < e.maxIterations; iteration++ {
		// Check if any delta is non-empty
		anyNonEmpty := false
		for _, delta := range deltas {
			if !delta.IsEmpty() {
				anyNonEmpty = true
				break
			}
		}
		if !anyNonEmpty {
			break // Fixpoint reached
		}

		// Compute new deltas
		newDeltas := make(map[string]*DeltaTable)
		for _, rule := range rules {
			if _, exists := newDeltas[rule.HeadPredicate]; !exists {
				newDeltas[rule.HeadPredicate] = NewDeltaTable()
			}
		}

		for _, rule := range rules {
			if !rule.Enabled {
				continue
			}
			newDelta, err := e.applyGeneralRuleSemiNaive(rule, deltas, db)
			if err != nil {
				return totalDerived, err
			}
			// Add truly new edges to database and newDeltas
			for _, edge := range newDelta.Edges() {
				if db.AddEdge(edge) {
					newDeltas[rule.HeadPredicate].Add(edge)
					totalDerived++
				}
			}
		}

		// Swap deltas
		deltas = newDeltas
	}

	return totalDerived, nil
}

// applyRuleInitial applies a rule without delta optimization (for initial iteration).
// This is equivalent to naive evaluation for a single iteration.
func (e *SemiNaiveEvaluator) applyRuleInitial(rule *InferenceRule, db Database) (*DeltaTable, error) {
	result := NewDeltaTable()

	if len(rule.Body) == 0 {
		return result, nil
	}

	// Start with the first body condition and extend bindings
	initialBindings := []map[string]string{{}}
	allBindings := e.extendBindings(initialBindings, rule.Body, db, nil)

	// Generate head edges from all valid bindings
	for _, bindings := range allBindings {
		headEdge := e.substituteHead(rule.Head, bindings)
		if headEdge != nil && !db.ContainsEdge(*headEdge) {
			result.Add(*headEdge)
		}
	}

	return result, nil
}

// applyRuleSemiNaive applies a rule using only the delta from previous iteration.
// This is the core optimization: instead of re-computing all tuples,
// we only compute new tuples that involve at least one delta tuple.
//
// For transitive closure rules (like `reachable(X,Z) :- reachable(X,Y), edge(Y,Z)`):
// 1. Join delta tuples from previous iteration with base relations
// 2. Only generate new tuples that weren't seen before
// 3. Use DeltaTable to track what's new vs old
// 4. Implement proper deduplication
func (e *SemiNaiveEvaluator) applyRuleSemiNaive(
	rule *InferenceRule,
	delta *DeltaTable,
	db Database,
) (*DeltaTable, error) {
	result := NewDeltaTable()

	if len(rule.Body) == 0 || delta.IsEmpty() {
		return result, nil
	}

	// For transitive closure optimization, we need to identify which body atoms
	// can use the delta and try each substitution pattern.
	//
	// For a rule like: reachable(X,Z) :- reachable(X,Y), edge(Y,Z)
	// We compute: delta_reachable × edge UNION reachable × delta_edge
	//
	// But since edge is typically a base relation (not derived), only the first
	// pattern applies: delta_reachable × edge

	deltaEdges := delta.Edges()

	// Try each body atom position as the "delta" position
	for deltaPos := 0; deltaPos < len(rule.Body); deltaPos++ {
		bodyAtom := rule.Body[deltaPos]

		// Skip negated atoms - they use full relation, not delta
		if bodyAtom.Negated {
			continue
		}

		// For each delta edge, try to unify with this body atom
		for _, deltaEdge := range deltaEdges {
			// Check if this delta edge matches the body atom's predicate
			if !e.matchesPredicate(bodyAtom, deltaEdge) {
				continue
			}

			// Try to unify the delta edge with the body atom
			bindings, ok := bodyAtom.Unify(map[string]string{}, deltaEdge)
			if !ok {
				continue
			}

			// Extend bindings with other body atoms (using full relations)
			extendedBindings := e.extendBindingsExcluding(
				[]map[string]string{bindings},
				rule.Body,
				deltaPos,
				db,
				nil, // No deltas for other atoms in this pattern
			)

			// Generate head edges from all valid bindings
			for _, finalBindings := range extendedBindings {
				headEdge := e.substituteHead(rule.Head, finalBindings)
				if headEdge != nil && !db.ContainsEdge(*headEdge) {
					result.Add(*headEdge)
				}
			}
		}
	}

	return result, nil
}

// applyGeneralRuleSemiNaive handles rules with multiple body atoms.
// At least one body atom must use delta values.
//
// Implementation requirements:
// 1. For each body atom, try substituting delta values
// 2. Combine results from all substitution patterns
// 3. Filter out tuples already in the full relation
// 4. Handle negated atoms correctly (use full relation, not delta)
// 5. Return only genuinely new tuples
func (e *SemiNaiveEvaluator) applyGeneralRuleSemiNaive(
	rule *InferenceRule,
	deltas map[string]*DeltaTable,
	db Database,
) (*DeltaTable, error) {
	result := NewDeltaTable()

	if len(rule.Body) == 0 {
		return result, nil
	}

	// Check if any relevant delta is non-empty
	hasRelevantDelta := false
	for _, bodyAtom := range rule.Body {
		if !bodyAtom.Negated {
			pred := bodyAtom.Predicate
			if !IsRuleVariable(pred) {
				if delta, exists := deltas[pred]; exists && !delta.IsEmpty() {
					hasRelevantDelta = true
					break
				}
			}
		}
	}

	if !hasRelevantDelta {
		return result, nil
	}

	// Generate all delta substitution patterns
	// For a rule with n non-negated body atoms, we try patterns where
	// at least one body atom uses delta values.
	//
	// To avoid double-counting, we use the standard semi-naive technique:
	// For body atoms b1, b2, ..., bn, we compute:
	//   delta_b1 × full_b2 × ... × full_bn
	// + full_b1 × delta_b2 × ... × full_bn  (only new combinations)
	// + ...
	// + full_b1 × full_b2 × ... × delta_bn  (only new combinations)
	//
	// The "only new combinations" part is handled by requiring that for pattern i,
	// all previous positions (j < i) use their OLD values (from before this iteration).
	// Since we don't track "old" values explicitly, we approximate this by using
	// the full relation minus delta.

	seenEdges := make(map[EdgeKey]struct{})

	for deltaPos := 0; deltaPos < len(rule.Body); deltaPos++ {
		bodyAtom := rule.Body[deltaPos]

		// Skip negated atoms - they always use full relation
		if bodyAtom.Negated {
			continue
		}

		// Get the predicate for this body atom
		pred := bodyAtom.Predicate
		if IsRuleVariable(pred) {
			continue // Can't use delta for variable predicates
		}

		// Get delta for this predicate
		delta, exists := deltas[pred]
		if !exists || delta.IsEmpty() {
			continue
		}

		// For each delta edge matching this body atom
		deltaEdges := delta.Edges()
		for _, deltaEdge := range deltaEdges {
			// Check predicate match
			if !e.matchesPredicate(bodyAtom, deltaEdge) {
				continue
			}

			// Unify delta edge with body atom
			bindings, ok := bodyAtom.Unify(map[string]string{}, deltaEdge)
			if !ok {
				continue
			}

			// Extend bindings with other body atoms
			// Body atoms at positions < deltaPos should use full relation (already in DB)
			// Body atoms at positions > deltaPos should use full relation
			// Body atoms at position = deltaPos use delta (already unified)
			extendedBindings := e.extendBindingsExcluding(
				[]map[string]string{bindings},
				rule.Body,
				deltaPos,
				db,
				deltas,
			)

			// Generate head edges
			for _, finalBindings := range extendedBindings {
				headEdge := e.substituteHead(rule.Head, finalBindings)
				if headEdge != nil {
					// Check if already seen in this iteration
					if _, seen := seenEdges[*headEdge]; seen {
						continue
					}
					seenEdges[*headEdge] = struct{}{}

					// Check if not already in database
					if !db.ContainsEdge(*headEdge) {
						result.Add(*headEdge)
					}
				}
			}
		}
	}

	return result, nil
}

// extendBindings extends a set of bindings by matching all body conditions.
// Returns all valid binding combinations.
func (e *SemiNaiveEvaluator) extendBindings(
	bindings []map[string]string,
	body []RuleCondition,
	db Database,
	deltas map[string]*DeltaTable,
) []map[string]string {
	result := bindings

	for _, bodyAtom := range body {
		result = e.extendBindingsForAtom(result, bodyAtom, db, deltas)
		if len(result) == 0 {
			return result // No valid bindings possible
		}
	}

	return result
}

// extendBindingsExcluding extends bindings for all body atoms except the one at excludePos.
func (e *SemiNaiveEvaluator) extendBindingsExcluding(
	bindings []map[string]string,
	body []RuleCondition,
	excludePos int,
	db Database,
	deltas map[string]*DeltaTable,
) []map[string]string {
	result := bindings

	for i, bodyAtom := range body {
		if i == excludePos {
			continue // Already unified with delta edge
		}
		result = e.extendBindingsForAtom(result, bodyAtom, db, deltas)
		if len(result) == 0 {
			return result
		}
	}

	return result
}

// extendBindingsForAtom extends bindings by matching a single body atom.
func (e *SemiNaiveEvaluator) extendBindingsForAtom(
	bindings []map[string]string,
	atom RuleCondition,
	db Database,
	deltas map[string]*DeltaTable,
) []map[string]string {
	var result []map[string]string

	for _, binding := range bindings {
		extended := e.matchAtom(binding, atom, db, deltas)
		result = append(result, extended...)
	}

	return result
}

// matchAtom attempts to match a body atom against the database.
// For negated atoms, it checks that no matching edge exists.
// For positive atoms, it finds all matching edges and extends bindings.
func (e *SemiNaiveEvaluator) matchAtom(
	bindings map[string]string,
	atom RuleCondition,
	db Database,
	deltas map[string]*DeltaTable,
) []map[string]string {
	if atom.Negated {
		return e.matchNegatedAtom(bindings, atom, db)
	}
	return e.matchPositiveAtom(bindings, atom, db)
}

// matchNegatedAtom checks that no edge matches the atom.
// Returns the original bindings if no match exists, empty otherwise.
func (e *SemiNaiveEvaluator) matchNegatedAtom(
	bindings map[string]string,
	atom RuleCondition,
	db Database,
) []map[string]string {
	// Substitute known variables
	substituted := atom.Substitute(bindings)

	// If all positions are ground (no variables), check for existence
	if !IsRuleVariable(substituted.Subject) &&
		!IsRuleVariable(substituted.Predicate) &&
		!IsRuleVariable(substituted.Object) {
		edge := EdgeKey{
			Subject:   substituted.Subject,
			Predicate: substituted.Predicate,
			Object:    substituted.Object,
		}
		if db.ContainsEdge(edge) {
			return nil // Edge exists, negation fails
		}
		return []map[string]string{bindings} // Edge doesn't exist, negation succeeds
	}

	// If there are still variables, we need to check if ANY matching edge exists
	pred := substituted.Predicate
	if IsRuleVariable(pred) {
		// Variable predicate - need to check all predicates
		// For simplicity, we assume negation on variable predicates is rare
		// and check all edges in the database
		// This is expensive but correct
		for _, p := range e.getAllPredicates(db) {
			edges := db.GetAllEdges(p)
			for _, edge := range edges {
				if e.atomMatchesEdge(substituted, edge, bindings) {
					return nil // Found a matching edge, negation fails
				}
			}
		}
		return []map[string]string{bindings}
	}

	// Fixed predicate - check only edges with that predicate
	edges := db.GetAllEdges(pred)
	for _, edge := range edges {
		if e.atomMatchesEdge(substituted, edge, bindings) {
			return nil // Found a matching edge, negation fails
		}
	}
	return []map[string]string{bindings}
}

// matchPositiveAtom finds all edges that match the atom and extends bindings.
func (e *SemiNaiveEvaluator) matchPositiveAtom(
	bindings map[string]string,
	atom RuleCondition,
	db Database,
) []map[string]string {
	var result []map[string]string

	// Substitute known variables
	substituted := atom.Substitute(bindings)

	// If predicate is fixed, only look at edges with that predicate
	if !IsRuleVariable(substituted.Predicate) {
		edges := db.GetAllEdges(substituted.Predicate)
		for _, edge := range edges {
			if newBindings, ok := atom.Unify(bindings, edge); ok {
				result = append(result, newBindings)
			}
		}
		return result
	}

	// Variable predicate - need to check all predicates
	for _, pred := range e.getAllPredicates(db) {
		edges := db.GetAllEdges(pred)
		for _, edge := range edges {
			if newBindings, ok := atom.Unify(bindings, edge); ok {
				result = append(result, newBindings)
			}
		}
	}

	return result
}

// getAllPredicates returns all predicates from a database.
func (e *SemiNaiveEvaluator) getAllPredicates(db Database) []string {
	// Use type assertion to access extended methods if available
	if imdb, ok := db.(*InMemoryDatabase); ok {
		return imdb.GetAllPredicates()
	}
	// Fallback: cannot get all predicates from generic Database interface
	return []string{}
}

// atomMatchesEdge checks if a (partially substituted) atom matches an edge.
func (e *SemiNaiveEvaluator) atomMatchesEdge(
	atom RuleCondition,
	edge EdgeKey,
	bindings map[string]string,
) bool {
	_, ok := atom.Unify(bindings, edge)
	return ok
}

// substituteHead substitutes variables in the rule head with bound values.
// Returns nil if any variable is unbound.
func (e *SemiNaiveEvaluator) substituteHead(
	head RuleCondition,
	bindings map[string]string,
) *EdgeKey {
	substituted := head.Substitute(bindings)

	// Check that all positions are ground (no unbound variables)
	if IsRuleVariable(substituted.Subject) ||
		IsRuleVariable(substituted.Predicate) ||
		IsRuleVariable(substituted.Object) {
		return nil
	}

	return &EdgeKey{
		Subject:   substituted.Subject,
		Predicate: substituted.Predicate,
		Object:    substituted.Object,
	}
}

// matchesPredicate checks if an edge matches a body atom's predicate.
func (e *SemiNaiveEvaluator) matchesPredicate(atom RuleCondition, edge EdgeKey) bool {
	if IsRuleVariable(atom.Predicate) {
		return true // Variable predicate matches any edge
	}
	return atom.Predicate == edge.Predicate
}

// =============================================================================
// Context-Aware Evaluation Methods (SNE.9 & SNE.10)
// =============================================================================

// EvaluateWithContext performs stratified semi-naive evaluation across all rule strata
// with context cancellation support. Returns all derived edges.
func (e *SemiNaiveEvaluator) EvaluateWithContext(ctx context.Context, db Database) (*DeltaTable, error) {
	if e.stratifier == nil {
		return nil, ErrNotStratified
	}

	allDerived := NewDeltaTable()
	maxStratum := e.stratifier.MaxStratum()

	// Handle case where there are no rules
	if maxStratum < 0 {
		return allDerived, nil
	}

	for stratum := 0; stratum <= maxStratum; stratum++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		stratumResult, err := e.evaluateStratumWithContext(ctx, stratum, db)
		if err != nil {
			return nil, fmt.Errorf("evaluate stratum %d: %w", stratum, err)
		}

		// Merge stratum results into database for next stratum
		for _, edge := range stratumResult.Edges() {
			db.AddEdge(edge)
			allDerived.Add(edge)
		}
	}

	return allDerived, nil
}

// evaluateStratumWithContext evaluates all rules in a stratum until no new tuples are derived.
// Uses semi-naive evaluation: only process delta tuples from previous iteration.
func (e *SemiNaiveEvaluator) evaluateStratumWithContext(
	ctx context.Context,
	stratum int,
	db Database,
) (*DeltaTable, error) {
	rulesByStratum := e.stratifier.GetRulesByStratum()
	rules := rulesByStratum[stratum]
	if len(rules) == 0 {
		return NewDeltaTable(), nil
	}

	// Initial delta contains all base facts for predicates in this stratum
	currentDelta := e.initializeStratumDelta(rules, db)
	fullResult := NewDeltaTable()

	for iteration := 0; iteration < e.maxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		newDelta := NewDeltaTable()

		for _, rule := range rules {
			if !rule.Enabled {
				continue
			}

			// Apply rule using only delta tuples from previous iteration
			ruleDelta, err := e.applyRuleSemiNaiveWithContext(ctx, rule, currentDelta, db)
			if err != nil {
				return nil, fmt.Errorf("apply rule %s: %w", rule.ID, err)
			}

			// Filter out tuples already in full result
			for _, edge := range ruleDelta.Edges() {
				if !fullResult.Contains(edge) && !db.ContainsEdge(edge) {
					newDelta.Add(edge)
					fullResult.Add(edge)
				}
			}
		}

		// Fixpoint reached when no new tuples derived
		if newDelta.IsEmpty() {
			break
		}

		currentDelta = newDelta
	}

	return fullResult, nil
}

// initializeStratumDelta initializes the delta with base facts relevant to rules in this stratum.
// For the initial iteration, we need to consider all existing edges that could match
// the body patterns of rules in this stratum.
func (e *SemiNaiveEvaluator) initializeStratumDelta(rules []*InferenceRule, db Database) *DeltaTable {
	delta := NewDeltaTable()

	// Collect all predicates referenced in rule bodies
	predicatesNeeded := make(map[string]struct{})
	for _, rule := range rules {
		for _, bodyPred := range rule.BodyPredicates {
			predicatesNeeded[bodyPred] = struct{}{}
		}
	}

	// Add all edges from these predicates to the initial delta
	for pred := range predicatesNeeded {
		edges := db.GetAllEdges(pred)
		for _, edge := range edges {
			delta.Add(edge)
		}
	}

	return delta
}

// applyRuleSemiNaiveWithContext applies a rule using semi-naive evaluation with context support.
// It processes only delta tuples from the previous iteration.
func (e *SemiNaiveEvaluator) applyRuleSemiNaiveWithContext(
	ctx context.Context,
	rule *InferenceRule,
	delta *DeltaTable,
	db Database,
) (*DeltaTable, error) {
	result := NewDeltaTable()

	if len(rule.Body) == 0 || delta.IsEmpty() {
		return result, nil
	}

	deltaEdges := delta.Edges()
	seenEdges := make(map[EdgeKey]struct{})

	// Try each body atom position as the "delta" position
	for deltaPos := 0; deltaPos < len(rule.Body); deltaPos++ {
		// Check for context cancellation periodically
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		bodyAtom := rule.Body[deltaPos]

		// Skip negated atoms - they use full relation, not delta
		if bodyAtom.Negated {
			continue
		}

		// For each delta edge, try to unify with this body atom
		for _, deltaEdge := range deltaEdges {
			// Check if this delta edge matches the body atom's predicate
			if !e.matchesPredicate(bodyAtom, deltaEdge) {
				continue
			}

			// Try to unify the delta edge with the body atom
			bindings, ok := bodyAtom.Unify(map[string]string{}, deltaEdge)
			if !ok {
				continue
			}

			// Extend bindings with other body atoms (using full relations)
			extendedBindings := e.extendBindingsExcluding(
				[]map[string]string{bindings},
				rule.Body,
				deltaPos,
				db,
				nil, // No deltas for other atoms in this pattern
			)

			// Generate head edges from all valid bindings
			for _, finalBindings := range extendedBindings {
				headEdge := e.substituteHead(rule.Head, finalBindings)
				if headEdge != nil {
					// Check if already seen in this iteration
					if _, seen := seenEdges[*headEdge]; seen {
						continue
					}
					seenEdges[*headEdge] = struct{}{}

					// Add to result (caller will filter against db)
					result.Add(*headEdge)
				}
			}
		}
	}

	return result, nil
}

// EvaluateIncremental performs incremental evaluation after base fact changes.
// It starts with the changes as the initial delta and only recomputes affected strata.
// This is more efficient than full evaluation when only a few facts have changed.
func (e *SemiNaiveEvaluator) EvaluateIncremental(
	ctx context.Context,
	db Database,
	changes *DeltaTable,
) (*DeltaTable, error) {
	if e.stratifier == nil {
		return nil, ErrNotStratified
	}

	if changes == nil || changes.IsEmpty() {
		return NewDeltaTable(), nil
	}

	allDerived := NewDeltaTable()
	maxStratum := e.stratifier.MaxStratum()

	// Handle case where there are no rules
	if maxStratum < 0 {
		return allDerived, nil
	}

	// Determine which strata are affected by the changes
	// A stratum is affected if any of its rules reference predicates in the changes
	affectedStrata := e.findAffectedStrata(changes)

	// Start from the lowest affected stratum
	startStratum := maxStratum + 1
	for stratum := range affectedStrata {
		if stratum < startStratum {
			startStratum = stratum
		}
	}

	// If no strata are affected, return empty result
	if startStratum > maxStratum {
		return allDerived, nil
	}

	// Initialize the current delta with the input changes
	currentDelta := NewDeltaTable()
	currentDelta.Merge(changes)

	for stratum := startStratum; stratum <= maxStratum; stratum++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		stratumResult, err := e.evaluateStratumIncrementalWithContext(ctx, stratum, db, currentDelta)
		if err != nil {
			return nil, fmt.Errorf("evaluate stratum %d incrementally: %w", stratum, err)
		}

		// Merge stratum results into database and track for next stratum
		for _, edge := range stratumResult.Edges() {
			if db.AddEdge(edge) {
				allDerived.Add(edge)
				currentDelta.Add(edge) // New facts become delta for next stratum
			}
		}
	}

	return allDerived, nil
}

// findAffectedStrata determines which strata are affected by changes to certain predicates.
func (e *SemiNaiveEvaluator) findAffectedStrata(changes *DeltaTable) map[int]struct{} {
	affected := make(map[int]struct{})

	// Collect predicates from changes
	changedPredicates := make(map[string]struct{})
	for _, edge := range changes.Edges() {
		changedPredicates[edge.Predicate] = struct{}{}
	}

	// Find rules that reference these predicates
	rulesByStratum := e.stratifier.GetRulesByStratum()
	for stratum, rules := range rulesByStratum {
		for _, rule := range rules {
			for _, bodyPred := range rule.BodyPredicates {
				if _, changed := changedPredicates[bodyPred]; changed {
					affected[stratum] = struct{}{}
					break
				}
			}
		}
	}

	// Also mark all higher strata as affected (due to cascading dependencies)
	maxStratum := e.stratifier.MaxStratum()
	minAffected := maxStratum + 1
	for stratum := range affected {
		if stratum < minAffected {
			minAffected = stratum
		}
	}

	for stratum := minAffected; stratum <= maxStratum; stratum++ {
		affected[stratum] = struct{}{}
	}

	return affected
}

// evaluateStratumIncrementalWithContext evaluates a stratum incrementally using provided delta.
func (e *SemiNaiveEvaluator) evaluateStratumIncrementalWithContext(
	ctx context.Context,
	stratum int,
	db Database,
	initialDelta *DeltaTable,
) (*DeltaTable, error) {
	rulesByStratum := e.stratifier.GetRulesByStratum()
	rules := rulesByStratum[stratum]
	if len(rules) == 0 {
		return NewDeltaTable(), nil
	}

	// Use the provided initial delta instead of computing from scratch
	currentDelta := NewDeltaTable()
	currentDelta.Merge(initialDelta)
	fullResult := NewDeltaTable()

	for iteration := 0; iteration < e.maxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Check if delta is empty (fixpoint reached)
		if currentDelta.IsEmpty() {
			break
		}

		newDelta := NewDeltaTable()

		for _, rule := range rules {
			if !rule.Enabled {
				continue
			}

			// Apply rule using only delta tuples from previous iteration
			ruleDelta, err := e.applyRuleSemiNaiveWithContext(ctx, rule, currentDelta, db)
			if err != nil {
				return nil, fmt.Errorf("apply rule %s: %w", rule.ID, err)
			}

			// Filter out tuples already in full result or database
			for _, edge := range ruleDelta.Edges() {
				if !fullResult.Contains(edge) && !db.ContainsEdge(edge) {
					newDelta.Add(edge)
					fullResult.Add(edge)
				}
			}
		}

		currentDelta = newDelta
	}

	return fullResult, nil
}
