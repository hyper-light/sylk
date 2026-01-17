package credentials

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/adalundhe/sylk/core/storage"
)

type Manager struct {
	dirs     *storage.Dirs
	keychain KeychainProvider
	encFile  *EncryptedFileStore
	profile  string
	mu       sync.RWMutex
}

type KeychainProvider interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
	Available() bool
}

type ProviderVerifier interface {
	VerifyAPIKey(ctx context.Context, provider, key string) error
}

func NewManager(dirs *storage.Dirs, profile string) (*Manager, error) {
	if profile == "" {
		profile = "default"
	}

	encFile, err := NewEncryptedFileStore(dirs.ConfigDir("credentials"))
	if err != nil {
		return nil, fmt.Errorf("encrypted store: %w", err)
	}

	return &Manager{
		dirs:     dirs,
		keychain: newPlatformKeychain(),
		encFile:  encFile,
		profile:  profile,
	}, nil
}

func (m *Manager) GetAPIKey(provider string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	envVar := providerEnvVar(provider)
	if key := os.Getenv(envVar); key != "" {
		return key, nil
	}

	if m.keychain.Available() {
		service := fmt.Sprintf("sylk:%s", m.profile)
		if key, err := m.keychain.Get(service, provider); err == nil && key != "" {
			return key, nil
		}
	}

	if key, err := m.encFile.Get(m.profile, provider); err == nil && key != "" {
		return key, nil
	}

	return "", fmt.Errorf("no credentials found for %s", provider)
}

func (m *Manager) SetAPIKey(ctx context.Context, provider, key string, verifier ProviderVerifier) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if verifier != nil {
		if err := verifier.VerifyAPIKey(ctx, provider, key); err != nil {
			return fmt.Errorf("invalid API key: %w", err)
		}
	}

	if m.keychain.Available() {
		service := fmt.Sprintf("sylk:%s", m.profile)
		return m.keychain.Set(service, provider, key)
	}

	return m.encFile.Set(m.profile, provider, key)
}

func (m *Manager) DeleteAPIKey(provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.keychain.Available() {
		service := fmt.Sprintf("sylk:%s", m.profile)
		_ = m.keychain.Delete(service, provider)
	}

	return m.encFile.Delete(m.profile, provider)
}

func (m *Manager) ListProviders() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.encFile.ListKeys(m.profile)
}

func (m *Manager) SwitchProfile(profile string) {
	m.mu.Lock()
	m.profile = profile
	m.mu.Unlock()
}

func (m *Manager) CurrentProfile() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profile
}

func (m *Manager) KeychainAvailable() bool {
	return m.keychain.Available()
}

func providerEnvVar(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "google":
		return "GOOGLE_API_KEY"
	case "github":
		return "GITHUB_TOKEN"
	default:
		return fmt.Sprintf("%s_API_KEY", toUpperSnake(provider))
	}
}

func toUpperSnake(s string) string {
	result := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			result = append(result, c-32)
		} else if c >= 'A' && c <= 'Z' {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, c)
		} else if c == '-' || c == ' ' {
			result = append(result, '_')
		} else {
			result = append(result, c)
		}
	}
	return string(result)
}
