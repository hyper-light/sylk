package cache

import (
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"

	"github.com/adalundhe/sylk/core/domain"
)

const (
	defaultNumCounters = 1e7 // 10M counters for admission policy
	defaultMaxCost     = 1e8 // 100MB max cost
	defaultBufferItems = 64  // Buffer items for async writes
	defaultTTL         = 5 * time.Minute
)

// DomainCache provides a high-performance cache for domain classification results.
type DomainCache struct {
	cache  *ristretto.Cache
	ttl    time.Duration
	stats  *CacheStats
	mu     sync.RWMutex
	closed bool
}

// CacheConfig configures the domain cache.
type CacheConfig struct {
	NumCounters int64
	MaxCost     int64
	BufferItems int64
	TTL         time.Duration
}

// NewDomainCache creates a new DomainCache with the given configuration.
func NewDomainCache(config *CacheConfig) (*DomainCache, error) {
	cfg := applyDefaults(config)

	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: cfg.NumCounters,
		MaxCost:     cfg.MaxCost,
		BufferItems: cfg.BufferItems,
	})
	if err != nil {
		return nil, err
	}

	return &DomainCache{
		cache: cache,
		ttl:   cfg.TTL,
		stats: NewCacheStats(),
	}, nil
}

func applyDefaults(config *CacheConfig) *CacheConfig {
	cfg := &CacheConfig{
		NumCounters: defaultNumCounters,
		MaxCost:     defaultMaxCost,
		BufferItems: defaultBufferItems,
		TTL:         defaultTTL,
	}

	if config == nil {
		return cfg
	}

	if config.NumCounters > 0 {
		cfg.NumCounters = config.NumCounters
	}
	if config.MaxCost > 0 {
		cfg.MaxCost = config.MaxCost
	}
	if config.BufferItems > 0 {
		cfg.BufferItems = config.BufferItems
	}
	if config.TTL > 0 {
		cfg.TTL = config.TTL
	}

	return cfg
}

// Get retrieves a cached DomainContext by key.
func (dc *DomainCache) Get(key string) (*domain.DomainContext, bool) {
	dc.mu.RLock()
	if dc.closed {
		dc.mu.RUnlock()
		return nil, false
	}
	dc.mu.RUnlock()

	value, found := dc.cache.Get(key)
	if !found {
		dc.stats.RecordMiss()
		return nil, false
	}

	ctx, ok := value.(*domain.DomainContext)
	if !ok {
		dc.stats.RecordMiss()
		return nil, false
	}

	dc.stats.RecordHit()
	result := ctx.Clone()
	result.MarkCacheHit(key)
	return result, true
}

// Set stores a DomainContext with the default TTL.
func (dc *DomainCache) Set(key string, ctx *domain.DomainContext) bool {
	return dc.SetWithTTL(key, ctx, dc.ttl)
}

// SetWithTTL stores a DomainContext with a custom TTL.
func (dc *DomainCache) SetWithTTL(key string, ctx *domain.DomainContext, ttl time.Duration) bool {
	dc.mu.RLock()
	if dc.closed {
		dc.mu.RUnlock()
		return false
	}
	dc.mu.RUnlock()

	if ctx == nil {
		return false
	}

	cost := dc.estimateCost(ctx)
	stored := dc.cache.SetWithTTL(key, ctx.Clone(), cost, ttl)

	if stored {
		dc.stats.RecordSet()
	}

	return stored
}

func (dc *DomainCache) estimateCost(ctx *domain.DomainContext) int64 {
	base := int64(200)
	domainsSize := int64(len(ctx.DetectedDomains) * 8)
	confidencesSize := int64(len(ctx.DomainConfidences) * 16)
	signalsSize := dc.estimateSignalsSize(ctx.Signals)
	querySize := int64(len(ctx.OriginalQuery))

	return base + domainsSize + confidencesSize + signalsSize + querySize
}

func (dc *DomainCache) estimateSignalsSize(signals map[domain.Domain][]string) int64 {
	var size int64
	for _, sigs := range signals {
		for _, s := range sigs {
			size += int64(len(s))
		}
	}
	return size
}

// Delete removes an entry from the cache.
func (dc *DomainCache) Delete(key string) {
	dc.mu.RLock()
	if dc.closed {
		dc.mu.RUnlock()
		return
	}
	dc.mu.RUnlock()

	dc.cache.Del(key)
	dc.stats.RecordEviction()
}

// Clear removes all entries from the cache.
func (dc *DomainCache) Clear() {
	dc.mu.RLock()
	if dc.closed {
		dc.mu.RUnlock()
		return
	}
	dc.mu.RUnlock()

	dc.cache.Clear()
	dc.stats.Reset()
}

// Close closes the cache and releases resources.
func (dc *DomainCache) Close() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if dc.closed {
		return
	}

	dc.closed = true
	dc.cache.Close()
}

// Wait waits for all pending sets to complete.
func (dc *DomainCache) Wait() {
	dc.mu.RLock()
	if dc.closed {
		dc.mu.RUnlock()
		return
	}
	dc.mu.RUnlock()

	dc.cache.Wait()
}

// Stats returns the current cache statistics.
func (dc *DomainCache) Stats() *CacheStats {
	return dc.stats.Snapshot()
}

// GetTTL returns the default TTL.
func (dc *DomainCache) GetTTL() time.Duration {
	return dc.ttl
}

// SetTTL updates the default TTL for new entries.
func (dc *DomainCache) SetTTL(ttl time.Duration) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.ttl = ttl
}

// IsClosed returns whether the cache has been closed.
func (dc *DomainCache) IsClosed() bool {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.closed
}
