// Package resource provides resource management integration for the Sylk
// Document Search System. This file implements pressure-aware search that
// responds to memory pressure by degrading gracefully.
package resource

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/coordinator"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// DefaultNormalConcurrency is the max concurrent searches at normal pressure.
	DefaultNormalConcurrency = 8

	// DefaultHighConcurrency is the max concurrent searches at high pressure.
	DefaultHighConcurrency = 4

	// DefaultCriticalConcurrency is the max concurrent searches at critical pressure.
	DefaultCriticalConcurrency = 2

	// DefaultSearchTimeout is the default timeout for search operations.
	DefaultSearchTimeout = 5 * time.Second

	// DefaultDegradedTimeout is the reduced timeout during high pressure.
	DefaultDegradedTimeout = 2 * time.Second
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrSearchPaused indicates search is paused due to critical pressure.
	ErrSearchPaused = errors.New("search paused: critical memory pressure")

	// ErrIndexingPaused indicates indexing is paused due to critical pressure.
	ErrIndexingPaused = errors.New("indexing paused: critical memory pressure")

	// ErrNilSearcher indicates a nil searcher was provided.
	ErrNilSearcher = errors.New("searcher cannot be nil")

	// ErrNilIndexer indicates a nil indexer was provided.
	ErrNilIndexer = errors.New("indexer cannot be nil")
)

// =============================================================================
// DegradationLevel
// =============================================================================

// DegradationLevel represents the current search degradation state.
type DegradationLevel int

const (
	// DegradationNone indicates full functionality with no degradation.
	DegradationNone DegradationLevel = iota

	// DegradationReduced indicates reduced concurrency and prefer cache.
	DegradationReduced

	// DegradationMinimal indicates minimal functionality, cache-only preferred.
	DegradationMinimal

	// DegradationPaused indicates search is paused for indexing operations.
	DegradationPaused
)

// String returns the string representation of the degradation level.
func (d DegradationLevel) String() string {
	switch d {
	case DegradationNone:
		return "none"
	case DegradationReduced:
		return "reduced"
	case DegradationMinimal:
		return "minimal"
	case DegradationPaused:
		return "paused"
	default:
		return "unknown"
	}
}

// =============================================================================
// Searcher Interface
// =============================================================================

// Searcher defines the interface for search operations.
type Searcher interface {
	Search(ctx context.Context, req *coordinator.HybridSearchRequest) (*coordinator.HybridSearchResult, error)
	IsClosed() bool
}

// =============================================================================
// Indexer Interface
// =============================================================================

// Indexer defines the interface for indexing operations.
type Indexer interface {
	Index(ctx context.Context, doc *search.Document) error
	IndexBatch(ctx context.Context, docs []*search.Document) error
}

// =============================================================================
// CacheProvider Interface
// =============================================================================

// CacheProvider defines the interface for cache-backed search.
type CacheProvider interface {
	Get(key string) (*coordinator.HybridSearchResult, bool)
	Set(key string, result *coordinator.HybridSearchResult)
}

// =============================================================================
// PressureSearchConfig
// =============================================================================

// PressureSearchConfig configures the pressure-aware search controller.
type PressureSearchConfig struct {
	// NormalConcurrency is max concurrent searches at normal pressure.
	NormalConcurrency int

	// HighConcurrency is max concurrent searches at high pressure.
	HighConcurrency int

	// CriticalConcurrency is max concurrent searches at critical pressure.
	CriticalConcurrency int

	// SearchTimeout is the default search timeout.
	SearchTimeout time.Duration

	// DegradedTimeout is the reduced timeout during high pressure.
	DegradedTimeout time.Duration

	// PreferCacheAtHigh enables cache-first at high pressure.
	PreferCacheAtHigh bool

	// PauseIndexingAtCritical pauses indexing at critical pressure.
	PauseIndexingAtCritical bool
}

// DefaultPressureSearchConfig returns sensible defaults.
func DefaultPressureSearchConfig() PressureSearchConfig {
	return PressureSearchConfig{
		NormalConcurrency:       DefaultNormalConcurrency,
		HighConcurrency:         DefaultHighConcurrency,
		CriticalConcurrency:     DefaultCriticalConcurrency,
		SearchTimeout:           DefaultSearchTimeout,
		DegradedTimeout:         DefaultDegradedTimeout,
		PreferCacheAtHigh:       true,
		PauseIndexingAtCritical: true,
	}
}

// =============================================================================
// PressureSearchController
// =============================================================================

// PressureSearchController manages search operations under memory pressure.
// It reduces concurrency, prefers cache, and pauses indexing as needed.
type PressureSearchController struct {
	config   PressureSearchConfig
	searcher Searcher
	indexer  Indexer
	cache    CacheProvider

	// Pressure state
	pressureLevel atomic.Int32
	degradation   atomic.Int32

	// Concurrency control
	semaphore chan struct{}
	semMu     sync.RWMutex

	// Subscription management
	unsubscribe func()
	mu          sync.RWMutex
	closed      bool
}

// NewPressureSearchController creates a new pressure-aware search controller.
func NewPressureSearchController(
	config PressureSearchConfig,
	searcher Searcher,
) (*PressureSearchController, error) {
	if searcher == nil {
		return nil, ErrNilSearcher
	}

	return newController(config, searcher, nil, nil), nil
}

// NewPressureSearchControllerWithCache creates a controller with cache support.
func NewPressureSearchControllerWithCache(
	config PressureSearchConfig,
	searcher Searcher,
	cache CacheProvider,
) (*PressureSearchController, error) {
	if searcher == nil {
		return nil, ErrNilSearcher
	}

	return newController(config, searcher, nil, cache), nil
}

// NewPressureSearchControllerFull creates a controller with all dependencies.
func NewPressureSearchControllerFull(
	config PressureSearchConfig,
	searcher Searcher,
	indexer Indexer,
	cache CacheProvider,
) (*PressureSearchController, error) {
	if searcher == nil {
		return nil, ErrNilSearcher
	}

	return newController(config, searcher, indexer, cache), nil
}

// newController creates a controller with the given dependencies.
func newController(
	config PressureSearchConfig,
	searcher Searcher,
	indexer Indexer,
	cache CacheProvider,
) *PressureSearchController {
	applyConfigDefaults(&config)

	return &PressureSearchController{
		config:    config,
		searcher:  searcher,
		indexer:   indexer,
		cache:     cache,
		semaphore: make(chan struct{}, config.NormalConcurrency),
	}
}

// applyConfigDefaults sets default values for zero-valued config fields.
func applyConfigDefaults(config *PressureSearchConfig) {
	if config.NormalConcurrency <= 0 {
		config.NormalConcurrency = DefaultNormalConcurrency
	}
	if config.HighConcurrency <= 0 {
		config.HighConcurrency = DefaultHighConcurrency
	}
	if config.CriticalConcurrency <= 0 {
		config.CriticalConcurrency = DefaultCriticalConcurrency
	}
	if config.SearchTimeout <= 0 {
		config.SearchTimeout = DefaultSearchTimeout
	}
	if config.DegradedTimeout <= 0 {
		config.DegradedTimeout = DefaultDegradedTimeout
	}
}

// =============================================================================
// Pressure Monitoring
// =============================================================================

// RegisterWithPressureController subscribes to pressure level changes.
func (c *PressureSearchController) RegisterWithPressureController(pc *resources.PressureController) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	// Set initial level
	c.handlePressureChange(pc.CurrentLevel())
}

// OnPressureChange handles pressure level transitions.
func (c *PressureSearchController) OnPressureChange(level resources.PressureLevel) {
	c.handlePressureChange(level)
}

// handlePressureChange updates internal state based on pressure level.
func (c *PressureSearchController) handlePressureChange(level resources.PressureLevel) {
	c.pressureLevel.Store(int32(level))
	c.updateDegradation(level)
	c.adjustConcurrency(level)
}

// updateDegradation maps pressure level to degradation level.
func (c *PressureSearchController) updateDegradation(level resources.PressureLevel) {
	var degradation DegradationLevel

	switch level {
	case resources.PressureNormal, resources.PressureElevated:
		degradation = DegradationNone
	case resources.PressureHigh:
		degradation = DegradationReduced
	case resources.PressureCritical:
		if c.config.PauseIndexingAtCritical {
			degradation = DegradationPaused
		} else {
			degradation = DegradationMinimal
		}
	}

	c.degradation.Store(int32(degradation))
}

// adjustConcurrency updates the semaphore capacity based on pressure.
func (c *PressureSearchController) adjustConcurrency(level resources.PressureLevel) {
	newConcurrency := c.concurrencyForLevel(level)

	c.semMu.Lock()
	defer c.semMu.Unlock()

	// Create new semaphore with adjusted capacity
	c.semaphore = make(chan struct{}, newConcurrency)
}

// concurrencyForLevel returns the appropriate concurrency for the level.
func (c *PressureSearchController) concurrencyForLevel(level resources.PressureLevel) int {
	switch level {
	case resources.PressureNormal, resources.PressureElevated:
		return c.config.NormalConcurrency
	case resources.PressureHigh:
		return c.config.HighConcurrency
	case resources.PressureCritical:
		return c.config.CriticalConcurrency
	default:
		return c.config.NormalConcurrency
	}
}

// =============================================================================
// Search Operations
// =============================================================================

// Search performs a search with pressure-aware degradation.
func (c *PressureSearchController) Search(
	ctx context.Context,
	req *coordinator.HybridSearchRequest,
) (*coordinator.HybridSearchResult, error) {
	if err := c.checkSearchable(); err != nil {
		return nil, err
	}

	return c.executeSearch(ctx, req)
}

// SearchWithDegradation performs a search and returns the degradation level used.
func (c *PressureSearchController) SearchWithDegradation(
	ctx context.Context,
	req *coordinator.HybridSearchRequest,
	pressure resources.PressureLevel,
) (*coordinator.HybridSearchResult, DegradationLevel, error) {
	if err := c.checkSearchable(); err != nil {
		return nil, c.CurrentDegradation(), err
	}

	// Apply specific pressure level for this search
	c.handlePressureChange(pressure)

	result, err := c.executeSearch(ctx, req)
	return result, c.CurrentDegradation(), err
}

// checkSearchable verifies the controller is ready for searches.
func (c *PressureSearchController) checkSearchable() error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return ErrSearchPaused
	}

	if c.searcher.IsClosed() {
		return ErrSearchPaused
	}

	return nil
}

// executeSearch runs the search with appropriate degradation.
func (c *PressureSearchController) executeSearch(
	ctx context.Context,
	req *coordinator.HybridSearchRequest,
) (*coordinator.HybridSearchResult, error) {
	degradation := c.CurrentDegradation()

	// Try cache first at high pressure
	if result := c.tryCache(req, degradation); result != nil {
		return result, nil
	}

	// Acquire semaphore for concurrency control
	if err := c.acquireSemaphore(ctx); err != nil {
		return nil, err
	}
	defer c.releaseSemaphore()

	// Apply timeout based on degradation
	searchCtx := c.applyTimeout(ctx, degradation)

	result, err := c.searcher.Search(searchCtx, req)
	if err != nil {
		return nil, err
	}

	// Cache result for future use
	c.cacheResult(req, result)

	return result, nil
}

// tryCache attempts to get result from cache if appropriate.
func (c *PressureSearchController) tryCache(
	req *coordinator.HybridSearchRequest,
	degradation DegradationLevel,
) *coordinator.HybridSearchResult {
	if c.cache == nil {
		return nil
	}

	// Only prefer cache at reduced degradation or higher
	if !c.config.PreferCacheAtHigh || degradation < DegradationReduced {
		return nil
	}

	cacheKey := c.buildCacheKey(req)
	result, ok := c.cache.Get(cacheKey)
	if ok {
		return result
	}

	return nil
}

// buildCacheKey creates a cache key from the request.
func (c *PressureSearchController) buildCacheKey(req *coordinator.HybridSearchRequest) string {
	return req.Query
}

// cacheResult stores the result in cache.
func (c *PressureSearchController) cacheResult(
	req *coordinator.HybridSearchRequest,
	result *coordinator.HybridSearchResult,
) {
	if c.cache == nil {
		return
	}

	cacheKey := c.buildCacheKey(req)
	c.cache.Set(cacheKey, result)
}

// applyTimeout returns a context with appropriate timeout for degradation.
func (c *PressureSearchController) applyTimeout(
	ctx context.Context,
	degradation DegradationLevel,
) context.Context {
	timeout := c.config.SearchTimeout

	if degradation >= DegradationReduced {
		timeout = c.config.DegradedTimeout
	}

	ctx, _ = context.WithTimeout(ctx, timeout)
	return ctx
}

// acquireSemaphore acquires a slot from the concurrency semaphore.
func (c *PressureSearchController) acquireSemaphore(ctx context.Context) error {
	c.semMu.RLock()
	sem := c.semaphore
	c.semMu.RUnlock()

	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseSemaphore releases a slot back to the semaphore.
func (c *PressureSearchController) releaseSemaphore() {
	c.semMu.RLock()
	sem := c.semaphore
	c.semMu.RUnlock()

	select {
	case <-sem:
	default:
	}
}

// =============================================================================
// Indexing Operations
// =============================================================================

// Index indexes a document with pressure awareness.
func (c *PressureSearchController) Index(
	ctx context.Context,
	doc *search.Document,
) error {
	if c.indexer == nil {
		return ErrNilIndexer
	}

	if err := c.checkIndexable(); err != nil {
		return err
	}

	return c.indexer.Index(ctx, doc)
}

// IndexBatch indexes multiple documents with pressure awareness.
func (c *PressureSearchController) IndexBatch(
	ctx context.Context,
	docs []*search.Document,
) error {
	if c.indexer == nil {
		return ErrNilIndexer
	}

	if err := c.checkIndexable(); err != nil {
		return err
	}

	return c.indexer.IndexBatch(ctx, docs)
}

// checkIndexable verifies indexing is allowed at current pressure.
func (c *PressureSearchController) checkIndexable() error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return ErrIndexingPaused
	}

	if c.CurrentDegradation() == DegradationPaused {
		return ErrIndexingPaused
	}

	return nil
}

// =============================================================================
// State Accessors
// =============================================================================

// CurrentPressure returns the current pressure level.
func (c *PressureSearchController) CurrentPressure() resources.PressureLevel {
	return resources.PressureLevel(c.pressureLevel.Load())
}

// CurrentDegradation returns the current degradation level.
func (c *PressureSearchController) CurrentDegradation() DegradationLevel {
	return DegradationLevel(c.degradation.Load())
}

// IsIndexingPaused returns true if indexing is paused due to pressure.
func (c *PressureSearchController) IsIndexingPaused() bool {
	return c.CurrentDegradation() == DegradationPaused
}

// CurrentConcurrency returns the current concurrency limit.
func (c *PressureSearchController) CurrentConcurrency() int {
	return c.concurrencyForLevel(c.CurrentPressure())
}

// =============================================================================
// Lifecycle
// =============================================================================

// Close shuts down the pressure search controller.
func (c *PressureSearchController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	if c.unsubscribe != nil {
		c.unsubscribe()
		c.unsubscribe = nil
	}

	return nil
}

// IsClosed returns true if the controller has been closed.
func (c *PressureSearchController) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}
