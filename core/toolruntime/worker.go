package toolruntime

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/concurrency"
)

type workerResult struct {
	value any
	err   error
}

type workerRequest struct {
	toolName string
	fn     func() (any, error)
	result chan workerResult
}

type SerialWorker struct {
	queue     chan workerRequest
	closed    atomic.Bool
	closeOnce sync.Once
}

// NewSerialWorker constructs a serial worker whose run loop is
// tracked by the given GoroutineScope. The scope is required
// (untracked goroutines are forbidden per CLAUDE.md); callers
// without a scope should use NewSerialWorkerDetached for in-memory
// test configurations that accept the leak risk.
func NewSerialWorker(scope *concurrency.GoroutineScope, queueSize int) (*SerialWorker, error) {
	if queueSize <= 0 {
		queueSize = 16
	}
	worker := &SerialWorker{
		queue: make(chan workerRequest, queueSize),
	}
	if scope == nil {
		return nil, fmt.Errorf("serial worker: NewSerialWorker requires a GoroutineScope (untracked goroutines are forbidden)")
	}
	if err := scope.Go("toolruntime:serial-worker", 0, func(ctx context.Context) error {
		worker.run()
		return nil
	}); err != nil {
		return nil, fmt.Errorf("serial worker: scope.Go: %w", err)
	}
	return worker, nil
}

func (w *SerialWorker) run() {
	for req := range w.queue {
		value, err := runWorkerFn(req.toolName, req.fn)
		req.result <- workerResult{value: value, err: err}
		close(req.result)
	}
}

func (w *SerialWorker) Do(ctx context.Context, toolName string, fn func() (any, error)) (any, error) {
	if w == nil {
		return nil, fmt.Errorf("serial worker is not configured")
	}
	if w.closed.Load() {
		return nil, fmt.Errorf("serial worker is closed")
	}
	if fn == nil {
		return nil, fmt.Errorf("worker function is required")
	}
	resultCh := make(chan workerResult, 1)
	req := workerRequest{
		toolName: toolName,
		fn:     fn,
		result: resultCh,
	}
	if err := w.enqueue(ctx, req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		return result.value, result.err
	}
}

func (w *SerialWorker) Close() {
	if w == nil {
		return
	}
	w.closeOnce.Do(func() {
		w.closed.Store(true)
		close(w.queue)
	})
}

func (w *SerialWorker) enqueue(ctx context.Context, req workerRequest) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("serial worker is closed")
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case w.queue <- req:
		return nil
	}
}

func runWorkerFn(toolName string, fn func() (any, error)) (value any, err error) {
	if fn == nil {
		return nil, fmt.Errorf("worker function is required")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool runtime panic in %q: %v\n%s", toolName, recovered, debug.Stack())
		}
	}()
	return fn()
}
