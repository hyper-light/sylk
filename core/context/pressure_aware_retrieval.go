// Package context provides AR.6.1: PressureAwareRetrieval - coordinates all adaptive
// retrieval components in response to system-wide memory pressure.
package context

import (
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/resources"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// Cache size percentages for each pressure level
	CacheSizeNormal   = 1.0  // 100% cache at normal
	CacheSizeElevated = 0.75 // 75% cache at elevated
	CacheSizeHigh     = 0.50 // 50% cache at high
	CacheSizeCritical = 0.25 // 25% cache at critical

	// Eviction percentages when transitioning up
	EvictionNone     = 0.0
	EvictionModerate = 0.25
	EvictionHeavy    = 0.50
)

// =============================================================================
// Configuration
// =============================================================================

// PressureAwareRetrievalConfig holds configuration for PressureAwareRetrieval.
type PressureAwareRetrievalConfig struct {
	// HotCache is the hot cache to manage
	HotCache *HotCache

	// Searcher is the tiered searcher to manage
	Searcher *TieredSearcher

	// Prefetcher is the speculative prefetcher to manage
	Prefetcher *SpeculativePrefetcher

	// PressureController is the system pressure controller to register with
	PressureController *resources.PressureController

	// InitialCacheSize is the initial max size for the hot cache (in entries)
	InitialCacheSize int
}

// =============================================================================
// PressureAwareRetrieval
// =============================================================================

// PressureAwareRetrieval coordinates adaptive retrieval components in response
// to memory pressure. It implements graceful degradation as pressure increases.
type PressureAwareRetrieval struct {
	hotCache   *HotCache
	searcher   *TieredSearcher
	prefetcher *SpeculativePrefetcher

	initialCacheSize int
	currentLevel     atomic.Int32

	mu sync.RWMutex
}

// NewPressureAwareRetrieval creates a new pressure-aware retrieval coordinator.
func NewPressureAwareRetrieval(config PressureAwareRetrievalConfig) *PressureAwareRetrieval {
	par := &PressureAwareRetrieval{
		hotCache:         config.HotCache,
		searcher:         config.Searcher,
		prefetcher:       config.Prefetcher,
		initialCacheSize: config.InitialCacheSize,
	}

	par.currentLevel.Store(int32(resources.PressureNormal))

	// Register hot cache with pressure controller if both available
	if config.PressureController != nil && config.HotCache != nil {
		config.PressureController.RegisterCache(config.HotCache)
	}

	return par
}

// =============================================================================
// Pressure Response
// =============================================================================

// OnPressureChange handles pressure level transitions.
// Implements resources.GoroutineBudgetIntegration interface.
func (par *PressureAwareRetrieval) OnPressureChange(level int32) {
	oldLevel := resources.PressureLevel(par.currentLevel.Swap(level))
	newLevel := resources.PressureLevel(level)

	if oldLevel == newLevel {
		return
	}

	par.handleTransition(oldLevel, newLevel)
}

func (par *PressureAwareRetrieval) handleTransition(from, to resources.PressureLevel) {
	par.mu.Lock()
	defer par.mu.Unlock()

	switch to {
	case resources.PressureNormal:
		par.transitionToNormal()
	case resources.PressureElevated:
		par.transitionToElevated(from)
	case resources.PressureHigh:
		par.transitionToHigh(from)
	case resources.PressureCritical:
		par.transitionToCritical(from)
	}
}

func (par *PressureAwareRetrieval) transitionToNormal() {
	// Restore full functionality
	par.setPrefetchEnabled(true)
	par.setSearcherMaxTier(TierFullSearch)
	par.setCacheSize(CacheSizeNormal)
}

func (par *PressureAwareRetrieval) transitionToElevated(from resources.PressureLevel) {
	// Reduce cache, but continue prefetch
	par.setPrefetchEnabled(true)
	par.setSearcherMaxTier(TierFullSearch)
	par.setCacheSize(CacheSizeElevated)

	if from == resources.PressureNormal {
		par.evictCache(EvictionModerate)
	}
}

func (par *PressureAwareRetrieval) transitionToHigh(from resources.PressureLevel) {
	// Disable prefetch, hot-only search
	par.setPrefetchEnabled(false)
	par.setSearcherMaxTier(TierHotCache)
	par.setCacheSize(CacheSizeHigh)

	if from < resources.PressureHigh {
		par.evictCache(EvictionModerate)
	}
}

func (par *PressureAwareRetrieval) transitionToCritical(from resources.PressureLevel) {
	// Emergency mode: minimal cache, no prefetch, hot-only
	par.setPrefetchEnabled(false)
	par.setSearcherMaxTier(TierHotCache)
	par.setCacheSize(CacheSizeCritical)

	// Heavy eviction on transition to critical
	if from < resources.PressureCritical {
		par.evictCache(EvictionHeavy)
	}

	// Cancel all in-flight prefetches
	par.cancelPrefetches()
}

// =============================================================================
// Component Control
// =============================================================================

func (par *PressureAwareRetrieval) setPrefetchEnabled(enabled bool) {
	if par.prefetcher != nil {
		par.prefetcher.SetEnabled(enabled)
	}
}

func (par *PressureAwareRetrieval) setSearcherMaxTier(tier SearchTier) {
	if par.searcher != nil {
		par.searcher.SetMaxTier(tier)
	}
}

func (par *PressureAwareRetrieval) setCacheSize(factor float64) {
	if par.hotCache != nil && par.initialCacheSize > 0 {
		newSize := int(float64(par.initialCacheSize) * factor)
		if newSize < 1 {
			newSize = 1
		}
		par.hotCache.SetMaxSize(newSize)
	}
}

func (par *PressureAwareRetrieval) evictCache(percent float64) {
	if par.hotCache != nil && percent > 0 {
		par.hotCache.EvictPercent(percent)
	}
}

func (par *PressureAwareRetrieval) cancelPrefetches() {
	if par.prefetcher != nil {
		par.prefetcher.CancelAll()
	}
}

// =============================================================================
// Accessors
// =============================================================================

// CurrentLevel returns the current pressure level.
func (par *PressureAwareRetrieval) CurrentLevel() resources.PressureLevel {
	return resources.PressureLevel(par.currentLevel.Load())
}

// IsPrefetchEnabled returns whether prefetch is currently enabled.
func (par *PressureAwareRetrieval) IsPrefetchEnabled() bool {
	if par.prefetcher == nil {
		return false
	}
	return par.prefetcher.IsEnabled()
}

// CurrentMaxTier returns the current max search tier.
func (par *PressureAwareRetrieval) CurrentMaxTier() SearchTier {
	if par.searcher == nil {
		return TierNone
	}
	return par.searcher.MaxTier()
}

// CurrentCacheSize returns the current cache max size.
func (par *PressureAwareRetrieval) CurrentCacheSize() int {
	if par.hotCache == nil {
		return 0
	}
	return par.hotCache.MaxSize()
}

// =============================================================================
// Statistics
// =============================================================================

// PressureRetrievalStats holds statistics for the pressure-aware retrieval.
type PressureRetrievalStats struct {
	CurrentLevel     resources.PressureLevel
	PrefetchEnabled  bool
	MaxSearchTier    SearchTier
	CacheMaxSize     int
	CacheCurrentSize int
}

// Stats returns current statistics.
func (par *PressureAwareRetrieval) Stats() PressureRetrievalStats {
	par.mu.RLock()
	defer par.mu.RUnlock()

	stats := PressureRetrievalStats{
		CurrentLevel:    par.CurrentLevel(),
		PrefetchEnabled: par.IsPrefetchEnabled(),
		MaxSearchTier:   par.CurrentMaxTier(),
		CacheMaxSize:    par.CurrentCacheSize(),
	}

	if par.hotCache != nil {
		stats.CacheCurrentSize = par.hotCache.Size()
	}

	return stats
}
