package hnsw

import (
	"log/slog"
	"slices"
	"sort"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// Search performs k-nearest neighbor search on the frozen snapshot.
// The snapshot is immutable, so no locks are required.
// Returns nil if the snapshot is empty, k <= 0, or query is invalid.
func (snap *HNSWSnapshot) Search(query []float32, k int, filter *SearchFilter) []SearchResult {
	if !snap.isValidSearchInput(query, k) {
		return nil
	}
	queryMag := Magnitude(query)
	if queryMag == 0 {
		return nil
	}
	return snap.searchFromEntry(query, queryMag, k, filter)
}

// isValidSearchInput checks if the search parameters are valid.
func (snap *HNSWSnapshot) isValidSearchInput(query []float32, k int) bool {
	if snap.EntryPoint == "" {
		return false
	}
	if len(query) == 0 || k <= 0 {
		return false
	}
	return true
}

// searchFromEntry navigates from the entry point to find k nearest neighbors.
// Validates entry point vector exists at each layer and handles missing entries gracefully.
func (snap *HNSWSnapshot) searchFromEntry(query []float32, queryMag float64, k int, filter *SearchFilter) []SearchResult {
	currentNode := snap.EntryPoint

	if !snap.hasValidEntryVector(currentNode) {
		slog.Debug("entry point vector missing from snapshot", slog.String("entry_point", currentNode))
		return nil
	}

	for level := snap.MaxLevel; level > 0; level-- {
		currentNode = snap.searchLayerWithValidation(query, queryMag, currentNode, level)
	}

	candidates := snap.searchLayer0(query, queryMag, currentNode, k, filter)
	return candidates
}

// hasValidEntryVector checks if a node has both vector and magnitude data.
func (snap *HNSWSnapshot) hasValidEntryVector(nodeID string) bool {
	_, hasVec := snap.Vectors[nodeID]
	_, hasMag := snap.Magnitudes[nodeID]
	return hasVec && hasMag
}

// searchLayerWithValidation performs greedy search with entry point validation.
// Returns the best found node, or the original entry if the layer is invalid.
func (snap *HNSWSnapshot) searchLayerWithValidation(query []float32, queryMag float64, entry string, level int) string {
	if !snap.isValidLayer(level) {
		return entry
	}

	if !snap.nodeExistsAtLayer(entry, level) {
		slog.Debug("entry point missing at layer",
			slog.String("entry_point", entry),
			slog.Int("layer", level))
		return entry
	}

	return snap.greedySearchLayer(query, queryMag, entry, level)
}

// isValidLayer checks if a layer index is within bounds.
func (snap *HNSWSnapshot) isValidLayer(level int) bool {
	return level >= 0 && level < len(snap.Layers)
}

// nodeExistsAtLayer checks if a node exists in the specified layer.
func (snap *HNSWSnapshot) nodeExistsAtLayer(nodeID string, level int) bool {
	if !snap.isValidLayer(level) {
		return false
	}
	_, exists := snap.Layers[level].Nodes[nodeID]
	return exists
}

// greedySearchLayer performs greedy search at a single layer to find the closest node.
func (snap *HNSWSnapshot) greedySearchLayer(query []float32, queryMag float64, entry string, level int) string {
	if level >= len(snap.Layers) {
		return entry
	}

	current := entry
	currentDist := snap.distance(query, queryMag, current)

	for {
		neighbors := snap.getLayerNeighbors(current, level)
		improved, newCurrent, newDist := snap.findCloserNeighbor(query, queryMag, neighbors, currentDist)
		if !improved {
			break
		}
		current = newCurrent
		currentDist = newDist
	}

	return current
}

// getLayerNeighbors returns the neighbors of a node at the given layer.
func (snap *HNSWSnapshot) getLayerNeighbors(nodeID string, level int) []string {
	if level >= len(snap.Layers) {
		return nil
	}
	neighbors, ok := snap.Layers[level].Nodes[nodeID]
	if !ok {
		return nil
	}
	return neighbors
}

// findCloserNeighbor finds a neighbor closer to the query than the current distance.
func (snap *HNSWSnapshot) findCloserNeighbor(query []float32, queryMag float64, neighbors []string, currentDist float64) (bool, string, float64) {
	for _, neighbor := range neighbors {
		dist := snap.distance(query, queryMag, neighbor)
		if dist < currentDist {
			return true, neighbor, dist
		}
	}
	return false, "", currentDist
}

// searchLayer0 performs beam search at layer 0 to find k nearest neighbors.
func (snap *HNSWSnapshot) searchLayer0(query []float32, queryMag float64, entry string, k int, filter *SearchFilter) []SearchResult {
	visited := make(map[string]bool)
	visited[entry] = true

	candidates := snap.initializeCandidates(query, queryMag, entry)
	candidates = snap.expandCandidates(query, queryMag, candidates, visited, k)

	return snap.filterAndLimit(candidates, k, filter)
}

// initializeCandidates creates the initial candidate list with the entry point.
func (snap *HNSWSnapshot) initializeCandidates(query []float32, queryMag float64, entry string) []SearchResult {
	vec, ok := snap.Vectors[entry]
	if !ok {
		return []SearchResult{}
	}
	mag, ok := snap.Magnitudes[entry]
	if !ok {
		return []SearchResult{}
	}
	sim := CosineSimilarity(query, vec, queryMag, mag)
	return []SearchResult{{ID: entry, Similarity: sim}}
}

// expandCandidates expands the search by exploring neighbors of candidates.
func (snap *HNSWSnapshot) expandCandidates(query []float32, queryMag float64, candidates []SearchResult, visited map[string]bool, k int) []SearchResult {
	efSearch := snap.getEffectiveEfSearch(k)

	for i := 0; i < len(candidates) && len(candidates) < efSearch*2; i++ {
		curr := candidates[i]
		neighbors := snap.getLayerNeighbors(curr.ID, 0)
		candidates = snap.processNeighbors(query, queryMag, neighbors, candidates, visited)
	}

	return snap.sortCandidates(candidates)
}

// getEffectiveEfSearch returns the efSearch value to use for search.
// Uses the stored EfSearch if set, otherwise falls back to max(k*2, DefaultEfSearch).
func (snap *HNSWSnapshot) getEffectiveEfSearch(k int) int {
	if snap.EfSearch > 0 {
		return snap.EfSearch
	}
	return max(k*2, vectorgraphdb.DefaultEfSearch)
}

// processNeighbors adds unvisited neighbors to the candidate list.
func (snap *HNSWSnapshot) processNeighbors(query []float32, queryMag float64, neighbors []string, candidates []SearchResult, visited map[string]bool) []SearchResult {
	for _, neighbor := range neighbors {
		if visited[neighbor] {
			continue
		}
		visited[neighbor] = true
		result := snap.createSearchResult(query, queryMag, neighbor)
		if result != nil {
			candidates = append(candidates, *result)
		}
	}
	return candidates
}

// createSearchResult creates a SearchResult for a node if its vector exists.
func (snap *HNSWSnapshot) createSearchResult(query []float32, queryMag float64, nodeID string) *SearchResult {
	vec, ok := snap.Vectors[nodeID]
	if !ok {
		return nil
	}
	mag, ok := snap.Magnitudes[nodeID]
	if !ok {
		return nil
	}
	sim := CosineSimilarity(query, vec, queryMag, mag)
	return &SearchResult{ID: nodeID, Similarity: sim}
}

// sortCandidates sorts candidates by similarity in descending order.
// Uses ID as a stable tie-breaker for deterministic ordering.
func (snap *HNSWSnapshot) sortCandidates(candidates []SearchResult) []SearchResult {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Similarity != candidates[j].Similarity {
			return candidates[i].Similarity > candidates[j].Similarity
		}
		// Stable tie-breaker: sort by ID for deterministic ordering
		return candidates[i].ID < candidates[j].ID
	})
	return candidates
}

// filterAndLimit applies the filter and limits results to k items.
func (snap *HNSWSnapshot) filterAndLimit(candidates []SearchResult, k int, filter *SearchFilter) []SearchResult {
	results := make([]SearchResult, 0, k)
	for _, c := range candidates {
		if !snap.matchesFilter(c.ID, c.Similarity, filter) {
			continue
		}
		c.Domain = snap.getDomain(c.ID)
		c.NodeType = snap.getNodeType(c.ID)
		results = append(results, c)
		if len(results) >= k {
			break
		}
	}
	return results
}

// matchesFilter checks if a node passes the search filter criteria.
func (snap *HNSWSnapshot) matchesFilter(id string, similarity float64, filter *SearchFilter) bool {
	if filter == nil {
		return true
	}
	if !snap.passesMinSimilarity(similarity, filter.MinSimilarity) {
		return false
	}
	if !snap.passesDomainFilter(id, filter.Domains) {
		return false
	}
	return snap.passesNodeTypeFilter(id, filter.NodeTypes)
}

// passesMinSimilarity checks if the similarity meets the minimum threshold.
func (snap *HNSWSnapshot) passesMinSimilarity(similarity, minSimilarity float64) bool {
	if minSimilarity <= 0 {
		return true
	}
	return similarity >= minSimilarity
}

// passesDomainFilter checks if the node's domain is in the allowed list.
func (snap *HNSWSnapshot) passesDomainFilter(id string, domains []vectorgraphdb.Domain) bool {
	if len(domains) == 0 {
		return true
	}
	return slices.Contains(domains, snap.getDomain(id))
}

// passesNodeTypeFilter checks if the node's type is in the allowed list.
func (snap *HNSWSnapshot) passesNodeTypeFilter(id string, nodeTypes []vectorgraphdb.NodeType) bool {
	if len(nodeTypes) == 0 {
		return true
	}
	return slices.Contains(nodeTypes, snap.getNodeType(id))
}

// getDomain returns the domain for a node ID, or default if not found.
func (snap *HNSWSnapshot) getDomain(id string) vectorgraphdb.Domain {
	if snap.Domains == nil {
		return vectorgraphdb.DomainCode
	}
	domain, ok := snap.Domains[id]
	if !ok {
		return vectorgraphdb.DomainCode
	}
	return domain
}

// getNodeType returns the node type for a node ID, or default if not found.
func (snap *HNSWSnapshot) getNodeType(id string) vectorgraphdb.NodeType {
	if snap.NodeTypes == nil {
		return vectorgraphdb.NodeTypeFile
	}
	nodeType, ok := snap.NodeTypes[id]
	if !ok {
		return vectorgraphdb.NodeTypeFile
	}
	return nodeType
}

// distance computes the cosine distance between query and a stored node.
// Uses the shared CosineDistance function to ensure consistency with live search.
// Returns 2.0 (maximum cosine distance) if the node's vector or magnitude is not found.
func (snap *HNSWSnapshot) distance(query []float32, queryMag float64, nodeID string) float64 {
	vec, ok := snap.Vectors[nodeID]
	if !ok {
		return 2.0 // max cosine distance when vector missing
	}
	mag, ok := snap.Magnitudes[nodeID]
	if !ok {
		return 2.0 // max cosine distance when magnitude missing
	}
	return CosineDistance(query, vec, queryMag, mag)
}
