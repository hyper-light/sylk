package hnsw

import (
	"sort"
	"sync"
)

type Neighbor struct {
	ID       uint32
	Distance float32
}

type NeighborSet struct {
	neighbors   map[uint32]float32
	sortedCache []Neighbor
	dirty       bool
}

func NewNeighborSet() *NeighborSet {
	return &NeighborSet{
		neighbors: make(map[uint32]float32),
		dirty:     true,
	}
}

func NewNeighborSetWithCapacity(capacity int) *NeighborSet {
	return &NeighborSet{
		neighbors: make(map[uint32]float32, capacity),
		dirty:     true,
	}
}

func (ns *NeighborSet) Add(id uint32, dist float32) {
	ns.neighbors[id] = dist
	ns.dirty = true
}

func (ns *NeighborSet) Remove(id uint32) {
	if _, exists := ns.neighbors[id]; exists {
		delete(ns.neighbors, id)
		ns.dirty = true
	}
}

func (ns *NeighborSet) Contains(id uint32) bool {
	_, exists := ns.neighbors[id]
	return exists
}

func (ns *NeighborSet) GetDistance(id uint32) (float32, bool) {
	dist, exists := ns.neighbors[id]
	return dist, exists
}

func (ns *NeighborSet) GetSortedNeighbors() []Neighbor {
	ns.refreshCacheIfDirty()
	result := make([]Neighbor, len(ns.sortedCache))
	copy(result, ns.sortedCache)
	return result
}

func (ns *NeighborSet) refreshCacheIfDirty() {
	if !ns.dirty {
		return
	}
	ns.sortedCache = make([]Neighbor, 0, len(ns.neighbors))
	for id, dist := range ns.neighbors {
		ns.sortedCache = append(ns.sortedCache, Neighbor{ID: id, Distance: dist})
	}
	sort.Slice(ns.sortedCache, func(i, j int) bool {
		return ns.sortedCache[i].Distance < ns.sortedCache[j].Distance
	})
	ns.dirty = false
}

func (ns *NeighborSet) GetSortedNeighborsDescending() []Neighbor {
	ns.refreshCacheIfDirty()
	n := len(ns.sortedCache)
	result := make([]Neighbor, n)
	for i := 0; i < n; i++ {
		result[i] = ns.sortedCache[n-1-i]
	}
	return result
}

func (ns *NeighborSet) GetTopK(k int) []Neighbor {
	sorted := ns.GetSortedNeighbors()
	if len(sorted) <= k {
		return sorted
	}
	return sorted[:k]
}

func (ns *NeighborSet) GetIDs() []uint32 {
	ids := make([]uint32, 0, len(ns.neighbors))
	for id := range ns.neighbors {
		ids = append(ids, id)
	}
	return ids
}

func (ns *NeighborSet) Size() int {
	return len(ns.neighbors)
}

func (ns *NeighborSet) Clear() {
	ns.neighbors = make(map[uint32]float32)
	ns.sortedCache = nil
	ns.dirty = true
}

func (ns *NeighborSet) Clone() *NeighborSet {
	clone := NewNeighborSetWithCapacity(len(ns.neighbors))
	for id, dist := range ns.neighbors {
		clone.neighbors[id] = dist
	}
	return clone
}

func (ns *NeighborSet) Merge(other *NeighborSet) {
	for id, dist := range other.neighbors {
		ns.neighbors[id] = dist
	}
	if len(other.neighbors) > 0 {
		ns.dirty = true
	}
}

func (ns *NeighborSet) TrimToSize(maxSize int) {
	if len(ns.neighbors) <= maxSize {
		return
	}

	sorted := ns.GetSortedNeighbors()
	ns.neighbors = make(map[uint32]float32, maxSize)
	for i := 0; i < maxSize && i < len(sorted); i++ {
		ns.neighbors[sorted[i].ID] = sorted[i].Distance
	}
	ns.dirty = true
}

func (ns *NeighborSet) ForEach(fn func(id uint32, dist float32)) {
	for id, dist := range ns.neighbors {
		fn(id, dist)
	}
}

type ConcurrentNeighborSet struct {
	mu          sync.RWMutex
	neighbors   map[uint32]float32
	sortedCache []Neighbor
	dirty       bool
}

func NewConcurrentNeighborSet() *ConcurrentNeighborSet {
	return &ConcurrentNeighborSet{
		neighbors: make(map[uint32]float32),
		dirty:     true,
	}
}

func NewConcurrentNeighborSetWithCapacity(capacity int) *ConcurrentNeighborSet {
	return &ConcurrentNeighborSet{
		neighbors: make(map[uint32]float32, capacity),
		dirty:     true,
	}
}

func (cns *ConcurrentNeighborSet) Add(id uint32, dist float32) {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	cns.neighbors[id] = dist
	cns.dirty = true
}

func (cns *ConcurrentNeighborSet) Remove(id uint32) {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	if _, exists := cns.neighbors[id]; exists {
		delete(cns.neighbors, id)
		cns.dirty = true
	}
}

func (cns *ConcurrentNeighborSet) Contains(id uint32) bool {
	cns.mu.RLock()
	defer cns.mu.RUnlock()
	_, exists := cns.neighbors[id]
	return exists
}

func (cns *ConcurrentNeighborSet) GetDistance(id uint32) (float32, bool) {
	cns.mu.RLock()
	defer cns.mu.RUnlock()
	dist, exists := cns.neighbors[id]
	return dist, exists
}

func (cns *ConcurrentNeighborSet) GetSortedNeighbors() []Neighbor {
	cns.mu.Lock()
	defer cns.mu.Unlock()

	cns.refreshCacheIfDirtyLocked()
	result := make([]Neighbor, len(cns.sortedCache))
	copy(result, cns.sortedCache)
	return result
}

func (cns *ConcurrentNeighborSet) refreshCacheIfDirtyLocked() {
	if !cns.dirty {
		return
	}
	cns.sortedCache = make([]Neighbor, 0, len(cns.neighbors))
	for id, dist := range cns.neighbors {
		cns.sortedCache = append(cns.sortedCache, Neighbor{ID: id, Distance: dist})
	}
	sort.Slice(cns.sortedCache, func(i, j int) bool {
		return cns.sortedCache[i].Distance < cns.sortedCache[j].Distance
	})
	cns.dirty = false
}

func (cns *ConcurrentNeighborSet) GetSortedNeighborsDescending() []Neighbor {
	cns.mu.Lock()
	defer cns.mu.Unlock()

	cns.refreshCacheIfDirtyLocked()
	n := len(cns.sortedCache)
	result := make([]Neighbor, n)
	for i := 0; i < n; i++ {
		result[i] = cns.sortedCache[n-1-i]
	}
	return result
}

func (cns *ConcurrentNeighborSet) GetTopK(k int) []Neighbor {
	sorted := cns.GetSortedNeighbors()
	if len(sorted) <= k {
		return sorted
	}
	return sorted[:k]
}

func (cns *ConcurrentNeighborSet) GetIDs() []uint32 {
	cns.mu.RLock()
	defer cns.mu.RUnlock()

	ids := make([]uint32, 0, len(cns.neighbors))
	for id := range cns.neighbors {
		ids = append(ids, id)
	}
	return ids
}

func (cns *ConcurrentNeighborSet) Size() int {
	cns.mu.RLock()
	defer cns.mu.RUnlock()
	return len(cns.neighbors)
}

func (cns *ConcurrentNeighborSet) Clear() {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	cns.neighbors = make(map[uint32]float32)
	cns.sortedCache = nil
	cns.dirty = true
}

func (cns *ConcurrentNeighborSet) Clone() *ConcurrentNeighborSet {
	cns.mu.RLock()
	defer cns.mu.RUnlock()

	clone := NewConcurrentNeighborSetWithCapacity(len(cns.neighbors))
	for id, dist := range cns.neighbors {
		clone.neighbors[id] = dist
	}
	return clone
}

func (cns *ConcurrentNeighborSet) Merge(other *ConcurrentNeighborSet) {
	other.mu.RLock()
	otherCopy := make(map[uint32]float32, len(other.neighbors))
	for id, dist := range other.neighbors {
		otherCopy[id] = dist
	}
	other.mu.RUnlock()

	cns.mu.Lock()
	defer cns.mu.Unlock()
	for id, dist := range otherCopy {
		cns.neighbors[id] = dist
	}
	if len(otherCopy) > 0 {
		cns.dirty = true
	}
}

func (cns *ConcurrentNeighborSet) TrimToSize(maxSize int) {
	cns.mu.Lock()
	defer cns.mu.Unlock()

	if len(cns.neighbors) <= maxSize {
		return
	}

	cns.refreshCacheIfDirtyLocked()

	cns.neighbors = make(map[uint32]float32, maxSize)
	for i := 0; i < maxSize && i < len(cns.sortedCache); i++ {
		cns.neighbors[cns.sortedCache[i].ID] = cns.sortedCache[i].Distance
	}
	cns.dirty = true
}

func (cns *ConcurrentNeighborSet) ForEach(fn func(id uint32, dist float32)) {
	cns.mu.RLock()
	defer cns.mu.RUnlock()
	for id, dist := range cns.neighbors {
		fn(id, dist)
	}
}

func (cns *ConcurrentNeighborSet) AddIfAbsent(id uint32, dist float32) bool {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	if _, exists := cns.neighbors[id]; exists {
		return false
	}
	cns.neighbors[id] = dist
	cns.dirty = true
	return true
}

func (cns *ConcurrentNeighborSet) UpdateDistance(id uint32, dist float32) bool {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	if _, exists := cns.neighbors[id]; !exists {
		return false
	}
	cns.neighbors[id] = dist
	cns.dirty = true
	return true
}

func (cns *ConcurrentNeighborSet) AddWithLimit(id uint32, dist float32, maxSize int) bool {
	cns.mu.Lock()
	defer cns.mu.Unlock()

	if _, exists := cns.neighbors[id]; exists {
		cns.neighbors[id] = dist
		cns.dirty = true
		return true
	}

	if len(cns.neighbors) < maxSize {
		cns.neighbors[id] = dist
		cns.dirty = true
		return true
	}

	var worstID uint32
	var worstDist float32 = -1
	for existingID, existingDist := range cns.neighbors {
		if existingDist > worstDist {
			worstID = existingID
			worstDist = existingDist
		}
	}

	if dist < worstDist {
		delete(cns.neighbors, worstID)
		cns.neighbors[id] = dist
		cns.dirty = true
		return true
	}

	return false
}
