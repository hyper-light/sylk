// Package context provides tests for AR.5.3: SpeculativePrefetcher CancelAll race condition fix.
// W4N.10: Tests for CancelAll race condition elimination.
package context

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestW4N10_CancelAll_HappyPath tests that CancelAll cancels all prefetches.
func TestW4N10_CancelAll_HappyPath(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     10,
	})

	ctx := context.Background()

	// Start several prefetches
	var futures []*TrackedPrefetchFuture
	for i := 0; i < 5; i++ {
		future, err := sp.StartSpeculative(ctx, "query"+string(rune('A'+i)))
		if err != nil {
			t.Fatalf("failed to start prefetch %d: %v", i, err)
		}
		futures = append(futures, future)
	}

	// Cancel all
	sp.CancelAll()

	// Verify all futures are marked as done
	// Note: futures may complete with either context.Canceled (if CancelAll runs first)
	// or with "no searcher configured" error (if they complete before CancelAll)
	for i, future := range futures {
		if !future.IsDone() {
			t.Errorf("future %d not done after CancelAll", i)
		}
		// Future must have some error (either canceled or no searcher)
		if future.Err() == nil {
			t.Errorf("future %d has no error, expected either Canceled or searcher error", i)
		}
	}

	// The key invariant: inflightCount should never go negative
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("inflightCount went negative: %d", count)
	}
}

// TestW4N10_CancelAll_DuringActiveCleanup tests CancelAll during active cleanup.
func TestW4N10_CancelAll_DuringActiveCleanup(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     20,
	})

	ctx := context.Background()

	// Start prefetches
	var futures []*TrackedPrefetchFuture
	queries := make([]string, 10)
	for i := 0; i < 10; i++ {
		queries[i] = "cleanupQuery" + string(rune('A'+i))
		future, err := sp.StartSpeculative(ctx, queries[i])
		if err != nil {
			t.Fatalf("failed to start prefetch %d: %v", i, err)
		}
		futures = append(futures, future)
	}

	// Simulate concurrent cleanup by completing some futures
	// and calling cleanup in parallel with CancelAll
	var wg sync.WaitGroup

	// Goroutine 1: Complete futures and trigger cleanup via proper method
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			futures[i].complete(nil, nil)
			// Use the proper cleanup method (which is now idempotent)
			hash := sp.hashQuery(queries[i])
			sp.cleanup(hash)
		}
	}()

	// Goroutine 2: CancelAll concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(1 * time.Millisecond) // Small delay to interleave
		sp.CancelAll()
	}()

	wg.Wait()

	// The key invariant: inflightCount should never go negative
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("inflightCount went negative: %d", count)
	}
}

// TestW4N10_CancelAll_CounterNeverNegative verifies counter never goes negative.
func TestW4N10_CancelAll_CounterNeverNegative(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     100,
	})

	ctx := context.Background()
	var negativeDetected atomic.Bool

	// Monitor the counter in a separate goroutine
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if sp.InflightCount() < 0 {
					negativeDetected.Store(true)
				}
			}
		}
	}()

	// Run multiple iterations of start/cancel
	for iter := 0; iter < 20; iter++ {
		// Start prefetches
		for i := 0; i < 5; i++ {
			_, _ = sp.StartSpeculative(ctx, "negQuery"+string(rune('A'+i))+string(rune('0'+iter)))
		}

		// Cancel all
		sp.CancelAll()

		// Brief pause
		time.Sleep(100 * time.Microsecond)
	}

	close(done)

	// Final check
	finalCount := sp.InflightCount()
	if finalCount < 0 {
		t.Errorf("final inflightCount is negative: %d", finalCount)
	}

	if negativeDetected.Load() {
		t.Error("counter went negative at some point during execution")
	}
}

// TestW4N10_CancelAll_ConcurrentWithCleanup tests race between CancelAll and cleanup.
func TestW4N10_CancelAll_ConcurrentWithCleanup(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     100,
	})

	ctx := context.Background()

	// Run multiple concurrent operations
	var wg sync.WaitGroup
	iterations := 50
	queriesPerIteration := 5

	for iter := 0; iter < iterations; iter++ {
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()

			// Start some prefetches
			hashes := make([]string, 0, queriesPerIteration)
			for i := 0; i < queriesPerIteration; i++ {
				query := "concurrentQuery" + string(rune('A'+iteration%26)) + string(rune('0'+i))
				_, _ = sp.StartSpeculative(ctx, query)
				hashes = append(hashes, sp.hashQuery(query))
			}

			// Randomly either cancel all or cleanup individual
			if iteration%2 == 0 {
				sp.CancelAll()
			} else {
				for _, hash := range hashes {
					sp.cleanup(hash)
				}
			}
		}(iter)
	}

	wg.Wait()

	// Verify counter is not negative
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("inflightCount went negative after concurrent operations: %d", count)
	}
}

// TestW4N10_CancelAll_NoInflight tests CancelAll with no in-flight prefetches.
func TestW4N10_CancelAll_NoInflight(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     10,
	})

	// Verify initial state
	if count := sp.InflightCount(); count != 0 {
		t.Errorf("expected 0 inflight initially, got %d", count)
	}

	// CancelAll on empty should not panic or cause issues
	sp.CancelAll()

	// Verify still at 0
	if count := sp.InflightCount(); count != 0 {
		t.Errorf("expected 0 inflight after CancelAll on empty, got %d", count)
	}

	// Should still be functional
	ctx := context.Background()
	future, err := sp.StartSpeculative(ctx, "afterEmptyCancel")
	if err != nil {
		t.Fatalf("failed to start prefetch after empty CancelAll: %v", err)
	}
	if future == nil {
		t.Error("expected non-nil future after empty CancelAll")
	}
}

// TestW4N10_CancelAll_MultipleCalls tests multiple CancelAll calls.
func TestW4N10_CancelAll_MultipleCalls(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     20,
	})

	ctx := context.Background()

	// Start prefetches
	for i := 0; i < 5; i++ {
		_, err := sp.StartSpeculative(ctx, "multiCancelQuery"+string(rune('A'+i)))
		if err != nil {
			t.Fatalf("failed to start prefetch %d: %v", i, err)
		}
	}

	// Call CancelAll multiple times
	for i := 0; i < 10; i++ {
		sp.CancelAll()
	}

	// Counter should not be negative
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("inflightCount went negative after multiple CancelAll: %d", count)
	}
}

// TestW4N10_CancelAll_ConcurrentMultipleCancelAll tests concurrent CancelAll calls.
func TestW4N10_CancelAll_ConcurrentMultipleCancelAll(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     50,
	})

	ctx := context.Background()

	// Start prefetches
	for i := 0; i < 20; i++ {
		_, _ = sp.StartSpeculative(ctx, "concurrentCancelQuery"+string(rune('A'+i)))
	}

	// Launch multiple concurrent CancelAll calls
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sp.CancelAll()
		}()
	}

	wg.Wait()

	// Counter should not be negative
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("inflightCount went negative after concurrent CancelAll calls: %d", count)
	}
}

// TestW4N10_CancelAll_RaceDetection runs with race detector to find races.
func TestW4N10_CancelAll_RaceDetection(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     100,
	})

	ctx := context.Background()
	var wg sync.WaitGroup

	// Concurrent prefetch starts
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = sp.StartSpeculative(ctx, "raceQuery"+string(rune('A'+i%26)))
		}
	}()

	// Concurrent CancelAll calls
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			sp.CancelAll()
			time.Sleep(10 * time.Microsecond)
		}
	}()

	// Concurrent cleanup calls
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			hash := sp.hashQuery("raceQuery" + string(rune('A'+i%26)))
			sp.cleanup(hash)
		}
	}()

	// Concurrent stats reads
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = sp.InflightCount()
			_ = sp.Stats()
		}
	}()

	wg.Wait()

	// Just verify no panic and counter is not negative
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("inflightCount went negative during race test: %d", count)
	}
}

// TestW4N10_CancelAll_InterleavedOperations tests interleaved start/cancel/cleanup.
func TestW4N10_CancelAll_InterleavedOperations(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     100,
	})

	ctx := context.Background()

	// Interleave operations
	for i := 0; i < 100; i++ {
		switch i % 3 {
		case 0:
			_, _ = sp.StartSpeculative(ctx, "interleavedQuery"+string(rune('A'+i%26)))
		case 1:
			sp.CancelAll()
		case 2:
			hash := sp.hashQuery("interleavedQuery" + string(rune('A'+i%26)))
			sp.cleanup(hash)
		}
	}

	// Verify counter is not negative
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("inflightCount went negative after interleaved operations: %d", count)
	}
}

// TestW4N10_CancelAll_CleanupAfterCancel verifies cleanup is safe after CancelAll.
// This tests the scenario where cleanup() is called for already-cleaned-up entries.
func TestW4N10_CancelAll_CleanupAfterCancel(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     10,
	})

	ctx := context.Background()

	// Start prefetches and save hashes
	hashes := make([]string, 5)
	for i := 0; i < 5; i++ {
		query := "cleanupAfterCancelQuery" + string(rune('A'+i))
		_, err := sp.StartSpeculative(ctx, query)
		if err != nil {
			t.Fatalf("failed to start prefetch %d: %v", i, err)
		}
		hashes[i] = sp.hashQuery(query)
	}

	// Note: with nil scope, prefetches run in goroutines that may complete
	// before we check, so we don't assert exact count here.
	initialCount := sp.InflightCount()
	t.Logf("Initial count after starting 5 prefetches: %d", initialCount)

	// Cancel all - this marks futures as canceled but doesn't decrement counter
	sp.CancelAll()

	// Now call cleanup for each hash multiple times (simulating redundant cleanup)
	// This tests the idempotent behavior - cleanup should only decrement once per hash
	for _, hash := range hashes {
		sp.cleanup(hash)
		sp.cleanup(hash) // Redundant call should be safe
	}

	// The key invariant: counter should never go negative
	count := sp.InflightCount()
	if count < 0 {
		t.Errorf("inflightCount went negative: %d", count)
	}
	t.Logf("Final count after cleanup: %d", count)
}

// TestW4N10_CancelAll_FuturesStillUsable verifies futures are marked properly.
func TestW4N10_CancelAll_FuturesStillUsable(t *testing.T) {
	sp := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		PrefetchTimeout: 5 * time.Second,
		MaxInflight:     10,
	})

	ctx := context.Background()

	future, err := sp.StartSpeculative(ctx, "usableQuery")
	if err != nil {
		t.Fatalf("failed to start prefetch: %v", err)
	}

	// Cancel
	sp.CancelAll()

	// Future should be usable (done and with error)
	if !future.IsDone() {
		t.Error("future should be done after CancelAll")
	}

	// Wait should return immediately with error
	result, waitErr := future.Wait()
	if waitErr != context.Canceled {
		t.Errorf("Wait() error = %v, want context.Canceled", waitErr)
	}
	if result != nil {
		t.Errorf("Wait() result = %v, want nil", result)
	}

	// GetIfReady should work
	ready := future.GetIfReady(time.Millisecond)
	if ready != nil {
		t.Errorf("GetIfReady() = %v, want nil (canceled)", ready)
	}
}
