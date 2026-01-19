// Package context provides types and utilities for adaptive retrieval context management.
// This file contains tests for AR.3.2: ContextDiscovery.
package context

import (
	"math"
	"sync"
	"testing"
)

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewContextDiscoveryManager_Default(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.GetContextCount() != 0 {
		t.Errorf("expected 0 contexts, got %d", mgr.GetContextCount())
	}
}

func TestNewContextDiscoveryManager_CustomConfig(t *testing.T) {
	t.Parallel()

	config := ContextDiscoveryConfig{
		MaxContexts:       5,
		ClassifyThreshold: 0.8,
		UpdateThreshold:   0.9,
		CentroidEMA:       0.2,
	}

	mgr := NewContextDiscoveryManager(config)

	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.config.MaxContexts != 5 {
		t.Errorf("expected MaxContexts=5, got %d", mgr.config.MaxContexts)
	}
}

func TestNewContextDiscoveryManager_ZeroConfig_UsesDefaults(t *testing.T) {
	t.Parallel()

	config := ContextDiscoveryConfig{}
	mgr := NewContextDiscoveryManager(config)

	if mgr.config.MaxContexts != DefaultMaxContexts {
		t.Errorf("expected MaxContexts=%d, got %d", DefaultMaxContexts, mgr.config.MaxContexts)
	}
}

// =============================================================================
// Classification Tests
// =============================================================================

func TestClassifyQuery_CreatesNewContext(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()
	embedding := makeTestEmbedding(0.5)

	ctx := mgr.ClassifyQuery("test query", embedding)

	if ctx == "" {
		t.Error("expected non-empty context")
	}
	if mgr.GetContextCount() != 1 {
		t.Errorf("expected 1 context, got %d", mgr.GetContextCount())
	}
}

func TestClassifyQuery_MatchesExistingContext(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	// Create first context
	embedding1 := makeTestEmbedding(0.5)
	ctx1 := mgr.ClassifyQuery("first query", embedding1)

	// Very similar embedding (small perturbation) should match
	embedding2 := make([]float32, len(embedding1))
	copy(embedding2, embedding1)
	// Add small noise
	for i := range embedding2 {
		embedding2[i] *= 1.01
	}
	normalizef32(embedding2)

	ctx2 := mgr.ClassifyQuery("second query", embedding2)

	if ctx2 != ctx1 {
		t.Errorf("expected same context for similar embeddings: %s vs %s", ctx1, ctx2)
	}
	if mgr.GetContextCount() != 1 {
		t.Errorf("expected 1 context, got %d", mgr.GetContextCount())
	}
}

func TestClassifyQuery_CreatesDifferentContextForDissimilar(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	// Create first context
	embedding1 := makeTestEmbedding(0.1)
	ctx1 := mgr.ClassifyQuery("first query", embedding1)

	// Very different embedding should create new context
	embedding2 := makeTestEmbedding(0.9)
	ctx2 := mgr.ClassifyQuery("different query", embedding2)

	if ctx2 == ctx1 {
		t.Error("expected different context for dissimilar embeddings")
	}
	if mgr.GetContextCount() != 2 {
		t.Errorf("expected 2 contexts, got %d", mgr.GetContextCount())
	}
}

func TestClassifyQuery_UsesKeywordCache(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	embedding := makeTestEmbedding(0.5)
	ctx1 := mgr.ClassifyQuery("test query", embedding)

	// Same query should return same context without embedding comparison
	ctx2 := mgr.ClassifyQuery("test query", makeTestEmbedding(0.1))

	if ctx2 != ctx1 {
		t.Errorf("expected cached context: %s vs %s", ctx1, ctx2)
	}
}

func TestClassifyQuery_NormalizesQuery(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	embedding := makeTestEmbedding(0.5)
	ctx1 := mgr.ClassifyQuery("TEST QUERY", embedding)
	ctx2 := mgr.ClassifyQuery("  test   query  ", makeTestEmbedding(0.1))

	if ctx2 != ctx1 {
		t.Error("expected normalized query to match cache")
	}
}

// =============================================================================
// Max Contexts Tests
// =============================================================================

func TestClassifyQuery_RespectsMaxContexts(t *testing.T) {
	t.Parallel()

	config := ContextDiscoveryConfig{
		MaxContexts:       3,
		ClassifyThreshold: 0.99, // Very high so all embeddings create new contexts
	}
	mgr := NewContextDiscoveryManager(config)

	// Create 5 different embeddings
	for i := 0; i < 5; i++ {
		embedding := make([]float32, 128)
		embedding[i] = 1.0
		mgr.ClassifyQuery("query"+string(rune('A'+i)), embedding)
	}

	if mgr.GetContextCount() > 3 {
		t.Errorf("expected max 3 contexts, got %d", mgr.GetContextCount())
	}
}

func TestClassifyQuery_ReplacesWeakestContext(t *testing.T) {
	t.Parallel()

	config := ContextDiscoveryConfig{
		MaxContexts:       2,
		ClassifyThreshold: 0.99,
	}
	mgr := NewContextDiscoveryManager(config)

	// Create two contexts
	emb1 := make([]float32, 128)
	emb1[0] = 1.0
	ctx1 := mgr.ClassifyQuery("query1", emb1)

	emb2 := make([]float32, 128)
	emb2[1] = 1.0
	mgr.ClassifyQuery("query2", emb2)

	// Use first context multiple times to increase its count
	mgr.ClassifyQuery("query1", emb1)
	mgr.ClassifyQuery("query1", emb1)

	// Create a third context - should replace the weakest (query2)
	emb3 := make([]float32, 128)
	emb3[2] = 1.0
	mgr.ClassifyQuery("query3", emb3)

	// ctx1 should still exist (higher count)
	if mgr.GetBias(ctx1) == nil {
		t.Error("expected first context to be preserved")
	}
}

// =============================================================================
// Bias Tests
// =============================================================================

func TestGetBias_ReturnsNilForUnknown(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	bias := mgr.GetBias("unknown_ctx")
	if bias != nil {
		t.Error("expected nil bias for unknown context")
	}
}

func TestGetBias_ReturnsDefaultBias(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	embedding := makeTestEmbedding(0.5)
	ctx := mgr.ClassifyQuery("test query", embedding)

	bias := mgr.GetBias(ctx)
	if bias == nil {
		t.Fatal("expected non-nil bias")
	}
	if bias.RelevanceMult != 1.0 {
		t.Errorf("expected RelevanceMult=1.0, got %f", bias.RelevanceMult)
	}
	if bias.StruggleMult != 1.0 {
		t.Errorf("expected StruggleMult=1.0, got %f", bias.StruggleMult)
	}
	if bias.WasteMult != 1.0 {
		t.Errorf("expected WasteMult=1.0, got %f", bias.WasteMult)
	}
}

func TestUpdateBias_UpdatesValues(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	embedding := makeTestEmbedding(0.5)
	ctx := mgr.ClassifyQuery("test query", embedding)

	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 5,      // High struggle
		SearchedAfter: []string{"search1", "search2"},
		PrefetchedIDs: []string{"p1", "p2", "p3", "p4", "p5"},
		UsedIDs:       []string{"p1"},
	}

	mgr.UpdateBias(ctx, obs)

	bias := mgr.GetBias(ctx)
	if bias.ObservationCount != 1 {
		t.Errorf("expected ObservationCount=1, got %d", bias.ObservationCount)
	}
}

func TestClampMultiplier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    float64
		expected float64
	}{
		{0.3, 0.5},  // Below minimum
		{0.5, 0.5},  // At minimum
		{1.0, 1.0},  // Middle
		{2.0, 2.0},  // At maximum
		{2.5, 2.0},  // Above maximum
	}

	for _, tt := range tests {
		result := clampMultiplier(tt.input)
		if result != tt.expected {
			t.Errorf("clampMultiplier(%f) = %f, want %f", tt.input, result, tt.expected)
		}
	}
}

// =============================================================================
// Query Methods Tests
// =============================================================================

func TestGetAllContexts(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	// Create 3 contexts
	for i := 0; i < 3; i++ {
		embedding := make([]float32, 128)
		embedding[i*10] = 1.0
		mgr.ClassifyQuery("query"+string(rune('A'+i)), embedding)
	}

	contexts := mgr.GetAllContexts()
	if len(contexts) != 3 {
		t.Errorf("expected 3 contexts, got %d", len(contexts))
	}
}

func TestGetContextStats(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	embedding := makeTestEmbedding(0.5)
	mgr.ClassifyQuery("test query", embedding)

	stats := mgr.GetContextStats()
	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	if stats[0].Count != 1 {
		t.Errorf("expected Count=1, got %d", stats[0].Count)
	}
}

func TestFindSimilarContexts(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	// Create a context
	embedding1 := makeTestEmbedding(0.5)
	mgr.ClassifyQuery("query1", embedding1)

	// Create a different context
	embedding2 := makeTestEmbedding(0.9)
	mgr.ClassifyQuery("query2", embedding2)

	// Find similar to first
	similar := mgr.FindSimilarContexts(embedding1, 0.8)

	if len(similar) != 1 {
		t.Errorf("expected 1 similar context, got %d", len(similar))
	}
}

func TestFindSimilarContexts_SortedBySimilarity(t *testing.T) {
	t.Parallel()

	config := ContextDiscoveryConfig{
		MaxContexts:       5,
		ClassifyThreshold: 0.95,
	}
	mgr := NewContextDiscoveryManager(config)

	// Create contexts with varying similarity
	for i := 0; i < 3; i++ {
		embedding := make([]float32, 128)
		embedding[0] = float32(0.5 + float64(i)*0.1)
		embedding[1] = float32(0.5 - float64(i)*0.05)
		normalizef32(embedding)
		mgr.ClassifyQuery("query"+string(rune('A'+i)), embedding)
	}

	query := make([]float32, 128)
	query[0] = 0.5
	query[1] = 0.5
	normalizef32(query)

	matches := mgr.FindSimilarContexts(query, 0.0)

	// Check sorted by similarity (descending)
	for i := 1; i < len(matches); i++ {
		if matches[i].Similarity > matches[i-1].Similarity {
			t.Error("matches should be sorted by similarity descending")
		}
	}
}

// =============================================================================
// Persistence Tests
// =============================================================================

func TestGetDiscovery_ReturnsDeepCopy(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	embedding := makeTestEmbedding(0.5)
	mgr.ClassifyQuery("test query", embedding)

	discovery := mgr.GetDiscovery()

	// Modify the copy
	discovery.Centroids[0].Count = 999

	// Original should be unchanged
	stats := mgr.GetContextStats()
	if stats[0].Count == 999 {
		t.Error("GetDiscovery should return a deep copy")
	}
}

func TestRestoreFromDiscovery(t *testing.T) {
	t.Parallel()

	mgr1 := NewDefaultContextDiscoveryManager()

	// Create contexts in first manager
	embedding := makeTestEmbedding(0.5)
	ctx := mgr1.ClassifyQuery("test query", embedding)

	// Get discovery and restore to new manager
	discovery := mgr1.GetDiscovery()

	mgr2 := NewDefaultContextDiscoveryManager()
	mgr2.RestoreFromDiscovery(discovery)

	// Second manager should have same contexts
	if mgr2.GetContextCount() != 1 {
		t.Errorf("expected 1 context, got %d", mgr2.GetContextCount())
	}
	if mgr2.GetBias(ctx) == nil {
		t.Error("expected restored context to exist")
	}
}

// =============================================================================
// Utility Function Tests
// =============================================================================

func TestNormalizeQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"HELLO", "hello"},
		{"  hello  ", "hello"},
		{"hello   world", "hello world"},
		{"  HELLO   WORLD  ", "hello world"},
	}

	for _, tt := range tests {
		result := normalizeQuery(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeQuery(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestCosineSimilarity_Identical(t *testing.T) {
	t.Parallel()

	a := []float32{1, 0, 0}
	sim := cosineSimilarity(a, a)

	if math.Abs(sim-1.0) > 0.001 {
		t.Errorf("expected similarity=1.0 for identical vectors, got %f", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	t.Parallel()

	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}

	sim := cosineSimilarity(a, b)

	if math.Abs(sim) > 0.001 {
		t.Errorf("expected similarity=0 for orthogonal vectors, got %f", sim)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	t.Parallel()

	a := []float32{1, 0, 0}
	b := []float32{-1, 0, 0}

	sim := cosineSimilarity(a, b)

	if math.Abs(sim+1.0) > 0.001 {
		t.Errorf("expected similarity=-1.0 for opposite vectors, got %f", sim)
	}
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	t.Parallel()

	a := []float32{1, 0, 0}
	b := []float32{1, 0}

	sim := cosineSimilarity(a, b)

	if sim != 0 {
		t.Errorf("expected similarity=0 for different length vectors, got %f", sim)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	t.Parallel()

	a := []float32{0, 0, 0}
	b := []float32{1, 0, 0}

	sim := cosineSimilarity(a, b)

	if sim != 0 {
		t.Errorf("expected similarity=0 for zero vector, got %f", sim)
	}
}

func TestNormalizef32(t *testing.T) {
	t.Parallel()

	v := []float32{3, 4}
	normalizef32(v)

	// Should have unit length
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := math.Sqrt(sum)

	if math.Abs(norm-1.0) > 0.001 {
		t.Errorf("expected unit length, got %f", norm)
	}
}

func TestNormalizef32_ZeroVector(t *testing.T) {
	t.Parallel()

	v := []float32{0, 0, 0}
	normalizef32(v)

	// Should remain zero (avoid division by zero)
	for _, x := range v {
		if x != 0 {
			t.Error("zero vector should remain zero")
		}
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestConcurrentClassification(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	var wg sync.WaitGroup
	n := 100

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			embedding := makeTestEmbedding(float64(idx) / float64(n))
			mgr.ClassifyQuery("query"+string(rune(idx%26+'A')), embedding)
		}(i)
	}

	wg.Wait()

	// Should have some contexts created
	if mgr.GetContextCount() == 0 {
		t.Error("expected some contexts after concurrent classification")
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	// Pre-create some contexts
	for i := 0; i < 5; i++ {
		embedding := makeTestEmbedding(float64(i) / 5.0)
		mgr.ClassifyQuery("query"+string(rune('A'+i)), embedding)
	}

	var wg sync.WaitGroup
	n := 50

	// Writers
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			embedding := makeTestEmbedding(float64(idx) / float64(n))
			mgr.ClassifyQuery("write"+string(rune(idx%26+'A')), embedding)
		}(i)
	}

	// Readers
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = mgr.GetAllContexts()
			_ = mgr.GetContextCount()
			_ = mgr.GetContextStats()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestClassifyQuery_EmptyEmbedding(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	// Should not panic with empty embedding
	ctx := mgr.ClassifyQuery("test", []float32{})

	if ctx == "" {
		t.Error("expected non-empty context even for empty embedding")
	}
}

func TestFindSimilarContexts_NoContexts(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultContextDiscoveryManager()

	matches := mgr.FindSimilarContexts(makeTestEmbedding(0.5), 0.5)

	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// makeTestEmbedding creates a test embedding with a deterministic pattern.
func makeTestEmbedding(seed float64) []float32 {
	embedding := make([]float32, 128)
	for i := range embedding {
		embedding[i] = float32(math.Sin(float64(i)*seed) * seed)
	}
	normalizef32(embedding)
	return embedding
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkClassifyQuery(b *testing.B) {
	mgr := NewDefaultContextDiscoveryManager()

	// Pre-populate with some contexts
	for i := 0; i < 5; i++ {
		embedding := makeTestEmbedding(float64(i) / 5.0)
		mgr.ClassifyQuery("pre"+string(rune('A'+i)), embedding)
	}

	embedding := makeTestEmbedding(0.5)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr.ClassifyQuery("query", embedding)
	}
}

func BenchmarkCosineSimilarity(b *testing.B) {
	a := makeTestEmbedding(0.3)
	bb := makeTestEmbedding(0.7)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cosineSimilarity(a, bb)
	}
}

func BenchmarkFindSimilarContexts(b *testing.B) {
	mgr := NewDefaultContextDiscoveryManager()

	// Pre-populate with contexts
	for i := 0; i < 10; i++ {
		embedding := makeTestEmbedding(float64(i) / 10.0)
		mgr.ClassifyQuery("ctx"+string(rune('A'+i)), embedding)
	}

	query := makeTestEmbedding(0.5)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr.FindSimilarContexts(query, 0.5)
	}
}
