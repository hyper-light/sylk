package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/adalundhe/sylk/skills"
	"google.golang.org/genai"
)

// formatGoogleError extracts genai.APIError details if present and returns
// a descriptive error. For non-API errors it falls back to err.Error().
func formatGoogleError(errContext string, err error) error {
	if err == nil {
		return nil
	}
	if providerErr := buildGoogleProviderError(errContext, err); providerErr != nil {
		return providerErr
	}
	return WrapError(ProviderTypeGoogle, errContext, err)
}

func buildGoogleProviderError(operation string, err error) error {
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		return nil
	}

	status := strings.TrimSpace(apiErr.Status)
	msg := strings.TrimSpace(apiErr.Message)
	details := strings.TrimSpace(formatGoogleErrorDetails(apiErr.Details))
	if msg == "" {
		msg = "request failed"
	}

	message := fmt.Sprintf("%d", apiErr.Code)
	if status != "" {
		message += " " + status
	}
	message += " — " + msg
	if details != "" {
		message += " [" + details + "]"
	}

	providerErr := &ProviderError{
		Provider:   ProviderTypeGoogle,
		Operation:  operation,
		StatusCode: apiErr.Code,
		Message:    message,
		Underlying: err,
		Retryable:  isGoogleRetryableStatus(apiErr.Code),
	}
	if retryAfter, ok := googleRetryAfter(apiErr); ok {
		providerErr.RetryAfter = retryAfter
	}
	return providerErr
}

func formatGoogleErrorDetails(details []map[string]any) string {
	if len(details) == 0 {
		return ""
	}
	parts := make([]string, 0, len(details))
	for _, d := range details {
		for k, v := range d {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(parts, "; ")
}

// isGoogleRetryable returns true for transient Google API errors: rate limits
// (429) and server errors (5xx).
func isGoogleRetryable(err error) bool {
	if err == nil {
		return false
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr.Provider == ProviderTypeGoogle {
		return isGoogleRetryableStatus(providerErr.StatusCode)
	}
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return isGoogleRetryableStatus(apiErr.Code)
	}
	return false
}

func isGoogleRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func googleRetryAfter(apiErr genai.APIError) (time.Duration, bool) {
	if d, ok := googleRetryAfterFromDetails(apiErr.Details); ok {
		return d, true
	}
	if d, ok := googleRetryAfterFromMessage(apiErr.Message); ok {
		return d, true
	}
	return parseRetryAfterValue(apiErr.Message)
}

// googleRetryAfterFromMessage extracts a duration from human-readable API
// messages like "Your quota will reset after 39s." by scanning for a
// time.ParseDuration-compatible token following "after".
func googleRetryAfterFromMessage(message string) (time.Duration, bool) {
	lower := strings.ToLower(message)
	idx := strings.Index(lower, "after ")
	if idx < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(lower[idx+len("after "):])
	token, _, _ := strings.Cut(rest, " ")
	token = strings.TrimRight(token, ".!,;")
	if token == "" {
		return 0, false
	}
	return googleRetryAfterFromString(token)
}

func googleRetryAfterFromDetails(details []map[string]any) (time.Duration, bool) {
	for _, detail := range details {
		if d, ok := googleRetryAfterFromAny(detail); ok {
			return d, true
		}
	}
	return 0, false
}

func googleRetryAfterFromAny(value any) (time.Duration, bool) {
	switch v := value.(type) {
	case map[string]any:
		if d, ok := googleRetryAfterFromRetryDelayKey(v); ok {
			return d, true
		}
		for _, nested := range v {
			if d, ok := googleRetryAfterFromAny(nested); ok {
				return d, true
			}
		}
	case []any:
		for _, nested := range v {
			if d, ok := googleRetryAfterFromAny(nested); ok {
				return d, true
			}
		}
	case string:
		if d, ok := googleRetryAfterFromString(v); ok {
			return d, true
		}
	}
	return 0, false
}

func googleRetryAfterFromRetryDelayKey(value map[string]any) (time.Duration, bool) {
	for key, raw := range value {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized != "retrydelay" && normalized != "retry_delay" {
			continue
		}
		if d, ok := googleRetryAfterFromAny(raw); ok {
			return d, true
		}
	}
	return 0, false
}

func googleRetryAfterFromString(value string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	if d, err := time.ParseDuration(trimmed); err == nil && d > 0 {
		return d, true
	}
	if d, ok := parseRetryAfterValue(trimmed); ok {
		return d, true
	}
	if seconds, err := strconv.ParseFloat(trimmed, 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second)), true
	}
	return 0, false
}

// GoogleProvider implements Provider for Google's Gemini models
type GoogleProvider struct {
	client            *genai.Client
	config            GoogleConfig
	skills            []skills.Skill
	authService       oauth.GoogleAuthService
	codeAssistProject string       // set when Code Assist is active
	codeAssistHTTP    *http.Client // OAuth-wrapped HTTP client for Code Assist
}

type GoogleOAuthLoginSession struct {
	Challenge *oauth.GoogleOAuthChallenge
	Results   <-chan GoogleOAuthLoginResult
	Cancel    context.CancelFunc
}

type GoogleOAuthLoginResult struct {
	Auth *oauth.GoogleOAuthAuth
	Err  error
}

type GoogleModel string

const (
	Gemini3Pro GoogleModel = "gemini-3-pro-preview"
)

// Supported Google models
var googleModels = map[string]bool{
	// Gemini 3 family
	"gemini-3-pro": true, // Google Gemini 3 Pro
}

const googleServiceAccountCredentialProvider = "google_service_account"
const legacyGoogleServiceAccountFilename = "google-credentials.json"

type googleServiceAccountFileCache struct {
	mu      sync.Mutex
	payload string
	path    string
}

var googleServiceAccountCache googleServiceAccountFileCache
var googleProviderAPIKeyResolver = resolveGoogleProviderAPIKey

// NewGoogleProvider creates a new Google provider with the given configuration
func NewGoogleProvider(ctx context.Context, config GoogleConfig, skills ...skills.Skill) (*GoogleProvider, error) {
	return NewGoogleProviderWithAuthService(ctx, config, oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{}), skills...)
}

// NewGoogleProviderWithAuthService creates a provider using a custom Google OAuth service.
func NewGoogleProviderWithAuthService(
	ctx context.Context,
	config GoogleConfig,
	authService oauth.GoogleAuthService,
	skills ...skills.Skill,
) (*GoogleProvider, error) {
	googleTrace("provider_init", "start", map[string]any{
		"auth_mode":       strings.TrimSpace(config.AuthMode),
		"model":          strings.TrimSpace(config.Model),
		"project_id":     strings.TrimSpace(config.ProjectID),
		"use_vertex_ai":  config.UseVertexAI,
		"use_code_assist": config.UseCodeAssist,
		"has_api_key":    strings.TrimSpace(config.APIKey) != "",
	})
	applyGoogleProviderDefaults(&config)
	if err := hydrateGoogleConfig(ctx, &config, authService); err != nil {
		googleTrace("provider_init", "hydrate_failed", map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}
	if err := config.Validate(); err != nil {
		googleTrace("provider_init", "validate_failed", map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}
	clientConfig, err := buildGoogleClientConfig(config, authService)
	if err != nil {
		googleTrace("provider_init", "client_config_failed", map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}
	googleTrace("provider_init", "client_config_built", map[string]any{
		"backend":        googleBackendName(config.UseVertexAI),
		"has_http_client": clientConfig.HTTPClient != nil,
		"has_api_key":    clientConfig.APIKey != "",
		"has_project":    clientConfig.Project != "",
	})
	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		googleTrace("provider_init", "genai_client_failed", map[string]any{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("google provider: failed to create client: %w", err)
	}

	provider := &GoogleProvider{
		client:      client,
		config:      config,
		skills:      skills,
		authService: authService,
	}
	if config.UseCodeAssist {
		provider.codeAssistProject = config.ProjectID
		provider.codeAssistHTTP = oauth.NewGoogleOAuthHTTPClient(authService, newDefaultGoogleHTTPClient(config.Timeout))
		googleTrace("provider_init", "code_assist_enabled", map[string]any{
			"project": config.ProjectID,
		})
	}
	googleTrace("provider_init", "success", map[string]any{
		"auth_mode":            strings.TrimSpace(config.AuthMode),
		"model":               strings.TrimSpace(config.Model),
		"use_vertex_ai":       config.UseVertexAI,
		"use_code_assist":     config.UseCodeAssist,
		"code_assist_project": provider.codeAssistProject,
	})
	return provider, nil
}

func (g *GoogleProvider) StartOAuthLogin(
	ctx context.Context,
	timeout time.Duration,
) (*GoogleOAuthLoginSession, error) {
	if g.authService == nil {
		return nil, fmt.Errorf("google oauth auth service is not configured")
	}
	session, err := oauth.StartGoogleOAuthSession(ctx, g.authService, timeout)
	if err != nil {
		return nil, err
	}
	results := make(chan GoogleOAuthLoginResult, 1)
	go forwardGoogleOAuthLoginResults(session.Results, results)
	return &GoogleOAuthLoginSession{
		Challenge: session.Challenge,
		Results:   results,
		Cancel:    session.Cancel,
	}, nil
}

func forwardGoogleOAuthLoginResults(
	source <-chan oauth.GoogleOAuthResult,
	target chan<- GoogleOAuthLoginResult,
) {
	defer close(target)
	for result := range source {
		target <- GoogleOAuthLoginResult{
			Auth: result.Auth,
			Err:  result.Err,
		}
	}
}

func applyGoogleProviderDefaults(config *GoogleConfig) {
	if config == nil {
		return
	}
	defaults := DefaultGoogleConfig()
	if strings.TrimSpace(config.Model) == "" {
		config.Model = defaults.Model
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = defaults.MaxTokens
	}
	if strings.TrimSpace(config.AuthMode) == "" {
		config.AuthMode = defaults.AuthMode
	}
	if strings.TrimSpace(config.Location) == "" {
		config.Location = defaults.Location
	}
}

func hydrateGoogleConfig(
	ctx context.Context,
	config *GoogleConfig,
	authService oauth.GoogleAuthService,
) error {
	if config == nil {
		return fmt.Errorf("google config is nil")
	}
	normalizeGoogleHydrationFields(config)
	switch config.AuthMode {
	case GoogleAuthModeOAuth:
		return hydrateGoogleOAuthConfig(ctx, config, authService)
	case GoogleAuthModeServiceAccount:
		return hydrateGoogleServiceAccountConfig(config)
	default:
		return hydrateGoogleAPIKeyConfig(config)
	}
}

func normalizeGoogleHydrationFields(config *GoogleConfig) {
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.AuthMode = strings.TrimSpace(config.AuthMode)
	config.ProjectID = strings.TrimSpace(config.ProjectID)
	config.Location = strings.TrimSpace(config.Location)
	if config.AuthMode == "" {
		config.AuthMode = GoogleAuthModeAPIKey
	}
}

func hydrateGoogleAPIKeyConfig(config *GoogleConfig) error {
	if config == nil {
		return nil
	}
	if config.APIKey != "" {
		return nil
	}
	config.APIKey = googleProviderAPIKeyResolver()
	return nil
}

func resolveGoogleProviderAPIKey() string {
	return resolveGoogleProviderAPIKeyWithResolvers(
		os.Getenv,
		resolveGoogleSecureAPIKey,
		resolveGoogleLegacyAPIKey,
	)
}

func resolveGoogleProviderAPIKeyWithResolvers(
	lookupEnv func(string) string,
	secureResolver func() string,
	legacyResolver func() (string, error),
) string {
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	if key := strings.TrimSpace(lookupEnv("GEMINI_API_KEY")); key != "" {
		return key
	}
	if key := strings.TrimSpace(lookupEnv("GOOGLE_API_KEY")); key != "" {
		return key
	}
	if secureResolver != nil {
		if key := strings.TrimSpace(secureResolver()); key != "" {
			return key
		}
	}
	if legacyResolver == nil {
		return ""
	}
	key, err := legacyResolver()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func resolveGoogleSecureAPIKey() string {
	dirs, err := storage.ResolveDirs()
	if err != nil || dirs == nil {
		return ""
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return ""
	}
	key, err := manager.GetAPIKey("google")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func resolveGoogleLegacyAPIKey() (string, error) {
	return llm.ResolveAPIKey("google")
}

func hydrateGoogleOAuthConfig(
	ctx context.Context,
	config *GoogleConfig,
	authService oauth.GoogleAuthService,
) error {
	if authService == nil {
		googleTrace("hydrate_oauth", "no_auth_service", nil)
		return fmt.Errorf("google oauth auth service is not configured")
	}
	auth, err := authService.Resolve(ctx)
	if err == nil {
		googleTrace("hydrate_oauth", "resolve_success", map[string]any{
			"has_access_token":    auth.AccessToken != "",
			"has_refresh_token":   auth.RefreshToken != "",
			"project_id":         strings.TrimSpace(auth.ProjectID),
			"location":           strings.TrimSpace(auth.Location),
			"code_assist_project": strings.TrimSpace(auth.CodeAssistProject),
			"code_assist_tier_id": strings.TrimSpace(auth.CodeAssistTierID),
		})
		applyResolvedGoogleOAuth(config, auth)

		googleTrace("hydrate_oauth", "after_apply", map[string]any{
			"config_project_id": strings.TrimSpace(config.ProjectID),
			"config_use_vertex": config.UseVertexAI,
			"config_auth_mode":  strings.TrimSpace(config.AuthMode),
		})

		// If OAuth resolved but no project is available, try Code Assist setup.
		// Code Assist provisions a managed project for free-tier users.
		if config.ProjectID == "" && auth.CodeAssistProject == "" {
			googleTrace("hydrate_oauth", "code_assist_setup_start", map[string]any{
				"config_project_id":   "",
				"auth_code_assist":    "",
				"endpoint":            oauth.CodeAssistEndpointForTrace(),
			})
			httpClient := oauth.NewGoogleOAuthHTTPClient(authService, nil)
			result, setupErr := oauth.SetupCodeAssist(ctx, httpClient, "")
			if setupErr != nil {
				googleTrace("hydrate_oauth", "code_assist_setup_failed", map[string]any{
					"error": setupErr.Error(),
				})
			} else {
				googleTrace("hydrate_oauth", "code_assist_setup_success", map[string]any{
					"project_id": result.ProjectID,
					"tier_id":    result.TierID,
					"tier_name":  result.TierName,
				})
				auth.CodeAssistProject = result.ProjectID
				auth.CodeAssistTierID = result.TierID
				if saveErr := authService.Save(ctx, auth); saveErr != nil {
					googleTrace("hydrate_oauth", "code_assist_save_failed", map[string]any{
						"error": saveErr.Error(),
					})
				}
			}
		} else {
			googleTrace("hydrate_oauth", "code_assist_setup_skipped", map[string]any{
				"config_project_id":        strings.TrimSpace(config.ProjectID),
				"auth_code_assist_project": strings.TrimSpace(auth.CodeAssistProject),
			})
		}

		// Apply Code Assist project if available.
		if auth.CodeAssistProject != "" {
			config.UseCodeAssist = true
			if config.ProjectID == "" {
				config.ProjectID = auth.CodeAssistProject
			}
			googleTrace("hydrate_oauth", "code_assist_applied", map[string]any{
				"project_id":     config.ProjectID,
				"use_code_assist": true,
			})
		}

		// OAuth can target either backend:
		// - Vertex AI when a project is available (or explicitly requested).
		// - Gemini API when no project is available.
		config.UseVertexAI = config.UseVertexAI || strings.TrimSpace(config.ProjectID) != ""

		googleTrace("hydrate_oauth", "final_config", map[string]any{
			"auth_mode":       strings.TrimSpace(config.AuthMode),
			"project_id":     strings.TrimSpace(config.ProjectID),
			"use_vertex_ai":  config.UseVertexAI,
			"use_code_assist": config.UseCodeAssist,
			"model":          strings.TrimSpace(config.Model),
			"api_key_set":    strings.TrimSpace(config.APIKey) != "",
		})
		return nil
	}
	googleTrace("hydrate_oauth", "resolve_failed", map[string]any{
		"error": err.Error(),
	})
	return fallbackGoogleOAuthToAPIKey(config, err)
}

func fallbackGoogleOAuthToAPIKey(config *GoogleConfig, resolveErr error) error {
	if config == nil {
		return fmt.Errorf("google config is nil")
	}
	if canFallbackGoogleOAuthToServiceAccount(config) {
		return nil
	}
	if err := hydrateGoogleAPIKeyConfig(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("resolve google oauth: %w", resolveErr)
	}
	config.AuthMode = GoogleAuthModeAPIKey
	config.UseVertexAI = false
	return nil
}

func canFallbackGoogleOAuthToServiceAccount(config *GoogleConfig) bool {
	if err := hydrateGoogleServiceAccountConfig(config); err != nil {
		return false
	}
	config.AuthMode = GoogleAuthModeServiceAccount
	return true
}

func hydrateGoogleServiceAccountConfig(config *GoogleConfig) error {
	if config == nil {
		return fmt.Errorf("google config is nil")
	}
	payload, err := resolveGoogleServiceAccountJSON()
	if err != nil {
		return err
	}
	if strings.TrimSpace(payload) == "" {
		return fmt.Errorf("google config: service account credentials not configured")
	}
	metadata, err := parseGoogleServiceAccountMetadata(payload)
	if err != nil {
		return err
	}
	if config.ProjectID == "" {
		config.ProjectID = metadata.ProjectID
	}
	if config.Location == "" {
		config.Location = "us-central1"
	}
	config.UseVertexAI = true
	return ensureGoogleApplicationCredentials(payload)
}

type googleServiceAccountMetadata struct {
	ProjectID string `json:"project_id"`
}

func parseGoogleServiceAccountMetadata(payload string) (*googleServiceAccountMetadata, error) {
	parsed := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, fmt.Errorf("google config: invalid service account JSON: %w", err)
	}
	if err := validateGoogleServiceAccountMap(parsed); err != nil {
		return nil, err
	}
	return &googleServiceAccountMetadata{ProjectID: mapStringValue(parsed, "project_id")}, nil
}

func validateGoogleServiceAccountMap(payload map[string]any) error {
	if mapStringValue(payload, "type") != "service_account" {
		return fmt.Errorf("google config: service account JSON must have type=service_account")
	}
	required := []struct {
		Key    string
		ErrMsg string
	}{
		{Key: "project_id", ErrMsg: "google config: service account JSON missing project_id"},
		{Key: "client_email", ErrMsg: "google config: service account JSON missing client_email"},
		{Key: "private_key", ErrMsg: "google config: service account JSON missing private_key"},
	}
	for _, field := range required {
		if mapStringValue(payload, field.Key) == "" {
			return errors.New(field.ErrMsg)
		}
	}
	return nil
}

func mapStringValue(payload map[string]any, key string) string {
	text, _ := payload[key].(string)
	return strings.TrimSpace(text)
}

func resolveGoogleServiceAccountJSON() (string, error) {
	if payload := strings.TrimSpace(os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON")); payload != "" {
		return payload, nil
	}
	payload, err := loadGoogleServiceAccountFromSecureStore()
	if err != nil {
		return "", err
	}
	if payload != "" {
		return payload, nil
	}
	return migrateLegacyGoogleServiceAccount()
}

func loadGoogleServiceAccountFromSecureStore() (string, error) {
	dirs, err := storage.ResolveDirs()
	if err != nil {
		return "", err
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return "", err
	}
	payload, err := manager.GetAPIKey(googleServiceAccountCredentialProvider)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(payload), nil
}

func migrateLegacyGoogleServiceAccount() (string, error) {
	path := legacyGoogleServiceAccountPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("google config: read legacy credentials file: %w", err)
	}
	payload, err := normalizeGoogleServiceAccountPayload(data)
	if err != nil {
		return "", err
	}
	if err := persistGoogleServiceAccountCredential(payload); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("google config: remove legacy credentials file: %w", err)
	}
	return payload, nil
}

func normalizeGoogleServiceAccountPayload(data []byte) (string, error) {
	payload := map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("google config: invalid service account JSON: %w", err)
	}
	if err := validateGoogleServiceAccountMap(payload); err != nil {
		return "", err
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("google config: normalize service account JSON: %w", err)
	}
	return string(normalized), nil
}

func persistGoogleServiceAccountCredential(payload string) error {
	dirs, err := storage.ResolveDirs()
	if err != nil {
		return err
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return err
	}
	return manager.SetAPIKey(context.Background(), googleServiceAccountCredentialProvider, payload, nil)
}

func legacyGoogleServiceAccountPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", legacyGoogleServiceAccountFilename)
	}
	return filepath.Join(home, ".sylk", legacyGoogleServiceAccountFilename)
}

func ensureGoogleApplicationCredentials(payload string) error {
	if current := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); current != "" {
		return nil
	}
	path, err := writeGoogleServiceAccountFile(payload)
	if err != nil {
		return err
	}
	return os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
}

func writeGoogleServiceAccountFile(payload string) (string, error) {
	cache := &googleServiceAccountCache
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cache.payload == payload && cache.path != "" {
		if _, err := os.Stat(cache.path); err == nil {
			return cache.path, nil
		}
	}

	dir := filepath.Join(os.TempDir(), "sylk-google")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("google config: create service-account dir: %w", err)
	}
	hash := sha256.Sum256([]byte(payload))
	name := "service-account-" + hex.EncodeToString(hash[:8]) + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		return "", fmt.Errorf("google config: write service-account file: %w", err)
	}
	cache.payload = payload
	cache.path = path
	return path, nil
}

func applyResolvedGoogleOAuth(config *GoogleConfig, auth *oauth.GoogleOAuthAuth) {
	if config == nil || auth == nil {
		return
	}
	if config.ProjectID == "" {
		config.ProjectID = strings.TrimSpace(auth.ProjectID)
	}
	if config.Location == "" {
		config.Location = strings.TrimSpace(auth.Location)
	}
}

func buildGoogleClientConfig(
	config GoogleConfig,
	authService oauth.GoogleAuthService,
) (*genai.ClientConfig, error) {
	clientConfig := &genai.ClientConfig{}
	if config.UseVertexAI {
		clientConfig.Project = config.ProjectID
		clientConfig.Location = config.Location
		clientConfig.Backend = genai.BackendVertexAI
	} else {
		clientConfig.APIKey = config.APIKey
		clientConfig.Backend = genai.BackendGeminiAPI
	}
	if config.AuthMode != GoogleAuthModeOAuth {
		return clientConfig, nil
	}
	if authService == nil {
		return nil, fmt.Errorf("google oauth auth service is not configured")
	}
	clientConfig.HTTPClient = oauth.NewGoogleOAuthHTTPClient(authService, newDefaultGoogleHTTPClient(config.Timeout))
	return clientConfig, nil
}

func newDefaultGoogleHTTPClient(timeout time.Duration) *http.Client {
	resolved := resolveGoogleProviderTimeout(timeout)
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: resolved,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   resolved,
	}
}

func resolveGoogleProviderTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return DefaultBaseConfig().Timeout
}

// Name returns the provider identifier
func (g *GoogleProvider) Name() string {
	return string(ProviderTypeGoogle)
}

// AuthMode returns the effective auth mode after hydration/fallback.
func (g *GoogleProvider) AuthMode() string {
	if g == nil {
		return ""
	}
	return strings.TrimSpace(g.config.AuthMode)
}

// UsesVertexAI reports whether requests are routed to Vertex AI backend.
func (g *GoogleProvider) UsesVertexAI() bool {
	if g == nil {
		return false
	}
	return g.config.UseVertexAI
}

// UsesCodeAssist reports whether requests are routed via Code Assist API.
func (g *GoogleProvider) UsesCodeAssist() bool {
	if g == nil {
		return false
	}
	return g.codeAssistProject != ""
}

// Generate performs a non-streaming completion request with retry.
func (g *GoogleProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	googleTrace("generate", "start", map[string]any{
		"req_model":          strings.TrimSpace(req.Model),
		"config_model":       strings.TrimSpace(g.config.Model),
		"code_assist_project": g.codeAssistProject,
		"use_code_assist":    g.codeAssistProject != "",
		"use_vertex_ai":      g.config.UseVertexAI,
		"auth_mode":          strings.TrimSpace(g.config.AuthMode),
		"message_count":      len(req.Messages),
		"has_system_prompt":  strings.TrimSpace(req.SystemPrompt) != "",
	})
	resp, err := retryGoogleGenerate(ctx, g.config.BaseConfig, func(ctx context.Context) (*Response, error) {
		if g.codeAssistProject != "" {
			googleTrace("generate", "using_code_assist", map[string]any{
				"project": g.codeAssistProject,
			})
			return g.generateWithCodeAssist(ctx, req)
		}
		model := req.Model
		if model == "" {
			model = g.config.Model
		}
		googleTrace("generate", "using_genai_sdk", map[string]any{
			"model":        model,
			"use_vertex_ai": g.config.UseVertexAI,
			"backend":      googleBackendName(g.config.UseVertexAI),
		})
		contents := g.convertMessages(req.Messages)
		genConfig := g.buildGenerateConfig(req)
		result, err := g.client.Models.GenerateContent(ctx, model, contents, genConfig)
		if err != nil {
			googleTrace("generate", "genai_sdk_error", map[string]any{
				"model": model,
				"error": err.Error(),
			})
			return nil, formatGoogleError("generate", err)
		}
		googleTrace("generate", "genai_sdk_success", map[string]any{
			"model": model,
		})
		return g.convertResponse(result, model), nil
	})
	if err != nil {
		googleTrace("generate", "failed", map[string]any{
			"error": err.Error(),
		})
	} else {
		googleTrace("generate", "success", map[string]any{
			"content_length": len(resp.Content),
			"stop_reason":    string(resp.StopReason),
		})
	}
	return resp, err
}

func googleBackendName(useVertexAI bool) string {
	if useVertexAI {
		return "vertex_ai"
	}
	return "gemini_api"
}

// retryGoogleGenerate wraps generate with Google-specific retryable error detection.
func retryGoogleGenerate(ctx context.Context, cfg BaseConfig, fn func(context.Context) (*Response, error)) (*Response, error) {
	maxAttempts := resolveGoogleMaxRetries(cfg.MaxRetries)
	var lastErr error
	for attempt := range maxAttempts {
		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !shouldRetryGoogleCall(ctx, err, attempt, maxAttempts) {
			break
		}
		delay := googleRetryDelay(err, attempt, cfg)
		notifyRetryObserver(ctx, RetryEvent{
			Attempt:     attempt + 1,
			MaxAttempts: maxAttempts,
			Err:         err,
			Delay:       delay,
		})
		if err := waitRetryDelay(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func shouldRetryGoogleCall(ctx context.Context, err error, attempt int, maxAttempts int) bool {
	if attempt+1 >= maxAttempts {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return isGoogleRetryable(err)
}

func (g *GoogleProvider) StreamWithHandler(ctx context.Context, req *Request, handler StreamHandler) error {
	return retryGoogleStream(ctx, g.config.BaseConfig, func(ctx context.Context) error {
		return g.streamWithHandlerOnce(ctx, req, handler)
	})
}

// retryGoogleStream wraps stream with Google-specific retryable error detection.
func retryGoogleStream(ctx context.Context, cfg BaseConfig, fn func(context.Context) error) error {
	maxAttempts := resolveGoogleMaxRetries(cfg.MaxRetries)
	var lastErr error
	for attempt := range maxAttempts {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if !shouldRetryGoogleCall(ctx, err, attempt, maxAttempts) {
			break
		}
		delay := googleRetryDelay(err, attempt, cfg)
		notifyRetryObserver(ctx, RetryEvent{
			Attempt:     attempt + 1,
			MaxAttempts: maxAttempts,
			Err:         err,
			Delay:       delay,
		})
		if err := waitRetryDelay(ctx, delay); err != nil {
			return err
		}
	}
	return lastErr
}

// resolveGoogleMaxRetries returns a floor of 5 retries for Google providers
// to handle quota resets that may require longer backoff sequences.
func resolveGoogleMaxRetries(configured int) int {
	resolved := resolveMaxRetries(configured)
	if resolved < 5 {
		return 5
	}
	return resolved
}

func googleRetryDelay(err error, attempt int, cfg BaseConfig) time.Duration {
	if retryAfter := GetRetryAfter(err); retryAfter > 0 {
		// Server-specified delays (from quota reset info) are authoritative —
		// respect them even when they exceed the configured max delay cap.
		return retryAfter
	}
	return retryDelay(attempt, cfg.RetryBaseDelay, cfg.RetryMaxDelay)
}

func (g *GoogleProvider) streamWithHandlerOnce(ctx context.Context, req *Request, handler StreamHandler) error {
	if g.codeAssistProject != "" {
		return g.streamWithCodeAssist(ctx, req, handler)
	}

	model := req.Model
	if model == "" {
		model = g.config.Model
	}

	contents := g.convertMessages(req.Messages)
	genConfig := g.buildGenerateConfig(req)

	if err := handler(&StreamChunk{
		Index:     0,
		Type:      ChunkTypeStart,
		Timestamp: time.Now(),
	}); err != nil {
		return err
	}

	var chunkIndex int
	var totalInputTokens, totalOutputTokens int
	var emittedEarlyUsage bool
	var stopReason StopReason
	toolCallsSeen := map[googleToolCallKey]bool{}
	textDelta := &providerDeltaEmitter{}
	thoughtDelta := &providerDeltaEmitter{}

	for resp, err := range g.client.Models.GenerateContentStream(ctx, model, contents, genConfig) {
		if err != nil {
			handler(&StreamChunk{
				Index:     chunkIndex + 1,
				Type:      ChunkTypeError,
				Text:      err.Error(),
				Timestamp: time.Now(),
			})
			return formatGoogleError("stream", err)
		}

		chunkIndex++

		text, thought := extractGoogleStreamTextAndThought(resp)
		if text := textDelta.Delta(text); text != "" {
			if err := handler(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeText,
				Text:      text,
				Timestamp: time.Now(),
			}); err != nil {
				return err
			}
		}
		if thought := thoughtDelta.Delta(thought); thought != "" {
			if err := handler(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeThought,
				Text:      thought,
				Timestamp: time.Now(),
			}); err != nil {
				return err
			}
		}

		if resp.FunctionCalls() != nil {
			for _, fc := range resp.FunctionCalls() {
				argsJSON, _ := json.Marshal(fc.Args)
				key := googleToolCallKey{ID: fc.ID, Name: fc.Name}
				if !toolCallsSeen[key] {
					toolCallsSeen[key] = true
					if err := handler(&StreamChunk{
						Index: chunkIndex,
						Type:  ChunkTypeToolStart,
						ToolCall: &ToolCallChunk{
							ID:   fc.ID,
							Name: fc.Name,
						},
						Timestamp: time.Now(),
					}); err != nil {
						return err
					}
				}
				if err := handler(&StreamChunk{
					Index: chunkIndex,
					Type:  ChunkTypeToolDelta,
					ToolCall: &ToolCallChunk{
						ID:             fc.ID,
						ArgumentsDelta: string(argsJSON),
					},
					Timestamp: time.Now(),
				}); err != nil {
					return err
				}
			}
		}

		if resp.UsageMetadata != nil {
			totalInputTokens = int(resp.UsageMetadata.PromptTokenCount)
			totalOutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
			if !emittedEarlyUsage && totalInputTokens > 0 {
				emittedEarlyUsage = true
				handler(&StreamChunk{
					Index:     chunkIndex,
					Type:      ChunkTypeStart,
					Usage:     &Usage{InputTokens: totalInputTokens},
					Timestamp: time.Now(),
				})
			}
		}
		if len(resp.Candidates) > 0 {
			switch resp.Candidates[0].FinishReason {
			case genai.FinishReasonStop:
				stopReason = StopReasonEndTurn
			case genai.FinishReasonMaxTokens:
				stopReason = StopReasonMaxTokens
			case genai.FinishReasonSafety:
				stopReason = StopReasonError
			}
		}
	}

	if stopReason == "" {
		stopReason = StopReasonEndTurn
	}

	return handler(&StreamChunk{
		Index:      chunkIndex + 1,
		Type:       ChunkTypeEnd,
		StopReason: stopReason,
		Usage: &Usage{
			InputTokens:  totalInputTokens,
			OutputTokens: totalOutputTokens,
			TotalTokens:  totalInputTokens + totalOutputTokens,
		},
		Timestamp: time.Now(),
	})
}

func (g *GoogleProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	return streamViaHandler(ctx, g, req), nil
}

// ValidateConfig checks if the provider configuration is valid
func (g *GoogleProvider) ValidateConfig() error {
	return g.config.Validate()
}

// SupportsModel checks if the provider supports the given model
func (g *GoogleProvider) SupportsModel(model string) bool {
	return googleModels[model]
}

// DefaultModel returns the provider's default model
func (g *GoogleProvider) DefaultModel() string {
	return g.config.Model
}

// Close cleans up any resources
func (g *GoogleProvider) Close() error {
	return nil
}

func (g *GoogleProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	return g.Generate(ctx, req)
}

func (g *GoogleProvider) SupportedModels() []ModelInfo {
	return []ModelInfo{
		{ID: "gemini-3-pro", Name: "Gemini 3 Pro", MaxContext: 200000},
	}
}

func (g *GoogleProvider) CountTokens(messages []Message) (int, error) {
	count := 0
	for _, msg := range messages {
		count += len(msg.Content) / 4
	}
	return count, nil
}

func (g *GoogleProvider) MaxContextTokens(model string) int {
	return 200000
}

func (g *GoogleProvider) HealthCheck(ctx context.Context) error {
	return nil
}

// buildGenerateConfig constructs generation config from a Request
func (g *GoogleProvider) buildGenerateConfig(req *Request) *genai.GenerateContentConfig {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = g.config.MaxTokens
	}

	config := &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxTokens),
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingLevel:   googleThinkingLevel(req.ReasoningEffort),
		},
	}

	var systemParts []*genai.Part
	systemPrompt := combineGoogleSystemPrompts(g.config.SystemPrompt, req.SystemPrompt)
	if systemPrompt != "" {
		systemParts = append(systemParts, genai.NewPartFromText(systemPrompt))
	}
	if len(g.skills) > 0 {
		systemParts = append(systemParts, genai.NewPartFromText(
			skills.ToPrompt(g.skills),
		))
	}
	if len(systemParts) > 0 {
		config.SystemInstruction = &genai.Content{Parts: systemParts}
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		config.Temperature = &temp
	} else if g.config.Temperature > 0 {
		temp := float32(g.config.Temperature)
		config.Temperature = &temp
	}

	if req.TopP != nil {
		topP := float32(*req.TopP)
		config.TopP = &topP
	}

	if g.config.TopK != nil {
		topK := float32(*g.config.TopK)
		config.TopK = &topK
	}

	if len(req.StopSequences) > 0 {
		config.StopSequences = req.StopSequences
	}

	if len(req.Tools) > 0 {
		config.Tools = g.convertTools(req.Tools)
		config.ToolConfig = googleToolConfig(req.ToolChoice)
	}

	if len(g.config.SafetySettings) > 0 {
		config.SafetySettings = g.convertSafetySettings()
	}

	if req.ResponseSchema != nil {
		config.ResponseSchema = convertJSONSchemaToGenaiSchema(req.ResponseSchema)
	}
	if req.ResponseMIMEType != "" {
		config.ResponseMIMEType = req.ResponseMIMEType
	}

	return config
}

func combineGoogleSystemPrompts(configPrompt string, requestPrompt string) string {
	sections := make([]string, 0, 2)
	if trimmed := strings.TrimSpace(configPrompt); trimmed != "" {
		sections = append(sections, trimmed)
	}
	if trimmed := strings.TrimSpace(requestPrompt); trimmed != "" {
		sections = append(sections, trimmed)
	}
	return strings.Join(sections, "\n\n")
}

// googleToolConfig maps provider-agnostic ToolChoice to genai.ToolConfig.
// Returns nil when no explicit mode is requested (provider default applies).
func googleToolConfig(toolChoice string) *genai.ToolConfig {
	mode := googleFunctionCallingMode(toolChoice)
	if mode == "" {
		return nil
	}
	return &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode: mode,
		},
	}
}

func googleFunctionCallingMode(toolChoice string) genai.FunctionCallingConfigMode {
	switch strings.ToLower(strings.TrimSpace(toolChoice)) {
	case "auto":
		return genai.FunctionCallingConfigModeAuto
	case "any":
		return genai.FunctionCallingConfigModeAny
	case "none":
		return genai.FunctionCallingConfigModeNone
	default:
		return ""
	}
}

func googleThinkingLevel(reasoningEffort string) genai.ThinkingLevel {
	switch strings.ToLower(strings.TrimSpace(reasoningEffort)) {
	case "low":
		return genai.ThinkingLevelLow
	case "medium":
		return genai.ThinkingLevelMedium
	case "high", "xhigh":
		return genai.ThinkingLevelHigh
	default:
		return genai.ThinkingLevelUnspecified
	}
}

// extractGoogleRawContent returns the first candidate's Content from a
// GenerateContentResponse. This preserves thought parts with their signatures.
func extractGoogleRawContent(resp *genai.GenerateContentResponse) *genai.Content {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil
	}
	return resp.Candidates[0].Content
}

func extractGoogleStreamTextAndThought(resp *genai.GenerateContentResponse) (string, string) {
	if resp == nil {
		return "", ""
	}
	if text, thought := extractGoogleCandidateParts(resp.Candidates); text != "" || thought != "" {
		return text, thought
	}
	return resp.Text(), ""
}

func extractGoogleCandidateParts(candidates []*genai.Candidate) (string, string) {
	if len(candidates) == 0 || candidates[0] == nil || candidates[0].Content == nil {
		return "", ""
	}
	var text strings.Builder
	var thought strings.Builder
	for _, part := range candidates[0].Content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		if part.Thought {
			thought.WriteString(part.Text)
			continue
		}
		text.WriteString(part.Text)
	}
	return text.String(), thought.String()
}

type providerDeltaEmitter struct {
	last string
}

func (e *providerDeltaEmitter) Delta(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if e.last == "" {
		e.last = text
		return text
	}
	if strings.HasPrefix(text, e.last) {
		delta := text[len(e.last):]
		e.last = text
		return delta
	}
	e.last += text
	return text
}

// convertMessages converts generic messages to Gemini format
func (g *GoogleProvider) convertMessages(messages []Message) []*genai.Content {
	result := make([]*genai.Content, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			// System messages handled via SystemInstruction in config
			continue
		case RoleAssistant:
			// When raw model content is available (with thought signatures),
			// use it directly instead of reconstructing from flattened fields.
			if raw, ok := msg.Metadata[googleRawContentKey].(*genai.Content); ok && len(raw.Parts) > 0 {
				result = append(result, raw)
				continue
			}
		}

		content := &genai.Content{}
		switch msg.Role {
		case RoleUser:
			content.Role = "user"
		case RoleAssistant:
			content.Role = "model"
		case RoleTool:
			// Vertex AI / Code Assist REST API accepts only "user" and "model".
			// Function responses are carried as FunctionResponse parts inside a
			// "user" content block — not a separate "function" role.
			content.Role = "user"
		}

		// Tool messages carry their content inside the FunctionResponse part,
		// so skip adding a redundant text part for them.
		if msg.Role != RoleTool && msg.Content != "" {
			content.Parts = append(content.Parts, genai.NewPartFromText(msg.Content))
		}

		// Add tool results (function responses)
		if msg.Role == RoleTool {
			name := resolveGoogleFunctionResponseName(msg.ToolName, msg.ToolCallID)
			content.Parts = append(content.Parts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       msg.ToolCallID,
					Name:     name,
					Response: map[string]any{"result": msg.Content},
				},
			})
		}

		// Add function calls from assistant
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			json.Unmarshal([]byte(tc.Arguments), &args)

			content.Parts = append(content.Parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   tc.ID,
					Name: tc.Name,
					Args: args,
				},
			})
		}

		if len(content.Parts) == 0 {
			continue
		}
		result = append(result, content)
	}

	return result
}

// googleToolCallKey uniquely identifies a function call in a streaming response.
// Uses both ID and Name because Gemini may return empty IDs.
type googleToolCallKey struct {
	ID   string
	Name string
}

// resolveGoogleFunctionResponseName returns the function name for a tool-result
// message. Prefers the explicit ToolName; falls back to ToolCallID for backward
// compatibility with callers that only set the ID.
func resolveGoogleFunctionResponseName(toolName string, toolCallID string) string {
	if name := strings.TrimSpace(toolName); name != "" {
		return name
	}
	return strings.TrimSpace(toolCallID)
}

// convertTools converts generic tools to Gemini format
func (g *GoogleProvider) convertTools(tools []Tool) []*genai.Tool {
	declarations := make([]*genai.FunctionDeclaration, len(tools))

	for i, tool := range tools {
		// Convert the full JSON Schema to Gemini schema format
		schema := convertJSONSchemaToGenaiSchema(tool.Parameters)

		declarations[i] = &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schema,
		}
	}

	return []*genai.Tool{
		{FunctionDeclarations: declarations},
	}
}

// convertJSONSchemaToGenaiSchema converts a JSON Schema to genai.Schema
func convertJSONSchemaToGenaiSchema(schemaMap map[string]any) *genai.Schema {
	if schemaMap == nil {
		return &genai.Schema{Type: genai.TypeObject}
	}

	return &genai.Schema{
		Type:        extractSchemaType(schemaMap),
		Description: extractString(schemaMap, "description"),
		Properties:  extractProperties(schemaMap),
		Required:    extractStringSlice(schemaMap, "required"),
		Items:       extractItems(schemaMap),
		Enum:        extractStringSlice(schemaMap, "enum"),
	}
}

func extractSchemaType(schemaMap map[string]any) genai.Type {
	if t, ok := schemaMap["type"].(string); ok {
		return convertToGenaiType(t)
	}
	return genai.TypeObject
}

func extractString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func extractStringSlice(m map[string]any, key string) []string {
	vals, ok := m[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(vals))
	for _, v := range vals {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func extractProperties(schemaMap map[string]any) map[string]*genai.Schema {
	props, ok := schemaMap["properties"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]*genai.Schema, len(props))
	for name, propDef := range props {
		if propMap, ok := propDef.(map[string]any); ok {
			result[name] = convertJSONSchemaToGenaiSchema(propMap)
		}
	}
	return result
}

func extractItems(schemaMap map[string]any) *genai.Schema {
	if items, ok := schemaMap["items"].(map[string]any); ok {
		return convertJSONSchemaToGenaiSchema(items)
	}
	return nil
}

// convertSafetySettings converts config safety settings to Gemini format
func (g *GoogleProvider) convertSafetySettings() []*genai.SafetySetting {
	result := make([]*genai.SafetySetting, len(g.config.SafetySettings))

	for i, ss := range g.config.SafetySettings {
		result[i] = &genai.SafetySetting{
			Category:  genai.HarmCategory(ss.Category),
			Threshold: genai.HarmBlockThreshold(ss.Threshold),
		}
	}

	return result
}

// googleRawContentKey is the ProviderMetadata key for the raw genai.Content
// from the model's response. Preserving the original content (including thought
// parts with signatures) is required when replaying multi-turn tool-call
// conversations with thinking mode enabled.
const googleRawContentKey = "google_raw_content"

// convertResponse converts a Gemini response to generic format
func (g *GoogleProvider) convertResponse(result *genai.GenerateContentResponse, model string) *Response {
	resp := &Response{
		Model:            model,
		ProviderMetadata: make(map[string]any),
	}

	// Extract text content
	resp.Content = result.Text()

	// Extract function calls
	if fcs := result.FunctionCalls(); fcs != nil {
		for _, fc := range fcs {
			argsJSON, _ := json.Marshal(fc.Args)
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        fc.ID,
				Name:      fc.Name,
				Arguments: string(argsJSON),
			})
		}
	}

	// Preserve raw candidate content for multi-turn replay with thought signatures.
	if raw := extractGoogleRawContent(result); raw != nil {
		resp.ProviderMetadata[googleRawContentKey] = raw
	}

	// Extract usage
	if result.UsageMetadata != nil {
		resp.Usage = Usage{
			InputTokens:  int(result.UsageMetadata.PromptTokenCount),
			OutputTokens: int(result.UsageMetadata.CandidatesTokenCount),
			TotalTokens:  int(result.UsageMetadata.TotalTokenCount),
		}
	}

	// Determine stop reason
	if len(result.Candidates) > 0 {
		candidate := result.Candidates[0]
		switch candidate.FinishReason {
		case genai.FinishReasonStop:
			resp.StopReason = StopReasonEndTurn
		case genai.FinishReasonMaxTokens:
			resp.StopReason = StopReasonMaxTokens
		case genai.FinishReasonSafety:
			resp.StopReason = StopReasonError
		default:
			resp.StopReason = StopReasonEndTurn
		}
	}

	return resp
}

// convertToGenaiType converts a JSON schema type string to genai.Type
func convertToGenaiType(typeStr string) genai.Type {
	switch typeStr {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default:
		return genai.TypeString
	}
}
