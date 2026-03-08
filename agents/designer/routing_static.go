package designer

import "github.com/adalundhe/sylk/agents/guide"

// DesignerRoutingInfo returns static routing metadata for the designer
// agent using the provided canonical ID. This enables pre-registration
// with the Guide before the designer container is activated.
func DesignerRoutingInfo(canonicalID string) *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      canonicalID,
		Type:    "designer",
		Name:    "designer",
		Aliases: []string{"design", "ui", "ux", "frontend"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "design",
				Description:   "Design a UI component or layout",
				DefaultIntent: guide.IntentDesign,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "component",
				Description:   "Create or modify a UI component",
				DefaultIntent: guide.IntentDesign,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "a11y",
				Description:   "Run accessibility audit",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"design",
				"component",
				"ui",
				"ux",
				"layout",
				"style",
				"accessible",
				"accessibility",
				"a11y",
				"wcag",
				"color contrast",
				"design token",
				"responsive",
			},
			WeakTriggers: []string{
				"button",
				"form",
				"modal",
				"dialog",
				"input",
				"card",
				"navigation",
				"header",
				"footer",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentDesign: {
					"design",
					"create component",
					"build ui",
					"layout",
					"style",
				},
				guide.IntentCheck: {
					"audit",
					"accessibility",
					"a11y",
					"contrast",
					"wcag",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "designer",
			Aliases: []string{"design", "ui", "ux"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentDesign,
					guide.IntentComplete,
					guide.IntentCheck,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
					guide.DomainFiles,
				},
				Tags:     []string{"ui", "ux", "design", "accessibility", "components", "frontend"},
				Keywords: []string{"design", "component", "ui", "ux", "style", "layout", "a11y", "accessible", "wcag"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.7,
			},
			Description:           "UI/UX design specialist powered by Gemini 3.1 Pro Preview. LLM-driven 6-phase protocol for accessible, performant UI implementation.",
			Priority:              70,
			RuntimeProfiles:       designerRuntimeProfiles(),
			DefaultRuntimeProfile: designerDefaultRuntimeProfile(),
		},
	}
}
