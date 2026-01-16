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

	if idx.updateExistingNode(id, vector) {
		return nil
	}

	level := idx.randomLevel()
	node := idx.newIndexNode(id, vector, level)

	if idx.insertFirstNode(id, node, level) {
		return nil
	}

	ep := idx.navigateToLevel(vector, level)
	idx.connectNode(node, vector, ep, level)

	idx.storeNode(id, node, level)
	return nil
}

func (idx *SimilarityIndex) updateExistingNode(id string, vector []float32) bool {
	if _, exists := idx.nodes[id]; !exists {
		return false
	}
	idx.nodes[id].Vector = vector
	return true
}

func (idx *SimilarityIndex) newIndexNode(id string, vector []float32, level int) *IndexNode {
	node := &IndexNode{
		ID:        id,
		Vector:    vector,
		Level:     level,
		Neighbors: make([][]string, level+1),
	}
	for i := 0; i <= level; i++ {
		node.Neighbors[i] = make([]string, 0, idx.config.M)
	}
	return node
}

func (idx *SimilarityIndex) insertFirstNode(id string, node *IndexNode, level int) bool {
	if len(idx.nodes) != 0 {
		return false
	}
	idx.nodes[id] = node
	idx.entryPoint = id
	idx.maxLevel = level
	idx.ensureLayerCapacity(level)
	idx.layers[level].NodeIDs = append(idx.layers[level].NodeIDs, id)
	atomic.AddInt64(&idx.stats.totalNodes, 1)
	return true
}

func (idx *SimilarityIndex) navigateToLevel(vector []float32, level int) string {
	ep := idx.entryPoint
	for l := idx.maxLevel; l > level; l-- {
		ep = idx.searchLayerGreedy(vector, ep, l)
	}
	return ep
}

func (idx *SimilarityIndex) connectNode(node *IndexNode, vector []float32, entryPoint string, level int) {
	ep := entryPoint
	for l := min(level, idx.maxLevel); l >= 0; l-- {
		neighbors := idx.searchLayerEf(vector, ep, idx.config.EfConstruction, l)
		selected := idx.selectNeighbors(vector, neighbors, idx.getM(l))
		node.Neighbors[l] = selected
		idx.connectNeighbors(idForNode(node), selected, l)
		ep = nextEntryPoint(ep, neighbors)
	}
}

func idForNode(node *IndexNode) string {
	return node.ID
}

func (idx *SimilarityIndex) connectNeighbors(nodeID string, neighbors []string, level int) {
	for _, neighborID := range neighbors {
		idx.connectNeighbor(nodeID, neighborID, level)
	}
}

func (idx *SimilarityIndex) connectNeighbor(nodeID string, neighborID string, level int) {
	neighbor := idx.nodes[neighborID]
	if neighbor == nil || level >= len(neighbor.Neighbors) {
		return
	}
	if len(neighbor.Neighbors[level]) < idx.getM(level) {
		neighbor.Neighbors[level] = append(neighbor.Neighbors[level], nodeID)
		return
	}
	neighbor.Neighbors[level] = idx.selectNeighbors(
		neighbor.Vector,
		append(neighbor.Neighbors[level], nodeID),
		idx.getM(level),
	)
}

func nextEntryPoint(current string, neighbors []string) string {
	if len(neighbors) == 0 {
		return current
	}
	return neighbors[0]
}

func (idx *SimilarityIndex) storeNode(id string, node *IndexNode, level int) {
	idx.nodes[id] = node
	atomic.AddInt64(&idx.stats.totalNodes, 1)
	idx.ensureLayerCapacity(level)
	idx.layers[level].NodeIDs = append(idx.layers[level].NodeIDs, id)
	if level > idx.maxLevel {
		idx.entryPoint = id
		idx.maxLevel = level
	}
}

// Remove removes a vector from the index
func (idx *SimilarityIndex) Remove(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	node, exists := idx.nodes[id]
	if !exists {
		return
	}

	idx.removeNeighborConnections(id, node)
	idx.removeFromLayers(id, node.Level)
	idx.deleteNode(id)
	idx.updateEntryPointIfNeeded(id)
}

func (idx *SimilarityIndex) removeNeighborConnections(id string, node *IndexNode) {
	for level := 0; level <= node.Level; level++ {
		idx.removeNeighborConnectionsAtLevel(id, node, level)
	}
}

func (idx *SimilarityIndex) removeNeighborConnectionsAtLevel(id string, node *IndexNode, level int) {
	for _, neighborID := range node.Neighbors[level] {
		idx.removeNeighborLink(id, neighborID, level)
	}
}

func (idx *SimilarityIndex) removeNeighborLink(id string, neighborID string, level int) {
	neighbor := idx.nodes[neighborID]
	if neighbor == nil {
		return
	}
	if level >= len(neighbor.Neighbors) {
		return
	}
	neighbor.Neighbors[level] = removeFromStringSlice(neighbor.Neighbors[level], id)
}

func (idx *SimilarityIndex) removeFromLayers(id string, maxLevel int) {
	for level := 0; level <= maxLevel; level++ {
		idx.layers[level].NodeIDs = removeFromStringSlice(idx.layers[level].NodeIDs, id)
	}
}

func (idx *SimilarityIndex) deleteNode(id string) {
	delete(idx.nodes, id)
	atomic.AddInt64(&idx.stats.totalNodes, -1)
}

func (idx *SimilarityIndex) updateEntryPointIfNeeded(id string) {
	if idx.entryPoint != id {
		return
	}
	idx.entryPoint = ""
	idx.refreshEntryPoint()
}

func (idx *SimilarityIndex) refreshEntryPoint() {
	for level := idx.maxLevel; level >= 0; level-- {
		entryPoint, ok := idx.layerEntryPoint(level)
		if ok {
			idx.entryPoint = entryPoint
			idx.maxLevel = level
			return
		}
	}
}

func (idx *SimilarityIndex) layerEntryPoint(level int) (string, bool) {
	if len(idx.layers[level].NodeIDs) == 0 {
		return "", false
	}
	return idx.layers[level].NodeIDs[0], true
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

	if !idx.beginSearch() {
		return nil
	}

	ep := idx.searchEntryPoint(query)
	candidates := idx.searchLayerEf(query, ep, max(idx.config.EfSearch, k), 0)
	results := idx.collectTopResults(query, candidates, k)
	sortResults(results)
	return results
}

// SearchWithThreshold finds neighbors above a similarity threshold
func (idx *SimilarityIndex) SearchWithThreshold(query []float32, threshold float64, maxResults int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if !idx.beginSearch() {
		return nil
	}

	ep := idx.searchEntryPoint(query)
	candidates := idx.searchLayerEf(query, ep, max(idx.config.EfSearch, maxResults*2), 0)
	results := idx.filterThresholdResults(query, candidates, threshold, maxResults)
	sortResults(results)
	return results
}

func (idx *SimilarityIndex) beginSearch() bool {
	atomic.AddInt64(&idx.stats.totalSearches, 1)
	return len(idx.nodes) > 0 && idx.entryPoint != ""
}

func (idx *SimilarityIndex) searchEntryPoint(query []float32) string {
	ep := idx.entryPoint
	for level := idx.maxLevel; level > 0; level-- {
		ep = idx.searchLayerGreedy(query, ep, level)
	}
	return ep
}

func (idx *SimilarityIndex) collectTopResults(query []float32, candidates []string, limit int) []SearchResult {
	results := make([]SearchResult, 0, limit)
	for i := 0; i < len(candidates) && i < limit; i++ {
		if result, ok := idx.scoreCandidate(query, candidates[i]); ok {
			results = append(results, result)
		}
	}
	return results
}

func (idx *SimilarityIndex) filterThresholdResults(query []float32, candidates []string, threshold float64, maxResults int) []SearchResult {
	results := make([]SearchResult, 0, maxResults)
	for _, candidateID := range candidates {
		result, ok := idx.scoreCandidate(query, candidateID)
		if ok && result.Score >= threshold {
			results = append(results, result)
		}
		if len(results) >= maxResults {
			break
		}
	}
	return results
}

func (idx *SimilarityIndex) scoreCandidate(query []float32, candidateID string) (SearchResult, bool) {
	node := idx.nodes[candidateID]
	if node == nil {
		return SearchResult{}, false
	}
	atomic.AddInt64(&idx.stats.totalComparisons, 1)
	score := cosineSimilarity(query, node.Vector)
	return SearchResult{ID: candidateID, Score: score}, true
}

func sortResults(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}

// =============================================================================
// Internal Search Methods
// =============================================================================

// searchLayerGreedy performs greedy search to find closest node at a layer
func (idx *SimilarityIndex) searchLayerGreedy(query []float32, ep string, level int) string {
	current := ep
	currentDist := idx.distance(query, current)

	for {
		nextID, nextDist, moved := idx.greedyStep(query, current, currentDist, level)
		if !moved {
			return current
		}
		current = nextID
		currentDist = nextDist
	}
}

func (idx *SimilarityIndex) greedyStep(query []float32, current string, currentDist float64, level int) (string, float64, bool) {
	node := idx.nodes[current]
	if node == nil || level >= len(node.Neighbors) {
		return current, currentDist, false
	}

	nextID := current
	nextDist := currentDist
	for _, neighborID := range node.Neighbors[level] {
		atomic.AddInt64(&idx.stats.totalComparisons, 1)
		dist := idx.distance(query, neighborID)
		if dist < nextDist {
			nextID = neighborID
			nextDist = dist
		}
	}

	return nextID, nextDist, nextID != current
}

// searchLayerEf performs ef-search at a layer
func (idx *SimilarityIndex) searchLayerEf(query []float32, ep string, ef int, level int) []string {
	state := newLayerSearchState(ep)
	idx.seedLayerSearch(state, query, ep)

	for state.candidates.Len() > 0 {
		closest := heap.Pop(state.candidates).(distanceItem)
		if shouldStopLayerSearch(state.results, closest) {
			break
		}

		neighbors := idx.layerNeighborIDs(closest.id, level)
		if len(neighbors) == 0 {
			continue
		}
		idx.visitLayerNeighbors(state, query, neighbors, ef)
	}

	return extractLayerResults(state.results)
}

type layerSearchState struct {
	visited    map[string]bool
	candidates *distanceHeap
	results    *distanceHeap
}

func newLayerSearchState(ep string) *layerSearchState {
	visited := map[string]bool{ep: true}
	candidates := &distanceHeap{}
	results := &distanceHeap{maxHeap: true}
	heap.Init(candidates)
	heap.Init(results)
	return &layerSearchState{
		visited:    visited,
		candidates: candidates,
		results:    results,
	}
}

func (idx *SimilarityIndex) seedLayerSearch(state *layerSearchState, query []float32, ep string) {
	epDist := idx.distance(query, ep)
	heap.Push(state.candidates, distanceItem{id: ep, dist: epDist})
	heap.Push(state.results, distanceItem{id: ep, dist: epDist})
}

func shouldStopLayerSearch(results *distanceHeap, closest distanceItem) bool {
	if results.Len() == 0 {
		return false
	}
	return closest.dist > results.items[0].dist
}

func (idx *SimilarityIndex) layerNeighborIDs(nodeID string, level int) []string {
	node := idx.nodes[nodeID]
	if node == nil {
		return nil
	}
	if level >= len(node.Neighbors) {
		return nil
	}
	return node.Neighbors[level]
}

func (idx *SimilarityIndex) visitLayerNeighbors(state *layerSearchState, query []float32, neighbors []string, ef int) {
	for _, neighborID := range neighbors {
		idx.considerLayerNeighbor(state, query, neighborID, ef)
	}
}

func (idx *SimilarityIndex) considerLayerNeighbor(state *layerSearchState, query []float32, neighborID string, ef int) {
	if state.visited[neighborID] {
		return
	}
	state.visited[neighborID] = true

	atomic.AddInt64(&idx.stats.totalComparisons, 1)
	dist := idx.distance(query, neighborID)

	if shouldAddLayerResult(state.results, ef, dist) {
		heap.Push(state.candidates, distanceItem{id: neighborID, dist: dist})
		heap.Push(state.results, distanceItem{id: neighborID, dist: dist})
		trimLayerResults(state.results, ef)
	}
}

func shouldAddLayerResult(results *distanceHeap, ef int, dist float64) bool {
	if results.Len() < ef {
		return true
	}
	return dist < results.items[0].dist
}

func trimLayerResults(results *distanceHeap, ef int) {
	if results.Len() > ef {
		heap.Pop(results)
	}
}

func extractLayerResults(results *distanceHeap) []string {
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
