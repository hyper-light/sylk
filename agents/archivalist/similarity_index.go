package archivalist

import (
	"container/heap"
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// =============================================================================
// HNSW-Inspired Similarity Index
// =============================================================================

// SimilarityIndex provides efficient approximate nearest neighbor search
type SimilarityIndex struct {
	mu sync.RWMutex

	// Index configuration
	config SimilarityIndexConfig

	// Node storage
	nodes map[string]*IndexNode

	// Level organization (layer 0 is base, higher = sparser)
	layers []*IndexLayer

	// Entry point for search
	entryPoint string

	// Maximum level currently in use
	maxLevel int

	// Statistics (atomic for thread safety)
	stats similarityIndexStatsInternal
}

// SimilarityIndexConfig configures the similarity index
type SimilarityIndexConfig struct {
	// Dimension of vectors
	Dimension int `json:"dimension"`

	// Maximum connections per node at base layer
	M int `json:"m"`

	// Maximum connections per node at higher layers
	MMax int `json:"m_max"`

	// Size of dynamic candidate list during construction
	EfConstruction int `json:"ef_construction"`

	// Size of dynamic candidate list during search
	EfSearch int `json:"ef_search"`

	// Level multiplier (1/ln(M))
	LevelMult float64 `json:"level_mult"`
}

// DefaultSimilarityIndexConfig returns sensible defaults
func DefaultSimilarityIndexConfig() SimilarityIndexConfig {
	return SimilarityIndexConfig{
		Dimension:      1536,
		M:              16,
		MMax:           32,
		EfConstruction: 100,
		EfSearch:       50,
		LevelMult:      1.0 / math.Log(16),
	}
}

// IndexNode represents a node in the similarity index
type IndexNode struct {
	ID        string
	Vector    []float32
	Level     int
	Neighbors [][]string // neighbors[level] = list of neighbor IDs
}

// IndexLayer represents a layer in the hierarchical structure
type IndexLayer struct {
	NodeIDs []string
}

// similarityIndexStatsInternal holds atomic counters
type similarityIndexStatsInternal struct {
	totalNodes       int64
	totalSearches    int64
	totalInsertions  int64
	totalComparisons int64
}

// SimilarityIndexStats contains index statistics
type SimilarityIndexStats struct {
	TotalNodes       int   `json:"total_nodes"`
	TotalSearches    int64 `json:"total_searches"`
	TotalInsertions  int64 `json:"total_insertions"`
	TotalComparisons int64 `json:"total_comparisons"`
	MaxLevel         int   `json:"max_level"`
	LayerSizes       []int `json:"layer_sizes"`
}

// NewSimilarityIndex creates a new similarity index
func NewSimilarityIndex(config SimilarityIndexConfig) *SimilarityIndex {
	if config.M == 0 {
		config = DefaultSimilarityIndexConfig()
	}

	return &SimilarityIndex{
		config: config,
		nodes:  make(map[string]*IndexNode),
		layers: make([]*IndexLayer, 0),
	}
}

// =============================================================================
// Insertion
// =============================================================================

// Insert adds a vector to the index
func (idx *SimilarityIndex) Insert(id string, vector []float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	atomic.AddInt64(&idx.stats.totalInsertions, 1)

	// Check if already exists
	if _, exists := idx.nodes[id]; exists {
		// Update existing node
		idx.nodes[id].Vector = vector
		return nil
	}

	// Determine level for new node
	level := idx.randomLevel()

	// Create new node
	node := &IndexNode{
		ID:        id,
		Vector:    vector,
		Level:     level,
		Neighbors: make([][]string, level+1),
	}
	for i := 0; i <= level; i++ {
		node.Neighbors[i] = make([]string, 0, idx.config.M)
	}

	// Handle first node
	if len(idx.nodes) == 0 {
		idx.nodes[id] = node
		idx.entryPoint = id
		idx.maxLevel = level
		idx.ensureLayerCapacity(level)
		idx.layers[level].NodeIDs = append(idx.layers[level].NodeIDs, id)
		atomic.AddInt64(&idx.stats.totalNodes, 1)
		return nil
	}

	// Find entry point and navigate down
	ep := idx.entryPoint

	// Search from top to the node's level + 1
	for l := idx.maxLevel; l > level; l-- {
		ep = idx.searchLayerGreedy(vector, ep, l)
	}

	// Insert at each level from node's level down to 0
	for l := min(level, idx.maxLevel); l >= 0; l-- {
		neighbors := idx.searchLayerEf(vector, ep, idx.config.EfConstruction, l)
		selectedNeighbors := idx.selectNeighbors(vector, neighbors, idx.getM(l))

		// Connect new node to selected neighbors
		node.Neighbors[l] = selectedNeighbors

		// Connect selected neighbors back to new node
		for _, neighborID := range selectedNeighbors {
			neighbor := idx.nodes[neighborID]
			if neighbor != nil && l < len(neighbor.Neighbors) {
				if len(neighbor.Neighbors[l]) < idx.getM(l) {
					neighbor.Neighbors[l] = append(neighbor.Neighbors[l], id)
				} else {
					// Prune neighbors to make room
					neighbor.Neighbors[l] = idx.selectNeighbors(
						neighbor.Vector,
						append(neighbor.Neighbors[l], id),
						idx.getM(l),
					)
				}
			}
		}

		if len(neighbors) > 0 {
			ep = neighbors[0]
		}
	}

	// Store node
	idx.nodes[id] = node
	atomic.AddInt64(&idx.stats.totalNodes, 1)

	// Update layers
	idx.ensureLayerCapacity(level)
	for l := 0; l <= level; l++ {
		idx.layers[l].NodeIDs = append(idx.layers[l].NodeIDs, id)
	}

	// Update entry point if new node has higher level
	if level > idx.maxLevel {
		idx.maxLevel = level
		idx.entryPoint = id
	}

	return nil
}

// Remove removes a vector from the index
func (idx *SimilarityIndex) Remove(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	node, exists := idx.nodes[id]
	if !exists {
		return
	}

	// Remove connections from neighbors
	for l := 0; l <= node.Level; l++ {
		for _, neighborID := range node.Neighbors[l] {
			neighbor := idx.nodes[neighborID]
			if neighbor != nil && l < len(neighbor.Neighbors) {
				neighbor.Neighbors[l] = removeFromStringSlice(neighbor.Neighbors[l], id)
			}
		}
	}

	// Remove from layers
	for l := 0; l <= node.Level; l++ {
		idx.layers[l].NodeIDs = removeFromStringSlice(idx.layers[l].NodeIDs, id)
	}

	// Delete node
	delete(idx.nodes, id)
	atomic.AddInt64(&idx.stats.totalNodes, -1)

	// Update entry point if needed
	if idx.entryPoint == id {
		idx.entryPoint = ""
		for l := idx.maxLevel; l >= 0; l-- {
			if len(idx.layers[l].NodeIDs) > 0 {
				idx.entryPoint = idx.layers[l].NodeIDs[0]
				idx.maxLevel = l
				break
			}
		}
	}
}

// =============================================================================
// Search
// =============================================================================

// SearchResult represents a search result with score
type SearchResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// Search finds the k nearest neighbors to the query vector
func (idx *SimilarityIndex) Search(query []float32, k int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	atomic.AddInt64(&idx.stats.totalSearches, 1)

	if len(idx.nodes) == 0 || idx.entryPoint == "" {
		return nil
	}

	// Start from entry point
	ep := idx.entryPoint

	// Navigate down from top level
	for l := idx.maxLevel; l > 0; l-- {
		ep = idx.searchLayerGreedy(query, ep, l)
	}

	// Search at base layer with larger ef
	candidates := idx.searchLayerEf(query, ep, max(idx.config.EfSearch, k), 0)

	// Return top k
	results := make([]SearchResult, 0, k)
	for i := 0; i < len(candidates) && i < k; i++ {
		node := idx.nodes[candidates[i]]
		if node != nil {
			atomic.AddInt64(&idx.stats.totalComparisons, 1)
			score := cosineSimilarity(query, node.Vector)
			results = append(results, SearchResult{
				ID:    candidates[i],
				Score: score,
			})
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// SearchWithThreshold finds neighbors above a similarity threshold
func (idx *SimilarityIndex) SearchWithThreshold(query []float32, threshold float64, maxResults int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	atomic.AddInt64(&idx.stats.totalSearches, 1)

	if len(idx.nodes) == 0 || idx.entryPoint == "" {
		return nil
	}

	// Start from entry point
	ep := idx.entryPoint

	// Navigate down from top level
	for l := idx.maxLevel; l > 0; l-- {
		ep = idx.searchLayerGreedy(query, ep, l)
	}

	// Search at base layer with larger ef
	candidates := idx.searchLayerEf(query, ep, max(idx.config.EfSearch, maxResults*2), 0)

	// Filter by threshold
	results := make([]SearchResult, 0)
	for _, candidateID := range candidates {
		node := idx.nodes[candidateID]
		if node != nil {
			atomic.AddInt64(&idx.stats.totalComparisons, 1)
			score := cosineSimilarity(query, node.Vector)
			if score >= threshold {
				results = append(results, SearchResult{
					ID:    candidateID,
					Score: score,
				})
			}
		}
		if len(results) >= maxResults {
			break
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// =============================================================================
// Internal Search Methods
// =============================================================================

// searchLayerGreedy performs greedy search to find closest node at a layer
func (idx *SimilarityIndex) searchLayerGreedy(query []float32, ep string, level int) string {
	current := ep
	currentDist := idx.distance(query, current)

	changed := true
	for changed {
		changed = false
		node := idx.nodes[current]
		if node == nil || level >= len(node.Neighbors) {
			break
		}

		for _, neighborID := range node.Neighbors[level] {
			atomic.AddInt64(&idx.stats.totalComparisons, 1)
			dist := idx.distance(query, neighborID)
			if dist < currentDist {
				current = neighborID
				currentDist = dist
				changed = true
			}
		}
	}

	return current
}

// searchLayerEf performs ef-search at a layer
func (idx *SimilarityIndex) searchLayerEf(query []float32, ep string, ef int, level int) []string {
	visited := make(map[string]bool)
	visited[ep] = true

	// Priority queue of candidates (min-heap by distance)
	candidates := &distanceHeap{}
	heap.Init(candidates)

	// Dynamic list of found results (max-heap by distance for pruning)
	results := &distanceHeap{maxHeap: true}
	heap.Init(results)

	epDist := idx.distance(query, ep)
	heap.Push(candidates, distanceItem{id: ep, dist: epDist})
	heap.Push(results, distanceItem{id: ep, dist: epDist})

	for candidates.Len() > 0 {
		closest := heap.Pop(candidates).(distanceItem)

		// Check termination condition
		if results.Len() > 0 && closest.dist > results.items[0].dist {
			break
		}

		node := idx.nodes[closest.id]
		if node == nil || level >= len(node.Neighbors) {
			continue
		}

		for _, neighborID := range node.Neighbors[level] {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true

			atomic.AddInt64(&idx.stats.totalComparisons, 1)
			dist := idx.distance(query, neighborID)

			if results.Len() < ef || dist < results.items[0].dist {
				heap.Push(candidates, distanceItem{id: neighborID, dist: dist})
				heap.Push(results, distanceItem{id: neighborID, dist: dist})

				if results.Len() > ef {
					heap.Pop(results)
				}
			}
		}
	}

	// Extract results
	resultIDs := make([]string, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		item := heap.Pop(results).(distanceItem)
		resultIDs[i] = item.id
	}

	return resultIDs
}

// selectNeighbors selects the best neighbors using simple heuristic
func (idx *SimilarityIndex) selectNeighbors(query []float32, candidates []string, m int) []string {
	if len(candidates) <= m {
		return candidates
	}

	// Score candidates
	type scored struct {
		id    string
		score float64
	}
	scoredCandidates := make([]scored, 0, len(candidates))
	for _, id := range candidates {
		node := idx.nodes[id]
		if node != nil {
			score := cosineSimilarity(query, node.Vector)
			scoredCandidates = append(scoredCandidates, scored{id: id, score: score})
		}
	}

	// Sort by score descending
	sort.Slice(scoredCandidates, func(i, j int) bool {
		return scoredCandidates[i].score > scoredCandidates[j].score
	})

	// Take top m
	result := make([]string, 0, m)
	for i := 0; i < len(scoredCandidates) && i < m; i++ {
		result = append(result, scoredCandidates[i].id)
	}

	return result
}

// =============================================================================
// Utility Methods
// =============================================================================

// distance computes distance between query and a node
func (idx *SimilarityIndex) distance(query []float32, nodeID string) float64 {
	node := idx.nodes[nodeID]
	if node == nil {
		return math.MaxFloat64
	}
	// Use 1 - cosine similarity as distance
	return 1.0 - cosineSimilarity(query, node.Vector)
}

// randomLevel generates a random level using exponential distribution
func (idx *SimilarityIndex) randomLevel() int {
	level := 0
	// Simple deterministic level assignment based on node count
	// In production, use proper random with -log(rand()) * levelMult
	count := atomic.LoadInt64(&idx.stats.totalNodes)
	for count > 0 && level < 15 {
		count = count / int64(idx.config.M)
		if count > 0 {
			level++
		}
	}
	return level
}

// getM returns the maximum connections for a level
func (idx *SimilarityIndex) getM(level int) int {
	if level == 0 {
		return idx.config.M * 2 // Base layer has more connections
	}
	return idx.config.M
}

// ensureLayerCapacity ensures layers slice has capacity for the given level
func (idx *SimilarityIndex) ensureLayerCapacity(level int) {
	for len(idx.layers) <= level {
		idx.layers = append(idx.layers, &IndexLayer{
			NodeIDs: make([]string, 0),
		})
	}
}

// Stats returns index statistics
func (idx *SimilarityIndex) Stats() SimilarityIndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	layerSizes := make([]int, len(idx.layers))
	for i, layer := range idx.layers {
		layerSizes[i] = len(layer.NodeIDs)
	}

	return SimilarityIndexStats{
		TotalNodes:       int(atomic.LoadInt64(&idx.stats.totalNodes)),
		TotalSearches:    atomic.LoadInt64(&idx.stats.totalSearches),
		TotalInsertions:  atomic.LoadInt64(&idx.stats.totalInsertions),
		TotalComparisons: atomic.LoadInt64(&idx.stats.totalComparisons),
		MaxLevel:         idx.maxLevel,
		LayerSizes:       layerSizes,
	}
}

// Count returns the number of indexed vectors
func (idx *SimilarityIndex) Count() int {
	return int(atomic.LoadInt64(&idx.stats.totalNodes))
}

// =============================================================================
// Priority Queue Implementation
// =============================================================================

type distanceItem struct {
	id   string
	dist float64
}

type distanceHeap struct {
	items   []distanceItem
	maxHeap bool
}

func (h distanceHeap) Len() int { return len(h.items) }

func (h distanceHeap) Less(i, j int) bool {
	if h.maxHeap {
		return h.items[i].dist > h.items[j].dist
	}
	return h.items[i].dist < h.items[j].dist
}

func (h distanceHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *distanceHeap) Push(x any) {
	h.items = append(h.items, x.(distanceItem))
}

func (h *distanceHeap) Pop() any {
	old := h.items
	n := len(old)
	x := old[n-1]
	h.items = old[0 : n-1]
	return x
}

// Helper to remove from string slice
func removeFromStringSlice(slice []string, item string) []string {
	for i, s := range slice {
		if s == item {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

// cosineSimilarity computes cosine similarity between two vectors
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
