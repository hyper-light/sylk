package hnsw

import (
	"math/rand"
	"sync"
)

type layerNode struct {
	neighbors []string
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
		l.nodes[id] = &layerNode{neighbors: make([]string, 0)}
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
	defer l.mu.RUnlock()
	node, exists := l.nodes[id]
	if !exists {
		return nil
	}
	result := make([]string, len(node.neighbors))
	copy(result, node.neighbors)
	return result
}

func (l *layer) setNeighbors(id string, neighbors []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	node, exists := l.nodes[id]
	if !exists {
		return
	}
	node.neighbors = make([]string, len(neighbors))
	copy(node.neighbors, neighbors)
}

func (l *layer) addNeighbor(id, neighborID string, maxNeighbors int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	node, exists := l.nodes[id]
	if !exists {
		return false
	}
	if l.containsNeighbor(node, neighborID) {
		return false
	}
	if len(node.neighbors) >= maxNeighbors {
		return false
	}
	node.neighbors = append(node.neighbors, neighborID)
	return true
}

func (l *layer) containsNeighbor(node *layerNode, neighborID string) bool {
	for _, n := range node.neighbors {
		if n == neighborID {
			return true
		}
	}
	return false
}

func (l *layer) removeNeighbor(id, neighborID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	node, exists := l.nodes[id]
	if !exists {
		return
	}
	node.neighbors = l.filterNeighbor(node.neighbors, neighborID)
}

func (l *layer) filterNeighbor(neighbors []string, toRemove string) []string {
	result := make([]string, 0, len(neighbors))
	for _, n := range neighbors {
		if n != toRemove {
			result = append(result, n)
		}
	}
	return result
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
