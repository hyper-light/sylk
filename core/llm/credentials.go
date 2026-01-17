package llm

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var providerEnvKeys = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
	"google":    "GOOGLE_API_KEY",
}

type credentialsFile struct {
	Credentials map[string]string `yaml:"credentials"`
}

func DefaultCredentialsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sylk", "credentials.yaml")
}

func ResolveAPIKey(provider string) (string, error) {
	if key := resolveFromEnv(provider); key != "" {
		return key, nil
	}

	key, err := resolveFromFile(provider)
	if err != nil {
		return "", err
	}
	if key != "" {
		return key, nil
	}

	return "", fmt.Errorf("no API key found for provider %q", provider)
}

func resolveFromEnv(provider string) string {
	envKey, ok := providerEnvKeys[provider]
	if !ok {
		return ""
	}
	return os.Getenv(envKey)
}

func resolveFromFile(provider string) (string, error) {
	path := DefaultCredentialsPath()
	if path == "" {
		return "", nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading credentials: %w", err)
	}

	var creds credentialsFile
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return "", fmt.Errorf("parsing credentials: %w", err)
	}

	return creds.Credentials[provider], nil
}

func GetEnvKeyName(provider string) string {
	return providerEnvKeys[provider]
}

func RegisterEnvKey(provider, envKey string) {
	providerEnvKeys[provider] = envKey
}

func HasCredentials(provider string) bool {
	key, err := ResolveAPIKey(provider)
	return err == nil && key != ""
}
