package quantization

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/viterin/vek/vek32"
)

// CoarseQuantizer partitions vector space into coarse regions using k-means.
type CoarseQuantizer struct {
	config    LOPQConfig
	centroids [][]float32
	trained   bool
	trainedAt time.Time
	mu        sync.RWMutex
}

// NewCoarseQuantizer creates a coarse quantizer with the given configuration.
func NewCoarseQuantizer(config LOPQConfig) (*CoarseQuantizer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &CoarseQuantizer{
		config: config,
	}, nil
}

// Train learns coarse partition centroids from training vectors.
func (cq *CoarseQuantizer) Train(ctx context.Context, vectors [][]float32) error {
	cq.mu.Lock()
	defer cq.mu.Unlock()

	if len(vectors) == 0 {
		return fmt.Errorf("no training vectors provided")
	}

	for i, v := range vectors {
		if len(v) != cq.config.VectorDimension {
			return fmt.Errorf("vector %d has dimension %d, expected %d",
				i, len(v), cq.config.VectorDimension)
		}
	}

	numPartitions := cq.config.NumPartitions
	if numPartitions > len(vectors) {
		numPartitions = len(vectors)
	}

	kmeansConfig := KMeansConfig{
		MaxIterations:        100,
		NumRestarts:          3,
		ConvergenceThreshold: 1e-4,
		Seed:                 cq.config.FallbackSeed,
	}

	centroids, err := KMeansOptimal(ctx, vectors, numPartitions, kmeansConfig)
	if err != nil {
		return fmt.Errorf("k-means training failed: %w", err)
	}

	cq.centroids = centroids
	cq.trained = true
	cq.trainedAt = time.Now()

	return nil
}

// GetPartition returns the partition ID for a vector.
func (cq *CoarseQuantizer) GetPartition(vector []float32) PartitionID {
	cq.mu.RLock()
	defer cq.mu.RUnlock()

	if !cq.trained || len(vector) != cq.config.VectorDimension {
		return InvalidPartitionID
	}

	return PartitionID(cq.findNearestCentroid(vector))
}

// GetPartitionBatch returns partition IDs for multiple vectors.
func (cq *CoarseQuantizer) GetPartitionBatch(vectors [][]float32) []PartitionID {
	cq.mu.RLock()
	defer cq.mu.RUnlock()

	results := make([]PartitionID, len(vectors))

	if !cq.trained {
		for i := range results {
			results[i] = InvalidPartitionID
		}
		return results
	}

	for i, vec := range vectors {
		if len(vec) != cq.config.VectorDimension {
			results[i] = InvalidPartitionID
			continue
		}
		results[i] = PartitionID(cq.findNearestCentroid(vec))
	}

	return results
}

func (cq *CoarseQuantizer) findNearestCentroid(vector []float32) int {
	minDist := float32(1e30)
	minIdx := 0

	for i, centroid := range cq.centroids {
		dist := cq.squaredDistance(vector, centroid)
		if dist < minDist {
			minDist = dist
			minIdx = i
		}
	}

	return minIdx
}

func (cq *CoarseQuantizer) squaredDistance(a, b []float32) float32 {
	if len(a) == 0 {
		return 0
	}
	aNorm := vek32.Dot(a, a)
	bNorm := vek32.Dot(b, b)
	aDotB := vek32.Dot(a, b)
	dist := aNorm + bNorm - 2*aDotB
	if dist < 0 {
		return 0
	}
	return dist
}

// GetCentroid returns the centroid vector for a partition.
func (cq *CoarseQuantizer) GetCentroid(partition PartitionID) ([]float32, error) {
	cq.mu.RLock()
	defer cq.mu.RUnlock()

	if !cq.trained {
		return nil, ErrLOPQCodebookNotReady
	}
	if !partition.Valid() || int(partition) >= len(cq.centroids) {
		return nil, ErrLOPQPartitionNotFound
	}

	result := make([]float32, len(cq.centroids[partition]))
	copy(result, cq.centroids[partition])
	return result, nil
}

// ComputeResidual computes the residual vector (original - centroid).
func (cq *CoarseQuantizer) ComputeResidual(vector []float32, partition PartitionID) ([]float32, error) {
	cq.mu.RLock()
	defer cq.mu.RUnlock()

	if !cq.trained {
		return nil, ErrLOPQCodebookNotReady
	}
	if !partition.Valid() || int(partition) >= len(cq.centroids) {
		return nil, ErrLOPQPartitionNotFound
	}
	if len(vector) != cq.config.VectorDimension {
		return nil, ErrLOPQDimensionMismatch
	}

	centroid := cq.centroids[partition]
	residual := make([]float32, len(vector))
	for i := range vector {
		residual[i] = vector[i] - centroid[i]
	}
	return residual, nil
}

// IsTrained returns true if centroids have been learned.
func (cq *CoarseQuantizer) IsTrained() bool {
	cq.mu.RLock()
	defer cq.mu.RUnlock()
	return cq.trained
}

// NumPartitions returns the number of trained partitions.
func (cq *CoarseQuantizer) NumPartitions() int {
	cq.mu.RLock()
	defer cq.mu.RUnlock()
	return len(cq.centroids)
}

// TrainedAt returns when the coarse quantizer was trained.
func (cq *CoarseQuantizer) TrainedAt() time.Time {
	cq.mu.RLock()
	defer cq.mu.RUnlock()
	return cq.trainedAt
}

// Centroids returns a copy of all centroids.
func (cq *CoarseQuantizer) Centroids() [][]float32 {
	cq.mu.RLock()
	defer cq.mu.RUnlock()

	if !cq.trained {
		return nil
	}

	result := make([][]float32, len(cq.centroids))
	for i, c := range cq.centroids {
		result[i] = make([]float32, len(c))
		copy(result[i], c)
	}
	return result
}

// DistanceToPartition computes squared distance from vector to partition centroid.
func (cq *CoarseQuantizer) DistanceToPartition(vector []float32, partition PartitionID) (float32, error) {
	cq.mu.RLock()
	defer cq.mu.RUnlock()

	if !cq.trained {
		return 0, ErrLOPQCodebookNotReady
	}
	if !partition.Valid() || int(partition) >= len(cq.centroids) {
		return 0, ErrLOPQPartitionNotFound
	}
	if len(vector) != cq.config.VectorDimension {
		return 0, ErrLOPQDimensionMismatch
	}

	return cq.squaredDistance(vector, cq.centroids[partition]), nil
}

// GetNearestPartitions returns the k nearest partitions to a vector.
func (cq *CoarseQuantizer) GetNearestPartitions(vector []float32, k int) ([]PartitionID, error) {
	cq.mu.RLock()
	defer cq.mu.RUnlock()

	if !cq.trained {
		return nil, ErrLOPQCodebookNotReady
	}
	if len(vector) != cq.config.VectorDimension {
		return nil, ErrLOPQDimensionMismatch
	}
	if k <= 0 {
		return []PartitionID{}, nil
	}
	if k > len(cq.centroids) {
		k = len(cq.centroids)
	}

	type distPair struct {
		partition PartitionID
		distance  float32
	}

	pairs := make([]distPair, len(cq.centroids))
	for i, c := range cq.centroids {
		pairs[i] = distPair{
			partition: PartitionID(i),
			distance:  cq.squaredDistance(vector, c),
		}
	}

	for i := 0; i < k; i++ {
		minIdx := i
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].distance < pairs[minIdx].distance {
				minIdx = j
			}
		}
		pairs[i], pairs[minIdx] = pairs[minIdx], pairs[i]
	}

	result := make([]PartitionID, k)
	for i := 0; i < k; i++ {
		result[i] = pairs[i].partition
	}
	return result, nil
}
