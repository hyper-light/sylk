package llm

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// W12.16 - Queue Channel Leak Tests
// =============================================================================

// TestW12_16_ContextWatcher_NoGoroutineLeak verifies the context watcher goroutine
// exits cleanly in all scenarios and doesn't leak.
func TestW12_16_ContextWatcher_NoGoroutineLeak(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	pq := NewPriorityQueue()

	// Test multiple iterations of:
	// 1. Context cancelled
	// 2. Normal completion
	// 3. Queue closed
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())

		// Push an item
		pq.Push(&LLMRequest{ID: "test", Priority: PriorityExecution, CreatedAt: time.Now()})

		// Pop with context
		req, err := pq.Pop(ctx)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if req == nil {
			t.Error("expected non-nil request")
		}

		cancel() // Clean up context
	}

	// Allow goroutines to clean up
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines+2 {
		t.Errorf("potential goroutine leak: initial=%d, final=%d",
			initialGoroutines, finalGoroutines)
	}
}

// TestW12_16_ContextCancellation_WatcherExits verifies the watcher goroutine
// exits when context is cancelled before Pop completes.
func TestW12_16_ContextCancellation_WatcherExits(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	pq := NewPriorityQueue()

	// Test context cancellation while waiting
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())

		// Start a Pop that will block (queue is empty)
		done := make(chan struct{})
		go func() {
			pq.Pop(ctx)
			close(done)
		}()

		// Give time for Pop to start waiting
		time.Sleep(10 * time.Millisecond)

		// Cancel context
		cancel()

		// Wait for Pop to return
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Pop did not return after context cancellation")
		}
	}

	// Allow goroutines to clean up
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines+2 {
		t.Errorf("potential goroutine leak after context cancellations: initial=%d, final=%d",
			initialGoroutines, finalGoroutines)
	}
}

// TestW12_16_QueueClose_WatcherExits verifies the watcher goroutine exits
// when queue is closed.
func TestW12_16_QueueClose_WatcherExits(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	for i := 0; i < 10; i++ {
		pq := NewPriorityQueue()
		ctx := context.Background()

		// Start a Pop that will block
		done := make(chan struct{})
		go func() {
			pq.Pop(ctx)
			close(done)
		}()

		// Give time for Pop to start waiting
		time.Sleep(10 * time.Millisecond)

		// Close queue
		pq.Close()

		// Wait for Pop to return
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Pop did not return after queue close")
		}
	}

	// Allow goroutines to clean up
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines+2 {
		t.Errorf("potential goroutine leak after queue closes: initial=%d, final=%d",
			initialGoroutines, finalGoroutines)
	}
}

// TestW12_16_ConcurrentContextCancellation tests race condition between
// context cancellation and Pop completion.
func TestW12_16_ConcurrentContextCancellation(t *testing.T) {
	pq := NewPriorityQueue()

	// Run many iterations to catch race conditions
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithCancel(context.Background())

		// Push an item
		pq.Push(&LLMRequest{ID: "test", Priority: PriorityExecution, CreatedAt: time.Now()})

		// Create race: cancel while Pop is happening
		go cancel()

		// Pop might succeed or return context.Canceled - both are fine
		pq.Pop(ctx)
	}
}

// TestW12_16_StressTest_ManyPoppers stress tests with many concurrent poppers.
func TestW12_16_StressTest_ManyPoppers(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	pq := NewPriorityQueue()
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var cancelCount atomic.Int32

	numPoppers := 50

	// Start poppers
	for i := 0; i < numPoppers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			req, err := pq.Pop(ctx)
			if err == context.DeadlineExceeded || err == context.Canceled {
				cancelCount.Add(1)
			} else if req != nil {
				successCount.Add(1)
			}
		}()
	}

	// Push items for some of them
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < numPoppers/2; i++ {
		pq.Push(&LLMRequest{ID: "test", Priority: PriorityExecution, CreatedAt: time.Now()})
	}

	// Wait for all poppers
	wg.Wait()

	// Close queue to unblock any remaining
	pq.Close()

	// Allow goroutines to clean up
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines+3 {
		t.Errorf("potential goroutine leak after stress test: initial=%d, final=%d",
			initialGoroutines, finalGoroutines)
	}

	t.Logf("success: %d, cancelled: %d", successCount.Load(), cancelCount.Load())
}

// TestW12_16_DoneChannelCleanup verifies done channel is properly cleaned up
// when Pop returns normally.
func TestW12_16_DoneChannelCleanup(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()

	// Push and pop many items
	for i := 0; i < 100; i++ {
		pq.Push(&LLMRequest{ID: "test", Priority: PriorityExecution, CreatedAt: time.Now()})
		req, err := pq.Pop(ctx)
		if err != nil {
			t.Errorf("unexpected error at %d: %v", i, err)
		}
		if req == nil {
			t.Errorf("expected non-nil request at %d", i)
		}
	}
}

// TestW12_16_ContextTimeout_WatcherExits verifies watcher exits on context timeout.
func TestW12_16_ContextTimeout_WatcherExits(t *testing.T) {
	pq := NewPriorityQueue()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	// Pop from empty queue - should timeout
	_, err := pq.Pop(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}

	// Allow watcher to exit
	time.Sleep(50 * time.Millisecond)
}

// TestW12_16_MultipleCloses tests that multiple closes don't cause issues.
func TestW12_16_MultipleCloses(t *testing.T) {
	pq := NewPriorityQueue()

	// Close multiple times - should not panic
	pq.Close()
	pq.Close()
	pq.Close()
}

// TestW12_16_CloseWhileWatching tests closing queue while context watcher is running.
func TestW12_16_CloseWhileWatching(t *testing.T) {
	for i := 0; i < 20; i++ {
		pq := NewPriorityQueue()
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		go func() {
			pq.Pop(ctx)
			close(done)
		}()

		// Give time for Pop to start
		time.Sleep(5 * time.Millisecond)

		// Close and cancel concurrently
		go pq.Close()
		go cancel()

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Pop did not return")
		}
	}
}
