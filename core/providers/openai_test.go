package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/oauth"
	"github.com/openai/openai-go"
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
	auth *oauth.OpenAIChatGPTAuth
	err  error
}

func (m *mockAuthService) BeginDeviceAuth(context.Context) (*oauth.DeviceCodeChallenge, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthService) CompleteDeviceAuth(context.Context, *oauth.DeviceCodeChallenge, time.Duration) (*oauth.OpenAIChatGPTAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthService) Refresh(context.Context, *oauth.OpenAIChatGPTAuth) (*oauth.OpenAIChatGPTAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthService) Resolve(context.Context) (*oauth.OpenAIChatGPTAuth, error) {
	return m.auth, m.err
}

func (m *mockAuthService) Save(context.Context, *oauth.OpenAIChatGPTAuth) error {
	return errors.New("not implemented")
}

func (m *mockAuthService) Load(context.Context) (*oauth.OpenAIChatGPTAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthService) Delete(context.Context) error {
	return errors.New("not implemented")
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
