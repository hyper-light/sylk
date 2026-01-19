package providers

import (
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// LLMMetrics - Metrics tracking for LLM requests/responses
// =============================================================================

// LLMMetrics contains telemetry data for LLM interactions.
// Used for tracking performance, costs, and debugging.
type LLMMetrics struct {
	// Model identifies the LLM model used (e.g., "gpt-4", "claude-3-opus")
	Model string

	// InputTokens is the number of tokens in the request
	InputTokens int

	// OutputTokens is the number of tokens in the response
	OutputTokens int

	// Duration is the total time from request to response
	Duration time.Duration

	// RequestTime is when the request was initiated
	RequestTime time.Time

	// ResponseTime is when the response was received
	ResponseTime time.Time
}

// TokensPerSecond calculates the output token generation rate.
// Returns 0 if duration is zero or negative.
func (m *LLMMetrics) TokensPerSecond() float64 {
	if m.Duration <= 0 {
		return 0
	}
	seconds := m.Duration.Seconds()
	if seconds <= 0 {
		return 0
	}
	return float64(m.OutputTokens) / seconds
}

// TotalTokens returns the sum of input and output tokens.
func (m *LLMMetrics) TotalTokens() int {
	return m.InputTokens + m.OutputTokens
}

// =============================================================================
// LLMProviderEventHook - Interface for LLM event integration
// =============================================================================

// LLMProviderEventHook defines callbacks for LLM provider events.
// This interface allows existing providers to integrate with the event system
// without direct dependency on the ActivityEventBus.
type LLMProviderEventHook interface {
	// OnRequest is called when an LLM request is initiated.
	// sessionID: the current session identifier
	// agentID: the agent making the request
	// model: the LLM model being used
	// tokenCount: estimated input token count
	OnRequest(sessionID, agentID, model string, tokenCount int)

	// OnResponse is called when an LLM response is received.
	// sessionID: the current session identifier
	// agentID: the agent that made the request
	// model: the LLM model used
	// inputTokens: actual input token count
	// outputTokens: output token count
	// duration: time from request to response
	OnResponse(sessionID, agentID, model string, inputTokens, outputTokens int, duration time.Duration)

	// OnError is called when an LLM API error occurs.
	// sessionID: the current session identifier
	// agentID: the agent that made the request
	// model: the LLM model being used
	// err: the error that occurred
	OnError(sessionID, agentID, model string, err error)
}

// =============================================================================
// LLMEventPublisher - Event publisher for LLM interactions
// =============================================================================

// LLMEventPublisher publishes LLM-related events to the ActivityEventBus.
// It tracks LLM requests, responses, and errors for observability and debugging.
type LLMEventPublisher struct {
	bus *events.ActivityEventBus
}

// NewLLMEventPublisher creates a new LLMEventPublisher with the given event bus.
// Returns nil if bus is nil.
func NewLLMEventPublisher(bus *events.ActivityEventBus) *LLMEventPublisher {
	if bus == nil {
		return nil
	}
	return &LLMEventPublisher{
		bus: bus,
	}
}

// PublishLLMRequest publishes an event when an LLM request is initiated.
// Creates an EventTypeLLMRequest event with model and token information.
func (p *LLMEventPublisher) PublishLLMRequest(sessionID, agentID, model string, tokenCount int) error {
	if p == nil || p.bus == nil {
		return fmt.Errorf("event publisher not initialized")
	}

	event := events.NewActivityEvent(
		events.EventTypeLLMRequest,
		sessionID,
		fmt.Sprintf("LLM request to %s with %d tokens", model, tokenCount),
	)
	event.AgentID = agentID
	event.Data = map[string]any{
		"model":        model,
		"input_tokens": tokenCount,
		"timestamp":    time.Now().UnixMilli(),
	}
	event.Category = "llm"

	p.bus.Publish(event)
	return nil
}

// PublishLLMResponse publishes an event when an LLM response is received.
// Creates an EventTypeLLMResponse event with full metrics including latency.
func (p *LLMEventPublisher) PublishLLMResponse(sessionID, agentID, model string, inputTokens, outputTokens int, duration time.Duration) error {
	if p == nil || p.bus == nil {
		return fmt.Errorf("event publisher not initialized")
	}

	// Calculate tokens per second
	var tokensPerSec float64
	if duration > 0 {
		tokensPerSec = float64(outputTokens) / duration.Seconds()
	}

	event := events.NewActivityEvent(
		events.EventTypeLLMResponse,
		sessionID,
		fmt.Sprintf("LLM response from %s: %d input, %d output tokens in %v", model, inputTokens, outputTokens, duration),
	)
	event.AgentID = agentID
	event.Outcome = events.OutcomeSuccess
	event.Data = map[string]any{
		"model":          model,
		"input_tokens":   inputTokens,
		"output_tokens":  outputTokens,
		"duration_ms":    duration.Milliseconds(),
		"tokens_per_sec": tokensPerSec,
	}
	event.Category = "llm"

	p.bus.Publish(event)
	return nil
}

// PublishLLMError publishes an event when an LLM API error occurs.
// Creates an EventTypeAgentError event with error details.
func (p *LLMEventPublisher) PublishLLMError(sessionID, agentID, model string, err error) error {
	if p == nil || p.bus == nil {
		return fmt.Errorf("event publisher not initialized")
	}

	if err == nil {
		return fmt.Errorf("error cannot be nil")
	}

	event := events.NewActivityEvent(
		events.EventTypeAgentError,
		sessionID,
		fmt.Sprintf("LLM error from %s: %v", model, err),
	)
	event.AgentID = agentID
	event.Outcome = events.OutcomeFailure
	event.Data = map[string]any{
		"model":         model,
		"error":         err.Error(),
		"error_type":    "llm_api_error",
		"timestamp":     time.Now().UnixMilli(),
	}
	event.Category = "llm"

	p.bus.Publish(event)
	return nil
}

// =============================================================================
// LLMEventPublisherHook - Adapter implementing LLMProviderEventHook
// =============================================================================

// LLMEventPublisherHook adapts LLMEventPublisher to the LLMProviderEventHook interface.
// This allows existing providers to use the event publisher through the hook interface.
type LLMEventPublisherHook struct {
	publisher *LLMEventPublisher
}

// NewLLMEventPublisherHook creates a new hook adapter for the given publisher.
// Returns nil if publisher is nil.
func NewLLMEventPublisherHook(publisher *LLMEventPublisher) *LLMEventPublisherHook {
	if publisher == nil {
		return nil
	}
	return &LLMEventPublisherHook{
		publisher: publisher,
	}
}

// OnRequest implements LLMProviderEventHook.OnRequest.
// Delegates to the underlying LLMEventPublisher.
func (h *LLMEventPublisherHook) OnRequest(sessionID, agentID, model string, tokenCount int) {
	if h == nil || h.publisher == nil {
		return
	}
	_ = h.publisher.PublishLLMRequest(sessionID, agentID, model, tokenCount)
}

// OnResponse implements LLMProviderEventHook.OnResponse.
// Delegates to the underlying LLMEventPublisher.
func (h *LLMEventPublisherHook) OnResponse(sessionID, agentID, model string, inputTokens, outputTokens int, duration time.Duration) {
	if h == nil || h.publisher == nil {
		return
	}
	_ = h.publisher.PublishLLMResponse(sessionID, agentID, model, inputTokens, outputTokens, duration)
}

// OnError implements LLMProviderEventHook.OnError.
// Delegates to the underlying LLMEventPublisher.
func (h *LLMEventPublisherHook) OnError(sessionID, agentID, model string, err error) {
	if h == nil || h.publisher == nil {
		return
	}
	_ = h.publisher.PublishLLMError(sessionID, agentID, model, err)
}

// =============================================================================
// NoOpLLMEventHook - No-operation hook for testing/disabled event publishing
// =============================================================================

// NoOpLLMEventHook is a no-operation implementation of LLMProviderEventHook.
// Useful for testing or when event publishing should be disabled.
type NoOpLLMEventHook struct{}

// OnRequest implements LLMProviderEventHook.OnRequest (no-op).
func (h *NoOpLLMEventHook) OnRequest(sessionID, agentID, model string, tokenCount int) {}

// OnResponse implements LLMProviderEventHook.OnResponse (no-op).
func (h *NoOpLLMEventHook) OnResponse(sessionID, agentID, model string, inputTokens, outputTokens int, duration time.Duration) {
}

// OnError implements LLMProviderEventHook.OnError (no-op).
func (h *NoOpLLMEventHook) OnError(sessionID, agentID, model string, err error) {}

// Compile-time interface compliance checks
var (
	_ LLMProviderEventHook = (*LLMEventPublisherHook)(nil)
	_ LLMProviderEventHook = (*NoOpLLMEventHook)(nil)
)
