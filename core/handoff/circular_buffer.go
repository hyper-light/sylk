package handoff

import (
	"encoding/json"
	"sync"
)

// =============================================================================
// HO.6.3 CircularBuffer[T] - Generic Circular Buffer with FIFO Eviction
// =============================================================================
//
// CircularBuffer provides a fixed-capacity, thread-safe circular buffer
// implementation with O(1) push, pop, and peek operations. When the buffer
// is full, new items evict the oldest item (FIFO eviction).
//
// The buffer is designed for memory-efficient storage of recent items
// without unbounded growth, making it ideal for maintaining recent
// conversation messages, tool states, or other rolling windows.
//
// Thread Safety:
//   - All operations are protected by a read-write mutex
//   - Safe for concurrent use from multiple goroutines
//
// Example usage:
//
//	buf := NewCircularBuffer[string](5)
//	buf.Push("a")
//	buf.Push("b")
//	buf.Push("c")
//	items := buf.Items()  // ["a", "b", "c"]
//	oldest := buf.Pop()   // "a"
//	newest := buf.Peek()  // "c"

// CircularBuffer is a generic fixed-capacity circular buffer with FIFO eviction.
// The type parameter T can be any type.
type CircularBuffer[T any] struct {
	mu       sync.RWMutex
	items    []T    // Underlying storage
	head     int    // Index of the oldest item (next to pop)
	tail     int    // Index where next item will be inserted
	count    int    // Current number of items
	capacity int    // Maximum capacity
}

// NewCircularBuffer creates a new CircularBuffer with the specified capacity.
// Capacity must be at least 1; if less, it defaults to 1.
func NewCircularBuffer[T any](capacity int) *CircularBuffer[T] {
	if capacity < 1 {
		capacity = 1
	}

	return &CircularBuffer[T]{
		items:    make([]T, capacity),
		head:     0,
		tail:     0,
		count:    0,
		capacity: capacity,
	}
}

// Push adds an item to the buffer. If the buffer is full, the oldest item
// is evicted (FIFO). Returns true if an item was evicted, false otherwise.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) Push(item T) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.pushLocked(item)
}

// pushLocked is the internal implementation of Push.
// Caller must hold cb.mu write lock.
func (cb *CircularBuffer[T]) pushLocked(item T) bool {
	evicted := false

	// If buffer is full, advance head (evict oldest)
	if cb.count == cb.capacity {
		cb.head = (cb.head + 1) % cb.capacity
		evicted = true
	} else {
		cb.count++
	}

	// Insert at tail and advance tail
	cb.items[cb.tail] = item
	cb.tail = (cb.tail + 1) % cb.capacity

	return evicted
}

// Pop removes and returns the oldest item from the buffer.
// Returns the zero value for T and false if the buffer is empty.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) Pop() (T, bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	var zero T
	if cb.count == 0 {
		return zero, false
	}

	item := cb.items[cb.head]
	cb.items[cb.head] = zero // Clear for GC
	cb.head = (cb.head + 1) % cb.capacity
	cb.count--

	return item, true
}

// Peek returns the newest item without removing it.
// Returns the zero value for T and false if the buffer is empty.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) Peek() (T, bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var zero T
	if cb.count == 0 {
		return zero, false
	}

	// Newest item is at (tail - 1 + capacity) % capacity
	newestIdx := (cb.tail - 1 + cb.capacity) % cb.capacity
	return cb.items[newestIdx], true
}

// PeekOldest returns the oldest item without removing it.
// Returns the zero value for T and false if the buffer is empty.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) PeekOldest() (T, bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var zero T
	if cb.count == 0 {
		return zero, false
	}

	return cb.items[cb.head], true
}

// At returns the item at the given index (0 = oldest, count-1 = newest).
// Returns the zero value for T and false if the index is out of bounds.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) At(index int) (T, bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var zero T
	if index < 0 || index >= cb.count {
		return zero, false
	}

	actualIdx := (cb.head + index) % cb.capacity
	return cb.items[actualIdx], true
}

// Items returns all items in order from oldest to newest.
// Returns a new slice that can be safely modified by the caller.
// Time complexity: O(n) where n is the number of items
func (cb *CircularBuffer[T]) Items() []T {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.itemsLocked()
}

// itemsLocked is the internal implementation of Items.
// Caller must hold cb.mu (read or write lock).
func (cb *CircularBuffer[T]) itemsLocked() []T {
	result := make([]T, cb.count)
	for i := 0; i < cb.count; i++ {
		idx := (cb.head + i) % cb.capacity
		result[i] = cb.items[idx]
	}
	return result
}

// RecentN returns up to n most recent items, from oldest to newest.
// If n > count, returns all items.
// Time complexity: O(min(n, count))
func (cb *CircularBuffer[T]) RecentN(n int) []T {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if n <= 0 {
		return []T{}
	}

	if n > cb.count {
		n = cb.count
	}

	result := make([]T, n)
	startIdx := cb.count - n
	for i := 0; i < n; i++ {
		idx := (cb.head + startIdx + i) % cb.capacity
		result[i] = cb.items[idx]
	}
	return result
}

// Len returns the current number of items in the buffer.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) Len() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.lenLocked()
}

// lenLocked is the internal implementation of Len.
// Caller must hold cb.mu (read or write lock).
func (cb *CircularBuffer[T]) lenLocked() int {
	return cb.count
}

// Cap returns the maximum capacity of the buffer.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) Cap() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.capacity
}

// IsEmpty returns true if the buffer contains no items.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) IsEmpty() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.count == 0
}

// IsFull returns true if the buffer is at capacity.
// Time complexity: O(1)
func (cb *CircularBuffer[T]) IsFull() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.count == cb.capacity
}

// Clear removes all items from the buffer.
// Time complexity: O(n) where n is the capacity (to clear for GC)
func (cb *CircularBuffer[T]) Clear() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.clearLocked()
}

// clearLocked is the internal implementation of Clear.
// Caller must hold cb.mu write lock.
func (cb *CircularBuffer[T]) clearLocked() {
	var zero T
	for i := 0; i < cb.capacity; i++ {
		cb.items[i] = zero
	}
	cb.head = 0
	cb.tail = 0
	cb.count = 0
}

// ForEach applies the given function to each item in order (oldest to newest).
// If the function returns false, iteration stops early.
// Time complexity: O(n) where n is the number of items
func (cb *CircularBuffer[T]) ForEach(fn func(item T, index int) bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	for i := 0; i < cb.count; i++ {
		idx := (cb.head + i) % cb.capacity
		if !fn(cb.items[idx], i) {
			break
		}
	}
}

// Filter returns a new slice containing only items that satisfy the predicate.
// Items are in order from oldest to newest.
// Time complexity: O(n) where n is the number of items
func (cb *CircularBuffer[T]) Filter(predicate func(item T) bool) []T {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var result []T
	for i := 0; i < cb.count; i++ {
		idx := (cb.head + i) % cb.capacity
		if predicate(cb.items[idx]) {
			result = append(result, cb.items[idx])
		}
	}
	return result
}

// =============================================================================
// JSON Serialization
// =============================================================================

// circularBufferJSON is the JSON representation of a CircularBuffer.
type circularBufferJSON[T any] struct {
	Items    []T `json:"items"`
	Capacity int `json:"capacity"`
}

// MarshalJSON implements json.Marshaler.
func (cb *CircularBuffer[T]) MarshalJSON() ([]byte, error) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return json.Marshal(circularBufferJSON[T]{
		Items:    cb.Items(),
		Capacity: cb.capacity,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (cb *CircularBuffer[T]) UnmarshalJSON(data []byte) error {
	var temp circularBufferJSON[T]
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	capacity := temp.Capacity
	if capacity < 1 {
		capacity = 1
	}
	if capacity < len(temp.Items) {
		capacity = len(temp.Items)
	}

	cb.items = make([]T, capacity)
	cb.capacity = capacity
	cb.head = 0
	cb.count = len(temp.Items)
	cb.tail = cb.count % capacity

	for i, item := range temp.Items {
		cb.items[i] = item
	}

	return nil
}

// =============================================================================
// Snapshot for Thread-Safe Access
// =============================================================================

// BufferSnapshot contains a point-in-time snapshot of the buffer state.
type BufferSnapshot[T any] struct {
	Items    []T
	Capacity int
	Count    int
}

// Snapshot returns a thread-safe snapshot of the current buffer state.
// The returned snapshot can be accessed without holding any locks.
func (cb *CircularBuffer[T]) Snapshot() BufferSnapshot[T] {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return BufferSnapshot[T]{
		Items:    cb.Items(),
		Capacity: cb.capacity,
		Count:    cb.count,
	}
}
