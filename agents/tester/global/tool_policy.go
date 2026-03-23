package global

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func globalTesterVisibleSkillNames() []string {
	return []string{
		"analyze_risk",
		"plan_tests",
		"read_file",
		"prepare_global_write_context",
		"diff_workspace_file",
		"list_global_changes",
		"write_global_file",
		"edit_global_file",
		"delete_global_file",
		"create_global_directory",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"run_command",
		"run_shell_script",
		"write_test",
		"run_test_suite",
		"diagnose_failure",
		"research_test_tool_install",
		"install_test_tooling",
	}
}

func globalTesterMutatingSkillNames() []string {
	return []string{
		"write_global_file",
		"edit_global_file",
		"delete_global_file",
		"create_global_directory",
		"run_command",
		"run_shell_script",
		"write_test",
		"run_test_suite",
		"build_harness",
		"install_test_tooling",
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
