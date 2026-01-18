// Package bulkhead provides isolation and circuit breaking for LLM operations.
package bulkhead

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Bulkhead represents a single isolation unit with semaphore, queue, and circuit.
type Bulkhead struct {
	id     string
	level  BulkheadLevel
	config BulkheadConfig

	// Concurrency control
	semaphore chan struct{}
	queue     chan *pendingRequest

	// Circuit breaker state
	circuitState       atomic.Int32 // 0=closed, 1=open, 2=half-open
	consecutiveFail    atomic.Int32
	consecutiveSuccess atomic.Int32
	lastFailure        atomic.Int64 // Unix nano

	// Metrics
	activeCount   atomic.Int32
	queuedCount   atomic.Int32
	totalRequests atomic.Int64
	totalFailures atomic.Int64

	// Lifecycle
	stopCh chan struct{}
	wg     sync.WaitGroup

	mu sync.RWMutex
}

// pendingRequest represents a request waiting in the queue.
type pendingRequest struct {
	ctx      context.Context
	resultCh chan AcquireResult
	enqueued time.Time
}

// NewBulkhead creates a new bulkhead with the given configuration.
func NewBulkhead(id string, level BulkheadLevel, config BulkheadConfig) *Bulkhead {
	b := &Bulkhead{
		id:        id,
		level:     level,
		config:    config,
		semaphore: make(chan struct{}, config.MaxConcurrent),
		queue:     make(chan *pendingRequest, config.MaxQueueSize),
		stopCh:    make(chan struct{}),
	}

	// Pre-fill semaphore with available slots
	for i := 0; i < config.MaxConcurrent; i++ {
		b.semaphore <- struct{}{}
	}

	// Start queue processor
	b.wg.Add(1)
	go b.processQueue()

	return b
}

// Acquire attempts to acquire a slot from the bulkhead.
func (b *Bulkhead) Acquire(ctx context.Context) error {
	b.totalRequests.Add(1)

	// Check circuit breaker first
	if err := b.checkCircuit(); err != nil {
		return err
	}

	// Try immediate acquisition
	select {
	case <-b.semaphore:
		b.activeCount.Add(1)
		return nil
	default:
		// Semaphore full, try to enqueue
		return b.enqueue(ctx)
	}
}

// Release returns a slot to the bulkhead.
func (b *Bulkhead) Release() {
	b.activeCount.Add(-1)
	b.semaphore <- struct{}{}
}

// RecordSuccess records a successful request.
func (b *Bulkhead) RecordSuccess() {
	b.consecutiveFail.Store(0)
	newCount := b.consecutiveSuccess.Add(1)
	b.handleSuccessTransition(newCount)
}

// handleSuccessTransition handles circuit state transitions on success.
func (b *Bulkhead) handleSuccessTransition(successCount int32) {
	state := CircuitState(b.circuitState.Load())
	if state != CircuitHalfOpen {
		return
	}

	if int(successCount) >= b.config.CircuitConfig.SuccessThreshold {
		b.circuitState.Store(int32(CircuitClosed))
		b.consecutiveSuccess.Store(0)
	}
}

// RecordFailure records a failed request.
func (b *Bulkhead) RecordFailure() {
	b.totalFailures.Add(1)
	b.consecutiveSuccess.Store(0)
	b.lastFailure.Store(time.Now().UnixNano())
	newCount := b.consecutiveFail.Add(1)
	b.handleFailureTransition(newCount)
}

// handleFailureTransition handles circuit state transitions on failure.
func (b *Bulkhead) handleFailureTransition(failCount int32) {
	state := CircuitState(b.circuitState.Load())

	switch state {
	case CircuitHalfOpen:
		// Immediately reopen on any failure in half-open
		b.circuitState.Store(int32(CircuitOpen))
	case CircuitClosed:
		if int(failCount) >= b.config.CircuitConfig.FailureThreshold {
			b.circuitState.Store(int32(CircuitOpen))
		}
	}
}

// Stats returns current bulkhead statistics.
func (b *Bulkhead) Stats() BulkheadStats {
	return BulkheadStats{
		ID:            b.id,
		Level:         b.level,
		Active:        int(b.activeCount.Load()),
		Queued:        int(b.queuedCount.Load()),
		Available:     len(b.semaphore),
		CircuitState:  CircuitState(b.circuitState.Load()),
		TotalRequests: b.totalRequests.Load(),
		TotalFailures: b.totalFailures.Load(),
	}
}

// checkCircuit verifies the circuit breaker allows requests.
func (b *Bulkhead) checkCircuit() error {
	state := CircuitState(b.circuitState.Load())

	switch state {
	case CircuitClosed:
		return nil
	case CircuitHalfOpen:
		return nil
	case CircuitOpen:
		return b.checkOpenCircuit()
	default:
		return nil
	}
}

// checkOpenCircuit checks if an open circuit should transition to half-open.
func (b *Bulkhead) checkOpenCircuit() error {
	lastFail := time.Unix(0, b.lastFailure.Load())
	elapsed := time.Since(lastFail)

	if elapsed >= b.config.CircuitConfig.Timeout {
		b.transitionToHalfOpen()
		return nil
	}
	return ErrCircuitOpen
}

// transitionToHalfOpen atomically transitions from open to half-open state.
func (b *Bulkhead) transitionToHalfOpen() {
	b.circuitState.CompareAndSwap(int32(CircuitOpen), int32(CircuitHalfOpen))
	b.consecutiveSuccess.Store(0)
	b.consecutiveFail.Store(0)
}

// enqueue adds a request to the queue and waits for a slot.
func (b *Bulkhead) enqueue(ctx context.Context) error {
	req := &pendingRequest{
		ctx:      ctx,
		resultCh: make(chan AcquireResult, 1),
		enqueued: time.Now(),
	}

	// Try to add to queue
	select {
	case b.queue <- req:
		b.queuedCount.Add(1)
		return b.waitForSlot(ctx, req)
	default:
		return ErrQueueFull
	}
}

// waitForSlot waits for a slot to become available or timeout.
func (b *Bulkhead) waitForSlot(ctx context.Context, req *pendingRequest) error {
	timeout := time.After(b.config.QueueTimeout)

	select {
	case result := <-req.resultCh:
		return result.Error
	case <-timeout:
		return ErrQueueTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

// processQueue handles pending requests from the queue.
func (b *Bulkhead) processQueue() {
	defer b.wg.Done()

	for {
		select {
		case <-b.stopCh:
			b.drainQueue()
			return
		case req := <-b.queue:
			b.handlePendingRequest(req)
		}
	}
}

// handlePendingRequest processes a single pending request.
func (b *Bulkhead) handlePendingRequest(req *pendingRequest) {
	b.queuedCount.Add(-1)

	// Check if request has already timed out or been cancelled
	if b.isRequestExpired(req) {
		return
	}

	b.waitForSemaphore(req)
}

// waitForSemaphore waits for a semaphore slot and sends result to the request.
func (b *Bulkhead) waitForSemaphore(req *pendingRequest) {
	select {
	case <-req.ctx.Done():
		req.resultCh <- AcquireResult{Error: req.ctx.Err()}
	case <-b.stopCh:
		req.resultCh <- AcquireResult{Error: context.Canceled}
	case <-b.semaphore:
		b.activeCount.Add(1)
		req.resultCh <- AcquireResult{Acquired: true}
	}
}

// isRequestExpired checks if a pending request has exceeded its queue timeout.
func (b *Bulkhead) isRequestExpired(req *pendingRequest) bool {
	elapsed := time.Since(req.enqueued)
	if elapsed >= b.config.QueueTimeout {
		req.resultCh <- AcquireResult{Error: ErrQueueTimeout}
		return true
	}
	return false
}

// drainQueue closes all pending requests when shutting down.
func (b *Bulkhead) drainQueue() {
	for {
		select {
		case req := <-b.queue:
			b.queuedCount.Add(-1)
			req.resultCh <- AcquireResult{Error: context.Canceled}
		default:
			return
		}
	}
}

// Stop gracefully shuts down the bulkhead.
func (b *Bulkhead) Stop() {
	close(b.stopCh)
	b.wg.Wait()
}

// ID returns the bulkhead identifier.
func (b *Bulkhead) ID() string {
	return b.id
}

// Level returns the bulkhead level.
func (b *Bulkhead) Level() BulkheadLevel {
	return b.level
}

// CircuitStateValue returns the current circuit state.
func (b *Bulkhead) CircuitStateValue() CircuitState {
	return CircuitState(b.circuitState.Load())
}
