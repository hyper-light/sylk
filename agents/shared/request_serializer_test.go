package shared

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestSerializerAcquireRelease(t *testing.T) {
	rs := NewRequestSerializer()

	// First acquire should succeed immediately.
	ctx := context.Background()
	if !rs.Acquire(ctx) {
		t.Fatal("first Acquire should succeed")
	}
	rs.Release()

	// Second acquire after release should also succeed.
	if !rs.Acquire(ctx) {
		t.Fatal("second Acquire after Release should succeed")
	}
	rs.Release()
}

func TestRequestSerializerBlocksUntilRelease(t *testing.T) {
	rs := NewRequestSerializer()

	// Acquire the token.
	ctx := context.Background()
	if !rs.Acquire(ctx) {
		t.Fatal("initial Acquire should succeed")
	}

	acquired := make(chan struct{})
	go func() {
		if rs.Acquire(ctx) {
			close(acquired)
		}
	}()

	// The goroutine should be blocked.
	select {
	case <-acquired:
		t.Fatal("second Acquire should block while token is held")
	case <-time.After(50 * time.Millisecond):
		// Expected: still blocked.
	}

	// Release the token — goroutine should proceed.
	rs.Release()

	select {
	case <-acquired:
		// Expected: unblocked.
	case <-time.After(time.Second):
		t.Fatal("second Acquire should unblock after Release")
	}
}

func TestRequestSerializerCancelledContext(t *testing.T) {
	rs := NewRequestSerializer()

	// Hold the token.
	ctx := context.Background()
	if !rs.Acquire(ctx) {
		t.Fatal("initial Acquire should succeed")
	}

	// Try to acquire with an already-cancelled context.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if rs.Acquire(cancelled) {
		t.Fatal("Acquire with cancelled context should return false")
	}

	// Original holder can still release.
	rs.Release()
}

func TestRequestSerializerContextCancelWhileWaiting(t *testing.T) {
	rs := NewRequestSerializer()

	ctx := context.Background()
	if !rs.Acquire(ctx) {
		t.Fatal("initial Acquire should succeed")
	}

	waitCtx, waitCancel := context.WithCancel(context.Background())
	result := make(chan bool, 1)
	go func() {
		result <- rs.Acquire(waitCtx)
	}()

	// Let the goroutine block on Acquire.
	time.Sleep(20 * time.Millisecond)

	// Cancel the waiting context.
	waitCancel()

	select {
	case got := <-result:
		if got {
			t.Fatal("Acquire should return false when context is cancelled while waiting")
		}
	case <-time.After(time.Second):
		t.Fatal("Acquire should return promptly after context cancel")
	}

	rs.Release()
}

func TestRequestSerializerDoubleRelease(t *testing.T) {
	rs := NewRequestSerializer()

	ctx := context.Background()
	if !rs.Acquire(ctx) {
		t.Fatal("Acquire should succeed")
	}

	// Double release should not panic or corrupt state.
	rs.Release()
	rs.Release()

	// Should still be acquirable exactly once.
	if !rs.Acquire(ctx) {
		t.Fatal("Acquire after double Release should succeed")
	}

	// Second acquire should block (only one token).
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if rs.Acquire(cancelled) {
		t.Fatal("second Acquire should fail — only one token")
	}

	rs.Release()
}

func TestRequestSerializerSerializesRequests(t *testing.T) {
	rs := NewRequestSerializer()
	ctx := context.Background()

	// Verify strict serialization: no two goroutines hold the token concurrently.
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	var wg sync.WaitGroup

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !rs.Acquire(ctx) {
				return
			}
			defer rs.Release()

			n := concurrent.Add(1)
			// Track maximum concurrency observed.
			for {
				cur := maxConcurrent.Load()
				if n <= cur || maxConcurrent.CompareAndSwap(cur, n) {
					break
				}
			}
			time.Sleep(time.Millisecond) // simulate work
			concurrent.Add(-1)
		}()
	}

	wg.Wait()

	if maxConcurrent.Load() > 1 {
		t.Fatalf("max concurrent = %d, want 1", maxConcurrent.Load())
	}
}
