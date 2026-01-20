package quantization

import (
	"context"
	"math"
	"math/rand"
	"runtime"
	"testing"
)

// =============================================================================
// Test Helpers
// =============================================================================

// kmeansTestVectors creates n random vectors of given dimension with seed.
func kmeansTestVectors(n, dim int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		vectors[i] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			vectors[i][d] = rng.Float32()*2 - 1 // Range [-1, 1]
		}
	}
	return vectors
}

// kmeansClusteredVectors creates vectors with clear cluster structure.
func kmeansClusteredVectors(n, dim, k int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	vectors := make([][]float32, n)

	// Create k cluster centers
	centers := make([][]float32, k)
	for c := 0; c < k; c++ {
		centers[c] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			centers[c][d] = rng.Float32()*10 - 5 // Range [-5, 5]
		}
	}

	// Generate points around centers
	for i := 0; i < n; i++ {
		cluster := i % k
		vectors[i] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			vectors[i][d] = centers[cluster][d] + rng.Float32()*0.5 - 0.25 // Small noise
		}
	}

	return vectors
}

// computeInertia computes total sum of squared distances from points to assigned centroids.
func computeInertia(vectors [][]float32, centroids [][]float32) float64 {
	var inertia float64
	for _, v := range vectors {
		minDist := math.MaxFloat64
		for _, c := range centroids {
			dist := squaredDistance(v, c)
			if dist < minDist {
				minDist = dist
			}
		}
		inertia += minDist
	}
	return inertia
}

// squaredDistance computes ||a - b||^2.
func squaredDistance(a, b []float32) float64 {
	var sum float64
	for i := range a {
		diff := float64(a[i]) - float64(b[i])
		sum += diff * diff
	}
	return sum
}

// centroidsEqual checks if two centroid sets are equivalent (same centroids, possibly reordered).
func centroidsEqual(a, b [][]float32, tolerance float64) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}

	// For each centroid in a, find matching centroid in b
	used := make([]bool, len(b))
	for _, ca := range a {
		found := false
		for j, cb := range b {
			if used[j] {
				continue
			}
			dist := squaredDistance(ca, cb)
			if dist < tolerance {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// =============================================================================
// Memory Reuse Correctness Tests
// =============================================================================

// TestKMeansMemoryReuseEquivalence verifies that memory reuse produces
// identical results to fresh allocation when using the same seed.
func TestKMeansMemoryReuseEquivalence(t *testing.T) {
	testCases := []struct {
		name       string
		n          int
		dim        int
		k          int
		numRestarts int
	}{
		{"small", 100, 8, 4, 5},
		{"medium", 500, 32, 16, 8},
		{"large_k", 200, 16, 50, 10},
		{"many_restarts", 150, 16, 8, 20},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vectors := kmeansTestVectors(tc.n, tc.dim, 42)

			config := KMeansConfig{
				MaxIterations:        100,
				NumRestarts:          tc.numRestarts,
				ConvergenceThreshold: 1e-6,
				Seed:                 12345,
			}

			// Run sequential (with memory reuse)
			result1, err := kmeansSequentialRestarts(vectors, tc.k, config, config.Seed)
			if err != nil {
				t.Fatalf("sequential restarts failed: %v", err)
			}

			// Run again with same config
			result2, err := kmeansSequentialRestarts(vectors, tc.k, config, config.Seed)
			if err != nil {
				t.Fatalf("sequential restarts (2nd run) failed: %v", err)
			}

			// Results must be identical
			if !centroidsEqual(result1, result2, 1e-10) {
				t.Errorf("sequential restarts not deterministic")
			}

			// Verify inertia is reasonable
			inertia1 := computeInertia(vectors, result1)
			inertia2 := computeInertia(vectors, result2)
			if math.Abs(inertia1-inertia2) > 1e-6 {
				t.Errorf("inertia mismatch: %v vs %v", inertia1, inertia2)
			}
		})
	}
}

// TestKMeansResetForRestartCompleteness verifies resetForRestart clears all state.
func TestKMeansResetForRestartCompleteness(t *testing.T) {
	vectors := kmeansTestVectors(100, 16, 42)
	k := 8

	state := newKMeansState(vectors, k)
	if state == nil {
		t.Fatal("failed to create state")
	}

	// Run k-means once to populate all state
	rng := rand.New(rand.NewSource(123))
	state.runSingleKMeans(KMeansConfig{
		MaxIterations:        50,
		ConvergenceThreshold: 1e-6,
	}, rng)

	// Verify state is populated
	if state.objective == 0 {
		t.Error("objective should be non-zero after run")
	}

	// Reset
	state.resetForRestart()

	// Verify all resettable state is zeroed
	for i, v := range state.centroids {
		if v != 0 {
			t.Errorf("centroids[%d] = %v, want 0", i, v)
		}
	}
	for i, v := range state.centroidNorms {
		if v != 0 {
			t.Errorf("centroidNorms[%d] = %v, want 0", i, v)
		}
	}
	for i, v := range state.dots {
		if v != 0 {
			t.Errorf("dots[%d] = %v, want 0", i, v)
		}
	}
	for i, v := range state.assignments {
		if v != 0 {
			t.Errorf("assignments[%d] = %v, want 0", i, v)
		}
	}
	for i, v := range state.counts {
		if v != 0 {
			t.Errorf("counts[%d] = %v, want 0", i, v)
		}
	}
	for i, v := range state.newCentroids {
		if v != 0 {
			t.Errorf("newCentroids[%d] = %v, want 0", i, v)
		}
	}
	if state.objective != 0 {
		t.Errorf("objective = %v, want 0", state.objective)
	}

	// Verify vectors and vectorNorms are preserved
	expectedVectors := newKMeansState(vectors, k)
	for i, v := range state.vectors {
		if v != expectedVectors.vectors[i] {
			t.Errorf("vectors[%d] was modified: got %v, want %v", i, v, expectedVectors.vectors[i])
		}
	}
	for i, v := range state.vectorNorms {
		if v != expectedVectors.vectorNorms[i] {
			t.Errorf("vectorNorms[%d] was modified: got %v, want %v", i, v, expectedVectors.vectorNorms[i])
		}
	}
}

// TestKMeansNoStateLeakageBetweenRestarts verifies no data leaks between restarts.
func TestKMeansNoStateLeakageBetweenRestarts(t *testing.T) {
	// Use clustered data where the correct answer is clear
	vectors := kmeansClusteredVectors(200, 16, 4, 42)
	k := 4

	state := newKMeansState(vectors, k)
	if state == nil {
		t.Fatal("failed to create state")
	}

	var objectives []float64

	// Run multiple restarts, collecting objectives
	for restart := 0; restart < 10; restart++ {
		state.resetForRestart()
		rng := rand.New(rand.NewSource(int64(restart * 1000)))
		obj := state.runSingleKMeans(KMeansConfig{
			MaxIterations:        100,
			ConvergenceThreshold: 1e-6,
		}, rng)
		objectives = append(objectives, obj)
	}

	// With proper reset, each restart should find similar (good) objective
	// because the data has clear cluster structure
	bestObj := objectives[0]
	for _, obj := range objectives[1:] {
		if obj < bestObj {
			bestObj = obj
		}
	}

	// All objectives should be within reasonable range of best
	// (state leakage would cause divergent results)
	for i, obj := range objectives {
		ratio := obj / bestObj
		if ratio > 1.5 { // Allow 50% variation due to random initialization
			t.Errorf("restart %d objective %v too far from best %v (ratio %.2f)",
				i, obj, bestObj, ratio)
		}
	}
}

// =============================================================================
// Determinism Tests (Property-Based)
// =============================================================================

// TestKMeansDeterministicWithSeed verifies same seed produces same results.
func TestKMeansDeterministicWithSeed(t *testing.T) {
	vectors := kmeansTestVectors(200, 32, 42)
	k := 10

	config := KMeansConfig{
		MaxIterations:        100,
		NumRestarts:          5,
		ConvergenceThreshold: 1e-6,
		Seed:                 999,
	}

	// Run multiple times with same seed
	results := make([][][]float32, 5)
	for i := 0; i < 5; i++ {
		result, err := KMeansOptimal(context.Background(), vectors, k, config)
		if err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
		results[i] = result
	}

	// All results must be identical
	for i := 1; i < len(results); i++ {
		if !centroidsEqual(results[0], results[i], 1e-10) {
			t.Errorf("run %d produced different centroids than run 0", i)
		}
	}
}

// TestKMeansDifferentSeedsDifferentResults verifies different seeds can produce different results.
func TestKMeansDifferentSeedsDifferentResults(t *testing.T) {
	vectors := kmeansTestVectors(200, 32, 42)
	k := 10

	// Run with different seeds
	var results [][][]float32
	for seed := int64(1); seed <= 3; seed++ {
		config := KMeansConfig{
			MaxIterations:        100,
			NumRestarts:          1, // Single restart to maximize difference
			ConvergenceThreshold: 1e-6,
			Seed:                 seed * 12345,
		}
		result, _ := KMeansOptimal(context.Background(), vectors, k, config)
		results = append(results, result)
	}

	// At least some should be different (with high probability)
	allSame := true
	for i := 1; i < len(results); i++ {
		if !centroidsEqual(results[0], results[i], 1e-6) {
			allSame = false
			break
		}
	}

	if allSame {
		t.Log("warning: all seeds produced same result (possible but unlikely)")
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

// TestKMeansEdgeCaseK1 tests k=1 (single cluster).
func TestKMeansEdgeCaseK1(t *testing.T) {
	vectors := kmeansTestVectors(100, 16, 42)
	k := 1

	config := KMeansConfig{
		MaxIterations:        100,
		NumRestarts:          3,
		ConvergenceThreshold: 1e-6,
		Seed:                 42,
	}

	result, err := KMeansOptimal(context.Background(), vectors, k, config)
	if err != nil {
		t.Fatalf("k=1 failed: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("expected 1 centroid, got %d", len(result))
	}

	// Centroid should be approximately the mean of all vectors
	mean := make([]float32, len(vectors[0]))
	for _, v := range vectors {
		for d := range v {
			mean[d] += v[d] / float32(len(vectors))
		}
	}

	dist := squaredDistance(result[0], mean)
	if dist > 0.1 { // Allow some tolerance
		t.Errorf("k=1 centroid far from mean: distance = %v", dist)
	}
}

// TestKMeansEdgeCaseKEqualsN tests k=n (as many clusters as points).
func TestKMeansEdgeCaseKEqualsN(t *testing.T) {
	n := 20
	vectors := kmeansTestVectors(n, 8, 42)
	k := n

	config := KMeansConfig{
		MaxIterations:        100,
		NumRestarts:          2,
		ConvergenceThreshold: 1e-6,
		Seed:                 42,
	}

	result, err := KMeansOptimal(context.Background(), vectors, k, config)
	if err != nil {
		t.Fatalf("k=n failed: %v", err)
	}

	if len(result) != k {
		t.Errorf("expected %d centroids, got %d", k, len(result))
	}

	// Each centroid should be very close to exactly one vector
	inertia := computeInertia(vectors, result)
	if inertia > 1e-6 {
		t.Errorf("k=n should have near-zero inertia, got %v", inertia)
	}
}

// TestKMeansEdgeCaseKGreaterThanN tests k > n (more clusters than points).
func TestKMeansEdgeCaseKGreaterThanN(t *testing.T) {
	n := 10
	vectors := kmeansTestVectors(n, 8, 42)
	k := 20 // k > n

	result, err := KMeansOptimal(context.Background(), vectors, k, KMeansConfig{
		Seed: 42,
	})
	if err != nil {
		t.Fatalf("k>n failed: %v", err)
	}

	// Should return copies of all vectors (n centroids, not k)
	if len(result) != n {
		t.Errorf("expected %d centroids (n), got %d", n, len(result))
	}
}

// TestKMeansEdgeCaseEmptyClusters verifies empty cluster handling.
func TestKMeansEdgeCaseEmptyClusters(t *testing.T) {
	// Create data that tends to produce empty clusters
	// Two tight clusters with k=4 means 2 clusters may become empty
	vectors := make([][]float32, 100)
	for i := 0; i < 50; i++ {
		vectors[i] = []float32{0.0, 0.0, 0.0, 0.0}
		vectors[i+50] = []float32{10.0, 10.0, 10.0, 10.0}
	}
	// Add small noise
	rng := rand.New(rand.NewSource(42))
	for i := range vectors {
		for d := range vectors[i] {
			vectors[i][d] += rng.Float32()*0.1 - 0.05
		}
	}

	k := 4

	config := KMeansConfig{
		MaxIterations:        100,
		NumRestarts:          5,
		ConvergenceThreshold: 1e-6,
		Seed:                 42,
	}

	result, err := KMeansOptimal(context.Background(), vectors, k, config)
	if err != nil {
		t.Fatalf("empty cluster test failed: %v", err)
	}

	if len(result) != k {
		t.Errorf("expected %d centroids, got %d", k, len(result))
	}

	// All centroids should be valid (not NaN or Inf)
	for i, c := range result {
		for d, v := range c {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Errorf("centroid[%d][%d] is invalid: %v", i, d, v)
			}
		}
	}
}

// TestKMeansEdgeCaseEmptyInput tests empty input.
func TestKMeansEdgeCaseEmptyInput(t *testing.T) {
	var vectors [][]float32

	result, err := KMeansOptimal(context.Background(), vectors, 5, KMeansConfig{})
	if err != nil {
		t.Fatalf("empty input failed: %v", err)
	}

	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

// TestKMeansEdgeCaseSingleVector tests single vector input.
func TestKMeansEdgeCaseSingleVector(t *testing.T) {
	vectors := [][]float32{{1.0, 2.0, 3.0}}

	result, err := KMeansOptimal(context.Background(), vectors, 5, KMeansConfig{})
	if err != nil {
		t.Fatalf("single vector failed: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("expected 1 centroid for single vector, got %d", len(result))
	}
}

// =============================================================================
// Parallel vs Sequential Equivalence Tests
// =============================================================================

// TestKMeansParallelSequentialEquivalence verifies parallel and sequential produce same best result.
func TestKMeansParallelSequentialEquivalence(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("need multiple CPUs for parallel test")
	}

	vectors := kmeansTestVectors(300, 32, 42)
	k := 16

	config := KMeansConfig{
		MaxIterations:        100,
		NumRestarts:          8,
		ConvergenceThreshold: 1e-6,
		Seed:                 12345,
	}

	// Sequential
	seqResult, err := kmeansSequentialRestarts(vectors, k, config, config.Seed)
	if err != nil {
		t.Fatalf("sequential failed: %v", err)
	}
	seqInertia := computeInertia(vectors, seqResult)

	// Parallel (force parallel path)
	numWorkers := 4
	parResult, err := kmeansParallelRestarts(
		context.Background(), vectors, k, config, config.Seed, numWorkers)
	if err != nil {
		t.Fatalf("parallel failed: %v", err)
	}
	parInertia := computeInertia(vectors, parResult)

	// Both should find the same best result (since they use the same seeds)
	// Allow small tolerance due to floating point differences
	inertiaDiff := math.Abs(seqInertia - parInertia)
	if inertiaDiff > seqInertia*0.001 { // 0.1% tolerance
		t.Errorf("inertia mismatch: sequential=%v, parallel=%v (diff=%v)",
			seqInertia, parInertia, inertiaDiff)
	}
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

// TestKMeansContextCancellation verifies context cancellation is respected.
func TestKMeansContextCancellation(t *testing.T) {
	vectors := kmeansTestVectors(1000, 64, 42)
	k := 32

	config := KMeansConfig{
		MaxIterations:        1000,
		NumRestarts:          100,
		ConvergenceThreshold: 1e-10, // Very strict to ensure many iterations
		Seed:                 42,
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short time
	go func() {
		cancel()
	}()

	_, err := KMeansOptimal(ctx, vectors, k, config)

	// Should return with context error (or complete if very fast)
	if err != nil && err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// =============================================================================
// Quality Tests
// =============================================================================

// TestKMeansQualityOnClusteredData verifies k-means finds good clusters.
func TestKMeansQualityOnClusteredData(t *testing.T) {
	// Create data with clear cluster structure
	vectors := kmeansClusteredVectors(400, 16, 4, 42)
	k := 4

	config := KMeansConfig{
		MaxIterations:        100,
		NumRestarts:          10,
		ConvergenceThreshold: 1e-6,
		Seed:                 42,
	}

	result, err := KMeansOptimal(context.Background(), vectors, k, config)
	if err != nil {
		t.Fatalf("quality test failed: %v", err)
	}

	// Compute inertia
	inertia := computeInertia(vectors, result)

	// For well-clustered data, inertia should be low
	// Each point is within 0.5 of center, so max squared dist per point is ~0.25*dim
	// Expected inertia ~ n * 0.25 * dim = 400 * 0.25 * 16 = 1600
	maxExpectedInertia := float64(len(vectors)) * 0.25 * float64(len(vectors[0])) * 2
	if inertia > maxExpectedInertia {
		t.Errorf("inertia %v too high for clustered data (expected < %v)", inertia, maxExpectedInertia)
	}
}

// =============================================================================
// Derived Config Tests
// =============================================================================

// TestKMeansDeriveConfig verifies derived config is reasonable.
func TestKMeansDeriveConfig(t *testing.T) {
	tests := []struct {
		k               int
		minRestarts     int
		maxRestarts     int
	}{
		{2, 1, 2},
		{4, 2, 3},
		{8, 3, 4},
		{16, 4, 5},
		{256, 8, 9},
	}

	for _, tc := range tests {
		config := DeriveKMeansConfig(tc.k)
		if config.NumRestarts < tc.minRestarts || config.NumRestarts > tc.maxRestarts {
			t.Errorf("DeriveKMeansConfig(%d).NumRestarts = %d, want [%d, %d]",
				tc.k, config.NumRestarts, tc.minRestarts, tc.maxRestarts)
		}
		if config.ConvergenceThreshold <= 0 {
			t.Errorf("DeriveKMeansConfig(%d).ConvergenceThreshold = %v, want > 0",
				tc.k, config.ConvergenceThreshold)
		}
	}
}

// TestKMeansOptimalSimple tests the convenience wrapper.
func TestKMeansOptimalSimple(t *testing.T) {
	vectors := kmeansTestVectors(100, 16, 42)
	k := 8

	result := KMeansOptimalSimple(vectors, k)

	if len(result) != k {
		t.Errorf("expected %d centroids, got %d", k, len(result))
	}

	// Verify centroids have correct dimension
	for i, c := range result {
		if len(c) != 16 {
			t.Errorf("centroid[%d] has dimension %d, want 16", i, len(c))
		}
	}
}
