package pipeline

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineInspectorVisibleSkillNames() []string {
	return []string{
		"search_skills",
		"run_linter",
		"run_type_checker",
		"run_security_scan",
		"read_file",
		"prepare_pipeline_write_context",
		"diff_workspace_file",
		"list_pipeline_changes",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"read_workspace_file",
		"glob",
		"workspace_glob",
		"grep",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"define_criteria",
		"validate_criteria",
		"grade_task_quality",
		"get_validation_status",
		"coord_query_view",
		"coord_watch_updates",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
	}
}

func pipelineInspectorMutatingSkillNames() []string {
	return []string{
		"define_criteria",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"request_correction",
		"request_override",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
		"reroute_request",
	}
}

func pipelineInspectorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "inspector-pipeline",
		CapabilityScope:  "inspector.pipeline",
		Registry:         registry,
		VisibleByDefault: pipelineInspectorVisibleSkillNames(),
		Mutating:         pipelineInspectorMutatingSkillNames(),
	})
}
