package oauth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestEncryptedFileGoogleOAuthStoreRoundTrip(t *testing.T) {
	t.Helper()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "google_oauth_auth.yaml")
	store := NewEncryptedFileGoogleOAuthStore(path, EncryptedFileGoogleOAuthStoreConfig{
		KeyPath: filepath.Join(tmp, "google_oauth_auth.key"),
	})

	now := time.Now().UTC().Truncate(time.Second)
	auth := &GoogleOAuthAuth{
		AuthMode:          "oauth",
		AccessToken:       "ya29.secret-token",
		RefreshToken:      "refresh-secret",
		TokenType:         "Bearer",
		Scope:             "openid profile email",
		ObtainedAt:        now,
		AccessTokenExpiry: now.Add(time.Hour),
		ProjectID:         "test-project",
		Location:          "us-central1",
	}
	if err := store.Save(auth); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, auth.AccessToken) || strings.Contains(text, auth.RefreshToken) {
		t.Fatalf("encrypted store leaked plaintext token material: %s", text)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil auth")
	}
	if loaded.AccessToken != auth.AccessToken {
		t.Fatalf("AccessToken mismatch: got %q want %q", loaded.AccessToken, auth.AccessToken)
	}
	if loaded.RefreshToken != auth.RefreshToken {
		t.Fatalf("RefreshToken mismatch: got %q want %q", loaded.RefreshToken, auth.RefreshToken)
	}
}

func TestEncryptedFileGoogleOAuthStoreLoadsLegacyPlaintext(t *testing.T) {
	t.Helper()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "google_oauth_auth.yaml")
	auth := &GoogleOAuthAuth{
		AuthMode:     "oauth",
		AccessToken:  "legacy-token",
		RefreshToken: "legacy-refresh",
	}
	raw, err := yaml.Marshal(auth)
	if err != nil {
		t.Fatalf("yaml.Marshal() error: %v", err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	store := NewEncryptedFileGoogleOAuthStore(path, EncryptedFileGoogleOAuthStoreConfig{
		KeyPath: filepath.Join(tmp, "google_oauth_auth.key"),
	})
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil auth")
	}
	if loaded.AccessToken != auth.AccessToken {
		t.Fatalf("AccessToken mismatch: got %q want %q", loaded.AccessToken, auth.AccessToken)
	}
}

func TestFallbackGoogleOAuthStoreSaveWritesBothStores(t *testing.T) {
	t.Helper()

	primary := &recordingGoogleOAuthStore{}
	secondary := &recordingGoogleOAuthStore{}
	store := NewFallbackGoogleOAuthStore(primary, secondary)

	if err := store.Save(&GoogleOAuthAuth{AccessToken: "tok"}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if primary.saveCalls != 1 {
		t.Fatalf("primary save calls = %d, want 1", primary.saveCalls)
	}
	if secondary.saveCalls != 1 {
		t.Fatalf("secondary save calls = %d, want 1", secondary.saveCalls)
	}
}

func TestFallbackGoogleOAuthStoreSaveFallsBackToSecondary(t *testing.T) {
	t.Helper()

	primary := &recordingGoogleOAuthStore{saveErr: errors.New("primary failed")}
	secondary := &recordingGoogleOAuthStore{}
	store := NewFallbackGoogleOAuthStore(primary, secondary)

	if err := store.Save(&GoogleOAuthAuth{AccessToken: "tok"}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if primary.saveCalls != 1 {
		t.Fatalf("primary save calls = %d, want 1", primary.saveCalls)
	}
	if secondary.saveCalls != 1 {
		t.Fatalf("secondary save calls = %d, want 1", secondary.saveCalls)
	}
}

type recordingGoogleOAuthStore struct {
	saveCalls int
	saveErr   error
	loadAuth  *GoogleOAuthAuth
	loadErr   error
	deleteErr error
}

func (s *recordingGoogleOAuthStore) Save(_ *GoogleOAuthAuth) error {
	s.saveCalls++
	return s.saveErr
}

func (s *recordingGoogleOAuthStore) Load() (*GoogleOAuthAuth, error) {
	return s.loadAuth, s.loadErr
}

func (s *recordingGoogleOAuthStore) Delete() error {
	return s.deleteErr
}
