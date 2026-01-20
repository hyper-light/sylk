package hnsw

import (
	"errors"
	"slices"
	"sort"
	"sync"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

var (
	ErrNodeNotFound      = errors.New("node not found")
	ErrEmptyVector       = errors.New("vector cannot be empty")
	ErrDimensionMismatch = errors.New("vector dimension mismatch")
	ErrIndexEmpty        = errors.New("index is empty")
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

func (h *Index) Insert(id string, vector []float32, domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType) error {
	if len(vector) == 0 {
		return ErrEmptyVector
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.dimension == 0 {
		h.dimension = len(vector)
	} else if len(vector) != h.dimension {
		return ErrDimensionMismatch
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
	currDist := 1.0 - CosineSimilarity(vector, h.vectors[currObj], mag, h.magnitudes[currObj])

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
			if vec, exists := h.vectors[neighbor]; exists {
				dist := 1.0 - CosineSimilarity(query, vec, queryMag, h.magnitudes[neighbor])
				if dist < epDist {
					ep = neighbor
					epDist = dist
					changed = true
				}
			}
		}
	}
	return ep, epDist
}

func (h *Index) searchLayer(query []float32, queryMag float64, ep string, ef int, level int) []SearchResult {
	visited := make(map[string]bool)
	visited[ep] = true

	candidates := make([]SearchResult, 0, ef)
	epSim := CosineSimilarity(query, h.vectors[ep], queryMag, h.magnitudes[ep])
	candidates = append(candidates, SearchResult{ID: ep, Similarity: epSim})

	for i := range candidates {
		if len(candidates) >= ef*2 {
			break
		}
		curr := candidates[i]
		neighbors := h.layers[level].getNeighbors(curr.ID)
		for _, neighbor := range neighbors {
			if visited[neighbor] {
				continue
			}
			visited[neighbor] = true
			if vec, exists := h.vectors[neighbor]; exists {
				sim := CosineSimilarity(query, vec, queryMag, h.magnitudes[neighbor])
				candidates = append(candidates, SearchResult{ID: neighbor, Similarity: sim})
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Similarity > candidates[j].Similarity
	})

	if len(candidates) > ef {
		candidates = candidates[:ef]
	}
	return candidates
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
	currDist := 1.0 - CosineSimilarity(query, h.vectors[currObj], queryMag, h.magnitudes[currObj])

	for level := h.maxLevel; level > 0; level-- {
		currObj, currDist = h.greedySearchLayer(query, queryMag, currObj, currDist, level)
	}

	candidates := h.searchLayer(query, queryMag, currObj, h.efSearch, 0)
	return h.filterAndLimit(candidates, k, filter)
}

func (h *Index) filterAndLimit(candidates []SearchResult, k int, filter *SearchFilter) []SearchResult {
	results := make([]SearchResult, 0, k)
	for _, c := range candidates {
		if !h.matchesFilter(c.ID, filter) {
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

func (h *Index) matchesFilter(id string, filter *SearchFilter) bool {
	if filter == nil {
		return true
	}
	if filter.MinSimilarity > 0 {
		return true
	}
	if len(filter.Domains) > 0 {
		if !slices.Contains(filter.Domains, h.domains[id]) {
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

	if _, exists := h.vectors[id]; !exists {
		return ErrNodeNotFound
	}

	h.deleteUnlocked(id)
	return nil
}

// DeleteBatch deletes multiple nodes with a single lock acquisition.
// This is more efficient than calling Delete() multiple times when
// deleting many nodes, as it avoids repeated lock/unlock overhead.
func (h *Index) DeleteBatch(ids []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Validate all IDs exist first
	for _, id := range ids {
		if _, exists := h.vectors[id]; !exists {
			return ErrNodeNotFound
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

func (h *Index) GetVector(id string) ([]float32, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	vec, exists := h.vectors[id]
	if !exists {
		return nil, ErrNodeNotFound
	}
	result := make([]float32, len(vec))
	copy(result, vec)
	return result, nil
}

func (h *Index) GetMetadata(id string) (vectorgraphdb.Domain, vectorgraphdb.NodeType, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, exists := h.vectors[id]; !exists {
		return 0, 0, ErrNodeNotFound
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

// GetLayers returns the layers slice for snapshot creation.
// Caller must hold at least a read lock.
func (h *Index) GetLayers() []*layer {
	return h.layers
}

// GetVectors returns the vectors map for snapshot creation.
// Caller must hold at least a read lock.
func (h *Index) GetVectors() map[string][]float32 {
	return h.vectors
}

// GetMagnitudes returns the magnitudes map for snapshot creation.
// Caller must hold at least a read lock.
func (h *Index) GetMagnitudes() map[string]float64 {
	return h.magnitudes
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

// GetDomains returns the domains map for snapshot creation.
// Caller must hold at least a read lock.
func (h *Index) GetDomains() map[string]vectorgraphdb.Domain {
	return h.domains
}

// GetNodeTypes returns the node types map for snapshot creation.
// Caller must hold at least a read lock.
func (h *Index) GetNodeTypes() map[string]vectorgraphdb.NodeType {
	return h.nodeTypes
}
