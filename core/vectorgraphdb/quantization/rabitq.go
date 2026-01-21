package quantization

import (
	"fmt"
	"math"
	"sync"
)

// RaBitQEncoderImpl implements the RaBitQEncoder interface.
type RaBitQEncoderImpl struct {
	config   RaBitQConfig
	rotation [][]float32
	ready    bool
	mu       sync.RWMutex
}

// NewRaBitQEncoder creates a new RaBitQ encoder with the given configuration.
func NewRaBitQEncoder(config RaBitQConfig) (*RaBitQEncoderImpl, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	encoder := &RaBitQEncoderImpl{
		config: config,
	}

	encoder.rotation = GetCachedRotationMatrix(config.Dimension, config.Seed)
	encoder.ready = true

	return encoder, nil
}

func (e *RaBitQEncoderImpl) Encode(vector []float32) (RaBitQCode, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.ready {
		return nil, ErrRaBitQEncoderNotReady
	}
	if len(vector) != e.config.Dimension {
		return nil, fmt.Errorf("%w: got %d, expected %d",
			ErrRaBitQDimensionMismatch, len(vector), e.config.Dimension)
	}

	rotated := MatrixVectorMultiply(e.rotation, vector)
	return signQuantize(rotated), nil
}

func signQuantize(rotated []float32) RaBitQCode {
	numBytes := (len(rotated) + 7) / 8
	code := make(RaBitQCode, numBytes)

	for i, v := range rotated {
		if v > 0 {
			code[i/8] |= 1 << (i % 8)
		}
	}
	return code
}

func (e *RaBitQEncoderImpl) EncodeBatch(vectors [][]float32) ([]RaBitQCode, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.ready {
		return nil, ErrRaBitQEncoderNotReady
	}

	results := make([]RaBitQCode, len(vectors))
	rotated := make([]float32, e.config.Dimension)

	for i, vec := range vectors {
		if len(vec) != e.config.Dimension {
			return nil, fmt.Errorf("%w: vector %d has dimension %d, expected %d",
				ErrRaBitQDimensionMismatch, i, len(vec), e.config.Dimension)
		}

		MatrixVectorMultiplyInto(e.rotation, vec, rotated)
		results[i] = signQuantize(rotated)
	}

	return results, nil
}

func (e *RaBitQEncoderImpl) Decode(code RaBitQCode) ([]float32, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.ready {
		return nil, ErrRaBitQEncoderNotReady
	}
	if code == nil {
		return nil, ErrRaBitQNilCode
	}

	expectedBytes := (e.config.Dimension + 7) / 8
	if len(code) != expectedBytes {
		return nil, fmt.Errorf("%w: got %d bytes, expected %d",
			ErrRaBitQCodeLengthMismatch, len(code), expectedBytes)
	}

	signs := make([]float32, e.config.Dimension)
	for i := 0; i < e.config.Dimension; i++ {
		if code.GetBit(i) {
			signs[i] = 1.0
		} else {
			signs[i] = -1.0
		}
	}

	return TransposeMatrixVectorMultiply(e.rotation, signs), nil
}

func (e *RaBitQEncoderImpl) HammingDistance(a, b RaBitQCode) (int, error) {
	if a == nil || b == nil {
		return 0, ErrRaBitQNilCode
	}
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: %d vs %d bytes",
			ErrRaBitQCodeLengthMismatch, len(a), len(b))
	}

	distance := 0
	for i := range a {
		xor := a[i] ^ b[i]
		distance += popcount8[xor]
	}
	return distance, nil
}

// ApproximateL2Distance computes approximate squared L2 distance using asymmetric computation.
// For better accuracy, we compute the distance between the query and the reconstructed vector.
func (e *RaBitQEncoderImpl) ApproximateL2Distance(query []float32, code RaBitQCode) (float32, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.ready {
		return 0, ErrRaBitQEncoderNotReady
	}
	if len(query) != e.config.Dimension {
		return 0, fmt.Errorf("%w: query dimension %d, expected %d",
			ErrRaBitQDimensionMismatch, len(query), e.config.Dimension)
	}
	if code == nil {
		return 0, ErrRaBitQNilCode
	}

	rotatedQuery := MatrixVectorMultiply(e.rotation, query)

	var distance float32
	for i := 0; i < e.config.Dimension; i++ {
		sign := float32(-1.0)
		if code.GetBit(i) {
			sign = 1.0
		}
		diff := rotatedQuery[i] - sign
		distance += diff * diff
	}

	if e.config.CorrectionFactor {
		distance = applyCorrectionFactor(distance, e.config.Dimension)
	}

	return distance, nil
}

// applyCorrectionFactor scales distance per RaBitQ paper (arXiv:2405.12497) Section 3.2.
func applyCorrectionFactor(rawDistance float32, dim int) float32 {
	scale := float32(math.Pi) / (2.0 * float32(dim))
	return rawDistance * scale
}

func (e *RaBitQEncoderImpl) Dimension() int {
	return e.config.Dimension
}

func (e *RaBitQEncoderImpl) Seed() int64 {
	return e.config.Seed
}

func (e *RaBitQEncoderImpl) RotationMatrix() [][]float32 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rotation
}

// ComputeDistanceResult provides detailed distance information.
func (e *RaBitQEncoderImpl) ComputeDistanceResult(a, b RaBitQCode) (RaBitQDistanceResult, error) {
	hamming, err := e.HammingDistance(a, b)
	if err != nil {
		return RaBitQDistanceResult{}, err
	}

	result := RaBitQDistanceResult{
		HammingDistance:   hamming,
		CorrectionApplied: e.config.CorrectionFactor,
	}

	if e.config.CorrectionFactor {
		rawDist := float32(hamming) * 4.0
		result.ApproximateL2Squared = applyCorrectionFactor(rawDist, e.config.Dimension)
	} else {
		result.ApproximateL2Squared = float32(hamming) * 4.0
	}

	return result, nil
}

var _ RaBitQEncoder = (*RaBitQEncoderImpl)(nil)
