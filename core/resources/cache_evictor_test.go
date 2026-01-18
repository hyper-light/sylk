package resources

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockCache struct {
	name    string
	size    int64
	evicted float64
	mu      sync.Mutex
}

func (m *mockCache) Name() string {
	return m.name
}

func (m *mockCache) Size() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.size
}

func (m *mockCache) EvictPercent(percent float64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.evicted += percent
	freed := int64(float64(m.size) * percent)
	m.size -= freed
	return freed
}

func (m *mockCache) getEvicted() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.evicted
}

func TestCacheEvictor_NewEmpty(t *testing.T) {
	ce := NewCacheEvictor()

	assert.Equal(t, 0, ce.CacheCount())
	assert.Equal(t, int64(0), ce.TotalSize())
}

func TestCacheEvictor_Register(t *testing.T) {
	ce := NewCacheEvictor()

	cache1 := &mockCache{name: "cache1", size: 1000}
	cache2 := &mockCache{name: "cache2", size: 2000}

	ce.Register(cache1)
	ce.Register(cache2)

	assert.Equal(t, 2, ce.CacheCount())
	assert.Equal(t, int64(3000), ce.TotalSize())
}

func TestCacheEvictor_NewWithCaches(t *testing.T) {
	cache1 := &mockCache{name: "cache1", size: 1000}
	cache2 := &mockCache{name: "cache2", size: 2000}

	ce := NewCacheEvictor(cache1, cache2)

	assert.Equal(t, 2, ce.CacheCount())
	assert.Equal(t, int64(3000), ce.TotalSize())
}

func TestCacheEvictor_Evict(t *testing.T) {
	cache1 := &mockCache{name: "cache1", size: 1000}
	cache2 := &mockCache{name: "cache2", size: 2000}

	ce := NewCacheEvictor(cache1, cache2)

	freed := ce.Evict(0.25)

	assert.Equal(t, int64(750), freed)
	assert.InDelta(t, 0.25, cache1.getEvicted(), 0.01)
	assert.InDelta(t, 0.25, cache2.getEvicted(), 0.01)
}

func TestCacheEvictor_EvictEmpty(t *testing.T) {
	ce := NewCacheEvictor()

	freed := ce.Evict(0.25)

	assert.Equal(t, int64(0), freed)
}

func TestCacheEvictor_EvictBytes(t *testing.T) {
	cache1 := &mockCache{name: "small", size: 100}
	cache2 := &mockCache{name: "medium", size: 500}
	cache3 := &mockCache{name: "large", size: 1000}

	ce := NewCacheEvictor(cache1, cache2, cache3)

	freed := ce.EvictBytes(300)

	assert.GreaterOrEqual(t, freed, int64(300))
	assert.Greater(t, cache3.getEvicted(), 0.0, "Largest cache should be evicted first")
}

func TestCacheEvictor_EvictBytes_StopsWhenTargetMet(t *testing.T) {
	cache1 := &mockCache{name: "cache1", size: 10000}
	cache2 := &mockCache{name: "cache2", size: 10000}

	ce := NewCacheEvictor(cache1, cache2)

	freed := ce.EvictBytes(2000)

	assert.GreaterOrEqual(t, freed, int64(2000))
}

func TestCacheEvictor_EvictBytes_LargestFirst(t *testing.T) {
	small := &mockCache{name: "small", size: 100}
	large := &mockCache{name: "large", size: 10000}

	ce := NewCacheEvictor(small, large)

	ce.EvictBytes(200)

	assert.Greater(t, large.getEvicted(), 0.0, "Large cache should be evicted")
}

func TestCacheEvictor_EvictBytes_Empty(t *testing.T) {
	ce := NewCacheEvictor()

	freed := ce.EvictBytes(1000)

	assert.Equal(t, int64(0), freed)
}

func TestCacheEvictor_TotalSize(t *testing.T) {
	ce := NewCacheEvictor(
		&mockCache{name: "a", size: 100},
		&mockCache{name: "b", size: 200},
		&mockCache{name: "c", size: 300},
	)

	assert.Equal(t, int64(600), ce.TotalSize())
}

func TestCacheEvictor_ConcurrentAccess(t *testing.T) {
	ce := NewCacheEvictor()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cache := &mockCache{name: "cache", size: int64(idx * 100)}
			ce.Register(cache)
		}(i)
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ce.TotalSize()
			_ = ce.CacheCount()
			_ = ce.Evict(0.1)
		}()
	}

	wg.Wait()

	assert.Equal(t, 10, ce.CacheCount())
}

func TestCacheEvictor_EvictZeroPercent(t *testing.T) {
	cache := &mockCache{name: "cache", size: 1000}
	ce := NewCacheEvictor(cache)

	freed := ce.Evict(0)

	assert.Equal(t, int64(0), freed)
	assert.Equal(t, int64(1000), cache.Size())
}

func TestCacheEvictor_EvictFullPercent(t *testing.T) {
	cache := &mockCache{name: "cache", size: 1000}
	ce := NewCacheEvictor(cache)

	freed := ce.Evict(1.0)

	assert.Equal(t, int64(1000), freed)
	assert.Equal(t, int64(0), cache.Size())
}
