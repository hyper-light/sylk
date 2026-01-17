package credentials

import (
	"context"
	"errors"
	"sync"
)

type AuthorizationResult string

const (
	AuthorizationAllow        AuthorizationResult = "allow"
	AuthorizationAllowSession AuthorizationResult = "allow_session"
	AuthorizationDeny         AuthorizationResult = "deny"
)

var (
	ErrAuthorizationDenied  = errors.New("user denied authorization")
	ErrAuthorizationTimeout = errors.New("authorization request timed out")
)

type AuthorizationPrompt struct {
	AgentType   string
	Provider    string
	Reason      string
	ToolName    string
	Description string
}

type AuthorizationPromptFunc func(ctx context.Context, prompt AuthorizationPrompt) (AuthorizationResult, error)

type AuthorizationStorage interface {
	Save(agentType, provider string) error
	Load() (map[string]bool, error)
	Remove(agentType, provider string) error
}

type UserAuthorizationManager struct {
	mu              sync.RWMutex
	pendingMu       sync.Mutex
	authorizations  map[string]bool
	sessionOnly     map[string]bool
	storage         AuthorizationStorage
	promptFunc      AuthorizationPromptFunc
	pendingRequests map[string]chan struct{}
}

func NewUserAuthorizationManager(
	storage AuthorizationStorage,
	promptFunc AuthorizationPromptFunc,
) *UserAuthorizationManager {
	mgr := &UserAuthorizationManager{
		authorizations:  make(map[string]bool),
		sessionOnly:     make(map[string]bool),
		storage:         storage,
		promptFunc:      promptFunc,
		pendingRequests: make(map[string]chan struct{}),
	}

	if storage != nil {
		mgr.loadFromStorage()
	}

	return mgr
}

func (m *UserAuthorizationManager) loadFromStorage() {
	auths, err := m.storage.Load()
	if err != nil || auths == nil {
		return
	}
	m.authorizations = auths
}

func (m *UserAuthorizationManager) authKey(agentType, provider string) string {
	return agentType + ":" + provider
}

func (m *UserAuthorizationManager) IsAuthorized(agentType, provider string) bool {
	key := m.authKey(agentType, provider)

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.authorizations[key] || m.sessionOnly[key]
}

func (m *UserAuthorizationManager) RequestAuthorization(
	ctx context.Context,
	agentType, provider, reason, toolName string,
) error {
	key := m.authKey(agentType, provider)

	if m.IsAuthorized(agentType, provider) {
		return nil
	}

	if err := m.waitForPending(ctx, key); err != nil {
		return err
	}

	if m.IsAuthorized(agentType, provider) {
		return nil
	}

	return m.doPrompt(ctx, key, agentType, provider, reason, toolName)
}

func (m *UserAuthorizationManager) waitForPending(ctx context.Context, key string) error {
	m.pendingMu.Lock()
	ch, exists := m.pendingRequests[key]
	m.pendingMu.Unlock()

	if !exists {
		return nil
	}

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *UserAuthorizationManager) doPrompt(
	ctx context.Context,
	key, agentType, provider, reason, toolName string,
) error {
	m.pendingMu.Lock()
	if _, exists := m.pendingRequests[key]; exists {
		m.pendingMu.Unlock()
		return m.waitForPending(ctx, key)
	}
	ch := make(chan struct{})
	m.pendingRequests[key] = ch
	m.pendingMu.Unlock()

	defer func() {
		m.pendingMu.Lock()
		delete(m.pendingRequests, key)
		close(ch)
		m.pendingMu.Unlock()
	}()

	return m.executePrompt(ctx, key, agentType, provider, reason, toolName)
}

func (m *UserAuthorizationManager) executePrompt(
	ctx context.Context,
	key, agentType, provider, reason, toolName string,
) error {
	if m.promptFunc == nil {
		return ErrAuthorizationDenied
	}

	prompt := AuthorizationPrompt{
		AgentType:   agentType,
		Provider:    provider,
		Reason:      reason,
		ToolName:    toolName,
		Description: buildDescription(agentType, provider, toolName),
	}

	result, err := m.promptFunc(ctx, prompt)
	if err != nil {
		return err
	}

	return m.handleResult(key, agentType, provider, result)
}

func buildDescription(agentType, provider, toolName string) string {
	desc := "The " + agentType + " agent wants to access " + provider + " credentials"
	if toolName != "" {
		desc += " for " + toolName
	}
	return desc
}

func (m *UserAuthorizationManager) handleResult(
	key, agentType, provider string,
	result AuthorizationResult,
) error {
	switch result {
	case AuthorizationAllow:
		m.grantPermanent(key, agentType, provider)
		return nil
	case AuthorizationAllowSession:
		m.grantSession(key)
		return nil
	default:
		return ErrAuthorizationDenied
	}
}

func (m *UserAuthorizationManager) grantPermanent(key, agentType, provider string) {
	m.mu.Lock()
	m.authorizations[key] = true
	m.mu.Unlock()

	if m.storage != nil {
		_ = m.storage.Save(agentType, provider)
	}
}

func (m *UserAuthorizationManager) grantSession(key string) {
	m.mu.Lock()
	m.sessionOnly[key] = true
	m.mu.Unlock()
}

func (m *UserAuthorizationManager) RevokeAuthorization(agentType, provider string) error {
	key := m.authKey(agentType, provider)

	m.mu.Lock()
	delete(m.authorizations, key)
	delete(m.sessionOnly, key)
	m.mu.Unlock()

	if m.storage != nil {
		return m.storage.Remove(agentType, provider)
	}

	return nil
}

func (m *UserAuthorizationManager) ClearSessionAuthorizations() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionOnly = make(map[string]bool)
}

func (m *UserAuthorizationManager) ListAuthorizations() map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]bool)
	for k, v := range m.authorizations {
		result[k] = v
	}
	return result
}

func (m *UserAuthorizationManager) ListSessionAuthorizations() map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]bool)
	for k, v := range m.sessionOnly {
		result[k] = v
	}
	return result
}

func (m *UserAuthorizationManager) AuthCallback() AuthorizationCallback {
	return func(agentType, provider, reason string) (bool, error) {
		err := m.RequestAuthorization(context.Background(), agentType, provider, reason, "")
		if err != nil {
			return false, err
		}
		return true, nil
	}
}
