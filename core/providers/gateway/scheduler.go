package gateway

import (
	"container/heap"
	"context"
	"math"
	"sync"
	"time"
)

// LaneKeyFn extracts the WFQ lane key from a request context. Each distinct
// key gets its own virtual-time clock; the scheduler dispatches across lanes
// in start-time fair order so no single lane can starve others. Returning
// the empty string places the request in the shared "default" lane.
type LaneKeyFn func(ctx context.Context) string

// waiter represents a queued request waiting for a concurrency slot.
// Ordering across waiters is by virtualFinish (Start-Time Fair Queueing);
// higher RequestPriority maps to a smaller virtualFinish increment so it
// is dispatched sooner within its lane. Aging discounts the effective
// virtualFinish over wall-clock time so a long-waiting low-priority
// request will eventually overtake fresh high-priority requests.
type waiter struct {
	laneKey       string
	priority      RequestPriority
	enqueuedAt    time.Time
	virtualFinish float64
	readyCh       chan struct{}
	index         int // heap index
}

// effectiveVirtualFinish returns the priority- and aging-adjusted
// virtualFinish. Lower output is dispatched first.
//
//   - Within one lane, priority dominates: a UserInteractive request
//     subtracts more priority points than a Background request, so
//     high-priority work jumps the line even if it arrived later.
//   - Across lanes, WFQ still governs same-priority requests because
//     priority is a constant offset that cancels in cross-lane
//     comparisons of equal-priority waiters.
//   - Aging discounts the effective finish over wall-clock time so a
//     long-waiting low-priority request will eventually overtake fresh
//     high-priority requests, preventing starvation.
func (w *waiter) effectiveVirtualFinish(agingRate float64) float64 {
	age := time.Since(w.enqueuedAt).Seconds()
	return w.virtualFinish - float64(w.priority) - age*agingRate
}

// waiterHeap is a min-heap ordered by effective virtualFinish (smallest first),
// with FIFO tiebreak via enqueuedAt.
type waiterHeap struct {
	entries   []*waiter
	agingRate float64
}

func (h *waiterHeap) Len() int { return len(h.entries) }

func (h *waiterHeap) Less(i, j int) bool {
	vi := h.entries[i].effectiveVirtualFinish(h.agingRate)
	vj := h.entries[j].effectiveVirtualFinish(h.agingRate)
	if vi != vj {
		return vi < vj
	}
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

// laneState tracks per-lane virtual time. virtualTime is monotonically
// non-decreasing as waiters are enqueued on that lane; new arrivals on
// an idle lane are stamped at the current global virtual time so they
// catch up to the active lanes immediately.
type laneState struct {
	virtualTime float64
}

// Scheduler provides per-lane Weighted-Fair scheduling with priority weighting
// and aging. Waiters are never rejected on count — Enqueue always returns a
// signal channel. Callers bound their own wait via context deadline; on
// expiry they remove themselves from the queue. The only Enqueue error is
// ErrGatewayClosed, returned after Close.
type Scheduler struct {
	mu       sync.Mutex
	heap     *waiterHeap
	lanes    map[string]*laneState
	globalVT float64
	closed   bool
	keyFn    LaneKeyFn
}

// NewScheduler creates a scheduler keyed on lane keys produced by keyFn.
// agingRate sets the virtual-time discount per second of wait (0 disables
// aging). keyFn may be nil — all requests then share a single default lane.
func NewScheduler(agingRate float64, keyFn LaneKeyFn) *Scheduler {
	h := &waiterHeap{agingRate: agingRate}
	heap.Init(h)
	return &Scheduler{
		heap:  h,
		lanes: make(map[string]*laneState),
		keyFn: keyFn,
	}
}

// Enqueue adds a waiter to the WFQ heap. Always returns a signal channel
// (or ErrGatewayClosed after Close). Callers bound their own wait via
// context deadline; on cancel/timeout they call removeWaiter to clean up.
// The lane key is extracted from ctx via the configured LaneKeyFn.
func (s *Scheduler) Enqueue(ctx context.Context, priority RequestPriority) (chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrGatewayClosed
	}

	key := ""
	if s.keyFn != nil {
		key = s.keyFn(ctx)
	}
	lane, ok := s.lanes[key]
	if !ok {
		lane = &laneState{virtualTime: s.globalVT}
		s.lanes[key] = lane
	}

	// Higher RequestPriority → smaller increment → earlier virtualFinish →
	// dispatched sooner. PriorityUserInteractive (100) yields increment 1.0;
	// PriorityBackground (20) yields 5.0.
	weight := float64(priority) / float64(PriorityUserInteractive)
	if weight <= 0 {
		weight = 0.01
	}
	increment := 1.0 / weight

	startVT := math.Max(lane.virtualTime, s.globalVT)
	finishVT := startVT + increment
	lane.virtualTime = finishVT

	w := &waiter{
		laneKey:       key,
		priority:      priority,
		enqueuedAt:    time.Now(),
		virtualFinish: finishVT,
		readyCh:       make(chan struct{}),
	}
	heap.Push(s.heap, w)
	return w.readyCh, nil
}

// SignalNext re-sorts the heap (to account for aging) and signals the
// lowest-virtualFinish waiter. Returns true if a waiter was signaled.
// Advances globalVT to the signaled waiter's virtualFinish so subsequent
// arrivals on idle lanes start from the current dispatch frontier.
func (s *Scheduler) SignalNext() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.heap.Len() == 0 {
		return false
	}

	heap.Init(s.heap)
	w := heap.Pop(s.heap).(*waiter)
	if w.virtualFinish > s.globalVT {
		s.globalVT = w.virtualFinish
	}
	close(w.readyCh)
	return true
}

// WaitForSlot enqueues a waiter and blocks until signaled, ctx canceled,
// or maxWait exceeded. On cancellation or timeout the waiter is removed
// from the queue.
func (s *Scheduler) WaitForSlot(ctx context.Context, priority RequestPriority, maxWait time.Duration) error {
	readyCh, err := s.Enqueue(ctx, priority)
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
