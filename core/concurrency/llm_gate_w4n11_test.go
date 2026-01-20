package concurrency

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestW4N11_BlockedPush_SucceedsWhenSpaceAvailable tests the happy path where
// a blocked push succeeds when space becomes available via Pop().
func TestW4N11_BlockedPush_SucceedsWhenSpaceAvailable(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: time.Second,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	if err := q.Push(&LLMRequest{ID: "first"}); err != nil {
		t.Fatalf("unexpected error pushing first item: %v", err)
	}

	pushDone := make(chan error, 1)
	pushStarted := make(chan struct{})

	// Start a goroutine that will block on Push
	go func() {
		close(pushStarted)
		err := q.Push(&LLMRequest{ID: "second"})
		pushDone <- err
	}()

	// Wait for push to start blocking
	<-pushStarted
	time.Sleep(20 * time.Millisecond)

	// Pop to make space - this should wake the blocked push
	popped := q.Pop()
	if popped == nil || popped.ID != "first" {
		t.Errorf("expected to pop 'first', got %v", popped)
	}

	// The push should now succeed
	select {
	case err := <-pushDone:
		if err != nil {
			t.Errorf("expected push to succeed, got error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("blocked push did not complete after space was made available")
	}

	// Verify the second item is now in the queue
	if q.Len() != 1 {
		t.Errorf("expected queue length 1, got %d", q.Len())
	}

	second := q.Pop()
	if second == nil || second.ID != "second" {
		t.Errorf("expected to pop 'second', got %v", second)
	}
}

// TestW4N11_BlockedPush_TimesOut tests that a blocked push times out when no
// space becomes available within the block timeout.
func TestW4N11_BlockedPush_TimesOut(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 50 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	if err := q.Push(&LLMRequest{ID: "first"}); err != nil {
		t.Fatalf("unexpected error pushing first item: %v", err)
	}

	// This push should block and then timeout
	start := time.Now()
	err := q.Push(&LLMRequest{ID: "second"})
	elapsed := time.Since(start)

	if err != ErrUserQueueFull {
		t.Errorf("expected ErrUserQueueFull after timeout, got %v", err)
	}

	// Verify it blocked for approximately the timeout duration
	if elapsed < 40*time.Millisecond {
		t.Errorf("push returned too quickly: %v (expected ~50ms)", elapsed)
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("push took too long: %v (expected ~50ms)", elapsed)
	}

	// Queue should still have only the first item
	if q.Len() != 1 {
		t.Errorf("expected queue length 1, got %d", q.Len())
	}
}

// TestW4N11_BlockedPush_WakesOnPop tests that blocked pushes are properly
// woken up when Pop() is called.
func TestW4N11_BlockedPush_WakesOnPop(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: time.Second,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "blocker"})

	wakeupTimes := make([]time.Duration, 0, 3)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Start multiple blocked pushers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			start := time.Now()
			_ = q.Push(&LLMRequest{ID: "pusher"})
			elapsed := time.Since(start)
			mu.Lock()
			wakeupTimes = append(wakeupTimes, elapsed)
			mu.Unlock()
		}(i)
	}

	// Give goroutines time to block
	time.Sleep(30 * time.Millisecond)

	// Pop items to wake up blocked pushers one at a time
	popStart := time.Now()
	for i := 0; i < 3; i++ {
		q.Pop()
		time.Sleep(20 * time.Millisecond) // Small delay between pops
	}
	popDuration := time.Since(popStart)

	wg.Wait()

	// All pushers should have been woken up during the pop phase
	mu.Lock()
	defer mu.Unlock()

	if len(wakeupTimes) != 3 {
		t.Errorf("expected 3 wakeup times, got %d", len(wakeupTimes))
	}

	// All wakeups should have happened within a reasonable time after blocking started
	for i, d := range wakeupTimes {
		if d > popDuration+100*time.Millisecond {
			t.Errorf("pusher %d took too long to wake up: %v", i, d)
		}
	}
}

// TestW4N11_ConcurrentPushAndPop tests race conditions between concurrent
// push and pop operations with blocking enabled.
func TestW4N11_ConcurrentPushAndPop(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      5,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 500 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	var pushErrors atomic.Int32
	var pushSuccesses atomic.Int32
	var popCount atomic.Int32

	var wg sync.WaitGroup

	// Start pushers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				err := q.Push(&LLMRequest{ID: "concurrent"})
				if err != nil {
					pushErrors.Add(1)
				} else {
					pushSuccesses.Add(1)
				}
			}
		}(i)
	}

	// Start poppers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if req := q.Pop(); req != nil {
					popCount.Add(1)
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Drain any remaining items
	for q.Pop() != nil {
		popCount.Add(1)
	}

	// The total pops should equal total successful pushes
	successTotal := pushSuccesses.Load()
	popTotal := popCount.Load()
	if popTotal != successTotal {
		t.Errorf("pop count (%d) doesn't match push successes (%d)", popTotal, successTotal)
	}

	t.Logf("Push successes: %d, Push errors: %d, Pops: %d",
		successTotal, pushErrors.Load(), popTotal)
}

// TestW4N11_ZeroBlockTimeout tests the edge case where block timeout is zero.
func TestW4N11_ZeroBlockTimeout(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 0, // Will be normalized to default (5s)
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "first"})

	// With zero timeout (normalized to default), we can't easily test timeout
	// So we test that pop wakes up the blocked push quickly
	pushDone := make(chan error, 1)

	go func() {
		err := q.Push(&LLMRequest{ID: "second"})
		pushDone <- err
	}()

	// Give time to block
	time.Sleep(20 * time.Millisecond)

	// Pop to make space
	q.Pop()

	// Push should complete quickly
	select {
	case err := <-pushDone:
		if err != nil {
			t.Errorf("expected push to succeed, got error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("blocked push did not wake up after pop")
	}
}

// TestW4N11_ExactlyAtCapacity tests the edge case where the queue is exactly
// at capacity.
func TestW4N11_ExactlyAtCapacity(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      3,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 100 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	// Fill to exactly capacity
	for i := 0; i < 3; i++ {
		if err := q.Push(&LLMRequest{ID: "fill"}); err != nil {
			t.Fatalf("unexpected error filling queue: %v", err)
		}
	}

	if !q.IsFull() {
		t.Error("expected queue to be full")
	}

	// Next push should block
	pushDone := make(chan error, 1)
	go func() {
		err := q.Push(&LLMRequest{ID: "overflow"})
		pushDone <- err
	}()

	// Wait a bit and verify it's blocking
	select {
	case <-pushDone:
		t.Fatal("push should be blocking at capacity")
	case <-time.After(30 * time.Millisecond):
		// Expected - still blocking
	}

	// Pop one item to make space
	q.Pop()

	// Now push should succeed
	select {
	case err := <-pushDone:
		if err != nil {
			t.Errorf("expected push to succeed after pop, got error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("blocked push did not complete after making space")
	}

	// Queue should be back to capacity
	if q.Len() != 3 {
		t.Errorf("expected queue length 3, got %d", q.Len())
	}
}

// TestW4N11_BlockedPush_ClosedQueue tests that blocked pushes return
// ErrGateClosed when the queue is closed.
func TestW4N11_BlockedPush_ClosedQueue(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: time.Second,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "first"})

	pushDone := make(chan error, 1)
	pushStarted := make(chan struct{})

	go func() {
		close(pushStarted)
		err := q.Push(&LLMRequest{ID: "second"})
		pushDone <- err
	}()

	<-pushStarted
	time.Sleep(20 * time.Millisecond)

	// Close the queue - this should wake blocked pushers with ErrGateClosed
	q.Close()

	select {
	case err := <-pushDone:
		if err != ErrGateClosed {
			t.Errorf("expected ErrGateClosed, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("blocked push did not return after queue was closed")
	}
}

// TestW4N11_MultipleBlockedPushers_AllWakeOnClose tests that multiple blocked
// pushers all wake up when the queue is closed.
func TestW4N11_MultipleBlockedPushers_AllWakeOnClose(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: time.Second,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "blocker"})

	var wg sync.WaitGroup
	closeErrors := atomic.Int32{}

	// Start multiple blocked pushers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := q.Push(&LLMRequest{ID: "blocked"})
			if err == ErrGateClosed {
				closeErrors.Add(1)
			}
		}()
	}

	// Give pushers time to block
	time.Sleep(30 * time.Millisecond)

	// Close the queue
	q.Close()

	// Wait for all pushers to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All pushers returned
	case <-time.After(500 * time.Millisecond):
		t.Fatal("not all blocked pushers woke up after close")
	}

	// All should have received ErrGateClosed
	if closeErrors.Load() != 5 {
		t.Errorf("expected 5 close errors, got %d", closeErrors.Load())
	}
}

// TestW4N11_ConditionVariable_NoSpuriousWakeups tests that the condition
// variable implementation doesn't have problematic spurious wakeups that
// cause premature timeout.
func TestW4N11_ConditionVariable_NoSpuriousWakeups(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 200 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "first"})

	start := time.Now()

	// This should block for the full timeout
	err := q.Push(&LLMRequest{ID: "second"})

	elapsed := time.Since(start)

	if err != ErrUserQueueFull {
		t.Errorf("expected ErrUserQueueFull, got %v", err)
	}

	// Should have waited close to the full timeout, not returned early
	if elapsed < 180*time.Millisecond {
		t.Errorf("push returned too early: %v (expected ~200ms)", elapsed)
	}
}

// TestW4N11_RapidPushPop_NoDeadlock tests that rapid push/pop cycles don't
// cause deadlocks with the condition variable.
func TestW4N11_RapidPushPop_NoDeadlock(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      2,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 100 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	pushDone := make(chan struct{})
	popDone := make(chan struct{})
	itemsToPush := 100

	// Start pusher - will push items
	go func() {
		for i := 0; i < itemsToPush; i++ {
			_ = q.Push(&LLMRequest{ID: "rapid"})
		}
		close(pushDone)
	}()

	// Start popper - continuously pop until pusher is done and queue is empty
	go func() {
		for {
			select {
			case <-pushDone:
				// Pusher done, drain remaining items
				for q.Pop() != nil {
				}
				close(popDone)
				return
			default:
				q.Pop()
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	select {
	case <-popDone:
		// Completed without deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("test timed out - possible deadlock")
	}
}

// TestW4N11_TimerCleanup tests that the timer goroutine is properly cleaned
// up when push succeeds before timeout.
func TestW4N11_TimerCleanup(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 5 * time.Second, // Long timeout
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "first"})

	pushDone := make(chan struct{})

	go func() {
		_ = q.Push(&LLMRequest{ID: "second"})
		close(pushDone)
	}()

	// Give time to block
	time.Sleep(20 * time.Millisecond)

	// Pop to make space - this should wake the push and clean up the timer
	q.Pop()

	select {
	case <-pushDone:
		// Push completed
	case <-time.After(200 * time.Millisecond):
		t.Fatal("push did not complete after pop")
	}

	// If timer wasn't properly cleaned up, we'd see issues or leaks
	// This test primarily ensures no panics or hangs occur
}

// TestW4N11_BlockedPush_RespectsCloseBeforeTimeout tests that Close() is
// respected even if the timeout hasn't expired yet.
func TestW4N11_BlockedPush_RespectsCloseBeforeTimeout(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: time.Second,
	}
	q := NewBoundedQueue(config)

	// Fill the queue
	q.Push(&LLMRequest{ID: "first"})

	start := time.Now()
	pushDone := make(chan error, 1)

	go func() {
		err := q.Push(&LLMRequest{ID: "second"})
		pushDone <- err
	}()

	// Give time to start blocking
	time.Sleep(30 * time.Millisecond)

	// Close the queue (well before the 1 second timeout)
	q.Close()

	select {
	case err := <-pushDone:
		elapsed := time.Since(start)
		if err != ErrGateClosed {
			t.Errorf("expected ErrGateClosed, got %v", err)
		}
		// Should complete quickly after close, not wait for full timeout
		if elapsed > 200*time.Millisecond {
			t.Errorf("push took too long after close: %v", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("blocked push did not return after close")
	}
}
