package guide

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestShardedMap_BasicOperations(t *testing.T) {
	m := NewStringMap[int](64)

	// Test Set and Get
	m.Set("key1", 100)
	val, ok := m.Get("key1")
	if !ok || val != 100 {
		t.Errorf("expected 100, got %d (ok=%v)", val, ok)
	}

	// Test missing key
	_, ok = m.Get("missing")
	if ok {
		t.Error("expected ok=false for missing key")
	}

	// Test Delete
	m.Delete("key1")
	_, ok = m.Get("key1")
	if ok {
		t.Error("expected key to be deleted")
	}
}

func TestShardedMap_Has(t *testing.T) {
	m := NewStringMap[string](32)

	if m.Has("key") {
		t.Error("expected Has to return false for missing key")
	}

	m.Set("key", "value")
	if !m.Has("key") {
		t.Error("expected Has to return true for existing key")
	}
}

func TestShardedMap_GetOrSet(t *testing.T) {
	m := NewStringMap[int](32)

	// First call should set
	val, existed := m.GetOrSet("key", 42)
	if existed || val != 42 {
		t.Errorf("expected (42, false), got (%d, %v)", val, existed)
	}

	// Second call should return existing
	val, existed = m.GetOrSet("key", 100)
	if !existed || val != 42 {
		t.Errorf("expected (42, true), got (%d, %v)", val, existed)
	}
}

func TestShardedMap_GetOrCreate(t *testing.T) {
	m := NewStringMap[int](32)
	factoryCalls := 0

	factory := func() int {
		factoryCalls++
		return 42
	}

	// First call should create
	val, existed := m.GetOrCreate("key", factory)
	if existed || val != 42 || factoryCalls != 1 {
		t.Errorf("expected (42, false, calls=1), got (%d, %v, calls=%d)", val, existed, factoryCalls)
	}

	// Second call should return existing without calling factory
	val, existed = m.GetOrCreate("key", factory)
	if !existed || val != 42 || factoryCalls != 1 {
		t.Errorf("expected (42, true, calls=1), got (%d, %v, calls=%d)", val, existed, factoryCalls)
	}
}

func TestShardedMap_GetOrCreate_Concurrent(t *testing.T) {
	m := NewStringMap[*int](32)
	var factoryCalls int64
	const goroutines = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			val, _ := m.GetOrCreate("same-key", func() *int {
				atomic.AddInt64(&factoryCalls, 1)
				n := 42
				return &n
			})
			if *val != 42 {
				t.Errorf("expected 42, got %d", *val)
			}
		}()
	}

	wg.Wait()

	// Factory should only be called once despite concurrent access
	if factoryCalls != 1 {
		t.Errorf("expected factory to be called once, got %d", factoryCalls)
	}
}

func TestShardedMap_Update(t *testing.T) {
	m := NewStringMap[int](32)

	// Update non-existing
	result := m.Update("counter", func(existing int, exists bool) int {
		if exists {
			return existing + 1
		}
		return 1
	})
	if result != 1 {
		t.Errorf("expected 1, got %d", result)
	}

	// Update existing
	result = m.Update("counter", func(existing int, exists bool) int {
		return existing + 1
	})
	if result != 2 {
		t.Errorf("expected 2, got %d", result)
	}
}

func TestShardedMap_Len(t *testing.T) {
	m := NewStringMap[int](32)

	if m.Len() != 0 {
		t.Error("expected empty map")
	}

	m.Set("a", 1)
	m.Set("b", 2)
	m.Set("c", 3)

	if m.Len() != 3 {
		t.Errorf("expected 3, got %d", m.Len())
	}

	m.Delete("b")
	if m.Len() != 2 {
		t.Errorf("expected 2, got %d", m.Len())
	}
}

func TestShardedMap_Range(t *testing.T) {
	m := NewStringMap[int](32)

	m.Set("a", 1)
	m.Set("b", 2)
	m.Set("c", 3)

	sum := 0
	m.Range(func(key string, val int) bool {
		sum += val
		return true
	})

	if sum != 6 {
		t.Errorf("expected sum=6, got %d", sum)
	}
}

func TestShardedMap_RangeEarlyStop(t *testing.T) {
	m := NewStringMap[int](32)

	for i := 0; i < 100; i++ {
		m.Set(string(rune('a'+i)), i)
	}

	count := 0
	m.Range(func(key string, val int) bool {
		count++
		return count < 5 // Stop after 5
	})

	if count != 5 {
		t.Errorf("expected count=5, got %d", count)
	}
}

func TestShardedMap_Snapshot(t *testing.T) {
	m := NewStringMap[int](32)

	m.Set("a", 1)
	m.Set("b", 2)

	snap := m.Snapshot()

	if len(snap) != 2 {
		t.Errorf("expected 2 items in snapshot, got %d", len(snap))
	}
	if snap["a"] != 1 || snap["b"] != 2 {
		t.Error("snapshot values don't match")
	}

	// Modifying snapshot shouldn't affect original
	snap["c"] = 3
	if m.Has("c") {
		t.Error("snapshot modification affected original map")
	}
}

func TestShardedMap_Clear(t *testing.T) {
	m := NewStringMap[int](32)

	m.Set("a", 1)
	m.Set("b", 2)

	m.Clear()

	if m.Len() != 0 {
		t.Errorf("expected empty map after clear, got %d items", m.Len())
	}
}

func TestShardedMap_Concurrent(t *testing.T) {
	m := NewStringMap[int](64)
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + (i % 26)))
			m.Set(key, i)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + (i % 26)))
			m.Get(key)
		}(i)
	}

	wg.Wait()

	// Should have at most 26 unique keys
	if m.Len() > 26 {
		t.Errorf("expected at most 26 keys, got %d", m.Len())
	}
}

func TestHashString(t *testing.T) {
	// Same string should produce same hash
	h1 := HashString("test")
	h2 := HashString("test")
	if h1 != h2 {
		t.Error("same string produced different hashes")
	}

	// Different strings should (usually) produce different hashes
	h3 := HashString("other")
	if h1 == h3 {
		t.Error("different strings produced same hash (collision)")
	}
}

func TestNextPowerOf2(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{4, 4},
		{5, 8},
		{15, 16},
		{16, 16},
		{17, 32},
		{100, 128},
	}

	for _, tc := range tests {
		result := nextPowerOf2(tc.input)
		if result != tc.expected {
			t.Errorf("nextPowerOf2(%d) = %d, expected %d", tc.input, result, tc.expected)
		}
	}
}
