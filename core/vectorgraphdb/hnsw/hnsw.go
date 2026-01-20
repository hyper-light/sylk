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
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

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

type SearchFilter struct {
	Domains       []vectorgraphdb.Domain
	NodeTypes     []vectorgraphdb.NodeType
	MinSimilarity float64
}

type Index struct {
	mu          sync.RWMutex
	layers      []*layer
	vectors     map[string][]float32
	magnitudes  map[string]float64
	domains     map[string]vectorgraphdb.Domain
	nodeTypes   map[string]vectorgraphdb.NodeType
	M           int
	efConstruct int
	efSearch    int
	levelMult   float64
	maxLevel    int
	entryPoint  string
	dimension   int
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
	h.vectors[id] = vector
	h.magnitudes[id] = mag
	h.domains[id] = domain
	h.nodeTypes[id] = nodeType

	nodeLevel := randomLevel(h.levelMult)
	h.ensureLayers(nodeLevel)

	if h.entryPoint == "" {
		h.initializeFirstNode(id, nodeLevel)
		return nil
	}

	h.insertWithConnections(id, vector, mag, nodeLevel)
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
	currDist := 1.0 - CosineSimilarity(vector, epVec, mag, epMag)

	for level := h.maxLevel; level > nodeLevel; level-- {
		currObj, currDist = h.greedySearchLayer(vector, mag, currObj, currDist, level)
	}

	for level := min(nodeLevel, h.maxLevel); level >= 0; level-- {
		h.layers[level].addNode(id)
		neighbors := h.searchLayer(vector, mag, currObj, h.efConstruct, level)
		h.connectNode(id, neighbors, level)
		if len(neighbors) > 0 {
			currObj = neighbors[0].ID
			currDist = 1.0 - neighbors[0].Similarity
		}
	}

	if nodeLevel > h.maxLevel {
		h.maxLevel = nodeLevel
		h.entryPoint = id
	}
}

func (h *Index) greedySearchLayer(query []float32, queryMag float64, ep string, epDist float64, level int) (string, float64) {
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
			dist := 1.0 - CosineSimilarity(query, vec, queryMag, mag)
			if dist < epDist {
				ep = neighbor
				epDist = dist
				changed = true
			}
		}
	}
	return ep, epDist
}

func (h *Index) searchLayer(query []float32, queryMag float64, ep string, ef int, level int) []SearchResult {
	visited := make(map[string]bool)
	visited[ep] = true

	// W4P.8: Pre-allocate candidates with maxCandidates capacity to avoid
	// reallocation during search. The slice can grow up to ef*2 during BFS
	// exploration, so we pre-allocate that capacity upfront.
	maxCandidates := ef * 2
	candidates := make([]SearchResult, 0, maxCandidates)

	// Bounds check for entry point vector and magnitude
	epVec, epMag, ok := h.getVectorAndMagnitude(ep)
	if !ok {
		return candidates
	}
	epSim := CosineSimilarity(query, epVec, queryMag, epMag)
	candidates = append(candidates, SearchResult{ID: ep, Similarity: epSim})

	// Use explicit index with proper bounds checking to prevent unbounded growth
	for i := 0; i < len(candidates) && len(candidates) < maxCandidates; i++ {
		curr := candidates[i]
		neighbors := h.layers[level].getNeighbors(curr.ID)

		for _, neighbor := range neighbors {
			if visited[neighbor] {
				continue
			}
			visited[neighbor] = true

			vec, mag, exists := h.getVectorAndMagnitude(neighbor)
			if !exists {
				continue
			}

			sim := CosineSimilarity(query, vec, queryMag, mag)

			// Only append if we haven't hit the limit
			if len(candidates) < maxCandidates {
				candidates = append(candidates, SearchResult{ID: neighbor, Similarity: sim})
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Similarity != candidates[j].Similarity {
			return candidates[i].Similarity > candidates[j].Similarity
		}
		// Stable tie-breaker: sort by ID for deterministic ordering
		return candidates[i].ID < candidates[j].ID
	})

	if len(candidates) > ef {
		candidates = candidates[:ef]
	}
	return candidates
}

// getVectorAndMagnitude safely retrieves both vector and magnitude for a node.
// Returns false if either is missing.
func (h *Index) getVectorAndMagnitude(id string) ([]float32, float64, bool) {
	vec, vecExists := h.vectors[id]
	if !vecExists {
		return nil, 0, false
	}
	mag, magExists := h.magnitudes[id]
	if !magExists {
		return nil, 0, false
	}
	return vec, mag, true
}

func (h *Index) connectNode(id string, neighbors []SearchResult, level int) {
	maxConn := h.M
	if level == 0 {
		maxConn = h.M * 2
	}

	for i := range min(len(neighbors), maxConn) {
		neighbor := neighbors[i]
		// Convert similarity to distance (1 - similarity)
		distance := float32(1.0 - neighbor.Similarity)
		h.layers[level].addNeighbor(id, neighbor.ID, distance, maxConn)
		h.layers[level].addNeighbor(neighbor.ID, id, distance, maxConn)
	}
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
	currDist := 1.0 - CosineSimilarity(query, epVec, queryMag, epMag)

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
		if !slices.Contains(filter.Domains, h.domains[id]) {
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
// Caller must hold h.mu.Lock().
func (h *Index) deleteUnlocked(id string) {
	// Collect all layers that have this node first (read phase)
	layersWithNode := make([]*layer, 0, len(h.layers))
	for _, l := range h.layers {
		if l.hasNode(id) {
			layersWithNode = append(layersWithNode, l)
		}
	}

	// Now process all layers in one pass (write phase)
	for _, l := range layersWithNode {
		h.removeNodeConnections(id, l)
		l.removeNode(id)
	}

	// Remove from maps
	delete(h.vectors, id)
	delete(h.magnitudes, id)
	delete(h.domains, id)
	delete(h.nodeTypes, id)

	// Update entry point if needed
	if h.entryPoint == id {
		h.selectNewEntryPoint()
	}
}

func (h *Index) removeNodeConnections(id string, l *layer) {
	neighbors := l.getNeighbors(id)
	for _, neighbor := range neighbors {
		l.removeNeighbor(neighbor, id)
	}
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
