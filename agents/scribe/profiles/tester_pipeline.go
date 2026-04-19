package profiles

import (
	"github.com/adalundhe/sylk/agents/scribe"
	"github.com/adalundhe/sylk/core/activity"
)

func init() {
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "tester-pipeline",
		PromptModule: "tester_pipeline",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionChallengeEmitted,
			activity.ActionScopePartitioned,
			activity.ActionEscalationRequested,
		},
		OutputSchema: map[string]string{
			"framework":         "Test framework in use this batch.",
			"tests_authored":    "Tests written this batch with their target files.",
			"suite_outcome":     "Run summary: pass count, fail count, duration, regressions.",
			"coverage_delta":    "Coverage gained or lost.",
			"diagnosis":         "If failures occurred — root-cause hypothesis.",
		},
		PrecedentRules: []scribe.PrecedentRule{
			{
				Description: "Cross-pipeline scope-split resolved a Charter conflict in <3 messages — template for framework disputes",
				Predicate:   precedentTesterScopeSplit,
			},
		},
	})

	// Global tester reuses the pipeline tester output schema with
	// global-scoped emphasize kinds.
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "tester",
		PromptModule: "tester_global",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionEscalationRequested,
		},
		OutputSchema: map[string]string{
			"global_suite_outcome": "Whole-session test outcomes.",
			"integration_status":   "Cross-pipeline integration test status.",
			"e2e_status":           "End-to-end test status.",
		},
	})
}

func precedentTesterScopeSplit(_ map[string]any, batch []activity.AgentActivity) bool {
	for _, a := range batch {
		if a.Action == activity.ActionScopePartitioned {
			return true
		}
	}
	return false
}
