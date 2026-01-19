package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	keyPrefix      = "domain"
	keySeparator   = ":"
	maxKeyLength   = 256
	truncateLength = 64
)

// KeyGenerator creates cache keys for domain classification queries.
type KeyGenerator struct {
	prefix    string
	namespace string
}

// NewKeyGenerator creates a new KeyGenerator with the given namespace.
func NewKeyGenerator(namespace string) *KeyGenerator {
	return &KeyGenerator{
		prefix:    keyPrefix,
		namespace: namespace,
	}
}

// Generate creates a cache key for a query string.
func (kg *KeyGenerator) Generate(query string) string {
	normalized := normalizeForKey(query)
	hash := hashQuery(normalized)

	return kg.buildKey(hash)
}

// GenerateWithContext creates a cache key that includes context information.
func (kg *KeyGenerator) GenerateWithContext(query, sessionID string) string {
	normalized := normalizeForKey(query)
	combined := normalized + keySeparator + sessionID
	hash := hashQuery(combined)

	return kg.buildKey(hash)
}

func (kg *KeyGenerator) buildKey(hash string) string {
	parts := []string{kg.prefix}

	if kg.namespace != "" {
		parts = append(parts, kg.namespace)
	}

	parts = append(parts, hash)

	return strings.Join(parts, keySeparator)
}

func normalizeForKey(query string) string {
	normalized := strings.ToLower(strings.TrimSpace(query))

	normalized = strings.Join(strings.Fields(normalized), " ")

	if len(normalized) > maxKeyLength {
		normalized = normalized[:maxKeyLength]
	}

	return normalized
}

func hashQuery(query string) string {
	h := sha256.Sum256([]byte(query))
	return hex.EncodeToString(h[:])[:truncateLength]
}

// ParseKey extracts components from a cache key.
func ParseKey(key string) (prefix, namespace, hash string, ok bool) {
	parts := strings.Split(key, keySeparator)

	if len(parts) < 2 {
		return "", "", "", false
	}

	if parts[0] != keyPrefix {
		return "", "", "", false
	}

	prefix = parts[0]

	if len(parts) == 2 {
		hash = parts[1]
		return prefix, "", hash, true
	}

	namespace = parts[1]
	hash = parts[2]
	return prefix, namespace, hash, true
}

// SetNamespace updates the namespace for key generation.
func (kg *KeyGenerator) SetNamespace(namespace string) {
	kg.namespace = namespace
}

// GetNamespace returns the current namespace.
func (kg *KeyGenerator) GetNamespace() string {
	return kg.namespace
}

// DefaultKeyGenerator returns a KeyGenerator with default settings.
func DefaultKeyGenerator() *KeyGenerator {
	return NewKeyGenerator("")
}
