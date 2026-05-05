package concurrency

import (
	"context"
	"sync"
)

// agentSemaphore is a counting semaphore with per-waiter channels and a
// FIFO waiter queue. Replaces the prior cond.Broadcast pattern in the
// goroutine budget, where a single context cancellation woke every
// blocked Acquire on the agent — the classic thundering-herd shape.
//
// Permits are tokens: limit is the cap, free is the number currently
// available. Acquire takes a token (blocks if free == 0). Release
// returns a token (or hands it directly to the head waiter when
// someone is queued). SetLimit grows or shrinks the cap dynamically;
// growth wakes waiters in FIFO order, shrink simply caps free.
type agentSemaphore struct {
	mu      sync.Mutex
	free    int64
	limit   int64
	waiters []chan struct{} // FIFO; each chan is buffered(1)
}

func newAgentSemaphore(limit int64) *agentSemaphore {
	if limit < 0 {
		limit = 0
	}
	return &agentSemaphore{free: limit, limit: limit}
}

// TryAcquire takes a permit if one is available, otherwise returns
// false. Never blocks.
func (s *agentSemaphore) TryAcquire() bool {
	s.mu.Lock()
	if s.free > 0 {
		s.free--
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()
	return false
}

// Acquire takes a permit, blocking until one is available or ctx is
// cancelled. On ctx cancellation it returns ctx.Err() and ensures any
// permit racing into our slot is forwarded to the next waiter so no
// permit is lost.
func (s *agentSemaphore) Acquire(ctx context.Context) error {
	s.mu.Lock()
	if s.free > 0 {
		s.free--
		s.mu.Unlock()
		return nil
	}
	ch := make(chan struct{}, 1)
	s.waiters = append(s.waiters, ch)
	s.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		select {
		case <-ch:
			// Cancellation raced with delivery; we already hold a
			// permit. Hand it off rather than leak.
			s.mu.Unlock()
			s.Release()
		default:
			s.removeWaiterLocked(ch)
			s.mu.Unlock()
		}
		return ctx.Err()
	}
}

// Release returns a permit. If a waiter is queued, the permit is
// handed directly to the head waiter — exactly one wakeup, no herd.
// Otherwise free is incremented (clamped at limit).
func (s *agentSemaphore) Release() {
	s.mu.Lock()
	for len(s.waiters) > 0 {
		next := s.waiters[0]
		s.waiters = s.waiters[1:]
		select {
		case next <- struct{}{}:
			s.mu.Unlock()
			return
		default:
			// Waiter slot already full (cancelled mid-deliver and
			// rescued its own permit, or we picked up a stale entry
			// removed-after-send). Skip and keep looking.
			continue
		}
	}
	s.free++
	if s.free > s.limit {
		s.free = s.limit
	}
	s.mu.Unlock()
}

// SetLimit changes the permit cap. Growth releases additional permits
// to queued waiters in FIFO order; shrink caps free at the new limit
// but does not revoke permits already held by callers (they will be
// reclaimed naturally on Release once the held count drops below the
// new limit).
func (s *agentSemaphore) SetLimit(n int64) {
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	delta := n - s.limit
	s.limit = n
	switch {
	case delta > 0:
		// Hand new permits to waiters first.
		for delta > 0 && len(s.waiters) > 0 {
			next := s.waiters[0]
			s.waiters = s.waiters[1:]
			select {
			case next <- struct{}{}:
				delta--
			default:
				continue
			}
		}
		if delta > 0 {
			s.free += delta
			if s.free > s.limit {
				s.free = s.limit
			}
		}
	case delta < 0:
		s.free += delta
		if s.free < 0 {
			s.free = 0
		}
	}
	s.mu.Unlock()
}

// Limit returns the current permit cap.
func (s *agentSemaphore) Limit() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limit
}

// Waiters returns the number of goroutines currently blocked in
// Acquire. Used for telemetry.
func (s *agentSemaphore) Waiters() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waiters)
}

func (s *agentSemaphore) removeWaiterLocked(target chan struct{}) {
	for i, ch := range s.waiters {
		if ch == target {
			s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
			return
		}
	}
}
