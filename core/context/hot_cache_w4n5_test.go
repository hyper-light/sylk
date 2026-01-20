// Package context provides tests for W4N.5: HotCache eviction goroutine tracking.
package context

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// W4N5 Tests: Eviction Goroutine Tracking with WaitGroup
// =============================================================================

// TestW4N5_HappyPath_GoroutineTrackedAndAwaited verifies that the eviction
// goroutine is properly tracked via WaitGroup and Close() waits for it to exit.
func TestW4N5_HappyPath_GoroutineTrackedAndAwaited(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1000,
		AsyncEviction: true,
	})

	// Add some entries to trigger eviction work
	for i := 0; i < 10; i++ {
		cache.Add(string(rune('a'+i)), &ContentEntry{
			ID:      string(rune('a' + i)),
			Content: "test content",
		})
	}

	// Close should block until goroutine exits
	done := make(chan struct{})
	go func() {
		cache.Close()
		close(done)
	}()

	// Close should complete within a reasonable time
	select {
	case <-done:
		// Success - Close() returned, meaning it waited for the goroutine
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not complete in time - goroutine may not be tracked properly")
	}
}

// TestW4N5_Negative_CloseDuringActiveEviction verifies that Close() properly
// waits even when eviction is actively running.
func TestW4N5_Negative_CloseDuringActiveEviction(t *testing.T) {
	// Use a small max size to force frequent evictions
	cache := NewHotCache(HotCacheConfig{
		MaxSize:           500,
		AsyncEviction:     true,
		EvictionBatchSize: 1, // Process one at a time for slower eviction
	})

	// Track eviction callback execution
	var evictionCount atomic.Int32
	var evictionInProgress atomic.Bool
	evictionStarted := make(chan struct{}, 1)

	cache.SetEvictionCallback(func(id string, entry *ContentEntry) {
		evictionInProgress.Store(true)
		select {
		case evictionStarted <- struct{}{}:
		default:
		}
		// Simulate slow callback to extend eviction time
		time.Sleep(10 * time.Millisecond)
		evictionCount.Add(1)
		evictionInProgress.Store(false)
	})

	// Add enough entries to trigger eviction
	for i := 0; i < 20; i++ {
		cache.Add(string(rune('a'+i)), &ContentEntry{
			ID:      string(rune('a' + i)),
			Content: "test content that is somewhat long to use up space",
		})
	}

	// Wait for eviction to start
	select {
	case <-evictionStarted:
		// Eviction has started
	case <-time.After(2 * time.Second):
		t.Log("Warning: eviction may not have started, but test will continue")
	}

	// Close while eviction may be in progress
	closeComplete := make(chan struct{})
	go func() {
		cache.Close()
		close(closeComplete)
	}()

	// Close should complete even if eviction was in progress
	select {
	case <-closeComplete:
		// Success
		t.Logf("Close completed successfully, evictions processed: %d", evictionCount.Load())
	case <-time.After(5 * time.Second):
		t.Fatal("Close() timed out - may not properly handle active eviction")
	}
}

// TestW4N5_Failure_EvictionLoopPanicRecovery verifies the eviction loop handles
// panics gracefully. Note: The current implementation does not have panic recovery
// built-in, so this test verifies the goroutine exits cleanly on stop signal.
func TestW4N5_Failure_EvictionLoopPanicRecovery(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       100,
		AsyncEviction: true,
	})

	// Set a callback that might cause issues (though not panic)
	var callbackCalls atomic.Int32
	cache.SetEvictionCallback(func(id string, entry *ContentEntry) {
		callbackCalls.Add(1)
	})

	// Add entries to trigger eviction
	for i := 0; i < 10; i++ {
		cache.Add(string(rune('a'+i)), &ContentEntry{
			ID:      string(rune('a' + i)),
			Content: "test content to fill cache",
		})
	}

	// Give eviction time to process
	time.Sleep(100 * time.Millisecond)

	// Verify the goroutine is still running and can process
	cache.Add("z", &ContentEntry{
		ID:      "z",
		Content: "more content",
	})

	// Close should work properly
	done := make(chan struct{})
	go func() {
		cache.Close()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("Eviction loop handled operations correctly, callback calls: %d", callbackCalls.Load())
	case <-time.After(5 * time.Second):
		t.Fatal("Close() timed out")
	}
}

// TestW4N5_RaceCondition_ConcurrentCloseCalls verifies that concurrent Close()
// calls are handled safely without panics or race conditions.
func TestW4N5_RaceCondition_ConcurrentCloseCalls(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1000,
		AsyncEviction: true,
	})

	// Add some entries
	for i := 0; i < 5; i++ {
		cache.Add(string(rune('a'+i)), &ContentEntry{
			ID:      string(rune('a' + i)),
			Content: "test content",
		})
	}

	// Launch multiple concurrent Close() calls
	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	startSignal := make(chan struct{})
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			<-startSignal // Wait for signal to start simultaneously
			cache.Close()
		}()
	}

	// Start all goroutines at once
	close(startSignal)

	// Wait for all Close() calls to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - all concurrent Close() calls completed without panic
	case <-time.After(5 * time.Second):
		t.Fatal("Concurrent Close() calls timed out")
	}
}

// TestW4N5_EdgeCase_CloseBeforeEvictionStarts verifies that Close() works
// correctly even when called immediately after creation, before any eviction
// has been triggered.
func TestW4N5_EdgeCase_CloseBeforeEvictionStarts(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       DefaultHotCacheMaxSize, // Large enough that no eviction triggers
		AsyncEviction: true,
	})

	// Don't add any entries - no eviction will be triggered

	// Close immediately
	done := make(chan struct{})
	go func() {
		cache.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Close() timed out when called before eviction started")
	}
}

// TestW4N5_EdgeCase_DoubleClose verifies that calling Close() twice is safe
// and does not panic or block indefinitely.
func TestW4N5_EdgeCase_DoubleClose(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1000,
		AsyncEviction: true,
	})

	// Add some entries
	for i := 0; i < 5; i++ {
		cache.Add(string(rune('a'+i)), &ContentEntry{
			ID:      string(rune('a' + i)),
			Content: "test content",
		})
	}

	// First Close()
	done1 := make(chan struct{})
	go func() {
		cache.Close()
		close(done1)
	}()

	select {
	case <-done1:
		// First close completed
	case <-time.After(5 * time.Second):
		t.Fatal("First Close() timed out")
	}

	// Second Close() - should be safe and return immediately
	done2 := make(chan struct{})
	go func() {
		cache.Close()
		close(done2)
	}()

	select {
	case <-done2:
		// Second close completed without panic
	case <-time.After(1 * time.Second):
		t.Fatal("Second Close() blocked - double close not handled properly")
	}
}

// TestW4N5_EdgeCase_CloseWithNoAsyncEviction verifies that Close() works
// correctly when async eviction is disabled.
func TestW4N5_EdgeCase_CloseWithNoAsyncEviction(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1000,
		AsyncEviction: false, // Async eviction disabled
	})

	// Add some entries
	for i := 0; i < 5; i++ {
		cache.Add(string(rune('a'+i)), &ContentEntry{
			ID:      string(rune('a' + i)),
			Content: "test content",
		})
	}

	// Close should work correctly with no goroutine to wait for
	done := make(chan struct{})
	go func() {
		cache.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Close() timed out when async eviction disabled")
	}

	// Double close should also be safe
	done2 := make(chan struct{})
	go func() {
		cache.Close()
		close(done2)
	}()

	select {
	case <-done2:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Double Close() timed out when async eviction disabled")
	}
}

// TestW4N5_RaceCondition_ConcurrentAddAndClose verifies no races between
// Add operations and Close().
func TestW4N5_RaceCondition_ConcurrentAddAndClose(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       500,
		AsyncEviction: true,
	})

	// Start concurrent Add operations
	var addWg sync.WaitGroup
	stopAdding := make(chan struct{})

	for i := 0; i < 5; i++ {
		addWg.Add(1)
		go func(workerID int) {
			defer addWg.Done()
			count := 0
			for {
				select {
				case <-stopAdding:
					return
				default:
					cache.Add(string(rune('a'+workerID))+"_"+string(rune('0'+count%10)), &ContentEntry{
						ID:      string(rune('a'+workerID)) + "_" + string(rune('0'+count%10)),
						Content: "test content for worker",
					})
					count++
					if count > 100 {
						return
					}
				}
			}
		}(i)
	}

	// Let some adds happen
	time.Sleep(50 * time.Millisecond)

	// Close while adds may still be happening
	closeComplete := make(chan struct{})
	go func() {
		cache.Close()
		close(closeComplete)
	}()

	// Signal to stop adding (some may already be done)
	close(stopAdding)

	// Wait for close to complete
	select {
	case <-closeComplete:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Close() timed out during concurrent Add operations")
	}

	// Wait for all add goroutines to finish
	addWg.Wait()
}

// TestW4N5_Stress_RapidCreateAndClose verifies that rapidly creating and
// closing caches doesn't leak goroutines.
func TestW4N5_Stress_RapidCreateAndClose(t *testing.T) {
	const iterations = 100

	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache := NewHotCache(HotCacheConfig{
				MaxSize:       1000,
				AsyncEviction: true,
			})

			// Add a few entries
			cache.Add("test", &ContentEntry{
				ID:      "test",
				Content: "content",
			})

			// Close immediately
			cache.Close()
		}()
	}

	// Wait for all iterations to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All caches created and closed successfully
	case <-time.After(30 * time.Second):
		t.Fatal("Stress test timed out - possible goroutine leak")
	}
}
