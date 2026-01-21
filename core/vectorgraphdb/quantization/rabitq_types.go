package quantization

import (
	"errors"
	"fmt"
)

// =============================================================================
// RaBitQ (Random Bit Quantization) Types and Interfaces
// =============================================================================
//
// RaBitQ provides zero-training-time vector quantization using random rotation
// and sign-based encoding. From "RaBitQ: Quantizing High-Dimensional Vectors
// with a Theoretical Error Bound for Approximate Nearest Neighbor Search"
// (SIGMOD 2024).
//
// Key properties:
//   - ZERO training time: rotation matrix deterministically derived from seed
//   - 1 bit per dimension: 768-dim vector → 96 bytes
//   - ~88% recall as instant baseline while LOPQ trains in background
//   - Theoretical error bounds under mild assumptions

// =============================================================================
// RaBitQCode Type
// =============================================================================

// RaBitQCode represents a sign-quantized vector as packed bits.
// Each byte contains 8 sign bits from the rotated vector.
// For a 768-dimensional vector, this produces 96 bytes.
type RaBitQCode []byte

// NewRaBitQCode creates a new RaBitQCode for a vector of the given dimension.
// Returns nil if dimension is not positive.
func NewRaBitQCode(dim int) RaBitQCode {
	if dim <= 0 {
		return nil
	}
	// Ceiling division: (dim + 7) / 8
	numBytes := (dim + 7) / 8
	return make(RaBitQCode, numBytes)
}

// Dimension returns the vector dimension this code represents.
// Note: This may be rounded up to the nearest multiple of 8.
func (c RaBitQCode) Dimension() int {
	return len(c) * 8
}

// NumBytes returns the number of bytes in the code.
func (c RaBitQCode) NumBytes() int {
	return len(c)
}

// GetBit returns the sign bit at the given index.
// Returns false if index is out of bounds.
func (c RaBitQCode) GetBit(index int) bool {
	if index < 0 || index >= len(c)*8 {
		return false
	}
	byteIdx := index / 8
	bitIdx := index % 8
	return (c[byteIdx] & (1 << bitIdx)) != 0
}

// SetBit sets the sign bit at the given index to the specified value.
// Does nothing if index is out of bounds.
func (c RaBitQCode) SetBit(index int, value bool) {
	if index < 0 || index >= len(c)*8 {
		return
	}
	byteIdx := index / 8
	bitIdx := index % 8
	if value {
		c[byteIdx] |= 1 << bitIdx
	} else {
		c[byteIdx] &^= 1 << bitIdx
	}
}

// Equal returns true if both codes have identical bytes.
func (c RaBitQCode) Equal(other RaBitQCode) bool {
	if len(c) != len(other) {
		return false
	}
	for i := range c {
		if c[i] != other[i] {
			return false
		}
	}
	return true
}

// Clone returns a deep copy of the code.
func (c RaBitQCode) Clone() RaBitQCode {
	if c == nil {
		return nil
	}
	clone := make(RaBitQCode, len(c))
	copy(clone, c)
	return clone
}

// String returns a string representation for debugging.
// Shows byte count and first/last few bytes in hex.
func (c RaBitQCode) String() string {
	if c == nil {
		return "RaBitQCode(nil)"
	}
	if len(c) == 0 {
		return "RaBitQCode[0]{}"
	}
	if len(c) <= 4 {
		return fmt.Sprintf("RaBitQCode[%d]{%02x}", len(c), []byte(c))
	}
	// Show first 2 and last 2 bytes
	return fmt.Sprintf("RaBitQCode[%d]{%02x%02x...%02x%02x}",
		len(c), c[0], c[1], c[len(c)-2], c[len(c)-1])
}

// PopCount returns the number of set bits (Hamming weight) in the code.
// Uses efficient byte-level popcount lookup.
func (c RaBitQCode) PopCount() int {
	count := 0
	for _, b := range c {
		count += popcount8[b]
	}
	return count
}

// popcount8 is a lookup table for 8-bit popcount.
var popcount8 [256]int

func init() {
	for i := 0; i < 256; i++ {
		count := 0
		for j := i; j != 0; j >>= 1 {
			count += j & 1
		}
		popcount8[i] = count
	}
}

// =============================================================================
// RaBitQConfig
// =============================================================================

// RaBitQConfig holds configuration for creating a RaBitQ encoder.
type RaBitQConfig struct {
	// Dimension is the vector dimension (required).
	// Must be positive.
	Dimension int

	// Seed is the random seed for generating the rotation matrix.
	// The same seed always produces the same rotation matrix.
	// 0 uses a default seed for deterministic behavior.
	Seed int64

	// CorrectionFactor enables analytical distance correction.
	// When true, Hamming distances are scaled to approximate L2 distances.
	// Default: true
	CorrectionFactor bool
}

// DefaultRaBitQConfig returns a RaBitQConfig with default values.
// Dimension must still be set before use.
func DefaultRaBitQConfig() RaBitQConfig {
	return RaBitQConfig{
		Dimension:        0,    // Must be set
		Seed:             0,    // Default deterministic seed
		CorrectionFactor: true, // Enable distance correction
	}
}

// Validate checks that the configuration is valid.
// Returns an error describing any issues.
func (c RaBitQConfig) Validate() error {
	if c.Dimension <= 0 {
		return ErrRaBitQInvalidDimension
	}
	return nil
}

// CodeSize returns the number of bytes in an encoded vector.
func (c RaBitQConfig) CodeSize() int {
	return (c.Dimension + 7) / 8
}

// String returns a string representation for debugging.
func (c RaBitQConfig) String() string {
	return fmt.Sprintf("RaBitQConfig{Dim:%d, Seed:%d, Correction:%t}",
		c.Dimension, c.Seed, c.CorrectionFactor)
}

// =============================================================================
// RaBitQEncoder Interface
// =============================================================================

// RaBitQEncoder encodes vectors using random rotation and sign quantization.
// Implementations are safe for concurrent use.
type RaBitQEncoder interface {
	// Encode compresses a vector to a RaBitQCode.
	// The vector dimension must match the encoder's configured dimension.
	// Returns an error if dimensions don't match.
	Encode(vector []float32) (RaBitQCode, error)

	// EncodeBatch encodes multiple vectors efficiently.
	// All vectors must have the same dimension as the encoder.
	// Returns an error if any vector has incorrect dimension.
	EncodeBatch(vectors [][]float32) ([]RaBitQCode, error)

	// Decode reconstructs an approximate vector from a RaBitQCode.
	// The reconstruction uses the rotation matrix and unit magnitude.
	// Note: This is lossy - the original vector cannot be exactly recovered.
	Decode(code RaBitQCode) ([]float32, error)

	// HammingDistance computes the Hamming distance between two codes.
	// Returns the number of differing bits.
	HammingDistance(a, b RaBitQCode) (int, error)

	// ApproximateL2Distance computes an approximate squared L2 distance.
	// Uses Hamming distance with analytical correction factor.
	// When query is provided, uses asymmetric computation for better accuracy.
	ApproximateL2Distance(query []float32, code RaBitQCode) (float32, error)

	// Dimension returns the configured vector dimension.
	Dimension() int

	// Seed returns the random seed used for the rotation matrix.
	Seed() int64

	// RotationMatrix returns the orthogonal rotation matrix.
	// Shape: [Dimension][Dimension], row-major.
	// Returns nil if not computed yet.
	RotationMatrix() [][]float32
}

// =============================================================================
// RaBitQ Errors
// =============================================================================

var (
	// ErrRaBitQInvalidDimension indicates dimension must be positive.
	ErrRaBitQInvalidDimension = errors.New("rabitq: dimension must be positive")

	// ErrRaBitQDimensionMismatch indicates vector dimension doesn't match encoder.
	ErrRaBitQDimensionMismatch = errors.New("rabitq: vector dimension mismatch")

	// ErrRaBitQCodeLengthMismatch indicates code lengths don't match for distance.
	ErrRaBitQCodeLengthMismatch = errors.New("rabitq: code length mismatch")

	// ErrRaBitQNilCode indicates a nil code was provided.
	ErrRaBitQNilCode = errors.New("rabitq: nil code provided")

	// ErrRaBitQEncoderNotReady indicates encoder is not initialized.
	ErrRaBitQEncoderNotReady = errors.New("rabitq: encoder not initialized")
)

// =============================================================================
// RaBitQ Distance Helper Types
// =============================================================================

// RaBitQDistanceResult holds the result of a distance computation.
type RaBitQDistanceResult struct {
	// HammingDistance is the raw Hamming distance (number of differing bits).
	HammingDistance int

	// ApproximateL2Squared is the corrected approximate squared L2 distance.
	// Only valid when CorrectionFactor was enabled.
	ApproximateL2Squared float32

	// CorrectionApplied indicates whether distance correction was used.
	CorrectionApplied bool
}

// String returns a string representation for debugging.
func (r RaBitQDistanceResult) String() string {
	return fmt.Sprintf("RaBitQDist{Hamming:%d, L2²:%.4f, Corrected:%t}",
		r.HammingDistance, r.ApproximateL2Squared, r.CorrectionApplied)
}
