package archivalist

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// AgentStatus represents the current state of a registered agent
type AgentStatus string

const (
	AgentStatusActive   AgentStatus = "active"
	AgentStatusIdle     AgentStatus = "idle"
	AgentStatusInactive AgentStatus = "inactive"
)

// RegisteredAgent represents an agent in the registry
type RegisteredAgent struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	SessionID    string       `json:"session_id"`
	ParentID     string       `json:"parent_id,omitempty"`
	Children     []string     `json:"children,omitempty"`
	Status       AgentStatus  `json:"status"`
	Clock        uint64       `json:"clock"`
	LastVersion  string       `json:"last_version"`
	RegisteredAt time.Time    `json:"registered_at"`
	LastSeenAt   time.Time    `json:"last_seen_at"`
	Source       SourceModel  `json:"source,omitempty"`
}

// IsSubAgent returns true if this agent has a parent
func (a *RegisteredAgent) IsSubAgent() bool {
	return a.ParentID != ""
}

// Registry manages agent registration, versioning, and hierarchy
type Registry struct {
	mu sync.RWMutex

	// Agent tracking
	agents    map[string]*RegisteredAgent // ID -> Agent
	byName    map[string]string           // Name -> ID (for lookup)
	bySession map[string][]string         // SessionID -> Agent IDs

	// Global Lamport clock
	globalClock atomic.Uint64

	// Version tracking
	currentVersion uint64
	versionPrefix  string

	// Configuration
	idleTimeout     time.Duration
	inactiveTimeout time.Duration
}

// RegistryConfig configures the agent registry
type RegistryConfig struct {
	VersionPrefix   string        // Prefix for version strings (default: "v")
	IdleTimeout     time.Duration // Time before agent marked idle (default: 5m)
	InactiveTimeout time.Duration // Time before agent marked inactive (default: 30m)
}

// NewRegistry creates a new agent registry
func NewRegistry(cfg RegistryConfig) *Registry {
	prefix := cfg.VersionPrefix
	if prefix == "" {
		prefix = "v"
	}

	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 5 * time.Minute
	}

	inactiveTimeout := cfg.InactiveTimeout
	if inactiveTimeout == 0 {
		inactiveTimeout = 30 * time.Minute
	}

	return &Registry{
		agents:          make(map[string]*RegisteredAgent),
		byName:          make(map[string]string),
		bySession:       make(map[string][]string),
		versionPrefix:   prefix,
		idleTimeout:     idleTimeout,
		inactiveTimeout: inactiveTimeout,
	}
}

// Register adds a new agent to the registry
func (r *Registry) Register(name, sessionID, parentID string, source SourceModel) (*RegisteredAgent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if name already registered in this session
	if existingID, ok := r.byName[name]; ok {
		existing := r.agents[existingID]
		if existing != nil && existing.SessionID == sessionID {
			// Reactivate existing agent
			existing.Status = AgentStatusActive
			existing.LastSeenAt = time.Now()
			return existing, nil
		}
	}

	// Validate parent if specified
	if parentID != "" {
		parent, ok := r.agents[parentID]
		if !ok {
			return nil, fmt.Errorf("parent agent %s not found", parentID)
		}
		if parent.SessionID != sessionID {
			return nil, fmt.Errorf("parent agent %s is in different session", parentID)
		}
	}

	// Generate short ID
	id := r.generateID(name)

	// Get current version
	version := r.currentVersionString()

	now := time.Now()
	agent := &RegisteredAgent{
		ID:           id,
		Name:         name,
		SessionID:    sessionID,
		ParentID:     parentID,
		Status:       AgentStatusActive,
		Clock:        r.globalClock.Load(),
		LastVersion:  version,
		RegisteredAt: now,
		LastSeenAt:   now,
		Source:       source,
	}

	// Store agent
	r.agents[id] = agent
	r.byName[name] = id
	r.bySession[sessionID] = append(r.bySession[sessionID], id)

	// Update parent's children list
	if parentID != "" {
		if parent := r.agents[parentID]; parent != nil {
			parent.Children = append(parent.Children, id)
		}
	}

	return agent, nil
}

// generateID creates a short unique ID for an agent
func (r *Registry) generateID(name string) string {
	// Use first char of name + incrementing number
	prefix := "a"
	if len(name) > 0 {
		prefix = string(name[0])
	}
	return fmt.Sprintf("%s%d", prefix, len(r.agents)+1)
}

// Get retrieves an agent by ID
func (r *Registry) Get(id string) *RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[id]
}

// GetByName retrieves an agent by name
func (r *Registry) GetByName(name string) *RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id, ok := r.byName[name]; ok {
		return r.agents[id]
	}
	return nil
}

// GetBySession returns all agents in a session
func (r *Registry) GetBySession(sessionID string) []*RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.bySession[sessionID]
	agents := make([]*RegisteredAgent, 0, len(ids))
	for _, id := range ids {
		if agent := r.agents[id]; agent != nil {
			agents = append(agents, agent)
		}
	}
	return agents
}

// Touch updates an agent's last seen time and increments clock
func (r *Registry) Touch(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[id]
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}

	agent.LastSeenAt = time.Now()
	agent.Status = AgentStatusActive
	agent.Clock = r.globalClock.Add(1)

	return nil
}

// UpdateVersion updates an agent's last known version
func (r *Registry) UpdateVersion(id, version string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[id]
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}

	agent.LastVersion = version
	agent.LastSeenAt = time.Now()
	agent.Clock = r.globalClock.Add(1)

	return nil
}

// IncrementGlobalClock increments and returns the new global clock value
func (r *Registry) IncrementGlobalClock() uint64 {
	return r.globalClock.Add(1)
}

// GetGlobalClock returns the current global clock value
func (r *Registry) GetGlobalClock() uint64 {
	return r.globalClock.Load()
}

// SyncClock synchronizes agent clock with incoming clock value (Lamport)
func (r *Registry) SyncClock(id string, incomingClock uint64) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[id]
	if !ok {
		return r.globalClock.Load()
	}

	// Lamport clock rule: max(local, incoming) + 1
	current := r.globalClock.Load()
	newClock := max(current, incomingClock) + 1
	r.globalClock.Store(newClock)
	agent.Clock = newClock

	return newClock
}

// IncrementVersion increments the global version and returns new version string
func (r *Registry) IncrementVersion() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.currentVersion++
	return r.currentVersionString()
}

// GetVersion returns the current version string
func (r *Registry) GetVersion() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.currentVersionString()
}

// GetVersionNumber returns the current version number
func (r *Registry) GetVersionNumber() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.currentVersion
}

func (r *Registry) currentVersionString() string {
	return fmt.Sprintf("%s%d", r.versionPrefix, r.currentVersion)
}

// ParseVersion extracts version number from version string
func (r *Registry) ParseVersion(v string) (uint64, error) {
	var num uint64
	_, err := fmt.Sscanf(v, r.versionPrefix+"%d", &num)
	if err != nil {
		return 0, fmt.Errorf("invalid version format: %s", v)
	}
	return num, nil
}

// IsVersionCurrent checks if a version is current
func (r *Registry) IsVersionCurrent(v string) bool {
	num, err := r.ParseVersion(v)
	if err != nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return num == r.currentVersion
}

// GetVersionDelta returns the difference between a version and current
func (r *Registry) GetVersionDelta(v string) (int64, error) {
	num, err := r.ParseVersion(v)
	if err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(r.currentVersion) - int64(num), nil
}

// Unregister removes an agent from the registry
func (r *Registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[id]
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}

	r.removeFromParent(agent, id)
	r.unregisterChildren(agent)
	r.cleanupAgentMaps(agent, id)
	return nil
}

func (r *Registry) removeFromParent(agent *RegisteredAgent, id string) {
	if agent.ParentID == "" {
		return
	}
	parent := r.agents[agent.ParentID]
	if parent == nil {
		return
	}
	parent.Children = removeStringFromSlice(parent.Children, id)
}

func (r *Registry) unregisterChildren(agent *RegisteredAgent) {
	for _, childID := range agent.Children {
		delete(r.agents, childID)
		r.removeFromByName(childID)
	}
}

func (r *Registry) removeFromByName(agentID string) {
	for name, id := range r.byName {
		if id == agentID {
			delete(r.byName, name)
			return
		}
	}
}

func (r *Registry) cleanupAgentMaps(agent *RegisteredAgent, id string) {
	delete(r.agents, id)
	delete(r.byName, agent.Name)
	r.bySession[agent.SessionID] = removeStringFromSlice(r.bySession[agent.SessionID], id)
}

func removeStringFromSlice(slice []string, item string) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

// UpdateStatuses updates agent statuses based on timeouts
func (r *Registry) UpdateStatuses() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for _, agent := range r.agents {
		timeSinceLastSeen := now.Sub(agent.LastSeenAt)

		if timeSinceLastSeen > r.inactiveTimeout {
			agent.Status = AgentStatusInactive
		} else if timeSinceLastSeen > r.idleTimeout {
			agent.Status = AgentStatusIdle
		}
	}
}

// GetActiveAgents returns all active agents
func (r *Registry) GetActiveAgents() []*RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var active []*RegisteredAgent
	for _, agent := range r.agents {
		if agent.Status == AgentStatusActive {
			active = append(active, agent)
		}
	}
	return active
}

// GetAgentHierarchy returns the full hierarchy for an agent
func (r *Registry) GetAgentHierarchy(id string) []*RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[id]
	if !ok {
		return nil
	}

	// Build path from root to this agent
	var hierarchy []*RegisteredAgent

	// Walk up to root
	current := agent
	for current != nil {
		hierarchy = append([]*RegisteredAgent{current}, hierarchy...)
		if current.ParentID == "" {
			break
		}
		current = r.agents[current.ParentID]
	}

	return hierarchy
}

// GetRootAgent returns the root agent for a given agent
func (r *Registry) GetRootAgent(id string) *RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[id]
	if !ok {
		return nil
	}

	// Walk up to root
	current := agent
	for current.ParentID != "" {
		parent, ok := r.agents[current.ParentID]
		if !ok {
			break
		}
		current = parent
	}

	return current
}

// GetDescendants returns all descendants of an agent
func (r *Registry) GetDescendants(id string) []*RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[id]
	if !ok {
		return nil
	}

	var descendants []*RegisteredAgent
	r.collectDescendants(agent, &descendants)
	return descendants
}

func (r *Registry) collectDescendants(agent *RegisteredAgent, result *[]*RegisteredAgent) {
	for _, childID := range agent.Children {
		if child := r.agents[childID]; child != nil {
			*result = append(*result, child)
			r.collectDescendants(child, result)
		}
	}
}

// Stats returns registry statistics
type RegistryStats struct {
	TotalAgents   int                    `json:"total_agents"`
	ActiveAgents  int                    `json:"active_agents"`
	IdleAgents    int                    `json:"idle_agents"`
	InactiveAgents int                   `json:"inactive_agents"`
	SessionCount  int                    `json:"session_count"`
	GlobalClock   uint64                 `json:"global_clock"`
	CurrentVersion string                `json:"current_version"`
	AgentsBySession map[string]int       `json:"agents_by_session"`
}

// GetStats returns current registry statistics
func (r *Registry) GetStats() RegistryStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := RegistryStats{
		TotalAgents:     len(r.agents),
		SessionCount:    len(r.bySession),
		GlobalClock:     r.globalClock.Load(),
		CurrentVersion:  r.currentVersionString(),
		AgentsBySession: make(map[string]int),
	}

	for _, agent := range r.agents {
		switch agent.Status {
		case AgentStatusActive:
			stats.ActiveAgents++
		case AgentStatusIdle:
			stats.IdleAgents++
		case AgentStatusInactive:
			stats.InactiveAgents++
		}
	}

	for sessionID, agents := range r.bySession {
		stats.AgentsBySession[sessionID] = len(agents)
	}

	return stats
}
