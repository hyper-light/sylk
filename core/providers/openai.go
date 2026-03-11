package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/skills"
	"github.com/google/uuid"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// OpenAIProvider implements Provider for OpenAI's GPT models
type OpenAIProvider struct {
	client      *openai.Client
	clientMu    sync.RWMutex
	config      OpenAIConfig
	skills      []skills.Skill
	authService oauth.OpenAIAuthService
}

type OpenAIDeviceAuthSession struct {
	Challenge *oauth.DeviceCodeChallenge
	Results   <-chan OpenAIDeviceAuthResult
	Cancel    context.CancelFunc
}

type OpenAIDeviceAuthResult struct {
	Auth *oauth.OpenAIChatGPTAuth
	Err  error
}

type OpenAIModel string

const (
	GPT_5_4     OpenAIModel = "gpt-5.4"
	GPT_5_4_Pro OpenAIModel = "gpt-5.4-pro"
)

const (
	openAIAuthModeAPIKey  = "api_key"
	openAIAuthModeChatGPT = "chatgpt"

	// Mirrors Codex chatgpt-mode backend routing.
	defaultChatGPTCodexBaseURL = "https://chatgpt.com/backend-api/codex"
	defaultOpenAIUserAgent     = "sylk/1.0 (+https://github.com/adalundhe/sylk)"
	defaultOpenAIOriginator    = "sylk_cli_go"
)

// Supported OpenAI models (canonical slugs).
var openaiModelCatalog = map[string]ModelInfo{
	"gpt-5.4":     {ID: "gpt-5.4", Name: "GPT-5.4", MaxContext: 272000},
	"gpt-5.4-pro": {ID: "gpt-5.4-pro", Name: "GPT-5.4 Pro", MaxContext: 272000},
}

var openaiModelAliases = map[string]string{
	"gpt-5-4":     "gpt-5.4",
	"gpt-5-4-pro": "gpt-5.4-pro",
}

var openAIRoleToInputRole = map[Role]responses.EasyInputMessageRole{
	RoleSystem:    responses.EasyInputMessageRoleSystem,
	RoleUser:      responses.EasyInputMessageRoleUser,
	RoleAssistant: responses.EasyInputMessageRoleAssistant,
}

// NewOpenAIProvider creates a new OpenAI provider with the given configuration.
func NewOpenAIProvider(ctx context.Context, config OpenAIConfig, skills ...skills.Skill) (*OpenAIProvider, error) {
	return NewOpenAIProviderWithAuthService(ctx, config, oauth.NewOpenAIAuthService(oauth.OpenAIAuthServiceConfig{}), skills...)
}

// NewOpenAIProviderWithAuthService creates a provider using a custom auth service.
// This enables callers to orchestrate OAuth behavior through a shared interface.
func NewOpenAIProviderWithAuthService(
	ctx context.Context,
	config OpenAIConfig,
	authService oauth.OpenAIAuthService,
	skills ...skills.Skill,
) (*OpenAIProvider, error) {
	applyOpenAIProviderDefaults(&config)
	normalizeOpenAIProviderModels(&config)
	applyDefaultChatGPTBaseURL(&config)
	if err := hydrateOpenAIConfig(ctx, &config, authService); err != nil {
		return nil, err
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	client := openai.NewClient(buildOpenAIClientOptions(config)...)

	return &OpenAIProvider{
		client:      &client,
		config:      config,
		skills:      skills,
		authService: authService,
	}, nil
}

func applyOpenAIProviderDefaults(config *OpenAIConfig) {
	if config == nil {
		return
	}
	defaults := DefaultOpenAIConfig()
	applyDefaultString(&config.Model, defaults.Model)
	applyDefaultInt(&config.MaxTokens, defaults.MaxTokens)
	applyDefaultString(&config.AuthMode, defaults.AuthMode)
	applyDefaultString(&config.FallbackModel, defaults.FallbackModel)
}

func applyDefaultString(value *string, fallback string) {
	if strings.TrimSpace(*value) != "" {
		return
	}
	*value = fallback
}

func applyDefaultInt(value *int, fallback int) {
	if *value != 0 {
		return
	}
	*value = fallback
}

func normalizeOpenAIProviderModels(config *OpenAIConfig) {
	if config == nil {
		return
	}
	config.Model = normalizeOpenAIModel(config.Model)
	config.FallbackModel = normalizeOpenAIModel(config.FallbackModel)
}

func applyDefaultChatGPTBaseURL(config *OpenAIConfig) {
	if config == nil {
		return
	}
	if config.AuthMode != openAIAuthModeChatGPT {
		return
	}
	if config.BaseURL == "" {
		config.BaseURL = defaultChatGPTCodexBaseURL
	}
}

func hydrateOpenAIConfig(ctx context.Context, config *OpenAIConfig, authService oauth.OpenAIAuthService) error {
	if config == nil {
		return fmt.Errorf("openai config is nil")
	}
	normalizeOpenAIHydrationFields(config)
	if config.AuthMode == openAIAuthModeChatGPT {
		return hydrateChatGPTConfig(ctx, config, authService)
	}
	return hydrateOpenAIAPIKey(config)
}

func normalizeOpenAIHydrationFields(config *OpenAIConfig) {
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.ChatGPTAccountID = strings.TrimSpace(config.ChatGPTAccountID)
	config.AuthMode = strings.TrimSpace(config.AuthMode)
	if config.AuthMode == "" {
		config.AuthMode = openAIAuthModeAPIKey
	}
}

func hydrateChatGPTConfig(ctx context.Context, config *OpenAIConfig, authService oauth.OpenAIAuthService) error {
	if err := resolveAndApplyChatGPTAuth(ctx, config, authService); err != nil {
		return err
	}
	return validateChatGPTConfig(config)
}

func resolveAndApplyChatGPTAuth(ctx context.Context, config *OpenAIConfig, authService oauth.OpenAIAuthService) error {
	if !chatGPTConfigNeedsResolution(config) {
		return nil
	}
	if authService == nil {
		return nil
	}
	auth, err := authService.Resolve(ctx)
	if err != nil {
		return handleChatGPTResolveError(err)
	}
	applyResolvedChatGPTAuth(config, auth)
	return nil
}

func chatGPTConfigNeedsResolution(config *OpenAIConfig) bool {
	if config == nil {
		return false
	}
	if config.APIKey == "" {
		return true
	}
	return config.ChatGPTAccountID == ""
}

func handleChatGPTResolveError(err error) error {
	if errors.Is(err, oauth.ErrAuthNotConfigured) {
		return nil
	}
	return fmt.Errorf("resolve chatgpt auth: %w", err)
}

func applyResolvedChatGPTAuth(config *OpenAIConfig, auth *oauth.OpenAIChatGPTAuth) {
	if !canApplyResolvedChatGPTValues(config, auth) {
		return
	}
	if config.APIKey == "" {
		config.APIKey = strings.TrimSpace(auth.AccessToken)
	}
	if config.ChatGPTAccountID == "" {
		config.ChatGPTAccountID = strings.TrimSpace(auth.ChatGPTAccountID)
	}
}

func canApplyResolvedChatGPTValues(config *OpenAIConfig, auth *oauth.OpenAIChatGPTAuth) bool {
	if config == nil {
		return false
	}
	return auth != nil
}

func validateChatGPTConfig(config *OpenAIConfig) error {
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("openai chatgpt auth requires access token")
	}
	if strings.TrimSpace(config.ChatGPTAccountID) == "" {
		return fmt.Errorf("openai chatgpt auth requires chatgpt_account_id")
	}
	return nil
}

func hydrateOpenAIAPIKey(config *OpenAIConfig) error {
	if config == nil {
		return nil
	}
	if config.APIKey != "" {
		return nil
	}
	key, err := llm.ResolveAPIKey("openai")
	if err != nil {
		return nil
	}
	config.APIKey = strings.TrimSpace(key)
	return nil
}

func buildOpenAIClientOptions(config OpenAIConfig) []option.RequestOption {
	timeout := resolveOpenAITimeout(config.Timeout)
	opts := buildBaseOpenAIClientOptions(config, timeout)
	return appendOptionalOpenAIClientOptions(opts, config)
}

func resolveOpenAITimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultOpenAIRequestTimeout
	}
	return timeout
}

func buildBaseOpenAIClientOptions(config OpenAIConfig, timeout time.Duration) []option.RequestOption {
	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithMaxRetries(config.MaxRetries),
		option.WithHTTPClient(newDefaultOpenAIHTTPClient(timeout)),
		option.WithHeader("User-Agent", defaultOpenAIUserAgent),
	}
	if timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(timeout))
	}
	return opts
}

func appendOptionalOpenAIClientOptions(opts []option.RequestOption, config OpenAIConfig) []option.RequestOption {
	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}
	opts = appendOpenAIHeaderOption(opts, "OpenAI-Organization", config.Organization)
	opts = appendOpenAIHeaderOption(opts, "OpenAI-Project", config.Project)
	if config.AuthMode == openAIAuthModeChatGPT && config.ChatGPTAccountID != "" {
		opts = append(opts, option.WithHeader("ChatGPT-Account-ID", config.ChatGPTAccountID))
	}
	return opts
}

func appendOpenAIHeaderOption(opts []option.RequestOption, key string, value string) []option.RequestOption {
	if strings.TrimSpace(value) == "" {
		return opts
	}
	return append(opts, option.WithHeader(key, value))
}

func newDefaultOpenAIHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

func (p *OpenAIProvider) getClient() *openai.Client {
	p.clientMu.RLock()
	defer p.clientMu.RUnlock()
	return p.client
}

func (p *OpenAIProvider) replaceClient(client *openai.Client) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	p.client = client
}

func (p *OpenAIProvider) StartChatGPTDeviceAuth(
	ctx context.Context,
	timeout time.Duration,
) (*OpenAIDeviceAuthSession, error) {
	if p.authService == nil {
		return nil, fmt.Errorf("chatgpt auth service is not configured")
	}
	session, err := oauth.StartDeviceAuthSession(ctx, p.authService, timeout)
	if err != nil {
		return nil, err
	}
	results := make(chan OpenAIDeviceAuthResult, 1)
	go p.forwardDeviceAuthResults(session.Results, results)
	return &OpenAIDeviceAuthSession{
		Challenge: session.Challenge,
		Results:   results,
		Cancel:    session.Cancel,
	}, nil
}

func (p *OpenAIProvider) forwardDeviceAuthResults(
	source <-chan oauth.DeviceAuthResult,
	target chan<- OpenAIDeviceAuthResult,
) {
	defer close(target)
	for result := range source {
		if shouldApplyDeviceAuthResult(result) {
			p.applyRefreshedChatGPTAuth(result.Auth)
		}
		target <- OpenAIDeviceAuthResult{
			Auth: result.Auth,
			Err:  result.Err,
		}
	}
}

func shouldApplyDeviceAuthResult(result oauth.DeviceAuthResult) bool {
	if result.Err != nil {
		return false
	}
	return result.Auth != nil
}

type requestObserver struct {
	requestID  string
	retryCount int
	response   *http.Response
	bodyText   string
}

func newRequestObserver() *requestObserver {
	return &requestObserver{
		requestID: uuid.NewString(),
	}
}

func (o *requestObserver) options() []option.RequestOption {
	return []option.RequestOption{
		option.WithResponseInto(&o.response),
		option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			req.Header.Set("X-Request-ID", o.requestID)
			if v := req.Header.Get("X-Stainless-Retry-Count"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > o.retryCount {
					o.retryCount = n
				}
			}
			resp, err := next(req)
			o.captureErrorBody(resp)
			return resp, err
		}),
	}
}

func (o *requestObserver) captureErrorBody(resp *http.Response) {
	if o == nil || resp == nil || resp.StatusCode < http.StatusBadRequest || resp.Body == nil {
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	o.bodyText = strings.TrimSpace(string(body))
}

func (p *OpenAIProvider) buildResponseRequestOptions(
	req *Request,
	observer *requestObserver,
) []option.RequestOption {
	opts := make([]option.RequestOption, 0, 4)
	if observer != nil {
		opts = append(opts, observer.options()...)
	}
	if sessionID := resolveOpenAIRequestSessionID(req); sessionID != "" {
		opts = append(opts, option.WithHeader("session_id", sessionID))
	}
	if originator := resolveOpenAIRequestOriginator(req); originator != "" {
		opts = append(opts, option.WithHeader("originator", originator))
	}
	return opts
}

func resolveOpenAIRequestSessionID(req *Request) string {
	if req == nil || req.Metadata == nil {
		return ""
	}
	value, _ := req.Metadata["session_id"].(string)
	return strings.TrimSpace(value)
}

func resolveOpenAIRequestOriginator(req *Request) string {
	if req != nil && req.Metadata != nil {
		if value, ok := req.Metadata["originator"].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return defaultOpenAIOriginator
}

func (o *requestObserver) metadata() map[string]any {
	metadata := map[string]any{
		"request_id":  o.requestID,
		"retry_count": o.retryCount,
	}
	if o.response != nil {
		metadata["http_status"] = o.response.StatusCode
		if v := strings.TrimSpace(o.response.Header.Get("x-request-id")); v != "" {
			metadata["openai_request_id"] = v
		}
		if v := strings.TrimSpace(o.response.Header.Get("openai-processing-ms")); v != "" {
			metadata["processing_ms"] = v
		}
	}
	if o.bodyText != "" {
		metadata["http_error_body"] = o.bodyText
	}
	return metadata
}

func mergeMetadata(dst map[string]any, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		if shouldSkipMetadata(k, v) {
			continue
		}
		dst[k] = v
	}
	return dst
}

func shouldSkipMetadata(key string, value any) bool {
	if key == "" {
		return true
	}
	return value == nil
}

func (p *OpenAIProvider) refreshChatGPTAuth(ctx context.Context) error {
	if p.authService == nil {
		return fmt.Errorf("chatgpt auth service is not configured")
	}
	updated, err := p.resolveAndRefreshChatGPTAuth(ctx)
	if err != nil {
		return err
	}
	p.applyRefreshedChatGPTAuth(updated)
	return nil
}

func (p *OpenAIProvider) resolveAndRefreshChatGPTAuth(ctx context.Context) (*oauth.OpenAIChatGPTAuth, error) {
	auth, err := p.resolveChatGPTAuth(ctx)
	if err != nil {
		return nil, err
	}
	return p.refreshAndPersistChatGPTAuth(ctx, auth)
}

func (p *OpenAIProvider) resolveChatGPTAuth(ctx context.Context) (*oauth.OpenAIChatGPTAuth, error) {
	auth, err := p.authService.Resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve chatgpt auth: %w", err)
	}
	return auth, nil
}

func (p *OpenAIProvider) refreshAndPersistChatGPTAuth(
	ctx context.Context,
	auth *oauth.OpenAIChatGPTAuth,
) (*oauth.OpenAIChatGPTAuth, error) {
	updated, err := p.authService.Refresh(ctx, auth)
	if err != nil {
		return nil, p.handleChatGPTRefreshError(ctx, err)
	}
	if err := p.authService.Save(ctx, updated); err != nil {
		return nil, fmt.Errorf("persist refreshed auth: %w", err)
	}
	return updated, nil
}

func (p *OpenAIProvider) handleChatGPTRefreshError(ctx context.Context, err error) error {
	if oauth.IsInvalidGrant(err) {
		_ = p.authService.Delete(ctx)
	}
	return fmt.Errorf("refresh chatgpt auth: %w", err)
}

func (p *OpenAIProvider) applyRefreshedChatGPTAuth(updated *oauth.OpenAIChatGPTAuth) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	applyRefreshedChatGPTConfig(p, updated)
	client := openai.NewClient(buildOpenAIClientOptions(p.config)...)
	p.client = &client
}

func applyRefreshedChatGPTConfig(provider *OpenAIProvider, updated *oauth.OpenAIChatGPTAuth) {
	if !canApplyRefreshedChatGPTConfig(provider, updated) {
		return
	}
	applyRefreshedChatGPTAccessToken(provider, updated)
	applyRefreshedChatGPTAccountID(provider, updated)
}

func canApplyRefreshedChatGPTConfig(provider *OpenAIProvider, updated *oauth.OpenAIChatGPTAuth) bool {
	if provider == nil {
		return false
	}
	return updated != nil
}

func applyRefreshedChatGPTAccessToken(provider *OpenAIProvider, updated *oauth.OpenAIChatGPTAuth) {
	provider.config.APIKey = strings.TrimSpace(updated.AccessToken)
}

func applyRefreshedChatGPTAccountID(provider *OpenAIProvider, updated *oauth.OpenAIChatGPTAuth) {
	accountID := strings.TrimSpace(updated.ChatGPTAccountID)
	if accountID == "" {
		return
	}
	provider.config.ChatGPTAccountID = accountID
}

func isChatGPTAuthError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden
	}
	return false
}

func wrapOpenAIProviderError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if providerErr := buildOpenAIProviderError(operation, err); providerErr != nil {
		return providerErr
	}
	return WrapError(ProviderTypeOpenAI, operation, err)
}

func buildOpenAIProviderError(operation string, err error) error {
	apiErr, ok := asOpenAIError(err)
	if !ok {
		return nil
	}
	pe := &ProviderError{
		Provider:   ProviderTypeOpenAI,
		Operation:  operation,
		StatusCode: apiErr.StatusCode,
		Message:    apiErr.Message,
		Underlying: err,
		Retryable:  isOpenAIRetryableStatus(apiErr.StatusCode),
	}
	if d, ok := openAIRetryAfter(apiErr); ok {
		pe.RetryAfter = d
	}
	return pe
}

func asOpenAIError(err error) (*openai.Error, bool) {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

func isOpenAIRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func openAIRetryAfter(apiErr *openai.Error) (time.Duration, bool) {
	if apiErr == nil {
		return 0, false
	}
	if apiErr.Response == nil {
		return 0, false
	}
	return parseRetryAfterHeader(apiErr.Response.Header)
}

func parseRetryAfterHeader(h http.Header) (time.Duration, bool) {
	if h == nil {
		return 0, false
	}
	if d, ok := parseRetryAfterMS(h.Get("Retry-After-Ms")); ok {
		return d, true
	}
	return parseRetryAfterValue(h.Get("Retry-After"))
}

func parseRetryAfterMS(value string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	ms, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || ms <= 0 {
		return 0, false
	}
	return time.Duration(ms * float64(time.Millisecond)), true
}

func parseRetryAfterValue(value string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	if d, ok := parseRetryAfterSeconds(trimmed); ok {
		return d, true
	}
	return parseRetryAfterTime(trimmed)
}

func parseRetryAfterSeconds(value string) (time.Duration, bool) {
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func parseRetryAfterTime(value string) (time.Duration, bool) {
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	d := time.Until(when)
	if d <= 0 {
		return 0, false
	}
	return d, true
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
	if p.config.AuthMode == openAIAuthModeChatGPT {
		return p.generateChatGPT(ctx, req)
	}
	return p.generateStandard(ctx, req)
}

func (p *OpenAIProvider) generateChatGPT(ctx context.Context, req *Request) (*Response, error) {
	params := p.buildResponseParams(req)
	requestedModel := string(params.Model)
	resp, err := p.runChatGPTGenerateAttempt(ctx, req)
	if err == nil {
		return resp, nil
	}
	fallbackModel, ok := p.selectFallbackModel(requestedModel, err)
	if !ok {
		return nil, wrapOpenAIProviderError("generate", err)
	}

	retryReq := *req
	retryReq.Model = fallbackModel
	resp, err = p.runChatGPTGenerateAttempt(ctx, &retryReq)
	if err != nil {
		return nil, wrapOpenAIProviderError("generate", err)
	}
	resp.ProviderMetadata = mergeMetadata(resp.ProviderMetadata, map[string]any{
		"requested_model": requestedModel,
		"fallback_model":  fallbackModel,
		"auth_mode":       p.config.AuthMode,
	})
	return resp, nil
}

func (p *OpenAIProvider) runChatGPTGenerateAttempt(ctx context.Context, req *Request) (*Response, error) {
	resp, err := p.generateViaStreaming(ctx, req)
	if err == nil {
		return resp, nil
	}
	if !isChatGPTAuthError(err) {
		return nil, err
	}
	if refreshErr := p.refreshChatGPTAuth(ctx); refreshErr != nil {
		return nil, refreshErr
	}
	return p.generateViaStreaming(ctx, req)
}

func (p *OpenAIProvider) generateStandard(ctx context.Context, req *Request) (*Response, error) {
	params := p.buildResponseParams(req)
	requestedModel := string(params.Model)
	resp, err := p.runStandardGenerateAttempt(ctx, req, params)
	if err == nil {
		return resp, nil
	}
	fallbackModel, ok := p.selectFallbackModel(requestedModel, err)
	if !ok {
		return nil, wrapOpenAIProviderError("generate", err)
	}
	retryReq := *req
	retryReq.Model = fallbackModel
	resp, err = p.runStandardGenerateAttempt(ctx, &retryReq, p.buildResponseParams(&retryReq))
	if err != nil {
		return nil, wrapOpenAIProviderError("generate", err)
	}
	resp.ProviderMetadata = mergeMetadata(resp.ProviderMetadata, map[string]any{
		"requested_model": requestedModel,
		"fallback_model":  fallbackModel,
		"auth_mode":       p.config.AuthMode,
	})
	return resp, nil
}

func (p *OpenAIProvider) runStandardGenerateAttempt(
	ctx context.Context,
	req *Request,
	params responses.ResponseNewParams,
) (*Response, error) {
	observer := newRequestObserver()
	result, err := p.getClient().Responses.New(ctx, params, p.buildResponseRequestOptions(req, observer)...)
	if err != nil {
		return nil, err
	}
	resp := p.convertResponse(result)
	resp.ProviderMetadata = mergeMetadata(resp.ProviderMetadata, observer.metadata())
	resp.ProviderMetadata["auth_mode"] = p.config.AuthMode
	return resp, nil
}

func (p *OpenAIProvider) generateViaStreaming(ctx context.Context, req *Request) (*Response, error) {
	assembler := newStreamResponseAssembler()
	completion, streamStopReason, streamUsage, streamMetadata, err := p.runResponseStream(ctx, req, assembler.onChunk)
	if err != nil {
		return nil, err
	}

	if completion != nil {
		return p.responseFromStreamingCompletion(completion, streamMetadata), nil
	}
	return p.responseFromStreamingFallback(req, assembler, streamStopReason, streamUsage, streamMetadata), nil
}

func (p *OpenAIProvider) responseFromStreamingCompletion(completion *responses.Response, metadata map[string]any) *Response {
	resp := p.convertResponse(completion)
	resp.ProviderMetadata = mergeMetadata(resp.ProviderMetadata, metadata)
	resp.ProviderMetadata["auth_mode"] = p.config.AuthMode
	return resp
}

func (p *OpenAIProvider) responseFromStreamingFallback(
	req *Request,
	assembler *streamResponseAssembler,
	streamStopReason StopReason,
	streamUsage Usage,
	streamMetadata map[string]any,
) *Response {
	assembler = ensureStreamResponseAssembler(assembler)
	resp := &Response{
		Content:    assembler.content.String(),
		StopReason: streamStopReason,
		Usage:      streamUsage,
	}
	resp.StopReason = coalesceStopReason(resp.StopReason)
	resp.Model = p.resolveStreamResponseModel(req)
	if toolCalls := assembler.orderedToolCalls(); len(toolCalls) > 0 {
		resp.ToolCalls = toolCalls
	}
	resp.ProviderMetadata = mergeMetadata(resp.ProviderMetadata, streamMetadata)
	resp.ProviderMetadata["auth_mode"] = p.config.AuthMode
	return resp
}

func ensureStreamResponseAssembler(assembler *streamResponseAssembler) *streamResponseAssembler {
	if assembler != nil {
		return assembler
	}
	return newStreamResponseAssembler()
}

func coalesceStopReason(reason StopReason) StopReason {
	if reason != "" {
		return reason
	}
	return StopReasonEndTurn
}

func (p *OpenAIProvider) resolveStreamResponseModel(req *Request) string {
	if req != nil {
		if model := normalizeOpenAIModel(req.Model); model != "" {
			return ResolveOpenAIModelForAuth(model, p.config.AuthMode)
		}
	}
	return ResolveOpenAIModelForAuth(p.config.Model, p.config.AuthMode)
}

func (p *OpenAIProvider) StreamWithHandler(ctx context.Context, req *Request, handler StreamHandler) error {
	err := p.streamWithAuthRefreshRetry(ctx, req, retryAwareHandler(handler))
	return wrapOpenAIProviderError("stream", err)
}

func (p *OpenAIProvider) streamWithAuthRefreshRetry(ctx context.Context, req *Request, handler StreamHandler) error {
	_, _, _, _, err := p.runResponseStream(ctx, req, handler)
	if !p.shouldRetryStreamForAuth(err) {
		return err
	}
	if refreshErr := p.refreshChatGPTAuth(ctx); refreshErr != nil {
		return refreshErr
	}
	_, _, _, _, err = p.runResponseStream(ctx, req, handler)
	return err
}

func (p *OpenAIProvider) shouldRetryStreamForAuth(err error) bool {
	if err == nil {
		return false
	}
	if p.config.AuthMode != openAIAuthModeChatGPT {
		return false
	}
	return isChatGPTAuthError(err)
}

type streamToolCallState struct {
	ToolCallChunk
	OutputIndex int64
}

type streamResponseAssembler struct {
	content       strings.Builder
	toolCalls     map[string]*ToolCall
	toolCallOrder []string
	handlers      []func(*StreamChunk) error
}

func newStreamResponseAssembler() *streamResponseAssembler {
	assembler := &streamResponseAssembler{
		toolCalls:     make(map[string]*ToolCall),
		toolCallOrder: make([]string, 0, 4),
	}
	assembler.handlers = []func(*StreamChunk) error{
		assembler.handleTextChunk,
		assembler.handleToolStartChunk,
		assembler.handleToolDeltaChunk,
		assembler.handleToolEndChunk,
	}
	return assembler
}

func (a *streamResponseAssembler) onChunk(chunk *StreamChunk) error {
	if chunk == nil {
		return nil
	}
	for _, handler := range a.handlers {
		if err := handler(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (a *streamResponseAssembler) handleTextChunk(chunk *StreamChunk) error {
	if chunk.Type != ChunkTypeText {
		return nil
	}
	a.content.WriteString(chunk.Text)
	return nil
}

func (a *streamResponseAssembler) handleToolStartChunk(chunk *StreamChunk) error {
	if chunk.Type != ChunkTypeToolStart {
		return nil
	}
	return a.applyToolStart(chunk.ToolCall)
}

func (a *streamResponseAssembler) handleToolDeltaChunk(chunk *StreamChunk) error {
	if chunk.Type != ChunkTypeToolDelta {
		return nil
	}
	return a.applyToolDelta(chunk.ToolCall)
}

func (a *streamResponseAssembler) handleToolEndChunk(chunk *StreamChunk) error {
	if chunk.Type != ChunkTypeToolEnd {
		return nil
	}
	return a.applyToolEnd(chunk.ToolCall)
}

func (a *streamResponseAssembler) applyToolStart(tool *ToolCallChunk) error {
	if tool == nil {
		return nil
	}
	tc := a.ensureToolCall(tool.ID)
	applyToolCallName(tc, tool.Name)
	applyToolStartArguments(tc, tool.ArgumentsDelta)
	return nil
}

func (a *streamResponseAssembler) applyToolDelta(tool *ToolCallChunk) error {
	if tool == nil {
		return nil
	}
	tc := a.ensureToolCall(tool.ID)
	applyToolCallName(tc, tool.Name)
	tc.Arguments += tool.ArgumentsDelta
	return nil
}

func (a *streamResponseAssembler) applyToolEnd(tool *ToolCallChunk) error {
	if tool == nil {
		return nil
	}
	tc := a.ensureToolCall(tool.ID)
	applyToolCallName(tc, tool.Name)
	applyToolEndArguments(tc, tool.ArgumentsDelta)
	return nil
}

func applyToolCallName(tc *ToolCall, name string) {
	if tc == nil {
		return
	}
	if strings.TrimSpace(name) == "" {
		return
	}
	tc.Name = name
}

func applyToolStartArguments(tc *ToolCall, delta string) {
	if tc == nil {
		return
	}
	if tc.Arguments != "" {
		return
	}
	if strings.TrimSpace(delta) == "" {
		return
	}
	tc.Arguments = delta
}

func applyToolEndArguments(tc *ToolCall, delta string) {
	if tc == nil {
		return
	}
	if strings.TrimSpace(delta) == "" {
		return
	}
	tc.Arguments = delta
}

func (a *streamResponseAssembler) ensureToolCall(id string) *ToolCall {
	tc, ok := a.toolCalls[id]
	if ok {
		return tc
	}
	tc = &ToolCall{ID: id}
	a.toolCalls[id] = tc
	a.toolCallOrder = append(a.toolCallOrder, id)
	return tc
}

func (a *streamResponseAssembler) orderedToolCalls() []ToolCall {
	result := make([]ToolCall, 0, len(a.toolCallOrder))
	for _, id := range a.toolCallOrder {
		if tc, ok := a.toolCalls[id]; ok {
			result = append(result, *tc)
		}
	}
	return result
}

type openAIResponseEventStream interface {
	Next() bool
	Current() responses.ResponseStreamEventUnion
	Err() error
	Close() error
}

type openAIResponseStreamRunner struct {
	provider *OpenAIProvider
	observer *requestObserver
	handler  StreamHandler
	stream   openAIResponseEventStream

	chunkIndex int
	finalIndex int

	toolCallBuilders map[string]*streamToolCallState
	toolCallStarted  map[string]bool
	toolCallEnded    map[string]bool

	completion            *responses.Response
	responseStopReason    StopReason
	responseUsage         Usage
	preserveUsageOnErrors bool
	eventHandlers         []func(any) error
}

func newOpenAIResponseStreamRunner(
	provider *OpenAIProvider,
	ctx context.Context,
	req *Request,
	handler StreamHandler,
) *openAIResponseStreamRunner {
	observer := newRequestObserver()
	params := provider.buildResponseParams(req)
	stream := provider.getClient().Responses.NewStreaming(
		ctx,
		params,
		provider.buildResponseRequestOptions(req, observer)...,
	)
	runner := &openAIResponseStreamRunner{
		provider:           provider,
		observer:           observer,
		handler:            handler,
		stream:             stream,
		toolCallBuilders:   make(map[string]*streamToolCallState),
		toolCallStarted:    make(map[string]bool),
		toolCallEnded:      make(map[string]bool),
		responseStopReason: StopReasonEndTurn,
	}
	runner.eventHandlers = []func(any) error{
		runner.handleTextDeltaEvent,
		runner.handleReasoningSummaryDeltaEvent,
		runner.handleFunctionCallArgumentsDeltaEvent,
		runner.handleFunctionCallArgumentsDoneEvent,
		runner.handleOutputItemAddedEvent,
		runner.handleOutputItemDoneEvent,
		runner.handleCompletedEvent,
		runner.handleFailedEvent,
		runner.handleIncompleteEvent,
		runner.handleResponseErrorEvent,
	}
	return runner
}

func (r *openAIResponseStreamRunner) run() (*responses.Response, StopReason, Usage, map[string]any, error) {
	defer r.closeStream()
	if err := r.emitStartChunk(); err != nil {
		return r.zeroUsageFailure(err)
	}
	if err := r.consume(); err != nil {
		return r.consumeFailure(err)
	}
	if err := r.finish(); err != nil {
		return r.zeroUsageFailure(err)
	}
	return r.success()
}

func (r *openAIResponseStreamRunner) closeStream() {
	if r.stream != nil {
		_ = r.stream.Close()
	}
}

func (r *openAIResponseStreamRunner) emitStartChunk() error {
	return r.emitChunk(&StreamChunk{
		Index:     r.chunkIndex,
		Type:      ChunkTypeStart,
		Timestamp: time.Now(),
	})
}

func (r *openAIResponseStreamRunner) consume() error {
	if r.stream == nil {
		return fmt.Errorf("openai stream is nil")
	}
	for r.stream.Next() {
		r.chunkIndex++
		if err := r.processEvent(r.stream.Current().AsAny()); err != nil {
			return err
		}
	}
	return r.consumeStreamError()
}

func (r *openAIResponseStreamRunner) processEvent(event any) error {
	for _, handler := range r.eventHandlers {
		if err := handler(event); err != nil {
			return err
		}
	}
	return nil
}

func (r *openAIResponseStreamRunner) consumeStreamError() error {
	if r.stream == nil {
		return nil
	}
	err := r.stream.Err()
	if err == nil {
		return nil
	}
	err = enrichOpenAIStreamError(err, r.observer)
	r.emitBestEffortErrorChunk(r.chunkIndex+1, err.Error())
	return fmt.Errorf("openai stream: %w", err)
}

func enrichOpenAIStreamError(err error, observer *requestObserver) error {
	if err == nil || observer == nil {
		return err
	}
	body := strings.TrimSpace(observer.bodyText)
	if body == "" || strings.Contains(err.Error(), body) {
		return err
	}
	return fmt.Errorf("%w: %s", err, body)
}

func (r *openAIResponseStreamRunner) emitBestEffortErrorChunk(index int, message string) {
	_ = r.emitChunk(&StreamChunk{
		Index:     index,
		Type:      ChunkTypeError,
		Text:      message,
		Timestamp: time.Now(),
	})
}

func (r *openAIResponseStreamRunner) finish() error {
	if err := r.emitPendingToolEnds(); err != nil {
		return err
	}
	return r.emitEndChunk()
}

func (r *openAIResponseStreamRunner) emitPendingToolEnds() error {
	pending := r.pendingToolCalls()
	r.finalIndex = r.chunkIndex
	for _, call := range pending {
		r.finalIndex++
		if err := r.emitToolEnd(r.finalIndex, call); err != nil {
			return err
		}
	}
	return nil
}

func (r *openAIResponseStreamRunner) pendingToolCalls() []*streamToolCallState {
	pending := make([]*streamToolCallState, 0, len(r.toolCallBuilders))
	for _, toolCall := range r.toolCallBuilders {
		if r.shouldEmitToolEnd(toolCall) {
			pending = append(pending, toolCall)
		}
	}
	sortStreamToolCalls(pending)
	return pending
}

func sortStreamToolCalls(calls []*streamToolCallState) {
	sort.SliceStable(calls, func(i, j int) bool {
		if calls[i].OutputIndex != calls[j].OutputIndex {
			return calls[i].OutputIndex < calls[j].OutputIndex
		}
		return calls[i].ID < calls[j].ID
	})
}

func (r *openAIResponseStreamRunner) emitEndChunk() error {
	return r.emitChunk(&StreamChunk{
		Index:      r.finalIndex + 1,
		Type:       ChunkTypeEnd,
		StopReason: r.responseStopReason,
		Usage:      &r.responseUsage,
		Timestamp:  time.Now(),
	})
}

func (r *openAIResponseStreamRunner) success() (*responses.Response, StopReason, Usage, map[string]any, error) {
	return r.completion, r.responseStopReason, r.responseUsage, r.observer.metadata(), nil
}

func (r *openAIResponseStreamRunner) zeroUsageFailure(err error) (*responses.Response, StopReason, Usage, map[string]any, error) {
	return nil, StopReasonError, Usage{}, r.observer.metadata(), err
}

func (r *openAIResponseStreamRunner) consumeFailure(err error) (*responses.Response, StopReason, Usage, map[string]any, error) {
	if r.preserveUsageOnErrors {
		return nil, StopReasonError, r.responseUsage, r.observer.metadata(), err
	}
	return nil, StopReasonError, Usage{}, r.observer.metadata(), err
}

func (r *openAIResponseStreamRunner) emitChunk(chunk *StreamChunk) error {
	if r.handler == nil {
		return nil
	}
	return r.handler(chunk)
}

func (r *openAIResponseStreamRunner) handleTextDeltaEvent(event any) error {
	ev, ok := event.(responses.ResponseTextDeltaEvent)
	if !ok {
		return nil
	}
	if ev.Delta == "" {
		return nil
	}
	return r.emitChunk(&StreamChunk{
		Index:     r.chunkIndex,
		Type:      ChunkTypeText,
		Text:      ev.Delta,
		Timestamp: time.Now(),
	})
}

func (r *openAIResponseStreamRunner) handleReasoningSummaryDeltaEvent(event any) error {
	switch ev := event.(type) {
	case responses.ResponseReasoningSummaryPartAddedEvent:
		return r.emitReasoningSummaryChunk("\n")
	case responses.ResponseReasoningSummaryTextDeltaEvent:
		return r.emitReasoningSummaryChunk(ev.Delta)
	case responses.ResponseReasoningSummaryDeltaEvent:
		return r.emitReasoningSummaryChunk(reasoningSummaryText(ev.Delta))
	default:
		return nil
	}
}

func (r *openAIResponseStreamRunner) emitReasoningSummaryChunk(text string) error {
	if text == "" {
		return nil
	}
	return r.emitChunk(&StreamChunk{
		Index:     r.chunkIndex,
		Type:      ChunkTypeThought,
		Text:      text,
		Timestamp: time.Now(),
	})
}

func reasoningSummaryText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
	}
	return ""
}

func (r *openAIResponseStreamRunner) handleFunctionCallArgumentsDeltaEvent(event any) error {
	ev, ok := event.(responses.ResponseFunctionCallArgumentsDeltaEvent)
	if !ok {
		return nil
	}
	toolCall := r.ensureToolCallState(ev.ItemID, ev.OutputIndex)
	if err := r.emitToolStart(r.chunkIndex, toolCall); err != nil {
		return err
	}
	if ev.Delta == "" {
		return nil
	}
	return r.emitChunk(&StreamChunk{
		Index: r.chunkIndex,
		Type:  ChunkTypeToolDelta,
		ToolCall: &ToolCallChunk{
			ID:             ev.ItemID,
			ArgumentsDelta: ev.Delta,
		},
		Timestamp: time.Now(),
	})
}

func (r *openAIResponseStreamRunner) handleFunctionCallArgumentsDoneEvent(event any) error {
	ev, ok := event.(responses.ResponseFunctionCallArgumentsDoneEvent)
	if !ok {
		return nil
	}
	toolCall := r.ensureToolCallState(ev.ItemID, ev.OutputIndex)
	if err := r.emitToolStart(r.chunkIndex, toolCall); err != nil {
		return err
	}
	applyToolCallArguments(toolCall, ev.Arguments)
	return nil
}

func (r *openAIResponseStreamRunner) handleOutputItemAddedEvent(event any) error {
	ev, ok := event.(responses.ResponseOutputItemAddedEvent)
	if !ok {
		return nil
	}
	if !isFunctionCallOutputItem(ev.Item.Type) {
		return nil
	}
	toolCall := r.ensureToolCallState(ev.Item.ID, ev.OutputIndex)
	applyToolCallNameChunk(toolCall, ev.Item.Name)
	return r.emitToolStart(r.chunkIndex, toolCall)
}

func (r *openAIResponseStreamRunner) handleOutputItemDoneEvent(event any) error {
	ev, ok := event.(responses.ResponseOutputItemDoneEvent)
	if !ok {
		return nil
	}
	if !isFunctionCallOutputItem(ev.Item.Type) {
		return nil
	}
	toolCall := r.ensureToolCallState(ev.Item.ID, ev.OutputIndex)
	applyToolCallNameChunk(toolCall, ev.Item.Name)
	applyToolCallArguments(toolCall, ev.Item.Arguments)
	return r.emitToolEnd(r.chunkIndex, toolCall)
}

func (r *openAIResponseStreamRunner) handleCompletedEvent(event any) error {
	ev, ok := event.(responses.ResponseCompletedEvent)
	if !ok {
		return nil
	}
	r.completion = &ev.Response
	r.responseUsage = r.provider.convertResponseUsage(ev.Response)
	r.responseStopReason = r.provider.convertResponseStopReason(ev.Response)
	return nil
}

func (r *openAIResponseStreamRunner) handleFailedEvent(event any) error {
	ev, ok := event.(responses.ResponseFailedEvent)
	if !ok {
		return nil
	}
	r.completion = &ev.Response
	r.responseUsage = r.provider.convertResponseUsage(ev.Response)
	r.responseStopReason = StopReasonError
	return nil
}

func (r *openAIResponseStreamRunner) handleIncompleteEvent(event any) error {
	ev, ok := event.(responses.ResponseIncompleteEvent)
	if !ok {
		return nil
	}
	r.completion = &ev.Response
	r.responseUsage = r.provider.convertResponseUsage(ev.Response)
	r.responseStopReason = r.provider.convertIncompleteReason(ev.Response.IncompleteDetails.Reason)
	return nil
}

func (r *openAIResponseStreamRunner) handleResponseErrorEvent(event any) error {
	ev, ok := event.(responses.ResponseErrorEvent)
	if !ok {
		return nil
	}
	if err := r.emitChunk(&StreamChunk{
		Index:     r.chunkIndex,
		Type:      ChunkTypeError,
		Text:      ev.Message,
		Timestamp: time.Now(),
	}); err != nil {
		return err
	}
	r.preserveUsageOnErrors = true
	return fmt.Errorf("openai stream: %s", ev.Message)
}

func isFunctionCallOutputItem(itemType string) bool {
	return itemType == "function_call"
}

func (r *openAIResponseStreamRunner) ensureToolCallState(id string, outputIndex int64) *streamToolCallState {
	toolCall := r.toolCallBuilders[id]
	if toolCall == nil {
		toolCall = &streamToolCallState{
			ToolCallChunk: ToolCallChunk{ID: id},
		}
		r.toolCallBuilders[id] = toolCall
	}
	toolCall.OutputIndex = outputIndex
	return toolCall
}

func (r *openAIResponseStreamRunner) emitToolStart(index int, call *streamToolCallState) error {
	if !r.shouldEmitToolStart(call) {
		return nil
	}
	r.toolCallStarted[call.ID] = true
	toolCopy := call.ToolCallChunk
	return r.emitChunk(&StreamChunk{
		Index:     index,
		Type:      ChunkTypeToolStart,
		ToolCall:  &toolCopy,
		Timestamp: time.Now(),
	})
}

func (r *openAIResponseStreamRunner) shouldEmitToolStart(call *streamToolCallState) bool {
	if call == nil {
		return false
	}
	if call.ID == "" {
		return false
	}
	return !r.toolCallStarted[call.ID]
}

func (r *openAIResponseStreamRunner) emitToolEnd(index int, call *streamToolCallState) error {
	if !r.shouldEmitToolEnd(call) {
		return nil
	}
	r.toolCallEnded[call.ID] = true
	toolCopy := call.ToolCallChunk
	return r.emitChunk(&StreamChunk{
		Index:     index,
		Type:      ChunkTypeToolEnd,
		ToolCall:  &toolCopy,
		Timestamp: time.Now(),
	})
}

func (r *openAIResponseStreamRunner) shouldEmitToolEnd(call *streamToolCallState) bool {
	if call == nil {
		return false
	}
	if call.ID == "" {
		return false
	}
	return !r.toolCallEnded[call.ID]
}

func applyToolCallNameChunk(call *streamToolCallState, name string) {
	if call == nil {
		return
	}
	if strings.TrimSpace(name) == "" {
		return
	}
	call.Name = name
}

func applyToolCallArguments(call *streamToolCallState, arguments string) {
	if call == nil {
		return
	}
	if strings.TrimSpace(arguments) == "" {
		return
	}
	call.ArgumentsDelta = arguments
}

func (p *OpenAIProvider) runResponseStream(
	ctx context.Context,
	req *Request,
	handler StreamHandler,
) (*responses.Response, StopReason, Usage, map[string]any, error) {
	runner := newOpenAIResponseStreamRunner(p, ctx, req, handler)
	return runner.run()
}

func (p *OpenAIProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	return streamViaHandler(ctx, p, req), nil
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

func (p *OpenAIProvider) RequestTimeout() time.Duration {
	return resolveOpenAITimeout(p.config.Timeout)
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
		openaiModelCatalog["gpt-5.4"],
		openaiModelCatalog["gpt-5.4-pro"],
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
	req = ensureProviderRequest(req)
	model := p.resolveRequestModel(req)
	systemPrompt := p.resolveSystemPrompt(req)
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: p.convertResponseMessages(req.Messages, systemPrompt, p.config.AuthMode != openAIAuthModeChatGPT),
		},
	}
	p.applyStore(&params)
	p.applyMaxOutputTokens(&params, req)
	p.applyChatGPTResponseOptions(&params, systemPrompt)
	p.applyTemperature(&params, req, model)
	p.applyTopP(&params, req)
	p.applyReasoning(&params, req, model)
	p.applyTextConfig(&params, req)
	p.applyTruncation(&params, model)
	p.applyInclude(&params, model)
	p.applyTools(&params, req.Tools)
	p.applyParallelToolCalls(&params, req)
	p.applyToolChoice(&params, req.ToolChoice)
	p.applyPromptCache(&params, req, model)
	return params
}

func ensureProviderRequest(req *Request) *Request {
	if req != nil {
		return req
	}
	return &Request{}
}

func (p *OpenAIProvider) resolveRequestModel(req *Request) string {
	model := normalizeOpenAIModel(req.Model)
	if model != "" {
		return ResolveOpenAIModelForAuth(model, p.config.AuthMode)
	}
	return ResolveOpenAIModelForAuth(p.config.Model, p.config.AuthMode)
}

func (p *OpenAIProvider) resolveSystemPrompt(req *Request) string {
	systemPrompt := strings.TrimSpace(req.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = strings.TrimSpace(p.config.SystemPrompt)
	}
	if len(req.Tools) > 0 || req.SkipProviderSkills {
		// Skip prompt-based skill descriptions when native tool definitions
		// are present (prevents double-signaling that causes XML markup
		// leakage) or when the caller has explicitly suppressed skills
		// (e.g. classification requests need a focused prompt).
		return strings.TrimSpace(systemPrompt)
	}
	return appendPromptSkills(systemPrompt, p.skills)
}

func appendPromptSkills(systemPrompt string, promptSkills []skills.Skill) string {
	if len(promptSkills) == 0 {
		return strings.TrimSpace(systemPrompt)
	}
	skillsPrompt := strings.TrimSpace(skills.ToPrompt(promptSkills))
	base := strings.TrimSpace(systemPrompt)
	switch {
	case base == "":
		return skillsPrompt
	case skillsPrompt == "":
		return base
	default:
		return base + "\n" + skillsPrompt
	}
}

func (p *OpenAIProvider) resolveMaxTokens(req *Request) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return p.config.MaxTokens
}

func (p *OpenAIProvider) resolveReasoningEffort(req *Request) string {
	if req.ReasoningEffort != "" {
		return req.ReasoningEffort
	}
	return p.config.ReasoningEffort
}

func (p *OpenAIProvider) applyMaxOutputTokens(params *responses.ResponseNewParams, req *Request) {
	if p.config.AuthMode == openAIAuthModeChatGPT {
		return
	}
	params.MaxOutputTokens = openai.Int(int64(p.resolveMaxTokens(req)))
}

func (p *OpenAIProvider) applyChatGPTResponseOptions(params *responses.ResponseNewParams, systemPrompt string) {
	if p.config.AuthMode != openAIAuthModeChatGPT {
		return
	}
	params.Instructions = openai.String(chatGPTInstructions(systemPrompt))
	params.Store = openai.Bool(false)
}

func chatGPTInstructions(systemPrompt string) string {
	if systemPrompt != "" {
		return systemPrompt
	}
	return "You are Codex, an AI coding agent."
}

func (p *OpenAIProvider) applyTemperature(params *responses.ResponseNewParams, req *Request, model string) {
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
		return
	}
	if !p.shouldApplyDefaultTemperature(model) {
		return
	}
	params.Temperature = openai.Float(p.config.Temperature)
}

func (p *OpenAIProvider) shouldApplyDefaultTemperature(model string) bool {
	if p.config.Temperature <= 0 {
		return false
	}
	return modelSupportsTemperature(model)
}

func (p *OpenAIProvider) applyTopP(params *responses.ResponseNewParams, req *Request) {
	if req.TopP == nil {
		return
	}
	params.TopP = openai.Float(*req.TopP)
}

func (p *OpenAIProvider) applyReasoning(
	params *responses.ResponseNewParams,
	req *Request,
	model string,
) {
	if !supportsOpenAIReasoningConfig(model) {
		return
	}

	reasoningEffort := p.resolveReasoningEffort(req)
	params.Reasoning = shared.ReasoningParam{}

	if summary := resolveOpenAIReasoningSummary(req.ReasoningSummary); summary != "" {
		params.Reasoning.Summary = summary
	} else if strings.TrimSpace(req.ReasoningSummary) == "" {
		params.Reasoning.Summary = shared.ReasoningSummaryAuto
	}

	if reasoningEffort == "xhigh" {
		params.Reasoning.SetExtraFields(map[string]any{"effort": "xhigh"})
		return
	}
	if reasoningEffort != "" {
		params.Reasoning.Effort = shared.ReasoningEffort(reasoningEffort)
	}
}

func resolveOpenAIReasoningSummary(summary string) shared.ReasoningSummary {
	switch strings.ToLower(strings.TrimSpace(summary)) {
	case "auto":
		return shared.ReasoningSummaryAuto
	case "concise":
		return shared.ReasoningSummaryConcise
	case "detailed":
		return shared.ReasoningSummaryDetailed
	default:
		return ""
	}
}

func supportsOpenAIReasoningConfig(model string) bool {
	model = normalizeOpenAIModel(model)
	return strings.HasPrefix(model, "gpt-5") || strings.HasPrefix(model, "o")
}

func (p *OpenAIProvider) applyTextConfig(params *responses.ResponseNewParams, req *Request) {
	verbosity := resolveOpenAIVerbosity(req.Verbosity)
	if verbosity == "" {
		return
	}
	params.Text.SetExtraFields(map[string]any{"verbosity": verbosity})
}

func resolveOpenAIVerbosity(verbosity string) string {
	switch strings.ToLower(strings.TrimSpace(verbosity)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	default:
		return ""
	}
}

func (p *OpenAIProvider) applyTruncation(params *responses.ResponseNewParams, model string) {
	if supportsOpenAITruncationConfig(model, p.config.AuthMode) {
		params.Truncation = responses.ResponseNewParamsTruncationAuto
	}
}

func supportsOpenAITruncationConfig(model string, authMode string) bool {
	if authMode == openAIAuthModeChatGPT {
		return false
	}
	return supportsOpenAIReasoningConfig(model)
}

// applyInclude requests encrypted reasoning content so that reasoning items
// can be round-tripped through multi-turn tool loops. Without this, the API
// returns reasoning summaries but omits the encrypted content that the model
// needs to reconstruct its chain-of-thought on subsequent turns.
func (p *OpenAIProvider) applyInclude(params *responses.ResponseNewParams, model string) {
	if supportsOpenAIReasoningConfig(model) {
		params.Include = append(params.Include, responses.ResponseIncludableReasoningEncryptedContent)
	}
}

func (p *OpenAIProvider) applyStore(params *responses.ResponseNewParams) {
	params.Store = openai.Bool(false)
}

func (p *OpenAIProvider) applyTools(params *responses.ResponseNewParams, tools []Tool) {
	if len(tools) == 0 {
		return
	}
	params.Tools = p.convertResponseTools(tools)
}

func (p *OpenAIProvider) applyParallelToolCalls(params *responses.ResponseNewParams, req *Request) {
	if len(params.Tools) == 0 {
		return
	}
	switch {
	case req.ParallelToolCalls != nil:
		params.ParallelToolCalls = openai.Bool(*req.ParallelToolCalls)
	case req.DisableParallelToolUse:
		params.ParallelToolCalls = openai.Bool(false)
	}
}

func (p *OpenAIProvider) applyToolChoice(params *responses.ResponseNewParams, choice string) {
	if len(params.Tools) == 0 {
		return
	}
	switch strings.TrimSpace(strings.ToLower(choice)) {
	case "any", "required":
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsRequired),
		}
	case "none":
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsNone),
		}
	default:
		// "auto" or empty — let the API default.
	}
}

func (p *OpenAIProvider) applyPromptCache(params *responses.ResponseNewParams, req *Request, model string) {
	key := strings.TrimSpace(req.PromptCacheKey)
	if key != "" {
		params.PromptCacheKey = openai.String(key)
	}
	if !supportsOpenAIPromptCacheRetention(model) {
		return
	}
	if retention := normalizeOpenAIPromptCacheRetention(req.PromptCacheRetention); retention != "" {
		params.SetExtraFields(map[string]any{"prompt_cache_retention": retention})
	}
}

func supportsOpenAIPromptCacheRetention(model string) bool {
	switch normalizeOpenAIModel(model) {
	case string(GPT_5_4_Pro):
		return true
	default:
		return false
	}
}

func normalizeOpenAIPromptCacheRetention(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "in_memory", "in-memory", "in memory":
		return "in_memory"
	case "24h":
		return "24h"
	default:
		return ""
	}
}

func (p *OpenAIProvider) convertResponseMessages(messages []Message, systemPrompt string, includeSystemPrompt bool) responses.ResponseInputParam {
	result := make(responses.ResponseInputParam, 0, len(messages)+1)
	if includeSystemPrompt {
		appendSystemPromptMessage(&result, systemPrompt)
	}
	for _, msg := range messages {
		if !includeSystemPrompt && msg.Role == RoleSystem {
			continue
		}
		appendConvertedMessage(&result, msg)
	}
	return result
}

func appendSystemPromptMessage(result *responses.ResponseInputParam, systemPrompt string) {
	if strings.TrimSpace(systemPrompt) == "" {
		return
	}
	*result = append(*result, responses.ResponseInputItemParamOfMessage(systemPrompt, responses.EasyInputMessageRoleSystem))
}

func appendConvertedMessage(result *responses.ResponseInputParam, msg Message) {
	if msg.Role == RoleTool {
		*result = append(*result, responses.ResponseInputItemParamOfFunctionCallOutput(msg.ToolCallID, msg.Content))
		return
	}
	role, ok := openAIInputRole(msg.Role)
	if !ok {
		return
	}
	// Emit preserved reasoning items before text/tool-call items so the API
	// sees them in output order (reasoning → text → function_call).
	appendReasoningItems(result, msg.Metadata)
	if strings.TrimSpace(msg.Content) != "" {
		*result = append(*result, responses.ResponseInputItemParamOfMessage(msg.Content, role))
	}
	for _, tc := range msg.ToolCalls {
		*result = append(*result, responses.ResponseInputItemParamOfFunctionCall(tc.Arguments, tc.ID, tc.Name))
	}
}

// appendReasoningItems reconstructs ResponseReasoningItemParam entries from
// the metadata snapshot stored by extractOpenAIReasoningItems. This preserves
// the model's chain-of-thought (including encrypted content) across multi-turn
// tool loops, matching the Responses API's expected input format.
func appendReasoningItems(result *responses.ResponseInputParam, metadata map[string]any) {
	if metadata == nil {
		return
	}
	raw, ok := metadata["reasoning_items"]
	if !ok {
		return
	}
	items, ok := raw.([]openAIReasoningItem)
	if !ok {
		return
	}
	for _, ri := range items {
		summaries := make([]responses.ResponseReasoningItemSummaryParam, len(ri.SummaryTexts))
		for j, text := range ri.SummaryTexts {
			summaries[j] = responses.ResponseReasoningItemSummaryParam{Text: text}
		}
		item := responses.ResponseInputItemParamOfReasoning(ri.ID, summaries)
		if ri.EncryptedContent != "" {
			item.OfReasoning.EncryptedContent = openai.Opt(ri.EncryptedContent)
		}
		*result = append(*result, item)
	}
}

func openAIInputRole(role Role) (responses.EasyInputMessageRole, bool) {
	mapped, ok := openAIRoleToInputRole[role]
	return mapped, ok
}

func (p *OpenAIProvider) convertResponseTools(tools []Tool) []responses.ToolUnionParam {
	result := make([]responses.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		switch tool.ResolvedKind() {
		case ToolKindNativeWebSearch:
			result = append(result, openAIWebSearchToolParam(tool))
		default:
			params := sanitizeSchemaIterative(tool.Parameters)
			// Only enable strict mode when the schema is strict-compatible:
			// all properties must be in the required array and
			// additionalProperties must be false. Schemas with optional
			// parameters (required array shorter than properties) cannot
			// use strict mode — OpenAI rejects them at request time.
			strict := isStrictCompatible(params)
			param := responses.ToolParamOfFunction(tool.Name, params, strict)
			if tool.Description != "" {
				desc := openai.String(tool.Description)
				function := param.OfFunction
				function.Description = desc
				param.OfFunction = function
			}
			result = append(result, param)
		}
	}
	return result
}

func openAIWebSearchToolParam(tool Tool) responses.ToolUnionParam {
	param := responses.WebSearchToolParam{
		Type: responses.WebSearchToolTypeWebSearchPreview2025_03_11,
	}
	if tool.WebSearch != nil {
		if size := resolveOpenAIWebSearchContextSize(tool.WebSearch.SearchContextSize); size != "" {
			param.SearchContextSize = size
		}
		if location := openAIWebSearchUserLocation(tool.WebSearch.UserLocation); location != nil {
			param.UserLocation = *location
		}
	}
	return responses.ToolUnionParam{OfWebSearchPreview: &param}
}

func resolveOpenAIWebSearchContextSize(size WebSearchContextSize) responses.WebSearchToolSearchContextSize {
	switch strings.ToLower(strings.TrimSpace(string(size))) {
	case "low":
		return responses.WebSearchToolSearchContextSizeLow
	case "high":
		return responses.WebSearchToolSearchContextSizeHigh
	case "medium":
		return responses.WebSearchToolSearchContextSizeMedium
	default:
		return ""
	}
}

func openAIWebSearchUserLocation(location *WebSearchUserLocation) *responses.WebSearchToolUserLocationParam {
	if location == nil {
		return nil
	}
	if strings.TrimSpace(location.City) == "" &&
		strings.TrimSpace(location.Country) == "" &&
		strings.TrimSpace(location.Region) == "" &&
		strings.TrimSpace(location.Timezone) == "" {
		return nil
	}
	param := &responses.WebSearchToolUserLocationParam{}
	if city := strings.TrimSpace(location.City); city != "" {
		param.City = openai.String(city)
	}
	if country := strings.TrimSpace(location.Country); country != "" {
		param.Country = openai.String(country)
	}
	if region := strings.TrimSpace(location.Region); region != "" {
		param.Region = openai.String(region)
	}
	if timezone := strings.TrimSpace(location.Timezone); timezone != "" {
		param.Timezone = openai.String(timezone)
	}
	return param
}

// isStrictCompatible returns true when the JSON schema is compatible with
// OpenAI's strict function calling mode. Every object node in the schema tree
// must list all of its properties in required and set
// additionalProperties=false. Schemas with optional parameters anywhere in the
// tree must use non-strict mode.
func isStrictCompatible(params map[string]any) bool {
	if params == nil {
		return true
	}
	stack := []map[string]any{params}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if props, _ := node["properties"].(map[string]any); props != nil {
			ap, hasAP := node["additionalProperties"]
			if !hasAP {
				return false
			}
			apBool, ok := ap.(bool)
			if !ok || apBool {
				return false
			}

			required := requiredPropertySet(node["required"])
			for name, child := range props {
				if _, ok := required[name]; !ok {
					return false
				}
				if childMap, ok := child.(map[string]any); ok {
					stack = append(stack, childMap)
				}
			}
		}

		if items, ok := node["items"].(map[string]any); ok {
			stack = append(stack, items)
		}
	}
	return true
}

func requiredPropertySet(raw any) map[string]struct{} {
	if raw == nil {
		return nil
	}
	set := map[string]struct{}{}
	switch values := raw.(type) {
	case []any:
		for _, value := range values {
			name, ok := value.(string)
			if ok && name != "" {
				set[name] = struct{}{}
			}
		}
	case []string:
		for _, name := range values {
			if name != "" {
				set[name] = struct{}{}
			}
		}
	}
	return set
}

func (p *OpenAIProvider) convertResponse(result *responses.Response) *Response {
	if result == nil {
		return &Response{StopReason: StopReasonError}
	}

	metadata := map[string]any{
		"id":           result.ID,
		"status":       string(result.Status),
		"service_tier": string(result.ServiceTier),
	}
	if items := extractOpenAIReasoningItems(result); len(items) > 0 {
		metadata["reasoning_items"] = items
	}

	response := &Response{
		Content:          result.OutputText(),
		Thinking:         extractOpenAIReasoningSummary(result),
		Model:            string(result.Model),
		StopReason:       p.convertResponseStopReason(*result),
		Usage:            p.convertResponseUsage(*result),
		ProviderMetadata: metadata,
	}

	toolCalls := p.extractToolCalls(*result)
	if len(toolCalls) > 0 {
		response.ToolCalls = toolCalls
	}

	return response
}

func extractOpenAIReasoningSummary(result *responses.Response) string {
	if result == nil {
		return ""
	}
	var thinking strings.Builder
	for _, item := range result.Output {
		reasoning, ok := item.AsAny().(responses.ResponseReasoningItem)
		if !ok {
			continue
		}
		for _, summary := range reasoning.Summary {
			thinking.WriteString(summary.Text)
		}
	}
	return strings.TrimSpace(thinking.String())
}

// openAIReasoningItem is a JSON-serializable snapshot of a reasoning output
// item, stored in ProviderMetadata so that appendConvertedMessage can emit
// ResponseReasoningItemParam on subsequent multi-turn requests.
type openAIReasoningItem struct {
	ID               string   `json:"id"`
	SummaryTexts     []string `json:"summary_texts"`
	EncryptedContent string   `json:"encrypted_content,omitempty"`
}

// extractOpenAIReasoningItems collects reasoning output items from a response
// into a serializable slice for round-tripping through Message.Metadata.
func extractOpenAIReasoningItems(result *responses.Response) []openAIReasoningItem {
	if result == nil {
		return nil
	}
	var items []openAIReasoningItem
	for _, item := range result.Output {
		reasoning, ok := item.AsAny().(responses.ResponseReasoningItem)
		if !ok {
			continue
		}
		ri := openAIReasoningItem{ID: reasoning.ID}
		for _, summary := range reasoning.Summary {
			ri.SummaryTexts = append(ri.SummaryTexts, summary.Text)
		}
		if reasoning.EncryptedContent != "" {
			ri.EncryptedContent = reasoning.EncryptedContent
		}
		items = append(items, ri)
	}
	return items
}

func (p *OpenAIProvider) convertResponseUsage(result responses.Response) Usage {
	usage := result.Usage
	return Usage{
		InputTokens:     int(usage.InputTokens),
		OutputTokens:    int(usage.OutputTokens),
		TotalTokens:     int(usage.TotalTokens),
		CacheReadTokens: int(usage.InputTokensDetails.CachedTokens),
		ReasoningTokens: int(usage.OutputTokensDetails.ReasoningTokens),
	}
}

func (p *OpenAIProvider) convertResponseStopReason(result responses.Response) StopReason {
	for _, reason := range candidateStopReasons(result, p) {
		if reason != "" {
			return reason
		}
	}
	return StopReasonEndTurn
}

func candidateStopReasons(result responses.Response, provider *OpenAIProvider) []StopReason {
	reasons := []StopReason{
		stopReasonFromStatus(result),
		stopReasonFromIncomplete(result, provider),
		stopReasonFromError(result),
		stopReasonFromToolCalls(result),
	}
	return reasons
}

func stopReasonFromStatus(result responses.Response) StopReason {
	status := strings.ToLower(string(result.Status))
	if status == "failed" || status == "cancelled" {
		return StopReasonError
	}
	return ""
}

func stopReasonFromIncomplete(result responses.Response, provider *OpenAIProvider) StopReason {
	if result.IncompleteDetails.Reason == "" {
		return ""
	}
	return provider.convertIncompleteReason(result.IncompleteDetails.Reason)
}

func stopReasonFromError(result responses.Response) StopReason {
	if result.Error.Message != "" {
		return StopReasonError
	}
	return ""
}

func stopReasonFromToolCalls(result responses.Response) StopReason {
	if hasFunctionToolCalls(result) {
		return StopReasonToolUse
	}
	return ""
}

func hasFunctionToolCalls(result responses.Response) bool {
	for _, item := range result.Output {
		if item.Type == "function_call" {
			return true
		}
	}
	return false
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

// ResolveOpenAIModelForAuth maps unsupported ChatGPT-account model slugs to a
// supported equivalent while leaving API-key requests untouched.
func ResolveOpenAIModelForAuth(model string, authMode string) string {
	model = normalizeOpenAIModel(model)
	if authMode != openAIAuthModeChatGPT {
		return model
	}
	switch model {
	case string(GPT_5_4_Pro):
		return string(GPT_5_4)
	default:
		return model
	}
}

func modelSupportsTemperature(model string) bool {
	model = normalizeOpenAIModel(model)
	if model == "" {
		return true
	}
	// Reasoning-capable Responses models such as GPT-5 and o-series ignore or
	// reject default temperature controls; leave temperature unset unless the
	// caller explicitly overrides it at request time.
	if strings.HasPrefix(model, "gpt-5") || strings.HasPrefix(model, "o") {
		return false
	}
	return true
}

func (p *OpenAIProvider) selectFallbackModel(requestedModel string, err error) (string, bool) {
	if !isOpenAIModelUnavailableError(err) {
		return "", false
	}
	fallback := normalizeOpenAIModel(p.config.FallbackModel)
	requestedModel = normalizeOpenAIModel(requestedModel)
	if !p.isUsableFallbackModel(fallback, requestedModel) {
		return "", false
	}
	return fallback, true
}

func (p *OpenAIProvider) isUsableFallbackModel(fallback string, requestedModel string) bool {
	if fallback == "" {
		return false
	}
	if fallback == requestedModel {
		return false
	}
	return p.SupportsModel(fallback)
}

func isOpenAIModelUnavailableError(err error) bool {
	apiErr, ok := asOpenAIError(err)
	if !ok {
		return false
	}
	if apiErr.Code == "model_not_found" {
		return true
	}
	return isModelUnavailableByStatus(apiErr.StatusCode, strings.ToLower(apiErr.Message))
}

func isModelUnavailableByStatus(statusCode int, message string) bool {
	switch statusCode {
	case 404:
		return strings.Contains(message, "model")
	case 403:
		return hasAccessAndModelTerms(message)
	default:
		return false
	}
}

func hasAccessAndModelTerms(message string) bool {
	if !strings.Contains(message, "access") {
		return false
	}
	return strings.Contains(message, "model")
}

// sanitizeSchemaIterative walks the JSON schema tree using an explicit stack,
// ensuring every node has a "type" field and every object node has
// "additionalProperties": false. This satisfies OpenAI's strict function
// calling requirements without recursion.
func sanitizeSchemaIterative(params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{"type": "object", "additionalProperties": false}
	}
	stack := []map[string]any{params}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// Infer type if missing.
		if _, hasType := node["type"]; !hasType {
			switch {
			case node["properties"] != nil:
				node["type"] = "object"
			case node["items"] != nil:
				node["type"] = "array"
			default:
				node["type"] = "object"
			}
		}

		// Object nodes require additionalProperties: false and a properties
		// field for strict mode. OpenAI rejects schemas missing properties.
		if node["type"] == "object" {
			node["additionalProperties"] = false
			if _, hasProps := node["properties"]; !hasProps {
				node["properties"] = map[string]any{}
			}
		}

		// Array nodes require an items schema. Add a default if missing.
		if node["type"] == "array" {
			if _, hasItems := node["items"]; !hasItems {
				node["items"] = map[string]any{"type": "object"}
			}
		}

		// Push nested property schemas onto the stack.
		if props, ok := node["properties"].(map[string]any); ok {
			for _, v := range props {
				if child, ok := v.(map[string]any); ok {
					stack = append(stack, child)
				}
			}
		}

		// Push array items schema onto the stack.
		if items, ok := node["items"].(map[string]any); ok {
			stack = append(stack, items)
		}
	}
	return params
}
