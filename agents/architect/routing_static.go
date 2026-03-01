package architect

import "github.com/adalundhe/sylk/agents/guide"

// ArchitectRoutingInfo returns static routing metadata for the architect
// agent using the provided canonical ID. This enables pre-registration
// with the Guide before the architect container is activated.
func ArchitectRoutingInfo(canonicalID string) *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      canonicalID,
		Type:    "architect",
		Name:    "architect",
		Aliases: []string{"arch", "planner", "designer"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "plan",
				Description:   "Create a design plan with atomic tasks and workflow DAG",
				DefaultIntent: guide.IntentPlan,
				DefaultDomain: guide.DomainDesign,
			},
			{
				Name:          "design",
				Description:   "Design system architecture",
				DefaultIntent: guide.IntentDesign,
				DefaultDomain: guide.DomainDesign,
			},
			{
				Name:          "decompose",
				Description:   "Decompose requirements into atomic tasks",
				DefaultIntent: guide.IntentPlan,
				DefaultDomain: guide.DomainTasks,
			},
			{
				Name:          "execute",
				Description:   "Execute the current plan",
				DefaultIntent: guide.IntentPlan,
				DefaultDomain: guide.DomainTasks,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"plan",
				"design",
				"architect",
				"decompose",
				"break down",
				"create workflow",
				"task generation",
				"orchestrate",
				"coordinate",
				"structure",
				"execute plan",
				"go ahead",
				"start execution",
				"run the plan",
			},
			WeakTriggers: []string{
				"implement",
				"build",
				"create",
				"develop",
				"organize",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentPlan: {
					"plan",
					"design",
					"create workflow",
					"break down",
					"decompose",
					"execute plan",
					"go ahead",
					"start execution",
					"run the plan",
				},
				guide.IntentDesign: {
					"architect",
					"structure",
					"design",
					"organize",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "architect",
			Aliases: []string{"arch", "planner", "designer"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentPlan,
					guide.IntentDesign,
					guide.IntentExecute,
					guide.IntentRecall,
					guide.IntentCheck,
					guide.IntentHelp,
				},
				Domains: []guide.Domain{
					guide.DomainDesign,
					guide.DomainTasks,
				},
				Tags:     []string{"planning", "design", "architecture", "tasks", "workflow"},
				Keywords: []string{"plan", "design", "architect", "decompose", "workflow", "dag", "tasks"},
				Priority: 90,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalFuture,
				MinConfidence: 0.6,
			},
			Description: "System design and planning specialist. Creates atomic tasks and workflow DAGs using Pre-Delegation Planning Protocol.",
			Priority:    90,
		},
	}
}
