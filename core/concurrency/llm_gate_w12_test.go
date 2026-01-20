package concurrency

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// W12.13 - LLMGate Goroutine Leak in waitGroupWaiter Tests
// =============================================================================

// TestW12_13_SignalOnWaitGroupCompletion_Success tests the simple completion helper.
func TestW12_13_SignalOnWaitGroupCompletion_Success(t *testing.T) {
	gate := &DualQueueGate{}
	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go gate.signalOnWaitGroupCompletion(&wg, done)

	// Complete the WaitGroup
	wg.Done()

	select {
	case <-done:
		// Expected - done channel should be closed
	case <-time.After(100 * time.Millisecond):
		t.Error("done channel should be closed when WaitGroup completes")
	}
}

// TestW12_13_SignalOnWaitGroupCompletion_NoLeak verifies no goroutine leak when
// the WaitGroup completes normally.
func TestW12_13_SignalOnWaitGroupCompletion_NoLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()
	gate := &DualQueueGate{}

	for i := 0; i < 10; i++ {
		var wg sync.WaitGroup
		done := make(chan struct{})

		wg.Add(1)
		go gate.signalOnWaitGroupCompletion(&wg, done)

		// Complete the WaitGroup
		wg.Done()

		// Wait for completion
		<-done
	}

	// Allow goroutines to clean up
	time.Sleep(50 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	// Allow for some variance due to test framework goroutines
	if finalGoroutines > initialGoroutines+2 {
		t.Errorf("potential goroutine leak: initial=%d, final=%d",
			initialGoroutines, finalGoroutines)
	}
}

// TestW12_13_BoundedWaitGroupWait_NoNestedGoroutineLeak verifies that the
// simplified boundedWaitGroupWait doesn't leak goroutines on timeout.
func TestW12_13_BoundedWaitGroupWait_NoNestedGoroutineLeak(t *testing.T) {
	gate := &DualQueueGate{}

	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	// Run multiple iterations where timeout fires before WaitGroup completes
	for i := 0; i < 5; i++ {
		var wg sync.WaitGroup
		wg.Add(1) // WaitGroup will never complete in time

		// This should timeout
		gate.boundedWaitGroupWait(&wg, 20*time.Millisecond)

		// Now complete the WaitGroup to allow cleanup
		wg.Done()
	}

	// Allow goroutines to clean up
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	// Allow for small variance
	if finalGoroutines > initialGoroutines+2 {
		t.Errorf("potential goroutine leak after timeouts: initial=%d, final=%d",
			initialGoroutines, finalGoroutines)
	}
}

// TestW12_13_WaitGroupWaiter_ContextCancellation_Cleanup verifies that when
// context is cancelled, the waitGroupWaiter goroutine exits cleanly.
func TestW12_13_WaitGroupWaiter_ContextCancellation_Cleanup(t *testing.T) {
	gate := &DualQueueGate{}

	initialGoroutines := runtime.NumGoroutine()

	// Run multiple iterations where context is cancelled
	for i := 0; i < 5; i++ {
		var wg sync.WaitGroup
		done := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())

		wg.Add(1) // WaitGroup won't complete before cancellation

		// Start the waiter
		go gate.waitGroupWaiter(&wg, done, ctx)

		// Wait a bit then cancel
		time.Sleep(10 * time.Millisecond)
		cancel()

		// Wait for the outer waiter to exit
		time.Sleep(20 * time.Millisecond)

		// Now complete the WaitGroup to allow the inner goroutine to exit
		wg.Done()
	}

	// Allow goroutines to clean up
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	// Allow for small variance
	if finalGoroutines > initialGoroutines+2 {
		t.Errorf("potential goroutine leak after context cancellations: initial=%d, final=%d",
			initialGoroutines, finalGoroutines)
	}
}

// TestW12_13_WaitGroupWaiter_WaitCompletes tests normal completion path.
func TestW12_13_WaitGroupWaiter_WaitCompletes(t *testing.T) {
	gate := &DualQueueGate{}
	var wg sync.WaitGroup
	done := make(chan struct{})
	ctx := context.Background()

	wg.Add(1)

	go gate.waitGroupWaiter(&wg, done, ctx)

	// Complete the WaitGroup
	wg.Done()

	// The done channel should be closed
	select {
	case <-done:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("done channel should be closed when WaitGroup completes")
	}
}

// TestW12_13_GateShutdown_NoLeakOnTimeout tests that full gate shutdown doesn't
// leak goroutines even when requests don't respond to cancellation.
func TestW12_13_GateShutdown_NoLeakOnTimeout(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	blockCh := make(chan struct{})
	requestStarted := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			select {
			case requestStarted <- struct{}{}:
			default:
			}
			// Block until blockCh is closed, but respect context
			select {
			case <-blockCh:
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 2,
		ShutdownTimeout:       50 * time.Millisecond,
		HardDeadline:          100 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit requests
	for i := 0; i < 2; i++ {
		req := &LLMRequest{
			ID:          "leak-test",
			AgentType:   AgentGuide,
			UserInvoked: true,
		}
		gate.Submit(context.Background(), req)
	}

	// Wait for requests to start
	for i := 0; i < 2; i++ {
		select {
		case <-requestStarted:
		case <-time.After(time.Second):
			t.Fatal("requests did not start")
		}
	}

	// Close the gate - will wait then timeout
	gate.Close()

	// Unblock the requests
	close(blockCh)

	// Allow cleanup
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	// Allow for test framework variance
	if finalGoroutines > initialGoroutines+3 {
		t.Errorf("potential goroutine leak after gate shutdown: initial=%d, final=%d",
			initialGoroutines, finalGoroutines)
	}
}

// TestW12_13_ConcurrentBoundedWaits tests concurrent use of boundedWaitGroupWait.
func TestW12_13_ConcurrentBoundedWaits(t *testing.T) {
	gate := &DualQueueGate{}
	var wg sync.WaitGroup

	// Launch multiple concurrent bounded waits
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var innerWg sync.WaitGroup
			innerWg.Add(1)

			// Start a goroutine that will complete the inner WaitGroup
			go func() {
				time.Sleep(5 * time.Millisecond)
				innerWg.Done()
			}()

			gate.boundedWaitGroupWait(&innerWg, 100*time.Millisecond)
		}()
	}

	// Wait for all to complete
	wg.Wait()
}

// TestW12_13_RaceCondition_BoundedWaitWithCompletion tests race between timeout
// and WaitGroup completion.
func TestW12_13_RaceCondition_BoundedWaitWithCompletion(t *testing.T) {
	gate := &DualQueueGate{}

	// Run multiple iterations to catch race conditions
	for i := 0; i < 50; i++ {
		var wg sync.WaitGroup
		wg.Add(1)

		// Start completion in parallel with timeout - race condition scenario
		go func() {
			// Random small delay to create race
			time.Sleep(time.Duration(i%3) * time.Millisecond)
			wg.Done()
		}()

		// Very short timeout to create race
		gate.boundedWaitGroupWait(&wg, 5*time.Millisecond)
	}
}

// =============================================================================
// W12.14 - LLMGate Race in Cancel Tests
// =============================================================================

// TestW12_14_Cancel_RaceWithExecution tests that Cancel is thread-safe
// when called concurrently with request execution.
func TestW12_14_Cancel_RaceWithExecution(t *testing.T) {
	started := make(chan struct{})
	var cancelled atomic.Bool

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(started)
			<-ctx.Done()
			cancelled.Store(true)
			return nil, ctx.Err()
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	req := &LLMRequest{
		ID:          "cancel-race-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	<-started

	// Cancel from multiple goroutines concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate.Cancel("cancel-race-test")
		}()
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	if !cancelled.Load() {
		t.Error("request should be cancelled")
	}
}

// TestW12_14_Cancel_NonExistentRequest tests cancelling a request that doesn't exist.
func TestW12_14_Cancel_NonExistentRequest(t *testing.T) {
	executor := &mockExecutor{}
	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	// Should not panic
	gate.Cancel("non-existent")
	gate.Cancel("")
}

// TestW12_14_Cancel_ConcurrentWithClose tests Cancel concurrent with Close.
func TestW12_14_Cancel_ConcurrentWithClose(t *testing.T) {
	blockCh := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			select {
			case <-blockCh:
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)

	// Submit a request
	req := &LLMRequest{
		ID:          "close-cancel-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	// Give it time to start
	time.Sleep(20 * time.Millisecond)

	// Cancel and Close concurrently
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		gate.Cancel("close-cancel-test")
	}()
	go func() {
		defer wg.Done()
		gate.Close()
	}()

	wg.Wait()
	close(blockCh)
}
