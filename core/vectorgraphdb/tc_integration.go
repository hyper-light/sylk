// Package vectorgraphdb provides vector-enhanced graph database functionality.
package vectorgraphdb

import (
	"github.com/adalundhe/sylk/core/knowledge/relations"
)

// =============================================================================
// VTC.8 - Integration with HybridQueryCoordinator
// =============================================================================

// TCQueryEngine extends QueryEngine with transitive closure capabilities.
// It integrates VersionedIntervalIndex for O(1) reachability queries and
// lock-free concurrent access.
type TCQueryEngine struct {
	*QueryEngine
	tcIndex *relations.VersionedIntervalIndex
}

// NewTCQueryEngine creates a new TCQueryEngine with TC support.
// If tcIndex is nil, the engine will fall back to graph traversal for reachability.
func NewTCQueryEngine(
	db *VectorGraphDB,
	vs *VectorSearcher,
	gt *GraphTraverser,
	tcIndex *relations.VersionedIntervalIndex,
) *TCQueryEngine {
	return &TCQueryEngine{
		QueryEngine: NewQueryEngine(db, vs, gt),
		tcIndex:     tcIndex,
	}
}

// SetTCIndex updates the transitive closure index.
// This can be called after initialization to enable TC-based queries.
func (tce *TCQueryEngine) SetTCIndex(tcIndex *relations.VersionedIntervalIndex) {
	tce.tcIndex = tcIndex
}

// GetTCIndex returns the current transitive closure index.
func (tce *TCQueryEngine) GetTCIndex() *relations.VersionedIntervalIndex {
	return tce.tcIndex
}

// =============================================================================
// O(1) Reachability Checks
// =============================================================================

// IsReachable performs an O(1) reachability check using the interval index.
// Returns true if there is a path from source to target in the graph.
// If the TC index is not available, falls back to graph traversal.
func (tce *TCQueryEngine) IsReachable(source, target string) bool {
	if tce.tcIndex == nil {
		return tce.isReachableByTraversal(source, target)
	}
	return tce.tcIndex.IsReachable(source, target)
}

// IsReachableWithFallback checks reachability with explicit fallback control.
// If useFallback is true and TC lookup fails, it will use graph traversal.
func (tce *TCQueryEngine) IsReachableWithFallback(source, target string, useFallback bool) bool {
	if tce.tcIndex == nil {
		if useFallback {
			return tce.isReachableByTraversal(source, target)
		}
		return false
	}

	// Try TC index first
	if tce.tcIndex.IsReachable(source, target) {
		return true
	}

	// If TC says not reachable and fallback is enabled, verify with traversal
	// (TC interval index can have false negatives for non-tree edges in DAGs)
	if useFallback {
		return tce.isReachableByTraversal(source, target)
	}

	return false
}

// isReachableByTraversal falls back to graph traversal for reachability.
func (tce *TCQueryEngine) isReachableByTraversal(source, target string) bool {
	path, err := tce.gt.ShortestPath(source, target, &TraversalOptions{
		MaxDepth:  100,
		Direction: DirectionOutgoing,
	})
	return err == nil && path != nil && len(path.Nodes) > 0
}

// =============================================================================
// Multi-Hop Traversals
// =============================================================================

// GetAllReachable returns all nodes reachable from the source node.
// Uses the TC snapshot for O(1) lookup per reachable node.
// If the TC index is not available, falls back to graph traversal.
func (tce *TCQueryEngine) GetAllReachable(source string) []string {
	if tce.tcIndex == nil {
		return tce.getAllReachableByTraversal(source)
	}
	return tce.tcIndex.GetAllReachable(source)
}

// GetAllReachableNodes returns all nodes reachable from source as GraphNode objects.
// This combines TC reachability with node loading.
func (tce *TCQueryEngine) GetAllReachableNodes(source string) ([]*GraphNode, error) {
	reachableIDs := tce.GetAllReachable(source)
	if len(reachableIDs) == 0 {
		return nil, nil
	}

	// Use batch loading for efficiency
	ns := tce.getNodeStore()
	return ns.GetNodesBatchSlice(reachableIDs)
}

// getAllReachableByTraversal falls back to graph traversal.
func (tce *TCQueryEngine) getAllReachableByTraversal(source string) []string {
	component, err := tce.gt.GetConnectedComponent(source, &TraversalOptions{
		MaxDepth:  100,
		Direction: DirectionOutgoing,
	})
	if err != nil || len(component) == 0 {
		return nil
	}

	// Extract IDs, excluding the source
	var ids []string
	for _, node := range component {
		if node.ID != source {
			ids = append(ids, node.ID)
		}
	}
	return ids
}

// =============================================================================
// Consistent Batch Queries
// =============================================================================

// LookupInSnapshot performs a direct TC lookup using the current snapshot.
// This provides consistent reads for batch queries - all lookups in a batch
// will see the same version of the TC data.
func (tce *TCQueryEngine) LookupInSnapshot(source, target string) bool {
	if tce.tcIndex == nil {
		return tce.isReachableByTraversal(source, target)
	}
	return tce.tcIndex.LookupInSnapshot(source, target)
}

// BatchLookup performs multiple reachability lookups atomically.
// All lookups see a consistent snapshot of the TC index.
// Returns a map of "source->target" to boolean indicating reachability.
func (tce *TCQueryEngine) BatchLookup(pairs [][2]string) map[string]bool {
	results := make(map[string]bool, len(pairs))

	if tce.tcIndex == nil {
		// Fallback to individual traversals
		for _, pair := range pairs {
			key := pair[0] + "->" + pair[1]
			results[key] = tce.isReachableByTraversal(pair[0], pair[1])
		}
		return results
	}

	// Get consistent snapshot state
	snapshot, _, _ := tce.tcIndex.GetState()
	if snapshot == nil {
		// If no snapshot, use individual fallbacks
		for _, pair := range pairs {
			key := pair[0] + "->" + pair[1]
			results[key] = tce.isReachableByTraversal(pair[0], pair[1])
		}
		return results
	}

	// All lookups use the same snapshot (consistent view)
	for _, pair := range pairs {
		key := pair[0] + "->" + pair[1]
		results[key] = snapshot.Lookup(pair[0], pair[1])
	}

	return results
}

// =============================================================================
// Enhanced Hybrid Queries with TC
// =============================================================================

// TCHybridQueryOptions extends HybridQueryOptions with TC-specific settings.
type TCHybridQueryOptions struct {
	*HybridQueryOptions

	// UseTCReachability enables TC-based reachability filtering.
	// When true, results are filtered to only include nodes reachable
	// from seed nodes according to the TC index.
	UseTCReachability bool

	// TCSourceNode specifies the source node for TC reachability filtering.
	// Only nodes reachable from this source will be included in results.
	TCSourceNode string

	// BoostReachable increases the score of nodes that are TC-reachable.
	// The boost is multiplicative (e.g., 1.2 = 20% boost).
	BoostReachable float64
}

// TCHybridQuery performs a hybrid query with TC-enhanced scoring.
// Nodes that are reachable according to the TC index can receive a score boost.
func (tce *TCQueryEngine) TCHybridQuery(
	query []float32,
	seedNodes []string,
	opts *TCHybridQueryOptions,
) ([]HybridResult, error) {
	if opts == nil {
		opts = &TCHybridQueryOptions{
			HybridQueryOptions: &HybridQueryOptions{},
		}
	}
	if opts.HybridQueryOptions == nil {
		opts.HybridQueryOptions = &HybridQueryOptions{}
	}

	// Get base hybrid results
	results, err := tce.HybridQuery(query, seedNodes, opts.HybridQueryOptions)
	if err != nil {
		return nil, err
	}

	// Apply TC enhancements if enabled
	if opts.UseTCReachability && opts.TCSourceNode != "" && tce.tcIndex != nil {
		results = tce.applyTCEnhancements(results, opts)
	}

	return results, nil
}

// applyTCEnhancements filters and boosts results based on TC reachability.
func (tce *TCQueryEngine) applyTCEnhancements(
	results []HybridResult,
	opts *TCHybridQueryOptions,
) []HybridResult {
	var enhanced []HybridResult

	for _, r := range results {
		if r.Node == nil {
			continue
		}

		isReachable := tce.tcIndex.IsReachable(opts.TCSourceNode, r.Node.ID)

		if opts.UseTCReachability && !isReachable {
			// Filter out non-reachable nodes
			continue
		}

		// Apply boost to reachable nodes
		if isReachable && opts.BoostReachable > 0 {
			r.CombinedScore *= opts.BoostReachable
		}

		enhanced = append(enhanced, r)
	}

	return enhanced
}

// =============================================================================
// TC Index Lifecycle
// =============================================================================

// RebuildTCIndex rebuilds the TC index from the current graph state.
// This should be called after significant graph modifications.
func (tce *TCQueryEngine) RebuildTCIndex() error {
	if tce.tcIndex == nil {
		return nil
	}

	// Extract current graph structure
	graph, tcData, err := tce.extractGraphData()
	if err != nil {
		return err
	}

	return tce.tcIndex.Rebuild(graph, tcData)
}

// extractGraphData extracts the graph adjacency list and computes TC.
func (tce *TCQueryEngine) extractGraphData() (map[string][]string, map[string]map[string]bool, error) {
	graph := make(map[string][]string)
	tcData := make(map[string]map[string]bool)

	// This is a placeholder - in a real implementation, you would:
	// 1. Query all edges from the database
	// 2. Build the adjacency list
	// 3. Compute transitive closure

	// For now, return empty data (the caller should provide pre-computed data)
	return graph, tcData, nil
}

// GetTCVersion returns the current version of the TC index.
// Useful for detecting when the index has been updated.
func (tce *TCQueryEngine) GetTCVersion() uint64 {
	if tce.tcIndex == nil {
		return 0
	}
	return tce.tcIndex.Version()
}

// =============================================================================
// Utility Methods
// =============================================================================

// GetReachabilityStats returns statistics about reachability from a source.
func (tce *TCQueryEngine) GetReachabilityStats(source string) ReachabilityStats {
	stats := ReachabilityStats{
		Source: source,
	}

	if tce.tcIndex == nil {
		stats.Available = false
		return stats
	}

	stats.Available = true
	stats.Version = tce.tcIndex.Version()

	reachable := tce.tcIndex.GetAllReachable(source)
	stats.ReachableCount = len(reachable)

	return stats
}

// ReachabilityStats contains statistics about node reachability.
type ReachabilityStats struct {
	Source         string `json:"source"`
	Available      bool   `json:"available"`
	Version        uint64 `json:"version"`
	ReachableCount int    `json:"reachable_count"`
}
