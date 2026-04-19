package profiles

import (
	"github.com/adalundhe/sylk/agents/scribe"
	"github.com/adalundhe/sylk/core/activity"
)

func init() {
	scribe.RegisterProfile(scribe.ScribeProfile{
		AgentType:    "engineer",
		PromptModule: "engineer",
		EmphasizeKinds: []activity.ActionKind{
			activity.ActionEscalationRequested,
			activity.ActionChallengeEmitted,
			activity.ActionScopePartitioned,
		},
		OutputSchema: map[string]string{
			"files":               "Files authored or modified this batch.",
			"tests":               "Tests written or executed.",
			"implementation_delta": "What changed in the implementation surface.",
			"build_state":         "Build / lint / format outcomes.",
		},
		PrecedentRules: []scribe.PrecedentRule{
			{
				Description: "First commit on a new build backend — pattern for build tooling adoption",
				Predicate:   precedentEngineerFirstBuildBackendCommit,
			},
		},
	})
}

func precedentEngineerFirstBuildBackendCommit(_ map[string]any, batch []activity.AgentActivity) bool {
	sawBuildBackend := false
	sawArtifact := false
	for _, a := range batch {
		if a.Action == activity.ActionDecisionDeclared && a.Subject.Domain == "build_backend" && a.Confidence == activity.ConfidenceCommitted {
			sawBuildBackend = true
		}
		if a.Action == activity.ActionArtifactPublished {
			sawArtifact = true
		}
	}
	return sawBuildBackend && sawArtifact
}
