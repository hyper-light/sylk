package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/adalundhe/sylk/skills"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
)

// AnthropicProvider implements Provider for Anthropic's Claude models
type AnthropicProvider struct {
	client      *anthropic.Client
	clientMu    sync.RWMutex
	config      AnthropicConfig
	skills      []skills.Skill
	authService oauth.AnthropicAuthService
}

type AnthropicModel string

const (
	Opus46            AnthropicModel = "claude-opus-4-6"
	Opus45            AnthropicModel = "claude-opus-4-5-20251101"
	SonnetLongContext AnthropicModel = "claude-sonnet-4-6"
	Haiku             AnthropicModel = "claude-haiku-4-5-20251001"
)

// Supported Anthropic models
var anthropicModels = map[string]bool{
	"claude-opus-4-6": true, // Claude Opus 4.6
	// Claude 4.5 family
	"claude-opus-4-5-20251101":  true, // Claude Opus 4.5
	"claude-sonnet-4-6":         true, // Claude Sonnet 4.6
	"claude-haiku-4-5-20251001": true, // Claude Haiku 4.5
}

// NewAnthropicProvider creates a new Anthropic provider with the given configuration
func NewAnthropicProvider(config AnthropicConfig, skills ...skills.Skill) (*AnthropicProvider, error) {
	return NewAnthropicProviderWithAuthService(
		config,
		oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{}),
		skills...,
	)
}

// NewAnthropicProviderWithAuthService creates a provider using a custom Anthropic OAuth service.
func NewAnthropicProviderWithAuthService(
	config AnthropicConfig,
	authService oauth.AnthropicAuthService,
	skills ...skills.Skill,
) (*AnthropicProvider, error) {
	applyAnthropicProviderDefaults(&config)
	if err := hydrateAnthropicConfig(context.Background(), &config, authService); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	client := anthropic.NewClient(buildAnthropicClientOptions(config)...)
	return &AnthropicProvider{
		client:      &client,
		config:      config,
		skills:      skills,
		authService: authService,
	}, nil
}

func applyAnthropicProviderDefaults(config *AnthropicConfig) {
	if config == nil {
		return
	}
	defaults := DefaultAnthropicConfig()
	applyDefaultString(&config.Model, defaults.Model)
	applyDefaultInt(&config.MaxTokens, defaults.MaxTokens)
	applyDefaultString(&config.AuthMode, defaults.AuthMode)
}

func hydrateAnthropicConfig(
	ctx context.Context,
	config *AnthropicConfig,
	authService oauth.AnthropicAuthService,
) error {
	if config == nil {
		return fmt.Errorf("anthropic config is nil")
	}
	normalizeAnthropicHydrationFields(config)
	if config.AuthMode == AnthropicAuthModeOAuth {
		return hydrateAnthropicOAuthConfig(ctx, config, authService)
	}
	return hydrateAnthropicAPIKeyConfig(config)
}

func normalizeAnthropicHydrationFields(config *AnthropicConfig) {
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.AuthMode = strings.TrimSpace(config.AuthMode)
	if config.AuthMode == "" {
		config.AuthMode = AnthropicAuthModeAPIKey
	}
}

func hydrateAnthropicAPIKeyConfig(config *AnthropicConfig) error {
	if config == nil {
		return nil
	}
	if config.APIKey != "" {
		return nil
	}
	config.APIKey = ResolveAnthropicAPIKey("")
	return nil
}

func hydrateAnthropicOAuthConfig(
	ctx context.Context,
	config *AnthropicConfig,
	authService oauth.AnthropicAuthService,
) error {
	if config == nil {
		return fmt.Errorf("anthropic config is nil")
	}
	if config.APIKey != "" {
		return nil
	}
	if authService == nil {
		return fmt.Errorf("anthropic oauth auth service is not configured")
	}
	auth, err := authService.Resolve(ctx)
	if err == nil {
		applyResolvedAnthropicOAuth(config, auth)
		return validateAnthropicOAuthConfig(config)
	}
	if fallbackErr := hydrateAnthropicAPIKeyConfig(config); fallbackErr != nil {
		return fallbackErr
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("resolve anthropic oauth: %w", err)
	}
	config.AuthMode = AnthropicAuthModeAPIKey
	return nil
}

func applyResolvedAnthropicOAuth(config *AnthropicConfig, auth *oauth.AnthropicOAuthAuth) {
	if config == nil || auth == nil {
		return
	}
	if config.APIKey == "" {
		config.APIKey = strings.TrimSpace(auth.AccessToken)
	}
}

func validateAnthropicOAuthConfig(config *AnthropicConfig) error {
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("anthropic oauth auth requires access token")
	}
	return nil
}

func buildAnthropicClientOptions(config AnthropicConfig) []option.RequestOption {
	opts := []option.RequestOption{
		buildAnthropicAuthOption(config),
	}
	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}
	betaOpts := []string{
		string(anthropic.AnthropicBetaInterleavedThinking2025_05_14),
		"fine-grained-tool-streaming-2025-05-14",
		"claude-code-20250219",
	}
	if strings.TrimSpace(config.AuthMode) == AnthropicAuthModeOAuth {
		betaOpts = append(betaOpts, "oauth-2025-04-20")
		opts = append(opts, option.WithHeader("User-Agent", "claude-cli/2.1.2 (external, cli)"))
	}
	if isAnthropicLongContextModel(config.Model) {
		betaOpts = append(betaOpts, string(anthropic.AnthropicBetaContext1m2025_08_07))
	}
	if config.EnableCaching {
		betaOpts = append(betaOpts, string(anthropic.AnthropicBetaPromptCaching2024_07_31))
		if config.PromptCacheTTL >= time.Hour {
			betaOpts = append(betaOpts, string(anthropic.AnthropicBetaExtendedCacheTTL2025_04_11))
		}
	}
	return append(opts, option.WithHeader("anthropic-beta", strings.Join(betaOpts, ",")))
}

func buildAnthropicAuthOption(config AnthropicConfig) option.RequestOption {
	if strings.TrimSpace(config.AuthMode) == AnthropicAuthModeOAuth {
		return option.WithAuthToken(config.APIKey)
	}
	return option.WithAPIKey(config.APIKey)
}

func isAnthropicLongContextModel(model string) bool {
	return strings.TrimSpace(model) == string(SonnetLongContext)
}

// ResolveAnthropicAPIKey resolves the Anthropic API key from config, env, secure store, or llm provider.
func ResolveAnthropicAPIKey(configured string) string {
	if key := strings.TrimSpace(configured); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		return key
	}
	if key := resolveAnthropicSecureKey(); key != "" {
		return key
	}
	key, err := llm.ResolveAPIKey("anthropic")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func resolveAnthropicSecureKey() string {
	dirs, err := storage.ResolveDirs()
	if err != nil || dirs == nil {
		return ""
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return ""
	}
	key, err := manager.GetAPIKey("anthropic")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func (p *AnthropicProvider) getClient() *anthropic.Client {
	p.clientMu.RLock()
	defer p.clientMu.RUnlock()
	return p.client
}

func (p *AnthropicProvider) applyRefreshedAnthropicOAuthAuth(auth *oauth.AnthropicOAuthAuth) {
	if auth == nil {
		return
	}
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	p.config.APIKey = strings.TrimSpace(auth.AccessToken)
	client := anthropic.NewClient(buildAnthropicClientOptions(p.config)...)
	p.client = &client
}

func (p *AnthropicProvider) refreshAnthropicOAuthAuth(ctx context.Context) error {
	if p.authService == nil {
		return fmt.Errorf("anthropic oauth auth service is not configured")
	}
	auth, err := p.authService.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve anthropic oauth: %w", err)
	}
	updated, err := p.authService.Refresh(ctx, auth)
	if err != nil {
		return fmt.Errorf("refresh anthropic oauth: %w", err)
	}
	if err := p.authService.Save(ctx, updated); err != nil {
		return fmt.Errorf("persist refreshed anthropic oauth: %w", err)
	}
	p.applyRefreshedAnthropicOAuthAuth(updated)
	return nil
}

func (p *AnthropicProvider) shouldRetryForAnthropicAuth(err error) bool {
	if err == nil {
		return false
	}
	if strings.TrimSpace(p.config.AuthMode) != AnthropicAuthModeOAuth {
		return false
	}
	return isAnthropicOAuthAuthError(err)
}

func isAnthropicOAuthAuthError(err error) bool {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden
}

// Name returns the provider identifier
func (p *AnthropicProvider) Name() string {
	return string(ProviderTypeAnthropic)
}

// Generate performs a non-streaming completion request. Auth retry wraps the
// full transient retry loop so that at most one refresh occurs per call.
func (p *AnthropicProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	resp, err := p.generateWithRetry(ctx, req)
	if !p.shouldRetryForAnthropicAuth(err) {
		return resp, err
	}
	if refreshErr := p.refreshAnthropicOAuthAuth(ctx); refreshErr != nil {
		return nil, refreshErr
	}
	return p.generateWithRetry(ctx, req)
}

func (p *AnthropicProvider) generateWithRetry(ctx context.Context, req *Request) (*Response, error) {
	return retryGenerate(ctx, p.config.BaseConfig, func(ctx context.Context) (*Response, error) {
		return p.generateOnce(ctx, req)
	})
}

func (p *AnthropicProvider) generateOnce(ctx context.Context, req *Request) (*Response, error) {
	params := p.buildParams(req)
	msg, err := p.getClient().Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic generate: %w", err)
	}
	return p.convertResponse(msg), nil
}

// StreamWithHandler streams a completion. Auth retry wraps the full transient
// retry loop so that at most one refresh occurs per call.
func (p *AnthropicProvider) StreamWithHandler(ctx context.Context, req *Request, handler StreamHandler) error {
	err := p.streamWithRetry(ctx, req, handler)
	if !p.shouldRetryForAnthropicAuth(err) {
		return err
	}
	if refreshErr := p.refreshAnthropicOAuthAuth(ctx); refreshErr != nil {
		return refreshErr
	}
	return p.streamWithRetry(ctx, req, handler)
}

func (p *AnthropicProvider) streamWithRetry(ctx context.Context, req *Request, handler StreamHandler) error {
	return retryStream(ctx, p.config.BaseConfig, func(ctx context.Context) error {
		return p.streamWithHandlerOnce(ctx, req, handler)
	})
}

func (p *AnthropicProvider) streamWithHandlerOnce(ctx context.Context, req *Request, handler StreamHandler) error {
	params := p.buildParams(req)
	stream := p.getClient().Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var chunkIndex int
	var inputTokens, outputTokens int
	var cacheReadTokens, cacheWriteTokens int
	var stopReason StopReason
	var startSent bool
	toolCallIDForIndex := map[int64]string{}

	for stream.Next() {
		event := stream.Current()
		chunkIndex++

		switch ev := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			if ev.Message.Usage.InputTokens > 0 {
				inputTokens = int(ev.Message.Usage.InputTokens)
			}
			if !startSent {
				startSent = true
				startChunk := &StreamChunk{
					Index:     0,
					Type:      ChunkTypeStart,
					Timestamp: time.Now(),
				}
				if inputTokens > 0 {
					startChunk.Usage = &Usage{InputTokens: inputTokens}
				}
				if err := handler(startChunk); err != nil {
					return err
				}
			}
			continue
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
			continue
		}

		chunk := p.convertStreamEvent(event, chunkIndex, toolCallIDForIndex)
		if chunk != nil {
			if !startSent {
				startSent = true
				if err := handler(&StreamChunk{Index: 0, Type: ChunkTypeStart, Timestamp: time.Now()}); err != nil {
					return err
				}
			}
			if err := handler(chunk); err != nil {
				return err
			}
		}
	}

	if err := stream.Err(); err != nil {
		if p.shouldRetryForAnthropicAuth(err) {
			return err
		}
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
	return streamViaHandler(ctx, p, req), nil
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
		{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", MaxContext: 200000},
		{ID: "claude-opus-4-5-20251101", Name: "Claude Opus 4.5", MaxContext: 200000},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", MaxContext: 1000000},
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
	if isAnthropicLongContextModel(model) {
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

	systemPrompt := resolveSystemPrompt(req.SystemPrompt, p.config.SystemPrompt)
	if len(p.skills) > 0 {
		systemPrompt = systemPrompt + "\n" + skills.ToPrompt(p.skills)
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(maxTokens),
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: p.convertMessages(req.Messages),
		Tools:    p.convertTools(req.Tools),
	}

	if req.TopP != nil {
		params.TopP = anthropic.Float(*req.TopP)
	}

	if len(req.StopSequences) > 0 {
		params.StopSequences = req.StopSequences
	}

	params.Thinking = p.resolveThinkingConfig(req.ThinkingBudget, maxTokens)

	if p.config.EnableCaching {
		cacheControl := anthropic.NewCacheControlEphemeralParam()
		cacheControl.TTL = anthropicCacheTTL(p.config.PromptCacheTTL)
		params.CacheControl = cacheControl
	}

	return params
}

// resolveSystemPrompt returns the system prompt to use. Request-level takes
// precedence over config-level.
func resolveSystemPrompt(requestPrompt string, configPrompt string) string {
	if trimmed := strings.TrimSpace(requestPrompt); trimmed != "" {
		return trimmed
	}
	return configPrompt
}

// resolveThinkingConfig returns the thinking configuration for a request.
// Adaptive thinking takes precedence over budget-based thinking.
func (p *AnthropicProvider) resolveThinkingConfig(requestBudget int, maxTokens int) anthropic.ThinkingConfigParamUnion {
	if p.config.AdaptiveThinking {
		adaptive := anthropic.NewThinkingConfigAdaptiveParam()
		return anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
	}
	budget := resolveThinkingBudget(requestBudget, p.config.ThinkingBudget)
	if budget <= 0 {
		return anthropic.ThinkingConfigParamUnion{}
	}
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	return anthropic.ThinkingConfigParamOfEnabled(int64(budget))
}

// resolveThinkingBudget returns the thinking budget to use. Request-level takes
// precedence over config-level. Returns 0 to disable thinking.
func resolveThinkingBudget(requestBudget int, configBudget int) int {
	if requestBudget > 0 {
		return requestBudget
	}
	return configBudget
}

// anthropicCacheTTL converts a duration to the Anthropic cache TTL enum value.
func anthropicCacheTTL(ttl time.Duration) anthropic.CacheControlEphemeralTTL {
	if ttl >= time.Hour {
		return anthropic.CacheControlEphemeralTTLTTL1h
	}
	return anthropic.CacheControlEphemeralTTLTTL5m
}

// convertMessages converts generic messages to Anthropic format.
// Filters empty messages, normalizes tool call IDs, and merges
// consecutive same-role messages to satisfy API constraints.
func (p *AnthropicProvider) convertMessages(messages []Message) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			result = append(result, anthropic.NewUserMessage(
				anthropic.NewTextBlock(msg.Content),
			))

		case RoleAssistant:
			if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
				continue
			}
			if len(msg.ToolCalls) > 0 {
				blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.ToolCalls)+1)
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					blocks = append(blocks, anthropic.ContentBlockParamUnion{
						OfToolUse: &anthropic.ToolUseBlockParam{
							ID:    normalizeToolCallID(tc.ID),
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
				anthropic.NewToolResultBlock(normalizeToolCallID(msg.ToolCallID), msg.Content, false),
			))
		}
	}

	return normalizeAnthropicMessages(result)
}

// normalizeToolCallID ensures a tool call ID conforms to ^[a-zA-Z0-9_-]{1,64}$.
// Replaces non-conforming characters with '_' and truncates to 64 characters.
// Generates a short UUID fallback if the result would be empty.
func normalizeToolCallID(id string) string {
	var b strings.Builder
	b.Grow(min(len(id), 64))
	for _, r := range id {
		if b.Len() >= 64 {
			break
		}
		if isToolIDChar(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return generateShortID()
	}
	return b.String()
}

// isToolIDChar returns true if r is valid in an Anthropic tool call ID.
func isToolIDChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-'
}

// generateShortID returns an 8-character UUID prefix as a fallback ID.
func generateShortID() string {
	return uuid.NewString()[:8]
}

// normalizeAnthropicMessages merges consecutive same-role messages.
// Anthropic requires strict user/assistant alternation.
func normalizeAnthropicMessages(messages []anthropic.MessageParam) []anthropic.MessageParam {
	if len(messages) <= 1 {
		return messages
	}
	result := make([]anthropic.MessageParam, 0, len(messages))
	result = append(result, messages[0])
	for i := 1; i < len(messages); i++ {
		last := &result[len(result)-1]
		if last.Role == messages[i].Role {
			mergeAnthropicMessage(last, messages[i])
		} else {
			result = append(result, messages[i])
		}
	}
	return result
}

// mergeAnthropicMessage appends src's content blocks into dst.
func mergeAnthropicMessage(dst *anthropic.MessageParam, src anthropic.MessageParam) {
	dst.Content = append(dst.Content, src.Content...)
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
		case anthropic.ThinkingDelta:
			return &StreamChunk{
				Index:     index,
				Type:      ChunkTypeThought,
				Text:      delta.Thinking,
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
