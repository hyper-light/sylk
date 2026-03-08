package container

import (
	"strings"
	"sync"
)

// AgentIdentityRegistry generates and stores canonical IDs per agent type.
// For singleton specialists, the canonical ID is the stable human-readable
// agent type itself (for example, "architect" or "librarian"). That keeps
// Guide pre-registration, activation, routing, and UI identity aligned on a
// single name across spin-up and spin-down cycles.
//
// The registry also maintains a reverse index (ID -> type) so callers can
// resolve an agent's type from its canonical ID without additional lookup.
type AgentIdentityRegistry struct {
	mu      sync.RWMutex
	ids     map[string]string // agentType -> canonical ID
	reverse map[string]string // canonical ID -> agentType
}

// NewAgentIdentityRegistry creates a registry and pre-generates canonical
// IDs for the given agent types. Each type gets exactly one stable ID for
// the lifetime of the registry.
func NewAgentIdentityRegistry(agentTypes []string) *AgentIdentityRegistry {
	r := &AgentIdentityRegistry{
		ids:     make(map[string]string, len(agentTypes)),
		reverse: make(map[string]string, len(agentTypes)),
	}
	for _, rawType := range agentTypes {
		agentType := strings.TrimSpace(rawType)
		if agentType == "" {
			continue
		}
		if _, exists := r.ids[agentType]; exists {
			continue
		}
		r.ids[agentType] = agentType
		r.reverse[agentType] = agentType
	}
	return r
}

// Get returns the canonical ID for the given agent type.
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
