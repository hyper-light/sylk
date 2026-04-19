// Package profiles registers per-agent ScribeProfile values via init().
// Importing this subpackage from cmd/tui.go (or anywhere else) is the
// extension hook for new agent types — registration is declarative
// and the scribe registry consults ProfileFor() at runtime.
package profiles

import (
	"github.com/adalundhe/sylk/agents/scribe"
	"github.com/adalundhe/sylk/core/activity"
)

func init() {
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "architect",
		PromptModule: "architect",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionPlanProposed,
			activity.ActionPlanRatified,
			activity.ActionCharterRatified,
			activity.ActionEscalationRequested,
		},
		OutputSchema: map[string]string{
			"plan_delta":    "Plan-level changes since the last narration.",
			"dependencies":  "Cross-agent dependencies introduced or resolved.",
			"assumptions":   "Assumptions surfaced or invalidated.",
			"handoff_delta": "What downstream agents need to inherit.",
		},
		PrecedentRules: []scribe.PrecedentRule{
			{
				Description: "Charter ratification — high-quality precedent for project-shaping decisions",
				Predicate:   precedentArchitectCharterRatified,
			},
			{
				Description: "Plan recovered cleanly after stall — template for future stall recovery",
				Predicate:   precedentArchitectPlanRecovered,
			},
		},
	})
}

func precedentArchitectCharterRatified(_ map[string]any, batch []activity.AgentActivity) bool {
	for _, a := range batch {
		if a.Action == activity.ActionCharterRatified {
			return true
		}
	}
	return false
}

func precedentArchitectPlanRecovered(_ map[string]any, batch []activity.AgentActivity) bool {
	sawProposed := false
	sawRatified := false
	for _, a := range batch {
		if a.Action == activity.ActionPlanProposed {
			sawProposed = true
		}
		if a.Action == activity.ActionPlanRatified && sawProposed {
			sawRatified = true
		}
	}
	return sawRatified
}
