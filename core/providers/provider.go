package providers

import (
	"context"
)

type ModelInfo struct {
	ID              string
	Name            string
	MaxContext      int
	InputPricePerM  float64
	OutputPricePerM float64
}

type ProviderAdapter interface {
	Name() string
	SupportedModels() []ModelInfo
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req *CompletionRequest) (<-chan *StreamChunk, error)
	CountTokens(messages []Message) (int, error)
	MaxContextTokens(model string) int
	HealthCheck(ctx context.Context) error
}

type Provider = ProviderAdapter

type ProviderValidator interface {
	ValidateConfig() error
}

type ProviderModelSupporter interface {
	SupportsModel(model string) bool
}

type ProviderCloser interface {
	Close() error
}

type StreamHandlerProvider interface {
	StreamWithHandler(ctx context.Context, req *StreamRequest, handler StreamHandler) error
}

type CompletionRequest = Request

type CompletionResponse = Response

type StreamHandler func(chunk *StreamChunk) error

type StreamRequest = Request

type Request struct {
	Messages        []Message      `json:"messages"`
	Model           string         `json:"model,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	TopP            *float64       `json:"top_p,omitempty"`
	StopSequences   []string       `json:"stop_sequences,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	SystemPrompt    string         `json:"system_prompt,omitempty"`
	Tools           []Tool         `json:"tools,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`

	// ToolChoice controls function-calling behavior when Tools are present.
	// Supported values (provider-agnostic):
	//   "auto"  — model decides whether to call a function or respond with text (default)
	//   "any"   — model must call at least one function
	//   "none"  — model must not call any function
	// Empty string means provider default (typically "auto").
	ToolChoice string `json:"tool_choice,omitempty"`

	// ThinkingBudget sets the Anthropic extended thinking token budget.
	// When positive, enables thinking mode with this budget.
	ThinkingBudget int `json:"thinking_budget,omitempty"`

	// ResponseSchema provides structured output schema for Google provider.
	ResponseSchema map[string]any `json:"response_schema,omitempty"`

	// ResponseMIMEType sets the response format for Google provider (e.g. "application/json").
	ResponseMIMEType string `json:"response_mime_type,omitempty"`

	// SkipProviderSkills suppresses ambient skill injection into the system
	// prompt. Classification requests set this to true — they need a focused
	// prompt without the full skill catalogue.
	SkipProviderSkills bool `json:"skip_provider_skills,omitempty"`
}

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`

	// IsError signals that a tool result represents a failed invocation.
	// Used by providers to set the API's is_error flag (e.g. Anthropic).
	IsError bool `json:"is_error,omitempty"`

	// Metadata carries provider-specific data through multi-turn loops.
	// For Google, this preserves raw model content with thought signatures.
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Response struct {
	Content          string         `json:"content"`
	Thinking         string         `json:"thinking,omitempty"`
	Model            string         `json:"model"`
	StopReason       StopReason     `json:"stop_reason"`
	Usage            Usage          `json:"usage"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	ProviderMetadata map[string]any `json:"provider_metadata,omitempty"`
}

type StopReason string

const (
	StopReasonEndTurn      StopReason = "end_turn"
	StopReasonMaxTokens    StopReason = "max_tokens"
	StopReasonStopSequence StopReason = "stop_sequence"
	StopReasonToolUse      StopReason = "tool_use"
	StopReasonError        StopReason = "error"
)

type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}
