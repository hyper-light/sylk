package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
)

// PauseBarrier blocks operations when engaged.
// Operations check the barrier before proceeding.
type PauseBarrier struct {
	mu      sync.Mutex
	cond    *sync.Cond
	engaged bool
	waiters int32
}

// NewPauseBarrier creates a new PauseBarrier in the released state.
func NewPauseBarrier() *PauseBarrier {
	pb := &PauseBarrier{}
	pb.cond = sync.NewCond(&pb.mu)
	return pb
}

// Engage sets the barrier to engaged state.
// Subsequent Wait calls will block until Release is called.
func (pb *PauseBarrier) Engage() {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.engaged = true
}

// Release sets the barrier to released state.
// All blocked waiters are unblocked.
func (pb *PauseBarrier) Release() {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.engaged = false
	pb.cond.Broadcast()
}

// IsEngaged returns true if the barrier is engaged.
func (pb *PauseBarrier) IsEngaged() bool {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	return pb.engaged
}

// WaiterCount returns the number of operations blocked on the barrier.
func (pb *PauseBarrier) WaiterCount() int32 {
	return atomic.LoadInt32(&pb.waiters)
}

// Wait blocks if the barrier is engaged.
// Returns immediately if not engaged.
// Returns ctx.Err() if context is cancelled while waiting.
func (pb *PauseBarrier) Wait(ctx context.Context) error {
	pb.mu.Lock()
	if !pb.engaged {
		pb.mu.Unlock()
		return nil
	}

	atomic.AddInt32(&pb.waiters, 1)
	defer atomic.AddInt32(&pb.waiters, -1)

	return pb.waitForRelease(ctx)
}

// waitForRelease handles the blocking wait with context cancellation.
func (pb *PauseBarrier) waitForRelease(ctx context.Context) error {
	done := make(chan struct{})
	defer close(done)

	go pb.watchContext(ctx, done)

	for pb.engaged {
		pb.cond.Wait()
		if ctx.Err() != nil {
			pb.mu.Unlock()
			return ctx.Err()
		}
	}

	pb.mu.Unlock()
	return nil
}

// watchContext monitors context cancellation and broadcasts to wake waiters.
func (pb *PauseBarrier) watchContext(ctx context.Context, done <-chan struct{}) {
	select {
	case <-ctx.Done():
		pb.cond.Broadcast()
	case <-done:
	}
}
