package coordinator

import (
	"container/heap"
	"context"
	"sort"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// SearchCoordinator
// =============================================================================

// SearchCoordinator orchestrates hybrid search combining Bleve and VectorDB.
type SearchCoordinator struct {
	bleve     BleveSearcher
	vector    VectorSearcher
	embedder  EmbeddingGenerator
	config    CoordinatorConfig
	mu        sync.RWMutex
	closed    bool
	semaphore chan struct{}
}

// NewSearchCoordinator creates a new SearchCoordinator.
func NewSearchCoordinator(
	bleve BleveSearcher,
	vector VectorSearcher,
	embedder EmbeddingGenerator,
) *SearchCoordinator {
	return NewSearchCoordinatorWithConfig(
		bleve, vector, embedder, DefaultCoordinatorConfig(),
	)
}

// NewSearchCoordinatorWithConfig creates a SearchCoordinator with custom config.
func NewSearchCoordinatorWithConfig(
	bleve BleveSearcher,
	vector VectorSearcher,
	embedder EmbeddingGenerator,
	config CoordinatorConfig,
) *SearchCoordinator {
	_ = config.Validate()

	return &SearchCoordinator{
		bleve:     bleve,
		vector:    vector,
		embedder:  embedder,
		config:    config,
		semaphore: make(chan struct{}, config.MaxConcurrentSearches),
	}
}

// Search performs a hybrid search combining Bleve and Vector results.
func (c *SearchCoordinator) Search(
	ctx context.Context,
	req *HybridSearchRequest,
) (*HybridSearchResult, error) {
	if err := c.validateSearch(req); err != nil {
		return nil, err
	}

	return c.executeSearch(ctx, req)
}

// validateSearch checks coordinator state and validates request.
func (c *SearchCoordinator) validateSearch(req *HybridSearchRequest) error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return ErrCoordinatorClosed
	}

	return req.ValidateAndNormalize()
}

// executeSearch performs the parallel search and fusion.
func (c *SearchCoordinator) executeSearch(
	ctx context.Context,
	req *HybridSearchRequest,
) (*HybridSearchResult, error) {
	startTime := time.Now()

	// Apply timeout
	searchCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	// Acquire semaphore
	if err := c.acquireSemaphore(searchCtx); err != nil {
		return nil, err
	}
	defer c.releaseSemaphore()

	// Execute parallel searches
	bleveResult, vectorResult := c.searchParallel(searchCtx, req)

	// Build result with fusion
	return c.buildResult(req, bleveResult, vectorResult, startTime)
}

// acquireSemaphore acquires a slot from the semaphore.
// W4M.16: Supports fail-fast backpressure when enabled.
func (c *SearchCoordinator) acquireSemaphore(ctx context.Context) error {
	// W4M.16: Fail-fast backpressure mode
	if c.config.EnableBackpressure {
		select {
		case c.semaphore <- struct{}{}:
			return nil
		default:
			// Queue is full, fail fast instead of blocking
			return ErrSearchQueueFull
		}
	}

	// Standard blocking mode with timeout
	select {
	case c.semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrSearchTimeout
	}
}

// releaseSemaphore releases a slot back to the semaphore.
func (c *SearchCoordinator) releaseSemaphore() {
	<-c.semaphore
}

// =============================================================================
// Parallel Search Execution
// =============================================================================

// searchResult holds the result from a single search backend.
type searchResult struct {
	documents []search.ScoredDocument
	duration  time.Duration
	err       error
}

// searchParallel executes Bleve and Vector searches concurrently.
func (c *SearchCoordinator) searchParallel(
	ctx context.Context,
	req *HybridSearchRequest,
) (searchResult, searchResult) {
	var wg sync.WaitGroup
	wg.Add(2)

	var bleveResult, vectorResult searchResult

	go func() {
		defer wg.Done()
		bleveResult = c.searchBleve(ctx, req)
	}()

	go func() {
		defer wg.Done()
		vectorResult = c.searchVector(ctx, req)
	}()

	wg.Wait()
	return bleveResult, vectorResult
}

// searchBleve performs the Bleve full-text search.
func (c *SearchCoordinator) searchBleve(
	ctx context.Context,
	req *HybridSearchRequest,
) searchResult {
	startTime := time.Now()

	if c.bleve == nil || !c.bleve.IsOpen() {
		return searchResult{err: ErrCoordinatorClosed}
	}

	bleveReq := c.buildBleveRequest(req)
	result, err := c.bleve.Search(ctx, bleveReq)

	return c.processBleveResult(result, err, startTime)
}

// buildBleveRequest creates a Bleve search request from the hybrid request.
func (c *SearchCoordinator) buildBleveRequest(req *HybridSearchRequest) *search.SearchRequest {
	bleveReq := &search.SearchRequest{
		Query:  req.Query,
		Limit:  capFusionLimit(req.Limit),
		Offset: 0, // Always start from 0 for fusion
	}

	if req.Filters != nil && len(req.Filters.Types) > 0 {
		bleveReq.Type = req.Filters.Types[0]
	}
	if req.Filters != nil && req.Filters.PathPrefix != "" {
		bleveReq.PathFilter = req.Filters.PathPrefix
	}

	return bleveReq
}

// capFusionLimit calculates the fusion limit, capping to prevent unbounded allocation.
func capFusionLimit(limit int) int {
	fusionLimit := limit * DefaultFusionMultiplier
	if fusionLimit > MaxFusionOverfetch {
		return MaxFusionOverfetch
	}
	return fusionLimit
}

// processBleveResult converts Bleve results to the internal format.
func (c *SearchCoordinator) processBleveResult(
	result *search.SearchResult,
	err error,
	startTime time.Time,
) searchResult {
	if err != nil {
		return searchResult{
			duration: time.Since(startTime),
			err:      err,
		}
	}

	return searchResult{
		documents: result.Documents,
		duration:  time.Since(startTime),
	}
}

// searchVector performs the Vector semantic search.
func (c *SearchCoordinator) searchVector(
	ctx context.Context,
	req *HybridSearchRequest,
) searchResult {
	startTime := time.Now()

	if c.vector == nil {
		return searchResult{err: ErrCoordinatorClosed}
	}

	queryVector, err := c.getQueryVector(ctx, req)
	if err != nil {
		return searchResult{duration: time.Since(startTime), err: err}
	}

	return c.executeVectorSearch(ctx, queryVector, capFusionLimit(req.Limit), startTime)
}

// getQueryVector gets or generates the query embedding.
func (c *SearchCoordinator) getQueryVector(
	ctx context.Context,
	req *HybridSearchRequest,
) ([]float32, error) {
	if len(req.QueryVector) > 0 {
		return req.QueryVector, nil
	}

	if c.embedder == nil {
		return nil, ErrCoordinatorClosed
	}

	return c.embedder.Generate(ctx, req.Query)
}

// executeVectorSearch runs the vector search and processes results.
func (c *SearchCoordinator) executeVectorSearch(
	ctx context.Context,
	queryVector []float32,
	limit int,
	startTime time.Time,
) searchResult {
	results, err := c.vector.Search(ctx, queryVector, limit)
	if err != nil {
		return searchResult{duration: time.Since(startTime), err: err}
	}

	docs := make([]search.ScoredDocument, 0, len(results))
	for _, r := range results {
		docs = append(docs, r.ToScoredDocument())
	}

	return searchResult{
		documents: docs,
		duration:  time.Since(startTime),
	}
}

// =============================================================================
// Result Building and Fusion
// =============================================================================

// buildResult constructs the final HybridSearchResult with fusion.
func (c *SearchCoordinator) buildResult(
	req *HybridSearchRequest,
	bleveResult, vectorResult searchResult,
	startTime time.Time,
) (*HybridSearchResult, error) {
	// Check if both failed
	if bleveResult.err != nil && vectorResult.err != nil {
		if !c.config.EnableFallback {
			return nil, ErrBothSearchersFailed
		}
	}

	fusionStart := time.Now()
	fused := c.fuseResults(req, bleveResult.documents, vectorResult.documents)
	fusionDuration := time.Since(fusionStart)

	result := c.assembleResult(req, bleveResult, vectorResult, fused, fusionDuration, startTime)

	return result, nil
}

// assembleResult creates the final result structure.
func (c *SearchCoordinator) assembleResult(
	req *HybridSearchRequest,
	bleveResult, vectorResult searchResult,
	fused []search.ScoredDocument,
	fusionDuration time.Duration,
	startTime time.Time,
) *HybridSearchResult {
	result := &HybridSearchResult{
		FusedResults: fused,
		FusionMethod: req.FusionMethod,
		Query:        req.Query,
		Metadata: SearchMetadata{
			TotalTime:  time.Since(startTime),
			BleveTime:  bleveResult.duration,
			VectorTime: vectorResult.duration,
			FusionTime: fusionDuration,
			BleveHits:  len(bleveResult.documents),
			VectorHits: len(vectorResult.documents),
			FusedHits:  len(fused),
		},
	}

	c.populateErrors(result, bleveResult, vectorResult)
	c.populateRawResults(result, req, bleveResult, vectorResult)

	return result
}

// populateErrors adds error information to the result metadata.
func (c *SearchCoordinator) populateErrors(
	result *HybridSearchResult,
	bleveResult, vectorResult searchResult,
) {
	if bleveResult.err != nil {
		result.Metadata.BleveFailed = true
		result.Metadata.BleveError = bleveResult.err.Error()
	}
	if vectorResult.err != nil {
		result.Metadata.VectorFailed = true
		result.Metadata.VectorError = vectorResult.err.Error()
	}
}

// populateRawResults adds raw results if requested.
func (c *SearchCoordinator) populateRawResults(
	result *HybridSearchResult,
	req *HybridSearchRequest,
	bleveResult, vectorResult searchResult,
) {
	if req.IncludeBleveResults {
		result.BleveResults = bleveResult.documents
	}
	if req.IncludeVectorResults {
		result.VectorResults = vectorResult.documents
	}
}

// =============================================================================
// Fusion Algorithms
// =============================================================================

// fuseResults combines results using the specified fusion method.
func (c *SearchCoordinator) fuseResults(
	req *HybridSearchRequest,
	bleveResults, vectorResults []search.ScoredDocument,
) []search.ScoredDocument {
	switch req.FusionMethod {
	case FusionLinear:
		return c.fuseLinear(bleveResults, vectorResults, req)
	case FusionMax:
		return c.fuseMax(bleveResults, vectorResults, req)
	default:
		return c.fuseRRF(bleveResults, vectorResults, req)
	}
}

// fuseRRF implements Reciprocal Rank Fusion.
func (c *SearchCoordinator) fuseRRF(
	bleveResults, vectorResults []search.ScoredDocument,
	req *HybridSearchRequest,
) []search.ScoredDocument {
	scores := make(map[string]float64)
	docs := make(map[string]search.ScoredDocument)
	k := float64(c.config.RRFK)

	// Process Bleve results
	for rank, doc := range bleveResults {
		scores[doc.ID] += req.BleveWeight / (k + float64(rank+1))
		docs[doc.ID] = doc
	}

	// Process Vector results
	for rank, doc := range vectorResults {
		scores[doc.ID] += req.VectorWeight / (k + float64(rank+1))
		if _, exists := docs[doc.ID]; !exists {
			docs[doc.ID] = doc
		}
	}

	return c.sortByScores(docs, scores, req.Limit)
}

// fuseLinear implements weighted linear combination.
func (c *SearchCoordinator) fuseLinear(
	bleveResults, vectorResults []search.ScoredDocument,
	req *HybridSearchRequest,
) []search.ScoredDocument {
	scores := make(map[string]float64)
	docs := make(map[string]search.ScoredDocument)

	// Normalize and weight Bleve scores
	bleveMax := c.maxScore(bleveResults)
	for _, doc := range bleveResults {
		normalized := c.normalizeScore(doc.Score, bleveMax)
		scores[doc.ID] += normalized * req.BleveWeight
		docs[doc.ID] = doc
	}

	// Normalize and weight Vector scores
	vectorMax := c.maxScore(vectorResults)
	for _, doc := range vectorResults {
		normalized := c.normalizeScore(doc.Score, vectorMax)
		scores[doc.ID] += normalized * req.VectorWeight
		if _, exists := docs[doc.ID]; !exists {
			docs[doc.ID] = doc
		}
	}

	return c.sortByScores(docs, scores, req.Limit)
}

// fuseMax takes the maximum score from either source.
func (c *SearchCoordinator) fuseMax(
	bleveResults, vectorResults []search.ScoredDocument,
	req *HybridSearchRequest,
) []search.ScoredDocument {
	scores := make(map[string]float64)
	docs := make(map[string]search.ScoredDocument)

	// Normalize Bleve scores
	bleveMax := c.maxScore(bleveResults)
	for _, doc := range bleveResults {
		normalized := c.normalizeScore(doc.Score, bleveMax) * req.BleveWeight
		scores[doc.ID] = normalized
		docs[doc.ID] = doc
	}

	// Normalize Vector scores and take max
	vectorMax := c.maxScore(vectorResults)
	for _, doc := range vectorResults {
		normalized := c.normalizeScore(doc.Score, vectorMax) * req.VectorWeight
		if normalized > scores[doc.ID] {
			scores[doc.ID] = normalized
		}
		if _, exists := docs[doc.ID]; !exists {
			docs[doc.ID] = doc
		}
	}

	return c.sortByScores(docs, scores, req.Limit)
}

// maxScore returns the maximum score from results.
func (c *SearchCoordinator) maxScore(results []search.ScoredDocument) float64 {
	if len(results) == 0 {
		return 1.0
	}
	maxVal := results[0].Score
	for _, doc := range results[1:] {
		if doc.Score > maxVal {
			maxVal = doc.Score
		}
	}
	if maxVal == 0 {
		return 1.0
	}
	return maxVal
}

// normalizeScore normalizes a score to 0-1 range.
func (c *SearchCoordinator) normalizeScore(score, maxScore float64) float64 {
	if maxScore == 0 {
		return 0
	}
	return score / maxScore
}

// sortByScores sorts documents by their fused scores using efficient top-K selection.
func (c *SearchCoordinator) sortByScores(
	docs map[string]search.ScoredDocument,
	scores map[string]float64,
	limit int,
) []search.ScoredDocument {
	// Prepare scored documents
	items := make([]search.ScoredDocument, 0, len(docs))
	for id, doc := range docs {
		doc.Score = scores[id]
		items = append(items, doc)
	}

	// If no limit or items fit within limit, do a full sort
	if limit <= 0 || len(items) <= limit {
		sort.Slice(items, func(i, j int) bool {
			return items[i].Score > items[j].Score
		})
		return items
	}

	// Use heap for efficient top-K selection
	return selectTopK(items, limit)
}

// =============================================================================
// Heap-based Top-K Selection
// =============================================================================

// scoredDocHeap is a min-heap of scored documents (smallest score at top).
// This allows efficient top-K selection: we maintain K items, and when a new
// item comes in, we only add it if it's larger than the smallest in the heap.
type scoredDocHeap []search.ScoredDocument

func (h scoredDocHeap) Len() int           { return len(h) }
func (h scoredDocHeap) Less(i, j int) bool { return h[i].Score < h[j].Score } // min-heap
func (h scoredDocHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *scoredDocHeap) Push(x interface{}) {
	*h = append(*h, x.(search.ScoredDocument))
}

func (h *scoredDocHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// selectTopK efficiently selects the top K documents by score.
// Uses a min-heap to avoid a full sort when K << len(items).
// Time complexity: O(n log k) vs O(n log n) for full sort.
func selectTopK(items []search.ScoredDocument, k int) []search.ScoredDocument {
	if k <= 0 {
		return nil
	}
	if len(items) <= k {
		sort.Slice(items, func(i, j int) bool {
			return items[i].Score > items[j].Score
		})
		return items
	}

	// Initialize heap with first k items
	h := make(scoredDocHeap, k)
	copy(h, items[:k])
	heap.Init(&h)

	// Process remaining items
	for i := k; i < len(items); i++ {
		// If current item has higher score than min in heap, replace
		if items[i].Score > h[0].Score {
			h[0] = items[i]
			heap.Fix(&h, 0)
		}
	}

	// Extract items from heap in reverse order (highest to lowest score)
	result := make([]search.ScoredDocument, k)
	for i := k - 1; i >= 0; i-- {
		result[i] = heap.Pop(&h).(search.ScoredDocument)
	}

	return result
}

// =============================================================================
// Lifecycle
// =============================================================================

// Close shuts down the coordinator.
func (c *SearchCoordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	close(c.semaphore)
	return nil
}

// IsClosed returns true if the coordinator has been closed.
func (c *SearchCoordinator) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// Config returns the coordinator configuration.
func (c *SearchCoordinator) Config() CoordinatorConfig {
	return c.config
}
