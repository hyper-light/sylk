package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestW4N4_WaitForRequests_HappyPath tests that shutdown completes within
// timeout when all requests finish promptly.
func TestW4N4_WaitForRequests_HappyPath(t *testing.T) {
	requestsCompleted := atomic.Int32{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			// Short execution time
			time.Sleep(10 * time.Millisecond)
			requestsCompleted.Add(1)
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 4,
		ShutdownTimeout:       time.Second,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit a few requests
	for i := 0; i < 3; i++ {
		req := &LLMRequest{
			ID:          "happy-path-req",
			AgentType:   AgentGuide,
			UserInvoked: true,
		}
		if err := gate.Submit(context.Background(), req); err != nil {
			t.Fatalf("unexpected submit error: %v", err)
		}
	}

	// Give requests time to start
	time.Sleep(20 * time.Millisecond)

	// Close should complete within timeout since requests are fast
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Requests complete in ~10ms each, with 4 concurrent slots, all should finish
	// well within the 1 second timeout
	if elapsed > 500*time.Millisecond {
		t.Errorf("Close took too long: %v (expected < 500ms)", elapsed)
	}

	if requestsCompleted.Load() != 3 {
		t.Errorf("expected 3 requests completed, got %d", requestsCompleted.Load())
	}
}

// TestW4N4_WaitForRequests_TimeoutFiresGoroutineCleanup tests that when shutdown
// timeout fires, the wait goroutine is properly cleaned up.
func TestW4N4_WaitForRequests_TimeoutFiresGoroutineCleanup(t *testing.T) {
	blockCh := make(chan struct{})
	requestStarted := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(requestStarted)
			// Block indefinitely - IGNORE context cancellation to simulate
			// a misbehaving executor that doesn't respect cancellation
			<-blockCh
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       50 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "timeout-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	if err := gate.Submit(context.Background(), req); err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	// Wait for request to start
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start in time")
	}

	// Close - should timeout after 50ms because the executor ignores cancellation
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete around the shutdown timeout (executor ignores cancellation)
	if elapsed < 40*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("Close took unexpected time: %v (expected ~50ms)", elapsed)
	}

	// Unblock the request to allow the executor goroutine to exit
	close(blockCh)

	// Give time for any leaked goroutines to cause issues
	time.Sleep(50 * time.Millisecond)
}

// TestW4N4_WaitForRequests_RequestsPendingAtShutdown tests that requests still
// pending at shutdown time are properly handled.
func TestW4N4_WaitForRequests_RequestsPendingAtShutdown(t *testing.T) {
	blockCh := make(chan struct{})
	activeRequests := atomic.Int32{}
	cancelledRequests := atomic.Int32{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			activeRequests.Add(1)
			defer activeRequests.Add(-1)

			select {
			case <-blockCh:
				return "done", nil
			case <-ctx.Done():
				cancelledRequests.Add(1)
				return nil, ctx.Err()
			}
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 2,
		ShutdownTimeout:       100 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit requests
	for i := 0; i < 2; i++ {
		req := &LLMRequest{
			ID:          "pending-test",
			AgentType:   AgentGuide,
			UserInvoked: true,
		}
		gate.Submit(context.Background(), req)
	}

	// Wait for requests to start
	time.Sleep(50 * time.Millisecond)

	if activeRequests.Load() != 2 {
		t.Errorf("expected 2 active requests, got %d", activeRequests.Load())
	}

	// Close - requests should be cancelled
	gate.Close()

	// Allow time for cancellation propagation
	time.Sleep(50 * time.Millisecond)

	// Unblock to let goroutines exit
	close(blockCh)

	// All requests should have been cancelled
	if cancelledRequests.Load() != 2 {
		t.Errorf("expected 2 cancelled requests, got %d", cancelledRequests.Load())
	}
}

// TestW4N4_WaitForRequests_ConcurrentSubmitAndShutdown tests race conditions
// between concurrent submit and shutdown operations.
func TestW4N4_WaitForRequests_ConcurrentSubmitAndShutdown(t *testing.T) {
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			time.Sleep(5 * time.Millisecond)
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  1000,
		MaxUserQueueSize:      1000,
		MaxConcurrentRequests: 10,
		ShutdownTimeout:       500 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	var wg sync.WaitGroup
	submitErrors := atomic.Int32{}

	// Start submitting requests concurrently
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				req := &LLMRequest{
					ID:          "concurrent-test",
					AgentType:   AgentGuide,
					UserInvoked: true,
				}
				if err := gate.Submit(context.Background(), req); err != nil {
					// Expected: ErrGateClosed after shutdown
					submitErrors.Add(1)
				}
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// Close after some requests have been submitted
	time.Sleep(10 * time.Millisecond)
	gate.Close()

	wg.Wait()

	// Some submit errors are expected after gate is closed
	if submitErrors.Load() == 0 {
		t.Log("all submits succeeded before gate closed (timing-dependent)")
	}
}

// TestW4N4_WaitForRequests_ZeroTimeout tests the edge case where shutdown
// timeout is zero.
func TestW4N4_WaitForRequests_ZeroTimeout(t *testing.T) {
	blockCh := make(chan struct{})
	requestStarted := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(requestStarted)
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
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       0, // Zero timeout
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "zero-timeout-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	// Wait for request to start
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start in time")
	}

	// Close should return immediately with zero timeout
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// With zero timeout, Close should not block waiting for requests
	if elapsed > 100*time.Millisecond {
		t.Errorf("Close took too long with zero timeout: %v", elapsed)
	}

	// Cleanup
	close(blockCh)
	time.Sleep(50 * time.Millisecond)
}

// TestW4N4_WaitForRequests_NoPendingRequests tests the edge case where there
// are no pending requests at shutdown.
func TestW4N4_WaitForRequests_NoPendingRequests(t *testing.T) {
	executor := &mockExecutor{}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 4,
		ShutdownTimeout:       time.Second,
	}
	gate := NewDualQueueGate(config, executor)

	// Don't submit any requests

	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete almost immediately
	if elapsed > 100*time.Millisecond {
		t.Errorf("Close took too long with no pending requests: %v", elapsed)
	}
}

// TestW4N4_BoundedWaitGroupWait_Success tests the helper function directly
// for successful completion.
func TestW4N4_BoundedWaitGroupWait_Success(t *testing.T) {
	gate := &DualQueueGate{}
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		wg.Done()
	}()

	start := time.Now()
	gate.boundedWaitGroupWait(&wg, time.Second)
	elapsed := time.Since(start)

	// Should complete when WaitGroup is done, not at timeout
	if elapsed < 5*time.Millisecond || elapsed > 100*time.Millisecond {
		t.Errorf("unexpected wait duration: %v (expected ~10ms)", elapsed)
	}
}

// TestW4N4_BoundedWaitGroupWait_Timeout tests the helper function directly
// for timeout case.
func TestW4N4_BoundedWaitGroupWait_Timeout(t *testing.T) {
	gate := &DualQueueGate{}
	var wg sync.WaitGroup

	wg.Add(1)
	// Don't call wg.Done() - it will timeout

	start := time.Now()
	gate.boundedWaitGroupWait(&wg, 50*time.Millisecond)
	elapsed := time.Since(start)

	// Should complete at timeout
	if elapsed < 40*time.Millisecond || elapsed > 150*time.Millisecond {
		t.Errorf("unexpected wait duration: %v (expected ~50ms)", elapsed)
	}

	// Clean up the WaitGroup
	wg.Done()
}

// TestW4N4_WaitGroupWaiter_ContextCancellation tests that the waiter goroutine
// properly respects context cancellation.
func TestW4N4_WaitGroupWaiter_ContextCancellation(t *testing.T) {
	gate := &DualQueueGate{}
	var wg sync.WaitGroup
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	wg.Add(1) // WaitGroup will never complete

	go gate.waitGroupWaiter(&wg, done, ctx)

	// Cancel context
	cancel()

	// The done channel should NOT be closed (wait didn't complete)
	select {
	case <-done:
		t.Error("done channel should not be closed when context is cancelled")
	case <-time.After(100 * time.Millisecond):
		// Expected - done channel remains open
	}

	// Clean up
	wg.Done()
}

// TestW4N4_WaitGroupWaiter_WaitCompletes tests that the waiter goroutine
// properly signals completion when WaitGroup is done.
func TestW4N4_WaitGroupWaiter_WaitCompletes(t *testing.T) {
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

// TestW4N4_MultipleCloseIsIdempotent tests that calling Close multiple times
// doesn't cause issues with the wait goroutines.
func TestW4N4_MultipleCloseIsIdempotent(t *testing.T) {
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			time.Sleep(10 * time.Millisecond)
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 4,
		ShutdownTimeout:       time.Second,
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "multi-close-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	// Call Close multiple times concurrently
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate.Close()
		}()
	}

	wg.Wait()
	// If we get here without panics or hangs, the test passes
}

// TestW4N4_ShutdownWithQueuedRequests tests that queued (not yet started)
// requests are properly drained during shutdown.
func TestW4N4_ShutdownWithQueuedRequests(t *testing.T) {
	blockCh := make(chan struct{})
	requestStarted := make(chan struct{}, 1)

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			select {
			case requestStarted <- struct{}{}:
			default:
			}
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
		MaxUserQueueSize:      100,
		MaxConcurrentRequests: 1, // Only 1 concurrent, others will queue
		ShutdownTimeout:       100 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit multiple requests - first will start, others will queue
	for i := 0; i < 3; i++ {
		req := &LLMRequest{
			ID:          "queued-test",
			AgentType:   AgentGuide,
			UserInvoked: true,
			ResultCh:    make(chan *LLMResult, 1),
		}
		gate.Submit(context.Background(), req)
	}

	// Wait for first request to start
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("no request started")
	}

	// Close - should drain queued requests and wait for active one
	gate.Close()

	// Cleanup
	close(blockCh)
}
