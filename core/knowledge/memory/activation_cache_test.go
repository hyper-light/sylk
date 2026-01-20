package memory

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Configuration Tests
// =============================================================================

func TestDefaultActivationCacheConfig(t *testing.T) {
	config := DefaultActivationCacheConfig()

	if config.MaxSize <= 0 {
		t.Error("expected positive MaxSize")
	}
	if config.TTL <= 0 {
		t.Error("expected positive TTL")
	}
	if config.CleanupInterval <= 0 {
		t.Error("expected positive CleanupInterval")
	}
}

func TestSmallActivationCacheConfig(t *testing.T) {
	config := SmallActivationCacheConfig()

	if config.MaxSize >= DefaultActivationCacheConfig().MaxSize {
		t.Error("expected smaller MaxSize than default")
	}
}

func TestLargeActivationCacheConfig(t *testing.T) {
	config := LargeActivationCacheConfig()

	if config.MaxSize <= DefaultActivationCacheConfig().MaxSize {
		t.Error("expected larger MaxSize than default")
	}
}

// =============================================================================
// ActivationCacheEntry Tests
// =============================================================================

func TestActivationCacheEntry_IsExpired(t *testing.T) {
	entry := &ActivationCacheEntry{
		ExpiresAt: time.Now().Add(-time.Minute), // Already expired
	}

	if !entry.IsExpired() {
		t.Error("expected entry to be expired")
	}

	entry.ExpiresAt = time.Now().Add(time.Minute) // Not expired
	if entry.IsExpired() {
		t.Error("expected entry not to be expired")
	}
}

// =============================================================================
// ActivationCache Constructor Tests
// =============================================================================

func TestNewActivationCache(t *testing.T) {
	config := DefaultActivationCacheConfig()
	cache := NewActivationCache(config)
	defer cache.Stop()

	if cache == nil {
		t.Fatal("expected non-nil cache")
	}

	if cache.Size() != 0 {
		t.Errorf("expected empty cache, got size %d", cache.Size())
	}
}

func TestNewActivationCache_WithInvalidConfig(t *testing.T) {
	// Config with invalid values should be corrected
	config := ActivationCacheConfig{
		MaxSize:         0,
		TTL:             0,
		CleanupInterval: 0, // Disable cleanup for this test
	}

	cache := NewActivationCache(config)
	defer cache.Stop()

	// Should use default values
	if cache.config.MaxSize <= 0 {
		t.Error("expected positive MaxSize after correction")
	}
	if cache.config.TTL <= 0 {
		t.Error("expected positive TTL after correction")
	}
}

func TestNewDefaultActivationCache(t *testing.T) {
	cache := NewDefaultActivationCache()
	defer cache.Stop()

	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
}

// =============================================================================
// Cache Operations Tests
// =============================================================================

func TestActivationCache_SetAndGet(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0, // Disable cleanup
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	accessTime := time.Now()
	activation := 0.75

	// Set value
	cache.Set(nodeID, accessTime, activation)

	// Get value
	result, found := cache.Get(nodeID, accessTime)
	if !found {
		t.Fatal("expected to find cached value")
	}
	if result != activation {
		t.Errorf("expected activation %f, got %f", activation, result)
	}
}

func TestActivationCache_Get_NotFound(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	_, found := cache.Get("nonexistent", time.Now())
	if found {
		t.Error("expected not to find nonexistent entry")
	}
}

func TestActivationCache_Get_Expired(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Millisecond, // Very short TTL
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	accessTime := time.Now()
	cache.Set(nodeID, accessTime, 0.5)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	_, found := cache.Get(nodeID, accessTime)
	if found {
		t.Error("expected expired entry not to be found")
	}
}

func TestActivationCache_Set_UpdatesExisting(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	accessTime := time.Now()

	cache.Set(nodeID, accessTime, 0.5)
	cache.Set(nodeID, accessTime, 0.8)

	result, found := cache.Get(nodeID, accessTime)
	if !found {
		t.Fatal("expected to find cached value")
	}
	if result != 0.8 {
		t.Errorf("expected updated activation 0.8, got %f", result)
	}

	// Size should still be 1
	if cache.Size() != 1 {
		t.Errorf("expected size 1, got %d", cache.Size())
	}
}

func TestActivationCache_GetOrCompute(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	accessTime := time.Now()
	computeCalled := 0

	compute := func() float64 {
		computeCalled++
		return 0.75
	}

	// First call should compute
	result1 := cache.GetOrCompute(nodeID, accessTime, compute)
	if result1 != 0.75 {
		t.Errorf("expected 0.75, got %f", result1)
	}
	if computeCalled != 1 {
		t.Errorf("expected compute called once, called %d times", computeCalled)
	}

	// Second call should use cache
	result2 := cache.GetOrCompute(nodeID, accessTime, compute)
	if result2 != 0.75 {
		t.Errorf("expected 0.75, got %f", result2)
	}
	if computeCalled != 1 {
		t.Errorf("expected compute still called once, called %d times", computeCalled)
	}
}

func TestActivationCache_Invalidate(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	accessTime := time.Now()
	cache.Set(nodeID, accessTime, 0.5)

	cache.Invalidate(nodeID, accessTime)

	_, found := cache.Get(nodeID, accessTime)
	if found {
		t.Error("expected entry to be invalidated")
	}
}

func TestActivationCache_InvalidateNode(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	t1 := time.Now()
	t2 := t1.Add(time.Hour)

	cache.Set(nodeID, t1, 0.5)
	cache.Set(nodeID, t2, 0.6)
	cache.Set("node-2", t1, 0.7)

	cache.InvalidateNode(nodeID)

	if cache.Size() != 1 {
		t.Errorf("expected size 1 after invalidating node, got %d", cache.Size())
	}

	_, found := cache.Get("node-2", t1)
	if !found {
		t.Error("expected node-2 to still be cached")
	}
}

func TestActivationCache_Clear(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	cache.Set("node-1", time.Now(), 0.5)
	cache.Set("node-2", time.Now(), 0.6)

	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("expected empty cache after clear, got size %d", cache.Size())
	}
}

// =============================================================================
// LRU Eviction Tests
// =============================================================================

func TestActivationCache_LRUEviction(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         3, // Small cache
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	now := time.Now()

	// Add entries up to capacity
	cache.Set("node-1", now, 0.1)
	cache.Set("node-2", now, 0.2)
	cache.Set("node-3", now, 0.3)

	if cache.Size() != 3 {
		t.Errorf("expected size 3, got %d", cache.Size())
	}

	// Access node-1 to make it recently used
	cache.Get("node-1", now)

	// Add another entry - should evict node-2 (LRU)
	cache.Set("node-4", now, 0.4)

	if cache.Size() != 3 {
		t.Errorf("expected size 3 after eviction, got %d", cache.Size())
	}

	// node-2 should be evicted
	_, found := cache.Get("node-2", now)
	if found {
		t.Error("expected node-2 to be evicted")
	}

	// node-1 should still be present (was accessed)
	_, found = cache.Get("node-1", now)
	if !found {
		t.Error("expected node-1 to still be cached")
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestActivationCache_Stats(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	accessTime := time.Now()

	// Miss
	cache.Get(nodeID, accessTime)

	// Set
	cache.Set(nodeID, accessTime, 0.5)

	// Hit
	cache.Get(nodeID, accessTime)
	cache.Get(nodeID, accessTime)

	stats := cache.Stats()

	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
	if stats.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", stats.Hits)
	}
}

func TestActivationCache_HitRate(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	accessTime := time.Now()

	// Miss
	cache.Get(nodeID, accessTime)

	// Set and hit 3 times
	cache.Set(nodeID, accessTime, 0.5)
	cache.Get(nodeID, accessTime)
	cache.Get(nodeID, accessTime)
	cache.Get(nodeID, accessTime)

	hitRate := cache.HitRate()
	expected := 3.0 / 4.0 // 3 hits out of 4 total requests

	if hitRate != expected {
		t.Errorf("expected hit rate %f, got %f", expected, hitRate)
	}
}

func TestActivationCache_HitRate_NoRequests(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	hitRate := cache.HitRate()
	if hitRate != 0.0 {
		t.Errorf("expected hit rate 0.0 with no requests, got %f", hitRate)
	}
}

func TestActivationCache_ResetStats(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	// Generate some stats
	cache.Get("node-1", time.Now())
	cache.Set("node-1", time.Now(), 0.5)
	cache.Get("node-1", time.Now())

	cache.ResetStats()

	stats := cache.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Evictions != 0 || stats.Expirations != 0 {
		t.Error("expected all stats to be 0 after reset")
	}
}

// =============================================================================
// Batch Operations Tests
// =============================================================================

func TestActivationCache_GetBatch(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	accessTime := time.Now()
	cache.Set("node-1", accessTime, 0.1)
	cache.Set("node-2", accessTime, 0.2)
	cache.Set("node-3", accessTime, 0.3)

	result := cache.GetBatch([]string{"node-1", "node-2", "node-4"}, accessTime)

	if len(result) != 2 {
		t.Errorf("expected 2 results, got %d", len(result))
	}
	if result["node-1"] != 0.1 {
		t.Errorf("expected node-1=0.1, got %f", result["node-1"])
	}
	if result["node-2"] != 0.2 {
		t.Errorf("expected node-2=0.2, got %f", result["node-2"])
	}
}

func TestActivationCache_SetBatch(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	accessTime := time.Now()
	activations := map[string]float64{
		"node-1": 0.1,
		"node-2": 0.2,
		"node-3": 0.3,
	}

	cache.SetBatch(activations, accessTime)

	if cache.Size() != 3 {
		t.Errorf("expected size 3, got %d", cache.Size())
	}

	for nodeID, expected := range activations {
		result, found := cache.Get(nodeID, accessTime)
		if !found {
			t.Errorf("expected to find %s", nodeID)
		}
		if result != expected {
			t.Errorf("expected %s=%f, got %f", nodeID, expected, result)
		}
	}
}

func TestActivationCache_SetBatch_Empty(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	// Should not panic
	cache.SetBatch(map[string]float64{}, time.Now())
	cache.SetBatch(nil, time.Now())

	if cache.Size() != 0 {
		t.Errorf("expected empty cache, got size %d", cache.Size())
	}
}

// =============================================================================
// Diagnostic Method Tests
// =============================================================================

func TestActivationCache_EntriesPerNode(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Hour, // Long TTL to prevent same-bucket collision
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	now := time.Now()
	cache.Set("node-1", now, 0.1)
	cache.Set("node-1", now.Add(2*time.Hour), 0.2) // Different time bucket
	cache.Set("node-2", now, 0.3)

	counts := cache.EntriesPerNode()

	if counts["node-1"] != 2 {
		t.Errorf("expected node-1 count 2, got %d", counts["node-1"])
	}
	if counts["node-2"] != 1 {
		t.Errorf("expected node-2 count 1, got %d", counts["node-2"])
	}
}

func TestActivationCache_ExpiredCount(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Millisecond, // Very short TTL
		CleanupInterval: 0,                // No cleanup
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	cache.Set("node-1", time.Now(), 0.1)
	cache.Set("node-2", time.Now(), 0.2)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	expiredCount := cache.ExpiredCount()
	if expiredCount != 2 {
		t.Errorf("expected 2 expired entries, got %d", expiredCount)
	}
}

func TestActivationCache_OldestEntry(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	// Empty cache
	if cache.OldestEntry() != 0 {
		t.Error("expected 0 for empty cache")
	}

	cache.Set("node-1", time.Now(), 0.1)
	time.Sleep(10 * time.Millisecond)

	oldest := cache.OldestEntry()
	if oldest < 10*time.Millisecond {
		t.Errorf("expected oldest entry age >= 10ms, got %v", oldest)
	}
}

func TestActivationCache_MemoryEstimate(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	cache.Set("node-1", time.Now(), 0.1)
	cache.Set("node-2", time.Now(), 0.2)

	estimate := cache.MemoryEstimate()
	if estimate <= 0 {
		t.Error("expected positive memory estimate")
	}
}

// =============================================================================
// Lifecycle Tests
// =============================================================================

func TestActivationCache_Stop(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 100 * time.Millisecond,
	}
	cache := NewActivationCache(config)

	// Stop should not block
	done := make(chan struct{})
	go func() {
		cache.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("Stop() blocked too long")
	}

	// Double stop should be safe
	cache.Stop()
}

func TestActivationCache_CleanupRemovesExpired(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             10 * time.Millisecond,
		CleanupInterval: 20 * time.Millisecond,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	cache.Set("node-1", time.Now(), 0.1)

	// Wait for expiration and cleanup
	time.Sleep(50 * time.Millisecond)

	// Entry should be cleaned up
	if cache.Size() != 0 {
		t.Errorf("expected cleanup to remove expired entry, size=%d", cache.Size())
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestActivationCache_ConcurrentAccess(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         1000,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	var wg sync.WaitGroup
	nodeCount := 100
	goroutines := 10

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < nodeCount; i++ {
				nodeID := "node-" + string(rune('a'+i%26))
				accessTime := time.Now()
				activation := float64(i) / 100.0

				cache.Set(nodeID, accessTime, activation)
				cache.Get(nodeID, accessTime)
			}
		}(g)
	}

	wg.Wait()

	// Should not have panicked
	if cache.Size() == 0 {
		t.Error("expected non-empty cache after concurrent operations")
	}
}

func TestActivationCache_ConcurrentGetOrCompute(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	var computeCalls int64
	var mu sync.Mutex

	compute := func() float64 {
		mu.Lock()
		computeCalls++
		mu.Unlock()
		time.Sleep(time.Millisecond) // Simulate expensive computation
		return 0.75
	}

	var wg sync.WaitGroup
	goroutines := 10

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.GetOrCompute("shared-node", time.Now(), compute)
		}()
	}

	wg.Wait()

	// Compute should be called multiple times due to race, but much less than goroutine count
	// in a real scenario (here we're testing safety, not optimization)
	mu.Lock()
	calls := computeCalls
	mu.Unlock()

	if calls == 0 {
		t.Error("expected compute to be called at least once")
	}
}

// =============================================================================
// Time Bucketing Tests
// =============================================================================

func TestActivationCache_TimeBucketing(t *testing.T) {
	config := ActivationCacheConfig{
		MaxSize:         100,
		TTL:             time.Minute, // 1 minute buckets
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	nodeID := "node-1"
	baseTime := time.Now()

	// Set at base time
	cache.Set(nodeID, baseTime, 0.5)

	// Small time difference should hit same bucket
	result, found := cache.Get(nodeID, baseTime.Add(time.Second))
	if !found {
		t.Error("expected to find entry with small time difference")
	}
	if result != 0.5 {
		t.Errorf("expected 0.5, got %f", result)
	}

	// Large time difference should miss (different bucket)
	_, found = cache.Get(nodeID, baseTime.Add(2*time.Minute))
	if found {
		t.Error("expected to miss entry with large time difference (different bucket)")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkActivationCache_Get(b *testing.B) {
	config := ActivationCacheConfig{
		MaxSize:         10000,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	accessTime := time.Now()
	cache.Set("benchmark-node", accessTime, 0.5)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get("benchmark-node", accessTime)
	}
}

func BenchmarkActivationCache_Set(b *testing.B) {
	config := ActivationCacheConfig{
		MaxSize:         10000,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	accessTime := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set("benchmark-node", accessTime, float64(i)/float64(b.N))
	}
}

func BenchmarkActivationCache_GetOrCompute_Cached(b *testing.B) {
	config := ActivationCacheConfig{
		MaxSize:         10000,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	accessTime := time.Now()
	compute := func() float64 { return 0.5 }

	// Pre-populate
	cache.GetOrCompute("benchmark-node", accessTime, compute)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.GetOrCompute("benchmark-node", accessTime, compute)
	}
}

func BenchmarkActivationCache_ConcurrentGet(b *testing.B) {
	config := ActivationCacheConfig{
		MaxSize:         10000,
		TTL:             time.Minute,
		CleanupInterval: 0,
	}
	cache := NewActivationCache(config)
	defer cache.Stop()

	accessTime := time.Now()
	for i := 0; i < 100; i++ {
		cache.Set("node-"+string(rune('a'+i%26)), accessTime, 0.5)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Get("node-"+string(rune('a'+i%26)), accessTime)
			i++
		}
	})
}
