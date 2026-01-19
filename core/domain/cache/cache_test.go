package cache

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
)

func TestNewDomainCache(t *testing.T) {
	cache, err := NewDomainCache(nil)
	if err != nil {
		t.Fatalf("NewDomainCache failed: %v", err)
	}
	defer cache.Close()

	if cache == nil {
		t.Fatal("Cache should not be nil")
	}
	if cache.ttl != defaultTTL {
		t.Errorf("TTL = %v, want %v", cache.ttl, defaultTTL)
	}
}

func TestNewDomainCache_WithConfig(t *testing.T) {
	config := &CacheConfig{
		NumCounters: 1000,
		MaxCost:     10000,
		BufferItems: 32,
		TTL:         10 * time.Minute,
	}

	cache, err := NewDomainCache(config)
	if err != nil {
		t.Fatalf("NewDomainCache failed: %v", err)
	}
	defer cache.Close()

	if cache.ttl != 10*time.Minute {
		t.Errorf("TTL = %v, want 10m", cache.ttl)
	}
}

func TestDomainCache_SetAndGet(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	ctx := domain.NewDomainContext("test query")
	ctx.AddDomain(domain.DomainLibrarian, 0.85, []string{"signal1"})

	stored := cache.Set("key1", ctx)
	cache.Wait() // Wait for async write

	if !stored {
		t.Error("Set should return true")
	}

	retrieved, found := cache.Get("key1")
	if !found {
		t.Fatal("Should find cached entry")
	}
	if retrieved.OriginalQuery != "test query" {
		t.Errorf("OriginalQuery = %s, want test query", retrieved.OriginalQuery)
	}
	if !retrieved.CacheHit {
		t.Error("CacheHit should be true")
	}
}

func TestDomainCache_Get_NotFound(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	_, found := cache.Get("nonexistent")
	if found {
		t.Error("Should not find nonexistent key")
	}

	stats := cache.Stats()
	if stats.Misses() != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses())
	}
}

func TestDomainCache_SetWithTTL(t *testing.T) {
	cache, _ := NewDomainCache(&CacheConfig{
		TTL: 1 * time.Hour,
	})
	defer cache.Close()

	ctx := domain.NewDomainContext("test")
	cache.SetWithTTL("key", ctx, 100*time.Millisecond)
	cache.Wait()

	_, found := cache.Get("key")
	if !found {
		t.Error("Should find entry immediately")
	}

	// Wait for TTL to expire
	time.Sleep(200 * time.Millisecond)

	_, found = cache.Get("key")
	if found {
		t.Error("Entry should have expired")
	}
}

func TestDomainCache_Set_NilContext(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	stored := cache.Set("key", nil)
	if stored {
		t.Error("Should not store nil context")
	}
}

func TestDomainCache_Delete(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	ctx := domain.NewDomainContext("test")
	cache.Set("key", ctx)
	cache.Wait()

	cache.Delete("key")

	_, found := cache.Get("key")
	if found {
		t.Error("Should not find deleted entry")
	}
}

func TestDomainCache_Clear(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	ctx := domain.NewDomainContext("test")
	cache.Set("key1", ctx)
	cache.Set("key2", ctx)
	cache.Wait()

	cache.Clear()

	_, found1 := cache.Get("key1")
	_, found2 := cache.Get("key2")

	if found1 || found2 {
		t.Error("Clear should remove all entries")
	}
}

func TestDomainCache_Close(t *testing.T) {
	cache, _ := NewDomainCache(nil)

	cache.Close()

	if !cache.IsClosed() {
		t.Error("IsClosed should return true after Close")
	}

	// Operations on closed cache should be safe
	_, found := cache.Get("key")
	if found {
		t.Error("Get on closed cache should return false")
	}

	stored := cache.Set("key", domain.NewDomainContext("test"))
	if stored {
		t.Error("Set on closed cache should return false")
	}
}

func TestDomainCache_Stats(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	ctx := domain.NewDomainContext("test")
	cache.Set("key", ctx)
	cache.Wait()

	cache.Get("key")     // hit
	cache.Get("key")     // hit
	cache.Get("missing") // miss

	stats := cache.Stats()

	if stats.Hits() != 2 {
		t.Errorf("Hits = %d, want 2", stats.Hits())
	}
	if stats.Misses() != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses())
	}
	if stats.Sets() != 1 {
		t.Errorf("Sets = %d, want 1", stats.Sets())
	}
}

func TestDomainCache_GetTTL(t *testing.T) {
	cache, _ := NewDomainCache(&CacheConfig{TTL: 15 * time.Minute})
	defer cache.Close()

	if cache.GetTTL() != 15*time.Minute {
		t.Errorf("GetTTL = %v, want 15m", cache.GetTTL())
	}
}

func TestDomainCache_SetTTL(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	cache.SetTTL(30 * time.Minute)

	if cache.GetTTL() != 30*time.Minute {
		t.Errorf("GetTTL = %v, want 30m", cache.GetTTL())
	}
}

func TestDomainCache_CloneOnGet(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	original := domain.NewDomainContext("test")
	original.AddDomain(domain.DomainLibrarian, 0.85, nil)
	cache.Set("key", original)
	cache.Wait()

	retrieved, _ := cache.Get("key")
	retrieved.AddDomain(domain.DomainAcademic, 0.75, nil)

	// Original in cache should not be affected
	retrieved2, _ := cache.Get("key")
	if retrieved2.HasDomain(domain.DomainAcademic) {
		t.Error("Cached value should not be modified by returned value")
	}
}

func TestDomainCache_EstimateCost(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	ctx := domain.NewDomainContext("short query")
	ctx.AddDomain(domain.DomainLibrarian, 0.85, []string{"sig1", "sig2"})
	ctx.AddDomain(domain.DomainAcademic, 0.75, []string{"sig3"})

	cost := cache.estimateCost(ctx)

	if cost <= 0 {
		t.Error("Cost should be positive")
	}
	if cost < 200 { // Base cost
		t.Error("Cost should be at least base cost")
	}
}

func TestDomainCache_DoubleClose(t *testing.T) {
	cache, _ := NewDomainCache(nil)

	cache.Close()
	cache.Close() // Should not panic
}

func TestDomainCache_Wait(t *testing.T) {
	cache, _ := NewDomainCache(nil)
	defer cache.Close()

	ctx := domain.NewDomainContext("test")
	cache.Set("key", ctx)

	// Wait should not block indefinitely
	done := make(chan struct{})
	go func() {
		cache.Wait()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Error("Wait should complete quickly")
	}
}

func TestApplyDefaults(t *testing.T) {
	// Nil config
	cfg := applyDefaults(nil)
	if cfg.NumCounters != defaultNumCounters {
		t.Error("Should use default NumCounters")
	}

	// Partial config
	partial := &CacheConfig{NumCounters: 500}
	cfg = applyDefaults(partial)
	if cfg.NumCounters != 500 {
		t.Error("Should use provided NumCounters")
	}
	if cfg.MaxCost != defaultMaxCost {
		t.Error("Should use default MaxCost for unspecified value")
	}
}
