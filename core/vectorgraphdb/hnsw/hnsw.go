// Package hnsw implements the Hierarchical Navigable Small World (HNSW) algorithm
// for approximate nearest neighbor search. HNSW builds a multi-layer graph where
// higher layers contain fewer nodes with longer-range connections for fast traversal,
// while lower layers provide fine-grained navigation to the nearest neighbors.
//
// Key algorithm concepts:
//   - Multi-layer structure: Nodes exist at a random level L, appearing in layers 0 through L
//   - Greedy search: Starting from the entry point, traverse layers from top to bottom
//   - Neighbor selection: Use heuristic to maintain diverse, high-quality connections
//   - M parameter: Maximum number of connections per node (M*2 for layer 0)
//   - efConstruct: Size of dynamic candidate list during insertion
//   - efSearch: Size of dynamic candidate list during search
//
// Reference: "Efficient and robust approximate nearest neighbor search using
// Hierarchical Navigable Small World graphs" (Malkov & Yashunin, 2016)
package hnsw

import (
	"container/heap"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
	"sync"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// magnitudeTolerance defines the relative tolerance for magnitude validation.
// A cached magnitude is considered stale if it differs from the recomputed
// value by more than this fraction of the recomputed magnitude.
// W4P.24: Used for detecting stale magnitude cache entries.
const magnitudeTolerance = 1e-6

// Sentinel errors for HNSW operations. Use errors.Is() to check error types.
// W4L.1: Improved error definitions with clearer documentation.
var (
	// ErrNodeNotFound indicates the requested node ID does not exist in the index.
	ErrNodeNotFound = errors.New("hnsw: node not found")

	// ErrEmptyVector indicates an attempt to insert or search with a zero-length vector.
	ErrEmptyVector = errors.New("hnsw: vector cannot be empty")

	// ErrDimensionMismatch indicates the vector dimension doesn't match the index dimension.
	ErrDimensionMismatch = errors.New("hnsw: vector dimension mismatch")

	// ErrIndexEmpty indicates the index has no nodes to search.
	ErrIndexEmpty = errors.New("hnsw: index is empty")
)

type SearchResult struct {
	ID         string
	Similarity float64
	Domain     vectorgraphdb.Domain
	NodeType   vectorgraphdb.NodeType
}

// DomainFilterMode controls how missing domains are handled during filtering.
type DomainFilterMode int

const (
	// DomainFilterStrict excludes nodes with missing domain metadata from results.
	// Use this when domain filtering must be exact and missing data indicates a problem.
	DomainFilterStrict DomainFilterMode = iota

	// DomainFilterLenient includes nodes with missing domain metadata in results.
	// A warning is logged when a node's domain is not found.
	// Use this when missing domain data is acceptable but should be noted.
	DomainFilterLenient
)

type SearchFilter struct {
	Domains          []vectorgraphdb.Domain
	NodeTypes        []vectorgraphdb.NodeType
	MinSimilarity    float64
	DomainFilterMode DomainFilterMode // Default: DomainFilterStrict (zero value)
}

type Index struct {
	mu          sync.RWMutex
	layers      []*layer
	vectors     map[string][]float32
	magnitudes  map[string]float64
	domains     map[string]vectorgraphdb.Domain
	nodeTypes   map[string]vectorgraphdb.NodeType
	nodeLevels  map[string]int // W4P.32: Track assigned level for each node
	M           int
	efConstruct int
	efSearch    int
	levelMult   float64
	maxLevel    int
	entryPoint  string
	dimension   int
	finger      *FINGERAccelerator // Optional FINGER accelerator for candidate skipping
}

type Config struct {
	M           int
	EfConstruct int
	EfSearch    int
	LevelMult   float64
	Dimension   int
}

func DefaultConfig() Config {
	return Config{
		M:           vectorgraphdb.DefaultM,
		EfConstruct: vectorgraphdb.DefaultEfConstruct,
		EfSearch:    vectorgraphdb.DefaultEfSearch,
		LevelMult:   vectorgraphdb.DefaultLevelMult,
		Dimension:   0,
	}
}

func New(cfg Config) *Index {
	return &Index{
		layers:      make([]*layer, 0),
		vectors:     make(map[string][]float32),
		magnitudes:  make(map[string]float64),
		domains:     make(map[string]vectorgraphdb.Domain),
		nodeTypes:   make(map[string]vectorgraphdb.NodeType),
		nodeLevels:  make(map[string]int), // W4P.32: Initialize node level tracking
		M:           cfg.M,
		efConstruct: cfg.EfConstruct,
		efSearch:    cfg.EfSearch,
		levelMult:   cfg.LevelMult,
		dimension:   cfg.Dimension,
		maxLevel:    -1,
	}
}

// Insert adds a new vector to the index with the given ID, domain, and node type.
// W4L.1: Improved error messages with context for debugging.
//
// The vector will be connected to its nearest neighbors at each layer it exists in,
// where the node's maximum layer is determined randomly based on levelMult.
//
// Returns ErrEmptyVector if the vector is empty, or ErrDimensionMismatch if the
// vector dimension doesn't match the index's established dimension.
func (h *Index) Insert(id string, vector []float32, domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType) error {
	if len(vector) == 0 {
		return fmt.Errorf("%w: node %q cannot have empty vector", ErrEmptyVector, id)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.dimension == 0 {
		h.dimension = len(vector)
	} else if len(vector) != h.dimension {
		return fmt.Errorf("%w: node %q has dimension %d, expected %d",
			ErrDimensionMismatch, id, len(vector), h.dimension)
	}

	return h.insertLocked(id, vector, domain, nodeType)
}

func (h *Index) insertLocked(id string, vector []float32, domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType) error {
	mag := Magnitude(vector)

	// W4P.32: Check if this is an update (node already exists)
	existingLevel, isUpdate := h.nodeLevels[id]

	// Synchronize metadata atomically
	h.vectors[id] = vector
	h.magnitudes[id] = mag
	h.domains[id] = domain
	h.nodeTypes[id] = nodeType

	// W4P.32: For updates, preserve existing layer membership
	nodeLevel := existingLevel
	if !isUpdate {
		nodeLevel = randomLevel(h.levelMult)
	}
	h.nodeLevels[id] = nodeLevel
	h.ensureLayers(nodeLevel)

	if h.entryPoint == "" {
		h.initializeFirstNode(id, nodeLevel)
		return nil
	}

	// W4P.32: Skip layer operations for updates - node already has connections
	if !isUpdate {
		h.insertWithConnections(id, vector, mag, nodeLevel)
	}
	return nil
}

func (h *Index) ensureLayers(level int) {
	for len(h.layers) <= level {
		h.layers = append(h.layers, newLayer())
	}
}

func (h *Index) initializeFirstNode(id string, level int) {
	h.entryPoint = id
	h.maxLevel = level
	for l := range level + 1 {
		h.layers[l].addNode(id)
	}
}

func (h *Index) insertWithConnections(id string, vector []float32, mag float64, nodeLevel int) {
	currObj := h.entryPoint

	// Bounds check for entry point vector and magnitude
	epVec, epMag, ok := h.getVectorAndMagnitude(currObj)
	if !ok {
		// Entry point missing data - this shouldn't happen in normal operation
		// but we handle it gracefully by skipping upper layer traversal
		epVec = vector
		epMag = mag
	}
	currDist := CosineDistance(vector, epVec, mag, epMag)

	// W12.11: Bounds check for layer access during upper level traversal
	for level := h.maxLevel; level > nodeLevel; level-- {
		if !h.isValidLayerIndex(level) {
			continue
		}
		currObj, currDist = h.greedySearchLayer(vector, mag, currObj, currDist, level)
	}

	// W12.11: Bounds check for layer access during connection phase
	for level := min(nodeLevel, h.maxLevel); level >= 0; level-- {
		if !h.isValidLayerIndex(level) {
			continue
		}
		h.layers[level].addNode(id)
		neighbors := h.searchLayer(vector, mag, currObj, h.efConstruct, level)
		h.connectNode(id, neighbors, level)
		// W12.11: Validate first neighbor before using it
		if len(neighbors) > 0 && h.isValidNeighbor(neighbors[0].ID) {
			currObj = neighbors[0].ID
			// Use similarity from search result to compute distance
			// (already using shared CosineDistance via CosineSimilarity)
			currDist = 1.0 - neighbors[0].Similarity
		}
	}

	if nodeLevel > h.maxLevel {
		h.maxLevel = nodeLevel
		h.entryPoint = id
	}
}

// isValidLayerIndex checks if a layer index is within bounds.
// W12.11: Prevents panic on invalid layer access.
// Caller must hold h.mu (read or write lock).
func (h *Index) isValidLayerIndex(level int) bool {
	return level >= 0 && level < len(h.layers)
}

// greedySearchLayer performs greedy search at a single layer to find the closest node.
// W12.36: Caller must hold h.mu (read or write lock) to safely access h.vectors,
// h.magnitudes, and h.layers. This function does not acquire locks itself to avoid
// deadlocks when called from insertLocked (which holds write lock).
func (h *Index) greedySearchLayer(query []float32, queryMag float64, ep string, epDist float64, level int) (string, float64) {
	// W12.11: Bounds check for layer access
	if !h.isValidLayerIndex(level) {
		return ep, epDist
	}

	changed := true
	for changed {
		changed = false
		neighbors := h.layers[level].getNeighbors(ep)
		for _, neighbor := range neighbors {
			// Bounds check for both vector and magnitude
			vec, mag, exists := h.getVectorAndMagnitude(neighbor)
			if !exists {
				continue
			}
			dist := CosineDistance(query, vec, queryMag, mag)
			if dist < epDist {
				ep = neighbor
				epDist = dist
				changed = true
			}
		}
	}
	return ep, epDist
}

type searchCandidate struct {
	id       string
	distance float64
}

// candidateMinHeap: min-heap by distance (closest first) for candidate expansion
type candidateMinHeap []searchCandidate

func (h candidateMinHeap) Len() int           { return len(h) }
func (h candidateMinHeap) Less(i, j int) bool { return h[i].distance < h[j].distance }
func (h candidateMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *candidateMinHeap) Push(x any)        { *h = append(*h, x.(searchCandidate)) }
func (h *candidateMinHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// candidateMaxHeap: max-heap by distance (furthest first) for result eviction
type candidateMaxHeap []searchCandidate

func (h candidateMaxHeap) Len() int           { return len(h) }
func (h candidateMaxHeap) Less(i, j int) bool { return h[i].distance > h[j].distance }
func (h candidateMaxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *candidateMaxHeap) Push(x any)        { *h = append(*h, x.(searchCandidate)) }
func (h *candidateMaxHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// searchLayer implements HNSW Algorithm 2: SEARCH-LAYER from Malkov & Yashunin (2016).
// Uses dual-heap approach: min-heap for candidate expansion, max-heap for result eviction.
// Includes patience-based early termination via AdaptiveSearchConfig.
func (h *Index) searchLayer(query []float32, queryMag float64, ep string, ef int, level int) []SearchResult {
	if !h.isValidLayerIndex(level) {
		return nil
	}

	visited := make(map[string]bool)
	visited[ep] = true

	epVec, epMag, ok := h.getVectorAndMagnitude(ep)
	if !ok {
		return nil
	}
	epDist := CosineDistance(query, epVec, queryMag, epMag)

	candidates := &candidateMinHeap{{id: ep, distance: epDist}}
	heap.Init(candidates)

	results := &candidateMaxHeap{{id: ep, distance: epDist}}
	heap.Init(results)

	adaptiveCfg := NewAdaptiveSearchConfig(ef)
	stableCount := 0
	lastBestDist := epDist

	for candidates.Len() > 0 {
		closest := heap.Pop(candidates).(searchCandidate)
		furthest := (*results)[0]

		if closest.distance > furthest.distance {
			break
		}

		// Track best distance improvement for this iteration
		iterationBestDist := lastBestDist

		neighbors := h.layers[level].getNeighbors(closest.id)
		for _, neighborID := range neighbors {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true

			if h.finger != nil && h.finger.HasProjectionMatrix() {
				currentBestSim := 1.0 - (*results)[0].distance
				if h.finger.ShouldSkipCandidate(query, currentBestSim, closest.id, neighborID) {
					continue
				}
			}

			vec, mag, exists := h.getVectorAndMagnitude(neighborID)
			if !exists {
				continue
			}

			neighborDist := CosineDistance(query, vec, queryMag, mag)
			furthest = (*results)[0]

			if neighborDist < furthest.distance || results.Len() < ef {
				heap.Push(candidates, searchCandidate{id: neighborID, distance: neighborDist})
				heap.Push(results, searchCandidate{id: neighborID, distance: neighborDist})

				if results.Len() > ef {
					heap.Pop(results)
				}

				// Track if we found a better (smaller) distance
				if neighborDist < iterationBestDist {
					iterationBestDist = neighborDist
				}
			}
		}

		improvement := adaptiveCfg.ComputeImprovement(iterationBestDist, lastBestDist)
		significantlyImproved := improvement < 0 && adaptiveCfg.IsSignificantImprovement(-improvement)

		if significantlyImproved {
			stableCount = 0
			lastBestDist = iterationBestDist
		} else {
			stableCount++
		}

		if adaptiveCfg.ShouldTerminate(stableCount, results.Len(), iterationBestDist, lastBestDist) {
			break
		}
	}

	output := make([]SearchResult, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		c := heap.Pop(results).(searchCandidate)
		output[i] = SearchResult{
			ID:         c.id,
			Similarity: 1.0 - c.distance,
		}
	}

	sort.Slice(output, func(i, j int) bool {
		if output[i].Similarity != output[j].Similarity {
			return output[i].Similarity > output[j].Similarity
		}
		return output[i].ID < output[j].ID
	})

	return output
}

// getVectorAndMagnitude safely retrieves both vector and magnitude for a node.
// W4P.24: Validates that cached magnitude matches the vector, recomputes if stale.
// This function does NOT update the cache to avoid data races during read operations.
// Returns false if the vector is missing.
func (h *Index) getVectorAndMagnitude(id string) ([]float32, float64, bool) {
	vec, vecExists := h.vectors[id]
	if !vecExists {
		return nil, 0, false
	}
	mag, magExists := h.magnitudes[id]
	if !magExists {
		// No cached magnitude - compute it (don't cache during read to avoid race)
		mag = Magnitude(vec)
		slog.Warn("magnitude cache miss, computed on read",
			slog.String("node_id", id),
			slog.Float64("magnitude", mag))
		return vec, mag, true
	}
	// Validate cached magnitude against vector
	return validateAndReturnMagnitude(id, vec, mag)
}

// validateAndReturnMagnitude checks if cached magnitude is valid for the vector.
// W4P.24: Recomputes magnitude if mismatch detected, logs warning.
// Does NOT update cache to avoid data races during read operations.
func validateAndReturnMagnitude(id string, vec []float32, cachedMag float64) ([]float32, float64, bool) {
	computedMag := Magnitude(vec)
	if isMagnitudeValid(cachedMag, computedMag) {
		return vec, cachedMag, true
	}
	// Stale magnitude detected - log warning and return computed value
	// Note: We don't update the cache here to avoid write during read lock
	slog.Warn("stale magnitude cache detected, using computed value",
		slog.String("node_id", id),
		slog.Float64("cached_magnitude", cachedMag),
		slog.Float64("computed_magnitude", computedMag),
		slog.Float64("difference", math.Abs(cachedMag-computedMag)))
	return vec, computedMag, true
}

// isMagnitudeValid checks if cached magnitude matches computed magnitude within tolerance.
// W4P.24: Uses relative tolerance for floating-point comparison.
func isMagnitudeValid(cached, computed float64) bool {
	if computed == 0 {
		return cached == 0
	}
	relDiff := math.Abs(cached-computed) / computed
	return relDiff <= magnitudeTolerance
}

func (h *Index) connectNode(id string, neighbors []SearchResult, level int) {
	maxConn := h.M
	if level == 0 {
		maxConn = h.M * 2
	}

	for i := range min(len(neighbors), maxConn) {
		neighbor := neighbors[i]
		// W12.11: Bounds check - validate neighbor exists in index before connecting
		if !h.isValidNeighbor(neighbor.ID) {
			continue
		}
		// Convert similarity to distance (1 - similarity)
		distance := float32(1.0 - neighbor.Similarity)
		h.layers[level].addNeighbor(id, neighbor.ID, distance, maxConn)
		h.layers[level].addNeighbor(neighbor.ID, id, distance, maxConn)
	}
}

// isValidNeighbor checks if a neighbor ID is valid for connection.
// W12.11: Validates neighbor exists in the index to prevent corrupt connections.
// Caller must hold h.mu (read or write lock).
func (h *Index) isValidNeighbor(neighborID string) bool {
	_, exists := h.vectors[neighborID]
	return exists
}

func (h *Index) Search(query []float32, k int, filter *SearchFilter) []SearchResult {
	if len(query) == 0 || k <= 0 {
		return nil
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.entryPoint == "" {
		return nil
	}

	queryMag := Magnitude(query)
	if queryMag == 0 {
		return nil
	}

	return h.searchLocked(query, queryMag, k, filter)
}

func (h *Index) searchLocked(query []float32, queryMag float64, k int, filter *SearchFilter) []SearchResult {
	currObj := h.entryPoint

	// Bounds check for entry point vector and magnitude
	epVec, epMag, ok := h.getVectorAndMagnitude(currObj)
	if !ok {
		// Entry point missing data - return empty results
		return nil
	}
	currDist := CosineDistance(query, epVec, queryMag, epMag)

	for level := h.maxLevel; level > 0; level-- {
		currObj, currDist = h.greedySearchLayer(query, queryMag, currObj, currDist, level)
	}

	candidates := h.searchLayer(query, queryMag, currObj, h.efSearch, 0)
	return h.filterAndLimit(candidates, k, filter)
}

func (h *Index) filterAndLimit(candidates []SearchResult, k int, filter *SearchFilter) []SearchResult {
	results := make([]SearchResult, 0, k)
	for _, c := range candidates {
		if !h.matchesFilter(c.ID, c.Similarity, filter) {
			continue
		}
		c.Domain = h.domains[c.ID]
		c.NodeType = h.nodeTypes[c.ID]
		results = append(results, c)
		if len(results) >= k {
			break
		}
	}
	return results
}

func (h *Index) matchesFilter(id string, similarity float64, filter *SearchFilter) bool {
	if filter == nil {
		return true
	}

	// Check minimum similarity threshold FIRST
	if filter.MinSimilarity > 0 && similarity < filter.MinSimilarity {
		return false // Reject if below threshold
	}

	// Check domain filter
	if len(filter.Domains) > 0 {
		domain, found := h.domains[id]
		if !found {
			// W4P.23: Handle missing domain based on filter mode
			if filter.DomainFilterMode == DomainFilterLenient {
				slog.Warn("domain not found for node during search filter",
					slog.String("node_id", id),
					slog.String("mode", "lenient"),
					slog.String("action", "including node in results"))
			} else {
				// Strict mode (default): exclude nodes with missing domains
				slog.Debug("domain not found for node during search filter",
					slog.String("node_id", id),
					slog.String("mode", "strict"),
					slog.String("action", "excluding node from results"))
				return false
			}
		} else if !slices.Contains(filter.Domains, domain) {
			return false
		}
	}

	// Check node type filter
	if len(filter.NodeTypes) > 0 {
		if !slices.Contains(filter.NodeTypes, h.nodeTypes[id]) {
			return false
		}
	}
	return true
}

// Delete removes a node from the index by ID.
// W4L.1: Improved error message with node context.
//
// This operation removes the node from all layers and updates neighbor connections.
// If the deleted node was the entry point, a new entry point is selected.
//
// Returns ErrNodeNotFound if the node doesn't exist.
func (h *Index) Delete(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.vectors[id]; !exists {
		return fmt.Errorf("%w: cannot delete node %q", ErrNodeNotFound, id)
	}

	h.deleteUnlocked(id)
	return nil
}

// DeleteBatch deletes multiple nodes with a single lock acquisition.
// W4L.1: Improved error message to identify which node was not found.
//
// This is more efficient than calling Delete() multiple times when
// deleting many nodes, as it avoids repeated lock/unlock overhead.
// All IDs are validated before any deletions occur, ensuring atomicity.
//
// Returns ErrNodeNotFound (with the specific ID) if any node doesn't exist.
func (h *Index) DeleteBatch(ids []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Validate all IDs exist first
	for _, id := range ids {
		if _, exists := h.vectors[id]; !exists {
			return fmt.Errorf("%w: cannot delete node %q in batch of %d nodes",
				ErrNodeNotFound, id, len(ids))
		}
	}

	// Process all deletions with single lock acquisition
	for _, id := range ids {
		h.deleteUnlocked(id)
	}
	return nil
}

// deleteUnlocked performs deletion without acquiring locks.
// W4P.26: Enhanced to validate and clean up ALL references to deleted node.
// W4P.32: Also removes node level tracking.
// Caller must hold h.mu.Lock().
func (h *Index) deleteUnlocked(id string) {
	// Process all layers to clean up references
	for _, l := range h.layers {
		h.cleanupNodeReferences(id, l)
	}

	// Remove from maps
	delete(h.vectors, id)
	delete(h.magnitudes, id)
	delete(h.domains, id)
	delete(h.nodeTypes, id)
	delete(h.nodeLevels, id) // W4P.32: Clean up level tracking

	// Update entry point if needed
	if h.entryPoint == id {
		h.selectNewEntryPoint()
	}
}

// cleanupNodeReferences removes all references to a deleted node from a layer.
// W4P.26: Uses reverse lookup to find ALL nodes pointing to the deleted node,
// ensuring no dangling references remain that could cause search failures.
func (h *Index) cleanupNodeReferences(id string, l *layer) {
	// Find all nodes that point to the deleted node (reverse lookup)
	pointingNodes := l.findNodesPointingTo(id)
	for _, nodeID := range pointingNodes {
		l.removeNeighbor(nodeID, id)
	}

	// Remove the node itself from the layer
	l.removeNode(id)
}

func (h *Index) selectNewEntryPoint() {
	h.entryPoint = ""
	for level := h.maxLevel; level >= 0; level-- {
		ids := h.layers[level].allNodeIDs()
		if len(ids) > 0 {
			h.entryPoint = ids[0]
			h.maxLevel = level
			return
		}
	}
	h.maxLevel = -1
}

func (h *Index) Size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.vectors)
}

func (h *Index) Contains(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, exists := h.vectors[id]
	return exists
}

// GetVector returns a copy of the vector for the given node ID.
// W4L.1: Improved error message with node context.
//
// Returns ErrNodeNotFound if the node doesn't exist.
func (h *Index) GetVector(id string) ([]float32, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	vec, exists := h.vectors[id]
	if !exists {
		return nil, fmt.Errorf("%w: cannot get vector for node %q", ErrNodeNotFound, id)
	}
	result := make([]float32, len(vec))
	copy(result, vec)
	return result, nil
}

// GetMetadata returns the domain and node type for the given node ID.
// W4L.1: Improved error message with node context.
//
// Returns ErrNodeNotFound if the node doesn't exist.
func (h *Index) GetMetadata(id string) (vectorgraphdb.Domain, vectorgraphdb.NodeType, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, exists := h.vectors[id]; !exists {
		return 0, 0, fmt.Errorf("%w: cannot get metadata for node %q", ErrNodeNotFound, id)
	}
	return h.domains[id], h.nodeTypes[id], nil
}

// GetNodeLevel returns the assigned level for the given node ID.
// W4P.32: Provides access to layer membership information.
//
// Returns ErrNodeNotFound if the node doesn't exist.
func (h *Index) GetNodeLevel(id string) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	level, exists := h.nodeLevels[id]
	if !exists {
		return -1, fmt.Errorf("%w: cannot get level for node %q", ErrNodeNotFound, id)
	}
	return level, nil
}

// ValidateMetadataConsistency checks that all metadata maps are consistent.
// W4P.32: Detects metadata inconsistencies where a node exists in vectors
// but is missing from other metadata maps or layer membership.
//
// Returns a list of inconsistency descriptions, empty if all is consistent.
func (h *Index) ValidateMetadataConsistency() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.validateMetadataLocked()
}

// validateMetadataLocked performs consistency validation without locking.
// W4P.32: Caller must hold at least a read lock.
func (h *Index) validateMetadataLocked() []string {
	var issues []string

	for id := range h.vectors {
		issues = h.checkNodeConsistency(id, issues)
	}
	return issues
}

// checkNodeConsistency validates a single node's metadata consistency.
// W4P.32: Verifies domain, nodeType, magnitude, and level tracking exist.
func (h *Index) checkNodeConsistency(id string, issues []string) []string {
	if _, ok := h.domains[id]; !ok {
		issues = append(issues, fmt.Sprintf("node %q: missing domain", id))
	}
	if _, ok := h.nodeTypes[id]; !ok {
		issues = append(issues, fmt.Sprintf("node %q: missing nodeType", id))
	}
	if _, ok := h.magnitudes[id]; !ok {
		issues = append(issues, fmt.Sprintf("node %q: missing magnitude", id))
	}
	if _, ok := h.nodeLevels[id]; !ok {
		issues = append(issues, fmt.Sprintf("node %q: missing level tracking", id))
	}
	return issues
}

// EnableFINGER creates and initializes a FINGER accelerator for candidate skipping.
// lowRank specifies the dimensionality of the low-rank approximation space.
// If lowRank <= 0, a default value of min(32, dimension/4) is used.
// Call PrecomputeFINGER after bulk insertions to build the projection matrix.
func (h *Index) EnableFINGER(lowRank int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.finger = NewFINGERAccelerator(h.dimension, lowRank)
}

// PrecomputeFINGER builds the FINGER projection matrix using current vectors and
// layer 0 neighbor relationships. This is an expensive operation that should be
// called after bulk insertions, not after every single insert.
// Does nothing if FINGER is not enabled.
func (h *Index) PrecomputeFINGER() {
	h.mu.RLock()
	if h.finger == nil || len(h.layers) == 0 {
		h.mu.RUnlock()
		return
	}

	vectors := h.GetVectors()
	layer0 := h.layers[0]
	nodeIDs := layer0.allNodeIDs()
	neighbors := layer0.getNeighborsMany(nodeIDs)
	h.mu.RUnlock()

	h.finger.PrecomputeResiduals(vectors, neighbors)
}

// HasFINGER returns true if FINGER acceleration is enabled and has a projection matrix.
func (h *Index) HasFINGER() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.finger != nil && h.finger.HasProjectionMatrix()
}

// DisableFINGER removes the FINGER accelerator, disabling candidate skipping.
func (h *Index) DisableFINGER() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.finger = nil
}

func (h *Index) Stats() IndexStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return IndexStats{
		TotalNodes:  len(h.vectors),
		MaxLevel:    h.maxLevel,
		NumLayers:   len(h.layers),
		M:           h.M,
		EfConstruct: h.efConstruct,
		EfSearch:    h.efSearch,
	}
}

type IndexStats struct {
	TotalNodes  int
	MaxLevel    int
	NumLayers   int
	M           int
	EfConstruct int
	EfSearch    int
}

// RLock acquires a read lock on the index.
// Used by snapshot manager to ensure consistent reads.
func (h *Index) RLock() {
	h.mu.RLock()
}

// RUnlock releases the read lock on the index.
func (h *Index) RUnlock() {
	h.mu.RUnlock()
}

// GetLayers returns a copy of the layers slice for snapshot creation.
// W4N.13: Returns a shallow copy of the layers slice to protect internal state.
// Note: The layer objects themselves are not cloned for performance reasons.
// Caller must hold at least a read lock.
func (h *Index) GetLayers() []*layer {
	if h.layers == nil {
		return nil
	}
	result := make([]*layer, len(h.layers))
	copy(result, h.layers)
	return result
}

// GetVectors returns a deep copy of the vectors map for snapshot creation.
// W4N.13: Returns a deep copy to protect internal state from external modification.
// Caller must hold at least a read lock.
func (h *Index) GetVectors() map[string][]float32 {
	if h.vectors == nil {
		return nil
	}
	result := make(map[string][]float32, len(h.vectors))
	for id, vec := range h.vectors {
		vecCopy := make([]float32, len(vec))
		copy(vecCopy, vec)
		result[id] = vecCopy
	}
	return result
}

// GetMagnitudes returns a copy of the magnitudes map for snapshot creation.
// W4N.13: Returns a copy to protect internal state from external modification.
// Caller must hold at least a read lock.
func (h *Index) GetMagnitudes() map[string]float64 {
	if h.magnitudes == nil {
		return nil
	}
	result := make(map[string]float64, len(h.magnitudes))
	for id, mag := range h.magnitudes {
		result[id] = mag
	}
	return result
}

// GetEntryPoint returns the current entry point node ID.
// Caller must hold at least a read lock.
func (h *Index) GetEntryPoint() string {
	return h.entryPoint
}

// GetMaxLevel returns the current maximum level of the index.
// Caller must hold at least a read lock.
func (h *Index) GetMaxLevel() int {
	return h.maxLevel
}

// GetDomains returns a copy of the domains map for snapshot creation.
// W4N.13: Returns a copy to protect internal state from external modification.
// Caller must hold at least a read lock.
func (h *Index) GetDomains() map[string]vectorgraphdb.Domain {
	if h.domains == nil {
		return nil
	}
	result := make(map[string]vectorgraphdb.Domain, len(h.domains))
	for id, domain := range h.domains {
		result[id] = domain
	}
	return result
}

// GetNodeTypes returns a copy of the node types map for snapshot creation.
// W4N.13: Returns a copy to protect internal state from external modification.
// Caller must hold at least a read lock.
func (h *Index) GetNodeTypes() map[string]vectorgraphdb.NodeType {
	if h.nodeTypes == nil {
		return nil
	}
	result := make(map[string]vectorgraphdb.NodeType, len(h.nodeTypes))
	for id, nodeType := range h.nodeTypes {
		result[id] = nodeType
	}
	return result
}

// GetNodeLevels returns a copy of the node levels map for snapshot creation.
// W4P.32: Returns a copy to protect internal state from external modification.
// Caller must hold at least a read lock.
func (h *Index) GetNodeLevels() map[string]int {
	if h.nodeLevels == nil {
		return nil
	}
	result := make(map[string]int, len(h.nodeLevels))
	for id, level := range h.nodeLevels {
		result[id] = level
	}
	return result
}
