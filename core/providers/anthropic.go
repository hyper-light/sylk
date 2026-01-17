package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/skills"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicProvider implements Provider for Anthropic's Claude models
type AnthropicProvider struct {
	client *anthropic.Client
	config AnthropicConfig
	skills []skills.Skill
}

type AnthropicModel string

const (
	Opus              AnthropicModel = "claude-opus-4-5-20251101"
	SonnetLongContext AnthropicModel = "claude-sonnet-4-5-20250901"
	Haiku             AnthropicModel = "claude-haiku-4-5-20251001"
)

// Supported Anthropic models
var anthropicModels = map[string]bool{
	// Claude 4.5 family
	"claude-opus-4-5-20251101":   true, // Claude Opus 4.5
	"claude-sonnet-4-5-20250901": true, // Claude Sonnet 4.5 (1M token window)
	"claude-haiku-4-5-20251001":  true, // Claude Haiku 4.5
}

// NewAnthropicProvider creates a new Anthropic provider with the given configuration
func NewAnthropicProvider(config AnthropicConfig, skills ...skills.Skill) (*AnthropicProvider, error) {
	if config.Model == "" {
		config.Model = DefaultAnthropicConfig().Model
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = DefaultAnthropicConfig().MaxTokens
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
	}

	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}

	betaOpts := []string{
		string(anthropic.AnthropicBetaInterleavedThinking2025_05_14),
		"fine-grained-tool-streaming-2025-05-14",
	}

	if config.Model == string(SonnetLongContext) {
		betaOpts = append(betaOpts, string(anthropic.AnthropicBetaContext1m2025_08_07))

	}

	opts = append(opts, option.WithHeader("anthropic-beta", strings.Join(betaOpts, ",")))

	client := anthropic.NewClient(opts...)

	return &AnthropicProvider{
		client: &client,
		config: config,
		skills: skills,
	}, nil
}

// Name returns the provider identifier
func (p *AnthropicProvider) Name() string {
	return string(ProviderTypeAnthropic)
}

// Generate performs a non-streaming completion request
func (p *AnthropicProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	params := p.buildParams(req)

	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic generate: %w", err)
	}

	return p.convertResponse(msg), nil
}

func (p *AnthropicProvider) StreamWithHandler(ctx context.Context, req *Request, handler StreamHandler) error {
	params := p.buildParams(req)

	stream := p.client.Messages.NewStreaming(ctx, params)

	// Send start chunk
	if err := handler(&StreamChunk{
		Index:     0,
		Type:      ChunkTypeStart,
		Timestamp: time.Now(),
	}); err != nil {
		return err
	}

	var chunkIndex int
	var inputTokens, outputTokens int
	var cacheReadTokens, cacheWriteTokens int
	var stopReason StopReason
	toolCallIDForIndex := map[int64]string{}

	for stream.Next() {
		event := stream.Current()
		chunkIndex++

		chunk := p.convertStreamEvent(event, chunkIndex, toolCallIDForIndex)
		if chunk != nil {
			if err := handler(chunk); err != nil {
				return err
			}
		}

		switch ev := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			if ev.Message.Usage.InputTokens > 0 {
				inputTokens = int(ev.Message.Usage.InputTokens)
			}
		case anthropic.MessageDeltaEvent:
			if ev.Usage.OutputTokens > 0 {
				outputTokens = int(ev.Usage.OutputTokens)
			}
			if ev.Usage.CacheReadInputTokens > 0 {
				cacheReadTokens = int(ev.Usage.CacheReadInputTokens)
			}
			if ev.Usage.CacheCreationInputTokens > 0 {
				cacheWriteTokens = int(ev.Usage.CacheCreationInputTokens)
			}
			if ev.Delta.StopReason != "" {
				stopReason = p.convertStopReason(ev.Delta.StopReason)
			}
		}
	}

	if err := stream.Err(); err != nil {
		// Send error chunk
		handler(&StreamChunk{
			Index:     chunkIndex + 1,
			Type:      ChunkTypeError,
			Text:      err.Error(),
			Timestamp: time.Now(),
		})
		return fmt.Errorf("anthropic stream: %w", err)
	}

	if stopReason == "" {
		stopReason = StopReasonEndTurn
	}

	return handler(&StreamChunk{
		Index:      chunkIndex + 1,
		Type:       ChunkTypeEnd,
		StopReason: stopReason,
		Usage: &Usage{
			InputTokens:      inputTokens,
			OutputTokens:     outputTokens,
			TotalTokens:      inputTokens + outputTokens,
			CacheReadTokens:  cacheReadTokens,
			CacheWriteTokens: cacheWriteTokens,
		},
		Timestamp: time.Now(),
	})

}

func (p *AnthropicProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	chunks := make(chan *StreamChunk, 100)
	go func() {
		defer close(chunks)
		_ = p.StreamWithHandler(ctx, req, func(chunk *StreamChunk) error {
			select {
			case chunks <- chunk:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	return chunks, nil
}

// ValidateConfig checks if the provider configuration is valid
func (p *AnthropicProvider) ValidateConfig() error {
	return p.config.Validate()
}

// SupportsModel checks if the provider supports the given model
func (p *AnthropicProvider) SupportsModel(model string) bool {
	return anthropicModels[model]
}

// DefaultModel returns the provider's default model
func (p *AnthropicProvider) DefaultModel() string {
	return p.config.Model
}

// Close cleans up any resources
func (p *AnthropicProvider) Close() error {
	return nil
}

func (p *AnthropicProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	return p.Generate(ctx, req)
}

func (p *AnthropicProvider) SupportedModels() []ModelInfo {
	return []ModelInfo{
		{ID: "claude-opus-4-5-20251101", Name: "Claude Opus 4.5", MaxContext: 200000},
		{ID: "claude-sonnet-4-5-20250901", Name: "Claude Sonnet 4.5", MaxContext: 1000000},
		{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", MaxContext: 200000},
	}
}

func (p *AnthropicProvider) CountTokens(messages []Message) (int, error) {
	count := 0
	for _, msg := range messages {
		count += len(msg.Content) / 4
	}
	return count, nil
}

func (p *AnthropicProvider) MaxContextTokens(model string) int {
	if model == "claude-sonnet-4-5-20250901" {
		return 1000000
	}
	return 200000
}

func (p *AnthropicProvider) HealthCheck(ctx context.Context) error {
	return nil
}

// buildParams constructs Anthropic API parameters from a Request
func (p *AnthropicProvider) buildParams(req *Request) anthropic.MessageNewParams {
	model := p.config.Model
	if model == "" {
		model = p.config.Model
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.config.MaxTokens
	}

	if len(p.skills) > 0 {
		p.config.SystemPrompt = strings.Join(
			[]string{
				p.config.SystemPrompt,
				skills.ToPrompt(p.skills),
			},
			"\n",
		)
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(maxTokens),
		System: []anthropic.TextBlockParam{
			{Text: p.config.SystemPrompt},
		},
		Messages: p.convertMessages(req.Messages),
		Tools:    p.convertTools(req.Tools),
	}

	if req.Temperature != nil {
		params.Temperature = anthropic.Float(*req.Temperature)
	} else if p.config.Temperature > 0 {
		params.Temperature = anthropic.Float(p.config.Temperature)
	}

	if req.TopP != nil {
		params.TopP = anthropic.Float(*req.TopP)
	}

	if len(req.StopSequences) > 0 {
		params.StopSequences = req.StopSequences
	}

	return params
}

// convertMessages converts generic messages to Anthropic format
func (p *AnthropicProvider) convertMessages(messages []Message) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			result = append(result, anthropic.NewUserMessage(
				anthropic.NewTextBlock(msg.Content),
			))

		case RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.ToolCalls)+1)
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					blocks = append(blocks, anthropic.ContentBlockParamUnion{
						OfToolUse: &anthropic.ToolUseBlockParam{
							ID:    tc.ID,
							Name:  tc.Name,
							Input: tc.Arguments,
						},
					})
				}
				result = append(result, anthropic.NewAssistantMessage(blocks...))
			} else {
				result = append(result, anthropic.NewAssistantMessage(
					anthropic.NewTextBlock(msg.Content),
				))
			}

		case RoleTool:
			result = append(result, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false),
			))
		}
	}

	return result
}

// convertTools converts generic tools to Anthropic format
func (p *AnthropicProvider) convertTools(tools []Tool) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, len(tools))
	for i, tool := range tools {
		result[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name,
				Description: anthropic.String(tool.Description),
				InputSchema: buildAnthropicSchema(tool.Parameters),
			},
		}
	}
	return result
}

func buildAnthropicSchema(params map[string]any) anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Type:       "object",
		Properties: params["properties"],
		Required:   extractRequiredFields(params),
	}
}

func extractRequiredFields(params map[string]any) []string {
	req, ok := params["required"].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(req))
	for _, r := range req {
		if s, ok := r.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// convertResponse converts an Anthropic response to generic format
func (p *AnthropicProvider) convertResponse(msg *anthropic.Message) *Response {
	var content string
	var toolCalls []ToolCall

	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			content += b.Text
		case anthropic.ToolUseBlock:
			args, _ := b.Input.MarshalJSON()
			toolCalls = append(toolCalls, ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: string(args),
			})
		}
	}

	return &Response{
		Content:    content,
		Model:      string(msg.Model),
		StopReason: p.convertStopReason(msg.StopReason),
		Usage: Usage{
			InputTokens:     int(msg.Usage.InputTokens),
			OutputTokens:    int(msg.Usage.OutputTokens),
			TotalTokens:     int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
			CacheReadTokens: int(msg.Usage.CacheReadInputTokens),
		},
		ToolCalls: toolCalls,
		ProviderMetadata: map[string]any{
			"id": msg.ID,
		},
	}
}

// convertStreamEvent converts an Anthropic stream event to a StreamChunk
func (p *AnthropicProvider) convertStreamEvent(event anthropic.MessageStreamEventUnion, index int, toolCallIDForIndex map[int64]string) *StreamChunk {
	switch ev := event.AsAny().(type) {
	case anthropic.ContentBlockDeltaEvent:
		switch delta := ev.Delta.AsAny().(type) {
		case anthropic.TextDelta:
			return &StreamChunk{
				Index:     index,
				Type:      ChunkTypeText,
				Text:      delta.Text,
				Timestamp: time.Now(),
			}
		case anthropic.InputJSONDelta:
			toolID := toolCallIDForIndex[ev.Index]
			if toolID == "" {
				return nil
			}
			return &StreamChunk{
				Index: index,
				Type:  ChunkTypeToolDelta,
				ToolCall: &ToolCallChunk{
					ID:             toolID,
					ArgumentsDelta: delta.PartialJSON,
				},
				Timestamp: time.Now(),
			}
		}

	case anthropic.ContentBlockStartEvent:
		if ev.ContentBlock.Type == "tool_use" {
			tb := ev.ContentBlock.AsAny().(anthropic.ToolUseBlock)
			toolCallIDForIndex[ev.Index] = tb.ID
			return &StreamChunk{
				Index: index,
				Type:  ChunkTypeToolStart,
				ToolCall: &ToolCallChunk{
					ID:   tb.ID,
					Name: tb.Name,
				},
				Timestamp: time.Now(),
			}
		}

	case anthropic.ContentBlockStopEvent:
		toolID := toolCallIDForIndex[ev.Index]
		if toolID == "" {
			return nil
		}
		return &StreamChunk{
			Index: index,
			Type:  ChunkTypeToolEnd,
			ToolCall: &ToolCallChunk{
				ID: toolID,
			},
			Timestamp: time.Now(),
		}
	}

	return nil
}

// convertStopReason converts Anthropic stop reason to generic format
func (p *AnthropicProvider) convertStopReason(reason anthropic.StopReason) StopReason {
	switch reason {
	case anthropic.StopReasonEndTurn:
		return StopReasonEndTurn
	case anthropic.StopReasonMaxTokens:
		return StopReasonMaxTokens
	case anthropic.StopReasonStopSequence:
		return StopReasonStopSequence
	case anthropic.StopReasonToolUse:
		return StopReasonToolUse
	default:
		return StopReasonEndTurn
	}
}
