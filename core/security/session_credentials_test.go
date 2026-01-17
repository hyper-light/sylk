package security

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type mockBaseCredentialManager struct {
	mu            sync.RWMutex
	credentials   map[string]string
	profiles      []string
	activeProfile string
}

func newMockBaseManager() *mockBaseCredentialManager {
	return &mockBaseCredentialManager{
		credentials: map[string]string{
			"openai":    "base-openai-key",
			"github":    "base-github-key",
			"anthropic": "base-anthropic-key",
		},
		profiles:      []string{"default", "work", "personal"},
		activeProfile: "default",
	}
}

func (m *mockBaseCredentialManager) GetAPIKey(provider string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if key, ok := m.credentials[provider]; ok {
		return key, nil
	}
	return "", errors.New("provider not found")
}

func (m *mockBaseCredentialManager) GetProfile() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeProfile
}

func (m *mockBaseCredentialManager) SetProfile(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeProfile = name
	return nil
}

func (m *mockBaseCredentialManager) ListProfiles() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]string, len(m.profiles))
	copy(result, m.profiles)
	return result
}

func TestSessionCredentialManager_GetAPIKey_FromBase(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	key, err := mgr.GetAPIKey("openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "base-openai-key" {
		t.Errorf("expected base-openai-key, got %s", key)
	}
}

func TestSessionCredentialManager_GetAPIKey_FromTemp(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	err := mgr.SetTemporaryCredential("openai", "temp-openai-key", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	key, err := mgr.GetAPIKey("openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "temp-openai-key" {
		t.Errorf("expected temp-openai-key, got %s", key)
	}
}

func TestSessionCredentialManager_ResolutionOrder(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	key, _ := mgr.GetAPIKey("openai")
	if key != "base-openai-key" {
		t.Error("without temp, should get base key")
	}

	mgr.SetTemporaryCredential("openai", "temp-key", time.Hour)
	key, _ = mgr.GetAPIKey("openai")
	if key != "temp-key" {
		t.Error("with temp, should get temp key")
	}

	mgr.RemoveTemporaryCredential("openai")
	key, _ = mgr.GetAPIKey("openai")
	if key != "base-openai-key" {
		t.Error("after removing temp, should get base key again")
	}
}

func TestSessionCredentialManager_TempCredentialExpires(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	err := mgr.SetTemporaryCredential("openai", "temp-key", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	key, err := mgr.GetAPIKey("openai")
	if err != nil {
		t.Fatalf("should work before expiry: %v", err)
	}
	if key != "temp-key" {
		t.Error("should get temp key before expiry")
	}

	time.Sleep(100 * time.Millisecond)

	_, err = mgr.GetAPIKey("openai")
	if !errors.Is(err, ErrCredentialExpired) {
		t.Errorf("expected expired error, got: %v", err)
	}
}

func TestSessionCredentialManager_TempCredentialMaxTTL(t *testing.T) {
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		MaxTempTTL: time.Hour,
		SessionID:  "test-session",
	})

	err := mgr.SetTemporaryCredential("openai", "key", 2*time.Hour)
	if !errors.Is(err, ErrTempCredentialMaxTTL) {
		t.Errorf("expected max TTL error, got: %v", err)
	}

	err = mgr.SetTemporaryCredential("openai", "key", 30*time.Minute)
	if err != nil {
		t.Errorf("should allow TTL under max: %v", err)
	}
}

func TestSessionCredentialManager_ProfileOverride(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	err := mgr.SetProfileOverride("work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mgr.GetProfileOverride() != "work" {
		t.Error("profile override should be 'work'")
	}

	if base.GetProfile() != "work" {
		t.Error("base manager profile should be updated")
	}
}

func TestSessionCredentialManager_ProfileOverrideNotFound(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	err := mgr.SetProfileOverride("nonexistent")
	if !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("expected profile not found error, got: %v", err)
	}
}

func TestSessionCredentialManager_ClearSession(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	mgr.SetTemporaryCredential("openai", "temp-1", time.Hour)
	mgr.SetTemporaryCredential("github", "temp-2", time.Hour)
	mgr.SetProfileOverride("work")

	mgr.ClearSession()

	temps := mgr.ListTempCredentials()
	if len(temps) != 0 {
		t.Errorf("expected no temp credentials after cleanup, got %d", len(temps))
	}

	if mgr.GetProfileOverride() != "" {
		t.Error("profile override should be cleared")
	}
}

func TestSessionCredentialManager_ClearSessionIdempotent(t *testing.T) {
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		SessionID: "test-session",
	})

	mgr.SetTemporaryCredential("openai", "temp", time.Hour)

	mgr.ClearSession()
	mgr.ClearSession()
	mgr.ClearSession()

	temps := mgr.ListTempCredentials()
	if len(temps) != 0 {
		t.Error("multiple cleanups should not fail")
	}
}

func TestSessionCredentialManager_ListTempCredentials(t *testing.T) {
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		SessionID: "test-session",
	})

	mgr.SetTemporaryCredential("openai", "key1", time.Hour)
	mgr.SetTemporaryCredential("github", "key2", time.Hour)
	mgr.SetTemporaryCredential("anthropic", "key3", time.Hour)

	providers := mgr.ListTempCredentials()
	if len(providers) != 3 {
		t.Errorf("expected 3 providers, got %d", len(providers))
	}
}

func TestSessionCredentialManager_GetTempCredentialInfo(t *testing.T) {
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		SessionID: "test-session",
	})

	mgr.SetTemporaryCredential("openai", "key", time.Hour)

	expiresAt, exists := mgr.GetTempCredentialInfo("openai")
	if !exists {
		t.Error("expected credential to exist")
	}
	if time.Until(expiresAt) < 59*time.Minute {
		t.Error("expiry should be ~1 hour from now")
	}

	_, exists = mgr.GetTempCredentialInfo("nonexistent")
	if exists {
		t.Error("nonexistent credential should not exist")
	}
}

func TestSessionCredentialManager_CleanupExpired(t *testing.T) {
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		SessionID: "test-session",
	})

	mgr.SetTemporaryCredential("short", "key1", 50*time.Millisecond)
	mgr.SetTemporaryCredential("long", "key2", time.Hour)

	time.Sleep(100 * time.Millisecond)

	removed := mgr.CleanupExpired()
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	temps := mgr.ListTempCredentials()
	if len(temps) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(temps))
	}
}

func TestSessionCredentialManager_Concurrent(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%3 == 0 {
				mgr.SetTemporaryCredential("openai", "temp", time.Hour)
			} else if n%3 == 1 {
				mgr.GetAPIKey("openai")
			} else {
				mgr.ListTempCredentials()
			}
		}(i)
	}

	wg.Wait()
}

func TestSessionCredentialManager_NoBaseManager(t *testing.T) {
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		SessionID: "test-session",
	})

	_, err := mgr.GetAPIKey("openai")
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Errorf("expected provider not configured error, got: %v", err)
	}

	err = mgr.SetProfileOverride("work")
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Errorf("expected provider not configured error, got: %v", err)
	}
}

func TestSessionCredentialManager_RemoveTemporaryCredential(t *testing.T) {
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		SessionID: "test-session",
	})

	mgr.SetTemporaryCredential("openai", "key", time.Hour)

	removed := mgr.RemoveTemporaryCredential("openai")
	if !removed {
		t.Error("expected removal to succeed")
	}

	removed = mgr.RemoveTemporaryCredential("openai")
	if removed {
		t.Error("second removal should return false")
	}
}

func TestSessionCredentialManager_ClearProfileOverride(t *testing.T) {
	base := newMockBaseManager()
	mgr := NewSessionCredentialManager(SessionCredentialConfig{
		BaseManager: base,
		SessionID:   "test-session",
	})

	mgr.SetProfileOverride("work")
	if mgr.GetProfileOverride() != "work" {
		t.Error("override should be set")
	}

	mgr.ClearProfileOverride()
	if mgr.GetProfileOverride() != "" {
		t.Error("override should be cleared")
	}
}
