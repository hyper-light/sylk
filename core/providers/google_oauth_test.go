package providers

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/oauth"
	"google.golang.org/genai"
)

type mockGoogleAuthService struct {
	auth           *oauth.GoogleOAuthAuth
	err            error
	beginChallenge *oauth.GoogleOAuthChallenge
	beginErr       error
	completeAuth   *oauth.GoogleOAuthAuth
	completeErr    error
	completeDelay  time.Duration
	saveCalls      int
	saveErr        error
}

func (m *mockGoogleAuthService) BeginAuth(context.Context) (*oauth.GoogleOAuthChallenge, error) {
	if m.beginErr != nil {
		return nil, m.beginErr
	}
	return m.beginChallenge, nil
}

func (m *mockGoogleAuthService) CompleteAuth(ctx context.Context, _ *oauth.GoogleOAuthChallenge, _ time.Duration) (*oauth.GoogleOAuthAuth, error) {
	if m.completeDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.completeDelay):
		}
	}
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return m.completeAuth, nil
}

func (m *mockGoogleAuthService) Refresh(context.Context, *oauth.GoogleOAuthAuth) (*oauth.GoogleOAuthAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *mockGoogleAuthService) Resolve(context.Context) (*oauth.GoogleOAuthAuth, error) {
	return m.auth, m.err
}

func (m *mockGoogleAuthService) Save(context.Context, *oauth.GoogleOAuthAuth) error {
	m.saveCalls++
	return m.saveErr
}

func (m *mockGoogleAuthService) Load(context.Context) (*oauth.GoogleOAuthAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *mockGoogleAuthService) Delete(context.Context) error {
	return errors.New("not implemented")
}

func TestGoogleConfigValidate_OAuthModeAllowsMissingAPIKey(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeOAuth
	cfg.APIKey = ""
	cfg.UseVertexAI = true
	cfg.ProjectID = "proj_test"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected oauth config validation to pass, got: %v", err)
	}
}

func TestDefaultGoogleConfig_UsesOAuthAuthMode(t *testing.T) {
	cfg := DefaultGoogleConfig()
	if cfg.AuthMode != GoogleAuthModeOAuth {
		t.Fatalf("expected default google auth mode %q, got %q", GoogleAuthModeOAuth, cfg.AuthMode)
	}
}

func TestGoogleConfigValidate_APIKeyModeRequiresAPIKey(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeAPIKey
	cfg.APIKey = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected api_key mode to require API key")
	}
}

func TestGoogleConfigValidate_ServiceAccountModeAllowsMissingAPIKey(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeServiceAccount
	cfg.APIKey = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected service_account config validation to pass, got: %v", err)
	}
}

func TestHydrateGoogleConfig_OAuthAppliesResolvedAuth(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeOAuth
	cfg.ProjectID = ""
	cfg.Location = ""
	cfg.APIKey = ""

	authSvc := &mockGoogleAuthService{
		auth: &oauth.GoogleOAuthAuth{
			AccessToken: "access_token",
			ProjectID:   "proj_resolved",
			Location:    "us-east1",
		},
	}

	if err := hydrateGoogleConfig(context.Background(), &cfg, authSvc); err != nil {
		t.Fatalf("hydrateGoogleConfig() error: %v", err)
	}
	if !cfg.UseVertexAI {
		t.Fatal("expected oauth mode to force UseVertexAI")
	}
	if cfg.ProjectID != "proj_resolved" {
		t.Fatalf("expected resolved project_id, got %q", cfg.ProjectID)
	}
	if cfg.Location != "us-east1" {
		t.Fatalf("expected resolved location, got %q", cfg.Location)
	}
}

func TestHydrateGoogleConfig_OAuthWithoutProjectUsesGeminiAPI(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeOAuth
	cfg.ProjectID = ""
	cfg.UseVertexAI = false
	cfg.Location = ""
	cfg.APIKey = ""

	authSvc := &mockGoogleAuthService{
		auth: &oauth.GoogleOAuthAuth{
			AccessToken: "access_token",
			Location:    "us-east1",
		},
	}

	if err := hydrateGoogleConfig(context.Background(), &cfg, authSvc); err != nil {
		t.Fatalf("hydrateGoogleConfig() error: %v", err)
	}
	if cfg.UseVertexAI {
		t.Fatal("expected oauth without project to keep Gemini API backend")
	}
	if cfg.ProjectID != "" {
		t.Fatalf("expected empty project_id, got %q", cfg.ProjectID)
	}
	if cfg.Location != "us-east1" {
		t.Fatalf("expected resolved location, got %q", cfg.Location)
	}
}

func TestHydrateGoogleConfig_OAuthReturnsResolveError(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeOAuth
	authSvc := &mockGoogleAuthService{err: oauth.ErrGoogleAuthNotConfigured}
	originalResolver := googleProviderAPIKeyResolver
	googleProviderAPIKeyResolver = func() string { return "" }
	t.Cleanup(func() {
		googleProviderAPIKeyResolver = originalResolver
	})

	err := hydrateGoogleConfig(context.Background(), &cfg, authSvc)
	if err == nil {
		t.Fatal("expected resolve error for unconfigured oauth")
	}
}

func TestHydrateGoogleConfig_OAuthFallsBackToAPIKey(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeOAuth
	cfg.APIKey = "fallback_key"
	cfg.UseVertexAI = true
	authSvc := &mockGoogleAuthService{err: oauth.ErrGoogleAuthNotConfigured}

	if err := hydrateGoogleConfig(context.Background(), &cfg, authSvc); err != nil {
		t.Fatalf("hydrateGoogleConfig() error: %v", err)
	}
	if cfg.AuthMode != GoogleAuthModeAPIKey {
		t.Fatalf("expected auth mode fallback to %q, got %q", GoogleAuthModeAPIKey, cfg.AuthMode)
	}
	if cfg.UseVertexAI {
		t.Fatal("expected UseVertexAI false after api key fallback")
	}
}

func TestHydrateGoogleConfig_ServiceAccountMode(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv(
		"GOOGLE_SERVICE_ACCOUNT_JSON",
		`{"type":"service_account","project_id":"proj_sa","client_email":"svc@test.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"}`,
	)
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeServiceAccount
	cfg.ProjectID = ""
	cfg.Location = ""
	cfg.UseVertexAI = false

	if err := hydrateGoogleConfig(context.Background(), &cfg, &mockGoogleAuthService{}); err != nil {
		t.Fatalf("hydrateGoogleConfig() error: %v", err)
	}
	if cfg.ProjectID != "proj_sa" {
		t.Fatalf("expected project from service account, got %q", cfg.ProjectID)
	}
	if !cfg.UseVertexAI {
		t.Fatal("expected service account mode to force UseVertexAI")
	}
	if got := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); got == "" {
		t.Fatal("expected GOOGLE_APPLICATION_CREDENTIALS to be set")
	}
}

func TestHydrateGoogleConfig_OAuthFallsBackToServiceAccount(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv(
		"GOOGLE_SERVICE_ACCOUNT_JSON",
		`{"type":"service_account","project_id":"proj_sa","client_email":"svc@test.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"}`,
	)
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeOAuth
	cfg.APIKey = ""
	authSvc := &mockGoogleAuthService{err: oauth.ErrGoogleAuthNotConfigured}

	if err := hydrateGoogleConfig(context.Background(), &cfg, authSvc); err != nil {
		t.Fatalf("hydrateGoogleConfig() error: %v", err)
	}
	if cfg.AuthMode != GoogleAuthModeServiceAccount {
		t.Fatalf("expected auth mode fallback to %q, got %q", GoogleAuthModeServiceAccount, cfg.AuthMode)
	}
	if !cfg.UseVertexAI {
		t.Fatal("expected UseVertexAI true in service_account fallback")
	}
}

func TestBuildGoogleClientConfig_OAuthUsesVertexAndHTTPClient(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeOAuth
	cfg.UseVertexAI = true
	cfg.ProjectID = "proj_test"
	cfg.Location = "us-central1"

	clientCfg, err := buildGoogleClientConfig(cfg, &mockGoogleAuthService{})
	if err != nil {
		t.Fatalf("buildGoogleClientConfig() error: %v", err)
	}
	if clientCfg.Backend != genai.BackendVertexAI {
		t.Fatalf("expected vertex backend, got %v", clientCfg.Backend)
	}
	if clientCfg.HTTPClient == nil {
		t.Fatal("expected oauth HTTP client to be configured")
	}
}

func TestBuildGoogleClientConfig_OAuthWithoutProjectUsesGeminiAPI(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.AuthMode = GoogleAuthModeOAuth
	cfg.UseVertexAI = false
	cfg.ProjectID = ""
	cfg.APIKey = ""

	clientCfg, err := buildGoogleClientConfig(cfg, &mockGoogleAuthService{})
	if err != nil {
		t.Fatalf("buildGoogleClientConfig() error: %v", err)
	}
	if clientCfg.Backend != genai.BackendGeminiAPI {
		t.Fatalf("expected gemini backend, got %v", clientCfg.Backend)
	}
	if clientCfg.HTTPClient == nil {
		t.Fatal("expected oauth HTTP client to be configured")
	}
}

func TestGoogleProviderStartOAuthLogin_ReturnsChallengeImmediately(t *testing.T) {
	authSvc := &mockGoogleAuthService{
		beginChallenge: &oauth.GoogleOAuthChallenge{
			AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth?foo=bar",
			RedirectURL:  "http://127.0.0.1:12345/oauth2callback",
			State:        "state_123",
			CallbackHost: "127.0.0.1",
			CallbackPort: 12345,
			CallbackPath: "/oauth2callback",
		},
		completeAuth: &oauth.GoogleOAuthAuth{
			AuthMode:    "oauth",
			AccessToken: "token_new",
			ProjectID:   "proj_test",
			Location:    "us-central1",
		},
		completeDelay: 120 * time.Millisecond,
	}

	cfg := DefaultGoogleConfig()
	cfg.APIKey = "test_key"
	cfg.AuthMode = GoogleAuthModeAPIKey
	provider, err := NewGoogleProviderWithAuthService(context.Background(), cfg, authSvc)
	if err != nil {
		t.Fatalf("NewGoogleProviderWithAuthService() error: %v", err)
	}

	start := time.Now()
	session, err := provider.StartOAuthLogin(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("StartOAuthLogin() error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("StartOAuthLogin() should return immediately, took %v", elapsed)
	}
	if session == nil || session.Challenge == nil {
		t.Fatal("expected non-nil oauth challenge")
	}
	if session.Challenge.AuthURL == "" {
		t.Fatal("expected challenge auth URL")
	}

	select {
	case result := <-session.Results:
		if result.Err != nil {
			t.Fatalf("unexpected oauth result error: %v", result.Err)
		}
		if result.Auth == nil {
			t.Fatal("expected oauth auth payload")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for oauth completion")
	}

	if authSvc.saveCalls != 1 {
		t.Fatalf("expected Save() call count 1, got %d", authSvc.saveCalls)
	}
}
