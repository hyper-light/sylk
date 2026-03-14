package designer

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func designerVisibleSkillNames() []string {
	return []string{
		"search_skills",
		"read_file",
		"prepare_pipeline_write_context",
		"diff_workspace_file",
		"list_pipeline_changes",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"component_search",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"component_create",
		"component_modify",
		"ask_user_clarification",
		"handoff_next",
		"validate_work",
		"process_validation",
		"coord_query_view",
		"coord_watch_updates",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
	}
}

func designerMutatingSkillNames() []string {
	return []string{
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"request_engineer_review",
		"request_inspector_check",
		"request_tester_validation",
		"ask_user_clarification",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
		"handoff_next",
		"validate_work",
		"process_validation",
		"report_to_engineer",
		"report_to_orchestrator",
		"reroute_request",
	}
}

func designerToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "designer",
		CapabilityScope:  "designer.default",
		Registry:         registry,
		VisibleByDefault: designerVisibleSkillNames(),
		Mutating:         designerMutatingSkillNames(),
	})
}
