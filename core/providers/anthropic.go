package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/logging"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/adalundhe/sylk/skills"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
)

var (
	providerDebugLog     *slog.Logger
	providerDebugLogOnce sync.Once
)

func providerFileLog() *slog.Logger {
	providerDebugLogOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		_ = os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "ui_events.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			providerDebugLog = slog.Default()
			return
		}
		providerDebugLog = slog.New(slog.NewTextHandler(f, logging.HandlerOptions(slog.LevelInfo)))
	})
	return providerDebugLog
}

// AnthropicProvider implements Provider for Anthropic's Claude models
type AnthropicProvider struct {
	client      *anthropic.Client
	clientMu    sync.RWMutex
	config      AnthropicConfig
	skills      []skills.Skill
	authService oauth.AnthropicAuthService
}

func (p *AnthropicProvider) SupportsNativeWebSearchEvidence() bool {
	return p != nil
}

type AnthropicModel string

const (
	Opus46                       AnthropicModel = "claude-opus-4-6"
	SonnetLongContext            AnthropicModel = "claude-sonnet-4-6"
	Haiku                        AnthropicModel = "claude-haiku-4-5-20251001"
	minAnthropicThinkingBudget                  = 1024
	anthropicOAuthToolPrefix                    = "mcp_"
	anthropicOAuthSystemShim                    = "You are Claude Code, Anthropic's official CLI for Claude."
	anthropicOAuthSystemBoundary                = "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__"
)

// Supported Anthropic models
var anthropicModels = map[string]bool{
	"claude-opus-4-6":           true, // Claude Opus 4.6
	"claude-sonnet-4-6":         true, // Claude Sonnet 4.6
	"claude-haiku-4-5-20251001": true, // Claude Haiku 4.5
}

// NewAnthropicProvider creates a new Anthropic provider with the given configuration.
func NewAnthropicProvider(ctx context.Context, config AnthropicConfig, skills ...skills.Skill) (*AnthropicProvider, error) {
	return NewAnthropicProviderWithAuthService(
		ctx,
		config,
		oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{}),
		skills...,
	)
}

// NewAnthropicProviderWithAuthService creates a provider using a custom Anthropic OAuth service.
func NewAnthropicProviderWithAuthService(
	ctx context.Context,
	config AnthropicConfig,
	authService oauth.AnthropicAuthService,
	skills ...skills.Skill,
) (*AnthropicProvider, error) {
	applyAnthropicProviderDefaults(&config)
	if err := hydrateAnthropicConfig(ctx, &config, authService); err != nil {
		return nil, err
	}
	providerFileLog().Info("DEBUG: anthropic_provider_validate_start")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	providerFileLog().Info("DEBUG: anthropic_provider_new_client_start")
	client := anthropic.NewClient(buildAnthropicClientOptions(config)...)
	providerFileLog().Info("DEBUG: anthropic_provider_new_client_done")
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
	providerFileLog().Info("DEBUG: hydrate_anthropic_start",
		"auth_mode", config.AuthMode,
		"has_api_key", config.APIKey != "",
		"model", config.Model)
	hydrateStart := time.Now()
	var err error
	if config.AuthMode == AnthropicAuthModeOAuth {
		err = hydrateAnthropicOAuthConfig(ctx, config, authService)
	} else {
		err = hydrateAnthropicAPIKeyConfig(config)
	}
	providerFileLog().Info("DEBUG: hydrate_anthropic_done",
		"auth_mode", config.AuthMode,
		"elapsed_ms", time.Since(hydrateStart).Milliseconds(),
		"error", err)
	return err
}

func normalizeAnthropicHydrationFields(config *AnthropicConfig) {
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.AuthMode = normalizeAnthropicAuthModeValue(config.AuthMode)
	if config.AuthMode == "" {
		if config.APIKey != "" {
			config.AuthMode = AnthropicAuthModeAPIKey
		} else {
			config.AuthMode = ResolveAnthropicAuthMode("")
		}
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

func (p *AnthropicProvider) usesAnthropicOAuth() bool {
	return strings.TrimSpace(p.config.AuthMode) == AnthropicAuthModeOAuth
}

func (p *AnthropicProvider) anthropicRequestOptions() []option.RequestOption {
	if !p.usesAnthropicOAuth() {
		return nil
	}
	return []option.RequestOption{option.WithQuery("beta", "true")}
}

func anthropicOAuthWireToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, anthropicOAuthToolPrefix) {
		return trimmed
	}
	return anthropicOAuthToolPrefix + trimmed
}

func anthropicOAuthCanonicalToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	return strings.TrimPrefix(trimmed, anthropicOAuthToolPrefix)
}

func (p *AnthropicProvider) outboundAnthropicToolName(name string) string {
	if p.usesAnthropicOAuth() {
		return anthropicOAuthWireToolName(name)
	}
	return name
}

func (p *AnthropicProvider) inboundAnthropicToolName(name string) string {
	if p.usesAnthropicOAuth() {
		return anthropicOAuthCanonicalToolName(name)
	}
	return name
}

func buildAnthropicClientOptions(config AnthropicConfig) []option.RequestOption {
	opts := []option.RequestOption{
		buildAnthropicAuthOption(config),
		// Provider-level retry logic is the single retry authority so we can
		// enforce 500-only retries and emit accurate retry telemetry.
		option.WithMaxRetries(0),
	}
	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}
	if config.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(config.HTTPClient))
	}
	betaOpts := []string{
		string(anthropic.AnthropicBetaInterleavedThinking2025_05_14),
		"fine-grained-tool-streaming-2025-05-14",
		"claude-code-20250219",
	}
	if strings.TrimSpace(config.AuthMode) == AnthropicAuthModeOAuth {
		betaOpts = []string{
			"oauth-2025-04-20",
			string(anthropic.AnthropicBetaInterleavedThinking2025_05_14),
			"fine-grained-tool-streaming-2025-05-14",
		}
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

func normalizeAnthropicAuthModeValue(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return ""
	case "apikey":
		return AnthropicAuthModeAPIKey
	case AnthropicAuthModeAPIKey:
		return AnthropicAuthModeAPIKey
	case AnthropicAuthModeOAuth:
		return AnthropicAuthModeOAuth
	default:
		return strings.TrimSpace(mode)
	}
}

func resolveAnthropicAuthMode(configured, pref string, hasAPIKey, hasOAuth bool) string {
	if mode := normalizeAnthropicAuthModeValue(configured); mode != "" {
		return mode
	}
	if mode := normalizeAnthropicAuthModeValue(pref); mode != "" {
		return mode
	}
	if hasAPIKey {
		return AnthropicAuthModeAPIKey
	}
	if hasOAuth {
		return AnthropicAuthModeOAuth
	}
	return AnthropicAuthModeAPIKey
}

// ResolveAnthropicAuthMode resolves the effective Anthropic auth mode from
// explicit config, stored auth preference, and available credential state.
func ResolveAnthropicAuthMode(configured string) string {
	return resolveAnthropicAuthMode(
		configured,
		"",
		ResolveAnthropicAPIKey("") != "",
		hasStoredAnthropicOAuthAuth(context.Background()),
	)
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

func hasStoredAnthropicOAuthAuth(ctx context.Context) bool {
	auth, err := oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{}).Load(ctx)
	if err != nil || auth == nil {
		return false
	}
	return strings.TrimSpace(auth.AccessToken) != ""
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
	return retryAnthropicInternalServerGenerate(ctx, p.config.BaseConfig, func(ctx context.Context) (*Response, error) {
		return p.generateOnce(ctx, req)
	})
}

func (p *AnthropicProvider) generateOnce(ctx context.Context, req *Request) (*Response, error) {
	params := p.buildParams(req)
	msg, err := p.getClient().Messages.New(ctx, params, p.anthropicRequestOptions()...)
	if err != nil {
		return nil, fmt.Errorf("anthropic generate: %w", err)
	}
	return p.convertResponse(msg), nil
}

// StreamWithHandler streams a completion. Auth retry wraps the full transient
// retry loop so that at most one refresh occurs per call.
func (p *AnthropicProvider) StreamWithHandler(ctx context.Context, req *Request, handler StreamHandler) error {
	wrapped := retryAwareHandler(handler)
	err := p.streamWithRetry(ctx, req, wrapped)
	if !p.shouldRetryForAnthropicAuth(err) {
		return err
	}
	if refreshErr := p.refreshAnthropicOAuthAuth(ctx); refreshErr != nil {
		return refreshErr
	}
	return p.streamWithRetry(ctx, req, wrapped)
}

func (p *AnthropicProvider) streamWithRetry(ctx context.Context, req *Request, handler StreamHandler) error {
	return retryAnthropicInternalServerStream(ctx, p.config.BaseConfig, func(ctx context.Context) error {
		return p.streamWithHandlerOnce(ctx, req, handler)
	})
}

func (p *AnthropicProvider) streamWithHandlerOnce(ctx context.Context, req *Request, handler StreamHandler) error {
	providerFileLog().Info("anthropic_stream: BUILD_PARAMS",
		"model", p.config.Model,
		"req_max_tokens", req.MaxTokens,
		"req_thinking_budget", req.ThinkingBudget,
		"req_tools_count", len(req.Tools),
		"req_messages_count", len(req.Messages),
		"adaptive_thinking", p.config.AdaptiveThinking,
		"config_thinking_budget", p.config.ThinkingBudget,
		"config_max_tokens", p.config.MaxTokens,
		"system_prompt_len", len(req.SystemPrompt))

	params := p.buildParams(req)

	providerFileLog().Info("anthropic_stream: PARAMS_BUILT",
		"param_max_tokens", params.MaxTokens,
		"param_model", string(params.Model),
		"param_tools_count", len(params.Tools))

	stream := p.getClient().Messages.NewStreaming(ctx, params, p.anthropicRequestOptions()...)
	defer stream.Close()

	streamStart := time.Now()
	var chunkIndex int
	var inputTokens, outputTokens int
	var cacheReadTokens, cacheWriteTokens int
	var stopReason StopReason
	var startSent bool
	toolCallIDForIndex := map[int64]string{}

	// Track all content blocks for multi-turn replay. Anthropic requires
	// that thinking blocks (with opaque signatures) be preserved verbatim
	// in assistant messages when extended thinking is enabled.
	contentBlockTracker := newContentBlockTracker()

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

		contentBlockTracker.observe(event)

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
		providerFileLog().Info("anthropic_stream: STREAM_ERROR",
			"err", err.Error(),
			"elapsed", time.Since(streamStart).String(),
			"chunk_count", chunkIndex)
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

	providerFileLog().Info("anthropic_stream: STREAM_COMPLETE",
		"elapsed", time.Since(streamStart).String(),
		"chunk_count", chunkIndex,
		"stop_reason", string(stopReason),
		"input_tokens", inputTokens,
		"output_tokens", outputTokens,
		"total_tokens", inputTokens+outputTokens,
		"cache_read_tokens", cacheReadTokens,
		"cache_write_tokens", cacheWriteTokens,
		"tool_calls_seen", len(toolCallIDForIndex))

	var providerData map[string]any
	if raw := contentBlockTracker.build(p.inboundAnthropicToolName); len(raw) > 0 {
		providerData = map[string]any{anthropicRawContentKey: raw}
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
		ProviderData: providerData,
		Timestamp:    time.Now(),
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
	if len(p.skills) > 0 && len(req.Tools) == 0 && !req.SkipProviderSkills {
		// Inject skill descriptions into the system prompt only when proper
		// API tool definitions are absent and the caller hasn't suppressed
		// skills (e.g. classification requests need a focused prompt without
		// the full skill catalogue). When req.Tools is populated, the LLM
		// uses the native tool API; the <available_skills> XML block would
		// create a conflicting double signal that causes the model to emit
		// XML tool-call markup as text instead.
		systemPrompt = systemPrompt + "\n" + skills.ToPrompt(p.skills)
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(maxTokens),
		System:    p.oauthSystemBlocks(systemPrompt),
		Messages:  p.convertMessages(req.Messages, p.requestUsesPromptCache(req)),
		Tools:     p.convertTools(req.Tools, p.requestUsesPromptCache(req)),
	}

	applyAnthropicToolChoice(&params, req.ToolChoice, req.DisableParallelToolUse, p.outboundAnthropicToolName)

	params.Thinking = p.resolveThinkingConfig(req.ThinkingBudget, maxTokens)
	thinkingEnabled := isAnthropicThinkingEnabled(params.Thinking)

	// TopP and TopK are incompatible with extended thinking — the API
	// returns 400 if they are set alongside a thinking config.
	if !thinkingEnabled {
		if req.TopP != nil {
			params.TopP = anthropic.Float(*req.TopP)
		}
		if req.TopK != nil {
			params.TopK = anthropic.Int(int64(*req.TopK))
		}
	}

	if len(req.StopSequences) > 0 {
		params.StopSequences = req.StopSequences
	}

	// Log the resolved thinking configuration for debugging.
	thinkingDesc := "disabled"
	if p.config.AdaptiveThinking {
		thinkingDesc = "adaptive"
	} else if req.ThinkingBudget > 0 {
		thinkingDesc = fmt.Sprintf("fixed(%d)", req.ThinkingBudget)
	} else if p.config.ThinkingBudget > 0 {
		thinkingDesc = fmt.Sprintf("config(%d)", p.config.ThinkingBudget)
	}
	providerFileLog().Info("anthropic_build_params: THINKING_CONFIG",
		"thinking_mode", thinkingDesc,
		"req_thinking_budget", req.ThinkingBudget,
		"config_thinking_budget", p.config.ThinkingBudget,
		"adaptive_thinking", p.config.AdaptiveThinking,
		"max_tokens", maxTokens,
		"tools_count", len(req.Tools))

	if p.requestUsesPromptCache(req) {
		params.CacheControl = p.newCacheControl()
	}

	return params
}

// applyAnthropicToolChoice maps the provider-agnostic ToolChoice string to
// the Anthropic SDK's ToolChoiceUnionParam. Only applied when tools are present.
// When disableParallel is true, the DisableParallelToolUse flag is set on the
// chosen variant (auto, any, or tool).
func applyAnthropicToolChoice(
	params *anthropic.MessageNewParams,
	choice string,
	disableParallel bool,
	toolNameTransform func(string) string,
) {
	if len(params.Tools) == 0 {
		return
	}
	normalized := strings.TrimSpace(strings.ToLower(choice))
	switch normalized {
	case "auto":
		auto := &anthropic.ToolChoiceAutoParam{}
		if disableParallel {
			auto.DisableParallelToolUse = anthropic.Bool(true)
		}
		params.ToolChoice = anthropic.ToolChoiceUnionParam{OfAuto: auto}
	case "any", "required":
		any := &anthropic.ToolChoiceAnyParam{}
		if disableParallel {
			any.DisableParallelToolUse = anthropic.Bool(true)
		}
		params.ToolChoice = anthropic.ToolChoiceUnionParam{OfAny: any}
	case "none":
		params.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfNone: &anthropic.ToolChoiceNoneParam{},
		}
	case "":
		// Empty string — let the API default.
	default:
		// Treat any other value as a specific tool name.
		if toolNameTransform != nil {
			normalized = toolNameTransform(normalized)
		}
		tc := anthropic.ToolChoiceParamOfTool(normalized)
		if disableParallel {
			tc.OfTool.DisableParallelToolUse = anthropic.Bool(true)
		}
		params.ToolChoice = tc
	}
}

// resolveSystemPrompt returns the system prompt to use. Request-level takes
// precedence over config-level.
func resolveSystemPrompt(requestPrompt string, configPrompt string) string {
	if trimmed := strings.TrimSpace(requestPrompt); trimmed != "" {
		return trimmed
	}
	return configPrompt
}

func (p *AnthropicProvider) oauthSystemBlocks(systemPrompt string) []anthropic.TextBlockParam {
	if !p.usesAnthropicOAuth() {
		return []anthropic.TextBlockParam{{Text: systemPrompt}}
	}
	blocks := []anthropic.TextBlockParam{{Text: anthropicOAuthSystemShim}}
	dynamic := oauthSystemDynamicPrompt(systemPrompt)
	if dynamic == "" {
		return blocks
	}
	return append(blocks,
		anthropic.TextBlockParam{Text: anthropicOAuthSystemBoundary},
		anthropic.TextBlockParam{Text: dynamic},
	)
}

func oauthSystemDynamicPrompt(systemPrompt string) string {
	sanitized := strings.ReplaceAll(systemPrompt, "OpenCode", "Claude Code")
	sanitized = strings.ReplaceAll(sanitized, "opencode", "claude code")
	trimmed := strings.TrimSpace(sanitized)
	if trimmed == "" {
		return ""
	}
	if trimmed == anthropicOAuthSystemShim {
		return ""
	}
	if strings.HasPrefix(trimmed, anthropicOAuthSystemShim) {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, anthropicOAuthSystemShim))
	}
	return trimmed
}

// resolveThinkingConfig returns the thinking configuration for a request.
// Adaptive thinking takes precedence over budget-based thinking.
func (p *AnthropicProvider) resolveThinkingConfig(requestBudget int, maxTokens int) anthropic.ThinkingConfigParamUnion {
	if p.config.AdaptiveThinking {
		adaptive := anthropic.NewThinkingConfigAdaptiveParam()
		return anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
	}
	budget := normalizeAnthropicThinkingBudget(resolveThinkingBudget(requestBudget, p.config.ThinkingBudget), maxTokens)
	if budget <= 0 {
		return anthropic.ThinkingConfigParamUnion{}
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

func normalizeAnthropicThinkingBudget(budget int, maxTokens int) int {
	if budget <= 0 {
		return 0
	}
	if maxTokens <= minAnthropicThinkingBudget {
		return 0
	}
	if budget < minAnthropicThinkingBudget {
		budget = minAnthropicThinkingBudget
	}
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	if budget < minAnthropicThinkingBudget {
		return 0
	}
	return budget
}

// isAnthropicThinkingEnabled returns true when the thinking config union has
// an active variant (either adaptive or enabled with a budget).
func isAnthropicThinkingEnabled(cfg anthropic.ThinkingConfigParamUnion) bool {
	return cfg.OfAdaptive != nil || cfg.OfEnabled != nil
}

// newCacheControl returns a CacheControlEphemeralParam with the TTL derived
// from the provider config. All cache breakpoints (top-level, per-tool,
// per-block) must use the same TTL or the API returns 400.
func (p *AnthropicProvider) newCacheControl() anthropic.CacheControlEphemeralParam {
	cc := anthropic.NewCacheControlEphemeralParam()
	cc.TTL = anthropicCacheTTL(p.config.PromptCacheTTL)
	return cc
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
// When prompt caching is enabled, a cache control breakpoint is set on
// the last user text block so conversational context is cached.
func (p *AnthropicProvider) requestUsesPromptCache(req *Request) bool {
	if req != nil && req.UsePromptCache != nil {
		return *req.UsePromptCache
	}
	return p.config.EnableCaching
}

func (p *AnthropicProvider) convertMessages(messages []Message, usePromptCache bool) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, 0, len(messages))

	// Track the index and block position of the last user text block so
	// we can stamp it with a cache control breakpoint after the loop.
	var lastUserIdx int
	var lastUserHasText bool

	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			result = append(result, anthropic.NewUserMessage(
				anthropic.NewTextBlock(msg.Content),
			))
			lastUserIdx = len(result) - 1
			lastUserHasText = true

		case RoleAssistant:
			safeMetadata := sanitizeProviderMetadataForHistory(msg.Metadata)
			safeToolCalls := SanitizeToolCallsForHistory(msg.ToolCalls)
			if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
				continue
			}
			// When serialized content blocks are available (preserved from streaming
			// or disk), reconstruct SDK params to retain thinking blocks with
			// opaque signatures. Uses decodeAnthropicBlocks to handle both
			// in-memory []anthropicContentBlock and deserialized []any forms.
			if v, ok := safeMetadata[anthropicRawContentKey]; ok {
				if blocks := decodeAnthropicBlocks(v); len(blocks) > 0 {
					params := make([]anthropic.ContentBlockParamUnion, len(blocks))
					for i := range blocks {
						params[i] = p.anthropicContentBlockToParam(&blocks[i])
					}
					result = append(result, anthropic.NewAssistantMessage(params...))
					continue
				}
			}
			if len(safeToolCalls) > 0 {
				blocks := make([]anthropic.ContentBlockParamUnion, 0, len(safeToolCalls)+1)
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				for _, tc := range safeToolCalls {
					blocks = append(blocks, anthropic.ContentBlockParamUnion{
						OfToolUse: &anthropic.ToolUseBlockParam{
							ID:    normalizeToolCallID(tc.ID),
							Name:  p.outboundAnthropicToolName(tc.Name),
							Input: json.RawMessage(tc.Arguments),
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
				anthropic.NewToolResultBlock(normalizeToolCallID(msg.ToolCallID), msg.Content, msg.IsError),
			))
		}
	}

	// Stamp the last user text block with a cache control breakpoint so
	// that the conversational context up to this point is cached.
	if usePromptCache && lastUserHasText && lastUserIdx < len(result) {
		blocks := result[lastUserIdx].Content
		if len(blocks) > 0 {
			if tb := blocks[len(blocks)-1].OfText; tb != nil {
				tb.CacheControl = p.newCacheControl()
			}
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

// convertTools converts generic tools to Anthropic format. When prompt
// caching is enabled, a cache control breakpoint is set on the last tool
// so the tool definitions are cached across requests.
func (p *AnthropicProvider) convertTools(tools []Tool, usePromptCache bool) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(tools))
	for i, tool := range tools {
		lastTool := usePromptCache && i == len(tools)-1
		switch tool.ResolvedKind() {
		case ToolKindNativeWebSearch:
			result = append(result, p.anthropicWebSearchTool(tool, lastTool))
		default:
			tp := &anthropic.ToolParam{
				Name:        p.outboundAnthropicToolName(tool.Name),
				Description: anthropic.String(tool.Description),
				InputSchema: buildAnthropicSchema(tool.Parameters),
			}
			if lastTool {
				tp.CacheControl = p.newCacheControl()
			}
			result = append(result, anthropic.ToolUnionParam{OfTool: tp})
		}
	}
	return result
}

func (p *AnthropicProvider) anthropicWebSearchTool(tool Tool, usePromptCache bool) anthropic.ToolUnionParam {
	param := &anthropic.WebSearchTool20260209Param{}
	if tool.WebSearch != nil {
		param.AllowedDomains = append([]string(nil), tool.WebSearch.AllowedDomains...)
		param.BlockedDomains = append([]string(nil), tool.WebSearch.BlockedDomains...)
		if tool.WebSearch.MaxUses > 0 {
			param.MaxUses = anthropic.Int(int64(tool.WebSearch.MaxUses))
		}
		if tool.WebSearch.Strict {
			param.Strict = anthropic.Bool(true)
		}
		if tool.WebSearch.DeferLoading {
			param.DeferLoading = anthropic.Bool(true)
		}
		if location := anthropicWebSearchUserLocation(tool.WebSearch.UserLocation); location != nil {
			param.UserLocation = *location
		}
	}
	if usePromptCache {
		param.CacheControl = p.newCacheControl()
	}
	return anthropic.ToolUnionParam{OfWebSearchTool20260209: param}
}

func anthropicWebSearchUserLocation(location *WebSearchUserLocation) *anthropic.UserLocationParam {
	if location == nil {
		return nil
	}
	if strings.TrimSpace(location.City) == "" &&
		strings.TrimSpace(location.Country) == "" &&
		strings.TrimSpace(location.Region) == "" &&
		strings.TrimSpace(location.Timezone) == "" {
		return nil
	}
	param := &anthropic.UserLocationParam{}
	if city := strings.TrimSpace(location.City); city != "" {
		param.City = anthropic.String(city)
	}
	if country := strings.TrimSpace(location.Country); country != "" {
		param.Country = anthropic.String(country)
	}
	if region := strings.TrimSpace(location.Region); region != "" {
		param.Region = anthropic.String(region)
	}
	if timezone := strings.TrimSpace(location.Timezone); timezone != "" {
		param.Timezone = anthropic.String(timezone)
	}
	return param
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
	var thinking strings.Builder
	var toolCalls []ToolCall
	var nativeSearchCalls []NativeWebSearchCall

	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			content += b.Text
		case anthropic.ThinkingBlock:
			thinking.WriteString(b.Thinking)
		case anthropic.ToolUseBlock:
			args, _ := b.Input.MarshalJSON()
			toolCalls = append(toolCalls, ToolCall{
				ID:        b.ID,
				Name:      p.inboundAnthropicToolName(b.Name),
				Arguments: string(args),
			})
		case anthropic.ServerToolUseBlock:
			if native := anthropicServerToolUseToNativeWebSearch(b); native != nil {
				nativeSearchCalls = append(nativeSearchCalls, *native)
			}
		}
	}

	meta := map[string]any{"id": msg.ID}
	if raw := extractAnthropicRawContent(msg, p.inboundAnthropicToolName); len(raw) > 0 {
		meta[anthropicRawContentKey] = raw
	}
	if len(nativeSearchCalls) > 0 {
		meta[ProviderMetadataNativeWebSearchCallsKey] = nativeSearchCalls
	}

	return &Response{
		Content:    content,
		Thinking:   strings.TrimSpace(thinking.String()),
		Model:      string(msg.Model),
		StopReason: p.convertStopReason(msg.StopReason),
		Usage: Usage{
			InputTokens:      int(msg.Usage.InputTokens),
			OutputTokens:     int(msg.Usage.OutputTokens),
			TotalTokens:      int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
			CacheReadTokens:  int(msg.Usage.CacheReadInputTokens),
			CacheWriteTokens: int(msg.Usage.CacheCreationInputTokens),
		},
		ToolCalls:        toolCalls,
		ProviderMetadata: meta,
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
		case anthropic.SignatureDelta:
			// Signature deltas carry the opaque thinking block signature.
			// The contentBlockTracker accumulator already captures them for
			// multi-turn replay; no user-visible chunk is needed.
			return nil
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
					Name: p.inboundAnthropicToolName(tb.Name),
				},
				Timestamp: time.Now(),
			}
		}
		if ev.ContentBlock.Type == "server_tool_use" {
			tb := ev.ContentBlock.AsAny().(anthropic.ServerToolUseBlock)
			if native := anthropicServerToolUseToNativeWebSearch(tb); native != nil {
				toolCallIDForIndex[ev.Index] = tb.ID
				return &StreamChunk{
					Index: index,
					Type:  ChunkTypeToolStart,
					ToolCall: &ToolCallChunk{
						ID:             tb.ID,
						Name:           "web_search",
						Kind:           ToolKindNativeWebSearch,
						ArgumentsDelta: native.ArgumentsJSON(),
					},
					Timestamp: time.Now(),
				}
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

func anthropicServerToolUseToNativeWebSearch(block anthropic.ServerToolUseBlock) *NativeWebSearchCall {
	if string(block.Name) != "web_search" {
		return nil
	}
	call := &NativeWebSearchCall{
		ID:       block.ID,
		Provider: "anthropic",
		Action:   "search",
	}
	switch input := block.Input.(type) {
	case map[string]any:
		if query, ok := input["query"].(string); ok {
			call.Query = strings.TrimSpace(query)
		}
	case []byte:
		var decoded map[string]any
		if err := json.Unmarshal(input, &decoded); err == nil {
			if query, ok := decoded["query"].(string); ok {
				call.Query = strings.TrimSpace(query)
			}
		}
	case string:
		var decoded map[string]any
		if err := json.Unmarshal([]byte(input), &decoded); err == nil {
			if query, ok := decoded["query"].(string); ok {
				call.Query = strings.TrimSpace(query)
			}
		}
	}
	return call
}

// anthropicRawContentKey is the ProviderMetadata key for serialized content
// blocks from the model's response. Preserving the original content (including
// thinking blocks with opaque signatures) is required for multi-turn
// conversations with extended thinking enabled.
//
// Values stored under this key are []anthropicContentBlock — a plain struct
// slice that survives JSON round-trips through disk persistence.
const anthropicRawContentKey = "anthropic_raw_content"

// anthropicContentBlock is a JSON-serializable representation of an Anthropic
// content block. Unlike raw SDK ContentBlockParamUnion values, this survives
// JSON round-trips through disk persistence (map[string]any → JSON → map[string]any).
type anthropicContentBlock struct {
	Type      string `json:"type"`                // "thinking", "redacted_thinking", "text", "tool_use"
	Text      string `json:"text,omitempty"`      // text blocks
	Thinking  string `json:"thinking,omitempty"`  // thinking blocks
	Signature string `json:"signature,omitempty"` // thinking blocks (opaque, required for replay)
	Data      string `json:"data,omitempty"`      // redacted thinking blocks
	ToolID    string `json:"id,omitempty"`        // tool_use blocks
	ToolName  string `json:"name,omitempty"`      // tool_use blocks
	ToolInput string `json:"input,omitempty"`     // tool_use blocks (raw JSON string)
}

// anthropicContentBlockToParam converts a serializable content block back into
// the SDK param type, applying any OAuth wire-format tool name transforms.
func (p *AnthropicProvider) anthropicContentBlockToParam(b *anthropicContentBlock) anthropic.ContentBlockParamUnion {
	switch b.Type {
	case "thinking":
		return anthropic.NewThinkingBlock(b.Signature, b.Thinking)
	case "redacted_thinking":
		return anthropic.NewRedactedThinkingBlock(b.Data)
	case "tool_use":
		toolName := b.ToolName
		if p != nil {
			toolName = p.outboundAnthropicToolName(toolName)
		}
		return anthropic.ContentBlockParamUnion{
			OfToolUse: &anthropic.ToolUseBlockParam{
				ID:    b.ToolID,
				Name:  toolName,
				Input: json.RawMessage(b.ToolInput),
			},
		}
	default: // "text" and any unknown types
		return anthropic.NewTextBlock(b.Text)
	}
}

// decodeAnthropicBlocks extracts []anthropicContentBlock from a Metadata value.
// Handles both the in-memory type (direct assertion) and the deserialized form
// ([]any of map[string]any from JSON round-trip through disk).
func decodeAnthropicBlocks(v any) []anthropicContentBlock {
	if blocks, ok := v.([]anthropicContentBlock); ok {
		return blocks
	}
	// JSON round-trip for data that was deserialized from disk.
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil
	}
	return blocks
}

// contentBlockTracker accumulates streaming content blocks using the SDK's
// built-in Message.Accumulate method. This captures thinking blocks (with
// signatures), redacted thinking, text, and tool_use blocks in stream order
// for faithful multi-turn replay.
type contentBlockTracker struct {
	msg anthropic.Message
}

func newContentBlockTracker() *contentBlockTracker {
	return &contentBlockTracker{}
}

// observe feeds a stream event into the SDK accumulator.
func (t *contentBlockTracker) observe(event anthropic.MessageStreamEventUnion) {
	t.msg.Accumulate(event)
}

// build converts the accumulated content blocks into serializable form.
// Returns nil when there are no content blocks.
func (t *contentBlockTracker) build(toolNameTransform func(string) string) []anthropicContentBlock {
	return contentBlocksFromSDK(t.msg.Content, toolNameTransform)
}

// extractAnthropicRawContent converts an Anthropic Message's content blocks
// into serializable form for multi-turn replay. Used by the non-streaming path.
func extractAnthropicRawContent(msg *anthropic.Message, toolNameTransform func(string) string) []anthropicContentBlock {
	return contentBlocksFromSDK(msg.Content, toolNameTransform)
}

// contentBlocksFromSDK converts SDK content block unions into the serializable
// representation. Preserves block order for faithful multi-turn replay.
func contentBlocksFromSDK(content []anthropic.ContentBlockUnion, toolNameTransform func(string) string) []anthropicContentBlock {
	if len(content) == 0 {
		return nil
	}
	blocks := make([]anthropicContentBlock, 0, len(content))
	for _, cb := range content {
		switch b := cb.AsAny().(type) {
		case anthropic.ThinkingBlock:
			blocks = append(blocks, anthropicContentBlock{
				Type:      "thinking",
				Thinking:  b.Thinking,
				Signature: b.Signature,
			})
		case anthropic.RedactedThinkingBlock:
			blocks = append(blocks, anthropicContentBlock{
				Type: "redacted_thinking",
				Data: b.Data,
			})
		case anthropic.TextBlock:
			blocks = append(blocks, anthropicContentBlock{
				Type: "text",
				Text: b.Text,
			})
		case anthropic.ToolUseBlock:
			args, _ := b.Input.MarshalJSON()
			toolName := b.Name
			if toolNameTransform != nil {
				toolName = toolNameTransform(toolName)
			}
			blocks = append(blocks, anthropicContentBlock{
				Type:      "tool_use",
				ToolID:    b.ID,
				ToolName:  toolName,
				ToolInput: string(args),
			})
		}
	}
	return blocks
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
	case anthropic.StopReasonPauseTurn:
		return StopReasonPauseTurn
	case anthropic.StopReasonRefusal:
		return StopReasonContentFilter
	default:
		return StopReasonEndTurn
	}
}
