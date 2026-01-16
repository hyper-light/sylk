package guide

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"time"
)

// =============================================================================
// Retry Policy
// =============================================================================
//
// RetryPolicy defines how failed requests should be retried.
// Supports exponential backoff with jitter to prevent thundering herd.

// RetryPolicy configures retry behavior
type RetryPolicy struct {
	// MaxAttempts is the maximum number of attempts (including initial)
	MaxAttempts int

	// InitialDelay is the delay before the first retry
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries
	MaxDelay time.Duration

	// Multiplier is applied to delay after each retry
	Multiplier float64

	// JitterFactor adds randomness to delays (0.0 to 1.0)
	// A factor of 0.1 means ±10% jitter
	JitterFactor float64

	// RetryableErrors defines which errors should trigger retry
	// If nil, all errors are retryable
	RetryableErrors func(error) bool
}

// DefaultRetryPolicy returns sensible defaults
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0.2,
	}
}

// AggressiveRetryPolicy retries more times with longer waits
func AggressiveRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:  5,
		InitialDelay: 200 * time.Millisecond,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0.3,
	}
}

// NoRetryPolicy disables retries
func NoRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 1,
	}
}

// =============================================================================
// Retry Executor
// =============================================================================

// RetryResult contains the result of a retry operation
type RetryResult[T any] struct {
	Value    T
	Attempts int
	Err      error
}

// Retry executes an operation with retry logic
func Retry[T any](ctx context.Context, policy RetryPolicy, operation func(ctx context.Context, attempt int) (T, error)) RetryResult[T] {
	var lastErr error
	var zero T

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		// Check context before each attempt
		if ctx.Err() != nil {
			return RetryResult[T]{Value: zero, Attempts: attempt, Err: ctx.Err()}
		}

		// Execute operation
		result, err := operation(ctx, attempt)
		if err == nil {
			return RetryResult[T]{Value: result, Attempts: attempt, Err: nil}
		}

		lastErr = err

		// Check if error is retryable
		if policy.RetryableErrors != nil && !policy.RetryableErrors(err) {
			return RetryResult[T]{Value: zero, Attempts: attempt, Err: err}
		}

		// Don't sleep after last attempt
		if attempt < policy.MaxAttempts {
			delay := calculateDelay(policy, attempt)

			select {
			case <-ctx.Done():
				return RetryResult[T]{Value: zero, Attempts: attempt, Err: ctx.Err()}
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	return RetryResult[T]{Value: zero, Attempts: policy.MaxAttempts, Err: lastErr}
}

// RetryVoid executes an operation that returns no value with retry logic
func RetryVoid(ctx context.Context, policy RetryPolicy, operation func(ctx context.Context, attempt int) error) RetryResult[struct{}] {
	return Retry(ctx, policy, func(ctx context.Context, attempt int) (struct{}, error) {
		return struct{}{}, operation(ctx, attempt)
	})
}

// calculateDelay computes the delay for a given attempt
func calculateDelay(policy RetryPolicy, attempt int) time.Duration {
	// Exponential backoff: initialDelay * (multiplier ^ (attempt - 1))
	delay := float64(policy.InitialDelay) * math.Pow(policy.Multiplier, float64(attempt-1))

	// Cap at max delay
	if delay > float64(policy.MaxDelay) {
		delay = float64(policy.MaxDelay)
	}

	// Add jitter
	if policy.JitterFactor > 0 {
		jitter := delay * policy.JitterFactor
		delay = delay - jitter + (rand.Float64() * 2 * jitter)
	}

	return time.Duration(delay)
}

// =============================================================================
// Retry Queue
// =============================================================================
//
// RetryQueue manages requests that need to be retried.
// It implements exponential backoff and deduplication.

// RetryableRequest represents a request that can be retried
type RetryableRequest struct {
	// Unique ID for deduplication
	ID string

	// The message to retry
	Message *Message

	// Target topic
	Topic string

	// Retry state
	Attempts    int
	LastAttempt time.Time
	NextAttempt time.Time
	LastError   error

	// Policy
	Policy RetryPolicy

	// Callback when permanently failed
	OnFailure func(req *RetryableRequest)
}

// RetryQueue manages pending retries
type RetryQueue struct {
	mu       sync.RWMutex
	requests map[string]*RetryableRequest
	bus      EventBus

	// Control
	stopCh   chan struct{}
	stopped  bool
	interval time.Duration
}

// NewRetryQueue creates a new retry queue
func NewRetryQueue(bus EventBus, checkInterval time.Duration) *RetryQueue {
	if checkInterval <= 0 {
		checkInterval = 1 * time.Second
	}

	return &RetryQueue{
		requests: make(map[string]*RetryableRequest),
		bus:      bus,
		stopCh:   make(chan struct{}),
		interval: checkInterval,
	}
}

// Start begins processing retries
func (q *RetryQueue) Start() {
	go q.processLoop()
}

// Stop halts retry processing
func (q *RetryQueue) Stop() {
	q.mu.Lock()
	if !q.stopped {
		q.stopped = true
		close(q.stopCh)
	}
	q.mu.Unlock()
}

// Add adds a request to the retry queue
func (q *RetryQueue) Add(req *RetryableRequest) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Deduplicate by ID
	if existing, ok := q.requests[req.ID]; ok {
		// Update with new attempt info
		existing.Attempts = req.Attempts
		existing.LastAttempt = req.LastAttempt
		existing.LastError = req.LastError
		existing.NextAttempt = req.NextAttempt
		return
	}

	// Calculate next attempt time
	if req.NextAttempt.IsZero() {
		delay := calculateDelay(req.Policy, req.Attempts+1)
		req.NextAttempt = time.Now().Add(delay)
	}

	q.requests[req.ID] = req
}

// Remove removes a request from the retry queue
func (q *RetryQueue) Remove(id string) {
	q.mu.Lock()
	delete(q.requests, id)
	q.mu.Unlock()
}

// processLoop periodically processes retry queue
func (q *RetryQueue) processLoop() {
	ticker := time.NewTicker(q.interval)
	defer ticker.Stop()

	for {
		select {
		case <-q.stopCh:
			return
		case <-ticker.C:
			q.processRetries()
		}
	}
}

// processRetries attempts to retry pending requests
func (q *RetryQueue) processRetries() {
	now := time.Now()
	var toRetry []*RetryableRequest
	var toRemove []string

	// Find requests ready to retry
	q.mu.RLock()
	for id, req := range q.requests {
		if now.After(req.NextAttempt) {
			toRetry = append(toRetry, req)
		}
		// Check if exceeded max attempts
		if req.Attempts >= req.Policy.MaxAttempts {
			toRemove = append(toRemove, id)
		}
	}
	q.mu.RUnlock()

	// Process retries
	for _, req := range toRetry {
		if req.Attempts >= req.Policy.MaxAttempts {
			continue
		}

		// Attempt retry
		err := q.bus.Publish(req.Topic, req.Message)

		q.mu.Lock()
		if r, ok := q.requests[req.ID]; ok {
			r.Attempts++
			r.LastAttempt = now
			r.LastError = err

			if err != nil {
				// Schedule next retry
				delay := calculateDelay(r.Policy, r.Attempts+1)
				r.NextAttempt = now.Add(delay)
			} else {
				// Success - remove from queue
				delete(q.requests, req.ID)
			}

			// Check if exhausted
			if r.Attempts >= r.Policy.MaxAttempts && r.OnFailure != nil {
				go r.OnFailure(r)
			}
		}
		q.mu.Unlock()
	}

	// Remove exhausted requests
	q.mu.Lock()
	for _, id := range toRemove {
		delete(q.requests, id)
	}
	q.mu.Unlock()
}

// Len returns the number of pending retries
func (q *RetryQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.requests)
}

// Stats returns retry queue statistics
func (q *RetryQueue) Stats() RetryQueueStats {
	q.mu.RLock()
	defer q.mu.RUnlock()

	stats := RetryQueueStats{
		Pending: len(q.requests),
	}

	for _, req := range q.requests {
		stats.TotalAttempts += req.Attempts
		if req.Attempts > stats.MaxAttempts {
			stats.MaxAttempts = req.Attempts
		}
	}

	return stats
}

// RetryQueueStats contains retry queue statistics
type RetryQueueStats struct {
	Pending       int `json:"pending"`
	TotalAttempts int `json:"total_attempts"`
	MaxAttempts   int `json:"max_attempts"`
}
