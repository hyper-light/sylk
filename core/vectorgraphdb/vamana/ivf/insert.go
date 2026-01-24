package ivf

import (
	"errors"
	"math"
	"sync"

	"github.com/viterin/vek/vek32"
)

var ErrDimensionMismatch = errors.New("ivf: vector dimension mismatch")

type InsertResult struct {
	ID        uint32
	Partition int
	Neighbors []uint32
}

var insertMu sync.Mutex

func (idx *Index) Insert(vec []float32) (*InsertResult, error) {
	insertMu.Lock()
	defer insertMu.Unlock()

	if len(vec) != idx.dim {
		return nil, ErrDimensionMismatch
	}

	newID := uint32(idx.numVectors)
	partition := idx.findNearestCentroid(vec)

	idx.appendVector(vec)
	idx.appendNorm(vec)
	idx.appendBBQCode(vec)

	neighbors := idx.findInsertNeighbors(vec, newID, partition)
	idx.connectToGraph(newID, neighbors)

	idx.partitionIDs[partition] = append(idx.partitionIDs[partition], newID)

	if idx.graph != nil && idx.graph.nodeToPartition != nil {
		idx.graph.nodeToPartition = append(idx.graph.nodeToPartition, uint16(partition))
	}

	idx.numVectors++

	return &InsertResult{
		ID:        newID,
		Partition: partition,
		Neighbors: neighbors,
	}, nil
}

func (idx *Index) findNearestCentroid(vec []float32) int {
	vecNorm := math.Sqrt(float64(vek32.Dot(vec, vec)))
	if vecNorm == 0 {
		return 0
	}

	bestPartition := 0
	bestSim := -2.0

	for p, centroid := range idx.centroids {
		centroidNorm := idx.centroidNorms[p]
		if centroidNorm == 0 {
			continue
		}
		dot := vek32.Dot(vec, centroid)
		sim := float64(dot) / (vecNorm * centroidNorm)
		if sim > bestSim {
			bestSim = sim
			bestPartition = p
		}
	}

	return bestPartition
}

func (idx *Index) appendVector(vec []float32) {
	idx.vectorsFlat = append(idx.vectorsFlat, vec...)
}

func (idx *Index) appendNorm(vec []float32) {
	norm := math.Sqrt(float64(vek32.Dot(vec, vec)))
	idx.vectorNorms = append(idx.vectorNorms, norm)
}

func (idx *Index) appendBBQCode(vec []float32) {
	code := make([]byte, idx.bbqCodeLen)
	idx.bbq.EncodeDB(vec, code)
	idx.bbqCodes = append(idx.bbqCodes, code...)
}

func (idx *Index) findInsertNeighbors(vec []float32, newID uint32, partition int) []uint32 {
	if idx.graph == nil || idx.numVectors == 0 {
		return nil
	}

	partitionIDs := idx.partitionIDs[partition]
	if len(partitionIDs) == 0 {
		return nil
	}

	vecNorm := math.Sqrt(float64(vek32.Dot(vec, vec)))
	if vecNorm == 0 {
		return nil
	}

	R := idx.graph.R

	score := func(id uint32) float64 {
		v := idx.getVector(id)
		n := idx.vectorNorms[id]
		if v == nil || n == 0 {
			return -2
		}
		return float64(vek32.Dot(vec, v)) / (vecNorm * n)
	}

	visited := make(map[uint32]bool)
	visited[newID] = true

	result := make([]scored, 0, R)
	frontier := make([]scored, 0, R)

	entryPoint := partitionIDs[0]
	visited[entryPoint] = true
	frontier = append(frontier, scored{entryPoint, score(entryPoint)})

	for len(frontier) > 0 {
		best := frontier[0]
		frontier = frontier[1:]

		result = insertSorted(result, best, R)

		if len(result) >= R && best.sim < result[R-1].sim {
			break
		}

		neighbors := idx.graph.adjacency[best.id]
		for _, neighborID := range neighbors {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true
			frontier = insertSorted(frontier, scored{neighborID, score(neighborID)}, R)
		}
	}

	neighbors := make([]uint32, len(result))
	for i, s := range result {
		neighbors[i] = s.id
	}
	return neighbors
}

type scored struct {
	id  uint32
	sim float64
}

func insertSorted(list []scored, item scored, maxLen int) []scored {
	pos := len(list)
	for i := range list {
		if item.sim > list[i].sim {
			pos = i
			break
		}
	}

	if pos >= maxLen {
		return list
	}

	if len(list) < maxLen {
		list = append(list, scored{})
	}

	copy(list[pos+1:], list[pos:])
	list[pos] = item

	if len(list) > maxLen {
		list = list[:maxLen]
	}

	return list
}

func (idx *Index) connectToGraph(newID uint32, neighbors []uint32) {
	if idx.graph == nil {
		return
	}

	idx.graph.adjacency = append(idx.graph.adjacency, neighbors)

	for _, neighborID := range neighbors {
		idx.addReverseEdge(neighborID, newID)
	}
}

func (idx *Index) addReverseEdge(from, to uint32) {
	if idx.graph == nil || int(from) >= len(idx.graph.adjacency) {
		return
	}

	edges := idx.graph.adjacency[from]

	for _, e := range edges {
		if e == to {
			return
		}
	}

	if len(edges) < idx.graph.R {
		idx.graph.adjacency[from] = append(edges, to)
		return
	}

	idx.pruneAndAddEdge(from, to)
}

func (idx *Index) pruneAndAddEdge(nodeID, newNeighbor uint32) {
	edges := idx.graph.adjacency[nodeID]

	nodeVec := idx.getVector(nodeID)
	nodeNorm := idx.vectorNorms[nodeID]
	if nodeNorm == 0 {
		return
	}

	type edgeDist struct {
		id  uint32
		sim float64
	}

	candidates := make([]edgeDist, 0, len(edges)+1)

	for _, e := range edges {
		eVec := idx.getVector(e)
		eNorm := idx.vectorNorms[e]
		if eNorm == 0 {
			continue
		}
		sim := float64(vek32.Dot(nodeVec, eVec)) / (nodeNorm * eNorm)
		candidates = append(candidates, edgeDist{e, sim})
	}

	newVec := idx.getVector(newNeighbor)
	newNorm := idx.vectorNorms[newNeighbor]
	if newNorm > 0 {
		sim := float64(vek32.Dot(nodeVec, newVec)) / (nodeNorm * newNorm)
		candidates = append(candidates, edgeDist{newNeighbor, sim})
	}

	for i := 1; i < len(candidates); i++ {
		j := i
		for j > 0 && candidates[j].sim > candidates[j-1].sim {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
			j--
		}
	}

	newEdges := make([]uint32, 0, idx.graph.R)
	for i := 0; i < len(candidates) && len(newEdges) < idx.graph.R; i++ {
		newEdges = append(newEdges, candidates[i].id)
	}

	idx.graph.adjacency[nodeID] = newEdges
}

func (idx *Index) getVector(id uint32) []float32 {
	offset := int(id) * idx.dim
	if offset+idx.dim > len(idx.vectorsFlat) {
		return nil
	}
	return idx.vectorsFlat[offset : offset+idx.dim]
}

func (idx *Index) InsertBatch(vecs [][]float32) ([]*InsertResult, error) {
	results := make([]*InsertResult, len(vecs))
	for i, vec := range vecs {
		result, err := idx.Insert(vec)
		if err != nil {
			return results[:i], err
		}
		results[i] = result
	}
	return results, nil
}
