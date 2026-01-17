package credentials

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/security"
)

type mockCredentialProvider struct {
	credentials map[string]string
}

func newMockCredentialProvider() *mockCredentialProvider {
	return &mockCredentialProvider{
		credentials: map[string]string{
			"openai":    "sk-openai-key",
			"anthropic": "sk-anthropic-key",
			"github":    "ghp_github-token",
		},
	}
}

func (m *mockCredentialProvider) GetAPIKey(provider string) (string, error) {
	if key, ok := m.credentials[provider]; ok {
		return key, nil
	}
	return "", errors.New("not found")
}

func TestBroker_RequestAllowed(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	handle, err := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "need embedding",
	)

	if err != nil {
		t.Fatalf("request should succeed: %v", err)
	}
	if handle == nil {
		t.Fatal("handle should not be nil")
	}
	if handle.Provider != "openai" {
		t.Error("wrong provider")
	}
}

func TestBroker_RequestDenied(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "github", "call-123", "need git access",
	)

	if err == nil {
		t.Fatal("request should fail for denied provider")
	}

	var accessErr *CredentialAccessDeniedError
	if !errors.As(err, &accessErr) {
		t.Error("error should be CredentialAccessDeniedError")
	}
}

func TestBroker_RequestUnknownAgent(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-1", "unknown_agent", "openai", "call-123", "reason",
	)

	if err == nil {
		t.Error("unknown agent should be denied by default")
	}
}

func TestBroker_RequestRequiresAuth(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-1", "engineer", "github", "call-123", "need git access",
	)

	if err != ErrAuthRequired {
		t.Error("engineer+github should require auth")
	}
}

func TestBroker_RequestWithAuthCallback(t *testing.T) {
	t.Parallel()

	approved := false
	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
		AuthCallback: func(agentType, provider, reason string) (bool, error) {
			approved = true
			return true, nil
		},
	})
	defer broker.Stop()

	handle, err := broker.RequestCredential(
		context.Background(),
		"agent-1", "engineer", "github", "call-123", "need git access",
	)

	if err != nil {
		t.Fatalf("should succeed with approval: %v", err)
	}
	if !approved {
		t.Error("callback should have been called")
	}
	if handle == nil {
		t.Error("handle should be returned")
	}
}

func TestBroker_AuthCallbackDenied(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
		AuthCallback: func(agentType, provider, reason string) (bool, error) {
			return false, nil
		},
	})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-1", "engineer", "github", "call-123", "need git access",
	)

	if err != ErrAuthRequired {
		t.Error("should fail when callback denies")
	}
}

func TestBroker_AuthorizationCached(t *testing.T) {
	t.Parallel()

	callCount := 0
	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
		AuthCallback: func(agentType, provider, reason string) (bool, error) {
			callCount++
			return true, nil
		},
	})
	defer broker.Stop()

	broker.RequestCredential(context.Background(), "a1", "engineer", "github", "c1", "r1")
	broker.RequestCredential(context.Background(), "a2", "engineer", "github", "c2", "r2")

	if callCount != 1 {
		t.Errorf("callback should only be called once, got %d", callCount)
	}
}

func TestBroker_ResolveHandle(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "reason",
	)

	value, err := broker.ResolveHandle(handle.ID)
	if err != nil {
		t.Fatalf("resolve should succeed: %v", err)
	}
	if value != "sk-openai-key" {
		t.Error("wrong credential value")
	}
}

func TestBroker_ResolveHandleTwice(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "reason",
	)

	broker.ResolveHandle(handle.ID)

	_, err := broker.ResolveHandle(handle.ID)
	if err != ErrHandleUsed {
		t.Error("second resolve should fail")
	}
}

func TestBroker_RevokeHandle(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "reason",
	)

	if !broker.RevokeHandle(handle.ID) {
		t.Error("revoke should succeed")
	}

	_, err := broker.ResolveHandle(handle.ID)
	if err != ErrHandleNotFound {
		t.Error("revoked handle should not be found")
	}
}

func TestBroker_ActiveHandleCount(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	if broker.ActiveHandleCount() != 0 {
		t.Error("initial count should be 0")
	}

	broker.RequestCredential(context.Background(), "a1", "librarian", "openai", "c1", "r1")
	broker.RequestCredential(context.Background(), "a2", "librarian", "anthropic", "c2", "r2")

	if broker.ActiveHandleCount() != 2 {
		t.Error("count should be 2")
	}
}

func TestBroker_ClearAuthorizations(t *testing.T) {
	t.Parallel()

	callCount := 0
	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
		AuthCallback: func(agentType, provider, reason string) (bool, error) {
			callCount++
			return true, nil
		},
	})
	defer broker.Stop()

	broker.RequestCredential(context.Background(), "a1", "engineer", "github", "c1", "r1")
	broker.ClearAuthorizations()
	broker.RequestCredential(context.Background(), "a2", "engineer", "github", "c2", "r2")

	if callCount != 2 {
		t.Errorf("callback should be called twice after clear, got %d", callCount)
	}
}

func TestBroker_ConcurrentRequests(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			broker.RequestCredential(
				context.Background(),
				"agent", "librarian", "openai", "call", "reason",
			)
		}(i)
	}

	wg.Wait()

	if broker.ActiveHandleCount() != 100 {
		t.Errorf("expected 100 handles, got %d", broker.ActiveHandleCount())
	}
}

func TestBroker_NoCredentialProvider(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "reason",
	)

	if err != ErrProviderNotFound {
		t.Error("should fail with ErrProviderNotFound")
	}
}

func TestBroker_AuditLogging(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	cfg := security.AuditLogConfig{LogPath: logPath}
	logger, _ := security.NewAuditLogger(cfg)
	defer logger.Close()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
		AuditLogger:        logger,
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "reason",
	)

	broker.ResolveHandle(handle.ID)

	broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "github", "call-456", "reason",
	)

	if logger.Sequence() < 3 {
		t.Error("should have multiple audit log entries")
	}
}
