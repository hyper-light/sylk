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
			{
				Name:          "fetch",
				Description:   "Fetch a remote resource for research (web page, document, repository)",
				DefaultIntent: guide.IntentFetch,
				DefaultDomain: guide.DomainResearch,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"research", "paper", "best practice", "documentation",
				"fetch url", "fetch document", "download document",
			},
			WeakTriggers: []string{
				"compare", "approach", "methodology", "standard",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentRecall: {
					"research", "best practice", "compare", "approach",
				},
				guide.IntentFetch: {
					"fetch url", "fetch document", "download document",
					"fetch page", "crawl",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "academic",
			Aliases: []string{"research", "papers"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{guide.IntentRecall, guide.IntentFetch},
				Domains: []guide.Domain{guide.DomainResearch},
				Keywords: []string{
					"research", "best practice", "recommend", "compare",
					"fetch", "download", "url", "web", "crawl", "document",
				},
			},
			Description: "Researches best practices, academic papers, and external knowledge sources. " +
				"Can fetch web pages and documents. Validates against codebase reality via the Librarian.",
			RuntimeProfiles:       academicRuntimeProfiles(),
			DefaultRuntimeProfile: academicDefaultRuntimeProfile(),
		},
	}
}

// GetRoutingInfo implements guide.AgentRouter.
func (a *Academic) GetRoutingInfo() *guide.AgentRoutingInfo {
	return AcademicRoutingInfo(a.id)
}
