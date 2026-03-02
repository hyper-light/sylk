package guardian

import "github.com/adalundhe/sylk/agents/guide"

// GuardianRoutingInfo returns static routing metadata for the guardian
// agent using the provided canonical ID. This enables pre-registration
// with the Guide before the guardian container is activated.
func GuardianRoutingInfo(canonicalID string) *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      canonicalID,
		Type:    "guardian",
		Name:    "guardian",
		Aliases: []string{"guard", "safety"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "safety",
				Description:   "Run safety checks on recent changes",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCompliance,
			},
			{
				Name:          "checkpoint",
				Description:   "Create a safety checkpoint of current state",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainSystem,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"safety",
				"security",
				"checkpoint",
				"protect",
				"credential",
				"injection",
				"rollback",
				"guardian",
			},
			WeakTriggers: []string{
				"commit",
				"health",
				"budget",
				"token usage",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {
					"safety",
					"security",
					"checkpoint",
					"protect",
					"credential",
					"injection",
				},
				guide.IntentStatus: {
					"health",
					"budget",
					"token usage",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "guardian",
			Aliases: []string{"guard", "safety"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentCheck,
					guide.IntentStatus,
					guide.IntentHelp,
					guide.IntentChat,
				},
				Domains: []guide.Domain{
					guide.DomainSystem,
					guide.DomainCompliance,
				},
				Tags:     []string{"safety", "security", "checkpoint", "guardian", "protect"},
				Keywords: []string{"safety", "security", "checkpoint", "guardian", "protect", "credential", "injection", "health", "rollback"},
				Priority: 95,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Safety sidecar — git mutation gating, content validation, credential detection, agent health monitoring.",
			Priority:    95,
		},
	}
}

// GetRoutingInfo implements guide.AgentRouter.
func (g *Guardian) GetRoutingInfo() *guide.AgentRoutingInfo {
	return GuardianRoutingInfo(g.id)
}
