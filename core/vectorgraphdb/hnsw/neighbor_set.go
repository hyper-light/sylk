package hnsw

// W4L.3: Neighbor Set Data Structures for HNSW
//
// This file provides efficient neighbor management for HNSW nodes. The key challenge
// is balancing fast membership checks, sorted retrieval, and capacity management.
//
// Design rationale:
//   - Map-based storage: O(1) Contains() checks during graph construction
//   - On-demand sorting: Neighbors sorted by distance only when needed
//   - Capacity limits: HNSW requires M (or M*2) neighbors per node maximum
//   - Worst-neighbor replacement: When at capacity, replace furthest neighbor
//
// Two variants are provided:
//   - NeighborSet: Not thread-safe, for single-threaded operations
//   - ConcurrentNeighborSet: Thread-safe with RWMutex, for concurrent access

import (
	"sort"
	"sync"
)

// Neighbor represents a neighbor node with its distance from the query.
// W4L.3: Distance is typically 1-similarity for cosine similarity (lower is better).
type Neighbor struct {
	ID       string
	Distance float32 // Lower distance = more similar (1 - cosine_similarity)
}

// NeighborSet provides O(1) lookup for neighbor membership while supporting
// sorted retrieval. It replaces O(n) slice operations used in HNSW layers.
//
// This type is NOT thread-safe. For concurrent access, use ConcurrentNeighborSet.
type NeighborSet struct {
	neighbors map[string]float32
}

// NewNeighborSet creates a new empty NeighborSet.
func NewNeighborSet() *NeighborSet {
	return &NeighborSet{
		neighbors: make(map[string]float32),
	}
}

// NewNeighborSetWithCapacity creates a new NeighborSet with preallocated capacity.
func NewNeighborSetWithCapacity(capacity int) *NeighborSet {
	return &NeighborSet{
		neighbors: make(map[string]float32, capacity),
	}
}

// Add adds a neighbor with the given distance.
// If the neighbor already exists, the distance is updated.
func (ns *NeighborSet) Add(id string, dist float32) {
	ns.neighbors[id] = dist
}

// Remove removes a neighbor from the set.
// No-op if the neighbor doesn't exist.
func (ns *NeighborSet) Remove(id string) {
	delete(ns.neighbors, id)
}

// Contains returns true if the neighbor exists in the set.
// This is O(1) compared to O(n) for slice-based containment checks.
func (ns *NeighborSet) Contains(id string) bool {
	_, exists := ns.neighbors[id]
	return exists
}

// GetDistance returns the distance for a neighbor.
// Returns 0 and false if the neighbor doesn't exist.
func (ns *NeighborSet) GetDistance(id string) (float32, bool) {
	dist, exists := ns.neighbors[id]
	return dist, exists
}

// GetSortedNeighbors returns all neighbors sorted by distance (ascending).
// Neighbors with smaller distances come first.
func (ns *NeighborSet) GetSortedNeighbors() []Neighbor {
	result := make([]Neighbor, 0, len(ns.neighbors))
	for id, dist := range ns.neighbors {
		result = append(result, Neighbor{ID: id, Distance: dist})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Distance < result[j].Distance
	})
	return result
}

// GetSortedNeighborsDescending returns all neighbors sorted by distance (descending).
// Neighbors with larger distances come first.
func (ns *NeighborSet) GetSortedNeighborsDescending() []Neighbor {
	result := make([]Neighbor, 0, len(ns.neighbors))
	for id, dist := range ns.neighbors {
		result = append(result, Neighbor{ID: id, Distance: dist})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Distance > result[j].Distance
	})
	return result
}

// GetTopK returns the K neighbors with smallest distances.
// If the set has fewer than K neighbors, all neighbors are returned.
func (ns *NeighborSet) GetTopK(k int) []Neighbor {
	sorted := ns.GetSortedNeighbors()
	if len(sorted) <= k {
		return sorted
	}
	return sorted[:k]
}

// GetIDs returns all neighbor IDs (unordered).
func (ns *NeighborSet) GetIDs() []string {
	ids := make([]string, 0, len(ns.neighbors))
	for id := range ns.neighbors {
		ids = append(ids, id)
	}
	return ids
}

// Size returns the number of neighbors in the set.
func (ns *NeighborSet) Size() int {
	return len(ns.neighbors)
}

// Clear removes all neighbors from the set.
func (ns *NeighborSet) Clear() {
	ns.neighbors = make(map[string]float32)
}

// Clone creates a deep copy of the NeighborSet.
func (ns *NeighborSet) Clone() *NeighborSet {
	clone := NewNeighborSetWithCapacity(len(ns.neighbors))
	for id, dist := range ns.neighbors {
		clone.neighbors[id] = dist
	}
	return clone
}

// Merge adds all neighbors from another set.
// If a neighbor exists in both sets, the distance from the other set is used.
func (ns *NeighborSet) Merge(other *NeighborSet) {
	for id, dist := range other.neighbors {
		ns.neighbors[id] = dist
	}
}

// TrimToSize removes neighbors to ensure the set has at most maxSize elements.
// Keeps the neighbors with smallest distances.
func (ns *NeighborSet) TrimToSize(maxSize int) {
	if len(ns.neighbors) <= maxSize {
		return
	}

	sorted := ns.GetSortedNeighbors()
	ns.neighbors = make(map[string]float32, maxSize)
	for i := 0; i < maxSize && i < len(sorted); i++ {
		ns.neighbors[sorted[i].ID] = sorted[i].Distance
	}
}

// ForEach calls the given function for each neighbor.
// Iteration order is not guaranteed.
func (ns *NeighborSet) ForEach(fn func(id string, dist float32)) {
	for id, dist := range ns.neighbors {
		fn(id, dist)
	}
}

// ConcurrentNeighborSet is a thread-safe variant of NeighborSet using RWMutex.
// Use this when the set needs to be accessed from multiple goroutines.
type ConcurrentNeighborSet struct {
	mu        sync.RWMutex
	neighbors map[string]float32
}

// NewConcurrentNeighborSet creates a new thread-safe NeighborSet.
func NewConcurrentNeighborSet() *ConcurrentNeighborSet {
	return &ConcurrentNeighborSet{
		neighbors: make(map[string]float32),
	}
}

// NewConcurrentNeighborSetWithCapacity creates a new thread-safe NeighborSet with preallocated capacity.
func NewConcurrentNeighborSetWithCapacity(capacity int) *ConcurrentNeighborSet {
	return &ConcurrentNeighborSet{
		neighbors: make(map[string]float32, capacity),
	}
}

// Add adds a neighbor with the given distance.
// If the neighbor already exists, the distance is updated.
func (cns *ConcurrentNeighborSet) Add(id string, dist float32) {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	cns.neighbors[id] = dist
}

// Remove removes a neighbor from the set.
// No-op if the neighbor doesn't exist.
func (cns *ConcurrentNeighborSet) Remove(id string) {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	delete(cns.neighbors, id)
}

// Contains returns true if the neighbor exists in the set.
// This is O(1) compared to O(n) for slice-based containment checks.
func (cns *ConcurrentNeighborSet) Contains(id string) bool {
	cns.mu.RLock()
	defer cns.mu.RUnlock()
	_, exists := cns.neighbors[id]
	return exists
}

// GetDistance returns the distance for a neighbor.
// Returns 0 and false if the neighbor doesn't exist.
func (cns *ConcurrentNeighborSet) GetDistance(id string) (float32, bool) {
	cns.mu.RLock()
	defer cns.mu.RUnlock()
	dist, exists := cns.neighbors[id]
	return dist, exists
}

// GetSortedNeighbors returns all neighbors sorted by distance (ascending).
// Neighbors with smaller distances come first.
func (cns *ConcurrentNeighborSet) GetSortedNeighbors() []Neighbor {
	cns.mu.RLock()
	defer cns.mu.RUnlock()

	result := make([]Neighbor, 0, len(cns.neighbors))
	for id, dist := range cns.neighbors {
		result = append(result, Neighbor{ID: id, Distance: dist})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Distance < result[j].Distance
	})
	return result
}

// GetSortedNeighborsDescending returns all neighbors sorted by distance (descending).
// Neighbors with larger distances come first.
func (cns *ConcurrentNeighborSet) GetSortedNeighborsDescending() []Neighbor {
	cns.mu.RLock()
	defer cns.mu.RUnlock()

	result := make([]Neighbor, 0, len(cns.neighbors))
	for id, dist := range cns.neighbors {
		result = append(result, Neighbor{ID: id, Distance: dist})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Distance > result[j].Distance
	})
	return result
}

// GetTopK returns the K neighbors with smallest distances.
// If the set has fewer than K neighbors, all neighbors are returned.
func (cns *ConcurrentNeighborSet) GetTopK(k int) []Neighbor {
	sorted := cns.GetSortedNeighbors()
	if len(sorted) <= k {
		return sorted
	}
	return sorted[:k]
}

// GetIDs returns all neighbor IDs (unordered).
func (cns *ConcurrentNeighborSet) GetIDs() []string {
	cns.mu.RLock()
	defer cns.mu.RUnlock()

	ids := make([]string, 0, len(cns.neighbors))
	for id := range cns.neighbors {
		ids = append(ids, id)
	}
	return ids
}

// Size returns the number of neighbors in the set.
func (cns *ConcurrentNeighborSet) Size() int {
	cns.mu.RLock()
	defer cns.mu.RUnlock()
	return len(cns.neighbors)
}

// Clear removes all neighbors from the set.
func (cns *ConcurrentNeighborSet) Clear() {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	cns.neighbors = make(map[string]float32)
}

// Clone creates a deep copy of the ConcurrentNeighborSet.
// The returned set is also thread-safe.
func (cns *ConcurrentNeighborSet) Clone() *ConcurrentNeighborSet {
	cns.mu.RLock()
	defer cns.mu.RUnlock()

	clone := NewConcurrentNeighborSetWithCapacity(len(cns.neighbors))
	for id, dist := range cns.neighbors {
		clone.neighbors[id] = dist
	}
	return clone
}

// Merge adds all neighbors from another set.
// If a neighbor exists in both sets, the distance from the other set is used.
func (cns *ConcurrentNeighborSet) Merge(other *ConcurrentNeighborSet) {
	other.mu.RLock()
	otherCopy := make(map[string]float32, len(other.neighbors))
	for id, dist := range other.neighbors {
		otherCopy[id] = dist
	}
	other.mu.RUnlock()

	cns.mu.Lock()
	defer cns.mu.Unlock()
	for id, dist := range otherCopy {
		cns.neighbors[id] = dist
	}
}

// TrimToSize removes neighbors to ensure the set has at most maxSize elements.
// Keeps the neighbors with smallest distances.
func (cns *ConcurrentNeighborSet) TrimToSize(maxSize int) {
	cns.mu.Lock()
	defer cns.mu.Unlock()

	if len(cns.neighbors) <= maxSize {
		return
	}

	// Sort and trim
	result := make([]Neighbor, 0, len(cns.neighbors))
	for id, dist := range cns.neighbors {
		result = append(result, Neighbor{ID: id, Distance: dist})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Distance < result[j].Distance
	})

	cns.neighbors = make(map[string]float32, maxSize)
	for i := 0; i < maxSize && i < len(result); i++ {
		cns.neighbors[result[i].ID] = result[i].Distance
	}
}

// ForEach calls the given function for each neighbor.
// The function is called while holding a read lock, so operations
// that need a write lock will deadlock. Use GetSortedNeighbors
// if you need to modify the set during iteration.
func (cns *ConcurrentNeighborSet) ForEach(fn func(id string, dist float32)) {
	cns.mu.RLock()
	defer cns.mu.RUnlock()
	for id, dist := range cns.neighbors {
		fn(id, dist)
	}
}

// AddIfAbsent adds a neighbor only if it doesn't already exist.
// Returns true if the neighbor was added, false if it already existed.
func (cns *ConcurrentNeighborSet) AddIfAbsent(id string, dist float32) bool {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	if _, exists := cns.neighbors[id]; exists {
		return false
	}
	cns.neighbors[id] = dist
	return true
}

// UpdateDistance updates the distance for an existing neighbor.
// Returns true if the neighbor existed and was updated.
func (cns *ConcurrentNeighborSet) UpdateDistance(id string, dist float32) bool {
	cns.mu.Lock()
	defer cns.mu.Unlock()
	if _, exists := cns.neighbors[id]; !exists {
		return false
	}
	cns.neighbors[id] = dist
	return true
}

// AddWithLimit adds a neighbor if there's room or if it's better than the worst.
// W4L.3: Documented the worst-neighbor replacement strategy for HNSW.
//
// This implements the capacity-limited neighbor selection required by HNSW:
//   1. If neighbor already exists: Update its distance (connection reweighting)
//   2. If under capacity: Add the neighbor directly
//   3. If at capacity: Compare with worst (furthest) neighbor
//      - If new neighbor is closer: Replace the worst neighbor
//      - Otherwise: Reject the new neighbor
//
// The worst-neighbor replacement ensures the set always contains the M closest
// neighbors seen so far, which is essential for HNSW's search quality.
//
// Returns true if the neighbor was added or updated, false if rejected.
func (cns *ConcurrentNeighborSet) AddWithLimit(id string, dist float32, maxSize int) bool {
	cns.mu.Lock()
	defer cns.mu.Unlock()

	// Already exists - update distance (connection may have improved)
	if _, exists := cns.neighbors[id]; exists {
		cns.neighbors[id] = dist
		return true
	}

	// Room available - add directly
	if len(cns.neighbors) < maxSize {
		cns.neighbors[id] = dist
		return true
	}

	// At capacity - find the worst (furthest) neighbor for potential replacement
	var worstID string
	var worstDist float32 = -1
	for existingID, existingDist := range cns.neighbors {
		if existingDist > worstDist {
			worstID = existingID
			worstDist = existingDist
		}
	}

	// Replace worst neighbor if new one is closer (lower distance = better)
	if dist < worstDist {
		delete(cns.neighbors, worstID)
		cns.neighbors[id] = dist
		return true
	}

	return false
}
