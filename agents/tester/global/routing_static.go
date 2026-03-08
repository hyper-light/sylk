package global

import "github.com/adalundhe/sylk/agents/guide"

// TesterRoutingInfo returns static routing metadata for the global
// tester agent using the provided canonical ID. This enables
// pre-registration with the Guide before the tester container is activated.
func TesterRoutingInfo(canonicalID string) *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      canonicalID,
		Type:    "tester",
		Name:    "Tester",
		Aliases: []string{"test", "testing", "qa"},
		ActionShortcuts: []guide.ActionShortcut{
			{Name: "test", Description: "Run tests", DefaultIntent: guide.IntentCheck, DefaultDomain: guide.DomainCode},
			{Name: "coverage", Description: "Coverage report", DefaultIntent: guide.IntentCheck, DefaultDomain: guide.DomainCode},
			{Name: "chat", Description: "Conversational interaction", DefaultIntent: guide.IntentChat, DefaultDomain: guide.DomainSystem},
		},
		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"test", "testing", "coverage", "mutation",
				"integration test", "e2e test", "cross-pipeline",
			},
			WeakTriggers: []string{"verify", "check", "quality"},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentCheck: {"test", "run tests", "coverage", "integration test"},
			},
		},
		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "Tester",
			Aliases: []string{"test", "testing", "qa"},
			Capabilities: guide.AgentCapabilities{
				Intents:  []guide.Intent{guide.IntentCheck, guide.IntentRecall, guide.IntentHelp, guide.IntentChat},
				Domains:  []guide.Domain{guide.DomainCode},
				Tags:     []string{"testing", "quality", "coverage", "integration", "e2e", "sdet"},
				Keywords: []string{"test", "integration", "e2e", "coverage", "mutation", "quality"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description:           "Cross-pipeline SDET. Architects integration/e2e test strategies with 7-phase LLM-driven protocol.",
			Priority:              70,
			RuntimeProfiles:       testerRuntimeProfiles(),
			DefaultRuntimeProfile: testerDefaultRuntimeProfile(),
		},
	}
}
