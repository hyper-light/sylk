package tdd

import (
	"context"
	"errors"
	"sync"

	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/inspector"
	"github.com/adalundhe/sylk/agents/tester"
)

var ErrBusClosed = errors.New("pipeline bus closed")

// InspectorFeedback wraps inspector feedback for the worker.
type InspectorFeedback struct {
	Criteria *inspector.InspectorCriteria
	Feedback *inspector.InspectorFeedback
}

// TesterFeedback wraps tester feedback for the worker.
type TesterFeedback struct {
	Response    *tester.TesterResponse
	FailedTests []string
}

// WorkerResult wraps the worker output with changed file paths.
type WorkerResult struct {
	TaskResult   *engineer.TaskResult
	ChangedFiles []string
}

// PipelineBus provides bounded typed channels for intra-pipeline communication.
// All data channels are buffer-1 (single-flight). User messages are buffer-4.
type PipelineBus struct {
	inspectorFeedback chan *InspectorFeedback
	testerFeedback    chan *TesterFeedback
	workerDone        chan *WorkerResult
	inspectorDone     chan *inspector.InspectorResult
	testerDone        chan *tester.TesterResponse
	userMessages      chan any

	closed bool
	mu     sync.Mutex
}

// NewPipelineBus creates a bus with bounded channels.
func NewPipelineBus() *PipelineBus {
	return &PipelineBus{
		inspectorFeedback: make(chan *InspectorFeedback, 1),
		testerFeedback:    make(chan *TesterFeedback, 1),
		workerDone:        make(chan *WorkerResult, 1),
		inspectorDone:     make(chan *inspector.InspectorResult, 1),
		testerDone:        make(chan *tester.TesterResponse, 1),
		userMessages:      make(chan any, 4),
	}
}

// SendInspectorFeedback sends inspector feedback to the worker.
func (b *PipelineBus) SendInspectorFeedback(ctx context.Context, fb *InspectorFeedback) error {
	return sendCh(ctx, b, b.inspectorFeedback, fb)
}

// RecvInspectorFeedback receives inspector feedback.
func (b *PipelineBus) RecvInspectorFeedback(ctx context.Context) (*InspectorFeedback, error) {
	return recvCh(ctx, b, b.inspectorFeedback)
}

// SendTesterFeedback sends tester feedback to the worker.
func (b *PipelineBus) SendTesterFeedback(ctx context.Context, fb *TesterFeedback) error {
	return sendCh(ctx, b, b.testerFeedback, fb)
}

// RecvTesterFeedback receives tester feedback.
func (b *PipelineBus) RecvTesterFeedback(ctx context.Context) (*TesterFeedback, error) {
	return recvCh(ctx, b, b.testerFeedback)
}

// SendWorkerDone sends the worker result after execution.
func (b *PipelineBus) SendWorkerDone(ctx context.Context, r *WorkerResult) error {
	return sendCh(ctx, b, b.workerDone, r)
}

// RecvWorkerDone receives the worker result.
func (b *PipelineBus) RecvWorkerDone(ctx context.Context) (*WorkerResult, error) {
	return recvCh(ctx, b, b.workerDone)
}

// SendInspectorDone sends inspector validation result.
func (b *PipelineBus) SendInspectorDone(ctx context.Context, r *inspector.InspectorResult) error {
	return sendCh(ctx, b, b.inspectorDone, r)
}

// RecvInspectorDone receives inspector validation result.
func (b *PipelineBus) RecvInspectorDone(ctx context.Context) (*inspector.InspectorResult, error) {
	return recvCh(ctx, b, b.inspectorDone)
}

// SendTesterDone sends tester validation result.
func (b *PipelineBus) SendTesterDone(ctx context.Context, r *tester.TesterResponse) error {
	return sendCh(ctx, b, b.testerDone, r)
}

// RecvTesterDone receives tester validation result.
func (b *PipelineBus) RecvTesterDone(ctx context.Context) (*tester.TesterResponse, error) {
	return recvCh(ctx, b, b.testerDone)
}

// SendUserMessage sends a user override message into the pipeline.
func (b *PipelineBus) SendUserMessage(ctx context.Context, msg any) error {
	return sendCh(ctx, b, b.userMessages, msg)
}

// RecvUserMessage receives a user override message.
func (b *PipelineBus) RecvUserMessage(ctx context.Context) (any, error) {
	return recvCh(ctx, b, b.userMessages)
}

// Close idempotently drains and closes all channels.
func (b *PipelineBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true

	drainAndClose(b.inspectorFeedback)
	drainAndClose(b.testerFeedback)
	drainAndClose(b.workerDone)
	drainAndClose(b.inspectorDone)
	drainAndClose(b.testerDone)
	drainAndClose(b.userMessages)
}

// IsClosed reports whether the bus has been closed.
func (b *PipelineBus) IsClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func sendCh[T any](ctx context.Context, b *PipelineBus, ch chan T, val T) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrBusClosed
	}
	b.mu.Unlock()

	select {
	case ch <- val:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func recvCh[T any](ctx context.Context, b *PipelineBus, ch chan T) (T, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		var zero T
		return zero, ErrBusClosed
	}
	b.mu.Unlock()

	select {
	case val, ok := <-ch:
		if !ok {
			var zero T
			return zero, ErrBusClosed
		}
		return val, nil
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func drainAndClose[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			close(ch)
			return
		}
	}
}
