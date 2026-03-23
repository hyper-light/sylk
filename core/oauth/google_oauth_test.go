package oauth

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateGoogleAuth_AllowsMissingProjectID(t *testing.T) {
	auth := &GoogleOAuthAuth{
		AccessToken: "access_token",
		ProjectID:   "",
	}
	if err := validateGoogleAuth(auth); err != nil {
		t.Fatalf("expected missing project_id to be allowed, got %v", err)
	}
}

func TestValidateGoogleAuth_RequiresAccessToken(t *testing.T) {
	auth := &GoogleOAuthAuth{
		AccessToken: "",
	}
	if err := validateGoogleAuth(auth); err != ErrGoogleMissingToken {
		t.Fatalf("expected ErrGoogleMissingToken, got %v", err)
	}
}

func TestGoogleAuthService_Resolve_InvalidGrantPreservesStoredAuth(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "google_oauth.yaml")
	store := NewFileGoogleOAuthStore(storePath)
	if err := store.Save(&GoogleOAuthAuth{
		AuthMode:          googleOAuthAuthMode,
		AccessToken:       "existing_access",
		RefreshToken:      "existing_refresh",
		AccessTokenExpiry: time.Now().Add(-time.Minute).UTC(),
		ProjectID:         "existing-project",
		Location:          "us-central1",
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

	svc := NewGoogleAuthService(GoogleAuthServiceConfig{
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
	if preserved.ProjectID != "existing-project" {
		t.Fatalf("stored project id = %q, want existing-project", preserved.ProjectID)
	}
}
