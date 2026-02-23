package oauth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestGoogleAuthResolve_PrefersStoreOverEnv(t *testing.T) {
	store := &recordingGoogleOAuthStore{
		loadAuth: &GoogleOAuthAuth{AccessToken: "store_access"},
	}

	service := NewGoogleAuthService(GoogleAuthServiceConfig{
		Store:       store,
		LookupEnv:   mapLookup(map[string]string{"GOOGLE_OAUTH_ACCESS_TOKEN": "env_access"}),
		DotEnvPaths: []string{filepath.Join(t.TempDir(), ".missing")},
	})

	auth, err := service.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if auth == nil {
		t.Fatal("Resolve() returned nil auth")
	}
	if auth.AccessToken != "store_access" {
		t.Fatalf("Resolve() access token = %q, want %q", auth.AccessToken, "store_access")
	}
}

func TestGoogleAuthResolve_FallsBackToEnvWhenStoreMissing(t *testing.T) {
	store := &recordingGoogleOAuthStore{loadAuth: nil}

	service := NewGoogleAuthService(GoogleAuthServiceConfig{
		Store:       store,
		LookupEnv:   mapLookup(map[string]string{"GOOGLE_OAUTH_ACCESS_TOKEN": "env_access"}),
		DotEnvPaths: []string{filepath.Join(t.TempDir(), ".missing")},
	})

	auth, err := service.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if auth == nil {
		t.Fatal("Resolve() returned nil auth")
	}
	if auth.AccessToken != "env_access" {
		t.Fatalf("Resolve() access token = %q, want %q", auth.AccessToken, "env_access")
	}
}

func TestGoogleAuthResolve_FallsBackToEnvWhenStoreErrors(t *testing.T) {
	store := &recordingGoogleOAuthStore{loadErr: errors.New("store read failed")}

	service := NewGoogleAuthService(GoogleAuthServiceConfig{
		Store:       store,
		LookupEnv:   mapLookup(map[string]string{"GOOGLE_OAUTH_ACCESS_TOKEN": "env_access"}),
		DotEnvPaths: []string{filepath.Join(t.TempDir(), ".missing")},
	})

	auth, err := service.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if auth == nil {
		t.Fatal("Resolve() returned nil auth")
	}
	if auth.AccessToken != "env_access" {
		t.Fatalf("Resolve() access token = %q, want %q", auth.AccessToken, "env_access")
	}
}

func TestGoogleAuthResolve_ReturnsStoreErrorWhenNoRuntimeSource(t *testing.T) {
	storeErr := errors.New("store read failed")
	store := &recordingGoogleOAuthStore{loadErr: storeErr}

	service := NewGoogleAuthService(GoogleAuthServiceConfig{
		Store:       store,
		LookupEnv:   mapLookup(nil),
		DotEnvPaths: []string{filepath.Join(t.TempDir(), ".missing")},
	})

	_, err := service.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected Resolve() to return store error")
	}
	if !errors.Is(err, storeErr) {
		t.Fatalf("Resolve() error = %v, want %v", err, storeErr)
	}
}
