// Package query provides hybrid query execution for the knowledge system.
// HybridQueryCoordinator orchestrates parallel execution of text, semantic,
// and graph searches with RRF-based result fusion.
package query

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/domain"
)

// =============================================================================
// Query Metrics
// =============================================================================

// QueryMetrics contains timing and status information for a query execution.
type QueryMetrics struct {
	// Latency for each search component
	TextLatency     time.Duration `json:"text_latency"`
	SemanticLatency time.Duration `json:"semantic_latency"`
	GraphLatency    time.Duration `json:"graph_latency"`
	FusionLatency   time.Duration `json:"fusion_latency"`
	TotalLatency    time.Duration `json:"total_latency"`

	// Status flags
	TimedOut bool `json:"timed_out"`

	// Source contribution tracking
	TextContributed     bool `json:"text_contributed"`
	SemanticContributed bool `json:"semantic_contributed"`
	GraphContributed    bool `json:"graph_contributed"`

	// Error tracking
	TextError     string `json:"text_error,omitempty"`
	SemanticError string `json:"semantic_error,omitempty"`
	GraphError    string `json:"graph_error,omitempty"`
}

// =============================================================================
// Execution Result
// =============================================================================

// ExecutionResult wraps hybrid results with metadata about the execution.
type ExecutionResult struct {
	Results []HybridResult `json:"results"`
	Metrics *QueryMetrics  `json:"metrics"`
}

// =============================================================================
// Metrics Tracker Constants
// =============================================================================

const (
	// DefaultMetricsMaxEntries is the default maximum number of metrics entries.
	DefaultMetricsMaxEntries = 10000

	// DefaultMetricsTTL is the default time-to-live for metrics entries.
	DefaultMetricsTTL = 1 * time.Hour

	// DefaultCleanupInterval is the default interval for background cleanup.
	DefaultCleanupInterval = 5 * time.Minute
)

// =============================================================================
// Metrics Entry
// =============================================================================

// metricsEntry wraps QueryMetrics with timestamp for TTL tracking.
type metricsEntry struct {
	metrics   *QueryMetrics
	timestamp time.Time
}

// =============================================================================
// Metrics Tracker Config
// =============================================================================

// MetricsTrackerConfig holds configuration for the metrics tracker.
type MetricsTrackerConfig struct {
	// MaxEntries is the maximum number of metrics entries to retain.
	MaxEntries int

	// TTL is the time-to-live for metrics entries.
	TTL time.Duration

	// CleanupInterval is the interval for background cleanup.
	CleanupInterval time.Duration
}

// DefaultMetricsTrackerConfig returns the default configuration.
func DefaultMetricsTrackerConfig() MetricsTrackerConfig {
	return MetricsTrackerConfig{
		MaxEntries:      DefaultMetricsMaxEntries,
		TTL:             DefaultMetricsTTL,
		CleanupInterval: DefaultCleanupInterval,
	}
}

// =============================================================================
// Domain Detection Configuration (W4P.36)
// =============================================================================

// DomainFallbackMode specifies how to handle domain detection failures.
type DomainFallbackMode int

const (
	// FallbackSilent uses default domain without logging (legacy behavior).
	FallbackSilent DomainFallbackMode = iota
	// FallbackLog uses default domain but logs the fallback.
	FallbackLog
	// FallbackError returns an error when domain cannot be detected.
	FallbackError
)

// DomainDetectionConfig holds settings for domain detection behavior.
type DomainDetectionConfig struct {
	// FallbackMode specifies behavior when domain detection fails.
	FallbackMode DomainFallbackMode

	// DefaultDomain is used when detection fails (if FallbackMode is not FallbackError).
	DefaultDomain domain.Domain

	// RequireExplicitDomain when true, returns error if no explicit domain is provided.
	RequireExplicitDomain bool

	// LogFallbacks when true, logs whenever the fallback domain is used.
	LogFallbacks bool
}

// DefaultDomainDetectionConfig returns the default configuration with logging enabled.
func DefaultDomainDetectionConfig() DomainDetectionConfig {
	return DomainDetectionConfig{
		FallbackMode:          FallbackLog,
		DefaultDomain:         domain.DomainLibrarian,
		RequireExplicitDomain: false,
		LogFallbacks:          true,
	}
}

// StrictDomainDetectionConfig returns a configuration that requires explicit domains.
// Useful for critical queries where domain correctness is essential.
func StrictDomainDetectionConfig() DomainDetectionConfig {
	return DomainDetectionConfig{
		FallbackMode:          FallbackError,
		DefaultDomain:         domain.DomainLibrarian,
		RequireExplicitDomain: true,
		LogFallbacks:          true,
	}
}

// SilentDomainDetectionConfig returns a configuration with silent fallback.
// Maintains backward compatibility with legacy behavior.
func SilentDomainDetectionConfig() DomainDetectionConfig {
	return DomainDetectionConfig{
		FallbackMode:          FallbackSilent,
		DefaultDomain:         domain.DomainLibrarian,
		RequireExplicitDomain: false,
		LogFallbacks:          false,
	}
}

// =============================================================================
// Domain Detection Errors
// =============================================================================

// ErrDomainRequired is returned when explicit domain is required but not provided.
type ErrDomainRequired struct {
	QueryContext string
}

func (e *ErrDomainRequired) Error() string {
	return "explicit domain required but not provided for query: " + e.QueryContext
}

// =============================================================================
// Metrics Tracker
// =============================================================================

// metricsTracker provides thread-safe tracking of query metrics with bounded memory.
type metricsTracker struct {
	metrics       map[string]*metricsEntry
	totalLatency  atomic.Int64
	totalCount    atomic.Int64
	textLatency   atomic.Int64
	textCount     atomic.Int64
	semLatency    atomic.Int64
	semCount      atomic.Int64
	graphLatency  atomic.Int64
	graphCount    atomic.Int64
	fusionLatency atomic.Int64
	fusionCount   atomic.Int64
	timeoutCount  atomic.Int64
	mu            sync.RWMutex

	// Configuration
	maxEntries int
	ttl        time.Duration

	// Background cleanup
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// newMetricsTracker creates a new metrics tracker with default configuration.
func newMetricsTracker() *metricsTracker {
	return newMetricsTrackerWithConfig(DefaultMetricsTrackerConfig())
}

// newMetricsTrackerWithConfig creates a new metrics tracker with custom config.
func newMetricsTrackerWithConfig(config MetricsTrackerConfig) *metricsTracker {
	if config.MaxEntries <= 0 {
		config.MaxEntries = DefaultMetricsMaxEntries
	}
	if config.TTL <= 0 {
		config.TTL = DefaultMetricsTTL
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = DefaultCleanupInterval
	}

	mt := &metricsTracker{
		metrics:    make(map[string]*metricsEntry),
		maxEntries: config.MaxEntries,
		ttl:        config.TTL,
		stopCh:     make(chan struct{}),
	}

	// Start background cleanup goroutine
	mt.wg.Add(1)
	go mt.cleanupLoop(config.CleanupInterval)

	return mt
}

// cleanupLoop runs periodic cleanup of expired entries.
func (mt *metricsTracker) cleanupLoop(interval time.Duration) {
	defer mt.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-mt.stopCh:
			return
		case <-ticker.C:
			mt.cleanup()
		}
	}
}

// cleanup removes expired entries from the metrics map.
func (mt *metricsTracker) cleanup() {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	now := time.Now()
	for id, entry := range mt.metrics {
		if now.Sub(entry.timestamp) > mt.ttl {
			delete(mt.metrics, id)
		}
	}
}

// Close stops the background cleanup goroutine.
func (mt *metricsTracker) Close() {
	close(mt.stopCh)
	mt.wg.Wait()
}

// Record stores metrics for a query with bounded memory management.
func (mt *metricsTracker) Record(queryID string, metrics *QueryMetrics) {
	mt.mu.Lock()
	mt.recordLocked(queryID, metrics)
	mt.mu.Unlock()

	// Update aggregate counters (atomic, no lock needed)
	mt.updateAggregates(metrics)
}

// recordLocked stores metrics entry under lock and enforces bounds.
func (mt *metricsTracker) recordLocked(queryID string, metrics *QueryMetrics) {
	// Evict oldest entries if at capacity
	mt.evictIfNeeded()

	mt.metrics[queryID] = &metricsEntry{
		metrics:   metrics,
		timestamp: time.Now(),
	}
}

// evictIfNeeded removes oldest entries if at or over capacity.
func (mt *metricsTracker) evictIfNeeded() {
	if len(mt.metrics) < mt.maxEntries {
		return
	}

	// Find and remove oldest entry
	var oldestID string
	var oldestTime time.Time

	for id, entry := range mt.metrics {
		if oldestID == "" || entry.timestamp.Before(oldestTime) {
			oldestID = id
			oldestTime = entry.timestamp
		}
	}

	if oldestID != "" {
		delete(mt.metrics, oldestID)
	}
}

// updateAggregates updates the atomic aggregate counters.
func (mt *metricsTracker) updateAggregates(metrics *QueryMetrics) {
	mt.totalLatency.Add(int64(metrics.TotalLatency))
	mt.totalCount.Add(1)

	if metrics.TextContributed {
		mt.textLatency.Add(int64(metrics.TextLatency))
		mt.textCount.Add(1)
	}
	if metrics.SemanticContributed {
		mt.semLatency.Add(int64(metrics.SemanticLatency))
		mt.semCount.Add(1)
	}
	if metrics.GraphContributed {
		mt.graphLatency.Add(int64(metrics.GraphLatency))
		mt.graphCount.Add(1)
	}

	mt.fusionLatency.Add(int64(metrics.FusionLatency))
	mt.fusionCount.Add(1)

	if metrics.TimedOut {
		mt.timeoutCount.Add(1)
	}
}

// GetAverageMetrics returns average metrics across all recorded queries.
func (mt *metricsTracker) GetAverageMetrics() *QueryMetrics {
	totalCount := mt.totalCount.Load()
	if totalCount == 0 {
		return &QueryMetrics{}
	}

	textCount := mt.textCount.Load()
	semCount := mt.semCount.Load()
	graphCount := mt.graphCount.Load()
	fusionCount := mt.fusionCount.Load()

	var avgText, avgSem, avgGraph, avgFusion time.Duration

	if textCount > 0 {
		avgText = time.Duration(mt.textLatency.Load() / textCount)
	}
	if semCount > 0 {
		avgSem = time.Duration(mt.semLatency.Load() / semCount)
	}
	if graphCount > 0 {
		avgGraph = time.Duration(mt.graphLatency.Load() / graphCount)
	}
	if fusionCount > 0 {
		avgFusion = time.Duration(mt.fusionLatency.Load() / fusionCount)
	}

	return &QueryMetrics{
		TextLatency:     avgText,
		SemanticLatency: avgSem,
		GraphLatency:    avgGraph,
		FusionLatency:   avgFusion,
		TotalLatency:    time.Duration(mt.totalLatency.Load() / totalCount),
		TimedOut:        mt.timeoutCount.Load() > 0,
	}
}

// GetMetrics retrieves metrics for a specific query.
func (mt *metricsTracker) GetMetrics(queryID string) (*QueryMetrics, bool) {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	entry, ok := mt.metrics[queryID]
	if !ok {
		return nil, false
	}
	return entry.metrics, true
}

// EntryCount returns the current number of metrics entries.
func (mt *metricsTracker) EntryCount() int {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return len(mt.metrics)
}

// MaxEntries returns the configured maximum entries limit.
func (mt *metricsTracker) MaxEntries() int {
	return mt.maxEntries
}

// TTL returns the configured time-to-live for entries.
func (mt *metricsTracker) TTL() time.Duration {
	return mt.ttl
}

// =============================================================================
// Hybrid Query Coordinator
// =============================================================================

// HybridQueryCoordinator orchestrates parallel execution of text, semantic,
// and graph-based searches, fusing results using RRF with learned weights.
type HybridQueryCoordinator struct {
	bleveSearcher        *BleveSearcher
	vectorSearcher       *VectorSearcher
	graphTraverser       *GraphTraverser
	rrfFusion            *RRFFusion
	learnedWeights       *LearnedQueryWeights
	timeout              time.Duration
	metricsTracker       *metricsTracker
	domainDetectionCfg   DomainDetectionConfig
	mu                   sync.RWMutex
}

// NewHybridQueryCoordinator creates a new coordinator with the provided components.
// All components are optional - missing components will simply not contribute results.
func NewHybridQueryCoordinator(
	bleve *BleveSearcher,
	vector *VectorSearcher,
	graph *GraphTraverser,
) *HybridQueryCoordinator {
	return &HybridQueryCoordinator{
		bleveSearcher:      bleve,
		vectorSearcher:     vector,
		graphTraverser:     graph,
		rrfFusion:          DefaultRRFFusion(),
		learnedWeights:     NewLearnedQueryWeights(),
		timeout:            100 * time.Millisecond,
		metricsTracker:     newMetricsTracker(),
		domainDetectionCfg: DefaultDomainDetectionConfig(),
	}
}

// NewHybridQueryCoordinatorWithOptions creates a coordinator with custom options.
func NewHybridQueryCoordinatorWithOptions(
	bleve *BleveSearcher,
	vector *VectorSearcher,
	graph *GraphTraverser,
	rrfFusion *RRFFusion,
	learnedWeights *LearnedQueryWeights,
	timeout time.Duration,
) *HybridQueryCoordinator {
	if rrfFusion == nil {
		rrfFusion = DefaultRRFFusion()
	}
	if learnedWeights == nil {
		learnedWeights = NewLearnedQueryWeights()
	}
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}

	return &HybridQueryCoordinator{
		bleveSearcher:      bleve,
		vectorSearcher:     vector,
		graphTraverser:     graph,
		rrfFusion:          rrfFusion,
		learnedWeights:     learnedWeights,
		timeout:            timeout,
		metricsTracker:     newMetricsTracker(),
		domainDetectionCfg: DefaultDomainDetectionConfig(),
	}
}

// SetTimeout updates the query timeout duration.
func (c *HybridQueryCoordinator) SetTimeout(timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if timeout > 0 {
		c.timeout = timeout
	}
}

// GetTimeout returns the current timeout duration.
func (c *HybridQueryCoordinator) GetTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.timeout
}

// SetRRFParameter updates the RRF k parameter.
func (c *HybridQueryCoordinator) SetRRFParameter(k int) {
	c.rrfFusion.SetK(k)
}

// GetRRFParameter returns the current RRF k parameter.
func (c *HybridQueryCoordinator) GetRRFParameter() int {
	return c.rrfFusion.K()
}

// SetDomainDetectionConfig updates the domain detection configuration.
func (c *HybridQueryCoordinator) SetDomainDetectionConfig(cfg DomainDetectionConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.domainDetectionCfg = cfg
}

// GetDomainDetectionConfig returns the current domain detection configuration.
func (c *HybridQueryCoordinator) GetDomainDetectionConfig() DomainDetectionConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.domainDetectionCfg
}

// Execute performs a hybrid query with parallel execution of all enabled searchers.
// Results are fused using RRF with learned weights. The method respects the configured
// timeout and returns partial results if the timeout is exceeded.
func (c *HybridQueryCoordinator) Execute(ctx context.Context, query *HybridQuery) ([]HybridResult, error) {
	result, err := c.ExecuteWithMetrics(ctx, query)
	if err != nil {
		return nil, err
	}
	return result.Results, nil
}

// ExecuteWithMetrics performs a hybrid query and returns results with execution metrics.
func (c *HybridQueryCoordinator) ExecuteWithMetrics(ctx context.Context, query *HybridQuery) (*ExecutionResult, error) {
	startTime := time.Now()
	metrics := &QueryMetrics{}

	// Validate query
	if query == nil {
		return &ExecutionResult{
			Results: []HybridResult{},
			Metrics: metrics,
		}, nil
	}

	if err := query.Validate(); err != nil {
		return nil, err
	}

	// Determine timeout
	c.mu.RLock()
	timeout := c.timeout
	c.mu.RUnlock()

	if query.Timeout > 0 {
		timeout = query.Timeout
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Execute searches in parallel
	textResults, semanticResults, graphResults := c.executeParallel(ctx, query, metrics)

	// Check if we timed out
	select {
	case <-ctx.Done():
		metrics.TimedOut = true
	default:
	}

	// Get weights for fusion
	fusionStart := time.Now()
	weights := c.getWeightsForQuery(query)

	// Fuse results
	fusedResults := c.rrfFusion.Fuse(textResults, semanticResults, graphResults, weights)
	metrics.FusionLatency = time.Since(fusionStart)

	// Apply limit if specified
	if query.Limit > 0 && len(fusedResults) > query.Limit {
		fusedResults = fusedResults[:query.Limit]
	}

	metrics.TotalLatency = time.Since(startTime)

	return &ExecutionResult{
		Results: fusedResults,
		Metrics: metrics,
	}, nil
}

// searchResult holds results from a single searcher goroutine.
type searchResult struct {
	source   string
	text     []TextResult
	semantic []VectorResult
	graph    []GraphResult
	err      error
	latency  time.Duration
}

// executeParallel runs all three searchers concurrently.
func (c *HybridQueryCoordinator) executeParallel(
	ctx context.Context,
	query *HybridQuery,
	metrics *QueryMetrics,
) ([]TextResult, []VectorResult, []GraphResult) {
	var wg sync.WaitGroup
	results := make(chan searchResult, 3)

	// Launch text search
	if query.HasTextQuery() && c.bleveSearcher != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			textResults, err := c.bleveSearcher.Execute(ctx, query.TextQuery, c.getLimit(query))
			results <- searchResult{
				source:  "text",
				text:    textResults,
				err:     err,
				latency: time.Since(start),
			}
		}()
	}

	// Launch semantic search
	if query.HasSemanticQuery() && c.vectorSearcher != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			semanticResults, err := c.vectorSearcher.Execute(ctx, query.SemanticVector, c.getLimit(query))
			results <- searchResult{
				source:   "semantic",
				semantic: semanticResults,
				err:      err,
				latency:  time.Since(start),
			}
		}()
	}

	// Launch graph traversal
	if query.HasGraphQuery() && c.graphTraverser != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			graphResults, err := c.graphTraverser.Execute(ctx, query.GraphPattern, c.getLimit(query))
			results <- searchResult{
				source:  "graph",
				graph:   graphResults,
				err:     err,
				latency: time.Since(start),
			}
		}()
	}

	// Close channel when all searches complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var textResults []TextResult
	var semanticResults []VectorResult
	var graphResults []GraphResult

	for result := range results {
		switch result.source {
		case "text":
			textResults = result.text
			metrics.TextLatency = result.latency
			metrics.TextContributed = len(result.text) > 0
			if result.err != nil {
				metrics.TextError = result.err.Error()
			}
		case "semantic":
			semanticResults = result.semantic
			metrics.SemanticLatency = result.latency
			metrics.SemanticContributed = len(result.semantic) > 0
			if result.err != nil {
				metrics.SemanticError = result.err.Error()
			}
		case "graph":
			graphResults = result.graph
			metrics.GraphLatency = result.latency
			metrics.GraphContributed = len(result.graph) > 0
			if result.err != nil {
				metrics.GraphError = result.err.Error()
			}
		}
	}

	return textResults, semanticResults, graphResults
}

// getLimit returns the limit to use for individual searches.
// Uses a multiplier to get more candidates for fusion.
func (c *HybridQueryCoordinator) getLimit(query *HybridQuery) int {
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	// Get more candidates than requested to improve fusion quality
	return limit * 3
}

// getWeightsForQuery determines the weights to use for a query.
func (c *HybridQueryCoordinator) getWeightsForQuery(query *HybridQuery) *QueryWeights {
	// Check if query specifies explicit weights
	if query.TextWeight > 0 || query.SemanticWeight > 0 || query.GraphWeight > 0 {
		weights := &QueryWeights{
			TextWeight:     query.TextWeight,
			SemanticWeight: query.SemanticWeight,
			GraphWeight:    query.GraphWeight,
		}
		return weights.Normalize()
	}

	// Use learned weights with domain detection
	d := c.detectDomain(query)
	explore := c.shouldExplore()

	return c.learnedWeights.GetWeights(d, explore)
}

// DomainDetectionResult holds the result of domain detection with context.
type DomainDetectionResult struct {
	Domain     domain.Domain
	WasFallback bool
	Explicit    bool
}

// detectDomainWithResult attempts to determine the domain from query context.
// Returns detailed result including whether fallback was used.
func (c *HybridQueryCoordinator) detectDomainWithResult(query *HybridQuery) DomainDetectionResult {
	// Check filters for domain specification
	for _, filter := range query.Filters {
		if filter.Type == FilterDomain {
			if d, ok := filter.Value.(domain.Domain); ok {
				return DomainDetectionResult{
					Domain:     d,
					WasFallback: false,
					Explicit:    true,
				}
			}
			if dStr, ok := filter.Value.(string); ok {
				if d, ok := domain.ParseDomain(dStr); ok {
					return DomainDetectionResult{
						Domain:     d,
						WasFallback: false,
						Explicit:    true,
					}
				}
			}
		}
	}

	// No explicit domain found - this is a fallback
	c.mu.RLock()
	cfg := c.domainDetectionCfg
	c.mu.RUnlock()

	return DomainDetectionResult{
		Domain:     cfg.DefaultDomain,
		WasFallback: true,
		Explicit:    false,
	}
}

// detectDomain attempts to determine the domain from query context.
// Uses configured fallback behavior when detection fails.
func (c *HybridQueryCoordinator) detectDomain(query *HybridQuery) domain.Domain {
	result := c.detectDomainWithResult(query)

	if result.WasFallback {
		c.mu.RLock()
		cfg := c.domainDetectionCfg
		c.mu.RUnlock()

		// Log the fallback if configured
		if cfg.LogFallbacks || cfg.FallbackMode == FallbackLog {
			queryCtx := c.getQueryContext(query)
			log.Printf("[WARN] domain detection fallback: using default domain %q for query %s",
				result.Domain.String(), queryCtx)
		}
	}

	return result.Domain
}

// getQueryContext extracts a brief context string from the query for logging.
func (c *HybridQueryCoordinator) getQueryContext(query *HybridQuery) string {
	if query.TextQuery != "" {
		// Truncate long queries for logging
		text := query.TextQuery
		if len(text) > 50 {
			text = text[:47] + "..."
		}
		return "text=\"" + text + "\""
	}
	if len(query.SemanticVector) > 0 {
		return "semantic_vector"
	}
	if query.GraphPattern != nil {
		return "graph_pattern"
	}
	return "unknown"
}

// DetectDomainStrict attempts to detect the domain and returns an error if
// explicit domain is required but not provided. This is useful for critical
// queries where using the wrong domain could cause incorrect results.
func (c *HybridQueryCoordinator) DetectDomainStrict(query *HybridQuery) (domain.Domain, error) {
	result := c.detectDomainWithResult(query)

	c.mu.RLock()
	cfg := c.domainDetectionCfg
	c.mu.RUnlock()

	if result.WasFallback {
		// Check if explicit domain is required
		if cfg.RequireExplicitDomain || cfg.FallbackMode == FallbackError {
			queryCtx := c.getQueryContext(query)
			log.Printf("[ERROR] domain detection failed: explicit domain required for query %s",
				queryCtx)
			return domain.DomainLibrarian, &ErrDomainRequired{QueryContext: queryCtx}
		}

		// Log the fallback if configured
		if cfg.LogFallbacks || cfg.FallbackMode == FallbackLog {
			queryCtx := c.getQueryContext(query)
			log.Printf("[WARN] domain detection fallback: using default domain %q for query %s",
				result.Domain.String(), queryCtx)
		}
	}

	return result.Domain, nil
}

// shouldExplore determines whether to use exploration mode for weights.
// Uses a simple heuristic based on confidence.
func (c *HybridQueryCoordinator) shouldExplore() bool {
	confidence := c.learnedWeights.GetGlobalConfidence()
	// Explore when confidence is low (weights still being learned)
	return confidence < 0.7
}

// RecordMetrics stores metrics for a query execution.
func (c *HybridQueryCoordinator) RecordMetrics(queryID string, metrics *QueryMetrics) {
	c.metricsTracker.Record(queryID, metrics)
}

// GetAverageMetrics returns average metrics across all recorded queries.
func (c *HybridQueryCoordinator) GetAverageMetrics() *QueryMetrics {
	return c.metricsTracker.GetAverageMetrics()
}

// GetQueryMetrics retrieves metrics for a specific query.
func (c *HybridQueryCoordinator) GetQueryMetrics(queryID string) (*QueryMetrics, bool) {
	return c.metricsTracker.GetMetrics(queryID)
}

// =============================================================================
// Component Accessors
// =============================================================================

// BleveSearcher returns the text search component.
func (c *HybridQueryCoordinator) BleveSearcher() *BleveSearcher {
	return c.bleveSearcher
}

// VectorSearcher returns the semantic search component.
func (c *HybridQueryCoordinator) VectorSearcher() *VectorSearcher {
	return c.vectorSearcher
}

// GraphTraverser returns the graph traversal component.
func (c *HybridQueryCoordinator) GraphTraverser() *GraphTraverser {
	return c.graphTraverser
}

// RRFFusion returns the fusion component.
func (c *HybridQueryCoordinator) RRFFusion() *RRFFusion {
	return c.rrfFusion
}

// LearnedWeights returns the learned weights component.
func (c *HybridQueryCoordinator) LearnedWeights() *LearnedQueryWeights {
	return c.learnedWeights
}

// =============================================================================
// Status Methods
// =============================================================================

// IsReady returns true if at least one searcher is available.
func (c *HybridQueryCoordinator) IsReady() bool {
	hasBleve := c.bleveSearcher != nil && c.bleveSearcher.IsReady()
	hasVector := c.vectorSearcher != nil && c.vectorSearcher.IsReady()
	hasGraph := c.graphTraverser != nil && c.graphTraverser.IsReady()
	return hasBleve || hasVector || hasGraph
}

// ReadySearchers returns a list of search modalities that are available.
func (c *HybridQueryCoordinator) ReadySearchers() []string {
	var ready []string
	if c.bleveSearcher != nil && c.bleveSearcher.IsReady() {
		ready = append(ready, "text")
	}
	if c.vectorSearcher != nil && c.vectorSearcher.IsReady() {
		ready = append(ready, "semantic")
	}
	if c.graphTraverser != nil && c.graphTraverser.IsReady() {
		ready = append(ready, "graph")
	}
	return ready
}

// =============================================================================
// Lifecycle Methods
// =============================================================================

// Close cleans up resources including the background metrics cleanup goroutine.
func (c *HybridQueryCoordinator) Close() {
	if c.metricsTracker != nil {
		c.metricsTracker.Close()
	}
}

// =============================================================================
// Weight Learning Integration
// =============================================================================

// UpdateWeights updates the learned weights based on user feedback.
// clickedResultID is the ID of the result the user selected.
// textResults, semanticResults, graphResults are the original search results
// (before fusion) to compute source rankings.
func (c *HybridQueryCoordinator) UpdateWeights(
	queryID string,
	clickedResultID string,
	textResults []TextResult,
	semanticResults []VectorResult,
	graphResults []GraphResult,
	d domain.Domain,
) {
	ranks := BuildSourceRanks(textResults, semanticResults, graphResults)
	c.learnedWeights.Update(queryID, clickedResultID, ranks, d)
}
