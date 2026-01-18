// Package coordinator provides the LLM request coordination system.
// It orchestrates all protection layers including bulkheads, rate limiting,
// health monitoring, backpressure, failure correlation, and streaming timeouts.
package coordinator

import (
	"context"
	"errors"
	"time"

	"github.com/adalundhe/sylk/core/llm/backpressure"
	"github.com/adalundhe/sylk/core/llm/bulkhead"
	"github.com/adalundhe/sylk/core/llm/correlation"
	"github.com/adalundhe/sylk/core/llm/health"
	"github.com/adalundhe/sylk/core/llm/ratelimit"
	"github.com/adalundhe/sylk/core/llm/timeout"
)

// CoordinatorConfig combines all component configurations for the LLM coordinator.
type CoordinatorConfig struct {
	// BulkheadConfigs maps each isolation level to its configuration.
	BulkheadConfigs map[bulkhead.BulkheadLevel]bulkhead.BulkheadConfig
	// RateLimitConfig configures multi-layer rate limiting.
	RateLimitConfig ratelimit.MultiLayerConfig
	// HealthConfig configures health monitoring behavior.
	HealthConfig health.HealthConfig
	// BackpressureConfig configures cost-aware backpressure.
	BackpressureConfig backpressure.CostBackpressureConfig
	// CorrelationConfig configures failure correlation detection.
	CorrelationConfig correlation.CorrelationConfig
	// StreamTimeoutConfig configures streaming response timeouts.
	StreamTimeoutConfig timeout.StreamingTimeoutConfig
}

// DefaultCoordinatorConfig returns production defaults for the coordinator.
func DefaultCoordinatorConfig() CoordinatorConfig {
	return CoordinatorConfig{
		BulkheadConfigs:     bulkhead.DefaultBulkheadConfigs(),
		RateLimitConfig:     ratelimit.DefaultMultiLayerConfig(),
		HealthConfig:        health.DefaultHealthConfig(),
		BackpressureConfig:  backpressure.DefaultCostBackpressureConfig(),
		CorrelationConfig:   correlation.DefaultCorrelationConfig(),
		StreamTimeoutConfig: timeout.DefaultStreamingTimeoutConfig(),
	}
}

// Validate checks if the configuration is valid.
func (c CoordinatorConfig) Validate() error {
	if err := c.validateBulkheadConfigs(); err != nil {
		return err
	}
	return c.validateOtherConfigs()
}

// validateBulkheadConfigs validates the bulkhead configurations.
func (c CoordinatorConfig) validateBulkheadConfigs() error {
	if c.BulkheadConfigs == nil {
		return ErrNilBulkheadConfigs
	}
	return nil
}

// validateOtherConfigs validates the non-bulkhead configurations.
func (c CoordinatorConfig) validateOtherConfigs() error {
	if err := c.CorrelationConfig.Validate(); err != nil {
		return err
	}
	if err := c.StreamTimeoutConfig.Validate(); err != nil {
		return err
	}
	if !c.BackpressureConfig.Validate() {
		return ErrInvalidBackpressureConfig
	}
	return nil
}

// LLMRequest represents an LLM request to be coordinated.
type LLMRequest struct {
	// SessionID identifies the session making the request.
	SessionID string
	// TaskID identifies the specific task within the session.
	TaskID string
	// Provider is the LLM provider name (e.g., "openai", "anthropic").
	Provider string
	// Model is the model name (e.g., "gpt-4", "claude-3").
	Model string
	// Messages contains the conversation messages.
	Messages []Message
	// Stream indicates whether to use streaming response.
	Stream bool
}

// NewLLMRequest creates a new LLMRequest with the given parameters.
func NewLLMRequest(sessionID, taskID, provider, model string) *LLMRequest {
	return &LLMRequest{
		SessionID: sessionID,
		TaskID:    taskID,
		Provider:  provider,
		Model:     model,
		Messages:  make([]Message, 0),
	}
}

// AddMessage appends a message to the request.
func (r *LLMRequest) AddMessage(role, content string) {
	r.Messages = append(r.Messages, Message{Role: role, Content: content})
}

// WithStreaming sets the streaming flag and returns the request.
func (r *LLMRequest) WithStreaming(stream bool) *LLMRequest {
	r.Stream = stream
	return r
}

// Validate checks if the request has required fields.
func (r *LLMRequest) Validate() error {
	if r.SessionID == "" {
		return ErrMissingSessionID
	}
	if r.Provider == "" {
		return ErrMissingProvider
	}
	if r.Model == "" {
		return ErrMissingModel
	}
	return nil
}

// Message represents a chat message in an LLM conversation.
type Message struct {
	// Role is the message role (e.g., "user", "assistant", "system").
	Role string
	// Content is the message content.
	Content string
}

// NewMessage creates a new Message with the given role and content.
func NewMessage(role, content string) Message {
	return Message{Role: role, Content: content}
}

// IsEmpty returns true if the message has no content.
func (m Message) IsEmpty() bool {
	return m.Content == ""
}

// LLMResponse represents an LLM response from the coordinator.
type LLMResponse struct {
	// Content is the complete response content (for non-streaming).
	Content string
	// Chunks is a channel for streaming response chunks.
	Chunks chan string
	// Latency is the time taken for the request.
	Latency time.Duration
	// Usage contains token usage information.
	Usage TokenUsage
}

// NewLLMResponse creates a new non-streaming LLMResponse.
func NewLLMResponse(content string, latency time.Duration) *LLMResponse {
	return &LLMResponse{
		Content: content,
		Latency: latency,
	}
}

// NewStreamingLLMResponse creates a new streaming LLMResponse with a buffered channel.
func NewStreamingLLMResponse(bufferSize int) *LLMResponse {
	return &LLMResponse{
		Chunks: make(chan string, bufferSize),
	}
}

// WithUsage sets the token usage and returns the response.
func (r *LLMResponse) WithUsage(input, output int) *LLMResponse {
	r.Usage = TokenUsage{InputTokens: input, OutputTokens: output}
	return r
}

// IsStreaming returns true if this is a streaming response.
func (r *LLMResponse) IsStreaming() bool {
	return r.Chunks != nil
}

// TokenUsage represents token usage information for an LLM request.
type TokenUsage struct {
	// InputTokens is the number of tokens in the input.
	InputTokens int
	// OutputTokens is the number of tokens in the output.
	OutputTokens int
}

// TotalTokens returns the total number of tokens used.
func (u TokenUsage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// IsEmpty returns true if no tokens were used.
func (u TokenUsage) IsEmpty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0
}

// CoordinatorStats provides statistics for the coordinator.
type CoordinatorStats struct {
	// Sessions maps session IDs to their hierarchy statistics.
	Sessions map[string]bulkhead.HierarchyStats
	// Providers maps provider names to their statistics.
	Providers map[string]ProviderStats
}

// NewCoordinatorStats creates a new CoordinatorStats with initialized maps.
func NewCoordinatorStats() CoordinatorStats {
	return CoordinatorStats{
		Sessions:  make(map[string]bulkhead.HierarchyStats),
		Providers: make(map[string]ProviderStats),
	}
}

// SessionCount returns the number of active sessions.
func (s CoordinatorStats) SessionCount() int {
	return len(s.Sessions)
}

// ProviderCount returns the number of tracked providers.
func (s CoordinatorStats) ProviderCount() int {
	return len(s.Providers)
}

// ProviderStats provides statistics for a single provider.
type ProviderStats struct {
	// HealthScore is the current health score (0-1).
	HealthScore float64
}

// IsHealthy returns true if the health score is above the threshold.
func (s ProviderStats) IsHealthy(threshold float64) bool {
	return s.HealthScore >= threshold
}

// Error types for coordinator operations.
var (
	// ErrGlobalBackoff is returned when global backoff is active for a provider.
	ErrGlobalBackoff = errors.New("global backoff active for provider")
	// ErrProviderUnhealthy is returned when a provider fails health checks.
	ErrProviderUnhealthy = errors.New("provider health check failed")
	// ErrRateLimited is returned when the request is rate limited.
	ErrRateLimited = errors.New("rate limited")
	// ErrBudgetExhausted is returned when the budget is exhausted.
	ErrBudgetExhausted = errors.New("budget exhausted")
	// ErrMissingSessionID is returned when session ID is not provided.
	ErrMissingSessionID = errors.New("session ID is required")
	// ErrMissingProvider is returned when provider is not specified.
	ErrMissingProvider = errors.New("provider is required")
	// ErrMissingModel is returned when model is not specified.
	ErrMissingModel = errors.New("model is required")
	// ErrNilBulkheadConfigs is returned when bulkhead configs are nil.
	ErrNilBulkheadConfigs = errors.New("bulkhead configs cannot be nil")
	// ErrInvalidBackpressureConfig is returned when backpressure config is invalid.
	ErrInvalidBackpressureConfig = errors.New("invalid backpressure configuration")
)

// LLMProvider interface for provider implementations.
type LLMProvider interface {
	// Complete executes a non-streaming LLM request.
	Complete(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
	// Stream executes a streaming LLM request and returns a channel of chunks.
	Stream(ctx context.Context, req *LLMRequest) <-chan string
	// Name returns the provider name.
	Name() string
}
