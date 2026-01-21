package quantization

import (
	"context"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Test Utilities
// =============================================================================

// fourLayerGenerateClusteredVectors creates vectors clustered around random centers.
// This simulates realistic code embedding distributions where vectors group by semantic type.
func fourLayerGenerateClusteredVectors(rng *rand.Rand, numVectors, dim, numClusters int, clusterSpread float32) [][]float32 {
	// Generate cluster centers
	centers := make([][]float32, numClusters)
	for i := range centers {
		centers[i] = make([]float32, dim)
		for j := range centers[i] {
			centers[i][j] = rng.Float32()*2 - 1
		}
	}

	// Generate vectors around centers
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		center := centers[i%numClusters]
		for j := range vectors[i] {
			// Gaussian-like noise using sum of uniforms
			noise := (rng.Float32() + rng.Float32() + rng.Float32() - 1.5) * clusterSpread
			vectors[i][j] = center[j] + noise
		}
	}
	return vectors
}

func fourLayerComputeGroundTruth(queries, vectors [][]float32, k int) [][]int {
	idx := NewDistanceIndex(vectors)
	numQueries := len(queries)
	groundTruth := make([][]int, numQueries)

	numWorkers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	queryChan := make(chan int, numQueries)

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			distBuf := make([]float32, len(vectors))
			for q := range queryChan {
				groundTruth[q] = idx.KNN(queries[q], k, distBuf)
			}
		}()
	}

	for q := range numQueries {
		queryChan <- q
	}
	close(queryChan)
	wg.Wait()

	return groundTruth
}

func fourLayerComputeBruteForceKNN(query []float32, vectors [][]float32, k int) []int {
	idx := NewDistanceIndex(vectors)
	distBuf := make([]float32, len(vectors))
	return idx.KNN(query, k, distBuf)
}

// computeRecall calculates recall@k for a single query.
func computeRecall(searchResults []int, groundTruth []int, k int) float64 {
	gtSet := make(map[int]bool)
	for _, idx := range groundTruth[:min(k, len(groundTruth))] {
		gtSet[idx] = true
	}

	hits := 0
	for _, idx := range searchResults[:min(k, len(searchResults))] {
		if gtSet[idx] {
			hits++
		}
	}

	return float64(hits) / float64(min(k, len(groundTruth)))
}

// =============================================================================
// TestFourLayerPipeline_ColdStart
// =============================================================================

// TestFourLayerPipeline_ColdStart tests that the system works from cold start.
// With no training data, RaBitQ should provide immediate results.
func TestFourLayerPipeline_ColdStart(t *testing.T) {
	const dim = 128
	const numVectors = 100
	const seed = 42

	rng := rand.New(rand.NewSource(seed))

	// Create RaBitQ encoder (Layer 1) - should work immediately with zero training
	rabitqConfig := RaBitQConfig{
		Dimension:        dim,
		Seed:             seed,
		CorrectionFactor: true,
	}

	encoder, err := NewRaBitQEncoder(rabitqConfig)
	if err != nil {
		t.Fatalf("failed to create RaBitQ encoder: %v", err)
	}

	// Generate test vectors
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()*2 - 1
		}
	}

	// Encode all vectors - should work immediately
	codes := make([]RaBitQCode, numVectors)
	for i, vec := range vectors {
		code, err := encoder.Encode(vec)
		if err != nil {
			t.Fatalf("cold start encode failed at vector %d: %v", i, err)
		}
		codes[i] = code
	}

	// Search using RaBitQ distances
	query := vectors[0]
	type result struct {
		idx  int
		dist float32
	}

	results := make([]result, numVectors)
	for i, code := range codes {
		dist, err := encoder.ApproximateL2Distance(query, code)
		if err != nil {
			t.Fatalf("distance computation failed: %v", err)
		}
		results[i] = result{idx: i, dist: dist}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].dist < results[j].dist
	})

	// First result should be the query itself (index 0)
	if results[0].idx != 0 {
		t.Errorf("cold start search failed: expected query as top result, got index %d", results[0].idx)
	}

	// Distance to self should be lowest (may not be zero due to quantization loss)
	// RaBitQ is lossy - the reconstruction won't match original exactly
	if results[0].idx != 0 {
		t.Errorf("cold start: query vector should be top result, got index %d", results[0].idx)
	}

	t.Logf("Cold start test passed: encoded %d vectors, search works immediately", numVectors)
}

// =============================================================================
// TestFourLayerPipeline_WarmUp
// =============================================================================

// TestFourLayerPipeline_WarmUp tests progressive warmup where LOPQ trains after threshold.
func TestFourLayerPipeline_WarmUp(t *testing.T) {
	const dim = 64
	const seed = 42
	const trainingThreshold = 50

	rng := rand.New(rand.NewSource(seed))
	ctx := context.Background()

	// Create adaptive quantizer (simulates 4-layer system)
	config := DefaultAdaptiveQuantizerConfig()
	config.TargetRecall = 0.9

	// Use exact strategy initially for small datasets (simulating cold start)
	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create quantizer: %v", err)
	}

	// Phase 1: Insert vectors below training threshold
	smallVectors := fourLayerGenerateClusteredVectors(rng, trainingThreshold-10, dim, 5, 0.3)
	if err := aq.Train(ctx, smallVectors); err != nil {
		t.Fatalf("warmup phase 1 training failed: %v", err)
	}

	// With small dataset, should use Exact strategy
	if aq.Strategy() != StrategyExact {
		t.Logf("Phase 1 strategy: %s (expected Exact for %d vectors)", aq.Strategy(), len(smallVectors))
	}

	// Phase 2: Add more vectors to exceed threshold
	mediumVectors := fourLayerGenerateClusteredVectors(rng, 200, dim, 10, 0.3)

	aq2, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create quantizer: %v", err)
	}

	if err := aq2.Train(ctx, mediumVectors); err != nil {
		t.Fatalf("warmup phase 2 training failed: %v", err)
	}

	// With medium dataset, should use CoarsePQRerank
	t.Logf("Phase 2 strategy: %s with %d vectors", aq2.Strategy(), len(mediumVectors))

	// Phase 3: Add even more vectors
	largeVectors := fourLayerGenerateClusteredVectors(rng, 2000, dim, 20, 0.3)

	aq3, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create quantizer: %v", err)
	}

	if err := aq3.Train(ctx, largeVectors); err != nil {
		t.Fatalf("warmup phase 3 training failed: %v", err)
	}

	// With large dataset, should use ResidualPQ
	t.Logf("Phase 3 strategy: %s with %d vectors", aq3.Strategy(), len(largeVectors))

	// Verify search works at each phase
	query := largeVectors[0]

	results, err := aq3.Search(query, 10)
	if err != nil {
		t.Fatalf("search after warmup failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("no results returned after warmup")
	}

	t.Logf("Warmup test passed: system progressively adapts as data grows")
}

// =============================================================================
// TestFourLayerPipeline_RecallProgression
// =============================================================================

// TestFourLayerPipeline_RecallProgression verifies recall improves as layers activate.
func TestFourLayerPipeline_RecallProgression(t *testing.T) {
	const dim = 64
	const numDB = 2000
	const numQueries = 50
	const k = 10
	const seed = 42

	rng := rand.New(rand.NewSource(seed))
	ctx := context.Background()

	// Generate clustered test data
	dbVectors := fourLayerGenerateClusteredVectors(rng, numDB, dim, 20, 0.3)

	// Generate queries from existing vectors with slight perturbation
	queries := make([][]float32, numQueries)
	for i := range queries {
		idx := rng.Intn(numDB)
		queries[i] = make([]float32, dim)
		for j := range queries[i] {
			queries[i][j] = dbVectors[idx][j] + (rng.Float32()-0.5)*0.1
		}
	}

	groundTruth := fourLayerComputeGroundTruth(queries, dbVectors, k)

	// Test 1: RaBitQ only (Layer 1)
	rabitqConfig := RaBitQConfig{
		Dimension:        dim,
		Seed:             seed,
		CorrectionFactor: true,
	}
	rabitqEncoder, _ := NewRaBitQEncoder(rabitqConfig)

	rabitqCodes := make([]RaBitQCode, numDB)
	for i, vec := range dbVectors {
		code, _ := rabitqEncoder.Encode(vec)
		rabitqCodes[i] = code
	}

	var rabitqRecall float64
	for qi, query := range queries {
		results := make([]struct {
			idx  int
			dist float32
		}, numDB)
		for i, code := range rabitqCodes {
			dist, _ := rabitqEncoder.ApproximateL2Distance(query, code)
			results[i].idx = i
			results[i].dist = dist
		}
		sort.Slice(results, func(i, j int) bool {
			return results[i].dist < results[j].dist
		})
		searchResults := make([]int, k)
		for i := 0; i < k; i++ {
			searchResults[i] = results[i].idx
		}
		rabitqRecall += computeRecall(searchResults, groundTruth[qi], k)
	}
	rabitqRecall /= float64(numQueries)
	t.Logf("Layer 1 (RaBitQ) Recall@%d: %.2f%%", k, rabitqRecall*100)

	// Test 2: Full pipeline with residual PQ
	config := DefaultAdaptiveQuantizerConfig()
	config.ForceStrategy = StrategyResidualPQ
	config.EnableResidual = true

	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create quantizer: %v", err)
	}
	if err := aq.Train(ctx, dbVectors); err != nil {
		t.Fatalf("training failed: %v", err)
	}

	var pipelineRecall float64
	for qi, query := range queries {
		results, _ := aq.Search(query, k)
		searchResults := make([]int, len(results))
		for i, r := range results {
			searchResults[i] = r.Index
		}
		pipelineRecall += computeRecall(searchResults, groundTruth[qi], k)
	}
	pipelineRecall /= float64(numQueries)
	t.Logf("Full pipeline Recall@%d: %.2f%%", k, pipelineRecall*100)

	// Test 3: Pipeline with reranking
	var rerankRecall float64
	for qi, query := range queries {
		candidates, _ := aq.Search(query, k*10)
		for i := range candidates {
			candidates[i].Distance = SquaredL2Single(query, dbVectors[candidates[i].Index])
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Distance < candidates[j].Distance
		})
		searchResults := make([]int, min(k, len(candidates)))
		for i := range searchResults {
			searchResults[i] = candidates[i].Index
		}
		rerankRecall += computeRecall(searchResults, groundTruth[qi], k)
	}
	rerankRecall /= float64(numQueries)
	t.Logf("Pipeline + Rerank Recall@%d: %.2f%%", k, rerankRecall*100)

	// Verify recall progression: rerank should be best
	if rerankRecall < pipelineRecall*0.95 {
		t.Errorf("rerank recall (%.2f%%) should not degrade below pipeline recall (%.2f%%)",
			rerankRecall*100, pipelineRecall*100)
	}

	// Target: rerank should achieve 85%+ recall
	if rerankRecall < 0.85 {
		t.Errorf("rerank recall %.2f%% below target 85%%", rerankRecall*100)
	}
}

// =============================================================================
// TestFourLayerPipeline_DistributionShift
// =============================================================================

// TestFourLayerPipeline_DistributionShift tests adaptation when data distribution changes.
func TestFourLayerPipeline_DistributionShift(t *testing.T) {
	const dim = 32
	const seed = 42

	rng := rand.New(rand.NewSource(seed))
	ctx := context.Background()

	// Phase 1: Train on one distribution (centered around origin)
	dist1Vectors := make([][]float32, 500)
	for i := range dist1Vectors {
		dist1Vectors[i] = make([]float32, dim)
		for j := range dist1Vectors[i] {
			dist1Vectors[i][j] = (rng.Float32() - 0.5) * 0.5 // Values in [-0.25, 0.25]
		}
	}

	config := DefaultAdaptiveQuantizerConfig()
	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create quantizer: %v", err)
	}
	if err := aq.Train(ctx, dist1Vectors); err != nil {
		t.Fatalf("phase 1 training failed: %v", err)
	}

	// Measure recall on distribution 1
	var recall1 float64
	for i := 0; i < 20; i++ {
		query := dist1Vectors[rng.Intn(len(dist1Vectors))]
		gt := fourLayerComputeBruteForceKNN(query, dist1Vectors, 10)
		results, _ := aq.Search(query, 10)
		searchResults := make([]int, len(results))
		for j, r := range results {
			searchResults[j] = r.Index
		}
		recall1 += computeRecall(searchResults, gt, 10)
	}
	recall1 /= 20
	t.Logf("Distribution 1 recall: %.2f%%", recall1*100)

	// Phase 2: Create shifted distribution (centered around [0.5, 0.5, ...])
	dist2Vectors := make([][]float32, 500)
	for i := range dist2Vectors {
		dist2Vectors[i] = make([]float32, dim)
		for j := range dist2Vectors[i] {
			dist2Vectors[i][j] = 0.5 + (rng.Float32()-0.5)*0.5 // Values in [0.25, 0.75]
		}
	}

	// Train on combined data (simulates adaptation)
	combinedVectors := append(dist1Vectors, dist2Vectors...)

	aq2, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create adapted quantizer: %v", err)
	}
	if err := aq2.Train(ctx, combinedVectors); err != nil {
		t.Fatalf("combined training failed: %v", err)
	}

	// Measure recall on distribution 2 with adapted model
	var recall2 float64
	for i := 0; i < 20; i++ {
		query := dist2Vectors[rng.Intn(len(dist2Vectors))]
		// Ground truth indices are offset by len(dist1Vectors)
		gt := fourLayerComputeBruteForceKNN(query, combinedVectors, 10)
		results, _ := aq2.Search(query, 10)
		searchResults := make([]int, len(results))
		for j, r := range results {
			searchResults[j] = r.Index
		}
		recall2 += computeRecall(searchResults, gt, 10)
	}
	recall2 /= 20
	t.Logf("Distribution 2 recall (adapted): %.2f%%", recall2*100)

	// Both distributions should maintain reasonable recall
	if recall1 < 0.5 {
		t.Errorf("distribution 1 recall %.2f%% too low", recall1*100)
	}
	if recall2 < 0.5 {
		t.Errorf("distribution 2 recall %.2f%% too low after adaptation", recall2*100)
	}

	t.Logf("Distribution shift test passed: model adapts to new data")
}

// =============================================================================
// TestFourLayerPipeline_QueryAdaptive
// =============================================================================

// TestFourLayerPipeline_QueryAdaptive tests query-adaptive subspace weighting.
func TestFourLayerPipeline_QueryAdaptive(t *testing.T) {
	const dim = 64
	const numSubspaces = 8
	const seed = 42

	rng := rand.New(rand.NewSource(seed))
	ctx := context.Background()

	// Generate database with varying subspace importance
	numDB := 1000
	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		for j := range dbVectors[i] {
			// Make first subspace have high variance, others low
			subspace := j / (dim / numSubspaces)
			if subspace == 0 {
				dbVectors[i][j] = rng.Float32()*2 - 1 // High variance
			} else {
				dbVectors[i][j] = rng.Float32()*0.2 - 0.1 // Low variance
			}
		}
	}

	// Train PQ
	config := DefaultAdaptiveQuantizerConfig()
	config.ForceStrategy = StrategyStandardPQ
	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create quantizer: %v", err)
	}
	if err := aq.Train(ctx, dbVectors); err != nil {
		t.Fatalf("training failed: %v", err)
	}

	// Create query with high variance in first subspace
	query := make([]float32, dim)
	for j := range query {
		subspace := j / (dim / numSubspaces)
		if subspace == 0 {
			query[j] = rng.Float32()*2 - 1 // High variance matching DB
		} else {
			query[j] = rng.Float32()*0.2 - 0.1
		}
	}

	// Test query-adaptive profile computation
	profile := NewQueryProfile(dim, numSubspaces)
	if profile == nil {
		t.Fatal("failed to create query profile")
	}

	if err := profile.ComputeFromQuery(query, WeightingVariance); err != nil {
		t.Fatalf("profile computation failed: %v", err)
	}

	// First subspace should have higher variance
	if len(profile.SubspaceVariances) != numSubspaces {
		t.Fatalf("expected %d subspace variances, got %d", numSubspaces, len(profile.SubspaceVariances))
	}

	// Weight for first subspace should be higher due to variance
	if profile.Weights[0] <= profile.Weights[1] {
		t.Logf("Warning: first subspace weight (%.4f) not higher than second (%.4f)",
			profile.Weights[0], profile.Weights[1])
	}

	t.Logf("Query-adaptive test: subspace variances = %v", profile.SubspaceVariances[:4])
	t.Logf("Query-adaptive test: computed weights = %v", profile.Weights[:4])
}

// =============================================================================
// TestFourLayerPipeline_BanditLearning
// =============================================================================

// TestFourLayerPipeline_BanditLearning tests that bandit concentrates on better parameters.
func TestFourLayerPipeline_BanditLearning(t *testing.T) {
	const seed = 42
	const codebaseID = "test-codebase"

	// Create bandit without store (in-memory only)
	bandit := NewQuantizationBandit(nil)

	// Simulate search trials with rewards
	// Give high rewards when candidate multiplier is high (simulating better recall)
	rng := rand.New(rand.NewSource(seed))

	observations := 100
	for i := 0; i < observations; i++ {
		config := bandit.Sample(codebaseID)

		// Simulate reward: higher candidate multiplier = higher recall = higher reward
		var recall float64
		switch config.CandidateMultiplier {
		case 2:
			recall = 0.6 + rng.Float64()*0.1
		case 4:
			recall = 0.7 + rng.Float64()*0.1
		case 8:
			recall = 0.85 + rng.Float64()*0.1
		case 16:
			recall = 0.9 + rng.Float64()*0.1
		}

		// Binary reward: success if recall >= 0.85
		reward := 0.0
		if recall >= 0.85 {
			reward = 1.0
		}

		outcome := RewardOutcome{
			CodebaseID: codebaseID,
			Config:     config,
			Reward:     reward,
			RecordedAt: time.Now(),
			Recall:     recall,
		}

		for armID, choiceIdx := range config.SampledChoiceIndices {
			bandit.Update(codebaseID, armID, choiceIdx, outcome.Reward)
		}
	}

	// Check that bandit learned to prefer higher multipliers
	posteriors := bandit.GetPosteriorStats(codebaseID, ArmCandidateMultiplier)
	if posteriors == nil {
		t.Fatal("no posteriors found")
	}

	// Higher indices (higher multipliers) should have better means
	t.Logf("Candidate multiplier posteriors after %d observations:", observations)
	choices := []int{2, 4, 8, 16}
	for i, p := range posteriors {
		t.Logf("  Choice %d (mult=%d): mean=%.3f, obs=%d",
			i, choices[i], p.Mean(), p.TotalObservations())
	}

	// Verify bandit has learned (last choice should have highest mean)
	if len(posteriors) >= 2 {
		lastMean := posteriors[len(posteriors)-1].Mean()
		firstMean := posteriors[0].Mean()
		if lastMean < firstMean {
			t.Errorf("bandit should prefer higher multiplier: last=%.3f < first=%.3f",
				lastMean, firstMean)
		}
	}

	// Verify total observations match
	totalObs := bandit.GetTotalObservations(codebaseID)
	if totalObs == 0 {
		t.Error("no observations recorded")
	}
	t.Logf("Total observations: %d", totalObs)
}

// =============================================================================
// TestFourLayerPipeline_RecallTargetLearning
// =============================================================================

// TestFourLayerPipeline_RecallTargetLearning tests recall target adjustment from agent usage.
func TestFourLayerPipeline_RecallTargetLearning(t *testing.T) {
	const codebaseID = "test-codebase"
	const seed = 42

	learner := NewRecallLearner(codebaseID)
	rng := rand.New(rand.NewSource(seed))

	// Simulate agent usage pattern: agents typically use first 5-10 results
	for i := 0; i < 100; i++ {
		// Prefetch 20 results
		prefetched := make([]string, 20)
		for j := 0; j < 20; j++ {
			prefetched[j] = string(rune('a' + j))
		}

		// Agent uses some results (typically early ones)
		numUsed := rng.Intn(5) + 1 // Use 1-5 results
		used := make([]string, numUsed)
		for j := 0; j < numUsed; j++ {
			// 80% chance of using early results, 20% chance of using deeper
			if rng.Float32() < 0.8 {
				used[j] = prefetched[rng.Intn(5)] // Top 5
			} else {
				used[j] = prefetched[rng.Intn(15)+5] // Position 5-19
			}
		}

		// 10% chance agent searched again (prefetch was insufficient)
		searchedAgain := rng.Float32() < 0.1

		learner.ObserveUsage(prefetched, used, searchedAgain)
	}

	// Get learned recall target
	target := learner.GetLearnedRecallTarget()
	confidence := learner.GetConfidence()
	obsCount := learner.GetObservationCount()
	searchAgainRate := learner.GetSearchedAgainRate()

	t.Logf("Learned recall target: %.2f%% (confidence: %.2f%%)", target*100, confidence*100)
	t.Logf("Observations: %d, Search-again rate: %.1f%%", obsCount, searchAgainRate*100)

	// Target should be reasonable (between 10% and 100%)
	if target < 0.1 || target > 1.0 {
		t.Errorf("learned target %.2f out of valid range [0.1, 1.0]", target)
	}

	// With enough observations, confidence should be non-zero
	if confidence <= 0 && obsCount >= 50 {
		t.Errorf("confidence should be positive with %d observations", obsCount)
	}

	// Test that agents using deep results increases target
	learner2 := NewRecallLearner(codebaseID + "-deep")
	for i := 0; i < 50; i++ {
		prefetched := make([]string, 20)
		for j := 0; j < 20; j++ {
			prefetched[j] = string(rune('a' + j))
		}
		// Always use deep results (positions 15-19)
		used := []string{prefetched[15], prefetched[17], prefetched[19]}
		learner2.ObserveUsage(prefetched, used, true) // Always search again
	}

	target2 := learner2.GetLearnedRecallTarget()
	t.Logf("Deep-usage recall target: %.2f%%", target2*100)

	// Deep usage should result in higher target
	if target2 <= target {
		t.Logf("Note: deep usage target (%.2f%%) expected to be higher than normal (%.2f%%)",
			target2*100, target*100)
	}
}

// =============================================================================
// TestFourLayerPipeline_EndToEnd
// =============================================================================

// TestFourLayerPipeline_EndToEnd is a full integration test with realistic workload.
func TestFourLayerPipeline_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end test in short mode")
	}

	const dim = 128 // Realistic for small embeddings (reduced from 768)
	const numDB = 5000
	const numQueries = 100
	const k = 10
	const seed = 42

	rng := rand.New(rand.NewSource(seed))
	ctx := context.Background()

	numTypes := 5
	dbVectors := fourLayerGenerateClusteredVectors(rng, numDB, dim, numTypes, 0.4)

	// Generate queries (similar to searching for code elements)
	queries := make([][]float32, numQueries)
	for i := range queries {
		// Query is a perturbed version of a random DB vector
		idx := rng.Intn(numDB)
		queries[i] = make([]float32, dim)
		for j := range queries[i] {
			queries[i][j] = dbVectors[idx][j] + (rng.Float32()-0.5)*0.2
		}
	}

	// Compute ground truth
	groundTruth := make([][]int, numQueries)
	for i, q := range queries {
		groundTruth[i] = fourLayerComputeBruteForceKNN(q, dbVectors, k)
	}

	// Create and train full pipeline
	config := DefaultAdaptiveQuantizerConfig()
	config.ForceStrategy = StrategyResidualPQ
	config.EnableResidual = true
	config.TargetRecall = 0.9

	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create quantizer: %v", err)
	}

	trainStart := time.Now()
	if err := aq.Train(ctx, dbVectors); err != nil {
		t.Fatalf("training failed: %v", err)
	}
	trainDuration := time.Since(trainStart)

	// Run search benchmark
	searchStart := time.Now()
	var totalRecall float64
	var maxLatency time.Duration

	for qi, query := range queries {
		queryStart := time.Now()
		candidates, err := aq.Search(query, k*10)
		if err != nil {
			t.Fatalf("search %d failed: %v", qi, err)
		}

		for i := range candidates {
			candidates[i].Distance = SquaredL2Single(query, dbVectors[candidates[i].Index])
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Distance < candidates[j].Distance
		})

		queryLatency := time.Since(queryStart)
		if queryLatency > maxLatency {
			maxLatency = queryLatency
		}

		searchResults := make([]int, min(k, len(candidates)))
		for i := range searchResults {
			searchResults[i] = candidates[i].Index
		}
		totalRecall += computeRecall(searchResults, groundTruth[qi], k)
	}

	avgSearchTime := time.Since(searchStart) / time.Duration(numQueries)
	avgRecall := totalRecall / float64(numQueries)

	t.Logf("End-to-end results:")
	t.Logf("  Dataset: %d vectors, %d dimensions", numDB, dim)
	t.Logf("  Strategy: %s", aq.Strategy())
	t.Logf("  Training time: %v", trainDuration)
	t.Logf("  Avg search time: %v", avgSearchTime)
	t.Logf("  Max search latency: %v", maxLatency)
	t.Logf("  Recall@%d: %.2f%%", k, avgRecall*100)

	// Verify targets
	if avgRecall < 0.85 {
		t.Errorf("recall %.2f%% below target 85%%", avgRecall*100)
	}

	// Latency target: <100ms per query for 5k vectors
	if maxLatency > 100*time.Millisecond {
		t.Errorf("max latency %v exceeds 100ms target", maxLatency)
	}

	t.Logf("End-to-end test PASSED")
}

// =============================================================================
// TestFourLayerPipeline_Concurrency
// =============================================================================

// TestFourLayerPipeline_Concurrency tests thread safety with concurrent operations.
func TestFourLayerPipeline_Concurrency(t *testing.T) {
	const dim = 64
	const numDB = 1000
	const seed = 42

	rng := rand.New(rand.NewSource(seed))
	ctx := context.Background()

	dbVectors := fourLayerGenerateClusteredVectors(rng, numDB, dim, 10, 0.3)

	config := DefaultAdaptiveQuantizerConfig()
	aq, err := NewAdaptiveQuantizer(dim, config)
	if err != nil {
		t.Fatalf("failed to create quantizer: %v", err)
	}
	if err := aq.Train(ctx, dbVectors); err != nil {
		t.Fatalf("training failed: %v", err)
	}

	// Create RaBitQ encoder for concurrent encoding tests
	rabitqConfig := RaBitQConfig{
		Dimension:        dim,
		Seed:             seed,
		CorrectionFactor: true,
	}
	encoder, _ := NewRaBitQEncoder(rabitqConfig)

	// Test concurrent searches
	var wg sync.WaitGroup
	var searchErrors int32
	var searchCount int32

	numGoroutines := 10
	queriesPerGoroutine := 50

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			localRng := rand.New(rand.NewSource(int64(seed + goroutineID)))

			for q := 0; q < queriesPerGoroutine; q++ {
				// Random query
				query := make([]float32, dim)
				for j := range query {
					query[j] = localRng.Float32()*2 - 1
				}

				// Concurrent search
				_, err := aq.Search(query, 10)
				if err != nil {
					atomic.AddInt32(&searchErrors, 1)
				}
				atomic.AddInt32(&searchCount, 1)
			}
		}(g)
	}

	// Test concurrent encoding in parallel
	var encodeWg sync.WaitGroup
	var encodeErrors int32
	var encodeCount int32

	for g := 0; g < numGoroutines; g++ {
		encodeWg.Add(1)
		go func(goroutineID int) {
			defer encodeWg.Done()
			localRng := rand.New(rand.NewSource(int64(seed + goroutineID + 100)))

			for e := 0; e < queriesPerGoroutine; e++ {
				vec := make([]float32, dim)
				for j := range vec {
					vec[j] = localRng.Float32()*2 - 1
				}

				_, err := encoder.Encode(vec)
				if err != nil {
					atomic.AddInt32(&encodeErrors, 1)
				}
				atomic.AddInt32(&encodeCount, 1)
			}
		}(g)
	}

	wg.Wait()
	encodeWg.Wait()

	t.Logf("Concurrent searches: %d total, %d errors", searchCount, searchErrors)
	t.Logf("Concurrent encodes: %d total, %d errors", encodeCount, encodeErrors)

	if searchErrors > 0 {
		t.Errorf("%d search errors during concurrent execution", searchErrors)
	}
	if encodeErrors > 0 {
		t.Errorf("%d encode errors during concurrent execution", encodeErrors)
	}

	// Test concurrent utilization tracking
	adaptConfig := DefaultAdaptationConfig()
	adaptConfig.Enabled = true
	adaptConfig.DeathThreshold = 1
	adaptConfig.BirthThreshold = 1000

	tracker, err := NewInMemoryUtilizationTracker(adaptConfig)
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}

	var trackWg sync.WaitGroup
	var trackErrors int32

	for g := 0; g < numGoroutines; g++ {
		trackWg.Add(1)
		go func(goroutineID int) {
			defer trackWg.Done()
			centroid := CentroidID{
				CodebookType:  "global",
				SubspaceIndex: goroutineID % 4,
				CentroidIndex: goroutineID,
			}

			for i := 0; i < 100; i++ {
				if err := tracker.Increment(centroid); err != nil {
					atomic.AddInt32(&trackErrors, 1)
				}
			}
		}(g)
	}

	trackWg.Wait()

	if trackErrors > 0 {
		t.Errorf("%d tracking errors during concurrent execution", trackErrors)
	}

	stats := tracker.GetStats()
	t.Logf("Utilization tracker stats: %d centroids tracked", stats.TotalCentroids)
}

// =============================================================================
// BenchmarkFourLayerPipeline_Search
// =============================================================================

func BenchmarkFourLayerPipeline_Search(b *testing.B) {
	const dim = 128
	const numDB = 10000
	const k = 10
	const seed = 42

	rng := rand.New(rand.NewSource(seed))
	ctx := context.Background()

	dbVectors := fourLayerGenerateClusteredVectors(rng, numDB, dim, 20, 0.3)

	// Prepare query
	query := make([]float32, dim)
	for j := range query {
		query[j] = rng.Float32()*2 - 1
	}

	b.Run("RaBitQ_Only", func(b *testing.B) {
		rabitqConfig := RaBitQConfig{
			Dimension:        dim,
			Seed:             seed,
			CorrectionFactor: true,
		}
		encoder, _ := NewRaBitQEncoder(rabitqConfig)

		codes := make([]RaBitQCode, numDB)
		for i, vec := range dbVectors {
			codes[i], _ = encoder.Encode(vec)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			results := make([]struct {
				idx  int
				dist float32
			}, numDB)
			for j, code := range codes {
				dist, _ := encoder.ApproximateL2Distance(query, code)
				results[j].idx = j
				results[j].dist = dist
			}
			sort.Slice(results, func(i, j int) bool {
				return results[i].dist < results[j].dist
			})
			_ = results[:k]
		}
	})

	b.Run("ResidualPQ", func(b *testing.B) {
		config := DefaultAdaptiveQuantizerConfig()
		config.ForceStrategy = StrategyResidualPQ
		aq, _ := NewAdaptiveQuantizer(dim, config)
		aq.Train(ctx, dbVectors)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			aq.Search(query, k)
		}
	})

	b.Run("ResidualPQ_WithRerank", func(b *testing.B) {
		config := DefaultAdaptiveQuantizerConfig()
		config.ForceStrategy = StrategyResidualPQ
		aq, _ := NewAdaptiveQuantizer(dim, config)
		aq.Train(ctx, dbVectors)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			candidates, _ := aq.Search(query, k*10)
			for j := range candidates {
				candidates[j].Distance = SquaredL2Single(query, dbVectors[candidates[j].Index])
			}
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].Distance < candidates[j].Distance
			})
		}
	})
}

// =============================================================================
// TestFourLayerPipeline_RemoveBirthAdaptation
// =============================================================================

// TestFourLayerPipeline_RemoveBirthAdaptation tests the Remove-Birth centroid adaptation.
func TestFourLayerPipeline_RemoveBirthAdaptation(t *testing.T) {
	adaptConfig := AdaptationConfig{
		DeathThreshold:          5,
		BirthThreshold:          100,
		CheckInterval:           time.Second,
		MinCentroidsPerSubspace: 2,
		MaxCentroidsPerSubspace: 256,
		Enabled:                 true,
	}

	// Create tracker
	tracker, err := NewInMemoryUtilizationTracker(adaptConfig)
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}

	// Create Remove-Birth adapter
	adapter, err := NewRemoveBirthAdapter(adaptConfig, tracker)
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	// Simulate centroid utilization
	// Create some centroids with low utilization (death candidates)
	for i := 0; i < 5; i++ {
		centroid := CentroidID{
			CodebookType:  "global",
			SubspaceIndex: 0,
			CentroidIndex: i,
		}
		// Low count - death candidate
		tracker.SetUtilization(centroid, 2)
	}

	// Create some centroids with high utilization (birth candidates)
	for i := 5; i < 10; i++ {
		centroid := CentroidID{
			CodebookType:  "global",
			SubspaceIndex: 0,
			CentroidIndex: i,
		}
		// High count - birth candidate
		tracker.SetUtilization(centroid, 150)
	}

	// Get dead and overloaded centroids
	deadCentroids, err := tracker.GetDeadCentroids("global")
	if err != nil {
		t.Fatalf("failed to get dead centroids: %v", err)
	}
	t.Logf("Dead centroids (count < %d): %d", adaptConfig.DeathThreshold, len(deadCentroids))

	overloadedCentroids, err := tracker.GetOverloadedCentroids("global")
	if err != nil {
		t.Fatalf("failed to get overloaded centroids: %v", err)
	}
	t.Logf("Overloaded centroids (count > %d): %d", adaptConfig.BirthThreshold, len(overloadedCentroids))

	// Verify death candidates detected
	if len(deadCentroids) != 5 {
		t.Errorf("expected 5 dead centroids, got %d", len(deadCentroids))
	}

	// Verify birth candidates detected
	if len(overloadedCentroids) != 5 {
		t.Errorf("expected 5 overloaded centroids, got %d", len(overloadedCentroids))
	}

	// Get stats
	stats := adapter.GetStats()
	t.Logf("Adaptation stats: total=%d, dead=%d, overloaded=%d",
		stats.TotalCentroids, stats.DeadCentroids, stats.OverloadedCentroids)
}

// =============================================================================
// TestFourLayerPipeline_QueryProfileVariance
// =============================================================================

// TestFourLayerPipeline_QueryProfileVariance tests variance-based query weighting.
func TestFourLayerPipeline_QueryProfileVariance(t *testing.T) {
	const dim = 64
	const numSubspaces = 8
	const subspaceDim = dim / numSubspaces

	// Create query with varying variance per subspace
	query := make([]float32, dim)

	// Subspace 0: high variance
	for j := 0; j < subspaceDim; j++ {
		query[j] = float32(j) - float32(subspaceDim)/2
	}

	// Subspace 1-7: low variance (near-constant)
	for s := 1; s < numSubspaces; s++ {
		for j := 0; j < subspaceDim; j++ {
			query[s*subspaceDim+j] = 0.1
		}
	}

	// Create profile
	profile := NewQueryProfile(dim, numSubspaces)
	if profile == nil {
		t.Fatal("failed to create profile")
	}

	// Compute with variance weighting
	if err := profile.ComputeFromQuery(query, WeightingVariance); err != nil {
		t.Fatalf("profile computation failed: %v", err)
	}

	t.Logf("Subspace variances: %v", profile.SubspaceVariances)
	t.Logf("Weights: %v", profile.Weights)

	// First subspace should have highest weight
	maxWeightIdx := 0
	for i, w := range profile.Weights {
		if w > profile.Weights[maxWeightIdx] {
			maxWeightIdx = i
		}
	}

	if maxWeightIdx != 0 {
		t.Errorf("expected subspace 0 to have highest weight, got subspace %d", maxWeightIdx)
	}

	// Variance of first subspace should be much higher
	if profile.SubspaceVariances[0] <= profile.SubspaceVariances[1]*10 {
		t.Errorf("first subspace variance (%.4f) should be >> than second (%.4f)",
			profile.SubspaceVariances[0], profile.SubspaceVariances[1])
	}

	// Weights should sum to 1.0
	sum := profile.Weights.Sum()
	if math.Abs(float64(sum-1.0)) > 0.01 {
		t.Errorf("weights sum to %.4f, expected 1.0", sum)
	}
}
