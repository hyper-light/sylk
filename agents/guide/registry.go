package guide

import (
	"strings"
	"sync"
)

// =============================================================================
// Registry Implementation
// =============================================================================

// Registry is a thread-safe implementation of AgentRegistry
type Registry struct {
	mu sync.RWMutex

	// Agents by ID
	agents map[string]*AgentRegistration

	// Name/alias to ID mapping for fast lookup
	nameIndex map[string]string
}

// NewRegistry creates a new agent registry
func NewRegistry() *Registry {
	return &Registry{
		agents:    make(map[string]*AgentRegistration),
		nameIndex: make(map[string]string),
	}
}

// Register adds or updates an agent registration
func (r *Registry) Register(registration *AgentRegistration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove old name mappings if updating
	if old, exists := r.agents[registration.ID]; exists {
		delete(r.nameIndex, strings.ToLower(old.Name))
		for _, alias := range old.Aliases {
			delete(r.nameIndex, strings.ToLower(alias))
		}
	}

	// Store registration
	r.agents[registration.ID] = registration

	// Index by name and aliases
	r.nameIndex[strings.ToLower(registration.Name)] = registration.ID
	for _, alias := range registration.Aliases {
		r.nameIndex[strings.ToLower(alias)] = registration.ID
	}
}

// Unregister removes an agent by ID
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if agent, exists := r.agents[id]; exists {
		// Remove name mappings
		delete(r.nameIndex, strings.ToLower(agent.Name))
		for _, alias := range agent.Aliases {
			delete(r.nameIndex, strings.ToLower(alias))
		}

		// Remove agent
		delete(r.agents, id)
	}
}

// Get retrieves an agent by ID
func (r *Registry) Get(id string) *AgentRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.agents[id]
}

// GetByName retrieves an agent by name or alias
func (r *Registry) GetByName(name string) *AgentRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if id, ok := r.nameIndex[strings.ToLower(name)]; ok {
		return r.agents[id]
	}
	return nil
}

// FindBestMatch finds the best agent for a route result
func (r *Registry) FindBestMatch(result *RouteResult) *AgentRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var best *AgentRegistration
	bestScore := 0

	for _, agent := range r.agents {
		score := agent.MatchScore(result)
		if score > bestScore {
			bestScore = score
			best = agent
		}
	}

	return best
}

// GetAll returns all registered agents
func (r *Registry) GetAll() []*AgentRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*AgentRegistration, 0, len(r.agents))
	for _, agent := range r.agents {
		result = append(result, agent)
	}
	return result
}

// =============================================================================
// Default Agent Registrations
// =============================================================================

// GuideRegistration returns the registration for the Guide agent.
// Other agents should define their own registrations in their routing.go files
// and register via RegisterFromRoutingInfo.
func GuideRegistration() *AgentRegistration {
	return &AgentRegistration{
		ID:      "guide",
		Name:    "guide",
		Aliases: []string{"g"},
		Capabilities: AgentCapabilities{
			Intents: []Intent{
				IntentHelp,
				IntentStatus,
			},
			Domains: []Domain{
				DomainSystem,
				DomainAgents,
			},
		},
		Constraints: AgentConstraints{
			// Guide handles any temporal focus for its domains
		},
		Description: "Intent-based routing, help, and system status",
		Priority:    50,
	}
}

// NewRegistryWithDefaults creates a registry with only the Guide registered.
// Other agents should register themselves via RegisterFromRoutingInfo.
func NewRegistryWithDefaults() *Registry {
	registry := NewRegistry()
	registry.Register(GuideRegistration())
	return registry
}

// RegisterFromRoutingInfo registers an agent from its AgentRoutingInfo.
// This is the preferred way for agents to register with the Guide.
func (r *Registry) RegisterFromRoutingInfo(info *AgentRoutingInfo) {
	if info == nil || info.Registration == nil {
		return
	}
	r.Register(info.Registration)
}
