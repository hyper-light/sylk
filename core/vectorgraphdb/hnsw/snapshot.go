package hnsw

import (
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// LayerSnapshot represents an immutable snapshot of a layer's state.
// All data is deep copied to ensure isolation from the live index.
type LayerSnapshot struct {
	Nodes map[string][]string // nodeID -> neighbors (deep copied, immutable)
}

// HNSWSnapshot represents a point-in-time frozen state of the HNSW index.
// It provides thread-safe read access through atomic reader counting.
type HNSWSnapshot struct {
	ID         uint64               // unique snapshot ID
	SeqNum     uint64               // index sequence number at snapshot time
	CreatedAt  time.Time
	EntryPoint string
	MaxLevel   int
	EfSearch   int                                // search beam width (0 uses default)
	Layers     []LayerSnapshot                    // frozen layer state (copied)
	Vectors    map[string][]float32               // frozen vector cache
	Magnitudes map[string]float64                 // frozen magnitudes
	Domains    map[string]vectorgraphdb.Domain    // frozen domain metadata
	NodeTypes  map[string]vectorgraphdb.NodeType  // frozen node type metadata
	readers    atomic.Int32                       // active reader count
}

// NewLayerSnapshot creates a deep copy of a layer's nodes.
func NewLayerSnapshot(l *layer) LayerSnapshot {
	if l == nil {
		return LayerSnapshot{Nodes: make(map[string][]string)}
	}
	return LayerSnapshot{Nodes: copyLayerNodes(l)}
}

// copyLayerNodes extracts and deep copies all nodes from a layer.
func copyLayerNodes(l *layer) map[string][]string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	nodes := make(map[string][]string, len(l.nodes))
	for id, node := range l.nodes {
		// Get neighbor IDs from ConcurrentNeighborSet and copy
		neighborIDs := node.neighbors.GetIDs()
		nodes[id] = copyStringSlice(neighborIDs)
	}
	return nodes
}

// copyStringSlice creates a deep copy of a string slice.
func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// GetNeighbors returns the neighbors for a node ID.
// Returns nil if the node does not exist.
func (ls *LayerSnapshot) GetNeighbors(id string) []string {
	neighbors, exists := ls.Nodes[id]
	if !exists {
		return nil
	}
	return copyStringSlice(neighbors)
}

// HasNode checks if a node exists in the layer snapshot.
func (ls *LayerSnapshot) HasNode(id string) bool {
	_, exists := ls.Nodes[id]
	return exists
}

// NodeCount returns the number of nodes in the layer snapshot.
func (ls *LayerSnapshot) NodeCount() int {
	return len(ls.Nodes)
}

// NewHNSWSnapshot creates a new snapshot from an Index.
// The caller must hold at least a read lock on the index.
func NewHNSWSnapshot(idx *Index, seqNum uint64) *HNSWSnapshot {
	if idx == nil {
		return &HNSWSnapshot{
			SeqNum:     seqNum,
			CreatedAt:  time.Now(),
			MaxLevel:   -1,
			Layers:     make([]LayerSnapshot, 0),
			Vectors:    make(map[string][]float32),
			Magnitudes: make(map[string]float64),
			Domains:    make(map[string]vectorgraphdb.Domain),
			NodeTypes:  make(map[string]vectorgraphdb.NodeType),
		}
	}
	return createSnapshotFromIndex(idx, seqNum)
}

// createSnapshotFromIndex builds a snapshot from index data.
func createSnapshotFromIndex(idx *Index, seqNum uint64) *HNSWSnapshot {
	return &HNSWSnapshot{
		SeqNum:     seqNum,
		CreatedAt:  time.Now(),
		EntryPoint: idx.entryPoint,
		MaxLevel:   idx.maxLevel,
		EfSearch:   idx.efSearch,
		Layers:     copyLayers(idx.layers),
		Vectors:    copyVectors(idx.vectors),
		Magnitudes: copyMagnitudes(idx.magnitudes),
		Domains:    copyDomains(idx.domains),
		NodeTypes:  copyNodeTypes(idx.nodeTypes),
	}
}

// copyLayers creates deep copies of all layers with batch lock acquisition.
// W4P.2: Uses batch locking to prevent lock contention and deadlocks.
// Locks are acquired in consistent order (by index) and released together.
func copyLayers(layers []*layer) []LayerSnapshot {
	if len(layers) == 0 {
		return make([]LayerSnapshot, 0)
	}
	return copyLayersWithBatchLock(layers)
}

// copyLayersWithBatchLock acquires all layer locks before copying.
// This prevents O(n) lock acquisitions and ensures consistent snapshot state.
func copyLayersWithBatchLock(layers []*layer) []LayerSnapshot {
	// Acquire all read locks in order (prevents deadlocks)
	for _, l := range layers {
		if l != nil {
			l.mu.RLock()
		}
	}
	// Defer release of all locks in reverse order
	defer func() {
		for i := len(layers) - 1; i >= 0; i-- {
			if layers[i] != nil {
				layers[i].mu.RUnlock()
			}
		}
	}()

	// Copy all layer data while holding all locks
	snapshots := make([]LayerSnapshot, len(layers))
	for i, l := range layers {
		snapshots[i] = copyLayerNodesUnlocked(l)
	}
	return snapshots
}

// copyLayerNodesUnlocked extracts nodes without acquiring locks.
// Caller must hold the layer's read lock.
func copyLayerNodesUnlocked(l *layer) LayerSnapshot {
	if l == nil {
		return LayerSnapshot{Nodes: make(map[string][]string)}
	}
	nodes := make(map[string][]string, len(l.nodes))
	for id, node := range l.nodes {
		neighborIDs := node.neighbors.GetIDs()
		nodes[id] = copyStringSlice(neighborIDs)
	}
	return LayerSnapshot{Nodes: nodes}
}

// copyVectors creates a deep copy of the vectors map.
func copyVectors(vectors map[string][]float32) map[string][]float32 {
	result := make(map[string][]float32, len(vectors))
	for id, vec := range vectors {
		result[id] = copyFloat32Slice(vec)
	}
	return result
}

// copyFloat32Slice creates a deep copy of a float32 slice.
func copyFloat32Slice(src []float32) []float32 {
	if src == nil {
		return nil
	}
	dst := make([]float32, len(src))
	copy(dst, src)
	return dst
}

// copyMagnitudes creates a copy of the magnitudes map.
func copyMagnitudes(magnitudes map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(magnitudes))
	for id, mag := range magnitudes {
		result[id] = mag
	}
	return result
}

// copyDomains creates a copy of the domains map.
func copyDomains(domains map[string]vectorgraphdb.Domain) map[string]vectorgraphdb.Domain {
	result := make(map[string]vectorgraphdb.Domain, len(domains))
	for id, domain := range domains {
		result[id] = domain
	}
	return result
}

// copyNodeTypes creates a copy of the node types map.
func copyNodeTypes(nodeTypes map[string]vectorgraphdb.NodeType) map[string]vectorgraphdb.NodeType {
	result := make(map[string]vectorgraphdb.NodeType, len(nodeTypes))
	for id, nodeType := range nodeTypes {
		result[id] = nodeType
	}
	return result
}

// AcquireReader increments the reader count.
// Returns the new reader count.
func (s *HNSWSnapshot) AcquireReader() int32 {
	return s.readers.Add(1)
}

// ReleaseReader decrements the reader count if positive.
// Returns the new reader count and true if successful, or current count
// and false if already at zero (over-release attempt).
// Uses compare-and-swap for thread-safe underflow protection.
func (s *HNSWSnapshot) ReleaseReader() (int32, bool) {
	for {
		current := s.readers.Load()
		if current <= 0 {
			return current, false
		}
		if s.readers.CompareAndSwap(current, current-1) {
			return current - 1, true
		}
		// CAS failed, another goroutine modified the counter, retry
	}
}

// ReaderCount returns the current number of active readers.
func (s *HNSWSnapshot) ReaderCount() int32 {
	return s.readers.Load()
}

// IsEmpty returns true if the snapshot contains no vectors.
func (s *HNSWSnapshot) IsEmpty() bool {
	return len(s.Vectors) == 0
}

// Size returns the number of vectors in the snapshot.
func (s *HNSWSnapshot) Size() int {
	return len(s.Vectors)
}

// GetVector returns a copy of the vector for the given ID.
// Returns nil if the vector does not exist.
func (s *HNSWSnapshot) GetVector(id string) []float32 {
	vec, exists := s.Vectors[id]
	if !exists {
		return nil
	}
	return copyFloat32Slice(vec)
}

// GetMagnitude returns the magnitude for the given ID.
// Returns 0 and false if the ID does not exist.
func (s *HNSWSnapshot) GetMagnitude(id string) (float64, bool) {
	mag, exists := s.Magnitudes[id]
	return mag, exists
}

// GetLayer returns the layer snapshot at the given level.
// Returns nil if the level is out of bounds.
func (s *HNSWSnapshot) GetLayer(level int) *LayerSnapshot {
	if level < 0 || level >= len(s.Layers) {
		return nil
	}
	return &s.Layers[level]
}

// LayerCount returns the number of layers in the snapshot.
func (s *HNSWSnapshot) LayerCount() int {
	return len(s.Layers)
}

// ContainsVector checks if a vector with the given ID exists.
func (s *HNSWSnapshot) ContainsVector(id string) bool {
	_, exists := s.Vectors[id]
	return exists
}
