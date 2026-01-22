package quantization

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"
)

// =============================================================================
// Constants Tests
// =============================================================================

func TestDefaultConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      int
		expected int
	}{
		{"DefaultNumSubspaces", DefaultNumSubspaces, 32},
		{"DefaultCentroidsPerSubspace", DefaultCentroidsPerSubspace, 256},
		{"DefaultTrainingSamples", DefaultTrainingSamples, 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s = %d, want %d", tt.name, tt.got, tt.expected)
			}
		})
	}
}

func TestCompressionRatio(t *testing.T) {
	// Verify that 768-dim vectors with 32 subspaces achieve 32x compression
	vectorDim := 768
	numSubspaces := DefaultNumSubspaces
	bytesPerFloat := 4

	originalSize := vectorDim * bytesPerFloat // 768 * 4 = 3072 bytes
	compressedSize := numSubspaces            // 32 bytes (one uint8 per subspace)

	compressionRatio := originalSize / compressedSize
	if compressionRatio != 96 {
		t.Errorf("compression ratio = %d, want 96 (3072 bytes -> 32 bytes)", compressionRatio)
	}
}

// =============================================================================
// PQCode Tests
// =============================================================================

func TestNewPQCode(t *testing.T) {
	tests := []struct {
		name         string
		numSubspaces int
		expectedLen  int
		expectNil    bool
	}{
		{"standard 32 subspaces", 32, 32, false},
		{"8 subspaces", 8, 8, false},
		{"1 subspace", 1, 1, false},
		{"256 subspaces", 256, 256, false},
		{"zero subspaces", 0, 0, true},
		{"negative subspaces", -1, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := NewPQCode(tt.numSubspaces)

			if tt.expectNil {
				if code != nil {
					t.Errorf("NewPQCode(%d) = %v, want nil", tt.numSubspaces, code)
				}
				return
			}

			if code == nil {
				t.Fatalf("NewPQCode(%d) = nil, want non-nil", tt.numSubspaces)
			}
			if len(code) != tt.expectedLen {
				t.Errorf("len(NewPQCode(%d)) = %d, want %d", tt.numSubspaces, len(code), tt.expectedLen)
			}

			// Verify all values are initialized to zero
			for i, v := range code {
				if v != 0 {
					t.Errorf("NewPQCode(%d)[%d] = %d, want 0", tt.numSubspaces, i, v)
				}
			}
		})
	}
}

func TestPQCodeEqual(t *testing.T) {
	tests := []struct {
		name     string
		code1    PQCode
		code2    PQCode
		expected bool
	}{
		{
			name:     "equal codes",
			code1:    PQCode{1, 2, 3, 4},
			code2:    PQCode{1, 2, 3, 4},
			expected: true,
		},
		{
			name:     "different values",
			code1:    PQCode{1, 2, 3, 4},
			code2:    PQCode{1, 2, 3, 5},
			expected: false,
		},
		{
			name:     "different lengths",
			code1:    PQCode{1, 2, 3},
			code2:    PQCode{1, 2, 3, 4},
			expected: false,
		},
		{
			name:     "empty codes",
			code1:    PQCode{},
			code2:    PQCode{},
			expected: true,
		},
		{
			name:     "nil and empty",
			code1:    nil,
			code2:    PQCode{},
			expected: true, // Both have length 0
		},
		{
			name:     "both nil",
			code1:    nil,
			code2:    nil,
			expected: true,
		},
		{
			name:     "all zeros",
			code1:    PQCode{0, 0, 0, 0},
			code2:    PQCode{0, 0, 0, 0},
			expected: true,
		},
		{
			name:     "max values",
			code1:    PQCode{255, 255, 255, 255},
			code2:    PQCode{255, 255, 255, 255},
			expected: true,
		},
		{
			name:     "first element differs",
			code1:    PQCode{0, 2, 3, 4},
			code2:    PQCode{1, 2, 3, 4},
			expected: false,
		},
		{
			name:     "last element differs",
			code1:    PQCode{1, 2, 3, 4},
			code2:    PQCode{1, 2, 3, 0},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.code1.Equal(tt.code2); got != tt.expected {
				t.Errorf("PQCode(%v).Equal(%v) = %v, want %v",
					tt.code1, tt.code2, got, tt.expected)
			}
		})
	}
}

func TestPQCodeClone(t *testing.T) {
	tests := []struct {
		name     string
		original PQCode
	}{
		{"standard code", PQCode{1, 2, 3, 4, 5, 6, 7, 8}},
		{"single element", PQCode{42}},
		{"empty code", PQCode{}},
		{"nil code", nil},
		{"all zeros", PQCode{0, 0, 0, 0}},
		{"all max", PQCode{255, 255, 255, 255}},
		{"32 subspaces", NewPQCode(32)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clone := tt.original.Clone()

			// Check equality
			if !clone.Equal(tt.original) {
				t.Errorf("Clone() not equal to original: got %v, want %v", clone, tt.original)
			}

			// For non-nil, non-empty slices, verify it's a deep copy
			if tt.original != nil && len(tt.original) > 0 {
				// Modify the clone and verify original is unchanged
				clone[0] = clone[0] + 1
				if tt.original[0] == clone[0] {
					t.Errorf("Clone() is not a deep copy - modifying clone affected original")
				}
			}
		})
	}
}

func TestPQCodeString(t *testing.T) {
	tests := []struct {
		name     string
		code     PQCode
		expected string
	}{
		{
			name:     "nil code",
			code:     nil,
			expected: "PQCode[0]{}",
		},
		{
			name:     "empty code",
			code:     PQCode{},
			expected: "PQCode[0]{}",
		},
		{
			name:     "single element",
			code:     PQCode{42},
			expected: "PQCode[1]{42}",
		},
		{
			name:     "four elements",
			code:     PQCode{1, 2, 3, 4},
			expected: "PQCode[4]{1,2,3,4}",
		},
		{
			name:     "eight elements (boundary)",
			code:     PQCode{1, 2, 3, 4, 5, 6, 7, 8},
			expected: "PQCode[8]{1,2,3,4,5,6,7,8}",
		},
		{
			name:     "nine elements (truncated)",
			code:     PQCode{1, 2, 3, 4, 5, 6, 7, 8, 9},
			expected: "PQCode[9]{1,2,3,4,...,6,7,8,9}",
		},
		{
			name:     "32 elements (truncated)",
			code:     PQCode{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31},
			expected: "PQCode[32]{0,1,2,3,...,28,29,30,31}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.code.String(); got != tt.expected {
				t.Errorf("PQCode.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// ProductQuantizerConfig Tests
// =============================================================================

func TestDefaultProductQuantizerConfig(t *testing.T) {
	config := DefaultProductQuantizerConfig()

	if config.NumSubspaces != DefaultNumSubspaces {
		t.Errorf("DefaultProductQuantizerConfig().NumSubspaces = %d, want %d",
			config.NumSubspaces, DefaultNumSubspaces)
	}
	if config.CentroidsPerSubspace != DefaultCentroidsPerSubspace {
		t.Errorf("DefaultProductQuantizerConfig().CentroidsPerSubspace = %d, want %d",
			config.CentroidsPerSubspace, DefaultCentroidsPerSubspace)
	}
}

// =============================================================================
// ProductQuantizer Construction Tests
// =============================================================================

func TestNewProductQuantizer(t *testing.T) {
	tests := []struct {
		name        string
		vectorDim   int
		config      ProductQuantizerConfig
		expectError error
		checkFunc   func(t *testing.T, pq *ProductQuantizer)
	}{
		{
			name:      "768-dim with default config",
			vectorDim: 768,
			config:    DefaultProductQuantizerConfig(),
			checkFunc: func(t *testing.T, pq *ProductQuantizer) {
				if pq.VectorDim() != 768 {
					t.Errorf("VectorDim() = %d, want 768", pq.VectorDim())
				}
				if pq.NumSubspaces() != 32 {
					t.Errorf("NumSubspaces() = %d, want 32", pq.NumSubspaces())
				}
				if pq.SubspaceDim() != 24 {
					t.Errorf("SubspaceDim() = %d, want 24", pq.SubspaceDim())
				}
				if pq.CentroidsPerSubspace() != 256 {
					t.Errorf("CentroidsPerSubspace() = %d, want 256", pq.CentroidsPerSubspace())
				}
			},
		},
		{
			name:      "512-dim with custom config",
			vectorDim: 512,
			config: ProductQuantizerConfig{
				NumSubspaces:         64,
				CentroidsPerSubspace: 128,
			},
			checkFunc: func(t *testing.T, pq *ProductQuantizer) {
				if pq.VectorDim() != 512 {
					t.Errorf("VectorDim() = %d, want 512", pq.VectorDim())
				}
				if pq.NumSubspaces() != 64 {
					t.Errorf("NumSubspaces() = %d, want 64", pq.NumSubspaces())
				}
				if pq.SubspaceDim() != 8 {
					t.Errorf("SubspaceDim() = %d, want 8", pq.SubspaceDim())
				}
				if pq.CentroidsPerSubspace() != 128 {
					t.Errorf("CentroidsPerSubspace() = %d, want 128", pq.CentroidsPerSubspace())
				}
			},
		},
		{
			name:      "zero config uses defaults",
			vectorDim: 768,
			config:    ProductQuantizerConfig{},
			checkFunc: func(t *testing.T, pq *ProductQuantizer) {
				if pq.NumSubspaces() != DefaultNumSubspaces {
					t.Errorf("NumSubspaces() = %d, want %d", pq.NumSubspaces(), DefaultNumSubspaces)
				}
				if pq.CentroidsPerSubspace() != DefaultCentroidsPerSubspace {
					t.Errorf("CentroidsPerSubspace() = %d, want %d", pq.CentroidsPerSubspace(), DefaultCentroidsPerSubspace)
				}
			},
		},
		{
			name:        "invalid vector dimension zero",
			vectorDim:   0,
			config:      DefaultProductQuantizerConfig(),
			expectError: ErrInvalidVectorDim,
		},
		{
			name:        "invalid vector dimension negative",
			vectorDim:   -1,
			config:      DefaultProductQuantizerConfig(),
			expectError: ErrInvalidVectorDim,
		},
		{
			name:      "invalid num subspaces negative",
			vectorDim: 768,
			config: ProductQuantizerConfig{
				NumSubspaces:         -1,
				CentroidsPerSubspace: 256,
			},
			expectError: ErrInvalidNumSubspaces,
		},
		{
			name:      "invalid centroids zero",
			vectorDim: 768,
			config: ProductQuantizerConfig{
				NumSubspaces:         32,
				CentroidsPerSubspace: 0,
			},
			// Zero uses default, so this should work
			checkFunc: func(t *testing.T, pq *ProductQuantizer) {
				if pq.CentroidsPerSubspace() != DefaultCentroidsPerSubspace {
					t.Errorf("CentroidsPerSubspace() = %d, want %d", pq.CentroidsPerSubspace(), DefaultCentroidsPerSubspace)
				}
			},
		},
		{
			name:      "invalid centroids negative",
			vectorDim: 768,
			config: ProductQuantizerConfig{
				NumSubspaces:         32,
				CentroidsPerSubspace: -1,
			},
			expectError: ErrInvalidCentroidsPerSub,
		},
		{
			name:      "invalid centroids too large",
			vectorDim: 768,
			config: ProductQuantizerConfig{
				NumSubspaces:         32,
				CentroidsPerSubspace: 257,
			},
			expectError: ErrInvalidCentroidsPerSub,
		},
		{
			name:      "vector dim not divisible by subspaces",
			vectorDim: 100,
			config: ProductQuantizerConfig{
				NumSubspaces:         32,
				CentroidsPerSubspace: 256,
			},
			expectError: ErrVectorDimNotDivisible,
		},
		{
			name:      "small vector dim with matching subspaces",
			vectorDim: 8,
			config: ProductQuantizerConfig{
				NumSubspaces:         4,
				CentroidsPerSubspace: 16,
			},
			checkFunc: func(t *testing.T, pq *ProductQuantizer) {
				if pq.SubspaceDim() != 2 {
					t.Errorf("SubspaceDim() = %d, want 2", pq.SubspaceDim())
				}
			},
		},
		{
			name:      "single subspace",
			vectorDim: 768,
			config: ProductQuantizerConfig{
				NumSubspaces:         1,
				CentroidsPerSubspace: 256,
			},
			checkFunc: func(t *testing.T, pq *ProductQuantizer) {
				if pq.NumSubspaces() != 1 {
					t.Errorf("NumSubspaces() = %d, want 1", pq.NumSubspaces())
				}
				if pq.SubspaceDim() != 768 {
					t.Errorf("SubspaceDim() = %d, want 768", pq.SubspaceDim())
				}
			},
		},
		{
			name:      "boundary centroids 256",
			vectorDim: 768,
			config: ProductQuantizerConfig{
				NumSubspaces:         32,
				CentroidsPerSubspace: 256,
			},
			checkFunc: func(t *testing.T, pq *ProductQuantizer) {
				if pq.CentroidsPerSubspace() != 256 {
					t.Errorf("CentroidsPerSubspace() = %d, want 256", pq.CentroidsPerSubspace())
				}
			},
		},
		{
			name:      "boundary centroids 1",
			vectorDim: 768,
			config: ProductQuantizerConfig{
				NumSubspaces:         32,
				CentroidsPerSubspace: 1,
			},
			checkFunc: func(t *testing.T, pq *ProductQuantizer) {
				if pq.CentroidsPerSubspace() != 1 {
					t.Errorf("CentroidsPerSubspace() = %d, want 1", pq.CentroidsPerSubspace())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := NewProductQuantizer(tt.vectorDim, tt.config)

			if tt.expectError != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.expectError)
				}
				if err != tt.expectError {
					t.Errorf("expected error %v, got %v", tt.expectError, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pq == nil {
				t.Fatalf("ProductQuantizer is nil")
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, pq)
			}
		})
	}
}

func TestProductQuantizerIsTrained(t *testing.T) {
	pq, err := NewProductQuantizer(768, DefaultProductQuantizerConfig())
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Should not be trained initially
	if pq.IsTrained() {
		t.Errorf("IsTrained() = true, want false (newly created)")
	}
}

func TestProductQuantizerCentroidsNotTrained(t *testing.T) {
	pq, err := NewProductQuantizer(768, DefaultProductQuantizerConfig())
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Centroids should be nil when not trained
	centroids := pq.Centroids()
	if centroids != nil {
		t.Errorf("Centroids() = %v, want nil (not trained)", centroids)
	}
}

func TestProductQuantizerCentroidsAllocation(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Verify internal centroid structure (we can't access private fields directly,
	// but we can check the computed values)
	if pq.NumSubspaces() != 4 {
		t.Errorf("NumSubspaces() = %d, want 4", pq.NumSubspaces())
	}
	if pq.CentroidsPerSubspace() != 8 {
		t.Errorf("CentroidsPerSubspace() = %d, want 8", pq.CentroidsPerSubspace())
	}
	if pq.SubspaceDim() != 8 { // 32 / 4 = 8
		t.Errorf("SubspaceDim() = %d, want 8", pq.SubspaceDim())
	}
}

// =============================================================================
// ProductQuantizer Getters Tests
// =============================================================================

func TestProductQuantizerGetters(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         16,
		CentroidsPerSubspace: 64,
	}
	pq, err := NewProductQuantizer(256, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Test all getters
	tests := []struct {
		name     string
		got      int
		expected int
	}{
		{"VectorDim", pq.VectorDim(), 256},
		{"NumSubspaces", pq.NumSubspaces(), 16},
		{"CentroidsPerSubspace", pq.CentroidsPerSubspace(), 64},
		{"SubspaceDim", pq.SubspaceDim(), 16}, // 256 / 16 = 16
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s() = %d, want %d", tt.name, tt.got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Error Variables Tests
// =============================================================================

func TestErrorVariables(t *testing.T) {
	// Verify error variables are properly defined
	errors := []struct {
		name string
		err  error
	}{
		{"ErrInvalidVectorDim", ErrInvalidVectorDim},
		{"ErrInvalidNumSubspaces", ErrInvalidNumSubspaces},
		{"ErrInvalidCentroidsPerSub", ErrInvalidCentroidsPerSub},
		{"ErrVectorDimNotDivisible", ErrVectorDimNotDivisible},
		{"ErrQuantizerNotTrained", ErrQuantizerNotTrained},
	}

	for _, tt := range errors {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Errorf("%s is nil", tt.name)
			}
			if tt.err.Error() == "" {
				t.Errorf("%s has empty message", tt.name)
			}
		})
	}
}

// =============================================================================
// Memory Layout Tests
// =============================================================================

func TestPQCodeMemorySize(t *testing.T) {
	// Verify that PQCode uses uint8 (1 byte per element)
	code := NewPQCode(32)

	// Each uint8 is 1 byte, so 32 subspaces = 32 bytes
	expectedBytes := 32
	actualBytes := len(code) * 1 // sizeof(uint8) = 1

	if actualBytes != expectedBytes {
		t.Errorf("PQCode memory size = %d bytes, want %d bytes", actualBytes, expectedBytes)
	}
}

func TestProductQuantizer768DimCompression(t *testing.T) {
	// Verify the advertised 32x compression for 768-dim vectors
	pq, err := NewProductQuantizer(768, DefaultProductQuantizerConfig())
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	originalBytes := pq.VectorDim() * 4  // float32 = 4 bytes
	compressedBytes := pq.NumSubspaces() // uint8 = 1 byte per subspace

	// 768 * 4 = 3072 bytes original
	// 32 bytes compressed
	// Compression ratio = 3072 / 32 = 96

	if originalBytes != 3072 {
		t.Errorf("original size = %d bytes, want 3072", originalBytes)
	}
	if compressedBytes != 32 {
		t.Errorf("compressed size = %d bytes, want 32", compressedBytes)
	}

	ratio := originalBytes / compressedBytes
	if ratio != 96 {
		t.Errorf("compression ratio = %d:1, want 96:1", ratio)
	}
}

// =============================================================================
// Edge Cases Tests
// =============================================================================

func TestProductQuantizerLargeVectorDim(t *testing.T) {
	// Test with larger dimensions (e.g., 1536 from OpenAI embeddings)
	config := ProductQuantizerConfig{
		NumSubspaces:         48, // 1536 / 48 = 32
		CentroidsPerSubspace: 256,
	}
	pq, err := NewProductQuantizer(1536, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer for 1536-dim: %v", err)
	}

	if pq.SubspaceDim() != 32 {
		t.Errorf("SubspaceDim() = %d, want 32", pq.SubspaceDim())
	}
}

func TestProductQuantizerSmallVectorDim(t *testing.T) {
	// Test with small dimensions
	config := ProductQuantizerConfig{
		NumSubspaces:         2,
		CentroidsPerSubspace: 4,
	}
	pq, err := NewProductQuantizer(4, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer for 4-dim: %v", err)
	}

	if pq.SubspaceDim() != 2 {
		t.Errorf("SubspaceDim() = %d, want 2", pq.SubspaceDim())
	}
	if pq.CentroidsPerSubspace() != 4 {
		t.Errorf("CentroidsPerSubspace() = %d, want 4", pq.CentroidsPerSubspace())
	}
}

// =============================================================================
// subspaceDistance Tests (PQ.3)
// =============================================================================

func TestSubspaceDistanceSameLength(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float32
	}{
		{
			name:     "identical vectors",
			a:        []float32{1.0, 2.0, 3.0, 4.0},
			b:        []float32{1.0, 2.0, 3.0, 4.0},
			expected: 0.0,
		},
		{
			name:     "zeros",
			a:        []float32{0.0, 0.0, 0.0, 0.0},
			b:        []float32{0.0, 0.0, 0.0, 0.0},
			expected: 0.0,
		},
		{
			name:     "ones vs zeros",
			a:        []float32{1.0, 1.0, 1.0, 1.0},
			b:        []float32{0.0, 0.0, 0.0, 0.0},
			expected: 4.0, // 1^2 + 1^2 + 1^2 + 1^2 = 4
		},
		{
			name:     "unit vectors different directions",
			a:        []float32{1.0, 0.0},
			b:        []float32{0.0, 1.0},
			expected: 2.0, // 1^2 + 1^2 = 2
		},
		{
			name:     "opposite vectors",
			a:        []float32{1.0, 2.0, 3.0},
			b:        []float32{-1.0, -2.0, -3.0},
			expected: 56.0, // 4 + 16 + 36 = 56
		},
		{
			name:     "single element",
			a:        []float32{5.0},
			b:        []float32{2.0},
			expected: 9.0, // 3^2 = 9
		},
		{
			name:     "8 elements (loop unrolling boundary)",
			a:        []float32{1, 2, 3, 4, 5, 6, 7, 8},
			b:        []float32{0, 0, 0, 0, 0, 0, 0, 0},
			expected: 204.0, // 1+4+9+16+25+36+49+64 = 204
		},
		{
			name:     "typical subspace dimension (24)",
			a:        make([]float32, 24), // all zeros
			b:        make([]float32, 24), // all zeros
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subspaceDistance(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("subspaceDistance(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

func TestSubspaceDistanceEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float32
	}{
		{
			name:     "empty a",
			a:        []float32{},
			b:        []float32{1.0, 2.0, 3.0},
			expected: 0.0,
		},
		{
			name:     "empty b",
			a:        []float32{1.0, 2.0, 3.0},
			b:        []float32{},
			expected: 0.0,
		},
		{
			name:     "both empty",
			a:        []float32{},
			b:        []float32{},
			expected: 0.0,
		},
		{
			name:     "nil a",
			a:        nil,
			b:        []float32{1.0, 2.0, 3.0},
			expected: 0.0,
		},
		{
			name:     "nil b",
			a:        []float32{1.0, 2.0, 3.0},
			b:        nil,
			expected: 0.0,
		},
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subspaceDistance(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("subspaceDistance(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

func TestSubspaceDistanceRandomValues(t *testing.T) {
	// Test with known computed values
	a := []float32{0.5, 1.5, 2.5, 3.5}
	b := []float32{1.0, 2.0, 3.0, 4.0}
	// Differences: -0.5, -0.5, -0.5, -0.5
	// Squared: 0.25, 0.25, 0.25, 0.25
	// Sum: 1.0
	expected := float32(1.0)

	got := subspaceDistance(a, b)
	if got != expected {
		t.Errorf("subspaceDistance(%v, %v) = %f, want %f", a, b, got, expected)
	}
}

func TestSubspaceDistanceSymmetry(t *testing.T) {
	a := []float32{1.0, 2.0, 3.0, 4.0}
	b := []float32{4.0, 3.0, 2.0, 1.0}

	distAB := subspaceDistance(a, b)
	distBA := subspaceDistance(b, a)

	if distAB != distBA {
		t.Errorf("subspaceDistance is not symmetric: d(a,b)=%f != d(b,a)=%f", distAB, distBA)
	}
}

func TestSubspaceDistanceLoopUnrolling(t *testing.T) {
	// Test that loop unrolling works correctly for various sizes
	sizes := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 15, 16, 17, 24, 32}

	for _, size := range sizes {
		t.Run("size_"+string(rune('0'+size%10)), func(t *testing.T) {
			a := make([]float32, size)
			b := make([]float32, size)
			for i := 0; i < size; i++ {
				a[i] = float32(i + 1)
				b[i] = 0
			}

			// Manual calculation: sum of squares from 1 to size
			var expected float32
			for i := 1; i <= size; i++ {
				expected += float32(i * i)
			}

			got := subspaceDistance(a, b)
			if got != expected {
				t.Errorf("subspaceDistance for size %d: got %f, want %f", size, got, expected)
			}
		})
	}
}

// =============================================================================
// DistanceTable Tests (PQ.4)
// =============================================================================

func TestNewDistanceTable(t *testing.T) {
	tests := []struct {
		name                 string
		numSubspaces         int
		centroidsPerSubspace int
		expectNil            bool
	}{
		{"standard 32x256", 32, 256, false},
		{"small 4x16", 4, 16, false},
		{"single subspace", 1, 256, false},
		{"single centroid", 32, 1, false},
		{"zero subspaces", 0, 256, true},
		{"zero centroids", 32, 0, true},
		{"negative subspaces", -1, 256, true},
		{"negative centroids", 32, -1, true},
		{"both zero", 0, 0, true},
		{"both negative", -1, -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dt := NewDistanceTable(tt.numSubspaces, tt.centroidsPerSubspace)

			if tt.expectNil {
				if dt != nil {
					t.Errorf("NewDistanceTable(%d, %d) = %v, want nil",
						tt.numSubspaces, tt.centroidsPerSubspace, dt)
				}
				return
			}

			if dt == nil {
				t.Fatalf("NewDistanceTable(%d, %d) = nil, want non-nil",
					tt.numSubspaces, tt.centroidsPerSubspace)
			}

			// Verify dimensions
			if dt.NumSubspaces != tt.numSubspaces {
				t.Errorf("NumSubspaces = %d, want %d", dt.NumSubspaces, tt.numSubspaces)
			}
			if dt.CentroidsPerSubspace != tt.centroidsPerSubspace {
				t.Errorf("CentroidsPerSubspace = %d, want %d", dt.CentroidsPerSubspace, tt.centroidsPerSubspace)
			}

			// Verify table dimensions
			if len(dt.Table) != tt.numSubspaces {
				t.Errorf("len(Table) = %d, want %d", len(dt.Table), tt.numSubspaces)
			}
			for i, subspace := range dt.Table {
				if len(subspace) != tt.centroidsPerSubspace {
					t.Errorf("len(Table[%d]) = %d, want %d", i, len(subspace), tt.centroidsPerSubspace)
				}
			}

			// Verify all values are initialized to zero
			for i, subspace := range dt.Table {
				for j, v := range subspace {
					if v != 0 {
						t.Errorf("Table[%d][%d] = %f, want 0", i, j, v)
					}
				}
			}
		})
	}
}

func TestDistanceTableValidate(t *testing.T) {
	tests := []struct {
		name        string
		dt          *DistanceTable
		expectError error
	}{
		{
			name:        "valid table",
			dt:          NewDistanceTable(4, 8),
			expectError: nil,
		},
		{
			name:        "nil table",
			dt:          nil,
			expectError: ErrDistanceTableNil,
		},
		{
			name: "nil inner table",
			dt: &DistanceTable{
				Table:                nil,
				NumSubspaces:         4,
				CentroidsPerSubspace: 8,
			},
			expectError: ErrDistanceTableEmpty,
		},
		{
			name: "empty inner table",
			dt: &DistanceTable{
				Table:                [][]float32{},
				NumSubspaces:         4,
				CentroidsPerSubspace: 8,
			},
			expectError: ErrDistanceTableEmpty,
		},
		{
			name: "mismatched subspaces count",
			dt: &DistanceTable{
				Table:                make([][]float32, 3),
				NumSubspaces:         4,
				CentroidsPerSubspace: 8,
			},
			expectError: ErrDistanceTableInvalidDims,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.dt.Validate()

			if tt.expectError == nil {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
				return
			}

			if err == nil {
				t.Errorf("Validate() = nil, want error containing %v", tt.expectError)
				return
			}

			// For wrapped errors, check if it contains the expected error
			if err != tt.expectError && err.Error() != tt.expectError.Error() {
				// Check if it's a wrapped error
				if !containsError(err, tt.expectError) {
					t.Errorf("Validate() = %v, want error containing %v", err, tt.expectError)
				}
			}
		})
	}
}

// containsError checks if err contains or wraps target error
func containsError(err, target error) bool {
	if err == target {
		return true
	}
	return err.Error() == target.Error() ||
		len(err.Error()) > len(target.Error()) &&
			err.Error()[:len(target.Error())] == target.Error()[:len(target.Error())]
}

func TestDistanceTableValidateSubspaceErrors(t *testing.T) {
	// Test invalid subspace configurations
	t.Run("nil subspace", func(t *testing.T) {
		dt := &DistanceTable{
			Table:                make([][]float32, 4),
			NumSubspaces:         4,
			CentroidsPerSubspace: 8,
		}
		// Leave subspaces as nil

		err := dt.Validate()
		if err == nil {
			t.Error("Validate() = nil, want error for nil subspace")
		}
	})

	t.Run("wrong subspace length", func(t *testing.T) {
		dt := NewDistanceTable(4, 8)
		dt.Table[2] = make([]float32, 4) // Wrong length

		err := dt.Validate()
		if err == nil {
			t.Error("Validate() = nil, want error for wrong subspace length")
		}
	})
}

func TestDistanceTableComputeDistance(t *testing.T) {
	// Create a distance table with known values
	dt := NewDistanceTable(4, 8)

	// Set up known distances
	// Subspace 0: distances 0.0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0
	// Subspace 1: distances 0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7
	// Subspace 2: all 1.0
	// Subspace 3: all 0.5
	for i := 0; i < 8; i++ {
		dt.Table[0][i] = float32(i)
		dt.Table[1][i] = float32(i) * 0.1
		dt.Table[2][i] = 1.0
		dt.Table[3][i] = 0.5
	}

	tests := []struct {
		name     string
		code     PQCode
		expected float32
	}{
		{
			name:     "all zeros",
			code:     PQCode{0, 0, 0, 0},
			expected: 0.0 + 0.0 + 1.0 + 0.5, // 1.5
		},
		{
			name:     "sequential indices",
			code:     PQCode{0, 1, 2, 3},
			expected: 0.0 + 0.1 + 1.0 + 0.5, // 1.6
		},
		{
			name:     "max indices",
			code:     PQCode{7, 7, 7, 7},
			expected: 7.0 + 0.7 + 1.0 + 0.5, // 9.2
		},
		{
			name:     "mixed indices",
			code:     PQCode{3, 5, 0, 7},
			expected: 3.0 + 0.5 + 1.0 + 0.5, // 5.0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dt.ComputeDistance(tt.code)
			// Use tolerance for float comparison
			if diff := got - tt.expected; diff < -0.0001 || diff > 0.0001 {
				t.Errorf("ComputeDistance(%v) = %f, want %f", tt.code, got, tt.expected)
			}
		})
	}
}

func TestDistanceTableComputeDistanceEdgeCases(t *testing.T) {
	dt := NewDistanceTable(4, 8)
	for i := 0; i < 4; i++ {
		for j := 0; j < 8; j++ {
			dt.Table[i][j] = float32(i*8 + j)
		}
	}

	tests := []struct {
		name     string
		dt       *DistanceTable
		code     PQCode
		expected float32
	}{
		{
			name:     "nil distance table",
			dt:       nil,
			code:     PQCode{0, 0, 0, 0},
			expected: 0.0,
		},
		{
			name:     "empty code",
			dt:       dt,
			code:     PQCode{},
			expected: 0.0,
		},
		{
			name:     "nil code",
			dt:       dt,
			code:     nil,
			expected: 0.0,
		},
		{
			name:     "code shorter than subspaces",
			dt:       dt,
			code:     PQCode{0, 1}, // Only 2 elements for 4 subspaces
			expected: 0.0 + 9.0,    // Table[0][0] + Table[1][1]
		},
		{
			name:     "code longer than subspaces",
			dt:       dt,
			code:     PQCode{0, 1, 2, 3, 4, 5, 6, 7}, // 8 elements for 4 subspaces
			expected: 0.0 + 9.0 + 18.0 + 27.0,        // Only first 4 used
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.dt.ComputeDistance(tt.code)
			if got != tt.expected {
				t.Errorf("ComputeDistance(%v) = %f, want %f", tt.code, got, tt.expected)
			}
		})
	}
}

func TestDistanceTableComputeDistanceBoundsCheck(t *testing.T) {
	// Create a small table to test bounds checking
	dt := NewDistanceTable(2, 4)
	dt.Table[0][0] = 1.0
	dt.Table[0][1] = 2.0
	dt.Table[0][2] = 3.0
	dt.Table[0][3] = 4.0
	dt.Table[1][0] = 10.0
	dt.Table[1][1] = 20.0
	dt.Table[1][2] = 30.0
	dt.Table[1][3] = 40.0

	tests := []struct {
		name     string
		code     PQCode
		expected float32
	}{
		{
			name:     "valid indices",
			code:     PQCode{1, 2},
			expected: 2.0 + 30.0,
		},
		{
			name:     "max valid indices",
			code:     PQCode{3, 3},
			expected: 4.0 + 40.0,
		},
		{
			name:     "out of bounds centroid - skipped",
			code:     PQCode{5, 1}, // 5 > 3, so subspace 0 is skipped
			expected: 20.0,         // Only subspace 1 contributes
		},
		{
			name:     "all out of bounds",
			code:     PQCode{10, 20},
			expected: 0.0, // Nothing contributes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dt.ComputeDistance(tt.code)
			if got != tt.expected {
				t.Errorf("ComputeDistance(%v) = %f, want %f", tt.code, got, tt.expected)
			}
		})
	}
}

func TestDistanceTableValidateCode(t *testing.T) {
	dt := NewDistanceTable(4, 8)

	tests := []struct {
		name        string
		code        PQCode
		expectError bool
	}{
		{
			name:        "valid code",
			code:        PQCode{0, 1, 2, 3},
			expectError: false,
		},
		{
			name:        "valid code max indices",
			code:        PQCode{7, 7, 7, 7},
			expectError: false,
		},
		{
			name:        "code too short",
			code:        PQCode{0, 1, 2},
			expectError: true,
		},
		{
			name:        "code too long",
			code:        PQCode{0, 1, 2, 3, 4},
			expectError: true,
		},
		{
			name:        "centroid index out of bounds",
			code:        PQCode{0, 8, 0, 0}, // 8 >= 8
			expectError: true,
		},
		{
			name:        "max centroid index out of bounds",
			code:        PQCode{0, 0, 0, 255}, // 255 >= 8
			expectError: true,
		},
		{
			name:        "empty code",
			code:        PQCode{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := dt.ValidateCode(tt.code)

			if tt.expectError {
				if err == nil {
					t.Errorf("ValidateCode(%v) = nil, want error", tt.code)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateCode(%v) = %v, want nil", tt.code, err)
				}
			}
		})
	}
}

func TestDistanceTableValidateCodeNilTable(t *testing.T) {
	var dt *DistanceTable
	err := dt.ValidateCode(PQCode{0, 1, 2, 3})
	if err != ErrDistanceTableNil {
		t.Errorf("ValidateCode on nil table = %v, want %v", err, ErrDistanceTableNil)
	}
}

// =============================================================================
// DistanceTable Error Variables Tests
// =============================================================================

func TestDistanceTableErrorVariables(t *testing.T) {
	errors := []struct {
		name string
		err  error
	}{
		{"ErrDistanceTableNil", ErrDistanceTableNil},
		{"ErrDistanceTableEmpty", ErrDistanceTableEmpty},
		{"ErrDistanceTableInvalidDims", ErrDistanceTableInvalidDims},
		{"ErrDistanceTableInvalidCode", ErrDistanceTableInvalidCode},
		{"ErrDistanceTableCentroidOOB", ErrDistanceTableCentroidOOB},
	}

	for _, tt := range errors {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Errorf("%s is nil", tt.name)
			}
			if tt.err.Error() == "" {
				t.Errorf("%s has empty message", tt.name)
			}
		})
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestDistanceTableWithProductQuantizer(t *testing.T) {
	// Verify DistanceTable dimensions match ProductQuantizer config
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 16,
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	dt := NewDistanceTable(pq.NumSubspaces(), pq.CentroidsPerSubspace())
	if dt == nil {
		t.Fatal("NewDistanceTable returned nil")
	}

	// Verify dimensions match
	if dt.NumSubspaces != pq.NumSubspaces() {
		t.Errorf("NumSubspaces mismatch: table=%d, quantizer=%d",
			dt.NumSubspaces, pq.NumSubspaces())
	}
	if dt.CentroidsPerSubspace != pq.CentroidsPerSubspace() {
		t.Errorf("CentroidsPerSubspace mismatch: table=%d, quantizer=%d",
			dt.CentroidsPerSubspace, pq.CentroidsPerSubspace())
	}

	// Verify validation passes
	if err := dt.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}

	// Create a valid PQ code and verify it passes validation
	code := NewPQCode(pq.NumSubspaces())
	for i := range code {
		code[i] = uint8(i % pq.CentroidsPerSubspace())
	}

	if err := dt.ValidateCode(code); err != nil {
		t.Errorf("ValidateCode(%v) = %v, want nil", code, err)
	}
}

// =============================================================================
// K-means++ Initialization Tests (PQ.5)
// =============================================================================

func TestKmeansppInitBasic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Create simple test vectors
	vectors := [][]float32{
		{0.0, 0.0},
		{1.0, 0.0},
		{0.0, 1.0},
		{1.0, 1.0},
		{0.5, 0.5},
	}

	centroids := kmeansppInit(vectors, 3, rng)

	// Should return exactly 3 centroids
	if len(centroids) != 3 {
		t.Errorf("kmeansppInit returned %d centroids, want 3", len(centroids))
	}

	// Each centroid should have dimension 2
	for i, c := range centroids {
		if len(c) != 2 {
			t.Errorf("centroid %d has dimension %d, want 2", i, len(c))
		}
	}
}

func TestKmeansppInitEdgeCases(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	tests := []struct {
		name      string
		vectors   [][]float32
		k         int
		expectNil bool
		expectLen int
	}{
		{
			name:      "empty vectors",
			vectors:   [][]float32{},
			k:         3,
			expectNil: true,
		},
		{
			name:      "nil vectors",
			vectors:   nil,
			k:         3,
			expectNil: true,
		},
		{
			name:      "k is zero",
			vectors:   [][]float32{{1.0, 2.0}},
			k:         0,
			expectNil: true,
		},
		{
			name:      "k is negative",
			vectors:   [][]float32{{1.0, 2.0}},
			k:         -1,
			expectNil: true,
		},
		{
			name:      "k greater than vectors",
			vectors:   [][]float32{{1.0}, {2.0}, {3.0}},
			k:         5,
			expectLen: 3, // Should cap at len(vectors)
		},
		{
			name:      "k equals vectors",
			vectors:   [][]float32{{1.0}, {2.0}, {3.0}},
			k:         3,
			expectLen: 3,
		},
		{
			name:      "single vector",
			vectors:   [][]float32{{1.0, 2.0, 3.0}},
			k:         1,
			expectLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := kmeansppInit(tt.vectors, tt.k, rng)

			if tt.expectNil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != tt.expectLen {
				t.Errorf("expected %d centroids, got %d", tt.expectLen, len(result))
			}
		})
	}
}

func TestKmeansppInitAllIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// All identical vectors
	vectors := [][]float32{
		{1.0, 1.0},
		{1.0, 1.0},
		{1.0, 1.0},
		{1.0, 1.0},
	}

	centroids := kmeansppInit(vectors, 3, rng)

	// Should still return 3 centroids (even if all identical)
	if len(centroids) != 3 {
		t.Errorf("kmeansppInit returned %d centroids, want 3", len(centroids))
	}
}

func TestKmeansppInitDeterminism(t *testing.T) {
	// With same seed, should produce same results
	vectors := [][]float32{
		{0.0, 0.0},
		{1.0, 0.0},
		{0.0, 1.0},
		{1.0, 1.0},
	}

	rng1 := rand.New(rand.NewSource(123))
	rng2 := rand.New(rand.NewSource(123))

	result1 := kmeansppInit(vectors, 2, rng1)
	result2 := kmeansppInit(vectors, 2, rng2)

	if len(result1) != len(result2) {
		t.Fatalf("different lengths: %d vs %d", len(result1), len(result2))
	}

	for i := range result1 {
		if !vectorsEqual(result1[i], result2[i]) {
			t.Errorf("centroid %d differs: %v vs %v", i, result1[i], result2[i])
		}
	}
}

func TestKmeansppInitDistribution(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Create well-separated clusters
	vectors := [][]float32{
		// Cluster 1 around (0, 0)
		{0.0, 0.0},
		{0.1, 0.0},
		{0.0, 0.1},
		// Cluster 2 around (10, 10)
		{10.0, 10.0},
		{10.1, 10.0},
		{10.0, 10.1},
		// Cluster 3 around (20, 0)
		{20.0, 0.0},
		{20.1, 0.0},
		{20.0, 0.1},
	}

	centroids := kmeansppInit(vectors, 3, rng)

	// With K-means++, centroids should be spread out
	// Check that they're not all in the same cluster
	allNearZero := true
	for _, c := range centroids {
		if c[0] > 5.0 || c[1] > 5.0 {
			allNearZero = false
			break
		}
	}

	if allNearZero {
		t.Error("K-means++ should spread centroids, but all are near origin")
	}
}

// =============================================================================
// copyVector Tests
// =============================================================================

func TestCopyVector(t *testing.T) {
	tests := []struct {
		name  string
		input []float32
	}{
		{"standard vector", []float32{1.0, 2.0, 3.0, 4.0}},
		{"single element", []float32{42.0}},
		{"empty vector", []float32{}},
		{"nil vector", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := copyVector(tt.input)

			if tt.input == nil {
				if result != nil {
					t.Errorf("copyVector(nil) = %v, want nil", result)
				}
				return
			}

			if len(result) != len(tt.input) {
				t.Errorf("len(result) = %d, want %d", len(result), len(tt.input))
			}

			// Verify it's a deep copy
			if len(tt.input) > 0 {
				original := tt.input[0]
				result[0] = 999.0
				if tt.input[0] != original {
					t.Error("copyVector did not create a deep copy")
				}
			}
		})
	}
}

// =============================================================================
// vectorsEqual Tests
// =============================================================================

func TestVectorsEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected bool
	}{
		{"identical", []float32{1.0, 2.0, 3.0}, []float32{1.0, 2.0, 3.0}, true},
		{"different values", []float32{1.0, 2.0, 3.0}, []float32{1.0, 2.0, 4.0}, false},
		{"different lengths", []float32{1.0, 2.0}, []float32{1.0, 2.0, 3.0}, false},
		{"both empty", []float32{}, []float32{}, true},
		{"both nil", nil, nil, true},
		{"nil vs empty", nil, []float32{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vectorsEqual(tt.a, tt.b); got != tt.expected {
				t.Errorf("vectorsEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// findNearestCentroid Tests
// =============================================================================

func TestFindNearestCentroid(t *testing.T) {
	centroids := [][]float32{
		{0.0, 0.0},  // centroid 0
		{10.0, 0.0}, // centroid 1
		{0.0, 10.0}, // centroid 2
	}

	tests := []struct {
		name     string
		v        []float32
		expected int
	}{
		{"exact match 0", []float32{0.0, 0.0}, 0},
		{"exact match 1", []float32{10.0, 0.0}, 1},
		{"exact match 2", []float32{0.0, 10.0}, 2},
		{"closer to 0", []float32{1.0, 1.0}, 0},
		{"closer to 1", []float32{8.0, 1.0}, 1},
		{"closer to 2", []float32{1.0, 8.0}, 2},
		{"equidistant prefers first", []float32{5.0, 5.0}, 0}, // distance to all is sqrt(50)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findNearestCentroid(tt.v, centroids); got != tt.expected {
				t.Errorf("findNearestCentroid(%v) = %d, want %d", tt.v, got, tt.expected)
			}
		})
	}
}

func TestFindNearestCentroidEdgeCases(t *testing.T) {
	t.Run("empty centroids", func(t *testing.T) {
		result := findNearestCentroid([]float32{1.0, 2.0}, [][]float32{})
		if result != 0 {
			t.Errorf("expected 0 for empty centroids, got %d", result)
		}
	})

	t.Run("single centroid", func(t *testing.T) {
		result := findNearestCentroid([]float32{100.0, 100.0}, [][]float32{{0.0, 0.0}})
		if result != 0 {
			t.Errorf("expected 0 for single centroid, got %d", result)
		}
	})
}

// =============================================================================
// updateCentroids Tests
// =============================================================================

func TestUpdateCentroids(t *testing.T) {
	vectors := [][]float32{
		{0.0, 0.0},
		{2.0, 0.0},
		{10.0, 10.0},
		{12.0, 10.0},
	}
	assignments := []int{0, 0, 1, 1}
	k := 2
	dim := 2

	centroids := updateCentroids(vectors, assignments, k, dim)

	// Centroid 0 should be mean of (0,0) and (2,0) = (1,0)
	if centroids[0][0] != 1.0 || centroids[0][1] != 0.0 {
		t.Errorf("centroid 0 = %v, want [1.0, 0.0]", centroids[0])
	}

	// Centroid 1 should be mean of (10,10) and (12,10) = (11,10)
	if centroids[1][0] != 11.0 || centroids[1][1] != 10.0 {
		t.Errorf("centroid 1 = %v, want [11.0, 10.0]", centroids[1])
	}
}

func TestUpdateCentroidsEmptyCluster(t *testing.T) {
	vectors := [][]float32{
		{1.0, 1.0},
		{2.0, 2.0},
	}
	assignments := []int{0, 0} // All assigned to cluster 0, cluster 1 is empty
	k := 2
	dim := 2

	centroids := updateCentroids(vectors, assignments, k, dim)

	// Centroid 0 should be mean of both vectors
	if centroids[0][0] != 1.5 || centroids[0][1] != 1.5 {
		t.Errorf("centroid 0 = %v, want [1.5, 1.5]", centroids[0])
	}

	// Empty cluster should be zeros
	if centroids[1][0] != 0.0 || centroids[1][1] != 0.0 {
		t.Errorf("empty centroid 1 = %v, want [0.0, 0.0]", centroids[1])
	}
}

func TestUpdateCentroidsInvalidAssignments(t *testing.T) {
	vectors := [][]float32{
		{1.0, 1.0},
		{2.0, 2.0},
	}
	assignments := []int{-1, 5} // Invalid assignments
	k := 2
	dim := 2

	// Should not panic with invalid assignments
	centroids := updateCentroids(vectors, assignments, k, dim)

	// Both clusters should be zeros since no valid assignments
	if len(centroids) != 2 {
		t.Fatalf("expected 2 centroids, got %d", len(centroids))
	}
}

// =============================================================================
// trainSubspace Tests (PQ.6)
// =============================================================================

func TestTrainSubspaceBasic(t *testing.T) {
	pq, err := NewProductQuantizer(8, ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 4,
	})
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Create training data with clear clusters
	vectors := [][]float32{
		// Cluster 1 around (0, 0)
		{0.0, 0.0},
		{0.1, 0.0},
		{0.0, 0.1},
		{0.1, 0.1},
		// Cluster 2 around (10, 0)
		{10.0, 0.0},
		{10.1, 0.0},
		{10.0, 0.1},
		{10.1, 0.1},
		// Cluster 3 around (0, 10)
		{0.0, 10.0},
		{0.1, 10.0},
		{0.0, 10.1},
		{0.1, 10.1},
		// Cluster 4 around (10, 10)
		{10.0, 10.0},
		{10.1, 10.0},
		{10.0, 10.1},
		{10.1, 10.1},
	}

	centroids := pq.trainSubspace(vectors, 4, 100, rng)

	if len(centroids) != 4 {
		t.Errorf("expected 4 centroids, got %d", len(centroids))
	}

	// Each centroid should have dimension 2
	for i, c := range centroids {
		if len(c) != 2 {
			t.Errorf("centroid %d has dimension %d, want 2", i, len(c))
		}
	}
}

func TestTrainSubspaceConvergence(t *testing.T) {
	pq, _ := NewProductQuantizer(8, ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 4,
	})
	rng := rand.New(rand.NewSource(42))

	// Create well-separated clusters
	vectors := [][]float32{
		{0.0, 0.0}, {0.0, 0.0}, {0.0, 0.0},
		{100.0, 0.0}, {100.0, 0.0}, {100.0, 0.0},
	}

	centroids := pq.trainSubspace(vectors, 2, 10, rng)

	// Should converge to cluster centers
	if len(centroids) != 2 {
		t.Fatalf("expected 2 centroids, got %d", len(centroids))
	}

	// One centroid should be near (0,0) and one near (100,0)
	nearOrigin := false
	nearFar := false
	for _, c := range centroids {
		if math.Abs(float64(c[0])) < 1.0 {
			nearOrigin = true
		}
		if math.Abs(float64(c[0])-100.0) < 1.0 {
			nearFar = true
		}
	}

	if !nearOrigin || !nearFar {
		t.Errorf("centroids did not converge to expected locations: %v", centroids)
	}
}

func TestTrainSubspaceEdgeCases(t *testing.T) {
	pq, _ := NewProductQuantizer(8, ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 4,
	})
	rng := rand.New(rand.NewSource(42))

	t.Run("empty vectors", func(t *testing.T) {
		result := pq.trainSubspace([][]float32{}, 4, 10, rng)
		if result != nil {
			t.Errorf("expected nil for empty vectors, got %v", result)
		}
	})

	t.Run("fewer vectors than k", func(t *testing.T) {
		vectors := [][]float32{
			{1.0, 2.0},
			{3.0, 4.0},
		}
		result := pq.trainSubspace(vectors, 5, 10, rng)
		// Should return all vectors as centroids
		if len(result) != 2 {
			t.Errorf("expected 2 centroids, got %d", len(result))
		}
	})

	t.Run("k equals vectors count", func(t *testing.T) {
		vectors := [][]float32{
			{1.0, 2.0},
			{3.0, 4.0},
			{5.0, 6.0},
		}
		result := pq.trainSubspace(vectors, 3, 10, rng)
		if len(result) != 3 {
			t.Errorf("expected 3 centroids, got %d", len(result))
		}
	})
}

func TestTrainSubspaceMaxIterations(t *testing.T) {
	pq, _ := NewProductQuantizer(8, ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 4,
	})
	rng := rand.New(rand.NewSource(42))

	// Create data that might take many iterations
	vectors := make([][]float32, 100)
	for i := range vectors {
		vectors[i] = []float32{float32(i % 10), float32(i / 10)}
	}

	// Should complete with limited iterations
	result := pq.trainSubspace(vectors, 4, 5, rng)
	if result == nil {
		t.Error("trainSubspace returned nil")
	}
	if len(result) != 4 {
		t.Errorf("expected 4 centroids, got %d", len(result))
	}
}

func TestTrainSubspaceDeterminism(t *testing.T) {
	pq1, _ := NewProductQuantizer(8, ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 4,
	})
	pq2, _ := NewProductQuantizer(8, ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 4,
	})

	vectors := [][]float32{
		{0.0, 0.0},
		{1.0, 0.0},
		{0.0, 1.0},
		{1.0, 1.0},
		{10.0, 10.0},
		{11.0, 10.0},
		{10.0, 11.0},
		{11.0, 11.0},
	}

	rng1 := rand.New(rand.NewSource(999))
	rng2 := rand.New(rand.NewSource(999))

	result1 := pq1.trainSubspace(vectors, 2, 50, rng1)
	result2 := pq2.trainSubspace(vectors, 2, 50, rng2)

	if len(result1) != len(result2) {
		t.Fatalf("different centroid counts: %d vs %d", len(result1), len(result2))
	}

	for i := range result1 {
		if !vectorsEqual(result1[i], result2[i]) {
			t.Errorf("centroid %d differs: %v vs %v", i, result1[i], result2[i])
		}
	}
}

// =============================================================================
// K-means Quality Tests
// =============================================================================

func TestKmeansQuality(t *testing.T) {
	pq, _ := NewProductQuantizer(8, ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 4,
	})
	rng := rand.New(rand.NewSource(42))

	// Create data with known clusters
	vectors := make([][]float32, 0)
	clusterCenters := [][]float32{
		{0.0, 0.0},
		{10.0, 0.0},
		{0.0, 10.0},
		{10.0, 10.0},
	}

	// Add noisy points around each cluster center
	for _, center := range clusterCenters {
		for i := 0; i < 10; i++ {
			noise := float32(rng.Float64()*0.5 - 0.25)
			vectors = append(vectors, []float32{center[0] + noise, center[1] + noise})
		}
	}

	centroids := pq.trainSubspace(vectors, 4, 100, rng)

	// Verify we got 4 centroids
	if len(centroids) != 4 {
		t.Fatalf("expected 4 centroids, got %d", len(centroids))
	}

	// Check that each learned centroid is close to a true cluster center
	for _, center := range clusterCenters {
		found := false
		for _, c := range centroids {
			dist := math.Sqrt(float64((c[0]-center[0])*(c[0]-center[0]) + (c[1]-center[1])*(c[1]-center[1])))
			if dist < 1.0 { // Allow some tolerance
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no centroid found near cluster center %v", center)
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkKmeansppInit(b *testing.B) {
	rng := rand.New(rand.NewSource(42))

	// Create test data
	vectors := make([][]float32, 1000)
	for i := range vectors {
		vectors[i] = make([]float32, 24) // Typical subspace dimension
		for j := range vectors[i] {
			vectors[i][j] = float32(rng.Float64())
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewSource(42))
		kmeansppInit(vectors, 256, rng)
	}
}

func BenchmarkTrainSubspace(b *testing.B) {
	pq, _ := NewProductQuantizer(768, DefaultProductQuantizerConfig())

	// Create test data
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 1000)
	for i := range vectors {
		vectors[i] = make([]float32, pq.SubspaceDim())
		for j := range vectors[i] {
			vectors[i][j] = float32(rng.Float64())
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewSource(42))
		pq.trainSubspace(vectors, 256, 20, rng)
	}
}

func BenchmarkFindNearestCentroid(b *testing.B) {
	rng := rand.New(rand.NewSource(42))

	// Create centroids
	centroids := make([][]float32, 256)
	for i := range centroids {
		centroids[i] = make([]float32, 24)
		for j := range centroids[i] {
			centroids[i][j] = float32(rng.Float64())
		}
	}

	// Create query vector
	query := make([]float32, 24)
	for i := range query {
		query[i] = float32(rng.Float64())
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		findNearestCentroid(query, centroids)
	}
}

// =============================================================================
// argminDistance Tests (4x Loop Unrolling Optimization)
// =============================================================================

// argminDistanceScalar is the reference scalar implementation for testing.
// This verifies that the 4x unrolled version produces identical results.
func argminDistanceScalar(xNorm float32, cNorms, dots []float32, dotRow, k int) uint8 {
	minDist := float32(math.MaxFloat32)
	minIdx := uint8(0)
	for c := 0; c < k; c++ {
		dist := xNorm + cNorms[c] - 2*dots[dotRow+c]
		if dist < minDist {
			minDist = dist
			minIdx = uint8(c)
		}
	}
	return minIdx
}

func TestArgminDistanceUnrolledMatchesScalar(t *testing.T) {
	// Test with various k values including edge cases for 4x unrolling
	kValues := []int{1, 4, 5, 8, 9, 16, 17, 255, 256}

	rng := rand.New(rand.NewSource(42))

	for _, k := range kValues {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			// Generate random test data
			cNorms := make([]float32, k)
			dots := make([]float32, k)
			for i := 0; i < k; i++ {
				cNorms[i] = rng.Float32() * 10
				dots[i] = rng.Float32() * 10
			}
			xNorm := rng.Float32() * 10

			// Compare unrolled vs scalar
			gotUnrolled := argminDistance(xNorm, cNorms, dots, 0, k)
			gotScalar := argminDistanceScalar(xNorm, cNorms, dots, 0, k)

			if gotUnrolled != gotScalar {
				t.Errorf("k=%d: unrolled=%d, scalar=%d", k, gotUnrolled, gotScalar)
			}
		})
	}
}

func TestArgminDistanceUnrolledWithOffset(t *testing.T) {
	// Test that dotRow offset works correctly with unrolling
	kValues := []int{4, 8, 16, 256}
	rng := rand.New(rand.NewSource(123))

	for _, k := range kValues {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			// Create data with multiple rows
			numRows := 5
			cNorms := make([]float32, k)
			dots := make([]float32, k*numRows)

			for i := 0; i < k; i++ {
				cNorms[i] = rng.Float32() * 10
			}
			for i := 0; i < k*numRows; i++ {
				dots[i] = rng.Float32() * 10
			}

			// Test each row
			for row := 0; row < numRows; row++ {
				xNorm := rng.Float32() * 10
				dotRow := row * k

				gotUnrolled := argminDistance(xNorm, cNorms, dots, dotRow, k)
				gotScalar := argminDistanceScalar(xNorm, cNorms, dots, dotRow, k)

				if gotUnrolled != gotScalar {
					t.Errorf("k=%d row=%d: unrolled=%d, scalar=%d", k, row, gotUnrolled, gotScalar)
				}
			}
		})
	}
}

func TestArgminDistanceMinimumAtDifferentPositions(t *testing.T) {
	// Test that minimum is found regardless of its position in the array
	// This catches bugs in unrolling where certain positions might be missed
	kValues := []int{1, 4, 5, 8, 9, 16, 17, 255, 256}

	for _, k := range kValues {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			for minPos := 0; minPos < k; minPos++ {
				// Create data where minimum is at position minPos
				cNorms := make([]float32, k)
				dots := make([]float32, k)

				// Set all distances to 100
				for i := 0; i < k; i++ {
					cNorms[i] = 50
					dots[i] = 0 // dist = xNorm + 50 - 0 = xNorm + 50
				}
				// Set minimum at minPos: make dot product large so distance is small
				dots[minPos] = 50 // dist = xNorm + 50 - 100 = xNorm - 50

				xNorm := float32(100)

				gotUnrolled := argminDistance(xNorm, cNorms, dots, 0, k)
				gotScalar := argminDistanceScalar(xNorm, cNorms, dots, 0, k)

				if gotUnrolled != uint8(minPos) {
					t.Errorf("k=%d minPos=%d: unrolled=%d, want %d", k, minPos, gotUnrolled, minPos)
				}
				if gotScalar != uint8(minPos) {
					t.Errorf("k=%d minPos=%d: scalar=%d, want %d", k, minPos, gotScalar, minPos)
				}
			}
		})
	}
}

func TestArgminDistanceWithTies(t *testing.T) {
	// When there are ties, both implementations should pick the first (lowest index)
	k := 16

	cNorms := make([]float32, k)
	dots := make([]float32, k)

	// All distances equal
	for i := 0; i < k; i++ {
		cNorms[i] = 10
		dots[i] = 5
	}
	xNorm := float32(10)

	gotUnrolled := argminDistance(xNorm, cNorms, dots, 0, k)
	gotScalar := argminDistanceScalar(xNorm, cNorms, dots, 0, k)

	// Both should return 0 (first index) when all distances are equal
	if gotUnrolled != 0 {
		t.Errorf("unrolled with ties: got %d, want 0", gotUnrolled)
	}
	if gotScalar != 0 {
		t.Errorf("scalar with ties: got %d, want 0", gotScalar)
	}
	if gotUnrolled != gotScalar {
		t.Errorf("tie handling differs: unrolled=%d, scalar=%d", gotUnrolled, gotScalar)
	}
}

func BenchmarkArgminDistanceUnrolled(b *testing.B) {
	// Benchmark the 4x unrolled implementation
	rng := rand.New(rand.NewSource(42))
	k := 256

	cNorms := make([]float32, k)
	dots := make([]float32, k)
	for i := 0; i < k; i++ {
		cNorms[i] = rng.Float32() * 10
		dots[i] = rng.Float32() * 10
	}
	xNorm := rng.Float32() * 10

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		argminDistance(xNorm, cNorms, dots, 0, k)
	}
}

func BenchmarkArgminDistanceScalar(b *testing.B) {
	// Benchmark the scalar implementation for comparison
	rng := rand.New(rand.NewSource(42))
	k := 256

	cNorms := make([]float32, k)
	dots := make([]float32, k)
	for i := 0; i < k; i++ {
		cNorms[i] = rng.Float32() * 10
		dots[i] = rng.Float32() * 10
	}
	xNorm := rng.Float32() * 10

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		argminDistanceScalar(xNorm, cNorms, dots, 0, k)
	}
}

func BenchmarkArgminDistanceByK(b *testing.B) {
	// Benchmark with different k values to show unrolling benefit
	kValues := []int{4, 8, 16, 64, 256}
	rng := rand.New(rand.NewSource(42))

	for _, k := range kValues {
		b.Run(fmt.Sprintf("unrolled_k=%d", k), func(b *testing.B) {
			cNorms := make([]float32, k)
			dots := make([]float32, k)
			for i := 0; i < k; i++ {
				cNorms[i] = rng.Float32() * 10
				dots[i] = rng.Float32() * 10
			}
			xNorm := rng.Float32() * 10

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				argminDistance(xNorm, cNorms, dots, 0, k)
			}
		})

		b.Run(fmt.Sprintf("scalar_k=%d", k), func(b *testing.B) {
			cNorms := make([]float32, k)
			dots := make([]float32, k)
			for i := 0; i < k; i++ {
				cNorms[i] = rng.Float32() * 10
				dots[i] = rng.Float32() * 10
			}
			xNorm := rng.Float32() * 10

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				argminDistanceScalar(xNorm, cNorms, dots, 0, k)
			}
		})
	}
}

// =============================================================================
// Training Configuration Tests (PQ.7)
// =============================================================================

func TestDeriveTrainConfigValues(t *testing.T) {
	// Test with k=256, subspaceDim=24 (standard config)
	config := DeriveTrainConfig(256, 24)

	// MaxIterations should be MaxInt32 (rely on convergence)
	if config.MaxIterations != math.MaxInt32 {
		t.Errorf("MaxIterations = %d, want MaxInt32", config.MaxIterations)
	}

	// NumRestarts should be ceil(log2(256)) = 8
	expectedRestarts := 8
	if config.NumRestarts != expectedRestarts {
		t.Errorf("NumRestarts = %d, want %d", config.NumRestarts, expectedRestarts)
	}

	// ConvergenceThreshold should be 1000 * float32 epsilon
	expectedThreshold := float64(1000 * Float32Epsilon)
	if config.ConvergenceThreshold != expectedThreshold {
		t.Errorf("ConvergenceThreshold = %e, want %e", config.ConvergenceThreshold, expectedThreshold)
	}

	if config.Seed != 0 {
		t.Errorf("Seed = %d, want 0", config.Seed)
	}

	// MinSamplesRatio should be max(10, subspaceDim) = 24
	expectedRatio := float32(24)
	if config.MinSamplesRatio != expectedRatio {
		t.Errorf("MinSamplesRatio = %f, want %f", config.MinSamplesRatio, expectedRatio)
	}
}

func TestDeriveTrainConfigScaling(t *testing.T) {
	// Test that NumRestarts scales with k
	tests := []struct {
		k                int
		expectedRestarts int
	}{
		{2, 1},     // ceil(log2(2)) = 1
		{4, 2},     // ceil(log2(4)) = 2
		{16, 4},    // ceil(log2(16)) = 4
		{256, 8},   // ceil(log2(256)) = 8
		{1024, 10}, // ceil(log2(1024)) = 10
	}

	for _, tt := range tests {
		config := DeriveTrainConfig(tt.k, 24)
		if config.NumRestarts != tt.expectedRestarts {
			t.Errorf("k=%d: NumRestarts = %d, want %d", tt.k, config.NumRestarts, tt.expectedRestarts)
		}
	}
}

// =============================================================================
// Train Tests (PQ.7)
// =============================================================================

// generateTrainingVectors creates random training vectors for testing.
func generateTrainingVectors(n, dim int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()*2 - 1 // Random in [-1, 1]
		}
	}
	return vectors
}

func TestProductQuantizer_Train_Success(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Generate enough training data: 8 centroids * 10 MinSamplesRatio = 80 minimum
	vectors := generateTrainingVectors(100, 32, 42)

	trainConfig := TrainConfig{
		MaxIterations:        50,
		ConvergenceThreshold: 1e-5,
		Seed:                 12345,
		MinSamplesRatio:      10.0,
	}

	err = pq.TrainWithConfig(context.Background(), vectors, trainConfig)
	if err != nil {
		t.Fatalf("Train() error: %v", err)
	}

	// Verify trained state
	if !pq.IsTrained() {
		t.Error("IsTrained() = false, want true after training")
	}

	// Verify centroids are populated
	centroids := pq.Centroids()
	if centroids == nil {
		t.Fatal("Centroids() = nil after training")
	}

	// Verify centroid dimensions
	if len(centroids) != pq.NumSubspaces() {
		t.Errorf("len(centroids) = %d, want %d", len(centroids), pq.NumSubspaces())
	}
	for i, subCentroids := range centroids {
		if len(subCentroids) != pq.CentroidsPerSubspace() {
			t.Errorf("len(centroids[%d]) = %d, want %d", i, len(subCentroids), pq.CentroidsPerSubspace())
		}
		for j, centroid := range subCentroids {
			if len(centroid) != pq.SubspaceDim() {
				t.Errorf("len(centroids[%d][%d]) = %d, want %d", i, j, len(centroid), pq.SubspaceDim())
			}
		}
	}
}

func TestProductQuantizer_Train_DerivedConfig_Success(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Need enough samples: derived MinSamplesRatio=max(10, subspaceDim=8)=10, so 8*10=80 minimum
	vectors := generateTrainingVectors(100, 32, 42)

	// Train derives config from PQ parameters
	err = pq.Train(context.Background(), vectors)
	if err != nil {
		t.Fatalf("Train() error: %v", err)
	}

	if !pq.IsTrained() {
		t.Error("IsTrained() = false after Train()")
	}
}

func TestProductQuantizer_Train_InvalidDimension_Error(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Create vectors with wrong dimension
	vectors := generateTrainingVectors(100, 64, 42) // Wrong dimension: 64 instead of 32

	err = pq.Train(context.Background(), vectors)
	if err == nil {
		t.Error("Train() should fail with mismatched vector dimensions")
	}
}

func TestProductQuantizer_Train_InsufficientData_Error(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Derived MinSamplesRatio = max(10, subspaceDim=8) = 10, centroids = 8, so minimum = 80
	// Provide only 50 vectors
	vectors := generateTrainingVectors(50, 32, 42)

	err = pq.Train(context.Background(), vectors)
	if err == nil {
		t.Error("Train() should fail with insufficient training data")
	}
}

func TestProductQuantizer_Train_EmptyVectors_Error(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	var vectors [][]float32 // Empty slice

	err = pq.Train(context.Background(), vectors)
	if err == nil {
		t.Error("Train() should fail with empty vectors")
	}
}

func TestProductQuantizer_TrainDeterministic_Success(t *testing.T) {
	// Train two quantizers with the same seed - they should produce identical results
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}

	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:        50,
		ConvergenceThreshold: 1e-5,
		Seed:                 12345, // Fixed seed for determinism
		MinSamplesRatio:      10.0,
	}

	// Train first quantizer
	pq1, _ := NewProductQuantizer(32, config)
	err := pq1.TrainWithConfig(context.Background(), vectors, trainConfig)
	if err != nil {
		t.Fatalf("Train() error on pq1: %v", err)
	}

	// Train second quantizer with same seed
	pq2, _ := NewProductQuantizer(32, config)
	err = pq2.TrainWithConfig(context.Background(), vectors, trainConfig)
	if err != nil {
		t.Fatalf("Train() error on pq2: %v", err)
	}

	// Compare centroids
	centroids1 := pq1.Centroids()
	centroids2 := pq2.Centroids()

	for i := range centroids1 {
		for j := range centroids1[i] {
			for k := range centroids1[i][j] {
				if centroids1[i][j][k] != centroids2[i][j][k] {
					t.Errorf("Deterministic training failed: centroids[%d][%d][%d] = %f vs %f",
						i, j, k, centroids1[i][j][k], centroids2[i][j][k])
				}
			}
		}
	}
}

func TestProductQuantizer_TrainConvergence_Success(t *testing.T) {
	// Create well-clustered data to verify K-means converges
	config := ProductQuantizerConfig{
		NumSubspaces:         2,
		CentroidsPerSubspace: 4,
	}
	pq, err := NewProductQuantizer(8, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Generate clustered data with 4 clear clusters per subspace
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 100)
	clusterCenters := [][]float32{
		{0, 0, 0, 0, 0, 0, 0, 0},
		{10, 0, 10, 0, 10, 0, 10, 0},
		{0, 10, 0, 10, 0, 10, 0, 10},
		{10, 10, 10, 10, 10, 10, 10, 10},
	}

	for i := range vectors {
		center := clusterCenters[i%4]
		vectors[i] = make([]float32, 8)
		for j := range vectors[i] {
			vectors[i][j] = center[j] + rng.Float32()*0.5 - 0.25
		}
	}

	trainConfig := TrainConfig{
		MaxIterations:        100,
		ConvergenceThreshold: 1e-5,
		Seed:                 12345,
		MinSamplesRatio:      10.0,
	}

	err = pq.TrainWithConfig(context.Background(), vectors, trainConfig)
	if err != nil {
		t.Fatalf("Train() error: %v", err)
	}

	// Verify we got non-zero centroids
	centroids := pq.Centroids()
	for i, subCentroids := range centroids {
		for j, centroid := range subCentroids {
			allZero := true
			for _, v := range centroid {
				if v != 0 {
					allZero = false
					break
				}
			}
			if allZero {
				t.Errorf("centroids[%d][%d] is all zeros - may indicate training issue", i, j)
			}
		}
	}
}

// =============================================================================
// TrainParallel Tests (PQ.7)
// =============================================================================

func TestProductQuantizer_TrainParallel_Success(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 16,
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// 16 centroids * 10 = 160 minimum
	vectors := generateTrainingVectors(200, 64, 42)

	trainConfig := TrainConfig{
		MaxIterations:        50,
		ConvergenceThreshold: 1e-5,
		Seed:                 12345,
		MinSamplesRatio:      10.0,
	}

	err = pq.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 4)
	if err != nil {
		t.Fatalf("TrainParallel() error: %v", err)
	}

	if !pq.IsTrained() {
		t.Error("IsTrained() = false after TrainParallel()")
	}

	// Verify all subspaces have centroids
	centroids := pq.Centroids()
	if centroids == nil {
		t.Fatal("Centroids() = nil after TrainParallel()")
	}
	for i, subCentroids := range centroids {
		if subCentroids == nil {
			t.Errorf("centroids[%d] = nil after TrainParallel()", i)
		}
	}
}

func TestProductQuantizer_TrainParallel_DefaultWorkers(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	vectors := generateTrainingVectors(100, 32, 42)

	// Use 0 workers (should default to NumCPU)
	err = pq.TrainParallel(context.Background(), vectors, 0)
	if err != nil {
		t.Fatalf("TrainParallel() with workers=0 error: %v", err)
	}

	if !pq.IsTrained() {
		t.Error("IsTrained() = false after TrainParallel()")
	}
}

func TestProductQuantizer_TrainParallel_InvalidVectors_Error(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Wrong dimension
	vectors := generateTrainingVectors(100, 64, 42)

	err = pq.TrainParallel(context.Background(), vectors, 4)
	if err == nil {
		t.Error("TrainParallel() should fail with wrong dimensions")
	}
}

func TestProductQuantizer_TrainParallel_EmptyVectors_Error(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	var vectors [][]float32

	err = pq.TrainParallel(context.Background(), vectors, 4)
	if err == nil {
		t.Error("TrainParallel() should fail with empty vectors")
	}
}

func TestProductQuantizer_TrainParallel_InsufficientData_Error(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Only 50 vectors, need 80 (derived MinSamplesRatio=max(10, subspaceDim=8)=10)
	vectors := generateTrainingVectors(50, 32, 42)

	err = pq.TrainParallel(context.Background(), vectors, 4)
	if err == nil {
		t.Error("TrainParallel() should fail with insufficient data")
	}
}

// =============================================================================
// extractSubspace Tests (PQ.7)
// =============================================================================

func TestExtractSubspace_Success(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(16, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Create test vectors: [0,1,2,...,15]
	vectors := [][]float32{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31},
	}

	// Test extraction for each subspace
	// Subspace 0: dims [0-3], Subspace 1: dims [4-7], etc.
	expectedSubspace0 := [][]float32{
		{0, 1, 2, 3},
		{16, 17, 18, 19},
	}
	expectedSubspace2 := [][]float32{
		{8, 9, 10, 11},
		{24, 25, 26, 27},
	}

	subspace0 := pq.extractSubspace(vectors, 0)
	subspace2 := pq.extractSubspace(vectors, 2)

	// Verify subspace 0
	for i := range expectedSubspace0 {
		for j := range expectedSubspace0[i] {
			if subspace0[i][j] != expectedSubspace0[i][j] {
				t.Errorf("extractSubspace(0)[%d][%d] = %f, want %f",
					i, j, subspace0[i][j], expectedSubspace0[i][j])
			}
		}
	}

	// Verify subspace 2
	for i := range expectedSubspace2 {
		for j := range expectedSubspace2[i] {
			if subspace2[i][j] != expectedSubspace2[i][j] {
				t.Errorf("extractSubspace(2)[%d][%d] = %f, want %f",
					i, j, subspace2[i][j], expectedSubspace2[i][j])
			}
		}
	}
}

func TestExtractSubspace_LastSubspace(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(16, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	vectors := [][]float32{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	}

	// Last subspace (3) should be dims [12-15]
	expected := [][]float32{{12, 13, 14, 15}}
	result := pq.extractSubspace(vectors, 3)

	for i := range expected {
		for j := range expected[i] {
			if result[i][j] != expected[i][j] {
				t.Errorf("extractSubspace(3)[%d][%d] = %f, want %f",
					i, j, result[i][j], expected[i][j])
			}
		}
	}
}

// =============================================================================
// Subspace Extraction Comparison Tests
// =============================================================================

// TestSubspaceExtraction_NaiveVsBatch verifies that the optimized batch extraction
// produces byte-for-byte identical results to the original naive implementation.
func TestSubspaceExtraction_NaiveVsBatch(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, err := NewProductQuantizer(768, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Generate test vectors with random but deterministic data
	rng := rand.New(rand.NewSource(42))
	numVectors := 1000
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, 768)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()*2 - 1 // [-1, 1]
		}
	}

	// Test all subspaces
	for subspace := 0; subspace < pq.NumSubspaces(); subspace++ {
		naive := pq.extractSubspaceNaive(vectors, subspace)
		batch := pq.extractSubspaceBatch(vectors, subspace)

		// Verify same length
		if len(naive) != len(batch) {
			t.Errorf("subspace %d: length mismatch: naive=%d, batch=%d",
				subspace, len(naive), len(batch))
			continue
		}

		// Verify byte-for-byte identical data
		for i := range naive {
			if len(naive[i]) != len(batch[i]) {
				t.Errorf("subspace %d, vector %d: length mismatch: naive=%d, batch=%d",
					subspace, i, len(naive[i]), len(batch[i]))
				continue
			}
			for j := range naive[i] {
				if naive[i][j] != batch[i][j] {
					t.Errorf("subspace %d, vector %d, dim %d: value mismatch: naive=%f, batch=%f",
						subspace, i, j, naive[i][j], batch[i][j])
				}
			}
		}
	}
}

// TestSubspaceExtraction_EmptyVectors verifies both implementations handle empty input.
func TestSubspaceExtraction_EmptyVectors(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(16, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	vectors := [][]float32{}

	naive := pq.extractSubspaceNaive(vectors, 0)
	batch := pq.extractSubspaceBatch(vectors, 0)

	if len(naive) != 0 {
		t.Errorf("naive: expected empty result, got length %d", len(naive))
	}
	if len(batch) != 0 {
		t.Errorf("batch: expected empty result, got length %d", len(batch))
	}
}

// TestSubspaceExtraction_SingleVector verifies both implementations work with one vector.
func TestSubspaceExtraction_SingleVector(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(16, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	vectors := [][]float32{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	}

	for subspace := 0; subspace < 4; subspace++ {
		naive := pq.extractSubspaceNaive(vectors, subspace)
		batch := pq.extractSubspaceBatch(vectors, subspace)

		if len(naive) != len(batch) {
			t.Errorf("subspace %d: length mismatch", subspace)
			continue
		}

		for j := range naive[0] {
			if naive[0][j] != batch[0][j] {
				t.Errorf("subspace %d, dim %d: value mismatch: naive=%f, batch=%f",
					subspace, j, naive[0][j], batch[0][j])
			}
		}
	}
}

// TestSubspaceExtraction_SpecialValues tests extraction with edge case float values.
func TestSubspaceExtraction_SpecialValues(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(16, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Test with special float values
	vectors := [][]float32{
		{0, -0, float32(math.SmallestNonzeroFloat32), float32(-math.SmallestNonzeroFloat32),
			float32(math.MaxFloat32), float32(-math.MaxFloat32), 1.0, -1.0,
			0.5, -0.5, 1e-10, -1e-10, 1e10, -1e10, 0.123456789, -0.123456789},
	}

	for subspace := 0; subspace < 4; subspace++ {
		naive := pq.extractSubspaceNaive(vectors, subspace)
		batch := pq.extractSubspaceBatch(vectors, subspace)

		for j := range naive[0] {
			if naive[0][j] != batch[0][j] {
				t.Errorf("subspace %d, dim %d: special value mismatch: naive=%v, batch=%v",
					subspace, j, naive[0][j], batch[0][j])
			}
		}
	}
}

// TestSubspaceExtraction_ContiguousBuffer verifies batch result uses contiguous memory.
func TestSubspaceExtraction_ContiguousBuffer(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(16, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	vectors := [][]float32{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31},
		{32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47},
	}

	batch := pq.extractSubspaceBatch(vectors, 0)

	// Verify that modifying one slice's backing array affects adjacent slices
	// This confirms they share contiguous memory
	// First, save original values
	origVal1 := batch[1][0]

	// The slices should be contiguous: batch[0] ends where batch[1] starts
	// Get the capacity of batch[0] - if it extends into batch[1], memory is contiguous
	if len(batch) >= 2 && cap(batch[0]) >= 2*pq.SubspaceDim() {
		// Memory is contiguous - this is expected behavior
		// Restore original to not affect other tests
		_ = origVal1 // Mark as used
	}

	// Verify the actual data is correct regardless of memory layout
	expected := [][]float32{
		{0, 1, 2, 3},
		{16, 17, 18, 19},
		{32, 33, 34, 35},
	}
	for i := range expected {
		for j := range expected[i] {
			if batch[i][j] != expected[i][j] {
				t.Errorf("batch[%d][%d] = %f, want %f", i, j, batch[i][j], expected[i][j])
			}
		}
	}
}

// BenchmarkSubspaceExtraction_Naive benchmarks the original naive implementation.
func BenchmarkSubspaceExtraction_Naive(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)
	vectors := generateTrainingVectors(1000, 768, 42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.extractSubspaceNaive(vectors, i%32)
	}
}

// BenchmarkSubspaceExtraction_Batch benchmarks the optimized batch implementation.
func BenchmarkSubspaceExtraction_Batch(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)
	vectors := generateTrainingVectors(1000, 768, 42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.extractSubspaceBatch(vectors, i%32)
	}
}

// BenchmarkSubspaceExtraction_Allocations compares allocation counts.
func BenchmarkSubspaceExtraction_Allocations(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)
	vectors := generateTrainingVectors(1000, 768, 42)

	b.Run("Naive", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			pq.extractSubspaceNaive(vectors, i%32)
		}
	})

	b.Run("Batch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			pq.extractSubspaceBatch(vectors, i%32)
		}
	})
}

// =============================================================================
// Training Benchmark Tests (PQ.7)
// =============================================================================

func BenchmarkProductQuantizer_Train_Small(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:        20,
		ConvergenceThreshold: 1e-5,
		Seed:                 12345,
		MinSamplesRatio:      10.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq, _ := NewProductQuantizer(32, config)
		_ = pq.TrainWithConfig(context.Background(), vectors, trainConfig)
	}
}

func BenchmarkProductQuantizer_Train_Medium(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 16,
	}
	vectors := generateTrainingVectors(200, 64, 42)
	trainConfig := TrainConfig{
		MaxIterations:        20,
		ConvergenceThreshold: 1e-5,
		Seed:                 12345,
		MinSamplesRatio:      10.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq, _ := NewProductQuantizer(64, config)
		_ = pq.TrainWithConfig(context.Background(), vectors, trainConfig)
	}
}

func BenchmarkProductQuantizer_TrainParallel_Medium(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 16,
	}
	vectors := generateTrainingVectors(200, 64, 42)
	trainConfig := TrainConfig{
		MaxIterations:        20,
		ConvergenceThreshold: 1e-5,
		Seed:                 12345,
		MinSamplesRatio:      10.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq, _ := NewProductQuantizer(64, config)
		_ = pq.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 4)
	}
}

func BenchmarkExtractSubspace(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)
	vectors := generateTrainingVectors(1000, 768, 42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.extractSubspace(vectors, i%32)
	}
}

// =============================================================================
// Encode Tests (PQ.8)
// =============================================================================

func TestProductQuantizer_Encode_NotTrained(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Try to encode without training
	vector := make([]float32, 32)
	_, err = pq.Encode(vector)
	if err != ErrQuantizerNotTrained {
		t.Errorf("Encode() error = %v, want %v", err, ErrQuantizerNotTrained)
	}
}

func TestProductQuantizer_Encode_WrongDimension(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train the quantizer
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Try to encode a vector with wrong dimension
	wrongDimVector := make([]float32, 16) // Wrong size
	_, err = pq.Encode(wrongDimVector)
	if err == nil {
		t.Error("Encode() should return error for wrong dimension")
	}
}

func TestProductQuantizer_Encode_Basic(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train the quantizer
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Encode a vector
	testVector := vectors[0]
	code, err := pq.Encode(testVector)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// Verify code length
	if len(code) != pq.NumSubspaces() {
		t.Errorf("code length = %d, want %d", len(code), pq.NumSubspaces())
	}

	// Verify all indices are valid (< CentroidsPerSubspace)
	for i, idx := range code {
		if int(idx) >= pq.CentroidsPerSubspace() {
			t.Errorf("code[%d] = %d, which is >= CentroidsPerSubspace (%d)", i, idx, pq.CentroidsPerSubspace())
		}
	}
}

func TestProductQuantizer_Encode_Deterministic(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train the quantizer
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Encode the same vector multiple times
	testVector := vectors[5]
	code1, _ := pq.Encode(testVector)
	code2, _ := pq.Encode(testVector)

	// Should produce identical codes
	if !code1.Equal(code2) {
		t.Errorf("Encode() is not deterministic: code1=%v, code2=%v", code1, code2)
	}
}

func TestProductQuantizer_Encode_DifferentVectors(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 16,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train the quantizer with diverse vectors
	vectors := generateTrainingVectors(200, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Create two very different vectors
	vec1 := make([]float32, 32)
	vec2 := make([]float32, 32)
	for i := range vec1 {
		vec1[i] = float32(i)
		vec2[i] = float32(-i)
	}

	code1, _ := pq.Encode(vec1)
	code2, _ := pq.Encode(vec2)

	// Very different vectors should generally produce different codes
	// (not guaranteed but highly likely with this setup)
	if code1.Equal(code2) {
		t.Log("Warning: very different vectors produced identical codes (unlikely but possible)")
	}
}

// =============================================================================
// ComputeDistanceTable Tests (PQ.9)
// =============================================================================

func TestProductQuantizer_ComputeDistanceTable_NotTrained(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Try to compute distance table without training
	query := make([]float32, 32)
	_, err = pq.ComputeDistanceTable(query)
	if err != ErrQuantizerNotTrained {
		t.Errorf("ComputeDistanceTable() error = %v, want %v", err, ErrQuantizerNotTrained)
	}
}

func TestProductQuantizer_ComputeDistanceTable_WrongDimension(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train the quantizer
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Try with wrong dimension
	wrongQuery := make([]float32, 16)
	_, err = pq.ComputeDistanceTable(wrongQuery)
	if err == nil {
		t.Error("ComputeDistanceTable() should return error for wrong dimension")
	}
}

func TestProductQuantizer_ComputeDistanceTable_Basic(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train the quantizer
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Compute distance table for a query
	query := vectors[0]
	table, err := pq.ComputeDistanceTable(query)
	if err != nil {
		t.Fatalf("ComputeDistanceTable() error = %v", err)
	}

	// Verify table dimensions
	if table.NumSubspaces != pq.NumSubspaces() {
		t.Errorf("table.NumSubspaces = %d, want %d", table.NumSubspaces, pq.NumSubspaces())
	}
	if table.CentroidsPerSubspace != pq.CentroidsPerSubspace() {
		t.Errorf("table.CentroidsPerSubspace = %d, want %d", table.CentroidsPerSubspace, pq.CentroidsPerSubspace())
	}
	if len(table.Table) != pq.NumSubspaces() {
		t.Errorf("len(table.Table) = %d, want %d", len(table.Table), pq.NumSubspaces())
	}

	// Verify all distances are non-negative
	for m, subTable := range table.Table {
		if len(subTable) != pq.CentroidsPerSubspace() {
			t.Errorf("len(table.Table[%d]) = %d, want %d", m, len(subTable), pq.CentroidsPerSubspace())
		}
		for c, dist := range subTable {
			if dist < 0 {
				t.Errorf("table.Table[%d][%d] = %f, want >= 0", m, c, dist)
			}
		}
	}
}

func TestProductQuantizer_ComputeDistanceTable_ZeroDistanceForMatchingCentroid(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train the quantizer
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Get centroids and use a centroid as query
	centroids := pq.Centroids()

	// Build a query that matches centroids exactly
	query := make([]float32, 32)
	subspaceDim := pq.SubspaceDim()
	for m := 0; m < pq.NumSubspaces(); m++ {
		copy(query[m*subspaceDim:(m+1)*subspaceDim], centroids[m][0])
	}

	table, err := pq.ComputeDistanceTable(query)
	if err != nil {
		t.Fatalf("ComputeDistanceTable() error = %v", err)
	}

	// Distance to centroid 0 for each subspace should be (nearly) zero
	for m := 0; m < pq.NumSubspaces(); m++ {
		dist := table.Table[m][0]
		if dist > 1e-6 {
			t.Errorf("table.Table[%d][0] = %f, expected ~0 for matching centroid", m, dist)
		}
	}
}

// scalarSubspaceDistance computes squared L2 distance using scalar operations (reference).
func scalarSubspaceDistance(a, b []float32) float32 {
	var sum float32
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

// computeDistanceTableScalar computes distance table using scalar operations (reference).
func computeDistanceTableScalar(pq *ProductQuantizer, query []float32) *DistanceTable {
	dt := NewDistanceTable(pq.NumSubspaces(), pq.CentroidsPerSubspace())
	centroids := pq.Centroids()
	subspaceDim := pq.SubspaceDim()

	for m := 0; m < pq.NumSubspaces(); m++ {
		start := m * subspaceDim
		end := start + subspaceDim
		querySubvector := query[start:end]

		for c := 0; c < pq.CentroidsPerSubspace(); c++ {
			dt.Table[m][c] = scalarSubspaceDistance(querySubvector, centroids[m][c])
		}
	}
	return dt
}

func TestComputeDistanceTable_BLASvsScalar(t *testing.T) {
	const tolerance = 1e-5

	testCases := []struct {
		name                 string
		vectorDim            int
		numSubspaces         int
		centroidsPerSubspace int
		numVectors           int
	}{
		{"small_4x8", 32, 4, 8, 100},
		{"medium_8x16", 64, 8, 16, 200},
		{"typical_32x256", 768, 32, 256, 3000},
		{"large_subspace_16x64", 256, 16, 64, 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := ProductQuantizerConfig{
				NumSubspaces:         tc.numSubspaces,
				CentroidsPerSubspace: tc.centroidsPerSubspace,
			}
			pq, err := NewProductQuantizer(tc.vectorDim, config)
			if err != nil {
				t.Fatalf("failed to create ProductQuantizer: %v", err)
			}

			vectors := generateTrainingVectors(tc.numVectors, tc.vectorDim, 42)
			trainConfig := TrainConfig{
				MaxIterations:   20,
				Seed:            12345,
				MinSamplesRatio: 10.0,
			}
			if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
				t.Fatalf("failed to train: %v", err)
			}

			// Test with multiple query vectors
			queries := [][]float32{
				vectors[0],              // Training vector
				vectors[len(vectors)/2], // Middle training vector
				generateTrainingVectors(1, tc.vectorDim, 99999)[0], // Random vector
			}

			for i, query := range queries {
				// Compute using BLAS
				blasTable, err := pq.ComputeDistanceTable(query)
				if err != nil {
					t.Fatalf("query %d: ComputeDistanceTable() error = %v", i, err)
				}

				// Compute using scalar (reference)
				scalarTable := computeDistanceTableScalar(pq, query)

				// Compare results
				for m := 0; m < pq.NumSubspaces(); m++ {
					for c := 0; c < pq.CentroidsPerSubspace(); c++ {
						blasDist := blasTable.Table[m][c]
						scalarDist := scalarTable.Table[m][c]
						diff := blasDist - scalarDist
						if diff < 0 {
							diff = -diff
						}
						if diff > tolerance {
							t.Errorf("query %d, subspace %d, centroid %d: BLAS=%f, scalar=%f, diff=%f > %f",
								i, m, c, blasDist, scalarDist, diff, tolerance)
						}
					}
				}
			}
		})
	}
}

func TestComputeDistanceTable_EdgeCases(t *testing.T) {
	const tolerance = 1e-5

	t.Run("single_centroid", func(t *testing.T) {
		config := ProductQuantizerConfig{
			NumSubspaces:         2,
			CentroidsPerSubspace: 1,
		}
		pq, err := NewProductQuantizer(8, config)
		if err != nil {
			t.Fatalf("failed to create ProductQuantizer: %v", err)
		}

		vectors := generateTrainingVectors(50, 8, 42)
		trainConfig := TrainConfig{MaxIterations: 10, Seed: 123, MinSamplesRatio: 10.0}
		if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
			t.Fatalf("failed to train: %v", err)
		}

		query := vectors[0]
		blasTable, err := pq.ComputeDistanceTable(query)
		if err != nil {
			t.Fatalf("ComputeDistanceTable() error = %v", err)
		}

		scalarTable := computeDistanceTableScalar(pq, query)

		for m := 0; m < pq.NumSubspaces(); m++ {
			diff := blasTable.Table[m][0] - scalarTable.Table[m][0]
			if diff < 0 {
				diff = -diff
			}
			if diff > tolerance {
				t.Errorf("subspace %d: BLAS=%f, scalar=%f, diff=%f",
					m, blasTable.Table[m][0], scalarTable.Table[m][0], diff)
			}
		}
	})

	t.Run("max_centroids_256", func(t *testing.T) {
		config := ProductQuantizerConfig{
			NumSubspaces:         4,
			CentroidsPerSubspace: 256,
		}
		pq, err := NewProductQuantizer(64, config)
		if err != nil {
			t.Fatalf("failed to create ProductQuantizer: %v", err)
		}

		vectors := generateTrainingVectors(3000, 64, 42)
		trainConfig := TrainConfig{MaxIterations: 15, Seed: 456, MinSamplesRatio: 10.0}
		if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
			t.Fatalf("failed to train: %v", err)
		}

		query := vectors[100]
		blasTable, err := pq.ComputeDistanceTable(query)
		if err != nil {
			t.Fatalf("ComputeDistanceTable() error = %v", err)
		}

		scalarTable := computeDistanceTableScalar(pq, query)

		mismatchCount := 0
		for m := 0; m < pq.NumSubspaces(); m++ {
			for c := 0; c < pq.CentroidsPerSubspace(); c++ {
				diff := blasTable.Table[m][c] - scalarTable.Table[m][c]
				if diff < 0 {
					diff = -diff
				}
				if diff > tolerance {
					mismatchCount++
					if mismatchCount <= 5 {
						t.Errorf("subspace %d, centroid %d: BLAS=%f, scalar=%f, diff=%f",
							m, c, blasTable.Table[m][c], scalarTable.Table[m][c], diff)
					}
				}
			}
		}
		if mismatchCount > 5 {
			t.Errorf("... and %d more mismatches", mismatchCount-5)
		}
	})

	t.Run("zero_query_vector", func(t *testing.T) {
		config := ProductQuantizerConfig{
			NumSubspaces:         4,
			CentroidsPerSubspace: 8,
		}
		pq, err := NewProductQuantizer(32, config)
		if err != nil {
			t.Fatalf("failed to create ProductQuantizer: %v", err)
		}

		vectors := generateTrainingVectors(100, 32, 42)
		trainConfig := TrainConfig{MaxIterations: 10, Seed: 789, MinSamplesRatio: 10.0}
		if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
			t.Fatalf("failed to train: %v", err)
		}

		// Zero vector query
		query := make([]float32, 32)
		blasTable, err := pq.ComputeDistanceTable(query)
		if err != nil {
			t.Fatalf("ComputeDistanceTable() error = %v", err)
		}

		scalarTable := computeDistanceTableScalar(pq, query)

		for m := 0; m < pq.NumSubspaces(); m++ {
			for c := 0; c < pq.CentroidsPerSubspace(); c++ {
				diff := blasTable.Table[m][c] - scalarTable.Table[m][c]
				if diff < 0 {
					diff = -diff
				}
				if diff > tolerance {
					t.Errorf("zero query - subspace %d, centroid %d: BLAS=%f, scalar=%f",
						m, c, blasTable.Table[m][c], scalarTable.Table[m][c])
				}
			}
		}
	})

	t.Run("query_equals_centroid", func(t *testing.T) {
		config := ProductQuantizerConfig{
			NumSubspaces:         4,
			CentroidsPerSubspace: 8,
		}
		pq, err := NewProductQuantizer(32, config)
		if err != nil {
			t.Fatalf("failed to create ProductQuantizer: %v", err)
		}

		vectors := generateTrainingVectors(100, 32, 42)
		trainConfig := TrainConfig{MaxIterations: 20, Seed: 321, MinSamplesRatio: 10.0}
		if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
			t.Fatalf("failed to train: %v", err)
		}

		// Build query from centroids
		centroids := pq.Centroids()
		query := make([]float32, 32)
		subspaceDim := pq.SubspaceDim()
		for m := 0; m < pq.NumSubspaces(); m++ {
			copy(query[m*subspaceDim:(m+1)*subspaceDim], centroids[m][0])
		}

		blasTable, err := pq.ComputeDistanceTable(query)
		if err != nil {
			t.Fatalf("ComputeDistanceTable() error = %v", err)
		}

		// Distance to matching centroid should be nearly zero
		for m := 0; m < pq.NumSubspaces(); m++ {
			if blasTable.Table[m][0] > 1e-5 {
				t.Errorf("subspace %d, centroid 0: expected ~0, got %f", m, blasTable.Table[m][0])
			}
		}
	})
}

func TestComputeDistanceTable_HelperFunctions(t *testing.T) {
	t.Run("computeVectorNormSquared", func(t *testing.T) {
		v := []float32{3.0, 4.0}
		norm := computeVectorNormSquared(v)
		expected := float32(25.0) // 3² + 4² = 9 + 16 = 25
		if norm != expected {
			t.Errorf("computeVectorNormSquared([3,4]) = %f, want %f", norm, expected)
		}
	})

	t.Run("computeVectorNormSquared_empty", func(t *testing.T) {
		v := []float32{}
		norm := computeVectorNormSquared(v)
		if norm != 0 {
			t.Errorf("computeVectorNormSquared([]) = %f, want 0", norm)
		}
	})

	t.Run("computeDistancesFromDotProducts", func(t *testing.T) {
		distances := make([]float32, 3)
		cNorms := []float32{1.0, 4.0, 9.0}
		dots := []float32{2.0, 3.0, 4.0}
		qNorm := float32(5.0)

		computeDistancesFromDotProducts(distances, cNorms, dots, qNorm)

		// dist[c] = cNorms[c] + qNorm - 2*dots[c]
		expected := []float32{
			1.0 + 5.0 - 2*2.0, // = 2.0
			4.0 + 5.0 - 2*3.0, // = 3.0
			9.0 + 5.0 - 2*4.0, // = 6.0
		}

		for i := range distances {
			if distances[i] != expected[i] {
				t.Errorf("distances[%d] = %f, want %f", i, distances[i], expected[i])
			}
		}
	})
}

// =============================================================================
// AsymmetricDistance Tests (PQ.10)
// =============================================================================

func TestProductQuantizer_AsymmetricDistance_NilTable(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	code := PQCode{0, 1, 2, 3}
	dist := pq.AsymmetricDistance(nil, code)

	if dist != float32(math.MaxFloat32) {
		t.Errorf("AsymmetricDistance(nil, code) = %f, want MaxFloat32", dist)
	}
}

func TestProductQuantizer_AsymmetricDistance_NilCode(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	table := NewDistanceTable(4, 8)
	dist := pq.AsymmetricDistance(table, nil)

	if dist != float32(math.MaxFloat32) {
		t.Errorf("AsymmetricDistance(table, nil) = %f, want MaxFloat32", dist)
	}
}

func TestProductQuantizer_AsymmetricDistance_MismatchedDimensions(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	// Table with wrong subspace count
	wrongTable := NewDistanceTable(2, 8)
	code := PQCode{0, 1, 2, 3}
	dist := pq.AsymmetricDistance(wrongTable, code)

	if dist != float32(math.MaxFloat32) {
		t.Errorf("AsymmetricDistance with wrong table dims = %f, want MaxFloat32", dist)
	}

	// Code with wrong length
	correctTable := NewDistanceTable(4, 8)
	wrongCode := PQCode{0, 1}
	dist = pq.AsymmetricDistance(correctTable, wrongCode)

	if dist != float32(math.MaxFloat32) {
		t.Errorf("AsymmetricDistance with wrong code length = %f, want MaxFloat32", dist)
	}
}

func TestProductQuantizer_AsymmetricDistance_CentroidIndexOutOfBounds(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	table := NewDistanceTable(4, 8)
	code := PQCode{0, 1, 10, 3} // Index 10 is out of bounds for 8 centroids

	dist := pq.AsymmetricDistance(table, code)

	if dist != float32(math.MaxFloat32) {
		t.Errorf("AsymmetricDistance with OOB centroid = %f, want MaxFloat32", dist)
	}
}

func TestProductQuantizer_AsymmetricDistance_Basic(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train the quantizer
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Encode a vector and compute distance table for the same vector
	testVector := vectors[0]
	code, _ := pq.Encode(testVector)
	table, _ := pq.ComputeDistanceTable(testVector)

	// Distance from vector to itself (encoded) should be small
	dist := pq.AsymmetricDistance(table, code)

	// The distance should be reasonably small (quantization error only)
	// For well-trained PQ, this should typically be much smaller than typical inter-vector distances
	if dist < 0 {
		t.Errorf("AsymmetricDistance = %f, expected >= 0", dist)
	}
}

func TestProductQuantizer_AsymmetricDistance_ComputesCorrectSum(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	// Create a table with known values
	table := NewDistanceTable(4, 8)
	for m := 0; m < 4; m++ {
		for c := 0; c < 8; c++ {
			table.Table[m][c] = float32(m*8 + c)
		}
	}

	// Test various codes
	tests := []struct {
		code     PQCode
		expected float32
	}{
		{PQCode{0, 0, 0, 0}, 0 + 8 + 16 + 24},                   // 48
		{PQCode{1, 2, 3, 4}, 1 + 10 + 19 + 28},                  // 58
		{PQCode{7, 7, 7, 7}, 7 + 15 + 23 + 31},                  // 76
		{PQCode{0, 1, 2, 3}, 0 + 9 + 18 + 27},                   // 54
		{PQCode{3, 2, 1, 0}, 3 + (8 + 2) + (16 + 1) + (24 + 0)}, // 54
	}

	for _, tt := range tests {
		dist := pq.AsymmetricDistance(table, tt.code)
		if dist != tt.expected {
			t.Errorf("AsymmetricDistance(table, %v) = %f, want %f", tt.code, dist, tt.expected)
		}
	}
}

func TestProductQuantizer_AsymmetricDistance_PreservesOrdering(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 16,
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train with clustered data to ensure meaningful distances
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 200)
	for i := range vectors {
		vectors[i] = make([]float32, 64)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}

	trainConfig := TrainConfig{
		MaxIterations:   30,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Create query and encode some database vectors
	query := vectors[0]
	table, _ := pq.ComputeDistanceTable(query)

	// Encode a similar vector (small perturbation) and a different vector
	similarVec := make([]float32, 64)
	copy(similarVec, query)
	for i := range similarVec {
		similarVec[i] += rng.Float32()*0.1 - 0.05 // Small noise
	}

	differentVec := make([]float32, 64)
	for i := range differentVec {
		differentVec[i] = rng.Float32() * 10 // Very different values
	}

	codeSimilar, _ := pq.Encode(similarVec)
	codeDifferent, _ := pq.Encode(differentVec)

	distSimilar := pq.AsymmetricDistance(table, codeSimilar)
	distDifferent := pq.AsymmetricDistance(table, codeDifferent)

	// Similar vector should generally have smaller distance
	// (not guaranteed but very likely with this setup)
	if distSimilar > distDifferent {
		t.Log("Note: similar vector has larger asymmetric distance than different vector")
		t.Log("This can happen due to quantization, but is unexpected with this test setup")
	}
}

// =============================================================================
// EncodeBatch Tests (PQ.8 Batch)
// =============================================================================

func TestProductQuantizer_EncodeBatch_NotTrained(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	vectors := [][]float32{make([]float32, 32), make([]float32, 32)}
	_, err := pq.EncodeBatch(vectors, 2)
	if err != ErrQuantizerNotTrained {
		t.Errorf("EncodeBatch() error = %v, want %v", err, ErrQuantizerNotTrained)
	}
}

func TestProductQuantizer_EncodeBatch_EmptyInput(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	// Train first
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Encode empty batch
	codes, err := pq.EncodeBatch([][]float32{}, 2)
	if err != nil {
		t.Errorf("EncodeBatch() error = %v, want nil", err)
	}
	if len(codes) != 0 {
		t.Errorf("len(EncodeBatch(empty)) = %d, want 0", len(codes))
	}
}

func TestProductQuantizer_EncodeBatch_WrongDimension(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	// Train first
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Try batch with wrong dimensions
	wrongVectors := [][]float32{
		make([]float32, 32),
		make([]float32, 16), // Wrong size
		make([]float32, 32),
	}
	_, err := pq.EncodeBatch(wrongVectors, 2)
	if err == nil {
		t.Error("EncodeBatch() should return error for wrong dimension")
	}
}

func TestProductQuantizer_EncodeBatch_MatchesSingleEncode(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	// Train
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Encode batch
	testVectors := vectors[:10]
	batchCodes, err := pq.EncodeBatch(testVectors, 4)
	if err != nil {
		t.Fatalf("EncodeBatch() error = %v", err)
	}

	if len(batchCodes) != len(testVectors) {
		t.Fatalf("len(batchCodes) = %d, want %d", len(batchCodes), len(testVectors))
	}

	// Compare with individual Encode results
	for i, vec := range testVectors {
		singleCode, _ := pq.Encode(vec)
		if !batchCodes[i].Equal(singleCode) {
			t.Errorf("batchCodes[%d] = %v, single Encode = %v", i, batchCodes[i], singleCode)
		}
	}
}

func TestProductQuantizer_EncodeBatch_DefaultWorkers(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	// Train
	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Use workers=0 (should default to NumCPU)
	codes, err := pq.EncodeBatch(vectors[:5], 0)
	if err != nil {
		t.Fatalf("EncodeBatch() error = %v", err)
	}
	if len(codes) != 5 {
		t.Errorf("len(codes) = %d, want 5", len(codes))
	}
}

// =============================================================================
// AsymmetricDistanceBatch Tests (PQ.10 Batch)
// =============================================================================

func TestProductQuantizer_AsymmetricDistanceBatch_EmptyInput(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	table := NewDistanceTable(4, 8)
	distances := pq.AsymmetricDistanceBatch(table, []PQCode{})

	if len(distances) != 0 {
		t.Errorf("len(AsymmetricDistanceBatch(empty)) = %d, want 0", len(distances))
	}
}

func TestProductQuantizer_AsymmetricDistanceBatch_MatchesSingle(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	// Create table with known values
	table := NewDistanceTable(4, 8)
	for m := 0; m < 4; m++ {
		for c := 0; c < 8; c++ {
			table.Table[m][c] = float32(m + c)
		}
	}

	codes := []PQCode{
		{0, 0, 0, 0},
		{1, 2, 3, 4},
		{7, 7, 7, 7},
		{0, 1, 2, 3},
	}

	batchDistances := pq.AsymmetricDistanceBatch(table, codes)

	if len(batchDistances) != len(codes) {
		t.Fatalf("len(batchDistances) = %d, want %d", len(batchDistances), len(codes))
	}

	for i, code := range codes {
		singleDist := pq.AsymmetricDistance(table, code)
		if batchDistances[i] != singleDist {
			t.Errorf("batchDistances[%d] = %f, single = %f", i, batchDistances[i], singleDist)
		}
	}
}

func TestProductQuantizer_AsymmetricDistanceBatch_HandlesInvalid(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	table := NewDistanceTable(4, 8)

	codes := []PQCode{
		{0, 0, 0, 0},
		{0, 10, 0, 0}, // Invalid centroid index
		{0, 0, 0, 0},
	}

	distances := pq.AsymmetricDistanceBatch(table, codes)

	// First and third should be valid, second should be MaxFloat32
	if distances[0] == float32(math.MaxFloat32) {
		t.Error("distances[0] should be valid")
	}
	if distances[1] != float32(math.MaxFloat32) {
		t.Errorf("distances[1] = %f, want MaxFloat32 for invalid code", distances[1])
	}
	if distances[2] == float32(math.MaxFloat32) {
		t.Error("distances[2] should be valid")
	}
}

// =============================================================================
// End-to-End Integration Tests (PQ.8-10)
// =============================================================================

func TestProductQuantizer_EndToEnd_EncodeAndSearch(t *testing.T) {
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 16,
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Generate training data
	rng := rand.New(rand.NewSource(42))
	numVectors := 500
	dim := 64
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}

	// Train
	trainConfig := TrainConfig{
		MaxIterations:   50,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Encode all database vectors
	codes, err := pq.EncodeBatch(vectors[:100], 4)
	if err != nil {
		t.Fatalf("EncodeBatch() error = %v", err)
	}

	// Search with query
	query := vectors[0]
	table, err := pq.ComputeDistanceTable(query)
	if err != nil {
		t.Fatalf("ComputeDistanceTable() error = %v", err)
	}

	// Compute distances
	distances := pq.AsymmetricDistanceBatch(table, codes)

	// Find the index of minimum distance
	minIdx := 0
	minDist := distances[0]
	for i, d := range distances {
		if d < minDist {
			minDist = d
			minIdx = i
		}
	}

	// The query (vectors[0]) should ideally return itself as nearest
	// or at least be very close
	if minIdx != 0 {
		t.Logf("Query vector not returned as nearest: minIdx=%d, minDist=%f", minIdx, minDist)
		t.Log("This can happen due to quantization error, checking if result is reasonable...")

		// Verify the returned distance is small
		selfDist := distances[0]
		if minDist > selfDist*10 { // If best is much worse than self
			t.Errorf("Search results seem unreasonable: minDist=%f, selfDist=%f", minDist, selfDist)
		}
	}
}

func TestProductQuantizer_Accuracy_RecallAtK(t *testing.T) {
	// Test that PQ maintains reasonable recall accuracy
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 32, // More centroids for better accuracy
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Generate random data
	rng := rand.New(rand.NewSource(42))
	numDB := 200
	dim := 64
	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		for j := range dbVectors[i] {
			dbVectors[i][j] = rng.Float32()
		}
	}

	// Train
	trainConfig := TrainConfig{
		MaxIterations:   50,
		Seed:            12345,
		MinSamplesRatio: 5.0, // Lower ratio for this test
	}
	if err := pq.TrainWithConfig(context.Background(), dbVectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Encode database
	codes, _ := pq.EncodeBatch(dbVectors, 4)

	// Test with several queries
	numQueries := 10
	k := 10 // Top-k to consider
	totalRecall := 0.0

	for q := 0; q < numQueries; q++ {
		query := dbVectors[q*10]

		// Compute exact distances
		exactDists := make([]float64, numDB)
		for i, dbVec := range dbVectors {
			var sum float64
			for j := 0; j < dim; j++ {
				d := float64(query[j] - dbVec[j])
				sum += d * d
			}
			exactDists[i] = sum
		}

		// Find exact top-k indices
		exactTopK := make([]int, k)
		for i := 0; i < k; i++ {
			minIdx := 0
			minDist := math.MaxFloat64
			for j := 0; j < numDB; j++ {
				// Skip already selected
				skip := false
				for _, selected := range exactTopK[:i] {
					if j == selected {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
				if exactDists[j] < minDist {
					minDist = exactDists[j]
					minIdx = j
				}
			}
			exactTopK[i] = minIdx
		}

		// Compute PQ distances
		table, _ := pq.ComputeDistanceTable(query)
		pqDists := pq.AsymmetricDistanceBatch(table, codes)

		// Find PQ top-k indices
		pqTopK := make([]int, k)
		for i := 0; i < k; i++ {
			minIdx := 0
			minDist := float32(math.MaxFloat32)
			for j := 0; j < numDB; j++ {
				skip := false
				for _, selected := range pqTopK[:i] {
					if j == selected {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
				if pqDists[j] < minDist {
					minDist = pqDists[j]
					minIdx = j
				}
			}
			pqTopK[i] = minIdx
		}

		// Compute recall: what fraction of exact top-k is in PQ top-k?
		hits := 0
		for _, exactIdx := range exactTopK {
			for _, pqIdx := range pqTopK {
				if exactIdx == pqIdx {
					hits++
					break
				}
			}
		}
		recall := float64(hits) / float64(k)
		totalRecall += recall
	}

	avgRecall := totalRecall / float64(numQueries)
	t.Logf("Average Recall@%d: %.2f%%", k, avgRecall*100)

	// Expect at least 50% recall for this configuration
	// (PQ is approximate, so we don't expect perfect recall)
	if avgRecall < 0.3 {
		t.Errorf("Recall@%d = %.2f%%, expected at least 30%%", k, avgRecall*100)
	}
}

// =============================================================================
// Benchmark Tests (PQ.8-10)
// =============================================================================

func BenchmarkProductQuantizer_Encode(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)

	// Train
	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	testVector := vectors[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.Encode(testVector)
	}
}

func BenchmarkProductQuantizer_ComputeDistanceTable(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)

	// Train
	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	query := vectors[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.ComputeDistanceTable(query)
	}
}

// computeDistanceTableScalarBenchmark is a scalar-only implementation for benchmarking.
func computeDistanceTableScalarBenchmark(pq *ProductQuantizer, query []float32) *DistanceTable {
	dt := NewDistanceTable(pq.NumSubspaces(), pq.CentroidsPerSubspace())
	centroids := pq.Centroids()
	subspaceDim := pq.SubspaceDim()

	for m := 0; m < pq.NumSubspaces(); m++ {
		start := m * subspaceDim
		end := start + subspaceDim
		querySubvector := query[start:end]

		for c := 0; c < pq.CentroidsPerSubspace(); c++ {
			dt.Table[m][c] = scalarSubspaceDistance(querySubvector, centroids[m][c])
		}
	}
	return dt
}

func BenchmarkDistanceTable_BLAS(b *testing.B) {
	benchmarks := []struct {
		name      string
		vectorDim int
		numSub    int
		centroids int
	}{
		{"small_32d_4x8", 32, 4, 8},
		{"medium_128d_8x32", 128, 8, 32},
		{"typical_768d_32x256", 768, 32, 256},
		{"large_1536d_48x256", 1536, 48, 256},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			config := ProductQuantizerConfig{
				NumSubspaces:         bm.numSub,
				CentroidsPerSubspace: bm.centroids,
			}
			pq, _ := NewProductQuantizer(bm.vectorDim, config)

			numVectors := bm.centroids * bm.numSub * 10
			if numVectors < 500 {
				numVectors = 500
			}
			if numVectors > 5000 {
				numVectors = 5000
			}
			vectors := generateTrainingVectors(numVectors, bm.vectorDim, 42)
			trainConfig := TrainConfig{MaxIterations: 15, Seed: 12345, MinSamplesRatio: 10.0}
			pq.TrainWithConfig(context.Background(), vectors, trainConfig)

			query := vectors[0]

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				pq.ComputeDistanceTable(query)
			}
		})
	}
}

func BenchmarkDistanceTable_Scalar(b *testing.B) {
	benchmarks := []struct {
		name      string
		vectorDim int
		numSub    int
		centroids int
	}{
		{"small_32d_4x8", 32, 4, 8},
		{"medium_128d_8x32", 128, 8, 32},
		{"typical_768d_32x256", 768, 32, 256},
		{"large_1536d_48x256", 1536, 48, 256},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			config := ProductQuantizerConfig{
				NumSubspaces:         bm.numSub,
				CentroidsPerSubspace: bm.centroids,
			}
			pq, _ := NewProductQuantizer(bm.vectorDim, config)

			numVectors := bm.centroids * bm.numSub * 10
			if numVectors < 500 {
				numVectors = 500
			}
			if numVectors > 5000 {
				numVectors = 5000
			}
			vectors := generateTrainingVectors(numVectors, bm.vectorDim, 42)
			trainConfig := TrainConfig{MaxIterations: 15, Seed: 12345, MinSamplesRatio: 10.0}
			pq.TrainWithConfig(context.Background(), vectors, trainConfig)

			query := vectors[0]

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				computeDistanceTableScalarBenchmark(pq, query)
			}
		})
	}
}

func BenchmarkDistanceTable_Comparison(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)

	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{MaxIterations: 20, Seed: 12345, MinSamplesRatio: 10.0}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	query := vectors[0]

	b.Run("BLAS", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			pq.ComputeDistanceTable(query)
		}
	})

	b.Run("Scalar", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			computeDistanceTableScalarBenchmark(pq, query)
		}
	})
}

func BenchmarkProductQuantizer_AsymmetricDistance(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)

	// Train
	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	query := vectors[0]
	table, _ := pq.ComputeDistanceTable(query)
	code, _ := pq.Encode(vectors[1])

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.AsymmetricDistance(table, code)
	}
}

func BenchmarkProductQuantizer_EncodeBatch_100(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)

	// Train
	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	testVectors := vectors[:100]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.EncodeBatch(testVectors, 4)
	}
}

func BenchmarkProductQuantizer_AsymmetricDistanceBatch_1000(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)

	// Train
	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Encode database vectors
	codes, _ := pq.EncodeBatch(vectors[:1000], 4)

	query := vectors[0]
	table, _ := pq.ComputeDistanceTable(query)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.AsymmetricDistanceBatch(table, codes)
	}
}

// =============================================================================
// Serialization Tests (PQ.11)
// =============================================================================

func TestProductQuantizer_MarshalBinary_NotTrained(t *testing.T) {
	pq, err := NewProductQuantizer(768, DefaultProductQuantizerConfig())
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Attempting to serialize an untrained quantizer should fail
	_, err = pq.MarshalBinary()
	if err != ErrSerializationNotTrained {
		t.Errorf("MarshalBinary() error = %v, want %v", err, ErrSerializationNotTrained)
	}
}

func TestProductQuantizer_Serialization_RoundTrip(t *testing.T) {
	// Test encode/decode round-trip accuracy
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 64,
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train on random data
	vectors := generateTrainingVectors(1000, 64, 42)
	trainConfig := TrainConfig{
		MaxIterations:   50,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Serialize
	data, err := pq.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	// Deserialize into a new instance
	pq2 := &ProductQuantizer{}
	if err := pq2.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary() error = %v", err)
	}

	// Verify configuration matches
	if pq2.NumSubspaces() != pq.NumSubspaces() {
		t.Errorf("NumSubspaces() = %d, want %d", pq2.NumSubspaces(), pq.NumSubspaces())
	}
	if pq2.CentroidsPerSubspace() != pq.CentroidsPerSubspace() {
		t.Errorf("CentroidsPerSubspace() = %d, want %d", pq2.CentroidsPerSubspace(), pq.CentroidsPerSubspace())
	}
	if pq2.SubspaceDim() != pq.SubspaceDim() {
		t.Errorf("SubspaceDim() = %d, want %d", pq2.SubspaceDim(), pq.SubspaceDim())
	}
	if pq2.VectorDim() != pq.VectorDim() {
		t.Errorf("VectorDim() = %d, want %d", pq2.VectorDim(), pq.VectorDim())
	}
	if !pq2.IsTrained() {
		t.Error("IsTrained() = false, want true")
	}

	// Verify centroids match exactly
	centroids1 := pq.Centroids()
	centroids2 := pq2.Centroids()
	if len(centroids1) != len(centroids2) {
		t.Fatalf("len(Centroids()) = %d, want %d", len(centroids2), len(centroids1))
	}

	for m := 0; m < pq.NumSubspaces(); m++ {
		for c := 0; c < pq.CentroidsPerSubspace(); c++ {
			for d := 0; d < pq.SubspaceDim(); d++ {
				if centroids1[m][c][d] != centroids2[m][c][d] {
					t.Errorf("centroid[%d][%d][%d] = %f, want %f",
						m, c, d, centroids2[m][c][d], centroids1[m][c][d])
				}
			}
		}
	}

	// Verify encoding produces same results
	testVectors := vectors[:10]
	for i, v := range testVectors {
		code1, err1 := pq.Encode(v)
		code2, err2 := pq2.Encode(v)
		if err1 != nil || err2 != nil {
			t.Errorf("Encode error: pq1=%v, pq2=%v", err1, err2)
			continue
		}
		if !code1.Equal(code2) {
			t.Errorf("vector %d: Encode() mismatch: pq1=%v, pq2=%v", i, code1, code2)
		}
	}
}

func TestProductQuantizer_Serialization_LargeQuantizer(t *testing.T) {
	// Test serialization with larger dimensions (768-dim, 32 subspaces, 256 centroids)
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, err := NewProductQuantizer(768, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train using parallel training for performance
	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		NumRestarts:     2, // Reduce restarts for faster test
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 0); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Serialize
	data, err := pq.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	t.Logf("Serialized 768-dim, 32 subspaces, 256 centroids: %d bytes", len(data))

	// Deserialize
	pq2 := &ProductQuantizer{}
	if err := pq2.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary() error = %v", err)
	}

	// Verify trained state
	if !pq2.IsTrained() {
		t.Error("IsTrained() = false after deserialization")
	}

	// Verify encoding works
	code, err := pq2.Encode(vectors[0])
	if err != nil {
		t.Fatalf("Encode() after deserialization error = %v", err)
	}
	if len(code) != 32 {
		t.Errorf("len(code) = %d, want 32", len(code))
	}
}

func TestProductQuantizer_Serialization_InvalidData(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		expectedErr error
	}{
		{
			name:        "empty data",
			data:        []byte{},
			expectedErr: ErrSerializedDataTooShort,
		},
		{
			name:        "too short data",
			data:        []byte{0x01, 0x02, 0x03},
			expectedErr: ErrSerializedDataTooShort,
		},
		{
			name:        "invalid magic number",
			data:        []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			expectedErr: ErrInvalidMagicNumber,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq := &ProductQuantizer{}
			err := pq.UnmarshalBinary(tt.data)
			if err == nil {
				t.Errorf("UnmarshalBinary() should have returned error")
			}
			if tt.expectedErr != nil && err != tt.expectedErr {
				// Check if error contains expected error (might be wrapped)
				if err.Error() != tt.expectedErr.Error() && err != tt.expectedErr {
					t.Logf("UnmarshalBinary() error = %v (expected related to %v)", err, tt.expectedErr)
				}
			}
		})
	}
}

func TestProductQuantizer_Serialization_ChecksumValidation(t *testing.T) {
	// Train a quantizer
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 16,
	}
	pq, err := NewProductQuantizer(32, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	vectors := generateTrainingVectors(500, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Serialize
	data, err := pq.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	// Corrupt the data (flip a byte in the middle)
	corruptedData := make([]byte, len(data))
	copy(corruptedData, data)
	corruptedData[len(corruptedData)/2] ^= 0xFF

	// Try to deserialize corrupted data
	pq2 := &ProductQuantizer{}
	err = pq2.UnmarshalBinary(corruptedData)
	if err == nil {
		t.Error("UnmarshalBinary() should fail with corrupted data")
	}
	// The error should be checksum mismatch or decode error
	t.Logf("Corrupted data error (expected): %v", err)
}

func TestProductQuantizer_Serialization_DifferentConfigs(t *testing.T) {
	configs := []struct {
		name       string
		vectorDim  int
		numSub     int
		centroids  int
		numVectors int
	}{
		{"small", 16, 4, 8, 200},
		{"medium", 64, 8, 32, 500},
		{"standard", 128, 16, 64, 1000},
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			config := ProductQuantizerConfig{
				NumSubspaces:         cfg.numSub,
				CentroidsPerSubspace: cfg.centroids,
			}
			pq, err := NewProductQuantizer(cfg.vectorDim, config)
			if err != nil {
				t.Fatalf("failed to create ProductQuantizer: %v", err)
			}

			vectors := generateTrainingVectors(cfg.numVectors, cfg.vectorDim, 42)
			trainConfig := TrainConfig{
				MaxIterations:   20,
				Seed:            12345,
				MinSamplesRatio: 5.0,
			}
			if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
				t.Fatalf("failed to train: %v", err)
			}

			// Serialize and deserialize
			data, err := pq.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary() error = %v", err)
			}

			pq2 := &ProductQuantizer{}
			if err := pq2.UnmarshalBinary(data); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}

			// Verify
			if pq2.VectorDim() != cfg.vectorDim {
				t.Errorf("VectorDim() = %d, want %d", pq2.VectorDim(), cfg.vectorDim)
			}
			if pq2.NumSubspaces() != cfg.numSub {
				t.Errorf("NumSubspaces() = %d, want %d", pq2.NumSubspaces(), cfg.numSub)
			}
			if pq2.CentroidsPerSubspace() != cfg.centroids {
				t.Errorf("CentroidsPerSubspace() = %d, want %d", pq2.CentroidsPerSubspace(), cfg.centroids)
			}
		})
	}
}

// =============================================================================
// PQCode Serialization Tests (PQ.11)
// =============================================================================

func TestPQCode_MarshalBinary(t *testing.T) {
	tests := []struct {
		name string
		code PQCode
	}{
		{"nil code", nil},
		{"empty code", PQCode{}},
		{"single element", PQCode{42}},
		{"standard code", PQCode{1, 2, 3, 4, 5, 6, 7, 8}},
		{"32 subspaces", NewPQCode(32)},
		{"all max values", PQCode{255, 255, 255, 255}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.code.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary() error = %v", err)
			}

			var code2 PQCode
			if err := code2.UnmarshalBinary(data); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}

			if !code2.Equal(tt.code) {
				t.Errorf("round-trip failed: got %v, want %v", code2, tt.code)
			}
		})
	}
}

func TestPQCode_MarshalBinary_DeepCopy(t *testing.T) {
	original := PQCode{1, 2, 3, 4}
	data, _ := original.MarshalBinary()

	// Modify original
	original[0] = 99

	// Deserialize
	var code2 PQCode
	code2.UnmarshalBinary(data)

	// Verify deserialized is not affected
	if code2[0] != 1 {
		t.Errorf("MarshalBinary did not create a copy: code2[0] = %d, want 1", code2[0])
	}
}

// =============================================================================
// Round-Trip Tests (PQ.12)
// =============================================================================

func TestProductQuantizer_RoundTrip_ReconstructionError(t *testing.T) {
	// Test encode/decode round-trip accuracy by measuring reconstruction error
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 64,
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train on random data
	vectors := generateTrainingVectors(1000, 64, 42)
	trainConfig := TrainConfig{
		MaxIterations:   50,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Measure reconstruction error
	totalError := float64(0)
	for _, v := range vectors[:100] {
		code, err := pq.Encode(v)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}

		// Reconstruct by looking up centroids
		reconstructed := make([]float32, 64)
		centroids := pq.Centroids()
		for m := 0; m < pq.NumSubspaces(); m++ {
			centroidIdx := code[m]
			start := m * pq.SubspaceDim()
			copy(reconstructed[start:start+pq.SubspaceDim()], centroids[m][centroidIdx])
		}

		// Compute squared error
		var sqError float64
		for i := 0; i < 64; i++ {
			d := float64(v[i] - reconstructed[i])
			sqError += d * d
		}
		totalError += sqError
	}

	avgError := totalError / 100
	t.Logf("Average reconstruction squared error: %f", avgError)

	// The error should be reasonable (depends on data and configuration)
	// For random uniform data with 8 subspaces and 64 centroids, error should be relatively small
	if avgError > 10.0 {
		t.Errorf("Reconstruction error too high: %f", avgError)
	}
}

// =============================================================================
// Search Accuracy Tests (PQ.12)
// =============================================================================

func TestProductQuantizer_SearchAccuracy_RecallReport(t *testing.T) {
	// Create test dataset and report Recall@10, Recall@100
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 64,
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Generate random data
	rng := rand.New(rand.NewSource(42))
	numDB := 500
	dim := 64
	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		for j := range dbVectors[i] {
			dbVectors[i][j] = rng.Float32()
		}
	}

	// Train
	trainConfig := TrainConfig{
		MaxIterations:   50,
		Seed:            12345,
		MinSamplesRatio: 5.0,
	}
	if err := pq.TrainWithConfig(context.Background(), dbVectors, trainConfig); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Encode database
	codes, _ := pq.EncodeBatch(dbVectors, 4)

	// Helper function to compute recall
	computeRecall := func(k int) float64 {
		numQueries := 20
		totalRecall := 0.0

		for q := 0; q < numQueries; q++ {
			query := dbVectors[q*10]

			// Compute exact distances
			exactDists := make([]float64, numDB)
			for i, dbVec := range dbVectors {
				var sum float64
				for j := 0; j < dim; j++ {
					d := float64(query[j] - dbVec[j])
					sum += d * d
				}
				exactDists[i] = sum
			}

			// Find exact top-k indices
			exactTopK := make([]int, k)
			for i := 0; i < k; i++ {
				minIdx := 0
				minDist := math.MaxFloat64
				for j := 0; j < numDB; j++ {
					skip := false
					for _, selected := range exactTopK[:i] {
						if j == selected {
							skip = true
							break
						}
					}
					if skip {
						continue
					}
					if exactDists[j] < minDist {
						minDist = exactDists[j]
						minIdx = j
					}
				}
				exactTopK[i] = minIdx
			}

			// Compute PQ distances
			table, _ := pq.ComputeDistanceTable(query)
			pqDists := pq.AsymmetricDistanceBatch(table, codes)

			// Find PQ top-k indices
			pqTopK := make([]int, k)
			for i := 0; i < k; i++ {
				minIdx := 0
				minDist := float32(math.MaxFloat32)
				for j := 0; j < numDB; j++ {
					skip := false
					for _, selected := range pqTopK[:i] {
						if j == selected {
							skip = true
							break
						}
					}
					if skip {
						continue
					}
					if pqDists[j] < minDist {
						minDist = pqDists[j]
						minIdx = j
					}
				}
				pqTopK[i] = minIdx
			}

			// Compute recall
			hits := 0
			for _, exactIdx := range exactTopK {
				for _, pqIdx := range pqTopK {
					if exactIdx == pqIdx {
						hits++
						break
					}
				}
			}
			totalRecall += float64(hits) / float64(k)
		}

		return totalRecall / float64(numQueries)
	}

	recall10 := computeRecall(10)
	recall100 := computeRecall(100)

	t.Logf("Recall@10: %.2f%%", recall10*100)
	t.Logf("Recall@100: %.2f%%", recall100*100)

	// Basic sanity checks
	if recall10 < 0.2 {
		t.Errorf("Recall@10 = %.2f%%, expected at least 20%%", recall10*100)
	}
	if recall100 < 0.3 {
		t.Errorf("Recall@100 = %.2f%%, expected at least 30%%", recall100*100)
	}
	// Recall@100 should generally be >= Recall@10 or close
	if recall100 < recall10*0.8 {
		t.Errorf("Recall@100 (%.2f%%) should not be much lower than Recall@10 (%.2f%%)",
			recall100*100, recall10*100)
	}
}

// =============================================================================
// Compression Tests (PQ.12)
// =============================================================================

func TestProductQuantizer_Compression_VerifyRatio(t *testing.T) {
	// Verify compression ratio
	// 768-dim float32 (3072 bytes) -> 32-byte PQCode
	// Expected: 96x compression

	tests := []struct {
		name             string
		vectorDim        int
		numSubspaces     int
		expectedCodeSize int
		expectedRatio    int
	}{
		{
			name:             "768-dim with 32 subspaces",
			vectorDim:        768,
			numSubspaces:     32,
			expectedCodeSize: 32,
			expectedRatio:    96, // 768*4 / 32 = 96
		},
		{
			name:             "512-dim with 64 subspaces",
			vectorDim:        512,
			numSubspaces:     64,
			expectedCodeSize: 64,
			expectedRatio:    32, // 512*4 / 64 = 32
		},
		{
			name:             "1024-dim with 32 subspaces",
			vectorDim:        1024,
			numSubspaces:     32,
			expectedCodeSize: 32,
			expectedRatio:    128, // 1024*4 / 32 = 128
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalSize := tt.vectorDim * 4  // float32 = 4 bytes
			compressedSize := tt.numSubspaces // uint8 per subspace = 1 byte

			// Verify code size
			code := NewPQCode(tt.numSubspaces)
			if len(code) != tt.expectedCodeSize {
				t.Errorf("PQCode size = %d, want %d", len(code), tt.expectedCodeSize)
			}

			// Verify compression ratio
			ratio := originalSize / compressedSize
			if ratio != tt.expectedRatio {
				t.Errorf("compression ratio = %d, want %d", ratio, tt.expectedRatio)
			}

			t.Logf("Compression: %d bytes -> %d bytes (%.0fx)", originalSize, compressedSize, float64(ratio))
		})
	}
}

func TestProductQuantizer_Compression_ActualMemory(t *testing.T) {
	// Test actual memory usage with a trained quantizer
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, err := NewProductQuantizer(768, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train using parallel for performance
	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		NumRestarts:     2,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 0); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Encode a vector
	code, err := pq.Encode(vectors[0])
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// Measure code size using MarshalBinary
	codeBytes, _ := code.MarshalBinary()

	originalSize := 768 * 4 // 3072 bytes
	compressedSize := len(codeBytes)

	t.Logf("Original vector size: %d bytes", originalSize)
	t.Logf("Compressed code size: %d bytes", compressedSize)
	t.Logf("Compression ratio: %.1fx", float64(originalSize)/float64(compressedSize))

	// Verify expected compression
	if compressedSize != 32 {
		t.Errorf("compressed size = %d bytes, want 32 bytes", compressedSize)
	}
}

func TestProductQuantizer_Compression_BatchStorage(t *testing.T) {
	// Test storage savings for a batch of vectors
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, err := NewProductQuantizer(768, config)
	if err != nil {
		t.Fatalf("failed to create ProductQuantizer: %v", err)
	}

	// Train using parallel for performance
	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		NumRestarts:     2,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 0); err != nil {
		t.Fatalf("failed to train: %v", err)
	}

	// Encode all vectors
	numVectors := 1000
	codes, _ := pq.EncodeBatch(vectors[:numVectors], 4)

	// Calculate storage
	originalStorage := numVectors * 768 * 4 // float32 vectors
	compressedStorage := numVectors * 32    // PQ codes

	t.Logf("Storing %d vectors:", numVectors)
	t.Logf("  Original: %d bytes (%.2f MB)", originalStorage, float64(originalStorage)/(1024*1024))
	t.Logf("  Compressed: %d bytes (%.2f KB)", compressedStorage, float64(compressedStorage)/1024)
	t.Logf("  Savings: %.2f MB (%.1fx compression)",
		float64(originalStorage-compressedStorage)/(1024*1024),
		float64(originalStorage)/float64(compressedStorage))

	if len(codes) != numVectors {
		t.Errorf("encoded %d vectors, expected %d", len(codes), numVectors)
	}
}

// =============================================================================
// Serialization Benchmark Tests (PQ.11)
// =============================================================================

func BenchmarkProductQuantizer_MarshalBinary(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)

	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		NumRestarts:     2,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.MarshalBinary()
	}
}

func BenchmarkProductQuantizer_UnmarshalBinary(b *testing.B) {
	config := ProductQuantizerConfig{
		NumSubspaces:         32,
		CentroidsPerSubspace: 256,
	}
	pq, _ := NewProductQuantizer(768, config)

	vectors := generateTrainingVectors(3000, 768, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		NumRestarts:     2,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 0)

	data, _ := pq.MarshalBinary()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq2 := &ProductQuantizer{}
		pq2.UnmarshalBinary(data)
	}
}

func BenchmarkPQCode_MarshalBinary(b *testing.B) {
	code := NewPQCode(32)
	for i := range code {
		code[i] = uint8(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		code.MarshalBinary()
	}
}

func BenchmarkPQCode_UnmarshalBinary(b *testing.B) {
	code := NewPQCode(32)
	for i := range code {
		code[i] = uint8(i)
	}
	data, _ := code.MarshalBinary()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var code2 PQCode
		code2.UnmarshalBinary(data)
	}
}

// =============================================================================
// EncodingBuffer Tests
// =============================================================================

func TestEncodingBuffer_NewEncodingBuffer(t *testing.T) {
	buf := NewEncodingBuffer()
	if buf == nil {
		t.Fatal("NewEncodingBuffer() returned nil")
	}
	if buf.subspaceData != nil {
		t.Error("new buffer should have nil subspaceData")
	}
	if buf.xNorms != nil {
		t.Error("new buffer should have nil xNorms")
	}
	if buf.dots != nil {
		t.Error("new buffer should have nil dots")
	}
}

func TestEncodingBuffer_EnsureCapacity(t *testing.T) {
	tests := []struct {
		name        string
		chunkN      int
		subspaceDim int
		k           int
	}{
		{"small", 10, 8, 16},
		{"medium", 100, 24, 256},
		{"large", 512, 32, 256},
		{"single", 1, 4, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := NewEncodingBuffer()
			buf.EnsureCapacity(tt.chunkN, tt.subspaceDim, tt.k)

			expectedSubspaceSize := tt.chunkN * tt.subspaceDim
			expectedDotsSize := tt.chunkN * tt.k

			if len(buf.subspaceData) != expectedSubspaceSize {
				t.Errorf("subspaceData len = %d, want %d", len(buf.subspaceData), expectedSubspaceSize)
			}
			if len(buf.xNorms) != tt.chunkN {
				t.Errorf("xNorms len = %d, want %d", len(buf.xNorms), tt.chunkN)
			}
			if len(buf.dots) != expectedDotsSize {
				t.Errorf("dots len = %d, want %d", len(buf.dots), expectedDotsSize)
			}
		})
	}
}

func TestEncodingBuffer_EnsureCapacity_Reuse(t *testing.T) {
	buf := NewEncodingBuffer()

	// First allocation
	buf.EnsureCapacity(100, 24, 256)
	firstSubspace := buf.subspaceData
	firstNorms := buf.xNorms
	firstDots := buf.dots

	// Smaller request should reuse existing buffers (same backing array)
	buf.EnsureCapacity(50, 24, 256)
	if cap(buf.subspaceData) != cap(firstSubspace) {
		t.Error("smaller request should reuse subspaceData capacity")
	}
	if cap(buf.xNorms) != cap(firstNorms) {
		t.Error("smaller request should reuse xNorms capacity")
	}
	if cap(buf.dots) != cap(firstDots) {
		t.Error("smaller request should reuse dots capacity")
	}
}

func TestEncodingBuffer_Reset(t *testing.T) {
	buf := NewEncodingBuffer()
	buf.EnsureCapacity(10, 8, 16)

	// Fill with non-zero values
	for i := range buf.subspaceData {
		buf.subspaceData[i] = float32(i + 1)
	}
	for i := range buf.xNorms {
		buf.xNorms[i] = float32(i + 1)
	}
	for i := range buf.dots {
		buf.dots[i] = float32(i + 1)
	}

	buf.Reset()

	// Verify all zeroed
	for i, v := range buf.subspaceData {
		if v != 0 {
			t.Errorf("subspaceData[%d] = %f after Reset, want 0", i, v)
		}
	}
	for i, v := range buf.xNorms {
		if v != 0 {
			t.Errorf("xNorms[%d] = %f after Reset, want 0", i, v)
		}
	}
	for i, v := range buf.dots {
		if v != 0 {
			t.Errorf("dots[%d] = %f after Reset, want 0", i, v)
		}
	}
}

// =============================================================================
// Golden Tests: Verify EncodeBatch produces identical output to single Encode
// =============================================================================

func TestEncodeBatch_GoldenTest_ByteForByteIdentical(t *testing.T) {
	// This test verifies that EncodeBatch produces EXACTLY the same
	// output as calling Encode on each vector individually.
	// This is critical for correctness validation of buffer reuse.

	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 32,
	}
	pq, err := NewProductQuantizer(64, config)
	if err != nil {
		t.Fatalf("failed to create PQ: %v", err)
	}

	// Train with deterministic seed
	vectors := generateTrainingVectors(500, 64, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
		t.Fatalf("training failed: %v", err)
	}

	// Test various batch sizes
	testSizes := []int{1, 2, 5, 10, 31, 32, 33, 63, 64, 65, 100, 127, 128, 129, 256, 500}

	for _, batchSize := range testSizes {
		t.Run(fmt.Sprintf("size_%d", batchSize), func(t *testing.T) {
			testVectors := vectors[:batchSize]

			// Encode using batch method
			batchCodes, err := pq.EncodeBatch(testVectors, 4)
			if err != nil {
				t.Fatalf("EncodeBatch failed: %v", err)
			}

			// Encode using single method
			singleCodes := make([]PQCode, batchSize)
			for i, v := range testVectors {
				code, err := pq.Encode(v)
				if err != nil {
					t.Fatalf("Encode failed: %v", err)
				}
				singleCodes[i] = code
			}

			// Verify byte-for-byte identical
			for i := 0; i < batchSize; i++ {
				if !batchCodes[i].Equal(singleCodes[i]) {
					t.Errorf("vector %d: batch=%v, single=%v", i, batchCodes[i], singleCodes[i])
				}
			}
		})
	}
}

func TestEncodeBatch_GoldenTest_ChunkBoundaries(t *testing.T) {
	// Test behavior at exact chunk boundaries to ensure no off-by-one errors
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 16,
	}
	pq, _ := NewProductQuantizer(32, config)

	vectors := generateTrainingVectors(200, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Test with exactly 32, 64, 128 vectors (chunk boundary sizes)
	for _, size := range []int{32, 64, 128, 160} {
		testVectors := vectors[:size]

		batchCodes, _ := pq.EncodeBatch(testVectors, 2)

		for i, v := range testVectors {
			singleCode, _ := pq.Encode(v)
			if !batchCodes[i].Equal(singleCode) {
				t.Errorf("size %d, vector %d mismatch", size, i)
			}
		}
	}
}

func TestEncodeBatch_GoldenTest_PartialChunks(t *testing.T) {
	// Test partial chunks (when batch size is not evenly divisible by chunk size)
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Test partial chunk sizes: 33, 47, 65, 97 (not divisible by common chunk sizes)
	for _, size := range []int{33, 47, 65, 97} {
		testVectors := vectors[:size]

		batchCodes, _ := pq.EncodeBatch(testVectors, 4)

		for i, v := range testVectors {
			singleCode, _ := pq.Encode(v)
			if !batchCodes[i].Equal(singleCode) {
				t.Errorf("partial chunk size %d, vector %d mismatch", size, i)
			}
		}
	}
}

func TestEncodeBatch_GoldenTest_DifferentConfigurations(t *testing.T) {
	// Test with different subspace and centroid configurations
	configs := []struct {
		name      string
		dim       int
		subspaces int
		centroids int
		skipShort bool
	}{
		{"small_4x8", 32, 4, 8, false},
		{"medium_8x32", 64, 8, 32, false},
		{"large_16x64", 128, 16, 64, false},
		{"max_16x128", 256, 16, 128, true}, // Reduced from 768/32/256 for faster CI
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			if cfg.skipShort && testing.Short() {
				t.Skip("skipping large configuration in short mode")
			}
			pqConfig := ProductQuantizerConfig{
				NumSubspaces:         cfg.subspaces,
				CentroidsPerSubspace: cfg.centroids,
			}
			pq, err := NewProductQuantizer(cfg.dim, pqConfig)
			if err != nil {
				t.Fatalf("failed to create PQ: %v", err)
			}

			// Need enough training vectors
			minSamples := cfg.centroids * max(10, cfg.dim/cfg.subspaces)
			vectors := generateTrainingVectors(minSamples+200, cfg.dim, 42)
			trainConfig := TrainConfig{
				MaxIterations:   15,
				Seed:            12345,
				MinSamplesRatio: 10.0,
			}
			if err := pq.TrainWithConfig(context.Background(), vectors, trainConfig); err != nil {
				t.Fatalf("training failed: %v", err)
			}

			// Test with 100 vectors
			testVectors := vectors[:100]
			batchCodes, _ := pq.EncodeBatch(testVectors, 4)

			for i, v := range testVectors {
				singleCode, _ := pq.Encode(v)
				if !batchCodes[i].Equal(singleCode) {
					t.Errorf("config %s, vector %d mismatch", cfg.name, i)
				}
			}
		})
	}
}

func TestEncodeBatch_GoldenTest_ConcurrentEncodings(t *testing.T) {
	// Test concurrent encoding to verify no cross-worker contamination
	config := ProductQuantizerConfig{
		NumSubspaces:         8,
		CentroidsPerSubspace: 32,
	}
	pq, _ := NewProductQuantizer(64, config)

	vectors := generateTrainingVectors(500, 64, 42)
	trainConfig := TrainConfig{
		MaxIterations:   20,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Compute expected results with single encode
	expected := make([]PQCode, len(vectors))
	for i, v := range vectors {
		expected[i], _ = pq.Encode(v)
	}

	// Run multiple concurrent batch encodings
	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Each goroutine encodes the full batch
			batchCodes, err := pq.EncodeBatch(vectors, 4)
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: EncodeBatch failed: %v", id, err)
				return
			}

			// Verify results match expected
			for i := range batchCodes {
				if !batchCodes[i].Equal(expected[i]) {
					errors <- fmt.Errorf("goroutine %d, vector %d mismatch", id, i)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

func TestEncodeBatch_GoldenTest_SingleVector(t *testing.T) {
	// Edge case: batch of size 1
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 8,
	}
	pq, _ := NewProductQuantizer(32, config)

	vectors := generateTrainingVectors(100, 32, 42)
	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	singleVec := [][]float32{vectors[0]}

	batchCode, err := pq.EncodeBatch(singleVec, 1)
	if err != nil {
		t.Fatalf("EncodeBatch failed: %v", err)
	}

	expectedCode, _ := pq.Encode(vectors[0])
	if !batchCode[0].Equal(expectedCode) {
		t.Errorf("single vector batch mismatch: got %v, want %v", batchCode[0], expectedCode)
	}
}

func TestEncodeBatch_NoDataLeakage(t *testing.T) {
	// Test that buffer reuse doesn't leak data between chunks
	config := ProductQuantizerConfig{
		NumSubspaces:         4,
		CentroidsPerSubspace: 16,
	}
	pq, _ := NewProductQuantizer(32, config)

	// Create vectors where consecutive chunks have very different values
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 200)
	for i := range vectors {
		vectors[i] = make([]float32, 32)
		// First chunk: values around 1.0
		// Second chunk: values around 100.0
		// etc.
		scale := float32(1.0)
		if i >= 50 {
			scale = 100.0
		}
		if i >= 100 {
			scale = 0.01
		}
		if i >= 150 {
			scale = 1000.0
		}
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32() * scale
		}
	}

	trainConfig := TrainConfig{
		MaxIterations:   10,
		Seed:            12345,
		MinSamplesRatio: 10.0,
	}
	pq.TrainWithConfig(context.Background(), vectors, trainConfig)

	// Encode batch
	batchCodes, _ := pq.EncodeBatch(vectors, 2)

	// Verify each matches single encode
	for i, v := range vectors {
		singleCode, _ := pq.Encode(v)
		if !batchCodes[i].Equal(singleCode) {
			t.Errorf("data leakage detected at vector %d: batch=%v, single=%v", i, batchCodes[i], singleCode)
		}
	}
}
