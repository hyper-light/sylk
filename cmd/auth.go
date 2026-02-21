package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/spf13/cobra"
)

var (
	apiKey          string
	credentialsFile string
)

const googleServiceAccountCredentialProvider = "google_service_account"

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
	if provider == "google" {
		if err := migrateLegacyGoogleServiceAccountCredential(context.Background()); err != nil {
			return err
		}
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

	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}
	if err := saveCredentialSecure(ctx, provider, key); err != nil {
		return err
	}
	if _, err := removeLegacyAPIKey(provider); err != nil {
		return err
	}
	fmt.Printf("Credentials saved for %s\n", provider)
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	if err := migrateLegacyGoogleServiceAccountCredential(context.Background()); err != nil {
		return err
	}

	providers := []string{"anthropic", "openai", "google"}

	fmt.Println("Provider Status:")
	fmt.Println("----------------")

	for _, p := range providers {
		status := "not configured"
		if providerHasCredentials(p) {
			status = "configured"
		}
		fmt.Printf("  %-12s %s\n", p+":", status)
	}

	return nil
}

func providerHasCredentials(provider string) bool {
	if hasSecureCredential(provider) {
		return true
	}
	if llm.HasCredentials(provider) {
		return true
	}
	switch provider {
	case "google":
		return hasGoogleOAuthCredentials() || hasGoogleServiceAccountCredentials()
	case "openai":
		return hasOpenAIOAuthCredentials()
	default:
		return false
	}
}

func hasGoogleOAuthCredentials() bool {
	authSvc := oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{})
	_, err := authSvc.Resolve(context.Background())
	return err == nil
}

func hasOpenAIOAuthCredentials() bool {
	authSvc := oauth.NewOpenAIAuthService(oauth.OpenAIAuthServiceConfig{})
	_, err := authSvc.Resolve(context.Background())
	return err == nil
}

func hasGoogleServiceAccountCredentials() bool {
	_ = migrateLegacyGoogleServiceAccountCredential(context.Background())
	return hasSecureCredential(googleServiceAccountCredentialProvider)
}

func runAuthRemove(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(args[0])
	if !isValidProvider(provider) {
		return fmt.Errorf("invalid provider: %s (valid: anthropic, openai, google)", provider)
	}
	if provider == "google" {
		if err := migrateLegacyGoogleServiceAccountCredential(context.Background()); err != nil {
			return err
		}
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

func removeCredential(provider string) error {
	removed, err := removeProviderCredentials(provider)
	if err != nil {
		return err
	}
	if !removed {
		fmt.Printf("No credentials found for %s\n", provider)
		return nil
	}
	fmt.Printf("Credentials removed for %s\n", provider)
	return nil
}

func saveCredentialSecure(ctx context.Context, provider, key string) error {
	manager, err := newCredentialsManager()
	if err != nil {
		return err
	}
	return manager.SetAPIKey(ctx, provider, key, nil)
}

func hasSecureCredential(provider string) bool {
	manager, err := newCredentialsManager()
	if err != nil {
		return false
	}
	key, err := manager.GetAPIKey(provider)
	return err == nil && strings.TrimSpace(key) != ""
}

func removeSecureCredential(provider string) (bool, error) {
	manager, err := newCredentialsManager()
	if err != nil {
		return false, err
	}
	_, err = manager.GetAPIKey(provider)
	if err != nil {
		return false, nil
	}
	if err := manager.DeleteAPIKey(provider); err != nil {
		return false, err
	}
	return true, nil
}

func removeProviderCredentials(provider string) (bool, error) {
	removedAny := false
	removed, err := removeSecureCredential(provider)
	if err != nil {
		return false, err
	}
	removedAny = removedAny || removed

	removed, err = removeLegacyAPIKey(provider)
	if err != nil {
		return false, err
	}
	removedAny = removedAny || removed

	removed, err = removeProviderOAuthCredential(provider)
	if err != nil {
		return false, err
	}
	removedAny = removedAny || removed

	if provider != "google" {
		return removedAny, nil
	}
	removed, err = removeGoogleServiceAccountCredential()
	if err != nil {
		return false, err
	}
	removedAny = removedAny || removed
	return removedAny, nil
}

func removeLegacyAPIKey(provider string) (bool, error) {
	creds, err := llm.LoadCredentials()
	if err != nil {
		return false, err
	}
	if _, ok := creds[provider]; !ok {
		return false, nil
	}
	delete(creds, provider)
	if err := llm.SaveCredentials(creds); err != nil {
		return false, err
	}
	return true, nil
}

func removeProviderOAuthCredential(provider string) (bool, error) {
	switch provider {
	case "google":
		return removeGoogleOAuthCredential()
	case "openai":
		return removeOpenAIOAuthCredential()
	default:
		return false, nil
	}
}

func removeGoogleOAuthCredential() (bool, error) {
	authSvc := oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{})
	if _, err := authSvc.Resolve(context.Background()); err != nil {
		return false, nil
	}
	if err := authSvc.Delete(context.Background()); err != nil {
		return false, err
	}
	return true, nil
}

func removeOpenAIOAuthCredential() (bool, error) {
	authSvc := oauth.NewOpenAIAuthService(oauth.OpenAIAuthServiceConfig{})
	if _, err := authSvc.Resolve(context.Background()); err != nil {
		return false, nil
	}
	if err := authSvc.Delete(context.Background()); err != nil {
		return false, err
	}
	return true, nil
}

func removeGoogleServiceAccountCredential() (bool, error) {
	removedSecure, err := removeSecureCredential(googleServiceAccountCredentialProvider)
	if err != nil {
		return false, err
	}
	removedLegacy, err := removeLegacyGoogleServiceAccountFile()
	if err != nil {
		return false, err
	}
	return removedSecure || removedLegacy, nil
}

func newCredentialsManager() (*credentials.Manager, error) {
	dirs, err := storage.ResolveDirs()
	if err != nil {
		return nil, err
	}
	if err := dirs.EnsureAll(); err != nil {
		return nil, err
	}
	return credentials.NewManager(dirs, "default")
}

func saveGoogleCredentialsFile(path string) error {
	payload, err := readGoogleServiceAccountPayload(path)
	if err != nil {
		return err
	}
	if err := saveCredentialSecure(context.Background(), googleServiceAccountCredentialProvider, payload); err != nil {
		return err
	}
	if _, err := removeLegacyGoogleServiceAccountFile(); err != nil {
		return err
	}
	fmt.Println("Google service-account credentials saved securely")
	return nil
}

func readGoogleServiceAccountPayload(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading credentials file: %w", err)
	}
	validated, err := validateGoogleServiceAccountJSON(data)
	if err != nil {
		return "", err
	}
	return validated, nil
}

func validateGoogleServiceAccountJSON(data []byte) (string, error) {
	payload := map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("invalid credentials JSON: %w", err)
	}
	if err := validateGoogleServiceAccountMap(payload); err != nil {
		return "", err
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("normalize credentials JSON: %w", err)
	}
	return string(normalized), nil
}

func validateGoogleServiceAccountMap(payload map[string]any) error {
	if valueAsString(payload["type"]) != "service_account" {
		return fmt.Errorf("credentials file is not a Google service account")
	}
	required := []struct {
		Key    string
		ErrMsg string
	}{
		{Key: "client_email", ErrMsg: "credentials file is missing client_email"},
		{Key: "private_key", ErrMsg: "credentials file is missing private_key"},
	}
	for _, field := range required {
		if valueAsString(payload[field.Key]) == "" {
			return errors.New(field.ErrMsg)
		}
	}
	return nil
}

func valueAsString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func migrateLegacyGoogleServiceAccountCredential(ctx context.Context) error {
	path := legacyGoogleServiceAccountPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy google credentials file: %w", err)
	}
	if hasGoogleServiceAccountCredentials() {
		return os.Remove(path)
	}
	payload, err := validateGoogleServiceAccountJSON(data)
	if err != nil {
		return err
	}
	if err := saveCredentialSecure(ctx, googleServiceAccountCredentialProvider, payload); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove legacy google credentials file: %w", err)
	}
	return nil
}

func removeLegacyGoogleServiceAccountFile() (bool, error) {
	path := legacyGoogleServiceAccountPath()
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("remove legacy google credentials file: %w", err)
	}
	return true, nil
}

func legacyGoogleServiceAccountPath() string {
	dir, err := llm.EnsureCredentialsDir()
	if err != nil {
		return filepath.Join(".", "google-credentials.json")
	}
	return filepath.Join(dir, "google-credentials.json")
}
