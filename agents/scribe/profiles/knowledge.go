package profiles

import (
	"github.com/adalundhe/sylk/agents/scribe"
	"github.com/adalundhe/sylk/core/activity"
)

func init() {
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "librarian",
		PromptModule: "librarian",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionAdvisoryEmitted,
			activity.ActionProactiveAdvisory,
		},
		OutputSchema: map[string]string{
			"patterns":           "Patterns surfaced this batch.",
			"recommendations":    "Recommendations made to peer agents.",
			"proactive_pushes":   "Proactive advisories pushed to specific peers.",
			"knowledge_grounded": "Sources grounded in (forest, document DB, fabric).",
		},
	})

	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "academic",
		PromptModule: "academic",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionAdvisoryEmitted,
			activity.ActionProactiveAdvisory,
		},
		OutputSchema: map[string]string{
			"thesis":           "Thesis or analytic position offered this batch.",
			"findings":         "Findings from research consultations.",
			"tradeoffs":        "Tradeoffs surfaced.",
			"handoff_delta":    "What downstream agents need from the academic's contribution.",
		},
	})

	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "archivalist",
		PromptModule: "archivalist",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionAdvisoryEmitted,
			activity.ActionPrecedentEmitted,
		},
		OutputSchema: map[string]string{
			"ingestions":      "Entries ingested this batch.",
			"recall_outcomes": "Recall queries served and their hits.",
			"compactions":     "Session-store compactions or L1→L2 archives.",
			"graph_updates":   "Knowledge graph entity / relationship updates.",
		},
	})
}
