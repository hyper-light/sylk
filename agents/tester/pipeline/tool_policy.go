package pipeline

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineTesterVisibleSkillNames() []string {
	return []string{
		"search_skills",
		"check_inspector_gate",
		"detect_test_harness",
		"prepare_test_harness",
		"analyze_risk",
		"plan_tests",
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
		"run_test_suite",
		"report_to_engineer",
		"report_to_designer",
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
