package llm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageLimitValidate(t *testing.T) {
	tests := []struct {
		name    string
		limit   *UsageLimit
		wantErr bool
	}{
		{
			name:    "valid limit",
			limit:   &UsageLimit{Requests: 100, Tokens: 10000, Period: time.Hour, WarnAt: 0.8},
			wantErr: false,
		},
		{
			name:    "zero values valid",
			limit:   &UsageLimit{Requests: 0, Tokens: 0, Period: 0, WarnAt: 0},
			wantErr: false,
		},
		{
			name:    "warn_at at 1.0 valid",
			limit:   &UsageLimit{WarnAt: 1.0},
			wantErr: false,
		},
		{
			name:    "negative requests",
			limit:   &UsageLimit{Requests: -1},
			wantErr: true,
		},
		{
			name:    "negative tokens",
			limit:   &UsageLimit{Tokens: -1},
			wantErr: true,
		},
		{
			name:    "warn_at below 0",
			limit:   &UsageLimit{WarnAt: -0.1},
			wantErr: true,
		},
		{
			name:    "warn_at above 1",
			limit:   &UsageLimit{WarnAt: 1.1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.limit.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("UsageLimit.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProviderConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &ProviderConfig{
				Provider: "anthropic",
				Enabled:  true,
			},
			wantErr: false,
		},
		{
			name: "valid with soft limit",
			config: &ProviderConfig{
				Provider:  "openai",
				SoftLimit: &UsageLimit{Requests: 100, WarnAt: 0.8},
			},
			wantErr: false,
		},
		{
			name: "valid with models and base_url",
			config: &ProviderConfig{
				Provider: "custom",
				BaseURL:  "https://api.example.com",
				Models:   map[string]string{"gpt4": "gpt-4-turbo"},
			},
			wantErr: false,
		},
		{
			name:    "empty provider",
			config:  &ProviderConfig{Provider: ""},
			wantErr: true,
		},
		{
			name: "invalid soft limit",
			config: &ProviderConfig{
				Provider:  "anthropic",
				SoftLimit: &UsageLimit{Requests: -1},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ProviderConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: &Config{
				Providers: map[string]*ProviderConfig{
					"anthropic": {Provider: "anthropic", Enabled: true},
				},
				DefaultProvider: "anthropic",
			},
			wantErr: false,
		},
		{
			name: "valid without default",
			config: &Config{
				Providers: map[string]*ProviderConfig{
					"openai": {Provider: "openai"},
				},
			},
			wantErr: false,
		},
		{
			name: "empty providers valid",
			config: &Config{
				Providers: map[string]*ProviderConfig{},
			},
			wantErr: false,
		},
		{
			name: "nil providers valid",
			config: &Config{
				Providers: nil,
			},
			wantErr: false,
		},
		{
			name: "invalid provider config",
			config: &Config{
				Providers: map[string]*ProviderConfig{
					"invalid": {Provider: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "default provider not in providers",
			config: &Config{
				Providers: map[string]*ProviderConfig{
					"openai": {Provider: "openai"},
				},
				DefaultProvider: "anthropic",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path := DefaultConfigPath()
	if path == "" {
		t.Skip("Could not determine home directory")
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".sylk", "config.yaml")
	if path != expected {
		t.Errorf("DefaultConfigPath() = %v, want %v", path, expected)
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("file not found", func(t *testing.T) {
		_, err := LoadConfig("/nonexistent/path/config.yaml")
		if err == nil {
			t.Error("LoadConfig() should return error for nonexistent file")
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "config-*.yaml")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())

		tmpFile.WriteString("invalid: yaml: content: [")
		tmpFile.Close()

		_, err = LoadConfig(tmpFile.Name())
		if err == nil {
			t.Error("LoadConfig() should return error for invalid YAML")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "config-*.yaml")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())

		configYAML := `
providers:
  anthropic:
    provider: anthropic
    enabled: true
default_provider: anthropic
`
		tmpFile.WriteString(configYAML)
		tmpFile.Close()

		cfg, err := LoadConfig(tmpFile.Name())
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}

		if cfg.DefaultProvider != "anthropic" {
			t.Errorf("DefaultProvider = %v, want anthropic", cfg.DefaultProvider)
		}

		if _, ok := cfg.Providers["anthropic"]; !ok {
			t.Error("Expected anthropic provider in config")
		}
	})

	t.Run("config fails validation", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "config-*.yaml")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())

		configYAML := `
providers:
  test:
    provider: ""
`
		tmpFile.WriteString(configYAML)
		tmpFile.Close()

		_, err = LoadConfig(tmpFile.Name())
		if err == nil {
			t.Error("LoadConfig() should return error for invalid config")
		}
	})
}

func TestNewDefaultConfig(t *testing.T) {
	cfg := NewDefaultConfig()

	if cfg == nil {
		t.Fatal("NewDefaultConfig() returned nil")
	}

	if cfg.Providers == nil {
		t.Error("NewDefaultConfig() should initialize Providers map")
	}

	if len(cfg.Providers) != 0 {
		t.Errorf("NewDefaultConfig() Providers should be empty, got %d", len(cfg.Providers))
	}

	if cfg.DefaultProvider != "" {
		t.Errorf("NewDefaultConfig() DefaultProvider should be empty, got %v", cfg.DefaultProvider)
	}
}
