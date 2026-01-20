package concurrency

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// Basic Construction Tests
// ============================================================================

func TestNewBoundedOverflow(t *testing.T) {
	tests := []struct {
		name       string
		maxSize    int
		dropPolicy DropPolicy
		wantSize   int
	}{
		{"positive size", 10, DropOldest, 10},
		{"size of 1", 1, DropNewest, 1},
		{"zero size defaults to 1", 0, Block, 1},
		{"negative size defaults to 1", -5, DropOldest, 1},
		{"large size", 10000, DropNewest, 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bo := NewBoundedOverflow[int](tt.maxSize, tt.dropPolicy)
			if bo.Cap() != tt.wantSize {
				t.Errorf("Cap() = %d, want %d", bo.Cap(), tt.wantSize)
			}
			if bo.DropPolicy() != tt.dropPolicy {
				t.Errorf("DropPolicy() = %v, want %v", bo.DropPolicy(), tt.dropPolicy)
			}
			if bo.Len() != 0 {
				t.Errorf("Len() = %d, want 0", bo.Len())
			}
			if bo.DroppedCount() != 0 {
				t.Errorf("DroppedCount() = %d, want 0", bo.DroppedCount())
			}
		})
	}
}

func TestDropPolicyString(t *testing.T) {
	tests := []struct {
		policy DropPolicy
		want   string
	}{
		{DropOldest, "DropOldest"},
		{DropNewest, "DropNewest"},
		{Block, "Block"},
		{DropPolicy(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.policy.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ============================================================================
// Normal Operation Under Limit Tests
// ============================================================================

func TestBoundedOverflow_AddUnderLimit(t *testing.T) {
	policies := []DropPolicy{DropOldest, DropNewest, Block}

	for _, policy := range policies {
		t.Run(policy.String(), func(t *testing.T) {
			bo := NewBoundedOverflow[int](5, policy)

			// Add items under the limit
			for i := 0; i < 5; i++ {
				if !bo.Add(i) {
					t.Errorf("Add(%d) returned false, want true", i)
				}
			}

			if bo.Len() != 5 {
				t.Errorf("Len() = %d, want 5", bo.Len())
			}
			if bo.DroppedCount() != 0 {
				t.Errorf("DroppedCount() = %d, want 0", bo.DroppedCount())
			}
			if !bo.IsFull() {
				t.Error("IsFull() = false, want true")
			}
		})
	}
}

func TestBoundedOverflow_TakeItems(t *testing.T) {
	bo := NewBoundedOverflow[string](3, DropOldest)

	bo.Add("a")
	bo.Add("b")
	bo.Add("c")

	// Take items in FIFO order
	for _, expected := range []string{"a", "b", "c"} {
		item, ok := bo.Take()
		if !ok {
			t.Error("Take() returned false, want true")
		}
		if item != expected {
			t.Errorf("Take() = %q, want %q", item, expected)
		}
	}

	// Buffer should be empty now
	_, ok := bo.Take()
	if ok {
		t.Error("Take() on empty buffer returned true, want false")
	}
}

func TestBoundedOverflow_DrainItems(t *testing.T) {
	bo := NewBoundedOverflow[int](5, DropNewest)

	expected := []int{10, 20, 30}
	for _, v := range expected {
		bo.Add(v)
	}

	items := bo.Drain()
	if len(items) != len(expected) {
		t.Errorf("Drain() returned %d items, want %d", len(items), len(expected))
	}

	for i, v := range items {
		if v != expected[i] {
			t.Errorf("Drain()[%d] = %d, want %d", i, v, expected[i])
		}
	}

	// Buffer should be empty after drain
	if bo.Len() != 0 {
		t.Errorf("Len() after Drain() = %d, want 0", bo.Len())
	}
}

func TestBoundedOverflow_Peek(t *testing.T) {
	bo := NewBoundedOverflow[int](3, DropOldest)

	// Peek on empty buffer
	_, ok := bo.Peek()
	if ok {
		t.Error("Peek() on empty buffer returned true, want false")
	}

	bo.Add(100)
	bo.Add(200)

	// Peek should return oldest without removing
	item, ok := bo.Peek()
	if !ok {
		t.Error("Peek() returned false, want true")
	}
	if item != 100 {
		t.Errorf("Peek() = %d, want 100", item)
	}

	// Length should be unchanged
	if bo.Len() != 2 {
		t.Errorf("Len() after Peek() = %d, want 2", bo.Len())
	}
}

// ============================================================================
// Overflow Behavior Tests - DropOldest
// ============================================================================

func TestBoundedOverflow_DropOldest(t *testing.T) {
	bo := NewBoundedOverflow[int](3, DropOldest)

	// Fill the buffer
	bo.Add(1)
	bo.Add(2)
	bo.Add(3)

	// Add more items - oldest should be dropped
	if !bo.Add(4) {
		t.Error("Add(4) with DropOldest returned false, want true")
	}
	if !bo.Add(5) {
		t.Error("Add(5) with DropOldest returned false, want true")
	}

	if bo.DroppedCount() != 2 {
		t.Errorf("DroppedCount() = %d, want 2", bo.DroppedCount())
	}

	// Buffer should contain [3, 4, 5]
	items := bo.Drain()
	expected := []int{3, 4, 5}
	if len(items) != len(expected) {
		t.Errorf("Drain() returned %d items, want %d", len(items), len(expected))
	}
	for i, v := range items {
		if v != expected[i] {
			t.Errorf("items[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

func TestBoundedOverflow_DropOldest_WrapAround(t *testing.T) {
	bo := NewBoundedOverflow[int](3, DropOldest)

	// Fill, drain partially, fill again to test wrap-around
	bo.Add(1)
	bo.Add(2)
	bo.Add(3)

	bo.Take() // Remove 1
	bo.Take() // Remove 2

	// Now head is at index 2, tail at index 2, count = 1
	bo.Add(4)
	bo.Add(5)
	bo.Add(6)

	// Should have [4, 5, 6] and 1 dropped (the 3)
	if bo.DroppedCount() != 1 {
		t.Errorf("DroppedCount() = %d, want 1", bo.DroppedCount())
	}

	items := bo.Drain()
	expected := []int{4, 5, 6}
	for i, v := range items {
		if v != expected[i] {
			t.Errorf("items[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

// ============================================================================
// Overflow Behavior Tests - DropNewest
// ============================================================================

func TestBoundedOverflow_DropNewest(t *testing.T) {
	bo := NewBoundedOverflow[int](3, DropNewest)

	// Fill the buffer
	bo.Add(1)
	bo.Add(2)
	bo.Add(3)

	// Try to add more - new items should be dropped
	if bo.Add(4) {
		t.Error("Add(4) with DropNewest returned true, want false")
	}
	if bo.Add(5) {
		t.Error("Add(5) with DropNewest returned true, want false")
	}

	if bo.DroppedCount() != 2 {
		t.Errorf("DroppedCount() = %d, want 2", bo.DroppedCount())
	}

	// Buffer should still contain [1, 2, 3]
	items := bo.Drain()
	expected := []int{1, 2, 3}
	for i, v := range items {
		if v != expected[i] {
			t.Errorf("items[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

// ============================================================================
// Overflow Behavior Tests - Block
// ============================================================================

func TestBoundedOverflow_Block(t *testing.T) {
	bo := NewBoundedOverflow[int](2, Block)

	// Fill the buffer
	bo.Add(1)
	bo.Add(2)

	// Start a goroutine that will block on Add
	done := make(chan bool)
	go func() {
		bo.Add(3) // This should block
		done <- true
	}()

	// Give the goroutine time to block
	time.Sleep(50 * time.Millisecond)

	// The goroutine should still be blocked
	select {
	case <-done:
		t.Error("Add should have blocked but didn't")
	default:
		// Expected - goroutine is blocked
	}

	// Take an item to unblock
	item, _ := bo.Take()
	if item != 1 {
		t.Errorf("Take() = %d, want 1", item)
	}

	// Now the blocked Add should complete
	select {
	case <-done:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Add didn't unblock after Take()")
	}

	// No items should be dropped with Block policy
	if bo.DroppedCount() != 0 {
		t.Errorf("DroppedCount() = %d, want 0", bo.DroppedCount())
	}
}

func TestBoundedOverflow_Block_CloseUnblocks(t *testing.T) {
	bo := NewBoundedOverflow[int](1, Block)
	bo.Add(1) // Fill the buffer

	// Start a goroutine that will block
	result := make(chan bool)
	go func() {
		ok := bo.Add(2)
		result <- ok
	}()

	// Give time to block
	time.Sleep(50 * time.Millisecond)

	// Close the buffer
	bo.Close()

	// The Add should return false
	select {
	case ok := <-result:
		if ok {
			t.Error("Add after Close returned true, want false")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Add didn't unblock after Close()")
	}
}

// ============================================================================
// TryAdd Tests
// ============================================================================

func TestBoundedOverflow_TryAdd(t *testing.T) {
	t.Run("DropOldest", func(t *testing.T) {
		bo := NewBoundedOverflow[int](2, DropOldest)
		bo.Add(1)
		bo.Add(2)

		if !bo.TryAdd(3) {
			t.Error("TryAdd with DropOldest returned false, want true")
		}
		if bo.DroppedCount() != 1 {
			t.Errorf("DroppedCount() = %d, want 1", bo.DroppedCount())
		}
	})

	t.Run("DropNewest", func(t *testing.T) {
		bo := NewBoundedOverflow[int](2, DropNewest)
		bo.Add(1)
		bo.Add(2)

		if bo.TryAdd(3) {
			t.Error("TryAdd with DropNewest returned true, want false")
		}
		if bo.DroppedCount() != 1 {
			t.Errorf("DroppedCount() = %d, want 1", bo.DroppedCount())
		}
	})

	t.Run("Block", func(t *testing.T) {
		bo := NewBoundedOverflow[int](2, Block)
		bo.Add(1)
		bo.Add(2)

		if bo.TryAdd(3) {
			t.Error("TryAdd with Block policy returned true, want false")
		}
		// TryAdd with Block doesn't count as dropped since it's non-blocking variant
		if bo.DroppedCount() != 0 {
			t.Errorf("DroppedCount() = %d, want 0", bo.DroppedCount())
		}
	})
}

// ============================================================================
// Stats and Metrics Tests
// ============================================================================

func TestBoundedOverflow_Stats(t *testing.T) {
	bo := NewBoundedOverflow[int](5, DropOldest)

	bo.Add(1)
	bo.Add(2)
	bo.Add(3)

	stats := bo.Stats()
	if stats.Length != 3 {
		t.Errorf("stats.Length = %d, want 3", stats.Length)
	}
	if stats.Capacity != 5 {
		t.Errorf("stats.Capacity = %d, want 5", stats.Capacity)
	}
	if stats.DroppedCount != 0 {
		t.Errorf("stats.DroppedCount = %d, want 0", stats.DroppedCount)
	}
	if stats.IsFull {
		t.Error("stats.IsFull = true, want false")
	}
	if stats.IsClosed {
		t.Error("stats.IsClosed = true, want false")
	}
	if stats.DropPolicy != DropOldest {
		t.Errorf("stats.DropPolicy = %v, want DropOldest", stats.DropPolicy)
	}
}

func TestBoundedOverflow_ResetDroppedCount(t *testing.T) {
	bo := NewBoundedOverflow[int](2, DropNewest)

	// Fill and overflow
	bo.Add(1)
	bo.Add(2)
	bo.Add(3) // Dropped
	bo.Add(4) // Dropped

	if bo.DroppedCount() != 2 {
		t.Errorf("DroppedCount() before reset = %d, want 2", bo.DroppedCount())
	}

	prev := bo.ResetDroppedCount()
	if prev != 2 {
		t.Errorf("ResetDroppedCount() returned %d, want 2", prev)
	}

	if bo.DroppedCount() != 0 {
		t.Errorf("DroppedCount() after reset = %d, want 0", bo.DroppedCount())
	}
}

// ============================================================================
// Close and Clear Tests
// ============================================================================

func TestBoundedOverflow_Close(t *testing.T) {
	bo := NewBoundedOverflow[int](3, DropOldest)
	bo.Add(1)
	bo.Add(2)

	bo.Close()

	if !bo.IsClosed() {
		t.Error("IsClosed() = false after Close()")
	}

	// Add should fail after close
	if bo.Add(3) {
		t.Error("Add after Close returned true, want false")
	}

	// But we can still drain
	items := bo.Drain()
	if len(items) != 2 {
		t.Errorf("Drain() after Close returned %d items, want 2", len(items))
	}
}

func TestBoundedOverflow_Clear(t *testing.T) {
	bo := NewBoundedOverflow[int](5, DropNewest)

	bo.Add(1)
	bo.Add(2)
	bo.Add(3)

	bo.Clear()

	if bo.Len() != 0 {
		t.Errorf("Len() after Clear() = %d, want 0", bo.Len())
	}
	if !bo.IsEmpty() {
		t.Error("IsEmpty() = false after Clear()")
	}
}

// ============================================================================
// Concurrent Access Stress Tests
// ============================================================================

func TestBoundedOverflow_ConcurrentAdd(t *testing.T) {
	policies := []DropPolicy{DropOldest, DropNewest}

	for _, policy := range policies {
		t.Run(policy.String(), func(t *testing.T) {
			bo := NewBoundedOverflow[int](100, policy)
			var wg sync.WaitGroup
			numGoroutines := 10
			itemsPerGoroutine := 1000

			wg.Add(numGoroutines)
			for g := 0; g < numGoroutines; g++ {
				go func(gid int) {
					defer wg.Done()
					for i := 0; i < itemsPerGoroutine; i++ {
						bo.Add(gid*itemsPerGoroutine + i)
					}
				}(g)
			}

			wg.Wait()

			totalSent := numGoroutines * itemsPerGoroutine
			inBuffer := bo.Len()
			dropped := bo.DroppedCount()

			// With bounded buffer, items in buffer + dropped should equal total sent
			if int64(inBuffer)+dropped != int64(totalSent) {
				t.Errorf("inBuffer(%d) + dropped(%d) = %d, want %d",
					inBuffer, dropped, int64(inBuffer)+dropped, totalSent)
			}

			// Buffer should not exceed capacity
			if inBuffer > 100 {
				t.Errorf("buffer length %d exceeds capacity 100", inBuffer)
			}
		})
	}
}

func TestBoundedOverflow_ConcurrentAddAndTake(t *testing.T) {
	bo := NewBoundedOverflow[int](50, DropOldest)
	var wg sync.WaitGroup
	var added atomic.Int64
	var taken atomic.Int64

	// Producers
	numProducers := 5
	itemsPerProducer := 500

	wg.Add(numProducers)
	for p := 0; p < numProducers; p++ {
		go func() {
			defer wg.Done()
			for i := 0; i < itemsPerProducer; i++ {
				if bo.Add(i) {
					added.Add(1)
				}
			}
		}()
	}

	// Consumers
	numConsumers := 3
	stopConsumers := make(chan struct{})

	wg.Add(numConsumers)
	for c := 0; c < numConsumers; c++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopConsumers:
					// Drain remaining items
					for {
						_, ok := bo.Take()
						if !ok {
							return
						}
						taken.Add(1)
					}
				default:
					if _, ok := bo.Take(); ok {
						taken.Add(1)
					}
				}
			}
		}()
	}

	// Let producers finish
	time.Sleep(100 * time.Millisecond)
	close(stopConsumers)

	wg.Wait()

	totalSent := int64(numProducers * itemsPerProducer)
	dropped := bo.DroppedCount()
	remaining := int64(bo.Len())

	// Verify: added always equals totalSent for DropOldest
	if added.Load() != totalSent {
		t.Errorf("added = %d, want %d", added.Load(), totalSent)
	}

	// taken + remaining + dropped should equal totalSent
	accounted := taken.Load() + remaining + dropped
	if accounted != totalSent {
		t.Errorf("taken(%d) + remaining(%d) + dropped(%d) = %d, want %d",
			taken.Load(), remaining, dropped, accounted, totalSent)
	}
}

func TestBoundedOverflow_ConcurrentBlock(t *testing.T) {
	bo := NewBoundedOverflow[int](10, Block)
	var wg sync.WaitGroup
	var added atomic.Int64
	var taken atomic.Int64

	// Start consumers first
	numConsumers := 2
	wg.Add(numConsumers)
	for c := 0; c < numConsumers; c++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, ok := bo.Take(); ok {
					taken.Add(1)
				}
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Start producers
	numProducers := 3
	itemsPerProducer := 20
	wg.Add(numProducers)
	for p := 0; p < numProducers; p++ {
		go func() {
			defer wg.Done()
			for i := 0; i < itemsPerProducer; i++ {
				if bo.Add(i) {
					added.Add(1)
				}
			}
		}()
	}

	// Give time for operations
	time.Sleep(200 * time.Millisecond)

	// Close to unblock any waiting producers
	bo.Close()

	wg.Wait()

	// No drops should occur with Block policy
	if bo.DroppedCount() != 0 {
		t.Errorf("DroppedCount() = %d, want 0 for Block policy", bo.DroppedCount())
	}
}

func TestBoundedOverflow_ConcurrentDrain(t *testing.T) {
	bo := NewBoundedOverflow[int](100, DropNewest)
	var wg sync.WaitGroup
	var totalDrained atomic.Int64

	// Fill the buffer
	for i := 0; i < 100; i++ {
		bo.Add(i)
	}

	// Multiple concurrent drains
	numDrainers := 5
	wg.Add(numDrainers)
	for d := 0; d < numDrainers; d++ {
		go func() {
			defer wg.Done()
			items := bo.Drain()
			totalDrained.Add(int64(len(items)))
		}()
	}

	wg.Wait()

	// Only one drain should get the items
	if totalDrained.Load() != 100 {
		t.Errorf("totalDrained = %d, want 100", totalDrained.Load())
	}

	if bo.Len() != 0 {
		t.Errorf("Len() after concurrent drains = %d, want 0", bo.Len())
	}
}

// ============================================================================
// Edge Cases and Boundary Tests
// ============================================================================

func TestBoundedOverflow_EmptyDrain(t *testing.T) {
	bo := NewBoundedOverflow[int](5, DropOldest)

	items := bo.Drain()
	if items != nil {
		t.Errorf("Drain() on empty buffer = %v, want nil", items)
	}
}

func TestBoundedOverflow_SingleCapacity(t *testing.T) {
	bo := NewBoundedOverflow[string](1, DropOldest)

	bo.Add("first")
	if !bo.IsFull() {
		t.Error("IsFull() = false, want true")
	}

	bo.Add("second") // Should drop "first"

	item, ok := bo.Take()
	if !ok || item != "second" {
		t.Errorf("Take() = (%q, %v), want (\"second\", true)", item, ok)
	}

	if bo.DroppedCount() != 1 {
		t.Errorf("DroppedCount() = %d, want 1", bo.DroppedCount())
	}
}

func TestBoundedOverflow_AddToClosedBuffer(t *testing.T) {
	bo := NewBoundedOverflow[int](5, DropOldest)
	bo.Close()

	if bo.Add(1) {
		t.Error("Add to closed buffer returned true")
	}
	if bo.TryAdd(1) {
		t.Error("TryAdd to closed buffer returned true")
	}
}

func TestBoundedOverflow_RingBufferWrapAround(t *testing.T) {
	bo := NewBoundedOverflow[int](3, DropNewest)

	// Add 3 items
	bo.Add(1)
	bo.Add(2)
	bo.Add(3)

	// Take 2, add 2 more
	bo.Take() // Takes 1
	bo.Take() // Takes 2

	bo.Add(4)
	bo.Add(5)

	// Now should have [3, 4, 5] with head wrapping around
	items := bo.Drain()
	expected := []int{3, 4, 5}

	if len(items) != len(expected) {
		t.Fatalf("len(items) = %d, want %d", len(items), len(expected))
	}

	for i, v := range items {
		if v != expected[i] {
			t.Errorf("items[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

// ============================================================================
// Metrics Accuracy Tests
// ============================================================================

func TestBoundedOverflow_MetricsAccuracy(t *testing.T) {
	bo := NewBoundedOverflow[int](10, DropNewest)

	// Add 15 items (10 will fit, 5 will be dropped)
	for i := 0; i < 15; i++ {
		bo.Add(i)
	}

	if bo.Len() != 10 {
		t.Errorf("Len() = %d, want 10", bo.Len())
	}
	if bo.DroppedCount() != 5 {
		t.Errorf("DroppedCount() = %d, want 5", bo.DroppedCount())
	}

	// Take 5 items
	for i := 0; i < 5; i++ {
		bo.Take()
	}

	if bo.Len() != 5 {
		t.Errorf("Len() after taking 5 = %d, want 5", bo.Len())
	}

	// Drop count should not change
	if bo.DroppedCount() != 5 {
		t.Errorf("DroppedCount() after taking = %d, want 5", bo.DroppedCount())
	}
}

func TestBoundedOverflow_DroppedCountWithDropOldest(t *testing.T) {
	bo := NewBoundedOverflow[int](5, DropOldest)

	// Add 100 items
	for i := 0; i < 100; i++ {
		bo.Add(i)
	}

	// First 5 fit, then 95 more cause 95 drops
	if bo.DroppedCount() != 95 {
		t.Errorf("DroppedCount() = %d, want 95", bo.DroppedCount())
	}

	// Buffer should contain last 5 items
	items := bo.Drain()
	expected := []int{95, 96, 97, 98, 99}
	for i, v := range items {
		if v != expected[i] {
			t.Errorf("items[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

// ============================================================================
// Type Safety Tests
// ============================================================================

func TestBoundedOverflow_PointerType(t *testing.T) {
	type Data struct {
		Value int
	}

	bo := NewBoundedOverflow[*Data](3, DropOldest)

	d1 := &Data{Value: 1}
	d2 := &Data{Value: 2}
	d3 := &Data{Value: 3}

	bo.Add(d1)
	bo.Add(d2)
	bo.Add(d3)

	item, ok := bo.Take()
	if !ok || item.Value != 1 {
		t.Errorf("Take() = (%v, %v), want (*Data{1}, true)", item, ok)
	}
}

func TestBoundedOverflow_StructType(t *testing.T) {
	type Message struct {
		ID      int
		Content string
	}

	bo := NewBoundedOverflow[Message](2, DropNewest)

	bo.Add(Message{ID: 1, Content: "hello"})
	bo.Add(Message{ID: 2, Content: "world"})
	bo.Add(Message{ID: 3, Content: "dropped"}) // Should be dropped

	items := bo.Drain()
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].ID != 1 || items[1].ID != 2 {
		t.Errorf("items = %v, want [{1, hello}, {2, world}]", items)
	}
}

// ============================================================================
// Benchmark Tests
// ============================================================================

func BenchmarkBoundedOverflow_Add_DropOldest(b *testing.B) {
	bo := NewBoundedOverflow[int](1000, DropOldest)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		bo.Add(i)
	}
}

func BenchmarkBoundedOverflow_Add_DropNewest(b *testing.B) {
	bo := NewBoundedOverflow[int](1000, DropNewest)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		bo.Add(i)
	}
}

func BenchmarkBoundedOverflow_AddTake_Parallel(b *testing.B) {
	bo := NewBoundedOverflow[int](1000, DropOldest)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				bo.Add(i)
			} else {
				bo.Take()
			}
			i++
		}
	})
}

func BenchmarkBoundedOverflow_Drain(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bo := NewBoundedOverflow[int](100, DropOldest)
		for j := 0; j < 100; j++ {
			bo.Add(j)
		}
		b.StartTimer()

		bo.Drain()
	}
}
