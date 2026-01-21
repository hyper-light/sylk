package quantization

import (
	"math"
	"math/rand"
	"sync"
	"testing"
)

func TestAnisotropicVQ_NewAnisotropicVQ(t *testing.T) {
	tests := []struct {
		name string
		dim  int
		want int
	}{
		{"positive dimension", 64, 64},
		{"zero dimension defaults to 1", 0, 1},
		{"negative dimension defaults to 1", -5, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			avq := NewAnisotropicVQ(tt.dim)
			if avq.Dim() != tt.want {
				t.Errorf("Dim() = %v, want %v", avq.Dim(), tt.want)
			}
			if avq.GetParallelWeight() != 10.0 {
				t.Errorf("GetParallelWeight() = %v, want 10.0", avq.GetParallelWeight())
			}
			if avq.ObservationCount() != 0 {
				t.Errorf("ObservationCount() = %v, want 0", avq.ObservationCount())
			}
		})
	}
}

func TestAnisotropicVQ_AnisotropicLoss(t *testing.T) {
	avq := NewAnisotropicVQ(3)
	avq.SetParallelWeight(10.0)

	original := []float32{1.0, 0.0, 0.0}
	reconstructed := []float32{0.9, 0.1, 0.0}
	queryDir := []float32{1.0, 0.0, 0.0}

	loss := avq.AnisotropicLoss(original, reconstructed, queryDir)

	diff := []float64{0.1, -0.1, 0.0}
	parallelError := diff[0]*1.0 + diff[1]*0.0 + diff[2]*0.0
	totalError := diff[0]*diff[0] + diff[1]*diff[1] + diff[2]*diff[2]
	orthogonalErrorSq := totalError - parallelError*parallelError
	expectedLoss := 10.0*parallelError*parallelError + orthogonalErrorSq

	if math.Abs(loss-expectedLoss) > 1e-6 {
		t.Errorf("AnisotropicLoss() = %v, want %v", loss, expectedLoss)
	}
}

func TestAnisotropicVQ_AnisotropicLoss_ParallelVsOrthogonalWeighting(t *testing.T) {
	avq := NewAnisotropicVQ(2)
	avq.SetParallelWeight(10.0)

	original := []float32{1.0, 1.0}

	parallelErrorVec := []float32{0.9, 1.0}
	orthogonalErrorVec := []float32{1.0, 0.9}

	queryDir := []float32{1.0, 0.0}

	parallelLoss := avq.AnisotropicLoss(original, parallelErrorVec, queryDir)
	orthogonalLoss := avq.AnisotropicLoss(original, orthogonalErrorVec, queryDir)

	if parallelLoss <= orthogonalLoss {
		t.Errorf("Parallel error should have higher loss: parallelLoss=%v, orthogonalLoss=%v",
			parallelLoss, orthogonalLoss)
	}
}

func TestAnisotropicVQ_AnisotropicLoss_EmptyInputs(t *testing.T) {
	avq := NewAnisotropicVQ(3)

	if loss := avq.AnisotropicLoss(nil, []float32{1, 2, 3}, []float32{1, 0, 0}); loss != 0 {
		t.Errorf("Expected 0 loss for nil original, got %v", loss)
	}

	if loss := avq.AnisotropicLoss([]float32{1, 2, 3}, nil, []float32{1, 0, 0}); loss != 0 {
		t.Errorf("Expected 0 loss for nil reconstructed, got %v", loss)
	}

	if loss := avq.AnisotropicLoss([]float32{}, []float32{1, 2, 3}, []float32{1, 0, 0}); loss != 0 {
		t.Errorf("Expected 0 loss for empty original, got %v", loss)
	}
}

func TestAnisotropicVQ_UpdateQueryDistribution(t *testing.T) {
	avq := NewAnisotropicVQ(3)

	queries := [][]float32{
		{1.0, 0.0, 0.0},
		{1.0, 0.0, 0.0},
		{1.0, 0.0, 0.0},
	}

	for _, q := range queries {
		avq.UpdateQueryDistribution(q)
	}

	if avq.ObservationCount() != 3 {
		t.Errorf("ObservationCount() = %v, want 3", avq.ObservationCount())
	}

	cov := avq.GetQueryCovariance()
	if cov == nil {
		t.Fatal("GetQueryCovariance() returned nil")
	}

	if cov.At(0, 0) < cov.At(1, 1) || cov.At(0, 0) < cov.At(2, 2) {
		t.Error("Covariance should show higher variance in x-direction")
	}
}

func TestAnisotropicVQ_UpdateQueryDistribution_EmptyQuery(t *testing.T) {
	avq := NewAnisotropicVQ(3)
	avq.UpdateQueryDistribution([]float32{})

	if avq.ObservationCount() != 0 {
		t.Errorf("Empty query should not increment count, got %v", avq.ObservationCount())
	}
}

func TestAnisotropicVQ_GetPrincipalQueryDirection(t *testing.T) {
	avq := NewAnisotropicVQ(3)

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		query := []float32{
			float32(rng.NormFloat64() + 5.0),
			float32(rng.NormFloat64() * 0.1),
			float32(rng.NormFloat64() * 0.1),
		}
		avq.UpdateQueryDistribution(query)
	}

	dir := avq.GetPrincipalQueryDirection()

	if len(dir) != 3 {
		t.Fatalf("Expected direction of length 3, got %v", len(dir))
	}

	if math.Abs(float64(dir[0])) < math.Abs(float64(dir[1])) ||
		math.Abs(float64(dir[0])) < math.Abs(float64(dir[2])) {
		t.Errorf("Principal direction should be dominated by x-component, got %v", dir)
	}
}

func TestAnisotropicVQ_GetPrincipalQueryDirection_Identity(t *testing.T) {
	avq := NewAnisotropicVQ(3)

	dir := avq.GetPrincipalQueryDirection()

	if len(dir) != 3 {
		t.Fatalf("Expected direction of length 3, got %v", len(dir))
	}

	var norm float64
	for _, v := range dir {
		norm += float64(v * v)
	}
	norm = math.Sqrt(norm)

	if math.Abs(norm-1.0) > 0.01 {
		t.Errorf("Direction should be unit vector, got norm=%v", norm)
	}
}

func TestAnisotropicVQ_SetParallelWeight(t *testing.T) {
	avq := NewAnisotropicVQ(3)

	avq.SetParallelWeight(50.0)
	if got := avq.GetParallelWeight(); got != 50.0 {
		t.Errorf("GetParallelWeight() = %v, want 50.0", got)
	}

	avq.SetParallelWeight(1.0)
	if got := avq.GetParallelWeight(); got != 1.0 {
		t.Errorf("GetParallelWeight() = %v, want 1.0", got)
	}
}

func TestAnisotropicVQ_DeriveParallelWeight(t *testing.T) {
	avq := NewAnisotropicVQ(3)

	t.Run("insufficient samples returns default", func(t *testing.T) {
		samples := make([][]float32, 5)
		for i := range samples {
			samples[i] = []float32{1.0, 2.0, 3.0}
		}
		weight := avq.DeriveParallelWeight(samples)
		if weight != 10.0 {
			t.Errorf("Expected default weight 10.0 for insufficient samples, got %v", weight)
		}
	})

	t.Run("low variance gives high weight", func(t *testing.T) {
		samples := make([][]float32, 100)
		for i := range samples {
			samples[i] = []float32{1.0, 1.0, 1.0}
		}
		weight := avq.DeriveParallelWeight(samples)
		if weight < 50.0 {
			t.Errorf("Low variance should give high weight, got %v", weight)
		}
	})

	t.Run("high variance gives low weight", func(t *testing.T) {
		rng := rand.New(rand.NewSource(42))
		samples := make([][]float32, 100)
		for i := range samples {
			samples[i] = []float32{
				float32(rng.NormFloat64() * 10),
				float32(rng.NormFloat64() * 10),
				float32(rng.NormFloat64() * 10),
			}
		}
		weight := avq.DeriveParallelWeight(samples)
		if weight > 10.0 {
			t.Errorf("High variance should give low weight, got %v", weight)
		}
	})

	t.Run("weight is clamped to range", func(t *testing.T) {
		samples := make([][]float32, 100)
		for i := range samples {
			samples[i] = []float32{1.0, 1.0, 1.0}
		}
		weight := avq.DeriveParallelWeight(samples)
		if weight < 1.0 || weight > 100.0 {
			t.Errorf("Weight should be in [1.0, 100.0], got %v", weight)
		}
	})
}

func TestAnisotropicVQ_Reset(t *testing.T) {
	avq := NewAnisotropicVQ(3)

	for i := 0; i < 10; i++ {
		avq.UpdateQueryDistribution([]float32{1.0, 2.0, 3.0})
	}
	avq.SetParallelWeight(50.0)

	if avq.ObservationCount() != 10 {
		t.Fatalf("Expected 10 observations before reset")
	}

	avq.Reset()

	if avq.ObservationCount() != 0 {
		t.Errorf("ObservationCount() = %v after reset, want 0", avq.ObservationCount())
	}

	cov := avq.GetQueryCovariance()
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			expected := 0.0
			if i == j {
				expected = 1.0
			}
			if math.Abs(cov.At(i, j)-expected) > 1e-10 {
				t.Errorf("Covariance[%d][%d] = %v, want %v", i, j, cov.At(i, j), expected)
			}
		}
	}

	if avq.GetParallelWeight() != 50.0 {
		t.Error("Reset should not affect parallelWeight")
	}
}

func TestAnisotropicVQ_ThreadSafety(t *testing.T) {
	avq := NewAnisotropicVQ(32)

	var wg sync.WaitGroup
	numGoroutines := 10
	operationsPerGoroutine := 100

	wg.Add(numGoroutines * 3)

	for i := 0; i < numGoroutines; i++ {
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for j := 0; j < operationsPerGoroutine; j++ {
				query := make([]float32, 32)
				for k := range query {
					query[k] = float32(rng.NormFloat64())
				}
				avq.UpdateQueryDistribution(query)
			}
		}(int64(i))

		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				_ = avq.GetPrincipalQueryDirection()
			}
		}()

		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed + 1000))
			for j := 0; j < operationsPerGoroutine; j++ {
				original := make([]float32, 32)
				reconstructed := make([]float32, 32)
				queryDir := make([]float32, 32)
				for k := range original {
					original[k] = float32(rng.NormFloat64())
					reconstructed[k] = float32(rng.NormFloat64())
					queryDir[k] = float32(rng.NormFloat64())
				}
				_ = avq.AnisotropicLoss(original, reconstructed, queryDir)
			}
		}(int64(i))
	}

	wg.Wait()
}

func TestAnisotropicVQ_IntegrationWithQuantization(t *testing.T) {
	dim := 64
	avq := NewAnisotropicVQ(dim)
	rng := rand.New(rand.NewSource(42))

	queries := make([][]float32, 50)
	for i := range queries {
		queries[i] = make([]float32, dim)
		for j := range queries[i] {
			if j < 8 {
				queries[i][j] = float32(rng.NormFloat64() + 2.0)
			} else {
				queries[i][j] = float32(rng.NormFloat64() * 0.2)
			}
		}
		avq.UpdateQueryDistribution(queries[i])
	}

	derivedWeight := avq.DeriveParallelWeight(queries)
	avq.SetParallelWeight(derivedWeight)

	principalDir := avq.GetPrincipalQueryDirection()

	original := make([]float32, dim)
	for i := range original {
		original[i] = float32(rng.NormFloat64())
	}

	parallelQuantized := make([]float32, dim)
	orthogonalQuantized := make([]float32, dim)
	copy(parallelQuantized, original)
	copy(orthogonalQuantized, original)

	for i := 0; i < 8; i++ {
		parallelQuantized[i] += 0.1
	}
	for i := 8; i < 16; i++ {
		orthogonalQuantized[i] += 0.1
	}

	parallelLoss := avq.AnisotropicLoss(original, parallelQuantized, principalDir)
	orthogonalLoss := avq.AnisotropicLoss(original, orthogonalQuantized, principalDir)

	t.Logf("Parallel loss: %v", parallelLoss)
	t.Logf("Orthogonal loss: %v", orthogonalLoss)
	t.Logf("Derived weight: %v", derivedWeight)
	t.Logf("Principal direction (first 8): %v", principalDir[:8])
}
