package tools

import (
	"sync"
	"testing"
	"time"
)

func TestNewToolOutputCache(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
	if cache.Size() != 0 {
		t.Errorf("expected empty cache, got %d entries", cache.Size())
	}
}

func TestToolOutputCache_PutGet(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	result := &ToolResult{ExitCode: 0, Stdout: []byte("output")}
	cache.Put("eslint", "hash123", result)

	got, ok := cache.Get("eslint", "hash123")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.ExitCode != 0 {
		t.Errorf("expected ExitCode=0, got %d", got.ExitCode)
	}
	if string(got.Stdout) != "output" {
		t.Errorf("expected 'output', got %q", got.Stdout)
	}
}

func TestToolOutputCache_Miss(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	_, ok := cache.Get("eslint", "nonexistent")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestToolOutputCache_NotCacheable(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	result := &ToolResult{ExitCode: 0}
	cache.Put("go test", "hash", result)

	_, ok := cache.Get("go test", "hash")
	if ok {
		t.Error("go test should not be cached")
	}
}

func TestToolOutputCache_TTLExpiry(t *testing.T) {
	config := ToolCacheConfig{
		MaxEntries: 100,
		MaxSize:    1024 * 1024,
		TTL:        50 * time.Millisecond,
		CacheableTools: map[string]CachePolicy{
			"test": {Cacheable: true, TTL: 50 * time.Millisecond},
		},
	}
	cache := NewToolOutputCache(config)

	result := &ToolResult{ExitCode: 0}
	cache.Put("test", "hash", result)

	_, ok := cache.Get("test", "hash")
	if !ok {
		t.Fatal("expected cache hit immediately after put")
	}

	time.Sleep(60 * time.Millisecond)

	_, ok = cache.Get("test", "hash")
	if ok {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestToolOutputCache_MaxEntries(t *testing.T) {
	config := ToolCacheConfig{
		MaxEntries: 3,
		MaxSize:    1024 * 1024,
		TTL:        time.Hour,
		CacheableTools: map[string]CachePolicy{
			"test": {Cacheable: true, TTL: time.Hour},
		},
	}
	cache := NewToolOutputCache(config)

	for i := 0; i < 5; i++ {
		result := &ToolResult{ExitCode: i}
		cache.Put("test", string(rune('a'+i)), result)
		time.Sleep(time.Millisecond)
	}

	if cache.Size() > 3 {
		t.Errorf("expected max 3 entries, got %d", cache.Size())
	}
}

func TestToolOutputCache_MaxSize(t *testing.T) {
	config := ToolCacheConfig{
		MaxEntries: 100,
		MaxSize:    100,
		TTL:        time.Hour,
		CacheableTools: map[string]CachePolicy{
			"test": {Cacheable: true, TTL: time.Hour},
		},
	}
	cache := NewToolOutputCache(config)

	largeOutput := make([]byte, 50)
	for i := 0; i < 5; i++ {
		result := &ToolResult{Stdout: largeOutput}
		cache.Put("test", string(rune('a'+i)), result)
		time.Sleep(time.Millisecond)
	}

	if cache.TotalBytes() > 100 {
		t.Errorf("expected max 100 bytes, got %d", cache.TotalBytes())
	}
}

func TestToolOutputCache_Invalidate(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	cache.Put("eslint", "hash1", &ToolResult{ExitCode: 1})
	cache.Put("eslint", "hash2", &ToolResult{ExitCode: 2})
	cache.Put("prettier", "hash3", &ToolResult{ExitCode: 3})

	cache.Invalidate("eslint")

	if _, ok := cache.Get("eslint", "hash1"); ok {
		t.Error("eslint hash1 should be invalidated")
	}
	if _, ok := cache.Get("eslint", "hash2"); ok {
		t.Error("eslint hash2 should be invalidated")
	}
	if _, ok := cache.Get("prettier", "hash3"); !ok {
		t.Error("prettier should still be cached")
	}
}

func TestToolOutputCache_InvalidateAll(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	cache.Put("eslint", "hash1", &ToolResult{})
	cache.Put("prettier", "hash2", &ToolResult{})

	cache.InvalidateAll()

	if cache.Size() != 0 {
		t.Errorf("expected empty cache, got %d entries", cache.Size())
	}
	if cache.TotalBytes() != 0 {
		t.Errorf("expected 0 bytes, got %d", cache.TotalBytes())
	}
}

func TestToolOutputCache_ComputeInputHash(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	inv1 := ToolInvocation{Command: "eslint", Args: []string{"file.js"}, WorkingDir: "/project"}
	inv2 := ToolInvocation{Command: "eslint", Args: []string{"file.js"}, WorkingDir: "/project"}
	inv3 := ToolInvocation{Command: "eslint", Args: []string{"other.js"}, WorkingDir: "/project"}

	hash1 := cache.ComputeInputHash(inv1)
	hash2 := cache.ComputeInputHash(inv2)
	hash3 := cache.ComputeInputHash(inv3)

	if hash1 != hash2 {
		t.Error("identical invocations should have same hash")
	}
	if hash1 == hash3 {
		t.Error("different args should have different hash")
	}
	if len(hash1) != 64 {
		t.Errorf("expected 64-char SHA256 hex, got %d chars", len(hash1))
	}
}

func TestToolOutputCache_Concurrent(t *testing.T) {
	config := ToolCacheConfig{
		MaxEntries: 1000,
		MaxSize:    10 * 1024 * 1024,
		TTL:        time.Hour,
		CacheableTools: map[string]CachePolicy{
			"test": {Cacheable: true, TTL: time.Hour},
		},
	}
	cache := NewToolOutputCache(config)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			hash := string(rune('a' + (n % 26)))
			cache.Put("test", hash, &ToolResult{ExitCode: n})
			cache.Get("test", hash)
			cache.Size()
			cache.TotalBytes()
		}(i)
	}
	wg.Wait()
}

func TestToolOutputCache_HitCount(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	cache.Put("eslint", "hash", &ToolResult{})

	for i := 0; i < 5; i++ {
		cache.Get("eslint", "hash")
	}

	cache.mu.RLock()
	cached := cache.cache["eslint:hash"]
	hitCount := cached.HitCount
	cache.mu.RUnlock()

	if hitCount != 5 {
		t.Errorf("expected 5 hits, got %d", hitCount)
	}
}

func TestToolOutputCache_EvictOldest(t *testing.T) {
	config := ToolCacheConfig{
		MaxEntries: 2,
		MaxSize:    1024 * 1024,
		TTL:        time.Hour,
		CacheableTools: map[string]CachePolicy{
			"test": {Cacheable: true, TTL: time.Hour},
		},
	}
	cache := NewToolOutputCache(config)

	cache.Put("test", "first", &ToolResult{ExitCode: 1})
	time.Sleep(time.Millisecond)
	cache.Put("test", "second", &ToolResult{ExitCode: 2})
	time.Sleep(time.Millisecond)
	cache.Put("test", "third", &ToolResult{ExitCode: 3})

	if _, ok := cache.Get("test", "first"); ok {
		t.Error("oldest entry should be evicted")
	}
	if _, ok := cache.Get("test", "second"); !ok {
		t.Error("second entry should remain")
	}
	if _, ok := cache.Get("test", "third"); !ok {
		t.Error("third entry should remain")
	}
}

func TestToolOutputCache_NilResult(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	cache.Put("eslint", "hash", nil)

	got, ok := cache.Get("eslint", "hash")
	if !ok {
		t.Fatal("expected cache hit for nil result")
	}
	if got != nil {
		t.Error("expected nil result")
	}
}

func TestToolOutputCache_PartialResult(t *testing.T) {
	config := DefaultToolCacheConfig()
	cache := NewToolOutputCache(config)

	result := &ToolResult{ExitCode: 1, Partial: true}
	cache.Put("eslint", "hash", result)

	got, ok := cache.Get("eslint", "hash")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if !got.Partial {
		t.Error("expected Partial=true")
	}
}

func TestToolOutputCache_PerToolTTL(t *testing.T) {
	config := ToolCacheConfig{
		MaxEntries: 100,
		MaxSize:    1024 * 1024,
		TTL:        time.Hour,
		CacheableTools: map[string]CachePolicy{
			"fast": {Cacheable: true, TTL: 10 * time.Millisecond},
			"slow": {Cacheable: true, TTL: time.Hour},
		},
	}
	cache := NewToolOutputCache(config)

	cache.Put("fast", "hash", &ToolResult{})
	cache.Put("slow", "hash", &ToolResult{})

	time.Sleep(20 * time.Millisecond)

	if _, ok := cache.Get("fast", "hash"); ok {
		t.Error("fast tool should have expired")
	}
	if _, ok := cache.Get("slow", "hash"); !ok {
		t.Error("slow tool should still be cached")
	}
}
