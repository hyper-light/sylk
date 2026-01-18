package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPauseBarrier_WaitReturnsImmediatelyWhenNotEngaged(t *testing.T) {
	pb := NewPauseBarrier()
	ctx := context.Background()

	err := pb.Wait(ctx)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestPauseBarrier_WaitBlocksWhenEngaged(t *testing.T) {
	pb := NewPauseBarrier()
	pb.Engage()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := pb.Wait(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestPauseBarrier_WaitUnblocksWhenReleased(t *testing.T) {
	pb := NewPauseBarrier()
	pb.Engage()

	done := make(chan error, 1)
	go func() {
		done <- pb.Wait(context.Background())
	}()

	time.Sleep(10 * time.Millisecond)
	pb.Release()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Wait did not unblock after Release")
	}
}

func TestPauseBarrier_WaitReturnsContextErrorOnCancellation(t *testing.T) {
	pb := NewPauseBarrier()
	pb.Engage()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- pb.Wait(ctx)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Wait did not return after context cancellation")
	}
}

func TestPauseBarrier_MultipleGoroutinesWaitAndUnblock(t *testing.T) {
	pb := NewPauseBarrier()
	pb.Engage()

	const numWaiters = 10
	var wg sync.WaitGroup
	var unblocked int32

	wg.Add(numWaiters)
	for i := 0; i < numWaiters; i++ {
		go func() {
			defer wg.Done()
			_ = pb.Wait(context.Background())
			atomic.AddInt32(&unblocked, 1)
		}()
	}

	time.Sleep(20 * time.Millisecond)
	if atomic.LoadInt32(&unblocked) != 0 {
		t.Error("waiters unblocked before Release")
	}

	pb.Release()
	wg.Wait()

	if atomic.LoadInt32(&unblocked) != numWaiters {
		t.Errorf("expected %d unblocked, got %d", numWaiters, unblocked)
	}
}

func TestPauseBarrier_WaiterCountTracksBlockedOperations(t *testing.T) {
	pb := NewPauseBarrier()
	pb.Engage()

	const numWaiters = 5
	started := make(chan struct{})

	for i := 0; i < numWaiters; i++ {
		go func() {
			started <- struct{}{}
			_ = pb.Wait(context.Background())
		}()
	}

	for i := 0; i < numWaiters; i++ {
		<-started
	}
	time.Sleep(20 * time.Millisecond)

	count := pb.WaiterCount()
	if count != numWaiters {
		t.Errorf("expected %d waiters, got %d", numWaiters, count)
	}

	pb.Release()
	time.Sleep(20 * time.Millisecond)

	count = pb.WaiterCount()
	if count != 0 {
		t.Errorf("expected 0 waiters after release, got %d", count)
	}
}

func TestPauseBarrier_EngageReleaseCycleWorksMultipleTimes(t *testing.T) {
	pb := NewPauseBarrier()

	for cycle := 0; cycle < 3; cycle++ {
		pb.Engage()
		if !pb.IsEngaged() {
			t.Errorf("cycle %d: expected engaged", cycle)
		}

		done := make(chan struct{})
		go func() {
			_ = pb.Wait(context.Background())
			close(done)
		}()

		time.Sleep(10 * time.Millisecond)
		pb.Release()

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Errorf("cycle %d: Wait did not unblock", cycle)
		}

		if pb.IsEngaged() {
			t.Errorf("cycle %d: expected not engaged after release", cycle)
		}
	}
}

func TestPauseBarrier_IsEngagedReturnsCorrectState(t *testing.T) {
	pb := NewPauseBarrier()

	if pb.IsEngaged() {
		t.Error("expected not engaged initially")
	}

	pb.Engage()
	if !pb.IsEngaged() {
		t.Error("expected engaged after Engage")
	}

	pb.Release()
	if pb.IsEngaged() {
		t.Error("expected not engaged after Release")
	}
}

func TestPauseBarrier_ConcurrentEngageWaitRelease(t *testing.T) {
	pb := NewPauseBarrier()
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			pb.Engage()
			time.Sleep(time.Microsecond)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			_ = pb.Wait(ctx)
			cancel()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			pb.Release()
			time.Sleep(time.Microsecond)
		}
	}()

	wg.Wait()
}
