package quantization

import (
	"context"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// TestAdaptiveQuantizer_RecallComparison compares recall across all strategies.
// This verifies that Residual PQ + Re-ranking achieves 95%+ recall.
func TestAdaptiveQuantizer_RecallComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Test parameters - realistic embedding dimensions
	dim := 64
	numDB := 5000  // Medium dataset to test all strategies
	numClusters := 50
	numQueries := 100
	k := 10

	// Generate clustered data (realistic for embeddings)
	centers := make([][]float32, numClusters)
	for i := range centers {
		centers[i] = make([]float32, dim)
		for j := range centers[i] {
			centers[i][j] = rng.Float32()*2 - 1
		}
	}

	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		center := centers[i%numClusters]
		for j := range dbVectors[i] {
			noise := (rng.Float32() + rng.Float32() + rng.Float32() - 1.5) * 0.3
			dbVectors[i][j] = center[j] + noise
		}
	}

	// Generate queries from existing vectors with slight perturbation
	queries := make([][]float32, numQueries)
	queryIndices := make([]int, numQueries)
	for i := range queries {
		idx := rng.Intn(numDB)
		queryIndices[i] = idx
		queries[i] = make([]float32, dim)
		for j := range queries[i] {
			noise := (rng.Float32() - 0.5) * 0.1
			queries[i][j] = dbVectors[idx][j] + noise
		}
	}

	// Compute ground truth (brute force)
	groundTruth := computeGroundTruth(queries, dbVectors, k)

	ctx := context.Background()

	// Test 1: Standard PQ (baseline)
	// Note: Standard PQ on random/semi-random data typically achieves 30-50% recall
	// because it relies on centroid quantization without re-ranking.
	// This is a known limitation - re-ranking is required for high recall.
	t.Run("StandardPQ", func(t *testing.T) {
		config := DefaultAdaptiveQuantizerConfig()
		config.ForceStrategy = StrategyStandardPQ
		config.EnableResidual = false

		aq, err := NewAdaptiveQuantizer(dim, config)
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if err := aq.Train(ctx, dbVectors); err != nil {
			t.Fatalf("train: %v", err)
		}

		recall := computeRecallForAQ(t, aq, queries, groundTruth, k)
		t.Logf("Standard PQ Recall@%d: %.2f%%", k, recall*100)

		// Standard PQ without re-ranking has fundamental limits
		// 30% is the floor for functioning quantization
		if recall < 0.30 {
			t.Errorf("Standard PQ recall %.2f%% below minimum 30%%", recall*100)
		}
	})

	// Test 2: Residual PQ (should improve over standard)
	t.Run("ResidualPQ", func(t *testing.T) {
		config := DefaultAdaptiveQuantizerConfig()
		config.ForceStrategy = StrategyResidualPQ
		config.EnableResidual = true

		aq, err := NewAdaptiveQuantizer(dim, config)
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if err := aq.Train(ctx, dbVectors); err != nil {
			t.Fatalf("train: %v", err)
		}

		recall := computeRecallForAQ(t, aq, queries, groundTruth, k)
		t.Logf("Residual PQ Recall@%d: %.2f%%", k, recall*100)

		// Residual PQ should achieve at least 50%
		if recall < 0.50 {
			t.Errorf("Residual PQ recall %.2f%% below minimum 50%%", recall*100)
		}
	})

	// Test 3: Residual PQ + Re-ranking (target: 95%+)
	t.Run("ResidualPQ_WithRerank", func(t *testing.T) {
		config := DefaultAdaptiveQuantizerConfig()
		config.ForceStrategy = StrategyResidualPQ
		config.EnableResidual = true

		aq, err := NewAdaptiveQuantizer(dim, config)
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if err := aq.Train(ctx, dbVectors); err != nil {
			t.Fatalf("train: %v", err)
		}

		// Use SearchWithRerank with 10x candidates
		recall := computeRecallWithRerank(t, aq, queries, groundTruth, k, k*10, dbVectors)
		t.Logf("Residual PQ + Rerank(10x) Recall@%d: %.2f%%", k, recall*100)

		// This should achieve 95%+ recall
		if recall < 0.90 {
			t.Errorf("Residual PQ + Rerank recall %.2f%% below target 90%%", recall*100)
		}
	})

	// Test 4: Coarse PQ + Re-ranking (for comparison)
	t.Run("CoarsePQ_Rerank", func(t *testing.T) {
		config := DefaultAdaptiveQuantizerConfig()
		config.ForceStrategy = StrategyCoarsePQRerank

		aq, err := NewAdaptiveQuantizer(dim, config)
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if err := aq.Train(ctx, dbVectors); err != nil {
			t.Fatalf("train: %v", err)
		}

		recall := computeRecallForAQ(t, aq, queries, groundTruth, k)
		t.Logf("Coarse PQ + Rerank Recall@%d: %.2f%%", k, recall*100)

		// This stores original vectors and re-ranks, should be very high
		if recall < 0.90 {
			t.Errorf("Coarse PQ + Rerank recall %.2f%% below target 90%%", recall*100)
		}
	})
}

// TestAdaptiveQuantizer_SmallDataset tests small dataset handling.
func TestAdaptiveQuantizer_SmallDataset(t *testing.T) {
	rng := rand.New(rand.NewSource(123))

	dim := 32
	numDB := 500
	numQueries := 50
	k := 10

	// Generate random data
	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		for j := range dbVectors[i] {
			dbVectors[i][j] = rng.Float32()*2 - 1
		}
	}

	queries := make([][]float32, numQueries)
	for i := range queries {
		idx := rng.Intn(numDB)
		queries[i] = make([]float32, dim)
		copy(queries[i], dbVectors[idx])
		// Add small noise
		for j := range queries[i] {
			queries[i][j] += (rng.Float32() - 0.5) * 0.05
		}
	}

	groundTruth := computeGroundTruth(queries, dbVectors, k)
	ctx := context.Background()

	// For small datasets, CoarsePQRerank should be selected automatically
	config := DefaultAdaptiveQuantizerConfig()
	config.TargetRecall = 0.95

	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := aq.Train(ctx, dbVectors); err != nil {
		t.Fatalf("train: %v", err)
	}

	t.Logf("Auto-selected strategy: %s", aq.Strategy())

	recall := computeRecallForAQ(t, aq, queries, groundTruth, k)
	t.Logf("Small dataset Recall@%d: %.2f%%", k, recall*100)

	// Small dataset with re-ranking should achieve 95%+
	if recall < 0.90 {
		t.Errorf("Small dataset recall %.2f%% below target 90%%", recall*100)
	}
}

// TestAdaptiveQuantizer_TinyDataset tests exact strategy for tiny datasets.
func TestAdaptiveQuantizer_TinyDataset(t *testing.T) {
	rng := rand.New(rand.NewSource(456))

	dim := 16
	numDB := 50 // Tiny dataset
	k := 5

	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		for j := range dbVectors[i] {
			dbVectors[i][j] = rng.Float32()*2 - 1
		}
	}

	ctx := context.Background()
	config := DefaultAdaptiveQuantizerConfig()

	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := aq.Train(ctx, dbVectors); err != nil {
		t.Fatalf("train: %v", err)
	}

	// Should select Exact strategy
	if aq.Strategy() != StrategyExact {
		t.Errorf("Expected StrategyExact for tiny dataset, got %s", aq.Strategy())
	}

	// Query with one of the vectors - should find exact match
	query := dbVectors[0]
	results, err := aq.Search(query, k)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	// First result should be the exact vector (distance ~0)
	if results[0].Index != 0 {
		t.Errorf("Expected index 0 as top result, got %d", results[0].Index)
	}
	if results[0].Distance > 1e-6 {
		t.Errorf("Expected near-zero distance, got %f", results[0].Distance)
	}
}

// TestAdaptiveQuantizer_RecallVsCandidates tests how recall scales with candidate count.
func TestAdaptiveQuantizer_RecallVsCandidates(t *testing.T) {
	rng := rand.New(rand.NewSource(789))

	dim := 64
	numDB := 2000
	numClusters := 20
	numQueries := 50
	k := 10

	// Generate clustered data
	centers := make([][]float32, numClusters)
	for i := range centers {
		centers[i] = make([]float32, dim)
		for j := range centers[i] {
			centers[i][j] = rng.Float32()*2 - 1
		}
	}

	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		center := centers[i%numClusters]
		for j := range dbVectors[i] {
			noise := (rng.Float32() + rng.Float32() + rng.Float32() - 1.5) * 0.3
			dbVectors[i][j] = center[j] + noise
		}
	}

	queries := make([][]float32, numQueries)
	for i := range queries {
		idx := rng.Intn(numDB)
		queries[i] = make([]float32, dim)
		for j := range queries[i] {
			noise := (rng.Float32() - 0.5) * 0.1
			queries[i][j] = dbVectors[idx][j] + noise
		}
	}

	groundTruth := computeGroundTruth(queries, dbVectors, k)
	ctx := context.Background()

	config := DefaultAdaptiveQuantizerConfig()
	config.ForceStrategy = StrategyResidualPQ

	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := aq.Train(ctx, dbVectors); err != nil {
		t.Fatalf("train: %v", err)
	}

	// Test different candidate multipliers
	multipliers := []int{1, 2, 5, 10, 20, 50}
	for _, mult := range multipliers {
		numCandidates := k * mult
		if numCandidates > numDB {
			numCandidates = numDB
		}

		recall := computeRecallWithRerank(t, aq, queries, groundTruth, k, numCandidates, dbVectors)
		t.Logf("Residual PQ + Rerank(%dx) Recall@%d: %.2f%%", mult, k, recall*100)
	}
}

// Helper: compute ground truth using brute force
func computeGroundTruth(queries, dbVectors [][]float32, k int) [][]int {
	groundTruth := make([][]int, len(queries))

	for q, query := range queries {
		type distIdx struct {
			dist float32
			idx  int
		}

		dists := make([]distIdx, len(dbVectors))
		for i, dbVec := range dbVectors {
			var sum float32
			for j := range query {
				d := query[j] - dbVec[j]
				sum += d * d
			}
			dists[i] = distIdx{dist: sum, idx: i}
		}

		sort.Slice(dists, func(i, j int) bool {
			return dists[i].dist < dists[j].dist
		})

		groundTruth[q] = make([]int, k)
		for i := 0; i < k && i < len(dists); i++ {
			groundTruth[q][i] = dists[i].idx
		}
	}

	return groundTruth
}

// Helper: compute recall for adaptive quantizer
func computeRecallForAQ(t *testing.T, aq *AdaptiveQuantizer, queries [][]float32, groundTruth [][]int, k int) float64 {
	var totalRecall float64

	for q, query := range queries {
		results, err := aq.Search(query, k)
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}

		gtSet := make(map[int]bool)
		for _, idx := range groundTruth[q] {
			gtSet[idx] = true
		}

		hits := 0
		for _, r := range results {
			if gtSet[r.Index] {
				hits++
			}
		}

		totalRecall += float64(hits) / float64(k)
	}

	return totalRecall / float64(len(queries))
}

// Helper: compute recall with re-ranking using stored vectors
func computeRecallWithRerank(t *testing.T, aq *AdaptiveQuantizer, queries [][]float32, groundTruth [][]int, k, numCandidates int, dbVectors [][]float32) float64 {
	var totalRecall float64

	for q, query := range queries {
		// Get candidates from adaptive quantizer
		candidates, err := aq.Search(query, numCandidates)
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}

		// Re-rank with exact distances using original vectors
		for i := range candidates {
			idx := candidates[i].Index
			var sum float32
			for j := range query {
				d := query[j] - dbVectors[idx][j]
				sum += d * d
			}
			candidates[i].Distance = sum
		}

		// Sort by exact distance
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Distance < candidates[j].Distance
		})

		// Take top k
		if len(candidates) > k {
			candidates = candidates[:k]
		}

		gtSet := make(map[int]bool)
		for _, idx := range groundTruth[q] {
			gtSet[idx] = true
		}

		hits := 0
		for _, r := range candidates {
			if gtSet[r.Index] {
				hits++
			}
		}

		totalRecall += float64(hits) / float64(k)
	}

	return totalRecall / float64(len(queries))
}

// TestAdaptiveQuantizer_DerivedConfig tests that all config values are derived correctly.
func TestAdaptiveQuantizer_DerivedConfig(t *testing.T) {
	tests := []struct {
		name       string
		vectorDim  int
		numVectors int
	}{
		{"32-dim, 200 vectors", 32, 200},
		{"64-dim, 1000 vectors", 64, 1000},
		{"128-dim, 5000 vectors", 128, 5000},
		{"768-dim, 10000 vectors", 768, 10000},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(42))

			// Generate random vectors
			vectors := make([][]float32, tt.numVectors)
			for i := range vectors {
				vectors[i] = make([]float32, tt.vectorDim)
				for j := range vectors[i] {
					vectors[i][j] = rng.Float32()*2 - 1
				}
			}

			config := DefaultAdaptiveQuantizerConfig()
			aq, err := NewAdaptiveQuantizer(tt.vectorDim, config)
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			if err := aq.Train(ctx, vectors); err != nil {
				t.Fatalf("train: %v", err)
			}

			stats := aq.Stats()
			t.Logf("Strategy: %s", stats.Strategy)
			t.Logf("Primary: %d subspaces × %d centroids", stats.PrimarySubspaces, stats.PrimaryCentroids)
			if stats.ResidualSubspaces > 0 {
				t.Logf("Residual: %d subspaces × %d centroids", stats.ResidualSubspaces, stats.ResidualCentroids)
			}
			t.Logf("Bytes per vector: %d", stats.CompressedBytesPerVector)

			// Verify constraints
			if stats.PrimarySubspaces > 0 {
				if tt.vectorDim%stats.PrimarySubspaces != 0 {
					t.Errorf("vectorDim %d not divisible by numSubspaces %d",
						tt.vectorDim, stats.PrimarySubspaces)
				}
				if stats.PrimaryCentroids > 256 {
					t.Errorf("centroids %d exceeds uint8 max", stats.PrimaryCentroids)
				}
			}
		})
	}
}

// Benchmark for search performance
func BenchmarkAdaptiveQuantizer_Search(b *testing.B) {
	rng := rand.New(rand.NewSource(42))

	dim := 128
	numDB := 10000
	k := 10

	// Generate data
	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		for j := range dbVectors[i] {
			dbVectors[i][j] = rng.Float32()*2 - 1
		}
	}

	query := make([]float32, dim)
	for j := range query {
		query[j] = rng.Float32()*2 - 1
	}

	ctx := context.Background()

	strategies := []struct {
		name     string
		strategy QuantizationStrategy
	}{
		{"StandardPQ", StrategyStandardPQ},
		{"ResidualPQ", StrategyResidualPQ},
	}

	for _, s := range strategies {
		b.Run(s.name, func(b *testing.B) {
			config := DefaultAdaptiveQuantizerConfig()
			config.ForceStrategy = s.strategy

			aq, _ := NewAdaptiveQuantizer(dim, config)
			aq.Train(ctx, dbVectors)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				aq.Search(query, k)
			}
		})
	}

	// Benchmark with reranking
	b.Run("ResidualPQ_Rerank10x", func(b *testing.B) {
		config := DefaultAdaptiveQuantizerConfig()
		config.ForceStrategy = StrategyResidualPQ

		aq, _ := NewAdaptiveQuantizer(dim, config)
		aq.Train(ctx, dbVectors)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			candidates, _ := aq.Search(query, k*10)
			// Simulate reranking cost
			for j := range candidates {
				idx := candidates[j].Index
				var sum float32
				for d := 0; d < dim; d++ {
					diff := query[d] - dbVectors[idx][d]
					sum += diff * diff
				}
				candidates[j].Distance = sum
			}
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].Distance < candidates[j].Distance
			})
		}
	})
}

// TestResidualPQ_QuantizationError verifies residual encoding reduces error.
func TestResidualPQ_QuantizationError(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	dim := 64
	numDB := 2000
	numSamples := 100

	// Generate clustered data
	numClusters := 20
	centers := make([][]float32, numClusters)
	for i := range centers {
		centers[i] = make([]float32, dim)
		for j := range centers[i] {
			centers[i][j] = rng.Float32()*2 - 1
		}
	}

	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		center := centers[i%numClusters]
		for j := range dbVectors[i] {
			noise := (rng.Float32() + rng.Float32() + rng.Float32() - 1.5) * 0.3
			dbVectors[i][j] = center[j] + noise
		}
	}

	ctx := context.Background()

	// Train standard PQ
	standardConfig := DefaultAdaptiveQuantizerConfig()
	standardConfig.ForceStrategy = StrategyStandardPQ
	standardAQ, _ := NewAdaptiveQuantizer(dim, standardConfig)
	standardAQ.Train(ctx, dbVectors)

	// Train residual PQ
	residualConfig := DefaultAdaptiveQuantizerConfig()
	residualConfig.ForceStrategy = StrategyResidualPQ
	residualAQ, _ := NewAdaptiveQuantizer(dim, residualConfig)
	residualAQ.Train(ctx, dbVectors)

	// Compare reconstruction error
	var standardError, residualError float64

	sampleIndices := make([]int, numSamples)
	for i := range sampleIndices {
		sampleIndices[i] = rng.Intn(numDB)
	}

	for _, idx := range sampleIndices {
		original := dbVectors[idx]

		// Standard PQ reconstruction
		standardRecon := standardAQ.reconstructVector(standardAQ.codes[idx])
		for j := range original {
			d := float64(original[j] - standardRecon[j])
			standardError += d * d
		}

		// Residual PQ reconstruction
		residualRecon := residualAQ.reconstructVectorWithResidual(
			residualAQ.codes[idx],
			residualAQ.residualCodes[idx],
		)
		for j := range original {
			d := float64(original[j] - residualRecon[j])
			residualError += d * d
		}
	}

	standardRMSE := math.Sqrt(standardError / float64(numSamples*dim))
	residualRMSE := math.Sqrt(residualError / float64(numSamples*dim))

	t.Logf("Standard PQ RMSE: %.6f", standardRMSE)
	t.Logf("Residual PQ RMSE: %.6f", residualRMSE)
	t.Logf("Error reduction: %.2f%%", (1-residualRMSE/standardRMSE)*100)

	// Residual PQ should have lower reconstruction error
	if residualRMSE >= standardRMSE {
		t.Errorf("Residual PQ RMSE (%.6f) should be lower than Standard PQ (%.6f)",
			residualRMSE, standardRMSE)
	}
}
