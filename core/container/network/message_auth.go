package network

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
)

var (
	ErrMessageTampered  = errors.New("message HMAC verification failed")
	ErrNoSigningKey     = errors.New("no signing key configured")
)

// MessageAuthenticator provides HMAC-SHA256 message authentication for
// bus messages between containers. Each container has a signing key
// derived from its security context.
type MessageAuthenticator struct {
	mu   sync.RWMutex
	keys map[string][]byte // containerID → signing key
}

// NewMessageAuthenticator creates a message authenticator.
func NewMessageAuthenticator() *MessageAuthenticator {
	return &MessageAuthenticator{
		keys: make(map[string][]byte),
	}
}

// RegisterKey associates a signing key with a container ID.
func (ma *MessageAuthenticator) RegisterKey(containerID string, key []byte) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	cp := make([]byte, len(key))
	copy(cp, key)
	ma.keys[containerID] = cp
}

// UnregisterKey removes the signing key for a container.
func (ma *MessageAuthenticator) UnregisterKey(containerID string) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	delete(ma.keys, containerID)
}

// Sign produces an HMAC-SHA256 signature for the given message payload
// using the specified container's key.
func (ma *MessageAuthenticator) Sign(containerID string, payload []byte) (string, error) {
	key := ma.getKey(containerID)
	if key == nil {
		return "", ErrNoSigningKey
	}
	return computeHMAC(key, payload), nil
}

// Verify checks that the given signature matches the expected HMAC for
// the payload using the specified container's key.
func (ma *MessageAuthenticator) Verify(containerID string, payload []byte, signature string) error {
	key := ma.getKey(containerID)
	if key == nil {
		return ErrNoSigningKey
	}
	expected := computeHMAC(key, payload)
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return ErrMessageTampered
	}
	return nil
}

func (ma *MessageAuthenticator) getKey(containerID string) []byte {
	ma.mu.RLock()
	defer ma.mu.RUnlock()
	return ma.keys[containerID]
}

func computeHMAC(key, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// KeyCount returns the number of registered signing keys.
func (ma *MessageAuthenticator) KeyCount() int {
	ma.mu.RLock()
	defer ma.mu.RUnlock()
	return len(ma.keys)
}
