package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/core/llm"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	apiKey          string
	credentialsFile string
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage provider authentication",
	Long:  `Configure API keys and credentials for LLM providers.`,
}

var authSetCmd = &cobra.Command{
	Use:   "set <provider>",
	Short: "Set API key for a provider",
	Long:  `Set the API key for an LLM provider (anthropic, openai, google).`,
	Args:  cobra.ExactArgs(1),
	RunE:  runAuthSet,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show configured providers and their status",
	Long:  `Display which providers have credentials configured.`,
	RunE:  runAuthStatus,
}

var authRemoveCmd = &cobra.Command{
	Use:   "remove <provider>",
	Short: "Remove credentials for a provider",
	Long:  `Remove the stored API key for an LLM provider.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runAuthRemove,
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(authSetCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authRemoveCmd)

	authSetCmd.Flags().StringVar(&apiKey, "api-key", "", "API key (reads from stdin if not provided)")
	authSetCmd.Flags().StringVar(&credentialsFile, "credentials-file", "", "Path to service account JSON (Google only)")
}

func runAuthSet(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(args[0])
	if !isValidProvider(provider) {
		return fmt.Errorf("invalid provider: %s (valid: anthropic, openai, google)", provider)
	}

	key := apiKey
	if key == "" && credentialsFile == "" {
		var err error
		key, err = readKeyInteractive(provider)
		if err != nil {
			return err
		}
	}

	if credentialsFile != "" && provider == "google" {
		return saveGoogleCredentialsFile(credentialsFile)
	}

	return saveCredential(provider, key)
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	providers := []string{"anthropic", "openai", "google"}

	fmt.Println("Provider Status:")
	fmt.Println("----------------")

	for _, p := range providers {
		status := "not configured"
		if llm.HasCredentials(p) {
			status = "configured"
		}
		fmt.Printf("  %-12s %s\n", p+":", status)
	}

	return nil
}

func runAuthRemove(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(args[0])
	if !isValidProvider(provider) {
		return fmt.Errorf("invalid provider: %s (valid: anthropic, openai, google)", provider)
	}

	return removeCredential(provider)
}

func isValidProvider(provider string) bool {
	switch provider {
	case "anthropic", "openai", "google":
		return true
	default:
		return false
	}
}

func readKeyInteractive(provider string) (string, error) {
	fmt.Printf("Enter API key for %s: ", provider)
	reader := bufio.NewReader(os.Stdin)
	key, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimSpace(key), nil
}

func ensureCredentialsDir() (string, error) {
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

func loadCredentialsFile() (map[string]string, error) {
	path := llm.DefaultCredentialsPath()
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

	var file struct {
		Credentials map[string]string `yaml:"credentials"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}

	if file.Credentials == nil {
		return make(map[string]string), nil
	}
	return file.Credentials, nil
}

func saveCredentialsFile(creds map[string]string) error {
	dir, err := ensureCredentialsDir()
	if err != nil {
		return err
	}

	path := filepath.Join(dir, "credentials.yaml")
	file := struct {
		Credentials map[string]string `yaml:"credentials"`
	}{Credentials: creds}

	data, err := yaml.Marshal(&file)
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing credentials: %w", err)
	}

	return nil
}

func saveCredential(provider, key string) error {
	creds, err := loadCredentialsFile()
	if err != nil {
		return err
	}

	creds[provider] = key
	if err := saveCredentialsFile(creds); err != nil {
		return err
	}

	fmt.Printf("Credentials saved for %s\n", provider)
	return nil
}

func removeCredential(provider string) error {
	creds, err := loadCredentialsFile()
	if err != nil {
		return err
	}

	if _, ok := creds[provider]; !ok {
		fmt.Printf("No credentials found for %s\n", provider)
		return nil
	}

	delete(creds, provider)
	if err := saveCredentialsFile(creds); err != nil {
		return err
	}

	fmt.Printf("Credentials removed for %s\n", provider)
	return nil
}

func saveGoogleCredentialsFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading credentials file: %w", err)
	}

	dir, err := ensureCredentialsDir()
	if err != nil {
		return err
	}

	destPath := filepath.Join(dir, "google-credentials.json")
	if err := os.WriteFile(destPath, data, 0600); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}

	fmt.Printf("Google credentials saved to %s\n", destPath)
	return nil
}
