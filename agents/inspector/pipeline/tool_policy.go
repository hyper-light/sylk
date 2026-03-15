package pipeline

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineInspectorVisibleSkillNames() []string {
	return []string{
		"search_skills",
		"read_file",
		"diff_workspace_file",
		"list_pipeline_changes",
		"read_workspace_file",
		"glob",
		"workspace_glob",
		"grep",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"define_criteria",
		"get_validation_status",
		"challenge_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		"finalize_pipeline",
		"handoff_to_ot",
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
		"request_correction",
		"request_override",
		"challenge_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		"finalize_pipeline",
		"handoff_to_ot",
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
