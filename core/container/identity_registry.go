package container

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// AgentIdentityRegistry generates and stores canonical UUID8 IDs per agent
// type. IDs are sticky — the same canonical ID is returned across spin-up
// and spin-down cycles. Thread-safe; bounded to one entry per agent type.
type AgentIdentityRegistry struct {
	mu  sync.RWMutex
	ids map[string]string // agentType -> canonical UUID8
}

// NewAgentIdentityRegistry creates a registry and pre-generates canonical
// IDs for the given agent types. Each type gets exactly one stable ID for
// the lifetime of the registry.
func NewAgentIdentityRegistry(agentTypes []string) *AgentIdentityRegistry {
	r := &AgentIdentityRegistry{ids: make(map[string]string, len(agentTypes))}
	for _, t := range agentTypes {
		r.ids[t] = generateCanonicalID(t)
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

// generateCanonicalID produces a stable-format UUID8: "<type>_<uuid8>".
// Example: "engineer_a1b2c3d4".
func generateCanonicalID(agentType string) string {
	return fmt.Sprintf("%s_%s", agentType, uuid.New().String()[:8])
}
