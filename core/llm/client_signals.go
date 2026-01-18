package llm

import (
	"sync"
)

// LLMSignalEmitter defines the interface for emitting LLM progress signals.
// This allows the TrackedLLMClient to emit progress signals without depending
// on concrete recovery package types.
type LLMSignalEmitter interface {
	// EmitLLMResponse emits a signal indicating an LLM response was received.
	EmitLLMResponse(agentID, sessionID string)
}

// SignalConfig holds configuration for signal emission.
type SignalConfig struct {
	// Emitter is the signal emitter to use. May be nil for no-op behavior.
	Emitter LLMSignalEmitter
	// SessionID is the session identifier for signal attribution.
	SessionID string
}

// SignallingLLMClient wraps a TrackedLLMClient to add signal emission.
// It emits SignalLLMResponse after each successful LLM response.
type SignallingLLMClient struct {
	client    *TrackedLLMClient
	emitter   LLMSignalEmitter
	sessionID string
	mu        sync.RWMutex
}

// NewSignallingLLMClient creates a new SignallingLLMClient.
// If emitter is nil, no signals will be emitted.
func NewSignallingLLMClient(client *TrackedLLMClient, config SignalConfig) *SignallingLLMClient {
	return &SignallingLLMClient{
		client:    client,
		emitter:   config.Emitter,
		sessionID: config.SessionID,
	}
}

// Complete executes a completion request and emits a signal on success.
func (c *SignallingLLMClient) Complete(
	supervisor LLMSupervisor,
	req *CompletionRequest,
) (*CompletionResponse, error) {
	resp, err := c.client.Complete(supervisor, req)
	if err == nil {
		c.emitLLMResponse(supervisor.AgentID())
	}
	return resp, err
}

// Stream executes a streaming request and emits a signal on stream completion.
func (c *SignallingLLMClient) Stream(
	supervisor LLMSupervisor,
	req *CompletionRequest,
) (<-chan StreamChunk, error) {
	chunks, err := c.client.Stream(supervisor, req)
	if err != nil {
		return nil, err
	}
	return c.wrapStreamWithSignal(supervisor.AgentID(), chunks), nil
}

// wrapStreamWithSignal wraps the stream to emit a signal when it completes successfully.
func (c *SignallingLLMClient) wrapStreamWithSignal(
	agentID string,
	input <-chan StreamChunk,
) <-chan StreamChunk {
	output := make(chan StreamChunk, 10)
	go c.forwardAndSignal(agentID, input, output)
	return output
}

// forwardAndSignal forwards chunks and emits a signal on successful completion.
func (c *SignallingLLMClient) forwardAndSignal(
	agentID string,
	input <-chan StreamChunk,
	output chan<- StreamChunk,
) {
	defer close(output)

	var lastError error
	for chunk := range input {
		output <- chunk
		if chunk.Error != nil {
			lastError = chunk.Error
		}
	}

	// Only emit signal if stream completed without error
	if lastError == nil {
		c.emitLLMResponse(agentID)
	}
}

// emitLLMResponse safely emits an LLM response signal.
func (c *SignallingLLMClient) emitLLMResponse(agentID string) {
	c.mu.RLock()
	emitter := c.emitter
	sessionID := c.sessionID
	c.mu.RUnlock()

	if emitter != nil {
		emitter.EmitLLMResponse(agentID, sessionID)
	}
}

// SetEmitter updates the signal emitter. Thread-safe.
func (c *SignallingLLMClient) SetEmitter(emitter LLMSignalEmitter) {
	c.mu.Lock()
	c.emitter = emitter
	c.mu.Unlock()
}

// SetSessionID updates the session ID. Thread-safe.
func (c *SignallingLLMClient) SetSessionID(sessionID string) {
	c.mu.Lock()
	c.sessionID = sessionID
	c.mu.Unlock()
}

// Underlying returns the wrapped TrackedLLMClient.
func (c *SignallingLLMClient) Underlying() *TrackedLLMClient {
	return c.client
}
