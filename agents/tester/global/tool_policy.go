package global

import (
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func globalTesterVisibleSkillNames() []string {
	base := agentshared.AppendMemoryForestVisibleSkillNames([]string{
		"analyze_risk",
		"plan_tests",
		// Phase 2.K / CR-2: 12 workspace skills collapsed into 3 verbs.
		"workspace_read",
		"workspace_write",
		"bash",
		"challenge_inspector",
		"handoff_next",
		"validate_work",
		"process_validation",
		"write_test",
		"run_test_suite",
		"diagnose_failure",
		// Phase 2.K / GT-4 + GI-5: collapsed into dependency(action=…, category=test).
		"dependency",
		// Phase 2.K / GT-A: single unified escalation primitive.
		"escalate_failure",
		"ask_user_clarification",
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"query_board",
		"inspect_claim_conflicts",
		"traverse",
	}, "tester")
	return fabric.AppendFabricAwarenessSkillNames(base)
}

func globalTesterMutatingSkillNames() []string {
	return agentshared.AppendMemoryForestMutatingSkillNames([]string{
		// Phase 2.K / CR-2: 4 write skills collapsed into workspace_write.
		"workspace_write",
		"bash",
		"challenge_inspector",
		"handoff_next",
		"validate_work",
		"process_validation",
		// Phase 2.K / GT-2 refactor: write_integration_test +
		// write_e2e_test collapsed into write_test(level=…).
		"write_test",
		"run_test_suite",
		"build_harness",
		"dependency",
		// Phase 2.K / GT-A: report_to_orchestrator + report_to_architect
		// + escalate_failure collapsed into escalate_failure(targets=[…]).
		"escalate_failure",
		"reroute_request",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
	})
}

func globalTesterToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "tester",
		CapabilityScope:  "tester.global",
		Registry:         registry,
		VisibleByDefault: agentshared.FilterRegisteredSkillNames(registry, globalTesterVisibleSkillNames()),
		Mutating:         agentshared.FilterRegisteredSkillNames(registry, globalTesterMutatingSkillNames()),
	})
}
