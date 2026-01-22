package hnsw

import (
	"math/rand"
	"sync"
)

type layerNode struct {
	neighbors *ConcurrentNeighborSet
}

type layer struct {
	mu    sync.RWMutex
	nodes map[uint32]*layerNode
}

func newLayer() *layer {
	return &layer{
		nodes: make(map[uint32]*layerNode),
	}
}

func (l *layer) addNode(id uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.nodes[id]; !exists {
		l.nodes[id] = &layerNode{neighbors: NewConcurrentNeighborSet()}
	}
}

func (l *layer) removeNode(id uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.nodes, id)
}

func (l *layer) hasNode(id uint32) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, exists := l.nodes[id]
	return exists
}

func (l *layer) getNeighbors(id uint32) []uint32 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node, exists := l.nodes[id]
	if !exists {
		return nil
	}
	neighbors := node.neighbors.GetSortedNeighbors()
	ids := make([]uint32, len(neighbors))
	for i, n := range neighbors {
		ids[i] = n.ID
	}
	return ids
}

func (l *layer) getNeighborsMany(nodeIDs []uint32) map[uint32][]uint32 {
	result := make(map[uint32][]uint32, len(nodeIDs))
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, id := range nodeIDs {
		if node, exists := l.nodes[id]; exists {
			neighbors := node.neighbors.GetSortedNeighbors()
			ids := make([]uint32, len(neighbors))
			for i, n := range neighbors {
				ids[i] = n.ID
			}
			result[id] = ids
		}
	}
	return result
}

func (l *layer) getNeighborsWithDistances(id uint32) []Neighbor {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node, exists := l.nodes[id]
	if !exists {
		return nil
	}
	return node.neighbors.GetSortedNeighbors()
}

func (l *layer) setNeighbors(id uint32, neighbors []uint32, distances []float32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	node, exists := l.nodes[id]
	if !exists {
		return
	}
	node.neighbors.Clear()
	for i, neighborID := range neighbors {
		dist := float32(0)
		if i < len(distances) {
			dist = distances[i]
		}
		node.neighbors.Add(neighborID, dist)
	}
}

func (l *layer) addNeighbor(id, neighborID uint32, distance float32, maxNeighbors int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node, exists := l.nodes[id]
	if !exists {
		return false
	}
	return node.neighbors.AddWithLimit(neighborID, distance, maxNeighbors)
}

func (l *layer) containsNeighbor(node *layerNode, neighborID uint32) bool {
	return node.neighbors.Contains(neighborID)
}

func (l *layer) removeNeighbor(id, neighborID uint32) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node, exists := l.nodes[id]
	if !exists {
		return
	}
	node.neighbors.Remove(neighborID)
}

func (l *layer) findNodesPointingTo(targetID uint32) []uint32 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var pointing []uint32
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

func (l *layer) allNodeIDs() []uint32 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]uint32, 0, len(l.nodes))
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
