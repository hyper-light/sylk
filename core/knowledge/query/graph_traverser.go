// Package query provides hybrid query execution for the knowledge system.
// GraphTraverser implements pattern-based graph traversal with BFS and cycle detection.
package query

import (
	"container/list"
	"context"
	"fmt"
	"hash/fnv"
)

// =============================================================================
// Graph Result Type
// =============================================================================

// GraphResult represents a single result from a graph pattern traversal.
type GraphResult struct {
	// ID is the unique identifier of the terminal node in the path.
	ID string `json:"id"`

	// Path contains the sequence of node IDs from start to end.
	Path []string `json:"path"`

	// MatchedEdges contains information about edges traversed.
	MatchedEdges []EdgeMatch `json:"matched_edges,omitempty"`

	// Score is the combined score based on edge weights and path length.
	Score float64 `json:"score"`
}

// =============================================================================
// Edge Types for Traversal
// =============================================================================

// Edge represents a graph edge returned by EdgeQuerier.
type Edge struct {
	ID       int64   `json:"id"`
	SourceID string  `json:"source_id"`
	TargetID string  `json:"target_id"`
	EdgeType string  `json:"edge_type"`
	Weight   float64 `json:"weight"`
}

// =============================================================================
// Edge Querier Interface
// =============================================================================

// EdgeQuerier defines the interface for querying graph edges.
// This abstraction allows for testing with mock implementations.
type EdgeQuerier interface {
	// GetOutgoingEdges returns all outgoing edges from the given node.
	// If edgeTypes is provided, only edges of those types are returned.
	GetOutgoingEdges(nodeID string, edgeTypes []string) ([]Edge, error)

	// GetIncomingEdges returns all incoming edges to the given node.
	// If edgeTypes is provided, only edges of those types are returned.
	GetIncomingEdges(nodeID string, edgeTypes []string) ([]Edge, error)

	// GetNodeByPattern finds nodes matching the given pattern.
	// Returns node IDs matching the pattern constraints.
	GetNodesByPattern(pattern *NodeMatcher) ([]string, error)
}

// =============================================================================
// Graph Traverser Configuration
// =============================================================================

// TraverserConfig configures the behavior of the GraphTraverser.
type TraverserConfig struct {
	// SimpleVisit when true uses only nodeID for visit tracking (faster but
	// may miss paths in highly connected graphs). When false, uses path-based
	// visit tracking for proper cycle detection across all path branches.
	// Default is false for full cycle detection.
	SimpleVisit bool

	// MaxPathLength limits the maximum path length during traversal.
	// Helps prevent exponential path explosion in highly connected graphs.
	// Default is 20 if not set.
	MaxPathLength int

	// ConvergenceDetection when true enables detection of diamond/convergence
	// patterns where different paths lead to the same node. When detected,
	// the converged node is recorded in results but not re-expanded, preventing
	// redundant processing. Default is true.
	ConvergenceDetection bool
}

// DefaultTraverserConfig returns a TraverserConfig with sensible defaults.
func DefaultTraverserConfig() TraverserConfig {
	return TraverserConfig{
		SimpleVisit:          false,
		MaxPathLength:        20,
		ConvergenceDetection: true,
	}
}

// =============================================================================
// Graph Traverser
// =============================================================================

// GraphTraverser provides pattern-based graph traversal using BFS.
// It implements graceful degradation by returning empty results on failure.
//
// PF.4.6: Supports configurable path-based visit tracking for proper cycle
// detection across different path branches in highly connected graphs.
type GraphTraverser struct {
	db     EdgeQuerier
	config TraverserConfig
}

// NewGraphTraverser creates a new GraphTraverser with the provided edge querier.
// Uses default configuration with full path-based cycle detection.
func NewGraphTraverser(db EdgeQuerier) *GraphTraverser {
	return NewGraphTraverserWithConfig(db, DefaultTraverserConfig())
}

// NewGraphTraverserWithConfig creates a new GraphTraverser with custom configuration.
func NewGraphTraverserWithConfig(db EdgeQuerier, config TraverserConfig) *GraphTraverser {
	if config.MaxPathLength <= 0 {
		config.MaxPathLength = 20
	}
	return &GraphTraverser{
		db:     db,
		config: config,
	}
}

// Execute performs a graph pattern traversal with the given pattern and limit.
// Returns empty results on failure rather than propagating errors (graceful degradation).
// Respects context cancellation.
func (gt *GraphTraverser) Execute(ctx context.Context, pattern *GraphPattern, limit int) ([]GraphResult, error) {
	if gt.db == nil {
		return []GraphResult{}, nil
	}

	if pattern == nil || pattern.IsEmpty() {
		return []GraphResult{}, nil
	}

	if limit <= 0 {
		limit = 10
	}

	// Check context before executing
	select {
	case <-ctx.Done():
		return []GraphResult{}, ctx.Err()
	default:
	}

	return gt.executeTraversal(ctx, pattern, limit)
}

// executeTraversal performs the actual graph traversal.
func (gt *GraphTraverser) executeTraversal(ctx context.Context, pattern *GraphPattern, limit int) ([]GraphResult, error) {
	// Find starting nodes
	startNodes, err := gt.findStartNodes(pattern)
	if err != nil {
		// Graceful degradation: return empty results on error
		return []GraphResult{}, nil
	}

	if len(startNodes) == 0 {
		return []GraphResult{}, nil
	}

	// Execute BFS traversal from each start node
	var allResults []GraphResult
	for _, startID := range startNodes {
		select {
		case <-ctx.Done():
			return []GraphResult{}, ctx.Err()
		default:
		}

		results := gt.bfsTraverse(ctx, startID, pattern, limit-len(allResults))
		allResults = append(allResults, results...)

		if len(allResults) >= limit {
			break
		}
	}

	if len(allResults) > limit {
		allResults = allResults[:limit]
	}

	return allResults, nil
}

// findStartNodes identifies nodes matching the start pattern.
func (gt *GraphTraverser) findStartNodes(pattern *GraphPattern) ([]string, error) {
	if pattern.StartNode == nil || !pattern.StartNode.Matches() {
		// No start constraint - this is not a valid traversal
		return nil, nil
	}

	return gt.db.GetNodesByPattern(pattern.StartNode)
}

// =============================================================================
// BFS Traversal State
// =============================================================================

// traversalNode represents a node being explored in BFS traversal.
type traversalNode struct {
	id           string
	depth        int
	stepIndex    int
	path         []string
	matchedEdges []EdgeMatch
	totalWeight  float64
}

// bfsTraverse performs BFS traversal from a single start node.
// W4P.34: Added convergence detection to prevent redundant processing
// when different paths lead to the same node (diamond patterns).
func (gt *GraphTraverser) bfsTraverse(ctx context.Context, startID string, pattern *GraphPattern, limit int) []GraphResult {
	if len(pattern.Traversals) == 0 {
		return []GraphResult{{ID: startID, Path: []string{startID}, Score: 1.0}}
	}

	var results []GraphResult
	visited := make(map[string]bool)
	expanded := make(map[string]bool) // W4P.34: Global tracking for convergence
	queue := list.New()

	queue.PushBack(traversalNode{
		id: startID, depth: 0, stepIndex: 0, path: []string{startID},
	})

	for queue.Len() > 0 && len(results) < limit {
		if ctx.Err() != nil {
			return results
		}

		curr := queue.Remove(queue.Front()).(traversalNode)

		if curr.stepIndex >= len(pattern.Traversals) {
			results = append(results, gt.createResult(curr))
			continue
		}

		step := pattern.Traversals[curr.stepIndex]
		maxHops := gt.resolveMaxHops(step.MaxHops)

		if curr.depth >= maxHops {
			queue.PushBack(gt.advanceStep(curr))
			continue
		}

		// W4P.34: Skip expansion if this is a convergence point
		// The node was already expanded via a different path at this depth/step
		expandKey := gt.expandKey(curr.id, curr.stepIndex)
		if gt.config.ConvergenceDetection && expanded[expandKey] {
			continue
		}
		expanded[expandKey] = true

		gt.processNeighbors(curr, step, visited, queue)
	}

	return results
}

// expandKey creates a unique key for tracking which nodes have been expanded.
// W4P.34: Includes stepIndex to allow re-expansion at different traversal steps.
func (gt *GraphTraverser) expandKey(nodeID string, stepIndex int) string {
	return fmt.Sprintf("%s@%d", nodeID, stepIndex)
}

// resolveMaxHops normalizes the MaxHops value with sensible defaults.
func (gt *GraphTraverser) resolveMaxHops(maxHops int) int {
	if maxHops <= 0 {
		return 1
	}
	if maxHops == -1 {
		return 10
	}
	return maxHops
}

// advanceStep creates a new traversal node for the next pattern step.
func (gt *GraphTraverser) advanceStep(curr traversalNode) traversalNode {
	return traversalNode{
		id: curr.id, depth: 0, stepIndex: curr.stepIndex + 1,
		path: curr.path, matchedEdges: curr.matchedEdges, totalWeight: curr.totalWeight,
	}
}

// processNeighbors expands neighbors and adds valid ones to the queue.
func (gt *GraphTraverser) processNeighbors(
	curr traversalNode, step TraversalStep,
	visited map[string]bool, queue *list.List,
) {
	neighbors := gt.expandNeighbors(curr.id, step, visited)

	for _, neighbor := range neighbors {
		if gt.shouldSkipNeighbor(neighbor.id, curr.path, visited) {
			continue
		}
		queue.PushBack(gt.createNextNode(curr, neighbor))
	}
}

// shouldSkipNeighbor checks if a neighbor should be skipped during traversal.
// Returns true for cycles, path length violations, or already visited paths.
func (gt *GraphTraverser) shouldSkipNeighbor(
	neighborID string, path []string, visited map[string]bool,
) bool {
	// Cycle detection: skip if already in current path
	if gt.isInPath(neighborID, path) {
		return true
	}

	// Path length limit to prevent exponential explosion
	if len(path) >= gt.config.MaxPathLength {
		return true
	}

	// Path-based visit tracking for cycle detection
	visitKey := gt.visitKey(path, neighborID)
	if visited[visitKey] {
		return true
	}
	visited[visitKey] = true

	return false
}

// createNextNode creates a new traversal node for a neighbor.
func (gt *GraphTraverser) createNextNode(curr traversalNode, neighbor expandNeighbor) traversalNode {
	newPath := make([]string, len(curr.path), len(curr.path)+1)
	copy(newPath, curr.path)
	newPath = append(newPath, neighbor.id)

	newEdges := make([]EdgeMatch, len(curr.matchedEdges), len(curr.matchedEdges)+1)
	copy(newEdges, curr.matchedEdges)
	newEdges = append(newEdges, neighbor.edgeMatch)

	return traversalNode{
		id: neighbor.id, depth: curr.depth + 1, stepIndex: curr.stepIndex,
		path: newPath, matchedEdges: newEdges,
		totalWeight: curr.totalWeight + neighbor.edgeMatch.Weight,
	}
}

// expandNeighbor represents a neighbor found during BFS expansion.
type expandNeighbor struct {
	id        string
	edgeMatch EdgeMatch
}

// expandNeighbors finds all neighbors from a node based on the traversal step.
func (gt *GraphTraverser) expandNeighbors(nodeID string, step TraversalStep, visited map[string]bool) []expandNeighbor {
	var neighbors []expandNeighbor

	edgeTypes := gt.getEdgeTypes(step)

	switch step.Direction {
	case DirectionOutgoing:
		neighbors = gt.getOutgoingNeighbors(nodeID, edgeTypes)
	case DirectionIncoming:
		neighbors = gt.getIncomingNeighbors(nodeID, edgeTypes)
	case DirectionBoth:
		outgoing := gt.getOutgoingNeighbors(nodeID, edgeTypes)
		incoming := gt.getIncomingNeighbors(nodeID, edgeTypes)
		neighbors = append(outgoing, incoming...)
	}

	return neighbors
}

// getEdgeTypes extracts edge type filter from traversal step.
func (gt *GraphTraverser) getEdgeTypes(step TraversalStep) []string {
	if step.EdgeType == "" {
		return nil
	}
	return []string{step.EdgeType}
}

// getOutgoingNeighbors retrieves neighbors via outgoing edges.
func (gt *GraphTraverser) getOutgoingNeighbors(nodeID string, edgeTypes []string) []expandNeighbor {
	edges, err := gt.db.GetOutgoingEdges(nodeID, edgeTypes)
	if err != nil {
		return nil
	}

	neighbors := make([]expandNeighbor, 0, len(edges))
	for _, edge := range edges {
		neighbors = append(neighbors, expandNeighbor{
			id: edge.TargetID,
			edgeMatch: EdgeMatch{
				EdgeID:   edge.ID,
				EdgeType: edge.EdgeType,
				Weight:   edge.Weight,
			},
		})
	}
	return neighbors
}

// getIncomingNeighbors retrieves neighbors via incoming edges.
func (gt *GraphTraverser) getIncomingNeighbors(nodeID string, edgeTypes []string) []expandNeighbor {
	edges, err := gt.db.GetIncomingEdges(nodeID, edgeTypes)
	if err != nil {
		return nil
	}

	neighbors := make([]expandNeighbor, 0, len(edges))
	for _, edge := range edges {
		neighbors = append(neighbors, expandNeighbor{
			id: edge.SourceID,
			edgeMatch: EdgeMatch{
				EdgeID:   edge.ID,
				EdgeType: edge.EdgeType,
				Weight:   edge.Weight,
			},
		})
	}
	return neighbors
}

// isInPath checks if a node ID is already in the current path (cycle detection).
func (gt *GraphTraverser) isInPath(nodeID string, path []string) bool {
	for _, id := range path {
		if id == nodeID {
			return true
		}
	}
	return false
}

// visitKey creates a unique key for visited state based on path.
// PF.4.6: Fixed to include path information for proper cycle detection.
//
// When SimpleVisit is true, uses only nodeID (faster but may revisit nodes
// via different paths). When false, includes path context using a hash
// for efficient storage while detecting true cycles across path branches.
func (gt *GraphTraverser) visitKey(path []string, nextID string) string {
	// Simple mode: just use nodeID (faster, prevents any revisit)
	if gt.config.SimpleVisit {
		return nextID
	}

	// Full cycle detection mode: include path context
	// Use FNV-1a hash for efficiency - combines path with nextID
	h := fnv.New64a()
	for _, p := range path {
		h.Write([]byte(p))
		h.Write([]byte{0}) // null byte separator to avoid collisions
	}
	h.Write([]byte(nextID))

	// Return nodeID:hash format for debuggability
	return fmt.Sprintf("%s:%x", nextID, h.Sum64())
}

// hashPath computes a hash of the path for use in path-based visited tracking.
func hashPath(path []string) uint64 {
	h := fnv.New64a()
	for _, p := range path {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// createResult converts a traversal node into a GraphResult.
func (gt *GraphTraverser) createResult(node traversalNode) GraphResult {
	// Calculate score based on path length and edge weights
	score := gt.calculateScore(node)

	return GraphResult{
		ID:           node.id,
		Path:         node.path,
		MatchedEdges: node.matchedEdges,
		Score:        score,
	}
}

// calculateScore computes a relevance score for a traversal result.
// Shorter paths and higher edge weights result in higher scores.
func (gt *GraphTraverser) calculateScore(node traversalNode) float64 {
	pathLength := len(node.path)
	if pathLength == 0 {
		return 0
	}

	// Base score decreases with path length
	lengthScore := 1.0 / float64(pathLength)

	// Add weight bonus
	weightBonus := 0.0
	if len(node.matchedEdges) > 0 {
		weightBonus = node.totalWeight / float64(len(node.matchedEdges))
	}

	// Combine scores (length is primary, weight is secondary)
	return lengthScore*0.7 + weightBonus*0.3
}

// IsReady returns true if the traverser has a valid edge querier.
func (gt *GraphTraverser) IsReady() bool {
	return gt.db != nil
}

// Config returns the current traverser configuration.
func (gt *GraphTraverser) Config() TraverserConfig {
	return gt.config
}
