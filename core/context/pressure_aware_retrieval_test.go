package context

import (
	"testing"

	"github.com/adalundhe/sylk/core/resources"
)

// =============================================================================
// Test Helpers
// =============================================================================

func createTestPAR(t *testing.T) *PressureAwareRetrieval {
	t.Helper()

	hotCache := NewHotCache(HotCacheConfig{
		MaxSize: 10000,
	})

	bleve := NewMockBleveSearcher()
	searcher := NewTieredSearcher(TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
	})

	prefetcher := NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		Searcher: searcher,
	})

	return NewPressureAwareRetrieval(PressureAwareRetrievalConfig{
		HotCache:         hotCache,
		Searcher:         searcher,
		Prefetcher:       prefetcher,
		InitialCacheSize: 10000,
	})
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewPressureAwareRetrieval_DefaultConfig(t *testing.T) {
	par := NewPressureAwareRetrieval(PressureAwareRetrievalConfig{})

	if par.CurrentLevel() != resources.PressureNormal {
		t.Errorf("CurrentLevel = %v, want PressureNormal", par.CurrentLevel())
	}
}

func TestNewPressureAwareRetrieval_WithComponents(t *testing.T) {
	par := createTestPAR(t)

	if par.hotCache == nil {
		t.Error("hotCache should not be nil")
	}

	if par.searcher == nil {
		t.Error("searcher should not be nil")
	}

	if par.prefetcher == nil {
		t.Error("prefetcher should not be nil")
	}

	if par.initialCacheSize != 10000 {
		t.Errorf("initialCacheSize = %d, want 10000", par.initialCacheSize)
	}
}

func TestNewPressureAwareRetrieval_InheritsCacheSize(t *testing.T) {
	hotCache := NewHotCache(HotCacheConfig{
		MaxSize: 5000,
	})

	par := NewPressureAwareRetrieval(PressureAwareRetrievalConfig{
		HotCache: hotCache,
		// InitialCacheSize not set - should inherit from HotCache
	})

	if par.initialCacheSize != 5000 {
		t.Errorf("initialCacheSize = %d, want 5000 (inherited)", par.initialCacheSize)
	}
}

// =============================================================================
// Pressure Transition Tests
// =============================================================================

func TestOnPressureChange_NormalToElevated(t *testing.T) {
	par := createTestPAR(t)

	par.OnPressureChange(int32(resources.PressureElevated))

	if par.CurrentLevel() != resources.PressureElevated {
		t.Errorf("CurrentLevel = %v, want PressureElevated", par.CurrentLevel())
	}

	// Prefetch should still be enabled
	if !par.IsPrefetchEnabled() {
		t.Error("Prefetch should be enabled at Elevated")
	}

	// Cache should be reduced to 75%
	expectedSize := int64(float64(10000) * CacheSizeElevated)
	if par.CurrentCacheSize() != expectedSize {
		t.Errorf("CacheSize = %d, want %d", par.CurrentCacheSize(), expectedSize)
	}
}

func TestOnPressureChange_NormalToHigh(t *testing.T) {
	par := createTestPAR(t)

	par.OnPressureChange(int32(resources.PressureHigh))

	if par.CurrentLevel() != resources.PressureHigh {
		t.Errorf("CurrentLevel = %v, want PressureHigh", par.CurrentLevel())
	}

	// Prefetch should be disabled
	if par.IsPrefetchEnabled() {
		t.Error("Prefetch should be disabled at High")
	}

	// Search should be limited to hot only
	if par.CurrentMaxTier() != TierHotCache {
		t.Errorf("MaxTier = %v, want TierHotCache", par.CurrentMaxTier())
	}

	// Cache should be at 50%
	expectedSize := int64(float64(10000) * CacheSizeHigh)
	if par.CurrentCacheSize() != expectedSize {
		t.Errorf("CacheSize = %d, want %d", par.CurrentCacheSize(), expectedSize)
	}
}

func TestOnPressureChange_NormalToCritical(t *testing.T) {
	par := createTestPAR(t)

	par.OnPressureChange(int32(resources.PressureCritical))

	if par.CurrentLevel() != resources.PressureCritical {
		t.Errorf("CurrentLevel = %v, want PressureCritical", par.CurrentLevel())
	}

	// Prefetch should be disabled
	if par.IsPrefetchEnabled() {
		t.Error("Prefetch should be disabled at Critical")
	}

	// Search should be limited to hot only
	if par.CurrentMaxTier() != TierHotCache {
		t.Errorf("MaxTier = %v, want TierHotCache", par.CurrentMaxTier())
	}

	// Cache should be at 25%
	expectedSize := int64(float64(10000) * CacheSizeCritical)
	if par.CurrentCacheSize() != expectedSize {
		t.Errorf("CacheSize = %d, want %d", par.CurrentCacheSize(), expectedSize)
	}
}

func TestOnPressureChange_CriticalToNormal(t *testing.T) {
	par := createTestPAR(t)

	// First go to critical
	par.OnPressureChange(int32(resources.PressureCritical))

	// Then back to normal
	par.OnPressureChange(int32(resources.PressureNormal))

	if par.CurrentLevel() != resources.PressureNormal {
		t.Errorf("CurrentLevel = %v, want PressureNormal", par.CurrentLevel())
	}

	// Prefetch should be re-enabled
	if !par.IsPrefetchEnabled() {
		t.Error("Prefetch should be enabled at Normal")
	}

	// Full search should be enabled
	if par.CurrentMaxTier() != TierFullSearch {
		t.Errorf("MaxTier = %v, want TierFullSearch", par.CurrentMaxTier())
	}

	// Cache should be back to 100%
	if par.CurrentCacheSize() != 10000 {
		t.Errorf("CacheSize = %d, want 10000", par.CurrentCacheSize())
	}
}

func TestOnPressureChange_SameLevelNoOp(t *testing.T) {
	par := createTestPAR(t)

	// Set to elevated
	par.OnPressureChange(int32(resources.PressureElevated))
	initialSize := par.CurrentCacheSize()

	// Call again with same level - should be no-op
	par.OnPressureChange(int32(resources.PressureElevated))

	// Size should not change
	if par.CurrentCacheSize() != initialSize {
		t.Errorf("CacheSize changed on same-level transition: %d -> %d",
			initialSize, par.CurrentCacheSize())
	}
}

// =============================================================================
// Component Control Tests
// =============================================================================

func TestSetPrefetchEnabled_NilPrefetcher(t *testing.T) {
	par := NewPressureAwareRetrieval(PressureAwareRetrievalConfig{})

	// Should not panic with nil prefetcher
	par.OnPressureChange(int32(resources.PressureHigh))

	if par.IsPrefetchEnabled() {
		t.Error("IsPrefetchEnabled should return false with nil prefetcher")
	}
}

func TestSetSearcherMaxTier_NilSearcher(t *testing.T) {
	par := NewPressureAwareRetrieval(PressureAwareRetrievalConfig{})

	// Should not panic with nil searcher
	par.OnPressureChange(int32(resources.PressureHigh))

	if par.CurrentMaxTier() != TierNone {
		t.Errorf("CurrentMaxTier = %v, want TierNone with nil searcher", par.CurrentMaxTier())
	}
}

func TestSetCacheSize_NilCache(t *testing.T) {
	par := NewPressureAwareRetrieval(PressureAwareRetrievalConfig{})

	// Should not panic with nil cache
	par.OnPressureChange(int32(resources.PressureElevated))

	if par.CurrentCacheSize() != 0 {
		t.Errorf("CurrentCacheSize = %d, want 0 with nil cache", par.CurrentCacheSize())
	}
}

// =============================================================================
// Stats Tests
// =============================================================================

func TestPressureAwareRetrieval_Stats_Initial(t *testing.T) {
	par := createTestPAR(t)

	stats := par.Stats()

	if stats.CurrentLevel != resources.PressureNormal {
		t.Errorf("CurrentLevel = %v, want PressureNormal", stats.CurrentLevel)
	}

	if !stats.PrefetchEnabled {
		t.Error("PrefetchEnabled should be true initially")
	}

	if stats.MaxSearchTier != TierFullSearch {
		t.Errorf("MaxSearchTier = %v, want TierFullSearch", stats.MaxSearchTier)
	}

	if stats.CacheMaxSize != 10000 {
		t.Errorf("CacheMaxSize = %d, want 10000", stats.CacheMaxSize)
	}
}

func TestPressureAwareRetrieval_Stats_AfterPressureChange(t *testing.T) {
	par := createTestPAR(t)

	par.OnPressureChange(int32(resources.PressureHigh))

	stats := par.Stats()

	if stats.CurrentLevel != resources.PressureHigh {
		t.Errorf("CurrentLevel = %v, want PressureHigh", stats.CurrentLevel)
	}

	if stats.PrefetchEnabled {
		t.Error("PrefetchEnabled should be false at High")
	}

	if stats.MaxSearchTier != TierHotCache {
		t.Errorf("MaxSearchTier = %v, want TierHotCache", stats.MaxSearchTier)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestOnPressureChange_AllTransitions(t *testing.T) {
	par := createTestPAR(t)

	levels := []resources.PressureLevel{
		resources.PressureNormal,
		resources.PressureElevated,
		resources.PressureHigh,
		resources.PressureCritical,
		resources.PressureHigh,
		resources.PressureElevated,
		resources.PressureNormal,
	}

	for _, level := range levels {
		par.OnPressureChange(int32(level))

		if par.CurrentLevel() != level {
			t.Errorf("After transition, CurrentLevel = %v, want %v",
				par.CurrentLevel(), level)
		}
	}
}

func TestOnPressureChange_RapidTransitions(t *testing.T) {
	par := createTestPAR(t)

	// Rapid transitions should not cause issues
	for i := 0; i < 100; i++ {
		level := resources.PressureLevel(i % 4)
		par.OnPressureChange(int32(level))
	}

	// Should end at level 3 (99 % 4 = 3 = Critical)
	if par.CurrentLevel() != resources.PressureCritical {
		t.Errorf("Final level = %v, want PressureCritical", par.CurrentLevel())
	}
}

// =============================================================================
// Interface Compliance Tests
// =============================================================================

func TestPressureAwareRetrieval_ImplementsGoroutineBudgetIntegration(t *testing.T) {
	par := createTestPAR(t)

	// This should compile if PressureAwareRetrieval implements the interface
	var _ resources.GoroutineBudgetIntegration = par

	// Verify the method works
	par.OnPressureChange(int32(resources.PressureElevated))

	if par.CurrentLevel() != resources.PressureElevated {
		t.Error("OnPressureChange should update level")
	}
}
