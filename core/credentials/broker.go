package credentials

import (
	"context"
	"errors"
	"sync"

	"github.com/adalundhe/sylk/core/security"
)

var (
	ErrAccessDenied       = errors.New("credential access denied")
	ErrAuthRequired       = errors.New("user authorization required")
	ErrProviderNotFound   = errors.New("credential provider not found")
	ErrCredentialNotFound = errors.New("credential not found")
)

type CredentialAccessDeniedError struct {
	AgentType string
	Provider  string
	Reason    string
}

func (e *CredentialAccessDeniedError) Error() string {
	return "credential access denied: " + e.Reason
}

type CredentialProvider interface {
	GetAPIKey(provider string) (string, error)
}

type AuthorizationCallback func(agentType, provider, reason string) (bool, error)

type CredentialBroker struct {
	mu            sync.RWMutex
	credProvider  CredentialProvider
	scopeManager  *ScopeManager
	handleStore   *HandleStore
	auditLogger   *security.AuditLogger
	authCallback  AuthorizationCallback
	authorizedSet map[string]bool
}

type BrokerConfig struct {
	CredentialProvider CredentialProvider
	ScopeManager       *ScopeManager
	AuditLogger        *security.AuditLogger
	AuthCallback       AuthorizationCallback
}

func NewCredentialBroker(cfg BrokerConfig) *CredentialBroker {
	scopeMgr := cfg.ScopeManager
	if scopeMgr == nil {
		scopeMgr = NewScopeManager()
	}

	return &CredentialBroker{
		credProvider:  cfg.CredentialProvider,
		scopeManager:  scopeMgr,
		handleStore:   NewHandleStore(),
		auditLogger:   cfg.AuditLogger,
		authCallback:  cfg.AuthCallback,
		authorizedSet: make(map[string]bool),
	}
}

func (b *CredentialBroker) RequestCredential(
	ctx context.Context,
	agentID, agentType, provider, toolCallID, reason string,
) (*CredentialHandle, error) {
	if !b.scopeManager.IsAllowed(agentType, provider) {
		b.logDenied(agentID, agentType, provider, "scope_denied")
		return nil, &CredentialAccessDeniedError{
			AgentType: agentType,
			Provider:  provider,
			Reason:    "agent type not allowed for this provider",
		}
	}

	if err := b.checkAuthorization(agentType, provider, reason); err != nil {
		b.logDenied(agentID, agentType, provider, "auth_required")
		return nil, err
	}

	value, err := b.fetchCredential(provider)
	if err != nil {
		b.logDenied(agentID, agentType, provider, "fetch_failed")
		return nil, err
	}

	handle := NewCredentialHandle(provider, agentID, agentType, toolCallID, value)
	b.handleStore.Store(handle)

	b.logGranted(handle, reason)

	return handle, nil
}

func (b *CredentialBroker) checkAuthorization(agentType, provider, reason string) error {
	if !b.scopeManager.RequiresAuth(agentType, provider) {
		return nil
	}

	authKey := agentType + ":" + provider
	b.mu.RLock()
	authorized := b.authorizedSet[authKey]
	b.mu.RUnlock()

	if authorized {
		return nil
	}

	if b.authCallback == nil {
		return ErrAuthRequired
	}

	approved, err := b.authCallback(agentType, provider, reason)
	if err != nil {
		return err
	}
	if !approved {
		return ErrAuthRequired
	}

	b.mu.Lock()
	b.authorizedSet[authKey] = true
	b.mu.Unlock()

	return nil
}

func (b *CredentialBroker) fetchCredential(provider string) (string, error) {
	if b.credProvider == nil {
		return "", ErrProviderNotFound
	}

	value, err := b.credProvider.GetAPIKey(provider)
	if err != nil {
		return "", ErrCredentialNotFound
	}
	if value == "" {
		return "", ErrCredentialNotFound
	}

	return value, nil
}

func (b *CredentialBroker) ResolveHandle(handleID string) (string, error) {
	value, err := b.handleStore.Resolve(handleID)
	if err != nil {
		b.logResolveFailed(handleID, err)
		return "", err
	}

	b.logResolved(handleID)
	return value, nil
}

func (b *CredentialBroker) RevokeHandle(handleID string) bool {
	revoked := b.handleStore.Revoke(handleID)
	if revoked {
		b.logRevoked(handleID)
	}
	return revoked
}

func (b *CredentialBroker) GetHandle(handleID string) (*CredentialHandle, error) {
	return b.handleStore.Get(handleID)
}

func (b *CredentialBroker) ActiveHandleCount() int {
	return b.handleStore.Count()
}

func (b *CredentialBroker) Stop() {
	b.handleStore.Stop()
}

func (b *CredentialBroker) ClearAuthorizations() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.authorizedSet = make(map[string]bool)
}

func (b *CredentialBroker) logGranted(handle *CredentialHandle, reason string) {
	if b.auditLogger == nil {
		return
	}

	entry := security.NewAuditEntry(security.AuditCategoryPermission, "credential_granted", "grant")
	entry.AgentID = handle.AgentID
	entry.Target = handle.Provider
	entry.Outcome = "granted"
	entry.Details = map[string]interface{}{
		"handle_id":  handle.ID,
		"agent_type": handle.AgentType,
		"tool_call":  handle.ToolCallID,
		"reason":     reason,
		"expires_at": handle.ExpiresAt,
	}
	_ = b.auditLogger.Log(entry)
}

func (b *CredentialBroker) logDenied(agentID, agentType, provider, reason string) {
	if b.auditLogger == nil {
		return
	}

	entry := security.NewAuditEntry(security.AuditCategoryPermission, "credential_denied", "deny")
	entry.Severity = security.AuditSeveritySecurity
	entry.AgentID = agentID
	entry.Target = provider
	entry.Outcome = "denied"
	entry.Details = map[string]interface{}{
		"agent_type": agentType,
		"reason":     reason,
	}
	_ = b.auditLogger.Log(entry)
}

func (b *CredentialBroker) logResolved(handleID string) {
	if b.auditLogger == nil {
		return
	}

	entry := security.NewAuditEntry(security.AuditCategoryPermission, "credential_resolved", "resolve")
	entry.Target = handleID
	entry.Outcome = "resolved"
	_ = b.auditLogger.Log(entry)
}

func (b *CredentialBroker) logResolveFailed(handleID string, err error) {
	if b.auditLogger == nil {
		return
	}

	entry := security.NewAuditEntry(security.AuditCategoryPermission, "credential_resolve_failed", "resolve")
	entry.Severity = security.AuditSeverityWarning
	entry.Target = handleID
	entry.Outcome = "failed"
	entry.Details = map[string]interface{}{
		"error": err.Error(),
	}
	_ = b.auditLogger.Log(entry)
}

func (b *CredentialBroker) logRevoked(handleID string) {
	if b.auditLogger == nil {
		return
	}

	entry := security.NewAuditEntry(security.AuditCategoryPermission, "credential_revoked", "revoke")
	entry.Target = handleID
	entry.Outcome = "revoked"
	_ = b.auditLogger.Log(entry)
}
