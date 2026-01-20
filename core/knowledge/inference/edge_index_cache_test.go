package inference

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// =============================================================================
// W4P.13: Edge Index Cache Tests
// =============================================================================

func TestEdgeIndexCache_NewEdgeIndexCache(t *testing.T) {
	cache := NewEdgeIndexCache()
	if cache == nil {
		t.Fatal("NewEdgeIndexCache should return non-nil")
	}
	if cache.Version() != 0 {
		t.Errorf("Initial version should be 0, got %d", cache.Version())
	}
}

// =============================================================================
// Happy Path: Cached index used correctly
// =============================================================================

func TestEdgeIndexCache_GetOrBuild_BuildsOnFirstCall(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
	}

	index := cache.GetOrBuild(edges)

	if index == nil {
		t.Fatal("GetOrBuild should return non-nil index")
	}
	if cache.Version() != 1 {
		t.Errorf("Version should be 1 after first build, got %d", cache.Version())
	}
	if len(index.allEdges) != 2 {
		t.Errorf("Index should contain 2 edges, got %d", len(index.allEdges))
	}
}

func TestEdgeIndexCache_GetOrBuild_ReturnsCache(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
	}

	// First call builds
	index1 := cache.GetOrBuild(edges)
	version1 := cache.Version()

	// Second call returns cached
	index2 := cache.GetOrBuild(edges)
	version2 := cache.Version()

	if index1 != index2 {
		t.Error("Second call should return the same index instance")
	}
	if version1 != version2 {
		t.Errorf("Version should not change on cache hit: %d != %d", version1, version2)
	}
}

func TestEdgeIndexCache_GetOrBuild_IndexContentsCorrect(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "A", Predicate: "calls", Target: "C"},
		{Source: "B", Predicate: "imports", Target: "D"},
	}

	index := cache.GetOrBuild(edges)

	// Verify bySource
	if len(index.bySource["A"]) != 2 {
		t.Errorf("Expected 2 edges from A, got %d", len(index.bySource["A"]))
	}
	if len(index.bySource["B"]) != 1 {
		t.Errorf("Expected 1 edge from B, got %d", len(index.bySource["B"]))
	}

	// Verify byPredicate
	if len(index.byPredicate["calls"]) != 2 {
		t.Errorf("Expected 2 calls edges, got %d", len(index.byPredicate["calls"]))
	}
	if len(index.byPredicate["imports"]) != 1 {
		t.Errorf("Expected 1 imports edge, got %d", len(index.byPredicate["imports"]))
	}

	// Verify bySourcePred
	if len(index.bySourcePred["A:calls"]) != 2 {
		t.Errorf("Expected 2 edges for A:calls, got %d", len(index.bySourcePred["A:calls"]))
	}
}

// =============================================================================
// Invalidation: Cache rebuilds after edge changes
// =============================================================================

func TestEdgeIndexCache_Invalidate(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges1 := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
	}

	// Build cache
	index1 := cache.GetOrBuild(edges1)
	version1 := cache.Version()

	// Invalidate
	cache.Invalidate()

	// Rebuild with same edges
	index2 := cache.GetOrBuild(edges1)
	version2 := cache.Version()

	if index1 == index2 {
		t.Error("After invalidation, should build new index")
	}
	if version2 != version1+1 {
		t.Errorf("Version should increment after rebuild: expected %d, got %d", version1+1, version2)
	}
}

func TestEdgeIndexCache_InvalidateIfChanged_NoChange(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
	}

	cache.GetOrBuild(edges)
	version1 := cache.Version()

	// Same count, should not invalidate
	invalidated := cache.InvalidateIfChanged(2)

	if invalidated {
		t.Error("Should not invalidate when edge count unchanged")
	}

	// Verify cache still valid
	index := cache.GetOrBuild(edges)
	if cache.Version() != version1 {
		t.Error("Version should not change when not invalidated")
	}
	if index == nil {
		t.Error("Cache should still return valid index")
	}
}

func TestEdgeIndexCache_InvalidateIfChanged_WithChange(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges1 := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
	}

	cache.GetOrBuild(edges1)
	version1 := cache.Version()

	// Different count, should invalidate
	invalidated := cache.InvalidateIfChanged(2)

	if !invalidated {
		t.Error("Should invalidate when edge count changes")
	}

	// Build with new edges
	edges2 := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
	}

	cache.GetOrBuild(edges2)

	if cache.Version() != version1+1 {
		t.Errorf("Version should increment after rebuild: expected %d, got %d", version1+1, cache.Version())
	}
}

func TestEdgeIndexCache_RebuildWithDifferentEdges(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges1 := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
	}

	index1 := cache.GetOrBuild(edges1)
	if len(index1.allEdges) != 1 {
		t.Errorf("First index should have 1 edge")
	}

	cache.Invalidate()

	edges2 := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
		{Source: "C", Predicate: "calls", Target: "D"},
	}

	index2 := cache.GetOrBuild(edges2)
	if len(index2.allEdges) != 3 {
		t.Errorf("Second index should have 3 edges, got %d", len(index2.allEdges))
	}
}

// =============================================================================
// Concurrent: Multiple evaluators sharing cache
// =============================================================================

func TestEdgeIndexCache_ConcurrentGetOrBuild(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
	}

	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	indices := make([]*EdgeIndex, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			indices[idx] = cache.GetOrBuild(edges)
		}(i)
	}

	wg.Wait()

	// All should get the same index
	for i := 1; i < numGoroutines; i++ {
		if indices[i] != indices[0] {
			t.Errorf("Concurrent access returned different indices")
			break
		}
	}

	// Version should be 1 (only one build)
	if cache.Version() != 1 {
		t.Errorf("Expected version 1, got %d (multiple builds occurred)", cache.Version())
	}
}

func TestEdgeIndexCache_ConcurrentInvalidateAndBuild(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
	}

	const numIterations = 50
	var wg sync.WaitGroup

	for i := 0; i < numIterations; i++ {
		wg.Add(2)

		// Goroutine 1: Build
		go func() {
			defer wg.Done()
			index := cache.GetOrBuild(edges)
			if index == nil {
				t.Error("GetOrBuild returned nil")
			}
		}()

		// Goroutine 2: Invalidate and rebuild
		go func() {
			defer wg.Done()
			cache.Invalidate()
			index := cache.GetOrBuild(edges)
			if index == nil {
				t.Error("GetOrBuild after invalidate returned nil")
			}
		}()
	}

	wg.Wait()

	// Final state should be valid
	finalIndex := cache.GetOrBuild(edges)
	if finalIndex == nil {
		t.Error("Final GetOrBuild returned nil")
	}
}

func TestEdgeIndexCache_ConcurrentReadVersion(t *testing.T) {
	cache := NewEdgeIndexCache()

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
	}

	const numIterations = 100
	var wg sync.WaitGroup

	for i := 0; i < numIterations; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			cache.GetOrBuild(edges)
		}()

		go func() {
			defer wg.Done()
			_ = cache.Version()
		}()

		go func() {
			defer wg.Done()
			cache.Invalidate()
		}()
	}

	wg.Wait()
	// No panics = success
}

// =============================================================================
// Integration: ForwardChainer uses cached index
// =============================================================================

func TestForwardChainer_UsesCachedIndex(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Multiple rules - all should share the same index per iteration
	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Rule 1",
			Head: RuleCondition{Subject: "?x", Predicate: "derived1", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "original", Object: "?y"},
			},
			Enabled: true,
		},
		{
			ID:   "r2",
			Name: "Rule 2",
			Head: RuleCondition{Subject: "?x", Predicate: "derived2", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "original", Object: "?y"},
			},
			Enabled: true,
		},
		{
			ID:   "r3",
			Name: "Rule 3",
			Head: RuleCondition{Subject: "?x", Predicate: "derived3", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "original", Object: "?y"},
			},
			Enabled: true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "original", Target: "B"},
		{Source: "C", Predicate: "original", Target: "D"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have 6 results (3 rules x 2 edges)
	if len(results) != 6 {
		t.Errorf("Expected 6 results, got %d", len(results))
	}

	// Verify cache was used:
	// - Iteration 1: Build index (v1), evaluate 3 rules (sharing index), derive 6 edges, invalidate
	// - Iteration 2: Build index (v2), evaluate rules, no new results (fixpoint)
	// So version should be 2 (one build per iteration)
	// Without caching, we would have 3 builds per iteration = 6 total
	if fc.indexCache.Version() != 2 {
		t.Errorf("Cache should have version 2 (one per iteration), got %d", fc.indexCache.Version())
	}
}

func TestForwardChainer_InvalidatesCacheOnNewEdges(t *testing.T) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Transitive rule creates new edges, requiring cache invalidation
	rules := []InferenceRule{
		{
			ID:   "trans",
			Name: "Transitive",
			Head: RuleCondition{Subject: "?x", Predicate: "reaches", Object: "?z"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "reaches", Object: "?y"},
				{Subject: "?y", Predicate: "reaches", Object: "?z"},
			},
			Enabled: true,
		},
	}

	// Linear chain: A -> B -> C -> D
	edges := []Edge{
		{Source: "A", Predicate: "reaches", Target: "B"},
		{Source: "B", Predicate: "reaches", Target: "C"},
		{Source: "C", Predicate: "reaches", Target: "D"},
	}

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should derive: A->C, B->D (iter 1), A->D (iter 2)
	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Cache should have been rebuilt multiple times (once per iteration with new edges)
	// Iteration 1: build (v1), derive A->C, B->D, invalidate
	// Iteration 2: build (v2), derive A->D, invalidate
	// Iteration 3: build (v3), no new edges (fixpoint)
	if fc.indexCache.Version() < 2 {
		t.Errorf("Cache should have been rebuilt at least twice, version=%d", fc.indexCache.Version())
	}
}

func TestForwardChainer_ConcurrentEvaluations(t *testing.T) {
	// Note: Each goroutine creates its own ForwardChainer since the RuleEvaluator
	// has internal memoization state that is not thread-safe. This test verifies
	// that the EdgeIndexCache is thread-safe when shared across evaluators.
	cache := NewEdgeIndexCache()

	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Simple",
			Head: RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "original", Object: "?y"},
			},
			Enabled: true,
		},
	}

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ctx := context.Background()

			// Each goroutine creates its own evaluator and chainer
			// but they share the same edge index cache
			evaluator := NewRuleEvaluator()
			fc := NewForwardChainer(evaluator, 100)
			fc.indexCache = cache // Share the cache

			edges := []Edge{
				{Source: "A", Predicate: "original", Target: "B"},
			}

			results, err := fc.Evaluate(ctx, rules, edges)
			if err != nil {
				errors <- err
				return
			}
			if len(results) != 1 {
				errors <- &testError{msg: "expected 1 result"}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent evaluation error: %v", err)
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// =============================================================================
// RuleEvaluator with shared index
// =============================================================================

func TestRuleEvaluator_EvaluateRuleWithIndex(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "r1",
		Name: "Test",
		Head: RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "original", Object: "?y"},
		},
		Enabled: true,
	}

	edges := []Edge{
		{Source: "A", Predicate: "original", Target: "B"},
		{Source: "C", Predicate: "original", Target: "D"},
	}

	// Build index externally
	index := buildEdgeIndex(edges)

	// Evaluate with pre-built index
	results := evaluator.EvaluateRuleWithIndex(ctx, rule, edges, index)

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
}

func TestRuleEvaluator_EvaluateRuleWithIndex_MultipleRulesShareIndex(t *testing.T) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rules := []InferenceRule{
		{
			ID:   "r1",
			Name: "Rule 1",
			Head: RuleCondition{Subject: "?x", Predicate: "derived1", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "calls", Object: "?y"},
			},
			Enabled: true,
		},
		{
			ID:   "r2",
			Name: "Rule 2",
			Head: RuleCondition{Subject: "?x", Predicate: "derived2", Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "calls", Object: "?y"},
			},
			Enabled: true,
		},
	}

	edges := []Edge{
		{Source: "A", Predicate: "calls", Target: "B"},
		{Source: "B", Predicate: "calls", Target: "C"},
	}

	// Build index once
	index := buildEdgeIndex(edges)

	// Evaluate multiple rules with same index
	var allResults []InferenceResult
	for _, rule := range rules {
		results := evaluator.EvaluateRuleWithIndex(ctx, rule, edges, index)
		allResults = append(allResults, results...)
	}

	if len(allResults) != 4 {
		t.Errorf("Expected 4 total results (2 rules x 2 edges), got %d", len(allResults))
	}
}

// =============================================================================
// Benchmark: Verify reduced index builds
// =============================================================================

func BenchmarkRuleEvaluator_WithoutCache(b *testing.B) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "r1",
		Name: "Test",
		Head: RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
		},
		Enabled: true,
	}

	// Create a larger edge set
	edges := make([]Edge, 1000)
	for i := 0; i < 1000; i++ {
		edges[i] = Edge{
			Source:    "node" + string(rune('A'+i%26)),
			Predicate: "calls",
			Target:    "node" + string(rune('A'+(i+1)%26)),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.EvaluateRule(ctx, rule, edges)
	}
}

func BenchmarkRuleEvaluator_WithCache(b *testing.B) {
	evaluator := NewRuleEvaluator()
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "r1",
		Name: "Test",
		Head: RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
		},
		Enabled: true,
	}

	// Create a larger edge set
	edges := make([]Edge, 1000)
	for i := 0; i < 1000; i++ {
		edges[i] = Edge{
			Source:    "node" + string(rune('A'+i%26)),
			Predicate: "calls",
			Target:    "node" + string(rune('A'+(i+1)%26)),
		}
	}

	// Build index once
	index := buildEdgeIndex(edges)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.EvaluateRuleWithIndex(ctx, rule, edges, index)
	}
}

func BenchmarkForwardChainer_MultipleRules_WithCache(b *testing.B) {
	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// Create multiple rules
	rules := make([]InferenceRule, 10)
	for i := 0; i < 10; i++ {
		rules[i] = InferenceRule{
			ID:   "r" + string(rune('0'+i)),
			Name: "Rule " + string(rune('0'+i)),
			Head: RuleCondition{Subject: "?x", Predicate: "derived" + string(rune('0'+i)), Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "original", Object: "?y"},
			},
			Enabled: true,
		}
	}

	// Create edges
	edges := make([]Edge, 100)
	for i := 0; i < 100; i++ {
		edges[i] = Edge{
			Source:    "node" + string(rune('A'+i%26)),
			Predicate: "original",
			Target:    "node" + string(rune('A'+(i+1)%26)),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fc.Evaluate(ctx, rules, edges)
		// Reset cache for next iteration to simulate fresh evaluation
		fc.indexCache.Invalidate()
	}
}

// CountingEdgeIndexCache wraps EdgeIndexCache to count builds for testing.
type CountingEdgeIndexCache struct {
	*EdgeIndexCache
	buildCount int64
}

func NewCountingEdgeIndexCache() *CountingEdgeIndexCache {
	return &CountingEdgeIndexCache{
		EdgeIndexCache: NewEdgeIndexCache(),
	}
}

func (c *CountingEdgeIndexCache) GetOrBuild(edges []Edge) *EdgeIndex {
	// Check if we'll need to build
	c.mu.RLock()
	needsBuild := !c.isValid(edges)
	c.mu.RUnlock()

	if needsBuild {
		atomic.AddInt64(&c.buildCount, 1)
	}

	return c.EdgeIndexCache.GetOrBuild(edges)
}

func (c *CountingEdgeIndexCache) BuildCount() int64 {
	return atomic.LoadInt64(&c.buildCount)
}

func TestForwardChainer_ReducedIndexBuilds(t *testing.T) {
	// This test verifies that with caching, we build the index far fewer times
	// than without caching (once per iteration vs once per rule evaluation)

	evaluator := NewRuleEvaluator()
	fc := NewForwardChainer(evaluator, 100)
	ctx := context.Background()

	// 5 rules
	rules := make([]InferenceRule, 5)
	for i := 0; i < 5; i++ {
		rules[i] = InferenceRule{
			ID:   "r" + string(rune('0'+i)),
			Name: "Rule " + string(rune('0'+i)),
			Head: RuleCondition{Subject: "?x", Predicate: "derived" + string(rune('0'+i)), Object: "?y"},
			Body: []RuleCondition{
				{Subject: "?x", Predicate: "original", Object: "?y"},
			},
			Enabled: true,
		}
	}

	edges := []Edge{
		{Source: "A", Predicate: "original", Target: "B"},
		{Source: "C", Predicate: "original", Target: "D"},
	}

	// Track initial cache version
	initialVersion := fc.indexCache.Version()

	results, err := fc.Evaluate(ctx, rules, edges)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify results
	if len(results) != 10 { // 5 rules x 2 edges
		t.Errorf("Expected 10 results, got %d", len(results))
	}

	// With caching: should build once per iteration (2 iterations total)
	// - Iteration 1: Build (v1), evaluate 5 rules sharing index, derive 10 edges, invalidate
	// - Iteration 2: Build (v2), evaluate rules, no new results (fixpoint)
	// Without caching: would build 5 times per iteration = 10 total
	finalVersion := fc.indexCache.Version()
	buildCount := finalVersion - initialVersion

	// Should have exactly 2 builds (one per iteration), not 10 (one per rule)
	if buildCount != 2 {
		t.Errorf("With caching, should build once per iteration (expected 2); built %d times", buildCount)
	}
}
