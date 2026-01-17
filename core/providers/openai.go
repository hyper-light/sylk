package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/skills"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// OpenAIProvider implements Provider for OpenAI's GPT models
type OpenAIProvider struct {
	client *openai.Client
	config OpenAIConfig
	skills []skills.Skill
}

type OpenAIModel string

const (
	Codex_5_2 OpenAIModel = "gpt-5.2-codex"
)

// Supported OpenAI models
var openaiModels = map[string]bool{
	// OpenAI 5.x family
	"gpt-5.2-codex": true, // OpenAI 5.2 Codex
}

// NewOpenAIProvider creates a new OpenAI provider with the given configuration
func NewOpenAIProvider(config OpenAIConfig, skills ...skills.Skill) (*OpenAIProvider, error) {
	if config.Model == "" {
		config.Model = DefaultOpenAIConfig().Model
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = DefaultOpenAIConfig().MaxTokens
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

	if config.Organization != "" {
		opts = append(opts, option.WithHeader("OpenAI-Organization", config.Organization))
	}

	if config.Project != "" {
		opts = append(opts, option.WithHeader("OpenAI-Project", config.Project))
	}

	client := openai.NewClient(opts...)

	return &OpenAIProvider{
		client: &client,
		config: config,
		skills: skills,
	}, nil
}

// Name returns the provider identifier
func (p *OpenAIProvider) Name() string {
	return string(ProviderTypeOpenAI)
}

// Generate performs a non-streaming completion request
func (p *OpenAIProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	params := p.buildResponseParams(req)

	result, err := p.client.Responses.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai generate: %w", err)
	}

	return p.convertResponse(result), nil
}

// Stream performs a streaming completion request
func (p *OpenAIProvider) Stream(ctx context.Context, req *Request, handler StreamHandler) error {
	params := p.buildResponseParams(req)

	stream := p.client.Responses.NewStreaming(ctx, params)

	if err := handler(&StreamChunk{
		Index:     0,
		Type:      ChunkTypeStart,
		Timestamp: time.Now(),
	}); err != nil {
		return err
	}

	var chunkIndex int
	var toolCallBuilders = make(map[string]*ToolCallChunk)
	var responseStopReason StopReason
	var responseUsage *Usage
	var completion *responses.Response

	for stream.Next() {
		event := stream.Current()
		chunkIndex++

		switch ev := event.AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			if ev.Delta == "" {
				continue
			}
			if err := handler(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeText,
				Text:      ev.Delta,
				Timestamp: time.Now(),
			}); err != nil {
				return err
			}
		case responses.ResponseFunctionCallArgumentsDeltaEvent:
			toolCall, exists := toolCallBuilders[ev.ItemID]
			if !exists {
				toolCall = &ToolCallChunk{ID: ev.ItemID}
				toolCallBuilders[ev.ItemID] = toolCall
				if err := handler(&StreamChunk{
					Index:    chunkIndex,
					Type:     ChunkTypeToolStart,
					ToolCall: toolCall,
				}); err != nil {
					return err
				}
			}

			if ev.Delta != "" {
				if err := handler(&StreamChunk{
					Index: chunkIndex,
					Type:  ChunkTypeToolDelta,
					ToolCall: &ToolCallChunk{
						ID:             ev.ItemID,
						ArgumentsDelta: ev.Delta,
					},
					Timestamp: time.Now(),
				}); err != nil {
					return err
				}
			}
		case responses.ResponseOutputItemAddedEvent:
			if ev.Item.Type != "function_call" {
				continue
			}
			toolCall := toolCallBuilders[ev.Item.ID]
			if toolCall == nil {
				toolCall = &ToolCallChunk{ID: ev.Item.ID}
				toolCallBuilders[ev.Item.ID] = toolCall
			}
			toolCall.Name = ev.Item.Name
		case responses.ResponseCompletedEvent:
			completion = &ev.Response
			usage := p.convertResponseUsage(ev.Response)
			responseUsage = &usage
			responseStopReason = p.convertResponseStopReason(ev.Response)
		case responses.ResponseFailedEvent:
			completion = &ev.Response
			usage := p.convertResponseUsage(ev.Response)
			responseUsage = &usage
			responseStopReason = StopReasonError
		case responses.ResponseIncompleteEvent:
			completion = &ev.Response
			usage := p.convertResponseUsage(ev.Response)
			responseUsage = &usage
			responseStopReason = p.convertIncompleteReason(ev.Response.IncompleteDetails.Reason)
		case responses.ResponseErrorEvent:
			if err := handler(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeError,
				Text:      ev.Message,
				Timestamp: time.Now(),
			}); err != nil {
				return err
			}
			return fmt.Errorf("openai stream: %s", ev.Message)
		}
	}

	if err := stream.Err(); err != nil {
		handler(&StreamChunk{
			Index:     chunkIndex + 1,
			Type:      ChunkTypeError,
			Text:      err.Error(),
			Timestamp: time.Now(),
		})
		return fmt.Errorf("openai stream: %w", err)
	}

	if responseUsage == nil {
		responseUsage = &Usage{}
	}

	if completion == nil {
		responseStopReason = StopReasonEndTurn
	}

	return handler(&StreamChunk{
		Index:      chunkIndex + 1,
		Type:       ChunkTypeEnd,
		StopReason: responseStopReason,
		Usage:      responseUsage,
		Timestamp:  time.Now(),
	})
}

// ValidateConfig checks if the provider configuration is valid
func (p *OpenAIProvider) ValidateConfig() error {
	return p.config.Validate()
}

// SupportsModel checks if the provider supports the given model
func (p *OpenAIProvider) SupportsModel(model string) bool {
	return openaiModels[model]
}

// DefaultModel returns the provider's default model
func (p *OpenAIProvider) DefaultModel() string {
	return p.config.Model
}

// Close cleans up any resources
func (p *OpenAIProvider) Close() error {
	return nil
}

// buildParams constructs OpenAI API parameters from a Request
func (p *OpenAIProvider) buildResponseParams(req *Request) responses.ResponseNewParams {
	model := req.Model
	if model == "" {
		model = p.config.Model
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.config.MaxTokens
	}

	messages := p.convertResponseMessages(req.Messages, req.SystemPrompt)

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: messages,
		},
		MaxOutputTokens: openai.Int(int64(maxTokens)),
	}

	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	} else if p.config.Temperature > 0 {
		params.Temperature = openai.Float(p.config.Temperature)
	}

	if req.TopP != nil {
		params.TopP = openai.Float(*req.TopP)
	}

	reasoningEffort := req.ReasoningEffort
	if reasoningEffort == "" {
		reasoningEffort = p.config.ReasoningEffort
	}
	if reasoningEffort == "xhigh" {
		params.Reasoning = shared.ReasoningParam{}
		params.Reasoning.SetExtraFields(map[string]any{"effort": "xhigh"})
	} else if reasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{}
		params.Reasoning.Effort = shared.ReasoningEffort(reasoningEffort)
	}

	if len(req.Tools) > 0 {
		params.Tools = p.convertResponseTools(req.Tools)
	}

	return params
}

func (p *OpenAIProvider) convertResponseMessages(messages []Message, systemPrompt string) responses.ResponseInputParam {
	result := make(responses.ResponseInputParam, 0, len(messages)+1)

	if systemPrompt != "" {
		result = append(result, responses.ResponseInputItemParamOfMessage(systemPrompt, responses.EasyInputMessageRoleSystem))
	}

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			result = append(result, responses.ResponseInputItemParamOfMessage(msg.Content, responses.EasyInputMessageRoleSystem))
		case RoleUser:
			result = append(result, responses.ResponseInputItemParamOfMessage(msg.Content, responses.EasyInputMessageRoleUser))
		case RoleAssistant:
			result = append(result, responses.ResponseInputItemParamOfMessage(msg.Content, responses.EasyInputMessageRoleAssistant))
		case RoleTool:
			result = append(result, responses.ResponseInputItemParamOfFunctionCallOutput(msg.ToolCallID, msg.Content))
		}
	}

	return result
}

func (p *OpenAIProvider) convertResponseTools(tools []Tool) []responses.ToolUnionParam {
	result := make([]responses.ToolUnionParam, len(tools))
	for i, tool := range tools {
		result[i] = responses.ToolParamOfFunction(tool.Name, ensureObjectType(tool.Parameters), true)
		if tool.Description != "" {
			desc := openai.String(tool.Description)
			function := result[i].OfFunction
			function.Description = desc
			result[i].OfFunction = function
		}
	}
	return result
}

func (p *OpenAIProvider) convertResponse(result *responses.Response) *Response {
	if result == nil {
		return &Response{StopReason: StopReasonError}
	}

	response := &Response{
		Content:    result.OutputText(),
		Model:      string(result.Model),
		StopReason: p.convertResponseStopReason(*result),
		Usage:      p.convertResponseUsage(*result),
		ProviderMetadata: map[string]any{
			"id": result.ID,
		},
	}

	toolCalls := p.extractToolCalls(*result)
	if len(toolCalls) > 0 {
		response.ToolCalls = toolCalls
	}

	return response
}

func (p *OpenAIProvider) convertResponseUsage(result responses.Response) Usage {
	usage := result.Usage
	return Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),
		TotalTokens:  int(usage.TotalTokens),
	}
}

func (p *OpenAIProvider) convertResponseStopReason(result responses.Response) StopReason {
	if result.IncompleteDetails.Reason != "" {
		return p.convertIncompleteReason(result.IncompleteDetails.Reason)
	}
	if result.Error.Message != "" {
		return StopReasonError
	}
	return StopReasonEndTurn
}

func (p *OpenAIProvider) convertIncompleteReason(reason string) StopReason {
	switch reason {
	case "max_output_tokens":
		return StopReasonMaxTokens
	case "content_filter":
		return StopReasonError
	default:
		return StopReasonEndTurn
	}
}

func (p *OpenAIProvider) extractToolCalls(result responses.Response) []ToolCall {
	var toolCalls []ToolCall
	for _, item := range result.Output {
		switch item.Type {
		case "function_call":
			toolCalls = append(toolCalls, ToolCall{
				ID:        item.ID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
		}
	}
	return toolCalls
}

func ensureObjectType(params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{"type": "object"}
	}
	if _, hasType := params["type"]; !hasType {
		params["type"] = "object"
	}
	return params
}
