package providers

import (
	"fmt"
	"time"
)

// BaseConfig contains configuration common to all providers
type BaseConfig struct {
	// APIKey is the authentication key for the provider
	APIKey string `json:"api_key" yaml:"api_key"`

	// Model is the default model to use
	Model string `json:"model" yaml:"model"`

	// MaxTokens is the default maximum tokens to generate
	MaxTokens int `json:"max_tokens" yaml:"max_tokens"`

	// Temperature is the default sampling temperature (0.0-1.0)
	Temperature float64 `json:"temperature" yaml:"temperature"`

	// Timeout for API requests
	Timeout time.Duration `json:"timeout" yaml:"timeout"`

	// MaxRetries for transient failures
	MaxRetries int `json:"max_retries" yaml:"max_retries"`

	// RetryBaseDelay for exponential backoff
	RetryBaseDelay time.Duration `json:"retry_base_delay" yaml:"retry_base_delay"`

	// RetryMaxDelay caps the backoff
	RetryMaxDelay time.Duration `json:"retry_max_delay" yaml:"retry_max_delay"`
}

// DefaultBaseConfig returns sensible defaults
func DefaultBaseConfig() BaseConfig {
	return BaseConfig{
		MaxTokens:      4096,
		Temperature:    0.7,
		Timeout:        5 * time.Minute,
		MaxRetries:     3,
		RetryBaseDelay: 1 * time.Second,
		RetryMaxDelay:  30 * time.Second,
	}
}

// Validate checks the base configuration
func (c *BaseConfig) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("api_key is required")
	}
	if c.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be positive")
	}
	if c.Temperature < 0 || c.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	return nil
}

// AnthropicConfig contains Anthropic-specific configuration
type AnthropicConfig struct {
	BaseConfig `json:",inline" yaml:",inline"`

	// BaseURL overrides the default API endpoint
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty"`

	// APIVersion is the Anthropic API version header
	APIVersion string `json:"api_version,omitempty" yaml:"api_version,omitempty"`

	// EnableCaching enables prompt caching (beta feature)
	EnableCaching bool `json:"enable_caching" yaml:"enable_caching"`

	// BetaFeatures to enable (e.g., "prompt-caching-2024-07-31")
	BetaFeatures []string `json:"beta_features,omitempty" yaml:"beta_features,omitempty"`

	SystemPrompt string `json:"system_prompt" yaml:"system_prompt"`

	ThinkingBudget int `json:"thinking_budget" yaml:"thinking_budget"`
}

// DefaultAnthropicConfig returns Anthropic defaults
func DefaultAnthropicConfig() AnthropicConfig {
	base := DefaultBaseConfig()
	base.Model = "claude-sonnet-4-5-20250901" // Claude Sonnet 4.5 with 1M token window
	base.MaxTokens = 8192

	return AnthropicConfig{
		BaseConfig: base,
		APIVersion: "2023-06-01",
	}
}

// Validate checks Anthropic-specific configuration
func (c *AnthropicConfig) Validate() error {
	if err := c.BaseConfig.Validate(); err != nil {
		return fmt.Errorf("anthropic config: %w", err)
	}
	return nil
}

// OpenAIConfig contains OpenAI-specific configuration
type OpenAIConfig struct {
	BaseConfig `json:",inline" yaml:",inline"`

	// BaseURL overrides the default API endpoint (for Azure, proxies, etc.)
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty"`

	// Organization ID for OpenAI
	Organization string `json:"organization,omitempty" yaml:"organization,omitempty"`

	// Project ID for OpenAI
	Project string `json:"project,omitempty" yaml:"project,omitempty"`

	// AzureDeployment for Azure OpenAI Service
	AzureDeployment string `json:"azure_deployment,omitempty" yaml:"azure_deployment,omitempty"`

	// AzureAPIVersion for Azure OpenAI Service
	AzureAPIVersion string `json:"azure_api_version,omitempty" yaml:"azure_api_version,omitempty"`

	// FrequencyPenalty reduces repetition (-2.0 to 2.0)
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty" yaml:"frequency_penalty,omitempty"`

	// PresencePenalty encourages new topics (-2.0 to 2.0)
	PresencePenalty *float64 `json:"presence_penalty,omitempty" yaml:"presence_penalty,omitempty"`

	// ResponseFormat for structured output ("json_object", "json_schema")
	ResponseFormat string `json:"response_format,omitempty" yaml:"response_format,omitempty"`

	ReasoningEffort string `json:"reasoning_effort,omitempty" yaml:"reasoning_effort,omitempty"`

	SystemPrompt string `json:"system_prompt" yaml:"system_prompt"`
}

// DefaultOpenAIConfig returns OpenAI defaults
func DefaultOpenAIConfig() OpenAIConfig {
	base := DefaultBaseConfig()
	base.Model = "codex-5-2-20250901" // OpenAI 5.2 Codex
	base.MaxTokens = 8192

	return OpenAIConfig{
		BaseConfig:      base,
		ReasoningEffort: "xhigh",
	}
}

// Validate checks OpenAI-specific configuration
func (c *OpenAIConfig) Validate() error {
	if err := c.BaseConfig.Validate(); err != nil {
		return fmt.Errorf("openai config: %w", err)
	}
	if c.FrequencyPenalty != nil && (*c.FrequencyPenalty < -2 || *c.FrequencyPenalty > 2) {
		return fmt.Errorf("openai config: frequency_penalty must be between -2 and 2")
	}
	if c.PresencePenalty != nil && (*c.PresencePenalty < -2 || *c.PresencePenalty > 2) {
		return fmt.Errorf("openai config: presence_penalty must be between -2 and 2")
	}
	if c.ReasoningEffort != "" {
		switch c.ReasoningEffort {
		case "low", "medium", "high", "xhigh":
		default:
			return fmt.Errorf("openai config: reasoning_effort must be low, medium, high, or xhigh")
		}
	}
	return nil
}

// GoogleConfig contains Google/Gemini-specific configuration
type GoogleConfig struct {
	BaseConfig `json:",inline" yaml:",inline"`

	// ProjectID for Vertex AI (optional, uses Gemini API if not set)
	ProjectID string `json:"project_id,omitempty" yaml:"project_id,omitempty"`

	// Location for Vertex AI (e.g., "us-central1")
	Location string `json:"location,omitempty" yaml:"location,omitempty"`

	// UseVertexAI switches from Gemini API to Vertex AI
	UseVertexAI bool `json:"use_vertex_ai" yaml:"use_vertex_ai"`

	// SafetySettings configures content filtering
	SafetySettings []SafetySetting `json:"safety_settings,omitempty" yaml:"safety_settings,omitempty"`

	// TopK for sampling (1-40)
	TopK *int `json:"top_k,omitempty" yaml:"top_k,omitempty"`

	SystemPrompt string `json:"system_prompt" yaml:"system_prompt"`
}

// SafetySetting configures content filtering for a category
type SafetySetting struct {
	Category  string `json:"category" yaml:"category"`
	Threshold string `json:"threshold" yaml:"threshold"`
}

// DefaultGoogleConfig returns Google/Gemini defaults
func DefaultGoogleConfig() GoogleConfig {
	base := DefaultBaseConfig()
	base.Model = string(Gemini3Pro) // Google Gemini 3 Pro
	base.MaxTokens = 8192

	return GoogleConfig{
		BaseConfig: base,
		Location:   "us-central1",
	}
}

// Validate checks Google-specific configuration
func (c *GoogleConfig) Validate() error {
	if err := c.BaseConfig.Validate(); err != nil {
		return fmt.Errorf("google config: %w", err)
	}
	if c.UseVertexAI && c.ProjectID == "" {
		return fmt.Errorf("google config: project_id required for Vertex AI")
	}
	if c.TopK != nil && (*c.TopK < 1 || *c.TopK > 40) {
		return fmt.Errorf("google config: top_k must be between 1 and 40")
	}
	return nil
}

// ProviderType identifies the provider
type ProviderType string

const (
	ProviderTypeAnthropic ProviderType = "anthropic"
	ProviderTypeOpenAI    ProviderType = "openai"
	ProviderTypeGoogle    ProviderType = "google"
)

// ConfigLoader provides an interface for loading provider configs
type ConfigLoader interface {
	// LoadAnthropicConfig loads Anthropic configuration
	LoadAnthropicConfig() (*AnthropicConfig, error)

	// LoadOpenAIConfig loads OpenAI configuration
	LoadOpenAIConfig() (*OpenAIConfig, error)

	// LoadGoogleConfig loads Google configuration
	LoadGoogleConfig() (*GoogleConfig, error)
}
