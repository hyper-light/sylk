package engineer

import "github.com/adalundhe/sylk/agents/guide"

// EngineerRoutingInfo returns static routing metadata for the engineer
// agent using the provided canonical ID. This enables pre-registration
// with the Guide before the engineer container is activated.
func EngineerRoutingInfo(canonicalID string) *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      canonicalID,
		Type:    "engineer",
		Name:    "engineer",
		Aliases: []string{"eng", "impl", "code", "implement"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "implement",
				Description:   "Implement a coding task",
				DefaultIntent: guide.IntentComplete,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "code",
				Description:   "Write code for a specific feature or fix",
				DefaultIntent: guide.IntentComplete,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"implement", "code", "write", "create", "build",
				"fix", "refactor", "add feature", "modify", "update code",
			},
			WeakTriggers: []string{
				"function", "method", "class", "file", "module",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentComplete: {
					"implement", "code", "write", "create", "build",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "engineer",
			Aliases: []string{"eng", "impl", "code"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentComplete,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
					guide.DomainFiles,
				},
				Tags:     []string{"implementation", "code", "development", "testing"},
				Keywords: []string{"implement", "code", "write", "create", "build", "fix", "refactor", "test"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.7,
			},
			Description: "Staff-level implementation engineer. GPT-5.4 Pro with xhigh reasoning. Executes coding tasks with self-audit and consultation.",
			Priority:    70,
		},
	}
}
