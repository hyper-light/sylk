package resources

import (
	"sort"
	"sync"
)

type CacheEvictor struct {
	mu     sync.RWMutex
	caches []EvictableCache
}

func NewCacheEvictor(caches ...EvictableCache) *CacheEvictor {
	return &CacheEvictor{
		caches: caches,
	}
}

func (ce *CacheEvictor) Register(cache EvictableCache) {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	ce.caches = append(ce.caches, cache)
}

func (ce *CacheEvictor) Evict(percent float64) int64 {
	ce.mu.RLock()
	caches := append([]EvictableCache(nil), ce.caches...)
	ce.mu.RUnlock()

	var totalFreed int64
	for _, cache := range caches {
		totalFreed += cache.EvictPercent(percent)
	}
	return totalFreed
}

func (ce *CacheEvictor) EvictBytes(targetBytes int64) int64 {
	ce.mu.RLock()
	caches := ce.sortedBySize()
	ce.mu.RUnlock()

	var totalFreed int64
	for _, cache := range caches {
		if totalFreed >= targetBytes {
			break
		}
		totalFreed += cache.EvictPercent(0.25)
	}
	return totalFreed
}

func (ce *CacheEvictor) sortedBySize() []EvictableCache {
	caches := append([]EvictableCache(nil), ce.caches...)
	sort.Slice(caches, func(i, j int) bool {
		return caches[i].Size() > caches[j].Size()
	})
	return caches
}

func (ce *CacheEvictor) TotalSize() int64 {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	var total int64
	for _, cache := range ce.caches {
		total += cache.Size()
	}
	return total
}

func (ce *CacheEvictor) CacheCount() int {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return len(ce.caches)
}
