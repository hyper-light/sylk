// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.5.3: SpeculativePrefetcher - launches context retrieval in parallel
// with user input processing for latency hiding.
package context

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrPrefetchDisabled indicates prefetching is disabled (under pressure).
	ErrPrefetchDisabled = errors.New("speculative prefetch is disabled")

	// ErrPrefetchBudgetExhausted indicates the prefetch budget is exhausted.
	ErrPrefetchBudgetExhausted = errors.New("prefetch budget exhausted")
)

// =============================================================================
// Constants
// =============================================================================

const (
	// DefaultMaxInflight is the default maximum number of in-flight prefetches.
	DefaultMaxInflight = 10

	// CleanupInterval is how often to clean up completed futures.
	CleanupInterval = 30 * time.Second
)

// =============================================================================
// TrackedPrefetchFuture
// =============================================================================

// TrackedPrefetchFuture represents an in-flight prefetch operation.
type TrackedPrefetchFuture struct {
	result    atomic.Pointer[AugmentedQuery]
	err       atomic.Pointer[error]
	done      chan struct{}
	closeOnce sync.Once
	started   time.Time
	query     string
	hash      string
}

// newTrackedPrefetchFuture creates a new prefetch future.
func newTrackedPrefetchFuture(query, hash string) *TrackedPrefetchFuture {
	return &TrackedPrefetchFuture{
		done:    make(chan struct{}),
		started: time.Now(),
		query:   query,
		hash:    hash,
	}
}

// complete marks the future as complete with results.
func (f *TrackedPrefetchFuture) complete(result *AugmentedQuery, err error) {
	if result != nil {
		f.result.Store(result)
	}
	if err != nil {
		f.err.Store(&err)
	}
	f.closeOnce.Do(func() { close(f.done) })
}

// IsDone returns true if the prefetch is complete.
func (f *TrackedPrefetchFuture) IsDone() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// GetIfReady returns results if ready within timeout, nil otherwise.
func (f *TrackedPrefetchFuture) GetIfReady(timeout time.Duration) *AugmentedQuery {
	select {
	case <-f.done:
		return f.result.Load()
	case <-time.After(timeout):
		return nil
	}
}

// Wait blocks until the future completes and returns the result.
func (f *TrackedPrefetchFuture) Wait() (*AugmentedQuery, error) {
	<-f.done
	result := f.result.Load()
	errPtr := f.err.Load()
	if errPtr != nil {
		return result, *errPtr
	}
	return result, nil
}

// Err returns any error from the prefetch.
func (f *TrackedPrefetchFuture) Err() error {
	errPtr := f.err.Load()
	if errPtr != nil {
		return *errPtr
	}
	return nil
}

// Query returns the original query string.
func (f *TrackedPrefetchFuture) Query() string {
	return f.query
}

// Duration returns how long the prefetch took (or has taken so far).
func (f *TrackedPrefetchFuture) Duration() time.Duration {
	return time.Since(f.started)
}

// =============================================================================
// SpeculativePrefetcher Configuration
// =============================================================================

// SpeculativePrefetcherConfig holds configuration for the prefetcher.
type SpeculativePrefetcherConfig struct {
	// Searcher is the tiered searcher for executing searches.
	Searcher *TieredSearcher

	// Scope is the goroutine scope for WAVE 4 compliance.
	Scope *concurrency.GoroutineScope

	// PrefetchTimeout is the timeout for each prefetch operation.
	PrefetchTimeout time.Duration

	// MaxInflight is the maximum number of in-flight prefetches.
	MaxInflight int

	// SearchBudget is the search budget for prefetch operations.
	SearchBudget time.Duration
}

// =============================================================================
// SpeculativePrefetcher
// =============================================================================

// SpeculativePrefetcher launches context retrieval in parallel with user input.
// When a query is received, prefetch begins speculatively. If the query matches
// prefetch, results are ready instantly; if not, the prefetch is discarded.
type SpeculativePrefetcher struct {
	searcher *TieredSearcher
	scope    *concurrency.GoroutineScope

	// inflight tracks in-flight prefetch futures by query hash
	inflight sync.Map // map[string]*TrackedPrefetchFuture

	// enabled controls whether new prefetches are allowed
	enabled atomic.Bool

	// inflightCount tracks the number of in-flight operations
	inflightCount atomic.Int64

	// cleanup worker control
	cleanupStop   chan struct{}
	cleanupDone   chan struct{}
	cleanupOnce   sync.Once
	cleanupStopMu sync.Mutex

	// config
	prefetchTimeout time.Duration
	maxInflight     int
	searchBudget    time.Duration

	// stats
	mu            sync.RWMutex
	totalStarted  int64
	totalHits     int64
	totalMisses   int64
	totalDisabled int64
}

// NewSpeculativePrefetcher creates a new speculative prefetcher.
func NewSpeculativePrefetcher(config SpeculativePrefetcherConfig) *SpeculativePrefetcher {
	timeout := config.PrefetchTimeout
	if timeout <= 0 {
		timeout = DefaultPrefetchTimeout
	}

	maxInflight := config.MaxInflight
	if maxInflight <= 0 {
		maxInflight = DefaultMaxInflight
	}

	searchBudget := config.SearchBudget
	if searchBudget <= 0 {
		searchBudget = TierFullBudget
	}

	sp := &SpeculativePrefetcher{
		searcher:        config.Searcher,
		scope:           config.Scope,
		prefetchTimeout: timeout,
		maxInflight:     maxInflight,
		searchBudget:    searchBudget,
	}

	sp.enabled.Store(true)
	return sp
}

// =============================================================================
// Core Operations
// =============================================================================

// StartSpeculative launches a background prefetch for the given query.
// Returns an error if prefetching is disabled or budget is exhausted.
func (sp *SpeculativePrefetcher) StartSpeculative(
	ctx context.Context,
	query string,
) (*TrackedPrefetchFuture, error) {
	if !sp.enabled.Load() {
		sp.recordDisabled()
		return nil, ErrPrefetchDisabled
	}

	if sp.inflightCount.Load() >= int64(sp.maxInflight) {
		return nil, ErrPrefetchBudgetExhausted
	}

	hash := sp.hashQuery(query)

	// Check for existing future
	if existing := sp.getExisting(hash); existing != nil {
		sp.recordHit()
		return existing, nil
	}

	// Create and launch new future
	return sp.launchPrefetch(ctx, query, hash)
}

// GetOrStart returns an existing future or starts a new one.
// This is the primary deduplication entry point.
func (sp *SpeculativePrefetcher) GetOrStart(
	ctx context.Context,
	query string,
) *TrackedPrefetchFuture {
	hash := sp.hashQuery(query)

	// Check for existing
	if existing := sp.getExisting(hash); existing != nil {
		sp.recordHit()
		return existing
	}

	// Try to start new prefetch
	future, err := sp.StartSpeculative(ctx, query)
	if err != nil {
		return nil
	}
	return future
}

// GetInflight returns an existing in-flight future for the query, or nil.
func (sp *SpeculativePrefetcher) GetInflight(query string) *TrackedPrefetchFuture {
	hash := sp.hashQuery(query)
	return sp.getExisting(hash)
}

func (sp *SpeculativePrefetcher) getExisting(hash string) *TrackedPrefetchFuture {
	if val, ok := sp.inflight.Load(hash); ok {
		return val.(*TrackedPrefetchFuture)
	}
	return nil
}

func (sp *SpeculativePrefetcher) launchPrefetch(
	ctx context.Context,
	query string,
	hash string,
) (*TrackedPrefetchFuture, error) {
	future := newTrackedPrefetchFuture(query, hash)

	// Store in inflight map (race: first one wins)
	actual, loaded := sp.inflight.LoadOrStore(hash, future)
	if loaded {
		// Another goroutine beat us, return their future
		sp.recordHit()
		return actual.(*TrackedPrefetchFuture), nil
	}

	sp.inflightCount.Add(1)
	sp.recordStarted()

	// Launch via GoroutineScope (WAVE 4 compliant)
	if sp.scope != nil {
		err := sp.scope.Go("prefetch:"+hash, sp.prefetchTimeout, func(workerCtx context.Context) error {
			sp.executePrefetch(workerCtx, query, hash, future)
			return nil
		})
		if err != nil {
			sp.cleanup(hash)
			return nil, err
		}
	} else {
		// Fallback for nil scope (testing)
		go sp.executePrefetch(ctx, query, hash, future)
	}

	return future, nil
}

func (sp *SpeculativePrefetcher) executePrefetch(
	ctx context.Context,
	query string,
	hash string,
	future *TrackedPrefetchFuture,
) {
	defer sp.cleanup(hash)

	if sp.searcher == nil {
		future.complete(nil, fmt.Errorf("speculative prefetch failed for query hash %s: no searcher configured", hash))
		return
	}

	result := sp.searcher.SearchWithBudget(ctx, query, sp.searchBudget)
	augmented := sp.resultsToAugmented(query, result)
	future.complete(augmented, nil)
}

func (sp *SpeculativePrefetcher) resultsToAugmented(
	query string,
	result *TieredSearchResult,
) *AugmentedQuery {
	if result == nil || !result.HasResults() {
		return &AugmentedQuery{
			OriginalQuery:    query,
			Excerpts:         nil,
			Summaries:        nil,
			TierSource:       TierNone,
			PrefetchDuration: result.TotalTime,
		}
	}

	excerpts := sp.entriesToExcerpts(result.Results)

	return &AugmentedQuery{
		OriginalQuery:    query,
		Excerpts:         excerpts,
		TierSource:       result.Tier,
		PrefetchDuration: result.TotalTime,
	}
}

func (sp *SpeculativePrefetcher) entriesToExcerpts(entries []*ContentEntry) []Excerpt {
	excerpts := make([]Excerpt, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		excerpts = append(excerpts, Excerpt{
			ID:         entry.ID,
			Content:    entry.Content,
			Source:     entry.ID,
			Confidence: 0.8, // Default confidence for prefetched content
		})
	}
	return excerpts
}

func (sp *SpeculativePrefetcher) cleanup(hash string) {
	sp.inflight.Delete(hash)
	sp.inflightCount.Add(-1)
}

// =============================================================================
// Enable/Disable Control
// =============================================================================

// SetEnabled enables or disables prefetching (for pressure response).
func (sp *SpeculativePrefetcher) SetEnabled(enabled bool) {
	sp.enabled.Store(enabled)
}

// IsEnabled returns whether prefetching is enabled.
func (sp *SpeculativePrefetcher) IsEnabled() bool {
	return sp.enabled.Load()
}

// Searcher returns the underlying tiered searcher.
// This is exposed primarily for testing purposes.
func (sp *SpeculativePrefetcher) Searcher() *TieredSearcher {
	return sp.searcher
}

// =============================================================================
// Query Hashing
// =============================================================================

func (sp *SpeculativePrefetcher) hashQuery(query string) string {
	normalized := sp.normalizeQuery(query)
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:8]) // 16 hex chars = 64 bits
}

func (sp *SpeculativePrefetcher) normalizeQuery(query string) string {
	// Simple normalization: lowercase and trim
	// Could be extended with stemming, stopword removal, etc.
	normalized := make([]byte, 0, len(query))
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		// Skip leading/trailing whitespace
		if c == ' ' || c == '\t' || c == '\n' {
			if len(normalized) > 0 && normalized[len(normalized)-1] != ' ' {
				normalized = append(normalized, ' ')
			}
			continue
		}
		normalized = append(normalized, c)
	}
	// Trim trailing space
	for len(normalized) > 0 && normalized[len(normalized)-1] == ' ' {
		normalized = normalized[:len(normalized)-1]
	}
	return string(normalized)
}

// =============================================================================
// Statistics
// =============================================================================

func (sp *SpeculativePrefetcher) recordStarted() {
	sp.mu.Lock()
	sp.totalStarted++
	sp.mu.Unlock()
}

func (sp *SpeculativePrefetcher) recordHit() {
	sp.mu.Lock()
	sp.totalHits++
	sp.mu.Unlock()
}

func (sp *SpeculativePrefetcher) recordMiss() {
	sp.mu.Lock()
	sp.totalMisses++
	sp.mu.Unlock()
}

func (sp *SpeculativePrefetcher) recordDisabled() {
	sp.mu.Lock()
	sp.totalDisabled++
	sp.mu.Unlock()
}

// PrefetcherStats holds prefetcher statistics.
type PrefetcherStats struct {
	TotalStarted  int64
	TotalHits     int64
	TotalMisses   int64
	TotalDisabled int64
	InflightCount int64
	Enabled       bool
}

// Stats returns current prefetcher statistics.
func (sp *SpeculativePrefetcher) Stats() PrefetcherStats {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	return PrefetcherStats{
		TotalStarted:  sp.totalStarted,
		TotalHits:     sp.totalHits,
		TotalMisses:   sp.totalMisses,
		TotalDisabled: sp.totalDisabled,
		InflightCount: sp.inflightCount.Load(),
		Enabled:       sp.enabled.Load(),
	}
}

// ResetStats resets statistics counters.
func (sp *SpeculativePrefetcher) ResetStats() {
	sp.mu.Lock()
	sp.totalStarted = 0
	sp.totalHits = 0
	sp.totalMisses = 0
	sp.totalDisabled = 0
	sp.mu.Unlock()
}

// =============================================================================
// Cleanup Operations
// =============================================================================

// CleanupCompleted removes completed futures from the inflight map.
// This should be called periodically or when inflight count is high.
func (sp *SpeculativePrefetcher) CleanupCompleted() int {
	var cleaned int
	sp.inflight.Range(func(key, value any) bool {
		future := value.(*TrackedPrefetchFuture)
		if future.IsDone() {
			sp.inflight.Delete(key)
			cleaned++
		}
		return true
	})
	return cleaned
}

// InflightCount returns the number of in-flight prefetches.
func (sp *SpeculativePrefetcher) InflightCount() int64 {
	return sp.inflightCount.Load()
}

// MapSize returns the actual number of entries in the inflight map.
// This is useful for testing to verify cleanup is working correctly.
func (sp *SpeculativePrefetcher) MapSize() int {
	var count int
	sp.inflight.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// CancelAll cancels all in-flight prefetches.
// Used during shutdown or pressure response.
func (sp *SpeculativePrefetcher) CancelAll() {
	sp.inflight.Range(func(key, value any) bool {
		future := value.(*TrackedPrefetchFuture)
		err := context.Canceled
		future.err.Store(&err)
		future.closeOnce.Do(func() { close(future.done) })
		sp.inflight.Delete(key)
		return true
	})
	sp.inflightCount.Store(0)
}

// StartCleanupWorker starts a background goroutine that periodically
// removes completed futures from the inflight map.
func (sp *SpeculativePrefetcher) StartCleanupWorker() {
	sp.cleanupStopMu.Lock()
	defer sp.cleanupStopMu.Unlock()

	if sp.cleanupStop != nil {
		return // Already running
	}

	stopChan := make(chan struct{})
	doneChan := make(chan struct{})
	sp.cleanupStop = stopChan
	sp.cleanupDone = doneChan

	go sp.runCleanupLoop(stopChan, doneChan)
}

func (sp *SpeculativePrefetcher) runCleanupLoop(stopChan, doneChan chan struct{}) {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()
	defer close(doneChan)

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			sp.CleanupCompleted()
		}
	}
}

// StopCleanupWorker stops the background cleanup goroutine.
func (sp *SpeculativePrefetcher) StopCleanupWorker() {
	sp.cleanupStopMu.Lock()
	stopChan := sp.cleanupStop
	doneChan := sp.cleanupDone
	sp.cleanupStop = nil
	sp.cleanupDone = nil
	sp.cleanupStopMu.Unlock()

	if stopChan == nil {
		return // Not running
	}

	close(stopChan)
	<-doneChan
}
