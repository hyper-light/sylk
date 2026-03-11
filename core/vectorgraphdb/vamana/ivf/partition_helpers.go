package ivf

import (
	"math"
	"sync"

	"github.com/viterin/vek/vek32"
)

type superCentroidLayout struct {
	dim            int
	sqrtK          int
	groupStarts    []int
	groupEnds      []int
	superCentroids []float32
	superNormsSq   []float64
}

func newSuperCentroidLayout(k, dim int, centroidsFlat []float32) *superCentroidLayout {
	sqrtK := 1
	for sqrtK*sqrtK < k {
		sqrtK++
	}
	layout := &superCentroidLayout{
		dim:            dim,
		sqrtK:          sqrtK,
		groupStarts:    make([]int, sqrtK),
		groupEnds:      make([]int, sqrtK),
		superCentroids: make([]float32, sqrtK*dim),
		superNormsSq:   make([]float64, sqrtK),
	}
	layout.initGroupBounds(k)
	layout.rebuild(centroidsFlat)
	return layout
}

func (l *superCentroidLayout) initGroupBounds(k int) {
	groupSize := k / l.sqrtK
	for i := range l.sqrtK {
		l.groupStarts[i] = i * groupSize
		l.groupEnds[i] = l.groupStarts[i] + groupSize
		if i == l.sqrtK-1 {
			l.groupEnds[i] = k
		}
	}
}

func (l *superCentroidLayout) rebuild(centroidsFlat []float32) {
	for i := range l.sqrtK {
		start := l.groupStarts[i]
		end := l.groupEnds[i]
		offset := i * l.dim
		for j := range l.dim {
			var sum float32
			for c := start; c < end; c++ {
				sum += centroidsFlat[c*l.dim+j]
			}
			l.superCentroids[offset+j] = sum / float32(end-start)
		}
		l.superNormsSq[i] = float64(vek32.Dot(
			l.superCentroids[offset:offset+l.dim],
			l.superCentroids[offset:offset+l.dim],
		))
	}
}

func (l *superCentroidLayout) bestCentroid(
	vec []float32,
	vecNormSq float64,
	centroidsFlat []float32,
	centroidNormsSq []float64,
	scratch []float64,
) int {
	l.fillSuperDists(vec, vecNormSq, scratch)
	best1, best2 := selectBestSuperCentroids(scratch)
	bestCentroid, bestDist := nearestCentroidInRange(
		vec,
		vecNormSq,
		centroidsFlat,
		centroidNormsSq,
		l.groupStarts[best1],
		l.groupEnds[best1],
		l.dim,
	)
	if best2 == best1 {
		return bestCentroid
	}
	candidateCentroid, candidateDist := nearestCentroidInRange(
		vec,
		vecNormSq,
		centroidsFlat,
		centroidNormsSq,
		l.groupStarts[best2],
		l.groupEnds[best2],
		l.dim,
	)
	if candidateDist < bestDist {
		return candidateCentroid
	}
	return bestCentroid
}

func (l *superCentroidLayout) fillSuperDists(vec []float32, vecNormSq float64, scratch []float64) {
	for s := range l.sqrtK {
		offset := s * l.dim
		dot := vek32.Dot(vec, l.superCentroids[offset:offset+l.dim])
		scratch[s] = vecNormSq + l.superNormsSq[s] - 2*float64(dot)
	}
}

func selectBestSuperCentroids(dists []float64) (int, int) {
	if len(dists) <= 1 {
		return 0, 0
	}
	best1, best2 := 0, 1
	if dists[1] < dists[0] {
		best1, best2 = 1, 0
	}
	for s := 2; s < len(dists); s++ {
		if dists[s] < dists[best1] {
			best2 = best1
			best1 = s
			continue
		}
		if dists[s] < dists[best2] {
			best2 = s
		}
	}
	return best1, best2
}

func nearestCentroidInRange(
	vec []float32,
	vecNormSq float64,
	centroidsFlat []float32,
	centroidNormsSq []float64,
	start, end, dim int,
) (int, float64) {
	bestCentroid := start
	bestDist := math.MaxFloat64
	for c := start; c < end; c++ {
		offset := c * dim
		dot := vek32.Dot(vec, centroidsFlat[offset:offset+dim])
		dist := vecNormSq + centroidNormsSq[c] - 2*float64(dot)
		if dist < bestDist {
			bestDist = dist
			bestCentroid = c
		}
	}
	return bestCentroid, bestDist
}

func runChunkWorkers(n, numWorkers int, fn func(start, end int)) {
	if n == 0 {
		return
	}
	if numWorkers <= 0 {
		numWorkers = 1
	}
	chunkSize := (n + numWorkers - 1) / numWorkers
	var wg sync.WaitGroup
	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, n)
		if start >= end {
			continue
		}
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			fn(start, end)
		}(start, end)
	}
	wg.Wait()
}

func buildPartitionSample(vectors [][]float32, perm []int, sampleSize int) ([][]float32, []float64) {
	sample := make([][]float32, sampleSize)
	sampleNormsSq := make([]float64, sampleSize)
	for i := range sampleSize {
		sample[i] = vectors[perm[i]]
		sampleNormsSq[i] = float64(vek32.Dot(sample[i], sample[i]))
	}
	return sample, sampleNormsSq
}

func (idx *Index) initializeCentroidsFromPermutation(
	vectors [][]float32,
	perm []int,
	k, sampleSize int,
) ([]float32, []float64) {
	idx.centroids = make([][]float32, k)
	centroidsFlat := make([]float32, k*idx.dim)
	centroidNormsSq := make([]float64, k)
	for i := range k {
		idx.centroids[i] = make([]float32, idx.dim)
		copy(idx.centroids[i], vectors[perm[i%sampleSize]])
		offset := i * idx.dim
		copy(centroidsFlat[offset:offset+idx.dim], idx.centroids[i])
		centroidNormsSq[i] = float64(vek32.Dot(idx.centroids[i], idx.centroids[i]))
	}
	return centroidsFlat, centroidNormsSq
}

func sampleReassignments(assignments, prev []uint32) int {
	reassignments := 0
	for i := range assignments {
		if assignments[i] != prev[i] {
			reassignments++
		}
	}
	return reassignments
}

func samplePartitionCounts(assignments []uint32, k int) []int {
	counts := make([]int, k)
	for _, p := range assignments {
		counts[p]++
	}
	return counts
}

func resetFloat32Slice(values []float32) {
	for i := range values {
		values[i] = 0
	}
}

func accumulateAssignedVectors(sample [][]float32, sampleAssign []uint32, centroidsFlat []float32, dim int) {
	for i, vec := range sample {
		offset := int(sampleAssign[i]) * dim
		for j, value := range vec {
			centroidsFlat[offset+j] += value
		}
	}
}

func normalizeSampleCentroids(
	centroidsFlat []float32,
	centroidNormsSq []float64,
	counts []int,
	vectors [][]float32,
	perm []int,
	iter, k, dim, sampleSize int,
) int {
	emptyCount := 0
	for i := range k {
		offset := i * dim
		if counts[i] == 0 {
			emptyCount++
			copy(centroidsFlat[offset:offset+dim], vectors[perm[(iter*k+i)%sampleSize]])
		} else {
			invCount := 1.0 / float32(counts[i])
			for j := range dim {
				centroidsFlat[offset+j] *= invCount
			}
		}
		centroidNormsSq[i] = float64(vek32.Dot(
			centroidsFlat[offset:offset+dim],
			centroidsFlat[offset:offset+dim],
		))
	}
	return emptyCount
}

func (idx *Index) assignVectorsHierarchically(
	vectors [][]float32,
	centroidsFlat []float32,
	centroidNormsSq []float64,
	layout *superCentroidLayout,
	numWorkers int,
) []uint32 {
	assignments := make([]uint32, len(vectors))
	runChunkWorkers(len(vectors), numWorkers, func(start, end int) {
		scratch := make([]float64, layout.sqrtK)
		for i := start; i < end; i++ {
			vec := vectors[i]
			vecNormSq := float64(vek32.Dot(vec, vec))
			assignments[i] = uint32(layout.bestCentroid(vec, vecNormSq, centroidsFlat, centroidNormsSq, scratch))
		}
	})
	return assignments
}

func buildPartitionIDsFromAssignments(k, n int, assignments []uint32) [][]uint32 {
	partitionIDs := make([][]uint32, k)
	for p := range k {
		partitionIDs[p] = make([]uint32, 0, n/k+1)
	}
	for i, p := range assignments {
		partitionIDs[p] = append(partitionIDs[p], uint32(i))
	}
	return partitionIDs
}

func cloneCentroids(centroids [][]float32) [][]float32 {
	cloned := make([][]float32, len(centroids))
	for i, c := range centroids {
		cloned[i] = make([]float32, len(c))
		copy(cloned[i], c)
	}
	return cloned
}

func flattenCentroidsAndNorms(
	centroids [][]float32,
	centroidNorms []float64,
	dim int,
) ([]float32, []float64) {
	centroidsFlat := make([]float32, len(centroids)*dim)
	centroidNormsSq := make([]float64, len(centroids))
	for p := range len(centroids) {
		offset := p * dim
		copy(centroidsFlat[offset:offset+dim], centroids[p])
		centroidNormsSq[p] = centroidNorms[p] * centroidNorms[p]
	}
	return centroidsFlat, centroidNormsSq
}

func (idx *Index) assignStoredVectorsHierarchically(
	centroidsFlat []float32,
	centroidNormsSq []float64,
	layout *superCentroidLayout,
	numWorkers int,
) []int {
	assignments := make([]int, idx.numVectors)
	runChunkWorkers(idx.numVectors, numWorkers, func(start, end int) {
		scratch := make([]float64, layout.sqrtK)
		for i := start; i < end; i++ {
			vec := idx.getVector(uint32(i))
			if vec == nil {
				continue
			}
			vecNormSq := idx.vectorNorms[i] * idx.vectorNorms[i]
			assignments[i] = layout.bestCentroid(vec, vecNormSq, centroidsFlat, centroidNormsSq, scratch)
		}
	})
	return assignments
}

func (idx *Index) recomputeCentroidsFromAssignments(
	assignments []int,
	centroidsFlat []float32,
	centroidNormsSq []float64,
	k, dim int,
) []float32 {
	newCentroidsFlat := make([]float32, k*dim)
	counts := make([]int, k)
	for i := range idx.numVectors {
		vec := idx.getVector(uint32(i))
		if vec == nil {
			continue
		}
		offset := assignments[i] * dim
		for j, value := range vec {
			newCentroidsFlat[offset+j] += value
		}
		counts[assignments[i]]++
	}
	for p := range k {
		offset := p * dim
		if counts[p] > 0 {
			invCount := 1.0 / float32(counts[p])
			for j := range dim {
				newCentroidsFlat[offset+j] *= invCount
			}
		} else {
			copy(newCentroidsFlat[offset:offset+dim], centroidsFlat[offset:offset+dim])
		}
		centroidNormsSq[p] = float64(vek32.Dot(
			newCentroidsFlat[offset:offset+dim],
			newCentroidsFlat[offset:offset+dim],
		))
	}
	return newCentroidsFlat
}

func (idx *Index) updateCentroidsFromFlat(centroidsFlat []float32, centroidNormsSq []float64) {
	for p := range len(idx.centroids) {
		idx.centroids[p] = make([]float32, idx.dim)
		offset := p * idx.dim
		copy(idx.centroids[p], centroidsFlat[offset:offset+idx.dim])
	}
	idx.centroidNorms = make([]float64, len(idx.centroids))
	for p := range len(idx.centroids) {
		idx.centroidNorms[p] = math.Sqrt(centroidNormsSq[p])
	}
}

func originalPartitionsFromIDs(partitionIDs [][]uint32, n int) []int {
	originalPartitions := make([]int, n)
	for p, ids := range partitionIDs {
		for _, id := range ids {
			if int(id) < n {
				originalPartitions[id] = p
			}
		}
	}
	return originalPartitions
}

func buildPartitionIDsFromIntAssignments(k, n int, assignments []int) [][]uint32 {
	partitionIDs := make([][]uint32, k)
	for p := range k {
		partitionIDs[p] = make([]uint32, 0, n/k+1)
	}
	for i, p := range assignments {
		partitionIDs[p] = append(partitionIDs[p], uint32(i))
	}
	return partitionIDs
}

func countPartitionReassignments(originalPartitions, finalAssignments []int) int {
	reassignments := 0
	for i := range finalAssignments {
		if originalPartitions[i] != finalAssignments[i] {
			reassignments++
		}
	}
	return reassignments
}
