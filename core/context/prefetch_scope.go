// Package context provides lossless context virtualization types for the Sylk
// multi-agent coding application. This file implements AR.2.2 GoroutineScope
// Prefetch Wrapper for speculative prefetch operations with bounded timeouts.
package context

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// DefaultPrefetchTimeout is the default budget for speculative prefetch operations.
const DefaultPrefetchTimeout = 200 * time.Millisecond

// PrefetchFuture represents an in-flight prefetch operation that will
// eventually produce an AugmentedQuery result or an error.
type PrefetchFuture struct {
	result    atomic.Pointer[AugmentedQuery]
	done      chan struct{}
	started   time.Time
	err       atomic.Pointer[error]
	operation string
	closeOnce sync.Once
}

// NewPrefetchFuture creates a new PrefetchFuture for tracking a prefetch operation.
func NewPrefetchFuture(operation string) *PrefetchFuture {
	return &PrefetchFuture{
		done:      make(chan struct{}),
		started:   time.Now(),
		operation: operation,
	}
}

// Wait blocks until the prefetch completes or the context is cancelled.
func (f *PrefetchFuture) Wait(ctx context.Context) (*AugmentedQuery, error) {
	select {
	case <-f.done:
		return f.getResult()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// getResult returns the result or error after the future is done.
func (f *PrefetchFuture) getResult() (*AugmentedQuery, error) {
	if errPtr := f.err.Load(); errPtr != nil {
		return nil, *errPtr
	}
	return f.result.Load(), nil
}

// SetResult stores the result and signals completion.
func (f *PrefetchFuture) SetResult(result *AugmentedQuery) {
	f.result.Store(result)
	f.closeDone()
}

// SetError stores an error and signals completion.
func (f *PrefetchFuture) SetError(err error) {
	f.err.Store(&err)
	f.closeDone()
}

// closeDone safely closes the done channel exactly once.
func (f *PrefetchFuture) closeDone() {
	f.closeOnce.Do(func() {
		close(f.done)
	})
}

// IsReady returns true if the result is available without blocking.
func (f *PrefetchFuture) IsReady() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// Duration returns the elapsed time since the prefetch started.
func (f *PrefetchFuture) Duration() time.Duration {
	return time.Since(f.started)
}

// Operation returns the operation description for tracking purposes.
func (f *PrefetchFuture) Operation() string {
	return f.operation
}

// PrefetchScope wraps a GoroutineScope to manage speculative prefetch operations
// with bounded timeouts and tracking of in-flight queries.
type PrefetchScope struct {
	scope    *concurrency.GoroutineScope
	timeout  time.Duration
	inflight sync.Map // queryHash -> *PrefetchFuture
}

// NewPrefetchScope creates a new PrefetchScope wrapping the given GoroutineScope.
func NewPrefetchScope(scope *concurrency.GoroutineScope, timeout time.Duration) *PrefetchScope {
	if timeout <= 0 {
		timeout = DefaultPrefetchTimeout
	}
	return &PrefetchScope{
		scope:   scope,
		timeout: timeout,
	}
}

// PrefetchWorkFunc is the signature for prefetch work functions.
type PrefetchWorkFunc func(ctx context.Context) (*AugmentedQuery, error)

// StartSpeculative launches a speculative prefetch operation using the managed scope.
// It returns immediately with a future that can be awaited for the result.
func (ps *PrefetchScope) StartSpeculative(
	ctx context.Context,
	queryHash string,
	work PrefetchWorkFunc,
) *PrefetchFuture {
	future := NewPrefetchFuture("prefetch:" + queryHash)
	ps.inflight.Store(queryHash, future)

	err := ps.scope.Go("prefetch:"+queryHash, ps.timeout, func(workerCtx context.Context) error {
		ps.executePrefetch(workerCtx, queryHash, work, future)
		return nil
	})

	if err != nil {
		future.SetError(err)
		ps.inflight.Delete(queryHash)
	}

	return future
}

// executePrefetch runs the work function and handles the result.
func (ps *PrefetchScope) executePrefetch(
	ctx context.Context,
	queryHash string,
	work PrefetchWorkFunc,
	future *PrefetchFuture,
) {
	defer ps.inflight.Delete(queryHash)

	result, err := work(ctx)
	if err != nil {
		future.SetError(err)
		return
	}
	future.SetResult(result)
}

// GetInflight returns an existing in-flight prefetch future if present.
func (ps *PrefetchScope) GetInflight(queryHash string) (*PrefetchFuture, bool) {
	if val, ok := ps.inflight.Load(queryHash); ok {
		return val.(*PrefetchFuture), true
	}
	return nil, false
}

// CancelInflight cancels and removes an in-flight prefetch operation.
func (ps *PrefetchScope) CancelInflight(queryHash string) {
	if val, ok := ps.inflight.LoadAndDelete(queryHash); ok {
		future := val.(*PrefetchFuture)
		future.SetError(context.Canceled)
	}
}

// InflightCount returns the number of in-flight prefetch operations.
func (ps *PrefetchScope) InflightCount() int {
	count := 0
	ps.inflight.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// Timeout returns the configured timeout for prefetch operations.
func (ps *PrefetchScope) Timeout() time.Duration {
	return ps.timeout
}
