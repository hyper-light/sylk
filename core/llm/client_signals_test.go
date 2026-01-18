package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSignalEmitter implements LLMSignalEmitter for testing.
type mockSignalEmitter struct {
	mu          sync.Mutex
	calls       []signalCall
	emitCount   atomic.Int32
	lastAgentID atomic.Value
}

type signalCall struct {
	agentID   string
	sessionID string
}

func newMockSignalEmitter() *mockSignalEmitter {
	return &mockSignalEmitter{}
}

func (m *mockSignalEmitter) EmitLLMResponse(agentID, sessionID string) {
	m.mu.Lock()
	m.calls = append(m.calls, signalCall{agentID: agentID, sessionID: sessionID})
	m.mu.Unlock()
	m.emitCount.Add(1)
	m.lastAgentID.Store(agentID)
}

func (m *mockSignalEmitter) CallCount() int {
	return int(m.emitCount.Load())
}

func (m *mockSignalEmitter) GetCalls() []signalCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]signalCall, len(m.calls))
	copy(result, m.calls)
	return result
}

// Test helpers

func newTestSupervisor(agentID string) *mockLLMSupervisor {
	return &mockLLMSupervisor{agentID: agentID}
}

func newSuccessfulClient() *mockLLMClient {
	return &mockLLMClient{
		completeResp: &CompletionResponse{
			Content: "test response",
			Model:   "test-model",
		},
	}
}

func newStreamingClient(chunks []StreamChunk) *mockLLMClient {
	return &mockLLMClient{streamChunks: chunks}
}

// Tests for signal emission after successful completion

func TestSignallingLLMClient_Complete_EmitsSignalOnSuccess(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	resp, err := client.Complete(supervisor, &CompletionRequest{Model: "test"})

	require.NoError(t, err)
	assert.Equal(t, "test response", resp.Content)
	assert.Equal(t, 1, emitter.CallCount())

	calls := emitter.GetCalls()
	assert.Equal(t, "agent-1", calls[0].agentID)
	assert.Equal(t, "session-1", calls[0].sessionID)
}

func TestSignallingLLMClient_Complete_MultipleCallsEmitMultipleSignals(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	for i := 0; i < 3; i++ {
		_, err := client.Complete(supervisor, &CompletionRequest{Model: "test"})
		require.NoError(t, err)
	}

	assert.Equal(t, 3, emitter.CallCount())
}

// Tests for no signal on failed completion

func TestSignallingLLMClient_Complete_NoSignalOnError(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := &mockLLMClient{
		completeErr: errors.New("LLM API error"),
	}
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	_, err := client.Complete(supervisor, &CompletionRequest{Model: "test"})

	assert.Error(t, err)
	assert.Equal(t, 0, emitter.CallCount())
}

func TestSignallingLLMClient_Complete_NoSignalOnSupervisorError(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := &mockLLMSupervisor{
		agentID:  "agent-1",
		beginErr: errors.New("supervisor busy"),
	}

	_, err := client.Complete(supervisor, &CompletionRequest{Model: "test"})

	assert.Error(t, err)
	assert.Equal(t, 0, emitter.CallCount())
}

// Tests for stream completion signals

func TestSignallingLLMClient_Stream_EmitsSignalOnSuccess(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newStreamingClient([]StreamChunk{
		{Content: "Hello"},
		{Content: " World", Done: true},
	})
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	chunks, err := client.Stream(supervisor, &CompletionRequest{Model: "test"})
	require.NoError(t, err)

	// Drain the stream
	for range chunks {
	}

	// Wait briefly for signal goroutine
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 1, emitter.CallCount())
	calls := emitter.GetCalls()
	assert.Equal(t, "agent-1", calls[0].agentID)
}

func TestSignallingLLMClient_Stream_NoSignalOnStreamError(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newStreamingClient([]StreamChunk{
		{Content: "Hello"},
		{Error: errors.New("stream interrupted"), Done: true},
	})
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	chunks, err := client.Stream(supervisor, &CompletionRequest{Model: "test"})
	require.NoError(t, err)

	// Drain the stream
	for range chunks {
	}

	// Wait briefly for signal goroutine
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 0, emitter.CallCount())
}

func TestSignallingLLMClient_Stream_NoSignalOnInitError(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := &mockLLMClient{
		streamErr: errors.New("failed to start stream"),
	}
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	_, err := client.Stream(supervisor, &CompletionRequest{Model: "test"})

	assert.Error(t, err)
	assert.Equal(t, 0, emitter.CallCount())
}

// Tests for nil signal emitter (no-op)

func TestSignallingLLMClient_Complete_NilEmitterNoOp(t *testing.T) {
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   nil,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	resp, err := client.Complete(supervisor, &CompletionRequest{Model: "test"})

	require.NoError(t, err)
	assert.Equal(t, "test response", resp.Content)
	// No panic, no error - just a no-op
}

func TestSignallingLLMClient_Stream_NilEmitterNoOp(t *testing.T) {
	underlying := newStreamingClient([]StreamChunk{
		{Content: "data", Done: true},
	})
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   nil,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	chunks, err := client.Stream(supervisor, &CompletionRequest{Model: "test"})
	require.NoError(t, err)

	// Drain the stream
	for range chunks {
	}

	// No panic, no error - just a no-op
}

// Race condition tests

func TestSignallingLLMClient_ConcurrentCompletes_RaceConditionSafe(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			supervisor := newTestSupervisor("agent-" + string(rune('A'+id%26)))
			_, _ = client.Complete(supervisor, &CompletionRequest{Model: "test"})
		}(i)
	}

	wg.Wait()

	assert.Equal(t, numGoroutines, emitter.CallCount())
}

func TestSignallingLLMClient_ConcurrentSetEmitter_RaceConditionSafe(t *testing.T) {
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   nil,
		SessionID: "session-1",
	})

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Half the goroutines set emitter
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			client.SetEmitter(newMockSignalEmitter())
		}()
	}

	// Half do completions
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			supervisor := newTestSupervisor("agent-1")
			_, _ = client.Complete(supervisor, &CompletionRequest{Model: "test"})
		}(i)
	}

	wg.Wait()
	// Test passes if no race detected
}

func TestSignallingLLMClient_ConcurrentStreams_RaceConditionSafe(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newStreamingClient([]StreamChunk{
		{Content: "test", Done: true},
	})
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			supervisor := newTestSupervisor("agent-" + string(rune('A'+id)))
			chunks, err := client.Stream(supervisor, &CompletionRequest{Model: "test"})
			if err != nil {
				return
			}
			for range chunks {
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Allow signal goroutines to complete

	// Signals should be emitted for successful streams
	assert.GreaterOrEqual(t, emitter.CallCount(), 1)
}

// Additional edge case tests

func TestSignallingLLMClient_SetSessionID_UpdatesSessionID(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	// First call with original session
	_, _ = client.Complete(supervisor, &CompletionRequest{Model: "test"})

	// Update session ID
	client.SetSessionID("session-2")

	// Second call with new session
	_, _ = client.Complete(supervisor, &CompletionRequest{Model: "test"})

	calls := emitter.GetCalls()
	assert.Equal(t, 2, len(calls))
	assert.Equal(t, "session-1", calls[0].sessionID)
	assert.Equal(t, "session-2", calls[1].sessionID)
}

func TestSignallingLLMClient_Underlying_ReturnsWrappedClient(t *testing.T) {
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{})

	assert.Same(t, tracked, client.Underlying())
}

func TestSignallingLLMClient_EmptyStreamEmitsSignal(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newStreamingClient([]StreamChunk{})
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "session-1",
	})
	supervisor := newTestSupervisor("agent-1")

	chunks, err := client.Stream(supervisor, &CompletionRequest{Model: "test"})
	require.NoError(t, err)

	// Drain the empty stream
	for range chunks {
	}

	time.Sleep(50 * time.Millisecond)

	// Empty stream without error should still emit signal
	assert.Equal(t, 1, emitter.CallCount())
}

// Test with real concurrency Operation context

type realSupervisor struct {
	agentID string
	ops     []*concurrency.Operation
	mu      sync.Mutex
}

func (s *realSupervisor) AgentID() string {
	return s.agentID
}

func (s *realSupervisor) BeginOperation(
	opType concurrency.OperationType,
	description string,
	timeout time.Duration,
) (*concurrency.Operation, error) {
	op := concurrency.NewOperation(
		context.Background(),
		opType,
		s.agentID,
		description,
		timeout,
	)
	s.mu.Lock()
	s.ops = append(s.ops, op)
	s.mu.Unlock()
	return op, nil
}

func (s *realSupervisor) EndOperation(op *concurrency.Operation, result any, err error) {
	op.SetResult(result, err)
	op.MarkDone()
}

func TestSignallingLLMClient_WithRealSupervisor(t *testing.T) {
	emitter := newMockSignalEmitter()
	underlying := newSuccessfulClient()
	tracked := NewTrackedLLMClient(underlying, time.Minute)
	client := NewSignallingLLMClient(tracked, SignalConfig{
		Emitter:   emitter,
		SessionID: "test-session",
	})
	supervisor := &realSupervisor{agentID: "real-agent"}

	resp, err := client.Complete(supervisor, &CompletionRequest{
		Model:     "gpt-4",
		MaxTokens: 100,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, emitter.CallCount())

	calls := emitter.GetCalls()
	assert.Equal(t, "real-agent", calls[0].agentID)
	assert.Equal(t, "test-session", calls[0].sessionID)
}
