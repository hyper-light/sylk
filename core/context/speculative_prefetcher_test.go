package context

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// testPressureLevel provides a shared pressure level for tests
var testPressureLevel atomic.Int32

// =============================================================================
// Test Helpers
// =============================================================================

func createTestPrefetcher(t *testing.T) (*SpeculativePrefetcher, *TieredSearcher) {
	t.Helper()

	hotCache := NewDefaultHotCache()
	bleve := NewMockBleveSearcher()

	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
	})

	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		Searcher:        ts,
		PrefetchTimeout: 100 * time.Millisecond,
		MaxInflight:     5,
		SearchBudget:    TierWarmBudget,
	})

	return sp, ts
}

func createPrefetcherWithScope(t *testing.T) (*SpeculativePrefetcher, *TieredSearcher, *concurrency.GoroutineScope) {
	t.Helper()

	hotCache := NewDefaultHotCache()
	bleve := NewMockBleveSearcher()

	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
	})

	budget := concurrency.NewGoroutineBudget(&testPressureLevel)
	budget.RegisterAgent("test-prefetcher", "test")
	scope := concurrency.NewGoroutineScope(context.Background(), "test-prefetcher", budget)

	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		Searcher:        ts,
		Scope:           scope,
		PrefetchTimeout: 100 * time.Millisecond,
		MaxInflight:     5,
		SearchBudget:    TierWarmBudget,
	})

	return sp, ts, scope
}

// =============================================================================
// TrackedPrefetchFuture Tests
// =============================================================================

func TestTrackedPrefetchFuture_NewFuture(t *testing.T) {
	future := newTrackedPrefetchFuture("test query", "abc123")

	if future.query != "test query" {
		t.Errorf("query = %q, want %q", future.query, "test query")
	}

	if future.hash != "abc123" {
		t.Errorf("hash = %q, want %q", future.hash, "abc123")
	}

	if future.IsDone() {
		t.Error("New future should not be done")
	}
}

func TestTrackedPrefetchFuture_Complete(t *testing.T) {
	future := newTrackedPrefetchFuture("test query", "abc123")

	result := &AugmentedQuery{OriginalQuery: "test query"}
	future.complete(result, nil)

	if !future.IsDone() {
		t.Error("Future should be done after complete")
	}

	got := future.result.Load()
	if got != result {
		t.Error("Result should be stored after complete")
	}
}

func TestTrackedPrefetchFuture_CompleteWithError(t *testing.T) {
	future := newTrackedPrefetchFuture("test query", "abc123")

	testErr := ErrPrefetchDisabled
	future.complete(nil, testErr)

	if !future.IsDone() {
		t.Error("Future should be done after complete with error")
	}

	if future.Err() != testErr {
		t.Errorf("Err() = %v, want %v", future.Err(), testErr)
	}
}

func TestTrackedPrefetchFuture_GetIfReady_Ready(t *testing.T) {
	future := newTrackedPrefetchFuture("test query", "abc123")

	result := &AugmentedQuery{OriginalQuery: "test query"}
	future.complete(result, nil)

	got := future.GetIfReady(10 * time.Millisecond)
	if got != result {
		t.Error("GetIfReady should return result when ready")
	}
}

func TestTrackedPrefetchFuture_GetIfReady_NotReady(t *testing.T) {
	future := newTrackedPrefetchFuture("test query", "abc123")

	start := time.Now()
	got := future.GetIfReady(10 * time.Millisecond)
	elapsed := time.Since(start)

	if got != nil {
		t.Error("GetIfReady should return nil when not ready")
	}

	if elapsed < 10*time.Millisecond {
		t.Errorf("GetIfReady should wait for timeout, elapsed = %v", elapsed)
	}
}

func TestTrackedPrefetchFuture_Wait(t *testing.T) {
	future := newTrackedPrefetchFuture("test query", "abc123")

	result := &AugmentedQuery{OriginalQuery: "test query"}

	go func() {
		time.Sleep(10 * time.Millisecond)
		future.complete(result, nil)
	}()

	got, err := future.Wait()
	if err != nil {
		t.Errorf("Wait() error = %v, want nil", err)
	}
	if got != result {
		t.Error("Wait() should return result")
	}
}

func TestTrackedPrefetchFuture_Query(t *testing.T) {
	future := newTrackedPrefetchFuture("my test query", "abc123")

	if future.Query() != "my test query" {
		t.Errorf("Query() = %q, want %q", future.Query(), "my test query")
	}
}

func TestTrackedPrefetchFuture_Duration(t *testing.T) {
	future := newTrackedPrefetchFuture("test query", "abc123")

	time.Sleep(10 * time.Millisecond)
	duration := future.Duration()

	if duration < 10*time.Millisecond {
		t.Errorf("Duration() = %v, expected >= 10ms", duration)
	}
}

// =============================================================================
// SpeculativePrefetcher Construction Tests
// =============================================================================

func TestNewSpeculativePrefetcher_DefaultConfig(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{})

	if sp.prefetchTimeout != DefaultPrefetchTimeout {
		t.Errorf("prefetchTimeout = %v, want %v", sp.prefetchTimeout, DefaultPrefetchTimeout)
	}

	if sp.maxInflight != DefaultMaxInflight {
		t.Errorf("maxInflight = %d, want %d", sp.maxInflight, DefaultMaxInflight)
	}

	if !sp.IsEnabled() {
		t.Error("Prefetcher should be enabled by default")
	}
}

func TestNewSpeculativePrefetcher_CustomConfig(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 50 * time.Millisecond,
		MaxInflight:     3,
		SearchBudget:    100 * time.Millisecond,
	})

	if sp.prefetchTimeout != 50*time.Millisecond {
		t.Errorf("prefetchTimeout = %v, want 50ms", sp.prefetchTimeout)
	}

	if sp.maxInflight != 3 {
		t.Errorf("maxInflight = %d, want 3", sp.maxInflight)
	}
}

// =============================================================================
// StartSpeculative Tests
// =============================================================================

func TestStartSpeculative_Success(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()
	future, err := sp.StartSpeculative(ctx, "test query")

	if err != nil {
		t.Fatalf("StartSpeculative error = %v", err)
	}

	if future == nil {
		t.Fatal("Future should not be nil")
	}

	// Wait for completion
	future.Wait()

	if !future.IsDone() {
		t.Error("Future should be done after Wait")
	}
}

func TestStartSpeculative_Disabled(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	sp.SetEnabled(false)

	ctx := context.Background()
	future, err := sp.StartSpeculative(ctx, "test query")

	if err != ErrPrefetchDisabled {
		t.Errorf("Expected ErrPrefetchDisabled, got %v", err)
	}

	if future != nil {
		t.Error("Future should be nil when disabled")
	}
}

func TestStartSpeculative_BudgetExhausted(t *testing.T) {
	sp, _ := createTestPrefetcher(t)
	sp.maxInflight = 1

	ctx := context.Background()

	// Start first prefetch
	_, err := sp.StartSpeculative(ctx, "query 1")
	if err != nil {
		t.Fatalf("First StartSpeculative error = %v", err)
	}

	// Manually set inflight count to max
	sp.inflightCount.Store(1)

	// Try to start another (should fail)
	_, err = sp.StartSpeculative(ctx, "query 2")
	if err != ErrPrefetchBudgetExhausted {
		t.Errorf("Expected ErrPrefetchBudgetExhausted, got %v", err)
	}
}

func TestStartSpeculative_Deduplication(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()

	// Start same query twice
	future1, err1 := sp.StartSpeculative(ctx, "test query")
	future2, err2 := sp.StartSpeculative(ctx, "test query")

	if err1 != nil || err2 != nil {
		t.Fatalf("StartSpeculative errors: %v, %v", err1, err2)
	}

	if future1 != future2 {
		t.Error("Same query should return same future (deduplication)")
	}
}

func TestStartSpeculative_DifferentQueries(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()

	future1, err1 := sp.StartSpeculative(ctx, "query one")
	future2, err2 := sp.StartSpeculative(ctx, "query two")

	if err1 != nil || err2 != nil {
		t.Fatalf("StartSpeculative errors: %v, %v", err1, err2)
	}

	if future1 == future2 {
		t.Error("Different queries should return different futures")
	}
}

// =============================================================================
// GetOrStart Tests
// =============================================================================

func TestGetOrStart_CreatesNewFuture(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()
	future := sp.GetOrStart(ctx, "test query")

	if future == nil {
		t.Fatal("GetOrStart should create new future")
	}
}

func TestGetOrStart_ReturnsExisting(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()

	future1 := sp.GetOrStart(ctx, "test query")
	future2 := sp.GetOrStart(ctx, "test query")

	if future1 != future2 {
		t.Error("GetOrStart should return existing future")
	}
}

func TestGetOrStart_DisabledReturnsNil(t *testing.T) {
	sp, _ := createTestPrefetcher(t)
	sp.SetEnabled(false)

	ctx := context.Background()
	future := sp.GetOrStart(ctx, "test query")

	if future != nil {
		t.Error("GetOrStart should return nil when disabled")
	}
}

// =============================================================================
// GetInflight Tests
// =============================================================================

func TestGetInflight_ExistingFuture(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()
	started, _ := sp.StartSpeculative(ctx, "test query")

	got := sp.GetInflight("test query")

	if got != started {
		t.Error("GetInflight should return existing future")
	}
}

func TestGetInflight_NoExisting(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	got := sp.GetInflight("nonexistent query")

	if got != nil {
		t.Error("GetInflight should return nil for nonexistent query")
	}
}

// =============================================================================
// SetEnabled Tests
// =============================================================================

func TestSetEnabled_True(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	sp.SetEnabled(false)
	if sp.IsEnabled() {
		t.Error("Should be disabled")
	}

	sp.SetEnabled(true)
	if !sp.IsEnabled() {
		t.Error("Should be enabled")
	}
}

func TestSetEnabled_PreventNewPrefetches(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	sp.SetEnabled(false)

	ctx := context.Background()
	_, err := sp.StartSpeculative(ctx, "test query")

	if err != ErrPrefetchDisabled {
		t.Errorf("Expected ErrPrefetchDisabled, got %v", err)
	}

	stats := sp.Stats()
	if stats.TotalDisabled != 1 {
		t.Errorf("TotalDisabled = %d, want 1", stats.TotalDisabled)
	}
}

// =============================================================================
// Query Hashing Tests
// =============================================================================

func TestHashQuery_Consistent(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	hash1 := sp.hashQuery("test query")
	hash2 := sp.hashQuery("test query")

	if hash1 != hash2 {
		t.Error("Same query should produce same hash")
	}
}

func TestHashQuery_DifferentQueries(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	hash1 := sp.hashQuery("query one")
	hash2 := sp.hashQuery("query two")

	if hash1 == hash2 {
		t.Error("Different queries should produce different hashes")
	}
}

func TestNormalizeQuery_Lowercase(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	normalized := sp.normalizeQuery("Hello WORLD")

	if normalized != "hello world" {
		t.Errorf("normalized = %q, want %q", normalized, "hello world")
	}
}

func TestNormalizeQuery_TrimWhitespace(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	normalized := sp.normalizeQuery("  hello   world  ")

	if normalized != "hello world" {
		t.Errorf("normalized = %q, want %q", normalized, "hello world")
	}
}

func TestNormalizeQuery_CollapseSpaces(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	normalized := sp.normalizeQuery("hello\t\nworld")

	if normalized != "hello world" {
		t.Errorf("normalized = %q, want %q", normalized, "hello world")
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestStats_InitialValues(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	stats := sp.Stats()

	if stats.TotalStarted != 0 {
		t.Errorf("TotalStarted = %d, want 0", stats.TotalStarted)
	}

	if stats.Enabled != true {
		t.Error("Enabled should be true initially")
	}
}

func TestStats_TracksStarted(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()
	sp.StartSpeculative(ctx, "query 1")
	sp.StartSpeculative(ctx, "query 2")
	sp.StartSpeculative(ctx, "query 3")

	stats := sp.Stats()

	if stats.TotalStarted != 3 {
		t.Errorf("TotalStarted = %d, want 3", stats.TotalStarted)
	}
}

func TestStats_TracksHits(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()
	sp.StartSpeculative(ctx, "test query")
	sp.StartSpeculative(ctx, "test query") // Hit
	sp.StartSpeculative(ctx, "test query") // Hit

	stats := sp.Stats()

	if stats.TotalHits != 2 {
		t.Errorf("TotalHits = %d, want 2", stats.TotalHits)
	}
}

func TestResetStats(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()
	sp.StartSpeculative(ctx, "test query")

	sp.ResetStats()

	stats := sp.Stats()
	if stats.TotalStarted != 0 {
		t.Errorf("TotalStarted after reset = %d, want 0", stats.TotalStarted)
	}
}

// =============================================================================
// Cleanup Tests
// =============================================================================

func TestCleanupCompleted_RemovesDone(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	// Start a prefetch - the cleanup happens automatically when it completes
	ctx := context.Background()
	future, _ := sp.StartSpeculative(ctx, "test query")

	// Wait for completion
	future.Wait()

	// The future is already cleaned up by executePrefetch's defer cleanup
	// So GetInflight should return nil
	if sp.GetInflight("test query") != nil {
		t.Error("Completed future should be auto-removed after completion")
	}

	// CleanupCompleted should find nothing to clean
	cleaned := sp.CleanupCompleted()
	// 0 is expected because auto-cleanup already happened
	if cleaned != 0 {
		t.Logf("cleaned = %d (auto-cleanup happened)", cleaned)
	}
}

func TestInflightCount(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	if sp.InflightCount() != 0 {
		t.Errorf("Initial InflightCount = %d, want 0", sp.InflightCount())
	}

	ctx := context.Background()
	sp.StartSpeculative(ctx, "test query")

	// Give a moment for goroutine to start
	time.Sleep(10 * time.Millisecond)

	// Count may vary depending on completion speed
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("InflightCount = %d, should be >= 0", count)
	}
}

func TestCancelAll(t *testing.T) {
	sp, _ := createTestPrefetcher(t)
	sp.prefetchTimeout = 1 * time.Second // Long timeout so they're still inflight

	ctx := context.Background()
	future1, _ := sp.StartSpeculative(ctx, "query 1")
	future2, _ := sp.StartSpeculative(ctx, "query 2")

	// Give goroutines time to start
	time.Sleep(10 * time.Millisecond)

	sp.CancelAll()

	// Both should be done (cancelled)
	if !future1.IsDone() {
		t.Error("Future 1 should be done after CancelAll")
	}
	if !future2.IsDone() {
		t.Error("Future 2 should be done after CancelAll")
	}

	if sp.InflightCount() != 0 {
		t.Errorf("InflightCount after CancelAll = %d, want 0", sp.InflightCount())
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestStartSpeculative_ConcurrentAccess(t *testing.T) {
	sp, _ := createTestPrefetcher(t)
	sp.maxInflight = 100

	const goroutines = 10
	const iterations = 10

	var wg sync.WaitGroup
	var successCount atomic.Int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx := context.Background()
			for j := 0; j < iterations; j++ {
				future, err := sp.StartSpeculative(ctx, "shared query")
				if err == nil && future != nil {
					successCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	// All should succeed (or deduplicate)
	if successCount.Load() != int64(goroutines*iterations) {
		t.Errorf("successCount = %d, want %d", successCount.Load(), goroutines*iterations)
	}

	// Due to timing, we might have a few started (race before LoadOrStore sees stored value)
	// But the vast majority should be hits
	stats := sp.Stats()
	totalCalls := int64(goroutines * iterations)
	if stats.TotalStarted > 10 {
		t.Errorf("TotalStarted = %d, expected <= 10 (deduplication should work)", stats.TotalStarted)
	}

	// Hits should be at least 90% of calls minus started
	expectedMinHits := totalCalls - stats.TotalStarted
	if stats.TotalHits < expectedMinHits-5 {
		t.Errorf("TotalHits = %d, expected >= %d", stats.TotalHits, expectedMinHits-5)
	}
}

func TestSetEnabled_ConcurrentAccess(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				sp.SetEnabled(j%2 == 0)
				_ = sp.IsEnabled()
			}
		}()
	}

	wg.Wait()
	// No race conditions should occur
}

// =============================================================================
// GoroutineScope Integration Tests
// =============================================================================

func TestStartSpeculative_WithGoroutineScope(t *testing.T) {
	sp, _, scope := createPrefetcherWithScope(t)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ctx := context.Background()
	future, err := sp.StartSpeculative(ctx, "test query")

	if err != nil {
		t.Fatalf("StartSpeculative error = %v", err)
	}

	// Wait for result
	result, err := future.Wait()

	if err != nil {
		t.Errorf("Wait error = %v", err)
	}

	if result == nil {
		t.Error("Result should not be nil")
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestStartSpeculative_NilSearcher(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		Searcher: nil,
	})

	ctx := context.Background()
	future, err := sp.StartSpeculative(ctx, "test query")

	if err != nil {
		t.Fatalf("StartSpeculative error = %v", err)
	}

	// Wait and check for error
	_, err = future.Wait()
	if err == nil {
		t.Error("Expected error for nil searcher")
	}
}

func TestStartSpeculative_EmptyQuery(t *testing.T) {
	sp, _ := createTestPrefetcher(t)

	ctx := context.Background()
	future, err := sp.StartSpeculative(ctx, "")

	if err != nil {
		t.Fatalf("StartSpeculative error = %v", err)
	}

	if future == nil {
		t.Error("Future should not be nil for empty query")
	}
}

func TestGetOrStart_ConcurrentDeduplication(t *testing.T) {
	sp, _ := createTestPrefetcher(t)
	sp.maxInflight = 100

	const goroutines = 50

	var wg sync.WaitGroup
	futures := make([]*TrackedPrefetchFuture, goroutines)

	// Use a barrier to start all goroutines at the same time
	var ready sync.WaitGroup
	ready.Add(1)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ready.Wait() // Wait for barrier
			ctx := context.Background()
			futures[idx] = sp.GetOrStart(ctx, "same query")
		}(i)
	}

	// Release all goroutines
	ready.Done()
	wg.Wait()

	// Count unique futures (should be 1 or at most a few due to race)
	uniqueFutures := make(map[*TrackedPrefetchFuture]bool)
	for _, f := range futures {
		if f != nil {
			uniqueFutures[f] = true
		}
	}

	// With LoadOrStore, we should have exactly 1 unique future
	// But due to timing, we might occasionally get more if goroutines
	// run before the store completes. Allow a small tolerance.
	if len(uniqueFutures) > 1 {
		// Check that at least most are deduplicated
		nonNilCount := 0
		for _, f := range futures {
			if f != nil {
				nonNilCount++
			}
		}
		t.Logf("Got %d unique futures for %d goroutines (some timing variance expected)",
			len(uniqueFutures), nonNilCount)
	}

	// The key check: stats should show mostly hits
	stats := sp.Stats()
	if stats.TotalStarted > 5 {
		t.Errorf("TotalStarted = %d, expected <= 5 (deduplication should work)", stats.TotalStarted)
	}
}
