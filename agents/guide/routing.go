package guide

// =============================================================================
// Agent Routing Interface
// =============================================================================
//
// Agents implement this interface to register their routing information with
// the Guide. This allows agents to define their own:
// - DSL aliases (e.g., "arch" -> "archivalist")
// - Action shortcuts (e.g., "@archive" -> route to archivalist)
// - Trigger phrases for natural language detection
//
// The Guide aggregates these from all registered agents to build its routing tables.
//
// Usage:
//   // Agent implements AgentRouter interface
//   func (a *MyAgent) GetRoutingInfo() *guide.AgentRoutingInfo { ... }
//
//   // Register with Guide
//   guide.Register(myAgent.GetRoutingInfo())
//   // Or: guide.RegisterRouter(myAgent)

// AgentRouter defines the routing information an agent provides to the Guide.
// Agents implement this interface to register their routing capabilities.
type AgentRouter interface {
	// GetRoutingInfo returns the agent's routing configuration
	GetRoutingInfo() *AgentRoutingInfo
}

// AgentRoutingInfo contains all routing configuration for an agent.
// This is returned by AgentRouter.GetRoutingInfo().
type AgentRoutingInfo struct {
	// Agent identity
	ID   string `json:"id"`
	Name string `json:"name"`

	// DSL aliases (e.g., "arch" -> "archivalist")
	// Maps short alias to full agent name
	Aliases []string `json:"aliases,omitempty"`

	// Action shortcuts (e.g., "archive" triggers routing to this agent)
	// When user types @archive, routes to this agent
	ActionShortcuts []ActionShortcut `json:"action_shortcuts,omitempty"`

	// Trigger phrases for natural language detection
	// These help the Guide decide when to route to this agent
	Triggers AgentTriggers `json:"triggers,omitempty"`

	// Agent capabilities and constraints
	Registration *AgentRegistration `json:"registration"`
}

// ActionShortcut defines a shortcut command that routes to this agent
type ActionShortcut struct {
	// The shortcut name (without @). E.g., "archive" for @archive
	Name string `json:"name"`

	// Description for help text
	Description string `json:"description,omitempty"`

	// Default intent when this shortcut is used (optional)
	DefaultIntent Intent `json:"default_intent,omitempty"`

	// Default domain when this shortcut is used (optional)
	DefaultDomain Domain `json:"default_domain,omitempty"`
}

// AgentTriggers contains trigger phrases for natural language routing
type AgentTriggers struct {
	// Phrases that strongly suggest routing to this agent
	StrongTriggers []string `json:"strong_triggers,omitempty"`

	// Phrases that weakly suggest this agent (may need confirmation)
	WeakTriggers []string `json:"weak_triggers,omitempty"`

	// Intent-specific triggers (map of intent -> trigger phrases)
	IntentTriggers map[Intent][]string `json:"intent_triggers,omitempty"`
}

// =============================================================================
// Routing Aggregator
// =============================================================================

// RoutingAggregator collects routing info from multiple agents
type RoutingAggregator struct {
	// Maps agent name/alias to agent ID
	aliasIndex map[string]string

	// Maps action shortcut to agent ID
	actionIndex map[string]string

	// Collected triggers by agent
	triggersByAgent map[string]*AgentTriggers

	// All registered routing info
	routingInfo map[string]*AgentRoutingInfo
}

// NewRoutingAggregator creates a new routing aggregator
func NewRoutingAggregator() *RoutingAggregator {
	return &RoutingAggregator{
		aliasIndex:      make(map[string]string),
		actionIndex:     make(map[string]string),
		triggersByAgent: make(map[string]*AgentTriggers),
		routingInfo:     make(map[string]*AgentRoutingInfo),
	}
}

// RegisterAgent registers an agent's routing information
func (ra *RoutingAggregator) RegisterAgent(info *AgentRoutingInfo) {
	if info == nil {
		return
	}

	// Store routing info
	ra.routingInfo[info.ID] = info

	// Index aliases
	ra.aliasIndex[info.Name] = info.ID
	ra.aliasIndex[info.ID] = info.ID
	for _, alias := range info.Aliases {
		ra.aliasIndex[alias] = info.ID
	}

	// Index action shortcuts
	for _, shortcut := range info.ActionShortcuts {
		ra.actionIndex[shortcut.Name] = info.ID
	}

	// Store triggers
	ra.triggersByAgent[info.ID] = &info.Triggers
}

// UnregisterAgent removes an agent's routing information
func (ra *RoutingAggregator) UnregisterAgent(id string) {
	info, ok := ra.routingInfo[id]
	if !ok {
		return
	}

	// Remove alias index entries
	delete(ra.aliasIndex, info.Name)
	delete(ra.aliasIndex, info.ID)
	for _, alias := range info.Aliases {
		delete(ra.aliasIndex, alias)
	}

	// Remove action shortcuts
	for _, shortcut := range info.ActionShortcuts {
		delete(ra.actionIndex, shortcut.Name)
	}

	// Remove triggers
	delete(ra.triggersByAgent, id)

	// Remove routing info
	delete(ra.routingInfo, id)
}

// ResolveAgent resolves an agent name or alias to the full agent ID
func (ra *RoutingAggregator) ResolveAgent(nameOrAlias string) (string, bool) {
	id, ok := ra.aliasIndex[nameOrAlias]
	return id, ok
}

// ResolveAction resolves an action shortcut to agent ID
func (ra *RoutingAggregator) ResolveAction(action string) (string, bool) {
	id, ok := ra.actionIndex[action]
	return id, ok
}

// GetActionShortcut returns the action shortcut details for an action
func (ra *RoutingAggregator) GetActionShortcut(action string) (*ActionShortcut, string, bool) {
	agentID, ok := ra.actionIndex[action]
	if !ok {
		return nil, "", false
	}

	info := ra.routingInfo[agentID]
	if info == nil {
		return nil, agentID, true
	}

	for _, shortcut := range info.ActionShortcuts {
		if shortcut.Name == action {
			return &shortcut, agentID, true
		}
	}

	return nil, agentID, true
}

// GetAllAliases returns all registered aliases with their agent IDs
func (ra *RoutingAggregator) GetAllAliases() map[string]string {
	result := make(map[string]string, len(ra.aliasIndex))
	for alias, id := range ra.aliasIndex {
		result[alias] = id
	}
	return result
}

// GetAllActions returns all registered action shortcuts
func (ra *RoutingAggregator) GetAllActions() map[string]string {
	result := make(map[string]string, len(ra.actionIndex))
	for action, id := range ra.actionIndex {
		result[action] = id
	}
	return result
}

// GetAgentTriggers returns triggers for a specific agent
func (ra *RoutingAggregator) GetAgentTriggers(agentID string) *AgentTriggers {
	return ra.triggersByAgent[agentID]
}

// GetAllStrongTriggers returns all strong triggers from all agents
func (ra *RoutingAggregator) GetAllStrongTriggers() map[string][]string {
	result := make(map[string][]string)
	for id, triggers := range ra.triggersByAgent {
		if triggers != nil && len(triggers.StrongTriggers) > 0 {
			result[id] = triggers.StrongTriggers
		}
	}
	return result
}

// GetRoutingInfo returns routing info for a specific agent
func (ra *RoutingAggregator) GetRoutingInfo(agentID string) *AgentRoutingInfo {
	return ra.routingInfo[agentID]
}

// GetAllRoutingInfo returns all registered routing info
func (ra *RoutingAggregator) GetAllRoutingInfo() []*AgentRoutingInfo {
	result := make([]*AgentRoutingInfo, 0, len(ra.routingInfo))
	for _, info := range ra.routingInfo {
		result = append(result, info)
	}
	return result
}

// =============================================================================
// Guide Routing Info
// =============================================================================

// GuideRoutingInfo returns the Guide's own routing information.
// The Guide registers itself so it can be routed to for help/status queries.
func GuideRoutingInfo() *AgentRoutingInfo {
	return &AgentRoutingInfo{
		ID:      "guide",
		Name:    "guide",
		Aliases: []string{"g"},

		// Action shortcuts for direct Guide invocation
		ActionShortcuts: []ActionShortcut{
			{
				Name:          "help",
				Description:   "Get help with routing, DSL syntax, or agents",
				DefaultIntent: IntentHelp,
				DefaultDomain: DomainSystem,
			},
			{
				Name:          "status",
				Description:   "Check system and agent status",
				DefaultIntent: IntentStatus,
				DefaultDomain: DomainAgents,
			},
		},

		// Triggers for Guide-specific queries
		Triggers: AgentTriggers{
			StrongTriggers: GuideTriggers,
			IntentTriggers: map[Intent][]string{
				IntentHelp: {
					"how do i",
					"help with",
					"what is",
					"explain",
				},
				IntentStatus: {
					"status",
					"health",
					"registered",
					"available",
				},
			},
		},

		Registration: GuideRegistration(),
	}
}
