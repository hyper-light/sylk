package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ProviderConfig holds configuration for a single LLM provider.
type ProviderConfig struct {
	// Provider is the provider identifier (e.g., "anthropic", "openai", "google")
	Provider string `yaml:"provider"`

	// APIKey is the authentication key - never serialized to YAML
	APIKey string `yaml:"-"`

	// BaseURL overrides the default API endpoint (optional)
	BaseURL string `yaml:"base_url,omitempty"`

	// Models maps model aliases to actual model identifiers
	Models map[string]string `yaml:"models,omitempty"`

	// SoftLimit defines usage limits that trigger warnings
	SoftLimit *UsageLimit `yaml:"soft_limit,omitempty"`

	// Enabled indicates if this provider is active
	Enabled bool `yaml:"enabled"`
}

// UsageLimit defines rate limiting thresholds for a provider.
type UsageLimit struct {
	// Requests is the maximum number of requests allowed
	Requests int `yaml:"requests,omitempty"`

	// Tokens is the maximum number of tokens allowed
	Tokens int `yaml:"tokens,omitempty"`

	// Period is the time window for the limits
	Period time.Duration `yaml:"period,omitempty"`

	// WarnAt is the percentage threshold to trigger warnings (0.0-1.0)
	WarnAt float64 `yaml:"warn_at,omitempty"`
}

// Config holds the complete LLM configuration.
type Config struct {
	// Providers maps provider names to their configurations
	Providers map[string]*ProviderConfig `yaml:"providers"`

	// DefaultProvider is the provider to use when none is specified
	DefaultProvider string `yaml:"default_provider,omitempty"`
}

// Validate checks if the provider configuration is valid.
func (c *ProviderConfig) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("provider name is required")
	}
	if c.SoftLimit != nil {
		if err := c.SoftLimit.Validate(); err != nil {
			return fmt.Errorf("soft_limit: %w", err)
		}
	}
	return nil
}

// Validate checks if the usage limit configuration is valid.
func (u *UsageLimit) Validate() error {
	if u.Requests < 0 {
		return fmt.Errorf("requests must be non-negative")
	}
	if u.Tokens < 0 {
		return fmt.Errorf("tokens must be non-negative")
	}
	if u.WarnAt < 0 || u.WarnAt > 1 {
		return fmt.Errorf("warn_at must be between 0 and 1")
	}
	return nil
}

// Validate checks if the complete configuration is valid.
func (c *Config) Validate() error {
	for name, provider := range c.Providers {
		if err := provider.Validate(); err != nil {
			return fmt.Errorf("provider %s: %w", name, err)
		}
	}
	if c.DefaultProvider != "" {
		if _, ok := c.Providers[c.DefaultProvider]; !ok {
			return fmt.Errorf("default_provider %q not found in providers", c.DefaultProvider)
		}
	}
	return nil
}

// DefaultConfigPath returns the default path for the config file.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sylk", "config.yaml")
}

// LoadConfig loads configuration from the specified path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// LoadConfigFromDefaultPath loads configuration from ~/.sylk/config.yaml.
func LoadConfigFromDefaultPath() (*Config, error) {
	path := DefaultConfigPath()
	if path == "" {
		return nil, fmt.Errorf("could not determine home directory")
	}
	return LoadConfig(path)
}

// NewDefaultConfig creates a new configuration with sensible defaults.
func NewDefaultConfig() *Config {
	return &Config{
		Providers: make(map[string]*ProviderConfig),
	}
}
