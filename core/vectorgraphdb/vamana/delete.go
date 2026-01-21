package vamana

import (
	"sync"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

type TombstoneSet struct {
	deleted map[uint32]struct{}
	mu      sync.RWMutex
}

func NewTombstoneSet() *TombstoneSet {
	return &TombstoneSet{
		deleted: make(map[uint32]struct{}),
	}
}

func (t *TombstoneSet) Mark(id uint32) {
	t.mu.Lock()
	t.deleted[id] = struct{}{}
	t.mu.Unlock()
}

func (t *TombstoneSet) Unmark(id uint32) {
	t.mu.Lock()
	delete(t.deleted, id)
	t.mu.Unlock()
}

func (t *TombstoneSet) IsDeleted(id uint32) bool {
	t.mu.RLock()
	_, deleted := t.deleted[id]
	t.mu.RUnlock()
	return deleted
}

func (t *TombstoneSet) Count() int {
	t.mu.RLock()
	n := len(t.deleted)
	t.mu.RUnlock()
	return n
}

func (t *TombstoneSet) All() []uint32 {
	t.mu.RLock()
	result := make([]uint32, 0, len(t.deleted))
	for id := range t.deleted {
		result = append(result, id)
	}
	t.mu.RUnlock()
	return result
}

func (t *TombstoneSet) Clear() {
	t.mu.Lock()
	t.deleted = make(map[uint32]struct{})
	t.mu.Unlock()
}

// VamanaDelete performs lazy deletion by marking the node as tombstoned
// and removing it from all neighbor lists.
func VamanaDelete(
	nodeID uint32,
	tombstones *TombstoneSet,
	graph *storage.GraphStore,
	vectors *storage.VectorStore,
	magCache *MagnitudeCache,
	config VamanaConfig,
) error {
	if tombstones == nil || graph == nil {
		return nil
	}

	tombstones.Mark(nodeID)

	neighbors := graph.GetNeighbors(nodeID)
	if len(neighbors) == 0 {
		return nil
	}

	distFn := makeDistanceFunc(vectors, magCache)

	for _, neighborID := range neighbors {
		if tombstones.IsDeleted(neighborID) {
			continue
		}

		neighborList := graph.GetNeighbors(neighborID)
		if len(neighborList) == 0 {
			continue
		}

		filtered := make([]uint32, 0, len(neighborList))
		for _, n := range neighborList {
			if n != nodeID && !tombstones.IsDeleted(n) {
				filtered = append(filtered, n)
			}
		}

		if len(filtered) < len(neighborList) {
			if len(filtered) < config.R/2 && vectors != nil {
				filtered = repairNeighborhood(neighborID, filtered, tombstones, graph, vectors, magCache, config, distFn)
			}
			if err := graph.SetNeighbors(neighborID, filtered); err != nil {
				return err
			}
		}
	}

	if err := graph.SetNeighbors(nodeID, nil); err != nil {
		return err
	}

	return nil
}

func repairNeighborhood(
	nodeID uint32,
	currentNeighbors []uint32,
	tombstones *TombstoneSet,
	graph *storage.GraphStore,
	vectors *storage.VectorStore,
	magCache *MagnitudeCache,
	config VamanaConfig,
	distFn DistanceFunc,
) []uint32 {
	candidates := make(map[uint32]struct{}, config.R*2)

	for _, n := range currentNeighbors {
		candidates[n] = struct{}{}
	}

	for _, neighbor := range currentNeighbors {
		secondHop := graph.GetNeighbors(neighbor)
		for _, n := range secondHop {
			if n != nodeID && !tombstones.IsDeleted(n) {
				candidates[n] = struct{}{}
			}
		}
	}

	if len(candidates) <= len(currentNeighbors) {
		return currentNeighbors
	}

	candidateList := make([]uint32, 0, len(candidates))
	for id := range candidates {
		candidateList = append(candidateList, id)
	}

	return RobustPrune(nodeID, candidateList, config.Alpha, config.R, distFn)
}

// VamanaDeleteBatch deletes multiple nodes, returning first error encountered.
func VamanaDeleteBatch(
	nodeIDs []uint32,
	tombstones *TombstoneSet,
	graph *storage.GraphStore,
	vectors *storage.VectorStore,
	magCache *MagnitudeCache,
	config VamanaConfig,
) error {
	for _, nodeID := range nodeIDs {
		if err := VamanaDelete(nodeID, tombstones, graph, vectors, magCache, config); err != nil {
			return err
		}
	}
	return nil
}

// CompactTombstones returns a list of non-deleted node IDs for index compaction.
func CompactTombstones(totalNodes uint32, tombstones *TombstoneSet) []uint32 {
	if tombstones == nil {
		result := make([]uint32, totalNodes)
		for i := uint32(0); i < totalNodes; i++ {
			result[i] = i
		}
		return result
	}

	result := make([]uint32, 0, int(totalNodes)-tombstones.Count())
	for i := uint32(0); i < totalNodes; i++ {
		if !tombstones.IsDeleted(i) {
			result = append(result, i)
		}
	}
	return result
}
