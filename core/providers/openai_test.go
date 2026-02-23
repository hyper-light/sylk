package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/oauth"
	promptskills "github.com/adalundhe/sylk/skills"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
)

func TestDefaultOpenAIConfig_UsesCodex53WithFallback(t *testing.T) {
	cfg := DefaultOpenAIConfig()
	if cfg.Model != "gpt-5.3-codex" {
		t.Fatalf("expected default model gpt-5.3-codex, got %q", cfg.Model)
	}
	if cfg.FallbackModel != "gpt-5.2-codex" {
		t.Fatalf("expected default fallback model gpt-5.2-codex, got %q", cfg.FallbackModel)
	}
	if cfg.AuthMode != "api_key" {
		t.Fatalf("expected default auth mode api_key, got %q", cfg.AuthMode)
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
		{"gpt-5.3-codex", "gpt-5.3-codex"},
		{"codex-5.3", "gpt-5.3-codex"},
		{"codex-5.2", "gpt-5.2-codex"},
		{"codex-5-2-20250901", "gpt-5.2-codex"},
		{"GPT-5-3-CODEX", "gpt-5.3-codex"},
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

	if !p.SupportsModel("gpt-5.3-codex") {
		t.Fatal("expected gpt-5.3-codex to be supported")
	}
	if !p.SupportsModel("codex-5.2") {
		t.Fatal("expected codex-5.2 alias to be supported")
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
				Model:     "codex-5.2",
				MaxTokens: 1024,
			},
		},
	}

	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})

	if got := string(params.Model); got != "gpt-5.2-codex" {
		t.Fatalf("expected normalized model gpt-5.2-codex, got %q", got)
	}
}

func TestOpenAIProviderBuildResponseParams_OmitsDefaultTemperatureForCodex(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:       "gpt-5.3-codex",
				MaxTokens:   1024,
				Temperature: 0.7,
			},
		},
	}

	params := p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if params.Temperature.Valid() {
		t.Fatal("expected default temperature to be omitted for codex model")
	}

	p.config.Model = "gpt-4o"
	params = p.buildResponseParams(&Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if !params.Temperature.Valid() {
		t.Fatal("expected default temperature to be set for non-codex model")
	}
}

func TestOpenAIProviderBuildResponseParams_ChatGPTSetsInstructions(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			BaseConfig: BaseConfig{
				Model:     "gpt-5.3-codex",
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
				Model:     "gpt-5.3-codex",
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
				Model:     "gpt-5.3-codex",
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

func TestSelectFallbackModel(t *testing.T) {
	p := &OpenAIProvider{
		config: OpenAIConfig{
			FallbackModel: "gpt-5.2-codex",
		},
	}

	err := &openai.Error{
		StatusCode: 404,
		Code:       "model_not_found",
		Message:    "The model gpt-5.3-codex does not exist",
	}

	fallback, ok := p.selectFallbackModel("gpt-5.3-codex", err)
	if !ok {
		t.Fatal("expected fallback model selection to succeed")
	}
	if fallback != "gpt-5.2-codex" {
		t.Fatalf("expected fallback gpt-5.2-codex, got %q", fallback)
	}

	if _, ok := p.selectFallbackModel("gpt-5.2-codex", err); ok {
		t.Fatal("expected no fallback when requested model already equals fallback model")
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
	provider, err := NewOpenAIProviderWithAuthService(OpenAIConfig{
		BaseConfig: BaseConfig{
			Model:     "gpt-5.3-codex",
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
			Model:     "gpt-5.3-codex",
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
			Model:     "gpt-5.3-codex",
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
