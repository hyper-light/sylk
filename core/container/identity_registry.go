package container

import (
	"sync"

	"github.com/google/uuid"
)

// AgentIdentityRegistry generates and stores canonical UUID IDs per agent
// type. IDs are pure UUIDs (no type prefix) — required for the direct
// communication protocol where agents address each other by ID alone.
// IDs are sticky — the same canonical ID is returned across spin-up
// and spin-down cycles. Thread-safe; bounded to one entry per agent type.
//
// The registry also maintains a reverse index (ID → type) so callers can
// resolve an agent's type from its opaque ID without parsing conventions.
type AgentIdentityRegistry struct {
	mu      sync.RWMutex
	ids     map[string]string // agentType -> canonical UUID8
	reverse map[string]string // canonical UUID8 -> agentType
}

// NewAgentIdentityRegistry creates a registry and pre-generates canonical
// IDs for the given agent types. Each type gets exactly one stable ID for
// the lifetime of the registry.
func NewAgentIdentityRegistry(agentTypes []string) *AgentIdentityRegistry {
	r := &AgentIdentityRegistry{
		ids:     make(map[string]string, len(agentTypes)),
		reverse: make(map[string]string, len(agentTypes)),
	}
	for _, t := range agentTypes {
		id := generateCanonicalID()
		r.ids[t] = id
		r.reverse[id] = t
	}
	return r
}

// Get returns the canonical UUID8 for the given agent type.
func (r *AgentIdentityRegistry) Get(agentType string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.ids[agentType]
	return id, ok
}

// TypeOf returns the agent type for a canonical ID.
// Returns ("", false) when the ID is not recognized.
func (r *AgentIdentityRegistry) TypeOf(canonicalID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.reverse[canonicalID]
	return t, ok
}

// generateCanonicalID produces a pure UUID8 with no type prefix.
// Example: "a1b2c3d4".
func generateCanonicalID() string {
	return uuid.New().String()[:8]
}
