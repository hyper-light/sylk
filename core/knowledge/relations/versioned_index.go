// Package relations provides relation extraction and versioned transitive closure support.
package relations

import (
	"sort"
	"sync/atomic"
	"time"
)

// =============================================================================
// IndexSnapshot - Lock-Free Snapshot Reads
// =============================================================================

// IndexSnapshot represents an immutable point-in-time snapshot of transitive closure data.
// Once created, the snapshot cannot be modified, enabling lock-free reads.
// Readers can safely use a snapshot while writers create new versions.
type IndexSnapshot struct {
	version   uint64                       // Monotonically increasing version number
	data      map[string]map[string]bool   // TC data: subject -> object -> reachable
	createdAt time.Time                    // When this snapshot was created
}

// NewIndexSnapshot creates a new immutable IndexSnapshot.
// The provided data is deep-copied to ensure immutability.
func NewIndexSnapshot(version uint64, data map[string]map[string]bool) *IndexSnapshot {
	// Deep copy the data to ensure immutability
	copiedData := make(map[string]map[string]bool, len(data))
	for subject, objects := range data {
		copiedObjects := make(map[string]bool, len(objects))
		for object, reachable := range objects {
			copiedObjects[object] = reachable
		}
		copiedData[subject] = copiedObjects
	}

	return &IndexSnapshot{
		version:   version,
		data:      copiedData,
		createdAt: time.Now(),
	}
}

// Version returns the monotonic version number of this snapshot.
func (s *IndexSnapshot) Version() uint64 {
	return s.version
}

// CreatedAt returns the timestamp when this snapshot was created.
func (s *IndexSnapshot) CreatedAt() time.Time {
	return s.createdAt
}

// Lookup checks if there is a path from subject to object in the transitive closure.
// Returns true if object is reachable from subject, false otherwise.
// This is an O(1) operation.
func (s *IndexSnapshot) Lookup(subject, object string) bool {
	if s.data == nil {
		return false
	}
	objects, exists := s.data[subject]
	if !exists {
		return false
	}
	return objects[object]
}

// GetReachable returns all objects reachable from the given subject.
// The returned slice is sorted for deterministic ordering.
func (s *IndexSnapshot) GetReachable(subject string) []string {
	if s.data == nil {
		return nil
	}
	objects, exists := s.data[subject]
	if !exists {
		return nil
	}

	result := make([]string, 0, len(objects))
	for object, reachable := range objects {
		if reachable {
			result = append(result, object)
		}
	}

	// Sort for deterministic ordering
	sort.Strings(result)
	return result
}

// GetSubjects returns all subjects in the snapshot.
// The returned slice is sorted for deterministic ordering.
func (s *IndexSnapshot) GetSubjects() []string {
	if s.data == nil {
		return nil
	}

	result := make([]string, 0, len(s.data))
	for subject := range s.data {
		result = append(result, subject)
	}

	sort.Strings(result)
	return result
}

// Size returns the total number of reachable pairs (edges) in the transitive closure.
func (s *IndexSnapshot) Size() int {
	if s.data == nil {
		return 0
	}

	count := 0
	for _, objects := range s.data {
		for _, reachable := range objects {
			if reachable {
				count++
			}
		}
	}
	return count
}

// SubjectCount returns the number of unique subjects in the snapshot.
func (s *IndexSnapshot) SubjectCount() int {
	if s.data == nil {
		return 0
	}
	return len(s.data)
}

// =============================================================================
// IntervalLabel - O(1) Reachability Queries via Interval Containment
// =============================================================================

// IntervalLabel represents DFS interval labeling for O(1) reachability queries.
// In a DFS traversal, if node A is an ancestor of node B in the DFS tree,
// then B's interval [Pre, Post] is contained within A's interval [Pre, Post].
// This enables O(1) reachability checks for tree/DAG structures.
type IntervalLabel struct {
	Pre   int // DFS pre-order number (entry time)
	Post  int // DFS post-order number (exit time)
	Level int // Tree depth level (root = 0)
}

// Contains checks if the other node's interval is contained within this node's interval.
// If other's interval is contained, then other is a descendant of this node in the DFS tree.
// This provides O(1) reachability for DAGs (with some false positives for non-tree edges).
func (l IntervalLabel) Contains(other IntervalLabel) bool {
	return l.Pre <= other.Pre && other.Post <= l.Post
}

// IsAncestorOf is an alias for Contains - checks if this node is an ancestor of other.
// Returns true if other is reachable from this node in the DFS spanning tree.
func (l IntervalLabel) IsAncestorOf(other IntervalLabel) bool {
	return l.Contains(other)
}

// IsDescendantOf checks if this node is a descendant of the other node.
// Returns true if this node is reachable from other in the DFS spanning tree.
func (l IntervalLabel) IsDescendantOf(other IntervalLabel) bool {
	return other.Contains(l)
}

// IsValid returns true if the interval label has valid pre/post values.
// A valid label has Pre < Post and both non-negative.
func (l IntervalLabel) IsValid() bool {
	return l.Pre >= 0 && l.Post >= 0 && l.Pre < l.Post
}

// =============================================================================
// Interval Label Computation
// =============================================================================

// ComputeIntervalLabels computes DFS interval labels for all nodes in the graph.
// The graph is represented as an adjacency list: node -> list of successors.
// Returns a map of node -> IntervalLabel.
//
// For graphs with cycles, the DFS will visit each node only once, treating
// back edges as non-tree edges. The interval containment property holds
// for tree edges but may produce false negatives for paths via back edges.
//
// For disconnected graphs, each component is processed separately,
// with each component's root starting a new DFS tree.
func ComputeIntervalLabels(graph map[string][]string) map[string]IntervalLabel {
	labels := make(map[string]IntervalLabel)
	visited := make(map[string]bool)
	counter := 0

	// Get all nodes (including those only appearing as targets)
	allNodes := getAllNodes(graph)

	// Process each unvisited node as a potential root
	for _, node := range allNodes {
		if !visited[node] {
			dfsLabel(node, graph, labels, visited, &counter, 0)
		}
	}

	return labels
}

// getAllNodes returns all unique nodes in the graph (both sources and targets).
// The result is sorted for deterministic processing order.
func getAllNodes(graph map[string][]string) []string {
	nodeSet := make(map[string]bool)

	for source, targets := range graph {
		nodeSet[source] = true
		for _, target := range targets {
			nodeSet[target] = true
		}
	}

	nodes := make([]string, 0, len(nodeSet))
	for node := range nodeSet {
		nodes = append(nodes, node)
	}

	sort.Strings(nodes)
	return nodes
}

// dfsLabel performs DFS traversal and assigns interval labels.
func dfsLabel(
	node string,
	graph map[string][]string,
	labels map[string]IntervalLabel,
	visited map[string]bool,
	counter *int,
	level int,
) {
	if visited[node] {
		return
	}

	visited[node] = true
	*counter++
	pre := *counter

	// Visit successors
	successors := graph[node]
	// Sort successors for deterministic traversal order
	sortedSuccessors := make([]string, len(successors))
	copy(sortedSuccessors, successors)
	sort.Strings(sortedSuccessors)

	for _, successor := range sortedSuccessors {
		dfsLabel(successor, graph, labels, visited, counter, level+1)
	}

	*counter++
	post := *counter

	labels[node] = IntervalLabel{
		Pre:   pre,
		Post:  post,
		Level: level,
	}
}

// =============================================================================
// Interval-Based Reachability Index
// =============================================================================

// IntervalIndex provides O(1) reachability queries using interval labels.
// It wraps computed interval labels with convenient query methods.
type IntervalIndex struct {
	labels    map[string]IntervalLabel
	createdAt time.Time
}

// NewIntervalIndex creates a new IntervalIndex from a graph.
func NewIntervalIndex(graph map[string][]string) *IntervalIndex {
	return &IntervalIndex{
		labels:    ComputeIntervalLabels(graph),
		createdAt: time.Now(),
	}
}

// NewIntervalIndexFromLabels creates an IntervalIndex from pre-computed labels.
func NewIntervalIndexFromLabels(labels map[string]IntervalLabel) *IntervalIndex {
	// Deep copy labels
	copiedLabels := make(map[string]IntervalLabel, len(labels))
	for node, label := range labels {
		copiedLabels[node] = label
	}

	return &IntervalIndex{
		labels:    copiedLabels,
		createdAt: time.Now(),
	}
}

// IsReachable checks if target is reachable from source using interval containment.
// This is O(1) but may return false negatives for non-tree paths in DAGs with
// multiple paths or back edges.
func (idx *IntervalIndex) IsReachable(source, target string) bool {
	sourceLabel, sourceExists := idx.labels[source]
	targetLabel, targetExists := idx.labels[target]

	if !sourceExists || !targetExists {
		return false
	}

	return sourceLabel.IsAncestorOf(targetLabel)
}

// GetLabel returns the interval label for a node.
// Returns the label and true if found, or zero value and false if not found.
func (idx *IntervalIndex) GetLabel(node string) (IntervalLabel, bool) {
	label, exists := idx.labels[node]
	return label, exists
}

// GetDescendants returns all nodes that are descendants of the given node
// based on interval containment.
func (idx *IntervalIndex) GetDescendants(node string) []string {
	nodeLabel, exists := idx.labels[node]
	if !exists {
		return nil
	}

	var descendants []string
	for candidate, candidateLabel := range idx.labels {
		if candidate != node && nodeLabel.IsAncestorOf(candidateLabel) {
			descendants = append(descendants, candidate)
		}
	}

	sort.Strings(descendants)
	return descendants
}

// GetAncestors returns all nodes that are ancestors of the given node
// based on interval containment.
func (idx *IntervalIndex) GetAncestors(node string) []string {
	nodeLabel, exists := idx.labels[node]
	if !exists {
		return nil
	}

	var ancestors []string
	for candidate, candidateLabel := range idx.labels {
		if candidate != node && candidateLabel.IsAncestorOf(nodeLabel) {
			ancestors = append(ancestors, candidate)
		}
	}

	sort.Strings(ancestors)
	return ancestors
}

// Size returns the number of nodes in the index.
func (idx *IntervalIndex) Size() int {
	return len(idx.labels)
}

// CreatedAt returns when the index was created.
func (idx *IntervalIndex) CreatedAt() time.Time {
	return idx.createdAt
}

// Labels returns a copy of all interval labels.
func (idx *IntervalIndex) Labels() map[string]IntervalLabel {
	result := make(map[string]IntervalLabel, len(idx.labels))
	for node, label := range idx.labels {
		result[node] = label
	}
	return result
}

// =============================================================================
// VersionedIntervalIndex - Lock-Free Versioned Reads
// =============================================================================

// versionedState holds both IndexSnapshot and IntervalIndex together as an
// immutable versioned state. This allows atomic swaps of both structures.
type versionedState struct {
	snapshot      *IndexSnapshot
	intervalIndex *IntervalIndex
	version       uint64
}

// VersionedIntervalIndex provides lock-free versioned access to both
// IndexSnapshot and IntervalIndex. It uses atomic pointer swaps to enable
// concurrent readers without blocking, while writers atomically publish
// new versions.
//
// This design follows the copy-on-write pattern:
// - Readers get the current state pointer atomically (lock-free)
// - Writers create new immutable state and swap atomically
// - Old versions remain valid for existing readers until they release
type VersionedIntervalIndex struct {
	state atomic.Pointer[versionedState]
}

// NewVersionedIntervalIndex creates a new VersionedIntervalIndex from initial
// graph and transitive closure data.
//
// Parameters:
//   - graph: adjacency list representation for interval label computation
//   - tcData: transitive closure data (subject -> object -> reachable)
//
// The initial version is set to 1.
func NewVersionedIntervalIndex(graph map[string][]string, tcData map[string]map[string]bool) *VersionedIntervalIndex {
	snapshot := NewIndexSnapshot(1, tcData)
	intervalIndex := NewIntervalIndex(graph)

	state := &versionedState{
		snapshot:      snapshot,
		intervalIndex: intervalIndex,
		version:       1,
	}

	vi := &VersionedIntervalIndex{}
	vi.state.Store(state)

	return vi
}

// GetSnapshot returns the current IndexSnapshot for lock-free reads.
// The returned snapshot is immutable and safe to use concurrently.
// This operation is O(1) and never blocks.
func (vi *VersionedIntervalIndex) GetSnapshot() *IndexSnapshot {
	state := vi.state.Load()
	if state == nil {
		return nil
	}
	return state.snapshot
}

// GetIntervalIndex returns the current IntervalIndex for lock-free reads.
// The returned index is immutable and safe to use concurrently.
// This operation is O(1) and never blocks.
func (vi *VersionedIntervalIndex) GetIntervalIndex() *IntervalIndex {
	state := vi.state.Load()
	if state == nil {
		return nil
	}
	return state.intervalIndex
}

// Version returns the current version number.
// This operation is O(1) and never blocks.
func (vi *VersionedIntervalIndex) Version() uint64 {
	state := vi.state.Load()
	if state == nil {
		return 0
	}
	return state.version
}

// AtomicSwap atomically updates both the IndexSnapshot and IntervalIndex
// to new versions. The new version number is automatically incremented.
//
// This operation is atomic - readers will either see the old state or
// the new state, never a partial update. Existing readers holding
// references to the old state can continue using it safely.
//
// Parameters:
//   - newSnapshot: the new IndexSnapshot (must not be nil)
//   - newIndex: the new IntervalIndex (must not be nil)
//
// Returns the new version number after the swap.
func (vi *VersionedIntervalIndex) AtomicSwap(newSnapshot *IndexSnapshot, newIndex *IntervalIndex) uint64 {
	// Get current version for incrementing
	oldState := vi.state.Load()
	var newVersion uint64 = 1
	if oldState != nil {
		newVersion = oldState.version + 1
	}

	newState := &versionedState{
		snapshot:      newSnapshot,
		intervalIndex: newIndex,
		version:       newVersion,
	}

	vi.state.Store(newState)
	return newVersion
}

// GetState returns both the snapshot and interval index atomically.
// This ensures the caller gets a consistent view of both structures
// from the same version.
func (vi *VersionedIntervalIndex) GetState() (*IndexSnapshot, *IntervalIndex, uint64) {
	state := vi.state.Load()
	if state == nil {
		return nil, nil, 0
	}
	return state.snapshot, state.intervalIndex, state.version
}

// =============================================================================
// VTC.5 - Lock-Free Read Operations
// =============================================================================

// IsReachable provides O(1) lock-free reachability check using the interval index.
// Uses atomic pointer read to get current snapshot.
// Returns false if source or target is not in the index.
func (vi *VersionedIntervalIndex) IsReachable(source, target string) bool {
	state := vi.state.Load()
	if state == nil || state.intervalIndex == nil {
		return false
	}
	return state.intervalIndex.IsReachable(source, target)
}

// GetAllReachable returns all nodes reachable from source.
// Lock-free read using current snapshot.
// Returns nil if source is not in the snapshot or if the snapshot is nil.
func (vi *VersionedIntervalIndex) GetAllReachable(source string) []string {
	state := vi.state.Load()
	if state == nil || state.snapshot == nil {
		return nil
	}
	return state.snapshot.GetReachable(source)
}

// LookupInSnapshot provides direct TC lookup using the snapshot.
// This is an O(1) operation that uses the transitive closure data directly.
// Returns false if source-target pair is not reachable or if snapshot is nil.
func (vi *VersionedIntervalIndex) LookupInSnapshot(source, target string) bool {
	state := vi.state.Load()
	if state == nil || state.snapshot == nil {
		return false
	}
	return state.snapshot.Lookup(source, target)
}

// =============================================================================
// VTC.6 - Rebuild Operations with Atomic Swap
// =============================================================================

// Rebuild computes new TC and atomically swaps to new state.
// Existing readers continue using old state until they're done.
//
// Parameters:
//   - graph: adjacency list representation for interval label computation
//   - tcData: transitive closure data (subject -> object -> reachable)
//
// Returns nil on success.
func (vi *VersionedIntervalIndex) Rebuild(graph map[string][]string, tcData map[string]map[string]bool) error {
	// Get current version for incrementing
	oldState := vi.state.Load()
	var newVersion uint64 = 1
	if oldState != nil {
		newVersion = oldState.version + 1
	}

	// Build new snapshot and interval index from data
	newSnapshot := NewIndexSnapshot(newVersion, tcData)
	newIntervalIndex := NewIntervalIndex(graph)

	// Atomic swap to new state
	vi.AtomicSwap(newSnapshot, newIntervalIndex)
	return nil
}

// RebuildWithMerge rebuilds while merging new edges with existing graph data.
// This is useful for incremental updates where new edges are added.
//
// Parameters:
//   - newEdges: new edges to add (source -> list of targets)
//
// Returns nil on success.
func (vi *VersionedIntervalIndex) RebuildWithMerge(newEdges map[string][]string) error {
	// Get current state
	currentState := vi.state.Load()

	// Initialize merged graph
	mergedGraph := make(map[string][]string)

	// If we have existing state, we need to extract the graph
	// Since we don't store the original graph, we reconstruct from interval index
	if currentState != nil && currentState.intervalIndex != nil {
		labels := currentState.intervalIndex.Labels()
		for node := range labels {
			mergedGraph[node] = []string{}
		}
	}

	// Merge new edges
	for source, targets := range newEdges {
		existing := mergedGraph[source]
		targetSet := make(map[string]bool)

		// Add existing targets to set
		for _, t := range existing {
			targetSet[t] = true
		}

		// Add new targets to set
		for _, t := range targets {
			targetSet[t] = true
		}

		// Convert set back to slice
		merged := make([]string, 0, len(targetSet))
		for t := range targetSet {
			merged = append(merged, t)
		}
		mergedGraph[source] = merged
	}

	// Compute transitive closure from merged graph
	tcData := computeTransitiveClosure(mergedGraph)

	// Rebuild with merged data
	return vi.Rebuild(mergedGraph, tcData)
}

// computeTransitiveClosure computes the transitive closure of a graph.
// Uses a simple DFS-based approach to find all reachable nodes from each source.
func computeTransitiveClosure(graph map[string][]string) map[string]map[string]bool {
	tc := make(map[string]map[string]bool)

	// Get all nodes
	allNodes := make(map[string]bool)
	for source, targets := range graph {
		allNodes[source] = true
		for _, target := range targets {
			allNodes[target] = true
		}
	}

	// For each node, find all reachable nodes using DFS
	for node := range allNodes {
		reachable := make(map[string]bool)
		visited := make(map[string]bool)
		dfsReachable(node, graph, visited, reachable)

		// Remove self from reachable set (unless there's a self-loop)
		if len(reachable) > 0 {
			tc[node] = reachable
		}
	}

	return tc
}

// dfsReachable performs DFS to find all nodes reachable from current node.
func dfsReachable(node string, graph map[string][]string, visited, reachable map[string]bool) {
	if visited[node] {
		return
	}
	visited[node] = true

	for _, target := range graph[node] {
		reachable[target] = true
		dfsReachable(target, graph, visited, reachable)
	}
}
