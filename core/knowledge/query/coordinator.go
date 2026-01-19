// Package query provides hybrid query execution for the knowledge system.
// HybridQueryCoordinator orchestrates parallel execution of text, semantic,
// and graph searches with RRF-based result fusion.
package query

import (
	"context"
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
// Metrics Tracker
// =============================================================================

// metricsTracker provides thread-safe tracking of query metrics.
type metricsTracker struct {
	metrics       map[string]*QueryMetrics
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
}

// newMetricsTracker creates a new metrics tracker.
func newMetricsTracker() *metricsTracker {
	return &metricsTracker{
		metrics: make(map[string]*QueryMetrics),
	}
}

// Record stores metrics for a query.
func (mt *metricsTracker) Record(queryID string, metrics *QueryMetrics) {
	mt.mu.Lock()
	mt.metrics[queryID] = metrics
	mt.mu.Unlock()

	// Update aggregate counters
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
	m, ok := mt.metrics[queryID]
	return m, ok
}

// =============================================================================
// Hybrid Query Coordinator
// =============================================================================

// HybridQueryCoordinator orchestrates parallel execution of text, semantic,
// and graph-based searches, fusing results using RRF with learned weights.
type HybridQueryCoordinator struct {
	bleveSearcher  *BleveSearcher
	vectorSearcher *VectorSearcher
	graphTraverser *GraphTraverser
	rrfFusion      *RRFFusion
	learnedWeights *LearnedQueryWeights
	timeout        time.Duration
	metricsTracker *metricsTracker
	mu             sync.RWMutex
}

// NewHybridQueryCoordinator creates a new coordinator with the provided components.
// All components are optional - missing components will simply not contribute results.
func NewHybridQueryCoordinator(
	bleve *BleveSearcher,
	vector *VectorSearcher,
	graph *GraphTraverser,
) *HybridQueryCoordinator {
	return &HybridQueryCoordinator{
		bleveSearcher:  bleve,
		vectorSearcher: vector,
		graphTraverser: graph,
		rrfFusion:      DefaultRRFFusion(),
		learnedWeights: NewLearnedQueryWeights(),
		timeout:        100 * time.Millisecond,
		metricsTracker: newMetricsTracker(),
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
		bleveSearcher:  bleve,
		vectorSearcher: vector,
		graphTraverser: graph,
		rrfFusion:      rrfFusion,
		learnedWeights: learnedWeights,
		timeout:        timeout,
		metricsTracker: newMetricsTracker(),
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

// detectDomain attempts to determine the domain from query context.
func (c *HybridQueryCoordinator) detectDomain(query *HybridQuery) domain.Domain {
	// Check filters for domain specification
	for _, filter := range query.Filters {
		if filter.Type == FilterDomain {
			if d, ok := filter.Value.(domain.Domain); ok {
				return d
			}
			if dStr, ok := filter.Value.(string); ok {
				if d, ok := domain.ParseDomain(dStr); ok {
					return d
				}
			}
		}
	}

	// Default to Librarian domain
	return domain.DomainLibrarian
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
