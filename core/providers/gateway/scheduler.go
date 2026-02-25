package gateway

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

// waiter represents a queued request waiting for a concurrency slot.
type waiter struct {
	priority   RequestPriority
	enqueuedAt time.Time
	readyCh    chan struct{}
	index      int // heap index
}

// effectivePriority returns the aging-adjusted priority. Lower-priority
// requests gain agingRate points per second of wait time, preventing
// indefinite starvation.
func (w *waiter) effectivePriority(agingRate float64) float64 {
	age := time.Since(w.enqueuedAt).Seconds()
	return float64(w.priority) + age*agingRate
}

// waiterHeap is a max-heap ordered by effective priority (highest first),
// with FIFO tiebreak for equal priorities.
type waiterHeap struct {
	entries   []*waiter
	agingRate float64
}

func (h *waiterHeap) Len() int { return len(h.entries) }

func (h *waiterHeap) Less(i, j int) bool {
	pi := h.entries[i].effectivePriority(h.agingRate)
	pj := h.entries[j].effectivePriority(h.agingRate)
	if pi != pj {
		return pi > pj // max-heap: higher priority first
	}
	// FIFO tiebreak: earlier enqueue time wins
	return h.entries[i].enqueuedAt.Before(h.entries[j].enqueuedAt)
}

func (h *waiterHeap) Swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.entries[i].index = i
	h.entries[j].index = j
}

func (h *waiterHeap) Push(x any) {
	w := x.(*waiter)
	w.index = len(h.entries)
	h.entries = append(h.entries, w)
}

func (h *waiterHeap) Pop() any {
	old := h.entries
	n := len(old)
	w := old[n-1]
	old[n-1] = nil // avoid memory leak
	w.index = -1
	h.entries = old[:n-1]
	return w
}

// Scheduler provides priority-fair scheduling with aging for queued requests
// waiting for concurrency slots.
type Scheduler struct {
	mu       sync.Mutex
	heap     *waiterHeap
	maxSize  int
	closed   bool
}

// NewScheduler creates a scheduler with bounded queue size and aging rate.
func NewScheduler(maxSize int, agingRate float64) *Scheduler {
	h := &waiterHeap{
		entries:   make([]*waiter, 0, maxSize),
		agingRate: agingRate,
	}
	heap.Init(h)
	return &Scheduler{
		heap:    h,
		maxSize: maxSize,
	}
}

// Enqueue adds a waiter to the priority queue. Returns the signal channel
// that will be closed when a slot becomes available, or ErrSchedulerFull
// if the queue is at capacity.
func (s *Scheduler) Enqueue(priority RequestPriority) (chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrGatewayClosed
	}
	if s.heap.Len() >= s.maxSize {
		return nil, ErrSchedulerFull
	}

	w := &waiter{
		priority:   priority,
		enqueuedAt: time.Now(),
		readyCh:    make(chan struct{}),
	}
	heap.Push(s.heap, w)
	return w.readyCh, nil
}

// SignalNext re-sorts the heap (to account for aging) and signals the
// highest effective-priority waiter. Returns true if a waiter was signaled.
func (s *Scheduler) SignalNext() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.heap.Len() == 0 {
		return false
	}

	// Re-init heap to recalculate effective priorities with current time.
	heap.Init(s.heap)

	w := heap.Pop(s.heap).(*waiter)
	close(w.readyCh)
	return true
}

// WaitForSlot enqueues a waiter and blocks until signaled, context canceled,
// or maxWait exceeded. On cancellation or timeout the waiter is removed from
// the queue.
func (s *Scheduler) WaitForSlot(ctx context.Context, priority RequestPriority, maxWait time.Duration) error {
	readyCh, err := s.Enqueue(priority)
	if err != nil {
		return err
	}

	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	select {
	case <-readyCh:
		return nil
	case <-ctx.Done():
		s.removeWaiter(readyCh)
		return ctx.Err()
	case <-timer.C:
		s.removeWaiter(readyCh)
		return ErrGatewayTimeout
	}
}

// removeWaiter finds and removes a waiter identified by its readyCh.
func (s *Scheduler) removeWaiter(readyCh chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, w := range s.heap.entries {
		if w.readyCh == readyCh {
			heap.Remove(s.heap, i)
			return
		}
	}
}

// Len returns the current number of queued waiters.
func (s *Scheduler) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heap.Len()
}

// Close signals all queued waiters and prevents further enqueues.
func (s *Scheduler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true

	for s.heap.Len() > 0 {
		w := heap.Pop(s.heap).(*waiter)
		close(w.readyCh)
	}
}
