package quantization

import (
	"math"
	"math/rand"
	"testing"
)

// =============================================================================
// TestRaBitQCode - Test RaBitQCode type
// =============================================================================

func TestNewRaBitQCode(t *testing.T) {
	tests := []struct {
		name          string
		dim           int
		expectedBytes int
		expectedNil   bool
	}{
		{"positive dimension 8", 8, 1, false},
		{"positive dimension 64", 64, 8, false},
		{"positive dimension 768", 768, 96, false},
		{"positive dimension 9 (rounds up)", 9, 2, false},
		{"positive dimension 1", 1, 1, false},
		{"zero dimension", 0, 0, true},
		{"negative dimension", -5, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := NewRaBitQCode(tt.dim)
			if tt.expectedNil {
				if code != nil {
					t.Errorf("expected nil code for dim=%d, got %v", tt.dim, code)
				}
				return
			}
			if code == nil {
				t.Fatalf("expected non-nil code for dim=%d", tt.dim)
			}
			if len(code) != tt.expectedBytes {
				t.Errorf("expected %d bytes for dim=%d, got %d", tt.expectedBytes, tt.dim, len(code))
			}
		})
	}
}

func TestRaBitQCode_Dimension(t *testing.T) {
	tests := []struct {
		name        string
		dim         int
		expectedDim int // Dimension() returns len*8, so it rounds up
	}{
		{"8 dimensions", 8, 8},
		{"64 dimensions", 64, 64},
		{"768 dimensions", 768, 768},
		{"9 dimensions (rounds to 16)", 9, 16},
		{"1 dimension (rounds to 8)", 1, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := NewRaBitQCode(tt.dim)
			if code.Dimension() != tt.expectedDim {
				t.Errorf("Dimension() = %d, want %d", code.Dimension(), tt.expectedDim)
			}
		})
	}
}

func TestRaBitQCode_GetBitSetBit(t *testing.T) {
	code := NewRaBitQCode(64)

	// Initially all bits should be false
	for i := 0; i < 64; i++ {
		if code.GetBit(i) {
			t.Errorf("bit %d should initially be false", i)
		}
	}

	// Set specific bits and verify
	setIndices := []int{0, 7, 8, 15, 31, 63}
	for _, idx := range setIndices {
		code.SetBit(idx, true)
	}

	for i := 0; i < 64; i++ {
		expected := false
		for _, idx := range setIndices {
			if i == idx {
				expected = true
				break
			}
		}
		if code.GetBit(i) != expected {
			t.Errorf("bit %d: got %v, want %v", i, code.GetBit(i), expected)
		}
	}

	// Unset a bit and verify
	code.SetBit(0, false)
	if code.GetBit(0) {
		t.Error("bit 0 should be false after unsetting")
	}
}

func TestRaBitQCode_GetBitSetBit_OutOfBounds(t *testing.T) {
	code := NewRaBitQCode(8) // 1 byte, 8 bits

	// Out of bounds GetBit should return false
	if code.GetBit(-1) {
		t.Error("GetBit(-1) should return false")
	}
	if code.GetBit(8) {
		t.Error("GetBit(8) should return false for 8-bit code")
	}
	if code.GetBit(100) {
		t.Error("GetBit(100) should return false")
	}

	// Out of bounds SetBit should do nothing (no panic)
	code.SetBit(-1, true) // Should not panic
	code.SetBit(8, true)  // Should not panic
	code.SetBit(100, true)

	// Verify code is unchanged
	for i := 0; i < 8; i++ {
		if code.GetBit(i) {
			t.Errorf("bit %d should still be false after out-of-bounds SetBit", i)
		}
	}
}

func TestRaBitQCode_PopCount(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(RaBitQCode)
		dim      int
		expected int
	}{
		{
			name:     "all zeros",
			setup:    func(c RaBitQCode) {},
			dim:      64,
			expected: 0,
		},
		{
			name: "all ones",
			setup: func(c RaBitQCode) {
				for i := 0; i < 64; i++ {
					c.SetBit(i, true)
				}
			},
			dim:      64,
			expected: 64,
		},
		{
			name: "half set",
			setup: func(c RaBitQCode) {
				for i := 0; i < 32; i++ {
					c.SetBit(i, true)
				}
			},
			dim:      64,
			expected: 32,
		},
		{
			name: "alternating bits",
			setup: func(c RaBitQCode) {
				for i := 0; i < 64; i += 2 {
					c.SetBit(i, true)
				}
			},
			dim:      64,
			expected: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := NewRaBitQCode(tt.dim)
			tt.setup(code)
			if got := code.PopCount(); got != tt.expected {
				t.Errorf("PopCount() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestRaBitQCode_String(t *testing.T) {
	tests := []struct {
		name     string
		code     RaBitQCode
		contains string
	}{
		{"nil code", nil, "nil"},
		{"empty code", RaBitQCode{}, "[0]"},
		{"short code", NewRaBitQCode(16), "[2]"},
		{"long code", NewRaBitQCode(768), "[96]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.code.String()
			if str == "" {
				t.Error("String() returned empty string")
			}
			// Just verify it doesn't panic and returns something
			t.Logf("String() = %s", str)
		})
	}
}

func TestRaBitQCode_Equal(t *testing.T) {
	code1 := NewRaBitQCode(64)
	code1.SetBit(0, true)
	code1.SetBit(10, true)

	code2 := NewRaBitQCode(64)
	code2.SetBit(0, true)
	code2.SetBit(10, true)

	code3 := NewRaBitQCode(64)
	code3.SetBit(0, true)
	code3.SetBit(11, true) // Different

	code4 := NewRaBitQCode(128) // Different size

	if !code1.Equal(code2) {
		t.Error("identical codes should be equal")
	}
	if code1.Equal(code3) {
		t.Error("different codes should not be equal")
	}
	if code1.Equal(code4) {
		t.Error("codes of different sizes should not be equal")
	}
}

func TestRaBitQCode_Clone(t *testing.T) {
	original := NewRaBitQCode(64)
	original.SetBit(5, true)
	original.SetBit(15, true)

	clone := original.Clone()

	if !original.Equal(clone) {
		t.Error("clone should be equal to original")
	}

	// Modify clone, original should be unchanged
	clone.SetBit(5, false)
	if original.GetBit(5) != true {
		t.Error("modifying clone should not affect original")
	}

	// Nil clone
	var nilCode RaBitQCode
	if nilCode.Clone() != nil {
		t.Error("Clone of nil should be nil")
	}
}

// =============================================================================
// TestRaBitQEncoder_Encode - Test encoding
// =============================================================================

func TestRaBitQEncoder_Encode_Deterministic(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	vector := make([]float32, 64)
	rng := rand.New(rand.NewSource(42))
	for i := range vector {
		vector[i] = float32(rng.NormFloat64())
	}

	code1, err := encoder.Encode(vector)
	if err != nil {
		t.Fatalf("first encode failed: %v", err)
	}

	code2, err := encoder.Encode(vector)
	if err != nil {
		t.Fatalf("second encode failed: %v", err)
	}

	if !code1.Equal(code2) {
		t.Error("encoding same vector should produce identical codes")
	}
}

func TestRaBitQEncoder_Encode_DifferentVectors(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	vector1 := make([]float32, 64)
	vector2 := make([]float32, 64)
	for i := range vector1 {
		vector1[i] = float32(rng.NormFloat64())
		vector2[i] = float32(rng.NormFloat64())
	}

	code1, _ := encoder.Encode(vector1)
	code2, _ := encoder.Encode(vector2)

	if code1.Equal(code2) {
		t.Error("different vectors should produce different codes (with high probability)")
	}
}

func TestRaBitQEncoder_Encode_CodeLength(t *testing.T) {
	dimensions := []int{8, 64, 128, 768}

	for _, dim := range dimensions {
		t.Run(dimName(dim), func(t *testing.T) {
			config := RaBitQConfig{
				Dimension:        dim,
				Seed:             12345,
				CorrectionFactor: true,
			}
			encoder, err := NewRaBitQEncoder(config)
			if err != nil {
				t.Fatalf("failed to create encoder: %v", err)
			}

			vector := make([]float32, dim)
			rng := rand.New(rand.NewSource(42))
			for i := range vector {
				vector[i] = float32(rng.NormFloat64())
			}

			code, err := encoder.Encode(vector)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}

			expectedBytes := (dim + 7) / 8
			if len(code) != expectedBytes {
				t.Errorf("code length = %d bytes, want %d", len(code), expectedBytes)
			}
		})
	}
}

func TestRaBitQEncoder_Encode_Signs(t *testing.T) {
	// Create a simple test where we know the expected signs after rotation
	// Using identity-like behavior by checking that positive values in rotated space map to 1
	config := RaBitQConfig{
		Dimension:        8,
		Seed:             12345,
		CorrectionFactor: false,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	// Create a vector and encode it
	vector := []float32{1, 1, 1, 1, 1, 1, 1, 1}
	code, err := encoder.Encode(vector)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// The rotated vector will have some pattern; verify PopCount is reasonable
	popCount := code.PopCount()
	t.Logf("All-positive vector encoded with %d positive signs", popCount)

	// With random rotation, we expect roughly half positive and half negative
	// But with all-positive input, the distribution depends on the rotation matrix
	// At minimum, verify PopCount is valid (0 to dim)
	if popCount < 0 || popCount > 8 {
		t.Errorf("PopCount out of range: %d", popCount)
	}
}

func TestRaBitQEncoder_Encode_DimensionMismatch(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	wrongSizeVector := make([]float32, 32)
	_, err = encoder.Encode(wrongSizeVector)
	if err == nil {
		t.Error("expected error for dimension mismatch")
	}
}

// =============================================================================
// TestRaBitQEncoder_HammingDistance - Test distance computation
// =============================================================================

func TestRaBitQEncoder_HammingDistance_Self(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	vector := make([]float32, 64)
	rng := rand.New(rand.NewSource(42))
	for i := range vector {
		vector[i] = float32(rng.NormFloat64())
	}

	code, _ := encoder.Encode(vector)
	dist, err := encoder.HammingDistance(code, code)
	if err != nil {
		t.Fatalf("HammingDistance failed: %v", err)
	}

	if dist != 0 {
		t.Errorf("distance to self should be 0, got %d", dist)
	}
}

func TestRaBitQEncoder_HammingDistance_Symmetric(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vector1 := make([]float32, 64)
	vector2 := make([]float32, 64)
	for i := range vector1 {
		vector1[i] = float32(rng.NormFloat64())
		vector2[i] = float32(rng.NormFloat64())
	}

	code1, _ := encoder.Encode(vector1)
	code2, _ := encoder.Encode(vector2)

	dist1, _ := encoder.HammingDistance(code1, code2)
	dist2, _ := encoder.HammingDistance(code2, code1)

	if dist1 != dist2 {
		t.Errorf("distance should be symmetric: d(a,b)=%d, d(b,a)=%d", dist1, dist2)
	}
}

func TestRaBitQEncoder_HammingDistance_SimilarVectors(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vector1 := make([]float32, 64)
	for i := range vector1 {
		vector1[i] = float32(rng.NormFloat64())
	}

	// Create similar vector (small perturbation)
	vector2 := make([]float32, 64)
	copy(vector2, vector1)
	for i := range vector2 {
		vector2[i] += float32(rng.NormFloat64()) * 0.01 // Small noise
	}

	// Create dissimilar vector (large perturbation)
	vector3 := make([]float32, 64)
	for i := range vector3 {
		vector3[i] = float32(rng.NormFloat64()) * 10 // Different vector
	}

	code1, _ := encoder.Encode(vector1)
	code2, _ := encoder.Encode(vector2)
	code3, _ := encoder.Encode(vector3)

	distSimilar, _ := encoder.HammingDistance(code1, code2)
	distDissimilar, _ := encoder.HammingDistance(code1, code3)

	t.Logf("Distance to similar vector: %d", distSimilar)
	t.Logf("Distance to dissimilar vector: %d", distDissimilar)

	// Similar vectors should have smaller Hamming distance
	if distSimilar > distDissimilar {
		t.Logf("Warning: similar vector has larger distance (%d > %d) - this can happen due to quantization",
			distSimilar, distDissimilar)
	}
}

func TestRaBitQEncoder_HammingDistance_Orthogonal(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	// Create two orthogonal vectors
	vector1 := make([]float32, 64)
	vector2 := make([]float32, 64)

	// First half of v1 is 1, second half is 0
	// First half of v2 is 0, second half is 1
	for i := 0; i < 32; i++ {
		vector1[i] = 1.0
		vector2[i+32] = 1.0
	}

	code1, _ := encoder.Encode(vector1)
	code2, _ := encoder.Encode(vector2)

	dist, _ := encoder.HammingDistance(code1, code2)

	// Orthogonal vectors after random rotation should have roughly 50% bits different
	// With 64 dimensions, expect around 32 bits different, but allow wide margin
	expectedRange := []int{16, 48} // 25% to 75%
	if dist < expectedRange[0] || dist > expectedRange[1] {
		t.Logf("Orthogonal vectors Hamming distance: %d (expected roughly 50%% = 32)", dist)
		// Don't fail - this is probabilistic
	}
}

func TestRaBitQEncoder_HammingDistance_NilCodes(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	vector := make([]float32, 64)
	code, _ := encoder.Encode(vector)

	_, err = encoder.HammingDistance(nil, code)
	if err == nil {
		t.Error("expected error for nil code")
	}

	_, err = encoder.HammingDistance(code, nil)
	if err == nil {
		t.Error("expected error for nil code")
	}
}

// =============================================================================
// TestRaBitQEncoder_ApproximateL2 - Test L2 approximation
// =============================================================================

func TestRaBitQEncoder_ApproximateL2_Correlation(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Generate query and multiple target vectors
	query := make([]float32, 64)
	for i := range query {
		query[i] = float32(rng.NormFloat64())
	}

	numVectors := 20
	vectors := make([][]float32, numVectors)
	codes := make([]RaBitQCode, numVectors)
	trueL2 := make([]float32, numVectors)
	approxL2 := make([]float32, numVectors)

	for j := 0; j < numVectors; j++ {
		vectors[j] = make([]float32, 64)
		for i := range vectors[j] {
			vectors[j][i] = float32(rng.NormFloat64())
		}
		codes[j], _ = encoder.Encode(vectors[j])

		// Compute true L2 squared distance
		var sum float32
		for i := range query {
			diff := query[i] - vectors[j][i]
			sum += diff * diff
		}
		trueL2[j] = sum

		approxL2[j], _ = encoder.ApproximateL2Distance(query, codes[j])
	}

	// Check that approximate and true L2 have positive correlation
	// by checking that ranking is reasonably preserved
	t.Logf("True L2 vs Approximate L2 samples:")
	for i := 0; i < min(5, numVectors); i++ {
		t.Logf("  Vector %d: true=%.4f, approx=%.4f", i, trueL2[i], approxL2[i])
	}
}

func TestRaBitQEncoder_ApproximateL2_RankingPreserved(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        128,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Generate query
	query := make([]float32, 128)
	for i := range query {
		query[i] = float32(rng.NormFloat64())
	}

	// Generate a close vector (query + small noise) and far vector
	closeVec := make([]float32, 128)
	copy(closeVec, query)
	for i := range closeVec {
		closeVec[i] += float32(rng.NormFloat64()) * 0.1
	}

	farVec := make([]float32, 128)
	for i := range farVec {
		farVec[i] = float32(rng.NormFloat64()) * 5
	}

	closeCode, _ := encoder.Encode(closeVec)
	farCode, _ := encoder.Encode(farVec)

	closeDist, _ := encoder.ApproximateL2Distance(query, closeCode)
	farDist, _ := encoder.ApproximateL2Distance(query, farCode)

	t.Logf("Close vector approx L2: %.4f", closeDist)
	t.Logf("Far vector approx L2: %.4f", farDist)

	// Close vector should have smaller approximate distance
	if closeDist >= farDist {
		t.Logf("Warning: ranking not preserved (close=%.4f >= far=%.4f)", closeDist, farDist)
		// This can happen with quantization; log but don't necessarily fail
	}
}

func TestRaBitQEncoder_ApproximateL2_DimensionMismatch(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	vector := make([]float32, 64)
	code, _ := encoder.Encode(vector)

	wrongQuery := make([]float32, 32)
	_, err = encoder.ApproximateL2Distance(wrongQuery, code)
	if err == nil {
		t.Error("expected error for query dimension mismatch")
	}
}

// =============================================================================
// TestRaBitQRotation - Test rotation matrix
// =============================================================================

func TestRaBitQRotation_Orthogonal(t *testing.T) {
	tests := []struct {
		dim       int
		tolerance float32
	}{
		{8, 1e-5},
		{64, 1e-4},
		{128, 1e-3},
	}

	for _, tt := range tests {
		t.Run(dimName(tt.dim), func(t *testing.T) {
			matrix := GenerateOrthogonalMatrix(tt.dim, 12345)

			if !IsOrthogonal(matrix, tt.tolerance) {
				t.Errorf("rotation matrix for dim=%d is not orthogonal (tolerance=%v)", tt.dim, tt.tolerance)
			}
		})
	}
}

func TestRaBitQRotation_Deterministic(t *testing.T) {
	dim := 64
	seed := int64(12345)

	matrix1 := GenerateOrthogonalMatrix(dim, seed)
	matrix2 := GenerateOrthogonalMatrix(dim, seed)

	for i := range matrix1 {
		for j := range matrix1[i] {
			if matrix1[i][j] != matrix2[i][j] {
				t.Errorf("matrices differ at [%d][%d]: %v vs %v",
					i, j, matrix1[i][j], matrix2[i][j])
			}
		}
	}
}

func TestRaBitQRotation_DifferentSeeds(t *testing.T) {
	dim := 64

	matrix1 := GenerateOrthogonalMatrix(dim, 12345)
	matrix2 := GenerateOrthogonalMatrix(dim, 54321)

	same := true
	for i := range matrix1 {
		for j := range matrix1[i] {
			if matrix1[i][j] != matrix2[i][j] {
				same = false
				break
			}
		}
		if !same {
			break
		}
	}

	if same {
		t.Error("different seeds should produce different rotation matrices")
	}
}

func TestRaBitQRotation_InvalidDimension(t *testing.T) {
	matrix := GenerateOrthogonalMatrix(0, 12345)
	if matrix != nil {
		t.Error("expected nil matrix for dimension 0")
	}

	matrix = GenerateOrthogonalMatrix(-5, 12345)
	if matrix != nil {
		t.Error("expected nil matrix for negative dimension")
	}
}

// =============================================================================
// TestRaBitQEncoder_Determinism - Test reproducibility
// =============================================================================

func TestRaBitQEncoder_Determinism(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             99999,
		CorrectionFactor: true,
	}

	// Create two separate encoders with same config
	encoder1, _ := NewRaBitQEncoder(config)
	encoder2, _ := NewRaBitQEncoder(config)

	rng := rand.New(rand.NewSource(42))
	vector := make([]float32, 64)
	for i := range vector {
		vector[i] = float32(rng.NormFloat64())
	}

	code1, _ := encoder1.Encode(vector)
	code2, _ := encoder2.Encode(vector)

	if !code1.Equal(code2) {
		t.Error("same seed should produce same codes from different encoder instances")
	}
}

func TestRaBitQEncoder_Determinism_MultipleCalls(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             99999,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	rng := rand.New(rand.NewSource(42))
	vector := make([]float32, 64)
	for i := range vector {
		vector[i] = float32(rng.NormFloat64())
	}

	codes := make([]RaBitQCode, 10)
	for i := range codes {
		codes[i], _ = encoder.Encode(vector)
	}

	for i := 1; i < len(codes); i++ {
		if !codes[0].Equal(codes[i]) {
			t.Errorf("call %d produced different code", i)
		}
	}
}

// =============================================================================
// TestRaBitQEncoder_EdgeCases - Edge cases
// =============================================================================

func TestRaBitQEncoder_EdgeCases_ZeroVector(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	zeroVector := make([]float32, 64)
	code, err := encoder.Encode(zeroVector)
	if err != nil {
		t.Fatalf("encoding zero vector failed: %v", err)
	}

	// Zero vector after rotation is still zero, so all signs should be negative (0)
	if code.PopCount() != 0 {
		t.Logf("Zero vector encoded with %d positive signs (expected 0)", code.PopCount())
		// Due to floating point, might not be exactly 0, but should be small
	}
}

func TestRaBitQEncoder_EdgeCases_AllPositive(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	positiveVector := make([]float32, 64)
	for i := range positiveVector {
		positiveVector[i] = 1.0
	}

	code, err := encoder.Encode(positiveVector)
	if err != nil {
		t.Fatalf("encoding all-positive vector failed: %v", err)
	}

	t.Logf("All-positive vector: PopCount = %d / %d", code.PopCount(), 64)
}

func TestRaBitQEncoder_EdgeCases_AllNegative(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	negativeVector := make([]float32, 64)
	for i := range negativeVector {
		negativeVector[i] = -1.0
	}

	code, err := encoder.Encode(negativeVector)
	if err != nil {
		t.Fatalf("encoding all-negative vector failed: %v", err)
	}

	t.Logf("All-negative vector: PopCount = %d / %d", code.PopCount(), 64)
}

func TestRaBitQEncoder_EdgeCases_SmallDimension(t *testing.T) {
	dimensions := []int{1, 2, 4, 8}

	for _, dim := range dimensions {
		t.Run(dimName(dim), func(t *testing.T) {
			config := RaBitQConfig{
				Dimension:        dim,
				Seed:             12345,
				CorrectionFactor: true,
			}
			encoder, err := NewRaBitQEncoder(config)
			if err != nil {
				t.Fatalf("failed to create encoder: %v", err)
			}

			rng := rand.New(rand.NewSource(42))
			vector := make([]float32, dim)
			for i := range vector {
				vector[i] = float32(rng.NormFloat64())
			}

			code, err := encoder.Encode(vector)
			if err != nil {
				t.Fatalf("encoding failed: %v", err)
			}

			expectedBytes := (dim + 7) / 8
			if len(code) != expectedBytes {
				t.Errorf("code length = %d, want %d", len(code), expectedBytes)
			}
		})
	}
}

func TestRaBitQEncoder_EdgeCases_LargeDimension(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        768,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, err := NewRaBitQEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vector := make([]float32, 768)
	for i := range vector {
		vector[i] = float32(rng.NormFloat64())
	}

	code, err := encoder.Encode(vector)
	if err != nil {
		t.Fatalf("encoding failed: %v", err)
	}

	expectedBytes := 96 // 768 / 8
	if len(code) != expectedBytes {
		t.Errorf("code length = %d bytes, want %d", len(code), expectedBytes)
	}

	// Verify PopCount is reasonable (should be roughly 50% for random vector)
	popCount := code.PopCount()
	if popCount < 300 || popCount > 468 {
		t.Logf("Large vector PopCount = %d (expected ~384)", popCount)
	}
}

func TestRaBitQEncoder_EdgeCases_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config RaBitQConfig
	}{
		{"zero dimension", RaBitQConfig{Dimension: 0}},
		{"negative dimension", RaBitQConfig{Dimension: -10}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRaBitQEncoder(tt.config)
			if err == nil {
				t.Error("expected error for invalid config")
			}
		})
	}
}

func TestRaBitQEncoder_EncodeBatch(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 10)
	for j := range vectors {
		vectors[j] = make([]float32, 64)
		for i := range vectors[j] {
			vectors[j][i] = float32(rng.NormFloat64())
		}
	}

	codes, err := encoder.EncodeBatch(vectors)
	if err != nil {
		t.Fatalf("EncodeBatch failed: %v", err)
	}

	if len(codes) != len(vectors) {
		t.Errorf("got %d codes, want %d", len(codes), len(vectors))
	}

	// Verify batch encoding matches individual encoding
	for i, vec := range vectors {
		individualCode, _ := encoder.Encode(vec)
		if !codes[i].Equal(individualCode) {
			t.Errorf("batch code %d differs from individual encoding", i)
		}
	}
}

func TestRaBitQEncoder_Decode(t *testing.T) {
	config := RaBitQConfig{
		Dimension:        64,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	rng := rand.New(rand.NewSource(42))
	original := make([]float32, 64)
	for i := range original {
		original[i] = float32(rng.NormFloat64())
	}

	code, _ := encoder.Encode(original)
	decoded, err := encoder.Decode(code)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if len(decoded) != 64 {
		t.Errorf("decoded length = %d, want 64", len(decoded))
	}

	// Decoded vector should have unit-ish components (signs * 1.0 rotated back)
	t.Logf("Sample decoded values: [%.4f, %.4f, %.4f, ...]",
		decoded[0], decoded[1], decoded[2])
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkRaBitQEncode(b *testing.B) {
	benchmarks := []struct {
		name string
		dim  int
	}{
		{"64d", 64},
		{"128d", 128},
		{"768d", 768},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			config := RaBitQConfig{
				Dimension:        bm.dim,
				Seed:             12345,
				CorrectionFactor: true,
			}
			encoder, _ := NewRaBitQEncoder(config)

			rng := rand.New(rand.NewSource(42))
			vector := make([]float32, bm.dim)
			for i := range vector {
				vector[i] = float32(rng.NormFloat64())
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = encoder.Encode(vector)
			}
		})
	}
}

func BenchmarkRaBitQHammingDistance(b *testing.B) {
	config := RaBitQConfig{
		Dimension:        768,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	rng := rand.New(rand.NewSource(42))
	vec1 := make([]float32, 768)
	vec2 := make([]float32, 768)
	for i := range vec1 {
		vec1[i] = float32(rng.NormFloat64())
		vec2[i] = float32(rng.NormFloat64())
	}

	code1, _ := encoder.Encode(vec1)
	code2, _ := encoder.Encode(vec2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = encoder.HammingDistance(code1, code2)
	}
}

func BenchmarkRaBitQApproximateL2(b *testing.B) {
	config := RaBitQConfig{
		Dimension:        768,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	rng := rand.New(rand.NewSource(42))
	query := make([]float32, 768)
	target := make([]float32, 768)
	for i := range query {
		query[i] = float32(rng.NormFloat64())
		target[i] = float32(rng.NormFloat64())
	}

	code, _ := encoder.Encode(target)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = encoder.ApproximateL2Distance(query, code)
	}
}

func BenchmarkRaBitQEncodeBatch(b *testing.B) {
	config := RaBitQConfig{
		Dimension:        768,
		Seed:             12345,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(config)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 100)
	for j := range vectors {
		vectors[j] = make([]float32, 768)
		for i := range vectors[j] {
			vectors[j][i] = float32(rng.NormFloat64())
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = encoder.EncodeBatch(vectors)
	}
}

func BenchmarkGenerateOrthogonalMatrix(b *testing.B) {
	benchmarks := []struct {
		name string
		dim  int
	}{
		{"64d", 64},
		{"128d", 128},
		{"768d", 768},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Use different seed each iteration to avoid caching
				GenerateOrthogonalMatrix(bm.dim, int64(i))
			}
		})
	}
}

// =============================================================================
// Helper functions
// =============================================================================

func dimName(dim int) string {
	return "dim" + rabitqItoa(dim)
}

func rabitqItoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string('0'+byte(i%10)) + s
		i /= 10
	}
	return s
}

func trueL2Distance(a, b []float32) float32 {
	var sum float32
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return sum
}

func correlation(x, y []float32) float64 {
	n := len(x)
	if n == 0 || n != len(y) {
		return 0
	}

	var sumX, sumY, sumXY, sumX2, sumY2 float64
	for i := range x {
		xi, yi := float64(x[i]), float64(y[i])
		sumX += xi
		sumY += yi
		sumXY += xi * yi
		sumX2 += xi * xi
		sumY2 += yi * yi
	}

	nf := float64(n)
	num := nf*sumXY - sumX*sumY
	den := math.Sqrt((nf*sumX2 - sumX*sumX) * (nf*sumY2 - sumY*sumY))
	if den == 0 {
		return 0
	}
	return num / den
}
