package profiles

import (
	"github.com/adalundhe/sylk/agents/scribe"
	"github.com/adalundhe/sylk/core/activity"
)

func init() {
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "orchestrator",
		PromptModule: "orchestrator",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionDAGIngested,
			activity.ActionDAGAccepted,
			activity.ActionEscalationRequested,
			activity.ActionValidationRejected,
		},
		OutputSchema: map[string]string{
			"critical_path":      "Tasks on the critical path this batch.",
			"coordination":       "Cross-agent coordination events (claims, artifacts, reviews).",
			"interventions":      "Steering or override actions taken.",
			"layer_status":       "DAG layer dispatch and completion status.",
			"validation_status":  "Validation epoch transitions.",
		},
	})
}
