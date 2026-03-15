package pipeline

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineTesterVisibleSkillNames() []string {
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
		"detect_test_harness",
		"prepare_test_harness",
		"analyze_risk",
		"plan_tests",
		"research_test_tool_install",
		"install_test_tooling",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"write_test",
		"run_test_suite",
		"diagnose_failure",
		"report_to_engineer",
		"report_to_designer",
		"challenge_agent",
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

func pipelineTesterMutatingSkillNames() []string {
	return []string{
		"write_test",
		"install_test_tooling",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"run_test_suite",
		"report_to_engineer",
		"report_to_designer",
		"challenge_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		"coord_claim_scope",
		"coord_release_scope",
		"coord_publish_artifact",
		"coord_request_review",
		"coord_resolve_artifact",
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
