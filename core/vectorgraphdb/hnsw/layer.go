package hnsw

import (
	"math/rand"
	"sync"
)

// layerNode represents a node in an HNSW layer with its neighbors.
// Uses ConcurrentNeighborSet for O(1) neighbor lookups instead of O(n) slice scans.
type layerNode struct {
	neighbors *ConcurrentNeighborSet
}

type layer struct {
	mu    sync.RWMutex
	nodes map[string]*layerNode
}

func newLayer() *layer {
	return &layer{
		nodes: make(map[string]*layerNode),
	}
}

func (l *layer) addNode(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.nodes[id]; !exists {
		l.nodes[id] = &layerNode{neighbors: NewConcurrentNeighborSet()}
	}
}

func (l *layer) removeNode(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.nodes, id)
}

func (l *layer) hasNode(id string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, exists := l.nodes[id]
	return exists
}

func (l *layer) getNeighbors(id string) []string {
	l.mu.RLock()
	node, exists := l.nodes[id]
	l.mu.RUnlock()
	if !exists {
		return nil
	}
	// Get sorted neighbors from ConcurrentNeighborSet
	neighbors := node.neighbors.GetSortedNeighbors()
	ids := make([]string, len(neighbors))
	for i, n := range neighbors {
		ids[i] = n.ID
	}
	return ids
}

// getNeighborsMany retrieves neighbors for multiple nodes in a single batch operation.
// This reduces lock acquisitions compared to calling getNeighbors multiple times.
// PF.2.4: Batch neighbor retrieval for improved performance.
func (l *layer) getNeighborsMany(nodeIDs []string) map[string][]string {
	result := make(map[string][]string, len(nodeIDs))
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, id := range nodeIDs {
		if node, exists := l.nodes[id]; exists {
			neighbors := node.neighbors.GetSortedNeighbors()
			ids := make([]string, len(neighbors))
			for i, n := range neighbors {
				ids[i] = n.ID
			}
			result[id] = ids
		}
	}
	return result
}

// getNeighborsWithDistances returns neighbors with their distances for a node.
// Useful when distance information is needed for neighbor selection.
func (l *layer) getNeighborsWithDistances(id string) []Neighbor {
	l.mu.RLock()
	node, exists := l.nodes[id]
	l.mu.RUnlock()
	if !exists {
		return nil
	}
	return node.neighbors.GetSortedNeighbors()
}

func (l *layer) setNeighbors(id string, neighbors []string, distances []float32) {
	l.mu.RLock()
	node, exists := l.nodes[id]
	l.mu.RUnlock()
	if !exists {
		return
	}
	// Clear and repopulate the neighbor set
	node.neighbors.Clear()
	for i, neighborID := range neighbors {
		dist := float32(0)
		if i < len(distances) {
			dist = distances[i]
		}
		node.neighbors.Add(neighborID, dist)
	}
}

// addNeighbor adds a neighbor with distance to a node.
// Uses ConcurrentNeighborSet.AddWithLimit for O(1) contains check and automatic
// replacement of worst neighbors when at capacity.
func (l *layer) addNeighbor(id, neighborID string, distance float32, maxNeighbors int) bool {
	l.mu.RLock()
	node, exists := l.nodes[id]
	l.mu.RUnlock()
	if !exists {
		return false
	}
	// AddWithLimit handles the contains check, capacity limit, and worst-neighbor replacement
	return node.neighbors.AddWithLimit(neighborID, distance, maxNeighbors)
}

// containsNeighbor checks if a node has a specific neighbor.
// O(1) lookup using ConcurrentNeighborSet instead of O(n) slice scan.
func (l *layer) containsNeighbor(node *layerNode, neighborID string) bool {
	return node.neighbors.Contains(neighborID)
}

func (l *layer) removeNeighbor(id, neighborID string) {
	l.mu.RLock()
	node, exists := l.nodes[id]
	l.mu.RUnlock()
	if !exists {
		return
	}
	node.neighbors.Remove(neighborID)
}

func (l *layer) nodeCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.nodes)
}

func (l *layer) allNodeIDs() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]string, 0, len(l.nodes))
	for id := range l.nodes {
		ids = append(ids, id)
	}
	return ids
}

func randomLevel(levelMult float64) int {
	r := rand.Float64()
	level := int(-levelMult * (1.0 / 0.36067977499789996) * float64(fastLog(r)))
	if level < 0 {
		return 0
	}
	return level
}

func fastLog(x float64) float64 {
	if x <= 0 {
		return -1e10
	}
	return fastLogApprox(x)
}

func fastLogApprox(x float64) float64 {
	var result float64
	for x >= 2 {
		x /= 2
		result += 0.693147
	}
	for x < 1 {
		x *= 2
		result -= 0.693147
	}
	return result + (x - 1) - (x-1)*(x-1)/2 + (x-1)*(x-1)*(x-1)/3
}
