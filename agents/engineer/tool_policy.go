package engineer

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func engineerVisibleSkillNames() []string {
	return []string{
		"search_skills",
		"read_file",
		"read_workspace_file",
		"prepare_pipeline_write_context",
		"diff_workspace_file",
		"list_pipeline_changes",
		"lsp",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"glob",
		"workspace_glob",
		"grep",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"discover_project_tools",
		"discover_code_patterns",
		"format",
		"lint",
		"consult",
		"research_dependency_install",
		"install_dependency_tooling",
		"coord_query_view",
		"coord_watch_updates",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
		"challenge_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		"audit",
		"run_command",
		"run_shell_script",
		"report_confidence",
		"signal_orchestrator",
	}
}

func engineerMutatingSkillNames() []string {
	return []string{
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"run_command",
		"run_shell_script",
		"install_dependency_tooling",
		"format",
		"consult",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
		"challenge_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		"signal_orchestrator",
		"report_confidence",
		"reroute_request",
	}
}

func engineerToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "engineer",
		CapabilityScope:  "engineer.default",
		Registry:         registry,
		VisibleByDefault: engineerVisibleSkillNames(),
		Mutating:         engineerMutatingSkillNames(),
	})
}
