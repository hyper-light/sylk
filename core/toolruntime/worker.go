package toolruntime

import (
	"context"
	"fmt"
)

type workerResult struct {
	value any
	err   error
}

type workerRequest struct {
	fn     func() (any, error)
	result chan workerResult
}

type SerialWorker struct {
	queue chan workerRequest
}

func NewSerialWorker(queueSize int) *SerialWorker {
	if queueSize <= 0 {
		queueSize = 16
	}
	worker := &SerialWorker{
		queue: make(chan workerRequest, queueSize),
	}
	go worker.run()
	return worker
}

func (w *SerialWorker) run() {
	for req := range w.queue {
		value, err := req.fn()
		req.result <- workerResult{value: value, err: err}
		close(req.result)
	}
}

func (w *SerialWorker) Do(ctx context.Context, fn func() (any, error)) (any, error) {
	if w == nil {
		return nil, fmt.Errorf("serial worker is not configured")
	}
	if fn == nil {
		return nil, fmt.Errorf("worker function is required")
	}
	resultCh := make(chan workerResult, 1)
	req := workerRequest{
		fn:     fn,
		result: resultCh,
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case w.queue <- req:
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
	close(w.queue)
}
