package hnsw

// W4L.2: HNSW Layer Management
//
// This file implements the layer structure for the HNSW index. Each layer maintains
// a graph of nodes with neighbor connections. The multi-layer structure is key to
// HNSW's logarithmic search complexity:
//
// Layer Structure:
//   - Layer 0 (base): Contains ALL nodes, with M*2 connections per node
//   - Layer 1+: Contains progressively fewer nodes, with M connections per node
//   - Higher layers have longer-range connections for fast global navigation
//
// Node Level Assignment:
//   - Each node is assigned a random level L using: floor(-ln(uniform(0,1)) * levelMult)
//   - A node exists in layers 0 through L (always present in layer 0)
//   - Probability of node at level L: (1/levelMult)^L
//
// Search Algorithm (per layer):
//   1. Start at entry point
//   2. Greedily move to the neighbor closest to query
//   3. Repeat until no closer neighbor exists
//   4. At layer 0, expand search to ef candidates for best results
//
// Connection Heuristics:
//   - Neighbors are selected based on distance (cosine similarity)
//   - Connections are bidirectional for search efficiency
//   - Worst neighbors are replaced when at capacity

import (
	"math/rand"
	"sync"
)

// layerNode represents a node in an HNSW layer with its neighbors.
// Uses ConcurrentNeighborSet for O(1) neighbor lookups instead of O(n) slice scans.
// W4L.2: Added structured neighbor management for HNSW requirements.
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

// findNodesPointingTo returns all node IDs that have targetID as a neighbor.
// W4P.26: Used to find and clean up dangling references after node deletion.
// This ensures graph integrity by finding ALL nodes pointing to a deleted node,
// not just the nodes the deleted node points to.
func (l *layer) findNodesPointingTo(targetID string) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var pointing []string
	for nodeID, node := range l.nodes {
		if node.neighbors.Contains(targetID) {
			pointing = append(pointing, nodeID)
		}
	}
	return pointing
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

// randomLevel generates a random level for a new node using the HNSW level distribution.
// W4L.2: Documented the level generation algorithm.
//
// The formula is: L = floor(-ln(uniform(0,1)) * mL)
// where mL = 1/ln(M) ensures optimal layer distribution.
//
// The constant 0.36067977499789996 is ln(e^(1/e)) which provides good default scaling.
// This gives an expected number of nodes at level L proportional to (1/M)^L.
//
// Example with M=16: ~94% of nodes at level 0 only, ~6% at level 1+, etc.
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
