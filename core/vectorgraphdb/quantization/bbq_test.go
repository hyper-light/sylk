package quantization

import (
	"math"
	"math/rand"
	"testing"
)

func TestNewBBQEncoder(t *testing.T) {
	tests := []struct {
		name           string
		dim            int
		queryBits      int
		expectedMaxVal float64
	}{
		{"4-bit quantization", 128, 4, 7},
		{"8-bit quantization", 128, 8, 127},
		{"2-bit quantization", 64, 2, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewBBQEncoder(tt.dim, tt.queryBits)
			if enc == nil {
				t.Fatal("expected non-nil encoder")
			}
			if enc.dim != tt.dim {
				t.Errorf("dim = %d, want %d", enc.dim, tt.dim)
			}
			if enc.queryBits != tt.queryBits {
				t.Errorf("queryBits = %d, want %d", enc.queryBits, tt.queryBits)
			}
			if enc.maxVal != tt.expectedMaxVal {
				t.Errorf("maxVal = %f, want %f", enc.maxVal, tt.expectedMaxVal)
			}
			if len(enc.means) != tt.dim {
				t.Errorf("len(means) = %d, want %d", len(enc.means), tt.dim)
			}
			if len(enc.scales) != tt.dim {
				t.Errorf("len(scales) = %d, want %d", len(enc.scales), tt.dim)
			}
		})
	}
}

func TestBBQEncoder_Train(t *testing.T) {
	dim := 4
	enc := NewBBQEncoder(dim, 4)

	// Create training data with known distribution
	vectors := [][]float32{
		{1, 2, 3, 4},
		{2, 4, 6, 8},
		{3, 6, 9, 12},
		{4, 8, 12, 16},
	}

	enc.Train(vectors)

	// Check means are computed (mean of {1,2,3,4}, {2,4,6,8}, etc.)
	expectedMeans := []float64{2.5, 5, 7.5, 10}
	for i, expected := range expectedMeans {
		if math.Abs(enc.means[i]-expected) > 1e-6 {
			t.Errorf("means[%d] = %f, want %f", i, enc.means[i], expected)
		}
	}

	// Scales should be non-zero and finite
	for i := 0; i < dim; i++ {
		if enc.scales[i] <= 0 || math.IsInf(enc.scales[i], 0) || math.IsNaN(enc.scales[i]) {
			t.Errorf("scales[%d] = %f, expected positive finite value", i, enc.scales[i])
		}
	}
}

func TestBBQEncoder_Train_EmptyVectors(t *testing.T) {
	enc := NewBBQEncoder(4, 4)
	enc.Train([][]float32{}) // Should not panic

	// Means and scales should remain at defaults
	for i := 0; i < 4; i++ {
		if enc.means[i] != 0 {
			t.Errorf("means[%d] should be 0 for empty training, got %f", i, enc.means[i])
		}
	}
}

func TestBBQEncoder_Train_ConstantValues(t *testing.T) {
	dim := 4
	enc := NewBBQEncoder(dim, 4)

	// All vectors have same values (zero variance)
	vectors := [][]float32{
		{5, 5, 5, 5},
		{5, 5, 5, 5},
		{5, 5, 5, 5},
	}

	enc.Train(vectors)

	// Means should all be 5
	for i := 0; i < dim; i++ {
		if math.Abs(enc.means[i]-5) > 1e-6 {
			t.Errorf("means[%d] = %f, want 5", i, enc.means[i])
		}
	}

	// Scales should fallback to 1.0 (zero variance case)
	for i := 0; i < dim; i++ {
		if enc.scales[i] != 1.0 {
			t.Errorf("scales[%d] = %f, want 1.0 for zero variance", i, enc.scales[i])
		}
	}
}

func TestBBQEncoder_EncodeDatabase(t *testing.T) {
	dim := 8
	enc := NewBBQEncoder(dim, 4)

	// Set known means
	for i := 0; i < dim; i++ {
		enc.means[i] = 0.5
	}

	tests := []struct {
		name     string
		vector   []float32
		expected []byte
	}{
		{
			"all above mean",
			[]float32{1, 1, 1, 1, 1, 1, 1, 1},
			[]byte{0xFF}, // all bits set
		},
		{
			"all below mean",
			[]float32{0, 0, 0, 0, 0, 0, 0, 0},
			[]byte{0x00}, // no bits set
		},
		{
			"alternating",
			[]float32{1, 0, 1, 0, 1, 0, 1, 0},
			[]byte{0x55}, // 01010101
		},
		{
			"first half above",
			[]float32{1, 1, 1, 1, 0, 0, 0, 0},
			[]byte{0x0F}, // 00001111
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := enc.EncodeDatabase(tt.vector)
			if len(code) != len(tt.expected) {
				t.Errorf("len(code) = %d, want %d", len(code), len(tt.expected))
				return
			}
			for i := range code {
				if code[i] != tt.expected[i] {
					t.Errorf("code[%d] = %02x, want %02x", i, code[i], tt.expected[i])
				}
			}
		})
	}
}

func TestBBQEncoder_EncodeDatabase_MultipleBytes(t *testing.T) {
	dim := 16
	enc := NewBBQEncoder(dim, 4)

	for i := 0; i < dim; i++ {
		enc.means[i] = 0
	}

	// All values above mean
	vector := make([]float32, dim)
	for i := range vector {
		vector[i] = 1
	}

	code := enc.EncodeDatabase(vector)
	if len(code) != 2 {
		t.Errorf("expected 2 bytes for dim=16, got %d", len(code))
	}
	if code[0] != 0xFF || code[1] != 0xFF {
		t.Errorf("expected [0xFF, 0xFF], got %v", code)
	}
}

func TestBBQEncoder_EncodeQuery(t *testing.T) {
	dim := 4
	enc := NewBBQEncoder(dim, 4) // maxVal = 7, minVal = -8

	// Set simple means and scales
	for i := 0; i < dim; i++ {
		enc.means[i] = 0
		enc.scales[i] = 1
	}

	tests := []struct {
		name     string
		vector   []float32
		expected []int8
	}{
		{
			"zeros",
			[]float32{0, 0, 0, 0},
			[]int8{0, 0, 0, 0},
		},
		{
			"positive clipped",
			[]float32{100, 100, 100, 100},
			[]int8{7, 7, 7, 7}, // Clipped to maxVal
		},
		{
			"negative clipped",
			[]float32{-100, -100, -100, -100},
			[]int8{-8, -8, -8, -8}, // Clipped to minVal
		},
		{
			"mixed",
			[]float32{3, -4, 0, 7},
			[]int8{3, -4, 0, 7},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := enc.EncodeQuery(tt.vector)
			if len(code) != len(tt.expected) {
				t.Errorf("len(code) = %d, want %d", len(code), len(tt.expected))
				return
			}
			for i := range code {
				if code[i] != tt.expected[i] {
					t.Errorf("code[%d] = %d, want %d", i, code[i], tt.expected[i])
				}
			}
		})
	}
}

func TestBBQEncoder_EncodeQuery_8bit(t *testing.T) {
	dim := 4
	enc := NewBBQEncoder(dim, 8) // maxVal = 127, minVal = -128

	for i := 0; i < dim; i++ {
		enc.means[i] = 0
		enc.scales[i] = 1
	}

	vector := []float32{127, -128, 50, -50}
	code := enc.EncodeQuery(vector)

	expected := []int8{127, -128, 50, -50}
	for i := range code {
		if code[i] != expected[i] {
			t.Errorf("code[%d] = %d, want %d", i, code[i], expected[i])
		}
	}
}

func TestBBQEncoder_AsymmetricDistance(t *testing.T) {
	enc := NewBBQEncoder(8, 4)

	tests := []struct {
		name      string
		queryCode []int8
		dbCode    []byte
		expected  int
	}{
		{
			"all bits set positive query",
			[]int8{1, 1, 1, 1, 1, 1, 1, 1},
			[]byte{0xFF},
			8, // all bits 1, so +1 each
		},
		{
			"all bits clear positive query",
			[]int8{1, 1, 1, 1, 1, 1, 1, 1},
			[]byte{0x00},
			-8, // all bits 0, so -1 each
		},
		{
			"all bits set negative query",
			[]int8{-1, -1, -1, -1, -1, -1, -1, -1},
			[]byte{0xFF},
			-8, // all bits 1, so -(-1)=-1 each? No: +queryCode
		},
		{
			"mixed",
			[]int8{2, -3, 4, -5, 0, 1, -1, 2},
			[]byte{0xA5}, // 10100101
			// bit 0=1: +2, bit 1=0: -(-3)=+3, bit 2=1: +4, bit 3=0: -(-5)=+5
			// bit 4=0: -0=0, bit 5=1: +1, bit 6=0: -(-1)=+1, bit 7=1: +2
			// = 2+3+4+5+0+1+1+2 = 18
			18,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dist := enc.AsymmetricDistance(tt.queryCode, tt.dbCode)
			if dist != tt.expected {
				t.Errorf("AsymmetricDistance = %d, want %d", dist, tt.expected)
			}
		})
	}
}

func TestBBQEncoder_BatchDistance(t *testing.T) {
	enc := NewBBQEncoder(8, 4)

	queryCode := []int8{1, 1, 1, 1, 1, 1, 1, 1}
	dbCodes := [][]byte{
		{0xFF}, // all 1s -> +8
		{0x00}, // all 0s -> -8
		{0xF0}, // upper 4 bits -> +4-4=0
	}

	dists := enc.BatchDistance(queryCode, dbCodes)

	expected := []int{8, -8, 0}
	for i := range dists {
		if dists[i] != expected[i] {
			t.Errorf("dists[%d] = %d, want %d", i, dists[i], expected[i])
		}
	}
}

// =============================================================================
// TestBBQEncoder_EndToEnd - Full pipeline test
// =============================================================================

func TestBBQEncoder_EndToEnd(t *testing.T) {
	dim := 64
	numVectors := 100
	enc := NewBBQEncoder(dim, 4)

	// Generate random training data
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()*2 - 1 // [-1, 1]
		}
	}

	// Train
	enc.Train(vectors)

	// Verify means are reasonable (should be near 0 for uniform [-1,1])
	for i := 0; i < dim; i++ {
		if math.Abs(enc.means[i]) > 0.5 {
			t.Errorf("means[%d] = %f, expected near 0", i, enc.means[i])
		}
	}

	// Encode all database vectors
	dbCodes := make([][]byte, numVectors)
	for i, v := range vectors {
		dbCodes[i] = enc.EncodeDatabase(v)
	}

	// Query with first vector
	queryCode := enc.EncodeQuery(vectors[0])

	// The closest match should be vector 0 itself (or very close)
	dists := enc.BatchDistance(queryCode, dbCodes)

	// Find max distance (BBQ returns higher = more similar)
	maxDist := dists[0]
	maxIdx := 0
	for i, d := range dists {
		if d > maxDist {
			maxDist = d
			maxIdx = i
		}
	}

	// Self-similarity should be highest or very close
	selfDist := dists[0]
	if maxDist > selfDist+dim/4 { // Allow some margin
		t.Errorf("self distance %d not among highest (max at idx %d = %d)", selfDist, maxIdx, maxDist)
	}
}

// =============================================================================
// TestBBQEncoder_DerivedParameters - Verify no hardcoded magic numbers
// =============================================================================

func TestBBQEncoder_DerivedParameters(t *testing.T) {
	tests := []struct {
		queryBits      int
		expectedMaxVal float64
		expectedMinVal float64
	}{
		{4, 7, -8},
		{8, 127, -128},
		{2, 1, -2},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			enc := NewBBQEncoder(4, tt.queryBits)
			enc.means = []float64{0, 0, 0, 0}
			enc.scales = []float64{1, 1, 1, 1}

			// Test maxVal clamping
			code := enc.EncodeQuery([]float32{1000, 1000, 1000, 1000})
			for i := range code {
				if int8(tt.expectedMaxVal) != code[i] {
					t.Errorf("queryBits=%d: expected max %d, got %d", tt.queryBits, int8(tt.expectedMaxVal), code[i])
				}
			}

			// Test minVal clamping
			code = enc.EncodeQuery([]float32{-1000, -1000, -1000, -1000})
			for i := range code {
				if int8(tt.expectedMinVal) != code[i] {
					t.Errorf("queryBits=%d: expected min %d, got %d", tt.queryBits, int8(tt.expectedMinVal), code[i])
				}
			}
		})
	}
}
