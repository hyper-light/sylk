package vamana

import (
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

// VamanaInsert adds a new node to the Vamana graph using the FreshVamana algorithm.
//
// Algorithm (DiskANN FreshVamana):
//  1. Search from medoid to find L nearest neighbors
//  2. RobustPrune to select R forward edges for new node
//  3. Add bidirectional edges (prune neighbors if they exceed R)
func VamanaInsert(
	nodeID uint32,
	vector []float32,
	medoid uint32,
	config VamanaConfig,
	vectors *storage.VectorStore,
	graph *storage.GraphStore,
	magCache *MagnitudeCache,
) error {
	if vectors == nil || graph == nil {
		return nil
	}

	magCache.GetOrCompute(nodeID, vector)

	distFn := makeDistanceFunc(vectors, magCache)

	candidates := GreedySearch(vector, medoid, config.L, config.L, vectors, graph, magCache)

	candidateIDs := make([]uint32, len(candidates))
	for i, c := range candidates {
		candidateIDs[i] = uint32(c.InternalID)
	}

	neighbors := RobustPrune(nodeID, candidateIDs, config.Alpha, config.R, distFn)

	if err := graph.SetNeighbors(nodeID, neighbors); err != nil {
		return err
	}

	for _, neighborID := range neighbors {
		existingNeighbors := graph.GetNeighbors(neighborID)

		hasEdge := false
		for _, n := range existingNeighbors {
			if n == nodeID {
				hasEdge = true
				break
			}
		}

		if hasEdge {
			continue
		}

		newNeighbors := append(existingNeighbors, nodeID)

		if len(newNeighbors) > config.R {
			newNeighbors = RobustPrune(neighborID, newNeighbors, config.Alpha, config.R, distFn)
		}

		if err := graph.SetNeighbors(neighborID, newNeighbors); err != nil {
			return err
		}
	}

	return nil
}

// VamanaInsertBatch inserts multiple nodes, returning first error encountered.
func VamanaInsertBatch(
	nodeIDs []uint32,
	vectors [][]float32,
	medoid uint32,
	config VamanaConfig,
	vectorStore *storage.VectorStore,
	graph *storage.GraphStore,
	magCache *MagnitudeCache,
) error {
	if len(nodeIDs) != len(vectors) {
		return nil
	}

	for i, nodeID := range nodeIDs {
		if err := VamanaInsert(nodeID, vectors[i], medoid, config, vectorStore, graph, magCache); err != nil {
			return err
		}
	}

	return nil
}

func makeDistanceFunc(vectors *storage.VectorStore, magCache *MagnitudeCache) DistanceFunc {
	return func(a, b uint32) float64 {
		vecA := vectors.Get(a)
		vecB := vectors.Get(b)
		if vecA == nil || vecB == nil {
			return 2.0
		}

		magA := magCache.GetOrCompute(a, vecA)
		magB := magCache.GetOrCompute(b, vecB)
		if magA == 0 || magB == 0 {
			return 2.0
		}

		dot := DotProduct(vecA, vecB)
		similarity := float64(dot) / (magA * magB)
		return 1.0 - similarity
	}
}
