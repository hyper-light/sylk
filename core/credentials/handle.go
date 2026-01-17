package credentials

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const (
	HandleLifetime  = 30 * time.Second
	HandleIDLength  = 16
	CleanupInterval = 10 * time.Second
)

var (
	ErrHandleNotFound = errors.New("credential handle not found")
	ErrHandleExpired  = errors.New("credential handle expired")
	ErrHandleUsed     = errors.New("credential handle already used")
	ErrHandleInvalid  = errors.New("credential handle invalid")
)

type CredentialHandle struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	AgentID    string    `json:"agent_id"`
	AgentType  string    `json:"agent_type"`
	ToolCallID string    `json:"tool_call_id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Used       bool      `json:"used"`
	value      string
}

func NewCredentialHandle(provider, agentID, agentType, toolCallID, value string) *CredentialHandle {
	now := time.Now()
	return &CredentialHandle{
		ID:         generateHandleID(),
		Provider:   provider,
		AgentID:    agentID,
		AgentType:  agentType,
		ToolCallID: toolCallID,
		CreatedAt:  now,
		ExpiresAt:  now.Add(HandleLifetime),
		Used:       false,
		value:      value,
	}
}

func generateHandleID() string {
	b := make([]byte, HandleIDLength)
	rand.Read(b)
	return "cred_" + hex.EncodeToString(b)
}

func (h *CredentialHandle) IsExpired() bool {
	return time.Now().After(h.ExpiresAt)
}

func (h *CredentialHandle) IsValid() bool {
	return !h.Used && !h.IsExpired()
}

func (h *CredentialHandle) Consume() (string, error) {
	if h.Used {
		return "", ErrHandleUsed
	}
	if h.IsExpired() {
		return "", ErrHandleExpired
	}
	h.Used = true
	return h.value, nil
}

type HandleStore struct {
	mu      sync.RWMutex
	handles map[string]*CredentialHandle
	stopCh  chan struct{}
}

func NewHandleStore() *HandleStore {
	hs := &HandleStore{
		handles: make(map[string]*CredentialHandle),
		stopCh:  make(chan struct{}),
	}
	go hs.cleanupLoop()
	return hs
}

func (hs *HandleStore) Store(handle *CredentialHandle) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.handles[handle.ID] = handle
}

func (hs *HandleStore) Get(id string) (*CredentialHandle, error) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	handle, ok := hs.handles[id]
	if !ok {
		return nil, ErrHandleNotFound
	}
	return handle, nil
}

func (hs *HandleStore) Resolve(id string) (string, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	handle, ok := hs.handles[id]
	if !ok {
		return "", ErrHandleNotFound
	}

	value, err := handle.Consume()
	if err != nil {
		return "", err
	}

	return value, nil
}

func (hs *HandleStore) Revoke(id string) bool {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if _, ok := hs.handles[id]; ok {
		delete(hs.handles, id)
		return true
	}
	return false
}

func (hs *HandleStore) cleanupLoop() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hs.cleanupExpired()
		case <-hs.stopCh:
			return
		}
	}
}

func (hs *HandleStore) cleanupExpired() {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	now := time.Now()
	for id, handle := range hs.handles {
		if now.After(handle.ExpiresAt) {
			delete(hs.handles, id)
		}
	}
}

func (hs *HandleStore) Stop() {
	close(hs.stopCh)
}

func (hs *HandleStore) Count() int {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	return len(hs.handles)
}

func (hs *HandleStore) ActiveHandles() []*CredentialHandle {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	var active []*CredentialHandle
	for _, h := range hs.handles {
		if h.IsValid() {
			active = append(active, h)
		}
	}
	return active
}
