package cache

import (
	"sync/atomic"
	"time"
)

// CacheStats tracks cache performance metrics.
type CacheStats struct {
	hits      atomic.Int64
	misses    atomic.Int64
	sets      atomic.Int64
	evictions atomic.Int64
	startTime time.Time
}

// NewCacheStats creates a new CacheStats instance.
func NewCacheStats() *CacheStats {
	return &CacheStats{
		startTime: time.Now(),
	}
}

// RecordHit records a cache hit.
func (s *CacheStats) RecordHit() {
	s.hits.Add(1)
}

// RecordMiss records a cache miss.
func (s *CacheStats) RecordMiss() {
	s.misses.Add(1)
}

// RecordSet records a cache set operation.
func (s *CacheStats) RecordSet() {
	s.sets.Add(1)
}

// RecordEviction records a cache eviction.
func (s *CacheStats) RecordEviction() {
	s.evictions.Add(1)
}

// Hits returns the total number of cache hits.
func (s *CacheStats) Hits() int64 {
	return s.hits.Load()
}

// Misses returns the total number of cache misses.
func (s *CacheStats) Misses() int64 {
	return s.misses.Load()
}

// Sets returns the total number of set operations.
func (s *CacheStats) Sets() int64 {
	return s.sets.Load()
}

// Evictions returns the total number of evictions.
func (s *CacheStats) Evictions() int64 {
	return s.evictions.Load()
}

// Total returns the total number of get operations (hits + misses).
func (s *CacheStats) Total() int64 {
	return s.Hits() + s.Misses()
}

// HitRate returns the cache hit rate as a value between 0 and 1.
func (s *CacheStats) HitRate() float64 {
	total := s.Total()
	if total == 0 {
		return 0
	}
	return float64(s.Hits()) / float64(total)
}

// Uptime returns the duration since the cache was created.
func (s *CacheStats) Uptime() time.Duration {
	return time.Since(s.startTime)
}

// Reset resets all statistics to zero.
func (s *CacheStats) Reset() {
	s.hits.Store(0)
	s.misses.Store(0)
	s.sets.Store(0)
	s.evictions.Store(0)
	s.startTime = time.Now()
}

// Snapshot returns a copy of the current statistics.
func (s *CacheStats) Snapshot() *CacheStats {
	snapshot := &CacheStats{
		startTime: s.startTime,
	}
	snapshot.hits.Store(s.hits.Load())
	snapshot.misses.Store(s.misses.Load())
	snapshot.sets.Store(s.sets.Load())
	snapshot.evictions.Store(s.evictions.Load())
	return snapshot
}

// StatsSnapshot is a non-atomic snapshot of cache statistics for serialization.
type StatsSnapshot struct {
	Hits      int64         `json:"hits"`
	Misses    int64         `json:"misses"`
	Sets      int64         `json:"sets"`
	Evictions int64         `json:"evictions"`
	HitRate   float64       `json:"hit_rate"`
	Total     int64         `json:"total"`
	Uptime    time.Duration `json:"uptime"`
	UptimeStr string        `json:"uptime_str"`
}

// ToSnapshot converts CacheStats to a serializable StatsSnapshot.
func (s *CacheStats) ToSnapshot() StatsSnapshot {
	return StatsSnapshot{
		Hits:      s.Hits(),
		Misses:    s.Misses(),
		Sets:      s.Sets(),
		Evictions: s.Evictions(),
		HitRate:   s.HitRate(),
		Total:     s.Total(),
		Uptime:    s.Uptime(),
		UptimeStr: s.Uptime().String(),
	}
}
