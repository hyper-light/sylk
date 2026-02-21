package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	for _, envKey := range providerEnvCandidates(provider) {
		if value := os.Getenv(envKey); value != "" {
			return value
		}
	}
	return ""
}

func resolveFromDotEnv(provider string) string {
	candidates := providerEnvCandidates(provider)
	if len(candidates) == 0 {
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
		for _, envKey := range candidates {
			if value := parseEnvFile(path, envKey); value != "" {
				return value
			}
		}
	}
	return ""
}

func providerEnvCandidates(provider string) []string {
	envKey, ok := providerEnvKeys[provider]
	if !ok {
		return nil
	}
	if provider == "google" {
		return []string{envKey, "GEMINI_API_KEY"}
	}
	return []string{envKey}
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

// ValidateKeyFormat checks whether key has the expected prefix and minimum
// length for the given provider. It is intentionally loose — the goal is to
// reject obviously-wrong values (empty strings, truncated pastes) without
// blocking keys whose format evolves over time.
func ValidateKeyFormat(provider, key string) bool {
	const minLen = 24
	if len(key) < minLen {
		return false
	}
	switch provider {
	case "anthropic":
		return strings.HasPrefix(key, "sk-ant-")
	case "openai":
		return strings.HasPrefix(key, "sk-") && !strings.HasPrefix(key, "sk-ant-")
	case "google":
		return strings.HasPrefix(key, "AIza")
	default:
		return true
	}
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

// EnsureCredentialsDir creates the ~/.sylk directory and returns its path.
func EnsureCredentialsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	dir := filepath.Join(home, ".sylk")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}
	return dir, nil
}

// LoadCredentials reads the credentials map from disk.
func LoadCredentials() (map[string]string, error) {
	path := DefaultCredentialsPath()
	if path == "" {
		return nil, fmt.Errorf("could not determine credentials path")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(map[string]string), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}
	var file credentialsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	if file.Credentials == nil {
		return make(map[string]string), nil
	}
	return file.Credentials, nil
}

// SaveCredentials writes the full credentials map to disk.
func SaveCredentials(creds map[string]string) error {
	dir, err := EnsureCredentialsDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "credentials.yaml")
	file := credentialsFile{Credentials: creds}
	data, err := yaml.Marshal(&file)
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// SaveAPIKey stores a single provider's API key in the credentials file.
func SaveAPIKey(provider, key string) error {
	creds, err := LoadCredentials()
	if err != nil {
		return err
	}
	creds[provider] = key
	return SaveCredentials(creds)
}
