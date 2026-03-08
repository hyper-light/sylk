package global

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func globalTesterVisibleSkillNames() []string {
	return []string{
		"analyze_risk",
		"plan_tests",
		"write_test",
		"run_test_suite",
		"diagnose_failure",
	}
}

func globalTesterMutatingSkillNames() []string {
	return []string{
		"write_test",
		"run_test_suite",
		"build_harness",
		"write_integration_test",
		"write_e2e_test",
		"report_to_orchestrator",
		"report_to_architect",
		"escalate_failure",
		"reroute_request",
	}
}

func globalTesterToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "tester",
		CapabilityScope:  "tester.global",
		Registry:         registry,
		VisibleByDefault: globalTesterVisibleSkillNames(),
		Mutating:         globalTesterMutatingSkillNames(),
	})
}
