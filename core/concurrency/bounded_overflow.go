package concurrency

import (
	"sync"
	"sync/atomic"
)

// DropPolicy defines the behavior when the buffer is full.
type DropPolicy int

const (
	// DropOldest removes the oldest item when adding to a full buffer.
	DropOldest DropPolicy = iota
	// DropNewest discards the new item when adding to a full buffer.
	DropNewest
	// Block waits until space is available (via condition variable).
	Block
)

// String returns a human-readable name for the drop policy.
func (dp DropPolicy) String() string {
	switch dp {
	case DropOldest:
		return "DropOldest"
	case DropNewest:
		return "DropNewest"
	case Block:
		return "Block"
	default:
		return "Unknown"
	}
}

// BoundedOverflow is a generic bounded buffer that prevents unbounded memory growth.
// It supports configurable drop policies when the buffer reaches capacity.
// All operations are thread-safe.
type BoundedOverflow[T any] struct {
	maxSize    int
	dropPolicy DropPolicy

	mu    sync.Mutex
	cond  *sync.Cond
	items []T
	head  int // Index of first item (for ring buffer)
	tail  int // Index where next item will be written
	count int // Number of items currently in buffer

	droppedCount atomic.Int64
	closed       atomic.Bool
}

// NewBoundedOverflow creates a new bounded overflow buffer with the specified
// maximum size and drop policy.
func NewBoundedOverflow[T any](maxSize int, dropPolicy DropPolicy) *BoundedOverflow[T] {
	if maxSize <= 0 {
		maxSize = 1
	}

	bo := &BoundedOverflow[T]{
		maxSize:    maxSize,
		dropPolicy: dropPolicy,
		items:      make([]T, maxSize),
		head:       0,
		tail:       0,
		count:      0,
	}
	bo.cond = sync.NewCond(&bo.mu)
	return bo
}

// Add adds an item to the buffer. Returns true if the item was added,
// false if it was dropped (only possible with DropNewest policy).
// For Block policy, this will wait until space is available.
func (bo *BoundedOverflow[T]) Add(item T) bool {
	bo.mu.Lock()
	defer bo.mu.Unlock()

	if bo.closed.Load() {
		return false
	}

	// Fast path: buffer has space
	if bo.count < bo.maxSize {
		bo.items[bo.tail] = item
		bo.tail = (bo.tail + 1) % bo.maxSize
		bo.count++
		bo.cond.Signal() // Wake up any blocked Drain calls
		return true
	}

	// Buffer is full, apply drop policy
	switch bo.dropPolicy {
	case DropOldest:
		// Overwrite the oldest item (at head)
		var zero T
		bo.items[bo.head] = zero // Clear old reference for GC
		bo.items[bo.head] = item
		bo.head = (bo.head + 1) % bo.maxSize
		bo.tail = bo.head // tail follows head since buffer is full
		bo.droppedCount.Add(1)
		bo.cond.Signal()
		return true

	case DropNewest:
		// Discard the new item
		bo.droppedCount.Add(1)
		return false

	case Block:
		// Wait until space is available
		for bo.count >= bo.maxSize && !bo.closed.Load() {
			bo.cond.Wait()
		}

		if bo.closed.Load() {
			return false
		}

		bo.items[bo.tail] = item
		bo.tail = (bo.tail + 1) % bo.maxSize
		bo.count++
		bo.cond.Signal()
		return true
	}

	return false
}

// TryAdd attempts to add an item without blocking.
// For Block policy, this returns false immediately if the buffer is full.
func (bo *BoundedOverflow[T]) TryAdd(item T) bool {
	bo.mu.Lock()
	defer bo.mu.Unlock()

	if bo.closed.Load() {
		return false
	}

	// Buffer has space
	if bo.count < bo.maxSize {
		bo.items[bo.tail] = item
		bo.tail = (bo.tail + 1) % bo.maxSize
		bo.count++
		bo.cond.Signal()
		return true
	}

	// Buffer is full
	switch bo.dropPolicy {
	case DropOldest:
		var zero T
		bo.items[bo.head] = zero
		bo.items[bo.head] = item
		bo.head = (bo.head + 1) % bo.maxSize
		bo.tail = bo.head
		bo.droppedCount.Add(1)
		bo.cond.Signal()
		return true

	case DropNewest:
		bo.droppedCount.Add(1)
		return false

	case Block:
		// Non-blocking version returns false for Block policy when full
		return false
	}

	return false
}

// Take removes and returns the oldest item from the buffer.
// Returns false if the buffer is empty.
func (bo *BoundedOverflow[T]) Take() (T, bool) {
	bo.mu.Lock()
	defer bo.mu.Unlock()

	var zero T
	if bo.count == 0 {
		return zero, false
	}

	item := bo.items[bo.head]
	bo.items[bo.head] = zero // Clear reference for GC
	bo.head = (bo.head + 1) % bo.maxSize
	bo.count--

	bo.cond.Signal() // Wake up any blocked Add calls (for Block policy)
	return item, true
}

// Drain removes and returns all items from the buffer, clearing it.
// Items are returned in FIFO order.
func (bo *BoundedOverflow[T]) Drain() []T {
	bo.mu.Lock()
	defer bo.mu.Unlock()

	if bo.count == 0 {
		return nil
	}

	result := make([]T, bo.count)
	var zero T

	for i := 0; i < len(result); i++ {
		idx := (bo.head + i) % bo.maxSize
		result[i] = bo.items[idx]
		bo.items[idx] = zero // Clear reference for GC
	}

	bo.head = 0
	bo.tail = 0
	bo.count = 0

	bo.cond.Broadcast() // Wake up all blocked Add calls
	return result
}

// Peek returns the oldest item without removing it.
// Returns false if the buffer is empty.
func (bo *BoundedOverflow[T]) Peek() (T, bool) {
	bo.mu.Lock()
	defer bo.mu.Unlock()

	var zero T
	if bo.count == 0 {
		return zero, false
	}

	return bo.items[bo.head], true
}

// Len returns the current number of items in the buffer.
func (bo *BoundedOverflow[T]) Len() int {
	bo.mu.Lock()
	length := bo.count
	bo.mu.Unlock()
	return length
}

// Cap returns the maximum capacity of the buffer.
func (bo *BoundedOverflow[T]) Cap() int {
	return bo.maxSize
}

// IsFull returns true if the buffer is at maximum capacity.
func (bo *BoundedOverflow[T]) IsFull() bool {
	bo.mu.Lock()
	full := bo.count >= bo.maxSize
	bo.mu.Unlock()
	return full
}

// IsEmpty returns true if the buffer has no items.
func (bo *BoundedOverflow[T]) IsEmpty() bool {
	bo.mu.Lock()
	empty := bo.count == 0
	bo.mu.Unlock()
	return empty
}

// DroppedCount returns the total number of items that have been dropped.
func (bo *BoundedOverflow[T]) DroppedCount() int64 {
	return bo.droppedCount.Load()
}

// ResetDroppedCount resets the dropped counter to zero and returns the previous value.
func (bo *BoundedOverflow[T]) ResetDroppedCount() int64 {
	return bo.droppedCount.Swap(0)
}

// DropPolicy returns the configured drop policy.
func (bo *BoundedOverflow[T]) DropPolicy() DropPolicy {
	return bo.dropPolicy
}

// Close marks the buffer as closed. Blocked Add operations will return false.
// The buffer can still be drained after closing.
func (bo *BoundedOverflow[T]) Close() {
	bo.closed.Store(true)
	bo.cond.Broadcast() // Wake up any blocked operations
}

// IsClosed returns true if the buffer has been closed.
func (bo *BoundedOverflow[T]) IsClosed() bool {
	return bo.closed.Load()
}

// Stats returns current buffer statistics.
type BoundedOverflowStats struct {
	Length       int
	Capacity     int
	DroppedCount int64
	IsFull       bool
	IsClosed     bool
	DropPolicy   DropPolicy
}

// Stats returns a snapshot of the buffer's current state.
func (bo *BoundedOverflow[T]) Stats() BoundedOverflowStats {
	bo.mu.Lock()
	stats := BoundedOverflowStats{
		Length:       bo.count,
		Capacity:     bo.maxSize,
		DroppedCount: bo.droppedCount.Load(),
		IsFull:       bo.count >= bo.maxSize,
		IsClosed:     bo.closed.Load(),
		DropPolicy:   bo.dropPolicy,
	}
	bo.mu.Unlock()
	return stats
}

// Clear removes all items from the buffer without returning them.
func (bo *BoundedOverflow[T]) Clear() {
	bo.mu.Lock()
	defer bo.mu.Unlock()

	var zero T
	for i := 0; i < bo.count; i++ {
		idx := (bo.head + i) % bo.maxSize
		bo.items[idx] = zero
	}

	bo.head = 0
	bo.tail = 0
	bo.count = 0

	bo.cond.Broadcast()
}
