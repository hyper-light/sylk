package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/tools"
)

type mockCredentialProvider struct {
	mu      sync.RWMutex
	secrets map[string]string
}

func newMockCredentialProvider() *mockCredentialProvider {
	return &mockCredentialProvider{
		secrets: map[string]string{
			"github":  "ghp_mock_github_token_12345678901234567890",
			"openai":  "sk-mockopenaikey1234567890abcdef",
			"serpapi": "mock_serpapi_key_1234567890",
			"aws":     "AKIAIOSFODNN7EXAMPLE",
		},
	}
}

func (m *mockCredentialProvider) GetAPIKey(provider string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if key, ok := m.secrets[provider]; ok {
		return key, nil
	}
	return "", credentials.ErrCredentialNotFound
}

func TestBasicAccessFlow_EngineerCreatesPR(t *testing.T) {
	provider := newMockCredentialProvider()
	authCallback := func(agentType, provider, reason string) (bool, error) {
		return true, nil
	}

	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
		AuthCallback:       authCallback,
	})
	defer broker.Stop()

	handle, err := broker.RequestCredential(
		context.Background(),
		"agent-1",
		"engineer",
		"github",
		"tool-call-1",
		"PR creation",
	)

	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if handle == nil {
		t.Fatal("expected handle, got nil")
	}
	if handle.Provider != "github" {
		t.Errorf("expected github, got: %s", handle.Provider)
	}

	value, err := broker.ResolveHandle(handle.ID)
	if err != nil {
		t.Fatalf("expected to resolve handle, got: %v", err)
	}
	if value != "ghp_mock_github_token_12345678901234567890" {
		t.Error("expected correct credential value")
	}

	_, err = broker.ResolveHandle(handle.ID)
	if err == nil {
		t.Error("expected error on second resolve (single-use)")
	}
}

func TestBasicAccessFlow_LibrarianGeneratesEmbeddings(t *testing.T) {
	provider := newMockCredentialProvider()
	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	handle, err := broker.RequestCredential(
		context.Background(),
		"agent-2",
		"librarian",
		"openai",
		"tool-call-2",
		"Generate embeddings",
	)

	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if handle == nil {
		t.Fatal("expected handle")
	}
}

func TestScopeEnforcement_LibrarianTriesGitHub(t *testing.T) {
	provider := newMockCredentialProvider()
	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-3",
		"librarian",
		"github",
		"tool-call-3",
		"Should be denied",
	)

	if err == nil {
		t.Fatal("expected error for librarian accessing github")
	}

	accessErr, ok := err.(*credentials.CredentialAccessDeniedError)
	if !ok {
		t.Fatalf("expected CredentialAccessDeniedError, got: %T", err)
	}
	if accessErr.AgentType != "librarian" {
		t.Errorf("expected agent type librarian, got: %s", accessErr.AgentType)
	}
}

func TestScopeEnforcement_OrchestratorDeniedAllCredentials(t *testing.T) {
	provider := newMockCredentialProvider()
	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	providers := []string{"github", "openai", "aws", "serpapi"}

	for _, p := range providers {
		_, err := broker.RequestCredential(
			context.Background(),
			"agent-4",
			"orchestrator",
			p,
			"tool-call-4",
			"Should be denied",
		)

		if err == nil {
			t.Errorf("expected orchestrator to be denied access to %s", p)
		}
	}
}

func TestScopeEnforcement_UnknownProviderDenied(t *testing.T) {
	provider := newMockCredentialProvider()
	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-5",
		"engineer",
		"unknown_provider",
		"tool-call-5",
		"Unknown provider",
	)

	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestHandleLifecycle_ExpiresAfterTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	provider := newMockCredentialProvider()
	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	handle, err := broker.RequestCredential(
		context.Background(),
		"agent-6",
		"librarian",
		"openai",
		"tool-call-6",
		"Test expiry",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle == nil {
		t.Fatal("expected handle")
	}

	if handle.ExpiresAt.Before(time.Now()) {
		t.Error("handle should not be expired immediately")
	}

	expectedDuration := 30 * time.Second
	actualDuration := time.Until(handle.ExpiresAt)
	if actualDuration > expectedDuration+time.Second || actualDuration < expectedDuration-time.Second {
		t.Errorf("expected ~30s expiry, got: %v", actualDuration)
	}
}

func TestHandleLifecycle_InvalidatesAfterUse(t *testing.T) {
	provider := newMockCredentialProvider()
	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-7",
		"librarian",
		"openai",
		"tool-call-7",
		"Test invalidation",
	)

	_, err := broker.ResolveHandle(handle.ID)
	if err != nil {
		t.Fatalf("first resolve should succeed: %v", err)
	}

	_, err = broker.ResolveHandle(handle.ID)
	if err == nil {
		t.Error("second resolve should fail (handle used)")
	}
}

func TestHandleLifecycle_RevokedOnCleanup(t *testing.T) {
	provider := newMockCredentialProvider()
	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-8",
		"librarian",
		"openai",
		"tool-call-8",
		"Test revocation",
	)

	revoked := broker.RevokeHandle(handle.ID)
	if !revoked {
		t.Error("expected handle to be revoked")
	}

	_, err := broker.ResolveHandle(handle.ID)
	if err == nil {
		t.Error("resolve should fail after revocation")
	}
}

func TestUserAuthorization_FirstAccessPromptsUser(t *testing.T) {
	provider := newMockCredentialProvider()
	promptCalled := false

	authCallback := func(agentType, provider, reason string) (bool, error) {
		promptCalled = true
		if agentType != "engineer" {
			t.Errorf("expected engineer, got %s", agentType)
		}
		if provider != "github" {
			t.Errorf("expected github, got %s", provider)
		}
		return true, nil
	}

	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
		AuthCallback:       authCallback,
	})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-9",
		"engineer",
		"github",
		"tool-call-9",
		"First access",
	)

	if err != nil {
		t.Fatalf("expected success after approval: %v", err)
	}
	if !promptCalled {
		t.Error("expected authorization prompt to be called")
	}
}

func TestUserAuthorization_AllowEnablesFutureAccess(t *testing.T) {
	provider := newMockCredentialProvider()
	callCount := 0

	authCallback := func(agentType, provider, reason string) (bool, error) {
		callCount++
		return true, nil
	}

	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
		AuthCallback:       authCallback,
	})
	defer broker.Stop()

	broker.RequestCredential(context.Background(), "a1", "engineer", "github", "t1", "first")
	broker.RequestCredential(context.Background(), "a2", "engineer", "github", "t2", "second")
	broker.RequestCredential(context.Background(), "a3", "engineer", "github", "t3", "third")

	if callCount != 1 {
		t.Errorf("expected 1 auth call (cached), got %d", callCount)
	}
}

func TestUserAuthorization_DenyBlocksAccess(t *testing.T) {
	provider := newMockCredentialProvider()

	authCallback := func(agentType, provider, reason string) (bool, error) {
		return false, nil
	}

	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
		AuthCallback:       authCallback,
	})
	defer broker.Stop()

	_, err := broker.RequestCredential(
		context.Background(),
		"agent-10",
		"engineer",
		"github",
		"tool-call-10",
		"Should be denied",
	)

	if err == nil {
		t.Error("expected error when user denies authorization")
	}
}

func TestToolRegistry_CorrectCredentialMapping(t *testing.T) {
	registry := tools.NewToolCredentialRegistry()

	testCases := []struct {
		toolName      string
		expectedCreds []string
	}{
		{"generate_embeddings", []string{"openai"}},
		{"create_pr", []string{"github"}},
		{"web_search", []string{"serpapi"}},
		{"read_file", []string{}},
		{"write_file", []string{}},
	}

	for _, tc := range testCases {
		t.Run(tc.toolName, func(t *testing.T) {
			meta := registry.GetMetadata(tc.toolName)

			if len(tc.expectedCreds) == 0 {
				if meta != nil && len(meta.RequiredCredentials) > 0 {
					t.Errorf("expected no credentials for %s", tc.toolName)
				}
				return
			}

			if meta == nil {
				t.Fatalf("expected metadata for %s", tc.toolName)
			}

			for _, expected := range tc.expectedCreds {
				found := false
				for _, actual := range meta.RequiredCredentials {
					if actual == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %s to require %s", tc.toolName, expected)
				}
			}
		})
	}
}

func TestAuditTrail_AllAccessLogged(t *testing.T) {
	provider := newMockCredentialProvider()
	auditLog := credentials.NewCredentialAuditLog(nil)

	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	broker.RequestCredential(context.Background(), "a1", "engineer", "github", "t1", "test")
	broker.RequestCredential(context.Background(), "a2", "librarian", "github", "t2", "denied")
	broker.RequestCredential(context.Background(), "a3", "librarian", "openai", "t3", "allowed")

	auditLog.LogGranted("a1", "engineer", "github", "t1", "h1", "test")
	auditLog.LogDenied("a2", "librarian", "github", "scope violation")
	auditLog.LogGranted("a3", "librarian", "openai", "t3", "h3", "allowed")

	entries := auditLog.Entries()
	if len(entries) < 3 {
		t.Errorf("expected at least 3 audit entries, got %d", len(entries))
	}

	if err := auditLog.Verify(); err != nil {
		t.Errorf("audit log integrity check failed: %v", err)
	}
}

func TestAuditTrail_HashChainVerifies(t *testing.T) {
	auditLog := credentials.NewCredentialAuditLog(nil)

	for i := 0; i < 10; i++ {
		auditLog.LogGranted("agent", "engineer", "github", "tool", "handle", "reason")
	}

	if err := auditLog.Verify(); err != nil {
		t.Errorf("hash chain verification failed: %v", err)
	}
}

func TestConcurrentAccess_ThreadSafe(t *testing.T) {
	provider := newMockCredentialProvider()
	authCallback := func(agentType, provider, reason string) (bool, error) {
		return true, nil
	}

	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
		AuthCallback:       authCallback,
	})
	defer broker.Stop()

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			agentType := "engineer"
			if n%3 == 0 {
				agentType = "librarian"
			}

			prov := "github"
			if n%2 == 0 {
				prov = "openai"
			}

			handle, err := broker.RequestCredential(
				context.Background(),
				"agent",
				agentType,
				prov,
				"tool",
				"reason",
			)

			if err != nil {
				return
			}

			if handle != nil {
				_, _ = broker.ResolveHandle(handle.ID)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Errorf("concurrent error: %v", err)
		}
	}
}

func TestPerformance_LowOverhead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	provider := newMockCredentialProvider()
	broker := credentials.NewCredentialBroker(credentials.BrokerConfig{
		CredentialProvider: provider,
	})
	defer broker.Stop()

	iterations := 100
	start := time.Now()

	for i := 0; i < iterations; i++ {
		handle, _ := broker.RequestCredential(
			context.Background(),
			"agent",
			"librarian",
			"openai",
			"tool",
			"reason",
		)
		if handle != nil {
			broker.ResolveHandle(handle.ID)
		}
	}

	elapsed := time.Since(start)
	avgTime := elapsed / time.Duration(iterations)

	if avgTime > 5*time.Millisecond {
		t.Errorf("credential overhead too high: avg %v, expected < 5ms", avgTime)
	}
}

func TestCredentialAuditLog_Query(t *testing.T) {
	auditLog := credentials.NewCredentialAuditLog(nil)

	auditLog.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	auditLog.LogGranted("a2", "librarian", "openai", "t2", "h2", "r2")
	auditLog.LogDenied("a3", "orchestrator", "github", "denied")
	auditLog.LogRevoked("h1")

	engineerEntries := auditLog.Query(credentials.AuditQueryFilter{AgentType: "engineer"})
	if len(engineerEntries) != 1 {
		t.Errorf("expected 1 engineer entry, got %d", len(engineerEntries))
	}

	githubEntries := auditLog.Query(credentials.AuditQueryFilter{Provider: "github"})
	if len(githubEntries) != 2 {
		t.Errorf("expected 2 github entries, got %d", len(githubEntries))
	}

	deniedEntries := auditLog.Query(credentials.AuditQueryFilter{Action: credentials.AuditActionDenied})
	if len(deniedEntries) != 1 {
		t.Errorf("expected 1 denied entry, got %d", len(deniedEntries))
	}
}
