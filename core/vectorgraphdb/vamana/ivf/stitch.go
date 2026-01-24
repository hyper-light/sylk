package ivf

import (
	"math"
	"math/rand/v2"
	"sync"

	"github.com/viterin/vek/vek32"
)

// StitchResult contains statistics from a stitch operation.
type StitchResult struct {
	VectorsAdded      int
	BoundaryNodes     int
	EdgesAdded        int
	SearchesPerformed int
}

// StitchBatch efficiently merges a batch of vectors into the index.
// Unlike InsertBatch (which does N graph searches), StitchBatch:
//  1. Appends all vectors without graph connections - O(N)
//  2. Samples √N boundary nodes from new vectors
//  3. For each boundary: graph search + bidirectional edges - O(√N) searches
//
// This is more efficient for bulk imports where per-vector connectivity
// isn't needed - boundary nodes provide graph navigability.
func (idx *Index) StitchBatch(vecs [][]float32) (*StitchResult, error) {
	insertMu.Lock()
	defer insertMu.Unlock()

	if len(vecs) == 0 {
		return &StitchResult{}, nil
	}

	for _, vec := range vecs {
		if len(vec) != idx.dim {
			return nil, ErrDimensionMismatch
		}
	}

	result := &StitchResult{
		VectorsAdded: len(vecs),
	}

	startID := uint32(idx.numVectors)

	partitions := make([]int, len(vecs))
	for i, vec := range vecs {
		partitions[i] = idx.findNearestCentroid(vec)
		idx.appendVector(vec)
		idx.appendNorm(vec)
		idx.appendBBQCode(vec)
	}

	for range vecs {
		idx.graph.adjacency = append(idx.graph.adjacency, nil)
	}

	for i, partition := range partitions {
		newID := startID + uint32(i)
		idx.partitionIDs[partition] = append(idx.partitionIDs[partition], newID)
		if idx.graph.nodeToPartition != nil {
			idx.graph.nodeToPartition = append(idx.graph.nodeToPartition, uint16(partition))
		}
	}

	idx.numVectors += len(vecs)

	numBoundary := int(math.Sqrt(float64(len(vecs))))
	if numBoundary < 1 {
		numBoundary = 1
	}
	if numBoundary > len(vecs) {
		numBoundary = len(vecs)
	}

	boundaryLocalIDs := sampleBoundaryNodes(len(vecs), numBoundary)
	result.BoundaryNodes = len(boundaryLocalIDs)

	for _, localID := range boundaryLocalIDs {
		globalID := startID + uint32(localID)
		vec := vecs[localID]
		partition := partitions[localID]

		neighbors := idx.findInsertNeighbors(vec, globalID, partition)
		result.SearchesPerformed++

		if len(neighbors) > 0 {
			idx.graph.adjacency[globalID] = neighbors
			for _, neighborID := range neighbors {
				idx.addReverseEdge(neighborID, globalID)
				result.EdgesAdded += 2
			}
		}
	}

	if len(boundaryLocalIDs) > 0 && len(vecs) > numBoundary {
		idx.connectNonBoundaryNodes(vecs, startID, boundaryLocalIDs, partitions, result)
	}

	return result, nil
}

// sampleBoundaryNodes selects k random nodes from [0, n) using partial Fisher-Yates.
func sampleBoundaryNodes(n, k int) []int {
	if k >= n {
		result := make([]int, n)
		for i := range n {
			result[i] = i
		}
		return result
	}

	indices := make([]int, n)
	for i := range n {
		indices[i] = i
	}

	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	for i := range k {
		j := i + rng.IntN(n-i)
		indices[i], indices[j] = indices[j], indices[i]
	}

	return indices[:k]
}

// connectNonBoundaryNodes connects non-boundary nodes to nearby boundary nodes.
// This ensures all new nodes have some graph connectivity.
func (idx *Index) connectNonBoundaryNodes(
	vecs [][]float32,
	startID uint32,
	boundaryLocalIDs []int,
	partitions []int,
	result *StitchResult,
) {
	boundarySet := make(map[int]bool, len(boundaryLocalIDs))
	for _, id := range boundaryLocalIDs {
		boundarySet[id] = true
	}

	type boundaryInfo struct {
		localID int
		vec     []float32
		norm    float64
	}
	boundaryInfos := make([]boundaryInfo, len(boundaryLocalIDs))
	for i, localID := range boundaryLocalIDs {
		boundaryInfos[i] = boundaryInfo{
			localID: localID,
			vec:     vecs[localID],
			norm:    idx.vectorNorms[startID+uint32(localID)],
		}
	}

	var edgesMu sync.Mutex
	var wg sync.WaitGroup
	chunkSize := 1000
	for chunkStart := 0; chunkStart < len(vecs); chunkStart += chunkSize {
		chunkEnd := min(chunkStart+chunkSize, len(vecs))
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			localEdges := make([][2]uint32, 0, end-start)

			for localID := start; localID < end; localID++ {
				if boundarySet[localID] {
					continue
				}

				globalID := startID + uint32(localID)
				if len(idx.graph.adjacency[globalID]) > 0 {
					continue
				}

				vec := vecs[localID]
				vecNorm := idx.vectorNorms[globalID]
				if vecNorm == 0 {
					continue
				}

				bestBoundary := -1
				bestSim := -2.0
				for _, bi := range boundaryInfos {
					if bi.norm == 0 {
						continue
					}
					sim := float64(vek32.Dot(vec, bi.vec)) / (vecNorm * bi.norm)
					if sim > bestSim {
						bestSim = sim
						bestBoundary = bi.localID
					}
				}

				if bestBoundary >= 0 {
					boundaryGlobalID := startID + uint32(bestBoundary)
					localEdges = append(localEdges, [2]uint32{globalID, boundaryGlobalID})
				}
			}

			edgesMu.Lock()
			for _, edge := range localEdges {
				idx.graph.adjacency[edge[0]] = append(idx.graph.adjacency[edge[0]], edge[1])
				idx.graph.adjacency[edge[1]] = append(idx.graph.adjacency[edge[1]], edge[0])
				result.EdgesAdded += 2
			}
			edgesMu.Unlock()
		}(chunkStart, chunkEnd)
	}
	wg.Wait()
}
