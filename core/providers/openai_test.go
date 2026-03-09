package providers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/oauth"
	promptskills "github.com/adalundhe/sylk/skills"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

func TestDefaultOpenAIConfig_UsesGPT54Pro(t *testing.T) {
	cfg := DefaultOpenAIConfig()
	if cfg.Model != "gpt-5.4-pro" {
		t.Fatalf("expected default model gpt-5.4-pro, got %q", cfg.Model)
	}
	if cfg.Timeout != defaultOpenAIRequestTimeout {
		t.Fatalf("expected default timeout %v, got %v", defaultOpenAIRequestTimeout, cfg.Timeout)
	}
	if cfg.FallbackModel != "" {
		t.Fatalf("expected no fallback model, got %q", cfg.FallbackModel)
	}
	if cfg.AuthMode != "api_key" {
		t.Fatalf("expected default auth mode api_key, got %q", cfg.AuthMode)
	}
}

func TestResolveOpenAITimeout_UsesOpenAISpecificDefault(t *testing.T) {
	if got := resolveOpenAITimeout(0); got != defaultOpenAIRequestTimeout {
		t.Fatalf("resolveOpenAITimeout(0) = %v, want %v", got, defaultOpenAIRequestTimeout)
	}
}

func TestOpenAIConfigValidate_AuthMode(t *testing.T) {
	cfg := DefaultOpenAIConfig()
	cfg.APIKey = "test-key"

	cfg.AuthMode = "chatgpt"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected chatgpt auth mode to validate, got error: %v", err)
	}

	cfg.AuthMode = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid auth mode to fail validation")
	}
}

func TestNormalizeOpenAIModel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"gpt-5.4-pro", "gpt-5.4-pro"},
		{"gpt-5-4-pro", "gpt-5.4-pro"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := normalizeOpenAIModel(tt.in); got != tt.want {
			t.Fatalf("normalizeOpenAIModel(%q): got %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOpenAIProviderSupportsModel(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{},
	}

	if !p.SupportsModel("gpt-5.4-pro") {
		t.Fatal("expected gpt-5.4-pro to be supported")
	}
	if !p.SupportsModel("gpt-5-4-pro") {
		t.Fatal("expected gpt-5-4-pro alias to be supported")
	}
	if p.SupportsModel("unknown-model") {
		t.Fatal("expected unknown-model to be unsupported by default")
	}

	p.config.AllowUnknownModels = true
	if !p.SupportsModel("unknown-model") {
		t.Fatal("expected unknown-model to be supported when allow_unknown_models is enabled")
	}
}

func TestOpenAIProviderBuildResponseParams_NormalizesModel(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5-4-pro",
				MaxTokens: 1024,
			},
		},
	}

	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})

	if got := string(params.Model); got != "gpt-5.4-pro" {
		t.Fatalf("expected normalized model gpt-5.4-pro, got %q", got)
	}
}

func TestOpenAIProviderBuildResponseParams_OmitsTemperatureForThinkingModel(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:       "gpt-5.4-pro",
				MaxTokens:   1024,
				Temperature: 0.7,
			},
		},
	}

	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if params.Temperature.Valid() {
		t.Fatal("expected temperature to be omitted for thinking model")
	}
}

func TestOpenAIProviderBuildResponseParams_AppliesTemperatureForNonReasoningModel(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:       "gpt-4.1",
				MaxTokens:   1024,
				Temperature: 0.7,
			},
		},
	}

	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if !params.Temperature.Valid() {
		t.Fatal("expected temperature to be set for non-reasoning model")
	}
	if params.Temperature.Value != 0.7 {
		t.Fatalf("temperature = %v, want 0.7", params.Temperature.Value)
	}
}

func TestOpenAIProviderBuildResponseParams_ReasoningSummaryNoneOmitsSummary(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.4-pro",
				MaxTokens: 1024,
			},
			ReasoningEffort: "high",
		},
	}

	params := p.buildResponseParams(&Request{
		Messages:         []Message{{Role: RoleUser, Content: "hello"}},
		ReasoningSummary: "none",
	})

	if params.Reasoning.Summary != "" {
		t.Fatalf("expected reasoning summary to be omitted, got %q", params.Reasoning.Summary)
	}
	if params.Reasoning.Effort != shared.ReasoningEffortHigh {
		t.Fatalf("expected reasoning effort high, got %q", params.Reasoning.Effort)
	}
}

func TestOpenAIProviderBuildResponseParams_AppliesTextAndPromptCacheControls(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.4-pro",
				MaxTokens: 1024,
			},
		},
	}

	params := p.buildResponseParams(&Request{
		Messages:             []Message{{Role: RoleUser, Content: "hello"}},
		Verbosity:            "low",
		PromptCacheKey:       "guardian:session-123",
		PromptCacheRetention: "24h",
	})

	if !params.PromptCacheKey.Valid() {
		t.Fatal("expected prompt cache key to be set")
	}
	if params.PromptCacheKey.Value != "guardian:session-123" {
		t.Fatalf("unexpected prompt cache key: %q", params.PromptCacheKey.Value)
	}

	body := marshalResponseNewParams(t, params)
	textConfig, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text config in marshaled params, got %#v", body["text"])
	}
	if textConfig["verbosity"] != "low" {
		t.Fatalf("expected verbosity low, got %#v", textConfig["verbosity"])
	}
	if body["prompt_cache_retention"] != "24h" {
		t.Fatalf("expected 24h prompt cache retention, got %#v", body["prompt_cache_retention"])
	}
}

func TestOpenAIProviderBuildResponseParams_NormalizesLegacyPromptCacheRetention(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.4-pro",
				MaxTokens: 1024,
			},
		},
	}

	params := p.buildResponseParams(&Request{
		Messages:             []Message{{Role: RoleUser, Content: "hello"}},
		PromptCacheKey:       "architect:session-123",
		PromptCacheRetention: "in-memory",
	})

	body := marshalResponseNewParams(t, params)
	if body["prompt_cache_retention"] != "in_memory" {
		t.Fatalf("expected normalized in_memory prompt cache retention, got %#v", body["prompt_cache_retention"])
	}
}

func TestOpenAIProviderBuildResponseParams_AppliesParallelToolControls(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.4-pro",
				MaxTokens: 1024,
			},
		},
	}

	parallel := true
	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
		Tools: []Tool{{
			Name:        "check_status",
			Description: "Check status",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}},
		ParallelToolCalls: &parallel,
	})

	if !params.ParallelToolCalls.Valid() {
		t.Fatal("expected parallel_tool_calls to be set")
	}
	if !params.ParallelToolCalls.Value {
		t.Fatal("expected parallel_tool_calls=true")
	}

	params = p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
		Tools: []Tool{{
			Name:        "check_status",
			Description: "Check status",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}},
		DisableParallelToolUse: true,
	})

	if !params.ParallelToolCalls.Valid() {
		t.Fatal("expected parallel_tool_calls to be set when parallel tool use is disabled")
	}
	if params.ParallelToolCalls.Value {
		t.Fatal("expected parallel_tool_calls=false when DisableParallelToolUse is set")
	}
}

func TestOpenAIProviderBuildResponseParams_IncludesNativeWebSearchTool(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.4-pro",
				MaxTokens: 1024,
			},
		},
	}

	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "research Go error handling"}},
		Tools: []Tool{
			{
				Kind: ToolKindNativeWebSearch,
				Name: "web_search",
				WebSearch: &WebSearchOptions{
					SearchContextSize: WebSearchContextSizeHigh,
					UserLocation: &WebSearchUserLocation{
						Country:  "US",
						Timezone: "America/Chicago",
					},
				},
			},
			{
				Name:        "web_fetch",
				Description: "Fetch a URL",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{"type": "string"},
					},
					"required":             []string{"url"},
					"additionalProperties": false,
				},
			},
		},
	})

	body := marshalResponseNewParams(t, params)
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected 2 tools in marshaled params, got %#v", body["tools"])
	}

	first, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first tool object, got %#v", tools[0])
	}
	if first["type"] != "web_search_preview_2025_03_11" {
		t.Fatalf("expected native web search tool type, got %#v", first["type"])
	}
	if first["search_context_size"] != "high" {
		t.Fatalf("expected high search context size, got %#v", first["search_context_size"])
	}
	userLocation, ok := first["user_location"].(map[string]any)
	if !ok {
		t.Fatalf("expected user_location object, got %#v", first["user_location"])
	}
	if userLocation["country"] != "US" {
		t.Fatalf("expected country US, got %#v", userLocation["country"])
	}
	if userLocation["timezone"] != "America/Chicago" {
		t.Fatalf("expected timezone America/Chicago, got %#v", userLocation["timezone"])
	}
}

func marshalResponseNewParams(t *testing.T, params responses.ResponseNewParams) map[string]any {
	t.Helper()

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return body
}

func TestOpenAIProviderBuildResponseParams_ChatGPTSetsInstructions(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.4-pro",
				MaxTokens: 256,
			},
			AuthMode:     openAIAuthModeChatGPT,
			SystemPrompt: "You are a strict coding assistant.",
		},
	}

	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if !params.Instructions.Valid() {
		t.Fatal("expected instructions to be set in chatgpt mode")
	}
	if params.Instructions.Value != "You are a strict coding assistant." {
		t.Fatalf("unexpected instructions: %q", params.Instructions.Value)
	}
	if !params.Store.Valid() {
		t.Fatal("expected store to be set in chatgpt mode")
	}
	if params.Store.Value {
		t.Fatal("expected store=false in chatgpt mode")
	}
	if params.MaxOutputTokens.Valid() {
		t.Fatal("expected max_output_tokens to be omitted in chatgpt mode")
	}
}

func TestOpenAIProviderBuildResponseParams_ChatGPTFallbackInstructions(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.4-pro",
				MaxTokens: 256,
			},
			AuthMode: openAIAuthModeChatGPT,
		},
	}

	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if !params.Instructions.Valid() {
		t.Fatal("expected default instructions to be set in chatgpt mode")
	}
	if params.Instructions.Value == "" {
		t.Fatal("expected non-empty default instructions")
	}
}

func TestOpenAIProviderResolveSystemPrompt_AppendsSkills(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.4-pro",
				MaxTokens: 256,
			},
			SystemPrompt: "Base prompt",
		},
		skills: []promptskills.Skill{
			{
				Name:        "self-diagnostic",
				Description: "Explain what failed and how to recover.",
				Path:        "agents/guide/skillfiles/self-diagnostic/SKILL.md",
			},
		},
	}

	got := p.resolveSystemPrompt(&Request{})
	if !strings.Contains(got, "Base prompt") {
		t.Fatalf("expected system prompt to contain base prompt: %q", got)
	}
	if !strings.Contains(got, "<available_skills>") {
		t.Fatalf("expected system prompt to contain skills envelope: %q", got)
	}
	if !strings.Contains(got, "self-diagnostic") {
		t.Fatalf("expected system prompt to contain skill name: %q", got)
	}
}

func TestSelectFallbackModel_NoFallbackConfigured(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{},
	}

	err := &openai.Error{
		StatusCode: 404,
		Code:       "model_not_found",
		Message:    "The model gpt-5.4-pro does not exist",
	}

	if _, ok := p.selectFallbackModel("gpt-5.4-pro", err); ok {
		t.Fatal("expected no fallback when no fallback model is configured")
	}
}

type mockAuthService struct {
	auth           *oauth.OpenAIChatGPTAuth
	err            error
	beginChallenge *oauth.DeviceCodeChallenge
	beginErr       error
	completeAuth   *oauth.OpenAIChatGPTAuth
	completeErr    error
	completeDelay  time.Duration
	saveErr        error
	saveCalls      int
}

func (m *mockAuthService) BeginDeviceAuth(context.Context) (*oauth.DeviceCodeChallenge, error) {
	if m.beginErr != nil {
		return nil, m.beginErr
	}
	return m.beginChallenge, nil
}

func (m *mockAuthService) CompleteDeviceAuth(ctx context.Context, _ *oauth.DeviceCodeChallenge, _ time.Duration) (*oauth.OpenAIChatGPTAuth, error) {
	if m.completeDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.completeDelay):
		}
	}
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return m.completeAuth, nil
}

func (m *mockAuthService) Refresh(context.Context, *oauth.OpenAIChatGPTAuth) (*oauth.OpenAIChatGPTAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthService) Resolve(context.Context) (*oauth.OpenAIChatGPTAuth, error) {
	return m.auth, m.err
}

func (m *mockAuthService) Save(context.Context, *oauth.OpenAIChatGPTAuth) error {
	m.saveCalls++
	return m.saveErr
}

func (m *mockAuthService) Load(context.Context) (*oauth.OpenAIChatGPTAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthService) Delete(context.Context) error {
	return errors.New("not implemented")
}

func TestOpenAIProviderStartChatGPTDeviceAuth_ReturnsChallengeImmediately(t *testing.T) {
	authSvc := &mockAuthService{
		beginChallenge: &oauth.DeviceCodeChallenge{
			VerificationURL: "https://auth.openai.com/codex/device",
			UserCode:        "ABCD-1234",
			DeviceAuthID:    "device_abc",
			PollInterval:    time.Second,
		},
		completeAuth: &oauth.OpenAIChatGPTAuth{
			AuthMode:         "chatgpt",
			AccessToken:      "new_token",
			ChatGPTAccountID: "org_new",
		},
		completeDelay: 120 * time.Millisecond,
	}
	provider, err := NewOpenAIProviderWithAuthService(context.Background(), OpenAIConfig{
		BaseConfig: BaseConfig{
			Model:     "gpt-5.4-pro",
			APIKey:    "old_token",
			MaxTokens: 1024,
		},
		AuthMode:           openAIAuthModeChatGPT,
		ChatGPTAccountID:   "org_old",
		AllowUnknownModels: true,
	}, authSvc)
	if err != nil {
		t.Fatalf("NewOpenAIProviderWithAuthService() error: %v", err)
	}

	start := time.Now()
	session, err := provider.StartChatGPTDeviceAuth(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("StartChatGPTDeviceAuth() error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("StartChatGPTDeviceAuth() should return immediately, took %v", elapsed)
	}
	if session == nil || session.Challenge == nil {
		t.Fatal("expected non-nil session challenge")
	}
	if session.Challenge.UserCode != "ABCD-1234" {
		t.Fatalf("unexpected user code: %q", session.Challenge.UserCode)
	}

	select {
	case result := <-session.Results:
		if result.Err != nil {
			t.Fatalf("unexpected async auth error: %v", result.Err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for device auth completion")
	}

	if provider.config.APIKey != "new_token" {
		t.Fatalf("expected provider API key update, got %q", provider.config.APIKey)
	}
	if provider.config.ChatGPTAccountID != "org_new" {
		t.Fatalf("expected provider account update, got %q", provider.config.ChatGPTAccountID)
	}
	if authSvc.saveCalls != 1 {
		t.Fatalf("expected auth Save() call, got %d", authSvc.saveCalls)
	}
}

func TestHydrateOpenAIConfig_ChatGPTFromAuthService(t *testing.T) {
	cfg := OpenAIConfig{
		BaseConfig: BaseConfig{
			Model:     "gpt-5.4-pro",
			MaxTokens: 1024,
		},
		AuthMode: openAIAuthModeChatGPT,
	}
	authSvc := &mockAuthService{
		auth: &oauth.OpenAIChatGPTAuth{
			AccessToken:      "chatgpt_token",
			ChatGPTAccountID: "org_123",
		},
	}

	if err := hydrateOpenAIConfig(context.Background(), &cfg, authSvc); err != nil {
		t.Fatalf("hydrateOpenAIConfig() error: %v", err)
	}
	if cfg.APIKey != "chatgpt_token" {
		t.Fatalf("expected APIKey from auth service, got %q", cfg.APIKey)
	}
	if cfg.ChatGPTAccountID != "org_123" {
		t.Fatalf("expected ChatGPTAccountID from auth service, got %q", cfg.ChatGPTAccountID)
	}
}

func TestHydrateOpenAIConfig_ChatGPTRequiresAccountID(t *testing.T) {
	cfg := OpenAIConfig{
		BaseConfig: BaseConfig{
			Model:     "gpt-5.4-pro",
			MaxTokens: 1024,
			APIKey:    "chatgpt_token_only",
		},
		AuthMode: openAIAuthModeChatGPT,
	}
	authSvc := &mockAuthService{err: oauth.ErrAuthNotConfigured}

	err := hydrateOpenAIConfig(context.Background(), &cfg, authSvc)
	if err == nil {
		t.Fatal("expected error when chatgpt_account_id is missing")
	}
}

func TestConvertResponseStopReason_ToolUse(t *testing.T) {
	p := &OpenAIProvider{}
	result := responses.Response{
		Status: responses.ResponseStatusCompleted,
		Output: []responses.ResponseOutputItemUnion{
			{
				ID:   "tool_1",
				Type: "function_call",
				Name: "run_tests",
			},
		},
	}
	if got := p.convertResponseStopReason(result); got != StopReasonToolUse {
		t.Fatalf("expected stop reason %q, got %q", StopReasonToolUse, got)
	}
}
