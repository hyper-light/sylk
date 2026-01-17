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
}

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
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
