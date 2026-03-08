package pipeline

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineTesterVisibleSkillNames() []string {
	return []string{
		"analyze_risk",
		"plan_tests",
		"write_test",
		"run_test_suite",
		"diagnose_failure",
	}
}

func pipelineTesterMutatingSkillNames() []string {
	return []string{
		"write_test",
		"run_test_suite",
		"report_to_engineer",
		"report_to_designer",
		"reroute_request",
	}
}

func pipelineTesterToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "tester-pipeline",
		CapabilityScope:  "tester.pipeline",
		Registry:         registry,
		VisibleByDefault: pipelineTesterVisibleSkillNames(),
		Mutating:         pipelineTesterMutatingSkillNames(),
	})
}
