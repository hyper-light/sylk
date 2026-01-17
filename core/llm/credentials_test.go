package llm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAPIKeyFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		envKey   string
		envValue string
		wantKey  string
		wantErr  bool
	}{
		{
			name:     "anthropic from env",
			provider: "anthropic",
			envKey:   "ANTHROPIC_API_KEY",
			envValue: "sk-ant-test123",
			wantKey:  "sk-ant-test123",
			wantErr:  false,
		},
		{
			name:     "openai from env",
			provider: "openai",
			envKey:   "OPENAI_API_KEY",
			envValue: "sk-openai-test456",
			wantKey:  "sk-openai-test456",
			wantErr:  false,
		},
		{
			name:     "google from env",
			provider: "google",
			envKey:   "GOOGLE_API_KEY",
			envValue: "google-key-789",
			wantKey:  "google-key-789",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original value
			originalValue := os.Getenv(tt.envKey)
			defer os.Setenv(tt.envKey, originalValue)

			os.Setenv(tt.envKey, tt.envValue)

			key, err := ResolveAPIKey(tt.provider)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveAPIKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if key != tt.wantKey {
				t.Errorf("ResolveAPIKey() = %v, want %v", key, tt.wantKey)
			}
		})
	}
}

func TestResolveAPIKeyUnknownProvider(t *testing.T) {
	// Clear any potentially set env vars for unknown provider
	_, err := ResolveAPIKey("unknown_provider")
	if err == nil {
		t.Error("ResolveAPIKey() should return error for unknown provider without credentials")
	}
}

func TestResolveAPIKeyNoCredentials(t *testing.T) {
	// Save and clear the env var
	originalValue := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer os.Setenv("ANTHROPIC_API_KEY", originalValue)

	_, err := ResolveAPIKey("anthropic")
	if err == nil {
		t.Error("ResolveAPIKey() should return error when no credentials available")
	}
}

func TestResolveAPIKeyFromFile(t *testing.T) {
	// Create a temporary directory to simulate ~/.sylk
	tmpDir, err := os.MkdirTemp("", "sylk-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Save original home directory
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create .sylk directory
	sylkDir := filepath.Join(tmpDir, ".sylk")
	if err := os.MkdirAll(sylkDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create credentials file
	credentialsFile := filepath.Join(sylkDir, "credentials.yaml")
	credentialsContent := `
credentials:
  anthropic: file-based-key-123
  openai: file-based-openai-456
`
	if err := os.WriteFile(credentialsFile, []byte(credentialsContent), 0600); err != nil {
		t.Fatal(err)
	}

	// Clear env vars to force file resolution
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")

	t.Run("anthropic from file", func(t *testing.T) {
		key, err := ResolveAPIKey("anthropic")
		if err != nil {
			t.Errorf("ResolveAPIKey() error = %v", err)
			return
		}
		if key != "file-based-key-123" {
			t.Errorf("ResolveAPIKey() = %v, want file-based-key-123", key)
		}
	})

	t.Run("openai from file", func(t *testing.T) {
		key, err := ResolveAPIKey("openai")
		if err != nil {
			t.Errorf("ResolveAPIKey() error = %v", err)
			return
		}
		if key != "file-based-openai-456" {
			t.Errorf("ResolveAPIKey() = %v, want file-based-openai-456", key)
		}
	})
}

func TestGetEnvKeyName(t *testing.T) {
	tests := []struct {
		provider string
		expected string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"google", "GOOGLE_API_KEY"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := GetEnvKeyName(tt.provider)
			if got != tt.expected {
				t.Errorf("GetEnvKeyName(%s) = %v, want %v", tt.provider, got, tt.expected)
			}
		})
	}
}

func TestRegisterEnvKey(t *testing.T) {
	// Register a new provider
	RegisterEnvKey("custom_provider", "CUSTOM_PROVIDER_API_KEY")

	got := GetEnvKeyName("custom_provider")
	if got != "CUSTOM_PROVIDER_API_KEY" {
		t.Errorf("GetEnvKeyName() = %v, want CUSTOM_PROVIDER_API_KEY", got)
	}

	// Test that env resolution works for the new provider
	originalValue := os.Getenv("CUSTOM_PROVIDER_API_KEY")
	defer os.Setenv("CUSTOM_PROVIDER_API_KEY", originalValue)

	os.Setenv("CUSTOM_PROVIDER_API_KEY", "custom-key-value")
	key, err := ResolveAPIKey("custom_provider")
	if err != nil {
		t.Errorf("ResolveAPIKey() error = %v", err)
		return
	}
	if key != "custom-key-value" {
		t.Errorf("ResolveAPIKey() = %v, want custom-key-value", key)
	}
}

func TestHasCredentials(t *testing.T) {
	t.Run("has credentials", func(t *testing.T) {
		originalValue := os.Getenv("ANTHROPIC_API_KEY")
		defer os.Setenv("ANTHROPIC_API_KEY", originalValue)

		os.Setenv("ANTHROPIC_API_KEY", "test-key")

		if !HasCredentials("anthropic") {
			t.Error("HasCredentials() should return true when env var is set")
		}
	})

	t.Run("no credentials", func(t *testing.T) {
		originalValue := os.Getenv("ANTHROPIC_API_KEY")
		os.Unsetenv("ANTHROPIC_API_KEY")
		defer os.Setenv("ANTHROPIC_API_KEY", originalValue)

		if HasCredentials("anthropic") {
			t.Error("HasCredentials() should return false when no credentials")
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		if HasCredentials("totally_unknown_provider_xyz") {
			t.Error("HasCredentials() should return false for unknown provider")
		}
	})
}

func TestDefaultCredentialsPath(t *testing.T) {
	path := DefaultCredentialsPath()
	if path == "" {
		t.Skip("Could not determine home directory")
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".sylk", "credentials.yaml")
	if path != expected {
		t.Errorf("DefaultCredentialsPath() = %v, want %v", path, expected)
	}
}

func TestResolveAPIKeyEnvPrecedence(t *testing.T) {
	// Create a temporary directory to simulate ~/.sylk
	tmpDir, err := os.MkdirTemp("", "sylk-test-precedence")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Save original home directory
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create .sylk directory and credentials file
	sylkDir := filepath.Join(tmpDir, ".sylk")
	if err := os.MkdirAll(sylkDir, 0755); err != nil {
		t.Fatal(err)
	}

	credentialsFile := filepath.Join(sylkDir, "credentials.yaml")
	credentialsContent := `
credentials:
  anthropic: file-key
`
	if err := os.WriteFile(credentialsFile, []byte(credentialsContent), 0600); err != nil {
		t.Fatal(err)
	}

	// Set env var as well - it should take precedence
	originalEnvValue := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "env-key")
	defer os.Setenv("ANTHROPIC_API_KEY", originalEnvValue)

	key, err := ResolveAPIKey("anthropic")
	if err != nil {
		t.Errorf("ResolveAPIKey() error = %v", err)
		return
	}
	if key != "env-key" {
		t.Errorf("ResolveAPIKey() = %v, want env-key (env should take precedence)", key)
	}
}

func TestResolveFromFileInvalidYAML(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "sylk-test-invalid")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	sylkDir := filepath.Join(tmpDir, ".sylk")
	if err := os.MkdirAll(sylkDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create invalid credentials file
	credentialsFile := filepath.Join(sylkDir, "credentials.yaml")
	if err := os.WriteFile(credentialsFile, []byte("invalid: yaml: ["), 0600); err != nil {
		t.Fatal(err)
	}

	// Clear env var
	originalEnvValue := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer os.Setenv("ANTHROPIC_API_KEY", originalEnvValue)

	_, err = ResolveAPIKey("anthropic")
	if err == nil {
		t.Error("ResolveAPIKey() should return error for invalid YAML")
	}
}
