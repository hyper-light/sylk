package container

import (
	"context"
	"fmt"
	"sync"
)

// AgentCreatorFunc creates a ContainerAgent for a given agent type.
// The context carries the parent lifecycle; factories should respect
// cancellation for long-running provider initialization.
type AgentCreatorFunc func(ctx context.Context) (ContainerAgent, error)

// AgentCreatorRegistry maps agent type strings to factory functions.
// Each factory creates a ContainerAgent for its agent type. Factories
// are registered during bootstrap, before the container runtime starts.
type AgentCreatorRegistry struct {
	mu       sync.RWMutex
	creators map[string]AgentCreatorFunc
}

// NewAgentCreatorRegistry creates an empty registry.
func NewAgentCreatorRegistry() *AgentCreatorRegistry {
	return &AgentCreatorRegistry{
		creators: make(map[string]AgentCreatorFunc),
	}
}

// Register adds a factory for the given agent type.
func (r *AgentCreatorRegistry) Register(agentType string, creator AgentCreatorFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creators[agentType] = creator
}

// Create invokes the registered factory for the given agent type.
func (r *AgentCreatorRegistry) Create(ctx context.Context, agentType string) (ContainerAgent, error) {
	r.mu.RLock()
	creator, ok := r.creators[agentType]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("agent_creator: no factory registered for %q", agentType)
	}
	return creator(ctx)
}

// Types returns the registered agent types as a stable snapshot.
func (r *AgentCreatorRegistry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.creators))
	for agentType := range r.creators {
		types = append(types, agentType)
	}
	return types
}

// Creator returns an AgentCreator function suitable for DefaultRuntimeConfig.
// It looks up the agent type in the registry and delegates to the registered
// factory. Returns an error if no factory is registered for the agent type.
func (r *AgentCreatorRegistry) Creator() AgentCreator {
	return func(ctx context.Context, agentType string) (ContainerAgent, error) {
		return r.Create(ctx, agentType)
	}
}
