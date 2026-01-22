package vamana

import (
	"math"
	"math/rand/v2"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

// ComputeMedoid finds the node with minimum total distance to all other nodes.
// Exact computation: O(n²). Use for small datasets (< 10k nodes).
func ComputeMedoid(
	vectors *storage.VectorStore,
	magCache *MagnitudeCache,
	tombstones *TombstoneSet,
) uint32 {
	n := vectors.Count()
	if n == 0 {
		return 0
	}

	activeNodes := collectActiveNodes(uint32(n), tombstones)
	if len(activeNodes) == 0 {
		return 0
	}

	if len(activeNodes) == 1 {
		return activeNodes[0]
	}

	var bestNode uint32
	bestTotalDist := math.MaxFloat64

	for _, nodeID := range activeNodes {
		vecI := vectors.Get(nodeID)
		if vecI == nil {
			continue
		}
		magI := magCache.GetOrCompute(nodeID, vecI)
		if magI == 0 {
			continue
		}

		totalDist := 0.0
		for _, otherID := range activeNodes {
			if nodeID == otherID {
				continue
			}
			vecJ := vectors.Get(otherID)
			if vecJ == nil {
				continue
			}
			magJ := magCache.GetOrCompute(otherID, vecJ)
			if magJ == 0 {
				continue
			}

			dot := DotProduct(vecI, vecJ)
			similarity := float64(dot) / (magI * magJ)
			totalDist += 1.0 - similarity
		}

		if totalDist < bestTotalDist {
			bestTotalDist = totalDist
			bestNode = nodeID
		}
	}

	return bestNode
}

// ComputeApproximateMedoid uses sampling for O(n*S) complexity where S is sample size.
// Use for medium datasets (10k-100k nodes).
func ComputeApproximateMedoid(
	vectors *storage.VectorStore,
	magCache *MagnitudeCache,
	tombstones *TombstoneSet,
	sampleSize int,
) uint32 {
	n := vectors.Count()
	if n == 0 {
		return 0
	}

	activeNodes := collectActiveNodes(uint32(n), tombstones)
	if len(activeNodes) == 0 {
		return 0
	}

	if len(activeNodes) <= sampleSize {
		return ComputeMedoid(vectors, magCache, tombstones)
	}

	sample := sampleNodes(activeNodes, sampleSize)

	var bestNode uint32
	bestTotalDist := math.MaxFloat64

	for _, nodeID := range activeNodes {
		vecI := vectors.Get(nodeID)
		if vecI == nil {
			continue
		}
		magI := magCache.GetOrCompute(nodeID, vecI)
		if magI == 0 {
			continue
		}

		totalDist := 0.0
		for _, sampleID := range sample {
			vecJ := vectors.Get(sampleID)
			if vecJ == nil {
				continue
			}
			magJ := magCache.GetOrCompute(sampleID, vecJ)
			if magJ == 0 {
				continue
			}

			dot := DotProduct(vecI, vecJ)
			similarity := float64(dot) / (magI * magJ)
			totalDist += 1.0 - similarity
		}

		if totalDist < bestTotalDist {
			bestTotalDist = totalDist
			bestNode = nodeID
		}
	}

	return bestNode
}

// ComputeCentroidMedoid computes centroid then finds nearest node. O(n*d) where d is dimension.
// Use for large datasets (> 100k nodes).
func ComputeCentroidMedoid(
	vectors *storage.VectorStore,
	magCache *MagnitudeCache,
	tombstones *TombstoneSet,
) uint32 {
	n := vectors.Count()
	if n == 0 {
		return 0
	}

	activeNodes := collectActiveNodes(uint32(n), tombstones)
	if len(activeNodes) == 0 {
		return 0
	}

	if len(activeNodes) == 1 {
		return activeNodes[0]
	}

	dim := vectors.Dimension()
	centroid := make([]float64, dim)

	for _, nodeID := range activeNodes {
		vec := vectors.Get(nodeID)
		if vec == nil {
			continue
		}
		for i, v := range vec {
			centroid[i] += float64(v)
		}
	}

	count := float64(len(activeNodes))
	for i := range centroid {
		centroid[i] /= count
	}

	centroidF32 := make([]float32, dim)
	for i, v := range centroid {
		centroidF32[i] = float32(v)
	}
	centroidMag := Magnitude(centroidF32)

	var bestNode uint32
	bestSimilarity := -2.0

	for _, nodeID := range activeNodes {
		vec := vectors.Get(nodeID)
		if vec == nil {
			continue
		}
		mag := magCache.GetOrCompute(nodeID, vec)
		if mag == 0 {
			continue
		}

		dot := DotProduct(centroidF32, vec)
		similarity := float64(dot) / (centroidMag * mag)

		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestNode = nodeID
		}
	}

	return bestNode
}

func collectActiveNodes(totalNodes uint32, tombstones *TombstoneSet) []uint32 {
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

func sampleNodes(nodes []uint32, sampleSize int) []uint32 {
	if len(nodes) <= sampleSize {
		return nodes
	}

	shuffled := make([]uint32, len(nodes))
	copy(shuffled, nodes)

	for i := len(shuffled) - 1; i > 0; i-- {
		j := rand.IntN(i + 1)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled[:sampleSize]
}

// ComputeMedoidFromVectors finds the centroid-nearest vector from a slice.
// Returns index within the slice (local ID), not a global VectorStore ID.
// O(n*d) where n is vector count and d is dimension.
func ComputeMedoidFromVectors(vectors [][]float32) uint32 {
	n := len(vectors)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return 0
	}

	dim := len(vectors[0])
	centroid := make([]float64, dim)

	for _, vec := range vectors {
		for i, v := range vec {
			centroid[i] += float64(v)
		}
	}

	count := float64(n)
	for i := range centroid {
		centroid[i] /= count
	}

	centroidF32 := make([]float32, dim)
	for i, v := range centroid {
		centroidF32[i] = float32(v)
	}
	centroidMag := Magnitude(centroidF32)
	if centroidMag == 0 {
		return 0
	}

	var bestIdx uint32
	bestSimilarity := -2.0

	for i, vec := range vectors {
		mag := Magnitude(vec)
		if mag == 0 {
			continue
		}

		dot := DotProduct(centroidF32, vec)
		similarity := float64(dot) / (centroidMag * mag)

		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIdx = uint32(i)
		}
	}

	return bestIdx
}
