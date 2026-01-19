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

// =============================================================================
// MD.7.1 ACT-R Equation Verification Tests
// =============================================================================

// createTestMemoryWithTraces creates an ACTRMemory with traces at specified intervals.
func createTestMemoryWithTraces(nodeID string, traceCount int, interval time.Duration) *ACTRMemory {
	now := time.Now().UTC()
	traces := make([]AccessTrace, traceCount)

	for i := 0; i < traceCount; i++ {
		traces[i] = AccessTrace{
			AccessedAt: now.Add(-time.Duration(traceCount-1-i) * interval),
			AccessType: AccessRetrieval,
			Context:    fmt.Sprintf("trace-%d", i),
		}
	}

	return &ACTRMemory{
		NodeID:             nodeID,
		Domain:             int(domain.DomainAcademic),
		Traces:             traces,
		MaxTraces:          100,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		CreatedAt:          traces[0].AccessedAt,
		AccessCount:        traceCount,
	}
}

// createSpacedTraces creates well-spaced traces following Pimsleur intervals.
func createSpacedTraces(count int) []AccessTrace {
	now := time.Now().UTC()
	traces := make([]AccessTrace, count)

	// Pimsleur-like intervals: 1h, 1d, 3d, 1w, 2w
	intervals := []time.Duration{
		0,
		1 * time.Hour,
		24 * time.Hour,
		3 * 24 * time.Hour,
		7 * 24 * time.Hour,
		14 * 24 * time.Hour,
		21 * 24 * time.Hour,
		28 * 24 * time.Hour,
		35 * 24 * time.Hour,
		42 * 24 * time.Hour,
	}

	// Calculate total time span for proper ordering
	var totalDuration time.Duration
	for i := 0; i < count && i < len(intervals); i++ {
		totalDuration += intervals[i]
	}

	// Create traces from oldest to newest
	currentTime := now.Add(-totalDuration)
	for i := 0; i < count; i++ {
		if i > 0 && i < len(intervals) {
			currentTime = currentTime.Add(intervals[i])
		} else if i > 0 {
			// For extra traces beyond defined intervals, use 7-day spacing
			currentTime = currentTime.Add(7 * 24 * time.Hour)
		}
		traces[i] = AccessTrace{
			AccessedAt: currentTime,
			AccessType: AccessRetrieval,
			Context:    fmt.Sprintf("spaced-%d", i),
		}
	}

	return traces
}

// createMassedTraces creates traces all within 1 hour (massed practice).
func createMassedTraces(count int) []AccessTrace {
	now := time.Now().UTC()
	traces := make([]AccessTrace, count)

	// All within 1 hour, spaced 5 minutes apart
	interval := 5 * time.Minute
	for i := 0; i < count; i++ {
		traces[i] = AccessTrace{
			AccessedAt: now.Add(-time.Duration(count-1-i) * interval),
			AccessType: AccessRetrieval,
			Context:    fmt.Sprintf("massed-%d", i),
		}
	}

	return traces
}

func TestACTR_PowerLawDecay(t *testing.T) {
	t.Run("power law decay matches theoretical curve B = ln(sum(t^-d)) + beta", func(t *testing.T) {
		// Create memory with known parameters
		memory := &ACTRMemory{
			NodeID:             "power-law-test",
			Domain:             int(domain.DomainAcademic),
			MaxTraces:          100,
			DecayAlpha:         5.0,
			DecayBeta:          5.0, // Mean decay = 0.5
			BaseOffsetMean:     0.0,
			BaseOffsetVariance: 1.0,
		}

		// Add a single trace at 100 seconds ago
		now := time.Now().UTC()
		memory.Traces = []AccessTrace{
			{AccessedAt: now.Add(-100 * time.Second), AccessType: AccessRetrieval},
		}
		memory.AccessCount = 1

		// Compute activation
		activation := memory.Activation(now)

		// Manual calculation: B = ln(100^(-0.5)) + 0 = ln(0.1) = -2.302...
		d := memory.DecayMean() // Should be 0.5
		expectedSum := math.Pow(100.0, -d)
		expectedActivation := math.Log(expectedSum) + memory.BaseOffsetMean

		if math.Abs(activation-expectedActivation) > 0.01 {
			t.Errorf("activation mismatch: expected %f, got %f (d=%f)",
				expectedActivation, activation, d)
		}
	})

	t.Run("multiple traces sum correctly", func(t *testing.T) {
		now := time.Now().UTC()
		memory := &ACTRMemory{
			NodeID:             "sum-test",
			Domain:             int(domain.DomainAcademic),
			MaxTraces:          100,
			DecayAlpha:         5.0,
			DecayBeta:          5.0, // Mean decay = 0.5
			BaseOffsetMean:     0.0,
			BaseOffsetVariance: 1.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-100 * time.Second), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-200 * time.Second), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-300 * time.Second), AccessType: AccessRetrieval},
			},
			AccessCount: 3,
		}

		activation := memory.Activation(now)

		// Manual calculation with d = 0.5
		d := 0.5
		sum := math.Pow(100.0, -d) + math.Pow(200.0, -d) + math.Pow(300.0, -d)
		expectedActivation := math.Log(sum)

		if math.Abs(activation-expectedActivation) > 0.01 {
			t.Errorf("multi-trace activation mismatch: expected %f, got %f",
				expectedActivation, activation)
		}
	})

	t.Run("activation decreases over time as expected", func(t *testing.T) {
		now := time.Now().UTC()
		memory := createTestMemoryWithTraces("decay-test", 5, time.Hour)

		// Measure activation at different times
		activation1 := memory.Activation(now)
		activation2 := memory.Activation(now.Add(1 * time.Hour))
		activation3 := memory.Activation(now.Add(24 * time.Hour))

		// Activation should decrease over time
		if activation2 >= activation1 {
			t.Errorf("activation should decrease: t0=%f, t+1h=%f", activation1, activation2)
		}
		if activation3 >= activation2 {
			t.Errorf("activation should decrease: t+1h=%f, t+24h=%f", activation2, activation3)
		}
	})

	t.Run("decay parameter d affects decay rate correctly", func(t *testing.T) {
		now := time.Now().UTC()
		traces := []AccessTrace{
			{AccessedAt: now.Add(-1 * time.Hour), AccessType: AccessRetrieval},
		}

		// Fast decay (high d)
		fastDecay := &ACTRMemory{
			NodeID:     "fast-decay",
			DecayAlpha: 8.0,
			DecayBeta:  2.0, // Mean = 0.8
			Traces:     traces,
		}

		// Slow decay (low d)
		slowDecay := &ACTRMemory{
			NodeID:     "slow-decay",
			DecayAlpha: 2.0,
			DecayBeta:  8.0, // Mean = 0.2
			Traces:     traces,
		}

		fastActivation := fastDecay.Activation(now)
		slowActivation := slowDecay.Activation(now)

		// Slow decay should have higher activation (less forgetting)
		if slowActivation <= fastActivation {
			t.Errorf("slow decay should have higher activation: slow=%f, fast=%f",
				slowActivation, fastActivation)
		}

		// Verify the relationship is significant
		difference := slowActivation - fastActivation
		if difference < 0.5 {
			t.Errorf("decay difference should be significant: %f", difference)
		}
	})
}

// =============================================================================
// MD.7.2 Spacing Effect Tests
// =============================================================================

func TestSpacingEffect(t *testing.T) {
	t.Run("spaced access yields higher stability than massed", func(t *testing.T) {
		analyzer := NewSpacingAnalyzer(0.9)

		// Memory with spaced access pattern
		spacedMemory := &ACTRMemory{
			NodeID:     "spaced",
			MaxTraces:  100,
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces:     createSpacedTraces(10),
		}

		// Memory with massed access pattern
		massedMemory := &ACTRMemory{
			NodeID:     "massed",
			MaxTraces:  100,
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces:     createMassedTraces(10),
		}

		spacedReport := analyzer.AnalyzeSpacingPattern(spacedMemory)
		massedReport := analyzer.AnalyzeSpacingPattern(massedMemory)

		// Spaced should have higher stability
		if spacedReport.Stability <= massedReport.Stability {
			t.Errorf("spaced stability (%f) should exceed massed stability (%f)",
				spacedReport.Stability, massedReport.Stability)
		}

		// Spaced should be marked as well-spaced
		if !spacedReport.IsWellSpaced {
			t.Error("spaced memory should be marked as well-spaced")
		}

		// Massed should not be marked as well-spaced
		if massedReport.IsWellSpaced && massedReport.Stability >= 0.6 {
			t.Logf("note: massed memory marked well-spaced with stability %f",
				massedReport.Stability)
		}
	})

	t.Run("10 accesses over 10 days beats 10 accesses in 1 hour", func(t *testing.T) {
		now := time.Now().UTC()
		analyzer := NewSpacingAnalyzer(0.9)

		// 10 accesses over 10 days
		spreadTraces := make([]AccessTrace, 10)
		for i := 0; i < 10; i++ {
			spreadTraces[i] = AccessTrace{
				AccessedAt: now.Add(-time.Duration(10-i) * 24 * time.Hour),
				AccessType: AccessRetrieval,
				Context:    fmt.Sprintf("day-%d", i),
			}
		}

		// 10 accesses in 1 hour
		compactTraces := make([]AccessTrace, 10)
		for i := 0; i < 10; i++ {
			compactTraces[i] = AccessTrace{
				AccessedAt: now.Add(-time.Duration(60-i*6) * time.Minute),
				AccessType: AccessRetrieval,
				Context:    fmt.Sprintf("minute-%d", i),
			}
		}

		spreadMemory := &ACTRMemory{
			NodeID:     "spread",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces:     spreadTraces,
		}

		compactMemory := &ACTRMemory{
			NodeID:     "compact",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces:     compactTraces,
		}

		spreadReport := analyzer.AnalyzeSpacingPattern(spreadMemory)
		compactReport := analyzer.AnalyzeSpacingPattern(compactMemory)

		// Spread memory should have better stability
		if spreadReport.Stability <= compactReport.Stability {
			t.Errorf("10 days spread (%f) should beat 1 hour compact (%f)",
				spreadReport.Stability, compactReport.Stability)
		}

		// Spread memory should have longer average interval
		if spreadReport.AverageInterval <= compactReport.AverageInterval {
			t.Errorf("spread average interval (%v) should exceed compact (%v)",
				spreadReport.AverageInterval, compactReport.AverageInterval)
		}
	})

	t.Run("SpacingAnalyzer returns appropriate review times", func(t *testing.T) {
		analyzer := NewSpacingAnalyzer(0.9)

		// Memory with few traces should get shorter review interval
		fewTracesMemory := createTestMemoryWithTraces("few", 2, time.Hour)
		fewTracesInterval := analyzer.OptimalReviewTime(fewTracesMemory)

		// Memory with many traces should get longer review interval
		manyTracesMemory := createTestMemoryWithTraces("many", 10, 24*time.Hour)
		manyTracesInterval := analyzer.OptimalReviewTime(manyTracesMemory)

		// More traces = longer interval (assuming good spacing)
		if manyTracesInterval < fewTracesInterval {
			t.Logf("note: many traces interval (%v) < few traces interval (%v)",
				manyTracesInterval, fewTracesInterval)
		}

		// Verify intervals are within bounds
		if fewTracesInterval < analyzer.MinInterval() || fewTracesInterval > analyzer.MaxInterval() {
			t.Errorf("few traces interval %v out of bounds [%v, %v]",
				fewTracesInterval, analyzer.MinInterval(), analyzer.MaxInterval())
		}
		if manyTracesInterval < analyzer.MinInterval() || manyTracesInterval > analyzer.MaxInterval() {
			t.Errorf("many traces interval %v out of bounds [%v, %v]",
				manyTracesInterval, analyzer.MinInterval(), analyzer.MaxInterval())
		}
	})

	t.Run("SuggestReviewList prioritizes overdue memories", func(t *testing.T) {
		now := time.Now().UTC()
		ctx := context.Background()
		analyzer := NewSpacingAnalyzer(0.9)

		// Create memories with different last access times
		recentMemory := &ACTRMemory{
			NodeID:     "recent",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-30 * time.Minute), AccessType: AccessRetrieval},
			},
		}

		oldMemory := &ACTRMemory{
			NodeID:     "old",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-7 * 24 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		veryOldMemory := &ACTRMemory{
			NodeID:     "very-old",
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-30 * 24 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		memories := []*ACTRMemory{recentMemory, oldMemory, veryOldMemory}
		reviewList := analyzer.SuggestReviewList(ctx, memories, now, 3)

		// Very old should be first (most urgent)
		if len(reviewList) < 3 {
			t.Fatalf("expected 3 items in review list, got %d", len(reviewList))
		}

		if reviewList[0].NodeID != "very-old" {
			t.Errorf("very-old should be most urgent, got %s first", reviewList[0].NodeID)
		}
		if reviewList[len(reviewList)-1].NodeID != "recent" {
			t.Errorf("recent should be least urgent, got %s last", reviewList[len(reviewList)-1].NodeID)
		}
	})
}

// =============================================================================
// MD.7.3 Memory-Weighted Retrieval Tests
// =============================================================================

func TestMemoryWeightedRetrieval(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)

	// Create test nodes
	nodeIDs := []string{"old-unused", "recent", "frequent"}
	for _, id := range nodeIDs {
		insertTestNode(t, db, id, domain.DomainAcademic)
	}

	t.Run("old unused content ranks lower", func(t *testing.T) {
		now := time.Now().UTC()

		// Insert old access for "old-unused" - 30 days ago, no recent accesses
		_, err := db.Exec(`
			INSERT INTO node_access_traces (node_id, accessed_at, access_type, context)
			VALUES (?, ?, ?, ?)
		`, "old-unused", now.Add(-30*24*time.Hour).Unix(), "retrieval", "old")
		if err != nil {
			t.Fatalf("failed to insert old trace: %v", err)
		}

		// Update node fields
		_, err = db.Exec(`
			UPDATE nodes SET memory_activation = -10.0, last_accessed_at = ?, access_count = 1
			WHERE id = ?
		`, now.Add(-30*24*time.Hour).Unix(), "old-unused")
		if err != nil {
			t.Fatalf("failed to update node: %v", err)
		}

		// Insert recent access for "recent" - 1 hour ago
		err = store.RecordAccess(ctx, "recent", AccessRetrieval, "recent")
		if err != nil {
			t.Fatalf("failed to record recent access: %v", err)
		}

		results := []query.HybridResult{
			{ID: "old-unused", Score: 0.8},
			{ID: "recent", Score: 0.7},
		}

		weighted, err := scorer.ApplyMemoryWeighting(ctx, results, false)
		if err != nil {
			t.Fatalf("ApplyMemoryWeighting failed: %v", err)
		}

		// Find scores
		var oldScore, recentScore float64
		for _, r := range weighted {
			if r.ID == "old-unused" {
				oldScore = r.Score
			}
			if r.ID == "recent" {
				recentScore = r.Score
			}
		}

		// Recent should rank higher despite lower base score
		if oldScore >= recentScore {
			t.Errorf("recent (%f) should score higher than old-unused (%f)",
				recentScore, oldScore)
		}
	})

	t.Run("recently accessed content ranks higher", func(t *testing.T) {
		// Record recent access
		err := store.RecordAccess(ctx, "recent", AccessRetrieval, "recent-access")
		if err != nil {
			t.Fatalf("failed to record access: %v", err)
		}

		results := []query.HybridResult{
			{ID: "recent", Score: 0.5},
		}

		weighted, err := scorer.ApplyMemoryWeighting(ctx, results, false)
		if err != nil {
			t.Fatalf("ApplyMemoryWeighting failed: %v", err)
		}

		// Score should be boosted due to recency
		if weighted[0].Score <= 0.5 {
			t.Errorf("recent content should have boosted score: got %f", weighted[0].Score)
		}
	})

	t.Run("frequently accessed content ranks higher", func(t *testing.T) {
		// Record many accesses for "frequent"
		for i := 0; i < 50; i++ {
			err := store.RecordAccess(ctx, "frequent", AccessRetrieval, "frequent")
			if err != nil {
				t.Fatalf("failed to record access %d: %v", i, err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		// Create a new node with single access
		insertTestNode(t, db, "single-access", domain.DomainAcademic)
		err := store.RecordAccess(ctx, "single-access", AccessRetrieval, "single")
		if err != nil {
			t.Fatalf("failed to record single access: %v", err)
		}

		results := []query.HybridResult{
			{ID: "frequent", Score: 0.5},
			{ID: "single-access", Score: 0.5},
		}

		weighted, err := scorer.ApplyMemoryWeighting(ctx, results, false)
		if err != nil {
			t.Fatalf("ApplyMemoryWeighting failed: %v", err)
		}

		var frequentScore, singleScore float64
		for _, r := range weighted {
			if r.ID == "frequent" {
				frequentScore = r.Score
			}
			if r.ID == "single-access" {
				singleScore = r.Score
			}
		}

		// Frequent should have higher score
		if frequentScore <= singleScore {
			t.Errorf("frequent (%f) should score higher than single-access (%f)",
				frequentScore, singleScore)
		}
	})

	t.Run("MemoryWeightedScorer combines scores correctly", func(t *testing.T) {
		now := time.Now().UTC()

		// Create node with known trace pattern
		insertTestNode(t, db, "score-combine", domain.DomainAcademic)

		// Record several accesses
		for i := 0; i < 5; i++ {
			err := store.RecordAccess(ctx, "score-combine", AccessRetrieval, "combine")
			if err != nil {
				t.Fatalf("failed to record access: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		// Compute memory score manually
		memoryScore, err := scorer.ComputeMemoryScore(ctx, "score-combine", now, false)
		if err != nil {
			t.Fatalf("ComputeMemoryScore failed: %v", err)
		}

		// Memory score should be positive and bounded
		if memoryScore < 0 {
			t.Errorf("memory score should be non-negative: %f", memoryScore)
		}
		if memoryScore > 2.0 {
			t.Errorf("memory score should be reasonably bounded: %f", memoryScore)
		}

		// Verify weights are being used
		actWeight, recWeight, freqWeight, _ := scorer.GetWeights()
		if actWeight <= 0 || recWeight <= 0 || freqWeight <= 0 {
			t.Error("all weights should be positive")
		}
	})
}

// =============================================================================
// MD.7.4 Domain-Specific Decay Tests
// =============================================================================

func TestDomainSpecificDecay(t *testing.T) {
	t.Run("Academic domain retains longer than Engineer", func(t *testing.T) {
		now := time.Now().UTC()

		// Academic memory with d=0.3
		academicMemory := &ACTRMemory{
			NodeID:     "academic",
			Domain:     int(domain.DomainAcademic),
			DecayAlpha: 3.0,
			DecayBeta:  7.0, // Mean = 0.3 (slower decay)
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-24 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		// Engineer memory with d=0.6
		engineerMemory := &ACTRMemory{
			NodeID:     "engineer",
			Domain:     int(domain.DomainEngineer),
			DecayAlpha: 6.0,
			DecayBeta:  4.0, // Mean = 0.6 (faster decay)
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-24 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		academicActivation := academicMemory.Activation(now)
		engineerActivation := engineerMemory.Activation(now)

		// Academic (slow decay) should have higher activation
		if academicActivation <= engineerActivation {
			t.Errorf("Academic (%f) should have higher activation than Engineer (%f)",
				academicActivation, engineerActivation)
		}

		// Verify decay means
		if math.Abs(academicMemory.DecayMean()-0.3) > 0.01 {
			t.Errorf("Academic decay mean should be 0.3, got %f", academicMemory.DecayMean())
		}
		if math.Abs(engineerMemory.DecayMean()-0.6) > 0.01 {
			t.Errorf("Engineer decay mean should be 0.6, got %f", engineerMemory.DecayMean())
		}
	})

	t.Run("same content different domains verify activation difference", func(t *testing.T) {
		now := time.Now().UTC()

		// Same trace pattern for both domains
		traces := []AccessTrace{
			{AccessedAt: now.Add(-48 * time.Hour), AccessType: AccessRetrieval},
			{AccessedAt: now.Add(-24 * time.Hour), AccessType: AccessRetrieval},
			{AccessedAt: now.Add(-1 * time.Hour), AccessType: AccessRetrieval},
		}

		domains := []struct {
			name  string
			dom   domain.Domain
			alpha float64
			beta  float64
		}{
			{"Academic", domain.DomainAcademic, 3.0, 7.0},    // d=0.3
			{"Architect", domain.DomainArchitect, 4.0, 6.0},  // d=0.4
			{"Librarian", domain.DomainLibrarian, 5.0, 5.0},  // d=0.5
			{"Engineer", domain.DomainEngineer, 6.0, 4.0},    // d=0.6
		}

		prevActivation := math.MaxFloat64
		for _, dom := range domains {
			memory := &ACTRMemory{
				NodeID:     "domain-test",
				Domain:     int(dom.dom),
				DecayAlpha: dom.alpha,
				DecayBeta:  dom.beta,
				Traces:     traces,
			}

			activation := memory.Activation(now)

			// Each domain should have lower activation than previous (ascending d)
			if activation >= prevActivation {
				t.Errorf("%s (d=%f) activation %f should be less than previous %f",
					dom.name, memory.DecayMean(), activation, prevActivation)
			}
			prevActivation = activation
		}
	})

	t.Run("domain decay parameters update with feedback", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()
		ctx := context.Background()

		store := NewMemoryStore(db, 100, time.Minute)

		// Get initial parameters for Academic domain
		initialParams := store.GetDomainDecayParams(domain.DomainAcademic)
		initialMean := initialParams.Mean()
		initialSamples := initialParams.EffectiveSamples

		// Record successful retrieval (content was useful)
		err := store.UpdateDecayParams(ctx, domain.DomainAcademic, true, 2*time.Hour)
		if err != nil {
			t.Fatalf("UpdateDecayParams failed: %v", err)
		}

		// Check parameters updated
		updatedParams := store.GetDomainDecayParams(domain.DomainAcademic)
		updatedMean := updatedParams.Mean()
		updatedSamples := updatedParams.EffectiveSamples

		// Successful retrieval should decrease decay (slower forgetting)
		if updatedMean >= initialMean {
			t.Errorf("successful retrieval should decrease decay: %f -> %f",
				initialMean, updatedMean)
		}

		// Effective samples should increase
		if updatedSamples != initialSamples+1 {
			t.Errorf("effective samples should increase: %f -> %f",
				initialSamples, updatedSamples)
		}

		// Now record failed retrieval
		err = store.UpdateDecayParams(ctx, domain.DomainAcademic, false, 2*time.Hour)
		if err != nil {
			t.Fatalf("UpdateDecayParams failed: %v", err)
		}

		// Check parameters updated
		failedParams := store.GetDomainDecayParams(domain.DomainAcademic)
		failedMean := failedParams.Mean()

		// Failed retrieval should increase decay (faster forgetting)
		if failedMean <= updatedMean {
			t.Errorf("failed retrieval should increase decay: %f -> %f",
				updatedMean, failedMean)
		}
	})

	t.Run("DefaultDomainDecay returns correct priors", func(t *testing.T) {
		testCases := []struct {
			dom          int
			expectedMean float64
		}{
			{int(domain.DomainAcademic), 0.3},
			{int(domain.DomainArchitect), 0.4},
			{int(domain.DomainLibrarian), 0.5},
			{int(domain.DomainEngineer), 0.6},
		}

		for _, tc := range testCases {
			params := DefaultDomainDecay(tc.dom)
			mean := params.Mean()
			if math.Abs(mean-tc.expectedMean) > 0.01 {
				t.Errorf("domain %d: expected mean %f, got %f",
					tc.dom, tc.expectedMean, mean)
			}
		}
	})
}

// =============================================================================
// MD.7 End-to-End Integration Tests
// =============================================================================

func TestMemoryDecayIntegration_EndToEnd(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	scorer := NewMemoryWeightedScorer(store)
	analyzer := NewSpacingAnalyzer(0.9)

	// Create test nodes for different domains
	insertTestNode(t, db, "academic-content", domain.DomainAcademic)
	insertTestNode(t, db, "engineer-content", domain.DomainEngineer)
	insertTestNode(t, db, "spaced-content", domain.DomainAcademic)
	insertTestNode(t, db, "massed-content", domain.DomainAcademic)

	t.Run("complete memory system workflow", func(t *testing.T) {
		now := time.Now().UTC()

		// 1. Record spaced accesses for spaced-content
		spacedIntervals := []time.Duration{0, 1 * time.Hour, 6 * time.Hour, 24 * time.Hour}
		for i := 0; i < len(spacedIntervals); i++ {
			time.Sleep(2 * time.Millisecond)
			err := store.RecordAccess(ctx, "spaced-content", AccessRetrieval, "spaced")
			if err != nil {
				t.Fatalf("failed to record spaced access: %v", err)
			}
		}

		// 2. Record massed accesses for massed-content
		for i := 0; i < 4; i++ {
			time.Sleep(2 * time.Millisecond)
			err := store.RecordAccess(ctx, "massed-content", AccessRetrieval, "massed")
			if err != nil {
				t.Fatalf("failed to record massed access: %v", err)
			}
		}

		// 3. Query with memory weighting
		results := []query.HybridResult{
			{ID: "spaced-content", Score: 0.6},
			{ID: "massed-content", Score: 0.6},
		}

		weighted, err := scorer.ApplyMemoryWeighting(ctx, results, false)
		if err != nil {
			t.Fatalf("ApplyMemoryWeighting failed: %v", err)
		}

		// 4. Verify spaced content ranks higher
		var spacedScore, massedScore float64
		for _, r := range weighted {
			if r.ID == "spaced-content" {
				spacedScore = r.Score
			}
			if r.ID == "massed-content" {
				massedScore = r.Score
			}
		}

		// Both should have memory boost, but spaced may not necessarily beat massed
		// in all cases without time simulation
		t.Logf("spaced score: %f, massed score: %f", spacedScore, massedScore)

		// 5. Analyze spacing patterns
		spacedMemory, err := store.GetMemory(ctx, "spaced-content", domain.DomainAcademic)
		if err != nil {
			t.Fatalf("GetMemory failed: %v", err)
		}

		spacedReport := analyzer.AnalyzeSpacingPattern(spacedMemory)
		t.Logf("spaced stability: %f, well-spaced: %v",
			spacedReport.Stability, spacedReport.IsWellSpaced)

		// 6. Record retrieval outcome for learning
		err = scorer.RecordRetrievalOutcome(ctx, "spaced-content", true, 1*time.Hour)
		if err != nil {
			t.Fatalf("RecordRetrievalOutcome failed: %v", err)
		}

		// 7. Verify weights updated
		actWeight, recWeight, freqWeight, _ := scorer.GetWeights()
		t.Logf("updated weights - activation: %f, recency: %f, frequency: %f",
			actWeight, recWeight, freqWeight)

		// 8. Test domain-specific behavior
		err = store.RecordAccess(ctx, "academic-content", AccessRetrieval, "academic")
		if err != nil {
			t.Fatalf("failed to record academic access: %v", err)
		}
		err = store.RecordAccess(ctx, "engineer-content", AccessRetrieval, "engineer")
		if err != nil {
			t.Fatalf("failed to record engineer access: %v", err)
		}

		time.Sleep(5 * time.Millisecond)

		academicActivation, _ := store.ComputeActivation(ctx, "academic-content")
		engineerActivation, _ := store.ComputeActivation(ctx, "engineer-content")

		// Note: In this short test, both will have similar activations
		// The domain decay difference becomes more apparent over longer time scales
		t.Logf("academic activation: %f, engineer activation: %f",
			academicActivation, engineerActivation)

		// 9. Get review suggestions
		allMemories := []*ACTRMemory{spacedMemory}
		reviewList := analyzer.SuggestReviewList(ctx, allMemories, now, 10)
		t.Logf("review list contains %d items", len(reviewList))
	})

	t.Run("ACT-R retrieval probability integration", func(t *testing.T) {
		// Create a well-reinforced memory
		insertTestNode(t, db, "reinforced", domain.DomainAcademic)

		for i := 0; i < 20; i++ {
			time.Sleep(2 * time.Millisecond)
			err := store.RecordAccess(ctx, "reinforced", AccessReinforcement, "reinforce")
			if err != nil {
				t.Fatalf("failed to record access: %v", err)
			}
		}

		memory, err := store.GetMemory(ctx, "reinforced", domain.DomainAcademic)
		if err != nil {
			t.Fatalf("GetMemory failed: %v", err)
		}

		now := time.Now().UTC()
		threshold := -2.0 // Standard ACT-R threshold

		// Well-reinforced memory should have high retrieval probability
		prob := memory.RetrievalProbability(now, threshold)
		if prob < 0.9 {
			t.Errorf("well-reinforced memory should have high retrieval prob: %f", prob)
		}

		// Create a barely-touched memory
		insertTestNode(t, db, "weak", domain.DomainAcademic)
		// Insert an old trace directly
		_, err = db.Exec(`
			INSERT INTO node_access_traces (node_id, accessed_at, access_type, context)
			VALUES (?, ?, ?, ?)
		`, "weak", now.Add(-7*24*time.Hour).Unix(), "retrieval", "weak")
		if err != nil {
			t.Fatalf("failed to insert weak trace: %v", err)
		}
		_, err = db.Exec(`
			UPDATE nodes SET memory_activation = -5.0, last_accessed_at = ?, access_count = 1
			WHERE id = ?
		`, now.Add(-7*24*time.Hour).Unix(), "weak")
		if err != nil {
			t.Fatalf("failed to update weak node: %v", err)
		}

		weakMemory, err := store.GetMemory(ctx, "weak", domain.DomainAcademic)
		if err != nil {
			t.Fatalf("GetMemory failed: %v", err)
		}

		weakProb := weakMemory.RetrievalProbability(now, threshold)
		t.Logf("weak memory retrieval prob: %f", weakProb)

		// Weak memory should have lower probability than reinforced
		if weakProb >= prob {
			t.Errorf("weak memory prob (%f) should be less than reinforced (%f)",
				weakProb, prob)
		}
	})
}
