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
	requestStarted := make(chan struct{}, 2) // Buffered to avoid blocking

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			requestStarted <- struct{}{}
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
			t.Fatalf("request %d did not start", i)
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

// TestW12_14_GetRequestCancelFunc_Exists tests getting cancel func for existing request.
func TestW12_14_GetRequestCancelFunc_Exists(t *testing.T) {
	started := make(chan struct{})
	blockCh := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(started)
			select {
			case <-blockCh:
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer func() {
		close(blockCh)
		gate.Close()
	}()

	req := &LLMRequest{
		ID:          "get-cancel-func-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	<-started

	// Get the cancel func
	cancelFunc := gate.getRequestCancelFunc("get-cancel-func-test")
	if cancelFunc == nil {
		t.Error("expected non-nil cancel func for active request")
	}

	// Verify it can be called without error
	cancelFunc()
}

// TestW12_14_GetRequestCancelFunc_NotExists tests getting cancel func for non-existent request.
func TestW12_14_GetRequestCancelFunc_NotExists(t *testing.T) {
	executor := &mockExecutor{}
	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	cancelFunc := gate.getRequestCancelFunc("non-existent")
	if cancelFunc != nil {
		t.Error("expected nil cancel func for non-existent request")
	}
}

// TestW12_14_Cancel_ConcurrentWithCompletion tests Cancel racing with request completion.
func TestW12_14_Cancel_ConcurrentWithCompletion(t *testing.T) {
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			// Very short execution to create race with Cancel
			time.Sleep(time.Millisecond)
			return "done", nil
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	// Run multiple iterations to catch race conditions
	for i := 0; i < 50; i++ {
		req := &LLMRequest{
			ID:          "completion-race-" + string(rune('0'+i%10)),
			AgentType:   AgentGuide,
			UserInvoked: true,
			ResultCh:    make(chan *LLMResult, 1),
		}
		gate.Submit(context.Background(), req)

		// Cancel concurrently with potential completion
		go gate.Cancel(req.ID)
	}

	// Allow all requests to complete
	time.Sleep(100 * time.Millisecond)
}

// TestW12_14_Cancel_StressTest stress tests concurrent Cancel calls.
func TestW12_14_Cancel_StressTest(t *testing.T) {
	blockCh := make(chan struct{})
	var started atomic.Int32

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			started.Add(1)
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
		MaxConcurrentRequests: 10,
		ShutdownTimeout:       time.Second,
	}
	gate := NewDualQueueGate(config, executor)
	defer func() {
		close(blockCh)
		gate.Close()
	}()

	// Submit multiple requests
	reqIDs := make([]string, 10)
	for i := 0; i < 10; i++ {
		reqIDs[i] = "stress-" + string(rune('0'+i))
		req := &LLMRequest{
			ID:          reqIDs[i],
			AgentType:   AgentGuide,
			UserInvoked: true,
		}
		gate.Submit(context.Background(), req)
	}

	// Wait for all to start
	time.Sleep(50 * time.Millisecond)

	// Cancel all from multiple goroutines concurrently
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Cancel random request
			gate.Cancel(reqIDs[n%len(reqIDs)])
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// W12.15 - LLMGate Deadlock in waitForSpace Tests
// =============================================================================

// TestW12_15_WaitForSpace_NoDeadlock verifies no deadlock in waitForSpace when
// using the block policy with concurrent Push and Pop operations.
func TestW12_15_WaitForSpace_NoDeadlock(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      2,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 100 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "1"})
	q.Push(&LLMRequest{ID: "2"})

	// Start a goroutine that will try to push (and block)
	pushDone := make(chan error)
	go func() {
		pushDone <- q.Push(&LLMRequest{ID: "3"})
	}()

	// Pop an item to make space
	time.Sleep(20 * time.Millisecond)
	popped := q.Pop()
	if popped == nil {
		t.Error("expected to pop an item")
	}

	// The push should now succeed
	select {
	case err := <-pushDone:
		if err != nil {
			t.Errorf("push should have succeeded, got error: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("push did not complete - potential deadlock")
	}
}

// TestW12_15_WaitForSpace_Timeout verifies timeout works correctly without deadlock.
func TestW12_15_WaitForSpace_Timeout(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 50 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "1"})

	// Try to push - should timeout
	start := time.Now()
	err := q.Push(&LLMRequest{ID: "2"})
	elapsed := time.Since(start)

	if err != ErrUserQueueFull {
		t.Errorf("expected ErrUserQueueFull, got %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("expected to block for ~50ms, blocked for %v", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

// TestW12_15_WaitForSpace_CloseUnblocks verifies Close unblocks waiting pushers.
func TestW12_15_WaitForSpace_CloseUnblocks(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 5 * time.Second,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "1"})

	// Start a goroutine that will block on push
	pushDone := make(chan error)
	go func() {
		pushDone <- q.Push(&LLMRequest{ID: "2"})
	}()

	// Give time to start blocking
	time.Sleep(20 * time.Millisecond)

	// Close the queue
	q.Close()

	// The push should return with closed error
	select {
	case err := <-pushDone:
		if err != ErrGateClosed {
			t.Errorf("expected ErrGateClosed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("push did not complete after close - potential deadlock")
	}
}

// TestW12_15_WaitForSpace_ConcurrentPushPop stress tests concurrent push/pop.
func TestW12_15_WaitForSpace_ConcurrentPushPop(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      5,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 100 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	var wg sync.WaitGroup
	var pushErrors atomic.Int32
	var popCount atomic.Int32

	// Start multiple pushers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := q.Push(&LLMRequest{ID: "push"}); err != nil {
				pushErrors.Add(1)
			}
		}(i)
	}

	// Start multiple poppers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			time.Sleep(time.Duration(n%5) * time.Millisecond)
			if q.Pop() != nil {
				popCount.Add(1)
			}
		}(i)
	}

	// Wait with timeout to detect deadlock
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no deadlock
	case <-time.After(5 * time.Second):
		t.Error("concurrent push/pop did not complete - potential deadlock")
	}
}

// TestW12_15_TimedWakeup_CleanExit verifies timedWakeup goroutine exits cleanly.
func TestW12_15_TimedWakeup_CleanExit(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: time.Second,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "1"})

	// Start multiple pushers that will block
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Try to push, will block then timeout
			q.Push(&LLMRequest{ID: "block"})
		}(i)
	}

	// Give time to start blocking
	time.Sleep(20 * time.Millisecond)

	// Pop to unblock one
	q.Pop()

	// Wait for all to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(3 * time.Second):
		t.Error("pushers did not complete - goroutine leak or deadlock")
	}
}
