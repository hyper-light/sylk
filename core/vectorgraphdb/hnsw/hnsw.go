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

var visitedPool = sync.Pool{
	New: func() any {
		return make(map[uint32]bool, 256)
	},
}

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

const invalidNodeID uint32 = 0

type Index struct {
	mu          sync.RWMutex
	layers      []*layer
	vectors     map[uint32][]float32
	magnitudes  map[uint32]float64
	domains     map[uint32]vectorgraphdb.Domain
	nodeTypes   map[uint32]vectorgraphdb.NodeType
	nodeLevels  map[uint32]int
	stringToID  map[string]uint32
	idToString  []string
	nextID      uint32
	M           int
	efConstruct int
	efSearch    int
	levelMult   float64
	maxLevel    int
	entryPoint  uint32
	dimension   int
	finger      *FINGERAccelerator
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
		vectors:     make(map[uint32][]float32),
		magnitudes:  make(map[uint32]float64),
		domains:     make(map[uint32]vectorgraphdb.Domain),
		nodeTypes:   make(map[uint32]vectorgraphdb.NodeType),
		nodeLevels:  make(map[uint32]int),
		stringToID:  make(map[string]uint32),
		idToString:  []string{""},
		nextID:      1,
		M:           cfg.M,
		efConstruct: cfg.EfConstruct,
		efSearch:    cfg.EfSearch,
		levelMult:   cfg.LevelMult,
		dimension:   cfg.Dimension,
		maxLevel:    -1,
		entryPoint:  invalidNodeID,
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

	internalID, isUpdate := h.stringToID[id]
	if !isUpdate {
		internalID = h.nextID
		h.nextID++
		h.stringToID[id] = internalID
		h.idToString = append(h.idToString, id)
	}

	existingLevel := h.nodeLevels[internalID]

	h.vectors[internalID] = vector
	h.magnitudes[internalID] = mag
	h.domains[internalID] = domain
	h.nodeTypes[internalID] = nodeType

	nodeLevel := existingLevel
	if !isUpdate {
		nodeLevel = randomLevel(h.levelMult)
	}
	h.nodeLevels[internalID] = nodeLevel
	h.ensureLayers(nodeLevel)

	if h.entryPoint == invalidNodeID {
		h.initializeFirstNode(internalID, nodeLevel)
		return nil
	}

	if !isUpdate {
		h.insertWithConnections(internalID, vector, mag, nodeLevel)
	}
	return nil
}

func (h *Index) ensureLayers(level int) {
	for len(h.layers) <= level {
		h.layers = append(h.layers, newLayer())
	}
}

func (h *Index) initializeFirstNode(id uint32, level int) {
	h.entryPoint = id
	h.maxLevel = level
	for l := range level + 1 {
		h.layers[l].addNode(id)
	}
}

func (h *Index) insertWithConnections(id uint32, vector []float32, mag float64, nodeLevel int) {
	currObj := h.entryPoint

	epVec, epMag, ok := h.getVectorAndMagnitude(currObj)
	if !ok {
		epVec = vector
		epMag = mag
	}
	currDist := CosineDistance(vector, epVec, mag, epMag)

	for level := h.maxLevel; level > nodeLevel; level-- {
		if !h.isValidLayerIndex(level) {
			continue
		}
		currObj, currDist = h.greedySearchLayer(vector, mag, currObj, currDist, level)
	}

	for level := min(nodeLevel, h.maxLevel); level >= 0; level-- {
		if !h.isValidLayerIndex(level) {
			continue
		}
		h.layers[level].addNode(id)
		neighbors := h.searchLayerInternal(vector, mag, currObj, h.efConstruct, level)
		h.connectNode(id, neighbors, level)
		if len(neighbors) > 0 && h.isValidNeighbor(neighbors[0].id) {
			currObj = neighbors[0].id
			currDist = neighbors[0].distance
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

func (h *Index) greedySearchLayer(query []float32, queryMag float64, ep uint32, epDist float64, level int) (uint32, float64) {
	if !h.isValidLayerIndex(level) {
		return ep, epDist
	}

	changed := true
	for changed {
		changed = false
		neighbors := h.layers[level].getNeighbors(ep)
		for _, neighbor := range neighbors {
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
	id       uint32
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

func (h *Index) searchLayerInternal(query []float32, queryMag float64, ep uint32, ef int, level int) []searchCandidate {
	if !h.isValidLayerIndex(level) {
		return nil
	}

	visited := visitedPool.Get().(map[uint32]bool)
	clear(visited)
	defer visitedPool.Put(visited)
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

		iterationBestDist := lastBestDist

		neighbors := h.layers[level].getNeighbors(closest.id)
		for _, neighborID := range neighbors {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true

			if h.finger != nil && h.finger.HasProjectionMatrix() {
				currentBestSim := 1.0 - (*results)[0].distance
				if h.finger.ShouldSkipCandidateByID(query, currentBestSim, closest.id, neighborID) {
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

	output := make([]searchCandidate, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		output[i] = heap.Pop(results).(searchCandidate)
	}

	sort.Slice(output, func(i, j int) bool {
		return output[i].distance < output[j].distance
	})

	return output
}

func (h *Index) searchLayer(query []float32, queryMag float64, ep uint32, ef int, level int) []SearchResult {
	internal := h.searchLayerInternal(query, queryMag, ep, ef, level)
	if internal == nil {
		return nil
	}
	output := make([]SearchResult, len(internal))
	for i, c := range internal {
		output[i] = SearchResult{
			ID:         h.idToString[c.id],
			Similarity: 1.0 - c.distance,
		}
	}
	return output
}

func (h *Index) getVectorAndMagnitude(id uint32) ([]float32, float64, bool) {
	vec, vecExists := h.vectors[id]
	if !vecExists {
		return nil, 0, false
	}
	mag, magExists := h.magnitudes[id]
	if !magExists {
		mag = Magnitude(vec)
		slog.Warn("magnitude cache miss, computed on read",
			slog.Uint64("node_id", uint64(id)),
			slog.Float64("magnitude", mag))
	}
	return vec, mag, true
}

func (h *Index) connectNode(id uint32, neighbors []searchCandidate, level int) {
	maxConn := h.M
	if level == 0 {
		maxConn = h.M * 2
	}

	for i := range min(len(neighbors), maxConn) {
		neighbor := neighbors[i]
		if !h.isValidNeighbor(neighbor.id) {
			continue
		}
		distance := float32(neighbor.distance)
		h.layers[level].addNeighbor(id, neighbor.id, distance, maxConn)
		h.layers[level].addNeighbor(neighbor.id, id, distance, maxConn)
	}
}

func (h *Index) isValidNeighbor(neighborID uint32) bool {
	_, exists := h.vectors[neighborID]
	return exists
}

func (h *Index) Search(query []float32, k int, filter *SearchFilter) []SearchResult {
	if len(query) == 0 || k <= 0 {
		return nil
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.entryPoint == invalidNodeID {
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

	epVec, epMag, ok := h.getVectorAndMagnitude(currObj)
	if !ok {
		return nil
	}
	currDist := CosineDistance(query, epVec, queryMag, epMag)

	for level := h.maxLevel; level > 0; level-- {
		currObj, currDist = h.greedySearchLayer(query, queryMag, currObj, currDist, level)
	}

	internal := h.searchLayerInternal(query, queryMag, currObj, h.efSearch, 0)
	return h.filterAndLimit(internal, k, filter)
}

func (h *Index) filterAndLimit(candidates []searchCandidate, k int, filter *SearchFilter) []SearchResult {
	results := make([]SearchResult, 0, k)
	for _, c := range candidates {
		if !h.matchesFilter(c.id, 1.0-c.distance, filter) {
			continue
		}
		results = append(results, SearchResult{
			ID:         h.idToString[c.id],
			Similarity: 1.0 - c.distance,
			Domain:     h.domains[c.id],
			NodeType:   h.nodeTypes[c.id],
		})
		if len(results) >= k {
			break
		}
	}
	return results
}

func (h *Index) matchesFilter(id uint32, similarity float64, filter *SearchFilter) bool {
	if filter == nil {
		return true
	}

	if filter.MinSimilarity > 0 && similarity < filter.MinSimilarity {
		return false
	}

	if len(filter.Domains) > 0 {
		domain, found := h.domains[id]
		if !found {
			if filter.DomainFilterMode == DomainFilterLenient {
				slog.Warn("domain not found for node during search filter",
					slog.Uint64("node_id", uint64(id)),
					slog.String("mode", "lenient"),
					slog.String("action", "including node in results"))
			} else {
				slog.Debug("domain not found for node during search filter",
					slog.Uint64("node_id", uint64(id)),
					slog.String("mode", "strict"),
					slog.String("action", "excluding node from results"))
				return false
			}
		} else if !slices.Contains(filter.Domains, domain) {
			return false
		}
	}

	if len(filter.NodeTypes) > 0 {
		if !slices.Contains(filter.NodeTypes, h.nodeTypes[id]) {
			return false
		}
	}
	return true
}

func (h *Index) Delete(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	internalID, exists := h.stringToID[id]
	if !exists {
		return fmt.Errorf("%w: cannot delete node %q", ErrNodeNotFound, id)
	}

	h.deleteUnlocked(internalID, id)
	return nil
}

func (h *Index) DeleteBatch(ids []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	internalIDs := make([]uint32, len(ids))
	for i, id := range ids {
		internalID, exists := h.stringToID[id]
		if !exists {
			return fmt.Errorf("%w: cannot delete node %q in batch of %d nodes",
				ErrNodeNotFound, id, len(ids))
		}
		internalIDs[i] = internalID
	}

	for i, internalID := range internalIDs {
		h.deleteUnlocked(internalID, ids[i])
	}
	return nil
}

func (h *Index) deleteUnlocked(id uint32, stringID string) {
	for _, l := range h.layers {
		h.cleanupNodeReferences(id, l)
	}

	delete(h.vectors, id)
	delete(h.magnitudes, id)
	delete(h.domains, id)
	delete(h.nodeTypes, id)
	delete(h.nodeLevels, id)
	delete(h.stringToID, stringID)

	if h.entryPoint == id {
		h.selectNewEntryPoint()
	}
}

func (h *Index) cleanupNodeReferences(id uint32, l *layer) {
	pointingNodes := l.findNodesPointingTo(id)
	for _, nodeID := range pointingNodes {
		l.removeNeighbor(nodeID, id)
	}
	l.removeNode(id)
}

func (h *Index) selectNewEntryPoint() {
	h.entryPoint = invalidNodeID
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
	_, exists := h.stringToID[id]
	return exists
}

func (h *Index) GetVector(id string) ([]float32, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	internalID, exists := h.stringToID[id]
	if !exists {
		return nil, fmt.Errorf("%w: cannot get vector for node %q", ErrNodeNotFound, id)
	}
	vec := h.vectors[internalID]
	result := make([]float32, len(vec))
	copy(result, vec)
	return result, nil
}

func (h *Index) GetMetadata(id string) (vectorgraphdb.Domain, vectorgraphdb.NodeType, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	internalID, exists := h.stringToID[id]
	if !exists {
		return 0, 0, fmt.Errorf("%w: cannot get metadata for node %q", ErrNodeNotFound, id)
	}
	return h.domains[internalID], h.nodeTypes[internalID], nil
}

func (h *Index) GetNodeLevel(id string) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	internalID, exists := h.stringToID[id]
	if !exists {
		return -1, fmt.Errorf("%w: cannot get level for node %q", ErrNodeNotFound, id)
	}
	return h.nodeLevels[internalID], nil
}

func (h *Index) ValidateMetadataConsistency() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.validateMetadataLocked()
}

func (h *Index) validateMetadataLocked() []string {
	var issues []string
	for id := range h.vectors {
		issues = h.checkNodeConsistency(id, issues)
	}
	return issues
}

func (h *Index) checkNodeConsistency(id uint32, issues []string) []string {
	stringID := h.idToString[id]
	if _, ok := h.domains[id]; !ok {
		issues = append(issues, fmt.Sprintf("node %q: missing domain", stringID))
	}
	if _, ok := h.nodeTypes[id]; !ok {
		issues = append(issues, fmt.Sprintf("node %q: missing nodeType", stringID))
	}
	if _, ok := h.magnitudes[id]; !ok {
		issues = append(issues, fmt.Sprintf("node %q: missing magnitude", stringID))
	}
	if _, ok := h.nodeLevels[id]; !ok {
		issues = append(issues, fmt.Sprintf("node %q: missing level tracking", stringID))
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

func (h *Index) PrecomputeFINGER() {
	h.mu.RLock()
	if h.finger == nil || len(h.layers) == 0 {
		h.mu.RUnlock()
		return
	}

	vectors := h.getVectorsInternal()
	layer0 := h.layers[0]
	nodeIDs := layer0.allNodeIDs()
	neighbors := layer0.getNeighborsMany(nodeIDs)
	h.mu.RUnlock()

	h.finger.PrecomputeResiduals(vectors, neighbors)
}

func (h *Index) getVectorsInternal() map[uint32][]float32 {
	result := make(map[uint32][]float32, len(h.vectors))
	for id, vec := range h.vectors {
		result[id] = vec
	}
	return result
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

func (h *Index) GetVectors() map[string][]float32 {
	if h.vectors == nil {
		return nil
	}
	result := make(map[string][]float32, len(h.vectors))
	for id, vec := range h.vectors {
		vecCopy := make([]float32, len(vec))
		copy(vecCopy, vec)
		result[h.idToString[id]] = vecCopy
	}
	return result
}

func (h *Index) GetMagnitudes() map[string]float64 {
	if h.magnitudes == nil {
		return nil
	}
	result := make(map[string]float64, len(h.magnitudes))
	for id, mag := range h.magnitudes {
		result[h.idToString[id]] = mag
	}
	return result
}

func (h *Index) GetEntryPoint() string {
	if h.entryPoint == invalidNodeID {
		return ""
	}
	return h.idToString[h.entryPoint]
}

func (h *Index) GetMaxLevel() int {
	return h.maxLevel
}

func (h *Index) GetDomains() map[string]vectorgraphdb.Domain {
	if h.domains == nil {
		return nil
	}
	result := make(map[string]vectorgraphdb.Domain, len(h.domains))
	for id, domain := range h.domains {
		result[h.idToString[id]] = domain
	}
	return result
}

func (h *Index) GetNodeTypes() map[string]vectorgraphdb.NodeType {
	if h.nodeTypes == nil {
		return nil
	}
	result := make(map[string]vectorgraphdb.NodeType, len(h.nodeTypes))
	for id, nodeType := range h.nodeTypes {
		result[h.idToString[id]] = nodeType
	}
	return result
}

func (h *Index) GetNodeLevels() map[string]int {
	if h.nodeLevels == nil {
		return nil
	}
	result := make(map[string]int, len(h.nodeLevels))
	for id, level := range h.nodeLevels {
		result[h.idToString[id]] = level
	}
	return result
}
