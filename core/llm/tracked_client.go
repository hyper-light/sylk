package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

type CompletionRequest struct {
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature float64
	Timeout     time.Duration
}

type CompletionResponse struct {
	Content      string
	Model        string
	FinishReason string
	Usage        TokenUsage
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type StreamChunk struct {
	Content      string
	Done         bool
	FinishReason string
	Error        error
}

type LLMClient interface {
	CompleteWithContext(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
	StreamWithContext(ctx context.Context, req *CompletionRequest) (<-chan StreamChunk, error)
}

type LLMSupervisor interface {
	AgentID() string
	BeginOperation(opType concurrency.OperationType, description string, timeout time.Duration) (*concurrency.Operation, error)
	EndOperation(op *concurrency.Operation, result any, err error)
}

type TrackedLLMClient struct {
	underlying LLMClient
	maxTimeout time.Duration
}

func NewTrackedLLMClient(underlying LLMClient, maxTimeout time.Duration) *TrackedLLMClient {
	if maxTimeout <= 0 {
		maxTimeout = 5 * time.Minute
	}
	return &TrackedLLMClient{
		underlying: underlying,
		maxTimeout: maxTimeout,
	}
}

func (c *TrackedLLMClient) Complete(
	supervisor LLMSupervisor,
	req *CompletionRequest,
) (*CompletionResponse, error) {
	timeout := c.normalizeTimeout(req.Timeout)
	description := c.buildDescription(req)

	op, err := supervisor.BeginOperation(concurrency.OpTypeLLMCall, description, timeout)
	if err != nil {
		return nil, err
	}

	resp, callErr := c.underlying.CompleteWithContext(op.Context(), req)
	supervisor.EndOperation(op, resp, callErr)

	return resp, callErr
}

func (c *TrackedLLMClient) Stream(
	supervisor LLMSupervisor,
	req *CompletionRequest,
) (<-chan StreamChunk, error) {
	timeout := c.normalizeTimeout(req.Timeout)
	description := c.buildDescription(req)

	op, err := supervisor.BeginOperation(concurrency.OpTypeLLMCall, description, timeout)
	if err != nil {
		return nil, err
	}

	chunks, streamErr := c.underlying.StreamWithContext(op.Context(), req)
	if streamErr != nil {
		supervisor.EndOperation(op, nil, streamErr)
		return nil, streamErr
	}

	return c.wrapStreamWithTracking(supervisor, op, chunks), nil
}

func (c *TrackedLLMClient) wrapStreamWithTracking(
	supervisor LLMSupervisor,
	op *concurrency.Operation,
	input <-chan StreamChunk,
) <-chan StreamChunk {
	output := make(chan StreamChunk, 10)

	go c.forwardChunks(supervisor, op, input, output)

	return output
}

func (c *TrackedLLMClient) forwardChunks(
	supervisor LLMSupervisor,
	op *concurrency.Operation,
	input <-chan StreamChunk,
	output chan<- StreamChunk,
) {
	defer close(output)
	defer supervisor.EndOperation(op, nil, nil)

	for chunk := range input {
		if c.sendChunkOrCancel(op, output, chunk) {
			return
		}
	}
}

func (c *TrackedLLMClient) sendChunkOrCancel(
	op *concurrency.Operation,
	output chan<- StreamChunk,
	chunk StreamChunk,
) bool {
	select {
	case output <- chunk:
		return chunk.Done || chunk.Error != nil
	case <-op.Context().Done():
		output <- StreamChunk{Error: op.Context().Err(), Done: true}
		return true
	}
}

func (c *TrackedLLMClient) normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 || timeout > c.maxTimeout {
		return c.maxTimeout
	}
	return timeout
}

func (c *TrackedLLMClient) buildDescription(req *CompletionRequest) string {
	return fmt.Sprintf("llm model=%s tokens=%d", req.Model, req.MaxTokens)
}

func (c *TrackedLLMClient) MaxTimeout() time.Duration {
	return c.maxTimeout
}
