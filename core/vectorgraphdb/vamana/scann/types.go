// Package scann provides types and utilities for ScaNN (Scalable Nearest Neighbors)
// batch building operations with support for Anisotropic Vector Quantization (AVQ).
package scann

import "runtime"

// AVQConfig configures Anisotropic Vector Quantization (AVQ) for ScaNN indexing.
//
// AVQ is a learned quantization technique that weights dimensions based on their
// importance for inner product similarity. Unlike isotropic quantization which
// treats all dimensions equally, AVQ learns an anisotropic weighting that
// prioritizes dimensions contributing most to similarity scores.
//
// The anisotropic weight controls the trade-off between reconstruction error
// and inner product preservation:
//   - Weight 0.0: Pure reconstruction error minimization (isotropic)
//   - Weight 1.0: Pure inner product preservation
//   - Values in between: Balanced approach (0.2 is a good default)
type AVQConfig struct {
	// NumPartitions is the number of partitions for hierarchical quantization.
	// More partitions increase precision but require more memory.
	NumPartitions int

	// CodebookSize is the number of centroids per partition codebook.
	// Typical values are powers of 2 (e.g., 256 for 8-bit codes).
	CodebookSize int

	// AnisotropicWeight controls the anisotropy of the quantization.
	// Range [0.0, 1.0]: 0.0 = isotropic, 1.0 = fully anisotropic.
	AnisotropicWeight float64
}

// DefaultAVQConfig returns an AVQConfig with sensible defaults suitable for
// most workloads.
func DefaultAVQConfig() AVQConfig {
	return AVQConfig{
		NumPartitions:     16,
		CodebookSize:      256,
		AnisotropicWeight: 0.2,
	}
}

// BatchBuildConfig configures parallel batch building operations for ScaNN indexes.
type BatchBuildConfig struct {
	// NumWorkers is the number of parallel workers for batch operations.
	// Defaults to runtime.GOMAXPROCS(0) if zero.
	NumWorkers int

	// ProgressCallback is called periodically to report build progress.
	// Arguments are (completed, total). May be nil.
	ProgressCallback func(completed, total int)

	// TempDir is the directory for temporary files during batch building.
	// If empty, the system default temp directory is used.
	TempDir string
}

// DefaultBatchBuildConfig returns a BatchBuildConfig with NumWorkers set to
// the number of available CPU cores.
func DefaultBatchBuildConfig() BatchBuildConfig {
	return BatchBuildConfig{
		NumWorkers: runtime.GOMAXPROCS(0),
	}
}

// PartitionAssignment represents the assignment of a vector to a partition
// along with its residual vector after quantization.
type PartitionAssignment struct {
	// VectorID is the unique identifier of the original vector.
	VectorID uint32

	// PartitionID is the partition this vector is assigned to.
	PartitionID uint32

	// Residual is the difference between the original vector and its
	// quantized representation (original - centroid).
	Residual []float32
}
