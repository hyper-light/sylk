package llm

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLLMSupervisor struct {
	agentID     string
	beginCalled atomic.Bool
	endCalled   atomic.Bool
	beginErr    error
	lastOp      *concurrency.Operation
}

func (m *mockLLMSupervisor) AgentID() string {
	return m.agentID
}

func (m *mockLLMSupervisor) BeginOperation(
	opType concurrency.OperationType,
	description string,
	timeout time.Duration,
) (*concurrency.Operation, error) {
	m.beginCalled.Store(true)
	if m.beginErr != nil {
		return nil, m.beginErr
	}

	op := concurrency.NewOperation(
		context.Background(),
		opType,
		m.agentID,
		description,
		timeout,
	)
	m.lastOp = op
	return op, nil
}

func (m *mockLLMSupervisor) EndOperation(op *concurrency.Operation, result any, err error) {
	m.endCalled.Store(true)
	op.SetResult(result, err)
	op.MarkDone()
}

type mockLLMClient struct {
	completeResp  *CompletionResponse
	completeErr   error
	streamChunks  []StreamChunk
	streamErr     error
	completeCalls atomic.Int32
	streamCalls   atomic.Int32
}

func (m *mockLLMClient) CompleteWithContext(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	m.completeCalls.Add(1)
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return m.completeResp, nil
}

func (m *mockLLMClient) StreamWithContext(ctx context.Context, req *CompletionRequest) (<-chan StreamChunk, error) {
	m.streamCalls.Add(1)
	if m.streamErr != nil {
		return nil, m.streamErr
	}

	ch := make(chan StreamChunk, len(m.streamChunks))
	for _, chunk := range m.streamChunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func TestTrackedLLMClient_CompleteTracksLifecycle(t *testing.T) {
	supervisor := &mockLLMSupervisor{agentID: "test-agent"}
	underlying := &mockLLMClient{
		completeResp: &CompletionResponse{
			Content: "Hello",
			Model:   "test-model",
		},
	}

	client := NewTrackedLLMClient(underlying, 5*time.Minute)

	req := &CompletionRequest{
		Model:     "test-model",
		MaxTokens: 100,
		Timeout:   time.Second,
	}

	resp, err := client.Complete(supervisor, req)

	require.NoError(t, err)
	assert.Equal(t, "Hello", resp.Content)
	assert.True(t, supervisor.beginCalled.Load())
	assert.True(t, supervisor.endCalled.Load())
	assert.Equal(t, int32(1), underlying.completeCalls.Load())
}

func TestTrackedLLMClient_CompleteRespectsTimeout(t *testing.T) {
	supervisor := &mockLLMSupervisor{agentID: "test-agent"}
	underlying := &mockLLMClient{
		completeResp: &CompletionResponse{Content: "OK"},
	}

	client := NewTrackedLLMClient(underlying, 100*time.Millisecond)

	req := &CompletionRequest{
		Model:   "test-model",
		Timeout: time.Hour,
	}

	_, _ = client.Complete(supervisor, req)

	assert.LessOrEqual(t, supervisor.lastOp.Deadline.Sub(supervisor.lastOp.StartedAt), 200*time.Millisecond)
}

func TestTrackedLLMClient_CompleteCancelsOnSupervisorError(t *testing.T) {
	supervisor := &mockLLMSupervisor{
		agentID:  "test-agent",
		beginErr: errors.New("supervisor busy"),
	}
	underlying := &mockLLMClient{}

	client := NewTrackedLLMClient(underlying, time.Minute)

	req := &CompletionRequest{Model: "test-model"}

	_, err := client.Complete(supervisor, req)

	assert.Error(t, err)
	assert.Equal(t, int32(0), underlying.completeCalls.Load())
	assert.False(t, supervisor.endCalled.Load())
}

func TestTrackedLLMClient_StreamTracksUntilCompletion(t *testing.T) {
	supervisor := &mockLLMSupervisor{agentID: "test-agent"}
	underlying := &mockLLMClient{
		streamChunks: []StreamChunk{
			{Content: "Hello"},
			{Content: " World"},
			{Content: "!", Done: true},
		},
	}

	client := NewTrackedLLMClient(underlying, time.Minute)

	req := &CompletionRequest{Model: "test-model", Timeout: time.Second}

	chunks, err := client.Stream(supervisor, req)
	require.NoError(t, err)

	var collected []string
	for chunk := range chunks {
		collected = append(collected, chunk.Content)
		if chunk.Done {
			break
		}
	}

	assert.Equal(t, []string{"Hello", " World", "!"}, collected)
	assert.True(t, supervisor.beginCalled.Load())

	time.Sleep(50 * time.Millisecond)
	assert.True(t, supervisor.endCalled.Load())
}

func TestTrackedLLMClient_StreamHandlesError(t *testing.T) {
	supervisor := &mockLLMSupervisor{agentID: "test-agent"}
	underlying := &mockLLMClient{
		streamErr: errors.New("stream failed"),
	}

	client := NewTrackedLLMClient(underlying, time.Minute)

	req := &CompletionRequest{Model: "test-model", Timeout: time.Second}

	_, err := client.Stream(supervisor, req)

	assert.Error(t, err)
	assert.True(t, supervisor.endCalled.Load())
}

func TestTrackedLLMClient_BuildsDescription(t *testing.T) {
	client := NewTrackedLLMClient(nil, time.Minute)

	req := &CompletionRequest{
		Model:     "gpt-4",
		MaxTokens: 1000,
	}

	desc := client.buildDescription(req)
	assert.Contains(t, desc, "llm")
	assert.Contains(t, desc, "gpt-4")
	assert.Contains(t, desc, "1000")
}

func TestTrackedLLMClient_NormalizeTimeout(t *testing.T) {
	client := NewTrackedLLMClient(nil, 100*time.Millisecond)

	assert.Equal(t, 50*time.Millisecond, client.normalizeTimeout(50*time.Millisecond))
	assert.Equal(t, 100*time.Millisecond, client.normalizeTimeout(time.Hour))
	assert.Equal(t, 100*time.Millisecond, client.normalizeTimeout(0))
}

func TestTrackedLLMClient_DefaultMaxTimeout(t *testing.T) {
	client := NewTrackedLLMClient(nil, 0)

	assert.Equal(t, 5*time.Minute, client.MaxTimeout())
}

func TestTrackedLLMClient_OperationType(t *testing.T) {
	supervisor := &mockLLMSupervisor{agentID: "test-agent"}
	underlying := &mockLLMClient{
		completeResp: &CompletionResponse{Content: "OK"},
	}

	client := NewTrackedLLMClient(underlying, time.Minute)

	req := &CompletionRequest{Model: "test-model", Timeout: time.Second}
	_, _ = client.Complete(supervisor, req)

	assert.Equal(t, concurrency.OpTypeLLMCall, supervisor.lastOp.Type)
}
