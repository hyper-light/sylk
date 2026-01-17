package credentials

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/storage"
)

type mockKeychain struct {
	data      map[string]map[string]string
	available bool
}

func newMockKeychain(available bool) *mockKeychain {
	return &mockKeychain{
		data:      make(map[string]map[string]string),
		available: available,
	}
}

func (m *mockKeychain) Available() bool {
	return m.available
}

func (m *mockKeychain) Get(service, account string) (string, error) {
	if svc, ok := m.data[service]; ok {
		if val, ok := svc[account]; ok {
			return val, nil
		}
	}
	return "", nil
}

func (m *mockKeychain) Set(service, account, secret string) error {
	if m.data[service] == nil {
		m.data[service] = make(map[string]string)
	}
	m.data[service][account] = secret
	return nil
}

func (m *mockKeychain) Delete(service, account string) error {
	if svc, ok := m.data[service]; ok {
		delete(svc, account)
	}
	return nil
}

func TestManagerGetFromEnv(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Config: tmpDir,
	}

	t.Setenv("ANTHROPIC_API_KEY", "test-key-from-env")

	mgr, err := NewManager(dirs, "default")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	key, err := mgr.GetAPIKey("anthropic")
	if err != nil {
		t.Fatalf("GetAPIKey failed: %v", err)
	}

	if key != "test-key-from-env" {
		t.Errorf("key: got %s, want test-key-from-env", key)
	}
}

func TestManagerSetAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Config: tmpDir,
	}

	mgr, err := NewManager(dirs, "default")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	mgr.keychain = newMockKeychain(false)

	ctx := context.Background()
	if err := mgr.SetAPIKey(ctx, "test-provider", "test-secret", nil); err != nil {
		t.Fatalf("SetAPIKey failed: %v", err)
	}

	key, err := mgr.GetAPIKey("test-provider")
	if err != nil {
		t.Fatalf("GetAPIKey failed: %v", err)
	}

	if key != "test-secret" {
		t.Errorf("key: got %s, want test-secret", key)
	}
}

func TestManagerWithKeychain(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Config: tmpDir,
	}

	mgr, err := NewManager(dirs, "default")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	mockKc := newMockKeychain(true)
	mgr.keychain = mockKc

	ctx := context.Background()
	if err := mgr.SetAPIKey(ctx, "anthropic", "keychain-secret", nil); err != nil {
		t.Fatalf("SetAPIKey failed: %v", err)
	}

	if mockKc.data["sylk:default"]["anthropic"] != "keychain-secret" {
		t.Error("Secret should be stored in keychain")
	}

	key, err := mgr.GetAPIKey("anthropic")
	if err != nil {
		t.Fatalf("GetAPIKey failed: %v", err)
	}

	if key != "keychain-secret" {
		t.Errorf("key: got %s, want keychain-secret", key)
	}
}

func TestManagerDelete(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Config: tmpDir,
	}

	mgr, err := NewManager(dirs, "default")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	mgr.keychain = newMockKeychain(false)

	ctx := context.Background()
	_ = mgr.SetAPIKey(ctx, "to-delete", "secret", nil)

	if err := mgr.DeleteAPIKey("to-delete"); err != nil {
		t.Fatalf("DeleteAPIKey failed: %v", err)
	}

	_, err = mgr.GetAPIKey("to-delete")
	if err == nil {
		t.Error("Key should be deleted")
	}
}

func TestManagerProfiles(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Config: tmpDir,
	}

	mgr, err := NewManager(dirs, "profile1")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	mgr.keychain = newMockKeychain(false)

	ctx := context.Background()
	_ = mgr.SetAPIKey(ctx, "provider", "secret1", nil)

	mgr.SwitchProfile("profile2")
	_ = mgr.SetAPIKey(ctx, "provider", "secret2", nil)

	mgr.SwitchProfile("profile1")
	key1, _ := mgr.GetAPIKey("provider")
	if key1 != "secret1" {
		t.Errorf("profile1 key: got %s, want secret1", key1)
	}

	mgr.SwitchProfile("profile2")
	key2, _ := mgr.GetAPIKey("provider")
	if key2 != "secret2" {
		t.Errorf("profile2 key: got %s, want secret2", key2)
	}
}

func TestProviderEnvVar(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"google", "GOOGLE_API_KEY"},
		{"github", "GITHUB_TOKEN"},
		{"custom", "CUSTOM_API_KEY"},
		{"myProvider", "MY_PROVIDER_API_KEY"},
	}

	for _, tt := range tests {
		got := providerEnvVar(tt.provider)
		if got != tt.want {
			t.Errorf("providerEnvVar(%s): got %s, want %s", tt.provider, got, tt.want)
		}
	}
}

func TestListProviders(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Config: tmpDir,
	}

	mgr, err := NewManager(dirs, "default")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	mgr.keychain = newMockKeychain(false)

	ctx := context.Background()
	_ = mgr.SetAPIKey(ctx, "anthropic", "key1", nil)
	_ = mgr.SetAPIKey(ctx, "openai", "key2", nil)

	providers, err := mgr.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}

	if len(providers) != 2 {
		t.Errorf("providers: got %d, want 2", len(providers))
	}
}
