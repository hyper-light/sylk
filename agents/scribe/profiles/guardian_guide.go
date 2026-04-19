package profiles

import (
	"github.com/adalundhe/sylk/agents/scribe"
	"github.com/adalundhe/sylk/core/activity"
)

func init() {
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "guardian",
		PromptModule: "guardian",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionCommandDenied,
		},
		OutputSchema: map[string]string{
			"assessments":   "Commands assessed this batch with their decisions.",
			"decisions":     "Approval / denial breakdown.",
			"approvals":     "Specific approvals issued, with rationale.",
			"denials":       "Specific denials issued, with rationale and scope.",
		},
	})

	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "guide",
		PromptModule: "guide",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionRouteClassified,
			activity.ActionSteeringEmitted,
		},
		OutputSchema: map[string]string{
			"classifications": "Routing classifications made this batch.",
			"steering":        "Steering actions taken.",
			"routing_path":    "Notable routes taken or denied.",
		},
	})
}
