package guide

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Request Batching
// =============================================================================

// BatchProcessor handles batching of route requests
type BatchProcessor struct {
	mu sync.Mutex

	// Configuration
	config BatchConfig

	// Pending batches by target agent
	batches map[string]*RequestBatch

	// Parent guide for routing
	guide *Guide

	// Processing state
	running atomic.Bool
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc

	// Statistics
	stats BatchStats
}

// BatchConfig configures the batch processor
type BatchConfig struct {
	// Maximum batch size before flushing
	MaxBatchSize int `json:"max_batch_size"`

	// Maximum wait time before flushing
	MaxWaitTime time.Duration `json:"max_wait_time"`

	// Whether to auto-flush periodically
	AutoFlush bool `json:"auto_flush"`

	// Flush interval (if AutoFlush is true)
	FlushInterval time.Duration `json:"flush_interval"`

	// Maximum batches per agent
	MaxBatchesPerAgent int `json:"max_batches_per_agent"`

	// Process batches in parallel
	ParallelProcessing bool `json:"parallel_processing"`

	// Maximum parallel batches
	MaxParallelBatches int `json:"max_parallel_batches"`
}

// DefaultBatchConfig returns sensible defaults
func DefaultBatchConfig() BatchConfig {
	return BatchConfig{
		MaxBatchSize:       10,
		MaxWaitTime:        100 * time.Millisecond,
		AutoFlush:          true,
		FlushInterval:      50 * time.Millisecond,
		MaxBatchesPerAgent: 100,
		ParallelProcessing: true,
		MaxParallelBatches: 4,
	}
}

// RequestBatch contains a batch of requests for the same target
type RequestBatch struct {
	mu sync.Mutex

	// Target agent
	TargetAgentID string `json:"target_agent_id"`

	// Batched requests
	Requests []*BatchedRequest `json:"requests"`

	// Creation time (for timeout calculation)
	CreatedAt time.Time `json:"created_at"`

	// Whether this batch is being processed
	Processing bool `json:"processing"`
}

// BatchedRequest wraps a request with its result channel
type BatchedRequest struct {
	Request *RouteRequest     `json:"request"`
	Result  chan *BatchResult `json:"-"`
	Ctx     context.Context   `json:"-"`
	AddedAt time.Time         `json:"added_at"`
}

// BatchResult contains the result of processing a batched request
type BatchResult struct {
	Request   *RouteRequest     `json:"request"`
	Forwarded *ForwardedRequest `json:"forwarded,omitempty"`
	Error     error             `json:"error,omitempty"`
	Duration  time.Duration     `json:"duration"`
}

// BatchStats contains batch processor statistics
type BatchStats struct {
	TotalBatches     int64   `json:"total_batches"`
	TotalRequests    int64   `json:"total_requests"`
	ProcessedBatches int64   `json:"processed_batches"`
	ProcessedReqs    int64   `json:"processed_requests"`
	FailedRequests   int64   `json:"failed_requests"`
	DroppedRequests  int64   `json:"dropped_requests"`
	AvgBatchSize     float64 `json:"avg_batch_size"`
	AvgWaitTime      float64 `json:"avg_wait_time_ms"`
}

// NewBatchProcessor creates a new batch processor
func NewBatchProcessor(guide *Guide, config BatchConfig) *BatchProcessor {
	if config.MaxBatchSize <= 0 {
		config = DefaultBatchConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &BatchProcessor{
		config:  config,
		batches: make(map[string]*RequestBatch),
		guide:   guide,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// =============================================================================
// Lifecycle
// =============================================================================

// Start begins the batch processor
func (bp *BatchProcessor) Start() {
	if bp.running.Swap(true) {
		return
	}

	if bp.config.AutoFlush {
		bp.wg.Add(1)
		go bp.flushWorker()
	}
}

// Stop halts the batch processor
func (bp *BatchProcessor) Stop() {
	if !bp.running.Swap(false) {
		return
	}

	bp.cancel()
	bp.FlushAll()
	bp.wg.Wait()
}

// flushWorker periodically flushes ready batches
func (bp *BatchProcessor) flushWorker() {
	defer bp.wg.Done()

	ticker := time.NewTicker(bp.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bp.ctx.Done():
			return
		case <-ticker.C:
			bp.flushReady()
		}
	}
}

// =============================================================================
// Batching Operations
// =============================================================================

// Add adds a request to a batch, returning a channel for the result
func (bp *BatchProcessor) Add(ctx context.Context, request *RouteRequest) <-chan *BatchResult {
	resultCh := make(chan *BatchResult, 1)

	// Route to determine target
	forwarded, err := bp.guide.Route(ctx, request)
	if err != nil {
		resultCh <- &BatchResult{
			Request: request,
			Error:   err,
		}
		close(resultCh)
		return resultCh
	}

	// Get target from pending request
	pending := bp.guide.GetPending(forwarded.CorrelationID)
	if pending == nil {
		resultCh <- &BatchResult{
			Request:   request,
			Forwarded: forwarded,
		}
		close(resultCh)
		return resultCh
	}

	targetAgentID := pending.TargetAgentID

	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Get or create batch for target
	batch, ok := bp.batches[targetAgentID]
	if !ok || batch.Processing {
		batch = &RequestBatch{
			TargetAgentID: targetAgentID,
			Requests:      make([]*BatchedRequest, 0, bp.config.MaxBatchSize),
			CreatedAt:     time.Now(),
		}
		bp.batches[targetAgentID] = batch
		atomic.AddInt64(&bp.stats.TotalBatches, 1)
	}

	// Add to batch
	batched := &BatchedRequest{
		Request: request,
		Result:  resultCh,
		Ctx:     ctx,
		AddedAt: time.Now(),
	}

	batch.mu.Lock()
	batch.Requests = append(batch.Requests, batched)
	batchSize := len(batch.Requests)
	batch.mu.Unlock()

	atomic.AddInt64(&bp.stats.TotalRequests, 1)

	// Check if batch should be flushed
	if batchSize >= bp.config.MaxBatchSize {
		go bp.flushBatch(targetAgentID)
	}

	return resultCh
}

// AddSync adds a request and waits for the result
func (bp *BatchProcessor) AddSync(ctx context.Context, request *RouteRequest) (*BatchResult, error) {
	resultCh := bp.Add(ctx, request)

	select {
	case result := <-resultCh:
		return result, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AddBatch adds multiple requests as a batch
func (bp *BatchProcessor) AddBatch(ctx context.Context, requests []*RouteRequest) []<-chan *BatchResult {
	results := make([]<-chan *BatchResult, len(requests))
	for i, req := range requests {
		results[i] = bp.Add(ctx, req)
	}
	return results
}

// AddBatchSync adds multiple requests and waits for all results
func (bp *BatchProcessor) AddBatchSync(ctx context.Context, requests []*RouteRequest) ([]*BatchResult, error) {
	channels := bp.AddBatch(ctx, requests)
	results := make([]*BatchResult, len(channels))

	for i, ch := range channels {
		select {
		case result := <-ch:
			results[i] = result
		case <-ctx.Done():
			return results, ctx.Err()
		}
	}

	return results, nil
}

// =============================================================================
// Flushing
// =============================================================================

// flushReady flushes batches that are ready
func (bp *BatchProcessor) flushReady() {
	readyBatches := bp.collectReadyBatches(time.Now())
	bp.flushReadyBatches(readyBatches)
}

func (bp *BatchProcessor) collectReadyBatches(now time.Time) []string {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	readyBatches := make([]string, 0)
	for targetID, batch := range bp.batches {
		if bp.batchReady(batch, now) {
			readyBatches = append(readyBatches, targetID)
		}
	}
	return readyBatches
}

func (bp *BatchProcessor) batchReady(batch *RequestBatch, now time.Time) bool {
	batch.mu.Lock()
	defer batch.mu.Unlock()

	if batch.Processing {
		return false
	}
	if len(batch.Requests) >= bp.config.MaxBatchSize {
		return true
	}
	return now.Sub(batch.CreatedAt) >= bp.config.MaxWaitTime
}

func (bp *BatchProcessor) flushReadyBatches(readyBatches []string) {
	if bp.config.ParallelProcessing {
		bp.flushReadyBatchesParallel(readyBatches)
		return
	}
	bp.flushReadyBatchesSequential(readyBatches)
}

func (bp *BatchProcessor) flushReadyBatchesParallel(readyBatches []string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, bp.config.MaxParallelBatches)
	for _, targetID := range readyBatches {
		sem <- struct{}{}
		wg.Add(1)
		go func(id string) {
			defer func() {
				<-sem
				wg.Done()
			}()
			bp.flushBatch(id)
		}(targetID)
	}
	wg.Wait()
}

func (bp *BatchProcessor) flushReadyBatchesSequential(readyBatches []string) {
	for _, targetID := range readyBatches {
		bp.flushBatch(targetID)
	}
}

// flushBatch flushes a specific batch
func (bp *BatchProcessor) flushBatch(targetAgentID string) {
	bp.mu.Lock()
	batch, ok := bp.batches[targetAgentID]
	if !ok {
		bp.mu.Unlock()
		return
	}

	batch.mu.Lock()
	if batch.Processing || len(batch.Requests) == 0 {
		batch.mu.Unlock()
		bp.mu.Unlock()
		return
	}

	batch.Processing = true
	requests := batch.Requests
	batch.Requests = nil
	batch.mu.Unlock()

	// Remove from pending batches
	delete(bp.batches, targetAgentID)
	bp.mu.Unlock()

	// Process the batch
	bp.processBatch(targetAgentID, requests)
}

// FlushAll flushes all pending batches
func (bp *BatchProcessor) FlushAll() {
	bp.mu.Lock()
	targets := make([]string, 0, len(bp.batches))
	for target := range bp.batches {
		targets = append(targets, target)
	}
	bp.mu.Unlock()

	for _, target := range targets {
		bp.flushBatch(target)
	}
}

// processBatch processes a batch of requests
func (bp *BatchProcessor) processBatch(targetAgentID string, requests []*BatchedRequest) {
	if len(requests) == 0 {
		return
	}

	atomic.AddInt64(&bp.stats.ProcessedBatches, 1)

	// Calculate average wait time
	var totalWait time.Duration
	for _, req := range requests {
		totalWait += time.Since(req.AddedAt)
	}

	// Process each request
	for _, batched := range requests {
		start := time.Now()

		// Already routed in Add(), just send result
		forwarded, err := bp.guide.Route(batched.Ctx, batched.Request)

		result := &BatchResult{
			Request:   batched.Request,
			Forwarded: forwarded,
			Error:     err,
			Duration:  time.Since(start),
		}

		if err != nil {
			atomic.AddInt64(&bp.stats.FailedRequests, 1)
		} else {
			atomic.AddInt64(&bp.stats.ProcessedReqs, 1)
		}

		// Send result
		select {
		case batched.Result <- result:
		default:
			atomic.AddInt64(&bp.stats.DroppedRequests, 1)
		}
		close(batched.Result)
	}

	// Update stats
	totalBatches := atomic.LoadInt64(&bp.stats.ProcessedBatches)
	if totalBatches > 0 {
		bp.stats.AvgBatchSize = float64(atomic.LoadInt64(&bp.stats.ProcessedReqs)) / float64(totalBatches)
	}
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns batch processor statistics
func (bp *BatchProcessor) Stats() BatchStats {
	return BatchStats{
		TotalBatches:     atomic.LoadInt64(&bp.stats.TotalBatches),
		TotalRequests:    atomic.LoadInt64(&bp.stats.TotalRequests),
		ProcessedBatches: atomic.LoadInt64(&bp.stats.ProcessedBatches),
		ProcessedReqs:    atomic.LoadInt64(&bp.stats.ProcessedReqs),
		FailedRequests:   atomic.LoadInt64(&bp.stats.FailedRequests),
		DroppedRequests:  atomic.LoadInt64(&bp.stats.DroppedRequests),
		AvgBatchSize:     bp.stats.AvgBatchSize,
		AvgWaitTime:      bp.stats.AvgWaitTime,
	}
}

// PendingCount returns the number of pending batches
func (bp *BatchProcessor) PendingCount() int {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return len(bp.batches)
}

// PendingRequestCount returns the total number of pending requests
func (bp *BatchProcessor) PendingRequestCount() int {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	total := 0
	for _, batch := range bp.batches {
		batch.mu.Lock()
		total += len(batch.Requests)
		batch.mu.Unlock()
	}
	return total
}
