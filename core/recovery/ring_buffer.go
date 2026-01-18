package recovery

import "sync"

// RingBuffer is a thread-safe circular buffer with a fixed capacity.
// When full, new items overwrite the oldest items.
type RingBuffer[T any] struct {
	items    []T
	head     int
	tail     int
	count    int
	capacity int
	mu       sync.RWMutex
}

// NewRingBuffer creates a new ring buffer with the given capacity.
func NewRingBuffer[T any](capacity int) *RingBuffer[T] {
	if capacity <= 0 {
		capacity = 100
	}
	return &RingBuffer[T]{
		items:    make([]T, capacity),
		capacity: capacity,
	}
}

// Push adds an item to the buffer.
// If full, the oldest item is overwritten.
func (rb *RingBuffer[T]) Push(item T) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.items[rb.head] = item
	rb.head = (rb.head + 1) % rb.capacity

	if rb.count < rb.capacity {
		rb.count++
	} else {
		rb.tail = (rb.tail + 1) % rb.capacity
	}
}

// Last returns the last n items in chronological order (oldest to newest).
// If n > count, returns all items.
func (rb *RingBuffer[T]) Last(n int) []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	return rb.lastUnlocked(n)
}

func (rb *RingBuffer[T]) lastUnlocked(n int) []T {
	if n <= 0 || rb.count == 0 {
		return nil
	}

	n = min(n, rb.count)
	return rb.copyFromTail(n)
}

func (rb *RingBuffer[T]) copyFromTail(n int) []T {
	result := make([]T, n)
	start := (rb.head - n + rb.capacity) % rb.capacity

	for i := 0; i < n; i++ {
		idx := (start + i) % rb.capacity
		result[i] = rb.items[idx]
	}
	return result
}

// Len returns the current number of items in the buffer.
func (rb *RingBuffer[T]) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}

// Cap returns the capacity of the buffer.
func (rb *RingBuffer[T]) Cap() int {
	return rb.capacity
}

// Clear removes all items from the buffer.
func (rb *RingBuffer[T]) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	var zero T
	for i := range rb.items {
		rb.items[i] = zero
	}
	rb.head = 0
	rb.tail = 0
	rb.count = 0
}

// All returns all items in chronological order (oldest to newest).
func (rb *RingBuffer[T]) All() []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.lastUnlocked(rb.count)
}
