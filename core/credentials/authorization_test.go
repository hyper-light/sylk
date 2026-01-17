package credentials

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUserAuthorizationManager_IsAuthorized_Empty(t *testing.T) {
	mgr := NewUserAuthorizationManager(nil, nil)

	if mgr.IsAuthorized("engineer", "github") {
		t.Error("expected not authorized for new manager")
	}
}

func TestUserAuthorizationManager_RequestAuthorization_Allow(t *testing.T) {
	promptCalled := false
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		promptCalled = true
		if prompt.AgentType != "engineer" {
			t.Errorf("expected engineer, got %s", prompt.AgentType)
		}
		if prompt.Provider != "github" {
			t.Errorf("expected github, got %s", prompt.Provider)
		}
		return AuthorizationAllow, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)

	err := mgr.RequestAuthorization(context.Background(), "engineer", "github", "PR creation", "create_pr")

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if !promptCalled {
		t.Error("expected prompt to be called")
	}
	if !mgr.IsAuthorized("engineer", "github") {
		t.Error("expected authorized after allow")
	}
}

func TestUserAuthorizationManager_RequestAuthorization_AllowSession(t *testing.T) {
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		return AuthorizationAllowSession, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)

	err := mgr.RequestAuthorization(context.Background(), "engineer", "github", "PR creation", "create_pr")

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if !mgr.IsAuthorized("engineer", "github") {
		t.Error("expected authorized after allow session")
	}

	auths := mgr.ListAuthorizations()
	if auths["engineer:github"] {
		t.Error("session-only should not appear in permanent authorizations")
	}

	sessionAuths := mgr.ListSessionAuthorizations()
	if !sessionAuths["engineer:github"] {
		t.Error("expected in session authorizations")
	}
}

func TestUserAuthorizationManager_RequestAuthorization_Deny(t *testing.T) {
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		return AuthorizationDeny, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)

	err := mgr.RequestAuthorization(context.Background(), "engineer", "github", "PR creation", "create_pr")

	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Errorf("expected denied error, got: %v", err)
	}
	if mgr.IsAuthorized("engineer", "github") {
		t.Error("expected not authorized after deny")
	}
}

func TestUserAuthorizationManager_RequestAuthorization_AlreadyAuthorized(t *testing.T) {
	promptCalled := false
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		promptCalled = true
		return AuthorizationAllow, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)

	_ = mgr.RequestAuthorization(context.Background(), "engineer", "github", "first", "tool1")
	promptCalled = false

	err := mgr.RequestAuthorization(context.Background(), "engineer", "github", "second", "tool2")

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if promptCalled {
		t.Error("prompt should not be called for already authorized")
	}
}

func TestUserAuthorizationManager_RequestAuthorization_NoPromptFunc(t *testing.T) {
	mgr := NewUserAuthorizationManager(nil, nil)

	err := mgr.RequestAuthorization(context.Background(), "engineer", "github", "reason", "tool")

	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Errorf("expected denied error when no prompt func, got: %v", err)
	}
}

func TestUserAuthorizationManager_RevokeAuthorization(t *testing.T) {
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		return AuthorizationAllow, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)
	_ = mgr.RequestAuthorization(context.Background(), "engineer", "github", "reason", "tool")

	if !mgr.IsAuthorized("engineer", "github") {
		t.Error("expected authorized before revoke")
	}

	err := mgr.RevokeAuthorization("engineer", "github")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	if mgr.IsAuthorized("engineer", "github") {
		t.Error("expected not authorized after revoke")
	}
}

func TestUserAuthorizationManager_ClearSessionAuthorizations(t *testing.T) {
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		if prompt.Provider == "github" {
			return AuthorizationAllow, nil
		}
		return AuthorizationAllowSession, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)
	_ = mgr.RequestAuthorization(context.Background(), "engineer", "github", "r1", "t1")
	_ = mgr.RequestAuthorization(context.Background(), "engineer", "openai", "r2", "t2")

	mgr.ClearSessionAuthorizations()

	if !mgr.IsAuthorized("engineer", "github") {
		t.Error("permanent authorization should remain")
	}
	if mgr.IsAuthorized("engineer", "openai") {
		t.Error("session authorization should be cleared")
	}
}

func TestUserAuthorizationManager_Concurrent(t *testing.T) {
	var promptCount int32
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		atomic.AddInt32(&promptCount, 1)
		time.Sleep(10 * time.Millisecond)
		return AuthorizationAllow, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.RequestAuthorization(context.Background(), "engineer", "github", "reason", "tool")
		}()
	}

	wg.Wait()

	count := atomic.LoadInt32(&promptCount)
	if count != 1 {
		t.Errorf("expected 1 prompt call (serialized), got %d", count)
	}

	if !mgr.IsAuthorized("engineer", "github") {
		t.Error("expected authorized after concurrent requests")
	}
}

func TestUserAuthorizationManager_ContextCancellation(t *testing.T) {
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
			return AuthorizationAllow, nil
		}
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := mgr.RequestAuthorization(ctx, "engineer", "github", "reason", "tool")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected deadline exceeded, got: %v", err)
	}
}

func TestUserAuthorizationManager_AuthCallback(t *testing.T) {
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		return AuthorizationAllow, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)
	callback := mgr.AuthCallback()

	approved, err := callback("engineer", "github", "PR creation")

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if !approved {
		t.Error("expected approved")
	}
	if !mgr.IsAuthorized("engineer", "github") {
		t.Error("expected authorized via callback")
	}
}

func TestUserAuthorizationManager_AuthCallback_Deny(t *testing.T) {
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		return AuthorizationDeny, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)
	callback := mgr.AuthCallback()

	approved, err := callback("engineer", "github", "PR creation")

	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Errorf("expected denied error, got: %v", err)
	}
	if approved {
		t.Error("expected not approved")
	}
}

type mockAuthStorage struct {
	mu    sync.Mutex
	auths map[string]bool
}

func newMockAuthStorage() *mockAuthStorage {
	return &mockAuthStorage{auths: make(map[string]bool)}
}

func (m *mockAuthStorage) Save(agentType, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auths[agentType+":"+provider] = true
	return nil
}

func (m *mockAuthStorage) Load() (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]bool)
	for k, v := range m.auths {
		result[k] = v
	}
	return result, nil
}

func (m *mockAuthStorage) Remove(agentType, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.auths, agentType+":"+provider)
	return nil
}

func TestUserAuthorizationManager_WithStorage(t *testing.T) {
	storage := newMockAuthStorage()

	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		return AuthorizationAllow, nil
	}

	mgr1 := NewUserAuthorizationManager(storage, promptFunc)
	_ = mgr1.RequestAuthorization(context.Background(), "engineer", "github", "reason", "tool")

	if !storage.auths["engineer:github"] {
		t.Error("expected storage to have authorization")
	}

	mgr2 := NewUserAuthorizationManager(storage, promptFunc)
	if !mgr2.IsAuthorized("engineer", "github") {
		t.Error("expected loaded authorization from storage")
	}

	_ = mgr2.RevokeAuthorization("engineer", "github")
	if storage.auths["engineer:github"] {
		t.Error("expected storage to remove authorization")
	}
}

func TestUserAuthorizationManager_SessionNotPersisted(t *testing.T) {
	storage := newMockAuthStorage()

	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		return AuthorizationAllowSession, nil
	}

	mgr := NewUserAuthorizationManager(storage, promptFunc)
	_ = mgr.RequestAuthorization(context.Background(), "engineer", "github", "reason", "tool")

	if storage.auths["engineer:github"] {
		t.Error("session-only should not be persisted to storage")
	}
}

func TestUserAuthorizationManager_PromptDescription(t *testing.T) {
	var capturedPrompt AuthorizationPrompt
	promptFunc := func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error) {
		capturedPrompt = prompt
		return AuthorizationAllow, nil
	}

	mgr := NewUserAuthorizationManager(nil, promptFunc)
	_ = mgr.RequestAuthorization(context.Background(), "engineer", "github", "PR creation", "create_pr")

	if capturedPrompt.Description == "" {
		t.Error("expected description to be set")
	}

	expected := "The engineer agent wants to access github credentials for create_pr"
	if capturedPrompt.Description != expected {
		t.Errorf("expected '%s', got '%s'", expected, capturedPrompt.Description)
	}
}
