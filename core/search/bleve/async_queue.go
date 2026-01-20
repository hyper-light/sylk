// Package bleve provides Bleve index management for the Sylk Document Search System.
package bleve

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrQueueFull indicates the queue has reached capacity and cannot accept more operations.
	ErrQueueFull = errors.New("async index queue is full")

	// ErrQueueClosed indicates an operation was attempted on a closed queue.
	ErrQueueClosed = errors.New("async index queue is closed")

	// ErrNilOperation indicates a nil operation was submitted.
	ErrNilOperation = errors.New("operation cannot be nil")

	// ErrRetriesExhausted indicates all retry attempts failed.
	ErrRetriesExhausted = errors.New("all retry attempts exhausted")
)

// =============================================================================
// Operation Types
// =============================================================================

// OperationType represents the type of index operation.
type OperationType int

const (
	// OpIndex represents an index (add/update) operation.
	OpIndex OperationType = iota

	// OpDelete represents a delete operation.
	OpDelete

	// OpBatch represents a batch operation (internal use).
	OpBatch
)

// String returns a string representation of the operation type.
func (ot OperationType) String() string {
	switch ot {
	case OpIndex:
		return "index"
	case OpDelete:
		return "delete"
	case OpBatch:
		return "batch"
	default:
		return "unknown"
	}
}

// =============================================================================
// IndexOperation
// =============================================================================

// IndexOperation represents a pending index operation.
type IndexOperation struct {
	Type      OperationType
	DocID     string
	Document  interface{}
	ResultCh  chan error
	CreatedAt time.Time
}

// NewIndexOperation creates a new index operation.
func NewIndexOperation(opType OperationType, docID string, doc interface{}) *IndexOperation {
	return &IndexOperation{
		Type:      opType,
		DocID:     docID,
		Document:  doc,
		ResultCh:  make(chan error, 1),
		CreatedAt: time.Now(),
	}
}

// =============================================================================
// AsyncIndexQueueConfig
// =============================================================================

// AsyncIndexQueueConfig configures the async index queue.
type AsyncIndexQueueConfig struct {
	// MaxQueueSize is the maximum number of pending operations (default 10000).
	MaxQueueSize int

	// BatchSize is the number of operations to batch before committing (default 100).
	BatchSize int

	// FlushInterval is the maximum time before forcing a flush (default 100ms).
	FlushInterval time.Duration

	// Workers is the number of worker goroutines processing operations (default 2).
	Workers int

	// MaxRetries is the number of retry attempts for failed submissions (default 3).
	MaxRetries int

	// RetryBaseDelay is the initial backoff delay for retries (default 10ms).
	RetryBaseDelay time.Duration

	// RetryMaxDelay caps the exponential backoff delay (default 100ms).
	RetryMaxDelay time.Duration
}

// DefaultAsyncIndexQueueConfig returns sensible defaults for the queue configuration.
func DefaultAsyncIndexQueueConfig() AsyncIndexQueueConfig {
	return AsyncIndexQueueConfig{
		MaxQueueSize:   10000,
		BatchSize:      100,
		FlushInterval:  100 * time.Millisecond,
		Workers:        2,
		MaxRetries:     3,
		RetryBaseDelay: 10 * time.Millisecond,
		RetryMaxDelay:  100 * time.Millisecond,
	}
}

// validate ensures the configuration has valid values, applying defaults where needed.
func (c *AsyncIndexQueueConfig) validate() {
	if c.MaxQueueSize <= 0 {
		c.MaxQueueSize = 10000
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 100 * time.Millisecond
	}
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 3
	}
	if c.RetryBaseDelay <= 0 {
		c.RetryBaseDelay = 10 * time.Millisecond
	}
	if c.RetryMaxDelay <= 0 {
		c.RetryMaxDelay = 100 * time.Millisecond
	}
}

// =============================================================================
// AsyncIndexQueueStats
// =============================================================================

// AsyncIndexQueueStats contains queue statistics.
type AsyncIndexQueueStats struct {
	// QueueLength is the current number of pending operations.
	QueueLength int

	// Enqueued is the total number of operations enqueued.
	Enqueued int64

	// Processed is the total number of operations successfully processed.
	Processed int64

	// Dropped is the total number of operations dropped (e.g., due to errors).
	Dropped int64

	// BatchesProcessed is the total number of batches committed.
	BatchesProcessed int64

	// Retried is the total number of retry attempts made.
	Retried int64
}

// =============================================================================
// AsyncIndexQueue
// =============================================================================

// OverflowCallback is invoked when an operation fails permanently after all retries.
type OverflowCallback func(op *IndexOperation, err error)

// AsyncIndexQueue manages asynchronous indexing operations.
// It batches operations before committing to Bleve for better performance.
type AsyncIndexQueue struct {
	queue chan *IndexOperation

	mu      sync.Mutex
	pending []*IndexOperation

	config AsyncIndexQueueConfig

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// closeMu protects the queue channel from concurrent close/send.
	// It must be held (RLock for send, Lock for close) to avoid races.
	closeMu sync.RWMutex
	closed  atomic.Bool

	// Callbacks for actual index operations
	indexFn  func(docID string, doc interface{}) error
	deleteFn func(docID string) error
	batchFn  func(ops []*IndexOperation) error

	// Overflow callback for permanent failures after retries exhausted
	overflowMu sync.RWMutex
	overflowFn OverflowCallback

	// Metrics
	enqueued         atomic.Int64
	processed        atomic.Int64
	dropped          atomic.Int64
	batchesProcessed atomic.Int64
	retried          atomic.Int64
}

// NewAsyncIndexQueue creates a new async indexing queue.
//
// Parameters:
//   - ctx: Parent context for the queue lifecycle
//   - config: Queue configuration
//   - indexFn: Function to call for indexing a single document
//   - deleteFn: Function to call for deleting a document
//   - batchFn: Optional function for batch operations (can be nil)
func NewAsyncIndexQueue(
	ctx context.Context,
	config AsyncIndexQueueConfig,
	indexFn func(docID string, doc interface{}) error,
	deleteFn func(docID string) error,
	batchFn func(ops []*IndexOperation) error,
) *AsyncIndexQueue {
	config.validate()

	queueCtx, cancel := context.WithCancel(ctx)

	q := &AsyncIndexQueue{
		queue:    make(chan *IndexOperation, config.MaxQueueSize),
		pending:  make([]*IndexOperation, 0, config.BatchSize),
		config:   config,
		ctx:      queueCtx,
		cancel:   cancel,
		indexFn:  indexFn,
		deleteFn: deleteFn,
		batchFn:  batchFn,
	}

	// Start the processor goroutine
	q.wg.Add(1)
	go q.processor()

	return q
}

// =============================================================================
// Submit Methods
// =============================================================================

// trySubmit attempts a single non-blocking submission to the queue.
// Returns ErrQueueClosed if the queue was closed, ErrQueueFull if full.
func (q *AsyncIndexQueue) trySubmit(op *IndexOperation) error {
	// Hold read lock to prevent Close() from closing channel during send
	q.closeMu.RLock()
	defer q.closeMu.RUnlock()

	// Check closed after acquiring lock
	if q.closed.Load() {
		return ErrQueueClosed
	}

	select {
	case q.queue <- op:
		q.enqueued.Add(1)
		return nil
	default:
		return ErrQueueFull
	}
}

// calculateBackoff returns the backoff duration for a given retry attempt.
func (q *AsyncIndexQueue) calculateBackoff(attempt int) time.Duration {
	delay := q.config.RetryBaseDelay << attempt // Exponential: base * 2^attempt
	if delay > q.config.RetryMaxDelay {
		return q.config.RetryMaxDelay
	}
	return delay
}

// Submit adds an operation to the queue with exponential backoff retry.
// Returns ErrRetriesExhausted if all retry attempts fail and invokes overflow callback.
func (q *AsyncIndexQueue) Submit(op *IndexOperation) error {
	if op == nil {
		return ErrNilOperation
	}

	if q.closed.Load() {
		return ErrQueueClosed
	}

	// Try immediate submission first
	err := q.trySubmit(op)
	if err == nil {
		return nil
	}
	if err == ErrQueueClosed {
		return ErrQueueClosed
	}

	// Retry with exponential backoff
	return q.retrySubmit(op)
}

// retrySubmit handles the retry loop for failed submissions.
func (q *AsyncIndexQueue) retrySubmit(op *IndexOperation) error {
	for attempt := 0; attempt < q.config.MaxRetries; attempt++ {
		// Check if queue closed during retry
		if q.closed.Load() {
			return ErrQueueClosed
		}

		// Wait with backoff
		delay := q.calculateBackoff(attempt)
		timer := time.NewTimer(delay)

		select {
		case <-timer.C:
			// Timer expired, try again
		case <-q.ctx.Done():
			timer.Stop()
			return ErrQueueClosed
		}

		q.retried.Add(1)

		// Attempt submission (trySubmit handles closed channel safely)
		err := q.trySubmit(op)
		if err == nil {
			return nil
		}
		if err == ErrQueueClosed {
			return ErrQueueClosed
		}
		// err == ErrQueueFull, continue retry loop
	}

	// All retries exhausted
	q.dropped.Add(1)
	q.invokeOverflowCallback(op, ErrRetriesExhausted)
	return ErrRetriesExhausted
}

// invokeOverflowCallback safely calls the overflow callback if set.
func (q *AsyncIndexQueue) invokeOverflowCallback(op *IndexOperation, err error) {
	q.overflowMu.RLock()
	fn := q.overflowFn
	q.overflowMu.RUnlock()

	if fn != nil {
		fn(op, err)
	}
}

// SubmitBlocking adds an operation to the queue, blocking if the queue is full.
// Returns ErrQueueClosed if the queue is closed while waiting.
func (q *AsyncIndexQueue) SubmitBlocking(op *IndexOperation) error {
	if op == nil {
		return ErrNilOperation
	}

	if q.closed.Load() {
		return ErrQueueClosed
	}

	select {
	case q.queue <- op:
		q.enqueued.Add(1)
		return nil
	case <-q.ctx.Done():
		return ErrQueueClosed
	}
}

// SubmitIndex is a convenience method for indexing a document.
// Returns a channel that will receive the result error (or nil on success).
func (q *AsyncIndexQueue) SubmitIndex(docID string, doc interface{}) <-chan error {
	op := NewIndexOperation(OpIndex, docID, doc)

	if err := q.Submit(op); err != nil {
		// Return error immediately on the channel
		op.ResultCh <- err
	}

	return op.ResultCh
}

// SubmitDelete is a convenience method for deleting a document.
// Returns a channel that will receive the result error (or nil on success).
func (q *AsyncIndexQueue) SubmitDelete(docID string) <-chan error {
	op := NewIndexOperation(OpDelete, docID, nil)

	if err := q.Submit(op); err != nil {
		// Return error immediately on the channel
		op.ResultCh <- err
	}

	return op.ResultCh
}

// SubmitIndexSync submits an index operation and waits for the result.
// This is useful when you need synchronous behavior but still want batching.
func (q *AsyncIndexQueue) SubmitIndexSync(ctx context.Context, docID string, doc interface{}) error {
	resultCh := q.SubmitIndex(docID, doc)

	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-q.ctx.Done():
		return ErrQueueClosed
	}
}

// SubmitDeleteSync submits a delete operation and waits for the result.
func (q *AsyncIndexQueue) SubmitDeleteSync(ctx context.Context, docID string) error {
	resultCh := q.SubmitDelete(docID)

	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-q.ctx.Done():
		return ErrQueueClosed
	}
}

// =============================================================================
// Flush and Close
// =============================================================================

// Flush forces immediate processing of all pending operations.
// Blocks until all currently queued operations are processed.
func (q *AsyncIndexQueue) Flush(ctx context.Context) error {
	if q.closed.Load() {
		return ErrQueueClosed
	}

	// Create a sentinel operation to mark the end of the flush
	sentinel := NewIndexOperation(OpBatch, "", nil)

	// Submit the sentinel (blocking to ensure it gets in)
	select {
	case q.queue <- sentinel:
	case <-ctx.Done():
		return ctx.Err()
	case <-q.ctx.Done():
		return ErrQueueClosed
	}

	// Wait for the sentinel to be processed
	select {
	case err := <-sentinel.ResultCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-q.ctx.Done():
		return ErrQueueClosed
	}
}

// Close gracefully shuts down the queue, processing all remaining items.
// Blocks until all operations are processed and workers have stopped.
func (q *AsyncIndexQueue) Close() error {
	// Acquire write lock to prevent any concurrent sends
	q.closeMu.Lock()
	if q.closed.Swap(true) {
		// Already closed
		q.closeMu.Unlock()
		return nil
	}
	// Close the input queue to signal processor to finish
	close(q.queue)
	q.closeMu.Unlock()

	// Wait for all goroutines to finish
	q.wg.Wait()

	return nil
}

// =============================================================================
// Stats
// =============================================================================

// Stats returns current queue statistics.
func (q *AsyncIndexQueue) Stats() AsyncIndexQueueStats {
	return AsyncIndexQueueStats{
		QueueLength:      len(q.queue),
		Enqueued:         q.enqueued.Load(),
		Processed:        q.processed.Load(),
		Dropped:          q.dropped.Load(),
		BatchesProcessed: q.batchesProcessed.Load(),
		Retried:          q.retried.Load(),
	}
}

// IsClosed returns true if the queue has been closed.
func (q *AsyncIndexQueue) IsClosed() bool {
	return q.closed.Load()
}

// SetOverflowCallback sets the callback invoked when operations fail permanently.
// The callback is invoked synchronously when all retries are exhausted.
// Pass nil to clear the callback.
func (q *AsyncIndexQueue) SetOverflowCallback(fn OverflowCallback) {
	q.overflowMu.Lock()
	defer q.overflowMu.Unlock()
	q.overflowFn = fn
}

// SubmitNoRetry adds an operation without retry (original behavior).
// Returns ErrQueueFull immediately if the queue is full.
func (q *AsyncIndexQueue) SubmitNoRetry(op *IndexOperation) error {
	if op == nil {
		return ErrNilOperation
	}

	if q.closed.Load() {
		return ErrQueueClosed
	}

	err := q.trySubmit(op)
	if err == nil {
		return nil
	}
	if err == ErrQueueClosed {
		return ErrQueueClosed
	}
	// ErrQueueFull
	q.dropped.Add(1)
	return err
}

// =============================================================================
// Internal: Processor
// =============================================================================

// processor collects operations and processes them in batches.
func (q *AsyncIndexQueue) processor() {
	defer q.wg.Done()

	flushTimer := time.NewTimer(q.config.FlushInterval)
	defer flushTimer.Stop()

	for {
		select {
		case op, ok := <-q.queue:
			if !ok {
				// Queue closed, flush remaining and exit
				q.flushPendingSync()
				return
			}

			// Handle sentinel (flush) operations
			if op.Type == OpBatch && op.DocID == "" {
				q.flushPendingSync()
				op.ResultCh <- nil
				continue
			}

			// Add to pending batch
			q.mu.Lock()
			q.pending = append(q.pending, op)
			shouldFlush := len(q.pending) >= q.config.BatchSize
			q.mu.Unlock()

			if shouldFlush {
				q.flushPendingSync()
				// Reset timer after flush
				if !flushTimer.Stop() {
					select {
					case <-flushTimer.C:
					default:
					}
				}
				flushTimer.Reset(q.config.FlushInterval)
			}

		case <-flushTimer.C:
			q.flushPendingSync()
			flushTimer.Reset(q.config.FlushInterval)

		case <-q.ctx.Done():
			// Context cancelled, flush remaining and exit
			q.flushPendingSync()
			return
		}
	}
}

// flushPendingSync processes the current pending batch synchronously.
func (q *AsyncIndexQueue) flushPendingSync() {
	q.mu.Lock()
	if len(q.pending) == 0 {
		q.mu.Unlock()
		return
	}

	batch := q.pending
	q.pending = make([]*IndexOperation, 0, q.config.BatchSize)
	q.mu.Unlock()

	q.processBatch(batch)
}

// processBatch processes a batch of operations.
func (q *AsyncIndexQueue) processBatch(ops []*IndexOperation) error {
	if len(ops) == 0 {
		return nil
	}

	// Try batch function first if available
	if q.batchFn != nil {
		err := q.batchFn(ops)
		q.notifyBatchResults(ops, err)
		q.batchesProcessed.Add(1)
		if err == nil {
			q.processed.Add(int64(len(ops)))
		}
		return err
	}

	// Fall back to individual operations
	var lastErr error
	for _, op := range ops {
		var err error
		switch op.Type {
		case OpIndex:
			if q.indexFn != nil {
				err = q.indexFn(op.DocID, op.Document)
			}
		case OpDelete:
			if q.deleteFn != nil {
				err = q.deleteFn(op.DocID)
			}
		}

		// Notify the operation's result channel
		if op.ResultCh != nil {
			select {
			case op.ResultCh <- err:
			default:
				// Channel full or closed, skip
			}
		}

		if err != nil {
			lastErr = err
			q.dropped.Add(1)
		} else {
			q.processed.Add(1)
		}
	}

	q.batchesProcessed.Add(1)
	return lastErr
}

// notifyBatchResults notifies all operations in a batch with the same result.
func (q *AsyncIndexQueue) notifyBatchResults(ops []*IndexOperation, err error) {
	for _, op := range ops {
		if op.ResultCh != nil {
			select {
			case op.ResultCh <- err:
			default:
				// Channel full or closed, skip
			}
		}
	}
}
