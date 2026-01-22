package hnsw

import (
	"sort"
	"sync"
)

type MNRUUpdater struct {
	mu    sync.RWMutex
	index *Index
}

func NewMNRUUpdater(index *Index) *MNRUUpdater {
	return &MNRUUpdater{index: index}
}

func (u *MNRUUpdater) UpdateVector(id string, newVector []float32) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.index == nil {
		return ErrIndexEmpty
	}

	u.index.mu.Lock()
	defer u.index.mu.Unlock()

	internalID, exists := u.index.stringToID[id]
	if !exists {
		return ErrNodeNotFound
	}

	oldVector := u.index.vectors[internalID]
	incomingNodes := u.findIncomingNodesLocked(internalID)

	u.index.vectors[internalID] = newVector
	u.index.magnitudes[internalID] = Magnitude(newVector)

	nodeLevel, hasLevel := u.index.nodeLevels[internalID]
	if hasLevel {
		u.rebuildConnectionsLocked(internalID, newVector, nodeLevel)
	}

	for _, incomingID := range incomingNodes {
		if !u.isStillConnectedLocked(incomingID, internalID) {
			u.maybeReconnectLocked(incomingID, internalID, oldVector)
		}
	}

	u.repairGlobalConnectivityLocked()

	return nil
}

func (u *MNRUUpdater) findIncomingNodesLocked(targetID uint32) []uint32 {
	incoming := make([]uint32, 0)
	seen := make(map[uint32]bool)

	for _, layer := range u.index.layers {
		if layer == nil {
			continue
		}
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

func (u *MNRUUpdater) rebuildConnectionsLocked(id uint32, newVector []float32, maxLevel int) {
	newMag := Magnitude(newVector)

	for level := 0; level <= maxLevel && level < len(u.index.layers); level++ {
		layer := u.index.layers[level]
		if layer == nil {
			continue
		}

		candidates := make([]searchCandidate, 0)
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
			candidates = append(candidates, searchCandidate{id: nodeID, distance: 1.0 - sim})
		}

		maxNeighbors := u.index.M
		if level == 0 {
			maxNeighbors = u.index.M * 2
		}

		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].distance < candidates[j].distance
		})

		newNeighborIDs := make([]uint32, 0, maxNeighbors)
		newNeighborDists := make([]float32, 0, maxNeighbors)
		for i := 0; i < len(candidates) && len(newNeighborIDs) < maxNeighbors; i++ {
			newNeighborIDs = append(newNeighborIDs, candidates[i].id)
			newNeighborDists = append(newNeighborDists, float32(candidates[i].distance))
		}

		layer.setNeighbors(id, newNeighborIDs, newNeighborDists)
	}
}

func (u *MNRUUpdater) isStillConnectedLocked(fromID, toID uint32) bool {
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

func (u *MNRUUpdater) maybeReconnectLocked(fromID, toID uint32, oldTargetVector []float32) {
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

		neighbors := layer.getNeighborsWithDistances(fromID)
		if len(neighbors) < maxNeighbors {
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
			worstIdx := -1
			worstDist := newDist
			for i, n := range neighbors {
				if n.Distance > worstDist {
					worstDist = n.Distance
					worstIdx = i
				}
			}
			if worstIdx >= 0 {
				newNeighborIDs := make([]uint32, 0, len(neighbors))
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
	if u.index.entryPoint == invalidNodeID {
		return
	}

	reachable := make(map[uint32]bool)
	queue := []uint32{u.index.entryPoint}
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

		var bestID uint32
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

		if bestID == invalidNodeID {
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

	internalID, exists := u.index.stringToID[id]
	if !exists {
		return ErrNodeNotFound
	}

	incomingNodes := u.findIncomingNodesLocked(internalID)

	for _, layer := range u.index.layers {
		if layer == nil {
			continue
		}
		layer.removeNode(internalID)
		for _, nodeID := range layer.allNodeIDs() {
			layer.removeNeighbor(nodeID, internalID)
		}
	}

	delete(u.index.vectors, internalID)
	delete(u.index.magnitudes, internalID)
	delete(u.index.nodeLevels, internalID)
	delete(u.index.domains, internalID)
	delete(u.index.nodeTypes, internalID)
	delete(u.index.stringToID, id)

	if u.index.entryPoint == internalID {
		u.selectNewEntryPointLocked()
	}

	for _, incomingID := range incomingNodes {
		if incomingID != internalID {
			u.repairConnectionsLocked(incomingID)
		}
	}

	return nil
}

func (u *MNRUUpdater) selectNewEntryPointLocked() {
	u.index.entryPoint = invalidNodeID
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

func (u *MNRUUpdater) repairConnectionsLocked(nodeID uint32) {
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
			continue
		}

		existingNeighbors := make(map[uint32]bool)
		for _, n := range currentNeighbors {
			existingNeighbors[n.ID] = true
		}

		candidates := make([]searchCandidate, 0)
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
			candidates = append(candidates, searchCandidate{id: otherID, distance: 1.0 - sim})
		}

		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].distance < candidates[j].distance
		})

		for _, c := range candidates {
			if len(currentNeighbors) >= maxNeighbors {
				break
			}
			dist := float32(c.distance)
			layer.addNeighbor(nodeID, c.id, dist, maxNeighbors)
			currentNeighbors = append(currentNeighbors, Neighbor{ID: c.id, Distance: dist})
		}
	}
}

func (u *MNRUUpdater) FindIncomingNodes(targetID string) []string {
	u.mu.RLock()
	defer u.mu.RUnlock()

	if u.index == nil {
		return nil
	}

	u.index.mu.RLock()
	defer u.index.mu.RUnlock()

	internalID, exists := u.index.stringToID[targetID]
	if !exists {
		return nil
	}

	internalIDs := u.findIncomingNodesLocked(internalID)
	result := make([]string, len(internalIDs))
	for i, id := range internalIDs {
		result[i] = u.index.idToString[id]
	}
	return result
}

func (u *MNRUUpdater) ValidateConnectivity() []string {
	u.mu.RLock()
	defer u.mu.RUnlock()

	if u.index == nil {
		return nil
	}

	u.index.mu.RLock()
	defer u.index.mu.RUnlock()

	if u.index.entryPoint == invalidNodeID {
		return nil
	}

	reachable := make(map[uint32]bool)
	queue := []uint32{u.index.entryPoint}
	reachable[u.index.entryPoint] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

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

	unreachable := make([]string, 0)
	for id := range u.index.vectors {
		if !reachable[id] {
			unreachable = append(unreachable, u.index.idToString[id])
		}
	}

	return unreachable
}
