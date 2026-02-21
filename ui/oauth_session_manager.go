package ui

import (
	"context"
	"strings"
	"sync"
)

type oauthSessionState struct {
	flowID uint64
	cancel context.CancelFunc
}

type oauthSessionManager struct {
	mu              sync.Mutex
	nextFlowID      uint64
	currentProvider string
	sessions        map[string]oauthSessionState
}

func newOAuthSessionManager() *oauthSessionManager {
	return &oauthSessionManager{
		sessions: make(map[string]oauthSessionState),
	}
}

func (m *oauthSessionManager) Begin(provider string) uint64 {
	normalized := normalizeOAuthProvider(provider)
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cancelLocked(m.currentProvider)
	m.nextFlowID++
	m.currentProvider = normalized
	m.sessions[normalized] = oauthSessionState{flowID: m.nextFlowID}
	return m.nextFlowID
}

func (m *oauthSessionManager) Attach(provider string, flowID uint64, cancel context.CancelFunc) bool {
	normalized := normalizeOAuthProvider(provider)
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.sessions[normalized]
	if !ok || state.flowID != flowID {
		if cancel != nil {
			cancel()
		}
		return false
	}
	if state.cancel != nil {
		state.cancel()
	}
	state.cancel = cancel
	m.sessions[normalized] = state
	return true
}

func (m *oauthSessionManager) Complete(provider string, flowID uint64) bool {
	normalized := normalizeOAuthProvider(provider)
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.sessions[normalized]
	if !ok || state.flowID != flowID {
		return false
	}
	delete(m.sessions, normalized)
	if m.currentProvider == normalized {
		m.currentProvider = ""
	}
	return true
}

func (m *oauthSessionManager) Abort(provider string, flowID uint64, cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
	normalized := normalizeOAuthProvider(provider)
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.sessions[normalized]
	if !ok || state.flowID != flowID {
		return
	}
	delete(m.sessions, normalized)
	if m.currentProvider == normalized {
		m.currentProvider = ""
	}
}

func (m *oauthSessionManager) CancelCurrent() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelLocked(m.currentProvider)
	m.currentProvider = ""
}

func (m *oauthSessionManager) cancelLocked(provider string) {
	normalized := normalizeOAuthProvider(provider)
	state, ok := m.sessions[normalized]
	if !ok {
		return
	}
	if state.cancel != nil {
		state.cancel()
	}
	delete(m.sessions, normalized)
}

func normalizeOAuthProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
