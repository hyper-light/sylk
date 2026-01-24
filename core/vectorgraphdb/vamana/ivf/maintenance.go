package ivf

import (
	"errors"
	"math"
	"math/rand/v2"
	"runtime"
	"sync"

	"github.com/viterin/vek/vek32"
)

type MaintenanceStats struct {
	NumVectors    int
	NumPartitions int

	PartitionSizeGini float64 // 0 = perfect balance, 1 = maximum imbalance
	PartitionSizeCV   float64 // coefficient of variation (stddev/mean)

	DriftRatio float64 // current_drift / baseline_drift; 1.0 = healthy, >1 = degraded

	ConnectivityRatio float64 // avg_degree / R; 1.0 = fully connected, <1 = degraded
	DegreeFillRate    float64 // fraction of nodes with degree >= R
}

func (idx *Index) MaintenanceStats() MaintenanceStats {
	insertMu.Lock()
	defer insertMu.Unlock()

	stats := MaintenanceStats{
		NumVectors:    idx.numVectors,
		NumPartitions: len(idx.partitionIDs),
	}

	if len(idx.partitionIDs) == 0 {
		return stats
	}

	stats.PartitionSizeGini, stats.PartitionSizeCV = idx.computePartitionBalance()
	stats.DriftRatio = idx.computeDriftRatio()
	stats.ConnectivityRatio, stats.DegreeFillRate = idx.computeGraphHealth()

	return stats
}

func (idx *Index) computePartitionBalance() (gini, cv float64) {
	n := len(idx.partitionIDs)
	if n == 0 {
		return 0, 0
	}

	sizes := make([]float64, n)
	sum := 0.0
	for i, ids := range idx.partitionIDs {
		sizes[i] = float64(len(ids))
		sum += sizes[i]
	}

	if sum == 0 {
		return 0, 0
	}

	mean := sum / float64(n)

	var variance float64
	var giniSum float64
	for i := range n {
		diff := sizes[i] - mean
		variance += diff * diff
		for j := range n {
			giniSum += math.Abs(sizes[i] - sizes[j])
		}
	}

	variance /= float64(n)
	stddev := math.Sqrt(variance)

	if mean > 0 {
		cv = stddev / mean
	}

	gini = giniSum / (2.0 * float64(n) * sum)

	return gini, cv
}

func (idx *Index) computeDriftRatio() float64 {
	if idx.graph == nil || idx.graph.clusteringQuality <= 0 {
		return 0
	}

	currentDrift := idx.computeCentroidDrift()
	baselineDrift := 1.0 - idx.graph.clusteringQuality

	if baselineDrift <= 0 {
		return 0
	}

	return currentDrift / baselineDrift
}

func (idx *Index) computeGraphHealth() (connectivityRatio, fillRate float64) {
	if idx.graph == nil || idx.graph.R == 0 || len(idx.graph.adjacency) == 0 {
		return 0, 0
	}

	totalDegree := 0
	fullNodes := 0

	for _, neighbors := range idx.graph.adjacency {
		deg := len(neighbors)
		totalDegree += deg
		if deg >= idx.graph.R {
			fullNodes++
		}
	}

	avgDegree := float64(totalDegree) / float64(len(idx.graph.adjacency))
	connectivityRatio = avgDegree / float64(idx.graph.R)
	fillRate = float64(fullNodes) / float64(len(idx.graph.adjacency))

	return connectivityRatio, fillRate
}

func (idx *Index) computeCentroidDrift() float64 {
	if len(idx.centroids) == 0 || idx.numVectors == 0 {
		return 0
	}

	sqrtN := int(math.Sqrt(float64(idx.numVectors)))
	sampleSize := sqrtN
	if sampleSize > idx.numVectors {
		sampleSize = idx.numVectors
	}
	perm := rand.Perm(idx.numVectors)[:sampleSize]

	totalDrift := 0.0
	validSamples := 0

	for _, i := range perm {
		vec := idx.getVector(uint32(i))
		if vec == nil {
			continue
		}
		vecNorm := idx.vectorNorms[i]
		if vecNorm == 0 {
			continue
		}

		assignedPartition := idx.findNearestCentroid(vec)
		centroid := idx.centroids[assignedPartition]
		centroidNorm := idx.centroidNorms[assignedPartition]
		if centroidNorm == 0 {
			continue
		}

		sim := float64(vek32.Dot(vec, centroid)) / (vecNorm * centroidNorm)
		drift := 1.0 - sim
		totalDrift += drift
		validSamples++
	}

	if validSamples == 0 {
		return 0
	}

	return totalDrift / float64(validSamples)
}

type RefreshResult struct {
	VectorsReassigned int
	OldCentroids      [][]float32
	NewCentroids      [][]float32
}

var (
	ErrInvalidIterations    = errors.New("ivf: iterations must be > 0")
	ErrInvalidPartitionSize = errors.New("ivf: maxPartitionSize must be > 0")
)

func (idx *Index) RefreshCentroids(iterations int) (*RefreshResult, error) {
	if iterations <= 0 {
		return nil, ErrInvalidIterations
	}

	insertMu.Lock()
	defer insertMu.Unlock()

	if idx.numVectors == 0 || len(idx.centroids) == 0 {
		return &RefreshResult{}, nil
	}

	result := &RefreshResult{
		OldCentroids: make([][]float32, len(idx.centroids)),
	}

	for i, c := range idx.centroids {
		result.OldCentroids[i] = make([]float32, len(c))
		copy(result.OldCentroids[i], c)
	}

	k := len(idx.centroids)
	dim := idx.dim
	n := idx.numVectors
	numWorkers := runtime.GOMAXPROCS(0)

	centroidsFlat := make([]float32, k*dim)
	centroidNormsSq := make([]float64, k)
	for p := range k {
		copy(centroidsFlat[p*dim:(p+1)*dim], idx.centroids[p])
		centroidNormsSq[p] = idx.centroidNorms[p] * idx.centroidNorms[p]
	}

	sqrtK := 1
	for sqrtK*sqrtK < k {
		sqrtK++
	}

	superCentroids := make([]float32, sqrtK*dim)
	superNormsSq := make([]float64, sqrtK)
	groupStarts := make([]int, sqrtK)
	groupEnds := make([]int, sqrtK)

	for i := range sqrtK {
		groupStarts[i] = i * (k / sqrtK)
		groupEnds[i] = groupStarts[i] + k/sqrtK
		if i == sqrtK-1 {
			groupEnds[i] = k
		}
		for j := range dim {
			var sum float32
			for c := groupStarts[i]; c < groupEnds[i]; c++ {
				sum += centroidsFlat[c*dim+j]
			}
			superCentroids[i*dim+j] = sum / float32(groupEnds[i]-groupStarts[i])
		}
		superNormsSq[i] = float64(vek32.Dot(superCentroids[i*dim:(i+1)*dim], superCentroids[i*dim:(i+1)*dim]))
	}

	assignFast := func(assignments []int) {
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
				superDists := make([]float64, sqrtK)

				for i := start; i < end; i++ {
					vec := idx.getVector(uint32(i))
					if vec == nil {
						continue
					}
					vecNormSq := idx.vectorNorms[i] * idx.vectorNorms[i]

					for s := range sqrtK {
						dot := vek32.Dot(vec, superCentroids[s*dim:(s+1)*dim])
						superDists[s] = vecNormSq + superNormsSq[s] - 2*float64(dot)
					}

					best1, best2 := 0, 1
					if superDists[1] < superDists[0] {
						best1, best2 = 1, 0
					}
					for s := 2; s < sqrtK; s++ {
						if superDists[s] < superDists[best1] {
							best2 = best1
							best1 = s
						} else if superDists[s] < superDists[best2] {
							best2 = s
						}
					}

					bestCentroid := groupStarts[best1]
					bestDist := math.MaxFloat64

					for c := groupStarts[best1]; c < groupEnds[best1]; c++ {
						dot := vek32.Dot(vec, centroidsFlat[c*dim:(c+1)*dim])
						d := vecNormSq + centroidNormsSq[c] - 2*float64(dot)
						if d < bestDist {
							bestDist = d
							bestCentroid = c
						}
					}
					for c := groupStarts[best2]; c < groupEnds[best2]; c++ {
						dot := vek32.Dot(vec, centroidsFlat[c*dim:(c+1)*dim])
						d := vecNormSq + centroidNormsSq[c] - 2*float64(dot)
						if d < bestDist {
							bestDist = d
							bestCentroid = c
						}
					}
					assignments[i] = bestCentroid
				}
			}(start, end)
		}
		wg.Wait()
	}

	rebuildSuperCentroids := func() {
		for i := range sqrtK {
			for j := range dim {
				var sum float32
				for c := groupStarts[i]; c < groupEnds[i]; c++ {
					sum += centroidsFlat[c*dim+j]
				}
				superCentroids[i*dim+j] = sum / float32(groupEnds[i]-groupStarts[i])
			}
			superNormsSq[i] = float64(vek32.Dot(superCentroids[i*dim:(i+1)*dim], superCentroids[i*dim:(i+1)*dim]))
		}
	}

	assignments := make([]int, n)
	assignFast(assignments)

	for iter := range iterations {
		newCentroidsFlat := make([]float32, k*dim)
		counts := make([]int, k)

		for i := range n {
			p := assignments[i]
			vec := idx.getVector(uint32(i))
			if vec == nil {
				continue
			}
			for j, v := range vec {
				newCentroidsFlat[p*dim+j] += v
			}
			counts[p]++
		}

		for p := range k {
			if counts[p] > 0 {
				invCount := 1.0 / float32(counts[p])
				for j := range dim {
					newCentroidsFlat[p*dim+j] *= invCount
				}
			} else {
				copy(newCentroidsFlat[p*dim:(p+1)*dim], centroidsFlat[p*dim:(p+1)*dim])
			}
			centroidNormsSq[p] = float64(vek32.Dot(newCentroidsFlat[p*dim:(p+1)*dim], newCentroidsFlat[p*dim:(p+1)*dim]))
		}

		centroidsFlat = newCentroidsFlat
		rebuildSuperCentroids()

		if iter < iterations-1 {
			assignFast(assignments)
		}
	}

	for p := range k {
		idx.centroids[p] = make([]float32, dim)
		copy(idx.centroids[p], centroidsFlat[p*dim:(p+1)*dim])
		idx.centroidNorms[p] = math.Sqrt(centroidNormsSq[p])
	}

	result.NewCentroids = idx.centroids

	originalPartitions := make([]int, n)
	for p, ids := range idx.partitionIDs {
		for _, id := range ids {
			if int(id) < n {
				originalPartitions[id] = p
			}
		}
	}

	finalAssignments := make([]int, n)
	assignFast(finalAssignments)

	newPartitionIDs := make([][]uint32, k)
	for p := range k {
		newPartitionIDs[p] = make([]uint32, 0)
	}

	for i := range n {
		oldPartition := originalPartitions[i]
		newPartition := finalAssignments[i]
		newPartitionIDs[newPartition] = append(newPartitionIDs[newPartition], uint32(i))

		if oldPartition != newPartition {
			result.VectorsReassigned++
		}

		if idx.graph != nil && idx.graph.nodeToPartition != nil && i < len(idx.graph.nodeToPartition) {
			idx.graph.nodeToPartition[i] = uint16(newPartition)
		}
	}

	idx.partitionIDs = newPartitionIDs

	return result, nil
}

type RebalanceResult struct {
	PartitionsSplit   int
	VectorsMoved      int
	NewPartitionCount int
}

func (idx *Index) RebalancePartitions(maxPartitionSize int) (*RebalanceResult, error) {
	if maxPartitionSize <= 0 {
		return nil, ErrInvalidPartitionSize
	}

	insertMu.Lock()
	defer insertMu.Unlock()

	result := &RebalanceResult{}

	newPartitions := make([][]uint32, 0, len(idx.partitionIDs))
	newCentroids := make([][]float32, 0, len(idx.centroids))
	newCentroidNorms := make([]float64, 0, len(idx.centroidNorms))

	for p, ids := range idx.partitionIDs {
		if len(ids) <= maxPartitionSize {
			newPartitions = append(newPartitions, ids)
			newCentroids = append(newCentroids, idx.centroids[p])
			newCentroidNorms = append(newCentroidNorms, idx.centroidNorms[p])
			continue
		}

		result.PartitionsSplit++

		mid := len(ids) / 2
		part1 := ids[:mid]
		part2 := ids[mid:]

		centroid1 := idx.computePartitionCentroid(part1)
		centroid2 := idx.computePartitionCentroid(part2)

		newPartitions = append(newPartitions, part1, part2)
		newCentroids = append(newCentroids, centroid1, centroid2)
		newCentroidNorms = append(newCentroidNorms,
			math.Sqrt(float64(vek32.Dot(centroid1, centroid1))),
			math.Sqrt(float64(vek32.Dot(centroid2, centroid2))))

		result.VectorsMoved += len(part2)
	}

	idx.partitionIDs = newPartitions
	idx.centroids = newCentroids
	idx.centroidNorms = newCentroidNorms
	idx.config.NumPartitions = len(newPartitions)

	result.NewPartitionCount = len(newPartitions)

	if idx.graph != nil && idx.graph.nodeToPartition != nil {
		for newP, ids := range idx.partitionIDs {
			for _, id := range ids {
				if int(id) < len(idx.graph.nodeToPartition) {
					idx.graph.nodeToPartition[id] = uint16(newP)
				}
			}
		}
	}

	return result, nil
}

func (idx *Index) computePartitionCentroid(ids []uint32) []float32 {
	centroid := make([]float32, idx.dim)
	count := 0

	for _, id := range ids {
		vec := idx.getVector(id)
		if vec == nil {
			continue
		}
		for j, v := range vec {
			centroid[j] += v
		}
		count++
	}

	if count > 0 {
		invCount := 1.0 / float32(count)
		for j := range centroid {
			centroid[j] *= invCount
		}
	}

	return centroid
}

type OptimizeResult struct {
	EdgesAdded   int
	EdgesRemoved int
	NodesUpdated int
}

func (idx *Index) OptimizeGraph(sampleFraction float64) *OptimizeResult {
	insertMu.Lock()
	defer insertMu.Unlock()

	result := &OptimizeResult{}

	if idx.graph == nil || idx.numVectors == 0 {
		return result
	}

	sqrtN := int(math.Sqrt(float64(idx.numVectors)))
	defaultFraction := float64(sqrtN) / float64(idx.numVectors)

	if sampleFraction <= 0 || sampleFraction > 1 {
		sampleFraction = defaultFraction
	}

	sampleSize := int(float64(idx.numVectors) * sampleFraction)
	if sampleSize < sqrtN {
		sampleSize = min(sqrtN, idx.numVectors)
	}

	perm := rand.Perm(idx.numVectors)[:sampleSize]

	numWorkers := runtime.GOMAXPROCS(0)
	chunkSize := (sampleSize + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	var mu sync.Mutex

	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, sampleSize)
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(nodeIDs []int) {
			defer wg.Done()

			localAdded := 0
			localRemoved := 0
			localUpdated := 0

			for _, nodeIDInt := range nodeIDs {
				nodeID := uint32(nodeIDInt)

				newNeighbors := idx.graph.searchGraphBBQ(nodeID, idx.graph.R, idx.graph.R*2)

				mu.Lock()
				oldNeighbors := idx.graph.adjacency[nodeID]
				oldSet := make(map[uint32]bool)
				for _, n := range oldNeighbors {
					oldSet[n] = true
				}
				newSet := make(map[uint32]bool)
				for _, n := range newNeighbors {
					newSet[n] = true
				}

				added := 0
				removed := 0
				for n := range newSet {
					if !oldSet[n] {
						added++
					}
				}
				for n := range oldSet {
					if !newSet[n] {
						removed++
					}
				}

				if added > 0 || removed > 0 {
					idx.graph.adjacency[nodeID] = newNeighbors
					localAdded += added
					localRemoved += removed
					localUpdated++
				}
				mu.Unlock()
			}

			mu.Lock()
			result.EdgesAdded += localAdded
			result.EdgesRemoved += localRemoved
			result.NodesUpdated += localUpdated
			mu.Unlock()
		}(perm[start:end])
	}

	wg.Wait()

	return result
}
