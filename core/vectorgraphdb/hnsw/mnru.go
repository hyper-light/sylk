// Package hnsw implements HNSW graph operations.
//
// MN-RU: Mutual Neighbor Replaced Update
//
// This file implements the MN-RU algorithm for safe incremental HNSW updates.
// Standard HNSW updates can create "unreachable" nodes when a vector is updated
// and its connections are rebuilt. MN-RU prevents this by:
//
//  1. Finding all nodes that point TO the updated node (reverse edges)
//  2. Updating the vector and rebuilding outgoing connections
//  3. Checking if incoming nodes still have a path to the updated node
//  4. Re-establishing connections for nodes that would become unreachable
//
// Reference: GRAPH_OPTIMIZATIONS.md lines 1055-1103
package hnsw

import (
	"sort"
	"sync"
)

// MNRUUpdater handles safe incremental HNSW updates using the
// Mutual Neighbor Replaced Update algorithm.
type MNRUUpdater struct {
	mu    sync.RWMutex
	index *Index
}

// NewMNRUUpdater creates a new MNRU updater for the given index.
func NewMNRUUpdater(index *Index) *MNRUUpdater {
	return &MNRUUpdater{index: index}
}

// UpdateVector safely updates a vector in the index while preventing unreachable nodes.
// This implements the MN-RU algorithm:
//  1. Find all nodes pointing to this node (incoming edges)
//  2. Update the vector and magnitude
//  3. Rebuild connections for the updated node
//  4. Re-establish connections for any nodes that lost their path
func (u *MNRUUpdater) UpdateVector(id string, newVector []float32) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.index == nil {
		return ErrIndexEmpty
	}

	u.index.mu.Lock()
	defer u.index.mu.Unlock()

	oldVector, exists := u.index.vectors[id]
	if !exists {
		return ErrNodeNotFound
	}

	// Step 1: Find all nodes that point TO this node (reverse edges)
	incomingNodes := u.findIncomingNodesLocked(id)

	// Step 2: Update the vector
	u.index.vectors[id] = newVector
	u.index.magnitudes[id] = Magnitude(newVector)

	// Step 3: Rebuild outgoing connections for this node
	nodeLevel, hasLevel := u.index.nodeLevels[id]
	if hasLevel {
		u.rebuildConnectionsLocked(id, newVector, nodeLevel)
	}

	for _, incomingID := range incomingNodes {
		if !u.isStillConnectedLocked(incomingID, id) {
			u.maybeReconnectLocked(incomingID, id, oldVector)
		}
	}

	u.repairGlobalConnectivityLocked()

	return nil
}

// findIncomingNodesLocked finds all nodes that have targetID as a neighbor.
// Caller must hold u.index.mu lock.
func (u *MNRUUpdater) findIncomingNodesLocked(targetID string) []string {
	incoming := make([]string, 0)
	seen := make(map[string]bool)

	for _, layer := range u.index.layers {
		if layer == nil {
			continue
		}
		// Find nodes pointing to target within this layer
		pointing := layer.findNodesPointingTo(targetID)
		for _, nodeID := range pointing {
			if !seen[nodeID] {
				incoming = append(incoming, nodeID)
				seen[nodeID] = true
			}
		}
	}

	return incoming
}

// rebuildConnectionsLocked rebuilds the outgoing connections for a node after its vector changed.
// Caller must hold u.index.mu lock.
func (u *MNRUUpdater) rebuildConnectionsLocked(id string, newVector []float32, maxLevel int) {
	newMag := Magnitude(newVector)

	for level := 0; level <= maxLevel && level < len(u.index.layers); level++ {
		layer := u.index.layers[level]
		if layer == nil {
			continue
		}

		// Gather candidates from all nodes in this layer
		candidates := make([]SearchResult, 0)
		nodeIDs := layer.allNodeIDs()
		for _, nodeID := range nodeIDs {
			if nodeID == id {
				continue
			}
			vec, ok := u.index.vectors[nodeID]
			if !ok {
				continue
			}
			mag, ok := u.index.magnitudes[nodeID]
			if !ok {
				mag = Magnitude(vec)
			}
			sim := CosineSimilarity(newVector, vec, newMag, mag)
			candidates = append(candidates, SearchResult{ID: nodeID, Similarity: sim})
		}

		maxNeighbors := u.index.M
		if level == 0 {
			maxNeighbors = u.index.M * 2
		}

		// Sort by similarity descending and select top neighbors
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Similarity > candidates[j].Similarity
		})

		// Rebuild neighbors for this node at this level
		newNeighborIDs := make([]string, 0, maxNeighbors)
		newNeighborDists := make([]float32, 0, maxNeighbors)
		for i := 0; i < len(candidates) && len(newNeighborIDs) < maxNeighbors; i++ {
			newNeighborIDs = append(newNeighborIDs, candidates[i].ID)
			// Distance = 1 - similarity
			newNeighborDists = append(newNeighborDists, float32(1.0-candidates[i].Similarity))
		}

		layer.setNeighbors(id, newNeighborIDs, newNeighborDists)
	}
}

// isStillConnectedLocked checks if fromID still has a connection to toID in any layer.
// Caller must hold u.index.mu lock.
func (u *MNRUUpdater) isStillConnectedLocked(fromID, toID string) bool {
	for _, layer := range u.index.layers {
		if layer == nil {
			continue
		}
		neighbors := layer.getNeighbors(fromID)
		for _, n := range neighbors {
			if n == toID {
				return true
			}
		}
	}
	return false
}

// maybeReconnectLocked considers re-adding a connection from fromID to toID.
// This is called when an update removed a connection that might still be valuable.
// Caller must hold u.index.mu lock.
func (u *MNRUUpdater) maybeReconnectLocked(fromID, toID string, oldTargetVector []float32) {
	fromVec, ok := u.index.vectors[fromID]
	if !ok {
		return
	}
	toVec, ok := u.index.vectors[toID]
	if !ok {
		return
	}

	fromMag := u.index.magnitudes[fromID]
	toMag := u.index.magnitudes[toID]
	if fromMag == 0 {
		fromMag = Magnitude(fromVec)
	}
	if toMag == 0 {
		toMag = Magnitude(toVec)
	}

	newSim := CosineSimilarity(fromVec, toVec, fromMag, toMag)
	newDist := float32(1.0 - newSim)

	nodeLevel, hasLevel := u.index.nodeLevels[fromID]
	if !hasLevel {
		nodeLevel = 0
	}

	for level := 0; level <= nodeLevel && level < len(u.index.layers); level++ {
		layer := u.index.layers[level]
		if layer == nil {
			continue
		}

		maxNeighbors := u.index.M
		if level == 0 {
			maxNeighbors = u.index.M * 2
		}

		// Get current neighbors for fromID
		neighbors := layer.getNeighborsWithDistances(fromID)
		if len(neighbors) < maxNeighbors {
			// Room available - add if not already present
			alreadyNeighbor := false
			for _, n := range neighbors {
				if n.ID == toID {
					alreadyNeighbor = true
					break
				}
			}
			if !alreadyNeighbor {
				layer.addNeighbor(fromID, toID, newDist, maxNeighbors)
			}
		} else {
			// At capacity - replace worst if this is better
			worstIdx := -1
			worstDist := newDist
			for i, n := range neighbors {
				if n.Distance > worstDist {
					worstDist = n.Distance
					worstIdx = i
				}
			}
			if worstIdx >= 0 {
				// Build new neighbor list replacing the worst
				newNeighborIDs := make([]string, 0, len(neighbors))
				newNeighborDists := make([]float32, 0, len(neighbors))
				for i, n := range neighbors {
					if i == worstIdx {
						newNeighborIDs = append(newNeighborIDs, toID)
						newNeighborDists = append(newNeighborDists, newDist)
					} else {
						newNeighborIDs = append(newNeighborIDs, n.ID)
						newNeighborDists = append(newNeighborDists, n.Distance)
					}
				}
				layer.setNeighbors(fromID, newNeighborIDs, newNeighborDists)
			}
		}
	}
}

func (u *MNRUUpdater) repairGlobalConnectivityLocked() {
	if u.index.entryPoint == "" {
		return
	}

	reachable := make(map[string]bool)
	queue := []string{u.index.entryPoint}
	reachable[u.index.entryPoint] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, layer := range u.index.layers {
			if layer == nil {
				continue
			}
			for _, neighbor := range layer.getNeighbors(current) {
				if !reachable[neighbor] {
					reachable[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
	}

	for orphanID := range u.index.vectors {
		if reachable[orphanID] {
			continue
		}

		orphanVec := u.index.vectors[orphanID]
		orphanMag := u.index.magnitudes[orphanID]
		if orphanMag == 0 {
			orphanMag = Magnitude(orphanVec)
		}

		var bestID string
		bestSim := float64(-1)
		for reachableID := range reachable {
			vec := u.index.vectors[reachableID]
			mag := u.index.magnitudes[reachableID]
			if mag == 0 {
				mag = Magnitude(vec)
			}
			sim := CosineSimilarity(orphanVec, vec, orphanMag, mag)
			if sim > bestSim {
				bestSim = sim
				bestID = reachableID
			}
		}

		if bestID == "" {
			continue
		}

		dist := float32(1.0 - bestSim)
		orphanLevel := u.index.nodeLevels[orphanID]
		bestLevel := u.index.nodeLevels[bestID]
		maxLevel := min(orphanLevel, bestLevel)

		for level := 0; level <= maxLevel && level < len(u.index.layers); level++ {
			layer := u.index.layers[level]
			if layer == nil {
				continue
			}

			maxNeighbors := u.index.M
			if level == 0 {
				maxNeighbors = u.index.M * 2
			}

			layer.addNeighbor(bestID, orphanID, dist, maxNeighbors)
			layer.addNeighbor(orphanID, bestID, dist, maxNeighbors)
		}

		reachable[orphanID] = true
	}
}

func (u *MNRUUpdater) DeleteVector(id string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.index == nil {
		return ErrIndexEmpty
	}

	u.index.mu.Lock()
	defer u.index.mu.Unlock()

	if _, exists := u.index.vectors[id]; !exists {
		return ErrNodeNotFound
	}

	// Find nodes that point to this node before deletion
	incomingNodes := u.findIncomingNodesLocked(id)

	// Remove from all layers
	for _, layer := range u.index.layers {
		if layer == nil {
			continue
		}
		// Remove node from layer
		layer.removeNode(id)
		// Remove as neighbor from all nodes (already done by removeNode for this layer's nodes,
		// but we need to clean references in other nodes)
		for _, nodeID := range layer.allNodeIDs() {
			layer.removeNeighbor(nodeID, id)
		}
	}

	// Remove from maps
	delete(u.index.vectors, id)
	delete(u.index.magnitudes, id)
	delete(u.index.nodeLevels, id)
	delete(u.index.domains, id)
	delete(u.index.nodeTypes, id)

	// Update entry point if needed
	if u.index.entryPoint == id {
		u.selectNewEntryPointLocked()
	}

	// Repair connections for nodes that pointed to the deleted node
	for _, incomingID := range incomingNodes {
		if incomingID != id { // Skip the deleted node itself
			u.repairConnectionsLocked(incomingID)
		}
	}

	return nil
}

// selectNewEntryPointLocked selects a new entry point after deletion.
// Caller must hold u.index.mu lock.
func (u *MNRUUpdater) selectNewEntryPointLocked() {
	u.index.entryPoint = ""
	for level := u.index.maxLevel; level >= 0; level-- {
		if level >= len(u.index.layers) {
			continue
		}
		ids := u.index.layers[level].allNodeIDs()
		if len(ids) > 0 {
			u.index.entryPoint = ids[0]
			u.index.maxLevel = level
			return
		}
	}
	u.index.maxLevel = -1
}

// repairConnectionsLocked adds new neighbors to a node that lost connections.
// This ensures the node maintains sufficient connectivity after deletions.
// Caller must hold u.index.mu lock.
func (u *MNRUUpdater) repairConnectionsLocked(nodeID string) {
	nodeVec, ok := u.index.vectors[nodeID]
	if !ok {
		return
	}
	nodeMag := u.index.magnitudes[nodeID]
	if nodeMag == 0 {
		nodeMag = Magnitude(nodeVec)
	}

	nodeLevel, hasLevel := u.index.nodeLevels[nodeID]
	if !hasLevel {
		nodeLevel = 0
	}

	for level := 0; level <= nodeLevel && level < len(u.index.layers); level++ {
		layer := u.index.layers[level]
		if layer == nil {
			continue
		}

		maxNeighbors := u.index.M
		if level == 0 {
			maxNeighbors = u.index.M * 2
		}

		currentNeighbors := layer.getNeighborsWithDistances(nodeID)
		if len(currentNeighbors) >= maxNeighbors {
			continue // Already at capacity
		}

		// Build set of existing neighbors for fast lookup
		existingNeighbors := make(map[string]bool)
		for _, n := range currentNeighbors {
			existingNeighbors[n.ID] = true
		}

		// Find candidates from other nodes in this layer
		candidates := make([]SearchResult, 0)
		for _, otherID := range layer.allNodeIDs() {
			if otherID == nodeID || existingNeighbors[otherID] {
				continue
			}
			otherVec, ok := u.index.vectors[otherID]
			if !ok {
				continue
			}
			otherMag := u.index.magnitudes[otherID]
			if otherMag == 0 {
				otherMag = Magnitude(otherVec)
			}
			sim := CosineSimilarity(nodeVec, otherVec, nodeMag, otherMag)
			candidates = append(candidates, SearchResult{ID: otherID, Similarity: sim})
		}

		// Sort by similarity descending
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Similarity > candidates[j].Similarity
		})

		// Add best candidates up to capacity
		for _, c := range candidates {
			if len(currentNeighbors) >= maxNeighbors {
				break
			}
			dist := float32(1.0 - c.Similarity)
			layer.addNeighbor(nodeID, c.ID, dist, maxNeighbors)
			currentNeighbors = append(currentNeighbors, Neighbor{ID: c.ID, Distance: dist})
		}
	}
}

// FindIncomingNodes returns all nodes that have targetID as a neighbor.
// This is useful for analyzing graph connectivity.
func (u *MNRUUpdater) FindIncomingNodes(targetID string) []string {
	u.mu.RLock()
	defer u.mu.RUnlock()

	if u.index == nil {
		return nil
	}

	u.index.mu.RLock()
	defer u.index.mu.RUnlock()

	return u.findIncomingNodesLocked(targetID)
}

// ValidateConnectivity checks that all nodes in the index are reachable from the entry point.
// Returns a list of unreachable node IDs (empty if all nodes are reachable).
func (u *MNRUUpdater) ValidateConnectivity() []string {
	u.mu.RLock()
	defer u.mu.RUnlock()

	if u.index == nil {
		return nil
	}

	u.index.mu.RLock()
	defer u.index.mu.RUnlock()

	if u.index.entryPoint == "" {
		return nil // Empty index
	}

	// BFS from entry point to find all reachable nodes
	reachable := make(map[string]bool)
	queue := []string{u.index.entryPoint}
	reachable[u.index.entryPoint] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Check all layers for neighbors
		for _, layer := range u.index.layers {
			if layer == nil {
				continue
			}
			neighbors := layer.getNeighbors(current)
			for _, neighbor := range neighbors {
				if !reachable[neighbor] {
					reachable[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
	}

	// Find unreachable nodes
	unreachable := make([]string, 0)
	for id := range u.index.vectors {
		if !reachable[id] {
			unreachable = append(unreachable, id)
		}
	}

	return unreachable
}
