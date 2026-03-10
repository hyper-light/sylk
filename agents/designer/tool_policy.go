package designer

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func designerVisibleSkillNames() []string {
	return []string{
		"search_skills",
		"component_search",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"component_create",
		"component_modify",
		"ask_user_clarification",
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
		"component_create",
		"component_modify",
		"request_engineer_review",
		"request_inspector_check",
		"request_tester_validation",
		"ask_user_clarification",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
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
