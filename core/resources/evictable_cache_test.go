package resources

import (
	"sync"
	"testing"
)

type mockEvictableCache struct {
	name        string
	size        int64
	evictCalled bool
	evictAmount float64
	freed       int64
}

func (m *mockEvictableCache) Name() string {
	return m.name
}

func (m *mockEvictableCache) Size() int64 {
	return m.size
}

func (m *mockEvictableCache) EvictPercent(percent float64) int64 {
	m.evictCalled = true
	m.evictAmount = percent
	m.freed = int64(float64(m.size) * percent)
	m.size -= m.freed
	return m.freed
}

func TestEvictableCache_Interface(t *testing.T) {
	var cache EvictableCache = &mockEvictableCache{
		name: "test-cache",
		size: 1000,
	}

	if cache.Name() != "test-cache" {
		t.Errorf("Name() = %q, want %q", cache.Name(), "test-cache")
	}
	if cache.Size() != 1000 {
		t.Errorf("Size() = %d, want 1000", cache.Size())
	}

	freed := cache.EvictPercent(0.25)
	if freed != 250 {
		t.Errorf("EvictPercent(0.25) = %d, want 250", freed)
	}
	if cache.Size() != 750 {
		t.Errorf("Size() after evict = %d, want 750", cache.Size())
	}
}

func TestEvictableCache_MultipleCaches(t *testing.T) {
	caches := []EvictableCache{
		&mockEvictableCache{name: "cache-1", size: 1000},
		&mockEvictableCache{name: "cache-2", size: 2000},
		&mockEvictableCache{name: "cache-3", size: 500},
	}

	var totalFreed int64
	for _, c := range caches {
		totalFreed += c.EvictPercent(0.50)
	}

	if totalFreed != 1750 {
		t.Errorf("total freed = %d, want 1750", totalFreed)
	}
}

func TestEvictableCache_ConcurrentAccess(t *testing.T) {
	cache := &mockEvictableCache{name: "concurrent", size: 10000}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cache.Name()
			_ = cache.Size()
		}()
	}
	wg.Wait()
}
