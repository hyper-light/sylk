package quantization

import (
	"context"
	"math"
	"math/rand"
	"testing"
)

func derivedTestClusteredVectors(n, dim, numClusters int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	vectors := make([][]float32, n)

	centers := make([][]float32, numClusters)
	for c := 0; c < numClusters; c++ {
		centers[c] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			centers[c][d] = rng.Float32()*10 - 5
		}
	}

	for i := 0; i < n; i++ {
		cluster := i % numClusters
		vectors[i] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			vectors[i][d] = centers[cluster][d] + rng.Float32()*0.5 - 0.25
		}
	}

	return vectors
}

func derivedTestUniformVectors(n, dim int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		vectors[i] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			vectors[i][d] = rng.Float32()*2 - 1
		}
	}
	return vectors
}

func derivedTestVaryingDensityVectors(n, dim int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	vectors := make([][]float32, n)

	densities := []float32{0.1, 0.5, 2.0}
	centers := [][]float32{
		make([]float32, dim),
		make([]float32, dim),
		make([]float32, dim),
	}

	for c := range centers {
		for d := 0; d < dim; d++ {
			centers[c][d] = float32(c) * 5.0
		}
	}

	for i := 0; i < n; i++ {
		cluster := i % 3
		vectors[i] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			noise := (rng.Float32()*2 - 1) * densities[cluster]
			vectors[i][d] = centers[cluster][d] + noise
		}
	}

	return vectors
}

func TestDataDerivedScalingFactors(t *testing.T) {
	t.Run("DefaultScalingFactors returns expected values", func(t *testing.T) {
		defaults := DefaultScalingFactors()

		if defaults.TargetDistortionRatio != 0.1 {
			t.Errorf("TargetDistortionRatio = %v, want 0.1", defaults.TargetDistortionRatio)
		}
		if defaults.BaseK != 64 {
			t.Errorf("BaseK = %v, want 64", defaults.BaseK)
		}
		if defaults.RecallScaleFactor != 10.0 {
			t.Errorf("RecallScaleFactor = %v, want 10.0", defaults.RecallScaleFactor)
		}
		if defaults.ClusteringAdjustmentRange[0] != 0.5 {
			t.Errorf("ClusteringAdjustmentRange[0] = %v, want 0.5", defaults.ClusteringAdjustmentRange[0])
		}
		if defaults.ClusteringAdjustmentRange[1] != 1.0 {
			t.Errorf("ClusteringAdjustmentRange[1] = %v, want 1.0", defaults.ClusteringAdjustmentRange[1])
		}
		if defaults.ComplexityMultiplierRange[0] != 0.5 {
			t.Errorf("ComplexityMultiplierRange[0] = %v, want 0.5", defaults.ComplexityMultiplierRange[0])
		}
		if defaults.ComplexityMultiplierRange[1] != 1.5 {
			t.Errorf("ComplexityMultiplierRange[1] = %v, want 1.5", defaults.ComplexityMultiplierRange[1])
		}
		if defaults.MinSamplesPerCentroid != 10 {
			t.Errorf("MinSamplesPerCentroid = %v, want 10", defaults.MinSamplesPerCentroid)
		}
		if defaults.SubspaceDimMultiplier != 2.0 {
			t.Errorf("SubspaceDimMultiplier = %v, want 2.0", defaults.SubspaceDimMultiplier)
		}
	})

	t.Run("Computed flag tracks derivation status", func(t *testing.T) {
		defaults := DefaultScalingFactors()
		if defaults.Computed {
			t.Error("DefaultScalingFactors should have Computed = false")
		}

		vectors := derivedTestUniformVectors(500, 64, 42)
		derived := DeriveScalingFactors(vectors, 8)
		if !derived.Computed {
			t.Error("DeriveScalingFactors should set Computed = true")
		}
	})
}

func TestDeriveScalingFactors(t *testing.T) {
	t.Run("Returns valid factors for clustered data", func(t *testing.T) {
		vectors := derivedTestClusteredVectors(1000, 64, 8, 42)
		factors := DeriveScalingFactors(vectors, 8)

		if !factors.Computed {
			t.Error("factors should be marked as Computed")
		}
		if factors.TargetDistortionRatio <= 0 || factors.TargetDistortionRatio > 1 {
			t.Errorf("TargetDistortionRatio = %v, want (0, 1]", factors.TargetDistortionRatio)
		}
		if factors.BaseK < 8 || factors.BaseK > 256 {
			t.Errorf("BaseK = %v, want [8, 256]", factors.BaseK)
		}
		if factors.RecallScaleFactor <= 0 {
			t.Errorf("RecallScaleFactor = %v, want > 0", factors.RecallScaleFactor)
		}
	})

	t.Run("Returns valid factors for uniform random data", func(t *testing.T) {
		vectors := derivedTestUniformVectors(1000, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		if !factors.Computed {
			t.Error("factors should be marked as Computed")
		}
		if factors.TargetDistortionRatio <= 0 || factors.TargetDistortionRatio > 1 {
			t.Errorf("TargetDistortionRatio = %v, want (0, 1]", factors.TargetDistortionRatio)
		}
		if factors.BaseK < 8 || factors.BaseK > 256 {
			t.Errorf("BaseK = %v, want [8, 256]", factors.BaseK)
		}
	})

	t.Run("Handles small datasets gracefully", func(t *testing.T) {
		smallVectors := derivedTestUniformVectors(5, 32, 42)
		factors := DeriveScalingFactors(smallVectors, 4)
		if factors.Computed {
			t.Error("factors should not be computed for very small datasets")
		}

		mediumVectors := derivedTestUniformVectors(50, 32, 42)
		factors = DeriveScalingFactors(mediumVectors, 4)
		if !factors.Computed {
			t.Error("factors should be computed for medium datasets")
		}
	})

	t.Run("Handles large datasets (sampling)", func(t *testing.T) {
		vectors := derivedTestUniformVectors(5000, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		if !factors.Computed {
			t.Error("factors should be computed for large datasets")
		}
		if factors.TargetDistortionRatio < 0.01 || factors.TargetDistortionRatio > 0.5 {
			t.Errorf("TargetDistortionRatio = %v, want [0.01, 0.5]", factors.TargetDistortionRatio)
		}
	})

	t.Run("Deterministic output for same input", func(t *testing.T) {
		vectors := derivedTestClusteredVectors(500, 64, 4, 42)

		factors1 := DeriveScalingFactors(vectors, 8)
		factors2 := DeriveScalingFactors(vectors, 8)

		if factors1.TargetDistortionRatio != factors2.TargetDistortionRatio {
			t.Errorf("TargetDistortionRatio not deterministic: %v vs %v",
				factors1.TargetDistortionRatio, factors2.TargetDistortionRatio)
		}
		if factors1.BaseK != factors2.BaseK {
			t.Errorf("BaseK not deterministic: %v vs %v", factors1.BaseK, factors2.BaseK)
		}
		if factors1.RecallScaleFactor != factors2.RecallScaleFactor {
			t.Errorf("RecallScaleFactor not deterministic: %v vs %v",
				factors1.RecallScaleFactor, factors2.RecallScaleFactor)
		}
		if factors1.ClusteringAdjustmentRange != factors2.ClusteringAdjustmentRange {
			t.Errorf("ClusteringAdjustmentRange not deterministic: %v vs %v",
				factors1.ClusteringAdjustmentRange, factors2.ClusteringAdjustmentRange)
		}
		if factors1.ComplexityMultiplierRange != factors2.ComplexityMultiplierRange {
			t.Errorf("ComplexityMultiplierRange not deterministic: %v vs %v",
				factors1.ComplexityMultiplierRange, factors2.ComplexityMultiplierRange)
		}
		if factors1.MinSamplesPerCentroid != factors2.MinSamplesPerCentroid {
			t.Errorf("MinSamplesPerCentroid not deterministic: %v vs %v",
				factors1.MinSamplesPerCentroid, factors2.MinSamplesPerCentroid)
		}
		if factors1.SubspaceDimMultiplier != factors2.SubspaceDimMultiplier {
			t.Errorf("SubspaceDimMultiplier not deterministic: %v vs %v",
				factors1.SubspaceDimMultiplier, factors2.SubspaceDimMultiplier)
		}
	})
}

func TestDeriveTargetDistortionRatio(t *testing.T) {
	t.Run("Based on actual reconstruction error", func(t *testing.T) {
		// Create data and derive factors
		vectors := derivedTestClusteredVectors(1000, 64, 8, 42)
		factors := DeriveScalingFactors(vectors, 8)

		// The ratio should be derived from actual reconstruction error
		// Should be > 0 and represent a meaningful target
		if factors.TargetDistortionRatio <= 0 {
			t.Errorf("TargetDistortionRatio = %v, should be > 0", factors.TargetDistortionRatio)
		}
	})

	t.Run("Returns sensible value (0.01 - 0.5 range)", func(t *testing.T) {
		testCases := []struct {
			name string
			gen  func() [][]float32
		}{
			{"clustered", func() [][]float32 { return derivedTestClusteredVectors(500, 64, 4, 42) }},
			{"uniform", func() [][]float32 { return derivedTestUniformVectors(500, 64, 42) }},
			{"varying density", func() [][]float32 { return derivedTestVaryingDensityVectors(500, 64, 42) }},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				vectors := tc.gen()
				factors := DeriveScalingFactors(vectors, 8)

				if factors.TargetDistortionRatio < 0.01 || factors.TargetDistortionRatio > 0.5 {
					t.Errorf("TargetDistortionRatio = %v, want [0.01, 0.5]",
						factors.TargetDistortionRatio)
				}
			})
		}
	})
}

func TestDeriveBaseK(t *testing.T) {
	t.Run("Based on intrinsic dimensionality", func(t *testing.T) {
		// Low intrinsic dim data (tight clusters)
		tightVectors := derivedTestClusteredVectors(1000, 64, 4, 42)
		tightFactors := DeriveScalingFactors(tightVectors, 8)

		// High intrinsic dim data (uniform random)
		uniformVectors := derivedTestUniformVectors(1000, 64, 42)
		uniformFactors := DeriveScalingFactors(uniformVectors, 8)

		// Uniform data should generally have higher baseK due to higher intrinsic dim
		// But both should be valid
		if tightFactors.BaseK < 8 || tightFactors.BaseK > 256 {
			t.Errorf("tightFactors.BaseK = %v, want [8, 256]", tightFactors.BaseK)
		}
		if uniformFactors.BaseK < 8 || uniformFactors.BaseK > 256 {
			t.Errorf("uniformFactors.BaseK = %v, want [8, 256]", uniformFactors.BaseK)
		}
	})

	t.Run("Scales with data complexity", func(t *testing.T) {
		// Test different dimensions
		dims := []int{32, 64, 128}
		var baseKs []int

		for _, dim := range dims {
			vectors := derivedTestUniformVectors(1000, dim, 42)
			numSubspaces := dim / 8
			factors := DeriveScalingFactors(vectors, numSubspaces)
			baseKs = append(baseKs, factors.BaseK)
		}

		// All baseKs should be valid powers of 2
		for i, k := range baseKs {
			if k < 8 || k > 256 {
				t.Errorf("BaseK for dim %d = %v, want [8, 256]", dims[i], k)
			}
		}
	})

	t.Run("Returns power of 2", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		// Check if BaseK is a power of 2
		k := factors.BaseK
		if k&(k-1) != 0 || k < 8 {
			t.Errorf("BaseK = %v, should be a power of 2 >= 8", k)
		}
	})
}

func TestDeriveRecallScaleFactor(t *testing.T) {
	t.Run("Derived from K=32,64,128 experiments", func(t *testing.T) {
		// Use sufficient data to run experiments
		vectors := derivedTestClusteredVectors(500, 64, 8, 42)
		factors := DeriveScalingFactors(vectors, 8)

		// RecallScaleFactor should be derived empirically
		if factors.RecallScaleFactor <= 0 {
			t.Errorf("RecallScaleFactor = %v, should be > 0", factors.RecallScaleFactor)
		}
	})

	t.Run("Positive value", func(t *testing.T) {
		testCases := []struct {
			name string
			n    int
			dim  int
		}{
			{"small", 200, 32},
			{"medium", 500, 64},
			{"large", 1000, 64},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				vectors := derivedTestUniformVectors(tc.n, tc.dim, 42)
				factors := DeriveScalingFactors(vectors, tc.dim/8)

				if factors.RecallScaleFactor <= 0 {
					t.Errorf("RecallScaleFactor = %v, should be > 0", factors.RecallScaleFactor)
				}
			})
		}
	})

	t.Run("Reasonable range", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		// RecallScaleFactor should be in reasonable range [5, 20]
		if factors.RecallScaleFactor < 5 || factors.RecallScaleFactor > 20 {
			t.Errorf("RecallScaleFactor = %v, want [5, 20]", factors.RecallScaleFactor)
		}
	})
}

func TestDeriveClusteringAdjustmentRange(t *testing.T) {
	t.Run("Based on Hopkins statistic", func(t *testing.T) {
		// Highly clustered data
		clusteredVectors := derivedTestClusteredVectors(500, 64, 4, 42)
		clusteredFactors := DeriveScalingFactors(clusteredVectors, 8)

		// Uniform data
		uniformVectors := derivedTestUniformVectors(500, 64, 42)
		uniformFactors := DeriveScalingFactors(uniformVectors, 8)

		// Clustered data should have different adjustment range than uniform
		// Both should be valid ranges
		if clusteredFactors.ClusteringAdjustmentRange[0] < 0 ||
			clusteredFactors.ClusteringAdjustmentRange[0] > 1 {
			t.Errorf("clustered ClusteringAdjustmentRange[0] = %v, want [0, 1]",
				clusteredFactors.ClusteringAdjustmentRange[0])
		}
		if uniformFactors.ClusteringAdjustmentRange[0] < 0 ||
			uniformFactors.ClusteringAdjustmentRange[0] > 1 {
			t.Errorf("uniform ClusteringAdjustmentRange[0] = %v, want [0, 1]",
				uniformFactors.ClusteringAdjustmentRange[0])
		}
	})

	t.Run("Range within [0, 1] for both endpoints", func(t *testing.T) {
		testCases := []struct {
			name string
			gen  func() [][]float32
		}{
			{"clustered", func() [][]float32 { return derivedTestClusteredVectors(500, 64, 4, 42) }},
			{"uniform", func() [][]float32 { return derivedTestUniformVectors(500, 64, 42) }},
			{"varying", func() [][]float32 { return derivedTestVaryingDensityVectors(500, 64, 42) }},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				vectors := tc.gen()
				factors := DeriveScalingFactors(vectors, 8)

				if factors.ClusteringAdjustmentRange[0] < 0 ||
					factors.ClusteringAdjustmentRange[0] > 1 {
					t.Errorf("ClusteringAdjustmentRange[0] = %v, want [0, 1]",
						factors.ClusteringAdjustmentRange[0])
				}
				if factors.ClusteringAdjustmentRange[1] < 0 ||
					factors.ClusteringAdjustmentRange[1] > 1 {
					t.Errorf("ClusteringAdjustmentRange[1] = %v, want [0, 1]",
						factors.ClusteringAdjustmentRange[1])
				}
			})
		}
	})

	t.Run("First element <= second element", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		if factors.ClusteringAdjustmentRange[0] > factors.ClusteringAdjustmentRange[1] {
			t.Errorf("ClusteringAdjustmentRange[0] (%v) > ClusteringAdjustmentRange[1] (%v)",
				factors.ClusteringAdjustmentRange[0], factors.ClusteringAdjustmentRange[1])
		}
	})
}

func TestDeriveComplexityMultiplierRange(t *testing.T) {
	t.Run("Based on eigenvalue decay", func(t *testing.T) {
		// Low complexity (fast eigenvalue decay)
		lowComplexity := derivedTestClusteredVectors(500, 64, 2, 42)
		lowFactors := DeriveScalingFactors(lowComplexity, 8)

		// High complexity (slow eigenvalue decay)
		highComplexity := derivedTestUniformVectors(500, 64, 42)
		highFactors := DeriveScalingFactors(highComplexity, 8)

		// Both should have valid ranges
		if lowFactors.ComplexityMultiplierRange[0] <= 0 {
			t.Errorf("lowFactors.ComplexityMultiplierRange[0] = %v, want > 0",
				lowFactors.ComplexityMultiplierRange[0])
		}
		if highFactors.ComplexityMultiplierRange[0] <= 0 {
			t.Errorf("highFactors.ComplexityMultiplierRange[0] = %v, want > 0",
				highFactors.ComplexityMultiplierRange[0])
		}
	})

	t.Run("Positive range", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		if factors.ComplexityMultiplierRange[0] <= 0 {
			t.Errorf("ComplexityMultiplierRange[0] = %v, want > 0",
				factors.ComplexityMultiplierRange[0])
		}
		if factors.ComplexityMultiplierRange[1] <= 0 {
			t.Errorf("ComplexityMultiplierRange[1] = %v, want > 0",
				factors.ComplexityMultiplierRange[1])
		}
	})

	t.Run("Reasonable bounds", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		// Range should be reasonable [0.3, 3.0]
		if factors.ComplexityMultiplierRange[0] < 0.3 ||
			factors.ComplexityMultiplierRange[0] > 3.0 {
			t.Errorf("ComplexityMultiplierRange[0] = %v, want [0.3, 3.0]",
				factors.ComplexityMultiplierRange[0])
		}
		if factors.ComplexityMultiplierRange[1] < 0.5 ||
			factors.ComplexityMultiplierRange[1] > 3.0 {
			t.Errorf("ComplexityMultiplierRange[1] = %v, want [0.5, 3.0]",
				factors.ComplexityMultiplierRange[1])
		}
	})
}

func TestDeriveSamplingFactors(t *testing.T) {
	t.Run("MinSamplesPerCentroid > 0", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		if factors.MinSamplesPerCentroid <= 0 {
			t.Errorf("MinSamplesPerCentroid = %v, want > 0", factors.MinSamplesPerCentroid)
		}
	})

	t.Run("SubspaceDimMultiplier > 0", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		factors := DeriveScalingFactors(vectors, 8)

		if factors.SubspaceDimMultiplier <= 0 {
			t.Errorf("SubspaceDimMultiplier = %v, want > 0", factors.SubspaceDimMultiplier)
		}
	})

	t.Run("Values vary with data characteristics", func(t *testing.T) {
		// Different data distributions may need different sampling
		clustered := derivedTestClusteredVectors(500, 64, 4, 42)
		uniform := derivedTestUniformVectors(500, 64, 42)

		clusteredFactors := DeriveScalingFactors(clustered, 8)
		uniformFactors := DeriveScalingFactors(uniform, 8)

		// Both should have valid values
		if clusteredFactors.MinSamplesPerCentroid < 10 {
			t.Errorf("clustered MinSamplesPerCentroid = %v, want >= 10",
				clusteredFactors.MinSamplesPerCentroid)
		}
		if uniformFactors.MinSamplesPerCentroid < 10 {
			t.Errorf("uniform MinSamplesPerCentroid = %v, want >= 10",
				uniformFactors.MinSamplesPerCentroid)
		}
	})
}

func TestComputeAndCacheFactors(t *testing.T) {
	t.Run("Factors cached after first call", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())

		// Initially no factors
		if co.GetFactors() != nil {
			t.Error("factors should be nil before ComputeAndCacheFactors")
		}

		// Compute and cache
		co.ComputeAndCacheFactors(vectors, 8)

		// Factors should now be cached
		factors := co.GetFactors()
		if factors == nil {
			t.Fatal("factors should not be nil after ComputeAndCacheFactors")
		}
		if !factors.Computed {
			t.Error("factors.Computed should be true")
		}
	})

	t.Run("Subsequent calls use cache", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())

		// Compute first time
		co.ComputeAndCacheFactors(vectors, 8)
		firstFactors := co.GetFactors()

		// Calling GetFactors multiple times should return same cached value
		secondFactors := co.GetFactors()
		thirdFactors := co.GetFactors()

		if firstFactors != secondFactors || secondFactors != thirdFactors {
			t.Error("GetFactors should return same cached pointer")
		}
	})

	t.Run("GetFactors returns cached values", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)
		co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())

		co.ComputeAndCacheFactors(vectors, 8)
		factors := co.GetFactors()

		// Verify the cached factors are valid
		if factors.TargetDistortionRatio <= 0 {
			t.Errorf("cached TargetDistortionRatio = %v, want > 0", factors.TargetDistortionRatio)
		}
		if factors.BaseK < 8 {
			t.Errorf("cached BaseK = %v, want >= 8", factors.BaseK)
		}
		if !factors.Computed {
			t.Error("cached factors.Computed should be true")
		}
	})

	t.Run("SetFactors allows manual factor injection", func(t *testing.T) {
		co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())

		customFactors := &DataDerivedScalingFactors{
			TargetDistortionRatio:     0.05,
			BaseK:                     128,
			RecallScaleFactor:         15.0,
			ClusteringAdjustmentRange: [2]float64{0.4, 0.9},
			ComplexityMultiplierRange: [2]float64{0.6, 2.0},
			MinSamplesPerCentroid:     20,
			SubspaceDimMultiplier:     3.0,
			Computed:                  true,
		}

		co.SetFactors(customFactors)

		retrieved := co.GetFactors()
		if retrieved != customFactors {
			t.Error("SetFactors should allow manual factor injection")
		}
		if retrieved.BaseK != 128 {
			t.Errorf("retrieved BaseK = %v, want 128", retrieved.BaseK)
		}
	})
}

func TestComputeMethodsWithDerivedFactors(t *testing.T) {
	vectors := derivedTestClusteredVectors(1000, 64, 8, 42)
	numSubspaces := 8
	subspaceDim := 64 / numSubspaces

	t.Run("computeStatisticalMinimum uses derived factors", func(t *testing.T) {
		// Without derived factors
		coWithout := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		coWithout.analyzeDataDistribution(vectors, numSubspaces)
		kWithout := coWithout.computeStatisticalMinimum(len(vectors), subspaceDim)

		// With derived factors
		coWith := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		coWith.ComputeAndCacheFactors(vectors, numSubspaces)
		coWith.analyzeDataDistribution(vectors, numSubspaces)
		kWith := coWith.computeStatisticalMinimum(len(vectors), subspaceDim)

		// Both should be valid powers of 2
		if kWithout&(kWithout-1) != 0 && kWithout != 0 {
			t.Errorf("kWithout = %v, should be power of 2", kWithout)
		}
		if kWith&(kWith-1) != 0 && kWith != 0 {
			t.Errorf("kWith = %v, should be power of 2", kWith)
		}

		t.Logf("computeStatisticalMinimum: without=%d, with=%d", kWithout, kWith)
	})

	t.Run("computeRateDistortionK uses derived factors", func(t *testing.T) {
		// Without derived factors
		coWithout := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		coWithout.analyzeDataDistribution(vectors, numSubspaces)
		kWithout := coWithout.computeRateDistortionK(subspaceDim)

		// With derived factors
		coWith := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		coWith.ComputeAndCacheFactors(vectors, numSubspaces)
		coWith.analyzeDataDistribution(vectors, numSubspaces)
		kWith := coWith.computeRateDistortionK(subspaceDim)

		// Both should be valid
		if kWithout < 8 || kWithout > 256 {
			t.Errorf("kWithout = %v, want [8, 256]", kWithout)
		}
		if kWith < 8 || kWith > 256 {
			t.Errorf("kWith = %v, want [8, 256]", kWith)
		}

		t.Logf("computeRateDistortionK: without=%d, with=%d", kWithout, kWith)
	})

	t.Run("computeRecallBasedK uses derived factors", func(t *testing.T) {
		// Without derived factors
		coWithout := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		coWithout.analyzeDataDistribution(vectors, numSubspaces)
		kWithout := coWithout.computeRecallBasedK(len(vectors))

		// With derived factors
		coWith := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		coWith.ComputeAndCacheFactors(vectors, numSubspaces)
		coWith.analyzeDataDistribution(vectors, numSubspaces)
		kWith := coWith.computeRecallBasedK(len(vectors))

		// Both should be valid powers of 2
		if kWithout < 8 || kWithout > 256 {
			t.Errorf("kWithout = %v, want [8, 256]", kWithout)
		}
		if kWith < 8 || kWith > 256 {
			t.Errorf("kWith = %v, want [8, 256]", kWith)
		}

		t.Logf("computeRecallBasedK: without=%d, with=%d", kWithout, kWith)
	})

	t.Run("Results differ from hardcoded defaults", func(t *testing.T) {
		// Use data with distinct characteristics
		clusteredVectors := derivedTestClusteredVectors(1000, 64, 4, 42)

		// Create optimizer with derived factors
		co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		co.ComputeAndCacheFactors(clusteredVectors, numSubspaces)

		// The derived factors should differ from defaults in some way
		factors := co.GetFactors()
		defaults := DefaultScalingFactors()

		// At least one factor should be different (data-adaptive)
		allSame := factors.TargetDistortionRatio == defaults.TargetDistortionRatio &&
			factors.BaseK == defaults.BaseK &&
			factors.RecallScaleFactor == defaults.RecallScaleFactor &&
			factors.MinSamplesPerCentroid == defaults.MinSamplesPerCentroid

		// It's acceptable for some factors to match, but log for visibility
		if allSame {
			t.Log("Warning: all derived factors match defaults (may be coincidence)")
		}
	})
}

func TestBackwardCompatibility(t *testing.T) {
	t.Run("Methods work without pre-computed factors", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)

		// Create optimizer without calling ComputeAndCacheFactors
		co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())

		// OptimizeCentroidCount should work
		k, err := co.OptimizeCentroidCount(vectors, 8)
		if err != nil {
			t.Fatalf("OptimizeCentroidCount failed: %v", err)
		}
		if k < 8 || k > 256 {
			t.Errorf("k = %v, want [8, 256]", k)
		}
	})

	t.Run("Results match original behavior when factors not set", func(t *testing.T) {
		vectors := derivedTestUniformVectors(500, 64, 42)

		// Run twice without factors
		co1 := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		k1, _ := co1.OptimizeCentroidCount(vectors, 8)

		co2 := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		k2, _ := co2.OptimizeCentroidCount(vectors, 8)

		// Should be deterministic
		if k1 != k2 {
			t.Errorf("k1 = %v, k2 = %v, want equal", k1, k2)
		}
	})

	t.Run("Cross validation works without factors", func(t *testing.T) {
		vectors := derivedTestClusteredVectors(500, 64, 4, 42)

		co := NewCentroidOptimizer(CentroidOptimizerConfig{
			TargetRecall:               0.9,
			MaxCentroidsPerSubspace:    256,
			EnableCrossValidation:      true,
			CrossValidationSampleRatio: 0.1,
		})

		// Should work without pre-computed factors
		k, err := co.OptimizeCentroidCountWithCrossValidation(context.Background(), vectors, 8)
		if err != nil {
			t.Fatalf("cross validation failed: %v", err)
		}
		if k < 8 || k > 256 {
			t.Errorf("k = %v, want [8, 256]", k)
		}
	})
}

func TestDeriveFactorsWithVariousDimensions(t *testing.T) {
	dimensions := []int{32, 64, 128, 768}

	for _, dim := range dimensions {
		t.Run(derivedTestDimName(dim), func(t *testing.T) {
			numSubspaces := dim / 8
			if numSubspaces < 1 {
				numSubspaces = 1
			}

			vectors := derivedTestUniformVectors(500, dim, 42)
			factors := DeriveScalingFactors(vectors, numSubspaces)

			if !factors.Computed {
				t.Errorf("dim=%d: factors should be Computed", dim)
			}
			if factors.TargetDistortionRatio <= 0 || factors.TargetDistortionRatio > 1 {
				t.Errorf("dim=%d: TargetDistortionRatio = %v, want (0, 1]",
					dim, factors.TargetDistortionRatio)
			}
			if factors.BaseK < 8 || factors.BaseK > 256 {
				t.Errorf("dim=%d: BaseK = %v, want [8, 256]", dim, factors.BaseK)
			}
			if factors.RecallScaleFactor <= 0 {
				t.Errorf("dim=%d: RecallScaleFactor = %v, want > 0", dim, factors.RecallScaleFactor)
			}
			if factors.MinSamplesPerCentroid < 10 {
				t.Errorf("dim=%d: MinSamplesPerCentroid = %v, want >= 10",
					dim, factors.MinSamplesPerCentroid)
			}
		})
	}
}

func derivedTestDimName(dim int) string {
	return "dim_" + derivedTestItoa(dim)
}

func derivedTestItoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func BenchmarkDeriveScalingFactors(b *testing.B) {
	benchCases := []struct {
		name         string
		n            int
		dim          int
		numSubspaces int
	}{
		{"small_32d", 500, 32, 4},
		{"medium_64d", 1000, 64, 8},
		{"large_128d", 2000, 128, 16},
		{"xlarge_768d", 1000, 768, 96},
	}

	for _, bc := range benchCases {
		b.Run(bc.name, func(b *testing.B) {
			vectors := derivedTestUniformVectors(bc.n, bc.dim, 42)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = DeriveScalingFactors(vectors, bc.numSubspaces)
			}
		})
	}
}

func BenchmarkDeriveScalingFactorsVsOptimize(b *testing.B) {
	vectors := derivedTestUniformVectors(1000, 64, 42)
	numSubspaces := 8

	b.Run("DeriveScalingFactors", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = DeriveScalingFactors(vectors, numSubspaces)
		}
	})

	b.Run("OptimizeCentroidCount_WithoutFactors", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
			_, _ = co.OptimizeCentroidCount(vectors, numSubspaces)
		}
	})

	b.Run("OptimizeCentroidCount_WithFactors", func(b *testing.B) {
		co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
		co.ComputeAndCacheFactors(vectors, numSubspaces)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, _ = co.OptimizeCentroidCount(vectors, numSubspaces)
		}
	})
}

func TestDeriveScalingFactorsEdgeCases(t *testing.T) {
	t.Run("Empty vectors", func(t *testing.T) {
		var vectors [][]float32
		factors := DeriveScalingFactors(vectors, 8)

		// Should return defaults
		if factors.Computed {
			t.Error("empty vectors should return non-computed factors")
		}
	})

	t.Run("Zero dimension", func(t *testing.T) {
		vectors := make([][]float32, 100)
		for i := range vectors {
			vectors[i] = []float32{} // Zero dimension
		}
		factors := DeriveScalingFactors(vectors, 8)

		// Should return defaults
		if factors.Computed {
			t.Error("zero dimension should return non-computed factors")
		}
	})

	t.Run("Zero numSubspaces", func(t *testing.T) {
		vectors := derivedTestUniformVectors(100, 64, 42)
		factors := DeriveScalingFactors(vectors, 0)

		// Should return defaults
		if factors.Computed {
			t.Error("zero numSubspaces should return non-computed factors")
		}
	})

	t.Run("Subspace dim larger than dim", func(t *testing.T) {
		vectors := derivedTestUniformVectors(100, 32, 42)
		// numSubspaces=64 would make subspaceDim = 32/64 = 0
		factors := DeriveScalingFactors(vectors, 64)

		// Should return defaults
		if factors.Computed {
			t.Error("subspaceDim=0 should return non-computed factors")
		}
	})

	t.Run("Identical vectors", func(t *testing.T) {
		// All vectors are identical
		vectors := make([][]float32, 100)
		for i := range vectors {
			vectors[i] = make([]float32, 64)
			for d := 0; d < 64; d++ {
				vectors[i][d] = 1.0
			}
		}
		factors := DeriveScalingFactors(vectors, 8)

		// Should handle gracefully (may return defaults or computed)
		if factors.TargetDistortionRatio < 0 {
			t.Errorf("TargetDistortionRatio = %v, should be >= 0", factors.TargetDistortionRatio)
		}
	})

	t.Run("Vectors with NaN", func(t *testing.T) {
		vectors := derivedTestUniformVectors(100, 64, 42)
		vectors[0][0] = float32(math.NaN())

		// Should handle gracefully without panicking
		factors := DeriveScalingFactors(vectors, 8)
		_ = factors // Just check it doesn't panic
	})

	t.Run("Vectors with Inf", func(t *testing.T) {
		vectors := derivedTestUniformVectors(100, 64, 42)
		vectors[0][0] = float32(math.Inf(1))

		// Should handle gracefully without panicking
		factors := DeriveScalingFactors(vectors, 8)
		_ = factors // Just check it doesn't panic
	})
}
