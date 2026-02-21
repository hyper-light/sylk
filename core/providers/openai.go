package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/skills"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// OpenAIProvider implements Provider for OpenAI's GPT models
type OpenAIProvider struct {
	client      *openai.Client
	config      OpenAIConfig
	skills      []skills.Skill
	authService oauth.OpenAIAuthService
}

type OpenAIModel string

const (
	Codex_5_3 OpenAIModel = "gpt-5.3-codex"
	Codex_5_2 OpenAIModel = "gpt-5.2-codex"
)

const (
	openAIAuthModeAPIKey  = "api_key"
	openAIAuthModeChatGPT = "chatgpt"

	// Mirrors Codex chatgpt-mode backend routing.
	defaultChatGPTCodexBaseURL = "https://chatgpt.com/backend-api/codex"
)

// Supported OpenAI models (canonical slugs).
var openaiModelCatalog = map[string]ModelInfo{
	"gpt-5.3-codex": {ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex", MaxContext: 200000},
	"gpt-5.2-codex": {ID: "gpt-5.2-codex", Name: "GPT-5.2 Codex", MaxContext: 200000},
}

var openaiModelAliases = map[string]string{
	"codex-5.3":          "gpt-5.3-codex",
	"codex-5.2":          "gpt-5.2-codex",
	"codex-5-2-20250901": "gpt-5.2-codex",
	"codex-5-3-20251001": "gpt-5.3-codex",
	"gpt-5-3-codex":      "gpt-5.3-codex",
	"gpt-5-2-codex":      "gpt-5.2-codex",
}

// NewOpenAIProvider creates a new OpenAI provider with the given configuration
func NewOpenAIProvider(config OpenAIConfig, skills ...skills.Skill) (*OpenAIProvider, error) {
	return NewOpenAIProviderWithAuthService(config, oauth.NewOpenAIAuthService(oauth.OpenAIAuthServiceConfig{}), skills...)
}

// NewOpenAIProviderWithAuthService creates a provider using a custom auth service.
// This enables callers to orchestrate OAuth behavior through a shared interface.
func NewOpenAIProviderWithAuthService(
	config OpenAIConfig,
	authService oauth.OpenAIAuthService,
	skills ...skills.Skill,
) (*OpenAIProvider, error) {
	if config.Model == "" {
		config.Model = DefaultOpenAIConfig().Model
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = DefaultOpenAIConfig().MaxTokens
	}
	if config.AuthMode == "" {
		config.AuthMode = DefaultOpenAIConfig().AuthMode
	}
	if config.FallbackModel == "" {
		config.FallbackModel = DefaultOpenAIConfig().FallbackModel
	}
	config.Model = normalizeOpenAIModel(config.Model)
	config.FallbackModel = normalizeOpenAIModel(config.FallbackModel)
	if config.AuthMode == openAIAuthModeChatGPT && config.BaseURL == "" {
		config.BaseURL = defaultChatGPTCodexBaseURL
	}
	if err := hydrateOpenAIConfig(context.Background(), &config, authService); err != nil {
		return nil, err
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithMaxRetries(config.MaxRetries),
	}
	if config.Timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(config.Timeout))
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
	if config.AuthMode == openAIAuthModeChatGPT && config.ChatGPTAccountID != "" {
		opts = append(opts, option.WithHeader("ChatGPT-Account-ID", config.ChatGPTAccountID))
	}

	client := openai.NewClient(opts...)

	return &OpenAIProvider{
		client:      &client,
		config:      config,
		skills:      skills,
		authService: authService,
	}, nil
}

func hydrateOpenAIConfig(ctx context.Context, config *OpenAIConfig, authService oauth.OpenAIAuthService) error {
	if config == nil {
		return fmt.Errorf("openai config is nil")
	}

	config.APIKey = strings.TrimSpace(config.APIKey)
	config.ChatGPTAccountID = strings.TrimSpace(config.ChatGPTAccountID)
	config.AuthMode = strings.TrimSpace(config.AuthMode)
	if config.AuthMode == "" {
		config.AuthMode = openAIAuthModeAPIKey
	}

	switch config.AuthMode {
	case openAIAuthModeChatGPT:
		missingToken := config.APIKey == ""
		missingAccount := config.ChatGPTAccountID == ""
		if (missingToken || missingAccount) && authService != nil {
			auth, err := authService.Resolve(ctx)
			if err == nil && auth != nil {
				if missingToken {
					config.APIKey = strings.TrimSpace(auth.AccessToken)
				}
				if missingAccount {
					config.ChatGPTAccountID = strings.TrimSpace(auth.ChatGPTAccountID)
				}
			} else if !errors.Is(err, oauth.ErrAuthNotConfigured) {
				return fmt.Errorf("resolve chatgpt auth: %w", err)
			}
		}

		if strings.TrimSpace(config.APIKey) == "" {
			return fmt.Errorf("openai chatgpt auth requires access token")
		}
		if strings.TrimSpace(config.ChatGPTAccountID) == "" {
			return fmt.Errorf("openai chatgpt auth requires chatgpt_account_id")
		}
		return nil
	default:
		if config.APIKey == "" {
			if key, err := llm.ResolveAPIKey("openai"); err == nil {
				config.APIKey = strings.TrimSpace(key)
			}
		}
		return nil
	}
}

// Name returns the provider identifier
func (p *OpenAIProvider) Name() string {
	return string(ProviderTypeOpenAI)
}

// Generate performs a non-streaming completion request
func (p *OpenAIProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		req = &Request{}
	}

	params := p.buildResponseParams(req)
	requestedModel := string(params.Model)

	if p.config.AuthMode == openAIAuthModeChatGPT {
		resp, err := p.generateViaStreaming(ctx, req)
		if err != nil {
			if fallbackModel, ok := p.selectFallbackModel(requestedModel, err); ok {
				retryReq := *req
				retryReq.Model = fallbackModel
				resp, err = p.generateViaStreaming(ctx, &retryReq)
				if err == nil {
					if resp.ProviderMetadata == nil {
						resp.ProviderMetadata = map[string]any{}
					}
					resp.ProviderMetadata["requested_model"] = requestedModel
					resp.ProviderMetadata["fallback_model"] = fallbackModel
					return resp, nil
				}
			}
			return nil, fmt.Errorf("openai generate: %w", err)
		}
		return resp, nil
	}

	result, err := p.client.Responses.New(ctx, params)
	if err != nil {
		if fallbackModel, ok := p.selectFallbackModel(requestedModel, err); ok {
			retryReq := *req
			retryReq.Model = fallbackModel
			result, err = p.client.Responses.New(ctx, p.buildResponseParams(&retryReq))
			if err == nil {
				resp := p.convertResponse(result)
				if resp.ProviderMetadata == nil {
					resp.ProviderMetadata = map[string]any{}
				}
				resp.ProviderMetadata["requested_model"] = requestedModel
				resp.ProviderMetadata["fallback_model"] = fallbackModel
				return resp, nil
			}
		}
		return nil, fmt.Errorf("openai generate: %w", err)
	}

	return p.convertResponse(result), nil
}

func (p *OpenAIProvider) generateViaStreaming(ctx context.Context, req *Request) (*Response, error) {
	var content strings.Builder
	toolCalls := make(map[string]*ToolCall)
	toolCallOrder := make([]string, 0, 4)
	ensureToolCall := func(id string) *ToolCall {
		tc, ok := toolCalls[id]
		if ok {
			return tc
		}
		tc = &ToolCall{ID: id}
		toolCalls[id] = tc
		toolCallOrder = append(toolCallOrder, id)
		return tc
	}

	completion, streamStopReason, streamUsage, err := p.runResponseStream(ctx, req, func(chunk *StreamChunk) error {
		switch chunk.Type {
		case ChunkTypeText:
			content.WriteString(chunk.Text)
		case ChunkTypeToolStart:
			if chunk.ToolCall != nil {
				tc := ensureToolCall(chunk.ToolCall.ID)
				if chunk.ToolCall.Name != "" {
					tc.Name = chunk.ToolCall.Name
				}
				if tc.Arguments == "" && chunk.ToolCall.ArgumentsDelta != "" {
					tc.Arguments = chunk.ToolCall.ArgumentsDelta
				}
			}
		case ChunkTypeToolDelta:
			if chunk.ToolCall != nil {
				tc := ensureToolCall(chunk.ToolCall.ID)
				if chunk.ToolCall.Name != "" {
					tc.Name = chunk.ToolCall.Name
				}
				tc.Arguments += chunk.ToolCall.ArgumentsDelta
			}
		case ChunkTypeToolEnd:
			if chunk.ToolCall != nil {
				tc := ensureToolCall(chunk.ToolCall.ID)
				if chunk.ToolCall.Name != "" {
					tc.Name = chunk.ToolCall.Name
				}
				// End events can carry finalized arguments. Prefer that canonical value.
				if chunk.ToolCall.ArgumentsDelta != "" {
					tc.Arguments = chunk.ToolCall.ArgumentsDelta
				}
			}
		case ChunkTypeEnd:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if completion != nil {
		return p.convertResponse(completion), nil
	}

	// Fallback path if stream ended without a terminal response payload.
	resp := &Response{
		Content:    content.String(),
		StopReason: streamStopReason,
		Usage:      streamUsage,
	}
	if resp.StopReason == "" {
		resp.StopReason = StopReasonEndTurn
	}
	model := ""
	if req != nil {
		model = normalizeOpenAIModel(req.Model)
	}
	resp.Model = model
	if resp.Model == "" {
		resp.Model = normalizeOpenAIModel(p.config.Model)
	}
	if len(toolCalls) > 0 {
		resp.ToolCalls = make([]ToolCall, 0, len(toolCallOrder))
		for _, id := range toolCallOrder {
			if tc, ok := toolCalls[id]; ok {
				resp.ToolCalls = append(resp.ToolCalls, *tc)
			}
		}
	}
	return resp, nil
}

func (p *OpenAIProvider) StreamWithHandler(ctx context.Context, req *Request, handler StreamHandler) error {
	_, _, _, err := p.runResponseStream(ctx, req, handler)
	return err
}

func (p *OpenAIProvider) runResponseStream(
	ctx context.Context,
	req *Request,
	handler StreamHandler,
) (*responses.Response, StopReason, Usage, error) {
	params := p.buildResponseParams(req)
	stream := p.client.Responses.NewStreaming(ctx, params)
	if stream != nil {
		defer stream.Close()
	}

	chunkIndex := 0
	emit := func(chunk *StreamChunk) error {
		if handler == nil {
			return nil
		}
		return handler(chunk)
	}

	if err := emit(&StreamChunk{
		Index:     chunkIndex,
		Type:      ChunkTypeStart,
		Timestamp: time.Now(),
	}); err != nil {
		return nil, StopReasonError, Usage{}, err
	}

	var toolCallBuilders = make(map[string]*ToolCallChunk)
	toolCallStarted := make(map[string]bool)
	toolCallEnded := make(map[string]bool)
	var responseStopReason StopReason
	var responseUsage Usage
	var completion *responses.Response

	emitToolStart := func(index int, call *ToolCallChunk) error {
		if call == nil || call.ID == "" || toolCallStarted[call.ID] {
			return nil
		}
		toolCallStarted[call.ID] = true
		toolCopy := *call
		return emit(&StreamChunk{
			Index:     index,
			Type:      ChunkTypeToolStart,
			ToolCall:  &toolCopy,
			Timestamp: time.Now(),
		})
	}
	emitToolEnd := func(index int, call *ToolCallChunk) error {
		if call == nil || call.ID == "" || toolCallEnded[call.ID] {
			return nil
		}
		toolCallEnded[call.ID] = true
		toolCopy := *call
		return emit(&StreamChunk{
			Index:     index,
			Type:      ChunkTypeToolEnd,
			ToolCall:  &toolCopy,
			Timestamp: time.Now(),
		})
	}

	for stream.Next() {
		event := stream.Current()
		chunkIndex++

		switch ev := event.AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			if ev.Delta == "" {
				continue
			}
			if err := emit(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeText,
				Text:      ev.Delta,
				Timestamp: time.Now(),
			}); err != nil {
				return nil, StopReasonError, Usage{}, err
			}

		case responses.ResponseFunctionCallArgumentsDeltaEvent:
			toolCall := toolCallBuilders[ev.ItemID]
			if toolCall == nil {
				toolCall = &ToolCallChunk{ID: ev.ItemID}
				toolCallBuilders[ev.ItemID] = toolCall
				if err := emitToolStart(chunkIndex, toolCall); err != nil {
					return nil, StopReasonError, Usage{}, err
				}
			}
			if ev.Delta != "" {
				if err := emit(&StreamChunk{
					Index: chunkIndex,
					Type:  ChunkTypeToolDelta,
					ToolCall: &ToolCallChunk{
						ID:             ev.ItemID,
						ArgumentsDelta: ev.Delta,
					},
					Timestamp: time.Now(),
				}); err != nil {
					return nil, StopReasonError, Usage{}, err
				}
			}

		case responses.ResponseFunctionCallArgumentsDoneEvent:
			toolCall := toolCallBuilders[ev.ItemID]
			if toolCall == nil {
				toolCall = &ToolCallChunk{ID: ev.ItemID}
				toolCallBuilders[ev.ItemID] = toolCall
				if err := emitToolStart(chunkIndex, toolCall); err != nil {
					return nil, StopReasonError, Usage{}, err
				}
			}
			if ev.Arguments != "" {
				toolCall.ArgumentsDelta = ev.Arguments
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
			if ev.Item.Name != "" {
				toolCall.Name = ev.Item.Name
			}
			if err := emitToolStart(chunkIndex, toolCall); err != nil {
				return nil, StopReasonError, Usage{}, err
			}

		case responses.ResponseOutputItemDoneEvent:
			if ev.Item.Type != "function_call" {
				continue
			}
			toolCall := toolCallBuilders[ev.Item.ID]
			if toolCall == nil {
				toolCall = &ToolCallChunk{ID: ev.Item.ID}
				toolCallBuilders[ev.Item.ID] = toolCall
			}
			if ev.Item.Name != "" {
				toolCall.Name = ev.Item.Name
			}
			if ev.Item.Arguments != "" {
				toolCall.ArgumentsDelta = ev.Item.Arguments
			}
			if err := emitToolEnd(chunkIndex, toolCall); err != nil {
				return nil, StopReasonError, Usage{}, err
			}

		case responses.ResponseCompletedEvent:
			completion = &ev.Response
			responseUsage = p.convertResponseUsage(ev.Response)
			responseStopReason = p.convertResponseStopReason(ev.Response)

		case responses.ResponseFailedEvent:
			completion = &ev.Response
			responseUsage = p.convertResponseUsage(ev.Response)
			responseStopReason = StopReasonError

		case responses.ResponseIncompleteEvent:
			completion = &ev.Response
			responseUsage = p.convertResponseUsage(ev.Response)
			responseStopReason = p.convertIncompleteReason(ev.Response.IncompleteDetails.Reason)

		case responses.ResponseErrorEvent:
			if err := emit(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeError,
				Text:      ev.Message,
				Timestamp: time.Now(),
			}); err != nil {
				return nil, StopReasonError, Usage{}, err
			}
			return nil, StopReasonError, responseUsage, fmt.Errorf("openai stream: %s", ev.Message)
		}
	}

	if err := stream.Err(); err != nil {
		_ = emit(&StreamChunk{
			Index:     chunkIndex + 1,
			Type:      ChunkTypeError,
			Text:      err.Error(),
			Timestamp: time.Now(),
		})
		return nil, StopReasonError, responseUsage, fmt.Errorf("openai stream: %w", err)
	}

	if responseStopReason == "" {
		responseStopReason = StopReasonEndTurn
	}

	finalIndex := chunkIndex
	for _, toolCall := range toolCallBuilders {
		if toolCall == nil {
			continue
		}
		finalIndex++
		if err := emitToolEnd(finalIndex, toolCall); err != nil {
			return nil, StopReasonError, Usage{}, err
		}
	}

	if err := emit(&StreamChunk{
		Index:      finalIndex + 1,
		Type:       ChunkTypeEnd,
		StopReason: responseStopReason,
		Usage:      &responseUsage,
		Timestamp:  time.Now(),
	}); err != nil {
		return nil, StopReasonError, Usage{}, err
	}

	return completion, responseStopReason, responseUsage, nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
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
func (p *OpenAIProvider) ValidateConfig() error {
	return p.config.Validate()
}

// SupportsModel checks if the provider supports the given model
func (p *OpenAIProvider) SupportsModel(model string) bool {
	model = normalizeOpenAIModel(model)
	if _, ok := openaiModelCatalog[model]; ok {
		return true
	}
	return p.config.AllowUnknownModels
}

// DefaultModel returns the provider's default model
func (p *OpenAIProvider) DefaultModel() string {
	return p.config.Model
}

// Close cleans up any resources
func (p *OpenAIProvider) Close() error {
	return nil
}

func (p *OpenAIProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	return p.Generate(ctx, req)
}

func (p *OpenAIProvider) SupportedModels() []ModelInfo {
	return []ModelInfo{
		openaiModelCatalog["gpt-5.3-codex"],
		openaiModelCatalog["gpt-5.2-codex"],
	}
}

func (p *OpenAIProvider) CountTokens(messages []Message) (int, error) {
	count := 0
	for _, msg := range messages {
		count += len(msg.Content) / 4
	}
	return count, nil
}

func (p *OpenAIProvider) MaxContextTokens(model string) int {
	model = normalizeOpenAIModel(model)
	if info, ok := openaiModelCatalog[model]; ok {
		return info.MaxContext
	}
	return 200000
}

func (p *OpenAIProvider) HealthCheck(ctx context.Context) error {
	return nil
}

// buildParams constructs OpenAI API parameters from a Request
func (p *OpenAIProvider) buildResponseParams(req *Request) responses.ResponseNewParams {
	if req == nil {
		req = &Request{}
	}

	model := normalizeOpenAIModel(req.Model)
	if model == "" {
		model = normalizeOpenAIModel(p.config.Model)
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.config.MaxTokens
	}

	systemPrompt := strings.TrimSpace(req.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = strings.TrimSpace(p.config.SystemPrompt)
	}

	messages := p.convertResponseMessages(req.Messages, systemPrompt)

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: messages,
		},
	}
	if p.config.AuthMode != openAIAuthModeChatGPT {
		params.MaxOutputTokens = openai.Int(int64(maxTokens))
	}

	if p.config.AuthMode == openAIAuthModeChatGPT {
		instructions := systemPrompt
		if instructions == "" {
			instructions = "You are Codex, an AI coding agent."
		}
		params.Instructions = openai.String(instructions)
		params.Store = openai.Bool(false)
	}

	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	} else if p.config.Temperature > 0 && modelSupportsTemperature(model) {
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

func normalizeOpenAIModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ""
	}
	if canonical, ok := openaiModelAliases[model]; ok {
		return canonical
	}
	return model
}

func modelSupportsTemperature(model string) bool {
	model = normalizeOpenAIModel(model)
	return !(strings.HasPrefix(model, "gpt-5") && strings.Contains(model, "codex"))
}

func (p *OpenAIProvider) selectFallbackModel(requestedModel string, err error) (string, bool) {
	if !isOpenAIModelUnavailableError(err) {
		return "", false
	}
	fallback := normalizeOpenAIModel(p.config.FallbackModel)
	requestedModel = normalizeOpenAIModel(requestedModel)
	if fallback == "" || fallback == requestedModel {
		return "", false
	}
	if !p.SupportsModel(fallback) {
		return "", false
	}
	return fallback, true
}

func isOpenAIModelUnavailableError(err error) bool {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Code == "model_not_found" {
		return true
	}

	msg := strings.ToLower(apiErr.Message)
	switch apiErr.StatusCode {
	case 404:
		return strings.Contains(msg, "model")
	case 403:
		return strings.Contains(msg, "access") && strings.Contains(msg, "model")
	default:
		return false
	}
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
