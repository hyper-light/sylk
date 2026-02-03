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
	"voyage":    "VOYAGE_API_KEY",
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

	if key := resolveFromDotEnv(provider); key != "" {
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

func resolveFromDotEnv(provider string) string {
	envKey, ok := providerEnvKeys[provider]
	if !ok {
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	envPaths := []string{
		filepath.Join(cwd, ".env"),
		filepath.Join(cwd, ".env.local"),
	}

	for _, path := range envPaths {
		if value := parseEnvFile(path, envKey); value != "" {
			return value
		}
	}
	return ""
}

func parseEnvFile(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := splitLines(string(data))
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}

		idx := indexByte(line, '=')
		if idx < 0 {
			continue
		}

		k := trimSpace(line[:idx])
		if k != key {
			continue
		}

		v := trimSpace(line[idx+1:])
		v = trimQuotes(v)
		return v
	}
	return ""
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
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
