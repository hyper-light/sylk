package pipeline

import (
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineInspectorVisibleSkillNames() []string {
	base := agentShared.AppendMemoryForestVisibleSkillNames([]string{
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
		"run_command",
		"run_shell_script",
		"define_criteria",
		"get_validation_status",
		"research_dependency_install",
		"install_dependency_tooling",
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
	}, "inspector-pipeline")
	// Inspector gets BOTH the awareness skills AND the audit-time
	// inspect_open_activity. The audit skill is what makes the
	// inspector's role at finalize time actually inspect open
	// fabric activity (challenges, holds, hot scopes, decisions).
	return fabric.AppendFabricInspectorSkillNames(base)
}

func pipelineInspectorMutatingSkillNames() []string {
	return agentShared.AppendMemoryForestMutatingSkillNames([]string{
		"run_command",
		"run_shell_script",
		"define_criteria",
		"request_correction",
		"request_override",
		"install_dependency_tooling",
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
	})
}

func pipelineInspectorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "inspector-pipeline",
		CapabilityScope:  "inspector.pipeline",
		Registry:         registry,
		VisibleByDefault: agentShared.FilterRegisteredSkillNames(registry, pipelineInspectorVisibleSkillNames()),
		Mutating:         agentShared.FilterRegisteredSkillNames(registry, pipelineInspectorMutatingSkillNames()),
	})
}
