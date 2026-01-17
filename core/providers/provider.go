package providers

import (
	"context"
)

// Provider defines the interface all LLM providers must implement.
// Each provider handles its own client initialization and streaming logic.
type Provider interface {
	// Name returns the provider identifier (e.g., "anthropic", "openai", "google")
	Name() string

	// Generate performs a non-streaming completion request
	Generate(ctx context.Context, req *Request) (*Response, error)

	// Stream performs a streaming completion request, calling the handler for each chunk
	Stream(ctx context.Context, req *Request, handler StreamHandler) error

	// ValidateConfig checks if the provider configuration is valid
	ValidateConfig() error

	// SupportsModel checks if the provider supports the given model
	SupportsModel(model string) bool

	// DefaultModel returns the provider's default model
	DefaultModel() string

	// Close cleans up any resources held by the provider
	Close() error
}

// StreamHandler is called for each chunk during streaming
type StreamHandler func(chunk *StreamChunk) error

// Request represents a completion request to any provider
type Request struct {
	// Messages is the conversation history
	Messages []Message `json:"messages"`

	// Model overrides the provider's default model (optional)
	Model string `json:"model,omitempty"`

	// MaxTokens overrides the provider's default max tokens (optional)
	MaxTokens int `json:"max_tokens,omitempty"`

	// Temperature controls randomness (0.0-1.0)
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP controls nucleus sampling
	TopP *float64 `json:"top_p,omitempty"`

	// StopSequences are strings that stop generation
	StopSequences []string `json:"stop_sequences,omitempty"`

	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// SystemPrompt is the system instruction (some providers handle differently)
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Tools available for the model to use
	Tools []Tool `json:"tools,omitempty"`

	// Metadata for tracking and logging
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Message represents a single message in a conversation
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`

	// ToolCalls contains tool invocations (for assistant messages)
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID links a tool result to its call (for tool messages)
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Role represents the message sender
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// Tool represents a tool/function the model can call
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ToolCall represents a tool invocation by the model
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// Response represents a completion response from any provider
type Response struct {
	// Content is the generated text
	Content string `json:"content"`

	// Model is the actual model used
	Model string `json:"model"`

	// StopReason indicates why generation stopped
	StopReason StopReason `json:"stop_reason"`

	// Usage contains token counts
	Usage Usage `json:"usage"`

	// ToolCalls if the model invoked tools
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ProviderMetadata contains provider-specific response data
	ProviderMetadata map[string]any `json:"provider_metadata,omitempty"`
}

// StopReason indicates why generation stopped
type StopReason string

const (
	StopReasonEndTurn      StopReason = "end_turn"
	StopReasonMaxTokens    StopReason = "max_tokens"
	StopReasonStopSequence StopReason = "stop_sequence"
	StopReasonToolUse      StopReason = "tool_use"
	StopReasonError        StopReason = "error"
)

// Usage contains token usage information
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	// CacheReadTokens for providers that support prompt caching
	CacheReadTokens int `json:"cache_read_tokens,omitempty"`

	// CacheWriteTokens for providers that support prompt caching
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}
