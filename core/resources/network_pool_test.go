package resources

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewNetworkPool verifies pool creation.
func TestNewNetworkPool(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	stats := pool.Stats()
	if stats.Total != 50 {
		t.Errorf("expected total 50, got %d", stats.Total)
	}
	if stats.PerProviderMax != 10 {
		t.Errorf("expected per-provider max 10, got %d", stats.PerProviderMax)
	}
}

// TestNetworkPoolGlobalLimit verifies global connection limit.
func TestNetworkPoolGlobalLimit(t *testing.T) {
	cfg := NetworkPoolConfig{
		GlobalLimit:     5,
		PerProviderMax:  10, // Higher than global
		UserReserved:    0.20,
		PipelineTimeout: 100 * time.Millisecond,
	}
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	handles := make([]*NetworkHandle, 0, 5)

	// Acquire up to global limit
	for i := 0; i < 5; i++ {
		h, err := pool.AcquireUser(ctx, "provider-a")
		if err != nil {
			t.Fatalf("acquire %d failed: %v", i, err)
		}
		handles = append(handles, h)
	}

	stats := pool.Stats()
	if stats.Available != 0 {
		t.Errorf("expected 0 available, got %d", stats.Available)
	}

	// User can still acquire via preemption
	h, err := pool.AcquireUser(ctx, "provider-b")
	if err != nil {
		t.Fatalf("user should succeed via preemption: %v", err)
	}
	pool.Release(h)

	for _, h := range handles {
		pool.Release(h)
	}
}

// TestNetworkPoolPerProviderLimit verifies per-provider limits.
func TestNetworkPoolPerProviderLimit(t *testing.T) {
	cfg := NetworkPoolConfig{
		GlobalLimit:     100,
		PerProviderMax:  3,
		UserReserved:    0.20,
		PipelineTimeout: 100 * time.Millisecond,
	}
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	provider := "openai"
	handles := make([]*NetworkHandle, 0, 3)

	// Acquire up to per-provider limit
	for i := 0; i < 3; i++ {
		h, err := pool.AcquireUser(ctx, provider)
		if err != nil {
			t.Fatalf("acquire %d failed: %v", i, err)
		}
		handles = append(handles, h)
	}

	// Next acquire for same provider should fail
	_, err := pool.AcquireUser(ctx, provider)
	if err != ErrProviderLimitReached {
		t.Errorf("expected ErrProviderLimitReached, got %v", err)
	}

	// But different provider should work
	h, err := pool.AcquireUser(ctx, "anthropic")
	if err != nil {
		t.Fatalf("different provider should succeed: %v", err)
	}
	pool.Release(h)

	// Release one and try again
	pool.Release(handles[0])
	handles = handles[1:]

	h, err = pool.AcquireUser(ctx, provider)
	if err != nil {
		t.Fatalf("should succeed after release: %v", err)
	}
	pool.Release(h)

	for _, h := range handles {
		pool.Release(h)
	}
}

// TestNetworkPoolProviderCount verifies provider count tracking.
func TestNetworkPoolProviderCount(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	if pool.ProviderCount("test") != 0 {
		t.Error("expected 0 count for unknown provider")
	}

	h1, _ := pool.AcquireUser(ctx, "test")
	if pool.ProviderCount("test") != 1 {
		t.Errorf("expected 1, got %d", pool.ProviderCount("test"))
	}

	h2, _ := pool.AcquireUser(ctx, "test")
	if pool.ProviderCount("test") != 2 {
		t.Errorf("expected 2, got %d", pool.ProviderCount("test"))
	}

	pool.Release(h1)
	if pool.ProviderCount("test") != 1 {
		t.Errorf("expected 1 after release, got %d", pool.ProviderCount("test"))
	}

	pool.Release(h2)
	if pool.ProviderCount("test") != 0 {
		t.Errorf("expected 0 after all released, got %d", pool.ProviderCount("test"))
	}
}

// TestNetworkPoolAcquireUserForEndpoint verifies endpoint tracking.
func TestNetworkPoolAcquireUserForEndpoint(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	provider := "anthropic"
	endpoint := "https://api.anthropic.com/v1/messages"

	h, err := pool.AcquireUserForEndpoint(ctx, provider, endpoint)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer pool.Release(h)

	if h.ProviderID != provider {
		t.Errorf("expected provider %q, got %q", provider, h.ProviderID)
	}
	if h.Endpoint != endpoint {
		t.Errorf("expected endpoint %q, got %q", endpoint, h.Endpoint)
	}
}

// TestNetworkPoolAcquirePipeline verifies pipeline acquisition.
func TestNetworkPoolAcquirePipeline(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	h, err := pool.AcquirePipeline(ctx, "google", 5)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer pool.Release(h)

	if h.ProviderID != "google" {
		t.Errorf("expected provider 'google', got %q", h.ProviderID)
	}
}

// TestNetworkPoolAcquirePipelineForEndpoint verifies pipeline endpoint tracking.
func TestNetworkPoolAcquirePipelineForEndpoint(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	endpoint := "https://api.example.com/endpoint"

	h, err := pool.AcquirePipelineForEndpoint(ctx, "example", endpoint, 1)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer pool.Release(h)

	if h.Endpoint != endpoint {
		t.Errorf("expected endpoint %q, got %q", endpoint, h.Endpoint)
	}
}

// TestNetworkPoolRelease verifies proper release.
func TestNetworkPoolRelease(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUser(ctx, "test")

	beforeStats := pool.Stats()
	err := pool.Release(h)
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	afterStats := pool.Stats()
	if afterStats.Available != beforeStats.Available+1 {
		t.Error("expected available to increase")
	}
	if afterStats.ActiveHandles != beforeStats.ActiveHandles-1 {
		t.Error("expected active handles to decrease")
	}
}

// TestNetworkPoolReleaseInvalid verifies error on invalid release.
func TestNetworkPoolReleaseInvalid(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	err := pool.Release(nil)
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for nil, got %v", err)
	}

	err = pool.Release(&NetworkHandle{})
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for empty, got %v", err)
	}
}

// TestNetworkPoolTouch verifies usage tracking.
func TestNetworkPoolTouch(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUser(ctx, "test")
	defer pool.Release(h)

	initialLastUsed := h.LastUsed
	initialUseCount := h.UseCount

	time.Sleep(10 * time.Millisecond)
	pool.Touch(h)

	if !h.LastUsed.After(initialLastUsed) {
		t.Error("expected LastUsed to be updated")
	}
	if h.UseCount != initialUseCount+1 {
		t.Errorf("expected UseCount %d, got %d", initialUseCount+1, h.UseCount)
	}
}

// TestNetworkPoolTouchNil verifies nil touch is safe.
func TestNetworkPoolTouchNil(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	// Should not panic
	pool.Touch(nil)
}

// TestNetworkPoolStats verifies statistics accuracy.
func TestNetworkPoolStats(t *testing.T) {
	cfg := NetworkPoolConfig{
		GlobalLimit:     20,
		PerProviderMax:  5,
		UserReserved:    0.25,
		PipelineTimeout: time.Second,
	}
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	stats := pool.Stats()
	if stats.Type != ResourceTypeNetwork {
		t.Errorf("expected ResourceTypeNetwork, got %v", stats.Type)
	}
	if stats.Total != 20 {
		t.Errorf("expected total 20, got %d", stats.Total)
	}
	if stats.PerProviderMax != 5 {
		t.Errorf("expected per-provider max 5, got %d", stats.PerProviderMax)
	}

	h1, _ := pool.AcquireUser(ctx, "provider-a")
	h2, _ := pool.AcquireUser(ctx, "provider-a")
	h3, _ := pool.AcquireUser(ctx, "provider-b")

	stats = pool.Stats()
	if stats.ActiveHandles != 3 {
		t.Errorf("expected 3 active handles, got %d", stats.ActiveHandles)
	}
	if stats.ProviderCounts["provider-a"] != 2 {
		t.Errorf("expected 2 for provider-a, got %d", stats.ProviderCounts["provider-a"])
	}
	if stats.ProviderCounts["provider-b"] != 1 {
		t.Errorf("expected 1 for provider-b, got %d", stats.ProviderCounts["provider-b"])
	}

	pool.Release(h1)
	pool.Release(h2)
	pool.Release(h3)
}

// TestNetworkPoolClose verifies pool closure.
func TestNetworkPoolClose(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)

	err := pool.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	err = pool.Close()
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed on double close, got %v", err)
	}

	ctx := context.Background()
	_, err = pool.AcquireUser(ctx, "test")
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed after close, got %v", err)
	}
}

// TestNetworkPoolConcurrent verifies thread safety.
func TestNetworkPoolConcurrent(t *testing.T) {
	cfg := NetworkPoolConfig{
		GlobalLimit:     100,
		PerProviderMax:  20,
		UserReserved:    0.20,
		PipelineTimeout: time.Second,
	}
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	var successCount atomic.Int32
	providers := []string{"openai", "anthropic", "google", "azure", "aws"}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			provider := providers[idx%len(providers)]
			h, err := pool.AcquireUser(ctx, provider)
			if err != nil {
				return
			}
			successCount.Add(1)

			pool.Touch(h)
			time.Sleep(time.Millisecond)
			pool.Release(h)
		}(i)
	}

	wg.Wait()

	if successCount.Load() == 0 {
		t.Error("no successful acquisitions")
	}

	stats := pool.Stats()
	if stats.ActiveHandles != 0 {
		t.Errorf("expected 0 active handles, got %d", stats.ActiveHandles)
	}
}

// TestNetworkPoolMultipleProviders verifies independent provider limits.
func TestNetworkPoolMultipleProviders(t *testing.T) {
	cfg := NetworkPoolConfig{
		GlobalLimit:     10,
		PerProviderMax:  3,
		UserReserved:    0.20,
		PipelineTimeout: time.Second,
	}
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	// Max out provider A
	var handlesA []*NetworkHandle
	for i := 0; i < 3; i++ {
		h, _ := pool.AcquireUser(ctx, "provider-a")
		handlesA = append(handlesA, h)
	}

	// Provider A should be at limit
	_, err := pool.AcquireUser(ctx, "provider-a")
	if err != ErrProviderLimitReached {
		t.Error("expected provider-a to be at limit")
	}

	// Provider B should still have capacity
	hB, err := pool.AcquireUser(ctx, "provider-b")
	if err != nil {
		t.Fatalf("provider-b should have capacity: %v", err)
	}

	stats := pool.Stats()
	if stats.ProviderCounts["provider-a"] != 3 {
		t.Errorf("expected 3 for provider-a, got %d", stats.ProviderCounts["provider-a"])
	}
	if stats.ProviderCounts["provider-b"] != 1 {
		t.Errorf("expected 1 for provider-b, got %d", stats.ProviderCounts["provider-b"])
	}

	pool.Release(hB)
	for _, h := range handlesA {
		pool.Release(h)
	}
}

// TestNetworkPoolConfigNormalization verifies config defaults.
func TestNetworkPoolConfigNormalization(t *testing.T) {
	cfg := NetworkPoolConfig{
		GlobalLimit:     0,
		PerProviderMax:  0,
		UserReserved:    0,
		PipelineTimeout: 0,
	}
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	stats := pool.Stats()
	if stats.Total != 50 {
		t.Errorf("expected normalized total 50, got %d", stats.Total)
	}
	if stats.PerProviderMax != 10 {
		t.Errorf("expected normalized per-provider 10, got %d", stats.PerProviderMax)
	}
}

// TestNetworkPoolDefaultConfig verifies default configuration.
func TestNetworkPoolDefaultConfig(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()

	if cfg.GlobalLimit != 50 {
		t.Errorf("expected 50 global, got %d", cfg.GlobalLimit)
	}
	if cfg.PerProviderMax != 10 {
		t.Errorf("expected 10 per-provider, got %d", cfg.PerProviderMax)
	}
	if cfg.UserReserved != 0.20 {
		t.Errorf("expected 0.20 reserved, got %f", cfg.UserReserved)
	}
	if cfg.PipelineTimeout != 30*time.Second {
		t.Errorf("expected 30s timeout, got %v", cfg.PipelineTimeout)
	}
}

// TestNetworkPoolHandleFields verifies handle contains correct data.
func TestNetworkPoolHandleFields(t *testing.T) {
	cfg := DefaultNetworkPoolConfig()
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	before := time.Now()
	h, _ := pool.AcquireUser(ctx, "test-provider")
	after := time.Now()
	defer pool.Release(h)

	if h.ProviderID != "test-provider" {
		t.Errorf("expected provider 'test-provider', got %q", h.ProviderID)
	}
	if h.CreatedAt.Before(before) || h.CreatedAt.After(after) {
		t.Error("CreatedAt not in expected range")
	}
	if h.UseCount != 1 {
		t.Errorf("expected UseCount 1, got %d", h.UseCount)
	}
}

// TestNetworkPoolProviderLimitError verifies error type.
func TestNetworkPoolProviderLimitError(t *testing.T) {
	err := ErrProviderLimitReached
	if err.Error() != "provider connection limit reached" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

// TestNetworkPoolPipelineRespectsProviderLimit verifies pipeline respects limits.
func TestNetworkPoolPipelineRespectsProviderLimit(t *testing.T) {
	cfg := NetworkPoolConfig{
		GlobalLimit:     100,
		PerProviderMax:  2,
		UserReserved:    0.20,
		PipelineTimeout: 100 * time.Millisecond,
	}
	pool := NewNetworkPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	h1, _ := pool.AcquirePipeline(ctx, "limited", 1)
	h2, _ := pool.AcquirePipeline(ctx, "limited", 1)

	_, err := pool.AcquirePipeline(ctx, "limited", 1)
	if err != ErrProviderLimitReached {
		t.Errorf("expected ErrProviderLimitReached, got %v", err)
	}

	pool.Release(h1)
	pool.Release(h2)
}
