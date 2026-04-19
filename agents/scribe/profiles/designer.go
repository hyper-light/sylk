package profiles

import (
	"github.com/adalundhe/sylk/agents/scribe"
	"github.com/adalundhe/sylk/core/activity"
)

func init() {
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "designer",
		PromptModule: "designer",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionChallengeEmitted,
			activity.ActionScopePartitioned,
		},
		OutputSchema: map[string]string{
			"components":          "Components created or modified.",
			"design_tokens":       "Tokens validated, suggested, or applied.",
			"a11y_outcome":        "Accessibility audit results.",
			"framework_choice":    "UI framework decisions made.",
			"design_consistency":  "Cross-component consistency observations.",
		},
	})
}
