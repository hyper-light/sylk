package quantization

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// =============================================================================
// LOPQ (Locally Optimized Product Quantization) Types
// =============================================================================
//
// LOPQ partitions the vector space and trains local codebooks per partition.
// From "Locally Optimized Product Quantization for Approximate Nearest
// Neighbor Search" (CVPR 2014).
//
// Architecture:
//   - Coarse quantizer: k-means with 1024 centroids → PartitionID
//   - Per-partition local PQ: trained on partition's vectors
//   - Fallback: RaBitQ used until local codebook is ready

// =============================================================================
// PartitionID Type
// =============================================================================

// PartitionID identifies a coarse partition in the LOPQ index.
// Valid IDs are 0 to NumPartitions-1.
type PartitionID uint16

const (
	// InvalidPartitionID represents an unassigned or invalid partition.
	InvalidPartitionID PartitionID = 0xFFFF
)

// Valid returns true if this is a valid partition ID.
func (p PartitionID) Valid() bool {
	return p != InvalidPartitionID
}

// String returns a string representation.
func (p PartitionID) String() string {
	if p == InvalidPartitionID {
		return "PartitionID(invalid)"
	}
	return fmt.Sprintf("PartitionID(%d)", p)
}

// =============================================================================
// LOPQCode Type
// =============================================================================

// LOPQCode represents an LOPQ-encoded vector.
// Format: [PartitionID (2 bytes)][LocalPQCode (variable)]
type LOPQCode struct {
	// Partition is the coarse partition this vector belongs to.
	Partition PartitionID

	// LocalCode is the PQ code within the partition's local codebook.
	// Nil if the local codebook hasn't been trained yet.
	LocalCode PQCode

	// FallbackCode is the RaBitQ code used when LocalCode is unavailable.
	// Nil if local encoding is available.
	FallbackCode RaBitQCode
}

// HasLocalCode returns true if local PQ encoding is available.
func (c LOPQCode) HasLocalCode() bool {
	return c.LocalCode != nil
}

// HasFallbackCode returns true if RaBitQ fallback is being used.
func (c LOPQCode) HasFallbackCode() bool {
	return c.FallbackCode != nil
}

// Clone returns a deep copy of the code.
func (c LOPQCode) Clone() LOPQCode {
	return LOPQCode{
		Partition:    c.Partition,
		LocalCode:    c.LocalCode.Clone(),
		FallbackCode: c.FallbackCode.Clone(),
	}
}

// Equal returns true if both codes are identical.
func (c LOPQCode) Equal(other LOPQCode) bool {
	if c.Partition != other.Partition {
		return false
	}
	if !c.LocalCode.Equal(other.LocalCode) {
		return false
	}
	if !c.FallbackCode.Equal(other.FallbackCode) {
		return false
	}
	return true
}

// String returns a string representation for debugging.
func (c LOPQCode) String() string {
	if c.HasLocalCode() {
		return fmt.Sprintf("LOPQCode{Part:%d, Local:%s}", c.Partition, c.LocalCode)
	}
	if c.HasFallbackCode() {
		return fmt.Sprintf("LOPQCode{Part:%d, Fallback:%s}", c.Partition, c.FallbackCode)
	}
	return fmt.Sprintf("LOPQCode{Part:%d, NoCode}", c.Partition)
}

// =============================================================================
// LocalCodebook Type
// =============================================================================

// LocalCodebook represents a trained PQ codebook for a specific partition.
type LocalCodebook struct {
	// PartitionID identifies which partition this codebook serves.
	PartitionID PartitionID

	// Quantizer is the trained ProductQuantizer for this partition.
	Quantizer *ProductQuantizer

	// TrainedAt is when this codebook was trained.
	TrainedAt time.Time

	// VectorCount is the number of vectors used for training.
	VectorCount int

	// Version is incremented on each re-training for cache invalidation.
	Version uint64
}

// IsReady returns true if the codebook is trained and ready for use.
func (cb *LocalCodebook) IsReady() bool {
	return cb != nil && cb.Quantizer != nil && cb.Quantizer.IsTrained()
}

// String returns a string representation for debugging.
func (cb *LocalCodebook) String() string {
	if cb == nil {
		return "LocalCodebook(nil)"
	}
	if !cb.IsReady() {
		return fmt.Sprintf("LocalCodebook{Part:%d, NotReady}", cb.PartitionID)
	}
	return fmt.Sprintf("LocalCodebook{Part:%d, Vecs:%d, V:%d, Trained:%s}",
		cb.PartitionID, cb.VectorCount, cb.Version, cb.TrainedAt.Format(time.RFC3339))
}

// =============================================================================
// LOPQConfig
// =============================================================================

// LOPQConfig holds configuration for creating an LOPQ index.
type LOPQConfig struct {
	// VectorDimension is the dimension of input vectors (required).
	VectorDimension int

	// NumPartitions is the number of coarse partitions.
	// Default: 1024 (provides good partition granularity).
	NumPartitions int

	// LocalPQConfig configures local codebooks per partition.
	// If nil, derived from VectorDimension.
	LocalPQConfig *ProductQuantizerConfig

	// TrainingThreshold is the minimum vectors per partition before training.
	// Default: 100 (statistical minimum for stable codebooks).
	TrainingThreshold int

	// FallbackSeed is the RaBitQ seed for fallback encoding.
	// Default: 42 (deterministic fallback).
	FallbackSeed int64
}

// DefaultLOPQConfig returns an LOPQConfig with default values.
// VectorDimension must still be set before use.
func DefaultLOPQConfig() LOPQConfig {
	return LOPQConfig{
		VectorDimension:   0,
		NumPartitions:     1024,
		LocalPQConfig:     nil,
		TrainingThreshold: 100,
		FallbackSeed:      42,
	}
}

// Validate checks that the configuration is valid.
func (c LOPQConfig) Validate() error {
	if c.VectorDimension <= 0 {
		return ErrLOPQInvalidDimension
	}
	if c.NumPartitions <= 0 {
		return ErrLOPQInvalidPartitionCount
	}
	if c.TrainingThreshold <= 0 {
		return ErrLOPQInvalidThreshold
	}
	return nil
}

// String returns a string representation for debugging.
func (c LOPQConfig) String() string {
	return fmt.Sprintf("LOPQConfig{Dim:%d, Parts:%d, Thresh:%d}",
		c.VectorDimension, c.NumPartitions, c.TrainingThreshold)
}

// =============================================================================
// LOPQIndex Interface
// =============================================================================

// LOPQIndex provides locally-optimized product quantization.
// Implementations are safe for concurrent use.
type LOPQIndex interface {
	// Encode encodes a vector using partition-local codebook.
	// Falls back to RaBitQ if local codebook not ready.
	Encode(vector []float32) (LOPQCode, error)

	// EncodeBatch encodes multiple vectors efficiently.
	EncodeBatch(vectors [][]float32) ([]LOPQCode, error)

	// Decode reconstructs an approximate vector from an LOPQCode.
	Decode(code LOPQCode) ([]float32, error)

	// Distance computes approximate squared L2 distance.
	// Uses local codebook distance table when available.
	Distance(query []float32, code LOPQCode) (float32, error)

	// GetPartition returns the partition ID for a vector.
	GetPartition(vector []float32) PartitionID

	// GetLocalCodebook returns the codebook for a partition.
	// Returns nil if not trained yet.
	GetLocalCodebook(partition PartitionID) *LocalCodebook

	// IsPartitionReady returns true if partition has a trained local codebook.
	IsPartitionReady(partition PartitionID) bool

	// ScheduleTraining queues a partition for background codebook training.
	// Returns immediately; training happens asynchronously.
	ScheduleTraining(ctx context.Context, partition PartitionID) error

	// GetStats returns current index statistics.
	GetStats() LOPQStats
}

// =============================================================================
// LOPQStats
// =============================================================================

// LOPQStats contains statistics about the LOPQ index.
type LOPQStats struct {
	// TotalPartitions is the configured number of partitions.
	TotalPartitions int

	// ReadyPartitions is the number of partitions with trained codebooks.
	ReadyPartitions int

	// PendingTraining is the number of partitions queued for training.
	PendingTraining int

	// TotalVectors is the total number of encoded vectors.
	TotalVectors int64

	// FallbackRatio is the fraction of encodes using RaBitQ fallback.
	FallbackRatio float64

	// AverageLocalCodebookAge is the average age of trained codebooks.
	AverageLocalCodebookAge time.Duration
}

// ReadyRatio returns the fraction of partitions with trained codebooks.
func (s LOPQStats) ReadyRatio() float64 {
	if s.TotalPartitions == 0 {
		return 0
	}
	return float64(s.ReadyPartitions) / float64(s.TotalPartitions)
}

// String returns a string representation for debugging.
func (s LOPQStats) String() string {
	return fmt.Sprintf("LOPQStats{Ready:%d/%d (%.1f%%), Pending:%d, Fallback:%.1f%%}",
		s.ReadyPartitions, s.TotalPartitions, s.ReadyRatio()*100,
		s.PendingTraining, s.FallbackRatio*100)
}

// =============================================================================
// LOPQ Errors
// =============================================================================

var (
	// ErrLOPQInvalidDimension indicates dimension must be positive.
	ErrLOPQInvalidDimension = errors.New("lopq: dimension must be positive")

	// ErrLOPQInvalidPartitionCount indicates partition count must be positive.
	ErrLOPQInvalidPartitionCount = errors.New("lopq: partition count must be positive")

	// ErrLOPQInvalidThreshold indicates training threshold must be positive.
	ErrLOPQInvalidThreshold = errors.New("lopq: training threshold must be positive")

	// ErrLOPQDimensionMismatch indicates vector dimension doesn't match config.
	ErrLOPQDimensionMismatch = errors.New("lopq: vector dimension mismatch")

	// ErrLOPQPartitionNotFound indicates partition ID is out of range.
	ErrLOPQPartitionNotFound = errors.New("lopq: partition not found")

	// ErrLOPQCodebookNotReady indicates local codebook not trained yet.
	ErrLOPQCodebookNotReady = errors.New("lopq: local codebook not ready")

	// ErrLOPQNilCode indicates a nil code was provided.
	ErrLOPQNilCode = errors.New("lopq: nil code provided")
)
