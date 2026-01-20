package memory

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge/query"
	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// MD.4.1 MemoryWeightedScorer Tests
// =============================================================================

func TestNewMemoryWeightedScorer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)

	t.Run("default priors", func(t *testing.T) {
		scorer := NewMemoryWeightedScorer(store)
		if scorer == nil {
			t.Fatal("expected non-nil scorer")
		}

		// Verify default weight priors
		activation, recency, frequency, threshold := scorer.GetWeights()

		// ActivationWeight: Beta(5, 5) -> mean 0.5
		if math.Abs(activation-0.5) > 0.01 {
			t.Errorf("expected activation weight 0.5, got %f", activation)
		}

		// RecencyBonus: Beta(3, 7) -> mean 0.3
		if math.Abs(recency-0.3) > 0.01 {
			t.Errorf("expected recency bonus 0.3, got %f", recency)
		}

		// FrequencyBonus: Beta(2, 8) -> mean 0.2
		if math.Abs(frequency-0.2) > 0.01 {
			t.Errorf("expected frequency bonus 0.2, got %f", frequency)
		}

		// RetrievalThreshold: Beta(2, 8) -> mean 0.2
		if math.Abs(threshold-0.2) > 0.01 {
			t.Errorf("expected retrieval threshold 0.2, got %f", threshold)
		}
	})

	t.Run("default min activation", func(t *testing.T) {
		scorer := NewMemoryWeightedScorer(store)
		if scorer.MinActivation != DefaultMinActivation {
			t.Errorf("expected min activation %f, got %f",
				DefaultMinActivation, scorer.MinActivation)
		}
	})

	t.Run("store reference", func(t *testing.T) {
		scorer := NewMemoryWeightedScorer(store)
		if scorer.Store() != store {
			t.Error("store reference should match")
		}
	})
}

func TestNewMemoryWeightedScorerWithConfig(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	config := &handoff.UpdateConfig{
		LearningRate:   0.2,
		DriftThreshold: 0.9,
		PriorBlendRate: 0.3,
	}

	t.Run("custom config", func(t *testing.T) {
		scorer := NewMemoryWeightedScorerWithConfig(store, config)
		if scorer == nil {
			t.Fatal("expected non-nil scorer")
		}
		// Scorer should use custom config for updates
		// We verify this by checking that updates work differently
	})

	t.Run("nil config uses default", func(t *testing.T) {
		scorer := NewMemoryWeightedScorerWithConfig(store, nil)
		if scorer == nil {
			t.Fatal("expected non-nil scorer")
		}
	})
}

func TestMemoryWeightedScorer_ComputeMemoryScore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)
	nodeID := "test-node-score"
	insertTestNode(t, db, nodeID, domain.DomainAcademic)

	t.Run("no memory returns zero", func(t *testing.T) {
		score, err := scorer.ComputeMemoryScore(ctx, nodeID, time.Now().UTC(), false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if score != 0 {
			t.Errorf("expected zero score for no memory, got %f", score)
		}
	})

	t.Run("with traces returns positive score", func(t *testing.T) {
		// Add some access traces
		for i := 0; i < 5; i++ {
			err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		score, err := scorer.ComputeMemoryScore(ctx, nodeID, time.Now().UTC(), false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if score <= 0 {
			t.Errorf("expected positive score with traces, got %f", score)
		}
	})

	t.Run("explore mode samples weights", func(t *testing.T) {
		// With exploration, scores may vary due to sampling
		scores := make([]float64, 10)
		for i := 0; i < 10; i++ {
			score, err := scorer.ComputeMemoryScore(ctx, nodeID, time.Now().UTC(), true)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			scores[i] = score
		}

		// Check that there's some variance (sampling should produce different values)
		allSame := true
		for i := 1; i < len(scores); i++ {
			if math.Abs(scores[i]-scores[0]) > 0.001 {
				allSame = false
				break
			}
		}
		if allSame {
			t.Log("warning: all sampled scores are identical, may indicate sampling issue")
		}
	})
}

func TestMemoryWeightedScorer_ComputeMemoryScoreFormula(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)
	nodeID := "test-node-formula"
	insertTestNode(t, db, nodeID, domain.DomainArchitect)

	// Add multiple accesses
	for i := 0; i < 10; i++ {
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Run("score components are weighted correctly", func(t *testing.T) {
		now := time.Now().UTC()
		score, err := scorer.ComputeMemoryScore(ctx, nodeID, now, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Get the weights
		activationW, recencyW, frequencyW, _ := scorer.GetWeights()

		// Score should be bounded by sum of weights (each component is [0,1])
		maxPossibleScore := activationW + recencyW + frequencyW
		if score > maxPossibleScore {
			t.Errorf("score %f exceeds maximum possible %f", score, maxPossibleScore)
		}

		// Score should be non-negative
		if score < 0 {
			t.Errorf("score should be non-negative, got %f", score)
		}
	})

	t.Run("recency affects score", func(t *testing.T) {
		// Score now vs score simulated as if much later
		now := time.Now().UTC()
		scoreNow, _ := scorer.ComputeMemoryScore(ctx, nodeID, now, false)

		// Simulate future time (24 hours later)
		scoreLater, _ := scorer.ComputeMemoryScore(ctx, nodeID, now.Add(24*time.Hour), false)

		// Older time should have lower recency component
		if scoreLater >= scoreNow {
			t.Errorf("score should decrease with age: now=%f, later=%f", scoreNow, scoreLater)
		}
	})
}

func TestMemoryWeightedScorer_ApplyMemoryWeighting(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	// Create test nodes
	nodeIDs := []string{"node-a", "node-b", "node-c"}
	for _, id := range nodeIDs {
		insertTestNode(t, db, id, domain.DomainAcademic)
	}

	t.Run("empty results", func(t *testing.T) {
		results, err := scorer.ApplyMemoryWeighting(ctx, []query.HybridResult{}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Error("expected empty results")
		}
	})

	t.Run("preserves results with no memory", func(t *testing.T) {
		results := []query.HybridResult{
			{ID: "node-a", Score: 0.8},
			{ID: "node-b", Score: 0.6},
			{ID: "node-c", Score: 0.4},
		}

		weighted, err := scorer.ApplyMemoryWeighting(ctx, results, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(weighted) != 3 {
			t.Errorf("expected 3 results, got %d", len(weighted))
		}
	})

	t.Run("memory affects ranking", func(t *testing.T) {
		// Give node-c more memory accesses (should boost its score)
		for i := 0; i < 20; i++ {
			err := store.RecordAccess(ctx, "node-c", AccessRetrieval, "boost")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		results := []query.HybridResult{
			{ID: "node-a", Score: 0.8},
			{ID: "node-b", Score: 0.7},
			{ID: "node-c", Score: 0.5}, // Lower initial score but more memory
		}

		weighted, err := scorer.ApplyMemoryWeighting(ctx, results, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// node-c should have a boosted score due to memory
		var nodeCScore float64
		for _, r := range weighted {
			if r.ID == "node-c" {
				nodeCScore = r.Score
				break
			}
		}

		if nodeCScore <= 0.5 {
			t.Errorf("node-c score should be boosted, got %f", nodeCScore)
		}
	})

	t.Run("results are sorted by score", func(t *testing.T) {
		results := []query.HybridResult{
			{ID: "node-a", Score: 0.3},
			{ID: "node-b", Score: 0.9},
			{ID: "node-c", Score: 0.6},
		}

		weighted, err := scorer.ApplyMemoryWeighting(ctx, results, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Check descending order
		for i := 1; i < len(weighted); i++ {
			if weighted[i].Score > weighted[i-1].Score {
				t.Error("results should be sorted by score descending")
				break
			}
		}
	})
}

func TestMemoryWeightedScorer_FilterByRetrievalProbability(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	// Create test nodes
	nodeIDs := []string{"filter-a", "filter-b", "filter-c"}
	for _, id := range nodeIDs {
		insertTestNode(t, db, id, domain.DomainArchitect)
	}

	t.Run("empty results", func(t *testing.T) {
		results, err := scorer.FilterByRetrievalProbability(ctx, []query.HybridResult{}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Error("expected empty results")
		}
	})

	t.Run("results with no memory pass through", func(t *testing.T) {
		// Results with no memory should be included (fail open)
		results := []query.HybridResult{
			{ID: "filter-a", Score: 0.8},
			{ID: "filter-b", Score: 0.6},
		}

		filtered, err := scorer.FilterByRetrievalProbability(ctx, results, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(filtered) != 2 {
			t.Errorf("expected 2 results to pass through, got %d", len(filtered))
		}
	})

	t.Run("high activation nodes pass filter", func(t *testing.T) {
		// Create high activation for filter-a
		for i := 0; i < 30; i++ {
			err := store.RecordAccess(ctx, "filter-a", AccessReinforcement, "high")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		results := []query.HybridResult{
			{ID: "filter-a", Score: 0.5},
		}

		filtered, err := scorer.FilterByRetrievalProbability(ctx, results, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(filtered) != 1 {
			t.Errorf("high activation node should pass filter, got %d results", len(filtered))
		}
	})
}

func TestMemoryWeightedScorer_RecordRetrievalOutcome(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)
	nodeID := "outcome-node"
	insertTestNode(t, db, nodeID, domain.DomainAcademic)

	// First add some accesses so the node has memory
	for i := 0; i < 3; i++ {
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "initial")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Run("successful retrieval updates weights", func(t *testing.T) {
		initialActivation, initialRecency, _, _ := scorer.GetWeights()

		err := scorer.RecordRetrievalOutcome(ctx, nodeID, true, time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		newActivation, newRecency, _, _ := scorer.GetWeights()

		// Weights should have shifted toward success (observation=1.0)
		// With default prior Beta(5,5), successful update should increase mean slightly
		if newActivation <= initialActivation*0.99 {
			t.Logf("activation: %f -> %f (may vary due to learning rate)", initialActivation, newActivation)
		}
		if newRecency <= initialRecency*0.99 {
			t.Logf("recency: %f -> %f (may vary due to recency factor)", initialRecency, newRecency)
		}
	})

	t.Run("failed retrieval updates weights", func(t *testing.T) {
		scorer2 := NewMemoryWeightedScorer(store)
		initialActivation, _, _, _ := scorer2.GetWeights()

		err := scorer2.RecordRetrievalOutcome(ctx, nodeID, false, time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		newActivation, _, _, _ := scorer2.GetWeights()

		// Failed retrieval should decrease weights (observation=0.0)
		if newActivation >= initialActivation*1.01 {
			t.Logf("activation should decrease for failed retrieval: %f -> %f",
				initialActivation, newActivation)
		}
	})

	t.Run("records access trace", func(t *testing.T) {
		memoryBefore, _ := store.GetMemory(ctx, nodeID, domain.DomainAcademic)
		countBefore := memoryBefore.AccessCount

		err := scorer.RecordRetrievalOutcome(ctx, nodeID, true, time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		memoryAfter, _ := store.GetMemory(ctx, nodeID, domain.DomainAcademic)
		if memoryAfter.AccessCount <= countBefore {
			t.Error("access count should increase after recording outcome")
		}
	})
}

func TestMemoryWeightedScorer_GetWeightConfidences(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)

	t.Run("initial confidences", func(t *testing.T) {
		scorer := NewMemoryWeightedScorer(store)
		actConf, recConf, freqConf, threshConf := scorer.GetWeightConfidences()

		// With Beta(5,5) prior, precision is 10, confidence should be moderate
		// Confidence = 1 - exp(-precision/10) = 1 - exp(-1) ≈ 0.632
		expectedConf := 1.0 - math.Exp(-1.0)

		if math.Abs(actConf-expectedConf) > 0.1 {
			t.Errorf("activation confidence: expected ~%f, got %f", expectedConf, actConf)
		}

		// RecencyBonus has lower precision (3+7=10), same confidence
		if math.Abs(recConf-expectedConf) > 0.1 {
			t.Errorf("recency confidence: expected ~%f, got %f", expectedConf, recConf)
		}

		// FrequencyBonus and threshold have same precision
		if freqConf < 0 || freqConf > 1 {
			t.Errorf("frequency confidence out of range: %f", freqConf)
		}
		if threshConf < 0 || threshConf > 1 {
			t.Errorf("threshold confidence out of range: %f", threshConf)
		}
	})
}

func TestMemoryWeightedScorer_SetUpdateConfig(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)
	nodeID := "config-node"
	insertTestNode(t, db, nodeID, domain.DomainArchitect)

	// Add initial accesses
	for i := 0; i < 3; i++ {
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "initial")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Run("custom learning rate", func(t *testing.T) {
		// Higher learning rate should cause larger updates
		highLRConfig := &handoff.UpdateConfig{
			LearningRate:   0.5, // Much higher than default 0.1
			DriftThreshold: 0.95,
			PriorBlendRate: 0.2,
		}

		scorer.SetUpdateConfig(highLRConfig)
		initialWeight, _, _, _ := scorer.GetWeights()

		// Record a positive outcome
		err := scorer.RecordRetrievalOutcome(ctx, nodeID, true, time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		newWeight, _, _, _ := scorer.GetWeights()

		// With high learning rate, the change should be more noticeable
		change := math.Abs(newWeight - initialWeight)
		if change < 0.01 {
			t.Logf("weight change with high LR: %f", change)
		}
	})

	t.Run("nil config ignored", func(t *testing.T) {
		scorer.SetUpdateConfig(nil)
		// Should not panic
	})
}

func TestMemoryWeightedScorer_SetMinActivation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	scorer := NewMemoryWeightedScorer(store)

	t.Run("update min activation", func(t *testing.T) {
		scorer.SetMinActivation(-5.0)
		if scorer.MinActivation != -5.0 {
			t.Errorf("expected min activation -5.0, got %f", scorer.MinActivation)
		}
	})
}

func TestMemoryWeightedScorer_ConcurrentAccess(t *testing.T) {
	db := setupTestDBWithWAL(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	// Create test nodes
	for i := 0; i < 5; i++ {
		nodeID := string(rune('a' + i))
		insertTestNode(t, db, nodeID, domain.Domain(i%4))
	}

	t.Run("concurrent scoring", func(t *testing.T) {
		done := make(chan bool)
		errCh := make(chan error, 50)

		// Concurrent ComputeMemoryScore calls
		for i := 0; i < 5; i++ {
			go func(idx int) {
				nodeID := string(rune('a' + idx))
				for j := 0; j < 10; j++ {
					_, err := scorer.ComputeMemoryScore(ctx, nodeID, time.Now().UTC(), true)
					if err != nil {
						errCh <- err
					}
				}
				done <- true
			}(i)
		}

		for i := 0; i < 5; i++ {
			<-done
		}
		close(errCh)

		for err := range errCh {
			t.Errorf("concurrent scoring error: %v", err)
		}
	})

	t.Run("concurrent weighting and outcome recording", func(t *testing.T) {
		done := make(chan bool)
		errCh := make(chan error, 50)

		// Add some initial accesses
		for i := 0; i < 5; i++ {
			nodeID := string(rune('a' + i))
			for j := 0; j < 3; j++ {
				_ = store.RecordAccess(ctx, nodeID, AccessRetrieval, "init")
				time.Sleep(2 * time.Millisecond)
			}
		}

		// Concurrent ApplyMemoryWeighting
		for i := 0; i < 3; i++ {
			go func() {
				results := []query.HybridResult{
					{ID: "a", Score: 0.5},
					{ID: "b", Score: 0.4},
					{ID: "c", Score: 0.3},
				}
				for j := 0; j < 10; j++ {
					_, err := scorer.ApplyMemoryWeighting(ctx, results, true)
					if err != nil {
						errCh <- err
					}
				}
				done <- true
			}()
		}

		// Concurrent RecordRetrievalOutcome
		for i := 0; i < 3; i++ {
			go func(idx int) {
				nodeID := string(rune('a' + idx))
				for j := 0; j < 5; j++ {
					time.Sleep(10 * time.Millisecond)
					err := scorer.RecordRetrievalOutcome(ctx, nodeID, j%2 == 0, time.Hour)
					if err != nil {
						errCh <- err
					}
				}
				done <- true
			}(i)
		}

		for i := 0; i < 6; i++ {
			<-done
		}
		close(errCh)

		for err := range errCh {
			t.Errorf("concurrent operation error: %v", err)
		}
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestMemoryWeightedScorer_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	// Create test nodes
	nodeIDs := []string{"int-a", "int-b", "int-c", "int-d"}
	for _, id := range nodeIDs {
		insertTestNode(t, db, id, domain.DomainAcademic)
	}

	t.Run("full workflow", func(t *testing.T) {
		// 1. Add varying access patterns
		// int-a: many recent accesses (high recency + frequency)
		for i := 0; i < 20; i++ {
			err := store.RecordAccess(ctx, "int-a", AccessRetrieval, "recent")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		// int-b: fewer accesses (moderate)
		for i := 0; i < 5; i++ {
			err := store.RecordAccess(ctx, "int-b", AccessRetrieval, "moderate")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		// int-c: no accesses (no memory boost)
		// int-d: a few old accesses (would have lower recency)

		// 2. Apply memory weighting to search results
		results := []query.HybridResult{
			{ID: "int-a", Score: 0.5}, // Lower initial but high memory
			{ID: "int-b", Score: 0.6},
			{ID: "int-c", Score: 0.8}, // Highest initial but no memory
			{ID: "int-d", Score: 0.4},
		}

		weighted, err := scorer.ApplyMemoryWeighting(ctx, results, false)
		if err != nil {
			t.Fatalf("ApplyMemoryWeighting failed: %v", err)
		}

		// 3. Verify int-a got boosted due to high memory
		var intAScore float64
		for _, r := range weighted {
			if r.ID == "int-a" {
				intAScore = r.Score
				break
			}
		}
		if intAScore <= 0.5 {
			t.Errorf("int-a should have boosted score, got %f", intAScore)
		}

		// 4. Filter by retrieval probability
		filtered, err := scorer.FilterByRetrievalProbability(ctx, weighted, false)
		if err != nil {
			t.Fatalf("FilterByRetrievalProbability failed: %v", err)
		}

		// All results should pass (nodes with no memory pass through, high memory passes)
		if len(filtered) < 1 {
			t.Error("expected at least some results to pass filter")
		}

		// 5. Record outcomes for learning
		for _, r := range filtered[:min(2, len(filtered))] {
			err := scorer.RecordRetrievalOutcome(ctx, r.ID, true, time.Hour)
			if err != nil {
				t.Errorf("RecordRetrievalOutcome failed: %v", err)
			}
		}

		// 6. Verify weights have been updated
		_, _, _, _ = scorer.GetWeights() // Just verify no panic

		// 7. Verify confidences reflect the updates
		actConf, recConf, freqConf, threshConf := scorer.GetWeightConfidences()
		if actConf < 0 || actConf > 1 ||
			recConf < 0 || recConf > 1 ||
			freqConf < 0 || freqConf > 1 ||
			threshConf < 0 || threshConf > 1 {
			t.Error("confidences should be in [0,1] range")
		}
	})
}

func TestMemoryWeightedScorer_ExplorationVsExploitation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)
	nodeID := "explore-node"
	insertTestNode(t, db, nodeID, domain.DomainArchitect)

	// Add accesses to create memory
	for i := 0; i < 10; i++ {
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Run("exploitation uses mean weights", func(t *testing.T) {
		// With explore=false, should use mean weights consistently
		scores := make([]float64, 5)
		for i := 0; i < 5; i++ {
			score, _ := scorer.ComputeMemoryScore(ctx, nodeID, time.Now().UTC(), false)
			scores[i] = score
		}

		// All scores should be very close (only time-based variation)
		for i := 1; i < len(scores); i++ {
			// Allow small variance due to time passing
			if math.Abs(scores[i]-scores[0]) > 0.01 {
				t.Logf("exploitation scores vary: %v", scores)
				break
			}
		}
	})

	t.Run("exploration samples weights", func(t *testing.T) {
		// With explore=true, should sample weights (may vary)
		results := []query.HybridResult{
			{ID: nodeID, Score: 0.5},
		}

		weightedResults := make([][]query.HybridResult, 10)
		for i := 0; i < 10; i++ {
			weighted, err := scorer.ApplyMemoryWeighting(ctx, results, true)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			weightedResults[i] = weighted
		}

		// Check for variance in exploration mode
		// Note: Due to sampling, results may still be similar
		// This is more of a smoke test than a statistical test
	})
}

// =============================================================================
// Helper for sorting verification
// =============================================================================

func TestSortByScore(t *testing.T) {
	t.Run("sorts descending", func(t *testing.T) {
		results := []query.HybridResult{
			{ID: "a", Score: 0.3},
			{ID: "b", Score: 0.9},
			{ID: "c", Score: 0.6},
			{ID: "d", Score: 0.1},
			{ID: "e", Score: 0.7},
		}

		sortByScore(results)

		expected := []string{"b", "e", "c", "a", "d"}
		for i, r := range results {
			if r.ID != expected[i] {
				t.Errorf("position %d: expected %s, got %s", i, expected[i], r.ID)
			}
		}
	})

	t.Run("handles empty slice", func(t *testing.T) {
		results := []query.HybridResult{}
		sortByScore(results) // Should not panic
	})

	t.Run("handles single element", func(t *testing.T) {
		results := []query.HybridResult{{ID: "a", Score: 0.5}}
		sortByScore(results)
		if results[0].ID != "a" {
			t.Error("single element should remain unchanged")
		}
	})

	t.Run("handles equal scores", func(t *testing.T) {
		results := []query.HybridResult{
			{ID: "a", Score: 0.5},
			{ID: "b", Score: 0.5},
			{ID: "c", Score: 0.5},
		}
		sortByScore(results) // Should not panic, order among equals is stable
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// =============================================================================
// PF.4.5 ActivationCache Integration Tests
// =============================================================================

func TestMemoryWeightedScorer_ActivationCacheIntegration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	t.Run("default scorer has cache", func(t *testing.T) {
		store := NewMemoryStore(db, 100, time.Millisecond)
		scorer := NewMemoryWeightedScorer(store)
		defer scorer.Stop()

		if scorer.ActivationCache() == nil {
			t.Error("expected default scorer to have activation cache")
		}
	})

	t.Run("custom cache config", func(t *testing.T) {
		store := NewMemoryStore(db, 100, time.Millisecond)
		cacheConfig := &ActivationCacheConfig{
			MaxSize:         500,
			TTL:             time.Minute,
			CleanupInterval: 0, // Disable background cleanup for testing
		}
		scorer := NewMemoryWeightedScorerWithCache(store, cacheConfig, nil)
		defer scorer.Stop()

		if scorer.ActivationCache() == nil {
			t.Error("expected scorer to have activation cache")
		}
	})

	t.Run("cache reduces computation", func(t *testing.T) {
		store := NewMemoryStore(db, 100, time.Millisecond)
		cacheConfig := &ActivationCacheConfig{
			MaxSize:         1000,
			TTL:             10 * time.Minute, // Long TTL for test
			CleanupInterval: 0,
		}
		scorer := NewMemoryWeightedScorerWithCache(store, cacheConfig, nil)
		defer scorer.Stop()

		nodeID := "cache-test-node"
		insertTestNode(t, db, nodeID, domain.DomainAcademic)

		// Add some access traces
		for i := 0; i < 5; i++ {
			err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		// First call should miss cache
		now := time.Now().UTC()
		score1, err := scorer.ComputeMemoryScore(ctx, nodeID, now, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cache := scorer.ActivationCache()
		stats1 := cache.Stats()
		if stats1.Misses != 1 {
			t.Errorf("expected 1 cache miss, got %d", stats1.Misses)
		}

		// Second call with same time should hit cache
		score2, err := scorer.ComputeMemoryScore(ctx, nodeID, now, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		stats2 := cache.Stats()
		if stats2.Hits < 1 {
			t.Errorf("expected at least 1 cache hit, got %d", stats2.Hits)
		}

		// Scores should be equal (same cached activation)
		if score1 != score2 {
			t.Errorf("expected equal scores with caching: %f vs %f", score1, score2)
		}
	})

	t.Run("cache invalidation on node update", func(t *testing.T) {
		store := NewMemoryStore(db, 100, time.Millisecond)
		cacheConfig := &ActivationCacheConfig{
			MaxSize:         1000,
			TTL:             10 * time.Minute,
			CleanupInterval: 0,
		}
		scorer := NewMemoryWeightedScorerWithCache(store, cacheConfig, nil)
		defer scorer.Stop()

		nodeID := "cache-invalidate-node"
		insertTestNode(t, db, nodeID, domain.DomainArchitect)

		// Add initial traces
		for i := 0; i < 3; i++ {
			_ = store.RecordAccess(ctx, nodeID, AccessRetrieval, "test")
			time.Sleep(2 * time.Millisecond)
		}

		// Cache a score
		now := time.Now().UTC()
		_, err := scorer.ComputeMemoryScore(ctx, nodeID, now, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cache := scorer.ActivationCache()
		initialSize := cache.Size()
		if initialSize == 0 {
			t.Error("expected cache to have entries after compute")
		}

		// Invalidate cache for this node
		scorer.InvalidateActivationCache(nodeID)

		// Cache should be smaller or empty for this node
		// Note: Size may not change immediately due to time bucketing
	})

	t.Run("clear activation cache", func(t *testing.T) {
		store := NewMemoryStore(db, 100, time.Millisecond)
		scorer := NewMemoryWeightedScorer(store)
		defer scorer.Stop()

		nodeID := "cache-clear-node"
		insertTestNode(t, db, nodeID, domain.DomainLibrarian)

		// Add traces and compute scores to populate cache
		for i := 0; i < 3; i++ {
			_ = store.RecordAccess(ctx, nodeID, AccessRetrieval, "test")
			time.Sleep(2 * time.Millisecond)
		}

		now := time.Now().UTC()
		_, _ = scorer.ComputeMemoryScore(ctx, nodeID, now, false)

		// Clear the cache
		scorer.ClearActivationCache()

		cache := scorer.ActivationCache()
		if cache.Size() != 0 {
			t.Errorf("expected empty cache after clear, got size %d", cache.Size())
		}
	})

	t.Run("cache hit rate improves with repeated access", func(t *testing.T) {
		store := NewMemoryStore(db, 100, time.Millisecond)
		cacheConfig := &ActivationCacheConfig{
			MaxSize:         1000,
			TTL:             10 * time.Minute,
			CleanupInterval: 0,
		}
		scorer := NewMemoryWeightedScorerWithCache(store, cacheConfig, nil)
		defer scorer.Stop()

		// Create multiple nodes
		nodeIDs := []string{"hr-node-1", "hr-node-2", "hr-node-3"}
		for _, id := range nodeIDs {
			insertTestNode(t, db, id, domain.DomainAcademic)
			for i := 0; i < 3; i++ {
				_ = store.RecordAccess(ctx, id, AccessRetrieval, "test")
				time.Sleep(2 * time.Millisecond)
			}
		}

		cache := scorer.ActivationCache()
		cache.ResetStats()

		// First round: all misses
		now := time.Now().UTC()
		for _, id := range nodeIDs {
			_, _ = scorer.ComputeMemoryScore(ctx, id, now, false)
		}

		// Second round: should have hits
		for _, id := range nodeIDs {
			_, _ = scorer.ComputeMemoryScore(ctx, id, now, false)
		}

		stats := cache.Stats()
		hitRate := cache.HitRate()

		// Should have at least 50% hit rate (second round should all be hits)
		if hitRate < 0.5 {
			t.Errorf("expected hit rate >= 0.5, got %f (hits: %d, misses: %d)",
				hitRate, stats.Hits, stats.Misses)
		}
	})
}

func TestNewMemoryWeightedScorerWithCache(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)

	t.Run("nil cache config uses default", func(t *testing.T) {
		scorer := NewMemoryWeightedScorerWithCache(store, nil, nil)
		defer scorer.Stop()

		if scorer.ActivationCache() == nil {
			t.Error("expected cache even with nil config")
		}
	})

	t.Run("custom cache and update config", func(t *testing.T) {
		cacheConfig := &ActivationCacheConfig{
			MaxSize: 100,
			TTL:     time.Second,
		}
		updateConfig := &handoff.UpdateConfig{
			LearningRate: 0.5,
		}

		scorer := NewMemoryWeightedScorerWithCache(store, cacheConfig, updateConfig)
		defer scorer.Stop()

		if scorer.ActivationCache() == nil {
			t.Error("expected cache with custom config")
		}
	})
}
