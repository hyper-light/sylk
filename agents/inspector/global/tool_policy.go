package global

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func globalInspectorVisibleSkillNames() []string {
	return []string{
		"run_linter",
		"run_type_checker",
		"run_security_scan",
		"read_file",
		"diff_workspace_file",
		"list_global_changes",
		"read_workspace_file",
		"glob",
		"workspace_glob",
		"grep",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"run_command",
		"run_shell_script",
		"audit_layer",
		"challenge_global_tester",
		"challenge_orchestrator",
		"challenge_architect",
		"process_global_validation",
		"finalize_global_review",
		"commit_to_disk",
		"validate_plan_adherence",
		"cross_reference_changes",
		"grade_layer_quality",
		"load_plan_context",
		"consult_librarian_style",
		"consult_academic_approach",
		"consult_archivalist_context",
		"request_architect_research",
		"request_user_clarification",
		"escalate_findings",
		"research_dependency_install",
		"install_dependency_tooling",
	}
}

func globalInspectorMutatingSkillNames() []string {
	return []string{
		"run_command",
		"run_shell_script",
		"challenge_global_tester",
		"challenge_orchestrator",
		"challenge_architect",
		"process_global_validation",
		"finalize_global_review",
		"commit_to_disk",
		"install_dependency_tooling",
		"request_architect_research",
		"request_user_clarification",
		"escalate_findings",
		"reroute_request",
	}
}

func globalInspectorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "inspector",
		CapabilityScope:  "inspector.global",
		Registry:         registry,
		VisibleByDefault: globalInspectorVisibleSkillNames(),
		Mutating:         globalInspectorMutatingSkillNames(),
	})
}
