package oauth

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAnthropicBeginAuthUsesCodeFlowRedirect(t *testing.T) {
	service := NewAnthropicAuthService(AnthropicAuthServiceConfig{})
	challenge, err := service.BeginAuth(context.Background())
	if err != nil {
		t.Fatalf("begin auth: %v", err)
	}
	if challenge.RedirectURL != defaultAnthropicOAuthRedirectURL {
		t.Fatalf("redirect url = %q, want %q", challenge.RedirectURL, defaultAnthropicOAuthRedirectURL)
	}
	authURL, err := url.Parse(challenge.AuthURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	query := authURL.Query()
	if got := query.Get("client_id"); got != defaultAnthropicOAuthClientID {
		t.Fatalf("client_id = %q, want %q", got, defaultAnthropicOAuthClientID)
	}
	if got := query.Get("redirect_uri"); got != defaultAnthropicOAuthRedirectURL {
		t.Fatalf("redirect_uri = %q, want %q", got, defaultAnthropicOAuthRedirectURL)
	}
	if got := query.Get("code"); got != "true" {
		t.Fatalf("code = %q, want true", got)
	}
	if got := query.Get("state"); got != challenge.State {
		t.Fatalf("state = %q, want %q", got, challenge.State)
	}
	if challenge.State == "" {
		t.Fatal("expected non-empty state")
	}
	if challenge.State == challenge.CodeVerifier {
		t.Fatal("expected state to differ from verifier")
	}
}

func TestParseAnthropicAuthorizationCode(t *testing.T) {
	code, state, err := parseAnthropicAuthorizationCode("abc123#state456")
	if err != nil {
		t.Fatalf("parse split code: %v", err)
	}
	if code != "abc123" || state != "state456" {
		t.Fatalf("parsed = (%q, %q), want (%q, %q)", code, state, "abc123", "state456")
	}

	code, state, err = parseAnthropicAuthorizationCode("https://console.anthropic.com/oauth/code/callback?code=xyz#uvw")
	if err != nil {
		t.Fatalf("parse callback url: %v", err)
	}
	if code != "xyz" || state != "uvw" {
		t.Fatalf("parsed callback = (%q, %q), want (%q, %q)", code, state, "xyz", "uvw")
	}
}

func TestAnthropicCompleteAuthCodeStateMismatch(t *testing.T) {
	service := NewAnthropicAuthService(AnthropicAuthServiceConfig{})
	_, err := service.CompleteAuthCode(
		context.Background(),
		&AnthropicOAuthChallenge{
			AuthURL:      "https://claude.ai/oauth/authorize",
			RedirectURL:  defaultAnthropicOAuthRedirectURL,
			State:        "expected",
			CodeVerifier: "verifier",
		},
		"code#unexpected",
	)
	if err == nil {
		t.Fatalf("expected state mismatch error")
	}
	if err.Error() != "anthropic oauth state mismatch" {
		t.Fatalf("error = %q, want state mismatch", err.Error())
	}
}

func TestAnthropicAuthService_Resolve_InvalidGrantPreservesStoredAuth(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "anthropic_oauth.yaml")
	store := NewDefaultAnthropicOAuthStore(storePath, nil)
	if err := store.Save(&AnthropicOAuthAuth{
		AuthMode:          anthropicOAuthAuthMode,
		AccessToken:       "existing_access",
		RefreshToken:      "existing_refresh",
		AccessTokenExpiry: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/oauth/token" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			return jsonResponse(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"refresh token invalid"}`), nil
		}),
	}

	svc := NewAnthropicAuthService(AnthropicAuthServiceConfig{
		TokenURL:    "https://oauth.test/oauth/token",
		HTTPClient:  client,
		Store:       store,
		DotEnvPaths: []string{},
	})

	if _, err := svc.Resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("Resolve() error = %v, want invalid_grant", err)
	}

	preserved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() after failed refresh: %v", err)
	}
	if preserved == nil {
		t.Fatal("expected stored auth to remain after failed refresh")
	}
	if preserved.AccessToken != "existing_access" {
		t.Fatalf("stored access token = %q, want existing_access", preserved.AccessToken)
	}
	if preserved.RefreshToken != "existing_refresh" {
		t.Fatalf("stored refresh token = %q, want existing_refresh", preserved.RefreshToken)
	}
}

func TestNewAnthropicAuthService_UsesReferenceOAuthDefaults(t *testing.T) {
	service, ok := NewAnthropicAuthService(AnthropicAuthServiceConfig{}).(*anthropicAuthService)
	if !ok {
		t.Fatalf("service type = %T, want *anthropicAuthService", service)
	}

	if service.tokenURL != "https://console.anthropic.com/v1/oauth/token" {
		t.Fatalf("token url = %q, want %q", service.tokenURL, "https://console.anthropic.com/v1/oauth/token")
	}

	wantScopes := []string{
		"org:create_api_key",
		"user:profile",
		"user:inference",
	}
	if service.scopes != strings.Join(wantScopes, " ") {
		t.Fatalf("scopes = %q, want %q", service.scopes, strings.Join(wantScopes, " "))
	}
}
