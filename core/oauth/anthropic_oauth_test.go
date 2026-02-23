package oauth

import (
	"context"
	"net/url"
	"testing"
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
	if got := query.Get("state"); got != challenge.CodeVerifier {
		t.Fatalf("state = %q, want verifier", got)
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
