// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.5.2: TieredSearcher - multi-tier search with budget awareness.
package context

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/errors"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/coordinator"
)

// =============================================================================
// Constants
// =============================================================================

// Tier latency budgets.
const (
	TierHotBudget  = 1 * time.Millisecond
	TierWarmBudget = 10 * time.Millisecond
	TierFullBudget = 200 * time.Millisecond
)

// Default limits per tier.
const (
	DefaultHotLimit  = 50
	DefaultWarmLimit = 100
	DefaultFullLimit = 200
)

// Circuit breaker names.
const (
	CBNameTierHot  = "tiered-search-hot"
	CBNameTierWarm = "tiered-search-warm"
	CBNameTierFull = "tiered-search-full"
)

// TierBudget returns the latency budget for a given search tier.
func TierBudget(tier SearchTier) time.Duration {
	switch tier {
	case TierHotCache:
		return TierHotBudget
	case TierWarmIndex:
		return TierWarmBudget
	case TierFullSearch:
		return TierFullBudget
	default:
		return TierFullBudget
	}
}

// =============================================================================
// TieredSearchResult
// =============================================================================

// TieredSearchResult holds the results from a tiered search operation.
type TieredSearchResult struct {
	// Results contains the matched content entries.
	Results []*ContentEntry

	// Tier indicates which tier(s) contributed results.
	Tier SearchTier

	// HotHits is the number of hits from the hot cache.
	HotHits int

	// WarmHits is the number of hits from Bleve.
	WarmHits int

	// FullHits is the number of hits from full hybrid search.
	FullHits int

	// TotalTime is the total search duration.
	TotalTime time.Duration

	// TierTimes tracks time spent in each tier.
	TierTimes map[SearchTier]time.Duration

	// Errors contains any errors from tiers (non-fatal if results exist).
	Errors map[SearchTier]error
}

// HasResults returns true if any results were found.
func (r *TieredSearchResult) HasResults() bool {
	return len(r.Results) > 0
}

// =============================================================================
// TieredSearcher Dependencies
// =============================================================================

// TieredBleveSearcher is the interface for Bleve search in TieredSearcher.
type TieredBleveSearcher interface {
	Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error)
	IsOpen() bool
}

// TieredVectorSearcher is the interface for Vector search in TieredSearcher.
type TieredVectorSearcher interface {
	Search(ctx context.Context, query []float32, limit int) ([]coordinator.ScoredVectorResult, error)
}

// TieredEmbeddingGenerator is the interface for generating embeddings.
type TieredEmbeddingGenerator interface {
	Generate(ctx context.Context, text string) ([]float32, error)
}

// =============================================================================
// TieredSearcher Configuration
// =============================================================================

// TieredSearcherConfig holds configuration for the TieredSearcher.
type TieredSearcherConfig struct {
	// HotCache is the Tier 0 hot cache (required).
	HotCache *HotCache

	// Bleve is the Tier 1 Bleve searcher (optional).
	Bleve TieredBleveSearcher

	// Vector is the Tier 2 Vector searcher (optional).
	Vector TieredVectorSearcher

	// Embedder generates embeddings for vector search (optional).
	Embedder TieredEmbeddingGenerator

	// CircuitBreakerConfig for tier circuit breakers.
	CircuitBreakerConfig errors.CircuitBreakerConfig

	// DefaultLimit is the default result limit.
	DefaultLimit int
}

// =============================================================================
// TieredSearcher
// =============================================================================

// TieredSearcher provides multi-tier search with latency budget awareness.
// Tier 0 (Hot): In-memory cache, < 1ms
// Tier 1 (Warm): Bleve full-text, < 10ms
// Tier 2 (Full): Bleve + VectorDB with RRF fusion, < 200ms
type TieredSearcher struct {
	hotCache *HotCache
	bleve    TieredBleveSearcher
	vector   TieredVectorSearcher
	embedder TieredEmbeddingGenerator

	// Circuit breakers per tier
	cbHot  *errors.CircuitBreaker
	cbWarm *errors.CircuitBreaker
	cbFull *errors.CircuitBreaker

	// maxTier limits the maximum searchable tier (for pressure response)
	maxTier atomic.Int32

	// stats
	mu          sync.RWMutex
	searchCount int64
	tierHits    map[SearchTier]int64

	// config
	defaultLimit int
}

// NewTieredSearcher creates a new tiered searcher with the given configuration.
func NewTieredSearcher(config TieredSearcherConfig) *TieredSearcher {
	cbConfig := config.CircuitBreakerConfig
	if cbConfig.ConsecutiveFailures == 0 {
		cbConfig = errors.DefaultCircuitBreakerConfig()
	}

	defaultLimit := config.DefaultLimit
	if defaultLimit <= 0 {
		defaultLimit = DefaultWarmLimit
	}

	ts := &TieredSearcher{
		hotCache:     config.HotCache,
		bleve:        config.Bleve,
		vector:       config.Vector,
		embedder:     config.Embedder,
		cbHot:        errors.NewCircuitBreaker(CBNameTierHot, cbConfig),
		cbWarm:       errors.NewCircuitBreaker(CBNameTierWarm, cbConfig),
		cbFull:       errors.NewCircuitBreaker(CBNameTierFull, cbConfig),
		tierHits:     make(map[SearchTier]int64),
		defaultLimit: defaultLimit,
	}

	// Default max tier to full
	ts.maxTier.Store(int32(TierFullSearch))

	return ts
}

// =============================================================================
// Search Operations
// =============================================================================

// SearchWithBudget performs a tiered search respecting the given latency budget.
func (ts *TieredSearcher) SearchWithBudget(
	ctx context.Context,
	query string,
	budget time.Duration,
) *TieredSearchResult {
	start := time.Now()
	result := &TieredSearchResult{
		Results:   make([]*ContentEntry, 0),
		TierTimes: make(map[SearchTier]time.Duration),
		Errors:    make(map[SearchTier]error),
	}

	ts.incrementSearchCount()

	// Always search hot cache (Tier 0)
	ts.searchTierHot(ctx, query, result)

	// Determine which tiers to search based on budget
	if ts.shouldSearchWarm(budget) {
		ts.searchTierWarm(ctx, query, budget, result)
	}

	if ts.shouldSearchFull(budget, result) {
		ts.searchTierFull(ctx, query, budget, result)
	}

	result.TotalTime = time.Since(start)
	return result
}

func (ts *TieredSearcher) incrementSearchCount() {
	ts.mu.Lock()
	ts.searchCount++
	ts.mu.Unlock()
}

func (ts *TieredSearcher) shouldSearchWarm(budget time.Duration) bool {
	maxTier := SearchTier(ts.maxTier.Load())
	return budget >= TierWarmBudget && maxTier >= TierWarmIndex
}

func (ts *TieredSearcher) shouldSearchFull(budget time.Duration, result *TieredSearchResult) bool {
	maxTier := SearchTier(ts.maxTier.Load())
	hasMinResults := len(result.Results) < ts.defaultLimit
	return budget >= TierFullBudget && maxTier >= TierFullSearch && hasMinResults
}

// =============================================================================
// Tier 0: Hot Cache Search
// =============================================================================

func (ts *TieredSearcher) searchTierHot(
	ctx context.Context,
	query string,
	result *TieredSearchResult,
) {
	if ts.hotCache == nil || !ts.cbHot.Allow() {
		return
	}

	start := time.Now()

	// Search hot cache by content ID match or keyword match
	entries := ts.searchHotCacheEntries(query)

	elapsed := time.Since(start)
	result.TierTimes[TierHotCache] = elapsed

	if len(entries) > 0 {
		result.Results = append(result.Results, entries...)
		result.HotHits = len(entries)
		result.Tier = TierHotCache
		ts.recordTierHit(TierHotCache)
		ts.cbHot.RecordResult(true)
	}
}

func (ts *TieredSearcher) searchHotCacheEntries(query string) []*ContentEntry {
	entries := make([]*ContentEntry, 0)

	// Check if query is a content ID
	if entry := ts.hotCache.Get(query); entry != nil {
		entries = append(entries, entry)
		return entries
	}

	// Search through all cached entries for keyword matches
	allEntries := ts.hotCache.Entries()
	for _, entry := range allEntries {
		if ts.entryMatchesQuery(entry, query) {
			entries = append(entries, entry)
		}
	}

	return entries
}

func (ts *TieredSearcher) entryMatchesQuery(entry *ContentEntry, query string) bool {
	// Check keywords
	for _, kw := range entry.Keywords {
		if containsIgnoreCase(kw, query) || containsIgnoreCase(query, kw) {
			return true
		}
	}
	return false
}

// =============================================================================
// Tier 1: Warm (Bleve) Search
// =============================================================================

func (ts *TieredSearcher) searchTierWarm(
	ctx context.Context,
	query string,
	budget time.Duration,
	result *TieredSearchResult,
) {
	if ts.bleve == nil || !ts.bleve.IsOpen() || !ts.cbWarm.Allow() {
		return
	}

	start := time.Now()
	remaining := budget - result.TierTimes[TierHotCache]

	warmCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()

	searchResult, err := ts.executeBleveSearch(warmCtx, query)
	elapsed := time.Since(start)
	result.TierTimes[TierWarmIndex] = elapsed

	if err != nil {
		result.Errors[TierWarmIndex] = err
		ts.cbWarm.RecordResult(false)
		return
	}

	ts.processBleveResults(searchResult, result)
	ts.cbWarm.RecordResult(true)
}

func (ts *TieredSearcher) executeBleveSearch(
	ctx context.Context,
	query string,
) (*search.SearchResult, error) {
	req := &search.SearchRequest{
		Query: query,
		Limit: DefaultWarmLimit,
	}
	return ts.bleve.Search(ctx, req)
}

func (ts *TieredSearcher) processBleveResults(
	searchResult *search.SearchResult,
	result *TieredSearchResult,
) {
	if searchResult == nil {
		return
	}

	for _, doc := range searchResult.Documents {
		entry := ts.bleveDocToContentEntry(doc)
		result.Results = append(result.Results, entry)
	}

	result.WarmHits = len(searchResult.Documents)
	if result.WarmHits > 0 {
		result.Tier = TierWarmIndex
		ts.recordTierHit(TierWarmIndex)
	}
}

func (ts *TieredSearcher) bleveDocToContentEntry(doc search.ScoredDocument) *ContentEntry {
	return &ContentEntry{
		ID:       doc.Document.ID,
		Content:  doc.Document.Content,
		Metadata: make(map[string]string),
	}
}

// =============================================================================
// Tier 2: Full (Bleve + Vector) Search
// =============================================================================

func (ts *TieredSearcher) searchTierFull(
	ctx context.Context,
	query string,
	budget time.Duration,
	result *TieredSearchResult,
) {
	if !ts.canSearchFull() || !ts.cbFull.Allow() {
		return
	}

	start := time.Now()
	elapsed := result.TierTimes[TierHotCache] + result.TierTimes[TierWarmIndex]
	remaining := budget - elapsed

	fullCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()

	vectorResults, err := ts.executeVectorSearch(fullCtx, query)
	result.TierTimes[TierFullSearch] = time.Since(start)

	if err != nil {
		result.Errors[TierFullSearch] = err
		ts.cbFull.RecordResult(false)
		return
	}

	ts.processVectorResults(vectorResults, result)
	ts.cbFull.RecordResult(true)
}

func (ts *TieredSearcher) canSearchFull() bool {
	return ts.vector != nil && ts.embedder != nil
}

func (ts *TieredSearcher) executeVectorSearch(
	ctx context.Context,
	query string,
) ([]coordinator.ScoredVectorResult, error) {
	embedding, err := ts.embedder.Generate(ctx, query)
	if err != nil {
		return nil, err
	}

	return ts.vector.Search(ctx, embedding, DefaultFullLimit)
}

func (ts *TieredSearcher) processVectorResults(
	vectorResults []coordinator.ScoredVectorResult,
	result *TieredSearchResult,
) {
	for _, vr := range vectorResults {
		entry := ts.vectorResultToContentEntry(vr)
		result.Results = append(result.Results, entry)
	}

	result.FullHits = len(vectorResults)
	if result.FullHits > 0 {
		result.Tier = TierFullSearch
		ts.recordTierHit(TierFullSearch)
	}
}

func (ts *TieredSearcher) vectorResultToContentEntry(vr coordinator.ScoredVectorResult) *ContentEntry {
	return &ContentEntry{
		ID:       vr.ID,
		Content:  vr.Content,
		Metadata: make(map[string]string),
	}
}

// =============================================================================
// Tier Management
// =============================================================================

// SetMaxTier sets the maximum searchable tier (for pressure response).
func (ts *TieredSearcher) SetMaxTier(tier SearchTier) {
	ts.maxTier.Store(int32(tier))
}

// MaxTier returns the current maximum searchable tier.
func (ts *TieredSearcher) MaxTier() SearchTier {
	return SearchTier(ts.maxTier.Load())
}

// ResetMaxTier resets the maximum tier to full.
func (ts *TieredSearcher) ResetMaxTier() {
	ts.maxTier.Store(int32(TierFullSearch))
}

// HotCache returns the underlying hot cache.
// This is exposed primarily for testing purposes.
func (ts *TieredSearcher) HotCache() *HotCache {
	return ts.hotCache
}

// =============================================================================
// Circuit Breaker Access
// =============================================================================

// CircuitBreaker returns the circuit breaker for the given tier.
func (ts *TieredSearcher) CircuitBreaker(tier SearchTier) *errors.CircuitBreaker {
	switch tier {
	case TierHotCache:
		return ts.cbHot
	case TierWarmIndex:
		return ts.cbWarm
	case TierFullSearch:
		return ts.cbFull
	default:
		return nil
	}
}

// ResetCircuitBreakers resets all tier circuit breakers.
func (ts *TieredSearcher) ResetCircuitBreakers() {
	ts.cbHot.ForceReset()
	ts.cbWarm.ForceReset()
	ts.cbFull.ForceReset()
}

// =============================================================================
// Statistics
// =============================================================================

func (ts *TieredSearcher) recordTierHit(tier SearchTier) {
	ts.mu.Lock()
	ts.tierHits[tier]++
	ts.mu.Unlock()
}

// TieredSearcherStats holds searcher statistics.
type TieredSearcherStats struct {
	SearchCount int64
	TierHits    map[SearchTier]int64
	MaxTier     SearchTier
	CBStates    map[SearchTier]errors.CircuitState
}

// Stats returns current searcher statistics.
func (ts *TieredSearcher) Stats() TieredSearcherStats {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	tierHits := make(map[SearchTier]int64)
	for tier, count := range ts.tierHits {
		tierHits[tier] = count
	}

	return TieredSearcherStats{
		SearchCount: ts.searchCount,
		TierHits:    tierHits,
		MaxTier:     SearchTier(ts.maxTier.Load()),
		CBStates: map[SearchTier]errors.CircuitState{
			TierHotCache:  ts.cbHot.State(),
			TierWarmIndex: ts.cbWarm.State(),
			TierFullSearch: ts.cbFull.State(),
		},
	}
}

// ResetStats resets search statistics.
func (ts *TieredSearcher) ResetStats() {
	ts.mu.Lock()
	ts.searchCount = 0
	ts.tierHits = make(map[SearchTier]int64)
	ts.mu.Unlock()
}

// =============================================================================
// Helper Functions
// =============================================================================

// containsIgnoreCase checks if s contains substr (case-insensitive).
func containsIgnoreCase(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}

	for i := 0; i <= len(s)-len(substr); i++ {
		if matchAtPosition(s, substr, i) {
			return true
		}
	}
	return false
}

func matchAtPosition(s, substr string, pos int) bool {
	for j := 0; j < len(substr); j++ {
		sc := s[pos+j]
		pc := substr[j]
		if !charsEqualIgnoreCase(sc, pc) {
			return false
		}
	}
	return true
}

func charsEqualIgnoreCase(a, b byte) bool {
	if a == b {
		return true
	}
	// Convert both to lowercase and compare
	if a >= 'A' && a <= 'Z' {
		a += 'a' - 'A'
	}
	if b >= 'A' && b <= 'Z' {
		b += 'a' - 'A'
	}
	return a == b
}
