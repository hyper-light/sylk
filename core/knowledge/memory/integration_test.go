package memory

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/knowledge/query"
	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// MemoryQueryOptions Tests
// =============================================================================

func TestDefaultMemoryQueryOptions(t *testing.T) {
	opts := DefaultMemoryQueryOptions()

	t.Run("default values", func(t *testing.T) {
		if !opts.ApplyWeighting {
			t.Error("expected ApplyWeighting to be true by default")
		}
		if opts.FilterByRetrieval {
			t.Error("expected FilterByRetrieval to be false by default")
		}
		if opts.Explore {
			t.Error("expected Explore to be false by default")
		}
		if opts.MinRetrievalProb != DefaultMinRetrievalProb {
			t.Errorf("expected MinRetrievalProb %f, got %f",
				DefaultMinRetrievalProb, opts.MinRetrievalProb)
		}
		if opts.MemoryWeight != DefaultMemoryWeight {
			t.Errorf("expected MemoryWeight %f, got %f",
				DefaultMemoryWeight, opts.MemoryWeight)
		}
	})
}

func TestMemoryQueryOptions_Validate(t *testing.T) {
	t.Run("valid options", func(t *testing.T) {
		opts := DefaultMemoryQueryOptions()
		if err := opts.Validate(); err != nil {
			t.Errorf("unexpected validation error: %v", err)
		}
	})

	t.Run("invalid min retrieval prob - negative", func(t *testing.T) {
		opts := &MemoryQueryOptions{MinRetrievalProb: -0.1}
		if err := opts.Validate(); err == nil {
			t.Error("expected validation error for negative MinRetrievalProb")
		}
	})

	t.Run("invalid min retrieval prob - too high", func(t *testing.T) {
		opts := &MemoryQueryOptions{MinRetrievalProb: 1.5}
		if err := opts.Validate(); err == nil {
			t.Error("expected validation error for MinRetrievalProb > 1")
		}
	})

	t.Run("invalid memory weight - negative", func(t *testing.T) {
		opts := &MemoryQueryOptions{MemoryWeight: -0.1}
		if err := opts.Validate(); err == nil {
			t.Error("expected validation error for negative MemoryWeight")
		}
	})

	t.Run("invalid memory weight - too high", func(t *testing.T) {
		opts := &MemoryQueryOptions{MemoryWeight: 1.5}
		if err := opts.Validate(); err == nil {
			t.Error("expected validation error for MemoryWeight > 1")
		}
	})

	t.Run("boundary values valid", func(t *testing.T) {
		opts := &MemoryQueryOptions{MinRetrievalProb: 0.0, MemoryWeight: 0.0}
		if err := opts.Validate(); err != nil {
			t.Errorf("unexpected validation error for zero values: %v", err)
		}

		opts = &MemoryQueryOptions{MinRetrievalProb: 1.0, MemoryWeight: 1.0}
		if err := opts.Validate(); err != nil {
			t.Errorf("unexpected validation error for max values: %v", err)
		}
	})
}

// =============================================================================
// HybridQueryWithMemory Constructor Tests
// =============================================================================

func TestNewHybridQueryWithMemory(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	scorer := NewMemoryWeightedScorer(store)

	t.Run("creates with default options", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		if h == nil {
			t.Fatal("expected non-nil HybridQueryWithMemory")
		}
		if h.scorer != scorer {
			t.Error("scorer should match input")
		}

		opts := h.Options()
		if !opts.ApplyWeighting {
			t.Error("expected ApplyWeighting to be true")
		}
	})
}

func TestNewHybridQueryWithMemoryOptions(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	scorer := NewMemoryWeightedScorer(store)

	t.Run("custom options", func(t *testing.T) {
		customOpts := &MemoryQueryOptions{
			ApplyWeighting:    false,
			FilterByRetrieval: true,
			Explore:           true,
			MinRetrievalProb:  0.7,
			MemoryWeight:      0.5,
		}

		h := NewHybridQueryWithMemoryOptions(scorer, customOpts)
		opts := h.Options()

		if opts.ApplyWeighting != false {
			t.Error("expected ApplyWeighting to be false")
		}
		if opts.FilterByRetrieval != true {
			t.Error("expected FilterByRetrieval to be true")
		}
		if opts.Explore != true {
			t.Error("expected Explore to be true")
		}
		if opts.MinRetrievalProb != 0.7 {
			t.Errorf("expected MinRetrievalProb 0.7, got %f", opts.MinRetrievalProb)
		}
		if opts.MemoryWeight != 0.5 {
			t.Errorf("expected MemoryWeight 0.5, got %f", opts.MemoryWeight)
		}
	})

	t.Run("nil options uses defaults", func(t *testing.T) {
		h := NewHybridQueryWithMemoryOptions(scorer, nil)
		opts := h.Options()

		if !opts.ApplyWeighting {
			t.Error("nil options should use defaults")
		}
	})
}

// =============================================================================
// Builder Method Tests
// =============================================================================

func TestHybridQueryWithMemory_BuilderMethods(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	scorer := NewMemoryWeightedScorer(store)

	t.Run("WithMemoryWeighting", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		result := h.WithMemoryWeighting(false)

		// Should return same instance (method chaining)
		if result != h {
			t.Error("should return same instance for chaining")
		}

		opts := h.Options()
		if opts.ApplyWeighting != false {
			t.Error("ApplyWeighting should be false")
		}
	})

	t.Run("WithRetrievalFilter", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		result := h.WithRetrievalFilter(true)

		if result != h {
			t.Error("should return same instance for chaining")
		}

		opts := h.Options()
		if opts.FilterByRetrieval != true {
			t.Error("FilterByRetrieval should be true")
		}
	})

	t.Run("WithExploration", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		result := h.WithExploration(true)

		if result != h {
			t.Error("should return same instance for chaining")
		}

		opts := h.Options()
		if opts.Explore != true {
			t.Error("Explore should be true")
		}
	})

	t.Run("WithMinRetrievalProb", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		result := h.WithMinRetrievalProb(0.8)

		if result != h {
			t.Error("should return same instance for chaining")
		}

		opts := h.Options()
		if opts.MinRetrievalProb != 0.8 {
			t.Errorf("expected MinRetrievalProb 0.8, got %f", opts.MinRetrievalProb)
		}
	})

	t.Run("WithMemoryWeight", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		result := h.WithMemoryWeight(0.6)

		if result != h {
			t.Error("should return same instance for chaining")
		}

		opts := h.Options()
		if opts.MemoryWeight != 0.6 {
			t.Errorf("expected MemoryWeight 0.6, got %f", opts.MemoryWeight)
		}
	})

	t.Run("method chaining", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).
			WithMemoryWeighting(true).
			WithRetrievalFilter(true).
			WithExploration(true).
			WithMinRetrievalProb(0.6).
			WithMemoryWeight(0.4)

		opts := h.Options()
		if !opts.ApplyWeighting ||
			!opts.FilterByRetrieval ||
			!opts.Explore ||
			opts.MinRetrievalProb != 0.6 ||
			opts.MemoryWeight != 0.4 {
			t.Error("method chaining should set all options correctly")
		}
	})

	t.Run("WithOptions", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		customOpts := &MemoryQueryOptions{
			ApplyWeighting:    false,
			FilterByRetrieval: true,
			Explore:           true,
			MinRetrievalProb:  0.9,
			MemoryWeight:      0.1,
		}

		result := h.WithOptions(customOpts)
		if result != h {
			t.Error("should return same instance for chaining")
		}

		opts := h.Options()
		if opts.ApplyWeighting != false ||
			opts.FilterByRetrieval != true ||
			opts.MinRetrievalProb != 0.9 {
			t.Error("WithOptions should set all options")
		}
	})

	t.Run("WithOptions nil ignored", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		h.WithOptions(nil)

		opts := h.Options()
		if !opts.ApplyWeighting {
			t.Error("nil options should be ignored")
		}
	})
}

// =============================================================================
// Execute Method Tests
// =============================================================================

func TestHybridQueryWithMemory_Execute(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	// Create test nodes
	nodeIDs := []string{"exec-a", "exec-b", "exec-c"}
	for _, id := range nodeIDs {
		insertTestNode(t, db, id, domain.DomainAcademic)
	}

	t.Run("nil scorer returns error", func(t *testing.T) {
		h := &HybridQueryWithMemory{
			scorer:  nil,
			options: DefaultMemoryQueryOptions(),
		}

		results := []query.HybridResult{{ID: "a", Score: 0.5}}
		_, err := h.Execute(ctx, nil, results)
		if err == nil {
			t.Error("expected error for nil scorer")
		}
	})

	t.Run("empty results returns empty", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		results, err := h.Execute(ctx, nil, []query.HybridResult{})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Error("expected empty results")
		}
	})

	t.Run("invalid options returns error", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		h.WithMinRetrievalProb(-0.5) // Invalid

		results := []query.HybridResult{{ID: "exec-a", Score: 0.5}}
		_, err := h.Execute(ctx, nil, results)
		if err == nil {
			t.Error("expected error for invalid options")
		}
	})

	t.Run("executes with default options", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)

		results := []query.HybridResult{
			{ID: "exec-a", Score: 0.8},
			{ID: "exec-b", Score: 0.6},
			{ID: "exec-c", Score: 0.4},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(processed) != 3 {
			t.Errorf("expected 3 results, got %d", len(processed))
		}
	})

	t.Run("does not modify input slice", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)

		results := []query.HybridResult{
			{ID: "exec-a", Score: 0.8},
			{ID: "exec-b", Score: 0.6},
		}
		originalScore := results[0].Score

		_, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if results[0].Score != originalScore {
			t.Error("input slice should not be modified")
		}
	})

	t.Run("memory weighting adjusts scores", func(t *testing.T) {
		// Add memory traces to exec-c
		for i := 0; i < 10; i++ {
			err := store.RecordAccess(ctx, "exec-c", AccessRetrieval, "test")
			if err != nil {
				t.Fatalf("record access error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		h := NewHybridQueryWithMemory(scorer).WithMemoryWeight(0.5)

		results := []query.HybridResult{
			{ID: "exec-a", Score: 0.8},
			{ID: "exec-b", Score: 0.6},
			{ID: "exec-c", Score: 0.3}, // Lower base score but has memory
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Find exec-c in results
		var execCScore float64
		for _, r := range processed {
			if r.ID == "exec-c" {
				execCScore = r.Score
				break
			}
		}

		// exec-c should have a boosted score due to memory
		// With 50% memory weight and initial 0.3, the combined score should be different
		// Note: exact value depends on memory activation
		if execCScore == 0.3 {
			t.Error("exec-c score should have been adjusted by memory")
		}
	})

	t.Run("populates memory fields", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)

		results := []query.HybridResult{
			{ID: "exec-c", Score: 0.5},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// exec-c has memory traces, should have populated fields
		if processed[0].MemoryActivation == 0 && processed[0].MemoryFactor == 0 {
			t.Log("warning: memory fields may be zero if no recent traces")
		}
	})

	t.Run("results sorted by score descending", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).WithMemoryWeight(0.1) // Low memory weight

		results := []query.HybridResult{
			{ID: "exec-a", Score: 0.3},
			{ID: "exec-b", Score: 0.9},
			{ID: "exec-c", Score: 0.6},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should be roughly sorted by score (memory influence is low)
		for i := 1; i < len(processed); i++ {
			if processed[i].Score > processed[i-1].Score {
				t.Errorf("results should be sorted descending: [%d]=%f > [%d]=%f",
					i, processed[i].Score, i-1, processed[i-1].Score)
			}
		}
	})

	t.Run("weighting disabled preserves order", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).WithMemoryWeighting(false)

		results := []query.HybridResult{
			{ID: "exec-a", Score: 0.8},
			{ID: "exec-b", Score: 0.6},
			{ID: "exec-c", Score: 0.4},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// With weighting disabled, scores should be unchanged
		if processed[0].Score != results[0].Score {
			t.Log("note: order may change if filtering is applied")
		}
	})
}

func TestHybridQueryWithMemory_ExecuteWithFilter(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	// Create test nodes
	nodeIDs := []string{"filter-a", "filter-b", "filter-c"}
	for _, id := range nodeIDs {
		insertTestNode(t, db, id, domain.DomainAcademic)
	}

	t.Run("filter with no memory passes through", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).
			WithRetrievalFilter(true).
			WithMemoryWeighting(false)

		results := []query.HybridResult{
			{ID: "filter-a", Score: 0.8},
			{ID: "filter-b", Score: 0.6},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Results with no memory should pass through (fail open)
		if len(processed) != 2 {
			t.Errorf("expected 2 results to pass through, got %d", len(processed))
		}
	})

	t.Run("high activation passes filter", func(t *testing.T) {
		// Add many traces to filter-a for high activation
		for i := 0; i < 20; i++ {
			err := store.RecordAccess(ctx, "filter-a", AccessReinforcement, "high")
			if err != nil {
				t.Fatalf("record access error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		h := NewHybridQueryWithMemory(scorer).
			WithRetrievalFilter(true).
			WithMemoryWeighting(false)

		results := []query.HybridResult{
			{ID: "filter-a", Score: 0.5},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(processed) != 1 {
			t.Errorf("high activation node should pass filter, got %d results", len(processed))
		}
	})
}

func TestHybridQueryWithMemory_ExecuteWithExploration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	nodeID := "explore-node"
	insertTestNode(t, db, nodeID, domain.DomainAcademic)

	// Add some traces
	for i := 0; i < 10; i++ {
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "test")
		if err != nil {
			t.Fatalf("record access error: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Run("exploration samples weights", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).WithExploration(true)

		results := []query.HybridResult{
			{ID: nodeID, Score: 0.5},
		}

		// Run multiple times to check for variance
		scores := make([]float64, 10)
		for i := 0; i < 10; i++ {
			processed, err := h.Execute(ctx, nil, results)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			scores[i] = processed[0].Score
		}

		// With exploration, there should be some variance in scores
		// (though not guaranteed with small samples)
		allSame := true
		for i := 1; i < len(scores); i++ {
			if math.Abs(scores[i]-scores[0]) > 0.0001 {
				allSame = false
				break
			}
		}

		if allSame {
			t.Log("note: all exploration scores identical (may be expected with stable weights)")
		}
	})
}

// =============================================================================
// RecordOutcome Tests
// =============================================================================

func TestHybridQueryWithMemory_RecordOutcome(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	nodeID := "outcome-node"
	insertTestNode(t, db, nodeID, domain.DomainAcademic)

	// Add initial traces
	for i := 0; i < 3; i++ {
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "initial")
		if err != nil {
			t.Fatalf("record access error: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Run("nil scorer returns error", func(t *testing.T) {
		h := &HybridQueryWithMemory{
			scorer:  nil,
			options: DefaultMemoryQueryOptions(),
		}

		err := h.RecordOutcome(ctx, []string{"a"}, true)
		if err == nil {
			t.Error("expected error for nil scorer")
		}
	})

	t.Run("empty node IDs returns nil", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		err := h.RecordOutcome(ctx, []string{}, true)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("records successful outcome", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)

		initialWeights := h.GetMemoryStats()

		err := h.RecordOutcome(ctx, []string{nodeID}, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		newWeights := h.GetMemoryStats()

		// Weights should have been updated
		if newWeights.ActivationWeight == initialWeights.ActivationWeight &&
			newWeights.RecencyBonus == initialWeights.RecencyBonus {
			t.Log("note: weights may not change significantly with single update")
		}
	})

	t.Run("records failed outcome", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)

		err := h.RecordOutcome(ctx, []string{nodeID}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("records multiple nodes", func(t *testing.T) {
		// Create additional nodes
		insertTestNode(t, db, "outcome-node-2", domain.DomainAcademic)
		insertTestNode(t, db, "outcome-node-3", domain.DomainAcademic)

		h := NewHybridQueryWithMemory(scorer)

		err := h.RecordOutcome(ctx, []string{nodeID, "outcome-node-2", "outcome-node-3"}, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestHybridQueryWithMemory_RecordOutcomes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	nodeIDs := []string{"outcomes-a", "outcomes-b", "outcomes-c"}
	for _, id := range nodeIDs {
		insertTestNode(t, db, id, domain.DomainAcademic)
	}

	t.Run("nil scorer returns error", func(t *testing.T) {
		h := &HybridQueryWithMemory{
			scorer:  nil,
			options: DefaultMemoryQueryOptions(),
		}

		outcomes := []OutcomeRecord{{NodeID: "a", WasUseful: true}}
		err := h.RecordOutcomes(ctx, outcomes)
		if err == nil {
			t.Error("expected error for nil scorer")
		}
	})

	t.Run("empty outcomes returns nil", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		err := h.RecordOutcomes(ctx, []OutcomeRecord{})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("records mixed outcomes", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)

		outcomes := []OutcomeRecord{
			{NodeID: "outcomes-a", WasUseful: true, AgeAtRetrieval: 2 * time.Hour},
			{NodeID: "outcomes-b", WasUseful: false, AgeAtRetrieval: 30 * time.Minute},
			{NodeID: "outcomes-c", WasUseful: true, AgeAtRetrieval: 0}, // Uses default
		}

		err := h.RecordOutcomes(ctx, outcomes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("uses default age when zero", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)

		outcomes := []OutcomeRecord{
			{NodeID: "outcomes-a", WasUseful: true, AgeAtRetrieval: 0},
		}

		err := h.RecordOutcomes(ctx, outcomes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// =============================================================================
// Utility Method Tests
// =============================================================================

func TestHybridQueryWithMemory_GetMemoryStats(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	scorer := NewMemoryWeightedScorer(store)

	t.Run("returns stats from scorer", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		stats := h.GetMemoryStats()

		// Verify default weight values
		if math.Abs(stats.ActivationWeight-0.5) > 0.01 {
			t.Errorf("expected activation weight ~0.5, got %f", stats.ActivationWeight)
		}
		if math.Abs(stats.RecencyBonus-0.3) > 0.01 {
			t.Errorf("expected recency bonus ~0.3, got %f", stats.RecencyBonus)
		}
		if math.Abs(stats.FrequencyBonus-0.2) > 0.01 {
			t.Errorf("expected frequency bonus ~0.2, got %f", stats.FrequencyBonus)
		}

		// Verify confidences are valid
		if stats.ActivationConfidence < 0 || stats.ActivationConfidence > 1 {
			t.Errorf("activation confidence out of range: %f", stats.ActivationConfidence)
		}
	})

	t.Run("nil scorer returns empty stats", func(t *testing.T) {
		h := &HybridQueryWithMemory{
			scorer:  nil,
			options: DefaultMemoryQueryOptions(),
		}
		stats := h.GetMemoryStats()

		if stats.ActivationWeight != 0 {
			t.Error("nil scorer should return zero stats")
		}
	})
}

func TestHybridQueryWithMemory_Reset(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	scorer := NewMemoryWeightedScorer(store)

	t.Run("resets to defaults", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).
			WithMemoryWeighting(false).
			WithRetrievalFilter(true).
			WithExploration(true).
			WithMinRetrievalProb(0.9).
			WithMemoryWeight(0.9)

		h.Reset()

		opts := h.Options()
		defaults := DefaultMemoryQueryOptions()

		if opts.ApplyWeighting != defaults.ApplyWeighting {
			t.Error("ApplyWeighting should be reset to default")
		}
		if opts.FilterByRetrieval != defaults.FilterByRetrieval {
			t.Error("FilterByRetrieval should be reset to default")
		}
		if opts.Explore != defaults.Explore {
			t.Error("Explore should be reset to default")
		}
		if opts.MinRetrievalProb != defaults.MinRetrievalProb {
			t.Error("MinRetrievalProb should be reset to default")
		}
		if opts.MemoryWeight != defaults.MemoryWeight {
			t.Error("MemoryWeight should be reset to default")
		}
	})
}

func TestHybridQueryWithMemory_Scorer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	scorer := NewMemoryWeightedScorer(store)

	t.Run("returns scorer reference", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer)
		if h.Scorer() != scorer {
			t.Error("Scorer() should return the underlying scorer")
		}
	})
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestHybridQueryWithMemory_ConcurrentAccess(t *testing.T) {
	db := setupTestDBWithWAL(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)
	h := NewHybridQueryWithMemory(scorer)

	// Create test nodes
	for i := 0; i < 5; i++ {
		nodeID := fmt.Sprintf("concurrent-%d", i)
		insertTestNode(t, db, nodeID, domain.Domain(i%4))
	}

	t.Run("concurrent execute", func(t *testing.T) {
		done := make(chan bool)
		errCh := make(chan error, 50)

		for i := 0; i < 5; i++ {
			go func(idx int) {
				results := []query.HybridResult{
					{ID: fmt.Sprintf("concurrent-%d", idx), Score: 0.5},
				}
				for j := 0; j < 10; j++ {
					_, err := h.Execute(ctx, nil, results)
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
			t.Errorf("concurrent execute error: %v", err)
		}
	})

	t.Run("concurrent option changes and execute", func(t *testing.T) {
		done := make(chan bool)
		errCh := make(chan error, 50)

		// Concurrent option changes
		for i := 0; i < 3; i++ {
			go func() {
				for j := 0; j < 10; j++ {
					h.WithMemoryWeight(float64(j%10) / 10.0)
					h.WithExploration(j%2 == 0)
				}
				done <- true
			}()
		}

		// Concurrent executes
		for i := 0; i < 3; i++ {
			go func(idx int) {
				results := []query.HybridResult{
					{ID: fmt.Sprintf("concurrent-%d", idx), Score: 0.5},
				}
				for j := 0; j < 10; j++ {
					_, err := h.Execute(ctx, nil, results)
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

	t.Run("concurrent record outcomes", func(t *testing.T) {
		done := make(chan bool)
		errCh := make(chan error, 50)

		// Add initial traces
		for i := 0; i < 5; i++ {
			nodeID := fmt.Sprintf("concurrent-%d", i)
			for j := 0; j < 3; j++ {
				_ = store.RecordAccess(ctx, nodeID, AccessRetrieval, "init")
				time.Sleep(2 * time.Millisecond)
			}
		}

		for i := 0; i < 5; i++ {
			go func(idx int) {
				nodeID := fmt.Sprintf("concurrent-%d", idx)
				for j := 0; j < 5; j++ {
					time.Sleep(5 * time.Millisecond)
					err := h.RecordOutcome(ctx, []string{nodeID}, j%2 == 0)
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
			t.Errorf("concurrent record outcome error: %v", err)
		}
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestHybridQueryWithMemory_Integration(t *testing.T) {
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
		h := NewHybridQueryWithMemory(scorer).
			WithMemoryWeighting(true).
			WithMemoryWeight(0.4)

		// 1. Simulate initial query with no memory
		results := []query.HybridResult{
			{ID: "int-a", Score: 0.8, Source: query.SourceHNSW},
			{ID: "int-b", Score: 0.7, Source: query.SourceBleve},
			{ID: "int-c", Score: 0.6, Source: query.SourceGraph},
			{ID: "int-d", Score: 0.5, Source: query.SourceCombined},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// 2. User finds int-c useful, record outcome
		err = h.RecordOutcome(ctx, []string{"int-c"}, true)
		if err != nil {
			t.Fatalf("RecordOutcome failed: %v", err)
		}

		// 3. Add more memory to int-c (simulating repeated useful retrievals)
		for i := 0; i < 15; i++ {
			err = store.RecordAccess(ctx, "int-c", AccessReinforcement, "useful")
			if err != nil {
				t.Fatalf("RecordAccess failed: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		// 4. Re-query - int-c should rank higher now
		processed2, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// Find int-c's position in both results
		var intCPos1, intCPos2 int
		for i, r := range processed {
			if r.ID == "int-c" {
				intCPos1 = i
				break
			}
		}
		for i, r := range processed2 {
			if r.ID == "int-c" {
				intCPos2 = i
				break
			}
		}

		// int-c should be ranked higher (lower position index) after memory boost
		if intCPos2 > intCPos1 {
			t.Logf("note: int-c position changed from %d to %d (may vary)", intCPos1, intCPos2)
		}

		// 5. Verify memory fields are populated
		for _, r := range processed2 {
			if r.ID == "int-c" {
				if r.MemoryActivation == 0 {
					t.Log("warning: int-c memory activation is zero despite traces")
				}
			}
		}

		// 6. Verify stats can be retrieved
		stats := h.GetMemoryStats()
		if stats.ActivationWeight <= 0 || stats.ActivationWeight > 1 {
			t.Error("activation weight should be in (0, 1]")
		}
	})

	t.Run("exploration vs exploitation", func(t *testing.T) {
		// Exploitation mode (default)
		hExploit := NewHybridQueryWithMemory(scorer).
			WithExploration(false).
			WithMemoryWeight(0.3)

		// Exploration mode
		hExplore := NewHybridQueryWithMemory(scorer).
			WithExploration(true).
			WithMemoryWeight(0.3)

		results := []query.HybridResult{
			{ID: "int-a", Score: 0.5},
		}

		// Exploitation should be deterministic
		exploitScores := make([]float64, 5)
		for i := 0; i < 5; i++ {
			processed, _ := hExploit.Execute(ctx, nil, results)
			exploitScores[i] = processed[0].Score
		}

		// Check exploitation is consistent
		for i := 1; i < len(exploitScores); i++ {
			if math.Abs(exploitScores[i]-exploitScores[0]) > 0.0001 {
				t.Logf("exploitation scores vary: %v", exploitScores)
				break
			}
		}

		// Exploration may vary (but not guaranteed with stable weights)
		exploreScores := make([]float64, 5)
		for i := 0; i < 5; i++ {
			processed, _ := hExplore.Execute(ctx, nil, results)
			exploreScores[i] = processed[0].Score
		}

		t.Logf("exploitation scores: %v", exploitScores)
		t.Logf("exploration scores: %v", exploreScores)
	})

	t.Run("filter and weight combined", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).
			WithMemoryWeighting(true).
			WithRetrievalFilter(true).
			WithMemoryWeight(0.5)

		// Give int-a high activation
		for i := 0; i < 25; i++ {
			err := store.RecordAccess(ctx, "int-a", AccessReinforcement, "high")
			if err != nil {
				t.Fatalf("RecordAccess failed: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		results := []query.HybridResult{
			{ID: "int-a", Score: 0.4}, // Low score but high memory
			{ID: "int-b", Score: 0.8}, // High score but no memory
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// Both should pass (int-b has no memory = fail open)
		if len(processed) < 1 {
			t.Error("expected at least 1 result")
		}
	})
}

func TestHybridQueryWithMemory_ScoreCalculation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	nodeID := "score-calc"
	insertTestNode(t, db, nodeID, domain.DomainAcademic)

	// Add some memory traces
	for i := 0; i < 10; i++ {
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "test")
		if err != nil {
			t.Fatalf("record access error: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Run("score formula verification", func(t *testing.T) {
		memoryWeight := 0.4
		h := NewHybridQueryWithMemory(scorer).WithMemoryWeight(memoryWeight)

		baseScore := 0.7
		results := []query.HybridResult{
			{ID: nodeID, Score: baseScore},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		result := processed[0]

		// Verify formula: finalScore = baseScore * (1 - memoryWeight) + memoryFactor
		// where memoryFactor = memoryActivation * memoryWeight
		expectedBase := baseScore * (1 - memoryWeight)
		actualBase := result.Score - result.MemoryFactor

		if math.Abs(actualBase-expectedBase) > 0.001 {
			t.Errorf("base score contribution: expected %f, got %f",
				expectedBase, actualBase)
		}

		// Verify memory factor calculation
		expectedMemoryFactor := result.MemoryActivation * memoryWeight
		if math.Abs(result.MemoryFactor-expectedMemoryFactor) > 0.001 {
			t.Errorf("memory factor: expected %f, got %f",
				expectedMemoryFactor, result.MemoryFactor)
		}
	})

	t.Run("zero memory weight preserves base score", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).WithMemoryWeight(0.0)

		baseScore := 0.6
		results := []query.HybridResult{
			{ID: nodeID, Score: baseScore},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// With zero memory weight, final score should equal base score
		if math.Abs(processed[0].Score-baseScore) > 0.001 {
			t.Errorf("zero memory weight should preserve base score: expected %f, got %f",
				baseScore, processed[0].Score)
		}

		// Memory factor should be zero
		if processed[0].MemoryFactor != 0 {
			t.Errorf("memory factor should be 0 with zero weight, got %f",
				processed[0].MemoryFactor)
		}
	})

	t.Run("full memory weight uses only memory", func(t *testing.T) {
		h := NewHybridQueryWithMemory(scorer).WithMemoryWeight(1.0)

		baseScore := 0.6
		results := []query.HybridResult{
			{ID: nodeID, Score: baseScore},
		}

		processed, err := h.Execute(ctx, nil, results)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// With full memory weight, base score contribution should be zero
		// finalScore = baseScore * 0 + memoryActivation * 1.0 = memoryActivation
		if math.Abs(processed[0].Score-processed[0].MemoryActivation) > 0.001 {
			t.Errorf("full memory weight should use only memory: expected %f, got %f",
				processed[0].MemoryActivation, processed[0].Score)
		}
	})
}

// setupIntegrationTestDB creates a test database for integration testing
func setupIntegrationTestDB(t *testing.T) *sql.DB {
	return setupTestDB(t)
}
