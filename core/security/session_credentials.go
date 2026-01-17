package security

import (
	"errors"
	"slices"
	"sync"
	"time"
)

var (
	ErrProfileNotFound       = errors.New("profile not found")
	ErrCredentialExpired     = errors.New("temporary credential expired")
	ErrTempCredentialMaxTTL  = errors.New("TTL exceeds maximum allowed")
	ErrProviderNotConfigured = errors.New("credential provider not configured")
)

const DefaultMaxTempTTL = 24 * time.Hour

type CredentialSource string

const (
	SourceTemp    CredentialSource = "temporary"
	SourceProfile CredentialSource = "profile"
	SourceBase    CredentialSource = "base"
)

type TempCredential struct {
	Provider  string
	Value     string
	ExpiresAt time.Time
	Source    string
	CreatedAt time.Time
}

type BaseCredentialManager interface {
	GetAPIKey(provider string) (string, error)
	GetProfile() string
	SetProfile(name string) error
	ListProfiles() []string
}

type SessionCredentialConfig struct {
	MaxTempTTL  time.Duration
	AuditLogger *AuditLogger
	BaseManager BaseCredentialManager
	SessionID   string
}

type SessionCredentialManager struct {
	mu              sync.RWMutex
	baseManager     BaseCredentialManager
	tempCredentials map[string]*TempCredential
	profileOverride string
	maxTempTTL      time.Duration
	auditLogger     *AuditLogger
	sessionID       string
	cleanupDone     bool
}

func NewSessionCredentialManager(cfg SessionCredentialConfig) *SessionCredentialManager {
	maxTTL := cfg.MaxTempTTL
	if maxTTL == 0 {
		maxTTL = DefaultMaxTempTTL
	}

	return &SessionCredentialManager{
		baseManager:     cfg.BaseManager,
		tempCredentials: make(map[string]*TempCredential),
		maxTempTTL:      maxTTL,
		auditLogger:     cfg.AuditLogger,
		sessionID:       cfg.SessionID,
	}
}

func (m *SessionCredentialManager) GetAPIKey(provider string) (string, error) {
	m.mu.RLock()
	temp, hasTemp := m.tempCredentials[provider]
	m.mu.RUnlock()

	if hasTemp {
		return m.resolveTempCredential(provider, temp)
	}

	return m.resolveFromBase(provider)
}

func (m *SessionCredentialManager) resolveTempCredential(provider string, temp *TempCredential) (string, error) {
	if time.Now().After(temp.ExpiresAt) {
		m.removeTempCredential(provider)
		m.logCredentialAccess(provider, SourceTemp, "expired")
		return "", ErrCredentialExpired
	}

	m.logCredentialAccess(provider, SourceTemp, "resolved")
	return temp.Value, nil
}

func (m *SessionCredentialManager) removeTempCredential(provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tempCredentials, provider)
}

func (m *SessionCredentialManager) resolveFromBase(provider string) (string, error) {
	if m.baseManager == nil {
		return "", ErrProviderNotConfigured
	}

	value, err := m.baseManager.GetAPIKey(provider)
	if err != nil {
		return "", err
	}

	source := SourceBase
	if m.profileOverride != "" {
		source = SourceProfile
	}

	m.logCredentialAccess(provider, source, "resolved")
	return value, nil
}

func (m *SessionCredentialManager) SetTemporaryCredential(provider, value string, ttl time.Duration) error {
	if ttl > m.maxTempTTL {
		return ErrTempCredentialMaxTTL
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.tempCredentials[provider] = &TempCredential{
		Provider:  provider,
		Value:     value,
		ExpiresAt: time.Now().Add(ttl),
		Source:    "manual",
		CreatedAt: time.Now(),
	}

	m.logTempCredentialSet(provider, ttl)
	return nil
}

func (m *SessionCredentialManager) RemoveTemporaryCredential(provider string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tempCredentials[provider]; exists {
		delete(m.tempCredentials, provider)
		return true
	}
	return false
}

func (m *SessionCredentialManager) SetProfileOverride(profile string) error {
	if m.baseManager == nil {
		return ErrProviderNotConfigured
	}

	profiles := m.baseManager.ListProfiles()
	if !containsProfile(profiles, profile) {
		return ErrProfileNotFound
	}

	m.mu.Lock()
	oldProfile := m.profileOverride
	m.profileOverride = profile
	m.mu.Unlock()

	if err := m.baseManager.SetProfile(profile); err != nil {
		m.mu.Lock()
		m.profileOverride = oldProfile
		m.mu.Unlock()
		return err
	}

	m.logProfileOverride(profile)
	return nil
}

func containsProfile(profiles []string, target string) bool {
	return slices.Contains(profiles, target)
}

func (m *SessionCredentialManager) ClearProfileOverride() {
	m.mu.Lock()
	m.profileOverride = ""
	m.mu.Unlock()
}

func (m *SessionCredentialManager) GetProfileOverride() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profileOverride
}

func (m *SessionCredentialManager) ClearSession() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cleanupDone {
		return
	}

	for provider, cred := range m.tempCredentials {
		m.zeroCredential(cred)
		delete(m.tempCredentials, provider)
	}

	m.profileOverride = ""
	m.cleanupDone = true

	m.logSessionCleanup()
}

func (m *SessionCredentialManager) zeroCredential(cred *TempCredential) {
	if cred == nil {
		return
	}

	bytes := []byte(cred.Value)
	for i := range bytes {
		bytes[i] = 0
	}
	cred.Value = ""
}

func (m *SessionCredentialManager) ListTempCredentials() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	providers := make([]string, 0, len(m.tempCredentials))
	for provider := range m.tempCredentials {
		providers = append(providers, provider)
	}
	return providers
}

func (m *SessionCredentialManager) GetTempCredentialInfo(provider string) (expiresAt time.Time, exists bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if cred, ok := m.tempCredentials[provider]; ok {
		return cred.ExpiresAt, true
	}
	return time.Time{}, false
}

func (m *SessionCredentialManager) CleanupExpired() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	removed := 0

	for provider, cred := range m.tempCredentials {
		if now.After(cred.ExpiresAt) {
			m.zeroCredential(cred)
			delete(m.tempCredentials, provider)
			removed++
		}
	}

	return removed
}

func (m *SessionCredentialManager) logCredentialAccess(provider string, source CredentialSource, outcome string) {
	if m.auditLogger == nil {
		return
	}

	entry := NewAuditEntry(AuditCategoryPermission, "credential_access", "access")
	entry.SessionID = m.sessionID
	entry.Target = provider
	entry.Outcome = outcome
	entry.Details = map[string]any{
		"source": string(source),
	}
	_ = m.auditLogger.Log(entry)
}

func (m *SessionCredentialManager) logTempCredentialSet(provider string, ttl time.Duration) {
	if m.auditLogger == nil {
		return
	}

	entry := NewAuditEntry(AuditCategoryPermission, "temp_credential_set", "set")
	entry.SessionID = m.sessionID
	entry.Target = provider
	entry.Details = map[string]any{
		"ttl_seconds": ttl.Seconds(),
	}
	_ = m.auditLogger.Log(entry)
}

func (m *SessionCredentialManager) logProfileOverride(profile string) {
	if m.auditLogger == nil {
		return
	}

	entry := NewAuditEntry(AuditCategoryConfig, "profile_override", "set")
	entry.SessionID = m.sessionID
	entry.Target = profile
	_ = m.auditLogger.Log(entry)
}

func (m *SessionCredentialManager) logSessionCleanup() {
	if m.auditLogger == nil {
		return
	}

	entry := NewAuditEntry(AuditCategorySession, "session_credentials_cleared", "cleanup")
	entry.SessionID = m.sessionID
	_ = m.auditLogger.Log(entry)
}
