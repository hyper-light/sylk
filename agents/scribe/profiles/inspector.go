package profiles

import (
	"github.com/adalundhe/sylk/agents/scribe"
	"github.com/adalundhe/sylk/core/activity"
)

func init() {
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "inspector-pipeline",
		PromptModule: "inspector_pipeline",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionEscalationRequested,
			activity.ActionScopePartitioned,
		},
		OutputSchema: map[string]string{
			"criteria":          "Validation criteria defined or applied this batch.",
			"issues":            "Issues found and their severity.",
			"corrections":       "Correction requests issued and their resolution status.",
			"acceptance_status": "Whether this batch produced an acceptance, hold, or rejection.",
			"audit_findings":    "Cross-pipeline activity flagged at audit time.",
		},
		PrecedentRules: []scribe.PrecedentRule{
			{
				Description: "Inspector audit caught divergence and drove resolution — template for cross-pipeline reconciliation",
				Predicate:   precedentInspectorAuditResolved,
			},
		},
	})

	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "inspector",
		PromptModule: "inspector_global",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionEscalationRequested,
		},
		OutputSchema: map[string]string{
			"audit_depth":         "Depth of audit performed (light, standard, deep).",
			"global_findings":     "Cross-session quality findings.",
			"pipeline_validation": "Per-pipeline validation outcomes.",
			"checkpoint_status":   "Checkpoint accept / reject / commit decisions.",
		},
	})
}

func precedentInspectorAuditResolved(_ map[string]any, batch []activity.AgentActivity) bool {
	sawCorrection := false
	sawResolution := false
	for _, a := range batch {
		if a.Action == activity.ActionChallengeEmitted {
			sawCorrection = true
		}
		if a.Action == activity.ActionChallengeResponse && sawCorrection {
			sawResolution = true
		}
	}
	return sawResolution
}
