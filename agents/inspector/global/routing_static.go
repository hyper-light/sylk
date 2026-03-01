package global

import "github.com/adalundhe/sylk/agents/guide"

// InspectorRoutingInfo returns static routing metadata for the global
// inspector agent using the provided canonical ID. This enables
// pre-registration with the Guide before the inspector container is activated.
func InspectorRoutingInfo(canonicalID string) *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      canonicalID,
		Type:    "inspector",
		Name:    "Inspector",
		Aliases: []string{"global-inspector", "audit", "quality-audit"},
		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "audit",
				Description:   "Audit code quality across files",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "chat",
				Description:   "Conversational interaction",
				DefaultIntent: guide.IntentChat,
				DefaultDomain: guide.DomainCode,
			},
		},
		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"audit", "cross-file", "architectural review",
				"plan adherence", "quality audit",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {"audit", "inspect", "review quality"},
			},
		},
		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "Inspector",
			Aliases: []string{"global-inspector", "audit"},
			Capabilities: guide.AgentCapabilities{
				Intents:  []guide.Intent{guide.IntentCheck, guide.IntentRecall, guide.IntentHelp, guide.IntentChat},
				Domains:  []guide.Domain{guide.DomainCode},
				Tags:     []string{"audit", "quality", "cross-file", "architecture", "plan"},
				Keywords: []string{"audit", "inspect", "quality", "cross-file", "plan", "architecture"},
				Priority: 75,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Global quality inspector. Cross-file architectural auditing, plan adherence validation, and DAG layer gating.",
			Priority:    75,
		},
	}
}
