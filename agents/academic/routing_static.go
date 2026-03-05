package academic

import "github.com/adalundhe/sylk/agents/guide"

// AcademicRoutingInfo returns static routing metadata for the academic
// agent. Used for pre-registration with the Guide before the container
// is activated.
func AcademicRoutingInfo(canonicalID string) *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      canonicalID,
		Type:    "academic",
		Name:    "academic",
		Aliases: []string{"research", "papers"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "research",
				Description:   "Research best practices, patterns, and academic papers",
				DefaultIntent: guide.IntentRecall,
				DefaultDomain: guide.DomainResearch,
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "academic",
			Aliases: []string{"research", "papers"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{guide.IntentRecall},
				Domains: []guide.Domain{guide.DomainResearch},
			},
			Description: "Researches best practices, academic papers, and external knowledge sources, " +
				"validating recommendations against codebase reality via the Librarian.",
		},
	}
}

// GetRoutingInfo implements guide.AgentRouter.
func (a *Academic) GetRoutingInfo() *guide.AgentRoutingInfo {
	return AcademicRoutingInfo(a.id)
}
