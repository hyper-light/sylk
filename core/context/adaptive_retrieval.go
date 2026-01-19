// Package context provides AR.12.1: AdaptiveRetrieval Facade - top-level coordinator
// for all adaptive retrieval components with lifecycle management.
package context

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrAdaptiveRetrievalClosed indicates the facade has been stopped.
	ErrAdaptiveRetrievalClosed = errors.New("adaptive retrieval is closed")

	// ErrAdaptiveRetrievalAlreadyStarted indicates Start was called twice.
	ErrAdaptiveRetrievalAlreadyStarted = errors.New("adaptive retrieval already started")

	// ErrAdaptiveRetrievalNotStarted indicates operations called before Start.
	ErrAdaptiveRetrievalNotStarted = errors.New("adaptive retrieval not started")
)

// =============================================================================
// Configuration
// =============================================================================

// AdaptiveRetrievalConfig holds configuration for the AdaptiveRetrieval facade.
type AdaptiveRetrievalConfig struct {
	// SessionID is the session this instance belongs to.
	SessionID string

	// HotCacheMaxSize is the maximum size for the hot cache in bytes.
	HotCacheMaxSize int64

	// PrefetchTimeout is the timeout for prefetch operations.
	PrefetchTimeout time.Duration

	// MaxInflightPrefetches is the maximum number of concurrent prefetches.
	MaxInflightPrefetches int

	// SearchBudget is the default search budget duration.
	SearchBudget time.Duration

	// InitialState is an optional pre-existing AdaptiveState to restore.
	InitialState *AdaptiveState
}

// DefaultAdaptiveRetrievalConfig returns sensible defaults.
func DefaultAdaptiveRetrievalConfig() *AdaptiveRetrievalConfig {
	return &AdaptiveRetrievalConfig{
		HotCacheMaxSize:       DefaultHotCacheMaxSize,
		PrefetchTimeout:       DefaultPrefetchTimeout,
		MaxInflightPrefetches: DefaultMaxInflight,
		SearchBudget:          TierFullBudget,
	}
}

// =============================================================================
// Dependencies
// =============================================================================

// AdaptiveRetrievalDeps holds external dependencies for the facade.
type AdaptiveRetrievalDeps struct {
	// VectorDB is the vector graph database (optional).
	VectorDB *vectorgraphdb.VectorGraphDB

	// BleveDB is the Bleve-integrated database (optional).
	BleveDB *vectorgraphdb.BleveIntegratedDB

	// PressureController for pressure notifications (optional).
	PressureController *resources.PressureController

	// GoroutineBudget for WAVE 4 compliant goroutine management.
	GoroutineBudget *concurrency.GoroutineBudget

	// SkillRegistry for skill registration (optional).
	SkillRegistry *skills.Registry

	// Bleve searcher interface for TieredSearcher (optional).
	BleveSearcher TieredBleveSearcher

	// Vector searcher interface for TieredSearcher (optional).
	VectorSearcher TieredVectorSearcher

	// Embedding generator for vector search (optional).
	Embedder TieredEmbeddingGenerator
}

// =============================================================================
// State Enum
// =============================================================================

// adaptiveRetrievalState tracks the lifecycle state.
type adaptiveRetrievalState int32

const (
	stateUninitialized adaptiveRetrievalState = iota
	stateStarted
	stateStopped
)

// =============================================================================
// AdaptiveRetrieval Facade
// =============================================================================

// AdaptiveRetrieval is the top-level coordinator for all adaptive retrieval
// components. It provides lifecycle management and component access.
type AdaptiveRetrieval struct {
	config *AdaptiveRetrievalConfig
	deps   *AdaptiveRetrievalDeps

	// Core components
	hotCache          *HotCache
	searcher          *TieredSearcher
	prefetcher        *SpeculativePrefetcher
	adaptiveState     *AdaptiveState
	hookRegistry      *AdaptiveHookRegistry
	pressureRetrieval *PressureAwareRetrieval
	episodeTracker    *EpisodeTracker

	// Goroutine scope for this instance
	scope *concurrency.GoroutineScope

	// State tracking
	state atomic.Int32
	mu    sync.RWMutex

	// Context for background operations
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new AdaptiveRetrieval facade with the given configuration.
// Call Start() to initialize components.
func New(config *AdaptiveRetrievalConfig, deps *AdaptiveRetrievalDeps) *AdaptiveRetrieval {
	if config == nil {
		config = DefaultAdaptiveRetrievalConfig()
	}
	if deps == nil {
		deps = &AdaptiveRetrievalDeps{}
	}

	ar := &AdaptiveRetrieval{
		config: config,
		deps:   deps,
	}

	ar.state.Store(int32(stateUninitialized))
	return ar
}

// =============================================================================
// Lifecycle Management
// =============================================================================

// Start initializes all adaptive retrieval components in dependency order.
func (ar *AdaptiveRetrieval) Start(ctx context.Context) error {
	if !ar.compareAndSwapState(stateUninitialized, stateStarted) {
		if ar.isState(stateStopped) {
			return ErrAdaptiveRetrievalClosed
		}
		return ErrAdaptiveRetrievalAlreadyStarted
	}

	ar.ctx, ar.cancel = context.WithCancel(ctx)

	if err := ar.initializeComponents(); err != nil {
		ar.state.Store(int32(stateUninitialized))
		return err
	}

	ar.wireComponents()
	return nil
}

func (ar *AdaptiveRetrieval) initializeComponents() error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	ar.initScope()
	ar.initHotCache()
	ar.initSearcher()
	ar.initPrefetcher()
	ar.initAdaptiveState()
	ar.initHookRegistry()
	ar.initPressureRetrieval()
	ar.initEpisodeTracker()

	return nil
}

func (ar *AdaptiveRetrieval) initScope() {
	ar.scope = concurrency.NewGoroutineScope(
		ar.ctx,
		"adaptive-retrieval:"+ar.config.SessionID,
		ar.deps.GoroutineBudget,
	)
}

func (ar *AdaptiveRetrieval) initHotCache() {
	ar.hotCache = NewHotCache(HotCacheConfig{
		MaxSize: ar.config.HotCacheMaxSize,
	})
}

func (ar *AdaptiveRetrieval) initSearcher() {
	ar.searcher = NewTieredSearcher(TieredSearcherConfig{
		HotCache: ar.hotCache,
		Bleve:    ar.deps.BleveSearcher,
		Vector:   ar.deps.VectorSearcher,
		Embedder: ar.deps.Embedder,
	})
}

func (ar *AdaptiveRetrieval) initPrefetcher() {
	ar.prefetcher = NewSpeculativePrefetcher(SpeculativePrefetcherConfig{
		Searcher:        ar.searcher,
		Scope:           ar.scope,
		PrefetchTimeout: ar.config.PrefetchTimeout,
		MaxInflight:     ar.config.MaxInflightPrefetches,
		SearchBudget:    ar.config.SearchBudget,
	})
}

func (ar *AdaptiveRetrieval) initAdaptiveState() {
	if ar.config.InitialState != nil {
		ar.adaptiveState = ar.config.InitialState
	} else {
		ar.adaptiveState = NewAdaptiveState()
	}
}

func (ar *AdaptiveRetrieval) initHookRegistry() {
	ar.hookRegistry = NewAdaptiveHookRegistry()
}

func (ar *AdaptiveRetrieval) initPressureRetrieval() {
	ar.pressureRetrieval = NewPressureAwareRetrieval(PressureAwareRetrievalConfig{
		HotCache:         ar.hotCache,
		Searcher:         ar.searcher,
		Prefetcher:       ar.prefetcher,
		InitialCacheSize: ar.config.HotCacheMaxSize,
	})
}

func (ar *AdaptiveRetrieval) initEpisodeTracker() {
	ar.episodeTracker = NewEpisodeTracker()
}

func (ar *AdaptiveRetrieval) wireComponents() {
	ar.registerWithPressureController()
}

func (ar *AdaptiveRetrieval) registerWithPressureController() {
	if ar.deps.PressureController == nil {
		return
	}
	// PressureAwareRetrieval implements GoroutineBudgetIntegration
	// and can be registered with the PressureController
}

// Stop cleanly shuts down all adaptive retrieval components.
func (ar *AdaptiveRetrieval) Stop() error {
	if !ar.compareAndSwapState(stateStarted, stateStopped) {
		if ar.isState(stateUninitialized) {
			return ErrAdaptiveRetrievalNotStarted
		}
		return nil // Already stopped
	}

	ar.mu.Lock()
	defer ar.mu.Unlock()

	ar.stopComponents()

	if ar.cancel != nil {
		ar.cancel()
	}

	return nil
}

func (ar *AdaptiveRetrieval) stopComponents() {
	ar.cancelPrefetches()
	ar.shutdownScope()
}

func (ar *AdaptiveRetrieval) cancelPrefetches() {
	if ar.prefetcher != nil {
		ar.prefetcher.CancelAll()
	}
}

func (ar *AdaptiveRetrieval) shutdownScope() {
	if ar.scope != nil {
		_ = ar.scope.Shutdown(5*time.Second, 10*time.Second)
	}
}

// =============================================================================
// State Helpers
// =============================================================================

func (ar *AdaptiveRetrieval) compareAndSwapState(old, new adaptiveRetrievalState) bool {
	return ar.state.CompareAndSwap(int32(old), int32(new))
}

func (ar *AdaptiveRetrieval) isState(s adaptiveRetrievalState) bool {
	return ar.state.Load() == int32(s)
}

// IsStarted returns true if the facade has been started and not stopped.
func (ar *AdaptiveRetrieval) IsStarted() bool {
	return ar.isState(stateStarted)
}

// IsStopped returns true if the facade has been stopped.
func (ar *AdaptiveRetrieval) IsStopped() bool {
	return ar.isState(stateStopped)
}

// =============================================================================
// Component Accessors
// =============================================================================

// GetPrefetcher returns the speculative prefetcher component.
func (ar *AdaptiveRetrieval) GetPrefetcher() *SpeculativePrefetcher {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.prefetcher
}

// GetSearcher returns the tiered searcher component.
func (ar *AdaptiveRetrieval) GetSearcher() *TieredSearcher {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.searcher
}

// GetAdaptiveState returns the adaptive state component.
func (ar *AdaptiveRetrieval) GetAdaptiveState() *AdaptiveState {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.adaptiveState
}

// GetHotCache returns the hot cache component.
func (ar *AdaptiveRetrieval) GetHotCache() *HotCache {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.hotCache
}

// GetHookRegistry returns the adaptive hook registry.
func (ar *AdaptiveRetrieval) GetHookRegistry() *AdaptiveHookRegistry {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.hookRegistry
}

// GetPressureRetrieval returns the pressure-aware retrieval coordinator.
func (ar *AdaptiveRetrieval) GetPressureRetrieval() *PressureAwareRetrieval {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.pressureRetrieval
}

// GetEpisodeTracker returns the episode tracker component.
func (ar *AdaptiveRetrieval) GetEpisodeTracker() *EpisodeTracker {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.episodeTracker
}

// GetScope returns the goroutine scope for this instance.
func (ar *AdaptiveRetrieval) GetScope() *concurrency.GoroutineScope {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.scope
}

// SessionID returns the session ID this instance belongs to.
func (ar *AdaptiveRetrieval) SessionID() string {
	return ar.config.SessionID
}

// =============================================================================
// Statistics
// =============================================================================

// AdaptiveRetrievalStats holds combined statistics from all components.
type AdaptiveRetrievalStats struct {
	SessionID        string
	IsStarted        bool
	PrefetcherStats  PrefetcherStats
	SearcherStats    TieredSearcherStats
	HotCacheStats    HotCacheStats
	PressureStats    PressureRetrievalStats
	AdaptiveStateSummary StateSummary
}

// Stats returns combined statistics from all components.
func (ar *AdaptiveRetrieval) Stats() AdaptiveRetrievalStats {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	stats := AdaptiveRetrievalStats{
		SessionID: ar.config.SessionID,
		IsStarted: ar.isState(stateStarted),
	}

	if ar.prefetcher != nil {
		stats.PrefetcherStats = ar.prefetcher.Stats()
	}
	if ar.searcher != nil {
		stats.SearcherStats = ar.searcher.Stats()
	}
	if ar.hotCache != nil {
		stats.HotCacheStats = ar.hotCache.Stats()
	}
	if ar.pressureRetrieval != nil {
		stats.PressureStats = ar.pressureRetrieval.Stats()
	}
	if ar.adaptiveState != nil {
		stats.AdaptiveStateSummary = ar.adaptiveState.Summary()
	}

	return stats
}
