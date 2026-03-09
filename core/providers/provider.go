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
	Messages             []Message      `json:"messages"`
	Model                string         `json:"model,omitempty"`
	MaxTokens            int            `json:"max_tokens,omitempty"`
	Temperature          *float64       `json:"temperature,omitempty"`
	TopP                 *float64       `json:"top_p,omitempty"`
	StopSequences        []string       `json:"stop_sequences,omitempty"`
	ReasoningEffort      string         `json:"reasoning_effort,omitempty"`
	ReasoningSummary     string         `json:"reasoning_summary,omitempty"`
	Verbosity            string         `json:"verbosity,omitempty"`
	IncludeThoughts      *bool          `json:"include_thoughts,omitempty"`
	PromptCacheKey       string         `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string         `json:"prompt_cache_retention,omitempty"`
	UsePromptCache       *bool          `json:"use_prompt_cache,omitempty"`
	ParallelToolCalls    *bool          `json:"parallel_tool_calls,omitempty"`
	SystemPrompt         string         `json:"system_prompt,omitempty"`
	Tools                []Tool         `json:"tools,omitempty"`
	Metadata             map[string]any `json:"metadata,omitempty"`

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

	// FrequencyPenalty penalizes tokens proportional to how often they have
	// appeared so far. Range depends on provider (typically -2.0 to 2.0).
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

	// PresencePenalty penalizes tokens that have appeared at all. Range
	// depends on provider (typically -2.0 to 2.0).
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`

	// TopK limits sampling to the top K most likely tokens. Only
	// supported by Anthropic (mapped to top_k). Zero means unlimited.
	TopK *int `json:"top_k,omitempty"`

	// DisableParallelToolUse prevents the model from issuing multiple
	// tool calls in a single turn. Supported by Anthropic on all
	// ToolChoice modes (auto, any, tool).
	DisableParallelToolUse bool `json:"disable_parallel_tool_use,omitempty"`
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

type ToolKind string

const (
	ToolKindFunction        ToolKind = "function"
	ToolKindNativeWebSearch ToolKind = "native_web_search"
)

type WebSearchContextSize string

const (
	WebSearchContextSizeLow    WebSearchContextSize = "low"
	WebSearchContextSizeMedium WebSearchContextSize = "medium"
	WebSearchContextSizeHigh   WebSearchContextSize = "high"
)

type WebSearchUserLocation struct {
	City     string `json:"city,omitempty"`
	Country  string `json:"country,omitempty"`
	Region   string `json:"region,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type WebSearchOptions struct {
	SearchContextSize WebSearchContextSize   `json:"search_context_size,omitempty"`
	UserLocation      *WebSearchUserLocation `json:"user_location,omitempty"`
	AllowedDomains    []string               `json:"allowed_domains,omitempty"`
	BlockedDomains    []string               `json:"blocked_domains,omitempty"`
	MaxUses           int                    `json:"max_uses,omitempty"`
	Strict            bool                   `json:"strict,omitempty"`
	DeferLoading      bool                   `json:"defer_loading,omitempty"`
	EnableURLContext  bool                   `json:"enable_url_context,omitempty"`
}

type Tool struct {
	Kind        ToolKind          `json:"kind,omitempty"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  map[string]any    `json:"parameters"`
	WebSearch   *WebSearchOptions `json:"web_search,omitempty"`
}

func (t Tool) ResolvedKind() ToolKind {
	if t.Kind == "" {
		return ToolKindFunction
	}
	return t.Kind
}

func (t Tool) Clone() Tool {
	clone := t
	if t.Parameters != nil {
		clone.Parameters = cloneToolMap(t.Parameters)
	}
	if t.WebSearch != nil {
		ws := *t.WebSearch
		if t.WebSearch.UserLocation != nil {
			location := *t.WebSearch.UserLocation
			ws.UserLocation = &location
		}
		if len(t.WebSearch.AllowedDomains) > 0 {
			ws.AllowedDomains = append([]string(nil), t.WebSearch.AllowedDomains...)
		}
		if len(t.WebSearch.BlockedDomains) > 0 {
			ws.BlockedDomains = append([]string(nil), t.WebSearch.BlockedDomains...)
		}
		clone.WebSearch = &ws
	}
	return clone
}

func cloneToolMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
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
	StopReasonEndTurn       StopReason = "end_turn"
	StopReasonMaxTokens     StopReason = "max_tokens"
	StopReasonStopSequence  StopReason = "stop_sequence"
	StopReasonToolUse       StopReason = "tool_use"
	StopReasonError         StopReason = "error"
	StopReasonContentFilter StopReason = "content_filter"
	StopReasonPauseTurn     StopReason = "pause_turn"
)

type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}
