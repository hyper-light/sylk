package pipeline

import (
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineTesterVisibleSkillNames() []string {
	base := agentshared.AppendMemoryForestVisibleSkillNames([]string{
		"search_skills",
		// Phase 2.K / CR-2: 12 workspace skills collapsed into 3 verbs.
		"workspace_read",
		"workspace_write",
		"test_harness",
		"analyze_risk",
		"plan_tests",
		// Phase 2.K / GT-4 + GI-5: collapsed into dependency(action=…, category=test).
		"dependency",
		"bash",
		"write_test",
		"run_test_suite",
		"diagnose_failure",
		"declare_decision",
		"ask_user_clarification",
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"query_board",
		"recall_forward",
		"inspect_claim_conflicts",
		"traverse",
		"carry_forward",
	}, "tester-pipeline")
	// Activity Fabric: awareness + cross-pipeline + recall must be
	// in the LLM's default tool catalog so the ambient model is
	// reachable without keyword matching. See SCRIBE_FABRIC.md and
	// the screenshot review — without this, agents default to the
	// older direct skills (query_decisions, coord_query_view) and
	// never reach the fabric.
	return fabric.AppendFabricAwarenessSkillNames(base)
}

func pipelineTesterMutatingSkillNames() []string {
	return agentshared.AppendMemoryForestMutatingSkillNames([]string{
		"write_test",
		"dependency",
		// Phase 2.K / CR-2: 4 write skills collapsed into workspace_write.
		"workspace_write",
		"bash",
		"run_test_suite",
		"finalize_pipeline",
		"declare_decision",
		"reroute_request",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
		"carry_forward",
	})
}

func pipelineTesterToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "tester-pipeline",
		CapabilityScope:  "tester.pipeline",
		Registry:         registry,
		VisibleByDefault: agentshared.FilterRegisteredSkillNames(registry, pipelineTesterVisibleSkillNames()),
		Mutating:         agentshared.FilterRegisteredSkillNames(registry, pipelineTesterMutatingSkillNames()),
	})
}
